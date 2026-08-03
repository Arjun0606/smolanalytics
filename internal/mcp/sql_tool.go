package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sqlq "github.com/Arjun0606/smolanalytics/internal/sql"
)

// run_sql is the escape hatch. Every other tool here answers one shape of question that was
// designed in advance; this answers the rest, which is the difference between "we support the
// twenty reports we built" and "ask anything".
//
// It streams rather than loading events, so it is dispatched before callTool's shared load.
func (s *Server) toolRunSQL(args json.RawMessage) (string, error) {
	var a struct {
		Query string  `json:"query"`
		Limit float64 `json:"limit"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("query is required.\n\n%s", sqlq.Grammar())
	}

	q, err := sqlq.Parse(a.Query)
	if err != nil {
		// The grammar rides along with every parse failure. A model that guessed wrong gets the
		// exact supported surface back in the same turn and can fix it, instead of guessing
		// again against a dialect it cannot see.
		return "", fmt.Errorf("%w\n\n%s", err, sqlq.Grammar())
	}

	lim := sqlq.DefaultLimits()
	if a.Limit > 0 && int(a.Limit) < lim.MaxRows {
		lim.MaxRows = int(a.Limit)
	}

	res, err := sqlq.Run(q, s.store, lim)
	if err != nil {
		return "", err
	}

	// Rows go back as objects rather than positional arrays. A model reading
	// {"country":"US","n":412} cannot misalign a column the way it can with ["US",412], and the
	// cost is a few bytes per row.
	rows := make([]map[string]any, 0, len(res.Rows))
	for _, r := range res.Rows {
		m := make(map[string]any, len(res.Columns))
		for i, c := range res.Columns {
			if i < len(r) {
				m[c] = r[i]
			}
		}
		rows = append(rows, m)
	}

	out := map[string]any{
		"columns":        res.Columns,
		"rows":           rows,
		"row_count":      len(rows),
		"events_scanned": res.Scanned,
		"elapsed":        res.Elapsed,
	}
	if res.Groups > 0 {
		out["groups"] = res.Groups
	}
	if res.Truncated {
		// Never let a capped result read as the complete answer. A model that reports "the top
		// countries are..." from a silently truncated list has stated something false.
		out["truncated"] = true
		out["note"] = fmt.Sprintf("Showing the first %d rows; there are more. Add LIMIT, or narrow with WHERE, to see a complete answer.", len(rows))
	}
	if len(rows) == 0 {
		out["note"] = "No rows matched. Check event and property names with list_events — property keys are case-sensitive."
	}
	return jsonText(out)
}

// sqlToolDef is appended to toolList. The description carries the full grammar because the
// dialect is a subset: an agent that writes valid ANSI SQL and gets "unsupported" back has a
// worse time than one told the exact surface up front.
var sqlToolDef = map[string]any{
	"name": "run_sql",
	"description": "Answer a question no other tool covers, by querying the raw event stream with read-only SQL. " +
		"Reach for this when the question does not fit funnel/retention/trends/breakdown/paths — arbitrary grouping, " +
		"multi-property segmentation, custom arithmetic, or an ad-hoc cut nobody built a report for. " +
		"Prefer the purpose-built tools when one fits: they encode the correct definitions (a funnel's ordering and " +
		"window, retention's cohort rule) that hand-written SQL would have to reproduce. " +
		"Results are computed by scanning events, so they are exact rather than estimated, and identical on every run. " +
		"Only SELECT parses — there is no way to write data through this tool.\n\n" + sqlqGrammar(),
	"inputSchema": obj(map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "One read-only SELECT statement over the `events` table. See the grammar in this tool's description.",
		},
		"limit": map[string]any{
			"type":        "number",
			"description": "Optional cap on rows returned (default 1000, which is also the maximum).",
		},
	}, []string{"query"}),
}

func sqlqGrammar() string { return sqlq.Grammar() }
