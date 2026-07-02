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

	pages, refs, utms, devices := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	visitors, live := map[string]bool{}, map[string]bool{}
	pv := 0

	for _, e := range evs {
		if e.Name != pageview || e.Timestamp.Before(cutoff) || e.Timestamp.After(asof) {
			continue
		}
		pv++
		visitors[e.DistinctID] = true
		if !e.Timestamp.Before(liveCutoff) {
			live[e.DistinctID] = true
		}
		if p, _ := e.Properties["path"].(string); p != "" {
			bump(pages, p, e.DistinctID)
		}
		bump(refs, refHost(e.Properties["referrer"]), e.DistinctID)
		if u, _ := e.Properties["utm_source"].(string); u != "" {
			bump(utms, u, e.DistinctID)
		}
		if d, _ := e.Properties["device"].(string); d != "" {
			bump(devices, d, e.DistinctID)
		}
	}

	return Result{
		PeriodDays:  days,
		Visitors:    len(visitors),
		Pageviews:   pv,
		LiveNow:     len(live),
		TopPages:    rank(pages, 10),
		Referrers:   rank(refs, 10),
		UTMSources:  rank(utms, 10),
		DeviceSplit: rank(devices, 4),
	}
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
