# GEO category teardown — what to build (2026-08-01)

Six parallel research lanes across Profound, Evertune, Brandlight, Scrunch, Peec, Otterly,
Trakkr, Rankscale, Semrush, Ahrefs, Similarweb and the 2026 long tail, plus the evidence on
what tactics actually move AI visibility. Synthesised into a build list.

IT REFUTED TWO OF OUR OWN CLAIMS. Both corrections are kept below verbatim, because a
strategy doc that only contains the flattering half of the research is worthless.

## 1. Table stakes

**Already has (genuinely at parity or better):** daily multi-run prompt sampling (25 prompts × 3 ungrounded + 1 grounded — more runs/prompt/day than Semrush at $99/mo, and only Mangools also repeat-samples); judge scoring mention/rank/sentiment/competitors/cited-domain; first-touch AI-referral attribution inside a real event log; MCP; a GitHub App that opens PRs; daily verdict email; deploy→PR-comment reporting.

**Missing, brutally:**

- **AI crawler visibility. This is a hole, not a gap.** GPTBot, ClaudeBot, PerplexityBot, OAI-SearchBot execute no JavaScript. A JS pixel sees literally zero of them. Every serious competitor ships this; Matomo and Cloudflare give it away free. smolanalytics currently cannot answer "did anything even crawl me," which is the first question. Requires edge middleware or log ingest — architecture work, not a report.
- **Crawler-access diagnostics.** robots.txt blocking AI UAs, 403/4xx/5xx on cited URLs, client-rendered pages that bots see as an empty shell, TTFB. Scrunch, Semrush, Evertune and Brandlight all ship this. smolanalytics ships none of it.
- **A free, no-signup scanner.** Trakkr, HubSpot, Mangools, Geoptie, AmIOnAI all acquire this way. Worse: **no GEO tool requires a code install and smolanalytics does.** That is a structural onboarding disadvantage, not a marketing one. The GEO module must run on a URL alone.
- **Uncertainty reporting.** Category-wide criticism is that dashboards print point estimates on a 15%-variance process. smolanalytics has the sample counts to ship confidence intervals and a "not significant yet" state. Nobody does. It's cheap and it's now table stakes for a trust-positioned product.
- **Cheap parity items:** citation source-type taxonomy (owned/competitor/earned/PR/social), multi-region and persona prompt variants (Enterprise-gated at Profound, trivial here), sitemap importer.

## 2. The compounding plays

**Test result on the thesis: it holds, but two of the four claims are weaker than briefed.**

**1. Renderability + crawler access → PR. Unique, biggest effect size, build first.**
No major AI crawler renders JS as of mid-2026. A Bolt/Lovable/Cursor-built SPA can rank in Google and be *completely invisible* to ChatGPT — not a 5% lift, a 0→1. GEO tools don't detect it (it lives in SEO crawlers); SEO crawlers can't correlate it with "you're absent from these 25 prompts"; nobody fixes it. Requires: headless fetch with JS disabled, rendered-vs-raw diff, framework-aware codemods (Next/Astro/Vite). Caveat: Search Atlas OTTO has an undocumented GitHub/Vercel connector, so "nobody touches the repo" is *nearly* true, not absolutely true. **Pull: very large.**

**2. Cited ≠ recommended. Unique, nearly free, ship immediately.**
Seer showed citations are post-hoc: models pick brands from parametric memory, *then* retrieve sources. "Ghost citations" — your URL cited, competitor recommended. Every vendor conflates the two. The `$geo_check` schema already carries `mentioned` and `recommended` as separate props. This turns one useless number into a diagnosis with two entirely different remedies (retrieval → PR; memory → off-site). **Effort: S. Pull: large.**

**3. Merge-SHA-anchored before/after. Unique compound, defensible.**
LLM Pulse ships GEO Testing at €99/mo but the *user* declares the change date. Auriti measures against its own proxy score. Agencies (Click Laboratory) do it by hand at retainer prices. The unclaimed thing isn't "we open PRs" — it's **automatic change provenance (merge SHA + timestamp) + frozen prompt set + control prompt group + real-engine re-sampling + significance gating.** Requires the experiment harness, not new data. **Pull: large, and it's the moat.**

**4. Prompt-level conversion ROI. Genuinely unique — and the weakest of the four in practice.**
Nobody can say "prompt X drove 240 visits and 12 signups" (Profound/Conductor join at referral-source level; Amplitude joins at channel level). But be honest about the math: AI referrals are ~0.18% of web traffic, and roughly 1 click-through per 1,500 GPTBot crawls. A solo builder gets 5–40 AI referrals a month. **Prompt-level conversion rates will be statistically dead at ICP scale for most customers.** Ship it as a query and a demo; do not build the pricing story on it. It matures as the channel matures.

**5. "Did AI traffic convert" at channel level. NOT unique — stop claiming it.**
Amplitude ships this free on every tier with the identical marketing line. Matomo ships it free. Conductor ships it at $27K/yr. Ship it as hygiene, never as the headline.

## 3. Steal list (value per effort)

1. **Free no-signup URL scanner** (Trakkr/HubSpot/Mangools) — the category's universal acquisition mechanic, and the only way past the code-install wall. Weekend build. *Highest ROI item on this page.*
2. **Scrunch crawler-error diagnostics** — robots.txt/4xx/5xx/JS-render/TTFB. The most PR-able findings in the category with a real causal mechanism.
3. **Ahrefs hallucinated-URL 404 detection**, crossed with your *own* real 404 events → redirect PR. Ahrefs owns both halves and doesn't join them. Obviously-correct PR.
4. **Seer's review cliff + Outrigger's directory presence** as verdict items. 1% → 53.5% citation rate at the first ~13 reviews; directories r=0.391 beats DA. Detection is cheap; for a solo builder these are the highest-EV actions that exist and cost zero engineering.
5. **Self-listicle backfire audit** — own `/vs/` page cited, competitor recommended 69% of the time. Novel, cheap, every SaaS has the pages.
6. **Evertune Consumer Preferences** — one extra judge pass extracting category attributes and scoring you per attribute. Turns a score into a claim ("AI thinks you're hard to set up"), then cross it with the funnel.
7. **Freshness decay queue** ranked by *measured citation decay*, not last-modified date. Updates beat new publishing (72% vs 42%).
8. **Click Laboratory's protocol** — frozen prompt set, control page, log model version, read at 14/28/90 days, refuse a verdict before day 14.
9. **Profound's citation source-type taxonomy** and **Aim's project→task decomposition**, but ranked by conversion impact instead of visibility.

## 4. Do NOT build

- **llms.txt as a feature.** 97% of files got zero requests across 137K domains; 408 hits in 500M+ bot visits; Google refuses to support it. Ship it silently or not at all. *Publishing the null result is worth more than the feature.*
- **Schema as a GEO lever.** Ahrefs' 1,885-page matched-control study: −4.6% on AI Overviews, noise elsewhere. Ship Organization/Article as hygiene, labelled honestly.
- **Prompt-volume panels.** Data-acquisition + consent moat. Uncopyable and widely called pseudo-accurate.
- **Serving AI-specific pages / AXP.** Cloaking with a new name, no evidence it works, permanent vendor dependency.
- **Reddit seeding.** Only controlled test shows lift reverting the day you stop; independent contribution r=0.000. Violates the no-spray rule.
- **Shopping/product feeds, AI ads, retargeting, influencer scoring.** Wrong ICP entirely.
- **Agency white-label, seats, SSO, SOC 2.** That's where the $189–$500 tier money is and it is deliberately not your market.
- **Competing on prompt count, engine count, or MCP tool count.** PromptWatch ships 59 *write-capable* MCP tools free; Auriti's MIT toolkit ships MCP + GitHub Action + CI gating at 634 stars. "We have MCP" is a me-too claim now. Lead with CI-enforced parity, which no one else asserts.
- **A blog-post generator.** Commoditised and off-brand for serious infra.

## 5. Build order

**1. Free no-signup AI-readability scan (S).** URL in → can AI crawlers reach you, does the page render without JS, is robots.txt blocking GPTBot/ClaudeBot, are cited URLs 404ing. No account, no tag.
> *"We'll tell you in 60 seconds whether ChatGPT can physically read your site. No signup, no tracking script."*

**2. Renderability & access → pull request (M).** Turn every finding in #1 into a framework-aware diff: SSR/prerender the route, fix robots.txt, add redirects for cited-but-404 URLs. Guardrail: never ship a GEO change that degrades organic.
> *"Every other tool hands you the audit. We open the PR."*

**3. Cited-vs-recommended split + off-site verdict (S).** Separate metric, separate alert, separate diagnosis — plus review-profile and directory-gap detection with the Seer/Outrigger numbers attached.
> *"You were cited 40 times last week and recommended zero times. That's not a content problem, and here are the three things that actually fix it."*

**4. Merge-anchored GEO experiments (M).** Frozen prompt set at baseline, auto-assigned control group, change date = merge SHA, model version logged, confidence intervals from the existing 4 runs/day, no verdict before day 14.
> *"This PR moved mention rate 12% → 19% over 28 days. Here's the control group that didn't move, and here's the confidence interval."*

**5. Edge-middleware bot analytics — shipped as a PR (M/L).** Close the crawler hole the way only you can: the Cloudflare Worker / Vercel middleware that the whole category calls "needs CDN access and coding expertise" arrives as a one-click merge, writing bot hits into the same event log.
> *"What the AI crawlers saw, and what the humans they sent did next — one query, one event log, one merge."*
