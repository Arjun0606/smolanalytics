package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// GET /v1/backtest — what this would have told you, over history you already lived through.
//
// The CLI has had this since it was built; the HTTP surface did not, which meant the single
// most persuasive artefact the product can produce was reachable only by someone who had
// already installed it. That is backwards: the backtest is the argument for installing.
//
// SAME DEFAULTS AS THE CLI, deliberately. days=90, step=1. A web backtest that swept weekly
// while `smolanalytics backtest` swept daily would report a later FirstSeen for the same
// finding, and the entire claim of this artefact is the date. Two surfaces, one answer.
//
// The cost is real — one full investigation per sweep — so the result is memoized for an
// hour rather than the window being quietly widened. Caching an expensive exact answer is
// honest; cheapening the computation to make the endpoint feel fast is not.

type backtestKey struct {
	days, step int
	day        string // memo expires with the calendar day as well as the TTL
}

type backtestEntry struct {
	rep investigate.Replay
	at  time.Time
}

var (
	backtestMu   sync.Mutex
	backtestMemo = map[backtestKey]backtestEntry{}
	backtestTTL  = time.Hour
	// Sweeps. Reachable on purpose: days caps at 365, so a limit of 400 could never fire and
	// would be a guard in name only. 180 refuses a full-year daily replay over HTTP (ask for
	// step=3, or run the CLI, which has no request timeout to answer to) and leaves the CLI
	// default of 90 days at step 1 comfortably inside.
	backtestLimit = 180
)

func (s *Server) apiBacktest(w http.ResponseWriter, r *http.Request) {
	if s.readsGated() && !s.validSession(r) && !s.keyAuthed(r) {
		writeErr(w, http.StatusUnauthorized, "login or a valid key required")
		return
	}
	days, derr := posIntParam(r, "days", 90, 365)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	step, serr := posIntParam(r, "step", 1, 90)
	if serr != nil {
		writeQueryErr(w, serr)
		return
	}
	if days/step > backtestLimit {
		// Refuse loudly with the arithmetic in the message. A silently-clamped window would
		// return a replay whose From date contradicts the one that was asked for.
		writeErr(w, http.StatusBadRequest, fmt.Sprintf(
			"days=%d at step=%d is %d sweeps, over the %d limit — raise step",
			days, step, days/step, backtestLimit))
		return
	}

	now := time.Now().UTC()
	key := backtestKey{days: days, step: step, day: now.Format("2006-01-02")}
	backtestMu.Lock()
	if e, ok := backtestMemo[key]; ok && now.Sub(e.at) < backtestTTL {
		backtestMu.Unlock()
		writeJSON(w, http.StatusOK, e.rep)
		return
	}
	backtestMu.Unlock()

	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope, same as every other report

	rep := investigate.Backtest(evs, flagsFor(s), investigate.BacktestOpts{
		Opts:       investigate.Opts{Now: now},
		WindowDays: days,
		StepDays:   step,
		// Deploys are what let a replayed finding name the ship that caused it. Filtered to
		// the sweep's own date inside Backtest, so a 14-June sweep cannot cite a July release.
		Deploys: deploysFor(s),
	})

	backtestMu.Lock()
	// Bounded: one entry per (days, step) asked for today, and yesterday's keys dropped.
	for k := range backtestMemo {
		if k.day != key.day {
			delete(backtestMemo, k)
		}
	}
	backtestMemo[key] = backtestEntry{rep: rep, at: now}
	backtestMu.Unlock()

	writeJSON(w, http.StatusOK, rep)
}

// POST /v1/findings/acted — a human says "I did something about this".
//
// The single write the outcome ledger accepts. Session or key auth like the read side of the
// desk: marking a finding acted is an annotation on your own data, not an admin operation, and
// gating it behind an admin key is how the button never gets pressed.
func (s *Server) markActed(w http.ResponseWriter, r *http.Request) {
	if s.readsGated() && !s.validSession(r) && !s.keyAuthed(r) {
		writeErr(w, http.StatusUnauthorized, "login or a valid key required")
		return
	}
	if s.acted == nil {
		writeErr(w, http.StatusServiceUnavailable, "the outcome ledger is not enabled on this instance")
		return
	}
	var req struct {
		Fingerprint string `json:"fingerprint"`
		Note        string `json:"note"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err := json.Unmarshal(body, &req); err != nil || req.Fingerprint == "" {
		writeErr(w, http.StatusBadRequest, `send {"fingerprint": "<kind|event|day>", "note": "optional"}`)
		return
	}
	e, err := s.acted.Mark(req.Fingerprint, req.Note)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("finding.acted", req.Fingerprint)
	writeJSON(w, http.StatusOK, map[string]any{
		"acted": true, "fingerprint": e.Fingerprint, "at": e.At,
		"next": "the desk and the brief now carry this; when the metric recovers, the line upgrades to verified",
	})
}
