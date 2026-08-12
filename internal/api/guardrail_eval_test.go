package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// THE GUARD AGAINST RE-SHIPPING THE ORIGINAL DEFECT.
//
// flag.EvaluateGuardrails had zero callers for the whole life of the feature, while
// experiment_api.go attached a $exception guardrail to every experiment it created and the UI
// said it was watched. Nothing rendered wrong; the check simply never ran.
//
// The only thing that can prevent that recurring is an assertion that the evaluator is WIRED, not
// merely that it works when called. A unit test of the evaluator would have passed happily
// throughout the entire period the product was shipping an unkept promise.
func TestTheGuardrailEvaluatorIsActuallyScheduled(t *testing.T) {
	src, err := os.ReadFile("../../cmd/smolanalytics/main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "EvaluateGuardrails()") {
		t.Fatal("nothing in main.go ever calls EvaluateGuardrails — the guardrails on every " +
			"experiment are decoration again")
	}
	// and it must be on the recurring path, not a one-shot at boot
	if !strings.Contains(s, "func safetyPass(") || !strings.Contains(s, "for range t.C") {
		t.Error("EvaluateGuardrails is not on the ticker: a guardrail checked once at boot is a " +
			"guardrail that never fires")
	}
}

// expServerWithGuardrail seeds an experiment where the treatment arm throws far more exceptions.
func guardrailServer(t *testing.T, ctrlErrs, testErrs int) (*Server, flag.Flag) {
	t.Helper()
	return guardrailServerStarted(t, ctrlErrs, testErrs, 7*24*time.Hour)
}

// guardrailServerStarted packs the same traffic into a window that began `ago` in the past, so a
// fresh experiment can still be given enough events to fail a guardrail.
func guardrailServerStarted(t *testing.T, ctrlErrs, testErrs int, ago time.Duration) (*Server, flag.Flag) {
	t.Helper()
	st := memory.New()
	s := New(st)
	fs, err := flag.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)

	started := time.Now().UTC().Add(-ago)
	f := flag.Flag{
		Key: "risky", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{
			Goal: "checkout", Control: "control", Mode: flag.ModeSequential,
			Alpha: 0.05, Started: started,
			Guardrails: []flag.Guardrail{{Event: "$exception", Direction: flag.DirectionFor("$exception")}},
		},
	}
	if _, err := fs.Save(f); err != nil {
		t.Fatal(err)
	}

	var evs []event.Event
	const per = 600
	for i := 0; i < per*2; i++ {
		u := fmt.Sprintf("u%d", i)
		arm := "control"
		errs := ctrlErrs
		if i%2 == 1 {
			arm, errs = "treatment", testErrs
		}
		ts := started.Add(time.Duration(i) * ago / (per * 2))
		evs = append(evs, event.Event{
			ID: "x" + u, Name: flag.ExposureEvent, DistinctID: u, Timestamp: ts,
			Properties: map[string]any{flag.PropFlag: "risky", flag.PropVariant: arm},
		})
		if i%100 < errs {
			evs = append(evs, event.Event{
				ID: "e" + u, Name: "$exception", DistinctID: u, Timestamp: ts.Add(ago / (per * 8)),
			})
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}
	return s, f
}

// A treatment arm that breaks things must FAIL its guardrail — the case the whole feature exists
// for, and the one that has never once executed.
func TestABrokenArmFailsItsGuardrail(t *testing.T) {
	s, _ := guardrailServer(t, 20, 60) // 20% vs 60% exception rate
	breaches := s.EvaluateGuardrails()
	if len(breaches) == 0 {
		t.Fatal("a treatment arm throwing 20x the exceptions of control did not breach its guardrail")
	}
	b := breaches[0]
	if b.Flag != "risky" || b.Variant != "treatment" {
		t.Errorf("breach names the wrong arm: %+v", b)
	}
	if b.Result.Status != "FAIL" {
		t.Errorf("status is %q, expected FAIL", b.Result.Status)
	}
}

// A healthy experiment must not breach — a guardrail that fires on everything gets switched off,
// which costs the customer the protection entirely.
func TestAHealthyArmDoesNotBreach(t *testing.T) {
	s, _ := guardrailServer(t, 5, 5)
	if b := s.EvaluateGuardrails(); len(b) > 0 {
		t.Fatalf("an identical-error-rate experiment breached: %+v", b[0].Result)
	}
}

// THE VERDICT MUST BE WRITTEN DOWN, WITH A TIMESTAMP.
//
// Without CheckedAt there is nowhere for "we have not looked yet" to live, and silence renders as
// health — which is exactly how a guardrail that never ran went unnoticed for months.
func TestTheVerdictAndItsTimestampArePersisted(t *testing.T) {
	s, f := guardrailServer(t, 20, 60)

	before, _ := s.flags.Get(f.Key)
	if !before.Experiment.GuardrailCheckedAt.IsZero() {
		t.Fatal("a never-evaluated experiment already claims to have been checked")
	}

	s.EvaluateGuardrails()

	after, ok := s.flags.Get(f.Key)
	if !ok {
		t.Fatal("flag vanished")
	}
	if after.Experiment.GuardrailCheckedAt.IsZero() {
		t.Error("no CheckedAt recorded — the pane cannot distinguish 'passing' from 'never checked'")
	}
	if len(after.Experiment.GuardrailStatus) == 0 {
		t.Error("no status recorded")
	}
	if after.Experiment.GuardrailStatus[0].Status != "FAIL" {
		t.Errorf("persisted status is %q, expected FAIL", after.Experiment.GuardrailStatus[0].Status)
	}
}

// An experiment nobody started, or one already concluded, is not evaluated — we do not relitigate
// a call a human already made.
func TestStoppedAndUnstartedExperimentsAreLeftAlone(t *testing.T) {
	s, f := guardrailServer(t, 20, 60)
	f.Experiment.Stopped = time.Now().UTC()
	if _, err := s.flags.Save(f); err != nil {
		t.Fatal(err)
	}
	if b := s.EvaluateGuardrails(); len(b) > 0 {
		t.Fatalf("evaluated an experiment a human had already stopped: %+v", b[0])
	}
}

// THE GUARDRAIL POINTED THE WRONG WAY.
//
// flag.Guardrail's own doc warns: "An error-rate guardrail declared not_worse is an easy and real
// mistake — it would forbid errors FALLING." Every experiment this product created carried
// {Event: "$exception"} with no direction, which takes exactly that default.
//
// So even once the evaluator was connected, the check could not have caught an error spike under
// any circumstances — and would have fired on an improvement. Two independent failures stacked:
// a guardrail nothing evaluated, pointing the wrong way.
func TestTheDefaultErrorGuardrailForbidsErrorsRISING(t *testing.T) {
	if got := flag.DirectionFor("$exception"); got != flag.GuardrailNotBetter {
		t.Errorf("an error guardrail defaults to %q — that forbids errors falling", got)
	}
	if got := flag.DirectionFor("checkout"); got != flag.GuardrailNotWorse {
		t.Errorf("a conversion guardrail should forbid a DROP, got %q", got)
	}

	srv := expServer(t, 700, 30, 20)
	got := postExp(t, srv, map[string]any{"key": "pricing_v2", "goal": "signup", "start": true})
	f, _ := got["flag"].(map[string]any)
	exp, _ := f["experiment"].(map[string]any)
	gs, _ := exp["guardrails"].([]any)
	if len(gs) == 0 {
		t.Fatal("no guardrail was attached")
	}
	g := gs[0].(map[string]any)
	if g["direction"] != flag.GuardrailNotBetter {
		t.Errorf("the $exception guardrail this endpoint attaches has direction %v — it would "+
			"forbid errors falling, and could never catch the spike it exists for", g["direction"])
	}
}

// AUTO-REVERT. The first thing here that acts on its own, so the bar is: never pull something
// that was fine, always pull something that is hurting, and leave a receipt either way.

// One failing check is NOT enough. The statistics allow it — the sequential test is always-valid —
// but trust does not. A flappy revert makes someone disable the feature, which costs them the
// safety net entirely, so waiting one more cycle is far cheaper than being wrong once.
func TestOneFailingCheckDoesNotRevert(t *testing.T) {
	s, f := guardrailServer(t, 20, 60)

	// A previous check that is old enough to count as independent, but PASSED. Only the
	// consecutive-failure rule can stop the revert here.
	//
	// The first version of this test simply called Evaluate once, and passed even with that rule
	// deleted — the "checks must be 5 minutes apart" gate was catching it instead. A test that
	// holds for a reason other than the one it names is not a test of that reason.
	cur, _ := s.flags.Get(f.Key)
	cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
	cur.Experiment.GuardrailStatus = []flag.GuardrailResult{
		{Event: "$exception", Variant: "treatment", Status: "PASS"},
	}
	if _, err := s.flags.Save(cur); err != nil {
		t.Fatal(err)
	}

	s.EvaluateGuardrails() // the FIRST failing check

	after, _ := s.flags.Get(f.Key)
	if !after.Enabled {
		t.Error("reverted on a single failing check, after a passing one")
	}
	if !after.Experiment.RevertedAt.IsZero() {
		t.Error("recorded a revert after one failing check")
	}
}

// Two consecutive failures, far enough apart, past the warm-up: pull it.
func TestTwoConsecutiveFailuresRevert(t *testing.T) {
	s, f := guardrailServer(t, 20, 60)
	s.EvaluateGuardrails()

	// age the first verdict so the second counts as an independent check
	cur, _ := s.flags.Get(f.Key)
	cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
	if _, err := s.flags.Save(cur); err != nil {
		t.Fatal(err)
	}

	s.EvaluateGuardrails() // second FAIL

	after, _ := s.flags.Get(f.Key)
	if after.Enabled {
		t.Fatal("two confirmed guardrail failures did not disable the flag")
	}
	if after.Experiment.RevertedAt.IsZero() {
		t.Error("no RevertedAt recorded — the pane cannot say when or why")
	}
	if !strings.Contains(after.Experiment.RevertedReason, "$exception") {
		t.Errorf("the reason must name the guardrail that failed: %q", after.Experiment.RevertedReason)
	}
}

// THE RECEIPT. An autonomous act that leaves no trace where people look is indistinguishable from
// a bug. The revert lands on the operator's own timeline, beside the traffic it reacted to.
func TestARevertWritesItselfIntoTheEventLog(t *testing.T) {
	s, f := guardrailServer(t, 20, 60)
	s.EvaluateGuardrails()
	cur, _ := s.flags.Get(f.Key)
	cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
	if _, err := s.flags.Save(cur); err != nil {
		t.Fatal(err)
	}
	s.EvaluateGuardrails()

	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var found *event.Event
	for i := range all {
		if all[i].Name == RevertEvent {
			found = &all[i]
		}
	}
	if found == nil {
		t.Fatal("no $experiment_reverted event — the revert is invisible to every tool that reads events")
	}
	if found.Properties["flag"] != "risky" {
		t.Errorf("the event does not name the flag: %+v", found.Properties)
	}
	if fmt.Sprint(found.Properties["reason"]) == "" {
		t.Error("the event carries no reason")
	}
}

// A healthy experiment is never touched, however many times it is checked.
func TestAHealthyExperimentIsNeverReverted(t *testing.T) {
	s, f := guardrailServer(t, 20, 20)
	for i := 0; i < 3; i++ {
		s.EvaluateGuardrails()
		cur, _ := s.flags.Get(f.Key)
		if cur.Experiment != nil {
			cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
			if _, err := s.flags.Save(cur); err != nil {
				t.Fatal(err)
			}
		}
	}
	if after, _ := s.flags.Get(f.Key); !after.Enabled {
		t.Fatal("disabled a flag whose arms had identical error rates")
	}
}

// THE KILL SWITCH FOR THE KILL SWITCH. Anyone who does not want software turning their flags off
// must be able to say so in one variable — otherwise they switch off the whole feature and lose
// the safety net too. Reporting continues; only the acting stops.
func TestAutoRevertCanBeTurnedOff(t *testing.T) {
	t.Setenv("SMOLANALYTICS_AUTO_REVERT", "off")
	s, f := guardrailServer(t, 20, 60)
	s.EvaluateGuardrails()
	cur, _ := s.flags.Get(f.Key)
	cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
	if _, err := s.flags.Save(cur); err != nil {
		t.Fatal(err)
	}
	breaches := s.EvaluateGuardrails()

	after, _ := s.flags.Get(f.Key)
	if !after.Enabled {
		t.Fatal("reverted while SMOLANALYTICS_AUTO_REVERT=off")
	}
	// reporting must be unaffected — the operator still needs to know
	if len(breaches) == 0 {
		t.Error("switching off ACTING also switched off REPORTING; the breach must still surface")
	}
	if len(after.Experiment.GuardrailStatus) == 0 {
		t.Error("no status recorded with acting disabled")
	}
}

// A brand-new rollout sees the least representative traffic it will ever see. A breach inside the
// warm-up must not pull it.
func TestABreachInsideTheWarmupDoesNotRevert(t *testing.T) {
	// Started two minutes ago AND the traffic sits inside those two minutes, so the guardrail
	// genuinely fails. The first version moved Started without moving the events, so the window
	// was empty, there was no breach at all, and the test passed with the warm-up deleted — it
	// was asserting nothing.
	s, f := guardrailServerStarted(t, 20, 60, 2*time.Minute)
	cur, _ := s.flags.Get(f.Key)
	cur.Experiment.GuardrailCheckedAt = time.Now().UTC().Add(-10 * time.Minute)
	cur.Experiment.GuardrailStatus = []flag.GuardrailResult{
		{Event: "$exception", Variant: "treatment", Status: "FAIL"},
	}
	if _, err := s.flags.Save(cur); err != nil {
		t.Fatal(err)
	}
	if b := s.EvaluateGuardrails(); len(b) == 0 {
		t.Fatal("fixture produced no breach, so the warm-up is not what is being tested")
	}
	if after, _ := s.flags.Get(f.Key); !after.Enabled {
		t.Fatal("pulled an experiment two minutes after launch")
	}
}
