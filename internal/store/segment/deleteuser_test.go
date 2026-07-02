package segment

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

func ev(id, user string, ts time.Time) event.Event {
	return event.Event{ID: id, Name: "e", DistinctID: user, Timestamp: ts}
}

// DeleteUser must erase the target from BOTH tiers (sealed segments get rewritten,
// the hot log gets compacted) and leave everyone else intact.
func TestDeleteUserAcrossTiers(t *testing.T) {
	dir := t.TempDir()
	b, _ := blob.NewLocal(filepath.Join(dir, "cold"))
	s, err := Open(filepath.Join(dir, "hot.log"), b, 4) // seal every 4 events
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// 6 events → one sealed segment of 4 (u1,u2,u1,u2) + 2 hot (u1,u3)
	for i, u := range []string{"u1", "u2", "u1", "u2", "u1", "u3"} {
		if err := s.Ingest(ev(fmt.Sprintf("e%d", i), u, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Count(); got != 6 {
		t.Fatalf("precondition: want 6 events, got %d", got)
	}

	n, err := s.DeleteUser("u1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("u1 had 3 events, deleted %d", n)
	}
	evs, _ := s.Range(time.Time{}, time.Time{})
	if len(evs) != 3 {
		t.Fatalf("3 events should remain, got %d", len(evs))
	}
	for _, e := range evs {
		if e.DistinctID == "u1" {
			t.Fatalf("u1 event survived: %+v", e)
		}
	}

	// erasure survives a reopen (manifest + rewritten segments are durable)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(filepath.Join(dir, "hot.log"), b, 4)
	if err != nil {
		t.Fatal(err)
	}
	evs2, _ := s2.Range(time.Time{}, time.Time{})
	if len(evs2) != 3 {
		t.Fatalf("after reopen: want 3 events, got %d", len(evs2))
	}
	for _, e := range evs2 {
		if e.DistinctID == "u1" {
			t.Fatalf("u1 resurrected after reopen: %+v", e)
		}
	}
}

// Regression: segment keys must never be reused. Deriving them from len(manifest)
// meant a Prune (or a full-segment DeleteUser) shrinking the manifest let the next
// seal overwrite a LIVE segment — silent data loss.
func TestSealKeyNeverReusedAfterShrink(t *testing.T) {
	dir := t.TempDir()
	b, _ := blob.NewLocal(filepath.Join(dir, "cold"))
	s, err := Open(filepath.Join(dir, "hot.log"), b, 2) // seal every 2
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// segment 0: two OLD events. segment 1: two recent.
	_ = s.Ingest(ev("a1", "u1", old), ev("a2", "u1", old.Add(time.Minute)))
	_ = s.Ingest(ev("b1", "u2", recent), ev("b2", "u2", recent.Add(time.Minute)))

	// prune drops segment 0 → manifest shrinks to 1 entry
	if _, err := s.Prune(old.AddDate(0, 6, 0)); err != nil {
		t.Fatal(err)
	}
	// next seal must NOT reuse segment 1's key and overwrite it
	_ = s.Ingest(ev("c1", "u3", recent.Add(time.Hour)), ev("c2", "u3", recent.Add(2*time.Hour)))

	evs, _ := s.Range(time.Time{}, time.Time{})
	if len(evs) != 4 {
		t.Fatalf("segment was overwritten by a reused key: want 4 events (b1,b2,c1,c2), got %d", len(evs))
	}
	users := map[string]int{}
	for _, e := range evs {
		users[e.DistinctID]++
	}
	if users["u2"] != 2 || users["u3"] != 2 {
		t.Fatalf("data lost: %v", users)
	}
}
