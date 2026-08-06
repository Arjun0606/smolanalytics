// Package web composes the one-glance web-analytics view — live visitors, top
// pages, referrers, UTM sources, device split — from $pageview events. This is the
// Plausible-shaped report indie devs otherwise run a SECOND tool for; here it's the
// same engine, same events, one binary. Deterministic like every other report.
package web

import (
	"math"
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
	ExitPages    []Row   `json:"exit_pages"` // last pageview of each session — where visits END
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
	// Every ranked list above is TRUNCATED to its top N. A reader that sums the rows it can
	// see and calls that the total gets a denominator that only covers the visible head: a
	// site with 40 countries, 20 US visitors out of 100, rendered "US 20 · 52%" because the
	// ten visible rows summed to 38. The visible shares then also add to exactly 100%,
	// asserting there is no tail at all. These are the pre-truncation recorded totals — the
	// only honest denominator for "share of recorded" — so no surface has to re-derive one
	// from rows it was already handed in truncated form.
	Recorded Recorded `json:"recorded"`
}

// Recorded is the untruncated total behind each ranked dimension: how many pageviews (for
// per-pageview dimensions) or first-touch visitors (for the per-visitor ones) actually
// carried a value, before rank() cut the list down to what fits on screen.
type Recorded struct {
	TopPages     int `json:"top_pages"`
	Referrers    int `json:"referrers"`
	UTMSources   int `json:"utm_sources"`
	DeviceSplit  int `json:"device_split"`
	Browsers     int `json:"browsers"`
	OSes         int `json:"oses"`
	Countries    int `json:"countries"`
	UTMMediums   int `json:"utm_mediums"`
	UTMCampaigns int `json:"utm_campaigns"`
	EntryPages   int `json:"entry_pages"`
	ExitPages    int `json:"exit_pages"`
	TopTitles    int `json:"top_titles"`
	AIReferrers  int `json:"ai_referrers"`
}

const pageview = "$pageview"

// wallClock is the real present, indirected only so tests can pin it. Every number in this
// report is a fact about the SELECTED window except LiveNow, which is a fact about right
// now, and the two must not be derived from the same clock — see the live block below.
var wallClock = func() time.Time { return time.Now().UTC() }

// CalendarDays is THE definition of "the last N days" for this product: N complete calendar day
// buckets ending today, i.e. [midnight (N-1) days ago, now).
//
// It exists because there were TWO definitions, and they disagreed on live data. The dashboard
// windowed by calendar days (via ComputeRange) while Compute windowed by a rolling N×24h, which
// reaches back a further (24h - time-of-day) — about ten hours at midday. Measured on the demo at
// the same instant, same days=30, same event: /v1/web reported 1,779 visitors and 2,371 pageviews
// while /v1/rows reported 1,753 and 2,336. Same question, two answers.
//
// That is not a rounding difference, it is the product's core promise failing: "ask == dashboard
// == MCP, all from one report engine" is on the footer, and web_overview (MCP), /v1/web (HTTP) and
// the public share page all used the rolling form while the dashboard beside them used the
// calendar form. It is the same defect as the sampler leak that once had the verdict card saying
// 4% retention while the ask bar said 6% three inches above it.
//
// Calendar is the correct side to converge on: parseTrendWindow already documents why (a rolling
// window clips the oldest bucket mid-day and renders a phantom leading partial day on every
// chart), and every trend, funnel, retention and rows query in the product already uses it. One
// definition now, so a surface can only diverge on purpose.
func CalendarDays(days int, asof time.Time) (from, to time.Time) {
	if asof.IsZero() {
		asof = wallClock()
	}
	asof = asof.UTC()
	if days <= 0 {
		days = 30
	}
	return asof.Truncate(24*time.Hour).AddDate(0, 0, -(days - 1)), asof
}

// Compute builds the overview over the last `days` calendar days (default 30) as of `asof`.
func Compute(evs []event.Event, days int, asof time.Time) Result {
	from, to := CalendarDays(days, asof)
	return ComputeRange(evs, from, to)
}

// ComputeWindow is Compute over an arbitrary window rather than a whole number of days, so
// the dashboard's sub-day presets (6h, 12h) can report the SAME window in the tiles as the
// chart. Without it a 6h range moved the chart only and left every tile on 24h, which is a
// range control that lies about what it changed.
func ComputeWindow(evs []event.Event, window time.Duration, asof time.Time) Result {
	if asof.IsZero() {
		asof = wallClock()
	}
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	return ComputeRange(evs, asof.Add(-window), asof)
}

// ComputeRange is ComputeWindow over an EXPLICIT [from, to) — the form the dashboard needs.
//
// ComputeWindow derives its bounds from a duration and an as-of, which produces a ROLLING
// window: "last 10 days" means now−240h. The trends path next to it aligns the same preset to
// calendar days (day0−9d .. now) precisely so the tile, /v1/trends and the MCP tool agree. Two
// tiles sat side by side on the same row, both labelled "· 10d", measuring windows that differ
// by up to a day at each edge — and on a young instance that is the difference between a prior
// window with data in it and one without, so one tile showed "71x vs prior" and the one beside
// it showed no delta at all. Taking the bounds as arguments is what makes ONE window definition
// possible for the whole row.
func ComputeRange(evs []event.Event, from, to time.Time) Result {
	if to.IsZero() {
		to = wallClock()
	}
	if !from.Before(to) {
		from = to.Add(-30 * 24 * time.Hour)
	}
	// Rounded UP, because PeriodDays labels how many day-buckets the window covers, not how many
	// whole 24h spans have elapsed inside it. A calendar window is (N-1) days plus today-so-far,
	// so truncating told a user who asked for 30 days that they were looking at 29 — the range
	// control disagreeing with the caption directly beneath it.
	days := int(math.Ceil(to.Sub(from).Hours() / 24))
	if days < 1 {
		days = 1
	}
	cutoff, asof := from, to
	// "live now" means people on the site AT THIS MOMENT, so it is measured against the wall
	// clock — never against the end of the selected range. Anchored to the range end, opening
	// ?from=2026-01-01&to=2026-01-31 counted the visitors of the five minutes ending in
	// January and the header rendered a green "2 now" pill: two people declared to be on the
	// site right now, months after they left. A range that ends in the past has nobody live in
	// it, and this window intersection says so by construction.
	wall := wallClock()
	liveCutoff := wall.Add(-5 * time.Minute)
	// [from, to): the START is inclusive, the END is not. A window has to be half-open or
	// adjacent windows share an instant — and the dashboard's prior window ends exactly where
	// its current window begins, so an event landing on that boundary was counted in BOTH, and
	// every delta on the KPI row was computed against a prior that included one of the events
	// it was being compared to. Silent, and only ever visible at one nanosecond a month.
	inWindow := func(t time.Time) bool { return !t.Before(cutoff) && t.Before(asof) }

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
	// the SDK predates session_id) — "where do people land" as its own dimension; exit
	// page = the LAST — "where do visits die", the page-level drop-off answer
	entryFirst := map[string]event.Event{}
	exitLast := map[string]event.Event{}
	firstPV := map[string]event.Event{} // each visitor's earliest pageview (first-touch attribution)
	var hours [24]int
	visitors, live, aiVisitors := map[string]bool{}, map[string]bool{}, map[string]bool{}
	pv := 0
	pvPerUser := map[string]int{}
	engagedMs := map[string]float64{}
	hasEngagement := false

	for _, e := range evs {
		if !inWindow(e.Timestamp) {
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
		if !e.Timestamp.Before(liveCutoff) && !e.Timestamp.After(wall) {
			live[e.DistinctID] = true
		}
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(pages, p, e.DistinctID)
		}
		// title is a content dimension (a visitor legitimately sees several), so it
		// stays per-pageview like pages. Acquisition + device dimensions (referrer,
		// utm, device, browser, os, country) are VISITOR attributes and must be
		// attributed once per visitor, or a returning pageview with a different (or
		// missing) referrer double-counts the visitor into two segments — the segments
		// then sum past the visitor total and no breakdown reconciles. Those are
		// resolved first-touch after the loop, from each visitor's earliest pageview.
		if t, _ := e.Properties["title"].(string); t != "" {
			bump(titles, t, e.DistinctID)
		}
		if f, ok := firstPV[e.DistinctID]; !ok || e.Timestamp.Before(f.Timestamp) {
			firstPV[e.DistinctID] = e
		}
		hours[e.Timestamp.UTC().Hour()]++
		sess, _ := e.Properties["session_id"].(string)
		if sess == "" {
			sess = e.DistinctID + "·" + e.Timestamp.UTC().Format("2006-01-02")
		}
		if first, ok := entryFirst[sess]; !ok || e.Timestamp.Before(first.Timestamp) {
			entryFirst[sess] = e
		}
		if last, ok := exitLast[sess]; !ok || e.Timestamp.After(last.Timestamp) {
			exitLast[sess] = e
		}
	}
	// first-touch attribution: each visitor counted once per acquisition/device
	// dimension, so every breakdown sums to exactly the visitor total.
	for id, e := range firstPV {
		host := refHost(e.Properties["referrer"])
		bump(refs, host, id)
		if aiHosts[host] {
			aiVisitors[id] = true
			bump(aiRefs, host, id)
		} else if u, _ := e.Properties["utm_source"].(string); aiHosts[refHostString(u)] {
			aiVisitors[id] = true
			bump(aiRefs, refHostString(u), id)
		}
		if u, _ := e.Properties["utm_source"].(string); u != "" {
			bump(utms, u, id)
		}
		if d, _ := e.Properties["device"].(string); d != "" {
			bump(devices, d, id)
		}
		if b, _ := e.Properties["browser"].(string); b != "" {
			bump(browsers, b, id)
		}
		if o, _ := e.Properties["os"].(string); o != "" {
			bump(oses, o, id)
		}
		if c, _ := e.Properties["country"].(string); c != "" {
			bump(countries, c, id)
		}
		if m, _ := e.Properties["utm_medium"].(string); m != "" {
			bump(mediums, m, id)
		}
		if cp, _ := e.Properties["utm_campaign"].(string); cp != "" {
			bump(campaigns, cp, id)
		}
	}
	entries := map[string]*agg{}
	for _, e := range entryFirst {
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(entries, p, e.DistinctID)
		}
	}
	exits := map[string]*agg{}
	for _, e := range exitLast {
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(exits, p, e.DistinctID)
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
		ExitPages:    rank(exits, 10),
		TopTitles:    rank(titles, 10),
		Hours:        hours,

		Recorded: Recorded{
			TopPages:     recorded(pages),
			Referrers:    recorded(refs),
			UTMSources:   recorded(utms),
			DeviceSplit:  recorded(devices),
			Browsers:     recorded(browsers),
			OSes:         recorded(oses),
			Countries:    recorded(countries),
			UTMMediums:   recorded(mediums),
			UTMCampaigns: recorded(campaigns),
			EntryPages:   recorded(entries),
			ExitPages:    recorded(exits),
			TopTitles:    recorded(titles),
			AIReferrers:  recorded(aiRefs),
		},
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

// recorded is the total behind a dimension BEFORE rank() truncates it — the denominator a
// share-of-recorded percentage needs. Computed from the aggregation map itself so it cannot
// drift from the rows: summing the surviving rows instead is exactly the mistake that made
// the tail of a long-tailed dimension vanish from every percentage on the page.
func recorded(m map[string]*agg) int {
	t := 0
	for _, a := range m {
		t += a.count
	}
	return t
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
