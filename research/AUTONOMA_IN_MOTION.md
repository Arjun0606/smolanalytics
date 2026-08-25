# Autonoma in motion

Everything a code teardown cannot show: what they charge, what they shipped, who is using it,
and where the machine is grinding. Companion to `../AUTONOMA_TEARDOWN.md`, which it corrects in
four places.

**All fetches: 2026-08-25** unless stated. Evidence labels are used strictly:
**MEASURED** = I fetched or computed it. **CLAIMED** = their marketing says it.
**INFERRED** = my read from measured facts. **UNVERIFIED** = could not confirm; treated as unknown.

---

## 1. The sixty-second version

They are shipping harder than the teardown implies and selling worse.

**MEASURED — 1,447 commits to the public repo in 90 days** (2026-05-27 → 2026-08-24), from
**10 human engineers and 2 bots**, against **1,700 commits in the repo's entire history**. 85% of
all public history landed in the last quarter. That is not a stalled company.

**MEASURED — in the same 90 days: +12 GitHub stars (175 → 187), +9 Discord members (121 → 130,
8 online), zero new human contributors, zero new HN posts, zero Reddit threads, no G2 profile.**
Every human contributor's first commit is April or May 2026. The community is not growing; it
never started.

The gap between those two numbers is the whole story. Ten good engineers are pouring 11
commits/person/week into a product whose top-of-funnel is a 567-post blog and whose only named
customer reference is nine months old and describes a product they no longer sell.

---

## 2. Four corrections to the teardown

| Teardown said | Actually | Evidence |
|---|---|---|
| "The GitHub org dates from March 2026" | **Org created 2023-03-04.** The *monorepo* was created 2026-03-31 and seeded 2026-04-08. Older org repos: `docs` (2024-11-04), `envsync` (2025-03-03), `actions` (2025-06-02) | `api.github.com/orgs/Autonoma-AI`, `/repos?sort=pushed` |
| "175 stars, 43 forks, 121 Discord / 6 online" | **187 stars, 46 forks, 130 Discord / 8 online** | GitHub API; `discord.com/api/v9/invites/nsYQExXTsQ?with_counts=true` |
| "2 merged outside PRs" | **Still ~2.** 15 lifetime contributors: 10 internal humans, 2 bots, and 3 drive-bys with 1 commit each (`kurupoo` 2026-07-30, `TPereyraL` 2026-04-15, plus `pato-1441` who is internal) | `repos/Autonoma-AI/autonoma/commits` paginated to exhaustion |
| "The pricing is a subsidy with the meters switched off" | **Half-right, and the half that changed matters.** Three *new* meters went live in July–August. The *run* meter is still dark. See §3 | `packages/db/prisma/schema.prisma`, `packages/billing/src/*` |

One thing the teardown got exactly right and I could not dislodge: **the 48% figure is unchanged.**
`packages/diffs/README.md` still reads, verbatim, *"~48% of classifications have no recording, and
97% of those executed no steps either"* — the run died before or during startup. Five months and
1,447 commits later, the number in their own README has not moved. Either it has not improved or
they stopped measuring it. **MEASURED** (file fetched 2026-08-25).

---

## 3. What they charge today, exactly

There is **no `/pricing` page** — `getautonoma.com/pricing` returns **404 (MEASURED)**. Pricing
lives in an anchor section on the homepage and in the markdown mirror. Verbatim from
`https://getautonoma.com/md` (2026-08-25):

> - **Free** — 100K credits, no credit card. Then $100 per 150K credits, with optional auto top-up and no minimum.
> - **Cloud** — $499/month for 1M credits per month on managed infrastructure. For an average customer 1M credits is roughly 10K test runs and 2K test generations per month.
> - **Self-hosted** — the agent is open source and can run on your own infrastructure with no feature limits.

**The Cloud tier is new since the teardown**, which described only the $100/150K PAYG rate.

### The rate card, from the schema (MEASURED)

`packages/db/prisma/schema.prisma`, model `BillingPricing`, verbatim defaults:

```prisma
creditsPerSubscription       Int @default(1000000)
creditsPerTopup              Int @default(150000)
creditsFreeStart             Int @default(100000)
creditsWebGenerationCost     Int @default(500)
creditsIosGenerationCost     Int @default(700)
creditsAndroidGenerationCost Int @default(540)
creditsWebRunCost            Int @default(10)
creditsIosRunCost            Int @default(200)
creditsAndroidRunCost        Int @default(40)
stripeTopupAmountCents       Int @default(10000)
creditsPerVcpuHour           Int @default(0)
creditsPerGbMemoryHour       Int @default(0)
```

Comment on the last two, verbatim: *"Flat, fleet-uniform previewkit compute-usage rates. Zero
until a deliberate go/no-go… Set via the `admin.billing.updateComputePricing` action."*

So: **1 credit = $0.000667** (150,000 / $100). **1 USD = 1,500 credits.**

### Which meters actually fire (MEASURED, via authenticated GitHub code search)

The `CreditTransactionType` enum has grown from 7 to **14** values. Where each one is *written*:

| Transaction type | Written by | Live? |
|---|---|---|
| `GENERATION_CONSUMPTION` | `packages/billing/src/credits.service.ts` | **yes** |
| `LLM_PROXY_CONSUMPTION` | `deductCreditsForLlmProxy` | **yes** |
| `AI_COST_CONSUMPTION` | `packages/billing/src/ai-cost-persister.service.ts` | **yes — new 2026-08-19** |
| `PREVIEW_BUILD_CONSUMPTION` | `packages/billing/src/deduct-credits-for-build-usage.ts` | **yes — new** |
| `PREVIEW_RUNTIME_CONSUMPTION` | `packages/billing/src/credits.service.ts` | **yes — new 2026-07-21** |
| **`RUN_CONSUMPTION`** | **nowhere** — appears only in the UI formatter, the Zod schema, a read-only Vercel usage reporter, the enum, and the original migration | **NO** |

`creditsWebRunCost` / `creditsIosRunCost` / `creditsAndroidRunCost` appear in exactly four files:
two type declarations, the pricing service that only `select`s them, and the schema. **No charging
path consumes them.** The teardown's finding survives intact: **running your tests is still free.**

What they now charge for instead is **their own cost of goods** — model tokens, preview builds,
preview runtime. The meter has drifted from *value delivered* to *cost incurred*. **INFERRED**, but
the commit trail is unambiguous: `feat(billing): add previewkit compute-usage billing primitives`
(#1677, 07-21) → `enforce previewkit compute-billing credits at deploy time` (#1696, 07-22) →
`feat: aws compute pricing cronjob` (#2428, 08-14) → `deduct real credits for AI cost and previewkit
build/running usage` (#2471, 08-19) → `show a preview build's real AWS cost alongside its billed
credits` (#2694, 08-20).

### The arithmetic that should worry them

`ai-cost-persister.service.ts` converts provider dollars to credits with `usdToCreditCost()`, which
the code comments describe as *"the same rate top-ups are priced at"* — i.e. **1,500 credits per
dollar, zero markup** (MEASURED).

Therefore, on the **$499/month Cloud plan**, 1,000,000 credits ÷ 1,500 = **$666.67 of provider spend
sold for $499**. A customer who burns their allowance on model calls is **-25% gross margin before a
single dollar of AWS**, and AWS compute is currently billed at rate **zero**. The free tier is
100,000 credits = **$66.67 of model spend given away per person**.

That is not a land grab any more — the teardown's word. It is a land grab **formalised into a
subscription**, which is harder to unwind. **INFERRED from measured constants.**

Two more things a buyer cannot see from the homepage:

- Their own worked example over-spends the plan: 10K runs × 10 + 2K generations × 500 = **1.1M
  credits against a 1M allowance**, and **91% of it is generation**, not the runs the product is
  sold on. The runs half of that sentence costs nothing because `RUN_CONSUMPTION` never fires.
- The docs say, verbatim: *"Rates are configured per organization, so the billing page is the source
  of truth for what your account is charged. For a quote… ask in the in-app chat."*
  (`docs.autonoma.app/llms/troubleshooting.txt`). **The published rate card is not the rate card.**

Anti-abuse arrived in August: `fix(billing): close an unlimited free-credit mint` (#2374, 08-10) and
`feat(billing): make the free starting credits an entitlement per person` (#2383, 08-10). **INFERRED:**
new orgs used to mint 100K free credits each, so one person could farm it indefinitely.

---

## 4. What they shipped since the teardown

Method: all 1,447 commit subjects since 2026-05-27, pulled from the API and bucketed by
conventional-commit scope. **MEASURED.**

| Scope | Commits | Share |
|---|---:|---:|
| **previewkit** (preview-environment builder) | **232** | **16.0%** |
| ui | 143 | 9.9% |
| api | 87 | 6.0% |
| cli (the planner) | 79 | 5.5% |
| analysis | 52 | 3.6% |
| investigation | 48 | 3.3% |
| **onboarding** | **47** | **3.2%** |
| db | 25 | 1.7% |
| mcp | 17 | 1.2% |
| scenario | 14 | 1.0% |
| diffs | 13 | 0.9% |
| billing | 11 | 0.8% |

**The single largest engineering line item is the environment layer, by a factor of 1.6 over the
entire front end.** Sixteen percent of a quarter's work went into building and deploying other
people's apps — the layer the teardown told us to refuse, and the layer in which half their runs
still die.

### The features that are actually new

- **Impact Analysis** — a `DiffsAgent` loop over the PR diff that selects which tests to run.
  `packages/diffs/README.md`, verbatim: *"Judge a candidate model on how often it selects nothing,
  not on what it selects."* Shipped through #2648, #2749, #2750.
- **The two-plane verdict** (#1636, 07-17). The taxonomy is now *partitioned*: an **app-health plane**
  (`client_bug` / `passed` — the PR headline) and a **coverage-confidence plane** (`engine_artifact` /
  `environment_failure` / `scenario_issue` / `plan_mismatch` / `invalid_test`). Verbatim from
  `packages/diffs/README.md`. This is the best idea in their codebase.
- **Suite health** — a five-state public trust score. See §6.
- **The Investigator self-heal loop + full verdict taxonomy** (#1512/#1513/#1614, 07-17), with
  `scenario_unsupported` (#1129, 07-01) and `invalid_test` (#1853, 07-29) added. Still **no flake
  module**; quarantine was *removed* in a five-commit sweep on 2026-07-01 (*"stop quarantining
  reported tests so they re-run every snapshot"*, #1216).
- **A "FIX IT" page** replacing the PR-comment handoff (#2636, 08-13).
- **MCP-first onboarding** — the whole setup now runs through an MCP server the CLI registers for you.
- **Classifier on `gpt-5.6-luna`** (#1442, 07-11), with a capture-to-replay eval harness (#2149).
- **A fleet-wide kill switch for main-branch builds** (#2388) and a **circuit-breaker for repeatedly
  failing preview builds** (#2758, 08-24). **INFERRED:** you build those when the fleet is hurting.
- **Three new public repos**: `gatekeeper` (scale-to-zero K8s reverse proxy, MIT), `agent-mcp`,
  `mcp-registry` — all zero stars.

### Their release cadence

Date-stamped releases, up to 3/day: `v1.260824.2`, `v1.260811.1`. **27 GitHub Release objects total**,
newest published 2026-08-11, while release *commits* continue to 2026-08-24 — so the public Release
feed has been stale for two weeks while the code ships. `CHANGELOG.md` is **354KB** and its compare
links point at **`github.com/Autonoma-AI/agent`** — a private repo. **The public repo is a one-way
mirror**: 69 of the 1,447 commits are `sync: update marker … Source-Commit: <sha>` from
`autonoma-actions[bot]`. **There is no public changelog page on the website** (`/changelog` → 404).

---

## 5. The onboarding, reconstructed — and why a repo got switched to `autonoma-integration`

That was not a bug. **It is the shipped design, twice over**, and it is in the commit log:

> `2026-07-31 feat(cli): make the coding agent branch and open a PR for its integration (#1994)`
> `2026-08-10 feat(onboarding): set previews up on an integration branch instead of asking which branch to use (#2399)`
> `2026-08-10 feat(onboarding): make Autonoma-hosted previews the default the agent cannot talk itself out of (#2397)`

**MEASURED**, all three. And the docs confirm the repo-touching half, verbatim from
`docs.autonoma.app/llms/troubleshooting.txt`:

> "The one exception is the test-data step, which is real code: your coding agent implements the
> Environment Factory on **its own branch** and opens a pull request for you to review."

So a single `npx` command: takes over your terminal, **spawns a fresh coding agent of its own**
(Claude Code, Codex, Cursor — *"If you have a second agent installed, the planner switches to it by
itself"*), registers an MCP server with it, creates an integration branch, writes backend code onto
it, and opens a PR. The commit titled *"the default the agent cannot talk itself out of"* is them
hard-wiring the agent so a user cannot steer it off the hosted path.

### What their own docs admit about it

All verbatim from `docs.autonoma.app/llms/troubleshooting.txt`, fetched 2026-08-25:

- **"A full run takes an hour or more."**
- On whether it has hung: *"If both have been frozen for a long stretch, that is worth reporting."*
  They cannot tell you either.
- **"Can I delete an application and start over? Not from the dashboard today. Ask in the in-app
  chat and we will remove it."**
- GitHub App installs expire: *"Autonoma only connects an installation that GitHub created in the
  last half hour."* Miss the window and you must uninstall and reinstall.
- **"Can I connect a second GitHub organization? Not today."** One GitHub account per workspace.
- A documented, routine failure: the four upload chips, where *"the one that most often does not
  [arrive] is `recipe.json`"*, with an explicit warning **"Do not re-run the whole planner, and do
  not use `--resume`."**

### The support matrix, verbatim — and the contradiction in it

| You want to | Supported |
|---|---|
| Connect a **GitHub** repository | Yes — this is the only source host |
| Connect GitLab, Bitbucket, or Azure DevOps | No |
| Test a **web** application | Yes |
| **Test an iOS or Android app** | **No** |
| Use Autonoma without a repository | No |
| **Sign in with something other than Google** | **No** |
| Push results to Jira, Xray, or another test-management tool | No |

**Mobile is sold everywhere and supported nowhere.** The homepage meta description (CLAIMED):
*"Autonoma runs AI agents that test your web **and mobile** app end to end on every pull request."*
The FAQ: *"It works with any web or mobile application — React, Next.js, Vue, Angular, **Flutter,
React Native, Swift**."* The README ships `engine-mobile/ Appium-based mobile test execution`. The
schema carries iOS and Android credit rates. And the docs say **No**. That is a straight
marketing-versus-docs contradiction, **MEASURED on both sides, same day**.

---

## 6. Suite health — the most interesting thing they have built, and their worst commercial problem

`docs.autonoma.app/llms/suite-health.txt`. A five-rung public trust ladder, verbatim:

| Level | What it means |
|---|---|
| **Proven** 5/5 | Every failure here is worth reading. False alarms are rare. |
| **Steady** 4/5 | Tests are holding across pull requests and the agent is healing drift on its own. |
| **Calibrating** 3/5 | New suite. Written from your app, not yet proven against it. **Expect some noise.** |
| **At risk** 2/5 | **More tests are flaking than passing.** A few decisions from you will fix it. |
| **Degraded** 1/5 | Failures are piling up unresolved. **The agent can no longer tell a real bug from a stale test.** |

Score = `trust = (passed + confirmed bug) / every finding in the window`, over the last 20 analysis
runs, max 30 days old. Evidence gates clamp it in both directions: **Proven requires 20 runs, 8
distinct branches, a month of history, no stale open failures, and at least one issue you resolved.**

Then the sentence that should end any 14-day trial:

> **"Most suites reach Steady in about two weeks of normal pull-request traffic. Proven takes roughly
> a month, on purpose."**

And the framing they lead with, which is admirably honest and commercially fatal:

> "Autonoma writes your test suite by reading your code. **It has never operated your app.** Some of
> what it wrote is wrong."

One of the four score adjustments is *"**Pipeline failures** — up to -15 when analysis runs die
outright. A run that never finishes produces no findings, so it would otherwise be invisible."*
**INFERRED:** you only build a penalty term for your own pipeline dying if it dies often enough to
distort the metric. That is the 48% figure, showing up in the scoring model.

### They have no finding deduplication, and they say so

From `docs.autonoma.app/llms/suite-health/fixing.txt`, verbatim:

> "The same failure tends to land on every open pull request at once, so **a backlog of two hundred
> findings is often one broken thing counted two hundred times.**"
> "**One misbehaving endpoint accounted for 185 of that app's 200 findings.**"

92.5% duplication in their own worked example. And the "Fix it" button is not a fix — it is a dialog
with three manual steps: install the MCP, authorize in a browser, copy a prompt into your agent.

---

## 7. Traction, measured

| Signal | Value | Source |
|---|---|---|
| GitHub stars / forks | **187 / 46** | API, 2026-08-25 |
| Human contributors, lifetime | **10** (+3 one-commit drive-bys) | commits, paginated |
| **New human contributors in 90d** | **0** | first-commit dates all Apr–May 2026 |
| Discord | **130 members, 8 online** | invite API with counts |
| HN, all time | **3 stories: 5 pts / 3 comments, 1 pt, 1 pt** | hn.algolia.com |
| Reddit | **no organic threads found** | WebSearch |
| G2 / Capterra | **no profile found** | WebSearch |
| **Vercel Marketplace** | **1000+ installs** | vercel.com/marketplace/autonoma-ai |
| Public careers page | **404** — no job board found anywhere | probed `/careers`, `/jobs`; WebSearch |

**Vercel Marketplace at 1,000+ installs is their one genuinely good number** and the only channel
that has moved. It is also the channel the teardown told us to refuse, correctly: it is
partnership-gated and their route in was an investor.

### npm, and why the level is not the signal

**MEASURED**, 90-day range 2026-05-27 → 2026-08-24, weekly buckets:

| Package | 90d total | last full week | shape |
|---|---:|---:|---|
| `@autonoma-ai/planner` | 16,566 | 1,727 | **flat ~1,700/wk since mid-June** |
| `@autonoma-ai/sdk` | 98,654 | 11,275 | flat 8–11K/wk |
| `@autonoma-ai/server-web` | 27,620/mo | 9,855 | flat |
| `@autonoma-ai/sdk-prisma` | 140/mo | 22 | flat |
| `@autonoma-ai/sdk-drizzle` | 113/mo | 25 | flat |
| `@autonoma-ai/sdk-pg` | 57/mo | 10 | flat |
| `@autonoma-ai/sdk-mysql2` | 55/mo | 10 | flat |

Do not read 1,727 weekly `planner` invocations as 1,727 onboardings. They run it themselves — there
is an `SDK-integration eval harness for the planner CLI` (#1500) in the repo. **The level is
un-diagnostic; the flatness is the signal.** Eleven straight weeks inside a ±20% band, across a
quarter in which they shipped 1,447 commits, is a product whose adoption curve is a horizontal line.

The database adapters are the number that cannot be explained away by CI: **22, 25, 10 and 10
downloads a week.** The Environment Factory — the piece a customer must implement in their own
backend, the moat, the 660KB of protocol — has double-digit weekly pulls across all four adapters
combined.

---

## 8. The SEO machine, in motion

**MEASURED** from `sitemap.xml` (571 URLs, fetched 2026-08-25): **567 blog posts, and exactly four
non-blog pages** — `/`, `/blog`, `/developers`, `/contact`. Teardown counted 527. **+40 net.**

Publishing by `lastmod` month: Mar 68, Apr 166, May 36, **Jun 76, Jul 95, Aug 73** — 244 posts since
June 1, ≈2.8/day, sustained. The machine is running at full tilt.

Three things about *what* it is producing:

**(a) The highest-value cluster has not grown since April.** Still exactly **44
`opensource-alternative-{competitor}` pages** — browserstack, mabl, qa-wolf, testrigor, testim,
katalon, testrail, sauce-labs, lambdatest, applitools, and 34 more. Zero added in 4½ months. They
built their best asset in a 48-hour batch and then walked away from it.

**(b) They now SEO a component of their own stack as if it were a product.** July shipped
`previewkit-vs-qovery`, `-uffizzi`, `-northflank`, `-release`, `-signadot`, plus
`neon-branching-for-previews`, `supabase-branching-for-previews`, `planetscale-branching-for-previews`.
**INFERRED:** they are testing whether the preview-environment layer — the 232-commit sink — can be
sold on its own. That is a company hedging on its own positioning.

**(c) The blog has decoupled from the product.** Late July and August produced clusters with no
connection to QA at all: `claude-code-vs-cursor`, `best-local-llm-for-coding`, `ollama-alternatives`,
`lm-studio-vs-ollama`, `vllm-vs-ollama`, `openrouter-alternatives`, `deepseek-vs-claude-code`. And a
**15-page accelerator cluster** — `close-customers-before-{techstars, antler, entrepreneur-first,
pear-vc, seedcamp, on-deck, indiebio, plug-and-play, startupbootcamp, startup-chile, platanus, nxtp,
wayra, 500-global, 500-global-latam}-demo-day`. Pre-seed founders at demo day have no budget, no PR
volume, and nothing to test. **INFERRED: they have exhausted buy-intent QA keywords and are farming
volume.** Mid-August then reverts to undifferentiated glossary terms — `boundary-value-analysis`,
`equivalence-partitioning`, `stlc-vs-sdlc`, `defect-density-and-leakage`.

**(d) Their GEO surface is genuinely best-in-class and it is directly in our lane.** MEASURED:
`llms.txt` (a real curated index, not a dump), `.well-known/ai-catalog.json` (Agentic Resource
Discovery), an `Agentmap:` directive in `robots.txt`, **every page served as markdown by content
negotiation** (`Accept: text/markdown` or `/md` prefix, with `Vary: Accept`, a `406` on a bad Accept,
and a **markdown 404 body that navigates you back**), a `/developers` portal, a JSON `/api` error
envelope, an MCP server at `api.autonoma.app/v1/mcp` with **OAuth or API key**, and
`docs.autonoma.app/llms-full.txt`. Their 404 page is better engineered for agents than most
companies' homepages. This is the one place they are unambiguously ahead of us.

---

## 9. Customer evidence beyond the logo bar — there is one, and it is stale

Logo bar (CLAIMED, homepage): Vercel, Mercor, Superhuman, Hedra, Luxury Presence, Kavak, Uala,
Sandstone.

**MEASURED: exactly one case study exists — `/blog/kavak` — and it is dated `2025-11-17`.** Nine
months old, older than the current product. It is **absent from `/md/blog`, the article index they
serve to agents**, though it is in the sitemap.

And it describes a **different product**. Verbatim from the post:

> "Autonoma AI is configured to **continuously test production environments**, automatically creating
> **Jira tickets** whenever an anomaly is detected."
> "Autonoma runs **scheduled tests** across kavak.com and selected internal apps."

Against today's support matrix, same day:

> Push results to **Jira**, Xray, or another test-management tool → **No**
> Use Autonoma **without a repository** → **No — reviews are driven by pull requests**
> "What actually triggers a test run? **Pull request activity.**"

**Their only named, customer-co-signed reference is for a product they discontinued.** It is
production monitoring with Jira ticketing; they now sell PR-gated preview testing. Nothing in the
Kavak story could be delivered by the 2026 product. The quote is from *Antony Delgado, IT Solutions
Center Manager* — not an engineering leader, not the CTO. **MEASURED.**

The teardown's read — "the logos are the cap table" — needs one amendment. Some are cap table
(Luxury Presence via Bessemer's board seat, Vercel via Rauch). **Kavak and Ualá appear to be real,
but earned by the pre-2026 product.** The timeline is: company founded 2023 → Kavak case study Nov
2025 → Vercel Marketplace post Oct 2025 → repo created Mar 2026 → current product ~5 months old. The
logo bar is nine months of goodwill for five months of product.

The teardown's best steal still holds and is now sharper: **the one named case study contains zero
headcount language**, while "$2M workforce cost optimization" and "60 manual testers repurposed" live
in an anonymous blog post you have to go looking for.

---

## 10. Team, funding, hiring

- **CEO/co-founder Eugenio Scafati; CTO/co-founder Tomás Piaggio** (ex-Google AI, Buenos Aires).
  Org location: Argentina. **MEASURED** (GitHub org, LinkedIn/theorg search results).
- **Investors: Bessemer Venture Partners (lead), Proximity Angels, Vanaxis Investment Group; angels
  Guillermo Rauch (Vercel) and Matías Woloski (Auth0).** **CLAIMED/secondary** — Crunchbase and
  PitchBook both blocked direct fetch (403); this is from search-result summaries. **Round size:
  UNVERIFIED.** PitchBook's "$25K" is a data artifact, not a round.
- **Headcount: ~17 total (UNVERIFIED, PitchBook via search).** What is **MEASURED** is that
  **10 humans commit to the codebase and none of them is new in 90 days.**
- **No public hiring surface at all.** `/careers` and `/jobs` → 404; no Ashby, Greenhouse, Wellfound
  or startup.jobs listing found. **INFERRED: they are not hiring, or not hiring publicly.** For a
  Bessemer-backed company shipping 1,447 commits a quarter, a frozen headcount is a runway signal or
  a deliberate small-team bet. I cannot distinguish which.

---

## 11. Where they are visibly weak or stalled — ranked

1. **A buyer cannot trust the product inside a trial, and they publish this.** "Proven takes roughly
   a month, on purpose." "Calibrating… expect some noise." "At risk: more tests are flaking than
   passing." The honesty is admirable engineering and terrible go-to-market: no evaluator on a
   two-week window ever sees the product at its best.
2. **48% of classifications have no recording — their own number, unchanged since April.** Half the
   runs die in the environment layer. This is the load-bearing failure of the whole architecture,
   and 232 previewkit commits in 90 days did not move the published figure.
3. **No finding deduplication.** "185 of that app's 200 findings" was one endpoint. A tool that
   reports a bug 185 times trains its users to ignore it.
4. **Setup is an hour-plus, hands your machine to a spawned coding agent, and writes code into your
   repo on a branch you did not choose.** And you cannot delete the application afterwards without
   asking a human in a chat widget.
5. **Marketing sells mobile; docs say mobile is not supported.** Same day, same company. Anyone who
   reads both loses trust in everything else on the page.
6. **Google-only sign-in. GitHub-only source host. One GitHub org per workspace. No Jira, Xray, or
   test-management export.** Each is a single line in the docs and a hard disqualifier for a whole
   segment.
7. **Their only named customer story is nine months old and describes a discontinued product.**
8. **Unit economics inverted.** The Cloud plan sells $666 of at-cost model spend for $499. Runs are
   still free. Compute is still rate-zero. The subsidy got a subscription wrapper, not an end date.
9. **Community never started.** 187 stars, 130 Discord / 8 online, 3 HN posts topping out at 5
   points, no Reddit, no G2, zero outside contributors in 90 days. The BUSL bought a badge, not a
   community — and the badge cost them the ability to ever say MIT.
10. **The blog has drifted off-product.** `ollama-alternatives` and
    `close-customers-before-techstars-demo-day` are not QA buyers. Meanwhile the 44-page
    `opensource-alternative-{X}` cluster — their highest-intent asset — has not gained a page since
    2026-04-03.
11. **The public repo is a stale mirror.** GitHub Releases stopped at 2026-08-11 while release
    commits continue to 2026-08-24; no changelog page on the website; compare links point at a
    private repo. The "open source" surface is drifting from the real one.

**And where they are strong, honestly:** 1,447 commits/quarter from 10 engineers is real execution.
The two-plane verdict split (app-health vs coverage-confidence) is the best idea in their codebase.
Suite health with evidence gates in both directions is a piece of product thinking we do not have an
answer to. Their agent/GEO surface is the best I have measured anywhere. 1,000+ Vercel installs is a
real distribution win. Do not mistake a flat adoption curve for a weak team.

---

## 12. Read against our own runner and cloud

Grounded in `~/smolanalytics/cli/README.md` and `~/smolanalytics-cloud`, read 2026-08-25.

| | Autonoma | Ours |
|---|---|---|
| Where the app under test comes from | **They build and host it.** Full-stack preview per PR: apps, databases, side services, secrets, hooks, multirepo. 232 commits/90d. ~48% of classifications never get a recording | **The preview URL you already have.** Vercel, Netlify, Fly, Render, staging. Zero environment layer to fail |
| Test data | **Environment Factory**: an endpoint you implement in your backend, 8 languages, written for you by a spawned agent on an `autonoma-integration` branch. Adapters pull 10–25 downloads/week | Obviously-synthetic identities in the sentence (`{{email}}`, `smoltest+…@example.com`, RFC-2606 domain), one `LIKE 'smoltest%'` finds every row, `--teardown` POSTs the identity to your own endpoint after **every** run including failures |
| Time to a trustworthy verdict | **~1 month, self-documented.** Setup alone "an hour or more" | 60 seconds, one command, no account |
| Whose infra, whose key | Theirs; model calls resold through their credit meter at 1,500 credits/$ | **Your CI runner, your `ANTHROPIC_API_KEY`.** Not resold |
| Statuses | 7-verdict taxonomy split into an app-health plane and a coverage-confidence plane. Plus a 5-state suite trust score | 5 per-test statuses: **passed / failed / flaky / stale / errored**, with exit codes on the same split — `0` nothing failed, `1` a test failed, `2` the runner could not finish |
| Flake | **No flake module.** Quarantine was removed 2026-07-01. Flakiness surfaces only as a suite-level score | `--retries`, pass-on-retry is **flaky not passed**, warns without reddening the build |
| Second run | Re-runs the agent | **Record/replay**: 8.0s agent → 1.4s replay, no model calls. `stale` when the recording stops fitting, never worded as a failure |
| Licence | BUSL-1.1, converts to Apache 2.0 on **2028-03-23** | ours |
| Pricing | 4 shapes, no `/pricing` page, per-org rates, the advertised run meter never fires | tested PRs, $19/mo, one plan |

**Three things to take, and one to stop kidding ourselves about.**

Take: **the two-plane split** — "did your app break" and "could we tell" are different questions and
should never share a column; our `errored`/`stale` are already the coverage plane, we have just never
named the partition. Take: **evidence gates in both directions** — a new suite should be barred from
both "trusted" and "degraded" until it has run enough, and we have no equivalent. Take: **the
markdown-mirror GEO surface** — content negotiation on every page, a curated `llms.txt`, an
`ai-catalog.json`, a navigable markdown 404. That is a weekend of work and they are demonstrably
ahead of us on it.

Stop kidding ourselves: **their weakness is not their engineering, and copying their SEO will not
beat them.** 567 posts bought them 3 HN points, 130 Discord members and a flat npm line. The one
channel that moved was a marketplace they got into through an investor. Their real vulnerabilities
are a month-long time-to-trust, an environment layer that eats half their runs, and a product that
takes over your terminal for an hour and opens a PR against your repo before it has earned anything.
Every one of those is a *shape* problem, and the shape we already have — your preview URL, your CI,
your key, sixty seconds, no account — is the direct answer to all three. That is the thing to sharpen,
not the blog.
