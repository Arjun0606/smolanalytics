package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// These endpoints back the interactive Explore panel: run any report on any of the
// user's own events, not just the demo funnel. The engine already takes arbitrary
// event names — this just exposes it over REST (the MCP tools do the same for AI).

// GET /v1/meta — the event names available, so the UI can offer them.
func (s *Server) apiMeta(w http.ResponseWriter, _ *http.Request) {
	names, err := s.store.Names()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": names})
}

// GET /v1/trends?event=signup&unique=true
func (s *Server) apiTrends(w http.ResponseWriter, r *http.Request) {
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	unique := r.URL.Query().Get("unique") == "true"
	writeJSON(w, http.StatusOK, trends.Compute(evs, r.URL.Query().Get("event"), time.Time{}, time.Time{}, unique))
}

// GET /v1/breakdown?event=signup&property=source
func (s *Server) apiBreakdown(w http.ResponseWriter, r *http.Request) {
	property := r.URL.Query().Get("property")
	if property == "" {
		writeErr(w, http.StatusBadRequest, "property is required")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	eventName := r.URL.Query().Get("event")
	filtered := evs[:0:0]
	for _, e := range evs {
		if eventName == "" || e.Name == eventName {
			filtered = append(filtered, e)
		}
	}
	groups := query.Breakdown(filtered, property)
	rows := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, map[string]any{"value": g.Value, "count": g.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventName, "property": property, "groups": rows})
}

// GET /v1/retention?event=open&days=7
func (s *Server) apiRetention(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	if days > 90 {
		days = 90
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, retention.Compute(evs, days, r.URL.Query().Get("event")))
}
