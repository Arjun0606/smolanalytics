package file

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(id, name string) event.Event {
	return event.Event{ID: id, Name: name, DistinctID: "u1", Timestamp: time.Now().UTC()}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "events.jsonl")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Ingest(ev("e1", "signup"), ev("e2", "checkout")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen — events should replay from disk
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Count() != 2 {
		t.Fatalf("after reopen count = %d, want 2", s2.Count())
	}
	names, _ := s2.Names()
	if len(names) != 2 || names[0] != "checkout" || names[1] != "signup" {
		t.Fatalf("names = %v, want [checkout signup]", names)
	}
}

func TestPruneCompactsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, _ := Open(path)
	old := event.Event{ID: "old", Name: "x", DistinctID: "u1", Timestamp: time.Now().UTC().AddDate(0, 0, -40)}
	recent := event.Event{ID: "new", Name: "y", DistinctID: "u2", Timestamp: time.Now().UTC()}
	_ = s.Ingest(old, recent)

	n, err := s.Prune(time.Now().UTC().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || s.Count() != 1 {
		t.Fatalf("pruned %d, count %d; want pruned 1, count 1", n, s.Count())
	}
	s.Close()

	// reopen — the compacted log must contain only the recent event
	s2, _ := Open(path)
	defer s2.Close()
	if s2.Count() != 1 {
		t.Fatalf("after reopen count = %d, want 1 (compaction persisted)", s2.Count())
	}
	names, _ := s2.Names()
	if len(names) != 1 || names[0] != "y" {
		t.Fatalf("names = %v, want [y] (pruned event's name gone)", names)
	}
}

func TestIdempotentAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s1, _ := Open(path)
	_ = s1.Ingest(ev("dup", "x"))
	_ = s1.Ingest(ev("dup", "x")) // same ID, same process
	if s1.Count() != 1 {
		t.Fatalf("count = %d, want 1 (dedup)", s1.Count())
	}
	s1.Close()

	// reopen and re-ingest the same ID — still deduped against replayed state
	s2, _ := Open(path)
	defer s2.Close()
	_ = s2.Ingest(ev("dup", "x"))
	if s2.Count() != 1 {
		t.Fatalf("after reopen count = %d, want 1 (dedup vs replay)", s2.Count())
	}
}

func TestMaxEventsCapBoundsMemoryAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "events.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMaxEvents(100); err != nil {
		t.Fatal(err)
	}
	// flood well past the cap + slack (slack floor is 1000)
	for i := 0; i < 3000; i++ {
		if err := s.Ingest(ev("", "pageview")); err != nil {
			t.Fatal(err)
		}
	}
	// add a unique newest event we can look for after trimming
	if err := s.Ingest(event.Event{ID: "newest", Name: "checkout", DistinctID: "u1", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if c := s.Count(); c > 100+1000 { // never exceeds cap + slack band
		t.Fatalf("cap not enforced: resident=%d, want <= ~1100", c)
	}
	// the newest event must survive the trim (we keep the most-recent)
	evs, _ := s.Range(time.Time{}, time.Time{})
	found := false
	for _, e := range evs {
		if e.ID == "newest" {
			found = true
		}
	}
	if !found {
		t.Fatal("newest event was dropped by the cap — should keep the most recent")
	}
	persisted := s.Count()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// reopen: the compacted on-disk log must match the resident set (bounded, durable)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != persisted {
		t.Fatalf("reopen mismatch: disk=%d resident-before=%d", s2.Count(), persisted)
	}
	_ = s2.Close()
}

// A log larger than the cap must not be fully resident at boot.
//
// THE BUG: SetMaxEvents used to run AFTER Open returned, so boot loaded the entire log and only
// then trimmed. The moment a log outgrew the box, the process OOM-killed during Open on every
// restart, forever — and SMOLANALYTICS_MAX_EVENTS, the setting whose entire purpose is surviving
// that, was read after the crash point. A cap unreachable by the condition it exists for.
func TestOpenCappedBoundsMemoryDuringReplayNotAfterIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")

	// Write more events than the cap. 12000 against a cap of 500 clears the 1000-event slack band
	// several times over, so the trim must actually have run more than once.
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < 12000; i++ {
		if err := s.Ingest(event.Event{
			ID: fmt.Sprintf("e%d", i), Name: "pageview", DistinctID: "u1",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	reopened, err := OpenCapped(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	// Bounded, not exact: the trim fires on the slack band, so the resident count sits between the
	// cap and cap+slack. What matters is that it is nowhere near 12000.
	got := reopened.Count()
	if got > 500+1000 {
		t.Fatalf("resident count %d — the replay was not bounded, which is the whole bug", got)
	}
	if got < 500 {
		t.Fatalf("resident count %d is below the cap — the trim threw away too much", got)
	}

	// It must keep the NEWEST, matching enforceCapLocked's positional semantics. If it kept the
	// oldest, a capped instance would show a dashboard frozen in the past.
	evs, err := reopened.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.ID != "e11999" {
		t.Fatalf("newest resident event is %q, want e11999 — the trim kept the wrong end", last.ID)
	}
}

// An uncapped Open must behave exactly as it always did.
func TestOpenWithoutACapKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "all.log")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		s.Ingest(event.Event{ID: fmt.Sprintf("e%d", i), Name: "e", DistinctID: "u", Timestamp: time.Now().UTC()})
	}
	s.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if n := reopened.Count(); n != 3000 {
		t.Fatalf("uncapped Open kept %d of 3000 — it must not bound anything", n)
	}
}
