package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/retention"
)

// The retention grid and /v1/retention answer the same question over the same Result, so a
// cell the JSON reports as null (period not finished — nothing to report yet) must be blank
// on the page. It was not: the grid measured period offsets in DAYS while `d` counted
// PERIODS, so on a weekly grid a same-week cohort rendered W1..W5 as a finished "0%" for
// weeks that begin days in the FUTURE, and on any grid the period that started minutes ago
// printed as a settled number. Both are churn the product never observed.
//
// This test is the agreement itself: for every cohort and every period, blank on the page
// iff null in the JSON.
func TestRetentionGridBlanksExactlyWhatTheAPINulls(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// 20 users active this week (their cohort is the CURRENT week/day — nothing past period 0
	// can have happened yet), plus a user from three weeks ago who came back once.
	var evs []event.Event
	for i := 0; i < 20; i++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("fresh_%d", i),
			Timestamp: now.Add(-2 * time.Hour),
		})
	}
	evs = append(evs,
		event.Event{Name: "$pageview", DistinctID: "old", Timestamp: now.Add(-21 * day)},
		event.Event{Name: "$pageview", DistinctID: "old", Timestamp: now.Add(-7 * day)},
	)

	for _, bucket := range []string{"day", "week", "month"} {
		rr := retention.ComputeBucketed(evs, 7, "", bucket, false)
		_, rows, _ := retentionGrid(rr, now)
		api := retention.SerializeCohorts(rr, now)

		// rows are newest-cohort-first and capped at 12; walk the API list the same way.
		start := 0
		if len(api) > 12 {
			start = len(api) - 12
		}
		if len(rows) != len(api)-start {
			t.Fatalf("%s: grid has %d rows, API has %d cohorts", bucket, len(rows), len(api)-start)
		}
		for ri, row := range rows {
			cj := api[len(api)-1-ri]
			if row.Size == 0 {
				continue // an empty cohort has no rate to show; the JSON still carries the row
			}
			for d, cell := range row.Cells {
				apiNull := d >= len(cj.Returned) || cj.Returned[d] == nil
				if cell.Empty != apiNull {
					t.Errorf("%s bucket, cohort %s, period %d: grid empty=%v (label %q) but API null=%v — the page and /v1/retention disagree about whether this period has happened",
						bucket, row.Date, d, cell.Empty, cell.Label, apiNull)
				}
			}
		}
		t.Logf("%s: %d cohorts agree cell-for-cell with SerializeCohorts", bucket, len(rows))
	}
}

// The narrower statement of the weekly failure, spelled out so a regression names itself:
// a cohort that formed this week has no observable week 1, and the grid must not print one.
func TestWeeklyGridNeverRendersAFuturePeriodAsChurn(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 20; i++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("u%d", i),
			Timestamp: now.Add(-2 * time.Hour),
		})
	}
	rr := retention.ComputeBucketed(evs, 7, "", "week", false)
	_, rows, ready := retentionGrid(rr, now)
	if len(rows) != 1 {
		t.Fatalf("want 1 cohort, got %d", len(rows))
	}
	for d, c := range rows[0].Cells {
		if d == 0 {
			continue // period 0 is the cohort itself, always known
		}
		if !c.Empty {
			t.Errorf("W%d rendered %q for a week that has not begun (cohort %s, now %s)", d, c.Label, rows[0].Date, now.Format(time.RFC3339))
		}
	}
	if ready {
		t.Error("grid declared READY on a cohort whose only observable period is its own baseline")
	}
}

// The in-progress period, on the plainest grid there is. A cohort from two days ago viewed
// twenty minutes after midnight has an observable D1 and an unfinished D2; the grid used to
// print D2 as a final number, which read as a collapse from 50% to 10%.
func TestDailyGridBlanksTheDayStillRunning(t *testing.T) {
	now := time.Date(2026, 8, 4, 0, 20, 0, 0, time.UTC)
	day := 24 * time.Hour
	var evs []event.Event
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("u%d", i)
		evs = append(evs, event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-2 * day)})
		if i < 5 { // half return on D1
			evs = append(evs, event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-1 * day)})
		}
		if i < 1 { // one returns today, 20 minutes into D2
			evs = append(evs, event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-5 * time.Minute)})
		}
	}
	rr := retention.ComputeBucketed(evs, 7, "", "day", false)
	_, rows, _ := retentionGrid(rr, now)
	var cohort *retRow
	for i := range rows {
		if rows[i].Size == 10 {
			cohort = &rows[i]
		}
	}
	if cohort == nil {
		t.Fatalf("cohort of 10 not found in %+v", rows)
	}
	if cohort.Cells[1].Empty || cohort.Cells[1].Label != "50%" {
		t.Errorf("D1 is fully elapsed and should read 50%%, got empty=%v %q", cohort.Cells[1].Empty, cohort.Cells[1].Label)
	}
	if !cohort.Cells[2].Empty {
		t.Errorf("D2 started %v ago and is still collecting, but the grid printed %q as a finished number",
			now.Sub(now.Truncate(24*time.Hour)), cohort.Cells[2].Label)
	}
}
