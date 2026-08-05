package flag

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func expose(id, key, variant string, at time.Time) event.Event {
	return event.Event{Name: ExposureEvent, DistinctID: id, Timestamp: at,
		Properties: map[string]any{PropFlag: key, PropVariant: variant}}
}

func convert(id, goal string, at time.Time) event.Event {
	return event.Event{Name: goal, DistinctID: id, Timestamp: at}
}

// Control was sort.Strings(variants)[0]. An experiment with arms named {control, a_new} therefore
// elected `a_new` as the control and INVERTED THE SIGN OF EVERY LIFT — the winning arm reads as
// the losing one, with a p-value next to it. Nothing about the output looks wrong.
func TestControlIsTheDeclaredArmNotTheAlphabeticalOne(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "checkout_v2", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "a_new", Weight: 50}}}

	var evs []event.Event
	// control: 10 of 100 convert. a_new: 30 of 100 — a genuine, large WIN for a_new.
	for i := 0; i < 100; i++ {
		id := "c" + itoa(i)
		evs = append(evs, expose(id, f.Key, "control", base))
		if i < 10 {
			evs = append(evs, convert(id, "checkout", base.Add(time.Minute)))
		}
	}
	for i := 0; i < 100; i++ {
		id := "n" + itoa(i)
		evs = append(evs, expose(id, f.Key, "a_new", base))
		if i < 30 {
			evs = append(evs, convert(id, "checkout", base.Add(time.Minute)))
		}
	}

	rep := MeasureRange(evs, f, "checkout", base.Add(-time.Hour), base.Add(time.Hour))
	if rep.Control != "control" {
		t.Fatalf("control = %q, want %q — alphabetical order picked the treatment arm as the baseline", rep.Control, "control")
	}
	for _, v := range rep.Variants {
		if v.Key != "a_new" {
			continue
		}
		if v.DeltaPct <= 0 {
			t.Errorf("a_new converts 30%% against control's 10%% but its lift is %.1f%% — the sign is inverted", v.DeltaPct)
		}
	}
}

// byVariant was built purely from OBSERVED exposures, so an arm nobody was ever exposed to was
// simply absent. That is the single most important thing this report can say — the treatment
// code never ran — and it rendered as a tidy one-arm experiment with no complaint.
func TestDeadArmIsReportedNotOmitted(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "checkout_v2", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}}

	var evs []event.Event
	for i := 0; i < 80; i++ { // only control is ever exposed
		id := "c" + itoa(i)
		evs = append(evs, expose(id, f.Key, "control", base))
		if i < 8 {
			evs = append(evs, convert(id, "checkout", base.Add(time.Minute)))
		}
	}

	rep := MeasureRange(evs, f, "checkout", base.Add(-time.Hour), base.Add(time.Hour))
	var found bool
	for _, v := range rep.Variants {
		if v.Key == "treatment" {
			found = true
			if v.Exposed != 0 {
				t.Errorf("treatment exposed = %d, want 0", v.Exposed)
			}
		}
	}
	if !found {
		t.Fatal("the treatment arm is missing from the report entirely — a flag whose treatment " +
			"code never ran reads as a clean single-arm experiment")
	}
	if !strings.Contains(rep.Note, "zero exposures") {
		t.Errorf("the note does not call out the dead arm: %q", rep.Note)
	}
}

// `case ExposureEvent:` and `case goal:` shared one switch, so a goal that IS the exposure event
// was unreachable and silently scored zero conversions forever.
func TestGoalNamedLikeTheExposureEventStillCounts(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "onboarding", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "b", Weight: 50}}}
	var evs []event.Event
	for i := 0; i < 40; i++ {
		evs = append(evs, expose("u"+itoa(i), f.Key, map[bool]string{true: "control", false: "b"}[i%2 == 0], base))
	}
	// the goal here is the exposure event itself — unusual, but legal, and it used to be
	// unreachable rather than merely unusual
	rep := MeasureRange(evs, f, ExposureEvent, base.Add(-time.Hour), base.Add(time.Hour))
	total := 0
	for _, v := range rep.Variants {
		total += v.Converted
	}
	if total == 0 {
		t.Error("every conversion was dropped because the goal shares a name with the exposure event")
	}
}

// `days` was resolved with time.Now() INSIDE the pure computation, so the same events produced a
// different report every run. On a product whose claim is byte-identical answers, the experiment
// report was the one thing nobody could reproduce.
func TestMeasureRangeIsClockIndependent(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "k", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "t", Weight: 50}}}
	var evs []event.Event
	for i := 0; i < 60; i++ {
		v := map[bool]string{true: "control", false: "t"}[i%2 == 0]
		id := "u" + itoa(i)
		evs = append(evs, expose(id, f.Key, v, base))
		if i%3 == 0 {
			evs = append(evs, convert(id, "goal", base.Add(time.Minute)))
		}
	}
	from, to := base.Add(-24*time.Hour), base.Add(24*time.Hour)

	first, _ := json.Marshal(MeasureRange(evs, f, "goal", from, to))
	time.Sleep(5 * time.Millisecond) // any real clock movement at all
	second, _ := json.Marshal(MeasureRange(evs, f, "goal", from, to))
	if string(first) != string(second) {
		t.Errorf("two runs over identical input and an identical window disagree:\n %s\n %s", first, second)
	}
}

// The window is half-open, so an event on the boundary belongs to exactly one of two adjacent
// windows — the same rule the rest of the engine follows.
func TestMeasureRangeWindowIsHalfOpen(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "k", Variants: []Variant{{Key: "control", Weight: 100}}}
	evs := []event.Event{expose("edge", f.Key, "control", base)}

	if got := MeasureRange(evs, f, "goal", base, base.Add(time.Hour)); got.Variants[0].Exposed != 1 {
		t.Error("the event exactly at `from` should be inside the window")
	}
	if got := MeasureRange(evs, f, "goal", base.Add(-time.Hour), base); got.Variants[0].Exposed != 0 {
		t.Error("the event exactly at `to` should belong to the NEXT window, not this one")
	}
}

// Only the MINIMUM goal timestamp per user was kept, then compared against their exposure, so a
// user who did the goal once before the experiment and again after being exposed landed in the
// denominator and never in the numerator. On any repeatable goal — purchase, $pageview,
// session_start, message_sent — that is EVERY returning customer, so both arms read low, and
// unequally low whenever exposure timing differs between them.
//
// TestMeasureIgnoresPreExposureConversions covers only the user who never converts again, which
// is why it passed straight over this.
func TestRepeatConverterCountsOnTheGoalAfterExposure(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "banner", Variants: []Variant{{Key: "a", Weight: 100}}}
	evs := []event.Event{
		convert("u1", "purchase", base),                  // T0: bought before the experiment existed
		expose("u1", f.Key, "a", base.Add(1*time.Hour)),  // T1: exposed
		convert("u1", "purchase", base.Add(2*time.Hour)), // T2: bought AGAIN, after exposure
		expose("u2", f.Key, "a", base),                   // never converts at all
		convert("u3", "purchase", base.Add(3*time.Hour)), // converts but was never exposed
		expose("u3", f.Key, "a", base.Add(1*time.Hour)),  //
		convert("u4", "purchase", base),                  // converted only BEFORE exposure
		expose("u4", f.Key, "a", base.Add(1*time.Hour)),  //
	}
	rep := MeasureRange(evs, f, "purchase", time.Time{}, time.Time{})
	a := rep.Variants[0]
	if a.Exposed != 4 {
		t.Fatalf("exposed = %d, want 4", a.Exposed)
	}
	// u1 (repeat converter) and u3 both did the goal after their first exposure. u2 never
	// converted; u4 converted only beforehand and must still be excluded.
	if a.Converted != 2 {
		t.Fatalf("converted = %d, want 2 — a user who converted before AND after exposure is a "+
			"converter, but their earliest goal was used to disqualify them", a.Converted)
	}
}

// The absent case has to be distinguishable from a measured zero. Control 0 of 200 against a test
// arm at 60 of 200 is an infinite lift — the single biggest win this tool can find — and it left
// DeltaPct at its zero value, so the row rendered "0" (neutral, "no change") beside "significant
// at 95% against control".
func TestUndefinedLiftIsFlaggedRatherThanReportedAsZero(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// A pre-registered FIXED-horizon design at the sample this test actually runs. Sequential is
	// the default now, and an always-valid interval is deliberately wider — under repeated
	// peeking a 1.96 z-test's real false-positive rate is several times the 5% it advertises, so
	// the width is the price of being allowed to look. This test is about the LIFT being flagged
	// as undefined rather than rendered as 0, not about the inference regime, so it declares the
	// regime it was written for instead of silently depending on whatever the default is.
	f := Flag{
		Key:      "banner",
		Variants: []Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}},
		Experiment: &Experiment{
			Goal: "buy", Control: "a", Mode: ModeFixed, NPlanned: 400,
			BaselinePct: 5, MDEPct: 50, Power: 0.8,
		},
	}
	var evs []event.Event
	for i := 0; i < 200; i++ { // control: 0 of 200
		evs = append(evs, expose("a"+itoa(i), f.Key, "a", base))
	}
	for i := 0; i < 200; i++ { // test: 60 of 200
		id := "b" + itoa(i)
		evs = append(evs, expose(id, f.Key, "b", base))
		if i < 60 {
			evs = append(evs, convert(id, "purchase", base.Add(time.Minute)))
		}
	}
	rep := MeasureRange(evs, f, "purchase", time.Time{}, time.Time{})
	byKey := map[string]VariantResult{}
	for _, v := range rep.Variants {
		byKey[v.Key] = v
	}
	b := byKey["b"]
	if b.DeltaDefined {
		t.Errorf("delta_defined = true with a control that converted nobody — %v%% is not a "+
			"measurement, it is the zero value", b.DeltaPct)
	}
	if !b.Significant {
		t.Fatal("60 of 200 against 0 of 200 is significant; the test needs that to hold for the point to bite")
	}
	// The control row has no lift against itself, and 0 there is a convention, not a measurement.
	if byKey["a"].DeltaDefined {
		t.Error("the control row claims a defined lift against itself")
	}

	// A healthy comparison must still mark the number as real, or the flag is useless.
	var healthy []event.Event
	for i := 0; i < 200; i++ {
		id := "ha" + itoa(i)
		healthy = append(healthy, expose(id, f.Key, "a", base))
		if i < 20 {
			healthy = append(healthy, convert(id, "purchase", base.Add(time.Minute)))
		}
	}
	for i := 0; i < 200; i++ {
		id := "hb" + itoa(i)
		healthy = append(healthy, expose(id, f.Key, "b", base))
		if i < 40 {
			healthy = append(healthy, convert(id, "purchase", base.Add(time.Minute)))
		}
	}
	for _, v := range MeasureRange(healthy, f, "purchase", time.Time{}, time.Time{}).Variants {
		if v.Key == "b" && !v.DeltaDefined {
			t.Errorf("a real +100%% lift is marked undefined")
		}
	}
}
