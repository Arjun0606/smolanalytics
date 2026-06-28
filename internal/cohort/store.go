package cohort

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Store persists cohort definitions to a JSON file (atomic rewrite), like saved
// reports. Empty path = in-memory only.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Definition
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
			return nil, fmt.Errorf("cohorts file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Definition, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Definition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.items {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}

var now = func() time.Time { return time.Now().UTC() }

func (s *Store) Save(d Definition) (Definition, error) {
	if d.Name == "" {
		return Definition{}, fmt.Errorf("name is required")
	}
	if len(d.Events) == 0 && len(d.Filters) == 0 {
		return Definition{}, fmt.Errorf("a cohort needs at least one event or filter")
	}
	if d.Match != "all" {
		d.Match = "any"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d.ID = newID()
	d.Created = now()
	s.items = append(s.items, d)
	if err := s.persist(); err != nil {
		s.items = s.items[:len(s.items)-1] // roll back
		return Definition{}, err
	}
	return d, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Definition, 0, len(old))
	for _, d := range old {
		if d.ID != id {
			out = append(out, d)
		}
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return err
	}
	return nil
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
