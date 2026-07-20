package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/instrument"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// instrumentCmd is the one-command setup: read the repo, install autocapture, and show
// the exact custom-event track() calls to add — plus write the tracking plan. It injects
// the snippet where it can do so safely (an HTML <head>) and PRINTS the track() edits for
// a human or agent to apply, rather than blindly rewriting app logic with a regex. For
// full auto-instrumentation, connect a coding agent and use propose_instrumentation.
func instrumentCmd(args []string) {
	fs := flag.NewFlagSet("instrument", flag.ExitOnError)
	dir := fs.String("dir", ".", "path to the repo root")
	host := fs.String("host", "", "instance URL events are sent to (e.g. https://your-project.fly.dev)")
	key := fs.String("key", "", "write key (public by design; ships in tracked pages)")
	write := fs.Bool("write", false, "apply: inject the snippet into your HTML <head> and write smolanalytics.plan.json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: smolanalytics instrument [--dir=.] [--host=URL] [--key=KEY] [--write]")
		fmt.Fprintln(os.Stderr, "  reads your repo, installs autocapture, and shows the track() calls to add for signup/checkout/etc.")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	// accept a POSITIONAL path too: `smolanalytics instrument ./myapp` is the intuition
	// every dev has from docker/git/npm. Without this the bare arg was silently dropped and
	// the tool scanned the current directory, confidently proposing tracking for unrelated
	// files with no error — the worst possible first-run impression on the highest-intent action.
	if fs.NArg() > 0 && *dir == "." {
		*dir = fs.Arg(0)
	}

	h, k := *host, *key
	if h == "" {
		h = "<your-instance-host>"
	}
	if k == "" {
		k = "<your-write-key>"
	}
	prop := instrument.Propose(*dir, h, k)

	fmt.Printf("Detected: %s (%s)\n\n", prop.Framework.Name, nonEmpty(prop.Framework.Language, "unknown"))
	fmt.Println("1. Autocapture snippet — pageviews, clicks, and engagement, zero code:")
	if prop.Snippet.File != "" {
		fmt.Printf("   → %s  (%s)\n", prop.Snippet.File, prop.Framework.Install)
	} else {
		fmt.Printf("   → %s\n", prop.Framework.Install)
	}
	fmt.Println(indentBlock(prop.Snippet.Code))

	if len(prop.Events) > 0 {
		fmt.Println("\n2. Custom events found in your code — add these track() calls:")
		for _, e := range prop.Events {
			fmt.Printf("   [%-8s] %s:%d  (%s)\n       %s\n", e.Event, e.File, e.Line, e.Confidence, e.Snippet)
		}
	} else {
		fmt.Println("\n2. No signup/checkout call-sites auto-detected — add track() at your key conversion moments.")
	}
	for _, n := range prop.Notes {
		fmt.Printf("\n   note: %s\n", n)
	}

	if !*write {
		fmt.Println("\nDry run. Re-run with --write to inject the snippet and write smolanalytics.plan.json.")
		if *host == "" || *key == "" {
			fmt.Println("Tip: pass --host and --key (from your project page) for a copy-paste-ready snippet.")
		}
		return
	}

	// --write: inject the snippet where safe, and write the plan.
	if injected := tryInjectSnippet(*dir, prop); injected != "" {
		fmt.Printf("\n✓ injected the snippet into %s\n", injected)
	} else {
		fmt.Printf("\n• snippet not auto-injected (this stack needs a manual paste) — add the snippet above to %s\n",
			nonEmpty(prop.Snippet.File, "your site's <head>"))
	}

	planPath := filepath.Join(*dir, "smolanalytics.plan.json")
	tp, err := trackplan.Open(planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open plan file: %v\n", err)
		return
	}
	var events []trackplan.PlannedEvent
	for _, e := range prop.PlanEvents() {
		props, _ := e["properties"].([]string)
		events = append(events, trackplan.PlannedEvent{Name: e["name"].(string), Properties: props})
	}
	if len(events) > 0 {
		if _, err := tp.Set(events); err != nil {
			fmt.Fprintf(os.Stderr, "could not write plan: %v\n", err)
		} else {
			fmt.Printf("✓ wrote %s with %d event(s)\n", planPath, len(events))
		}
	}
	fmt.Println("\nNext: add the track() calls above, run the app and exercise the flows, then")
	fmt.Println("`smolanalytics plan check` (or verify_instrumentation over MCP) to prove each event fires.")
}

var headCloseRe = regexp.MustCompile(`(?i)</head>`)

// tryInjectSnippet inserts the snippet before </head> in an HTML-like file, idempotently.
// It only touches files with a real </head>, so it can never corrupt app logic. Returns
// the file it edited, or "" when no safe HTML target exists.
func tryInjectSnippet(root string, prop instrument.Proposal) string {
	candidates := []string{prop.Snippet.File, "index.html", "public/index.html",
		"src/app.html", "app/views/layouts/application.html.erb",
		"resources/views/layouts/app.blade.php", "templates/base.html", "templates/layout.html"}
	for _, rel := range candidates {
		if rel == "" {
			continue
		}
		full := filepath.Join(root, rel)
		b, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		src := string(b)
		if !headCloseRe.MatchString(src) {
			continue
		}
		if strings.Contains(src, "/sdk.js") { // already installed — idempotent
			return rel
		}
		out := headCloseRe.ReplaceAllString(src, "  "+strings.ReplaceAll(prop.Snippet.Code, "\n", "\n  ")+"\n</head>")
		// only replace the first occurrence: rebuild with a single replacement
		idx := headCloseRe.FindStringIndex(src)
		if idx != nil {
			out = src[:idx[0]] + "  " + strings.ReplaceAll(prop.Snippet.Code, "\n", "\n  ") + "\n" + src[idx[0]:]
		}
		if err := os.WriteFile(full, []byte(out), 0o644); err == nil {
			return rel
		}
	}
	return ""
}

func indentBlock(s string) string {
	return "     " + strings.ReplaceAll(s, "\n", "\n     ")
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
