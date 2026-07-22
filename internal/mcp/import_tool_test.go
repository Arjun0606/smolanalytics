package mcp

// import_events tool behavior at the MCP surface: dry runs write nothing, real
// runs land normalized events, and failure messages name the fix. Byte-level
// parity with the CLI import is pinned in cmd/smolanalytics/import_parity_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func callImportTool(t *testing.T, s *Server, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "import_events", "arguments": args},
	})
	r := call(t, s, string(raw))
	res, _ := r.Result.(map[string]any)
	content, _ := res["content"].([]map[string]any)
	if len(content) == 0 {
		t.Fatalf("no content: %+v", r.Result)
	}
	text, _ := content[0]["text"].(string)
	if isErr, _ := res["isError"].(bool); isErr {
		return nil, text, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("import_events returned non-JSON: %q", text)
	}
	return out, text, false
}

func TestImportEventsDryRunWritesNothing(t *testing.T) {
	st := memory.New()
	s := New(st)
	path := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(path, []byte("name,distinct_id,plan\nsignup,u9,pro\nsignup,,x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, isErr := callImportTool(t, s, map[string]any{"format": "csv", "path": path, "dry_run": true})
	if isErr {
		t.Fatalf("dry run errored: %v", out)
	}
	if out["parsed"] != float64(1) || out["skipped_total"] != float64(1) {
		t.Fatalf("summary = %v, want parsed 1 skipped 1", out)
	}
	if _, has := out["sent"]; has {
		t.Fatalf("a dry run must not claim anything was sent: %v", out)
	}
	prev, _ := out["preview_first_3"].([]any)
	if len(prev) != 1 {
		t.Fatalf("preview = %v, want the 1 mapped event", out["preview_first_3"])
	}
	if evs, _ := st.Range(time.Time{}, time.Time{}); len(evs) != 0 {
		t.Fatalf("dry run wrote %d events — must write zero", len(evs))
	}
}

func TestImportEventsNormalizesLikeIngest(t *testing.T) {
	st := memory.New()
	s := New(st)
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	path := filepath.Join(t.TempDir(), "in.jsonl")
	fixture := `{"name":"signup","distinct_id":"u1"}` + "\n" + // no id, no timestamp
		`{"id":"keep","name":"buy","distinct_id":"u2","timestamp":"` + future + `"}` + "\n" // clock way off
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, isErr := callImportTool(t, s, map[string]any{"format": "jsonl", "path": path})
	if isErr || out["sent"] != float64(2) {
		t.Fatalf("import = %v (err %v), want sent 2", out, isErr)
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 2 {
		t.Fatalf("stored %d events, want 2", len(evs))
	}
	now := time.Now().UTC()
	for _, e := range evs {
		if e.ID == "" {
			t.Fatalf("missing id not assigned: %+v", e)
		}
		if e.Timestamp.IsZero() || e.Timestamp.After(now.Add(2*time.Hour)) {
			t.Fatalf("timestamp not normalized (zero or future): %+v", e)
		}
	}
}

func TestImportEventsBadPathNamesTheFix(t *testing.T) {
	s := New(memory.New())
	_, text, isErr := callImportTool(t, s, map[string]any{"format": "jsonl", "path": "/nope/missing.jsonl"})
	if !isErr || !strings.Contains(text, "machine the server runs on") {
		t.Fatalf("want a server-local-path error, got (%v) %q", isErr, text)
	}
}

func TestImportEventsUnknownFormat(t *testing.T) {
	s := New(memory.New())
	path := filepath.Join(t.TempDir(), "x")
	_ = os.WriteFile(path, []byte("{}"), 0o600)
	_, text, isErr := callImportTool(t, s, map[string]any{"format": "parquet", "path": path})
	if !isErr || !strings.Contains(text, "jsonl, csv, posthog, mixpanel, amplitude or umami") {
		t.Fatalf("want the format menu in the error, got (%v) %q", isErr, text)
	}
}

// a fully-specified jsonl import is idempotent: running the same file twice must
// not double-count (ids are preserved and the store dedupes on id).
func TestImportEventsJSONLRerunIsIdempotent(t *testing.T) {
	st := memory.New()
	s := New(st)
	path := filepath.Join(t.TempDir(), "in.jsonl")
	fixture := `{"id":"a1","name":"signup","distinct_id":"u1","timestamp":"2024-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if out, _, isErr := callImportTool(t, s, map[string]any{"format": "jsonl", "path": path}); isErr {
			t.Fatalf("run %d errored: %v", i, out)
		}
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 1 {
		t.Fatalf("stored %d events after re-run, want 1 (deduped on id)", len(evs))
	}
}
