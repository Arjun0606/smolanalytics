package api

import (
	"sort"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Proposing a funnel from the paths people already walk.
//
// This is the difference between a tool that needs a PM and one that removes the need for one.
// Autocapture gives every instance page views on day one, and those page views already contain the
// journey: /pricing then /signup then /welcome. The product knows what its funnel probably is —
// asking the user to declare it is asking them to type back something we can already see.
//
// It is a PROPOSAL, never a silent assumption. A page-path funnel is a good guess and a bad fact:
// /signup being visited is not someone signing up, and treating it as a conversion would be the
// same class of lie as the invented $pageview → $click funnel it replaces. So this produces a
// suggestion with its real numbers attached, for a human to accept, and the copy says it is a
// guess.

// PathStep is one page in a proposed journey, with the evidence for it.
type PathStep struct {
	Path   string `json:"path"`
	People int    `json:"people"` // distinct people who reached it, IN ORDER
}

// ProposedFunnel is a guess at what this product's conversion actually is.
type ProposedFunnel struct {
	Steps []PathStep `json:"steps"`
	// Confident is true only when the shape looks like a funnel rather than a list of popular
	// pages: each step must genuinely narrow, and the last step must be meaningfully rarer than
	// the first. Without that test, "the three busiest pages" gets proposed as a journey and the
	// suggestion is worse than none.
	Confident bool   `json:"confident"`
	Why       string `json:"why,omitempty"`
}

// funnelIntentPaths are the page names that almost always mean a decision rather than browsing. Matched
// as substrings, lowercased, because /signup, /sign-up, /auth/signup and /en/signup are the same
// intent wearing different routing conventions.
var funnelIntentPaths = []string{
	"checkout", "purchase", "payment", "subscribe", "upgrade", "billing",
	"signup", "sign-up", "register", "join", "trial", "get-started", "welcome",
	"onboard", "install", "connect", "invite", "book", "demo", "contact",
}

// ProposeFunnel builds a funnel suggestion from page paths.
//
// The rule is ORDER-AWARE: a step only counts someone who reached it AFTER the previous one, which
// is what makes this a journey rather than three independent page-popularity numbers. A naive
// version that just counted visits per page would happily propose /pricing → / → /docs, three
// popular pages nobody walks in that order.
func ProposeFunnel(evs []event.Event) ProposedFunnel {
	// first arrival at each path, per person
	firstAt := map[string]map[string]int64{} // path -> person -> unix
	people := map[string]bool{}
	for _, e := range evs {
		if e.Name != "$pageview" || e.DistinctID == "" {
			continue
		}
		p, _ := e.Properties["path"].(string)
		if p == "" {
			continue
		}
		people[e.DistinctID] = true
		if firstAt[p] == nil {
			firstAt[p] = map[string]int64{}
		}
		t := e.Timestamp.Unix()
		if prev, ok := firstAt[p][e.DistinctID]; !ok || t < prev {
			firstAt[p][e.DistinctID] = t
		}
	}
	if len(people) < 10 {
		// Below this a "journey" is a handful of visits and any shape is coincidence. Saying
		// nothing beats proposing something a reader would then have to un-believe.
		return ProposedFunnel{Why: "not enough visits yet to see a pattern"}
	}

	// The entry point is the most-visited page; the destination is the rarest INTENT page that
	// enough people still reach. Ranking intent pages by rarity rather than popularity is the
	// point: the end of a funnel is the page fewest people get to, and the busiest intent page is
	// usually the top of the funnel, not the bottom.
	type cand struct {
		path string
		n    int
	}
	var entry cand
	var intents []cand
	for p, m := range firstAt {
		c := cand{p, len(m)}
		if c.n > entry.n {
			entry = c
		}
		lp := strings.ToLower(p)
		for _, kw := range funnelIntentPaths {
			if strings.Contains(lp, kw) {
				intents = append(intents, c)
				break
			}
		}
	}
	if entry.path == "" || len(intents) == 0 {
		return ProposedFunnel{Why: "no page here looks like a signup, checkout or trial, so there " +
			"is nothing to guess a destination from"}
	}
	sort.Slice(intents, func(i, j int) bool {
		if intents[i].n != intents[j].n {
			return intents[i].n < intents[j].n // rarest first: the end of a funnel is the narrow part
		}
		return intents[i].path < intents[j].path
	})

	// Order-aware counting: someone is at step k only if they reached it after step k-1.
	build := func(paths []string) []PathStep {
		out := make([]PathStep, 0, len(paths))
		reached := map[string]int64{} // person -> when they cleared the previous step
		for i, p := range paths {
			m := firstAt[p]
			next := map[string]int64{}
			for person, t := range m {
				if i == 0 {
					next[person] = t
					continue
				}
				if prev, ok := reached[person]; ok && t >= prev {
					next[person] = t
				}
			}
			out = append(out, PathStep{Path: p, People: len(next)})
			reached = next
		}
		return out
	}

	dest := intents[0]
	paths := []string{entry.path}
	// A middle step, when one exists that sits between the two in both order and volume — a funnel
	// with a middle is far more useful, because the drop is usually there rather than at the end.
	if len(intents) > 1 {
		mid := intents[len(intents)-1]
		if mid.path != entry.path && mid.path != dest.path && mid.n > dest.n {
			paths = append(paths, mid.path)
		}
	}
	if dest.path != entry.path {
		paths = append(paths, dest.path)
	}
	if len(paths) < 2 {
		return ProposedFunnel{Why: "the busiest page and the likeliest destination are the same page"}
	}

	steps := build(paths)
	pf := ProposedFunnel{Steps: steps}
	// Confidence is a shape test, not a vibe. Each step must narrow, and the destination must be
	// genuinely rarer than the entry — otherwise this is a list of popular pages wearing a funnel's
	// clothes, and proposing it would repeat the mistake it exists to fix.
	pf.Confident = true
	for i := 1; i < len(steps); i++ {
		if steps[i].People >= steps[i-1].People {
			pf.Confident = false
		}
	}
	if len(steps) > 0 && steps[len(steps)-1].People*4 > steps[0].People {
		pf.Confident = false // barely narrows: probably navigation, not a funnel
	}
	// A FLOOR on the destination, not just a narrowing test.
	//
	// Caught by looking at the proposal on a live instance: "/ 87 → /contact 5 → /signup 1" passed
	// every narrowing check and was presented as confident. One person is not a pattern, it is an
	// anecdote — and a funnel whose destination has a single visitor produces a conversion rate
	// that swings from 1% to 2% when one more person arrives. Narrowing says the SHAPE is right;
	// it says nothing about whether there is enough there to believe.
	if len(steps) > 0 && steps[len(steps)-1].People < 5 {
		pf.Confident = false
	}
	if !pf.Confident {
		// Two different doubts, and conflating them would tell someone to check the wrong thing.
		if len(steps) > 0 && steps[len(steps)-1].People < 5 {
			pf.Why = "only " + itoaSmall(steps[len(steps)-1].People) + " reached the last step, which is " +
				"too few to call a pattern — the shape looks right but there is not enough here to trust yet"
		} else {
			pf.Why = "these pages do not narrow the way a funnel does, so this is a guess worth checking"
		}
	}
	return pf
}

// itoaSmall renders a small count without pulling in strconv for one call site.
func itoaSmall(n int) string {
	if n <= 0 {
		return "nobody"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
