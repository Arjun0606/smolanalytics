package survey

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Store persists surveys to a JSON file (atomic tmp+rename), same discipline as the cohort/flag
// stores. Surveys are id-keyed (Save assigns a random id on create), so an empty path = in-memory.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Survey
}

var now = func() time.Time { return time.Now().UTC() }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
			return nil, fmt.Errorf("surveys file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Survey {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Survey, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Survey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.items {
		if v.ID == id {
			return v, true
		}
	}
	return Survey{}, false
}

// Save creates (empty ID → new random id) or updates (existing ID) a survey, validating it first.
func (s *Store) Save(sv Survey) (Survey, error) {
	if sv.Name == "" {
		return Survey{}, fmt.Errorf("survey name is required")
	}
	switch sv.Type {
	case "nps", "rating", "choice", "text":
	default:
		return Survey{}, fmt.Errorf("type must be nps, rating, choice, or text (got %q)", sv.Type)
	}
	if sv.Question == "" {
		return Survey{}, fmt.Errorf("a survey needs a question")
	}
	if sv.Type == "choice" && len(sv.Choices) == 0 {
		return Survey{}, fmt.Errorf("a choice survey needs at least one choice")
	}
	if sv.SamplePct < 0 || sv.SamplePct > 100 {
		return Survey{}, fmt.Errorf("sample_pct must be 0..100")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	sv.Updated = now()
	next := make([]Survey, len(old))
	copy(next, old)
	if sv.ID != "" {
		found := false
		for i := range next {
			if next[i].ID == sv.ID {
				sv.Created = next[i].Created
				next[i] = sv
				found = true
				break
			}
		}
		if !found {
			return Survey{}, fmt.Errorf("survey %q not found", sv.ID)
		}
	} else {
		sv.ID = newID()
		sv.Created = sv.Updated
		next = append(next, sv)
	}
	s.items = next
	if err := s.persist(); err != nil {
		s.items = old
		return Survey{}, err
	}
	return sv, nil
}

func (s *Store) SetActive(id string, on bool) (Survey, error) {
	sv, ok := s.Get(id)
	if !ok {
		return Survey{}, fmt.Errorf("survey %q not found", id)
	}
	sv.Active = on
	return s.Save(sv)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Survey, 0, len(old))
	for _, v := range old {
		if v.ID != id {
			out = append(out, v)
		}
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old
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
