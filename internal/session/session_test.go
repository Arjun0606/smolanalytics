package session

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(id, name, path string, at time.Time, x, y int) event.Event {
	props := map[string]any{}
	if path != "" {
		props["path"] = path
	}
	if x > 0 {
		props["x"] = float64(x)
		props["y"] = float64(y)
		props["vw"] = float64(1000)
	}
	return event.Event{Name: name, DistinctID: id, Timestamp: at, Properties: props}
}

func TestSessionsSplitByGap(t *testing.T) {
	base := now().Add(-2 * time.Hour)
	evs := []event.Event{
		ev("a", "$pageview", "/", base, 0, 0),
		ev("a", "$click", "/", base.Add(time.Minute), 100, 40),
		ev("a", "$pageview", "/pricing", base.Add(41*time.Minute), 0, 0), // 40-min gap → new session
	}
	ss := Sessions(evs, 30, 0)
	if len(ss) != 2 {
		t.Fatalf("want 2 sessions (split on 40-min gap), got %d", len(ss))
	}
	if ss[0].EntryPath != "/pricing" { // newest first
		t.Fatalf("newest session entry = %q, want /pricing", ss[0].EntryPath)
	}
	var first Session
	for _, s := range ss {
		if s.EntryPath == "/" {
			first = s
		}
	}
	if first.Events != 2 || first.Pages != 1 {
		t.Fatalf("first session events=%d pages=%d, want 2/1", first.Events, first.Pages)
	}
}

func TestSessionOneReconstructs(t *testing.T) {
	base := now().Add(-1 * time.Hour)
	evs := []event.Event{
		ev("a", "$pageview", "/", base, 0, 0),
		ev("a", "$click", "/", base.Add(5*time.Second), 100, 40),
	}
	ss := Sessions(evs, 30, 0)
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	d, ok := One(evs, "a", ss[0].StartUnix)
	if !ok {
		t.Fatal("should reconstruct the session")
	}
	if len(d.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(d.Steps))
	}
	if d.Steps[0].T != 0 {
		t.Fatalf("first step t = %d, want 0", d.Steps[0].T)
	}
	if d.Steps[1].X != 100 || d.Steps[1].Name != "$click" {
		t.Fatalf("second step wrong: %+v", d.Steps[1])
	}
}
