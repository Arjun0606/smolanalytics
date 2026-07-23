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
		{"heatmap", "/v1/heatmap?path=/pricing", "heatmap", `{"path":"/pricing"}`, false},
		{"heatmap desktop bucket + grid", "/v1/heatmap?path=/pricing&viewport=desktop&cols=20&row_px=40", "heatmap", `{"path":"/pricing","viewport":"desktop","cols":20,"row_px":40}`, false},
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
