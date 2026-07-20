package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func TestComputeBreakdown_SegmentsByStep0Property(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	// signup carries `source`; activate/checkout do NOT — the correct engine must still
	// keep each user in their signup-source segment through the whole funnel.
	su := func(u, src string) event.Event {
		return event.Event{Name: "signup", DistinctID: u, Timestamp: t0, Properties: map[string]any{"source": src}}
	}
	ev := func(u, name string, off time.Duration) event.Event {
		return event.Event{Name: name, DistinctID: u, Timestamp: t0.Add(off)}
	}
	evs := []event.Event{
		su("A", "hn"), ev("A", "activate", time.Hour), ev("A", "checkout", 2*time.Hour), // hn, full convert
		su("B", "hn"), ev("B", "activate", time.Hour), // hn, drops at checkout
		su("C", "twitter"), ev("C", "activate", time.Hour), ev("C", "checkout", 2*time.Hour), // twitter, full
		su("D", "twitter"), // twitter, drops at activate
	}
	steps := []Step{{"signup"}, {"activate"}, {"checkout"}}
	segs := ComputeBreakdown(evs, steps, 0, "source")

	if len(segs) != 2 {
		t.Fatalf("want 2 segments (hn, twitter), got %d", len(segs))
	}
	// sorted by step-0 users desc, then value asc -> hn first (tie broken alphabetically)
	byVal := map[string]SegmentResult{}
	for _, s := range segs {
		byVal[s.Value] = s
	}
	hn, tw := byVal["hn"], byVal["twitter"]
	if hn.Steps[0].Count != 2 || hn.Steps[2].Count != 1 {
		t.Errorf("hn: signup=%d checkout=%d, want 2 and 1 (activate/checkout carry despite no source prop)", hn.Steps[0].Count, hn.Steps[2].Count)
	}
	if tw.Steps[0].Count != 2 || tw.Steps[2].Count != 1 {
		t.Errorf("twitter: signup=%d checkout=%d, want 2 and 1", tw.Steps[0].Count, tw.Steps[2].Count)
	}
}
