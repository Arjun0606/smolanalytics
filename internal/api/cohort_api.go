package api

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
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
	found, err := s.cohorts.Delete(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRemoval(w, "deleted", "cohort", "id", r.PathValue("id"), found)
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
	// cohort.Resolve returns a map, and this ranged it straight into the response: two identical
	// requests over the same immutable event log returned the same count in a DIFFERENT order
	// every time (measured: 5 of 5 repeats differed on a 30-member cohort), so no diff, cached
	// snapshot or byte-comparison agreement test could rely on the one property this product
	// sells — ask twice, get the same answer. Sorted, and capped like every other user list here
	// (apiWho caps at 200) so a large cohort cannot serialize every distinct_id into one body;
	// `count` stays the TRUE size and `truncated` says so out loud rather than letting a reader
	// assume len(users) is the whole cohort.
	sort.Strings(ids)
	total := len(ids)
	if len(ids) > 200 {
		ids = ids[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cohort": d.Name, "count": total, "users": ids, "truncated": total > len(ids),
	})
}
