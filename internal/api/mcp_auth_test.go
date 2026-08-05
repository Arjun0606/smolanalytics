package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The worst finding of the audit, verified end to end before it was fixed.
//
// An operator who follows the startup guard's own advice — bind publicly, set
// SMOLANALYTICS_PASSWORD, set no keys (both key vars are optional and the guard only checks the
// password) — got a dashboard behind a login, /v1/* returning 401, and POST /mcp WIDE OPEN.
// isPublic() exempts /mcp from the session gate, and authorized()'s "nothing configured"
// fallback was keyed on KEYS only, so it returned true.
//
// With no credential at all a stranger could read every report, read raw events including
// distinct_ids, mint an export link and download the entire event log through it, and call
// create_api_key to mint themselves a permanent managed key.
func TestMCPIsClosedWhenOnlyAPasswordIsSet(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "ev1", Name: "signup", DistinctID: "secret-user@example.com",
		Timestamp: time.Now().UTC()})
	h := New(st).Handler() // deliberately NO read key and NO write key

	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w
	}

	for _, tc := range []struct{ name, body string }{
		{"read a report", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup"}}}`},
		{"read raw events", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recent_events","arguments":{}}}`},
		{"mint an export link", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_export_link","arguments":{"format":"jsonl"}}}`},
		{"mint a permanent key", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_api_key","arguments":{"name":"pwned"}}}`},
	} {
		w := post(tc.body)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: POST /mcp with no credential returned %d, want 401.\nbody: %s",
				tc.name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret-user@example.com") {
			t.Errorf("%s: raw user data leaked to an unauthenticated caller", tc.name)
		}
	}

	// /v1/usage is on the same public list and was open for the same reason
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/usage", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/usage with no credential returned %d, want 401", w.Code)
	}
}

// The fix must not lock the legitimate operator out, and must not break the genuinely open
// local-dev case (nothing configured at all — the public demo relies on it).
func TestMCPStaysOpenWhenNothingIsConfigured(t *testing.T) {
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "ev1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC()})
	h := New(st).Handler() // no password, no keys — pure local dev

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup"}}}`)))
	if w.Code != http.StatusOK {
		t.Errorf("a fully unconfigured instance should still serve /mcp locally, got %d", w.Code)
	}
}

// A read key alone is a credential, and holding it must work.
func TestMCPAcceptsTheReadKey(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "ev1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC()})
	s := New(st)
	s.SetReadKey("sa_read_abc")
	h := s.Handler()

	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup"}}}`))
	req.Header.Set("Authorization", "Bearer sa_read_abc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("the read key was rejected: %d %s", w.Code, w.Body.String())
	}
}
