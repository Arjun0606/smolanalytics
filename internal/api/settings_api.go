package api

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/settings"
)

//go:embed login.tmpl.html
var loginHTML string

//go:embed settings.tmpl.html
var settingsHTML string

var (
	loginTmpl    = template.Must(template.New("login").Parse(loginHTML))
	settingsTmpl = template.Must(template.New("settings").Parse(settingsHTML))
)

type settingsVM struct {
	Project     string
	Timezone    string
	Base        string
	Version     string
	Keys        []settings.APIKey
	EventCount  int
	AuthEnabled bool
	EnvKeySet   bool
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	evs, _ := s.store.Range(time.Time{}, time.Time{})
	vm := settingsVM{
		Project: s.projectName(), Base: baseURL(r), Version: Version,
		EventCount: len(evs), AuthEnabled: s.authEnabled(), EnvKeySet: s.writeKey != "",
	}
	if s.settings != nil {
		vm.Timezone = s.settings.Timezone()
		vm.Keys = s.settings.Keys()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingsTmpl.Execute(w, vm)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	var req struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	if err := s.settings.UpdateProject(req.Name, req.Timezone); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"project_name": s.settings.ProjectName(), "timezone": s.settings.Timezone()})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	k, err := s.settings.AddKey(req.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	if err := s.settings.RevokeKey(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"revoked": r.PathValue("id")})
}

func (s *Server) clearData(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Clear(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
