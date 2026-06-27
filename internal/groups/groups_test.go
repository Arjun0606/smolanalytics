package groups

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(user, company string, daysAgo int, asof time.Time) event.Event {
	return event.Event{DistinctID: user, Name: "open", Timestamp: asof.AddDate(0, 0, -daysAgo),
		Properties: map[string]any{"company": company}}
}

func TestComputeGroups(t *testing.T) {
	asof := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		ev("u1", "acme", 1, asof), ev("u2", "acme", 1, asof), ev("u1", "acme", 2, asof), // acme: 3 events, 2 users, active
		ev("u3", "globex", 20, asof),                      // globex: active within 30 not 7
		{DistinctID: "u4", Name: "open", Timestamp: asof}, // no company -> skipped
	}
	r := Compute(evs, "company", asof, 0)
	if r.TotalGroups != 2 {
		t.Fatalf("total groups = %d, want 2", r.TotalGroups)
	}
	if r.ActiveGroups7d != 1 || r.ActiveGroups30d != 2 {
		t.Fatalf("active 7d=%d 30d=%d, want 1/2", r.ActiveGroups7d, r.ActiveGroups30d)
	}
	// acme ranks first (most events)
	if r.Groups[0].Value != "acme" || r.Groups[0].Events != 3 || r.Groups[0].Users != 2 {
		t.Fatalf("top group = %+v, want acme/3 events/2 users", r.Groups[0])
	}
}
