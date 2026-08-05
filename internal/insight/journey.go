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
//
// `steps` is the funnel the drop-off was measured on, and it is not optional decoration.
// This used to measure both the segment and the "for everyone" base with a standalone
// two-step funnel over ALL events, which is a DIFFERENT population from the multi-step
// funnel the reader is looking at: anyone who did `from` counted, including users who never
// entered the funnel at step 0. Measured on signup→activate→checkout where 100 signups
// activate and 30 of them check out, plus 100 users who activate without signing up and 90
// of whom check out, the verdict card printed "2.0× worse than average — only 30% of mobile
// visitors continue, against 60% for everyone" one line above a drop-off card reading 30%
// for the same transition. Mobile WAS the funnel average, and 60% appeared nowhere else on
// the page. Both rates now come off the same funnel (ComputeBreakdown is the segmented half
// of the same engine), so "for everyone" is byte-for-byte the drop-off finding's own rate.
func segmentBlame(evs []event.Event, steps []funnel.Step, from, to string) *Finding {
	// Locate the blamed transition inside the caller's funnel. When the caller has no funnel
	// (the old two-argument callers, and the tests that blame a bare pair), the pair IS the
	// funnel and the two populations coincide.
	fromIdx, toIdx := -1, -1
	for i := 1; i < len(steps); i++ {
		if steps[i-1].Event == from && steps[i].Event == to {
			fromIdx, toIdx = i-1, i
			break
		}
	}
	if fromIdx < 0 {
		steps = []funnel.Step{{Event: from}, {Event: to}}
		fromIdx, toIdx = 0, 1
	}
	inFunnel := make(map[string]bool, len(steps))
	for _, s := range steps {
		inFunnel[s.Event] = true
	}
	// Acquisition/user attributes (device, browser, source, country…) live on the LANDING
	// pageview, never on the conversion step — so without stamping each user's first-touch
	// value onto their events, the blame property is never found on the `from` event and
	// the verdict stays vague ("conversion is 40%") instead of sharp ("it's mobile, at 9%").
	// This first-touch stamp is what turns the drop-off into a root cause you can act on.
	// Step 1: first-touch-stamp KNOWN acquisition attributes (device, source, country, …).
	// These live on the LANDING event, which is often BEFORE the funnel's `from` step — without
	// stamping they never reach `from`, so the verdict couldn't segment by them at all.
	// Two things used to be fused here and are now separated, because they have very
	// different costs. Finding a user's first touch must LOOK at every event — the country
	// is on the landing pageview, long before the funnel step being blamed. But stamping
	// COPIES a property map per event, and nothing below this line ever reads an event that
	// is not one of the funnel's own steps: usableBlameProps skips them, and an ordered funnel
	// ignores every other name anyway.
	//
	// So: scan everything to build the index (allocates nothing per event), then stamp only
	// the funnel's own event names. This was chaining eight full copies of history, measured
	// at 47% of everything the dashboard allocated.
	acq := []string{"source", "channel", "device", "country", "browser", "platform", "os", "referrer"}
	relevant := make([]event.Event, 0, len(evs)/4)
	for _, e := range evs {
		if inFunnel[e.Name] {
			relevant = append(relevant, e)
		}
	}
	stamped := query.BuildFirstTouch(evs, acq).Stamp(relevant)
	stampedProp := map[string]bool{}
	for _, p := range acq {
		stampedProp[p] = true
	}
	// Step 2: discover every property worth segmenting `from`→`to` by (now including the
	// stamped acquisition props, plus product/custom props already on the `from` event).
	props := usableBlameProps(stamped, from)
	// Step 3: first-touch-stamp every remaining candidate. A funnel breakdown reads the
	// segment off the user's FIRST STEP-0 event, so a property that lives anywhere else —
	// the landing pageview (ab_variant, $current_url), or the `from` step halfway down a
	// four-step funnel — leaves that user in "(none)" and out of every segment. The older
	// filter-based version had the mirror-image failure: it dropped conversion events that
	// never carried the property and read EVERY segment at 0%, fabricating a "converts
	// worst, fix this first" verdict under perfectly uniform conversion.
	var alsoStamp []string
	for _, p := range props {
		if !stampedProp[p] {
			alsoStamp = append(alsoStamp, p)
			stampedProp[p] = true
		}
	}
	// Index off the FULL history again (these properties live on the landing event too), but
	// apply to the already-narrowed slice.
	stamped = query.BuildFirstTouch(evs, alsoStamp).Stamp(stamped)

	// The base every segment is judged against: the SAME multi-step funnel the drop-off card
	// reports, restricted to the blamed transition. Narrowing `relevant` to the funnel's event
	// names does not move these counts — an ordered funnel ignores intervening events — so
	// this is the identical Result insight.go computed over the unstamped slice.
	base := funnel.Compute(stamped, steps, blameWindow)
	if len(base.Steps) <= toIdx {
		return nil
	}
	overallEntered, overallRate := base.Steps[fromIdx].Count, base.Steps[toIdx].ConversionFromPrev
	if overallEntered < minSample || overallRate <= 0 {
		return nil // too thin to blame anyone
	}

	// Scan EVERY usable property, not just the first — the segment to blame might be
	// device even when source is also present. We keep the single worst segment across all
	// of them (the one whose conversion is furthest below the average), so the verdict
	// names the real root cause wherever it lives (device / source / plan / country).
	var worst *Finding
	worstRate := overallRate
	for _, prop := range props {
		for _, sr := range funnel.ComputeBreakdown(stamped, steps, blameWindow, prop) {
			if sr.Value == "(none)" || len(sr.Steps) <= toIdx {
				continue // no value to name; "(none)" is the absence of a segment, not one
			}
			entered, converted := sr.Steps[fromIdx].Count, sr.Steps[toIdx].Count
			if entered < minSample {
				continue // not enough users in the segment to conclude anything
			}
			val := sr.Value
			r := sr.Steps[toIdx].ConversionFromPrev
			// a segment converting at ≤70% of the average through this step is a real,
			// actionable gap (e.g. mobile at 32% vs 50% overall — ~1.6× worse), not a
			// wobble. minSample + the "worst across all props" scan keep it from firing on
			// noise; the old 0.6 cutoff was strict enough to miss genuine 2× underperformers.
			if r < 0.7*overallRate && r < worstRate {
				worstRate = r
				mult := ""
				if r > 0 {
					if x := overallRate / r; x >= 1.5 {
						mult = fmt.Sprintf(", %.1f× worse than average", x)
					}
				}
				worst = &Finding{
					Severity: "warn",
					Kind:     KindSegment,
					From:     from,
					To:       to,
					Prop:     prop,
					Value:    val,
					Rate:     int(r*100 + 0.5),
					N:        entered,
					Title:    fmt.Sprintf("%s convert worst from %s to %s%s", capFirst(HumanSegment(prop, val)), HumanEventIng(from), HumanEventIng(to), mult),
					Detail: qualify(fmt.Sprintf("only %d%% of %s continue, against %d%% for everyone (%d of %d). Fixing this group is the biggest single lever on the funnel.",
						int(r*100+0.5), HumanSegment(prop, val), int(overallRate*100+0.5), converted, entered), entered),
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

// blameWindow is the conversion window every blame rate is measured over. It is the same
// 7-day window insight.GenerateForFunnel runs the drop-off funnel with — a segment measured
// over a different window than the card above it is the same "two numbers, one question"
// failure this package exists to rule out.
const blameWindow = 7 * 24 * time.Hour

// DetectJourney is detectJourney, exported.
//
// Other packages need the SAME funnel this package's verdict describes — the error report's
// impact join, for one. Exporting the existing detector rather than letting each caller write its
// own is the whole point: two detectors would eventually disagree, and then one screen would say
// "checkout dropped" about a funnel another screen never mentions.
func DetectJourney(evs []event.Event) []funnel.Step { return detectJourney(evs) }
