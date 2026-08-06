package api

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/agent"
	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/fixbrief"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

//go:embed dashboard.tmpl.html
var dashboardHTML string

// comma renders 12345 as "12,345" — big numbers must be readable at a glance.
func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg, s = true, s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

var dashTmpl = template.Must(template.New("dash").Funcs(template.FuncMap{
	"comma": comma,
	// the chart table classified its own CHANGE column by slicing the first character off the
	// delta, which is the same bug buildKPIs had: it read as neither up nor down the moment the
	// delta printed "70x". One definition now, so the two cannot drift apart again.
	"deltadir": deltaDir,
	// ev translates an event name for HUMAN display. The SQL pane and the raw event stream keep
	// the real names, because there the schema IS the interface.
	"ev":      EventLabel,
	"evgloss": EventGloss,
	"evauto":  EventIsAuto,
}).Parse(dashboardHTML))

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
	// Partial marks a bucket still in progress (today, or the current hour). Without it the
	// newest bar is always a stub next to a full prior-window bar, and the change column
	// always reads like a crash — an artefact of the clock, not the product.
	Partial bool
}

type segRow struct {
	Value  string
	Count  int
	Pct    int
	BarPct int // width relative to the top group
	// Flag/Code are set only for country rows. They are separate fields rather than baked
	// into Value because Value is what the reader sees AND what a click-to-filter would send;
	// gluing an emoji onto it makes the display string and the property value the same
	// mangled thing, and the first feature that needs the real code has to unpick it.
	Flag string
	Code string
	// FilterProp/FilterVal drive click-to-filter for rows whose displayed value isn't a
	// raw property (e.g. a first-touch CHANNEL, which may come from source, referrer host,
	// or utm). Empty FilterProp = not click-filterable (e.g. "direct" = absence of a
	// referrer). Other cards leave these empty and use a static data-fp in the template.
	FilterProp string
	FilterVal  string
}

// channelWithFilter is channelOf plus the property+value that click-to-filter should use
// to isolate that channel. Referrer-host channels filter on referrer (now host-aware in
// query.match); "direct" (no referrer at all) isn't a single-value filter, so it's left
// non-clickable rather than filtering to a wrong subset.
func channelWithFilter(e event.Event) (channel, fprop, fval string) {
	if s, _ := e.Properties["source"].(string); s != "" && !genericSource(s) {
		return s, "source", s
	}
	if r, _ := e.Properties["referrer"].(string); r != "" {
		if h := hostOf(r); h != "" {
			return h, "referrer", h
		}
	}
	if u, _ := e.Properties["utm_source"].(string); u != "" {
		return u, "utm_source", u
	}
	return "direct", "", ""
}

// segConv is one segment's funnel conversion — the "pro converts 2x free" insight.
type segConv struct {
	Value string
	Users int
	Conv  int // overall funnel conversion %, this segment
	// Same as segRow: set only when the segment property is `country`, so the card can show
	// a flag and hand the browser a code to expand into a full country name.
	Flag string
	Code string
}

// funnelBySegment runs the funnel separately for each value of a property — segmentation
// applied to a report, the core Mixpanel move. Uses funnel.ComputeBreakdown so a user is
// segmented by the property on their FIRST step and carried through the whole funnel; the
// old approach filtered EVENTS by the property, which dropped later steps that never carry
// it (e.g. source set only at signup) and understated every segment's conversion.
//
// Returns the rateable segments and a COUNT of the ones held back for being too thin to rate.
// opts is the SAME funnel definition the pane directly above it renders (?forder=, and any
// exclusions/step filters once the dashboard grows them). It used to call the optionless
// ComputeBreakdown, so "conversion by X" was hardcoded ordered-with-no-exclusions while the
// funnel above it honoured the toolbar — one page showing two different funnels, with segments
// that could not add up to the total printed over them.
func funnelBySegment(evs []event.Event, property string, steps []funnel.Step, opts funnel.Options) ([]segConv, int) {
	// a real funnel needs ≥2 distinct steps — a degenerate 1-step "funnel" reports a
	// meaningless 100% conversion for every segment ("conversion by plan — pro 100%") on a
	// brand-new instance that has only sent one event type. Suppress it entirely.
	if len(steps) < 2 {
		return nil, 0
	}
	// StampFirstTouch so a segment property that lives only on the acquisition event
	// (source/referrer/device on the landing pageview, not on later funnel steps) still
	// segments the whole funnel — matching /v1/funnel and the MCP funnel tool, which both
	// stamp. Without it this card rendered empty while those surfaces reported the segment.
	segs := funnel.ComputeBreakdownOpts(query.StampFirstTouch(evs, property), steps, 7*24*time.Hour, property, opts)
	out := make([]segConv, 0, len(segs))
	thin := 0
	for _, s := range segs {
		if s.Value == "(none)" {
			continue // preserve prior behavior: only show segments where the property is set
		}
		users := 0
		if len(s.Steps) > 0 {
			users = s.Steps[0].Count
		}
		if users < segConvMinUsers {
			thin++
			continue
		}
		out = append(out, segConv{Value: s.Value, Users: users, Conv: pct(s.OverallConversion)})
	}
	return out, thin // ComputeBreakdown already sorts by step-0 users descending
}

// segConvMinUsers is the floor under which this card refuses to state a conversion rate.
//
// Without it the card was a wall of one-user countries: fourteen rows reading "0%", and one
// reading "100%" off a single visitor with a full-width bar next to it — the best-converting
// segment on the page, drawn from one person. insight already refuses to build a finding on a
// base this thin ("a conversion % on a handful of entrants is noise, not a leak"); the card was
// the one surface still printing it as if it were a result.
//
// Ten, because below that a single user moves the rate by ten points or more, which is wider
// than any difference the card exists to show. The hidden ones are COUNTED on screen rather
// than silently dropped — a card that quietly truncates reads as a card that covered everything.
const segConvMinUsers = 10

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type dashVM struct {
	HasConversion bool // a real funnel (≥2 distinct steps) — gates the conversion KPI/pane
	// FunnelIsGuessed is true when every step came from autocapture, i.e. nobody has defined what
	// success is. The pane must say so rather than presenting the tracker measuring itself as this
	// product's conversion rate.
	FunnelIsGuessed bool
	// Proposal is a funnel read out of the page paths people already walk. Offering it is the
	// difference between a tool that needs a PM and one that removes the need for one: the journey
	// is already in the data, so asking the user to declare it is asking them to type back
	// something we can see.
	Proposal    ProposedFunnel
	TotalUsers  int
	Signups     int
	OverallConv int
	Funnel      []funnelRow
	Verdict     []insight.Finding
	// Findings is the SAME slice from the second element on, composed into rows that rank.
	// The template reads Verdict only for the card (element 0) and Findings for the list.
	Findings []findingLine
	// FixBriefs is every verdict finding's fix brief, keyed by fingerprint, as a JSON island.
	// Computed by the SAME path GET /v1/fix-brief and the fix_brief MCP tool return
	// (fixbrief.ComputeAll), over the funnel THIS PAGE is showing — so the button, the endpoint
	// and the tool can never hand out three different stories about one finding. Rendered
	// server-side rather than fetched: the sheet must open at click speed, and the whole point
	// of the action is that the evidence is already computed.
	FixBriefs template.JS // server-rendered "what to look at" so the front door isn't a JS-only spinner
	// FixOrder is the same briefs' fingerprints in severity order (element 0 = the lead finding).
	// It exists because FixBriefs is a MAP: JSON objects carry no order, so any "open the first
	// one" path over it is really "open the lexicographically smallest sha1".
	FixOrder       template.JS
	Retention      []retRow
	RetDayHeaders  []string
	RetentionReady bool // P2-8: at least one observable post-D0 return exists
	Trend          []trendBar
	BySource       []segRow
	ConvBySeg      []segConv
	ConvByThin     int // segments held back for being too thin to state a rate — said on screen, never silent
	Events         []string
	ProductEvents  []string // real named events (no $-prefixed internals) for the "your events" ask chips
	Updated        string
	HasData        bool   // false on a fresh install → show the big onboarding
	DevHidden      int    // count of env=development events hidden from production reports
	ShowingDev     bool   // true when ?env=development — viewing the hidden dev traffic
	Base           string // this server's base URL, for ready-to-paste snippets
	WriteKey       string // this instance's write key — real snippets, not placeholders (key is public-by-design: it ships in tracked pages' HTML)
	// CloudURL is the header's "Cloud ↗" href. Defaults to smolanalytics.com (right for
	// every self-hosted install); the hosted product overrides it via SMOLANALYTICS_CLOUD_URL
	// so the link leads back to the project the user came from, not the marketing home page.
	CloudURL string
	// adaptive labels — the default dashboard reflects the user's OWN events
	FunnelTitle    string
	ConvLabel      string // "<first> → <last>" of the detected funnel
	StatEventLabel string // the headline event (e.g. "signup")
	TrendLabel     string
	ConvByTitle    string // segment property for the segmented-funnel card
	SourceTitle    string
	HasConvBy      bool
	HasSource      bool
	// the web-analytics glance (from $pageview autocapture) — present only on instances that
	// have EVER recorded a pageview, so product-only (backend) instances see nothing extra
	// multi-site: observed `site` values + the currently selected one ("" = all)
	Sites []string
	Site  string
	// HasWeb is a LIFETIME fact: a $pageview has been recorded here at some point, so the web
	// panes and the Visitors tile belong on the page. WebWindowEmpty is the window fact that
	// used to be conflated with it — pageviews exist, just none in the selected range — and it
	// exists so the empty copy can say "nothing in this window" instead of "you never installed
	// the SDK", which is a different instruction to a different person.
	HasWeb         bool
	WebWindowEmpty bool
	LiveNow        int
	Visitors       int // unique visitors, 30d
	Pageviews      int // 30d
	TopPages       []segRow
	Referrers      []segRow
	// engagement + the AI channel (shown only when measurable / present)
	HasEngagement bool
	EngagedSecs   int
	BouncePct     int
	AIVisitors    int
	// AIRefs is which assistants those visitors arrived FROM — the traffic half of the AI
	// channel. web.Result computed it all along and no surface rendered it until the
	// AI-visibility card gave it the other half to sit beside.
	AIRefs []segRow
	// AIVis is the AI-visibility (GEO) card: what the engines SAY about this product,
	// aggregated from $geo_check sampling events. It sits beside AIVisitors/AIRefs on
	// purpose — nobody else holds both halves, so nobody else can put them side by side.
	AIVis aivisVM

	// AICrawl is the retrieval half of the same story: which AI crawlers actually FETCHED
	// this site, reported by the customer's own server. A JS pixel cannot see any of them,
	// which is why no other analytics product has this number.
	AICrawl aicrawlVM
	// search console (when the operator connected it)
	HasSearch  bool
	SearchRows []segRow // query → clicks, bar-scaled
	// named goals, resolved over the trailing 30 days
	Goals []goalCard
	// premium KPI cards (the glance, reshaped) — each a number + delta + a real sparkline
	KPIs []kpiCard
	// the depth deck — the PostHog-tier analyses (paths / lifecycle / stickiness) that
	// used to live only in the ask bar + MCP, surfaced as cards so the dashboard SHOWS
	// its depth instead of reading like a web-analytics tool. Computed with the exact
	// same functions the MCP tools use, so the numbers agree across surfaces.
	Stickiness   *stickyVM
	Lifecycle    []lifecycleBar
	HasLifecycle bool
	PathStart    string
	PathLevels   []pathLevelVM
	HasPaths     bool
	// store-presence flags: the share affordance and the goals empty-state form
	// only render when the wrapped store actually exists (no vapor buttons)
	HasShares     bool
	HasGoalsStore bool

	// agent observability. HasAgentEvents says whether this instance has ever ingested
	// agent_tool_call/agent_turn — the section renders either way (its empty state teaches
	// exactly what to send), this only decides whether it leads the deck or ends it.
	HasAgentEvents bool
	// ReportQS is this page's scope as a /v1 querystring: the window the toolbar shows,
	// every ?f chip, and the site/env selectors as the property filters /v1 understands.
	// Client-fetched panels ride on it so they answer over the same events the
	// server-rendered zones do, instead of quietly reporting over all history.
	ReportQS string

	// connect-your-agent artifacts, computed server-side from this instance's own
	// URL + key so every snippet is complete and correct as rendered — never a
	// "<YOUR_KEY_HERE>" placeholder the user has to hand-edit.
	MCPURL         string       // {base}/mcp
	MCPConfig      string       // the single-server JSON object ({"url":...,"headers":...})
	CursorLink     template.URL // cursor://anysphere.cursor-deeplink/mcp/install?name=...&config=b64
	VSCodeLink     template.URL // vscode:mcp/install?{urlencoded JSON}
	ClaudeCodeCmd  string       // claude mcp add --transport http ...
	DesktopConfig  string       // claude_desktop_config.json bridge via mcp-remote (Desktop UI is OAuth-only)
	MCPServersJSON string       // full {"mcpServers":{...}} wrapper for mcp.json clients
	ConnectPrompt  string       // the universal paste: any agent reads it and connects itself
	WindsurfConfig string       // windsurf mcp_config.json (serverUrl, NOT url)
	ZedConfig      string       // zed settings.json context_servers block
	ClineConfig    string       // cline_mcp_settings.json (type streamableHttp)

	// range + click-to-filter state: one global window and one global filter set that
	// EVERY zone inherits, exactly like the site selector. All state lives in the
	// querystring so every filtered view is a shareable, server-renderable URL.
	RangeDays      int
	RangeLabel     string // "24h" | "7d" | ... — the ONE name for the window
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
	TrendMid       int        // the y-axis middle tick, so the scale reads without arithmetic
	HoursMax       int        // the hour chart's peak count — its only number, so it gets printed
	GhostTotal     int        // prior window's total — 0 hides the ghost legend instead of promising invisible bars
	ChartMetric    string     // the charted event (?metric=), defaults to the detected headline event
	Gran           string     // chart bucket grain (?gran=): day|week|month (hour capped upstream)
	ChartTable     []chartRow // the sortable-data-table half of the chart+table unit
	// ChartTrunc is the sentence to print when trends trimmed the OLDEST buckets to stay under
	// its display cap (744 hourly points, ~11 years of days). Empty when nothing was trimmed.
	//
	// It has to be said out loud: the cap moves the CHART but never the TOTAL — Total is summed
	// over the whole matched window on purpose — so ?days=60&interval=hour drew the most recent
	// 31 days under a headline covering 60, with nothing on the page admitting the two spans
	// differ. /v1/trends and the MCP tool already ship `truncated` in their JSON; this is the
	// dashboard saying the same thing.
	ChartTrunc  string
	FunnelOrder string // the funnel discipline (?forder=): ordered|strict|unordered
	// Account grain (?grain=company): the funnel + retention panes count ACCOUNTS, not people.
	GrainProp    string   // the group property in effect, "" = user grain
	GrainOptions []string // group properties this instance actually has, for the picker
	GrainGroups  int      // how many accounts the re-keyed slice covers
	GrainNote    string   // coverage note when traffic could not be attributed to an account
	GrainMiss    string   // a requested property nothing carries — shown, never silently ignored
	// GrainOffHref returns to user grain while KEEPING the window, filters and every other
	// analysis parameter. A bare "?grain=" would silently reset the whole page to defaults, so
	// switching the unit would also change the question — the reader would see two numbers move
	// and attribute both to the grain.
	GrainOffHref string
	RetDays      int    // retention horizon (?rdays=): 7|30|90
	RetBucket    string // retention bucket (?rbucket=): day|week|month
	RetRolling   bool   // on-or-after mode (?rroll=1)
	AgentName    string // most recent MCP client ("" = never connected)
	AgentAgo     string // "2m ago"
	AgentLive    bool   // seen within 5 minutes
	AgentCalls   int
	CustomRange  bool   // an explicit ?from/?to window is active
	AnyMode      bool   // filters join with OR (?fm=any) instead of AND
	RangeFrom    string // the custom window's inputs, echoed into the date pickers
	RangeTo      string
	EngagedHuman string // "13m 23s", never "803s"
	// sparklines for the two engagement tiles, computed with the same definitions as the
	// numbers beside them (see engagementSeries)
	SparkBounce  *sparkVM
	SparkEngaged *sparkVM

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

// decorateCountrySegs gives country rows a flag and keeps the raw code alongside the label.
//
// A country row is two letters and nothing else, and "MD 0%" beside "US 3%" is a row most
// readers cannot decode — MD is Moldova to roughly nobody. The flag is computed here because
// it is pure arithmetic on the two letters; the FULL NAME is deliberately not, and is expanded
// in the browser instead (saCountryNames). Every modern browser already ships the whole ISO
// register via Intl.DisplayNames, maintained against CLDR and localised to the reader's own
// language, so shipping a 250-row table in the binary would be worse data, staler, in one
// language, for something that is presentation only — it never enters a filter, the API, or an
// MCP answer.
//
// MaxMind's "ZZ"/"XX" mean anonymous-proxy and unresolved. They get no flag and no code,
// because a wrong flag beside a real number is worse than no flag at all.
func decorateCountrySegs(rows []segConv) {
	for i := range rows {
		code := rows[i].Value
		if code == "ZZ" || code == "XX" || code == "" {
			rows[i].Value = "Unknown"
			continue
		}
		rows[i].Flag = flagOf(code)
		rows[i].Code = code
	}
}

// flagOf turns an ISO 3166-1 alpha-2 code into its flag emoji (regional indicators).
func flagOf(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	// The arithmetic below is only meaningful for A-Z. Without this check a lowercase code,
	// a digit, or any of MaxMind's non-country markers maps to some arbitrary codepoint pair
	// and renders as two unrelated glyphs — a wrong flag beside a real number, which is worse
	// than no flag at all.
	for i := 0; i < 2; i++ {
		if cc[i] < 'A' || cc[i] > 'Z' {
			return ""
		}
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
	// Partial marks the still-running newest bucket, exactly as trendBar.Partial does for the
	// chart. The bar carried the flag and the table did not, so the row for today read
	// "Aug 4 · 14 · prior 96 · -85%" in warning red at 09:00 — a crash that is purely the
	// clock. The table is the ONLY reading a screen reader or a phone gets (the per-bar numbers
	// live in a display:none .tip), so it is the one place the flag had to reach.
	Partial bool
}

// firstEventTime is the earliest timestamp this instance holds — the moment it started
// knowing anything. Zero when there are no events. Future-dated events are ignored: a
// clock-skewed client must not be able to move the start of history.
func firstEventTime(evs []event.Event) time.Time {
	var first time.Time
	now := time.Now().UTC()
	for _, e := range evs {
		if e.Timestamp.IsZero() || e.Timestamp.After(now) {
			continue
		}
		if first.IsZero() || e.Timestamp.Before(first) {
			first = e.Timestamp
		}
	}
	return first
}

// bucketPredatesData reports whether points[i] covers a span that ended before this instance
// recorded anything at all — i.e. its count of 0 means "unmeasured", not "nothing happened".
// The bucket's end is the next bucket's start; for the last one, one granularity step on.
func bucketPredatesData(points []trends.Point, i int, gran trends.Interval, first time.Time) bool {
	if first.IsZero() || i < 0 || i >= len(points) {
		return false
	}
	end := points[i].Date
	if i+1 < len(points) {
		end = points[i+1].Date
	} else {
		switch gran {
		case trends.Hour:
			end = end.Add(time.Hour)
		case trends.Week:
			end = end.AddDate(0, 0, 7)
		case trends.Month:
			end = end.AddDate(0, 1, 0)
		default:
			end = end.AddDate(0, 0, 1)
		}
	}
	return !end.After(first)
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
// deltaStr formats a change against the prior window.
//
// Above 10x it stops using a percentage. "+6875%" is arithmetically correct off a prior of 8 and
// reads as a broken number — nobody parses four figures as a ratio, and a reader who thinks one
// tile is broken discounts every tile beside it. "70x" is the same fact, instantly legible, and
// honest about the fact that the prior window was tiny.
//
// A prior of zero returns nothing at all rather than "+100%" or "∞": there is no percentage
// change from nothing, and inventing one is the kind of number someone screenshots.
// minPriorForDelta is the smallest prior-window total worth comparing against.
//
// Below it, a "change" is arithmetic on noise. Four visits last week and 128 this week is not
// 32x growth, it is an empty prior window with a rounding error in it — and on a freshly
// installed tracker the prior window is ALWAYS nearly empty, so the first thing a new user sees
// is a flattering number that is not true. Every cold reader flagged it, and one put it exactly
// right: the honesty is inconsistent, and it is inconsistent in the direction that flatters.
const minPriorForDelta = 30

func deltaStr(cur, prior int) string {
	if prior == 0 {
		return ""
	}
	// A prior window too small to compare against is stated as such rather than divided by. The
	// caller renders this verbatim, so it has to read as a sentence, not a metric.
	if prior < minPriorForDelta {
		return fmt.Sprintf("no comparison — only %d before", prior)
	}
	ratio := float64(cur) / float64(prior)
	if ratio >= 10 {
		// A bare multiplier hides its own baseline. "32x" and "3.2x" look like the same KIND of
		// claim while resting on wildly different evidence, so the baseline travels with it.
		return fmt.Sprintf("%.0fx vs %d before", ratio, prior)
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

// deltaDir classifies a deltaStr result as "up" | "down" | "" — the arrow, the colour and the
// sparkline's end-marker all hang off it.
//
// It exists because two call sites were reading the FIRST BYTE of the string, which stopped
// being a sign the moment deltaStr learned to print "70x". The biggest movements on the page —
// the only ones big enough to switch to the multiple form — were therefore the ones that
// rendered with no arrow, in the neutral tone, next to smaller changes shown in green. A
// silent, on-screen-only failure: the number was right and every signal around it was missing.
//
// The multiple form is only ever produced for a ratio of 10 or more, so it is always an increase.
func deltaDir(d string) string {
	if d == "" {
		return ""
	}
	// A refusal has no direction. "no comparison — only 4 before" declines to make a claim, so
	// pinning an up-arrow beside it would reassert exactly the claim it just declined.
	if strings.HasPrefix(d, "no comparison") {
		return ""
	}
	// The multiple form. Matched on the "Nx" token rather than a trailing "x", because the string
	// now carries its baseline ("70x vs 80 before") — and the version of this check that tested
	// HasSuffix silently stopped classifying the moment that baseline was appended. The polarity
	// test caught it, which is the second time this exact pair has drifted.
	if strings.Contains(d, "x vs ") || strings.HasSuffix(d, "x") {
		return "up"
	}
	switch d[0] {
	case '+':
		return "up"
	case '-':
		return "down"
	}
	return "" // "±0%": no movement to point at
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

// findingLine is one row of the findings block — the list under the verdict card.
//
// The block's whole job is triage, and the version this replaced could not do it: four bullets
// of undifferentiated prose where "day-1 retention 4%" and "page views up 249%" got identical
// treatment, one of them three lines long, every line ending in the same two links. The sort was
// already right (insight.GenerateForFunnel puts warnings first) and the rendering threw it away.
//
// A row that ranks needs the finding's MACHINE half — severity — and it needs a decision about
// which half of the prose survives. A template can do neither, so the row is composed here and
// the template only prints it.
type findingLine struct {
	Warn      bool
	Mark      string // "!" / "✦": the rank as a character, because colour alone is not a rank
	MarkLabel string // the same rank as a word, for the a11y tree
	// Title and Lead are pre-escaped HTML because exactly one figure per row is wrapped for
	// promotion. Everything that goes into them passes through template.HTMLEscapeString first.
	Title       template.HTML
	Lead        template.HTML // the detail's FIRST sentence: one line per finding, always
	Detail      string        // the whole prose, still on the row for hover and for a screen reader
	Q           string        // the ask the "why?" affordance runs — the finding's raw title
	Fingerprint string
}

// numToken matches the first standalone figure in a sentence: "49%", "1.7×", "1220", "23%".
// The leading boundary is a capture group rather than a look-behind (RE2 has none) and admits
// only start-of-string, whitespace or an open paren — so "Day-1 retention 45%" promotes the 45%
// and not the 1 in "Day-1", and "/blog/2024-guide" promotes nothing. A unit may be preceded by a
// space ("1.7 ×"), a bare number may not, or "1220 people" would swallow the space after it.
var numToken = regexp.MustCompile(`(^|[\s(])(\d[\d,]*(?:\.\d+)?(?:\s?(?:%|×|x\b))?)`)

// smallSampleTail is the suffix insight.qualify() appends. It is the honesty caveat on a thin
// base, so it survives the first-sentence trim that the rest of the prose does not — a number
// that drops "(n=34, small sample)" reads surer than it is.
var smallSampleTail = regexp.MustCompile(`\s*\(n=\d+, small sample\)\s*$`)

// promoteFigure wraps a sentence's first figure so the eye can find the number without reading
// the sentence. Reported so the caller can promote in exactly one place per row.
func promoteFigure(s string) (template.HTML, bool) {
	m := numToken.FindStringSubmatchIndex(s)
	if m == nil {
		return template.HTML(template.HTMLEscapeString(s)), false
	}
	a, b := m[4], m[5] // group 2: the figure itself, without its leading boundary
	return template.HTML(template.HTMLEscapeString(s[:a]) +
		`<b class="vn">` + template.HTMLEscapeString(s[a:b]) + `</b>` +
		template.HTMLEscapeString(s[b:])), true
}

// leadSentence is the first sentence of a finding's detail — the one carrying the figure.
// Everything after it is context, and context belongs in the brief, not in a list whose only
// job is to be scanned.
func leadSentence(detail string) string {
	tail := smallSampleTail.FindString(detail)
	body := strings.TrimSpace(strings.TrimSuffix(detail, tail))
	if i := strings.Index(body, ". "); i >= 0 {
		body = body[:i+1]
	}
	return body + strings.TrimRight(tail, " ")
}

// verdictLines composes the findings block from every finding AFTER the first — the first one
// is the verdict card above the list.
func verdictLines(fs []insight.Finding) []findingLine {
	if len(fs) < 2 {
		return nil
	}
	out := make([]findingLine, 0, len(fs)-1)
	for _, f := range fs[1:] {
		l := findingLine{
			Warn: f.Severity == "warn", Mark: "✦", MarkLabel: "note",
			Detail: f.Detail, Q: f.Title, Fingerprint: f.Fingerprint(),
		}
		if l.Warn {
			l.Mark, l.MarkLabel = "!", "warning"
		}
		// Exactly one figure stands out per row: the title's if the title states one, the lead
		// sentence's otherwise. Two promoted numbers on a line is the undifferentiated prose
		// again, one row further down.
		var promoted bool
		l.Title, promoted = promoteFigure(f.Title)
		lead := leadSentence(f.Detail)
		if promoted {
			l.Lead = template.HTML(template.HTMLEscapeString(lead))
		} else {
			l.Lead, _ = promoteFigure(lead)
		}
		out = append(out, l)
	}
	return out
}

// kpiCard is one premium headline metric: a number, its delta vs the prior window, and a
// real sparkline of the trailing daily series. Accent flags the conversion tile.
type kpiCard struct {
	Label, Value, Delta, Dir string // Dir: "up" | "down" | ""
	Accent                   bool
	Spark                    *sparkVM
	// Proof is the /v1/rows query that re-derives this number from its own rows.
	//
	// The audit against PostHog and Mixpanel called this the biggest INVISIBLE advantage in the
	// product: they roll up and sample, so their numbers have no rows left to show, and ours does.
	// It was reachable from exactly one place — inside a panel, after clicking a chart bar — and
	// nowhere near a KPI. An advantage nobody can see is a cost already paid.
	Proof string
}

// sparkVM holds the precomputed SVG geometry for a sparkline — points are laid out in a
// 64×26 viewBox (Go does the math; the template just draws), with the last point marked.
type sparkVM struct {
	Line, Area string
	EndX, EndY float64
	EndClass   string // "good" | "warn" | ""
}

// buildSpark turns a daily series into sparkline geometry. Fewer than 2 points → no spark.
func buildSpark(series []int, endClass string) *sparkVM {
	if len(series) < 2 {
		return nil
	}
	const w, h, pad = 64.0, 26.0, 2.0
	max := 1
	for _, v := range series {
		if v > max {
			max = v
		}
	}
	n := len(series)
	pts := make([]string, n)
	var lx, ly float64
	for i, v := range series {
		x := float64(i) / float64(n-1) * w
		y := h - pad - float64(v)/float64(max)*(h-2*pad)
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
		lx, ly = x, y
	}
	line := strings.Join(pts, " ")
	return &sparkVM{Line: line, Area: fmt.Sprintf("%s %.1f,%.1f 0.0,%.1f", line, w, h, h), EndX: lx, EndY: ly, EndClass: endClass}
}

// seriesWindow is the ONE definition of what a sparkline covers: the same window the tile
// above it reports. Returns the bucket count, the days per bucket, and the first bucket's
// start — all three series helpers below take it, so none of them can be drawn over a
// different period than its neighbour.
//
// Two failures it exists to stop, both measured on screen:
//   - `now` is the window's END, not today. The helpers anchored on time.Now(), so
//     ?from=2026-01-01&to=2026-01-07 drew a flat August zero-line under "Visitors · 7d 412",
//     with a good/warn end-dot coloured from the January delta.
//   - A window longer than the 30-point cap is BUCKETED, not truncated. Clamping days to 30
//     meant ?days=90 labelled a tile "90d" over a line covering the trailing third of it.
//
// The floor of 2 is buildSpark's: one point is a dot, not a line.
func seriesWindow(days int, now time.Time) (buckets, size int, from time.Time) {
	if days < 2 {
		days = 2
	}
	size = 1
	if days > 30 {
		size = (days + 29) / 30 // days per bucket, so at most 30 points
	}
	buckets = (days + size - 1) / size
	end := now.UTC().Truncate(24 * time.Hour)
	return buckets, size, end.AddDate(0, 0, -(buckets*size - 1))
}

// dailySeries returns per-bucket counts over the window seriesWindow defines, ending on the
// day that contains `now`.
// countUsers=true counts DISTINCT users per bucket (visitors); false counts matching events.
// cumulativeUserSeries is the sparkline for the "Users · all time" tile: distinct users
// known as of the end of each day, so the line RISES to exactly the number printed above
// it. A daily-active series would have been the easy thing to reuse here and it would have
// been a lie — the line would wobble under a number that only ever grows.
//
// Users first seen before the window still count, so the line starts high rather than at
// zero. That is the point: it is cumulative, not per-day.
// engagementSeries returns the per-day bounce-rate (%) and mean-engaged-seconds series for
// the two engagement tiles, using the SAME definitions as internal/web so the sparkline
// cannot drift from the number above it: bounce = visitors with exactly one pageview who
// engaged under 10s, over all visitors with a pageview; engaged = mean engaged time per
// engaged visitor. Both are computed per bucket in one pass rather than by calling the web
// report N times — and a multi-day bucket applies those definitions over the bucket, which is
// exactly what web.ComputeRange does over the window, so the shape still belongs to the number.
//
// Buckets with no pageviews yield 0 for both, which is honest: there is no rate to report.
func engagementSeries(evs []event.Event, days int, now time.Time) (bounce []int, engaged []int) {
	n, size, from := seriesWindow(days, now)
	pv := make([]map[string]int, n)
	ms := make([]map[string]float64, n)
	for i := 0; i < n; i++ {
		pv[i] = map[string]int{}
		ms[i] = map[string]float64{}
	}
	for _, e := range evs {
		ts := e.Timestamp.UTC()
		if ts.Before(from) {
			continue
		}
		i := int(ts.Truncate(24*time.Hour).Sub(from).Hours()/24) / size
		if i < 0 || i >= n {
			continue
		}
		switch e.Name {
		case "$pageview":
			pv[i][e.DistinctID]++
		case "$engagement":
			if v, ok := e.Properties["engaged_ms"].(float64); ok && v > 0 {
				ms[i][e.DistinctID] += v
			}
		}
	}
	bounce, engaged = make([]int, n), make([]int, n)
	for i := 0; i < n; i++ {
		if n := len(pv[i]); n > 0 {
			b := 0
			for u, c := range pv[i] {
				if c == 1 && ms[i][u] < 10_000 {
					b++
				}
			}
			bounce[i] = int(float64(b)/float64(n)*100 + 0.5)
		}
		var total float64
		cnt := 0
		for _, v := range ms[i] {
			total += v
			cnt++
		}
		if cnt > 0 {
			engaged[i] = int(total / float64(cnt) / 1000)
		}
	}
	return bounce, engaged
}

// rangeLabel is the single name for the selected window. The range button said "24h" while
// every KPI tile said "· 1d" for the same range, because each site formatted "%dd" itself.
// rangeWindowLabel names the window including the sub-day presets, so the button, the KPI
// tiles and the pane headers still cannot disagree (see rangeLabel).
func rangeWindowLabel(days, hours int) string {
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return rangeLabel(days)
}

func rangeLabel(days int) string {
	if days == 1 {
		return "24h"
	}
	return fmt.Sprintf("%dd", days)
}

func cumulativeUserSeries(evs []event.Event, days int, now time.Time) []int {
	n, size, from := seriesWindow(days, now)
	first := map[string]time.Time{}
	for _, e := range evs {
		if e.DistinctID == "" {
			continue
		}
		if f, ok := first[e.DistinctID]; !ok || e.Timestamp.Before(f) {
			first[e.DistinctID] = e.Timestamp
		}
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		// end of the i-th bucket in the window
		cutoff := from.AddDate(0, 0, (i+1)*size)
		known := 0
		for _, f := range first {
			if f.Before(cutoff) {
				known++
			}
		}
		out[i] = known
	}
	// Rebase to the window's own floor. buildSpark scales 0..max, and a cumulative total
	// that grew 2% across the window (3040 -> 3100) then draws a flat line pinned to the
	// top with the area filling the whole box — it renders as a featureless block, which
	// is exactly what it looked like on screen. Subtracting the floor shows the SHAPE of
	// the growth, which is all a sparkline claims to show; the tile's number carries the
	// absolute value.
	floor := out[0]
	for _, v := range out {
		if v < floor {
			floor = v
		}
	}
	for i := range out {
		out[i] -= floor
	}
	return out
}

func dailySeries(evs []event.Event, match func(event.Event) bool, days int, now time.Time, countUsers bool) []int {
	n, size, from := seriesWindow(days, now)
	out := make([]int, n)
	var seen []map[string]bool
	if countUsers {
		seen = make([]map[string]bool, n)
		for i := range seen {
			seen[i] = map[string]bool{}
		}
	}
	for _, e := range evs {
		if !match(e) || e.Timestamp.Before(from) {
			continue
		}
		idx := int(e.Timestamp.UTC().Truncate(24*time.Hour).Sub(from).Hours()/24) / size
		if idx < 0 || idx >= n {
			continue
		}
		if countUsers {
			if !seen[idx][e.DistinctID] {
				seen[idx][e.DistinctID] = true
				out[idx]++
			}
		} else {
			out[idx]++
		}
	}
	return out
}

// stickyVM is the DAU/WAU/MAU engagement ratio card.
type stickyVM struct {
	DAU, WAU, MAU int
	Ratio         int    // DAU/MAU %, the stickiness number
	Verdict       string // one plain-English read of the ratio
}

// lifecycleBar is one day's new/returning/resurrected/dormant split, pre-scaled to bar
// widths (percent of the day's largest active count) so the template just draws them.
type lifecycleBar struct {
	Label                                string
	New, Returning, Resurrected, Dormant int
	NewPct, RetPct, ResPct, DormPct      int
}

// pathLevelVM is one hop of the user-journey (paths) report: at depth N, the ranked
// next events users took after the anchor, bar-scaled.
type pathLevelVM struct {
	Depth int
	Steps []segRow
}

// geoWeekBar is one week of the share-of-voice trend, shaped for the .bars/.col chart the
// rest of the deck already draws with. MentPct/RecPct are ALREADY percentages, so they are
// the bar heights directly: this chart's y-axis is a fixed 0-100%, never a rescale against
// the window's max. Nothing here recomputes a rate — every number came out of aivis.
type geoWeekBar struct {
	Label   string
	Tip     string
	MentPct int
	RecPct  int
	// Thin marks a week whose rate rests on too few runs to have settled, or the week
	// still filling. Drawn with the existing .part hatch: visible, but not readable as a
	// settled number.
	Thin bool
}

// geoVerbatim is how one engine described the product on its newest sampled run. The model
// id and the run date are not decoration — the same sentence from a different model on a
// different day is a different fact, and a quote without them is unfalsifiable.
type geoVerbatim struct {
	Engine, Text, Model, When, Ago string
}

// sovSeries is one brand's line in the share-of-voice chart, already projected into SVG
// coordinates. The projection happens in Go, not the template: the template has exactly one
// helper function (comma), and doing arithmetic in it would mean a second place where a
// percentage could be computed differently from aivis.
type sovSeries struct {
	Name   string
	Us     bool
	Color  string
	Points string // polyline "x,y x,y ..."
	Last   int    // final week's share, for the inline legend value
	Share  int    // share across the whole window
	Dot    struct{ X, Y float64 }
}

// sovChart is the hero visual: every brand the engines name, trended. A GEO report that
// shows only your own line answers "are we visible" while the question that pays is
// "are we visible COMPARED TO the alternatives the model keeps offering instead".
type sovChart struct {
	Series []sovSeries
	Weeks  []string // x labels
	First  string
	Last   string
	Has    bool // at least two weeks and one series — below that a line is a dot
}

// aivisVM is the AI-visibility pane. It EMBEDS aivis.Result — the identical struct
// /v1/ai-visibility and the MCP ai_visibility tool serialize — so this card cannot drift
// from the other two surfaces. Every other field is presentation over those same numbers:
// bar heights, human dates, tooltips. No rate is derived here.
type aivisVM struct {
	aivis.Result
	Weeks     []geoWeekBar
	Comps     []segRow
	Verbatims []geoVerbatim
	// Latest is the newest week, for the headline tiles. A pointer so the template's
	// {{with}} skips it cleanly when no check landed in the window.
	Latest      *aivis.WeekPoint
	LatestLabel string
	FirstWeek   string
	LastWeek    string
	// ShowTrend gates the chart at two weeks. One bar is a reading, not a direction, and
	// drawing it as a trend is the exact dishonesty the low-sample rule exists to stop.
	ShowTrend bool
	SOV       sovChart
	// Sent/Rank/Cited are the signals the sampler already captured and no surface rendered.
	SentPos, SentNeu, SentNeg, SentRated int
	BrandRank, BrandCount                int
	RankRows                             []segRow
	CitedRate, CitedRuns, GroundRuns     int
	CitedRec, CitedNotRec                int
	// Ever says a $geo_check has EVER been ingested here, read from the store's event NAMES
	// rather than this window. "nothing yet" and "nothing HERE" are different empty states
	// with different fixes, and conflating them is how a filtered-out report reads as a
	// feature nobody ever set up.
	Ever bool
	// Scoped means a site/env/filter chip is narrowing the page, so an empty card might
	// mean the filters excluded the checks rather than that none exist. $geo_check events
	// carry no site or path, so ANY web-property filter excludes all of them.
	Scoped   bool
	WidenURL string // the 90d view of this same page, filters preserved
	// Managed says the cloud runs the sampler for this instance. A hosted customer must be
	// shown a link to the switch, NEVER a curl: handing someone a POST body for a job we
	// already do for them reads as "this feature is not finished".
	Managed  bool
	CloudURL string
	// Ships is the join no other tool can make: each release compared against the AI
	// visibility either side of it. Populated at the call site because it needs the deploy
	// store, which this builder deliberately does not take — it keeps the pure computation
	// pure and testable without a store.
	Ships []aivis.DeployShift
}

// buildKPIs assembles the headline metric cards from the already-computed numbers, each
// with a real trailing-daily sparkline. This is the glance, elevated — the first thing a
// builder sees, and it should feel effortless: the numbers that matter, moving.
func buildKPIs(vm *dashVM, evs []event.Event, trendEvent string, days, hours int, now time.Time) {
	dir := deltaDir
	endOf := func(d string) string {
		switch dir(d) {
		case "up":
			return "good"
		case "down":
			return "warn"
		}
		return ""
	}
	// The window, phrased the way /v1/rows phrases it. The first version hardcoded days=N, and
	// the 6h/12h presets force rangeDays to 1 — so on a six-hour window "show the rows" opened a
	// FULL DAY of rows underneath a six-hour number. A proof link that disagrees with the figure
	// it proves is worse than no proof link: it spends the trust it was built to earn.
	proofWindow := "days=" + strconv.Itoa(days)
	if hours > 0 {
		proofWindow = "hours=" + strconv.Itoa(hours)
	}

	var cards []kpiCard
	if vm.HasWeb {
		d := vm.VisitorsDelta
		cards = append(cards, kpiCard{
			// "People", not "visitors": the page used four nouns for one thing (visitors, users,
			// people, accounts) across five totals, and one cold reader answered "how many people
			// use my product?" with "somewhere between 128 and 136, I think." Reconciling the
			// NUMBERS is a separate fix; sharing one word is what makes the numbers comparable
			// enough to notice when they disagree.
			Label: "People · " + rangeWindowLabel(days, hours), Value: comma(vm.Visitors), Delta: d, Dir: dir(d),
			// unique=1, because this tile counts PEOPLE and the default recomputation counts
			// events. Without it the proof would open 2,336 pageview rows under a figure of
			// 1,753 people and call it verification.
			//
			// This number went unproven while the one beside it was proven, which made the
			// headline figure — the first thing anyone reads — the one thing they could not
			// check. It could not be wired earlier for a real reason: web.Compute and /v1/rows
			// windowed differently, so the link would have contradicted the tile. Unifying the
			// window (web.CalendarDays) is what made this honest, not a decision to show more.
			Proof: "event=" + url.QueryEscape("$pageview") + "&" + proofWindow + "&unique=1",
			Spark: buildSpark(dailySeries(evs, func(e event.Event) bool { return e.Name == "$pageview" }, days, now, true), endOf(d)),
		})
	}
	sd := vm.SignupsDelta
	cards = append(cards, kpiCard{
		// Translated HERE and not at the source, because StatEventLabel is load-bearing twice: it is
		// this tile's caption AND the raw event name the metric picker and SA_EVENT hand back to
		// /v1/trends. Translating it at the source would turn "$pageview" into "page view" in a
		// query string and quietly break the chart.
		Label: EventLabel(vm.StatEventLabel) + " · " + rangeWindowLabel(days, hours), Value: comma(vm.Signups), Delta: sd, Dir: dir(sd),
		Proof: "event=" + url.QueryEscape(vm.StatEventLabel) + "&" + proofWindow,
		Spark: buildSpark(dailySeries(evs, func(e event.Event) bool { return e.Name == trendEvent }, days, now, false), endOf(sd)),
	})
	if vm.ConvLabel != "" && vm.HasConversion {
		cards = append(cards, kpiCard{Label: vm.ConvLabel, Value: fmt.Sprintf("%d%%", vm.OverallConv), Accent: true})
	}
	cards = append(cards, kpiCard{Label: "People · all time", Value: comma(vm.TotalUsers), Spark: buildSpark(cumulativeUserSeries(evs, days, now), "")})
	vm.KPIs = cards
}

// retentionGrid renders a retention.Result as the cohort grid: the period headers, the rows
// (most-recent cohort first, capped at 12), and whether the grid is worth showing at all.
//
// It is a function rather than inline page code because its one hard rule — which cells may
// be shown — has to be the SAME rule /v1/retention and the MCP tool apply, and inline it was
// not. See retention.Observable for the two defects that came out of re-deriving it here.
func retentionGrid(rr retention.Result, now time.Time) (headers []string, rows []retRow, ready bool) {
	plabel := "D"
	switch rr.Bucket {
	case "week":
		plabel = "W"
	case "month":
		plabel = "M"
	}
	for d := 0; d <= rr.MaxDays; d++ {
		headers = append(headers, fmt.Sprintf("%s%d", plabel, d))
	}
	// most-recent cohorts first, capped for a clean grid
	start := 0
	if len(rr.Cohorts) > 12 {
		start = len(rr.Cohorts) - 12
	}
	// P2-8: retention needs at least one cohort old enough to have an OBSERVABLE
	// return past period 0. Below that the grid is a near-empty triangle plus a
	// tautological 100% D0 column — worse than saying "not enough history yet".
	for i := len(rr.Cohorts) - 1; i >= start; i-- {
		c := rr.Cohorts[i]
		for d := 1; d <= rr.MaxDays && d < len(c.Returned); d++ {
			if c.Size > 0 && retention.Observable(rr, c.Date, d, now) {
				ready = true
				break
			}
		}
	}
	for i := len(rr.Cohorts) - 1; i >= start; i-- {
		c := rr.Cohorts[i]
		row := retRow{Date: c.Date.Format("Jan 2"), Size: c.Size}
		for d := 0; d <= rr.MaxDays; d++ {
			// A period that has not FULLY elapsed for this cohort is blank, not "0%": the grid
			// must never render an unobservable (or half-collected) cell as churn.
			//
			// This used to be `cohortDate.Unix()/86400 + d > today`, with `d` counting PERIODS
			// and the division counting DAYS. On ?rbucket=week a same-week cohort therefore read
			// `20 | 100% | 0% | 0% | 0% | 0% | 0%` — weeks beginning 2 to 37 days in the FUTURE
			// printed as finished churn — and month buckets were off by 30x. The `>` was wrong
			// too: the period that started twenty minutes ago rendered as a final number while
			// /v1/retention nulled that cell and the summary excluded that cohort, three
			// different answers to one question on one instance.
			if c.Size == 0 || d >= len(c.Returned) || !retention.Observable(rr, c.Date, d, now) {
				row.Cells = append(row.Cells, retCell{Empty: true})
				continue
			}
			frac := float64(c.Returned[d]) / float64(c.Size)
			// The ramp tops out at 0.52, and the text never flips. Measured over the full
			// 0.08-1.00 amber wash this grid used to use, there is a dead band from about
			// 0.54 to 0.60 where NEITHER #F0ECDF nor #12100C reaches 4.5:1 — so no flip
			// threshold could have been right, and the old one (0.45) chose dark text at a
			// point where dark was 3.0:1 and light would have been 5.7:1. Capping the wash
			// keeps one text colour AA across every cell (worst case 4.71:1 at the top of
			// the ramp) and still leaves a clearly graded fill from rgb(40,34,23) to
			// rgb(138,97,29). A retention number a reader cannot make out is not a
			// weaker signal than a bright cell — it is no signal.
			a := 0.08 + 0.44*frac
			row.Cells = append(row.Cells, retCell{
				Label: fmt.Sprintf("%d%%", int(math.Round(frac*100))),
				Style: template.CSS(fmt.Sprintf("background:rgba(245,166,35,%.2f);color:#F0ECDF", a)),
			})
		}
		rows = append(rows, row)
	}
	return headers, rows, ready
}

// buildDepthCards computes the paths / lifecycle / stickiness deck from the SAME engine
// functions the MCP tools call (engagement.*, paths.After), so the dashboard and the ask
// bar can never disagree. Each card self-hides when the data is too thin to be honest.
func buildDepthCards(vm *dashVM, evs []event.Event, fsteps []funnel.Step, trendEvent string, now time.Time) {
	// Stickiness — DAU/WAU/MAU + the DAU/MAU ratio, the one-glance "are they hooked" read.
	st := engagement.ComputeStickiness(evs, now)
	if st.MAU > 0 {
		ratio := int(st.DAUoverMAU*100 + 0.5)
		verdict := "low: most users don't come back daily"
		switch {
		case ratio >= 50:
			verdict = "excellent: daily-habit territory"
		case ratio >= 20:
			verdict = "healthy for most products"
		case ratio >= 10:
			verdict = "typical: a weekly, not daily, habit"
		}
		vm.Stickiness = &stickyVM{DAU: st.DAU, WAU: st.WAU, MAU: st.MAU, Ratio: ratio, Verdict: verdict}
	}

	// Lifecycle — new / returning / resurrected / dormant per day, the growth-accounting
	// view (are new users offsetting churn?). Show the trailing 14 days, bar-scaled.
	lc := engagement.ComputeLifecycle(evs, 14)
	for _, d := range lc {
		// each day's bar is a COMPOSITION of that day's active users (new + returning +
		// resurrected), so the segments sum to ~100% and you read "how much of today is
		// new vs loyal" at a glance. Dormant is churn OUT of the day, shown as its own count.
		active := d.New + d.Returning + d.Resurrected
		if active == 0 {
			continue
		}
		scale := func(n int) int { return int(float64(n)/float64(active)*100 + 0.5) }
		vm.Lifecycle = append(vm.Lifecycle, lifecycleBar{
			Label: d.Date.Format("Jan 2"),
			New:   d.New, Returning: d.Returning, Resurrected: d.Resurrected, Dormant: d.Dormant,
			NewPct: scale(d.New), RetPct: scale(d.Returning), ResPct: scale(d.Resurrected), DormPct: 0,
		})
	}
	vm.HasLifecycle = len(vm.Lifecycle) > 0

	// Paths — what users do AFTER the anchor event (first funnel step, else the headline).
	// This is the user-journey / pathfinder report; a marquee product-analytics feature.
	start := trendEvent
	if len(fsteps) > 0 {
		start = fsteps[0].Event
	}
	if start != "" {
		pr := paths.After(evs, start, 3)
		if pr.Users > 0 {
			vm.PathStart = start
			for _, lvl := range pr.Levels {
				top := 0
				for _, s := range lvl.Steps {
					if s.Count > top {
						top = s.Count
					}
				}
				rows := make([]segRow, 0, len(lvl.Steps))
				for i, s := range lvl.Steps {
					if i >= 5 {
						break
					}
					r := segRow{Value: s.Event, Count: s.Count}
					if pr.Users > 0 {
						r.Pct = int(float64(s.Count)/float64(pr.Users)*100 + 0.5)
					}
					if top > 0 {
						r.BarPct = int(float64(s.Count)/float64(top)*100 + 0.5)
					}
					rows = append(rows, r)
				}
				if len(rows) > 0 {
					vm.PathLevels = append(vm.PathLevels, pathLevelVM{Depth: lvl.Depth, Steps: rows})
				}
			}
			vm.HasPaths = len(vm.PathLevels) > 0
		}
	}
}

// buildAIVis renders the AI-visibility card from aivis.Compute over THIS PAGE'S window and
// filters — the same evs slice, day count and asof web.ComputeWindow gets below — so the
// card can never quietly answer over a wider range than the toolbar advertises. That
// covenant is why this pane ships no window control of its own.
func buildAIVis(vm *dashVM, evs []event.Event, names []string, days int, asof time.Time, scoped bool, widenURL, cloudURL string) {
	av := aivisVM{
		Result:   aivis.Compute(evs, days, asof),
		Ever:     hasName(names, "$geo_check"),
		Scoped:   scoped,
		WidenURL: widenURL,
		Managed:  cloudURL != "",
		CloudURL: cloudURL,
	}
	end := asof
	if end.IsZero() {
		end = time.Now().UTC()
	}
	// the Monday of the bucket still in progress, derived with the SAME expression aivis
	// buckets by, so "is this the current week" cannot drift from the bucket key itself
	cw := end.UTC()
	curWeek := cw.AddDate(0, 0, -((int(cw.Weekday()) + 6) % 7)).Format("2006-01-02")

	label := func(iso string) string {
		t, err := time.Parse("2006-01-02", iso)
		if err != nil {
			return iso
		}
		return t.Format("Jan 2")
	}
	plural := func(n int) string {
		if n == 1 {
			return ""
		}
		return "s"
	}
	for _, w := range av.Trend {
		lbl := label(w.WeekStart)
		// the caveat rides on the bar you are looking at, not in a footnote under the card
		why := ""
		switch {
		case w.WeekStart == curWeek:
			why = " · week still filling"
		case w.Runs < 3:
			why = " · under 3 runs, not settled"
		}
		av.Weeks = append(av.Weeks, geoWeekBar{
			Label:   lbl,
			MentPct: w.MentionedPct,
			RecPct:  w.RecommendedPct,
			Thin:    w.Runs < 3 || w.WeekStart == curWeek,
			Tip: fmt.Sprintf("wk of %s · %d run%s · mentioned %d · recommended %d",
				lbl, w.Runs, plural(w.Runs), w.MentionedPct, w.RecommendedPct) + "%" + why,
		})
	}
	if n := len(av.Trend); n > 0 {
		last := av.Trend[n-1] // copy: av is about to be copied into the vm by value
		av.Latest = &last
		av.LatestLabel = label(last.WeekStart)
		av.FirstWeek, av.LastWeek = label(av.Trend[0].WeekStart), av.LatestLabel
	}
	av.ShowTrend = len(av.Weeks) >= 2

	// --- the share-of-voice chart, projected into SVG space here so the template does no
	// arithmetic. Y is a PERCENTAGE of that week's brand mentions, fixed 0-100, because a
	// rescaled y-axis makes a flat line look like a collapse.
	{
		// amber is ours; the competitor ramp is deliberately desaturated so the eye finds our
		// line first in a chart that may hold five of them
		// Distinguishable HUES, not five shades of grey. The competing tools are criticised
		// by name for palettes you cannot tell apart at five series, and a legend nobody can
		// map back to a line is a chart that does not work. Amber stays reserved for us, so
		// our line is findable before the legend is read.
		palette := []string{"#4EA8DE", "#B57BEE", "#E5726B", "#4FB286", "#C9A227"}
		const w, h = 640.0, 150.0
		// av.Weeks is the BAR chart's rows; the shared x-axis lives on the embedded result
		n := len(av.Result.Weeks)
		if n >= 2 {
			// each week's denominator: every brand mention that week, so the series sum to 100%
			tot := make([]int, n)
			for _, b := range av.Share {
				for i, v := range b.Weekly {
					if i < n {
						tot[i] += v
					}
				}
			}
			ci := 0
			// The CHART draws the capped set (us, the loudest few, and the rest as one line).
			// av.Share stays complete for the table below it. Plotting all of them drew 58
			// overlapping lines flat against zero under a 58-item legend, which reads as broken
			// or invented rather than as data.
			plot := av.SharePlot
			if len(plot) == 0 {
				plot = av.Share
			}
			for _, b := range plot {
				if len(b.Weekly) != n {
					continue
				}
				ser := sovSeries{Name: b.Name, Us: b.Us, Share: b.SharePct, Color: "var(--accent)"}
				if !b.Us {
					ser.Color = palette[ci%len(palette)]
					ci++
				}
				pts := make([]string, 0, n)
				for i, v := range b.Weekly {
					p := 0
					if tot[i] > 0 {
						p = int(float64(v)/float64(tot[i])*100 + 0.5)
					}
					x := w * float64(i) / float64(n-1)
					y := h - (h * float64(p) / 100)
					pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
					if i == n-1 {
						ser.Last, ser.Dot.X, ser.Dot.Y = p, x, y
					}
				}
				ser.Points = strings.Join(pts, " ")
				av.SOV.Series = append(av.SOV.Series, ser)
			}
			av.SOV.Weeks = av.Result.Weeks
			av.SOV.First, av.SOV.Last = label(av.Result.Weeks[0]), label(av.Result.Weeks[n-1])
			av.SOV.Has = len(av.SOV.Series) > 0
		}
	}
	// Rank among every brand the engines name, stated plainly even when it is unflattering —
	// "you are 4th of 4" is the sentence that makes someone act, and hiding it is how a
	// visibility dashboard becomes decoration.
	// Rank by TOTAL MENTIONS, never by position in av.Share — that slice is sorted with our
	// own series first so the chart's primary line is unambiguous, so reading an index off it
	// would report "#1 of 5" to a product sitting last. A flattering number computed from a
	// display order is worse than no number.
	if len(av.Share) > 0 {
		usTotal, usPresent := 0, false
		for _, b := range av.Share {
			if b.Us {
				usTotal, usPresent = b.Total, true
			}
		}
		if usPresent {
			rank := 1
			for _, b := range av.Share {
				if !b.Us && b.Total > usTotal {
					rank++
				}
			}
			av.BrandRank, av.BrandCount = rank, len(av.Share)
		} else {
			// Never named in a single answer, so there is no "us" series to rank. Ranking
			// against an absent series produced "#4 of 3 brands named" — a position in a list
			// we are not in. Being absent is the sharper fact anyway: BrandRank 0 makes the
			// card say so instead of inventing a place for us.
			av.BrandRank, av.BrandCount = 0, len(av.Share)+1
		}
	}
	av.SentPos, av.SentNeu, av.SentNeg, av.SentRated = av.Sentiment.Positive, av.Sentiment.Neutral, av.Sentiment.Negative, av.Sentiment.Rated
	av.CitedRate, av.CitedRuns, av.GroundRuns = av.Result.CitedRate, av.Result.CitedRuns, av.Result.GroundRuns
	av.CitedRec, av.CitedNotRec = av.Result.CitedRecommended, av.Result.CitedNotRecommended
	for _, rb := range av.RankDist {
		av.RankRows = append(av.RankRows, segRow{Value: rb.Label, Count: rb.Runs, BarPct: rb.Pct, Pct: rb.Pct})
	}

	top := 0
	for _, c := range av.Competitors {
		if c.Mentions > top {
			top = c.Mentions
		}
	}
	for i, c := range av.Competitors {
		if i >= 8 {
			break
		}
		row := segRow{Value: c.Name, Count: c.Mentions}
		if top > 0 {
			row.BarPct = int(float64(c.Mentions)/float64(top)*100 + 0.5)
		}
		// deliberately NO FilterProp: `competitors` is a property only $geo_check carries,
		// so drilling on one would filter the whole page down to check events and empty
		// every other card. A drill that destroys the page is worse than no drill.
		av.Comps = append(av.Comps, row)
	}

	for _, e := range av.Engines {
		if e.LatestVerbatim == "" {
			continue
		}
		v := geoVerbatim{Engine: e.Engine, Text: e.LatestVerbatim, Model: e.LatestModel}
		if v.Model == "" {
			v.Model = "model id not sent"
		}
		if t, err := time.Parse(time.RFC3339, e.LatestAt); err == nil {
			v.When = t.UTC().Format("Jan 2, 15:04 MST")
			// a custom range ending in the past makes "now minus then" negative; clamp
			// rather than print "-14d ago" on a perfectly valid time-travelled view
			d := end.Sub(t)
			if d < 0 {
				d = 0
			}
			switch {
			case d < time.Hour:
				v.Ago = fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 48*time.Hour:
				v.Ago = fmt.Sprintf("%dh ago", int(d.Hours()))
			default:
				v.Ago = fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		}
		av.Verbatims = append(av.Verbatims, v)
	}
	vm.AIVis = av
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	renderStart := time.Now()
	if r.URL.Path != "/" { // GET / is a catch-all; anything else is a real 404
		s.notFound(w, r)
		return
	}
	// Production scope hides env=development. The browser SDK stamps every localhost
	// load as development (sdk.js), so a developer testing locally sends events that
	// are ingested but invisible here — "I sent events and the dashboard shows nothing"
	// is an unexplained trust-killer. Count what's hidden so the UI can SAY so, and
	// support ?env=development as an opt-in view of exactly that traffic.
	// Any non-production env can be viewed with ?env=<value>, not just development: a Netlify
	// deploy preview or a staging subdomain is stamped "preview", and traffic you can neither
	// see by default nor ask for is traffic you have quietly lost.
	wantEnv := r.URL.Query().Get("env")
	showDev := wantEnv != "" && query.NonProduction[wantEnv]
	var scopeFilters []query.Filter
	if showDev {
		scopeFilters = []query.Filter{{Property: "env", Op: query.Eq, Value: wantEnv}}
	}
	// ONE pass over storage instead of loading all history and then filtering a second
	// full copy out of it. Scan streams and never materializes, so the only slice that
	// exists is the one the reports actually read. Everything else the page needed from
	// the raw set — the total, the hidden-env count, the newest timestamp — is a counter
	// computed on the way past, not a reason to keep 500k events alive.
	keep := query.Keeper(scopeFilters)
	var evs []event.Event
	totalAll, devHidden := 0, 0
	var newestTS time.Time
	if err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		totalAll++
		if v, _ := e.Properties["env"].(string); query.NonProduction[v] {
			devHidden++
		}
		// Scan yields in storage order, which is ascending by timestamp, so the last one
		// past is the newest. Compared rather than assumed: a backfilled import lands out
		// of order, and "last event 3 days ago" on a live site reads as an outage.
		if e.Timestamp.After(newestTS) {
			newestTS = e.Timestamp
		}
		if keep(e) {
			evs = append(evs, e)
		}
		return nil
	}); err != nil {
		serverError(w, "dashboard store.Scan", err)
		return
	}

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
	site := r.URL.Query().Get("site")
	if site != "" {
		evs = query.Apply(evs, []query.Filter{{Property: "site", Op: query.Eq, Value: site}})
	}

	// range control: ?days=7|30|90 presets, or ?from=YYYY-MM-DD&to=YYYY-MM-DD for
	// arbitrary time travel — every windowed report below recomputes over the window,
	// and it lives in the querystring so any past view is a shareable URL

	rangeDays := 30
	switch r.URL.Query().Get("days") {
	case "1":
		rangeDays = 1
	case "7":
		rangeDays = 7
	case "90":
		rangeDays = 90
	}
	// Sub-day windows. ?hours=6|12 narrows the CHART window without disturbing rangeDays,
	// which every day-based helper below still needs to be at least 1 — web.Compute,
	// engagementSeries, cumulativeUserSeries and buildKPIs all take a day count, and handing
	// them 0 would produce empty tiles rather than a short window.
	rangeHours := 0
	switch r.URL.Query().Get("hours") {
	case "6":
		rangeHours, rangeDays = 6, 1
	case "12":
		rangeHours, rangeDays = 12, 1
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

	// Young-account default: when the range wasn't explicitly chosen (?days / custom
	// from-to), a 30-day default renders mostly-empty bars for an account only a few days
	// old — reads as "flat/dead" rather than "new". Shrink the DEFAULT to fit the data's
	// real span (floor 7d) so the first view is full. This changes only the default
	// selection; "30d" still means 30 whole days everywhere it's explicitly chosen, so the
	// cross-surface window covenant is untouched.
	if !customRange && r.URL.Query().Get("days") == "" {
		var oldest time.Time
		for _, e := range evs {
			if oldest.IsZero() || e.Timestamp.Before(oldest) {
				oldest = e.Timestamp
			}
		}
		if !oldest.IsZero() {
			span := int(time.Now().UTC().Sub(oldest).Hours()/24) + 1
			if span < 7 {
				span = 7
			}
			if span < rangeDays {
				rangeDays = span
			}
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
				// Acquisition/user attributes (referrer, source, device, country, utm…) scope
				// by USER — keep every event of a matching user, including conversion events
				// that don't carry the attribute. Filtering EVENTS by "referrer=reddit" would
				// drop the signup events (referrer lives only on the landing pageview) and blank
				// the whole dashboard. Other props filter events directly. anyMode mixing both
				// classes can't be split without breaking the OR, so fall back to event-level.
				var userF, evF []query.Filter
				for _, f := range filters {
					if userAttr(f.Property) {
						userF = append(userF, f)
					} else {
						evF = append(evF, f)
					}
				}
				if len(userF) > 0 && !(anyMode && len(evF) > 0) {
					evs = query.ScopeUsers(evs, userF, anyMode)
					if len(evF) > 0 {
						evs = query.ApplyMode(evs, evF, false)
					}
				} else {
					evs = query.ApplyMode(evs, filters, anyMode)
				}
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

	// The verdict is computed here, at the BOTTOM of the scoping section, because `evs` is
	// only final now — after ?site=, after ?env=, and after the ?f= chips.
	//
	// It used to run a hundred lines above this, which handled ?site= (a multi-site instance
	// opened site B under a diagnosis of site A) and stopped there. So the chips scoped every
	// pane on the page and left the headline alone: filtering to ?f=plan:pro moved the funnel
	// pane to 90% conversion while the card above it still read "Free-plan users convert worst
	// — only 10% continue", telling you to go fix the segment you had just filtered out, and
	// contradicting the pane directly beneath it.
	//
	// It stays window-independent on purpose: the verdict reports NOW, the range control scopes
	// the reports below. Server-rendered only — real text on first paint, one source of markup.
	// Detect the funnel BEFORE the verdict so both describe the same one: the verdict used to
	// run insight's own journey detection while the funnel pane ran detectFunnel, and the page
	// could contradict itself about which funnel it was even talking about.
	verdictSteps, _ := detectFunnel(evs, eventsByVolume(evs))
	verdict := insight.GenerateForFunnel(evs, verdictSteps)

	// the range switcher links, preserving site + filters
	// every preset link must clear BOTH range params, or clicking 7d after 6h leaves
	// ?hours=6 in the URL and the chart silently stays on the six-hour window.
	mkHours := func(h int) rangeVM {
		nq := url.Values{}
		for k, vals := range r.URL.Query() {
			if k != "days" && k != "hours" && k != "gran" {
				nq[k] = vals
			}
		}
		nq.Set("hours", fmt.Sprint(h))
		u := "/"
		if enc := nq.Encode(); enc != "" {
			u += "?" + enc
		}
		return rangeVM{Label: fmt.Sprintf("%dh", h), URL: u, On: rangeHours == h}
	}
	mkRange := func(d int) rangeVM {
		nq := url.Values{}
		for k, vals := range r.URL.Query() {
			if k != "days" && k != "hours" && k != "gran" {
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
		return rangeVM{Label: rangeLabel(d), URL: u, On: rangeHours == 0 && d == rangeDays}
	}

	names, _ := s.store.Names()
	vol := eventsByVolume(evs)
	fsteps, ftitle := detectFunnel(evs, vol)
	trendEvent := pickEvent(vol, "signup")
	retEvent := "" // any activity — the same anchor /v1/retention, MCP, and the ask bar use
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

	// every finding's brief, computed over the funnel THIS PAGE is showing and on the page's
	// own clock — so the sheet, GET /v1/fix-brief and the fix_brief MCP tool cannot tell three
	// different stories about one finding. Server-rendered because the sheet must open at click
	// speed; the evidence being already computed is the whole point of the action.
	// Keyed map for O(1) lookup, PLUS the fingerprints in the order ComputeAll returned them
	// (severity-sorted, element 0 = today's lead finding). A Go map marshals to a JSON object
	// with no order, so the sheet's old "just open the first one" fallback opened whichever
	// fingerprint sorted lexicographically smallest — an arbitrary finding presented as the one
	// the reader asked for, which is precisely what internal/fixbrief's own invariant forbids
	// ("Never substitute a different finding for the one that was asked for").
	fixBriefs := map[string]fixbrief.Brief{}
	fixOrder := []string{}
	for _, fb := range fixbrief.ComputeAll(evs, verdictSteps, nowT) {
		fixBriefs[fb.Fingerprint] = fb
		fixOrder = append(fixOrder, fb.Fingerprint)
	}
	endT := nowT
	if !rangeAsof.IsZero() {
		endT = rangeAsof
	}
	// the chart's metric + grain are user-selectable and live in the URL like all
	// analysis state (?metric=checkout&gran=week)
	chartMetric := r.URL.Query().Get("metric")
	granParam := r.URL.Query().Get("gran")
	gran, granErr := trends.ParseInterval(granParam)
	if granErr != nil {
		gran = trends.Day
	}
	// A one-day window drawn in day buckets is a single bar, which tells you nothing. The
	// engine has always bucketed by hour; nothing ever asked it to. Note this cannot live
	// in the granErr branch: ParseInterval("") returns (Day, nil), so an absent ?gran is
	// not an error and that branch never runs.
	if granParam == "" && (rangeDays == 1 || rangeHours > 0) {
		gran = trends.Hour
	}
	// One Options value for BOTH the funnel pane and the "conversion by X" pane below it, so the
	// two cannot drift into rendering different funnel definitions on the same page.
	fopts := funnel.Options{Order: forder}
	// ?grain=company switches the funnel and retention panes to ACCOUNT grain — the B2B
	// question, where an account converts if anyone in it converts.
	//
	// Scoped to a LOCAL variable on purpose. Re-keying `evs` itself would silently turn every
	// other number on this page — the headline, the chart, the verdict, the segment table —
	// into an account number under a label that still said users, which is a worse bug than not
	// having the feature. Only the two panes that say "accounts" are computed from grainEvs.
	grainProp, grainMiss := r.URL.Query().Get("grain"), ""
	grainEvs := evs
	var grainInfo *groups.Grain
	if grainProp != "" {
		if re, g := groups.Regroup(evs, grainProp); g.Kept > 0 {
			grainEvs, grainInfo = re, &g
		} else {
			// Nothing carries it. Fall back to user grain rather than render two empty panes,
			// and SAY so — a silent fallback would show user numbers under an accounts label.
			grainMiss = grainProp
			grainProp = ""
		}
	}
	grainOptions := groupProps(evs)
	grainOff := *r.URL
	gq := grainOff.Query()
	gq.Del("grain")
	grainOff.RawQuery = gq.Encode()
	grainOffHref := grainOff.RequestURI()
	fr := funnel.ComputeOpts(grainEvs, fsteps, 7*24*time.Hour, fopts)
	rr := retention.ComputeBucketed(grainEvs, rdays, retEvent, rbucket, rroll)
	// the chart and the headline stat both follow the selected range, and the stat
	// carries a delta vs the prior equal window so movement is visible at a glance
	if chartMetric != "" {
		trendEvent = chartMetric
	}
	// Calendar-day-aligned window, IDENTICAL to /v1/trends (parseTrendWindow) and the MCP
	// trends tool, so the "· Nd" tile/chart, the API, and MCP report the SAME number for
	// "last N days". The old rolling now−N·24h start pulled in a clipped partial leading
	// day, rendering a phantom bar and a headline that disagreed with /v1 and MCP.
	curFrom, curTo := endT.AddDate(0, 0, -rangeDays), rangeAsof
	if !customRange && rangeHours == 0 {
		// The comment above was AN INTENTION, not the code: endT is nowT untruncated, so
		// endT.AddDate(0,0,-N) is a rolling now−N·24h — the very thing it says it is not. The
		// dashboard therefore windowed differently from /v1/trends and /v1/rows, which do
		// truncate. web.CalendarDays is the one definition now; a preset defers to it instead
		// of restating it, which is how the two drifted apart in the first place.
		//
		// A custom ?from=&to= range is left alone: its bounds are the dates the user typed, and
		// re-aligning them to "today" would answer a different question than the one asked.
		curFrom, curTo = web.CalendarDays(rangeDays, endT)
		if !rangeAsof.IsZero() {
			curTo = rangeAsof
		}
	}
	if rangeHours > 0 {
		curFrom = endT.Add(-time.Duration(rangeHours) * time.Hour)
	}
	if curTo.IsZero() || curTo.After(nowT) {
		curTo = nowT // presets (and any future-ending custom range) end NOW, never the future —
		// matches parseTrendWindow + MCP so a clock-skewed future event can't make the
		// headline stat disagree with /v1 and MCP
	}
	// The last INSTANT inside the window, which is the last DAY every sparkline must end on.
	// curTo is an exclusive bound, so handing it straight to the series helpers pushed a custom
	// range one day past itself: ?from=2026-01-01&to=2026-01-07 would draw Jan 2..Jan 8 with an
	// empty final bucket under a figure computed over Jan 1..Jan 7.
	sparkEnd := curTo.Add(-time.Nanosecond)
	priorFrom, priorTo := endT.AddDate(0, 0, -2*rangeDays), endT.AddDate(0, 0, -rangeDays)
	if rangeHours > 0 {
		d := time.Duration(rangeHours) * time.Hour
		priorFrom, priorTo = endT.Add(-2*d), endT.Add(-d)
	}
	if rangeAsof.IsZero() && gran == trends.Day && rangeHours == 0 { // preset day-range → align to whole days
		day0 := nowT.Truncate(24 * time.Hour)
		curFrom = day0.AddDate(0, 0, -(rangeDays - 1))
		priorFrom = day0.AddDate(0, 0, -(2*rangeDays - 1))
		priorTo = day0.AddDate(0, 0, -(rangeDays - 1))
	}
	tr := trends.ComputeInterval(evs, trendEvent, curFrom, curTo, false, gran)
	trPrior := trends.ComputeInterval(evs, trendEvent, priorFrom, priorTo, false, gran)
	sig30 := tr.Total
	sigPrior := trPrior.Total

	convLabel := ftitle
	if n := len(fsteps); n >= 2 {
		convLabel = EventLabel(fsteps[0].Event) + " → " + EventLabel(fsteps[n-1].Event)
	}

	// the agent-observability section's two inputs. hasAgentEvents comes from the event
	// NAMES (not the window) so an instance that instrumented its agent keeps the section
	// up top even on a quiet day; the panel itself still reports honestly per window.
	hasAgentEvents := false
	for _, n := range names {
		if n == agent.EventToolCall || n == agent.EventTurn {
			hasAgentEvents = true
			break
		}
	}
	// this page's scope, verbatim, as a /v1 querystring. The window is curFrom/curTo — the
	// SAME bounds the chart above uses — so a client-fetched panel can never answer over a
	// different range than the toolbar advertises. Site and env are property filters here
	// because that is the only scoping grammar /v1 has.
	reportQ := url.Values{}
	reportQ.Set("from", curFrom.UTC().Format(time.RFC3339))
	reportQ.Set("to", curTo.UTC().Format(time.RFC3339))
	for _, c := range chips {
		reportQ.Add("f", c.Raw)
	}
	if site != "" {
		reportQ.Add("f", "site:eq:"+site)
	}
	if showDev {
		reportQ.Add("f", "env:eq:development")
	}
	if anyMode {
		reportQ.Set("fm", "any")
	}

	// the "Cloud ↗" destination. Self-hosted instances (no SMOLANALYTICS_CLOUD_URL) keep
	// linking to smolanalytics.com; the hosted product points it at the project this
	// instance belongs to, so the dashboard is never a one-way door out of the account.
	cloudURL := "https://smolanalytics.com"
	if s.cloudURL != "" {
		cloudURL = s.cloudURL
	}

	vm := dashVM{
		HasConversion:   len(fsteps) >= 2,
		TotalUsers:      distinctUsers(evs),
		Signups:         sig30,
		OverallConv:     pct(fr.OverallConversion),
		Events:          names,
		ProductEvents:   productEvents(names, 8),
		Updated:         time.Now().UTC().Format("Jan 2, 15:04 MST"),
		HasData:         len(evs) > 0,
		Verdict:         verdict,
		Findings:        verdictLines(verdict),
		DevHidden:       devHidden,
		ShowingDev:      showDev,
		Sites:           sites,
		Site:            site,
		Base:            baseURL(r),
		WriteKey:        s.writeKey,
		CloudURL:        cloudURL,
		FunnelTitle:     ftitle,
		ConvLabel:       convLabel,
		StatEventLabel:  trendEvent,
		TrendLabel:      trendEvent,
		ConvByTitle:     segProp,
		HasConvBy:       segProp != "",
		HasSource:       srcProp != "",
		SourceTitle:     trendEvent + " by " + srcProp,
		HasShares:       s.shares != nil,
		HasGoalsStore:   s.goals != nil,
		HasAgentEvents:  hasAgentEvents,
		ReportQS:        reportQ.Encode(),
		RangeDays:       rangeDays,
		RangeLabel:      rangeWindowLabel(rangeDays, rangeHours),
		GhostTotal:      trPrior.Total,
		FunnelOrder:     string(forder),
		FunnelIsGuessed: funnelIsSynthetic(fsteps),
		GrainProp:       grainProp,
		GrainOptions:    grainOptions,
		GrainMiss:       grainMiss,
		GrainOffHref:    grainOffHref,
		RetDays:         rdays,
		RetBucket:       map[bool]string{true: rbucket, false: "day"}[rbucket != ""],
		RetRolling:      rroll,
		CustomRange:     customRange,
		AnyMode:         anyMode,
		RangeFrom:       r.URL.Query().Get("from"),
		RangeTo:         r.URL.Query().Get("to"),
		Ranges:          []rangeVM{mkHours(6), mkHours(12), mkRange(1), mkRange(7), mkRange(30), mkRange(90)},
		Chips:           chips,
		SourceProp:      srcProp,
		ConvByProp:      segProp,
		SignupsDelta:    deltaStr(sig30, sigPrior),
		LastEventSecs:   -1,
	}
	if grainInfo != nil {
		vm.GrainGroups, vm.GrainNote = grainInfo.Groups, grainInfo.Note
	}
	// Only worth computing when nobody has defined a funnel: it is a scan, and an instance with a
	// real funnel has no use for a guess at one.
	if vm.FunnelIsGuessed {
		vm.Proposal = ProposeFunnel(evs)
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
	if totalAll > 0 {
		// newest timestamp seen during the scan — this powers the header's "last event
		// Ns ago" liveness stamp
		vm.LastEventSecs = int(nowT.Sub(newestTS).Seconds())
		if vm.LastEventSecs < 0 {
			vm.LastEventSecs = 0
		}
	}

	// connect-your-agent artifacts: cursor's deeplink takes base64 of the single-server
	// config object (NOT the mcpServers map); vs code takes the urlencoded JSON. Both are
	// complete as rendered — key included — so connecting is one click, never an edit.
	// MCP auth uses the SECRET READ key: since the two-key split, /mcp rejects the public
	// write key (it's ingest-only), so baking the write key here made every copy-paste
	// config 401 on first use. The dashboard is password-gated — the operator seeing
	// their own read key here is exactly the intended distribution channel for it.
	mcpKey := s.readKey
	mcpURL := vm.Base + "/mcp"
	cfg := fmt.Sprintf(`{"url":%q`, mcpURL)
	vsCfg := fmt.Sprintf(`{"name":"smolanalytics","type":"http","url":%q`, mcpURL)
	cmd := "claude mcp add --transport http --scope user smolanalytics " + mcpURL
	if mcpKey != "" {
		hdr := fmt.Sprintf(`,"headers":{"Authorization":"Bearer %s"}`, mcpKey)
		cfg += hdr
		vsCfg += hdr
		cmd += fmt.Sprintf(` --header "Authorization: Bearer %s"`, mcpKey)
	}
	cfg += "}"
	vsCfg += "}"
	vm.MCPURL = mcpURL
	vm.MCPConfig = cfg
	// Claude Desktop's connector UI is OAuth-only (a bare URL field — a static key
	// cannot work there), so Desktop gets the config-file bridge via mcp-remote,
	// complete and paste-ready. Other mcp.json clients get the full wrapper.
	// Desktop bridges over mcp-remote. Windows Claude Desktop mangles spaces inside
	// args when invoking npx, so the header value (which needs "Bearer <key>") lives
	// in env and the arg carries no spaces — the documented mcp-remote workaround,
	// and it works identically on macOS/Linux.
	hdrArg, hdrEnv := "", ""
	if mcpKey != "" {
		hdrArg = `, "--header", "Authorization:${SMOL_AUTH}"`
		hdrEnv = fmt.Sprintf(`, "env": { "SMOL_AUTH": "Bearer %s" }`, mcpKey)
	}
	vm.DesktopConfig = fmt.Sprintf(`{ "mcpServers": { "smolanalytics": { "command": "npx", "args": ["-y", "mcp-remote", %q%s]%s } } }`, mcpURL, hdrArg, hdrEnv)
	vm.MCPServersJSON = fmt.Sprintf(`{ "mcpServers": { "smolanalytics": %s } }`, cfg)
	vm.ClaudeCodeCmd = cmd
	// each client's config dialect, exactly: Windsurf wants serverUrl (url fails
	// silently), Zed wants context_servers, Cline wants an explicit streamableHttp type.
	hdrJSON := ""
	if mcpKey != "" {
		hdrJSON = fmt.Sprintf(`, "headers": { "Authorization": "Bearer %s" }`, mcpKey)
	}
	vm.WindsurfConfig = fmt.Sprintf(`{ "mcpServers": { "smolanalytics": { "serverUrl": %q%s } } }`, mcpURL, hdrJSON)
	vm.ZedConfig = fmt.Sprintf(`{ "context_servers": { "smolanalytics": { "url": %q%s } } }`, mcpURL, hdrJSON)
	vm.ClineConfig = fmt.Sprintf(`{ "mcpServers": { "smolanalytics": { "type": "streamableHttp", "url": %q%s } } }`, mcpURL, hdrJSON)
	// the universal paste: one block that tells WHATEVER agent is reading it how to
	// wire itself up — the user copies once, pastes into any AI, done. Every path is
	// complete (url + key baked in); the agent picks its own.
	authLine := ""
	if mcpKey != "" {
		authLine = "\nAuth header: Authorization: Bearer " + mcpKey
	}
	vm.ConnectPrompt = "Connect the smolanalytics MCP server to yourself, then verify it.\n\n" +
		"Server: " + mcpURL + " (Streamable HTTP)" + authLine + "\n\n" +
		"Pick the path for the client you are:\n" +
		"- Claude Code: run " + cmd + "\n" +
		"- Cursor: merge into ~/.cursor/mcp.json: {\"mcpServers\": {\"smolanalytics\": " + cfg + "}}\n" +
		"- VS Code: merge the same object into .vscode/mcp.json under \"servers\" with \"type\": \"http\"\n" +
		"- Windsurf: merge into ~/.codeium/windsurf/mcp_config.json (note serverUrl, not url): " + vm.WindsurfConfig + "\n" +
		"- Cline: merge into cline_mcp_settings.json: " + vm.ClineConfig + "\n" +
		"- Zed: merge into ~/.config/zed/settings.json: " + vm.ZedConfig + "\n" +
		"- Claude Desktop (config file only, the connector UI is OAuth-only): open Settings > Developer > Edit Config and merge: " + vm.DesktopConfig + " then restart Claude Desktop\n" +
		"- anything else that reads an mcp.json: {\"mcpServers\": {\"smolanalytics\": " + cfg + "}}\n\n" +
		"After connecting, call the \"overview\" tool and report the visitor count so I know it works."
	vm.CursorLink = template.URL("cursor://anysphere.cursor-deeplink/mcp/install?name=smolanalytics&config=" + base64.StdEncoding.EncodeToString([]byte(cfg)))
	// A marshal failure must not take the page down with it: an empty island degrades the sheet
	// to "no brief for that finding", which the renderer already handles honestly.
	if bj, err := json.Marshal(fixBriefs); err == nil {
		vm.FixBriefs = template.JS(bj)
	} else {
		vm.FixBriefs = template.JS("{}")
	}
	if oj, err := json.Marshal(fixOrder); err == nil {
		vm.FixOrder = template.JS(oj)
	} else {
		vm.FixOrder = template.JS("[]")
	}
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

	vm.RetDayHeaders, vm.Retention, vm.RetentionReady = retentionGrid(rr, nowT)

	maxT, peakIdx := 1, -1
	for i, p := range tr.Points {
		if p.Count > maxT {
			maxT = p.Count
			peakIdx = i
		}
	}
	// the ghost: the prior equal window aligned position-by-position onto the same
	// x-axis and the SAME y-scale, so "vs what?" is answered by the chart itself
	priorHasData := false
	for _, p := range trPrior.Points {
		if p.Count > 0 {
			priorHasData = true
		}
		if p.Count > maxT {
			maxT = p.Count
		}
	}
	// tick cadence scales with the window so labels never crowd (~6 ticks)
	tickEvery := len(tr.Points) / 6
	if tickEvery < 1 {
		tickEvery = 1
	}
	// Bucket labels have to follow the grain. An hourly chart labelled "Jan 2" repeats the
	// same string 24 times and hides the thing the view exists to show, which is the shape
	// of the day.
	shortFmt, longFmt := "1/2", "Jan 2"
	if gran == trends.Hour {
		shortFmt, longFmt = "3pm", "Mon 3pm"
	}
	for i, p := range tr.Points {
		b := trendBar{
			Date:      p.Date.Format(shortFmt),
			ISO:       p.Date.Format("2006-01-02"),
			Count:     p.Count,
			HeightPct: int(math.Round(float64(p.Count) / float64(maxT) * 100)),
			Tip:       fmt.Sprintf("%s · %d", p.Date.Format(longFmt), p.Count),
			Peak:      i == peakIdx,
		}
		if i%tickEvery == 0 {
			b.Tick = p.Date.Format(shortFmt)
		}
		// P1-2: only compare against the prior window when it actually has data. A
		// brand-new account's prior window is all zeros, so "(prior window: 0)" on
		// every bar is noise — suppress the ghost + the clause until there's history.
		if priorHasData && i < len(trPrior.Points) {
			pp := trPrior.Points[i]
			b.GhostPct = int(math.Round(float64(pp.Count) / float64(maxT) * 100))
			b.Tip = fmt.Sprintf("%s · %d (prior window %s: %d)", p.Date.Format(longFmt), p.Count, pp.Date.Format(longFmt), pp.Count)
		}
		// the newest bucket is only complete if the window ended in the past
		if rangeAsof.IsZero() && i == len(tr.Points)-1 {
			b.Partial = true
			unit := "today so far"
			if gran == trends.Hour {
				unit = "this hour so far"
			}
			b.Tip += " · " + unit
		}
		vm.Trend = append(vm.Trend, b)
	}
	vm.TrendMax = maxT
	vm.TrendMid = (maxT + 1) / 2
	vm.ChartMetric = trendEvent
	vm.Gran = string(gran)
	if tr.Truncated && len(tr.Points) > 0 {
		span := "buckets"
		switch gran {
		case trends.Hour:
			span = fmt.Sprintf("%d hourly buckets", len(tr.Points))
		case trends.Week:
			span = fmt.Sprintf("%d weekly buckets", len(tr.Points))
		case trends.Month:
			span = fmt.Sprintf("%d monthly buckets", len(tr.Points))
		default:
			span = fmt.Sprintf("%d daily buckets", len(tr.Points))
		}
		vm.ChartTrunc = fmt.Sprintf("chart capped: showing the most recent %s (from %s). the total above still counts the whole window — the cap trims the picture, never the number.",
			span, tr.Points[0].Date.Format("Jan 2 2006"))
	}
	// the data-table half of the chart+table unit: newest first, and EVERY bucket the
	// chart draws. It used to stop at 15 rows, which was fine while it was a convenience
	// list beside the picture. It is now the chart's text equivalent — the only reading
	// available to a screen reader, a keyboard, or a phone, because the per-bar numbers
	// live in a .tip that is display:none until hover. A 30-bar chart with a 15-row table
	// hides half the window from exactly the people who have nothing else to read.
	// Length is handled where it belongs, by the scroll box on .charttable .tscroll.
	{
		n := len(tr.Points)
		start := 0
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
		// The prior column has to tell "you had no traffic" apart from "we have no data".
		// It could not: a bucket from before this instance recorded its FIRST event printed a
		// hard 0, so a ten-day-old install read as a site with a fortnight of zero traffic
		// behind it, in a column of zeros, under a change column that was blank because
		// deltaStr correctly refuses to divide by a prior of nothing. Prior = -1 already means
		// "unknown" to the template; a bucket that ends before the first event we ever saw is
		// exactly that, and now says so.
		firstEv := firstEventTime(evs)
		for i := n - 1; i >= start; i-- {
			p := tr.Points[i]
			row := chartRow{Label: lbl(p.Date), Count: p.Count, Prior: -1}
			// same condition the bar uses at the top of this function — one rule, two renderings
			row.Partial = rangeAsof.IsZero() && i == len(tr.Points)-1
			if maxT > 0 {
				row.Bar = int(math.Round(float64(p.Count) / float64(maxT) * 100))
			}
			if i < len(trPrior.Points) && !bucketPredatesData(trPrior.Points, i, gran, firstEv) {
				row.Prior = trPrior.Points[i].Count
				row.Delta = deltaStr(p.Count, trPrior.Points[i].Count)
			}
			vm.ChartTable = append(vm.ChartTable, row)
		}
	}

	// Segmentation: the headline event by FIRST-TOUCH CHANNEL, not by a raw "source"
	// property on the conversion event. Conversion events usually carry a uniform
	// source ("web") or none, so a naive breakdown collapsed to "web 100%" (or, when
	// source was absent, silently fell back to whatever property was most common —
	// country/path). channelWithFilter attributes each converter by the channel of
	// their earliest event (source → referrer host → utm → direct), the same first-touch
	// model the ask bar and funnel breakdown use.
	{
		firstOf := map[string]event.Event{}
		for _, e := range evs {
			if cur, ok := firstOf[e.DistinctID]; !ok || e.Timestamp.Before(cur.Timestamp) {
				firstOf[e.DistinctID] = e
			}
		}
		// Converters are the ones who fired it INSIDE the page's window; firstOf stays over all
		// history because a converter's first touch is a fact about them, not about the window,
		// and re-deriving it from the window would credit the channel of whatever they happened
		// to do first inside it.
		//
		// This set used to be built over the whole unwindowed `evs`, so on an instance with a
		// year of data the tile read "signup · 7d — 12" and the pane directly under it read
		// "signup by channel: direct 1,840 · 61%", and moving the range control from 7d to 90d
		// changed nothing in the pane — under a provenance line that names the window.
		// Half-open [curFrom, curTo), the same bounds trends and web.ComputeRange use.
		converters := map[string]bool{}
		for _, e := range evs {
			if e.Name == trendEvent && !e.Timestamp.Before(curFrom) && e.Timestamp.Before(curTo) {
				converters[e.DistinctID] = true
			}
		}
		type chAgg struct {
			count       int
			fprop, fval string
		}
		tally := map[string]*chAgg{}
		total := 0
		for uid := range converters {
			channel, fp, fv := channelWithFilter(firstOf[uid])
			if tally[channel] == nil {
				tally[channel] = &chAgg{fprop: fp, fval: fv}
			}
			tally[channel].count++
			total++
		}
		rows := make([]segRow, 0, len(tally))
		top := 0
		for ch, a := range tally {
			if a.count > top {
				top = a.count
			}
			rows = append(rows, segRow{Value: ch, Count: a.count, FilterProp: a.fprop, FilterVal: a.fval})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Value < rows[j].Value
		})
		for i := range rows {
			if total > 0 {
				rows[i].Pct = int(math.Round(float64(rows[i].Count) / float64(total) * 100))
			}
			if top > 0 {
				rows[i].BarPct = int(math.Round(float64(rows[i].Count) / float64(top) * 100))
			}
		}
		vm.BySource = rows
		vm.HasSource = len(rows) > 0
		// EventLabel, or an autocapture name leaks into a heading: the rail read "$pageview by
		// channel". Everywhere else on this page $pageview is called "page view", so the one place
		// a reader meets the raw name is a nav label, with no context to decode it.
		vm.SourceTitle = EventLabel(trendEvent) + " by channel"
	}

	if segProp != "" {
		vm.ConvBySeg, vm.ConvByThin = funnelBySegment(evs, segProp, fsteps, fopts)
		if segProp == "country" {
			decorateCountrySegs(vm.ConvBySeg)
		}
	}
	// Show the card only when it has renderable rows — a property that exists but sits
	// off the funnel-entry step (so every segment is "(none)", filtered out) otherwise
	// renders as an empty box. Gate on content, not mere property existence.
	vm.HasConvBy = len(vm.ConvBySeg) > 0

	// The depth deck: paths / lifecycle / stickiness — the analyses that make this read
	// as product analytics, not web analytics. Same compute the MCP tools use.
	buildDepthCards(&vm, evs, fsteps, trendEvent, nowT)

	// AI visibility over the SAME window/filters as everything above. `scoped` is what
	// separates "nothing sampled yet" from "your filters exclude the checks" — $geo_check
	// carries no site or path, so any web-property chip hides every one of them.
	buildAIVis(&vm, evs, names, rangeDays, rangeAsof,
		len(chips) > 0 || site != "" || showDev, mkRange(90).URL, s.cloudURL)

	// which ship moved it. Correlation, gated hard on run counts inside ByDeploy — the
	// window is the SAME one the card above is reporting, so a reader can never be looking
	// at a 7-day comparison under a 90-day headline.
	if s.deploys != nil {
		vm.AIVis.Ships = aivis.ByDeploy(evs, s.deploys.List(), 7, rangeAsof, aicrawl.CrawlEvent)
	}

	// and the retrieval half, over the same window. Ordered next to it on the page: "was
	// it fetched" and "what did the model then say" are one story read in that direction.
	buildAICrawl(&vm, evs, names, rangeDays, rangeAsof,
		len(chips) > 0 || site != "" || showDev, mkRange(90).URL, s.cloudURL)

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
	// ONE window for the tiles and the chart. A 6h preset that only moved the chart left
	// every KPI reading 24h, which makes the range control look broken.
	//
	// And ONE window across the tiles themselves: curFrom/curTo and priorFrom/priorTo are the
	// exact bounds the trend tile, /v1/trends and the MCP trends tool use, calendar-aligned for
	// day presets. This block used to derive its own ROLLING bounds from a duration, so the
	// "Visitors · 10d" tile and the "$pageview · 10d" tile beside it measured windows offset by
	// up to a day at each edge. On a ten-day-old instance that was the difference between a
	// prior window containing data and one containing none: one tile read "71x vs prior" and
	// its neighbour showed no delta at all, under the same label, on the same row.
	wv := web.ComputeRange(evs, curFrom, curTo)
	// "never installed" and "nothing in THIS window" are different facts with different fixes,
	// and this block used to answer both with wv.Pageviews > 0 — a WINDOW fact used as a
	// LIFETIME one. Pick the 6h preset at night on a site with months of pageviews and the page
	// claimed "this instance has events but no $pageview yet … drop <script src=…/sdk.js> into
	// your app", telling the operator to reinstall an SDK that is working, and buildKPIs silently
	// dropped the Visitors tile so the KPI row lost a card. Ever comes from the store's event
	// NAMES, exactly as aivisVM.Ever / aicrawlVM.Ever already do; the panes below keep their own
	// windowed empty states, and WebWindowEmpty tells the copy which sentence is true.
	everWeb := hasName(names, "$pageview")
	if wv.Pageviews > 0 || everWeb {
		vm.WebWindowEmpty = wv.Pageviews == 0
		wvPrior := web.ComputeRange(evs, priorFrom, priorTo)
		vm.VisitorsDelta = deltaStr(wv.Visitors, wvPrior.Visitors)
		vm.PageviewsDelta = deltaStr(wv.Pageviews, wvPrior.Pageviews)
		vm.HasWeb = true
		vm.LiveNow = wv.LiveNow
		vm.Visitors = wv.Visitors
		vm.Pageviews = wv.Pageviews
		vm.HasEngagement = wv.HasEngagement
		vm.EngagedSecs = wv.AvgEngagedSecs
		vm.EngagedHuman = humanDur(wv.AvgEngagedSecs)
		vm.BouncePct = wv.BounceRatePct
		// sparkEnd, not nowT: the sparkline is the picture OF the number above it, so it has to
		// end where that number's window ends. Anchored on "today" it drew the last N days
		// ending now regardless — so ?from=2026-01-01&to=2026-01-31 rendered a January bounce
		// rate with an August sparkline under it, a chart contradicting the figure it sits below.
		if bs, es := engagementSeries(evs, rangeDays, sparkEnd); len(bs) > 1 {
			// endOf() is a closure scoped to the KPI block above, and its "up = good" rule is
			// backwards for bounce anyway: a rising bounce rate is bad. So classify here.
			endClass := func(delta int, upIsGood bool) string {
				if delta == 0 {
					return ""
				}
				if (delta > 0) == upIsGood {
					return "good"
				}
				return "warn"
			}
			vm.SparkBounce = buildSpark(bs, endClass(bs[len(bs)-1]-bs[0], false))
			vm.SparkEngaged = buildSpark(es, endClass(es[len(es)-1]-es[0], true))
		}
		vm.AIVisitors = wv.AIVisitors
		// share-of-total is computed against the RIGHT denominator per dimension. Pages are
		// per-pageview → divide by pageviews. Referrer/UTM/device/browser/os/country are
		// per-VISITOR first-touch attributes AND partial (not every visitor carries one) —
		// dividing those by pageviews understates them and the percentages never sum to 100
		// (device "mobile 37% / desktop 31%" with a third of the bar silently missing). Each
		// partial per-visitor dimension is divided by its OWN recorded total instead.
		//
		// That total is web.Result.Recorded — the count BEFORE rank() truncates — never the sum
		// of the rows we were handed. Summing the rows was the bug: rank() keeps only the top N,
		// so on a site with 40 countries (20 US of 100 visitors, the tail 2 each) the ten visible
		// rows summed to 38 and the card rendered "US 20 · 52%" for a true 20% share, with the
		// visible rows adding to exactly 100% — asserting there is no tail at all. The longer the
		// tail, the bigger the overstatement.
		toRows := func(rows []web.Row, n int, denom int) []segRow {
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
				if denom > 0 {
					sr.Pct = int(math.Round(float64(r.Count) / float64(denom) * 100))
				}
				out = append(out, sr)
			}
			return out
		}
		vm.TopPages = toRows(wv.TopPages, 6, wv.Pageviews)
		vm.Referrers = toRows(wv.Referrers, 6, wv.Recorded.Referrers)
		// the traffic half of the AI channel, for the AI-visibility card. Counted per
		// VISITOR at first touch (web.go bumps aiRefs from firstPV), so the denominator is
		// the AI visitors themselves, not pageviews.
		vm.AIRefs = toRows(wv.AIReferrers, 6, wv.Recorded.AIReferrers)
		vm.EntryPages = toRows(wv.EntryPages, 6, wv.Recorded.EntryPages)
		vm.Browsers = toRows(wv.Browsers, 6, wv.Recorded.Browsers)
		vm.OSes = toRows(wv.OSes, 6, wv.Recorded.OSes)
		vm.DeviceRows = toRows(wv.DeviceSplit, 4, wv.Recorded.DeviceSplit)
		vm.UTMSources = toRows(wv.UTMSources, 6, wv.Recorded.UTMSources)
		vm.UTMMediums = toRows(wv.UTMMediums, 6, wv.Recorded.UTMMediums)
		vm.UTMCampaigns = toRows(wv.UTMCampaigns, 6, wv.Recorded.UTMCampaigns)
		vm.Countries = toRows(wv.Countries, 10, wv.Recorded.Countries)
		// "ZZ"/"XX" are MaxMind's not-a-real-country codes (anonymous proxy, unresolved).
		// Show them as a plain "Unknown" row, never a bogus flag. If the ONLY thing we have
		// is Unknown (geo disabled, or all traffic from unresolved IPs), the card carries no
		// signal at all — hide it rather than render a proud "Unknown 100%".
		realGeo := false
		for i := range vm.Countries {
			code := vm.Countries[i].Value
			if code == "ZZ" || code == "XX" || code == "" {
				vm.Countries[i].Value = "Unknown"
				continue
			}
			realGeo = true
			vm.Countries[i].Flag = flagOf(code)
			vm.Countries[i].Code = code
		}
		vm.HasGeo = realGeo
		maxH, totalH := 1, 0
		for _, c := range wv.Hours {
			totalH += c
			if c > maxH {
				maxH = c
			}
		}
		// P2-9: an hourly histogram needs enough volume to show a real pattern. Under
		// this floor it's a sparse row of near-empty bars ("peak 3/hr") dressed as a
		// chart — hide the whole zone until there's enough traffic to mean something.
		if totalH >= 200 {
			for h, c := range wv.Hours {
				vm.Hours = append(vm.Hours, hourBar{Hour: h, Count: c, HeightPct: int(math.Round(float64(c) / float64(maxH) * 100))})
			}
			vm.HoursMax = maxH
		}
	}

	buildKPIs(&vm, evs, trendEvent, rangeDays, rangeHours, sparkEnd)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is a live report AND it carries the whole app inline, so a cached copy is
	// both stale data and stale UI. It shipped with no cache directives at all, which lets a
	// browser apply its own heuristic: an engine upgrade could roll out and the operator would
	// still be looking at the previous build, with no way to tell. sdk.js is the asset that
	// wants caching (it sends max-age=3600); this page never does.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
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
		`<style>html{background:#12100C;color:#FAFAFA;font-family:ui-monospace,Menlo,monospace}`+
		`body{min-height:100vh;margin:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px}`+
		`a{color:#FFC900;text-decoration:none}.b{font-weight:800;letter-spacing:-.02em;font-size:18px;font-family:Inter,sans-serif}.b i{color:#FFC900;font-style:normal}</style>`+
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
	labels := make([]string, len(top))
	for i, n := range top {
		steps[i] = funnel.Step{Event: n}
		labels[i] = EventLabel(n)
	}
	return steps, strings.Join(labels, " → ")
}

// funnelIsSynthetic reports whether every step came from autocapture rather than from events the
// product deliberately sends.
//
// This is the difference between "your funnel is bad" and "you have not told us what a funnel is",
// and the dashboard was stating the first when only the second was true. With no product events
// instrumented, detectFunnel picks the three busiest names — which on a fresh install are always
// page view, engaged and click — computes the drop between them, marks it a WARNING, labels it
// FIX FIRST, and puts it at the top of the page in the loudest style available.
//
// Every one of four cold readers stopped there. One: "the number one alarm on my dashboard is
// scaring me about nothing I can act on." Another: "it is not a wrong number, it is a number
// promoted to a priority it has not earned." A drop between two autocapture events is not a
// product fact, and nothing anybody does to their product will move it.
func funnelIsSynthetic(steps []funnel.Step) bool {
	if len(steps) == 0 {
		return true
	}
	for _, st := range steps {
		if !EventIsAuto(st.Event) {
			return false // one real product event is enough to make this a real question
		}
	}
	return true
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
// groupProps finds the properties on this instance that look like ACCOUNT identifiers, so the
// grain picker offers real options instead of asking someone to remember what they instrumented.
//
// Name-matched against the conventional set rather than inferred from cardinality. Cardinality
// cannot tell an account id from a session id or a timestamp, and offering "session_id" as a way
// to group customers would produce a report that is confidently, unfalsifiably wrong — every
// "account" would have exactly one member. A short honest list beats a long guessed one.
func groupProps(evs []event.Event) []string {
	known := map[string]bool{
		"company": true, "company_id": true, "org": true, "org_id": true, "organization": true,
		"organisation": true, "account": true, "account_id": true, "workspace": true,
		"workspace_id": true, "team": true, "team_id": true, "tenant": true, "tenant_id": true,
		"group": true, "group_id": true, "customer": true, "customer_id": true,
	}
	seen := map[string]bool{}
	for _, e := range evs {
		for k := range e.Properties {
			if known[k] {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out) // map order is random; a picker that reshuffles is a picker nobody trusts
	return out
}

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
