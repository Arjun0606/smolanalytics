// Package insight produces the proactive "what's broken / what to look at" digest
// — the verdict founders actually want instead of a dashboard. Every finding is
// computed exactly from the deterministic engine, so it can't be hallucinated.
// Shared by the dashboard, the /v1/notable API, the MCP tool, and the daily brief.
package insight

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/retention"
)

// Kinds name the DETECTOR behind a finding. Downstream surfaces (the fix brief, the cloud's
// fix-PR runner) branch on these. They must never branch on Title: Title is a sentence written
// for a human and gets reworded whenever the wording improves, which would silently break a
// consumer in another repository with nothing failing loudly.
const (
	KindDropoff   = "funnel_dropoff"
	KindSegment   = "segment"
	KindAnomaly   = "anomaly"
	KindTrend     = "trend"
	KindRetention = "retention"
	KindAIVis     = "ai_visibility"
)

// Finding is one notable thing, ranked by severity ("warn" before "info").
type Finding struct {
	Severity string `json:"severity"` // warn | info
	Title    string `json:"title"`
	Detail   string `json:"detail"`

	// The machine half. Every value below was already in the detector's hand when it wrote the
	// sentence above, so acting on a finding never means re-reading its prose.
	Kind   string   `json:"kind"`
	Metric string   `json:"metric,omitempty"` // the event this is about
	Steps  []string `json:"steps,omitempty"`  // the funnel it was computed over
	From   string   `json:"from,omitempty"`   // the step users reach
	To     string   `json:"to,omitempty"`     // the step they fail to reach
	Prop   string   `json:"prop,omitempty"`   // the segment property being blamed
	Value  string   `json:"value,omitempty"`  // the segment value being blamed
	Rate   int      `json:"rate,omitempty"`   // the headline percentage (signed for deltas)
	N      int      `json:"n,omitempty"`      // the base the rate is computed over
}

// Fingerprint is a finding's identity ACROSS PROCESSES: sha1 of the lowercased, trimmed title,
// first 12 hex chars. The cloud's fix-PR runner derives its branch name and its idempotency key
// with the exact same recipe (lib/finding-id.ts). If the two ever diverge, a brief, the email
// link that opens it and the PR opened from it stop being about the same thing — and nothing
// errors. Do not change this on one side.
func (f Finding) Fingerprint() string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(f.Title))))
	return hex.EncodeToString(sum[:])[:12]
}

// minSample is the floor for any rate/percentage finding: below this base count
// the finding is suppressed outright. "activate jumped 50%" when it went 2→3 is
// noise, and shipping noise as a verdict costs trust with exactly the low-traffic
// products this digest serves.
const minSample = 20

// smallSample is the base under which a surviving rate finding carries an explicit
// qualifier, so the reader can weigh a swing on n=34 against one on n=3400.
const smallSample = 100

// qualify appends the small-sample note when the base clears the floor but is
// still thin enough that the percentage deserves a caveat.
func qualify(detail string, n int) string {
	if n < smallSample {
		return fmt.Sprintf("%s (n=%d, small sample)", detail, n)
	}
	return detail
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Generate returns the digest: the biggest funnel leak, the headline event's
// week-over-week change, and the retention read — computed exactly.
// Generate detects the journey itself. Use it where there is no page context: the CLI, the
// morning brief, an MCP call.
func Generate(evs []event.Event) []Finding {
	return GenerateForFunnel(evs, nil)
}

// GenerateForFunnel is Generate over a CALLER-SUPPLIED funnel.
//
// The dashboard picks its funnel one way (top events by volume, ordered by journey) and this
// package picked its own another way (first-touch coverage), so one page could show a funnel
// pane reading "$pageview → $engagement → $click" while its own verdict card said "overall
// $pageview→$deadclick conversion is 7%". Both were internally honest and the page still
// contradicted itself about what "the funnel" is. Passing the page's steps in makes the
// verdict describe the funnel the reader is actually looking at.
//
// steps with fewer than 2 entries falls back to detecting, so existing callers are unchanged.
func GenerateForFunnel(evs []event.Event, pageSteps []funnel.Step) []Finding {
	var out []Finding
	if len(evs) == 0 {
		return out
	}
	now := time.Now().UTC()

	// The GEO sampler writes $geo_check events to this same instance. They are our robot,
	// not the product's users, so every finding below is computed with them removed —
	// otherwise one synthetic distinct_id firing daily reads as a perfectly retained user,
	// and the sampler's volume competes for the anomaly slot with a raw $-prefixed name.
	// The visibility rule gets the unfiltered slice, because those checks are its input.
	all := evs
	evs = withoutGeoChecks(evs)

	// Computed before the product-activity guard: an instance can hold a quarter of
	// sampled answers and no traffic at all (checks arrive with the write key, the SDK
	// lands later), and that reader still deserves their verdict.
	geo := aiVisibilityShift(all, now)
	if len(evs) == 0 {
		if geo != nil {
			out = append(out, *geo)
		}
		return out
	}

	count := map[string]int{}
	for _, e := range evs {
		count[e.Name]++
	}
	names := make([]string, 0, len(count))
	for n := range count {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if count[names[i]] != count[names[j]] {
			return count[names[i]] > count[names[j]]
		}
		return names[i] < names[j]
	})
	has := func(n string) bool { _, ok := count[n]; return ok }

	// 0) what changed in the last 24h vs the trailing-week baseline — the timeliest read
	out = append(out, anomalies(evs, names, now)...)

	// 1) what the AI engines say about you, next to what they send you. Placed above the
	// funnel leak because it is a channel going quiet, which the funnel cannot show and
	// no other tool holds both halves of; below the 24h anomaly because a week-scale
	// move is never more urgent than something that broke today.
	if geo != nil {
		out = append(out, *geo)
	}

	// 2) biggest funnel leak — on the REAL journey. If the conventional names exist
	// use them; otherwise order the widest-coverage events by when users actually
	// first do them (median first-touch), so the auto-funnel follows the product's
	// true flow instead of raw volume order.
	var steps []funnel.Step
	switch {
	case len(pageSteps) >= 2:
		steps = pageSteps // the funnel the page is showing wins over any guess we would make
	case has("signup") && has("activate") && has("checkout"):
		steps = []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	default:
		steps = detectJourney(evs)
	}
	if len(steps) >= 2 {
		fr := funnel.Compute(evs, steps, 7*24*time.Hour)
		worstDrop, worstFrom, worstTo, worstPct, worstBase := -1, "", "", 0, 0
		for i := 1; i < len(fr.Steps); i++ {
			if fr.Steps[i-1].Count < minSample {
				continue // a conversion % on a handful of entrants is noise, not a leak
			}
			if fr.Steps[i].DroppedFromPrev > worstDrop {
				worstDrop = fr.Steps[i].DroppedFromPrev
				worstFrom, worstTo = fr.Steps[i-1].Event, fr.Steps[i].Event
				worstPct = int(fr.Steps[i].ConversionFromPrev*100 + 0.5)
				worstBase = fr.Steps[i-1].Count
			}
		}
		if worstDrop > 0 {
			names := make([]string, 0, len(fr.Steps))
			for _, s := range fr.Steps {
				names = append(names, s.Event)
			}
			dropoff := Finding{
				Severity: "warn",
				Kind:     KindDropoff,
				Metric:   worstTo,
				Steps:    names,
				From:     worstFrom,
				To:       worstTo,
				Rate:     worstPct,
				N:        worstBase,
				Title:    fmt.Sprintf("Biggest drop-off: after they %s", HumanStep(worstFrom)),
				Detail: qualify(fmt.Sprintf("only %d%% go on to %s, so %d people stop here. End to end, %d%% get from %s to %s.",
					worstPct, HumanEventBase(worstTo), worstDrop, int(fr.OverallConversion*100+0.5),
					HumanEventIng(fr.Steps[0].Event), HumanEventIng(fr.Steps[len(fr.Steps)-1].Event)), worstBase),
			}
			// name the segment to blame — the single most actionable thing on the page
			// ("it's mobile, 1.6× worse"). When we have it, LEAD with it and let the raw
			// drop-off follow as context, so the verdict opens with the root cause, not the
			// symptom.
			if f := segmentBlame(evs, worstFrom, worstTo); f != nil {
				// the blame finding leaks out of the SAME funnel — carry its definition so a
				// brief built from it re-runs the exact funnel the reader was looking at
				f.Steps = names
				out = append(out, *f, dropoff)
			} else {
				out = append(out, dropoff)
			}
		}
	}

	// 3) headline event, week-over-week
	head := "signup"
	if !has(head) {
		head = names[0]
	}
	var last7, prev7 int
	for _, e := range evs {
		if e.Name != head {
			continue
		}
		if e.Timestamp.After(now) {
			continue // a future-dated (clock-skewed) event has a NEGATIVE age, which would
			// fall into "last 7 days" and inflate the headline — guard it like anomalies() does.
		}
		switch age := now.Sub(e.Timestamp); {
		case age < 7*24*time.Hour:
			last7++
		case age < 14*24*time.Hour:
			prev7++
		}
	}
	if prev7 >= minSample {
		change := int(math.Round(float64(last7-prev7) / float64(prev7) * 100)) // round (handles negatives), not truncate
		sev, dir := "info", "up"
		if change < 0 {
			dir = "down"
			if change <= -15 {
				sev = "warn"
			}
		}
		out = append(out, Finding{
			Severity: sev,
			Kind:     KindTrend,
			Metric:   head,
			Rate:     change,
			N:        prev7,
			Title:    fmt.Sprintf("%s is %s %d%% week-over-week", HumanEventNoun(head), dir, absInt(change)),
			Detail:   qualify(fmt.Sprintf("%d in the last 7 days vs %d the week before.", last7, prev7), prev7),
		})
	}

	// 4) retention read
	// anchor: ANY event, the same default /v1/retention, the dashboard, and the ask
	// bar use — four surfaces, one definition of "came back".
	rr := retention.Compute(evs, 7, "")
	// retention.DayN keeps the denominator honest: only cohorts old enough to have
	// observed day N count (the retention-triangle rule).
	d1, size1 := retention.DayN(rr, 1, now)
	d7, size7 := retention.DayN(rr, 7, now)
	if size1 >= minSample {
		p1 := int(float64(d1)/float64(size1)*100 + 0.5)
		sev := "info"
		if p1 < 20 {
			sev = "warn"
		}
		title := fmt.Sprintf("Day-1 retention %d%%", p1)
		detail := fmt.Sprintf("of %d users past day 1 (any activity counts as returning).", size1)
		if size7 >= minSample {
			p7 := int(float64(d7)/float64(size7)*100 + 0.5)
			title = fmt.Sprintf("Day-1 retention %d%%, day-7 %d%%", p1, p7)
			detail = fmt.Sprintf("of %d users past day 1 (%d past day 7), any activity counts as returning.", size1, size7)
		}
		out = append(out, Finding{Severity: sev, Kind: KindRetention, Rate: p1, N: size1,
			Title: title, Detail: qualify(detail, size1)})
	}

	// warnings first
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity == "warn" && out[j].Severity != "warn" })
	return out
}

// anomalies flags the single sharpest "what changed since yesterday": an event whose
// last-24h volume deviates hard from its prior-7-day daily baseline. A sudden drop
// (tracking broke? a funnel regressed?) or spike is the most timely, actionable thing in
// the verdict. Noise-guarded — only events with a real baseline, only big swings — so a
// low-volume product never gets false alarms.
func anomalies(evs []event.Event, names []string, now time.Time) []Finding {
	recentStart := now.Add(-24 * time.Hour)
	baseStart := now.Add(-8 * 24 * time.Hour)
	type stat struct{ last24, baseTotal int }
	stats := map[string]*stat{}
	for _, e := range evs {
		if e.Timestamp.Before(baseStart) || e.Timestamp.After(now) {
			continue
		}
		s := stats[e.Name]
		if s == nil {
			s = &stat{}
			stats[e.Name] = s
		}
		if !e.Timestamp.Before(recentStart) {
			s.last24++
		} else {
			s.baseTotal++
		}
	}

	top := names // only the highest-volume events, so we never flag something obscure
	if len(top) > 6 {
		top = top[:6]
	}
	var best Finding
	bestScore, found := 0.0, false
	for _, n := range top {
		s := stats[n]
		if s == nil {
			continue
		}
		baseDaily := float64(s.baseTotal) / 7.0
		if s.baseTotal < minSample || baseDaily < 3 { // not enough normal volume to trust a percentage swing
			continue
		}
		dev := (float64(s.last24) - baseDaily) / baseDaily
		score := math.Abs(dev)
		if score < 0.4 || score <= bestScore { // need a real swing, keep the sharpest
			continue
		}
		bestScore, found = score, true
		pct := int(math.Round(score * 100))
		if dev < 0 {
			best = Finding{
				Severity: "warn", Kind: KindAnomaly, Metric: n, Rate: -pct, N: s.baseTotal,
				Title:  fmt.Sprintf("%s dropped %d%% in the last 24h", HumanEventNoun(n), pct),
				Detail: fmt.Sprintf("%d in the last 24h vs ~%.0f/day normally, worth a look (tracking down, or a regression?).", s.last24, baseDaily),
			}
		} else {
			best = Finding{
				Severity: "info", Kind: KindAnomaly, Metric: n, Rate: pct, N: s.baseTotal,
				Title:  fmt.Sprintf("%s jumped %d%% in the last 24h", HumanEventNoun(n), pct),
				Detail: fmt.Sprintf("%d in the last 24h vs ~%.0f/day normally.", s.last24, baseDaily),
			}
		}
		best.Detail = qualify(best.Detail, s.baseTotal)
	}
	if found {
		return []Finding{best}
	}
	return nil
}

// Text renders the digest as a plain-text brief (for the daily webhook/email).
func Text(findings []Finding) string {
	if len(findings) == 0 {
		return "No activity yet."
	}
	s := ""
	for _, f := range findings {
		mark := "•"
		if f.Severity == "warn" {
			mark = "⚠"
		}
		s += fmt.Sprintf("%s %s: %s\n", mark, f.Title, f.Detail)
	}
	return s
}
