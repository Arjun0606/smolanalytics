package insight

import (
	"fmt"
	"strings"
)

// Findings are the most-read text in the product: the verdict card is the first thing on the
// page. They were being written in the vocabulary of the event log rather than the reader's:
//
//	It's country=US: converts worst at $engagement → $click, 2.0× worse than average
//	only 10% continue; 52 users fall off here. Overall $pageview→$deadclick conversion is 7%
//
// Every one of those numbers is correct. The sentence still fails, because "$engagement →
// $click" and "country=US" are internals leaking through, and a reader who cannot parse a
// sentence has no way to tell whether it is right. Unreadable reads as untrustworthy, which
// is the expensive failure for a tool whose whole pitch is that the numbers are computed.
//
// These helpers translate at the presentation edge only. The event names stay exactly as
// recorded everywhere they are used as identifiers: filters, the API, MCP tool arguments and
// the funnel definition itself. Renaming those would break every saved report and every
// agent that has learned the real names.

// autocaptureLabel maps the $-prefixed events sdk.js records to what the reader did.
// Phrased as a past-tense action so it drops into "X → Y" and "N users did X" alike.
// A single label cannot serve every sentence. "after they viewed a page" needs the past
// tense, "go on to engage with a page" needs the infinitive, and "from viewing a page to
// clicking something" needs the gerund. Writing one form and reusing it produced "only 50%
// go on to engaged with a page", which is worse than the raw event name it replaced.
type eventWords struct {
	past string // they ___              : "viewed a page"
	base string // go on to ___          : "engage with a page"
	ing  string // from ___ to ___       : "viewing a page"
	noun string // ___ dropped 20%       : "page views"
}

var autocaptureLabel = map[string]eventWords{
	"$pageview":            {"viewed a page", "view a page", "viewing a page", "page views"},
	"$engagement":          {"engaged with a page", "engage with a page", "engaging with a page", "page engagement"},
	"$click":               {"clicked something", "click something", "clicking something", "clicks"},
	"$deadclick":           {"clicked something that did nothing", "click something that does nothing", "clicking something that does nothing", "dead clicks"},
	"$rageclick":           {"rage-clicked", "rage-click", "rage-clicking", "rage clicks"},
	"$feature_flag_called": {"saw a flagged feature", "see a flagged feature", "seeing a flagged feature", "flag exposures"},
	"$identify":            {"signed in", "sign in", "signing in", "sign-ins"},
	"$survey_shown":        {"saw a survey", "see a survey", "seeing a survey", "survey views"},
	"$survey_answered":     {"answered a survey", "answer a survey", "answering a survey", "survey answers"},
}

// HumanEvent is the PAST-tense form, for "after they ___".
// Custom events are returned unchanged: the operator chose "signup" or "checkout" and it
// already reads as English, so dressing it up would be worse and would stop matching what
// they see in filters, the API and their own tracking code.
func HumanEvent(name string) string {
	if w, ok := autocaptureLabel[name]; ok {
		return w.past
	}
	return name
}

// HumanEventBase is the infinitive, for "go on to ___".
func HumanEventBase(name string) string {
	if w, ok := autocaptureLabel[name]; ok {
		return w.base
	}
	return name
}

// HumanEventIng is the gerund, for "from ___ to ___".
func HumanEventIng(name string) string {
	if w, ok := autocaptureLabel[name]; ok {
		return w.ing
	}
	return name
}

// HumanEventNoun is the plural noun, for "___ dropped 20% in the last 24h", where an action
// cannot be the subject: "viewed a page dropped 100%" is not a sentence.
func HumanEventNoun(name string) string {
	if w, ok := autocaptureLabel[name]; ok {
		return w.noun
	}
	return name
}

// HumanStep reads a funnel step inside "A → B".
func HumanStep(name string) string { return HumanEvent(name) }

// HumanSegment turns a raw property match into a phrase. `country=US` is how a filter is
// written, not how a person describes a group of visitors.
func HumanSegment(prop, value string) string {
	switch strings.ToLower(prop) {
	case "country":
		return fmt.Sprintf("visitors from %s", value)
	case "device", "device_type":
		return fmt.Sprintf("%s visitors", strings.ToLower(value))
	case "browser":
		return fmt.Sprintf("%s users", value)
	case "os":
		return fmt.Sprintf("people on %s", value)
	case "source", "referrer", "channel":
		return fmt.Sprintf("visitors from %s", value)
	case "path", "page":
		return fmt.Sprintf("people who landed on %s", value)
	case "plan", "tier":
		return fmt.Sprintf("%s-plan users", strings.ToLower(value))
	}
	// unknown property: still better than "prop=value", and never invents a relationship
	// that the data does not support
	return fmt.Sprintf("%s %s", value, prop)
}

// capFirst uppercases the first rune only, for phrases that start a title. strings.Title
// would capitalise every word and turn "visitors from US" into "Visitors From US".
func capFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 32
	}
	return string(r)
}
