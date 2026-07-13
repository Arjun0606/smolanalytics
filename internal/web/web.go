// Package web composes the one-glance web-analytics view — live visitors, top
// pages, referrers, UTM sources, device split — from $pageview events. This is the
// Plausible-shaped report indie devs otherwise run a SECOND tool for; here it's the
// same engine, same events, one binary. Deterministic like every other report.
package web

import (
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Row is one ranked value (a page, a referrer, a source...).
type Row struct {
	Value    string `json:"value"`
	Count    int    `json:"count"` // pageviews
	Visitors int    `json:"visitors"`
}

// Result is the web overview for a period.
type Result struct {
	PeriodDays  int   `json:"period_days"`
	Visitors    int   `json:"visitors"`  // unique visitors in period
	Pageviews   int   `json:"pageviews"` // total $pageview in period
	LiveNow     int   `json:"live_now"`  // unique visitors in the last 5 minutes
	TopPages    []Row `json:"top_pages"`
	Referrers   []Row `json:"referrers"`    // grouped by host, "" → "direct"
	UTMSources  []Row `json:"utm_sources"`  // only when utm_source is present
	DeviceSplit []Row `json:"device_split"` // mobile / desktop
	// the dimensions people open first — browsers/os come from the ingest-time UA
	// parse, countries from ingest-time geo (when enabled), entry pages from the
	// SDK's session_id, titles from the SDK's title prop
	Browsers     []Row   `json:"browsers"`
	OSes         []Row   `json:"oses"`
	Countries    []Row   `json:"countries"`
	UTMMediums   []Row   `json:"utm_mediums"`
	UTMCampaigns []Row   `json:"utm_campaigns"`
	EntryPages   []Row   `json:"entry_pages"`
	TopTitles    []Row   `json:"top_titles"`
	Hours        [24]int `json:"hours"` // pageviews by UTC hour of day — the activity rhythm
	// engagement — from $engagement events (SDK measures visible+focused time).
	// Omitted (zero) when the SDK predates engagement tracking.
	HasEngagement  bool `json:"has_engagement"`
	AvgEngagedSecs int  `json:"avg_engaged_secs"` // mean engaged time per engaged visitor
	BounceRatePct  int  `json:"bounce_rate_pct"`  // 1 pageview AND <10s engaged
	// the AI channel — humans arriving FROM AI assistants (chatgpt/claude/perplexity...).
	// distinct from AI crawlers, which the bot filter drops before storage.
	AIVisitors  int   `json:"ai_visitors"`
	AIReferrers []Row `json:"ai_referrers"`
}

const pageview = "$pageview"

// Compute builds the overview over the trailing `days` (default 30) as of `asof`.
func Compute(evs []event.Event, days int, asof time.Time) Result {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	if days <= 0 {
		days = 30
	}
	cutoff := asof.AddDate(0, 0, -days)
	liveCutoff := asof.Add(-5 * time.Minute)

	bump := func(m map[string]*agg, key, user string) {
		a := m[key]
		if a == nil {
			a = &agg{visitors: map[string]bool{}}
			m[key] = a
		}
		a.count++
		a.visitors[user] = true
	}

	pages, refs, utms, devices, aiRefs := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	browsers, oses, countries, mediums, campaigns, titles := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	// entry page = the FIRST pageview of each session (fallback: visitor+utc-day when
	// the SDK predates session_id) — "where do people land" as its own dimension
	entryFirst := map[string]event.Event{}
	var hours [24]int
	visitors, live, aiVisitors := map[string]bool{}, map[string]bool{}, map[string]bool{}
	pv := 0
	pvPerUser := map[string]int{}
	engagedMs := map[string]float64{}
	hasEngagement := false

	for _, e := range evs {
		if e.Timestamp.Before(cutoff) || e.Timestamp.After(asof) {
			continue
		}
		if e.Name == "$engagement" {
			if ms, ok := e.Properties["engaged_ms"].(float64); ok && ms > 0 {
				hasEngagement = true
				engagedMs[e.DistinctID] += ms
			}
			continue
		}
		if e.Name != pageview {
			continue
		}
		pv++
		visitors[e.DistinctID] = true
		pvPerUser[e.DistinctID]++
		if !e.Timestamp.Before(liveCutoff) {
			live[e.DistinctID] = true
		}
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(pages, p, e.DistinctID)
		}
		host := refHost(e.Properties["referrer"])
		bump(refs, host, e.DistinctID)
		if aiHosts[host] {
			aiVisitors[e.DistinctID] = true
			bump(aiRefs, host, e.DistinctID)
		} else if u, _ := e.Properties["utm_source"].(string); aiHosts[refHostString(u)] {
			aiVisitors[e.DistinctID] = true
			bump(aiRefs, refHostString(u), e.DistinctID)
		}
		if u, _ := e.Properties["utm_source"].(string); u != "" {
			bump(utms, u, e.DistinctID)
		}
		if d, _ := e.Properties["device"].(string); d != "" {
			bump(devices, d, e.DistinctID)
		}
		if b, _ := e.Properties["browser"].(string); b != "" {
			bump(browsers, b, e.DistinctID)
		}
		if o, _ := e.Properties["os"].(string); o != "" {
			bump(oses, o, e.DistinctID)
		}
		if c, _ := e.Properties["country"].(string); c != "" {
			bump(countries, c, e.DistinctID)
		}
		if m, _ := e.Properties["utm_medium"].(string); m != "" {
			bump(mediums, m, e.DistinctID)
		}
		if cp, _ := e.Properties["utm_campaign"].(string); cp != "" {
			bump(campaigns, cp, e.DistinctID)
		}
		if t, _ := e.Properties["title"].(string); t != "" {
			bump(titles, t, e.DistinctID)
		}
		hours[e.Timestamp.UTC().Hour()]++
		sess, _ := e.Properties["session_id"].(string)
		if sess == "" {
			sess = e.DistinctID + "·" + e.Timestamp.UTC().Format("2006-01-02")
		}
		if first, ok := entryFirst[sess]; !ok || e.Timestamp.Before(first.Timestamp) {
			entryFirst[sess] = e
		}
	}
	entries := map[string]*agg{}
	for _, e := range entryFirst {
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(entries, p, e.DistinctID)
		}
	}

	r := Result{
		PeriodDays:  days,
		Visitors:    len(visitors),
		Pageviews:   pv,
		LiveNow:     len(live),
		TopPages:    rank(pages, 10),
		Referrers:   rank(refs, 10),
		UTMSources:  rank(utms, 10),
		DeviceSplit: rank(devices, 4),
		AIVisitors:  len(aiVisitors),
		AIReferrers: rank(aiRefs, 6),

		Browsers:     rank(browsers, 8),
		OSes:         rank(oses, 8),
		Countries:    rank(countries, 10),
		UTMMediums:   rank(mediums, 10),
		UTMCampaigns: rank(campaigns, 10),
		EntryPages:   rank(entries, 10),
		TopTitles:    rank(titles, 10),
		Hours:        hours,
	}
	if hasEngagement {
		r.HasEngagement = true
		var total float64
		engaged := 0
		for _, ms := range engagedMs {
			total += ms
			engaged++
		}
		if engaged > 0 {
			r.AvgEngagedSecs = int(total / float64(engaged) / 1000)
		}
		// bounce: a visitor with exactly one pageview who engaged under 10 seconds.
		// Only measurable once engagement events exist — never fabricated before that.
		bounced := 0
		for u, n := range pvPerUser {
			if n == 1 && engagedMs[u] < 10_000 {
				bounced++
			}
		}
		if len(pvPerUser) > 0 {
			r.BounceRatePct = int(float64(bounced)/float64(len(pvPerUser))*100 + 0.5)
		}
	}
	return r
}

// aiHosts are the AI assistants real humans click out of — the 2026 acquisition
// channel worth naming. AI *crawlers* (GPTBot etc.) never get this far: the bot
// filter drops them at ingest.
var aiHosts = map[string]bool{
	"chatgpt.com": true, "chat.openai.com": true, "claude.ai": true,
	"perplexity.ai": true, "gemini.google.com": true, "copilot.microsoft.com": true,
	"you.com": true, "poe.com": true, "phind.com": true, "kagi.com": true,
}

// refHostString normalizes a bare string (e.g. a utm_source like "chatgpt.com").
func refHostString(s string) string {
	if s == "" {
		return ""
	}
	return refHost(s)
}

// refHost reduces a raw document.referrer to its host ("" → "direct"), so a
// thousand deep google URLs rank as one "google.com" row.
func refHost(v any) string {
	s, _ := v.(string)
	if s == "" {
		return "direct"
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	if s == "" {
		return "direct"
	}
	return s
}

// agg accumulates pageviews + unique visitors for one ranked value.
type agg struct {
	count    int
	visitors map[string]bool
}

// rank turns an aggregation map into rows sorted by pageviews desc (name asc on
// ties — deterministic like everything else), capped at limit.
func rank(m map[string]*agg, limit int) []Row {
	out := make([]Row, 0, len(m))
	for k, a := range m {
		out = append(out, Row{Value: k, Count: a.count, Visitors: len(a.visitors)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
