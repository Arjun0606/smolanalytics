package flag

import (
	"fmt"
	"testing"
)

func units(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("u%06d", i)
	}
	return out
}

// The entire promise of a layer: two experiments sharing it never share a user. If this fails,
// both results are quietly measuring a population half of which is inside the other's treatment,
// and nothing on either screen looks wrong.
func TestTwoExperimentsInALayerNeverShareAUser(t *testing.T) {
	a := LayerSlice{Start: 0, End: 0.4}
	b := LayerSlice{Start: 0.4, End: 0.8}
	both, inA, inB := 0, 0, 0
	for _, u := range units(20000) {
		ga, gb := InSlice("checkout", u, a), InSlice("checkout", u, b)
		if ga {
			inA++
		}
		if gb {
			inB++
		}
		if ga && gb {
			both++
		}
	}
	if both != 0 {
		t.Errorf("%d users are in BOTH experiments — the slices overlap and every result from "+
			"either is contaminated by the other", both)
	}
	// and each slice must actually get roughly the traffic it claimed, or "40%" is decoration
	for name, got := range map[string]int{"a": inA, "b": inB} {
		if got < 7400 || got > 8600 { // 40% of 20k, generous band
			t.Errorf("slice %s admitted %d of 20000, want ~8000 (40%%)", name, got)
		}
	}
}

// Adding a layer must never move anyone's VARIANT. Folding the layer into the variant salt would
// re-randomise everyone already exposed — treatment users silently becoming control mid-flight,
// which corrupts a running experiment without changing anything visible.
func TestAddingALayerDoesNotReshuffleVariants(t *testing.T) {
	plain := Flag{Key: "checkout_v2", Enabled: true,
		Variants: []Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}}
	layered := plain
	layered.Experiment = &Experiment{Goal: "buy", Control: "control", Layer: "checkout",
		Slice: LayerSlice{Start: 0, End: 1}} // the whole layer, so eligibility changes nothing

	moved, compared := 0, 0
	for _, u := range units(5000) {
		v1, _ := plain.Evaluate(u, nil)
		v2, ok := layered.Evaluate(u, nil)
		if !ok {
			continue
		}
		compared++
		if v1 != v2 {
			moved++
		}
	}
	if compared == 0 {
		t.Fatal("nobody was eligible; the fixture proves nothing")
	}
	if moved != 0 {
		t.Errorf("%d of %d users changed variant when a layer was added — the layer is being folded "+
			"into the variant salt and is re-randomising a running experiment", moved, compared)
	}
}

// A holdout keeps its share out of experiments entirely, so months later it can answer the
// question individual wins never do: did all of this add up to anything?
func TestHoldoutExcludesItsShareFromEverything(t *testing.T) {
	f := Flag{Key: "checkout_v2", Enabled: true,
		Variants: []Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &Experiment{Goal: "buy", Control: "control",
			Holdout: "2026-h2", HoldoutPct: 10}}
	held, served := 0, 0
	for _, u := range units(20000) {
		if _, ok := f.Evaluate(u, nil); ok {
			served++
		} else {
			held++
		}
	}
	if held < 1700 || held > 2300 { // 10% of 20k
		t.Errorf("holdout kept back %d of 20000, want ~2000 (10%%)", held)
	}
	if served == 0 {
		t.Fatal("the holdout swallowed everyone")
	}

	// A SECOND experiment on the same holdout must hold back the SAME people, or the holdout is
	// not a population, just noise.
	g := f
	g.Key = "pricing_v3"
	for _, u := range units(2000) {
		_, inF := f.Evaluate(u, nil)
		_, inG := g.Evaluate(u, nil)
		if inF != inG {
			t.Fatalf("user %q is held out of one experiment and not the other — the holdout is not "+
				"a stable population", u)
		}
	}
}

// A flag with no experiment, or no layer and no holdout, must behave exactly as it always has.
// Every existing flag in every existing install is that flag.
func TestUnlayeredFlagsAreUnaffected(t *testing.T) {
	plain := Flag{Key: "banner", Enabled: true,
		Variants: []Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}}}
	withPlan := plain
	withPlan.Experiment = &Experiment{Goal: "buy", Control: "a"} // no layer, no holdout

	for _, u := range units(3000) {
		v1, ok1 := plain.Evaluate(u, nil)
		v2, ok2 := withPlan.Evaluate(u, nil)
		if ok1 != ok2 || v1 != v2 {
			t.Fatalf("user %q: plain=(%q,%v) planned=(%q,%v) — attaching a plan with no layer or "+
				"holdout changed the assignment", u, v1, ok1, v2, ok2)
		}
	}
}

// Overlapping slices are the one failure that produces no error at runtime and two subtly wrong
// results, so they are refused at validation with both sides named.
func TestOverlappingSlicesAreRefusedByName(t *testing.T) {
	err := ValidateSlices("checkout", map[string]LayerSlice{
		"checkout_v2": {Start: 0, End: 0.5},
		"pricing_v3":  {Start: 0.4, End: 0.9},
	})
	if err == nil {
		t.Fatal("two overlapping slices were accepted")
	}
	for _, want := range []string{"checkout_v2", "pricing_v3", "overlap"} {
		if !contains(err.Error(), want) {
			t.Errorf("the error does not mention %q, so an operator cannot act on it: %v", want, err)
		}
	}
	if err := ValidateSlices("checkout", map[string]LayerSlice{
		"a": {Start: 0, End: 0.5}, "b": {Start: 0.5, End: 1},
	}); err != nil {
		t.Errorf("touching but non-overlapping slices were refused: %v", err)
	}
}

// Allocation packs from the left into the first gap that fits, so a finished experiment frees
// space without shuffling the ones still running.
func TestNextSliceFillsGapsWithoutMovingAnyone(t *testing.T) {
	existing := map[string]LayerSlice{
		"a": {Start: 0, End: 0.2},
		"c": {Start: 0.5, End: 0.7},
	}
	got, err := NextSlice("checkout", existing, 0.3)
	if err != nil {
		t.Fatalf("NextSlice: %v", err)
	}
	if got.Start != 0.2 || got.End != 0.5 {
		t.Errorf("got [%g,%g), want the [0.2,0.5) gap between the two running experiments", got.Start, got.End)
	}
	existing["b"] = got
	if err := ValidateSlices("checkout", existing); err != nil {
		t.Errorf("the allocator produced an overlapping layer: %v", err)
	}
	// and a request that cannot fit says how much room there is rather than just refusing
	_, err = NextSlice("checkout", existing, 0.9)
	if err == nil {
		t.Fatal("90% was allocated into a layer with 30% free")
	}
	if !contains(err.Error(), "%") {
		t.Errorf("the refusal does not quantify the free space: %v", err)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
