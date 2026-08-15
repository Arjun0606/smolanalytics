// Package claim turns a merged pull request into a prediction with a deadline.
//
// A claim is the missing noun. Analytics says what happened; a claim says what a change was
// SUPPOSED to make happen, and then the adjudicator holds the team to it at a date. Everything in
// the ship ledger hangs off this object, so the question that decides whether the ledger is a
// product or a form is: what fraction of real merges yield a checkable claim at all?
//
// That is not a rhetorical question. Most merges are refactors, dependency bumps, CI fixes and
// copy tweaks with no measurable intent whatsoever, and a system that demands a claim for each one
// becomes an annoying form that makes a person do MORE product-management work, not less. So
// "no measurable intent" is a first-class outcome here, returned deliberately and counted, rather
// than an awkward edge case.
//
// Deliberately deterministic. A model could read intent out of prose more sensibly, but the point
// of this pass is to measure the CEILING structurally before committing to the design: if the
// diff never touches a line that records an event, no amount of language understanding will make
// the result checkable, because there is nothing downstream to check it against.
package claim

import (
	"regexp"
	"strings"
)

// PR is the part of a pull request this needs. Deliberately small so it can be filled from the
// GitHub API, a local git log, or a test fixture without adapters.
type PR struct {
	Number int
	Title  string
	Body   string
	Files  []string // paths touched
	// Instrumented is the subset of Files that contain a tracking call for a known event. The
	// caller resolves this, because only it knows how the product is instrumented.
	Instrumented []string
	// Events named anywhere in the title, body or diff.
	Events []string
}

// Verdict is why a PR did or did not produce a claim. A closed set, because "we could not decide"
// has to be visible in the counts rather than hidden in a default.
type Verdict string

const (
	// Claimable — the change touches something measurable and an event can be named.
	Claimable Verdict = "claimable"
	// TouchesInstrumented — it changes instrumented code but names no specific event. Still
	// adjudicable against the events those files record, which is the narrower fallback design.
	TouchesInstrumented Verdict = "touches_instrumented"
	// NoMeasurableIntent — a refactor, a bump, a docs change. A correct and common answer.
	NoMeasurableIntent Verdict = "no_measurable_intent"
	// Unclear — user-facing but nothing connects it to a metric. This is the interesting bucket:
	// it is where instrumentation is missing, and it is the honest upper bound on how much better
	// a smarter deriver could do.
	Unclear Verdict = "unclear"
)

// Derived is the outcome for one PR.
type Derived struct {
	PR      int      `json:"pr"`
	Verdict Verdict  `json:"verdict"`
	Metric  string   `json:"metric,omitempty"`
	Why     string   `json:"why"`
	Signals []string `json:"signals,omitempty"`
}

// Paths that can never move a product metric on their own.
var nonProduct = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\.github/`),
	regexp.MustCompile(`(?i)(^|/)(docs?|examples?|fixtures?|testdata)/`),
	regexp.MustCompile(`(?i)\.(md|mdx|txt|lock|sum|mod)$`),
	regexp.MustCompile(`(?i)_test\.(go|ts|tsx|js|jsx|py|rb)$`),
	regexp.MustCompile(`(?i)(^|/)(test|tests|spec|__tests__)/`),
	regexp.MustCompile(`(?i)(^|/)(Dockerfile|Makefile|\.gitignore|\.editorconfig)$`),
	regexp.MustCompile(`(?i)\.(yml|yaml|toml|ini|cfg)$`),
}

// Title prefixes that announce there is nothing to measure. Conventional-commit types plus the
// phrasings people actually use.
var choreTitle = regexp.MustCompile(`(?i)^\s*(chore|build|ci|docs|test|refactor|style|revert|deps|bump)(\(|:|\s)`)
var choreWords = regexp.MustCompile(`(?i)\b(bump|upgrade|downgrade|dependabot|renovate|typo|lint|format|rename|move|extract|cleanup|clean up|dead code)\b`)

// Words that mark a change as user-facing even when no event is named. Used only to separate
// "unclear" from "no measurable intent", never to manufacture a claim.
var userFacing = regexp.MustCompile(`(?i)\b(onboard|signup|sign-up|checkout|paywall|pricing|upgrade|trial|cta|button|banner|modal|flow|funnel|conversion|activation|retention|nudge|prompt|empty state|landing)\b`)

// Derive classifies one PR. It never invents a metric: a claim is only produced when an event is
// actually named or the diff touches code that records one.
func Derive(pr PR) Derived {
	d := Derived{PR: pr.Number}
	text := pr.Title + "\n" + pr.Body

	// 1. Announced chores. Cheapest and most reliable signal there is nothing to measure.
	if choreTitle.MatchString(pr.Title) {
		d.Verdict, d.Why = NoMeasurableIntent, "conventional-commit type announces a non-product change"
		return d
	}

	// 2. Nothing but non-product paths.
	product := 0
	for _, f := range pr.Files {
		if !isNonProduct(f) {
			product++
		}
	}
	if len(pr.Files) > 0 && product == 0 {
		d.Verdict, d.Why = NoMeasurableIntent, "every file touched is docs, tests, config or CI"
		return d
	}

	// 3. A named event is the strongest possible signal, and the only one that yields a metric
	// without guessing. Checked before the chore-words heuristic: a PR titled "rename the signup
	// button" that touches the signup event is still measurable.
	if len(pr.Events) > 0 {
		d.Verdict, d.Metric, d.Why = Claimable, pr.Events[0], "the change names an event the product records"
		d.Signals = pr.Events
		return d
	}

	// 4. Touches instrumented code without naming an event. Adjudicable against whatever those
	// files record — narrower, still real, and this is the fallback the whole design rests on if
	// the claimable rate turns out to be low.
	if len(pr.Instrumented) > 0 {
		d.Verdict, d.Why = TouchesInstrumented, "changes code that records events, but names none"
		d.Signals = pr.Instrumented
		return d
	}

	// 5. Unannounced chores.
	if choreWords.MatchString(text) {
		d.Verdict, d.Why = NoMeasurableIntent, "reads as maintenance: "+firstMatch(choreWords, text)
		return d
	}

	// 6. User-facing but unmeasurable. The honest bucket, and the one worth counting: it is where
	// instrumentation is missing rather than where intent is absent, so it is an upper bound on
	// what better derivation could reach.
	if userFacing.MatchString(text) {
		d.Verdict, d.Why = Unclear, "user-facing ("+firstMatch(userFacing, text)+") but nothing records it"
		return d
	}

	d.Verdict, d.Why = NoMeasurableIntent, "no event, no instrumented file, no user-facing language"
	return d
}

func isNonProduct(path string) bool {
	for _, re := range nonProduct {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func firstMatch(re *regexp.Regexp, s string) string {
	if m := re.FindString(s); m != "" {
		return strings.ToLower(m)
	}
	return ""
}

// Yield is the answer the whole test exists to produce.
type Yield struct {
	Total               int `json:"total"`
	Claimable           int `json:"claimable"`
	TouchesInstrumented int `json:"touches_instrumented"`
	Unclear             int `json:"unclear"`
	NoMeasurableIntent  int `json:"no_measurable_intent"`
	// StrictPct is the share yielding a claim with a named metric. BroadPct adds the ones
	// adjudicable against whatever their instrumented files record — the narrower fallback.
	StrictPct float64 `json:"strict_pct"`
	BroadPct  float64 `json:"broad_pct"`
	// CeilingPct adds Unclear: what derivation could reach IF the product were fully instrumented.
	// The gap between BroadPct and CeilingPct is an instrumentation problem, not a design problem,
	// and those need opposite responses.
	CeilingPct float64 `json:"ceiling_pct"`
}

// Measure runs the deriver over a corpus and reports the yield.
func Measure(prs []PR) (Yield, []Derived) {
	y := Yield{Total: len(prs)}
	out := make([]Derived, 0, len(prs))
	for _, pr := range prs {
		d := Derive(pr)
		out = append(out, d)
		switch d.Verdict {
		case Claimable:
			y.Claimable++
		case TouchesInstrumented:
			y.TouchesInstrumented++
		case Unclear:
			y.Unclear++
		default:
			y.NoMeasurableIntent++
		}
	}
	if y.Total > 0 {
		n := float64(y.Total)
		y.StrictPct = round1(100 * float64(y.Claimable) / n)
		y.BroadPct = round1(100 * float64(y.Claimable+y.TouchesInstrumented) / n)
		y.CeilingPct = round1(100 * float64(y.Claimable+y.TouchesInstrumented+y.Unclear) / n)
	}
	return y, out
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
