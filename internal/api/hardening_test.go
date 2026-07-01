package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// Missing distinct_id must be rejected — silently accepting it merges everything
// into one phantom user and corrupts every per-user report.
func TestIngestRejectsMissingDistinctID(t *testing.T) {
	h := New(memory.New()).Handler()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"name":"signup"}`)))
	if r.Code != http.StatusBadRequest {
		t.Fatalf("missing distinct_id: got %d, want 400", r.Code)
	}
	if !strings.Contains(r.Body.String(), "distinct_id") {
		t.Fatalf("error should say what's missing: %s", r.Body.String())
	}
}

// Far-future client timestamps must be clamped to now — they'd skew every
// trailing-window report and anchor lifecycle on a day that hasn't happened.
func TestIngestClampsFutureTimestamps(t *testing.T) {
	st := memory.New()
	h := New(st).Handler()
	future := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/events",
		strings.NewReader(`{"name":"signup","distinct_id":"u1","timestamp":"`+future+`"}`)))
	if r.Code != http.StatusAccepted {
		t.Fatalf("ingest: got %d, want 202 (%s)", r.Code, r.Body.String())
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Timestamp.After(time.Now().UTC().Add(2 * time.Hour)) {
		t.Fatalf("future timestamp was not clamped: %v", evs[0].Timestamp)
	}
}

// /v1/notable must accept the write key when a dashboard password is set — the
// cloud control plane polls it with Bearer <writeKey> for the daily brief. Session-
// only auth here silently kills the retention hook.
func TestNotableAcceptsKeyWhenPasswordSet(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "hunter2-hunter2")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", DistinctID: "u", Name: "open", Timestamp: time.Now().UTC()})
	s := New(st)
	s.SetWriteKey("sk_test")
	h := s.Handler()

	// bearer key → 200 (what the cloud does)
	r := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/notable", nil)
	req.Header.Set("Authorization", "Bearer sk_test")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("key-authed notable: got %d, want 200 (%s)", r.Code, r.Body.String())
	}

	// no credentials → 401, not a data leak
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/v1/notable", nil))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated notable with password set: got %d, want 401", r.Code)
	}

	// wrong key → 401
	r = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/notable", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-key notable: got %d, want 401", r.Code)
	}
}

// Malformed ?filters= must 400 — returning unfiltered data as if it were the
// requested segment is a silent wrong answer.
func TestBadFiltersJSONIs400(t *testing.T) {
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", DistinctID: "u", Name: "signup", Timestamp: time.Now().UTC()})
	h := New(st).Handler()

	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/v1/trends?event=signup&filters="+url.QueryEscape(`[{"property":"plan","op"`), nil))
	if r.Code != http.StatusBadRequest {
		t.Fatalf("bad filters JSON: got %d, want 400 (%s)", r.Code, r.Body.String())
	}

	// unknown op too
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/v1/trends?event=signup&filters="+url.QueryEscape(`[{"property":"plan","op":"equals","value":"pro"}]`), nil))
	if r.Code != http.StatusBadRequest {
		t.Fatalf("unknown filter op: got %d, want 400 (%s)", r.Code, r.Body.String())
	}
}

// Repeated failed logins from one IP must get rate-limited.
func TestLoginRateLimit(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "correct-horse-battery")
	loginGuard.mu.Lock() // reset shared state so other tests can't interfere
	loginGuard.fails = map[string]int{}
	loginGuard.window = time.Now()
	loginGuard.mu.Unlock()

	h := New(memory.New()).Handler()
	form := func() *http.Request {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:4242"
		return req
	}
	for i := 0; i < 10; i++ {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, form())
		if r.Code != http.StatusFound {
			t.Fatalf("attempt %d: got %d, want 302 redirect to /login?e=1", i, r.Code)
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, form())
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("11th failed attempt: got %d, want 429", r.Code)
	}
}

// The HTTP funnel default window must match the MCP default (7 days) — the same
// question must not produce two different numbers on two surfaces.
func TestFunnelDefaultWindowParity(t *testing.T) {
	st := memory.New()
	base := time.Now().UTC().Add(-30 * 24 * time.Hour)
	_ = st.Ingest(
		event.Event{ID: "1", DistinctID: "slow", Name: "signup", Timestamp: base},
		event.Event{ID: "2", DistinctID: "slow", Name: "checkout", Timestamp: base.Add(20 * 24 * time.Hour)}, // 20d later — outside 7d
		event.Event{ID: "3", DistinctID: "fast", Name: "signup", Timestamp: base},
		event.Event{ID: "4", DistinctID: "fast", Name: "checkout", Timestamp: base.Add(24 * time.Hour)}, // inside
	)
	h := New(st).Handler()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/v1/funnel?steps=signup,checkout", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("funnel: %d (%s)", r.Code, r.Body.String())
	}
	var res struct {
		Steps []struct {
			Count int `json:"count"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil || len(res.Steps) != 2 {
		t.Fatalf("bad funnel response: %s", r.Body.String())
	}
	if res.Steps[1].Count != 1 {
		t.Fatalf("default window should be 7d (1 conversion), got %d conversions", res.Steps[1].Count)
	}
}
