package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestIngestWriteKeyAuth(t *testing.T) {
	s := New(memory.New())
	s.SetWriteKey("secret")
	h := s.Handler()

	// no key -> 401
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"name":"x","distinct_id":"u1"}`)))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("no key: got %d, want 401", r.Code)
	}

	// correct key -> 202
	r = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"name":"x","distinct_id":"u1"}`))
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusAccepted {
		t.Fatalf("good key: got %d, want 202", r.Code)
	}
}

func TestIngestOpenWhenNoKey(t *testing.T) {
	s := New(memory.New()) // no write key set
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"name":"x","distinct_id":"u1"}`)))
	if r.Code != http.StatusAccepted {
		t.Fatalf("open ingest: got %d, want 202", r.Code)
	}
}

// TestIngestBatchCap pins the batch-size guard: a batch over the cap is rejected with
// 413 (and — per the F4 fix — short-circuited, never fully parsed), a batch exactly at
// the cap is accepted, and a malformed array is a clean 400.
func TestIngestBatchCap(t *testing.T) {
	h := New(memory.New()).Handler()
	post := func(body string) int {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/events", strings.NewReader(body)))
		return r.Code
	}
	batch := func(n int) string {
		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"name":"x","distinct_id":"u%d"}`, i)
		}
		b.WriteByte(']')
		return b.String()
	}

	if code := post(batch(maxBatchEvents)); code != http.StatusAccepted {
		t.Fatalf("at cap (%d events): got %d, want 202", maxBatchEvents, code)
	}
	if code := post(batch(maxBatchEvents + 1)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap (%d events): got %d, want 413", maxBatchEvents+1, code)
	}
	if code := post(`[{"name":"x","distinct_id":"u1"},`); code != http.StatusBadRequest {
		t.Fatalf("malformed array: got %d, want 400", code)
	}
	if code := post(`[]`); code != http.StatusAccepted {
		t.Fatalf("empty array: got %d, want 202", code)
	}
}

func TestCORSPreflightAndSDK(t *testing.T) {
	h := New(memory.New()).Handler()

	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("OPTIONS", "/v1/events", nil))
	if r.Code != http.StatusNoContent || r.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: code=%d cors=%q", r.Code, r.Header().Get("Access-Control-Allow-Origin"))
	}

	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/sdk.js", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "smolanalytics") {
		t.Fatalf("sdk.js: code=%d, body has SDK=%v", r.Code, strings.Contains(r.Body.String(), "smolanalytics"))
	}
	if ct := r.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("sdk.js content-type = %q", ct)
	}
}
