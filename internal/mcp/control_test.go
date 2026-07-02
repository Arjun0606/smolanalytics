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
		Healthy   bool             `json:"healthy"`
		Planned   []map[string]any `json:"planned"`
		Unplanned []string         `json:"unplanned_events"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Healthy {
		t.Fatal("checkout missing + signup missing 'source' → must be unhealthy")
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
	resp := s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "prompts/list"})
	b, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(b), "instrument-my-app") {
		t.Fatalf("prompts/list missing instrument-my-app: %s", b)
	}
	resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "prompts/get", Params: json.RawMessage(`{"name":"weekly-review"}`)})
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), "retention") {
		t.Fatalf("weekly-review prompt should mention retention: %s", b)
	}
	resp = s.Dispatch(request{JSONRPC: "2.0", ID: json.RawMessage("3"), Method: "prompts/get", Params: json.RawMessage(`{"name":"nope"}`)})
	if resp.Error == nil {
		t.Fatal("unknown prompt must error")
	}
}
