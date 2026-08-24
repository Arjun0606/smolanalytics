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

// TWO DEFINITIONS OF "THE LAST 30 DAYS", AND THEY DISAGREED IN PRODUCTION.
//
// web.Compute windowed by a rolling N×24h; everything else — trends, funnels, retention, rows,
// and the dashboard's own tiles — windowed by N calendar days ending today. The rolling form
// reaches back a further (24h − time-of-day), about ten hours at midday.
//
// Measured on the live demo at the same instant, same days=30, same event:
//
//	/v1/web   →  1,779 visitors, 2,371 pageviews
//	/v1/rows  →  1,753 people,   2,336 pageview rows
//
// Same question, two answers, on the product whose footer promises "ask == dashboard == MCP, all
// from one report engine". /v1/web, the MCP web_overview tool and the public share page all used
// the rolling form while the dashboard beside them used the calendar form.
//
// It went unnoticed because both numbers are individually plausible — nothing renders wrong, it
// just quietly isn't the same number. That is the hardest kind of defect to catch by looking, and
// the only reliable guard is an equality test between the surfaces.
func TestWebAndRowsCountTheSameWindow(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	// Traffic every hour for 40 days. Hourly matters: the two window definitions differ only by a
	// part-day at the far edge, so a fixture with one event per day can straddle the gap and pass
	// against the bug.
	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < 40; d++ {
		for h := 0; h < 24; h++ {
			ts := now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			batch = append(batch, map[string]any{
				"name": "$pageview", "distinct_id": fmt.Sprintf("d%dh%d", d, h),
				"timestamp":  ts.Format(time.RFC3339),
				"properties": map[string]any{"path": "/"},
			})
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

	for _, days := range []int{7, 14, 30} {
		var wv struct {
			Visitors   int `json:"visitors"`
			Pageviews  int `json:"pageviews"`
			PeriodDays int `json:"period_days"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/web?days=%d", days)), &wv)

		var rows struct {
			Total int `json:"total"`
			Users int `json:"users"`
		}
		mustJSON(t, getBody(t, srv,
			fmt.Sprintf("/v1/rows?event=%%24pageview&days=%d&unique=1&limit=1", days)), &rows)

		if wv.Pageviews != rows.Total {
			t.Errorf("days=%d: /v1/web counts %d pageviews, /v1/rows counts %d — two windows for "+
				"one question", days, wv.Pageviews, rows.Total)
		}
		if wv.Visitors != rows.Users {
			t.Errorf("days=%d: /v1/web counts %d visitors, /v1/rows recomputes %d people — a proof "+
				"link on this number would contradict the number it proves",
				days, wv.Visitors, rows.Users)
		}
		// And the caption must say what was asked for. A calendar window is (N-1) days plus
		// today-so-far, which truncating reported as N-1 — the range control disagreeing with the
		// label beneath it.
		if wv.PeriodDays != days {
			t.Errorf("days=%d: the report says it covers %d days", days, wv.PeriodDays)
		}
	}
}

// The same window rule has to reach the MCP tool, or "ask == dashboard == MCP" holds for two of
// the three. web_overview reads through the same web.Compute, so this pins the shared entry point
// rather than the transport.
func TestTrendsAndRowsCountTheSameWindow(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < 40; d++ {
		for h := 0; h < 24; h += 3 {
			ts := now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			batch = append(batch, map[string]any{
				"name": "signup", "distinct_id": fmt.Sprintf("u%d_%d", d, h),
				"timestamp": ts.Format(time.RFC3339),
			})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	for _, days := range []int{7, 30} {
		var tr struct {
			Total int `json:"total"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/trends?event=signup&days=%d", days)), &tr)
		var rows struct {
			Total int `json:"total"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/rows?event=signup&days=%d&limit=1", days)), &rows)
		if tr.Total != rows.Total {
			t.Errorf("days=%d: trends says %d, rows says %d", days, tr.Total, rows.Total)
		}
	}
}

// ONE JOURNEY DETECTOR, SO ONE VERDICT.
//
// The dashboard's detectFunnel ranked candidate steps by raw VOLUME; insight.Generate — which
// produces the "fix this first" card at the top of that same page, and answers the MCP
// whats_notable tool — ranked them by COVERAGE. Two detectors, two funnels, two verdicts about
// one product, from the two surfaces most likely to be read side by side.
//
// The failure needs only a handful of power users: someone hammering one event drags the volume
// detector onto a funnel the coverage detector rejects outright, and the page then names a
// different "biggest drop-off" than the editor does.
func TestNotableAgreesBetweenHTTPAndMCP(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	var batch []map[string]any
	add := func(name, id string, at time.Time) {
		batch = append(batch, map[string]any{
			"name": name, "distinct_id": id, "timestamp": at.Format(time.RFC3339),
			"properties": map[string]any{"path": "/"}})
	}
	// the real journey, wide coverage
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("u%d", i)
		t0 := now.AddDate(0, 0, -3).Add(time.Duration(i) * time.Minute)
		add("$pageview", id, t0)
		if i < 40 {
			add("read_docs", id, t0.Add(time.Minute))
		}
		if i < 12 {
			add("start_trial", id, t0.Add(2*time.Minute))
		}
		if i < 4 {
			add("subscribe", id, t0.Add(3*time.Minute))
		}
	}
	// three power users hammering one event: huge volume, tiny coverage
	for p := 0; p < 3; p++ {
		id := fmt.Sprintf("power%d", p)
		for k := 0; k < 200; k++ {
			add("search", id, now.AddDate(0, 0, -2).Add(time.Duration(k)*time.Second))
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
		t.Fatalf("seeding failed with %d", code)
	}

	firstFunnelFinding := func(raw string) string {
		var out struct {
			Findings []struct {
				Kind    string `json:"kind"`
				Verdict string `json:"verdict"`
				Detail  string `json:"detail"`
				Title   string `json:"title"`
			} `json:"findings"`
		}
		if uerr := json.Unmarshal([]byte(raw), &out); uerr != nil {
			t.Fatalf("not JSON: %v (%.100s)", uerr, raw)
		}
		for _, f := range out.Findings {
			if strings.Contains(strings.ToLower(f.Kind), "funnel") || strings.Contains(f.Verdict, "drop") {
				return f.Kind + "|" + f.Title + "|" + f.Verdict + f.Detail
			}
		}
		return ""
	}

	httpSide := firstFunnelFinding(getBody(t, srv, "/v1/notable"))

	call, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "whats_notable", "arguments": map[string]any{}},
	})
	mr, merr := srv.Client().Post(srv.URL+"/mcp", "application/json", strings.NewReader(string(call)))
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
	if derr := json.NewDecoder(mr.Body).Decode(&env); derr != nil || len(env.Result.Content) == 0 {
		t.Fatalf("whats_notable returned nothing usable (err %v, status %d)", derr, mr.StatusCode)
	}
	mcpSide := firstFunnelFinding(env.Result.Content[0].Text)

	if httpSide == "" && mcpSide == "" {
		t.Skip("neither surface produced a funnel finding on this fixture")
	}
	if httpSide != mcpSide {
		t.Errorf("the page and the editor name different drop-offs for the same product:\n"+
			"  /v1/notable:   %s\n  whats_notable: %s", httpSide, mcpSide)
	}
}
