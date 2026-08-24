package api

import (
	"net/http"
	"strings"
)

// WHAT THIS INSTANCE IS, NOW THAT IT IS NOT A DASHBOARD.
//
// `GET /` used to render 5,188 lines of product-analytics UI: funnels, retention, heatmaps,
// cohorts, a deck of charts. That was the old product's shape, and it was the wrong thing to put in
// front of a customer for two reasons.
//
// It was a worse copy of a screen they already had. Anyone on PostHog or Mixpanel already has
// somewhere to look at conversion by country, and ours was computed from an event stream they did
// not set up.
//
// And it was in the wrong place. When a test fails, a person reads the pull request; if they click
// through, the page has to know their GitHub installation, their org and their billing. None of
// that is on a per-tenant machine, and all of it is on the control plane.
//
// So this instance is a data layer now. It ingests events, answers /v1 reports, serves MCP to the
// customer's own editor, and reads their existing analytics through the connector. Nothing about it
// is a screen.
//
// THIS PAGE IS NOT A DASHBOARD REPLACEMENT AND MUST NOT GROW INTO ONE. It exists so that a person
// who lands here — usually because they bookmarked the old dashboard — is told where their product
// went in one sentence, rather than meeting a 404 and assuming the service is broken.

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	// Anything that is not exactly "/" is a real miss. Serving this page for every unknown path
	// would turn every typo into a 200, which hides broken links from us and from crawlers.
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}

	cloud := s.cloudURL
	if cloud == "" {
		cloud = "https://smolanalytics.com"
	}

	w.Header().Set("content-type", "text/html; charset=utf-8")
	// No caching: the one thing this page does is point somewhere, and a stale copy pointing at a
	// dead location is worse than a slow one.
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write([]byte(rootPage(cloud, baseURL(r))))
}

func rootPage(cloudURL, base string) string {
	return strings.NewReplacer("{{CLOUD}}", cloudURL, "{{BASE}}", base).Replace(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex">
<title>smolanalytics · instance</title>
<style>
  :root{color-scheme:light dark;--fg:#111;--mut:#666;--line:#ddd;--bg:#fbfaf7;--acc:#a06800}
  @media(prefers-color-scheme:dark){:root{--fg:#eee;--mut:#999;--line:#333;--bg:#111;--acc:#e8b34a}}
  body{margin:0;background:var(--bg);color:var(--fg);
    font:15px/1.6 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
  main{max-width:36rem;margin:0 auto;padding:14vh 1.5rem 4rem}
  h1{font-size:1.35rem;margin:0 0 .75rem;letter-spacing:-.01em}
  p{margin:0 0 1rem;color:var(--mut)}
  a{color:var(--acc)}
  ul{margin:0;padding:0;list-style:none;border-top:1px solid var(--line)}
  li{border-bottom:1px solid var(--line);padding:.6rem 0;display:flex;gap:1rem;justify-content:space-between}
  code{font:12.5px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--fg)}
  .go{display:inline-block;margin-top:.5rem;border:1px solid var(--acc);color:var(--acc);
    padding:.5rem .9rem;text-decoration:none}
</style>
<main>
  <h1>This is a smolanalytics instance, not a dashboard.</h1>
  <p>It stores your events and answers questions about them. It does not have a screen — your test
     runs, your suite and your tracking plan live on your project page, which is also where your
     repository and your billing are.</p>
  <p><a class="go" href="{{CLOUD}}">Open your project &rarr;</a></p>
  <p style="margin-top:2.5rem">What this instance serves:</p>
  <ul>
    <li><span>Your editor, over MCP</span><code>{{BASE}}/mcp</code></li>
    <li><span>Reports, as JSON</span><code>{{BASE}}/v1/…</code></li>
    <li><span>Event ingest</span><code>{{BASE}}/v1/events</code></li>
    <li><span>Keys and retention</span><a href="/settings">settings</a></li>
  </ul>
</main>
`)
}
