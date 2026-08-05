package flag

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// experiment.go's own comment promised "attach it to a Flag and Store.Save enforces the lock".
// Store.Save never called ValidatePlanChange, so it did not. Verified the gap by changing a
// locked plan's goal through the MCP tool and watching it succeed: a pre-registered plan that can
// be silently re-aimed once the data arrives is not pre-registration, it is a comment.
func TestSaveRefusesToEditALockedPlan(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "flags.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := Flag{Key: "checkout_v2", Enabled: true, Measured: true,
		Variants: []Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}}
	plan := Experiment{Goal: "checkout", Control: "control"}
	started, err := StartExperiment(&plan, f.Variants, 0, time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	f.Experiment = started
	if _, err := st.Save(f); err != nil {
		t.Fatalf("saving a freshly started plan: %v", err)
	}

	// re-aim it after the fact
	edited, _ := st.Get("checkout_v2")
	changed := *edited.Experiment
	changed.Goal = "$exception"
	edited.Experiment = &changed
	_, err = st.SaveWithExposure(edited, 4102)
	if err == nil {
		t.Fatal("a locked plan's goal was changed after the experiment started")
	}
	// the refusal has to be actionable, not just a no
	for _, want := range []string{"checkout_v2", "goal", "4,102", "amend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so an operator cannot act on it: %v", want, err)
		}
	}

	// and the stored plan must be untouched, not partially applied
	after, _ := st.Get("checkout_v2")
	if after.Experiment.Goal != "checkout" {
		t.Errorf("the refused edit was persisted anyway: goal is now %q", after.Experiment.Goal)
	}
}

// An UNLOCKED plan is still editable — the lock begins at start, not at creation, or nobody could
// ever design an experiment in more than one call.
func TestSaveAllowsEditingBeforeStart(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "flags.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := Flag{Key: "banner", Enabled: true,
		Variants: []Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}}}
	f.Experiment = &Experiment{Goal: "signup", Control: "a"}
	if _, err := st.Save(f); err != nil {
		t.Fatalf("first save: %v", err)
	}
	f.Experiment = &Experiment{Goal: "checkout", Control: "a"}
	if _, err := st.Save(f); err != nil {
		t.Fatalf("editing an unlocked plan was refused: %v", err)
	}
	got, _ := st.Get("banner")
	if got.Experiment.Goal != "checkout" {
		t.Errorf("goal = %q, want the edited checkout", got.Experiment.Goal)
	}
}

// A flag with no plan at all must save exactly as it always did.
func TestSaveIsUnchangedForFlagsWithoutAPlan(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "flags.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := Flag{Key: "plain", Enabled: true, Variants: []Variant{{Key: "on", Weight: 100}}}
	if _, err := st.Save(f); err != nil {
		t.Fatalf("a plain flag was refused: %v", err)
	}
	f.Enabled = false
	if _, err := st.Save(f); err != nil {
		t.Fatalf("toggling a plain flag was refused: %v", err)
	}
}
