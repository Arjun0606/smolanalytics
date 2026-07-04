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
	writeJSON(w, http.StatusCreated, ep)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeErr(w, http.StatusServiceUnavailable, "webhooks unavailable")
		return
	}
	if err := s.webhooks.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("webhook.deleted", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
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
	if err := s.alerts.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("alert.deleted", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}
