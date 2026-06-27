package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/cohort"
)

// Cohorts — define a reusable user group once, then scope any report to it with
// ?cohort=<id>. Managed from the dashboard (same open surface as saved reports).

func (s *Server) listCohorts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cohorts": s.cohorts.List()})
}

func (s *Server) saveCohort(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var d cohort.Definition
	if err := json.Unmarshal(body, &d); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid cohort JSON")
		return
	}
	saved, err := s.cohorts.Save(d)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteCohort(w http.ResponseWriter, r *http.Request) {
	if err := s.cohorts.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

// cohortUsers resolves a cohort to its members. GET /v1/cohorts/{id}/users
func (s *Server) cohortUsers(w http.ResponseWriter, r *http.Request) {
	d, ok := s.cohorts.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "cohort not found")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	members := cohort.Resolve(evs, d)
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cohort": d.Name, "count": len(ids), "users": ids})
}
