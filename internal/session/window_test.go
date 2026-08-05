package session

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A visit that straddles the days= cutoff starts LATER in the list than it does in the raw
// log: Sessions() drops the pre-cutoff events before splitting, One() did not. So the row
// carried a start_unix that did not exist in One()'s view and clicking it answered
// "session not found" — the product 404ing on a link it had just handed out.
func TestDetailResolvesASessionStraddlingTheWindowCutoff(t *testing.T) {
	edge := now().AddDate(0, 0, -7)
	evs := []event.Event{
		ev("bob", "$pageview", "/", edge.Add(-10*time.Minute), 0, 0),       // outside days=7
		ev("bob", "$pageview", "/pricing", edge.Add(10*time.Minute), 0, 0), // inside
		ev("bob", "$click", "/pricing", edge.Add(11*time.Minute), 120, 60), // inside
	}
	ss := Sessions(evs, 7, 0)
	if len(ss) != 1 {
		t.Fatalf("want 1 listed session, got %d", len(ss))
	}
	row := ss[0]
	if row.Events != 2 || row.EntryPath != "/pricing" {
		t.Fatalf("row = %+v, want the 2 in-window events entering on /pricing", row)
	}
	d, ok := One(evs, "bob", row.StartUnix)
	if !ok {
		t.Fatal("detail 404s on the start_unix the list just handed out")
	}
	// the detail must describe the SAME visit the row does, not a longer one
	if d.Events != row.Events || d.EntryPath != row.EntryPath || d.DurationSec != row.DurationSec {
		t.Fatalf("detail %+v disagrees with the row %+v it was opened from", d.Session, row)
	}
	if len(d.Steps) != 2 || d.Steps[0].Path != "/pricing" || d.Steps[0].T != 0 {
		t.Fatalf("playback must start at the handle: %+v", d.Steps)
	}
}

// The fallback must not turn every handle into a hit: a start that belongs to no event of
// this user is still a 404, because inventing a session is worse than admitting the miss.
func TestDetailStillMissesOnAnUnknownHandle(t *testing.T) {
	base := now().Add(-time.Hour)
	evs := []event.Event{ev("a", "$pageview", "/", base, 0, 0)}
	if _, ok := One(evs, "a", base.Add(90*time.Second).Unix()); ok {
		t.Fatal("a handle matching no event must not resolve")
	}
	if _, ok := One(evs, "nobody", base.Unix()); ok {
		t.Fatal("another user's handle must not resolve")
	}
}
