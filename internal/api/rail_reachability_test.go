package api

import (
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
