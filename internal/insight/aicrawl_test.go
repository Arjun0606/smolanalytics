package insight

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The crawler rules describe someone else's robots visiting the customer's server, which
// makes them spiky by nature: a sweep, then days of nothing, then another sweep. A detector
// that fired on ordinary spikiness would teach the reader to skip the whole card, so most of
// what follows asserts SILENCE — and the loudest silence of all is an instance where the
// middleware was never installed, because a nag about a feature you do not have is a bug.

// crawlHit builds one $ai_crawl event the way the edge middleware reports it: server-side,
// off the request itself, with the status the crawler was actually served.
func crawlHit(at time.Time, crawler, operator, purpose, path string, status int) event.Event {
	return event.Event{
		Name: aicrawl.CrawlEvent, DistinctID: "edge-reporter", Timestamp: at,
		Properties: map[string]any{
			"crawler": crawler, "operator": operator, "purpose": purpose,
			"path": path, "status": float64(status), "site": "example.com",
		},
	}
}

// crawlSweep lays n hits for one crawler ending at `end` and walking backwards an hour at a
// time, `bad` of them served `status`. Paths cycle over five URLs so Pages is a real distinct
// count rather than a copy of Hits, and the newest hit is always /p0 so the "last seen on"
// half of the stopped finding has a fixed answer.
func crawlSweep(end time.Time, crawler, operator, purpose string, n, bad, status int) []event.Event {
	var out []event.Event
	for i := 0; i < n; i++ {
		code := 200
		if i < bad {
			code = status
		}
		out = append(out, crawlHit(end.Add(-time.Duration(i)*time.Hour),
			crawler, operator, purpose, fmt.Sprintf("/p%d", i%5), code))
	}
	return out
}

// humanViews puts n distinct people on one page inside the window. The gap rule needs proven
// human demand, not just an uncrawled URL, so the visitors have to be real and separate.
func humanViews(path string, end time.Time, n int) []event.Event {
	var out []event.Event
	for i := 0; i < n; i++ {
		out = append(out, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("reader-%d", i),
			Timestamp:  end.Add(-24*time.Hour - time.Duration(i)*time.Minute),
			Properties: map[string]any{"path": path},
		})
	}
	return out
}

func crawlFinding(fs []Finding, metric string) (Finding, bool) {
	for _, f := range fs {
		if f.Kind == KindCrawl && f.Metric == metric {
			return f, true
		}
	}
	return Finding{}, false
}

// --- nothing is reporting -----------------------------------------------------------------

func TestNoCrawlReportingIsSilenceNotANag(t *testing.T) {
	now := time.Now().UTC()
	if fs := aiCrawlFindings(funnelFixture(), now); len(fs) != 0 {
		t.Fatalf("an instance with no middleware installed must produce nothing, got %+v", fs)
	}
	// and the same through the front door, where the nag would actually be seen
	for _, f := range Generate(funnelFixture()) {
		if f.Kind == KindCrawl {
			t.Errorf("crawler finding on an instance that reports no crawls: %+v", f)
		}
	}
}

// Reporting installed, nothing crawled yet is the other half of the pair: it is a real answer
// about the site, not a missing integration, and it still must not manufacture a warning.
func TestReporterInstalledButNothingCrawledYetStaysQuiet(t *testing.T) {
	now := time.Now().UTC()
	evs := append(funnelFixture(), humanViews("/pricing", now, 40)...)
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 3, 0, 200)...)
	for _, f := range aiCrawlFindings(evs, now) {
		t.Errorf("three hits is not evidence of anything: %+v", f)
	}
}

// --- crawlErrors --------------------------------------------------------------------------

func TestCrawlErrorsNamesTheOperatorAndTheRealCounts(t *testing.T) {
	now := time.Now().UTC()
	evs := crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 20, 12, 403)

	f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_errors")
	if !ok {
		t.Fatalf("60%% of fetches refused must be flagged; got %+v", aiCrawlFindings(evs, now))
	}
	if f.Severity != "warn" || f.Kind != KindCrawl || f.Value != "GPTBot" || f.Rate != 60 || f.N != 20 {
		t.Errorf("wrong shape: %+v", f)
	}
	if !strings.Contains(f.Title, "turning GPTBot away") {
		t.Errorf("title must name the crawler: %q", f.Title)
	}
	// the counts beside the percentage: 60% means nothing without the 20 it came off
	if !strings.Contains(f.Detail, "12 of its last 20 fetches got an error (60%)") {
		t.Errorf("detail must carry the raw counts: %q", f.Detail)
	}
	// and the company, because "GPTBot" is not who the reader has to think about
	if !strings.Contains(f.Detail, "OpenAI") {
		t.Errorf("detail must name the operator: %q", f.Detail)
	}
}

func TestCrawlErrorsAreSilentBelowTheHitFloor(t *testing.T) {
	now := time.Now().UTC()
	// every single fetch refused — but on 8 of them, which is one bot rule away from being
	// a coincidence. A percentage needs a denominator before it is worth a warning.
	evs := crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 8, 8, 403)
	if f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_errors"); ok {
		t.Fatalf("8 hits is under the floor and must not ship a rate: %+v", f)
	}
}

// The threshold is inclusive: a crawler sitting exactly on it is being turned away often
// enough to say so, and a floor that silently needed one more error would be untestable.
func TestCrawlErrorsFireExactlyOnTheThreshold(t *testing.T) {
	now := time.Now().UTC()
	evs := crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 20, 6, 500)
	f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_errors")
	if !ok {
		t.Fatalf("30%% is the stated threshold and must qualify; got %+v", aiCrawlFindings(evs, now))
	}
	if f.Rate != 30 || f.N != 20 {
		t.Errorf("wrong shape: %+v", f)
	}
}

func TestCrawlErrorsPickTheWorstOffender(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	// both qualify; ClaudeBot is losing more fetches, at a higher rate
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 30, 21, 403)...)
	evs = append(evs, crawlSweep(now.Add(-2*time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 20, 6, 403)...)

	fs := aiCrawlFindings(evs, now)
	f, ok := crawlFinding(fs, "crawl_errors")
	if !ok {
		t.Fatalf("expected the error finding; got %+v", fs)
	}
	if f.Value != "ClaudeBot" || f.Rate != 70 || f.N != 30 {
		t.Errorf("the worse offender must be the one named: %+v", f)
	}
	if !strings.Contains(f.Detail, "21 of its last 30") || !strings.Contains(f.Detail, "Anthropic") {
		t.Errorf("detail: %q", f.Detail)
	}
	// one sentence about refusals, not one per crawler — the digest is a verdict, not a log
	n := 0
	for _, x := range fs {
		if x.Metric == "crawl_errors" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the digest gets one refusal finding, not %d", n)
	}
}

// --- crawlStopped -------------------------------------------------------------------------

func TestCrawlStoppedFiresOnRealHistoryGoneQuiet(t *testing.T) {
	now := time.Now().UTC()
	evs := crawlSweep(now.Add(-10*24*time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 20, 0, 200)

	f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_stopped")
	if !ok {
		t.Fatalf("a regular visitor silent for 10 days must be flagged; got %+v", aiCrawlFindings(evs, now))
	}
	if f.Severity != "warn" || f.Kind != KindCrawl || f.Value != "ClaudeBot" || f.Rate != 10 || f.N != 20 {
		t.Errorf("wrong shape: %+v", f)
	}
	if !strings.Contains(f.Title, "ClaudeBot stopped fetching") {
		t.Errorf("title: %q", f.Title)
	}
	// pages read, days silent, where it left off, whose index is now frozen
	if !strings.Contains(f.Detail, "It read 5 pages here and then nothing for 10 days") {
		t.Errorf("detail must carry the history and the silence: %q", f.Detail)
	}
	if !strings.Contains(f.Detail, "/p0") || !strings.Contains(f.Detail, "Anthropic") {
		t.Errorf("detail must name the last path and the operator: %q", f.Detail)
	}
}

func TestCrawlStoppedIsSilentWhenItWasSeenRecently(t *testing.T) {
	now := time.Now().UTC()
	// three days between sweeps is a schedule, not an outage
	evs := crawlSweep(now.Add(-3*24*time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 20, 0, 200)
	if f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_stopped"); ok {
		t.Fatalf("a crawler seen 3 days ago has not stopped: %+v", f)
	}
}

// The case the hit floor exists for. One small sweep and then quiet is what a crawler
// discovering a site normally looks like; calling it an outage would fire on every new
// customer in their second week.
func TestOneSmallSweepThenQuietIsNotAnOutage(t *testing.T) {
	now := time.Now().UTC()
	evs := crawlSweep(now.Add(-12*24*time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 6, 0, 200)
	if f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_stopped"); ok {
		t.Fatalf("6 hits is not a habit that can be broken: %+v", f)
	}
}

// --- crawlAbsentOperator ------------------------------------------------------------------

// The whole point of the rule: absence is only evidence next to a presence. With one operator
// reporting, "OpenAI has never fetched you" is indistinguishable from "the middleware shipped
// on Tuesday", and printing it would be a guess wearing a verdict's clothes.
func TestAbsentOperatorNeedsAComparison(t *testing.T) {
	now := time.Now().UTC()
	evs := crawlSweep(now.Add(-time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 30, 0, 200)
	if f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_absent"); ok {
		t.Fatalf("one reporting operator says nothing about the others: %+v", f)
	}
}

func TestAbsentOperatorIsSilentOnTooFewFetches(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	// two operators, but 16 fetches between them — too early to conclude anyone is missing
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 8, 0, 200)...)
	evs = append(evs, crawlSweep(now.Add(-2*time.Hour), "PerplexityBot", "Perplexity", aicrawl.PurposeSearch, 8, 0, 200)...)
	if f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_absent"); ok {
		t.Fatalf("16 fetches is not a crawl history to be absent from: %+v", f)
	}
}

func TestAbsentOperatorFiresAsAnAsymmetry(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 15, 0, 200)...)
	evs = append(evs, crawlSweep(now.Add(-2*time.Hour), "PerplexityBot", "Perplexity", aicrawl.PurposeSearch, 15, 0, 200)...)

	f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_absent")
	if !ok {
		t.Fatalf("two operators in, one named operator missing, is a real asymmetry; got %+v", aiCrawlFindings(evs, now))
	}
	if f.Severity != "info" || f.Kind != KindCrawl || f.Value != "OpenAI" || f.N != 30 {
		t.Errorf("wrong shape: %+v", f)
	}
	if f.Rate != 0 {
		t.Errorf("there is no rate here and none may be invented: %+v", f)
	}
	if !strings.Contains(f.Title, "OpenAI has never fetched your site") {
		t.Errorf("title: %q", f.Title)
	}
	// the comparison IS the evidence, so both sides of it have to be in the sentence
	if !strings.Contains(f.Detail, "Anthropic and Perplexity crawled it in the last 30 days; OpenAI did not, across 30 fetches") {
		t.Errorf("detail must state the comparison and its base: %q", f.Detail)
	}
	// named in the reader's terms — nobody wonders whether "OpenAI" read them, they wonder
	// whether ChatGPT can answer about them
	if !strings.Contains(f.Detail, "ChatGPT") {
		t.Errorf("detail must connect the operator to the assistant: %q", f.Detail)
	}
}

// --- crawlGap -----------------------------------------------------------------------------

func TestCrawlGapNeedsBothHistoryAndHumanDemand(t *testing.T) {
	now := time.Now().UTC()
	crawls := crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 20, 0, 200)

	evs := append(append([]event.Event{}, crawls...), humanViews("/blog/pricing-guide", now, 25)...)
	f, ok := crawlFinding(aiCrawlFindings(evs, now), "crawl_gap")
	if !ok {
		t.Fatalf("a page 25 people read that nothing has fetched is the most actionable row there is; got %+v",
			aiCrawlFindings(evs, now))
	}
	if f.Severity != "info" || f.Kind != KindCrawl || f.Value != "/blog/pricing-guide" || f.Rate != 25 || f.N != 5 {
		t.Errorf("wrong shape: %+v", f)
	}
	if !strings.Contains(f.Title, "No AI crawler has read /blog/pricing-guide") {
		t.Errorf("title must name the page: %q", f.Title)
	}
	if !strings.Contains(f.Detail, "25 people did, from 25 visitors, while the crawlers fetched 5 other pages here") {
		t.Errorf("detail must carry both counts and the coverage it is measured against: %q", f.Detail)
	}

	// thin human demand: an uncrawled page nobody reads is not worth a sentence
	quiet := append(append([]event.Event{}, crawls...), humanViews("/blog/pricing-guide", now, 12)...)
	if g, ok := crawlFinding(aiCrawlFindings(quiet, now), "crawl_gap"); ok {
		t.Errorf("12 views is not proven demand: %+v", g)
	}

	// no crawl history: with nothing being fetched, EVERY page is "uncrawled" and the finding
	// would be a lie dressed as a specific URL
	thin := crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 4, 0, 200)
	thin = append(thin, humanViews("/blog/pricing-guide", now, 25)...)
	if g, ok := crawlFinding(aiCrawlFindings(thin, now), "crawl_gap"); ok {
		t.Errorf("without a crawl history there is no gap to report: %+v", g)
	}

	// no human traffic at all: the crawlers have read everything anyone has
	if g, ok := crawlFinding(aiCrawlFindings(crawls, now), "crawl_gap"); ok {
		t.Errorf("no human demand, no gap: %+v", g)
	}
}

// --- the whole card -----------------------------------------------------------------------

// allFourFixture makes every crawler rule fire at once: ClaudeBot is being refused AND has
// gone quiet, OpenAI has never been here while two other operators have, and a page with real
// readers has never been fetched.
func allFourFixture(now time.Time) []event.Event {
	var evs []event.Event
	evs = append(evs, crawlSweep(now.Add(-10*24*time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 20, 12, 403)...)
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "PerplexityBot", "Perplexity", aicrawl.PurposeSearch, 20, 0, 200)...)
	evs = append(evs, humanViews("/blog/pricing-guide", now, 25)...)
	return evs
}

// Ordered by how upstream the cause is, so a reader working top-down never fixes a symptom
// first: being refused outright, then having lost a regular visitor, then never having been
// visited by one operator, then a single page nothing has reached.
func TestCrawlFindingsAreOrderedByCause(t *testing.T) {
	now := time.Now().UTC()
	fs := aiCrawlFindings(allFourFixture(now), now)
	var got []string
	for _, f := range fs {
		got = append(got, f.Metric)
	}
	want := []string{"crawl_errors", "crawl_stopped", "crawl_absent", "crawl_gap"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("crawler findings out of causal order: %v, want %v", got, want)
	}
}

// The causal chain of the whole module, end to end: can it be fetched (readability) → was it
// fetched (these rules) → what does the model then say (visibility). Anything else sends the
// reader to rewrite copy for a page a WAF is refusing to serve.
func TestCrawlSitsBetweenReadabilityAndVisibility(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	evs := append([]event.Event{}, funnelFixture()...)
	evs = append(evs, scan(now.Add(-time.Hour), map[string]any{
		"renders_without_js": false, "words_without_js": float64(6),
	}))
	evs = append(evs, crawlSweep(now.Add(-time.Hour), "GPTBot", "OpenAI", aicrawl.PurposeTraining, 20, 12, 403)...)
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)

	readableAt, crawlAt, visAt := -1, -1, -1
	for i, f := range Generate(evs) {
		if f.Kind == KindReadable && readableAt < 0 {
			readableAt = i
		}
		if f.Kind == KindCrawl && crawlAt < 0 {
			crawlAt = i
		}
		if strings.Contains(f.Title, "AI answers") && visAt < 0 {
			visAt = i
		}
	}
	if readableAt < 0 || crawlAt < 0 || visAt < 0 {
		t.Fatalf("expected all three, got readable=%d crawl=%d visibility=%d", readableAt, crawlAt, visAt)
	}
	if !(readableAt < crawlAt && crawlAt < visAt) {
		t.Errorf("wrong causal order: readable=%d crawl=%d visibility=%d", readableAt, crawlAt, visAt)
	}
}

// The regression that would poison everything else: robot fetches counted as product
// activity. One reporter distinct_id firing hourly is a perfectly retained user, and a crawl
// wave is a traffic spike, an anomaly, and eventually an invoice.
func TestCrawlReportsDoNotPolluteTheRestOfTheVerdict(t *testing.T) {
	now := time.Now().UTC()
	base := funnelFixture()
	withCrawl := append([]event.Event{}, base...)
	withCrawl = append(withCrawl, crawlSweep(now.Add(-10*24*time.Hour), "ClaudeBot", "Anthropic", aicrawl.PurposeTraining, 20, 12, 403)...)
	withCrawl = append(withCrawl, crawlSweep(now.Add(-time.Hour), "PerplexityBot", "Perplexity", aicrawl.PurposeSearch, 20, 0, 200)...)

	clean := Generate(base)
	dirty := Generate(withCrawl)
	var dirtyRest []Finding
	for _, f := range dirty {
		if f.Kind != KindCrawl {
			dirtyRest = append(dirtyRest, f)
		}
	}
	if len(dirtyRest) != len(clean) {
		t.Fatalf("crawl reports changed the product verdict: %d findings vs %d", len(dirtyRest), len(clean))
	}
	for i := range clean {
		if !reflect.DeepEqual(dirtyRest[i], clean[i]) {
			t.Errorf("finding %d changed with the reporter on:\n  clean: %+v\n  dirty: %+v", i, clean[i], dirtyRest[i])
		}
	}
	if len(dirty) == len(dirtyRest) {
		t.Fatal("the fixture was supposed to produce crawler findings too, or this proves nothing")
	}

	// counts, not just prose: every product number downstream is computed off the filtered
	// slice, so that slice has to come back byte-identical to the one with no crawls in it.
	filtered := withoutGeoChecks(withCrawl)
	if !reflect.DeepEqual(filtered, base) {
		t.Fatalf("the product half saw %d events, want the original %d", len(filtered), len(base))
	}
	visitors := func(evs []event.Event) int {
		seen := map[string]bool{}
		for _, e := range evs {
			seen[e.DistinctID] = true
		}
		return len(seen)
	}
	if got, want := visitors(filtered), visitors(base); got != want {
		t.Errorf("crawler visits moved the visitor count: %d vs %d", got, want)
	}

	for _, f := range dirty {
		if strings.Contains(f.Title+f.Detail, aicrawl.CrawlEvent) || strings.Contains(f.Title+f.Detail, "edge-reporter") {
			t.Errorf("the reporter's internals leaked into prose: %+v", f)
		}
	}
}

// The house vocabulary guard. A crawler report is the most jargon-dense thing in the digest —
// user agents, status classes, event names — and none of it belongs in a sentence someone
// reads over coffee.
func TestCrawlFindingsLeakNoInternals(t *testing.T) {
	now := time.Now().UTC()
	fs := aiCrawlFindings(allFourFixture(now), now)
	if len(fs) == 0 {
		t.Fatal("nothing to check")
	}
	banned := []string{
		"$", "crawl_errors", "crawl_stopped", "crawl_absent", "crawl_gap",
		"distinct_id", "user agent", "user-agent", "4xx", "5xx", "properties", "edge-reporter",
	}
	for _, f := range fs {
		prose := f.Title + " " + f.Detail
		for _, b := range banned {
			if strings.Contains(prose, b) {
				t.Errorf("implementation word %q in prose: %q", b, prose)
			}
		}
		if m := filterSyntax.FindString(prose); m != "" {
			t.Errorf("finding reads like a filter (%s): %q", m, prose)
		}
	}
}
