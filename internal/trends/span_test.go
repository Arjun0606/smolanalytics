package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Every breakdown series must share one date span — otherwise each line starts and
// ends at its own first/last event and the chart's x-axes disagree.
func TestBreakdownSeriesShareSpan(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{ID: "1", DistinctID: "u1", Name: "signup", Timestamp: base, Properties: map[string]any{"source": "google"}},
		{ID: "2", DistinctID: "u2", Name: "signup", Timestamp: base.AddDate(0, 0, 9), Properties: map[string]any{"source": "google"}},
		// twitter only has one event, in the middle of the range
		{ID: "3", DistinctID: "u3", Name: "signup", Timestamp: base.AddDate(0, 0, 4), Properties: map[string]any{"source": "twitter"}},
	}
	series := ComputeBreakdown(evs, "signup", "source", time.Time{}, time.Time{}, false)
	if len(series) != 2 {
		t.Fatalf("want 2 series, got %d", len(series))
	}
	for _, s := range series {
		if len(s.Points) != 10 {
			t.Fatalf("series %q spans %d days, want the shared 10-day span", s.Value, len(s.Points))
		}
		if !s.Points[0].Date.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("series %q starts %v, want the shared 2026-06-01 start", s.Value, s.Points[0].Date)
		}
	}
}
