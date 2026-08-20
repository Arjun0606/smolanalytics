package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// The flagship, finally reachable from the editor.
//
// The entire positioning says the developer works where their agent already is, and until this
// file the one output the product is named for — the investigation — was browser-only. An agent
// could read every chart through MCP and could not ask for the conclusion. That is the largest
// claim-versus-binary gap the positioning audit found, and it is the first thing an evaluating
// agent would notice: "it says it investigates; the tool list has no investigate."
//
// Two tools, not four. investigate returns the whole desk in one call — findings with causes and
// costs, the kill list, the quarter movements — because an agent stitching three partial tools
// back together is an agent that will misquote one of them. backtest is separate because it is
// expensive and its question ("what would you have told me") is genuinely different.

func (s *Server) toolInvestigate(args json.RawMessage) (string, error) {
	var a struct {
		Days int `json:"days"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.Days <= 0 {
		a.Days = 30
	}
	if a.Days > 90 {
		a.Days = 90 // same clamp as /v1/brief, so the two doors agree
	}
	evs, err := s.all()
	if err != nil {
		return "", err
	}
	// The SAME entry point the dashboard uses — WithContext, with whatever stores this instance
	// has. Not a re-implementation: the editor and the dashboard must be two doors into one
	// computation, or a customer will eventually catch them disagreeing.
	var flags []flag.Flag
	if s.flags != nil {
		flags = s.flags.List()
	}
	var deps []deploys.Deploy
	if s.deploys != nil {
		deps = s.deploys.List()
	}
	now := time.Now().UTC()
	inv := investigate.WithContext(evs, flags, deps, investigate.Opts{
		Now:  now,
		Days: a.Days,
	})
	if s.acted != nil {
		investigate.ApplyActed(inv.Findings, s.acted.Lookup(), now)
	}

	out, err := json.MarshalIndent(map[string]any{
		"investigation": inv,
		"read_me_first": "Findings are ranked by cost — money when the metric carries revenue, people otherwise. " +
			"needs_you=true is worth interrupting a human for; recovered=true is closed work kept for the record. " +
			"cause is computed correlation and says so; never present it as proof. " +
			"A quiet=true result with a populated scanned list is a real answer: nothing moved enough to matter.",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *Server) toolBacktest(args json.RawMessage) (string, error) {
	var a struct {
		Days int `json:"days"`
		Step int `json:"step"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.Days <= 0 {
		a.Days = 90
	}
	if a.Days > 365 {
		a.Days = 365
	}
	if a.Step <= 0 {
		a.Step = 1
	}
	// Same sweep budget as the HTTP endpoint, refused with the same arithmetic rather than
	// silently widened — an agent told "180 sweeps max, raise step" can fix its own call.
	if a.Days/a.Step > 180 {
		return "", fmt.Errorf("days=%d at step=%d is %d sweeps, over the 180 limit — raise step",
			a.Days, a.Step, a.Days/a.Step)
	}
	evs, err := s.all()
	if err != nil {
		return "", err
	}
	evs = applyDefaultScope(evs)
	var flags []flag.Flag
	if s.flags != nil {
		flags = s.flags.List()
	}
	var deps []deploys.Deploy
	if s.deploys != nil {
		deps = s.deploys.List()
	}
	rep := investigate.Backtest(evs, flags, investigate.BacktestOpts{
		Opts:       investigate.Opts{Now: time.Now().UTC()},
		WindowDays: a.Days,
		StepDays:   a.Step,
		Deploys:    deps,
	})
	out, err := json.MarshalIndent(map[string]any{
		"replay": rep,
		"read_me_first": "Each finding is dated by when the sweep would FIRST have surfaced it (first_seen), with the " +
			"detection lag in days. The sweep never sees events after its own date, so lags are honest. " +
			"When relaying this to a human, lead with the gap between first_seen and when they actually noticed — " +
			"that gap is the whole point of the replay.",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var investigateToolDef = map[string]any{
	"name": "investigate",
	"description": "THE FLAGSHIP. Run the investigator and get the whole desk in one call: what changed " +
		"(step changes per metric, ranked by cost in money or people), the segment carrying each change, the ship that " +
		"correlates with it, the kill list (flags/experiments that ran long enough to conclude and did nothing), " +
		"recovered regressions, and the quarter movements (per metric: share of active people then vs now, " +
		"statistically tested across everything shipped). Use this FIRST for any 'how is the product doing', " +
		"'what changed', 'what needs attention', or 'did our shipping work' question — it is the same computation " +
		"the dashboard's desk renders, so the editor and the dashboard can never disagree.",
	"inputSchema": obj(map[string]any{
		"days": map[string]any{"type": "integer", "description": "Lookback for change detection, default 30, max 90"},
	}, []string{}),
}

var backtestToolDef = map[string]any{
	"name": "backtest",
	"description": "Replay the investigator across history the user already lived through: every finding dated by " +
		"the day it would FIRST have been surfaced, with the detection lag. The one artefact that cannot be a mockup — " +
		"computed against their own past, checkable against their own memory. Use when someone asks 'what would this " +
		"have caught', 'was that release worth it', or wants to evaluate the product against a quarter they remember. " +
		"Expensive: one full investigation per sweep. days=90 step=1 by default; max 180 sweeps.",
	"inputSchema": obj(map[string]any{
		"days": map[string]any{"type": "integer", "description": "How far back to replay, default 90, max 365"},
		"step": map[string]any{"type": "integer", "description": "Days between sweeps, default 1"},
	}, []string{}),
}

// mark_finding_acted — the write half of the loop, from the editor.
//
// The agent that just fixed the code is the best-placed writer of this bit: it knows which
// finding it was working from. Mutating, so it is in mutatingTools and the read-only guard
// refuses it on demo instances.
func (s *Server) toolMarkActed(args json.RawMessage) (string, error) {
	var a struct {
		Fingerprint string `json:"fingerprint"`
		Note        string `json:"note"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.Fingerprint == "" {
		return "", fmt.Errorf("fingerprint is required — it is on every finding the investigate tool returns (kind|event|day)")
	}
	if s.acted == nil {
		return "", fmt.Errorf("the outcome ledger is not enabled on this instance")
	}
	e, err := s.acted.Mark(a.Fingerprint, a.Note)
	if err != nil {
		return "", err
	}
	return jsonStr(map[string]any{
		"acted": true, "fingerprint": e.Fingerprint, "at": e.At,
		"next": "when the metric recovers, investigate will report this finding as verified — acted on, then measurably recovered",
	}), nil
}

var markActedToolDef = map[string]any{
	"name": "mark_finding_acted",
	"description": "Record that a finding from the investigate tool was ACTED ON — a fix shipped, a decision made. " +
		"Pass the finding's fingerprint (kind|event|day, present on every investigate result). From then on the desk, " +
		"the brief and investigate all show the acted state, and when the metric recovers the finding upgrades to " +
		"VERIFIED: acted on, then measurably recovered. Call this after you have actually done the work, never before.",
	"inputSchema": obj(map[string]any{
		"fingerprint": map[string]any{"type": "string", "description": "The finding's fingerprint from investigate"},
		"note":        map[string]any{"type": "string", "description": "Optional: what was done, one line"},
	}, []string{"fingerprint"}),
}
