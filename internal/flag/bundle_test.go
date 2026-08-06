package flag

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

// THE SECURITY PROPERTY. Publishing a definition publishes the VALUES inside its rules —
// `plan in ["enterprise","legacy_pro"]` names your customers to anyone who views source. So the
// default has to be that nothing is published, and the opt-in has to be per flag.
func TestNothingIsPublishedWithoutAnExplicitOptIn(t *testing.T) {
	flags := []Flag{
		{Key: "secret_targeting", Enabled: true, Rules: []Rule{{
			RolloutPct: 100,
			Filters:    []query.Filter{{Property: "plan", Op: query.In, Value: []any{"enterprise", "legacy_pro"}}},
		}}},
		{Key: "published", Enabled: true, Local: true},
	}
	b := BuildBundle(flags, 30)
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "legacy_pro") {
		t.Fatal("a flag that did not opt in leaked its targeting values into the bundle")
	}
	if len(b.F) != 1 || b.F[0].K != "published" {
		t.Fatalf("only the opted-in flag should appear, got %+v", b.F)
	}
	// And the client must be TOLD other flags exist, or it will treat them as off.
	if !b.Rest {
		t.Fatal("enabled flags exist outside the bundle and `rest` did not say so — a client would " +
			"silently disable every server-evaluated flag on the page")
	}
}

// A disabled flag is omitted entirely rather than published as off: the fact that a flag EXISTS is
// itself a disclosure (an unreleased feature name).
func TestDisabledFlagsAreOmittedNotPublishedAsOff(t *testing.T) {
	b := BuildBundle([]Flag{{Key: "unreleased_feature", Enabled: false, Local: true}}, 30)
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "unreleased_feature") {
		t.Fatal("a disabled flag's NAME was published")
	}
	if b.Rest {
		t.Fatal("a disabled flag must not count as 'more flags on the server' — it is not evaluable anywhere")
	}
}

// The numeric comparand ships PRE-PARSED. Go's ParseFloat and JavaScript's Number() disagree on
// real inputs, and every disagreement silently moves a user into a different arm. The client must
// never parse it.
func TestNumericComparandsShipAsGoParsedThem(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want float64
	}{
		{"plain number", 5.0, 5},
		{"numeric string", "42", 42},
		// The zero fallback travels too. Go coerces a non-numeric comparand to 0 and matches
		// accordingly; a client computing NaN instead would disagree on every row.
		{"non-numeric coerces to zero", "abc", 0},
		{"empty string", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := BuildBundle([]Flag{{Key: "f", Enabled: true, Local: true, Rules: []Rule{{
				RolloutPct: 100,
				Filters:    []query.Filter{{Property: "age", Op: query.Gt, Value: tc.val}},
			}}}}, 30)
			f := b.F[0].R[0].F[0]
			if f.N == nil {
				t.Fatal("a gt/lt filter shipped without its pre-parsed number")
			}
			if *f.N != tc.want {
				t.Fatalf("want %v, got %v — the client would re-parse and could disagree", tc.want, *f.N)
			}
		})
	}
	// Non-numeric ops must NOT carry it; a stray number invites a client to use the wrong branch.
	b := BuildBundle([]Flag{{Key: "f", Enabled: true, Local: true, Rules: []Rule{{
		RolloutPct: 100,
		Filters:    []query.Filter{{Property: "plan", Op: query.Eq, Value: "pro"}},
	}}}}, 30)
	if b.F[0].R[0].F[0].N != nil {
		t.Fatal("an eq filter carried a pre-parsed number")
	}
}

// Order is load-bearing in two places, and both are silent when broken: variants are walked in
// order to assign buckets, and rules are first-match-wins.
func TestVariantAndRuleOrderSurviveTheBundle(t *testing.T) {
	f := Flag{Key: "exp", Enabled: true, Local: true,
		Variants: []Variant{{Key: "control", Weight: 1}, {Key: "b", Weight: 2}, {Key: "c", Weight: 1}},
		Rules: []Rule{
			{RolloutPct: 10, Filters: []query.Filter{{Property: "plan", Op: query.Eq, Value: "pro"}}},
			{RolloutPct: 100},
		},
	}
	b := BuildBundle([]Flag{f}, 30)
	got := b.F[0]
	if len(got.V) != 3 || got.V[0].K != "control" || got.V[1].K != "b" || got.V[2].K != "c" {
		t.Fatalf("variant order changed: %+v — every assignment would shift", got.V)
	}
	if got.V[1].W != 2 {
		t.Fatalf("weights must travel verbatim, got %d", got.V[1].W)
	}
	if len(got.R) != 2 || got.R[0].P != 10 || got.R[1].P != 100 {
		t.Fatalf("rule order changed: %+v — first-match-wins would pick a different rule", got.R)
	}
}

// The layer and holdout travel, because they gate eligibility BEFORE targeting. A client missing
// them would put held-out users into experiments.
func TestLayerAndHoldoutTravel(t *testing.T) {
	b := BuildBundle([]Flag{{Key: "e", Enabled: true, Local: true, Experiment: &Experiment{
		Layer: "pricing", Slice: LayerSlice{Start: 0.2, End: 0.5},
		Holdout: "global_2026", HoldoutPct: 12.5,
	}}}, 30)
	f := b.F[0]
	if f.L != "pricing" || len(f.S) != 2 || f.S[0] != 0.2 || f.S[1] != 0.5 {
		t.Fatalf("layer/slice lost: %q %v", f.L, f.S)
	}
	if f.H != "global_2026" || f.HP != 12.5 {
		t.Fatalf("holdout lost: %q %v", f.H, f.HP)
	}
}

// The ETag must be stable for unchanged definitions and must move when they change. A tag that
// churns makes every client re-download a bundle that did not change; a tag that sticks serves
// stale flags forever.
func TestETagIsStableForUnchangedDefinitionsAndMovesWhenTheyChange(t *testing.T) {
	base := []Flag{{Key: "a", Enabled: true, Local: true, Variants: []Variant{{Key: "on", Weight: 1}}}}
	one := BuildBundle(base, 30).ETag
	// A different TTL is not a definition change and must not move the tag.
	two := BuildBundle(base, 600).ETag
	if one != two {
		t.Fatalf("the tag moved on a TTL change (%s vs %s) — every client would re-download", one, two)
	}
	changed := BuildBundle([]Flag{{Key: "a", Enabled: true, Local: true,
		Variants: []Variant{{Key: "on", Weight: 2}}}}, 30).ETag
	if changed == one {
		t.Fatal("a weight change did not move the tag — clients would serve the old split forever")
	}
}

// The bundle declares the hash generation. A client that cannot reproduce the bucketing must
// refuse to try rather than compute a different answer confidently.
func TestTheBundleDeclaresTheHashGeneration(t *testing.T) {
	b := BuildBundle([]Flag{{Key: "a", Enabled: true, Local: true}}, 30)
	if b.HV != CurrentHashVersion {
		t.Fatalf("bundle hash version %d != engine %d", b.HV, CurrentHashVersion)
	}
	if b.V != bundleVersion {
		t.Fatalf("bundle schema version %d != %d", b.V, bundleVersion)
	}
}
