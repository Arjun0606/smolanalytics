package api

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
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
	ISO       string // YYYY-MM-DD — the who-descriptor's date key
	Count     int
	HeightPct int
	Tip       string // instant CSS tooltip: "Jul 4 · 27" — no native-title hover delay
	Tick      string // x-axis date label under this bar ("" = no tick); every ~5th day
	Peak      bool   // the window's max — annotated with its value, always visible
	GhostPct  int    // the prior equal window's same-position value, same y-scale
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

// funnelBySegment runs the funnel separately for each value of a property — segmentation
// applied to a report, the core Mixpanel move. Uses funnel.ComputeBreakdown so a user is
// segmented by the property on their FIRST step and carried through the whole funnel; the
// old approach filtered EVENTS by the property, which dropped later steps that never carry
// it (e.g. source set only at signup) and understated every segment's conversion.
func funnelBySegment(evs []event.Event, property string, steps []funnel.Step) []segConv {
	if len(steps) == 0 {
		return nil
	}
	segs := funnel.ComputeBreakdown(evs, steps, 7*24*time.Hour, property)
	out := make([]segConv, 0, len(segs))
	for _, s := range segs {
		if s.Value == "(none)" {
			continue // preserve prior behavior: only show segments where the property is set
		}
		users := 0
		if len(s.Steps) > 0 {
			users = s.Steps[0].Count
		}
		out = append(out, segConv{Value: s.Value, Users: users, Conv: pct(s.OverallConversion)})
	}
	return out // ComputeBreakdown already sorts by step-0 users descending
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
	Verdict       []insight.Finding // server-rendered "what to look at" so the front door isn't a JS-only spinner
	Retention     []retRow
	RetDayHeaders []string
	Trend         []trendBar
	BySource      []segRow
	ConvBySeg     []segConv
	Events        []string
	ProductEvents []string // real named events (no $-prefixed internals) for the "your events" ask chips
	Updated       string
	HasData       bool   // false on a fresh install → show the big onboarding
	DevHidden     int    // count of env=development events hidden from production reports
	ShowingDev    bool   // true when ?env=development — viewing the hidden dev traffic
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

	// connect-your-agent artifacts, computed server-side from this instance's own
	// URL + key so every snippet is complete and correct as rendered — never a
	// "<YOUR_KEY_HERE>" placeholder the user has to hand-edit.
	MCPURL        string       // {base}/mcp
	MCPConfig     string       // the single-server JSON object ({"url":...,"headers":...})
	CursorLink    template.URL // cursor://anysphere.cursor-deeplink/mcp/install?name=...&config=b64
	VSCodeLink    template.URL // vscode:mcp/install?{urlencoded JSON}
	ClaudeCodeCmd string       // claude mcp add --transport http ...

	// range + click-to-filter state: one global window and one global filter set that
	// EVERY zone inherits, exactly like the site selector. All state lives in the
	// querystring so every filtered view is a shareable, server-renderable URL.
	RangeDays      int
	Ranges         []rangeVM
	Chips          []chipVM
	VisitorsDelta  string // vs the prior equal window; "" when unknowable
	PageviewsDelta string
	SignupsDelta   string
	SourceProp     string     // the property behind the sources rows (click-to-filter)
	ConvByProp     string     // the property behind conversion-by rows
	LastEventSecs  int        // seconds since the newest ingested event; -1 = none
	ComputeMS      int        // wall time this page took to compute — printed in the footer as a brag
	TrendMax       int        // the chart's y-axis top — rendered as a real scale, not a hover secret
	GhostTotal     int        // prior window's total — 0 hides the ghost legend instead of promising invisible bars
	ChartMetric    string     // the charted event (?metric=), defaults to the detected headline event
	Gran           string     // chart bucket grain (?gran=): day|week|month (hour capped upstream)
	ChartTable     []chartRow // the sortable-data-table half of the chart+table unit
	FunnelOrder    string     // the funnel discipline (?forder=): ordered|strict|unordered
	RetDays        int        // retention horizon (?rdays=): 7|30|90
	RetBucket      string     // retention bucket (?rbucket=): day|week|month
	RetRolling     bool       // on-or-after mode (?rroll=1)
	AgentName      string     // most recent MCP client ("" = never connected)
	AgentAgo       string     // "2m ago"
	AgentLive      bool       // seen within 5 minutes
	AgentCalls     int
	CustomRange    bool   // an explicit ?from/?to window is active
	AnyMode        bool   // filters join with OR (?fm=any) instead of AND
	RangeFrom      string // the custom window's inputs, echoed into the date pickers
	RangeTo        string
	EngagedHuman   string // "13m 23s", never "803s"

	// the data-richness dimensions (geo/devices/campaigns/entries/hours) — computed
	// in internal/web, surfaced as breakdown tabs. Countries carry flag emoji.
	Countries    []segRow
	Browsers     []segRow
	OSes         []segRow
	DeviceRows   []segRow
	UTMSources   []segRow
	UTMMediums   []segRow
	UTMCampaigns []segRow
	EntryPages   []segRow
	Hours        []hourBar
	HasGeo       bool // countries present → render the geo tab + the db-ip credit
}

type hourBar struct {
	Hour      int
	Count     int
	HeightPct int
}

// flagOf turns an ISO 3166-1 alpha-2 code into its flag emoji (regional indicators).
func flagOf(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	r1 := 0x1F1E6 + rune(cc[0]) - 'A'
	r2 := 0x1F1E6 + rune(cc[1]) - 'A'
	return string(r1) + string(r2)
}

type chartRow struct {
	Label string // bucket label ("Jul 8" / "wk of Jul 7" / "Jul 2026")
	Count int
	Prior int    // same-position prior-window value (-1 = unknown)
	Delta string // signed % vs prior, "" when unknowable
	Bar   int    // count as % of the max, for the inline bar
}

type rangeVM struct {
	Label string
	URL   string
	On    bool
}

type chipVM struct{ Prop, Op, Value, Raw, RemoveURL string }

// parseChip decodes one ?f token: "prop:value" (eq) or "prop:op:value"; set/notset
// need no value ("prop:set:"). Caps guard against abuse, not honest use.
func parseChip(raw string) (prop string, op query.Op, val string, ok bool) {
	if len(raw) > 300 {
		return "", "", "", false
	}
	parts := strings.SplitN(raw, ":", 3)
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", "", false
		}
		op = query.Eq
		if parts[0] == "referrer" {
			op = query.Contains
		}
		return parts[0], op, parts[1], true
	case 3:
		o := query.Op(parts[1])
		switch o {
		case query.Eq, query.Neq, query.Contains, query.NotContains, query.Regex, query.Gt, query.Lt, query.Set, query.NotSet:
		default:
			return "", "", "", false
		}
		if parts[0] == "" || (parts[2] == "" && o != query.Set && o != query.NotSet) {
			return "", "", "", false
		}
		return parts[0], o, parts[2], true
	}
	return "", "", "", false
}

// deltaStr renders a signed percent vs the prior window. A zero baseline returns ""
// (no prior period = say nothing) — never a fabricated percentage or filler copy.
func deltaStr(cur, prior int) string {
	if prior == 0 {
		return ""
	}
	d := int(math.Round(float64(cur-prior) / float64(prior) * 100))
	switch {
	case d == 0:
		return "±0%"
	case d > 0:
		return fmt.Sprintf("+%d%%", d)
	default:
		return fmt.Sprintf("-%d%%", -d)
	}
}

// humanDur renders seconds as a human duration — "13m 23s", not "803s".
func humanDur(secs int) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm %ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
}

type goalCard struct {
	Name        string
	Conversions int
	Pct         int
	TopChannel  string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	renderStart := time.Now()
	if r.URL.Path != "/" { // GET / is a catch-all; anything else is a real 404
		s.notFound(w, r)
		return
	}
	evsAll, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		serverError(w, "dashboard store.Range", err)
		return
	}
	// Production scope hides env=development. The browser SDK stamps every localhost
	// load as development (sdk.js), so a developer testing locally sends events that
	// are ingested but invisible here — "I sent events and the dashboard shows nothing"
	// is an unexplained trust-killer. Count what's hidden so the UI can SAY so, and
	// support ?env=development as an opt-in view of exactly that traffic.
	showDev := r.URL.Query().Get("env") == "development"
	devHidden := 0
	for _, e := range evsAll {
		if v, _ := e.Properties["env"].(string); v == "development" {
			devHidden++
		}
	}
	var evs []event.Event
	if showDev {
		evs = query.Apply(evsAll, []query.Filter{{Property: "env", Op: query.Eq, Value: "development"}})
	} else {
		evs = query.Apply(evsAll, nil) // production scope: dev-env events excluded by default
	}

	// the verdict is computed here (global, before the site filter) so it matches
	// /v1/notable exactly — the client refetch then replaces it with identical content
	// and never flashes. Server-rendering it means the "what to look at" front door is
	// real text on first paint and for no-JS/crawler views, not a "reading your data…"
	// spinner (the thing a non-agent evaluator judged the whole product on).
	verdict := insight.Generate(evs)

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

	// range control: ?days=7|30|90 presets, or ?from=YYYY-MM-DD&to=YYYY-MM-DD for
	// arbitrary time travel — every windowed zone below recomputes over the window,
	// and it lives in the querystring so any past view is a shareable URL
	rangeDays := 30
	switch r.URL.Query().Get("days") {
	case "7":
		rangeDays = 7
	case "90":
		rangeDays = 90
	}
	var rangeAsof time.Time // zero = now (presets); set = custom range's end
	customRange := false
	if fs, ts := r.URL.Query().Get("from"), r.URL.Query().Get("to"); fs != "" && ts != "" {
		fromT, errF := time.Parse("2006-01-02", fs)
		toT, errT := time.Parse("2006-01-02", ts)
		if errF == nil && errT == nil && toT.After(fromT) {
			toT = toT.AddDate(0, 0, 1) // inclusive end date
			rangeDays = int(toT.Sub(fromT).Hours() / 24)
			rangeAsof = toT
			customRange = true
		}
	}

	// click-to-filter + the filter builder: repeatable ?f chips scope every report
	// below. Grammar: prop:value (eq) or prop:op:value with op in eq|neq|contains|
	// ncontains|regex|gt|lt|set|notset; multi-value ORs via v1|v2 (the In op).
	// ?fm=any switches the rows from AND to OR. Referrer defaults to substring
	// match — rows show the host, the property stores the URL.
	anyMode := r.URL.Query().Get("fm") == "any"
	var chips []chipVM
	{
		qv := r.URL.Query()
		fs := qv["f"]
		if len(fs) > 8 {
			fs = fs[:8]
		}
		var filters []query.Filter
		for _, raw := range fs {
			p, op, v, ok := parseChip(raw)
			if !ok {
				continue
			}
			f := query.Filter{Property: p, Op: op, Value: v}
			if vs := strings.Split(fmt.Sprint(v), "|"); len(vs) > 1 && (op == query.Eq) {
				arr := make([]any, len(vs))
				for i, x := range vs {
					arr[i] = x
				}
				f = query.Filter{Property: p, Op: query.In, Value: arr}
			}
			filters = append(filters, f)
		}
		if len(filters) > 0 {
			if err := query.Validate(filters); err == nil {
				evs = query.ApplyMode(evs, filters, anyMode)
			}
		}
		for i, raw := range fs {
			p, op, v, ok := parseChip(raw)
			if !ok {
				continue
			}
			_ = op
			nq := url.Values{}
			for k, vals := range qv {
				if k != "f" {
					nq[k] = vals
				}
			}
			for j, o := range fs {
				if j != i {
					nq.Add("f", o)
				}
			}
			u := "/"
			if enc := nq.Encode(); enc != "" {
				u += "?" + enc
			}
			chips = append(chips, chipVM{Prop: p, Op: string(op), Value: v, Raw: raw, RemoveURL: u})
		}
	}

	// the range switcher links, preserving site + filters
	mkRange := func(d int) rangeVM {
		nq := url.Values{}
		for k, vals := range r.URL.Query() {
			if k != "days" {
				nq[k] = vals
			}
		}
		if d != 30 {
			nq.Set("days", fmt.Sprint(d))
		}
		u := "/"
		if enc := nq.Encode(); enc != "" {
			u += "?" + enc
		}
		return rangeVM{Label: fmt.Sprintf("%dd", d), URL: u, On: d == rangeDays}
	}

	names, _ := s.store.Names()
	vol := eventsByVolume(evs)
	fsteps, ftitle := detectFunnel(evs, vol)
	trendEvent := pickEvent(vol, "signup")
	retEvent := pickEvent(vol, "open")
	segProp := detectProp(evs, "plan")
	srcProp := detectProp(evs, "source")

	forder, _ := funnel.ParseOrder(r.URL.Query().Get("forder"))
	rdays := 7
	switch r.URL.Query().Get("rdays") {
	case "30":
		rdays = 30
	case "90":
		rdays = 90
	}
	rbucket := r.URL.Query().Get("rbucket")
	switch rbucket {
	case "", "day", "week", "month":
	default:
		rbucket = "day"
	}
	rroll := boolParam(r.URL.Query().Get("rroll"))
	nowT := time.Now().UTC()
	endT := nowT
	if !rangeAsof.IsZero() {
		endT = rangeAsof
	}
	// the chart's metric + grain are user-selectable and live in the URL like all
	// analysis state (?metric=checkout&gran=week)
	chartMetric := r.URL.Query().Get("metric")
	gran, granErr := trends.ParseInterval(r.URL.Query().Get("gran"))
	if granErr != nil {
		gran = trends.Day
	}
	fr := funnel.ComputeOpts(evs, fsteps, 7*24*time.Hour, funnel.Options{Order: forder})
	rr := retention.ComputeBucketed(evs, rdays, retEvent, rbucket, rroll)
	// the chart and the headline stat both follow the selected range, and the stat
	// carries a delta vs the prior equal window so movement is visible at a glance
	if chartMetric != "" {
		trendEvent = chartMetric
	}
	tr := trends.ComputeInterval(evs, trendEvent, endT.AddDate(0, 0, -rangeDays), rangeAsof, false, gran)
	trPrior := trends.ComputeInterval(evs, trendEvent, endT.AddDate(0, 0, -2*rangeDays), endT.AddDate(0, 0, -rangeDays), false, gran)
	sig30 := tr.Total
	sigPrior := trPrior.Total

	convLabel := ftitle
	if n := len(fsteps); n >= 2 {
		convLabel = fsteps[0].Event + " → " + fsteps[n-1].Event
	}

	vm := dashVM{
		TotalUsers:     distinctUsers(evs),
		Signups:        sig30,
		OverallConv:    pct(fr.OverallConversion),
		Events:         names,
		ProductEvents:  productEvents(names, 8),
		Updated:        time.Now().UTC().Format("Jan 2, 15:04 MST"),
		HasData:        len(evs) > 0,
		Verdict:        verdict,
		DevHidden:      devHidden,
		ShowingDev:     showDev,
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
		RangeDays:      rangeDays,
		GhostTotal:     trPrior.Total,
		FunnelOrder:    string(forder),
		RetDays:        rdays,
		RetBucket:      map[bool]string{true: rbucket, false: "day"}[rbucket != ""],
		RetRolling:     rroll,
		CustomRange:    customRange,
		AnyMode:        anyMode,
		RangeFrom:      r.URL.Query().Get("from"),
		RangeTo:        r.URL.Query().Get("to"),
		Ranges:         []rangeVM{mkRange(7), mkRange(30), mkRange(90)},
		Chips:          chips,
		SourceProp:     srcProp,
		ConvByProp:     segProp,
		SignupsDelta:   deltaStr(sig30, sigPrior),
		LastEventSecs:  -1,
	}
	if ags := s.agentStatus(); len(ags) > 0 {
		a := ags[0]
		vm.AgentName = a.Name
		vm.AgentCalls = a.Calls24h
		since := nowT.Sub(a.LastSeen)
		vm.AgentLive = since < 5*time.Minute
		switch {
		case since < time.Minute:
			vm.AgentAgo = "now"
		case since < time.Hour:
			vm.AgentAgo = fmt.Sprintf("%dm ago", int(since.Minutes()))
		default:
			vm.AgentAgo = fmt.Sprintf("%dh ago", int(since.Hours()))
		}
	}
	if n := len(evsAll); n > 0 {
		// events append in arrival order, so the tail is the newest — this powers the
		// header's "last event Ns ago" liveness stamp
		vm.LastEventSecs = int(nowT.Sub(evsAll[n-1].Timestamp).Seconds())
		if vm.LastEventSecs < 0 {
			vm.LastEventSecs = 0
		}
	}

	// connect-your-agent artifacts: cursor's deeplink takes base64 of the single-server
	// config object (NOT the mcpServers map); vs code takes the urlencoded JSON. Both are
	// complete as rendered — key included — so connecting is one click, never an edit.
	mcpURL := vm.Base + "/mcp"
	cfg := fmt.Sprintf(`{"url":%q`, mcpURL)
	vsCfg := fmt.Sprintf(`{"name":"smolanalytics","type":"http","url":%q`, mcpURL)
	cmd := "claude mcp add --transport http smolanalytics " + mcpURL
	if s.writeKey != "" {
		hdr := fmt.Sprintf(`,"headers":{"Authorization":"Bearer %s"}`, s.writeKey)
		cfg += hdr
		vsCfg += hdr
		cmd += fmt.Sprintf(` --header "Authorization: Bearer %s"`, s.writeKey)
	}
	cfg += "}"
	vsCfg += "}"
	vm.MCPURL = mcpURL
	vm.MCPConfig = cfg
	vm.ClaudeCodeCmd = cmd
	vm.CursorLink = template.URL("cursor://anysphere.cursor-deeplink/mcp/install?name=smolanalytics&config=" + base64.StdEncoding.EncodeToString([]byte(cfg)))
	vm.VSCodeLink = template.URL("vscode:mcp/install?" + url.QueryEscape(vsCfg))

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

	plabel := "D"
	switch rr.Bucket {
	case "week":
		plabel = "W"
	case "month":
		plabel = "M"
	}
	for d := 0; d <= rr.MaxDays; d++ {
		vm.RetDayHeaders = append(vm.RetDayHeaders, fmt.Sprintf("%s%d", plabel, d))
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

	maxT, peakIdx := 1, -1
	for i, p := range tr.Points {
		if p.Count > maxT {
			maxT = p.Count
			peakIdx = i
		}
	}
	// the ghost: the prior equal window aligned position-by-position onto the same
	// x-axis and the SAME y-scale, so "vs what?" is answered by the chart itself
	for _, p := range trPrior.Points {
		if p.Count > maxT {
			maxT = p.Count
		}
	}
	// tick cadence scales with the window so labels never crowd (~6 ticks)
	tickEvery := len(tr.Points) / 6
	if tickEvery < 1 {
		tickEvery = 1
	}
	for i, p := range tr.Points {
		b := trendBar{
			Date:      p.Date.Format("1/2"),
			ISO:       p.Date.Format("2006-01-02"),
			Count:     p.Count,
			HeightPct: int(math.Round(float64(p.Count) / float64(maxT) * 100)),
			Tip:       fmt.Sprintf("%s · %d", p.Date.Format("Jan 2"), p.Count),
			Peak:      i == peakIdx,
		}
		if i%tickEvery == 0 {
			b.Tick = p.Date.Format("Jan 2")
		}
		if i < len(trPrior.Points) {
			pp := trPrior.Points[i]
			b.GhostPct = int(math.Round(float64(pp.Count) / float64(maxT) * 100))
			b.Tip = fmt.Sprintf("%s · %d (prior window %s: %d)", p.Date.Format("Jan 2"), p.Count, pp.Date.Format("Jan 2"), pp.Count)
		}
		vm.Trend = append(vm.Trend, b)
	}
	vm.TrendMax = maxT
	vm.ChartMetric = trendEvent
	vm.Gran = string(gran)
	// the data-table half of the chart+table unit: newest first, capped at 15 rows
	{
		n := len(tr.Points)
		start := 0
		if n > 15 {
			start = n - 15
		}
		lbl := func(t time.Time) string {
			switch gran {
			case trends.Week:
				return "wk of " + t.Format("Jan 2")
			case trends.Month:
				return t.Format("Jan 2006")
			default:
				return t.Format("Jan 2")
			}
		}
		for i := n - 1; i >= start; i-- {
			p := tr.Points[i]
			row := chartRow{Label: lbl(p.Date), Count: p.Count, Prior: -1}
			if maxT > 0 {
				row.Bar = int(math.Round(float64(p.Count) / float64(maxT) * 100))
			}
			if i < len(trPrior.Points) {
				row.Prior = trPrior.Points[i].Count
				row.Delta = deltaStr(p.Count, trPrior.Points[i].Count)
			}
			vm.ChartTable = append(vm.ChartTable, row)
		}
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

	// the web glance — live now, visitors, top pages, referrers over the selected
	// range, with deltas vs the prior equal window. Only shown when $pageview data
	// exists; a backend-only instance stays product-only.
	wv := web.Compute(evs, rangeDays, rangeAsof)
	if wv.Pageviews > 0 {
		wvPrior := web.Compute(evs, rangeDays, endT.AddDate(0, 0, -rangeDays))
		vm.VisitorsDelta = deltaStr(wv.Visitors, wvPrior.Visitors)
		vm.PageviewsDelta = deltaStr(wv.Pageviews, wvPrior.Pageviews)
		vm.HasWeb = true
		vm.ShowProduct = view == "product"
		vm.LiveNow = wv.LiveNow
		vm.Visitors = wv.Visitors
		vm.Pageviews = wv.Pageviews
		vm.HasEngagement = wv.HasEngagement
		vm.EngagedSecs = wv.AvgEngagedSecs
		vm.EngagedHuman = humanDur(wv.AvgEngagedSecs)
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
		vm.EntryPages = toRows(wv.EntryPages, 6)
		vm.Browsers = toRows(wv.Browsers, 6)
		vm.OSes = toRows(wv.OSes, 6)
		vm.DeviceRows = toRows(wv.DeviceSplit, 4)
		vm.UTMSources = toRows(wv.UTMSources, 6)
		vm.UTMMediums = toRows(wv.UTMMediums, 6)
		vm.UTMCampaigns = toRows(wv.UTMCampaigns, 6)
		vm.Countries = toRows(wv.Countries, 10)
		// share-of-total for a PARTIAL dimension is computed against events that carry
		// it — geo stamping starts at the first geo-enabled ingest, so dividing by all
		// pageviews would render an honest 4-visitor day as a bogus "1%"
		geoTotal := 0
		for _, r := range wv.Countries {
			geoTotal += r.Count
		}
		for i := range vm.Countries {
			if geoTotal > 0 {
				vm.Countries[i].Pct = int(math.Round(float64(vm.Countries[i].Count) / float64(geoTotal) * 100))
			}
			if fl := flagOf(vm.Countries[i].Value); fl != "" {
				vm.Countries[i].Value = fl + " " + vm.Countries[i].Value
			}
		}
		vm.HasGeo = len(vm.Countries) > 0
		maxH := 1
		for _, c := range wv.Hours {
			if c > maxH {
				maxH = c
			}
		}
		for h, c := range wv.Hours {
			vm.Hours = append(vm.Hours, hourBar{Hour: h, Count: c, HeightPct: int(math.Round(float64(c) / float64(maxH) * 100))})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	vm.ComputeMS = int(time.Since(renderStart).Milliseconds())
	// a template execution error TRUNCATES the page silently (it already cost us a
	// missing funnel pane once) — always log it, loudly
	if err := dashTmpl.Execute(w, vm); err != nil {
		log.Printf("smolanalytics: DASHBOARD RENDER ERROR (page truncated): %v", err)
	}
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

// productEvents filters the event vocabulary down to real, named product events
// (dropping $-prefixed internals like $pageview) and caps the list, for the
// "your events" discovery chips in the ask bar.
func productEvents(names []string, max int) []string {
	out := make([]string, 0, max)
	for _, n := range names {
		if strings.HasPrefix(n, "$") {
			continue
		}
		out = append(out, n)
		if len(out) >= max {
			break
		}
	}
	return out
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
