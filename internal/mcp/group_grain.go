package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/groups"
)

// group_by for the MCP tools, matching GET /v1/funnel?group_by= and /v1/retention?group_by= —
// same helper shape on both sides so the two surfaces cannot answer "how many accounts converted"
// differently. That agreement is the whole reason a model is allowed to quote these numbers.

// regroup re-keys events to account grain, or returns them untouched when group_by is empty.
//
// An uninstrumented property is an ERROR, not an empty report. A model handed "0 of 0 accounts
// converted" will faithfully relay it, and the person reading will believe their product has no
// customers — the failure mode is worse over MCP than over HTTP, because there is no chart to
// look wrong, only a confident sentence.
func regroup(evs []event.Event, property string) ([]event.Event, *groups.Grain, error) {
	if property == "" {
		return evs, nil, nil
	}
	regrouped, g := groups.Regroup(evs, property)
	if g.Kept == 0 {
		return nil, nil, fmt.Errorf(
			"no event carries %q, so there is no account-level view to compute. Set it once on signup "+
				"or on an identify call and it will follow each user through the rest of their events. "+
				"Call groups to see which group properties this instance actually has.", property)
	}
	return regrouped, &g, nil
}

// withGrain attaches the grain to a report without the report type knowing groups exist. When no
// group_by was asked for, the report's own bytes go back unchanged, so every agreement test that
// locks MCP output against HTTP output keeps passing untouched.
func withGrain(v any, g *groups.Grain) any {
	if g == nil {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	m := map[string]any{}
	if json.Unmarshal(b, &m) != nil {
		return v
	}
	// Every "users" in the report below now means an ACCOUNT. Stating the unit is not decoration:
	// a model reading this JSON has to know that 12 is twelve companies, or it will write
	// "12 users converted" and be wrong in the exact way this feature exists to prevent.
	m["unit"] = "accounts"
	m["group_grain"] = g
	return m
}
