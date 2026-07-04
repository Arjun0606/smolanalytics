package main

// CLI-level import tests: the terminal summary and wire behavior are pinned here
// (they must never change shape under a refactor); the mapper unit tests live with
// the shared code in internal/importer.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/importer"
)

// end to end: jsonl fixture → runImport → fake /v1/events. Verifies the ingest
// shape (JSON array of events), the bearer key, batch splitting, preserved
// timestamps, and the summary counts.
func TestRunImportRoundTrip(t *testing.T) {
	oldBatch := importer.BatchSize
	importer.BatchSize = 2 // force two POSTs from three events
	defer func() { importer.BatchSize = oldBatch }()

	var batches [][]event.Event
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/events" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		auths = append(auths, r.Header.Get("Authorization"))
		var batch []event.Event
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("body is not a JSON array of events: %v", err)
		}
		batches = append(batches, batch)
		writeAccepted(w, len(batch))
	}))
	defer srv.Close()

	input := `{"id":"e1","name":"signup","distinct_id":"u1","timestamp":"2023-01-05T09:00:00Z"}` + "\n" +
		`{"id":"e2","name":"activate","distinct_id":"u1","timestamp":"2023-01-06T09:00:00Z"}` + "\n" +
		`{"id":"e3","name":"checkout","distinct_id":"u2","timestamp":"2023-01-07T09:00:00Z","properties":{"value":9.5}}` + "\n"

	var out strings.Builder
	if err := runImport("jsonl", srv.URL, "sekret", false, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}

	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("batches = %d (sizes %v), want 2 of sizes [2 1]", len(batches), batchSizes(batches))
	}
	for _, a := range auths {
		if a != "Bearer sekret" {
			t.Errorf("Authorization = %q, want Bearer sekret", a)
		}
	}
	got := batches[0][0]
	if got.ID != "e1" || got.Name != "signup" || got.DistinctID != "u1" {
		t.Errorf("first event mangled: %+v", got)
	}
	if !got.Timestamp.Equal(time.Date(2023, 1, 5, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("historical timestamp not preserved: %v", got.Timestamp)
	}
	if !strings.Contains(out.String(), "import complete: parsed 3, skipped 0, sent 3") {
		t.Errorf("summary missing or wrong:\n%s", out.String())
	}
}

// --dry-run must never touch the network.
func TestRunImportDryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("dry run sent a request")
	}))
	defer srv.Close()

	input := `{"name":"signup","distinct_id":"u1"}` + "\n" + "not json\n"
	var out strings.Builder
	if err := runImport("jsonl", srv.URL, "k", true, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "dry run: parsed 1, skipped 1") {
		t.Errorf("summary missing:\n%s", s)
	}
	if !strings.Contains(s, "invalid JSON line") || !strings.Contains(s, "first 1 mapped events") {
		t.Errorf("skip reason or preview missing:\n%s", s)
	}
}

func writeAccepted(w http.ResponseWriter, n int) {
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"accepted": n})
}

func batchSizes(batches [][]event.Event) []int {
	sizes := make([]int, len(batches))
	for i, b := range batches {
		sizes[i] = len(b)
	}
	return sizes
}
