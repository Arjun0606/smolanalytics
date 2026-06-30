// Package file is a durable store.Store: an append-only JSONL event log that
// replays into memory on open. Pure Go, zero dependencies — the single static
// binary stays single and static, and events survive restarts. The append log is
// the source of truth; queries are served from the in-memory index (same speed as
// the memory store). A columnar backend (DuckDB) can slot in behind the same
// interface later for scale; this is the smol, always-correct default.
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
	mu    sync.RWMutex
	seen  map[string]bool
	evs   []event.Event
	names map[string]bool
	w     *os.File
	path  string // the log file path, for atomic compaction (write-temp-then-rename)
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
	return s.w.Sync() // durable before the caller treats the event as accepted
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

// Prune drops events older than the cutoff and compacts the log (rewrites it with
// only the kept events). Runs under the write lock; O(n) but only on the retention
// schedule.
func (s *Store) Prune(before time.Time) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return 0, errors.New("store is closed")
	}
	kept := s.evs[:0:0]
	seen, names := map[string]bool{}, map[string]bool{}
	var buf []byte
	removed := 0
	for _, e := range s.evs {
		if e.Timestamp.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
		if e.ID != "" {
			seen[e.ID] = true
		}
		names[e.Name] = true
		b, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if removed == 0 {
		return 0, nil
	}
	// Atomic compaction: write the kept set to a temp file, fsync it durable, then
	// rename it over the live log. The original is never truncated until the new file
	// is safely on disk, so a Sync failure (ENOSPC/EIO) or a crash mid-compaction can
	// never lose the kept events — on restart you see either the old log or the new
	// one, both complete. (The old in-place truncate could leave an empty/torn file.)
	tmp := s.path + ".compact"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	if _, err := tf.Write(buf); err != nil {
		tf.Close()
		os.Remove(tmp)
		return 0, err
	}
	if err := tf.Sync(); err != nil {
		tf.Close()
		os.Remove(tmp)
		return 0, err
	}
	if err := tf.Close(); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	// Close the append handle, swap files atomically, then reopen on the new log.
	if err := s.w.Close(); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		s.w, _ = os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // keep store usable
		os.Remove(tmp)
		return 0, err
	}
	// fsync the directory so the rename itself survives a crash.
	if d, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	w, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	s.w = w
	s.evs, s.seen, s.names = kept, seen, names
	return removed, nil
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
