# Dashboard redesign — research → principles → spec

Why this exists: the last dashboard change ("turn the rail into a router") reorganized
navigation — it split 21 reports into subscreens. That is information *re-filing*, not
design. This document does it properly: what the best dashboards actually do, what the
psychology says, and the redesign that follows from both.

## Part 1 — research

### What the loved dashboards do (and why people love them)

**Plausible / Fathom — the "10-second read".** The entire product is ONE screen:
a top bar (site + date range), one graph with 5-6 headline metrics you can click to
swap the graph, then ranked panels below (Sources, Pages, Locations, Devices, Goals)
in a fixed order. Two structural moves carry the whole product:

1. **Click-to-filter everywhere.** Any row in any panel filters the *entire*
   dashboard. There is no "funnel builder" for "how did Google visitors do" — you
   click Google. The report builder is the dashboard itself.
2. **Expand for depth.** Each panel has a details view (sortable, more columns).
   Depth exists, but it costs one deliberate click and never greets you.

The philosophical line from the comparisons: GA4 treats analytics as a data warehouse
you must learn; Plausible treats it as a page you can read. People don't love the
metrics, they love *being done in 10 seconds*.

**PostHog / Mixpanel — powerful, and the ICP bounces off.** The consistent indie-dev
critique: busy interface, steep learning curve, "looks powerful but overwhelming",
requires planning to even instrument correctly. Nobody criticizes the *capability* —
they criticize being handed a toolbox when they asked a question. That's the gap we
claim: PostHog-grade computation behind a Plausible-grade front door.

**The "so what" literature.** Leaders are "drowning in data, starving for insight";
charts raise questions but don't answer them; numbers rarely change behavior unless
connected to a decision. The emerging pattern (Smart Narratives, insight-led views) is
to open with *sentences* — findings — and let charts be the evidence, not the message.
We already compute this (the verdict/whats_notable engine); the redesign makes it the
front door instead of one card among many.

### The psychology that constrains the layout

- **The 5-second rule.** Within 5 seconds of load a user must know "is my thing OK,
  and is anything broken?" — without scrolling, filtering, or choosing. If the first
  screen requires a choice (which subscreen?), the design has already failed this.
- **Working memory: 3-5 chunks** (up to ~7 at best). The glance layer holds at most
  5-7 numbers. Everything past that must be *summarized* (a sentence) or *deferred*
  (a click away). 21 reports on a rail = 21 chunks = fatigue by navigation.
- **Hick's law.** Decision time grows with number of choices. Every rail item is a
  choice the user pays for on every visit. Corollary: the default view must require
  ZERO choices — choices appear only after intent (a click) exists.
- **Progressive disclosure, concretely** (3 layers):
  glance (3-7 KPI cards, readable in ~2s) → detail (one click: full chart/breakdown)
  → configuration (filters, ranges, exports — behind an affordance). Rules: depth must
  be exactly one click away, disclosure patterns must be consistent across every
  panel, reveal on click not hover, always collapsible back.
- **Data-ink.** Pixels that aren't information are subtracted attention. Applies to
  chrome: borders, duplicated headers, navigation for navigation's sake.
- **Trust through freshness + provenance.** 2026-era guidance: show when data was
  computed and what window it covers, prominently. For us this is doctrine anyway —
  "computed, not guessed" should be *visible*, not a tagline.

### What this means for OUR user (solo builder, checks daily-ish)

The user's real questions, in the order they actually ask them:
1. "Anything broken / anything I should know?"      ← verdict, not a chart
2. "How's traffic / usage trending?"                ← one graph, headline metrics
3. "Where do people come from, what do they do,
    where do they leave?"                           ← ranked panels, click-to-filter
4. "The thing I personally care about"              ← pinned questions
5. Everything else (flags, surveys, agent obs, …)   ← tools, not reports; behind
                                                       explicit intent

A daily-check product is a *newspaper*, not a *library*. The rail-of-21 is a library
index. The newspaper has a front page: headline (verdict), weather (KPIs), and
sections you open when the headline sends you there.

## Part 2 — the design (see implementation notes below)

### Principles (each traceable to research above)

P1. **One screen answers the visit.** The default view is complete: verdict +
    headline graph + ranked panels. No choice required to get value (5s rule, Hick).
P2. **Findings first, charts as evidence.** The verdict sentences open the page.
    Every finding deep-links to the report that *shows* it (so-what literature).
P3. **The dashboard is the report builder.** Click any row → the whole page filters.
    Depth = expand-in-place, one click, consistent affordance on every panel
    (Plausible's two moves; progressive disclosure rules).
P4. **3-layer disclosure, no third-level nav.** glance → expanded panel → full
    report. The rail collapses to: Overview, plus *tools* (things you operate:
    flags, surveys, settings), never parallel copies of the data.
P5. **Provenance visible.** Window, computed-at, and event-count on the page —
    trust is the product (freshness transparency + our covenant).
P6. **Would Neon ship this?** Serious-infra bar for every element: purposeful,
    quiet, no decoration. (Positioning constraint, feedback memory.)

## Part 3 — what the audit found (why zones must die)

The `?zone=` router is a 9-line CSS block hiding `.deck` children. Consequences:
every "subscreen" is the full glance re-rendered plus 1-5 cards; the 22-item rail is
dead navigation in the default zone (click → `preventDefault` → `scrollIntoView` on a
`display:none` element → silent no-op); `#deckjump` advertises "22 reports" over a
page showing zero; three separately-built navigations index the same seven sections;
all ~12 pane loaders fetch on every zone because they gate on element existence, not
visibility; and the verdict is computed before site/env scoping, so it can describe
traffic you're not even looking at. None of this is a redesign problem — it is the
*absence* of design. The commit's own comment ("the rail is a MAP, NOT A ROUTER")
survives in the file, now false.

## Part 4 — implementation spec

One document again — but composed, not dumped:

1. **Zones deleted.** `?zone=` parsing, the zone CSS, `#zonebar`, `#deckjump`, the
   JS zone switcher, and the flat fallback index all go. One page, three strata.
2. **The glance leads with the verdict.** Order: verdict → KPIs → provenance line →
   trend chart. Ask chips compress to one horizontal row after the chart (they are
   shortcuts, not headlines). The hourly chart moves behind a `<details>`.
3. **Verdict is scope-honest.** Computed AFTER site/env scoping (it describes what
   you're looking at); window-independent by design (it reports *now* — the range
   control scopes the reports, not the diagnosis). The duplicated JS re-render of
   the verdict is deleted — server render is the only render.
4. **Panels get consistent one-click depth.** Every ranked list shows its top rows;
   beyond that, one `show all (N) →` toggle per panel — the same affordance
   everywhere (progressive-disclosure rule: consistent, click-not-hover,
   collapsible).
5. **The rail becomes true again.** Section-level links (7 strata + tools) that
   scroll to visible sections. No dead clicks.
6. **Loaders go lazy.** One `saLazy` IntersectionObserver helper; below-fold pane
   loaders fetch when approached. The glance stops paying for 21 invisible reports.
7. **Token discipline.** Ad-hoc font sizes (10.5/16.5/17/22/27/34px) fold back into
   the four-size scale; duplicate CSS rules deduped; inline `order:` styles deleted
   (document order IS the order now).
8. **Provenance visible** (P5): window · computed-at · event count, one quiet mono
   line on the glance.

Dropped ids are removed from dashboard_inventory.json in the same commit, per that
test's own contract.

