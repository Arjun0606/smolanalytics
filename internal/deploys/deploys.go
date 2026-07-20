// Package deploys records deployment markers — a timestamped point ("this shipped
// then") you overlay on any metric to answer the one question every other analytics
// tool leaves you guessing: did that deploy move the number? Markers are cheap to
// record (a git sha + message from CI, or a named release by hand); the impact math
// lives in impact.go and is the same trends engine the dashboard renders, so the
// answer is computed, never guessed, and a CI test pins it to the dashboard.
package deploys

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// Deploy is one recorded release. Identity is the git SHA when present (so re-recording
// the same commit upserts instead of duplicating — the cloud syncs every commit on each
// dashboard load); a marker without a SHA is a distinct manual entry.
type Deploy struct {
	ID      string    `json:"id"`
	SHA     string    `json:"sha,omitempty"`
	Message string    `json:"message,omitempty"`
	Author  string    `json:"author,omitempty"`
	Ref     string    `json:"ref,omitempty"`
	URL     string    `json:"url,omitempty"`
	Source  string    `json:"source,omitempty"` // github | ci | cli | manual
	At      time.Time `json:"at"`               // when it shipped (the marker time)
	Created time.Time `json:"created"`          // when it was recorded here
}

// Store is a small persisted list of deploy markers. Same shape + persistence as the
// goal / share stores: JSON on disk, atomic tmp+rename, one mutex, List returns a copy.
type Store struct {
	mu    sync.Mutex
	path  string
	items []Deploy
}

// Open loads the store from p, or returns an empty store if the file doesn't exist.
// An empty path means in-memory only (demo mode writes nothing to the visitor's disk).
func Open(p string) (*Store, error) {
	s := &Store{path: p}
	if p == "" {
		return s, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("deploys file corrupt: %w", err)
		}
	}
	return s, nil
}

// List returns all markers newest-first (a copy — never the internal slice).
func (s *Store) List() []Deploy {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Deploy, len(s.items))
	copy(out, s.items)
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Record adds a marker, upserting by SHA: recording the same commit again refreshes its
// fields (message/url/at) but keeps its id + first-recorded time, so the cloud can safely
// re-sync every commit on each load without ever creating duplicates.
func (s *Store) Record(d Deploy) (Deploy, error) {
	if d.At.IsZero() {
		d.At = time.Now().UTC()
	} else {
		d.At = d.At.UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.SHA != "" {
		for i := range s.items {
			if s.items[i].SHA == d.SHA {
				d.ID, d.Created = s.items[i].ID, s.items[i].Created
				s.items[i] = d
				return d, s.persist()
			}
		}
	}
	d.ID = fmt.Sprintf("dp%x", time.Now().UnixNano())
	d.Created = time.Now().UTC()
	s.items = append(s.items, d)
	return d, s.persist()
}

// Delete removes a marker by id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.items {
		if d.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("no deploy with id %q", id)
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
