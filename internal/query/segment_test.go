package query

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func evP(id string, props map[string]any) event.Event {
	return event.Event{Name: "signup", DistinctID: id, Properties: props}
}

func ids(evs []event.Event) map[string]bool {
	m := map[string]bool{}
	for _, e := range evs {
		m[e.DistinctID] = true
	}
	return m
}

func TestFilter_InNotInSetNotSet(t *testing.T) {
	evs := []event.Event{
		evP("hn", map[string]any{"source": "hn", "plan": "pro"}),
		evP("tw", map[string]any{"source": "twitter", "plan": "free"}),
		evP("gg", map[string]any{"source": "google", "plan": "pro"}),
		evP("none", map[string]any{"plan": "pro"}), // no source property
	}

	// in: OR over one property — "from HN or Twitter"
	got := ids(Apply(evs, []Filter{{Property: "source", Op: In, Value: []any{"hn", "twitter"}}}))
	if !got["hn"] || !got["tw"] || got["gg"] || got["none"] {
		t.Errorf("in[hn,twitter] matched %v, want {hn,tw}", got)
	}

	// notin: not HN, INCLUDING the event with no source at all
	got = ids(Apply(evs, []Filter{{Property: "source", Op: NotIn, Value: []any{"hn"}}}))
	if got["hn"] || !got["tw"] || !got["gg"] || !got["none"] {
		t.Errorf("notin[hn] matched %v, want {tw,gg,none}", got)
	}

	// set: has a source
	got = ids(Apply(evs, []Filter{{Property: "source", Op: Set}}))
	if !got["hn"] || !got["tw"] || !got["gg"] || got["none"] {
		t.Errorf("set(source) matched %v, want {hn,tw,gg}", got)
	}

	// notset: missing source
	got = ids(Apply(evs, []Filter{{Property: "source", Op: NotSet}}))
	if got["hn"] || got["tw"] || got["gg"] || !got["none"] {
		t.Errorf("notset(source) matched %v, want {none}", got)
	}

	// the headline query: pro users from HN OR Twitter
	got = ids(Apply(evs, []Filter{
		{Property: "plan", Op: Eq, Value: "pro"},
		{Property: "source", Op: In, Value: []any{"hn", "twitter"}},
	}))
	if !got["hn"] || got["tw"] || got["gg"] || got["none"] {
		t.Errorf("pro AND source in [hn,twitter] matched %v, want {hn} (tw is free, gg is google)", got)
	}
}

func TestValidate_InNeedsList(t *testing.T) {
	if err := Validate([]Filter{{Property: "source", Op: In, Value: "hn"}}); err == nil {
		t.Error("in with a non-list value must be rejected (would silently match nothing)")
	}
	if err := Validate([]Filter{{Property: "source", Op: In, Value: []any{"hn"}}}); err != nil {
		t.Errorf("in with a list value must validate, got %v", err)
	}
	if err := Validate([]Filter{{Property: "source", Op: Set}}); err != nil {
		t.Errorf("set must validate without a value, got %v", err)
	}
}
