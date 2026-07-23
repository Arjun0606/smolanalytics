// Package cohort defines reusable user groups — "users who did checkout", "users
// from Hacker News who activated" — that you define once and apply across every
// report. A definition is event membership + property filters; Resolve turns it
// into the matching user-id set; the Store persists definitions.
package cohort

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// Definition is a saved cohort. Two modes: simple event membership (users who did the
// listed events any/all, with optional property filters), or — when Sequence is set — a
// SEQUENCED behavioral cohort ("did A then B within 7 days but not C, at least 3 times"),
// which is deeper than what plain membership can express.
type Definition struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Match    string         `json:"match"` // "any" (default) or "all"
	Events   []string       `json:"events"`
	Filters  []query.Filter `json:"filters"`
	Sequence *Sequence      `json:"sequence,omitempty"` // set → membership is the ordered sequence match
	Created  time.Time      `json:"created"`
}

// Step is one condition in a sequence: an event that must occur, with optional property
// filters on that event and an optional minimum TOTAL occurrence count (default 1).
type Step struct {
	Event   string         `json:"event"`
	Filters []query.Filter `json:"filters,omitempty"`
	Count   int            `json:"count,omitempty"`
}

// Sequence is an ordered set of steps that must happen IN ORDER — the "did A then B then C"
// cohort. Optional: a max span between the first and last matched step (WithinMs), a
// first-N-after-signup anchor (WithinFirstMs, measured from the user's first-ever event), and
// excluded events that must NOT occur within the matched span (the "but not C" clause).
type Sequence struct {
	Steps         []Step   `json:"steps"`
	WithinMs      int64    `json:"within_ms,omitempty"`
	WithinFirstMs int64    `json:"within_first_ms,omitempty"`
	Exclude       []string `json:"exclude,omitempty"`
}

// Resolve returns the set of distinct_ids that match the definition. Top-level filters (and
// the default dev-env exclusion) are applied first; then either the sequence match or the
// simple event-membership match runs over each user's history.
func Resolve(events []event.Event, d Definition) map[string]bool {
	evs := query.Apply(events, d.Filters)
	if d.Sequence != nil {
		return resolveSequence(evs, *d.Sequence)
	}
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

// resolveSequence groups the (already top-level-filtered) events per user and returns the
// users whose stream satisfies the ordered sequence.
func resolveSequence(evs []event.Event, seq Sequence) map[string]bool {
	out := map[string]bool{}
	if len(seq.Steps) == 0 {
		return out
	}
	byUser := map[string][]event.Event{}
	for _, e := range evs {
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}
	for user, ue := range byUser {
		if matchSequence(ue, seq) {
			out[user] = true
		}
	}
	return out
}

// matchSequence reports whether ONE user's events satisfy the sequence. Two independent
// conditions are ANDed: (1) a per-step minimum TOTAL occurrence gate, and (2) an ordered
// match. The ordered match is anchor-then-greedy: for each occurrence of step 0 (in time
// order) it greedily matches the remaining steps strictly after it, then checks the window,
// the first-N-days anchor, and the "must not occur in the span" exclusions — accepting the
// first anchor that satisfies all of them. Trying every step-0 anchor (rather than only the
// earliest) is what makes a windowed sequence correct: a wide earliest match doesn't hide a
// tighter later one. Same greedy step semantics the funnel engine uses, so behavior is
// consistent across reports.
func matchSequence(userEvents []event.Event, seq Sequence) bool {
	evs := make([]event.Event, len(userEvents))
	copy(evs, userEvents)
	sort.Slice(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })
	if len(evs) == 0 {
		return false
	}

	stepMatch := func(e event.Event, s Step) bool {
		return e.Name == s.Event && query.Matches(e, s.Filters)
	}

	// (1) per-step minimum TOTAL occurrence gate — independent of ordering.
	for _, s := range seq.Steps {
		need := s.Count
		if need < 1 {
			need = 1
		}
		got := 0
		for _, e := range evs {
			if stepMatch(e, s) {
				got++
			}
		}
		if got < need {
			return false
		}
	}

	within := time.Duration(seq.WithinMs) * time.Millisecond
	withinFirst := time.Duration(seq.WithinFirstMs) * time.Millisecond
	firstSeen := evs[0].Timestamp

	ex := map[string]bool{}
	for _, n := range seq.Exclude {
		ex[n] = true
	}

	// (2) ordered anchor-then-greedy match.
	for a := 0; a < len(evs); a++ {
		if !stepMatch(evs[a], seq.Steps[0]) {
			continue
		}
		t0 := evs[a].Timestamp
		// anchors only get later in time, so once the anchor itself is past the first-N-days
		// window, no later anchor can fit it either — stop.
		if withinFirst > 0 && t0.Sub(firstSeen) > withinFirst {
			break
		}
		tLast := t0
		idx := a + 1
		ok := true
		for _, s := range seq.Steps[1:] {
			found := false
			for ; idx < len(evs); idx++ {
				if stepMatch(evs[idx], s) {
					tLast = evs[idx].Timestamp
					idx++
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if within > 0 && tLast.Sub(t0) > within {
			continue // span too wide for this anchor; a later anchor may be tighter
		}
		if withinFirst > 0 && tLast.Sub(firstSeen) > withinFirst {
			continue
		}
		if len(ex) > 0 && excludedInSpan(evs, t0, tLast, ex) {
			continue
		}
		return true
	}
	return false
}

// excludedInSpan reports whether any excluded-name event falls within [t0, tLast]. evs is
// sorted ascending, so it scans only the span and stops past it.
func excludedInSpan(evs []event.Event, t0, tLast time.Time, ex map[string]bool) bool {
	for _, e := range evs {
		if e.Timestamp.Before(t0) {
			continue
		}
		if e.Timestamp.After(tLast) {
			break
		}
		if ex[e.Name] {
			return true
		}
	}
	return false
}
