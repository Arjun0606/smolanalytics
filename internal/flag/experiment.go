package flag

// The pre-registered analysis plan. An experiment is not "a flag with a goal typed into the read
// screen" — it is a decision rule written down BEFORE the data arrives: which arm is control,
// which event counts as a win, how wide the interval is allowed to be, what degradation is
// acceptable on the metrics you refuse to trade away, and when you expect to decide.
//
// Everything here exists because the alternative is a number chosen after seeing the data. The
// goal event used to be retyped on every read, so "which metric moved" was answered by whoever
// typed last; the control arm was inferred alphabetically, which inverts the sign of every lift on
// arms named {control, a_new}; alpha was a constant nobody could see. None of that is a
// statistical subtlety — each one is a way to report a result you picked afterwards and believe it.
//
// The plan is hashed. A locked plan cannot be edited: it can be stopped, or amended with a reason
// that ships inside the report, so a reader always sees "this plan was changed on day 4" instead
// of a clean scorecard that was quietly re-aimed.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Inference mode. sequential is the DEFAULT: this dashboard invites daily checking, and a
// fixed-horizon p-value read every day has a true false-positive rate several times its nominal
// one. fixed exists for someone who genuinely commits to a single look at a planned sample size,
// and refuses to render its interval before that sample is reached.
const (
	ModeSequential = "sequential"
	ModeFixed      = "fixed"
)

// Randomisation unit. bucket_id is a device key written once per browser and never rewritten by
// identify() or reset(), so assignment survives login; distinct_id is the fallback for clients too
// old to send one.
//
// The report STATES which one it used, because they are two different estimands and mixing them is
// not a rounding error — it is measuring a different experiment. A user who logs in mid-test is one
// unit under bucket_id and two under distinct_id, and the second of those two carries only the
// post-login half of their behaviour.
const (
	UnitBucketID   = "bucket_id"
	UnitDistinctID = "distinct_id"
)

// How n_tune was chosen, echoed in the payload. n_tune is the sample size at which the confidence
// sequence is narrowest, so a wrong one silently widens every interval the user will ever see —
// which reads as "the tool is unsure" rather than "the tool guessed 5000".
const (
	NTuneFromPlanned  = "n_planned" // the power calculator's answer for this experiment's own MDE
	NTuneFromDefault  = "default"   // no baseline/MDE/power declared, so a generic 5000
	NTuneFromExplicit = "explicit"  // the operator typed a number
)

const (
	DefaultAlpha = 0.05
	DefaultPower = 0.80
	// DefaultNTune is the fallback only. When the experiment declares baseline + MDE + power, the
	// power calculator's n_planned is the right N* by construction, and the payload says so.
	DefaultNTune = 5000
	// DefaultCUPEDLookbackDays is the per-user pre-exposure window for the covariate.
	DefaultCUPEDLookbackDays = 7
	// CurrentHashVersion pins the bucketing construction. It exists so version 2 can never
	// retroactively re-randomise a running experiment — the defect LaunchDarkly shipped and could
	// not fix, because once users are bucketed the hash is load-bearing history.
	CurrentHashVersion = 1
)

// CUPEDCfg configures variance reduction against each user's own pre-exposure behaviour. Covariate
// defaults to the goal event; LookbackDays is measured per user, backwards from THEIR first
// exposure, not from a fixed calendar date — a fixed date mixes post-exposure behaviour into the
// covariate for anyone who joined late, which biases the adjustment instead of only sharpening it.
type CUPEDCfg struct {
	Enabled      bool   `json:"enabled"`
	LookbackDays int    `json:"lookback_days"`
	Covariate    string `json:"covariate,omitempty"`
}

// Amendment is a recorded change to a locked plan. It ships INSIDE the report payload, not only in
// the audit log: an amendment nobody sees is the same as no lock at all.
type Amendment struct {
	At       time.Time `json:"at"`
	Reason   string    `json:"reason"`
	Changed  []string  `json:"changed"`
	FromHash string    `json:"from_plan_hash"`
	ToHash   string    `json:"to_plan_hash"`
}

// Experiment is the plan. Attach it to a Flag (as *Experiment) and Store.Save enforces the lock.
//
// Started/Locked/PlanHash are set by Start, never by the caller. Stopped, Amendments, Locked and
// PlanHash are deliberately OUTSIDE the hash — see canonicalPlan.
type Experiment struct {
	Goal              string      `json:"goal"`                   // primary metric event
	Control           string      `json:"control"`                // declared, never inferred
	Guardrails        []Guardrail `json:"guardrails,omitempty"`   // one-sided, never δ=0
	Secondary         []string    `json:"secondary,omitempty"`    // excluded from the BH family
	Mode              string      `json:"mode"`                   // sequential | fixed
	Alpha             float64     `json:"alpha"`                  // 0.05
	Power             float64     `json:"power"`                  // 0.80
	NTune             int         `json:"n_tune"`                 // N*, where the sequence is narrowest
	NTuneSource       string      `json:"n_tune_source"`          // n_planned | default | explicit
	BaselinePct       float64     `json:"baseline_pct,omitempty"` // the control rate the plan assumed
	MDEPct            float64     `json:"mde_pct,omitempty"`      // relative, e.g. 5 = "+5% or better"
	NPlanned          int         `json:"n_planned,omitempty"`    // total exposed units, from power.go
	RandomisationUnit string      `json:"randomisation_unit"`     // bucket_id | distinct_id
	CUPED             *CUPEDCfg   `json:"cuped,omitempty"`
	Layer             string      `json:"layer,omitempty"` // immutable once anyone is exposed
	// Slice is this experiment's claim on its layer's [0,1) space. Empty means unlayered, which
	// admits everyone — so a flag that never opts in is unaffected by any of this.
	Slice LayerSlice `json:"slice,omitempty"`
	// Holdout names a population kept out of experiments entirely, and HoldoutPct is its share
	// (0..100). Its purpose is the question nobody can otherwise answer: after six months of
	// individually-significant wins, did any of it add up? Held-out users are the control for
	// everything you shipped.
	Holdout     string      `json:"holdout,omitempty"`
	HoldoutPct  float64     `json:"holdout_pct,omitempty"`
	HashVersion int         `json:"hash_version"`
	Started     time.Time   `json:"started"`
	Stopped     time.Time   `json:"stopped,omitempty"`
	PlanHash    string      `json:"plan_hash"`
	Locked      bool        `json:"locked"`
	Amendments  []Amendment `json:"amendments,omitempty"`
}

// Resolve returns the plan with every default filled in. This is also the ECHO form: what gets
// rendered in the payload, so a reader never has to know which of these numbers we chose for them.
// Nothing downstream should reason about a zero-valued Mode, Alpha or margin.
func (e Experiment) Resolve() Experiment {
	r := e
	if r.Mode == "" {
		r.Mode = ModeSequential
	}
	if r.RandomisationUnit == "" {
		r.RandomisationUnit = UnitBucketID
	}
	if r.Alpha == 0 {
		r.Alpha = DefaultAlpha
	}
	if r.HashVersion == 0 {
		r.HashVersion = CurrentHashVersion
	}

	// n_tune BEFORE power is defaulted: "declares power" has to mean the operator declared it. If
	// defaulting ran first, every plan would look like it had a design, and a plan with no baseline
	// and no MDE would claim its n_tune came from a power calculation that never happened.
	//
	// NPlanned > 0 is required too. Baseline + MDE + power with no n_planned means the calculator
	// was never run, and inventing N* from the parts here would be a second, disagreeing
	// implementation of power.go — two answers to one question, which is the thing this product
	// exists to not do.
	planned := e.BaselinePct > 0 && e.MDEPct > 0 && e.Power > 0 && e.NPlanned > 0
	switch {
	case e.NTuneSource != "":
		// A plan that has ALREADY been resolved carries its provenance, and re-deriving it here
		// would overwrite the truth with a guess. Resolve is not idempotent without this: the
		// first pass fills NTune from the default and stamps "default", and the second pass sees
		// NTune > 0 and restamps it "explicit" — so simply loading a stored plan and saving it
		// back reported n_tune_source as changed, and the lock refused an edit nobody made.
		// It also made the field untestable in the other direction: a real change to
		// n_tune_source was erased by the same overwrite before the diff could see it.
		if r.NTune == 0 {
			r.NTune = DefaultNTune
		}
	case e.NTune > 0:
		r.NTuneSource = NTuneFromExplicit
	case planned:
		r.NTune, r.NTuneSource = e.NPlanned, NTuneFromPlanned
	default:
		r.NTune, r.NTuneSource = DefaultNTune, NTuneFromDefault
	}

	if r.Power == 0 {
		r.Power = DefaultPower
	}
	// Guardrail margins are filled here, and that is only safe because Guardrail.MarginPct is a
	// *float64. An ABSENT margin ("I didn't think about it") and an explicit δ=0 ("prove this got
	// no worse at all") mean opposite things — the first takes the 10% default, the second is
	// impossible at any finite sample and is refused by name in validate. A plain float64 makes
	// them the same value; the pointer is what lets this function state the margin in the echo
	// without silently answering a question nobody asked.
	//
	// Every margin is copied into a FRESH pointer, not just the slice. Sharing the caller's
	// *float64 means anyone still holding that guardrail can change the margin of a locked plan
	// after the fact, and the plan would then no longer hash to the plan_hash on its own receipt.
	if len(e.Guardrails) > 0 {
		gs := make([]Guardrail, len(e.Guardrails))
		for i, g := range e.Guardrails {
			m := g.Margin() // nil → DefaultGuardrailMarginPct; an explicit 0 stays 0 and is refused
			g.MarginPct = &m
			if g.Direction == "" {
				g.Direction = GuardrailNotWorse // the wire default, stated rather than implied
			}
			gs[i] = g
		}
		r.Guardrails = gs
	}
	if r.CUPED != nil {
		c := *r.CUPED
		if c.LookbackDays == 0 {
			c.LookbackDays = DefaultCUPEDLookbackDays
		}
		if c.Covariate == "" {
			c.Covariate = r.Goal
		}
		r.CUPED = &c
	}
	if len(e.Secondary) > 0 {
		r.Secondary = append([]string(nil), e.Secondary...)
	}
	return r
}

// Validate checks a plan is coherent enough to be locked. variants may be empty (a boolean flag or
// a caller that has none to hand); when present, the declared control must be one of them.
func (e Experiment) Validate(variants []Variant) error {
	return e.Resolve().validate(variants)
}

func (e Experiment) validate(variants []Variant) error {
	if strings.TrimSpace(e.Goal) == "" {
		return fmt.Errorf("an experiment needs a goal event declared up front — that is the whole point of pre-registering it. Pass goal (e.g. \"purchase\") when you start it")
	}
	if strings.TrimSpace(e.Control) == "" {
		return fmt.Errorf("an experiment needs a declared control arm. Inferring it alphabetically picks a_new over control and inverts the sign of every lift")
	}
	if len(variants) > 0 {
		keys := make([]string, 0, len(variants))
		found := false
		for _, v := range variants {
			keys = append(keys, v.Key)
			if v.Key == e.Control {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("control %q is not one of this flag's arms (%s) — every lift would be measured against an arm nobody is in", e.Control, strings.Join(keys, ", "))
		}
	}
	if e.Mode != ModeSequential && e.Mode != ModeFixed {
		return fmt.Errorf("mode must be %q or %q, got %q", ModeSequential, ModeFixed, e.Mode)
	}
	if e.RandomisationUnit != UnitBucketID && e.RandomisationUnit != UnitDistinctID {
		return fmt.Errorf("randomisation_unit must be %q or %q, got %q — the report states which unit it used because they are two different estimands", UnitBucketID, UnitDistinctID, e.RandomisationUnit)
	}
	for name, f := range map[string]float64{"alpha": e.Alpha, "power": e.Power, "mde_pct": e.MDEPct, "baseline_pct": e.BaselinePct} {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%s is not a finite number (%v) — it would hash and render as garbage", name, f)
		}
	}
	if e.Alpha <= 0 || e.Alpha > 0.5 {
		return fmt.Errorf("alpha must be in (0, 0.5], got %v", e.Alpha)
	}
	if e.Power <= 0 || e.Power >= 1 {
		return fmt.Errorf("power must be in (0, 1), got %v", e.Power)
	}
	if e.MDEPct < 0 {
		return fmt.Errorf("mde_pct is a relative effect size and cannot be negative, got %v", e.MDEPct)
	}
	if e.BaselinePct < 0 || e.BaselinePct > 100 {
		return fmt.Errorf("baseline_pct is a conversion rate in percent and must be 0..100, got %v", e.BaselinePct)
	}
	if e.NPlanned < 0 {
		return fmt.Errorf("n_planned cannot be negative, got %d", e.NPlanned)
	}
	if e.NTune <= 0 {
		return fmt.Errorf("n_tune must be positive, got %d — it is the sample size the confidence sequence is tuned to, and zero makes the interval undefined", e.NTune)
	}
	if e.HashVersion != CurrentHashVersion {
		return fmt.Errorf("hash_version %d is not one this build knows how to bucket (current: %d). Refusing rather than silently bucketing everyone under a different construction", e.HashVersion, CurrentHashVersion)
	}
	if err := validateGuardrails(e.Guardrails); err != nil {
		return err
	}
	seenSecondary := map[string]bool{}
	for _, s := range e.Secondary {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("a secondary metric needs an event name")
		}
		if s == e.Goal {
			return fmt.Errorf("secondary metric %q is also the goal — it would be counted twice, once inside the multiple-comparison family and once outside it", s)
		}
		if seenSecondary[s] {
			return fmt.Errorf("secondary metric %q is listed twice, which would inflate the family size and over-correct every arm", s)
		}
		seenSecondary[s] = true
	}
	if e.CUPED != nil && e.CUPED.Enabled {
		if e.CUPED.LookbackDays <= 0 {
			return fmt.Errorf("cuped.lookback_days must be positive, got %d", e.CUPED.LookbackDays)
		}
		if strings.TrimSpace(e.CUPED.Covariate) == "" {
			return fmt.Errorf("cuped needs a covariate event (it defaults to the goal); an empty one adjusts against nothing")
		}
	}
	if !e.Stopped.IsZero() && !e.Started.IsZero() && e.Stopped.Before(e.Started) {
		return fmt.Errorf("experiment stopped (%s) before it started (%s)", rfc(e.Stopped), rfc(e.Started))
	}
	return nil
}

// validateGuardrails defers the per-guardrail rules to Guardrail.Validate — including the δ=0
// refusal — so there is exactly one definition of what a usable margin is. A second copy here
// would drift, and the two would disagree about whether a plan is safe to lock.
//
// The one rule that belongs to the PLAN rather than to a single guardrail is duplication: the same
// metric guarded twice in one direction is two tests of one hypothesis, and it would appear twice
// in every readiness verdict.
func validateGuardrails(gs []Guardrail) error {
	seen := map[string]bool{}
	for _, g := range gs {
		if err := g.Validate(); err != nil {
			return err
		}
		key := g.Event + "\x00" + g.Direction
		if seen[key] {
			return fmt.Errorf("guardrail %q (%s) is declared twice", g.Event, g.Direction)
		}
		seen[key] = true
	}
	return nil
}

// Start locks the plan. at is passed in, never read from the clock here: a pure function that
// calls time.Now() produces a different plan hash on every call, and the receipt that hash anchors
// is then unverifiable by construction. Callers resolve the instant at the API/MCP boundary and
// echo it back in the response.
//
// exposedUsers is how many users have already logged an exposure on this flag. It matters only for
// Layer: folding a layer into the bucketing salt re-randomises everyone, so a layer can only be
// declared while nobody is in the experiment yet.
func StartExperiment(e *Experiment, variants []Variant, exposedUsers int, at time.Time) (*Experiment, error) {
	if e == nil {
		return nil, fmt.Errorf("no experiment plan to start")
	}
	if at.IsZero() {
		return nil, fmt.Errorf("starting an experiment needs an explicit instant — the clock is resolved at the API boundary and passed in, so the plan hashes the same way every time it is recomputed")
	}
	if !e.Started.IsZero() {
		return nil, fmt.Errorf("this experiment already started at %s. Stop it and start a new one rather than restarting it — restarting would silently re-date a plan the data was already collected under", rfc(e.Started))
	}
	if e.Layer != "" && exposedUsers > 0 {
		return nil, layerErr(e.Layer, exposedUsers)
	}
	r := e.Resolve()
	if err := r.validate(variants); err != nil {
		return nil, err
	}
	// Truncated to the second, and the STORED value is the truncated one. The hash uses second
	// precision, so if the stored value kept nanoseconds the plan on disk and the plan that was
	// hashed would be two different values — and only one of them would ever verify.
	r.Started = at.UTC().Truncate(time.Second)
	r.Stopped = time.Time{}
	r.Locked = true
	r.PlanHash = r.ComputePlanHash()
	return &r, nil
}

// Stop ends the experiment. It does NOT change the plan hash: what was pre-registered did not
// change when you stopped looking, and a receipt issued yesterday has to keep verifying today.
func StopExperiment(e *Experiment, at time.Time) (*Experiment, error) {
	if e == nil || e.Started.IsZero() {
		return nil, fmt.Errorf("this experiment has not been started, so there is nothing to stop")
	}
	if at.IsZero() {
		return nil, fmt.Errorf("stopping an experiment needs an explicit instant, resolved by the caller")
	}
	if !e.Stopped.IsZero() {
		return nil, fmt.Errorf("this experiment was already stopped at %s", rfc(e.Stopped))
	}
	stop := at.UTC().Truncate(time.Second)
	if stop.Before(e.Started) {
		return nil, fmt.Errorf("cannot stop an experiment at %s, before it started at %s", rfc(stop), rfc(e.Started))
	}
	r := *e
	r.Stopped = stop
	return &r, nil
}

// PlanFields are the fields the lock protects, in the order they are reported. Changing any of
// them after the data starts arriving means the result was chosen after seeing it.
var PlanFields = []string{
	"goal", "control", "guardrails", "secondary", "mode", "alpha", "power",
	"n_tune", "n_tune_source", "baseline_pct", "mde_pct", "n_planned",
	"randomisation_unit", "cuped", "layer", "slice", "holdout", "holdout_pct",
	"hash_version", "variant weights",
}

// ChangedPlanFields names what differs between two plans, comparing them in RESOLVED form so a
// caller that round-trips a plan through defaults does not trip the lock on a change it did not
// make. Variant weights live on the Flag, not the plan, but changing them mid-flight is the exact
// cause the SRM verdict blames for split mismatches, so they are locked with everything else.
func ChangedPlanFields(old, next Experiment, oldVariants, nextVariants []Variant) []string {
	a, b := old.Resolve(), next.Resolve()
	var out []string
	add := func(cond bool, name string) {
		if cond {
			out = append(out, name)
		}
	}
	add(a.Goal != b.Goal, "goal")
	add(a.Control != b.Control, "control")
	add(canonGuardrails(a.Guardrails) != canonGuardrails(b.Guardrails), "guardrails")
	add(canonStrings(a.Secondary) != canonStrings(b.Secondary), "secondary")
	add(a.Mode != b.Mode, "mode")
	add(a.Alpha != b.Alpha, "alpha")
	add(a.Power != b.Power, "power")
	add(a.NTune != b.NTune, "n_tune")
	// n_tune_source travels with n_tune because the same N* reached two different ways is two
	// different statements about how the design was chosen, and both are printed on the page.
	add(a.NTuneSource != b.NTuneSource, "n_tune_source")
	add(a.BaselinePct != b.BaselinePct, "baseline_pct")
	add(a.MDEPct != b.MDEPct, "mde_pct")
	add(a.NPlanned != b.NPlanned, "n_planned")
	add(a.RandomisationUnit != b.RandomisationUnit, "randomisation_unit")
	add(canonCUPED(a.CUPED) != canonCUPED(b.CUPED), "cuped")
	add(a.Layer != b.Layer, "layer")
	add(a.Slice != b.Slice, "slice")
	add(a.Holdout != b.Holdout, "holdout")
	add(a.HoldoutPct != b.HoldoutPct, "holdout_pct")
	add(a.HashVersion != b.HashVersion, "hash_version")
	add(variantWeightsDiffer(oldVariants, nextVariants), "variant weights")
	return out
}

func variantWeightsDiffer(a, b []Variant) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Weight != b[i].Weight {
			return true
		}
	}
	return false
}

// ValidatePlanChange is the gate Store.Save calls on every write. It enforces three things:
//
//  1. A layer can never be added, changed or removed once anyone is exposed — the layer is folded
//     into the bucketing salt, so changing it re-randomises every user already in the experiment
//     and re-attributes their past behaviour to whichever arm they land in next.
//  2. A locked plan cannot be edited. Stop it, or amend it with a reason that ships in the report.
//  3. An unlocked plan still has to be coherent.
//
// flagKey and exposedUsers exist for the error message: "you cannot do that" is useless next to
// "checkout_v2 has been running since 2026-07-21 with 4,102 users exposed."
func ValidatePlanChange(flagKey string, old, next *Experiment, oldVariants, nextVariants []Variant, exposedUsers int) error {
	oldLayer, nextLayer := "", ""
	if old != nil {
		oldLayer = old.Layer
	}
	if next != nil {
		nextLayer = next.Layer
	}
	if oldLayer != nextLayer && exposedUsers > 0 {
		return layerErr(nextLayer, exposedUsers)
	}

	if old == nil || !old.Locked || old.Started.IsZero() {
		if next == nil {
			return nil
		}
		return next.Validate(nextVariants)
	}
	if next == nil {
		return fmt.Errorf("%s has been running since %s with %s users exposed. Removing its analysis plan would leave the results with no record of what was pre-registered. Stop the experiment instead", flagKey, day(old.Started), planCount(exposedUsers))
	}
	// Un-starting or unlocking would be the cheapest way around every rule below, so it is checked
	// before the field comparison rather than being one entry inside it.
	if !next.Locked {
		return fmt.Errorf("%s has been running since %s with %s users exposed. A started experiment cannot be unlocked — that is just the edit you were refused, with an extra step. Stop it and start a new one", flagKey, day(old.Started), planCount(exposedUsers))
	}
	if !next.Started.Equal(old.Started) {
		return fmt.Errorf("%s started at %s and cannot be re-dated to %s — the data was collected under the original start", flagKey, rfc(old.Started), rfc(next.Started))
	}
	changed := ChangedPlanFields(*old, *next, oldVariants, nextVariants)
	if len(changed) > 0 {
		return lockedErr(flagKey, *old, exposedUsers, changed)
	}
	// Belt and braces, and not redundant: ChangedPlanFields is a hand-written comparison and the
	// canonical encoder is a hand-written serializer. If a future term reaches one and not the
	// other, this catches the direction that matters — a plan whose hash moved while the lock said
	// nothing changed, i.e. an edit that would have gone through unrecorded.
	if old.ComputePlanHash() != next.ComputePlanHash() {
		return lockedErr(flagKey, *old, exposedUsers, []string{"the plan"})
	}
	// Nothing about the plan changed, so the hash must not have either. It is computed, never
	// supplied: letting a client post its own plan_hash would let it re-anchor every receipt the
	// plan has already been quoted in.
	if next.PlanHash != old.PlanHash {
		return fmt.Errorf("%s: plan_hash is computed from the plan, not supplied. The plan is unchanged but the hash was edited (%s → %s)", flagKey, short(old.PlanHash), short(next.PlanHash))
	}
	return nil
}

// Amend is the ONLY way past the lock, and it costs a reason that ships inside the report. This is
// the difference between a pre-registration and a suggestion: you may change the plan, and every
// reader of every result afterwards sees that you did, when, and why.
func AmendExperiment(flagKey string, old, next *Experiment, oldVariants, nextVariants []Variant, reason string, exposedUsers int, at time.Time) (*Experiment, error) {
	if old == nil || old.Started.IsZero() || !old.Locked {
		return nil, fmt.Errorf("%s has no locked experiment to amend — save the plan normally, or start it first", flagKey)
	}
	if next == nil {
		return nil, fmt.Errorf("amend needs the plan you want instead")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("an amendment needs a reason. It is recorded in the report next to the result, so whoever reads the number knows the plan changed and why")
	}
	if at.IsZero() {
		return nil, fmt.Errorf("amending an experiment needs an explicit instant, resolved by the caller")
	}
	when := at.UTC().Truncate(time.Second)
	if when.Before(old.Started) {
		return nil, fmt.Errorf("cannot amend at %s, before the experiment started at %s", rfc(when), rfc(old.Started))
	}

	changed := ChangedPlanFields(*old, *next, oldVariants, nextVariants)
	if len(changed) == 0 {
		return nil, fmt.Errorf("nothing to amend — this plan is identical to the one already running")
	}
	// Two fields are not amendable at any price once users are exposed, because neither changes
	// the analysis: both change WHO IS IN WHICH ARM. The layer and the hash version are inputs to
	// the bucketing salt, so amending either silently re-randomises everyone already exposed and
	// re-attributes their existing behaviour. There is no reason string that makes that readable.
	for _, f := range changed {
		if (f == "layer" || f == "hash_version") && exposedUsers > 0 {
			return nil, fmt.Errorf("%s: %s cannot be amended with %s users already exposed — it feeds the bucketing salt, so changing it re-randomises every one of them and re-attributes the behaviour they have already produced. Stop this experiment and start a new one", flagKey, f, planCount(exposedUsers))
		}
	}

	r := next.Resolve()
	r.Started = old.Started
	r.Stopped = old.Stopped
	r.Locked = true
	if err := r.validate(nextVariants); err != nil {
		return nil, err
	}
	from := old.PlanHash
	r.PlanHash = r.ComputePlanHash()
	sorted := append([]string(nil), changed...)
	sort.Strings(sorted)
	r.Amendments = append(append([]Amendment(nil), old.Amendments...), Amendment{
		At:       when,
		Reason:   strings.TrimSpace(reason),
		Changed:  sorted,
		FromHash: from,
		ToHash:   r.PlanHash,
	})
	return &r, nil
}

func lockedErr(flagKey string, old Experiment, exposedUsers int, changed []string) error {
	return fmt.Errorf("%s has been running since %s with %s users exposed. Changing %s now means the result you report was chosen after seeing the data. Stop it and start a new experiment, or amend it with a reason — which is recorded and shipped inside the report",
		flagKey, day(old.Started), planCount(exposedUsers), strings.Join(changed, ", "))
}

func layerErr(layer string, exposedUsers int) error {
	name := layer
	if name == "" {
		name = "(none)"
	}
	return fmt.Errorf("this flag already has %s users exposed, so its layer cannot be set to %s. The layer is folded into the bucketing salt, so changing it re-randomises every user already in the experiment — their past behaviour stays counted, under whichever arm they land in next, and nothing in the numbers would show it. Layers are declared before the first exposure or not at all",
		planCount(exposedUsers), name)
}

// ComputePlanHash is the content address of the plan: sha256 over canonicalPlan, hex.
func (e Experiment) ComputePlanHash() string {
	sum := sha256.Sum256(e.canonicalPlan())
	return hex.EncodeToString(sum[:])
}

// canonicalPlan renders the plan as bytes that depend ONLY on its meaning.
//
// It is written by hand rather than by json.Marshal, and that is the whole point. Struct
// marshalling drifts in ways that are invisible in review and fatal in production: reordering
// fields in the struct reorders the output; adding omitempty makes a zero disappear; float64
// formatting is shortest-repr, so a value that arrives as 5e-2 and one written as 0.05 can render
// differently; time.Time keeps nanoseconds and a monotonic reading, so a plan just constructed and
// the same plan read back off disk are not the same bytes. Each of those turns "same plan, same
// hash" into "same plan, different hash", which is worse than no hash at all — the receipt would
// report an amendment nobody made.
//
// So: keys sorted, one fixed float format, one fixed time precision (seconds, UTC), sets sorted,
// and every field named explicitly. Adding a field to Experiment without adding it here is caught
// by TestEveryPlanFieldIsHashedOrExplicitlyExcluded.
//
// Excluded on purpose:
//   - plan_hash — it is the output.
//   - locked — a state, not a term of the plan.
//   - amendments — the log OF changes; folding it in would change the hash for something that is
//     already recorded, and every prior receipt would stop verifying for the wrong reason.
//   - stopped — stopping is not a change to what was pre-registered. If it were hashed, every
//     receipt issued while the experiment ran would break the moment it ended.
func (e Experiment) canonicalPlan() []byte {
	r := e.Resolve()
	fields := map[string]string{
		"alpha":              canonFloat(r.Alpha),
		"baseline_pct":       canonFloat(r.BaselinePct),
		"control":            canonStr(r.Control),
		"cuped":              canonCUPED(r.CUPED),
		"goal":               canonStr(r.Goal),
		"guardrails":         canonGuardrails(r.Guardrails),
		"hash_version":       strconv.Itoa(r.HashVersion),
		"layer":              canonStr(r.Layer),
		"mde_pct":            canonFloat(r.MDEPct),
		"mode":               canonStr(r.Mode),
		"n_planned":          strconv.Itoa(r.NPlanned),
		"n_tune":             strconv.Itoa(r.NTune),
		"n_tune_source":      canonStr(r.NTuneSource),
		"power":              canonFloat(r.Power),
		"randomisation_unit": canonStr(r.RandomisationUnit),
		"secondary":          canonStrings(r.Secondary),
		"started":            canonTime(r.Started),
		// Slice and holdout decide WHO is eligible at all. Changing either after start silently
		// swaps the population underneath a running result — the most invisible way to pick an
		// outcome after seeing the data, because every number on screen stays plausible.
		"slice":       "[" + canonFloat(r.Slice.Start) + "," + canonFloat(r.Slice.End) + "]",
		"holdout":     canonStr(r.Holdout),
		"holdout_pct": canonFloat(r.HoldoutPct),
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(canonStr(k))
		b.WriteByte(':')
		b.WriteString(fields[k])
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// canonFloat formats a float64 by its VALUE, not by its shortest printable form. 'e' with 17
// significant digits round-trips every float64 exactly and never varies with magnitude, so 0.05
// and a 0.05 that arrived over the wire as 5e-2 — the same float64 — always produce the same
// bytes.
func canonFloat(f float64) string {
	if f == 0 {
		f = 0 // negative zero is the same number and must not hash differently
	}
	return strconv.FormatFloat(f, 'e', 17, 64)
}

// canonTime pins the precision at one second, UTC. A time from time.Now() carries nanoseconds and
// a monotonic reading; the same instant read back from JSON carries neither. Without a fixed
// precision the disk round-trip alone would change the hash.
func canonTime(t time.Time) string {
	if t.IsZero() {
		return `""`
	}
	return canonStr(t.UTC().Truncate(time.Second).Format(time.RFC3339))
}

func canonStr(s string) string { return strconv.Quote(s) }

// canonStrings sorts: Secondary is a SET of metrics. Two plans listing the same metrics in a
// different order are the same plan, and hashing them differently would report an amendment that
// never happened.
func canonStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	c := append([]string(nil), ss...)
	sort.Strings(c)
	parts := make([]string, len(c))
	for i, s := range c {
		parts[i] = canonStr(s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// canonGuardrails sorts by (event, direction) for the same reason as canonStrings: the guardrails
// are a set, and the order someone typed them in is not part of the plan.
//
// It hashes the RESOLVED margin (Margin(), not the pointer) and the resolved direction. A margin
// left unstated and one written as 10 are the same guardrail, so they must produce the same bytes:
// hashing the pointer's presence instead would report an amendment the moment a plan travelled
// through a client that filled its own defaults in. An explicit 0 still hashes as 0 and is still
// refused by validation, so nothing is laundered by this.
func canonGuardrails(gs []Guardrail) string {
	if len(gs) == 0 {
		return "[]"
	}
	c := append([]Guardrail(nil), gs...)
	for i := range c {
		if c[i].Direction == "" {
			c[i].Direction = GuardrailNotWorse
		}
	}
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Event != c[j].Event {
			return c[i].Event < c[j].Event
		}
		return c[i].Direction < c[j].Direction
	})
	parts := make([]string, len(c))
	for i, g := range c {
		parts[i] = "{" + canonStr("direction") + ":" + canonStr(g.Direction) +
			"," + canonStr("event") + ":" + canonStr(g.Event) +
			"," + canonStr("margin_pct") + ":" + canonFloat(g.Margin()) + "}"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func canonCUPED(c *CUPEDCfg) string {
	if c == nil {
		return "null"
	}
	return "{" + canonStr("covariate") + ":" + canonStr(c.Covariate) +
		"," + canonStr("enabled") + ":" + strconv.FormatBool(c.Enabled) +
		"," + canonStr("lookback_days") + ":" + strconv.Itoa(c.LookbackDays) + "}"
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func short(h string) string {
	if len(h) > 16 {
		return h[:16]
	}
	return h
}

// planCount groups thousands. "4102 users exposed" is a number; "4,102 users exposed" is a
// sentence someone reads at a glance, and these strings exist to be read under pressure.
func planCount(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
