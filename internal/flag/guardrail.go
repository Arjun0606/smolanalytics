package flag

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Guardrails: the metrics you are not trying to move, and are not willing to break.
//
// The mistake every homegrown A/B feature makes is running the SAME two-sided significance test on
// a guardrail as on the primary metric. That is backwards, and it fails silently in the direction
// that costs money. A two-sided test asks "did this change?" and answers "not significant" both
// for "we measured no harm across 40,000 users" and for "we have 60 users and no idea" — and the
// second one reads as a green light. What a guardrail actually asks is the non-inferiority
// question: "can I RULE OUT a degradation worse than δ?" That is a one-sided claim with a stated
// margin, it is only answerable with enough data, and it says so when it isn't.
//
// Hence three states, never two. Collapsing INCONCLUSIVE into PASS is exactly how guardrails
// become decorative, and a decorative guardrail is worse than none: it is a check somebody is
// relying on that has never once been able to fail.
//
// Guardrail directions. The names come from the metric's OWN good direction, which is the part
// that trips people up, so it is spelled out here rather than left to the reader:
//
//   - GuardrailNotWorse is for a metric where UP is good (checkout, activation, retention). It
//     forbids a relative DROP bigger than δ.
//   - GuardrailNotBetter is for a metric where UP is bad ($exception, refunds, support tickets).
//     It forbids a relative RISE bigger than δ.
//
// An error-rate guardrail declared not_worse is an easy and real mistake — it would forbid errors
// FALLING — which is why the direction is part of the plan and part of the plan hash.
const (
	GuardrailNotWorse  = "not_worse"
	GuardrailNotBetter = "not_better"
)

// DefaultGuardrailMarginPct is the relative non-inferiority margin used when a guardrail does not
// state one: 10% relative.
//
// It exists because δ=0 is not a guardrail. A non-inferiority test at margin zero IS a one-sided
// superiority test — it can only PASS by proving the treatment strictly better — so a zero-margin
// guardrail on error rate reads INCONCLUSIVE at every finite sample size, forever, on every
// experiment. A check that cannot pass, presented as a check that failed to pass, is the most
// expensive kind of wrong. See Guardrail.Validate.
const DefaultGuardrailMarginPct = 10.0

// Guardrail is one metric an experiment promises not to break, with the size of the degradation it
// must be able to rule out. It is part of the pre-registered plan, so it is hashed with it.
//
// MarginPct is a POINTER on purpose. Absent means "I didn't think about it" and takes the 10%
// default; an explicit 0 means "prove this got no worse at all", which no finite sample can do and
// which has to be refused by name. A plain float64 makes those two the same value, and the tool
// would then silently answer a question the operator never asked.
type Guardrail struct {
	Event     string `json:"event"`     // the metric event, e.g. "$exception" or "checkout"
	Direction string `json:"direction"` // not_worse (default) | not_better
	// MarginPct is δ: the RELATIVE degradation, in percent, this guardrail must rule out. 2 means
	// "I will accept up to a 2% relative loss; prove it is no worse than that".
	MarginPct *float64 `json:"margin_pct"`
}

// Margin resolves δ. The safe accessor for anything holding a guardrail that may not have been
// through ParseGuardrail.
func (g Guardrail) Margin() float64 {
	if g.MarginPct == nil {
		return DefaultGuardrailMarginPct
	}
	return *g.MarginPct
}

// Validate rejects a guardrail that cannot produce an honest verdict. One definition of a usable
// margin, called from the plan's own validation, so a plan and a report can never disagree about
// whether a guardrail is safe to lock.
//
// A nil margin is ACCEPTED: unstated means the 10% default, and ParseGuardrail fills it at the
// wire boundary. An explicit zero is refused, with the reason, because it is the single most
// common way a guardrail becomes decorative.
func (g Guardrail) Validate() error {
	if strings.TrimSpace(g.Event) == "" {
		return errors.New("a guardrail needs an event name — the metric it protects, e.g. $exception")
	}
	switch g.Direction {
	case GuardrailNotWorse, GuardrailNotBetter, "": // "" resolves to not_worse
	default:
		return fmt.Errorf("guardrail %q: direction must be %q or %q, got %q — use not_worse where up is "+
			"good (checkout) and not_better where up is bad ($exception)",
			g.Event, GuardrailNotWorse, GuardrailNotBetter, g.Direction)
	}
	if g.MarginPct == nil {
		return nil // unstated: DefaultGuardrailMarginPct applies
	}
	m := *g.MarginPct
	switch {
	case math.IsNaN(m) || math.IsInf(m, 0):
		return fmt.Errorf("guardrail %q: margin_pct is not a finite number (%v)", g.Event, m)
	case m == 0:
		return fmt.Errorf("guardrail %q: margin_pct is 0, which is not a guardrail — a non-inferiority "+
			"test at margin zero is a one-sided superiority test and can essentially never PASS at any "+
			"finite sample, so it would read INCONCLUSIVE forever and everyone would learn to ignore "+
			"it. State the loss you would actually accept (e.g. 2 for \"don't lose more than 2%%\"), or "+
			"leave it unset for the default %.0f%%", g.Event, DefaultGuardrailMarginPct)
	case m < 0:
		return fmt.Errorf("guardrail %q: margin_pct is a size, not a direction — use direction=%q with a "+
			"positive margin, got %v", g.Event, GuardrailNotWorse, m)
	case m >= 100:
		return fmt.Errorf("guardrail %q: margin_pct %v means you would accept losing the whole metric; "+
			"that is not a guardrail", g.Event, m)
	}
	return nil
}

// ParseGuardrail decodes a guardrail from JSON, distinguishing an ABSENT margin from an explicit
// zero one.
//
// The distinction cannot be made after unmarshalling into a float64 — both arrive as 0 — and it is
// the whole difference between "we chose 10% for you and said so" and "you asked for something
// impossible and we told you". Every API and MCP handler taking a user-authored guardrail should
// come through here rather than unmarshalling the struct directly.
func ParseGuardrail(raw []byte) (Guardrail, string, error) {
	var w struct {
		Event     string   `json:"event"`
		Direction string   `json:"direction"`
		MarginPct *float64 `json:"margin_pct"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Guardrail{}, "", fmt.Errorf("guardrail: %w", err)
	}
	g := Guardrail{Event: w.Event, Direction: w.Direction, MarginPct: w.MarginPct}
	if g.Direction == "" {
		g.Direction = GuardrailNotWorse
	}
	src := MarginDeclared
	if g.MarginPct == nil {
		m := DefaultGuardrailMarginPct
		g.MarginPct, src = &m, MarginDefault
	}
	if err := g.Validate(); err != nil {
		return Guardrail{}, "", err
	}
	return g, src, nil
}

// Guardrail verdicts. Strings, not a bool plus a "known" flag, because these travel into JSON and
// into a sentence a human reads, and PASS/FAIL/INCONCLUSIVE survives both without anybody having
// to remember which field qualifies which.
const (
	GuardrailPass         = "PASS"
	GuardrailFail         = "FAIL"
	GuardrailInconclusive = "INCONCLUSIVE"
)

// Where δ came from, echoed in the result. A margin nobody chose and a margin somebody chose lead
// to different decisions, and the reader cannot tell them apart from the number alone.
const (
	MarginDeclared = "declared"
	MarginDefault  = "default"
)

// GuardrailInput is everything one verdict is computed from. Every field that changes the answer
// is here and is echoed back out, because a guardrail result read without its α, its margin, its
// inference mode and its randomisation unit is four different claims wearing one word.
type GuardrailInput struct {
	Guardrail Guardrail
	// Variant being judged against control. Carried through so a multi-arm experiment's guardrail
	// block is readable without positional guessing.
	Variant string
	// Counts on the guardrail's OWN event: users who did it, out of users exposed, per arm.
	ConvTest, ExpTest int
	ConvCtrl, ExpCtrl int
	// Alpha is the ONE-SIDED level. 0 means DefaultAlpha.
	Alpha float64
	// Mode is ModeSequential (the default) or ModeFixed. NTune is N* for the confidence sequence,
	// needed in sequential mode so k(N) here is the SAME k the primary metric got — two different
	// inflations on one screen is two different experiments.
	Mode  string
	NTune int
	// KFactor overrides the computed k(N). Left at 0 it is derived with SeqInflation, which is the
	// path that cannot drift from the rest of the report. Anything below 1 is refused.
	KFactor float64
	// Unit is the randomisation unit the counts were aggregated to: UnitBucketID or
	// UnitDistinctID. Required, never defaulted — the two are different estimands, and a guardrail
	// that does not say which one it counted cannot be checked by anybody.
	Unit string
}

// GuardrailResult is one guardrail's verdict plus everything needed to audit it.
type GuardrailResult struct {
	Event        string  `json:"event"`
	Variant      string  `json:"variant,omitempty"`
	Direction    string  `json:"direction"`
	MarginPct    float64 `json:"margin_pct"`
	MarginSource string  `json:"margin_source"`
	Status       string  `json:"status"` // PASS | FAIL | INCONCLUSIVE

	Alpha    float64 `json:"alpha"`     // one-sided
	OneSided bool    `json:"one_sided"` // always true; stated so nobody assumes 1.96
	Mode     string  `json:"mode"`      // sequential | fixed
	NTune    int     `json:"n_tune,omitempty"`
	KFactor  float64 `json:"k_factor"` // the confidence-sequence inflation actually applied
	Z        float64 `json:"z"`        // the critical value used, AFTER k inflation
	Unit     string  `json:"unit"`     // randomisation unit

	ExposedTest   int `json:"exposed_test"`
	ConvertedTest int `json:"converted_test"`
	ExposedCtrl   int `json:"exposed_control"`
	ConvertedCtrl int `json:"converted_control"`

	// Bound is the one-sided interval on RELATIVE lift at the stated α, inflated by k. Present
	// only when a relative comparison is defined at all.
	Bound *Interval `json:"bound,omitempty"`
	// ThresholdPct is the number Bound is compared against: -δ for not_worse, +δ for not_better.
	ThresholdPct float64 `json:"threshold_pct"`
	// Reason names why no verdict beyond INCONCLUSIVE was possible. Empty on PASS/FAIL.
	Reason string `json:"reason,omitempty"`
	// Read is the sentence a person acts on.
	Read string `json:"read"`
}

// EvaluateGuardrail runs one guardrail as a one-sided non-inferiority test.
//
// The claim is: at level α, can the data rule out a relative degradation larger than δ? That is
// answered by ONE bound of the relative-lift interval computed at z_{1-α} — not z_{1-α/2}. Using
// the two-sided critical value here would widen the bound by 19% at α=0.05 (1.96 against 1.6449),
// making the guardrail less sensitive precisely where sensitivity is the entire point: a guardrail
// is a test you WANT to fire.
//
// FAIL uses the mirrored bound at the same α, so PASS and FAIL are each one-sided α-level claims
// and the gap between them is the honest INCONCLUSIVE region.
//
// In sequential mode the bound is inflated by the same k(N) the primary metric uses. k is defined
// against the two-sided critical value (SeqInflation divides by z_{1-α/2}), so applying it to the
// one-sided value keeps this bound conservative relative to the always-valid one-sided bound at
// the same α — the direction that can only make a PASS harder, never easier, which is the only
// direction an approximation is allowed to err in on a metric whose job is catching harm.
func EvaluateGuardrail(in GuardrailInput) (GuardrailResult, error) {
	g := in.Guardrail
	if g.Direction == "" {
		g.Direction = GuardrailNotWorse
	}
	// Resolve δ HERE rather than leaning on the caller, so the declared/default distinction is
	// recorded rather than inferred later from a number that no longer remembers where it came
	// from. A nil margin is "unstated" and gets the default; an explicit 0 is a mistake with a
	// name, and validateGuardrails is the one place that names it.
	src := MarginDeclared
	if g.MarginPct == nil {
		m := DefaultGuardrailMarginPct
		g.MarginPct, src = &m, MarginDefault
	}
	if err := g.Validate(); err != nil {
		return GuardrailResult{}, err
	}
	margin := g.Margin()

	alpha := in.Alpha
	if alpha == 0 {
		alpha = DefaultAlpha
	}
	if math.IsNaN(alpha) || alpha <= 0 || alpha >= 0.5 {
		return GuardrailResult{}, fmt.Errorf("guardrail %q: alpha must be in (0, 0.5), got %v", g.Event, alpha)
	}
	if in.Unit != UnitBucketID && in.Unit != UnitDistinctID {
		return GuardrailResult{}, fmt.Errorf("guardrail %q: the randomisation unit must be stated as %q "+
			"or %q, got %q. bucket_id is device-scoped and survives identify(), distinct_id is not — "+
			"counting one and labelling it the other is two different estimands",
			g.Event, UnitBucketID, UnitDistinctID, in.Unit)
	}
	mode := in.Mode
	if mode == "" {
		mode = ModeSequential // always-valid is the default: this number is read on a live dashboard
	}
	if mode != ModeSequential && mode != ModeFixed {
		return GuardrailResult{}, fmt.Errorf("guardrail %q: unknown mode %q (%s | %s)",
			g.Event, mode, ModeSequential, ModeFixed)
	}

	k, err := guardrailK(g.Event, mode, in.NTune, in.KFactor, in.ExpTest+in.ExpCtrl, alpha)
	if err != nil {
		return GuardrailResult{}, err
	}

	z := k * guardrailCriticalZ(alpha)
	res := GuardrailResult{
		Event: g.Event, Variant: in.Variant, Direction: g.Direction,
		MarginPct: margin, MarginSource: src,
		Alpha: alpha, OneSided: true, Mode: mode, NTune: in.NTune, KFactor: k, Z: z, Unit: in.Unit,
		ExposedTest: in.ExpTest, ConvertedTest: in.ConvTest,
		ExposedCtrl: in.ExpCtrl, ConvertedCtrl: in.ConvCtrl,
		Status: GuardrailInconclusive,
	}
	res.ThresholdPct = -margin
	if g.Direction == GuardrailNotBetter {
		res.ThresholdPct = margin
	}

	of := func(c, n int) string { return itoa(c) + " of " + itoa(n) }
	counts := " (" + of(in.ConvTest, in.ExpTest) + " against control's " + of(in.ConvCtrl, in.ExpCtrl) + ")"

	// Too small to say anything. Checked before the interval so the reason names the SAMPLE rather
	// than the arithmetic: an interval computed on 11 users is not wrong, it is useless, and
	// printing it invites someone to read the point estimate off it anyway.
	if in.ExpTest < minArmForStats || in.ExpCtrl < minArmForStats {
		res.Reason = "fewer than " + itoa(minArmForStats) + " users in an arm" + counts
		res.Read = "not enough data to rule out a " + trimFloat(margin) + "% " + harmWord(g.Direction) +
			" yet" + counts + ". This is not a pass."
		return res, nil
	}

	ci, why := liftInterval(in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl, z)
	if why != liftOK {
		// No relative comparison exists, so no non-inferiority claim exists either. Never PASS
		// here: the degenerate shapes — an arm that recorded nothing, a control indistinguishable
		// from zero — are exactly the ones where a missing verdict is most likely to be read as
		// "nothing wrong".
		res.Reason = guardrailBlockReason(why, in.ConvTest, in.ExpTest, in.ConvCtrl, in.ExpCtrl)
		res.Read = "no verdict on this guardrail: " + res.Reason + ". This is not a pass — read the raw counts."
		return res, nil
	}
	res.Bound = &ci

	if g.Direction == GuardrailNotWorse {
		switch {
		case ci.Lo > res.ThresholdPct:
			res.Status = GuardrailPass
			res.Read = "we can rule out a loss bigger than " + trimFloat(margin) + "%: at " +
				oneSidedLabel(alpha) + " the worst this arm plausibly is, is " + pct(ci.Lo) + counts + "."
		case ci.Hi < res.ThresholdPct:
			res.Status = GuardrailFail
			res.Read = "this arm is worse by more than the " + trimFloat(margin) + "% you said you " +
				"would accept — even the best it plausibly is, is " + pct(ci.Hi) + counts + ". Do not ship."
		default:
			res.Read = "not enough data to rule out a " + trimFloat(margin) + "% loss yet — the range " +
				"still spans it (" + pct(ci.Lo) + " to " + pct(ci.Hi) + ")" + counts + ". This is not a pass."
		}
		return res, nil
	}

	// GuardrailNotBetter: the harm is a RISE, so the roles of the two bounds swap.
	switch {
	case ci.Hi < res.ThresholdPct:
		res.Status = GuardrailPass
		res.Read = "we can rule out a rise bigger than " + trimFloat(margin) + "%: at " +
			oneSidedLabel(alpha) + " the highest this arm plausibly is, is " + pct(ci.Hi) + counts + "."
	case ci.Lo > res.ThresholdPct:
		res.Status = GuardrailFail
		res.Read = "this arm is up by more than the " + trimFloat(margin) + "% rise you said you " +
			"would accept — even the lowest it plausibly is, is " + pct(ci.Lo) + counts + ". Do not ship."
	default:
		res.Read = "not enough data to rule out a " + trimFloat(margin) + "% rise yet — the range " +
			"still spans it (" + pct(ci.Lo) + " to " + pct(ci.Hi) + ")" + counts + ". This is not a pass."
	}
	return res, nil
}

// EvaluateGuardrails runs a set and fails the whole set on the first invalid guardrail rather than
// dropping the bad one: a shorter list of results looks exactly like a set where everything passed.
func EvaluateGuardrails(ins []GuardrailInput) ([]GuardrailResult, error) {
	out := make([]GuardrailResult, 0, len(ins))
	for _, in := range ins {
		r, err := EvaluateGuardrail(in)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// GuardrailsBlocking returns the guardrails that are not a PASS, which is the list a "is this safe
// to ship" answer is built from. INCONCLUSIVE is in it deliberately: an unanswered guardrail blocks
// a decision exactly like a failed one does, and only the sentence differs.
func GuardrailsBlocking(rs []GuardrailResult) []GuardrailResult {
	var out []GuardrailResult
	for _, r := range rs {
		if r.Status != GuardrailPass {
			out = append(out, r)
		}
	}
	return out
}

// guardrailK resolves the confidence-sequence inflation.
//
// It is DERIVED by default rather than passed in, so the guardrail and the primary metric are
// inflated by the same k on the same screen. A caller may override it (a composed analysis may
// already carry one), but never below 1: k(N) > 1 for every N, and a k under 1 would NARROW the
// interval and manufacture a PASS out of arithmetic.
func guardrailK(event, mode string, nTune int, override float64, n int, alpha float64) (float64, error) {
	if math.IsNaN(override) || math.IsInf(override, 0) {
		return 0, fmt.Errorf("guardrail %q: k factor is not finite", event)
	}
	if override != 0 {
		if override < 1 {
			return 0, fmt.Errorf("guardrail %q: k factor %v is below 1, which would narrow the interval "+
				"and manufacture a PASS; k(N) from the confidence sequence is always > 1", event, override)
		}
		return override, nil
	}
	if mode == ModeFixed {
		return 1, nil // fixed horizon applies no inflation, by definition
	}
	if nTune <= 0 {
		// Silently proceeding at k=1 would print a fixed-horizon interval under an always-valid
		// label — the peeking bug this mode exists to prevent, wearing its cure's name.
		return 0, errors.New("guardrail " + event + ": sequential mode needs n_tune to compute the k(N) " +
			"inflation from the confidence sequence; none was supplied, and running without it would " +
			"label a fixed-horizon bound always-valid")
	}
	if n <= 0 {
		return 0, errors.New("guardrail " + event + ": no exposed users, so k(N) is undefined")
	}
	k := SeqInflation(n, alpha, nTune)
	if math.IsNaN(k) || math.IsInf(k, 0) || k < 1 {
		return 0, fmt.Errorf("guardrail %q: computed k(N) = %v, which is not a usable inflation", event, k)
	}
	return k, nil
}

func harmWord(dir string) string {
	if dir == GuardrailNotBetter {
		return "rise"
	}
	return "loss"
}

func oneSidedLabel(alpha float64) string { return trimFloat(100*(1-alpha)) + "% one-sided" }

// guardrailBlockReason turns the interval's own block reason into a guardrail sentence. It
// deliberately reuses liftBlock rather than re-deriving the degenerate cases, so a new block reason
// added to liftInterval cannot silently become a PASS here.
func guardrailBlockReason(why liftBlock, cTest, nTest, cCtrl, nCtrl int) string {
	of := func(c, n int) string { return itoa(c) + " of " + itoa(n) }
	switch why {
	case liftNoSample:
		return "one arm has no exposures at all, so there is nothing to compare"
	case liftNoConversions:
		return "neither arm has recorded this event yet (" + of(cTest, nTest) + " against control's " +
			of(cCtrl, nCtrl) + ")"
	case liftTestZero:
		return "this arm recorded none (" + of(cTest, nTest) + " against control's " + of(cCtrl, nCtrl) +
			"), so a relative margin is undefined against zero"
	case liftCtrlZero:
		return "control recorded none (" + of(cCtrl, nCtrl) + "), so there is no rate to take a percentage of"
	case liftSaturated:
		return "an arm recorded this event for every user it was shown, so the relative interval is undefined"
	case liftCtrlNearZero:
		return "the control rate is too close to zero for a relative margin to mean anything"
	}
	return "the relative interval could not be computed"
}

// guardrailCriticalZ is z_{1-alpha}: the ONE-SIDED normal critical value.
//
// Computed rather than tabled because α is configurable, and a hardcoded 1.6449 is the kind of
// constant that survives a change to α and quietly reports the wrong level. It routes through
// sequential.go's inverse-normal on purpose: one numeric path for every critical value in the
// package means a guardrail and the interval above it can never disagree in the last decimal.
func guardrailCriticalZ(alpha float64) float64 { return seqNormInv(1 - alpha) }
