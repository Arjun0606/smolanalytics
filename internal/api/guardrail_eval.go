package api

import (
	"log"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// The safety net that was never connected.
//
// experiment_api.go attaches a $exception guardrail to every experiment it creates, and the UI
// tells the reader: "$exception is watched as a guardrail so a conversion win that broke something
// does not read as a win." flag.EvaluateGuardrails, which would have checked it, had ZERO callers.
// Not one guardrail this product ever created has been evaluated.
//
// That is worse than not offering the feature. Someone reading that sentence ships a change more
// confidently than they otherwise would, on the strength of a check that does not exist. It is
// also the argument for the whole Investigator: a promise nobody was assigned to keep is exactly
// the thing a human misses for months and a machine should catch on the first pass.
//
// This evaluates. It does not act — see autoRevert for that, which is deliberately a separate
// decision with its own kill switch.

// GuardrailBreach is one experiment failing one guardrail.
type GuardrailBreach struct {
	Flag    string               `json:"flag"`
	Variant string               `json:"variant"`
	Result  flag.GuardrailResult `json:"result"`
}

// EvaluateGuardrails checks every running experiment's guardrails and records the verdict on the
// flag. Returns the breaches so a caller can act, alert, or ignore.
//
// Safe to call on a schedule: it is a pure read plus a status write, and the sequential test it
// runs is always-valid, which is the whole reason that machinery was chosen. Checking every five
// minutes does not inflate anyone's false-positive rate.
func (s *Server) EvaluateGuardrails() []GuardrailBreach {
	if s.flags == nil {
		return nil
	}
	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		log.Printf("[guardrails] could not read events: %v", err)
		return nil
	}
	evs := query.Apply(all, nil) // production scope, like every other report
	now := time.Now().UTC()

	// The PREVIOUS verdict, captured before this pass overwrites it. Auto-revert needs two
	// consecutive failures, so the only place that history exists is right here.
	prevStatus := map[string][]flag.GuardrailResult{}
	prevAt := map[string]time.Time{}

	var breaches []GuardrailBreach
	for _, f := range s.flags.List() {
		exp := f.Experiment
		if exp == nil || len(exp.Guardrails) == 0 || exp.Started.IsZero() || !exp.Stopped.IsZero() {
			continue
		}
		results := evaluateOne(evs, f, now)
		if results == nil {
			continue
		}
		prevStatus[f.Key] = exp.GuardrailStatus
		prevAt[f.Key] = exp.GuardrailCheckedAt
		// Persist so the pane can say WHEN it was last checked. An unchecked guardrail must never
		// render as PASS — the defect being fixed here is precisely a safety claim nobody verified,
		// and "no news" reading as "good news" is how it survived.
		exp.GuardrailStatus = results
		exp.GuardrailCheckedAt = now
		if _, serr := s.flags.Save(f); serr != nil {
			log.Printf("[guardrails] could not persist %s: %v", f.Key, serr)
		}
		for _, r := range results {
			if r.Status == "FAIL" {
				breaches = append(breaches, GuardrailBreach{Flag: f.Key, Variant: r.Variant, Result: r})
			}
		}
	}
	if n := s.applyAutoRevert(breaches, prevStatus, prevAt, now); n > 0 {
		log.Printf("[guardrails] auto-reverted %d flag(s)", n)
	}
	return breaches
}

// evaluateOne builds the per-arm counts for each guardrail and runs the test.
//
// The counts come from flag.MeasureRange with the GUARDRAIL's event as the goal, so a guardrail is
// counted by exactly the machinery that counts the primary metric — same exposure resolution, same
// randomisation unit, same window. Re-deriving them here is how the guardrail would come to
// disagree with the report printed beside it.
func evaluateOne(evs []event.Event, f flag.Flag, now time.Time) []flag.GuardrailResult {
	// Resolve, do not re-derive. It fills every default the plan left blank, and it is the same
	// call the primary-metric report makes — so the guardrail's alpha, mode, randomisation unit
	// and k(N) are identical to the ones printed beside it. Reading the raw fields here left
	// NTune at zero, which sequential mode rejects, and every guardrail came back INCONCLUSIVE:
	// a safety net that silently declines to answer, which is the failure it exists to prevent
	// wearing a different hat.
	exp := f.Experiment.Resolve()
	unit, mode := exp.RandomisationUnit, exp.Mode

	var out []flag.GuardrailResult
	for _, g := range exp.Guardrails {
		if g.Event == "" {
			continue
		}
		rep := flag.MeasureRange(evs, f, g.Event, exp.Started, now)
		var ctrl *flag.VariantResult
		for i := range rep.Variants {
			if rep.Variants[i].Key == exp.Control {
				ctrl = &rep.Variants[i]
				break
			}
		}
		if ctrl == nil {
			continue // no control arm observed yet; nothing to compare against
		}
		for _, v := range rep.Variants {
			if v.Key == exp.Control {
				continue
			}
			res, err := flag.EvaluateGuardrail(flag.GuardrailInput{
				Guardrail: g,
				Variant:   v.Key,
				ConvTest:  v.Converted, ExpTest: v.Exposed,
				ConvCtrl: ctrl.Converted, ExpCtrl: ctrl.Exposed,
				Alpha: exp.Alpha,
				Mode:  mode,
				NTune: exp.NTune,
				Unit:  unit,
			})
			if err != nil {
				// A guardrail we cannot evaluate is reported as INCONCLUSIVE, never dropped.
				// Silently omitting it would leave the pane looking like everything passed.
				out = append(out, flag.GuardrailResult{
					Event: g.Event, Variant: v.Key, Status: "INCONCLUSIVE",
				})
				continue
			}
			out = append(out, res)
		}
	}
	return out
}
