package mcp

// Action tools — the "get stuff done from your editor" half of the MCP surface.
// Reads answer questions; these CHANGE things: save reports, define cohorts, set up
// alerts and webhooks. Each mutates the same stores the dashboard uses, so anything
// created here appears there instantly (and vice versa). All are no-ops with a clear
// error when the server runs without persistent stores (e.g. bare demo mode).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

func zeroTime() time.Time { return time.Time{} }

// SetInsights / SetCohorts / SetWebhooks / SetAlerts attach the persistent stores.
// The API server forwards its own stores here so both surfaces share one source of truth.
func (s *Server) SetInsights(st *insights.Store) { s.insights = st }
func (s *Server) SetCohorts(st *cohort.Store)    { s.cohorts = st }
func (s *Server) SetWebhooks(st *webhook.Store)  { s.webhooks = st }
func (s *Server) SetAlerts(st *alert.Store)      { s.alerts = st }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_alert",
			"description": "Set up an alert: fire when an event's count over a rolling window crosses a threshold (checked every 5 minutes, delivered to the instance's webhooks). Use for 'tell me if signups drop below 10 a day' (op=lt, window_hours=24) or 'alert on a checkout spike'. Add a webhook first if none exists or the alert has nowhere to fire.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":         map[string]any{"type": "string", "description": "Human label, e.g. 'signup drop'"},
					"event":        map[string]any{"type": "string", "description": "Exact event name to watch"},
					"op":           map[string]any{"type": "string", "enum": []string{"gt", "lt"}, "description": "Fire when count is greater-than (spike) or less-than (drop) the threshold"},
					"threshold":    map[string]any{"type": "number"},
					"window_hours": map[string]any{"type": "integer", "description": "Rolling window in hours (e.g. 24)"},
				},
				"required": []string{"name", "event", "op", "threshold", "window_hours"},
			},
		},
		map[string]any{
			"name":        "list_alerts",
			"description": "List configured alerts with their last-checked value and last fire time.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_alert",
			"description": "Delete an alert by id (get ids from list_alerts).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "add_webhook",
			"description": "Register a webhook endpoint that receives the daily digest and alert fires. Slack incoming-webhook URLs (hooks.slack.com) are auto-detected and get Slack's {\"text\": ...} format with a plain-text rendering; any other HTTPS endpoint gets signed JSON (X-Smolanalytics-Signature, HMAC-SHA256) and the response includes the signing secret — show it to the user once. Follow up with test_webhook to prove the URL actually receives deliveries.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Label, e.g. 'slack #alerts'"},
					"url":    map[string]any{"type": "string", "description": "HTTPS endpoint to POST to"},
					"format": map[string]any{"type": "string", "enum": []string{"slack"}, "description": "Force Slack text format for Slack-compatible receivers on other hosts (Mattermost, Rocket.Chat). hooks.slack.com URLs get it automatically."},
				},
				"required": []string{"name", "url"},
			},
		},
		map[string]any{
			"name":        "list_webhooks",
			"description": "List registered webhook endpoints with their delivery format (secrets redacted).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_webhook",
			"description": "Delete a webhook endpoint by id (get ids from list_webhooks).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "test_webhook",
			"description": "Fire a REAL test delivery to a webhook — same format, signing, and network path as alerts and the daily digest — and report the HTTP status the endpoint answered with. Use it right after add_webhook to prove the URL works, or when the user says alerts aren't arriving. Get ids from list_webhooks or the add_webhook response.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Webhook id to test"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "create_cohort",
			"description": "Define a named user group once, reusable as a filter on any report (cohort=<name>). Users qualify by having done the events — match=any (default) or all. Example: 'Paying users' = did checkout.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"events": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Qualifying event names"},
					"match":  map[string]any{"type": "string", "enum": []string{"any", "all"}, "description": "Qualify on any (default) or all of the events"},
				},
				"required": []string{"name", "events"},
			},
		},
		map[string]any{
			"name":        "create_sequence_cohort",
			"description": "Define a SEQUENCED behavioral cohort — users who did events IN ORDER, deeper than create_cohort's membership. Example: 'did signup then activate within 7 days but not churn'. Reusable as a filter on any report (cohort=<name>).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"steps": map[string]any{
						"type":        "array",
						"description": "The ordered steps that must happen in sequence. Each: {event, optional count (min total occurrences of it), optional filters (property conditions on that event)}.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"event":   map[string]any{"type": "string"},
								"count":   map[string]any{"type": "integer", "description": "min total occurrences of this event (default 1)"},
								"filters": map[string]any{"type": "array", "description": "property conditions on this step's event, [{property, op, value}]"},
							},
							"required": []string{"event"},
						},
					},
					"within_days":       map[string]any{"type": "number", "description": "max days between the first and last step (omit = any time)"},
					"within_first_days": map[string]any{"type": "number", "description": "all steps must fall within this many days of the user's first-ever event (omit = off)"},
					"exclude":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "event names that must NOT occur within the matched span"},
				},
				"required": []string{"name", "steps"},
			},
		},
		map[string]any{
			"name":        "list_cohorts",
			"description": "List defined cohorts with their current member counts.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_cohort",
			"description": "Delete a cohort by id (get ids from list_cohorts).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "save_report",
			"description": "Pin a report to the dashboard's Saved Reports so the user sees it on every visit. type: funnel|trend|breakdown|retention. params mirror the matching report tool's arguments as strings, e.g. funnel: {\"steps\":\"signup,activate,checkout\"}; trend: {\"event\":\"signup\"}; breakdown: {\"event\":\"signup\",\"property\":\"source\"}.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Label shown on the dashboard"},
					"type":   map[string]any{"type": "string", "enum": []string{"funnel", "trend", "breakdown", "retention"}},
					"params": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				},
				"required": []string{"name", "type", "params"},
			},
		},
		map[string]any{
			"name":        "list_saved_reports",
			"description": "List reports pinned to the dashboard.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_saved_report",
			"description": "Unpin a saved report by id (get ids from list_saved_reports).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
	)
}

const noStore = "this instance is running without persistent %s storage (bare demo mode) — run `smolanalytics serve` (or connect to your running server over HTTP) and try again"

// callAction dispatches the mutation tools. Returns (handled, result, error).
func (s *Server) callAction(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "create_alert":
		if s.alerts == nil {
			return true, "", fmt.Errorf(noStore, "alert")
		}
		var p struct {
			Name        string  `json:"name"`
			Event       string  `json:"event"`
			Op          string  `json:"op"`
			Threshold   float64 `json:"threshold"`
			WindowHours int     `json:"window_hours"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Op != "gt" && p.Op != "lt" {
			return true, "", fmt.Errorf(`op must be "gt" (spike) or "lt" (drop), got %q`, p.Op)
		}
		if p.WindowHours <= 0 {
			return true, "", fmt.Errorf("window_hours must be positive, got %d", p.WindowHours)
		}
		if err := s.knownEvent(p.Event); err != nil {
			return true, "", err
		}
		a, err := s.alerts.Add(alert.Alert{Name: p.Name, Event: p.Event, Op: p.Op, Threshold: p.Threshold, WindowHours: p.WindowHours, Enabled: true})
		if err != nil {
			return true, "", err
		}
		hint := ""
		if s.webhooks == nil || len(s.webhooks.List()) == 0 {
			hint = " NOTE: no webhook is configured yet, so this alert has nowhere to fire — add one with add_webhook."
		}
		return true, jsonStr(map[string]any{"created": a, "note": "checked every 5 minutes; fires to the instance's webhooks." + hint}), nil

	case "list_alerts":
		if s.alerts == nil {
			return true, "", fmt.Errorf(noStore, "alert")
		}
		return true, jsonStr(map[string]any{"alerts": s.alerts.List()}), nil

	case "delete_alert":
		return s.deleteByID(args, "alert", func(id string) error { return s.alerts.Delete(id) }, s.alerts == nil)

	case "add_webhook":
		if s.webhooks == nil {
			return true, "", fmt.Errorf(noStore, "webhook")
		}
		var p struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			Format string `json:"format"` // "" = auto-detect (hooks.slack.com → slack), "slack" to force — same contract as the HTTP API
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		ep, err := s.webhooks.Add(p.Name, p.URL, p.Format)
		if err != nil {
			return true, "", err
		}
		created := map[string]any{"id": ep.ID, "name": ep.Name, "url": ep.URL, "format": formatOf(ep)}
		if ep.SlackFormat() {
			// no secret: Slack can't verify signature headers, so deliveries are plain
			// {"text": ...} messages and there is nothing for the user to store.
			return true, jsonStr(map[string]any{
				"created": created,
				"note":    "Slack-format endpoint — deliveries are sent as Slack {\"text\": ...} messages (alerts and the daily digest rendered as plain text), not signed JSON. Run test_webhook with this id next to confirm the channel receives it.",
			}), nil
		}
		return true, jsonStr(map[string]any{
			"created": created,
			"secret":  ep.Secret,
			"note":    "payloads are signed with this secret (X-Smolanalytics-Signature, HMAC-SHA256 hex). Shown once — tell the user to store it if they verify signatures. Run test_webhook with this id to confirm delivery.",
		}), nil

	case "list_webhooks":
		if s.webhooks == nil {
			return true, "", fmt.Errorf(noStore, "webhook")
		}
		list := s.webhooks.List()
		out := make([]map[string]any, 0, len(list))
		for _, e := range list {
			out = append(out, map[string]any{"id": e.ID, "name": e.Name, "url": e.URL, "format": formatOf(e), "enabled": e.Enabled})
		}
		return true, jsonStr(map[string]any{"webhooks": out}), nil

	case "delete_webhook":
		return s.deleteByID(args, "webhook", func(id string) error { return s.webhooks.Delete(id) }, s.webhooks == nil)

	case "test_webhook":
		if s.webhooks == nil {
			return true, "", fmt.Errorf(noStore, "webhook")
		}
		var p struct{ ID string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if strings.TrimSpace(p.ID) == "" {
			return true, "", fmt.Errorf("id is required — list_webhooks to get ids")
		}
		ep, ok := s.webhooks.Get(p.ID)
		if !ok {
			return true, "", fmt.Errorf("no webhook with id %q — list_webhooks to see what exists", p.ID)
		}
		status, err := webhook.SendTest(ep)
		who := "the endpoint"
		if ep.SlackFormat() {
			who = "Slack"
		}
		if err != nil {
			if status == 0 {
				return true, "", fmt.Errorf("test delivery to %q got no HTTP response (%v) — check the URL is reachable from this server", ep.Name, err)
			}
			if ep.SlackFormat() && status == 404 {
				return true, "", fmt.Errorf("404: the URL looks revoked — recreate it in Slack (workspace settings → Incoming Webhooks), then add_webhook the new URL and delete_webhook %s", ep.ID)
			}
			return true, "", fmt.Errorf("%s said %d — the delivery reached it but was rejected; check the receiver's logs (delete_webhook + add_webhook if the URL changed)", who, status)
		}
		return true, jsonStr(map[string]any{
			"delivered": true,
			"status":    status,
			"result":    fmt.Sprintf("%s said %d — deliveries to %q work.", who, status, ep.Name),
		}), nil

	case "create_cohort":
		if s.cohorts == nil {
			return true, "", fmt.Errorf(noStore, "cohort")
		}
		var p struct {
			Name   string   `json:"name"`
			Events []string `json:"events"`
			Match  string   `json:"match"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if len(p.Events) == 0 {
			return true, "", fmt.Errorf("events must name at least one qualifying event")
		}
		for _, ev := range p.Events {
			if err := s.knownEvent(ev); err != nil {
				return true, "", err
			}
		}
		d, err := s.cohorts.Save(cohort.Definition{Name: p.Name, Events: p.Events, Match: p.Match})
		if err != nil {
			return true, "", err
		}
		evs, _ := s.store.Range(zeroTime(), zeroTime())
		members := len(cohort.Resolve(evs, d))
		return true, jsonStr(map[string]any{"created": d, "current_members": members, "note": "reusable on any report via its name"}), nil

	case "create_sequence_cohort":
		if s.cohorts == nil {
			return true, "", fmt.Errorf(noStore, "cohort")
		}
		var p struct {
			Name            string        `json:"name"`
			Steps           []cohort.Step `json:"steps"`
			WithinDays      float64       `json:"within_days"`
			WithinFirstDays float64       `json:"within_first_days"`
			Exclude         []string      `json:"exclude"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if len(p.Steps) == 0 {
			return true, "", fmt.Errorf("steps must name at least one event, in order")
		}
		for _, st := range p.Steps {
			if st.Event == "" {
				return true, "", fmt.Errorf("each step needs an event")
			}
			if err := s.knownEvent(st.Event); err != nil {
				return true, "", err
			}
		}
		dayMs := float64(24 * time.Hour / time.Millisecond)
		seq := &cohort.Sequence{
			Steps:         p.Steps,
			WithinMs:      int64(p.WithinDays * dayMs),
			WithinFirstMs: int64(p.WithinFirstDays * dayMs),
			Exclude:       p.Exclude,
		}
		d, err := s.cohorts.Save(cohort.Definition{Name: p.Name, Sequence: seq})
		if err != nil {
			return true, "", err
		}
		evs, _ := s.store.Range(zeroTime(), zeroTime())
		members := len(cohort.Resolve(evs, d))
		return true, jsonStr(map[string]any{"created": d, "current_members": members, "note": "sequenced cohort, reusable on any report via its name"}), nil

	case "list_cohorts":
		if s.cohorts == nil {
			return true, "", fmt.Errorf(noStore, "cohort")
		}
		evs, _ := s.store.Range(zeroTime(), zeroTime())
		list := s.cohorts.List()
		out := make([]map[string]any, 0, len(list))
		for _, d := range list {
			out = append(out, map[string]any{"id": d.ID, "name": d.Name, "match": d.Match, "events": d.Events, "members": len(cohort.Resolve(evs, d))})
		}
		return true, jsonStr(map[string]any{"cohorts": out}), nil

	case "delete_cohort":
		return s.deleteByID(args, "cohort", func(id string) error { return s.cohorts.Delete(id) }, s.cohorts == nil)

	case "save_report":
		if s.insights == nil {
			return true, "", fmt.Errorf(noStore, "saved-report")
		}
		var p struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Params map[string]string `json:"params"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		switch p.Type {
		case "funnel", "trend", "breakdown", "retention":
		default:
			return true, "", fmt.Errorf("type must be funnel|trend|breakdown|retention, got %q", p.Type)
		}
		in, err := s.insights.Save(insights.Insight{Name: p.Name, Type: p.Type, Params: p.Params})
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"saved": in, "note": "now pinned on the dashboard under Saved Reports"}), nil

	case "list_saved_reports":
		if s.insights == nil {
			return true, "", fmt.Errorf(noStore, "saved-report")
		}
		return true, jsonStr(map[string]any{"saved_reports": s.insights.List()}), nil

	case "delete_saved_report":
		return s.deleteByID(args, "saved report", func(id string) error { return s.insights.Delete(id) }, s.insights == nil)
	}
	return false, "", nil
}

// formatOf names an endpoint's delivery contract for tool output: "slack"
// ({"text": ...} plain-text messages) or "json" (signed JSON).
func formatOf(e webhook.Endpoint) string {
	if e.SlackFormat() {
		return "slack"
	}
	return "json"
}

// deleteByID is the shared shape of every delete tool: parse id, guard nil store, delete.
func (s *Server) deleteByID(args json.RawMessage, kind string, del func(string) error, storeNil bool) (bool, string, error) {
	if storeNil {
		return true, "", fmt.Errorf(noStore, kind)
	}
	var p struct{ ID string }
	if err := unmarshalArgs(args, &p); err != nil {
		return true, "", err
	}
	if strings.TrimSpace(p.ID) == "" {
		return true, "", fmt.Errorf("id is required — list first to get ids")
	}
	if err := del(p.ID); err != nil {
		return true, "", err
	}
	return true, jsonStr(map[string]any{"deleted": p.ID}), nil
}

// knownEvent guards mutations against typo'd event names the same way reports do —
// a misspelled alert would silently never fire, which is worse than an error.
func (s *Server) knownEvent(name string) error {
	names, err := s.store.Names()
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == name {
			return nil
		}
	}
	return fmt.Errorf("unknown event %q — tracked events are: %s", name, strings.Join(names, ", "))
}

// applyDefaultScope runs the query layer's default production scope (excludes
// env=development) without any extra filters.
func applyDefaultScope(evs []event.Event) []event.Event { return query.Apply(evs, nil) }

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
