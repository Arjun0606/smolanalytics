package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func evp(user, name, source string, day int) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.AddDate(0, 0, day),
		Properties: map[string]any{"source": source}}
}

func TestComputeBreakdown(t *testing.T) {
	evs := []event.Event{
		evp("a", "signup", "google", 0), evp("b", "signup", "google", 0),
		evp("c", "signup", "twitter", 1),
		{DistinctID: "d", Name: "signup", Timestamp: base}, // no source -> (none)
		evp("e", "other", "google", 0),                     // wrong event, ignored
	}
	series := ComputeBreakdown(evs, "signup", "source", time.Time{}, time.Time{}, false)
	if len(series) != 3 {
		t.Fatalf("want 3 series, got %d", len(series))
	}
	// sorted by total desc: google(2) first
	if series[0].Value != "google" || series[0].Total != 2 {
		t.Fatalf("top series = %s/%d, want google/2", series[0].Value, series[0].Total)
	}
	var none bool
	for _, s := range series {
		if s.Value == "(none)" && s.Total == 1 {
			none = true
		}
	}
	if !none {
		t.Fatalf("missing (none) series for the property-less event")
	}
}

// TestComputeBreakdown_SumsToTotalIncludingLatest is the regression guard for an off-by-one
// an adversarial audit surfaced: for an unbounded (all-time) query, ComputeBreakdown expanded
// spanTo to the LAST event's exact timestamp and passed it as Compute's half-open [from, to)
// upper bound — silently dropping that last event from every series, so the segments summed to
// total−1. The breakdown must always sum to the unbroken trend total.
func TestComputeBreakdown_SumsToTotalIncludingLatest(t *testing.T) {
	b := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{DistinctID: "u1", Name: "signup", Timestamp: b, Properties: map[string]any{"plan": "free"}},
		{DistinctID: "u2", Name: "signup", Timestamp: b.Add(time.Hour), Properties: map[string]any{"plan": "free"}},
		{DistinctID: "u3", Name: "signup", Timestamp: b.Add(2 * time.Hour), Properties: map[string]any{"plan": "pro"}}, // the LATEST event
	}
	total := Compute(evs, "signup", time.Time{}, time.Time{}, false).Total
	series := ComputeBreakdown(evs, "signup", "plan", time.Time{}, time.Time{}, false)
	sum, proTotal := 0, 0
	for _, s := range series {
		sum += s.Total
		if s.Value == "pro" {
			proTotal = s.Total
		}
	}
	if sum != total {
		t.Errorf("breakdown segments sum to %d, want %d (the unbroken total) — latest event dropped", sum, total)
	}
	if total != 3 {
		t.Errorf("unbroken total = %d, want 3", total)
	}
	if proTotal != 1 {
		t.Errorf("the segment holding the latest event (pro) = %d, want 1", proTotal)
	}
}
