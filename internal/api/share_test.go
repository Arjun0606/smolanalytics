package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// A share link grants exactly one thing: the read-only overview page. A bad or
// revoked token gets a 404; a valid one never unlocks the dashboard or the API.
func TestShareLinkAccess(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123") // auth ON — the real threat model
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", Name: "$pageview", DistinctID: "u1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/", "referrer": "https://news.ycombinator.com/"}})
	s := New(st)
	sh, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetShares(sh)
	h := s.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}

	// mint via the MCP tool (the real creation path)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_share_link","arguments":{"name":"investor"}}}`)))
	var env struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	_ = json.NewDecoder(w.Body).Decode(&env)
	if env.Result.IsError {
		t.Fatalf("create_share_link failed: %s", env.Result.Content[0].Text)
	}
	var created struct {
		Path    string `json:"path"`
		Created struct{ ID string }
	}
	_ = json.Unmarshal([]byte(env.Result.Content[0].Text), &created)
	if !strings.HasPrefix(created.Path, "/share/") {
		t.Fatalf("no share path returned: %s", env.Result.Content[0].Text)
	}

	// valid token → the read-only page renders, with data, without a session
	w2 := get(created.Path)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), "read-only") || !strings.Contains(w2.Body.String(), "news.ycombinator.com") {
		t.Fatalf("share page should render traffic: %d", w2.Code)
	}
	// tokens never unlock anything else
	if w3 := get("/"); w3.Code != http.StatusFound {
		t.Fatalf("dashboard must still be login-gated, got %d", w3.Code)
	}
	if w4 := get("/v1/export"); w4.Code != http.StatusUnauthorized {
		t.Fatalf("api must still be gated, got %d", w4.Code)
	}
	// wrong token → 404
	if w5 := get("/share/00000000000000000000000000000000"); w5.Code != http.StatusNotFound {
		t.Fatalf("bad token should 404, got %d", w5.Code)
	}
	// revoke → the link dies immediately
	if err := sh.Delete(created.Created.ID); err != nil {
		t.Fatal(err)
	}
	if w6 := get(created.Path); w6.Code != http.StatusNotFound {
		t.Fatalf("revoked link must 404, got %d", w6.Code)
	}
}
