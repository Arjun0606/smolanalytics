// Package instrument turns "the agent instruments your app" from a pasted prompt into
// a real capability. It reads a repository, detects the framework, finds where the
// tracking snippet belongs, and locates the call-sites that deserve a custom event
// (signup, login, checkout, activate) — returning a structured Proposal of exact
// {event, file, line, track() snippet, properties} edits.
//
// smolanalytics never writes the user's code itself. The coding agent (Cursor / Claude
// Code) calls Propose over MCP, gets these deterministic, diffable edits, and applies
// them with its own editor — guided by us, correct by construction. The same engine
// backs the `smolanalytics instrument` CLI for people who want one command instead of
// an agent. Deterministic and dependency-free: no model call, so it is re-runnable and
// its output can be diff-reviewed.
package instrument

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Framework is the detected stack, and where/how the base snippet is installed.
type Framework struct {
	Name        string `json:"name"`         // next-app, next-pages, react, vue, svelte, express, rails, django, flask, laravel, go, static, unknown
	Language    string `json:"language"`     // typescript, javascript, python, ruby, php, go, html
	Install     string `json:"install"`      // human note: exactly where the snippet goes
	SnippetFile string `json:"snippet_file"` // best-guess file to insert the snippet into ("" if none found)
}

// CallSite is one place the app does something worth a named event, plus the exact
// track() call to insert there.
type CallSite struct {
	Event      string   `json:"event"`
	File       string   `json:"file"` // repo-relative
	Line       int      `json:"line"`
	Context    string   `json:"context"` // the matched source line, trimmed
	Snippet    string   `json:"snippet"` // the exact track() / POST call to add near this line
	Properties []string `json:"properties"`
	Confidence string   `json:"confidence"` // high | medium
}

// SnippetProposal is the base install (autocapture) snippet and where it belongs.
type SnippetProposal struct {
	File string `json:"file"` // where to put it ("" → paste into the site's <head>)
	Code string `json:"code"` // the exact snippet, host + key already resolved
	Note string `json:"note"`
}

// Proposal is the whole instrumentation plan for a repo: the base snippet, the custom
// events found, and the tracking-plan the agent should declare.
type Proposal struct {
	Framework Framework `json:"framework"`
	// Vendor is the analytics SDK this repo already uses, and the one every snippet below is
	// written in. smolanalytics only when the repo has none.
	Vendor  VendorInfo      `json:"vendor"`
	Snippet SnippetProposal `json:"snippet"`
	Events  []CallSite      `json:"events"`
	Notes   []string        `json:"notes"`
}

// skipDir are directories never worth scanning — dependencies, builds, vcs.
var skipDir = map[string]bool{
	"node_modules": true, ".git": true, "vendor": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, "out": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, ".svelte-kit": true, "coverage": true, ".turbo": true, "tmp": true,
}

// sourceExt are the extensions we read when scanning for call-sites.
var sourceExt = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".vue": true, ".svelte": true, ".py": true, ".rb": true, ".go": true, ".php": true,
}

// eventPattern maps a high-signal source pattern to the event it implies. These are
// heuristics, surfaced with confidence and the exact line, for the agent/human to
// confirm — not silent magic. Order matters: first match on a line wins.
var eventPattern = []struct {
	event      string
	desc       string
	props      []string
	confidence string
	re         *regexp.Regexp
}{
	// These require CALL SYNTAX, for the same reason the coverage patterns do.
	//
	// They used to match bare words — `\bcheckout\b`, `\bactivate\b`, `authenticate\b` — and
	// running the two tools against the same repository showed them disagreeing: coverage found
	// 64 real actions while this proposed adding checkout tracking to Funnel.tsx and Flags.tsx,
	// which are dashboard chart components where "checkout" appears as a funnel STEP NAME. An
	// agent that applies that edit instruments a chart, and the person reviewing the PR stops
	// trusting every other suggestion in it.
	{"checkout", "payment or subscription completed", []string{"plan", "amount"}, "high",
		regexp.MustCompile(`(?i)(checkout\.sessions?\.create\s*\(|\bcreateCheckoutSession\s*\(|stripe\.\w+\.\w*create\s*\(|\bcreateSubscription\s*\(|\.subscriptions?\.create\s*\(|\bpaymentIntents?\.create\s*\(|\.charges?\.create\s*\()`)},
	{"signup", "account created", []string{"plan", "source"}, "high",
		regexp.MustCompile(`(?i)(\b(auth\.)?sign[_-]?up(\.\w+)?\s*\(|\bsignUpWith\w*\s*\(|\bcreateUserWith\w*\s*\(|\bcreateUser\s*\(|\bregisterUser\s*\(|\bregisterAccount\s*\(|\.users\.create\s*\(|users?\.insert\s*\()`)},
	{"login", "user signed in", []string{"method"}, "medium",
		regexp.MustCompile(`(?i)(\b(auth\.)?sign[_-]?in(\.\w+)?\s*\(|\bsignInWith\w*\s*\(|\blog[_-]?in\s*\(|\bauthenticate\s*\()`)},
	{"activate", "activation / onboarding milestone", []string{}, "medium",
		regexp.MustCompile(`(?i)(\bactivateAccount\s*\(|\bcompleteOnboarding\s*\(|complete[_-]?setup\s*\(|\bfinishOnboarding\s*\(|\bmarkActivated\s*\()`)},
}

// nonAppFile reports whether a path is test / story / mock / fixture code. A "signup" in
// a login.spec.ts or an "activate" in a mock is noise, not a real user event — this was
// the single biggest source of false positives when the scanner ran on real third-party
// repos, so it never proposes instrumenting them.
func nonAppFile(rel string) bool {
	segs := strings.Split(strings.ToLower(filepath.ToSlash(rel)), "/")
	base := segs[len(segs)-1]
	for _, m := range []string{".test.", ".spec.", ".stories.", ".story.", ".cy.", ".e2e.", ".mock.", ".fixture."} {
		if strings.Contains(base, m) {
			return true
		}
	}
	// Go names test files foo_test.go, which none of the dotted suffixes above catch. Without
	// this, running the scanner on this very repository proposes instrumenting its own test
	// fixtures — the tracking calls inside them are examples, not a product.
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	for _, seg := range segs[:len(segs)-1] {
		switch seg {
		case "__tests__", "__mocks__", "test", "tests", "e2e", "cypress", "playwright",
			"stories", "__fixtures__", "fixtures", "mocks", "spec", ".storybook":
			return true
		}
	}
	return false
}

// DetectFramework inspects the repo's manifests to name the stack and pick the file the
// base snippet belongs in.
func DetectFramework(root string) Framework {
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(root, p))
		return string(b)
	}
	exists := func(p string) bool {
		_, err := os.Stat(filepath.Join(root, p))
		return err == nil
	}
	firstExisting := func(paths ...string) string {
		for _, p := range paths {
			if exists(p) {
				return p
			}
		}
		return ""
	}

	if pkg := read("package.json"); pkg != "" {
		lang := "javascript"
		if exists("tsconfig.json") || strings.Contains(pkg, "typescript") {
			lang = "typescript"
		}
		has := func(dep string) bool { return strings.Contains(pkg, `"`+dep+`"`) }
		switch {
		case has("next"):
			// app router if an app/ dir with a layout exists, else pages router
			if f := firstExisting("app/layout.tsx", "app/layout.jsx", "app/layout.js", "src/app/layout.tsx", "src/app/layout.js"); f != "" {
				return Framework{"next-app", lang, "insert a <Script> (or the async snippet) in the root layout's <body>", f}
			}
			f := firstExisting("pages/_app.tsx", "pages/_app.jsx", "pages/_app.js", "src/pages/_app.tsx", "src/pages/_app.js")
			return Framework{"next-pages", lang, "add the snippet via next/script in _app, or a custom _document <Head>", f}
		case has("nuxt"):
			return Framework{"vue", lang, "add the snippet to nuxt.config (app.head.script) or a plugin", firstExisting("nuxt.config.ts", "nuxt.config.js")}
		case has("vue"):
			return Framework{"vue", lang, "add the <script> to index.html <head>", firstExisting("index.html", "public/index.html")}
		case has("@sveltejs/kit"), has("svelte"):
			return Framework{"svelte", lang, "add the snippet to src/app.html <head>", firstExisting("src/app.html", "app.html")}
		case has("react-native"), has("expo"):
			return Framework{"react-native", lang, "no DOM: call smolanalytics.track()/POST /v1/events on screen changes and key taps", ""}
		case has("express"), has("fastify"), has("koa"), has("@hono/node-server"), has("hono"):
			return Framework{"express", lang, "serve the <script> in your HTML template, and POST /v1/events from server handlers for backend events", firstExisting("index.html", "public/index.html", "views/layout.ejs")}
		case has("react"), has("vite"):
			return Framework{"react", lang, "add the <script> to index.html <head> (or inject once in your entry file)", firstExisting("index.html", "public/index.html", "src/main.tsx", "src/main.jsx")}
		default:
			return Framework{"node", lang, "add the <script> to your HTML <head>, POST /v1/events from the server", firstExisting("index.html", "public/index.html")}
		}
	}
	if read("go.mod") != "" {
		return Framework{"go", "go", "serve the <script> in your HTML templates; POST /v1/events from handlers", firstExisting("templates/layout.html", "templates/base.html", "index.html")}
	}
	if read("Gemfile") != "" {
		return Framework{"rails", "ruby", "add the <script> to app/views/layouts/application.html.erb <head>", firstExisting("app/views/layouts/application.html.erb")}
	}
	if read("requirements.txt") != "" || read("pyproject.toml") != "" || exists("manage.py") {
		if exists("manage.py") {
			return Framework{"django", "python", "add the <script> to your base template <head>; track server events via POST /v1/events", firstExisting("templates/base.html")}
		}
		return Framework{"flask", "python", "add the <script> to your base template <head>; track server events via POST /v1/events", firstExisting("templates/base.html", "templates/layout.html")}
	}
	if read("composer.json") != "" {
		return Framework{"laravel", "php", "add the <script> to resources/views/layouts/app.blade.php <head>", firstExisting("resources/views/layouts/app.blade.php")}
	}
	if f := firstExisting("index.html", "public/index.html"); f != "" {
		return Framework{"static", "html", "add the <script> to <head> of your pages", f}
	}
	return Framework{"unknown", "", "add the <script> to your site's <head>", ""}
}

// isBrowserLang reports whether the SDK's browser idiom is the right one for this codebase.
//
// The vocabulary is set by DetectFramework: typescript, javascript, python, ruby, php, go, html.
// An earlier version of this test compared against "js" and "ts", which are not values it ever
// produces, so every javascript repo took the backend path.
func isBrowserLang(lang string) bool {
	switch lang {
	case "", "javascript", "typescript", "html":
		return true
	}
	return false
}

// webSnippet is the base autocapture install, host + key already resolved so there is
// no placeholder to forget.
func webSnippet(host, key string) string {
	return `<script src="` + host + `/sdk.js"></script>` + "\n" +
		`<script>smolanalytics.init("` + key + `", { host: "` + host + `" });</script>`
}

// trackSnippet renders the exact track() (or server POST) call for an event — REAL,
// paste-ready code with host + key already resolved, never commented pseudocode (the
// tools promise "the exact snippet to insert"; a comment isn't insertable code).
func trackSnippet(fw Framework, v Vendor, host, key, event string, props []string) string {
	if !v.IsOurs() {
		// Their SDK, their idiom. For a backend language we only write the call when we have a
		// confident idiom for that pair; otherwise we fall through to our own POST, which is at
		// least correct code, and Propose adds a note naming what we could not write.
		if isBrowserLang(fw.Language) {
			return v.Call(event, props)
		}
		if call, ok := v.ServerCall(fw.Language, event, props); ok {
			return call
		}
	}
	if fw.Language == "python" {
		return pyPost(host, key, event, props)
	}
	if fw.Language == "ruby" {
		return `Net::HTTP.post(URI("` + host + `/v1/events"),` + "\n" +
			`  { name: "` + event + `", distinct_id: current_user.id, properties: {` + rubyProps(props) + `} }.to_json,` + "\n" +
			`  "Authorization" => "Bearer ` + key + `", "Content-Type" => "application/json")`
	}
	if fw.Language == "go" {
		return `http.Post("` + host + `/v1/events?key=` + key + `", "application/json",` + "\n" +
			`  strings.NewReader(` + "`" + `{"name":"` + event + `","distinct_id":"` + "`" + `+userID+` + "`" + `","properties":{` + goProps(props) + `}}` + "`" + `))`
	}
	// JS/TS (web): the SDK's track()
	return Smolanalytics.Call(event, props)
}

func pyPost(host, key, event string, props []string) string {
	return `requests.post("` + host + `/v1/events",` + "\n" +
		`  headers={"Authorization": "Bearer ` + key + `"},` + "\n" +
		`  json={"name": "` + event + `", "distinct_id": user_id, "properties": {` + pyProps(props) + `}})`
}

// {py,ruby,go}Props render placeholder property keys so the shape is copy-ready; the
// user fills each value. Empty when the event carries no properties.
func pyProps(props []string) string {
	parts := make([]string, len(props))
	for i, p := range props {
		parts[i] = `"` + p + `": None`
	}
	return strings.Join(parts, ", ")
}

func rubyProps(props []string) string {
	parts := make([]string, len(props))
	for i, p := range props {
		parts[i] = p + ": nil"
	}
	return strings.Join(parts, ", ")
}

func goProps(props []string) string {
	parts := make([]string, len(props))
	for i, p := range props {
		parts[i] = `"` + p + `":null`
	}
	return strings.Join(parts, ",")
}

// ScanCallSites walks the repo and returns the call-sites that look like a custom event,
// deduped so the same event doesn't flood the list, capped for a readable proposal.
func ScanCallSites(root, host, key string, fw Framework, v Vendor) []CallSite {
	var sites []CallSite
	perEvent := map[string]int{}
	const maxPerEvent = 4

	// alreadyTracked lets us skip lines that already call track(), so a re-run doesn't
	// propose instrumenting an event that's already wired — in ANY SDK. Recognising only ours
	// meant a PostHog codebase got a duplicate call proposed beside every working one.
	tracked := anyTrack

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if nonAppFile(rel) { // test/story/mock files are noise, not app events
			return nil
		}
		info, e := d.Info()
		if e != nil || info.Size() > 512*1024 { // skip huge/generated files
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		lines := strings.Split(string(b), "\n")
		for i, raw := range lines {
			line := strings.TrimSpace(raw)
			if line == "" || len(line) > 400 || tracked.MatchString(line) {
				continue
			}
			// Already measured a few lines away — in ANY SDK. Checking only this line meant a
			// PostHog signup with posthog.capture() on the next line was reported as a gap and
			// got a duplicate call proposed beside it.
			if ok, _ := coveredNear(lines, i); ok {
				continue
			}
			for _, ep := range eventPattern {
				if perEvent[ep.event] >= maxPerEvent {
					continue
				}
				if ep.re.MatchString(line) && !skipAsNonAction(line, ep.event) {
					sites = append(sites, CallSite{
						Event:      ep.event,
						File:       filepath.ToSlash(rel),
						Line:       i + 1,
						Context:    trimLen(line, 160),
						Snippet:    trackSnippet(fw, v, host, key, ep.event, ep.props),
						Properties: ep.props,
						Confidence: ep.confidence,
					})
					perEvent[ep.event]++
					break // one event per line
				}
			}
		}
		return nil
	})

	// high-confidence first, then by event name, for a stable, readable proposal
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].Confidence != sites[j].Confidence {
			return sites[i].Confidence == "high"
		}
		if sites[i].Event != sites[j].Event {
			return sites[i].Event < sites[j].Event
		}
		return sites[i].File < sites[j].File
	})
	return sites
}

// Propose is the whole engine: detect the stack, build the base snippet with the real
// host+key, scan for custom events, and assemble the tracking-plan advice.
func Propose(root, host, key string) Proposal {
	fw := DetectFramework(root)
	v := DetectVendor(root)
	sites := ScanCallSites(root, host, key, fw, v)

	p := Proposal{
		Framework: fw,
		Vendor:    v.Info(),
		Snippet: SnippetProposal{
			File: fw.SnippetFile,
			Code: webSnippet(host, key),
			Note: fw.Install,
		},
		Events: sites,
	}
	if !v.IsOurs() {
		// They already run an analytics SDK, so there is nothing to install and the snippet
		// section would otherwise be an invitation to adopt a second one. Say what we found and
		// where the work actually is.
		p.Snippet = SnippetProposal{
			Note: v.Name + " is already installed and initialised in this repo, so there is nothing to add. " +
				"Every call below is written in " + v.Name + "'s own idiom and goes to " + v.Name + " — " +
				"smolanalytics maintains the instrumentation, it does not replace your analytics.",
		}
		if !isBrowserLang(fw.Language) {
			if _, ok := v.ServerCall(fw.Language, "x", nil); !ok {
				p.Notes = append(p.Notes, "This is a "+fw.Language+" codebase and we do not have a verified "+
					v.Name+" call for "+fw.Language+", so the snippets below use smolanalytics' HTTP ingest instead. "+
					"Swap them for "+v.Name+"'s server library if you would rather keep everything in one place.")
			}
		}
	} else if fw.Name == "react-native" || fw.Name == "go" || fw.Language == "python" || fw.Language == "ruby" || fw.Language == "php" {
		p.Snippet.Code = webSnippet(host, key) + "\n\n// backend/native: no browser SDK — POST /v1/events with your write key.\n// { \"name\": \"signup\", \"distinct_id\": \"<user id>\", \"properties\": { ... } }"
	}
	if len(sites) == 0 {
		if v.IsOurs() {
			p.Notes = append(p.Notes, "No obvious signup/checkout call-sites found by pattern. Autocapture still records pageviews, clicks, and engagement with zero code once the snippet is installed. Add track() calls at your key conversion moments, then declare them with set_tracking_plan.")
		} else {
			p.Notes = append(p.Notes, "Nothing here is missing tracking that we can spot by pattern — every signup, checkout and invite we found already has a "+v.Name+" call near it. Declare them with set_tracking_plan so we can tell you when one stops firing.")
		}
	} else {
		p.Notes = append(p.Notes, "These are heuristic matches — confirm each before applying. After wiring them, declare the plan with set_tracking_plan and verify with instrumentation_health.")
	}
	if fw.SnippetFile == "" && v.IsOurs() {
		p.Notes = append(p.Notes, "Could not locate a root layout/index.html automatically — paste the snippet into your site's <head>.")
	}
	return p
}

// PlanEvents distills a proposal into the distinct tracking-plan events to declare
// (deduped by name), so the CLI and the agent write the same plan the code implements.
func (p Proposal) PlanEvents() []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	for _, s := range p.Events {
		if seen[s.Event] {
			continue
		}
		seen[s.Event] = true
		out = append(out, map[string]any{"name": s.Event, "properties": s.Properties})
	}
	return out
}

// JSON renders the proposal for the MCP tool result.
func (p Proposal) JSON() string {
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b)
}

// ProposeResult is the full propose_instrumentation payload. Both the MCP tool and the
// cloud stdio proxy return this, so the two surfaces cannot drift.
func ProposeResult(root, host, key string) map[string]any {
	prop := Propose(root, host, key)
	out := map[string]any{
		"framework": prop.Framework,
		"vendor":    prop.Vendor,
		"snippet":   prop.Snippet,
		"events":    prop.Events,
		"plan":      prop.PlanEvents(),
		"notes":     prop.Notes,
	}
	if prop.Vendor.Detected {
		// Nothing to install, so step 1 is not "paste our script" — saying it anyway is how an
		// agent ends up adding a second analytics SDK to a working app.
		out["how_to_apply"] = "This repo already uses " + prop.Vendor.Name + ", so there is nothing to install and " +
			"every snippet below is written in " + prop.Vendor.Name + "'s own idiom. " +
			"1) For each item in `events`, add its `snippet` near the given file:line where that action happens, filling the property values. " +
			"2) Declare them with set_tracking_plan using `plan`, so we can tell you when one stops firing. " +
			"3) Run verify_instrumentation to confirm each is wired. Do NOT add a smolanalytics track() call beside an existing " +
			prop.Vendor.Name + " one: two SDKs on one action double-counts it."
	} else {
		out["how_to_apply"] = "1) Insert the snippet from `snippet` into the file it names (autocapture starts immediately — pageviews, clicks, engagement, zero code). " +
			"2) For each item in `events`, add the `snippet` near the given file:line where that action happens, filling the property values. " +
			"3) Declare them with set_tracking_plan using `plan`. 4) Run verify_instrumentation to confirm each is wired and firing."
	}
	if !prop.Vendor.Detected && (strings.Contains(host, "<") || strings.Contains(key, "<")) {
		out["heads_up"] = "host and/or key were not provided, so the snippet has placeholders. Pass the project's real host + write key (from the project page or `smolanalytics connect`) to get a copy-paste-ready snippet."
	}
	return out
}

// SuggestFixResult is the suggest_instrumentation_fix payload: the call-site(s) for one
// event and the exact snippet to add there.
func SuggestFixResult(root, event string) map[string]any {
	prop := Propose(root, "<your-instance-host>", "<your-write-key>")
	v := DetectVendor(root)
	var matches []CallSite
	for _, cs := range prop.Events {
		if strings.EqualFold(cs.Event, event) {
			matches = append(matches, cs)
		}
	}
	out := map[string]any{"event": event, "vendor": v.Info()}
	if len(matches) > 0 {
		out["found_call_sites"] = matches
		out["fix"] = "Add the shown snippet at (one of) these call-sites, then re-run the app and verify_instrumentation."
	} else {
		out["found_call_sites"] = []any{}
		// The suggested call has to be in the SDK this repo actually runs, or the fix is an
		// instruction to install a second analytics tool to repair the first one.
		out["fix"] = "No obvious call-site was found by pattern. Add " + v.Call(event, nil) +
			" at the exact point the action happens, then verify_instrumentation."
		if v.IsOurs() {
			out["fix"] = "No obvious call-site was found by pattern. Add " + v.Call(event, nil) +
				" (web) or a POST /v1/events with that name (backend) at the exact point the action happens, then verify_instrumentation."
		}
	}
	return out
}

// FindAllTracked scans the repo for every tracking call in ANY supported SDK — posthog.capture,
// mixpanel.track, amplitude.track, gtag("event", …), analytics.track, smolanalytics.track — and
// returns each event name -> where it was first found. Backs `plan sync`: regenerate the tracking
// plan from the code that actually implements it, so the two can't drift.
func FindAllTracked(root string) map[string]TrackedEvent {
	found := map[string]TrackedEvent{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if info, e := d.Info(); e != nil || info.Size() > 512*1024 {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			if name, ok := EventNameOn(line); ok {
				if !strings.HasPrefix(name, "$") { // skip autocapture names
					if _, seen := found[name]; !seen {
						found[name] = TrackedEvent{Name: name, File: filepath.ToSlash(rel), Line: i + 1}
					}
				}
			}
		}
		return nil
	})
	return found
}

// TrackedEvent is an event name found already wired in the code, and where.
type TrackedEvent struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

var backtick = "`"

var nameFieldRe = regexp.MustCompile(`["']?name["']?\s*[:=]\s*["'` + backtick + `]([^"'` + backtick + `]+)`)

// Wired scans the repo for each of the given event names and returns where it is already
// instrumented — a tracking call in any supported SDK, or a name field on a line that also
// mentions /v1/events. It lets the verify loop distinguish "wired but not yet fired" from
// "not wired at all", and backs the code-side drift gate. Deterministic, no model.
func Wired(root string, names []string) map[string]TrackedEvent {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	found := map[string]TrackedEvent{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if info, e := d.Info(); e != nil || info.Size() > 512*1024 {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			record := func(name string) {
				if want[name] {
					if _, seen := found[name]; !seen {
						found[name] = TrackedEvent{Name: name, File: filepath.ToSlash(rel), Line: i + 1}
					}
				}
			}
			if name, ok := EventNameOn(line); ok {
				record(name)
			}
			// a bare name field only counts as instrumentation on a line that is plausibly
			// an events POST (avoids matching every object with a `name:` key)
			if strings.Contains(line, "/v1/events") || strings.Contains(line, `"name"`) && strings.Contains(strings.ToLower(line), "event") {
				if m := nameFieldRe.FindStringSubmatch(line); m != nil {
					record(m[1])
				}
			}
		}
		return nil
	})
	return found
}

func trimLen(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
