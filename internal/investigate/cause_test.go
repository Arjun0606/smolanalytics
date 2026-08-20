package investigate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// THREE STATES, THREE DIFFERENT SENTENCES. The product may say "spread evenly" only when it
// actually measured a spread. Measured before this guard existed: identical data whose only
// difference was PostHog's "$" prefix produced "100% of the loss is browser=Safari" on our own
// shape and "the drop is spread evenly across browsers, devices and pages" on theirs — a
// confident claim about dimensions the code never read, shipped on every import path we sell.
func TestCauseNeverAssertsASpreadItDidNotMeasure(t *testing.T) {
	now := time.Now().UTC()
	// props: nil means "no properties at all"
	build := func(props func(browser string) map[string]any) []event.Event {
		var evs []event.Event
		id := 0
		add := func(day, n int, browser string) {
			for i := 0; i < n; i++ {
				id++
				var p map[string]any
				if props != nil {
					p = props(browser)
				}
				evs = append(evs, event.Event{
					ID: fmt.Sprintf("e%d", id), Name: "checkout",
					DistinctID: fmt.Sprintf("u%d_%d_%s", day, i, browser),
					Timestamp:  now.AddDate(0, 0, -day).Add(time.Duration(i) * time.Minute),
					Properties: p,
				})
			}
		}
		for d := 27; d >= 8; d-- {
			add(d, 30, "Safari")
			add(d, 30, "Chrome")
		}
		for d := 7; d >= 0; d-- {
			add(d, 0, "Safari") // Safari collapses entirely: the loss is 100% Safari
			add(d, 30, "Chrome")
		}
		return evs
	}
	causeOf := func(evs []event.Event) string {
		for _, f := range Run(evs, Opts{Now: now}).Findings {
			if f.Kind == KindRegression {
				return f.Cause
			}
		}
		return ""
	}

	ours := causeOf(build(func(b string) map[string]any { return map[string]any{"browser": b} }))
	if !strings.Contains(ours, "browser=Safari") {
		t.Errorf("our own shape lost its attribution: %q", ours)
	}

	// A vendor's shape must reach the SAME conclusion. Same events, same truth.
	vendor := causeOf(build(func(b string) map[string]any { return map[string]any{"$browser": b} }))
	if !strings.Contains(vendor, "browser=Safari") {
		t.Errorf("vendor-shaped properties were not attributed: %q", vendor)
	}
	if strings.Contains(vendor, "spread evenly") {
		t.Errorf("FALSE CLAIM: asserted an even spread over a 100%% Safari loss: %q", vendor)
	}

	// And with nothing to slice by, say exactly that — never "spread evenly".
	bare := causeOf(build(nil))
	if strings.Contains(bare, "spread evenly") {
		t.Errorf("asserted a spread across dimensions that do not exist: %q", bare)
	}
	if !strings.Contains(bare, "no properties") {
		t.Errorf("did not explain that there was nothing to break the drop down by: %q", bare)
	}
}
