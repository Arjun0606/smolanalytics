// Package segment is the scale tier: a durable hot append-log that seals into immutable,
// time-bounded, compressed columnar segments on a Blob backend (local disk now, S3/R2
// next). Memory stays bounded no matter the total volume — a query streams one segment at
// a time and skips, unread, every segment whose time range can't match. This is the
// "billions of events, one binary, your data" path (see gtm/SCALE.md), and it satisfies
// the same store.Store the engine already computes over.
package segment

import (
	"bytes"
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
	seq      int // next segment sequence number — monotonic, NEVER derived from len(manifest)
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
		if err := s.loadManifest(data); err != nil {
			return nil, err
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
	// Restore the sequence counter from the highest existing key. Deriving keys from
	// len(manifest) was a data-loss bug: after Prune shrinks the manifest, a new seal
	// could reuse a LIVE segment's key and overwrite it. Monotonic seq can't collide.
	for _, m := range s.manifest {
		var n int
		if _, err := fmt.Sscanf(m.Key, "seg/%010d.sms", &n); err == nil && n >= s.seq {
			s.seq = n + 1
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
	// Only the crash window looks like this: the hot WAL holds EXACTLY the last sealed
	// segment (same count) and every hot ID is in it. Requiring an exact count match (not
	// just a subset) means a genuine new open block — which will differ in size — is never
	// falsely cleared, even if some of its IDs happen to collide with the last segment.
	if s.hot.Count() != last.Count {
		return nil
	}
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
			return nil // a genuine open block — keep it
		}
	}
	return s.hot.Clear() // hot is exactly the last sealed segment: the crash window
}

func (s *Store) Ingest(evs ...event.Event) error {
	// Hold s.mu around the whole append+seal so a concurrent seal's Range→Clear can't
	// wipe an event that landed between the snapshot and the clear. (file.Store.Ingest
	// already serializes its own appends; this also serializes them against sealing.)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.hot.Ingest(evs...); err != nil {
		return err
	}
	for _, e := range evs {
		s.names[e.Name] = true
	}
	if s.hot.Count() >= s.sealAt {
		return s.sealLocked()
	}
	return nil
}

// seal moves the hot block into an immutable cold segment. Caller must NOT hold s.mu.
func (s *Store) seal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sealLocked()
}

// sealLocked is seal's body; the caller holds s.mu. Ordering is crash-safe: write the
// segment, persist the manifest, THEN clear the hot WAL — so a crash never loses data
// (at worst it leaves a duplicate the next Open reconciles).
func (s *Store) sealLocked() error {
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
	key := fmt.Sprintf("seg/%010d.sms", s.seq)
	if err := s.blob.Put(key, data); err != nil {
		return err
	}
	s.manifest = append(s.manifest, segMeta{Key: key, Count: len(evs), MinTS: minTS, MaxTS: maxTS, Names: names})
	if err := s.persistManifestLocked(); err != nil {
		s.manifest = s.manifest[:len(s.manifest)-1]
		return err
	}
	s.seq++
	return s.hot.Clear()
}

// manifestEnvelope is the on-blob manifest format. The explicit version field is
// what lets a future format change fail loudly ("manifest v3 needs a newer binary")
// instead of silently misreading — add a compat fixture whenever v bumps.
type manifestEnvelope struct {
	V        int       `json:"v"`
	Segments []segMeta `json:"segments"`
}

const manifestVersion = 1

// loadManifest accepts both formats: v0 legacy (a bare JSON array, written before
// versioning existed) and the v1+ envelope. Sniffed by the first non-space byte, so
// every existing install upgrades in place with no migration step.
func (s *Store) loadManifest(data []byte) error {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' { // legacy v0: bare []segMeta
		if err := json.Unmarshal(data, &s.manifest); err != nil {
			return fmt.Errorf("segment: corrupt manifest: %w", err)
		}
		return nil
	}
	var env manifestEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("segment: corrupt manifest: %w", err)
	}
	if env.V > manifestVersion {
		return fmt.Errorf("segment: manifest is v%d but this binary understands up to v%d — upgrade smolanalytics", env.V, manifestVersion)
	}
	s.manifest = env.Segments
	return nil
}

func (s *Store) persistManifestLocked() error {
	data, err := json.Marshal(manifestEnvelope{V: manifestVersion, Segments: s.manifest})
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
		old := s.manifest
		s.manifest = kept
		if err := s.persistManifestLocked(); err != nil {
			s.manifest = old // roll back so in-memory matches what's still on disk
			s.mu.Unlock()
			return 0, err
		}
		// manifest no longer references these — a failed Delete just orphans a blob
		// (harmless, unreferenced), never a dangling reference.
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

// DeleteUser erases every event for one distinct_id (GDPR erasure) across both
// tiers. Sealed segments are immutable, so each affected segment is rewritten:
// decode → filter → seal the kept events under a fresh key → swap the manifest
// entry → delete the old blob. The manifest is persisted once at the end with
// rollback, so a failure can orphan an unreferenced blob but never dangle a
// reference or half-delete a user.
func (s *Store) DeleteUser(distinctID string) (int, error) {
	if distinctID == "" {
		return 0, nil
	}
	s.mu.Lock()
	removed := 0
	old := make([]segMeta, len(s.manifest))
	copy(old, s.manifest)
	var dropBlobs []string
	changed := false

	for i, m := range s.manifest {
		data, err := s.blob.Get(m.Key)
		if err != nil {
			s.manifest = old
			s.mu.Unlock()
			return 0, fmt.Errorf("segment %s: %w", m.Key, err)
		}
		evs, err := decodeSegment(data)
		if err != nil {
			s.manifest = old
			s.mu.Unlock()
			return 0, fmt.Errorf("segment %s: %w", m.Key, err)
		}
		kept := evs[:0:0]
		for _, e := range evs {
			if e.DistinctID == distinctID {
				removed++
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == len(evs) {
			continue // user not in this segment
		}
		changed = true
		dropBlobs = append(dropBlobs, m.Key)
		if len(kept) == 0 {
			s.manifest[i].Count = 0 // marked for removal below
			continue
		}
		nd, minTS, maxTS, names, err := encodeSegment(kept)
		if err != nil {
			s.manifest = old
			s.mu.Unlock()
			return 0, err
		}
		nk := fmt.Sprintf("seg/%010d.sms", s.seq)
		if err := s.blob.Put(nk, nd); err != nil {
			s.manifest = old
			s.mu.Unlock()
			return 0, err
		}
		s.seq++
		s.manifest[i] = segMeta{Key: nk, Count: len(kept), MinTS: minTS, MaxTS: maxTS, Names: names}
	}

	if changed {
		final := s.manifest[:0:0]
		for _, m := range s.manifest {
			if m.Count > 0 {
				final = append(final, m)
			}
		}
		s.manifest = final
		if err := s.persistManifestLocked(); err != nil {
			s.manifest = old // rollback — rewritten blobs are orphaned, references stay valid
			s.mu.Unlock()
			return 0, err
		}
		for _, k := range dropBlobs {
			_ = s.blob.Delete(k) // unreferenced now — failure just orphans a blob
		}
		// names may have shrunk — rebuild from what remains
		s.names = map[string]bool{}
		for _, m := range s.manifest {
			for _, n := range m.Names {
				s.names[n] = true
			}
		}
		if hn, _ := s.hot.Names(); hn != nil {
			for _, n := range hn {
				s.names[n] = true
			}
		}
	}
	s.mu.Unlock()

	hr, err := s.hot.DeleteUser(distinctID)
	if err != nil {
		return removed, err
	}
	return removed + hr, nil
}

// VerifyReport is what Verify/scrub found. Problems make the instance unhealthy;
// orphans are expected debris (failed deletes leave unreferenced blobs by design).
type VerifyReport struct {
	Segments  int      `json:"segments"`
	Events    int      `json:"events"`     // decoded total across segments
	HotEvents int      `json:"hot_events"` // events in the open hot block
	Orphans   []string `json:"orphans"`    // blobs under seg/ not in the manifest (warning only)
	Problems  []string `json:"problems"`   // corruption / mismatches — any entry means unhealthy
}

// Verify checks the invariants the design doc promises: every manifest entry Gets
// from the blob backend, decodes (CRC), and matches its recorded count and time
// range; the hot WAL is readable; unreferenced blobs are reported as orphans.
func (s *Store) Verify() VerifyReport {
	s.mu.RLock()
	segs := make([]segMeta, len(s.manifest))
	copy(segs, s.manifest)
	s.mu.RUnlock()

	var r VerifyReport
	r.Segments = len(segs)
	referenced := map[string]bool{}
	for _, m := range segs {
		referenced[m.Key] = true
		data, err := s.blob.Get(m.Key)
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: referenced by manifest but unreadable: %v", m.Key, err))
			continue
		}
		evs, err := decodeSegment(data)
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: corrupt: %v", m.Key, err))
			continue
		}
		r.Events += len(evs)
		if len(evs) != m.Count {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: manifest says %d events, segment holds %d", m.Key, m.Count, len(evs)))
		}
		for _, e := range evs {
			if e.Timestamp.Before(m.MinTS) || e.Timestamp.After(m.MaxTS) {
				r.Problems = append(r.Problems, fmt.Sprintf("%s: event %s outside manifest time range", m.Key, e.ID))
				break
			}
		}
	}
	if keys, err := s.blob.List("seg/"); err == nil {
		for _, k := range keys {
			if !referenced[k] {
				r.Orphans = append(r.Orphans, k)
			}
		}
	} else {
		r.Problems = append(r.Problems, fmt.Sprintf("listing blobs: %v", err))
	}
	if evs, err := s.hot.Range(time.Time{}, time.Time{}); err == nil {
		r.HotEvents = len(evs)
	} else {
		r.Problems = append(r.Problems, fmt.Sprintf("hot log unreadable: %v", err))
	}
	return r
}

// Scrub is Verify plus housekeeping: deletes the orphaned blobs Verify found
// (they're unreferenced by design after a failed delete — safe to remove).
func (s *Store) Scrub() (VerifyReport, int) {
	r := s.Verify()
	deleted := 0
	for _, k := range r.Orphans {
		if err := s.blob.Delete(k); err == nil {
			deleted++
		}
	}
	return r, deleted
}
