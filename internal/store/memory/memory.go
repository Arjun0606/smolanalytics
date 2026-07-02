// Package memory is an in-memory store.Store: enough to run the full engine in
// tests and the zero-setup CLI demo. Concurrency-safe. The tiered segment store
// (columnar, object-store-backed) satisfies the same interface for scale.
package memory

import (
	"sort"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

type Store struct {
	mu    sync.RWMutex
	seen  map[string]bool
	evs   []event.Event
	names map[string]bool
}

func New() *Store {
	return &Store{seen: map[string]bool{}, names: map[string]bool{}}
}

func (s *Store) Ingest(events ...event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		if e.ID != "" {
			if s.seen[e.ID] {
				continue // idempotent: never double-count a retried event
			}
			s.seen[e.ID] = true
		}
		s.evs = append(s.evs, e)
		s.names[e.Name] = true
	}
	return nil
}

func (s *Store) Range(from, to time.Time) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]event.Event, 0, len(s.evs))
	for _, e := range s.evs {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && !e.Timestamp.Before(to) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) Scan(from, to time.Time, fn func(event.Event) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.evs {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && !e.Timestamp.Before(to) {
			continue
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = map[string]bool{}
	s.evs = nil
	s.names = map[string]bool{}
	return nil
}

func (s *Store) Prune(before time.Time) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.evs[:0:0]
	seen, names := map[string]bool{}, map[string]bool{}
	removed := 0
	for _, e := range s.evs {
		if e.Timestamp.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
		if e.ID != "" {
			seen[e.ID] = true
		}
		names[e.Name] = true
	}
	s.evs, s.seen, s.names = kept, seen, names
	return removed, nil
}

func (s *Store) Names() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// DeleteUser erases every event for one distinct_id (GDPR erasure).
func (s *Store) DeleteUser(distinctID string) (int, error) {
	if distinctID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.evs[:0:0]
	seen, names := map[string]bool{}, map[string]bool{}
	removed := 0
	for _, e := range s.evs {
		if e.DistinctID == distinctID {
			removed++
			continue
		}
		kept = append(kept, e)
		if e.ID != "" {
			seen[e.ID] = true
		}
		names[e.Name] = true
	}
	s.evs, s.seen, s.names = kept, seen, names
	return removed, nil
}
