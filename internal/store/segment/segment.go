// Package segment is the scale tier: a durable hot append-log that seals into immutable,
// time-bounded, compressed columnar segments on a Blob backend (local disk now, S3/R2
// next). Memory stays bounded no matter the total volume — a query streams one segment at
// a time and skips, unread, every segment whose time range can't match. This is the
// "billions of events, one binary, your data" path (see gtm/SCALE.md), and it satisfies
// the same store.Store the engine already computes over.
package segment

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
	"github.com/Arjun0606/smolanalytics/internal/store/file"
)

const manifestKey = "manifest.json"

// segMeta indexes one sealed segment so a query can skip it without a read.
type segMeta struct {
	Key   string    `json:"key"`
	Count int       `json:"count"`
	MinTS time.Time `json:"min_ts"`
	MaxTS time.Time `json:"max_ts"`
	Names []string  `json:"names"`
}

type Store struct {
	mu       sync.RWMutex
	hot      *file.Store // durable WAL for the open (unsealed) block
	blob     blob.Blob
	manifest []segMeta
	names    map[string]bool
	sealAt   int
}

// Open recovers the hot WAL at hotPath and the cold manifest from the blob backend.
// sealAt is how many events accumulate in the hot tier before being sealed to a segment.
func Open(hotPath string, b blob.Blob, sealAt int) (*Store, error) {
	hot, err := file.Open(hotPath)
	if err != nil {
		return nil, err
	}
	if sealAt <= 0 {
		sealAt = 50_000
	}
	s := &Store{hot: hot, blob: b, sealAt: sealAt, names: map[string]bool{}}

	if data, err := b.Get(manifestKey); err == nil {
		if err := json.Unmarshal(data, &s.manifest); err != nil {
			return nil, fmt.Errorf("segment: corrupt manifest: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, m := range s.manifest {
		for _, n := range m.Names {
			s.names[n] = true
		}
	}
	if hn, _ := hot.Names(); hn != nil {
		for _, n := range hn {
			s.names[n] = true
		}
	}
	// Crash-window recovery: a crash after a segment's manifest was persisted but before
	// the hot WAL was cleared leaves those events in BOTH places. If every hot event is
	// already in the last sealed segment, the seal completed — clear the hot WAL.
	if err := s.recoverHotLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) recoverHotLocked() error {
	if len(s.manifest) == 0 || s.hot.Count() == 0 {
		return nil
	}
	last := s.manifest[len(s.manifest)-1]
	data, err := s.blob.Get(last.Key)
	if err != nil {
		return nil
	}
	sealed, err := decodeSegment(data)
	if err != nil {
		return nil
	}
	ids := make(map[string]bool, len(sealed))
	for _, e := range sealed {
		if e.ID != "" {
			ids[e.ID] = true
		}
	}
	hot, _ := s.hot.Range(time.Time{}, time.Time{})
	for _, e := range hot {
		if e.ID == "" || !ids[e.ID] {
			return nil // hot has events not in the last segment — it's a genuine open block
		}
	}
	return s.hot.Clear() // every hot event already sealed; this was the crash window
}

func (s *Store) Ingest(evs ...event.Event) error {
	if err := s.hot.Ingest(evs...); err != nil {
		return err
	}
	s.mu.Lock()
	for _, e := range evs {
		s.names[e.Name] = true
	}
	full := s.hot.Count() >= s.sealAt
	s.mu.Unlock()
	if full {
		return s.seal()
	}
	return nil
}

// seal moves the hot block into an immutable cold segment. Ordering is crash-safe:
// write the segment, persist the manifest, THEN clear the hot WAL — so a crash never
// loses data (at worst it leaves a duplicate the next Open reconciles).
func (s *Store) seal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hot.Count() == 0 {
		return nil
	}
	evs, err := s.hot.Range(time.Time{}, time.Time{})
	if err != nil {
		return err
	}
	data, minTS, maxTS, names, err := encodeSegment(evs)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("seg/%010d.sms", len(s.manifest)) // seq key: stable across a crash-retry
	if err := s.blob.Put(key, data); err != nil {
		return err
	}
	s.manifest = append(s.manifest, segMeta{Key: key, Count: len(evs), MinTS: minTS, MaxTS: maxTS, Names: names})
	if err := s.persistManifestLocked(); err != nil {
		s.manifest = s.manifest[:len(s.manifest)-1]
		return err
	}
	return s.hot.Clear()
}

func (s *Store) persistManifestLocked() error {
	data, err := json.Marshal(s.manifest)
	if err != nil {
		return err
	}
	return s.blob.Put(manifestKey, data)
}

// Scan streams every event in [from, to) through fn, reading only the segments whose
// time range overlaps and the hot block — bounded memory regardless of total volume.
func (s *Store) Scan(from, to time.Time, fn func(event.Event) error) error {
	s.mu.RLock()
	segs := make([]segMeta, len(s.manifest))
	copy(segs, s.manifest)
	s.mu.RUnlock()

	for _, m := range segs {
		if !overlaps(m.MinTS, m.MaxTS, from, to) {
			continue // skip the whole segment unread — this is the scale win
		}
		data, err := s.blob.Get(m.Key)
		if err != nil {
			return err
		}
		evs, err := decodeSegment(data)
		if err != nil {
			return err
		}
		for _, e := range evs {
			if inRange(e.Timestamp, from, to) {
				if err := fn(e); err != nil {
					return err
				}
			}
		}
	}
	hot, err := s.hot.Range(from, to)
	if err != nil {
		return err
	}
	for _, e := range hot {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Range(from, to time.Time) ([]event.Event, error) {
	var out []event.Event
	err := s.Scan(from, to, func(e event.Event) error {
		out = append(out, e)
		return nil
	})
	return out, err
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

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.hot.Count()
	for _, m := range s.manifest {
		n += m.Count
	}
	return n
}

// Prune drops whole cold segments older than the cutoff (segment-granular retention) and
// prunes the hot block exactly. Returns the number of events removed.
func (s *Store) Prune(before time.Time) (int, error) {
	if before.IsZero() {
		return 0, nil
	}
	s.mu.Lock()
	removed := 0
	kept := s.manifest[:0:0]
	var drop []string
	for _, m := range s.manifest {
		if m.MaxTS.Before(before) {
			removed += m.Count
			drop = append(drop, m.Key)
			continue
		}
		kept = append(kept, m)
	}
	if len(drop) > 0 {
		s.manifest = kept
		if err := s.persistManifestLocked(); err != nil {
			s.mu.Unlock()
			return 0, err
		}
		for _, k := range drop {
			_ = s.blob.Delete(k)
		}
	}
	s.mu.Unlock()
	hn, err := s.hot.Prune(before)
	if err != nil {
		return removed, err
	}
	return removed + hn, nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.manifest {
		_ = s.blob.Delete(m.Key)
	}
	s.manifest = nil
	s.names = map[string]bool{}
	if err := s.persistManifestLocked(); err != nil {
		return err
	}
	return s.hot.Clear()
}

// Flush seals whatever is currently in the hot block (used on graceful shutdown so the
// newest events are durably columnar, and by tests).
func (s *Store) Flush() error { return s.seal() }

func (s *Store) Close() error {
	if err := s.Flush(); err != nil {
		return err
	}
	return s.hot.Close()
}

// overlaps reports whether a segment's [min,max] can contain any event in query [from,to).
func overlaps(min, max, from, to time.Time) bool {
	if !from.IsZero() && max.Before(from) {
		return false
	}
	if !to.IsZero() && !min.Before(to) {
		return false
	}
	return true
}

func inRange(ts, from, to time.Time) bool {
	if !from.IsZero() && ts.Before(from) {
		return false
	}
	if !to.IsZero() && !ts.Before(to) {
		return false
	}
	return true
}
