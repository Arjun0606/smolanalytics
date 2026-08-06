package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func pv(user, path string, at time.Time) event.Event {
	return event.Event{Name: "$pageview", DistinctID: user, Timestamp: at,
		Properties: map[string]any{"path": path}}
}

// The whole point: the journey is already in the page views, so the tool can propose it instead of
// asking the user to type back something we can already see.
func TestItProposesTheJourneyPeopleActuallyWalk(t *testing.T) {
	base := time.Now().UTC().Add(-48 * time.Hour)
	var evs []event.Event
	for i := 0; i < 60; i++ {
		u := fmt.Sprintf("u%d", i)
		evs = append(evs, pv(u, "/", base.Add(time.Duration(i)*time.Minute)))
		if i%2 == 0 { // 30 reach pricing
			evs = append(evs, pv(u, "/pricing", base.Add(time.Duration(i)*time.Minute+time.Minute)))
		}
		if i%6 == 0 { // 10 reach signup
			evs = append(evs, pv(u, "/signup", base.Add(time.Duration(i)*time.Minute+2*time.Minute)))
		}
	}
	got := ProposeFunnel(evs)
	if len(got.Steps) < 2 {
		t.Fatalf("no funnel proposed from an obvious journey: %+v", got)
	}
	if got.Steps[0].Path != "/" {
		t.Errorf("entry should be the busiest page, got %q", got.Steps[0].Path)
	}
	last := got.Steps[len(got.Steps)-1]
	if last.Path != "/signup" {
		t.Errorf("destination should be the rarest intent page, got %q", last.Path)
	}
	if !got.Confident {
		t.Errorf("a journey that narrows 60 → 30 → 10 is exactly the confident case: %+v", got)
	}
	// Order-aware, not popularity: each step counts only people who got there after the last one.
	for i := 1; i < len(got.Steps); i++ {
		if got.Steps[i].People >= got.Steps[i-1].People {
			t.Errorf("step %d (%d) did not narrow from %d", i, got.Steps[i].People, got.Steps[i-1].People)
		}
	}
}

// Three popular pages nobody walks in order are NOT a funnel. Proposing them would repeat exactly
// the mistake this replaces — a confident shape built from data that does not support it.
func TestPopularPagesAreNotAJourney(t *testing.T) {
	base := time.Now().UTC().Add(-48 * time.Hour)
	var evs []event.Event
	for i := 0; i < 60; i++ {
		u := fmt.Sprintf("u%d", i)
		// Everyone visits everything, in no particular order and with no narrowing.
		evs = append(evs,
			pv(u, "/", base.Add(time.Duration(i)*time.Minute)),
			pv(u, "/docs", base.Add(time.Duration(i)*time.Minute+time.Second)),
			pv(u, "/signup", base.Add(time.Duration(i)*time.Minute+2*time.Second)),
		)
	}
	got := ProposeFunnel(evs)
	if got.Confident {
		t.Errorf("pages that never narrow were proposed confidently as a funnel: %+v", got)
	}
	if got.Why == "" {
		t.Error("a low-confidence proposal must say why, or the reader cannot judge it")
	}
}

// Too little traffic and any shape is coincidence. Saying nothing beats proposing something the
// reader would have to un-believe next week.
func TestItRefusesToGuessFromAHandfulOfVisits(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	var evs []event.Event
	for i := 0; i < 4; i++ {
		u := fmt.Sprintf("u%d", i)
		evs = append(evs, pv(u, "/", base), pv(u, "/signup", base.Add(time.Minute)))
	}
	got := ProposeFunnel(evs)
	if len(got.Steps) > 0 {
		t.Errorf("proposed a funnel from 4 visitors: %+v", got)
	}
	if !strings.Contains(got.Why, "not enough") {
		t.Errorf("want a reason naming the sample, got %q", got.Why)
	}
}

// A site with no signup/checkout/trial page has no destination to guess, and should say so rather
// than nominate whatever page happens to be second-busiest.
func TestNoIntentPageMeansNoGuess(t *testing.T) {
	base := time.Now().UTC().Add(-24 * time.Hour)
	var evs []event.Event
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("u%d", i)
		evs = append(evs, pv(u, "/", base), pv(u, "/blog", base.Add(time.Minute)), pv(u, "/about", base.Add(2*time.Minute)))
	}
	got := ProposeFunnel(evs)
	if len(got.Steps) > 0 {
		t.Errorf("nominated a destination with no intent page present: %+v", got)
	}
	if !strings.Contains(got.Why, "signup") {
		t.Errorf("the reason should name what it was looking for, got %q", got.Why)
	}
}

// Routing conventions differ; the intent does not. /auth/sign-up and /en/signup are the same thing.
func TestIntentIsMatchedAcrossRoutingConventions(t *testing.T) {
	for _, p := range []string{"/auth/sign-up", "/en/signup", "/app/checkout", "/start-trial", "/get-started"} {
		base := time.Now().UTC().Add(-24 * time.Hour)
		var evs []event.Event
		for i := 0; i < 40; i++ {
			u := fmt.Sprintf("u%d", i)
			evs = append(evs, pv(u, "/", base.Add(time.Duration(i)*time.Minute)))
			if i%5 == 0 {
				evs = append(evs, pv(u, p, base.Add(time.Duration(i)*time.Minute+time.Minute)))
			}
		}
		got := ProposeFunnel(evs)
		if len(got.Steps) < 2 {
			t.Errorf("%q was not recognised as a destination: %+v", p, got)
			continue
		}
		if got.Steps[len(got.Steps)-1].Path != p {
			t.Errorf("%q should be the destination, got %q", p, got.Steps[len(got.Steps)-1].Path)
		}
	}
}

// Someone who lands on /signup FIRST and then browses backwards has not walked the funnel. Counting
// them would inflate the destination above what the order supports.
func TestArrivingAtTheEndFirstDoesNotCount(t *testing.T) {
	base := time.Now().UTC().Add(-24 * time.Hour)
	var evs []event.Event
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("u%d", i)
		if i%2 == 0 {
			// walks it properly
			evs = append(evs, pv(u, "/", base.Add(time.Duration(i)*time.Minute)),
				pv(u, "/signup", base.Add(time.Duration(i)*time.Minute+time.Minute)))
		} else {
			// deep-links straight to signup, THEN goes home
			evs = append(evs, pv(u, "/signup", base.Add(time.Duration(i)*time.Minute)),
				pv(u, "/", base.Add(time.Duration(i)*time.Minute+time.Minute)))
		}
	}
	got := ProposeFunnel(evs)
	if len(got.Steps) < 2 {
		t.Fatalf("expected a proposal: %+v", got)
	}
	last := got.Steps[len(got.Steps)-1]
	// 40 people touched /signup, but only 20 reached it after the entry page.
	if last.People != 20 {
		t.Errorf("destination counted %d people; only 20 walked the order — the other 20 arrived "+
			"there first, which is not conversion", last.People)
	}
}
