package flag

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Store persists flags to a JSON file (atomic tmp+rename), same discipline as the cohort and
// deploy stores. Flags are keyed by their stable Key (e.g. "checkout_v2"), so Save is an upsert:
// creating or updating the flag with that key. Empty path = in-memory only.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Flag
}

var now = func() time.Time { return time.Now().UTC() }

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
			return nil, fmt.Errorf("flags file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Flag {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Flag, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(key string) (Flag, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.items {
		if f.Key == key {
			return f, true
		}
	}
	return Flag{}, false
}

// Save upserts by Key. A new key is created (Created stamped); an existing key is updated in
// place (Created preserved, Updated bumped). Validates the key and the variant weights.
// Save persists a flag. Plan changes go through the lock.
//
// exposedUsers is unknown here — this store holds flags, not events — so the error message cannot
// say how many people are already in the experiment. Callers that DO know should use
// SaveWithExposure, which produces the message an operator can act on. The LOCK itself is
// enforced either way; only the wording degrades.
func (s *Store) Save(f Flag) (Flag, error) { return s.SaveWithExposure(f, 0) }

// SaveWithExposure is Save with the exposure count, for the error message and for the layer rule.
//
// This is the enforcement point experiment.go's own comment promised — "attach it to a Flag and
// Store.Save enforces the lock" — and which nothing implemented. A pre-registered plan that can be
// silently re-aimed after the data arrives is not pre-registration, it is a comment. Verified by
// changing a locked plan's goal through the MCP tool and watching it succeed.
func (s *Store) SaveWithExposure(f Flag, exposedUsers int) (Flag, error) {
	if f.Key == "" {
		return Flag{}, fmt.Errorf("flag key is required")
	}
	if prev, ok := s.Get(f.Key); ok || f.Experiment != nil {
		var oldPlan *Experiment
		var oldVariants []Variant
		if ok {
			oldPlan, oldVariants = prev.Experiment, prev.Variants
		}
		if err := ValidatePlanChange(f.Key, oldPlan, f.Experiment, oldVariants, f.Variants, exposedUsers); err != nil {
			return Flag{}, err
		}
	}
	for _, r := range f.Rules {
		if r.RolloutPct < 0 || r.RolloutPct > 100 {
			return Flag{}, fmt.Errorf("rollout_pct must be 0..100, got %d", r.RolloutPct)
		}
	}
	if len(f.Variants) > 0 {
		total := 0
		for _, v := range f.Variants {
			if v.Key == "" {
				return Flag{}, fmt.Errorf("each variant needs a key")
			}
			if v.Weight > 0 {
				total += v.Weight
			}
		}
		if total <= 0 {
			return Flag{}, fmt.Errorf("variants need at least one positive weight")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	f.Updated = now()
	found := false
	next := make([]Flag, len(old))
	copy(next, old)
	for i := range next {
		if next[i].Key == f.Key {
			f.Created = next[i].Created // preserve original creation time on update
			next[i] = f
			found = true
			break
		}
	}
	if !found {
		f.Created = f.Updated
		next = append(next, f)
	}
	s.items = next
	if err := s.persist(); err != nil {
		s.items = old // roll back so memory matches disk
		return Flag{}, err
	}
	return f, nil
}

// SetEnabled toggles a flag on/off by key (the common flip). Returns the updated flag. A future
// increment records this flip as a deploy marker so its impact is measured automatically.
func (s *Store) SetEnabled(key string, on bool) (Flag, error) {
	f, ok := s.Get(key)
	if !ok {
		return Flag{}, fmt.Errorf("flag %q not found", key)
	}
	f.Enabled = on
	return s.Save(f)
}

// Delete removes a flag by key. found is true only when a flag actually went away,
// so callers can report "nothing was deleted" instead of implying a removal that
// never happened. Deleting a key that isn't there is not an error (retries are fine).
func (s *Store) Delete(key string) (found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Flag, 0, len(old))
	for _, f := range old {
		if f.Key != key {
			out = append(out, f)
		} else {
			found = true
		}
	}
	if !found {
		return false, nil // nothing changed, no need to rewrite the file
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old
		return false, err
	}
	return true, nil
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
