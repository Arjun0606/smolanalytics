package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/session"
)

// GET /v1/sessions?days=7&limit=100&filters=... — recent sessions (a visit summary per row),
// reconstructed from the event log. GET /v1/session?distinct_id=&start=<unix> — one session's
// ordered playback steps. Both are query-time reports (pinned MCP==API), not a stored recording.

func (s *Server) apiSessions(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
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
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d, ok := session.One(evs, did, start)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, d)
}
