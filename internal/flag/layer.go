package flag

import (
	"fmt"
	"sort"
)

// Layers and holdouts: running more than one experiment at a time without them lying to you.
//
// Two experiments running over the same users interact. Usually harmlessly; sometimes not — a new
// checkout flow and a new pricing page tested simultaneously each measure a population half of
// which is inside the other's treatment, and both read as noisier than they are. The standard
// answer (Google's overlapping experiment infrastructure, and every platform since) is a LAYER:
// a shared [0,1) space that experiments carve into disjoint slices, so a user in one slice is
// never in another.
//
// A HOLDOUT is the same mechanism pointed at a different question: a slice of traffic kept out of
// EVERYTHING, so months later you can compare it against everyone else and find out what all your
// shipping actually added up to. It is the only measurement that survives the fact that
// individually-significant wins routinely sum to nothing.
//
// The bucketing hash itself is untouched. Layer position is a SEPARATE draw on a separate salt,
// so adding a layer never reshuffles variant assignment within an experiment — which is the
// footgun this file exists to make impossible rather than merely discouraged.

// LayerSlice is one experiment's claim on a layer's space, half-open [Start, End).
type LayerSlice struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Width is the share of traffic this slice admits.
func (s LayerSlice) Width() float64 { return s.End - s.Start }

// Empty reports whether no slice was declared — the ordinary case of a flag that is not in a
// layer at all, which sees ALL traffic.
func (s LayerSlice) Empty() bool { return s.Start == 0 && s.End == 0 }

// layerSalt keeps layer position independent of variant assignment.
//
// If the layer were folded into the variant salt instead, adding a flag to a layer would
// re-randomise every user already in the experiment — treatment users silently becoming control
// mid-flight, which corrupts the result without changing a single number on screen. Two salts,
// two independent draws, and StartExperiment refuses a layer on a flag that already has
// exposures anyway. Belt and braces, because this failure is invisible.
func layerSalt(layer string) string { return "layer:" + layer }

// holdoutSalt is likewise its own draw, so entering or leaving a holdout cannot move anyone's
// variant within an experiment they remain eligible for.
func holdoutSalt(key string) string { return "holdout:" + key }

// LayerPosition is where a unit falls in a layer's shared space, in [0,1).
//
// Deterministic and stable: the same unit is always in the same position in the same layer, for
// as long as the layer exists. That stability is what lets two experiments claim disjoint slices
// and stay disjoint.
func LayerPosition(layer, unitID string) float64 { return bucket(layerSalt(layer), unitID) }

// InSlice reports whether a unit is inside an experiment's slice of its layer.
//
// An empty slice means "not layered" and admits everyone, so every existing flag keeps its
// current behaviour exactly.
func InSlice(layer, unitID string, s LayerSlice) bool {
	if s.Empty() {
		return true
	}
	p := LayerPosition(layer, unitID)
	return p >= s.Start && p < s.End
}

// InHoldout reports whether a unit is being kept out of experiments entirely.
//
// pct is the share held out, 0..100. Zero means no holdout, which is the default — a holdout that
// switched itself on would quietly shrink every experiment's sample.
func InHoldout(key, unitID string, pct float64) bool {
	if pct <= 0 || key == "" {
		return false
	}
	if pct >= 100 {
		return true
	}
	return bucket(holdoutSalt(key), unitID) < pct/100
}

// ValidateSlices rejects a layer whose experiments overlap.
//
// This is the whole guarantee, so it is checked rather than documented. Overlapping slices do not
// fail loudly at runtime — they produce two experiments that quietly share users and two results
// that are each a little wrong in a way no amount of staring at the numbers reveals.
//
// `named` maps an experiment key to its claimed slice. The error names BOTH sides of a collision
// and the exact overlapping range, because "layer full" tells an operator nothing about what to
// move.
func ValidateSlices(layer string, named map[string]LayerSlice) error {
	type claim struct {
		key string
		s   LayerSlice
	}
	claims := make([]claim, 0, len(named))
	for k, s := range named {
		if s.Empty() {
			continue
		}
		if s.Start < 0 || s.End > 1 || s.Start >= s.End {
			return fmt.Errorf("experiment %q claims [%g, %g) of layer %q, which is not a range inside [0, 1)",
				k, s.Start, s.End, layer)
		}
		claims = append(claims, claim{k, s})
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].s.Start != claims[j].s.Start {
			return claims[i].s.Start < claims[j].s.Start
		}
		return claims[i].key < claims[j].key
	})
	for i := 1; i < len(claims); i++ {
		prev, cur := claims[i-1], claims[i]
		if cur.s.Start < prev.s.End {
			return fmt.Errorf("layer %q is oversubscribed: %q holds [%g, %g) and %q claims [%g, %g), "+
				"so they overlap on [%g, %g) and would share users. Move one, or shrink the other",
				layer, prev.key, prev.s.Start, prev.s.End, cur.key, cur.s.Start, cur.s.End,
				cur.s.Start, min64(prev.s.End, cur.s.End))
		}
	}
	return nil
}

// NextSlice finds room for a new experiment of the given width in a layer.
//
// It packs from the left into the first gap that fits, so allocations stay stable: an experiment
// that ends does not shuffle the ones still running, and a new one lands in freed space rather
// than moving anybody. Returns an error naming the free space when the layer cannot fit it,
// because "no room" without a number is not something an operator can act on.
func NextSlice(layer string, existing map[string]LayerSlice, width float64) (LayerSlice, error) {
	if width <= 0 || width > 1 {
		return LayerSlice{}, fmt.Errorf("a slice must be a share of the layer between 0 and 1, got %g", width)
	}
	taken := make([]LayerSlice, 0, len(existing))
	for _, s := range existing {
		if !s.Empty() {
			taken = append(taken, s)
		}
	}
	sort.Slice(taken, func(i, j int) bool { return taken[i].Start < taken[j].Start })

	cursor, free := 0.0, 0.0
	for _, s := range taken {
		if gap := s.Start - cursor; gap >= width {
			return LayerSlice{Start: cursor, End: cursor + width}, nil
		} else if gap > 0 {
			free += gap
		}
		if s.End > cursor {
			cursor = s.End
		}
	}
	if 1-cursor >= width {
		return LayerSlice{Start: cursor, End: cursor + width}, nil
	}
	free += 1 - cursor
	return LayerSlice{}, fmt.Errorf("layer %q has no contiguous room for %.0f%% of traffic "+
		"(%.0f%% is free but split into gaps too small to hold it) — stop an experiment or ask for less",
		layer, width*100, free*100)
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
