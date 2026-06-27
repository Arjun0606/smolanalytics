package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Arjun0606/smolanalytics/internal/insights"
)

// Saved reports — pin an Explore report so it's one click every morning. These are
// operator config managed from the dashboard, so they share the dashboard's surface
// (the write key protects public ingestion, not operator config).

func (s *Server) listInsights(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"insights": s.insights.List()})
}

func (s *Server) saveInsight(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var in insights.Insight
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid insight JSON")
		return
	}
	saved, err := s.insights.Save(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteInsight(w http.ResponseWriter, r *http.Request) {
	if err := s.insights.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}
