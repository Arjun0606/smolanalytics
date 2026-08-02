package api

import (
	"os"
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
