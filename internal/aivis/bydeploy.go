package aivis

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Which ship changed what the AI engines say about you.
//
// This is the join no other tool can make. A GEO monitor holds the answers and has never
// heard of your commits. A deploy tracker holds the commits and has never asked an engine
// anything. Both halves are already events on this one instance — the sampler writes
// $geo_check, the CI marker writes a Deploy — so lining them up is a query, not an
// integration.
//
// The honest part matters more than the clever part. AI answers are sampled, not measured:
// the same prompt to the same model twice can differ, so a swing across a deploy is often
// the sampler's noise rather than the release. Every comparison here therefore ships its
// run counts, and Significant is false unless BOTH sides carry enough runs AND the move
// clears a band that already accounts for repeated sampling of the same prompts. A
// dashboard that says "your deploy improved AI visibility" on eight runs a side is lying,
// and it is the exact lie this whole module exists to not tell.

// DeployShift is one release compared against the visibility either side of it.
type DeployShift struct {
	deploys.Deploy
	ShortSHA string `json:"short_sha"`

	// runs behind each side — printed everywhere the rates are, never inferable from them
	RunsBefore int `json:"runs_before"`
	RunsAfter  int `json:"runs_after"`

	MentionedBefore int `json:"mentioned_before_pct"`
	MentionedAfter  int `json:"mentioned_after_pct"`
	MentionedDelta  int `json:"mentioned_delta_pp"`

	RecommendedBefore int `json:"recommended_before_pct"`
	RecommendedAfter  int `json:"recommended_after_pct"`
	RecommendedDelta  int `json:"recommended_delta_pp"`

	// crawler activity across the same boundary. A release that adds server-rendered
	// content usually shows here FIRST — the crawlers have to fetch the new page before
	// any model can start naming it — so a crawl rise with flat visibility is the normal
	// early state, not a contradiction.
	CrawlBefore int `json:"crawl_before"`
	CrawlAfter  int `json:"crawl_after"`

	// Significant gates the prose. Direction is still reported when it is false, because
	// "we cannot tell yet" is a different message from "nothing moved".
	Significant bool   `json:"significant"`
	Direction   string `json:"direction"` // improvement | regression | flat | unknown
	Why         string `json:"why"`       // why it is not significant, when it is not
}

const (
	// runs each side must carry before a comparison is allowed to speak. Sampling is
	// clustered — the same prompts, re-asked — so independent-sample intuition overstates
	// how much a given run count buys. Same floor the visibility verdict uses.
	deployMinRuns = 20
	// percentage points a rate must move before it is called a change rather than noise
	deployMinShiftPP = 15
)

// ByDeploy compares AI visibility in the `window` days before each deploy against the
// `window` days after it. Returned newest-first, capped, and only for deploys that have a
// full window on both sides inside the data — a deploy from this morning cannot be judged
// and is left out rather than shown with a hopeful zero.
func ByDeploy(evs []event.Event, deps []deploys.Deploy, window int, asof time.Time, crawlEvent string) []DeployShift {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	if window <= 0 {
		window = 7
	}
	out := []DeployShift{}
	if len(deps) == 0 {
		return out
	}

	// one pass to split the events we care about, so this stays linear over the log
	type check struct {
		at                   time.Time
		mentioned, recommend bool
	}
	var checks []check
	var crawls []time.Time
	for _, e := range evs {
		switch e.Name {
		case CheckEvent:
			checks = append(checks, check{
				at: e.Timestamp, mentioned: asBool(e.Properties["mentioned"]),
				recommend: asBool(e.Properties["recommended"]),
			})
		case crawlEvent:
			if crawlEvent != "" {
				crawls = append(crawls, e.Timestamp)
			}
		}
	}
	if len(checks) == 0 {
		return out
	}

	span := time.Duration(window) * 24 * time.Hour
	for _, d := range deps {
		at := d.At.UTC()
		// a deploy needs a full window of data on BOTH sides to be judged at all
		if at.After(asof.Add(-span)) {
			continue
		}
		s := DeployShift{Deploy: d, ShortSHA: shortSHA(d.SHA), Direction: "unknown"}
		var mb, rb, ma, ra int
		for _, c := range checks {
			switch {
			case !c.at.Before(at.Add(-span)) && c.at.Before(at):
				s.RunsBefore++
				if c.mentioned {
					mb++
				}
				if c.recommend {
					rb++
				}
			case !c.at.Before(at) && c.at.Before(at.Add(span)):
				s.RunsAfter++
				if c.mentioned {
					ma++
				}
				if c.recommend {
					ra++
				}
			}
		}
		for _, t := range crawls {
			switch {
			case !t.Before(at.Add(-span)) && t.Before(at):
				s.CrawlBefore++
			case !t.Before(at) && t.Before(at.Add(span)):
				s.CrawlAfter++
			}
		}
		if s.RunsBefore == 0 && s.RunsAfter == 0 && s.CrawlBefore == 0 && s.CrawlAfter == 0 {
			continue // nothing was being sampled around this deploy; say nothing about it
		}
		s.MentionedBefore, s.MentionedAfter = pct(mb, s.RunsBefore), pct(ma, s.RunsAfter)
		s.RecommendedBefore, s.RecommendedAfter = pct(rb, s.RunsBefore), pct(ra, s.RunsAfter)
		s.MentionedDelta = s.MentionedAfter - s.MentionedBefore
		s.RecommendedDelta = s.RecommendedAfter - s.RecommendedBefore

		switch {
		case s.RunsBefore < deployMinRuns || s.RunsAfter < deployMinRuns:
			s.Why = "too few sampled runs either side of this ship to tell a real change from sampling noise"
		case abs(s.RecommendedDelta) < deployMinShiftPP && abs(s.MentionedDelta) < deployMinShiftPP:
			s.Direction, s.Why = "flat", "the rates either side are within the noise band for this many runs"
		default:
			s.Significant = true
			// recommendation leads: being named is table stakes, being picked is the outcome
			lead := s.RecommendedDelta
			if abs(s.MentionedDelta) > abs(lead) {
				lead = s.MentionedDelta
			}
			if lead > 0 {
				s.Direction = "improvement"
			} else {
				s.Direction = "regression"
			}
		}
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
