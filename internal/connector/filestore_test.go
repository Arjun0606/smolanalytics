package connector

import (
	"context"
	"os"
	"testing"
	"time"
)

// THE WATERMARK MUST SURVIVE A RESTART.
//
// MemStore keeps it in memory, so every restart re-reads the whole 90-day window against someone
// else's API. A deploy would cost a customer a full backfill and a crash loop would cost them
// their rate limit — which defeats the entire reason this connector reads aggregates rather than
// raw events.
func TestTheWatermarkSurvivesARestart(t *testing.T) {
	path := t.TempDir() + "/conn.json"
	mark := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	buckets := []Bucket{{Event: "checkout", Count: 12}}

	s1, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Save(context.Background(), "posthog", "42", buckets, mark); err != nil {
		t.Fatal(err)
	}

	// A new process, same file.
	s2, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, gotMark, err := s2.Load(context.Background(), "posthog", "42")
	if err != nil {
		t.Fatal(err)
	}
	if !gotMark.Equal(mark) {
		t.Errorf("watermark lost across restart: %v, want %v — the next sync would refetch 90 days", gotMark, mark)
	}
	if len(got) != 1 || got[0].Event != "checkout" {
		t.Errorf("held aggregate lost across restart: %+v", got)
	}
}

func TestProjectsAndVendorsDoNotBleedIntoEachOther(t *testing.T) {
	s, _ := OpenFileStore(t.TempDir() + "/c.json")
	ctx := context.Background()
	early := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	_ = s.Save(ctx, "posthog", "a", []Bucket{{Event: "one"}}, early)
	_ = s.Save(ctx, "posthog", "b", []Bucket{{Event: "two"}}, late)

	got, mark, _ := s.Load(ctx, "posthog", "a")
	if len(got) != 1 || got[0].Event != "one" || !mark.Equal(early) {
		t.Errorf("project a read project b's state: %+v %v", got, mark)
	}
	// An unknown key is empty, not an error and not someone else's data.
	got, mark, err := s.Load(ctx, "mixpanel", "a")
	if err != nil || len(got) != 0 || !mark.IsZero() {
		t.Errorf("an unread vendor returned something: %+v %v %v", got, mark, err)
	}
}

// A corrupt cache must not take the instance down. It holds refetchable aggregates, not events.
func TestACorruptCacheStartsEmptyRatherThanFailing(t *testing.T) {
	path := t.TempDir() + "/c.json"
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("a corrupt cache failed Open: %v", err)
	}
	got, mark, _ := s.Load(context.Background(), "posthog", "1")
	if len(got) != 0 || !mark.IsZero() {
		t.Error("read state out of garbage")
	}
}
