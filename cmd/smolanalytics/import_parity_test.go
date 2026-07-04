package main

// The two import surfaces — the CLI (POST /v1/events over HTTP) and the MCP
// import_events tool (direct store ingest) — share one mapper and one batcher in
// internal/importer. This test pins that promise end to end: the SAME fixture
// through both paths must land the SAME events in the store, byte for byte.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/api"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// parityFixture is fully specified (ids + timestamps) so both paths are
// deterministic and comparable; the bad rows pin identical skip handling.
const parityFixture = `{"id":"e1","name":"signup","distinct_id":"u1","timestamp":"2023-01-05T09:00:00Z","properties":{"plan":"pro","value":9.5}}
{"id":"e2","name":"activate","distinct_id":"u1","timestamp":"2023-01-06T09:00:00Z"}
not json
{"name":"nameless","timestamp":"2023-01-06T09:00:00Z"}
{"id":"e3","name":"checkout","distinct_id":"u2","timestamp":"2023-01-07T09:00:00Z"}
`

func storedEvents(t *testing.T, st *memory.Store) []event.Event {
	t.Helper()
	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].ID < evs[j].ID })
	return evs
}

func TestImportCLIAndMCPParity(t *testing.T) {
	// path A: the CLI against the real server handler
	cliStore := memory.New()
	srv := httptest.NewServer(api.New(cliStore).Handler())
	defer srv.Close()
	var out strings.Builder
	if err := runImport("jsonl", srv.URL, "", false, strings.NewReader(parityFixture), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "import complete: parsed 3, skipped 2, sent 3") {
		t.Fatalf("CLI summary wrong:\n%s", out.String())
	}

	// path B: the MCP import_events tool against its own store
	mcpStore := memory.New()
	m := mcp.New(mcpStore)
	fixture := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(fixture, []byte(parityFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "import_events", "arguments": map[string]any{"format": "jsonl", "path": fixture}},
	})
	status, resp := m.HTTPDispatch(req)
	if status != 200 {
		t.Fatalf("mcp dispatch status = %d", status)
	}
	var env struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.Unmarshal(resp, &env); err != nil || len(env.Result.Content) == 0 {
		t.Fatalf("bad mcp response: %v %s", err, resp)
	}
	if env.Result.IsError {
		t.Fatalf("import_events errored: %s", env.Result.Content[0].Text)
	}
	var sum struct {
		Parsed       int            `json:"parsed"`
		SkippedTotal int            `json:"skipped_total"`
		Skipped      map[string]int `json:"skipped_by_reason"`
		Sent         int            `json:"sent"`
	}
	if err := json.Unmarshal([]byte(env.Result.Content[0].Text), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Parsed != 3 || sum.SkippedTotal != 2 || sum.Sent != 3 {
		t.Fatalf("MCP summary = %+v, want parsed 3, skipped 2, sent 3 (same as CLI)", sum)
	}
	if sum.Skipped["invalid JSON line"] != 1 || sum.Skipped["missing distinct_id"] != 1 {
		t.Fatalf("MCP skip reasons = %v, want the CLI's", sum.Skipped)
	}

	// the pin: identical stored events, whichever door the file came through
	got, want := storedEvents(t, mcpStore), storedEvents(t, cliStore)
	if len(want) != 3 {
		t.Fatalf("CLI path stored %d events, want 3", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stores diverged:\n cli: %+v\n mcp: %+v", want, got)
	}
}
