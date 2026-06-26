// Package funnel computes ordered conversion funnels — the headline feature: of
// the users who did step 1, how many went on to do step 2, then 3, and where do
// they drop off. The computation is deterministic and storage-agnostic: it works
// on a slice of events (from the in-memory store in tests, from DuckDB in prod).
package funnel

import (
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
	Event             string  `json:"event"`
	Count             int     `json:"count"`               // distinct users who reached this step
	ConversionFromTop float64 `json:"conversion_from_top"` // count / step0 count
	ConversionFromPrev float64 `json:"conversion_from_prev"` // count / previous step count
	DroppedFromPrev   int     `json:"dropped_from_prev"`   // previous count - count
}

// Result is the full funnel: per-step counts + the overall conversion.
type Result struct {
	Steps           []StepResult `json:"steps"`
	OverallConversion float64    `json:"overall_conversion"` // last step / first step
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
	for _, evs := range byUser {
		reached := furthestStep(evs, steps, window)
		for i := 0; i < reached; i++ {
			counts[i]++
		}
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

// furthestStep returns how many funnel steps a single user completed (0..len). It
// anchors at the user's FIRST occurrence of step 0, then greedily advances through
// the remaining steps in time order, requiring each within `window` of the anchor.
func furthestStep(evs []event.Event, steps []Step, window time.Duration) int {
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })

	idx := 0
	var anchor time.Time
	for _, e := range evs {
		if e.Name != steps[idx].Event {
			continue
		}
		if idx == 0 {
			anchor = e.Timestamp
		} else if window > 0 && e.Timestamp.Sub(anchor) > window {
			break // the next step happened too late — conversion window expired
		}
		idx++
		if idx == len(steps) {
			break
		}
	}
	return idx
}
