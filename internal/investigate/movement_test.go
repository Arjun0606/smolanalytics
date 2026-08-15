package investigate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// mvSeed: `people` per half are active; `convA`/`convB` of them do "checkout" in each half.
func mvSeed(t *testing.T, now time.Time, people, convA, convB int) []event.Event {
	t.Helper()
	var evs []event.Event
	half := func(offset int, conv int, tag string) {
		for i := 0; i < people; i++ {
			u := fmt.Sprintf("%s%d", tag, i)
			ts := now.AddDate(0, 0, -offset).Add(time.Duration(i) * time.Minute)
			evs = append(evs, event.Event{
				ID: "p" + u, Name: "$pageview", DistinctID: u, Timestamp: ts,
				Properties: map[string]any{"path": "/"},
			})
			if i < conv {
				evs = append(evs, event.Event{
					ID: "c" + u, Name: "checkout", DistinctID: u, Timestamp: ts.Add(time.Minute),
				})
			}
		}
	}
	half(70, convA, "a") // first half
	half(20, convB, "b") // second half
	return evs
}

// THE SENTENCE THE WHOLE PIVOT RESTS ON. The per-ship claim was refuted at 3.8%; this replaces it,
// and it has to name the ship count in the same breath as the verdict, or it is just another chart.
func TestUnchangedAcrossNShipsIsTheHeadline(t *testing.T) {
	now := time.Now().UTC()
	deps := make([]deploys.Deploy, 23)
	for i := range deps {
		deps[i] = deploys.Deploy{SHA: fmt.Sprintf("sha%d", i), At: now.AddDate(0, 0, -(i*3 + 1))} // -1: the window is half-open, so a deploy at exactly `now` is correctly excluded
	}
	ms := Movements(mvSeed(t, now, 400, 40, 40), deps, MovementOpts{Now: now, Days: 90})

	var m *Movement
	for i := range ms {
		if ms[i].Metric == "checkout" {
			m = &ms[i]
		}
	}
	if m == nil {
		t.Fatalf("checkout not read at all: %+v", ms)
	}
	if m.Verdict != Unchanged {
		t.Errorf("identical rates read as %q (p=%v)", m.Verdict, m.PValue)
	}
	if m.Ships != 23 {
		t.Errorf("counted %d ships, expected 23", m.Ships)
	}
	if !strings.Contains(m.Headline, "23 ships") {
		t.Errorf("the ship count must be in the sentence: %q", m.Headline)
	}
	if !strings.Contains(m.Headline, "unchanged") {
		t.Errorf("headline does not state the verdict: %q", m.Headline)
	}
	t.Logf("%s", m.Headline)
}

// A REAL MOVE MUST BE CALLED. A tool that says "unchanged" about everything is as useless as one
// that says "changed" about everything.
func TestARealImprovementIsCalled(t *testing.T) {
	now := time.Now().UTC()
	ms := Movements(mvSeed(t, now, 400, 40, 120), nil, MovementOpts{Now: now, Days: 90})
	for _, m := range ms {
		if m.Metric == "checkout" {
			if m.Verdict != MovedUp {
				t.Fatalf("10%% -> 30%% read as %q (p=%v)", m.Verdict, m.PValue)
			}
			return
		}
	}
	t.Fatal("checkout missing")
}

// RATE, NOT COUNT. Traffic doubling while the rate holds must read as unchanged. Reporting the
// raw count as progress during a traffic increase is the commonest way an analytics tool flatters
// its customer, and it is the exact thing this product exists not to do.
func TestDoublingTrafficAtTheSameRateIsNotProgress(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	add := func(offset, people, conv int, tag string) {
		for i := 0; i < people; i++ {
			u := fmt.Sprintf("%s%d", tag, i)
			ts := now.AddDate(0, 0, -offset).Add(time.Duration(i) * time.Second)
			evs = append(evs, event.Event{ID: "p" + u, Name: "$pageview", DistinctID: u, Timestamp: ts,
				Properties: map[string]any{"path": "/"}})
			if i < conv {
				evs = append(evs, event.Event{ID: "c" + u, Name: "checkout", DistinctID: u,
					Timestamp: ts.Add(time.Second)})
			}
		}
	}
	add(70, 300, 30, "a") // 10%
	add(20, 600, 60, "b") // 10%, double the traffic, double the raw count

	for _, m := range Movements(evs, nil, MovementOpts{Now: now, Days: 90}) {
		if m.Metric != "checkout" {
			continue
		}
		if m.Verdict != Unchanged {
			t.Fatalf("doubling traffic at a flat 10%% rate read as %q — the count doubled and the "+
				"rate did not, and reporting that as movement is flattery", m.Verdict)
		}
		return
	}
	t.Fatal("checkout missing")
}

// "IT DID NOT MOVE" AND "WE CANNOT TELL" ARE OPPOSITE FINDINGS. Telling a small product its work
// was wasted, when the truth is that it cannot be measured yet, is the most damaging thing this
// report could say.
func TestTooFewPeopleIsNeverReportedAsUnchanged(t *testing.T) {
	now := time.Now().UTC()
	ms := Movements(mvSeed(t, now, 8, 2, 2), nil, MovementOpts{Now: now, Days: 90})
	for _, m := range ms {
		if m.Metric == "checkout" {
			if m.Verdict != CannotTell {
				t.Fatalf("8 people per half read as %q rather than cannot-tell", m.Verdict)
			}
			if !strings.Contains(m.Headline, "too few") {
				t.Errorf("the headline must say why: %q", m.Headline)
			}
			return
		}
	}
	t.Fatal("checkout missing")
}

// ONE DEFINITION OF "DID THIS CHANGE". The whole session's recurring defect is two surfaces
// computing one question differently. This pins that the inversion uses the exported experiment
// test rather than a private reimplementation.
func TestItUsesTheSharedSignificanceTest(t *testing.T) {
	src := readSource(t, "movement.go")
	if !strings.Contains(src, "flag.ProportionsDiffer") {
		t.Error("the inversion is not using the exported test — a fourth definition of 'did this " +
			"change' is exactly how surfaces come to disagree")
	}
}

// Autocapture is the denominator, never a headline. "page view fell 12%" is the cacophony
// complaint in one line.
func TestAutocaptureIsNeverAMetricLine(t *testing.T) {
	now := time.Now().UTC()
	for _, m := range Movements(mvSeed(t, now, 400, 40, 40), nil, MovementOpts{Now: now, Days: 90}) {
		if strings.HasPrefix(m.Metric, "$") {
			t.Errorf("autocapture became a headline metric: %q", m.Headline)
		}
	}
}

// EVERY SURFACE GETS IT, NOT JUST THE ONE WITH A DEPLOY STORE.
//
// The first version wired movements into WithContext only, so brief.Build — which is the CLI, the
// weekly email AND the dashboard — silently rendered none at all. Same "two paths, one question"
// split this package exists to prevent, reintroduced while integrating the thing meant to fix it.
func TestRunProducesMovementsWithoutADeployStore(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(mvSeed(t, now, 400, 40, 40), Opts{Now: now})
	if len(inv.Movements) == 0 {
		t.Fatal("Run produced no movements, so the CLI, the email and the dashboard show none")
	}
	// and with no deploys the sentence must say so rather than implying nothing shipped
	found := false
	for _, m := range inv.Movements {
		if m.Metric == "checkout" {
			found = true
			if m.Ships != 0 {
				t.Errorf("invented %d ships with no deploy store", m.Ships)
			}
			if !strings.Contains(m.Headline, "nothing recorded as shipped") {
				t.Errorf("silently implies nothing shipped: %q", m.Headline)
			}
		}
	}
	if !found {
		t.Fatal("checkout missing from Run's movements")
	}
}

// And WithContext must REPLACE that reading with the real ship count, not add a second one.
func TestWithContextFillsInTheRealShipCount(t *testing.T) {
	now := time.Now().UTC()
	deps := []deploys.Deploy{
		{SHA: "a", At: now.AddDate(0, 0, -10)},
		{SHA: "b", At: now.AddDate(0, 0, -40)},
	}
	inv := WithContext(mvSeed(t, now, 400, 40, 40), nil, deps, Opts{Now: now})
	for _, m := range inv.Movements {
		if m.Metric == "checkout" {
			if m.Ships != 2 {
				t.Fatalf("ship count is %d, expected 2", m.Ships)
			}
			if strings.Contains(m.Headline, "nothing recorded as shipped") {
				t.Error("still carrying the no-deploys sentence after deploys were supplied")
			}
			return
		}
	}
	t.Fatal("checkout missing")
}
