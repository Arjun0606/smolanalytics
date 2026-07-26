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
			"name":        "define_event",
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
			"description": "Remove a defined event by name. Reports stop resolving it immediately (the underlying autocapture rows are untouched). Returns deleted:true only if a defined event with that name existed; deleted:false means nothing was removed.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}},
		},
	)
}

// conditionShapes heads every `where` decode error: the shape define_event needs,
// plus a working example, so the model can self-correct on the next call instead of
// reading encoding/json's raw "cannot unmarshal string into Go struct field" leak.
const conditionShapes = `where must be an array of condition objects like [{"field":"text","op":"contains","value":"Buy"}]. field is one of text|id|classes|href|path|tag|name, op is one of equals|contains|prefix`

// ConditionSet is []defined.Condition that decodes what an agent naturally writes:
// the canonical array, or a single condition object minus its array wrapper.
// Anything else is a self-correcting error naming the shape, never a Go type name.
type ConditionSet []defined.Condition

func (cs *ConditionSet) UnmarshalJSON(b []byte) error {
	switch shape := jsonShape(b); shape {
	case "null":
		*cs = nil
		return nil
	case "an array":
		var arr []defined.Condition
		if err := json.Unmarshal(b, &arr); err != nil {
			return &argError{fmt.Sprintf("%s, and one of the entries isn't that shape", conditionShapes)}
		}
		*cs = arr
		return nil
	case "an object":
		// one condition without the array wrapper is the obvious near-miss, so accept it
		var c defined.Condition
		if err := json.Unmarshal(b, &c); err != nil || c.Field == "" {
			return &argError{fmt.Sprintf("%s, got an object that isn't a condition", conditionShapes)}
		}
		*cs = ConditionSet{c}
		return nil
	default:
		return &argError{fmt.Sprintf("%s, got %s", conditionShapes, shape)}
	}
}

func (s *Server) callDefined(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "define_event":
		if s.defined == nil {
			return true, "", fmt.Errorf(noStore, "defined-events")
		}
		var p struct {
			Name        string       `json:"name"`
			Event       string       `json:"event"`
			Where       ConditionSet `json:"where"`
			Description string       `json:"description"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
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
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if strings.TrimSpace(p.Name) == "" {
			return true, "", fmt.Errorf("name is required — list_defined_events to get names")
		}
		found, err := s.defined.Delete(p.Name)
		if err != nil {
			return true, "", err
		}
		rm := removal{kind: "defined event", field: "name", list: "list_defined_events"}
		return true, jsonStr(rm.result(found, p.Name)), nil
	}
	return false, "", nil
}
