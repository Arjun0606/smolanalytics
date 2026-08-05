package flag

import (
	"errors"
	"math"
)

// Always-valid inference: confidence sequences for experiments that get looked at.
//
// `significant()` in measure.go is a fixed-horizon two-proportion z-test. It is correct exactly
// once — at a sample size chosen before any data arrived — and it sits behind a dashboard built
// for checking every morning. Every extra look is another chance to cross 1.96 by luck, and the
// crossings accumulate: at the peek schedule pinned in sequential_test.go the fixed test rejects
// a TRUE NULL about a quarter of the time while claiming 5%. That is not a rounding error, it is
// the difference between "one in twenty of your wins are fake" and "one in four".
//
// A confidence sequence fixes the failure at the source: it is a sequence of intervals that
// simultaneously cover the truth with probability 1-alpha, so a reader may look at every one of
// them, stop whenever they like, and the guarantee still holds. Nothing about the stopping rule
// enters the maths, which is why it is the only honest thing to render on a page with a refresh
// button.
//
// The construction is the asymptotic confidence sequence of Waudby-Smith & Ramdas (2023), the one
// GrowthBook's gbstats ships, chosen over mSPRT because it is nonparametric, closed-form, assumes
// no likelihood, and can therefore be cross-checked line by line against a published open-source
// implementation by anyone who doubts us.
//
// Two rules this file holds to, both of them load-bearing:
//
//   - NOTHING here reads the clock or an RNG. Same counts in, same bits out, forever — the same
//     contract every other report in this codebase signs. The A/A simulation in the test file is
//     a proof about the method, never an input to a reported number.
//   - The width is a function of the total exposed N and the pre-registered parameters ONLY. It
//     cannot depend on how many times someone looked, because a rule that punishes looking is a
//     rule users route around by not looking.

// Mode names. Sequential is the DEFAULT, and that default is the whole point: a fixed-horizon
// interval on a live dashboard is a wrong number wearing the clothes of a right one.
const (
	SeqModeSequential = "sequential"
	SeqModeFixed      = "fixed"
)

// Defaults. NTune is only reached when the experiment declares no planned sample size — see
// SeqResolveNTune for why n_planned is always the better answer when it exists.
const (
	SeqDefaultAlpha = 0.05
	SeqDefaultNTune = 5000
)

// Where n_tune came from. It ships in the payload because k(N) is minimised at N = n_tune, so a
// reader who does not know which N we tuned for cannot tell a wide interval from a mistuned one.
const (
	SeqNTuneFromPlan    = "n_planned"    // the power calculator's answer: baseline + MDE + power were declared
	SeqNTuneFromDefault = "default_5000" // nothing was declared, so this is a guess and says so
)

// seqPFloor is the smallest always-valid p we will print. Below this the bisection is reporting
// the resolution of float64 arithmetic rather than evidence, and "p = 3e-47" invites a confidence
// no experiment with a few thousand users has earned.
const seqPFloor = 1e-12

// seqBisectIters is fixed, not a tolerance loop. A convergence criterion makes the output depend
// on how the compiler happened to round an intermediate; a fixed count makes the answer a pure
// function of the inputs, which is what "recomputed from raw events, byte-identical" means.
const seqBisectIters = 60

// SeqParams is the pre-registered analysis configuration. Every field is echoed in SeqStats
// rather than assumed, because an interval whose alpha, mode and tuning point are invisible is
// an interval a reader has to take on faith.
type SeqParams struct {
	Mode        string  `json:"mode"`
	Alpha       float64 `json:"alpha"`
	NTune       int     `json:"n_tune"`
	NTuneSource string  `json:"n_tune_source"`
}

// SeqDefaults is the configuration an experiment gets when it declares nothing: always-valid
// inference at 95%, tuned for 5,000 exposed units, and honest in the payload about the fact that
// 5,000 was nobody's decision.
func SeqDefaults() SeqParams {
	return SeqParams{
		Mode:        SeqModeSequential,
		Alpha:       SeqDefaultAlpha,
		NTune:       SeqDefaultNTune,
		NTuneSource: SeqNTuneFromDefault,
	}
}

// SeqResolveNTune picks the tuning point N*, and names its source.
//
// k(N) is minimised at N = N* and grows on both sides, so a wrong N* silently widens every
// interval the experiment will ever show. When the experiment declared a baseline, an MDE and a
// power target, the power calculator already computed the sample size at which the operator
// intends to decide — that number IS N*, and inventing a different one instead means shipping
// intervals wider than the design justifies while calling it rigour.
//
// nPlanned <= 0 means the experiment declared no design, so 5,000 is a placeholder. The source
// string travels with it so the report can say "tuned for 5,000 because you never said" rather
// than presenting a guess as a decision.
func SeqResolveNTune(nPlanned int) (int, string) {
	if nPlanned > 0 {
		return nPlanned, SeqNTuneFromPlan
	}
	return SeqDefaultNTune, SeqNTuneFromDefault
}

// SeqParamsFor builds the parameters from what the experiment actually declared. alpha <= 0 falls
// back to 0.05; an empty mode falls back to sequential, never to fixed — a missing mode is an
// experiment nobody configured, and the safe read for an unconfigured experiment is the one that
// survives peeking.
func SeqParamsFor(mode string, alpha float64, nPlanned int) SeqParams {
	p := SeqDefaults()
	if mode == SeqModeFixed {
		p.Mode = SeqModeFixed
	}
	if alpha > 0 && alpha < 1 {
		p.Alpha = alpha
	}
	p.NTune, p.NTuneSource = SeqResolveNTune(nPlanned)
	return p
}

// Validate rejects configurations whose intervals would be meaningless rather than merely wide.
func (p SeqParams) Validate() error {
	if p.Mode != SeqModeSequential && p.Mode != SeqModeFixed {
		return errors.New("mode must be " + SeqModeSequential + " or " + SeqModeFixed)
	}
	if !(p.Alpha > 0 && p.Alpha < 1) {
		return errors.New("alpha must be strictly between 0 and 1")
	}
	// alpha = 1 makes rho = 0 and the width factor infinite; alpha = 0 makes it infinite the other
	// way. Both produce an interval that is either everything or nothing, printed with a straight
	// face.
	if p.NTune <= 0 {
		return errors.New("n_tune must be positive — it is the sample size the interval is tuned for")
	}
	return nil
}

// SeqStats is the always-valid read, with every input that shaped it stated alongside.
//
// Absolute quantities (Diff, SE, HalfWidth, Lo, Hi) are on the PROPORTION scale — 0.031 means
// 3.1 percentage points — because that is the scale the arithmetic happens on. DiffInterval()
// converts to the percentage convention the rest of the report renders in; mixing the two
// silently is how a reader misreads by 100x.
type SeqStats struct {
	// ModeDeclared is what the experiment pre-registered. Mode is what was actually used to draw
	// this interval. They differ exactly when a fixed-horizon test is being read too early, and
	// ModeReason says so in words rather than leaving the reader to notice.
	ModeDeclared string  `json:"mode_declared"`
	Mode         string  `json:"mode"`
	ModeReason   string  `json:"mode_reason,omitempty"`
	Alpha        float64 `json:"alpha"`
	NTune        int     `json:"n_tune"`
	NTuneSource  string  `json:"n_tune_source"`
	NPlanned     int     `json:"n_planned,omitempty"`

	N int `json:"n"` // total exposed units across both arms — the N that sets the width

	// K is the inflation over the fixed-horizon half-width. Always > 1 in sequential mode, exactly
	// 1 in fixed mode. Printed because "your interval is 1.55x wider than a fixed test's" is the
	// honest price of being allowed to look whenever you want, and hiding the price reads as the
	// method being free.
	K float64 `json:"k"`
	// Z is the critical value the fixed half-width uses, and Z*K is what to hand liftInterval so
	// the relative-lift interval inflates by exactly the same factor as the absolute one. Two
	// intervals on one row disagreeing about how conservative they are is worse than either.
	Z     float64 `json:"z"`
	LiftZ float64 `json:"lift_z"`

	SE        float64 `json:"se"`         // the standard error this interval was built from
	Diff      float64 `json:"diff"`       // p_test - p_control
	HalfWidth float64 `json:"half_width"` // K * Z * SE
	Lo        float64 `json:"lo"`
	Hi        float64 `json:"hi"`

	// AlwaysValidP is the alpha at which this difference would just barely be called — the
	// anytime-valid p-value, NEVER the fixed-horizon one. Named in full because a field called
	// `p_value` next to a sequential interval would be read as the number everybody already knows,
	// and the two differ by a lot.
	AlwaysValidP float64 `json:"always_valid_p"`
	// Excludes0 is the decision: does the interval exclude no-difference. It is derived from the
	// interval actually rendered, so the sentence and the picture can never disagree.
	Excludes0 bool `json:"excludes_zero"`
	// Usable is false when there is nothing to compute from (no exposures, or an arm with no
	// variance yet). The zero interval is not a claim of "no effect" and this flag is what stops
	// it being read as one.
	Usable bool `json:"usable"`
}

// DiffInterval renders the absolute difference in percentage points, matching the units every
// other Interval on the report carries.
func (s SeqStats) DiffInterval() Interval {
	return Interval{
		Point: round1(100 * s.Diff),
		Lo:    round1(100 * s.Lo),
		Hi:    round1(100 * s.Hi),
	}
}

// SeqResolveMode decides which interval is actually valid to draw right now, and says why.
//
// A pre-registered fixed-horizon test is a promise to look ONCE, at n_planned. Rendering its
// interval before then is rendering an interval whose stated coverage is false, and the reader has
// no way to know: it looks exactly like the valid one, just earlier. So we refuse. We draw the
// always-valid interval instead — wider, correct at every N — and we name the substitution.
//
// The nPlanned <= 0 case is the same failure with the horizon missing entirely: a fixed-horizon
// test with no declared horizon is peeking with extra steps, so it gets the sequential interval
// too.
func SeqResolveMode(declared string, n, nPlanned int) (mode, reason string) {
	if declared != SeqModeFixed {
		return SeqModeSequential, ""
	}
	if nPlanned <= 0 {
		return SeqModeSequential, "you chose a fixed-horizon test but never pre-registered a sample " +
			"size, so there is no point at which its interval becomes valid. This is the always-valid " +
			"interval instead — it is wider on purpose, and it is correct at every N."
	}
	if n < nPlanned {
		return SeqModeSequential, "you pre-registered a fixed-horizon test at " + itoa(nPlanned) +
			" users and you're at " + itoa(n) + ". The fixed interval isn't valid yet, so this is the " +
			"always-valid one — it's wider on purpose."
	}
	return SeqModeFixed, ""
}

// SeqCompute is the whole always-valid read for an absolute difference in rates.
//
// It takes a difference and a standard error rather than four counts, and that signature is the
// composition rule with CUPED (spec item 12), not an accident. CUPED adjusts the estimate and
// shrinks the SE; the confidence sequence then inflates whatever half-width it is handed. Order:
// adjust FIRST, inflate SECOND. Handing this function the raw SE and applying the variance
// reduction afterwards inflates a quantity that no longer matches the estimate, and the interval
// silently stops covering. Callers echo the raw and adjusted SE side by side so the reduction is
// visible rather than asserted.
//
// n is the TOTAL exposed units across both arms — the CS is a statement about the experiment, not
// about one arm. nPlanned is the pre-registered sample size (0 when none was declared) and is used
// only for the mode decision.
func SeqCompute(diff, se float64, n, nPlanned int, par SeqParams) SeqStats {
	mode, reason := SeqResolveMode(par.Mode, n, nPlanned)
	z := seqZCrit(par.Alpha)
	s := SeqStats{
		ModeDeclared: par.Mode,
		Mode:         mode,
		ModeReason:   reason,
		Alpha:        par.Alpha,
		NTune:        par.NTune,
		NTuneSource:  par.NTuneSource,
		NPlanned:     nPlanned,
		N:            n,
		K:            1,
		Z:            z,
		LiftZ:        z,
		SE:           se,
		Diff:         diff,
		AlwaysValidP: 1,
	}
	// No sample, no variance, or a nonsense configuration: there is nothing to say and the zero
	// interval must not be dressed up as "no difference". Usable stays false and every consumer
	// has to look at it.
	if n <= 0 || par.NTune <= 0 || !(par.Alpha > 0 && par.Alpha < 1) ||
		se <= 0 || math.IsNaN(se) || math.IsInf(se, 0) || math.IsNaN(diff) || math.IsInf(diff, 0) {
		return s
	}
	s.Usable = true
	if mode == SeqModeSequential {
		s.K = SeqInflation(n, par.Alpha, par.NTune)
		s.AlwaysValidP = SeqAlwaysValidP(diff, se, n, par.NTune)
	} else {
		// Fixed mode at or past its pre-registered horizon: k = 1 by definition, and the
		// always-valid p is still computed and still true — it is simply the more conservative of
		// the two. Reporting it here costs nothing and means the two modes can be compared.
		s.AlwaysValidP = SeqAlwaysValidP(diff, se, n, par.NTune)
	}
	s.LiftZ = z * s.K
	// The explicit conversion is a rounding barrier, and it is load-bearing rather than noise: Go
	// permits a multiply to be fused into a following add or subtract, and on arm64 it is. Without
	// the barrier the compiler computes `diff - k*z*se` with a single rounding while the HalfWidth
	// field carries the twice-rounded value, so the payload ships a Lo that is not exactly
	// Diff - HalfWidth. A reader checking our own arithmetic against our own numbers finds it off
	// in the last digit, on the one report whose entire pitch is that you can check it.
	s.HalfWidth = float64(s.K * z * se)
	s.Lo, s.Hi = diff-s.HalfWidth, diff+s.HalfWidth
	s.Excludes0 = s.Lo > 0 || s.Hi < 0
	return s
}

// SeqInflation is k(N): how much wider the always-valid half-width is than the fixed-horizon one.
//
//	k(N) = sqrt( N · ( 2·(N·rho^2 + 1) / (N^2·rho^2) ) · ln( sqrt(N·rho^2 + 1) / alpha ) ) / z_{1-alpha/2}
//
// It is > 1 for every N — pinned as a test, because a k below 1 would mean the always-valid
// interval is NARROWER than the fixed one, which is impossible and would mean a sign or a bracket
// is wrong somewhere in the algebra.
func SeqInflation(n int, alpha float64, nTune int) float64 {
	z := seqZCrit(alpha)
	if z <= 0 {
		return math.Inf(1)
	}
	return seqWidthFactor(n, alpha, nTune) / z
}

// SeqHalfWidth is the always-valid half-width for a given standard error, on the proportion scale.
//
// Computed as se * widthFactor rather than as k * z * se — algebraically identical for every alpha
// we allow, but numerically safe at the edges the p-value bisection walks through: as alpha -> 1
// the critical value z -> 0 and k -> infinity, and k*z*se would evaluate 0 * infinity and hand back
// NaN. A NaN p-value renders as "significant" in most templates, which is the worst possible
// failure for this particular number.
func SeqHalfWidth(se float64, n int, alpha float64, nTune int) float64 {
	if se <= 0 || n <= 0 {
		return 0
	}
	return se * seqWidthFactor(n, alpha, nTune)
}

// SeqAlwaysValidP is the anytime-valid p-value: the smallest alpha at which the confidence
// sequence would exclude zero, i.e. the alpha solving half_seq(alpha) = |diff|.
//
// Found by bisection with a FIXED iteration count and no RNG, so it is bit-reproducible. The
// bracket is alpha in [1e-12, 1] as specified, walked in log-alpha because that is the only
// conditioning under which the answer is equally precise at p = 0.4 and p = 1e-11: linear
// bisection puts its entire resolution in the first decade and returns a number whose round trip
// through half_seq misses by orders of magnitude down at the floor.
//
// Bisection is only legitimate because half_seq is strictly decreasing in alpha — a wider interval
// is what a smaller alpha buys. That monotonicity is not assumed here, it is pinned by
// TestSeqHalfWidthIsMonotoneInAlpha; without it bisection returns a deterministic wrong answer,
// which is strictly worse than a nondeterministic one because nobody ever catches it.
//
// Note this p depends on n_tune (through rho) but NOT on the experiment's alpha. It is the
// evidence, not the decision.
func SeqAlwaysValidP(diff, se float64, n, nTune int) float64 {
	d := math.Abs(diff)
	if n <= 0 || nTune <= 0 || se <= 0 || d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return 1
	}
	half := func(alpha float64) float64 { return se * seqWidthFactor(n, alpha, nTune) }
	// Even at the floor the interval is narrower than the difference: the evidence is past what we
	// are willing to print, so print the floor rather than a number manufactured by rounding.
	if half(seqPFloor) <= d {
		return seqPFloor
	}
	loX, hiX := math.Log(seqPFloor), 0.0 // alpha = exp(x), so x=0 is alpha=1
	for i := 0; i < seqBisectIters; i++ {
		mid := (loX + hiX) / 2
		if half(math.Exp(mid)) > d {
			loX = mid // interval still too wide: we need a larger alpha
		} else {
			hiX = mid
		}
	}
	p := math.Exp((loX + hiX) / 2)
	switch {
	case p >= 1-1e-9:
		// The difference is smaller than the widest interval this construction can draw. That is
		// "no evidence at all", and it should read as exactly 1 rather than 0.99999999996.
		return 1
	case p < seqPFloor:
		return seqPFloor
	}
	return p
}

// seqRhoSq is rho^2, the tuning parameter of the confidence sequence:
//
//	rho = sqrt( ( -2·ln(alpha) + ln( -2·ln(alpha) + 1 ) ) / N* )
//
// Kept squared throughout because rho only ever appears as rho^2 — taking the square root and
// squaring it back is two roundings the algebra never asked for.
//
// This choice of rho is what makes the interval narrowest at N = N*; every other N pays for it.
// That is the entire reason n_tune must default to the power calculator's n_planned and not to a
// number picked because it looks round.
func seqRhoSq(alpha float64, nTune int) float64 {
	if nTune <= 0 || !(alpha > 0 && alpha < 1) {
		return math.NaN()
	}
	a := -2 * math.Log(alpha)
	return (a + math.Log(a+1)) / float64(nTune)
}

// seqWidthFactor is the multiplier on the standard error:
//
//	sqrt( 2·(u + 1)/u · ln( sqrt(u + 1) / alpha ) ),  u = N·rho^2
//
// which is the spec's k(N)·z with the z divided back out. Infinite for degenerate inputs, never
// NaN, so a caller that forgets to check gets an interval spanning everything — obviously
// unusable — rather than a blank one that reads as certainty.
func seqWidthFactor(n int, alpha float64, nTune int) float64 {
	if n <= 0 {
		return math.Inf(1)
	}
	u := float64(n) * seqRhoSq(alpha, nTune)
	if math.IsNaN(u) || u <= 0 {
		return math.Inf(1)
	}
	return math.Sqrt(2 * (u + 1) / u * math.Log(math.Sqrt(u+1)/alpha))
}

// seqZCrit is the two-sided normal critical value z_{1-alpha/2}.
func seqZCrit(alpha float64) float64 {
	if !(alpha > 0 && alpha < 1) {
		return math.NaN()
	}
	if alpha == SeqDefaultAlpha {
		// At the default alpha — which is what nearly every experiment runs at — return the
		// constant the fixed-horizon path already uses. seqNormInv lands within an ulp of it, and
		// an ulp is enough to make k(N)·z·se and z95·se disagree in the last printed digit, so the
		// same experiment read two ways would show two different numbers for what is definitionally
		// the same quantity. Bit-identical, not approximately identical.
		return z95
	}
	return seqNormInv(1 - alpha/2)
}

// seqNormInv is the inverse standard normal CDF.
//
// The standard library has erf and erfc but no inverse, and the whole point of an always-valid
// p-value is that alpha is a free variable — a hardcoded 1.96 cannot answer "at what alpha would
// this just barely be called". So: Acklam's rational approximation for the starting point, then
// two Halley steps against math.Erfc, which lands within a few ulp. Deterministic, no iteration
// count that depends on the input, no dependency added.
func seqNormInv(p float64) float64 {
	switch {
	case math.IsNaN(p) || p <= 0 || p >= 1:
		return math.NaN()
	}
	a := [6]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [5]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01}
	c := [6]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [4]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00}
	const plow = 0.02425
	var x float64
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > 1-plow:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
	// Halley refinement. Acklam alone is good to ~1e-9 relative, which is fine for a chart and not
	// fine for a round-trip assertion at 1e-9, and it would put a visible wobble in the last digits
	// of a number we tell people to reproduce byte for byte.
	for i := 0; i < 2; i++ {
		e := 0.5*math.Erfc(-x/math.Sqrt2) - p
		u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
		if math.IsNaN(u) || math.IsInf(u, 0) {
			break // far tail: exp overflows before the correction matters
		}
		x -= u / (1 + x*u/2)
	}
	return x
}
