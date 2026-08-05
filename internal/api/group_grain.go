package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/groups"
)

// ?group_by=company turns any report from a question about people into a question about accounts.
//
// Deliberately one parameter rather than a parallel set of /v1/group_funnel endpoints. The report
// is the same report — same engine, same options, same filters — and only the unit of analysis
// moves. Two endpoints would be two places for the definition of "converted" to drift, and the
// drift would show up as a B2B customer's account funnel disagreeing with their user funnel for a
// reason nobody could find.

// groupGrain reads ?group_by= and re-keys. Returns the events to report over, and the grain to
// attach to the response (nil when the caller did not ask for account grain).
//
// Validation here is MEASURED rather than looked up, and that is the stronger check: a typo
// (`group_by=comapny`) and a genuinely uninstrumented property fail identically because they are
// the same failure — no event carries it — and the error says the one thing that fixes both.
// Letting it through would render a structurally valid, completely meaningless funnel, and
// "0 of 0 accounts converted" is the most alarming thing this product could show someone for a
// reason that is entirely our fault.
func (s *Server) groupGrain(w http.ResponseWriter, r *http.Request, evs []event.Event) ([]event.Event, *groups.Grain, bool) {
	prop := r.URL.Query().Get("group_by")
	if prop == "" {
		return evs, nil, true
	}
	regrouped, g := groups.Regroup(evs, prop)
	if g.Kept == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf(
			"no event carries %q, so there is no account-level view to compute. Set it once on signup "+
				"or on an identify call and it will follow each user through the rest of their events.", prop))
		return nil, nil, false
	}
	return regrouped, &g, true
}

// withGrain attaches the grain to a report without any report type knowing groups exist.
//
// The round-trip through JSON is the point: when no group_by was asked for, the response is the
// report's own bytes, unchanged, so every agreement test that locks HTTP output against MCP output
// keeps passing untouched. The envelope only appears when someone opted into account grain.
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
	// The count words change meaning under this envelope — every "users" in the report below is an
	// ACCOUNT — so the response says which unit it counted rather than leaving a reader to infer
	// it from the presence of a key.
	m["unit"] = "accounts"
	m["group_grain"] = g
	return m
}
