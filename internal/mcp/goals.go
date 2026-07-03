package mcp

// Goal tools — "what counts as success" as a first-class primitive. The killer
// first-session question these answer: "how many signups did I get, at what rate,
// and which channel sent them?"

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/goal"
)

func (s *Server) SetGoals(g *goal.Store) { s.goals = g }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_goal",
			"description": "Define a named conversion goal: an event name (kind=event, e.g. signup) or a pageview path glob (kind=path, e.g. /thanks*). Goals power goal_report — conversions, rate, and which channel sent the converters.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "Label, e.g. 'Signed up'"},
					"kind":  map[string]any{"type": "string", "enum": []string{"event", "path"}},
					"value": map[string]any{"type": "string", "description": "Event name, or path glob like /thanks*"},
				},
				"required": []string{"name", "kind", "value"},
			},
		},
		map[string]any{
			"name":        "list_goals",
			"description": "List defined goals.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "delete_goal",
			"description": "Delete a goal by id (get ids from list_goals).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
		map[string]any{
			"name":        "goal_report",
			"description": "Resolve a goal over the trailing period: unique conversions, conversion rate vs unique visitors, and first-touch attribution (by referrer host and utm_source) for WHO converted. Answers 'signups per channel'. Pass the goal's name or id; days defaults to 30.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal": map[string]any{"type": "string", "description": "Goal name or id"},
					"days": map[string]any{"type": "integer"},
				},
				"required": []string{"goal"},
			},
		},
	)
}

func (s *Server) callGoals(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "create_goal":
		if s.goals == nil {
			return true, "", fmt.Errorf(noStore, "goal")
		}
		var p struct{ Name, Kind, Value string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Kind == "event" {
			if err := s.knownEvent(p.Value); err != nil {
				return true, "", err
			}
		}
		d, err := s.goals.Save(goal.Definition{Name: p.Name, Kind: p.Kind, Value: p.Value})
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"created": d, "note": "resolve it any time with goal_report"}), nil

	case "list_goals":
		if s.goals == nil {
			return true, "", fmt.Errorf(noStore, "goal")
		}
		return true, jsonStr(map[string]any{"goals": s.goals.List()}), nil

	case "delete_goal":
		return s.deleteByID(args, "goal", func(id string) error { return s.goals.Delete(id) }, s.goals == nil)

	case "goal_report":
		if s.goals == nil {
			return true, "", fmt.Errorf(noStore, "goal")
		}
		var p struct {
			Goal string `json:"goal"`
			Days int    `json:"days"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		var def *goal.Definition
		var names []string
		for _, d := range s.goals.List() {
			names = append(names, d.Name)
			if d.ID == p.Goal || strings.EqualFold(d.Name, p.Goal) {
				dd := d
				def = &dd
			}
		}
		if def == nil {
			if len(names) == 0 {
				return true, "", fmt.Errorf("no goals defined yet — create one with create_goal")
			}
			return true, "", fmt.Errorf("unknown goal %q — defined goals: %s", p.Goal, strings.Join(names, ", "))
		}
		evs, err := s.all()
		if err != nil {
			return true, "", err
		}
		evs = applyDefaultScope(evs)
		return true, jsonStr(goal.Resolve(evs, *def, p.Days, time.Time{})), nil
	}
	return false, "", nil
}
