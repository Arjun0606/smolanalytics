package flag

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var startedAt = time.Date(2026, 7, 21, 9, 14, 30, 0, time.UTC)

// planMargin states a guardrail margin. It has to be a pointer: nil is "unstated, take the 10%
// default" and an explicit 0 is "prove this got no worse at all", which is refused by name. Tests
// that want to say a number have to say it through here.
func planMargin(f float64) *float64 { return &f }

// basePlan is the fixture every hash test reconstructs by other means. Deliberately full: a
// guardrail with an explicit margin and one without, two secondary metrics out of alphabetical
// order, a CUPED block, and a complete power design so n_tune resolves from n_planned.
func basePlan() Experiment {
	return Experiment{
		Goal:    "purchase",
		Control: "control",
		Guardrails: []Guardrail{
			{Event: "$exception", Direction: GuardrailNotBetter, MarginPct: planMargin(2)},
			{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(DefaultGuardrailMarginPct)},
		},
		Secondary:         []string{"activation", "$pageview"},
		Mode:              ModeSequential,
		Alpha:             0.05,
		Power:             0.8,
		BaselinePct:       12.5,
		MDEPct:            5,
		NPlanned:          12400,
		RandomisationUnit: UnitBucketID,
		CUPED:             &CUPEDCfg{Enabled: true, LookbackDays: 7, Covariate: "purchase"},
		HashVersion:       1,
	}
}

func baseVariants() []Variant {
	return []Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}
}

// startedPlan is basePlan, locked, as Start would produce it.
func startedPlan(t *testing.T) Experiment {
	t.Helper()
	p := basePlan()
	got, err := StartExperiment(&p, baseVariants(), 0, startedAt)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return *got
}

// ---------------------------------------------------------------------------
// The plan hash. This is the file's whole product: if the hash drifts, a receipt
// reports an amendment nobody made, and the lock becomes theatre.
// ---------------------------------------------------------------------------

// TestPlanHashIsStableAcrossMarshalling puts the same plan through every route it actually travels
// in production — JSON, disk, and reconstruction field by field — rather than hashing one value
// twice, which is what a version of this test that passes on drifting code looks like.
func TestPlanHashIsStableAcrossMarshalling(t *testing.T) {
	base := startedPlan(t)
	want := base.ComputePlanHash()
	if want != base.PlanHash {
		t.Fatalf("Start stamped %s but the plan hashes to %s", short(base.PlanHash), short(want))
	}

	t.Run("json round trip", func(t *testing.T) {
		b, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		var back Experiment
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if got := back.ComputePlanHash(); got != want {
			t.Errorf("hash changed through json.Marshal/Unmarshal: %s → %s", short(want), short(got))
		}
	})

	t.Run("disk round trip", func(t *testing.T) {
		// The real path: the plan is written into .flags.json and read back on boot. A time with a
		// monotonic reading and nanoseconds goes out; a plain wall time comes back.
		path := filepath.Join(t.TempDir(), "flags.json")
		b, err := json.MarshalIndent([]Experiment{base}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var back []Experiment
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if got := back[0].ComputePlanHash(); got != want {
			t.Errorf("hash changed through disk: %s → %s", short(want), short(got))
		}
	})

	t.Run("reconstructed field by field", func(t *testing.T) {
		// Everything here is the SAME PLAN said differently: fields assigned in another order, the
		// start instant expressed in a +02:00 zone, floats written as arithmetic rather than
		// literals, the guardrail set and the secondary set listed in the opposite order, and the
		// non-hashed bookkeeping (plan_hash, locked, stopped, amendments) deliberately wrong.
		var e Experiment
		e.HashVersion = 1
		e.CUPED = &CUPEDCfg{Covariate: "purchase", LookbackDays: 14 / 2, Enabled: true}
		e.Secondary = []string{"$pageview", "activation"}
		e.Guardrails = []Guardrail{
			{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(1000.0 / 100.0)},
			{Event: "$exception", Direction: GuardrailNotBetter, MarginPct: planMargin(200.0 / 100.0)},
		}
		e.RandomisationUnit = UnitBucketID
		e.NPlanned = 12400
		e.MDEPct = 500.0 / 100.0
		e.BaselinePct = 125.0 / 10.0
		e.Power = 8e-1
		e.Alpha = 5e-2
		e.Mode = ModeSequential
		e.Control = "control"
		e.Goal = "purchase"
		e.Started = time.Date(2026, 7, 21, 11, 14, 30, 0, time.FixedZone("CEST", 2*60*60))
		e.NTune = 0 // resolves back to n_planned
		e.PlanHash = "deadbeefdeadbeef"
		e.Locked = false
		e.Stopped = startedAt.Add(72 * time.Hour)
		e.Amendments = []Amendment{{At: startedAt, Reason: "irrelevant to the plan itself"}}

		if got := e.ComputePlanHash(); got != want {
			t.Errorf("the same plan, reconstructed field by field, hashed differently: %s → %s\n want canon: %s\n got canon:  %s",
				short(want), short(got), base.canonicalPlan(), e.canonicalPlan())
		}
	})

	t.Run("sub-second start times hash the same", func(t *testing.T) {
		// A time.Now() value carries nanoseconds; the same instant off disk may not. Pinning the
		// precision at one second is what stops that from reading as an amendment.
		e := base
		e.Started = base.Started.Add(123 * time.Nanosecond)
		if got := e.ComputePlanHash(); got != want {
			t.Errorf("123ns changed the plan hash: %s → %s", short(want), short(got))
		}
	})

	t.Run("raw json with shuffled keys", func(t *testing.T) {
		raw := `{
		  "locked": true,
		  "plan_hash": "not-this",
		  "hash_version": 1,
		  "started": "2026-07-21T09:14:30Z",
		  "cuped": {"lookback_days": 7, "covariate": "purchase", "enabled": true},
		  "randomisation_unit": "bucket_id",
		  "n_planned": 12400,
		  "mde_pct": 5,
		  "baseline_pct": 1.25e1,
		  "power": 0.8,
		  "alpha": 5e-2,
		  "mode": "sequential",
		  "secondary": ["$pageview", "activation"],
		  "guardrails": [
		    {"event": "signup", "direction": "not_worse", "margin_pct": 10},
		    {"event": "$exception", "direction": "not_better", "margin_pct": 2}
		  ],
		  "control": "control",
		  "goal": "purchase"
		}`
		var e Experiment
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
		if got := e.ComputePlanHash(); got != want {
			t.Errorf("key order in the wire JSON changed the plan hash: %s → %s", short(want), short(got))
		}
	})
}

// hashedKeys reads the key set straight out of the canonical form, so the coverage tests below
// track the encoder rather than a list someone remembered to update.
func hashedKeys(t *testing.T) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(basePlan().canonicalPlan(), &m); err != nil {
		t.Fatalf("canonicalPlan is not parseable JSON: %v\n%s", err, basePlan().canonicalPlan())
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEveryPlanFieldIsHashedOrExplicitlyExcluded is the drift gate. Adding a field to Experiment
// and forgetting canonicalPlan gives a plan that can be changed without changing its hash — a lock
// that silently stops locking. This fails until the new field is either hashed or listed as
// deliberately excluded.
func TestEveryPlanFieldIsHashedOrExplicitlyExcluded(t *testing.T) {
	// Excluded on purpose; see canonicalPlan's comment for why each one is.
	excluded := map[string]bool{"stopped": true, "plan_hash": true, "locked": true, "amendments": true}
	// n_tune_source is derived, so it is hashed without being a settable field of its own.
	derived := map[string]bool{"n_tune_source": true}

	hashed := map[string]bool{}
	for _, k := range hashedKeys(t) {
		hashed[k] = true
	}

	rt := reflect.TypeOf(Experiment{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if name == "" {
			t.Fatalf("field %s has no json tag, so it has no canonical name", rt.Field(i).Name)
		}
		if !hashed[name] && !excluded[name] {
			t.Errorf("Experiment.%s (json %q) is neither hashed by canonicalPlan nor listed as excluded — a plan field that does not reach the hash can be changed after start without any receipt noticing", rt.Field(i).Name, name)
		}
		if hashed[name] && excluded[name] {
			t.Errorf("field %q is both hashed and excluded", name)
		}
		delete(hashed, name)
	}
	for k := range hashed {
		if !derived[k] {
			t.Errorf("canonicalPlan hashes %q, which is not a field of Experiment", k)
		}
	}
}

// planMutators changes exactly one hashed term of the plan, keyed by its canonical name.
func planMutators() map[string]func(*Experiment) {
	return map[string]func(*Experiment){
		"goal":       func(e *Experiment) { e.Goal = "signup" },
		"control":    func(e *Experiment) { e.Control = "treatment" },
		"guardrails": func(e *Experiment) { e.Guardrails[0].MarginPct = planMargin(3) },
		"secondary":  func(e *Experiment) { e.Secondary = []string{"activation"} },
		"mode":       func(e *Experiment) { e.Mode = ModeFixed },
		"alpha":      func(e *Experiment) { e.Alpha = 0.01 },
		"power":      func(e *Experiment) { e.Power = 0.9 },
		"n_tune":     func(e *Experiment) { e.NTune = 999 },
		// Same N*, but now the operator's own number rather than the calculator's. Clearing the
		// source is what makes that statement: Resolve is idempotent by design (a stored plan
		// re-resolves to itself, or simply loading and saving one would trip its own lock), so
		// re-deriving provenance from NTune alone is not possible — a plan that has already been
		// resolved always has NTune set, and "the operator typed this" and "a previous Resolve
		// filled it" are the same bytes.
		"n_tune_source":      func(e *Experiment) { e.NTune, e.NTuneSource = e.NPlanned, "" },
		"baseline_pct":       func(e *Experiment) { e.BaselinePct = 12.6 },
		"mde_pct":            func(e *Experiment) { e.MDEPct = 6 },
		"n_planned":          func(e *Experiment) { e.NPlanned = 12401 },
		"randomisation_unit": func(e *Experiment) { e.RandomisationUnit = UnitDistinctID },
		"cuped":              func(e *Experiment) { e.CUPED = nil },
		"layer":              func(e *Experiment) { e.Layer = "checkout" },
		"hash_version":       func(e *Experiment) { e.HashVersion = 2 },
		"started":            func(e *Experiment) { e.Started = startedAt.Add(time.Hour) },
	}
}

// TestEveryHashedFieldChangesTheHash proves the encoder actually reads each term. A field that is
// written into the canonical bytes but never varies (a typo pointing at the wrong struct member,
// say) would pass the coverage test above and fail this one.
func TestEveryHashedFieldChangesTheHash(t *testing.T) {
	muts := planMutators()

	var covered []string
	for k := range muts {
		covered = append(covered, k)
	}
	sort.Strings(covered)
	if want := hashedKeys(t); !reflect.DeepEqual(covered, want) {
		t.Fatalf("mutator coverage drifted from the canonical key set:\n mutators: %v\n hashed:   %v", covered, want)
	}

	base := startedPlan(t)
	want := base.ComputePlanHash()
	for name, mut := range muts {
		e := base
		e.Guardrails = append([]Guardrail(nil), base.Guardrails...) // don't mutate the shared backing array
		e.Secondary = append([]string(nil), base.Secondary...)
		mut(&e)
		if got := e.ComputePlanHash(); got == want {
			t.Errorf("changing %s did not change the plan hash — that term never reaches the receipt", name)
		}
	}
}

// TestExcludedFieldsDoNotChangeTheHash is the other half. If stopping an experiment or recording an
// amendment moved the hash, every receipt issued while it ran would break for a reason that has
// nothing to do with the numbers.
func TestExcludedFieldsDoNotChangeTheHash(t *testing.T) {
	base := startedPlan(t)
	want := base.ComputePlanHash()
	for name, mut := range map[string]func(*Experiment){
		"stopped":    func(e *Experiment) { e.Stopped = startedAt.Add(96 * time.Hour) },
		"locked":     func(e *Experiment) { e.Locked = false },
		"plan_hash":  func(e *Experiment) { e.PlanHash = "0000" },
		"amendments": func(e *Experiment) { e.Amendments = []Amendment{{At: startedAt, Reason: "x"}} },
	} {
		e := base
		mut(&e)
		if got := e.ComputePlanHash(); got != want {
			t.Errorf("%s is excluded from the plan hash but changed it: %s → %s", name, short(want), short(got))
		}
	}
}

func TestStoppingDoesNotChangeThePlanHash(t *testing.T) {
	p := startedPlan(t)
	stopped, err := StopExperiment(&p, startedAt.Add(240*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.PlanHash != p.PlanHash {
		t.Errorf("stopping changed the plan hash %s → %s; every receipt issued while it ran would stop verifying", short(p.PlanHash), short(stopped.PlanHash))
	}
	if stopped.Stopped.IsZero() {
		t.Error("Stop did not record when it stopped")
	}
	if _, err := StopExperiment(stopped, startedAt.Add(300*time.Hour)); err == nil {
		t.Error("stopping an already-stopped experiment should be refused")
	}
	if _, err := StopExperiment(&p, time.Time{}); err == nil {
		t.Error("Stop must refuse a zero instant rather than reaching for the clock")
	}
	if _, err := StopExperiment(&p, startedAt.Add(-time.Hour)); err == nil {
		t.Error("stopping before starting should be refused")
	}
}

func TestCanonicalFloatFormatIsValueBased(t *testing.T) {
	// 0.05 typed as a literal, computed by division, and parsed from "5e-2" are one float64. If the
	// encoder used shortest-repr formatting of whatever the caller wrote, they could differ.
	var fromWire struct {
		A float64 `json:"a"`
	}
	if err := json.Unmarshal([]byte(`{"a":5e-2}`), &fromWire); err != nil {
		t.Fatal(err)
	}
	for _, f := range []float64{0.05, 5.0 / 100.0, fromWire.A} {
		if got, want := canonFloat(f), canonFloat(0.05); got != want {
			t.Errorf("canonFloat(%v) = %s, want %s", f, got, want)
		}
	}
	if canonFloat(0) != canonFloat(math.Copysign(0, -1)) {
		t.Error("negative zero hashes differently from zero")
	}
	// math.Nextafter, not a hand-written literal: 0.050000000000000003 parses to the SAME float64
	// as 0.05 (they are well inside one ULP of each other), so the original assertion demanded
	// that canonFloat distinguish one value from itself.
	if canonFloat(0.05) == canonFloat(math.Nextafter(0.05, 1)) {
		t.Error("canonFloat collapsed two distinct float64 values — a real change would hash the same")
	}
}

// ---------------------------------------------------------------------------
// The clock never appears in this file
// ---------------------------------------------------------------------------

// TestNoClockInsidePlanCode reads the source. A pure function that calls time.Now() gives the same
// plan a different hash on every call, which makes the receipt it anchors unverifiable by
// construction — so the rule is enforced, not documented.
func TestNoClockInsidePlanCode(t *testing.T) {
	src, err := os.ReadFile("experiment.go")
	if err != nil {
		t.Fatal(err)
	}
	// Comments are stripped first. This file DOCUMENTS the rule at length — "a pure function that
	// calls time.Now() produces a different plan hash on every call" — and a raw substring scan
	// flagged its own explanation as a violation. A guard that fires on the prose describing it
	// gets deleted by the next person, which is how the real rule stops being enforced.
	if strings.Contains(stripGoComments(string(src)), "time.Now(") {
		t.Error("experiment.go calls time.Now(); windows and instants are resolved at the API/MCP boundary and passed in, so the same plan always hashes the same way")
	}
}

func TestStartRequiresAnExplicitInstantAndLocks(t *testing.T) {
	p := basePlan()
	if _, err := StartExperiment(&p, baseVariants(), 0, time.Time{}); err == nil {
		t.Error("Start must refuse a zero instant rather than reaching for the clock")
	}
	got, err := StartExperiment(&p, baseVariants(), 0, startedAt.Add(750*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Locked {
		t.Error("Start did not lock the plan")
	}
	if !got.Started.Equal(startedAt) {
		t.Errorf("Started = %s, want it truncated to %s so the stored value is the one that was hashed", rfc(got.Started), rfc(startedAt))
	}
	if got.PlanHash == "" || got.PlanHash != got.ComputePlanHash() {
		t.Errorf("Start stamped plan_hash %q, which is not this plan's hash", got.PlanHash)
	}
	if _, err := StartExperiment(got, baseVariants(), 0, startedAt.Add(time.Hour)); err == nil {
		t.Error("restarting a running experiment should be refused")
	}
}

// ---------------------------------------------------------------------------
// The lock
// ---------------------------------------------------------------------------

func TestLockedPlanRejectsGoalChange(t *testing.T) {
	old := startedPlan(t)
	next := old
	next.Goal = "signup"

	err := ValidatePlanChange("checkout_v2", &old, &next, baseVariants(), baseVariants(), 4102)
	if err == nil {
		t.Fatal("a locked plan accepted a new goal — the result would have been chosen after seeing the data")
	}
	for _, want := range []string{"checkout_v2", "2026-07-21", "4,102", "goal", "amend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q, so the reader cannot act on it: %v", want, err)
		}
	}
}

func TestLockedPlanRejectsWeightChange(t *testing.T) {
	old := startedPlan(t)
	next := old
	skewed := []Variant{{Key: "control", Weight: 90}, {Key: "treatment", Weight: 10}}

	err := ValidatePlanChange("checkout_v2", &old, &next, baseVariants(), skewed, 4102)
	if err == nil {
		t.Fatal("a locked experiment accepted a mid-flight weight edit — the exact change the SRM verdict blames for split mismatches")
	}
	if !strings.Contains(err.Error(), "variant weights") {
		t.Errorf("refusal does not name the weights: %v", err)
	}
}

// TestLockedPlanRejectsEveryPlanField walks the same mutator table the hash tests use, so a new
// plan term is locked the moment it is hashed rather than whenever someone remembers.
func TestLockedPlanRejectsEveryPlanField(t *testing.T) {
	old := startedPlan(t)
	for name, mut := range planMutators() {
		if name == "started" {
			continue // re-dating has its own refusal, asserted below
		}
		next := old
		next.Guardrails = append([]Guardrail(nil), old.Guardrails...)
		next.Secondary = append([]string(nil), old.Secondary...)
		mut(&next)
		if name == "layer" || name == "hash_version" {
			// These two also change the bucketing, so they are refused with a harder reason. Check
			// them at zero exposures, where only the lock is in play.
			if err := ValidatePlanChange("checkout_v2", &old, &next, baseVariants(), baseVariants(), 0); err == nil {
				t.Errorf("a locked plan accepted a change to %s", name)
			}
			continue
		}
		if err := ValidatePlanChange("checkout_v2", &old, &next, baseVariants(), baseVariants(), 4102); err == nil {
			t.Errorf("a locked plan accepted a change to %s", name)
		}
	}
}

func TestLockedPlanAllowsAnIdenticalSave(t *testing.T) {
	old := startedPlan(t)
	next := old
	if err := ValidatePlanChange("checkout_v2", &old, &next, baseVariants(), baseVariants(), 4102); err != nil {
		t.Fatalf("re-saving an unchanged flag was refused: %v", err)
	}
	// A plan that came off the wire unresolved (no mode, no alpha, no n_tune) is the same plan.
	loose := old
	loose.Mode, loose.Alpha, loose.NTune, loose.NTuneSource, loose.RandomisationUnit = "", 0, 0, "", ""
	// The guardrails are a SET: listing them the other way round is the same plan.
	loose.Guardrails = []Guardrail{
		{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(DefaultGuardrailMarginPct)},
		{Event: "$exception", Direction: GuardrailNotBetter, MarginPct: planMargin(2)},
	}
	if err := ValidatePlanChange("checkout_v2", &old, &loose, baseVariants(), baseVariants(), 4102); err != nil {
		t.Fatalf("a plan that only omitted its defaults tripped the lock: %v", err)
	}
}

func TestLockedPlanRejectsEscapeHatches(t *testing.T) {
	old := startedPlan(t)

	unlocked := old
	unlocked.Locked = false
	if err := ValidatePlanChange("checkout_v2", &old, &unlocked, baseVariants(), baseVariants(), 4102); err == nil {
		t.Error("a running experiment could be unlocked, which is the refused edit with an extra step")
	}

	redated := old
	redated.Started = startedAt.Add(48 * time.Hour)
	if err := ValidatePlanChange("checkout_v2", &old, &redated, baseVariants(), baseVariants(), 4102); err == nil {
		t.Error("a running experiment could be re-dated")
	}

	forged := old
	forged.PlanHash = strings.Repeat("a", 64)
	if err := ValidatePlanChange("checkout_v2", &old, &forged, baseVariants(), baseVariants(), 4102); err == nil {
		t.Error("a client could supply its own plan_hash and re-anchor every receipt quoting it")
	}

	if err := ValidatePlanChange("checkout_v2", &old, nil, baseVariants(), baseVariants(), 4102); err == nil {
		t.Error("a running experiment's plan could be deleted outright")
	}
}

func TestUnlockedPlanIsEditableButStillValidated(t *testing.T) {
	draft := basePlan()
	next := draft
	next.Goal = "signup"
	if err := ValidatePlanChange("checkout_v2", &draft, &next, baseVariants(), baseVariants(), 0); err != nil {
		t.Fatalf("an unstarted plan should be freely editable: %v", err)
	}
	broken := draft
	broken.Control = "nobody"
	if err := ValidatePlanChange("checkout_v2", &draft, &broken, baseVariants(), baseVariants(), 0); err == nil {
		t.Error("an unstarted plan naming a control that is not an arm was accepted")
	}
	if err := ValidatePlanChange("checkout_v2", nil, nil, nil, baseVariants(), 0); err != nil {
		t.Errorf("a flag with no experiment at all should save: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Layers: immutable once anyone is exposed
// ---------------------------------------------------------------------------

func TestLayerCannotBeSetOnceAnyoneIsExposed(t *testing.T) {
	// Fresh flag, nobody bucketed yet: fine.
	p := basePlan()
	p.Layer = "checkout"
	if _, err := StartExperiment(&p, baseVariants(), 0, startedAt); err != nil {
		t.Fatalf("declaring a layer before the first exposure should be allowed: %v", err)
	}

	withLayer := basePlan()
	withLayer.Layer = "checkout"
	if _, err := StartExperiment(&withLayer, baseVariants(), 1, startedAt); err == nil {
		t.Error("a layer was accepted on a flag that already has an exposure — every one of those users would be re-randomised")
	}

	// And through the save path, in all three shapes: added, changed, removed. All three move
	// every already-bucketed user, so all three are refused.
	unlayered := startedPlan(t)
	layered := startedPlan(t)
	layered.Layer = "checkout"

	cases := []struct {
		name      string
		old, next Experiment
	}{
		{"added", unlayered, layered},
		{"changed", layered, func() Experiment { e := layered; e.Layer = "pricing"; return e }()},
		{"removed", layered, unlayered},
	}
	for _, c := range cases {
		err := ValidatePlanChange("checkout_v2", &c.old, &c.next, baseVariants(), baseVariants(), 4102)
		if err == nil {
			t.Errorf("layer %s was accepted with 4,102 users exposed", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "salt") || !strings.Contains(err.Error(), "4,102") {
			t.Errorf("layer refusal (%s) does not explain the re-randomisation: %v", c.name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Guardrails
// ---------------------------------------------------------------------------

func TestZeroGuardrailMarginIsRejectedWithTheReason(t *testing.T) {
	p := basePlan()
	p.Guardrails = []Guardrail{{Event: "$exception", Direction: GuardrailNotWorse, MarginPct: planMargin(0)}}
	err := p.Validate(baseVariants())
	if err == nil {
		t.Fatal("a guardrail with δ=0 was accepted; it can essentially never PASS, so it would read INCONCLUSIVE forever")
	}
	for _, want := range []string{"$exception", "superiority", "PASS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not explain why δ=0 is not a guardrail (missing %q): %v", want, err)
		}
	}
}

// TestUnstatedMarginIsTheDefaultAndZeroIsStillRefused is the other half of the δ=0 rule, and it
// only holds because MarginPct is a *float64. An ABSENT margin means "I didn't think about it" and
// takes the 10% default; an explicit 0 means "prove this got no worse at all", which no finite
// sample can do. A plain float64 would make those the same value, and the plan would then either
// invent 10% for someone who asked for zero, or lock a guardrail that can never pass.
func TestUnstatedMarginIsTheDefaultAndZeroIsStillRefused(t *testing.T) {
	authored, src, err := ParseGuardrail([]byte(`{"event":"signup","direction":"not_worse"}`))
	if err != nil {
		t.Fatalf("a guardrail with no margin should get the default at the wire boundary: %v", err)
	}
	if authored.Margin() != DefaultGuardrailMarginPct || src != MarginDefault {
		t.Fatalf("parsed margin %v (%s), want the %v%% default", authored.Margin(), src, DefaultGuardrailMarginPct)
	}
	p := basePlan()
	p.Guardrails = []Guardrail{authored}
	if err := p.Validate(baseVariants()); err != nil {
		t.Errorf("a plan carrying a defaulted guardrail should lock: %v", err)
	}
	if got := p.Resolve().Guardrails[0].Margin(); got != DefaultGuardrailMarginPct {
		t.Errorf("Resolve changed a stated margin to %v", got)
	}

	// A margin that never reached ParseGuardrail (nil pointer) still resolves to the default, and
	// the ECHO states it — a payload rendering "margin: 0%" for a guardrail nobody set is a number
	// the reader would believe.
	unstated := basePlan()
	unstated.Guardrails = []Guardrail{{Event: "signup", Direction: GuardrailNotWorse}}
	if err := unstated.Validate(baseVariants()); err != nil {
		t.Errorf("a guardrail with no margin at all should take the default, not fail: %v", err)
	}
	got := unstated.Resolve().Guardrails[0]
	if got.MarginPct == nil || *got.MarginPct != DefaultGuardrailMarginPct {
		t.Errorf("Resolve left the margin unstated (%v); the payload has to name the number it used", got.MarginPct)
	}

	// Resolve must not reach back through the caller's pointer: a locked plan whose margin can be
	// changed from outside no longer hashes to the plan_hash printed on its own receipt.
	shared := 4.0
	aliased := basePlan()
	aliased.Guardrails = []Guardrail{{Event: "signup", Direction: GuardrailNotWorse, MarginPct: &shared}}
	resolved := aliased.Resolve()
	before := resolved.ComputePlanHash()
	shared = 9.0
	if resolved.Guardrails[0].Margin() != 4 || resolved.ComputePlanHash() != before {
		t.Error("mutating the caller's margin pointer changed the resolved plan and its hash")
	}

	// And an explicit zero is refused rather than quietly rounded up to the default.
	zeroed := basePlan()
	zeroed.Guardrails = []Guardrail{{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(0)}}
	if err := zeroed.Validate(baseVariants()); err == nil {
		t.Error("a plan holding an explicit δ=0 guardrail was accepted; it can never PASS, so it would read INCONCLUSIVE forever")
	}
}

// TestUnstatedAndDefaultMarginsHashTheSame guards the seam between the pointer and the hash. The
// same guardrail written two ways — margin omitted, margin written as 10 — is one plan, and hashing
// them differently would report an amendment nobody made the first time the plan passed through a
// client that fills its own defaults. An explicit 0 is a DIFFERENT plan and must still hash apart.
func TestUnstatedAndDefaultMarginsHashTheSame(t *testing.T) {
	mk := func(m *float64) Experiment {
		p := basePlan()
		p.Guardrails = []Guardrail{{Event: "signup", Direction: GuardrailNotWorse, MarginPct: m}}
		return p
	}
	unstated := mk(nil).ComputePlanHash()
	stated := mk(planMargin(DefaultGuardrailMarginPct)).ComputePlanHash()
	if unstated != stated {
		t.Errorf("an omitted margin and an explicit %v%% hashed differently: %s vs %s",
			DefaultGuardrailMarginPct, short(unstated), short(stated))
	}
	if zero := mk(planMargin(0)).ComputePlanHash(); zero == unstated {
		t.Error("δ=0 hashed the same as an unstated margin — two opposite requests, one receipt")
	}

	// Direction is defaulted at the wire boundary too, so an omitted "not_worse" is the same plan.
	implicit := basePlan()
	implicit.Guardrails = []Guardrail{{Event: "signup", MarginPct: planMargin(2)}}
	explicit := basePlan()
	explicit.Guardrails = []Guardrail{{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(2)}}
	if implicit.ComputePlanHash() != explicit.ComputePlanHash() {
		t.Error("an omitted direction hashed differently from an explicit not_worse")
	}
}

func TestGuardrailValidationRejectsNonsense(t *testing.T) {
	for name, g := range map[string]Guardrail{
		"no event":      {Event: "", MarginPct: planMargin(2)},
		"bad direction": {Event: "$exception", Direction: "up", MarginPct: planMargin(2)},
		"negative":      {Event: "$exception", Direction: GuardrailNotWorse, MarginPct: planMargin(-2)},
		"whole metric":  {Event: "$exception", Direction: GuardrailNotWorse, MarginPct: planMargin(100)},
		"not a number":  {Event: "$exception", Direction: GuardrailNotWorse, MarginPct: planMargin(math.NaN())},
	} {
		p := basePlan()
		p.Guardrails = []Guardrail{g}
		if err := p.Validate(baseVariants()); err == nil {
			t.Errorf("guardrail %q was accepted", name)
		}
	}
	dup := basePlan()
	dup.Guardrails = []Guardrail{
		{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(2)},
		{Event: "signup", Direction: GuardrailNotWorse, MarginPct: planMargin(2)},
	}
	if err := dup.Validate(baseVariants()); err == nil {
		t.Error("the same guardrail declared twice was accepted")
	}
}

// ---------------------------------------------------------------------------
// n_tune, mode, randomisation unit — the numbers the payload has to state
// ---------------------------------------------------------------------------

func TestNTuneComesFromNPlannedWhenTheDesignIsDeclared(t *testing.T) {
	p := basePlan().Resolve() // baseline + MDE + power + n_planned all present
	if p.NTune != 12400 || p.NTuneSource != NTuneFromPlanned {
		t.Errorf("n_tune = %d (%s), want 12400 from n_planned — the confidence sequence is narrowest at N*, so a generic 5000 silently widens every interval", p.NTune, p.NTuneSource)
	}
}

func TestNTuneFallsBackTo5000AndSaysSo(t *testing.T) {
	for name, e := range map[string]Experiment{
		"no design":    {Goal: "purchase", Control: "control"},
		"no baseline":  {Goal: "purchase", Control: "control", MDEPct: 5, Power: 0.8, NPlanned: 12400},
		"no mde":       {Goal: "purchase", Control: "control", BaselinePct: 12.5, Power: 0.8, NPlanned: 12400},
		"no power":     {Goal: "purchase", Control: "control", BaselinePct: 12.5, MDEPct: 5, NPlanned: 12400},
		"calc not run": {Goal: "purchase", Control: "control", BaselinePct: 12.5, MDEPct: 5, Power: 0.8},
	} {
		r := e.Resolve()
		if r.NTune != DefaultNTune || r.NTuneSource != NTuneFromDefault {
			t.Errorf("%s: n_tune = %d (%s), want %d from %q", name, r.NTune, r.NTuneSource, DefaultNTune, NTuneFromDefault)
		}
	}
	explicit := basePlan()
	explicit.NTune = 7777
	if r := explicit.Resolve(); r.NTune != 7777 || r.NTuneSource != NTuneFromExplicit {
		t.Errorf("an operator-supplied n_tune resolved to %d (%s)", r.NTune, r.NTuneSource)
	}
}

func TestModeAndUnitDefaultsAreStated(t *testing.T) {
	r := Experiment{Goal: "purchase", Control: "control"}.Resolve()
	if r.Mode != ModeSequential {
		t.Errorf("mode defaulted to %q, want %q — this dashboard invites daily checking and a fixed-horizon p-value read daily is not a 5%% test", r.Mode, ModeSequential)
	}
	if r.RandomisationUnit != UnitBucketID {
		t.Errorf("randomisation unit defaulted to %q, want %q", r.RandomisationUnit, UnitBucketID)
	}
	if r.Alpha != DefaultAlpha || r.Power != DefaultPower || r.HashVersion != CurrentHashVersion {
		t.Errorf("alpha/power/hash_version resolved to %v/%v/%d", r.Alpha, r.Power, r.HashVersion)
	}
}

func TestValidateRejectsIncoherentPlans(t *testing.T) {
	for name, mut := range map[string]func(*Experiment){
		"no goal":            func(e *Experiment) { e.Goal = "" },
		"no control":         func(e *Experiment) { e.Control = " " },
		"control not an arm": func(e *Experiment) { e.Control = "nobody" },
		"bad mode":           func(e *Experiment) { e.Mode = "bayesian" },
		"bad unit":           func(e *Experiment) { e.RandomisationUnit = "session_id" },
		"alpha too big":      func(e *Experiment) { e.Alpha = 0.9 },
		"alpha negative":     func(e *Experiment) { e.Alpha = -0.05 },
		"power at 1":         func(e *Experiment) { e.Power = 1 },
		"negative mde":       func(e *Experiment) { e.MDEPct = -5 },
		"baseline over 100":  func(e *Experiment) { e.BaselinePct = 140 },
		"nan alpha":          func(e *Experiment) { e.Alpha = math.NaN() },
		"unknown hash":       func(e *Experiment) { e.HashVersion = 9 },
		"secondary is goal":  func(e *Experiment) { e.Secondary = []string{"purchase"} },
		"secondary twice":    func(e *Experiment) { e.Secondary = []string{"activation", "activation"} },
		"cuped no lookback":  func(e *Experiment) { e.CUPED = &CUPEDCfg{Enabled: true, LookbackDays: -1, Covariate: "x"} },
		"stopped first":      func(e *Experiment) { e.Started = startedAt; e.Stopped = startedAt.Add(-time.Hour) },
	} {
		p := basePlan()
		mut(&p)
		if err := p.Validate(baseVariants()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := basePlan().Validate(baseVariants()); err != nil {
		t.Errorf("the fixture plan should be valid: %v", err)
	}
	if err := basePlan().Validate(nil); err != nil {
		t.Errorf("a plan should validate without a variant list to check the control against: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Amendments
// ---------------------------------------------------------------------------

func TestAmendmentIsRecordedAndTravelsWithThePlan(t *testing.T) {
	old := startedPlan(t)
	next := old
	next.Goal = "signup"
	at := startedAt.Add(96 * time.Hour)

	got, err := AmendExperiment("checkout_v2", &old, &next, baseVariants(), baseVariants(), "the purchase event was renamed upstream on day 3", 4102, at)
	if err != nil {
		t.Fatalf("Amend: %v", err)
	}
	if len(got.Amendments) != 1 {
		t.Fatalf("got %d amendments, want 1", len(got.Amendments))
	}
	a := got.Amendments[0]
	if !reflect.DeepEqual(a.Changed, []string{"goal"}) {
		t.Errorf("amendment names %v as changed, want [goal]", a.Changed)
	}
	if a.FromHash != old.PlanHash || a.ToHash != got.PlanHash || got.PlanHash == old.PlanHash {
		t.Errorf("amendment does not bracket the plan hashes: %s → %s (plan now %s)", short(a.FromHash), short(a.ToHash), short(got.PlanHash))
	}
	if !a.At.Equal(at) || !strings.Contains(a.Reason, "renamed upstream") {
		t.Errorf("amendment lost its when/why: %+v", a)
	}
	if !got.Locked || !got.Started.Equal(old.Started) {
		t.Error("amending unlocked or re-dated the experiment")
	}
	if got.PlanHash != got.ComputePlanHash() {
		t.Error("the amended plan does not hash to its own stamped plan_hash")
	}

	// A second amendment keeps the first: the record is the point.
	second := *got
	second.Alpha = 0.01
	got2, err := AmendExperiment("checkout_v2", got, &second, baseVariants(), baseVariants(), "pre-registered a tighter alpha after the pilot", 5000, at.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("second Amend: %v", err)
	}
	if len(got2.Amendments) != 2 || got2.Amendments[0].Reason != a.Reason {
		t.Errorf("the amendment log is not append-only: %+v", got2.Amendments)
	}
}

func TestAmendRefusesTheCasesThatWouldHideAChange(t *testing.T) {
	old := startedPlan(t)
	changed := old
	changed.Goal = "signup"
	at := startedAt.Add(96 * time.Hour)

	if _, err := AmendExperiment("checkout_v2", &old, &changed, baseVariants(), baseVariants(), "   ", 4102, at); err == nil {
		t.Error("an amendment with no reason was accepted; the reason is what the reader of the result sees")
	}
	if _, err := AmendExperiment("checkout_v2", &old, &changed, baseVariants(), baseVariants(), "why", 4102, time.Time{}); err == nil {
		t.Error("Amend must refuse a zero instant rather than reaching for the clock")
	}
	if _, err := AmendExperiment("checkout_v2", &old, &changed, baseVariants(), baseVariants(), "why", 4102, startedAt.Add(-time.Hour)); err == nil {
		t.Error("an amendment dated before the experiment started was accepted")
	}
	same := old
	if _, err := AmendExperiment("checkout_v2", &old, &same, baseVariants(), baseVariants(), "why", 4102, at); err == nil {
		t.Error("amending nothing was accepted, which would put a meaningless entry in front of every reader")
	}
	draft := basePlan()
	if _, err := AmendExperiment("checkout_v2", &draft, &changed, baseVariants(), baseVariants(), "why", 0, at); err == nil {
		t.Error("an unstarted plan was 'amended' instead of just saved")
	}
	broken := old
	broken.Alpha = 0.9
	if _, err := AmendExperiment("checkout_v2", &old, &broken, baseVariants(), baseVariants(), "why", 4102, at); err == nil {
		t.Error("an amendment to an invalid plan was accepted")
	}
}

// TestBucketingTermsCannotBeAmended is decision 6 at the amend boundary: a reason string can excuse
// a changed goal, because the data is untouched. It can never excuse re-randomising the users who
// already produced that data.
func TestBucketingTermsCannotBeAmended(t *testing.T) {
	old := startedPlan(t)
	at := startedAt.Add(96 * time.Hour)
	for name, mut := range map[string]func(*Experiment){
		"layer":        func(e *Experiment) { e.Layer = "checkout" },
		"hash_version": func(e *Experiment) { e.HashVersion = 2 },
	} {
		next := old
		mut(&next)
		if _, err := AmendExperiment("checkout_v2", &old, &next, baseVariants(), baseVariants(), "a very good reason", 4102, at); err == nil {
			t.Errorf("%s was amendable with 4,102 users exposed", name)
		}
	}
}

func TestPlanCountReadsAsASentence(t *testing.T) {
	for in, want := range map[int]string{0: "0", 42: "42", 999: "999", 4102: "4,102", 1234567: "1,234,567"} {
		if got := planCount(in); got != want {
			t.Errorf("planCount(%d) = %q, want %q", in, got, want)
		}
	}
}

// stripGoComments removes // line comments and /* block */ comments so a source-scanning guard
// tests the CODE rather than the sentences about it.
func stripGoComments(src string) string {
	var out strings.Builder
	for i := 0; i < len(src); i++ {
		if src[i] == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				out.WriteByte('\n')
				continue
			}
			if src[i+1] == '*' {
				i += 2
				for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i++
				continue
			}
		}
		out.WriteByte(src[i])
	}
	return out.String()
}
