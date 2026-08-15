package aicrawl

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// base is a fixed Monday in UTC. Every timestamp below is derived from it so the day
// buckets and the RFC3339 strings in the assertions are exact values rather than
// "whatever today happens to be" — a report that only agrees with itself on the day
// the test was written is not deterministic.
var base = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// status arrives as float64 because properties round-trip through JSON on the way in.
// asNum's int branch is covered separately in TestComputeStatusAcceptsIntAndFloat.
func crawl(crawler, operator, purpose, path string, status int, ts time.Time) event.Event {
	return event.Event{
		Name: CrawlEvent, DistinctID: "edge", Timestamp: ts,
		Properties: map[string]any{
			"crawler": crawler, "operator": operator, "purpose": purpose,
			"path": path, "status": float64(status), "site": "example.com",
		},
	}
}

func view(path, who string, ts time.Time) event.Event {
	return event.Event{
		Name: "$pageview", DistinctID: who, Timestamp: ts,
		Properties: map[string]any{"path": path},
	}
}

// --- Identify -------------------------------------------------------------------

type uaCase struct {
	ua       string
	name     string
	operator string
	purpose  string
}

// One realistic user agent per entry in defs. Written out longhand rather than
// generated from defs itself: a table built from the thing under test would happily
// agree with a typo in the operator column.
var uaCases = []uaCase{
	{"Mozilla/5.0 (compatible; ChatGPT-User/1.0; +https://openai.com/bot)", "ChatGPT-User", "OpenAI", PurposeUser},
	{"Mozilla/5.0 (compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot)", "OAI-SearchBot", "OpenAI", PurposeSearch},
	{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot", "GPTBot", "OpenAI", PurposeTraining},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Claude-User/1.0", "Claude-User", "Anthropic", PurposeUser},
	{"Mozilla/5.0 (compatible; Claude-SearchBot/1.0; +https://anthropic.com/searchbot)", "Claude-SearchBot", "Anthropic", PurposeSearch},
	{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ClaudeBot/1.0; +claudebot@anthropic.com", "ClaudeBot", "Anthropic", PurposeTraining},
	{"Mozilla/5.0 (compatible; Claude-Web/1.0)", "Claude-Web", "Anthropic", PurposeSearch},
	{"Mozilla/5.0 (compatible; anthropic-ai/1.0)", "Anthropic-AI", "Anthropic", PurposeTraining},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Perplexity-User/1.0", "Perplexity-User", "Perplexity", PurposeUser},
	{"Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)", "PerplexityBot", "Perplexity", PurposeSearch},
	{"Mozilla/5.0 (compatible; Google-Extended/1.0)", "Google-Extended", "Google", PurposeTraining},
	{"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", "Bingbot", "Microsoft", PurposeSearch},
	{"Mozilla/5.0 (compatible; Applebot-Extended/1.0; +http://www.apple.com/go/applebot)", "Applebot-Extended", "Apple", PurposeTraining},
	{"Mozilla/5.0 (compatible; Applebot/0.1; +http://www.apple.com/go/applebot)", "Applebot", "Apple", PurposeSearch},
	{"meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)", "Meta-ExternalAgent", "Meta", PurposeTraining},
	{"Mozilla/5.0 (compatible; Bytespider; spider-feedback@bytedance.com)", "Bytespider", "ByteDance", PurposeTraining},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15 (Amazonbot/0.1; +https://developer.amazon.com/amazonbot)", "Amazonbot", "Amazon", PurposeSearch},
	{"CCBot/2.0 (https://commoncrawl.org/faq/)", "CCBot", "Common Crawl", PurposeTraining},
	{"Mozilla/5.0 (compatible; cohere-ai/1.0; +https://cohere.com)", "Cohere-AI", "Cohere", PurposeTraining},
	{"Mozilla/5.0 (compatible; MistralAI-User/1.0; +https://mistral.ai)", "MistralAI-User", "Mistral", PurposeUser},
	{"Mozilla/5.0 (compatible; Diffbot/0.1; +http://www.diffbot.com)", "Diffbot", "Diffbot", PurposeTraining},
	{"Mozilla/5.0 (compatible; Timpibot/0.1; +http://www.timpi.io)", "Timpibot", "Timpi", PurposeTraining},
	{"Mozilla/5.0 (compatible; YouBot (+http://www.you.com/youbot))", "YouBot", "You.com", PurposeSearch},
	{"Mozilla/5.0 (compatible; DuckAssistBot/1.0; +https://duckduckgo.com/duckassistbot.html)", "DuckAssistBot", "DuckDuckGo", PurposeSearch},
}

func TestIdentifyEveryKnownCrawler(t *testing.T) {
	for _, tc := range uaCases {
		t.Run(tc.name, func(t *testing.T) {
			name, op, purpose, ok := Identify(tc.ua)
			if !ok {
				t.Fatalf("Identify(%q) not recognised, want %s", tc.ua, tc.name)
			}
			if name != tc.name || op != tc.operator || purpose != tc.purpose {
				t.Fatalf("Identify(%q) = %s/%s/%s, want %s/%s/%s",
					tc.ua, name, op, purpose, tc.name, tc.operator, tc.purpose)
			}
			if !Known(tc.ua) {
				t.Fatalf("Known(%q) = false", tc.ua)
			}
		})
	}
}

// A crawler added to defs without a user agent in the table above would otherwise ship
// untested — and the operator/purpose columns are exactly the fields a copy-paste
// addition gets wrong.
func TestIdentifyTableCoversEveryDef(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range uaCases {
		covered[tc.name] = true
	}
	for _, d := range defs {
		if !covered[d.Name] {
			t.Errorf("defs entry %q (pattern %q) has no user agent case in uaCases", d.Name, d.pattern)
		}
	}
	if len(uaCases) != len(defs) {
		t.Errorf("uaCases has %d entries, defs has %d — the table has drifted", len(uaCases), len(defs))
	}
}

func TestIdentifyIsCaseInsensitive(t *testing.T) {
	// crawlers do not agree on casing between their docs, their logs and their actual
	// requests, so the match has to survive all three renderings
	for _, ua := range []string{"GPTBot/1.2", "gptbot/1.2", "GPTBOT/1.2", "gPtBoT/1.2"} {
		name, op, purpose, ok := Identify(ua)
		if !ok || name != "GPTBot" || op != "OpenAI" || purpose != PurposeTraining {
			t.Fatalf("Identify(%q) = %s/%s/%s ok=%v, want GPTBot/OpenAI/training", ua, name, op, purpose, ok)
		}
	}
}

// The trap this guards: matching is substring-based, so a short pattern sitting above a
// long one would swallow every specific crawler underneath it. "applebot" inside
// "Applebot-Extended" is the live case — one is a search crawler that feeds citations,
// the other is a training opt-out token, and mislabelling one as the other inverts the
// advice the report gives. The Anthropic family is the one most likely to grow a
// shadowing pair as Anthropic ships more fetchers.
func TestIdentifyLongestPatternWins(t *testing.T) {
	cases := []uaCase{
		{"Claude-User/1.0", "Claude-User", "Anthropic", PurposeUser},
		{"Claude-SearchBot/1.0", "Claude-SearchBot", "Anthropic", PurposeSearch},
		{"ClaudeBot/1.0", "ClaudeBot", "Anthropic", PurposeTraining},
		{"Claude-Web/1.0", "Claude-Web", "Anthropic", PurposeSearch},
		{"Applebot-Extended/1.0", "Applebot-Extended", "Apple", PurposeTraining},
		{"Applebot/0.1", "Applebot", "Apple", PurposeSearch},
		{"Perplexity-User/1.0", "Perplexity-User", "Perplexity", PurposeUser},
		{"PerplexityBot/1.0", "PerplexityBot", "Perplexity", PurposeSearch},
		{"ChatGPT-User/1.0", "ChatGPT-User", "OpenAI", PurposeUser},
		{"GPTBot/1.2", "GPTBot", "OpenAI", PurposeTraining},
	}
	for _, tc := range cases {
		name, op, purpose, ok := Identify(tc.ua)
		if !ok || name != tc.name || op != tc.operator || purpose != tc.purpose {
			t.Errorf("Identify(%q) = %s/%s/%s ok=%v, want %s/%s/%s",
				tc.ua, name, op, purpose, ok, tc.name, tc.operator, tc.purpose)
		}
	}
}

// The ordering invariant behind the case above, asserted structurally so a future edit
// that drops the init() sort fails here rather than silently in one crawler's row.
func TestDefsSortedLongestPatternFirst(t *testing.T) {
	for i := 1; i < len(defs); i++ {
		if len(defs[i-1].pattern) < len(defs[i].pattern) {
			t.Fatalf("defs not longest-first at %d: %q before %q", i, defs[i-1].pattern, defs[i].pattern)
		}
	}
}

func TestIdentifyRejectsNonAICrawlers(t *testing.T) {
	// Googlebot is deliberately absent: it is a classic search crawler every site owner
	// already has a tool for, and including it would drown the AI signal this report exists
	// to isolate. Ordinary browsers must never appear here either — a browser hit is a
	// human, and counting one as a crawler would inflate the coverage numbers with people.
	for _, ua := range []string{
		"",
		"   ",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 (compatible; Googlebot-Image/1.0)",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:127.0) Gecko/20100101 Firefox/127.0",
		"curl/8.4.0",
		"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)",
	} {
		if name, op, purpose, ok := Identify(ua); ok {
			t.Errorf("Identify(%q) = %s/%s/%s, want not an AI crawler", ua, name, op, purpose)
		}
		if Known(ua) {
			t.Errorf("Known(%q) = true, want false", ua)
		}
	}
}

// --- normPath (exercised through Compute, the only path callers have) -------------

func TestNormPathThroughCompute(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/pricing", "/pricing"},
		{"/pricing?ref=twitter&utm_source=x", "/pricing"},
		{"/pricing#features", "/pricing"},
		{"/pricing?a=1#top", "/pricing"},
		{"/pricing#top?a=1", "/pricing"},
		{"/pricing/", "/pricing"},
		{"/blog/post/", "/blog/post"},
		{"/blog/post///", "/blog/post"},
		{"/", "/"},
		{"//", "/"},
		{"", "/"},
		{"   ", "/"},
		{"  /pricing  ", "/pricing"},
		{"?ref=x", "/"},
		{"pricing", "/pricing"},
		{"https://example.com/pricing", "/pricing"},
		{"https://example.com/pricing/", "/pricing"},
		{"https://example.com/pricing?ref=x", "/pricing"},
		{"http://example.com/blog/post#intro", "/blog/post"},
		{"https://example.com/", "/"},
		{"https://example.com", "/"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			r := Compute([]event.Event{
				crawl("GPTBot", "OpenAI", PurposeTraining, tc.in, 200, base),
			}, 30, base.Add(time.Hour))
			if len(r.Paths) != 1 {
				t.Fatalf("paths = %+v, want exactly one row", r.Paths)
			}
			if r.Paths[0].Path != tc.want {
				t.Fatalf("normPath(%q) = %q, want %q", tc.in, r.Paths[0].Path, tc.want)
			}
		})
	}
}

// Without normalisation "/pricing", "/pricing/" and "/pricing?ref=x" are three pages,
// which triples the page count, thirds every per-page hit count, and invents coverage
// gaps for pages that were in fact crawled.
func TestNormPathCollapsesVariantsOntoOnePage(t *testing.T) {
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/pricing", 200, base),
		crawl("GPTBot", "OpenAI", PurposeTraining, "/pricing/", 200, base.Add(time.Minute)),
		crawl("GPTBot", "OpenAI", PurposeTraining, "/pricing?ref=x", 200, base.Add(2*time.Minute)),
		crawl("GPTBot", "OpenAI", PurposeTraining, "https://example.com/pricing#buy", 200, base.Add(3*time.Minute)),
		// a human read the same page under a different rendering: it must not become a gap
		view("/pricing?utm_source=news", "v1", base.Add(4*time.Minute)),
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	if r.Pages != 1 || len(r.Paths) != 1 || r.Paths[0].Hits != 4 {
		t.Fatalf("pages = %d, paths = %+v; want one page with 4 hits", r.Pages, r.Paths)
	}
	if r.Crawlers[0].Pages != 1 {
		t.Fatalf("crawler pages = %d, want 1", r.Crawlers[0].Pages)
	}
	if len(r.Gaps) != 0 {
		t.Fatalf("gaps = %+v, want none — the crawled page was read under a query string", r.Gaps)
	}
}

// --- Compute --------------------------------------------------------------------

// mainEvents is the shared fixture: four crawlers, three operators, three pages, two
// errors, spread over three days inside a 30-day window, plus human traffic.
func mainEvents() []event.Event {
	return []event.Event{
		crawl("ClaudeBot", "Anthropic", PurposeTraining, "/b", 200, base.Add(-48*time.Hour)),   // 07-18
		crawl("ClaudeBot", "Anthropic", PurposeTraining, "/c", 500, base.Add(-47*time.Hour)),   // 07-18
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),                            // 07-20
		crawl("GPTBot", "OpenAI", PurposeTraining, "/b", 200, base.Add(time.Hour)),             // 07-20
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a?ref=x", 404, base.Add(2*time.Hour)),     // 07-20
		crawl("ChatGPT-User", "OpenAI", PurposeUser, "/a", 200, base.Add(20*time.Hour)),        // 07-21
		crawl("PerplexityBot", "Perplexity", PurposeSearch, "/a", 200, base.Add(21*time.Hour)), // 07-21

		view("/a", "v1", base),
		view("/gap-one", "v1", base),
		view("/gap-one", "v2", base.Add(time.Minute)),
		view("/gap-one", "v3", base.Add(2*time.Minute)),
		view("/gap-two", "v1", base.Add(3*time.Minute)),
		view("/gap-two", "v2", base.Add(4*time.Minute)),
	}
}

var mainAsof = base.Add(24 * time.Hour) // 2026-07-21T12:00:00Z

func TestComputeTotals(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	if !r.Reporting {
		t.Fatal("reporting = false with $ai_crawl events present")
	}
	if r.Days != 30 {
		t.Errorf("days = %d, want 30", r.Days)
	}
	if r.Hits != 7 {
		t.Errorf("hits = %d, want 7 (pageviews must not count)", r.Hits)
	}
	if r.Pages != 3 {
		t.Errorf("pages = %d, want 3 distinct crawled paths", r.Pages)
	}
	if r.Errors != 2 {
		t.Errorf("errors = %d, want 2 (one 404, one 500)", r.Errors)
	}
	// the purpose split is the whole point of the module: a training sweep and a live
	// user fetch are not the same event with a different label
	if r.Training != 5 || r.Search != 1 || r.User != 1 {
		t.Errorf("purpose split = training %d / search %d / user %d, want 5/1/1", r.Training, r.Search, r.User)
	}
	if r.Training+r.Search+r.User != r.Hits {
		t.Errorf("purpose split %d does not sum to hits %d", r.Training+r.Search+r.User, r.Hits)
	}
	if r.FirstAt != "2026-07-18T12:00:00Z" {
		t.Errorf("first_at = %q, want 2026-07-18T12:00:00Z", r.FirstAt)
	}
	if r.LastAt != "2026-07-21T09:00:00Z" {
		t.Errorf("last_at = %q, want 2026-07-21T09:00:00Z", r.LastAt)
	}
	if r.Note != "" {
		t.Errorf("note = %q, want empty when hits landed in the window", r.Note)
	}
}

func TestComputeCrawlerRows(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	want := []CrawlerRow{
		{Crawler: "GPTBot", Operator: "OpenAI", Purpose: PurposeTraining, Hits: 3, Pages: 2, Errors: 1,
			LastAt: "2026-07-20T14:00:00Z", LastPath: "/a"},
		{Crawler: "ClaudeBot", Operator: "Anthropic", Purpose: PurposeTraining, Hits: 2, Pages: 2, Errors: 1,
			LastAt: "2026-07-18T13:00:00Z", LastPath: "/c"},
		// 1 hit each: the tie breaks alphabetically, never by map order
		{Crawler: "ChatGPT-User", Operator: "OpenAI", Purpose: PurposeUser, Hits: 1, Pages: 1, Errors: 0,
			LastAt: "2026-07-21T08:00:00Z", LastPath: "/a"},
		{Crawler: "PerplexityBot", Operator: "Perplexity", Purpose: PurposeSearch, Hits: 1, Pages: 1, Errors: 0,
			LastAt: "2026-07-21T09:00:00Z", LastPath: "/a"},
	}
	if !reflect.DeepEqual(r.Crawlers, want) {
		t.Fatalf("crawlers =\n%+v\nwant\n%+v", r.Crawlers, want)
	}
}

func TestComputeOperatorRollup(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	// the level a reader actually thinks in: "has OpenAI read my site", with the
	// training/search/user split kept apart inside the rollup
	want := []OperatorRow{
		{Operator: "OpenAI", Hits: 4, Pages: 2, Training: 3, Search: 0, User: 1, LastAt: "2026-07-21T08:00:00Z"},
		{Operator: "Anthropic", Hits: 2, Pages: 2, Training: 2, LastAt: "2026-07-18T13:00:00Z"},
		{Operator: "Perplexity", Hits: 1, Pages: 1, Search: 1, LastAt: "2026-07-21T09:00:00Z"},
	}
	if !reflect.DeepEqual(r.Operators, want) {
		t.Fatalf("operators =\n%+v\nwant\n%+v", r.Operators, want)
	}
	total := 0
	for _, o := range r.Operators {
		if o.Training+o.Search+o.User != o.Hits {
			t.Errorf("%s: split %d/%d/%d does not sum to %d hits", o.Operator, o.Training, o.Search, o.User, o.Hits)
		}
		total += o.Hits
	}
	if total != r.Hits {
		t.Errorf("operator hits sum to %d, headline says %d", total, r.Hits)
	}
}

func TestComputePathTable(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	// Crawlers is DISTINCT crawlers, not hits: /a was fetched four times but by three
	// different fetchers, and it is the three that says every assistant can cite it.
	want := []PathRow{
		{Path: "/a", Hits: 4, Crawlers: 3, Errors: 1, LastAt: "2026-07-21T09:00:00Z"},
		{Path: "/b", Hits: 2, Crawlers: 2, Errors: 0, LastAt: "2026-07-20T13:00:00Z"},
		{Path: "/c", Hits: 1, Crawlers: 1, Errors: 1, LastAt: "2026-07-18T13:00:00Z"},
	}
	if !reflect.DeepEqual(r.Paths, want) {
		t.Fatalf("paths =\n%+v\nwant\n%+v", r.Paths, want)
	}
}

func TestComputeTrendIsDenseIncludingZeroDays(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	if len(r.Trend) != 30 {
		t.Fatalf("trend has %d points, want one per day in the window", len(r.Trend))
	}
	if r.Trend[0].Day != "2026-06-22" || r.Trend[29].Day != "2026-07-21" {
		t.Fatalf("trend spans %s..%s", r.Trend[0].Day, r.Trend[29].Day)
	}
	byDay := map[string]DayPoint{}
	for _, d := range r.Trend {
		if _, dup := byDay[d.Day]; dup {
			t.Fatalf("duplicate day %s in trend", d.Day)
		}
		byDay[d.Day] = d
	}
	want := map[string]DayPoint{
		"2026-07-18": {Day: "2026-07-18", Hits: 2, Training: 2},
		"2026-07-19": {Day: "2026-07-19"}, // the silent day between two sweeps must be drawn
		"2026-07-20": {Day: "2026-07-20", Hits: 3, Training: 3},
		"2026-07-21": {Day: "2026-07-21", Hits: 2, Search: 1, User: 1},
	}
	for day, w := range want {
		if got := byDay[day]; got != w {
			t.Errorf("trend[%s] = %+v, want %+v", day, got, w)
		}
	}
	zeros := 0
	for _, d := range r.Trend {
		if d.Hits == 0 {
			zeros++
		}
	}
	if zeros != 27 {
		t.Errorf("%d zero days drawn, want 27 — a sparse series draws a straight line across silence", zeros)
	}
	sum := 0
	for _, d := range r.Trend {
		sum += d.Hits
		if d.Training+d.Search+d.User != d.Hits {
			t.Errorf("trend[%s] split does not sum to its hits: %+v", d.Day, d)
		}
	}
	if sum != r.Hits {
		t.Errorf("trend sums to %d hits, headline says %d", sum, r.Hits)
	}
}

// FAILING ON PURPOSE — this is a real defect in Compute, not a wrong expectation.
//
// The aggregate window is `cut = asof.AddDate(0, 0, -days)`, an instant, but the trend
// is built from `asof.AddDate(0, 0, -i)` for i in [days-1, 0], which is the `days`
// CALENDAR DATES ending on asof's date. The date of `cut` is therefore inside the
// aggregate window but outside the trend, so every hit between `cut` and midnight of
// that date is counted in Result.Hits and in the crawler/operator/path tables while
// appearing on no day of the chart. sum(Trend.Hits) < Hits, and the dashboard's chart
// disagrees with the headline it sits under — exactly the disagreement the agreement
// guarantee forbids. Fix by starting the trend at `cut`'s date (days+1 points) or by
// cutting on the date boundary rather than the instant; do not weaken this test.
func TestTrendCoversEveryHitItCounted(t *testing.T) {
	asof := base // 2026-07-20T12:00:00Z
	// the first hour of the FIRST day of the window — where the old off-by-one lived: the
	// totals counted this hit and the chart started a day later, so it appeared in no bar
	inWindow := base.UTC().Truncate(24*time.Hour).AddDate(0, 0, -29).Add(1 * time.Hour)
	r := Compute([]event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, inWindow),
	}, 30, asof)

	if r.Hits != 1 {
		t.Fatalf("hits = %d, want 1 — the event sits after the window cut", r.Hits)
	}
	sum := 0
	for _, d := range r.Trend {
		sum += d.Hits
	}
	if sum != r.Hits {
		t.Errorf("trend sums to %d but %d hits were counted: the day %s is inside the aggregate window and missing from the trend (spans %s..%s)",
			sum, r.Hits, inWindow.UTC().Format("2006-01-02"), r.Trend[0].Day, r.Trend[len(r.Trend)-1].Day)
	}
}

func TestComputeWindowBoundary(t *testing.T) {
	asof := base
	// The window is thirty CALENDAR days ending on asof's date, so the cut is midnight at
	// the start of the first of them. It cannot be a rolling asof-minus-30-days instant:
	// this suite also requires the trend to be exactly `days` long AND to contain every
	// hit the totals counted, and a mid-day cut makes those two impossible together —
	// the overlap hour is counted in the totals and belongs to no bar.
	cut := base.UTC().Truncate(24*time.Hour).AddDate(0, 0, -29)

	// exactly on the cut is inside: the boundary is inclusive on the old side, so an
	// event is never dropped by both the current window and the previous one
	onCut := Compute([]event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, cut),
	}, 30, asof)
	if onCut.Hits != 1 {
		t.Errorf("event exactly on the cut: hits = %d, want 1", onCut.Hits)
	}

	// a nanosecond older is outside the aggregates — but it still proves the reporter
	// is installed, which is a fact about the customer's site, not about the window
	older := Compute([]event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, cut.Add(-time.Nanosecond)),
	}, 30, asof)
	if older.Hits != 0 || len(older.Crawlers) != 0 || len(older.Paths) != 0 || len(older.Operators) != 0 {
		t.Errorf("out-of-window event leaked into the aggregates: %+v", older)
	}
	if !older.Reporting {
		t.Error("reporting = false, want true — a hit outside the window still proves the reporter is installed")
	}
	// FirstAt/LastAt are all-time on purpose: "last crawled 6 weeks ago" is the answer
	// the reader needs, and a window-clipped timestamp would hide it
	if older.FirstAt == "" || older.LastAt == "" {
		t.Errorf("first/last must survive the window clip: %q..%q", older.FirstAt, older.LastAt)
	}
}

func TestComputeIgnoresEventsAfterAsof(t *testing.T) {
	// clock skew on an edge worker is routine; a hit stamped in the future must not
	// appear at all, not even as evidence that reporting exists, or a single bad clock
	// would flip an uninstrumented site into "installed, zero crawls"
	r := Compute([]event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base.Add(time.Hour)),
		view("/a", "v1", base.Add(2*time.Hour)),
	}, 30, base)
	if r.Reporting || r.Hits != 0 || len(r.Crawlers) != 0 || len(r.Gaps) != 0 {
		t.Fatalf("future event was not ignored: %+v", r)
	}
	if r.FirstAt != "" || r.LastAt != "" {
		t.Errorf("future event set first/last: %q..%q", r.FirstAt, r.LastAt)
	}
	if r.Note != "" {
		t.Errorf("note = %q, want empty — nothing has ever reported", r.Note)
	}
}

// --- Reporting vs Hits ------------------------------------------------------------

// "No crawler has ever read a page here" and "nobody installed the reporter" are
// identical in the data and mean opposite things. Conflating them prints a gap list
// naming every page on the site as uncrawled, which is a list of lies.
func TestNothingEverReportedIsNotZeroCrawls(t *testing.T) {
	evs := []event.Event{
		view("/", "v1", base),
		view("/pricing", "v1", base.Add(time.Minute)),
		view("/pricing", "v2", base.Add(2*time.Minute)),
		view("/blog/post", "v3", base.Add(3*time.Minute)),
	}
	r := Compute(evs, 30, base.Add(time.Hour))

	if r.Reporting {
		t.Fatal("reporting = true with no $ai_crawl event ever seen")
	}
	if r.Hits != 0 || r.Pages != 0 {
		t.Errorf("hits = %d, pages = %d, want 0/0", r.Hits, r.Pages)
	}
	if len(r.Gaps) != 0 {
		t.Fatalf("gaps = %+v, want none — with no reporter every page looks uncrawled and every row would be a lie", r.Gaps)
	}
	if r.Note != "" {
		t.Errorf("note = %q, want empty — there is nothing to report about, so nothing to explain away", r.Note)
	}
	// the dashboard card asks for installation on !Reporting; it must not also have to
	// defend against nulls to do it
	if r.Crawlers == nil || r.Operators == nil || r.Paths == nil || r.Gaps == nil || r.Trend == nil {
		t.Errorf("empty tables must be empty slices, not nil: %+v", r)
	}
	if len(r.Trend) != 30 {
		t.Errorf("trend has %d points, want 30 zeros — the chart still has an x-axis", len(r.Trend))
	}
}

func TestReportingButSilentInWindow(t *testing.T) {
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base.AddDate(0, 0, -60)),
		view("/pricing", "v1", base),
		view("/pricing", "v2", base.Add(time.Minute)),
	}
	r := Compute(evs, 30, base.Add(time.Hour))

	if !r.Reporting {
		t.Fatal("reporting = false, want true — a hit outside the window still proves installation")
	}
	if r.Hits != 0 {
		t.Errorf("hits = %d, want 0", r.Hits)
	}
	if r.Note == "" {
		t.Error("installed-but-silent must carry a note; an empty report with no explanation reads as broken")
	}
	if len(r.Gaps) != 0 {
		t.Fatalf("gaps = %+v, want none — nothing crawled in the window means every page is 'uncrawled'", r.Gaps)
	}
	if r.LastAt == "" {
		t.Error("last_at must survive so the reader can see how long the silence has run")
	}
}

// --- Gaps ---------------------------------------------------------------------

func TestGapsRankPagesHumansReadThatNoCrawlerFetched(t *testing.T) {
	r := Compute(mainEvents(), 30, mainAsof)

	want := []GapRow{
		{Path: "/gap-one", Views: 3, Visitors: 3},
		{Path: "/gap-two", Views: 2, Visitors: 2},
	}
	if !reflect.DeepEqual(r.Gaps, want) {
		t.Fatalf("gaps =\n%+v\nwant\n%+v", r.Gaps, want)
	}
	// /a had human traffic AND was crawled; it is not a gap
	for _, g := range r.Gaps {
		if g.Path == "/a" {
			t.Fatalf("a crawled page appeared as a gap: %+v", g)
		}
	}
}

func TestGapsUseTheSameWindowAsTheCrawls(t *testing.T) {
	// a page that stopped getting visitors months ago has no claim on crawler attention
	// today, so old pageviews must not manufacture a gap
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		view("/stale", "v1", base.AddDate(0, 0, -60)),
		view("/stale", "v2", base.AddDate(0, 0, -60)),
		view("/fresh", "v1", base.Add(-time.Hour)),
		view("/fresh", "v2", base.Add(-time.Hour)),
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	if len(r.Gaps) != 1 || r.Gaps[0].Path != "/fresh" {
		t.Fatalf("gaps = %+v, want only /fresh", r.Gaps)
	}
}

func TestGapsReadPathFromUrlWhenPathMissing(t *testing.T) {
	// some SDK versions autocapture `url` and not `path`; the gap list must not go blank
	// for those instances
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		{Name: "$pageview", DistinctID: "v1", Timestamp: base,
			Properties: map[string]any{"url": "https://example.com/docs/install?utm=1"}},
		{Name: "$pageview", DistinctID: "v2", Timestamp: base,
			Properties: map[string]any{"url": "https://example.com/docs/install?utm=2"}},
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	want := []GapRow{{Path: "/docs/install", Views: 2, Visitors: 2}}
	if !reflect.DeepEqual(r.Gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", r.Gaps, want)
	}
}

// FAILING ON PURPOSE — this is a real defect in Compute, not a wrong expectation.
//
// The pageview branch reads `path := normPath(pageviewPath(e))` and then guards with
// `if path == "" { continue }`. That guard can never fire: normPath's documented
// contract is that empty defaults to "/", so a pageview carrying neither `path` nor
// `url` is silently filed as a homepage view instead of being dropped. The guard's
// existence is the proof of intent — nothing else would explain it.
//
// The consequence is not cosmetic. Location-less pageviews (manual capture(), server
// SDKs, a stripped SDK version) accumulate against "/", and once 20 of them land on a
// site whose homepage no crawler happens to have fetched, insight.crawlGap prints
// "No AI crawler has read /" over traffic that was never on "/" at all — a specific,
// confident, fabricated claim. Fix by testing pageviewPath(e) for empty BEFORE
// normalising it, not by relaxing this test.
func TestPageviewWithNoLocationIsDroppedNotFiledAsHomepage(t *testing.T) {
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		{Name: "$pageview", DistinctID: "v1", Timestamp: base, Properties: map[string]any{}},
		{Name: "$pageview", DistinctID: "v2", Timestamp: base, Properties: map[string]any{"path": "   "}},
		{Name: "$pageview", DistinctID: "v3", Timestamp: base, Properties: nil},
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	if len(r.Gaps) != 0 {
		t.Fatalf("gaps = %+v, want none — pageviews with no location must be dropped, not attributed to \"/\"", r.Gaps)
	}
}

func TestGapsAreCappedAndTieBrokenByPath(t *testing.T) {
	evs := []event.Event{crawl("GPTBot", "OpenAI", PurposeTraining, "/crawled", 200, base)}
	// 30 uncrawled pages, all on the same view count so only the tie-break decides order.
	// Two DISTINCT visitors each: a page one person reloads is not proven demand and the
	// gap list now refuses to report it, so a single-visitor fixture would test nothing.
	for i := 0; i < 30; i++ {
		evs = append(evs, view(fmt.Sprintf("/p%02d", i), "v1", base))
		evs = append(evs, view(fmt.Sprintf("/p%02d", i), "v2", base))
	}
	r := Compute(evs, 30, base.Add(time.Hour))

	if len(r.Gaps) != maxRows {
		t.Fatalf("gaps = %d rows, want the table capped at %d", len(r.Gaps), maxRows)
	}
	for i, g := range r.Gaps {
		if want := fmt.Sprintf("/p%02d", i); g.Path != want {
			t.Fatalf("gaps[%d] = %q, want %q — equal views must break alphabetically", i, g.Path, want)
		}
	}
}

func TestPathsAreCappedButPagesCountsThemAll(t *testing.T) {
	var evs []event.Event
	for i := 0; i < 40; i++ {
		evs = append(evs, crawl("GPTBot", "OpenAI", PurposeTraining, fmt.Sprintf("/p%02d", i), 200, base))
	}
	r := Compute(evs, 30, base.Add(time.Hour))

	if len(r.Paths) != maxRows {
		t.Fatalf("paths = %d rows, want %d", len(r.Paths), maxRows)
	}
	// the cap is a display limit; the headline must still describe the whole site
	if r.Pages != 40 {
		t.Errorf("pages = %d, want 40 — the cap trims the table, not the count", r.Pages)
	}
	if r.Hits != 40 {
		t.Errorf("hits = %d, want 40", r.Hits)
	}
}

// --- properties and defaults -------------------------------------------------

func TestComputeSkipsEventsWithNoCrawlerName(t *testing.T) {
	// a malformed report is not a crawl: it must not invent a row, but it does prove
	// something is posting, so Reporting stays true and the note explains the silence
	r := Compute([]event.Event{
		{Name: CrawlEvent, DistinctID: "edge", Timestamp: base,
			Properties: map[string]any{"path": "/a", "status": float64(200)}},
	}, 30, base.Add(time.Hour))
	if r.Hits != 0 || len(r.Crawlers) != 0 {
		t.Fatalf("nameless crawl event produced a row: %+v", r)
	}
	if !r.Reporting || r.Note == "" {
		t.Errorf("reporting = %v, note = %q; want true with a note", r.Reporting, r.Note)
	}
}

func TestComputeUnknownOperatorAndPurpose(t *testing.T) {
	// a self-hoster's middleware may report a crawler we have no def for. It still gets
	// counted — an unrecognised fetcher is data, not an error — but it must not be
	// silently filed under a real operator or a purpose it never claimed.
	r := Compute([]event.Event{
		{Name: CrawlEvent, DistinctID: "edge", Timestamp: base,
			Properties: map[string]any{"crawler": "SomeNewBot", "path": "/a", "status": float64(200)}},
	}, 30, base.Add(time.Hour))

	if r.Hits != 1 {
		t.Fatalf("hits = %d, want 1", r.Hits)
	}
	if len(r.Operators) != 1 || r.Operators[0].Operator != "unknown" {
		t.Fatalf("operators = %+v, want a single 'unknown' row", r.Operators)
	}
	if r.Training != 0 || r.Search != 0 || r.User != 0 {
		t.Errorf("an unlabelled purpose was filed into the split: %d/%d/%d", r.Training, r.Search, r.User)
	}
	if r.Operators[0].Training+r.Operators[0].Search+r.Operators[0].User != 0 {
		t.Errorf("operator split invented a purpose: %+v", r.Operators[0])
	}
}

func TestComputeStatusAcceptsIntAndFloat(t *testing.T) {
	// status arrives as float64 from JSON and as int from an in-process caller; an error
	// that only counts on one of those paths would make /v1 and MCP disagree
	mk := func(status any) Result {
		return Compute([]event.Event{
			{Name: CrawlEvent, DistinctID: "edge", Timestamp: base,
				Properties: map[string]any{"crawler": "GPTBot", "operator": "OpenAI",
					"purpose": PurposeTraining, "path": "/a", "status": status}},
		}, 30, base.Add(time.Hour))
	}
	for _, status := range []any{403, float64(403)} {
		if r := mk(status); r.Errors != 1 {
			t.Errorf("status %v (%T): errors = %d, want 1", status, status, r.Errors)
		}
	}
	// a missing status is not an error; asNum returns 0 and 0 < 400
	if r := mk(nil); r.Errors != 0 {
		t.Errorf("missing status counted as an error: %d", r.Errors)
	}
	for _, status := range []any{float64(200), float64(301), float64(399)} {
		if r := mk(status); r.Errors != 0 {
			t.Errorf("status %v counted as an error", status)
		}
	}
	for _, status := range []any{float64(400), float64(404), float64(500), float64(503)} {
		if r := mk(status); r.Errors != 1 {
			t.Errorf("status %v not counted as an error", status)
		}
	}
}

func TestComputeDefaults(t *testing.T) {
	r := Compute(nil, 0, time.Time{})
	if r.Days != 30 {
		t.Errorf("days = %d, want the 30-day default", r.Days)
	}
	if len(r.Trend) != 30 {
		t.Errorf("trend = %d points, want 30", len(r.Trend))
	}
	if r.Reporting || r.Hits != 0 || r.Note != "" {
		t.Errorf("empty input produced content: %+v", r)
	}
	// a zero asof means "now"; the trend must end on today rather than the zero year
	if last := r.Trend[len(r.Trend)-1].Day; last != time.Now().UTC().Format("2006-01-02") {
		t.Errorf("trend ends %s, want today", last)
	}
	for _, days := range []int{-1, 0} {
		if got := Compute(nil, days, base).Days; got != 30 {
			t.Errorf("days = %d for input %d, want 30", got, days)
		}
	}
	if r7 := Compute(mainEvents(), 7, mainAsof); len(r7.Trend) != 7 || r7.Trend[0].Day != "2026-07-15" {
		t.Errorf("7-day window: %d points starting %s", len(r7.Trend), r7.Trend[0].Day)
	}
}

// --- determinism ----------------------------------------------------------------

// Every table here comes off a Go map, whose iteration order is randomised per run.
// Without a total order in each comparator the same events would render a different
// report on every request, and /v1, MCP and the dashboard would disagree about a
// number purely because they asked at different times.
func TestComputeIsDeterministic(t *testing.T) {
	evs := mainEvents()
	want := Compute(evs, 30, mainAsof)

	reversed := make([]event.Event, len(evs))
	for i, e := range evs {
		reversed[len(evs)-1-i] = e
	}
	if got := Compute(reversed, 30, mainAsof); !reflect.DeepEqual(got, want) {
		t.Fatalf("reversed input changed the report:\n%+v\nvs\n%+v", got, want)
	}
	// repeated runs over the identical slice must be byte-identical too: map iteration
	// is re-randomised on every range, not once per process
	for i := 0; i < 50; i++ {
		if got := Compute(evs, 30, mainAsof); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d differed:\n%+v\nvs\n%+v", i, got, want)
		}
	}
}

func TestTiesOrderAlphabetically(t *testing.T) {
	// every table tied on its sort key, so only the tie-break decides the output
	ts := base
	evs := []event.Event{
		crawl("Zeta", "Zulu", PurposeSearch, "/z", 200, ts),
		crawl("Alpha", "Alpha Corp", PurposeSearch, "/a", 200, ts),
		crawl("Mid", "Mike", PurposeSearch, "/m", 200, ts),
	}
	for i := 0; i < 50; i++ {
		r := Compute(evs, 30, base.Add(time.Hour))
		if r.Crawlers[0].Crawler != "Alpha" || r.Crawlers[1].Crawler != "Mid" || r.Crawlers[2].Crawler != "Zeta" {
			t.Fatalf("crawler tie order = %+v", r.Crawlers)
		}
		if r.Operators[0].Operator != "Alpha Corp" || r.Operators[1].Operator != "Mike" || r.Operators[2].Operator != "Zulu" {
			t.Fatalf("operator tie order = %+v", r.Operators)
		}
		if r.Paths[0].Path != "/a" || r.Paths[1].Path != "/m" || r.Paths[2].Path != "/z" {
			t.Fatalf("path tie order = %+v", r.Paths)
		}
	}
}

// --- Gap guards -------------------------------------------------------------
//
// Two rules keep the gap list from recommending pages nobody should be indexing. Both were added
// because the report was leading with logged-in app routes and telling the reader to get them
// into a sitemap, which is wrong advice and a privacy problem in the same sentence.

// A page one person looked at is not proven demand — it is usually the owner reloading their own
// dashboard. The rest of this module refuses to speak on thin samples and the gap list has to as
// well, or it fills with private pages that one account happens to visit a lot.
func TestGapsIgnorePagesWithASingleVisitor(t *testing.T) {
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		view("/dashboard", "owner", base),
		view("/dashboard", "owner", base.Add(time.Minute)),
		view("/dashboard", "owner", base.Add(2*time.Minute)), // three views, ONE person
		view("/pricing", "v1", base),
		view("/pricing", "v2", base.Add(time.Minute)),
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	for _, g := range r.Gaps {
		if g.Path == "/dashboard" {
			t.Errorf("a page with one visitor was reported as a coverage gap: %+v", g)
		}
	}
	if len(r.Gaps) != 1 || r.Gaps[0].Path != "/pricing" {
		t.Fatalf("gaps = %+v, want only /pricing", r.Gaps)
	}
}

// A page the site's own robots.txt keeps crawlers out of is not a gap, it is compliance working.
// Telling someone to fix it would be telling them to undo a decision they made deliberately.
func TestGapsRespectRobotsDisallow(t *testing.T) {
	scan := func(disallow string, ts time.Time) event.Event {
		return event.Event{Name: "$site_readable", DistinctID: "scanner", Timestamp: ts,
			Properties: map[string]any{"robots_disallow": disallow}}
	}
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		scan("/guides,/private-docs", base),
		view("/guides/intro", "v1", base), view("/guides/intro", "v2", base),
		view("/private-docs/x", "v1", base), view("/private-docs/x", "v2", base),
		view("/blog/post", "v1", base), view("/blog/post", "v2", base),
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	for _, g := range r.Gaps {
		if strings.HasPrefix(g.Path, "/guides") || strings.HasPrefix(g.Path, "/private-docs") {
			t.Errorf("a robots.txt-disallowed path was reported as a gap: %+v", g)
		}
	}
	if len(r.Gaps) != 1 || r.Gaps[0].Path != "/blog/post" {
		t.Fatalf("gaps = %+v, want only /blog/post", r.Gaps)
	}
}

// Only the NEWEST scan counts. A site that used to block /app and no longer does must not keep
// having those pages suppressed by a stale reading of its robots.txt.
func TestGapsUseTheNewestRobotsScan(t *testing.T) {
	scan := func(disallow string, ts time.Time) event.Event {
		return event.Event{Name: "$site_readable", DistinctID: "scanner", Timestamp: ts,
			Properties: map[string]any{"robots_disallow": disallow}}
	}
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
		scan("/guides", base.Add(-2*time.Hour)), // older: blocked
		scan("", base.Add(-time.Hour)),          // newer: no longer blocked
		view("/guides/intro", "v1", base), view("/guides/intro", "v2", base),
	}
	r := Compute(evs, 30, base.Add(time.Hour))
	if len(r.Gaps) != 1 || r.Gaps[0].Path != "/guides/intro" {
		t.Fatalf("gaps = %+v — a stale robots scan is still suppressing a page", r.Gaps)
	}
}

// robots.txt alone does not protect signed-in routes, because almost nobody disallows their own
// dashboard — there is no reason to, since it needs a session. So it sailed past the robots check
// and led the report on our own instance: "No AI crawler has read /dashboard … list it in your
// sitemap." Wrong advice and a privacy problem in one sentence.
func TestGapsNeverRecommendIndexingSignedInRoutes(t *testing.T) {
	evs := []event.Event{
		crawl("GPTBot", "OpenAI", PurposeTraining, "/a", 200, base),
	}
	for _, p := range []string{"/dashboard", "/settings/team", "/admin", "/billing", "/projects/abc", "/account"} {
		evs = append(evs, view(p, "v1", base), view(p, "v2", base.Add(time.Minute)))
	}
	evs = append(evs, view("/pricing", "v1", base), view("/pricing", "v2", base.Add(time.Minute)))

	r := Compute(evs, 30, base.Add(time.Hour))
	for _, g := range r.Gaps {
		if g.Path != "/pricing" {
			t.Errorf("recommended indexing a signed-in route: %+v", g)
		}
	}
	if len(r.Gaps) != 1 {
		t.Fatalf("gaps = %+v, want only /pricing", r.Gaps)
	}
}

// The heuristic matches the FIRST SEGMENT, so a public page whose name merely starts with those
// letters is not suppressed. /app-store-analytics is a blog post; /app/settings is not.
func TestPrivateRouteMatchesWholeSegmentsOnly(t *testing.T) {
	for _, p := range []string{"/app-store-analytics", "/dashboards-compared", "/accounting", "/teamwork"} {
		if privateRoute(p) {
			t.Errorf("%s is a public page but was treated as signed-in", p)
		}
	}
	for _, p := range []string{"/app", "/app/settings", "/dashboard", "/admin/users", "/projects/abc"} {
		if !privateRoute(p) {
			t.Errorf("%s is a signed-in route but was not caught", p)
		}
	}
}

// A USER-AGENT IS A CLAIM, NOT AN IDENTITY.
//
// Measured on a live instance: ClaudeBot's most recent request was /@fs/etc/passwd and CCBot's was
// /.aws/config. Neither Anthropic nor Common Crawl fetches those. Something is scanning for
// secrets while wearing a bot's user-agent, and counting it means the pane reports "Anthropic read
// your site" about a request Anthropic never made.
//
// Worse than an ordinary wrong number, because it is attributed to a named company on a page the
// customer might screenshot.
func TestAScannerWearingABotUserAgentIsNotCounted(t *testing.T) {
	now := time.Now().UTC()
	mk := func(path string) event.Event {
		return event.Event{
			ID: path, Name: CrawlEvent, DistinctID: "$crawler", Timestamp: now.Add(-time.Hour),
			Properties: map[string]any{
				"crawler": "ClaudeBot", "operator": "Anthropic", "purpose": "training",
				"path": path, "status": float64(200),
			},
		}
	}
	evs := []event.Event{
		mk("/pricing"),
		mk("/@fs/etc/passwd"),
		mk("/.aws/config"),
		mk("/.env"),
		mk("/wp-admin/setup-config.php"),
	}
	r := Compute(evs, 30, now)

	if r.Hits != 1 {
		t.Errorf("counted %d hits; only /pricing is a real fetch", r.Hits)
	}
	if r.Probes != 4 {
		t.Errorf("reported %d probes, expected 4", r.Probes)
	}
	// and the scan must not become the operator's "last page read"
	for _, c := range r.Crawlers {
		if strings.Contains(c.LastPath, "passwd") || strings.Contains(c.LastPath, ".aws") {
			t.Errorf("a scanner path became %s's last read: %q", c.Crawler, c.LastPath)
		}
	}
}

// Dropped, but never silently. A total that quietly shrank is one nobody can reconcile against
// their own server logs.
func TestProbesAreReportedNotHidden(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{{
		ID: "p", Name: CrawlEvent, DistinctID: "$crawler", Timestamp: now.Add(-time.Hour),
		Properties: map[string]any{"crawler": "CCBot", "operator": "Common Crawl",
			"purpose": "training", "path": "/.env", "status": float64(404)},
	}}
	r := Compute(evs, 30, now)
	if r.Probes != 1 {
		t.Fatalf("probe not reported: %+v", r.Probes)
	}
	if r.Hits != 0 {
		t.Errorf("a scan counted as a fetch: hits=%d", r.Hits)
	}
}
