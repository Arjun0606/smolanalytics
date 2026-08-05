package formula

import (
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }

func series(name string, days []int, vals []float64) Series {
	s := Series{Name: name, Values: vals}
	for _, d := range days {
		s.Dates = append(s.Dates, day(d))
	}
	return s
}

func TestArithmeticAndPrecedence(t *testing.T) {
	in := []Series{
		series("A", []int{1, 2}, []float64{10, 20}),
		series("B", []int{1, 2}, []float64{2, 5}),
	}
	for _, tc := range []struct {
		expr string
		want []float64
	}{
		{"A / B", []float64{5, 4}},
		{"A - B", []float64{8, 15}},
		{"A / B * 100", []float64{500, 400}},
		{"(A - B) / B * 100", []float64{400, 300}},
		{"-B + A", []float64{8, 15}},
		{"A / (B * 2)", []float64{2.5, 2}},
	} {
		r, err := Evaluate(tc.expr, in)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		for i, w := range tc.want {
			if !r.Points[i].Defined || r.Points[i].Value != w {
				t.Errorf("%s bucket %d = %v (defined=%v), want %v", tc.expr, i, r.Points[i].Value, r.Points[i].Defined, w)
			}
		}
	}
}

// Rendering a divide-by-zero as 0 makes "nobody visited" and "nobody converted out of the people
// who visited" the same pixel, and someone ships on it.
func TestDivideByZeroIsUndefinedNotZero(t *testing.T) {
	r, err := Evaluate("A / B", []Series{
		series("A", []int{1, 2}, []float64{10, 7}),
		series("B", []int{1, 2}, []float64{0, 7}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Points[0].Defined {
		t.Error("10/0 was reported as a defined value")
	}
	if r.Points[0].Why == "" {
		t.Error("an undefined point carries no reason; a bare gap in a chart reads as a broken tool")
	}
	if !r.Points[1].Defined || r.Points[1].Value != 1 {
		t.Errorf("the next bucket should still compute: %+v", r.Points[1])
	}
	if r.Undefined != 1 || r.Defined != 1 {
		t.Errorf("counts = %d defined / %d undefined, want 1 and 1", r.Defined, r.Undefined)
	}
}

// Zipping series by INDEX would divide Tuesday by Wednesday whenever the two have different
// lengths — which happens the moment one event started being tracked later. Wrong in a way no eye
// can catch, so alignment is by date and unmatched buckets are dropped with a note.
func TestSeriesAreAlignedByDateNotIndex(t *testing.T) {
	r, err := Evaluate("A / B", []Series{
		series("A", []int{1, 2, 3}, []float64{10, 20, 30}), // three days
		series("B", []int{2, 3}, []float64{2, 3}),          // started a day later
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) != 2 {
		t.Fatalf("got %d points, want the 2 shared days", len(r.Points))
	}
	// day 2: 20/2 = 10, day 3: 30/3 = 10. Zipped by index it would be 10/2=5 and 20/3=6.67.
	for i, want := range []float64{10, 10} {
		if r.Points[i].Value != want {
			t.Errorf("bucket %d = %v, want %v — the series were combined by position, not by date",
				i, r.Points[i].Value, want)
		}
	}
	if r.Note == "" {
		t.Error("a dropped bucket was not mentioned; silently shortening a chart is how it stops matching its own axis")
	}
	if !r.Points[0].Date.Equal(day(2)) {
		t.Errorf("first surviving bucket is %v, want day 2", r.Points[0].Date)
	}
}

// A typo in a formula must not render a confident flat line.
func TestUnknownSeriesIsAnErrorNotZero(t *testing.T) {
	r, err := Evaluate("A / Bb", []Series{series("A", []int{1}, []float64{10})})
	if err != nil {
		return // erroring at parse time is also fine
	}
	if r.Defined > 0 {
		t.Error("a formula naming a series that does not exist produced defined values")
	}
	if r.Points[0].Why == "" || !contains(r.Points[0].Why, "Bb") {
		t.Errorf("the reason does not name the missing series: %q", r.Points[0].Why)
	}
}

// Infinity and NaN must never leave as numbers — they serialise as null or break a chart, and
// both read as a bug in the product rather than a hole in the data.
func TestNoInfinityOrNaNEscapes(t *testing.T) {
	r, err := Evaluate("A / B", []Series{
		series("A", []int{1}, []float64{0}),
		series("B", []int{1}, []float64{0}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Points[0].Defined {
		t.Errorf("0/0 came back defined as %v", r.Points[0].Value)
	}
}

// Series computed over different windows share nothing, and quietly returning an empty chart
// would look like "no data" rather than "these cannot be combined".
func TestDisjointSeriesErrorRatherThanReturnEmpty(t *testing.T) {
	_, err := Evaluate("A / B", []Series{
		series("A", []int{1, 2}, []float64{1, 2}),
		series("B", []int{9, 10}, []float64{1, 2}),
	})
	if err == nil {
		t.Fatal("two series with no shared buckets were combined into an empty chart instead of erroring")
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
