package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dashboard auth: a single operator password (SMOLANALYTICS_PASSWORD). When set,
// the dashboard + management APIs require a signed session cookie; ingestion and
// MCP keep their own key auth. When unset, everything is open (local dev).

const sessionCookie = "sa_session"

func dashPassword() string { return os.Getenv("SMOLANALYTICS_PASSWORD") }

// authEnabled is true when a login is required — either the env password or an
// in-app account password has been set.
func (s *Server) authEnabled() bool {
	return dashPassword() != "" || (s.settings != nil && s.settings.HasPassword())
}

// checkLogin accepts either the env password or the in-app account password.
func (s *Server) checkLogin(pw string) bool {
	if pw == "" {
		return false
	}
	if env := dashPassword(); env != "" && hmac.Equal([]byte(pw), []byte(env)) {
		return true
	}
	return s.settings != nil && s.settings.CheckPassword(pw)
}

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
	case p == "/v1/events" || p == "/mcp" || p == "/v1/usage" || p == "/v1/notable": // own key auth / programmatic
		return true
	case strings.HasPrefix(p, "/share/"): // read-only share pages carry their own token auth
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
		// the stats API: a valid API key reads any GET /v1/* report programmatically
		// (scripts, CI, cron) — same key that authorizes ingest and MCP. Writes and
		// settings stay session-only.
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/") && s.keyAuthed(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			writeErr(w, http.StatusUnauthorized, "login required (or pass Authorization: Bearer <api key> on GET endpoints)")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// loginLimiter is a small fixed-window brute-force guard: max 10 failed attempts
// per client IP per 5 minutes. In-memory (per-process) is enough — this protects a
// single-operator password, not a multi-tenant login farm.
type loginLimiter struct {
	mu     sync.Mutex
	fails  map[string]int
	window time.Time
}

var loginGuard = &loginLimiter{fails: map[string]int{}}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.window) > 5*time.Minute {
		l.fails = map[string]int{}
		l.window = now
	}
	return l.fails[ip] >= 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip]++
}

func clientIP(r *http.Request) string {
	// behind the Fly/derivative proxy the real client is in X-Forwarded-For
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.IndexByte(xf, ','); i > 0 {
			return strings.TrimSpace(xf[:i])
		}
		return strings.TrimSpace(xf)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !s.authEnabled() || s.validSession(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		e := r.URL.Query().Get("e")
		_ = loginTmpl.Execute(w, map[string]any{"Project": s.projectName(), "Error": e != "", "RateLimited": e == "rl"})
		return
	}
	// POST
	ip := clientIP(r)
	if loginGuard.blocked(ip) {
		// a browser form post must get a page back, not raw JSON
		http.Redirect(w, r, "/login?e=rl", http.StatusFound)
		return
	}
	if s.checkLogin(r.FormValue("password")) {
		s.setSession(w, r)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	loginGuard.fail(ip)
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
