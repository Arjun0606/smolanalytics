package flag

import (
	"math"
	"strings"
	"testing"
)

func gmargin(f float64) *float64 { return &f }

// grBase is a guardrail input with everything stated, so each test only varies the one thing it is
// about. Fixed mode here (k=1) so the arithmetic in the fixtures is the plain one; the sequential
// composition gets its own test.
func grBase() GuardrailInput {
	return GuardrailInput{
		Guardrail: Guardrail{Event: "checkout", Direction: GuardrailNotWorse, MarginPct: gmargin(2)},
		Variant:   "treatment",
		Alpha:     0.05,
		Mode:      ModeFixed,
		Unit:      UnitBucketID,
	}
}

func TestGuardrailPassFailInconclusive(t *testing.T) {
	cases := []struct {
		name                       string
		dir                        string
		margin                     float64
		cTest, nTest, cCtrl, nCtrl int
		want                       string
	}{
		// 20.0% against 20.0% on 100k users an arm: tight enough to rule out a 2% relative loss.
		{"not_worse ruled out", GuardrailNotWorse, 2, 20000, 100000, 20000, 100000, GuardrailPass},
		// 15% against 20%: a 25% relative loss, and the whole interval sits past the margin.
		{"not_worse clearly breached", GuardrailNotWorse, 2, 1500, 10000, 2000, 10000, GuardrailFail},
		// 9.5% against 10% on 1k users: the range still spans the margin. Not a pass.
		{"not_worse still spans margin", GuardrailNotWorse, 2, 95, 1000, 100, 1000, GuardrailInconclusive},
		// error rate 5.05% against 5.00% on 100k: a 10% relative rise is ruled out.
		{"not_better ruled out", GuardrailNotBetter, 10, 5050, 100000, 5000, 100000, GuardrailPass},
		// errors doubled.
		{"not_better clearly breached", GuardrailNotBetter, 10, 1000, 10000, 500, 10000, GuardrailFail},
		{"not_better still spans margin", GuardrailNotBetter, 10, 505, 10000, 500, 10000, GuardrailInconclusive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := grBase()
			in.Guardrail.Direction = c.dir
			in.Guardrail.MarginPct = gmargin(c.margin)
			in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = c.cTest, c.nTest, c.cCtrl, c.nCtrl
			got, err := EvaluateGuardrail(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != c.want {
				bound := "none"
				if got.Bound != nil {
					bound = trimFloat(got.Bound.Lo) + " to " + trimFloat(got.Bound.Hi)
				}
				t.Fatalf("status = %s, want %s (bound %s, threshold %v)", got.Status, c.want, bound, got.ThresholdPct)
			}
			if got.Read == "" {
				t.Fatal("no read sentence: a verdict nobody can act on is not a verdict")
			}
		})
	}
}

// The honesty test. Every shape that cannot produce a bound must land on INCONCLUSIVE and say so,
// because a guardrail that quietly reads PASS when it has no idea is worse than no guardrail:
// somebody is relying on it.
func TestInconclusiveIsNeverReportedAsPass(t *testing.T) {
	cases := []struct {
		name                       string
		cTest, nTest, cCtrl, nCtrl int
	}{
		{"tiny arms", 2, 10, 3, 12},
		{"tiny control", 200, 2000, 3, 12},
		{"no exposures at all", 0, 0, 0, 0},
		{"nobody converted anywhere", 0, 500, 0, 500},
		{"test arm converted nobody", 0, 500, 60, 500},
		{"control converted nobody", 60, 500, 0, 500},
		{"control indistinguishable from zero", 6, 5000, 1, 5000},
		{"saturated arm", 500, 500, 480, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := grBase()
			in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = c.cTest, c.nTest, c.cCtrl, c.nCtrl
			got, err := EvaluateGuardrail(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status == GuardrailPass {
				t.Fatalf("PASS on %q — a guardrail with no interval must never read as passing", c.name)
			}
			if got.Reason == "" {
				t.Fatal("no reason given for a non-verdict")
			}
			if !strings.Contains(got.Read, "not a pass") {
				t.Fatalf("read does not say it is not a pass: %q", got.Read)
			}
		})
	}
}

// A guardrail wants to be SENSITIVE, so it uses z_{1-α}, not z_{1-α/2}. Using the two-sided value
// would widen the bound ~19% at α=0.05 and hide degradations the guardrail exists to catch.
func TestGuardrailUsesOneSidedBound(t *testing.T) {
	in := grBase()
	in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = 950, 10000, 1000, 10000
	got, err := EvaluateGuardrail(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bound == nil {
		t.Fatal("no bound computed")
	}
	if !got.OneSided {
		t.Fatal("result does not declare itself one-sided")
	}
	if math.Abs(got.Z-1.6448536269514722) > 1e-9 {
		t.Fatalf("z = %v, want z_{0.95} = 1.6448536269514722", got.Z)
	}
	twoSided, why := liftInterval(950, 10000, 1000, 10000, z95)
	if why != liftOK {
		t.Fatal("fixture should produce a lift interval")
	}
	if !(got.Bound.Lo > twoSided.Lo) {
		t.Fatalf("one-sided lower bound %v is not tighter than the two-sided %v at the same alpha",
			got.Bound.Lo, twoSided.Lo)
	}
}

// Binding decision 4. δ=0 is not a conservative default, it is an impossible test: a
// non-inferiority test at margin zero is a one-sided superiority test and can never PASS.
func TestZeroMarginIsRejectedWithAnExplanation(t *testing.T) {
	in := grBase()
	in.Guardrail.MarginPct = gmargin(0)
	in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = 20000, 100000, 20000, 100000
	_, err := EvaluateGuardrail(in)
	if err == nil {
		t.Fatal("a zero margin was accepted")
	}
	for _, want := range []string{"checkout", "superiority", "PASS"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not explain why δ=0 is not a guardrail (missing %q): %v", want, err)
		}
	}
	for name, g := range map[string]Guardrail{
		"no event":      {Event: "  ", MarginPct: gmargin(2)},
		"bad direction": {Event: "x", Direction: "up", MarginPct: gmargin(2)},
		"negative":      {Event: "x", Direction: GuardrailNotWorse, MarginPct: gmargin(-1)},
		"whole metric":  {Event: "x", Direction: GuardrailNotWorse, MarginPct: gmargin(150)},
		"not a number":  {Event: "x", Direction: GuardrailNotWorse, MarginPct: gmargin(math.NaN())},
	} {
		if err := g.Validate(); err == nil {
			t.Errorf("guardrail %q was accepted", name)
		}
	}
	// An UNSET margin is not an error — it is the 10% default, which ParseGuardrail fills.
	if err := (Guardrail{Event: "x", Direction: GuardrailNotWorse}).Validate(); err != nil {
		t.Fatalf("an unstated margin was rejected: %v", err)
	}
	if got := (Guardrail{Event: "x"}).Margin(); got != DefaultGuardrailMarginPct {
		t.Fatalf("Margin() on an unset margin = %v, want the %v default", got, DefaultGuardrailMarginPct)
	}
}

// An ABSENT margin gets the 10% default and says so; an EXPLICIT zero is refused. Those are
// different user intentions and a float64 alone cannot tell them apart, which is why the wire
// boundary parses through here rather than through json.Unmarshal on the struct.
func TestAbsentMarginDefaultsAndExplicitZeroIsRefused(t *testing.T) {
	g, src, err := ParseGuardrail([]byte(`{"event":"$exception","direction":"not_better"}`))
	if err != nil {
		t.Fatal(err)
	}
	if g.MarginPct == nil || *g.MarginPct != DefaultGuardrailMarginPct {
		t.Fatalf("margin = %v, want the %v default", g.MarginPct, DefaultGuardrailMarginPct)
	}
	if src != MarginDefault {
		t.Fatalf("margin source = %q, want %q — a default nobody chose must be labelled", src, MarginDefault)
	}
	if _, _, err := ParseGuardrail([]byte(`{"event":"$exception","margin_pct":0}`)); err == nil {
		t.Fatal("an explicit margin_pct of 0 was accepted")
	}
	g2, src2, err := ParseGuardrail([]byte(`{"event":"checkout","margin_pct":2.5}`))
	if err != nil {
		t.Fatal(err)
	}
	if g2.MarginPct == nil || *g2.MarginPct != 2.5 || src2 != MarginDeclared {
		t.Fatalf("declared margin was rewritten: %v/%s", g2.MarginPct, src2)
	}
	if g2.Direction != GuardrailNotWorse {
		t.Fatalf("direction default = %q, want %q", g2.Direction, GuardrailNotWorse)
	}
	if _, _, err := ParseGuardrail([]byte(`{"event":"checkout","direction":"sideways","margin_pct":2}`)); err == nil {
		t.Fatal("an unknown direction was accepted")
	}
	if _, _, err := ParseGuardrail([]byte(`{"direction":"not_worse","margin_pct":2}`)); err == nil {
		t.Fatal("a guardrail with no event was accepted")
	}
}

// Binding decision 8: sequential is the DEFAULT. Running it without the confidence sequence's k(N)
// would print a fixed-horizon bound under an always-valid label — the peeking bug the mode exists
// to prevent, wearing its cure's name.
func TestSequentialModeRefusesToRunWithoutTheInflation(t *testing.T) {
	in := grBase()
	in.Mode = "" // default: sequential
	in.NTune = 0 // nothing to tune against
	in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = 950, 10000, 1000, 10000
	if _, err := EvaluateGuardrail(in); err == nil {
		t.Fatal("sequential mode ran with no inflation factor")
	} else if !strings.Contains(err.Error(), "n_tune") {
		t.Fatalf("error does not name what was missing: %v", err)
	}

	in.KFactor = 0.9
	in.Mode = ModeFixed
	if _, err := EvaluateGuardrail(in); err == nil {
		t.Fatal("a k below 1 was accepted, which would narrow the interval and manufacture a PASS")
	}

	in.Mode = "nonsense"
	in.KFactor = 1
	if _, err := EvaluateGuardrail(in); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// The inflation must reach the decision: on identical data the always-valid guardrail is strictly
// more conservative than the fixed-horizon one, and k comes from the same SeqInflation the primary
// metric uses so the two can never disagree on one screen.
func TestSequentialBoundIsWiderAndCanWithdrawAPass(t *testing.T) {
	fixed := grBase()
	fixed.ConvTest, fixed.ExpTest, fixed.ConvCtrl, fixed.ExpCtrl = 20000, 100000, 20000, 100000
	fr, err := EvaluateGuardrail(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if fr.Status != GuardrailPass {
		t.Fatalf("fixture should PASS at fixed horizon, got %s", fr.Status)
	}
	if fr.KFactor != 1 {
		t.Fatalf("fixed horizon applied an inflation of %v", fr.KFactor)
	}

	seq := fixed
	seq.Mode = ModeSequential
	seq.NTune = 5000
	sr, err := EvaluateGuardrail(seq)
	if err != nil {
		t.Fatal(err)
	}
	wantK := SeqInflation(200000, 0.05, 5000)
	if math.Abs(sr.KFactor-wantK) > 1e-12 {
		t.Fatalf("k = %v, want SeqInflation(200000, 0.05, 5000) = %v — the guardrail must use the same "+
			"inflation as the metric above it", sr.KFactor, wantK)
	}
	if !(sr.Bound.Lo < fr.Bound.Lo) {
		t.Fatalf("sequential lower bound %v is not below the fixed %v — k did not inflate anything",
			sr.Bound.Lo, fr.Bound.Lo)
	}
	if sr.Status == GuardrailPass {
		t.Fatalf("the wider always-valid bound still passed a 2%% margin (bound %v): the inflation is "+
			"not reaching the decision", sr.Bound.Lo)
	}
	if sr.Mode != ModeSequential || sr.NTune != 5000 {
		t.Fatalf("result does not echo the mode and n_tune it used: %+v", sr)
	}
}

// Binding decisions 2 and 9: the payload states everything that changes the answer. A guardrail
// result read without its unit, α, margin and mode is four different claims wearing one word.
func TestGuardrailStatesEverythingThatChangesTheAnswer(t *testing.T) {
	in := grBase()
	in.Unit = ""
	in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl = 950, 10000, 1000, 10000
	if _, err := EvaluateGuardrail(in); err == nil {
		t.Fatal("a guardrail ran without stating its randomisation unit")
	} else if !strings.Contains(err.Error(), UnitBucketID) {
		t.Fatalf("error does not name the units: %v", err)
	}

	in.Unit = UnitDistinctID
	in.Guardrail.MarginPct = nil // unstated: the 10% default, and the payload must say so
	got, err := EvaluateGuardrail(in)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.Unit != UnitDistinctID:
		t.Fatal("unit not echoed")
	case got.Alpha != 0.05:
		t.Fatalf("alpha not echoed: %v", got.Alpha)
	case got.MarginPct != DefaultGuardrailMarginPct || got.MarginSource != MarginDefault:
		t.Fatalf("an unstated margin must be echoed as the default: %v/%s", got.MarginPct, got.MarginSource)
	case got.Mode != ModeFixed || got.KFactor != 1:
		t.Fatalf("mode/k not echoed: %s/%v", got.Mode, got.KFactor)
	case got.Event != "checkout" || got.Variant != "treatment":
		t.Fatalf("event/variant not echoed: %s/%s", got.Event, got.Variant)
	case got.ThresholdPct != -DefaultGuardrailMarginPct:
		t.Fatalf("threshold = %v, want the negated margin for a not_worse guardrail", got.ThresholdPct)
	case got.ExposedTest != 10000 || got.ConvertedTest != 950 || got.ExposedCtrl != 10000 || got.ConvertedCtrl != 1000:
		t.Fatal("raw counts not echoed")
	case !got.OneSided:
		t.Fatal("the result does not declare itself one-sided")
	}

	declared := in
	declared.Guardrail.MarginPct = gmargin(2)
	d, err := EvaluateGuardrail(declared)
	if err != nil {
		t.Fatal(err)
	}
	if d.MarginSource != MarginDeclared || d.MarginPct != 2 {
		t.Fatalf("a declared margin was relabelled: %v/%s", d.MarginPct, d.MarginSource)
	}

	notBetter := declared
	notBetter.Guardrail.Direction = GuardrailNotBetter
	nb, err := EvaluateGuardrail(notBetter)
	if err != nil {
		t.Fatal(err)
	}
	if nb.ThresholdPct != 2 {
		t.Fatalf("threshold = %v, want +2 for a not_better guardrail at 2%%", nb.ThresholdPct)
	}

	bad := declared
	bad.Alpha = 0.7
	if _, err := EvaluateGuardrail(bad); err == nil {
		t.Fatal("an alpha of 0.7 was accepted")
	}
}

// The set helper must fail loudly on a bad guardrail rather than dropping it: a shorter list of
// results looks exactly like a set where everything passed.
func TestGuardrailSetFailsLoudlyRatherThanDroppingOne(t *testing.T) {
	good := grBase()
	good.ConvTest, good.ExpTest, good.ConvCtrl, good.ExpCtrl = 20000, 100000, 20000, 100000
	bad := good
	bad.Guardrail.MarginPct = gmargin(0)
	if _, err := EvaluateGuardrails([]GuardrailInput{good, bad}); err == nil {
		t.Fatal("an invalid guardrail was silently dropped from the set")
	}

	inconclusive := good
	inconclusive.ConvTest, inconclusive.ExpTest, inconclusive.ConvCtrl, inconclusive.ExpCtrl = 95, 1000, 100, 1000
	rs, err := EvaluateGuardrails([]GuardrailInput{good, inconclusive})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 {
		t.Fatalf("got %d results for 2 guardrails", len(rs))
	}
	blocking := GuardrailsBlocking(rs)
	if len(blocking) != 1 || blocking[0].Status != GuardrailInconclusive {
		t.Fatalf("INCONCLUSIVE must block a ship decision exactly like FAIL does: %+v", blocking)
	}
}

// The critical value is computed, not tabled, because α is configurable and a hardcoded 1.6449
// would survive a change to α and quietly report the wrong level.
func TestGuardrailCriticalZMatchesKnownQuantiles(t *testing.T) {
	cases := []struct{ alpha, want float64 }{
		{0.025, z95}, // must reproduce the package's own two-sided 95% constant
		{0.05, 1.6448536269514722},
		{0.01, 2.3263478740408408},
		{0.10, 1.2815515655446004},
		{0.001, 3.0902323061678132},
	}
	for _, c := range cases {
		if got := guardrailCriticalZ(c.alpha); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("guardrailCriticalZ(%v) = %.15f, want %.15f", c.alpha, got, c.want)
		}
	}
	// Monotone in α, in the direction that matters: a smaller α buys a wider bound.
	prev := math.Inf(1)
	for _, a := range []float64{0.001, 0.01, 0.025, 0.05, 0.1, 0.2} {
		z := guardrailCriticalZ(a)
		if z >= prev {
			t.Fatalf("critical value did not fall as alpha rose, at alpha=%v", a)
		}
		prev = z
	}
}
