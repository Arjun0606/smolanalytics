package provenance

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(id, name, user string, at time.Time, props map[string]any) event.Event {
	return event.Event{ID: id, Name: name, DistinctID: user, Timestamp: at, Properties: props}
}

// The claim this package exists to make: the rows behind a number REPRODUCE that number. A
// listing that merely looks plausible is what every other tool already offers.
func TestRowsReproduceTheFigureTheyExplain(t *testing.T) {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	evs := []event.Event{
		ev("1", "signup", "u1", base, map[string]any{"plan": "pro"}),
		ev("2", "signup", "u2", base.Add(time.Hour), map[string]any{"plan": "free"}),
		ev("3", "signup", "u1", base.Add(2*time.Hour), map[string]any{"plan": "pro"}),
		ev("4", "checkout", "u1", base.Add(3*time.Hour), nil),
	}
	isSignup := func(e event.Event) bool { return e.Name == "signup" }

	r := Collect(evs, "how many signup events", 3, 0, isSignup, CountOf)
	if r.Total != 3 || r.Returned != 3 {
		t.Fatalf("total=%d returned=%d, want 3 and 3", r.Total, r.Returned)
	}
	if !r.Proof.Matches {
		t.Errorf("the rows do not reproduce the figure: claimed %v, recomputed %v", r.Proof.Claimed, r.Proof.Recomputed)
	}
	if r.Users != 2 {
		t.Errorf("users = %d, want 2", r.Users)
	}
	// the checkout must not be in the evidence for a signup figure
	for _, row := range r.Rows {
		if row.Name != "signup" {
			t.Errorf("an unrelated %q event is being offered as evidence", row.Name)
		}
	}
}

// A WRONG claim has to fail loudly. If this ever silently passes, the feature is decoration.
func TestAWrongClaimIsReportedAsAMismatch(t *testing.T) {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	evs := []event.Event{ev("1", "signup", "u1", base, nil), ev("2", "signup", "u2", base, nil)}
	r := Collect(evs, "how many signup events", 99, 0, func(e event.Event) bool { return true }, CountOf)
	if r.Proof.Matches {
		t.Fatal("a claim of 99 against 2 rows was reported as verified")
	}
	if r.Proof.Note == "" {
		t.Error("a mismatch was reported with no explanation of what it means")
	}
}

// The digest is what lets one person send another a number and have them CHECK it rather than
// take it. Same events, same digest, regardless of the order they arrived in.
func TestDigestIsStableAcrossInputOrder(t *testing.T) {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	a := []event.Event{ev("1", "signup", "u1", base, nil), ev("2", "signup", "u2", base.Add(time.Hour), nil)}
	b := []event.Event{a[1], a[0]}
	all := func(event.Event) bool { return true }

	d1 := Collect(a, "q", 2, 0, all, CountOf).Proof.Digest
	d2 := Collect(b, "q", 2, 0, all, CountOf).Proof.Digest
	if d1 != d2 {
		t.Errorf("digest depends on input order: %s vs %s — two people running the same query would disagree", d1, d2)
	}
	// and it must actually change when the evidence changes
	c := append(append([]event.Event{}, a...), ev("3", "signup", "u3", base.Add(2*time.Hour), nil))
	if Collect(c, "q", 3, 0, all, CountOf).Proof.Digest == d1 {
		t.Error("adding an event did not change the digest")
	}
}

// Capping the LISTING must never cap the COUNT or the proof — a truncated denominator presented
// as a total is the same class of lie this codebase spent the week removing.
func TestCappingTheListingNeverCapsTheProof(t *testing.T) {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 1200; i++ {
		evs = append(evs, ev(string(rune('a'+i%26))+itoa(i), "signup", "u"+itoa(i%7), base.Add(time.Duration(i)*time.Second), nil))
	}
	r := Collect(evs, "how many signup events", 1200, 10, func(e event.Event) bool { return true }, CountOf)
	if r.Total != 1200 {
		t.Errorf("total = %d, want the true 1200", r.Total)
	}
	if r.Returned != 10 || !r.Capped {
		t.Errorf("returned=%d capped=%v, want 10 and true", r.Returned, r.Capped)
	}
	if !r.Proof.Matches {
		t.Error("the proof was computed over the capped listing instead of every matching row")
	}
	if r.Users != 7 {
		t.Errorf("users = %d, want 7 — distinct users must also come from the full set", r.Users)
	}
}

// "How many people" is a different question from "how many events", and the evidence has to
// reproduce whichever one was asked.
func TestUniqueUsersFigureIsProvedToo(t *testing.T) {
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	evs := []event.Event{
		ev("1", "signup", "u1", base, nil),
		ev("2", "signup", "u1", base.Add(time.Hour), nil),
		ev("3", "signup", "u2", base.Add(2*time.Hour), nil),
	}
	r := Collect(evs, "how many people signed up", 2, 0, func(e event.Event) bool { return e.Name == "signup" }, UniqueUsersOf)
	if !r.Proof.Matches || r.Proof.Recomputed != 2 {
		t.Errorf("3 events from 2 people did not verify as 2: %+v", r.Proof)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
