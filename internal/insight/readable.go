package insight

import (
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The finding with the largest effect size in the whole AI-visibility module, and the one no
// GEO tool looks for: a site an AI crawler cannot physically read scores zero on every prompt,
// forever, no matter how good the product is.
//
// The major AI crawlers execute NO JavaScript. Googlebot does, which is exactly why this hides:
// a client-rendered app can rank perfectly well in Google search and be a blank page to
// ChatGPT. The GEO monitors measure the answers and never look at the retrieval; the SEO
// crawlers that could see it never connect it to "you are absent from these prompts". This
// instance holds both, because the cloud records each scan as a $site_readable event on the
// same log the visibility checks land in.
//
// Deliberately ahead of the visibility findings in the digest: if the page cannot be read,
// every other AI-visibility number is a symptom and this is the cause.

// ReadableEvent is what the cloud's scanner writes after checking the project's own site.
const ReadableEvent = "$site_readable"

func siteReadability(evs []event.Event, now time.Time) *Finding {
	// newest scan wins; older ones are history, not evidence about today
	var latest *event.Event
	for i := range evs {
		e := &evs[i]
		if e.Name != ReadableEvent || e.Timestamp.After(now) {
			continue
		}
		if latest == nil || e.Timestamp.After(latest.Timestamp) {
			latest = e
		}
	}
	if latest == nil {
		return nil
	}
	p := latest.Properties
	blocked, _ := p["robots_blocks_ai"].(bool)
	renders, hasRender := p["renders_without_js"].(bool)
	words, _ := p["words_without_js"].(float64)
	url, _ := p["url"].(string)
	if url == "" {
		url = "your site"
	}

	// robots.txt first: a blocked crawler reads nothing, so it outranks everything else.
	if blocked {
		detail, _ := p["robots_detail"].(string)
		return &Finding{
			Severity: "warn",
			Kind:     KindReadable,
			Metric:   "robots",
			Title:    "Your robots.txt turns the AI crawlers away",
			Detail: fmt.Sprintf("%s — so ChatGPT, Claude and Perplexity cannot read a single page, and no amount of content or reputation changes that while it stands. If blocking them is deliberate, nothing here is wrong; if it came from a template, this one line is the whole ceiling on your AI visibility.",
				firstNonEmpty(detail, "robots.txt disallows them")),
		}
	}
	if hasRender && !renders {
		return &Finding{
			Severity: "warn",
			Kind:     KindReadable,
			Metric:   "render",
			Rate:     int(words),
			Title:    "An AI crawler sees an almost empty page on your site",
			Detail: fmt.Sprintf("%s returns %d words of text before JavaScript runs, and the AI crawlers do not run it — Googlebot does, which is why this can hide behind perfectly good search rankings. Whatever your app paints after it boots, ChatGPT and Claude never see. Server-render or pre-render the route so the words are in the HTML itself.",
				url, int(words)),
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
