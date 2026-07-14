// Package api serves the single-binary HTTP surface: event ingestion + the
// server-rendered dashboard. No web framework, no SPA build step — the whole UI is
// embedded in the binary and rendered fast on the server (the speed IS a feature).
package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/audit"
	"github.com/Arjun0606/smolanalytics/internal/botua"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/defined"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/exportlink"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/geo"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/gsc"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

//go:embed sdk.js
var sdkJS string

// Version is the build version (overridable at build time via -ldflags).
var Version = "0.1.0"

type Server struct {
	store        store.Store
	mcp          *mcp.Server
	insights     *insights.Store
	cohorts      *cohort.Store
	settings     *settings.Store
	audit        *audit.Log
	webhooks     *webhook.Store
	alerts       *alert.Store
	shares       *share.Store
	aliases      *alias.Map
	gsc          *gsc.Store
	goals        *goal.Store
	exports      *exportlink.Store
	defined      *defined.Store // retroactive zero-code events (Heap wedge)
	writeKey     string         // if set, POST /v1/events requires Authorization: Bearer <writeKey>
	geo          *geo.Resolver  // ingest-time IP→country (IP never stored); nil = disabled
	anomalyMu    sync.Mutex
	anomalyFired map[string]time.Time // finding title -> last webhook fire (24h dedup)
	// autocaptured events dropped because the UA was a known crawler/bot — surfaced in
	// /v1/usage so "why is my dashboard lower than GA?" has a visible, honest answer.
	botsFiltered atomic.Int64
}

// SetSettings swaps in a persistent settings store (project, keys, session secret).
func (s *Server) SetSettings(st *settings.Store) { s.settings = st; s.mcp.SetSettings(st) }

// SetTrackPlan attaches the tracking-plan store (shared with the MCP instrumentation tools).
func (s *Server) SetTrackPlan(tp *trackplan.Store) { s.mcp.SetTrackPlan(tp) }

func New(s store.Store) *Server {
	ins, _ := insights.Open("") // in-memory by default; Set* adds persistence
	coh, _ := cohort.Open("")
	m := mcp.New(s)
	m.SetInsights(ins) // MCP action tools share the same stores from the start
	m.SetCohorts(coh)
	return &Server{store: s, mcp: m, insights: ins, cohorts: coh}
}

// SetInsights swaps in a persistent saved-reports store (shared with the MCP action
// tools, so "save this report" from the editor lands on the dashboard instantly).
func (s *Server) SetInsights(st *insights.Store) { s.insights = st; s.mcp.SetInsights(st) }

// SetCohorts swaps in a persistent cohort store (shared with MCP).
func (s *Server) SetCohorts(st *cohort.Store) { s.cohorts = st; s.mcp.SetCohorts(st) }

// SetAliases attaches the identity-stitching map (ingest records anon→user on
// $identify; the MCP import tool does the same for imported history).
func (s *Server) SetAliases(a *alias.Map) { s.aliases = a; s.mcp.SetAliases(a) }

// SetDefined attaches the retroactive defined-events store (shared with MCP + the
// dashboard "save as event" builder).
func (s *Server) SetDefined(d *defined.Store) { s.defined = d; s.mcp.SetDefined(d) }

// SetGSC attaches the Search Console store (dashboard card + MCP report).
func (s *Server) SetGSC(g *gsc.Store) { s.gsc = g; s.mcp.SetGSC(g) }

// SetGoals attaches the goals store (dashboard card + MCP goal tools).
func (s *Server) SetGoals(g *goal.Store) { s.goals = g; s.mcp.SetGoals(g) }

// SetAudit swaps in a persistent audit log.
func (s *Server) SetAudit(l *audit.Log) { s.audit = l }

// SetWebhooks / SetAlerts swap in the persistent notification stores (shared with MCP).
func (s *Server) SetWebhooks(w *webhook.Store) { s.webhooks = w; s.mcp.SetWebhooks(w) }
func (s *Server) SetAlerts(a *alert.Store)     { s.alerts = a; s.mcp.SetAlerts(a) }

// rec records an operator action to the audit log (best-effort, nil-safe).
func (s *Server) rec(action, detail string) { s.audit.Record(action, detail) }

// EvaluateAlerts runs every enabled alert against the current data and fires those
// whose condition is met (debounced to once per window). Called on a schedule.

// evaluateAnomalies pushes the verdict engine's WARN findings to the configured
// webhooks — the "signups down 34%: it's mobile safari at checkout" pull, with the
// finding itself as the proven diagnosis (same engine as the dashboard verdict, so
// the alert and the page can never disagree). Per-finding 24h dedup + a global cap
// of 2 anomaly sends per 24h (the plausible rule: alerts that fire rarely get read).
func (s *Server) evaluateAnomalies() {
	if s.webhooks == nil {
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return
	}
	evs = query.Apply(evs, nil)
	findings := insight.Generate(evs)
	now := time.Now().UTC()
	s.anomalyMu.Lock()
	defer s.anomalyMu.Unlock()
	if s.anomalyFired == nil {
		s.anomalyFired = map[string]time.Time{}
	}
	sent := 0
	for _, t := range s.anomalyFired {
		if now.Sub(t) < 24*time.Hour {
			sent++
		}
	}
	for _, f := range findings {
		if f.Severity != "warn" || sent >= 2 {
			continue
		}
		if last, ok := s.anomalyFired[f.Title]; ok && now.Sub(last) < 24*time.Hour {
			continue
		}
		s.anomalyFired[f.Title] = now
		sent++
		payload := map[string]any{
			"type": "anomaly", "title": f.Title, "detail": f.Detail, "fired_at": now,
			"computed_by": "the verdict engine (notable-change detection), the same computation the dashboard's 'what to look at' renders",
		}
		s.webhooks.DeliverAll(payload, "⚠ "+f.Title+" — "+f.Detail)
	}
}

func (s *Server) EvaluateAlerts() {
	s.evaluateAnomalies()
	if s.alerts == nil {
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default

	now := time.Now().UTC()
	for _, a := range s.alerts.List() {
		if !a.Enabled {
			continue
		}
		window := time.Duration(a.WindowHours) * time.Hour
		if window <= 0 {
			window = 24 * time.Hour
		}
		cutoff := now.Add(-window)
		var count float64
		for _, e := range evs {
			if e.Name == a.Event && !e.Timestamp.Before(cutoff) { // inclusive window, consistent
				count++
			}
		}
		met := (a.Op == "gt" && count > a.Threshold) || (a.Op == "lt" && count < a.Threshold)
		fired := false
		if met && (a.LastFired.IsZero() || now.Sub(a.LastFired) >= window) {
			fired = true
			payload := map[string]any{
				"type": "alert", "alert": a.Name, "event": a.Event,
				"op": a.Op, "threshold": a.Threshold, "value": count,
				"window_hours": a.WindowHours, "fired_at": now,
			}
			if s.webhooks != nil {
				// plain-text rendering for Slack-format endpoints — same tight
				// "⚠ title — detail" shape the daily brief uses
				verb := "above"
				if a.Op == "lt" {
					verb = "below"
				}
				text := fmt.Sprintf("⚠ %s — %s: %g events in the last %dh, %s threshold %g",
					a.Name, a.Event, count, a.WindowHours, verb, a.Threshold)
				s.webhooks.DeliverAll(payload, text)
			}
			s.rec("alert.fired", fmt.Sprintf("%s — %s %s %g (value %g)", a.Name, a.Event, a.Op, a.Threshold, count))
		}
		s.alerts.SetChecked(a.ID, count, fired, now)
	}
}

// SetWriteKey gates event ingestion behind a write key (production). Empty = open
// (dev). The SDK passes the same key.
func (s *Server) SetWriteKey(k string) { s.writeKey = k }

// SetGeo enables ingest-time country resolution (the IP is used for one lookup
// and never stored, only the ISO code lands on the event).
func (s *Server) SetGeo(g *geo.Resolver) { s.geo = g }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"name": "smolanalytics", "version": Version})
	})
	mux.HandleFunc("POST /v1/events", s.ingest)
	mux.HandleFunc("OPTIONS /v1/events", s.preflight) // browser SDK CORS preflight
	mux.HandleFunc("GET /sdk.js", s.serveSDK)
	mux.HandleFunc("POST /v1/ask", s.ask)
	mux.HandleFunc("GET /v1/funnel", s.apiFunnel)
	mux.HandleFunc("GET /v1/trends", s.apiTrends)
	mux.HandleFunc("GET /v1/breakdown", s.apiBreakdown)
	mux.HandleFunc("GET /v1/retention", s.apiRetention)
	mux.HandleFunc("GET /v1/lifecycle", s.apiLifecycle)
	mux.HandleFunc("GET /v1/stickiness", s.apiStickiness)
	mux.HandleFunc("GET /v1/paths", s.apiPaths)
	mux.HandleFunc("GET /v1/groups", s.apiGroups)
	mux.HandleFunc("GET /v1/web", s.apiWeb)
	mux.HandleFunc("GET /v1/meta", s.apiMeta)
	mux.HandleFunc("GET /v1/usage", s.usage)
	mux.HandleFunc("GET /v1/notable", s.notable)
	mux.HandleFunc("GET /v1/brief", s.apiBrief)
	mux.HandleFunc("GET /v1/events/recent", s.recentEvents)
	mux.HandleFunc("GET /v1/users/{id}", s.userActivity)
	mux.HandleFunc("GET /v1/who", s.apiWho) // the microscope: the people behind any datapoint
	mux.HandleFunc("GET /v1/export", s.export)
	mux.HandleFunc("GET /v1/insights", s.listInsights)
	mux.HandleFunc("POST /v1/insights", s.saveInsight)
	mux.HandleFunc("DELETE /v1/insights/{id}", s.deleteInsight)
	mux.HandleFunc("GET /v1/cohorts", s.listCohorts)
	mux.HandleFunc("POST /v1/cohorts", s.saveCohort)
	mux.HandleFunc("DELETE /v1/cohorts/{id}", s.deleteCohort)
	mux.HandleFunc("GET /v1/defined", s.listDefined)
	mux.HandleFunc("POST /v1/defined", s.saveDefined)
	mux.HandleFunc("DELETE /v1/defined/{name}", s.deleteDefined)
	mux.HandleFunc("GET /v1/cohorts/{id}/users", s.cohortUsers)
	mux.HandleFunc("POST /mcp", s.handleMCP)
	mux.HandleFunc("GET /share/{token}", s.sharePage)       // public read-only web overview (token-gated)
	mux.HandleFunc("GET /export/{token}", s.exportDownload) // one-time full-export download (token burns on use)
	// account + settings (the operational staple)
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /logout", s.logout)
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/account", http.StatusFound)
	})
	mux.HandleFunc("GET /settings/{section}", s.settingsPage)
	mux.HandleFunc("POST /v1/settings", s.updateSettings)
	mux.HandleFunc("POST /v1/settings/account", s.updateAccount)
	mux.HandleFunc("POST /v1/settings/signout-all", s.signoutAll)
	mux.HandleFunc("POST /v1/settings/retention", s.updateRetention)
	mux.HandleFunc("POST /v1/settings/keys", s.createKey)
	mux.HandleFunc("DELETE /v1/settings/keys/{id}", s.revokeKey)
	mux.HandleFunc("POST /v1/settings/clear", s.clearData)
	mux.HandleFunc("DELETE /v1/users/{id}/data", s.deleteUserData)
	// API-1 (resource symmetry): every store with a POST has a GET list, payload-
	// matched to its MCP list_* tool — and /v1/* never falls through to an HTML 404.
	mux.HandleFunc("GET /v1/webhooks", s.listWebhooks)
	mux.HandleFunc("GET /v1/alerts", s.listAlerts)
	mux.HandleFunc("GET /v1/shares", s.listShares)
	mux.HandleFunc("GET /v1/goals", s.listGoals)
	mux.HandleFunc("POST /v1/webhooks", s.createWebhook)
	mux.HandleFunc("DELETE /v1/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /v1/webhooks/{id}/test", s.testWebhook)
	mux.HandleFunc("POST /v1/alerts", s.createAlert)
	mux.HandleFunc("DELETE /v1/alerts/{id}", s.deleteAlert)
	mux.HandleFunc("POST /v1/shares", s.createShare)
	mux.HandleFunc("DELETE /v1/shares/{id}", s.deleteShare)
	mux.HandleFunc("POST /v1/goals", s.createGoal)
	mux.HandleFunc("DELETE /v1/goals/{id}", s.deleteGoal)
	mux.HandleFunc("GET /", s.dashboard)
	return recoverMW(s.authMW(mux))
}

// recoverMW turns a panic in any handler into a 500 instead of crashing the
// server — a basic production safety net.
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("smolanalytics: panic on %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// setCORS lets the browser SDK post events from any origin.
func setCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	h.Set("Access-Control-Max-Age", "86400")
}

func (s *Server) preflight(w http.ResponseWriter, _ *http.Request) {
	setCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSDK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, sdkJS)
}

// authorized checks the env write key (constant-time) or any managed key. Open
// only when NO key is configured anywhere (local dev).
func (s *Server) authorized(r *http.Request) bool {
	hasManaged := s.settings != nil && len(s.settings.Keys()) > 0
	if s.writeKey == "" && !hasManaged {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("key") // sendBeacon fallback can't set headers
	}
	if s.writeKey != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.writeKey)) == 1 {
		return true
	}
	return hasManaged && s.settings.ValidKey(got)
}

// keyAuthed is authorized() WITHOUT the open-when-no-keys fallback: it's true only
// when a key is actually configured and this request presented a valid one. Used by
// endpoints that accept key-OR-session — the "no keys configured" open mode must not
// bypass a configured dashboard password.
func (s *Server) keyAuthed(r *http.Request) bool {
	hasManaged := s.settings != nil && len(s.settings.Keys()) > 0
	if s.writeKey == "" && !hasManaged {
		return false
	}
	return s.authorized(r)
}

// handleMCP is the Streamable-HTTP MCP transport: point a remote MCP client
// (Claude, Cursor) at http://host/mcp and it reads this server's live data. When a
// key is configured it's required here too — otherwise a public deploy would leak
// all analytics to anyone. Local/stdio use stays open.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing key — add Authorization: Bearer <key>")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read error")
		return
	}
	status, resp := s.mcp.HTTPDispatch(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(resp)
}

// ingest accepts a single event or an array. Missing ID gets one (so the client
// need not generate it); missing timestamp defaults to now. Idempotent on ID.
// parseUA derives a coarse browser + OS from a User-Agent, dependency-free. It returns
// "" for anything it doesn't recognize (backend HTTP clients, bots), so server-to-server
// events are never mislabeled with a browser they didn't come from.
func parseUA(ua string) (browser, os string) {
	if ua == "" {
		return "", ""
	}
	switch {
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iOS"):
		os = "iOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	switch { // order matters: Edge/Opera/Chrome share the "Chrome" token
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Version/") && strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	return browser, os
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	body, tooLarge, err := readLimited(r, 4<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read error")
		return
	}
	if tooLarge {
		writeErr(w, http.StatusRequestEntityTooLarge, "payload too large — max 4MB per request")
		return
	}
	var batch []event.Event
	if len(body) > 0 && body[0] == '[' {
		// Decode the array element-by-element and stop the instant it exceeds the cap,
		// so a 100k-event body doesn't get fully parsed into a giant slice before the
		// 413 — an abusive/misconfigured client can't buy multi-second parse work per
		// rejected request. Bounds allocation to maxBatchEvents regardless of body size.
		b, overCap, err := decodeBatch(body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON array of events")
			return
		}
		if overCap {
			writeErr(w, http.StatusRequestEntityTooLarge, "too many events in one batch — max 10000")
			return
		}
		batch = b
	} else {
		var one event.Event
		if err := json.Unmarshal(body, &one); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid event JSON")
			return
		}
		batch = []event.Event{one}
	}

	// Bot filtering: crawlers/unfurlers/headless agents inflate every report, so
	// autocaptured web events ($-prefixed) from a bot UA are dropped — counted, never
	// stored. Backend events are exempt (server SDKs legitimately send curl/Go UAs).
	// SMOLANALYTICS_KEEP_BOTS=1 disables the filter.
	if os.Getenv("SMOLANALYTICS_KEEP_BOTS") == "" && botua.IsBot(r.UserAgent()) {
		kept := batch[:0:0]
		dropped := 0
		for _, e := range batch {
			if strings.HasPrefix(e.Name, "$") {
				dropped++
				continue
			}
			kept = append(kept, e)
		}
		if dropped > 0 {
			s.botsFiltered.Add(int64(dropped))
			batch = kept
		}
		if len(batch) == 0 {
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": 0, "bots_filtered": dropped})
			return
		}
	}

	now := time.Now().UTC()
	maxFuture := now.Add(time.Hour) // tolerate client clock skew, no more
	// parse the request UA once — the browser SDK's fetch carries the visitor's UA, so we
	// derive browser + OS server-side with zero SDK weight. Unrecognized (backend/library)
	// UAs return "", so server-to-server events are never stamped with a bogus browser.
	uaBrowser, uaOS := parseUA(r.Header.Get("User-Agent"))
	// geo: one in-memory lookup on the request IP, then the IP is gone. events
	// carry only a country code, same privacy shape as the UA-derived browser/os
	country := ""
	if s.geo != nil {
		country = s.geo.Country(net.ParseIP(clientIP(r)))
	}
	for i := range batch {
		if batch[i].Name == "" {
			writeErr(w, http.StatusBadRequest, "every event needs a name")
			return
		}
		if uaBrowser != "" || uaOS != "" {
			if batch[i].Properties == nil {
				batch[i].Properties = map[string]any{}
			}
			if _, ok := batch[i].Properties["browser"]; !ok && uaBrowser != "" {
				batch[i].Properties["browser"] = uaBrowser
			}
			if _, ok := batch[i].Properties["os"]; !ok && uaOS != "" {
				batch[i].Properties["os"] = uaOS
			}
		}
		if country != "" {
			if batch[i].Properties == nil {
				batch[i].Properties = map[string]any{}
			}
			if _, ok := batch[i].Properties["country"]; !ok {
				batch[i].Properties["country"] = country
			}
		}
		if batch[i].DistinctID == "$anon" {
			// cookieless mode: the SDK stored NOTHING on the device (no consent banner
			// needed), so we derive a daily-rotating anonymous visitor id server-side —
			// stable within a day (sessions/funnels work), unlinkable across days.
			// Plausible's model, made explicit via the $anon sentinel.
			batch[i].DistinctID = s.anonID(r, now)
		}
		// identity stitching: identify() / $create_alias carry the visitor's other ids —
		// record the edge so read-time canonicalization joins the pre-login journey to
		// the account (and PostHog's own person merges survive an import).
		alias.RecordFrom(s.aliases, batch[i])
		if batch[i].DistinctID == "" {
			// silently accepting these would merge every anonymous event into one
			// phantom "user" and quietly corrupt funnels/retention/DAU forever.
			writeErr(w, http.StatusBadRequest, "every event needs a distinct_id (the user/visitor id it belongs to; browsers may send \"$anon\" for cookieless mode)")
			return
		}
		if batch[i].ID == "" {
			batch[i].ID = newID()
		}
		if batch[i].Timestamp.IsZero() {
			batch[i].Timestamp = now
		} else if batch[i].Timestamp.After(maxFuture) {
			// a broken client clock must not plant events in the future — they'd skew
			// every trailing-window report (and lifecycle anchors on the max day seen).
			batch[i].Timestamp = now
		}
	}
	if err := s.store.Ingest(batch...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": len(batch)})
}

// apiFunnel returns a funnel as JSON: ?steps=signup,activate,checkout&window=168h
func (s *Server) apiFunnel(w http.ResponseWriter, r *http.Request) {
	steps := parseSteps(r.URL.Query().Get("steps"))
	if len(steps) < 2 {
		writeErr(w, http.StatusBadRequest, "steps must list at least two event names")
		return
	}
	window, _ := time.ParseDuration(r.URL.Query().Get("window"))
	if window <= 0 {
		window = 7 * 24 * time.Hour // same default as the MCP funnel tool — one question, one answer
	}
	// the funnel options contract (phase 1): order= discipline, exclude= disqualifying
	// events, sf<N>=prop:value per-step filters. Unknown enum values are a 400 naming
	// the valid set (ERRORS-1), never a silently different funnel.
	q := r.URL.Query()
	order, oerr := funnel.ParseOrder(q.Get("order"))
	if oerr != nil {
		writeErr(w, http.StatusBadRequest, oerr.Error())
		return
	}
	opts := funnel.Options{Order: order}
	if ex := q.Get("exclude"); ex != "" {
		for _, name := range strings.Split(ex, "|") {
			if name = strings.TrimSpace(name); name != "" {
				opts.Exclusions = append(opts.Exclusions, name)
			}
		}
	}
	for i := range steps {
		raw := q.Get(fmt.Sprintf("sf%d", i))
		if raw == "" {
			continue
		}
		prop, val, ok := strings.Cut(raw, ":")
		if !ok || prop == "" || val == "" {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("bad step filter sf%d=%q — use sf%d=property:value", i, raw, i))
			return
		}
		if opts.StepFilters == nil {
			opts.StepFilters = make([]map[string]string, len(steps))
		}
		opts.StepFilters[i] = map[string]string{prop: val}
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	// breakdown=source runs the funnel per segment (conversion by property) — the same
	// shape the MCP funnel tool returns, so agreement_test locks the two together.
	if bd := q.Get("breakdown"); bd != "" {
		names := make([]string, len(steps))
		for i, st := range steps {
			names[i] = st.Event
		}
		writeJSON(w, http.StatusOK, map[string]any{"steps": names, "breakdown": bd, "segments": funnel.ComputeBreakdown(evs, steps, window, bd)})
		return
	}
	writeJSON(w, http.StatusOK, funnel.ComputeOpts(evs, steps, window, opts))
}

// --- helpers ---

func parseSteps(q string) []funnel.Step {
	if q == "" {
		return nil
	}
	var steps []funnel.Step
	start := 0
	for i := 0; i <= len(q); i++ {
		if i == len(q) || q[i] == ',' {
			if name := q[start:i]; name != "" {
				steps = append(steps, funnel.Step{Event: name})
			}
			start = i + 1
		}
	}
	return steps
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// bootSalt anchors anonymous ids when no settings store exists (dev/demo) — random
// per process, which is fine there.
var bootSalt = newID()

// anonID derives the cookieless visitor id: HMAC(day-scoped salt, client IP + UA).
// Stable within a UTC day so sessions/funnels work; the salt rotates daily so
// visitors are unlinkable across days and nothing identifying is ever stored —
// that's what makes "no cookie banner" honest. IP and UA never leave this function.
func (s *Server) anonID(r *http.Request, now time.Time) string {
	secret := bootSalt
	if s.settings != nil {
		secret = s.settings.Secret()
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d|%s|%s", now.Unix()/86400, clientIP(r), r.UserAgent())
	return "anon-" + hex.EncodeToString(mac.Sum(nil))[:16]
}

const maxBatchEvents = 10000

// readLimited reads up to limit bytes; tooLarge is true if the body exceeded it,
// so callers can return a clear 413 instead of a misleading JSON-parse 400.
func readLimited(r *http.Request, limit int64) (body []byte, tooLarge bool, err error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > limit {
		return b[:limit], true, nil
	}
	return b, false, nil
}

// decodeBatch stream-decodes a JSON array of events, short-circuiting the moment it
// reads past maxBatchEvents so an oversized batch is rejected without parsing the whole
// thing into memory. overCap is true (with a nil error) when the array has more than the
// cap; err is non-nil only for malformed JSON.
func decodeBatch(body []byte) (batch []event.Event, overCap bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	// consume the opening '['
	if _, err := dec.Token(); err != nil {
		return nil, false, err
	}
	batch = make([]event.Event, 0, 256)
	for dec.More() {
		if len(batch) >= maxBatchEvents {
			return nil, true, nil // over the cap — stop before decoding the rest
		}
		var e event.Event
		if err := dec.Decode(&e); err != nil {
			return nil, false, err
		}
		batch = append(batch, e)
	}
	// consume the closing ']' so trailing garbage after the array is still rejected
	if _, err := dec.Token(); err != nil {
		return nil, false, err
	}
	return batch, false, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// serverError renders a branded HTML 500 for browser-facing routes and logs the
// real error server-side. The raw internal error is NEVER echoed to the client —
// especially on public routes (e.g. the unauthenticated share page), where it would
// leak internals to anyone holding a link. `where` is a short server-side tag for
// the log line; it is not shown to the user.
func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("smolanalytics: %s: %v", where, err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8">`+
		`<title>something went wrong · smolanalytics</title>`+
		`<style>html{background:#0A0A0A;color:#FAFAFA;font-family:ui-monospace,Menlo,monospace}`+
		`body{min-height:100vh;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px}`+
		`a{color:#F5A623;text-decoration:none}.b{font-weight:800;letter-spacing:-.02em;font-size:18px;font-family:Inter,sans-serif}.b i{color:#F5A623;font-style:normal}</style>`+
		`<div class="b">smol<i>analytics</i></div><div style="color:#8E8E8E">500 · something went wrong on our end</div><a href="/">← back to dashboard</a>`)
}

// distinctUsers counts unique DistinctIDs across events.
func distinctUsers(evs []event.Event) int {
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.DistinctID] = true
	}
	return len(seen)
}

// retentionOf is a thin pass-through so the dashboard can call one place.
func retentionOf(evs []event.Event, days int, ev string) retention.Result {
	return retention.Compute(evs, days, ev)
}

func trendOf(evs []event.Event, name string) trends.Result {
	return trends.Compute(evs, name, time.Time{}, time.Time{}, false)
}
