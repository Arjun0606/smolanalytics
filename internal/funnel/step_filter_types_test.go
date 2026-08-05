package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// sfEv builds one event with properties at a fixed offset from a base instant.
func sfEv(user, name string, min int, props map[string]any) event.Event {
	return event.Event{
		Name:       name,
		DistinctID: user,
		Timestamp:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute),
		Properties: props,
	}
}

// A per-step filter on a NUMERIC property used to match nothing: properties arrive from JSON
// as map[string]any, so 50 is a float64, and the old `e.Properties[k].(string)` assertion
// produced "" for it. The step collapsed to 0 with no error — a confident zero — while the
// same property through the ordinary filter engine matched. Old code: checkout count 0.
func TestStepFilter_NumericProperty(t *testing.T) {
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	evs := []event.Event{
		sfEv("u1", "signup", 0, nil),
		sfEv("u1", "checkout", 1, map[string]any{"amount": 50.0, "plan": "pro"}),
		sfEv("u2", "signup", 0, nil),
		sfEv("u2", "checkout", 1, map[string]any{"amount": 10.0, "plan": "free"}),
	}
	opts := Options{StepFilters: []map[string]string{nil, {"amount": "50"}}}
	r := ComputeOpts(evs, steps, time.Hour, opts)
	if got := r.Steps[1].Count; got != 1 {
		t.Errorf("checkout count with amount:50 = %d, want 1", got)
	}
	if r.Converted != 1 {
		t.Errorf("converted = %d, want 1", r.Converted)
	}
	// integer-typed properties (a Go caller, or an import) must read the same way
	evsInt := []event.Event{
		sfEv("u3", "signup", 0, nil),
		sfEv("u3", "checkout", 1, map[string]any{"amount": 50}),
	}
	if got := ComputeOpts(evsInt, steps, time.Hour, opts).Steps[1].Count; got != 1 {
		t.Errorf("int-typed amount:50 count = %d, want 1", got)
	}
}

// Booleans hit the identical assertion failure, and are the likelier real case
// (sf1=is_pro:true from the dashboard's step-filter box).
func TestStepFilter_BoolProperty(t *testing.T) {
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	evs := []event.Event{
		sfEv("u1", "signup", 0, nil),
		sfEv("u1", "checkout", 1, map[string]any{"is_pro": true}),
		sfEv("u2", "signup", 0, nil),
		sfEv("u2", "checkout", 1, map[string]any{"is_pro": false}),
	}
	opts := Options{StepFilters: []map[string]string{nil, {"is_pro": "true"}}}
	r := ComputeOpts(evs, steps, time.Hour, opts)
	if got := r.Steps[1].Count; got != 1 {
		t.Errorf("checkout count with is_pro:true = %d, want 1", got)
	}
	if got := ComputeOpts(evs, steps, time.Hour, Options{StepFilters: []map[string]string{nil, {"is_pro": "false"}}}).Steps[1].Count; got != 1 {
		t.Errorf("checkout count with is_pro:false = %d, want 1", got)
	}
}

// Users() is the funnel's drill-down list; it runs the same matcher, so the fix must show up
// there too or the list contradicts the bar it explains.
func TestStepFilter_NumericAgreesWithUsers(t *testing.T) {
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	evs := []event.Event{
		sfEv("u1", "signup", 0, nil),
		sfEv("u1", "checkout", 1, map[string]any{"amount": 50.0}),
		sfEv("u2", "signup", 0, nil),
		sfEv("u2", "checkout", 1, map[string]any{"amount": 10.0}),
	}
	opts := Options{StepFilters: []map[string]string{nil, {"amount": "50"}}}
	r := ComputeOpts(evs, steps, time.Hour, opts)
	var converted int
	for _, u := range Users(evs, steps, time.Hour, opts) {
		if u.Converted {
			converted++
		}
	}
	if converted != r.Converted {
		t.Errorf("Users converted = %d but funnel converted = %d", converted, r.Converted)
	}
}

// A step filter must still require the property to be PRESENT — the old code read a missing
// property as "" and would have matched a want of "", inventing conversions for events that
// never carried the property at all. query.Filter's Eq is `ok && toStr(v) == want`; this is
// the funnel half of the same rule.
func TestStepFilter_MissingPropertyNeverMatches(t *testing.T) {
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	evs := []event.Event{
		sfEv("u1", "signup", 0, nil),
		sfEv("u1", "checkout", 1, map[string]any{"other": "x"}),
	}
	opts := Options{StepFilters: []map[string]string{nil, {"plan": ""}}}
	if got := ComputeOpts(evs, steps, time.Hour, opts).Steps[1].Count; got != 0 {
		t.Errorf("checkout count for a missing property = %d, want 0", got)
	}
	// but an explicit JSON null stringifies to "" exactly as the filter engine reads it
	evsNull := []event.Event{
		sfEv("u2", "signup", 0, nil),
		sfEv("u2", "checkout", 1, map[string]any{"plan": nil}),
	}
	if got := ComputeOpts(evsNull, steps, time.Hour, opts).Steps[1].Count; got != 1 {
		t.Errorf("checkout count for an explicit null property = %d, want 1", got)
	}
}

// Existing behaviour must not move: a string-typed property still matches.
func TestStepFilter_StringStillMatches(t *testing.T) {
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}
	evs := []event.Event{
		sfEv("u1", "signup", 0, nil),
		sfEv("u1", "checkout", 1, map[string]any{"plan": "pro"}),
		sfEv("u2", "signup", 0, nil),
		sfEv("u2", "checkout", 1, map[string]any{"plan": "free"}),
	}
	opts := Options{StepFilters: []map[string]string{nil, {"plan": "pro"}}}
	if got := ComputeOpts(evs, steps, time.Hour, opts).Steps[1].Count; got != 1 {
		t.Errorf("checkout count with plan:pro = %d, want 1", got)
	}
}
