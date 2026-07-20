package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The deploy-impact number the dashboard shows MUST equal the one the editor gets over MCP —
// both call deploys.Report over the same scoped events, and this pins them together. If they
// ever drift, this fails the build, so the "which commit moved the metric" claim can't lie.
func TestDeployImpactAgreement(t *testing.T) {
	st := memory.New()
	app := New(st)
	dp, _ := deploys.Open("") // in-memory store, shared by the HTTP + MCP surfaces
	app.SetDeploys(dp)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	// seed a signup series with an obvious before/after break around a deploy
	now := time.Now().UTC()
	var batch []map[string]any
	add := func(name string, ageDays int, n int) {
		for i := 0; i < n; i++ {
			batch = append(batch, map[string]any{
				"name": name, "distinct_id": fmt.Sprintf("u%d_%d_%d", ageDays, i, len(batch)),
				"timestamp": now.AddDate(0, 0, -ageDays).Format(time.RFC3339),
			})
		}
	}
	for d := 8; d >= 6; d-- {
		add("signup", d, 20)
	} // 3 days at 20/day (before)
	for d := 4; d >= 2; d-- {
		add("signup", d, 8)
	} // 3 days at 8/day (after)
	body, _ := json.Marshal(batch)
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed failed: %v %v", err, resp)
	}

	// a deploy 5 days ago, right on the break
	if _, err := dp.Record(deploys.Deploy{SHA: "deadbeef1234", Message: "tighten signup", At: now.AddDate(0, 0, -5)}); err != nil {
		t.Fatal(err)
	}

	// list parity: GET /v1/deploys == MCP list_deploys
	if a, m := viaAPI(t, srv, "/v1/deploys"), viaMCP(t, srv, "list_deploys", `{}`); !reflect.DeepEqual(a, m) {
		t.Errorf("list_deploys disagreement:\n api=%v\n mcp=%v", a, m)
	}

	// impact parity: GET /v1/deploys?event=signup == MCP deploy_impact{event:signup}
	a := viaAPI(t, srv, "/v1/deploys?event=signup")
	m := viaMCP(t, srv, "deploy_impact", `{"event":"signup"}`)
	if !reflect.DeepEqual(a, m) {
		t.Errorf("deploy_impact disagreement:\n api=%v\n mcp=%v", a, m)
	}
	// and it actually computed a regression (sanity: the seed is a real drop)
	deps, _ := a["deploys"].([]any)
	if len(deps) == 0 {
		t.Fatal("expected at least one deploy in the impact")
	}
	row, _ := deps[0].(map[string]any)
	if row["direction"] != "regression" || row["significant"] != true {
		t.Errorf("seed is a 20→8 drop, expected significant regression, got dir=%v sig=%v", row["direction"], row["significant"])
	}
}
