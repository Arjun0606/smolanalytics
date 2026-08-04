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

// liftInterval is the confidence interval on RELATIVE lift, as a percentage.
//
// Computed on the log ratio and transformed back, because relative lift is a ratio and its
// sampling distribution is skewed — a symmetric interval around the point estimate would put the
// lower bound below -100%, which is not a thing that can happen.
//
// Returns ok=false when the control arm is too close to zero to divide by. A control converting
// at 0.1% turns any movement into a four-figure percentage, and "+4,000% lift" from eleven
// conversions is the single most misleading number an experiment tool can print. Better to show
// the absolute difference and say the relative figure is not meaningful.
func liftInterval(cTest, nTest, cCtrl, nCtrl int, z float64) (Interval, bool) {
	if nTest <= 0 || nCtrl <= 0 || cTest <= 0 || cCtrl <= 0 {
		return Interval{}, false
	}
	p1 := float64(cTest) / float64(nTest)
	p2 := float64(cCtrl) / float64(nCtrl)
	if p1 >= 1 || p2 >= 1 {
		return Interval{}, false // log-ratio variance is undefined at a 100% rate
	}
	// The control rate must be far enough from zero that a ratio means anything. Eppo publishes
	// this guard as a multiple of standard deviations; the same idea, stated in the units here.
	seCtrl := math.Sqrt(p2 * (1 - p2) / float64(nCtrl))
	if seCtrl <= 0 || p2 < 10*seCtrl {
		return Interval{}, false
	}
	logRatio := math.Log(p1 / p2)
	seLog := math.Sqrt((1-p1)/(p1*float64(nTest)) + (1-p2)/(p2*float64(nCtrl)))
	lo := math.Exp(logRatio-z*seLog) - 1
	hi := math.Exp(logRatio+z*seLog) - 1
	return Interval{
		Point: round1(100 * (p1/p2 - 1)),
		Lo:    round1(100 * lo),
		Hi:    round1(100 * hi),
	}, true
}

// readLift turns an interval on relative lift into the sentence a person should act on.
//
// The wording is the product. "Significant" invites shipping; "between 2% worse and 31% better"
// invites waiting, which is usually the correct call and the one a boolean never prompts.
func readLift(ci Interval, ok bool, p float64, enoughSample bool) string {
	if !enoughSample {
		return "too few users so far to say anything; the interval would be wider than any effect worth shipping"
	}
	if !ok {
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
