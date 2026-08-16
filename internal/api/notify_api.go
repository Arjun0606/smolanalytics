package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

// --- webhooks ---

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusServiceUnavailable, "webhooks unavailable")
		return
	}
	var req struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Format string `json:"format"` // "" = auto-detect (hooks.slack.com → slack), or "slack" to force Slack text format
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	ep, err := s.webhooks.Add(req.Name, req.URL, req.Format)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rec("webhook.created", ep.Name)
	// Return the RESOLVED format, not the stored one. Stored is "" for an auto-detected endpoint,
	// so a Discord URL added without picking a format would have been labelled "signed json" in
	// the row the user is looking at, while actually delivering Discord messages. The UI must be
	// told what will happen, not what was typed.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": ep.ID, "name": ep.Name, "url": ep.URL, "secret": ep.Secret,
		"format": resolvedFormat(ep), "enabled": ep.Enabled, "created": ep.Created,
	})
}

// resolvedFormat names what an endpoint will ACTUALLY send, auto-detection included.
func resolvedFormat(ep webhook.Endpoint) string {
	switch {
	case ep.DiscordFormat():
		return "discord"
	case ep.SlackFormat():
		return "slack"
	}
	return "signed json"
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusServiceUnavailable, "webhooks unavailable")
		return
	}
	found, err := s.webhooks.Delete(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if found { // never audit-log a deletion that didn't happen
		s.rec("webhook.deleted", r.PathValue("id"))
	}
	writeRemoval(w, "deleted", "webhook", "id", r.PathValue("id"), found)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusServiceUnavailable, "webhooks unavailable")
		return
	}
	ep, ok := s.webhooks.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "webhook not found")
		return
	}
	status, err := webhook.SendTest(ep)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("delivery failed (endpoint status %d): %v", status, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "delivered", "endpoint_status": status})
}

// --- alerts ---

func (s *Server) createAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		writeErr(w, http.StatusServiceUnavailable, "alerts unavailable")
		return
	}
	var req alert.Alert
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	a, err := s.alerts.Add(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rec("alert.created", a.Name)
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) deleteAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		writeErr(w, http.StatusServiceUnavailable, "alerts unavailable")
		return
	}
	found, err := s.alerts.Delete(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if found { // never audit-log a deletion that didn't happen
		s.rec("alert.deleted", r.PathValue("id"))
	}
	writeRemoval(w, "deleted", "alert", "id", r.PathValue("id"), found)
}

// listAlerts / listWebhooks — API-1: POST-ed resources are GET-listable, matching
// the MCP list_* tools' payloads.
func (s *Server) listAlerts(w http.ResponseWriter, _ *http.Request) {
	if s.alerts == nil {
		writeErr(w, http.StatusNotFound, "alerts are not enabled on this instance")
		return
	}
	// Describe() renders the rule in words, and the list returned the bare struct — so the
	// dashboard's "fires when" column printed an em dash for every alert ever created, and
	// list_alerts told an agent a threshold and an op with no way to know what they meant for
	// the kind in question. An alert nobody can read is an alert nobody trusts at 3am.
	out := make([]map[string]any, 0)
	for _, a := range s.alerts.List() {
		out = append(out, map[string]any{
			"id": a.ID, "name": a.Name, "event": a.Event, "kind": a.Kind(),
			"op": a.Op, "threshold": a.Threshold, "window_hours": a.WindowHours,
			"against": a.Against, "created": a.Created, "last_fired": a.LastFired,
			"describe": a.Describe(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
}

func (s *Server) listWebhooks(w http.ResponseWriter, _ *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusNotFound, "webhooks are not enabled on this instance")
		return
	}
	// Health and the RESOLVED format, not the bare struct. The secret is deliberately omitted:
	// it is shown once at creation and is not retrievable, so echoing it in a list every
	// dashboard load would undo that.
	out := make([]map[string]any, 0)
	for _, ep := range s.webhooks.List() {
		out = append(out, map[string]any{
			"id": ep.ID, "name": ep.Name, "url": ep.URL,
			"format": resolvedFormat(ep), "enabled": ep.Enabled,
			"created": ep.Created, "health": ep.Health(), "healthy": ep.Healthy(),
			"last_status": ep.LastStatus, "consecutive_failures": ep.Failures,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": out})
}

// PATCH /v1/webhooks/{id} — pause or resume an endpoint.
//
// The Enabled field shipped with the store and no method ever wrote it, so the only way to stop
// deliveries was to DELETE the endpoint, which destroys the signing secret and forces every
// receiver to be reconfigured. A pause that costs you your secret is not a pause.
func (s *Server) patchWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusNotFound, "webhooks are not enabled on this instance")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err := json.Unmarshal(body, &req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `send {"enabled": true} or {"enabled": false}`)
		return
	}
	ep, found, err := s.webhooks.SetEnabled(r.PathValue("id"), *req.Enabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no webhook with that id")
		return
	}
	s.rec("webhook.enabled", ep.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": ep.ID, "enabled": ep.Enabled, "health": ep.Health(),
	})
}
