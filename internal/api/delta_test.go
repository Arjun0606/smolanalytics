package api

import "testing"

// A four-figure percentage is arithmetically correct and completely unreadable. "+6875%" was on
// the live dashboard, off a prior window of 8, and a reader who decides one tile is broken
// discounts every tile beside it.
func TestDeltaStopsUsingPercentAboveTenTimes(t *testing.T) {
	cases := []struct {
		cur, prior int
		want       string
	}{
		{558, 8, "70x"}, // the real one from the live dashboard
		{1000, 10, "100x"},
		{100, 10, "10x"},  // exactly 10x switches over
		{99, 10, "+890%"}, // just below stays a percentage
		{150, 100, "+50%"},
		{50, 100, "-50%"},
		{100, 100, "±0%"},
	}
	for _, c := range cases {
		if got := deltaStr(c.cur, c.prior); got != c.want {
			t.Errorf("deltaStr(%d, %d) = %q, want %q", c.cur, c.prior, got, c.want)
		}
	}
}

// There is no percentage change from nothing. Inventing one ("+100%", "∞") produces a number
// someone screenshots as evidence the tool is wrong.
func TestDeltaFromZeroSaysNothing(t *testing.T) {
	if got := deltaStr(50, 0); got != "" {
		t.Errorf("deltaStr(50, 0) = %q, want empty", got)
	}
}
