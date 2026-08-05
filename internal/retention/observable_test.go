package retention

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// obsNow is a fixed instant so the epoch-block alignment below is a constant, not a
// property of the day the suite happens to run: 2026-08-04 12:00 UTC sits 5 days into the
// 7-day block that starts 2026-07-30, and 29 days into the 30-day block that starts
// 2026-07-06. Those two offsets are exactly what made the day-index rule look plausible.
var obsNow = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// obsCohort builds n users who were each seen once, two hours before obsNow, and nothing
// since — the ordinary "a batch of people signed up today, none has come back yet" shape.
func obsCohort(n int, bucket string, periods int) Result {
	evs := make([]event.Event, 0, n)
	for i := 0; i < n; i++ {
		evs = append(evs, event.Event{
			DistinctID: string(rune('a' + i)),
			Name:       "open",
			Timestamp:  obsNow.Add(-2 * time.Hour),
		})
	}
	return ComputeBucketed(evs, periods, "open", bucket, false)
}

// dayIndexRule is the expression the dashboard grid used to decide whether a cell was
// observable: both sides divided by 86400 while `d` was a PERIOD index. Reproduced here so
// the test can assert, in one place, that it and the real rule disagree — and in which
// direction. Never call this outside the test; it is the bug, kept as a witness.
func dayIndexRule(cohortDate time.Time, n int, now time.Time) bool {
	return cohortDate.UTC().Unix()/86400+int64(n) <= now.UTC().Unix()/86400
}

// A weekly grid measured period offsets in DAYS, so for a cohort formed 5 days into the
// current week it declared weeks 1..5 finished and rendered them as `0%` — six cells of
// fabricated churn for weeks that begin 2 to 37 days in the FUTURE. Meanwhile
// /v1/retention?bucket=week for the same instant returned [20,null,null,...]. Observable is
// the one predicate all three surfaces now ask, measured in the grid's own bucket unit.
func TestObservable_WeeklyMeasuresWeeksNotDays(t *testing.T) {
	r := obsCohort(4, "week", 7)
	if len(r.Cohorts) != 1 {
		t.Fatalf("want one cohort, got %d", len(r.Cohorts))
	}
	c := r.Cohorts[0]
	if got := c.Date.Format("2006-01-02"); got != "2026-07-30" {
		t.Fatalf("cohort week block starts %s, want 2026-07-30 — the fixture's alignment moved", got)
	}
	for n := 1; n <= 7; n++ {
		if Observable(r, c.Date, n, obsNow) {
			t.Errorf("week %d of a cohort formed this week reads as observable; that week has not started", n)
		}
	}
	// and the rule the grid used really would have painted W1..W5 as finished 0%
	fabricated := 0
	for n := 1; n <= 7; n++ {
		if dayIndexRule(c.Date, n, obsNow) {
			fabricated++
		}
	}
	if fabricated != 5 {
		t.Errorf("the day-index rule marks %d future weeks observable, expected the measured 5 "+
			"(W1..W5) — if this changed, the witness no longer reproduces the reported bug", fabricated)
	}
	// period 0 is the cohort's own size, known the moment it forms
	if !Observable(r, c.Date, 0, obsNow) {
		t.Error("period 0 must always be observable — it is the cohort size, not a return")
	}
}

// Month buckets are 30-day blocks, so the same day-index arithmetic was off by 30x: every
// one of M1..M7 rendered as a finished 0% for a cohort whose next month has not begun.
func TestObservable_MonthlyMeasuresMonthsNotDays(t *testing.T) {
	r := obsCohort(4, "month", 7)
	c := r.Cohorts[0]
	if got := c.Date.Format("2006-01-02"); got != "2026-07-06" {
		t.Fatalf("cohort month block starts %s, want 2026-07-06 — the fixture's alignment moved", got)
	}
	for n := 1; n <= 7; n++ {
		if Observable(r, c.Date, n, obsNow) {
			t.Errorf("month %d of a cohort formed this month reads as observable", n)
		}
		if !dayIndexRule(c.Date, n, obsNow) {
			t.Errorf("the day-index witness no longer marks month %d observable; the fixture drifted", n)
		}
	}
}

// The daily grid was the one case the day-index arithmetic got right, so pin it: day N is
// countable once it has fully elapsed, and today's in-progress day is not.
func TestObservable_DailyUnchanged(t *testing.T) {
	evs := []event.Event{{DistinctID: "a", Name: "open", Timestamp: obsNow.AddDate(0, 0, -3)}}
	r := ComputeBucketed(evs, 7, "open", "day", false)
	c := r.Cohorts[0]
	for n := 1; n <= 2; n++ {
		if !Observable(r, c.Date, n, obsNow) {
			t.Errorf("day %d of a 3-day-old cohort must be observable", n)
		}
	}
	for n := 3; n <= 7; n++ {
		if Observable(r, c.Date, n, obsNow) {
			t.Errorf("day %d reads as observable; day 3 is today and still in progress", n)
		}
	}
}

// One rule, every surface: whatever Observable says must be exactly what the JSON nulls and
// what the summary denominator excludes. The grid contradicting /v1/retention on the same
// instant is the failure this predicate exists to make impossible.
func TestObservable_AgreesWithSerializeAndPeriodN(t *testing.T) {
	for _, bucket := range []string{"day", "week", "month"} {
		r := obsCohort(4, bucket, 7)
		ser := SerializeCohorts(r, obsNow)
		if len(ser) != len(r.Cohorts) {
			t.Fatalf("%s: serialized %d cohorts, have %d", bucket, len(ser), len(r.Cohorts))
		}
		for i, c := range r.Cohorts {
			for n := range c.Returned {
				gotNull := ser[i].Returned[n] == nil
				wantNull := !Observable(r, c.Date, n, obsNow)
				if gotNull != wantNull {
					t.Errorf("%s bucket, cohort %s period %d: serialized-null=%v but Observable says unobservable=%v",
						bucket, c.Date.Format("2006-01-02"), n, gotNull, wantNull)
				}
			}
		}
		// no cohort is old enough for period 1, so the summary must report NO denominator —
		// not a 0% built from people who could not possibly have returned yet
		if ret, size := PeriodN(r, 1, obsNow); size != 0 || ret != 0 {
			t.Errorf("%s bucket: PeriodN(1) = %d/%d, want 0/0 — the only cohort's period 1 has not elapsed",
				bucket, ret, size)
		}
	}
}
