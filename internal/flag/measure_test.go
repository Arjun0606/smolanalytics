package flag

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func exp(id, variant string, at time.Time) event.Event {
	return event.Event{Name: ExposureEvent, DistinctID: id, Timestamp: at, Properties: map[string]any{PropFlag: "banner", PropVariant: variant}}
}
func goal(id string, at time.Time) event.Event {
	return event.Event{Name: "purchase", DistinctID: id, Timestamp: at}
}

func TestMeasureABWin(t *testing.T) {
	base := time.Now().UTC().Add(-24 * time.Hour)
	var evs []event.Event
	// control "a": 100 exposed, 20 convert
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("a%d", i)
		evs = append(evs, exp(id, "a", base))
		if i < 20 {
			evs = append(evs, goal(id, base.Add(time.Hour)))
		}
	}
	// treatment "b": 100 exposed, 40 convert
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("b%d", i)
		evs = append(evs, exp(id, "b", base))
		if i < 40 {
			evs = append(evs, goal(id, base.Add(time.Hour)))
		}
	}

	rep := Measure(evs, "banner", "purchase", 30)
	if rep.Control != "a" {
		t.Fatalf("control = %q, want a", rep.Control)
	}
	if len(rep.Variants) != 2 {
		t.Fatalf("want 2 variants, got %d", len(rep.Variants))
	}
	byKey := map[string]VariantResult{}
	for _, v := range rep.Variants {
		byKey[v.Key] = v
	}
	if byKey["a"].RatePct != 20 {
		t.Fatalf("a rate = %v, want 20", byKey["a"].RatePct)
	}
	if byKey["b"].RatePct != 40 {
		t.Fatalf("b rate = %v, want 40", byKey["b"].RatePct)
	}
	if byKey["b"].DeltaPct != 100 {
		t.Fatalf("b delta = %v, want +100%% vs control", byKey["b"].DeltaPct)
	}
	if !byKey["b"].Significant {
		t.Fatal("b should be significant (20%% vs 40%% at n=100 each)")
	}
	if byKey["b"].SmallSample {
		t.Fatal("n=100 is not a small sample")
	}
}

// A conversion that happens BEFORE the user was exposed must not count — otherwise you measure
// pre-existing behavior, not the flag's effect.
func TestMeasureIgnoresPreExposureConversions(t *testing.T) {
	base := time.Now().UTC().Add(-24 * time.Hour)
	evs := []event.Event{
		goal("u1", base),                    // converted BEFORE exposure — must not count
		exp("u1", "a", base.Add(time.Hour)), //
		exp("u2", "a", base),                //
		goal("u2", base.Add(time.Hour)),     // converted AFTER exposure — counts
	}
	rep := Measure(evs, "banner", "purchase", 0)
	var a VariantResult
	for _, v := range rep.Variants {
		if v.Key == "a" {
			a = v
		}
	}
	if a.Exposed != 2 {
		t.Fatalf("exposed = %d, want 2", a.Exposed)
	}
	if a.Converted != 1 {
		t.Fatalf("converted = %d, want 1 (pre-exposure conversion excluded)", a.Converted)
	}
}

func TestMeasureEmpty(t *testing.T) {
	rep := Measure(nil, "banner", "purchase", 30)
	if len(rep.Variants) != 0 || rep.Note == "" {
		t.Fatalf("empty measure should have no variants and a guiding note, got %+v", rep)
	}
}
