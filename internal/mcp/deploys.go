package mcp

// Deploy tools — the wedge, made agent-native. From your editor: record what you shipped,
// then ask "did it move the metric?" and get the deterministic before/after — the SAME
// numbers the dashboard shows (deploy_impact and GET /v1/deploys?event= both call
// deploys.Report, and the agreement test pins them together).

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
)

func (s *Server) SetDeploys(d *deploys.Store) { s.deploys = d }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "record_deploy",
			"description": "Record a deploy marker — what you just shipped (a message like 'new checkout', an optional git sha/author, optional when). Then use deploy_impact to see if it moved a metric.",
			"inputSchema": obj(map[string]any{
				"message": map[string]any{"type": "string", "description": "what shipped, e.g. 'tightened address validation'"},
				"sha":     map[string]any{"type": "string", "description": "git commit sha (optional; recording the same sha again updates it)"},
				"author":  map[string]any{"type": "string"},
				"when":    map[string]any{"type": "string", "description": "RFC3339 or YYYY-MM-DD; defaults to now"},
			}, []string{"message"}),
		},
		map[string]any{
			"name":        "list_deploys",
			"description": "List recorded deploy markers, newest first.",
			"inputSchema": obj(nil, nil),
		},
		map[string]any{
			"name":        "delete_deploy",
			"description": "Delete a deploy marker by id (get ids from list_deploys).",
			"inputSchema": obj(map[string]any{"id": map[string]any{"type": "string"}}, []string{"id"}),
		},
		map[string]any{
			"name":        "deploy_impact",
			"description": "Which deploy moved the metric? For each recorded deploy, compares the event's mean daily count in the N days after vs before — computed from the same reports the dashboard renders (never guessed). Leads with any deploy that correlates with a real regression. Correlation, not proof.",
			"inputSchema": obj(map[string]any{
				"event":  map[string]any{"type": "string", "description": "the metric to check, e.g. 'signup' or '$pageview' (default $pageview)"},
				"days":   map[string]any{"type": "integer", "description": "series length to consider, default 30"},
				"window": map[string]any{"type": "integer", "description": "days compared each side of a deploy, default 3"},
			}, nil),
		},
	)
}

func (s *Server) callDeploys(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "record_deploy":
		if s.deploys == nil {
			return true, "", fmt.Errorf(noStore, "deploy")
		}
		var p struct{ Message, SHA, Author, When string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Message == "" && p.SHA == "" {
			return true, "", fmt.Errorf("a deploy needs a message or a sha")
		}
		d := deploys.Deploy{Message: p.Message, SHA: p.SHA, Author: p.Author, Source: "manual"}
		if p.When != "" {
			t, err := parseWhen(p.When)
			if err != nil {
				return true, "", fmt.Errorf("bad when %q (want RFC3339 or YYYY-MM-DD)", p.When)
			}
			d.At = t
		}
		saved, err := s.deploys.Record(d)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"recorded": saved, "note": "see its effect with deploy_impact"}), nil

	case "list_deploys":
		if s.deploys == nil {
			return true, "", fmt.Errorf(noStore, "deploy")
		}
		return true, jsonStr(map[string]any{"deploys": s.deploys.List()}), nil

	case "delete_deploy":
		return s.deleteByID(args, "deploy", func(id string) error { return s.deploys.Delete(id) }, s.deploys == nil)

	case "deploy_impact":
		if s.deploys == nil {
			return true, "", fmt.Errorf(noStore, "deploy")
		}
		var p struct {
			Event  string `json:"event"`
			Days   int    `json:"days"`
			Window int    `json:"window"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Event == "" {
			p.Event = "$pageview"
		}
		if err := s.checkEvents(p.Event); err != nil {
			return true, "", err
		}
		evs, err := s.all()
		if err != nil {
			return true, "", err
		}
		evs = applyDefaultScope(evs)
		return true, jsonStr(deploys.Report(evs, s.deploys.List(), p.Event, p.Days, p.Window)), nil
	}
	return false, "", nil
}
