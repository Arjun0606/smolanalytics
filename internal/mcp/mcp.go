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
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/exportlink"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/gsc"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/web"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

const protocolVersion = "2025-03-26" // Streamable HTTP; we echo the client's version

const instructions = `You are a sharp product analyst with live access to this user's own product analytics — their real events, on their own instance. Nothing is shared with anyone; you (their model) do the reasoning, for free, right here in their editor. The whole point: they never build a report, they just ask you.

How to work:
- Orient first. Call overview for the headline numbers and list_events to see exactly what's tracked. Never invent event or property names — use the real ones.
- Pick the right tool: funnel (conversion + where users drop off), retention (do they come back), trends (counts over time, optionally broken down by a property), breakdown (segment by a property), web_overview (traffic: visitors, live-now, top pages, referrers, UTM sources, devices), lifecycle (new/returning/resurrected/dormant), stickiness (DAU/WAU/MAU), paths (what users do after an event), groups (B2B accounts), recent_events (debug instrumentation), user_activity (one user's timeline). Every report accepts filters to segment (e.g. plan=pro, source=hacker news).
- Answer like an analyst, not a database. Lead with the number, say what it means, then offer the most useful next cut. If conversion dropped, find the step; if a segment underperforms, name it; if retention is flat, say so plainly.
- Be concrete and honest. Quote the real figures. If the data is too thin to conclude, say that instead of guessing.
- For open-ended asks ("how's the product doing?"), proactively pull the 2-3 most telling reports and synthesize a short read.
- You can also DO things, not just read: create_alert ("tell me if signups drop below 10/day" → op=lt, window_hours=24), add_webhook (Slack/HTTPS endpoint the alerts and daily digest fire to; Slack URLs get readable text messages) then test_webhook (prove the delivery lands), create_cohort (define a user group once, reuse anywhere), save_report (pin a funnel/trend/breakdown to their dashboard). When the user says "watch this", "alert me", "save that" — reach for these, then confirm what you created by echoing it back.`

type Server struct {
	store store.Store
	// optional persistent stores backing the action tools (create_alert, save_report, …);
	// nil in bare demo/stdio-without-files mode, where those tools explain how to enable them.
	insights  *insights.Store
	cohorts   *cohort.Store
	webhooks  *webhook.Store
	alerts    *alert.Store
	settings  *settings.Store
	trackplan *trackplan.Store
	goals     *goal.Store
	shares    *share.Store
	gsc       *gsc.Store
	exports   *exportlink.Store
	aliases   *alias.Map // identity stitching for imported $identify events
}

// SetSettings / SetTrackPlan attach the instance-control stores.
func (s *Server) SetSettings(st *settings.Store)   { s.settings = st }
func (s *Server) SetTrackPlan(tp *trackplan.Store) { s.trackplan = tp }

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
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion // echo what the client speaks for max compatibility
		if pv == "" {
			pv = protocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}},
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
		// a mis-shaped params envelope must be an explicit error, not an empty tool
		// name that dispatches nowhere.
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return fail(-32602, "invalid params: "+err.Error())
			}
		}
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
		if r := s.dispatchPrompts(req.Method, req.Params, reply, fail); r != nil {
			return r
		}
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

// checkEvents rejects event names that aren't tracked, listing what IS — so a
// misspelled step comes back as a self-correcting error instead of a report full of
// zeros the model would read as "0 conversions". Empty names are allowed (they mean
// "any event" where a tool supports that).
func (s *Server) checkEvents(names ...string) error {
	known, err := s.store.Names()
	if err != nil {
		return err
	}
	if len(known) == 0 {
		return fmt.Errorf("no events ingested yet — nothing to analyze")
	}
	set := make(map[string]bool, len(known))
	for _, n := range known {
		set[n] = true
	}
	for _, n := range names {
		if n != "" && !set[n] {
			sort.Strings(known)
			return fmt.Errorf("unknown event %q — tracked events are: %s", n, strings.Join(known, ", "))
		}
	}
	return nil
}

func (s *Server) callTool(name string, args json.RawMessage) (string, error) {
	// reject malformed arguments JSON once for every tool, with a clear message — rather
	// than letting each handler silently see zero-valued args and emit a confusing error.
	if len(args) > 0 && !json.Valid(args) {
		return "", fmt.Errorf("invalid arguments: not valid JSON")
	}
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
			Steps       []string  `json:"steps"`
			WindowHours float64   `json:"window_hours"`
			Filters     FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if len(a.Steps) < 2 {
			return "", fmt.Errorf("funnel needs at least two steps (event names), e.g. [\"signup\",\"checkout\"]")
		}
		if err := s.checkEvents(a.Steps...); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		steps := make([]funnel.Step, len(a.Steps))
		for i, n := range a.Steps {
			steps[i] = funnel.Step{Event: n}
		}
		// multiply as float BEFORE converting: time.Duration(0.5) truncates to 0, which
		// would silently turn "30-minute window" into NO window at all.
		window := time.Duration(a.WindowHours * float64(time.Hour))
		if window <= 0 {
			window = 7 * 24 * time.Hour
		}
		return jsonText(funnel.Compute(query.Apply(evs, a.Filters), steps, window))
	case "retention":
		var a struct {
			Event   string    `json:"event"`
			Days    int       `json:"days"`
			Filters FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Days <= 0 {
			a.Days = 7
		}
		if a.Days > 90 {
			a.Days = 90 // same cap as the HTTP API — one question, one answer (agreement_test enforces this)
		}
		if err := s.checkEvents(a.Event); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		return jsonText(summarizeRetention(retention.Compute(query.Apply(evs, a.Filters), a.Days, a.Event)))
	case "trends":
		var a struct {
			Event     string    `json:"event"`
			Unique    bool      `json:"unique"`
			Breakdown string    `json:"breakdown"`
			Filters   FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if err := s.checkEvents(a.Event); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		ev := query.Apply(evs, a.Filters)
		if a.Breakdown != "" {
			return jsonText(map[string]any{"event": a.Event, "breakdown": a.Breakdown,
				"series": trends.ComputeBreakdown(ev, a.Event, a.Breakdown, time.Time{}, time.Time{}, a.Unique)})
		}
		return jsonText(trends.Compute(ev, a.Event, time.Time{}, time.Time{}, a.Unique))
	case "breakdown":
		var a struct {
			Event    string    `json:"event"`
			Property string    `json:"property"`
			Filters  FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Property == "" {
			return "", fmt.Errorf("breakdown needs a property to group by, e.g. \"source\"")
		}
		if err := s.checkEvents(a.Event); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		var filtered []event.Event
		for _, e := range query.Apply(evs, a.Filters) {
			if a.Event == "" || e.Name == a.Event {
				filtered = append(filtered, e)
			}
		}
		groups := query.Breakdown(filtered, a.Property)
		// every event fell into "(none)" → the property doesn't exist on these events;
		// error with what IS available instead of returning a bucket the model misreads.
		if len(filtered) > 0 && len(groups) == 1 && groups[0].Value == "(none)" {
			return "", fmt.Errorf("no %q events carry property %q — properties seen: %s", a.Event, a.Property, strings.Join(knownProps(filtered), ", "))
		}
		rows := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			rows = append(rows, map[string]any{"value": g.Value, "count": g.Count})
		}
		return jsonText(map[string]any{"event": a.Event, "property": a.Property, "groups": rows})
	case "web_overview":
		var a struct {
			Days    int       `json:"days"`
			Filters FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		return jsonText(web.Compute(query.Apply(evs, a.Filters), a.Days, time.Time{}))
	case "recent_events":
		var a struct {
			Limit int `json:"limit"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
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
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.DistinctID == "" {
			return "", fmt.Errorf("user_activity needs a distinct_id")
		}
		return jsonText(userProfile(evs, a.DistinctID))
	case "lifecycle":
		var a struct {
			Days    int       `json:"days"`
			Filters FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Days <= 0 {
			a.Days = 30
		}
		if a.Days > 180 { // same cap as GET /v1/lifecycle — surfaces must agree
			a.Days = 180
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		return jsonText(map[string]any{"days": engagement.ComputeLifecycle(query.Apply(evs, a.Filters), a.Days)})
	case "stickiness":
		var a struct {
			Filters FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		return jsonText(engagement.ComputeStickiness(query.Apply(evs, a.Filters), time.Time{}))
	case "whats_notable":
		return jsonText(map[string]any{"findings": insight.Generate(evs)})
	case "paths":
		var a struct {
			Start   string    `json:"start"`
			Depth   int       `json:"depth"`
			Filters FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Start == "" {
			return "", fmt.Errorf("paths needs a start event")
		}
		if err := s.checkEvents(a.Start); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		if a.Depth <= 0 {
			a.Depth = 3
		}
		if a.Depth > 10 { // same cap as GET /v1/paths — surfaces must agree
			a.Depth = 10
		}
		return jsonText(paths.After(query.Apply(evs, a.Filters), a.Start, a.Depth))
	case "groups":
		var a struct {
			Property string    `json:"property"`
			Limit    int       `json:"limit"`
			Filters  FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Property == "" {
			return "", fmt.Errorf("groups needs a group property, e.g. \"company\"")
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		res := groups.Compute(query.Apply(evs, a.Filters), a.Property, time.Time{}, a.Limit)
		// no event carries this property → say so (with what IS available) instead of
		// returning zeros the model would read as "you have 0 accounts".
		if res.TotalGroups == 0 && len(evs) > 0 {
			return "", fmt.Errorf("no events carry property %q — properties seen on your events: %s", a.Property, strings.Join(knownProps(evs), ", "))
		}
		return jsonText(res)
	default:
		if handled, out, aerr := s.callAction(name, args); handled {
			return out, aerr
		}
		if handled, out, cerr := s.callControl(name, args); handled {
			return out, cerr
		}
		if handled, out, gerr := s.callGoals(name, args); handled {
			return out, gerr
		}
		if handled, out, serr := s.callShares(name, args); handled {
			return out, serr
		}
		if handled, out, gserr := s.callGSC(name, args); handled {
			return out, gserr
		}
		if handled, out, ierr := s.callImport(name, args); handled {
			return out, ierr
		}
		if handled, out, xerr := s.callExportLink(name, args); handled {
			return out, xerr
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// knownProps lists the distinct property keys seen on events (sorted, capped) —
// used in error messages so the model can self-correct a bad property name.
func knownProps(evs []event.Event) []string {
	set := map[string]bool{}
	for _, e := range evs {
		for k := range e.Properties {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 40 {
		out = out[:40]
	}
	return out
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
	var size int
	for _, c := range rr.Cohorts {
		size += c.Size
	}
	out := map[string]any{"cohort_users": size, "max_days": rr.MaxDays}
	// Honest day-N summaries: only cohorts old enough to observe day N are in the
	// denominator (retention.DayN), and a day we can't measure yet is OMITTED, never
	// reported as 0% — the model must not be handed a fabricated number.
	now := time.Now().UTC()
	for _, n := range []int{1, 7, 30} {
		if ret, sz := retention.DayN(rr, n, now); sz > 0 {
			out[fmt.Sprintf("day%d_retention_pct", n)] = int(float64(ret)/float64(sz)*100 + 0.5)
			out[fmt.Sprintf("day%d_cohort_users", n)] = sz
		}
	}
	out["cohorts"] = rr.Cohorts
	out["note"] = "day-N percentages only include cohorts old enough to observe day N; per-cohort rows show raw counts (Returned[n] of Size)."
	return out
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
