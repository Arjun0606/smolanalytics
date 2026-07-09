package mcp

// MCP prompts — pre-canned workflows the client surfaces natively (slash-command-like
// in Claude Desktop/Code). Each returns a user message that drives the model through a
// multi-tool routine, so "do my weekly review" is one click instead of prompt-crafting.

import "encoding/json"

var promptList = []map[string]any{
	{
		"name":        "instrument-my-app",
		"description": "Wire smolanalytics into the current codebase, declare a tracking plan, and verify events flow — the full setup loop.",
	},
	{
		"name":        "whats-broken-today",
		"description": "The morning check: anomalies, funnel leaks, and instrumentation health, with what to do about each.",
	},
	{
		"name":        "weekly-review",
		"description": "A founder-grade weekly product review: growth, conversion, retention, traffic — with the one thing to fix next week.",
	},
	{
		"name":        "monthly-report",
		"description": "A client-grade monthly report: growth deltas, funnel, retention, traffic, search winners and losers — ending in 3 next-month actions.",
	},
	{
		"name":        "search-performance",
		"description": "Search Console deep read: query movers, the pages they land on, what searchers do next — output as ranked opportunities.",
	},
	{
		"name":        "content-gaps",
		"description": "Queries earning impressions without rank, crossed against existing pages — a prioritized content list with the query data as evidence.",
	},
	{
		"name":        "funnel-leak",
		"description": "Find the biggest drop-off, isolate the segment driving it, and leave with one measurable fix.",
	},
	{
		"name":        "channel-review",
		"description": "Which acquisition channel actually converts — a keep/cut/watch table with honest sample sizes.",
	},
	{
		"name":        "retention-review",
		"description": "The honest retention read: D1/D7, lifecycle, stickiness, what retained users do differently — ending in 2 levers.",
	},
	{
		"name":        "launch-day",
		"description": "Live launch monitoring: real-time traffic and referrers, today vs baseline, and a drop alert as the safety net.",
	},
	{
		"name":        "portfolio-review",
		"description": "For multi-product instances: compare every site's pulse and name the product that earned the week's attention.",
	},
	{
		"name":        "growth-experiments",
		"description": "Exactly 3 measurable experiments from real baselines — hypothesis, metric, tracking-plan additions, and the alert that catches the result.",
	},
	{
		"name":        "money-pages",
		"description": "The page-level SEO wins already in reach: near-page-1 quick wins, snippets that rank but don't earn the click, and pages cannibalizing each other.",
	},
}

var promptText = map[string]string{
	"instrument-my-app": `Instrument this codebase with smolanalytics, end to end. Don't hand-write the plan — let the tools read the repo and hand you the exact edits:
1. Call propose_instrumentation (pass repo_path=the project root, plus the project's host + write key if you have them). It returns the base autocapture snippet and where it goes, plus the exact track() calls to add at the signup / login / checkout / activation call-sites it found — each with file, line, snippet, and properties.
2. APPLY those edits with your editor: insert the snippet in the file it names, and add each track() call at the given file:line, filling property values from the surrounding code (stable distinct_id per user; identify() on login). Confirm anything low-confidence with me first.
3. Declare what you wired with set_tracking_plan using the returned plan.
4. Ask me to run the app and click through the flows, then call verify_instrumentation — it cross-references the code and live traffic and returns FIRING / WIRED / MISSING per event. For anything MISSING, call suggest_instrumentation_fix and apply the patch. Repeat until every planned event is FIRING.
5. Finish by setting a drop alert on the most important event (create_alert) and telling me exactly what you set up. Autocapture already has pageviews, clicks, and engagement — this step is only the custom conversion events.`,

	"whats-broken-today": `Run my morning check:
1. whats_notable for the verdict (anomalies, biggest funnel leak, retention read).
2. instrumentation_health (last 24h) — is tracking itself healthy?
3. web_overview — anything unusual in traffic or referrers?
Then give me the read like an analyst: what changed, what's broken (product vs tracking), and the single most useful action today. If everything's fine, say so in one line.`,

	"weekly-review": `Do my weekly product review:
1. trends on the headline events, this week vs last (use breakdown by source or plan where it's telling).
2. funnel — where's the biggest leak and did it move?
3. retention — is day-1/day-7 improving for recent cohorts?
4. web_overview — traffic mix shifts worth knowing.
Synthesize: 3-5 bullets a founder actually needs, each with the number and what it means. End with THE one thing to fix next week, and offer to save the most useful view with save_report.`,

	"monthly-report": `Build my monthly report — founder-grade, client-ready:
1. overview to orient, then trends on the headline events, this month vs last — lead with the deltas that matter.
2. funnel on the core path — conversion now vs last month.
3. retention — did this month's cohorts stick better than earlier ones?
4. web_overview (days=30) — traffic, top pages, referrer-mix shifts.
5. search_console_report — the winning and losing queries (skip quietly if GSC isn't connected).
Write it in sections a client could read cold: the number, the delta, one line of meaning. End with exactly 3 bullets under "Do this next month", each tied to a number above.`,

	"search-performance": `Deep-read my search performance (needs Search Console — say so and stop if it isn't connected):
1. search_console_report (limit 50) — top queries plus the movers: which are gaining or losing clicks, and where position shifted.
2. web_overview — the top pages those searchers land on.
3. paths (start "$pageview") and funnel from landing to the signup-shaped event, filtered to search traffic (referrer contains google) — what do those visitors actually do?
Output ranked opportunities: query → its click/position trend → the landing page → what visitors did there → the one change that captures more of them. Ignore queries with too few impressions to mean anything.`,

	"content-gaps": `Find the content I should create next (needs Search Console — say so and stop if it isn't connected):
1. search_console_report (limit 50) — keep the queries with real impressions but weak position (roughly 8 or worse): demand we're not capturing.
2. web_overview — the pages that exist today. Cross-reference: which of those queries has no dedicated page answering it?
Output a prioritized content list — for each: the target query, impressions, current position, the nearest existing page (or "none"), and the page to build. Rank by impressions x position gap. The query data IS the evidence — cite it under every item.`,

	"funnel-leak": `Find my biggest funnel leak and assign blame:
1. whats_notable — it names the biggest drop-off and the worst segment through it.
2. funnel on the core steps for the exact numbers, then re-run it with filters by source, device, and plan (breakdown first to see which properties exist) — is one segment dragging the average?
3. paths from the leaking step — what do drop-offs do instead?
Output three lines: THE leak (step → step, rate), the segment driving it (its rate vs everyone else), and one measurable fix with the number that should move. Offer create_alert on the leaking step's event.`,

	"channel-review": `Tell me which acquisition channel actually converts:
1. goal_report on the main conversion goal (create_goal on the signup-shaped event first if none exists) — conversions with first-touch attribution by referrer and utm_source.
2. breakdown of that conversion event by source for the raw split.
3. web_overview — the referrer mix including the AI-assistant channel (chatgpt/claude/perplexity), so volume sits next to conversions.
Be honest about sample size: a channel with 5 visitors proves nothing — mark it "watch", don't rank it.
Output a table: channel | visitors | conversions | rate | keep/cut/watch.`,

	"retention-review": `Give me the honest retention read:
1. retention (7 days) — the real day-1/day-7 numbers for recent cohorts.
2. lifecycle — new vs returning vs resurrected vs dormant: are we filling the bucket faster than it leaks?
3. stickiness — DAU/WAU/MAU and the DAU/MAU ratio.
4. paths from the signup event — what do users who come back do early that churned users don't?
Output: the day-1/day-7 verdict in one line, then exactly 2 retention levers — each naming the early behavior to push and the retention number that proves it worked.`,

	"launch-day": `It's launch day — be my situation room:
1. web_overview — LIVE visitors right now, and the referrers: where is the spike actually coming from?
2. trends on signups and $pageview, today vs the trailing week — how big is this, really?
3. recent_events — eyeball the stream; breakage under load shows up here first.
4. Safety net: create_alert for a signup drop on a short window (op=lt); add_webhook to Slack if I give you a URL.
Give me a running situation read each pass: traffic vs baseline, top source, and whether signups keep pace with visitors — conversion lagging traffic is the launch-day bug signal, flag it immediately.`,

	"portfolio-review": `Review my whole portfolio (every product reports here — the SDK stamps each event with its site):
1. breakdown (property "site") — the activity split across products.
2. web_overview per product with filters [{"property":"site","op":"eq","value":"<site>"}] — visitors, referrers, live-now for each.
3. trends on each site's headline event (same site filter) — growing, flat, or fading?
4. whats_notable — attribute anything anomalous to its product where the event mix allows.
Output one line per product — visitors, headline number, delta — then the verdict: which product earned this week's attention, and the single number that says why.`,

	"growth-experiments": `Design my next growth experiments from measured baselines, not vibes:
1. overview, funnel, retention — pull the current numbers: volume, per-step conversion, day-1/day-7.
2. Find the 3 softest numbers a plausible change could actually move.
Propose exactly 3 experiments, each with: a falsifiable one-sentence hypothesis; the metric and its current baseline (the real number you just measured); the tracking additions needed — new events or properties, declared via set_tracking_plan; and the create_alert that will catch the result moving.
No experiment without a baseline: if the data is too thin, say which experiment to skip and what volume unlocks it.`,

	"money-pages": `Find my money pages — the SEO wins already within reach (needs Search Console — say so and stop if it isn't connected):
1. search_console_report — read the money_pages section. If it says page data isn't fetched yet, relay that note and stop.
2. quick_wins are page/query pairs ranking 4-15: proven relevant, one push from page-1 clicks.
3. ctr_problems rank fine but the snippet doesn't earn the click. cannibalization is one query split across competing pages.
Output three lists:
- Quick wins: page | query | position | impressions — plus the ONE change to try (title/meta rewrite, add the query's exact phrasing, internal links to the page, or a content refresh — pick per row, don't say "improve SEO").
- CTR problems: page | query | expected vs actual CTR — the ranking is earned, the title/description isn't; propose the rewrite.
- Cannibalization: query | the competing pages | which page to consolidate into (usually the one with more clicks) and what to do with the loser (redirect, canonical, or retarget it).
Skip rows with too few impressions to mean anything. Rank each list by impressions at stake.`,
}

// dispatchPrompts handles prompts/list and prompts/get. Returns nil when the method
// isn't a prompts method.
func (s *Server) dispatchPrompts(method string, params json.RawMessage, reply func(any) *response, fail func(int, string) *response) *response {
	switch method {
	case "prompts/list":
		return reply(map[string]any{"prompts": promptList})
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &p)
		text, ok := promptText[p.Name]
		if !ok {
			return fail(-32602, "unknown prompt: "+p.Name)
		}
		return reply(map[string]any{
			"messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": text}},
			},
		})
	}
	return nil
}
