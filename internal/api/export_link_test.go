package api

// The one-time export link: minted by the create_export_link MCP tool, served by
// GET /export/{token}. The full lifecycle under the real handler, with auth ON —
// mint, download once, and prove the second download (and every other door) stays
// shut.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/exportlink"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestExportLinkMintDownloadBurn(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123") // auth ON — the real threat model
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "ev1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"plan": "pro"}})
	s := New(st)
	ex, err := exportlink.Open(filepath.Join(t.TempDir(), "exportlinks.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetExportLinks(ex)
	h := s.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}

	// mint via the MCP tool (the real creation path)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_export_link","arguments":{"format":"jsonl"}}}`)))
	var env struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	_ = json.NewDecoder(w.Body).Decode(&env)
	if env.Result.IsError {
		t.Fatalf("create_export_link failed: %s", env.Result.Content[0].Text)
	}
	var created struct {
		Path    string `json:"path"`
		Created struct{ Format string }
	}
	_ = json.Unmarshal([]byte(env.Result.Content[0].Text), &created)
	if !strings.HasPrefix(created.Path, "/export/") || created.Created.Format != "jsonl" {
		t.Fatalf("no export path returned: %s", env.Result.Content[0].Text)
	}

	// first download: the actual data, no session, no key
	w1 := get(created.Path)
	if w1.Code != 200 {
		t.Fatalf("download = %d, want 200", w1.Code)
	}
	if ct := w1.Header().Get("Content-Type"); !strings.Contains(ct, "ndjson") {
		t.Fatalf("Content-Type = %q, want jsonl", ct)
	}
	var got event.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(w1.Body.String())), &got); err != nil || got.ID != "ev1" {
		t.Fatalf("export body wrong (%v): %s", err, w1.Body.String())
	}

	// second download: burned — and the error names the recovery
	w2 := get(created.Path)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("second download = %d, want 404 (single-use)", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "create_export_link") {
		t.Fatalf("404 must point at minting a fresh link: %s", w2.Body.String())
	}

	// a made-up token gets the same 404, and the gated surfaces stay gated
	if w3 := get("/export/00000000000000000000000000000000"); w3.Code != http.StatusNotFound {
		t.Fatalf("bad token = %d, want 404", w3.Code)
	}
	if w4 := get("/v1/export"); w4.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/export must still require auth, got %d", w4.Code)
	}
	if w5 := get("/"); w5.Code != http.StatusFound {
		t.Fatalf("dashboard must still be login-gated, got %d", w5.Code)
	}
}

// without the store wired (bare demo / stdio), the tool must say what's missing
// and how to get it — not mint a dead URL.
func TestExportLinkWithoutStore(t *testing.T) {
	s := New(memory.New())
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_export_link","arguments":{}}}`)))
	var env struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	_ = json.NewDecoder(w.Body).Decode(&env)
	if !env.Result.IsError || !strings.Contains(env.Result.Content[0].Text, "export-link") {
		t.Fatalf("want a no-store error naming the fix, got: %+v", env.Result)
	}
	if w2 := httptest.NewRecorder(); true {
		s.Handler().ServeHTTP(w2, httptest.NewRequest("GET", "/export/00000000000000000000000000000000", nil))
		if w2.Code != http.StatusNotFound {
			t.Fatalf("download without a store = %d, want 404", w2.Code)
		}
	}
}
