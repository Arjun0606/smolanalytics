package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// renderDash logs in and returns the dashboard HTML.
func renderDash(t *testing.T, s *Server, pass string) string {
	t.Helper()
	h := s.Handler()
	c := loginSession(t, h, pass)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("dashboard: got %d", w.Code)
	}
	return w.Body.String()
}

// TestCloudLinkDefaultsToSmolanalyticsCom pins the self-hosted behaviour: with no
// SMOLANALYTICS_CLOUD_URL set, the header's "Cloud ↗" link still points at
// smolanalytics.com. A self-hoster must never see a broken or tenant-specific link.
func TestCloudLinkDefaultsToSmolanalyticsCom(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	s := New(memory.New())
	body := renderDash(t, s, "op-pass-1234")

	want := `href="https://smolanalytics.com" target="_blank" style="color:var(--accent)">Cloud ↗</a>`
	if !strings.Contains(body, want) {
		t.Error(`self-hosted dashboard must keep the "Cloud ↗" link pointing at https://smolanalytics.com`)
	}
}

// TestCloudLinkUsesConfiguredURL covers the hosted case: the cloud provisions each
// instance with SMOLANALYTICS_CLOUD_URL set to that project's setup page, so the one
// link out of the instance leads back INTO the account. Without this the dashboard is a
// one-way door: the project page redirects here, so browser-back just redirects out again.
func TestCloudLinkUsesConfiguredURL(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	s := New(memory.New())
	s.SetCloudURL("https://smolanalytics.com/projects/prj_abc123/setup")
	body := renderDash(t, s, "op-pass-1234")

	want := `href="https://smolanalytics.com/projects/prj_abc123/setup" target="_blank" style="color:var(--accent)">Cloud ↗</a>`
	if !strings.Contains(body, want) {
		t.Error(`"Cloud ↗" must use SMOLANALYTICS_CLOUD_URL when set, so the user can get back to their project`)
	}
	if strings.Contains(body, `href="https://smolanalytics.com" target="_blank" style="color:var(--accent)">Cloud ↗</a>`) {
		t.Error(`"Cloud ↗" still points at the marketing home page even though a cloud URL was configured`)
	}
}
