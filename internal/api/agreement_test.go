package api

// The agreement test: CI enforcement of the product's core promise. The answer your
// AI gives over MCP is byte-for-byte the SAME computation the HTTP API returns and the
// dashboard renders. There is no second query path that can drift (competitors' AI
// layers generate queries and admit results "may not match the UI"; ours cannot,
// and this test is why that claim stays true forever). If a default, a window, or a
// boundary ever diverges between surfaces, this fails the build.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/survey"
)

func agreementServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	h := New(st).Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("u%d", i)
		age := time.Duration(i%14) * 24 * time.Hour // spread across two weeks
		batch = append(batch, map[string]any{
			"name": "signup", "distinct_id": u, "timestamp": now.Add(-age).Format(time.RFC3339),
			"properties": map[string]any{"plan": map[bool]string{true: "pro", false: "free"}[i%3 == 0], "path": "/"},
		})
		if i%2 == 0 {
			batch = append(batch, map[string]any{
				"name": "activate", "distinct_id": u, "timestamp": now.Add(-age + time.Hour).Format(time.RFC3339),
			})
		}
		if i%4 == 0 {
			batch = append(batch, map[string]any{
				"name": "checkout", "distinct_id": u, "timestamp": now.Add(-age + 2*time.Hour).Format(time.RFC3339),
				"properties": map[string]any{"amount": float64(20 + (i%3)*10)}, // varies 20/30/40 for measure tests
			})
		}
		batch = append(batch, map[string]any{
			"name": "$pageview", "distinct_id": u, "timestamp": now.Add(-age).Format(time.RFC3339),
			"properties": map[string]any{"path": "/pricing", "referrer": "https://news.ycombinator.com/", "device": "desktop"},
		})
	}
	// agent-observability seed: agent_tool_call (two tools, two clients, some errored with a
	// type, varied latency) and agent_turn (several conversations with roles, TTFT, a resolved
	// bool) — so /v1/agent/* has real data to agree with the MCP tools over, not an empty result.
	toolCall := func(i int, tool, client string, latency float64, errored bool, errType string) {
		p := map[string]any{"tool": tool, "client": client, "latency_ms": latency,
			"error": errored, "conversation_id": fmt.Sprintf("c%d", i%5)}
		if errType != "" {
			p["error_type"] = errType
		}
		batch = append(batch, map[string]any{"name": "agent_tool_call",
			"distinct_id": fmt.Sprintf("a%d", i), "timestamp": now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
			"properties": p})
	}
	for i := 0; i < 30; i++ {
		tool := map[bool]string{true: "search", false: "write"}[i%3 != 0]
		client := map[bool]string{true: "cursor", false: "claude-code"}[i%2 == 0]
		errored := i%5 == 0
		errType := ""
		if errored {
			errType = map[bool]string{true: "timeout", false: "rate_limit"}[i%2 == 0]
		}
		toolCall(i, tool, client, float64(50+(i%7)*25), errored, errType)
	}
	turn := func(cid, role string, min int, extra map[string]any) {
		p := map[string]any{"conversation_id": cid, "role": role}
		for k, v := range extra {
			p[k] = v
		}
		batch = append(batch, map[string]any{"name": "agent_turn",
			"distinct_id": cid, "timestamp": now.Add(-time.Duration(min) * time.Minute).Format(time.RFC3339),
			"properties": p})
	}
	// conv t0: user, assistant(ttft, resolved true), user, user  (re-ask + abandon)
	turn("t0", "user", 40, nil)
	turn("t0", "assistant", 39, map[string]any{"ttft_ms": float64(120), "resolved": true})
	turn("t0", "user", 38, nil)
	turn("t0", "user", 37, nil)
	// conv t1: user, assistant(ttft, resolved false)  (clean)
	turn("t1", "user", 30, nil)
	turn("t1", "assistant", 29, map[string]any{"ttft_ms": float64(340), "resolved": false})
	// conv t2: user, user  (re-ask + abandon), no resolved on this one
	turn("t2", "user", 20, nil)
	turn("t2", "user", 19, map[string]any{"ttft_ms": float64(210)})
	// model-written labels (the BYO-model seam): t0 and t1 labeled by two different models, t2
	// deliberately left unlabeled so the labeled/unlabeled honesty counts are non-trivial. The
	// VALUES are inferences; the counts over them must agree across /v1 and MCP like everything
	// else. t0 is labeled twice so the latest-write-wins rule is exercised on both surfaces.
	label := func(cid, by string, min int, labels map[string]any) {
		p := map[string]any{"conversation_id": cid, "labeled_by": by,
			"labeled_at": now.Add(-time.Duration(min) * time.Minute).Format(time.RFC3339)}
		for k, v := range labels {
			p[k] = v
		}
		batch = append(batch, map[string]any{"name": "agent_label",
			"distinct_id": cid, "timestamp": now.Add(-time.Duration(min) * time.Minute).Format(time.RFC3339),
			"properties": p})
	}
	label("t0", "gpt-5", 15, map[string]any{"intent": "onboarding"})
	label("t0", "claude-sonnet-4-5", 10, map[string]any{"intent": "billing", "sentiment": "negative"})
	label("t1", "claude-sonnet-4-5", 9, map[string]any{"intent": "billing"})
	body, _ := json.Marshal(batch)
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed ingest failed: %v %v", err, resp)
	}
	return srv
}

func viaAPI(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %v (%v)", path, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func viaMCP(t *testing.T, srv *httptest.Server, tool, args string) map[string]any {
	t.Helper()
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, args)
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(req))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("mcp %s: %v (%v)", tool, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var envelope struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.IsError {
		t.Fatalf("mcp %s returned error: %s", tool, envelope.Result.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMCPAPIAgreement: the same question through both public surfaces must produce
// the identical answer — same defaults, same windows, same boundaries, same numbers.
func TestMCPAPIAgreement(t *testing.T) {
	srv := agreementServer(t)

	filters := `[{"property":"plan","op":"eq","value":"pro"}]`
	inFilters := `[{"property":"plan","op":"in","value":["pro","free"]}]`
	cases := []struct {
		name   string
		api    string
		tool   string
		args   string
		subset bool // MCP may ADD model-facing context (notes, extra counts) but every API field must match
	}{
		{"funnel default window", "/v1/funnel?steps=signup,activate,checkout", "funnel", `{"steps":["signup","activate","checkout"]}`, false},
		{"funnel filtered", "/v1/funnel?steps=signup,activate&filters=" + urlEnc(filters), "funnel", `{"steps":["signup","activate"],"filters":` + filters + `}`, false},
		{"funnel filtered in-list", "/v1/funnel?steps=signup,activate&filters=" + urlEnc(inFilters), "funnel", `{"steps":["signup","activate"],"filters":` + inFilters + `}`, false},
		{"funnel breakdown by property", "/v1/funnel?steps=signup,activate&breakdown=plan", "funnel", `{"steps":["signup","activate"],"breakdown":"plan"}`, false},
		{"trends", "/v1/trends?event=signup", "trends", `{"event":"signup"}`, false},
		// days=N is the windowed path: MCP once used a rolling from=now-N*24h while /v1
		// used calendar-day alignment, so MCP prepended a phantom leading day. Lock the
		// bucket list identical across surfaces.
		{"trends 7-day window", "/v1/trends?event=signup&days=7", "trends", `{"event":"signup","days":7}`, false},
		{"trends measure sum (revenue)", "/v1/trends?event=checkout&measure=sum&property=amount", "trends", `{"event":"checkout","measure":"sum","property":"amount"}`, false},
		{"trends measure avg (AOV)", "/v1/trends?event=checkout&measure=avg&property=amount", "trends", `{"event":"checkout","measure":"avg","property":"amount"}`, false},
		{"trends measure p90", "/v1/trends?event=checkout&measure=p90&property=amount", "trends", `{"event":"checkout","measure":"p90","property":"amount"}`, false},
		{"retention", "/v1/retention?days=7&event=signup", "retention", `{"days":7,"event":"signup"}`, true},
		{"retention weekly bucket", "/v1/retention?days=4&event=signup&bucket=week", "retention", `{"days":4,"event":"signup","bucket":"week"}`, true},
		{"retention rolling", "/v1/retention?days=7&event=signup&rolling=true", "retention", `{"days":7,"event":"signup","rolling":true}`, true},
		{"retention capped at 90 both sides", "/v1/retention?days=500&event=signup", "retention", `{"days":500,"event":"signup"}`, true},
		{"web overview", "/v1/web?days=30", "web_overview", `{"days":30}`, false},
		{"lifecycle capped at 180 both sides", "/v1/lifecycle?days=500", "lifecycle", `{"days":500}`, false},
		{"paths capped at 10 both sides", "/v1/paths?start=signup&depth=50", "paths", `{"start":"signup","depth":50}`, false},
		{"ai visibility", "/v1/ai-visibility?days=90", "ai_visibility", `{"days":90}`, false},
		// the fix brief is a REPORT, not a UI helper: the sheet, the endpoint and the tool
		// must hand out one story about a finding, not three
		{"fix brief lead finding", "/v1/fix-brief", "fix_brief", `{}`, false},
		{"fix brief over an explicit funnel", "/v1/fix-brief?steps=signup,activate,checkout", "fix_brief", `{"steps":["signup","activate","checkout"]}`, false},
		{"ai visibility capped at 365 both sides", "/v1/ai-visibility?days=999", "ai_visibility", `{"days":999}`, false},
		// a "/"-prefixed start flips both surfaces into page-to-page navigation mode
		{"paths page mode", "/v1/paths?start=/pricing", "paths", `{"start":"/pricing"}`, false},
		{"heatmap", "/v1/heatmap?path=/pricing", "heatmap", `{"path":"/pricing"}`, false},
		{"heatmap desktop bucket + grid", "/v1/heatmap?path=/pricing&viewport=desktop&cols=20&row_px=40", "heatmap", `{"path":"/pricing","viewport":"desktop","cols":20,"row_px":40}`, false},
		// agent observability: /v1/agent/* must equal the MCP agent tools byte-for-byte.
		{"agent tools", "/v1/agent/tools", "agent_tools", `{}`, false},
		{"agent errors", "/v1/agent/errors", "agent_errors", `{}`, false},
		{"agent errors by tool", "/v1/agent/errors?tool=search", "agent_errors", `{"tool":"search"}`, false},
		{"agent conversations", "/v1/agent/conversations", "agent_conversations", `{}`, false},
		// the BYO-model seam: the LABELS are a model's inference, but the counts over them are
		// computed — so /v1/agent/labels and the agent_labels tool must return the identical
		// breakdown, the identical labeled/unlabeled split, and the identical labeled_by list.
		{"agent labels", "/v1/agent/labels?label=intent", "agent_labels", `{"label":"intent"}`, false},
		{"agent labels sparse", "/v1/agent/labels?label=sentiment", "agent_labels", `{"label":"sentiment"}`, false},
		// a label nobody has written must be honestly empty on BOTH surfaces, identically.
		{"agent labels unlabeled", "/v1/agent/labels?label=frustration", "agent_labels", `{"label":"frustration"}`, false},
		{"agent labels windowed", "/v1/agent/labels?label=intent&days=7", "agent_labels", `{"label":"intent","days":7}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := viaAPI(t, srv, c.api)
			m := viaMCP(t, srv, c.tool, c.args)
			if c.subset {
				for k, av := range a {
					if !reflect.DeepEqual(av, m[k]) {
						aj, _ := json.Marshal(av)
						mj, _ := json.Marshal(m[k])
						t.Fatalf("SURFACES DISAGREE on %s field %q —\nAPI: %s\nMCP: %s", c.name, k, aj, mj)
					}
				}
				return
			}
			if !reflect.DeepEqual(a, m) {
				aj, _ := json.MarshalIndent(a, "", " ")
				mj, _ := json.MarshalIndent(m, "", " ")
				t.Fatalf("SURFACES DISAGREE on %s —\nAPI:\n%s\nMCP:\n%s", c.name, aj, mj)
			}
		})
	}
}

func urlEnc(s string) string { return url.QueryEscape(s) }

// TestAgentLabelsAreNonTriviallyAgreed keeps the labels agreement case honest: two surfaces
// returning the same EMPTY result would pass agreement while proving nothing. This pins the
// actual seeded numbers (latest label per conversation wins, unlabeled conversations counted,
// the labelling model named) so the agreement case above is comparing real data.
func TestAgentLabelsAreNonTriviallyAgreed(t *testing.T) {
	srv := agreementServer(t)
	got := viaAPI(t, srv, "/v1/agent/labels?label=intent")
	if got["conversations"] != float64(3) || got["labeled"] != float64(2) || got["unlabeled"] != float64(1) {
		t.Fatalf("expected 3 conversations, 2 labeled, 1 unlabeled: %v", got)
	}
	values, _ := got["values"].([]any)
	if len(values) != 1 {
		t.Fatalf("t0's later label must replace its earlier one, leaving one value: %v", got["values"])
	}
	v0, _ := values[0].(map[string]any)
	if v0["value"] != "billing" || v0["conversations"] != float64(2) {
		t.Fatalf("expected billing(2) after latest-write-wins: %v", v0)
	}
	by, _ := got["labeled_by"].([]any)
	if len(by) != 1 || by[0] != "claude-sonnet-4-5" {
		t.Fatalf("labeled_by must name the model behind the counted labels: %v", got["labeled_by"])
	}
	if got["inferred"] != true {
		t.Fatalf("label values are inferences and must be flagged: %v", got)
	}
	// a label nobody wrote is honestly empty, and says how to fix that
	none := viaAPI(t, srv, "/v1/agent/labels?label=frustration")
	if none["labeled"] != float64(0) || len(none["values"].([]any)) != 0 {
		t.Fatalf("unwritten label must be empty, not fabricated: %v", none)
	}
	if note, _ := none["note"].(string); !strings.Contains(note, "label_conversation") {
		t.Fatalf("empty label result must explain how to label: %q", note)
	}
}

// A missing label must be a guiding 400 on the HTTP surface, never a breakdown of nothing — AND
// the MCP tool must refuse with the SAME WORDS. api.errNoLabel and mcp.errNoLabel are declared
// independently in two packages, so without this the wording can drift and one surface starts
// teaching the labelling loop differently from the other. Agreement covers the error path too.
func TestAgentLabelsRequiresLabel(t *testing.T) {
	srv := agreementServer(t)
	resp, err := http.Get(srv.URL + "/v1/agent/labels")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing label, got %d", resp.StatusCode)
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr.Error == "" {
		t.Fatalf("the 400 must carry the guiding message, not an empty body")
	}
	if !strings.Contains(apiErr.Error, "label_conversation") {
		t.Fatalf("the missing-label error must teach the labelling loop: %q", apiErr.Error)
	}
	if mcpErr := mcpErrorText(t, srv, "agent_labels", `{}`); mcpErr != apiErr.Error {
		t.Fatalf("SURFACES DISAGREE on the missing-label error —\nAPI: %q\nMCP: %q", apiErr.Error, mcpErr)
	}
}

// mcpErrorText calls a tool expecting it to REFUSE, and returns the refusal text. The happy-path
// helper (viaMCP) fatals on an error result, so error-path agreement needs its own reader.
func mcpErrorText(t *testing.T, srv *httptest.Server, tool, args string) string {
	t.Helper()
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, args)
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(req))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("mcp %s: %v (%v)", tool, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var envelope struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Result.IsError {
		t.Fatalf("mcp %s should have refused %s, but returned a result", tool, args)
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatalf("mcp %s errored with no message", tool)
	}
	return envelope.Result.Content[0].Text
}

// featureAgreementServer seeds the feature-report surfaces (measured flag, survey, sessions) AND
// deliberately mixes in env=development events. The default production scope must drop those
// dev-env events IDENTICALLY on /v1 and MCP; before the fix, /v1/flags/{key}/measure and
// /v1/surveys/{id}/results passed RAW events while MCP applied applyDefaultScope, so the two
// surfaces disagreed the moment any dev-env event existed. The contaminants below are what make
// these tests a real guard rather than a trivially-passing empty comparison.
func featureAgreementServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st := memory.New()
	fl, err := flag.Open("")
	if err != nil {
		t.Fatal(err)
	}
	sv, err := survey.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s := New(st)
	s.SetFlags(fl)
	s.SetSurveys(sv)
	if _, err := fl.Save(flag.Flag{Key: "checkout_v2", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}}); err != nil {
		t.Fatal(err)
	}
	// surveyResults reads sv.Type from the store, so the survey must exist on both surfaces.
	// Save with an empty ID creates it and returns the generated id (a caller-supplied id is
	// treated as an update and rejected when it doesn't exist yet).
	saved, err := sv.Save(survey.Survey{Name: "pricing nps", Type: "nps", Question: "how likely to recommend?", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	surveyID := saved.ID
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	now := time.Now().UTC()
	var batch []map[string]any
	ev := func(name, did string, at time.Duration, props map[string]any) {
		batch = append(batch, map[string]any{"name": name, "distinct_id": did,
			"timestamp": now.Add(at).Format(time.RFC3339), "properties": props})
	}
	// Measured flag: 24 users split control/treatment, treatment converts more often.
	for i := 0; i < 24; i++ {
		u := fmt.Sprintf("f%d", i)
		variant := "control"
		if i%2 == 1 {
			variant = "treatment"
		}
		ev(flag.ExposureEvent, u, -48*time.Hour, map[string]any{flag.PropFlag: "checkout_v2", flag.PropVariant: variant})
		if (variant == "control" && i%4 == 0) || (variant == "treatment" && i%4 != 3) {
			ev("purchase", u, -47*time.Hour, map[string]any{"amount": float64(30)})
		}
	}
	// Survey: 20 shown, 18 respond with NPS answers spread 0..10.
	for i := 0; i < 20; i++ {
		u := fmt.Sprintf("s%d", i)
		ev(survey.ShownEvent, u, -36*time.Hour, map[string]any{survey.PropSurvey: surveyID})
		if i%10 != 9 {
			ev(survey.ResponseEvent, u, -35*time.Hour, map[string]any{survey.PropSurvey: surveyID, survey.PropAnswer: float64(i % 11)})
		}
	}
	// AI-visibility samples: two engines (one grounded row via the naming convention),
	// so /v1/ai-visibility and the ai_visibility tool have real aggregation to agree on.
	ev("$geo_check", "geo-runner", -30*time.Hour, map[string]any{
		"engine": "claude", "prompt": "best analytics for indie devs", "mentioned": true,
		"recommended": true, "rank": float64(1), "competitors": "PostHog, Plausible",
		"verbatim": "open-source agent-operated analytics", "model_version": "claude-test-1"})
	ev("$geo_check", "geo-runner", -29*time.Hour, map[string]any{
		"engine": "claude", "prompt": "best analytics for indie devs", "mentioned": true,
		"recommended": false, "rank": float64(3), "competitors": "PostHog",
		"verbatim": "an analytics option", "model_version": "claude-test-1"})
	ev("$geo_check", "geo-runner", -28*time.Hour, map[string]any{
		"engine": "claude-grounded", "prompt": "best analytics for indie devs", "mentioned": false,
		"recommended": false, "rank": float64(0), "competitors": "PostHog, Mixpanel",
		"verbatim": "", "model_version": "claude-test-1"})

	// AI-crawler reports: five crawlers across three operators and all three purposes, one
	// of them served a 403, over two of the three paths the humans below read — so
	// /v1/ai-crawlers and the ai_crawlers tool have real per-crawler, per-operator, error
	// and coverage-gap aggregation to agree on rather than two empty structs.
	crawl := func(name, op, purpose, path string, status float64, at time.Duration) {
		ev(aicrawl.CrawlEvent, "$crawler", at, map[string]any{
			"crawler": name, "operator": op, "purpose": purpose,
			"path": path, "status": status, "site": "example.com"})
	}
	crawl("GPTBot", "OpenAI", aicrawl.PurposeTraining, "/pricing", 200, -20*time.Hour)
	crawl("GPTBot", "OpenAI", aicrawl.PurposeTraining, "/pricing", 200, -19*time.Hour)
	crawl("GPTBot", "OpenAI", aicrawl.PurposeTraining, "/docs", 200, -18*time.Hour)
	crawl("OAI-SearchBot", "OpenAI", aicrawl.PurposeSearch, "/docs", 200, -17*time.Hour)
	crawl("ChatGPT-User", "OpenAI", aicrawl.PurposeUser, "/pricing", 200, -16*time.Hour)
	crawl("ClaudeBot", "Anthropic", aicrawl.PurposeTraining, "/docs", 200, -15*time.Hour)
	crawl("PerplexityBot", "Perplexity", aicrawl.PurposeSearch, "/pricing", 403, -14*time.Hour)

	// A clean 'alice' journey (no dev events) so session_timeline is stable to fetch by start.
	ev("$pageview", "alice", -3*time.Hour, map[string]any{"path": "/"})
	ev("$click", "alice", -3*time.Hour+2*time.Second, map[string]any{"path": "/", "x": float64(100), "y": float64(200), "vw": float64(1280), "text": "Start"})
	ev("$pageview", "alice", -3*time.Hour+5*time.Second, map[string]any{"path": "/pricing"})
	ev("$pageview", "bob", -2*time.Hour, map[string]any{"path": "/"})
	ev("$pageview", "bob", -2*time.Hour+time.Minute, map[string]any{"path": "/docs"})

	// DEV-ENV CONTAMINANTS — must be excluded identically on both surfaces (the whole point).
	ev(flag.ExposureEvent, "devuser", -48*time.Hour, map[string]any{flag.PropFlag: "checkout_v2", flag.PropVariant: "treatment", "env": "development"})
	ev("purchase", "devuser", -47*time.Hour, map[string]any{"amount": float64(30), "env": "development"})
	ev(survey.ShownEvent, "devuser", -36*time.Hour, map[string]any{survey.PropSurvey: surveyID, "env": "development"})
	ev(survey.ResponseEvent, "devuser", -35*time.Hour, map[string]any{survey.PropSurvey: surveyID, survey.PropAnswer: float64(10), "env": "development"})
	ev("$pageview", "devuser", -3*time.Hour, map[string]any{"path": "/", "env": "development"})
	// a crawl report from a preview deploy: dropped by the production scope on BOTH surfaces,
	// and the pageview above is what makes the coverage gap's visitor count prove it happened
	ev(aicrawl.CrawlEvent, "$crawler", -13*time.Hour, map[string]any{
		"crawler": "GPTBot", "operator": "OpenAI", "purpose": aicrawl.PurposeTraining,
		"path": "/", "status": float64(200), "site": "example.com", "env": "development"})

	body, _ := json.Marshal(batch)
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed ingest failed: %v %v", err, resp)
	}
	return srv, surveyID
}

func assertAgree(t *testing.T, name string, a, m map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(a, m) {
		aj, _ := json.MarshalIndent(a, "", " ")
		mj, _ := json.MarshalIndent(m, "", " ")
		t.Fatalf("SURFACES DISAGREE on %s —\nAPI:\n%s\nMCP:\n%s", name, aj, mj)
	}
}

// TestFlagImpactAgreement pins GET /v1/flags/{key}/measure == MCP flag_impact. Guards the dev-env
// scope divergence: measureFlag must apply the same production scope MCP does.
func TestFlagImpactAgreement(t *testing.T) {
	srv, _ := featureAgreementServer(t)
	a := viaAPI(t, srv, "/v1/flags/checkout_v2/measure?event=purchase")
	m := viaMCP(t, srv, "flag_impact", `{"key":"checkout_v2","event":"purchase"}`)
	assertAgree(t, "flag_impact", a, m)
}

// TestSurveyResultsAgreement pins GET /v1/surveys/{id}/results == MCP survey_results. Same dev-env
// scope guard as flag_impact.
func TestSurveyResultsAgreement(t *testing.T) {
	srv, sid := featureAgreementServer(t)
	a := viaAPI(t, srv, "/v1/surveys/"+sid+"/results")
	m := viaMCP(t, srv, "survey_results", fmt.Sprintf(`{"id":%q}`, sid))
	assertAgree(t, "survey_results", a, m)
}

// TestAICrawlAgreement pins GET /v1/ai-crawlers == MCP ai_crawlers. The crawler report is the
// one number a founder will quote at people ("Anthropic has read 40 of my pages"), so the
// endpoint and the tool their agent calls have to be the same computation — including the
// default window and the 365-day cap, which are the two places a second surface silently
// drifts. The assertions after the comparison exist because two EMPTY results also agree:
// without them this test would keep passing after the aggregation stopped working.
func TestAICrawlAgreement(t *testing.T) {
	srv, _ := featureAgreementServer(t)
	assertAgree(t, "ai_crawlers", viaAPI(t, srv, "/v1/ai-crawlers?days=30"), viaMCP(t, srv, "ai_crawlers", `{"days":30}`))
	assertAgree(t, "ai_crawlers default window", viaAPI(t, srv, "/v1/ai-crawlers"), viaMCP(t, srv, "ai_crawlers", `{}`))
	assertAgree(t, "ai_crawlers capped at 365 both sides", viaAPI(t, srv, "/v1/ai-crawlers?days=999"), viaMCP(t, srv, "ai_crawlers", `{"days":999}`))

	got := viaAPI(t, srv, "/v1/ai-crawlers?days=30")
	if got["reporting"] != true || got["hits"] != float64(7) || got["errors"] != float64(1) {
		t.Fatalf("expected 7 seeded fetches with 1 error, reporting on: %v", got)
	}
	if crawlers, _ := got["crawlers"].([]any); len(crawlers) != 5 {
		t.Fatalf("expected the 5 seeded crawlers: %v", got["crawlers"])
	}
	// the purposes are counted apart, which is the module's whole argument
	if got["training"] != float64(4) || got["search"] != float64(2) || got["user"] != float64(1) {
		t.Fatalf("purposes were not kept apart: %v", got)
	}
	// The coverage gap doubles as the dev-env scope guard: a preview-deploy crawl of "/" is
	// seeded, so if either surface stopped applying the production scope, "/" would read as
	// crawled and this row would vanish from one of them.
	gaps, _ := got["gaps"].([]any)
	if len(gaps) != 1 {
		t.Fatalf("expected exactly the one uncrawled page humans read: %v", got["gaps"])
	}
	g, _ := gaps[0].(map[string]any)
	if g["path"] != "/" || g["views"] != float64(2) || g["visitors"] != float64(2) {
		t.Fatalf("the coverage gap must be / read by 2 visitors, dev traffic excluded: %v", g)
	}
}

// TestSessionAgreement pins the session inspector: GET /v1/sessions == MCP list_sessions, and
// GET /v1/session == MCP session_timeline for the same (distinct_id, start).
func TestSessionAgreement(t *testing.T) {
	srv, _ := featureAgreementServer(t)
	al := viaAPI(t, srv, "/v1/sessions?days=7&limit=500")
	ml := viaMCP(t, srv, "list_sessions", `{"days":7,"limit":500}`)
	assertAgree(t, "list_sessions", al, ml)

	// Fetch alice's session start from the list, then compare the play-by-play.
	var start int64
	sessions, _ := al["sessions"].([]any)
	for _, row := range sessions {
		r, _ := row.(map[string]any)
		if r["distinct_id"] == "alice" {
			start = int64(r["start_unix"].(float64))
		}
	}
	if start == 0 {
		t.Fatalf("no session for alice in list: %v", al)
	}
	a := viaAPI(t, srv, fmt.Sprintf("/v1/session?distinct_id=alice&start=%d", start))
	m := viaMCP(t, srv, "session_timeline", fmt.Sprintf(`{"distinct_id":"alice","start":%d}`, start))
	assertAgree(t, "session_timeline", a, m)
}
