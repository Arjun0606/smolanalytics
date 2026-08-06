// Package flag is feature flags for smolanalytics — boolean and multivariate, with property
// targeting and percentage rollouts, evaluated deterministically so the same user always lands
// in the same bucket. What makes it deeper than a plain flag console (a later increment): a
// flag flip is auto-recorded as a deploy marker, so the existing deploy-impact engine answers
// "did flag X move activation?" from your editor, provably. This file is the pure engine —
// types + evaluation — with no I/O, so it's trivially testable and shared verbatim with any SDK
// that copies the bucketing.
package flag

import (
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// Variant is one arm of a multivariate flag; Weight is its relative share (need not sum to 100).
type Variant struct {
	Key    string `json:"key"`
	Weight int    `json:"weight"`
}

// Rule is one ordered targeting clause: the user's context must pass all Filters (empty = every
// user), and RolloutPct (0..100) is the deterministic share of matched users served. Rules are
// evaluated in order, first match wins.
type Rule struct {
	Filters    []query.Filter `json:"filters,omitempty"`
	RolloutPct int            `json:"rollout_pct"`
}

// Flag is a saved feature flag. Variants empty = a boolean flag (served variant is "on"). Rules
// empty = on for everyone when Enabled. Measured opts this flag into exposure logging (a later
// increment) so it can be A/B-analysed without every flag inflating the event count.
type Flag struct {
	Key         string    `json:"key"`
	Description string    `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	Variants    []Variant `json:"variants,omitempty"`
	Rules       []Rule    `json:"rules,omitempty"`
	Measured    bool      `json:"measured,omitempty"`
	// Local publishes this flag's DEFINITION — including the values inside its targeting rules —
	// so a browser or edge worker can evaluate it without a round-trip.
	//
	// Off by default and opted into per flag, because turning it on is a disclosure, not a
	// performance setting: `plan in ["enterprise","legacy_pro"]` tells anyone who views source
	// which customers are on a legacy plan. Any UI for this must describe the consequence rather
	// than the feature.
	Local bool `json:"local,omitempty"`
	// Experiment is the pre-registered analysis plan: which arm is control, what the goal is,
	// the inference mode, alpha, N*, the guardrails and their margins. Nil means "a flag, not an
	// experiment" — Measure then falls back to documented defaults and SAYS SO in the report,
	// rather than pretending a plan existed. Locked once the experiment starts, because a design
	// chosen after seeing the data is not a design.
	Experiment *Experiment `json:"experiment,omitempty"`
	Created    time.Time   `json:"created"`
	Updated    time.Time   `json:"updated"`
}

// Evaluate resolves the flag for one user, given their context properties. Returns the served
// variant ("on" for a boolean flag, "" when off) and whether the flag is on. Deterministic: the
// same key + distinct_id always yields the same result, computed only from a stable hash — no
// randomness, no state — so a client SDK that copies this bucketing agrees byte-for-byte.
func (f Flag) Evaluate(distinctID string, context map[string]any) (string, bool) {
	if !f.Enabled {
		return "", false
	}
	// Layer and holdout eligibility come BEFORE targeting and rollout, because they are
	// statements about which experiments a user may be in at all, not about who this one is for.
	// Both are no-ops unless the experiment declares them, so a flag without a plan behaves
	// exactly as it always has — and both draw on their own salt, so switching either on never
	// moves anyone's variant inside an experiment they stay eligible for.
	if e := f.Experiment; e != nil {
		if InHoldout(e.Holdout, distinctID, e.HoldoutPct) {
			return "", false
		}
		if e.Layer != "" && !InSlice(e.Layer, distinctID, e.Slice) {
			return "", false
		}
	}
	if len(f.Rules) == 0 {
		return f.variantFor(distinctID), true
	}
	ctx := event.Event{Properties: context}
	for _, r := range f.Rules {
		if len(r.Filters) > 0 && !query.Matches(ctx, r.Filters) {
			continue // a filter MISS is the only thing that advances to the next rule
		}
		// Once the filters match, this rule decides — including deciding "no". Every miss used to
		// `continue`, so a user who matched a targeted rule but lost its rollout fell through to
		// the next, broader rule. On the standard shape [{plan==free, 10%}, {catch-all, 100%}],
		// measured over 5,000 free ids: 5000 of 5000 served, against a configured 10%. The
		// rollout on a targeted rule was not a percentage at all, it was decoration, and the
		// targeting it was meant to limit could be bypassed entirely by any later rule.
		if r.RolloutPct <= 0 {
			return "", false // this rule matched and serves no one
		}
		if r.RolloutPct < 100 && !bucketIn("rollout:"+f.Key, distinctID, r.RolloutPct) {
			return "", false // matched, but outside this rule's rollout share
		}
		return f.variantFor(distinctID), true
	}
	return "", false
}

// variantFor picks the served variant. A boolean flag (no variants) serves "on". A multivariate
// flag buckets the user into a variant by weighted share, deterministically.
func (f Flag) variantFor(distinctID string) string {
	total := 0
	for _, v := range f.Variants {
		if v.Weight > 0 {
			total += v.Weight
		}
	}
	if total <= 0 {
		return "on"
	}
	// Hash into a FIXED [0,1) space, then walk cumulative ranges built from the weights.
	//
	// The bucket space must not depend on the weights. This previously did `hash % sum(weights)`,
	// so editing a running experiment from 1:1 to 2:1 changed the modulus from 2 to 3 and
	// re-randomized half of everyone: measured, 5,041 of 10,000 users changed arm, and 1,674 of
	// them LEFT the variant that had just been widened — which is impossible if assignment is
	// positional. Their pre-edit behaviour stayed attributed to whichever arm they ended up in,
	// so the result was quietly wrong with nothing logged.
	//
	// With a fixed space, widening "a" only moves the boundary between a and b. Users already
	// inside a's range keep their assignment and only the share that genuinely changed hands
	// moves. This is the same shape LaunchDarkly, PostHog, Statsig and GrowthBook all converged on.
	r := bucket("variant:"+f.Key, distinctID)
	cum := 0
	for _, v := range f.Variants {
		if v.Weight <= 0 {
			continue
		}
		cum += v.Weight
		if r < float64(cum)/float64(total) {
			return v.Key
		}
	}
	return f.Variants[len(f.Variants)-1].Key // unreachable when total>0, but a safe fallback
}

// bucket maps salt:id to a stable position in [0,1). The salt (per-flag, per-purpose) keeps a
// user's rollout bucket independent of their variant bucket, so a 50% rollout and a 50/50
// variant split aren't correlated.
//
// SHA-256 rather than FNV-1a, and that is a correctness fix rather than a preference. FNV-1a is
// `h = (h ^ byte) * prime` with an odd prime, and multiplying by an odd number can never change
// the low bit — so the final low bit is just the XOR-parity of the input bytes. Taking `% 2` of
// that discards the salt almost entirely: two different flags put the SAME user in the SAME arm
// every time. Measured on the old code, phi between two independent 50/50 experiments was
// exactly +1.0000 — any two concurrent experiments were perfectly confounded and neither could
// be read. GrowthBook shipped this same construction, measured the bias, and retired it.
//
// Taking the top 60 bits follows LaunchDarkly and PostHog, who slice 15 hex chars for the same
// reason: 60 bits divides into a float64 without the result crowding the mantissa, where a full
// 64 would.
func bucket(salt, id string) float64 {
	sum := sha256.Sum256([]byte(salt + ":" + id))
	return float64(binary.BigEndian.Uint64(sum[:8])>>4) / float64(uint64(1)<<60)
}

// bucketIn reports whether salt:id falls inside the first pct percent of the space.
func bucketIn(salt, id string, pct int) bool { return bucket(salt, id) < float64(pct)/100 }
