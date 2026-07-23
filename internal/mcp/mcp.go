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
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/defined"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/exportlink"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/gsc"
	"github.com/Arjun0606/smolanalytics/internal/heatmap"
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
- ALWAYS answer analytics questions through these tools — never by fetching the HTTP API yourself, scraping the dashboard, or estimating. The tools ARE the deterministic report engine; a CI test proves their numbers equal the dashboard's. Time questions map directly: "last 6 hours" = trends(hours=6, interval="hour"), "last 7 days"/"past week" = trends(days=7), any range = from/to. NOTE: "this week" means the current calendar week (since Monday), NOT the last 7 days — use trends(from=<this Monday 00:00 UTC>, to=now) for it, so your answer matches the ask bar and dashboard. Reserve days=N for "last N days".
- Orient first. Call overview for the headline numbers and list_events to see exactly what's tracked. Never invent event or property names — use the real ones.
- Pick the right tool: funnel (conversion + where users drop off), retention (do they come back), trends (counts over time, optionally broken down by a property), breakdown (segment by a property), web_overview (traffic: visitors, live-now, top pages, referrers, UTM sources, devices), lifecycle (new/returning/resurrected/dormant), stickiness (DAU/WAU/MAU), paths (what users do after an event), groups (B2B accounts), recent_events (debug instrumentation), user_activity (one user's timeline). Every report accepts filters to segment (e.g. plan=pro, source=hacker news).
- Answer like an analyst, not a database. Lead with the number, say what it means, then offer the most useful next cut. If conversion dropped, find the step; if a segment underperforms, name it; if retention is flat, say so plainly.
- Be concrete and honest. Quote the real figures. If the data is too thin to conclude, say that instead of guessing.
- For open-ended asks ("how's the product doing?"), proactively pull the 2-3 most telling reports and synthesize a short read.
- Presentation matters: lead with what's URGENT or moved, quantify it, then offer the single most useful next cut — curate, don't dump a field list. overview returns a one-line "read" and a "next" suggestion; use them as your opening, then go deeper only where it earns attention. A tight, prioritized answer beats an exhaustive one.
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
	deploys   *deploys.Store
	flags     *flag.Store
	gsc       *gsc.Store
	exports   *exportlink.Store
	aliases   *alias.Map     // identity stitching for imported $identify events
	defined   *defined.Store // retroactive zero-code events
}

// SetDefined attaches the retroactive defined-events store (the define_event tools).
func (s *Server) SetDefined(d *defined.Store) { s.defined = d }

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
			Steps       []string            `json:"steps"`
			WindowHours float64             `json:"window_hours"`
			Breakdown   string              `json:"breakdown"`
			Days        float64             `json:"days"`
			From        string              `json:"from"`
			To          string              `json:"to"`
			Filters     FilterSet           `json:"filters"`
			Order       string              `json:"order"`
			Exclude     []string            `json:"exclude"`
			StepFilters []map[string]string `json:"step_filters"`
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		// scope which events enter the funnel by the time range (days/from/to), like
		// GET /v1/funnel — the tool used to swallow these and always run all-time.
		fFrom, fTo, fwErr := mcpWindow(a.Days, 0, a.From, a.To)
		if fwErr != nil {
			return "", fwErr
		}
		evs = scopeWindow(evs, fFrom, fTo)
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
		// breakdown: conversion by a property (segment by the user's first step-0 value) —
		// same shape as GET /v1/funnel?breakdown=, locked by agreement_test.
		if a.Breakdown != "" {
			return jsonText(map[string]any{"steps": a.Steps, "breakdown": a.Breakdown,
				"segments": funnel.ComputeBreakdown(query.StampFirstTouch(query.ScopeUsers(evs, a.Filters, false), a.Breakdown), steps, window, a.Breakdown)})
		}
		order, oerr := funnel.ParseOrder(a.Order)
		if oerr != nil {
			return "", oerr
		}
		return jsonText(funnel.ComputeOpts(query.ScopeUsers(evs, a.Filters, false), steps, window,
			funnel.Options{Order: order, Exclusions: a.Exclude, StepFilters: a.StepFilters}))
	case "retention":
		var a struct {
			Event   string    `json:"event"`
			Days    int       `json:"days"`
			Bucket  string    `json:"bucket"`
			Rolling bool      `json:"rolling"`
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
		// validate the bucket like GET /v1/retention — an unrecognized bucket ("weekly",
		// "Week", "fortnight") used to be silently computed as DAILY, answering a different
		// granularity than asked. Normalize obvious aliases, reject the rest.
		switch a.Bucket {
		case "", "day", "week", "month":
		case "daily":
			a.Bucket = "day"
		case "weekly":
			a.Bucket = "week"
		case "monthly":
			a.Bucket = "month"
		default:
			return "", fmt.Errorf("unknown bucket %q (want day, week or month)", a.Bucket)
		}
		if err := s.checkEvents(a.Event); err != nil {
			return "", err
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		return jsonText(summarizeRetention(retention.ComputeBucketed(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), a.Days, a.Event, a.Bucket, a.Rolling)))
	case "trends":
		var a struct {
			Event     string    `json:"event"`
			Unique    bool      `json:"unique"`
			Breakdown string    `json:"breakdown"`
			Measure   string    `json:"measure"`
			Property  string    `json:"property"`
			Days      float64   `json:"days"`
			Hours     float64   `json:"hours"`
			From      string    `json:"from"`
			To        string    `json:"to"`
			Interval  string    `json:"interval"`
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		// the time grammar, identical to GET /v1/trends: days/hours are rolling
		// windows ending now; from/to are absolute; interval buckets the series
		var from, to time.Time
		nowW := time.Now().UTC()
		// validate like GET /v1/trends (parseTrendWindow): days must be a positive integer,
		// hours a positive number — a negative/NaN/fractional value used to slip through
		// (days=0.5 computed a FUTURE from bound; days=-1 fell back to the default 7-day total).
		if math.IsNaN(a.Days) || math.IsInf(a.Days, 0) || a.Days < 0 || a.Days != math.Trunc(a.Days) {
			return "", fmt.Errorf("days must be a positive integer")
		}
		if math.IsNaN(a.Hours) || math.IsInf(a.Hours, 0) || a.Hours < 0 {
			return "", fmt.Errorf("hours must be a positive number")
		}
		// clamp to the SAME bounds GET /v1/trends uses (parseTrendWindow: days<=365,
		// hours<=24*366). Without this, days=10000000 built ~10M daily buckets in
		// ComputeInterval — ~1GB allocated in a single call (an OOM/DoS on the shared
		// engine) — AND overflowed the year past 9999, leaking a raw marshal error, AND
		// disagreed with the (clamped) GET path for the same question.
		if a.Days > 365 {
			a.Days = 365
		}
		if a.Hours > 24*366 {
			a.Hours = 24 * 366
		}
		if a.Days > 0 {
			// calendar-day aligned, IDENTICAL to GET /v1/trends (parseTrendWindow): N
			// whole day-buckets ending today, so MCP and the dashboard never disagree
			// (the old rolling calc prepended a phantom empty leading day — a covenant break).
			from = nowW.Truncate(24*time.Hour).AddDate(0, 0, -(int(a.Days) - 1))
			to = nowW // "last N days" ends now, never the future — matches parseTrendWindow
		}
		if a.Hours > 0 {
			from = nowW.Add(-time.Duration(a.Hours * float64(time.Hour)))
			to = nowW // cap at now: a future-dated event must not diverge MCP from /v1/ask
		}
		if a.From != "" {
			if t, err := parseWhen(a.From); err == nil {
				from = t
			} else {
				return "", fmt.Errorf("bad from %q (want RFC3339 or YYYY-MM-DD)", a.From)
			}
		}
		if a.To != "" {
			if t, err := parseWhen(a.To); err == nil {
				to = t
			} else {
				return "", fmt.Errorf("bad to %q (want RFC3339 or YYYY-MM-DD)", a.To)
			}
		}
		// absolute from/to windows get the SAME now-cap as days/hours — IDENTICAL to
		// parseTrendWindow, so a clock-skewed future event can never make from=<today>
		// answer differently than days=1 for the same question. Only the fully-unbounded
		// call (no window args) keeps future events, matching the raw export.
		if a.From != "" || a.To != "" {
			if to.IsZero() || to.After(nowW) {
				to = nowW
			}
		}
		iv, iverr := trends.ParseInterval(a.Interval)
		if iverr != nil {
			return "", iverr
		}
		ev := query.Apply(query.StampForFilters(evs, a.Filters), a.Filters)
		// XAU: measure=dau|wau|mau plots rolling distinct-actives per day — mirrors
		// GET /v1/trends, which accepts these; the MCP tool used to reject them so the
		// active-users question was unanswerable via MCP.
		switch a.Measure {
		case "dau":
			return jsonText(trends.ComputeXAU(ev, a.Event, from, to, 1))
		case "wau":
			return jsonText(trends.ComputeXAU(ev, a.Event, from, to, 7))
		case "mau":
			return jsonText(trends.ComputeXAU(ev, a.Event, from, to, 30))
		}
		// numeric aggregation over a property (revenue, AOV, p90) — mirrors GET /v1/trends
		// with measure=; window is all-events here (no from/to arg), same as Compute below.
		if a.Measure != "" {
			if a.Property == "" {
				return "", fmt.Errorf("measure needs a numeric property (e.g. property=amount)")
			}
			m, ok := trends.ParseMeasure(a.Measure)
			if !ok {
				return "", fmt.Errorf("unknown measure %q (want sum, avg, min, max, median, p90, p95 or p99)", a.Measure)
			}
			// measure + breakdown = the aggregate per group ("revenue by plan") — the same
			// ComputeMeasureBreakdown GET /v1/trends serializes, so the two can't disagree.
			// This combination used to silently drop the breakdown and return the grand total.
			if a.Breakdown != "" {
				series := trends.ComputeMeasureBreakdown(ev, a.Event, a.Property, m, a.Breakdown, from, to)
				if !trends.FiniteSeries(series) {
					return "", fmt.Errorf("the %s of %q overflows a float64 (the result is not a finite number) — values this large can't be aggregated exactly; check the property's unit (e.g. cents vs dollars)", m, a.Property)
				}
				return jsonText(map[string]any{"event": a.Event, "property": a.Property,
					"measure": string(m), "breakdown": a.Breakdown, "series": series})
			}
			res := trends.ComputeMeasure(ev, a.Event, a.Property, m, from, to)
			// a +Inf/NaN aggregate must be an explicit tool error, never a leaked
			// "json: unsupported value: +Inf" marshal string.
			if !res.Finite() {
				return "", fmt.Errorf("the %s of %q overflows a float64 (the result is not a finite number) — values this large can't be aggregated exactly; check the property's unit (e.g. cents vs dollars)", m, a.Property)
			}
			return jsonText(res)
		}
		if a.Breakdown != "" {
			return jsonText(map[string]any{"event": a.Event, "breakdown": a.Breakdown,
				"series": trends.ComputeBreakdown(ev, a.Event, a.Breakdown, from, to, a.Unique)})
		}
		return jsonText(trends.ComputeInterval(ev, a.Event, from, to, a.Unique, iv))
	case "breakdown":
		var a struct {
			Event    string    `json:"event"`
			Property string    `json:"property"`
			Unique   bool      `json:"unique"`
			Days     float64   `json:"days"`
			Hours    float64   `json:"hours"`
			From     string    `json:"from"`
			To       string    `json:"to"`
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		// the shared time grammar — the tool used to silently IGNORE days/from/to and
		// return the all-time split as if it were windowed, disagreeing with
		// GET /v1/trends?breakdown and the MCP trends tool for the same question.
		bFrom, bTo, werr := mcpWindow(a.Days, a.Hours, a.From, a.To)
		if werr != nil {
			return "", werr
		}
		var filtered []event.Event
		for _, e := range query.Apply(query.StampForFilters(evs, a.Filters), a.Filters) {
			if a.Event != "" && e.Name != a.Event {
				continue
			}
			ts := e.Timestamp.UTC()
			if !bFrom.IsZero() && ts.Before(bFrom) {
				continue
			}
			if !bTo.IsZero() && !ts.Before(bTo) {
				continue
			}
			filtered = append(filtered, e)
		}
		groups := query.Breakdown(filtered, a.Property)
		// every event fell into "(none)" → the property doesn't exist on these events;
		// error with what IS available instead of returning a bucket the model misreads.
		if len(filtered) > 0 && len(groups) == 1 && groups[0].Value == "(none)" {
			return "", fmt.Errorf("no %q events carry property %q — properties seen: %s", a.Event, a.Property, strings.Join(knownProps(filtered), ", "))
		}
		rows := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			usered := make(map[string]struct{}, len(g.Events))
			for _, e := range g.Events {
				usered[e.DistinctID] = struct{}{}
			}
			visitors := len(usered)
			count := g.Count
			if a.Unique {
				count = visitors
			}
			rows = append(rows, map[string]any{"value": g.Value, "count": count, "events": g.Count, "visitors": visitors})
		}
		if a.Unique {
			sort.SliceStable(rows, func(i, j int) bool { return rows[i]["count"].(int) > rows[j]["count"].(int) })
		}
		return jsonText(map[string]any{"event": a.Event, "property": a.Property, "unique": a.Unique, "groups": rows})
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		return jsonText(web.Compute(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), a.Days, time.Time{}))
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		return jsonText(map[string]any{"days": engagement.ComputeLifecycle(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), a.Days)})
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		return jsonText(engagement.ComputeStickiness(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), time.Time{}))
	case "whats_notable":
		return jsonText(map[string]any{"findings": insight.Generate(evs)})
	case "paths":
		var a struct {
			Start   string    `json:"start"`
			Depth   int       `json:"depth"`
			Days    float64   `json:"days"`
			Hours   float64   `json:"hours"`
			From    string    `json:"from"`
			To      string    `json:"to"`
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		if a.Depth <= 0 {
			a.Depth = 3
		}
		if a.Depth > 10 { // same cap as GET /v1/paths — surfaces must agree
			a.Depth = 10
		}
		pFrom, pTo, pErr := mcpWindow(a.Days, a.Hours, a.From, a.To)
		if pErr != nil {
			return "", pErr
		}
		return jsonText(paths.After(scopeWindow(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), pFrom, pTo), a.Start, a.Depth))
	case "heatmap":
		var a struct {
			Path     string    `json:"path"`
			Viewport string    `json:"viewport"`
			Cols     int       `json:"cols"`
			RowPx    int       `json:"row_px"`
			Days     float64   `json:"days"`
			Hours    float64   `json:"hours"`
			From     string    `json:"from"`
			To       string    `json:"to"`
			Filters  FilterSet `json:"filters"`
		}
		if err := unmarshalArgs(args, &a); err != nil {
			return "", err
		}
		if a.Path == "" {
			return "", fmt.Errorf("heatmap needs a path (get one from web_overview top_pages)")
		}
		if err := query.Validate(a.Filters); err != nil {
			return "", err
		}
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		hFrom, hTo, hErr := mcpWindow(a.Days, a.Hours, a.From, a.To)
		if hErr != nil {
			return "", hErr
		}
		return jsonText(heatmap.Compute(scopeWindow(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), hFrom, hTo), a.Path, a.Viewport, a.Cols, a.RowPx))
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
		if err := guardFilters(evs, a.Filters); err != nil {
			return "", err
		}
		res := groups.Compute(query.Apply(query.StampForFilters(evs, a.Filters), a.Filters), a.Property, time.Time{}, a.Limit)
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
		if handled, out, derr := s.callDeploys(name, args); handled {
			return out, derr
		}
		if handled, out, ferr := s.callFlags(name, args); handled {
			return out, ferr
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
		if handled, out, nerr := s.callInstrument(name, args); handled {
			return out, nerr
		}
		if handled, out, derr := s.callDefined(name, args); handled {
			return out, derr
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

// toolOverview is the ORIENT tool — the first thing an agent calls. It's a sharp, at-a-glance
// "state of the product": totals, this-week-vs-last deltas, the headline event's momentum, a
// one-line read, and the natural next call. Rich by design so the agent leads with meaning,
// not a bare row of counts. (MCP-only convenience — not part of the /v1 covenant.)
func (s *Server) toolOverview(evs []event.Event) (string, error) {
	now := time.Now().UTC()
	w1, w2 := now.AddDate(0, 0, -7), now.AddDate(0, 0, -14)
	seen := map[string]bool{}
	act1, act2 := map[string]bool{}, map[string]bool{}
	byName1, byName2 := map[string]int{}, map[string]int{}
	events7 := 0
	for _, e := range evs {
		seen[e.DistinctID] = true
		switch {
		case e.Timestamp.After(w1):
			act1[e.DistinctID] = true
			byName1[e.Name]++
			events7++
		case e.Timestamp.After(w2):
			act2[e.DistinctID] = true
			byName2[e.Name]++
		}
	}
	names, _ := s.store.Names()
	sort.Strings(names)

	headline := pickHeadlineEvent(names, byName1)
	out := map[string]any{
		"total_users":         len(seen),
		"active_users_7d":     len(act1),
		"active_users_wow":    wowDir(len(act1), len(act2)),
		"events_7d":           events7,
		"total_events":        len(evs),
		"tracked_event_names": names,
	}
	readParts := []string{fmt.Sprintf("%s users", commaInt(len(seen))),
		fmt.Sprintf("%s active in the last 7d (%s WoW)", commaInt(len(act1)), wowDir(len(act1), len(act2)))}
	if headline != "" {
		out["headline_event"] = headline
		out["headline_7d"] = byName1[headline]
		out["headline_wow"] = wowDir(byName1[headline], byName2[headline])
		readParts = append(readParts, fmt.Sprintf("%s %s (%s)", headline, commaInt(byName1[headline]), wowDir(byName1[headline], byName2[headline])))
	}
	out["read"] = strings.Join(readParts, " · ")
	if len(evs) == 0 {
		out["read"] = "No events yet — install the SDK, or call propose_instrumentation to wire it up."
		out["next"] = "propose_instrumentation"
	} else {
		out["next"] = "Call whats_notable for the single biggest lever, funnel for where users drop off, or retention for whether they come back."
	}
	return jsonText(out)
}

// pickHeadlineEvent chooses the event that best represents product progress: the conventional
// conversion event if tracked, else the highest-volume non-autocapture event this week.
func pickHeadlineEvent(names []string, vol map[string]int) string {
	for _, pref := range []string{"signup", "sign_up", "purchase", "checkout", "subscribe", "activate"} {
		for _, n := range names {
			if strings.EqualFold(n, pref) {
				return n
			}
		}
	}
	best, bestN := "", 0
	for _, n := range names {
		if strings.HasPrefix(n, "$") { // skip autocapture ($pageview, $click, $engagement)
			continue
		}
		if vol[n] > bestN {
			best, bestN = n, vol[n]
		}
	}
	return best
}

// wowDir renders a compact week-over-week direction (+13% / -8% / flat / new).
func wowDir(cur, prev int) string {
	if prev == 0 {
		if cur > 0 {
			return "new"
		}
		return "flat"
	}
	d := (cur - prev) * 100 / prev
	switch {
	case d > 3:
		return fmt.Sprintf("+%d%%", d)
	case d < -3:
		return fmt.Sprintf("%d%%", d)
	default:
		return "flat"
	}
}

// commaInt formats an int with thousands separators (1200 -> "1,200") for the read line.
func commaInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func summarizeRetention(rr retention.Result) map[string]any {
	// retention.Summarize is the SAME honest headline computation the HTTP API serializes,
	// so the two surfaces can never disagree (agreement_test enforces it). We add the raw
	// grid + a model-facing note on top.
	now := time.Now().UTC()
	out := retention.Summarize(rr, now)
	out["cohorts"] = retention.SerializeCohorts(rr, now)
	out["note"] = "period-N percentages only include cohorts old enough to observe period N; per-cohort rows show raw counts (Returned[n] of Size)."
	return out
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseWhen accepts RFC3339 or bare YYYY-MM-DD, the same grammar as /v1.
func parseWhen(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// guardFilters rejects a filter naming a property that exists on NO event — almost always a
// typo (plann=pro). GET /v1 already errors here (query_api.go filtered()); without the same
// guard these tools returned a silent 0 presented as a real answer, so the two surfaces
// disagreed on the identical filter. Value mismatches on a real property still return an
// honest 0; only an unknown KEY is an error.
func guardFilters(evs []event.Event, fs FilterSet) error {
	if bad, known := query.FirstUnknownProp(evs, fs); bad != "" {
		return fmt.Errorf("no events carry the property %q, so this filter can only ever match 0 — check the spelling. Properties seen: %s", bad, strings.Join(known, ", "))
	}
	return nil
}

// scopeWindow keeps events in [from, to); a zero bound is unbounded on that side. Shared by
// the report tools that filter by a resolved mcpWindow (paths, breakdown).
func scopeWindow(evs []event.Event, from, to time.Time) []event.Event {
	if from.IsZero() && to.IsZero() {
		return evs
	}
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		ts := e.Timestamp.UTC()
		if !from.IsZero() && ts.Before(from) {
			continue
		}
		if !to.IsZero() && !ts.Before(to) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// mcpWindow resolves the shared days/hours/from/to time grammar IDENTICALLY to the trends
// tool and GET /v1 (parseTrendWindow): days are calendar-day-aligned whole buckets ending
// now, hours roll back from now, absolute from/to get the same now-cap as rolling windows
// (an explicit window never extends into the future), and no args at all = all recorded
// history (zero/zero). One definition, so no tool can disagree with another on the window.
func mcpWindow(days, hours float64, fromStr, toStr string) (from, to time.Time, err error) {
	now := time.Now().UTC()
	// reject non-finite / negative windows instead of computing a garbage bound off NaN
	// (now.Add(-NaN) is a nonsense time) or silently flipping a negative into the future.
	if math.IsNaN(days) || math.IsInf(days, 0) || days < 0 {
		return from, to, fmt.Errorf("days must be a non-negative number")
	}
	if math.IsNaN(hours) || math.IsInf(hours, 0) || hours < 0 {
		return from, to, fmt.Errorf("hours must be a non-negative number")
	}
	if days > 365 {
		days = 365
	}
	if hours > 24*366 {
		hours = 24 * 366
	}
	if days > 0 {
		from = now.Truncate(24*time.Hour).AddDate(0, 0, -(int(days) - 1))
		to = now
	}
	if hours > 0 {
		from = now.Add(-time.Duration(hours * float64(time.Hour)))
		to = now
	}
	if fromStr != "" {
		t, e := parseWhen(fromStr)
		if e != nil {
			return from, to, fmt.Errorf("bad from %q (want RFC3339 or YYYY-MM-DD)", fromStr)
		}
		from = t.UTC()
	}
	if toStr != "" {
		t, e := parseWhen(toStr)
		if e != nil {
			return from, to, fmt.Errorf("bad to %q (want RFC3339 or YYYY-MM-DD)", toStr)
		}
		to = t.UTC()
	}
	if fromStr != "" || toStr != "" {
		if to.IsZero() || to.After(now) {
			to = now
		}
	}
	return from, to, nil
}
