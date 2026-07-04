package api

// Goal management over HTTP — the dashboard's create-goal form and the settings
// page wrap the same goal.Store the MCP tools use, so a goal created anywhere
// shows up everywhere. Session-gated by authMW like every /v1 write.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/goal"
)

// createGoal saves a named conversion goal. Kind may be omitted: the dashboard
// form is one field, so a value starting with "/" is a page-path glob and
// anything else is an event name — the same rule the form documents.
func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) {
	if s.goals == nil {
		writeErr(w, http.StatusServiceUnavailable, "goals unavailable")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	if req.Kind == "" {
		if strings.HasPrefix(req.Value, "/") {
			req.Kind = "path"
		} else {
			req.Kind = "event"
		}
	}
	d, err := s.goals.Save(goal.Definition{Name: req.Name, Kind: req.Kind, Value: req.Value})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rec("goal.created", d.Name)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	if s.goals == nil {
		writeErr(w, http.StatusServiceUnavailable, "goals unavailable")
		return
	}
	if err := s.goals.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("goal.deleted", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}
