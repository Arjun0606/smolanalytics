package insight

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// detectJourney infers the product's real flow from the data: take the events with
// the widest user coverage, then order them by the median time a user first does
// each one (relative to that user's very first event). Volume order would put
// "pageview → click" first; journey order recovers "land → signup → activate".
// Deterministic, so the verdict's auto-funnel is honest about being a sequence.
func detectJourney(evs []event.Event) []funnel.Step {
	type firstTouch struct {
		userFirst  map[string]time.Time // user -> first event ever
		eventFirst map[string]map[string]time.Time
	}
	ft := firstTouch{userFirst: map[string]time.Time{}, eventFirst: map[string]map[string]time.Time{}}
	for _, e := range evs {
		if t, ok := ft.userFirst[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			ft.userFirst[e.DistinctID] = e.Timestamp
		}
		m := ft.eventFirst[e.Name]
		if m == nil {
			m = map[string]time.Time{}
			ft.eventFirst[e.Name] = m
		}
		if t, ok := m[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			m[e.DistinctID] = e.Timestamp
		}
	}

	totalUsers := len(ft.userFirst)
	if totalUsers == 0 {
		return nil
	}
	minCoverage := totalUsers / 20 // an event must touch ≥5% of users to be a "step"
	if minCoverage < 3 {
		minCoverage = 3
	}

	type cand struct {
		name     string
		coverage int
		median   time.Duration // median first-touch offset from the user's journey start
	}
	var cands []cand
	for name, users := range ft.eventFirst {
		if len(users) < minCoverage {
			continue
		}
		offs := make([]time.Duration, 0, len(users))
		for id, t := range users {
			offs = append(offs, t.Sub(ft.userFirst[id]))
		}
		sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
		cands = append(cands, cand{name: name, coverage: len(users), median: offs[len(offs)/2]})
	}
	if len(cands) < 2 {
		return nil
	}
	// widest coverage first, keep the top 4 (a readable funnel), then journey order
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].coverage != cands[j].coverage {
			return cands[i].coverage > cands[j].coverage
		}
		return cands[i].name < cands[j].name
	})
	if len(cands) > 4 {
		cands = cands[:4]
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].median != cands[j].median {
			return cands[i].median < cands[j].median
		}
		if cands[i].coverage != cands[j].coverage {
			return cands[i].coverage > cands[j].coverage
		}
		return cands[i].name < cands[j].name
	})
	steps := make([]funnel.Step, len(cands))
	for i, c := range cands {
		steps[i] = funnel.Step{Event: c.name}
	}
	return steps
}

// blameProps are the property names most likely to explain a conversion gap, tried
// in this order before falling back to whatever low-cardinality property the events
// actually carry.
var blameProps = []string{"source", "plan", "platform", "device", "channel", "country", "browser"}

// segmentBlame finds the property value that converts dramatically worse through
// the from→to step than everyone else — the difference between "conversion is 40%"
// and "fix mobile, it converts at 9%". Noise-guarded: the segment needs real volume
// and a gap big enough (≤70% of the overall rate) to be an action, not a wobble.
func segmentBlame(evs []event.Event, from, to string) *Finding {
	// Acquisition/user attributes (device, browser, source, country…) live on the LANDING
	// pageview, never on the conversion step — so without stamping each user's first-touch
	// value onto their events, the blame property is never found on the `from` event and
	// the verdict stays vague ("conversion is 40%") instead of sharp ("it's mobile, at 9%").
	// This first-touch stamp is what turns the drop-off into a root cause you can act on.
	// Step 1: first-touch-stamp KNOWN acquisition attributes (device, source, country, …).
	// These live on the LANDING event, which is often BEFORE the funnel's `from` step — without
	// stamping they never reach `from`, so the verdict couldn't segment by them at all.
	stamped := evs
	stampedProp := map[string]bool{}
	for _, p := range []string{"source", "channel", "device", "country", "browser", "platform", "os", "referrer"} {
		stamped = query.StampFirstTouch(stamped, p)
		stampedProp[p] = true
	}
	// Step 2: discover every property worth segmenting `from`→`to` by (now including the
	// stamped acquisition props, plus product/custom props already on the `from` event).
	props := usableBlameProps(stamped, from)
	// Step 3: any discovered property NOT natively carried on the conversion (`to`) event must
	// ALSO be first-touch-stamped — otherwise filtering by it drops every conversion event
	// (which never had the property) and the segment falsely reads 0%. THIS is the bug that
	// fabricated a "converts worst — fix this first" verdict for a custom entry-only property
	// (ab_variant, $current_url) even under perfectly uniform conversion.
	toProps := map[string]bool{}
	for _, e := range evs {
		if e.Name == to {
			for k := range e.Properties {
				toProps[k] = true
			}
		}
	}
	for _, p := range props {
		if !toProps[p] && !stampedProp[p] {
			stamped = query.StampFirstTouch(stamped, p)
			stampedProp[p] = true
		}
	}
	overall := stepRate(stamped, from, to, nil)
	if overall.entered < minSample || overall.rate() <= 0 {
		return nil // too thin to blame anyone
	}

	// Scan EVERY usable property, not just the first — the segment to blame might be
	// device even when source is also present. We keep the single worst segment across all
	// of them (the one whose conversion is furthest below the average), so the verdict
	// names the real root cause wherever it lives (device / source / plan / country).
	var worst *Finding
	worstRate := overall.rate()
	for _, prop := range props {
		values := map[string]bool{}
		for _, e := range stamped {
			if e.Name != from {
				continue
			}
			if v, ok := e.Properties[prop]; ok {
				values[fmt.Sprintf("%v", v)] = true
			}
		}
		for val := range values {
			seg := stepRate(stamped, from, to, []query.Filter{{Property: prop, Op: query.Eq, Value: val}})
			if seg.entered < minSample {
				continue // not enough users in the segment to conclude anything
			}
			r := seg.rate()
			// a segment converting at ≤70% of the average through this step is a real,
			// actionable gap (e.g. mobile at 32% vs 50% overall — ~1.6× worse), not a
			// wobble. minSample + the "worst across all props" scan keep it from firing on
			// noise; the old 0.6 cutoff was strict enough to miss genuine 2× underperformers.
			if r < 0.7*overall.rate() && r < worstRate {
				worstRate = r
				mult := ""
				if r > 0 {
					if x := overall.rate() / r; x >= 1.5 {
						mult = fmt.Sprintf(" — %.1f× worse than average", x)
					}
				}
				worst = &Finding{
					Severity: "warn",
					Title:    fmt.Sprintf("It's %s=%s: converts worst at %s → %s%s", prop, val, from, to, mult),
					Detail: qualify(fmt.Sprintf("only %d%% of %s=%s users continue vs %d%% overall (%d of %d). Fix this segment first — it's the biggest lever on the funnel.",
						int(r*100+0.5), prop, val, int(overall.rate()*100+0.5), seg.converted, seg.entered), seg.entered),
				}
			}
		}
	}
	return worst
}

// usableBlameProps returns every property worth segmenting the step by — the known
// explanatory names plus any other low-cardinality, wide-coverage property — so
// segmentBlame can find the underperforming segment wherever it lives, not just under
// the first-listed property.
func usableBlameProps(evs []event.Event, from string) []string {
	coverage := map[string]int{}
	distinct := map[string]map[string]bool{}
	total := 0
	for _, e := range evs {
		if e.Name != from {
			continue
		}
		total++
		for k, v := range e.Properties {
			coverage[k]++
			if distinct[k] == nil {
				distinct[k] = map[string]bool{}
			}
			distinct[k][fmt.Sprintf("%v", v)] = true
		}
	}
	if total == 0 {
		return nil
	}
	usable := func(k string) bool {
		n := len(distinct[k])
		return coverage[k]*10 >= total*6 && n >= 2 && n <= 10
	}
	seen := map[string]bool{}
	out := []string{}
	for _, k := range blameProps { // known-explanatory first, deterministic
		if usable(k) {
			out = append(out, k)
			seen[k] = true
		}
	}
	extra := make([]string, 0, len(coverage))
	for k := range coverage {
		if !seen[k] && usable(k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra) // deterministic tie-break
	return append(out, extra...)
}


// stepRate computes how many users who did `from` went on to `to` (7-day window),
// optionally within a filtered segment.
type rateResult struct{ entered, converted int }

func (r rateResult) rate() float64 {
	if r.entered == 0 {
		return 0
	}
	return float64(r.converted) / float64(r.entered)
}

func stepRate(evs []event.Event, from, to string, filters []query.Filter) rateResult {
	scoped := evs
	if len(filters) > 0 {
		scoped = query.Apply(evs, filters)
	}
	fr := funnel.Compute(scoped, []funnel.Step{{Event: from}, {Event: to}}, 7*24*time.Hour)
	if len(fr.Steps) != 2 {
		return rateResult{}
	}
	return rateResult{entered: fr.Steps[0].Count, converted: fr.Steps[1].Count}
}
