package insight

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func uev(name, id string, ts time.Time) event.Event {
	return event.Event{Name: name, DistinctID: id, Timestamp: ts}
}

// Matched on KIND rather than on the word "retention" in the title. The title is copy and copy
// changes — when the finding became "We can't tell yet whether people come back" for instances
// with no identity, a title match stopped finding it and reported the finding as absent rather
// than as different.
func findRetention(fs []Finding) (Finding, bool) {
	for _, f := range fs {
		if f.Kind == KindRetention {
			return f, true
		}
	}
	return Finding{}, false
}

// A cohort younger than N days can't have day-N activity — it must not sit in the
// day-N denominator dragging the percentage down (the retention-triangle mistake).
func TestRetentionExcludesUnobservableCohorts(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event

	// old cohort: 25 users first seen 10 days ago, ALL return on day 1 and day 7.
	// (25, not fewer — the cohort must clear the minSample floor to produce a finding.)
	//
	// They identify, and that is now load-bearing: without an $identify anywhere, a retention
	// RATE is not reported at all, because every visit from an anonymous browser looks like a new
	// person and the resulting number measures instrumentation rather than the product. These
	// users demonstrably come back, which is only knowable because they are identified.
	base := now.Add(-10 * 24 * time.Hour)
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("old_%d", i)
		evs = append(evs,
			uev("$identify", id, base),
			uev("open", id, base),
			uev("open", id, base.Add(24*time.Hour)),   // day 1
			uev("open", id, base.Add(7*24*time.Hour)), // day 7
		)
	}
	// young cohort: 90 users first seen a few hours ago — day 1/7 haven't happened yet.
	for i := 0; i < 90; i++ {
		evs = append(evs, uev("open", fmt.Sprintf("new_%d", i), now.Add(-2*time.Hour)))
	}

	f, ok := findRetention(Generate(evs))
	if !ok {
		t.Fatal("expected a retention finding")
	}
	// truth: 100% day-1 and 100% day-7 among users old enough to observe. With the young
	// cohort wrongly in the denominator it would read ~22%.
	if !strings.Contains(f.Title, "Day-1 retention 100%") || !strings.Contains(f.Title, "day-7 100%") {
		t.Fatalf("young cohort polluted the denominator: %q (%s)", f.Title, f.Detail)
	}
	if f.Severity != "info" {
		t.Fatalf("100%% retention must not be a warn, got %s", f.Severity)
	}
}

// With ONLY a young cohort (nothing observable yet), there must be no retention
// finding at all — better silent than a fabricated 0%.
func TestRetentionSilentWhenNothingObservable(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 50; i++ {
		evs = append(evs, uev("open", fmt.Sprintf("u%d", i), now.Add(-3*time.Hour)))
	}
	if f, ok := findRetention(Generate(evs)); ok {
		t.Fatalf("no cohort is past day 1 yet — expected no retention finding, got %q", f.Title)
	}
}

// A retention RATE must not be reported when nothing has ever identified.
//
// Every visit from an anonymous browser looks like a brand new person, so someone returning
// tomorrow is indistinguishable from a stranger arriving today. The rate that produces is a floor,
// it is always terrible, and the dashboard was printing it as a WARNING — the second loudest thing
// on the page — right beside a pane quietly admitting that nothing had ever called identify. Two
// facts, two panes, never connected, and the reader concludes their product is failing.
func TestNoRetentionRateWithoutIdentity(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	// 40 anonymous browsers, each seen once, ten days ago: a cohort big enough to produce a
	// finding and a 0% rate that means nothing.
	base := now.Add(-10 * 24 * time.Hour)
	for i := 0; i < 40; i++ {
		evs = append(evs, uev("open", fmt.Sprintf("anon_%d", i), base))
	}

	f, ok := findRetention(Generate(evs))
	if !ok {
		t.Fatal("the slot must still say something — silence would leave the reader with no idea " +
			"why there is no retention number")
	}
	if f.Severity == "warn" {
		t.Errorf("an unmeasurable retention rate was reported as a WARNING: %q · %q", f.Title, f.Detail)
	}
	if strings.Contains(f.Title, "%") {
		t.Errorf("a percentage was reported with no identity to measure it against: %q", f.Title)
	}
	if !strings.Contains(f.Detail, "identify") {
		t.Errorf("the finding must name what is missing, got %q", f.Detail)
	}
}
