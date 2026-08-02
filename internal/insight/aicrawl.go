package insight

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Findings about the AI crawlers' visits to the customer's own site — the retrieval
// half of AI visibility.
//
// Ordered by how directly the reader can act on them, which happens to be the same as
// how upstream the cause is: being served errors beats going quiet beats never being
// fetched at all beats a page nobody has read yet. Each one is silent unless the
// evidence is unambiguous; a crawler report is naturally spiky (a sweep, then days of
// nothing) and a detector that fires on ordinary spikiness would train the reader to
// ignore the whole card.

const (
	// enough hits before any rate is believable
	crawlMinHits = 12
	// a crawler is "regular" once it has fetched on this many separate days
	crawlRegularDays = 4
	// silence after regular activity that counts as having stopped
	crawlSilentDays = 7
	// error rate that earns a warn
	crawlErrorPct = 30
	// human views a page needs before "no crawler has ever read it" is worth saying
	crawlGapViews = 20
)

func aiCrawlFindings(evs []event.Event, now time.Time) []Finding {
	r := aicrawl.Compute(evs, 30, now)
	if !r.Reporting {
		return nil // nothing installed: silence, not a nag. The dashboard card asks.
	}
	var out []Finding

	// 1) we are actively refusing them. The only failure here that costs nothing to fix
	// and blocks everything downstream.
	if f := crawlErrors(r); f != nil {
		out = append(out, *f)
	}
	// 2) a regular visitor went quiet
	if f := crawlStopped(r, now); f != nil {
		out = append(out, *f)
	}
	// 3) a whole operator has never been here, while others have
	if f := crawlAbsentOperator(r); f != nil {
		out = append(out, *f)
	}
	// 4) a page people read that no assistant has ever seen
	if f := crawlGap(r); f != nil {
		out = append(out, *f)
	}
	return out
}

// crawlErrors: the crawlers are arriving and being turned away. Firewalls and bot
// rules are the usual cause, and the site owner is almost never aware — the crawler
// does not complain and the WAF dashboard calls it a win.
func crawlErrors(r aicrawl.Result) *Finding {
	worst := aicrawl.CrawlerRow{}
	for _, c := range r.Crawlers {
		if c.Hits < crawlMinHits {
			continue
		}
		if pctOf(c.Errors, c.Hits) >= crawlErrorPct && c.Errors > worst.Errors {
			worst = c
		}
	}
	if worst.Crawler == "" {
		return nil
	}
	rate := pctOf(worst.Errors, worst.Hits)
	return &Finding{
		Severity: "warn",
		Kind:     KindCrawl,
		Metric:   "crawl_errors",
		Value:    worst.Crawler,
		Rate:     rate,
		N:        worst.Hits,
		Title:    fmt.Sprintf("Your server is turning %s away", worst.Crawler),
		Detail: fmt.Sprintf("%d of its last %d fetches got an error (%d%%). %s runs it, and a crawler that cannot load the page cannot quote you in an answer — this is usually a bot rule or a WAF doing exactly what it was told, not a bug in your app. Allow it, or accept that %s's assistant will keep answering about your category without you in it.",
			worst.Errors, worst.Hits, rate, worst.Operator, worst.Operator),
	}
}

// crawlStopped: it used to come regularly and hasn't in a week. Usually a deploy that
// changed robots.txt, a new bot rule, or a sitemap that stopped resolving.
func crawlStopped(r aicrawl.Result, now time.Time) *Finding {
	// "regular" is carried by crawlMinHits + the silence being longer than the gap it
	// used to keep. A single big sweep followed by quiet is normal crawler behaviour
	// and must not fire, which is why the hit floor sits where it does.
	var best *aicrawl.CrawlerRow
	for i := range r.Crawlers {
		c := &r.Crawlers[i]
		if c.Hits < crawlMinHits || c.LastAt == "" {
			continue
		}
		last, err := time.Parse(time.RFC3339, c.LastAt)
		if err != nil {
			continue
		}
		quiet := int(now.Sub(last).Hours() / 24)
		if quiet < crawlSilentDays {
			continue
		}
		if best == nil || c.Hits > best.Hits {
			best = c
		}
	}
	if best == nil {
		return nil
	}
	last, _ := time.Parse(time.RFC3339, best.LastAt)
	quiet := int(now.Sub(last).Hours() / 24)
	return &Finding{
		Severity: "warn",
		Kind:     KindCrawl,
		Metric:   "crawl_stopped",
		Value:    best.Crawler,
		Rate:     quiet,
		N:        best.Hits,
		Title:    fmt.Sprintf("%s stopped fetching your site", best.Crawler),
		Detail: fmt.Sprintf("It read %d pages here and then nothing for %d days — last seen on %s. Whatever %s knows about you is now frozen at that date, so anything you have shipped since is invisible to it. Check robots.txt, any bot rule you added recently, and that your sitemap still resolves.",
			best.Pages, quiet, best.LastPath, best.Operator),
	}
}

// crawlAbsentOperator: only stated as a comparison. "OpenAI has never fetched you"
// on its own could just mean the reporter was installed yesterday; "Anthropic and
// Perplexity have, OpenAI hasn't" is a real asymmetry.
func crawlAbsentOperator(r aicrawl.Result) *Finding {
	if r.Hits < crawlMinHits*2 {
		return nil
	}
	seen := map[string]bool{}
	for _, o := range r.Operators {
		seen[o.Operator] = true
	}
	if len(seen) < 2 {
		return nil // one operator reporting is not evidence about the others
	}
	// the operators worth naming when absent, in the order a reader cares
	for _, want := range []struct{ op, why string }{
		{"OpenAI", "ChatGPT"},
		{"Anthropic", "Claude"},
		{"Perplexity", "Perplexity"},
	} {
		if seen[want.op] {
			continue
		}
		var others []string
		for _, o := range r.Operators {
			if o.Operator != "unknown" {
				others = append(others, o.Operator)
			}
		}
		if len(others) < 2 {
			return nil
		}
		sort.Strings(others)
		return &Finding{
			Severity: "info",
			Kind:     KindCrawl,
			Metric:   "crawl_absent",
			Value:    want.op,
			N:        r.Hits,
			Title:    fmt.Sprintf("%s has never fetched your site", want.op),
			Detail: fmt.Sprintf("%s crawled it in the last 30 days; %s did not, across %d fetches. %s answers from what it has read, so being absent from its index is the reason it can leave you out of an answer even when you are the better fit. Getting listed on a page it already reads is usually faster than waiting to be discovered.",
				joinAnd(others), want.op, r.Hits, want.why),
		}
	}
	return nil
}

// crawlGap: a page with proven human demand that no AI crawler has ever fetched.
func crawlGap(r aicrawl.Result) *Finding {
	if len(r.Gaps) == 0 || r.Hits < crawlMinHits {
		return nil
	}
	g := r.Gaps[0]
	if g.Views < crawlGapViews {
		return nil
	}
	return &Finding{
		Severity: "info",
		Kind:     KindCrawl,
		Metric:   "crawl_gap",
		Value:    g.Path,
		Rate:     g.Views,
		N:        r.Pages,
		Title:    fmt.Sprintf("No AI crawler has read %s", g.Path),
		Detail: fmt.Sprintf("%d people did, from %d visitors, while the crawlers fetched %d other pages here. An assistant cannot cite a page it has never read, so your best-performing content is invisible to them. Link to it from a page they do fetch, or list it in your sitemap.",
			g.Views, g.Visitors, r.Pages),
	}
}

// joinAnd renders a list the way a sentence needs it.
func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	}
	out := ""
	for i, x := range xs[:len(xs)-1] {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out + " and " + xs[len(xs)-1]
}
