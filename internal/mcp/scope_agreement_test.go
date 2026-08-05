package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// mixedEnvServer seeds the shape a solo dev's store actually has: production traffic sitting
// next to the localhost and branch-preview events sdk.js auto-stamps env=development /
// env=preview. query.Apply hides those from every dashboard report, so any MCP tool that reads
// s.all() without scoping answers a question the dashboard answers differently — and the user
// has no way to tell which surface is lying.
//
// prodUsers convert signup -> activate -> checkout; devUsers only ever signup, which is what
// makes the unscoped read look like a catastrophic funnel leak that does not exist.
func mixedEnvServer(t *testing.T, prodUsers, devUsers int) (*Server, []event.Event) {
	t.Helper()
	st := memory.New()
	base := time.Now().UTC().Add(-72 * time.Hour)
	var evs []event.Event
	ev := func(u, n, env string, off time.Duration) {
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("%s-%s-%d", u, n, off), DistinctID: u, Name: n,
			Timestamp: base.Add(off), Properties: map[string]any{"env": env, "source": "google"},
		})
	}
	for i := 0; i < prodUsers; i++ {
		u := fmt.Sprintf("p%d", i)
		ev(u, "signup", "production", 0)
		if i%4 != 0 { // 75% activate
			ev(u, "activate", "production", time.Hour)
		}
		if i%5 != 0 { // 80% of those get to checkout
			ev(u, "checkout", "production", 2*time.Hour)
		}
	}
	for i := 0; i < devUsers; i++ {
		ev(fmt.Sprintf("d%d", i), "signup", "development", 0)
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}
	return New(st), evs
}

// overview is the tool the server instructions tell the model to call FIRST, so it is the first
// number the user hears. It counted the raw store while trends, breakdown, web_overview and the
// dashboard all counted the production scope — measured on a mixed-env fixture, overview said
// 140 users where trends(unique=true) said 40.
func TestOverviewCountsTheSameUsersTrendsDoes(t *testing.T) {
	s, _ := mixedEnvServer(t, 40, 100)

	var ov struct {
		TotalUsers   int `json:"total_users"`
		ActiveUsers7 int `json:"active_users_7d"`
	}
	out, err := s.callTool("overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &ov); err != nil {
		t.Fatalf("overview returned invalid JSON: %v\n%s", err, out)
	}

	var tr struct {
		Total float64 `json:"total"`
	}
	out, err = s.callTool("trends", mustJSON(map[string]any{"event": "signup", "days": 7, "unique": true}))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &tr); err != nil {
		t.Fatalf("trends returned invalid JSON: %v\n%s", err, out)
	}

	if ov.TotalUsers != 40 {
		t.Errorf("overview total_users = %d, want 40 — the 100 env=development users are in the count", ov.TotalUsers)
	}
	if ov.ActiveUsers7 != 40 {
		t.Errorf("overview active_users_7d = %d, want 40", ov.ActiveUsers7)
	}
	if float64(ov.TotalUsers) != tr.Total {
		t.Errorf("overview says %d users, trends(unique=true) says %v — the orient tool disagrees with the report beside it",
			ov.TotalUsers, tr.Total)
	}
}

// whats_notable is the verdict an agent reads in the editor; the dashboard's verdict card and
// the anomaly-alert path both compute their findings over query.Apply'd events. This one read
// the raw slice, so the two surfaces diagnosed different data: on this fixture the unscoped
// read reports a funnel leak of ~110 people at a 21% activation rate, the scoped one 10 people
// at 75%. Same product, two verdicts, and only one of them is reproducible anywhere in the UI.
func TestWhatsNotableUsesTheSameScopeTheDashboardVerdictDoes(t *testing.T) {
	s, evs := mixedEnvServer(t, 40, 100)

	out, err := s.callTool("whats_notable", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Findings []insight.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("whats_notable returned invalid JSON: %v\n%s", err, out)
	}

	scoped := jsonStr(insight.Generate(query.Apply(evs, nil)))
	raw := jsonStr(insight.Generate(evs))
	if scoped == raw {
		t.Fatal("fixture does not discriminate: scoped and unscoped findings are identical, so this test proves nothing")
	}
	if jsonStr(got.Findings) != scoped {
		t.Errorf("whats_notable findings differ from the ones the dashboard verdict card computes.\nMCP:       %s\ndashboard: %s", jsonStr(got.Findings), scoped)
	}
}

// run_sql is the escape hatch, and an escape hatch that answers the same question with a
// different number is worse than no escape hatch: it scanned the store raw while every built
// report applied the production scope, so the grammar's own worked example counted localhost
// users. The opt-back-in is naming prop.env in the WHERE, mirroring query.Keeper's rule.
func TestRunSQLInheritsTheProductionScopeAndSaysSo(t *testing.T) {
	s, _ := mixedEnvServer(t, 40, 100)

	users := func(m map[string]any) float64 {
		t.Helper()
		rows, _ := m["rows"].([]any)
		if len(rows) != 1 {
			t.Fatalf("expected exactly one row, got %v", m["rows"])
		}
		return rows[0].(map[string]any)["users"].(float64)
	}

	m := sqlCall(t, s, "SELECT count(distinct distinct_id) AS users FROM events WHERE name = 'signup'")
	if got := users(m); got != 40 {
		t.Errorf("run_sql counted %v signup users, trends(unique=true) counts 40 — the escape hatch sees dev traffic nothing else sees", got)
	}
	if note, _ := m["scope"].(string); note == "" {
		t.Error("run_sql must state its scope in the result: a model comparing this to trends has to be able to explain a difference rather than pick a side")
	}

	// naming prop.env is the documented way to ask for the other environments back.
	m = sqlCall(t, s, "SELECT count(distinct distinct_id) AS users FROM events WHERE prop.env = 'development'")
	if got := users(m); got != 100 {
		t.Errorf("WHERE prop.env = 'development' returned %v users, want 100 — the opt-back-in must step the default scope aside, not AND with it", got)
	}
}

// The grammar in the tool description is the only thing a model sees before it writes a query,
// so the scope has to be in it. A model that does not know the rows are production-only cannot
// explain why its SQL and its trends call disagree with the number a user quotes back.
func TestRunSQLDescriptionDocumentsTheScope(t *testing.T) {
	for _, tool := range toolList {
		if tool["name"] != "run_sql" {
			continue
		}
		desc, _ := tool["description"].(string)
		for _, must := range []string{"prop.env", "development"} {
			if !strings.Contains(desc, must) {
				t.Errorf("run_sql description never mentions %q — the model cannot know it is seeing a scoped view", must)
			}
		}
		return
	}
	t.Fatal("run_sql is not in toolList")
}
