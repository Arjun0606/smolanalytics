package flag

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

func TestEvaluateBasics(t *testing.T) {
	tests := []struct {
		name    string
		flag    Flag
		ctx     map[string]any
		wantOn  bool
		wantVar string // only checked when wantOn
	}{
		{"disabled is always off", Flag{Key: "x", Enabled: false, Rules: []Rule{{RolloutPct: 100}}}, nil, false, ""},
		{"enabled, no rules → on for all", Flag{Key: "x", Enabled: true}, nil, true, "on"},
		{"rollout 100 → on", Flag{Key: "x", Enabled: true, Rules: []Rule{{RolloutPct: 100}}}, nil, true, "on"},
		{"rollout 0 → off", Flag{Key: "x", Enabled: true, Rules: []Rule{{RolloutPct: 0}}}, nil, false, ""},
		{"targeting matches", Flag{Key: "x", Enabled: true, Rules: []Rule{{Filters: []query.Filter{{Property: "plan", Op: query.Eq, Value: "pro"}}, RolloutPct: 100}}}, map[string]any{"plan": "pro"}, true, "on"},
		{"targeting misses → off", Flag{Key: "x", Enabled: true, Rules: []Rule{{Filters: []query.Filter{{Property: "plan", Op: query.Eq, Value: "pro"}}, RolloutPct: 100}}}, map[string]any{"plan": "free"}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, on := tt.flag.Evaluate("u1", tt.ctx)
			if on != tt.wantOn {
				t.Fatalf("on = %v, want %v", on, tt.wantOn)
			}
			if on && v != tt.wantVar {
				t.Fatalf("variant = %q, want %q", v, tt.wantVar)
			}
		})
	}
}

// The same key + distinct_id must always resolve the same way — no randomness, no state.
func TestEvaluateDeterministic(t *testing.T) {
	f := Flag{Key: "checkout_v2", Enabled: true, Rules: []Rule{{RolloutPct: 50}}}
	for _, id := range []string{"user-42", "abc", "9931"} {
		v1, on1 := f.Evaluate(id, nil)
		v2, on2 := f.Evaluate(id, nil)
		if on1 != on2 || v1 != v2 {
			t.Fatalf("non-deterministic for %s: (%q,%v) vs (%q,%v)", id, v1, on1, v2, on2)
		}
	}
}

// A 50% rollout should land roughly half the users on — deterministically, but well-distributed.
func TestRolloutDistribution(t *testing.T) {
	f := Flag{Key: "exp", Enabled: true, Rules: []Rule{{RolloutPct: 50}}}
	on := 0
	const n = 2000
	for i := 0; i < n; i++ {
		if _, isOn := f.Evaluate(fmt.Sprintf("user-%d", i), nil); isOn {
			on++
		}
	}
	pct := float64(on) / float64(n) * 100
	if pct < 42 || pct > 58 {
		t.Fatalf("50%% rollout landed %.1f%% on (want ~50%%)", pct)
	}
}

// Multivariate weights should split roughly by weight, deterministically.
func TestVariantDistribution(t *testing.T) {
	f := Flag{Key: "color", Enabled: true, Variants: []Variant{{Key: "a", Weight: 25}, {Key: "b", Weight: 75}}}
	a := 0
	const n = 2000
	for i := 0; i < n; i++ {
		v, on := f.Evaluate(fmt.Sprintf("user-%d", i), nil)
		if !on {
			t.Fatal("no-rules flag should be on for everyone")
		}
		if v == "a" {
			a++
		} else if v != "b" {
			t.Fatalf("unexpected variant %q", v)
		}
	}
	pct := float64(a) / float64(n) * 100
	if pct < 17 || pct > 33 {
		t.Fatalf("variant a (weight 25) landed %.1f%% (want ~25%%)", pct)
	}
}

// Rollout and variant buckets use different salts, so they must be independent — a user being in
// the rollout says nothing about which variant they get. Sanity: among rolled-in users of a
// multivariate flag, both variants appear.
func TestRolloutAndVariantIndependent(t *testing.T) {
	f := Flag{Key: "combo", Enabled: true, Variants: []Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}}, Rules: []Rule{{RolloutPct: 50}}}
	seen := map[string]int{}
	for i := 0; i < 2000; i++ {
		if v, on := f.Evaluate(fmt.Sprintf("u%d", i), nil); on {
			seen[v]++
		}
	}
	if seen["a"] == 0 || seen["b"] == 0 {
		t.Fatalf("both variants should appear among rolled-in users, got %v", seen)
	}
}

func TestStoreUpsertAndValidation(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "flags.json"))

	// create
	f1, err := s.Save(Flag{Key: "checkout_v2", Enabled: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if f1.Created.IsZero() || f1.Updated.IsZero() {
		t.Fatal("save should stamp created + updated")
	}

	// update same key → upsert (count stays 1, Created preserved)
	f2, err := s.Save(Flag{Key: "checkout_v2", Enabled: false, Description: "rolled back"})
	if err != nil {
		t.Fatal(err)
	}
	if !f2.Created.Equal(f1.Created) {
		t.Fatal("update should preserve the original Created")
	}
	if len(s.List()) != 1 {
		t.Fatalf("upsert should not duplicate, got %d", len(s.List()))
	}

	// SetEnabled toggles
	if f, _ := s.SetEnabled("checkout_v2", true); !f.Enabled {
		t.Fatal("SetEnabled(true) should enable")
	}

	// validation
	if _, err := s.Save(Flag{Key: ""}); err == nil {
		t.Fatal("empty key must be rejected")
	}
	if _, err := s.Save(Flag{Key: "bad", Rules: []Rule{{RolloutPct: 150}}}); err == nil {
		t.Fatal("rollout > 100 must be rejected")
	}
	if _, err := s.Save(Flag{Key: "bad", Variants: []Variant{{Key: "a", Weight: 0}}}); err == nil {
		t.Fatal("variants with no positive weight must be rejected")
	}
}

func TestStorePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	s, _ := Open(path)
	if _, err := s.Save(Flag{Key: "beta", Enabled: true, Variants: []Variant{{Key: "a", Weight: 30}, {Key: "b", Weight: 70}}, Rules: []Rule{{RolloutPct: 25}}}); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(path)
	got, ok := s2.Get("beta")
	if !ok || !got.Enabled || len(got.Variants) != 2 || got.Variants[1].Weight != 70 || len(got.Rules) != 1 || got.Rules[0].RolloutPct != 25 {
		t.Fatalf("flag did not round-trip through persist/reload: %+v", got)
	}
}
