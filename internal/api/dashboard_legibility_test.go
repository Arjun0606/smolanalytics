package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// the raw template, not the rendered page: these are all source-level rules, and reading the
// source means a failure names the line you have to change.
func dashboardTemplateSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatalf("read dashboard template: %v", err)
	}
	return string(src)
}

// The legibility spec's §8 was a list of things I knew were broken and shipped anyway.
// Every one of them passed `go build`, passed every test, and was only ever visible by
// rendering the page — which is exactly why they need to be pinned in text here.

// 1. The type scale. The file's header comment claimed four sizes while 133 declarations
// hardcoded a literal, so the tokens moved and almost nothing on screen moved with them.
func TestNoHardcodedFontSizes(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	// the `font:` shorthand hides a size inside it AND silently resets font-variant-numeric,
	// which is why it is banned outright rather than just checked for a token
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`font-size:\s*[0-9]`),
		regexp.MustCompile(`font:\s*[0-9]+\s+[0-9.]+px`),
	} {
		if m := re.FindAllString(tpl, -1); len(m) > 0 {
			t.Errorf("%d hardcoded font size(s), first %d: %v\n"+
				"every size must read --fs-lbl/--fs-body/--fs-lead/--fs-num, or the scale is a comment, not a rule",
				len(m), min(3, len(m)), m[:min(3, len(m))])
		}
	}
}

// 2. The neutral ladder. --mut2 was 4.22:1 on a card and 3.97:1 while you hovered it, --dis
// was 2.83:1, and both were carrying axis labels, table heads, the footer and links — under
// a :root comment asserting every tone was AA. Text tones are AA now; --dis is not AA and is
// therefore allowed on exactly one thing: a value that does not exist.
func TestDisabledToneCarriesNoText(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	var offenders []string
	for _, line := range strings.Split(tpl, "\n") {
		if !strings.Contains(line, "var(--dis)") {
			continue
		}
		if strings.Contains(line, ".sk.agnull .skv") {
			continue // the one legitimate use: "no value", where nothing is there to read
		}
		offenders = append(offenders, strings.TrimSpace(line))
	}
	if len(offenders) > 0 {
		t.Errorf("--dis is 3.97:1 — below AA — and is carrying text on %d line(s):\n  %s\n"+
			"move it to --mut2 (4.94:1 worst case) or prove nobody has to read it",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// The tone values themselves, so a future edit cannot quietly walk them back down. These are
// the measured worst case, on --s3 #1C1C1C — a hovered pane, the darkest surface any of them
// renders on. AA for normal text is 4.5:1.
func TestReadingTonesAreAA(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	for tone, want := range map[string]string{
		"--sec":  "#B5B5B5", // 8.31:1
		"--mut":  "#9A9A9A", // 6.06:1
		"--mut2": "#8A8A8A", // 4.94:1
	} {
		if !strings.Contains(tpl, tone+":"+want) {
			t.Errorf("%s is no longer %s — recompute its contrast on --s3 before changing it; "+
				"the last three values that lived here were all below AA", tone, want)
		}
	}
	// the select chevron is a data URI and cannot read a custom property, so it is the one
	// colour that will NOT follow --mut2. Pinned here because "it silently didn't follow" is
	// precisely how it drifted last time.
	if !strings.Contains(tpl, "fill='%238A8A8A'") {
		t.Error("the select chevron no longer matches --mut2 (#8A8A8A) — a data URI cannot read the token, so it has to be hand-synced")
	}
}

// 3. Charts. Every per-bar number lives in a .tip that is display:none until hover, which
// removes it from the a11y tree entirely and puts it out of reach on a touchscreen. Each
// chart therefore owes the same numbers as text.
func TestEveryChartHasATextEquivalent(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	bars := strings.Count(tpl, `class="bars`)
	eq := strings.Count(tpl, "charttable") - 3 // 3 of those are the CSS rules
	if eq < bars {
		t.Errorf("%d bar charts in the template but only %d text equivalents — "+
			"a chart whose only reading is a hover tooltip has no reading at all", bars, eq)
	}
	if !strings.Contains(tpl, `class="crawlbars"`) {
		return // pane removed; nothing to assert
	}
	if !strings.Contains(tpl, "fetches per day by purpose") {
		t.Error("the crawl-by-day chart lost its data table — its split was only ever in a title= on a role-less div")
	}
}

// 4. Keyboard. The ask chips are the dashboard's primary call to action and were spans with
// a click handler: no focus, no role, no Enter. The verdict's "why?" and "Show me" were
// hrefless anchors with the same problem.
func TestAskControlsAreOperableByKeyboard(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	chips := regexp.MustCompile(`<span class="chip" data-q="[^"]*"`).FindAllString(tpl, -1)
	if len(chips) == 0 {
		t.Fatal("no ask chips found — did the markup change shape?")
	}
	for _, c := range chips {
		idx := strings.Index(tpl, c)
		tail := tpl[idx : idx+len(c)+64]
		if !strings.Contains(tail, `role="button"`) || !strings.Contains(tail, `tabindex="0"`) {
			t.Errorf("ask chip is not focusable: %s", c)
		}
	}
	for _, sel := range []string{`class="co-go"`, `class="vwhy"`} {
		i := strings.Index(tpl, sel)
		if i < 0 {
			continue
		}
		if !strings.Contains(tpl[i:i+140], `tabindex="0"`) {
			t.Errorf(`%s has no tab stop — it is an <a> with no href, so nothing else gives it one`, sel)
		}
	}
	// Enter/Space have to be wired by hand for role=button on a non-button
	if !strings.Contains(tpl, "function saAsk(") {
		t.Error("saAsk() is gone — the chips' keyboard activation went with it")
	}
}

// A container that scrolls is mouse-operable and nothing else unless it can take focus.
// Handing every wrapper a permanent tabindex is its own keyboard bug, so this is measured
// at runtime; what is pinned here is that the measuring code still exists and still skips
// containers whose contents are already focusable.
func TestScrollContainersGetMeasuredForFocus(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	for _, want := range []string{"function saScrollables(", "data-scrollfocus", `role','region'`} {
		if !strings.Contains(tpl, want) {
			t.Errorf("scroll-container keyboard handling lost %q (WCAG 2.1.1)", want)
		}
	}
	if !strings.Contains(tpl, `el.querySelector('a[href],button,select,input,textarea,summary,[tabindex]:not([tabindex="-1"])')`) {
		t.Error("the already-focusable-children check is gone — every scrollable box will now get a redundant tab stop")
	}
}

// The dashboard is not the only page we serve. Login, settings and the public share page
// each carried their OWN copy of the palette, all three with --mut2 at #6A6A6A — 3.15:1 on
// their darkest surface, worse than the dashboard's was, on the page a customer sends to
// someone else. Three palettes drifting apart is how one of them ends up illegible without
// anyone touching it, so all four now hold the same measured values.
func TestEveryServedPageSharesTheMeasuredLadder(t *testing.T) {
	for _, f := range []string{"login.tmpl.html", "settings.tmpl.html", "share_api.go", "dashboard.tmpl.html"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		s := string(src)
		if !strings.Contains(s, "--mut:#9A9A9A") {
			t.Errorf("%s: --mut is not #9A9A9A (6.06:1 worst case)", f)
		}
		if !strings.Contains(s, "--mut2:#8A8A8A") {
			t.Errorf("%s: --mut2 is not #8A8A8A (4.94:1 worst case)", f)
		}
		// 12px floor, product-wide. The dashboard reads it from a token; these three still
		// carry literals, so the floor is asserted on the number itself.
		if m := regexp.MustCompile(`font-size:(?:[0-9]|1[01])(?:\.[0-9]+)?px`).FindAllString(s, -1); len(m) > 0 {
			t.Errorf("%s: text below the 12px floor: %v", f, m)
		}
	}
}

// Share-of-total rows draw the bar as an absolutely-positioned box across the whole row and
// print the value on top of it. Any bar over ~90% therefore put its 2px end-marker inside
// the digits — measured at 8 of 55 rows on the dashboard, cutting up to 18.7px into the
// number, and on every high referrer on the public share page. Nothing catches this: it is
// not a contrast failure, not clipped text, and not an overflow. The bar needs a track that
// ends where the value column begins.
func TestBarsCannotDrawThroughTheirOwnValue(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	bars := strings.Count(tpl, `class="segbar"`)
	tracks := strings.Count(tpl, `class="segtrack"`)
	if bars != tracks {
		t.Errorf("%d segbars but %d segtracks — an untracked bar spans the value column", bars, tracks)
	}
	if !strings.Contains(tpl, "--seg-num:") || !strings.Contains(tpl, "inset:0 var(--seg-num) 0 0") {
		t.Error("the value-column reservation is gone; the bar's end-marker will land on the digits again")
	}
	share, err := os.ReadFile("share_api.go")
	if err != nil {
		t.Fatalf("read share_api.go: %v", err)
	}
	s := string(share)
	if strings.Count(s, `class="bar"`) != strings.Count(s, `class="track"`) {
		t.Error("share page: a bar is not inside a track")
	}
	if !strings.Contains(s, "--num-col:") {
		t.Error("share page lost its value-column reservation")
	}
}

// A tile grid sized with auto-fit knows how many columns FIT and nothing about how many items
// there are, so six tiles in a five-column track render as five and then one orphan. Measured at
// 1440 — the width most people read this at — on both the KPI row and every .stickrow inside a
// pane. An orphan row is the single loudest "unfinished" signal a dashboard can send, and it was
// on the first screen.
func TestTileGridsCannotOrphanTheLastRow(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	for _, grid := range []string{".kpis", ".stickrow"} {
		// quantity queries: ":has(>:nth-child(N):last-child)" means "exactly N children"
		for _, n := range []int{4, 5, 6, 7, 8} {
			want := fmt.Sprintf("%s:has(>:nth-child(%d):last-child)", grid, n)
			if !strings.Contains(tpl, want) {
				t.Errorf("%s has no column rule for exactly %d tiles, so %d can orphan", grid, n, n)
			}
		}
	}
	// six is the count that actually broke, and three columns is the only even split of it that
	// keeps the tiles readable at this width
	if !strings.Contains(tpl, ".kpis:has(>:nth-child(6):last-child){grid-template-columns:repeat(3,1fr)}") {
		t.Error("six KPI tiles no longer resolve to 3x2 — the 5+1 orphan is back")
	}
	if strings.Contains(tpl, ".stickrow{display:flex") {
		t.Error(".stickrow is content-sized flex again: tiles get different widths, so the numbers stop being comparable")
	}
}

// ONE QUESTION, ONE RULE — for the properties that decide legibility.
//
// Three times in one day a font declaration I had just edited did nothing, because a SECOND rule
// for the same selector, later in the file, redeclared the same property: .pmh set font-size
// twice inside one block, .ask input was declared twice ~750 lines apart, and the site-wide
// anchor reset sat unlayered where it beat every utility. Each time the source read correctly and
// the rendered page disagreed.
//
// This does not forbid multiple rules per selector — a media query legitimately overrides. It
// forbids the same selector declaring `font` or `font-size` more than once at the SAME nesting
// depth, which is never intentional and always invisible.
func TestNoSelectorDeclaresItsFontTwiceAtTopLevel(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	// INDENTATION IS THE DISCRIMINATOR, not position in the file.
	//
	// My first version truncated at the first "@media" — which appears at line 125, ~870 lines
	// above the duplicate it was written to catch. It passed when I reintroduced the exact bug,
	// which makes it worse than no guard: a green test that proves nothing.
	//
	// Top-level rules in this file are indented two spaces; anything inside a media query is
	// indented four or more. So match exactly two, anywhere in the file. Verified by putting the
	// duplicate back and watching this fail.
	// The comma is the point. Without it a grouped selector never matched at all, so the
	// split-the-group logic below had nothing to split and the guard was inert — it passed with
	// the .pane h3 duplicate put back deliberately. Second time one of these was green and
	// useless; both were found the same way, by reintroducing the bug on purpose.
	rule := regexp.MustCompile(`(?m)^  ([.#][A-Za-z0-9_.#,\- >]+?)\{([^}]*)\}`)
	seen := map[string][]string{}
	for _, m := range rule.FindAllStringSubmatch(string(src), -1) {
		group, body := strings.TrimSpace(m[1]), m[2]
		// SPLIT THE GROUP. This compared whole selector LISTS, so `.pane h3` and
		// `.zonelbl,.chiplbl,.gtile .l,.dcard h3,.pane h3` were two different keys and the
		// duplicate between them was invisible — which is exactly how all 33 pane titles came to
		// render as 13px uppercase mono labels while a correct 19px sans rule sat 450 lines above,
		// losing on source order. A selector is a selector wherever it appears in a list.
		for _, sel := range strings.Split(group, ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			if strings.Count(body, "font-size:") > 1 || strings.Count(body, "font:") > 1 {
				t.Errorf("%s declares its font twice inside ONE block; only the last applies", group)
			}
			if strings.Contains(body, "font:") || strings.Contains(body, "font-size:") {
				seen[sel] = append(seen[sel], strings.TrimSpace(body[:min(48, len(body))]))
			}
		}
	}
	for sel, bodies := range seen {
		if len(bodies) > 1 {
			t.Errorf("%s sets its font in %d separate top-level rules — whichever is later wins, "+
				"so editing the other silently does nothing:\n    %s", sel, len(bodies),
				strings.Join(bodies, "\n    "))
		}
	}
}

// EVERY SERVED TEMPLATE'S JAVASCRIPT MUST PARSE.
//
// A CSS @media block sat inside settings.tmpl.html's script element for twelve days. That is a
// JavaScript syntax error, so the ENTIRE 14KB script never parsed and every control on the
// settings page was dead: creating an API key, adding or testing a webhook, creating an alert,
// changing retention, signing out everywhere, and every confirm row. The page rendered perfectly
// and did nothing.
//
// It survived because the failure is silent in exactly the two places nobody looks — the console
// of a settings page, and a template no test had ever parsed. Rendering the page does not catch
// it either; the markup is fine.
//
// Skipped when node is unavailable rather than failing, so the suite still runs in a bare
// container; CI has node.
func TestEveryTemplateScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; this guard needs a JS parser")
	}
	// Non-greedy, and anchored on the closing tag, so one template with several scripts is
	// checked block by block.
	script := regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	for _, f := range []string{"login.tmpl.html", "settings.tmpl.html", "dashboard.tmpl.html"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		blocks := script.FindAllStringSubmatch(string(src), -1)
		if len(blocks) == 0 {
			continue
		}
		for i, b := range blocks {
			body := b[1]
			body = neutraliseTemplate(body)
			tmp := filepath.Join(t.TempDir(), "block.js")
			if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(node, "--check", tmp).CombinedOutput()
			if err != nil {
				t.Errorf("%s: script block %d does not parse — every control it wires is dead:\n%s",
					f, i, out)
			}
		}
	}
}

// neutraliseTemplate turns a Go template's JS block into plain JavaScript for parsing.
//
// A REGEX CANNOT DO THIS, which cost me three attempts. Actions nest —
// `{{range .F}}{{if $i}},{{end}}"{{.X}}"{{end}}` — so a non-greedy match for range..end stops at
// the INNER end and leaves `"0"0`, a syntax error invented by the test rather than present in
// the file. A test that reports faults it created itself is worse than no test: it gets muted.
//
// So: walk the string, track nesting depth of range/if/with/block/define against end, and drop
// whole control-flow spans. What survives is a value interpolation, replaced with 0 — valid both
// bare and inside quotes, which are the only two positions a value appears in JS source.
func neutraliseTemplate(s string) string {
	var out strings.Builder
	depth := 0
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], "{{")
		if j < 0 {
			if depth == 0 {
				out.WriteString(s[i:])
			}
			break
		}
		if depth == 0 {
			out.WriteString(s[i : i+j])
		}
		i += j
		k := strings.Index(s[i:], "}}")
		if k < 0 {
			break // unterminated action; nothing useful left to check
		}
		action := strings.TrimSpace(strings.Trim(s[i+2:i+k], "-"))
		i += k + 2

		switch {
		case strings.HasPrefix(action, "end"):
			if depth > 0 {
				depth--
			}
		case isControl(action):
			depth++
		case depth == 0:
			out.WriteString("0") // a value: valid bare and inside quotes
		}
	}
	return out.String()
}

func isControl(a string) bool {
	for _, kw := range []string{"range", "if", "with", "block", "define"} {
		if a == kw || strings.HasPrefix(a, kw+" ") {
			return true
		}
	}
	return false
}
