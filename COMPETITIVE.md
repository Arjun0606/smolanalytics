# the competitive picture, verified

Research run 2026-08-03 across six fronts, with every load-bearing claim sent to a second
agent told to refute it. **17 claims were killed by that pass**, including three of our own
positioning lines. Those are at the top, because acting on a refuted claim is worse than not
researching at all.

---

## 1. things we believed that are false

### "our agent writes your tracking code" is no longer a differentiator

This was recorded as the **#1 USP**. It is now table stakes.

| vendor | what ships today |
|---|---|
| **Mixpanel** | Implementation Skill — you point an agent at their `skill.md` and it "writes the integration code". Plus a `tracking-implementation` skill inside the `mixpanel-mcp` plugin (Apache 2.0) |
| **PostHog** | `npx @posthog/wizard` — Claude-powered, "writes code and instruments the PostHog SDKs client-side and server-side", covers 22+ frameworks, and also installs the MCP into Cursor/Claude/VS Code/Zed |
| **Amplitude** | `npx @amplitude/wizard` — "proposes an event plan for your review, then writes the code", plus `add-analytics-instrumentation` / `instrument-events` skills |

The narrow version that survives — *"no incumbent exposes code-writing as an MCP **tool call**; they deliver it via bundled skills"* — is true and confers nothing. Nobody buys on that distinction.

**Where the wedge actually is.** All three write instrumentation. None of them close the loop afterwards. We already have the loop and have never led with it:

`propose_instrumentation` → agent writes it → **`verify_instrumentation`** (did events actually arrive?) → **`record_deploy`** → **`deploy_impact`** → **`instrumentation_health`** → **`regenerate_plan_from_code`** catches drift when the code changes.

The honest line is **"the agent writes your tracking, then proves it works, and tells you when it breaks"** — not "the agent writes your tracking."

### "the EU-safe alternatives have no funnels or retention" is false

I wrote this in DISPLACEMENT.md this morning. It does not survive.

**Umami** — MIT, free, self-hostable — ships funnels, retention, journeys, cohorts, segments, attribution, goals, revenue, heatmaps **and session replay**. A prospect disproves the claim with one `docker compose up`.

What survives, narrowed and citable:
- **Plausible CE** genuinely excludes funnels. Say "Plausible's self-hosted edition has no funnels."
- **Matomo** has funnels and cohorts, but as paid plugins: **€275/mo** for 4 users self-hosted. Reframe from "can't" to "costs €275/mo" — a price argument we win at $49.

### the EU wedge has a qualification problem

EU directories gate on **company domicile**, not data residency. Our cloud runs on Fly.io and Neon (US companies). The CLOUD Act argument cuts against us too, and a sharp EU buyer asks this in the first call.

Two answers, and one must be picked before writing any EU copy: offer a **Hetzner/Scaleway** deployment target, or **lead EU prospects with self-hosting** where domicile is irrelevant.

---

## 2. the feature matrix

✅ have · ⚠️ partial · ❌ don't · 💰 costs extra

| | smolanalytics | PostHog | Mixpanel |
|---|---|---|---|
| funnels / retention / paths | ✅ | ✅ | ✅ |
| lifecycle · stickiness · groups (B2B) | ✅ | 💰 groups paid; lifecycle **not in self-host** | ✅ |
| cohorts, incl. sequence cohorts | ✅ | ✅ | ✅ |
| feature flags | ✅ | ✅ | ✅ |
| A/B experiments | ✅ *(two bugs fixed today — §3)* | ✅ sequential testing | ✅ |
| heatmaps | ✅ | ✅ toolbar | ❌ |
| surveys | ✅ | ✅ 1.5k free | ❌ |
| web analytics | ✅ | ✅ | ⚠️ |
| **session replay (video)** | ❌ **deliberate** | ✅ 💰 the bill-shock line | ✅ 💰 |
| **error tracking** | ❌ | ✅ Sentry-class | ❌ |
| **data warehouse / CDP** | ❌ | ✅ 💰 | ✅ 💰 Mirror |
| **SQL access** | ❌ **biggest real gap** | ✅ HogQL + 33 query MCP tools | ✅ |
| MCP server | ✅ 95 tools | ✅ (CLI-mode wrapper) | ✅ ~50 tools |
| agent writes instrumentation | ✅ | ✅ wizard | ✅ skill |
| **verifies instrumentation worked** | ✅ **only one** | ❌ | ❌ |
| AI answers | **computed, never LLM** | LLM, $0.01/credit | LLM (Claude) |
| self-host full product | ✅ MIT, no carve-out | ❌ **officially unsupported** | ❌ |
| native mobile SDKs | ✅ 4 | ✅ | ✅ |

### pricing reality — we do not win at the bottom

PostHog's free tier is **1M events/mo**, per product, independently. A solo builder under 1M events pays PostHog **$0** and us **$49**.

| monthly events | PostHog | smolanalytics |
|---|---|---|
| 1M | **$0** | $49 |
| 2M | $50 | $54 |
| 10M | ~$325 | **$149 + overage** |

We win above ~2M events and lose below it. **Do not build a pricing calculator until this is true in the band the ICP lives in.** Either reprice the entry tier or stop competing on price and sell the binary, the data ownership, and the verify loop.

Real bill-shock story, citable: OpenPanel's founder left Mixpanel when his bill hit **$300-400/mo on 250-500k events** at 10k MAU. That maps to $49 flat here.

### the strongest single fact we have

**Self-hosted PostHog is officially unsupported.** Their own docs steer you to Cloud unless you're under 300k events/mo, and self-hosting drops group analytics, lifecycle, correlation analysis, advanced paths, subscriptions, extended retention, multiple alerts and data pipelines.

Developers on HN (July 2026, on PostHog's own FOSS repo) report it outright broken — 4 vCPU/16GB minimum, a documented ~12-minute multi-container cold start, one user with a 16-core/64GB box pinned at 100%.

That is a page: `/posthog-self-hosted-alternative`, their disclaimer quoted verbatim next to one binary and a real benchmark.

---

## 3. A/B testing: two live bugs, both fixed today

Researching how LaunchDarkly, PostHog, Statsig, GrowthBook and Unleash bucket users, then checking ours against it, found two bugs. Neither errored. Both silently corrupted results. **Fixed and shipped** — see `internal/flag/bucketing_test.go`.

1. **Two concurrent experiments were perfectly confounded.** FNV-1a is `h = (h ^ byte) * prime` with an odd prime, and multiplying by an odd number cannot change the low bit — so the low bit was the XOR-parity of the input bytes and `% 2` discarded the salt. **Measured phi between two independent 50/50 experiments: +1.0000.** Every user in arm A of one was in arm A of the other. GrowthBook shipped this, measured it, retired it.

2. **Editing weights reshuffled everyone.** `hash % sum(weights)` made the bucket space a function of the weights. 1:1 → 2:1 moved the modulus from 2 to 3: **5,041 of 10,000 users changed arm** where ~1,667 should have, and **1,674 LEFT the variant that had just been widened** — impossible if assignment is positional.

Now SHA-256 into a fixed [0,1) space with cumulative ranges. phi measures **-0.0052**.

### what's still missing, in build order

Items 1-3 are about a week and convert experiments from "silently wrong" to "trustworthy".

1. **SRM detection** — chi-square goodness-of-fit against configured weights, fire at p < 0.001 (GrowthBook's threshold; Microsoft uses 0.0005). ~25 lines. **Highest trust-per-line in the product**: it tells the user their experiment is broken instead of handing them a confident wrong answer. When it fires, auto-run the breakdown we already have across device/browser/country and name the worst dimension.
2. **Device-id bucketing.** Anonymous → identified changes `distinct_id`, which changes the hash, which changes the variant. PostHog calls this data corruption in their own docs. Fix: persist an `sa_bucket_id` in localStorage on first visit and bucket on that. ~15 lines of SDK. Explicitly do **not** build experience-continuity/DB-pinning — it breaks local evaluation.
3. **Confidence intervals + raw numerator/denominator** instead of today's significant-yes/no boolean. Showing the raw counts *is* the "computed, not guessed" claim made visible.
4. **Sample-size as an MCP tool** — `N = 16 × variance / d²`. Four lines of Go, and the agent-native angle is the moat: asking Claude Code "how long do I need to run this?" and getting a number computed from *your actual current exposure rate*. No competitor can do that from inside the editor.
5. **Sequential testing (mSPRT).** Solo builders peek constantly — it's the defining behaviour of the segment — and a fixed-horizon z-test is wrong under peeking. Statsig publishes the formula; ~30 lines.
6. **CUPED.** Halves required traffic. The only hard prerequisite is per-user pre-period history, which we already store. Ship Statsig's four safety gates alongside it, not after.
7. **Benjamini-Hochberg** across metrics. ~20 lines, matters as soon as there's more than one goal metric.

**Licensing:** gbstats (GrowthBook's stats engine) is plain MIT — safe to read and port. Unleash **SDKs** are Apache-2.0, safe. The Unleash **server** is AGPL — do not read it. Published formulas in Statsig/PostHog docs aren't copyrightable and can be implemented freely.

---

## 4. from dub.co, ranked by value-per-hour

1. **Collect-but-lock on overage.** Their best mechanic. Exceeding the click limit keeps collecting data and only locks *viewing* it. Copy exactly: when a project blows its quota or the trial ends, **keep ingesting, never 429 the SDK, never drop a row** — gate the dashboard and MCP reads. It cannot break the customer's app, it creates maximum upgrade pressure exactly when they care, and on upgrade their history has no gap.
2. **One `<LockedState>` component**, titled like a product state, leading with reassurance: *"Your events are still being recorded. Upgrade to view them."* A paywall that looks like an error makes people assume the tool broke.
3. **A systematised `<EmptyState>`** whose props *force* a ghost preview of the filled report, a title, a primary action and a docs link. Every funnel/retention/paths screen is empty on day one for every new user, and right now that reads as "nothing works."
4. **The changelog as the main indexed surface.** Dub has 94 changelog URLs — more than blog, customers and integrations combined. One URL per shipped item, dated, bylined, one screenshot, prev/next links. We ship constantly and none of it accrues indexable surface. This is the highest-leverage SEO available because the content is a byproduct of work already done, and it directly attacks the "61 pages discovered, not indexed" problem.
5. **`/llms.txt` + a `.md` twin of every docs page.** For a product whose pitch is "your coding agent instruments this", an agent that lands on our docs and can't enumerate them is a lost install.
6. **One install page per stack** (Next.js, Vite, SvelteKit, Astro, Remix, Expo) and per auth provider (Clerk, Supabase, Auth0, better-auth, NextAuth). Buy-intent SEO and agent-grounding in the same artifact.
7. **API-key one-click import** for PostHog and Umami. Our importers are file-based, so the user must go export first — and the switch-and-stick motion dies at exactly that step.
8. **Annual = 10% off + 12 months of events as one pooled allowance.** Removes the exact fear that stops solo builders prepaying: that one Show HN day blows the cap.
9. **25% off for OSS and non-profits.** Costs nothing until claimed, self-selects the ICP, and every claim is a warm inbound conversation.
10. **Server-persisted dashboard state** — date range, filters, chart type — not localStorage.

**Explicitly do not attempt** (capital or headcount we don't have): SOC 2, an enterprise sales motion, payout/tax infrastructure, vanity domains, a paid docs platform, a founding designer.

---

## 5. leads, verified and public

Highest-intent first. All public posts, all answerable in-thread. **No DMs** — that's the no-spray rule, and it's also what gets accounts banned.

| lead | why |
|---|---|
| **HN `mrr7337`**, Ask HN 2026-05-28, *"PostHog training on end-users; Indie Alternatives?"* | Literally asking our question. One substantive reply, **no self-hosted product-analytics answer**. The existing reply raised a mobile-SDK gap — we ship 4 native SDKs |
| **HN `Sankra`** | Hand-tweaked a self-hosted Umami to support iOS because Umami has no mobile SDK. Doing manual work our product removes |
| **HN `rafael-lua`** | Asked for migration recommendations after PostHog's AI-training change |
| **Jamayal Tanweer** | Tested 18 GA4 alternatives, concluded no single tool works, recommended a 4-tool stack. Never encountered us — a distribution failure, not a product one. We cover 3 of his 4 layers |

**Context:** PostHog announced 2026-05-27 it will train its own models on customer data. US cloud users are **opted in by default**; EU cloud users and those with BAA/MSA agreements are opted out. Both defaults are reversible. At least six identifiable HN users publicly said they're leaving over it.

### the migration-refugee flows

- **June.so** → acquired by Amplitude 2025-08-08. Displaced small teams; the searches still exist.
- **Highlight.io** → acquired by LaunchDarkly, all services deprecated 2026-02-28, customers pushed onto contracts starting ~$75K/year. Small teams handed an enterprise bill, and they need **feature flags**, which we have. Time-boxed.
- **Aptabase** → structurally cannot do retention or MAU; its anonymous data model rules out user-level analytics permanently. A referral partner, not a rival. "When you outgrow Aptabase" is the cleanest upgrade path in the whole list, because the wall is architectural.

### the competitors that actually matter

- **Swetrix** — the real head-to-head. EU-hosted, AGPL self-hostable, funnels + session replay + flags + A/B at **$19-39/mo**. We are *not* the cheapest EU option; stop implying it. We win on the MCP/verify layer, groups/B2B, and 4 native mobile SDKs.
- **OpenPanel** — closest like-for-like. AGPL, free unlimited self-host, ~100M events/mo across ~200 instances. At 1M events their cloud is $90 vs our $49 — we win at ICP volume.
- **TelemetryDeck** — EU-based, funnels + retention, **already ships an MCP**. No self-hosting and mobile-first; that's the gap to attack.
- **Rybbit** — 12,560 stars in ~15 months with an unfinished business model, which says the lever here is **a strong Show HN, not SEO**. Its 164-comment launch thread is the densest free customer research available; read it before the next positioning pass.

**Top-4 complaints in that thread**, and the commitments that answer them: pricing that punishes growth → keep flat per-tier; self-hosting weight → the binary; **feature-gating the OSS edition** → never gate a feature out of the binary (this hits Plausible hardest and is our cleanest contrast); disputed GDPR claims → publish the bot-filtering method.

### demand signal

The web-analytics category on european-alternatives.eu grew **>2,700% in unique visitors during 2025** — 5th most popular category, 9th most visited page. That's from a competitor's own blog.

Channels: **only-eu.eu** is self-serve, no email, and its analytics category lists only 8 entries — but it requires EU/EEA/Swiss HQ and no US parent, so check qualification first (see §1). european-alternatives.eu has an apparently abandoned review queue — submit, budget nothing on it, send no follow-up email.

---

## 6. what to build, ordered

**This week**
1. SRM detection + Health tab *(highest trust-per-line in the product)*
2. Device-id bucketing in the SDK *(prevents the next silent corruption)*
3. Collect-but-lock overage + the `<LockedState>` component

**This month**
4. `run_sql` as a single read-only MCP tool with a row cap and timeout — the biggest real gap, and it converts "we answer the 20 questions we pre-built" into "the agent can answer anything"
5. Reposition on the **verify loop**, not on "the agent writes your tracking"
6. Changelog as an indexed surface + `/llms.txt` + `.md` docs twins
7. `/posthog-self-hosted-alternative` with their disclaimer quoted and a real benchmark

**Decide before writing EU copy**
8. Hetzner/Scaleway deployment target, or lead EU prospects with self-hosting

**Deliberately not building:** video session replay (the bill-shock line — say so as a position), Sentry-class error tracking (months, not weeks — ship a stack-trace event type in the session inspector instead), logs, the data warehouse (build **Stripe only**, for "which source produces paying users"), an editor.
