package trends

// Regressions for the display-cap and label defects found in the 2026-08-04 hunt: the hourly
// bucket guard that truncated the Total and kept the OLDEST hours, the all-time day cap that
// summed the Total only over emitted buckets, the Compute/ComputeMeasure pair answering the
// same capped window in opposite ways, and a JSON null getting its own breakdown label here
// while query.Breakdown filed it under "(none)".

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// WINDOW-2, at hour grain. ?days=60&interval=hour used to return total 744 where the same
// window at day and week grain returned 1440 — the cap was a `break` inside the loop that also
// accumulated the Total — and the 744 buckets it did emit were the OLDEST ones, so on a 60-day
// window the chart rendered the first month and silently dropped the most recent one.
func TestHourGrainCapKeepsRecentBucketsAndTheWholeTotal(t *testing.T) {
	to := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -60)
	var evs []event.Event
	for h := 0; h < 60*24; h++ {
		evs = append(evs, event.Event{
			ID: string(rune(h)), Name: "signup", DistinctID: "u1",
			Timestamp: from.Add(time.Duration(h) * time.Hour),
		})
	}

	day := ComputeInterval(evs, "signup", from, to, false, Day)
	week := ComputeInterval(evs, "signup", from, to, false, Week)
	hour := ComputeInterval(evs, "signup", from, to, false, Hour)

	if day.Total != 1440 || week.Total != 1440 || hour.Total != 1440 {
		t.Errorf("same window, three grains: day=%d week=%d hour=%d — buckets of one window must sum to one total",
			day.Total, week.Total, hour.Total)
	}
	if len(hour.Points) != 744 {
		t.Errorf("hour grain emitted %d points, want the 744-point (31-day) display cap", len(hour.Points))
	}
	if !hour.Truncated {
		t.Error("hour grain trimmed 29 days of buckets without setting Truncated — the chart and the total silently disagree")
	}
	// the surviving window must be the RECENT end: last bucket is the final hour before `to`.
	lastWant := to.Add(-time.Hour)
	if got := hour.Points[len(hour.Points)-1].Date; !got.Equal(lastWant) {
		t.Errorf("hour grain's last bucket = %s, want %s — the cap kept the oldest hours and dropped the newest month",
			got.Format(time.RFC3339), lastWant.Format(time.RFC3339))
	}
	if got, want := hour.Points[0].Date, lastWant.Add(-743*time.Hour); !got.Equal(want) {
		t.Errorf("hour grain's first bucket = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	// day grain is uncapped over 60 days and must not have been flagged
	if day.Truncated {
		t.Error("day grain over 60 days reported Truncated")
	}
}

// The all-time span cap is derived from the earliest event seen, so ONE event at the 2000-01-01
// ingest floor pushes `lo` past every real 2026 day. The Total used to be accumulated inside the
// emit loop, so those real days were never counted: 31 matching events reported total=1, with no
// error and no warning.
func TestAllTimeTotalCountsCappedOutDays(t *testing.T) {
	ancient := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) // the ingest floor: storable, and >4200 days back
	recent := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{{ID: "old", Name: "view", DistinctID: "u0", Timestamp: ancient}}
	for i := 0; i < 30; i++ {
		evs = append(evs, event.Event{
			ID: "r" + string(rune('a'+i)), Name: "view", DistinctID: "u1",
			Timestamp: recent.AddDate(0, 0, i%15),
		})
	}

	r := Compute(evs, "view", time.Time{}, time.Time{}, false)
	if r.Total != 31 {
		t.Errorf("all-time total = %d for 31 matching events — the display cap changed the answer", r.Total)
	}
	if !r.Truncated {
		t.Error("the span cap dropped the ancient bucket without setting Truncated")
	}
	if len(r.Points) != maxDayBuckets+1 {
		t.Errorf("emitted %d points, want the %d-bucket cap", len(r.Points), maxDayBuckets+1)
	}
	// unique counts were always window-wide; they must stay that way.
	if u := Compute(evs, "view", time.Time{}, time.Time{}, true); u.Total != 2 {
		t.Errorf("all-time unique total = %d, want 2 users", u.Total)
	}
}

// Compute and ComputeMeasure used to make OPPOSITE choices about capped-out days — the count
// trend shrank its Total to the emitted buckets while the measure aggregated the whole window —
// so "how many buys" and "how much revenue" described different populations of the same events.
func TestCountAndMeasureAgreeOnCappedWindow(t *testing.T) {
	ancient := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{ID: "1", Name: "buy", DistinctID: "u1", Timestamp: ancient, Properties: map[string]any{"amount": 1000.0}},
		{ID: "2", Name: "buy", DistinctID: "u2", Timestamp: recent, Properties: map[string]any{"amount": 5.0}},
	}

	count := Compute(evs, "buy", time.Time{}, time.Time{}, false)
	sum := ComputeMeasure(evs, "buy", "amount", Sum, time.Time{}, time.Time{})

	if count.Total != sum.N {
		t.Errorf("count trend total = %d but measure N = %d over the identical events — "+
			"the two endpoints capped the same window differently", count.Total, sum.N)
	}
	if sum.Total != 1005 {
		t.Errorf("sum(amount) = %v, want 1005 (the window aggregate, cap or no cap)", sum.Total)
	}
	if !sum.Truncated || !count.Truncated {
		t.Errorf("Truncated: count=%v measure=%v — both trimmed the oldest bucket and must say so, "+
			"or the headline number reads as contradicting its own chart", count.Truncated, sum.Truncated)
	}
}

// One group, two labels. trends carried its own stringifier (valueOf) that rendered a JSON null
// as "<nil>" while query.Breakdown rendered "", so track("signup", {plan: null}) produced a
// different segment name depending on which endpoint you asked, and the drill-down filter the
// legend generates (f=plan:eq:<nil>) matched neither.
func TestBreakdownLabelsNullAsNoneLikeQuery(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		{ID: "1", Name: "signup", DistinctID: "a", Timestamp: t0, Properties: map[string]any{"plan": nil, "amount": 1.0}},
		{ID: "2", Name: "signup", DistinctID: "b", Timestamp: t0, Properties: map[string]any{"plan": nil, "amount": 2.0}},
		{ID: "3", Name: "signup", DistinctID: "c", Timestamp: t0, Properties: map[string]any{"plan": "pro", "amount": 4.0}},
		{ID: "4", Name: "signup", DistinctID: "d", Timestamp: t0, Properties: map[string]any{"amount": 8.0}}, // genuinely missing
	}

	trend := map[string]int{}
	for _, s := range ComputeBreakdown(evs, "signup", "plan", time.Time{}, time.Time{}, false) {
		trend[s.Value] = s.Total
	}
	card := map[string]int{}
	for _, g := range query.Breakdown(evs, "plan") {
		card[g.Value] = g.Count
	}
	for _, label := range []string{"(none)", "pro"} {
		if trend[label] != card[label] {
			t.Errorf("group %q: /v1/trends?breakdown= = %d, /v1/breakdown = %d — same events, two labellings",
				label, trend[label], card[label])
		}
	}
	if trend["(none)"] != 3 || len(trend) != 2 {
		t.Errorf("trend breakdown by plan = %v, want {(none):3, pro:1} — a null carries no more information than a missing key", trend)
	}
	if _, bad := trend["<nil>"]; bad {
		t.Errorf("trend breakdown produced a \"<nil>\" group: %v", trend)
	}

	measure := map[string]float64{}
	for _, s := range ComputeMeasureBreakdown(evs, "signup", "amount", Sum, "plan", time.Time{}, time.Time{}) {
		measure[s.Value] = s.Total
	}
	if measure["(none)"] != 11 || len(measure) != 2 {
		t.Errorf("measure breakdown by plan = %v, want {(none):11, pro:4}", measure)
	}
}
