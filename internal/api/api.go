// Package api serves the single-binary HTTP surface: event ingestion + the
// server-rendered dashboard. No web framework, no SPA build step — the whole UI is
// embedded in the binary and rendered fast on the server (the speed IS a feature).
package api

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

//go:embed sdk.js
var sdkJS string

// Version is the build version (overridable at build time via -ldflags).
var Version = "0.1.0"

type Server struct {
	store    store.Store
	mcp      *mcp.Server
	insights *insights.Store
	writeKey string // if set, POST /v1/events requires Authorization: Bearer <writeKey>
}

func New(s store.Store) *Server {
	ins, _ := insights.Open("") // in-memory by default; SetInsights adds persistence
	return &Server{store: s, mcp: mcp.New(s), insights: ins}
}

// SetInsights swaps in a persistent saved-reports store.
func (s *Server) SetInsights(st *insights.Store) { s.insights = st }

// SetWriteKey gates event ingestion behind a write key (production). Empty = open
// (dev). The SDK passes the same key.
func (s *Server) SetWriteKey(k string) { s.writeKey = k }

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
	mux.HandleFunc("GET /v1/meta", s.apiMeta)
	mux.HandleFunc("GET /v1/events/recent", s.recentEvents)
	mux.HandleFunc("GET /v1/users/{id}", s.userActivity)
	mux.HandleFunc("GET /v1/export", s.export)
	mux.HandleFunc("GET /v1/insights", s.listInsights)
	mux.HandleFunc("POST /v1/insights", s.saveInsight)
	mux.HandleFunc("DELETE /v1/insights/{id}", s.deleteInsight)
	mux.HandleFunc("POST /mcp", s.handleMCP)
	mux.HandleFunc("GET /", s.dashboard)
	return recoverMW(mux)
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

// authorized checks the write key (constant-time). Open when no key is configured.
func (s *Server) authorized(r *http.Request) bool {
	if s.writeKey == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("key") // sendBeacon fallback can't set headers
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.writeKey)) == 1
}

// handleMCP is the Streamable-HTTP MCP transport: point a remote MCP client
// (Claude, Cursor) at http://host/mcp and it reads this server's live data.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
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
func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read error")
		return
	}
	var batch []event.Event
	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &batch); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON array of events")
			return
		}
	} else {
		var one event.Event
		if err := json.Unmarshal(body, &one); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid event JSON")
			return
		}
		batch = []event.Event{one}
	}
	now := time.Now().UTC()
	for i := range batch {
		if batch[i].Name == "" {
			writeErr(w, http.StatusBadRequest, "every event needs a name")
			return
		}
		if batch[i].ID == "" {
			batch[i].ID = newID()
		}
		if batch[i].Timestamp.IsZero() {
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
	evs, err := s.filtered(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, funnel.Compute(evs, steps, window))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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
