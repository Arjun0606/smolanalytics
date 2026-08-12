package api

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
)

// Auto-revert: the first thing in this product that acts on its own.
//
// It is also what makes the rest of it sellable. A loop that notices, diagnoses and ships is
// terrifying without a way back; the same loop with an automatic kill switch is the reason someone
// says yes. Nobody hands an unattended system the ability to change what their users see unless
// pulling it back is more reliable than the pushing.
//
// So the bar here is not "usually right". It is: never revert something that was fine, always
// revert something that is hurting, and leave a receipt either way.

// RevertEvent is written into the operator's OWN event log when a flag is pulled.
//
// Writing it as an event rather than only a log line is deliberate: it lands on the same timeline
// as the traffic it reacted to, it is queryable by every tool that reads events, and it shows up
// in the brief. An autonomous act that leaves no trace in the place people look is
// indistinguishable from a bug.
const RevertEvent = "$experiment_reverted"

const (
	// revertWarmup is how long an experiment must have been running before a breach can pull it.
	// The first minutes of a rollout are the least representative traffic it will ever see.
	revertWarmup = 15 * time.Minute
	// revertMinGap is the minimum time between the two failing checks that confirm a breach, so
	// two evaluations inside one bad minute cannot count as agreement.
	revertMinGap = 5 * time.Minute
)

// autoRevertEnabled reports whether acting is switched on.
//
// Off is honoured loudly: the evaluation still runs and the pane still shows FAIL, we simply do
// not touch the flag. Anyone who does not want software turning their flags off must be able to
// say so in one variable, and the page must be able to say which mode it is in — a safety feature
// that cannot be switched off gets the whole product switched off instead.
func autoRevertEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("SMOLANALYTICS_AUTO_REVERT")), "off")
}

// confirmedBreach reports whether this FAIL is the SECOND consecutive one for the same guardrail
// and arm, far enough apart to count as independent, on an experiment past its warm-up.
//
// One failing check is not enough even though the sequential test is always-valid. The statistics
// are not the binding constraint here — trust is. A flappy revert makes someone disable the
// feature, which costs them the safety net entirely, so the cost of waiting one more cycle is far
// lower than the cost of being wrong once.
func confirmedBreach(prev []flag.GuardrailResult, prevAt time.Time, exp flag.Experiment, r flag.GuardrailResult, now time.Time) bool {
	if now.Sub(exp.Started) < revertWarmup {
		return false
	}
	if prevAt.IsZero() || now.Sub(prevAt) < revertMinGap {
		return false
	}
	for _, p := range prev {
		if p.Event == r.Event && p.Variant == r.Variant && p.Status == "FAIL" {
			return true
		}
	}
	return false
}

// autoRevert disables a flag whose guardrail has failed twice, and leaves the receipt.
func (s *Server) autoRevert(f flag.Flag, r flag.GuardrailResult, now time.Time) {
	reason := fmt.Sprintf("guardrail %s failed twice for %q: %s", r.Event, r.Variant, r.Read)

	if _, err := s.flags.SetEnabled(f.Key, false); err != nil {
		log.Printf("[auto-revert] could not disable %s: %v", f.Key, err)
		return
	}
	// Re-read: SetEnabled persisted its own copy, so mutating the stale one would drop the change.
	cur, ok := s.flags.Get(f.Key)
	if ok && cur.Experiment != nil {
		cur.Experiment.RevertedAt = now
		cur.Experiment.RevertedReason = reason
		if _, err := s.flags.Save(cur); err != nil {
			log.Printf("[auto-revert] could not record the reason on %s: %v", f.Key, err)
		}
	}

	// The receipt, on their own timeline.
	if err := s.store.Ingest(event.Event{
		Name:       RevertEvent,
		DistinctID: "$system",
		Timestamp:  now,
		Properties: map[string]any{
			"flag": f.Key, "variant": r.Variant, "guardrail": r.Event,
			"reason": reason, "status": r.Status,
		},
	}); err != nil {
		log.Printf("[auto-revert] could not write the %s event for %s: %v", RevertEvent, f.Key, err)
	}
	log.Printf("[auto-revert] %s disabled — %s", f.Key, reason)
}

// applyAutoRevert acts on the breaches from one evaluation pass. Returns how many flags it pulled.
func (s *Server) applyAutoRevert(breaches []GuardrailBreach, prevStatus map[string][]flag.GuardrailResult, prevAt map[string]time.Time, now time.Time) int {
	if !autoRevertEnabled() || len(breaches) == 0 {
		return 0
	}
	pulled := 0
	done := map[string]bool{}
	for _, b := range breaches {
		if done[b.Flag] {
			continue // one revert per flag per pass; the first reason is the reason
		}
		f, ok := s.flags.Get(b.Flag)
		if !ok || f.Experiment == nil {
			continue
		}
		// Never re-pull something already pulled — and never silently re-enable it either. A
		// reverted experiment stays reverted until a human decides otherwise.
		if !f.Experiment.RevertedAt.IsZero() {
			continue
		}
		if !confirmedBreach(prevStatus[b.Flag], prevAt[b.Flag], f.Experiment.Resolve(), b.Result, now) {
			continue
		}
		s.autoRevert(f, b.Result, now)
		done[b.Flag] = true
		pulled++
	}
	return pulled
}
