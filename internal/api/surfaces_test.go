package api

// Dashboard-surface routes for the MCP-only capabilities: minting share links,
// creating goals. Both are /v1 writes, so with a password set they must demand a
// session — a valid API key never unlocks them.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// loginSession logs in through the real form and returns the session cookie.
func loginSession(t *testing.T, h http.Handler, password string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("password="+password))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("login: got %d → %q, want 302 → /", w.Code, w.Header().Get("Location"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("login succeeded but set no session cookie")
	return nil
}

// POST /v1/shares mints a link whose raw token comes back exactly once; the link
// opens the read-only page, DELETE kills it, and no session means no minting.
func TestSharesRoutes(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", Name: "$pageview", DistinctID: "u1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/"}})
	s := New(st)
	sh, err := share.Open(filepath.Join(t.TempDir(), "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetShares(sh)
	h := s.Handler()

	do := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// no session → 401 (writes are never key-unlocked)
	if w := do("POST", "/v1/shares", `{"name":"investor"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: got %d, want 401", w.Code)
	}

	c := loginSession(t, h, "op-pass-1234")

	// happy path: 201 with the /share/<token> path, shown once
	w := do("POST", "/v1/shares", `{"name":"investor"}`, c)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d (%s), want 201", w.Code, w.Body.String())
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("bad create response: %s", w.Body.String())
	}
	if created.ID == "" || created.Name != "investor" || !strings.HasPrefix(created.Path, "/share/") {
		t.Fatalf("create response missing id/name/path: %s", w.Body.String())
	}

	// the minted link renders the read-only page with no session at all
	if w := do("GET", created.Path, "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "read-only") {
		t.Fatalf("minted link should open the share page: %d", w.Code)
	}

	// a nameless link is rejected with the fix in the message
	if w := do("POST", "/v1/shares", `{}`, c); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "name") {
		t.Fatalf("nameless create: got %d (%s), want 400 naming the fix", w.Code, w.Body.String())
	}

	// revoke needs a session too
	if w := do("DELETE", "/v1/shares/"+created.ID, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated revoke: got %d, want 401", w.Code)
	}
	if w := do("DELETE", "/v1/shares/"+created.ID, "", c); w.Code != http.StatusOK {
		t.Fatalf("revoke: got %d (%s), want 200", w.Code, w.Body.String())
	}
	// the link dies immediately
	if w := do("GET", created.Path, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("revoked link must 404, got %d", w.Code)
	}
}

// POST /v1/goals creates a goal (kind inferred from a leading "/" when omitted —
// the one-field dashboard form), DELETE removes it, and both demand a session.
func TestGoalsRoutes(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC()})
	s := New(st)
	gl, err := goal.Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetGoals(gl)
	h := s.Handler()

	post := func(body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/goals", strings.NewReader(body))
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// no session → 401
	if w := post(`{"name":"Signed up","value":"signup"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: got %d, want 401", w.Code)
	}

	c := loginSession(t, h, "op-pass-1234")

	// bare event name → kind "event"
	w := post(`{"name":"Signed up","value":"signup"}`, c)
	if w.Code != http.StatusCreated {
		t.Fatalf("create event goal: got %d (%s), want 201", w.Code, w.Body.String())
	}
	var d goal.Definition
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.ID == "" {
		t.Fatalf("bad create response: %s", w.Body.String())
	}
	if d.Kind != "event" || d.Value != "signup" {
		t.Fatalf("bare event name should infer kind=event, got %q %q", d.Kind, d.Value)
	}

	// leading slash → kind "path"
	w = post(`{"name":"Thanks page","value":"/thanks*"}`, c)
	if w.Code != http.StatusCreated {
		t.Fatalf("create path goal: got %d (%s), want 201", w.Code, w.Body.String())
	}
	var p goal.Definition
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p.Kind != "path" {
		t.Fatalf("leading / should infer kind=path, got %q", p.Kind)
	}
	// an explicit kind is respected, never re-inferred
	w = post(`{"name":"Explicit","kind":"event","value":"checkout"}`, c)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"kind":"event"`) {
		t.Fatalf("explicit kind: got %d (%s)", w.Code, w.Body.String())
	}
	if got := len(gl.List()); got != 3 {
		t.Fatalf("store should hold 3 goals, got %d", got)
	}

	// empty fields → 400 with guidance from the store
	if w := post(`{"name":"","value":""}`, c); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "name") {
		t.Fatalf("empty goal: got %d (%s), want 400 naming the fix", w.Code, w.Body.String())
	}

	// delete needs a session, then works
	rNoAuth := httptest.NewRequest("DELETE", "/v1/goals/"+d.ID, nil)
	wNoAuth := httptest.NewRecorder()
	h.ServeHTTP(wNoAuth, rNoAuth)
	if wNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete: got %d, want 401", wNoAuth.Code)
	}
	rDel := httptest.NewRequest("DELETE", "/v1/goals/"+d.ID, nil)
	rDel.AddCookie(c)
	wDel := httptest.NewRecorder()
	h.ServeHTTP(wDel, rDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete: got %d (%s), want 200", wDel.Code, wDel.Body.String())
	}
	if got := len(gl.List()); got != 2 {
		t.Fatalf("store should hold 2 goals after delete, got %d", got)
	}
}
