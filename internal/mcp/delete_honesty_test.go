package mcp

// The delete tools are agent-facing, so their answer IS the report the user gets.
// Every one of them used to reply {"deleted": "<whatever you passed>"} whether or
// not the thing existed: an agent told to "delete the checkout flag" that typo'd the
// key read that as a confirmed removal and told the user the flag was gone while it
// stayed live. These tests pin the honest contract in both directions — deleted:true
// only when a row really went away, deleted:false plus a self-correcting note when
// nothing matched, and a not-found is still an RPC success so retries work.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/defined"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/survey"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

// fullServer wires every persistent store so the whole delete/revoke surface is
// reachable from one server.
func fullServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	now := time.Now().UTC()
	for _, name := range []string{"signup", "checkout"} {
		if err := st.Ingest(event.Event{ID: name, Name: name, DistinctID: "u1", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	at := func(n string) string { return filepath.Join(dir, n) }
	s := New(st)

	ins, err := insights.Open(at("insights.json"))
	if err != nil {
		t.Fatal(err)
	}
	coh, err := cohort.Open(at("cohorts.json"))
	if err != nil {
		t.Fatal(err)
	}
	wh, err := webhook.Open(at("webhooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	al, err := alert.Open(at("alerts.json"))
	if err != nil {
		t.Fatal(err)
	}
	gl, err := goal.Open(at("goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	sh, err := share.Open(at("shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	dp, err := deploys.Open(at("deploys.json"))
	if err != nil {
		t.Fatal(err)
	}
	fl, err := flag.Open(at("flags.json"))
	if err != nil {
		t.Fatal(err)
	}
	sv, err := survey.Open(at("surveys.json"))
	if err != nil {
		t.Fatal(err)
	}
	df, err := defined.Open(at("defined.json"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := settings.Open(at("settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	s.SetInsights(ins)
	s.SetCohorts(coh)
	s.SetWebhooks(wh)
	s.SetAlerts(al)
	s.SetGoals(gl)
	s.SetShares(sh)
	s.SetDeploys(dp)
	s.SetFlags(fl)
	s.SetSurveys(sv)
	s.SetDefined(df)
	s.SetSettings(set)
	return s
}

// removalOf parses a delete/revoke tool's payload.
type removalPayload struct {
	Deleted bool   `json:"deleted"`
	Note    string `json:"note"`
}

func removalOf(t *testing.T, out string) removalPayload {
	t.Helper()
	var r removalPayload
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("delete payload isn't JSON: %s", out)
	}
	return r
}

// idFrom digs the new row's identifier out of a create-tool response, whose wrapper
// key differs per tool ("created", "saved", "flag", …).
func idFrom(t *testing.T, out, wrapper, field string) string {
	t.Helper()
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &outer); err != nil {
		t.Fatalf("create payload isn't JSON: %s", out)
	}
	raw, ok := outer[wrapper]
	if !ok {
		t.Fatalf("no %q key in create payload: %s", wrapper, out)
	}
	var inner map[string]any
	if err := json.Unmarshal(raw, &inner); err != nil {
		t.Fatalf("%q isn't an object in: %s", wrapper, out)
	}
	v, _ := inner[field].(string)
	if v == "" {
		t.Fatalf("no %s.%s in create payload: %s", wrapper, field, out)
	}
	return v
}

// A delete of something that never existed must NOT read as a removal. It stays an
// RPC success (deleting twice is a legitimate retry) but says deleted:false and
// names the list tool that shows what really is there.
func TestDeleteOfNonexistentNeverClaimsRemoval(t *testing.T) {
	s := fullServer(t)
	cases := []struct {
		tool, args, wantListTool string
	}{
		{"delete_flag", `{"key":"never_existed_xyz"}`, "list_flags"},
		{"delete_survey", `{"id":"nope123"}`, "list_surveys"},
		{"delete_cohort", `{"id":"nope123"}`, "list_cohorts"},
		{"delete_defined_event", `{"name":"totally_made_up_xyz"}`, "list_defined_events"},
		{"delete_goal", `{"id":"nope123"}`, "list_goals"},
		{"delete_alert", `{"id":"nope123"}`, "list_alerts"},
		{"delete_webhook", `{"id":"nope123"}`, "list_webhooks"},
		{"delete_deploy", `{"id":"nope123"}`, "list_deploys"},
		{"delete_saved_report", `{"id":"nope123"}`, "list_saved_reports"},
		{"revoke_share_link", `{"id":"nope123"}`, "list_share_links"},
		{"revoke_api_key", `{"id":"nope123"}`, "list_api_keys"},
	}
	for _, tc := range cases {
		out, err := callAct(t, s, tc.tool, tc.args)
		if err != nil {
			t.Fatalf("%s: a miss must stay a success so retries work, got error: %v", tc.tool, err)
		}
		r := removalOf(t, out)
		if r.Deleted {
			t.Fatalf("%s claimed it deleted something that never existed: %s", tc.tool, out)
		}
		if !strings.Contains(out, `"deleted":false`) {
			t.Fatalf("%s payload must carry deleted:false, got: %s", tc.tool, out)
		}
		if !strings.Contains(r.Note, tc.wantListTool) {
			t.Fatalf("%s note must point at %s so the agent can self-correct, got: %s", tc.tool, tc.wantListTool, out)
		}
		if !strings.Contains(r.Note, "nothing was deleted") {
			t.Fatalf("%s note must say nothing was deleted, got: %s", tc.tool, out)
		}
	}
}

// The mirror image: deleting a row that really is there reports deleted:true, no
// hedging note, and the row is actually gone afterwards.
func TestDeleteOfRealRowReportsTrue(t *testing.T) {
	s := fullServer(t)

	create := func(tool, args string) string {
		t.Helper()
		out, err := callAct(t, s, tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	cases := []struct {
		name, deleteTool, id, listTool string
	}{
		{
			name:       "flag",
			deleteTool: "delete_flag",
			id:         idFrom(t, create("create_flag", `{"key":"checkout_v2"}`), "flag", "key"),
			listTool:   "list_flags",
		},
		{
			name:       "survey",
			deleteTool: "delete_survey",
			id:         idFrom(t, create("create_survey", `{"name":"nps","type":"nps","question":"how likely?"}`), "survey", "id"),
			listTool:   "list_surveys",
		},
		{
			name:       "cohort",
			deleteTool: "delete_cohort",
			id:         idFrom(t, create("create_cohort", `{"name":"Paying","events":["checkout"]}`), "created", "id"),
			listTool:   "list_cohorts",
		},
		{
			name:       "defined event",
			deleteTool: "delete_defined_event",
			id:         idFrom(t, create("define_event", `{"name":"buy_click","event":"$click","where":[{"field":"text","op":"contains","value":"Buy"}]}`), "defined", "name"),
			listTool:   "list_defined_events",
		},
		{
			name:       "goal",
			deleteTool: "delete_goal",
			id:         idFrom(t, create("create_goal", `{"name":"Signed up","kind":"event","value":"signup"}`), "created", "id"),
			listTool:   "list_goals",
		},
		{
			name:       "alert",
			deleteTool: "delete_alert",
			id:         idFrom(t, create("create_alert", `{"name":"signup drop","event":"signup","op":"lt","threshold":10,"window_hours":24}`), "created", "id"),
			listTool:   "list_alerts",
		},
		{
			name:       "webhook",
			deleteTool: "delete_webhook",
			id:         idFrom(t, create("add_webhook", `{"name":"ops","url":"https://example.com/hook"}`), "created", "id"),
			listTool:   "list_webhooks",
		},
		{
			name:       "deploy",
			deleteTool: "delete_deploy",
			id:         idFrom(t, create("record_deploy", `{"message":"new checkout"}`), "recorded", "id"),
			listTool:   "list_deploys",
		},
		{
			name:       "saved report",
			deleteTool: "delete_saved_report",
			id:         idFrom(t, create("save_report", `{"name":"Signup trend","type":"trend","params":{"event":"signup"}}`), "saved", "id"),
			listTool:   "list_saved_reports",
		},
		{
			name:       "share link",
			deleteTool: "revoke_share_link",
			id:         idFrom(t, create("create_share_link", `{"name":"investor update"}`), "created", "id"),
			listTool:   "list_share_links",
		},
		{
			name:       "api key",
			deleteTool: "revoke_api_key",
			id:         idFrom(t, create("create_api_key", `{"name":"production web"}`), "created", "id"),
			listTool:   "list_api_keys",
		},
	}

	for _, tc := range cases {
		field := "id"
		if tc.deleteTool == "delete_flag" {
			field = "key"
		}
		if tc.deleteTool == "delete_defined_event" {
			field = "name"
		}
		out, err := callAct(t, s, tc.deleteTool, `{"`+field+`":"`+tc.id+`"}`)
		if err != nil {
			t.Fatalf("%s: %v", tc.deleteTool, err)
		}
		r := removalOf(t, out)
		if !r.Deleted {
			t.Fatalf("%s removed a real %s but reported deleted:false: %s", tc.deleteTool, tc.name, out)
		}
		if r.Note != "" {
			t.Fatalf("%s: a real deletion needs no hedging note, got: %s", tc.deleteTool, out)
		}
		if list, err := callAct(t, s, tc.listTool, `{}`); err != nil || strings.Contains(list, tc.id) {
			t.Fatalf("%s: %s still shows %q after a deleted:true: %s (%v)", tc.deleteTool, tc.listTool, tc.id, list, err)
		}
		// deleting the SAME id again is a legitimate retry: still a success, but it
		// must stop claiming a removal.
		again, err := callAct(t, s, tc.deleteTool, `{"`+field+`":"`+tc.id+`"}`)
		if err != nil {
			t.Fatalf("%s: re-delete must stay a success, got: %v", tc.deleteTool, err)
		}
		if removalOf(t, again).Deleted {
			t.Fatalf("%s: re-deleting an already-gone %s claimed a second removal: %s", tc.deleteTool, tc.name, again)
		}
	}
}

// delete_user_data reports a real count from the store, so it was already honest.
// Pin that: erasing a distinct_id that has no events reports 0, never a bare success.
func TestDeleteUserDataReportsRealCount(t *testing.T) {
	s := fullServer(t)
	out, err := callAct(t, s, "delete_user_data", `{"distinct_id":"never_existed_xyz","confirm":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"deleted_events":0`) {
		t.Fatalf("erasing an unknown user must report 0 events, got: %s", out)
	}
}

// A wrong-shaped `where` used to come back as encoding/json's raw error
// ("cannot unmarshal string into Go struct field .where of type []defined.Condition"),
// which names internal Go types and tells an agent nothing about the fix. It must be
// a house-style message carrying the shape AND a working example.
func TestDefineEventBadWhereShapeIsGuiding(t *testing.T) {
	s := fullServer(t)
	for _, args := range []string{
		`{"name":"checkout","event":"$click","where":"text contains Buy"}`,
		`{"name":"checkout","event":"$click","where":42}`,
		`{"name":"checkout","event":"$click","where":true}`,
	} {
		_, err := callAct(t, s, "define_event", args)
		if err == nil {
			t.Fatalf("a wrong-shaped where must error, got success for %s", args)
		}
		msg := err.Error()
		for _, leak := range []string{"cannot unmarshal", "Go struct", "Go value", "defined.Condition", "json:"} {
			if strings.Contains(msg, leak) {
				t.Fatalf("where error leaks Go internals (%q): %s", leak, msg)
			}
		}
		if !strings.Contains(msg, `[{"field":"text","op":"contains","value":"Buy"}]`) {
			t.Fatalf("where error must show a working example, got: %s", msg)
		}
		if !strings.Contains(msg, "equals|contains|prefix") {
			t.Fatalf("where error must name the valid ops, got: %s", msg)
		}
	}

	// the shorthand an agent naturally reaches for — one condition without the array
	// wrapper — decodes instead of erroring.
	out, err := callAct(t, s, "define_event", `{"name":"buy_click","event":"$click","where":{"field":"text","op":"contains","value":"Buy"}}`)
	if err != nil {
		t.Fatalf("a single condition object should decode as a one-element where: %v", err)
	}
	if !strings.Contains(out, `"field":"text"`) {
		t.Fatalf("the bare condition object was dropped: %s", out)
	}
}

// The JSON-RPC envelope had the same leak one level up: a mis-shaped tools/call
// params echoed encoding/json's error, Go type names and all.
func TestBadToolsCallParamsDoNotLeakGoInternals(t *testing.T) {
	s := fullServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":["list_events"]}}`)
	if r == nil || r.Error == nil {
		t.Fatalf("a mis-shaped params envelope must be an explicit error, got %+v", r)
	}
	msg := r.Error.Message
	for _, leak := range []string{"cannot unmarshal", "Go struct", "Go value", "json:"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("params error leaks Go internals (%q): %s", leak, msg)
		}
	}
	if !strings.Contains(msg, `"name"`) || !strings.Contains(msg, `"arguments"`) {
		t.Fatalf("params error must name the shape it wants, got: %s", msg)
	}
}
