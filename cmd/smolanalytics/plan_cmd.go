package main

// `smolanalytics plan` — the tracking plan as a file in the repo, next to the code
// that implements it.
//
//	smolanalytics plan init    write a starter smolanalytics.plan.json
//	smolanalytics plan push    declare the file's plan on a running instance
//	smolanalytics plan pull    write the instance's current plan into the file
//	smolanalytics plan check   verify real traffic against the plan; exit 1 on breakage
//
// The file lives in git, so instrumentation intent is code-reviewed in the same PR
// as the code that implements it, and a coding agent reads it like any other file.
// Every subcommand talks to the running instance over its Streamable-HTTP MCP
// endpoint (POST /mcp) — the exact tools a connected agent calls — one source of
// truth, so the CLI and the agent can never disagree. docs/agents-ci.md has the
// nightly CI recipe.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// planFile is the repo file's shape: trackplan.Plan minus the server-side "updated"
// stamp, which would churn on every pull and turn plan diffs into noise.
type planFile struct {
	Events []trackplan.PlannedEvent `json:"events"`
}

// starterPlan is what `plan init` writes — real enough to push as-is, obvious
// enough to replace.
var starterPlan = planFile{Events: []trackplan.PlannedEvent{{
	Name:        "signup",
	Description: "account created",
	Properties:  []string{"plan"},
}}}

func planCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("plan "+sub, flag.ExitOnError)
	file := fs.String("file", "smolanalytics.plan.json", "plan file (keep it in the repo)")
	host := fs.String("host", "http://localhost:8080", "running smolanalytics server")
	key := fs.String("key", "", "API key (sent as Authorization: Bearer)")
	window := fs.Int("window", 0, "check: only verify events from the last N hours (0 = all time; posthog default 168)")
	asJSON := fs.Bool("json", false, "check: print the raw health payload instead of the report")
	source := fs.String("source", "", `check: verify against "posthog" instead of a smolanalytics server`)
	phKey := fs.String("ph-key", "", "check --source=posthog: PostHog personal API key with the query:read scope")
	phProject := fs.String("ph-project", "", "check --source=posthog: PostHog project id")
	phHost := fs.String("ph-host", "https://us.posthog.com", "check --source=posthog: PostHog API host (EU cloud: https://eu.posthog.com)")
	_ = fs.Parse(args)
	windowSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "window" {
			windowSet = true
		}
	})

	var err error
	switch sub {
	case "init":
		err = runPlanInit(*file, os.Stdout)
	case "push":
		err = runPlanPush(*file, *host, *key, os.Stdout)
	case "pull":
		err = runPlanPull(*file, *host, *key, os.Stdout)
	case "check":
		switch *source {
		case "":
			err = runPlanCheck(*host, *key, *window, *asJSON, os.Stdout)
		case "posthog":
			if !windowSet {
				// all-time is the right default against our own instance (it holds
				// exactly the app's history) but an unbounded scan of a PostHog
				// project is slow and stale; a week of traffic is the honest window.
				*window = 168
			}
			err = runPlanCheckPostHog(*file, *phHost, *phKey, *phProject, *window, *asJSON, os.Stdout)
		default:
			err = fmt.Errorf("unknown --source=%q — supported: posthog (omit the flag to check a smolanalytics server via --host)", *source)
		}
	default:
		if sub != "" && sub != "help" {
			fmt.Fprintf(os.Stderr, "plan: unknown subcommand %q\n\n", sub)
			planUsage(os.Stderr)
			os.Exit(2)
		}
		planUsage(os.Stdout)
		return
	}
	if err != nil {
		log.Fatalf("plan %s: %v", sub, err) // non-zero exit is the CI contract
	}
}

func planUsage(w io.Writer) {
	fmt.Fprintln(w, "smolanalytics plan — the tracking plan as a file in your repo")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  smolanalytics plan init    write a starter smolanalytics.plan.json")
	fmt.Fprintln(w, "  smolanalytics plan push    declare the file's plan on your instance")
	fmt.Fprintln(w, "  smolanalytics plan pull    write the instance's plan into the file")
	fmt.Fprintln(w, "  smolanalytics plan check   verify real traffic matches; exit 1 if not (CI gate)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  --file=PATH    plan file (default smolanalytics.plan.json)")
	fmt.Fprintln(w, "  --host=URL     running server (default http://localhost:8080)")
	fmt.Fprintln(w, "  --key=KEY      API key, sent as Authorization: Bearer")
	fmt.Fprintln(w, "  --window=N     check: only verify the last N hours (nightly CI: 24)")
	fmt.Fprintln(w, "  --json         check: raw health payload instead of the report")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Already on PostHog? `plan check` runs the same gate against your existing")
	fmt.Fprintln(w, "PostHog project — no server, no migration (docs/agents-ci.md):")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  --source=posthog   check the plan file against PostHog's query API")
	fmt.Fprintln(w, "  --ph-key=KEY       personal API key with the query:read scope")
	fmt.Fprintln(w, "  --ph-project=ID    project id (PostHog → Settings → Project)")
	fmt.Fprintln(w, "  --ph-host=URL      default https://us.posthog.com (EU: https://eu.posthog.com)")
	fmt.Fprintln(w, "  --window=N         defaults to 168 (a week) for this source")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commit the file: instrumentation intent gets code-reviewed with the code that")
	fmt.Fprintln(w, "implements it, and `plan check` in CI catches tracking that silently broke.")
	fmt.Fprintln(w, "See docs/agents-ci.md for the copy-paste GitHub Actions job.")
}

func runPlanInit(file string, out io.Writer) error {
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s already exists — edit it, then `smolanalytics plan push`", file)
	}
	if err := writePlanFile(file, starterPlan); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n\nnext:\n", file)
	fmt.Fprintln(out, "  1. edit it — one entry per event your code sends (commit it with the code)")
	fmt.Fprintln(out, "  2. smolanalytics plan push --host=URL --key=KEY    declare it on your instance")
	fmt.Fprintln(out, "  3. smolanalytics plan check --host=URL --key=KEY   verify real traffic matches")
	return nil
}

func runPlanPush(file, host, key string, out io.Writer) error {
	pf, err := readPlanFile(file)
	if err != nil {
		return err
	}
	if err := validatePlanFile(pf); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if _, err := mcpCall(host, key, "set_tracking_plan", map[string]any{"events": pf.Events}); err != nil {
		return err
	}
	fmt.Fprintf(out, "plan pushed: %s\n", plural(len(pf.Events), "event"))
	return nil
}

func runPlanPull(file, host, key string, out io.Writer) error {
	// instrumentation_health carries the declared plan; when none is declared the
	// server's error already says so and points at push — propagate it as exit 1.
	text, err := mcpCall(host, key, "instrumentation_health", map[string]any{})
	if err != nil {
		return err
	}
	var h struct {
		Plan planFile `json:"plan"` // decoding into planFile drops the server's "updated" stamp
	}
	if err := json.Unmarshal([]byte(text), &h); err != nil {
		return fmt.Errorf("unreadable health payload: %w", err)
	}
	if len(h.Plan.Events) == 0 {
		return fmt.Errorf("the server didn't return the plan — upgrade it to a build whose instrumentation_health includes \"plan\"")
	}
	if err := writePlanFile(file, h.Plan); err != nil {
		return err
	}
	fmt.Fprintf(out, "plan written: %s\n", plural(len(h.Plan.Events), "event"))
	return nil
}

// healthReport mirrors the slice of the instrumentation_health payload the report
// renders; extra fields (plan, note) are ignored on purpose.
type healthReport struct {
	Healthy bool `json:"healthy"`
	Planned []struct {
		Event             string   `json:"event"`
		Status            string   `json:"status"`
		Count             int      `json:"count"`
		LastSeen          string   `json:"last_seen"`
		MissingProperties []string `json:"missing_properties"`
	} `json:"planned"`
	UnplannedEvents []string `json:"unplanned_events"`
}

func runPlanCheck(host, key string, windowHours int, asJSON bool, out io.Writer) error {
	args := map[string]any{}
	if windowHours > 0 {
		args["window_hours"] = windowHours
	}
	text, err := mcpCall(host, key, "instrumentation_health", args)
	if err != nil {
		return err
	}
	return renderAndGate(text, asJSON, "", out)
}

// renderAndGate parses a health payload, renders the report (or echoes the raw
// JSON), and returns the CI error when anything planned is broken. Every check
// source (a smolanalytics instance, --source=posthog) funnels through here, so
// the report format and the exit-code contract can never drift between sources.
// note, when set, is one extra informational line under the report.
func renderAndGate(text string, asJSON bool, note string, out io.Writer) error {
	var h healthReport
	if err := json.Unmarshal([]byte(text), &h); err != nil {
		return fmt.Errorf("unreadable health payload: %w", err)
	}
	broken := 0
	if asJSON {
		fmt.Fprintln(out, text)
	} else {
		broken = renderPlanCheck(h, out)
		if note != "" {
			fmt.Fprintf(out, "  %s\n", note)
		}
	}
	// ✗ rows and the payload's healthy verdict must agree; failing on either keeps
	// the exit code honest even if one side drifts.
	if broken > 0 || !h.Healthy {
		return fmt.Errorf("%d of %d planned events broken", brokenCount(h), len(h.Planned))
	}
	if !asJSON {
		fmt.Fprintf(out, "\nall %s verified ✓\n", plural(len(h.Planned), "planned event"))
	}
	return nil
}

// renderPlanCheck prints the per-event report and returns how many rows are ✗.
// Unplanned events are informational only — they never fail the check, because a
// gate that punishes adding tracking teaches people to stop adding tracking.
func renderPlanCheck(h healthReport, out io.Writer) int {
	width := 0
	for _, p := range h.Planned {
		if len(p.Event) > width {
			width = len(p.Event)
		}
	}
	broken := 0
	fmt.Fprintf(out, "tracking plan: %s declared\n\n", plural(len(h.Planned), "event"))
	for _, p := range h.Planned {
		switch {
		case p.Status != "flowing":
			fmt.Fprintf(out, "  ✗ %-*s  planned but never arrived\n", width, p.Event)
			broken++
		case len(p.MissingProperties) > 0:
			fmt.Fprintf(out, "  ✗ %-*s  flowing (%d events) but missing properties: %s\n",
				width, p.Event, p.Count, strings.Join(p.MissingProperties, ", "))
			broken++
		default:
			fmt.Fprintf(out, "  ✓ %-*s  %d events · last seen %s\n", width, p.Event, p.Count, p.LastSeen)
		}
	}
	for _, u := range h.UnplannedEvents {
		fmt.Fprintf(out, "  • %s: seen but not in the plan (informational)\n", u)
	}
	return broken
}

// brokenCount recomputes ✗ rows from the payload so the error message is right
// even on the --json path, where nothing was rendered.
func brokenCount(h healthReport) int {
	n := 0
	for _, p := range h.Planned {
		if p.Status != "flowing" || len(p.MissingProperties) > 0 {
			n++
		}
	}
	return n
}

// --- the plan file ---

func readPlanFile(path string) (planFile, error) {
	var pf planFile
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pf, fmt.Errorf("no %s — run `smolanalytics plan init` to create one", path)
		}
		return pf, err
	}
	if err := json.Unmarshal(b, &pf); err != nil {
		return pf, fmt.Errorf("%s: %v", path, err)
	}
	return pf, nil
}

// validatePlanFile rejects a plan the server would reject (or silently mangle)
// BEFORE anything is sent: no events, a blank name, or the same name twice.
func validatePlanFile(pf planFile) error {
	if len(pf.Events) == 0 {
		return fmt.Errorf("plan has no events — declare at least one")
	}
	seen := make(map[string]bool, len(pf.Events))
	for i, e := range pf.Events {
		if strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("events[%d] has an empty name", i)
		}
		if seen[e.Name] {
			return fmt.Errorf("duplicate event %q — declare each event once", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

// writePlanFile emits pretty JSON with a trailing newline — the file lives in git,
// so it must diff cleanly and satisfy end-of-file linters.
func writePlanFile(path string, pf planFile) error {
	b, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// --- the MCP client ---

// mcpCall performs one JSON-RPC tools/call against the instance's Streamable-HTTP
// MCP endpoint and returns the tool's JSON payload (result.content[0].text). This
// is the same wire a connected coding agent uses — deliberately: if the CLI had its
// own private API, the two could drift apart.
func mcpCall(host, key, tool string, args any) (string, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(host, "/") + "/mcp"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // matches the server's body cap
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s returned %s: %s", url, resp.Status, trimBody(raw))
	}
	var rpc struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return "", fmt.Errorf("%s did not answer MCP JSON-RPC — is that a smolanalytics server? (%s)", url, trimBody(raw))
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("mcp: %s", rpc.Error.Message)
	}
	if len(rpc.Result.Content) == 0 {
		return "", fmt.Errorf("empty MCP response from %s", url)
	}
	if rpc.Result.IsError {
		return "", fmt.Errorf("%s", rpc.Result.Content[0].Text) // the tool's own message, already actionable
	}
	return rpc.Result.Content[0].Text, nil
}

// trimBody keeps error output readable when a proxy answers with an HTML page.
func trimBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// plural: report lines read as prose — "1 event", not "1 events".
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
