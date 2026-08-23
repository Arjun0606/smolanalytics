package planhealth

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// ONE VERDICT, RENDERED IN TWO PLACES.
//
// This computation lived inline in the instrumentation_health MCP handler, so it was reachable by
// an agent and by nothing else — the dashboard had no notion of a tracking plan at all, which is
// how the product's central claim came to be invisible in the product. Now the tool and the screen
// both call this, and the failure to guard against is the two drifting into disagreement: the
// engine says an event is missing while the screen says it is fine, silently, until a customer
// finds out.
//
// The traps below are the ones a renderer falls into, and each has a wrong answer that looks
// plausible on screen.

type fakeStore struct {
	events  []event.Event
	gotFrom time.Time
}

func (f *fakeStore) Scan(from, _ time.Time, fn func(event.Event) error) error {
	f.gotFrom = from
	for _, e := range f.events {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func ev(name string, at time.Time, props map[string]any) event.Event {
	return event.Event{Name: name, Timestamp: at, Properties: props}
}

func planOf(events ...trackplan.PlannedEvent) trackplan.Plan { return trackplan.Plan{Events: events} }

func TestNoPlanIsNotAnUnhealthyPlan(t *testing.T) {
	// The trap: rendering 0% for a project that never declared a plan. It reads as an indictment
	// of instrumentation that does not exist yet, and it is the first thing a new customer sees.
	h, err := Compute(&fakeStore{}, trackplan.Plan{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.Declared {
		t.Error("Declared is true for an empty plan")
	}
	if !h.Healthy {
		t.Error("an undeclared plan reads as unhealthy; nothing is wrong yet")
	}
	if h.Percent() != 100 {
		t.Errorf("Percent() = %d for no plan; a renderer that skips Declared would print an invented failure", h.Percent())
	}
	if got := h.Headline(); got != "No tracking plan declared, so there is nothing to verify yet. Declare one and this becomes the list of events we watch for you." {
		t.Errorf("headline for no plan reads wrong: %q", got)
	}
}

func TestAFlowingEventIsCountedAndDated(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{events: []event.Event{
		ev("signed_up", now.Add(-2*time.Hour), map[string]any{"plan": "free"}),
		ev("signed_up", now.Add(-time.Hour), map[string]any{"plan": "pro"}),
	}}
	h, err := Compute(st, planOf(trackplan.PlannedEvent{Name: "signed_up", Properties: []string{"plan"}}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Healthy || h.Broken() != 0 || h.Percent() != 100 {
		t.Fatalf("healthy=%v broken=%d pct=%d, want healthy", h.Healthy, h.Broken(), h.Percent())
	}
	e := h.Events[0]
	if e.Status != StatusFlowing || e.Count != 2 {
		t.Errorf("got %+v, want flowing with count 2", e)
	}
	if !e.LastSeen.Equal(now.Add(-time.Hour)) {
		t.Errorf("LastSeen = %v, want the most recent of the two", e.LastSeen)
	}
}

func TestAMissingEventCarriesNoCount(t *testing.T) {
	// The trap: Count defaults to 0, and a row that prints "0 seen" beside a missing event states
	// a measurement we never made. omitempty plus this guard keeps the renderer honest.
	h, err := Compute(&fakeStore{}, planOf(trackplan.PlannedEvent{Name: "checkout"}), 0)
	if err != nil {
		t.Fatal(err)
	}
	e := h.Events[0]
	if e.Status != StatusMissing {
		t.Fatalf("status = %q, want %q", e.Status, StatusMissing)
	}
	if e.Count != 0 || !e.LastSeen.IsZero() {
		t.Errorf("a never-seen event carries count=%d lastSeen=%v", e.Count, e.LastSeen)
	}
	if h.Healthy {
		t.Error("a missing planned event still reads healthy")
	}
}

func TestFlowingButUnderInstrumentedIsBroken(t *testing.T) {
	// The failure that hides longest: the event fires, the count looks right, and every breakdown
	// by the missing property is silently empty. Counting this as healthy is how it stays hidden.
	now := time.Now().UTC()
	st := &fakeStore{events: []event.Event{ev("checkout", now, map[string]any{"amount": 10})}}
	h, err := Compute(st, planOf(trackplan.PlannedEvent{Name: "checkout", Properties: []string{"amount", "plan"}}), 0)
	if err != nil {
		t.Fatal(err)
	}
	e := h.Events[0]
	if e.Status != StatusFlowing {
		t.Fatalf("status = %q, want flowing — the event IS arriving", e.Status)
	}
	if len(e.MissingProperties) != 1 || e.MissingProperties[0] != "plan" {
		t.Fatalf("missing properties = %v, want [plan]", e.MissingProperties)
	}
	if e.OK() || h.Healthy || h.Broken() != 1 {
		t.Errorf("an event missing a declared property counts as fine: ok=%v healthy=%v broken=%d", e.OK(), h.Healthy, h.Broken())
	}
}

func TestAutocaptureIsNeverUnplanned(t *testing.T) {
	// $-prefixed names arrive on every project and nobody declares them. Listing them would bury
	// the one real undeclared event under pageviews, which is the whole value of the list.
	now := time.Now().UTC()
	st := &fakeStore{events: []event.Event{
		ev("$pageview", now, nil),
		ev("$autocapture", now, nil),
		ev("invite_sent", now, nil),
		ev("signed_up", now, nil),
	}}
	h, err := Compute(st, planOf(trackplan.PlannedEvent{Name: "signed_up"}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Unplanned) != 1 || h.Unplanned[0] != "invite_sent" {
		t.Errorf("unplanned = %v, want only [invite_sent]", h.Unplanned)
	}
}

func TestTheWindowIsApplied(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{events: []event.Event{ev("signed_up", now.Add(-72*time.Hour), nil)}}
	h, err := Compute(st, planOf(trackplan.PlannedEvent{Name: "signed_up"}), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if st.gotFrom.IsZero() {
		t.Fatal("a 24h window scanned all of history")
	}
	if h.Events[0].Status != StatusMissing {
		t.Errorf("an event older than the window counted as flowing")
	}
}

func TestPercentAndHeadlineAgreeWithTheRows(t *testing.T) {
	// A meter that disagrees with the list under it is worse than no meter: it makes the reader
	// distrust both. Percent, Broken and the headline all derive from Events, and this pins that.
	now := time.Now().UTC()
	st := &fakeStore{events: []event.Event{ev("a", now, nil), ev("b", now, nil), ev("c", now, nil)}}
	h, err := Compute(st, planOf(
		trackplan.PlannedEvent{Name: "a"},
		trackplan.PlannedEvent{Name: "b"},
		trackplan.PlannedEvent{Name: "c"},
		trackplan.PlannedEvent{Name: "d"},
	), 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.Flowing() != 3 || h.Broken() != 1 || h.Percent() != 75 {
		t.Fatalf("flowing=%d broken=%d pct=%d, want 3/1/75", h.Flowing(), h.Broken(), h.Percent())
	}
	if h.Flowing()+h.Broken() != len(h.Events) {
		t.Error("flowing + broken does not account for every planned event")
	}
	if got, want := h.Headline(), "1 event of 4 in your tracking plan is not arriving as declared."; got != want {
		t.Errorf("headline = %q, want %q", got, want)
	}
}

func TestHeadlineNeverReportsMeasurementLossAsUserLoss(t *testing.T) {
	// "you lost signups" is a claim about the business. An event going quiet only supports a claim
	// about our measurement, and conflating the two is the fastest way to lose a reader's trust.
	now := time.Now().UTC()
	cases := []Health{
		mustCompute(t, &fakeStore{}, planOf(trackplan.PlannedEvent{Name: "signed_up"})),
		mustCompute(t, &fakeStore{events: []event.Event{ev("signed_up", now, nil)}}, planOf(trackplan.PlannedEvent{Name: "signed_up"})),
		mustCompute(t, &fakeStore{}, trackplan.Plan{}),
	}
	for _, h := range cases {
		got := h.Headline()
		for _, banned := range []string{"lost", "users left", "drop in users", "fewer people"} {
			if contains(got, banned) {
				t.Errorf("headline %q states user loss from an instrumentation fact", got)
			}
		}
		if got == "" {
			t.Error("empty headline")
		}
	}
}

func mustCompute(t *testing.T, sc Scanner, p trackplan.Plan) Health {
	t.Helper()
	h, err := Compute(sc, p, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
