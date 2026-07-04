// Package brief computes the morning "what to fix" digest: the pulse (last N days
// vs the N before), the per-product portfolio split, and the verdict engine's
// findings. One struct feeds the CLI text, JSON, webhook, and the cloud's email
// renderings, so they can never disagree.
package brief

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
)

// Brief is the computed digest.
type Brief struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	Days          int               `json:"days"`
	Visitors      int               `json:"visitors"`
	Events        int               `json:"events"`
	PriorVisitors int               `json:"prior_visitors"`
	PriorEvents   int               `json:"prior_events"`
	Sites         []SiteLine        `json:"sites,omitempty"`
	Findings      []insight.Finding `json:"findings"`
}

// SiteLine is one product's slice of the pulse. The SDK stamps every event's
// `site` (hostname) — the same key the dashboard's site selector filters on —
// so one instance carrying several products splits cleanly per site.
type SiteLine struct {
	Site          string `json:"site"`
	Visitors      int    `json:"visitors"`
	Events        int    `json:"events"`
	PriorVisitors int    `json:"prior_visitors"`
	PriorEvents   int    `json:"prior_events"`
}

// siteAgg accumulates one site's pulse windows; the seen maps dedupe visitors
// per site, so a user active on two products counts once in each.
type siteAgg struct {
	visitors, events, priorVisitors, priorEvents int
	seen, priorSeen                              map[string]bool
}

// Build computes the pulse windows ([now-N, now) vs [now-2N, now-N)) and runs
// the verdict engine. The findings see the FULL history — same as the dashboard —
// so week-over-week and retention reads stay correct even when days narrows the pulse.
func Build(evs []event.Event, days int, now time.Time) Brief {
	b := Brief{GeneratedAt: now, Days: days, Findings: []insight.Finding{}} // [] not null in JSON
	cur := now.AddDate(0, 0, -days)
	prior := now.AddDate(0, 0, -2*days)
	seen, priorSeen := map[string]bool{}, map[string]bool{}
	aggs := map[string]*siteAgg{} // keyed by the `site` property; "" = unstamped
	agg := func(site string) *siteAgg {
		a := aggs[site]
		if a == nil {
			a = &siteAgg{seen: map[string]bool{}, priorSeen: map[string]bool{}}
			aggs[site] = a
		}
		return a
	}
	for _, e := range evs {
		site, _ := e.Properties["site"].(string)
		switch {
		case !e.Timestamp.Before(cur):
			b.Events++
			if !seen[e.DistinctID] {
				seen[e.DistinctID] = true
				b.Visitors++
			}
			a := agg(site)
			a.events++
			if !a.seen[e.DistinctID] {
				a.seen[e.DistinctID] = true
				a.visitors++
			}
		case !e.Timestamp.Before(prior):
			b.PriorEvents++
			if !priorSeen[e.DistinctID] {
				priorSeen[e.DistinctID] = true
				b.PriorVisitors++
			}
			a := agg(site)
			a.priorEvents++
			if !a.priorSeen[e.DistinctID] {
				a.priorSeen[e.DistinctID] = true
				a.priorVisitors++
			}
		}
	}
	b.Sites = siteLines(aggs, b.Events)
	b.Findings = append(b.Findings, insight.Generate(evs)...)
	return b
}

// siteLines turns the per-site pulse into the "By product:" data. The section
// exists only once 2+ named sites report activity — a single-product instance
// keeps the brief byte-for-byte. Unstamped events surface as "(no site)" only
// when they are 2%+ of the current window; a stray untagged event should not
// earn its own line.
func siteLines(aggs map[string]*siteAgg, totalEvents int) []SiteLine {
	named := 0
	for site := range aggs {
		if site != "" {
			named++
		}
	}
	if named < 2 {
		return nil
	}
	var lines []SiteLine
	for site, a := range aggs {
		if site == "" {
			if a.events == 0 || a.events*50 < totalEvents {
				continue
			}
			site = "(no site)"
		}
		lines = append(lines, SiteLine{Site: site, Visitors: a.visitors, Events: a.events,
			PriorVisitors: a.priorVisitors, PriorEvents: a.priorEvents})
	}
	// busiest product first; name breaks ties so the order is stable run to run
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].Events != lines[j].Events {
			return lines[i].Events > lines[j].Events
		}
		return lines[i].Site < lines[j].Site
	})
	return lines
}

// Format renders the digest as plain text — no ANSI, short lines — so it reads
// the same in a terminal, an email body, or a Slack message.
func Format(b Brief) string {
	var s strings.Builder
	fmt.Fprintf(&s, "smolanalytics brief — %s\n\n", b.GeneratedAt.Format("Mon Jan 2, 2006"))
	lastLbl := fmt.Sprintf("Last %d days:", b.Days)
	priorLbl := fmt.Sprintf("Prior %d days:", b.Days)
	fmt.Fprintf(&s, "%-*s %s · %s\n", len(priorLbl), lastLbl, plural(b.Visitors, "visitor"), plural(b.Events, "event"))
	fmt.Fprintf(&s, "%s %s · %s%s\n", priorLbl, plural(b.PriorVisitors, "visitor"), plural(b.PriorEvents, "event"), pulseDelta(b))
	formatSites(&s, b.Sites)
	s.WriteString("\nWhat to look at:\n")
	if len(b.Findings) == 0 {
		s.WriteString("  nothing notable — no big swings, funnel leaks, or retention flags.\n")
	}
	for _, f := range b.Findings {
		mark := "•"
		if f.Severity == "warn" {
			mark = "⚠"
		}
		fmt.Fprintf(&s, "  %s %s — %s\n", mark, f.Title, f.Detail)
	}
	return s.String()
}

// maxSiteLines caps the "By product:" block — past a dozen products the brief
// stops being a morning read; the tail folds into "…and N more".
const maxSiteLines = 12

// formatSites renders the per-product block. Columns align so the eye can scan
// the counts vertically; the delta is per-site events vs the prior window.
func formatSites(s *strings.Builder, sites []SiteLine) {
	if len(sites) == 0 {
		return
	}
	s.WriteString("\nBy product:\n")
	more := 0
	if len(sites) > maxSiteLines {
		more = len(sites) - maxSiteLines
		sites = sites[:maxSiteLines]
	}
	nameW, visW, evW := 0, 0, 0
	for _, l := range sites {
		nameW = max(nameW, len(l.Site))
		visW = max(visW, len(pluralGrouped(l.Visitors, "visitor")))
		evW = max(evW, len(pluralGrouped(l.Events, "event")))
	}
	for _, l := range sites {
		fmt.Fprintf(s, "  %-*s  %*s · %*s  %s\n", nameW, l.Site,
			visW, pluralGrouped(l.Visitors, "visitor"), evW, pluralGrouped(l.Events, "event"), siteDelta(l))
	}
	if more > 0 {
		fmt.Fprintf(s, "  …and %d more\n", more)
	}
}

// siteDelta mirrors pulseDelta per site: "(new)" over a zero baseline instead
// of a fabricated percentage.
func siteDelta(l SiteLine) string {
	if l.PriorEvents == 0 {
		return "(new)"
	}
	return fmt.Sprintf("(%s)", pctChange(l.Events, l.PriorEvents))
}

// pulseDelta renders the change vs the prior window, or says there is nothing to
// compare against — a percentage over a zero baseline would mislead.
func pulseDelta(b Brief) string {
	if b.PriorEvents == 0 {
		return "  (no prior data to compare)"
	}
	return fmt.Sprintf("  (visitors %s, events %s)",
		pctChange(b.Visitors, b.PriorVisitors), pctChange(b.Events, b.PriorEvents))
}

// pctChange is signed ("+12%", "-8%") so direction is unmissable in plain text.
func pctChange(cur, prior int) string {
	return fmt.Sprintf("%+d%%", int(math.Round(float64(cur-prior)/float64(prior)*100)))
}

// pluralGrouped is plural with thousands separators — portfolio counts cross
// 1,000 routinely and raw digit runs misread in a column.
func pluralGrouped(n int, word string) string {
	if n == 1 {
		return group(n) + " " + word
	}
	return group(n) + " " + word + "s"
}

// group inserts commas into a non-negative count ("1893" → "1,893").
func group(n int) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// plural: the brief is read by humans over morning coffee — "1 visitor", not "1 visitors".
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
