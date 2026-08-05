package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// ComputeBreakdown called Compute — the DEFAULT-options variant — with no way to pass Options
// in. So `order=strict` and `exclude=refund` were parsed, validated, applied to the headline
// funnel, and silently dropped for every segment underneath it. One response, two funnel
// definitions, no error and no note: the reader compares segments against a total measured a
// different way and concludes the segments do not add up.
func TestBreakdownHonoursOrderAndExclusions(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ev := func(u, name string, off time.Duration, props map[string]any) event.Event {
		return event.Event{Name: name, DistinctID: u, Timestamp: t0.Add(off), Properties: props}
	}
	// A converts cleanly. B does the same steps but fires `refund` in the middle, so an
	// exclusion must drop B — from the total AND from the "hn" segment, identically.
	evs := []event.Event{
		ev("A", "signup", 0, map[string]any{"source": "hn"}),
		ev("A", "activate", time.Minute, nil),
		ev("A", "checkout", 2*time.Minute, nil),
		ev("B", "signup", 0, map[string]any{"source": "hn"}),
		ev("B", "refund", 30*time.Second, nil),
		ev("B", "activate", time.Minute, nil),
		ev("B", "checkout", 2*time.Minute, nil),
	}
	steps := []Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	opts := Options{Order: Strict, Exclusions: []string{"refund"}}

	total := ComputeOpts(evs, steps, time.Hour, opts)
	segs := ComputeBreakdownOpts(evs, steps, time.Hour, "source", opts)
	if len(segs) != 1 {
		t.Fatalf("want 1 segment (both users are source=hn), got %d", len(segs))
	}
	seg := segs[0]

	// every user is in the one segment, so the segment IS the total — by construction
	if seg.Converted != total.Converted {
		t.Errorf("the only segment converted %d but the total converted %d — the segment is "+
			"being computed with different options than the funnel above it",
			seg.Converted, total.Converted)
	}
	if seg.Order != total.Order {
		t.Errorf("segment order %q != total order %q", seg.Order, total.Order)
	}
	if len(seg.ExcludedEvents) != len(total.ExcludedEvents) {
		t.Errorf("segment exclusions %v != total exclusions %v", seg.ExcludedEvents, total.ExcludedEvents)
	}
	if total.Converted != 1 {
		t.Errorf("total converted %d, want 1 — B fired the excluded event and must not count", total.Converted)
	}

	// and the old entry point still means exactly what it always did
	plain := ComputeBreakdown(evs, steps, time.Hour, "source")
	deflt := ComputeBreakdownOpts(evs, steps, time.Hour, "source", Options{})
	if len(plain) != len(deflt) || plain[0].Converted != deflt[0].Converted {
		t.Error("ComputeBreakdown no longer equals ComputeBreakdownOpts with zero Options")
	}
}
