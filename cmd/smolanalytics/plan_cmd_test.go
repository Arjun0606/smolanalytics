package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// fakeMCP is a canned Streamable-HTTP MCP endpoint: it records every tools/call it
// receives and answers each tool with a fixed text payload. An "ERR:" prefix makes
// the answer an isError tool result — exactly how the real server surfaces tool
// errors (e.g. "no tracking plan declared yet").
type fakeMCP struct {
	answers map[string]string
	calls   []fakeMCPCall
	path    string // last request path — must be /mcp
	auth    string // last Authorization header
}

type fakeMCPCall struct {
	Tool string
	Args json.RawMessage
}

func (f *fakeMCP) serve() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path = r.URL.Path
		f.auth = r.Header.Get("Authorization")
		var req struct {
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.calls = append(f.calls, fakeMCPCall{Tool: req.Params.Name, Args: req.Params.Arguments})
		text, ok := f.answers[req.Params.Name]
		if !ok {
			text = "ERR:unknown tool: " + req.Params.Name
		}
		isErr := strings.HasPrefix(text, "ERR:")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": strings.TrimPrefix(text, "ERR:")}},
				"isError": isErr,
			},
		})
	}))
}

func TestValidatePlanFile(t *testing.T) {
	cases := []struct {
		name string
		pf   planFile
		want string // substring of the error; "" = must be valid
	}{
		{"valid", planFile{Events: []trackplan.PlannedEvent{
			{Name: "signup", Description: "account created", Properties: []string{"plan"}},
			{Name: "checkout"},
		}}, ""},
		{"no events", planFile{}, "no events"},
		{"blank name", planFile{Events: []trackplan.PlannedEvent{{Name: "  "}}}, "empty name"},
		{"duplicate name", planFile{Events: []trackplan.PlannedEvent{{Name: "signup"}, {Name: "signup"}}}, "duplicate"},
	}
	for _, tc := range cases {
		err := validatePlanFile(tc.pf)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
	}
}

// push must send the file's events verbatim via set_tracking_plan on /mcp with the
// Bearer key, and report the count.
func TestPlanPush(t *testing.T) {
	f := &fakeMCP{answers: map[string]string{"set_tracking_plan": `{"plan":{},"note":"ok"}`}}
	srv := f.serve()
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	if err := os.WriteFile(path, []byte(
		`{"events":[{"name":"signup","description":"account created","properties":["plan"]},{"name":"checkout"}]}`,
	), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runPlanPush(path, srv.URL, "k1", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plan pushed: 2 events") {
		t.Errorf("output: %q", out.String())
	}
	if f.path != "/mcp" || f.auth != "Bearer k1" {
		t.Errorf("wire: path %q auth %q, want /mcp + Bearer k1", f.path, f.auth)
	}
	if len(f.calls) != 1 || f.calls[0].Tool != "set_tracking_plan" {
		t.Fatalf("calls: %+v", f.calls)
	}
	var sent planFile
	if err := json.Unmarshal(f.calls[0].Args, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Events) != 2 || sent.Events[0].Description != "account created" || sent.Events[0].Properties[0] != "plan" {
		t.Errorf("pushed events mangled: %+v", sent.Events)
	}
}

// a file that fails validation must abort BEFORE anything reaches the server.
func TestPlanPushInvalidFileSendsNothing(t *testing.T) {
	f := &fakeMCP{answers: map[string]string{}}
	srv := f.serve()
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	if err := os.WriteFile(path, []byte(`{"events":[{"name":"a"},{"name":"a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runPlanPush(path, srv.URL, "", &out); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate-name error, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("invalid plan must not be sent: %+v", f.calls)
	}

	if err := runPlanPush(filepath.Join(t.TempDir(), "nope.json"), srv.URL, "", &out); err == nil || !strings.Contains(err.Error(), "plan init") {
		t.Fatalf("missing file should point at `plan init`, got %v", err)
	}
}

// pull must write the plan portion of instrumentation_health as pretty JSON with a
// trailing newline — and never the server's "updated" stamp.
func TestPlanPull(t *testing.T) {
	f := &fakeMCP{answers: map[string]string{
		"instrumentation_health": `{"healthy":true,"plan":{"events":[{"name":"signup","description":"account created","properties":["plan"]}],"updated":"2026-07-01T10:00:00Z"},"planned":[],"unplanned_events":[]}`,
	}}
	srv := f.serve()
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	var out bytes.Buffer
	if err := runPlanPull(path, srv.URL, "", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "plan written: 1 event") {
		t.Errorf("output: %q", out.String())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Error("file must end with a newline")
	}
	if bytes.Contains(b, []byte("updated")) {
		t.Errorf("repo file must omit the server's updated stamp:\n%s", b)
	}
	if !bytes.Contains(b, []byte("\n  \"events\"")) {
		t.Errorf("file must be indented JSON:\n%s", b)
	}
	var pf planFile
	if err := json.Unmarshal(b, &pf); err != nil {
		t.Fatal(err)
	}
	if len(pf.Events) != 1 || pf.Events[0].Name != "signup" || pf.Events[0].Description != "account created" {
		t.Errorf("plan mangled: %+v", pf.Events)
	}
}

// no plan on the server → the error path (exit 1 via planCmd), and no file written.
func TestPlanPullNoPlanOnServer(t *testing.T) {
	f := &fakeMCP{answers: map[string]string{
		"instrumentation_health": "ERR:no tracking plan declared yet — set one with set_tracking_plan, then this tool verifies events against it",
	}}
	srv := f.serve()
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	var out bytes.Buffer
	if err := runPlanPull(path, srv.URL, "", &out); err == nil || !strings.Contains(err.Error(), "no tracking plan declared") {
		t.Fatalf("want the server's no-plan message, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("no file must be written when the server has no plan")
	}
}

func TestPlanCheck(t *testing.T) {
	healthyPayload := `{"healthy":true,"planned":[{"event":"signup","status":"flowing","count":312,"last_seen":"2026-07-02T14:03:11Z"},{"event":"checkout","status":"flowing","count":57,"last_seen":"2026-07-02T12:11:02Z"}],"unplanned_events":["mystery_click"]}`
	brokenPayload := `{"healthy":false,"planned":[{"event":"signup","status":"flowing","count":312,"last_seen":"2026-07-02T14:03:11Z"},{"event":"activate","status":"flowing","count":198,"last_seen":"2026-07-02T13:58:40Z","missing_properties":["source"]},{"event":"checkout","status":"MISSING — never seen"}],"unplanned_events":["mystery_click"]}`

	cases := []struct {
		name    string
		payload string
		asJSON  bool
		wantErr string // substring of the returned error; "" = exit 0
		want    []string
		not     []string
	}{
		{
			name: "all flowing", payload: healthyPayload,
			want: []string{"✓ signup", "312 events", "✓ checkout", "• mystery_click", "informational", "all 2 planned events verified"},
			not:  []string{"✗"},
		},
		{
			name: "missing event and property", payload: brokenPayload,
			wantErr: "2 of 3 planned events broken",
			want: []string{
				"✓ signup",
				"✗ activate", "missing properties: source",
				"✗ checkout", "never arrived",
				"• mystery_click",
			},
		},
		{
			name: "json passes payload through", payload: healthyPayload, asJSON: true,
			want: []string{`"healthy":true`},
			not:  []string{"✓", "✗"},
		},
		{
			name: "json still gates on health", payload: brokenPayload, asJSON: true,
			wantErr: "2 of 3 planned events broken",
			want:    []string{`"healthy":false`},
			not:     []string{"✗"},
		},
	}
	for _, tc := range cases {
		f := &fakeMCP{answers: map[string]string{"instrumentation_health": tc.payload}}
		srv := f.serve()
		var out bytes.Buffer
		err := runPlanCheck(srv.URL, "", 0, tc.asJSON, &out)
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

// --window must reach the tool as window_hours; without it no window is sent
// (all-time, the tool's default).
func TestPlanCheckWindow(t *testing.T) {
	payload := `{"healthy":true,"planned":[],"unplanned_events":[]}`
	f := &fakeMCP{answers: map[string]string{"instrumentation_health": payload}}
	srv := f.serve()
	defer srv.Close()

	var out bytes.Buffer
	if err := runPlanCheck(srv.URL, "", 24, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := runPlanCheck(srv.URL, "", 0, false, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls: %+v", f.calls)
	}
	if !bytes.Contains(f.calls[0].Args, []byte(`"window_hours":24`)) {
		t.Errorf("--window=24 not sent: %s", f.calls[0].Args)
	}
	if bytes.Contains(f.calls[1].Args, []byte("window_hours")) {
		t.Errorf("no --window must mean no window_hours: %s", f.calls[1].Args)
	}
}

// a 401 (bad --key) must surface the HTTP status and the server's hint.
func TestPlanCheckAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid or missing key — add Authorization: Bearer <key>"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	var out bytes.Buffer
	err := runPlanCheck(srv.URL, "wrong", 0, false, &out)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "missing key") {
		t.Fatalf("want the 401 and the server's hint, got %v", err)
	}
}

// pull → file → push must preserve the plan exactly (names, descriptions,
// properties) — the repo file is a faithful copy of the declared intent.
func TestPlanPullPushRoundTrip(t *testing.T) {
	served := []trackplan.PlannedEvent{
		{Name: "signup", Description: "account created", Properties: []string{"plan", "source"}},
		{Name: "checkout", Properties: []string{"amount"}},
	}
	servedJSON, err := json.Marshal(served)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeMCP{answers: map[string]string{
		"instrumentation_health": `{"healthy":true,"plan":{"events":` + string(servedJSON) + `,"updated":"2026-07-01T00:00:00Z"},"planned":[],"unplanned_events":[]}`,
		"set_tracking_plan":      `{"plan":{},"note":"ok"}`,
	}}
	srv := f.serve()
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	var out bytes.Buffer
	if err := runPlanPull(path, srv.URL, "", &out); err != nil {
		t.Fatal(err)
	}
	if err := runPlanPush(path, srv.URL, "", &out); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if last.Tool != "set_tracking_plan" {
		t.Fatalf("last call: %+v", last)
	}
	var pushed planFile
	if err := json.Unmarshal(last.Args, &pushed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pushed.Events, served) {
		t.Errorf("round trip mangled the plan:\n got %+v\nwant %+v", pushed.Events, served)
	}
}

// init writes a starter that validates and pushes as-is, prints the next steps,
// and refuses to overwrite an existing file.
func TestPlanInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smolanalytics.plan.json")
	var out bytes.Buffer
	if err := runPlanInit(path, &out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Error("starter must end with a newline")
	}
	var pf planFile
	if err := json.Unmarshal(b, &pf); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanFile(pf); err != nil {
		t.Errorf("starter must be pushable as-is: %v", err)
	}
	if len(pf.Events) != 1 || pf.Events[0].Name != "signup" || pf.Events[0].Description != "account created" || pf.Events[0].Properties[0] != "plan" {
		t.Errorf("starter content: %+v", pf.Events)
	}
	for _, step := range []string{"plan push", "plan check"} {
		if !strings.Contains(out.String(), step) {
			t.Errorf("next steps must mention %q:\n%s", step, out.String())
		}
	}
	if err := runPlanInit(path, &out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second init must refuse, got %v", err)
	}
}
