package api

import (
	"strings"
	"testing"
)

// Three bugs found by opening the live instance in a browser, all of the same species: the
// number was right and everything around it was wrong. None of them failed a test, and none of
// them were visible in the source — you had to look at the page.

// deltaStr learned to print "70x" so that four-figure percentages stopped being unreadable.
// Both classifiers downstream were still reading the first BYTE of the string to decide up or
// down, so the biggest moves on the page — the only ones large enough to reach the multiple
// form — lost their arrow, their colour and their sparkline marker, and rendered neutral beside
// smaller changes shown in green.
func TestDeltaDirectionSurvivesTheMultipleForm(t *testing.T) {
	for _, tc := range []struct{ delta, want string }{
		{"70x", "up"},   // the one on the live dashboard
		{"100x", "up"},  //
		{"10x", "up"},   // exactly where deltaStr switches over
		{"+890%", "up"}, // just below it
		{"+50%", "up"},
		{"-50%", "down"},
		{"±0%", ""}, // no movement to point at — and a multi-byte first rune, once a crash risk
		{"", ""},
	} {
		if got := deltaDir(tc.delta); got != tc.want {
			t.Errorf("deltaDir(%q) = %q, want %q", tc.delta, got, tc.want)
		}
	}
	// Every COMPARABLE form deltaStr produces has to classify — this is the pair that drifted apart.
	// Priors are at or above minPriorForDelta, because below it deltaStr no longer returns a delta
	// at all: it says the comparison cannot be made. The original fixtures used priors of 8 and 10,
	// which is precisely the window where "32x growth" is arithmetic on noise.
	for _, c := range []struct{ cur, prior int }{{5580, 80}, {1000, 100}, {400, 40}, {990, 99}, {150, 100}, {50, 100}} {
		d := deltaStr(c.cur, c.prior)
		if deltaDir(d) == "" {
			t.Errorf("deltaStr(%d, %d) = %q, which deltaDir cannot classify — it will render with no arrow", c.cur, c.prior, d)
		}
	}
	// And the refusal must NOT classify: it is a sentence explaining that there is nothing to
	// compare against, so rendering an up-arrow beside it would reassert the very claim it declines
	// to make.
	for _, c := range []struct{ cur, prior int }{{558, 8}, {1000, 10}, {128, 4}} {
		d := deltaStr(c.cur, c.prior)
		if !strings.Contains(d, "no comparison") {
			t.Errorf("deltaStr(%d, %d) = %q — a prior this small must refuse, not divide", c.cur, c.prior, d)
		}
		if deltaDir(d) != "" {
			t.Errorf("deltaStr(%d, %d) = %q got an arrow; a refusal has no direction", c.cur, c.prior, d)
		}
	}
}

// The chart table classified its CHANGE column by slicing the first character off the delta —
// the same bug, in a second place. One shared function now, so they cannot drift again.
func TestChartTableUsesTheSharedDirectionRule(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if strings.Contains(tpl, `slice .Delta 0 1`) {
		t.Error("the chart table is classifying its own change column by first character again — use {{deltadir .Delta}}")
	}
	if !strings.Contains(tpl, `{{deltadir .Delta}}`) {
		t.Error("the chart table lost its direction class; every change renders in the neutral tone")
	}
}

// The conversion-by-segment card had no sample floor, so a country with ONE visitor who
// happened to convert rendered "100%" with a full-width bar — the best-performing segment on
// the page, drawn from one person — above a wall of one-user rows reading 0%.
func TestConversionBySegmentRefusesToRateAThinSegment(t *testing.T) {
	if segConvMinUsers < 10 {
		t.Errorf("segConvMinUsers is %d: below 10 a single user moves the rate by more than the "+
			"differences this card exists to show", segConvMinUsers)
	}
	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, `class="sc-thin"`) {
		t.Error("the held-back segments are no longer counted on screen — a card that quietly " +
			"truncates reads as a card that covered everything")
	}
}
