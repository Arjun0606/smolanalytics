package mcp

// The instrumentation tools: this is what makes "your coding agent instruments your app"
// a real capability, not a prompt. The agent (Cursor / Claude Code) calls these over MCP,
// gets deterministic {event, file, line, exact track() snippet} edits, and applies them
// with its own editor — smolanalytics guides, the agent writes. verify_instrumentation
// then cross-references the declared plan against both the code and live traffic, so the
// agent can prove each event is wired AND firing before it calls the job done.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/instrument"
)

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "propose_instrumentation",
			"description": "Read the user's repository and return the exact instrumentation to add: the base autocapture <script> (host + key resolved) and where it goes, plus the custom-event track() calls to insert at the signup / login / checkout / activation call-sites found in the code, each with file, line, the exact snippet, and expected properties. This is how you instrument the app: call this, then APPLY the returned edits with your own editor, then declare them with set_tracking_plan and confirm with verify_instrumentation. Deterministic and re-runnable. Pass the project's host + write key (from the project page / connect) so the snippet is copy-paste ready.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repo root on this machine (default: current directory)"},
					"host":      map[string]any{"type": "string", "description": "The instance URL events are sent to, e.g. https://your-project.fly.dev"},
					"key":       map[string]any{"type": "string", "description": "The write key (public by design; ships in tracked pages)"},
				},
			},
		},
		map[string]any{
			"name":        "suggest_instrumentation_fix",
			"description": "When instrumentation_health reports an event as MISSING (planned but never arriving), call this to get the exact fix: the call-site in the code where that event should fire and the precise track() snippet to insert. Turns 'this event isn't arriving' into an applied patch. Pass the event name.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"event":     map[string]any{"type": "string", "description": "The planned event that isn't arriving"},
					"repo_path": map[string]any{"type": "string", "description": "Path to the repo root (default: current directory)"},
				},
				"required": []string{"event"},
			},
		},
		map[string]any{
			"name":        "regenerate_plan_from_code",
			"description": "Scan the repo for every smolanalytics.track(\"name\") call and return the tracking plan the code implies. Call this after wiring events, then pass the result to set_tracking_plan, so the declared plan always matches the code that implements it (no manual drift).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"repo_path": map[string]any{"type": "string", "description": "Path to the repo root (default: current directory)"}}},
		},
		map[string]any{
			"name":        "verify_instrumentation",
			"description": "Prove the tracking is real: for every event in the tracking plan, cross-reference the code (is there a track() call?) with live traffic (has it fired?) and return a green/red table — FIRING, WIRED (call-site found, no traffic yet: run the app and exercise it), or MISSING (no call-site and no traffic: not wired). Call this after applying instrumentation to confirm the job is done. Pass repo_path so it can read the call-sites.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repo root (default: current directory)"},
				},
			},
		},
	)
}

func (s *Server) callInstrument(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "propose_instrumentation":
		var p struct {
			RepoPath string `json:"repo_path"`
			Host     string `json:"host"`
			Key      string `json:"key"`
		}
		_ = json.Unmarshal(args, &p)
		root := orDot(p.RepoPath)
		if !looksLikeRepo(root) {
			return true, "", notLocalErr(root)
		}
		host := p.Host
		if host == "" {
			host = "<your-instance-host>"
		}
		key := p.Key
		if key == "" {
			key = "<your-write-key>"
		}
		b, _ := json.MarshalIndent(instrument.ProposeResult(root, host, key), "", "  ")
		return true, string(b), nil

	case "suggest_instrumentation_fix":
		var p struct {
			Event    string `json:"event"`
			RepoPath string `json:"repo_path"`
		}
		_ = json.Unmarshal(args, &p)
		if strings.TrimSpace(p.Event) == "" {
			return true, "", fmt.Errorf("event is required — the planned event that isn't arriving")
		}
		if root := orDot(p.RepoPath); !looksLikeRepo(root) {
			return true, "", notLocalErr(root)
		}
		b, _ := json.MarshalIndent(instrument.SuggestFixResult(orDot(p.RepoPath), p.Event), "", "  ")
		return true, string(b), nil

	case "regenerate_plan_from_code":
		var p struct {
			RepoPath string `json:"repo_path"`
		}
		_ = json.Unmarshal(args, &p)
		if root := orDot(p.RepoPath); !looksLikeRepo(root) {
			return true, "", notLocalErr(root)
		}
		found := instrument.FindAllTracked(orDot(p.RepoPath))
		events := make([]map[string]any, 0, len(found))
		for name, loc := range found {
			events = append(events, map[string]any{"name": name, "at": fmt.Sprintf("%s:%d", loc.File, loc.Line)})
		}
		out := map[string]any{
			"events": events,
			"note":   "these are the track() calls in your code. Pass the names to set_tracking_plan to declare them, then plan check gates them in CI.",
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return true, string(b), nil

	case "verify_instrumentation":
		if s.trackplan == nil {
			return true, "", fmt.Errorf(noStore, "tracking-plan")
		}
		plan := s.trackplan.Get()
		if len(plan.Events) == 0 {
			return true, "", fmt.Errorf("no tracking plan declared yet — call propose_instrumentation, apply the edits, then set_tracking_plan; this tool then proves each event is wired and firing")
		}
		var p struct {
			RepoPath string `json:"repo_path"`
		}
		_ = json.Unmarshal(args, &p)
		root := orDot(p.RepoPath)
		// P1-4: verify still reports FIRING from live traffic even without code access,
		// but it must NOT read "no call-site" as MISSING when it simply can't see the
		// code (hosted endpoint / wrong path). Track whether the repo is readable and
		// label the non-firing rows honestly instead of falsely declaring them unwired.
		haveCode := looksLikeRepo(root)

		// firing = the event name appears in stored traffic
		firing := map[string]bool{}
		if names, err := s.store.Names(); err == nil {
			for _, n := range names {
				firing[n] = true
			}
		}
		// wired = a track()/POST call-site exists for the planned name in the code
		planNames := make([]string, len(plan.Events))
		for i, e := range plan.Events {
			planNames[i] = e.Name
		}
		wired := map[string]instrument.TrackedEvent{}
		if haveCode {
			wired = instrument.Wired(root, planNames)
		}

		type row struct {
			Event  string `json:"event"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		}
		var rows []row
		firingN, wiredN, missingN := 0, 0, 0
		for _, e := range plan.Events {
			switch {
			case firing[e.Name]:
				rows = append(rows, row{e.Name, "FIRING", "✓ arriving in traffic"})
				firingN++
			case wired[e.Name].Name != "":
				w := wired[e.Name]
				rows = append(rows, row{e.Name, "WIRED", fmt.Sprintf("track() found at %s:%d but no traffic yet — run the app and exercise this flow", w.File, w.Line)})
				wiredN++
			case !haveCode:
				rows = append(rows, row{e.Name, "NOT FIRING", "no traffic yet, and I can't read your code from here to check the call-site — run the LOCAL smolanalytics MCP in your project to verify it's wired"})
				missingN++
			default:
				rows = append(rows, row{e.Name, "MISSING", "no track() call in code and no traffic — call suggest_instrumentation_fix"})
				missingN++
			}
		}
		note := "FIRING = proven end to end. WIRED = code is there, just exercise the flow. MISSING = not wired; fix it. Re-run after clicking through the app."
		// A typo'd or wrong repo_path must not silently masquerade as "no code access" —
		// otherwise the user thinks they pointed us at their repo and never learns the
		// path was bad. If they explicitly passed a path we couldn't read as a project,
		// say so loudly, with the path, so it's fixable.
		if strings.TrimSpace(p.RepoPath) != "" && !haveCode {
			note = fmt.Sprintf("⚠ repo_path %q doesn't look like a code project (no package.json/go.mod/src/… found there) — the code-side checks were skipped, so any non-FIRING row reflects a bad path, NOT a missing call-site. Fix the path and re-run. "+note, p.RepoPath)
		}
		out := map[string]any{
			"summary": fmt.Sprintf("%d firing, %d wired-not-yet-fired, %d missing (of %d planned)", firingN, wiredN, missingN, len(plan.Events)),
			"events":  rows,
			"note":    note,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return true, string(b), nil
	}
	return false, "", nil
}

// orDot defaults an empty repo path to the current directory (where a stdio MCP server
// runs — i.e. the user's project).
func orDot(p string) string {
	if strings.TrimSpace(p) == "" {
		return "."
	}
	return p
}

// repoMarkers are files/dirs that mark a real code project. Used to detect when a
// repo-scan tool is pointed at something that ISN'T the user's code (e.g. a hosted
// MCP server's own container), so it can say so instead of returning empty-as-success.
var repoMarkers = []string{
	"package.json", "go.mod", "requirements.txt", "pyproject.toml", "Gemfile",
	"composer.json", "Cargo.toml", "pom.xml", "build.gradle", "index.html",
	"src", "app", "pages", "components", ".git",
}

// looksLikeRepo reports whether root plausibly holds a code project. When it
// doesn't, the repo-scan tools return an honest error (P1-4): scanning a hosted
// server's cwd would otherwise report "no track() calls / framework unknown" as a
// successful result, silently telling the user their code has no instrumentation.
func looksLikeRepo(root string) bool {
	for _, m := range repoMarkers {
		if _, err := os.Stat(filepath.Join(root, m)); err == nil {
			return true
		}
	}
	return false
}

// notLocalErr is the honest miss when a repo-scan tool can't see the user's code —
// the repo tools only work where the code lives (the local stdio MCP).
func notLocalErr(root string) error {
	return fmt.Errorf("I can't see your code at %q — no project files there. The repo-scanning tools "+
		"(propose_instrumentation, verify_instrumentation, regenerate_plan_from_code, suggest_instrumentation_fix) "+
		"read your source on the machine where your editor runs, so they need the LOCAL smolanalytics MCP "+
		"(run `npx -y @smolanalytics/mcp` or `smolanalytics mcp` in your project), not the hosted HTTP endpoint. "+
		"Pass an explicit repo_path if your code is elsewhere.", root)
}
