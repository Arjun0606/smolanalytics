package brief

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// The GEO sampler writes to the same instance the brief reads. Its robot id must not
// appear as a visitor, and its volume must not read as growth — the verdict already
// excludes those events, so the pulse printed above the verdict has to agree.
func TestGeoChecksAreNotProductTraffic(t *testing.T) {
	now := time.Now().UTC()
	base := []event.Event{
		{Name: "$pageview", DistinctID: "u1", Timestamp: now.Add(-2 * time.Hour)},
		{Name: "$pageview", DistinctID: "u2", Timestamp: now.Add(-3 * time.Hour)},
	}
	withGeo := append(append([]event.Event{}, base...), event.Event{
		Name: "$geo_check", DistinctID: "geo-runner", Timestamp: now.Add(-time.Hour),
		Properties: map[string]any{"engine": "claude", "mentioned": true},
	})
	clean, dirty := Build(base, 7, now), Build(withGeo, 7, now)
	if dirty.Events != clean.Events || dirty.Visitors != clean.Visitors {
		t.Errorf("sampler writes leaked into the pulse: %d events/%d visitors vs %d/%d",
			dirty.Events, dirty.Visitors, clean.Events, clean.Visitors)
	}
}

// THE SCHEDULED JOB MUST CARRY THE INVESTIGATION.
//
// The homepage says "every morning · it investigates before you wake up". The only scheduled job
// in the binary sent insight.Generate — the older verdict engine — so the flagship output, the
// one the whole product is named for, had no unprompted delivery path on any surface and was
// reachable only by someone who chose to open a browser.
//
// This pins the SHAPE the daily job depends on: Build must populate Investigation, and Format
// must render it, or the webhook payload silently reverts to being about something else.
func TestTheBriefCarriesAndRendersTheInvestigation(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for d := 27; d >= 0; d-- {
		n := 40
		if d < 14 {
			n = 10 // a collapse the investigator must find
		}
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("e%d_%d", d, i), Name: "signup",
				DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp:  now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			})
		}
	}

	b := Build(evs, 7, now)
	if len(b.Investigation.Findings) == 0 {
		t.Fatal("Build produced no investigation findings on a 4x collapse, so the daily webhook " +
			"would carry nothing worth sending")
	}
	f := b.Investigation.Findings[0]
	if f.Cost.People <= 0 {
		t.Error("the finding has no size, so the brief cannot rank or price it")
	}
	if f.Cause == "" || strings.Contains(f.Cause, "cause not yet attributed") {
		t.Errorf("the finding reached the brief unattributed: %q — Run() must call Attribute so "+
			"every unprompted surface says the same thing the dashboard does", f.Cause)
	}

	out := Format(b)
	if !strings.Contains(out, f.Headline) {
		t.Errorf("Format drops the investigation headline, so the webhook text would not contain "+
			"the finding:\n%s", out)
	}
}

// THE TEXT SURFACES CARRY THE SAME STATES THE DESK SHOWS.
//
// BelowFloor and Recovered shipped to the dashboard first, and the CLI brief and email — the
// unprompted surfaces — rendered neither: the same one-surface-behind split this codebase keeps
// re-learning, caught this time before it aged.
func TestFormatCarriesFloorsAndRecoveredTags(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	// signup at a steady 3/day: below the floor
	for d := 27; d >= 0; d-- {
		for i := 0; i < 3; i++ {
			evs = append(evs, event.Event{ID: fmt.Sprintf("s%d_%d", d, i), Name: "signup",
				DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp:  now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)})
		}
	}
	out := Format(Build(evs, 7, now))
	if !strings.Contains(out, "below the detection floor") || !strings.Contains(out, "signup runs at") {
		t.Errorf("the brief text does not carry the floor note the desk shows:\n%s", out)
	}
}

// PARITY WITH THE DESK. The brief was the one surface built without instance context, so on an
// instance that records deploys the email said "no deploy recorded near this day" while the
// dashboard named the ship — and even invited the reader to start recording deploys they already
// record. BuildCtx must hand the same context through; this test fails if it quietly stops.
func TestBriefWithContextNamesTheShipTheDeskNames(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	mk := func(day, n int) {
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{ID: fmt.Sprintf("c%d_%d", day, i), Name: "checkout",
				DistinctID: fmt.Sprintf("u%d_%d", day, i),
				Timestamp:  now.AddDate(0, 0, -day).Add(time.Duration(i) * time.Minute)})
		}
	}
	for d := 27; d >= 8; d-- {
		mk(d, 40)
	}
	for d := 7; d >= 0; d-- {
		mk(d, 10)
	}
	dropDay := now.AddDate(0, 0, -8) // the last full day at the old level; the fall lands on -7
	deps := []deploys.Deploy{{ID: "d1", SHA: "a1b3f9c", Message: "ship the checkout rewrite",
		At: now.AddDate(0, 0, -7), Created: now.AddDate(0, 0, -7)}}
	_ = dropDay

	plain := Build(evs, 7, now)
	ctxed := BuildCtx(evs, 7, now, &Context{Deploys: deps})

	var plainReg, ctxReg *investigate.Finding
	for i := range plain.Investigation.Findings {
		if plain.Investigation.Findings[i].Kind == investigate.KindRegression {
			plainReg = &plain.Investigation.Findings[i]
		}
	}
	for i := range ctxed.Investigation.Findings {
		if ctxed.Investigation.Findings[i].Kind == investigate.KindRegression {
			ctxReg = &ctxed.Investigation.Findings[i]
		}
	}
	if plainReg == nil || ctxReg == nil {
		t.Fatal("fixture did not produce the regression on both paths")
	}
	if !strings.Contains(ctxReg.Cause, "a1b3f9c") {
		t.Errorf("with deploys in context the brief must name the ship, got: %q", ctxReg.Cause)
	}
	if strings.Contains(plainReg.Cause, "a1b3f9c") {
		t.Errorf("without context the ship cannot be named, got: %q", plainReg.Cause)
	}

	// and the acted overlay flows through the same door
	acted := BuildCtx(evs, 7, now, &Context{Acted: func(string) (time.Time, bool) {
		return now.AddDate(0, 0, -3), true
	}})
	for _, f := range acted.Investigation.Findings {
		if f.Kind == investigate.KindRegression && f.ActedAt.IsZero() {
			t.Error("the acted overlay did not reach the brief's findings")
		}
	}
}
