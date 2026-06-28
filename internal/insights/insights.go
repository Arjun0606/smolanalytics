// Package insights persists saved reports — the "pin this report" feature that
// turns ad-hoc Explore into a dashboard you open every morning. Config-shaped
// (small, mutable), so it's a single JSON file rewritten atomically on change,
// separate from the append-only event log.
package insights

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Insight is one saved report: a type (funnel|trend|breakdown|retention) plus the
// params Explore needs to re-run it.
type Insight struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Params  map[string]string `json:"params"`
	Created time.Time         `json:"created"`
}

// Store holds saved insights in memory and (when path != "") persists them.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Insight
}

// Open loads saved insights from path (empty/missing = start fresh). An empty path
// means in-memory only (used by the throwaway demo).
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
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("insights file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Insight {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Insight, len(s.items))
	copy(out, s.items)
	return out
}

var now = func() time.Time { return time.Now().UTC() }

// Save validates and stores an insight (assigning an id), then persists. Returns
// the stored copy.
func (s *Store) Save(in Insight) (Insight, error) {
	if in.Name == "" {
		return Insight{}, fmt.Errorf("name is required")
	}
	switch in.Type {
	case "funnel", "trend", "breakdown", "retention", "paths", "lifecycle", "stickiness", "groups":
	default:
		return Insight{}, fmt.Errorf("unknown report type %q", in.Type)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in.ID = newID()
	in.Created = now()
	if in.Params == nil {
		in.Params = map[string]string{}
	}
	s.items = append(s.items, in)
	if err := s.persist(); err != nil {
		s.items = s.items[:len(s.items)-1] // roll back
		return Insight{}, err
	}
	return in, nil
}

// Delete removes an insight by id (no error if it's already gone).
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Insight, 0, len(old))
	for _, it := range old {
		if it.ID != id {
			out = append(out, it)
		}
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return err
	}
	return nil
}

// persist writes the whole list via temp-file + rename (atomic). Caller holds lock.
func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
