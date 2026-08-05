package flag

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// ---- the critical value ------------------------------------------------------------------

// The whole file rests on an inverse normal CDF that the standard library does not provide. If
// seqNormInv is off, every interval is off by the same invisible amount, so it gets pinned against
// values anyone can look up before anything else is tested.
func TestSeqZCritMatchesKnownQuantiles(t *testing.T) {
	cases := []struct {
		alpha, want float64
	}{
		{0.05, 1.9599639845400534}, // must equal z95, the constant measure.go already uses
		{0.01, 2.5758293035489},
		{0.001, 3.2905267314919247},
		{0.10, 1.6448536269514715},
		{0.32, 0.994457883209753},
		{1e-6, 4.891638475714779},
	}
	for _, c := range cases {
		got := seqZCrit(c.alpha)
		if math.Abs(got-c.want) > 1e-11 {
			t.Errorf("seqZCrit(%g) = %.15f, want %.15f", c.alpha, got, c.want)
		}
	}
	if seqZCrit(0.05) != z95 {
		t.Errorf("seqZCrit(0.05) = %.17g, must be bit-identical to z95 = %.17g so the fixed and "+
			"sequential paths agree exactly at the default alpha", seqZCrit(0.05), z95)
	}
}

func TestSeqNormInvRoundTripsThroughTheCDF(t *testing.T) {
	for _, p := range []float64{1e-11, 1e-6, 0.001, 0.02, 0.1, 0.25, 0.5, 0.75, 0.9, 0.975, 0.999, 1 - 1e-9} {
		x := seqNormInv(p)
		back := 0.5 * math.Erfc(-x/math.Sqrt2)
		if math.Abs(back-p) > 1e-14*math.Max(p, 1e-12) {
			t.Errorf("seqNormInv(%g) = %g, CDF back = %g", p, x, back)
		}
	}
	for _, bad := range []float64{0, 1, -0.1, 1.5, math.NaN()} {
		if !math.IsNaN(seqNormInv(bad)) {
			t.Errorf("seqNormInv(%v) should be NaN, got %v", bad, seqNormInv(bad))
		}
	}
}

// ---- the three invariants the construction must satisfy ----------------------------------

// k(N) < 1 anywhere would mean the always-valid interval is NARROWER than the fixed-horizon one,
// which is not a thing that can be true: you cannot buy the right to peek forever and get a tighter
// answer as well. It would mean a bracket or a sign is wrong in the algebra, and the failure is
// silent — the numbers still render.
func TestSequentialIsAlwaysWiderThanFixed(t *testing.T) {
	for _, alpha := range []float64{0.001, 0.01, 0.05, 0.1, 0.2} {
		for _, nTune := range []int{100, 5000, 250000} {
			for n := 100; n <= 1000000; n = int(math.Ceil(float64(n) * 1.15)) {
				k := SeqInflation(n, alpha, nTune)
				if !(k > 1) || math.IsNaN(k) {
					t.Fatalf("k(N=%d, alpha=%g, n_tune=%d) = %g, must be > 1", n, alpha, nTune, k)
				}
			}
		}
	}
}

// A confidence sequence that did not tighten with data would be useless: users would watch it sit
// still for a week and conclude the tool does not work. This pins that more evidence is always a
// narrower answer, across four orders of magnitude of N.
func TestSequentialWidthShrinksWithN(t *testing.T) {
	const rate = 0.2
	halfAt := func(n int) float64 {
		se := math.Sqrt(2 * rate * (1 - rate) / (float64(n) / 2))
		return SeqHalfWidth(se, n, 0.05, 5000)
	}
	prev := math.Inf(1)
	for n := 100; n <= 2000000; n = int(math.Ceil(float64(n) * 1.1)) {
		half := halfAt(n)
		if !(half < prev) {
			t.Fatalf("half-width at N=%d is %g, not smaller than %g at the previous N", n, half, prev)
		}
		prev = half
	}
	// It shrinks, and it shrinks by enough to matter: two orders of magnitude more users must buy
	// a visibly narrower answer, not an asymptote the user stares at for a month.
	if !(halfAt(2000000) < halfAt(20000)/5) {
		t.Errorf("100x the users only took the half-width from %g to %g", halfAt(20000), halfAt(2000000))
	}
}

// rho is chosen to minimise the width at N = n_tune. This is the reason n_tune must be the power
// calculator's n_planned rather than a round number: at a tenth or ten times the tuning point the
// interval is meaningfully wider, and a user who never declared a sample size pays for it without
// being told. (SeqResolveNTune + the n_tune_source field are what tell them.)
func TestSequentialWidthMinimizedNearNTune(t *testing.T) {
	const alpha, nTune = 0.05, 5000
	kAt := func(n int) float64 { return SeqInflation(n, alpha, nTune) }
	if !(kAt(nTune) < kAt(nTune/10)) {
		t.Errorf("k(N*) = %g not below k(N*/10) = %g", kAt(nTune), kAt(nTune/10))
	}
	if !(kAt(nTune) < kAt(10*nTune)) {
		t.Errorf("k(N*) = %g not below k(10·N*) = %g", kAt(nTune), kAt(10*nTune))
	}
	// and the true argmin sits within a factor of two of N*, not merely somewhere to the left
	best, bestK := 0, math.Inf(1)
	for n := 100; n <= 500000; n = int(math.Ceil(float64(n) * 1.02)) {
		if k := kAt(n); k < bestK {
			best, bestK = n, k
		}
	}
	if float64(best) < float64(nTune)/2 || float64(best) > float64(nTune)*2 {
		t.Errorf("k is minimised at N=%d, which is not near n_tune=%d — rho is mistuned", best, nTune)
	}
}

// The spec states the width two ways: k(N)·z·se, and se·widthFactor. The code computes the second
// because the first evaluates 0·infinity as alpha approaches 1, which is a path the p-value
// bisection walks every single call. They must still agree everywhere else, or "k" in the payload
// is not the number that actually widened the interval and the printed price is a fiction.
func TestSeqHalfWidthEqualsKTimesFixedHalfWidth(t *testing.T) {
	for _, alpha := range []float64{0.01, 0.05, 0.2} {
		for _, n := range []int{200, 5000, 137000} {
			se := 0.0123
			viaK := SeqInflation(n, alpha, 5000) * seqZCrit(alpha) * se
			direct := SeqHalfWidth(se, n, alpha, 5000)
			if math.Abs(viaK-direct) > 1e-12*direct {
				t.Errorf("alpha=%g N=%d: k·z·se = %.17g but se·widthFactor = %.17g", alpha, n, viaK, direct)
			}
		}
	}
}

// SeqAlwaysValidP bisects on alpha. Bisection on a non-monotone function returns a deterministic
// wrong answer, which is worse than a nondeterministic one because nothing ever catches it. The
// monotonicity is assumed by the algorithm, so it is proven here rather than believed.
func TestSeqHalfWidthIsMonotoneInAlpha(t *testing.T) {
	for _, n := range []int{100, 1000, 50000} {
		for _, nTune := range []int{500, 5000, 100000} {
			prev := math.Inf(1)
			for x := math.Log(1e-12); x < -1e-9; x += 0.01 {
				h := SeqHalfWidth(0.01, n, math.Exp(x), nTune)
				if !(h < prev) {
					t.Fatalf("N=%d n_tune=%d: half-width at alpha=%g is %g, not below %g at the "+
						"smaller alpha — bisection is invalid", n, nTune, math.Exp(x), h, prev)
				}
				prev = h
			}
		}
	}
}

// ---- the always-valid p-value -------------------------------------------------------------

func TestAlwaysValidPValueRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	checked := 0
	for i := 0; i < 500; i++ {
		nT := 100 + rng.Intn(20000)
		nC := 100 + rng.Intn(20000)
		pC := 0.05 + rng.Float64()*0.55
		se := math.Sqrt(2 * pC * (1 - pC) / (float64(nT+nC) / 2))
		// Effects sized in standard errors, spanning the whole decision boundary. Drawing two
		// independent rates instead puts almost every experiment miles past the floor, and the
		// bisection — the thing under test — never actually runs.
		diff := (rng.Float64()*10 - 5) * se
		pT := pC + diff
		if pT <= 0 || pT >= 1 {
			continue
		}
		n := nT + nC
		p := SeqAlwaysValidP(diff, se, n, 5000)
		if p <= seqPFloor || p >= 1 {
			continue // clamped by design; the round trip is only defined on the open interval
		}
		checked++
		half := SeqHalfWidth(se, n, p, 5000)
		if math.Abs(half-math.Abs(diff)) > 1e-9 {
			t.Fatalf("p=%g reproduces half-width %.12g, want |diff| = %.12g", p, half, math.Abs(diff))
		}
	}
	if checked < 200 {
		t.Fatalf("only %d of 500 draws exercised the bisection; the fixture is not testing it", checked)
	}
}

// A p-value is a number people act on, so both ends have to be honest rather than merely finite.
func TestAlwaysValidPValueClampsAtBothEnds(t *testing.T) {
	se := math.Sqrt(2 * 0.2 * 0.8 / 5000)
	// An effect smaller than the widest interval the construction can draw is no evidence at all,
	// and must read as exactly 1 — not 0.99999999996, which invites "well, it's under 1".
	if p := SeqAlwaysValidP(1e-9, se, 10000, 5000); p != 1 {
		t.Errorf("a difference of 1e-9pp gave p = %.17g, want exactly 1", p)
	}
	// An enormous effect stops at the floor rather than printing a number produced by float64
	// resolution rather than by data.
	if p := SeqAlwaysValidP(0.5, se, 10000, 5000); p != seqPFloor {
		t.Errorf("a 50pp difference gave p = %g, want the floor %g", p, seqPFloor)
	}
	// Degenerate inputs are "we cannot say", never "significant".
	for _, c := range []struct {
		diff, se float64
		n, nTune int
	}{
		{0.1, 0.01, 0, 5000}, {0.1, 0, 100, 5000}, {0, 0.01, 100, 5000}, {0.1, 0.01, 100, 0},
		{math.NaN(), 0.01, 100, 5000},
	} {
		if p := SeqAlwaysValidP(c.diff, c.se, c.n, c.nTune); p != 1 {
			t.Errorf("degenerate input %+v gave p = %g, want 1", c, p)
		}
	}
	// Symmetric: a loss of the same size is exactly as much evidence as a win.
	if a, b := SeqAlwaysValidP(0.02, se, 10000, 5000), SeqAlwaysValidP(-0.02, se, 10000, 5000); a != b {
		t.Errorf("p is not symmetric in the sign of the difference: %g vs %g", a, b)
	}
}

// ---- configuration: n_tune, mode, validation ----------------------------------------------

func TestSeqResolveNTunePrefersThePlannedSample(t *testing.T) {
	n, src := SeqResolveNTune(12400)
	if n != 12400 || src != SeqNTuneFromPlan {
		t.Errorf("declared design gave n_tune=%d (%s), want 12400 from %s", n, src, SeqNTuneFromPlan)
	}
	for _, none := range []int{0, -1} {
		n, src = SeqResolveNTune(none)
		if n != SeqDefaultNTune || src != SeqNTuneFromDefault {
			t.Errorf("no declared design gave n_tune=%d (%s), want %d from %s",
				n, src, SeqDefaultNTune, SeqNTuneFromDefault)
		}
	}
	// The source string is not decoration: tuning at n_planned is measurably narrower than tuning
	// at the 5,000 placeholder, so the payload has to say which one bought this width.
	planned := SeqInflation(12400, 0.05, 12400)
	placeholder := SeqInflation(12400, 0.05, SeqDefaultNTune)
	if !(planned < placeholder) {
		t.Errorf("k tuned at n_planned (%g) should be below k tuned at the 5,000 default (%g)",
			planned, placeholder)
	}
}

func TestSeqResolveModeRefusesFixedBeforeItIsValid(t *testing.T) {
	// Declared sequential: always sequential, nothing to explain.
	if mode, reason := SeqResolveMode(SeqModeSequential, 100, 12400); mode != SeqModeSequential || reason != "" {
		t.Errorf("sequential resolved to %q with reason %q", mode, reason)
	}
	// Declared fixed, read early: the fixed interval is not valid yet, so it is not drawn, and the
	// substitution is named with BOTH numbers — a swap the reader cannot see is a lie by omission.
	mode, reason := SeqResolveMode(SeqModeFixed, 3100, 12400)
	if mode != SeqModeSequential {
		t.Fatalf("fixed at 3,100 of 12,400 resolved to %q, want the always-valid interval", mode)
	}
	for _, want := range []string{"12400", "3100", "wider on purpose"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not contain %q", reason, want)
		}
	}
	// At the horizon it becomes valid; half-open, so reaching n_planned counts.
	if mode, reason := SeqResolveMode(SeqModeFixed, 12400, 12400); mode != SeqModeFixed || reason != "" {
		t.Errorf("fixed at its planned N resolved to %q (%q), want fixed with no caveat", mode, reason)
	}
	// Fixed with no pre-registered horizon is peeking with extra steps: there is no N at which it
	// becomes valid, so it never gets drawn.
	mode, reason = SeqResolveMode(SeqModeFixed, 999999, 0)
	if mode != SeqModeSequential || !strings.Contains(reason, "never pre-registered a sample") {
		t.Errorf("fixed with no n_planned resolved to %q (%q)", mode, reason)
	}
}

func TestSeqParamsValidate(t *testing.T) {
	if err := SeqDefaults().Validate(); err != nil {
		t.Fatalf("the default configuration must be valid: %v", err)
	}
	bad := []SeqParams{
		{Mode: "bayesian", Alpha: 0.05, NTune: 5000},
		{Mode: SeqModeSequential, Alpha: 0, NTune: 5000},
		{Mode: SeqModeSequential, Alpha: 1, NTune: 5000},
		{Mode: SeqModeSequential, Alpha: -0.1, NTune: 5000},
		{Mode: SeqModeSequential, Alpha: 0.05, NTune: 0},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
	// An unconfigured experiment falls back to sequential, never to fixed: the safe read for
	// something nobody configured is the one that survives being looked at.
	if got := SeqParamsFor("", 0, 0); got.Mode != SeqModeSequential ||
		got.Alpha != SeqDefaultAlpha || got.NTune != SeqDefaultNTune {
		t.Errorf("SeqParamsFor(zero values) = %+v", got)
	}
	if got := SeqParamsFor(SeqModeFixed, 0.01, 12400); got.Mode != SeqModeFixed ||
		got.Alpha != 0.01 || got.NTune != 12400 || got.NTuneSource != SeqNTuneFromPlan {
		t.Errorf("SeqParamsFor(fixed, 0.01, 12400) = %+v", got)
	}
}

// ---- the payload -----------------------------------------------------------------------

// Decision 9: everything that shaped the number is stated. An interval whose alpha, mode, tuning
// point and inflation are invisible is an interval a reader has to take on faith, which is the
// posture this product exists to avoid.
func TestSeqComputeStatesEveryInput(t *testing.T) {
	par := SeqParamsFor(SeqModeSequential, 0.05, 12400)
	se := math.Sqrt(2 * 0.2 * 0.8 / 3000)
	s := SeqCompute(0.03, se, 6000, 12400, par)
	switch {
	case !s.Usable:
		t.Fatal("a 6,000-user experiment with a real SE is usable")
	case s.Mode != SeqModeSequential || s.ModeDeclared != SeqModeSequential:
		t.Errorf("mode = %q (declared %q)", s.Mode, s.ModeDeclared)
	case s.Alpha != 0.05 || s.NTune != 12400 || s.NTuneSource != SeqNTuneFromPlan:
		t.Errorf("params not echoed: alpha=%g n_tune=%d source=%q", s.Alpha, s.NTune, s.NTuneSource)
	case s.N != 6000 || s.NPlanned != 12400:
		t.Errorf("N=%d n_planned=%d", s.N, s.NPlanned)
	case !(s.K > 1):
		t.Errorf("k = %g, must exceed 1", s.K)
	case s.SE != se || s.Diff != 0.03:
		t.Errorf("SE/diff not echoed: %g / %g", s.SE, s.Diff)
	}
	if math.Abs(s.HalfWidth-s.K*s.Z*s.SE) > 1e-15 {
		t.Errorf("half-width %g is not k·z·se = %g", s.HalfWidth, s.K*s.Z*s.SE)
	}
	if s.Lo != s.Diff-s.HalfWidth || s.Hi != s.Diff+s.HalfWidth {
		t.Errorf("interval [%g,%g] does not bracket the estimate by the half-width", s.Lo, s.Hi)
	}
	if math.Abs(s.LiftZ-s.Z*s.K) > 1e-15 {
		t.Errorf("lift_z = %g, want z·k = %g — the relative interval must inflate by the same "+
			"factor as the absolute one", s.LiftZ, s.Z*s.K)
	}
	// The verdict is read off the interval that was actually rendered, so the sentence and the
	// picture can never disagree.
	if s.Excludes0 != (s.Lo > 0 || s.Hi < 0) {
		t.Error("excludes_zero disagrees with the rendered interval")
	}
	// And the always-valid p agrees with the verdict at this alpha: the interval excludes zero
	// exactly when p < alpha.
	if s.Excludes0 != (s.AlwaysValidP < s.Alpha) {
		t.Errorf("excludes_zero=%v but always_valid_p=%g against alpha=%g",
			s.Excludes0, s.AlwaysValidP, s.Alpha)
	}
	// Percentage-point rendering, matching every other Interval on the report.
	if ci := s.DiffInterval(); math.Abs(ci.Point-3) > 0.05 {
		t.Errorf("DiffInterval point = %g, want ~3 percentage points", ci.Point)
	}
}

// Fixed mode past its horizon is a plain z-interval: k is exactly 1, not "about 1".
func TestSeqComputeFixedModeDoesNotInflate(t *testing.T) {
	par := SeqParamsFor(SeqModeFixed, 0.05, 12400)
	se := 0.01
	s := SeqCompute(0.02, se, 12400, 12400, par)
	if s.Mode != SeqModeFixed || s.K != 1 {
		t.Fatalf("mode=%q k=%g, want fixed with k exactly 1", s.Mode, s.K)
	}
	if math.Abs(s.HalfWidth-z95*se) > 1e-15 {
		t.Errorf("half-width %g, want z95·se = %g", s.HalfWidth, z95*se)
	}
	// Read one user early and the fixed interval is not valid yet, so the wider one is drawn.
	early := SeqCompute(0.02, se, 12399, 12400, par)
	if early.Mode != SeqModeSequential || !(early.HalfWidth > s.HalfWidth) {
		t.Errorf("at 12,399 of 12,400: mode=%q half=%g, want the wider always-valid interval",
			early.Mode, early.HalfWidth)
	}
	if early.ModeDeclared != SeqModeFixed {
		t.Errorf("the declared mode must survive the substitution, got %q", early.ModeDeclared)
	}
}

// Decision 5, the CUPED composition order, pinned as behaviour rather than as a comment: adjust
// the estimate and the SE first, THEN inflate the adjusted half-width by k(N). k depends only on N
// and the pre-registered parameters, so a variance reduction must pass through it untouched. If k
// ever moved with the SE it would mean the inflation was being applied to the wrong quantity and
// the interval would quietly stop covering.
func TestSeqComputeInflatesTheAdjustedSENotTheRaw(t *testing.T) {
	par := SeqParamsFor(SeqModeSequential, 0.05, 8000)
	const rawSE = 0.02
	adjSE := rawSE * math.Sqrt(1-0.6*0.6) // a CUPED covariate at rho = 0.6
	raw := SeqCompute(0.03, rawSE, 8000, 8000, par)
	adj := SeqCompute(0.03, adjSE, 8000, 8000, par)
	if raw.K != adj.K {
		t.Errorf("k moved with the SE: %g vs %g — the inflation must depend on N alone", raw.K, adj.K)
	}
	ratio := adj.HalfWidth / raw.HalfWidth
	if math.Abs(ratio-adjSE/rawSE) > 1e-12 {
		t.Errorf("half-width shrank by %g, want exactly the SE ratio %g", ratio, adjSE/rawSE)
	}
	if !(adj.AlwaysValidP < raw.AlwaysValidP) {
		t.Errorf("a reduced SE must be stronger evidence: p went %g -> %g", raw.AlwaysValidP, adj.AlwaysValidP)
	}
}

// The zero interval is not a claim of "no difference", and this is the flag that stops it being
// read as one on an experiment with no data yet.
func TestSeqComputeUnusableWithoutASample(t *testing.T) {
	par := SeqDefaults()
	for _, c := range []struct {
		name     string
		diff, se float64
		n        int
	}{
		{"no exposures", 0.1, 0.01, 0},
		{"no variance", 0.1, 0, 500},
		{"NaN estimate", math.NaN(), 0.01, 500},
		{"infinite SE", 0.1, math.Inf(1), 500},
	} {
		s := SeqCompute(c.diff, c.se, c.n, 0, par)
		if s.Usable || s.Excludes0 || s.HalfWidth != 0 || s.AlwaysValidP != 1 {
			t.Errorf("%s: got usable=%v excludes0=%v half=%g p=%g, want an explicitly unusable read",
				c.name, s.Usable, s.Excludes0, s.HalfWidth, s.AlwaysValidP)
		}
	}
}

// No clock, no RNG, no map iteration: the same inputs must produce identical bits every run, which
// is the contract every other report in this codebase signs and the reason MCP and the API can be
// pinned against each other at all.
func TestSeqComputeIsDeterministic(t *testing.T) {
	par := SeqParamsFor(SeqModeSequential, 0.037, 9100)
	a := SeqCompute(0.0123, 0.00456, 7777, 9100, par)
	for i := 0; i < 25; i++ {
		if b := SeqCompute(0.0123, 0.00456, 7777, 9100, par); b != a {
			t.Fatalf("run %d differs:\n %+v\n %+v", i, a, b)
		}
	}
}

// ---- the reason sequential is the default ------------------------------------------------

// This is the justification for shipping always-valid inference as the DEFAULT, stated as a
// measurement instead of as an appeal to authority.
//
// 2,000 A/A experiments — both arms drawn from the SAME 20% conversion rate, so every rejection is
// by construction a false positive. Each is peeked at every 100 users from 200 to 20,000 (199
// looks) and stopped at the first look that calls a winner, which is exactly what a person with a
// dashboard does. The fixed-horizon z-test is nominally a 5% test; under this schedule it fires on
// roughly a quarter of experiments that have no effect at all. The confidence sequence, given no
// more information, stays under its 5% budget.
//
// The 0.15 floor asserted for the fixed test is a property of THIS peek schedule (199 looks to
// 20,000 users), named here so nobody later "fixes" a failure by moving the threshold: change the
// schedule and you must change the sentence, not the number. The sequential bound is the real
// assertion and it is the one tied to alpha.
func TestSequentialControlsErrorUnderPeeking(t *testing.T) {
	const (
		experiments = 2000
		rate        = 0.20 // identical in both arms: the null is TRUE by construction
		maxUsers    = 20000
		peekEvery   = 100
		firstPeek   = 200
		alpha       = 0.05
	)
	// n_tune is the horizon the operator would have declared. Tuning elsewhere changes the width,
	// not the guarantee — the coverage claim holds at every N regardless.
	const nTune = maxUsers

	master := rand.New(rand.NewSource(20260804))
	seeds := make([]int64, experiments)
	for i := range seeds {
		seeds[i] = master.Int63()
	}

	var seqFalse, fixedFalse int
	for _, seed := range seeds {
		rng := rand.New(rand.NewSource(seed))
		var nT, cT, nC, cC int
		seqRejected, fixedRejected := false, false
		for u := 1; u <= maxUsers; u++ {
			converted := rng.Float64() < rate
			if rng.Intn(2) == 0 { // randomised allocation, so arm sizes wobble like a real one
				nT++
				if converted {
					cT++
				}
			} else {
				nC++
				if converted {
					cC++
				}
			}
			if u < firstPeek || u%peekEvery != 0 {
				continue
			}
			if !fixedRejected && significant(cT, nT, cC, nC) {
				fixedRejected = true // stopped at the first significant look, as a human would
			}
			if !seqRejected && nT > 0 && nC > 0 {
				pT := float64(cT) / float64(nT)
				pC := float64(cC) / float64(nC)
				se := math.Sqrt(pT*(1-pT)/float64(nT) + pC*(1-pC)/float64(nC))
				if math.Abs(pT-pC) > SeqHalfWidth(se, u, alpha, nTune) {
					seqRejected = true
				}
			}
			if seqRejected && fixedRejected {
				break // both have already fired; the rest of this experiment cannot change either
			}
		}
		if seqRejected {
			seqFalse++
		}
		if fixedRejected {
			fixedFalse++
		}
	}

	seqFPR := float64(seqFalse) / experiments
	fixedFPR := float64(fixedFalse) / experiments
	t.Logf("A/A over %d experiments, %d looks each: sequential FPR = %.3f, fixed FPR = %.3f (nominal %.2f)",
		experiments, 1+(maxUsers-firstPeek)/peekEvery, seqFPR, fixedFPR, alpha)

	if seqFPR > 0.06 {
		t.Errorf("sequential false-positive rate %.3f exceeds its 5%% budget (+ Monte Carlo slack) — "+
			"the always-valid claim is not holding", seqFPR)
	}
	if fixedFPR <= 0.15 {
		t.Errorf("fixed-horizon false-positive rate under peeking is only %.3f; this test exists to "+
			"demonstrate the inflation that makes sequential the default, and it is not "+
			"demonstrating it", fixedFPR)
	}
	if !(fixedFPR > seqFPR*2) {
		t.Errorf("fixed (%.3f) is not dramatically worse than sequential (%.3f) under peeking",
			fixedFPR, seqFPR)
	}
}
