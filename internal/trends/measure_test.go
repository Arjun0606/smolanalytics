package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func evAmt(day int, amount any) event.Event {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return event.Event{
		Name:       "checkout",
		DistinctID: "u",
		Timestamp:  base.AddDate(0, 0, day),
		Properties: map[string]any{"amount": amount},
	}
}

func TestComputeMeasure_Aggregations(t *testing.T) {
	// day 0: 10, 20  | day 1: 30. Window [Jan 1 00:00, Jan 3 00:00) spans both days
	// (events land at noon), so all three are in-window: Total over all 3, 2 day buckets.
	evs := []event.Event{evAmt(0, 10.0), evAmt(0, 20.0), evAmt(1, 30.0)}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		m         Measure
		wantTotal float64
		wantDay0  float64
	}{
		{Sum, 60, 30},
		{Avg, 20, 15}, // window avg = (10+20+30)/3 = 20, NOT (15+30)/2 = 22.5
		{Min, 10, 10},
		{Max, 30, 20},
		{Median, 20, 15}, // window median of [10,20,30] = 20; day0 [10,20] = 15
		{P90, 30, 20},    // nearest-rank p90 of [10,20,30] = 30; day0 [10,20] = 20
	}
	for _, c := range cases {
		r := ComputeMeasure(evs, "checkout", "amount", c.m, from, to)
		if r.Total != c.wantTotal {
			t.Errorf("%s Total = %v, want %v", c.m, r.Total, c.wantTotal)
		}
		if len(r.Points) != 2 {
			t.Fatalf("%s: got %d points, want 2 (continuous day span)", c.m, len(r.Points))
		}
		if r.Points[0].Value != c.wantDay0 {
			t.Errorf("%s day0 = %v, want %v", c.m, r.Points[0].Value, c.wantDay0)
		}
		if r.N != 3 {
			t.Errorf("%s N = %d, want 3", c.m, r.N)
		}
	}
}

// TestComputeMeasure_WindowExcludesOutOfWindow is the regression guard for the covenant
// bug an adversarial audit surfaced: res.Total/res.N were built from an UNfiltered slice,
// so a windowed money query (measure=sum&days=1) silently returned the ALL-TIME aggregate —
// disagreeing with the ask bar and with the result's own (windowed) per-day points. The
// window must bound the aggregate exactly like [from, to) bounds Compute() and the points.
func TestComputeMeasure_WindowExcludesOutOfWindow(t *testing.T) {
	// day 0: 10 | day 1: 20 | day 2: 1000 (a big out-of-window outlier)
	evs := []event.Event{evAmt(0, 10.0), evAmt(1, 20.0), evAmt(2, 1000.0)}
	// window = day 1 only: [Jan 2 00:00, Jan 3 00:00)
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	for _, m := range []Measure{Sum, Avg, Min, Max, Median, P90} {
		r := ComputeMeasure(evs, "checkout", "amount", m, from, to)
		// only day 1's single event (20) is in-window: every aggregate must be 20, N must be 1.
		if r.Total != 20 {
			t.Errorf("%s Total = %v, want 20 (windowed) — NOT 1030 (all-time) or any out-of-window value", m, r.Total)
		}
		if r.N != 1 {
			t.Errorf("%s N = %d, want 1 (only the in-window event)", m, r.N)
		}
		if len(r.Points) != 1 {
			t.Errorf("%s: got %d points, want 1 (single in-window day)", m, len(r.Points))
		}
	}
}

func TestComputeMeasure_SkipsMissingAndNonNumeric(t *testing.T) {
	evs := []event.Event{
		evAmt(0, 10.0),
		evAmt(0, "not-a-number"), // skipped
		{Name: "checkout", DistinctID: "u", Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Properties: map[string]any{"other": 99}}, // no amount, skipped
		evAmt(0, "29.99"), // numeric string, parsed
	}
	r := ComputeMeasure(evs, "checkout", "amount", Sum, time.Time{}, time.Time{})
	if d := r.Total - 39.99; d > 1e-9 || d < -1e-9 { // float tolerance
		t.Errorf("Total = %v, want ~39.99 (numeric string counted, junk skipped)", r.Total)
	}
	if r.N != 2 {
		t.Errorf("N = %d, want 2 (only the two numeric values)", r.N)
	}
}

func TestComputeMeasure_EmptyIsZeroNotFabricated(t *testing.T) {
	r := ComputeMeasure(nil, "checkout", "amount", Sum, time.Time{}, time.Time{})
	if r.Total != 0 || r.N != 0 || len(r.Points) != 0 {
		t.Errorf("empty input should give a zero result, got total=%v n=%d points=%d", r.Total, r.N, len(r.Points))
	}
}

func TestComputeMeasure_MedianEvenCount(t *testing.T) {
	// [10,20,30,40] median = (20+30)/2 = 25
	evs := []event.Event{evAmt(0, 10.0), evAmt(0, 20.0), evAmt(0, 30.0), evAmt(0, 40.0)}
	r := ComputeMeasure(evs, "checkout", "amount", Median, time.Time{}, time.Time{})
	if r.Total != 25 {
		t.Errorf("even-count median = %v, want 25", r.Total)
	}
}

func TestParseMeasure(t *testing.T) {
	// p95/p99 are REAL percentiles — they used to alias to p90, silently answering the
	// wrong tail value for exactly the latency questions that ask for them.
	for in, want := range map[string]Measure{"sum": Sum, "avg": Avg, "average": Avg, "mean": Avg, "p90": P90, "p95": P95, "p99": P99, "median": Median} {
		got, ok := ParseMeasure(in)
		if !ok || got != want {
			t.Errorf("ParseMeasure(%q) = %v,%v want %v,true", in, got, ok, want)
		}
	}
	if m, ok := ParseMeasure("garbage"); ok || m != Sum {
		t.Errorf("ParseMeasure(garbage) = %v,%v want sum,false", m, ok)
	}
}

// TestPercentilesAreDistinct pins nearest-rank p90/p95/p99 on a 100-value distribution where
// each returns a DIFFERENT value — the p95→p90 alias would fail this immediately.
func TestPercentilesAreDistinct(t *testing.T) {
	var evs []event.Event
	for i := 1; i <= 100; i++ {
		evs = append(evs, evAmt(0, float64(i)))
	}
	for m, want := range map[Measure]float64{P90: 90, P95: 95, P99: 99} {
		if r := ComputeMeasure(evs, "checkout", "amount", m, time.Time{}, time.Time{}); r.Total != want {
			t.Errorf("%s over 1..100 = %v, want %v", m, r.Total, want)
		}
	}
}

// TestComputeMeasureBreakdown pins the per-group aggregate ("revenue by plan"): one row per
// breakdown value including "(none)", each group's Total computed over its own values. The
// measure+breakdown combination used to silently drop the breakdown and return the grand
// total with no series and no error.
func TestComputeMeasureBreakdown(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mk := func(amount float64, plan string) event.Event {
		props := map[string]any{"amount": amount}
		if plan != "" {
			props["plan"] = plan
		}
		return event.Event{Name: "checkout", DistinctID: "u", Timestamp: base, Properties: props}
	}
	evs := []event.Event{mk(100, "pro"), mk(150, "pro"), mk(30, "free"), mk(20, "free"), mk(50, "")}
	rows := ComputeMeasureBreakdown(evs, "checkout", "amount", Sum, "plan", time.Time{}, time.Time{})
	want := map[string]float64{"pro": 250, "free": 50, "(none)": 50}
	if len(rows) != 3 {
		t.Fatalf("want 3 groups, got %d: %+v", len(rows), rows)
	}
	sum := 0.0
	for _, r := range rows {
		if r.Total != want[r.Value] {
			t.Errorf("group %q = %v, want %v", r.Value, r.Total, want[r.Value])
		}
		sum += r.Total
	}
	if sum != 350 { // groups must sum to the unbroken total — no dropped or double-counted events
		t.Errorf("groups sum to %v, want 350 (the grand total)", sum)
	}
}
