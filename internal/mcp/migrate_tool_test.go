package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// callTool runs migrate_from through the real dispatch, so the test exercises the surface a model
// actually reaches rather than the helper underneath it.
func callMigrateTool(t *testing.T, s *Server, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	handled, out, err := s.callMigrate("migrate_from", raw)
	if !handled {
		t.Fatal("migrate_from was not handled — it is not wired into the dispatch")
	}
	return out, err
}

// A fake Mixpanel raw-export endpoint.
func fakeMixpanel(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _, ok := r.BasicAuth(); !ok || u == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}))
}

// The whole point: a key and a date range, and the history is here. Dry run must write NOTHING —
// a migration is easy to repeat and impossible to un-ring, so the safe path has to actually be safe.
func TestMigrateDryRunCountsWithoutWriting(t *testing.T) {
	st := memory.New()
	s := New(st)
	srv := fakeMixpanel(t,
		`{"event":"signup","properties":{"time":1767322800,"distinct_id":"u1","$insert_id":"mx-1"}}`,
		`{"event":"checkout","properties":{"time":1767326400,"distinct_id":"u1","$insert_id":"mx-2"}}`)
	defer srv.Close()

	out, err := callMigrateTool(t, s, map[string]any{
		"vendor": "mixpanel", "key": "sec", "host": srv.URL,
		"from": "2026-01-01", "to": "2026-01-03", "dry_run": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v — %s", err, out)
	}
	if got["parsed"].(float64) != 2 {
		t.Fatalf("want 2 parsed, got %v", got["parsed"])
	}
	if got["imported"].(float64) != 0 {
		t.Fatalf("a dry run imported %v events", got["imported"])
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 0 {
		t.Fatalf("dry run wrote %d events to the store", len(evs))
	}
	// The read has to tell the model what to do next, or it will stop at the dry run.
	if !strings.Contains(got["read"].(string), "dry_run:false") {
		t.Fatalf("the dry-run read must name the next step, got %q", got["read"])
	}
	if got["sample"] == nil {
		t.Fatal("a dry run must show sample events — counts alone do not tell you the mapping is right")
	}
}

// The real import lands, and lands through the same normalisation a file import uses.
func TestMigrateImportsForReal(t *testing.T) {
	st := memory.New()
	s := New(st)
	srv := fakeMixpanel(t,
		`{"event":"signup","properties":{"time":1767322800,"distinct_id":"u1","$insert_id":"mx-1"}}`)
	defer srv.Close()

	out, err := callMigrateTool(t, s, map[string]any{
		"vendor": "mixpanel", "key": "sec", "host": srv.URL,
		"from": "2026-01-01", "to": "2026-01-03",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal([]byte(out), &got)
	if got["imported"].(float64) != 1 {
		t.Fatalf("want 1 imported, got %v", got["imported"])
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 1 || evs[0].Name != "signup" || evs[0].DistinctID != "u1" {
		t.Fatalf("bad landing: %+v", evs)
	}
}

// Running the same range twice must not double every number. This is the failure people actually
// hit, because the natural response to a half-finished import is to run it again.
func TestMigratingTheSameRangeTwiceDoesNotDuplicate(t *testing.T) {
	st := memory.New()
	s := New(st)
	srv := fakeMixpanel(t,
		`{"event":"signup","properties":{"time":1767322800,"distinct_id":"u1","$insert_id":"mx-1"}}`,
		`{"event":"checkout","properties":{"time":1767326400,"distinct_id":"u1","$insert_id":"mx-2"}}`)
	defer srv.Close()

	args := map[string]any{"vendor": "mixpanel", "key": "sec", "host": srv.URL,
		"from": "2026-01-01", "to": "2026-01-03"}
	for i := 0; i < 2; i++ {
		if _, err := callMigrateTool(t, s, args); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 2 {
		t.Fatalf("importing the same range twice produced %d events, want 2 — the vendor ids are not "+
			"deduplicating and every number would now be doubled", len(evs))
	}
}

// An unbounded pull is refused at the tool boundary too, not just inside the package.
func TestMigrateRefusesAnUnboundedPull(t *testing.T) {
	s := New(memory.New())
	if _, err := callMigrateTool(t, s, map[string]any{"vendor": "mixpanel", "key": "k", "from": "", "to": ""}); err == nil {
		t.Fatal("a pull with no date range was accepted")
	}
	// A time is rejected rather than silently truncated: the vendor export is day-grained, so
	// accepting an hour would report a window nobody actually got.
	if _, err := callMigrateTool(t, s, map[string]any{
		"vendor": "mixpanel", "key": "k", "from": "2026-01-01T06:00:00Z", "to": "2026-01-03"}); err == nil {
		t.Fatal("an hour-precision range was accepted for a day-grained export")
	}
}

// An empty range is a FACT, not a failure — and must not read like a broken integration.
func TestAnEmptyRangeExplainsItself(t *testing.T) {
	s := New(memory.New())
	srv := fakeMixpanel(t) // no lines
	defer srv.Close()
	out, err := callMigrateTool(t, s, map[string]any{
		"vendor": "mixpanel", "key": "sec", "host": srv.URL,
		"from": "2026-01-01", "to": "2026-01-03", "dry_run": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal([]byte(out), &got)
	if got["parsed"].(float64) != 0 {
		t.Fatalf("want 0 parsed, got %v", got["parsed"])
	}
	read := got["read"].(string)
	if !strings.Contains(read, "empty") || !strings.Contains(read, "project id") {
		t.Fatalf("an empty range must say what to check, got %q", read)
	}
}

// The tool has to be discoverable, and its description has to steer toward the dry run.
func TestMigrateToolIsAdvertisedAndTellsTheModelToDryRunFirst(t *testing.T) {
	var found map[string]any
	for _, tl := range toolList {
		if tl["name"] == "migrate_from" {
			found = tl
			break
		}
	}
	if found == nil {
		t.Fatal("migrate_from is not in the tool list, so no model can reach it")
	}
	d := found["description"].(string)
	if !strings.Contains(d, "dry_run:true") {
		t.Fatal("the description must push the model to dry-run first")
	}
	if !strings.Contains(d, "never stored") {
		t.Fatal("the description must say the key is not stored — it is the user's first question")
	}
}
