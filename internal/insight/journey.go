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
// and a gap big enough (< 60% of the overall rate) to be an action, not a wobble.
func segmentBlame(evs []event.Event, from, to string) *Finding {
	prop := pickBlameProp(evs, from)
	if prop == "" {
		return nil
	}
	overall := stepRate(evs, from, to, nil)
	if overall.entered < 20 || overall.rate() <= 0 {
		return nil // too thin to blame anyone
	}

	values := map[string]bool{}
	for _, e := range evs {
		if e.Name != from {
			continue
		}
		if v, ok := e.Properties[prop]; ok {
			values[fmt.Sprintf("%v", v)] = true // same normalization the eq filter applies
		}
	}
	var worst *Finding
	worstRate := overall.rate()
	for val := range values {
		seg := stepRate(evs, from, to, []query.Filter{{Property: prop, Op: query.Eq, Value: val}})
		if seg.entered < 10 {
			continue // not enough users in the segment to conclude anything
		}
		r := seg.rate()
		if r < 0.6*overall.rate() && r < worstRate {
			worstRate = r
			worst = &Finding{
				Severity: "warn",
				Title:    fmt.Sprintf("%s=%s converts far worse at %s → %s", prop, val, from, to),
				Detail: fmt.Sprintf("%d%% vs %d%% overall (%d of %d users continue). Fix this segment first.",
					int(r*100+0.5), int(overall.rate()*100+0.5), seg.converted, seg.entered),
			}
		}
	}
	return worst
}

// pickBlameProp chooses the property to segment by: a known-explanatory name if
// present on the step event, else the lowest-cardinality property with wide
// coverage (2–10 distinct values on ≥60% of the step's events).
func pickBlameProp(evs []event.Event, from string) string {
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
		return ""
	}
	usable := func(k string) bool {
		n := len(distinct[k])
		return coverage[k]*10 >= total*6 && n >= 2 && n <= 10
	}
	for _, k := range blameProps {
		if usable(k) {
			return k
		}
	}
	best, bestN := "", 11
	keys := make([]string, 0, len(coverage))
	for k := range coverage {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	for _, k := range keys {
		if usable(k) && len(distinct[k]) < bestN {
			best, bestN = k, len(distinct[k])
		}
	}
	return best
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
