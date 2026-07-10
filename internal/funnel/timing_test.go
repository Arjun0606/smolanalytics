package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func TestFunnel_MedianTimeToConvert(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ev := func(user, name string, offset time.Duration) event.Event {
		return event.Event{Name: name, DistinctID: user, Timestamp: t0.Add(offset)}
	}
	evs := []event.Event{
		// A converts in 2h
		ev("A", "signup", 0), ev("A", "activate", time.Hour), ev("A", "checkout", 2*time.Hour),
		// B converts in 4h
		ev("B", "signup", 0), ev("B", "activate", 3*time.Hour), ev("B", "checkout", 4*time.Hour),
		// C never converts (signup only) — must not skew the median
		ev("C", "signup", 0),
	}
	steps := []Step{{"signup"}, {"activate"}, {"checkout"}}
	r := Compute(evs, steps, 0)

	if r.Converted != 2 {
		t.Fatalf("Converted = %d, want 2 (A and B, not C)", r.Converted)
	}
	// median of {2h, 4h} = 3h = 10800s
	if r.MedianConvSecs != 10800 {
		t.Errorf("MedianConvSecs = %v, want 10800 (3h)", r.MedianConvSecs)
	}
}

func TestFunnel_NoConvertersNoTiming(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{Name: "signup", DistinctID: "A", Timestamp: t0},
		{Name: "activate", DistinctID: "A", Timestamp: t0.Add(time.Hour)},
		// nobody reaches checkout
	}
	r := Compute(evs, []Step{{"signup"}, {"activate"}, {"checkout"}}, 0)
	if r.Converted != 0 || r.MedianConvSecs != 0 {
		t.Errorf("no full conversion should give zero timing, got Converted=%d Median=%v", r.Converted, r.MedianConvSecs)
	}
}
