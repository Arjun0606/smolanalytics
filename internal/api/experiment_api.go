package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// POST /v1/experiments — start an experiment from two questions.
//
// The statistics have been here for a long time: sequential always-valid intervals, SRM detection,
// CUPED, Benjamini-Hochberg, guardrail non-inferiority, power planning, layers, locked
// pre-registered plans. Mixpanel gates the equivalent behind Enterprise and PostHog wraps it in a
// form that assumes a product manager. Meanwhile our own dashboard could create a flag with two
// variants and nothing else, so none of it was reachable by the person this product is for.
//
// The design rule is the one that separates us from both: DO NOT ASK THE USER TO BE THE EXPERT.
// PostHog's setup asks for control, exposure, goal metric, minimum detectable effect, statistical
// method and duration. Every one of those is a decision a solo developer has no basis to make, and
// getting any of them wrong invalidates the result silently. So this asks two things — what are you
// changing, and what should get better — and derives the rest:
//
//	control        the first variant, declared not guessed (alphabetical inference inverts the
//	               sign of every lift on arms named {control, a_new})
//	mode           sequential, so checking daily is safe. Peeking at a fixed-horizon test runs
//	               ~43% false positives; a solo dev WILL peek daily, so the design has to survive it
//	guardrail      $exception, because "did this break anything" is the question nobody remembers
//	               to ask and the one that costs the most when unasked
//	n_planned      from the real baseline in this instance's own events
//
// AND THE REFUSAL IS THE FEATURE. If the traffic cannot support the test, this says so in months
// and tells them not to run it. A tool that lets someone run a four-month experiment on 40 visitors
// a day, and reports a p-value at the end, has done them more harm than one with no experiments at
// all.
func (s *Server) apiCreateExperiment(w http.ResponseWriter, r *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags are not enabled on this instance, so there is nowhere to store an experiment")
		return
	}
	var p struct {
		Key         string  `json:"key"`  // what you are changing
		Goal        string  `json:"goal"` // what should get better
		Description string  `json:"description"`
		MDEPct      float64 `json:"mde_pct"` // optional; the honest default is 5
		Start       bool    `json:"start"`   // false = plan only, show me the cost first
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "send {key, goal}: what you are changing, and what should get better")
		return
	}
	if p.Key == "" || p.Goal == "" {
		writeErr(w, http.StatusBadRequest, "an experiment needs two things: `key` (what you are changing, "+
			"the string your code passes to flag()) and `goal` (the event that counts as a win, e.g. signup)")
		return
	}
	if !s.knownEventOr400(w, p.Goal) {
		return
	}

	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope, same as every report

	// The baseline comes from THIS instance's traffic. A plan built on a number the user invented
	// answers a question about a product that does not exist.
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	base, berr := flag.EstimateBaseline(evs, p.Goal, "", "", from, to)
	if berr != nil {
		writeErr(w, http.StatusBadRequest, berr.Error())
		return
	}

	mde := p.MDEPct
	if mde <= 0 {
		// 5% relative. Small enough to be worth shipping, large enough to be findable — and stated
		// rather than silently assumed, because the MDE is the single input that most changes how
		// long a test runs and it is the one nobody outside analytics has an intuition for.
		mde = 5
	}
	variants := []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}
	plan, perr := flag.ComputePower(flag.PowerInput{
		Baseline: base.RatePct / 100, MDEPct: mde,
		Variants: variants, DailyUnits: base.DailyUnits, Unit: base.Unit,
	})
	if perr != nil {
		writeErr(w, http.StatusBadRequest, perr.Error())
		return
	}

	out := map[string]any{
		"key": p.Key, "goal": p.Goal, "baseline": base, "plan": plan,
		"verdict": experimentVerdict(plan),
	}

	// Plan-only is the default path, deliberately. Someone should see the cost in days before
	// anything is created, because the honest answer is often "do not run this".
	if !p.Start {
		out["created"] = false
		out["read"] = "nothing was created. this is what the experiment would cost at your current " +
			"traffic. send start:true to actually begin it."
		writeJSON(w, http.StatusOK, out)
		return
	}

	f, existing := s.flags.Get(p.Key)
	if !existing {
		f = flag.Flag{Key: p.Key, Description: p.Description}
	}
	f.Measured = true // an experiment must log exposures or there is nothing to analyse
	if len(f.Variants) < 2 {
		f.Variants = variants
	}
	f.Experiment = &flag.Experiment{
		Goal: p.Goal,
		// Declared, never inferred. Inferring control alphabetically inverts the sign of every
		// lift on arms named {control, a_new}, and the report would be confidently backwards.
		Control:  f.Variants[0].Key,
		Mode:     "sequential",
		Alpha:    0.05,
		NPlanned: plan.NPlanned,
		// The question nobody remembers to ask. A conversion win that doubled your error rate is
		// not a win, and by default nobody would have checked.
		Guardrails: []flag.Guardrail{{Event: "$exception"}},
	}
	started, serr := flag.StartExperiment(f.Experiment, f.Variants, 0, time.Now().UTC())
	if serr != nil {
		writeErr(w, http.StatusBadRequest, serr.Error())
		return
	}
	f.Experiment = started
	saved, saveErr := s.flags.SaveWithExposure(f, 0)
	if saveErr != nil {
		writeErr(w, http.StatusBadRequest, saveErr.Error())
		return
	}
	out["created"] = true
	out["flag"] = saved
	out["read"] = fmt.Sprintf("running. the plan is locked, so it cannot be re-aimed after seeing "+
		"data — that is what makes the result mean anything. control is %q, the win is %q, and "+
		"$exception is watched as a guardrail so a conversion win that broke something does not "+
		"read as a win. check it as often as you like: the read is always-valid, so looking daily "+
		"does not inflate your false-positive rate.", saved.Experiment.Control, p.Goal)
	writeJSON(w, http.StatusOK, out)
}

// experimentVerdict turns a sample-size calculation into advice, including advice not to run it.
//
// This is the part a solo developer cannot do for themselves and the part every other tool leaves
// out. "n = 47,000" is arithmetic; "at 40 visitors a day that is three years, so ship it and watch
// retention instead" is the answer. A tool that lets someone start a test it can never finish, and
// then hands them a p-value, has done them more harm than one with no experiments at all.
func experimentVerdict(plan flag.PowerPlan) string {
	d := plan.RecommendedDays
	switch {
	case plan.DailyUnits <= 0:
		return "there is not enough traffic on this instance yet to estimate how long a test would " +
			"take. run it only if you are happy to wait and see."
	case d <= 0:
		return "the duration could not be estimated from your traffic."
	case d > 120:
		return fmt.Sprintf("do not run this. at your traffic it needs about %d days — %.0f months — "+
			"to detect a %.0f%% improvement, and a product changes more in that time than the test "+
			"measures. ship the change you believe in and watch retention instead.",
			d, float64(d)/30, plan.MDEPct)
	case d > 45:
		return fmt.Sprintf("this needs about %d days at your traffic. that is a long time to hold a "+
			"change still — consider testing something with a bigger expected effect, or shipping it "+
			"and measuring after.", d)
	case d > 21:
		return fmt.Sprintf("about %d days at your current traffic. worth running.", d)
	default:
		return fmt.Sprintf("about %d days at your current traffic. that is a fast test — run it.", d)
	}
}
