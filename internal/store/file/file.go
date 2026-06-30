// Package file is a durable store.Store: an append-only JSONL event log that
// replays into memory on open. Pure Go, zero dependencies — the single static
// binary stays single and static, and events survive restarts. The append log is
// the source of truth; queries are served from the in-memory index (same speed as
// the memory store). This is the smol, always-correct default for a single box; the
// columnar segment tier (internal/store/segment) sits behind the same interface for scale.
package file

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

type Store struct {
	mu        sync.RWMutex
	seen      map[string]bool
	evs       []event.Event
	names     map[string]bool
	w         *os.File
	path      string // the log file path, for atomic compaction (write-temp-then-rename)
	maxEvents int    // 0 = unlimited; >0 keeps only the newest N resident (OOM guardrail)
}

// Open replays the log at path (creating it and any parent dirs if absent) and
// returns a store ready to append. Corrupt trailing lines are skipped, not fatal.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	s := &Store{seen: map[string]bool{}, names: map[string]bool{}, path: path}

	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var e event.Event
			if json.Unmarshal(line, &e) != nil {
				continue // skip a torn/partial line rather than refuse to start
			}
			s.index(e)
		}
		err := sc.Err()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // holds PII
	if err != nil {
		return nil, err
	}
	s.w = w
	return s, nil
}

// index records an event in memory (no disk write). Caller holds no lock during
// Open (single-threaded); Ingest holds the write lock.
func (s *Store) index(e event.Event) {
	if e.ID != "" {
		s.seen[e.ID] = true
	}
	s.evs = append(s.evs, e)
	s.names[e.Name] = true
}

func (s *Store) Ingest(events ...event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return errors.New("store is closed")
	}
	// Marshal all new events into one buffer first, then a single append + fsync,
	// then index — so a batch is all-or-nothing on disk (no half-written batch that
	// a client retry would partially duplicate) and is durable before we ack.
	var buf []byte
	toIndex := events[:0:0]
	for _, e := range events {
		if e.ID != "" && s.seen[e.ID] {
			continue // idempotent: a retried event is neither re-logged nor re-counted
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
		toIndex = append(toIndex, e)
	}
	if len(buf) == 0 {
		return nil
	}
	if _, err := s.w.Write(buf); err != nil {
		return err
	}
	for _, e := range toIndex {
		s.index(e)
	}
	if err := s.w.Sync(); err != nil { // durable before the caller treats the event as accepted
		return err
	}
	return s.enforceCapLocked() // keep memory bounded so a flood can't OOM the box
}

func (s *Store) Range(from, to time.Time) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]event.Event, 0, len(s.evs))
	for _, e := range s.evs {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && !e.Timestamp.Before(to) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) Scan(from, to time.Time, fn func(event.Event) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.evs {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && !e.Timestamp.Before(to) {
			continue
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Names() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Clear truncates the event log and resets the in-memory index.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = map[string]bool{}
	s.evs = nil
	s.names = map[string]bool{}
	if s.w == nil {
		return nil
	}
	if err := s.w.Truncate(0); err != nil {
		return err
	}
	_, err := s.w.Seek(0, 0)
	return err
}

// Prune drops events older than the cutoff and compacts the log. Runs under the write
// lock; O(n) but only on the retention schedule.
func (s *Store) Prune(before time.Time) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return 0, errors.New("store is closed")
	}
	kept := make([]event.Event, 0, len(s.evs))
	removed := 0
	for _, e := range s.evs {
		if e.Timestamp.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := s.compactToLocked(kept); err != nil {
		return 0, err
	}
	return removed, nil
}

// SetMaxEvents bounds how many of the most-recent events stay resident (0 = unlimited).
// This is the guardrail that keeps a small instance alive under a flood of ingest
// instead of OOM-crashing it; it trims immediately if already over. Pair it with
// retention (RetainDays) to also bound the on-disk log.
func (s *Store) SetMaxEvents(n int) error {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxEvents = n
	return s.enforceCapLocked()
}

// enforceCapLocked drops the oldest events down to maxEvents and compacts the log when
// resident count exceeds the cap (plus a slack band, so we rewrite on growth rather than
// on every event). Caller holds the write lock.
func (s *Store) enforceCapLocked() error {
	if s.maxEvents <= 0 || s.w == nil {
		return nil
	}
	slack := s.maxEvents / 10
	if slack < 1000 {
		slack = 1000
	}
	if len(s.evs) <= s.maxEvents+slack {
		return nil
	}
	over := len(s.evs) - s.maxEvents
	return s.compactToLocked(s.evs[over:]) // keep the newest maxEvents
}

// compactToLocked atomically rewrites the log to contain exactly `kept` (in order),
// rebuilds the in-memory index, and swaps it in. Write the kept set to a temp file,
// fsync it durable, then rename it over the live log — the original is never truncated
// until the new file is safely on disk, so a Sync failure (ENOSPC/EIO) or a crash
// mid-compaction can never lose the kept events (on restart you see either the old log
// or the new one, both complete). Caller holds the write lock.
func (s *Store) compactToLocked(kept []event.Event) error {
	seen, names := make(map[string]bool, len(kept)), map[string]bool{}
	var buf []byte
	for _, e := range kept {
		if e.ID != "" {
			seen[e.ID] = true
		}
		names[e.Name] = true
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	tmp := s.path + ".compact"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := tf.Write(buf); err != nil {
		tf.Close()
		os.Remove(tmp)
		return err
	}
	if err := tf.Sync(); err != nil {
		tf.Close()
		os.Remove(tmp)
		return err
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := s.w.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		s.w, _ = os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // keep store usable
		os.Remove(tmp)
		return err
	}
	// fsync the directory so the rename itself survives a crash.
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	w, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	s.w = w
	// copy into a fresh slice so we never alias the old backing array (kept may be a
	// sub-slice of s.evs when called from the cap path).
	ne := make([]event.Event, len(kept))
	copy(ne, kept)
	s.evs, s.seen, s.names = ne, seen, names
	return nil
}

// Count is the number of events held (handy for "is this store empty").
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.evs)
}

// Close flushes and closes the append handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return nil
	}
	_ = s.w.Sync()
	err := s.w.Close()
	s.w = nil
	return err
}
