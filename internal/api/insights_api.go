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
	found, err := s.insights.Delete(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRemoval(w, "deleted", "saved report", "id", r.PathValue("id"), found)
}

// --- boards -----------------------------------------------------------------------------
//
// A board is a named screen: its own reports, its own default filters, its own time range.
// Until now the product had exactly one flat pinned list, which is the single biggest functional
// gap against PostHog and Mixpanel — for most of their users the dashboard IS the product, and
// "you get one" is the first thing an evaluator notices.

func (s *Server) listBoards(w http.ResponseWriter, _ *http.Request) {
	if s.insights == nil {
		writeErr(w, http.StatusServiceUnavailable, "saved reports are not configured on this instance")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"boards": s.insights.Boards(),
		// counts travel with the list so the switcher can show "Growth · 4" without a second
		// round trip per board
		"counts": s.insights.CountByBoard(),
	})
}

func (s *Server) saveBoard(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		writeErr(w, http.StatusServiceUnavailable, "saved reports are not configured on this instance")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var b insights.Board
	if err := json.Unmarshal(body, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid board JSON")
		return
	}
	var saved insights.Board
	var err error
	if b.ID != "" {
		saved, err = s.insights.UpdateBoard(b.ID, b)
	} else {
		saved, err = s.insights.CreateBoard(b)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteBoard(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		writeErr(w, http.StatusServiceUnavailable, "saved reports are not configured on this instance")
		return
	}
	if err := s.insights.DeleteBoard(r.PathValue("id")); err != nil {
		// The store refuses to delete a board that still holds reports rather than silently
		// taking them with it. Surfaced as the reason, not a bare 400 — "move them first" is an
		// instruction, "bad request" is a shrug.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("id")})
}

func (s *Server) moveInsight(w http.ResponseWriter, r *http.Request) {
	if s.insights == nil {
		writeErr(w, http.StatusServiceUnavailable, "saved reports are not configured on this instance")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	var req struct {
		BoardID string `json:"board_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.BoardID == "" {
		writeErr(w, http.StatusBadRequest, "board_id is required")
		return
	}
	moved, err := s.insights.MoveInsight(r.PathValue("id"), req.BoardID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, moved)
}
