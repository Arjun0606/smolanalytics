package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The root page is for one reader: somebody with a bookmark to the dashboard this instance no
// longer has. It was written in the commit that deleted the dashboard and then never rendered for
// anyone, because authMW ran first and sent every visitor to /login. cloud_link_test.go calls
// rootPage() directly, so the template was tested and the route never was — the page was dead
// code for a whole release (MEASURED on a fresh v0.91.0 instance: GET / -> 302 /login).
//
// This is the test that would have caught it: the full handler chain, auth ON, no session.
func TestRootRendersForAVisitorWhenAuthIsOn(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "operator-pass-123") // the gate is live — the real condition
	s := New(memory.New())
	h := s.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil)) // no cookie: a stranger
		return w
	}

	// / renders, and says what it is.
	if w := get("/"); w.Code != http.StatusOK {
		t.Fatalf("GET / for a visitor = %d, want 200: the root page is gated again and nobody can read it", w.Code)
	} else if !strings.Contains(w.Body.String(), "not a dashboard") {
		t.Fatalf("GET / returned 200 but not the root page:\n%s", w.Body.String()[:min(300, len(w.Body.String()))])
	}

	// And opening / did not open anything else: the gate is intact for a page that IS gated.
	if w := get("/settings"); w.Code != http.StatusFound {
		t.Fatalf("GET /settings for a visitor = %d, want 302: allowing / must not have widened the gate", w.Code)
	}
	if w := get("/v1/export"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/export for a visitor = %d, want 401", w.Code)
	}
}

// With no password configured the gate is off and / was always reachable; pinned so the two
// modes cannot drift apart again.
func TestRootRendersWhenAuthIsOff(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "")
	h := New(memory.New()).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not a dashboard") {
		t.Fatalf("GET / with auth off = %d, want 200 with the root page", w.Code)
	}
}
