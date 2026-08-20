package acted

import (
	"os"
	"testing"
)

// THE ONE BIT NOTHING ELSE CAN RECORD: a human read the finding and did something.
func TestMarkIsIdempotentAndKeepsTheFirstTimestamp(t *testing.T) {
	s, err := Open(t.TempDir() + "/acted.json")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Mark2(s, "regression|checkout|2026-08-01", "shipped a fix")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Mark2(s, "regression|checkout|2026-08-01", "clicked twice")
	if err != nil {
		t.Fatal(err)
	}
	// "When did a human first act" is the number the verified line is computed from; a re-click
	// must not rewrite history.
	if !again.At.Equal(first.At) || again.Note != first.Note {
		t.Fatalf("a second mark rewrote the first: %+v vs %+v", again, first)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("Get invented an entry")
	}
}

func Mark2(s *Store, fp, note string) (Entry, error) { return s.Mark(fp, note) }

// A corrupt ledger must not take the instance down — it holds annotations, not events.
func TestACorruptLedgerStartsEmptyRatherThanFailing(t *testing.T) {
	p := t.TempDir() + "/acted.json"
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatalf("a corrupt sidecar failed Open: %v", err)
	}
	if _, ok := s.Get("x"); ok {
		t.Error("read an entry out of garbage")
	}
}
