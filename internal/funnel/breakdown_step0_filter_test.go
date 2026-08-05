package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A breakdown is supposed to be a CUT of the funnel above it, so both halves have to agree on
// what "step 0" is. They didn't: the segmenter picked the earliest event merely NAMED steps[0],
// while the matcher (with a step-0 filter, e.g. sf0=plan:pro) anchors only on step-0 events that
// pass the filter. A user who signed up free first and pro later was therefore filed under the
// property carried by the free signup — the segment header read "hn" while the conversion under
// it was measured from the twitter signup, and the "hn" row showed a converter who converted as
// a twitter user. Same class of failure as the options being dropped entirely: one response,
// two definitions.
func TestBreakdownSegmentsOnFilteredStepZero(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	ev := func(u, name string, off time.Duration, props map[string]any) event.Event {
		return event.Event{Name: name, DistinctID: u, Timestamp: t0.Add(off), Properties: props}
	}
	evs := []event.Event{
		ev("u1", "signup", 0, map[string]any{"plan": "free", "source": "hn"}),
		ev("u1", "signup", time.Minute, map[string]any{"plan": "pro", "source": "twitter"}),
		ev("u1", "checkout", 2*time.Minute, nil),
	}
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	opts := Options{StepFilters: []map[string]string{{"plan": "pro"}, nil}}

	segs := ComputeBreakdownOpts(evs, steps, time.Hour, "source", opts)
	if len(segs) != 1 {
		t.Fatalf("want exactly 1 segment, got %d (%+v)", len(segs), segs)
	}
	if segs[0].Value != "twitter" {
		t.Errorf("segment = %q, want twitter — the funnel anchored on the pro signup, so the "+
			"breakdown must label the user by that event's source", segs[0].Value)
	}
	// the segments must still add up to the funnel they claim to cut
	total := ComputeOpts(evs, steps, time.Hour, opts)
	if segs[0].Converted != total.Converted {
		t.Errorf("segment converted %d but the funnel converted %d", segs[0].Converted, total.Converted)
	}
}

// A user whose ONLY step-0 events fail the step-0 filter never enters the funnel, so they must
// not create a segment either — an empty "free" row with 0 users reads as a segment that
// churned, not as a segment that was never in the funnel.
func TestBreakdownDropsUsersWhoseStepZeroIsFilteredOut(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	ev := func(u, name string, off time.Duration, props map[string]any) event.Event {
		return event.Event{Name: name, DistinctID: u, Timestamp: t0.Add(off), Properties: props}
	}
	evs := []event.Event{
		ev("keeper", "signup", 0, map[string]any{"plan": "pro", "source": "hn"}),
		ev("keeper", "checkout", time.Minute, nil),
		ev("dropped", "signup", 0, map[string]any{"plan": "free", "source": "reddit"}),
		ev("dropped", "checkout", time.Minute, nil),
	}
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	opts := Options{StepFilters: []map[string]string{{"plan": "pro"}, nil}}

	segs := ComputeBreakdownOpts(evs, steps, time.Hour, "source", opts)
	if len(segs) != 1 || segs[0].Value != "hn" {
		t.Fatalf("want only the hn segment, got %+v", segs)
	}
	if segs[0].Steps[0].Count != 1 {
		t.Errorf("hn step-0 count = %d, want 1", segs[0].Steps[0].Count)
	}
}
