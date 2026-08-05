package flag

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// reportFor builds the smallest event log that produces the requested arm counts: control "a"
// converts cCtrl of nCtrl, test "b" converts cTest of nTest.
func reportFor(t *testing.T, cTest, nTest, cCtrl, nCtrl int) Report {
	t.Helper()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "banner", Variants: []Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}}}
	var evs []event.Event
	arm := func(key string, n, c int) {
		for i := 0; i < n; i++ {
			id := key + itoa(i)
			evs = append(evs, expose(id, f.Key, key, base))
			if i < c {
				evs = append(evs, convert(id, "purchase", base.Add(time.Minute)))
			}
		}
	}
	arm("a", nCtrl, cCtrl)
	arm("b", nTest, cTest)
	return MeasureRange(evs, f, "purchase", time.Time{}, time.Time{})
}

// Every degenerate input used to collapse into one sentence: "the control rate is too close to
// zero for a relative comparison to mean anything — read the raw counts instead". Fed a control
// of 60 of 200 (30%!) against a test arm of 0 of 200, the report named the HEALTHY arm as the
// broken one and read as a data-quality shrug, when what actually happened is the clearest loss
// the tool can find. The two mirror-image inputs were literally indistinguishable to the reader.
func TestDegenerateArmsAreNamedRatherThanBlamedOnTheControl(t *testing.T) {
	// test arm converted nobody, control is perfectly healthy
	ci, why := liftInterval(0, 200, 60, 200, z95)
	if why != liftTestZero {
		t.Fatalf("liftInterval(0/200 vs 60/200) blocked for reason %d, want liftTestZero", why)
	}
	s := readLift(ci, why, 0, true, 0, 200, 60, 200)
	if strings.Contains(s, "control rate is too close to zero") {
		t.Errorf("the control converted 60 of 200 — 30%% — and the sentence blames it: %q", s)
	}
	if !strings.Contains(s, "0 of 200") || !strings.Contains(s, "60 of 200") {
		t.Errorf("the verdict does not name the counts that make it checkable: %q", s)
	}
	if !strings.Contains(s, "worse") {
		t.Errorf("an arm that converted nobody is strictly worse and must say so: %q", s)
	}

	// the mirror image must read differently — that is the whole point
	mirrorCI, mirrorWhy := liftInterval(60, 200, 0, 200, z95)
	if mirrorWhy != liftCtrlZero {
		t.Fatalf("liftInterval(60/200 vs 0/200) blocked for reason %d, want liftCtrlZero", mirrorWhy)
	}
	m := readLift(mirrorCI, mirrorWhy, 0, true, 60, 200, 0, 200)
	if m == s {
		t.Fatalf("opposite degenerate inputs produce the identical sentence: %q", s)
	}
	if !strings.Contains(m, "better") {
		t.Errorf("beating a control that converted nobody is strictly better: %q", m)
	}

	// neither arm converted: nothing is worse or better, and the usual cause is a wrong goal name
	nc := readLift(Interval{}, liftNoConversions, 1, true, 0, 200, 0, 200)
	if strings.Contains(nc, "worse") || strings.Contains(nc, "better") {
		t.Errorf("no conversions anywhere is not a verdict on either arm: %q", nc)
	}
	if !strings.Contains(nc, "goal event") {
		t.Errorf("the likeliest cause (goal event name) is not mentioned: %q", nc)
	}

	// an arm with no exposures at all is a broken code path, not a near-zero rate
	if s := readLift(Interval{}, liftNoSample, 1, true, 0, 0, 60, 200); !strings.Contains(s, "no exposures") {
		t.Errorf("a zero-exposure arm should say the flag is not being read: %q", s)
	}

	// and the genuinely-near-zero control keeps its original sentence
	nearZero, nzWhy := liftInterval(40, 1000, 1, 1000, z95)
	if nzWhy != liftCtrlNearZero {
		t.Fatalf("a 0.1%% control blocked for reason %d, want liftCtrlNearZero", nzWhy)
	}
	if s := readLift(nearZero, nzWhy, 0.2, true, 40, 1000, 1, 1000); !strings.Contains(s, "too close to zero") {
		t.Errorf("a control at 0.1%% really is too close to zero, got %q", s)
	}
}

// The report is what a person reads, so the named reason has to survive the trip through Measure
// rather than only being right inside liftInterval.
func TestReportSentenceNamesTheDeadArm(t *testing.T) {
	rep := reportFor(t, 0, 200, 60, 200)
	for _, v := range rep.Variants {
		if v.Key != "b" {
			continue
		}
		if strings.Contains(v.Read, "control rate is too close to zero") {
			t.Errorf("arm b converted 0 of 200 against a 30%% control and the report blames the control: %q", v.Read)
		}
		if !strings.Contains(v.Read, "0 of 200") {
			t.Errorf("the report's sentence does not name the counts: %q", v.Read)
		}
	}
}
