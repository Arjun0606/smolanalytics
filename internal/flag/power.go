package flag

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Sample size, minimum detectable effect, and duration for an experiment — the three numbers a
// person needs BEFORE they start, and the reason they otherwise peek at the result every morning.
//
// Nothing here reads the clock. A window arrives as two absolute instants and is echoed back in
// the answer, so the same events and the same request produce the same plan tomorrow as today.
// The rest of this package earned that property the hard way (see MeasureRange's defect list);
// a planner that silently resolved "last 28 days" against time.Now() would hand it straight back.
//
// Everything the number depends on is stated in the result rather than assumed by the caller:
// alpha, power, the baseline and where it came from, the randomisation unit, the allocation, the
// arm actually powered, and whether the sequential penalty was applied. An implementer reading
// only the JSON should be able to redo the arithmetic.

const (
	// Defaults, applied when the caller leaves the field at zero and echoed in the result so
	// nobody has to guess which convention was used.
	defaultAlpha = 0.05
	defaultPower = 0.80

	// Two business cycles. Weekday and weekend users are different people, and a test that
	// finishes on Wednesday has measured only one kind of them. This is a floor on the
	// RECOMMENDED duration, never on the computed one — both are reported.
	minRunDays = 14

	// Kohavi's rule of thumb for how many users per arm a t-test needs before the normal
	// approximation holds on a skewed metric: n ~= 355 * skew^2. Past this much skew the
	// honest answer is a refusal, not a sample size.
	maxGoalSkewness   = 5.0
	kohaviSkewFactor  = 355.0
	mdeSearchLo       = 1e-6
	mdeSearchHi       = 10.0
	mdeSearchRounds   = 60 // halves the bracket 60 times: 10 / 2^60, far below the 1e-6 tolerance
	sequentialDoubles = 60
)

// PowerBaseline is what this instance's own events say about the goal, over one resolved window.
//
// The whole point of computing it here is that nobody should have to type a baseline conversion
// rate they do not know. The cost of that convenience is that the number carries assumptions, so
// every one of them is a field: which window, which randomisation unit, how many days, and the
// caveat that active units are not the same thing as ELIGIBLE units.
type PowerBaseline struct {
	// Unit names the randomisation unit the rate was computed over — "bucket_id" when the
	// events carry one, "distinct_id" otherwise. It is stated rather than assumed because the
	// two are different estimands: bucket_id is device-scoped and survives identify(), so a
	// rate per bucket and a rate per person are different numbers and averaging them is
	// meaningless.
	Unit string `json:"unit"`
	// From and To echo the resolved half-open window [from, to) as RFC3339 UTC. Echoed, not
	// remembered: a caller who asked for "28 days" gets told exactly which 28 days answered.
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`

	Units      int     `json:"units"`      // distinct randomisation units with any event in the window
	Converters int     `json:"converters"` // units that did the goal at least once in the window
	RatePct    float64 `json:"rate_pct"`   // p_A as a percentage, for display
	Rate       float64 `json:"-"`          // p_A as a proportion, for arithmetic

	// DailyUnits is the MEDIAN distinct units per day, not the mean: one launch day or one bot
	// crawl otherwise doubles the planned traffic and halves the estimated duration.
	DailyUnits float64 `json:"daily_units"`

	// EligibilityCaveat is present always, because DailyUnits counts ACTIVE units and an
	// experiment only randomises units that reach the flag AND pass its targeting rules. The
	// honest denominator for eligibility cannot be computed from events alone — it depends on
	// whether the code path ran — so this says so instead of quietly reporting a duration that
	// is too optimistic by whatever the targeting rules filter out.
	EligibilityCaveat string `json:"eligibility_caveat"`

	// ValueProp and Skewness are populated only for a value goal (revenue per unit, say).
	// Skewness is the sample skewness of the per-unit total, zeros included, because a unit
	// that never converted is still a randomised unit and dropping it flatters the tail.
	ValueProp string  `json:"value_prop,omitempty"`
	Skewness  float64 `json:"skewness,omitempty"`
}

// PowerInput is one power question. Everything optional has a stated default.
type PowerInput struct {
	// Baseline is p_A as a PROPORTION in (0,1), typically PowerBaseline.Rate.
	Baseline float64 `json:"baseline"`
	// MDEPct is the RELATIVE minimum detectable effect in percent: 5 means "detect a 5%
	// relative lift", i.e. p_B = p_A * 1.05. Relative because that is how people describe a
	// win, and mixing it up with an absolute 5 percentage points is a 20x error at a 5%
	// baseline — which is why both forms are echoed in the result.
	MDEPct float64 `json:"mde_pct"`

	Alpha float64 `json:"alpha"` // 0 => 0.05
	Power float64 `json:"power"` // 0 => 0.80

	// Variants is the flag's arms in DECLARED order, control first — the same control rule
	// MeasureRange uses, so the plan and the report agree on which arm is the baseline.
	// Empty or single => a boolean flag, powered as an even two-arm split.
	Variants []Variant `json:"variants,omitempty"`

	// DailyUnits is eligible randomisation units per day. 0 => duration is not computed and
	// the result says so rather than printing a zero that reads as "ready today".
	DailyUnits float64 `json:"daily_units"`

	// Unit is the randomisation unit label to echo ("bucket_id" / "distinct_id"). Empty is
	// reported as unstated rather than defaulted, because guessing here silently changes the
	// estimand the sample size belongs to.
	Unit string `json:"unit"`

	// GoalSkewness, when non-zero, is the sample skewness of the per-unit goal value. Past
	// maxGoalSkewness ComputePower refuses: a normal-approximation sample size on a metric
	// that skewed is a confidently wrong number, which is worse than no number.
	GoalSkewness float64 `json:"goal_skewness,omitempty"`

	// HalfWidth is the always-valid (sequential) half-width of the difference interval, on the
	// proportion scale, given the fixed-horizon standard error at a per-arm N and that N. It is
	// the package's confidence-sequence half-width with alpha and n_tune already closed over.
	//
	// Passed in rather than reimplemented here so there is exactly ONE definition of the
	// sequence in this package. Two would drift apart, and the calculator would then plan for a
	// wider interval than the report renders — the user hits significance early and concludes
	// the planner lied. This function supplies the SE, since only it knows the design.
	//
	// nil => the sequential requirement is not computed and the result says why.
	//
	// The search assumes the half-width is non-increasing in N. The inflation factor k(N) does
	// grow again once N passes n_tune, but only like sqrt(log N), while the SE falls like
	// 1/sqrt(N) — so the product is decreasing everywhere past a handful of units. Stated
	// rather than detected: a non-monotone half-width makes "the smallest N that is narrow
	// enough" ill-defined, not merely hard to find.
	HalfWidth func(se float64, nPerArm int) float64 `json:"-"`
}

// PowerArm is one arm's share of the plan.
type PowerArm struct {
	Key      string  `json:"key"`
	Weight   int     `json:"weight"`
	SharePct float64 `json:"share_pct"`
	N        int     `json:"n"` // units this arm receives once NPlanned units are exposed
}

// PowerPlan is the answer, with every input that produced it restated.
type PowerPlan struct {
	Unit string `json:"unit"`

	BaselinePct float64 `json:"baseline_pct"` // p_A
	TargetPct   float64 `json:"target_pct"`   // p_B = p_A * (1 + r)
	MDEPct      float64 `json:"mde_pct"`      // relative, as asked
	MDEAbsPct   float64 `json:"mde_abs_pct"`  // p_B - p_A in percentage points, because people conflate the two
	Alpha       float64 `json:"alpha"`
	Power       float64 `json:"power"`

	Arms []PowerArm `json:"arms"`
	// PoweredArm is the arm the sample size was computed against control. With more than two
	// arms this is the SMALLEST non-control arm — the one that fills last, so the plan is not
	// quietly powered for the fastest comparison while the user waits on the slowest.
	PoweredArm string `json:"powered_arm"`
	// NPlanned is total units exposed across ALL arms. This is the headline number, and it is
	// also the default for the confidence sequence's tuning parameter n_tune: k(N) is
	// minimised near the N a design is actually aiming at, so tuning anywhere else
	// systematically widens every interval the experiment ever renders.
	NPlanned int `json:"n_planned"`
	// NControl and NTest are the two arms the power calculation is about, which is not the
	// same as NPlanned/len(arms) once the weights are uneven.
	NControl int `json:"n_control"`
	NTest    int `json:"n_test"`

	// NPlannedSequential is what the same design costs under always-valid inference, and
	// SequentialNote says either how much extra that is or why it could not be computed. Zero
	// with a note, never zero silently.
	NPlannedSequential int    `json:"n_planned_sequential"`
	SequentialNote     string `json:"sequential_note"`

	DurationDays    int     `json:"duration_days"`    // NPlanned / DailyUnits, rounded up; 0 when unknown
	RecommendedDays int     `json:"recommended_days"` // max(DurationDays, 14)
	DurationNote    string  `json:"duration_note"`
	DailyUnits      float64 `json:"daily_units"`

	// Formula names the closed form used, so a reader can check the arithmetic instead of
	// trusting it. Same reason every rate in this package ships its numerator and denominator.
	Formula string `json:"formula"`
	Read    string `json:"read"`
}

// ComputePower turns a design into a plan: how many units, split how, for how long.
//
// Two-sided, unpooled planning variance, normal approximation. Unpooled because the planning
// question is "how much noise will the DIFFERENCE have if the effect is real", which is
// p_A q_A / n_C + p_B q_B / n_T — the pooled form answers the null's question instead and, used
// only for the uneven-allocation branch (as the spec draft had it), makes a 50/50 split come out
// to two different sample sizes depending on which code path computed it. One design, two
// answers, is the exact defect this package exists to eliminate.
func ComputePower(in PowerInput) (PowerPlan, error) {
	alpha, power := in.Alpha, in.Power
	if alpha == 0 {
		alpha = defaultAlpha
	}
	if power == 0 {
		power = defaultPower
	}
	if err := validateDesign(in.Baseline, in.MDEPct, alpha, power); err != nil {
		return PowerPlan{}, err
	}
	if err := refuseHeavyTail(in.GoalSkewness); err != nil {
		return PowerPlan{}, err
	}

	r := in.MDEPct / 100
	pA := in.Baseline
	pB := pA * (1 + r)
	if pB >= 1 {
		return PowerPlan{}, fmt.Errorf(
			"a %.4g%% relative lift on a %.4g%% baseline is a %.4g%% conversion rate, which cannot happen — "+
				"lower the MDE or state it in absolute percentage points",
			in.MDEPct, 100*pA, 100*pB)
	}

	arms, ctrlIdx, testIdx, err := powerArms(in.Variants)
	if err != nil {
		return PowerPlan{}, err
	}
	totalW := 0.0
	for _, a := range arms {
		totalW += float64(a.Weight)
	}
	ctrlFrac := float64(arms[ctrlIdx].Weight) / totalW
	testFrac := float64(arms[testIdx].Weight) / totalW
	ratio := testFrac / ctrlFrac

	nCtrl := requiredControlExact(pA, pB, alpha, power, ratio)
	// Units exposed overall, not just to the two arms being compared. With three arms the
	// third one still consumes traffic, and a plan that ignored it would under-count the wait
	// by exactly that arm's share.
	nTotal := nCtrl / ctrlFrac

	// Every arm rounds UP, and the headline is then the SUM of the arms rather than a separate
	// ceil of the total. Rounding the total independently is how a plan ends up saying "29,497
	// users" above a table whose rows add to 29,498: two numbers for one design, on one screen,
	// which is the exact class of defect this package exists to remove. Rounding up rather than
	// apportioning also means no arm is ever handed fewer units than its own power requirement,
	// so the plan can only be conservative, never quietly underpowered.
	for i := range arms {
		share := float64(arms[i].Weight) / totalW
		arms[i].SharePct = round1(100 * share)
		arms[i].N = ceilInt(nTotal * share)
	}
	nPlanned := 0
	for _, a := range arms {
		nPlanned += a.N
	}

	plan := PowerPlan{
		Unit:        unitLabel(in.Unit),
		BaselinePct: round4(100 * pA),
		TargetPct:   round4(100 * pB),
		MDEPct:      in.MDEPct,
		MDEAbsPct:   round4(100 * (pB - pA)),
		Alpha:       alpha,
		Power:       power,
		Arms:        arms,
		PoweredArm:  arms[testIdx].Key,
		NPlanned:    nPlanned,
		// Taken from the arms, not recomputed, so the two arms named in the prose are literally
		// the rows in the table.
		NControl:   arms[ctrlIdx].N,
		NTest:      arms[testIdx].N,
		DailyUnits: in.DailyUnits,
		Formula: "n_control = (z_(1-alpha/2) + z_power)^2 * [p_A(1-p_A) + p_B(1-p_B)/k] / (p_B-p_A)^2, " +
			"k = n_test/n_control; n_planned = sum over arms of ceil(n_control / control_share * arm_share)",
	}

	plan.NPlannedSequential, plan.SequentialNote = sequentialRequirement(in.HalfWidth, pA, pB, ratio, ctrlFrac, power, plan.NControl, plan.NPlanned)
	plan.DurationDays, plan.RecommendedDays, plan.DurationNote = planDuration(plan.NPlanned, in.DailyUnits)
	plan.Read = readPlan(plan, len(arms))
	return plan, nil
}

// MDEForSample is the inverse: given a total number of exposed units you can actually get, what
// is the smallest relative lift the design can detect?
//
// This is the question people actually have — they know their traffic, not their effect size —
// and answering it is what stops someone starting a test that could never have concluded.
func MDEForSample(baseline float64, nPlanned int, alpha, power float64, variants []Variant) (float64, error) {
	if alpha == 0 {
		alpha = defaultAlpha
	}
	if power == 0 {
		power = defaultPower
	}
	if nPlanned <= 0 {
		return 0, fmt.Errorf("need a positive number of exposed units to solve for an MDE, got %d", nPlanned)
	}
	if err := validateDesign(baseline, 1, alpha, power); err != nil {
		return 0, err
	}
	arms, ctrlIdx, testIdx, err := powerArms(variants)
	if err != nil {
		return 0, err
	}
	totalW := 0.0
	for _, a := range arms {
		totalW += float64(a.Weight)
	}
	ctrlFrac := float64(arms[ctrlIdx].Weight) / totalW
	ratio := (float64(arms[testIdx].Weight) / totalW) / ctrlFrac
	r, err := mdeExact(baseline, float64(nPlanned), alpha, power, ratio, ctrlFrac)
	if err != nil {
		return 0, err
	}
	return 100 * r, nil
}

// mdeExact solves requiredExposedExact(r) == nTotal for r by bisection.
//
// Bisection rather than a closed form because the required-n expression is not invertible in
// closed form once p_B(1-p_B) moves with r — and bisection is exactly reproducible, which a
// Newton iteration seeded from a floating-point guess is not.
//
// Valid because required-n is strictly DECREASING in r over the whole bracket: differentiating
// [p_A q_A + p_B q_B/k] / (p_B-p_A)^2 with respect to p_B gives a numerator of
// (1-2p_B)(p_B-p_A)/k - 2(p_A q_A + p_B q_B/k), which is negative for every 0 < p_A < p_B < 1 and
// every k > 0. Monotonicity is proven rather than assumed because a bisection on a non-monotone
// function returns a confident wrong answer instead of failing.
func mdeExact(pA, nTotal, alpha, power, ratio, ctrlFrac float64) (float64, error) {
	// p_B must stay below 1, so the largest relative lift that means anything is capped by the
	// baseline itself: at p_A = 0.5 there is no such thing as a 200% lift.
	hi := math.Min(mdeSearchHi, (1-1e-9)/pA-1)
	if hi <= mdeSearchLo {
		return 0, fmt.Errorf("a baseline of %.4g%% leaves no room for a relative lift — there is no reachable target rate below 100%%", 100*pA)
	}
	need := func(r float64) float64 {
		return requiredControlExact(pA, pA*(1+r), alpha, power, ratio) / ctrlFrac
	}
	if need(hi) > nTotal {
		return 0, fmt.Errorf(
			"%.0f exposed units cannot detect any lift at alpha=%.3g power=%.3g on a %.4g%% baseline: "+
				"even a jump to %.4g%% would need %.0f units. Get more traffic, accept lower power, or pick a goal that fires more often",
			nTotal, alpha, power, 100*pA, 100*pA*(1+hi), need(hi))
	}
	lo := mdeSearchLo
	if need(lo) < nTotal {
		// More traffic than any sensible effect needs. Returning the floor is honest — the
		// design detects effects smaller than a thousandth of a percent, which is another way
		// of saying the constraint is no longer sample size.
		return lo, nil
	}
	for i := 0; i < mdeSearchRounds; i++ {
		mid := (lo + hi) / 2
		if need(mid) > nTotal {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2, nil
}

// requiredControlExact is the CONTINUOUS per-control-arm sample size. Continuous because the
// MDE inversion round-trips through it, and rounding up inside the objective would put a
// staircase in the middle of the bisection — the returned MDE would then be right to about one
// unit of n rather than to 1e-6.
func requiredControlExact(pA, pB, alpha, power, ratio float64) float64 {
	z := powerZ(1-alpha/2) + powerZ(power)
	delta := pB - pA
	return z * z * (pA*(1-pA) + pB*(1-pB)/ratio) / (delta * delta)
}

// sequentialRequirement finds the smallest per-arm N at which an always-valid design still has
// the requested power against the planned effect — the price of being allowed to peek.
//
// The criterion is half_seq(N) + z_power * se(N) <= delta, NOT the tempting one-liner
// "half_seq(N) <= delta". The one-liner is wrong in a way that prints backwards: at the fixed
// horizon the interval half-width is already only z_(1-alpha/2)/(z_(1-alpha/2)+z_power) ~= 70% of
// the effect — that gap IS the power — so "narrow enough to exclude delta" is satisfied at about
// 83% of the fixed-horizon sample, and the report would announce that always-valid inference is
// CHEAPER than a fixed horizon. It is not; it costs more, and the whole point of showing this
// number is to price that honestly.
//
// The criterion used here reduces exactly to the fixed-horizon formula when the inflation factor
// k(N) is 1 (half_seq = z_(1-alpha/2)*se gives delta = (z_(1-alpha/2)+z_power)*se), so the two
// figures are the same design measured two ways rather than two different designs.
//
// It is a conservative bound: it powers the LAST look, while a sequence that crosses early stops
// early. Overstating the cost of peeking is the safe direction for a number a user plans around.
//
// Returns (0, reason) rather than a silent zero when it cannot be computed. A blank 0 next to
// "fixed horizon: 12,400" reads as "sequential is free", which is the opposite of true.
func sequentialRequirement(halfWidth func(float64, int) float64, pA, pB, ratio, ctrlFrac, power float64, nCtrlFixed, nPlannedFixed int) (int, string) {
	if halfWidth == nil {
		return 0, "not computed: no always-valid half-width was supplied, so the peeking penalty for this design is unknown"
	}
	delta := pB - pA
	if delta <= 0 || nCtrlFixed <= 0 || ratio <= 0 || ctrlFrac <= 0 {
		return 0, "not computed: the planned effect is not positive"
	}
	// The SE of the DIFFERENCE at a given control-arm size, under the same unpooled planning
	// variance the fixed-horizon number used. Reusing the planning variance is what makes the
	// two figures comparable: a sequential requirement computed against a pooled SE would be
	// telling the user the cost of peeking plus the cost of a different variance assumption,
	// and labelling the sum "the price of peeking".
	seAt := func(nCtrl int) float64 {
		if nCtrl <= 0 {
			return math.Inf(1)
		}
		nc := float64(nCtrl)
		return math.Sqrt(pA*(1-pA)/nc + pB*(1-pB)/(ratio*nc))
	}
	zPower := powerZ(power)
	// "Powered enough", not "narrow enough" — see the criterion note above.
	narrowEnough := func(nCtrl int) bool {
		se := seAt(nCtrl)
		return halfWidth(se, nCtrl)+zPower*se <= delta
	}

	n := nCtrlFixed
	if !narrowEnough(n) {
		found := false
		for i := 0; i < sequentialDoubles; i++ {
			n *= 2
			if n <= 0 { // overflow guard: better to say "unbounded" than to wrap to a small N
				return 0, "not computed: the always-valid interval does not reach the planned effect at any practical sample size"
			}
			if narrowEnough(n) {
				found = true
				break
			}
		}
		if !found {
			return 0, "not computed: the always-valid interval does not reach the planned effect at any practical sample size"
		}
	}
	// n now satisfies the width requirement; bisect back to the boundary. Non-increasing
	// half-width makes this bracket valid (see PowerInput.HalfWidth).
	lo, hi := n/2, n
	if narrowEnough(nCtrlFixed) {
		lo = 1
	}
	for lo < hi {
		mid := lo + (hi-lo)/2
		if narrowEnough(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	total := ceilInt(float64(lo) / ctrlFrac)
	// The fixed-horizon figure quoted here is the plan's OWN NPlanned, passed in, not a second
	// derivation of it. Recomputing it from the control arm re-rounds and lands a unit or two
	// away, and the note would then price the peeking penalty against a headline number that
	// appears nowhere else in the payload.
	fixedTotal := nPlannedFixed
	extra := 0.0
	if fixedTotal > 0 {
		extra = 100 * (float64(total)/float64(fixedTotal) - 1)
	}
	return total, fmt.Sprintf(
		"always-valid inference needs %d units against the fixed horizon's %d — %.0f%% more, and that is the price of being allowed to look whenever you want",
		total, fixedTotal, extra)
}

// planDuration converts units into days, and refuses to let the arithmetic be the whole answer.
func planDuration(nPlanned int, dailyUnits float64) (days, recommended int, note string) {
	if dailyUnits <= 0 {
		return 0, minRunDays, fmt.Sprintf(
			"duration unknown: no daily unit rate was supplied. Regardless of what the sample size says, run at least %d days — "+
				"weekday and weekend users are different people", minRunDays)
	}
	days = ceilInt(float64(nPlanned) / dailyUnits)
	recommended = days
	if recommended < minRunDays {
		recommended = minRunDays
		return days, recommended, fmt.Sprintf(
			"the sample size arrives in %d days at %.0f units/day, but run %d anyway — one business cycle measures one kind of user",
			days, dailyUnits, minRunDays)
	}
	return days, recommended, fmt.Sprintf("about %d days at %.0f units/day", days, dailyUnits)
}

func readPlan(p PowerPlan, nArms int) string {
	s := fmt.Sprintf("to detect a %.4g%% relative lift on a %.4g%% baseline (that is %.4g%% to %.4g%%) at alpha=%.3g with %.0f%% power, "+
		"you need %d exposed %s in total",
		p.MDEPct, p.BaselinePct, p.BaselinePct, p.TargetPct, p.Alpha, 100*p.Power, p.NPlanned, p.Unit)
	if nArms > 2 {
		s += fmt.Sprintf(", powered on %q — the smallest arm, so it is the one you will wait on", p.PoweredArm)
	}
	// The duration note is always appended, including when the duration is unknown — that branch
	// carries the "run at least two business cycles" floor, which is the sentence a user most
	// needs when the arithmetic could not produce a number.
	return s + ". " + p.DurationNote
}

// powerArms normalises the flag's variants into arms, picking the control and the arm to power.
func powerArms(variants []Variant) (arms []PowerArm, ctrlIdx, testIdx int, err error) {
	if len(variants) < 2 {
		// A boolean flag is a two-arm experiment with an even split. Named "control"/"on" to
		// match what Evaluate serves, so the plan and the report use the same words.
		return []PowerArm{{Key: "control", Weight: 1}, {Key: "on", Weight: 1}}, 0, 1, nil
	}
	arms = make([]PowerArm, 0, len(variants))
	for _, v := range variants {
		if v.Weight <= 0 {
			return nil, 0, 0, fmt.Errorf("variant %q has weight %d: an arm that receives no traffic cannot be powered, and one that receives none is not an arm", v.Key, v.Weight)
		}
		arms = append(arms, PowerArm{Key: v.Key, Weight: v.Weight})
	}
	// Control is the DECLARED first variant, the same rule MeasureRange uses. Picking it
	// alphabetically (the defect that shipped once already) inverts the sign of every lift.
	ctrlIdx = 0
	testIdx = -1
	for i := 1; i < len(arms); i++ {
		if testIdx == -1 || arms[i].Weight < arms[testIdx].Weight {
			testIdx = i
		}
	}
	return arms, ctrlIdx, testIdx, nil
}

func validateDesign(baseline, mdePct, alpha, power float64) error {
	if !(baseline > 0 && baseline < 1) {
		return fmt.Errorf("baseline must be a proportion strictly between 0 and 1, got %v — a 0%% baseline has no relative lift to detect and a 100%% one has nowhere to go", baseline)
	}
	if !(mdePct > 0) {
		return fmt.Errorf("mde_pct must be positive, got %v — a design that detects no effect is not a design", mdePct)
	}
	if !(alpha > 0 && alpha < 0.5) {
		return fmt.Errorf("alpha must be in (0, 0.5), got %v", alpha)
	}
	if !(power >= 0.5 && power < 1) {
		return fmt.Errorf("power must be in [0.5, 1), got %v — below 0.5 the design misses a real effect more often than it finds one, which is not worth running", power)
	}
	return nil
}

// refuseHeavyTail is the "when we cannot compute it correctly, we refuse and say why" rule,
// applied to the one place a proportion-based sample size quietly becomes nonsense.
func refuseHeavyTail(skew float64) error {
	if math.Abs(skew) <= maxGoalSkewness {
		return nil
	}
	need := kohaviSkewFactor * skew * skew
	return fmt.Errorf(
		"this goal's per-unit values have skewness %.4g, so the normal approximation behind every sample size here does not hold: "+
			"a t-test would need roughly 355*skew^2 = %.0f units per arm before it could be trusted. "+
			"Use a binary conversion goal, or cap the value at a percentile, instead of a number that would look precise and be wrong",
		skew, math.Ceil(need))
}

func unitLabel(u string) string {
	if u == "" {
		return "units (randomisation unit unstated)"
	}
	return u
}

// EstimateBaseline reads p_A, the daily unit rate, and (for a value goal) the skewness straight
// out of this instance's events, over one explicit half-open window [from, to).
//
// Both instants are required. "Last 28 days" resolved in here against the wall clock is the same
// bug MeasureRange was fixed for: the plan would change between two reads of the same page, and
// the number a user wrote down would never reproduce. The window is resolved by the caller at the
// API/MCP boundary and echoed back in the result.
//
// unitProp names the event property holding the randomisation unit (a bucket id). Empty, or
// absent from the events, falls back to DistinctID — and the result SAYS which was used, because
// a rate per device and a rate per person are different estimands and a plan built on one does
// not apply to the other.
func EstimateBaseline(evs []event.Event, goal, unitProp, valueProp string, from, to time.Time) (PowerBaseline, error) {
	if from.IsZero() || to.IsZero() {
		return PowerBaseline{}, fmt.Errorf("EstimateBaseline needs an explicit window: resolve days to absolute instants at the API boundary so the same request answers the same tomorrow")
	}
	if !to.After(from) {
		return PowerBaseline{}, fmt.Errorf("window is empty: from=%s is not before to=%s", powerRFC(from), powerRFC(to))
	}
	if goal == "" {
		return PowerBaseline{}, fmt.Errorf("need a goal event to estimate a baseline conversion rate")
	}

	unitSeen := false
	unitOf := func(e event.Event) string {
		if unitProp != "" {
			if v, ok := e.Properties[unitProp].(string); ok && v != "" {
				unitSeen = true
				return v
			}
		}
		return e.DistinctID
	}

	units := map[string]bool{}
	converters := map[string]bool{}
	perDay := map[string]map[string]bool{}
	values := map[string]float64{}

	for _, e := range evs {
		if e.Timestamp.Before(from) || !e.Timestamp.Before(to) { // half-open [from, to)
			continue
		}
		id := unitOf(e)
		if id == "" {
			continue
		}
		units[id] = true
		day := e.Timestamp.UTC().Format("2006-01-02")
		if perDay[day] == nil {
			perDay[day] = map[string]bool{}
		}
		perDay[day][id] = true
		if e.Name == goal {
			converters[id] = true
			if valueProp != "" {
				values[id] += numericProp(e.Properties[valueProp])
			}
		}
	}

	days := int(math.Ceil(to.Sub(from).Hours() / 24))
	if days < 1 {
		days = 1
	}
	unit := "distinct_id"
	if unitSeen {
		unit = unitProp
	}
	b := PowerBaseline{
		Unit:       unit,
		From:       powerRFC(from),
		To:         powerRFC(to),
		Days:       days,
		Units:      len(units),
		Converters: len(converters),
		DailyUnits: medianDailyUnits(perDay, from, days),
		EligibilityCaveat: "daily_units counts units ACTIVE in the window, not units eligible for the flag — " +
			"eligibility also depends on the targeting rules and on whether the code path ran at all, neither of which is in the events. " +
			"Treat the duration as a floor.",
		ValueProp: valueProp,
	}
	if len(units) > 0 {
		b.Rate = float64(len(converters)) / float64(len(units))
		b.RatePct = round4(100 * b.Rate)
	}
	if valueProp != "" {
		// Zeros included: a unit that never converted is still a randomised unit, and dropping
		// it makes a long-tailed revenue metric look tame enough for a t-test when it is not.
		vals := make([]float64, 0, len(units))
		for id := range units {
			vals = append(vals, values[id])
		}
		b.Skewness = round4(sampleSkewness(vals))
		if err := refuseHeavyTail(b.Skewness); err != nil {
			// The baseline is returned ALONGSIDE the error on purpose: the caller should be
			// able to show the user the skewness that caused the refusal, not just the word no.
			return b, err
		}
	}
	if len(units) == 0 {
		return b, fmt.Errorf("no events in %s .. %s, so there is no baseline to read — widen the window or check the goal name %q", powerRFC(from), powerRFC(to), goal)
	}
	return b, nil
}

// medianDailyUnits is the median over EVERY day in the window, silent days included as zero.
//
// Taking the median over only the days that had traffic is how a weekend-quiet product gets a
// duration estimate that assumes every day is a Tuesday.
func medianDailyUnits(perDay map[string]map[string]bool, from time.Time, days int) float64 {
	if days <= 0 {
		return 0
	}
	counts := make([]float64, 0, days)
	d := from.UTC()
	for i := 0; i < days; i++ {
		counts = append(counts, float64(len(perDay[d.AddDate(0, 0, i).Format("2006-01-02")])))
	}
	sort.Float64s(counts)
	mid := len(counts) / 2
	if len(counts)%2 == 1 {
		return counts[mid]
	}
	return (counts[mid-1] + counts[mid]) / 2
}

// sampleSkewness is the standard g1 moment estimator. Zero for fewer than three values or a
// constant series, where skewness is undefined rather than zero — but zero is the answer that
// keeps the refusal gate from firing on a metric it knows nothing about, and a two-value sample
// has bigger problems than its third moment.
func sampleSkewness(xs []float64) float64 {
	n := float64(len(xs))
	if n < 3 {
		return 0
	}
	mean := 0.0
	for _, x := range xs {
		mean += x
	}
	mean /= n
	var m2, m3 float64
	for _, x := range xs {
		d := x - mean
		m2 += d * d
		m3 += d * d * d
	}
	m2 /= n
	m3 /= n
	if m2 <= 0 {
		return 0
	}
	return m3 / math.Pow(m2, 1.5)
}

// Same coercion as everywhere else, with this caller's existing 0-on-miss contract preserved.
// The lossy shape is kept deliberately local: a non-number contributing 0 to a variance estimate
// is tolerable, whereas a non-number contributing 0 to a REVENUE total would be a fabricated
// figure, which is why revenue sizing uses event.Numeric's ok directly.
func numericProp(v any) float64 {
	n, _ := event.Numeric(v)
	return n
}

// powerZ is the inverse standard normal CDF: the z with P(Z <= z) = p.
//
// Go ships Erf and Erfc but not their inverse, and every sample-size formula here needs
// z_(1-alpha/2) and z_power. Acklam's rational approximation gives about 1.15e-9 relative error;
// the Halley refinement below — which uses the stdlib Erfc, so it is corrected against the exact
// function rather than against another approximation — takes it to full double precision. Full
// precision matters less for the answer (nobody cares about the 12th digit of a sample size) than
// for reproducibility: an approximation refined to machine epsilon returns the same bits on every
// run and every platform, and a plan that wobbles is a plan nobody writes down.
func powerZ(p float64) float64 {
	switch {
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	}
	const (
		pLow  = 0.02425
		pHigh = 1 - pLow
	)
	a := [6]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [5]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := [6]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [4]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}

	var x float64
	switch {
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	// Two Halley steps against the exact CDF.
	for i := 0; i < 2; i++ {
		e := 0.5*math.Erfc(-x/math.Sqrt2) - p
		u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
		x -= u / (1 + x*u/2)
	}
	return x
}

func ceilInt(f float64) int {
	if f <= 0 {
		return 0
	}
	return int(math.Ceil(f))
}

func powerRFC(t time.Time) string { return t.UTC().Format(time.RFC3339) }
