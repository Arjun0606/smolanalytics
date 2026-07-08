// Package alias joins anonymous pre-login activity to the logged-in user —
// identity stitching. The SDK sends the anonymous id as a breadcrumb on
// identify(); we record anon→canonical here, and a Store decorator rewrites ids
// at read time so funnels, retention, and journeys survive the login boundary.
// GDPR erasure fans out across a user's aliases, so "delete user u123" also
// erases their pre-login trail.
package alias

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store"
)

type Map struct {
	mu   sync.RWMutex
	path string
	m    map[string]string // anon id -> canonical (logged-in) id
}

func Open(p string) (*Map, error) {
	a := &Map{path: p, m: map[string]string{}}
	if p == "" {
		return a, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return a, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &a.m); err != nil {
			return nil, fmt.Errorf("aliases file corrupt: %w", err)
		}
	}
	return a, nil
}

// Add records anon→canonical. Guards: the shared cookieless sentinel, empty ids,
// self-aliases, and canonical ids that are themselves aliased (one hop only — a
// chain would make resolution order-dependent).
func (a *Map) Add(anon, canonical string) error {
	anon, canonical = strings.TrimSpace(anon), strings.TrimSpace(canonical)
	if anon == "" || canonical == "" || anon == canonical || anon == "$anon" {
		return nil // nothing to stitch
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if tgt, ok := a.m[canonical]; ok && tgt != "" {
		canonical = tgt // collapse to the existing canonical, never build chains
	}
	a.m[anon] = canonical
	return a.persistLocked()
}

// RecordFrom records an identity edge from an identity event, so the HTTP ingest path
// and the import path stitch the same way and can never drift. It understands both
// conventions PostHog (and our own SDK) emit:
//   - $identify: the logged-in event carries the visitor's prior anonymous id in
//     $anon_distinct_id → Add(anon, user).
//   - $create_alias: merges a second id into the person, carried in the alias property
//     → Add(alias, user). PostHog uses this for its own person merges, so importing a
//     PostHog export with these events reconstructs its stitched identities instead of
//     splitting one human into two "users" (which silently corrupts retention/funnels).
//
// Any other event is ignored. A nil map is a no-op so callers needn't guard.
func RecordFrom(a *Map, e event.Event) {
	if a == nil {
		return
	}
	switch e.Name {
	case "$identify":
		if prev, ok := e.Properties["$anon_distinct_id"].(string); ok {
			_ = a.Add(prev, e.DistinctID)
		}
	case "$create_alias":
		if al, ok := e.Properties["alias"].(string); ok {
			_ = a.Add(al, e.DistinctID)
		}
	}
}

// Resolve returns the canonical id for any id (identity for unaliased ids).
func (a *Map) Resolve(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if c, ok := a.m[id]; ok {
		return c
	}
	return id
}

// AliasesOf returns every id that resolves to canonical (excluding itself).
func (a *Map) AliasesOf(canonical string) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []string
	for anon, c := range a.m {
		if c == canonical {
			out = append(out, anon)
		}
	}
	return out
}

// Forget removes every alias touching id (called after erasure).
func (a *Map) Forget(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for anon, c := range a.m {
		if anon == id || c == id {
			delete(a.m, anon)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return a.persistLocked()
}

func (a *Map) Clear() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m = map[string]string{}
	return a.persistLocked()
}

func (a *Map) persistLocked() error {
	if a.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(a.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// Store decorates a store.Store with read-time canonicalization: every event
// comes back under its canonical id, so the engine computes joined journeys
// without the storage layer knowing aliases exist.
type Store struct {
	store.Store
	aliases *Map
}

func Wrap(s store.Store, a *Map) *Store { return &Store{Store: s, aliases: a} }

func (s *Store) canon(e event.Event) event.Event {
	if c := s.aliases.Resolve(e.DistinctID); c != e.DistinctID {
		e.DistinctID = c
	}
	return e
}

func (s *Store) Range(from, to time.Time) ([]event.Event, error) {
	evs, err := s.Store.Range(from, to)
	if err != nil {
		return nil, err
	}
	for i := range evs {
		evs[i] = s.canon(evs[i])
	}
	return evs, nil
}

func (s *Store) Scan(from, to time.Time, fn func(event.Event) error) error {
	return s.Store.Scan(from, to, func(e event.Event) error { return fn(s.canon(e)) })
}

// DeleteUser erases the canonical id AND every alias pointing at it — the GDPR
// request must take the pre-login trail with it — then forgets the aliases.
func (s *Store) DeleteUser(id string) (int, error) {
	canonical := s.aliases.Resolve(id)
	total, err := s.Store.DeleteUser(canonical)
	if err != nil {
		return total, err
	}
	for _, anon := range s.aliases.AliasesOf(canonical) {
		n, err := s.Store.DeleteUser(anon)
		total += n
		if err != nil {
			return total, err
		}
	}
	if id != canonical { // asked via an alias — erase that id's own rows too
		n, err := s.Store.DeleteUser(id)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, s.aliases.Forget(canonical)
}

func (s *Store) Clear() error {
	if err := s.Store.Clear(); err != nil {
		return err
	}
	return s.aliases.Clear()
}
