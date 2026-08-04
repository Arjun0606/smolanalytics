package mcp

import (
	"encoding/json"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/instrument"
)

// instrumentation_coverage names what the product DOES that nothing measures.
//
// Every analytics tool can describe the events it receives. None can name the ones nobody ever
// wrote, because absence is invisible in event data — a checkout nobody instrumented looks
// exactly like a checkout nobody performed. Answering needs the source, so this is not a feature
// a hosted vendor can ship.
//
// It is also the direct answer to the incumbent's own published cost of instrumentation: Mixpanel
// says roughly 30 minutes per event, 10-30 engineer hours for a 20-60 event product, and most
// customers finishing around day 40. Knowing WHAT to track is most of that time.
func (s *Server) toolCoverage(args json.RawMessage) (string, error) {
	var a struct {
		RepoPath string `json:"repo_path"`
		Limit    int    `json:"limit"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.RepoPath == "" {
		a.RepoPath = "."
	}
	if a.Limit <= 0 || a.Limit > 200 {
		a.Limit = 60
	}

	rep := instrument.Report(a.RepoPath)

	// Uncovered actions sort first, so truncation drops the already-instrumented tail rather than
	// the list someone is going to act on.
	shown := rep.Actions
	truncated := false
	if len(shown) > a.Limit {
		shown = shown[:a.Limit]
		truncated = true
	}

	out := map[string]any{
		"repo":         a.RepoPath,
		"actions":      shown,
		"total":        len(rep.Actions),
		"covered":      rep.Covered,
		"uncovered":    rep.Uncovered,
		"coverage_pct": rep.Percent,
		"headline":     rep.Headline,
	}
	if truncated {
		out["truncated"] = true
		out["note"] = "Showing the least-covered actions first; raise limit to see the rest."
	}
	if rep.Uncovered > 0 {
		out["next"] = "Pick the ones that matter to the business, then call propose_instrumentation to get the exact edits, apply them, and confirm with verify_instrumentation."
	}
	return jsonText(out)
}

var coverageToolDef = map[string]any{
	"name": "instrumentation_coverage",
	"description": "List what this product DOES that nothing is measuring: form submissions, auth flows, payments and mutating API handlers found in the code, each marked as having tracking nearby or not. " +
		"No events-only tool can answer this — an action nobody instrumented looks identical to one nobody performed, so what is missing is invisible in the data. " +
		"Call this when setting up analytics for the first time, when someone asks what should we be tracking, or when a report cannot answer a question because the event does not exist. " +
		"Deliberately conservative: it reports actions almost always worth an event rather than every click, so the list stays short enough to act on. " +
		strings.TrimSpace("Pair with propose_instrumentation to generate the edits, then verify_instrumentation to confirm the events arrive. Requires the repo on this machine."),
	"inputSchema": obj(map[string]any{
		"repo_path": map[string]any{"type": "string", "description": "Path to the repo root on this machine (default: current directory)"},
		"limit":     map[string]any{"type": "integer", "description": "Maximum actions to return, least-covered first (default 60)"},
	}, nil),
}
