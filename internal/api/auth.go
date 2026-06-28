package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Dashboard auth: a single operator password (SMOLANALYTICS_PASSWORD). When set,
// the dashboard + management APIs require a signed session cookie; ingestion and
// MCP keep their own key auth. When unset, everything is open (local dev).

const sessionCookie = "sa_session"

func dashPassword() string { return os.Getenv("SMOLANALYTICS_PASSWORD") }

func (s *Server) authEnabled() bool { return dashPassword() != "" }

func (s *Server) sessionSecret() string {
	if s.settings != nil {
		return s.settings.Secret()
	}
	return "smolanalytics-dev-secret"
}

func (s *Server) signSession(exp int64) string {
	msg := strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, []byte(s.sessionSecret()))
	mac.Write([]byte(msg))
	return msg + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	dot := strings.IndexByte(c.Value, '.')
	if dot < 0 {
		return false
	}
	exp, err := strconv.ParseInt(c.Value[:dot], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(c.Value), []byte(s.signSession(exp)))
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.signSession(exp), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(exp, 0),
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

// isPublic lists paths that never require a dashboard session (they have their own
// auth or are meant to be reachable by the SDK / AI tools).
func isPublic(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/login" || p == "/logout" || p == "/healthz" || p == "/version" || p == "/sdk.js":
		return true
	case p == "/v1/events" || p == "/mcp": // own key auth / programmatic
		return true
	case r.Method == http.MethodOptions:
		return true
	}
	return false
}

// authMW gates the dashboard + management surface behind the session when a
// password is configured.
func (s *Server) authMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled() || isPublic(r) || s.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !s.authEnabled() || s.validSession(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = loginTmpl.Execute(w, map[string]any{"Project": s.projectName(), "Error": r.URL.Query().Get("e") != ""})
		return
	}
	// POST
	pw := r.FormValue("password")
	if pw != "" && hmac.Equal([]byte(pw), []byte(dashPassword())) {
		s.setSession(w, r)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login?e=1", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) projectName() string {
	if s.settings != nil {
		return s.settings.ProjectName()
	}
	return "smolanalytics"
}
