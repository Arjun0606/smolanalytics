package person

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(name, user string, at time.Time, props map[string]any) event.Event {
	return event.Event{Name: name, DistinctID: user, Timestamp: at, Properties: props}
}

// The correctness question this whole package turns on. Events do not arrive in order: a
// backfilled import, a mobile client flushing a week-old queue, a retry across a partition. If
// "last known" means "last processed", someone who upgraded on Monday shows as free on Friday
// when their offline phone finally syncs — and nothing anywhere looks broken.
func TestLastKnownTraitIsDecidedByTimeNotArrivalOrder(t *testing.T) {
	mon := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	fri := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)

	// the LATE-ARRIVING event is the OLD one — the ordinary shape of an offline flush
	evs := []event.Event{
		ev(IdentifyEvent, "u1", fri, map[string]any{"plan": "pro"}),
		ev(IdentifyEvent, "u1", mon, map[string]any{"plan": "free"}),
	}
	got := Compute(evs)["u1"]
	if got.Traits["plan"] != "pro" {
		t.Errorf("plan = %v, want pro — a week-old event overwrote today's because it arrived second",
			got.Traits["plan"])
	}
	// and the other order must give the same answer, or the result depends on the store
	rev := Compute([]event.Event{evs[1], evs[0]})["u1"]
	if rev.Traits["plan"] != got.Traits["plan"] {
		t.Errorf("input order changed the profile: %v vs %v", rev.Traits["plan"], got.Traits["plan"])
	}
}

// Replaying the same event must be a no-op, not a coin flip decided by map iteration.
func TestExactTiesAreStable(t *testing.T) {
	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	e := ev(IdentifyEvent, "u1", at, map[string]any{"plan": "pro"})
	for i := 0; i < 20; i++ {
		if Compute([]event.Event{e, e, e})["u1"].Traits["plan"] != "pro" {
			t.Fatal("a replayed event produced a different profile")
		}
	}
}

// A trait can ride along with an ordinary event via $set, so instrumenting one does not require a
// second call — but the transport properties must NOT become traits.
func TestSetRidesAlongAndReservedPropsAreNotTraits(t *testing.T) {
	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	p := Compute([]event.Event{
		ev("checkout", "u1", at, map[string]any{
			"$set":       map[string]any{"plan": "pro", "seats": float64(5)},
			"session_id": "s-123", "path": "/checkout", "env": "production",
		}),
	})["u1"]
	if p.Traits["plan"] != "pro" || p.Traits["seats"] != float64(5) {
		t.Errorf("$set traits did not land: %v", p.Traits)
	}
	for _, bad := range []string{"session_id", "path", "env"} {
		if _, ok := p.Traits[bad]; ok {
			t.Errorf("%q became a trait — a profile full of session ids is one nobody can filter on", bad)
		}
	}
}

// Map iteration order is random in Go. A "recent people" list that reshuffles on every load makes
// a reader distrust every other number on the page.
func TestListOrderIsDeterministic(t *testing.T) {
	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 40; i++ {
		evs = append(evs, ev("$pageview", string(rune('a'+i%26))+itoa(i), base.Add(time.Duration(i)*time.Minute), nil))
	}
	first := List(Compute(evs), 10)
	for i := 0; i < 15; i++ {
		got := List(Compute(evs), 10)
		for j := range first {
			if got[j].DistinctID != first[j].DistinctID {
				t.Fatalf("run %d differs at row %d: %s vs %s", i, j, got[j].DistinctID, first[j].DistinctID)
			}
		}
	}
	// most recently seen first
	if first[0].LastSeen.Before(first[1].LastSeen) {
		t.Error("the list is not ordered most-recent-first")
	}
}

// The bridge that makes a person property useful: turn "plan = pro" into a set of ids the
// event-level reports can be scoped to.
func TestMatchingBridgesTraitsToEvents(t *testing.T) {
	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	profiles := Compute([]event.Event{
		ev(IdentifyEvent, "u1", at, map[string]any{"plan": "pro"}),
		ev(IdentifyEvent, "u2", at, map[string]any{"plan": "free"}),
		ev(IdentifyEvent, "u3", at, map[string]any{"plan": "pro"}),
		ev("$pageview", "u4", at, nil), // no trait at all
	})
	pro := Matching(profiles, "plan", "pro")
	if len(pro) != 2 || !pro["u1"] || !pro["u3"] {
		t.Errorf("plan=pro matched %v, want u1 and u3", pro)
	}
	// a numeric trait must compare the way the event filter compares it
	num := Compute([]event.Event{ev(IdentifyEvent, "n1", at, map[string]any{"seats": float64(50)})})
	if len(Matching(num, "seats", "50")) != 1 {
		t.Error("a numeric trait did not match its string form — person and event filters disagree")
	}

	vals := TraitValues(profiles, "plan")
	if len(vals) != 2 || vals[0].Value != "pro" || vals[0].People != 2 {
		t.Errorf("trait breakdown = %+v, want pro first with 2 people", vals)
	}
	if ts := Traits(profiles); len(ts) != 1 || ts[0].Value != "plan" || ts[0].People != 3 {
		t.Errorf("trait list = %+v, want plan held by 3 people", ts)
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
