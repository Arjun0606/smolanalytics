package mcp

// Feature-flag tools — create and flip flags, and evaluate one for a user, from your editor.
// Boolean or multivariate, with property targeting + percentage rollout, evaluated
// deterministically (flag.Evaluate) so the SDK and the agent always agree on a user's bucket.

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/flag"
)

func (s *Server) SetFlags(f *flag.Store) { s.flags = f }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_flag",
			"description": "Create or update a feature flag. Boolean (no variants) or multivariate (variants [{key,weight}]). Optional rollout_pct (0..100) serves it to that share of users. Set measured:true to log exposures so it can be A/B-analysed. Saving an existing key updates it in place.",
			"inputSchema": obj(map[string]any{
				"key":         map[string]any{"type": "string", "description": "stable key, e.g. 'checkout_v2'"},
				"description": map[string]any{"type": "string"},
				"enabled":     map[string]any{"type": "boolean", "description": "on/off; defaults to true"},
				"variants":    map[string]any{"type": "array", "description": "multivariate arms [{\"key\":\"a\",\"weight\":50},...]; omit for a boolean flag", "items": map[string]any{"type": "object"}},
				"rollout_pct": map[string]any{"type": "integer", "description": "0..100; serve to this percentage of users (a single no-filter rule)"},
				"measured":    map[string]any{"type": "boolean", "description": "log $feature_flag_called exposures for A/B analysis"},
			}, []string{"key"}),
		},
		map[string]any{
			"name":        "list_flags",
			"description": "List all feature flags with their state (enabled, variants, rules, measured).",
			"inputSchema": obj(nil, nil),
		},
		map[string]any{
			"name":        "set_flag_enabled",
			"description": "Turn a feature flag on or off by key.",
			"inputSchema": obj(map[string]any{
				"key":     map[string]any{"type": "string"},
				"enabled": map[string]any{"type": "boolean"},
			}, []string{"key", "enabled"}),
		},
		map[string]any{
			"name":        "delete_flag",
			"description": "Delete a feature flag by key.",
			"inputSchema": obj(map[string]any{"key": map[string]any{"type": "string"}}, []string{"key"}),
		},
		map[string]any{
			"name":        "evaluate_flag",
			"description": "Evaluate a flag for one distinct_id (with optional context properties for targeting). Returns the served variant and whether it's on — the exact deterministic result the SDK computes, so you can debug 'why is user X in variant B?' from your editor.",
			"inputSchema": obj(map[string]any{
				"key":         map[string]any{"type": "string"},
				"distinct_id": map[string]any{"type": "string"},
				"context":     map[string]any{"type": "object", "description": "user properties the targeting rules match on"},
			}, []string{"key", "distinct_id"}),
		},
	)
}

func (s *Server) callFlags(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "create_flag":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key         string         `json:"key"`
			Description string         `json:"description"`
			Enabled     *bool          `json:"enabled"`
			Variants    []flag.Variant `json:"variants"`
			RolloutPct  *int           `json:"rollout_pct"`
			Measured    bool           `json:"measured"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		f := flag.Flag{Key: p.Key, Description: p.Description, Enabled: true, Variants: p.Variants, Measured: p.Measured}
		if p.Enabled != nil {
			f.Enabled = *p.Enabled
		}
		if p.RolloutPct != nil {
			f.Rules = []flag.Rule{{RolloutPct: *p.RolloutPct}}
		}
		saved, err := s.flags.Save(f)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"flag": saved}), nil

	case "list_flags":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		return true, jsonStr(map[string]any{"flags": s.flags.List()}), nil

	case "set_flag_enabled":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key     string `json:"key"`
			Enabled bool   `json:"enabled"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		f, err := s.flags.SetEnabled(p.Key, p.Enabled)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"flag": f}), nil

	case "delete_flag":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key string `json:"key"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Key == "" {
			return true, "", fmt.Errorf("flag key is required")
		}
		if err := s.flags.Delete(p.Key); err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{"deleted": p.Key}), nil

	case "evaluate_flag":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key        string         `json:"key"`
			DistinctID string         `json:"distinct_id"`
			Context    map[string]any `json:"context"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		f, ok := s.flags.Get(p.Key)
		if !ok {
			return true, "", fmt.Errorf("flag %q not found", p.Key)
		}
		variant, on := f.Evaluate(p.DistinctID, p.Context)
		return true, jsonStr(map[string]any{"key": p.Key, "distinct_id": p.DistinctID, "on": on, "variant": variant}), nil
	}
	return false, "", nil
}
