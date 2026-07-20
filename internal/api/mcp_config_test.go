package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// TestDashboardMCPConfigUsesReadKey is the regression guard for an audit finding: after the
// two-key split, every "connect your agent" config the dashboard renders (Cursor, VS Code,
// Claude Code, Desktop, Windsurf, Zed, Cline, the universal prompt) still embedded the PUBLIC
// write key — which /mcp rejects — so every copy-paste MCP setup 401'd on first use. The
// configs must carry the SECRET read key (the dashboard is password-gated, so showing the
// operator their own read key is the intended distribution channel), and never the write key.
func TestDashboardMCPConfigUsesReadKey(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	s := New(memory.New())
	s.SetWriteKey("wk_public_123")
	s.SetReadKey("rk_secret_456")
	h := s.Handler()
	c := loginSession(t, h, "op-pass-1234")

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("dashboard: got %d", w.Code)
	}
	body := w.Body.String()

	// every Bearer the page renders for MCP must be the read key…
	if !strings.Contains(body, "Bearer rk_secret_456") {
		t.Error("dashboard MCP configs must embed the READ key (Bearer rk_secret_456) — copy-paste setup would 401 otherwise")
	}
	// …and the write key must never appear as a Bearer credential (it IS allowed inside the
	// SDK init snippet, which is ingest and public-by-design).
	if strings.Contains(body, "Bearer wk_public_123") {
		t.Error("dashboard renders the PUBLIC write key as a Bearer credential — /mcp rejects it, and it must never be shown as an auth header")
	}
	if !strings.Contains(body, `smolanalytics.init("wk_public_123"`) {
		t.Error("the SDK install snippet should still use the write key (ingest is its job)")
	}
}
