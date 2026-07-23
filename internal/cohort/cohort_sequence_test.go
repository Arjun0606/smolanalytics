package cohort

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

func TestMatchSequence(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return base.Add(d) }
	e := func(name string, d time.Duration) event.Event {
		return event.Event{DistinctID: "u", Name: name, Timestamp: at(d)}
	}
	ep := func(name string, d time.Duration, props map[string]any) event.Event {
		return event.Event{DistinctID: "u", Name: name, Timestamp: at(d), Properties: props}
	}
	day := int64(24 * time.Hour / time.Millisecond)
	proOnly := []query.Filter{{Property: "plan", Op: query.Eq, Value: "pro"}}

	tests := []struct {
		name string
		evs  []event.Event
		seq  Sequence
		want bool
	}{
		{"ordered A then B", []event.Event{e("A", 0), e("B", time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}}, true},
		{"wrong order B then A", []event.Event{e("B", 0), e("A", time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}}, false},
		{"missing second step", []event.Event{e("A", 0)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}}, false},
		{"three-step ordered", []event.Event{e("A", 0), e("B", time.Hour), e("C", 2 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}, {Event: "C"}}}, true},

		{"within window ok", []event.Event{e("A", 0), e("B", time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, WithinMs: day}, true},
		{"within window too wide", []event.Event{e("A", 0), e("B", 2 * 24 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, WithinMs: day}, false},
		// the correctness case: earliest A→B is too wide, but a LATER anchor's A→B fits the window.
		{"later anchor is tighter", []event.Event{e("A", 0), e("B", 2 * 24 * time.Hour), e("A", 2 * 24 * time.Hour), e("B", 2*24*time.Hour + time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, WithinMs: day}, true},

		{"exclude in span fails", []event.Event{e("A", 0), e("C", time.Hour), e("B", 2 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, Exclude: []string{"C"}}, false},
		{"exclude after span ok", []event.Event{e("A", 0), e("B", time.Hour), e("C", 2 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, Exclude: []string{"C"}}, true},
		{"exclude before span ok", []event.Event{e("C", 0), e("A", time.Hour), e("B", 2 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, Exclude: []string{"C"}}, true},

		{"count gate met", []event.Event{e("A", 0), e("B", time.Hour), e("B", 2 * time.Hour), e("B", 3 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B", Count: 3}}}, true},
		{"count gate unmet", []event.Event{e("A", 0), e("B", time.Hour), e("B", 2 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B", Count: 3}}}, false},

		{"within-first ok", []event.Event{e("signup", 0), e("A", 24 * time.Hour), e("B", 2 * 24 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, WithinFirstMs: 7 * day}, true},
		{"within-first too late", []event.Event{e("signup", 0), e("A", 8 * 24 * time.Hour), e("B", 9 * 24 * time.Hour)}, Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}, WithinFirstMs: 7 * day}, false},

		{"per-step filter matches pro", []event.Event{ep("A", 0, map[string]any{"plan": "pro"}), e("B", time.Hour)}, Sequence{Steps: []Step{{Event: "A", Filters: proOnly}, {Event: "B"}}}, true},
		{"per-step filter rejects free", []event.Event{ep("A", 0, map[string]any{"plan": "free"}), e("B", time.Hour)}, Sequence{Steps: []Step{{Event: "A", Filters: proOnly}, {Event: "B"}}}, false},

		{"single step present", []event.Event{e("A", 0)}, Sequence{Steps: []Step{{Event: "A"}}}, true},
		{"single step absent", []event.Event{e("B", 0)}, Sequence{Steps: []Step{{Event: "A"}}}, false},
		{"no events", nil, Sequence{Steps: []Step{{Event: "A"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchSequence(tt.evs, tt.seq); got != tt.want {
				t.Errorf("matchSequence = %v, want %v", got, tt.want)
			}
		})
	}
}

// Resolve with a Sequence returns only the users whose stream matches, and top-level filters
// + the default dev-env exclusion still apply first.
func TestResolveSequence(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		// "seq" does A then B — matches
		{DistinctID: "seq", Name: "A", Timestamp: base},
		{DistinctID: "seq", Name: "B", Timestamp: base.Add(time.Hour)},
		// "rev" does B then A — does not
		{DistinctID: "rev", Name: "B", Timestamp: base},
		{DistinctID: "rev", Name: "A", Timestamp: base.Add(time.Hour)},
	}
	d := Definition{Sequence: &Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}}}
	got := Resolve(evs, d)
	if !got["seq"] || got["rev"] || len(got) != 1 {
		t.Fatalf("Resolve(sequence) = %v, want {seq}", got)
	}
}

// A dev-env event is excluded by the top-level Apply, so a sequence that depends on it fails —
// the sequence path inherits the same dev exclusion as simple membership.
func TestResolveSequenceExcludesDevEnv(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{DistinctID: "u", Name: "A", Timestamp: base, Properties: map[string]any{"env": "development"}},
		{DistinctID: "u", Name: "B", Timestamp: base.Add(time.Hour)},
	}
	d := Definition{Sequence: &Sequence{Steps: []Step{{Event: "A"}, {Event: "B"}}}}
	if got := Resolve(evs, d); got["u"] {
		t.Fatalf("dev-env A should be excluded so A→B fails; got %v", got)
	}
}
