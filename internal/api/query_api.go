package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

// filtersFrom parses ?filters=<url-encoded JSON array> into predicates, so any
// report can be segmented (e.g. plan=pro AND country=US). Malformed filters are an
// ERROR, never ignored — silently returning unfiltered data as if it were the
// segment is the worst kind of wrong answer.
func filtersFrom(r *http.Request) ([]query.Filter, error) {
	raw := r.URL.Query().Get("filters")
	if raw == "" {
		return nil, nil
	}
	var fs []query.Filter
	if err := json.Unmarshal([]byte(raw), &fs); err != nil {
		return nil, badRequestError{fmt.Sprintf("invalid filters JSON: %v", err)}
	}
	if err := query.Validate(fs); err != nil {
		return nil, badRequestError{err.Error()}
	}
	return fs, nil
}

// badRequestError marks a caller mistake (bad filters) so handlers return 400, not 500.
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

// writeQueryErr maps a filtered()/store error to the right status code.
func writeQueryErr(w http.ResponseWriter, err error) {
	var br badRequestError
	if errors.As(err, &br) {
		writeErr(w, http.StatusBadRequest, br.msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// filtered loads all events, applies the request's property filters, and (if
// ?cohort=<id> is set) scopes to that cohort's members. Cohort membership is
// resolved over the full history, then the filtered events are kept for those users.
func (s *Server) filtered(r *http.Request) ([]event.Event, error) {
	fs, err := filtersFrom(r)
	if err != nil {
		return nil, err
	}
	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	evs := query.Apply(all, fs)
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
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": names})
}

// GET /v1/trends?event=signup&unique=true&breakdown=source&filters=...
// With breakdown set, returns one series per property value (multi-line trend).
func (s *Server) apiTrends(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
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
		writeQueryErr(w, err)
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
		writeQueryErr(w, err)
		return
	}
	rr := retention.Compute(evs, days, r.URL.Query().Get("event"))
	// ship the honest day-N summaries (observable cohorts only, retention.DayN) so no
	// client ever re-derives them wrong from the raw grid.
	out := map[string]any{"cohorts": rr.Cohorts, "max_days": rr.MaxDays}
	now := time.Now().UTC()
	for _, n := range []int{1, 7, 30} {
		if ret, size := retention.DayN(rr, n, now); size > 0 {
			out[fmt.Sprintf("day%d_retention_pct", n)] = int(float64(ret)/float64(size)*100 + 0.5)
			out[fmt.Sprintf("day%d_cohort_users", n)] = size
		}
	}
	writeJSON(w, http.StatusOK, out)
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
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": engagement.ComputeLifecycle(evs, days)})
}

// GET /v1/stickiness?filters=... — DAU/WAU/MAU + ratio
func (s *Server) apiStickiness(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
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
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, paths.After(evs, start, depth))
}

// GET /v1/web?days=30&filters=... — the web-analytics overview (visitors, pageviews,
// live-now, top pages, referrers, UTM sources, device split) from $pageview events.
func (s *Server) apiWeb(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	if days > 365 {
		days = 365
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, web.Compute(evs, days, time.Time{}))
}

// GET /v1/groups?property=company&limit=50&filters=... — account-level roll-up
func (s *Server) apiGroups(w http.ResponseWriter, r *http.Request) {
	property := r.URL.Query().Get("property")
	if property == "" {
		writeErr(w, http.StatusBadRequest, "property is required (the group key, e.g. company)")
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups.Compute(evs, property, time.Time{}, limit))
}
