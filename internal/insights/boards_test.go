package insights

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The migration is the only part of this package that can destroy something a real person made.
// Boards arrived after saved reports did, so every existing install has an insights file with no
// boards in it and no board id on any insight. If those insights do not find a home on load they
// are not "unassigned" — they are gone from every list the product renders, and the file is
// rewritten without them the next time anything is saved.
//
// So this test comes before any test of the board CRUD itself.
func TestPreBoardsFileKeepsEverySavedReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insights.json")

	// exactly what a v0 file looks like: insights, no boards, no board_id
	legacy := `{"insights":[
	  {"id":"a1","name":"Signup funnel","type":"funnel","params":{"steps":"signup,checkout"},"created":"2026-03-01T10:00:00Z"},
	  {"id":"a2","name":"Weekly retention","type":"retention","params":{"days":"7"},"created":"2026-02-01T09:00:00Z"},
	  {"id":"a3","name":"Traffic","type":"trend","params":{"event":"$pageview"},"created":"2026-04-15T12:00:00Z"}
	]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-boards file: %v", err)
	}

	if got := len(s.List()); got != 3 {
		t.Fatalf("%d insights survived the upgrade, want 3 — saved work was dropped on load", got)
	}
	// and every one of them has to be REACHABLE, which means on a board that exists
	for _, in := range s.List() {
		if in.BoardID == "" {
			t.Errorf("insight %q has no board after migration; it will render on no board at all", in.Name)
			continue
		}
		if _, err := s.GetBoard(in.BoardID); err != nil {
			t.Errorf("insight %q points at board %q, which does not exist: %v", in.Name, in.BoardID, err)
		}
	}
	onDefault, err := s.ListBoard(DefaultBoardID)
	if err != nil {
		t.Fatalf("the default board does not exist after migrating a pre-boards file: %v", err)
	}
	if len(onDefault) != 3 {
		t.Errorf("%d insights landed on the default board, want all 3", len(onDefault))
	}

	// The migrated board must not claim to be newer than the work on it. A naive now() would
	// date a board holding reports from March as created today.
	b, err := s.GetBoard(DefaultBoardID)
	if err != nil {
		t.Fatal(err)
	}
	oldest := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	if b.Created.After(oldest) {
		t.Errorf("default board created %s, but it inherited a report from %s — the board is newer than its own contents",
			b.Created.Format(time.RFC3339), oldest.Format(time.RFC3339))
	}

	// Reopening must be stable: the migration writes a board id onto each insight, and a second
	// pass must not re-migrate, renumber or duplicate anything.
	before := s.List()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	after := s2.List()
	if len(after) != len(before) {
		t.Fatalf("reopening changed the insight count: %d then %d", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID || before[i].BoardID != after[i].BoardID {
			t.Errorf("insight %d changed identity across a reopen: %+v then %+v", i, before[i], after[i])
		}
	}
	if len(s2.Boards()) != len(s.Boards()) {
		t.Errorf("reopening created a second set of boards: %d then %d", len(s.Boards()), len(s2.Boards()))
	}
}

// An insight pointing at a board that was deleted out from under it is the same failure as the
// migration case — it renders nowhere — so it is adopted by the same rule.
func TestInsightOnAMissingBoardIsAdoptedNotLost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insights.json")
	orphaned := `{"boards":[{"id":"` + DefaultBoardID + `","name":"Overview","created":"2026-01-01T00:00:00Z"}],
	  "insights":[{"id":"x1","name":"Ghost","type":"trend","board_id":"board_that_is_gone","created":"2026-05-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(orphaned), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatalf("the orphaned insight vanished on load")
	}
	if got := s.List()[0].BoardID; got != DefaultBoardID {
		t.Errorf("orphan landed on board %q, want the default %q", got, DefaultBoardID)
	}
}

// A file written by a NEWER binary must not be silently half-read by an older one. The v0 shape
// was a bare object; whatever the current shape is, loading it back has to round-trip.
func TestBoardsRoundTripThroughDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insights.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	made, err := s.CreateBoard(Board{Name: "Growth", Filters: map[string]string{"plan": "pro"}})
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	if _, err := s.Save(Insight{Name: "Checkout", Type: "funnel", BoardID: made.ID}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("the persisted file is not an object: %v", err)
	}
	if _, ok := probe["boards"]; !ok {
		t.Error("boards were not persisted — they would be rebuilt as defaults on every restart")
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.GetBoard(made.ID)
	if err != nil {
		t.Fatalf("board did not survive a reopen: %v", err)
	}
	if got.Name != "Growth" || got.Filters["plan"] != "pro" {
		t.Errorf("board came back as %+v, want name Growth with filter plan=pro", got)
	}
	on, err := s2.ListBoard(made.ID)
	if err != nil || len(on) != 1 {
		t.Errorf("the insight saved to that board did not come back with it: %v %d", err, len(on))
	}
}

// Deleting a board must have a stated answer for its contents. Whichever rule the store picked,
// the one outcome that is not allowed is insights that still point at an id that no longer exists.
func TestDeletingABoardNeverStrandsItsInsights(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "insights.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(Board{Name: "Temp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Insight{Name: "Report", Type: "trend", BoardID: b.ID}); err != nil {
		t.Fatal(err)
	}

	err = s.DeleteBoard(b.ID)
	if err != nil {
		// Refusing while non-empty is a legitimate rule — but then the board and its contents
		// must both still be there.
		if _, gerr := s.GetBoard(b.ID); gerr != nil {
			t.Fatalf("DeleteBoard refused (%v) but the board is gone anyway", err)
		}
		if len(s.List()) != 1 {
			t.Fatal("DeleteBoard refused but the insight was removed")
		}
		return
	}
	// Or it deleted — in which case nothing may still point at the dead id.
	for _, in := range s.List() {
		if in.BoardID == b.ID {
			t.Errorf("insight %q still points at the deleted board %q", in.Name, b.ID)
		}
		if _, err := s.GetBoard(in.BoardID); err != nil {
			t.Errorf("insight %q points at a board that does not exist after the delete", in.Name)
		}
	}
}
