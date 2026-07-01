package retention

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// DayN must only count cohorts whose day-N has fully elapsed.
func TestDayNObservableDenominator(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-10 * 24 * time.Hour)
	evs := []event.Event{
		// old cohort: 2 users, 1 returns on day 1, 1 on day 7
		{ID: "a1", DistinctID: "a", Name: "open", Timestamp: old},
		{ID: "a2", DistinctID: "a", Name: "open", Timestamp: old.Add(24 * time.Hour)},
		{ID: "b1", DistinctID: "b", Name: "open", Timestamp: old},
		{ID: "b2", DistinctID: "b", Name: "open", Timestamp: old.Add(7 * 24 * time.Hour)},
		// young cohort: 3 users today — unobservable for any day N
		{ID: "c", DistinctID: "c", Name: "open", Timestamp: now.Add(-time.Hour)},
		{ID: "d", DistinctID: "d", Name: "open", Timestamp: now.Add(-time.Hour)},
		{ID: "e", DistinctID: "e", Name: "open", Timestamp: now.Add(-time.Hour)},
	}
	rr := Compute(evs, 7, "open")

	if ret, size := DayN(rr, 1, now); ret != 1 || size != 2 {
		t.Fatalf("day1: got %d/%d, want 1/2 (young cohort excluded)", ret, size)
	}
	if ret, size := DayN(rr, 7, now); ret != 1 || size != 2 {
		t.Fatalf("day7: got %d/%d, want 1/2", ret, size)
	}
	// n beyond maxDays → nothing, never a fabricated zero-rate
	if ret, size := DayN(rr, 30, now); ret != 0 || size != 0 {
		t.Fatalf("day30 with maxDays=7: got %d/%d, want 0/0", ret, size)
	}
}
