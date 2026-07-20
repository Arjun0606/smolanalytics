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

// TestSerializeCohortsNullsFuture pins the world-class-test P0: a retention period
// whose window hasn't started yet must serialize as null, never 0 (0 reads as
// "retention cratered to 0%"). Past periods with genuine zero returns stay 0.
func TestSerializeCohortsNullsFuture(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	// one cohort anchored 2 days ago; days 0,1 observable, days 2..7 in the future.
	anchor := now.AddDate(0, 0, -2).Truncate(24 * time.Hour)
	evs := []event.Event{
		{DistinctID: "a", Name: "x", Timestamp: anchor},
		{DistinctID: "a", Name: "x", Timestamp: anchor.AddDate(0, 0, 1)}, // returns day 1
	}
	rr := ComputeBucketed(evs, 7, "", "day", false)
	cj := SerializeCohorts(rr, now)
	if len(cj) != 1 {
		t.Fatalf("want 1 cohort, got %d", len(cj))
	}
	ret := cj[0].Returned
	// day0 and day1 observable (non-nil); day2 is today (observable, genuine 0);
	// days 3..7 are future → must be nil.
	if ret[0] == nil || *ret[0] != 1 || ret[1] == nil || *ret[1] != 1 {
		t.Errorf("day0/day1 should be observed 1/1, got %v %v", ret[0], ret[1])
	}
	for n := 3; n <= 7; n++ {
		if ret[n] != nil {
			t.Errorf("day %d has not started yet, must serialize null, got %d", n, *ret[n])
		}
	}
}

// TestSerializeCohortsNullsInProgressPeriod is the regression guard for an audit finding: the
// cohort grid rendered the CURRENT in-progress period as a finalized count, while the summary
// denominator (PeriodN) excluded it — the grid contradicted its own summary. The grid must use
// the same "fully elapsed" rule: null any period n>=1 whose window hasn't fully elapsed.
func TestSerializeCohortsNullsInProgressPeriod(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cohortDate := now.Truncate(24 * time.Hour).AddDate(0, 0, -2) // started 2 whole days ago
	r := Result{
		Bucket:  "day",
		MaxDays: 4,
		Cohorts: []Cohort{{Date: cohortDate, Size: 100, Returned: []int{100, 40, 25, 0, 0}}},
	}
	got := SerializeCohorts(r, now)[0].Returned
	if got[0] == nil || *got[0] != 100 {
		t.Errorf("period 0 (baseline) should be 100, got %v", got[0])
	}
	if got[1] == nil || *got[1] != 40 {
		t.Errorf("period 1 (fully elapsed) should be 40, got %v", got[1])
	}
	if got[2] != nil {
		t.Errorf("period 2 (in-progress, cp+2==today) must be null (not a partial count), got %d", *got[2])
	}
	// consistency with the summary denominator, both surfaces must agree on what's observable.
	if _, sz := PeriodN(r, 2, now); sz != 0 {
		t.Errorf("PeriodN(2) must exclude the in-progress cohort, got size %d", sz)
	}
	if _, sz := PeriodN(r, 1, now); sz != 100 {
		t.Errorf("PeriodN(1) must include the fully-elapsed cohort, got size %d", sz)
	}
}
