// Package cohort defines reusable user groups — "users who did checkout", "users
// from Hacker News who activated" — that you define once and apply across every
// report. A definition is event membership + property filters; Resolve turns it
// into the matching user-id set; the Store persists definitions.
package cohort

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// Definition is a saved cohort: users who did the listed events (any/all), with
// optional property filters applied when checking membership.
type Definition struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Match   string         `json:"match"` // "any" (default) or "all"
	Events  []string       `json:"events"`
	Filters []query.Filter `json:"filters"`
	Created time.Time      `json:"created"`
}

// Resolve returns the set of distinct_ids that match the definition. Membership is
// evaluated over the user's full history (filters applied first).
func Resolve(events []event.Event, d Definition) map[string]bool {
	evs := query.Apply(events, d.Filters)
	done := map[string]map[string]bool{} // user -> set of its matched events
	for _, e := range evs {
		for _, want := range d.Events {
			if e.Name == want {
				if done[e.DistinctID] == nil {
					done[e.DistinctID] = map[string]bool{}
				}
				done[e.DistinctID][want] = true
			}
		}
	}
	out := map[string]bool{}
	for user, set := range done {
		if d.Match == "all" {
			if len(set) == len(d.Events) {
				out[user] = true
			}
		} else if len(set) > 0 {
			out[user] = true
		}
	}
	return out
}

// FilterToUsers keeps only events belonging to the given user set.
func FilterToUsers(events []event.Event, users map[string]bool) []event.Event {
	out := events[:0:0]
	for _, e := range events {
		if users[e.DistinctID] {
			out = append(out, e)
		}
	}
	return out
}
