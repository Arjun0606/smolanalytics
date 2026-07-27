package api

import (
	_ "embed"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

//go:embed dashboard_inventory.json
var inventoryJSON []byte

// TestDashboardInventory is the completeness guarantee for the dashboard.
//
// The template is being reworked from the ground up, and the owner's standing requirement is
// that NOT ONE feature is lost in the move: "we have all the features already built, it's just
// about presenting it". Every previous regression in this file was silent — a pane that stopped
// rendering, a control that lost its id, a delegated handler whose selector no longer matched
// anything. Nothing failed loudly; the feature simply was not there any more.
//
// So the inventory of what exists is frozen in dashboard_inventory.json, and this test fails if
// the template ever stops containing one of those things. It deliberately checks the RAW
// TEMPLATE rather than a rendered page, because panes sit behind Go conditionals ({{if .HasWeb}}
// and friends) that a single render would not exercise — an element that renders for nobody
// still has to exist in the source.
//
// When a feature is deliberately REMOVED, update the JSON in the same commit and say why in the
// message. When one is added, add it. What this test forbids is losing one by accident.
func TestDashboardInventory(t *testing.T) {
	var inv struct {
		Panes              []string `json:"panes"`
		Controls           []string `json:"controls"`
		JSWiredIDs         []string `json:"jsWiredIds"`
		DelegatedSelectors []string `json:"delegatedSelectors"`
	}
	if err := json.Unmarshal(inventoryJSON, &inv); err != nil {
		t.Fatalf("inventory json: %v", err)
	}

	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	tmpl := string(src)

	// every report pane must still exist as an id in the markup
	for _, id := range inv.Panes {
		if !strings.Contains(tmpl, `id="`+id+`"`) {
			t.Errorf("REPORT LOST: pane %q is no longer in the template — a whole report disappeared", id)
		}
	}

	// every control the user can operate must still exist
	for _, id := range inv.Controls {
		if !strings.Contains(tmpl, `id="`+id+`"`) {
			t.Errorf("CONTROL LOST: %q is no longer in the template — a control the user could operate is gone", id)
		}
	}

	// an id the JS reaches for must still be produced by the markup, or the handler binds to
	// nothing and the feature is dead while looking fine
	for _, id := range inv.JSWiredIDs {
		if !strings.Contains(tmpl, `id="`+id+`"`) {
			t.Errorf("WIRING BROKEN: JS calls getElementById(%q) but nothing in the markup has that id", id)
		}
	}

	// a delegated handler is only alive if something can match its selector. Check the
	// distinguishing token (a class or data attribute) still appears somewhere in the file.
	tokenRe := regexp.MustCompile(`[.\[]([\w-]+)`)
	for _, sel := range inv.DelegatedSelectors {
		m := tokenRe.FindStringSubmatch(sel)
		if m == nil {
			continue
		}
		if !strings.Contains(tmpl, m[1]) {
			t.Errorf("HANDLER ORPHANED: delegated selector %q can no longer match — %q appears nowhere in the template", sel, m[1])
		}
	}
}

// TestDashboardTemplateHygiene catches the two mistakes that have actually been made in this
// file, both of which fail in confusing ways far from their cause.
func TestDashboardTemplateHygiene(t *testing.T) {
	src, err := os.ReadFile("dashboard.tmpl.html")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	tmpl := string(src)

	// 1. html/template parses {{...}} INSIDE HTML comments. An unpaired action written in a
	//    comment is reported as "unexpected EOF" at the END of the file, which sends you
	//    hunting in the wrong place entirely.
	for _, c := range regexp.MustCompile(`(?s)<!--.*?-->`).FindAllString(tmpl, -1) {
		if strings.Contains(c, "{{") {
			t.Errorf("template action inside an HTML comment — html/template parses these, and an "+
				"unpaired one is an EOF error at the bottom of the file:\n%.160s", c)
		}
	}

	// 2. Prose accidentally left AFTER a CSS comment close becomes stray CSS and silently
	//    kills the rule that follows it. That is how .deckjump lost its display:flex.
	if i := strings.Index(tmpl, "<style>"); i >= 0 {
		css := tmpl[i:]
		if j := strings.Index(css, "</style>"); j >= 0 {
			css = css[:j]
		}
		depth := 0
		for k := 0; k+1 < len(css); k++ {
			switch css[k : k+2] {
			case "/*":
				depth++
				k++
			case "*/":
				depth--
				k++
			}
			if depth < 0 {
				t.Errorf("unbalanced CSS comment near offset %d — prose after a */ becomes stray CSS "+
					"and silently drops the next rule", k)
				break
			}
		}
		if depth != 0 {
			t.Errorf("unclosed CSS comment (depth %d at end of <style>)", depth)
		}
	}
}
