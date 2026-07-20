package api

// ask_scope.go — the segment + comparison layer of the ask engine.
//
// The ask battery (188 natural PM questions, adversarially judged against the /v1
// reports) failed 108. Nearly all failures shared three roots: questions that pin a
// metric to a SEGMENT ("traffic from reddit", "visitors from india", "ios signups")
// answered with the unfiltered total; questions that COMPARE two windows ("this week
// vs last week", "did traffic grow") answered with a single period; and questions
// that compare two SEGMENTS ("android vs ios", "safari vs chrome") refused. This
// file is the shared machinery that answers all three shapes from the same
// deterministic counts the dashboard uses — values are validated against the events
// actually sent, and a named segment with no data answers "0" honestly instead of
// falling through to a site-wide number.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
)

// answerPaths answers "what do users do after signup" from the paths (user-journey)
// report — the SAME paths.After the MCP paths tool and the dashboard user-journeys card
// use, so the three surfaces agree. The anchor is the event named after "after", else the
// conventional conversion, else the highest-volume event.
func answerPaths(evs []event.Event, q string, vol []string, win askWindow) string {
	// honor the asked window — the journey used to run over ALL history while the provenance
	// line still claimed the requested window ("...in the last 24 hours"), a false-provenance
	// covenant break. Scope first so the answer and its receipt describe the same events.
	evs = scope(evs, win)
	start := ""
	if i := strings.Index(q, "after "); i >= 0 {
		// match the anchor as a WHOLE WORD, not a substring — "after purchase" used to
		// silently anchor on ANY event whose name is a substring of the phrase, so a
		// nonexistent "purchase" confidently returned some other event's journey.
		afterToks := askTokens(strings.ToLower(q[i+6:]))
		tokSet := map[string]bool{}
		for _, t := range afterToks {
			tokSet[t] = true
		}
		for _, name := range vol {
			ln := strings.ToLower(name)
			if tokSet[ln] || tokSet[strings.ReplaceAll(ln, "_", "")] {
				start = name
				break
			}
		}
		// the user explicitly named an anchor after "after" but it matches no tracked event:
		// say so with the real list instead of silently tracing a different event's journey.
		if start == "" && len(afterToks) > 0 {
			cand := afterToks[0]
			if len(cand) >= 3 && !segStopwords[cand] {
				return fmt.Sprintf("No event named %q to trace a journey from — tracked events are: %s.", cand, strings.Join(vol, ", "))
			}
		}
	}
	if start == "" {
		start = pickConversion(evs, vol)
	}
	if start == "" && len(vol) > 0 {
		start = vol[0]
	}
	if start == "" {
		return "No events to trace a journey from yet — send some custom events first."
	}
	pr := paths.After(evs, start, 3)
	if pr.Users == 0 {
		return fmt.Sprintf("No users have a %q event to trace forward from.", start)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "After %q (%d users), the most common next steps:", start, pr.Users)
	any := false
	for _, lvl := range pr.Levels {
		if len(lvl.Steps) == 0 {
			continue
		}
		any = true
		parts := make([]string, 0, 3)
		for i, s := range lvl.Steps {
			if i >= 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%d, %d%%)", s.Event, s.Count,
				int(float64(s.Count)/float64(pr.Users)*100+0.5)))
		}
		fmt.Fprintf(&b, "\n  next: %s", strings.Join(parts, " · "))
	}
	if !any {
		return fmt.Sprintf("%d users did %q, but nothing tracked after it yet — add events for the next steps to see the journey.", pr.Users, start)
	}
	return b.String()
}

// askSeg is one extracted segment: a real property/value pair plus the human label
// used in the answer ("from reddit.com", "on iOS", "from India").
type askSeg struct {
	prop     string
	value    string // the value as it appears in the DATA (correct casing)
	label    string
	found    bool     // value exists in the events; false = honest-zero answer
	orUTM    string   // twitter special case: also count utm_source=<orUTM>
	altHosts []string // extra referrer hosts to match (e.g. twitter = twitter.com|t.co|x.com)
}

// segMatch is an alias table row: any of words in the question maps to prop=value.
type segMatch struct {
	words []string
	prop  string
	value string
	label string
}

var segAliases = []segMatch{
	// referrer hosts — stored as URLs, matched by host substring
	{[]string{"reddit"}, "referrer", "reddit.com", "reddit"},
	{[]string{"hacker news", "hackernews", "ycombinator"}, "referrer", "news.ycombinator.com", "hacker news"},
	{[]string{"chatgpt", "chat gpt"}, "referrer", "chatgpt.com", "chatgpt"},
	{[]string{"claude"}, "referrer", "claude.ai", "claude"},
	{[]string{"perplexity"}, "referrer", "perplexity.ai", "perplexity"},
	{[]string{"google"}, "referrer", "google.com", "google"},
	{[]string{"bing"}, "referrer", "bing.com", "bing"},
	{[]string{"tiktok"}, "referrer", "tiktok.com", "tiktok"},
	{[]string{"facebook"}, "referrer", "facebook.com", "facebook"},
	{[]string{"linkedin"}, "referrer", "linkedin.com", "linkedin"},
	{[]string{"youtube"}, "referrer", "youtube.com", "youtube"},
	{[]string{"instagram"}, "referrer", "instagram.com", "instagram"},
	// twitter lives in two places: t.co referrers and utm_source=twitter; count either
	{[]string{"twitter", "t.co", "x.com"}, "referrer", "twitter.com", "twitter"},
	// devices / browsers / OS — validated against data casing below
	{[]string{"mobile", "phone"}, "device", "mobile", "mobile"},
	{[]string{"desktop"}, "device", "desktop", "desktop"},
	{[]string{"chrome"}, "browser", "chrome", "Chrome"},
	{[]string{"safari"}, "browser", "safari", "Safari"},
	{[]string{"firefox"}, "browser", "firefox", "Firefox"},
	{[]string{"edge"}, "browser", "edge", "Edge"},
	{[]string{"ios", "iphone", "ipad"}, "os", "ios", "iOS"},
	{[]string{"android"}, "os", "android", "Android"},
	{[]string{"windows"}, "os", "windows", "Windows"},
	{[]string{"macos", "mac os", " mac "}, "os", "macos", "macOS"},
	{[]string{"linux"}, "os", "linux", "Linux"},
	// utm
	{[]string{"cpc", "paid ads", "the ads", "ad spend"}, "utm_medium", "cpc", "cpc ads"},
	{[]string{"social traffic", "social media"}, "utm_medium", "social", "social"},
	{[]string{"launch campaign", "campaign launch"}, "utm_campaign", "launch", "the launch campaign"},
}

// countryNames maps the names people type to ISO codes (how country is stored).
var countryNames = []segMatch{
	{[]string{"india", "indian"}, "country", "IN", "India"},
	{[]string{"united states", " usa ", "the us ", "from us ", "in the us", "us traffic", "us visitors", "us users", "america"}, "country", "US", "the US"},
	{[]string{"united kingdom", " uk ", "britain", "england"}, "country", "GB", "the UK"},
	{[]string{"germany", "german"}, "country", "DE", "Germany"},
	{[]string{"france", "french"}, "country", "FR", "France"},
	{[]string{"brazil"}, "country", "BR", "Brazil"},
	{[]string{"netherlands", "holland"}, "country", "NL", "the Netherlands"},
	{[]string{"canada", "canadian"}, "country", "CA", "Canada"},
	{[]string{"japan"}, "country", "JP", "Japan"},
	{[]string{"australia"}, "country", "AU", "Australia"},
	{[]string{"spain"}, "country", "ES", "Spain"},
	{[]string{"italy"}, "country", "IT", "Italy"},
}

// europeCodes is the country-set behind "traffic from europe".
var europeCodes = map[string]bool{"GB": true, "DE": true, "FR": true, "NL": true, "ES": true,
	"IT": true, "SE": true, "NO": true, "DK": true, "FI": true, "PL": true, "PT": true,
	"IE": true, "BE": true, "AT": true, "CH": true, "CZ": true}

// realValue finds the value of prop in the data matching wanted case-insensitively
// (Chrome vs chrome), so filters always compare against what was actually sent.
func realValue(evs []event.Event, prop, wanted string) (string, bool) {
	w := strings.ToLower(wanted)
	for _, e := range evs {
		v, ok := e.Properties[prop]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", v)
		ls := strings.ToLower(s)
		if ls == w || (prop == "referrer" && hostEquals(hostOf(ls), hostOf(w))) {
			return s, true
		}
	}
	return wanted, false
}

// extractSegments pulls up to two real segments out of the question, in the order
// they appear, so "android vs ios" compares in the asked order.
func extractSegments(q string, evs []event.Event) []askSeg {
	padded := " " + q + " "
	type hit struct {
		pos int
		seg askSeg
	}
	var hits []hit
	seen := map[string]bool{}
	tables := append(append([]segMatch{}, segAliases...), countryNames...)
	for _, a := range tables {
		for _, w := range a.words {
			pos := strings.Index(padded, w)
			if pos < 0 {
				continue
			}
			key := a.prop + "=" + a.value
			if seen[key] {
				continue
			}
			seen[key] = true
			val, found := a.value, true
			if a.prop == "referrer" {
				val, found = realValue(evs, "referrer", a.value)
			} else {
				val, found = realValue(evs, a.prop, a.value)
			}
			s := askSeg{prop: a.prop, value: val, label: a.label, found: found}
			if a.label == "twitter" {
				s.orUTM = "twitter"
				s.altHosts = []string{"twitter.com", "t.co", "x.com"}
				for _, h := range s.altHosts {
					if rv, ok := realValue(evs, "referrer", h); ok {
						s.value, s.found = rv, true
						break
					}
				}
			}
			// referrer aliases fall back to the `source`/`utm_source` PROPERTY when the host
			// (and, for twitter, its utm/altHost union) isn't in the data: many products track
			// acquisition as source=twitter, not a t.co referrer. Without this, "signups from
			// twitter" answered 0 while the source property held 28. Runs AFTER the twitter
			// union check so a real t.co/utm dataset keeps its referrer-based resolution.
			if !s.found && a.prop == "referrer" {
				for _, srcProp := range []string{"source", "utm_source", "channel"} {
					if rv, ok := realValue(evs, srcProp, a.label); ok {
						s = askSeg{prop: srcProp, value: rv, label: a.label, found: true}
						break
					}
				}
			}
			hits = append(hits, hit{pos, s})
			break
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	var out []askSeg
	for _, h := range hits {
		out = append(out, h.seg)
		if len(out) == 2 {
			break
		}
	}
	// path words ("homepage pageviews", "how many people hit the blog") become path
	// segments, validated against the paths actually tracked — an untracked path
	// answers 0 honestly instead of the site-wide total
	if len(out) < 2 {
		for _, pa := range []segMatch{
			{[]string{"homepage", "home page", "landing page"}, "path", "/", "the homepage"},
			{[]string{"pricing"}, "path", "/pricing", "/pricing"},
			{[]string{"blog"}, "path", "/blog", "/blog"},
			{[]string{"docs", "documentation"}, "path", "/docs", "/docs"},
		} {
			for _, w := range pa.words {
				if strings.Contains(padded, w) {
					val, found := realValue(evs, "path", pa.value)
					out = append(out, askSeg{prop: "path", value: val, label: pa.label, found: found})
					break
				}
			}
			if len(out) >= 2 {
				break
			}
		}
	}
	// "traffic from europe" — a country-set, handled as its own pseudo-segment
	if len(out) == 0 && strings.Contains(q, "europe") {
		out = append(out, askSeg{prop: "country", value: "__europe__", label: "Europe", found: true})
	}
	// generic property=value qualifiers the alias tables miss ("pro signups", "enterprise
	// plan") — resolved against real, low-cardinality custom property values in the data.
	// Without this the ask bar SILENTLY DROPS the qualifier and returns the UNFILTERED number.
	if len(out) < 2 {
		for _, s := range genericSegments(q, evs, out) {
			out = append(out, s)
			if len(out) >= 2 {
				break
			}
		}
	}
	// unresolved explicit qualifier: "from X" / "where <prop> is X" naming a value that exists
	// NOWHERE in the data. Returning the UNFILTERED total for it (stamped "cannot be fabricated")
	// is the worst kind of wrong answer, so emit a found=false segment — the answer path then
	// says "0 — no events with X" honestly instead of the whole-dataset number.
	if len(out) == 0 {
		if tok := unresolvedQualifier(q, evs); tok != "" {
			out = append(out, askSeg{prop: "that segment", value: tok, label: tok, found: false})
		}
	}
	return out
}

// unresolvedQualifier extracts an explicit segment token ("from twitter", "where source is
// hn") that names a value present on NO event, so the caller can answer an honest 0 instead
// of the unfiltered total. Returns "" when there's no such dangling qualifier.
func unresolvedQualifier(q string, evs []event.Event) string {
	ql := strings.ToLower(q)
	var tok string
	if i := strings.Index(ql, " where "); i >= 0 {
		// "... where source is enterprise" / "... where plan = pro"
		rest := ql[i+7:]
		rest = strings.NewReplacer(" is ", " ", " = ", " ", "=", " ").Replace(rest)
		f := strings.Fields(rest)
		if len(f) >= 2 {
			tok = f[len(f)-1]
		}
	} else if i := strings.Index(ql, " from "); i >= 0 {
		f := strings.Fields(ql[i+6:])
		if len(f) >= 1 {
			tok = f[0]
		}
	}
	tok = strings.Trim(tok, "?.,!\"'")
	if len(tok) < 2 || segStopwords[tok] {
		return ""
	}
	// a numeric/date token after "from" is a date range ("from 18 to 19"), not a segment
	if tok[0] >= '0' && tok[0] <= '9' {
		return ""
	}
	// time/geo/date words after "from" are not segments ("from yesterday", "from june 18",
	// "from europe", "from india") — never treat these as a dangling segment qualifier.
	switch tok {
	case "yesterday", "today", "europe", "last", "the", "this", "our", "all", "now",
		"january", "february", "march", "april", "may", "june", "july", "august",
		"september", "october", "november", "december", "monday", "tuesday", "wednesday",
		"thursday", "friday", "saturday", "sunday", "jan", "feb", "mar", "apr", "jun",
		"jul", "aug", "sep", "sept", "oct", "nov", "dec":
		return ""
	}
	// does the token exist as ANY string property value, or a referrer host? if so it WAS
	// resolvable — not a dangling qualifier (don't shadow a real segment).
	for _, e := range evs {
		for _, v := range e.Properties {
			if sv, ok := v.(string); ok {
				lv := strings.ToLower(sv)
				if lv == tok || strings.Contains(lv, tok) {
					return ""
				}
			}
		}
	}
	return tok
}

// breakdownByProp detects a "break down <event> by <prop>" / "<event> by <prop>" / "grouped
// by <prop>" request and returns the property name IF it is a real property present in the
// data. Conversion questions ("which browser converts") own the "by <dim>" phrasing and are
// left to the conversion-by-dimension route, so this returns "" for them.
func breakdownByProp(q string, evs []event.Event) string {
	ql := strings.ToLower(q)
	if hasAny(ql, "convert", "conversion", "converts") {
		return ""
	}
	if !hasAny(ql, " by ", "grouped by", "broken down by", "break down", "breakdown", "group by") {
		return ""
	}
	toks := askTokens(ql)
	present := func(cand string) bool {
		for _, e := range evs {
			if _, ok := e.Properties[cand]; ok {
				return true
			}
		}
		return false
	}
	for i := 0; i+1 < len(toks); i++ {
		if toks[i] == "by" {
			if cand := toks[i+1]; present(cand) {
				return cand
			}
		}
	}
	return ""
}

// segStopwords are question-structure words the generic value matcher must never treat as a
// property value, so "new users today" can't become a bogus segment.
var segStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "all": true, "how": true, "many": true, "who": true,
	"did": true, "are": true, "was": true, "our": true, "per": true, "day": true, "week": true,
	"month": true, "year": true, "today": true, "now": true, "this": true, "last": true,
	"past": true, "over": true, "from": true, "with": true, "count": true, "total": true,
	"number": true, "much": true, "have": true, "get": true, "vs": true, "versus": true,
	"each": true, "any": true, "more": true, "most": true, "users": true, "user": true,
	"event": true, "events": true, "people": true, "visitors": true, "signup": true, "signups": true,
}

// askTokens splits a question into lowercase ASCII word tokens (enough for property values
// like "pro"/"free"/"enterprise"; avoids a unicode dependency).
func askTokens(q string) []string {
	return strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// genericSegments resolves qualifiers the alias tables miss: a token like "pro" that is a
// real value of a low-cardinality custom property (plan=pro). It scans the ACTUAL event
// property values so the ask bar filters by exactly what the user named instead of silently
// dropping it and returning the unfiltered total as if it were the answer. Reserved props
// (referrer/device/os/…) already have dedicated alias handling and are skipped here.
func genericSegments(q string, evs []event.Event, existing []askSeg) []askSeg {
	skip := map[string]bool{
		"referrer": true, "device": true, "browser": true, "os": true, "country": true,
		"path": true, "utm_source": true, "utm_medium": true, "utm_campaign": true,
	}
	for _, s := range existing {
		skip[s.prop] = true
	}
	distinct := map[string]map[string]bool{}
	for _, e := range evs {
		for k, v := range e.Properties {
			if skip[k] {
				continue
			}
			sv, ok := v.(string)
			if !ok || sv == "" {
				continue
			}
			if distinct[k] == nil {
				distinct[k] = map[string]bool{}
			}
			distinct[k][sv] = true
		}
	}
	valProp := map[string]string{} // lower value -> prop
	valReal := map[string]string{} // lower value -> real-cased value
	ambiguous := map[string]bool{}
	for prop, vals := range distinct {
		if len(vals) < 1 || len(vals) > 15 {
			continue // high-cardinality (ids/urls/amounts) is never a segment dimension
		}
		for sv := range vals {
			lv := strings.ToLower(sv)
			if len(lv) < 3 || segStopwords[lv] {
				continue // too short/common to match safely
			}
			if p, dup := valProp[lv]; dup && p != prop {
				ambiguous[lv] = true // same token, two props — refuse to guess
				continue
			}
			valProp[lv], valReal[lv] = prop, sv
		}
	}
	// a comparison ("pro vs free", "compare pro and free") wants TWO values of the SAME
	// property; otherwise one segment per property (so "pro signups on desktop" = plan AND device).
	comparison := hasAny(strings.ToLower(q), " vs ", " versus ", "compare ", " compared to ", " against ")
	var out []askSeg
	for _, tok := range askTokens(q) {
		if ambiguous[tok] {
			continue
		}
		prop, ok := valProp[tok]
		if !ok || skip[prop] {
			continue
		}
		out = append(out, askSeg{prop: prop, value: valReal[tok], label: valReal[tok], found: true})
		// keep the property open for a second value only on a same-property comparison.
		if !(comparison && len(out) == 1) {
			skip[prop] = true
		}
		if len(out) >= 2 {
			break
		}
	}
	return out
}

// segFilter keeps events matching the segment (referrer by host-contains, everything
// else by exact value; twitter also matches utm_source).
func segFilter(evs []event.Event, s askSeg) []event.Event {
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if segMatches(e, s) {
			out = append(out, e)
		}
	}
	return out
}

func segMatches(e event.Event, s askSeg) bool {
	if s.value == "__europe__" {
		v, ok := e.Properties["country"]
		return ok && europeCodes[fmt.Sprintf("%v", v)]
	}
	if v, ok := e.Properties[s.prop]; ok {
		sv := fmt.Sprintf("%v", v)
		if s.prop == "referrer" {
			if hostEquals(hostOf(sv), hostOf(s.value)) {
				return true
			}
			for _, h := range s.altHosts {
				if hostEquals(hostOf(sv), hostOf(h)) {
					return true
				}
			}
		} else if strings.EqualFold(sv, s.value) {
			return true
		}
	}
	if s.orUTM != "" {
		if v, ok := e.Properties["utm_source"]; ok && strings.EqualFold(fmt.Sprintf("%v", v), s.orUTM) {
			return true
		}
	}
	return false
}

// hostEquals matches a host exactly or as a subdomain — never by substring
// (reddit.com literally contains "t.co").
func hostEquals(host, want string) bool {
	host, want = strings.ToLower(host), strings.ToLower(want)
	return host == want || strings.HasSuffix(host, "."+want)
}

func hostOf(v string) string {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimPrefix(v, "www.")
}

// ---- the metric: what is being counted -------------------------------------------

type askMetric struct {
	kind  string // "visitors" | "pageviews" | "event"
	event string // when kind == "event"
	label string
}

// resolveMetric decides what number the question wants. Visitors is the default for
// traffic/people phrasings; a named real event wins when the question says one.
func resolveMetric(q string, volAll []string) askMetric {
	if ev := namedEvent(q, volAll); ev != "" {
		return askMetric{kind: "event", event: ev, label: fmt.Sprintf("%q events", ev)}
	}
	if hasAny(q, "pageview", "page view", "views") {
		return askMetric{kind: "pageviews", label: "pageviews"}
	}
	return askMetric{kind: "visitors", label: "visitors"}
}

// metricCount computes the metric over the given events: visitors = distinct users
// on $pageview (falling back to all events when nothing is web-tracked), pageviews =
// $pageview count, event = event count. Same identities the dashboard counts.
func metricCount(evs []event.Event, m askMetric) int {
	switch m.kind {
	case "pageviews":
		n := 0
		for _, e := range evs {
			if e.Name == "$pageview" {
				n++
			}
		}
		return n
	case "event":
		n := 0
		for _, e := range evs {
			if e.Name == m.event {
				n++
			}
		}
		return n
	default: // visitors
		users := map[string]bool{}
		anyPV := false
		for _, e := range evs {
			if e.Name == "$pageview" {
				anyPV = true
				users[e.DistinctID] = true
			}
		}
		if !anyPV {
			for _, e := range evs {
				users[e.DistinctID] = true
			}
		}
		return len(users)
	}
}

// ---- window comparisons ------------------------------------------------------------

// parseCompare spots two-period questions and returns the paired windows. The prior
// window is the equal-length period immediately before, aligned the way people mean
// it (calendar week vs calendar week, month vs month, day vs day).
func parseCompare(q string, now time.Time) (cur, prior askWindow, ok bool) {
	const day = 24 * time.Hour
	today := now.Truncate(day)
	monday := today.AddDate(0, 0, -mondayOffset(today))
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	weekPair := func() (askWindow, askWindow) {
		return askWindow{from: monday, to: now, label: "this week (since Mon " + monday.Format("Jan 2") + ", UTC)"},
			askWindow{from: monday.AddDate(0, 0, -7), to: monday, label: "last week"}
	}
	monthPair := func() (askWindow, askWindow) {
		return askWindow{from: firstOfMonth, to: now, label: "this month (since " + firstOfMonth.Format("Jan 2") + ", UTC)"},
			askWindow{from: firstOfMonth.AddDate(0, -1, 0), to: firstOfMonth, label: "last month (" + firstOfMonth.AddDate(0, -1, 0).Format("January") + ")"}
	}
	dayPair := func() (askWindow, askWindow) {
		return askWindow{from: today.Add(-day), to: today, label: "yesterday (UTC)"},
			askWindow{from: today.Add(-2 * day), to: today.Add(-day), label: "the day before"}
	}
	todayPair := func() (askWindow, askWindow) {
		return askWindow{from: today, to: now, label: "today (UTC)"},
			askWindow{from: today.Add(-day), to: today, label: "yesterday"}
	}

	// explicit pairs first
	switch {
	case hasAny(q, "week over week", "week-over-week", "wow"):
		cur, prior = weekPair()
		return cur, prior, true
	case hasAny(q, "month over month", "month-over-month"):
		cur, prior = monthPair()
		return cur, prior, true
	case strings.Contains(q, "yesterday") && hasAny(q, "day before", "vs the day", "previous day"):
		cur, prior = dayPair()
		return cur, prior, true
	case strings.Contains(q, "today") && hasAny(q, "vs yesterday", "than yesterday", "compare", "compared"):
		cur, prior = todayPair()
		return cur, prior, true
	}

	compareWord := hasAny(q, " vs ", " versus ", "compare", "compared", "than last", "than the previous",
		"grow", "grew", "growing", "better or worse", "up or down", "more or less", "more or fewer",
		"did we get more", "did more", "getting better", "getting worse", "dying", "falling off", "tanking")
	if !compareWord {
		return cur, prior, false
	}
	switch {
	case strings.Contains(q, "last week") || strings.Contains(q, "this week"):
		cur, prior = weekPair()
		return cur, prior, true
	case strings.Contains(q, "last month") || strings.Contains(q, "this month"):
		cur, prior = monthPair()
		return cur, prior, true
	case strings.Contains(q, "yesterday"):
		cur, prior = todayPair()
		return cur, prior, true
	case hasAny(q, "grow", "grew", "growing", "better or worse", "up or down", "getting better", "getting worse", "dying", "falling off", "tanking"):
		// bare growth questions default to week-over-week — say so in the answer
		cur, prior = weekPair()
		return cur, prior, true
	}
	return cur, prior, false
}

// answerCompareWindows answers "metric this period vs last period" with both numbers
// and the direction — the shape every "did we grow" question wants.
func answerCompareWindows(evs []event.Event, m askMetric, segs []askSeg, cur, prior askWindow) string {
	scopeEvs := evs
	segNote := ""
	if len(segs) == 1 {
		if !segs[0].found {
			return fmt.Sprintf("No events from %s in the data at all, so there's nothing to compare — 0 in both periods.", segs[0].label)
		}
		scopeEvs = segFilter(evs, segs[0])
		segNote = " from " + segs[0].label
	}
	a := metricCount(scope(scopeEvs, cur), m)
	b := metricCount(scope(scopeEvs, prior), m)
	// P1-2: a brand-new account has no prior period — comparing "N vs 0 — up" is
	// noise. When the prior window is genuinely empty, say so instead of inventing a
	// direction and a divide-by-zero delta.
	if b == 0 {
		return fmt.Sprintf("%s%s %s: %d. No data yet for %s to compare against — the prior period is empty, so there's no trend to read until you have two periods of history.",
			title(m.label), segNote, cur.label, a, prior.label)
	}
	dir := "flat"
	if a > b {
		dir = "up"
	} else if a < b {
		dir = "down"
	}
	delta := fmt.Sprintf(" (%+d%%)", int(float64(a-b)/float64(b)*100+copysign(0.5, float64(a-b))))
	note := ""
	if strings.HasPrefix(cur.label, "this ") {
		note = " Note: the current period is still in progress."
	}
	return fmt.Sprintf("%s%s: %d %s vs %d %s — %s%s.%s",
		title(m.label), segNote, a, cur.label, b, prior.label, dir, delta, note)
}

func copysign(mag, sign float64) float64 {
	if sign < 0 {
		return -mag
	}
	return mag
}

// answerSegment answers a metric pinned to one segment ("traffic from reddit",
// "visitors from india this week", "ios signups") from the filtered count. A segment
// named but absent from the data answers 0 honestly.
func answerSegment(evs []event.Event, m askMetric, s askSeg, win askWindow) string {
	if !s.found {
		return fmt.Sprintf("0 — no events with %s = %s have been sent, so there's no %s from %s in the data. If that's unexpected, check the tracking on that channel.",
			s.prop, s.label, m.label, s.label)
	}
	// user-scope for acquisition/user attributes (referrer/source/utm/device/os/
	// browser/country): "reddit signups" = signups BY reddit-acquired users, since the
	// signup event itself carries no referrer. path/other props stay event-scoped.
	pool := segFilter(evs, s)
	if m.kind == "event" && userAttr(s.prop) {
		pool = segFilterUsers(evs, s) // "reddit signups": the signup event carries no referrer
	}
	scoped := scope(pool, win)
	n := metricCount(scoped, m)
	from := "from"
	if s.prop == "device" || s.prop == "browser" || s.prop == "os" {
		from = "on"
	}
	return fmt.Sprintf("%d %s %s %s%s.", n, m.label, from, s.label, winSuffix(win))
}

// userAttr reports whether a property describes the VISITOR (so a metric filtered by
// it must scope users, not events — the acquisition/device props aren't on every event).
func userAttr(prop string) bool {
	switch prop {
	case "referrer", "source", "utm_source", "utm_medium", "utm_campaign", "device", "os", "browser", "country":
		return true
	}
	return false
}

// answerSegVsSeg answers "X vs Y" over the same metric — both numbers, then the verdict.
func answerSegVsSeg(evs []event.Event, m askMetric, a, b askSeg, win askWindow) string {
	segPool := func(sg askSeg) []event.Event {
		if m.kind == "event" && userAttr(sg.prop) {
			return segFilterUsers(evs, sg)
		}
		return segFilter(evs, sg)
	}
	na, nb := 0, 0
	if a.found {
		na = metricCount(scope(segPool(a), win), m)
	}
	if b.found {
		nb = metricCount(scope(segPool(b), win), m)
	}
	verdict := fmt.Sprintf("%s leads", a.label)
	if nb > na {
		verdict = fmt.Sprintf("%s leads", b.label)
	} else if na == nb {
		verdict = "dead even"
	}
	return fmt.Sprintf("%s: %s %d vs %s %d%s — %s.", title(m.label), a.label, na, b.label, nb, winSuffix(win), verdict)
}

// answerSegAnd counts a metric filtered by TWO segments at once ("pro signups on desktop" =
// plan=pro AND device=desktop). Different-property segments without a comparison word mean
// intersection, not "X vs Y"; dropping either would return a materially wrong number as fact.
func answerSegAnd(evs []event.Event, m askMetric, a, b askSeg, win askWindow) string {
	for _, s := range []askSeg{a, b} {
		if !s.found {
			return fmt.Sprintf("0 — no events with %s = %s have been sent, so there's no %s for %s + %s.",
				s.prop, s.label, m.label, a.label, b.label)
		}
	}
	pool := evs
	for _, s := range []askSeg{a, b} {
		// user attributes (device/referrer/country/…) aren't on every event, so scope by USER;
		// product/custom props (plan) stay event-scoped. Applying both narrows to the intersection.
		if m.kind == "event" && userAttr(s.prop) {
			pool = segFilterUsers(pool, s)
		} else {
			pool = segFilter(pool, s)
		}
	}
	n := metricCount(scope(pool, win), m)
	return fmt.Sprintf("%d %s for %s + %s%s.", n, m.label, a.label, b.label, winSuffix(win))
}

// ---- new report answers -------------------------------------------------------------

// answerSources ranks where traffic comes from by VISITORS — the report "where is our
// traffic coming from" wants, including direct (the attribution report hid it).
// answerPropBreakdown ranks distinct users per value of one property — the report
// behind "utm medium breakdown".
func answerPropBreakdown(evs []event.Event, prop string, win askWindow) string {
	return answerPropBreakdownLabel(evs, prop, "visitors", win)
}

func answerPropBreakdownLabel(evs []event.Event, prop, unit string, win askWindow) string {
	return answerPropBreakdownWith(evs, prop, unit, win, false)
}

// answerPropBreakdownEvents is the EVENT-count variant: when the unit names events
// ("signups"), the numbers must be event counts — matching GET /v1/breakdown — not
// distinct users quietly reported under an event label.
func answerPropBreakdownEvents(evs []event.Event, prop, unit string, win askWindow) string {
	return answerPropBreakdownWith(evs, prop, unit, win, true)
}

func answerPropBreakdownWith(evs []event.Event, prop, unit string, win askWindow, countEvents bool) string {
	scoped := scope(evs, win)
	byVal := map[string]map[string]bool{}
	evCount := map[string]int{}
	any := false
	for _, e := range scoped {
		// events missing the property land in "(none)" — the same bucket GET /v1/breakdown,
		// MCP, and trends show. Skipping them silently under-reported the split.
		val := "(none)"
		if v, ok := e.Properties[prop]; ok {
			val = fmt.Sprintf("%v", v)
			if val == "" {
				val = "(empty)" // an explicit empty-string value, distinct from a missing property
			}
			any = true
		}
		if byVal[val] == nil {
			byVal[val] = map[string]bool{}
		}
		byVal[val][e.DistinctID] = true
		evCount[val]++
	}
	if !any {
		return fmt.Sprintf("No events carry a %q property%s yet.", prop, windowClause(win))
	}
	type row struct {
		v string
		n int
	}
	var rows []row
	for v, users := range byVal {
		n := len(users)
		if countEvents {
			n = evCount[v]
		}
		rows = append(rows, row{v, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n || (rows[i].n == rows[j].n && rows[i].v < rows[j].v) })
	var parts []string
	for i, r := range rows {
		if i == 8 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", r.v, r.n))
	}
	// never truncate silently: the tail is disclosed, matching the report's honesty rule.
	more := ""
	if len(rows) > 8 {
		more = fmt.Sprintf(" (+%d more values)", len(rows)-8)
	}
	return fmt.Sprintf("%s breakdown (%s%s): %s%s.", prop, unit, winSuffix(win), strings.Join(parts, " · "), more)
}

func answerSources(evs []event.Event, win askWindow) string {
	scoped := scope(evs, win)
	// web-tracked sites rank referrer hosts on pageviews; events-only datasets fall
	// back to the source-ish property across every event, so the ranking never
	// refuses on a product that tracks custom events without the web SDK.
	byHost := map[string]map[string]bool{}
	tally := func(host, id string) {
		if byHost[host] == nil {
			byHost[host] = map[string]bool{}
		}
		byHost[host][id] = true
	}
	// first-touch attribution, IDENTICAL to web.Compute (the dashboard/GET/MCP engine):
	// each visitor is attributed to the referrer of their EARLIEST pageview in the window,
	// once. Tallying every pageview's host used to double-count multi-referrer visitors
	// AND surface "sources" (a later visit's t.co) that no first-touch surface shows.
	firstPV := map[string]event.Event{}
	for _, e := range scoped {
		if e.Name != "$pageview" {
			continue
		}
		if f, ok := firstPV[e.DistinctID]; !ok || e.Timestamp.Before(f.Timestamp) {
			firstPV[e.DistinctID] = e
		}
	}
	for id, e := range firstPV {
		host := "direct"
		if v, ok := e.Properties["referrer"]; ok {
			if h := hostOf(fmt.Sprintf("%v", v)); h != "" {
				host = h
			}
		}
		tally(host, id)
	}
	if len(byHost) == 0 {
		if prop := detectProp(scoped, "source"); prop != "" {
			// same first-touch rule for events-only datasets: each user's earliest event
			// carrying the source property attributes them, once.
			firstSrc := map[string]event.Event{}
			for _, e := range scoped {
				if _, ok := e.Properties[prop]; !ok {
					continue
				}
				if f, ok := firstSrc[e.DistinctID]; !ok || e.Timestamp.Before(f.Timestamp) {
					firstSrc[e.DistinctID] = e
				}
			}
			for id, e := range firstSrc {
				src := "direct"
				if sv := fmt.Sprintf("%v", e.Properties[prop]); sv != "" {
					src = sv
				}
				tally(src, id)
			}
		}
	}
	if len(byHost) == 0 {
		return "No events" + windowClause(win) + " carry a referrer or source property to rank traffic by."
	}
	type row struct {
		host string
		n    int
	}
	var rows []row
	for h, users := range byHost {
		rows = append(rows, row{h, len(users)})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].n > rows[j].n || (rows[i].n == rows[j].n && rows[i].host < rows[j].host)
	})
	var parts []string
	for i, r := range rows {
		if i == 8 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", r.host, r.n))
	}
	return fmt.Sprintf("Traffic by source (visitors%s): %s.", winSuffix(win), strings.Join(parts, " · "))
}

// answerAIReferrers ranks just the AI assistants sending traffic.
func answerAIReferrers(evs []event.Event, win askWindow) string {
	aiHosts := []string{"claude.ai", "chatgpt.com", "perplexity.ai", "gemini.google.com", "copilot.microsoft.com"}
	scoped := scope(evs, win)
	type row struct {
		host string
		n    int
	}
	var rows []row
	for _, h := range aiHosts {
		users := map[string]bool{}
		for _, e := range scoped {
			if v, ok := e.Properties["referrer"]; ok && strings.Contains(strings.ToLower(fmt.Sprintf("%v", v)), h) {
				users[e.DistinctID] = true
			}
		}
		if len(users) > 0 {
			rows = append(rows, row{h, len(users)})
		}
	}
	if len(rows) == 0 {
		return "No visitors from AI assistants" + windowClause(win) + " yet."
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s %d", r.host, r.n))
	}
	return fmt.Sprintf("Visitors from AI assistants%s: %s.", winSuffix(win), strings.Join(parts, " · "))
}

// answerSplit handles the named two-bucket splits: direct vs search, paid vs organic.
func answerSplit(evs []event.Event, q string, win askWindow) string {
	scoped := scope(evs, win)
	searchHosts := []string{"google.", "bing.", "duckduckgo.", "yahoo.", "baidu."}
	users := func(pred func(e event.Event) bool) int {
		u := map[string]bool{}
		for _, e := range scoped {
			if e.Name == "$pageview" && pred(e) {
				u[e.DistinctID] = true
			}
		}
		return len(u)
	}
	ref := func(e event.Event) string {
		if v, ok := e.Properties["referrer"]; ok {
			return strings.ToLower(fmt.Sprintf("%v", v))
		}
		return ""
	}
	if hasAny(q, "direct vs search", "search vs direct") {
		direct := users(func(e event.Event) bool { return ref(e) == "" })
		search := users(func(e event.Event) bool {
			r := ref(e)
			for _, h := range searchHosts {
				if strings.Contains(r, h) {
					return true
				}
			}
			return false
		})
		return fmt.Sprintf("Direct %d visitors vs search %d%s. (Search = google/bing/duckduckgo/yahoo referrers; everything else is other referral traffic.)",
			direct, search, winSuffix(win))
	}
	// paid vs organic: paid = utm_medium cpc/paid/ppc; organic = everything else
	paid := users(func(e event.Event) bool {
		if v, ok := e.Properties["utm_medium"]; ok {
			m := strings.ToLower(fmt.Sprintf("%v", v))
			return m == "cpc" || m == "paid" || m == "ppc"
		}
		return false
	})
	all := users(func(e event.Event) bool { return true })
	return fmt.Sprintf("Paid (utm_medium cpc/paid/ppc) %d visitors vs organic %d%s.", paid, all-paid, winSuffix(win))
}

// answerEntryPages ranks where sessions LAND (first pageview per user in window).
func answerEntryPages(evs []event.Event, win askWindow) string {
	scoped := scope(evs, win)
	first := map[string]event.Event{}
	for _, e := range scoped {
		if e.Name != "$pageview" {
			continue
		}
		if f, ok := first[e.DistinctID]; !ok || e.Timestamp.Before(f.Timestamp) {
			first[e.DistinctID] = e
		}
	}
	if len(first) == 0 {
		return "No pageviews" + windowClause(win) + " to compute entry pages from."
	}
	counts := map[string]int{}
	for _, e := range first {
		if p, ok := e.Properties["path"]; ok {
			counts[fmt.Sprintf("%v", p)]++
		}
	}
	type row struct {
		p string
		n int
	}
	var rows []row
	for p, n := range counts {
		rows = append(rows, row{p, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n || (rows[i].n == rows[j].n && rows[i].p < rows[j].p) })
	var parts []string
	for i, r := range rows {
		if i == 6 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", r.p, r.n))
	}
	return fmt.Sprintf("Entry pages, where sessions land first%s: %s.", winSuffix(win), strings.Join(parts, " · "))
}

// answerPeakDay names the single biggest day for the metric inside the window.
func answerPeakDay(evs []event.Event, m askMetric, win askWindow, now time.Time) string {
	if !win.scoped() {
		win = askWindow{from: now.AddDate(0, 0, -30), to: now, label: "the last 30 days (UTC)"}
	}
	scoped := scope(evs, win)
	perDay := map[string][]event.Event{}
	for _, e := range scoped {
		perDay[e.Timestamp.UTC().Format("2006-01-02")] = append(perDay[e.Timestamp.UTC().Format("2006-01-02")], e)
	}
	best, bestN := "", -1
	for d, des := range perDay {
		n := metricCount(des, m)
		if n > bestN || (n == bestN && d > best) {
			best, bestN = d, n
		}
	}
	if best == "" {
		return "No events" + windowClause(win) + " to find a peak day in."
	}
	t, _ := time.Parse("2006-01-02", best)
	return fmt.Sprintf("Biggest day for %s%s: %s with %d.", m.label, winSuffix(win), t.Format("Mon Jan 2"), bestN)
}

// answerHours names the busiest hours of the day (UTC) — "when are users most active".
func answerHours(evs []event.Event, win askWindow, now time.Time) string {
	if !win.scoped() {
		win = askWindow{from: now.AddDate(0, 0, -30), to: now, label: "the last 30 days (UTC)"}
	}
	scoped := scope(evs, win)
	var hours [24]int
	for _, e := range scoped {
		hours[e.Timestamp.UTC().Hour()]++
	}
	best, bestN, total := 0, -1, 0
	for h, n := range hours {
		total += n
		if n > bestN {
			best, bestN = h, n
		}
	}
	if total == 0 {
		return "No events" + windowClause(win) + " to read activity hours from."
	}
	return fmt.Sprintf("Activity peaks at %02d:00–%02d:59 UTC (%d events)%s. The dashboard's activity-by-hour strip shows the full 24h shape.",
		best, best, bestN, winSuffix(win))
}

// answerActiveUsers answers DAU/WAU/MAU with the window the acronym actually means.
func answerActiveUsers(evs []event.Event, q string, now time.Time) string {
	days, name := 7, "WAU (7-day actives)"
	switch {
	case hasAny(q, "dau", "daily active"):
		days, name = 1, "DAU (24h actives)"
	case hasAny(q, "mau", "monthly active"):
		days, name = 30, "MAU (30-day actives)"
	case hasAny(q, "wau", "weekly active"):
		days, name = 7, "WAU (7-day actives)"
	}
	users := map[string]bool{}
	cut := now.AddDate(0, 0, -days)
	for _, e := range evs {
		if !e.Timestamp.Before(cut) {
			users[e.DistinctID] = true
		}
	}
	return fmt.Sprintf("%s: %d users active in the last %s.", name, len(users), map[int]string{1: "24 hours", 7: "7 days", 30: "30 days"}[days])
}

// answerStickiness is DAU/MAU — how much of the monthly base shows up daily.
func answerStickiness(evs []event.Event, now time.Time) string {
	dau, mau := map[string]bool{}, map[string]bool{}
	dayCut, monCut := now.AddDate(0, 0, -1), now.AddDate(0, 0, -30)
	for _, e := range evs {
		if !e.Timestamp.Before(monCut) {
			mau[e.DistinctID] = true
			if !e.Timestamp.Before(dayCut) {
				dau[e.DistinctID] = true
			}
		}
	}
	if len(mau) == 0 {
		return "No active users in the last 30 days to compute stickiness from."
	}
	return fmt.Sprintf("Stickiness (DAU/MAU): %d%% — %d daily actives of %d monthly actives. Rule of thumb: 20%%+ is strong for a product used most days.",
		int(float64(len(dau))/float64(len(mau))*100+0.5), len(dau), len(mau))
}

// answerLifecycle splits the recent base into new vs returning vs dormant — behind
// "mostly new or returning", "how many churned/went dormant". A short asked window
// ("dormant yesterday") answers that exact day from the SAME per-day lifecycle
// engine /v1/lifecycle serves, never a silently substituted week.
func answerLifecycle(evs []event.Event, q string, win askWindow, now time.Time) string {
	if win.scoped() && win.to.Sub(win.from) <= 48*time.Hour {
		if row, ok := lifecycleDayAt(evs, win.from); ok {
			if hasAny(q, "churn", "dormant", "stopped", "lost", "gone quiet") {
				return fmt.Sprintf("%d users went dormant %s — active the day before but silent that day (computed by the per-day lifecycle report).", row.Dormant, win.label)
			}
			return fmt.Sprintf("Lifecycle %s: %d new, %d returning, %d resurrected, %d went dormant (per-day lifecycle report).",
				win.label, row.New, row.Returning, row.Resurrected, row.Dormant)
		}
		return "No activity on that day to classify."
	}
	return answerLifecycleWeek(evs, q, now)
}

// lifecycleDayAt picks the lifecycle row for the calendar day containing t.
func lifecycleDayAt(evs []event.Event, t time.Time) (engagement.LifecycleDay, bool) {
	want := t.UTC().Truncate(24 * time.Hour)
	for _, row := range engagement.ComputeLifecycle(evs, 90) {
		if row.Date.Equal(want) {
			return row, true
		}
	}
	return engagement.LifecycleDay{}, false
}

func answerLifecycleWeek(evs []event.Event, q string, now time.Time) string {
	weekCut, priorCut := now.AddDate(0, 0, -7), now.AddDate(0, 0, -14)
	firstSeen := map[string]time.Time{}
	activeNow, activePrior := map[string]bool{}, map[string]bool{}
	for _, e := range evs {
		if f, ok := firstSeen[e.DistinctID]; !ok || e.Timestamp.Before(f) {
			firstSeen[e.DistinctID] = e.Timestamp
		}
		if !e.Timestamp.Before(weekCut) {
			activeNow[e.DistinctID] = true
		} else if !e.Timestamp.Before(priorCut) {
			activePrior[e.DistinctID] = true
		}
	}
	newN, retN := 0, 0
	for id := range activeNow {
		if firstSeen[id].Before(weekCut) {
			retN++
		} else {
			newN++
		}
	}
	dormant := 0
	for id := range activePrior {
		if !activeNow[id] {
			dormant++
		}
	}
	if hasAny(q, "churn", "dormant", "stopped", "lost", "gone quiet") {
		return fmt.Sprintf("%d users went dormant this week — active the prior week (%s–%s) but silent since. %d stayed active.",
			dormant, priorCut.Format("Jan 2"), weekCut.Format("Jan 2"), retN)
	}
	tot := newN + retN
	if tot == 0 {
		return "No active users in the last 7 days to split into new vs returning."
	}
	return fmt.Sprintf("Of %d users active in the last 7 days: %d new (%d%%), %d returning (%d%%). %d went dormant vs the prior week.",
		tot, newN, newN*100/tot, retN, retN*100/tot, dormant)
}

// answerConvBy is conversion-by-segment: of the users in each bucket of prop, how many
// did the conversion event — "which browser converts best", "conversion by country".
// answerConvBySteps computes a real 2-step (or more) funnel conversion PER segment value
// ("conversion from signup to checkout by plan"), first-touch-stamping the property so a
// segment set only on the entry event still attributes. Segments below a sample floor are
// listed but never headline (n<5 at "100%" is noise, not a winner).
func answerConvBySteps(evs []event.Event, prop string, steps []funnel.Step, win askWindow) string {
	segs := funnel.ComputeBreakdown(query.StampFirstTouch(evs, prop), steps, 7*24*time.Hour, prop)
	type row struct {
		val   string
		users int
		conv  int // overall conversion %
	}
	var rows []row
	for _, s := range segs {
		if s.Value == "(none)" {
			continue
		}
		users := 0
		if len(s.Steps) > 0 {
			users = s.Steps[0].Count
		}
		if users == 0 {
			continue
		}
		rows = append(rows, row{s.Value, users, pct(s.OverallConversion)})
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No events carry a %q property%s to segment the %s → %s funnel by.", prop, windowClause(win), steps[0].Event, steps[len(steps)-1].Event)
	}
	// eligible (n>=5) segments rank first by rate; tiny ones sort last so they can't headline.
	const floor = 5
	sort.Slice(rows, func(i, j int) bool {
		ei, ej := rows[i].users >= floor, rows[j].users >= floor
		if ei != ej {
			return ei // eligible before ineligible
		}
		if rows[i].conv != rows[j].conv {
			return rows[i].conv > rows[j].conv
		}
		return rows[i].users > rows[j].users
	})
	var parts []string
	for i, r := range rows {
		if i == 6 {
			break
		}
		tag := ""
		if r.users < floor {
			tag = " (small sample)"
		}
		parts = append(parts, fmt.Sprintf("%s %d%% (%d)%s", r.val, r.conv, r.users, tag))
	}
	return fmt.Sprintf("%s → %s conversion by %s%s: %s.", steps[0].Event, steps[len(steps)-1].Event, prop, winSuffix(win), strings.Join(parts, " · "))
}

func answerConvBy(evs []event.Event, prop, convEvent string, win askWindow) string {
	scoped := scope(evs, win)
	segUsers := map[string]map[string]bool{}
	conv := map[string]bool{}
	for _, e := range scoped {
		if e.Name == convEvent {
			conv[e.DistinctID] = true
		}
	}
	for _, e := range scoped {
		v, ok := e.Properties[prop]
		if !ok {
			continue
		}
		val := fmt.Sprintf("%v", v)
		if segUsers[val] == nil {
			segUsers[val] = map[string]bool{}
		}
		segUsers[val][e.DistinctID] = true
	}
	if len(segUsers) == 0 {
		return fmt.Sprintf("No events carry a %q property%s, so there's nothing to segment conversion by.", prop, windowClause(win))
	}
	type row struct {
		val        string
		users, did int
	}
	var rows []row
	for val, users := range segUsers {
		did := 0
		for id := range users {
			if conv[id] {
				did++
			}
		}
		rows = append(rows, row{val, len(users), did})
	}
	// rank by rate BUT keep small samples out of the headline — a 1-of-1 "100%" segment must
	// not outrank a 40-of-100 one. Eligible (n>=5) segments sort first; tiny ones trail, tagged.
	const floor = 5
	sort.Slice(rows, func(i, j int) bool {
		ei, ej := rows[i].users >= floor, rows[j].users >= floor
		if ei != ej {
			return ei
		}
		ri := float64(rows[i].did) / float64(rows[i].users)
		rj := float64(rows[j].did) / float64(rows[j].users)
		if ri != rj {
			return ri > rj
		}
		return rows[i].users > rows[j].users
	})
	var parts []string
	for i, r := range rows {
		if i == 6 {
			break
		}
		tag := ""
		if r.users < floor {
			tag = " (small sample)"
		}
		parts = append(parts, fmt.Sprintf("%s %d%% (%d of %d)%s", r.val, int(float64(r.did)/float64(r.users)*100+0.5), r.did, r.users, tag))
	}
	return fmt.Sprintf("%q conversion by %s%s: %s.", convEvent, prop, winSuffix(win), strings.Join(parts, " · "))
}

// ---- extra window forms --------------------------------------------------------------

var sinceWeekdayRe = regexp.MustCompile(`since\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday)`)
var monthDayRe = `(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|oct|nov|dec)\.?\s+(\d{1,2})`
var rangeRe = regexp.MustCompile(`(?:between|from)\s+` + monthDayRe + `\s+(?:and|to|through|-|–)\s+(?:` + monthDayRe + `|(\d{1,2}))`)

var monthNums = map[string]time.Month{"january": 1, "jan": 1, "february": 2, "feb": 2, "march": 3, "mar": 3,
	"april": 4, "apr": 4, "may": 5, "june": 6, "jun": 6, "july": 7, "jul": 7, "august": 8, "aug": 8,
	"september": 9, "sep": 9, "october": 10, "oct": 10, "november": 11, "nov": 11, "december": 12, "dec": 12}

var weekdayNums = map[string]time.Weekday{"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3,
	"thursday": 4, "friday": 5, "saturday": 6}

// parseExtraWindow handles the window shapes parseWindow doesn't: "since monday",
// "between june 20 and june 30", "from july 1 to july 7". Dates resolve to the most
// recent occurrence not in the future (UTC).
func parseExtraWindow(q string, now time.Time) (askWindow, bool) {
	if m := sinceWeekdayRe.FindStringSubmatch(q); m != nil {
		target := weekdayNums[m[1]]
		back := (int(now.Weekday()) - int(target) + 7) % 7
		if back == 0 {
			back = 7 // "since monday" asked on a Monday means the week that's just started today
		}
		if m[1] == strings.ToLower(now.Weekday().String()) {
			back = 0
		}
		from := now.Truncate(24*time.Hour).AddDate(0, 0, -back)
		return askWindow{from: from, to: now, label: "since " + title(m[1]) + " (" + from.Format("Jan 2") + ", UTC)"}, true
	}
	if m := rangeRe.FindStringSubmatch(q); m != nil {
		y := now.Year()
		d1, _ := strconv.Atoi(m[2])
		from := time.Date(y, monthNums[m[1]], d1, 0, 0, 0, 0, time.UTC)
		var to time.Time
		if m[3] != "" { // second month named
			d2, _ := strconv.Atoi(m[4])
			to = time.Date(y, monthNums[m[3]], d2, 0, 0, 0, 0, time.UTC)
		} else { // "june 20 to 30"
			d2, _ := strconv.Atoi(m[5])
			to = time.Date(y, monthNums[m[1]], d2, 0, 0, 0, 0, time.UTC)
		}
		if from.After(now) {
			from = from.AddDate(-1, 0, 0)
			to = to.AddDate(-1, 0, 0)
		}
		toEx := to.AddDate(0, 0, 1) // inclusive end date, exclusive bound
		return askWindow{from: from, to: toEx,
			label: from.Format("Jan 2") + " – " + to.Format("Jan 2") + " (UTC)"}, true
	}
	return askWindow{}, false
}

// ---- the helpers the dispatcher calls -------------------------------------------------

// windowTolerantIntent lists the intents whose vocabulary legitimately contains
// day/week/month/hour as the METRIC ("week 1 retention", "peak hour"), so the window
// parser's unsupported-phrase refusal must not fire for them.
func windowTolerantIntent(i askIntent) bool {
	switch i {
	case intentRetention, intentStickiness, intentLifecycle, intentHours, intentPeakDay:
		return true
	}
	return false
}

// answerCompareFunnel answers "did more people convert this week than last week" with
// both periods' funnel and the verdict.
func answerCompareFunnel(evs []event.Event, vol []string, q string, cur, prior askWindow) string {
	fsteps, ftitle := detectFunnel(evs, vol)
	// a named step scopes the comparison to ITS rate: "activation rate this week vs
	// last week" compares activate/signup, not the whole funnel
	if named := stepsInQuestion(normalizeEventWords(q), funnel.Compute(evs, fsteps, 7*24*time.Hour)); len(named) >= 1 && hasAny(q, "rate", "conversion", "convert") {
		lo, hi := 0, named[0]
		if len(named) >= 2 {
			lo, hi = named[0], named[1]
		}
		if lo < hi {
			pair := []funnel.Step{fsteps[lo], fsteps[hi]}
			a := funnelOver(scope(evs, cur), pair)
			b := funnelOver(scope(evs, prior), pair)
			dir := "flat"
			if a.conv > b.conv {
				dir = "up"
			} else if a.conv < b.conv {
				dir = "down"
			}
			return fmt.Sprintf("%s → %s rate: %d of %d (%d%%) %s vs %d of %d (%d%%) %s — %s.",
				fsteps[lo].Event, fsteps[hi].Event, a.done, a.started, a.pct, cur.label,
				b.done, b.started, b.pct, prior.label, dir)
		}
	}
	frCur := funnelOver(scope(evs, cur), fsteps)
	frPrior := funnelOver(scope(evs, prior), fsteps)
	dir := "flat"
	if frCur.conv > frPrior.conv {
		dir = "up"
	} else if frCur.conv < frPrior.conv {
		dir = "down"
	}
	note := ""
	if strings.HasPrefix(cur.label, "this ") {
		note = " Note: the current period is still in progress."
	}
	return fmt.Sprintf("Conversion through %s: %d of %d (%d%%) %s vs %d of %d (%d%%) %s — %s.%s",
		ftitle, frCur.done, frCur.started, frCur.pct, cur.label,
		frPrior.done, frPrior.started, frPrior.pct, prior.label, dir, note)
}

type funnelNums struct {
	started, done, pct int
	conv               float64
}

func funnelOver(evs []event.Event, steps []funnel.Step) funnelNums {
	fr := funnel.Compute(evs, steps, 7*24*time.Hour)
	if len(fr.Steps) == 0 || fr.Steps[0].Count == 0 {
		return funnelNums{}
	}
	started := fr.Steps[0].Count
	done := fr.Steps[len(fr.Steps)-1].Count
	conv := float64(done) / float64(started)
	return funnelNums{started: started, done: done, pct: int(conv*100 + 0.5), conv: conv}
}

// answerVisitorShare answers "what share of visitors ever sign up" — distinct users
// who did the event over distinct visitors.
func answerVisitorShare(evs []event.Event, ev string, win askWindow) string {
	visitors := map[string]bool{}
	did := map[string]bool{}
	anyPV := false
	for _, e := range evs {
		if e.Name == "$pageview" {
			anyPV = true
			visitors[e.DistinctID] = true
		}
		if e.Name == ev {
			did[e.DistinctID] = true
		}
	}
	if !anyPV {
		for _, e := range evs {
			visitors[e.DistinctID] = true
		}
	}
	if len(visitors) == 0 {
		return "No visitors" + windowClause(win) + " to compute a share from."
	}
	n := 0
	for id := range did {
		if visitors[id] {
			n++
		}
	}
	return fmt.Sprintf("%d%% of visitors did %q%s (%d of %d).",
		int(float64(n)/float64(len(visitors))*100+0.5), ev, winSuffix(win), n, len(visitors))
}

// answerRetentionVsSeg compares day-1 retention between two segments ("do users in
// india come back more than users in the us").
func answerRetentionVsSeg(evs []event.Event, win askWindow, now time.Time, q string, a, b askSeg) string {
	// honor the asked period ("day-7 retention mobile vs desktop" must answer DAY-7, not
	// day-1) — substituting day-1 under the "cannot be fabricated" seal is exactly the
	// wrong-metric-under-the-seal trust bug.
	day := retentionPeriodAsked(q)
	if day < 1 {
		day = 1
	}
	one := func(s askSeg) (int, int) {
		if !s.found {
			return 0, 0
		}
		// acquisition/user attributes (device/referrer/…) scope by USER, not event-level —
		// a mobile-acquired user's signup carries no device, so segFilter would drop them.
		pool := segFilter(evs, s)
		if userAttr(s.prop) {
			pool = segFilterUsers(evs, s)
		}
		rr := retention.Compute(scope(pool, win), day, "")
		return retention.DayN(rr, day, now)
	}
	dNa, sa := one(a)
	dNb, sb := one(b)
	pcts := func(d, n int) string {
		if n == 0 {
			return "not enough history"
		}
		return fmt.Sprintf("%d%% (of %d)", int(float64(d)/float64(n)*100+0.5), n)
	}
	verdict := ""
	if sa > 0 && sb > 0 {
		ra, rb := float64(dNa)/float64(sa), float64(dNb)/float64(sb)
		switch {
		case ra > rb:
			verdict = fmt.Sprintf(" — %s retains better", a.label)
		case rb > ra:
			verdict = fmt.Sprintf(" — %s retains better", b.label)
		default:
			verdict = " — dead even"
		}
	}
	return fmt.Sprintf("Day-%d retention: %s %s vs %s %s%s. (Any activity counts as returning.)",
		day, a.label, pcts(dNa, sa), b.label, pcts(dNb, sb), verdict)
}

// answerConvByQ resolves the dimension + conversion event from the question and runs
// the conversion-by-segment report.
func answerConvByQ(evs []event.Event, vol []string, q string, win askWindow) string {
	prop := ""
	for _, p := range []struct {
		words []string
		prop  string
	}{
		{[]string{"country", "countries", "nation"}, "country"},
		{[]string{"browser"}, "browser"},
		{[]string{"device", "mobile", "desktop", "phone"}, "device"},
		{[]string{" os", "operating system", "ios", "android", "windows", "macos"}, "os"},
		{[]string{"plan"}, "plan"},
		{[]string{"source", "channel", "referrer"}, "source"},
	} {
		for _, w := range p.words {
			if strings.Contains(q, w) {
				prop = p.prop
				break
			}
		}
		if prop != "" {
			break
		}
	}
	if prop == "" {
		prop = "device"
	}
	// "conversion FROM x TO y by <prop>" is a real 2-step funnel per segment — using a single
	// event (namedEvent's first hit) computes a degenerate single-step rate that is 100% for
	// every segment. Detect two named steps and run the proper per-segment funnel.
	if steps := namedFunnelSteps(q, vol); len(steps) >= 2 {
		return answerConvBySteps(scope(evs, win), prop, steps, win)
	}
	conv := namedEvent(q, vol)
	if conv == "" {
		conv = pickConversion(evs, vol)
	}
	if conv == "" {
		return "No conversion event tracked yet to segment — send a signup/checkout style event first."
	}
	// "conversion by source/channel/referrer" must attribute the ACQUISITION channel
	// (first-touch referrer/utm), not a raw "source" property that's only stamped on the
	// conversion event itself — that made every converter fall in one bucket at 100%.
	if prop == "source" {
		return answerConvByChannel(evs, conv, win)
	}
	return answerConvBy(evs, prop, conv, win)
}

// answerConvByChannel is conversion segmented by first-touch acquisition channel, so
// "conversion by source" reads real channels (reddit X%, google Y%) instead of a
// degenerate 100% for a property present only on the conversion event.
func answerConvByChannel(evs []event.Event, convEvent string, win askWindow) string {
	scoped := scope(evs, win)
	firstTS := map[string]time.Time{}
	channel := map[string]string{}
	conv := map[string]bool{}
	for _, e := range scoped {
		if t, ok := firstTS[e.DistinctID]; !ok || e.Timestamp.Before(t) {
			firstTS[e.DistinctID] = e.Timestamp
			channel[e.DistinctID] = channelOf(e)
		}
		if e.Name == convEvent {
			conv[e.DistinctID] = true
		}
	}
	type row struct {
		ch         string
		users, did int
	}
	byCh := map[string]*row{}
	for id, ch := range channel {
		r := byCh[ch]
		if r == nil {
			r = &row{ch: ch}
			byCh[ch] = r
		}
		r.users++
		if conv[id] {
			r.did++
		}
	}
	if len(byCh) == 0 {
		return "No visitors to segment conversion by channel yet."
	}
	rows := make([]*row, 0, len(byCh))
	for _, r := range byCh {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := float64(rows[i].did)/float64(rows[i].users), float64(rows[j].did)/float64(rows[j].users)
		if ri != rj {
			return ri > rj
		}
		return rows[i].users > rows[j].users
	})
	parts := []string{}
	for i, r := range rows {
		if i == 6 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d%% (%d of %d)", r.ch, int(float64(r.did)/float64(r.users)*100+0.5), r.did, r.users))
	}
	return fmt.Sprintf("%q conversion by channel%s: %s.", convEvent, winSuffix(win), strings.Join(parts, " · "))
}

// stepsInQuestion finds funnel step names mentioned in the question, returned as step
// indices ordered by where they appear in the sentence — so "signup to activation
// rate" yields [signup, activate] in the asked direction.
// namedFunnelSteps returns the event names the question explicitly mentions as whole tokens,
// in the order they appear — so "what percent of landing users convert" yields landing→convert
// exactly. Returns <2 steps when the user didn't name a funnel, so the caller falls back to
// the auto-detected funnel.
func namedFunnelSteps(q string, vol []string) []funnel.Step {
	toks := askTokens(normalizeEventWords(q))
	firstPos := map[string]int{}
	for i, t := range toks {
		if _, ok := firstPos[t]; !ok {
			firstPos[t] = i
		}
	}
	type hit struct {
		pos  int
		name string
	}
	var hits []hit
	for _, n := range vol {
		if p, ok := firstPos[strings.ToLower(n)]; ok {
			hits = append(hits, hit{p, n})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	steps := make([]funnel.Step, 0, len(hits))
	for _, h := range hits {
		steps = append(steps, funnel.Step{Event: h.name})
	}
	return steps
}

func stepsInQuestion(q string, fr funnel.Result) []int {
	aliases := func(name string) []string {
		out := []string{name}
		switch name {
		case "activate":
			out = append(out, "activation", "activating", "activated")
		case "signup":
			out = append(out, "sign up", "sign-up", "signed up", "signups")
		case "checkout":
			out = append(out, "check out", "checking out", "checked out", "checkouts")
		case "purchase":
			out = append(out, "purchases", "buying", "bought")
		case "subscribe":
			out = append(out, "subscription", "subscribing", "subscribed")
		}
		return out
	}
	type hit struct{ pos, idx int }
	var hits []hit
	for i, st := range fr.Steps {
		best := -1
		for _, a := range aliases(strings.ToLower(st.Event)) {
			if p := strings.Index(q, a); p >= 0 && (best == -1 || p < best) {
				best = p
			}
		}
		if best >= 0 {
			hits = append(hits, hit{best, i})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	var out []int
	for _, h := range hits {
		out = append(out, h.idx)
	}
	return out
}

// oldestObservableDay is the highest retention day any current cohort is old enough
// to have observed — what "day 30 retention" can honestly fall back to naming.
func oldestObservableDay(rr retention.Result, now time.Time) int {
	best := 0
	for d := rr.MaxDays; d >= 1; d-- {
		if _, size := retention.PeriodN(rr, d, now); size > 0 {
			best = d
			break
		}
	}
	return best
}

// retentionPeriodAsked parses an explicit "day N" out of a retention question
// (day 30, day-14, d30). 0 = nothing explicit beyond the day-1/day-7 defaults.
var dayNRe = regexp.MustCompile(`\bday[ -]?(\d+)\b|\bd(\d+)\b`)

func retentionPeriodAsked(q string) int {
	m := dayNRe.FindStringSubmatch(q)
	if m == nil {
		return 0
	}
	num := m[1]
	if num == "" {
		num = m[2]
	}
	n, _ := strconv.Atoi(num)
	if n > 365 {
		return 0
	}
	return n
}

// segFilterUsers keeps ALL events belonging to users who have at least one event
// matching the segment. Funnels, retention, and engagement need this user-level
// scope: a reddit visitor's signup event doesn't carry the referrer property, so
// event-level filtering would erase the very conversions being asked about.
func segFilterUsers(evs []event.Event, s askSeg) []event.Event {
	ids := map[string]bool{}
	for _, e := range evs {
		if segMatches(e, s) {
			ids[e.DistinctID] = true
		}
	}
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if ids[e.DistinctID] {
			out = append(out, e)
		}
	}
	return out
}

// answerTrendText renders an over-time answer for "trend" questions: bucketed
// counts plus the direction, since the ask bar speaks text, not charts.
func answerTrendText(evs []event.Event, m askMetric, segs []askSeg, win askWindow, now time.Time) string {
	if !win.scoped() {
		win = askWindow{from: now.AddDate(0, 0, -30), to: now, label: "the last 30 days (UTC)"}
	}
	segNote := ""
	if len(segs) == 1 {
		if !segs[0].found {
			return fmt.Sprintf("No events from %s in the data, so there's no trend to show.", segs[0].label)
		}
		evs = segFilter(evs, segs[0])
		segNote = " from " + segs[0].label
	}
	scoped := scope(evs, win)
	bucket := 7 * 24 * time.Hour
	unit := "weekly"
	if win.to.Sub(win.from) <= 14*24*time.Hour {
		bucket, unit = 24*time.Hour, "daily"
	}
	var series []int
	for t := win.from; t.Before(win.to); t = t.Add(bucket) {
		end := t.Add(bucket)
		if end.After(win.to) {
			end = win.to
		}
		series = append(series, metricCount(scope(scoped, askWindow{from: t, to: end}), m))
	}
	total := metricCount(scoped, m)
	if total == 0 {
		return fmt.Sprintf("No %s%s %s, so the trend is flat at zero.", m.label, segNote, win.label)
	}
	var parts []string
	for _, n := range series {
		parts = append(parts, strconv.Itoa(n))
	}
	dir := "flat"
	if len(series) >= 2 {
		if series[len(series)-1] > series[0] {
			dir = "trending up"
		} else if series[len(series)-1] < series[0] {
			dir = "trending down"
		}
	}
	last := ""
	if len(series) >= 1 && strings.HasSuffix(unit, "ly") {
		last = " (the last bucket may be partial)"
	}
	return fmt.Sprintf("%s%s %s: %d total, %s %s — %s%s.", title(m.label), segNote, win.label, total, unit, strings.Join(parts, " → "), dir, last)
}

// filterByName keeps only events with the given name — the scope for "break down
// signups by country".
func filterByName(evs []event.Event, name string) []event.Event {
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// answerReturning counts users active in a short window who were first seen before
// it — "how many people came back today". "After being gone a while" means the
// lifecycle RESURRECTED bucket (dormant 1+ days, back now), not merely returning.
func answerReturning(evs []event.Event, q string, win askWindow) string {
	if hasAny(q, "gone a while", "after being gone", "been away", "gone for a while", "resurrected", "won back") {
		if row, ok := lifecycleDayAt(evs, win.from); ok {
			return fmt.Sprintf("%d resurrected users %s — back after being inactive, distinct from the %d who were also active the day before (per-day lifecycle report).",
				row.Resurrected, win.label, row.Returning)
		}
		return "No activity on that day to classify."
	}
	firstSeen := map[string]time.Time{}
	for _, e := range evs {
		if f, ok := firstSeen[e.DistinctID]; !ok || e.Timestamp.Before(f) {
			firstSeen[e.DistinctID] = e.Timestamp
		}
	}
	active := map[string]bool{}
	for _, e := range scope(evs, win) {
		active[e.DistinctID] = true
	}
	n := 0
	for id := range active {
		if firstSeen[id].Before(win.from) {
			n++
		}
	}
	return fmt.Sprintf("%d returning users %s — active in the window and first seen before it (%d active in total).",
		n, win.label, len(active))
}
