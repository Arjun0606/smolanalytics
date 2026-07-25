// Package agent computes agent-observability reports over the same generic event log
// everything else in the engine runs on — no new storage, no schema change. Two reserved
// event names carry the signal: agent_tool_call (one tool/MCP dispatch, with latency + error)
// and agent_turn (one conversation turn, with role + time-to-first-token). Every number here
// is COMPUTED and deterministic — latency percentiles reuse the exact nearest-rank code the
// p90/p95/p99 measures use (trends.Percentile), so the provably-correct brand holds. The one
// number we refuse to compute unless the app sends it is resolution rate (the honesty covenant):
// ResolutionRate stays nil unless a turn actually carries a `resolved` bool — never fabricated.
package agent

import (
	"sort"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// Reserved event names, deliberately NOT $-prefixed so they sidestep the autocapture bot filter
// at ingest (that filter only drops $-prefixed events from crawler UAs). Documented for the
// paste-to-install skill so an agent instruments its own observability with these exact names.
const (
	EventToolCall = "agent_tool_call"
	EventTurn     = "agent_turn"
)

// ClientSplit is one client's share of a tool's calls (cursor / claude-code / copilot / …).
type ClientSplit struct {
	Client string `json:"client"`
	Calls  int    `json:"calls"`
}

// ToolStat is one tool's health: volume, error rate, latency tail, and who's calling it.
type ToolStat struct {
	Tool       string        `json:"tool"`
	Calls      int           `json:"calls"`
	Errors     int           `json:"errors"`
	ErrorRate  float64       `json:"error_rate"`  // Errors/Calls, 0 when no calls
	LatencyP50 float64       `json:"latency_p50"` // over latency_ms, nearest-rank
	LatencyP90 float64       `json:"latency_p90"`
	LatencyP99 float64       `json:"latency_p99"`
	Clients    []ClientSplit `json:"clients"`
}

// ToolHealth is the tool-call report: one row per tool, plus the overall totals.
type ToolHealth struct {
	Event  string     `json:"event"`
	Tools  []ToolStat `json:"tools"`
	Calls  int        `json:"calls"`
	Errors int        `json:"errors"`
}

// ComputeToolHealth groups agent_tool_call events (already filtered + dev-env-scoped by the
// caller) by their `tool` property within [from, to), and for each tool computes call/error
// counts, error rate, latency p50/p90/p99 over `latency_ms` (via trends.Percentile — the same
// nearest-rank the measures use), and the client split via query.Breakdown over `client`. A tool
// that doesn't exist simply doesn't appear — an honest empty report, never an error.
func ComputeToolHealth(events []event.Event, from, to time.Time) ToolHealth {
	out := ToolHealth{Event: EventToolCall, Tools: []ToolStat{}}
	byTool := map[string][]event.Event{}
	for _, e := range events {
		if e.Name != EventToolCall || !inWindow(e, from, to) {
			continue
		}
		tool := "(none)"
		if v, ok := e.Properties["tool"]; ok {
			tool = valueOf(v)
		}
		byTool[tool] = append(byTool[tool], e)
	}
	for tool, evs := range byTool {
		st := ToolStat{Tool: tool, Calls: len(evs), Clients: []ClientSplit{}}
		lat := make([]float64, 0, len(evs))
		for _, e := range evs {
			if isErr(e.Properties["error"]) {
				st.Errors++
			}
			if f, ok := numOf(e.Properties["latency_ms"]); ok {
				lat = append(lat, f)
			}
		}
		if st.Calls > 0 {
			st.ErrorRate = float64(st.Errors) / float64(st.Calls)
		}
		st.LatencyP50 = trends.Percentile(lat, 0.50)
		st.LatencyP90 = trends.Percentile(lat, 0.90)
		st.LatencyP99 = trends.Percentile(lat, 0.99)
		for _, g := range query.Breakdown(evs, "client") {
			st.Clients = append(st.Clients, ClientSplit{Client: g.Value, Calls: g.Count})
		}
		out.Tools = append(out.Tools, st)
		out.Calls += st.Calls
		out.Errors += st.Errors
	}
	// deterministic order: busiest tool first, name as tie-break (same rule as query.Breakdown).
	sort.Slice(out.Tools, func(i, j int) bool {
		if out.Tools[i].Calls != out.Tools[j].Calls {
			return out.Tools[i].Calls > out.Tools[j].Calls
		}
		return out.Tools[i].Tool < out.Tools[j].Tool
	})
	return out
}

// ErrorType is one error class and how often it occurred.
type ErrorType struct {
	ErrorType string `json:"error_type"`
	Count     int    `json:"count"`
}

// ErrorTaxonomy is the breakdown of failing tool calls by their `error_type`, optionally scoped
// to a single tool. Total is the number of error calls considered (the taxonomy sums to it).
type ErrorTaxonomy struct {
	Event string      `json:"event"`
	Tool  string      `json:"tool,omitempty"`
	Total int         `json:"total"`
	Types []ErrorType `json:"types"`
}

// ComputeErrorTaxonomy runs query.Breakdown over `error_type` for the failing agent_tool_call
// events (error truthy) in [from, to), optionally restricted to one tool. A typo'd or unseen
// tool matches nothing and returns an empty taxonomy — honest empty, never an error.
func ComputeErrorTaxonomy(events []event.Event, tool string, from, to time.Time) ErrorTaxonomy {
	out := ErrorTaxonomy{Event: EventToolCall, Tool: tool, Types: []ErrorType{}}
	var errs []event.Event
	for _, e := range events {
		if e.Name != EventToolCall || !inWindow(e, from, to) {
			continue
		}
		if tool != "" && valueOf(e.Properties["tool"]) != tool {
			continue
		}
		if isErr(e.Properties["error"]) {
			errs = append(errs, e)
		}
	}
	out.Total = len(errs)
	for _, g := range query.Breakdown(errs, "error_type") {
		out.Types = append(out.Types, ErrorType{ErrorType: g.Value, Count: g.Count})
	}
	return out
}

// Conversations is the conversation-health report over agent_turn events. Every field is
// computed except ResolutionRate, which stays nil unless the app actually sends a `resolved`
// bool on a turn — we never invent whether a conversation was resolved.
type Conversations struct {
	Event          string   `json:"event"`
	Conversations  int      `json:"conversations"`
	Turns          int      `json:"turns"`
	MedianTurns    float64  `json:"median_turns"`
	P90Turns       float64  `json:"p90_turns"`
	ReAskRate      float64  `json:"reask_rate"`   // fraction with >=2 consecutive user turns (no assistant between)
	AbandonRate    float64  `json:"abandon_rate"` // fraction whose last turn is a user turn
	TTFTP50        float64  `json:"ttft_p50"`     // time-to-first-token percentiles over ttft_ms
	TTFTP90        float64  `json:"ttft_p90"`
	TTFTP99        float64  `json:"ttft_p99"`
	ResolutionRate *float64 `json:"resolution_rate"` // nil unless a turn carries a `resolved` bool
}

// ComputeConversations groups agent_turn events (already filtered + dev-env-scoped) by
// conversation_id within [from, to). Turns per conversation drive the median/p90 (via
// trends.Percentile). Ordering each conversation by timestamp yields re-ask (two consecutive
// user turns with no assistant between) and abandon (ends on a user turn). TTFT percentiles run
// over every turn's ttft_ms. ResolutionRate is computed ONLY when at least one turn carried a
// `resolved` bool anywhere in the set — otherwise it is nil (the honesty covenant).
func ComputeConversations(events []event.Event, from, to time.Time) Conversations {
	out := Conversations{Event: EventTurn}
	byConv := map[string][]event.Event{}
	var ttft []float64
	sawResolved := false
	for _, e := range events {
		if e.Name != EventTurn || !inWindow(e, from, to) {
			continue
		}
		cid := "(none)"
		if v, ok := e.Properties["conversation_id"]; ok {
			cid = valueOf(v)
		}
		byConv[cid] = append(byConv[cid], e)
		if f, ok := numOf(e.Properties["ttft_ms"]); ok {
			ttft = append(ttft, f)
		}
		if _, ok := boolOf(e.Properties["resolved"]); ok {
			sawResolved = true
		}
		out.Turns++
	}
	out.Conversations = len(byConv)
	if out.Conversations == 0 {
		return out
	}
	turnCounts := make([]float64, 0, len(byConv))
	reask, abandon, resolved := 0, 0, 0
	for _, evs := range byConv {
		turnCounts = append(turnCounts, float64(len(evs)))
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })
		roles := make([]string, len(evs))
		convResolved := false
		for i, e := range evs {
			roles[i] = valueOf(e.Properties["role"])
			if b, ok := boolOf(e.Properties["resolved"]); ok && b {
				convResolved = true
			}
		}
		for i := 0; i+1 < len(roles); i++ {
			if roles[i] == "user" && roles[i+1] == "user" {
				reask++
				break
			}
		}
		if roles[len(roles)-1] == "user" {
			abandon++
		}
		if convResolved {
			resolved++
		}
	}
	n := float64(out.Conversations)
	// median (not nearest-rank p50): a field NAMED median must agree with the engine's Median
	// measure, or "median turns" here and "median" in /v1/trends are two different numbers
	out.MedianTurns = trends.MedianOf(turnCounts)
	out.P90Turns = trends.Percentile(turnCounts, 0.90)
	out.ReAskRate = float64(reask) / n
	out.AbandonRate = float64(abandon) / n
	out.TTFTP50 = trends.Percentile(ttft, 0.50)
	out.TTFTP90 = trends.Percentile(ttft, 0.90)
	out.TTFTP99 = trends.Percentile(ttft, 0.99)
	if sawResolved {
		r := float64(resolved) / n
		out.ResolutionRate = &r
	}
	return out
}

// inWindow reports whether e falls in the half-open [from, to) window; a zero bound is
// unbounded on that side. Identical semantics to the rest of the engine (Compute, scopeWindow).
func inWindow(e event.Event, from, to time.Time) bool {
	ts := e.Timestamp.UTC()
	if !from.IsZero() && ts.Before(from) {
		return false
	}
	if !to.IsZero() && !ts.Before(to) {
		return false
	}
	return true
}

// valueOf stringifies a property value the same way query.Breakdown does (so buckets align).
func valueOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return strconv.FormatFloat(toFloat(v), 'f', -1, 64)
}

func toFloat(v any) float64 {
	f, _ := numOf(v)
	return f
}

// numOf coerces a JSON-decoded property value to a float, mirroring trends' numeric coercion:
// JSON numbers, Go ints, and numeric strings all count; anything else is not numeric.
func numOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// boolOf reports a property's boolean value AND whether it was actually a bool-shaped value.
// The ok flag is what gates ResolutionRate: only a real boolean (Go bool, or the JSON strings
// "true"/"false") counts as the app having sent a `resolved` signal — a number or arbitrary
// string does not, so we never mistake unrelated data for a resolution flag.
func boolOf(v any) (val, ok bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		switch b {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// isErr reports whether an agent_tool_call's `error` property marks the call as failed. A missing
// property is not an error; a bool, a truthy number, or "true"/"1"/"yes" is.
func isErr(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case float32:
		return b != 0
	case int:
		return b != 0
	case int64:
		return b != 0
	case string:
		switch b {
		case "true", "1", "yes":
			return true
		}
	}
	return false
}
