package mcp

// Instance-control + instrumentation tools — full parity with the settings screens,
// so the editor can run the whole instance: project settings, retention, API keys,
// the tracking plan, and instrumentation verification. The goal: an agent scaffolds
// an app, wires tracking, declares the plan, verifies events flow, sets up alerts —
// without the user ever opening a browser.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "get_settings",
			"description": "Read the instance configuration: project name, timezone, retention days, whether dashboard auth is set, and how many API keys exist. Orient here before changing settings.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "set_project",
			"description": "Rename the project and/or set its IANA timezone (e.g. 'Europe/Berlin'). Omit a field to leave it unchanged.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":     map[string]any{"type": "string"},
					"timezone": map[string]any{"type": "string"},
				},
			},
		},
		map[string]any{
			"name":        "set_retention",
			"description": "Set how many days of events to keep (0 = keep forever). Older events are pruned periodically. Confirm with the user before shortening retention — pruned data is gone.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"days": map[string]any{"type": "integer", "minimum": 0}},
				"required":   []string{"days"},
			},
		},
		map[string]any{
			"name":        "list_api_keys",
			"description": "List ingest API keys (ids and names only — the key material is never shown again after creation).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "create_api_key",
			"description": "Create a new ingest API key (e.g. one per app or environment). Returns the key ONCE — show it to the user and tell them to store it.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Label, e.g. 'production web'"}}, "required": []string{"name"}},
		},
		map[string]any{
			"name":        "revoke_api_key",
			"description": "Revoke an ingest API key by id (get ids from list_api_keys). Events signed with it stop being accepted immediately.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "set_tracking_plan",
			"description": "Declare the app's intended instrumentation: every event it should send, with expected property keys. Replaces the whole plan. Do this right after wiring tracking code, then use instrumentation_health to verify reality matches. Example: [{\"name\":\"signup\",\"description\":\"account created\",\"properties\":[\"plan\",\"source\"]}]",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"events": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":        map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
								"properties":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
							"required": []string{"name"},
						},
					},
				},
				"required": []string{"events"},
			},
		},
		map[string]any{
			"name":        "instrumentation_health",
			"description": "Verify instrumentation against the tracking plan: per planned event — is it arriving (count, last seen), are its expected properties present? Plus events arriving that aren't in the plan. THE tool to run after wiring or changing tracking code; also great for 'is my tracking broken?'.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"window_hours": map[string]any{"type": "integer", "description": "Only look at events in the last N hours (default: all time)"}},
			},
		},
	)
}

func (s *Server) callControl(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "get_settings":
		if s.settings == nil {
			return true, "", fmt.Errorf(noStore, "settings")
		}
		return true, jsonStr(map[string]any{
			"project":        s.settings.ProjectName(),
			"timezone":       s.settings.Timezone(),
			"retention_days": s.settings.RetainDays(),
			"auth_set":       s.settings.HasPassword(),
			"api_keys":       len(s.settings.Keys()),
		}), nil

	case "set_project":
		if s.settings == nil {
			return true, "", fmt.Errorf(noStore, "settings")
		}
		var p struct{ Name, Timezone string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Name == "" && p.Timezone == "" {
			return true, "", fmt.Errorf("nothing to change — pass name and/or timezone")
		}
		if p.Name == "" {
			p.Name = s.settings.ProjectName()
		}
		if p.Timezone == "" {
			p.Timezone = s.settings.Timezone()
		} else if _, err := time.LoadLocation(p.Timezone); err != nil {
			return true, "", fmt.Errorf("unknown timezone %q — use an IANA name like Europe/Berlin", p.Timezone)
		}
		if err := s.settings.UpdateProject(p.Name, p.Timezone); err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"project": p.Name, "timezone": p.Timezone}), nil

	case "set_retention":
		if s.settings == nil {
			return true, "", fmt.Errorf(noStore, "settings")
		}
		var p struct {
			Days int `json:"days"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Days < 0 {
			return true, "", fmt.Errorf("days must be >= 0 (0 keeps everything forever)")
		}
		if err := s.settings.SetRetainDays(p.Days); err != nil {
			return true, "", err
		}
		note := "events older than this are pruned periodically"
		if p.Days == 0 {
			note = "retention disabled — everything is kept"
		}
		return true, jsonStr(map[string]any{"retention_days": p.Days, "note": note}), nil

	case "list_api_keys":
		if s.settings == nil {
			return true, "", fmt.Errorf(noStore, "settings")
		}
		keys := s.settings.Keys()
		out := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, map[string]any{"id": k.ID, "name": k.Name, "created": k.Created})
		}
		return true, jsonStr(map[string]any{"api_keys": out, "note": "key material is only shown at creation"}), nil

	case "create_api_key":
		if s.settings == nil {
			return true, "", fmt.Errorf(noStore, "settings")
		}
		var p struct{ Name string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if strings.TrimSpace(p.Name) == "" {
			return true, "", fmt.Errorf("give the key a name (e.g. 'production web') so it's identifiable later")
		}
		k, err := s.settings.AddKey(p.Name)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{
			"created": map[string]any{"id": k.ID, "name": k.Name},
			"key":     k.Key,
			"note":    "shown ONCE — tell the user to store it now.",
		}), nil

	case "revoke_api_key":
		return s.deleteByID(args, "settings", func(id string) error { return s.settings.RevokeKey(id) }, s.settings == nil)

	case "set_tracking_plan":
		if s.trackplan == nil {
			return true, "", fmt.Errorf(noStore, "tracking-plan")
		}
		var p struct {
			Events []trackplan.PlannedEvent `json:"events"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if len(p.Events) == 0 {
			return true, "", fmt.Errorf("declare at least one planned event")
		}
		plan, err := s.trackplan.Set(p.Events)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"plan": plan, "note": "now run instrumentation_health (after the app has sent traffic) to verify reality matches"}), nil

	case "instrumentation_health":
		if s.trackplan == nil {
			return true, "", fmt.Errorf(noStore, "tracking-plan")
		}
		plan := s.trackplan.Get()
		if len(plan.Events) == 0 {
			return true, "", fmt.Errorf("no tracking plan declared yet — set one with set_tracking_plan, then this tool verifies events against it")
		}
		var p struct {
			WindowHours int `json:"window_hours"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		from := time.Time{}
		if p.WindowHours > 0 {
			from = time.Now().UTC().Add(-time.Duration(p.WindowHours) * time.Hour)
		}

		type stat struct {
			count    int
			lastSeen time.Time
			props    map[string]bool
		}
		seen := map[string]*stat{}
		if err := s.store.Scan(from, time.Time{}, func(e event.Event) error {
			st := seen[e.Name]
			if st == nil {
				st = &stat{props: map[string]bool{}}
				seen[e.Name] = st
			}
			st.count++
			if e.Timestamp.After(st.lastSeen) {
				st.lastSeen = e.Timestamp
			}
			for k := range e.Properties {
				st.props[k] = true
			}
			return nil
		}); err != nil {
			return true, "", err
		}

		planned := map[string]bool{}
		report := make([]map[string]any, 0, len(plan.Events))
		healthy := true
		for _, pe := range plan.Events {
			planned[pe.Name] = true
			st := seen[pe.Name]
			row := map[string]any{"event": pe.Name}
			if st == nil {
				row["status"] = "MISSING — never seen"
				healthy = false
			} else {
				row["status"] = "flowing"
				row["count"] = st.count
				row["last_seen"] = st.lastSeen.Format(time.RFC3339)
				var missing []string
				for _, prop := range pe.Properties {
					if !st.props[prop] {
						missing = append(missing, prop)
					}
				}
				if len(missing) > 0 {
					row["missing_properties"] = missing
					healthy = false
				}
			}
			report = append(report, row)
		}
		var unplanned []string
		for name := range seen {
			if !planned[name] && !strings.HasPrefix(name, "$") { // autocapture events are expected
				unplanned = append(unplanned, name)
			}
		}
		sort.Strings(unplanned)
		return true, jsonStr(map[string]any{
			"healthy":          healthy,
			"planned":          report,
			"unplanned_events": unplanned,
			"note":             "MISSING = tracking code for that event isn't firing (or hasn't run yet); missing_properties = the event arrives without keys the plan expects.",
		}), nil
	}
	return false, "", nil
}
