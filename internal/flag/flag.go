// Package flag is feature flags for smolanalytics — boolean and multivariate, with property
// targeting and percentage rollouts, evaluated deterministically so the same user always lands
// in the same bucket. What makes it deeper than a plain flag console (a later increment): a
// flag flip is auto-recorded as a deploy marker, so the existing deploy-impact engine answers
// "did flag X move activation?" from your editor, provably. This file is the pure engine —
// types + evaluation — with no I/O, so it's trivially testable and shared verbatim with any SDK
// that copies the bucketing.
package flag

import (
	"hash/fnv"
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
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// Evaluate resolves the flag for one user, given their context properties. Returns the served
// variant ("on" for a boolean flag, "" when off) and whether the flag is on. Deterministic: the
// same key + distinct_id always yields the same result, computed only from a stable hash — no
// randomness, no state — so a client SDK that copies this bucketing agrees byte-for-byte.
func (f Flag) Evaluate(distinctID string, context map[string]any) (string, bool) {
	if !f.Enabled {
		return "", false
	}
	if len(f.Rules) == 0 {
		return f.variantFor(distinctID), true
	}
	ctx := event.Event{Properties: context}
	for _, r := range f.Rules {
		if len(r.Filters) > 0 && !query.Matches(ctx, r.Filters) {
			continue // targeting doesn't match this user
		}
		if r.RolloutPct <= 0 {
			continue // this rule serves no one
		}
		if r.RolloutPct < 100 && bucketPct("rollout:"+f.Key, distinctID) >= r.RolloutPct {
			continue // user falls outside this rule's rollout percentage
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
	r := int(hash32("variant:"+f.Key, distinctID) % uint32(total))
	cum := 0
	for _, v := range f.Variants {
		if v.Weight <= 0 {
			continue
		}
		cum += v.Weight
		if r < cum {
			return v.Key
		}
	}
	return f.Variants[len(f.Variants)-1].Key // unreachable when total>0, but a safe fallback
}

// hash32 is a stable FNV-1a hash of salt:id. The salt (per-flag, per-purpose) keeps a user's
// rollout bucket independent of their variant bucket, so a 50% rollout and a 50/50 variant split
// aren't correlated.
func hash32(salt, id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(salt))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}

// bucketPct maps a user into 0..99 for percentage rollouts.
func bucketPct(salt, id string) int { return int(hash32(salt, id) % 100) }
