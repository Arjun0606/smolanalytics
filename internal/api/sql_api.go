package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/query"
	sqlq "github.com/Arjun0606/smolanalytics/internal/sql"
)

// GET /v1/sql?q=SELECT ... — the query engine, over HTTP.
//
// The engine, the grammar, the limits and the production scoping have existed for a while and were
// reachable from exactly one place: the run_sql MCP tool. So the deepest thing this product can do
// was available only to someone already talking to it through an agent, and completely absent from
// the dashboard — the same "built but unreachable" gap that hid experiments, provenance, people
// and accounts.
//
// This is deliberately the SAME code path as the MCP tool, including the scoping rule, because the
// one thing an escape hatch may never do is disagree with the reports beside it. A dashboard query
// returning 4 while the trends card says 2 would discredit both.
func (s *Server) apiSQL(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		// No query is not an error worth a bare 400: the grammar IS the documentation, and handing
		// it back turns an empty request into a usable one.
		writeJSON(w, http.StatusOK, map[string]any{
			"grammar": sqlq.Grammar(),
			"read":    "pass ?q=<SELECT …>. The grammar above is the whole supported surface.",
		})
		return
	}
	parsed, err := sqlq.Parse(q)
	if err != nil {
		// The grammar rides along with every parse failure, exactly as it does over MCP. Someone
		// who guessed wrong gets the supported surface back in the same response rather than
		// guessing again against a dialect they cannot see.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   err.Error(),
			"grammar": sqlq.Grammar(),
		})
		return
	}

	lim := sqlq.DefaultLimits()
	if v := r.URL.Query().Get("limit"); v != "" {
		n, cerr := strconv.Atoi(v)
		if cerr != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "limit must be a positive whole number of rows")
			return
		}
		if n < lim.MaxRows {
			lim.MaxRows = n
		}
	}

	// Production scoping, and the opt-back-out, mirror the MCP tool exactly: a WHERE that names
	// env asked for that env on purpose, so the default scope steps aside instead of ANDing with
	// it and returning a flat zero.
	var sc sqlq.Scanner = s.store
	scoped := !sqlq.ExprTouchesEnv(parsed.Where)
	if scoped {
		sc = sqlq.ScopedScanner{Inner: s.store, Keep: query.Keeper(nil)}
	}
	res, rerr := sqlq.Run(parsed, sc, lim)
	if rerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   rerr.Error(),
			"grammar": sqlq.Grammar(),
		})
		return
	}

	out := map[string]any{
		"columns": res.Columns, "rows": res.Rows, "scanned": res.Scanned,
		"elapsed": res.Elapsed, "scoped_to_production": scoped,
	}
	if res.Groups > 0 {
		out["groups"] = res.Groups
	}
	// Truncation must be stated, never inferred from a round row count. "Exactly 1000 rows" and
	// "the first 1000 of many" are different answers, and a reader cannot tell them apart.
	if res.Truncated {
		out["truncated"] = true
		out["note"] = "showing the first " + strconv.Itoa(len(res.Rows)) +
			" rows; there are more. Add LIMIT, or narrow the WHERE."
	}
	if scoped {
		out["read"] = "counts cover PRODUCTION traffic, the same scope every other report uses. " +
			"Name env in a WHERE to query another environment."
	}
	writeJSON(w, http.StatusOK, out)
}
