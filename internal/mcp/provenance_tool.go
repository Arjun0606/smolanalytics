package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/instrument"
)

// Reconciling the CODE against the DATA.
//
// PostHog and Mixpanel see events. They do not see the repository, and they cannot: asking a
// company for repo access is a security review a SaaS analytics vendor does not win. It is not a
// feature they declined to build, it is a door closed by what they are.
//
// It is already open here, because the MCP server runs in the editor where the agent already has
// the code. No new permission, nothing leaving the machine.
//
// Comparing the two sides answers a question neither incumbent can. Every analytics tool can
// tell you an event's count fell. Only one that can also read the code can tell you the call site
// was deleted — and can tell you about tracking that was never written at all, because absence is
// invisible in event data.

type provenance struct {
	Name     string `json:"event"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	LastSeen string `json:"last_seen,omitempty"`
	Count    int    `json:"count_30d"`
	State    string `json:"state"`
	Meaning  string `json:"meaning"`
}

// The three states an event can be in once code and data are compared. The names are written for
// a person reading a tool result, not for a schema.
const (
	stateHealthy = "healthy"
	stateSilent  = "in code, never fires"
	stateOrphan  = "arriving, not in code"
)

func (s *Server) toolEventSource(args json.RawMessage) (string, error) {
	var a struct {
		RepoPath string `json:"repo_path"`
		Event    string `json:"event"`
		Days     int    `json:"days"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if a.RepoPath == "" {
		a.RepoPath = "."
	}
	if a.Days <= 0 {
		a.Days = 30
	}

	inCode := instrument.FindAllTracked(a.RepoPath)

	// What the data actually received, and when it was last seen. Streamed rather than loaded:
	// this is a reconciliation over names, so holding events would be pointless.
	type seen struct {
		count int
		last  time.Time
	}
	inData := map[string]*seen{}
	cutoff := time.Now().UTC().AddDate(0, 0, -a.Days)
	if err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		if e.Timestamp.Before(cutoff) {
			return nil
		}
		d := inData[e.Name]
		if d == nil {
			d = &seen{}
			inData[e.Name] = d
		}
		d.count++
		if e.Timestamp.After(d.last) {
			d.last = e.Timestamp
		}
		return nil
	}); err != nil {
		return "", err
	}

	names := map[string]bool{}
	for n := range inCode {
		names[n] = true
	}
	for n := range inData {
		// Events the SDK emits on its own are not written by anyone, so they can never appear in
		// the repo. Reporting them as untracked code would bury the real findings in noise.
		if strings.HasPrefix(n, "$") {
			continue
		}
		names[n] = true
	}
	if a.Event != "" {
		if !names[a.Event] {
			return "", fmt.Errorf("no event named %q found in the code at %s or in the last %d days of data", a.Event, a.RepoPath, a.Days)
		}
		names = map[string]bool{a.Event: true}
	}

	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	out := make([]provenance, 0, len(ordered))
	var silent, orphan int
	for _, n := range ordered {
		p := provenance{Name: n}
		code, hasCode := inCode[n]
		data, hasData := inData[n]
		if hasCode {
			p.File, p.Line = code.File, code.Line
		}
		if hasData {
			p.Count = data.count
			p.LastSeen = data.last.UTC().Format(time.RFC3339)
		}
		switch {
		case hasCode && hasData:
			p.State = stateHealthy
			p.Meaning = "fired from this call site and arriving normally"
		case hasCode && !hasData:
			p.State = stateSilent
			silent++
			p.Meaning = fmt.Sprintf("the track() call exists at %s:%d but nothing has arrived in %d days. Either the code path never runs, the change was never deployed, or the SDK is not initialised on that page.", code.File, code.Line, a.Days)
		default:
			p.State = stateOrphan
			orphan++
			p.Meaning = "events are arriving but no track() call for this name exists in the repo. Usually a deleted or renamed call site still running in production, or traffic from a different app sharing this write key."
		}
		out = append(out, p)
	}

	res := map[string]any{
		"repo":     a.RepoPath,
		"window":   fmt.Sprintf("%d days", a.Days),
		"events":   out,
		"in_code":  len(inCode),
		"in_data":  len(inData),
		"headline": headline(len(out), silent, orphan),
	}
	return jsonText(res)
}

func headline(total, silent, orphan int) string {
	if total == 0 {
		return "No custom events found in the code or the data. If the app tracks events, check repo_path points at the right directory."
	}
	if silent == 0 && orphan == 0 {
		return fmt.Sprintf("All %d events reconcile: every track() call in the code is arriving, and everything arriving has a call site.", total)
	}
	var parts []string
	if silent > 0 {
		parts = append(parts, fmt.Sprintf("%d written but never arriving", silent))
	}
	if orphan > 0 {
		parts = append(parts, fmt.Sprintf("%d arriving with no call site in the repo", orphan))
	}
	return strings.Join(parts, ", ") + ". These are the ones worth looking at — a count that merely dropped is a symptom, but a missing call site is a cause."
}

var provenanceToolDef = map[string]any{
	"name": "event_source",
	"description": "Reconcile the CODE against the DATA: for each event, where in the repository it is fired from, and whether it is actually arriving. " +
		"Answers three questions no events-only tool can: where does this event come from (file and line, so you can jump straight to it), " +
		"which track() calls exist but never fire (deployed wrong, unreachable code path, or SDK not initialised), " +
		"and which events are arriving with no call site left in the repo (a deleted or renamed call site still live in production). " +
		"Call this when an event looks wrong, before changing instrumentation, and after any refactor that touched tracking. " +
		"Requires the repo on this machine, which is why this works from the editor and not from a hosted dashboard.",
	"inputSchema": obj(map[string]any{
		"repo_path": map[string]any{"type": "string", "description": "Path to the repo root on this machine (default: current directory)"},
		"event":     map[string]any{"type": "string", "description": "Optional: reconcile just this one event instead of all of them"},
		"days":      map[string]any{"type": "integer", "description": "How far back to look for arriving events, default 30"},
	}, nil),
}
