package errortrack

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

// The join no error tracker can make and no product-analytics tool bothers to.
//
// Sentry ranks errors by occurrences and can tell you nothing about what they cost, because it
// has never seen your funnel. Product analytics can see the drop-off and cannot tell you an
// exception caused it, because it has never seen your errors. Both streams are ordinary events
// here, so the question "did this bug actually cost us anything" is a computation rather than an
// afternoon of cross-referencing two tabs.
//
// It is stated as a COMPARISON, never as a cause: people who hit this error converted at X, people
// who did not converted at Y. An error that fires on the checkout page will always look damning
// on a checkout funnel, because reaching that page is what exposes you to it. Saying "caused"
// there would be the same fabrication this codebase refuses everywhere else.

// Impact is one error group measured against the funnel.
type Impact struct {
	Fingerprint string `json:"fingerprint"`
	Title       string `json:"title"`
	// Step is where the affected users were furthest along when they hit it — the actionable
	// half, because it names the screen to go and look at.
	Step string `json:"step,omitempty"`
	// Affected/Clean are conversion rates for the two populations, as percentages.
	AffectedUsers int     `json:"affected_users"`
	AffectedConv  float64 `json:"affected_conversion_pct"`
	CleanUsers    int     `json:"clean_users"`
	CleanConv     float64 `json:"clean_conversion_pct"`
	// GapPct is CleanConv - AffectedConv: how many points worse the affected group did. Positive
	// means the error is associated with worse conversion.
	GapPct float64 `json:"gap_pct"`
	// Read is the sentence, and it is careful about the difference between association and cause.
	Read string `json:"read"`
	// Thin says the comparison is too small to mean anything. It is still returned rather than
	// hidden, because "we cannot tell yet" is an answer and an empty row is not.
	Thin bool `json:"thin"`
}

// minCompare is the floor for either side of the comparison. Below it a conversion rate swings by
// double digits on one person, and a ranked list of coin flips reads exactly like a ranked list
// of findings.
const minCompare = 20

// FunnelImpact measures each error group against the funnel the caller is already showing.
//
// Passing the page's own steps in — rather than detecting a funnel here — is deliberate: a report
// that quietly measures a different funnel than the one on screen is the contradiction this
// codebase spent a week removing.
func FunnelImpact(evs []event.Event, groups []Group, steps []funnel.Step, window time.Duration) []Impact {
	if len(steps) < 2 || len(groups) == 0 {
		return nil
	}
	// Who hit which error. Built once for every group rather than per group, so this stays one
	// pass over the events no matter how many distinct errors exist.
	hitBy := map[string]map[string]bool{}
	for _, e := range evs {
		if e.Name != ExceptionEvent {
			continue
		}
		msg, _ := e.Properties["message"].(string)
		src, _ := e.Properties["source"].(string)
		fp := Fingerprint(msg, src, intOf(e.Properties["lineno"]))
		if hitBy[fp] == nil {
			hitBy[fp] = map[string]bool{}
		}
		hitBy[fp][e.DistinctID] = true
	}

	out := make([]Impact, 0, len(groups))
	for _, g := range groups {
		hit := hitBy[g.Fingerprint]
		if len(hit) == 0 {
			continue
		}
		affected, clean := splitUsers(evs, hit)
		aConv, aN := conversionOf(affected, steps, window)
		cConv, cN := conversionOf(clean, steps, window)

		im := Impact{
			Fingerprint: g.Fingerprint, Title: g.Title,
			AffectedUsers: aN, CleanUsers: cN,
			AffectedConv: round1(aConv * 100), CleanConv: round1(cConv * 100),
			Step: furthestStepName(affected, steps, window),
		}
		im.GapPct = round1((cConv - aConv) * 100)

		switch {
		case aN < minCompare || cN < minCompare:
			im.Thin = true
			im.Read = "too few people on one side to compare yet — " + itoa(aN) + " hit this and " +
				itoa(cN) + " did not, and a conversion rate on that many swings on one person"
		case im.GapPct <= 0:
			im.Read = "people who hit this converted about as well as everyone else (" +
				pct(im.AffectedConv) + " against " + pct(im.CleanConv) + "), so it is not costing conversions"
		default:
			im.Read = itoa(aN) + " people hit this and converted at " + pct(im.AffectedConv) +
				", against " + pct(im.CleanConv) + " for everyone else — " + pct(im.GapPct) +
				" worse. Associated, not proven: an error that only fires on a late step will " +
				"always look worse, because reaching that step is what exposes people to it"
		}
		out = append(out, im)
	}
	// Biggest measured gap first, thin comparisons last regardless of their apparent gap — a
	// coin flip at the top of a ranked list is worse than no list.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Thin != out[j].Thin {
			return !out[i].Thin
		}
		if out[i].GapPct != out[j].GapPct {
			return out[i].GapPct > out[j].GapPct
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// splitUsers partitions every user's events into those who hit the error and those who did not.
// Both populations come from the SAME slice, so they are comparable by construction.
func splitUsers(evs []event.Event, hit map[string]bool) (affected, clean []event.Event) {
	for _, e := range evs {
		if hit[e.DistinctID] {
			affected = append(affected, e)
		} else {
			clean = append(clean, e)
		}
	}
	return
}

// conversionOf returns end-to-end conversion and the entrant count, using the same funnel engine
// every other surface uses — never a second implementation of the arithmetic.
func conversionOf(evs []event.Event, steps []funnel.Step, window time.Duration) (float64, int) {
	r := funnel.Compute(evs, steps, window)
	if len(r.Steps) == 0 {
		return 0, 0
	}
	return r.OverallConversion, r.Steps[0].Count
}

// furthestStepName names the step the affected group most often reached last — the screen to go
// and look at.
func furthestStepName(evs []event.Event, steps []funnel.Step, window time.Duration) string {
	r := funnel.Compute(evs, steps, window)
	last := ""
	for _, s := range r.Steps {
		if s.Count == 0 {
			break
		}
		last = s.Event
	}
	return last
}

func pct(f float64) string {
	whole := int(f)
	frac := int((f-float64(whole))*10 + 0.5)
	if frac == 0 {
		return itoa(whole) + "%"
	}
	return itoa(whole) + "." + itoa(frac) + "%"
}
