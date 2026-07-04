// Package exportlink mints one-time download links for the full raw event export
// (GET /export/<token>) — "give me my data" from the editor without dumping
// millions of rows through a conversation. Same token discipline as
// internal/share: 128-bit random tokens stored ONLY as sha256 hashes (a leaked
// sidecar file can't mint access). Stricter lifecycle than a share link, because
// this is the whole dataset: every link expires after an hour and burns on its
// first download.
package exportlink

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// TTL is how long an unused link stays downloadable.
const TTL = time.Hour

// Link is one pending export grant (the raw token is returned once at creation,
// never stored).
type Link struct {
	ID      string    `json:"id"`
	Format  string    `json:"format"` // csv | jsonl
	Hash    string    `json:"hash"`   // sha256(token), hex
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
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
			return nil, fmt.Errorf("export-links file corrupt: %w", err)
		}
	}
	return s, nil
}

// Create mints a one-time link for a format ("" defaults to jsonl) and returns it
// with the RAW token — shown once, never stored.
func (s *Store) Create(format string, now time.Time) (Link, string, error) {
	switch format {
	case "":
		format = "jsonl"
	case "csv", "jsonl":
	default:
		return Link{}, "", fmt.Errorf(`format must be "csv" or "jsonl", got %q`, format)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Link{}, "", err
	}
	token := hex.EncodeToString(raw)
	h := sha256.Sum256([]byte(token))
	l := Link{
		ID:      "ex" + hex.EncodeToString(raw[:6]),
		Format:  format,
		Hash:    hex.EncodeToString(h[:]),
		Created: now,
		Expires: now.Add(TTL),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now) // housekeeping: dead links don't pile up in the sidecar file
	s.items = append(s.items, l)
	return l, token, s.persistLocked()
}

// Redeem burns the link for a presented token and returns its format. ok is false
// for unknown, expired, and already-used tokens alike — callers must not give a
// prober a way to tell those apart. The burn happens before the download starts,
// so a link is single-use even if the transfer then fails.
func (s *Store) Redeem(token string, now time.Time) (format string, ok bool) {
	if len(token) != 32 {
		return "", false
	}
	h := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(h[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, l := range s.items {
		if l.Hash != want {
			continue
		}
		s.items = append(s.items[:i], s.items[i+1:]...) // burn: single-use
		_ = s.persistLocked()                           // in-memory burn already holds for this process
		if now.After(l.Expires) {
			return "", false
		}
		return l.Format, true
	}
	return "", false
}

// pruneLocked drops expired links. Callers hold s.mu.
func (s *Store) pruneLocked(now time.Time) {
	kept := s.items[:0]
	for _, l := range s.items {
		if !now.After(l.Expires) {
			kept = append(kept, l)
		}
	}
	s.items = kept
}

func (s *Store) persistLocked() error {
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
