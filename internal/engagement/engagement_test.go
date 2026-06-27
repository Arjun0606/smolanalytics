package engagement

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func ev(user string, day int) event.Event {
	return event.Event{DistinctID: user, Name: "open", Timestamp: base.AddDate(0, 0, day)}
}

func TestLifecycle(t *testing.T) {
	evs := []event.Event{
		// alice: day0 (new), day1 (returning)
		ev("alice", 0), ev("alice", 1),
		// bob: day0 (new), day2 (resurrected — gap on day1; dormant on day1)
		ev("bob", 0), ev("bob", 2),
	}
	rows := ComputeLifecycle(evs, 3) // days 0,1,2
	if len(rows) != 3 {
		t.Fatalf("want 3 days, got %d", len(rows))
	}
	// day0: alice new, bob new
	if rows[0].New != 2 {
		t.Fatalf("day0 new = %d, want 2", rows[0].New)
	}
	// day1: alice returning; bob dormant (active day0, not day1)
	if rows[1].Returning != 1 || rows[1].Dormant != 1 {
		t.Fatalf("day1 returning=%d dormant=%d, want 1/1", rows[1].Returning, rows[1].Dormant)
	}
	// day2: bob resurrected (active, not day1, but active before)
	if rows[2].Resurrected != 1 {
		t.Fatalf("day2 resurrected = %d, want 1", rows[2].Resurrected)
	}
}

func TestStickiness(t *testing.T) {
	asof := base.AddDate(0, 0, 3) // "now" = day3 12:00
	evs := []event.Event{
		ev("a", 3), // within 1 day
		ev("b", 0), // within 7/30 days, not 1
		ev("c", 2), // within 1 day (day2 12:00 is >2 days? day3-1day = day2 12:00; day2 ev is at day2 12:00, not After)
	}
	s := ComputeStickiness(evs, asof)
	if s.MAU != 3 {
		t.Fatalf("MAU = %d, want 3", s.MAU)
	}
	if s.DAU < 1 {
		t.Fatalf("DAU = %d, want >=1", s.DAU)
	}
	if s.DAUoverMAU <= 0 {
		t.Fatalf("ratio should be > 0")
	}
}
