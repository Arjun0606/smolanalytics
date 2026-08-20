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

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
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
	// Investigation is the PM half: conclusions, each with a size, a cause and a next move,
	// ranked so the most expensive thing leads.
	//
	// The pulse above tells you traffic moved. That is the raw material a PM would then spend a
	// morning slicing. This is the slicing, already done — which is the difference between a
	// digest someone skims and one they act on.
	Investigation investigate.Investigation `json:"investigation"`
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

// Context is the optional instance state the investigation reads beyond the events: recorded
// flags (the kill list), deploy markers (cause attribution), and the outcome ledger (the
// acted/verified overlay). All optional, all nil-safe.
//
// It exists because the brief was the one surface built from investigate.Run while the dashboard
// used WithContext — so on an instance that records deploys, the emailed finding said "no deploy
// recorded near this day" while the desk named the ship. Same events, two answers; the email
// even invites the reader to record deploys they already record.
type Context struct {
	Flags   []flag.Flag
	Deploys []deploys.Deploy
	Acted   func(string) (time.Time, bool)
	// Planned is the tracking-plan lookup. Without it the emailed brief would report
	// "checkout fell 100%" about the very event the dashboard is calling a tracking break —
	// the same split this type was created to close, one finding kind later.
	Planned investigate.PlanLookup
}

// Build computes the pulse windows ([now-N, now) vs [now-2N, now-N)) and runs
// the verdict engine, without instance context. Prefer BuildCtx wherever the flag, deploy or
// acted stores are in reach.
func Build(evs []event.Event, days int, now time.Time) Brief {
	return BuildCtx(evs, days, now, nil)
}

// BuildCtx is Build with the same instance context the dashboard's investigation gets.
func BuildCtx(evs []event.Event, days int, now time.Time, ctx *Context) Brief {
	b := Brief{GeneratedAt: now, Days: days, Findings: []insight.Finding{}} // [] not null in JSON
	// The investigation sees the FULL history, like the verdict engine below, because a step
	// change is only visible against what came before it. Handing it the pulse window would
	// leave the detector with no "before" and it would find nothing, every time.
	if ctx != nil {
		b.Investigation = investigate.WithContext(evs, ctx.Flags, ctx.Deploys, investigate.Opts{Now: now, Planned: ctx.Planned})
		investigate.ApplyActed(b.Investigation.Findings, ctx.Acted, now)
	} else {
		b.Investigation = investigate.Run(evs, investigate.Opts{Now: now})
	}
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
		// the GEO sampler's own writes are not the product's traffic: one robot id firing
		// daily would show up as a visitor and its volume as growth. The verdict engine
		// already excludes them; the pulse above the verdict must agree, or the digest
		// contradicts itself in its own first line.
		if e.Name == aivis.CheckEvent || e.Name == insight.ReadableEvent || e.Name == aicrawl.CrawlEvent {
			continue
		}
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
	fmt.Fprintf(&s, "smolanalytics brief, %s\n\n", b.GeneratedAt.Format("Mon Jan 2, 2006"))
	lastLbl := fmt.Sprintf("Last %d days:", b.Days)
	priorLbl := fmt.Sprintf("Prior %d days:", b.Days)
	fmt.Fprintf(&s, "%-*s %s · %s\n", len(priorLbl), lastLbl, plural(b.Visitors, "visitor"), plural(b.Events, "event"))
	fmt.Fprintf(&s, "%s %s · %s%s\n", priorLbl, plural(b.PriorVisitors, "visitor"), plural(b.PriorEvents, "event"), pulseDelta(b))
	formatSites(&s, b.Sites)
	formatInvestigation(&s, b.Investigation)
	s.WriteString("\nWhat to look at:\n")
	if len(b.Findings) == 0 {
		s.WriteString("  nothing notable, no big swings, funnel leaks, or retention flags.\n")
	}
	for _, f := range b.Findings {
		mark := "•"
		if f.Severity == "warn" {
			mark = "⚠"
		}
		fmt.Fprintf(&s, "  %s %s, %s\n", mark, f.Title, f.Detail)
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

// formatInvestigation renders the PM section: the triage line, then one block per finding.
//
// The triage line ("3 things changed. 1 needs you.") is the most important string in the whole
// digest. Without it every line reads as equally urgent, which is the same as none of them being
// urgent, and the reader goes back to opening the dashboard.
func formatInvestigation(s *strings.Builder, inv investigate.Investigation) {
	if inv.Quiet {
		// A calm product is a real answer, and saying it plainly is what earns the reader's
		// trust on the day the brief DOES have something. Naming what was scanned separates
		// "nothing happened" from "nothing ran".
		fmt.Fprintf(s, "\nNothing needs you today. Checked %d metric(s) for step changes.\n", len(inv.Scanned))
		formatMovements(s, inv.Movements)
		formatFloors(s, inv.BelowFloor)
		return
	}
	formatMovements(s, inv.Movements)
	formatFloors(s, inv.BelowFloor)
	n := inv.NeedsYouCount()
	fmt.Fprintf(s, "\n%s changed. %d needs you.\n\n", plural(len(inv.Findings), "thing"), n)
	for i, f := range inv.Findings {
		// The chip travels into text: the CLI and the email carry the same states the desk shows.
		tag := ""
		switch {
		case f.Recovered && !f.ActedAt.IsZero():
			tag = "[verified] " // acted on AND recovered — the loop, closed, in an email
		case f.Recovered:
			tag = "[recovered] "
		case !f.ActedAt.IsZero():
			tag = "[acted] "
		case f.NeedsYou:
			tag = "[needs you] "
		}
		fmt.Fprintf(s, "%d. %s%s", i+1, tag, f.Headline)
		// One shared renderer, so this cannot drift from the share page, the dashboard and the
		// backtest — which is precisely what happened while only People was ever printed.
		if sz := f.Cost.Size(); sz != "" {
			fmt.Fprintf(s, "   %s", sz)
		}
		s.WriteString("\n")
		if f.Cause != "" {
			fmt.Fprintf(s, "   %s\n", f.Cause)
		}
		if f.NextMove != "" {
			fmt.Fprintf(s, "   → %s\n", f.NextMove)
		}
		s.WriteString("\n")
	}
}

// formatFloors renders the below-detection-floor notes: which metrics CANNOT yet produce a
// finding, with the arithmetic. Without this the CLI and the email were silent about small
// metrics forever, while the desk explained itself — the exact one-surface-behind split this
// codebase keeps re-learning.
func formatFloors(s *strings.Builder, fs []investigate.FloorNote) {
	if len(fs) == 0 {
		return
	}
	s.WriteString("\nbelow the detection floor:\n")
	for _, f := range fs {
		fmt.Fprintf(s, "   %s runs at ~%.1f/day; step-change detection needs ~%d/day.\n",
			f.Event, f.PerDay, f.NeedPerDay)
	}
}

// formatMovements renders the quarter: did any of it add up.
//
// Printed even on a calm week, and especially then — "nothing needed you this week" beside
// "conversion is unchanged across 23 ships" is the pair of sentences that makes someone act.
// Either alone is comfortable; together they are not.
func formatMovements(s *strings.Builder, ms []investigate.Movement) {
	if len(ms) == 0 {
		return
	}
	s.WriteString("\nAcross the last 90 days:\n")
	for _, m := range ms {
		fmt.Fprintf(s, "  %s\n", m.Headline)
	}
}
