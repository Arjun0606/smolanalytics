package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/audit"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

//go:embed login.tmpl.html
var loginHTML string

//go:embed settings.tmpl.html
var settingsHTML string

var (
	loginTmpl    = template.Must(template.New("login").Parse(loginHTML))
	settingsTmpl = template.Must(template.New("settings").Parse(settingsHTML))
)

type eventStat struct {
	Name  string
	Count int
}

type settingsVM struct {
	Section     string
	Project     string
	Timezone    string
	Username    string
	HasPassword bool
	AuthVia     string // "in-app" | "env" | "none"
	RetainDays  int
	Base        string
	Version     string
	Keys        []settings.APIKey
	EventCount  int
	EventStats  []eventStat
	Audit       []audit.Entry
	Webhooks    []webhook.Endpoint
	Alerts      []alert.Alert
	AuthEnabled bool
	EnvKeySet   bool
}

var settingsSections = map[string]bool{
	"account": true, "project": true, "keys": true, "install": true,
	"data": true, "webhooks": true, "alerts": true, "audit": true, "about": true,
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	if !settingsSections[section] {
		section = "account"
	}
	evs, _ := s.store.Range(time.Time{}, time.Time{})

	vm := settingsVM{
		Section: section, Project: s.projectName(), Base: baseURL(r), Version: Version,
		EventCount: len(evs), AuthEnabled: s.authEnabled(), EnvKeySet: s.writeKey != "",
		Username: "admin", AuthVia: "none",
	}
	if s.settings != nil {
		vm.Timezone = s.settings.Timezone()
		vm.Keys = s.settings.Keys()
		vm.Username = s.settings.Username()
		vm.HasPassword = s.settings.HasPassword()
		vm.RetainDays = s.settings.RetainDays()
	}
	switch {
	case vm.HasPassword:
		vm.AuthVia = "in-app"
	case dashPassword() != "":
		vm.AuthVia = "env"
	}

	if section == "data" { // event taxonomy
		counts := map[string]int{}
		for _, e := range evs {
			counts[e.Name]++
		}
		names, _ := s.store.Names()
		for _, n := range names {
			vm.EventStats = append(vm.EventStats, eventStat{Name: n, Count: counts[n]})
		}
		sort.Slice(vm.EventStats, func(i, j int) bool { return vm.EventStats[i].Count > vm.EventStats[j].Count })
	}
	if section == "audit" {
		vm.Audit = s.audit.Recent(200)
	}
	if section == "webhooks" && s.webhooks != nil {
		vm.Webhooks = s.webhooks.List()
	}
	if section == "alerts" {
		if s.alerts != nil {
			vm.Alerts = s.alerts.List()
		}
		if len(vm.EventStats) == 0 { // event-name suggestions for the alert form
			names, _ := s.store.Names()
			for _, n := range names {
				vm.EventStats = append(vm.EventStats, eventStat{Name: n})
			}
		}
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
	s.rec("project.updated", s.settings.ProjectName())
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
	s.rec("key.created", k.Name)
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
	s.rec("key.revoked", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"revoked": r.PathValue("id")})
}

// updateAccount sets/changes the operator login. Changing an existing password
// requires the current one; setting the first password (bootstrap) does not.
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		CurrentPassword string `json:"current_password"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, "new password is required")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if s.settings.HasPassword() && !s.settings.CheckPassword(req.CurrentPassword) {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	if err := s.settings.SetAccount(req.Username, req.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSession(w, r) // keep the current operator logged in after the change
	s.rec("account.updated", "username "+s.settings.Username())
	writeJSON(w, http.StatusOK, map[string]string{"username": s.settings.Username()})
}

// signoutAll rotates the session secret, invalidating every session (including the
// caller's), then clears the current cookie.
func (s *Server) signoutAll(w http.ResponseWriter, _ *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	if err := s.settings.RotateSecret(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("account.signout_all", "all sessions invalidated")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out everywhere"})
}

func (s *Server) updateRetention(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	var req struct {
		Days int `json:"days"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	_ = json.Unmarshal(body, &req)
	if err := s.settings.SetRetainDays(req.Days); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("retention.updated", fmt.Sprintf("%d days", req.Days))
	writeJSON(w, http.StatusOK, map[string]int{"retain_days": req.Days})
}

func (s *Server) clearData(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Clear(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("data.cleared", "all events deleted")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
