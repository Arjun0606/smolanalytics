// Package funnel computes ordered conversion funnels — the headline feature: of
// the users who did step 1, how many went on to do step 2, then 3, and where do
// they drop off. The computation is deterministic and storage-agnostic: it works
// on a slice of events from any store.Store — memory, the single-file log, or the
// columnar segment tier for scale.
package funnel

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Step is one stage of the funnel, matched by event name.
type Step struct {
	Event string `json:"event"`
}

// StepResult is the outcome for one funnel stage.
type StepResult struct {
	Event              string  `json:"event"`
	Count              int     `json:"count"`                // distinct users who reached this step
	ConversionFromTop  float64 `json:"conversion_from_top"`  // count / step0 count
	ConversionFromPrev float64 `json:"conversion_from_prev"` // count / previous step count
	DroppedFromPrev    int     `json:"dropped_from_prev"`    // previous count - count
}

// Result is the full funnel: per-step counts + the overall conversion.
type Result struct {
	// the discipline + options this funnel ran under, echoed so every surface
	// (HTTP, MCP, dashboard) emits byte-identical JSON for identical questions
	Order             string       `json:"order"`
	ExcludedEvents    []string     `json:"excluded_events,omitempty"`
	Steps             []StepResult `json:"steps"`
	OverallConversion float64      `json:"overall_conversion"`     // last step / first step
	Converted         int          `json:"converted"`              // users who completed every step
	MedianConvSecs    float64      `json:"median_conversion_secs"` // median time first->last step for converters (0 if none)
	// time-to-convert DISTRIBUTION (first→last step, for converters) — the incumbents all show
	// percentiles, not just the median: p25 = your fast movers, p90 = the long tail you'd chase
	// with a nudge/reminder. 0 when there are no converters.
	P25ConvSecs float64 `json:"p25_conversion_secs"`
	P75ConvSecs float64 `json:"p75_conversion_secs"`
	P90ConvSecs float64 `json:"p90_conversion_secs"`
	// under 5 converters the percentiles above are one or two people's timings, and a
	// median of 140719s from n=1 reads as a distribution when it's an anecdote — say so
	// in the payload itself, the same honesty rule whats_notable applies to findings.
	TimingNote string `json:"timing_note,omitempty"`
}

// Compute runs the funnel over events. A user counts toward step i only if they
// did steps[0..i] IN ORDER, each strictly after the previous, and all within
// `window` of the FIRST step (the conversion window; 0 = no limit). Other events
// in between are ignored. This matches the standard Mixpanel/Amplitude semantics.
func Compute(events []event.Event, steps []Step, window time.Duration) Result {
	return ComputeOpts(events, steps, window, Options{})
}

// SegmentResult is one value of a breakdown property and that segment's full funnel.
type SegmentResult struct {
	Value  string `json:"value"`
	Result        // the funnel for users in this segment
}

// ComputeBreakdown runs the funnel separately for each segment, where a user's segment is
// the value of `property` on their FIRST step-0 event. This is the correct Mixpanel
// semantics: a source/plan set at signup carries the user through the whole funnel even if
// later steps don't repeat the property, unlike filtering events by the property (which
// would drop steps that never carry it and report a broken conversion). Segments are sorted
// by step-0 users descending; users who never reach step 0 belong to no segment.
func ComputeBreakdown(events []event.Event, steps []Step, window time.Duration, property string) []SegmentResult {
	return ComputeBreakdownOpts(events, steps, window, property, Options{})
}

// ComputeBreakdownOpts is ComputeBreakdown with Options — the segmented half of the SAME
// engine, so `order` and `exclude` mean the same thing whether or not you asked for a
// breakdown.
//
// They did not. ComputeBreakdown called Compute (default options) with no way to pass any in,
// so GET /v1/funnel?steps=...&order=strict&exclude=refund&breakdown=source parsed both options,
// applied them to the headline funnel, and silently dropped them for every segment underneath
// it. One response, two different funnel definitions, no error and no note — the reader compares
// segments against a total that was measured a different way and concludes the segments do not
// add up. That is the agreement guarantee failing inside a single JSON body.
func ComputeBreakdownOpts(events []event.Event, steps []Step, window time.Duration, property string, opts Options) []SegmentResult {
	if len(steps) == 0 {
		return nil
	}
	if opts.Order == "" {
		opts.Order = Ordered // stepMatches reads opts; normalize once, exactly as ComputeOpts does
	}
	type u struct {
		evs      []event.Event
		seg      string
		anchorTS time.Time
		hasStep0 bool
	}
	byUser := map[string]*u{}
	for _, e := range events {
		x := byUser[e.DistinctID]
		if x == nil {
			x = &u{}
			byUser[e.DistinctID] = x
		}
		x.evs = append(x.evs, e)
		// stepMatches, not a bare name comparison: with a step-0 filter (sf0=plan:pro) the
		// funnel anchors only on step-0 events that PASS the filter, so segmenting on the
		// first event merely NAMED signup labelled the user by a property carried on an
		// event the funnel itself refused to anchor on — the segment header said hn while
		// the conversion underneath it was measured from the twitter signup. One definition
		// of "step 0" for both halves, or the breakdown is not a cut of the same funnel.
		if stepMatches(e, steps, 0, opts) && (!x.hasStep0 || e.Timestamp.Before(x.anchorTS)) {
			x.hasStep0 = true
			x.anchorTS = e.Timestamp
			if v, ok := e.Properties[property]; ok {
				x.seg = segValue(v)
			} else {
				x.seg = "(none)"
			}
		}
	}
	segEvents := map[string][]event.Event{}
	for _, x := range byUser {
		if x.hasStep0 {
			segEvents[x.seg] = append(segEvents[x.seg], x.evs...)
		}
	}
	out := make([]SegmentResult, 0, len(segEvents))
	for val, evs := range segEvents {
		out = append(out, SegmentResult{Value: val, Result: ComputeOpts(evs, steps, window, opts)})
	}
	sort.Slice(out, func(i, j int) bool {
		ci, cj := stepZero(out[i].Result), stepZero(out[j].Result)
		if ci != cj {
			return ci > cj
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func stepZero(r Result) int {
	if len(r.Steps) > 0 {
		return r.Steps[0].Count
	}
	return 0
}

// segValue renders a property value as the string a filter compares against. Deliberately the
// same rule as query.toStr — a nil is empty, a string is itself, anything else takes %v — so the
// funnel's per-step filters and the main filter engine can never disagree about a value.
func segValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// CapSegments keeps the `limit` largest segments (ComputeBreakdown already sorts by
// step-0 users descending, ties by value) and reports how many were cut. A noisy
// breakdown property (path, referrer) yields dozens of n=1 segments; returning them
// all buries the answer. Both the HTTP handler and the MCP tool cap through this one
// function with the same default, so the agreement guarantee holds.
func CapSegments(segs []SegmentResult, limit int) ([]SegmentResult, int) {
	if limit <= 0 || len(segs) <= limit {
		return segs, 0
	}
	return segs[:limit], len(segs) - limit
}

// furthestStep returns how many funnel steps a single user completed (0..len), the time
// from the anchor step-0 to the furthest matched step, and whether they fully converted.
// It tries each occurrence of step 0 as the anchor and returns the furthest the user
// reaches from the best one — so a user whose first step-0 falls out of window but who
// later retries and converts is still counted (standard Mixpanel/Amplitude re-anchoring,
// rather than dropping them on the first anchor). dur is measured on that best path.
func furthestStep(evs []event.Event, steps []Step, window time.Duration) (reached int, dur time.Duration, converted bool) {
	sortForFunnel(evs, steps)

	best := 0
	var bestDur time.Duration
	for start := range evs {
		if evs[start].Name != steps[0].Event {
			continue
		}
		anchor := evs[start].Timestamp
		idx := 1 // matched step 0
		lastMatch := anchor
		for k := start + 1; k < len(evs) && idx < len(steps); k++ {
			if window > 0 && evs[k].Timestamp.Sub(anchor) > window {
				break // out of window — and everything after is later, so stop
			}
			if evs[k].Name == steps[idx].Event {
				idx++
				lastMatch = evs[k].Timestamp
			}
		}
		if idx > best {
			best = idx
			bestDur = lastMatch.Sub(anchor)
		}
		if best == len(steps) {
			break // can't do better than full conversion
		}
	}
	return best, bestDur, best == len(steps)
}

// Order is the step-matching discipline.
type Order string

const (
	Ordered   Order = "ordered"   // default: steps in order, other events may interleave
	Strict    Order = "strict"    // steps in order with NO other events between matched steps
	Unordered Order = "unordered" // all steps within the window, any order
)

// ParseOrder maps a request string to a discipline; empty = ordered. Unknown is an
// error, never silently ordered — a wrong-discipline funnel is a silent-wrong answer.
func ParseOrder(s string) (Order, error) {
	switch s {
	case "", "ordered":
		return Ordered, nil
	case "strict":
		return Strict, nil
	case "unordered", "any", "any_order":
		return Unordered, nil
	}
	return "", fmt.Errorf("unknown order %q (want ordered, strict or unordered)", s)
}

// Options extends Compute with the disciplines the incumbents document: ordering
// mode, exclusion events (a user who fires one between first-match and full
// conversion is dropped from the funnel entirely), and per-step property filters
// (step N only matches when the event carries prop=value).
type Options struct {
	Order       Order
	Exclusions  []string            // event names that disqualify between step 0 and conversion
	StepFilters []map[string]string // per-step property equals-filters; nil entry = no filter
}

// propString stringifies a property value the same way the ordinary filter engine does
// (internal/query toStr): nil is the empty string, a string is itself, everything else goes
// through %v. Kept as a three-line copy rather than an import so funnel stays dependency-free
// on query — but the two MUST agree, because they are the two halves of "eq" in one product.
func propString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// stepMatches reports whether e satisfies step i under opts (name + per-step filter).
//
// The comparison used to be a bare type assertion, `e.Properties[k].(string)`. Properties
// arrive from JSON as map[string]any, so a numeric property is a float64 and a boolean is a
// bool; the assertion failed on both and yielded "", which never equals the wanted value.
// Measured: two checkout events with amount 50 and 10, then sf1=amount:50 returned step
// count 0, conversion 0%, dropped_from_prev = the whole population — a confident, error-free
// zero, while the SAME property through /v1/trends?filters=[{"property":"amount","op":"eq",
// "value":50}] returned 1. Two filtering surfaces of one product disagreeing on one property
// is the exact failure this engine exists to rule out. Stringify like the filter engine, and
// require the property to be PRESENT — a missing property is not an empty-string match, which
// is also how query.Filter's Eq reads it (`ok && toStr(v) == toStr(want)`).
func stepMatches(e event.Event, steps []Step, i int, opts Options) bool {
	if e.Name != steps[i].Event {
		return false
	}
	if opts.StepFilters == nil || i >= len(opts.StepFilters) || opts.StepFilters[i] == nil {
		return true
	}
	for k, want := range opts.StepFilters[i] {
		// segValue, not a bare .(string) assertion. Properties arrive from JSON as
		// map[string]any, so 50 is a float64 and the assertion yielded "" for it — the step
		// collapsed to a confident 0 with no error, while the SAME property compared through
		// query's filter engine matched fine. Two filter engines in one product disagreeing
		// about what "amount = 50" means is the shape of bug this codebase exists to not have,
		// so this stringifies exactly the way query.toStr does — including requiring the key to
		// be PRESENT, which is query.Filter's Eq rule (`ok && toStr(v) == want`). Reading a
		// missing property as "" would invent conversions for events that never carried it.
		v, present := e.Properties[k]
		if !present || segValue(v) != want {
			return false
		}
	}
	return true
}

// ComputeOpts is Compute with Options. Options{} degrades to exactly Compute's
// behavior, and Compute delegates here so there is ONE matching engine (the
// agreement guarantee depends on that).
func ComputeOpts(events []event.Event, steps []Step, window time.Duration, opts Options) Result {
	if opts.Order == "" {
		opts.Order = Ordered
	}
	res := Result{Steps: make([]StepResult, len(steps)), Order: string(opts.Order), ExcludedEvents: opts.Exclusions}
	for i, s := range steps {
		res.Steps[i].Event = s.Event
	}
	if len(steps) == 0 {
		return res
	}
	excl := map[string]bool{}
	for _, x := range opts.Exclusions {
		if x != "" {
			excl[x] = true
		}
	}
	byUser := map[string][]event.Event{}
	for _, e := range events {
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}
	counts := make([]int, len(steps))
	var convTimes []time.Duration
	for _, evs := range byUser {
		reached, dur, converted := furthestStepOpts(evs, steps, window, opts, excl)
		for i := 0; i < reached; i++ {
			counts[i]++
		}
		if converted {
			convTimes = append(convTimes, dur)
		}
	}
	finishFromCounts(&res, steps, counts, convTimes)
	return res
}

// furthestStepOpts is the single matching core under every discipline.
func furthestStepOpts(evs []event.Event, steps []Step, window time.Duration, opts Options, excl map[string]bool) (reached int, dur time.Duration, converted bool) {
	sortForFunnel(evs, steps)
	best := 0
	var bestDur time.Duration
	for start := range evs {
		// unordered anchors on ANY step's event (amplitude/posthog "any order"
		// semantics: the window opens at the first step-matching event, whichever
		// step it is); ordered/strict anchor on step 0.
		anchorStep := -1
		if opts.Order == Unordered {
			for si := range steps {
				if stepMatches(evs[start], steps, si, opts) {
					anchorStep = si
					break
				}
			}
			if anchorStep == -1 {
				continue
			}
		} else if !stepMatches(evs[start], steps, 0, opts) {
			continue
		}
		anchor := evs[start].Timestamp
		lastMatch := anchor
		excluded := false
		var idx int
		switch opts.Order {
		case Unordered:
			seen := make([]bool, len(steps))
			seen[anchorStep] = true
			matched := 1
			last := anchor
			for k := start + 1; k < len(evs); k++ {
				if window > 0 && evs[k].Timestamp.Sub(anchor) > window {
					break
				}
				if excl[evs[k].Name] {
					excluded = true
					break
				}
				for si := range steps {
					if !seen[si] && stepMatches(evs[k], steps, si, opts) {
						seen[si] = true
						matched++
						last = evs[k].Timestamp
						break
					}
				}
				if matched == len(steps) {
					break
				}
			}
			// any-order funnel depth = the longest PREFIX of the listed steps the user
			// actually performed. Counting `matched` (how many distinct steps matched,
			// anywhere) and assigning it positionally was the fabrication bug: a user who
			// did only step 2 got counted in step 1's column, and a step whose event never
			// occurs still showed the full population. Prefix-membership fixes both — step
			// k counts a user only if they performed every one of events[0..k].
			depth := 0
			for depth < len(steps) && seen[depth] {
				depth++
			}
			idx, lastMatch = depth, last
		case Strict:
			idx = 1
			for k := start + 1; k < len(evs) && idx < len(steps); k++ {
				if window > 0 && evs[k].Timestamp.Sub(anchor) > window {
					break
				}
				if excl[evs[k].Name] {
					excluded = true
					break
				}
				if stepMatches(evs[k], steps, idx, opts) {
					idx++
					lastMatch = evs[k].Timestamp
				} else {
					break // strict: ANY intervening event breaks the sequence
				}
			}
		default: // Ordered
			idx = 1
			for k := start + 1; k < len(evs) && idx < len(steps); k++ {
				if window > 0 && evs[k].Timestamp.Sub(anchor) > window {
					break
				}
				if excl[evs[k].Name] {
					excluded = true
					break
				}
				if stepMatches(evs[k], steps, idx, opts) {
					idx++
					lastMatch = evs[k].Timestamp
				}
			}
		}
		if excluded {
			continue // this anchor is disqualified; a later anchor may still convert
		}
		if idx > best {
			best = idx
			bestDur = lastMatch.Sub(anchor)
		}
		if best == len(steps) {
			break
		}
	}
	return best, bestDur, best == len(steps)
}

// finishFromCounts assembles a Result from per-step reach counts + conversion
// durations — the ONE assembly path Compute and ComputeOpts share, so the
// agreement guarantee can't drift between the plain and options funnels.
func finishFromCounts(res *Result, steps []Step, counts []int, convTimes []time.Duration) {
	if len(convTimes) > 0 {
		sort.Slice(convTimes, func(i, j int) bool { return convTimes[i] < convTimes[j] })
		n := len(convTimes)
		var med time.Duration
		if n%2 == 1 {
			med = convTimes[n/2]
		} else {
			med = (convTimes[n/2-1] + convTimes[n/2]) / 2
		}
		res.Converted = n
		res.MedianConvSecs = med.Seconds()
		// nearest-rank percentiles over the same sorted converter durations
		pct := func(f float64) float64 {
			rank := int(f*float64(n)+0.999999) - 1 // ceil(f*n)-1
			if rank < 0 {
				rank = 0
			}
			if rank >= n {
				rank = n - 1
			}
			return convTimes[rank].Seconds()
		}
		res.P25ConvSecs, res.P75ConvSecs, res.P90ConvSecs = pct(0.25), pct(0.75), pct(0.90)
		if n < 5 {
			res.TimingNote = fmt.Sprintf("conversion-time stats are from %d converter%s — treat as anecdote, not a distribution", n, plural(n))
		}
	}
	for i := range res.Steps {
		res.Steps[i].Count = counts[i]
		if counts[0] > 0 {
			res.Steps[i].ConversionFromTop = float64(counts[i]) / float64(counts[0])
		}
		if i == 0 {
			// the entry step is 100% of itself — but only when it actually has users.
			// On a zero-match funnel (e.g. a filter no one satisfies) reporting "100%"
			// on 0 users reads as a real conversion; leave it 0.
			if counts[0] > 0 {
				res.Steps[i].ConversionFromPrev = 1
			}
		} else {
			if counts[i-1] > 0 {
				res.Steps[i].ConversionFromPrev = float64(counts[i]) / float64(counts[i-1])
			}
			res.Steps[i].DroppedFromPrev = counts[i-1] - counts[i]
		}
	}
	if counts[0] > 0 {
		res.OverallConversion = float64(counts[len(counts)-1]) / float64(counts[0])
	}
}

// UserOutcome is one user's funnel result — the Microscope's raw material: the
// people BEHIND a funnel bar, not just its height.
type UserOutcome struct {
	DistinctID string `json:"distinct_id"`
	Reached    int    `json:"reached"` // steps completed (1 = step 0 only)
	Converted  bool   `json:"converted"`
}

// Users returns every user's outcome under the same matching engine as
// ComputeOpts — counts derived from this list always agree with the funnel's
// bars, because they are the same computation.
func Users(events []event.Event, steps []Step, window time.Duration, opts Options) []UserOutcome {
	if opts.Order == "" {
		opts.Order = Ordered
	}
	excl := map[string]bool{}
	for _, x := range opts.Exclusions {
		if x != "" {
			excl[x] = true
		}
	}
	byUser := map[string][]event.Event{}
	for _, e := range events {
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}
	out := make([]UserOutcome, 0, len(byUser))
	for id, evs := range byUser {
		reached, _, converted := furthestStepOpts(evs, steps, window, opts, excl)
		if reached == 0 {
			continue
		}
		out = append(out, UserOutcome{DistinctID: id, Reached: reached, Converted: converted})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistinctID < out[j].DistinctID })
	return out
}

// sortForFunnel orders a user's events into the sequence the matcher walks.
//
// It used to be a plain stable sort on timestamp, which leaves EQUAL timestamps in whatever
// order the storage layer happened to yield. The matcher scans forward from the anchor and
// treats "later in the slice" as "later in time", so two events sharing a millisecond decided
// the result by input order: the same signup and checkout returned converted=1 or converted=0
// depending on nothing but how the bytes came off disk. An import, a backfill, a DeleteUser
// segment rewrite or a compaction could therefore flip a funnel with no data change at all —
// the sharpest possible contradiction of the thing this engine exists to promise.
//
// Ties break on STEP ORDER first, which is deterministic AND the only reading consistent with
// the funnel's own definition: if signup and checkout carry the identical instant, the sequence
// that makes sense is signup then checkout. (Same-millisecond batches are ordinary — a client
// with second-resolution stamps, or an import — so refusing to credit ties would quietly
// under-count real conversions instead.) Events that are not funnel steps sort after the steps
// they tie with, then by ID and name so the order is total and storage-independent.
func sortForFunnel(evs []event.Event, steps []Step) {
	rank := make(map[string]int, len(steps))
	for i, s := range steps {
		if _, seen := rank[s.Event]; !seen {
			rank[s.Event] = i
		}
	}
	stepRank := func(name string) int {
		if r, ok := rank[name]; ok {
			return r
		}
		return len(steps) // not a step: after every step it ties with
	}
	sort.SliceStable(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if ra, rb := stepRank(a.Name), stepRank(b.Name); ra != rb {
			return ra < rb
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Name < b.Name
	})
}
