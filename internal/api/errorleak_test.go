package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// TestFiltersErrorDoesNotLeakGoInternals is the regression guard for an audit finding: a
// malformed `filters` value made the shared parser return json.Unmarshal's raw error, which
// leaks internal Go type names ("[]query.Filter", "cannot unmarshal string into Go value of
// type ...") to the caller. Bad input must 400 with a clean, shape-guiding message and NO Go
// internals — this is the "don't expose my code" invariant, on every endpoint that filters.
func TestFiltersErrorDoesNotLeakGoInternals(t *testing.T) {
	s := New(memory.New())
	h := s.Handler()
	// filters as a bare string is malformed (must be a JSON array of objects)
	for _, path := range []string{
		`/v1/paths?start=signup&filters="plan=pro"`,
		`/v1/trends?event=signup&filters=notjson`,
		`/v1/breakdown?event=signup&property=plan&filters={bad}`,
	} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", path, w.Code)
		}
		for _, leak := range []string{"query.Filter", "cannot unmarshal", "Go value", "Go struct", "invalid character", "json:"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: response leaks Go internals (%q): %s", path, leak, body)
			}
		}
	}
}
