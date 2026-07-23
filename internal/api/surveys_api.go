package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/survey"
)

// In-product micro-surveys. Management (list/save/delete/results) is gated like the rest of /v1:
// GET reads with the read key, POST/DELETE are session-only (the dashboard writes over MCP). The
// public path is GET /v1/surveys/active — the SDK widget holds only the write key, so it fetches
// the surveys to show with write-key auth + CORS, and gets only the widget-facing fields.

func (s *Server) listSurveys(w http.ResponseWriter, _ *http.Request) {
	if s.surveys == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveys not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"surveys": s.surveys.List()})
}

func (s *Server) saveSurvey(w http.ResponseWriter, r *http.Request) {
	if s.surveys == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveys not configured")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var sv survey.Survey
	if err := json.Unmarshal(body, &sv); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid survey JSON")
		return
	}
	saved, err := s.surveys.Save(sv)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteSurvey(w http.ResponseWriter, r *http.Request) {
	if s.surveys == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveys not configured")
		return
	}
	if err := s.surveys.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

// surveyResults: GET /v1/surveys/{id}/results?days=30 — the aggregate read (pinned MCP==API).
func (s *Server) surveyResults(w http.ResponseWriter, r *http.Request) {
	if s.surveys == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveys not configured")
		return
	}
	id := r.PathValue("id")
	sv, ok := s.surveys.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "survey not found")
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			days = n
		}
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, survey.Results(evs, id, sv.Type, days))
}

// activeSurveys: GET /v1/surveys/active?path=/pricing — the widget fetches which surveys to show
// on the current page. Public (write-key + CORS); returns only active surveys whose url_match (if
// set) is contained in path, with only the fields the widget needs — never internals.
func (s *Server) activeSurveys(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if !s.ingestAuth(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	path := r.URL.Query().Get("path")
	out := []map[string]any{}
	if s.surveys != nil {
		for _, sv := range s.surveys.List() {
			if !sv.Active {
				continue
			}
			if sv.URLMatch != "" && !strings.Contains(path, sv.URLMatch) {
				continue
			}
			out = append(out, map[string]any{
				"id":         sv.ID,
				"type":       sv.Type,
				"question":   sv.Question,
				"choices":    sv.Choices,
				"sample_pct": sv.SamplePct,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"surveys": out})
}
