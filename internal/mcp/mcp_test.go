package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ev := func(u, n string, off time.Duration) event.Event {
		return event.Event{ID: u + n + off.String(), DistinctID: u, Name: n, Timestamp: base.Add(off),
			Properties: map[string]any{"source": "google"}}
	}
	_ = st.Ingest(
		ev("a", "signup", 0), ev("a", "activate", time.Hour), ev("a", "checkout", 2*time.Hour),
		ev("b", "signup", 0), ev("b", "activate", time.Hour),
		ev("c", "signup", 0),
	)
	return New(st)
}

func call(t *testing.T, s *Server, raw string) *response {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("bad request json: %v", err)
	}
	return s.Dispatch(req)
}

func TestInitializeAndToolsList(t *testing.T) {
	s := newServer(t)
	if r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); r == nil || r.Error != nil {
		t.Fatalf("initialize failed: %+v", r)
	}
	r := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	b, _ := json.Marshal(r.Result)
	for _, name := range []string{"overview", "funnel", "retention", "trends", "breakdown", "list_events"} {
		if !strings.Contains(string(b), `"`+name+`"`) {
			t.Fatalf("tools/list missing %s: %s", name, b)
		}
	}
}

func TestNotificationReturnsNil(t *testing.T) {
	s := newServer(t)
	if r := call(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r != nil {
		t.Fatalf("notification should produce no response, got %+v", r)
	}
}

func TestFunnelToolCall(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"funnel","arguments":{"steps":["signup","activate","checkout"]}}}`)
	b, _ := json.Marshal(r.Result)
	// 3 signup, 2 activate, 1 checkout from the seeded data
	if !strings.Contains(string(b), `\"count\":3`) || !strings.Contains(string(b), `\"count\":1`) {
		t.Fatalf("funnel result missing expected counts: %s", b)
	}
	if strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("funnel call errored: %s", b)
	}
}

func TestUnknownToolIsError(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	b, _ := json.Marshal(r.Result)
	if !strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("unknown tool should be isError: %s", b)
	}
}

// TestTrendsDaysClampNoDoS is the regression guard for an audit finding: MCP trends had no
// upper bound on `days`, so days=10000000 built ~10M daily buckets in ComputeInterval —
// ~1GB allocated in a single call (an OOM/DoS on the shared engine) — and overflowed the
// year past 9999, leaking a raw marshal error. It must clamp to the same 365 the GET path
// uses: bounded, fast, no error.
func TestTrendsDaysClampNoDoS(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","days":10000000}}}`)
	b, _ := json.Marshal(r.Result)
	if strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("clamped trends must not error (year overflow leak?): %s", b)
	}
	if strings.Contains(string(b), "outside of range") || strings.Contains(string(b), "year") {
		t.Fatalf("raw marshal/year error leaked to caller: %s", b)
	}
	// the clamped window is 365 days => at most ~366 daily points, never 10M.
	var env struct {
		Content []struct{ Text string }
	}
	_ = json.Unmarshal(b, &env)
	if len(env.Content) > 0 {
		var res struct {
			Points []struct{} `json:"points"`
		}
		_ = json.Unmarshal([]byte(env.Content[0].Text), &res)
		if len(res.Points) > 366 {
			t.Fatalf("days=10000000 produced %d buckets — not clamped to 365", len(res.Points))
		}
	}
}

// TestOverviewIsRichNotBare pins the sleek orient tool: overview must lead with a synthesized
// one-line read, week-over-week momentum, the headline event, and the guided next call — not a
// bare row of counts — so the agent presents meaning immediately.
func TestOverviewIsRichNotBare(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"overview","arguments":{}}}`)
	b, _ := json.Marshal(r.Result)
	var env struct {
		Content []struct{ Text string }
	}
	_ = json.Unmarshal(b, &env)
	if len(env.Content) == 0 {
		t.Fatal("overview returned no content")
	}
	var ov map[string]any
	if err := json.Unmarshal([]byte(env.Content[0].Text), &ov); err != nil {
		t.Fatalf("overview text not JSON: %v", err)
	}
	for _, f := range []string{"read", "next", "active_users_7d", "active_users_wow", "total_users", "events_7d", "tracked_event_names"} {
		if _, ok := ov[f]; !ok {
			t.Errorf("overview missing sleek field %q: %v", f, ov)
		}
	}
	if read, _ := ov["read"].(string); !strings.Contains(read, "active in the last 7d") {
		t.Errorf("overview read line should synthesize the state, got %q", read)
	}
	// seeded data has a "signup" event — it must be picked as the headline over $pageview.
	if h, _ := ov["headline_event"].(string); h != "signup" {
		t.Errorf("headline_event should be signup (the conversion event), got %q", h)
	}
}

// TestBreakdownHonorsWindow is the regression guard for an audit finding: the breakdown tool
// silently IGNORED days/from/to and returned the all-time split as if windowed — disagreeing
// with GET /v1/trends?breakdown and the MCP trends tool for the same question.
func TestBreakdownHonorsWindow(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -30)
	var seed []event.Event
	for i := 0; i < 5; i++ {
		seed = append(seed,
			event.Event{ID: fmt.Sprintf("t%d", i), DistinctID: fmt.Sprintf("t%d", i), Name: "checkout",
				Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"plan": "pro"}},
			event.Event{ID: fmt.Sprintf("o%d", i), DistinctID: fmt.Sprintf("o%d", i), Name: "checkout",
				Timestamp: old, Properties: map[string]any{"plan": "free"}})
	}
	_ = st.Ingest(seed...)
	s := New(st)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"breakdown","arguments":{"event":"checkout","property":"plan","hours":6}}}`)
	b, _ := json.Marshal(r.Result)
	var env struct {
		Content []struct{ Text string }
	}
	_ = json.Unmarshal(b, &env)
	txt := env.Content[0].Text
	if strings.Contains(txt, "free") {
		t.Errorf("days=1 breakdown must exclude the 30-day-old free events (window ignored): %s", txt)
	}
	if !strings.Contains(txt, "pro") || !strings.Contains(txt, `"count":5`) {
		t.Errorf("days=1 breakdown should show today's 5 pro checkouts: %s", txt)
	}
}

// TestFiltersUnknownPropertyErrors is the regression guard for an audit finding: a filter on
// a property NO event carries returned a silent 0 as if it were a real windowed answer, while
// GET /v1 errored — surfaces disagreed on the same filter. All report tools must return the
// same guidance error.
func TestFiltersUnknownPropertyErrors(t *testing.T) {
	s := newServer(t)
	for _, tool := range []string{"trends", "breakdown", "web_overview", "lifecycle", "paths"} {
		args := map[string]any{"filters": []map[string]any{{"property": "plann", "op": "eq", "value": "pro"}}}
		switch tool {
		case "trends":
			args["event"] = "signup"
		case "breakdown":
			args["event"], args["property"] = "signup", "source"
		case "paths":
			args["start"] = "signup"
		}
		aj, _ := json.Marshal(args)
		r := call(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, aj))
		b, _ := json.Marshal(r.Result)
		if !strings.Contains(string(b), `"isError":true`) || !strings.Contains(string(b), "plann") {
			t.Errorf("%s: filter on unknown property must be a guidance error naming it, got: %s", tool, b)
		}
	}
}

// TestTrendsAbsoluteWindowCapsAtNow is the regression guard for the window-uniformity
// finding: from/to windows must get the SAME now-cap as days/hours, so a clock-skewed
// future event can't make from=<today> answer differently than days=1.
func TestTrendsAbsoluteWindowCapsAtNow(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	_ = st.Ingest(
		event.Event{ID: "a", DistinctID: "a", Name: "signup", Timestamp: now.Add(-2 * time.Hour)},
		event.Event{ID: "b", DistinctID: "b", Name: "signup", Timestamp: now.Add(-1 * time.Hour)},
		event.Event{ID: "f", DistinctID: "f", Name: "signup", Timestamp: now.Add(30 * time.Minute)}, // future (within ingest tolerance)
	)
	s := New(st)
	get := func(args string) int {
		r := call(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":%s}}`, args))
		b, _ := json.Marshal(r.Result)
		var env struct {
			Content []struct{ Text string }
		}
		_ = json.Unmarshal(b, &env)
		var res struct{ Total int }
		_ = json.Unmarshal([]byte(env.Content[0].Text), &res)
		return res.Total
	}
	// RFC3339 windows (midnight-independent): from = 3h ago covers both past events; the
	// upper bound must be capped at now so the +30m future event is excluded everywhere.
	from := now.Add(-3 * time.Hour).Format(time.RFC3339)
	farTo := now.Add(24 * time.Hour).Format(time.RFC3339)
	rolling := get(`{"event":"signup","hours":3}`)
	absolute := get(fmt.Sprintf(`{"event":"signup","from":%q,"to":%q}`, from, farTo))
	fromOnly := get(fmt.Sprintf(`{"event":"signup","from":%q}`, from))
	if rolling != 2 || absolute != 2 || fromOnly != 2 {
		t.Errorf("window uniformity broken: hours=3=%d, from/to=%d, from-only=%d — all must be 2 (future event capped at now everywhere)", rolling, absolute, fromOnly)
	}
}

// TestPathsAndFunnelHonorWindow_R5 guards two round-5 findings: GET/MCP paths and the MCP
// funnel tool silently ignored days/from/to, returning all-time data as a windowed answer.
func TestPathsHonorsWindow_R5(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	// u1: signup+activate 10 days ago (out of a 1-day window); u2: signup+activate 2h ago.
	_ = st.Ingest(
		event.Event{ID: "a", DistinctID: "u1", Name: "signup", Timestamp: now.AddDate(0, 0, -10)},
		event.Event{ID: "b", DistinctID: "u1", Name: "activate", Timestamp: now.AddDate(0, 0, -10).Add(time.Hour)},
		event.Event{ID: "c", DistinctID: "u2", Name: "signup", Timestamp: now.Add(-2 * time.Hour)},
		event.Event{ID: "d", DistinctID: "u2", Name: "activate", Timestamp: now.Add(-1 * time.Hour)},
	)
	s := New(st)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"paths","arguments":{"start":"signup","hours":6}}}`)
	b, _ := json.Marshal(r.Result)
	var env struct{ Content []struct{ Text string } }
	_ = json.Unmarshal(b, &env)
	var pr struct{ Users int }
	_ = json.Unmarshal([]byte(env.Content[0].Text), &pr)
	if pr.Users != 1 {
		t.Errorf("paths days=1 should trace only the in-window signup (1 user), got %d — window ignored", pr.Users)
	}
}

func TestFunnelToolHonorsWindow_R5(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	// 5 users convert today, 5 users converted 30 days ago
	var seed []event.Event
	for i := 0; i < 5; i++ {
		seed = append(seed,
			event.Event{ID: string(rune('a' + i)), DistinctID: "t" + string(rune('a'+i)), Name: "signup", Timestamp: now.Add(-3 * time.Hour)},
			event.Event{ID: string(rune('A' + i)), DistinctID: "t" + string(rune('a'+i)), Name: "checkout", Timestamp: now.Add(-2 * time.Hour)},
			event.Event{ID: string(rune('k' + i)), DistinctID: "o" + string(rune('a'+i)), Name: "signup", Timestamp: now.AddDate(0, 0, -30)},
			event.Event{ID: string(rune('K' + i)), DistinctID: "o" + string(rune('a'+i)), Name: "checkout", Timestamp: now.AddDate(0, 0, -30).Add(time.Hour)},
		)
	}
	_ = st.Ingest(seed...)
	s := New(st)
	// from = 6h ago (RFC3339, midnight-independent): only today's 5 convert, not the all-time 10.
	from := now.Add(-6 * time.Hour).Format(time.RFC3339)
	r := call(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"funnel","arguments":{"steps":["signup","checkout"],"from":%q}}}`, from))
	b, _ := json.Marshal(r.Result)
	var env struct{ Content []struct{ Text string } }
	_ = json.Unmarshal(b, &env)
	var fr struct {
		Steps []struct{ Count int } `json:"steps"`
	}
	_ = json.Unmarshal([]byte(env.Content[0].Text), &fr)
	if len(fr.Steps) == 0 || fr.Steps[0].Count != 5 {
		t.Errorf("windowed funnel should count only the recent 5 signups (not the all-time 10): %s", env.Content[0].Text)
	}
}

// TestWindowRejectsNaN_R5 guards the hours=NaN / non-finite window findings.
func TestWindowRejectsNaN_R5(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","from":"not-a-date"}}}`)
	b, _ := json.Marshal(r.Result)
	if !strings.Contains(string(b), `"isError":true`) {
		t.Errorf("bad from date should error: %s", b)
	}
}
