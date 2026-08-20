package desk

// The desk is where the pure investigator meets the deployment: the tracking plan is read here,
// and the kill switch is read here. Both of those decisions are the difference between software
// that opens a pull request in somebody's repository and software that does not, so neither is
// allowed to be untested just because it is three lines long.

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

func planWith(t *testing.T, names ...string) *trackplan.Store {
	t.Helper()
	tp, err := trackplan.Open("")
	if err != nil {
		t.Fatal(err)
	}
	var evs []trackplan.PlannedEvent
	for _, n := range names {
		evs = append(evs, trackplan.PlannedEvent{Name: n})
	}
	if len(evs) > 0 {
		if _, err := tp.Set(evs); err != nil {
			t.Fatal(err)
		}
	}
	return tp
}

// NIL IS THE REFUSAL, AND IT MUST BE REACHED BY EVERY ROUTE THAT MEANS "WE DO NOT KNOW".
//
// Gate G1 in the investigator reads a nil lookup as "no declared intent, never promote". Every
// case below is a state where we genuinely do not know what this app is supposed to send, and a
// non-nil lookup in any of them would be this feature's worst failure: a pull request restoring a
// call somebody deleted on purpose.
func TestPlannedRefusesWheneverIntentIsUnknown(t *testing.T) {
	if Planned(nil) != nil {
		t.Error("no tracking-plan store at all produced a usable lookup, so an instance that never " +
			"declared anything could still have a pull request opened against it")
	}
	if Planned(planWith(t)) != nil {
		t.Error("an EMPTY plan produced a lookup — an empty declaration is the absence of one, and " +
			"treating it as a statement of intent is how a deliberately removed event gets restored")
	}
	t.Setenv(TrackingBrokeEnv, "off")
	if Planned(planWith(t, "checkout_completed")) != nil {
		t.Error("the kill switch is off and the plan was handed over anyway — the one variable an " +
			"operator flips to stop this did nothing")
	}
}

// And when intent IS declared, the lookup must answer about the plan and nothing else. A lookup
// that returns true for everything would pass gate G1 for every event on the instance.
func TestPlannedAnswersOnlyForDeclaredEvents(t *testing.T) {
	p := Planned(planWith(t, "checkout_completed", "signup"))
	if p == nil {
		t.Fatal("a declared plan produced no lookup, so nothing can ever be promoted")
	}
	if !p("checkout_completed") || !p("signup") {
		t.Error("a declared event was not recognised as declared")
	}
	if p("some_event_nobody_declared") {
		t.Error("the lookup said yes to an undeclared event, which makes gate G1 a no-op and lets " +
			"any stopped event become a candidate for a pull request")
	}
}

// THE SWITCH IS EXACTLY ONE WORD.
//
// A safety switch that also disarms on a typo is worse than no switch: an operator who typed
// "false" or "no" believes the detector is off while it is running, and finds out from a pull
// request. Default-on, and only the literal word "off" turns it off.
func TestOnlyTheWordOffDisablesDetection(t *testing.T) {
	for _, v := range []string{"off", "OFF", " off ", "Off"} {
		t.Run("off/"+v, func(t *testing.T) {
			t.Setenv(TrackingBrokeEnv, v)
			if DetectionEnabled() {
				t.Errorf("%q did not disable detection, so the documented switch does not work", v)
			}
		})
	}
	for _, v := range []string{"", "on", "false", "0", "no", "disabled", "true"} {
		t.Run("on/"+v, func(t *testing.T) {
			t.Setenv(TrackingBrokeEnv, v)
			if !DetectionEnabled() {
				t.Errorf("%q silently disabled detection — an operator reading the docs expects only "+
					"%q to do that, so this instance stopped detecting without anyone deciding to", v, "off")
			}
		})
	}
}

// One contract text, served by both doors. The MCP tool and GET /v1/investigate both hand this to
// an agent as the first thing it reads; an agent taught the rules one way over MCP and another way
// over HTTP will eventually quote the wrong ones.
func TestReadMeFirstDescribesTheClassThatTriggersAction(t *testing.T) {
	if ReadMeFirst == "" {
		t.Fatal("no contract text at all")
	}
	for _, must := range []string{"tracking_broke", "tracking_ruled_out"} {
		if !contains(ReadMeFirst, must) {
			t.Errorf("the contract text never mentions %q, so an agent reading it has no way to know "+
				"the one finding kind that is not a product problem", must)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
