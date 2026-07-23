package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Arjun0606/smolanalytics/internal/flag"
)

// Feature flags — boolean + multivariate, with property targeting and percentage rollout,
// evaluated deterministically. Management (list/save/delete) is gated like the rest of /v1:
// GET reads with the read key, POST/DELETE are session-only (the dashboard writes over MCP with
// the read key, mirroring cohorts). Evaluate is the one public path: the SDK holds only the
// write key, so GET /v1/flags/evaluate is write-key authed + CORS'd and returns ONLY the
// resolved key→variant map for the requested user — never the rule definitions.

func (s *Server) listFlags(w http.ResponseWriter, _ *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": s.flags.List()})
}

func (s *Server) saveFlag(w http.ResponseWriter, r *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags not configured")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var f flag.Flag
	if err := json.Unmarshal(body, &f); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid flag JSON")
		return
	}
	saved, err := s.flags.Save(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) deleteFlag(w http.ResponseWriter, r *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags not configured")
		return
	}
	if err := s.flags.Delete(r.PathValue("key")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("key")})
}

// evaluateFlags resolves every enabled flag for one user. GET /v1/flags/evaluate?distinct_id=…
// Optional ?context={json} carries user properties for targeting rules. Returns { flags: {key:
// variant} } containing only the flags that are ON for this user (an off/unmatched flag is
// simply absent, so the SDK's flag(key, default) falls back to the default). Public + CORS so
// the browser SDK (which only ever holds the write key) can call it.
func (s *Server) evaluateFlags(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if !s.ingestAuth(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key — add Authorization: Bearer <write key>")
		return
	}
	if s.flags == nil {
		writeJSON(w, http.StatusOK, map[string]any{"flags": map[string]any{}})
		return
	}
	did := r.URL.Query().Get("distinct_id")
	if did == "" {
		writeErr(w, http.StatusBadRequest, "distinct_id is required")
		return
	}
	var ctx map[string]any
	if c := r.URL.Query().Get("context"); c != "" {
		_ = json.Unmarshal([]byte(c), &ctx) // best-effort; bad context just means no rule matches
	}
	out := map[string]string{}
	for _, f := range s.flags.List() {
		if variant, on := f.Evaluate(did, ctx); on {
			out[f.Key] = variant
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": out})
}
