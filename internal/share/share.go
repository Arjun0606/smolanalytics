// Package share issues revocable read-only share links — show your traffic to a
// cofounder or investor without giving them a login. Tokens are 128-bit random,
// stored ONLY as sha256 hashes (a leaked sidecar file can't mint access), and scope
// to the web overview: no actions, no settings, no raw events.
package share

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Link is one share grant (the raw token is returned once at creation, never stored).
type Link struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"` // who it's for, e.g. "investor update"
	Hash    string    `json:"hash"` // sha256(token), hex
	Created time.Time `json:"created"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	items []Link
}

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
			return nil, fmt.Errorf("share-links file corrupt: %w", err)
		}
	}
	return s, nil
}

// Create mints a link and returns it with the RAW token — shown once, never stored.
func (s *Store) Create(name string) (Link, string, error) {
	if strings.TrimSpace(name) == "" {
		return Link{}, "", fmt.Errorf("give the link a name (who it's for) so it's identifiable when revoking")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Link{}, "", err
	}
	token := hex.EncodeToString(raw)
	h := sha256.Sum256([]byte(token))
	l := Link{
		ID:      "sh" + hex.EncodeToString(raw[:6]),
		Name:    name,
		Hash:    hex.EncodeToString(h[:]),
		Created: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, l)
	return l, token, s.persist()
}

// Verify reports whether a presented token matches any live link.
func (s *Store) Verify(token string) bool {
	if len(token) != 32 {
		return false
	}
	h := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(h[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.items {
		if l.Hash == want {
			return true
		}
	}
	return false
}

func (s *Store) List() []Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Link, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, l := range s.items {
		if l.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("no share link with id %q", id)
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
