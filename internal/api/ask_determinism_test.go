package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The same question over the same data must return the same answer, every time.
//
// Every top-N list in the ask bar builds its rows by ranging a map and sorts them with
// sort.Slice, which is not stable. With ties — eight countries on 3 visitors each, seven
// plans all converting 5 of 10 — the WINNERS changed run to run: 12 calls to "which
// countries are my users in" produced 7 different country lists, and the browser list was
// distinct on all 12. A user who re-runs a question and gets different countries cannot
// trust either answer, and the dashboard (which does tie-break) disagrees with both.
func TestTopListsDoNotShuffleBetweenIdenticalCalls(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	ccs := []string{"US", "IN", "DE", "FR", "GB", "JP", "BR", "CA"}
	browsers := []string{"Chrome", "Safari", "Firefox", "Edge", "Opera", "Brave", "Vivaldi", "Arc"}
	var evs []event.Event
	for i, cc := range ccs {
		for u := 0; u < 3; u++ { // an exact tie: 3 visitors per country, 3 per browser
			evs = append(evs, event.Event{
				Name: "$pageview", DistinctID: fmt.Sprintf("%s-%d", cc, u),
				Timestamp: now.Add(-time.Duration(u+1) * time.Hour),
				Properties: map[string]any{
					"path": "/", "country": cc, "browser": browsers[i],
				},
			})
		}
	}

	for _, q := range []string{"which countries are my users in", "what browsers do my visitors use"} {
		first := answer(q, evs, now, nil)
		for i := 0; i < 30; i++ {
			if got := answer(q, evs, now, nil); got != first {
				t.Fatalf("%q answered differently on run %d over identical data:\n  %s\n  %s", q, i, first, got)
			}
		}
	}
}

// answerConvBy ranks conversion by segment and keeps only six rows, so map order didn't just
// reshuffle the list — it dropped a DIFFERENT segment off the end on each call.
func TestConversionBySegmentDoesNotShuffleBetweenIdenticalCalls(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	plans := []string{"pro", "free", "team", "starter", "growth", "scale", "enterprise"}
	var evs []event.Event
	for _, p := range plans {
		for u := 0; u < 10; u++ { // identical 5-of-10 rate in every plan: a full tie
			id := fmt.Sprintf("%s-%d", p, u)
			evs = append(evs, event.Event{
				Name: "$pageview", DistinctID: id, Timestamp: now.Add(-2 * time.Hour),
				Properties: map[string]any{"path": "/", "plan": p},
			})
			if u < 5 {
				evs = append(evs, event.Event{
					Name: "signup", DistinctID: id, Timestamp: now.Add(-time.Hour),
					Properties: map[string]any{"plan": p},
				})
			}
		}
	}

	first := answerConvBy(evs, "plan", "signup", askWindow{})
	for i := 0; i < 30; i++ {
		if got := answerConvBy(evs, "plan", "signup", askWindow{}); got != first {
			t.Fatalf("conversion-by-plan answered differently on run %d over identical data:\n  %s\n  %s", i, first, got)
		}
	}
}
