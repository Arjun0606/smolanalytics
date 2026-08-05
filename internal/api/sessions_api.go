package api

import (
	"net/http"
	"strconv"

	"github.com/Arjun0606/smolanalytics/internal/session"
)

// GET /v1/sessions?days=7&limit=100&filters=... — recent sessions (a visit summary per row),
// reconstructed from the event log. GET /v1/session?distinct_id=&start=<unix> — one session's
// ordered playback steps. Both are query-time reports (pinned MCP==API), not a stored recording.

func (s *Server) apiSessions(w http.ResponseWriter, r *http.Request) {
	// days/limit used to be parsed with `err == nil && v > 0`, which reads a typo'd or negative
	// value as ABSENT: /v1/sessions?days=abc answered over the default 7 days with a 200 and no
	// sign that the window was not the one asked for. posIntParam 400s instead, matching
	// /v1/trends, and from/to/hours are refused by name because this report windows by whole
	// days only (the dashboard panel already strips them — see dashboard.tmpl.html's SA_FILTERS).
	days, derr := posIntParam(r, "days", 7, 0)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	limit, derr := posIntParam(r, "limit", 100, 0)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	if derr := rejectWindowParams(r, "the session list windows by whole days (days=N)", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": session.Sessions(evs, days, limit)})
}

func (s *Server) apiSession(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("distinct_id")
	start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	if did == "" || start == 0 {
		writeErr(w, http.StatusBadRequest, "distinct_id and start are required (from a /v1/sessions row)")
		return
	}
	// s.filtered, the SAME scope the list above is built from. This read the RAW store, so the
	// row and the detail behind it were reconstructed from two different event sets: one
	// dev-env pageview inside a visit moves that visit's boundaries under scoping, so the
	// `start` the list had just emitted did not exist in the unscoped view and clicking the row
	// returned 404. When it did resolve, the detail's summary disagreed with the row that
	// linked to it. A "one question, one answer" product cannot 404 on its own link.
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	d, ok := session.One(evs, did, start)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}
