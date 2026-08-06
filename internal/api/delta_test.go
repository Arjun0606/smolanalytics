package api

import (
	"strings"
	"testing"
)

// A four-figure percentage is arithmetically correct and completely unreadable. "+6875%" was on
// the live dashboard, off a prior window of 8, and a reader who decides one tile is broken
// discounts every tile beside it.
//
// FIXTURES UPDATED 2026-08-06, and the reason matters. The first fix rendered that case as "70x",
// which is readable and still misleading: a prior of 8 cannot support ANY growth claim, in either
// notation. Four people reading the live dashboard cold all stopped at "▲ 32x vs prior" an hour
// after install and concluded the page flatters itself. So the rule is now stronger — below
// minPriorForDelta there is no comparison at all — and these fixtures use priors that can actually
// carry one. The original intent is unchanged and still asserted below: never a four-figure
// percentage.
func TestDeltaStopsUsingPercentAboveTenTimes(t *testing.T) {
	cases := []struct {
		cur, prior int
		want       string
	}{
		{5580, 80, "70x vs 80 before"}, // the multiple form carries its own baseline
		{10000, 100, "100x vs 100 before"},
		{400, 40, "10x vs 40 before"}, // exactly 10x switches over
		{396, 40, "+890%"},            // just below stays a percentage
		{150, 100, "+50%"},
		{50, 100, "-50%"},
		{100, 100, "±0%"},
	}
	for _, c := range cases {
		if got := deltaStr(c.cur, c.prior); got != c.want {
			t.Errorf("deltaStr(%d, %d) = %q, want %q", c.cur, c.prior, got, c.want)
		}
	}
	// The original wound, restated: no output may ever be a four-figure percentage, whatever the
	// inputs. This is the property the test was written for and it outlives any fixture.
	for _, c := range []struct{ cur, prior int }{{5580, 80}, {99999, 30}, {396, 40}, {1, 30}} {
		got := deltaStr(c.cur, c.prior)
		if strings.Contains(got, "%") && len(strings.TrimLeft(got, "+-")) > 5 {
			t.Errorf("deltaStr(%d, %d) = %q — an unreadable percentage is back", c.cur, c.prior, got)
		}
	}
}

// A prior window too small to support a claim must decline to make one, rather than making it in
// a different notation. This is the fix the cold-read audit forced.
func TestATinyPriorWindowRefusesToCompare(t *testing.T) {
	for _, c := range []struct{ cur, prior int }{{128, 4}, {618, 8}, {558, 8}, {50, 29}} {
		got := deltaStr(c.cur, c.prior)
		if !strings.Contains(got, "no comparison") {
			t.Errorf("deltaStr(%d, %d) = %q — a prior of %d cannot carry a growth claim in ANY notation",
				c.cur, c.prior, got, c.prior)
		}
	}
	// At the threshold it compares again, or the guard would swallow real early growth.
	if got := deltaStr(60, 30); !strings.HasPrefix(got, "+") {
		t.Errorf("deltaStr(60, 30) = %q, want a real percentage at the threshold", got)
	}
}

// There is no percentage change from nothing. Inventing one ("+100%", "∞") produces a number
// someone screenshots as evidence the tool is wrong.
func TestDeltaFromZeroSaysNothing(t *testing.T) {
	if got := deltaStr(50, 0); got != "" {
		t.Errorf("deltaStr(50, 0) = %q, want empty", got)
	}
}
