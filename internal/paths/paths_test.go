package paths

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func ev(user, name string, off time.Duration) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.Add(off)}
}

func TestAfter(t *testing.T) {
	evs := []event.Event{
		// alice: signup -> open -> checkout
		ev("alice", "signup", 0), ev("alice", "open", time.Hour), ev("alice", "checkout", 2*time.Hour),
		// bob: signup -> open (then stops)
		ev("bob", "signup", 0), ev("bob", "open", time.Hour),
		// carol: signup -> settings
		ev("carol", "signup", 0), ev("carol", "settings", time.Hour),
		// dave: never signs up
		ev("dave", "open", 0),
	}
	r := After(evs, "signup", 2)
	if r.Users != 3 {
		t.Fatalf("users = %d, want 3 (alice,bob,carol)", r.Users)
	}
	// level 1: open x2 (alice,bob), settings x1 (carol)
	if r.Levels[0].Steps[0].Event != "open" || r.Levels[0].Steps[0].Count != 2 {
		t.Fatalf("level1 top = %+v, want open/2", r.Levels[0].Steps[0])
	}
	// level 2: checkout x1 (alice); bob/carol stopped
	if r.Levels[1].Steps[0].Event != "checkout" || r.Levels[1].Steps[0].Count != 1 {
		t.Fatalf("level2 top = %+v, want checkout/1", r.Levels[1].Steps[0])
	}
}

func pvp(user, path string, off time.Duration) event.Event {
	return event.Event{DistinctID: user, Name: "$pageview", Timestamp: base.Add(off), Properties: map[string]any{"path": path}}
}

func TestPages(t *testing.T) {
	evs := []event.Event{
		// alice: / -> / (reload — collapses) -> /pricing -> /signup
		pvp("alice", "/", 0), pvp("alice", "/", time.Minute), pvp("alice", "/pricing", 2*time.Minute), pvp("alice", "/signup", 3*time.Minute),
		// bob: / -> /docs; the $click between them is autocapture noise, not a page
		pvp("bob", "/", 0), ev("bob", "$click", 30*time.Second), pvp("bob", "/docs", time.Minute),
		// carol: never visits / — not in this flow
		pvp("carol", "/pricing", 0),
	}
	r := Pages(evs, "/", 2)
	if r.Users != 2 {
		t.Fatalf("users = %d, want 2 (alice,bob)", r.Users)
	}
	// level 1: /docs x1, /pricing x1 (count tie — name asc); alice's reload of / must NOT appear
	l1 := r.Levels[0].Steps
	if len(l1) != 2 || l1[0].Event != "/docs" || l1[1].Event != "/pricing" {
		t.Fatalf("level1 = %+v, want [/docs /pricing]", l1)
	}
	for _, s := range l1 {
		if s.Event == "/" {
			t.Fatal("a reload of the start page leaked into level 1")
		}
	}
	// level 2: /signup x1 (alice); bob stopped
	if r.Levels[1].Steps[0].Event != "/signup" || r.Levels[1].Steps[0].Count != 1 {
		t.Fatalf("level2 top = %+v, want /signup/1", r.Levels[1].Steps[0])
	}
}
