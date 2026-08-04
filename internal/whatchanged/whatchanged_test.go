package whatchanged

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func series(counts ...int) []Point {
	out := make([]Point, 0, len(counts))
	d := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range counts {
		out = append(out, Point{Day: d.AddDate(0, 0, i).Format("2006-01-02"), Count: c})
	}
	return out
}

// A detector that reports a change every time is useless, and one that misses a real break is
// worse than nothing because it grants false confidence. These pin both directions.

func TestDetectsARealDrop(t *testing.T) {
	// Steady at ~100, then falls to ~20 on the eighth day.
	c := Detect(series(100, 98, 102, 99, 101, 97, 103, 20, 18, 22, 19, 21))
	if !c.Found {
		t.Fatalf("missed an 80%% drop: %+v", c)
	}
	if c.Day != "2026-07-08" {
		t.Errorf("attributed the drop to %s, want 2026-07-08", c.Day)
	}
	if c.Direction != "drop" {
		t.Errorf("direction %q, want drop", c.Direction)
	}
	if c.PercentPct > -70 {
		t.Errorf("change of %.0f%% understates an 80%% fall", c.PercentPct)
	}
}

func TestDetectsASpike(t *testing.T) {
	c := Detect(series(10, 12, 9, 11, 10, 8, 50, 55, 48, 52))
	if !c.Found || c.Direction != "spike" {
		t.Fatalf("missed a spike: %+v", c)
	}
}

// Noise must not read as a change. This is the property that decides whether anyone trusts the
// tool after the first week.
func TestSteadySeriesReportsNoChange(t *testing.T) {
	for _, s := range [][]Point{
		series(100, 98, 102, 99, 101, 97, 103, 100, 99, 101),
		series(50, 55, 45, 52, 48, 51, 49, 53, 47, 50),
		series(5, 5, 5, 5, 5, 5, 5, 5),
	} {
		if c := Detect(s); c.Found {
			t.Errorf("reported a change in a steady series: %s", c.Verdict)
		}
	}
}

// An event going to exactly zero is the most important case and needs its own wording. It is
// almost never a product change — it is broken tracking or a failed deploy — and "down 100%"
// reads like every other number.
func TestGoingToZeroIsCalledOutAsBrokenNotAsADrop(t *testing.T) {
	c := Detect(series(40, 42, 38, 41, 39, 0, 0, 0, 0, 0))
	if !c.Found {
		t.Fatalf("missed an event stopping entirely: %+v", c)
	}
	if !strings.Contains(c.Verdict, "STOPPED") {
		t.Errorf("an event reaching zero should be called out as stopped, got %q", c.Verdict)
	}
	if !strings.Contains(c.Verdict, "broken tracking") {
		t.Errorf("verdict should name the likely cause, got %q", c.Verdict)
	}
}

func TestTooLittleHistorySaysSo(t *testing.T) {
	c := Detect(series(1, 2, 3))
	if c.Found {
		t.Error("three days is not enough to locate a change")
	}
	if !strings.Contains(c.Verdict, "at least") {
		t.Errorf("should say how much history it needs, got %q", c.Verdict)
	}
}

// A fall that loses 600 events a day must outrank a rise that gains 19, even though the rise is
// a bigger multiple. Someone asking what changed is hunting the lost traffic, and scoring on the
// ratio alone puts 1→20 (20x) above 900→300 (3x), which is backwards.
func TestScoreWeighsTrafficMovedNotJustTheRatio(t *testing.T) {
	rise := Detect(series(1, 1, 1, 1, 20, 20, 20, 20))
	fall := Detect(series(900, 900, 900, 900, 300, 300, 300, 300))
	if !rise.Found || !fall.Found {
		t.Fatalf("both should be detected: rise=%+v fall=%+v", rise, fall)
	}
	if scoreOf(fall) <= scoreOf(rise) {
		t.Errorf("a 600/day fall scored no higher than a 19/day rise — the detector is ranking on ratio alone")
	}
}

// scoreOf reproduces the ranking statistic so the property above can be asserted directly.
func scoreOf(c Change) float64 {
	d := c.After - c.Before
	return d * d / (c.Before + c.After + 1)
}

// One outlying day must not invent a step change. This is what the median level buys, and it is
// the difference between a detector people trust and one they learn to ignore.
func TestSingleOutlierDoesNotInventAStep(t *testing.T) {
	if c := Detect(series(900, 10, 11, 9, 10, 12, 10, 11, 9, 10)); c.Found {
		t.Errorf("one loud first day was reported as a step change: %s", c.Verdict)
	}
	if c := Detect(series(10, 11, 9, 10, 12, 10, 11, 9, 10, 900)); c.Found {
		t.Errorf("one loud last day was reported as a step change: %s", c.Verdict)
	}
}

// Days with no events must exist in the series as zeros. A series that omits them cannot see a
// metric going to zero, which is the single most important thing it needs to see.
func TestSeriesIncludesEmptyDaysAsZero(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{Name: "signup", Timestamp: now.AddDate(0, 0, -9)},
		{Name: "signup", Timestamp: now.AddDate(0, 0, -9)},
		{Name: "signup", Timestamp: now},
		{Name: "other", Timestamp: now}, // must not be counted
	}
	s := Series(evs, "signup", 10, now)
	if len(s) != 10 {
		t.Fatalf("got %d days, want 10", len(s))
	}
	if s[0].Count != 2 {
		t.Errorf("first day has %d, want 2", s[0].Count)
	}
	if s[len(s)-1].Count != 1 {
		t.Errorf("last day has %d, want 1", s[len(s)-1].Count)
	}
	zeros := 0
	for _, p := range s {
		if p.Count == 0 {
			zeros++
		}
	}
	if zeros != 8 {
		t.Errorf("got %d empty days, want 8 — missing days would hide a metric going to zero", zeros)
	}
	// Oldest first, so the series reads left to right like a chart.
	if s[0].Day >= s[1].Day {
		t.Error("series is not in chronological order")
	}
}

// The search window must straddle the change. A deploy on Friday shows up in Monday's numbers,
// so looking only at the exact day misses the commit that actually did it.
func TestWindowStraddlesTheChangeDay(t *testing.T) {
	from, to, ok := Window("2026-07-15", 3)
	if !ok {
		t.Fatal("valid date rejected")
	}
	if from != "2026-07-12" || to != "2026-07-19" {
		t.Errorf("window %s..%s, want 2026-07-12..2026-07-19", from, to)
	}
	if _, _, ok := Window("not-a-date", 3); ok {
		t.Error("a bad date should be rejected rather than producing a silent window")
	}
}

// Ranking must be closest-first and deterministic, or the same question gives a different answer
// on each run.
func TestRankIsClosestFirstAndDeterministic(t *testing.T) {
	cs := []Commit{
		{SHA: "ccc", When: "2026-07-18T10:00:00Z", Subject: "far after"},
		{SHA: "aaa", When: "2026-07-15T09:00:00Z", Subject: "same day"},
		{SHA: "bbb", When: "2026-07-13T09:00:00Z", Subject: "two days before"},
	}
	got := Rank(cs, "2026-07-15")
	if got[0].SHA != "aaa" {
		t.Errorf("closest commit should rank first, got %s", got[0].SHA)
	}
	first := fmt.Sprint(Rank(cs, "2026-07-15"))
	for i := 0; i < 10; i++ {
		if fmt.Sprint(Rank(cs, "2026-07-15")) != first {
			t.Fatal("ranking is not deterministic")
		}
	}
	// An unparseable timestamp must sort last rather than crash or win.
	withBad := append(append([]Commit(nil), cs...), Commit{SHA: "zzz", When: "garbage"})
	if Rank(withBad, "2026-07-15")[3].SHA != "zzz" {
		t.Error("a commit with an unreadable date should sort last")
	}
}

// The detector must name the day the change actually happened, not merely a day consistent with
// it. A clean step from 40/day to 5/day gives an identical before/after median at every split
// between the two levels, so an unweighted score ties across all of them and the earliest wins —
// reporting a change days before it occurred, and sending someone to read the wrong commits.
func TestLandsOnTheActualChangeDayNotAnEarlierTiedOne(t *testing.T) {
	// Twelve days at 40, then eight at 5. The step is between index 11 and 12.
	counts := make([]int, 0, 20)
	for i := 0; i < 12; i++ {
		counts = append(counts, 40)
	}
	for i := 0; i < 8; i++ {
		counts = append(counts, 5)
	}
	s := series(counts...)
	c := Detect(s)
	if !c.Found {
		t.Fatalf("missed a clean step: %+v", c)
	}
	if c.Day != s[12].Day {
		t.Errorf("attributed the change to %s, but it happened on %s — the score is tying across splits and taking the earliest",
			c.Day, s[12].Day)
	}
	if c.Before != 40 || c.After != 5 {
		t.Errorf("levels %v -> %v, want 40 -> 5", c.Before, c.After)
	}
}

// The same, mirrored, so the fix is not accidentally biased toward late splits instead.
func TestLandsOnTheActualChangeDayForAnEarlyStep(t *testing.T) {
	counts := make([]int, 0, 20)
	for i := 0; i < 5; i++ {
		counts = append(counts, 8)
	}
	for i := 0; i < 15; i++ {
		counts = append(counts, 90)
	}
	s := series(counts...)
	c := Detect(s)
	if !c.Found {
		t.Fatalf("missed a clean step: %+v", c)
	}
	if c.Day != s[5].Day {
		t.Errorf("attributed the change to %s, but it happened on %s", c.Day, s[5].Day)
	}
}
