package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func ev(user, name string, offset time.Duration) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.Add(offset)}
}

func steps(names ...string) []Step {
	s := make([]Step, len(names))
	for i, n := range names {
		s[i] = Step{Event: n}
	}
	return s
}

func TestFullAndPartialConversion(t *testing.T) {
	evs := []event.Event{
		// alice: full funnel
		ev("alice", "signup", 0), ev("alice", "activate", time.Hour), ev("alice", "checkout", 2*time.Hour),
		// bob: drops after activate
		ev("bob", "signup", 0), ev("bob", "activate", time.Hour),
		// carol: only signup
		ev("carol", "signup", 0),
	}
	r := Compute(evs, steps("signup", "activate", "checkout"), 0)

	if r.Steps[0].Count != 3 || r.Steps[1].Count != 2 || r.Steps[2].Count != 1 {
		t.Fatalf("counts = %d/%d/%d, want 3/2/1", r.Steps[0].Count, r.Steps[1].Count, r.Steps[2].Count)
	}
	if r.Steps[1].DroppedFromPrev != 1 || r.Steps[2].DroppedFromPrev != 1 {
		t.Fatalf("drops = %d/%d, want 1/1", r.Steps[1].DroppedFromPrev, r.Steps[2].DroppedFromPrev)
	}
	if r.OverallConversion != 1.0/3.0 {
		t.Fatalf("overall = %v, want 1/3", r.OverallConversion)
	}
	if r.Steps[1].ConversionFromPrev != 2.0/3.0 {
		t.Fatalf("step1 conv-from-prev = %v, want 2/3", r.Steps[1].ConversionFromPrev)
	}
}

func TestOrderMatters_OutOfOrderDoesNotConvert(t *testing.T) {
	// did the steps, but in the wrong order → only step 0 counts.
	evs := []event.Event{
		ev("dave", "checkout", 0), ev("dave", "signup", time.Hour), ev("dave", "activate", 2*time.Hour),
	}
	r := Compute(evs, steps("signup", "activate", "checkout"), 0)
	if r.Steps[0].Count != 1 || r.Steps[1].Count != 1 || r.Steps[2].Count != 0 {
		t.Fatalf("counts = %d/%d/%d, want 1/1/0 (checkout was before signup)", r.Steps[0].Count, r.Steps[1].Count, r.Steps[2].Count)
	}
}

func TestConversionWindowExpires(t *testing.T) {
	evs := []event.Event{
		// within 24h window: converts
		ev("ann", "signup", 0), ev("ann", "activate", 10*time.Hour),
		// activate 48h later: outside a 24h window → drops
		ev("ben", "signup", 0), ev("ben", "activate", 48*time.Hour),
	}
	r := Compute(evs, steps("signup", "activate"), 24*time.Hour)
	if r.Steps[1].Count != 1 {
		t.Fatalf("step1 count = %d, want 1 (ben outside window)", r.Steps[1].Count)
	}
}

func TestRepeatedFirstStepAnchorsAtFirst(t *testing.T) {
	// signs up twice then activates — should still convert, anchored at first signup.
	evs := []event.Event{
		ev("eve", "signup", 0), ev("eve", "signup", time.Hour), ev("eve", "activate", 2*time.Hour),
	}
	r := Compute(evs, steps("signup", "activate"), 0)
	if r.Steps[1].Count != 1 {
		t.Fatalf("step1 count = %d, want 1", r.Steps[1].Count)
	}
}

func TestEmptyAndSingleStep(t *testing.T) {
	if got := Compute(nil, nil, 0); len(got.Steps) != 0 {
		t.Fatalf("nil steps should give empty result")
	}
	evs := []event.Event{ev("a", "x", 0), ev("b", "x", 0)}
	r := Compute(evs, steps("x"), 0)
	if r.Steps[0].Count != 2 || r.OverallConversion != 1 {
		t.Fatalf("single-step funnel wrong: %+v", r)
	}
}
