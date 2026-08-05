package mcp

// Feature-flag tools — create and flip flags, and evaluate one for a user, from your editor.
// Boolean or multivariate, with property targeting + percentage rollout, evaluated
// deterministically (flag.Evaluate) so the SDK and the agent always agree on a user's bucket.

import (
	"encoding/json"
	"fmt"
	"time"

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
			"description": "Delete a feature flag by key. Returns deleted:true only if a flag with that key existed; deleted:false means nothing was removed (check the key with list_flags before telling anyone the flag is gone).",
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
		map[string]any{
			"name":        "flag_impact",
			"description": "A/B read for a measured flag: for each variant, the conversion rate on a goal event among users exposed to that variant (counted only after their first exposure), the lift vs the control arm, and 95% two-proportion significance. Computed from your events, never guessed. Correlation, not proof.",
			"inputSchema": obj(map[string]any{
				"key":   map[string]any{"type": "string", "description": "the flag key (must be a measured flag)"},
				"event": map[string]any{"type": "string", "description": "the goal/conversion event, e.g. 'purchase'"},
				"days":  map[string]any{"type": "integer", "description": "window in days, default 30"},
			}, []string{"key", "event"}),
		},
		map[string]any{
			"name": "experiment_health",
			"description": "Check whether an experiment's traffic actually split the way it was configured, BEFORE trusting any result from it. " +
				"Runs a chi-square sample-ratio-mismatch test at p<0.001 comparing exposures per variant against the flag's weights, and when it fails, names the segment most responsible. " +
				"A mismatch means the randomization broke, so the arms differ by whatever broke it rather than by the change being tested, and the conversion numbers cannot be read. " +
				"Call this whenever you are about to report or act on an A/B result, and any time a result looks surprisingly large.",
			"inputSchema": obj(map[string]any{
				"key":  map[string]any{"type": "string", "description": "the flag key"},
				"days": map[string]any{"type": "integer", "description": "window in days, default 30 (0 = all time)"},
			}, []string{"key"}),
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
		found, err := s.flags.Delete(p.Key)
		if err != nil {
			return true, "", err
		}
		rm := removal{kind: "flag", field: "key", list: "list_flags"}
		return true, jsonStr(rm.result(found, p.Key)), nil

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

	case "flag_impact":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key   string `json:"key"`
			Event string `json:"event"`
			Days  int    `json:"days"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Event == "" {
			return true, "", fmt.Errorf("event (the goal metric) is required")
		}
		if p.Days == 0 {
			p.Days = 30
		}
		evs, err := s.all()
		if err != nil {
			return true, "", err
		}
		evs = applyDefaultScope(evs)
		// same resolution as GET /v1/flags/{key}/measure — the agreement test pins them equal
		f, _ := s.flags.Get(p.Key)
		to := time.Now().UTC()
		rep := flag.MeasureRange(evs, f, p.Event, to.AddDate(0, 0, -p.Days), to)
		rep.Days = p.Days
		return true, jsonStr(rep), nil
	case "experiment_health":
		if s.flags == nil {
			return true, "", fmt.Errorf(noStore, "flag")
		}
		var p struct {
			Key string `json:"key"`
			// Pointer, not int, because the tool documents "0 = all time" and a plain int cannot
			// tell an omitted key from an explicit 0 — the old `if p.Days == 0 { p.Days = 30 }`
			// silently rewrote the documented all-time request into a 30-day one.
			Days *int `json:"days"`
		}
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		if p.Key == "" {
			return true, "", fmt.Errorf("key (the flag to check) is required")
		}
		f, ok := s.flags.Get(p.Key)
		if !ok {
			return true, "", fmt.Errorf("no flag named %q", p.Key)
		}
		days := 30
		if p.Days != nil {
			days = *p.Days // including an explicit 0, which CheckSRM reads as all history
		}
		evs, err := s.all()
		if err != nil {
			return true, "", err
		}
		// Deliberately NOT default-scoped. The split check counts who was actually bucketed, and
		// filtering the population before checking it is how a real mismatch gets hidden behind
		// the filter that caused it.
		return true, jsonStr(flag.CheckSRM(evs, f, days)), nil
	}
	return false, "", nil
}
