package mcp

// Survey tools — create in-product micro-surveys (NPS, rating, choice, text), toggle them, and
// read results, all from your editor. Responses arrive as events, so results are the same
// deterministic report the dashboard renders.

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/survey"
)

func (s *Server) SetSurveys(sv *survey.Store) { s.surveys = sv }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_survey",
			"description": "Create or update an in-product micro-survey. One question of type nps (0-10), rating (1-5), choice (needs choices), or text. Optional url_match (show only on paths containing it), sample_pct (0-100), and active (default true). Saving with an id updates it.",
			"inputSchema": obj(map[string]any{
				"id":         map[string]any{"type": "string", "description": "omit to create; pass to update"},
				"name":       map[string]any{"type": "string"},
				"type":       map[string]any{"type": "string", "description": "nps | rating | choice | text"},
				"question":   map[string]any{"type": "string"},
				"choices":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "for type=choice"},
				"url_match":  map[string]any{"type": "string", "description": "only show on paths containing this substring"},
				"sample_pct": map[string]any{"type": "integer", "description": "0..100 of eligible users; 0/100 = everyone"},
				"active":     map[string]any{"type": "boolean", "description": "default true"},
			}, []string{"name", "type", "question"}),
		},
		map[string]any{
			"name":        "list_surveys",
			"description": "List all surveys with their type, question, targeting, and active state.",
			"inputSchema": obj(nil, nil),
		},
		map[string]any{
			"name":        "set_survey_active",
			"description": "Turn a survey on or off by id.",
			"inputSchema": obj(map[string]any{"id": map[string]any{"type": "string"}, "active": map[string]any{"type": "boolean"}}, []string{"id", "active"}),
		},
		map[string]any{
			"name":        "delete_survey",
			"description": "Delete a survey by id.",
			"inputSchema": obj(map[string]any{"id": map[string]any{"type": "string"}}, []string{"id"}),
		},
		map[string]any{
			"name":        "survey_results",
			"description": "Results for a survey: how many saw it vs answered (response rate), plus the aggregate — an NPS score, an average rating, choice counts, or recent text answers, depending on type. Computed from your events, never guessed.",
			"inputSchema": obj(map[string]any{
				"id":   map[string]any{"type": "string"},
				"days": map[string]any{"type": "integer", "description": "window in days, default 30"},
			}, []string{"id"}),
		},
	)
}

func (s *Server) callSurveys(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "create_survey":
		if s.surveys == nil {
			return true, "", fmt.Errorf(noStore, "survey")
		}
		var p struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Type      string   `json:"type"`
			Question  string   `json:"question"`
			Choices   []string `json:"choices"`
			URLMatch  string   `json:"url_match"`
			SamplePct int      `json:"sample_pct"`
			Active    *bool    `json:"active"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		sv := survey.Survey{ID: p.ID, Name: p.Name, Type: p.Type, Question: p.Question, Choices: p.Choices, URLMatch: p.URLMatch, SamplePct: p.SamplePct, Active: true}
		if p.Active != nil {
			sv.Active = *p.Active
		}
		saved, err := s.surveys.Save(sv)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"survey": saved}), nil

	case "list_surveys":
		if s.surveys == nil {
			return true, "", fmt.Errorf(noStore, "survey")
		}
		return true, jsonStr(map[string]any{"surveys": s.surveys.List()}), nil

	case "set_survey_active":
		if s.surveys == nil {
			return true, "", fmt.Errorf(noStore, "survey")
		}
		var p struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		sv, err := s.surveys.SetActive(p.ID, p.Active)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"survey": sv}), nil

	case "delete_survey":
		return s.deleteByID(args, "survey", func(id string) error { return s.surveys.Delete(id) }, s.surveys == nil)

	case "survey_results":
		if s.surveys == nil {
			return true, "", fmt.Errorf(noStore, "survey")
		}
		var p struct {
			ID   string `json:"id"`
			Days int    `json:"days"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		sv, ok := s.surveys.Get(p.ID)
		if !ok {
			return true, "", fmt.Errorf("survey %q not found", p.ID)
		}
		if p.Days == 0 {
			p.Days = 30
		}
		evs, err := s.all()
		if err != nil {
			return true, "", err
		}
		evs = applyDefaultScope(evs)
		return true, jsonStr(survey.Results(evs, p.ID, sv.Type, p.Days)), nil
	}
	return false, "", nil
}
