// Package settings holds the operational config every real product needs —
// project name, timezone, managed API keys, and the session-signing secret —
// persisted as a single JSON file (atomic rewrite), separate from event data.
package settings

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// APIKey is a managed ingestion key (in addition to any env-configured key).
type APIKey struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Key     string    `json:"key"`
	Created time.Time `json:"created"`
}

type data struct {
	ProjectName string   `json:"project_name"`
	Timezone    string   `json:"timezone"`
	Secret      string   `json:"secret"` // HMAC key for session cookies
	Keys        []APIKey `json:"keys"`
}

// Store is the concurrency-safe, persisted settings.
type Store struct {
	mu   sync.Mutex
	path string
	d    data
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, d: data{ProjectName: "My project", Timezone: "UTC"}}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if len(b) > 0 {
			if err := json.Unmarshal(b, &s.d); err != nil {
				return nil, fmt.Errorf("settings file corrupt: %w", err)
			}
		}
	}
	if s.d.Secret == "" {
		s.d.Secret = newToken(32) // generated once, persisted, so sessions survive restarts
		_ = s.persist()
	}
	return s, nil
}

func (s *Store) ProjectName() string { s.mu.Lock(); defer s.mu.Unlock(); return s.d.ProjectName }
func (s *Store) Timezone() string    { s.mu.Lock(); defer s.mu.Unlock(); return s.d.Timezone }
func (s *Store) Secret() string      { s.mu.Lock(); defer s.mu.Unlock(); return s.d.Secret }

func (s *Store) Keys() []APIKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]APIKey, len(s.d.Keys))
	copy(out, s.d.Keys)
	return out
}

// ValidKey reports whether key matches any managed key, compared in constant time
// (and without early-exit) to avoid leaking which/how-many keys exist via timing.
func (s *Store) ValidKey(key string) bool {
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := false
	for _, k := range s.d.Keys {
		if subtle.ConstantTimeCompare([]byte(k.Key), []byte(key)) == 1 {
			ok = true
		}
	}
	return ok
}

func (s *Store) UpdateProject(name, tz string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	on, ot := s.d.ProjectName, s.d.Timezone
	if name != "" {
		s.d.ProjectName = name
	}
	if tz != "" {
		s.d.Timezone = tz
	}
	if err := s.persist(); err != nil {
		s.d.ProjectName, s.d.Timezone = on, ot // roll back so memory matches disk
		return err
	}
	return nil
}

func (s *Store) AddKey(name string) (APIKey, error) {
	if name == "" {
		name = "default"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := APIKey{ID: newToken(6), Name: name, Key: "sa_" + newToken(20), Created: time.Now().UTC()}
	s.d.Keys = append(s.d.Keys, k)
	if err := s.persist(); err != nil {
		s.d.Keys = s.d.Keys[:len(s.d.Keys)-1] // roll back
		return APIKey{}, err
	}
	return k, nil
}

func (s *Store) RevokeKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.d.Keys
	out := make([]APIKey, 0, len(old)) // fresh slice — don't mutate the backing array
	for _, k := range old {
		if k.ID != id {
			out = append(out, k)
		}
	}
	s.d.Keys = out
	if err := s.persist(); err != nil {
		s.d.Keys = old // roll back so a revoked key can't resurrect on restart
		return err
	}
	return nil
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil { // 0600 — holds keys + secret
		return err
	}
	return os.Rename(tmp, s.path)
}

func newToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
