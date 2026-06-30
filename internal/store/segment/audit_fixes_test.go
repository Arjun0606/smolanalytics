package segment

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

// #5: a corrupt/truncated segment must be a clean error, not silently-decoded garbage.
func TestCorruptSegmentRejected(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		mk("e1", "signup", "u1", base, map[string]any{"plan": "pro"}),
		mk("e2", "checkout", "u2", base.Add(time.Hour), nil),
	}
	data, _, _, _, err := encodeSegment(evs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSegment(data); err != nil {
		t.Fatalf("clean segment should decode: %v", err)
	}
	// flip a byte in the middle of the compressed blob
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)/2] ^= 0xFF
	if _, err := decodeSegment(corrupt); err == nil {
		t.Fatal("corrupt segment decoded without error — checksum not catching corruption")
	}
	// a truncated blob must also error, not panic
	if _, err := decodeSegment(data[:len(data)/2]); err == nil {
		t.Fatal("truncated segment decoded without error")
	}
}

// #1: recovery must NOT clear a genuine new hot block, even if one of its IDs collides
// with the last sealed segment (different count => not the crash window).
func TestRecoveryKeepsGenuineNewBlock(t *testing.T) {
	dir := t.TempDir()
	b, _ := blob.NewLocal(dir + "/cold")
	hotPath := dir + "/hot.data"
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	s, err := Open(hotPath, b, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	// seal a segment of 3 events (ids e0,e1,e2)
	for i := 0; i < 3; i++ {
		_ = s.Ingest(mk("e"+itoa(i), "ev", "u", base.Add(time.Duration(i)*time.Minute), nil))
	}
	if err := s.Flush(); err != nil { // seals + clears hot
		t.Fatal(err)
	}
	// now a genuine NEW hot block of 2 events — one reuses id "e1" (collision), one new
	_ = s.Ingest(mk("e1", "ev", "u", base.Add(time.Hour), nil))   // colliding id
	_ = s.Ingest(mk("new9", "ev", "u", base.Add(2*time.Hour), nil))
	_ = s.hot.Close() // crash with the new block unsealed (count=2 != sealed count=3)

	s2, err := Open(hotPath, b, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	// the 2 new events must survive (3 sealed + 2 hot = 5), NOT be falsely cleared
	if c := s2.Count(); c != 5 {
		t.Fatalf("recovery wrongly cleared a genuine new block: count=%d want 5", c)
	}
}
