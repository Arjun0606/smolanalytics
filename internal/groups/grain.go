package groups

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// Group-grain reports: every existing report, with the ACCOUNT as the unit instead of the person.
//
// This is the B2B question, and it is a different question, not a presentation of the same one.
// "62% of users reached checkout" and "62% of accounts reached checkout" can diverge completely: a
// fifty-seat customer where one admin converts is a converted ACCOUNT and forty-nine unconverted
// users. Reporting the second when someone means the first is how a company concludes its product
// is failing while its revenue says otherwise. Both incumbents charge for this — PostHog bills
// every identified event the moment you enable group analytics, Mixpanel prices it at 40% of
// overage — which tells you it is worth having and also worth not re-implementing badly.
//
// So nothing here recomputes a funnel or a retention curve. Events are RE-KEYED by account and
// handed to the same engines every other surface uses. A group-grain funnel and a user-grain
// funnel are then the same code over different input, and they cannot drift apart into two
// definitions of "converted".

// Grain is the result of re-keying, and the honesty half of the feature.
type Grain struct {
	Property string `json:"property"`
	// Groups is how many distinct accounts the re-keyed slice covers.
	Groups int `json:"groups"`
	// Events kept and dropped. Dropped events belong to nobody: their user never carried the group
	// property on any event, so there is no account to attribute them to. Reporting an
	// account-level funnel over a slice that quietly omits a third of the traffic would be exactly
	// the truncated-denominator lie this codebase has spent its history removing, so the count
	// travels with the result and every surface prints it.
	Kept    int `json:"events_kept"`
	Dropped int `json:"events_dropped"`
	// Unattributed is how many distinct USERS had no account at all — the number that says whether
	// the group property is instrumented properly or only on a corner of the product.
	Unattributed int `json:"users_unattributed"`
	// Note states the coverage in words when it is poor enough to change how the report reads.
	Note string `json:"note,omitempty"`
}

// Regroup re-keys events so the unit of analysis is the account.
//
// First-touch stamping runs first, and it is not optional. A group property is set once — on
// signup, or on an identify — and is absent from the pageviews and clicks that follow. Re-keying
// on the raw property would keep one event per user and drop the rest, so every account-level
// funnel would collapse to a single step. StampFirstTouch carries the account across all of a
// user's events, which is the same rule the segmented funnel already uses; using a different one
// here would make "conversion by company" and "the company funnel" two different reports.
func Regroup(evs []event.Event, property string) ([]event.Event, Grain) {
	g := Grain{Property: property}
	if property == "" {
		return evs, g
	}
	stamped := query.StampFirstTouch(evs, property)

	accounts := map[string]bool{}
	noAccount := map[string]bool{}
	out := make([]event.Event, 0, len(stamped))
	for _, e := range stamped {
		v, ok := e.Properties[property]
		acct := valueOf(v)
		if !ok || acct == "" {
			g.Dropped++
			if e.DistinctID != "" {
				noAccount[e.DistinctID] = true
			}
			continue
		}
		// The account BECOMES the unit. Every engine downstream counts distinct DistinctID, so
		// this one substitution turns user-grain into group-grain with no second implementation
		// of funnels, retention, stickiness or paths.
		e.DistinctID = acct
		accounts[acct] = true
		out = append(out, e)
		g.Kept++
	}
	g.Groups, g.Unattributed = len(accounts), len(noAccount)

	// Coverage is stated when it is bad enough to change the reading. A number computed over a
	// third of the traffic is not wrong, but presenting it without saying so is.
	total := g.Kept + g.Dropped
	switch {
	case total == 0:
		g.Note = "no events carry " + property + ", so there is no account-level view to compute"
	case g.Kept == 0:
		g.Note = "no event carries " + property + " — set it on signup or on an identify call, and " +
			"it will follow each user through the rest of their events"
	case g.Dropped*2 > total:
		g.Note = "only " + itoa(pct(g.Kept, total)) + "% of events could be attributed to a " +
			property + "; the rest belong to users who never carried one, and are excluded from " +
			"every number here rather than counted as an account of their own"
	}
	return out, g
}

// ActiveWithin counts accounts with any activity in the window — the B2B health number that a
// user count cannot express, because one champion logging in daily and a fifty-seat rollout look
// identical at user grain.
func ActiveWithin(regrouped []event.Event, from, to time.Time) int {
	seen := map[string]bool{}
	for _, e := range regrouped {
		ts := e.Timestamp
		if !ts.Before(from) && (to.IsZero() || ts.Before(to)) {
			seen[e.DistinctID] = true
		}
	}
	return len(seen)
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return int(float64(a)/float64(b)*100 + 0.5)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
