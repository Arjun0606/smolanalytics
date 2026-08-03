package flag

import (
	"fmt"
	"math"
	"testing"
)

// These pin the two properties an experiment platform cannot be wrong about, because being
// wrong about them does not produce an error — it produces a confidently reported result that
// happens to be garbage. A solo builder ships the losing variant and never finds out why.

// Changing a variant's WEIGHT must move a boundary, not reshuffle the deck.
//
// The original implementation bucketed with `hash % sum(weights)`, so the bucket space itself
// was a function of the weights. Going from 50/50 to 67/33 changed the modulus from 2 to 3 and
// re-randomized nearly every user mid-experiment: people who had already been shown A started
// seeing B, their pre-switch behaviour still attributed to whichever arm they landed in last.
// Nothing logs, nothing errors, and the readout is ruined.
func TestChangingWeightsDoesNotReshuffleExistingUsers(t *testing.T) {
	// Weights written the way a person actually writes them — small integers whose SUM changes.
	// This is the trigger: the old code took `hash % sum(weights)`, so 1+1=2 becoming 2+1=3
	// changed the modulus itself. (Two weight sets that both happen to total 100 hide the bug
	// entirely, which is why it survived this long.)
	before := Flag{Key: "checkout-copy", Variants: []Variant{{Key: "a", Weight: 1}, {Key: "b", Weight: 1}}}
	after := Flag{Key: "checkout-copy", Variants: []Variant{{Key: "a", Weight: 2}, {Key: "b", Weight: 1}}}

	const n = 10_000
	moved, movedIntoShrunk := 0, 0
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("user_%d", i)
		w, g := before.variantFor(id), after.variantFor(id)
		if w != g {
			moved++
			// Widening "a" from 1:1 to 2:1 may only pull users INTO "a". Anyone leaving "a" is
			// a user whose assignment was re-randomized rather than merely re-bounded.
			if w == "a" {
				movedIntoShrunk++
			}
		}
	}
	// 50/50 → 67/33 hands 17 points of probability from b to a. Only those users may move,
	// with slack for hash granularity. A re-randomizing implementation moves roughly half.
	if moved > n*22/100 {
		t.Errorf("%d of %d users (%.0f%%) changed variant after a 1:1 → 2:1 weight edit; only the ~17%% that changed hands should move — the bucket space depends on the sum of the weights",
			moved, n, 100*float64(moved)/n)
	}
	if movedIntoShrunk > 0 {
		t.Errorf("%d users LEFT variant 'a' when 'a' was widened from 1:1 to 2:1 — assignment is being re-randomized, not re-bounded", movedIntoShrunk)
	}
}

// Two different flags must bucket the same user independently. Single-pass FNV-1a has weak
// avalanche in its low-order bits, so `fnv(salt:id) % k` leaves the salt's influence partly
// intact and correlates assignments across flags. GrowthBook shipped exactly this, measured the
// bias, and retired it. The consequence is that running two experiments at once quietly
// confounds them: the users in A's treatment are disproportionately in B's treatment too.
func TestParallelExperimentsAreIndependent(t *testing.T) {
	x := Flag{Key: "experiment-one", Variants: []Variant{{Key: "a", Weight: 1}, {Key: "b", Weight: 1}}}
	y := Flag{Key: "experiment-two", Variants: []Variant{{Key: "a", Weight: 1}, {Key: "b", Weight: 1}}}

	const n = 20_000
	var both, xa, ya int
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("user_%d", i)
		inX, inY := x.variantFor(id) == "a", y.variantFor(id) == "a"
		if inX {
			xa++
		}
		if inY {
			ya++
		}
		if inX && inY {
			both++
		}
	}
	// Independent assignment puts the overlap at P(x)*P(y). The phi coefficient measures how
	// far the joint distribution sits from that product.
	px, py := float64(xa)/n, float64(ya)/n
	observed := float64(both) / n
	denom := math.Sqrt(px * (1 - px) * py * (1 - py))
	if denom == 0 {
		t.Fatal("degenerate split — one flag assigned every user to the same variant")
	}
	phi := (observed - px*py) / denom
	t.Logf("overlap %.4f vs independent %.4f (phi = %+.4f)", observed, px*py, phi)
	if math.Abs(phi) > 0.05 {
		t.Errorf("assignments across two flags correlate (phi = %+.4f, want |phi| < 0.05) — one experiment is leaking into the other", phi)
	}
}

// Whatever the internals, the same user must land in the same place every time. This is the one
// property every SDK and the engine must agree on for "same user, same variant, everywhere".
func TestAssignmentIsStable(t *testing.T) {
	f := Flag{Key: "k", Variants: []Variant{{Key: "a", Weight: 1}, {Key: "b", Weight: 1}, {Key: "c", Weight: 1}}}
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("user_%d", i)
		first := f.variantFor(id)
		for r := 0; r < 5; r++ {
			if got := f.variantFor(id); got != first {
				t.Fatalf("user %q got %q then %q — assignment is not deterministic", id, first, got)
			}
		}
	}
}

// Weights must actually be honoured, or none of the above matters.
func TestWeightsAreRespected(t *testing.T) {
	f := Flag{Key: "split", Variants: []Variant{{Key: "a", Weight: 70}, {Key: "b", Weight: 20}, {Key: "c", Weight: 10}}}
	const n = 100_000
	count := map[string]int{}
	for i := 0; i < n; i++ {
		count[f.variantFor(fmt.Sprintf("user_%d", i))]++
	}
	for key, want := range map[string]float64{"a": 70, "b": 20, "c": 10} {
		got := 100 * float64(count[key]) / n
		if math.Abs(got-want) > 1.0 {
			t.Errorf("variant %q served %.2f%% of users, want %.0f%%", key, got, want)
		}
	}
}
