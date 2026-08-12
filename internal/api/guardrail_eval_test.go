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
	st := memory.New()
	s := New(st)
	fs, err := flag.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)

	started := time.Now().UTC().AddDate(0, 0, -7)
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
		ts := started.Add(time.Duration(i) * time.Minute)
		evs = append(evs, event.Event{
			ID: "x" + u, Name: flag.ExposureEvent, DistinctID: u, Timestamp: ts,
			Properties: map[string]any{flag.PropFlag: "risky", flag.PropVariant: arm},
		})
		if i%100 < errs {
			evs = append(evs, event.Event{
				ID: "e" + u, Name: "$exception", DistinctID: u, Timestamp: ts.Add(time.Minute),
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
