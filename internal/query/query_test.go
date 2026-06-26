package query

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func mk(name, source string, n float64) event.Event {
	return event.Event{Name: name, Properties: map[string]any{"source": source, "n": n}}
}

func TestFilters(t *testing.T) {
	evs := []event.Event{
		mk("signup", "google", 5),
		mk("signup", "twitter", 20),
		mk("signup", "google", 1),
	}
	if got := Apply(evs, []Filter{{Property: "source", Op: Eq, Value: "google"}}); len(got) != 2 {
		t.Fatalf("eq google = %d, want 2", len(got))
	}
	if got := Apply(evs, []Filter{{Property: "source", Op: Neq, Value: "google"}}); len(got) != 1 {
		t.Fatalf("neq google = %d, want 1", len(got))
	}
	if got := Apply(evs, []Filter{{Property: "n", Op: Gt, Value: 10}}); len(got) != 1 {
		t.Fatalf("n>10 = %d, want 1", len(got))
	}
	// AND of two filters
	got := Apply(evs, []Filter{{Property: "source", Op: Eq, Value: "google"}, {Property: "n", Op: Lt, Value: 3}})
	if len(got) != 1 {
		t.Fatalf("google AND n<3 = %d, want 1", len(got))
	}
}

func TestBreakdown(t *testing.T) {
	evs := []event.Event{
		mk("signup", "google", 0), mk("signup", "google", 0), mk("signup", "twitter", 0),
		{Name: "signup"}, // missing source -> (none)
	}
	g := Breakdown(evs, "source")
	if len(g) != 3 {
		t.Fatalf("want 3 groups, got %d", len(g))
	}
	if g[0].Value != "google" || g[0].Count != 2 {
		t.Fatalf("top group = %s/%d, want google/2", g[0].Value, g[0].Count)
	}
}
