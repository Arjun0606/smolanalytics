package defined

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestDefinedEventInjectedRetroactively(t *testing.T) {
	base := memory.New()
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	click := func(id, user, text, path string) event.Event {
		return event.Event{ID: id, Name: "$click", DistinctID: user, Timestamp: now,
			Properties: map[string]any{"text": text, "path": path}}
	}
	_ = base.Ingest(
		click("1", "u1", "Buy now", "/pricing"),
		click("2", "u2", "Buy now", "/pricing"),
		click("3", "u3", "Learn more", "/"),
		event.Event{ID: "4", Name: "signup", DistinctID: "u1", Timestamp: now},
	)

	ds, _ := Open(filepath.Join(t.TempDir(), "defined.json"))
	// define checkout = $click where text contains "Buy" — retroactive, zero code
	if _, err := ds.Save(Definition{Name: "checkout", Event: "$click", Where: []Condition{{Field: "text", Op: "contains", Value: "buy"}}}); err != nil {
		t.Fatal(err)
	}
	w := Wrap(base, ds)

	evs, _ := w.Range(time.Time{}, time.Time{})
	checkout := 0
	for _, e := range evs {
		if e.Name == "checkout" {
			checkout++
		}
	}
	if checkout != 2 {
		t.Fatalf("checkout events = %d, want 2 (the two Buy clicks, retroactive)", checkout)
	}
	// original $click rows are preserved (additive), and defined name shows in Names()
	names, _ := w.Names()
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("checkout") || !has("$click") || !has("signup") {
		t.Errorf("names = %v, want checkout + $click + signup", names)
	}

	// validation: unconditioned or $-prefixed names rejected
	if _, err := ds.Save(Definition{Name: "x", Event: "$click"}); err == nil {
		t.Error("expected error for no conditions")
	}
	if _, err := ds.Save(Definition{Name: "$click", Event: "$click", Where: []Condition{{"text", "contains", "a"}}}); err == nil {
		t.Error("expected error for $-prefixed name")
	}
}
