// Package acted is the outcome ledger: which findings a human said they acted on, and when.
//
// This is the missing half of the loop. The investigator produces findings; recovery detection
// notices when a metric comes back; and until this store there was no way to connect the two —
// "it recovered" and "you fixed it and THEN it recovered" are different sentences, and only the
// second one proves the tool changed what someone did with their week.
//
// Deliberately tiny. One JSON file beside the event log, like the alert and webhook stores; an
// entry is a fingerprint, a timestamp, and an optional note. No states beyond "acted" — a
// workflow engine with open/in-progress/blocked is a project tracker, and the operator already
// has one of those. The single bit this records is the one bit nothing else can: a human read
// the finding and did something about it.
package acted

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Entry is one acted-on finding.
type Entry struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
	Note        string    `json:"note,omitempty"`
}

// Store persists entries. Same shape and persistence discipline as the alert store.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Entry
}

// Open loads or creates the ledger at path. An empty path is the demo convention shared by every
// sidecar store: fully in-memory, nothing written to disk — marks work for the session and vanish.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.items); err != nil {
		// A corrupt ledger must not take the instance down; it holds annotations, not events.
		// Starting empty loses the annotations and keeps the product up, which is the right
		// trade for a sidecar. The unreadable file is preserved by the next persist writing
		// clean JSON over it — the event log itself is untouched either way.
		s.items = nil
	}
	return s, nil
}

// Mark records that the finding was acted on. Idempotent: marking twice keeps the FIRST
// timestamp, because "when did a human first act" is the number the verified-recovery line is
// computed from, and a re-click must not rewrite history.
func (s *Store) Mark(fingerprint, note string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.items {
		if e.Fingerprint == fingerprint {
			return e, nil
		}
	}
	e := Entry{Fingerprint: fingerprint, At: time.Now().UTC(), Note: note}
	s.items = append(s.items, e)
	return e, s.persist()
}

// Get returns the entry for a fingerprint, if a human acted on it.
func (s *Store) Get(fingerprint string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.items {
		if e.Fingerprint == fingerprint {
			return e, true
		}
	}
	return Entry{}, false
}

// Lookup returns a snapshot function suitable for investigate.ApplyActed — a plain func so the
// investigate package needs no dependency on this one.
func (s *Store) Lookup() func(string) (time.Time, bool) {
	return func(fp string) (time.Time, bool) {
		e, ok := s.Get(fp)
		return e.At, ok
	}
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil // in-memory (demo): the mark holds for the process lifetime only
	}
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
