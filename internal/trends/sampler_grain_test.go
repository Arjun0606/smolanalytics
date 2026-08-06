package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// EVERY ENTRY POINT DROPS THIS TOOL'S OWN WRITES, NOT JUST THE DEFAULT ONE.
//
// Compute filtered the sampler. ComputeInterval, ComputeBreakdown, ComputeMeasure,
// ComputeMeasureBreakdown and ComputeXAU did not. Same package, same input, same question — so
// /v1/trends answered one number at the default daily grain and a different one at interval=hour,
// and a breakdown of a total did not sum to the total printed above it.
//
// This is the third appearance of one bug: a filter that lives at one call site instead of at the
// boundary. It cost a live dashboard "Day-1 retention 4% of 114 users" beside an ask bar saying
// "6% ... of 119 users", and it cost /v1/web 1,779 visitors against /v1/rows' 1,753. The guard has
// to be an equality between surfaces, because each number looks perfectly reasonable alone.
func seed(t *testing.T) ([]event.Event, time.Time, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Hour)
	from := now.AddDate(0, 0, -7)
	var evs []event.Event
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h += 4 {
			ts := from.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			// real users
			evs = append(evs, event.Event{
				Name: "$pageview", DistinctID: "u" + string(rune('a'+d)), Timestamp: ts,
				Properties: map[string]any{"plan": "pro"},
			})
			// this tool's own robot, on the same schedule, under its synthetic id
			evs = append(evs, event.Event{
				Name: "$ai_crawl", DistinctID: "$crawler", Timestamp: ts,
				Properties: map[string]any{"plan": "pro"},
			})
			evs = append(evs, event.Event{
				Name: "$geo_check", DistinctID: "$sampler", Timestamp: ts,
				Properties: map[string]any{"plan": "pro"},
			})
		}
	}
	return evs, from, now.Add(time.Hour)
}

// Asking for "all events" must mean the same thing however you bucket it.
func TestEveryGrainCountsTheSameEvents(t *testing.T) {
	evs, from, to := seed(t)

	daily := Compute(evs, "", from, to, false)
	total := 0
	for _, p := range daily.Points {
		total += p.Count
	}

	for _, iv := range []Interval{Hour, Day, Week, Month} {
		got := ComputeInterval(evs, "", from, to, false, iv)
		sum := 0
		for _, p := range got.Points {
			sum += p.Count
		}
		if sum != total {
			t.Errorf("interval %v totals %d, the daily default totals %d — the same question "+
				"answered differently depending on how it is bucketed", iv, sum, total)
		}
	}
}

// A breakdown must sum to the total it breaks down. It could not while one path filtered the
// sampler and the other did not.
func TestBreakdownSumsToTheTotal(t *testing.T) {
	evs, from, to := seed(t)

	total := 0
	for _, p := range Compute(evs, "", from, to, false).Points {
		total += p.Count
	}
	sum := 0
	for _, s := range ComputeBreakdown(evs, "", "plan", from, to, false) {
		for _, p := range s.Points {
			sum += p.Count
		}
	}
	if sum != total {
		t.Errorf("the breakdown sums to %d under a total of %d — segments that do not add up to "+
			"their own total is the clearest possible signal that a number cannot be trusted",
			sum, total)
	}
}

// Rolling actives must never count our own robot as a loyal daily user. It arrives on a schedule
// and never misses a day, so it is the single most inflating identity possible here.
func TestXAUNeverCountsOurOwnRobotAsAPerson(t *testing.T) {
	evs, from, to := seed(t)
	for _, w := range []int{1, 7, 30} {
		got := ComputeXAU(evs, "", from, to, w)
		for _, p := range got.Points {
			if p.Count > 7 {
				t.Fatalf("window=%d day %s counts %d actives, but only 7 real people exist in the "+
					"fixture — the sampler is being counted as a person", w, p.Date.Format("2006-01-02"), p.Count)
			}
		}
	}
}

// And the sampler's own reports must still be able to see the sampler's events, or the filter has
// simply emptied the panes that exist to show them.
func TestTheCrawlerReportCanStillSeeCrawlerEvents(t *testing.T) {
	evs, from, to := seed(t)
	got := Compute(evs, "$ai_crawl", from, to, false)
	sum := 0
	for _, p := range got.Points {
		sum += p.Count
	}
	if sum == 0 {
		t.Fatal("asking for $ai_crawl returned nothing — the filter ate its own input, which is " +
			"the failure that turns a working AI-crawler pane into a permanent zero")
	}
}
