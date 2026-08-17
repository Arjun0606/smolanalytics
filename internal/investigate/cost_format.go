package investigate

import "fmt"

// Size renders a Cost the one way, for every surface.
//
// This exists because four surfaces — the CLI brief, the share page, the backtest output and the
// dashboard — each rendered Cost themselves, and all four printed only People. The moment
// UsdPerMonth became reachable, that was four places to remember, and this codebase's most
// repeated defect is exactly that: one question, several surfaces, answers drifting apart.
//
// Money leads when it is there, because a dollar figure is what makes a finding rankable against
// another team's priorities. People is always shown alongside it rather than replaced: the dollar
// figure is a product of the people figure, and hiding the input makes the output unauditable.
func (c Cost) Size() string {
	// SizeText is populated on the way out so JSON consumers get the same string; when it is
	// already set (a finding decoded from JSON) it is authoritative.
	if c.SizeText != "" {
		return c.SizeText
	}
	if c.UsdPerMonth > 0 {
		return fmt.Sprintf("~$%s/mo · %s people", money(c.UsdPerMonth), group(c.People))
	}
	if c.People > 0 {
		return fmt.Sprintf("~%s people/mo", group(c.People))
	}
	return ""
}

// Readout is the figure alone, for the one place on a screen that shows a figure alone.
//
// The slab sets this at 64px with the unit printed beside it, so it must NOT carry a unit or a
// second quantity. It exists here rather than as {{comma .Cost.People}} in a template because
// this package already owns how a cost is rendered, and a second renderer growing in the HTML is
// exactly how four surfaces came to print only the people half.
//
// ALWAYS People, in every state including zero on a quiet day. A readout whose unit changes when
// revenue happens to be instrumented is a readout nobody can compare week to week — so money,
// when it exists, prints in the support line via Size() and never displaces this.
func (c Cost) Readout() string { return group(c.People) }

// HasMoney reports whether a dollar figure was computed, so a renderer can decide whether to show
// the money line without reaching into the field and formatting it itself.
func (c Cost) HasMoney() bool { return c.UsdPerMonth > 0 }

// money renders whole dollars with thousands separators. No cents: a figure derived from a mean
// times a count is precise to about the nearest hundred, and printing "$14,880.37" claims an
// accuracy the arithmetic does not have.
func money(f float64) string {
	return group(int(f + 0.5))
}

// group is the thousands separator, defined here so Size has no dependency on the brief package.
func group(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
