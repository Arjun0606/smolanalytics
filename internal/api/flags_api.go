package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/query"
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
	found, err := s.flags.Delete(r.PathValue("key"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeRemoval(w, "deleted", "flag", "key", r.PathValue("key"), found)
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
	// Bucket on a STABLE key, not on distinct_id.
	//
	// distinct_id changes the moment a visitor logs in — identify() replaces the anonymous id
	// with the account id. Bucketing on it means the hash input changes, so the variant changes:
	// the same person sees A before signing up and B afterwards, and their pre-login behaviour
	// stays credited to whichever arm they left. Nothing errors. The experiment quietly reports
	// a mixture of two populations, and signup is usually the exact moment it cares about.
	//
	// bucket_id is written once per browser and never rewritten by identify, so assignment
	// survives login. Falling back to distinct_id keeps older SDKs working unchanged.
	bucketKey := r.URL.Query().Get("bucket_id")
	if bucketKey == "" {
		bucketKey = did
	}
	var ctx map[string]any
	if c := r.URL.Query().Get("context"); c != "" {
		_ = json.Unmarshal([]byte(c), &ctx) // best-effort; bad context just means no rule matches
	}
	out := map[string]string{}
	measured := []string{}
	for _, f := range s.flags.List() {
		if variant, on := f.Evaluate(bucketKey, ctx); on {
			out[f.Key] = variant
			if f.Measured {
				measured = append(measured, f.Key)
			}
		}
	}
	// `measured` tells the SDK which of this user's on-flags to log a $feature_flag_called
	// exposure for (once per session), so only opted-in flags ever add events.
	writeJSON(w, http.StatusOK, map[string]any{"flags": out, "measured": measured})
}

// measureFlag is the A/B read for one flag against a goal event. GET /v1/flags/{key}/measure?event=&days=
// Read-key authed (a report, like every other GET /v1/*), pinned MCP==API by the agreement test.
func (s *Server) measureFlag(w http.ResponseWriter, r *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags not configured")
		return
	}
	goalEvent := r.URL.Query().Get("event")
	if goalEvent == "" {
		writeErr(w, http.StatusBadRequest, "event (the goal metric) is required")
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			days = n
		}
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Production scope: exclude dev-env events by default, IDENTICAL to MCP flag_impact
	// (applyDefaultScope). Without this the /v1 read and the editor's read disagree whenever
	// any event carries env=development — the exact MCP==API break the agreement test guards.
	evs = query.Apply(evs, nil)
	// Look the flag up and resolve `days` to absolute instants HERE, at the boundary. Measure
	// used to do both itself: it never saw the Flag (so control was picked alphabetically and a
	// zero-exposure arm vanished) and it called time.Now() inside the pure computation (so the
	// same events produced a different report every run).
	key := r.PathValue("key")
	f, _ := s.flags.Get(key)
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	rep := flag.MeasureRange(evs, f, goalEvent, from, to)
	rep.Days = days
	writeJSON(w, http.StatusOK, rep)
}

// flagHealth is the sample-ratio-mismatch check for one experiment.
// GET /v1/flags/{key}/health?days=30 — read-key authed, pinned MCP==API by the agreement test.
//
// This is the check you run BEFORE trusting any A/B number. A mismatch means the randomisation
// broke, so the arms differ by whatever broke it rather than by the change being tested, and
// every conversion figure beside it is unreadable. flag.CheckSRM has existed and been correct
// for a while — validated against fifteen published chi-square criticals — and had no HTTP route
// at all, so the dashboard could not show it and the ask bar could not cite it, while the A/B
// report's own note told the reader to go and check it.
func (s *Server) flagHealth(w http.ResponseWriter, r *http.Request) {
	if s.flags == nil {
		writeErr(w, http.StatusServiceUnavailable, "feature flags not configured")
		return
	}
	key := r.PathValue("key")
	f, ok := s.flags.Get(key)
	if !ok {
		writeErr(w, http.StatusNotFound, "no flag named "+key)
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "days must be a non-negative integer (0 = all time)")
			return
		}
		days = n // including an explicit 0, which CheckSRM reads as all history
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Deliberately NOT default-scoped, matching the MCP tool exactly. The split check counts who
	// was actually bucketed, and filtering the population before checking it is how a real
	// mismatch hides behind the very filter that caused it.
	writeJSON(w, http.StatusOK, flag.CheckSRM(evs, f, days))
}
