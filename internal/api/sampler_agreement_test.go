package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// This is the worst bug the audit found, and it was live.
//
// The GEO runner writes $geo_check into the operator's OWN instance on a daily schedule under
// one synthetic distinct_id. That identity is a user who never misses a day, so it inflates
// every "any activity counts" report. internal/insight knew this and filtered them out — in a
// PRIVATE helper. Nothing else did.
//
// Measured on a live dashboard: the verdict card read "Day-1 retention 4% of 114 users" while
// the ask bar, three inches above it on the same page at the same moment, answered "Day-1
// retention is 6% ... of 119 users". Two numbers for one metric, on the product whose own
// footer promises "ask == dashboard == MCP, all from one report engine".
func TestSamplerEventsAreExcludedFromEverySurface(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	var batch []map[string]any
	add := func(name, user string, at time.Time) {
		batch = append(batch, map[string]any{
			"name": name, "distinct_id": user, "timestamp": at.Format(time.RFC3339),
			"properties": map[string]any{"path": "/"},
		})
	}
	// 40 real users first seen six days ago; a third of them come back the next day, so
	// day-1 retention is a real, measurable number rather than a suppressed zero.
	for i := 0; i < 40; i++ {
		add("$pageview", fmt.Sprintf("u%d", i), now.AddDate(0, 0, -6))
		if i%3 == 0 {
			add("$pageview", fmt.Sprintf("u%d", i), now.AddDate(0, 0, -5))
		}
	}
	// ...and OUR robot, firing every day for a fortnight under one id. Left in, it is a
	// perfectly retained user and it drags day-1 retention up.
	for d := 0; d < 14; d++ {
		add("$geo_check", "geo-runner", now.AddDate(0, 0, -d))
		add("$site_readable", "geo-runner", now.AddDate(0, 0, -d))
	}
	body, _ := json.Marshal(batch) // the ingest takes a bare array
	res, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || res.StatusCode != http.StatusAccepted {
		t.Fatalf("seed ingest failed: %v (%v)", err, res.StatusCode)
	}
	res.Body.Close()

	// what the ask bar says
	askBody, _ := json.Marshal(map[string]any{"question": "how is retention?"})
	ar, err := http.Post(srv.URL+"/v1/ask", "application/json", strings.NewReader(string(askBody)))
	if err != nil {
		t.Fatal(err)
	}
	defer ar.Body.Close()
	var ask struct{ Answer string }
	_ = json.NewDecoder(ar.Body).Decode(&ask)

	// what the verdict says, from the same engine the dashboard card renders
	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var verdictBase int
	for _, f := range insight.Generate(query.Apply(evs, nil)) {
		if f.Kind == insight.KindRetention {
			verdictBase = f.N
		}
	}
	if verdictBase == 0 {
		t.Fatal("no retention finding produced — the fixture no longer exercises the bug")
	}

	// the ask bar prints its own base; the two must be the same population
	askBase := firstIntAfter(ask.Answer, `of (\d+)`)
	if askBase == 0 {
		t.Fatalf("could not read a base out of the ask answer: %q", ask.Answer)
	}
	if askBase != verdictBase {
		t.Errorf("the ask bar and the verdict disagree about who counts as a user:\n"+
			"  ask:     %d users (%q)\n"+
			"  verdict: %d users\n"+
			"one of them is counting this tool's own sampler as a customer", askBase, ask.Answer, verdictBase)
	}
	// and neither may include the robot
	if askBase > 40 {
		t.Errorf("base of %d exceeds the 40 real users seeded — the sampler is being counted", askBase)
	}
}

// The names live as literals in internal/query because query sits underneath the packages that
// define them. This is the joint that holds the two ends together.
func TestSamplerNamesMatchTheirSources(t *testing.T) {
	for _, name := range []string{"$geo_check", "$site_readable", "$ai_crawl"} {
		if !query.IsSampler(name) {
			t.Errorf("%s is no longer recognised as a sampler event — it will start counting as a user", name)
		}
	}
	// a product event must never be swept up by this
	for _, name := range []string{"$pageview", "$click", "signup", "checkout", "$engagement"} {
		if query.IsSampler(name) {
			t.Errorf("%s is being treated as a sampler event — real user activity is being deleted from every report", name)
		}
	}
}

// The AI panes are the documented exception: these events are their INPUT, so filtering them
// there empties the pane instead of cleaning it.
func TestAIVisibilityStillSeesTheSampler(t *testing.T) {
	src, err := os.ReadFile("ask.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "query.WithoutSampler(evs)") {
		t.Error("the ask bar no longer excludes the sampler; it will answer a different retention " +
			"number than the verdict card above it")
	}
}

func firstIntAfter(s, pattern string) int {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n
}

// THE HEADLINE TOTALS, ON THE TWO SURFACES THAT OPEN A SESSION.
//
// The retention check above was written for the verdict-vs-ask-bar divergence and did not reach
// either of these, so both kept counting the robot long after that was fixed:
//
//   - the dashboard's "People · all time" tile, built on distinctUsers(), which counted every
//     DistinctID including $crawler and $sampler
//   - the MCP overview tool, whose own description says "Call this first to orient" — so every
//     agent session opened on inflated total_users and active_users_7d, and everything the agent
//     reasoned afterwards was anchored to them
//
// A sampler identity arrives daily and never misses a day, which is the most flattering possible
// shape for any actives or retention figure. The assertion is a ceiling of REAL seeded people,
// because the exact figure is not the point — being above it at all means the robot is in there.
func TestHeadlineTotalsNeverCountTheSampler(t *testing.T) {
	st := memory.New()
	s := New(st)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	const realPeople = 12
	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < realPeople; i++ {
		for d := 0; d < 5; d++ {
			batch = append(batch, map[string]any{
				"name": "$pageview", "distinct_id": fmt.Sprintf("real%d", i),
				"timestamp":  now.AddDate(0, 0, -d).Format(time.RFC3339),
				"properties": map[string]any{"path": "/"},
			})
		}
	}
	// the robot, every day, under both synthetic identities
	for d := 0; d < 30; d++ {
		at := now.AddDate(0, 0, -d).Format(time.RFC3339)
		batch = append(batch, map[string]any{"name": "$ai_crawl", "distinct_id": "$crawler", "timestamp": at})
		batch = append(batch, map[string]any{"name": "$geo_check", "distinct_id": "$sampler", "timestamp": at})
		batch = append(batch, map[string]any{"name": "$site_readable", "distinct_id": "$sampler", "timestamp": at})
	}
	body, _ := json.Marshal(batch)
	res, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	code := res.StatusCode
	res.Body.Close()
	if code >= 300 {
		t.Fatalf("seed ingest failed with %d", code)
	}

	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	scoped := query.Apply(evs, nil)

	if got := distinctUsers(scoped); got != realPeople {
		t.Errorf("distinctUsers counts %d, but only %d real people were seeded — this is what the "+
			"dashboard's \"People · all time\" tile is built from", got, realPeople)
	}

	// The MCP tool an agent is told to call first — reached over the real transport, so this
	// covers the wiring an editor actually uses and not just the function underneath it.
	call, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "overview", "arguments": map[string]any{}},
	})
	mr, merr := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(string(call)))
	if merr != nil {
		t.Fatal(merr)
	}
	defer mr.Body.Close()
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if derr := json.NewDecoder(mr.Body).Decode(&env); derr != nil {
		t.Fatalf("overview call did not return JSON-RPC: %v", derr)
	}
	if len(env.Result.Content) == 0 {
		t.Fatalf("overview returned no content (status %d)", mr.StatusCode)
	}
	var ov struct {
		TotalUsers    int `json:"total_users"`
		ActiveUsers7d int `json:"active_users_7d"`
	}
	if uerr := json.Unmarshal([]byte(env.Result.Content[0].Text), &ov); uerr != nil {
		t.Fatalf("overview payload was not JSON: %v (%.120s)", uerr, env.Result.Content[0].Text)
	}
	if ov.TotalUsers > realPeople {
		t.Errorf("MCP overview reports %d total users against %d real people — the first number an "+
			"agent ever sees is inflated by this tool's own robot", ov.TotalUsers, realPeople)
	}
	if ov.ActiveUsers7d > realPeople {
		t.Errorf("MCP overview reports %d weekly actives against %d real people", ov.ActiveUsers7d, realPeople)
	}
}
