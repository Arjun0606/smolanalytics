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
