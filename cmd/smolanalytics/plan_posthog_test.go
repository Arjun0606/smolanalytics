package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePostHog is a canned PostHog query endpoint. It records every HogQL query it
// receives and answers the per-event counts query (the one with GROUP BY) with
// countRows and the property-presence query with propRows. A non-zero status makes
// every request fail with that HTTP status — how the real API surfaces bad keys.
type fakePostHog struct {
	countRows [][]any
	propRows  [][]any
	status    int
	body      string
	queries   []string
	path      string
	auth      string
}

func (f *fakePostHog) serve() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path = r.URL.Path
		f.auth = r.Header.Get("Authorization")
		var req struct {
			Query struct {
				Kind  string `json:"kind"`
				Query string `json:"query"`
			} `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.queries = append(f.queries, req.Query.Query)
		if f.status != 0 {
			http.Error(w, f.body, f.status)
			return
		}
		rows := f.propRows
		if strings.Contains(req.Query.Query, "GROUP BY event") {
			rows = f.countRows
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
	}))
}

// writeTestPlan writes the two-event plan the posthog tests check against:
// signup expects the "plan" property, checkout expects "amount".
func writeTestPlan(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	if err := os.WriteFile(path, []byte(
		`{"events":[{"name":"signup","properties":["plan"]},{"name":"checkout","properties":["amount"]}]}`,
	), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanCheckPostHog(t *testing.T) {
	flowing := [][]any{
		{"signup", 312.0, "2026-07-02T14:03:11Z"},
		{"checkout", 57.0, "2026-07-02 12:11:02"}, // ClickHouse shape — must normalize
	}
	cases := []struct {
		name      string
		countRows [][]any
		propRows  [][]any // one row: countIf(signup.plan), countIf(checkout.amount)
		asJSON    bool
		wantErr   string // substring of the returned error; "" = exit 0
		want      []string
		not       []string
	}{
		{
			name: "all flowing", countRows: flowing, propRows: [][]any{{300.0, 57.0}},
			want: []string{
				"✓ signup", "312 events", "last seen 2026-07-02T14:03:11Z",
				"✓ checkout", "last seen 2026-07-02T12:11:02Z",
				"unplanned events not checked",
				"all 2 planned events verified",
			},
			not: []string{"✗"},
		},
		{
			name:      "missing event",
			countRows: [][]any{{"signup", 312.0, "2026-07-02T14:03:11Z"}},
			propRows:  [][]any{{300.0, 0.0}},
			wantErr:   "1 of 2 planned events broken",
			want:      []string{"✓ signup", "✗ checkout", "never arrived"},
		},
		{
			name: "missing property", countRows: flowing, propRows: [][]any{{300.0, 0.0}},
			wantErr: "1 of 2 planned events broken",
			want:    []string{"✓ signup", "✗ checkout", "missing properties: amount"},
		},
		{
			name: "json payload", countRows: flowing, propRows: [][]any{{300.0, 57.0}}, asJSON: true,
			want: []string{`"healthy":true`, `"source":"posthog"`, `"unplanned_events":[]`},
			not:  []string{"✓", "✗"},
		},
		{
			name: "json still gates", countRows: [][]any{}, propRows: [][]any{{0.0, 0.0}}, asJSON: true,
			wantErr: "2 of 2 planned events broken",
			want:    []string{`"healthy":false`},
			not:     []string{"✗"},
		},
	}
	for _, tc := range cases {
		f := &fakePostHog{countRows: tc.countRows, propRows: tc.propRows}
		srv := f.serve()
		var out bytes.Buffer
		err := runPlanCheckPostHog(writeTestPlan(t), srv.URL, "phx_key", "42", 168, tc.asJSON, &out)
		srv.Close()
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: got error %v, want %q", tc.name, err, tc.wantErr)
		}
		for _, w := range tc.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: report missing %q\n%s", tc.name, w, out.String())
			}
		}
		for _, n := range tc.not {
			if strings.Contains(out.String(), n) {
				t.Errorf("%s: report should not contain %q\n%s", tc.name, n, out.String())
			}
		}
	}
}

// the wire contract: project id in the path, Bearer key, exactly two HogQL queries
// (per-event counts, then one countIf per planned property), window applied to both.
func TestPlanCheckPostHogWire(t *testing.T) {
	f := &fakePostHog{
		countRows: [][]any{{"signup", 1.0, "2026-07-02T14:03:11Z"}, {"checkout", 1.0, "2026-07-02T14:03:11Z"}},
		propRows:  [][]any{{1.0, 1.0}},
	}
	srv := f.serve()
	defer srv.Close()

	var out bytes.Buffer
	if err := runPlanCheckPostHog(writeTestPlan(t), srv.URL, "phx_key", "42", 168, false, &out); err != nil {
		t.Fatal(err)
	}
	if f.path != "/api/projects/42/query" {
		t.Errorf("path: %q", f.path)
	}
	if f.auth != "Bearer phx_key" {
		t.Errorf("auth: %q", f.auth)
	}
	if len(f.queries) != 2 {
		t.Fatalf("want exactly 2 queries regardless of plan size, got %d: %q", len(f.queries), f.queries)
	}
	counts, props := f.queries[0], f.queries[1]
	for _, want := range []string{"event IN ('signup', 'checkout')", "INTERVAL 168 HOUR", "GROUP BY event"} {
		if !strings.Contains(counts, want) {
			t.Errorf("counts query missing %q: %s", want, counts)
		}
	}
	for _, want := range []string{
		"countIf(event = 'signup' AND isNotNull(properties.`plan`))",
		"countIf(event = 'checkout' AND isNotNull(properties.`amount`))",
		"INTERVAL 168 HOUR",
	} {
		if !strings.Contains(props, want) {
			t.Errorf("props query missing %q: %s", want, props)
		}
	}
}

// a 401 (bad or under-scoped key) must say which flag to fix and what the key needs.
func TestPlanCheckPostHogAuthError(t *testing.T) {
	f := &fakePostHog{status: http.StatusUnauthorized, body: `{"detail":"Invalid personal API key."}`}
	srv := f.serve()
	defer srv.Close()

	var out bytes.Buffer
	err := runPlanCheckPostHog(writeTestPlan(t), srv.URL, "wrong", "42", 168, false, &out)
	if err == nil || !strings.Contains(err.Error(), "--ph-key") || !strings.Contains(err.Error(), "query:read") {
		t.Fatalf("want the --ph-key + query:read hint, got %v", err)
	}
}

// a 404 must point at --ph-project (and the us/eu host split).
func TestPlanCheckPostHogProjectNotFound(t *testing.T) {
	f := &fakePostHog{status: http.StatusNotFound, body: `{"detail":"Not found."}`}
	srv := f.serve()
	defer srv.Close()

	var out bytes.Buffer
	err := runPlanCheckPostHog(writeTestPlan(t), srv.URL, "phx_key", "999", 168, false, &out)
	if err == nil || !strings.Contains(err.Error(), "--ph-project") {
		t.Fatalf("want the --ph-project hint, got %v", err)
	}
}

// missing flags must fail before any file or network access, naming the flag.
func TestPlanCheckPostHogFlagValidation(t *testing.T) {
	cases := []struct {
		name      string
		phKey     string
		phProject string
		want      string // substring of the error; "" = valid
	}{
		{"both set", "phx_key", "42", ""},
		{"no key", "", "42", "--ph-key"},
		{"no project", "phx_key", "", "--ph-project"},
		{"neither", "", "", "--ph-key"},
	}
	for _, tc := range cases {
		err := validatePostHogFlags(tc.phKey, tc.phProject)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
		// runPlanCheckPostHog must surface the same error without touching anything.
		if err2 := runPlanCheckPostHog("nonexistent.plan.json", "http://unreachable.invalid", tc.phKey, tc.phProject, 168, false, &bytes.Buffer{}); err2 == nil || !strings.Contains(err2.Error(), tc.want) {
			t.Errorf("%s: runPlanCheckPostHog got %v, want error containing %q", tc.name, err2, tc.want)
		}
	}
}

// hogql escaping: names and property keys are user input, not trusted syntax.
func TestHogQLQuoting(t *testing.T) {
	cases := []struct{ in, wantStr, wantIdent string }{
		{"plan", "'plan'", "`plan`"},
		{"it's", `'it\'s'`, "`it's`"},
		{"a`b", "'a`b'", "`a\\`b`"},
		{`a\b`, `'a\\b'`, "`a\\\\b`"},
	}
	for _, tc := range cases {
		if got := hogqlString(tc.in); got != tc.wantStr {
			t.Errorf("hogqlString(%q) = %s, want %s", tc.in, got, tc.wantStr)
		}
		if got := hogqlIdent(tc.in); got != tc.wantIdent {
			t.Errorf("hogqlIdent(%q) = %s, want %s", tc.in, got, tc.wantIdent)
		}
	}
}
