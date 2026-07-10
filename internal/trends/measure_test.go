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
	// day 0: 10, 20  | day 1: 30
	evs := []event.Event{evAmt(0, 10.0), evAmt(0, 20.0), evAmt(1, 30.0)}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		m         Measure
		wantTotal float64
		wantDay0  float64
	}{
		{Sum, 60, 30},
		{Avg, 20, 15},   // window avg = (10+20+30)/3 = 20, NOT (15+30)/2 = 22.5
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

func TestComputeMeasure_SkipsMissingAndNonNumeric(t *testing.T) {
	evs := []event.Event{
		evAmt(0, 10.0),
		evAmt(0, "not-a-number"),                 // skipped
		{Name: "checkout", DistinctID: "u", Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Properties: map[string]any{"other": 99}}, // no amount, skipped
		evAmt(0, "29.99"),                        // numeric string, parsed
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
	for in, want := range map[string]Measure{"sum": Sum, "avg": Avg, "average": Avg, "mean": Avg, "p90": P90, "p95": P90, "median": Median} {
		got, ok := ParseMeasure(in)
		if !ok || got != want {
			t.Errorf("ParseMeasure(%q) = %v,%v want %v,true", in, got, ok, want)
		}
	}
	if m, ok := ParseMeasure("garbage"); ok || m != Sum {
		t.Errorf("ParseMeasure(garbage) = %v,%v want sum,false", m, ok)
	}
}
