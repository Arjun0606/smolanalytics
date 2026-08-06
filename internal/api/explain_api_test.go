package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// explainServer seeds a step change: `pre` events a day for two weeks, then `post` a day for two.
func explainServer(t *testing.T, pre, post int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(memory.New()).Handler())
	t.Cleanup(srv.Close)

	now := time.Now().UTC()
	var batch []map[string]any
	for d := 27; d >= 0; d-- {
		n := pre
		if d < 14 {
			n = post
		}
		for i := 0; i < n; i++ {
			ts := now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)
			batch = append(batch, map[string]any{
				"name": "signup", "distinct_id": fmt.Sprintf("d%d_u%d", d, i),
				"timestamp": ts.Format(time.RFC3339)})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	code := resp.StatusCode
	resp.Body.Close()
	if code >= 300 {
		t.Fatalf("seeding failed with %d — the fixture never landed", code)
	}
	return srv
}

func explain(t *testing.T, srv *httptest.Server, q string) map[string]any {
	t.Helper()
	var out map[string]any
	mustJSON(t, getBody(t, srv, "/v1/explain?"+q), &out)
	return out
}

// The whole point: find the day, and say the size of the move in words.
func TestExplainFindsTheDayAndSaysItInWords(t *testing.T) {
	srv := explainServer(t, 40, 10) // a 75% drop, fourteen days ago
	got := explain(t, srv, "event=signup&days=30")

	c, _ := got["change"].(map[string]any)
	if c == nil || c["found"] != true {
		t.Fatalf("a 40/day → 10/day step was not detected: %v", got["verdict"])
	}
	if c["direction"] != "drop" {
		t.Errorf("direction should be drop, got %v", c["direction"])
	}
	if c["day"] == "" || c["day"] == nil {
		t.Error("no day — 'something changed' without a date is not an answer")
	}
	v, _ := c["verdict"].(string)
	if v == "" {
		t.Fatal("no verdict sentence — a series with no reading is the cacophony this replaces")
	}
	// A number and a date, in the sentence, so it can be read without the chart.
	if !strings.Contains(v, "%") {
		t.Errorf("the verdict must state the size of the move: %q", v)
	}
}

// THE REFUSAL. A steady series must not produce a confident story. An automatic change detector
// that always finds a change is a random-cause generator, and one bad call teaches the user to
// distrust every future one.
func TestExplainRefusesToInventAChangeInASteadySeries(t *testing.T) {
	srv := explainServer(t, 30, 30) // flat
	got := explain(t, srv, "event=signup&days=30")
	c, _ := got["change"].(map[string]any)
	if c != nil && c["found"] == true {
		t.Fatalf("invented a change in a flat series: %v", c["verdict"])
	}
	if v, _ := got["verdict"].(string); v == "" {
		t.Error("a not-found answer still needs to say so in words")
	}
}

// It never claims a cause it cannot prove. With no deploys recorded, the read must say what is
// missing and where the answer actually lives — not go quiet, and not guess.
func TestExplainNamesNoCauseItCannotProve(t *testing.T) {
	srv := explainServer(t, 40, 10)
	got := explain(t, srv, "event=signup&days=30")
	read, _ := got["read"].(string)
	if read == "" {
		t.Fatal("a detected change with no next step leaves the user exactly where they started")
	}
	if _, has := got["deploys_near"]; has {
		t.Fatal("no deploys were recorded, yet some were reported")
	}
	// It must point at the place that CAN answer it: the agent, which can read the repo.
	if !strings.Contains(read, "agent") {
		t.Errorf("with no deploys the read should send them to the tool that can read git: %q", read)
	}
}

// A typo'd event must fail loudly, not return an empty flat series that reads as "nothing changed".
func TestExplainRejectsAnEventNobodySends(t *testing.T) {
	srv := explainServer(t, 40, 10)
	resp, err := srv.Client().Get(srv.URL + "/v1/explain?event=singup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("a misspelled event returned %d — an empty series reads as 'steady', which is a "+
			"confident wrong answer about an event that does not exist", resp.StatusCode)
	}
}

// THE FIRST-MONTH BUG. A window that reaches back before the event existed opens with zero-days,
// and a step detector reads the day it first appeared as a 100% jump. That fires for every new
// user on the day they install — the single worst moment to hand someone a fabricated finding, and
// exactly the "it tells me things that aren't true" complaint this endpoint exists to answer.
func TestExplainDoesNotReportTheDayYouInstalledAsAChange(t *testing.T) {
	srv := explainServer(t, 30, 30) // 28 days of flat traffic, asked over a 60-day window
	got := explain(t, srv, "event=signup&days=60")
	if c, _ := got["change"].(map[string]any); c != nil && c["found"] == true {
		t.Fatalf("the start of the data was reported as a change: %v", c["verdict"])
	}
}

// But a metric DYING must stay findable. That is the finding people most need, and it looks
// identical to the install-date artefact if you trim zeros carelessly from both ends.
func TestExplainStillCatchesAMetricGoingToZero(t *testing.T) {
	srv := explainServer(t, 40, 0) // fourteen days of traffic, then nothing
	got := explain(t, srv, "event=signup&days=30")
	c, _ := got["change"].(map[string]any)
	if c == nil || c["found"] != true {
		t.Fatalf("a metric that went to zero and stayed there was not reported: %v", got["verdict"])
	}
	if c["direction"] != "drop" {
		t.Errorf("going to zero is a drop, got %v", c["direction"])
	}
}

// Asking about the AI-crawler feed is a fair question, and the sampler filter every other activity
// report uses would delete every event here and answer "steady at zero" — silently, confidently,
// about a pane this product sells.
func TestExplainCanAnswerAboutOurOwnSamplerEvents(t *testing.T) {
	srv := explainServer(t, 1, 1)
	now := time.Now().UTC()
	var batch []map[string]any
	for d := 27; d >= 0; d-- {
		n := 5
		if d < 14 {
			n = 60
		}
		for i := 0; i < n; i++ {
			batch = append(batch, map[string]any{
				"name": "$ai_crawl", "distinct_id": "$crawler",
				"timestamp": now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute).Format(time.RFC3339)})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got := explain(t, srv, "event=$ai_crawl&days=30")
	c, _ := got["change"].(map[string]any)
	if c == nil || c["found"] != true {
		t.Fatalf("a 12x rise in AI-crawler traffic was invisible — the sampler filter ate its own "+
			"input: %v", got["verdict"])
	}
}

// ASK == MCP, ON THE ONE TOOL WHERE THEY DID NOT.
//
// explain_change read the raw store while /v1/explain applied production scope — the only tool
// across the entire MCP surface that skipped it. So "why did signup change?" could name a
// different day, or a different size of move, depending on whether it was asked from an editor or
// from the page, about the same event on the same instance.
//
// It is the footer promise ("ask == dashboard == MCP, all from one report engine") failing on the
// feature that exists to explain a number, which is the worst possible place for it: a reader who
// notices the discrepancy learns not to trust the explanation OR the number.
func TestExplainAgreesBetweenHTTPAndMCP(t *testing.T) {
	srv := explainServer(t, 40, 10) // a real, detectable drop

	var got map[string]any
	mustJSON(t, getBody(t, srv, "/v1/explain?event=signup&days=30"), &got)
	httpChange, _ := got["change"].(map[string]any)
	if httpChange == nil || httpChange["found"] != true {
		t.Fatalf("the HTTP side found no change to compare against: %v", got["verdict"])
	}

	call, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "explain_change",
			"arguments": map[string]any{"event": "signup", "days": 30},
		},
	})
	resp, err := srv.Client().Post(srv.URL+"/mcp", "application/json", strings.NewReader(string(call)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&env); derr != nil {
		t.Fatalf("explain_change did not return JSON-RPC: %v", derr)
	}
	if len(env.Result.Content) == 0 {
		t.Fatalf("explain_change returned no content (status %d)", resp.StatusCode)
	}
	var mcpOut map[string]any
	if uerr := json.Unmarshal([]byte(env.Result.Content[0].Text), &mcpOut); uerr != nil {
		t.Fatalf("explain_change payload was not JSON: %v", uerr)
	}
	mcpChange, _ := mcpOut["change"].(map[string]any)
	if mcpChange == nil {
		t.Fatal("explain_change returned no change block")
	}

	for _, k := range []string{"found", "day", "direction"} {
		if fmt.Sprint(httpChange[k]) != fmt.Sprint(mcpChange[k]) {
			t.Errorf("%s disagrees: /v1/explain says %v, MCP explain_change says %v — one question, "+
				"two answers, decided by which doorway you came through",
				k, httpChange[k], mcpChange[k])
		}
	}
}
