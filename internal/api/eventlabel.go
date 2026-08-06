package api

import "strings"

// EventLabel turns an event name into something a human reads without being taught the schema.
//
// This is the single change four independent cold readers hit in every pane. The dashboard's
// headline metric rendered as "$pageview → $engagement → $click  8%" — a sentence in a language
// none of them spoke. A left-nav item was literally named "$pageview → $engagement → $click".
// "$crawler" appeared in a column headed VISITOR, and one reader said it looked like a person
// named $crawler had visited the site. Three empty states told the reader to send "$identify"
// without saying that was code they had to write.
//
// The "$" prefix is real and meaningful — it marks events the SDK captures on its own, as opposed
// to ones the product sends deliberately — but that is a fact about our implementation, and the
// dashboard is not the place to teach it. Two surfaces keep the raw names because there they ARE
// the interface: the SQL pane and the raw event stream, where you are writing against the schema.
//
// Deliberately a translation at the render boundary, not a rename. The stored names, the API, the
// SDK and every query are untouched — renaming events would break every customer's instrumentation
// to fix a reading problem.
func EventLabel(name string) string {
	switch name {
	case "$pageview":
		return "page view"
	case "$engagement":
		return "engaged"
	case "$click":
		return "click"
	case "$rageclick":
		return "rage click"
	case "$deadclick":
		return "dead click"
	case "$form_submit":
		return "form submit"
	case "$exception":
		return "error"
	case "$identify":
		return "identify"
	case "$ai_crawl", "$crawler":
		return "AI bot"
	case "$geo_check":
		return "location check"
	case "$site_readable":
		return "readability scan"
	case "$feature_flag_called":
		return "flag shown"
	case "agent_turn":
		return "AI agent turn"
	case "agent_tool_call":
		return "AI tool call"
	}
	// A product's OWN events are already in its own words — "signup", "checkout" — so they pass
	// through untouched. Only underscores are softened, because "trial_started" reads as a
	// variable name and "trial started" reads as a thing that happened.
	if strings.HasPrefix(name, "$") {
		// An unrecognised internal event: drop the sigil rather than invent a translation for
		// something this function has not been taught.
		return strings.ReplaceAll(strings.TrimPrefix(name, "$"), "_", " ")
	}
	return strings.ReplaceAll(name, "_", " ")
}

// EventGloss is the one-time explanation for events whose LABEL alone is still not enough.
//
// "engaged" is the case that matters: it sounds like a judgement the tool made about the visitor,
// and nobody can act on it without knowing the rule. Returns "" when the label speaks for itself,
// so callers can render a gloss only where one is owed.
func EventGloss(name string) string {
	switch name {
	case "$engagement":
		return "stayed on the page 10 seconds or scrolled half of it"
	case "$rageclick":
		return "clicked the same thing several times fast, usually because nothing happened"
	case "$deadclick":
		return "clicked something that was not clickable"
	case "$ai_crawl", "$crawler":
		return "an AI company's crawler fetching your page, not a person"
	case "$geo_check", "$site_readable":
		return "this tool checking your own site, not a visitor"
	}
	return ""
}

// EventIsAuto reports whether the SDK writes this event by itself.
//
// The distinction the "$" was carrying, made available as a fact instead of a sigil: it is what
// separates "your product has no funnel yet" from "your product's funnel is bad", and the verdict
// card gets that wrong in the loudest possible way when it cannot tell them apart.
func EventIsAuto(name string) bool { return strings.HasPrefix(name, "$") }
