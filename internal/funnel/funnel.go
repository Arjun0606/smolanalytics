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
	Steps             []StepResult `json:"steps"`
	OverallConversion float64      `json:"overall_conversion"`     // last step / first step
	Converted         int          `json:"converted"`              // users who completed every step
	MedianConvSecs    float64      `json:"median_conversion_secs"` // median time first->last step for converters (0 if none)
}

// Compute runs the funnel over events. A user counts toward step i only if they
// did steps[0..i] IN ORDER, each strictly after the previous, and all within
// `window` of the FIRST step (the conversion window; 0 = no limit). Other events
// in between are ignored. This matches the standard Mixpanel/Amplitude semantics.
func Compute(events []event.Event, steps []Step, window time.Duration) Result {
	res := Result{Steps: make([]StepResult, len(steps))}
	for i, s := range steps {
		res.Steps[i].Event = s.Event
	}
	if len(steps) == 0 {
		return res
	}

	byUser := map[string][]event.Event{}
	for _, e := range events {
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}

	counts := make([]int, len(steps))
	var convTimes []time.Duration // time first->last step, for users who fully converted
	for _, evs := range byUser {
		reached, dur, converted := furthestStep(evs, steps, window)
		for i := 0; i < reached; i++ {
			counts[i]++
		}
		if converted {
			convTimes = append(convTimes, dur)
		}
	}
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
	}

	for i := range res.Steps {
		res.Steps[i].Count = counts[i]
		if counts[0] > 0 {
			res.Steps[i].ConversionFromTop = float64(counts[i]) / float64(counts[0])
		}
		if i == 0 {
			res.Steps[i].ConversionFromPrev = 1
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
	return res
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
	if len(steps) == 0 {
		return nil
	}
	first := steps[0].Event
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
		if e.Name == first && (!x.hasStep0 || e.Timestamp.Before(x.anchorTS)) {
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
		out = append(out, SegmentResult{Value: val, Result: Compute(evs, steps, window)})
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

func segValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// furthestStep returns how many funnel steps a single user completed (0..len), the time
// from the anchor step-0 to the furthest matched step, and whether they fully converted.
// It tries each occurrence of step 0 as the anchor and returns the furthest the user
// reaches from the best one — so a user whose first step-0 falls out of window but who
// later retries and converts is still counted (standard Mixpanel/Amplitude re-anchoring,
// rather than dropping them on the first anchor). dur is measured on that best path.
func furthestStep(evs []event.Event, steps []Step, window time.Duration) (reached int, dur time.Duration, converted bool) {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })

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
