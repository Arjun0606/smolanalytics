package api

import (
	"encoding/json"
	"fmt"
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
	// a credential, because /mcp is no longer open on a password-protected instance — this test
	// used to mint a share link with none, which is exactly the hole it was meant to guard
	s.SetReadKey(readKeyForTest)
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
	mcpReq := httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_share_link","arguments":{"name":"investor"}}}`))
	mcpReq.Header.Set("Authorization", "Bearer "+readKeyForTest)
	h.ServeHTTP(w, mcpReq)
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
	// tokens never unlock anything else. /settings, not /: since the dashboard was deleted, GET /
	// is the public "this is not a dashboard" page (root.go) and 200 is its correct answer. What a
	// share token must never do is mint a session, and /settings is a surface a session still gates.
	if w3 := get("/settings"); w3.Code != http.StatusFound {
		t.Fatalf("settings must still be login-gated, got %d", w3.Code)
	}
	if w4 := get("/v1/export"); w4.Code != http.StatusUnauthorized {
		t.Fatalf("api must still be gated, got %d", w4.Code)
	}
	// wrong token → 404
	if w5 := get("/share/00000000000000000000000000000000"); w5.Code != http.StatusNotFound {
		t.Fatalf("bad token should 404, got %d", w5.Code)
	}
	// revoke → the link dies immediately
	if found, err := sh.Delete(created.Created.ID); err != nil || !found {
		t.Fatalf("revoking a real link: found=%v err=%v", found, err)
	}
	if w6 := get(created.Path); w6.Code != http.StatusNotFound {
		t.Fatalf("revoked link must 404, got %d", w6.Code)
	}
}

// THE FORWARDABLE ARTEFACT MUST CARRY THE UPDATE, AND IT MUST BE THE SAME UPDATE.
//
// This page is the only surface a stranger reads. A dashboard is opened by the person who bought
// it; a share link gets sent to a team, which is the whole distribution argument for a product
// with no sales motion.
//
// So it has to carry the same Investigation the dashboard and the weekly email carry, computed by
// the same call. A share page that computed its own version would be a marketing artefact rather
// than a report, and the first reader to check it against the dashboard would discover that.
func TestTheSharePageCarriesTheSameUpdateAsTheDashboard(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123")
	st := memory.New()

	now := time.Now().UTC()
	var evs []event.Event
	for d := 89; d >= 0; d-- {
		for i := 0; i < 40; i++ {
			u := fmt.Sprintf("d%d_%d", d, i)
			ts := now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)
			evs = append(evs, event.Event{ID: "p" + u, Name: "$pageview", DistinctID: u, Timestamp: ts,
				Properties: map[string]any{"path": "/"}})
			if i < 4 {
				evs = append(evs, event.Event{ID: "c" + u, Name: "checkout", DistinctID: u,
					Timestamp: ts.Add(time.Minute)})
			}
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	s := New(st)
	s.SetReadKey(readKeyForTest)
	sh, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetShares(sh)
	_, tok, err := sh.Create("weekly")
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/share/"+tok, nil))
	if w.Code != 200 {
		t.Fatalf("share page returned %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "did any of it add up") {
		t.Error("the share page has no movement section — a forwarded link that shows only " +
			"visitor counts is a traffic widget, not an update")
	}
	if !strings.Contains(body, "checkout") {
		t.Error("the metric is missing from the forwarded page")
	}
	// the honesty line travels with the number, because a stranger reads this without context
	if !strings.Contains(body, "share of active people") {
		t.Error("no basis stated: a rate on a forwarded page with no explanation is either " +
			"over-trusted or ignored, and both are worse than a smaller number that explains itself")
	}
	// and it must not leak the operator's console
	if strings.Contains(body, "operator-pass-123") || strings.Contains(body, readKeyForTest) {
		t.Fatal("the public share page leaked a credential")
	}
}
