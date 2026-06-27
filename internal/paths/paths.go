// Package paths computes user flows — "what do users do after event X?" — the
// Flows / Pathfinder / Paths report. For each user it anchors at their first
// occurrence of the start event and follows the next steps in time order, then
// aggregates the ranked next-event at each depth. Deterministic.
package paths

import (
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Step is one event and how many users took it at a given depth.
type Step struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// Level is the ranked distribution of the Nth event after the start.
type Level struct {
	Depth int    `json:"depth"`
	Steps []Step `json:"steps"`
}

// Result is the flow: how many users hit the start, then what they did next.
type Result struct {
	Start  string  `json:"start"`
	Users  int     `json:"users"`
	Levels []Level `json:"levels"`
}

// After follows up to `depth` steps after each user's first `start` event and
// ranks what they did at each step (a user who stops contributes an implicit drop).
func After(events []event.Event, start string, depth int) Result {
	if depth < 1 {
		depth = 3
	}
	byUser := map[string][]event.Event{}
	for _, e := range events {
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}

	levelCounts := make([]map[string]int, depth) // depth-1 index → event → users
	for i := range levelCounts {
		levelCounts[i] = map[string]int{}
	}
	users := 0

	for _, evs := range byUser {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })
		startIdx := -1
		for i, e := range evs {
			if e.Name == start {
				startIdx = i
				break
			}
		}
		if startIdx < 0 {
			continue
		}
		users++
		next := evs[startIdx+1:]
		for d := 0; d < depth && d < len(next); d++ {
			levelCounts[d][next[d].Name]++
		}
	}

	res := Result{Start: start, Users: users}
	for d := 0; d < depth; d++ {
		steps := make([]Step, 0, len(levelCounts[d]))
		for name, c := range levelCounts[d] {
			steps = append(steps, Step{Event: name, Count: c})
		}
		sort.Slice(steps, func(i, j int) bool {
			if steps[i].Count != steps[j].Count {
				return steps[i].Count > steps[j].Count
			}
			return steps[i].Event < steps[j].Event
		})
		res.Levels = append(res.Levels, Level{Depth: d + 1, Steps: steps})
	}
	return res
}
