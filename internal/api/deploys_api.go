package api

// Deploy markers over HTTP — the wedge no other analytics tool ships: tie a metric change
// to the deploy that caused it. Recording a marker takes the PUBLIC write key (like POST
// /v1/events), so CI, a git hook, `smolanalytics deploy`, or the cloud's GitHub sync can all
// post one with the key they already hold. Reading the before/after impact takes the read
// key/session and is computed by the SAME trends engine the dashboard renders — a CI test
// (agreement_test.go) pins the number to the dashboard, so it's proven, never guessed.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
)

// SetDeploys attaches the store to both the HTTP server and the MCP server, so a marker
// recorded over HTTP is visible to the editor and vice-versa (one store, every surface).
func (s *Server) SetDeploys(d *deploys.Store) { s.deploys = d; s.mcp.SetDeploys(d) }

func (s *Server) createDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.ingestAuth(r) { // public write key, exactly like POST /v1/events
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	if s.deploys == nil {
		writeErr(w, http.StatusServiceUnavailable, "deploys unavailable")
		return
	}
	var req struct {
		SHA, Message, Author, Ref, URL, Source, At string
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if req.SHA == "" && req.Message == "" {
		writeErr(w, http.StatusBadRequest, "a deploy needs a sha or a message")
		return
	}
	d := deploys.Deploy{SHA: req.SHA, Message: req.Message, Author: req.Author, Ref: req.Ref, URL: req.URL, Source: req.Source}
	if req.At != "" {
		t, err := parseDeployTime(req.At)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad 'at' time (want RFC3339 or YYYY-MM-DD)")
			return
		}
		d.At = t
	}
	saved, err := s.deploys.Record(d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("deploy.recorded", saved.SHA+" "+saved.Message)
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteDeploy(w http.ResponseWriter, r *http.Request) {
	if s.deploys == nil {
		writeErr(w, http.StatusServiceUnavailable, "deploys unavailable")
		return
	}
	if err := s.deploys.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("deploy.deleted", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

// listDeploys returns the markers; with ?event=<name> it also returns each deploy's
// before/after impact on that metric (?days bounds the series, ?window the days compared
// each side). The impact uses trends.Compute — the dashboard's own engine — so the number
// is the dashboard's, and the MCP deploy_impact tool returns byte-identical output.
func (s *Server) listDeploys(w http.ResponseWriter, r *http.Request) {
	if s.deploys == nil {
		writeErr(w, http.StatusNotFound, "deploys are not enabled on this instance")
		return
	}
	q := r.URL.Query()
	event := q.Get("event")
	if event == "" {
		writeJSON(w, http.StatusOK, map[string]any{"deploys": s.deploys.List()})
		return
	}
	if !s.knownEventOr400(w, event) {
		return
	}
	days := clampInt(q.Get("days"), 30, 1, 365)
	window := clampInt(q.Get("window"), 3, 1, 30)
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deploys.Report(evs, s.deploys.List(), event, days, window))
}

func parseDeployTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", v)
	return t.UTC(), err
}

func clampInt(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < lo {
		if err != nil {
			return def
		}
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
