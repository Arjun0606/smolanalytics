# smolanalytics — the game plan (draft, 2026-08-01)

> Status: frame written from session knowledge; two deep-research passes in flight
> (expansion white-space; demand-side evidence). Synthesis lands in §4-5 when done.

## 1. What we actually are (the honest inventory)

Assets, all live:
- OSS engine (Go, single binary) + hosted cloud, isolated instance per project
- The four loops nobody else runs: repo→instrumentation PR · daily verdict email ·
  deploy→PR impact comments · in-editor MCP answers with the agreement proof
- 4 mobile SDKs published; flags/A-B/heatmaps/surveys shipped; discount codes live
- Distribution surfaces: MCP registry (current), Glama, PulseMCP, awesome-mcp PR,
  GitHub Marketplace (1 click from review), /proof restored, privacy+terms live
- Outreach machine: codes + kit + YC S26 and Show-HN target lists

Constraints, equally honest:
- ~86 visitors/mo, ~0 paying customers, solo founder, no audience
- Funded startups get PostHog free (YC deal) → we only win COEXISTENCE with them
- The repo is 5 weeks old: age-gated from awesome-selfhosted (~Oct 26), 2 stars

## 2. The identity that all decisions filter through

"The analytics that ACTS." Incumbents render charts and wait to be visited. We:
tell you what to fix (verdict), where you live (email, PR, editor), wire ourselves
in (PR), and prove we're not hallucinating (agreement test). Every expansion idea
must strengthen "acts, provably" — or it's someone else's product.

## 3. Strategic directions on the table (priors, pre-research)

A. **Deepen the wedge** (PARITY_AND_EDGE_SPEC): explain-this-change, feature
   graveyard, mobile parity. Low risk, makes every pitch stronger. DEFAULT PATH.
B. **AI-visibility / GEO measurement**: we already count ai_referrers; "what does
   ChatGPT send you + how do LLMs describe you" at indie prices. Riding a wave that
   is exploding and enterprise-priced today. Possibly the sharpest NEW wedge.
C. **Agent observability doubling-down** (existing Phase-1 per BATTLE_PLAN):
   agent_tools/errors/conversations exist; the agnost category.
D. **Sentry-lite**: errors+analytics in one tool/one price for the indie tier.
E. **Embedded analytics for vibe-coded apps**: "analytics your users see" as a
   component; Lovable/Bolt apps need user-facing dashboards.
F. **Growth-agent closed loop**: verdict → fix-PR runner already exists; the
   category-of-one story ("analytics that opens the fix PR").

## 4. Research findings

### 4a. Demand side (completed 2026-08-01)

1. **Trust is the #1 stated switching reason.** IH founders write "our conversion
   data was lying to us", "dashboards nobody trusted"; one tested 18 GA4
   alternatives over it. Our agreement proof is a direct hit on the top articulated
   pain — not a nice-to-have.
2. **PostHog bill anxiety is a named phenomenon** ("the 4x event trap"; third-party
   "decode your PostHog bill" tools exist). Their overwhelm + billing surprise is
   the exact inverse of flat pricing + repo→PR simplicity.
3. **PostHog already validated agent-installed analytics** — official Lovable
   integration: Lovable's agent installs posthog-js and queries via PostHog's MCP.
   The motion we bet on is confirmed by the biggest player; the underneath is still
   their overwhelming suite. Urgency: the vibe-coder tier is being claimed NOW.
4. **Nobody ships the "so what."** The collect-vs-act gap is acknowledged everywhere
   and served nowhere at indie prices. The verdict email is the only feature in the
   research with zero direct competitor.
5. **Willingness to pay:** $9-19 is the indie ceiling for commodity pageviews (free
   self-hosted Umami is the floor). $29+ must buy what a dashboard can't do — i.e.
   the acting layer, not the charts.
6. **Second-tool coexistence is the norm** (traffic tool + product tool + error
   tool; "PostHog and Plausible barely compete"). Asking to be KEPT, not to
   replace, matches how the market already behaves.
7. **The Rybbit playbook is the proven zero-to-traction path (2025):** open-source
   + the same post to r/SideProject and r/selfhosted repeatedly → #1 hot → 5k
   GitHub stars in 9 days. Open-source is what makes repeat posting acceptable.
   Cookie-banner fatigue ("no banner needed") remains the top-performing hook.

### 4b. Expansion white-space (completed 2026-08-01)

1. **GEO / AI-visibility is the strongest new wedge.** Profound: $155M raised, $1B
   valuation, real coverage $2,000+/mo; Peec ~$10M ARR, entry $95/mo. The entire
   category prices for marketing teams; nobody is agent-native or bundled with real
   traffic analytics. We already own the referral-truth half (ai_referrers).
   "GEO for indie devs, inside your analytics" = clear pricing gap. Effort M.
2. **PostHog has repositioned around "self-driving products"** — their agent
   diagnoses and opens PRs (open beta). This VALIDATES our whole thesis and means
   the category has a giant. Counter-position: "the growth agent you can audit" —
   our loop is provably-computed (agreement test), scoped, and aimed at repos their
   enterprise motion ignores. This is positioning + one build (verdict→fix-PR,
   which reuses the existing fix-PR runner). Effort M.
3. **Sentry-lite via a Sentry-compatible DSN is the #3 move.** Highlight.io's
   hosted shutdown (Feb 2026) orphaned bundle-seekers; GlitchTip thrives because
   Sentry is heavy; a compatible DSN = zero SDK work for users; errors are the best
   verdict + fix-PR raw material ("this error cost you 40 signups → PR"). Effort M.
4. **Session replay: commodity** (Clarity is free-unlimited). Parity checkbox
   someday; never a wedge. **Uptime: commodity** ($6/mo floor); weekend goodwill
   feature feeding the verdict, zero revenue. **Agent observability: bundle, don't
   lead** — Langfuse (acquired by ClickHouse, 2k+ paying) and Helicone own the
   indie tier. REVISES the Jul pivot: keep Phase-1 computed metrics as a checkbox,
   do not lead with it.
5. **Embedded analytics for vibe-coded apps: empty cell, unproven demand.** Probe
   with public/embedded shared dashboards (S) before any multi-tenant build (L).

## 5. The game plan — 30/60/90

**Positioning, effective now:** "Analytics that acts — and you can audit every
step." Trust proof stays; the four loops move to the front of every pitch. Vs
PostHog: keep yours; ours is the layer that tells you what to fix, where you
already live, without bill anxiety.

**Days 1-30 (August) — close the loop, start the drumbeat**
- Build: W1 explain-this-change + W2 feature graveyard (engine, 3 surfaces) +
  verdict→fix-PR wiring (the auditable growth agent, mostly reuse).
- Distribution: run the Rybbit playbook — the same honest post to r/SideProject
  and r/selfhosted (open-source angle makes repeat posting legitimate), weekly.
  Publish the Marketplace listing. Make the 60s demo video → Show HN.
- Outreach: 10/week from the Show-HN + PH lists with codes. YC only where
  agent-native resonates.

**Days 31-60 (September) — the GEO module**
- Build: AI-visibility v1 — scheduled prompt-sampling across the major LLMs ("how
  is [product] described / recommended?"), share-of-voice + ai_referrers in one
  report, MCP-queryable, verdict-integrated ("Claude stopped recommending you").
  Price: included in Pro — the wedge IS the price gap vs $95-2,000/mo incumbents.
- Mobile parity M1-M2 (mobile_overview + foreground engagement).
- Content: "the CI test that keeps our AI honest" essay + GEO-angle piece riding
  the wave ("what ChatGPT sends indie sites — data from N real sites").

**Days 61-90 (October) — bundle the breakage**
- Build: Sentry-compatible DSN error ingest → errors in verdicts and fix-PRs.
- Probe: public/embedded dashboards (/open page first).
- Distribution: awesome-selfhosted PR (eligible ~Oct 26), r/selfhosted launch of
  the error bundle.
- Checkpoint vs kill-gates: stars, weekly-active dashboards, paying customers.

**What we deliberately do NOT do:** lead with agent observability, build session
replay now, build white-label tenancy on inferred demand, chase enterprise.
