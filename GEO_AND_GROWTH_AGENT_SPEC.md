# Build spec: GEO module + auditable growth agent (2026-08-01)

Greenlit by Arjun. Bar: "super super well" — rip the best of the incumbents,
improve where they're structurally stuck. Ship order: growth-agent v1 (mostly
reuse) → GEO v1 (the new wedge) → iterate both.

---

## A. The auditable growth agent (verdict → fix, provably)

**What PostHog self-driving does** (rip): signal → their background agent diagnoses
→ opens a PR with instrumentation/flags/experiments. **Where they're stuck**
(improve): it's THEIR agent — a black box running on your repo, on a platform with
bill anxiety; you can't see why it decided anything.

**Our v1 — "brief YOUR agent" (ships in days, not weeks):**
Every verdict finding gets a **Fix with your agent** action that produces a
FIX BRIEF: the finding + the computed evidence (the exact report rows) + repo
pointers + acceptance criteria ("re-run the funnel; step-2 conversion should rise").
Delivered as: copy-block + editor deeplinks (the connect sheet already computes
cursor/claude-code deeplinks with keys baked in) + `fix_brief` MCP prompt so the
agent can pull it natively. Differentiator vs PostHog: your agent, your review, our
provably-computed evidence. AUDITABLE because every number in the brief carries its
computed_by provenance (ask-bar pattern reused).

**v2 — we open the PR** (reuse `fixRepo`/`openFixPr` — the runner exists for deploy
regressions): extend trigger from "regressing commit" to "verdict finding"; for
funnel findings the runner reads the page files for the leaking step instead of a
commit diff. Draft PRs only, spend-capped (FIX_MAX_SPEND_USD exists). This is the
PostHog-parity move; v1's brief flow stays the default (trust ladder: brief → PR).

Engine work: none for v1 (verdict + provenance exist). Cloud work: brief renderer
(server), a `fix_brief` addition to the engine MCP prompts (S), dashboard verdict
card action + email CTA. Copy rule: never "AI will fix it" — "your agent gets the
evidence".

---

## B. GEO / AI-visibility module

**What Profound/Peec sell** (rip the report shapes): prompt tracking ("best X for
Y" asked daily across engines), share-of-voice vs competitors, sentiment/accuracy
of how the LLM describes you, citations (which sources the answer pulled), plus
"what sent traffic". **Where they're stuck** (improve): $95-$2,000/mo marketing-team
pricing, dashboard-only, zero agent-nativity, and none of them own the OTHER half —
real referral traffic — which we already measure (`ai_referrers`).

**The architecture decision that makes ours structurally better: GEO checks are
EVENTS on the project's own instance.** The cloud runner POSTs each check as a
`$geo_check` event (props: engine, prompt, mentioned, recommended, rank, sentiment,
competitors_mentioned, description_hash, verbatim excerpt). Consequences, all free:
- every surface gets it instantly (dashboard pane, /v1, MCP tools, ask bar) via the
  existing single query path — agreement guarantee included, no second store
- trends/breakdown/alerts/retention machinery apply ("alert me if Claude stops
  recommending us" = existing alert on $geo_check where recommended=false)
- the verdict engine sees visibility shifts next to traffic shifts: "Claude stopped
  recommending you this week — AI referrals -40%" is a finding NOBODY else can
  compute because nobody else holds both halves.

**v1 scope (September per GAMEPLAN, start now):**
1. Cloud cron `geo-check` (weekly, per active project w/ opt-in): run the project's
   prompt set (default: derived from site title/description + "best <category> for
   <audience>" templates; user-editable list, cap ~10) against engines we hold keys
   for — Anthropic first (key exists), OpenAI/Perplexity env-gated like every other
   integration. Parse: mentioned? recommended? rank among named tools? competitor
   names? one-line verbatim. POST as $geo_check events with the project write key.
2. Engine: nothing structural (events flow) + a `geo_visibility` report/tool that
   aggregates $geo_check (share-of-voice over time, per engine, vs competitors,
   latest verbatims) + web_overview ai_referrers cross-referenced. Insight rule:
   visibility shift + referral shift = lead finding.
3. Dashboard: one pane (share-of-voice line, per-engine status row, latest
   "how Claude describes you" verbatim, ai-referral overlay) + the alert preset.
4. Pricing: included in Pro. The wedge IS the price gap.
5. Honesty rails: sampling variance is real — n runs per prompt, report
   "recommended in 3/5 runs", never a single-sample verdict (same low-n discipline
   as everything else). Verbatims marked with run date + model version.

Costs: ~10 prompts × 5 runs × weekly ≈ trivial Anthropic spend per project;
cap per org, env-tunable.

**Copy (site, later — after it works):** "The AI search console. See what ChatGPT
and Claude say about you, and what they send you — in the same analytics your agent
already operates." Comparison pages: vs Profound (price), vs Peec (agent-native +
referral truth).

---

Sequencing within the greenlight: A-v1 this week (reuse-heavy), B-v1 cron+events
next, B engine report + pane, then A-v2. Every piece: three surfaces + agreement
locks + low-n honesty, per house rules.

---

## B-addendum: the v1 recipe (methodology research, 2026-08-01)

Distilled from Peec/Profound/Otterly/Scrunch/Trakkr teardowns + variance studies:

- **Prompt set: 25, auto-generated** (Claude reads the site's homepage → category,
  use-cases, competitors): 10 category "best X for Y", 5 use-case, 5 comparison vs
  detected competitors, 3 problem-solution, 2 branded. BRANDED CAPPED ≤10% — the
  incumbents' rule, branded prompts inflate visibility. User-editable list.
- **Sampling: 3 runs/prompt/day, temperature 1, report 7-day rolling aggregates.**
  Research: within-prompt resampling = 34.8% of total variance in a 12,933-response
  study — single samples are noise; ≥10 runs for coarse estimates; MoE ≈ ±8pp at
  n=100. Never report single-day numbers (existing low-n Note already enforces the
  spirit; aggregation window does the rest).
- **Two modes, labeled honestly**: ungrounded ("does the model KNOW you") vs
  web_search-grounded ("does the live web SURFACE you") — API-vs-UI divergence is
  real and the honest tools label it. Convention: mode is encoded in the engine
  name (`claude` vs `claude-grounded`) — zero schema change, separate rows for free.
  Grounded is the cost driver ($10/1k searches): 1 grounded run/day vs 3 ungrounded.
- **Parsing: Haiku judge, strict JSON** per answer: {mentioned, rank (position among
  brands listed), recommended, sentiment, competitors[], cited_urls[]}; regex
  pre-check for the exact brand string as a sanity signal. Judge over regex is the
  consensus (name variants, false positives).
- **Citations**: Anthropic web_search returns cited sources → capture whether the
  user's DOMAIN was cited (their content earning retrieval, distinct from mention).
- **Metrics shipped** (aivis already computes most): visibility % (mentioned rate),
  recommended rate, avg rank, weekly share-of-voice trend, competitor mentions,
  latest verbatim + model id per engine. Add later: first-mention rate, domain
  citation rate, sentiment rollup.
- **Cost envelope**: 25 prompts × 3 runs × 30d ungrounded + Haiku judging ≈ low
  tens of $/brand/mo; grounded gated to daily-1. Cap per org, env-tunable.
  Incumbent pricing for the same: €90-2,000/mo. That gap is the wedge.

Engine status: internal/aivis SHIPPED (aggregation + tests green, commit 5cf61f7).
Next: /v1/aivis + MCP tool + agreement lock → dashboard pane → cloud geo-check
cron implementing this recipe → verdict rule (visibility shift × ai_referrers).
