package flag

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The chi-square tail is hand-implemented because the standard library has no incomplete gamma
// function. If it is wrong, every SRM verdict is wrong — and confidently so — which is worse
// than not having the check. These are published critical values: at each significance level the
// tail probability of the critical statistic must equal that level.
func TestChiSquareTailMatchesPublishedCriticalValues(t *testing.T) {
	cases := []struct {
		df    int
		x     float64
		wantP float64
	}{
		{1, 3.841, 0.05}, {1, 6.635, 0.01}, {1, 10.828, 0.001}, {1, 12.116, 0.0005},
		{2, 5.991, 0.05}, {2, 9.210, 0.01}, {2, 13.816, 0.001},
		{3, 7.815, 0.05}, {3, 11.345, 0.01}, {3, 16.266, 0.001},
		{4, 9.488, 0.05}, {4, 13.277, 0.01}, {4, 18.467, 0.001},
		{10, 18.307, 0.05}, {10, 23.209, 0.01}, {10, 29.588, 0.001},
	}
	for _, c := range cases {
		got := chiSquareP(c.x, c.df)
		if math.Abs(got-c.wantP) > c.wantP*0.02 {
			t.Errorf("chiSquareP(%.3f, df=%d) = %.6f, published value is %.4f", c.x, c.df, got, c.wantP)
		}
	}
	// A statistic of zero means a perfect fit, which can never be evidence against the split.
	if p := chiSquareP(0, 1); p != 1 {
		t.Errorf("chiSquareP(0, 1) = %v, want 1", p)
	}
	// The tail must decrease as the statistic grows, or the test would report more evidence for
	// a better fit.
	prev := 1.0
	for x := 0.5; x < 40; x += 0.5 {
		p := chiSquareP(x, 2)
		if p > prev {
			t.Fatalf("tail probability rose at x=%.1f (%.6f after %.6f)", x, p, prev)
		}
		prev = p
	}
}

func buildExposures(flagKey string, byVariant map[string]int, extra func(i int) map[string]any) []eventForTest {
	var out []eventForTest
	i := 0
	for variant, n := range byVariant {
		for j := 0; j < n; j++ {
			props := map[string]any{PropFlag: flagKey, PropVariant: variant}
			if extra != nil {
				for k, v := range extra(i) {
					props[k] = v
				}
			}
			out = append(out, eventForTest{id: fmt.Sprintf("u%d", i), props: props})
			i++
		}
	}
	return out
}

type eventForTest struct {
	id    string
	props map[string]any
}

func toEvents(in []eventForTest) []event.Event {
	out := make([]event.Event, 0, len(in))
	base := time.Now().UTC().Add(-time.Hour)
	for i, e := range in {
		out = append(out, event.Event{
			ID: fmt.Sprintf("e%d", i), Name: ExposureEvent, DistinctID: e.id,
			Timestamp: base.Add(time.Duration(i) * time.Millisecond), Properties: e.props,
		})
	}
	return out
}

func fiftyFifty() Flag {
	return Flag{Key: "exp", Enabled: true, Variants: []Variant{{Key: "control", Weight: 50}, {Key: "test", Weight: 50}}}
}

// A correct split must not fire. A check that cries wolf gets ignored, which is worse than not
// having it at all.
func TestCleanSplitDoesNotFire(t *testing.T) {
	f := fiftyFifty()
	res := CheckSRM(toEvents(buildExposures("exp", map[string]int{"control": 5000, "test": 4950}, nil)), f, 0)
	if !res.Checked {
		t.Fatalf("expected the split to be checked, got: %s", res.Verdict)
	}
	if res.Detected {
		t.Errorf("false alarm on a 5000/4950 split (p=%.5f): %s", res.PValue, res.Verdict)
	}
}

// A genuinely broken split must fire, and the verdict must say what to do about it.
func TestBrokenSplitFires(t *testing.T) {
	f := fiftyFifty()
	res := CheckSRM(toEvents(buildExposures("exp", map[string]int{"control": 5200, "test": 4800}, nil)), f, 0)
	if !res.Detected {
		t.Fatalf("5200/4800 on a 50/50 experiment should be a mismatch, p=%.6f", res.PValue)
	}
	if res.PValue > SRMAlpha {
		t.Errorf("p=%v is not below the %v threshold", res.PValue, SRMAlpha)
	}
	for _, want := range []string{"5200", "4800", "cannot be trusted"} {
		if !containsStr(res.Verdict, want) {
			t.Errorf("verdict should mention %q, got: %s", want, res.Verdict)
		}
	}
}

// Uneven configured weights must be respected — a 90/10 experiment delivering 90/10 is correct,
// and comparing against an assumed even split would report every weighted experiment as broken.
func TestUnevenWeightsAreTheBaseline(t *testing.T) {
	f := Flag{Key: "exp", Variants: []Variant{{Key: "control", Weight: 90}, {Key: "test", Weight: 10}}}
	ok := CheckSRM(toEvents(buildExposures("exp", map[string]int{"control": 9000, "test": 1000}, nil)), f, 0)
	if ok.Detected {
		t.Errorf("a 90/10 split on a 90/10 experiment fired: %s", ok.Verdict)
	}
	bad := CheckSRM(toEvents(buildExposures("exp", map[string]int{"control": 5000, "test": 5000}, nil)), f, 0)
	if !bad.Detected {
		t.Errorf("an even split on a 90/10 experiment should fire, p=%.6f", bad.PValue)
	}
}

// Below the validity floor the chi-square approximation does not hold, so the check must say it
// cannot tell rather than produce a number that looks like an answer.
func TestTooFewExposuresReportsUncertaintyNotAVerdict(t *testing.T) {
	f := fiftyFifty()
	res := CheckSRM(toEvents(buildExposures("exp", map[string]int{"control": 4, "test": 1}, nil)), f, 0)
	if res.Checked || res.Detected {
		t.Errorf("5 exposures is not enough to judge a split, but it reported: %s", res.Verdict)
	}
	if !containsStr(res.Verdict, "not enough") {
		t.Errorf("verdict should say there is not enough data, got: %s", res.Verdict)
	}
}

func TestNoExposuresAndNoVariantsAreHandled(t *testing.T) {
	if res := CheckSRM(nil, fiftyFifty(), 0); res.Checked || !containsStr(res.Verdict, "no exposures") {
		t.Errorf("empty input should report no exposures, got: %s", res.Verdict)
	}
	boolean := Flag{Key: "exp", Enabled: true}
	if res := CheckSRM(nil, boolean, 0); res.Checked || !containsStr(res.Verdict, "no split") {
		t.Errorf("a flag with no variants has no split to check, got: %s", res.Verdict)
	}
}

// Naming the skewed segment is what turns an alarm into a lead: "broken" becomes "broken, and it
// is iOS". Here every arm is balanced except iOS users, who land almost entirely in control.
func TestCulpritSegmentIsNamed(t *testing.T) {
	var evs []eventForTest
	i := 0
	add := func(variant, platform string, n int) {
		for j := 0; j < n; j++ {
			evs = append(evs, eventForTest{
				id:    fmt.Sprintf("u%d", i),
				props: map[string]any{PropFlag: "exp", PropVariant: variant, "platform": platform},
			})
			i++
		}
	}
	add("control", "web", 2000)
	add("test", "web", 2000)
	add("control", "ios", 400) // iOS barely reaches the test arm
	add("test", "ios", 40)

	res := CheckSRM(toEvents(evs), fiftyFifty(), 0)
	if !res.Detected {
		t.Fatalf("expected a mismatch, p=%.6f: %s", res.PValue, res.Verdict)
	}
	if !containsStr(res.Culprit, "ios") {
		t.Errorf("culprit should name the iOS segment, got %q", res.Culprit)
	}
	if !containsStr(res.Verdict, "Most skewed segment") {
		t.Errorf("verdict should surface the culprit, got: %s", res.Verdict)
	}
}

// Exposures tagged with an arm the flag no longer declares are excluded. Counting them would
// blame the split for a stale SDK, which is a different problem with a different fix.
func TestStaleVariantsAreExcluded(t *testing.T) {
	f := fiftyFifty()
	evs := buildExposures("exp", map[string]int{"control": 500, "test": 500, "legacy": 400}, nil)
	res := CheckSRM(toEvents(evs), f, 0)
	if res.Total != 1000 {
		t.Errorf("counted %d exposures, want 1000 (the 400 stale ones excluded)", res.Total)
	}
	if res.Detected {
		t.Errorf("a clean 500/500 fired because stale arms were counted: %s", res.Verdict)
	}
}

// One user seen on two arms is counted once, under their first exposure. Counting them twice
// would create a ratio mismatch out of the check's own bookkeeping.
func TestUserCountedOnceUnderFirstExposure(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	evs := []event.Event{
		{ID: "1", Name: ExposureEvent, DistinctID: "u1", Timestamp: base,
			Properties: map[string]any{PropFlag: "exp", PropVariant: "control"}},
		{ID: "2", Name: ExposureEvent, DistinctID: "u1", Timestamp: base.Add(time.Minute),
			Properties: map[string]any{PropFlag: "exp", PropVariant: "test"}},
	}
	res := CheckSRM(evs, fiftyFifty(), 0)
	if res.Total != 1 {
		t.Errorf("one user on two arms counted as %d exposures, want 1", res.Total)
	}
	if res.Observed["control"] != 1 {
		t.Errorf("user should be counted under their FIRST exposure (control), got %v", res.Observed)
	}
}

// The product's claim is that reports are computed, not generated. Same events, same verdict,
// every time — including which segment gets named.
func TestSRMIsDeterministic(t *testing.T) {
	var evs []eventForTest
	i := 0
	for _, p := range []string{"ios", "android", "web"} {
		for _, v := range []string{"control", "test"} {
			n := 500
			if p == "ios" && v == "test" {
				n = 50
			}
			for j := 0; j < n; j++ {
				evs = append(evs, eventForTest{id: fmt.Sprintf("u%d", i),
					props: map[string]any{PropFlag: "exp", PropVariant: v, "platform": p}})
				i++
			}
		}
	}
	in := toEvents(evs)
	first := CheckSRM(in, fiftyFifty(), 0)
	for k := 0; k < 20; k++ {
		got := CheckSRM(in, fiftyFifty(), 0)
		if got.PValue != first.PValue || got.Culprit != first.Culprit || got.Detected != first.Detected {
			t.Fatalf("run %d differed: p=%v culprit=%q vs p=%v culprit=%q",
				k, got.PValue, got.Culprit, first.PValue, first.Culprit)
		}
	}
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
