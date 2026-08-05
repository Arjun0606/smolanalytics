package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The anomaly detector assumed every event is one you want MORE of, so a drop was the warning
// and a rise was good news. For the autocapture events that only ever record a user failing at
// something that is exactly inverted — and the live dashboard was filing "dead clicks jumped
// 308% in the last 24h" as an info-level note, sorted in among the wins.
func TestAnomalyPolarityFollowsTheEvent(t *testing.T) {
	now := time.Now().UTC()
	// a baseline of ~7/day for 7 days, then a hard spike in the last 24h
	build := func(name string) []event.Event {
		var evs []event.Event
		for d := 1; d <= 7; d++ {
			for i := 0; i < 7; i++ {
				evs = append(evs, event.Event{
					Name: name, DistinctID: "u", Timestamp: now.Add(-time.Duration(d)*24*time.Hour - time.Hour),
				})
			}
		}
		for i := 0; i < 40; i++ { // the last 24h: ~5.7x the daily baseline
			evs = append(evs, event.Event{Name: name, DistinctID: "u", Timestamp: now.Add(-2 * time.Hour)})
		}
		return evs
	}

	for _, tc := range []struct {
		name    string
		wantSev string
	}{
		{"signup", "info"},     // more signups is good news
		{"$deadclick", "warn"}, // more dead clicks is the warning
		{"$rageclick", "warn"},
		{"$exception", "warn"},
	} {
		evs := build(tc.name)
		got := anomalies(evs, []string{tc.name}, now)
		if len(got) != 1 {
			t.Errorf("%s: expected one anomaly, got %d", tc.name, len(got))
			continue
		}
		f := got[0]
		if f.Severity != tc.wantSev {
			t.Errorf("%s spiking is severity %q, want %q\n title: %s", tc.name, f.Severity, tc.wantSev, f.Title)
		}
		// the prose still has to describe the direction that actually happened
		if !strings.Contains(f.Title, "jumped") {
			t.Errorf("%s spiked but the title says otherwise: %s", tc.name, f.Title)
		}
		if f.Rate <= 0 {
			t.Errorf("%s spiked but Rate is %d — consumers branch on the sign", tc.name, f.Rate)
		}
	}
}

// The inverse: dead clicks falling off is good news, not a regression to chase.
func TestAFallingFailureEventIsNotAWarning(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for d := 1; d <= 7; d++ {
		for i := 0; i < 10; i++ {
			evs = append(evs, event.Event{
				Name: "$deadclick", DistinctID: "u", Timestamp: now.Add(-time.Duration(d)*24*time.Hour - time.Hour),
			})
		}
	}
	evs = append(evs, event.Event{Name: "$deadclick", DistinctID: "u", Timestamp: now.Add(-2 * time.Hour)})

	got := anomalies(evs, []string{"$deadclick"}, now)
	if len(got) != 1 {
		t.Fatalf("expected one anomaly, got %d", len(got))
	}
	if got[0].Severity != "info" {
		t.Errorf("dead clicks collapsing is filed as %q: %s", got[0].Severity, got[0].Title)
	}
	if !strings.Contains(got[0].Title, "dropped") {
		t.Errorf("title does not describe the drop: %s", got[0].Title)
	}
}

// A custom event is never guessed at. "error_rate" or "checkout_failed" would be a fair guess
// and a wrong one often enough to matter, and calling a real improvement a regression costs
// more trust than staying quiet.
func TestCustomEventsAreNotGuessedAsFailures(t *testing.T) {
	for _, n := range []string{"signup", "checkout", "error_rate", "checkout_failed", "activate"} {
		if UpIsBad(n) {
			t.Errorf("UpIsBad(%q) is true — custom event names must not be pattern-matched", n)
		}
	}
	for _, n := range []string{"$deadclick", "$rageclick", "$exception", "$error"} {
		if !UpIsBad(n) {
			t.Errorf("UpIsBad(%q) is false — a rise in this event is the bad news", n)
		}
	}
}

// "page views is up 230% week-over-week" was on a live dashboard. Six of the nine autocapture
// nouns are plural and the sentence around them was hardcoded singular.
func TestTrendTitleAgreesWithItsSubject(t *testing.T) {
	for _, tc := range []struct{ event, want string }{
		{"$pageview", "are"},  // "page views"
		{"$click", "are"},     // "clicks"
		{"$deadclick", "are"}, // "dead clicks"
		{"$engagement", "is"}, // "page engagement"
		{"signup", "is"},      // custom: the operator named it, singular reads right
		{"checkout", "is"},
	} {
		if got := EventVerbIs(tc.event); got != tc.want {
			t.Errorf("EventVerbIs(%q) = %q, want %q (noun is %q)",
				tc.event, got, tc.want, HumanEventNoun(tc.event))
		}
	}
}
