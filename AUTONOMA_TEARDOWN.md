# Autonoma, taken apart

Six research lenses over the repo, the licence, the billing code, the blog, the copy and the
cap table. Every claim below came from a primary artefact unless marked otherwise.

---

## 1. What Autonoma actually is

**19% agent, 81% plumbing.** 1.6MB of agent/AI code against 6.9MB of everything else, across
1,600 TypeScript files. Excluding the front-end it is still 26/74.

And the 19% is mostly *prose*. The largest single source file in the entire monorepo is a
**58KB classifier prompt**. Forty-six prompt files total 388KB. The acting layer — the part that
clicks — is assembled from other people's parts: Vercel's AI SDK tool loop, Playwright, Appium,
Gemini Flash, Qwen, Moondream. Ten built-in commands. A competitor rebuilds it in a fortnight.

**The defensible asset is the adjudicator, not the actor.** Every run resolves to exactly one of
seven verdicts — passed / client_bug / engine_artifact / environment_failure / scenario_issue /
plan_mismatch / invalid_test — and their README states there is *deliberately no fallback verdict
path*. On `plan_mismatch` it rewrites the test and re-runs once; if it still fails the rewrite is
**reverted**, "because a rewrite optimized to make the test pass can blunt the very assertion
catching a real bug."

There is **no flake-detection module** — zero files match "flake". What the site calls separating
real bugs from flaky infrastructure is the verdict taxonomy plus four deterministic vision probes
run pre-loop, deliberately *not* exposed as tools "because the model gets those four signals wrong
when left to its own discretion."

Their own README publishes the cost of the plumbing: **~48% of classifications have no recording,
and 97% of those executed no steps** — the run died before or during startup.

## 2. The logos are the cap table

The correction that matters most, and it contradicts my earlier read.

**They did not come through the Vercel AI Accelerator** — absent from both cohort lists.
**Guillermo Rauch is an angel investor in the company.** Bessemer led the pre-seed. Luxury Presence
is on the logo bar because Bessemer sits on Luxury Presence's board. Kavak and Ualá are LatAm
unicorns in the founders' home market.

The Vercel Marketplace listing is "Vercel Native, billed by Vercel" — a partner integration
negotiated with people their launch post thanks by first name.

**And the open source is chronologically incapable of having produced any of it.** The GitHub org
dates from March 2026; the logos predate it. In four and a half months the public repo produced
**175 stars, 43 forks, 3 watchers, 121 Discord members with 6 online, 2 merged outside PRs**, and a
Show HN worth **5 points**. A Vercel-hosted community livestream got **397 views**.

## 3. But the open-sourcing was a keyword unlock

This is the part worth copying, and it is not the licence.

- repo created **2026-03-31**
- "we're open source" post **2026-04-01**
- **41 `opensource-alternative-{competitor}` pages published on 2026-04-03**, 3 more on 04-02

They occupied 44 competitor SERPs in a 48-hour batch. The blog is **527 posts at 14-28 a week**,
all in one flat un-paginated `/blog` namespace, every URL two clicks from root. Docs have no
sitemap and no robots.txt at all, so 100% of their organic surface is that one namespace.

The pages are not thin: sampled ones run **2,113-7,131 words**, first H2 is the literal query,
real `<table>` elements, FAQPage schema. Sibling pages share only 27% of their internal links.

**They treated open-sourcing as an SEO event, not a licensing decision.**

## 4. The pricing is a subsidy with the meters switched off

Their billing code is in the open repo, so the economics are readable rather than guessable — and
they contradict the marketing.

A credit is **$100/150,000 = $0.000667**. Rate card: web generation 500 credits ($0.333), iOS 700,
Android 540, web run 10 ($0.0067), iOS run 200, Android run 40.

Except: **`RUN_CONSUMPTION` is never written anywhere in the codebase.** `apps/workers` and
`apps/jobs` never import billing at all. Preview compute is hard-defaulted to **0 credits per
vCPU-hour** with enforcement off fleet-wide, and a code comment says it stays zero *"until a
deliberate go/no-go once shadow-mode usage data informs real numbers."* Three charge points
actually fire.

The loop they advertise is free. The compute is free. The LLM proxy is at cost. It is a land grab.

## 5. The copy is a four-audience segmentation of one claim

The homepage FAQ says *"Does Autonoma replace my QA team? Yes, and that's a good thing"* — and then
**never mentions money.** It argues SPAN ("no human QA team can multiplex across 7 features in
parallel") and closes on a benefit to the engineer reading it.

The dollars are **quarantined**. "$2M workforce cost optimization", "60 manual testers repurposed",
"~10% workforce reduction" all live in one blog post a budget-holder has to go looking for. Both
are anonymous.

**The one named, customer-co-signed case study (Kavak) contains zero headcount language.**

That is the rule worth stealing outright: a cost-saving number never appears in a story a real
customer put their name to.

## 6. The licence, precisely

BUSL-1.1, which MariaDB's own page states "is not an Open Source license". GitHub classifies it
NOASSERTION. Their Additional Use Grant is **harsher than HashiCorp's** — it forbids charging
"directly or indirectly" for the functionality with no internal-use safe harbour and no definition
of "competitive", where HashiCorp spends 400 words exempting internal use. Their README's friendly
gloss is more permissive than their own licence text.

The Change Date is a **fixed calendar cliff** (March 23 2028), not a rolling window — everything
ships into a moat that drains to zero on a date a competitor can diary.

Meanwhile the self-hosted tier is a bluff: **no deployment guide exists**, `docker-compose.yaml` is
dev-only, and running it for real means EKS + Karpenter + External Secrets + privileged rootful
BuildKit + a leader-elected proxy + Temporal + Loki + Prometheus.

---

## What to steal, ranked

1. **The `open source alternative to {X}` cluster.** Highest-value copyable asset here, and
   smolanalytics has the *stronger* claim: genuine MIT against their BUSL. Page spec, measured
   from their winners: 2,500-3,500 words, first H2 is the literal query verbatim, 1-2 real tables,
   FAQPage schema. Concentrate every post into ONE cluster until it has 12-15 pages.
2. **The adjudicator discipline, for the ship ledger.** A closed verdict set with no fallback —
   moved / did not move / cannot tell: instrumentation gap / cannot tell: traffic too thin /
   confounded. Append-only, with a `current` pointer, so re-adjudicating never overwrites history.
3. **The copy segmentation.** Replacement claim on the homepage with zero dollars in it; money
   quarantined in one findable post; named case studies never carry headcount language.
4. **The free tier's shape:** SSO + unlimited users + Slack + priority support, all free. Costs a
   solo founder nothing and deletes four objections before they are raised.
5. **Cost recording without billing.** One table keyed on a natural business id, idempotent, rate
   defaulted to zero, enforcement behind two switches. Ship the meter dark and set the rate later.
6. **Permissionless registries this week** — docker/mcp-registry, npm, the MCP directories. They
   only filed theirs four months after open-sourcing. 94 tools is a strong listing.

## What to refuse

1. **BUSL, and calling it open source.** Not under attack — no hyperscaler, no funded fork. The
   costs are immediate and certain: permanent exclusion from homebrew/core, and MIT is the one
   promise you cannot un-break. Genuine MIT is an asset against them, not a liability.
2. **Usage-based credits.** Theirs exist because a browser agent and 50 LLM steps per run are real
   variable cost. An analytics query over an event log is near zero. Metering a flat-COGS product
   is friction that makes people run the thing less.
3. **"Open source is our distribution."** It is not theirs. 175 stars, 3 watchers, 2 outside PRs,
   Show HN at 5 points. The blog did the acquisition; the licence bought a credibility badge.
4. **The Vercel Marketplace.** Partnership-gated, 500 installs before eligibility, and their route
   in was an investor. 136KB of bespoke integration code for a channel that needs someone's yes.
5. **Launch moments as a plan.** Show HN: 5 points. Vercel-platformed livestream: 397 views. No
   Product Hunt at all.
6. **Any environment, seeding or replay layer.** ~660KB of production TypeScript plus an
   eight-language protocol the customer has to implement — and half their runs die in it anyway.
