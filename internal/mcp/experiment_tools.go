package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// The experiment lifecycle, from the editor.
//
// The engine has had sequential inference, a power calculator, guardrails and a lockable
// pre-registered plan for a while, and an agent could reach exactly none of it — it could read a
// flag's A/B numbers and nothing else. So the model could report a result but could not help
// design the test that produced it, which is backwards: the design is where experiments are won
// or lost, and it is the part a person is most likely to skip.
//
// These tools exist so the conversation "should I ship this?" can start at "how long will it take
// to know?" instead of ending at a p-value nobody planned for.

func init() {
	toolList = append(toolList,
		map[string]any{
			"name": "plan_experiment",
			"description": "Work out how big an experiment needs to be and how long it will run, from " +
				"THIS instance's own traffic. Give it the flag and the goal event and it estimates the " +
				"baseline rate from real data, then returns the sample size, the per-arm split, the " +
				"expected duration, and what the always-valid (sequential) version costs on top. " +
				"Use it BEFORE anyone starts an experiment, and whenever someone asks 'how long until " +
				"we know'. If the answer is months, say so plainly — that is the single most useful " +
				"thing this tool can tell a small product, and the reason most experiments should not " +
				"be run at all.",
			"inputSchema": obj(map[string]any{
				"key":     map[string]any{"type": "string", "description": "The flag key, so the arms and their weights come from the real flag."},
				"goal":    map[string]any{"type": "string", "description": "The event that counts as a win."},
				"mde_pct": map[string]any{"type": "number", "description": "Relative lift to detect, in percent. 5 means 'a 5% relative improvement'. Default 5."},
				"alpha":   map[string]any{"type": "number", "description": "False-positive rate. Default 0.05."},
				"power":   map[string]any{"type": "number", "description": "Chance of detecting a real effect. Default 0.80."},
				"days":    map[string]any{"type": "number", "description": "History used to estimate the baseline rate and traffic. Default 30."},
			}, []string{"key", "goal"}),
		},
		map[string]any{
			"name": "experiment_plan",
			"description": "Read or WRITE an experiment's pre-registered analysis plan: which arm is " +
				"control, the goal, guardrails and their margins, inference mode, alpha, and the layer " +
				"or holdout it belongs to. Call with only `key` to read. Pass `start: true` to LOCK it, " +
				"after which the plan cannot be edited — only stopped, or amended with a reason that " +
				"ships inside every subsequent report. That is the point: a plan chosen after seeing " +
				"the data is not a plan. Tell the user what locking means before you do it.",
			"inputSchema": obj(map[string]any{
				"key":        map[string]any{"type": "string", "description": "The flag key."},
				"goal":       map[string]any{"type": "string", "description": "Primary metric event."},
				"control":    map[string]any{"type": "string", "description": "Which variant is the baseline. Declared, never guessed — inferring it alphabetically inverts the sign of every lift on arms named {control, a_new}."},
				"mode":       map[string]any{"type": "string", "description": "sequential (default, safe to check daily) or fixed (one look at a planned sample size)."},
				"alpha":      map[string]any{"type": "number", "description": "Default 0.05."},
				"n_planned":  map[string]any{"type": "number", "description": "Total exposed units you are planning for, from plan_experiment."},
				"guardrails": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Events that must not get worse, e.g. $exception. Each gets a non-inferiority test at a 10% margin by default."},
				"layer":      map[string]any{"type": "string", "description": "Mutually-exclusive layer. Cannot be set once anyone is exposed — it would re-randomise them."},
				"start":      map[string]any{"type": "boolean", "description": "Lock the plan and begin. Irreversible except by stopping or amending."},
			}, []string{"key"}),
		},
	)
}

// toolPlanExperiment answers "how long until we know", from real traffic.
func (s *Server) toolPlanExperiment(args json.RawMessage, evs []event.Event) (string, error) {
	var a struct {
		Key    string    `json:"key"`
		Goal   string    `json:"goal"`
		MDEPct float64   `json:"mde_pct"`
		Alpha  float64   `json:"alpha"`
		Power  float64   `json:"power"`
		Days   float64   `json:"days"`
		Filter FilterSet `json:"filters"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.Key == "" || a.Goal == "" {
		return "", fmt.Errorf("key (the flag) and goal (the winning event) are both required")
	}
	if s.flags == nil {
		return "", fmt.Errorf(noStore, "flag")
	}
	f, ok := s.flags.Get(a.Key)
	if !ok {
		return "", fmt.Errorf("no flag named %q", a.Key)
	}
	if err := s.checkEvents(a.Goal); err != nil {
		return "", err
	}
	days := int(a.Days)
	if days <= 0 {
		days = 30
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	scoped := query.Apply(evs, nil)

	// The baseline comes from THIS instance's events, not from a number the caller invented.
	// A plan built on a guessed baseline answers a question about a product that does not exist.
	base, err := flag.EstimateBaseline(scoped, a.Goal, "", "", from, to)
	if err != nil {
		return "", err
	}
	mde := a.MDEPct
	if mde <= 0 {
		mde = 5
	}
	// base.Unit travels with the rate. The sample size belongs to whichever unit the baseline was
	// measured over, and leaving it blank makes the plan say "randomisation unit unstated" — true,
	// but avoidable here, and a sample size whose estimand is unnamed is a number waiting to be
	// compared against a differently-defined one.
	plan, err := flag.ComputePower(flag.PowerInput{
		Baseline: base.RatePct / 100, MDEPct: mde, Alpha: a.Alpha, Power: a.Power,
		Variants: f.Variants, DailyUnits: base.DailyUnits, Unit: base.Unit,
	})
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{
		"flag": a.Key, "goal": a.Goal, "baseline": base, "plan": plan,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339),
	})
}

// toolExperimentPlan reads or writes the pre-registered plan.
//
// Writing is deliberately not a silent upsert. A plan that is already locked refuses edits with a
// message naming what would change and how many people are already exposed, because the entire
// value of pre-registration is that it cannot be quietly re-aimed once the data starts arriving.
func (s *Server) toolExperimentPlan(args json.RawMessage, evs []event.Event) (string, error) {
	var a struct {
		Key        string   `json:"key"`
		Goal       string   `json:"goal"`
		Control    string   `json:"control"`
		Mode       string   `json:"mode"`
		Alpha      float64  `json:"alpha"`
		NPlanned   int      `json:"n_planned"`
		Guardrails []string `json:"guardrails"`
		Layer      string   `json:"layer"`
		Start      bool     `json:"start"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.Key == "" {
		return "", fmt.Errorf("key (the flag) is required")
	}
	if s.flags == nil {
		return "", fmt.Errorf(noStore, "flag")
	}
	f, ok := s.flags.Get(a.Key)
	if !ok {
		return "", fmt.Errorf("no flag named %q", a.Key)
	}

	// Read: no plan is a fact worth stating, not an empty object to paper over.
	if a.Goal == "" && a.Control == "" && a.Mode == "" && a.Layer == "" &&
		a.Alpha == 0 && a.NPlanned == 0 && len(a.Guardrails) == 0 && !a.Start {
		if f.Experiment == nil {
			return jsonText(map[string]any{
				"flag": a.Key, "pre_registered": false,
				"read": "this flag has no pre-registered plan, so its A/B read falls back to documented " +
					"defaults: sequential inference at alpha 0.05, control taken as the first declared " +
					"variant, no guardrails. That is a working experiment, not a designed one.",
			})
		}
		r := f.Experiment.Resolve()
		return jsonText(map[string]any{"flag": a.Key, "pre_registered": true, "plan": r})
	}

	// Write. Start from the existing plan so a partial call amends rather than silently blanks
	// fields the caller did not mention.
	plan := flag.Experiment{}
	if f.Experiment != nil {
		plan = *f.Experiment
	}
	if a.Goal != "" {
		plan.Goal = a.Goal
		if err := s.checkEvents(a.Goal); err != nil {
			return "", err
		}
	}
	if a.Control != "" {
		plan.Control = a.Control
	}
	if a.Mode != "" {
		plan.Mode = a.Mode
	}
	if a.Alpha > 0 {
		plan.Alpha = a.Alpha
	}
	if a.NPlanned > 0 {
		plan.NPlanned = a.NPlanned
	}
	if a.Layer != "" {
		plan.Layer = a.Layer
	}
	for _, g := range a.Guardrails {
		if g == "" {
			continue
		}
		if err := s.checkEvents(g); err != nil {
			return "", err
		}
		// DirectionFor, so naming "$exception" as a guardrail does not silently produce one that
		// forbids errors falling. An explicit direction in the request still wins.
		plan.Guardrails = append(plan.Guardrails, flag.Guardrail{Event: g, Direction: flag.DirectionFor(g)})
	}

	// Measured, not assumed: it decides whether a layer may still be set (setting one on an
	// exposed flag re-randomises everyone already in the test) and it turns "you cannot edit
	// that" into "checkout_v2 has been running since Jul 21 with 4,102 users exposed".
	exposed := map[string]bool{}
	for _, e := range evs {
		if e.Name != flag.ExposureEvent {
			continue
		}
		if fk, _ := e.Properties[flag.PropFlag].(string); fk == a.Key {
			exposed[e.DistinctID] = true
		}
	}
	if a.Start {
		started, err := flag.StartExperiment(&plan, f.Variants, len(exposed), time.Now().UTC())
		if err != nil {
			return "", err
		}
		plan = *started
	}

	f.Experiment = &plan
	saved, err := s.flags.SaveWithExposure(f, len(exposed))
	if err != nil {
		return "", err
	}
	out := map[string]any{"flag": a.Key, "plan": saved.Experiment.Resolve()}
	if saved.Experiment.Locked {
		out["read"] = "locked. From here the plan cannot be edited — only stopped, or amended with a " +
			"reason that ships inside every report from now on, so a reader always sees that it changed."
	} else {
		out["read"] = "saved but NOT started. Nothing is locked until you start it, which means the " +
			"design can still be changed after seeing data — call again with start:true when you mean it."
	}
	return jsonText(out)
}
