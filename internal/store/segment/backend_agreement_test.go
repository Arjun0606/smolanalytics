package segment

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// store.Store's contract is "ingestion is idempotent on Event.ID so a retried request never
// double-counts", and the same HTTP handler sits in front of whichever backend an instance is
// configured with. So the identical body has to produce the identical number on all three, or
// the answer depends on the storage tier — which is the one thing this product may not do.
//
// The scale tier gets its own case because it dedupes through its hot WAL rather than by
// itself: the file backend used to let a repeated id INSIDE one batch through (s.seen was
// written by index(), which ran after the marshal loop), so the duplicate reached the hot log,
// sealed into an immutable segment, and inflated every count and revenue sum for good. This
// asserts the batch is clean before as well as after the seal.
func TestBatchDedupAgreesAcrossBackends(t *testing.T) {
	ts := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	batch := []event.Event{
		mk("same", "signup", "u1", ts, nil),
		mk("same", "signup", "u1", ts, nil),
		mk("other", "signup", "u2", ts, nil),
		mk("same", "signup", "u1", ts, nil),
	}

	mem := memory.New()
	if err := mem.Ingest(batch...); err != nil {
		t.Fatal(err)
	}
	memEvs, err := mem.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	b, err := blob.NewLocal(filepath.Join(dir, "cold"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "hot.data"), b, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(batch...); err != nil {
		t.Fatal(err)
	}
	if got := s.Count(); got != len(memEvs) {
		t.Fatalf("segment backend counted %d for a batch the memory backend counted %d at", got, len(memEvs))
	}
	// once sealed the segment is immutable, so a duplicate that got this far is permanent
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != len(memEvs) {
		t.Fatalf("after sealing the segment holds %d events, memory holds %d — the duplicate is now immutable", len(sealed), len(memEvs))
	}
	ids := map[string]int{}
	for _, e := range sealed {
		ids[e.ID]++
	}
	for id, n := range ids {
		if n != 1 {
			t.Fatalf("id %s is in the sealed segment %d times", id, n)
		}
	}
}
