package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// filtersFrom parses ?filters=<url-encoded JSON array> into predicates, so any
// report can be segmented (e.g. plan=pro AND country=US). Bad JSON = no filters.
func filtersFrom(r *http.Request) []query.Filter {
	raw := r.URL.Query().Get("filters")
	if raw == "" {
		return nil
	}
	var fs []query.Filter
	_ = json.Unmarshal([]byte(raw), &fs)
	return fs
}

// filtered loads all events, applies the request's property filters, and (if
// ?cohort=<id> is set) scopes to that cohort's members. Cohort membership is
// resolved over the full history, then the filtered events are kept for those users.
func (s *Server) filtered(r *http.Request) ([]event.Event, error) {
	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	evs := query.Apply(all, filtersFrom(r))
	if cid := r.URL.Query().Get("cohort"); cid != "" && s.cohorts != nil {
		if d, ok := s.cohorts.Get(cid); ok {
			evs = cohort.FilterToUsers(evs, cohort.Resolve(all, d))
		}
	}
	return evs, nil
}

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

// GET /v1/trends?event=signup&unique=true&breakdown=source&filters=...
// With breakdown set, returns one series per property value (multi-line trend).
func (s *Server) apiTrends(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	unique := q.Get("unique") == "true"
	event := q.Get("event")
	if bd := q.Get("breakdown"); bd != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"event": event, "breakdown": bd,
			"series": trends.ComputeBreakdown(evs, event, bd, time.Time{}, time.Time{}, unique),
		})
		return
	}
	writeJSON(w, http.StatusOK, trends.Compute(evs, event, time.Time{}, time.Time{}, unique))
}

// GET /v1/breakdown?event=signup&property=source&filters=...
func (s *Server) apiBreakdown(w http.ResponseWriter, r *http.Request) {
	property := r.URL.Query().Get("property")
	if property == "" {
		writeErr(w, http.StatusBadRequest, "property is required")
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	eventName := r.URL.Query().Get("event")
	scoped := evs[:0:0]
	for _, e := range evs {
		if eventName == "" || e.Name == eventName {
			scoped = append(scoped, e)
		}
	}
	groups := query.Breakdown(scoped, property)
	rows := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		rows = append(rows, map[string]any{"value": g.Value, "count": g.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventName, "property": property, "groups": rows})
}

// GET /v1/retention?event=open&days=7&filters=...
func (s *Server) apiRetention(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	if days > 90 {
		days = 90
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, retention.Compute(evs, days, r.URL.Query().Get("event")))
}

// GET /v1/lifecycle?days=30&filters=... — new/returning/resurrected/dormant per day
func (s *Server) apiLifecycle(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	if days > 180 {
		days = 180
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": engagement.ComputeLifecycle(evs, days)})
}

// GET /v1/stickiness?filters=... — DAU/WAU/MAU + ratio
func (s *Server) apiStickiness(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, engagement.ComputeStickiness(evs, time.Time{}))
}

// GET /v1/paths?start=signup&depth=3&filters=... — what users do after an event
func (s *Server) apiPaths(w http.ResponseWriter, r *http.Request) {
	start := r.URL.Query().Get("start")
	if start == "" {
		writeErr(w, http.StatusBadRequest, "start event is required")
		return
	}
	depth := 3
	if v, err := strconv.Atoi(r.URL.Query().Get("depth")); err == nil && v > 0 {
		depth = v
	}
	if depth > 10 {
		depth = 10
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, paths.After(evs, start, depth))
}
