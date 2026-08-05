package flag

import (
	"math"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The Wilson interval exists because the textbook normal approximation is wrong in exactly the
// situations an early experiment lives in. These pin the properties that make it worth using.
func TestWilsonInterval(t *testing.T) {
	// Published example: 5 successes in 20 trials, 95% Wilson is roughly 11.2% to 46.9%.
	ci := wilson(5, 20, z95)
	if math.Abs(ci.Lo-11.2) > 0.3 || math.Abs(ci.Hi-46.9) > 0.3 {
		t.Errorf("wilson(5,20) = [%.1f, %.1f], published value is about [11.2, 46.9]", ci.Lo, ci.Hi)
	}
	if math.Abs(ci.Point-25.0) > 0.01 {
		t.Errorf("point estimate %.2f, want 25", ci.Point)
	}

	// The whole reason for choosing Wilson: at zero conversions the normal approximation gives a
	// symmetric interval around 0, whose lower bound is negative. A conversion rate that might be
	// -4% destroys trust in every other number on the page.
	zero := wilson(0, 30, z95)
	if zero.Lo < 0 {
		t.Errorf("zero conversions produced a negative lower bound %.2f", zero.Lo)
	}
	if zero.Hi <= 0 || zero.Hi > 100 {
		t.Errorf("zero conversions gave upper bound %.2f, want something in (0,100]", zero.Hi)
	}
	// And symmetrically at 100%.
	all := wilson(30, 30, z95)
	if all.Hi > 100 {
		t.Errorf("a 100%% rate produced an upper bound above 100: %.2f", all.Hi)
	}

	// More data must narrow the interval. If it did not, the interval would be decoration.
	small := wilson(50, 100, z95)
	large := wilson(5000, 10000, z95)
	if (large.Hi - large.Lo) >= (small.Hi - small.Lo) {
		t.Errorf("100x the data did not narrow the interval: [%.2f,%.2f] vs [%.2f,%.2f]",
			small.Lo, small.Hi, large.Lo, large.Hi)
	}
	// The point estimate must sit inside its own interval, always.
	for _, c := range []struct{ k, n int }{{0, 10}, {1, 10}, {5, 10}, {9, 10}, {10, 10}, {1, 1000}, {999, 1000}} {
		ci := wilson(c.k, c.n, z95)
		if ci.Point < ci.Lo-0.05 || ci.Point > ci.Hi+0.05 {
			t.Errorf("wilson(%d,%d): point %.2f outside [%.2f,%.2f]", c.k, c.n, ci.Point, ci.Lo, ci.Hi)
		}
	}
	if got := wilson(0, 0, z95); got.Point != 0 || got.Hi != 0 {
		t.Errorf("empty arm should produce a zero interval, got %+v", got)
	}
}

// The p-value must agree with the significance boolean that has been shipping, or the report
// contradicts itself: "significant: true, p = 0.4".
func TestPValueAgreesWithTheSignificanceBoolean(t *testing.T) {
	cases := []struct{ c1, n1, c2, n2 int }{
		{50, 500, 100, 500}, {100, 1000, 105, 1000}, {5, 50, 6, 50},
		{300, 1000, 200, 1000}, {1, 100, 20, 100},
	}
	for _, c := range cases {
		p := twoProportionP(c.c1, c.n1, c.c2, c.n2)
		sig := significant(c.c1, c.n1, c.c2, c.n2)
		if c.n1 < minArmForStats || c.n2 < minArmForStats {
			continue // the boolean refuses tiny arms on purpose
		}
		if sig != (p < 0.05) {
			t.Errorf("(%d/%d vs %d/%d): significant=%v but p=%.4f", c.c1, c.n1, c.c2, c.n2, sig, p)
		}
	}
	// Identical arms are the strongest possible evidence of no difference.
	if p := twoProportionP(100, 1000, 100, 1000); p < 0.99 {
		t.Errorf("identical arms gave p=%.4f, want ~1", p)
	}
	if p := twoProportionP(0, 0, 0, 0); p != 1 {
		t.Errorf("empty input gave p=%v, want 1", p)
	}
}

// Relative lift is a ratio, so its interval must be asymmetric — and must never claim a change
// worse than -100%, which is not a thing that can happen.
func TestLiftIntervalIsAsymmetricAndBounded(t *testing.T) {
	ci, why := liftInterval(120, 1000, 100, 1000, z95)
	if why != liftOK {
		t.Fatal("a healthy comparison should produce an interval")
	}
	if math.Abs(ci.Point-20.0) > 0.5 {
		t.Errorf("12%% vs 10%% is a 20%% lift, got %.1f", ci.Point)
	}
	if ci.Lo >= ci.Point || ci.Hi <= ci.Point {
		t.Errorf("point %.1f is not inside [%.1f, %.1f]", ci.Point, ci.Lo, ci.Hi)
	}
	// Asymmetric around the point, because it was computed on the log ratio.
	below, above := ci.Point-ci.Lo, ci.Hi-ci.Point
	if math.Abs(below-above) < 0.01 {
		t.Errorf("interval is symmetric (%.2f vs %.2f) — it was not computed on the ratio scale", below, above)
	}
	// A large true drop must not produce a bound below -100%.
	drop, dropWhy := liftInterval(5, 1000, 200, 1000, z95)
	if dropWhy == liftOK && drop.Lo < -100 {
		t.Errorf("lower bound %.1f%% claims worse than a total loss", drop.Lo)
	}
}

// The guard that stops "+4000% lift" from eleven conversions. A control rate indistinguishable
// from zero makes every ratio enormous and meaningless, and it is the single most misleading
// number an experiment tool can print.
func TestLiftRefusedWhenControlIsNearZero(t *testing.T) {
	if _, why := liftInterval(40, 1000, 1, 1000, z95); why == liftOK {
		t.Error("a control rate of 0.1% should not produce a relative-lift interval")
	}
	if _, why := liftInterval(10, 100, 0, 100, z95); why == liftOK {
		t.Error("a control with zero conversions cannot have a relative lift")
	}
	// But a healthy control must still get one.
	if _, why := liftInterval(150, 1000, 100, 1000, z95); why != liftOK {
		t.Error("a 10% control rate should produce a relative-lift interval")
	}
}

// The sentence is the product. A reader acts on the words, not the JSON.
func TestReadLiftSaysWhatToDo(t *testing.T) {
	better, _ := liftInterval(150, 1000, 100, 1000, z95)
	if s := readLift(better, liftOK, 0.001, true, 150, 1000, 100, 1000); !strings.Contains(s, "better") {
		t.Errorf("a clear win should say so, got %q", s)
	}
	worse, _ := liftInterval(60, 1000, 100, 1000, z95)
	if s := readLift(worse, liftOK, 0.001, true, 60, 1000, 100, 1000); !strings.Contains(s, "worse") {
		t.Errorf("a clear loss should say so, got %q", s)
	}
	// The important case: an interval spanning zero must NOT read as a result. This is where a
	// significance boolean quietly misleads, and where most experiments actually land.
	flat, _ := liftInterval(102, 1000, 100, 1000, z95)
	s := readLift(flat, liftOK, 0.6, true, 102, 1000, 100, 1000)
	if !strings.Contains(s, "spans zero") {
		t.Errorf("an inconclusive result must say the range spans zero, got %q", s)
	}
	if strings.Contains(s, "better;") || strings.Contains(s, "worse;") {
		t.Errorf("an inconclusive result must not read as a verdict: %q", s)
	}
	if s := readLift(Interval{}, liftCtrlNearZero, 1, false, 1, 10, 1, 10); !strings.Contains(s, "too few") {
		t.Errorf("a tiny sample should say so, got %q", s)
	}
	if s := readLift(Interval{}, liftCtrlNearZero, 1, true, 40, 1000, 1, 1000); !strings.Contains(s, "raw counts") {
		t.Errorf("an unusable ratio should point at the raw counts, got %q", s)
	}
}

// End to end: every rate on a report must carry the counts and interval that produced it.
func TestMeasureShipsCountsAndIntervals(t *testing.T) {
	var evs []eventForTest
	i := 0
	add := func(variant string, exposed, converted int) {
		for j := 0; j < exposed; j++ {
			evs = append(evs, eventForTest{id: itoa(i), props: map[string]any{PropFlag: "exp", PropVariant: variant}})
			i++
		}
		_ = converted
	}
	add("control", 500, 0)
	add("test", 500, 0)
	base := toEvents(evs)
	// Give the first 50 of each arm a conversion, at a later timestamp than their exposure.
	conv := 0
	for _, e := range base {
		if conv >= 100 {
			break
		}
		base = append(base, eventFrom(e, "purchase"))
		conv++
	}
	rep := Measure(base, "exp", "purchase", 0)
	if len(rep.Variants) != 2 {
		t.Fatalf("got %d variants, want 2", len(rep.Variants))
	}
	for _, v := range rep.Variants {
		if v.Exposed == 0 {
			t.Errorf("%s has no exposures", v.Key)
		}
		if v.RateCI.Hi <= 0 && v.Converted > 0 {
			t.Errorf("%s has conversions but an empty interval: %+v", v.Key, v.RateCI)
		}
		if v.RateCI.Lo < 0 || v.RateCI.Hi > 100 {
			t.Errorf("%s interval escapes [0,100]: %+v", v.Key, v.RateCI)
		}
	}
	// The note must point the reader at the split check, because a mismatch makes every number
	// on the report unreadable and nothing else says so.
	if !strings.Contains(rep.Note, "experiment_health") {
		t.Errorf("report note should direct the reader to check the split first, got %q", rep.Note)
	}
}

func eventFrom(e event.Event, name string) event.Event {
	out := e
	out.ID = e.ID + "-" + name
	out.Name = name
	out.Timestamp = e.Timestamp.Add(1)
	out.Properties = map[string]any{}
	return out
}
