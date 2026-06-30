package file

import (
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
