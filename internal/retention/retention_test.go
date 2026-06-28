package retention

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func ev(user, name string, day int) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.AddDate(0, 0, day)}
}

func TestNegativeDaysDoesNotPanic(t *testing.T) {
	// regression: maxDays = -2 used to make([]int, -1) and panic.
	r := Compute([]event.Event{ev("a", "open", 0)}, -2, "")
	for _, c := range r.Cohorts {
		if len(c.Returned) < 1 {
			t.Fatalf("Returned len = %d, want >= 1", len(c.Returned))
		}
	}
}

func TestRetentionGrid(t *testing.T) {
	evs := []event.Event{
		// alice: day 0, returns day 1 and day 3
		ev("alice", "open", 0), ev("alice", "open", 1), ev("alice", "open", 3),
		// bob: day 0, returns day 1 only
		ev("bob", "open", 0), ev("bob", "open", 1),
		// carol: day 0, never returns
		ev("carol", "open", 0),
	}
	r := Compute(evs, 7, "")
	if len(r.Cohorts) != 1 {
		t.Fatalf("want 1 cohort, got %d", len(r.Cohorts))
	}
	c := r.Cohorts[0]
	if c.Size != 3 {
		t.Fatalf("cohort size = %d, want 3", c.Size)
	}
	// day0 = all 3, day1 = alice+bob = 2, day2 = 0, day3 = alice = 1
	if c.Returned[0] != 3 || c.Returned[1] != 2 || c.Returned[2] != 0 || c.Returned[3] != 1 {
		t.Fatalf("returned = %v, want [3 2 0 1 ...]", c.Returned[:4])
	}
}

func TestMultipleCohortsAndEventFilter(t *testing.T) {
	evs := []event.Event{
		ev("a", "open", 0), ev("a", "open", 1),
		ev("b", "open", 2),  // different cohort day
		ev("c", "noise", 0), // filtered out by retentionEvent
	}
	r := Compute(evs, 3, "open")
	if len(r.Cohorts) != 2 {
		t.Fatalf("want 2 cohorts, got %d", len(r.Cohorts))
	}
	// cohort day 0 has only 'a' (c was filtered); a returns day1
	if r.Cohorts[0].Size != 1 || r.Cohorts[0].Returned[1] != 1 {
		t.Fatalf("cohort0 = %+v", r.Cohorts[0])
	}
	if r.Cohorts[1].Size != 1 {
		t.Fatalf("cohort1 size = %d, want 1", r.Cohorts[1].Size)
	}
}
