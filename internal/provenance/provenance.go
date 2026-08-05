// Package provenance answers the one question no other analytics tool can: SHOW ME THE ROWS
// BEHIND THIS NUMBER.
//
// Every report in this engine is a pure function of raw events, recomputed on every load. No
// rollups, no sampling, no pre-aggregation. That architecture is usually described as a
// correctness property, but its real product consequence is this package: because the raw rows
// are still here at query time, any figure on the page can hand back the exact events that
// produced it — and then recompute itself over only those events to prove nothing else was
// involved.
//
// PostHog and Mixpanel cannot do this, and not because nobody thought of it. At their scale
// everything is pre-aggregated and sampled; by the time a number reaches a dashboard the rows
// behind it are gone or unreachable. Mixpanel's own FAQ answers "why don't your numbers match"
// with ad blockers and residency endpoints — the honest answer they cannot give is "here are the
// 3,431 events, count them yourself".
//
// So this is the difference between a tool that asks to be trusted and one that can be checked.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Row is one event, rendered for a human reading the evidence rather than for a machine
// re-ingesting it. The full property bag travels because the whole point is that nothing is
// hidden between the number and its source.
type Row struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name"`
	DistinctID string         `json:"distinct_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Proof is what makes this evidence rather than a listing.
//
// Recomputed is the figure obtained by running the SAME computation over only the rows returned
// below. Matches says whether that equals the headline figure the caller was explaining. If it
// ever comes back false, the honest reading is that this engine has a bug — and saying so out
// loud is worth more than a number that is never checked.
type Proof struct {
	Claimed    float64 `json:"claimed"`
	Recomputed float64 `json:"recomputed"`
	Matches    bool    `json:"matches"`
	// Digest is a content address of the evidence: sha256 over the sorted event identities.
	// Two people running the same query get the same digest, which is what lets one person send
	// another a number and have them verify it rather than take it.
	Digest string `json:"digest"`
	// Note explains a mismatch in words. Empty when Matches is true.
	Note string `json:"note,omitempty"`
}

// Result is the evidence behind one figure.
type Result struct {
	// Question restates what was asked, so a result pasted somewhere else still says what it is.
	Question string `json:"question"`
	Total    int    `json:"total"`    // matching events BEFORE the display cap
	Returned int    `json:"returned"` // how many rows are actually below
	Capped   bool   `json:"capped"`
	Rows     []Row  `json:"rows"`
	Proof    Proof  `json:"proof"`
	// Users is the distinct actors across the matching events — the bridge to /v1/who.
	Users int `json:"users"`
}

// DefaultLimit caps the rows rendered. The COUNT is always exact and reported in Total; only the
// listing is capped, and Capped says so. A truncated list that does not admit it is the same
// class of lie as a truncated denominator.
const DefaultLimit = 500

// Collect gathers the events matching `match` and proves they reproduce `claimed`.
//
// recompute receives exactly the matched events and returns the figure they produce. Passing the
// real report function here is the entire design: if the caller re-implements the arithmetic
// instead, the proof degrades into two copies of the same possible mistake agreeing with each
// other.
func Collect(evs []event.Event, question string, claimed float64, limit int, match func(event.Event) bool, recompute func([]event.Event) float64) Result {
	if limit <= 0 {
		limit = DefaultLimit
	}
	matched := make([]event.Event, 0, 64)
	for _, e := range evs {
		if match(e) {
			matched = append(matched, e)
		}
	}
	// Deterministic order, so the digest is stable and two runs list the same rows in the same
	// sequence. Timestamp then id then name: a total order that storage order cannot perturb,
	// the same rule the funnel matcher uses for tied events.
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Name < b.Name
	})

	users := map[string]bool{}
	for _, e := range matched {
		users[e.DistinctID] = true
	}

	res := Result{
		Question: question,
		Total:    len(matched),
		Users:    len(users),
	}

	// The proof runs over EVERY matched event, never the capped listing — otherwise a large
	// result would "fail" verification purely because the display was truncated.
	got := 0.0
	if recompute != nil {
		got = recompute(matched)
	}
	res.Proof = Proof{
		Claimed:    claimed,
		Recomputed: got,
		Matches:    nearlyEqual(claimed, got),
		Digest:     digest(matched),
	}
	if !res.Proof.Matches {
		res.Proof.Note = "the rows below do not reproduce the figure they were asked to explain. " +
			"That is a bug in this engine, not in your data — please report it with this digest."
	}

	if len(matched) > limit {
		res.Capped = true
		matched = matched[:limit]
	}
	res.Returned = len(matched)
	res.Rows = make([]Row, 0, len(matched))
	for _, e := range matched {
		res.Rows = append(res.Rows, Row{
			ID: e.ID, Name: e.Name, DistinctID: e.DistinctID,
			Timestamp: e.Timestamp.UTC(), Properties: e.Properties,
		})
	}
	return res
}

// digest content-addresses the evidence. Identity is (id, name, distinct_id, timestamp) — the
// property bag is deliberately excluded so a digest stays stable when an unrelated enrichment is
// added at ingest, while still changing the moment the SET of events changes.
func digest(evs []event.Event) string {
	h := sha256.New()
	for _, e := range evs {
		h.Write([]byte(e.ID))
		h.Write([]byte{0})
		h.Write([]byte(e.Name))
		h.Write([]byte{0})
		h.Write([]byte(e.DistinctID))
		h.Write([]byte{0})
		h.Write([]byte(e.Timestamp.UTC().Format(time.RFC3339Nano)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// nearlyEqual compares two report figures. Exact equality is wrong here: a rate is float64 and
// travels through rounding on the way to the page, so demanding bit-equality would report a
// mismatch on numbers that agree to every digit anyone can see.
func nearlyEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

// CountOf is the recompute function for any figure that is "how many events" — the most common
// number on the page by a wide margin.
func CountOf(evs []event.Event) float64 { return float64(len(evs)) }

// UniqueUsersOf is the recompute function for "how many people".
func UniqueUsersOf(evs []event.Event) float64 {
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.DistinctID] = true
	}
	return float64(len(seen))
}
