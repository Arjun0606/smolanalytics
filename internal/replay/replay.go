// Package replay stores session recordings — and is the one part of this engine that is NOT a
// pure function of the event log.
//
// THE QUARANTINE IS THE DESIGN. Everything else here recomputes from raw events, which is what
// makes "click any number, get its rows plus a recomputation and a digest" possible and is the one
// property competitors structurally cannot copy. A recording is an opaque blob: it can never be
// aggregated, never recomputed, never proven. So it lives behind its own door and NOTHING may
// reach through it — no report, no chart, no digest, no MCP tool reads this package. A number that
// came from a recording would be the first number in this product nobody could check.
//
// The arithmetic that forced a second store, measured against the real code: a 10-minute rrweb
// session is ~1MB of JSON, and a large string property costs ~1.13x resident for the life of the
// process. Against a 204MB GOMEMLIMIT that is roughly 180 sessions EVER — not per day. Compressed
// and base64'd into properties it is ~1,275, still the entire budget before a single real event.
// Recordings in the event log would also be walked by the ~14 read paths that materialise all
// history, so a funnel query would be paging through last month's video.
package replay

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

const (
	// MaxChunkBytes bounds ONE uploaded chunk after compression. The real memory bound is chunk
	// size, not clever streaming: cap it here and even a naive whole-object read is bounded.
	MaxChunkBytes = 256 << 10
	// MaxSessionChunks stops one tab from recording forever. At ~6KB/min compressed this is
	// several hours, far past any session worth watching.
	MaxSessionChunks = 400
	// DefaultBudgetMB is the total disk this package may occupy before it evicts.
	DefaultBudgetMB = 200
	// DefaultRetainDays bounds age independently of size.
	DefaultRetainDays = 14
)

// Meta is one recorded session. Deliberately tiny, and deliberately NOT a second source of truth:
// duration, pages, rage clicks, entry path, browser and country are all already pure functions of
// the event log, and storing them again would create a second answer to "what happened in this
// visit" — exactly what this product refuses to have.
type Meta struct {
	SID     string    `json:"sid"`
	DID     string    `json:"did"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
	Chunks  int       `json:"chunks"`
	Bytes   int64     `json:"bytes"`
}

// Store keeps recordings in a blob backend with a small in-memory index.
//
// The index is a CACHE, not the truth: it is rebuildable by listing the blob prefix, so the
// "derive everything from the durable bytes" discipline holds one layer down too.
type Store struct {
	mu     sync.Mutex
	blobs  blob.Blob
	index  map[string]*Meta
	budget int64 // bytes
	retain time.Duration
	total  int64
	now    func() time.Time // injectable so retention is testable without sleeping
}

// Open builds a store over a blob backend. budgetMB<=0 and retainDays<=0 take the defaults.
func Open(b blob.Blob, budgetMB, retainDays int) (*Store, error) {
	if b == nil {
		return nil, fmt.Errorf("replay needs a blob backend")
	}
	if budgetMB <= 0 {
		budgetMB = DefaultBudgetMB
	}
	if retainDays <= 0 {
		retainDays = DefaultRetainDays
	}
	s := &Store{
		blobs:  b,
		index:  map[string]*Meta{},
		budget: int64(budgetMB) << 20,
		retain: time.Duration(retainDays) * 24 * time.Hour,
		now:    time.Now,
	}
	return s, s.rebuild()
}

// rebuild reconstructs the index from the durable bytes. Called at open, and the reason the index
// can be treated as disposable.
func (s *Store) rebuild() error {
	keys, err := s.blobs.List("replay/")
	if err != nil {
		return nil // an empty or missing prefix is not an error; it is a store with nothing in it
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if !bytes.HasSuffix([]byte(k), []byte("/m.json")) {
			continue
		}
		raw, err := s.blobs.Get(k)
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(raw, &m) != nil || m.SID == "" {
			continue
		}
		s.index[m.SID] = &m
		s.total += m.Bytes
	}
	return nil
}

// Append stores one chunk of a session's recording.
//
// Eviction runs BEFORE the write, not after. Evicting afterwards lets a burst overshoot the budget
// by the size of the burst, which on a small box is the difference between a bounded feature and
// an outage.
func (s *Store) Append(sid, did string, at time.Time, raw []byte) error {
	if sid == "" {
		return fmt.Errorf("a recording chunk needs a session id")
	}
	packed, err := deflate(raw)
	if err != nil {
		return err
	}
	if len(packed) > MaxChunkBytes {
		return fmt.Errorf("chunk is %d bytes compressed (max %d) — record in smaller slices",
			len(packed), MaxChunkBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m := s.index[sid]
	if m == nil {
		m = &Meta{SID: sid, DID: did, Started: at}
		s.index[sid] = m
	}
	if m.Chunks >= MaxSessionChunks {
		// Refuse rather than roll: a session that silently drops its middle plays back as a jump
		// cut, and nobody watching would know a piece was missing.
		return fmt.Errorf("session %s already has %d chunks (max %d)", sid, m.Chunks, MaxSessionChunks)
	}
	if err := s.evictForLocked(int64(len(packed)), sid); err != nil {
		return err
	}

	key := fmt.Sprintf("replay/%s/%06d.z", sid, m.Chunks)
	if err := s.blobs.Put(key, packed); err != nil {
		return err
	}
	m.Chunks++
	m.Bytes += int64(len(packed))
	m.Ended = at
	s.total += int64(len(packed))
	// The meta is rewritten per chunk because it is what rebuild() reads; losing it would orphan
	// the chunks. It is a few dozen bytes.
	metaRaw, _ := json.Marshal(m)
	return s.blobs.Put("replay/"+sid+"/m.json", metaRaw)
}

// evictForLocked frees room for `need` bytes, plus drops anything past the retention window.
// Whole sessions only, oldest first. Caller holds the lock.
func (s *Store) evictForLocked(need int64, keep string) error {
	cutoff := s.now().Add(-s.retain)
	var victims []*Meta
	for _, m := range s.index {
		if m.SID != keep && m.Ended.Before(cutoff) {
			victims = append(victims, m)
		}
	}
	for _, m := range victims {
		s.deleteLocked(m.SID)
	}
	if s.total+need <= s.budget {
		return nil
	}
	// Oldest by end time. A half-evicted session is a player that dies mid-playback, so the unit
	// is always the whole recording.
	var all []*Meta
	for _, m := range s.index {
		if m.SID != keep {
			all = append(all, m)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Ended.Before(all[j].Ended) })
	for _, m := range all {
		if s.total+need <= s.budget {
			break
		}
		s.deleteLocked(m.SID)
	}
	if s.total+need > s.budget {
		return fmt.Errorf("this chunk does not fit in the %dMB replay budget even after evicting "+
			"every other recording", s.budget>>20)
	}
	return nil
}

func (s *Store) deleteLocked(sid string) {
	m := s.index[sid]
	if m == nil {
		return
	}
	for i := 0; i < m.Chunks; i++ {
		s.blobs.Delete(fmt.Sprintf("replay/%s/%06d.z", sid, i))
	}
	s.blobs.Delete("replay/" + sid + "/m.json")
	s.total -= m.Bytes
	delete(s.index, sid)
}

// Delete removes one recording. Exported for the GDPR path: erasing a person must erase their
// recordings too, and a package the delete path cannot see is a package that leaks.
func (s *Store) Delete(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(sid)
}

// DeleteUser removes every recording belonging to one person.
func (s *Store) DeleteUser(did string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sids []string
	for sid, m := range s.index {
		if m.DID == did {
			sids = append(sids, sid)
		}
	}
	for _, sid := range sids {
		s.deleteLocked(sid)
	}
	return len(sids)
}

// Get returns a session's chunks decompressed and concatenated, in order.
func (s *Store) Get(sid string) ([]byte, *Meta, error) {
	s.mu.Lock()
	m := s.index[sid]
	if m == nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("no recording for session %s — it may have been evicted; "+
			"recordings are bounded by size and age, and the events for this visit are still in the log", sid)
	}
	cp := *m
	chunks := m.Chunks
	s.mu.Unlock()

	var out bytes.Buffer
	for i := 0; i < chunks; i++ {
		packed, err := s.blobs.Get(fmt.Sprintf("replay/%s/%06d.z", sid, i))
		if err != nil {
			// A missing middle chunk is reported, never silently skipped: a jump cut nobody is
			// told about is worse than an error, because the viewer draws conclusions from it.
			return nil, nil, fmt.Errorf("recording for %s is incomplete at chunk %d of %d", sid, i, chunks)
		}
		raw, err := inflate(packed)
		if err != nil {
			return nil, nil, fmt.Errorf("recording for %s is corrupt at chunk %d", sid, i)
		}
		out.Write(raw)
	}
	return out.Bytes(), &cp, nil
}

// List returns recorded sessions, newest first.
func (s *Store) List(limit int) []Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Meta, 0, len(s.index))
	for _, m := range s.index {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ended.After(out[j].Ended) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Usage reports what this package is costing, so the dashboard can show it rather than leaving
// someone to discover a 200MB directory.
func (s *Store) Usage() (bytes int64, sessions int, budget int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, len(s.index), s.budget
}

func deflate(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inflate(packed []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(packed))
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 8<<20)) // a bounded read; a crafted chunk cannot zip-bomb
}
