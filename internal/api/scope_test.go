package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// Bot UAs must have their autocaptured ($-prefixed) events dropped and counted —
// while backend events on the same request survive (server SDKs send bot-looking UAs).
func TestIngestBotFiltering(t *testing.T) {
	st := memory.New()
	s := New(st)
	h := s.Handler()

	send := func(ua, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/events", strings.NewReader(body))
		req.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// a crawler sending a pageview → dropped, counted, still 202
	w := send("Mozilla/5.0 (compatible; GPTBot/1.0)", `[{"name":"$pageview","distinct_id":"u1"},{"name":"$click","distinct_id":"u1"}]`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"bots_filtered":2`) {
		t.Fatalf("bot pageviews should be dropped+counted: %d %s", w.Code, w.Body.String())
	}
	// same bot UA carrying a BACKEND event → the backend event survives
	w = send("Go-http-client/1.1 bot/monitor", `{"name":"signup","distinct_id":"u2"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("backend event with bot-ish UA must not be dropped: %d %s", w.Code, w.Body.String())
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 1 || evs[0].Name != "signup" {
		t.Fatalf("store should hold exactly the backend signup, got %+v", evs)
	}
	if got := s.botsFiltered.Load(); got != 2 {
		t.Fatalf("bots_filtered counter = %d, want 2", got)
	}
	// a real browser is never filtered
	w = send("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/126 Safari/537.36", `{"name":"$pageview","distinct_id":"u3"}`)
	if w.Code != http.StatusAccepted {
		t.Fatal("real browsers must pass")
	}
	evs, _ = st.Range(time.Time{}, time.Time{})
	if len(evs) != 2 {
		t.Fatalf("want 2 stored events, got %d", len(evs))
	}
}

// The production scope: env=development events are excluded from every report by
// default, included the moment a filter explicitly references env.
func TestEnvDefaultScope(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{
		{ID: "1", Name: "signup", DistinctID: "u1", Timestamp: now, Properties: map[string]any{"env": "production"}},
		{ID: "2", Name: "signup", DistinctID: "u2", Timestamp: now, Properties: map[string]any{"env": "development"}},
		{ID: "3", Name: "signup", DistinctID: "u3", Timestamp: now}, // unstamped (backend) = kept
	}
	if got := len(query.Apply(evs, nil)); got != 2 {
		t.Fatalf("default scope should keep prod+unstamped only, got %d", got)
	}
	dev := query.Apply(evs, []query.Filter{{Property: "env", Op: query.Eq, Value: "development"}})
	if len(dev) != 1 || dev[0].ID != "2" {
		t.Fatalf("explicit env filter must reach dev events, got %+v", dev)
	}
}

// The stats API: with auth enabled, a valid key reads GET /v1/* reports; writes,
// settings, and bad keys stay locked out.
func TestStatsAPIKeyAuth(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC()})
	s := New(st)
	s.SetWriteKey("wk_public") // ingest only
	s.SetReadKey("rk_secret")  // reads + MCP
	h := s.Handler()

	req := func(method, path, key string) int {
		r := httptest.NewRequest(method, path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// the READ key reads reports and the raw export.
	if got := req("GET", "/v1/trends?event=signup", "rk_secret"); got != 200 {
		t.Fatalf("read key should read reports, got %d", got)
	}
	if got := req("GET", "/v1/export", "rk_secret"); got != 200 {
		t.Fatalf("read key should read export, got %d", got)
	}
	// SECURITY REGRESSION: the WRITE key is public (it ships in the SDK). It must NEVER
	// read a report or the raw export — otherwise a scraped key leaks all data.
	if got := req("GET", "/v1/trends?event=signup", "wk_public"); got != http.StatusUnauthorized {
		t.Fatalf("write key must NOT read reports (public key = data leak), got %d", got)
	}
	if got := req("GET", "/v1/export", "wk_public"); got != http.StatusUnauthorized {
		t.Fatalf("write key must NOT read the raw export, got %d", got)
	}
	// the write key still authorizes ingest.
	body := `{"name":"signup","distinct_id":"u9"}`
	{
		r := httptest.NewRequest("POST", "/v1/events", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer wk_public")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code >= 300 {
			t.Fatalf("write key should ingest, got %d", w.Code)
		}
	}
	if got := req("GET", "/v1/trends?event=signup", "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("bad key must 401, got %d", got)
	}
	if got := req("GET", "/v1/trends?event=signup", ""); got != http.StatusUnauthorized {
		t.Fatalf("no auth must 401, got %d", got)
	}
	if got := req("POST", "/v1/settings/clear", "rk_secret"); got != http.StatusUnauthorized {
		t.Fatalf("keys must never unlock writes/settings, got %d", got)
	}
}
