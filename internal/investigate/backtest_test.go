package investigate

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// btSeed: `pre`/day until `dropDaysAgo`, then `post`/day, across `days` of history.
func btSeed(t *testing.T, now time.Time, days, dropDaysAgo, pre, post int) []event.Event {
	t.Helper()
	var evs []event.Event
	for d := days; d >= 0; d-- {
		n := pre
		if d < dropDaysAgo {
			n = post
		}
		for i := 0; i < n; i++ {
			u := fmt.Sprintf("d%d_%d", d, i)
			evs = append(evs, event.Event{
				ID: u, Name: "checkout", DistinctID: u,
				Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			})
		}
	}
	return evs
}

// THE ARTEFACT'S WHOLE VALUE IS THE DATE. "On 14 June this would have told you checkout was
// broken" is a claim about a day the reader lived through. Without a first-seen date it is just
// another report about the past.
func TestItSaysWhenItWouldHaveTOLDYou(t *testing.T) {
	now := time.Now().UTC()
	rep := Backtest(btSeed(t, now, 90, 40, 60, 15), nil, BacktestOpts{
		Opts: Opts{Now: now}, WindowDays: 90, StepDays: 2,
	})
	if len(rep.Findings) == 0 {
		t.Fatalf("a 4x collapse 40 days ago was never surfaced across %d sweeps", rep.Sweeps)
	}
	f := rep.Findings[0]
	if f.FirstSeen == "" {
		t.Fatal("no first-seen date — the finding is indistinguishable from a report about today")
	}
	if f.Day == "" {
		t.Error("no change day, so the lag cannot be computed")
	}
	if f.LagDays < 0 {
		t.Errorf("negative lag: reported on %s, about a change on %s", f.FirstSeen, f.Day)
	}
	t.Logf("would have reported %q on %s, about a change on %s (lag %d days)",
		f.Headline, f.FirstSeen, f.Day, f.LagDays)
}

// IT MUST NOT SEE THE FUTURE.
//
// Two honest notes, because this guard resisted three attempts to test it properly.
//
// The truncation in Backtest is defence in depth rather than a proven-necessary fix: whatchanged
// .Series already windows to `now` and ignores later events, so on every fixture I could build,
// handing a dated sweep the whole log produced the identical answer. My first version of this test
// asserted no finding was dated before any event existed, and passed with the truncation DELETED —
// the fourth test this week to hold for a reason other than the one it named.
//
// I am keeping the truncation anyway. Series clamping is a property of one function three calls
// down, and cause attribution's windowEitherSide takes its bound from the LAST event in the slice,
// which is exactly the shape that would leak if the pipeline ever changed. The cost is one filter
// per sweep; the failure it prevents is every lag number in a sales artefact being a fabrication.
//
// So this tests the properties that ARE checkable end to end: nothing is dated after the run, and
// no lag is negative. A finding dated before the change it describes would be the visible symptom
// of reading forward.
func TestNoFindingIsDatedAfterTheRunOrBeforeItsOwnCause(t *testing.T) {
	now := time.Now().UTC()
	rep := Backtest(btSeed(t, now, 90, 45, 60, 15), nil, BacktestOpts{
		Opts: Opts{Now: now}, WindowDays: 90, StepDays: 2,
	})
	if len(rep.Findings) == 0 {
		t.Fatal("no findings, so nothing was actually checked")
	}
	today := now.Format("2006-01-02")
	for _, f := range rep.Findings {
		if f.FirstSeen > today {
			t.Errorf("dated in the future: %s reported %s", f.Headline, f.FirstSeen)
		}
		if f.LagDays < 0 {
			t.Errorf("reported %s on %s, about a change on %s — a negative lag means it read forward",
				f.Headline, f.FirstSeen, f.Day)
		}
	}
}

// And `before` itself, since the pipeline depends on it being exact.
func TestBeforeExcludesTheBoundary(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Hour)
	evs := []event.Event{
		{ID: "a", Name: "x", Timestamp: at.Add(-time.Second)},
		{ID: "b", Name: "x", Timestamp: at},
		{ID: "c", Name: "x", Timestamp: at.Add(time.Second)},
	}
	got := before(evs, at)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected only the event strictly before the cutoff, got %d: %+v", len(got), got)
	}
}

// ONE FINDING, NOT FORTY. A regression visible on every daily sweep for six weeks is one thing you
// would have been told about once. Emitting it repeatedly is the "cacophony" failure the live
// Brief was built to avoid, spread over time instead of down a page.
func TestTheSameRegressionIsReportedOnce(t *testing.T) {
	now := time.Now().UTC()
	rep := Backtest(btSeed(t, now, 90, 45, 60, 15), nil, BacktestOpts{
		Opts: Opts{Now: now}, WindowDays: 90, StepDays: 1,
	})
	seen := map[string]int{}
	for _, f := range rep.Findings {
		seen[string(f.Kind)+f.Event+f.Day]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("%s reported %d times across the replay", k, n)
		}
	}
	if rep.Sweeps < 60 {
		t.Errorf("only %d sweeps over 90 days at step 1 — the replay is not actually walking", rep.Sweeps)
	}
}

// A flat product produces nothing, and says what it looked at. Same rule as the live Brief: a
// backtest that manufactures findings to look impressive is worth less than one that comes back
// empty honestly, because the first thing a sceptic does is check one.
func TestAFlatHistoryProducesNoFindings(t *testing.T) {
	now := time.Now().UTC()
	rep := Backtest(btSeed(t, now, 90, 0, 40, 40), nil, BacktestOpts{
		Opts: Opts{Now: now}, WindowDays: 90, StepDays: 3,
	})
	if len(rep.Findings) > 0 {
		t.Fatalf("invented %d finding(s) about a flat product: %q",
			len(rep.Findings), rep.Findings[0].Headline)
	}
	if len(rep.Scanned) == 0 {
		t.Error("no record of what was scanned — an empty result is then indistinguishable from a broken run")
	}
}
