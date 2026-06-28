// Package alert defines threshold alerts on event metrics — "fire when <event>
// count over the last N hours is above/below a threshold". The Server evaluates
// them on a schedule and delivers via webhooks; this package is the persisted
// rules + their runtime state.
package alert

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Alert is one rule plus its last-evaluation state.
type Alert struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Event       string    `json:"event"`
	Op          string    `json:"op"` // "gt" | "lt"
	Threshold   float64   `json:"threshold"`
	WindowHours int       `json:"window_hours"`
	Enabled     bool      `json:"enabled"`
	Created     time.Time `json:"created"`
	LastChecked time.Time `json:"last_checked"`
	LastValue   float64   `json:"last_value"`
	LastFired   time.Time `json:"last_fired"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	items []Alert
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
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("alerts file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Alert, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Add(a Alert) (Alert, error) {
	if a.Name == "" || a.Event == "" {
		return Alert{}, fmt.Errorf("name and event are required")
	}
	if a.Op != "gt" && a.Op != "lt" {
		return Alert{}, fmt.Errorf("op must be gt or lt")
	}
	if a.WindowHours <= 0 {
		a.WindowHours = 24
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a.ID = token(6)
	a.Created = time.Now().UTC()
	a.Enabled = true
	s.items = append(s.items, a)
	if err := s.persist(); err != nil {
		s.items = s.items[:len(s.items)-1]
		return Alert{}, err
	}
	return a, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Alert, 0, len(old))
	for _, a := range old {
		if a.ID != id {
			out = append(out, a)
		}
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old
		return err
	}
	return nil
}

// SetChecked records an evaluation result (and a fire time, if it fired).
func (s *Store) SetChecked(id string, value float64, fired bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].LastValue = value
			s.items[i].LastChecked = at
			if fired {
				s.items[i].LastFired = at
			}
			_ = s.persist()
			return
		}
	}
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
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

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
