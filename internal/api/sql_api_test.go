package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// sqlServer seeds production AND non-production events, because the whole risk with an escape
// hatch is that it sees a different slice of the store than the reports beside it.
func sqlServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	t.Cleanup(srv.Close)
	now := time.Now().UTC().Add(-time.Hour)
	batch := []map[string]any{
		{"name": "signup", "distinct_id": "p1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"country": "US"}},
		{"name": "signup", "distinct_id": "p2", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"country": "IN"}},
		{"name": "signup", "distinct_id": "d1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"country": "US", "env": "development"}},
		{"name": "signup", "distinct_id": "s1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"country": "US", "env": "preview"}},
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return srv
}

func sqlGet(t *testing.T, srv *httptest.Server, q string) map[string]any {
	t.Helper()
	var out map[string]any
	mustJSON(t, getBody(t, srv, "/v1/sql?q="+urlEnc(q)), &out)
	return out
}

// THE contract for an escape hatch: it answers the same question the reports answer.
//
// run_sql once scanned the store raw while every other tool applied the production scope, so the
// identical question returned 4 through SQL and 2 through trends — two surfaces, one question, two
// numbers, and nothing on screen saying which to trust. HTTP must inherit that fix, not re-earn it.
func TestSqlIsScopedToProductionLikeEveryOtherReport(t *testing.T) {
	srv := sqlServer(t)
	got := sqlGet(t, srv, "SELECT count(*) AS n FROM events WHERE name = 'signup'")
	rows := got["rows"].([]any)
	if n := rows[0].([]any)[0].(float64); n != 2 {
		t.Fatalf("want the 2 production signups, got %v — the dev and preview events leaked in", n)
	}
	if got["scoped_to_production"] != true {
		t.Fatal("the response must SAY it was scoped, or a reader cannot explain the number")
	}
}

// Naming env is an explicit request for that env, so the default scope steps aside rather than
// ANDing with it and returning a flat zero — which would read as "no data" rather than "excluded".
func TestNamingEnvOptsBackIntoEveryEnvironment(t *testing.T) {
	srv := sqlServer(t)
	got := sqlGet(t, srv, "SELECT count(*) AS n FROM events WHERE prop.env = 'development'")
	rows := got["rows"].([]any)
	if n := rows[0].([]any)[0].(float64); n != 1 {
		t.Fatalf("want the 1 development signup, got %v", n)
	}
	if got["scoped_to_production"] != false {
		t.Fatal("a query naming env must report that the production scope stepped aside")
	}
}

// A parse failure hands back the GRAMMAR. Someone who guessed wrong gets the supported surface in
// the same response instead of guessing again against a dialect they cannot see.
func TestABadQueryReturnsTheGrammar(t *testing.T) {
	srv := sqlServer(t)
	got := sqlGet(t, srv, "DELETE FROM events")
	if got["error"] == nil {
		t.Fatal("a non-SELECT was accepted")
	}
	g, _ := got["grammar"].(string)
	if !strings.Contains(g, "SELECT") || !strings.Contains(g, "prop.") {
		t.Fatalf("the grammar must ride along with a parse failure, got %q", g)
	}
}

// Write statements must not parse at all. This endpoint is reachable by anyone who can read the
// dashboard, so "read-only" has to be a property of the parser, not of good manners.
func TestOnlySelectParses(t *testing.T) {
	srv := sqlServer(t)
	for _, q := range []string{
		"DROP TABLE events", "INSERT INTO events VALUES (1)",
		"UPDATE events SET name = 'x'", "DELETE FROM events WHERE 1=1",
	} {
		got := sqlGet(t, srv, q)
		if got["error"] == nil {
			t.Fatalf("%q was accepted by a read-only endpoint", q)
		}
	}
}

// An empty query returns the grammar rather than a bare 400 — the grammar is the documentation,
// and this is the cheapest place to put it.
func TestAnEmptyQueryHandsBackTheGrammar(t *testing.T) {
	srv := sqlServer(t)
	var got map[string]any
	mustJSON(t, getBody(t, srv, "/v1/sql"), &got)
	if g, _ := got["grammar"].(string); !strings.Contains(g, "SELECT") {
		t.Fatalf("want the grammar, got %v", got)
	}
}

// GROUP BY works and the numbers are the production ones.
func TestGroupByReturnsRealAggregates(t *testing.T) {
	srv := sqlServer(t)
	got := sqlGet(t, srv, "SELECT prop.country AS c, count(*) AS n FROM events GROUP BY c ORDER BY n DESC")
	rows := got["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want US and IN from production only, got %d rows: %v", len(rows), rows)
	}
	total := 0.0
	for _, r := range rows {
		total += r.([]any)[1].(float64)
	}
	if total != 2 {
		t.Fatalf("the grouped counts must sum to the 2 production events, got %v", total)
	}
}

// A limit that is not a positive number is a typo, not a request for the default.
func TestABadLimitIsRejectedRatherThanIgnored(t *testing.T) {
	srv := sqlServer(t)
	resp, err := srv.Client().Get(srv.URL + "/v1/sql?q=" + urlEnc("SELECT name FROM events") + "&limit=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("limit=abc should be a 400, got %d — silently using the default answers a different question", resp.StatusCode)
	}
}
