package api

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A NAV THAT DOES NOT NAVIGATE.
//
// One-view mode hides every pane outside the current section with display:none. scrollIntoView on
// a display:none element does nothing — no scroll, no error, no feedback of any kind. So every
// rail entry pointing at a report in another section was a dead click, which was most of the rail
// most of the time, and it shipped.
//
// It is a worse failure than a wrong number. A number you distrust, you can open and check. A
// control that silently ignores you teaches the reader the whole product is broken, and gives them
// nothing to check.
//
// These are source-level assertions rather than a rendered-click test because the failure lives in
// the ordering of two JS calls, and no amount of looking at the page reveals it — the markup is
// perfect, the handler fires, the target exists, and nothing happens.

func dashSource(t *testing.T) string {
	t.Helper()
	return dashboardTemplateSource(t)
}

// The jump helpers must reveal before they scroll.
func TestRailJumpRevealsTheTargetBeforeScrolling(t *testing.T) {
	src := dashSource(t)

	for _, fn := range []string{"saJumpToPane", "saJumpToSection"} {
		i := strings.Index(src, "function "+fn+"(")
		if i < 0 {
			t.Fatalf("%s no longer exists — the rail's click path changed and this guard is stale", fn)
		}
		body := src[i:]
		if end := strings.Index(body[1:], "\n    function "); end > 0 {
			body = body[:end]
		}
		reveal := strings.Index(body, "saReveal(")
		scroll := strings.Index(body, "saScrollTo(")
		if reveal < 0 {
			t.Errorf("%s scrolls without revealing: in one-view mode the target is display:none and "+
				"the click does nothing at all", fn)
			continue
		}
		if scroll >= 0 && reveal > scroll {
			t.Errorf("%s scrolls before revealing — scrollIntoView on a hidden element is a no-op, so "+
				"the order is the whole fix", fn)
		}
	}
}

// saReveal has to switch the view, not merely detect that it should.
func TestRevealActuallySwitchesTheView(t *testing.T) {
	src := dashSource(t)
	i := strings.Index(src, "function saReveal(")
	if i < 0 {
		t.Fatal("saReveal is gone — rail clicks are dead again in one-view mode")
	}
	body := src[i : i+1400]
	if !strings.Contains(body, "saSetView(") {
		t.Error("saReveal never calls saSetView, so it cannot make a hidden pane visible")
	}
	if !strings.Contains(body, "data-stratum") {
		t.Error("saReveal must find the target's owning section to know which view to switch to")
	}
}

// Every rail entry points at a pane id that exists on the page. The rail is built from the panes
// that rendered, so this pins that it stays that way.
func TestRailOnlyLinksToPanesThatExist(t *testing.T) {
	src := dashSource(t)
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="(pane-[a-z0-9-]+)"`).FindAllStringSubmatch(src, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("no panes found in the template")
	}
	// The builder emits data-pane from p.id, so anything hardcoded elsewhere is the risk.
	for _, m := range regexp.MustCompile(`data-pane="(pane-[a-z0-9-]+)"`).FindAllStringSubmatch(src, -1) {
		if !ids[m[1]] {
			t.Errorf("the rail links to %s, which is not a pane on this page", m[1])
		}
	}
}

// No heading may show a raw autocapture event name. The rail labels come from the pane headings,
// so "$pageview by channel" reached the nav — the one place a reader meets the raw name with no
// context to decode it, while every other surface calls it "page view".
func TestNoHeadingLeaksARawAutocaptureName(t *testing.T) {
	src := dashSource(t)
	for _, m := range regexp.MustCompile(`<h3[^>]*>([^<]{0,80})`).FindAllStringSubmatch(src, -1) {
		if strings.Contains(m[1], "$") && !strings.Contains(m[1], "{{") {
			t.Errorf("a heading shows a raw event name: %q — run it through EventLabel", strings.TrimSpace(m[1]))
		}
	}
	// and the one that actually shipped
	if strings.Contains(src, `trendEvent + " by channel"`) {
		t.Error("the channel heading concatenates the raw event name again")
	}
}

// The dashboard handler builds SourceTitle; it must be a label, not a raw name.
func TestChannelHeadingIsLabelled(t *testing.T) {
	got := dashboardGoSource(t)
	if !strings.Contains(got, `EventLabel(trendEvent) + " by channel"`) {
		t.Error("SourceTitle must use EventLabel — otherwise autocapture names like $pageview " +
			"appear verbatim in the reports rail")
	}
}

// TWO PANES CANNOT SHARE AN ORDER.
//
// pane-deploys and pane-explain were both order:610, so which appeared first was decided by
// document order rather than by anyone's intent — and the deck menu, the rail and the printed page
// could each have resolved it differently. A layout whose sequence is accidental is one nobody can
// reason about, and it is invisible until someone notices two cards swapped between reloads.
func TestNoTwoPanesShareAnOrder(t *testing.T) {
	src := dashSource(t)
	re := regexp.MustCompile(`id="(pane-[a-z0-9-]+)"\s+style="order:(\d+)"`)
	byOrder := map[string][]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		byOrder[m[2]] = append(byOrder[m[2]], m[1])
	}
	// Panes that share an order DELIBERATELY because they sit in opposite branches of the same
	// {{if}} and can never render together. Listed by name rather than detected, so adding a pair
	// is a decision someone writes down instead of a collision that slips through.
	exempt := map[string]bool{"pane-pages|pane-web-empty": true}

	for order, ids := range byOrder {
		uniq := map[string]bool{}
		for _, id := range ids {
			uniq[id] = true
		}
		if len(uniq) > 1 {
			names := make([]string, 0, len(uniq))
			for id := range uniq {
				names = append(names, id)
			}
			sort.Strings(names)
			if exempt[strings.Join(names, "|")] {
				continue
			}
			t.Errorf("order:%s is claimed by %v — their relative position is undefined", order, names)
		}
	}
}

// EVERY COMPUTED SECTION REACHES EVERY SURFACE.
//
// The Investigation carries findings AND movements. I shipped movements to the share page and the
// CLI brief and forgot the dashboard, so the product told a forwarded stranger something it would
// not tell the person who owned the account. That is the same one-question-two-answers defect this
// codebase keeps producing, wearing a new hat.
//
// Source-level because it is a wiring property: each surface either references the section or it
// does not, and no rendered test would notice the absence — a page missing a card looks exactly
// like a page whose card had nothing to say.
func TestEverySurfaceRendersBothHalvesOfTheInvestigation(t *testing.T) {
	surfaces := map[string]string{
		"dashboard":  dashSource(t),
		"share page": readFileForTest(t, "share_api.go"),
		"cli brief":  readFileForTest(t, "../brief/brief.go"),
	}
	for name, src := range surfaces {
		if !strings.Contains(src, "Movements") {
			t.Errorf("%s never renders Movements — it shows what changed this week and hides "+
				"whether any of it added up", name)
		}
		if !strings.Contains(src, "Findings") {
			t.Errorf("%s never renders Findings", name)
		}
	}
}

// NO SURFACE MAY RENDER A COST ITS OWN WAY.
//
// Four surfaces — the CLI brief, the share page, the dashboard and the backtest output — each
// formatted Cost by hand, and all four printed only People. That was invisible for as long as
// UsdPerMonth was never set: the day it became reachable, it would have appeared on zero of them,
// and the feature would have been "shipped" and unfindable. Cost.Size() is now the one renderer,
// and this forbids anyone reintroducing a hand-rolled one.
//
// Source-level, because no rendered assertion can catch it: a surface printing only the people
// half looks exactly like a surface whose finding had no money on it.
func TestNoSurfaceFormatsACostByHand(t *testing.T) {
	// Files that render a finding's cost to a human, and the repo-relative path of each.
	surfaces := []string{
		"../brief/brief.go",
		"share_api.go",
		"dashboard.tmpl.html",
		"../../cmd/smolanalytics/backtest_cmd.go",
	}
	// The tell: reaching into the struct's fields in a formatting context instead of calling Size.
	banned := []string{
		"Cost.People}}",       // template interpolation of the raw field
		"Cost.UsdPerMonth}}",  //
		"f.Cost.People)",      // Go formatting of the raw field
		"f.Cost.UsdPerMonth)", //
	}
	for _, rel := range surfaces {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("cannot read %s, which this guard exists to watch: %v", rel, err)
		}
		src := string(b)
		if !strings.Contains(src, "Cost.Size") {
			t.Errorf("%s renders findings but never calls Cost.Size() — it is formatting the cost "+
				"itself, which is how four surfaces came to print only the people half", rel)
		}
		for _, bad := range banned {
			if strings.Contains(src, bad) {
				t.Errorf("%s formats %q by hand; use Cost.Size() so every surface says the same thing", rel, bad)
			}
		}
	}
}

// THE SLAB'S PRIMARY ACTION MUST BE WIRED.
//
// It is the largest, highest-contrast control on the first screen. A button that looks like the
// most important thing on the page and silently ignores a click is worse than no button, and this
// codebase has shipped exactly that shape before — 31 rail links that scrolled nowhere.
//
// Source-level, because a rendered assertion cannot tell a handler that never fires from one that
// fires and finds nothing to do.
func TestTheSlabPrimaryActionIsWired(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, `class="slab-go"`) {
		t.Fatal("no .slab-go in the template; this guard is watching something that no longer exists")
	}
	if !strings.Contains(s, ".slab-go[data-q]") {
		t.Error("the slab's primary action is not in the ASKABLE selector list, so clicking it " +
			"does nothing at all")
	}
	// And the readout must come from the one renderer, not from formatting the field here.
	if strings.Contains(s, "comma $lead.Cost.People") || strings.Contains(s, "$lead.Cost.People}}") {
		t.Error("the slab formats Cost.People itself; use Cost.Readout so the figure has one definition")
	}
}

// A PANE THAT IGNORES THE RANGE CONTROL MUST SAY SO.
//
// The toolbar shows a range and the reader reasonably assumes every pane honours it. Several do
// not, and said nothing: goals is hardcoded to 30 days (dashboard.go:2479), lifecycle to 14
// (dashboard.go:1320), and accounts / people / retention read `evs`, which is
// store.Scan(zero, zero) filtered by chips only — all recorded history.
//
// Silence means "the toolbar applies", so silence from a pane that ignores it is a false
// statement made by omission, on a product whose entire pitch is that its numbers are traceable.
//
// The worst was sql, whose subtitle promised "the same production scope as every card above, so
// it can never disagree with them" while applying no window at all — in the one pane a sceptic
// would use to check the others.
func TestPanesThatIgnoreTheRangeSaySo(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	// pane id -> a phrase its subtitle must contain, because its window is not the toolbar's.
	must := map[string]string{
		"pane-goals":      "not the range selected above",
		"pane-lifecycle":  "not the range selected above",
		"pane-accounts":   "not the range selected above",
		"pane-people":     "not the range selected above",
		"pane-stickiness": "does not apply",
		"pane-sql":        "does NOT apply the range",
	}
	for id, phrase := range must {
		i := strings.Index(s, `id="`+id+`"`)
		if i < 0 {
			t.Errorf("%s no longer exists; this guard is watching nothing", id)
			continue
		}
		// the pane's own header, up to the end of its <h3>
		end := strings.Index(s[i:], "</h3>")
		if end < 0 || !strings.Contains(s[i:i+end], phrase) {
			t.Errorf("%s answers over a different window than the toolbar shows and does not say so "+
				"(expected its subtitle to contain %q)", id, phrase)
		}
	}
	// And the sentence that was actively false must not come back.
	if strings.Contains(s, "so it can never disagree with them") {
		t.Error("the sql pane again claims it can never disagree with the cards above, while " +
			"applying no time window at all")
	}
}

// NO PANE MAY DELETE ITSELF.
//
// Five panes were wrapped in a data guard with no else branch, so on an instance with no traffic
// the whole card vanished from the grid. Every new instance starts empty, which means the first
// dashboard every customer ever saw was a grid with holes in it — and a hole is indistinguishable
// from a feature that does not exist. The populated state is the one we all look at; the empty
// state is the one every customer sees first.
//
// TWO WRONG VERSIONS OF THIS TEST CAME FIRST, and both were wrong in an instructive way.
// Looking for an {{else}} "near" the pane reported three false positives, because those panes
// have an else that sits AFTER their closing div. Walking to the first {{end}} reported every
// pane, because it found the PAGE-level wrapper rather than the pane's own guard.
//
// The property that actually distinguishes them: a pane's own guard wraps EXACTLY ONE pane.
// Anything wrapping several is a section or page wrapper, and its else is not this pane's
// business.
func TestNoPaneDeletesItselfWhenEmpty(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	tok := regexp.MustCompile(`\{\{-?\s*(range|if|with|block|define|end|else)\b`)
	pane := regexp.MustCompile(`<div class="pane[^"]*" id="(pane-[a-z0-9-]+)"`)

	for _, pm := range pane.FindAllStringSubmatchIndex(s, -1) {
		id, divPos := s[pm[2]:pm[3]], pm[0]

		// innermost unclosed control token before this pane
		type open struct{ pos int }
		var stack []open
		for _, m := range tok.FindAllStringSubmatchIndex(s[:divPos], -1) {
			switch s[m[2]:m[3]] {
			case "end":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case "else":
			default:
				stack = append(stack, open{m[0]})
			}
		}
		if len(stack) == 0 {
			continue
		}
		gPos := stack[len(stack)-1].pos
		close := strings.Index(s[gPos:], "}}")
		if close < 0 {
			continue
		}

		// forward to that guard's matching end, noting a depth-0 else on the way
		j, d, hasElse, end := gPos+close+2, 0, false, -1
		for j < len(s) {
			m := tok.FindStringSubmatchIndex(s[j:])
			if m == nil {
				break
			}
			kind, at := s[j+m[2]:j+m[3]], j+m[0]
			if kind == "end" {
				if d == 0 {
					end = at
					break
				}
				d--
			} else if kind == "else" {
				if d == 0 {
					hasElse = true
				}
			} else {
				d++
			}
			j = at + (m[1] - m[0])
		}
		if end < 0 {
			continue
		}
		// A guard wrapping more than one pane is a section wrapper, not this pane's guard.
		if len(pane.FindAllString(s[gPos:end], -1)) != 1 || hasElse {
			continue
		}
		t.Errorf("%s renders NOTHING when its data guard is false — the card vanishes from the grid, "+
			"and a hole is indistinguishable from a feature that was never built. Give it an "+
			"{{else}} carrying a title, one sentence saying what would fill it, and the hint.", id)
	}
}

// A FILTER THAT MATCHED NOTHING IS NOT A FRESH INSTALL.
//
// HasData is computed AFTER the scope filters, so a chip matching zero rows told a customer with
// millions of events "reports appear here as soon as your first events land" — and hid the scope
// block, which is where the chip that caused it lives. The only way back was editing the URL.
//
// Stripe states the rule for this outright: do not show a create-first-item call to action when
// items exist but are filtered out. Two states, two messages, and the way out stays on screen.
func TestFilteredToNothingIsNotConfusedWithAFreshInstall(t *testing.T) {
	tmpl, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(tmpl)

	// the scope block survives an empty result set, or the filter cannot be cleared
	if !strings.Contains(s, `{{if or .HasData .EverHadData}}<div class="toolbar">`) {
		t.Error("the scope toolbar is gated on HasData alone, so a filter matching nothing hides " +
			"the chips that caused it and there is no way back except editing the URL")
	}
	// and the fresh-install copy is not shown to an instance that has data
	i := strings.Index(s, "reports appear here as soon as your first events land")
	if i < 0 {
		t.Fatal("the fresh-install line is gone entirely; this guard is watching nothing")
	}
	if !strings.Contains(s[max(0, i-400):i], ".EverHadData") {
		t.Error("the fresh-install line is not gated on EverHadData, so an instance with events " +
			"is told it has none whenever a filter matches zero rows")
	}

	// and the Go side must actually compute it from the PRE-filter count
	src, err := os.ReadFile("dashboard.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "EverHadData:   totalAll > 0") {
		t.Error("EverHadData is not computed from totalAll (the pre-filter count), so it would " +
			"agree with HasData and distinguish nothing")
	}
}

// THE ONBOARDING POLLER MUST NOT RELOAD A FILTERED PAGE FOREVER.
//
// The onboarding block carries a poller that hits /v1/events/recent — unfiltered — and calls
// location.reload() the instant any event exists, to reveal the populated dashboard. That is
// correct on a fresh install and catastrophic anywhere else.
//
// It was gated on HasData, which is computed AFTER the scope filters. So a filter matching zero
// rows rendered the onboarding block, the poller saw the instance's real events, reloaded, came
// back still filtered to zero, and reloaded again. Measured on the deployed demo: 8 main-frame
// navigations in 6 seconds, indefinitely, against the customer's own server.
func TestOnboardingIsGatedOnEverHavingDataNotOnTheFilteredCount(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// THE PROPERTY, NOT THE STRING I HAPPENED TO FIX.
	//
	// The first version of this test asserted that ONE specific line had been changed, so it
	// passed while the loop was still running: #obstatus, which anchors the reload poller, lives
	// in a SECOND onboarding block I had not touched. Measured after "fixing" it: still 10
	// navigations in 6 seconds.
	//
	// HasData is post-filter. NOTHING that can trigger a reload may depend on it, anywhere.
	if n := strings.Count(s, "{{if not .HasData}}"); n > 0 {
		t.Errorf("%d onboarding gate(s) still key off HasData, which is computed AFTER the scope "+
			"filters — any filter matching zero rows renders onboarding, and its poller reloads "+
			"the page every 4 seconds forever", n)
	}
	// and the poller must still be anchored to something that only exists on a fresh install
	if !strings.Contains(s, "first event arrived") {
		t.Error("the first-event poller is gone; re-check what this guard is protecting")
	}
	if !strings.Contains(s, `id="obstatus"`) {
		t.Fatal(`#obstatus is gone; it is the poller's anchor and this guard depends on it`)
	}
	// the block containing #obstatus must be gated on EverHadData
	ob := strings.Index(s, `id="obstatus"`)
	before := s[max(0, ob-3000):ob]
	if !strings.Contains(before, "{{if not .EverHadData}}") {
		t.Error("the block holding #obstatus is not gated on EverHadData, so the reload poller " +
			"can run on an instance that already has events")
	}

	src2, err := os.ReadFile("dashboard.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src2), "EverHadData:   totalAll > 0") {
		t.Error("EverHadData is not computed from the pre-filter count, so it distinguishes nothing")
	}
}

// NAME THE ENGINES, NEVER COUNT THEM.
//
// The AI-visibility pane rendered "3 engines", which reads as three AI search engines — ChatGPT,
// Claude, Perplexity. Only two engine values are ever written, `claude` and `claude-grounded`
// (smolanalytics-cloud/lib/geo.ts:56-57), and both are the same vendor sampled two ways; the
// third was whatever "unknown" rows existed. So a count implied a breadth of coverage that does
// not exist, on the one pane whose entire job is telling you how visible you are to AI.
//
// A count hides the composition. Naming them cannot: "claude, claude-grounded" states the limit
// in the same breath as the finding, and gets better on its own the day a second vendor is added.
func TestTheVisibilityPaneNamesItsEnginesRatherThanCountingThem(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "{{len .Engines}} engine") {
		t.Error("the visibility pane counts its engines, which implies coverage it does not have — " +
			"only claude and claude-grounded are ever written, and they are one vendor. Range over " +
			"them and print the names.")
	}
	if !strings.Contains(s, "{{range $i, $e := .Engines}}") {
		t.Error("the pane no longer names its engines; a reader cannot tell one vendor from three")
	}
}

// NO BLOCK ELEMENT MAY SIT INSIDE A SELECT.
//
// The truncation-footer pass inserted a <div class="panemore"> inside the heatmap's <select> —
// its option list ranges over TopPages, and the inserter footered every TopPages range it found.
// A div inside a select is invalid HTML the browser silently discards, so it shipped rendering
// nothing and saying nothing, which is precisely the failure mode this codebase's whole test
// suite exists to make loud.
func TestNoBlockElementInsideASelect(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?s)<select.*?</select>`).FindAllString(string(src), -1) {
		if strings.Contains(m, "<div") {
			t.Errorf("a <div> inside a <select> is silently dropped by the parser: %.120s", m)
		}
	}
}

// HEALED ROWS RENDER ON A QUIET DAY TOO.
//
// The auto-revert receipt is most of the news precisely when the investigation is quiet — the
// system pulled a flag and nothing else happened. The first version of this block sat inside the
// busy branch, so the one self-operating act the product performs would have been invisible on
// exactly the day it was the headline. Same placement rule as the movements quarter, guarded the
// same way.
func TestHealedRowsAreOutsideTheQuietBranch(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	h := strings.Index(s, "healed &middot; the system acted on its own")
	if h < 0 {
		t.Fatal("the healed block is gone; this guard is watching nothing")
	}
	// It must sit AFTER the {{else}}-branch basis lines' closing {{end}} — i.e. adjacent to the
	// movements block, which is the established outside-the-branch position.
	m := strings.Index(s, "{{with .Movements}}")
	if m < 0 || h > m {
		t.Error("the healed block does not sit directly before the movements quarter — check it " +
			"renders on quiet days, not only busy ones")
	}
	if m-h > 900 {
		t.Errorf("healed block is %d bytes from the movements anchor; it has drifted somewhere "+
			"with different branch semantics", m-h)
	}
}
