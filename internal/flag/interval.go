package flag

import "math"

// Confidence intervals for experiment results.
//
// A significance boolean tells you almost nothing. "Significant: true" on 60 users and on 60,000
// users read identically, and neither says how big the effect might actually be. An interval
// says both at once: where the truth plausibly sits, and how much the data has narrowed it.
//
// This is also the "computed, not guessed" claim made visible. A reader who can see 43/512
// against 51/498, each with a range around it, can check the arithmetic. A reader shown only
// "significant" has to take it on faith, which is exactly the posture this product exists to
// avoid.

// z95 is the two-sided normal critical value at 95%.
const z95 = 1.959963984540054

// Interval is a range with the point estimate that generated it. Percentages, not proportions,
// because every other number on the report is a percentage and mixing the two is how a reader
// misreads by 100x.
type Interval struct {
	Point float64 `json:"point"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

// wilson computes the Wilson score interval for a proportion.
//
// Not the textbook normal approximation, which is wrong in exactly the cases an early experiment
// lives in: at small n, or when the rate is near 0% or 100%, it produces bounds below zero or
// above one. An interval that says a conversion rate might be -4% destroys trust in every other
// number on the page. Wilson stays inside [0,1] by construction and is the standard choice.
func wilson(successes, trials int, z float64) Interval {
	if trials <= 0 {
		return Interval{}
	}
	n := float64(trials)
	p := float64(successes) / n
	z2 := z * z
	denom := 1 + z2/n
	center := (p + z2/(2*n)) / denom
	half := (z / denom) * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	lo, hi := center-half, center+half
	return Interval{
		Point: round1(100 * p),
		Lo:    round1(100 * math.Max(0, lo)),
		Hi:    round1(100 * math.Min(1, hi)),
	}
}

// twoProportionP is the two-sided p-value for a difference in conversion rates.
//
// Reported alongside the interval rather than instead of it. The p-value answers "could this be
// noise"; the interval answers "how big is it". Shipping a change needs both, and a tool that
// gives only the first invites the classic mistake of acting on a statistically significant
// effect too small to matter.
func twoProportionP(c1, n1, c2, n2 int) float64 {
	if n1 <= 0 || n2 <= 0 {
		return 1
	}
	p1 := float64(c1) / float64(n1)
	p2 := float64(c2) / float64(n2)
	pooled := float64(c1+c2) / float64(n1+n2)
	se := math.Sqrt(pooled * (1 - pooled) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return 1
	}
	z := math.Abs(p1-p2) / se
	// Two-sided tail of the standard normal: erfc(z/sqrt(2)).
	return math.Erfc(z / math.Sqrt2)
}

// liftBlock names WHY a relative-lift interval could not be computed.
//
// It replaces a bare ok=false, and that was a correctness problem, not a tidiness one. Every
// degenerate input collapsed into ONE sentence: "the control rate is too close to zero…". Fed
// control 60/200 (30%) against a test arm of 0/200, the report said the CONTROL was near zero —
// naming the healthy arm as the broken one, and reading as a data-quality shrug rather than
// "this arm converted nobody". The two mirror-image inputs liftInterval(0,200,60,200) and
// liftInterval(60,200,0,200) were literally indistinguishable to the reader.
type liftBlock int

const (
	liftOK            liftBlock = iota
	liftNoSample                // an arm has no exposures at all
	liftNoConversions           // neither arm has converted anyone yet
	liftTestZero                // the test arm converted nobody: log(0) is -inf
	liftCtrlZero                // the control converted nobody: nothing to divide by
	liftSaturated               // an arm converted every user it was shown; variance undefined at p=1
	liftCtrlNearZero            // control is nonzero but statistically indistinguishable from zero
)

// liftInterval is the confidence interval on RELATIVE lift, as a percentage.
//
// Computed on the log ratio and transformed back, because relative lift is a ratio and its
// sampling distribution is skewed — a symmetric interval around the point estimate would put the
// lower bound below -100%, which is not a thing that can happen.
//
// Returns a non-OK liftBlock when the control arm is too close to zero to divide by. A control
// converting at 0.1% turns any movement into a four-figure percentage, and "+4,000% lift" from
// eleven conversions is the single most misleading number an experiment tool can print. Better to
// show the absolute difference and say the relative figure is not meaningful — but say WHICH arm
// made it meaningless, which is what the liftBlock carries.
func liftInterval(cTest, nTest, cCtrl, nCtrl int, z float64) (Interval, liftBlock) {
	switch {
	case nTest <= 0 || nCtrl <= 0:
		return Interval{}, liftNoSample
	case cTest <= 0 && cCtrl <= 0:
		return Interval{}, liftNoConversions
	case cTest <= 0:
		return Interval{}, liftTestZero
	case cCtrl <= 0:
		return Interval{}, liftCtrlZero
	}
	p1 := float64(cTest) / float64(nTest)
	p2 := float64(cCtrl) / float64(nCtrl)
	if p1 >= 1 || p2 >= 1 {
		return Interval{}, liftSaturated // log-ratio variance is undefined at a 100% rate
	}
	// The control rate must be far enough from zero that a ratio means anything. Eppo publishes
	// this guard as a multiple of standard deviations; the same idea, stated in the units here.
	seCtrl := math.Sqrt(p2 * (1 - p2) / float64(nCtrl))
	if seCtrl <= 0 || p2 < 10*seCtrl {
		return Interval{}, liftCtrlNearZero
	}
	logRatio := math.Log(p1 / p2)
	seLog := math.Sqrt((1-p1)/(p1*float64(nTest)) + (1-p2)/(p2*float64(nCtrl)))
	lo := math.Exp(logRatio-z*seLog) - 1
	hi := math.Exp(logRatio+z*seLog) - 1
	return Interval{
		Point: round1(100 * (p1/p2 - 1)),
		Lo:    round1(100 * lo),
		Hi:    round1(100 * hi),
	}, liftOK
}

// readLift turns an interval on relative lift into the sentence a person should act on.
//
// The wording is the product. "Significant" invites shipping; "between 2% worse and 31% better"
// invites waiting, which is usually the correct call and the one a boolean never prompts.
//
// It takes the raw counts because the degenerate cases have nothing else to say. A test arm at
// 0 of 200 against a control at 60 of 200 has no ratio, but it is not an unknown: it is the
// clearest loss the tool can find, and the previous single-string branch buried it under a
// sentence about the control being near zero. Naming the counts is what makes the verdict
// checkable rather than a shrug.
func readLift(ci Interval, why liftBlock, p float64, enoughSample bool, cTest, nTest, cCtrl, nCtrl int) string {
	if !enoughSample {
		return "too few users so far to say anything; the interval would be wider than any effect worth shipping"
	}
	of := func(c, n int) string { return itoa(c) + " of " + itoa(n) }
	switch why {
	case liftNoSample:
		return "one arm has no exposures at all, so there is nothing to compare — check that the flag is being read on both code paths"
	case liftNoConversions:
		return "neither arm has converted anyone yet (" + of(cTest, nTest) + " against control's " +
			of(cCtrl, nCtrl) + "), so there is nothing to compare — if that looks wrong it is usually the goal event name"
	case liftTestZero:
		return "this arm converted " + of(cTest, nTest) + " while control converted " + of(cCtrl, nCtrl) +
			" — a relative lift is undefined against zero, but this arm is strictly worse"
	case liftCtrlZero:
		return "control converted " + of(cCtrl, nCtrl) + " while this arm converted " + of(cTest, nTest) +
			" — there is no control rate to divide by, so the lift is undefined, but this arm is strictly better"
	case liftSaturated:
		return "one arm converted every user it was shown (" + of(cTest, nTest) + " against control's " +
			of(cCtrl, nCtrl) + "), so a relative interval is undefined — read the raw counts instead"
	case liftCtrlNearZero:
		return "the control rate is too close to zero for a relative comparison to mean anything — read the raw counts instead"
	}
	switch {
	case ci.Lo > 0:
		return "this arm is better; the true lift is very likely between " + pct(ci.Lo) + " and " + pct(ci.Hi)
	case ci.Hi < 0:
		return "this arm is worse; the true change is very likely between " + pct(ci.Lo) + " and " + pct(ci.Hi)
	default:
		return "no clear difference yet — the range still spans zero (" + pct(ci.Lo) + " to " + pct(ci.Hi) + "), so this could be a win or a loss. Keep running or accept it as flat."
	}
}

func pct(f float64) string {
	s := ""
	if f > 0 {
		s = "+"
	}
	return s + trimFloat(f) + "%"
}

func trimFloat(f float64) string {
	r := math.Round(f*10) / 10
	if r == math.Trunc(r) {
		return itoa(int(r))
	}
	neg := ""
	if r < 0 {
		neg, r = "-", -r
	}
	whole := int(r)
	frac := int(math.Round((r - float64(whole)) * 10))
	if frac == 10 {
		whole, frac = whole+1, 0
	}
	return neg + itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// diffInterval is the RISK DIFFERENCE — test minus control, in percentage points — with a
// Newcombe hybrid-score interval.
//
// It exists because the relative interval above honestly refuses the most important case this
// product has: an error-rate guardrail. A relative margin needs a control rate far enough from
// zero for a ratio to mean anything, and a healthy product's error rate is nowhere near it — so
// liftInterval returns liftCtrlNearZero and the guardrail reads INCONCLUSIVE forever. The safety
// net was quietest exactly where it should have been loudest.
//
// "No more than half a percentage point more of your users hitting an error" is answerable at any
// base rate, and is what a person actually means. Newcombe rather than Wald because Wald's
// interval misbehaves near zero, which is the entire region of interest here.
func diffInterval(cTest, nTest, cCtrl, nCtrl int, z float64) (Interval, liftBlock) {
	if nTest <= 0 || nCtrl <= 0 {
		return Interval{}, liftNoSample
	}
	p1 := float64(cTest) / float64(nTest)
	p2 := float64(cCtrl) / float64(nCtrl)
	// Wilson bounds per arm, then combined — this is the part that stays sane at a 0% rate,
	// where a Wald interval would produce a zero-width interval and a confident wrong answer.
	w1 := wilson(cTest, nTest, z)
	w2 := wilson(cCtrl, nCtrl, z)
	l1, u1 := w1.Lo/100, w1.Hi/100
	l2, u2 := w2.Lo/100, w2.Hi/100
	lo := (p1 - p2) - math.Sqrt((p1-l1)*(p1-l1)+(u2-p2)*(u2-p2))
	hi := (p1 - p2) + math.Sqrt((u1-p1)*(u1-p1)+(p2-l2)*(p2-l2))
	return Interval{
		Point: round1(100 * (p1 - p2)),
		Lo:    round1(100 * lo),
		Hi:    round1(100 * hi),
	}, liftOK
}

// ProportionsDiffer is the exported two-proportion test, for callers outside this package that
// need to ask "did this rate actually change" with the SAME arithmetic the experiment reports use.
//
// Exported rather than reimplemented because the recurring defect in this codebase is two
// surfaces computing one question differently and disagreeing in front of a customer. The weekly
// update's "unchanged across 23 ships" claim has to be the same statistic as everything else, or
// it is a fourth definition waiting to drift.
//
// Returns whether the difference clears alpha, and the two-sided p-value. Under-powered inputs
// return false with a p near 1, which reads as "cannot tell" rather than "no change" — the caller
// must keep those apart, because they are opposite findings.
func ProportionsDiffer(c1, n1, c2, n2 int, alpha float64) (bool, float64) {
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.05
	}
	p := twoProportionP(c1, n1, c2, n2)
	return p < alpha, p
}
