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

func findRetention(fs []Finding) (Finding, bool) {
	for _, f := range fs {
		if strings.Contains(f.Title, "retention") {
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
	base := now.Add(-10 * 24 * time.Hour)
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("old_%d", i)
		evs = append(evs,
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
