# The prompt library

smolanalytics ships 14 built-in prompts: complete analytics workflows, not one-liners.
In Claude Code, Cursor, or any MCP client that supports prompts, they surface natively:
type `/`, pick one, and the whole routine runs. The model calls the right tools in the
right order and gives you the read, not a data dump. Because it runs in your editor, the
model has your code and the tracking plan too, so it can resolve a codebase name to the
right query (ask "MAU for the PQR page" and it knows PQR is the `/pqr` route).

Not on MCP? Every prompt also works pasted into the dashboard's ask bar or typed into any
editor chat, because the prompts are only instructions: the numbers come from the tools
underneath, which run exact, deterministic reports and answer with computed numbers,
never guesses. The dashboard ask bar answers about your data in plain English and shows
your real event names and pages as clickable chips, so you ask by name ("visitors to
/pricing", "how many checkout this week") without guessing. It does not read your code;
that is what your coding agent is for. Any report a prompt builds can be pinned with
`save_report`, so a recurring read renders on the dashboard every visit instead of being
re-run by hand.

Determinism is enforced in CI: [the agreement test](../internal/api/agreement_test.go)
asserts the MCP answer and the HTTP API answer are identical for the same question, every build.

The output shapes below use placeholders (`N of M users (X%)`); your instance fills in
the real numbers.

## Setup

### instrument-my-app

Run it in the first ten minutes on a new codebase, or whenever tracking needs a rebuild.

Reads your code to pick the events worth tracking, wires them, declares the plan on the
instance, verifies events actually flow, and leaves a safety-net alert behind.

`set_tracking_plan · instrumentation_health · create_alert`

```
Wired K events: signup, activate, checkout (+ autocaptured pageviews).
Tracking plan declared: K events, P expected properties.
instrumentation_health: N of K flowing; "checkout" MISSING. Run that flow once and I'll re-check.
Alert set: fire if signup < N in 24h.
```

## The rhythm

### whats-broken-today

Every morning, before you decide what to work on.

Reads the anomaly digest, instrumentation health over the last 24 hours, and the traffic
picture, then separates product problems from tracking problems.

`whats_notable · instrumentation_health · web_overview`

```
One thing changed: signup is down X% vs a typical day (N vs M), started around HH:00.
Tracking is healthy: all K planned events flowing, no missing properties.
Traffic is normal; referrer mix unchanged.
Most useful action today: <one line, and whether it's a product fix or a tracking fix>.
```

### did-my-deploy-break-anything

Right after you ship. Ties the numbers to the deploy behind them, so a regression is caught
in hours, not at the next weekly review.

Records the current deploy marker, then reads before/after impact on your headline conversion
event, pageviews, and any error-shaped events (30-day window, 3-day before/after), leading
with anything that regressed.

`record_deploy · deploy_impact`

```
deadbeef "tighten checkout validation" — 3 days after:
  signups −X% (was A/day, now B/day) — significant given the volume; that ship is the suspect.
  pageviews flat, errors unchanged.
Correlation, not proof — but check that deploy first.
```

### weekly-review

End of the week, when you want the recap a good cofounder would give you.

Reads headline trends this week vs last, the funnel and how it moved, retention for
recent cohorts, and the traffic mix, then offers to pin the most useful view.

`trends · funnel · retention · web_overview · save_report`

```
Signups: N vs M last week (±X%), driven by <source> (N of M, X%).
Biggest leak: <step> → <step>, X% continue (was Y%).
Retention: day-7 at X% for this week's cohorts (was Y%).
Traffic: N visitors (±X%); <referrer> is the mover.
The one thing to fix next week: <step>, it costs roughly N users a week.
```

### monthly-report

First of the month, or the night before an investor update.

Reads the month against the prior month across growth, conversion, retention, lifecycle,
engagement, and channel attribution: one narrative, every number computed.

`trends · funnel · retention · lifecycle · stickiness · web_overview · goal_report · save_report`

```
<Month>: N new users (±X% MoM), M weekly actives at month end.
Conversion signup → checkout: X% (prior month: Y%). Day-30 retention: X%.
Lifecycle: N new / M returning / K resurrected per day; dormancy trending <direction>.
Channels: <source> drove X% of conversions (first touch) on N visitors.
Watch next month: <one line>. Pinned as "<Month> report" if you want it on the dashboard.
```

## Diagnosis

### funnel-leak

When conversion feels off and you want the exact step, the worst segment, and a fix.

Reads the funnel step by step, breaks the weak step down by segment, and follows what
the drop-offs do instead.

`funnel · breakdown · paths`

```
signup → activate → checkout: N → M (X%) → K (Y%).
The leak: activate → checkout, X% continue, worst in <property>=<value> at Y%.
Where drop-offs go: X% do <event> next; Y% do nothing.
Fix candidate: <one line>. Want an alert if this step drops further?
```

### retention-review

Monthly, or any time "do users come back?" starts nagging you.

Reads cohort retention, the daily lifecycle mix (new / returning / resurrected /
dormant), stickiness, and which segments retain best: the activation lever.

`retention · lifecycle · stickiness · breakdown`

```
Day-1: X% · day-7: Y% · day-30: Z% (recent cohorts).
Direction: day-7 is ±N points vs cohorts from a month ago.
Lifecycle: dormant users outpacing resurrected by N/day.
Stickiness: DAU/MAU at X%.
The lever: users who did <event> retain at X% vs Y% without it.
```

### channel-review

Before you spend the next unit of marketing effort.

Reads conversions per channel (first-touch attribution), volume vs quality per source,
and how each channel is trending.

`goal_report · breakdown · trends · web_overview`

```
By conversions (30d, first touch): <source> N (X%), <source> M (Y%), direct K.
Volume vs quality: <source> sends the most visitors (N) but converts at X%, below the Y% average.
Underrated: <source>, only N visitors, but X% of them convert.
The shift to make: <one line>.
```

## Search

All three need Search Console connected: one-time `smolanalytics gsc auth`.

### search-performance

When you want the Google side of the story: which queries bring people in, and what
those people do after they land.

Reads top queries and the biggest movers from Search Console, then connects them to
on-site behavior and conversions.

`search_console_report · web_overview · goal_report`

```
Top queries (28d): "<query>", N clicks / M impressions / X% CTR / position P.
Movers: "<query>" +N clicks, "<query>" −M since the last pull.
After landing: X% of search visitors reach <goal> (site average: Y%).
Quick win: "<query>", position P with M impressions but X% CTR; title rewrite candidate.
```

### content-gaps

When you're deciding what to write or improve next.

Reads the queries where you earn impressions but not clicks, or rank just off the first
page, against the pages you already have.

`search_console_report · web_overview`

```
Gap: "<query>", M impressions at position P, only N clicks; no page targets it directly.
Improve: <path> ranks P for "<query>", M impressions, X% CTR.
Extend: <path> already pulls N visitors across K adjacent queries.
Next page to write: "<working title>", targets <query cluster>, M combined impressions.
```

### money-pages

When you want the SEO wins already within reach, no new content required.

Reads the page-level cut of Search Console: pages ranking 4-15 (one push from page-1
clicks), pages that rank fine but whose snippet doesn't earn the click, and queries
split across competing pages.

`search_console_report`

```
Quick win: <path>, "<query>" at position P with M impressions; try <one change>.
CTR problem: <path>, "<query>" earns X% CTR where position P typically earns Y%; rewrite the title.
Cannibalization: "<query>" splits between <path> (N clicks) and <path> (M clicks); consolidate into the first.
```

## Big days, many products

### launch-day

The day you ship: the HN post is live, the tweet is out, and you want one screen of truth.

Reads live visitors right now, the raw events as they arrive, today's numbers against a
normal day, and whether tracking is holding up under the load.

`web_overview · recent_events · trends · funnel · instrumentation_health`

```
LIVE: N visitors on the site right now; M in the last hour.
Signups today: N (a typical day is M by this hour).
Driving it: <referrer> N, <referrer> M; the <channel> post is the engine.
Landing → signup today: X% (baseline: Y%).
Tracking is holding: all planned events flowing, ingest normal.
```

### portfolio-review

When several products report to one instance and you want a single read across all of them.

Reads everything split by site (every event carries its site's hostname, so one
breakdown covers the whole portfolio) and flags the product that needs attention.

`breakdown · trends · whats_notable`

```
<product-a>: N actives (±X% WoW), growing.
<product-b>: flat, N signups vs M last week.
<product-c>: best day-7 retention in the portfolio at X%.
Needs attention: <product>, <one line, with the number>.
The rest: nothing anomalous.
```

## What to try next

### growth-experiments

Idea time: what to test next week, anchored to measured numbers instead of vibes.

Reads the digest, the funnel, retention, and the segment splits, then proposes
experiments where each one cites the number that motivated it and names the exact
report that will judge it.

`whats_notable · funnel · retention · breakdown · create_goal · save_report`

```
1. <experiment>, because only X% of <segment> reach <step> (N of M users).
   Judge it with: funnel, filtered to <property>=<value>.
2. <experiment>: <source> converts at X% vs the Y% average; lean in.
   Judge it with: goal_report, weekly.
3. <experiment>: day-1 retention is X%. Judge it with: retention on next week's cohorts.
Want the goals and saved reports set up now?
```

## Roll your own

A prompt is nothing exotic: instructions over the same 73 tools your model already has.
If a routine keeps coming up ("compare pro vs free every Friday", "check both funnels
after each deploy"), write it once into your repo's `CLAUDE.md` / `AGENTS.md` /
`.cursorrules` and it becomes part of how your agent works, no MCP plumbing required.
The pattern, with a copy-paste block, is in [docs/agents.md](agents.md).
