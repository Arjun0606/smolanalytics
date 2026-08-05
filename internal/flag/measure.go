package flag

import (
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Exposure/response property keys, mirroring PostHog's $feature_flag_called convention. The "$"
// prefix means the tracking-plan drift gate treats these as system events, not unplanned ones.
const (
	ExposureEvent  = "$feature_flag_called"
	PropFlag       = "$feature_flag"
	PropVariant    = "$feature_flag_response"
	minArmForStats = 30 // below this a variant's rate is too noisy to call significant
)

// VariantResult is one arm of a measured flag's A/B read.
//
// Every rate ships with the raw numerator and denominator that produced it and a 95% interval
// around it. A bare percentage asks to be trusted; 43 of 512, somewhere between 6.3% and 11.1%,
// can be checked. That is the difference this product claims to sell.
type VariantResult struct {
	Key       string `json:"key"`
	Exposed   int    `json:"exposed"`
	Converted int    `json:"converted"`
	// RatePct is the point estimate; RateCI is where the true rate plausibly sits. Wilson
	// interval, so it never reports a bound below 0% or above 100% on a small arm.
	RatePct float64  `json:"rate_pct"`
	RateCI  Interval `json:"rate_ci"`
	// DeltaPct is relative lift vs control. DeltaCI is its interval, present only when the
	// control rate is far enough from zero for a ratio to be meaningful — otherwise a handful of
	// conversions prints "+4000%" and someone ships on it.
	//
	// DeltaDefined says whether DeltaPct is a real number. It has to exist because 0 was doing
	// double duty: a control at 0 of 200 against a test arm at 60 of 200 — an infinite lift, the
	// single biggest win the tool can find — left DeltaPct at its zero value, so the row rendered
	// "0" (neutral, "no change") next to "significant at 95% against control". Undefined and
	// genuinely flat were indistinguishable to every reader and every consumer. False on the
	// control row too: an arm has no lift against itself, and 0 there is a convention, not a
	// measurement.
	DeltaPct     float64   `json:"delta_pct"`
	DeltaDefined bool      `json:"delta_defined"`
	DeltaCI      *Interval `json:"delta_ci,omitempty"`
	PValue       float64   `json:"p_value,omitempty"`
	Significant  bool      `json:"significant"`  // 95% two-proportion z-test vs control
	SmallSample  bool      `json:"small_sample"` // too few exposed to trust the rate
	// Read is the sentence a person should act on. "Significant" invites shipping; "the range
	// still spans zero" invites waiting, which is usually the right call and one a boolean
	// never prompts.
	Read string `json:"read,omitempty"`
	// Seq is the always-valid machinery behind DeltaCI: the inflation factor K, the critical
	// value it used, and the always-valid p-value. Present on every compared arm so the width
	// of the interval can be traced to the method rather than taken on faith.
	Seq *SeqStats `json:"sequential,omitempty"`
}

// Report is the A/B read for one measured flag: for each variant, how many exposed users
// converted on the goal event AFTER their first exposure, and whether the lift over the control
// arm is statistically significant. Pure + deterministic (same events → same report), so it is
// pinnable MCP==API by an agreement test, the same contract as every other report.
type Report struct {
	Flag     string          `json:"flag"`
	Goal     string          `json:"goal"`
	Days     int             `json:"days"`
	Control  string          `json:"control"`
	Variants []VariantResult `json:"variants"`
	Note     string          `json:"note"`
	// The DESIGN, echoed. Every number above is conditional on these, and a reader who cannot
	// see them cannot check the result — "significant" means nothing without the alpha it was
	// tested at or the mode it was read under. Stated rather than assumed, on every response.
	Mode        string  `json:"mode"` // sequential | fixed — what actually drew the intervals
	ModeReason  string  `json:"mode_reason,omitempty"`
	Alpha       float64 `json:"alpha"`
	NTune       int     `json:"n_tune"`
	NTuneSource string  `json:"n_tune_source"`
	NPlanned    int     `json:"n_planned,omitempty"`
	// Unit names the randomisation unit the arms were counted over. bucket_id is device-scoped
	// and survives identify(); distinct_id changes at login. Counting one and calling it the
	// other is two different estimands wearing one label.
	Unit string `json:"randomisation_unit"`
	// Planned is nil when the flag carries no pre-registered plan, and the note says so. A
	// missing plan is a fact about the experiment, not a detail to paper over with defaults.
	Planned bool `json:"pre_registered"`
	// From/To are the resolved window, echoed so a re-run is reproducible without re-deriving
	// what "30 days" meant at the moment this ran.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Measure computes the report from raw events. An exposure is a $feature_flag_called event tagging
// the user's variant for this flag; a conversion is the user doing `goal` at or after their first
// exposure (so we never credit behavior that predates the experiment). Only events within the last
// `days` (0 = all) are considered.
//
// Deprecated in favour of MeasureRange: `days` is resolved against time.Now() INSIDE this
// function, so the same events return a different report as the clock moves. Kept so existing
// callers keep compiling; every one of them should resolve its own window and pass instants.
func Measure(evs []event.Event, flagKey, goal string, days int) Report {
	var from time.Time
	if days > 0 {
		from = time.Now().UTC().AddDate(0, 0, -days)
	}
	rep := MeasureRange(evs, Flag{Key: flagKey}, goal, from, time.Time{})
	rep.Days = days
	return rep
}

// MeasureRange is Measure over an explicit half-open window [from, to), told which flag it is
// measuring rather than only its key.
//
// Four defects made this necessary, all of them producing a confidently wrong answer:
//
//  1. THE CLOCK. `days` was turned into a cutoff with time.Now() inside the pure computation, so
//     the same input produced different output every time it ran. On a product whose claim is
//     "recomputed from raw events, byte-identical answers", the experiment report was the one
//     thing that could not be reproduced.
//
//  2. CONTROL WAS ALPHABETICAL. `sort.Strings(variants)[0]` — so an experiment with arms named
//     {control, a_new} elected `a_new` as the control and INVERTED THE SIGN OF EVERY LIFT. The
//     winning arm reads as the losing one. Control is now the flag's declared first variant.
//
//  3. A DEAD ARM VANISHED. byVariant was built purely from observed exposures, so an arm nobody
//     was ever exposed to simply was not in the report. That is the single most important
//     failure to surface — it means the treatment code never ran — and it rendered as a clean
//     one-arm experiment with nothing wrong.
//
//  4. A GOAL NAMED LIKE THE EXPOSURE EVENT WAS UNREACHABLE. `case ExposureEvent:` and
//     `case goal:` shared one switch, so measuring a goal of "$feature_flag_called" silently
//     counted zero conversions forever.
//
//  5. REPEAT CONVERTERS WERE SCORED ON THEIR FIRST GOAL EVENT EVER. Only the MINIMUM goal
//     timestamp per user was kept, then compared to their exposure — so a user who purchased
//     once before the experiment started and again after being exposed landed in the
//     denominator and never in the numerator, forever. On any repeatable goal (purchase,
//     $pageview, session_start) that is every returning customer, and it understates both arms
//     unequally whenever exposure timing differs between them. Measured on the three-event
//     case {goal T0, exposure T1, goal T2}: exposed=1, converted=0.
//
// `to` zero means "no upper bound"; `from` zero means "all history".
func MeasureRange(evs []event.Event, f Flag, goal string, from, to time.Time) Report {
	type exposure struct {
		variant string
		at      time.Time
	}
	firstExp := map[string]exposure{}
	// The LAST goal per user, not the first. A conversion is "did the goal at any point at or
	// after first exposure", and since exposure is the earliest one, that question is answered
	// exactly by max(goal) >= firstExp: if the latest goal predates exposure then none of them
	// followed it. Keeping the minimum answered a different question — "was this user's very
	// first goal ever after exposure" — which permanently disqualifies every repeat converter.
	// One scalar per user either way, so this costs nothing over a per-user timestamp list.
	lastGoal := map[string]time.Time{}

	inWindow := func(t time.Time) bool {
		if !from.IsZero() && t.Before(from) {
			return false
		}
		// half-open [from, to) — matches contracts_test.go WINDOW-1 and stops an event on the
		// boundary being counted by two adjacent windows
		return to.IsZero() || t.Before(to)
	}

	// The randomisation unit. bucket_id is what the SDK actually buckets on and is deliberately
	// device-scoped so it survives identify(); distinct_id changes at login. Analysing by
	// distinct_id while assigning by bucket_id splits one person's behaviour in half at the
	// moment they sign in — the arm keeps their pre-login events and loses their post-login
	// ones — which is a different estimand from the one the experiment randomised.
	//
	// unitOf falls back to distinct_id per event, so an instance whose SDK predates bucket_id
	// still reports, and unitUsed names whichever unit actually carried the exposures.
	unitUsed := UnitDistinctID
	unitOf := func(e event.Event) string {
		if b, _ := e.Properties[PropBucketID].(string); b != "" {
			return b
		}
		return e.DistinctID
	}
	// bucketOf maps a distinct_id to its bucket key so goal events — which do NOT carry
	// bucket_id — can be attributed to the same unit their exposure was counted under.
	bucketOf := map[string]string{}
	for _, e := range evs {
		if e.Name != ExposureEvent || !inWindow(e.Timestamp) {
			continue
		}
		if fk, _ := e.Properties[PropFlag].(string); fk != f.Key {
			continue
		}
		if b, _ := e.Properties[PropBucketID].(string); b != "" {
			bucketOf[e.DistinctID] = b
			unitUsed = UnitBucketID
		}
	}
	unitKey := func(e event.Event) string {
		if b, ok := bucketOf[e.DistinctID]; ok {
			return b
		}
		return unitOf(e)
	}

	for _, e := range evs {
		if !inWindow(e.Timestamp) {
			continue
		}
		u := unitKey(e)
		// deliberately two ifs, not a switch: a goal may legitimately BE the exposure event
		if e.Name == ExposureEvent {
			if fk, _ := e.Properties[PropFlag].(string); fk == f.Key {
				if cur, ok := firstExp[u]; !ok || e.Timestamp.Before(cur.at) {
					v, _ := e.Properties[PropVariant].(string)
					firstExp[u] = exposure{variant: v, at: e.Timestamp}
				}
			}
		}
		if e.Name == goal {
			if cur, ok := lastGoal[u]; !ok || e.Timestamp.After(cur) {
				lastGoal[u] = e.Timestamp
			}
		}
	}

	type tally struct{ exposed, converted int }
	byVariant := map[string]*tally{}
	// Seed from the DECLARED arms so an arm with no exposures reports a zero rather than
	// disappearing. Declaration order is preserved for the control rule below.
	declared := make([]string, 0, len(f.Variants))
	for _, v := range f.Variants {
		if v.Key == "" || byVariant[v.Key] != nil {
			continue
		}
		byVariant[v.Key] = &tally{}
		declared = append(declared, v.Key)
	}
	for id, ex := range firstExp {
		t := byVariant[ex.variant]
		if t == nil {
			t = &tally{}
			byVariant[ex.variant] = t
		}
		t.exposed++
		if g, ok := lastGoal[id]; ok && !g.Before(ex.at) {
			t.converted++
		}
	}

	// Declared arms first, in the order the operator wrote them; anything observed but not
	// declared (a stale variant from an earlier version of the flag) follows, sorted, so the
	// output stays deterministic either way.
	seen := map[string]bool{}
	variants := make([]string, 0, len(byVariant))
	for _, v := range declared {
		variants = append(variants, v)
		seen[v] = true
	}
	extra := make([]string, 0, len(byVariant))
	for v := range byVariant {
		if !seen[v] {
			extra = append(extra, v)
		}
	}
	sort.Strings(extra)
	variants = append(variants, extra...)

	control := ""
	if len(variants) > 0 {
		control = variants[0] // the declared first arm, NOT the alphabetically first
	}
	rep := Report{Flag: f.Key, Goal: goal, Control: control, Unit: unitUsed}
	if !from.IsZero() {
		rep.From = from.UTC().Format(time.RFC3339)
	}
	if !to.IsZero() {
		rep.To = to.UTC().Format(time.RFC3339)
	}
	// The pre-registered plan drives the inference. Without one, documented defaults apply and
	// the report says `pre_registered: false` — a design chosen by omission is still a design,
	// and the reader is entitled to know which one they got.
	var plan Experiment
	if f.Experiment != nil {
		plan = f.Experiment.Resolve()
		rep.Planned = true
	} else {
		plan = Experiment{}.Resolve()
	}
	seqPar := SeqParamsFor(plan.Mode, plan.Alpha, plan.NPlanned)
	// Declared for now; overwritten below with the mode that actually drew the intervals. A
	// fixed-horizon test read before its pre-registered N falls back to the always-valid one,
	// and echoing the DECLARED mode there would tell the reader they are looking at a fixed
	// interval while showing them a sequential one.
	rep.Mode, rep.Alpha = seqPar.Mode, seqPar.Alpha
	rep.NTune, rep.NTuneSource, rep.NPlanned = seqPar.NTune, seqPar.NTuneSource, plan.NPlanned
	var ctrl *tally
	if control != "" {
		ctrl = byVariant[control]
	}
	seqEchoed := false
	for _, v := range variants {
		t := byVariant[v]
		rate := 0.0
		if t.exposed > 0 {
			rate = 100 * float64(t.converted) / float64(t.exposed)
		}
		vr := VariantResult{
			Key:         v,
			Exposed:     t.exposed,
			Converted:   t.converted,
			RatePct:     round1(rate),
			RateCI:      wilson(t.converted, t.exposed, z95),
			SmallSample: t.exposed < minArmForStats,
		}
		if ctrl != nil && v != control && ctrl.exposed > 0 {
			ctrlRate := float64(ctrl.converted) / float64(ctrl.exposed)
			// DeltaDefined, not "is it non-zero". A control at 0 of 200 against a test arm at
			// 60 of 200 is an INFINITE lift — the biggest win the tool can find — and it left
			// DeltaPct at its zero value, so the row rendered "0" (no change) beside
			// "significant at 95%". Undefined and genuinely flat have to be distinguishable.
			if ctrlRate > 0 {
				vr.DeltaPct = round1(100 * ((rate/100 - ctrlRate) / ctrlRate))
				vr.DeltaDefined = true
			}
			// SEQUENTIAL, not fixed-horizon. significant() is a 1.96 z-test, and this dashboard
			// is built to be checked daily — under repeated peeking a fixed-horizon test's true
			// false-positive rate is several times the 5% it advertises. SeqCompute returns the
			// inflation K over the fixed half-width, and LiftZ is that same inflation expressed
			// as a critical value, so the RELATIVE interval widens by exactly the factor the
			// absolute one does. Two intervals on one row disagreeing about how conservative
			// they are would be worse than either alone.
			pT := float64(t.converted) / float64(t.exposed)
			pC := float64(ctrl.converted) / float64(ctrl.exposed)
			seUn := math.Sqrt(pT*(1-pT)/float64(t.exposed) + pC*(1-pC)/float64(ctrl.exposed))
			seq := SeqCompute(pT-pC, seUn, t.exposed+ctrl.exposed, plan.NPlanned, seqPar)
			vr.Seq = &seq

			vr.PValue = round4(twoProportionP(t.converted, t.exposed, ctrl.converted, ctrl.exposed))
			if seq.Mode == SeqModeSequential {
				// the always-valid p-value is the one that survives having been looked at
				vr.PValue = round4(seq.AlwaysValidP)
			}
			// No `PValue > 0` guard here. round4 sends an overwhelming result to exactly 0.0000,
			// and treating that as "unset" threw away the single most significant outcome the
			// tool can produce — 60 of 200 against 0 of 200 read as not significant.
			vr.Significant = vr.PValue < seqPar.Alpha &&
				t.exposed >= minArmForStats && ctrl.exposed >= minArmForStats
			enough := t.exposed >= minArmForStats && ctrl.exposed >= minArmForStats
			ci, why := liftInterval(t.converted, t.exposed, ctrl.converted, ctrl.exposed, seq.LiftZ)
			if why == liftOK {
				c := ci
				vr.DeltaCI = &c
			}
			// the raw counts travel into readLift so the sentence can name WHICH arm converted
			// nobody, instead of the old single line that blamed the control every time
			vr.Read = readLift(ci, why, vr.PValue, enough, t.converted, t.exposed, ctrl.converted, ctrl.exposed)
		}
		if vr.Seq != nil && !seqEchoed {
			rep.Mode, rep.ModeReason = vr.Seq.Mode, vr.Seq.ModeReason
			seqEchoed = true
		}
		rep.Variants = append(rep.Variants, vr)
	}
	// A declared arm with no exposures at all, while another arm has a real sample, is the
	// loudest thing this report can say: the code path that reads the flag has probably never
	// run in that branch. It used to say nothing, because the arm was simply absent.
	dead, live := "", 0
	for _, v := range rep.Variants {
		if v.Exposed == 0 && dead == "" {
			dead = v.Key
		}
		if v.Exposed >= minArmForStats {
			live++
		}
	}

	switch {
	case dead != "" && live > 0:
		rep.Note = "arm " + dead + " has zero exposures — the code path that reads this flag has " +
			"probably never run for it. Nothing below is a result until that is fixed."
	case len(rep.Variants) == 0:
		rep.Note = "no exposures yet. mark the flag measured and let the SDK log $feature_flag_called, then check back"
	case len(rep.Variants) == 1:
		rep.Note = "only one variant has exposures so far; there is nothing to compare it against yet"
	default:
		rep.Note = "conversion = the goal event at or after a user's first exposure. Every rate carries its raw counts and a 95% Wilson interval; lift carries a 95% interval too, omitted when the control rate is too near zero for a ratio to mean anything. Check the split with experiment_health before acting on any of this — a sample ratio mismatch makes every number here unreadable."
	}
	return rep
}

// significant is a two-proportion z-test at ~95% (|z| >= 1.96), guarded so tiny arms never
// read as significant.
func significant(c1, n1, c2, n2 int) bool {
	if n1 < minArmForStats || n2 < minArmForStats {
		return false
	}
	p1 := float64(c1) / float64(n1)
	p2 := float64(c2) / float64(n2)
	pooled := float64(c1+c2) / float64(n1+n2)
	se := math.Sqrt(pooled * (1 - pooled) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return false
	}
	return math.Abs(p1-p2)/se >= 1.96
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }
