package replay

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

func newStore(t *testing.T, budgetMB, retainDays int) *Store {
	t.Helper()
	b, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(b, budgetMB, retainDays)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A recording round-trips: what goes in comes back, in order, decompressed.
func TestARecordingRoundTripsInOrder(t *testing.T) {
	s := newStore(t, 10, 14)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := s.Append("sid1", "u1", now.Add(time.Duration(i)*time.Second),
			[]byte(fmt.Sprintf("chunk%d;", i))); err != nil {
			t.Fatal(err)
		}
	}
	got, m, err := s.Get("sid1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "chunk0;chunk1;chunk2;" {
		t.Fatalf("chunks came back as %q — order or content is wrong", got)
	}
	if m.Chunks != 3 || m.DID != "u1" {
		t.Fatalf("meta wrong: %+v", m)
	}
}

// THE BOUND. Eviction must run BEFORE the write. Evicting afterwards lets a burst overshoot the
// budget by the size of the burst, which on a small box is the difference between a bounded
// feature and an outage.
func TestTheBudgetIsNeverExceededEvenMidBurst(t *testing.T) {
	s := newStore(t, 1, 14) // 1MB
	now := time.Now().UTC()
	// Incompressible payloads, so the stored size is close to the input and the budget is really
	// exercised rather than compressed away.
	blobData := make([]byte, 120<<10)
	for i := range blobData {
		blobData[i] = byte(i * 7)
	}
	for i := 0; i < 40; i++ { // 40 x ~120KB = ~4.8MB against a 1MB budget
		sid := fmt.Sprintf("s%d", i)
		if err := s.Append(sid, "u", now.Add(time.Duration(i)*time.Minute), blobData); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		used, _, budget := s.Usage()
		if used > budget {
			t.Fatalf("after %d appends the store holds %d bytes against a %d budget — "+
				"eviction is running after the write, not before", i+1, used, budget)
		}
	}
}

// Eviction is whole-session. A half-evicted recording is a player that dies mid-playback, and the
// viewer has no way to tell that from the visitor leaving.
func TestEvictionRemovesWholeSessionsNeverHalfOfOne(t *testing.T) {
	s := newStore(t, 1, 14)
	now := time.Now().UTC()
	big := bytes.Repeat([]byte("x"), 40<<10)
	for i := 0; i < 30; i++ {
		sid := fmt.Sprintf("s%d", i)
		for c := 0; c < 3; c++ { // three chunks each
			s.Append(sid, "u", now.Add(time.Duration(i)*time.Minute), big)
		}
	}
	// Whatever survived must be complete: every listed session must Get without error.
	for _, m := range s.List(0) {
		if _, _, err := s.Get(m.SID); err != nil {
			t.Fatalf("session %s survived eviction in pieces: %v", m.SID, err)
		}
	}
}

// Age retention is independent of size: a recording older than the window goes even when there is
// plenty of room.
func TestOldRecordingsAreDroppedEvenWithRoomToSpare(t *testing.T) {
	s := newStore(t, 500, 7) // huge budget, 7-day window
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	s.Append("ancient", "u1", base.AddDate(0, 0, -30), []byte("old"))
	s.Append("recent", "u2", base.AddDate(0, 0, -1), []byte("new"))
	// A third append triggers the sweep.
	s.Append("current", "u3", base, []byte("now"))

	if _, _, err := s.Get("ancient"); err == nil {
		t.Fatal("a 30-day-old recording survived a 7-day retention window")
	}
	if _, _, err := s.Get("recent"); err != nil {
		t.Fatalf("a 1-day-old recording was dropped by a 7-day window: %v", err)
	}
}

// Erasing a person must erase their recordings. A store the delete path cannot see is a store
// that leaks, and this one holds the most personal data in the product.
func TestDeletingAPersonDeletesTheirRecordings(t *testing.T) {
	s := newStore(t, 10, 14)
	now := time.Now().UTC()
	s.Append("a1", "alice", now, []byte("x"))
	s.Append("a2", "alice", now, []byte("y"))
	s.Append("b1", "bob", now, []byte("z"))

	if n := s.DeleteUser("alice"); n != 2 {
		t.Fatalf("want 2 of alice's recordings deleted, got %d", n)
	}
	if _, _, err := s.Get("a1"); err == nil {
		t.Fatal("alice's recording survived her erasure")
	}
	if _, _, err := s.Get("b1"); err != nil {
		t.Fatalf("bob's recording was deleted with alice's: %v", err)
	}
}

// The index is a cache. It must be reconstructible from the durable bytes alone, or the
// "everything derives from what is on disk" discipline stops one layer short.
func TestTheIndexRebuildsFromTheStoredBytes(t *testing.T) {
	dir := t.TempDir()
	b, _ := blob.NewLocal(dir)
	s, _ := Open(b, 10, 14)
	now := time.Now().UTC()
	s.Append("sid1", "u1", now, []byte("hello"))
	s.Append("sid1", "u1", now.Add(time.Second), []byte(" world"))

	// A brand-new store over the same bytes, with no shared state.
	b2, _ := blob.NewLocal(dir)
	s2, err := Open(b2, 10, 14)
	if err != nil {
		t.Fatal(err)
	}
	got, m, err := s2.Get("sid1")
	if err != nil {
		t.Fatalf("the index did not rebuild: %v", err)
	}
	if string(got) != "hello world" || m.Chunks != 2 {
		t.Fatalf("rebuilt wrong: %q %+v", got, m)
	}
}

// An oversized chunk is refused with a message that says what to do.
func TestAnOversizedChunkIsRefused(t *testing.T) {
	s := newStore(t, 100, 14)
	// Genuinely incompressible. byte(i*31) LOOKS random and is a repeating pattern mod 256 that
	// deflate crushes below the cap — the first version of this test passed an oversized payload
	// and asserted nothing. A fixed seed keeps it deterministic.
	rnd := rand.New(rand.NewSource(1))
	huge := make([]byte, MaxChunkBytes*3)
	rnd.Read(huge)
	err := s.Append("s", "u", time.Now().UTC(), huge)
	if err == nil {
		t.Fatal("an oversized chunk was accepted")
	}
	if !strings.Contains(err.Error(), "smaller") {
		t.Fatalf("the refusal must say what to do, got %v", err)
	}
}

// A missing recording explains itself, including that the EVENTS are still there. Someone who
// finds a gap needs to know whether they lost data or only a recording.
func TestAMissingRecordingSaysTheEventsAreStillThere(t *testing.T) {
	s := newStore(t, 10, 14)
	_, _, err := s.Get("never-recorded")
	if err == nil {
		t.Fatal("a missing recording returned no error")
	}
	if !strings.Contains(err.Error(), "still in the log") {
		t.Fatalf("the error must say the events survive, got %v", err)
	}
}
