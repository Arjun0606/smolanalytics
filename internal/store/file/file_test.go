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
