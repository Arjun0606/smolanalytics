package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// windowServer seeds two cohorts of converters: one inside any short rolling window, one only
// reachable all-time. Any tool that drops the window silently reports the all-time number.
func windowServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	add := func(u string, at time.Time) {
		evs = append(evs,
			event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: at},
			event.Event{ID: u + "c", DistinctID: u, Name: "checkout", Timestamp: at.Add(10 * time.Minute)},
		)
	}
	for _, u := range []string{"recent1", "recent2"} {
		add(u, now.Add(-3*time.Hour))
	}
	for _, u := range []string{"old1", "old2", "old3"} {
		add(u, now.Add(-30*24*time.Hour))
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}
	return New(st)
}

func funnelSteps(t *testing.T, s *Server, args map[string]any) []int {
	t.Helper()
	out, err := s.callTool("funnel", mustJSON(args))
	if err != nil {
		t.Fatalf("funnel %v: %v", args, err)
	}
	var res struct {
		Steps []struct {
			Count int `json:"count"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("funnel returned invalid JSON: %v\n%s", err, out)
	}
	counts := make([]int, len(res.Steps))
	for i, st := range res.Steps {
		counts[i] = st.Count
	}
	return counts
}

// `hours` was not a field on funnel's argument struct, so encoding/json dropped it without a
// word: funnel(steps=[...], hours=6) measured the entire history while the model presented the
// result as "conversion in the last 6 hours". Measured on a seeded store, hours=6 and no window
// at all returned the identical count — the argument was provably a no-op.
func TestFunnelHonorsHours(t *testing.T) {
	s := windowServer(t)

	allTime := funnelSteps(t, s, map[string]any{"steps": []string{"signup", "checkout"}})
	if allTime[0] != 5 {
		t.Fatalf("fixture: all-time signup count = %d, want 5", allTime[0])
	}
	got := funnelSteps(t, s, map[string]any{"steps": []string{"signup", "checkout"}, "hours": 6})
	if got[0] != 2 || got[1] != 2 {
		t.Errorf("funnel(hours=6) = %v, want [2 2] — the 30-day-old cohort is in a six-hour answer", got)
	}
}

// A schema-validating MCP client strips arguments the inputSchema does not declare, so a window
// the handler honours but the schema hides never arrives. funnel was the only windowed report
// tool advertising no window at all, which is how a two-year-old instance answered "conversion
// this week" with two years of data even after the handler learned to read days/from/to.
func TestFunnelSchemaDeclaresItsReportingWindow(t *testing.T) {
	var props map[string]any
	for _, tool := range toolList {
		if tool["name"] != "funnel" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ = schema["properties"].(map[string]any)
	}
	if props == nil {
		t.Fatal("funnel is not in toolList, or its inputSchema has no properties")
	}
	for _, key := range []string{"days", "hours", "from", "to"} {
		if _, ok := props[key]; !ok {
			t.Errorf("funnel's inputSchema does not declare %q — a validating client will strip it and the tool answers all-time", key)
		}
	}
}

// days=0.5 is a natural reading of "rolling window in days" for "the last 12 hours", and
// int(0.5)==0 made the offset +1: from became TOMORROW 00:00 UTC while to stayed now. That is
// not a small window, it is an inverted one. Measured: breakdown(days=0.5) returned
// {"groups":[]} and funnel(days=0.5) returned every step at count 0 — a confident empty report
// rather than an error — while the identical hours=12 call returned the real numbers, and
// trends rejected the very same input. Three tools, three answers to one question.
func TestFractionalDaysIsRejectedEverywhereTrendsRejectsIt(t *testing.T) {
	s := windowServer(t)

	if _, err := s.callTool("trends", mustJSON(map[string]any{"event": "signup", "days": 0.5})); err == nil {
		t.Fatal("trends used to reject days=0.5; if it stopped, the reference point for this contract is gone")
	}

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"funnel", map[string]any{"steps": []string{"signup", "checkout"}, "days": 0.5}},
		{"breakdown", map[string]any{"event": "signup", "property": "source", "days": 0.5}},
		{"paths", map[string]any{"start": "signup", "days": 0.5}},
	} {
		out, err := s.callTool(c.tool, mustJSON(c.args))
		if err == nil {
			t.Errorf("%s(days=0.5) returned a report instead of an error — an inverted window answers a confident zero: %s", c.tool, out)
			continue
		}
		if !strings.Contains(err.Error(), "hours") {
			t.Errorf("%s(days=0.5) error must point at the fix (use hours), got: %v", c.tool, err)
		}
	}

	// and the same window still works when it is asked for the way that has always worked
	if got := funnelSteps(t, s, map[string]any{"steps": []string{"signup", "checkout"}, "hours": 12}); got[0] != 2 {
		t.Errorf("funnel(hours=12) = %v, want first step 2 — rejecting days=0.5 must not break the correct spelling", got)
	}
}

// A transposed from/to pair resolves to a window that can match nothing, and scopeWindow's
// half-open [from, to) turns that into a real-looking zero rather than a complaint.
func TestTransposedAbsoluteWindowIsAnError(t *testing.T) {
	s := windowServer(t)
	_, err := s.callTool("funnel", mustJSON(map[string]any{
		"steps": []string{"signup", "checkout"}, "from": "2026-06-01", "to": "2026-01-01",
	}))
	if err == nil {
		t.Fatal("from after to returned a report — every step reads 0 and the model presents it as the answer")
	}
	if !strings.Contains(err.Error(), "before") {
		t.Errorf("the error must name what is wrong with the bounds, got: %v", err)
	}
}
