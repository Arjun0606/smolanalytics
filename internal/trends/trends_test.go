package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func ev(user, name string, day int) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.AddDate(0, 0, day)}
}

func TestDailyCountsAndZeroFill(t *testing.T) {
	evs := []event.Event{
		ev("a", "view", 0), ev("a", "view", 0), // day0: 2 raw, 1 unique
		ev("b", "view", 0), // day0 now: 3 raw, 2 unique
		ev("a", "view", 2), // day2: 1
	}
	r := Compute(evs, "view", time.Time{}, time.Time{}, false)
	// days 0,1,2 filled
	if len(r.Points) != 3 {
		t.Fatalf("want 3 days, got %d", len(r.Points))
	}
	if r.Points[0].Count != 3 || r.Points[1].Count != 0 || r.Points[2].Count != 1 {
		t.Fatalf("counts = %d/%d/%d, want 3/0/1", r.Points[0].Count, r.Points[1].Count, r.Points[2].Count)
	}
	if r.Total != 4 {
		t.Fatalf("total = %d, want 4", r.Total)
	}
}

func TestUniqueUsers(t *testing.T) {
	evs := []event.Event{ev("a", "view", 0), ev("a", "view", 0), ev("b", "view", 0)}
	r := Compute(evs, "view", time.Time{}, time.Time{}, true)
	if r.Points[0].Count != 2 {
		t.Fatalf("unique day0 = %d, want 2", r.Points[0].Count)
	}
}
