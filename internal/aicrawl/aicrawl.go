// Package aicrawl aggregates $ai_crawl events: the AI crawlers' own visits to the
// customer's site, recorded server-side.
//
// This is the half of AI visibility that a JavaScript pixel physically cannot see.
// GPTBot, ClaudeBot and PerplexityBot execute no JavaScript, so they never fire a
// $pageview — every client-side analytics tool on the market reports zero of them,
// and the GEO monitors that would care never touch the customer's server. The fix is
// a few lines of edge middleware that report the hit from the request itself; the
// events land on the same instance as everything else, which is what lets the four
// stages of the loop finally sit on one log:
//
//	crawled → readable → named in the answer → sent a human who converted
//
// Every incumbent holds exactly one of those.
//
// Event contract (properties on $ai_crawl):
//
//	crawler   string — normalized name, e.g. "GPTBot", "ClaudeBot"
//	operator  string — who runs it: "OpenAI", "Anthropic", "Perplexity", ...
//	purpose   string — "training" | "search" | "user" (see Purpose*)
//	path      string — request path, query stripped by the middleware
//	status    number — HTTP status the crawler received
//	site      string — hostname, same key the dashboard's site selector uses
package aicrawl

import (
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// CrawlEvent is the event name the edge middleware writes. Exported because several
// packages must recognise it: ingest exempts it from the bot filter (it is BY
// DEFINITION a bot hit, reported by the server), and the billing/pulse counters
// exclude it so a crawl wave never reads as a traffic spike or an invoice.
const CrawlEvent = "$ai_crawl"

// readableEvent is the cloud's site scan. aicrawl reads one property off it — the robots.txt
// disallow list — rather than importing the insight package, which would be a cycle. The
// literal is duplicated deliberately and both sides say so.
const readableEvent = "$site_readable"

// The three reasons an AI company fetches a page, kept apart because they mean
// completely different things to the person reading the report:
//
//   - training: your words may end up in a future model. No traffic, no attribution.
//   - search: you are being indexed for answers. This is the one that feeds citations.
//   - user: a person asked an assistant about you RIGHT NOW and it fetched this page
//     to answer them. That is a live human intent signal, not a robot.
//
// Lumping them together is the mistake most crawler dashboards make: it turns the
// most valuable signal on the list into a rounding error inside "bot traffic".
const (
	PurposeTraining = "training"
	PurposeSearch   = "search"
	PurposeUser     = "user"
)

// crawlerDef identifies one AI fetcher. Matched as a lowercase substring of the UA,
// longest pattern first, so "chatgpt-user" wins over a looser "gpt" style rule.
type crawlerDef struct {
	pattern  string
	Name     string
	Operator string
	Purpose  string
}

// Curated from the operators' own published crawler docs. List-based forever, same
// rule as internal/botua: predictable beats clever.
var defs = []crawlerDef{
	{"chatgpt-user", "ChatGPT-User", "OpenAI", PurposeUser},
	{"oai-searchbot", "OAI-SearchBot", "OpenAI", PurposeSearch},
	{"gptbot", "GPTBot", "OpenAI", PurposeTraining},
	{"claude-user", "Claude-User", "Anthropic", PurposeUser},
	{"claude-searchbot", "Claude-SearchBot", "Anthropic", PurposeSearch},
	{"claudebot", "ClaudeBot", "Anthropic", PurposeTraining},
	{"claude-web", "Claude-Web", "Anthropic", PurposeSearch},
	{"anthropic-ai", "Anthropic-AI", "Anthropic", PurposeTraining},
	{"perplexity-user", "Perplexity-User", "Perplexity", PurposeUser},
	{"perplexitybot", "PerplexityBot", "Perplexity", PurposeSearch},
	{"google-extended", "Google-Extended", "Google", PurposeTraining},
	{"bingbot", "Bingbot", "Microsoft", PurposeSearch},
	{"applebot-extended", "Applebot-Extended", "Apple", PurposeTraining},
	{"applebot", "Applebot", "Apple", PurposeSearch},
	{"meta-externalagent", "Meta-ExternalAgent", "Meta", PurposeTraining},
	{"bytespider", "Bytespider", "ByteDance", PurposeTraining},
	{"amazonbot", "Amazonbot", "Amazon", PurposeSearch},
	{"ccbot", "CCBot", "Common Crawl", PurposeTraining},
	{"cohere-ai", "Cohere-AI", "Cohere", PurposeTraining},
	{"mistralai-user", "MistralAI-User", "Mistral", PurposeUser},
	{"diffbot", "Diffbot", "Diffbot", PurposeTraining},
	{"timpibot", "Timpibot", "Timpi", PurposeTraining},
	{"youbot", "YouBot", "You.com", PurposeSearch},
	{"duckassistbot", "DuckAssistBot", "DuckDuckGo", PurposeSearch},
}

func init() {
	// longest pattern first so a specific crawler is never shadowed by a shorter,
	// looser rule that happens to sit above it (claudebot vs claude-searchbot)
	sort.SliceStable(defs, func(i, j int) bool { return len(defs[i].pattern) > len(defs[j].pattern) })
}

// Identify classifies a user agent. ok is false for anything that isn't a known AI
// fetcher — including Googlebot, which is a search crawler people already have tools
// for and would only dilute this report.
func Identify(ua string) (name, operator, purpose string, ok bool) {
	l := strings.ToLower(ua)
	for _, d := range defs {
		if strings.Contains(l, d.pattern) {
			return d.Name, d.Operator, d.Purpose, true
		}
	}
	return "", "", "", false
}

// Known reports whether ua is a recognised AI crawler.
func Known(ua string) bool { _, _, _, ok := Identify(ua); return ok }

// CrawlerRow is one crawler's activity over the window.
type CrawlerRow struct {
	Crawler  string `json:"crawler"`
	Operator string `json:"operator"`
	Purpose  string `json:"purpose"`
	Hits     int    `json:"hits"`
	Pages    int    `json:"pages"`     // distinct paths it fetched
	Errors   int    `json:"errors"`    // hits it received a 4xx/5xx for
	LastAt   string `json:"last_at"`   // RFC3339, "" if never
	LastPath string `json:"last_path"` // the newest path it fetched
}

// PathRow is one page's crawl coverage. Crawlers is the count of DISTINCT crawlers
// that fetched it, which is the number that matters: a page every operator has read
// is a page that can be cited by every assistant.
type PathRow struct {
	Path     string `json:"path"`
	Hits     int    `json:"hits"`
	Crawlers int    `json:"crawlers"`
	Errors   int    `json:"errors"`
	LastAt   string `json:"last_at"`
}

// OperatorRow rolls the crawlers up to the company behind them — the level a reader
// actually thinks in ("has OpenAI read my site", not "has OAI-SearchBot").
type OperatorRow struct {
	Operator string `json:"operator"`
	Hits     int    `json:"hits"`
	Pages    int    `json:"pages"`
	Training int    `json:"training"`
	Search   int    `json:"search"`
	User     int    `json:"user"`
	LastAt   string `json:"last_at"`
}

// DayPoint is one day of the crawl trend, split by purpose so a training sweep never
// hides the fact that live user fetches went to zero.
type DayPoint struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Hits     int    `json:"hits"`
	Training int    `json:"training"`
	Search   int    `json:"search"`
	User     int    `json:"user"`
}

// GapRow is a page real visitors read that no AI crawler has ever fetched. The most
// actionable row in the module: it is a specific URL, it has proven human demand, and
// no assistant can cite what it has never read.
type GapRow struct {
	Path     string `json:"path"`
	Views    int    `json:"views"` // human pageviews over the window
	Visitors int    `json:"visitors"`
}

// Result is the whole report. Empty is a legitimate answer and Reporting says so —
// "no crawler has ever fetched this site" and "nobody has installed the reporter"
// look identical in the data and mean opposite things, so they are never conflated.
type Result struct {
	Days      int  `json:"days"`
	Reporting bool `json:"reporting"` // any $ai_crawl event ever seen
	Hits      int  `json:"hits"`
	Pages     int  `json:"pages"`
	// Probes are requests dropped as vulnerability scans wearing a bot user-agent (/.env,
	// /@fs/etc/passwd and friends). Reported rather than silently discarded: an operator whose
	// total quietly shrank is a number nobody can reconcile, and "we ignored 11 fake requests" is
	// a more trustworthy line than a total that happens to be right.
	Probes    int           `json:"probes"`
	Crawlers  []CrawlerRow  `json:"crawlers"`
	Operators []OperatorRow `json:"operators"`
	Paths     []PathRow     `json:"paths"`
	Trend     []DayPoint    `json:"trend"`
	Gaps      []GapRow      `json:"gaps"`
	Training  int           `json:"training"`
	Search    int           `json:"search"`
	User      int           `json:"user"`
	Errors    int           `json:"errors"`
	FirstAt   string        `json:"first_at"`
	LastAt    string        `json:"last_at"`
	Note      string        `json:"note,omitempty"`
}

// maxRows caps every table. A crawl log is long and this is a report, not a log viewer;
// the tail is available through /v1/events like any other event.
const maxRows = 25

// Compute aggregates the window ending at asof. Human pageviews are read from the same
// slice to find the coverage gaps, so the caller passes ALL events, not a filtered set.
func Compute(evs []event.Event, days int, asof time.Time) Result {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	if days <= 0 {
		days = 30
	}
	r := Result{Days: days, Crawlers: []CrawlerRow{}, Operators: []OperatorRow{}, Paths: []PathRow{}, Trend: []DayPoint{}, Gaps: []GapRow{}}
	// ONE window, in calendar days, used by both the totals and the chart.
	//
	// The first version cut the totals on a rolling instant (asof minus days*24h) while
	// bucketing the chart by date. Those are different windows: a hit in the overlap was
	// counted in every total and drawn in no bar, so the chart disagreed with the headline
	// printed directly above it. Calendar days are also what a bar chart means — each bar
	// IS a date — and they make the trend exactly `days` long instead of a ragged days+1
	// whose first bar is a partial day nobody can interpret.
	lastDay := asof.UTC().Truncate(24 * time.Hour)
	firstDay := lastDay.AddDate(0, 0, -(days - 1))
	cut := firstDay

	type crawlAgg struct {
		row      CrawlerRow
		paths    map[string]bool
		lastTime time.Time
	}
	type pathAgg struct {
		row      PathRow
		crawlers map[string]bool
		lastTime time.Time
	}
	type opAgg struct {
		row      OperatorRow
		paths    map[string]bool
		lastTime time.Time
	}
	crawlers := map[string]*crawlAgg{}
	paths := map[string]*pathAgg{}
	ops := map[string]*opAgg{}
	byDay := map[string]*DayPoint{}
	var first, last time.Time

	// human traffic, for the coverage gap. Counted over the same window so a page that
	// stopped getting visitors months ago doesn't demand crawler attention today.
	views := map[string]int{}
	viewers := map[string]map[string]bool{}
	// robots.txt disallow prefixes, off the newest site scan
	var blocked []string
	var blockedAt time.Time

	for _, e := range evs {
		if e.Timestamp.After(asof) {
			continue
		}
		switch e.Name {
		case CrawlEvent:
			r.Reporting = true
			if first.IsZero() || e.Timestamp.Before(first) {
				first = e.Timestamp
			}
			if e.Timestamp.After(last) {
				last = e.Timestamp
			}
			if e.Timestamp.Before(cut) {
				continue
			}
			name := asStr(e.Properties["crawler"])
			if name == "" {
				continue
			}
			op := asStr(e.Properties["operator"])
			if op == "" {
				op = "unknown"
			}
			purpose := asStr(e.Properties["purpose"])
			path := normPath(asStr(e.Properties["path"]))
			// A scanner wearing a bot's user-agent is not that bot. Dropped before counting, so it
			// cannot inflate an operator's total or become that operator's "last page read".
			if probePath(path) {
				r.Probes++
				continue
			}
			status := int(asNum(e.Properties["status"]))
			bad := status >= 400

			r.Hits++
			if bad {
				r.Errors++
			}
			switch purpose {
			case PurposeTraining:
				r.Training++
			case PurposeSearch:
				r.Search++
			case PurposeUser:
				r.User++
			}

			c := crawlers[name]
			if c == nil {
				c = &crawlAgg{row: CrawlerRow{Crawler: name, Operator: op, Purpose: purpose}, paths: map[string]bool{}}
				crawlers[name] = c
			}
			c.row.Hits++
			if bad {
				c.row.Errors++
			}
			c.paths[path] = true
			if e.Timestamp.After(c.lastTime) {
				c.lastTime, c.row.LastAt, c.row.LastPath = e.Timestamp, e.Timestamp.UTC().Format(time.RFC3339), path
			}

			o := ops[op]
			if o == nil {
				o = &opAgg{row: OperatorRow{Operator: op}, paths: map[string]bool{}}
				ops[op] = o
			}
			o.row.Hits++
			o.paths[path] = true
			switch purpose {
			case PurposeTraining:
				o.row.Training++
			case PurposeSearch:
				o.row.Search++
			case PurposeUser:
				o.row.User++
			}
			if e.Timestamp.After(o.lastTime) {
				o.lastTime, o.row.LastAt = e.Timestamp, e.Timestamp.UTC().Format(time.RFC3339)
			}

			p := paths[path]
			if p == nil {
				p = &pathAgg{row: PathRow{Path: path}, crawlers: map[string]bool{}}
				paths[path] = p
			}
			p.row.Hits++
			if bad {
				p.row.Errors++
			}
			p.crawlers[name] = true
			if e.Timestamp.After(p.lastTime) {
				p.lastTime, p.row.LastAt = e.Timestamp, e.Timestamp.UTC().Format(time.RFC3339)
			}

			day := e.Timestamp.UTC().Format("2006-01-02")
			d := byDay[day]
			if d == nil {
				d = &DayPoint{Day: day}
				byDay[day] = d
			}
			d.Hits++
			switch purpose {
			case PurposeTraining:
				d.Training++
			case PurposeSearch:
				d.Search++
			case PurposeUser:
				d.User++
			}

		case readableEvent:
			if e.Timestamp.Before(blockedAt) {
				continue
			}
			blockedAt = e.Timestamp
			blocked = blocked[:0]
			for _, p := range strings.Split(asStr(e.Properties["robots_disallow"]), ",") {
				if p = strings.TrimSpace(p); p != "" {
					blocked = append(blocked, p)
				}
			}

		case "$pageview":
			if e.Timestamp.Before(cut) {
				continue
			}
			raw := pageviewPath(e)
			if raw == "" {
				continue // no location on the event: unknowable, not the homepage
			}
			path := normPath(raw)
			views[path]++
			if viewers[path] == nil {
				viewers[path] = map[string]bool{}
			}
			viewers[path][e.DistinctID] = true
		}
	}

	if !first.IsZero() {
		r.FirstAt = first.UTC().Format(time.RFC3339)
	}
	if !last.IsZero() {
		r.LastAt = last.UTC().Format(time.RFC3339)
	}
	r.Pages = len(paths)

	for _, c := range crawlers {
		c.row.Pages = len(c.paths)
		r.Crawlers = append(r.Crawlers, c.row)
	}
	sort.Slice(r.Crawlers, func(i, j int) bool {
		if r.Crawlers[i].Hits != r.Crawlers[j].Hits {
			return r.Crawlers[i].Hits > r.Crawlers[j].Hits
		}
		return r.Crawlers[i].Crawler < r.Crawlers[j].Crawler
	})

	for _, o := range ops {
		o.row.Pages = len(o.paths)
		r.Operators = append(r.Operators, o.row)
	}
	sort.Slice(r.Operators, func(i, j int) bool {
		if r.Operators[i].Hits != r.Operators[j].Hits {
			return r.Operators[i].Hits > r.Operators[j].Hits
		}
		return r.Operators[i].Operator < r.Operators[j].Operator
	})

	for _, p := range paths {
		p.row.Crawlers = len(p.crawlers)
		r.Paths = append(r.Paths, p.row)
	}
	sort.Slice(r.Paths, func(i, j int) bool {
		if r.Paths[i].Hits != r.Paths[j].Hits {
			return r.Paths[i].Hits > r.Paths[j].Hits
		}
		return r.Paths[i].Path < r.Paths[j].Path
	})
	if len(r.Paths) > maxRows {
		r.Paths = r.Paths[:maxRows]
	}

	// the gap: pages humans read that no crawler has fetched. Only meaningful once
	// something is crawling — with no reporter installed EVERY page is "uncrawled",
	// which would be a list of lies.
	if r.Reporting && r.Hits > 0 {
		for path, n := range views {
			if paths[path] != nil {
				continue
			}
			// A page the site's own robots.txt keeps crawlers out of is not a coverage gap,
			// it is compliance. Without this the report led with /dashboard and /projects/…
			// and told the reader to put their logged-in app routes in a sitemap, which is
			// wrong advice and a privacy problem in the same sentence.
			if disallowed(path, blocked) || privateRoute(path) {
				continue
			}
			// One person reloading their own page is not proven demand. The rest of this
			// module refuses to speak on thin samples; the gap list has to as well, or it
			// fills with private pages that happen to be viewed a lot by one account.
			if len(viewers[path]) < 2 {
				continue
			}
			r.Gaps = append(r.Gaps, GapRow{Path: path, Views: n, Visitors: len(viewers[path])})
		}
		sort.Slice(r.Gaps, func(i, j int) bool {
			if r.Gaps[i].Views != r.Gaps[j].Views {
				return r.Gaps[i].Views > r.Gaps[j].Views
			}
			return r.Gaps[i].Path < r.Gaps[j].Path
		})
		if len(r.Gaps) > maxRows {
			r.Gaps = r.Gaps[:maxRows]
		}
	}

	// dense trend: every day in the window, including the zeros. A sparse series would
	// draw a straight line across a week of silence, which is the exact thing the reader
	// is looking for.
	for d := firstDay; !d.After(lastDay); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		if d := byDay[day]; d != nil {
			r.Trend = append(r.Trend, *d)
		} else {
			r.Trend = append(r.Trend, DayPoint{Day: day})
		}
	}

	if r.Reporting && r.Hits == 0 {
		r.Note = "reporting is installed but no AI crawler has fetched this site in the window"
	}
	return r
}

// normPath keeps paths comparable: query and fragment stripped, trailing slash dropped
// (except root), empty defaults to "/". Without this "/pricing" and "/pricing?ref=x" are
// two pages and every coverage number is wrong.
func normPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		// a full URL: keep everything from the first slash after the host
		if i := strings.Index(p, "://"); i >= 0 {
			rest := p[i+3:]
			if j := strings.Index(rest, "/"); j >= 0 {
				p = rest[j:]
			} else {
				p = "/"
			}
		} else {
			p = "/" + p
		}
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	return p
}

// pageviewPath reads the path off an autocaptured pageview, which carries `path` in
// some SDK versions and a full `url` in others.
func pageviewPath(e event.Event) string {
	if p := asStr(e.Properties["path"]); p != "" {
		return p
	}
	return asStr(e.Properties["url"])
}

func asStr(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asNum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// disallowed reports whether path sits under one of robots.txt's Disallow prefixes. Prefix
// match, because that is what a robots rule means: "Disallow: /projects" covers every page
// beneath it.
// privateRoute reports whether a path is almost certainly behind a login.
//
// robots.txt is not enough on its own. Hardly anyone disallows their own dashboard — there is no
// reason to, since it is unreachable without a session — so a signed-in route sails past the
// robots check and lands at the top of a report telling the owner to put it in their sitemap.
// That happened on our own instance: "No AI crawler has read /dashboard … list it in your
// sitemap", which is wrong advice and a privacy problem in one sentence.
//
// Matched on the FIRST segment only. A blog post at /app-store-analytics is public and must not
// be suppressed just because it starts with the letters "app"; /app/settings is not.
// probePath reports whether a request is a vulnerability scanner rather than a crawl.
//
// Measured on a live instance: ClaudeBot's most recent request was /@fs/etc/passwd and CCBot's was
// /.aws/config. Neither Anthropic nor Common Crawl fetches those. Something is sending a bot
// user-agent while scanning for secrets, and counting it means the pane reports "Anthropic read
// your site" about a request Anthropic never made.
//
// That is a wrong number of exactly the kind this product exists not to ship, and it is worse than
// most because it is attributed to a named company. A user-agent header is a claim, not an
// identity, and the paths are the cheapest available lie detector.
func probePath(path string) bool {
	p := strings.ToLower(path)
	for _, frag := range []string{
		"/.env", "/.git", "/.aws", "/.ssh", "/@fs/", "/etc/passwd", "/wp-admin", "/wp-login",
		"/phpmyadmin", "/.well-known/security", "/config.json", "/credentials", "/id_rsa",
		"/actuator", "/.svn", "/vendor/phpunit", "/xmlrpc.php", "/shell", "/cgi-bin/",
	} {
		if strings.Contains(p, frag) {
			return true
		}
	}
	return false
}

func privateRoute(path string) bool {
	seg := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	switch strings.ToLower(seg) {
	case "dashboard", "app", "admin", "account", "settings", "billing", "profile",
		"projects", "project", "workspace", "team", "teams", "org", "orgs",
		"onboarding", "logout", "signout", "auth", "internal", "portal", "console":
		return true
	}
	return false
}

func disallowed(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
