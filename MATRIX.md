# smolanalytics vs the big three: the honest matrix (2026-07-14)

> **STATUS UPDATE (2026-07-17): the layout/presentation gap this matrix opens with is CLOSED.**
> The report-page teardown below describes the pre-rebuild dashboard (a 13-tab rail, one
> hardcoded signup bar chart, KPI tiles with no deltas). Since then the dashboard was rebuilt
> and now ships: a fluid `display:grid` of always-visible report **tiles** (no tab rail);
> a **metric selector** on the main chart (any event / by device / os / source / utm — not
> hardcoded signup); a **chart+table unit** (data table below the chart, prior + change
> columns); **KPI tiles with period-over-period delta pills** and ellipsis+title labels (no
> text clipping); a **conditional compare-ghost** (only rendered when prior data exists, so no
> phantom legend); a **sticky filter/date toolbar**; and `body{overflow-x:hidden}` removed.
> Engine paper-cuts from build-priority 4 are also fixed: `interval=hour/week/month` honored,
> `unique=1`/`rolling=1` boolean parse, `measure=` name validation, `/v1/shares` 200 (was 404),
> and — via the 2026-07-17 hardening — breakdowns/filters by acquisition props (device/referrer/
> utm) first-touch-attribute so `signup by device` returns real segments, not `(none)`, on all
> four surfaces. **Build priorities 1–5 are effectively done; the genuine remaining gap is
> feature DEPTH (priorities 6–12): 30/90-day retention triangles, advanced funnel options,
> paths sankey, cohort builder, board-v2.** The rows below are kept as the original snapshot.

## matrix

### Report-page skeleton (builder placement)  [behind]
- mixpanel: Left query-builder rail: Metrics → Filters → Breakdown stacked blocks, per-metric measurement picker; top bar = date + compare + viz switcher; chart center; sortable table always below
- amplitude: Left rail: Events (10 max, per-event filter+group-by) → Measured As → Segment By modules; 'Previous Period vs.' button directly above chart; breakdown table below; full keyboard shortcut set
- posthog: Left resizable EditorFilters panel (420-600px, collapsible groups w/ collapsed-state summaries) + right card [display-config bar / chart / metadata] + detailed-results table OUTSIDE below the card
- **us**: No builder panel at all — Explore/cohorts/defined-events (the actual query builder) hidden in a collapsed <details> at y=1088 BELOW the footer fold; controls scattered across 3 misaligned center columns (x=214 / x=290 / x=340)
- **verdict**: behind — missing the entire canonical skeleton: no persistent builder, no chart+table unit, no single alignment grid; the one thing all three incumbents converge on

### Trends: measurements & granularity  [behind]
- mixpanel: Totals, uniques, DAU/WAU/MAU, frequency (avg/median/P25-P90/min/max), property aggregates (sum/avg/median/percentiles/distinct), sessions-with-event, rolling avg, cumulative; hourly→monthly intervals
- amplitude: Uniques, totals, active %, average, frequency histogram, sum/avg/median/distribution of property, distinct values per user; real-time→yearly intervals
- posthog: Per-series math menu (total/DAU/sum/avg/median/p90 property math, session duration math), smoothing, sampling; hour/day/week/month
- **us**: /v1/trends has unique=true, measure=sum|avg|median|p90 + property, custom from/to — but DAY-GRAIN ONLY (interval=week silently ignored), one event per call; unique=1 silently ignored (boolean parse bug), measure name unvalidated
- **verdict**: behind — engine has the aggregations but no interval param (no hour/week/month), no DAU/WAU/MAU-as-measurement on trends, no frequency/distribution; API param parsing is sloppy

### Multi-metric reports & formulas  [behind]
- mixpanel: Up to 40 metric/formula blocks per report; arithmetic formulas over lettered metrics; funnel+retention metrics inside Insights; saved formulas (Growth+)
- amplitude: 10 events per chart + full formula library: UNIQUES/TOTALS/PROPSUM/HIST/PERCENTILE/REVENUETOTAL/ARPAU/CUMSUM/ROLLAVG/TRENDLINE (OLS) + math fns
- posthog: Multi-series ActionFilter rows with rename/duplicate, formulaNodes ('A/B'), multiple y-axes
- **us**: One event per /v1/trends call; no multi-series, no formulas, no A/B metric arithmetic anywhere
- **verdict**: behind — zero multi-metric capability; can't even chart signups vs checkouts on one canvas, let alone compute a ratio

### Breakdown / segmentation  [behind]
- mixpanel: Multiple simultaneous breakdowns w/ hierarchy, manual locked-value segments, top/bottom N, uniques-with-segment-attribution, custom bucketing
- amplitude: Up to 5 group-bys per event (top 12 values), saved segments side-by-side, '+ Performed' behavioral conditions, wildcard search
- posthog: Multi-breakdown, histogram bins, per-result color customization, breakdown on funnels/retention/paths
- **us**: Single breakdown= param on trends+funnel+/v1/breakdown; click-any-segment-bar-to-filter is live and nice — BUT demo custom events carry no geo/device/utm props so every breakdown of signup/activate/checkout renders one grey '(none)' series: the flagship feature looks broken on our own demo
- **verdict**: behind — single-dimension only, no top-N control, no multi-breakdown; and the demo actively sabotages what does exist

### Chart types & display options  [behind]
- mixpanel: Line/stacked line/column/stacked column/bar/stacked bar/pie/metric/table + annotations, 4 sort orders, sub-totals
- amplitude: Chart/KPI/Table per card, y-axis rename/min-max/units/log/second axis, % change toggle, takeaway + target metrics
- posthog: Line/area/bar/stacked/box/slope/cumulative/number/sparkline/pie/table/world map/calendar heatmap; log scale, value labels, goal lines, confidence intervals, trend lines, moving avg, hide weekends — all behind one Options menu badged with non-default count
- **us**: One hardcoded bar chart (signup/day, 150px plot), y-axis shows exactly 2 labels against 3 unlabeled gridlines, prior-window ghost bars all render height:0 while the legend still promises them
- **verdict**: behind — one chart type, broken axis labeling, phantom legend; no options menu, no annotations, no goal lines

### Chart+table pairing (legend-as-table)  [behind]
- mixpanel: Every time-series chart ships an inseparable sortable data table with toggleable segments and sub-totals
- amplitude: Breakdown data table below every chart is core anatomy
- posthog: InsightsTable = the legend: series checkbox toggles chart visibility, color swatch, inline rename, aggregation-picker column header, sticky first column
- **us**: No data table under the main chart at all; breakdown panes show 3 bar rows (122px of content) with no sort, no toggle, no totals
- **verdict**: behind — the single highest-leverage incumbent pattern is entirely absent

### Date range & interval controls  [behind]
- mixpanel: Today/Yesterday/7/30d/3-6-12mo/WTD-YTD presets, Last-N rolling, Fixed, Since, minute granularity, guardrails per grain
- amplitude: Presets + between-ranges, real-time→yearly intervals, per-chart timezone
- posthog: InsightDateFilter + IntervalFilter on the left of the display-config bar, invariant position
- **us**: 7/30/90 presets + custom from/to in a bordered mid-page toolbar (45px row for 5 tiny controls), separate from header and from the chart; no interval picker (day only), no Since/rolling semantics
- **verdict**: behind — presets exist but no interval control (engine lacks it) and the control floats in its own orphaned toolbar instead of the report bar

### Compare-to-past  [behind]
- mixpanel: Compare to Past (prev day/week/month/quarter/year or custom offset) + Compare to Segment + Compare to Overall + % lift over baseline
- amplitude: 'Previous Period vs.' first-class button above chart, green ghost series, % change toggle, Behavior Offset chart
- posthog: CompareFilter (previous period / custom -1y relative) in the config bar on every insight
- **us**: Prior-window ghost exists on the one main chart only — and in the demo every ghost bar is 0-height so the comparison is invisible; no % change readout, no compare on funnels/retention/breakdowns
- **verdict**: behind — concept present on one chart, not generalized, and visually broken where it does exist; KPI tiles show no period-over-period deltas at all

### Funnels  [behind]
- mixpanel: Window to 366d, optimized re-entry, specific/any order, uniques/totals/sessions counting, 6 measurements incl time-to-convert percentiles, exclusion steps, hold-3-props-constant, first/last/per-step attribution, comparison events, p-value significance, View-as-Flow
- amplitude: This/Any/Exact order, clock-time OR session window, exclusions, hold-constant, optional steps, combine events, conversion drivers, A/B view, time-to-convert distribution, conversion-over-time w/ alerts
- posthog: ordered/strict/unordered, exclusions scoped from-step→to-step, step reference total|previous, breakdown attribution first/last/all/step-N, time-to-convert bins, correlation analysis, aggregate by HogQL
- **us**: /v1/funnel: 2+ steps, window= duration, breakdown=, per-step conversion_from_top/prev + dropped + median_conversion_secs, overall conversion — no ordering modes, no exclusions, no per-step filters, no hold-constant, no attribution, no time-to-convert distribution, no significance
- **verdict**: behind — solid minimal core (window + breakdown + median time) but missing every advanced option all three incumbents document; funnel is also buried as tab #6 of 13

### Retention  [behind]
- mixpanel: A→B return event, on/on-or-after/on-or-before/streak modes, custom variable brackets (d1-3, 4-7, 15-30), rolling vs calendar, 4 measurements incl property sum/avg, triangle heatmap + trend line + frequency view
- amplitude: Up to 2 return events, on-or-after/on/custom (100 brackets), Change-Over-Time axis flip, Usage Interval view, N-Day LTV, Microscope drill on any cell
- posthog: recurring/first-ever, hour-month period, custom brackets, reference-to-previous-interval, cumulative, min occurrences, weighted mean row, breakdowns
- **us**: /v1/retention: bucket=day|week, event= returning event, rolling=true, cohorts w/ returned[] — but max_days:7, so the ENTIRE horizon is D0-D7; no on/on-or-after modes, no brackets, no over-time flip, no drill; rolling=1 silently ignored
- **verdict**: behind — a 7-day-max retention grid is a toy next to 30/90-day triangles; return-event + rolling are genuine seeds to build on

### Paths / flows / journeys  [behind]
- mixpanel: Sankey w/ multi-anchor start+end, expand-by-property on any node, hide/expand events, top-50 paths list, cohort-colored parallel flows, exclusions, hold-constant, 366d window
- amplitude: Pathfinder flow + Sunburst + Journey Map (paths ordered by frequency/similarity/completion time, converted vs dropped side-by-side), convert-path-to-funnel, save drop-off cohorts
- posthog: Start+end points, wildcards ('/merchant/*/payment'), path cleaning regex, edge limits, exclusions, funnel-conjoined paths (before/between/after a funnel step)
- **us**: /v1/paths?start= returns 3 levels of next-step counts as plain lists — depth capped at 3, no end-anchor, no sankey/visual, no exclusions, no expand-by-property, not surfaced in the dashboard tab rail at all
- **verdict**: behind — endpoint exists but is depth-3 text output vs interactive sankeys; zero UI surface

### Lifecycle & stickiness  [parity on engine, behind on surface]
- mixpanel: Lifecycle via board template (Lifecycle Cohort Analysis); DAU/WAU/MAU as first-class measurements
- amplitude: Usage Interval view; Active % measurement; stickiness via custom formulas
- posthog: Dedicated Lifecycle insight (new/returning/resurrecting/dormant bands, toggleable) + Stickiness insight (≥N times in interval, cumulative)
- **us**: /v1/lifecycle (per-day new/returning/resurrected/dormant) and /v1/stickiness (dau/wau/mau/dau_over_mau) both live and verified — but neither has ANY dashboard surface; API-only
- **verdict**: parity on engine, behind on surface — data is computed and correct, invisible to a dashboard user

### User profiles & journeys  [behind]
- mixpanel: Users page (profiles vs all users), behavior filters, edit columns, in-app profile edit, CSV import/export, save-cohort-from-list
- amplitude: Deepest: search by ID/device/conditions, pinned properties, Activity stream w/ session-grouping + LIVE toggle + raw JSON, select-10-events→pivot to funnel, Insights/Replays/Cohorts/Experiments tabs
- posthog: Person profiles w/ event history, properties, cohort membership, recordings
- **us**: /v1/users/{id} per-user journey works and is linked from the dashboard; no user SEARCH/list page, no behavior-filtered user browser, no pivot-to-chart from a user's stream
- **verdict**: behind — single-user lookup exists; the list/search/filter half is missing

### Cohorts  [behind]
- mixpanel: did/did-not w/ count+lookback, first-time, historical property values, AND/OR groups, dynamic+static CSV, sync to Segment/Braze/webhooks; free tier can't SAVE cohorts
- amplitude: Richest: relative count of two events, count-in-interval, 1st-5th historical occurrence, '…then' sequencing, within-X-days-of-first-use, cohort nesting, predictive ML
- posthog: Behavioral + static cohorts, usable as filters/breakdowns everywhere
- **us**: /v1/cohorts CRUD verified live + funnel explore has a cohort selector; criteria vocabulary undocumented/thin, no save-cohort-from-any-segment drill, builder hidden in collapsed details
- **verdict**: behind — CRUD skeleton exists; missing did-not-do, count thresholds, sequencing, and the create-from-report-segment loop that makes cohorts compound

### Dashboards / boards composition  [behind]
- mixpanel: 12-column grid, 4 cards/row, 1/12 snap + min 3/12, drag grippers, row-height control, rich text + media cards, linked reports, board-level date+filter that FOLLOWS into reports, TV mode, templates, nested boards
- amplitude: Chart/KPI/Table per card, cohort + replay + image + video cards, takeaway/target metrics, dashboard-wide interval/date/filters, CSV/PDF/PNG export, TV mode 5-min refresh
- posthog: Insight cards on dashboards, tile grid w/ col-span + order, per-tile Export/Open-as-insight/Show-more
- **us**: One pinned board of saved ask-cards (/v1/insights GET/POST/DELETE) — no grid, no sizing, no text/media cards, no board-level date/filter override, no multi-board
- **verdict**: behind — a pin-list is not a dashboard product; this is the largest single feature-surface gap

### Global filters & filter UX  [parity on mechanics (regex + URL-state filters beat Amplitude's free tier), behind on presentation]
- mixpanel: Global filter button + inline per-event filters; board-level temp filters; typecasting at query time
- amplitude: Segment module property filters + behavioral '+ Performed'; dashboard Add Filter, copy-URL shares filtered view
- posthog: AND/OR property groups, one Filters popover badged with active count, closable midEllipsis pills, click-to-edit taxonomic picker
- **us**: ?f=prop:op:value + fm=any|all with 9 operators (eq/neq/contains/ncontains/regex/gt/lt/set/notset) — genuinely good, URL-shareable — but the builder renders EXPANDED WITH EMPTY INPUTS on page load (hidden attr not honored), occupying a permanent row; no AND/OR groups, no pill-per-filter chips with edit
- **verdict**: parity on mechanics (regex + URL-state filters beat Amplitude's free tier), behind on presentation — fix the always-open builder, add badged popover + pills

### Alerts & digests  [behind on surface]
- mixpanel: Threshold (above/below/±delta) on Insights+Funnels, email/Slack/webhooks, notification windows, test-fire, history; anomaly forecasting = Enterprise; board digests top-8 email/Slack
- amplitude: Zero-setup automatic monitors (99% CI), custom threshold/%-change/CI alerts, group-by tracks top 1000 segments, funnel alerts; gated Plus+/Growth
- posthog: Alert threshold lines + anomaly points rendered on the chart itself
- **us**: Engine has alerts + Slack webhooks per /v1 inventory — no dashboard surface to create/manage them, no thresholds visible on charts, no email digest
- **verdict**: behind on surface — engine primitive exists; note both incumbents PAYWALL alerts, so shipping free threshold+Slack alerts is an ahead-opportunity

### Sharing, public links & embeds  [behind]
- mixpanel: Viewer/Editor per board, public boards w/ optional password, iFrame embeds (Notion/Coda/Confluence); permission control Growth+
- amplitude: Public links for charts/dashboards/notebooks w/ password+expiry+revoke-to-404, iframe embed tab, Notion/Atlassian smart links, per-recipient schedules
- posthog: Shared dashboards + embeds
- **us**: Share links minted at /settings/shares (GET /v1/shares 404s — endpoint asymmetry); no password/expiry, no iframe embed mode, no revoke story
- **verdict**: behind — basic share exists; no embed, no password/expiry/revoke; the /v1/shares 404 is a bug

### Web analytics overview (KPI + dimensions)  [parity on data, behind on presentation]
- mixpanel: Not a web-analytics product; requires building boards
- amplitude: Not a first-class web dashboard; heatmaps at Plus
- posthog: 5-tile overview w/ previous-value + semantic delta arrows (isIncreaseBad flips color for bounce), then tabbed tiles: paths/sources(channels+UTMs)/devices/geo(map!)/retention/active-hours/goals on ONE scrollable grid
- **us**: /v1/web returns 11 dimensions in one payload (visitors, pageviews, live_now, top_pages, referrers, geo, devices, campaigns…) + GSC — data parity with PostHog — but rendered as 13 one-at-a-time tabs hiding 122px of content each, KPI tiles have no deltas, labels overflow tile borders at every width
- **verdict**: parity on data, behind on presentation — this is the founder's screenshot; the fix is layout, not engine

### Live / realtime  [parity]
- mixpanel: No dedicated live view (100-sample event hover)
- amplitude: User Activity LIVE toggle per user; real-time interval on charts
- posthog: Live events tab + '3 now' style presence
- **us**: 'N now' live pill in header, 'last event 1m ago' ticker, live tab, /v1/events/recent with full properties
- **verdict**: parity — live surface is fine as-is

### Natural-language ask  [ahead]
- mixpanel: None in-product at this depth
- amplitude: Dashboard Agent + Global Agent/MCP natural-language editing (LLM-based, non-deterministic)
- posthog: Max AI (LLM-based)
- **us**: POST /v1/ask: DETERMINISTIC, no model, returns answer + computed_by + intent; verified answers byte-match /v1/funnel; CI agreement test asserts ask == dashboard == MCP
- **verdict**: ahead — deterministic, provenance-cited, CI-proven ask is a genuine differentiator no incumbent has; but it must sit on top of a credible dashboard or it reads as a gimmick

### Agent / MCP integration  [ahead]
- mixpanel: None documented
- amplitude: MCP + AI agents included even on free tier
- posthog: MCP server exists
- **us**: 47 read-only MCP tools, one-click Cursor deeplink, claude mcp add one-liner, VS Code link, connect-your-agent sheet; MCP-guided agent INSTRUMENTATION is the stated #1 USP
- **verdict**: ahead — widest MCP tool surface and the only instrumentation-via-agent story; keep the connect sheet prominent but below the report, not competing with it

### Data trust & transparency  [ahead]
- mixpanel: 10% sampling at Enterprise 2B+ events; result caching w/ manual refresh
- amplitude: Chart cache, 10-min public-link cache
- posthog: Optional sampling factor, computation-time metadata row under every chart
- **us**: Footer covenant: 'every row, every event — no sampling, no thresholding' + 'page computed in 51ms'; deterministic engine
- **verdict**: ahead — no-sampling covenant + per-page compute-ms is a positioning weapon; steal PostHog's move of putting computed-in-Xms on EVERY chart card, not just the footer

### Drill-down (datapoint → users → cohort)  [behind]
- mixpanel: Click segment → View Users (save cohort) / View Events / hover 100 samples
- amplitude: Microscope on any datapoint → view users, create cohort, watch replays; retention cell drill
- posthog: Person modals from any insight datapoint
- **us**: Segment bars click-to-FILTER (data-fp/data-fv) — good — but no datapoint → user-list → save-cohort loop anywhere; users reachable only via events/journeys
- **verdict**: behind — click-to-filter is half the loop; missing view-users + save-cohort closes the analytics flywheel

### Annotations, goals & targets  [behind]
- mixpanel: Annotations on all time-series charts
- amplitude: X-axis annotations + release markers + target metrics w/ progress bars
- posthog: Annotations, goal lines w/ labels, alert threshold lines rendered on chart
- **us**: POST /v1/goals + goals tab exist; no chart annotations, no release markers, no goal lines drawn on charts
- **verdict**: behind — goals stored but never visualized; annotations absent entirely

## layout blueprint
- GLOBAL FRAME — kill .wrap{max-width:1060px}. New frame: fluid width, max-width 1560px, padding 24px, on a single 12-column CSS grid (grid-template-columns: repeat(12,1fr); gap:16px). ONE left alignment edge for everything — delete the 860px verdict column and 760px ask column; every zone spans grid columns and left-aligns to the same x. This alone reclaims the measured 380px dead at 1440 / 860px dead at 1920 and fixes 'placements all over the place' (the three competing alignment systems at x=214/290/340).
- ZONE 1: HEADER (48px, full frame width) — logo | site selector | flex spacer | live pill ('3 now') | last-event ticker | agent pill | share | settings. Remove .bar{overflow:hidden;flex-wrap:nowrap} — below 900px collapse right-side items into a single overflow '⋯' menu instead of clipping (PostHog folds filters behind an icon on mobile; never CSS-clip). Date/compare/filters do NOT live here — they belong to the report bar (Zone 3) like all three incumbents.
- ZONE 2: ASK HERO (the identity, kept, but 60px not 250px) — one full-width ask input, calm and quiet. The three permanent example-chip rows (measured 103px) collapse INTO the input's focus/empty state: show 3-4 example prompts as a dropdown under the focused input, exactly like a command palette (Mixpanel/Amplitude put examples inside the builder, never as page furniture). When an answer comes back, it renders as a card in Zone 5's grid with its computed_by provenance line — that card is what 'pin to board' saves. Net: 244px of control chrome above the first insight drops to ~60px.
- ZONE 3: STICKY FILTER BAR (40px, PostHog FilterBar verbatim) — position:sticky top:0 after scroll. LEFT: date presets (7/30/90/custom from-to, existing params) + interval picker (day now; hour/week/month when engine ships it) + Compare toggle (prior-period ghost on/off — generalizes the existing ghost). RIGHT: one 'Filters' button with an active-count badge opening the existing ?f=prop:op:value builder as a POPOVER (fix the hidden-attr bug that renders empty inputs on load), plus active filters as closable midEllipsis pills. Invariant stolen from all three: date+compare LEFT edge, filters/display options RIGHT edge, one row, size-small controls.
- ZONE 4: VERDICTS (grid col 1-12, three lines max) — keep the brief-powered sentences, fix the measured collision: .vline gets padding-left:20px with the bullet dot absolutely positioned (hanging indent so wrapped lines don't return flush under the dot), and an explicit ' — ' separator between .vt and .vd so 'day-7 7%' + 'of 991 users' can never concatenate into a different sentence. 'why? →' pinned right of line 1, not floating at wrap-end. This is OUR row — Mixpanel/Amplitude have nothing here; keep it above the KPIs as the calm ask-first signature.
- ZONE 5: KPI GLANCE ROW (grid col 1-12; 6-7 tiles) — rebuild as PostHog's OverviewItem: value + previous-period value + delta pill with SEMANTIC color (isIncreaseBad flips red/green for bounce rate). Fix the only true text-cutoff bug: remove white-space:nowrap on .gtile .l, add overflow:hidden + text-overflow:ellipsis + title= attr, min tile width 150px, grid auto-fit minmax(150px,1fr). Full label on hover. Skeleton placeholders match final tile count while loading.
- ZONE 6: MAIN REPORT CARD (grid col 1-8 at ≥1280px, col 1-12 below) — one bordered card with PostHog's InsightVizDisplay anatomy: (a) card-top config strip (border-b, 8px padding): metric select (visitors/pageviews/signups/any event — kills the hardcoded signup chart) + breakdown select LEFT; chart-type toggle (bar/line) + Options menu badged with non-default count RIGHT; (b) thin metadata row: 'computed in 51ms' moved up from footer + refresh — the covenant ON the chart, per-card; (c) chart area min-height 20rem (up from the 150px plot): label ALL gridlines (min 4 y-ticks), hide the ghost legend entirely when every prior value is 0 (never promise an invisible comparison), compare ghost as outline bars; (d) BELOW the chart INSIDE the card: the breakdown/series table — checkbox column toggling series visibility (legend-IS-the-table), color swatch, segment name, total, per-bucket values, sticky first column, horizontal scroll inside the card, 11px uppercase headers, sortable. Chart+table as one inseparable unit is the pattern all three incumbents share and we lack.
- ZONE 7: SIDE RAIL (grid col 9-12 at ≥1280px, stacks below at smaller) — hour-of-day strip (exists, 90px) + live-now card (events/recent tail, 5 rows, links to full live view) + pinned-board preview (top 3 pinned ask-cards, 'view board →'). Uses the width currently thrown away.
- ZONE 8: REPORT TILE GRID (replaces the 13-tab rail entirely) — the measured page is 1295px total; tabs hide 122px panes behind 13 clicks. Consolidate into PostHog's web-analytics tile grid: 'grid-cols-1 md:grid-cols-2 2xl:grid-cols-3', each tile a TABBED CARD (header: title + ⓘ popover left; segmented tab switcher w/ overflow-▼ dropdown right; footer: Export · Open in explore · Show more→modal). Tiles and spans: PAGES{pages|entry|exit} col-span-1 · SOURCES{referrers|channels|utm src/med/camp} col-span-1 · GEO{countries|regions|cities} col-span-1 · DEVICES{device|browser|os} col-span-1 · FUNNEL (steps bar + conversion + median-time, from /v1/funnel) col-span-2 · RETENTION (D0-D7 triangle w/ intensity shading + week1/week2 headline) col-span-2 · LIFECYCLE (stacked new/returning/resurrected/dormant — endpoint exists, zero surface today) col-span-2 · STICKINESS (dau/wau/mau + dau/mau ratio) col-span-1 · GOALS col-span-1 · CAMPAIGNS folds into SOURCES tabs · SEARCH (GSC) col-span-1 · LIVE+EVENTS become one 'activity' tile col-span-1 linking to full pages. Tile chart heights capped 16-18rem (PostHog's WebTile--short-chart trick); every tab swaps the DIMENSION, never the layout (same visitor/views/% columns). Every row keeps click-to-filter. Result: the whole product visible in ~2.5 scrolls instead of 13 clicks.
- ZONE 9: EXPLORE PROMOTION — the query builder (explore/cohorts/defined events) leaves its collapsed <details> grave below the footer. Two-step: (step 1, now) 'Explore' becomes a first-class header link to its own page; (step 2) that page adopts the incumbent skeleton — left panel (min 320px, collapsible groups: Event → Measure → Filters → Breakdown, each with a collapsed-state summary line like '2 filters') + right card with config bar / chart / table-below. Every tile's 'Open in explore' deep-links its current query. This page is where funnel window/breakdown, retention bucket/rolling, cohort criteria all get their UI.
- ZONE 10: CONNECT + FOOTER — connect-your-agent sheet stays as a collapsed card at page bottom (it's onboarding, not analysis; don't let it compete with reports) but gets a persistent quiet '⌁ agent' header pill opening it. Footer keeps the full covenant sentence + total page compute-ms; per-card ms now also lives on each card (Zone 6b pattern).
- DENSITY RULES (applied everywhere): controls size-small (26-28px height) in all card headers; table headers 11px uppercase letter-spaced (PostHog's density trick); tile body text 13px; card padding 12-16px; grid gap 16px; NEVER white-space:nowrap without overflow:hidden+ellipsis+title; NEVER overflow:hidden on flex bars that can shrink (wrap or fold to ⋯ menu); wide tables scroll inside their card (overflow-x:auto) with sticky first column — then delete body{overflow-x:hidden}, which currently masks bugs instead of preventing them; ⓘ-popover for help text, never inline paragraphs; empty states show a one-line CTA ('no UTM data yet — tag a campaign link'), not a bare '(none)' bar.
- IDENTITY GUARDRAILS (what NOT to steal): stay server-rendered — every control is a real <a>/<form> mutating query params (?days ?f ?breakdown already work this way), tabs are links, JS only progressively enhances (popover open, series toggle, live tick). No dark-pattern upsell chrome, no 40-block query builders on the main page — the main page stays a zero-config default report (calmer than Mixpanel's blank-builder cold start); depth lives in Explore. The verdict sentences + deterministic ask + covenant remain the top-of-page personality; the incumbent skeleton is adopted BELOW that signature, not instead of it.

## build priorities
### STATUS (2026-07-17): 1–8 DONE. 9–12 are agent-accessible today (create via MCP: create_alert, create_cohort, save_report) and remain only as dashboard-UI conveniences. ###
### ✅ 1–5 DONE — grid tile layout, text-integrity, chart+table+metric-selector, demo/paper-cuts, compare-ghost.
### ✅ 6 DONE — retention runs to 30/90-day (max_days cap 90), day/week/month buckets, rolling(on-or-after) mode; lifecycle+stickiness+paths all have dashboard tiles now (not API-only).
### ✅ 7 DONE — funnel has order modes (ordered/strict/unordered), exclusion events, per-step filters, AND time-to-convert median + p25/p75/p90 (added 2026-07-17).
### ✅ 8 DONE (engine) — multi-series trends (events=a,b,c → one canvas), interval=hour/week/month, measure=sum/avg/median/p90/p95/p99 + dau/wau/mau all live; a dedicated left-builder Explore PAGE is the only remaining UI-convenience slice.
### 9–12 = dashboard-UI conveniences (drill→cohort, alerts-creation form, board-as-grid, paths-sankey). The CAPABILITY exists via the agent (MCP) + engine; these are "click it in the dashboard too" polish, not capability gaps.
1. ✅ Layout rebuild sprint: fluid 1560px 12-col grid with one alignment edge; kill the 13-tab rail in favor of the always-visible tile grid (Zone 8); sticky filter bar with date/compare left + badged Filters popover right; collapse the 3 example-chip rows into the ask input's focus state. Pure HTML/CSS/vanilla-JS on existing endpoints — zero engine work.  — closes: 'space wasted' (380-860px dead width), 'placements all over the place' (3 alignment systems), 'unintuitive' (13 clicks to see 122px panes), and 244px of control chrome before the first insight
2. Text-integrity fixes (one day, surgical): glance labels get ellipsis+title and minmax(150px,1fr); verdict lines get ' — ' vt/vd separator + hanging indent via absolutely-positioned bullet; header folds to ⋯ menu below 900px instead of overflow:hidden clipping; delete body{overflow-x:hidden} and give wide tables their own overflow-x:auto+sticky-first-column.  — closes: 'text cut off' — every reproduced overflow at 1440/1920/1024/820, the vt/vd run-on that changes sentence meaning, and the sub-600px header clip
3. Chart+table unit on the main report card: metric selector (any event, not hardcoded signup), ≥4 labeled y-ticks, ghost legend hidden when prior window is all-zero, and the breakdown table below the chart with checkbox-toggles-series (legend-as-table), sort, totals, per-card computed-in-Xms.  — closes: the incumbents' single most universal pattern (chart+sortable table as one unit) plus the broken axis and phantom-ghost-legend defects; moves the covenant onto every chart
4. Demo credibility fix: stamp device/country/source/utm props onto the demo's custom events (signup/activate/checkout) so breakdowns return real segments instead of '(none)'; add empty-state CTA copy for genuinely-empty dimensions; accept '1' as boolean for unique/rolling; validate measure= names; fix GET /v1/shares 404.  — closes: the flagship breakdown feature looking broken on our own demo — the most-demoed incumbent capability currently self-sabotaged — plus three verified API paper cuts
5. Compare-to-past everywhere: generalize the prior-window ghost to a Compare toggle in the sticky bar affecting KPI tiles (delta pills w/ semantic red/green), the main chart, and tile charts; add % change readout. Engine already computes prior windows for /v1/brief.  — closes: the first-class 'Previous Period vs.' button all three incumbents put above the chart; KPI tiles currently show zero period-over-period context
6. Retention horizon + modes: extend /v1/retention past max_days:7 to 30/90-day triangles (day/week/month buckets), add on vs on-or-after semantics and 2-3 custom brackets (D1/D7/D30); render the intensity-shaded triangle + week-over-week trend line in the retention tile.  — closes: the D0-D7-only toy grid vs incumbents' 30/90-day triangles with mode switches — the biggest pure-engine capability gap for a SaaS buyer
7. Funnel depth, phase 1: ordering mode (ordered/strict/unordered), per-step property filters, and one exclusion step in /v1/funnel; expose window + these in the funnel tile's config and Explore; add time-to-convert beyond median (p25/p75/p90 — engine already does percentiles elsewhere).  — closes: the funnel option gap vs all three incumbents (order modes, exclusions, step filters) — table stakes for anyone comparing funnels side-by-side
8. Explore promotion: first-class header link now; then the left-builder-panel page (collapsible Event→Measure→Filters→Breakdown groups with collapsed summaries, chart+table right) with 'Open in explore' deep links from every tile; multi-series (2-3 events on one canvas) + interval=hour/week/month in /v1/trends to power it.  — closes: the query builder buried in a collapsed <details> below the footer, single-event day-grain-only trends, and no multi-metric canvas — the 'feature-poor' verdict's core
9. Drill-down loop: click any datapoint/segment → 'view users' list → 'save as cohort'; wire cohorts into funnel/trends filters everywhere; user list page with property + did/did-not-do filters (users/{id} and cohorts CRUD already exist).  — closes: the Microscope/View-Users flywheel both incumbents treat as core; converts our click-to-filter half-loop into the full segment→users→cohort→filter cycle
10. Alerts + goals surface: UI to create threshold alerts (above/below, existing engine alerts + Slack webhooks) from any chart's Options menu; draw goal lines and alert thresholds on charts; weekly email/Slack digest of the pinned board (top-8 pattern). Ship FREE.  — closes: engine capability with zero surface today — and both incumbents paywall alerts (Mixpanel Growth+, Amplitude Plus+), so free threshold+Slack alerts flips a behind row to ahead
11. Board v2: pinned board becomes a real grid (2-3 col, card spans, reorder), ask-answer cards + tile snapshots pinnable, board-level date override, text note cards; public share link with revoke.  — closes: the dashboards-of-many-charts composition gap (Mixpanel 12-col boards / Amplitude dashboards) — deferred to 11 because a world-class default report page matters more than composition for a single-site product
12. Paths visual: render /v1/paths as a simple 3-level flow (stacked columns with proportional links, hide-event control, click-node-to-filter) in a col-span-2 tile; raise depth to 5 when the tile proves usage.  — closes: the endpoint-with-no-UI gap vs Sankey/Pathfinder — lowest priority because depth-3 data can't compete with incumbent journeys yet; ship the honest small version