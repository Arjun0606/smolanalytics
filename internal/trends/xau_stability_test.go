package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A rolling XAU number whose value depends on WHAT TIME OF DAY you asked is not a number.
// The lookback floor subtracted calendar days from an unaligned `from`, so the earliest day in
// the window was collected only in part: ?measure=wau&hours=6 and ?measure=wau&days=1 returned
// different WAU counts over identical events. DAU/WAU/MAU is the one report whose entire job is
// to be comparable.
func TestXAULookbackDoesNotDependOnTimeOfDay(t *testing.T) {
	// seven days of history, one distinct user per day
	day0 := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	var evs []event.Event
	for d := 0; d < 7; d++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: "u" + string(rune('a'+d)),
			Timestamp: day0.AddDate(0, 0, d).Add(9 * time.Hour),
		})
	}
	lastDay := day0.AddDate(0, 0, 6)
	to := lastDay.Add(24 * time.Hour)

	// the same final day, asked with `from` at a series of different times of day
	var got []int
	for _, offset := range []time.Duration{0, 6 * time.Hour, 13 * time.Hour, 23 * time.Hour} {
		r := ComputeXAU(evs, "$pageview", lastDay.Add(offset), to, 7)
		if len(r.Points) == 0 {
			t.Fatalf("offset %v produced no points", offset)
		}
		got = append(got, r.Points[len(r.Points)-1].Count)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Errorf("WAU for the same day changes with the query's time of day: %v — "+
				"the lookback floor is not aligned to a whole UTC day", got)
			break
		}
	}
	// and it must actually be the 7-day rolling count, not a collapsed DAU
	if got[0] != 7 {
		t.Errorf("WAU = %d over seven days with one distinct user each, want 7", got[0])
	}
}
