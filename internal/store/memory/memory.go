// Package memory is an in-memory store.Store: enough to run the full engine in
// tests and the zero-setup CLI demo. Concurrency-safe. The DuckDB backend
// satisfies the same interface for production columnar speed.
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

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = map[string]bool{}
	s.evs = nil
	s.names = map[string]bool{}
	return nil
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
