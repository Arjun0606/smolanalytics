// Package trackplan stores the intended instrumentation — the events (and their
// properties) an app MEANS to track. Declared once (usually by the coding agent that
// wired the tracking), then compared against what actually arrives, so "I instrumented
// signup/activate/checkout" becomes verifiable: which planned events flow, which never
// arrived, which properties are missing. The missing half of agent-driven analytics.
package trackplan

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// PlannedEvent is one event the app intends to send.
type PlannedEvent struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Properties  []string `json:"properties,omitempty"` // property keys expected on this event
}

// Plan is the whole declared instrumentation, replaced atomically on set.
type Plan struct {
	Events  []PlannedEvent `json:"events"`
	Updated time.Time      `json:"updated"`
}

type Store struct {
	mu   sync.Mutex
	path string
	plan Plan
}

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
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.plan); err != nil {
			return nil, fmt.Errorf("tracking-plan file corrupt: %w", err)
		}
	}
	return s, nil
}

// Get returns the current plan (empty Events = no plan declared yet).
func (s *Store) Get() Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.plan
	out.Events = make([]PlannedEvent, len(s.plan.Events))
	copy(out.Events, s.plan.Events)
	return out
}

// Set replaces the plan wholesale — the declaring agent owns the full picture.
func (s *Store) Set(events []PlannedEvent) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan = Plan{Events: events, Updated: time.Now().UTC()}
	return s.plan, s.persist()
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.plan, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
