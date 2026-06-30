package engagement

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A user active at EXACTLY the window boundary must be counted (inclusive "trailing N days").
func TestStickinessBoundaryInclusive(t *testing.T) {
	asof := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{DistinctID: "u_day1", Name: "x", Timestamp: asof.AddDate(0, 0, -1)},   // exactly 1 day ago
		{DistinctID: "u_day7", Name: "x", Timestamp: asof.AddDate(0, 0, -7)},   // exactly 7 days ago
		{DistinctID: "u_day30", Name: "x", Timestamp: asof.AddDate(0, 0, -30)}, // exactly 30 days ago
	}
	s := ComputeStickiness(evs, asof)
	if s.DAU != 1 {
		t.Errorf("DAU boundary excluded: got %d, want 1 (the exactly-1-day-ago user)", s.DAU)
	}
	if s.WAU != 2 {
		t.Errorf("WAU boundary excluded: got %d, want 2", s.WAU)
	}
	if s.MAU != 3 {
		t.Errorf("MAU boundary excluded: got %d, want 3", s.MAU)
	}
}
