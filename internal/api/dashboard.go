package api

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

//go:embed dashboard.tmpl.html
var dashboardHTML string

var dashTmpl = template.Must(template.New("dash").Parse(dashboardHTML))

type funnelRow struct {
	Event   string
	Count   int
	PctTop  int // conversion from the top step
	PctPrev int // conversion from the previous step
	Dropped int
	First   bool
}

type retCell struct {
	Label string
	Style template.CSS // background intensity for the heatmap
	Empty bool
}

type retRow struct {
	Date  string
	Size  int
	Cells []retCell
}

type trendBar struct {
	Date      string
	Count     int
	HeightPct int
}

type segRow struct {
	Value  string
	Count  int
	Pct    int
	BarPct int // width relative to the top group
}

// segConv is one segment's funnel conversion — the "pro converts 2x free" insight.
type segConv struct {
	Value string
	Users int
	Conv  int // overall funnel conversion %, this segment
}

// funnelBySegment runs the funnel separately for each value of a property —
// segmentation applied to a report, the core Mixpanel move.
func funnelBySegment(evs []event.Event, property string, steps []funnel.Step) []segConv {
	if len(steps) == 0 {
		return nil
	}
	first := steps[0].Event
	vals := map[string]bool{}
	for _, e := range evs {
		if e.Name == first {
			if v, ok := e.Properties[property]; ok {
				vals[toStr(v)] = true
			}
		}
	}
	out := make([]segConv, 0, len(vals))
	for v := range vals {
		seg := query.Apply(evs, []query.Filter{{Property: property, Op: query.Eq, Value: v}})
		fr := funnel.Compute(seg, steps, 7*24*time.Hour)
		out = append(out, segConv{Value: v, Users: fr.Steps[0].Count, Conv: pct(fr.OverallConversion)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Users > out[j].Users })
	return out
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type dashVM struct {
	TotalUsers    int
	Signups       int
	OverallConv   int
	Funnel        []funnelRow
	Retention     []retRow
	RetDayHeaders []string
	Trend         []trendBar
	BySource      []segRow
	ConvBySeg     []segConv
	Events        []string
	Updated       string
	HasData       bool   // false on a fresh install → show the big onboarding
	Base          string // this server's base URL, for ready-to-paste snippets
	WriteKey      string // this instance's write key — real snippets, not placeholders (key is public-by-design: it ships in tracked pages' HTML)
	// adaptive labels — the default dashboard reflects the user's OWN events
	FunnelTitle    string
	ConvLabel      string // "<first> → <last>" of the detected funnel
	StatEventLabel string // the headline event (e.g. "signup")
	TrendLabel     string
	ConvByTitle    string // segment property for the segmented-funnel card
	SourceTitle    string
	HasConvBy      bool
	HasSource      bool
	// the web-analytics glance (from $pageview autocapture) — present only when
	// pageviews exist, so product-only (backend) instances see nothing extra
	// multi-site: observed `site` values + the currently selected one ("" = all)
	Sites []string
	Site  string
	// web-first screen one, full product view behind the tab; product-only
	// instances (no pageviews) always see the product view.
	ShowProduct bool
	HasWeb      bool
	LiveNow     int
	Visitors    int // unique visitors, 30d
	Pageviews   int // 30d
	TopPages    []segRow
	Referrers   []segRow
	// engagement + the AI channel (shown only when measurable / present)
	HasEngagement bool
	EngagedSecs   int
	BouncePct     int
	AIVisitors    int
	// search console (when the operator connected it)
	HasSearch  bool
	SearchRows []segRow // query → clicks, bar-scaled
	// named goals, resolved over the trailing 30 days
	Goals []goalCard
	// store-presence flags: the share affordance and the goals empty-state form
	// only render when the wrapped store actually exists (no vapor buttons)
	HasShares     bool
	HasGoalsStore bool
}

type goalCard struct {
	Name        string
	Conversions int
	Pct         int
	TopChannel  string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" { // GET / is a catch-all; anything else is a real 404
		s.notFound(w, r)
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		serverError(w, "dashboard store.Range", err)
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default

	// multi-site: every event carries `site` (the SDK stamps hostname). One global
	// selector scopes the WHOLE dashboard — every report below inherits it.
	siteSet := map[string]bool{}
	for _, e := range evs {
		if v, ok := e.Properties["site"].(string); ok && v != "" {
			siteSet[v] = true
		}
	}
	var sites []string
	for v := range siteSet {
		sites = append(sites, v)
	}
	sort.Strings(sites)
	if len(sites) > 20 {
		sites = sites[:20]
	}
	view := r.URL.Query().Get("view")
	site := r.URL.Query().Get("site")
	if site != "" {
		evs = query.Apply(evs, []query.Filter{{Property: "site", Op: query.Eq, Value: site}})
	}

	names, _ := s.store.Names()
	vol := eventsByVolume(evs)
	fsteps, ftitle := detectFunnel(evs, vol)
	trendEvent := pickEvent(vol, "signup")
	retEvent := pickEvent(vol, "open")
	segProp := detectProp(evs, "plan")
	srcProp := detectProp(evs, "source")

	fr := funnel.Compute(evs, fsteps, 7*24*time.Hour)
	rr := retentionOf(evs, 7, retEvent)
	tr := trendOf(evs, trendEvent)
	// the headline stat is genuinely the trailing 30 days (the label says "(30d)")
	sig30 := trends.Compute(evs, trendEvent, time.Now().UTC().AddDate(0, 0, -30), time.Time{}, false).Total

	convLabel := ftitle
	if n := len(fsteps); n >= 2 {
		convLabel = fsteps[0].Event + " → " + fsteps[n-1].Event
	}

	vm := dashVM{
		TotalUsers:     distinctUsers(evs),
		Signups:        sig30,
		OverallConv:    pct(fr.OverallConversion),
		Events:         names,
		Updated:        time.Now().UTC().Format("Jan 2, 15:04 MST"),
		HasData:        len(evs) > 0,
		Sites:          sites,
		Site:           site,
		Base:           baseURL(r),
		WriteKey:       s.writeKey,
		FunnelTitle:    ftitle,
		ConvLabel:      convLabel,
		StatEventLabel: trendEvent,
		TrendLabel:     trendEvent,
		ConvByTitle:    segProp,
		HasConvBy:      segProp != "",
		HasSource:      srcProp != "",
		SourceTitle:    trendEvent + " by " + srcProp,
		HasShares:      s.shares != nil,
		HasGoalsStore:  s.goals != nil,
	}

	for i, st := range fr.Steps {
		vm.Funnel = append(vm.Funnel, funnelRow{
			Event:   st.Event,
			Count:   st.Count,
			PctTop:  pct(st.ConversionFromTop),
			PctPrev: pct(st.ConversionFromPrev),
			Dropped: st.DroppedFromPrev,
			First:   i == 0,
		})
	}

	for d := 0; d <= rr.MaxDays; d++ {
		vm.RetDayHeaders = append(vm.RetDayHeaders, fmt.Sprintf("D%d", d))
	}
	// most-recent cohorts first, capped for a clean grid
	start := 0
	if len(rr.Cohorts) > 12 {
		start = len(rr.Cohorts) - 12
	}
	today := time.Now().UTC().Unix() / 86400
	for i := len(rr.Cohorts) - 1; i >= start; i-- {
		c := rr.Cohorts[i]
		row := retRow{Date: c.Date.Format("Jan 2"), Size: c.Size}
		cohortDay := c.Date.UTC().Unix() / 86400
		for d := 0; d <= rr.MaxDays; d++ {
			// a day that hasn't started yet for this cohort is blank, not "0%" —
			// the grid must never render an unobservable cell as churn.
			if c.Size == 0 || d >= len(c.Returned) || cohortDay+int64(d) > today {
				row.Cells = append(row.Cells, retCell{Empty: true})
				continue
			}
			frac := float64(c.Returned[d]) / float64(c.Size)
			row.Cells = append(row.Cells, retCell{
				Label: fmt.Sprintf("%d%%", int(math.Round(frac*100))),
				Style: template.CSS(fmt.Sprintf("background:rgba(245,166,35,%.2f)", 0.08+0.92*frac)),
			})
		}
		vm.Retention = append(vm.Retention, row)
	}

	maxT := 1
	for _, p := range tr.Points {
		if p.Count > maxT {
			maxT = p.Count
		}
	}
	for _, p := range tr.Points {
		vm.Trend = append(vm.Trend, trendBar{
			Date:      p.Date.Format("1/2"),
			Count:     p.Count,
			HeightPct: int(math.Round(float64(p.Count) / float64(maxT) * 100)),
		})
	}

	// Segmentation: the headline event broken down by the detected source property.
	if srcProp != "" {
		var headline []event.Event
		for _, e := range evs {
			if e.Name == trendEvent {
				headline = append(headline, e)
			}
		}
		groups := query.Breakdown(headline, srcProp)
		top := 0
		if len(groups) > 0 {
			top = groups[0].Count
		}
		for _, g := range groups {
			row := segRow{Value: g.Value, Count: g.Count}
			if len(headline) > 0 {
				row.Pct = int(math.Round(float64(g.Count) / float64(len(headline)) * 100))
			}
			if top > 0 {
				row.BarPct = int(math.Round(float64(g.Count) / float64(top) * 100))
			}
			vm.BySource = append(vm.BySource, row)
		}
	}

	if segProp != "" {
		vm.ConvBySeg = funnelBySegment(evs, segProp, fsteps)
	}

	vm.ShowProduct = true // default; flipped to tabbed mode below when web data exists
	if s.goals != nil {
		for _, d := range s.goals.List() {
			rep := goal.Resolve(evs, d, 30, time.Time{})
			gc := goalCard{Name: d.Name, Conversions: rep.Conversions, Pct: rep.ConversionPct}
			if len(rep.ByReferrer) > 0 {
				gc.TopChannel = rep.ByReferrer[0].Value
			}
			vm.Goals = append(vm.Goals, gc)
		}
	}

	if s.gsc != nil && s.gsc.Connected() {
		rows, _, _, _ := s.gsc.Snapshot()
		top := 0
		if len(rows) > 0 {
			top = rows[0].Clicks
		}
		if len(rows) > 8 {
			rows = rows[:8]
		}
		for _, r := range rows {
			sr := segRow{Value: r.Query, Count: r.Clicks}
			if top > 0 {
				sr.BarPct = int(math.Round(float64(r.Clicks) / float64(top) * 100))
			}
			vm.SearchRows = append(vm.SearchRows, sr)
		}
		vm.HasSearch = len(vm.SearchRows) > 0
	}

	// the web glance — live now, visitors, top pages, referrers (30d). Only shown
	// when $pageview data exists; a backend-only instance stays product-only.
	wv := web.Compute(evs, 30, time.Time{})
	if wv.Pageviews > 0 {
		vm.HasWeb = true
		vm.ShowProduct = view == "product"
		vm.LiveNow = wv.LiveNow
		vm.Visitors = wv.Visitors
		vm.Pageviews = wv.Pageviews
		vm.HasEngagement = wv.HasEngagement
		vm.EngagedSecs = wv.AvgEngagedSecs
		vm.BouncePct = wv.BounceRatePct
		vm.AIVisitors = wv.AIVisitors
		toRows := func(rows []web.Row, n int) []segRow {
			top := 0
			if len(rows) > 0 {
				top = rows[0].Count
			}
			if len(rows) > n {
				rows = rows[:n]
			}
			out := make([]segRow, 0, len(rows))
			for _, r := range rows {
				sr := segRow{Value: r.Value, Count: r.Count}
				if top > 0 {
					sr.BarPct = int(math.Round(float64(r.Count) / float64(top) * 100))
				}
				if wv.Pageviews > 0 {
					sr.Pct = int(math.Round(float64(r.Count) / float64(wv.Pageviews) * 100))
				}
				out = append(out, sr)
			}
			return out
		}
		vm.TopPages = toRows(wv.TopPages, 6)
		vm.Referrers = toRows(wv.Referrers, 6)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = dashTmpl.Execute(w, vm)
}

func pct(f float64) int { return int(math.Round(f * 100)) }

// notFound renders a clean branded 404 instead of the catch-all dashboard.
func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8">`+
		`<title>not found · smolanalytics</title>`+
		`<style>html{background:#0A0A0A;color:#FAFAFA;font-family:ui-monospace,Menlo,monospace}`+
		`body{min-height:100vh;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px}`+
		`a{color:#F5A623;text-decoration:none}.b{font-weight:800;letter-spacing:-.02em;font-size:18px;font-family:Inter,sans-serif}.b i{color:#F5A623;font-style:normal}</style>`+
		`<div class="b">smol<i>analytics</i></div><div style="color:#8E8E8E">404 · nothing here</div><a href="/">← back to dashboard</a>`)
}

// baseURL reconstructs this server's externally-visible URL for paste-ready
// snippets (honors a TLS-terminating proxy).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// --- adaptive dashboard: reflect the user's OWN events, not the demo's schema ---

func eventsByVolume(evs []event.Event) []string {
	c := map[string]int{}
	for _, e := range evs {
		c[e.Name]++
	}
	ns := make([]string, 0, len(c))
	for n := range c {
		ns = append(ns, n)
	}
	sort.Slice(ns, func(i, j int) bool {
		if c[ns[i]] != c[ns[j]] {
			return c[ns[i]] > c[ns[j]]
		}
		return ns[i] < ns[j]
	})
	return ns
}

func hasName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

func pickEvent(vol []string, preferred string) string {
	if hasName(vol, preferred) {
		return preferred
	}
	if len(vol) > 0 {
		return vol[0]
	}
	return ""
}

// detectFunnel uses the conventional signup→activate→checkout when present, else
// the top events ordered by how soon users do them after first contact.
func detectFunnel(evs []event.Event, vol []string) ([]funnel.Step, string) {
	if hasName(vol, "signup") && hasName(vol, "activate") && hasName(vol, "checkout") {
		return []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}, "signup → activate → checkout"
	}
	top := vol
	if len(top) > 3 {
		top = top[:3]
	}
	top = orderByJourney(evs, top)
	steps := make([]funnel.Step, len(top))
	for i, n := range top {
		steps[i] = funnel.Step{Event: n}
	}
	return steps, strings.Join(top, " → ")
}

// orderByJourney sorts events by mean delay from each user's first event, so the
// auto-funnel follows the typical sequence rather than raw volume.
func orderByJourney(evs []event.Event, want []string) []string {
	first := map[string]time.Time{}
	for _, e := range evs {
		if t, ok := first[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			first[e.DistinctID] = e.Timestamp
		}
	}
	type acc struct {
		sum time.Duration
		n   int
	}
	delay := map[string]*acc{}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, e := range evs {
		if !wantSet[e.Name] {
			continue
		}
		a := delay[e.Name]
		if a == nil {
			a = &acc{}
			delay[e.Name] = a
		}
		a.sum += e.Timestamp.Sub(first[e.DistinctID])
		a.n++
	}
	mean := func(n string) time.Duration {
		if a := delay[n]; a != nil && a.n > 0 {
			return a.sum / time.Duration(a.n)
		}
		return 0
	}
	// `want` arrives volume-ordered; a stable sort keeps that order on ties (e.g.
	// identical timestamps from a backfill) so the auto-funnel is deterministic.
	out := append([]string{}, want...)
	sort.SliceStable(out, func(i, j int) bool { return mean(out[i]) < mean(out[j]) })
	return out
}

// detectProp returns the preferred property if present, else the most common one.
func detectProp(evs []event.Event, preferred string) string {
	c := map[string]int{}
	for _, e := range evs {
		for k := range e.Properties {
			c[k]++
		}
	}
	if c[preferred] > 0 {
		return preferred
	}
	best, bestN := "", 0
	for k, n := range c {
		if n > bestN || (n == bestN && best != "" && k < best) {
			best, bestN = k, n
		}
	}
	return best
}
