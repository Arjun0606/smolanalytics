package api

// Regression tests for the /v1 surface's honesty rules: the drill-down must be computed by the
// same engine as the number it explains, a window parameter must never be silently swapped for a
// different one, and a list must come back in the same order twice. Each of these pins a bug that
// shipped: see the comments on the code they cover.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// surfacesServer seeds a store over HTTP (the real ingest path) and returns the live server.
func surfacesServer(t *testing.T, batch []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(memory.New()).Handler())
	t.Cleanup(srv.Close)
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("seed ingest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed ingest status %d", resp.StatusCode)
	}
	return srv
}

func getJSON(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestWhoFunnelUsesFunnelScope pins the microscope to the chart it explains. /v1/who's funnel mode
// loaded events with s.filtered (event-level) while /v1/funnel uses s.funnelScoped (user-level), so
// filtering by a property only the FIRST step carries charted "2 converted" and then listed nobody
// when you clicked that bar.
func TestWhoFunnelUsesFunnelScope(t *testing.T) {
	now := time.Now().UTC().Add(-2 * time.Hour)
	var batch []map[string]any
	for i := 0; i < 4; i++ {
		plan := "free"
		if i%2 == 0 {
			plan = "pro"
		}
		u := fmt.Sprintf("u%d", i)
		batch = append(batch,
			// only `signup` carries plan — checkout is a bare conversion event, which is the
			// normal shape (the attribute lives where it was known).
			map[string]any{"name": "signup", "distinct_id": u, "timestamp": now.Format(time.RFC3339),
				"properties": map[string]any{"plan": plan}},
			map[string]any{"name": "checkout", "distinct_id": u, "timestamp": now.Add(time.Minute).Format(time.RFC3339)},
		)
	}
	srv := surfacesServer(t, batch)

	_, fn := getJSON(t, srv, "/v1/funnel?steps=signup,checkout&f=plan:eq:pro")
	steps, _ := fn["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("funnel returned %v", fn)
	}
	last, _ := steps[1].(map[string]any)
	charted := int(last["count"].(float64))
	if charted != 2 {
		t.Fatalf("fixture broken: expected 2 pro users to reach checkout, got %d (%v)", charted, fn)
	}

	code, who := getJSON(t, srv, "/v1/who?steps=signup,checkout&step=1&state=reached&f=plan:eq:pro")
	if code != 200 {
		t.Fatalf("/v1/who status %d: %v", code, who)
	}
	listed := int(who["total_users"].(float64))
	if listed != charted {
		t.Fatalf("the bar says %d people reached checkout but the microscope lists %d — /v1/who must use the funnel's user-level scope", charted, listed)
	}
	if users, _ := who["users"].([]any); len(users) != charted {
		t.Fatalf("users array has %d rows, want %d", len(users), charted)
	}
}

// TestWindowParamsAreValidatedNotSubstituted pins ERRORS-1 across the six reports that used the
// `strconv.Atoi(...); err == nil && v > 0` idiom: a typo'd/negative/zero count read as ABSENT and
// the report answered over its default window with a 200, while /v1/trends 400s the same typo.
// from/to/hours on a days-only report are refused by name for the same reason.
func TestWindowParamsAreValidatedNotSubstituted(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	batch := []map[string]any{
		{"name": "signup", "distinct_id": "u1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"company": "acme"}},
		{"name": "checkout", "distinct_id": "u1", "timestamp": now.Add(time.Minute).Format(time.RFC3339)},
		{"name": "$pageview", "distinct_id": "u1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"path": "/"}},
	}
	srv := surfacesServer(t, batch)

	mustReject := []string{
		"/v1/web?days=abc",
		"/v1/web?days=-5",
		"/v1/web?days=0",
		"/v1/web?days=1.5",
		"/v1/web?from=2020-01-01&to=2020-02-01", // a 2020 question answered with the last 30 days
		"/v1/web?hours=6",
		"/v1/retention?event=signup&days=abc",
		"/v1/retention?event=signup&from=2020-01-01",
		"/v1/lifecycle?days=abc",
		"/v1/lifecycle?hours=6",
		"/v1/sessions?days=abc",
		"/v1/sessions?limit=abc",
		"/v1/sessions?from=2020-01-01&to=2020-02-01",
		"/v1/paths?start=signup&depth=abc",
		"/v1/paths?start=signup&depth=0",
		"/v1/groups?property=company&limit=abc",
		"/v1/groups?property=company&days=7", // groups has no window at all
		"/v1/stickiness?days=7",              // DAU/WAU/MAU windows are fixed
		"/v1/ai-visibility?days=abc",
		"/v1/funnel?steps=signup,checkout&breakdown=plan&breakdown_limit=abc",
		"/v1/brief?days=abc",
		"/v1/investigate?days=abc",
		"/v1/backtest?days=abc",
		"/v1/backtest?step=0",
		"/v1/backtest?days=365&step=1", // 365 sweeps over HTTP: refused with the arithmetic, not clamped
		"/v1/events/recent?limit=abc",
	}
	for _, p := range mustReject {
		code, body := getJSON(t, srv, p)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s → %d %v, want 400 (a silently substituted window is worse than an error)", p, code, body)
			continue
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Errorf("GET %s → 400 with no error message", p)
		}
	}

	// the honest values keep working, and the shared caps still clamp rather than reject —
	// the MCP tools cap identically and the two surfaces have to agree.
	mustAccept := []string{
		"/v1/web?days=30",
		"/v1/web",
		"/v1/retention?event=signup&days=500", // clamped to 90, same as MCP
		"/v1/lifecycle?days=500",              // clamped to 180
		"/v1/sessions?days=7&limit=500",
		"/v1/paths?start=signup&depth=50", // clamped to 10
		"/v1/groups?property=company",
		"/v1/stickiness",
		"/v1/ai-visibility?days=90",
		"/v1/brief?days=200",       // clamps to 90 — it used to fall back to 7 with no word
		"/v1/investigate?days=500", // clamps to 90, same as the MCP investigate tool
		"/v1/backtest?days=30&step=7",
		"/v1/events/recent?limit=1000",
	}
	for _, p := range mustAccept {
		if code, body := getJSON(t, srv, p); code != http.StatusOK {
			t.Errorf("GET %s → %d %v, want 200", p, code, body)
		}
	}
}

// TestCohortUsersIsStableAndCapped pins reproducibility: GET /v1/cohorts/{id}/users ranged a Go map
// straight into the response, so two identical requests over the same immutable log returned the
// same count in a different order — nothing downstream could diff or cache it.
func TestCohortUsersIsStableAndCapped(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	var batch []map[string]any
	for i := 0; i < 30; i++ {
		batch = append(batch, map[string]any{
			"name": "signup", "distinct_id": fmt.Sprintf("u%02d", i),
			"timestamp": now.Format(time.RFC3339),
		})
	}
	srv := surfacesServer(t, batch)

	resp, err := http.Post(srv.URL+"/v1/cohorts", "application/json",
		strings.NewReader(`{"name":"signed up","events":["signup"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var saved map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	id, _ := saved["id"].(string)
	if id == "" {
		t.Fatalf("cohort not saved: %v", saved)
	}

	var first []any
	for i := 0; i < 6; i++ {
		code, body := getJSON(t, srv, "/v1/cohorts/"+id+"/users")
		if code != 200 {
			t.Fatalf("cohort users status %d: %v", code, body)
		}
		if n := int(body["count"].(float64)); n != 30 {
			t.Fatalf("count = %d, want 30", n)
		}
		users, _ := body["users"].([]any)
		if len(users) != 30 {
			t.Fatalf("users has %d rows, want 30", len(users))
		}
		if i == 0 {
			first = users
			continue
		}
		if !reflect.DeepEqual(users, first) {
			t.Fatalf("repeat %d returned a different order:\n first: %v\n now:   %v", i, first, users)
		}
	}
	// and it is actually sorted, not just incidentally stable within one process
	for i := 1; i < len(first); i++ {
		if first[i-1].(string) > first[i].(string) {
			t.Fatalf("users are not sorted: %v", first)
		}
	}
}

// TestUnknownCohortIsAnError pins the scoping half of the same covenant: ?cohort=<deleted-or-typo'd>
// used to be dropped on the floor, so the report came back 200 with the FULL population in it,
// labelled as that cohort — while GET /v1/cohorts/<same id>/users 404s.
func TestUnknownCohortIsAnError(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	var batch []map[string]any
	for i := 0; i < 4; i++ {
		u := fmt.Sprintf("u%d", i)
		batch = append(batch,
			map[string]any{"name": "signup", "distinct_id": u, "timestamp": now.Format(time.RFC3339),
				"properties": map[string]any{"plan": "pro"}},
			map[string]any{"name": "checkout", "distinct_id": u, "timestamp": now.Add(time.Minute).Format(time.RFC3339)},
		)
	}
	srv := surfacesServer(t, batch)

	for _, p := range []string{
		"/v1/trends?event=signup&cohort=does-not-exist",
		"/v1/funnel?steps=signup,checkout&cohort=does-not-exist",
		"/v1/breakdown?event=signup&property=plan&cohort=does-not-exist",
	} {
		code, body := getJSON(t, srv, p)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s → %d %v, want 400 naming the unknown cohort", p, code, body)
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, "does-not-exist") {
			t.Errorf("GET %s → 400 %q, want the id named in the message", p, msg)
		}
	}

	// a REAL cohort still scopes, so the guard did not break the feature
	resp, err := http.Post(srv.URL+"/v1/cohorts", "application/json",
		strings.NewReader(`{"name":"checkers","events":["checkout"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var saved map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	id, _ := saved["id"].(string)
	if id == "" {
		t.Fatalf("cohort not saved: %v", saved)
	}
	code, body := getJSON(t, srv, "/v1/trends?event=signup&cohort="+id)
	if code != 200 {
		t.Fatalf("real cohort → %d %v", code, body)
	}
	if total := int(body["total"].(float64)); total != 4 {
		t.Fatalf("cohort-scoped total = %d, want 4", total)
	}
}
