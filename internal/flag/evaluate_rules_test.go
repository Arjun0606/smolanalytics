package flag

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

// Rules promise "evaluated in order, first match wins" in the type's own doc, and they did not
// honour it: a user who matched a rule's filters but lost its rollout `continue`d into the next,
// broader rule. On the shape everyone writes first — "free users get 10%, then everyone else" —
// that turned a 10% rollout into 100%, and the targeting the rule expressed could be bypassed by
// any later catch-all.
func TestMatchedRuleDecidesRatherThanFallingThrough(t *testing.T) {
	f := Flag{Key: "checkout_v2", Enabled: true, Rules: []Rule{
		{Filters: []query.Filter{{Property: "plan", Op: query.Eq, Value: "free"}}, RolloutPct: 10},
		{RolloutPct: 100},
	}}

	const n = 5000
	on := 0
	for i := 0; i < n; i++ {
		if _, isOn := f.Evaluate("free-"+itoa(i), map[string]any{"plan": "free"}); isOn {
			on++
		}
	}
	pct := 100 * float64(on) / float64(n)
	if pct < 7 || pct > 13 {
		t.Errorf("free users served %.1f%% of the time, the rule says 10%% — the catch-all rule below "+
			"is serving users the targeted rule already decided against", pct)
	}

	// The catch-all must still do its job for users the first rule never matched.
	paid := 0
	for i := 0; i < 500; i++ {
		if _, isOn := f.Evaluate("pro-"+itoa(i), map[string]any{"plan": "pro"}); isOn {
			paid++
		}
	}
	if paid != 500 {
		t.Errorf("pro users served %d of 500 — a filter MISS must still advance to the next rule", paid)
	}
}

// The same failure with an explicit 0%: "nobody on this segment" is a decision, not a shrug, and
// it must not be overturned by a later rule.
func TestZeroRolloutOnAMatchedRuleBlocks(t *testing.T) {
	f := Flag{Key: "beta", Enabled: true, Rules: []Rule{
		{Filters: []query.Filter{{Property: "country", Op: query.Eq, Value: "DE"}}, RolloutPct: 0},
		{RolloutPct: 100},
	}}
	for i := 0; i < 200; i++ {
		if _, on := f.Evaluate("de-"+itoa(i), map[string]any{"country": "DE"}); on {
			t.Fatalf("a DE user was served despite a matching 0%% rule — the catch-all overrode the exclusion")
		}
	}
	if _, on := f.Evaluate("fr-1", map[string]any{"country": "FR"}); !on {
		t.Error("a non-matching user should reach the catch-all rule")
	}
}
