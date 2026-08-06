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
	KindReadable  = "site_readable"
	KindCrawl     = "ai_crawl"
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

// baselineDays is how many days of history the 24h anomaly detector compares against, and
// minBaselineDays is how many of them must actually carry the event before that comparison
// means anything. A brand-new instance holds fewer days than the window asks for, and
// dividing by the window instead of by the data fabricates a baseline the event never had —
// see the divisor in anomalies() for the measured symptom.
const (
	baselineDays    = 7
	minBaselineDays = 5
)

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
	// before anything about what the engines SAY: if they cannot read the page, every
	// visibility number below is a symptom and this is the cause
	readable := siteReadability(all, now)
	// the retrieval half: what the crawlers actually did on the customer's own server.
	// Upstream of every "what does the model say" number for the same reason readability
	// is — an answer can only quote a page something fetched.
	crawl := aiCrawlFindings(all, now)
	geo := aiVisibilityShift(all, now)
	// the retrieval-vs-reputation split: read but not picked is a different problem from never
	// being read, and only this instance holds both halves of the evidence
	cnr := citedNotRecommended(all, now)
	if len(evs) == 0 {
		// a GEO-only instance still gets its verdict — both findings, not just the first.
		// (checks arrive with the write key; the SDK often lands later.)
		if readable != nil {
			out = append(out, *readable)
		}
		out = append(out, crawl...)
		if geo != nil {
			out = append(out, *geo)
		}
		if cnr != nil {
			out = append(out, *cnr)
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
	if readable != nil {
		out = append(out, *readable)
	}
	out = append(out, crawl...)
	if geo != nil {
		out = append(out, *geo)
	}
	if cnr != nil {
		out = append(out, *cnr)
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
		// A funnel built entirely from AUTOCAPTURE is not a product fact.
		//
		// With no product events instrumented, the dashboard picks the three busiest names — on a
		// fresh install always page view, engaged and click — and computes the drop between them.
		// That drop then became a WARNING labelled "FIX FIRST" at the top of the page, in the
		// loudest style available. Four people reading it cold all stopped there: "the number one
		// alarm on my dashboard is scaring me about nothing I can act on", and "it is not a wrong
		// number, it is a number promoted to a priority it has not earned."
		//
		// Nothing anyone does to their product moves the gap between "viewed a page" and "clicked
		// something". So instead of a warning about a funnel they never defined, say the true and
		// useful thing: we do not know what success looks like here yet.
		if worstDrop > 0 && allAutocapture(fr.Steps) {
			out = append(out, Finding{
				Severity: "note", // never a warning: nothing is wrong, something is undefined
				Kind:     KindDropoff,
				Metric:   worstTo,
				Title:    "We don't know what success looks like for your product yet",
				Detail: fmt.Sprintf("so far we can only see browsing: %d people viewed a page and %d clicked "+
					"something. that is this tracker measuring itself, not your product. tell us what counts "+
					"as success — a signup, a purchase — and this becomes a real funnel.",
					fr.Steps[0].Count, fr.Steps[len(fr.Steps)-1].Count),
			})
		} else if worstDrop > 0 {
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
			// steps, not just the endpoints: the blame has to be measured on THIS funnel, or
			// "against N% for everyone" is a number that appears nowhere else on the page.
			if f := segmentBlame(evs, steps, worstFrom, worstTo); f != nil {
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
		// Same polarity rule as anomalies() a hundred lines below, for the same reason. This
		// branch hardcoded "a drop is the warning", which is exactly inverted for the events
		// that only ever record a user failing at something. On an instance where $deadclick
		// is the highest-volume event (so `head` falls through to names[0]), dead clicks
		// collapsing 100→30 rendered as [warn] and sorted to the TOP of the verdict card as
		// the lead item, while dead clicks tripling rendered as [info] below the fold among
		// the wins. polarity_test.go pinned this for anomalies() only, so the sibling
		// detector kept the bug.
		rose, bad := change > 0, UpIsBad(head)
		sev, dir := "info", "up"
		if change < 0 {
			dir = "down"
		}
		if rose == bad && absInt(change) >= 15 { // the direction that means something got worse
			sev = "warn"
		}
		out = append(out, Finding{
			Severity: sev,
			Kind:     KindTrend,
			Metric:   head,
			Rate:     change,
			N:        prev7,
			Title:    fmt.Sprintf("%s %s %s %d%% week-over-week", HumanEventNoun(head), EventVerbIs(head), dir, absInt(change)),
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
	baseStart := now.Add(-baselineDays * 24 * time.Hour).Add(-24 * time.Hour)
	type stat struct {
		last24, baseTotal int
		day               [baselineDays]bool // which baseline days this event was actually seen on
	}
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
			continue
		}
		s.baseTotal++
		// Which of the seven baseline days this landed on, counted back from the start of the
		// last-24h window so the buckets line up with the window instead of with UTC midnight.
		d := int(recentStart.Sub(e.Timestamp) / (24 * time.Hour))
		if d >= baselineDays { // an event exactly on baseStart divides to baselineDays
			d = baselineDays - 1
		}
		s.day[d] = true
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
		// Divide by the days this event was ACTUALLY seen on, not by a hard-coded 7. A
		// three-day-old instance running a perfectly flat 30 signups/day has only two days
		// inside the baseline window, so 60/7 invented a baseline of ~9/day and the verdict
		// read "signup jumped 250% in the last 24h — 30 in the last 24h vs ~9/day normally"
		// on traffic that never moved. (n=60 clears minSample and 8.6 clears the 3/day floor,
		// so every existing guard passed it through.) The same arithmetic runs the other way
		// once an instance outlives its retention window.
		observed := 0
		for _, seen := range s.day {
			if seen {
				observed++
			}
		}
		if observed < minBaselineDays {
			continue // no baseline to speak of — a percentage against it would be fiction
		}
		baseDaily := float64(s.baseTotal) / float64(observed)
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
		volume := fmt.Sprintf("%d in the last 24h vs ~%.0f/day normally", s.last24, baseDaily)

		// WHICH direction is the bad one depends on the event. For everything a product wants
		// more of, the drop is the warning — which is what this used to assume for all of them.
		// For the autocapture events that exist only to record a user failing at something it is
		// exactly backwards, and the verdict was filing "dead clicks jumped 308% in the last 24h"
		// as an info-level note, sorted in among the good news, on a live dashboard.
		rose, bad := dev > 0, UpIsBad(n)
		verb := "dropped"
		best = Finding{Severity: "info", Kind: KindAnomaly, Metric: n, Rate: -pct, N: s.baseTotal, Detail: volume + "."}
		if rose {
			verb, best.Rate = "jumped", pct
		}
		best.Title = fmt.Sprintf("%s %s %d%% in the last 24h", HumanEventNoun(n), verb, pct)
		if rose == bad { // the move that means something got worse
			best.Severity = "warn"
			if bad {
				best.Detail = volume + " — that many more people hitting something that did not work."
			} else {
				best.Detail = volume + ", worth a look (tracking down, or a regression?)."
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

// allAutocapture reports whether every step is an event the SDK writes by itself.
//
// The "$" prefix marks events nobody chose to send. A funnel made only of those describes the
// tracker, not the product — and the distinction is what separates "your conversion is bad" from
// "you have not defined a conversion", which are opposite messages and need opposite reactions.
func allAutocapture(steps []funnel.StepResult) bool {
	if len(steps) == 0 {
		return true
	}
	for _, s := range steps {
		if !strings.HasPrefix(s.Event, "$") {
			return false
		}
	}
	return true
}
