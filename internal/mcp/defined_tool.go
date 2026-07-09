package mcp

// The retroactive-event tools: name a business event ("checkout") from autocaptured
// clicks/pageviews you already have, with zero tracking code, retroactive to install.
// The agent can propose these from real captured selectors, and the dashboard's
// "save as event" builder writes the same primitive — one path, two front doors.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/defined"
)

func init() {
	toolList = append(toolList,
		map[string]any{
			"name": "define_event",
			"description": "Create (or replace) a retroactive, zero-code event from autocaptured data. It names a slice of $click / $pageview / $form_submit rows and becomes a first-class event across every report, retroactive to install — no tracking code needed. Example: define \"checkout\" as $click where text contains \"Buy\". Great for a non-technical user, and you can propose one when you see many uncaptured clicks on a key element.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "The event name (no leading $)"},
					"event":       map[string]any{"type": "string", "enum": []string{"$click", "$pageview", "$form_submit", "$rageclick", "$deadclick"}, "description": "The autocapture event to build from"},
					"where":       map[string]any{"type": "array", "description": "Conditions (all must match), e.g. [{\"field\":\"text\",\"op\":\"contains\",\"value\":\"Buy\"}]", "items": map[string]any{"type": "object", "properties": map[string]any{"field": map[string]any{"type": "string", "enum": []string{"text", "id", "classes", "href", "path", "tag", "name"}}, "op": map[string]any{"type": "string", "enum": []string{"equals", "contains", "prefix"}}, "value": map[string]any{"type": "string"}}}},
					"description": map[string]any{"type": "string"},
				},
				"required": []string{"name", "event", "where"},
			},
		},
		map[string]any{
			"name":        "list_defined_events",
			"description": "List the retroactive defined events (name, the autocapture event and conditions each is built from).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_defined_event",
			"description": "Remove a defined event by name. Reports stop resolving it immediately (the underlying autocapture rows are untouched).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}},
		},
	)
}

func (s *Server) callDefined(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "define_event":
		if s.defined == nil {
			return true, "", fmt.Errorf(noStore, "defined-events")
		}
		var p struct {
			Name        string               `json:"name"`
			Event       string               `json:"event"`
			Where       []defined.Condition  `json:"where"`
			Description string               `json:"description"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return true, "", err
		}
		d, err := s.defined.Save(defined.Definition{Name: p.Name, Event: p.Event, Where: p.Where, Description: p.Description})
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"defined": d, "note": "now a first-class event across all reports, retroactive to install — try it in a funnel or `list_events`"}), nil

	case "list_defined_events":
		if s.defined == nil {
			return true, "", fmt.Errorf(noStore, "defined-events")
		}
		return true, jsonStr(map[string]any{"defined_events": s.defined.List()}), nil

	case "delete_defined_event":
		if s.defined == nil {
			return true, "", fmt.Errorf(noStore, "defined-events")
		}
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return true, "", err
		}
		if strings.TrimSpace(p.Name) == "" {
			return true, "", fmt.Errorf("name is required")
		}
		if err := s.defined.Delete(p.Name); err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"deleted": p.Name}), nil
	}
	return false, "", nil
}
