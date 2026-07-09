package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Arjun0606/smolanalytics/internal/defined"
)

// Defined events — name a business event from autocaptured clicks/pageviews, retroactive
// and zero-code (the Heap wedge). Managed from the dashboard "save as event" builder and
// the define_event MCP tool, which write the same primitive.

func (s *Server) listDefined(w http.ResponseWriter, _ *http.Request) {
	if s.defined == nil {
		writeJSON(w, http.StatusOK, map[string]any{"defined_events": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"defined_events": s.defined.List()})
}

func (s *Server) saveDefined(w http.ResponseWriter, r *http.Request) {
	if s.defined == nil {
		writeErr(w, http.StatusServiceUnavailable, "defined events aren't enabled on this instance")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var d defined.Definition
	if err := json.Unmarshal(body, &d); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	saved, err := s.defined.Save(d)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteDefined(w http.ResponseWriter, r *http.Request) {
	if s.defined == nil {
		writeErr(w, http.StatusServiceUnavailable, "defined events aren't enabled")
		return
	}
	if err := s.defined.Delete(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("name")})
}
