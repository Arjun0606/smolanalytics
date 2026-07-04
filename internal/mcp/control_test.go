package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

func controlServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	now := time.Now().UTC()
	// signup arrives WITH plan but WITHOUT source; checkout never arrives at all
	if err := st.Ingest(
		event.Event{ID: "1", Name: "signup", DistinctID: "u1", Timestamp: now, Properties: map[string]any{"plan": "pro"}},
		event.Event{ID: "2", Name: "mystery_click", DistinctID: "u1", Timestamp: now},
		event.Event{ID: "3", Name: "$pageview", DistinctID: "u1", Timestamp: now, Properties: map[string]any{"path": "/"}},
	); err != nil {
		t.Fatal(err)
	}
	s := New(st)
	set, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	tp, _ := trackplan.Open(filepath.Join(t.TempDir(), "plan.json"))
	s.SetSettings(set)
	s.SetTrackPlan(tp)
	return s
}

func TestInstrumentationHealth(t *testing.T) {
	s := controlServer(t)

	if _, err := callAct(t, s, "instrumentation_health", `{}`); err == nil || !strings.Contains(err.Error(), "set_tracking_plan") {
		t.Fatalf("no plan yet should point at set_tracking_plan, got %v", err)
	}

	if _, err := callAct(t, s, "set_tracking_plan",
		`{"events":[{"name":"signup","properties":["plan","source"]},{"name":"checkout","properties":["amount"]}]}`); err != nil {
		t.Fatal(err)
	}

	out, err := callAct(t, s, "instrumentation_health", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Healthy bool `json:"healthy"`
		Plan    struct {
			Events []trackplan.PlannedEvent `json:"events"`
		} `json:"plan"`
		Planned   []map[string]any `json:"planned"`
		Unplanned []string         `json:"unplanned_events"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Healthy {
		t.Fatal("checkout missing + signup missing 'source' → must be unhealthy")
	}
	// the declared plan must ride along verbatim — `plan pull` reads it from here
	if len(r.Plan.Events) != 2 || r.Plan.Events[0].Name != "signup" || len(r.Plan.Events[0].Properties) != 2 {
		t.Fatalf("health payload must carry the declared plan: %+v", r.Plan)
	}
	byEvent := map[string]map[string]any{}
	for _, row := range r.Planned {
		byEvent[row["event"].(string)] = row
	}
	if got := byEvent["signup"]["status"]; got != "flowing" {
		t.Fatalf("signup should be flowing, got %v", got)
	}
	if miss, _ := byEvent["signup"]["missing_properties"].([]any); len(miss) != 1 || miss[0] != "source" {
		t.Fatalf("signup should be missing exactly 'source': %v", byEvent["signup"])
	}
	if got := byEvent["checkout"]["status"]; got == "flowing" {
		t.Fatal("checkout never arrived — must be MISSING")
	}
	// mystery_click is unplanned; $pageview (autocapture) must NOT be flagged
	if len(r.Unplanned) != 1 || r.Unplanned[0] != "mystery_click" {
		t.Fatalf("unplanned should be exactly [mystery_click]: %v", r.Unplanned)
	}
}

func TestControlSettingsTools(t *testing.T) {
	s := controlServer(t)

	if _, err := callAct(t, s, "set_project", `{"timezone":"Not/AZone"}`); err == nil {
		t.Fatal("bad timezone must be rejected")
	}
	if _, err := callAct(t, s, "set_project", `{"name":"My SaaS","timezone":"Europe/Berlin"}`); err != nil {
		t.Fatal(err)
	}
	out, _ := callAct(t, s, "get_settings", `{}`)
	if !strings.Contains(out, "My SaaS") || !strings.Contains(out, "Europe/Berlin") {
		t.Fatalf("settings not applied: %s", out)
	}

	// key lifecycle: create shows the key once; list redacts; revoke works
	created, err := callAct(t, s, "create_api_key", `{"name":"prod web"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created, `"key"`) {
		t.Fatalf("create_api_key must return the key once: %s", created)
	}
	var c struct {
		Created struct{ ID string } `json:"created"`
		Key     string              `json:"key"`
	}
	_ = json.Unmarshal([]byte(created), &c)
	list, _ := callAct(t, s, "list_api_keys", `{}`)
	if strings.Contains(list, c.Key) {
		t.Fatalf("list_api_keys must not leak key material: %s", list)
	}
	if _, err := callAct(t, s, "revoke_api_key", `{"id":"`+c.Created.ID+`"}`); err != nil {
		t.Fatal(err)
	}

	if _, err := callAct(t, s, "set_retention", `{"days":-1}`); err == nil {
		t.Fatal("negative retention must be rejected")
	}
	if _, err := callAct(t, s, "set_retention", `{"days":180}`); err != nil {
		t.Fatal(err)
	}
}

func TestPromptsDispatch(t *testing.T) {
	s := controlServer(t)
	all := []string{
		"instrument-my-app", "whats-broken-today", "weekly-review",
		"monthly-report", "search-performance", "content-gaps",
		"funnel-leak", "channel-review", "retention-review",
		"launch-day", "portfolio-review", "growth-experiments",
	}

	resp := s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "prompts/list"})
	b, _ := json.Marshal(resp.Result)
	var list struct {
		Prompts []struct{ Name, Description string } `json:"prompts"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("prompts/list result: %v", err)
	}
	if len(list.Prompts) != len(all) {
		t.Fatalf("prompts/list returned %d prompts, want %d: %s", len(list.Prompts), len(all), b)
	}
	for _, name := range all {
		if !strings.Contains(string(b), `"`+name+`"`) {
			t.Fatalf("prompts/list missing %s: %s", name, b)
		}
	}

	for _, name := range all {
		resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "prompts/get", Params: json.RawMessage(`{"name":"` + name + `"}`)})
		if resp.Error != nil {
			t.Fatalf("prompts/get %s errored: %+v", name, resp.Error)
		}
		b, _ = json.Marshal(resp.Result)
		if !strings.Contains(string(b), `"messages"`) || !strings.Contains(string(b), `"text"`) {
			t.Fatalf("prompts/get %s should return a text message: %s", name, b)
		}
	}

	// spot-check content: the prompt bodies drive the intended tools
	resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("3"), Method: "prompts/get", Params: json.RawMessage(`{"name":"weekly-review"}`)})
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), "retention") {
		t.Fatalf("weekly-review prompt should mention retention: %s", b)
	}
	resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("4"), Method: "prompts/get", Params: json.RawMessage(`{"name":"content-gaps"}`)})
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), "search_console_report") {
		t.Fatalf("content-gaps prompt should name search_console_report: %s", b)
	}

	resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("5"), Method: "prompts/get", Params: json.RawMessage(`{"name":"nope"}`)})
	if resp.Error == nil {
		t.Fatal("unknown prompt must error")
	}
}

// TestPromptsOnlyNameRealTools guards the contract that every prompt body references
// tools that actually exist: extract tool_-ish tokens and check them against tools/list.
func TestPromptsOnlyNameRealTools(t *testing.T) {
	s := controlServer(t)
	resp := s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	b, _ := json.Marshal(resp.Result)
	tools := string(b)
	for name, text := range promptText {
		for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
			return !(r == '_' || r >= 'a' && r <= 'z')
		}) {
			if !strings.Contains(tok, "_") || strings.HasPrefix(tok, "_") || strings.HasSuffix(tok, "_") {
				continue // only tool-shaped tokens like web_overview
			}
			if tok == "utm_source" || tok == "distinct_id" || tok == "window_hours" || tok == "missing_properties" {
				continue // property/argument names, not tools
			}
			if !strings.Contains(tools, `"`+tok+`"`) {
				t.Errorf("prompt %s references %q which is not in tools/list", name, tok)
			}
		}
	}
}

func TestDeleteUserDataTool(t *testing.T) {
	s := controlServer(t)
	// no confirm → refuse with guidance, nothing deleted
	if _, err := callAct(t, s, "delete_user_data", `{"distinct_id":"u1","confirm":false}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("must demand confirmation, got %v", err)
	}
	out, err := callAct(t, s, "delete_user_data", `{"distinct_id":"u1","confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"deleted_events":3`) {
		t.Fatalf("u1 had 3 events: %s", out)
	}
	// idempotent second call deletes zero
	out, _ = callAct(t, s, "delete_user_data", `{"distinct_id":"u1","confirm":true}`)
	if !strings.Contains(out, `"deleted_events":0`) {
		t.Fatalf("second delete should be 0: %s", out)
	}
}
