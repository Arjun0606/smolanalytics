package insight

import (
	"regexp"
	"strings"
	"testing"
)

// The verdict card is the first thing on the dashboard, and it was written in the vocabulary
// of the event log: "It's country=US: converts worst at $engagement → $click". Correct, and
// unreadable — which for a tool selling computed numbers reads as untrustworthy.

func TestAutocaptureEventsReadAsActions(t *testing.T) {
	cases := map[string]string{
		"$pageview":   "viewed a page",
		"$engagement": "engaged with a page",
		"$click":      "clicked something",
		"$deadclick":  "clicked something that did nothing",
		"$rageclick":  "rage-clicked",
	}
	for raw, want := range cases {
		if got := HumanEvent(raw); got != want {
			t.Errorf("HumanEvent(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCustomEventNamesAreLeftAlone(t *testing.T) {
	// the operator chose these and they already read as English. Rewriting them would be
	// worse than leaving them, and would stop matching what they see everywhere else.
	for _, name := range []string{"signup", "activate", "checkout", "invite_sent"} {
		if got := HumanEvent(name); got != name {
			t.Errorf("HumanEvent(%q) = %q, should be unchanged", name, got)
		}
	}
}

func TestNoRawEventNameSurvivesInProse(t *testing.T) {
	// the thing the reader must never see: a $-prefixed internal in a sentence
	for _, raw := range []string{"$pageview", "$engagement", "$click", "$deadclick"} {
		if strings.Contains(HumanEvent(raw), "$") {
			t.Errorf("HumanEvent(%q) still leaks a $ name: %q", raw, HumanEvent(raw))
		}
	}
}

func TestSegmentsReadAsGroupsOfPeople(t *testing.T) {
	cases := []struct{ prop, val, want string }{
		{"country", "US", "visitors from US"},
		{"device", "mobile", "mobile visitors"},
		{"browser", "Chrome", "Chrome users"},
		{"os", "macOS", "people on macOS"},
		{"source", "reddit", "visitors from reddit"},
		{"plan", "Pro", "pro-plan users"},
	}
	for _, c := range cases {
		if got := HumanSegment(c.prop, c.val); got != c.want {
			t.Errorf("HumanSegment(%q,%q) = %q, want %q", c.prop, c.val, got, c.want)
		}
		if strings.Contains(HumanSegment(c.prop, c.val), "=") {
			t.Errorf("HumanSegment(%q,%q) still reads like a filter", c.prop, c.val)
		}
	}
}

func TestUnknownPropertyStillAvoidsFilterSyntax(t *testing.T) {
	got := HumanSegment("tenant_size", "enterprise")
	if strings.Contains(got, "=") {
		t.Errorf("unknown property fell back to filter syntax: %q", got)
	}
}

func TestCapFirstOnlyTouchesTheFirstLetter(t *testing.T) {
	if got := capFirst("visitors from US"); got != "Visitors from US" {
		t.Errorf("capFirst = %q, want %q (strings.Title would break the US)", got, "Visitors from US")
	}
	if got := capFirst(""); got != "" {
		t.Errorf("capFirst on empty = %q", got)
	}
}

// The regression that matters: a $-prefixed internal appearing in any finding a user reads.
// Checking the words in isolation is not enough — the bug was always in the assembled
// sentence, where the wrong grammatical form was picked for the slot.
func TestNoFindingLeaksARawEventName(t *testing.T) {
	evs := funnelFixture() // $pageview / $engagement / $click / $deadclick
	for _, f := range Generate(evs) {
		if strings.Contains(f.Title, "$") {
			t.Errorf("title leaks a raw event name: %q", f.Title)
		}
		if strings.Contains(f.Detail, "$") {
			t.Errorf("detail leaks a raw event name: %q", f.Detail)
		}
		// prop=value filter syntax, but NOT the honest "(n=40, small sample)" annotation,
		// which is a statistical caveat the reader should keep seeing
		if m := filterSyntax.FindString(f.Title + f.Detail); m != "" {
			t.Errorf("finding reads like a filter (%s): %q / %q", m, f.Title, f.Detail)
		}
	}
}

// Each slot needs its own form. A single label produced "go on to engaged with a page" and
// "viewed a page dropped 100%", neither of which is a sentence.
func TestEachSlotGetsItsOwnForm(t *testing.T) {
	if HumanEvent("$engagement") == HumanEventBase("$engagement") {
		t.Error("past and infinitive forms must differ")
	}
	if HumanEventNoun("$pageview") != "page views" {
		t.Errorf("noun form = %q, want %q", HumanEventNoun("$pageview"), "page views")
	}
	// a custom event has no forms to pick from and must survive every accessor unchanged
	for _, fn := range []func(string) string{HumanEvent, HumanEventBase, HumanEventIng, HumanEventNoun} {
		if got := fn("checkout"); got != "checkout" {
			t.Errorf("custom event mangled: %q", got)
		}
	}
}

// a property filter is a word, "=", then a value — "n=40" is a sample size, not a filter.
var filterSyntax = regexp.MustCompile(`\b[a-z_]{3,}=[A-Za-z0-9_./-]+`)
