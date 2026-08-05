package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The Store contract is "ingestion is idempotent on Event.ID so a retried request never
// double-counts", and the importer builds on it ("ids are kept, so re-importing is
// idempotent"). The dedup guard read s.seen, but s.seen was only written by index(), which
// ran AFTER the marshal loop — so two copies of an ID *inside one batch* both slipped
// through: POST /v1/events with the same event twice, an export whose $insert_id repeats,
// an SDK that retries a flush before clearing its queue. Measured 2 on this backend where
// the memory backend recorded 1, and the duplicate was durable: it survived restart and
// inflated every count, funnel step, trend bucket and revenue sum forever.
func TestIngestDedupesRepeatedIDsWithinOneBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	dup := event.Event{ID: "same", Name: "signup", DistinctID: "u1", Timestamp: ts}
	other := event.Event{ID: "other", Name: "signup", DistinctID: "u2", Timestamp: ts}

	if err := s.Ingest(dup, dup, other, dup); err != nil {
		t.Fatal(err)
	}
	if c := s.Count(); c != 2 {
		t.Fatalf("count=%d want 2 — a repeated id inside one batch was counted twice", c)
	}
	// and it must not be sitting in the log waiting to reappear on the next restart
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := s2.Count(); c != 2 {
		t.Fatalf("after reopen count=%d want 2 — the duplicate was written to the log", c)
	}

	// Cross-backend agreement: the same body behind the same handler must give the same
	// number whichever store.Store is configured. That divergence is what made this a
	// correctness bug rather than a wasted byte.
	m := memory.New()
	if err := m.Ingest(dup, dup, other, dup); err != nil {
		t.Fatal(err)
	}
	mem, err := m.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mem) != s2.Count() {
		t.Fatalf("memory backend says %d, file backend says %d for identical input", len(mem), s2.Count())
	}
}

// A crash (or an ENOSPC short write) leaves the log ending mid-record with no newline. Open
// skipped the unparseable tail but then opened the O_APPEND handle at the END of the file,
// so the next event was written directly onto the fragment and the two became one
// unparseable line. That event was fsynced, indexed, 202-ACKed and served by every query for
// the life of the process — and then vanished at the next restart (measured: 6 acked, 5 on
// disk). The segment tier's crash-safety argument rests on this WAL replaying completely, so
// the loss reached that tier too.
func TestAppendAfterTornTailRecoverySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		e := event.Event{ID: "e" + string(rune('0'+i)), Name: "ev", DistinctID: "u1", Timestamp: base.Add(time.Duration(i) * time.Second)}
		if err := s.Ingest(e); err != nil {
			t.Fatal(err)
		}
	}
	// the crash: abandon the store without Close, then tear the final record
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, fi.Size()-7); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("a torn tail must not prevent opening: %v", err)
	}
	recovered := s2.Count()
	if recovered < 3 {
		t.Fatalf("recovery kept %d of the 3 untouched events", recovered)
	}
	for _, id := range []string{"post-1", "post-2"} {
		if err := s2.Ingest(event.Event{ID: id, Name: "ev", DistinctID: "u1", Timestamp: base.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	s3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := s3.Count(); c != recovered+2 {
		t.Fatalf("after restart %d events, want %d — an acked post-recovery event was lost", c, recovered+2)
	}
	evs, err := s3.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range evs {
		have[e.ID] = true
	}
	for _, id := range []string{"post-1", "post-2"} {
		if !have[id] {
			t.Fatalf("%s was acked and served, then disappeared on restart (glued onto the torn tail)", id)
		}
	}
}
