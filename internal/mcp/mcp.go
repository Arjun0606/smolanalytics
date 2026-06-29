// Package mcp exposes the analytics engine over the Model Context Protocol so the
// user connects smolanalytics to THEIR OWN Claude / Cursor / Claude Code and asks
// questions in plain English — their model calls these tools, we never call a
// model ourselves (no API keys, no inference cost on our side). Same model as a
// Telegram-bot-style MCP: we make the data trivially askable, the user's agent
// does the reasoning.
//
// Two transports share one Dispatch: newline-delimited JSON-RPC over stdio (for
// local Claude Desktop / Cursor) and Streamable HTTP at POST /mcp (point a remote
// MCP client at the running server, sharing its live data).
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

const protocolVersion = "2024-11-05"

const instructions = `You are a sharp product analyst with live access to this user's own product analytics — their real events, on their own instance. Nothing is shared with anyone; you (their model) do the reasoning, for free, right here in their editor. The whole point: they never build a report, they just ask you.

How to work:
- Orient first. Call overview for the headline numbers and list_events to see exactly what's tracked. Never invent event or property names — use the real ones.
- Pick the right tool: funnel (conversion + where users drop off), retention (do they come back), trends (counts over time, optionally broken down by a property), breakdown (segment by a property), lifecycle (new/returning/resurrected/dormant), stickiness (DAU/WAU/MAU), paths (what users do after an event), groups (B2B accounts), recent_events (debug instrumentation), user_activity (one user's timeline). Every report accepts filters to segment (e.g. plan=pro, source=hacker news).
- Answer like an analyst, not a database. Lead with the number, say what it means, then offer the most useful next cut. If conversion dropped, find the step; if a segment underperforms, name it; if retention is flat, say so plainly.
- Be concrete and honest. Quote the real figures. If the data is too thin to conclude, say that instead of guessing.
- For open-ended asks ("how's the product doing?"), proactively pull the 2-3 most telling reports and synthesize a short read.`

type Server struct {
	store store.Store
}

func New(s store.Store) *Server { return &Server{store: s} }

// --- JSON-RPC envelope ---

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Dispatch handles one JSON-RPC request. Returns nil for notifications (no id /
// no reply expected), matching the MCP wire contract.
func (s *Server) Dispatch(req request) *response {
	reply := func(result any) *response { return &response{JSONRPC: "2.0", ID: req.ID, Result: result} }
	fail := func(code int, msg string) *response {
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		return reply(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "smolanalytics", "version": "0.1.0"},
			"instructions":    instructions,
		})
	case "notifications/initialized", "notifications/cancelled":
		return nil // notification — no response
	case "ping":
		return reply(map[string]any{})
	case "tools/list":
		return reply(map[string]any{"tools": toolList})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		text, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			return reply(map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
		}
		return reply(map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		})
	default:
		return fail(-32601, "method not found: "+req.Method)
	}
}

// ServeStdio runs the newline-delimited JSON-RPC loop on stdin/stdout. Protocol
// goes on stdout; logs must go to stderr only.
func (s *Server) ServeStdio() error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := os.Stdout
	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if resp := s.Dispatch(req); resp != nil {
			b, _ := json.Marshal(resp)
			_, _ = out.Write(append(b, '\n'))
		}
	}
	return in.Err()
}

// HTTPDispatch handles one Streamable-HTTP MCP request body (POST /mcp), returning
// the HTTP status and response bytes. Notifications return 202 with a nil body.
func (s *Server) HTTPDispatch(body []byte) (status int, resp []byte) {
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return 400, []byte(`{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error"}}`)
	}
	r := s.Dispatch(req)
	if r == nil {
		return 202, nil
	}
	b, _ := json.Marshal(r)
	return 200, b
}

// --- the data tools ---

func (s *Server) all() ([]event.Event, error) { return s.store.Range(time.Time{}, time.Time{}) }

func (s *Server) callTool(name string, args json.RawMessage) (string, error) {
	evs, err := s.all()
	if err != nil {
		return "", err
	}
	switch name {
	case "overview":
		return s.toolOverview(evs)
	case "list_events":
		names, _ := s.store.Names()
		return jsonText(map[string]any{"events": names})
	case "funnel":
		var a struct {
			Steps       []string       `json:"steps"`
			WindowHours float64        `json:"window_hours"`
			Filters     []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if len(a.Steps) < 2 {
			return "", fmt.Errorf("funnel needs at least two steps (event names), e.g. [\"signup\",\"checkout\"]")
		}
		steps := make([]funnel.Step, len(a.Steps))
		for i, n := range a.Steps {
			steps[i] = funnel.Step{Event: n}
		}
		window := time.Duration(a.WindowHours) * time.Hour
		if window == 0 {
			window = 7 * 24 * time.Hour
		}
		return jsonText(funnel.Compute(query.Apply(evs, a.Filters), steps, window))
	case "retention":
		var a struct {
			Event   string         `json:"event"`
			Days    int            `json:"days"`
			Filters []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Days <= 0 {
			a.Days = 7
		}
		return jsonText(summarizeRetention(retention.Compute(query.Apply(evs, a.Filters), a.Days, a.Event)))
	case "trends":
		var a struct {
			Event     string         `json:"event"`
			Unique    bool           `json:"unique"`
			Breakdown string         `json:"breakdown"`
			Filters   []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		ev := query.Apply(evs, a.Filters)
		if a.Breakdown != "" {
			return jsonText(map[string]any{"event": a.Event, "breakdown": a.Breakdown,
				"series": trends.ComputeBreakdown(ev, a.Event, a.Breakdown, time.Time{}, time.Time{}, a.Unique)})
		}
		return jsonText(trends.Compute(ev, a.Event, time.Time{}, time.Time{}, a.Unique))
	case "breakdown":
		var a struct {
			Event    string         `json:"event"`
			Property string         `json:"property"`
			Filters  []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Property == "" {
			return "", fmt.Errorf("breakdown needs a property to group by, e.g. \"source\"")
		}
		var filtered []event.Event
		for _, e := range query.Apply(evs, a.Filters) {
			if a.Event == "" || e.Name == a.Event {
				filtered = append(filtered, e)
			}
		}
		groups := query.Breakdown(filtered, a.Property)
		rows := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			rows = append(rows, map[string]any{"value": g.Value, "count": g.Count})
		}
		return jsonText(map[string]any{"event": a.Event, "property": a.Property, "groups": rows})
	case "recent_events":
		var a struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Limit <= 0 {
			a.Limit = 20
		}
		cp := make([]event.Event, len(evs))
		copy(cp, evs)
		sort.Slice(cp, func(i, j int) bool { return cp[i].Timestamp.After(cp[j].Timestamp) })
		if len(cp) > a.Limit {
			cp = cp[:a.Limit]
		}
		return jsonText(map[string]any{"events": cp})
	case "user_activity":
		var a struct {
			DistinctID string `json:"distinct_id"`
		}
		_ = json.Unmarshal(args, &a)
		if a.DistinctID == "" {
			return "", fmt.Errorf("user_activity needs a distinct_id")
		}
		return jsonText(userProfile(evs, a.DistinctID))
	case "lifecycle":
		var a struct {
			Days    int            `json:"days"`
			Filters []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Days <= 0 {
			a.Days = 30
		}
		return jsonText(map[string]any{"days": engagement.ComputeLifecycle(query.Apply(evs, a.Filters), a.Days)})
	case "stickiness":
		var a struct {
			Filters []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		return jsonText(engagement.ComputeStickiness(query.Apply(evs, a.Filters), time.Time{}))
	case "paths":
		var a struct {
			Start   string         `json:"start"`
			Depth   int            `json:"depth"`
			Filters []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Start == "" {
			return "", fmt.Errorf("paths needs a start event")
		}
		if a.Depth <= 0 {
			a.Depth = 3
		}
		return jsonText(paths.After(query.Apply(evs, a.Filters), a.Start, a.Depth))
	case "groups":
		var a struct {
			Property string         `json:"property"`
			Limit    int            `json:"limit"`
			Filters  []query.Filter `json:"filters"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Property == "" {
			return "", fmt.Errorf("groups needs a group property, e.g. \"company\"")
		}
		return jsonText(groups.Compute(query.Apply(evs, a.Filters), a.Property, time.Time{}, a.Limit))
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// userProfile summarizes one user's timeline + latest traits for the model.
func userProfile(evs []event.Event, id string) map[string]any {
	var mine []event.Event
	for _, e := range evs {
		if e.DistinctID == id {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return map[string]any{"distinct_id": id, "found": false}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Timestamp.Before(mine[j].Timestamp) })
	traits := map[string]any{}
	counts := map[string]int{}
	for _, e := range mine {
		counts[e.Name]++
		for k, v := range e.Properties {
			traits[k] = v
		}
	}
	return map[string]any{
		"distinct_id":  id,
		"found":        true,
		"event_count":  len(mine),
		"first_seen":   mine[0].Timestamp,
		"last_seen":    mine[len(mine)-1].Timestamp,
		"traits":       traits,
		"event_counts": counts,
	}
}

func (s *Server) toolOverview(evs []event.Event) (string, error) {
	seen := map[string]bool{}
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	active := map[string]bool{}
	for _, e := range evs {
		seen[e.DistinctID] = true
		if e.Timestamp.After(cutoff) {
			active[e.DistinctID] = true
		}
	}
	names, _ := s.store.Names()
	sort.Strings(names)
	return jsonText(map[string]any{
		"total_users":         len(seen),
		"active_users_7d":     len(active),
		"total_events":        len(evs),
		"tracked_event_names": names,
	})
}

func summarizeRetention(rr retention.Result) map[string]any {
	var size, d1, d7 int
	for _, c := range rr.Cohorts {
		size += c.Size
		if len(c.Returned) > 1 {
			d1 += c.Returned[1]
		}
		if len(c.Returned) > 7 {
			d7 += c.Returned[7]
		}
	}
	out := map[string]any{"cohort_users": size, "max_days": rr.MaxDays}
	if size > 0 {
		out["day1_retention_pct"] = int(float64(d1)/float64(size)*100 + 0.5)
		out["day7_retention_pct"] = int(float64(d7)/float64(size)*100 + 0.5)
	}
	out["cohorts"] = rr.Cohorts
	return out
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
