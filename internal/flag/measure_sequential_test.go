package flag

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// significant() was a fixed-horizon 1.96 z-test sitting behind a dashboard built to be checked
// daily. Under repeated peeking that test's real false-positive rate is several times the 5% it
// advertises, so the engine now draws always-valid intervals by default: wider on purpose, and
// correct at every N rather than only at one pre-declared one.
func TestSequentialIsTheDefaultAndIsWiderThanFixed(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(exp Experiment) Report {
		f := Flag{Key: "k", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "t", Weight: 50}}}
		if exp.Goal != "" {
			e := exp
			f.Experiment = &e
		}
		var evs []event.Event
		for i := 0; i < 300; i++ {
			id := "c" + itoa(i)
			evs = append(evs, expose(id, f.Key, "control", base))
			if i < 100 { // 33% control: a ratio needs a control clear of the 10-SE near-zero guard
				evs = append(evs, convert(id, "buy", base.Add(time.Minute)))
			}
		}
		for i := 0; i < 300; i++ {
			id := "t" + itoa(i)
			evs = append(evs, expose(id, f.Key, "t", base))
			if i < 160 { // 53% treatment
				evs = append(evs, convert(id, "buy", base.Add(time.Minute)))
			}
		}
		return MeasureRange(evs, f, "buy", time.Time{}, time.Time{})
	}

	// no plan at all → sequential, and the report says it was not pre-registered
	seq := mk(Experiment{})
	if seq.Mode != ModeSequential {
		t.Errorf("mode = %q with no plan, want %q — peeking has to be safe by default", seq.Mode, ModeSequential)
	}
	if seq.Planned {
		t.Error("a flag with no experiment reports itself as pre-registered")
	}

	fixed := mk(Experiment{Goal: "buy", Control: "control", Mode: ModeFixed, NPlanned: 600,
		BaselinePct: 33, MDEPct: 50, Power: 0.8})
	if fixed.Mode != ModeFixed {
		t.Fatalf("mode = %q with a fixed plan at its planned N, want %q (%s)", fixed.Mode, ModeFixed, fixed.ModeReason)
	}
	if !fixed.Planned {
		t.Error("a flag carrying an experiment reports itself as not pre-registered")
	}

	armOf := func(r Report) VariantResult {
		for _, v := range r.Variants {
			if v.Key == "t" {
				return v
			}
		}
		t.Fatal("treatment arm missing")
		return VariantResult{}
	}
	s, fx := armOf(seq), armOf(fixed)
	if s.Seq == nil || fx.Seq == nil {
		t.Fatal("the sequential block is not on the report; the width cannot be traced to its method")
	}
	if s.Seq.K <= 1 {
		t.Errorf("sequential K = %v, want > 1 — an always-valid interval is never narrower than a fixed one", s.Seq.K)
	}
	if fx.Seq.K != 1 {
		t.Errorf("fixed K = %v, want exactly 1", fx.Seq.K)
	}
	if s.DeltaCI == nil || fx.DeltaCI == nil {
		t.Fatal("no lift interval on a healthy comparison")
	}
	seqWidth, fixWidth := s.DeltaCI.Hi-s.DeltaCI.Lo, fx.DeltaCI.Hi-fx.DeltaCI.Lo
	if seqWidth <= fixWidth {
		t.Errorf("the sequential lift interval (%.1f wide) is not wider than the fixed one (%.1f) — "+
			"the RELATIVE interval is not being inflated by the same K as the absolute one", seqWidth, fixWidth)
	}
}

// A fixed-horizon test read before its pre-registered sample size is not valid yet, so the report
// hands back the always-valid interval instead and says why — rather than printing a number that
// only becomes true later.
func TestFixedReadTooEarlyFallsBackAndExplains(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{
		Key:      "k",
		Variants: []Variant{{Key: "control", Weight: 50}, {Key: "t", Weight: 50}},
		Experiment: &Experiment{Goal: "buy", Control: "control", Mode: ModeFixed, NPlanned: 100000,
			BaselinePct: 10, MDEPct: 5, Power: 0.8},
	}
	var evs []event.Event
	for i := 0; i < 100; i++ {
		evs = append(evs, expose("c"+itoa(i), f.Key, "control", base))
		evs = append(evs, expose("t"+itoa(i), f.Key, "t", base))
	}
	rep := MeasureRange(evs, f, "buy", time.Time{}, time.Time{})
	if rep.Mode != ModeSequential {
		t.Errorf("mode = %q, want the always-valid fallback while far short of n_planned", rep.Mode)
	}
	if rep.ModeReason == "" {
		t.Error("the fallback happened silently; a reader sees a different interval than the one they registered and is told nothing")
	}
}

// The randomisation unit is bucket_id, which survives identify(). Analysing by distinct_id splits
// one person's behaviour in half at the login — the arm keeps their pre-login events and loses
// their post-login ones, which is not the population the experiment randomised.
func TestBucketIDIsTheRandomisationUnit(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "k", Variants: []Variant{{Key: "control", Weight: 50}, {Key: "t", Weight: 50}}}

	withBucket := func(e event.Event, b string) event.Event {
		if e.Properties == nil {
			e.Properties = map[string]any{}
		}
		e.Properties[PropBucketID] = b
		return e
	}
	var evs []event.Event
	for i := 0; i < 40; i++ {
		// exposed while anonymous, converts after logging in under a NEW distinct_id
		anon, user, dev := "anon"+itoa(i), "user"+itoa(i), "dev"+itoa(i)
		evs = append(evs, withBucket(expose(anon, f.Key, "t", base), dev))
		evs = append(evs, withBucket(convert(user, "buy", base.Add(time.Hour)), dev))
	}
	rep := MeasureRange(evs, f, "buy", time.Time{}, time.Time{})
	if rep.Unit != UnitBucketID {
		t.Errorf("randomisation unit = %q, want %q", rep.Unit, UnitBucketID)
	}
	for _, v := range rep.Variants {
		if v.Key != "t" {
			continue
		}
		if v.Exposed != 40 {
			t.Errorf("exposed = %d, want 40", v.Exposed)
		}
		if v.Converted != 40 {
			t.Errorf("converted = %d, want 40 — the post-login conversions were attributed to a "+
				"different unit than the exposure, so every one of them was lost", v.Converted)
		}
	}
}

// The design has to be ON the response. "Significant" means nothing without the alpha it was
// tested at, and an interval means nothing without the mode that drew it.
func TestReportEchoesItsOwnDesign(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := Flag{Key: "k", Variants: []Variant{{Key: "control", Weight: 100}}}
	from, to := base.Add(-24*time.Hour), base.Add(24*time.Hour)
	rep := MeasureRange([]event.Event{expose("u", f.Key, "control", base)}, f, "buy", from, to)

	if rep.Mode == "" || rep.Alpha == 0 || rep.NTune == 0 || rep.NTuneSource == "" || rep.Unit == "" {
		t.Errorf("the report does not state its own design: mode=%q alpha=%v n_tune=%d source=%q unit=%q",
			rep.Mode, rep.Alpha, rep.NTune, rep.NTuneSource, rep.Unit)
	}
	if rep.From == "" || rep.To == "" {
		t.Error("the resolved window is not echoed, so the run cannot be reproduced without re-deriving it")
	}
}
