# HOW THEY SELL — what a buyer SEES when evaluating each rival

Research date: 2026-08-30. Method: primary sources fetched directly today (marketing pages,
pricing pages, docs, changelogs, GitHub repos, npm, review sites, HN/Reddit threads). This
document is about **presentation and the buying experience**, not capability. Capability is
already covered by the companion files in this folder and is not re-derived here:
`AUTONOMA_TEARDOWN.md`, `AUTONOMA_IN_MOTION.md`, `FIELD_AGENT_FIRST.md`,
`FIELD_INCUMBENTS_AND_FREE.md`, `GRAVEYARD_AND_BUYER.md`, `SCORECARD.md`, `RED_TEAM.md`,
`CROSS_POLLINATION.md`, `WHITESPACE_DB_AND_AI.md`.

**Evidence labels used on every claim:**
- **MEASURED** — I fetched the page / ran the command / read the file today, and the claim is what
  it literally said or did.
- **CLAIMED** — the vendor's own marketing asserts it; I am reporting that they assert it.
- **REPORTED** — a third party (review site, HN, Reddit, press) asserts it.
- **INFERRED** — my read of the evidence, flagged as opinion.
- **UNVERIFIED** — I could not confirm it and it stays unconfirmed. Never quietly promoted.

Our buyer, for every judgement below: a solo builder or a 5–50 person startup shipping web apps.
Engineers. No enterprise motion, no seats, no sales calls.

STATUS: complete.

---

# PART 0 — GROUND TRUTH ON OUR SIDE (verified before scoring anyone)

The scorecard in `SCORECARD.md` is dated 2026-08-24 and is stale. Every feature the brief listed as
shipped since then was checked against the code in `/Users/arjun/smolanalytics/cli` today
(2026-08-30). All MEASURED.

| Claimed shipped | Verified where | Status |
|---|---|---|
| Parallel execution, one browser + a context per worker | `cli/lib/pool.mjs` (405 lines). Header carries the measured table: `4 workers 8.1s peak 741MB` / `8 workers 4.9s peak 917MB` for one browser + context-per-worker, against `14.8s/1450MB` and `9.6s/2233MB` for a browser per test. Serial ladder in the same file: `workers 1 → 39.2s`, `2 → 15.0s`, `4 → 8.2s`, `8 → 4.9s`, `16 → 3.8s`. `--workers` parsed and refused rather than coerced. | **CONFIRMED.** Note the brief's "39s → 4.6s" is `39.2s → 4.9s` in the file. Use the file's numbers publicly. |
| Cross-browser chromium/firefox/webkit | `cli/lib/engines.mjs`: `export const ENGINES = ["chromium", "firefox", "webkit"]`, labels, and a refusal message naming the one-off download. Engine is part of the pool cache key (`pool.mjs:271`) and one engine per suite (`suite.mjs:368`). | **CONFIRMED** |
| File uploads, generated fixtures, magic-byte validated | `cli/lib/upload.mjs` (502) + `cli/lib/uploadsafe.mjs` (121). Fixtures are fabricated at run time, deliberately not stored in the recording ("would have put a base64 blob in somebody's repository"); `uploadsafe.mjs` exists because `setInputFiles` accepts a `<label>` and a wrapper and would silently target the wrong node. | **CONFIRMED** |
| `--seed` hook | `cli/lib/seed.mjs` (366) + `seedguard.mjs` (145). POSTs the run identity to a customer-written endpoint before the test, reads back JSON as placeholders, secret is env-only by design, inert when unused, and a seed failure is explicitly *not* a verdict about the app. | **CONFIRMED** |
| False-green render guard | `cli/lib/render.mjs` (685). Detects blank render, same-origin CSS 404 (with the correct Chromium subtlety: a 404'd sheet still has non-null `link.sheet` with `cssRules.length === 0`), and framework error overlays including ones in shadow DOM (Next.js, Vite, webpack-dev-server, react-error-overlay). Escape hatch `--no-render-check` exists. | **CONFIRMED.** This closes SCORECARD row 9, which was a loss to Autonoma. |
| Authenticated runs, one login per suite, credentials never on disk | `cli/lib/auth.mjs` (549). `--auth-file` for an existing storageState, `--login "<sentence>"` for a recorded login reused across the suite; every log line passes through `redact()`; session-bounce detection with exactly one repair login. | **CONFIRMED** |
| Token metering + `--max-calls` | `cli/lib/cost.mjs` (145). Exact token counts from the API (`input/output/cache_read/cache_creation`), optional `SMOLANALYTICS_PRICE_IN/OUT` to render money, and `overBudget()` stops at the ceiling with an honest message: *"Nothing is known about whether the app works."* | **CONFIRMED** |
| `--share` public run page, no account | `cli/lib/share.mjs` (937 — the largest lib file, and almost all of it is secret-scrubbing before anything becomes public: bypass tokens in URLs, keys quoted back in 401 bodies, passwords in step labels). | **CONFIRMED** |
| `--since` diff-aware selection | `cli/lib/select.mjs` (405). Intersects a recording's observed surface with `git diff`; every failure mode (no git, bad ref, empty diff, undiffable file) **runs everything and says why**. | **CONFIRMED** |

Two facts that change how we may talk about ourselves:

- **`cli/package.json` now reads `"license": "SEE LICENSE IN LICENSE"`, not `"MIT"`** (MEASURED today).
  SCORECARD row 17 flagged the MIT-vs-commercial-pivot contradiction as unresolved; the package
  manifest has since resolved it toward commercial. **Any comparison copy that says "MIT" is now
  false.** This also costs us the cleanest contrast with Autonoma's BUSL — see Part 1.
- Version is `0.16.1`, and the package `description` still leads with testing, then analytics.

---

# PART 1 — AUTONOMA

Fetched 2026-08-30: `https://www.getautonoma.com/`, `https://github.com/autonoma-ai/autonoma`,
`https://getautonoma.com/blog/autonoma-vs-momentic`, `https://getautonoma.com/blog/autonoma-vs-qa-wolf`.

### What changed since our last look — stated explicitly

`SCORECARD.md` recorded on 2026-08-24 that **`getautonoma.com/pricing` returned 404**. That is still
literally true — `https://www.getautonoma.com/pricing` **404s today** (MEASURED) — but it is no
longer the whole story, and I am contradicting the implication with evidence: **the homepage now
carries a Pricing nav link and an on-page pricing section with real numbers** (MEASURED):

- Free tier: **100K credits**
- Pay-as-you-go: **$100 per 150K credits**
- Self-hosted: **free**

Cross-checked against `AUTONOMA_TEARDOWN.md`'s reading of their billing code (credit ≈ $0.000667):
$100 / 150K credits = **$0.000667/credit — exactly the rate in their source**. The teardown's number
is now confirmed by their public page. What the teardown found in code and the page does not say:
a web run is 10 credits, generation 500, an iOS run 200. So $100 buys, at list, ~15,000 web runs.
INFERRED: the meter is priced to be ignorable, which matches the teardown's finding that enforcement
was off fleet-wide. **They are not trying to make money on runs yet.**

### The demo — what moves first

The hero is a **live interactive sandbox**: an AI agent navigating a login form and dashboard, agent
actions printed in real time (MEASURED). This is the strongest demo format in the entire field. It
is not a video and not a GIF; there is nothing to press play on and nothing that can be accused of
being edited.

The **oh moment is the agent typing into a login form it was never told the selectors for**, and it
arrives in the seconds it takes the page to paint — no click required. CTAs are
**"Find Your First Bug"** and **"View demo"** (MEASURED). "Find Your First Bug" is the best CTA
copy in this research: it names an outcome, not an action, and it presupposes there is a bug.

### Social proof — and how much of it is checkable

- Logos: **Vercel, Mercor, Superhuman, Hedra, Luxury Presence, Kavak, Uala, Sandstone** (MEASURED as
  displayed). Whether these are customers, users, or investors' portfolio companies is **UNVERIFIED**
  — the page's own header is the hedged "Trusted by teams shipping fast".
- Testimonial: **"Incredible. The opportunity is going to be huge." — Guillermo Rauch, CEO of Vercel**
  (MEASURED as displayed). A named, checkable, very senior person. INFERRED: this single line is
  doing more work than the entire logo wall.
- GitHub: **190 stars, 48 forks, 3 watchers, 1 open issue, 1,832 commits on main** (MEASURED today).
  Checkable, and modest. 190 stars is not a community; **1 open issue against 1,832 commits** is
  INFERRED as a repo where nobody outside the company is filing anything.
- No review-site badge, no G2 rating, no case study with a number.

### The word "open-source", and the licence

Their homepage subhead and their GitHub description both begin **"Open-source testing platform"**
(MEASURED, both). GitHub itself renders the licence as **Business Source License 1.1** (MEASURED),
which the teardown already established is not an open-source licence by the OSI definition and by
MariaDB's own page. **The contradiction is on one screen**: the description says open-source and the
licence box next to it says BUSL.

This was our sharpest attack line. **It is now partly blunted on our side** — see Part 0: our own
package manifest no longer says MIT. INFERRED, and it matters for what we are allowed to write: we
can still say *they are not open source and say they are*, because that is a factual contradiction
in their own repo. We can no longer follow it with *"ours is MIT"*. The honest version of the line
is about **exportability of the artefact**, not about licences: their tests live in their platform,
ours are plain files in the customer's repo.

### Their comparison pages — a whole SEO cluster

Autonoma is running a buy-intent comparison cluster, not a page. Found today (MEASURED, all live):
`/blog/autonoma-vs-qa-wolf`, `/blog/autonoma-vs-momentic`, `/blog/opensource-alternative-qa-wolf`,
`/blog/opensource-alternative-momentic`, `/blog/mabl-alternative-for-small-engineering-teams`,
`/blog/ai-testing-platform-comparison`, `/blog/what-an-ai-qa-agent-actually-does`.

Note the URL pattern: they hold both **`X-vs-Y`** (comparison intent) and **`opensource-alternative-to-X`**
(escape intent) for each named rival, plus a **`<rival>-alternative-for-small-engineering-teams`**
page that segments by team size. INFERRED: this is a deliberate buy-intent matrix, and it is the
single most copyable thing they do.

**Rows they chose on `autonoma-vs-momentic` (verbatim, MEASURED):** Test generation source ·
Self-healing tests · New feature detection · Database state setup · Test creation experience ·
CI/CD integration · Non-technical QA support · Coverage mapping to codebase · Execution
verification layers · Issue tracker integration · AI-generated code awareness · Test maintenance
model · Setup time to first test · Parallel execution · Free tier available · Pricing model.
Plus a second pricing-only table: Free tier · Starting paid price · Pricing model · Enterprise ·
Test maintenance cost · Test authorship cost · Engineer time per new feature.

**Rows on `autonoma-vs-qa-wolf` (verbatim, MEASURED):** Test creation model · New feature coverage ·
Test maintenance · Database state setup · Human judgment in coverage · AI-velocity compatibility ·
Self-healing · Execution framework · CI/CD integration · Pricing model · Hands-off for customer ·
Accountability model · Best fit. Plus a speed table (time to create a test, time to update, execution
time, coverage for 10 features, handling 50 PRs/week) and a **cost-scaling table** that puts QA Wolf
at **~$2,000–2,200/mo for 50 tests, ~$40,000–44,000/mo for 1,000 tests ($40–44/test)** — sourced,
in their own footnote, to *"publicly reported per-test pricing from G2, Vendr, and competitor
analyses"* and hedged with *"QA Wolf does not publish official pricing"* (MEASURED). That is
REPORTED-at-best, and they label it as such — which is more honest than most vendor comparisons and
worth copying as a practice.

**What their row choice reveals (INFERRED).** Autonoma's rows are overwhelmingly about **who does
the labour** — generation source, authorship cost, maintenance model, engineer time per feature,
throughput ceiling. They believe the buyer is buying *the removal of a person's work*, not a better
verdict. Notice what is **absent from every one of their tables**: flake, trust, false positives,
evidence, exit codes, whether the verdict is right. They never once compete on *is the answer
correct* — which is precisely the axis SCORECARD says we win on.

### First five minutes

Self-serve exists (Login in nav, a free 100K-credit tier). I did not create an account, so what
follows is **REPORTED from our own prior measurement, not re-measured today**: `AUTONOMA_IN_MOTION.md`
and the header of `cli/lib/test.mjs` record a real onboarding attempt — install the GitHub App across
repos → the agent pushes a Dockerfile into your codebase → hand over `SUPABASE_URL` / `OPENAI_API_KEY`
→ "ETA ~1h 13m" → 28 minutes in at 3% → 0% → step 1 of 7 failed. Nothing on the site today suggests
that path changed.

INFERRED, and it is the whole strategic point: **the hero demo is seconds to "oh", and the actual
product is an hour to a verdict, if it lands.** The gap between their demo and their onboarding is
the largest in the field, and it is ours to attack — not with a claim, but by having a first run
that finishes.

**Our own test suite, re-measured today for the record (MEASURED):** `node --test --test-concurrency=3 test/*.test.mjs` over 34 test files →
`tests 839 · suites 137 · pass 839 · fail 0 · cancelled 0 · skipped 0`, duration 151.4s, exit 0.
The scorecard's "290 tests" is six days old and now understates us by 549.

---

# PART 2 — QA WOLF

Fetched 2026-08-30: `https://www.qawolf.com/`, `https://www.qawolf.com/pricing`.

### The demo

Hero headline **"Agentic QA that just works"**; subhead **"Testing has to be fast"** followed by
*"When testing can't keep up with AI, PRs pile up, releases get stuck in QA, and incidents halt
development."* (MEASURED). The hero visual is **an animated GIF of a pixel-art blue wolf running**
(MEASURED). That is a mascot, not a product. **Nothing about the product moves above the fold.**

INFERRED: QA Wolf is the most established name here and has stopped selling the mechanism. Their
"oh" moment is deferred to a testimonial line further down — *"The tests run in 11 minutes. There's
about 300 and we rarely get a false negative."* That sentence is the best social proof on any page
in this research, because it is three checkable numbers about the thing buyers actually fear, and
it is buried below a running wolf.

### Pricing — and the split that matters

**Two products, and only one of them is for our buyer** (MEASURED):

| | Platform (self-serve) | Coverage as a Service (managed) |
|---|---|---|
| Price | **1¢ per AI credit**, **15¢ per runner minute** | **Custom**, by number of tests under management |
| Seats | "No per-seat or per-user fees" | — |
| CTA | **"Try for free"** | **"Book a demo"** |

FAQ, verbatim: *"you only begin paying for AI credits (1¢ each) and runner minutes (15¢ each) as you
use them"*, and pricing is *"usage-based, not seat-based"*. They also claim **"unlimited"** AI usage
and **"unlimited parallel runs"** (CLAIMED — "unlimited" alongside a per-credit meter is at minimum
in tension, and I could not reconcile it from the page; UNVERIFIED).

**15¢ per runner minute is the number to remember.** INFERRED arithmetic, not their claim: the
300-test suite in their own testimonial runs in 11 minutes of wall clock, but runner minutes are
summed per parallel runner, not wall clock. A 300-test suite where each test averages 30s of runner
time is 150 runner-minutes = **$22.50 per full suite run**. Ten runs a day is **~$6,750/mo** before
a single AI credit. That is the meter shape `GRAVEYARD_AND_BUYER.md` calls churn reason #2:
**it charges you more the more carefully you test.**

Autonoma's cost-scaling table puts QA Wolf's managed arm at **$40–44/test/mo** (REPORTED via
Autonoma citing G2/Vendr; QA Wolf publishes nothing to confirm or deny — UNVERIFIED).

### Social proof

- **G2 badge: 4.8 stars, 100+ reviews** (MEASURED as displayed). Checkable, and by a wide margin the
  strongest third-party proof anyone in this field has.
- **No customer logo wall** (MEASURED absence). Company names appear only inside testimonials.
- Numbers in testimonials: *"300 [tests] in 11 minutes"*, *"we rarely get a false negative"*,
  *"three releases in about three weeks"*.

INFERRED: this is a company selling on **reviews rather than logos** — the opposite of Meticulous
(below), and a signal they are being bought by ICs and engineering managers, not procured top-down.

### First five minutes

Self-serve **"Try for free"** exists, and the pricing page says AI credits and runner minutes only
start billing on use. I did not create an account, so the post-signup path is **UNVERIFIED**. What
is MEASURED is that the self-serve door is open and priced in public — which puts QA Wolf ahead of
Mabl, Testim, Rainforest, Meticulous and Reflect on the only gate that matters for an engineer
buyer.

---

# PART 3 — MOMENTIC

Fetched 2026-08-30: `https://momentic.ai/`, `https://momentic.ai/pricing`, `https://momentic.ai/docs/`
(`docs.momentic.ai` 301s here), `https://momentic.ai/comparison/momentic-vs-qa-wolf`.

**This is the rival closest to our position, and the one page in this research that should worry us.**

### Pricing — the cleanest in the field

| Tier | Price | What you get |
|---|---|---|
| Free | **$0/month forever** | **2,000 credits/mo (~200 test runs)**, all core features and AI test authoring, 30-day retention, **no credit card required** |
| Pay-as-you-go | **$125/month + additional usage** | — |
| Enterprise | Custom | "Contact sales" |

Meter, verbatim: *"Every test step uses one credit"*, and *"a typical run is about 10 steps"*
(MEASURED). CTAs: "Get started" / "Try for free" / "Contact sales".

INFERRED: a per-step meter is the *most* usage-punishing unit in this entire document — a test that
does more work costs more, so the incentive is to write shallow tests. But the free tier is real:
200 runs a month, no card, produces an actual verdict. **A free tier that produces a real verdict is
the rarest thing in this field, and only Momentic, Checkly and Playwright have one.**

### The demo, and the social proof

Hero: **"Catch real bugs before they ship."** / *"Write end-to-end tests for web and mobile in plain
English. Momentic runs them everywhere and keeps them up to date."* CTAs **"Try for free"** and
**"Contact sales"** (MEASURED). The hero visual was not resolvable from the fetched markup —
**UNVERIFIED**; the page's own headings suggest the moving proof is the section **"Bugs caught by
Momentic"** (MEASURED heading).

Section order (MEASURED): The agentic quality platform → Define what matters. Let Momentic verify it.
→ Learns how your product actually works. → Loved by engineering teams. → Built for scale, security,
and control. → **Bugs caught by Momentic** → Close the feedback loop. → **Changelog**.

Logos (MEASURED as displayed): **Notion, Webflow, Runway, GPTZero, Quora, Retool, CoverGo, Mutiny.**
Case-study numbers, verbatim (MEASURED as displayed, and each is CLAIMED as to truth):

- Quora — *"30 min daily test execution, down from 7 hours"*, *"500+ manual test cases replaced"*
- Retool — *"8x increase in release cadence"*
- GPTZero — *"80% faster release cycles"*
- CoverGo — *"6x faster end-to-end test creation"*
- Mutiny — *"85% reduction in production incidents"*

That is **five named customers with a hard number each**. Nobody else in this research has that.
Two structural notes, both INFERRED: every number is about **speed or volume**, never about a bug
caught or a false positive avoided; and a **Changelog on the marketing page** is a shipping-velocity
signal aimed squarely at engineers — cheap, honest, and something we do not do.

### The part that lands closest to us

Their own comparison table, `momentic-vs-qa-wolf`, row **"Who writes tests"**, verbatim:
**"Cursor, Claude Code and Codex, through our MCP server"** (MEASURED). And the best-fit row:
*"Your coding agents write the tests, and you keep them."*

Their docs quickstart is `npx --yes @momentic/wizard@latest -y --platform web --editor-tools skills`
(MEASURED, verbatim) — an npx wizard that installs **agent skill files** into the customer's editor.
Four steps to a verdict: install → authenticate with `MOMENTIC_API_KEY` → write a YAML test in
natural language → run via CLI locally or in CI. Tests are **YAML files in the customer's repo**
(MEASURED).

**A contradiction inside their own materials, stated plainly:** the comparison page and the homepage
changelog both name an **MCP server**; the docs I fetched **do not mention MCP at all**, describing
instead *"a team of agents built into the CLI"* and skill markdown for "your coding agent"
(MEASURED both ways). Which is current is **UNVERIFIED**. INFERRED: they are mid-migration from MCP
to skills — the same direction the ecosystem is moving.

**Where their table quietly hurts them, and we should take the row:** their own comparison admits
**"Ownership of test code: N/A, tests run on Momentic's platform"** while granting QA Wolf *"You own
the underlying Playwright/Appium code"* (MEASURED). They wrote our attack line for us. Their YAML
lives in the repo but cannot execute without their platform and an API key.

### Contradicting an earlier file, with evidence

`FIELD_AGENT_FIRST.md` treats Momentic as a plain-English test platform. As of today that
understates them: they are running **agent-authored tests via editor skills, a live comparison
cluster (`/comparison/momentic-vs-*`: qa-wolf, mabl, BrowserStack, Testlio), a free tier that
returns real verdicts, and five numbered case studies.** On *presentation*, Momentic is the
strongest operator in this field. Capability comparison is unchanged; this is a GTM correction.

---

# PART 4 — THE INCUMBENTS: PRICE IS THE TELL

None of these four will show an engineer a number. All MEASURED on their own pricing pages today.

| Vendor | Page says | Meter | Free path | CTA |
|---|---|---|---|---|
| **Mabl** (`mabl.com/pricing`) | **No tier names, no dollar figures at all.** "customized pricing", "flexible pricing model tailored to your testing requirements" | Credits; *"a starting point of 500 credits per month for cloud test runs, shared across browser and mobile UI and API testing as well as performance and accessibility testing"* | 14-day trial | **"Request a Quote"**, **"BOOK A DEMO"** |
| **Testim** (`testim.io/pricing`, **now Tricentis-branded throughout**) | Tier *names* only — Community, Essentials, Pro, Mobile, Enterprise — **no figures**. Split into three products (Salesforce / Web / Mobile) | not stated | Community free plan; *"when the trial is complete, it will revert to the Community free plan"* | **"Contact Us"**, "Try Testim for Free" |
| **Rainforest QA** (`rainforestqa.com/pricing`) | **No tiers, no figures, no meter.** Trial is gated behind a call: *"Get your initial questions answered, see a product demo, and set up your free trial."* Page also returned **"Rainforest is not available in your region"** | not stated | Sales-gated trial | **"book a demo"**, `/talk-to-sales` |
| **Reflect** (`reflect.run/pricing`, **SmartBear-owned** — footer: *"Copyright © 2026 SmartBear Software"*) | Tiers Premium / Advanced / Enterprise, **all "contact sales", no figures** | Credits: **web test = 1, mobile = 5, API = 0.1**; Premium 5,000/mo, Advanced 20,000/mo, Enterprise 40,000/mo | 14-day trial, *"unlimited functionality"* | **"Try for free"**, **"Contact Sales"**, "Book a demo →" |

**Meticulous** belongs with them on this axis despite being a modern product: **no pricing displayed,
demo required** (MEASURED). Its hero is *"exhaustive verification. zero developer effort."* over a
**static dashboard screenshot** — nothing moves — and its proof is the heaviest logo wall in the
field: **ElevenLabs, Brex, Dropbox, Core, Mercor, Notion, LaunchDarkly, Wealthsimple, Weights &
Biases, Cortex, Wiz, Engine, Canary Technologies, Bilt, Outtake**, with named quotes from **Dropbox**
(*"no more debugging after merge, zero maintenance, and no flakes"*) and **Notion** (MEASURED as
displayed). Their onboarding *is* their demo: the section heading is literally **"Get started by
adding the meticulous recorder script tag"** — a one-line install, which is a genuinely strong
first-five-minutes story undermined by the fact that you must book a call to learn the price.

INFERRED, and it is the single most exploitable fact in this document: **five of the six most
established vendors in this category will not tell an engineer what it costs.** `GRAVEYARD_AND_BUYER.md`
already recorded the buyer's response to this (the Preflight HN thread: *"without a pricing page I
just move along"*). Nothing has changed. Every one of them is optimising for a procurement motion
our buyer does not have.

---

# PART 5 — THE FREE TIER: CHECKLY, PLAYWRIGHT, MIDSCENE, STAGEHAND

These do not sell against us; they set the price of the alternative to zero, which is worse.

### Checkly — the only incumbent that publishes everything (MEASURED, `checklyhq.com/pricing`)

**Hobby $0** · **Starter $24/mo** · **Team $64/mo** · Enterprise custom. Hobby includes 10 uptime
monitors, **10,000 API check runs/mo, 1,000 browser check runs/mo**, 6 locations, 1 dashboard, and
**10 AI root-cause-analysis invocations/mo**. Overages published to the cent: Starter
**$6.50 per 1k browser runs / $2.60 per 10k API runs**; Team **$6.25 / $2.50**.

And the line our flake work exists to beat, verbatim from their docs: **"Each retry counts as a
check run."** (MEASURED). A retry is billed. On our side a retry runs on the customer's CI and
produces a `flaky` verdict rather than an invoice — that is not a feature comparison, it is a
business-model comparison, and it is the cleanest one we have.

INFERRED: Checkly is proof the transparent-pricing motion works in this category. They are the
price anchor an engineer already has in their head — **$24–64/mo** — and our $19/mo sits under it.

### Playwright's own agents — free, from Microsoft (MEASURED, `playwright.dev/docs/test-agents`)

**Planner** ("explores the app and produces a Markdown test plan"), **Generator** (turns the plan
into Playwright Test files), **Healer** ("executes the test suite and automatically repairs failing
tests"). Install, verbatim: **`npx playwright init-agents --loop=vscode`**, with `--loop=claude`,
`--loop=codex`, `--loop=opencode`. **No pricing statement anywhere, because there is no price.**

This confirms and sharpens SCORECARD row 11: **authoring, healing and planning are all $0 from the
platform vendor, and they ship a Claude Code loop by name.** Any comparison page we write that leads
on "it writes your tests" is competing with a free first-party tool. INFERRED: the only rows that
survive Microsoft are the ones about *the verdict* — flake, false greens, evidence, exit codes,
run history — which is where our shipped work already is.

### Midscene (MEASURED, `github.com/web-infra-dev/midscene`)

**14.7k stars, 1.1k forks, 38 open issues, MIT, 2,238 commits.** Description: *"GUI Agent for E2E
Testing · AI-powered vision. Cross-platform. Batteries included."* Their pitch attacks our exact
architecture, verbatim: *"Most UI automation — including AI tools that read the DOM — depends on page
structure. That structure is fragile and incomplete."* They work **from the screenshot alone**.

This is the strongest published argument against the a11y-tree approach SCORECARD row 1 says we win.
It deserves a straight answer rather than a dismissal, and the honest one is not "they're wrong": it
is that a screenshot cannot tell you *which* element it clicked, so a wrong click blames the wrong
feature — and, since 2026-08-30, `lib/render.mjs` gives us the one thing vision genuinely had over
us (catching a page that is visually broken) without paying per-step image tokens.

**14.7k stars against Autonoma's 190** (MEASURED both). If star count is the social proof anyone
checks, the open-source leader in this space is not Autonoma.

### Stagehand / Browserbase (MEASURED, `github.com/browserbase/stagehand`, `browserbase.com/pricing`)

**24.1k stars, 1.7k forks, 98 open issues, MIT.** *"Playwright was built for testing, Stagehand is
built for agents."* — they explicitly point testing *away* from themselves. Browserbase pricing is
fully public: **Free $0** (3 concurrent browsers, **1 browser hour**, 3 agent runs, 15-min sessions)
· **Developer $20/mo** · **Startup $99/mo** ("MOST POPULAR") · Scale custom.

INFERRED: not a competitor, a **supplier and a price anchor**. Their $20 developer tier is another
number sitting next to our $19. And their disclaimer is a gift — the biggest browser-agent project
in the world says it is not a testing tool.

### Octomind — the graveyard entry, and today's evidence

**`www.octomind.dev` does not resolve — `getaddrinfo ENOTFOUND`** (MEASURED today). Not a 404, not a
parked page: **the DNS is gone.** Third-party trackers report the service was *"discontinued in May
2026 and is not available for new customers"* (REPORTED, bug0/stackpick/toolradar knowledge bases;
no primary announcement survives to confirm — the primary source is unreachable, which is itself the
finding). Their positioning while alive — auto-generate, maintain and run Playwright tests, free
open-source tier — is preserved in `GRAVEYARD_AND_BUYER.md`.

INFERRED, and worth saying out loud in any comparison we publish: **an AI-testing vendor's domain
went dark inside four months.** That is the strongest possible argument for our own structural
promise (tests are plain files in your repo, the runner executes on your CI) and it costs us nothing
to make, because we are pointing at a fact rather than at a competitor.

---

# PART 6 — WHAT *WE* LOOK LIKE IN THE SAME MIRROR

Fetched `https://smolanalytics.com/` today, and read as a stranger would (MEASURED). Included
because a comparison document that never turns the camera around is a sales deck.

- Hero: **"Nobody writes the tests."** / *"End-to-end tests are the ones everybody agrees they should
  have and nobody keeps… So write a sentence instead."*
- Hero visual: **a static terminal command display.** Nothing moves.
- CTA: **"Start trial" / "Start the 14-day trial"**, secondary "Log in".
- Pricing, in public, with numbers: **14-day trial at Pro limits, no card → $19/mo → 100 tested PRs
  + 2M events included → $0.10/PR, $6/M events overage.**
- **No logos. No testimonials. No numbers from a customer. No comparison page of any kind.**
- Sections: the whole setup, no account · one sentence now → an agent using your app on every pull
  request · the second half, which no other testing tool has · check your own site · why this is not
  another test framework · say it, don't click it · what it costs · what's live today · not in the
  box · who built this.

Read against the field, three things are true at once (INFERRED):

1. **We are already in the top tier on the axis that decides whether an engineer evaluates at all** —
   a published price with a real number, a trial with no card, and a `npx` path with no account. Only
   Momentic, Checkly and Browserbase are as open. Mabl, Testim, Rainforest, Reflect and Meticulous
   are not.
2. **Two sections are doing something nobody else does, and one of them is a liability.**
   *"not in the box"* is an honesty section — the field has no equivalent and it is a real
   differentiator with this buyer. *"the second half, which no other testing tool has"* is the
   instrumentation half; SCORECARD calls it our least copyable row, and it is, but on a page whose
   first noun is testing it reads as a second product before the first one has been believed.
3. **We are the only page in this research with no proof of any kind.** Every rival has at least one
   checkable external artefact — a G2 badge (QA Wolf), a star count (Midscene 14.7k, Stagehand 24.1k,
   Autonoma 190), a named CEO quote (Autonoma/Rauch), a logo wall (Meticulous, Momentic), a numbered
   case study (Momentic ×5). We have zero. **`npmjs.com/package/smolanalytics` returned HTTP 403 to
   me today, so even our own download count is UNVERIFIED here.**

We also have proof material sitting unused: `839 tests / 839 pass` measured today, and the
`pool.mjs` numbers (`39.2s → 4.9s at 8 workers, 917MB peak`). Those are numbers about our own
software rather than a customer's, which is weaker than a case study — and stronger than nothing,
which is what is on the page.

---

# PART 7 — THE INDUSTRY'S IMPLICIT CHECKLIST

Seven comparison pages read in full today (MEASURED, every row transcribed above):
`autonoma-vs-momentic`, `autonoma-vs-qa-wolf`, `autonoma/ai-testing-platform-comparison`,
`autonoma/opensource-alternative-momentic`, `momentic-vs-qa-wolf`, `momentic-vs-mabl`,
`qawolf/qa-wolf-alternatives-mabl`.

Row-theme frequency, counted across those seven:

| Rank | Row theme | Appears on | How it is worded |
|---|---|---|---|
| **1** | **Who authors the tests** | **7 / 7** | "Test generation source" · "Test creation model" · "Who writes tests" · "Authoring" · "Test Generation Input" · "Generates from codebase" · full-code vs low-code |
| **1=** | **Who maintains them / self-healing** | **7 / 7** | "Self-healing tests" · "Test maintenance model" · "Maintenance" · "Re-derives on UI change" · "AI-native maintenance" · "Human-in-the-loop test maintenance" |
| **3** | **Pricing model, and whether a number is published at all** | **6 / 7** | "Pricing model" · "Starting Price" · "Cloud Price" · "Free tier available" · "Cost structure" · plus dedicated cost-scaling tables |
| **4** | **Best fit / who it is for** | **5 / 7** | "Best fit" · "Best for" · "Who They're Made For" |
| **5** | **Throughput — parallel execution and run limits** | **4 / 7** | "Parallel execution" · "Parallelization" · "Run limits" · "Test execution time" · "Handling 50 PRs in a week" |
| **5=** | **Coverage breadth — test types, browsers, platforms** | **4 / 7** | "Test types" · "Browser coverage" · "Coverage Approach" · QA Wolf's 22-row capability matrix |

Ranks 5 and 5= are a genuine tie at 4/7 and I am not going to break it artificially. **Take
throughput**, because we shipped it six days ago and have measured numbers (`39.2s → 4.9s`), and
because coverage breadth is a race against Appium and device farms that `SCORECARD.md` row 10
already says we should refuse.

### What the row choice reveals, and what is missing

Every one of the top four rows is about **labour** — who does the typing, who does the fixing, what
it costs, and which team it suits. INFERRED: the entire category has agreed it is selling *the
removal of a person's work*.

Now the absences, counted the same way:

| Row theme | Appears on |
|---|---|
| Flake / false positives / "is the verdict correct" | **1 / 7** — and only obliquely, inside QA Wolf's maintenance prose (*"its AI may auto heal incorrectly"*) and their "Suite Health & Reliability" table |
| Evidence when a test fails — can you tell *why* | **0 / 7** |
| False greens — does a pass mean the page rendered | **0 / 7** |
| Exit-code / CI contract — what reddens a build | **0 / 7** |
| Whether the tests still run if the vendor disappears | **2 / 7** — only Autonoma's escape-intent page (Vendor Lock-In, Data Sovereignty, Self-Hosting) and Momentic's one self-damning row |

**This is the most important finding in the document, and it cuts both ways. State both.**

*The opportunity:* `GRAVEYARD_AND_BUYER.md` and `RED_TEAM.md` (line 195) both concluded that the
axes this buyer **churns over** are flake honesty, verdict discipline, evidence and CI ergonomics —
32% of negative reviews field-wide are "the tool itself is flaky". So the category **sells on
authorship and loses customers on trust.** Every rival's comparison table is silent on the thing
their own churn is made of. A comparison page that adds those rows is not a differentiation
exercise; it is naming the buyer's actual injury in a room where nobody else will.

*The risk, stated honestly:* `RED_TEAM.md` line 167 already carries the counter — *"nobody buys
per-test"* — and the same logic applies here. Seven vendors independently declining to compete on
flake is evidence that **flake is a churn axis, not a purchase axis**. People buy on "it writes the
tests" and quit on "it lies to me." If that is right, the rows are still worth adding, but as the
*second* screen, not the headline. The headline still has to answer row 1 and row 2.

**The synthesis, and my actual recommendation (INFERRED):** lead with the labour rows because that is
where attention is, then win on the trust rows because that is where the incumbent is weak and
where our code already is. Concretely, a `smolanalytics vs X` page whose rows read:
*Who writes the tests · Who maintains them · What a failure tells you · What a pass guarantees ·
What happens on a retry · What it costs · What you keep if we vanish.* The first two match the
industry checklist so the page is legible to someone comparing. The next three are rows nobody else
can fill in without losing them: `render.mjs` is 685 lines of "what a pass guarantees", the `flaky`
verdict is "what happens on a retry" against Checkly's billed retries, and `--share` plus plain-file
recordings are "what you keep."

### Three tactical notes from the same seven pages

1. **Autonoma runs two different row sets for two different intents.** `X-vs-Y` pages get labour
   rows. `opensource-alternative-to-X` pages get escape rows: **Open Source · Self-Hosting · AI
   Transparency · Vendor Lock-In · Data Sovereignty · Compliance (HIPAA, SOC 2) · Vendor Risk ·
   Self-Hosted Cost** (MEASURED, verbatim). Same competitor, entirely different argument, matched to
   what the searcher typed. That is the single most copyable mechanic in this research.
2. **Their public prices do not agree with each other.** Homepage: free 100K credits, then
   **$100 per 150K credits**. The `opensource-alternative-momentic` page: cloud at **$499/month for
   1M credits, unlimited parallels**, plus a **"75–87% cost reduction"** three-year savings claim
   against Momentic's *"~$500/month"* (MEASURED, both pages, same day). Meanwhile
   `getautonoma.com/pricing` **404s**. INFERRED: the comparison cluster is written ahead of the
   product's own pricing page. A buyer who opens two tabs sees two prices.
3. **Autonoma's own escape page prints the contradiction.** It calls Autonoma "open-source"
   throughout and then states *"Licensed under BSL 1.1 (converts to Apache 2.0 in 2028)"* on the same
   page (MEASURED, verbatim). We do not need to argue this one; we can quote it.

---

# PART 8 — THE THREE DEMO MOMENTS THAT ACTUALLY LAND

Across every hero, demo and doc page fetched today, exactly three formats produced a moment where a
sceptical engineer would believe something new. Ranked.

### 1. An agent operating an app *live*, unedited, above the fold — Autonoma

The only live interactive sandbox in the field: the agent works a login form and a dashboard with
its actions printing in real time (MEASURED). **The oh moment is that nobody told it the selectors**,
and it arrives before the visitor clicks anything.

Why it lands: the buyer's first objection is never "does the concept work", it is **"it won't work on
*my* app."** A recorded video answers a different question — it proves the demo app works. A live
sandbox does not remove the objection either, but it removes every excuse about editing, and it is
the only format that cannot be accused of being a highlight reel.

Contrast with the same page's actual onboarding — GitHub App across repos, a Dockerfile pushed into
your code, API keys handed over, ETA 1h13m, failed at step 1 of 7 (REPORTED from
`AUTONOMA_IN_MOTION.md`). **Their demo is seconds and their product is an hour.** The transplant for
us is not the sandbox; it is the realisation that the demo and the first run should be the same
event. Ours can be, because `npx smolanalytics test --url … --test "…"` needs no account.

### 2. The verdict rendered *in the place the work already happens* — Autonoma's README, Meticulous's "Review"

Autonoma's README leads with a screenshot of a **PR review comment carrying a verdict banner, bug
counts and coverage**, then an issue report with **failure screenshot, expected vs actual, and the
relevant code** (MEASURED). Meticulous's how-it-works ends on **a PR comment showing behavioural,
logical and visual diffs** (MEASURED).

Why it lands: it shows the **output artefact** rather than the process. A GIF of a tool working asks
you to imagine the payoff; a PR comment *is* the payoff, in a UI the engineer already reads twelve
times a day. This is also the moment that answers "what do I actually get on Tuesday."

We have this artefact and do not show it. `suite.mjs` posts **one** PR comment, edited in place via
an idempotency marker; the shipped `templates/github-action.yml` writes a step summary and uploads
evidence. **The single highest-leverage change to smolanalytics.com is putting a real screenshot of
that comment above the fold**, replacing a static terminal string that shows the input rather than
the output.

### 3. One line you can read, understand and paste — Meticulous, Playwright, Momentic

Three different companies converged on the same move, and it works for the same reason each time:

- Meticulous's section heading is literally **"Get started by adding the meticulous recorder script
  tag"** — the install *is* the demo (MEASURED).
- Playwright: **`npx playwright init-agents --loop=claude`** (MEASURED, verbatim).
- Momentic: **`npx --yes @momentic/wizard@latest -y --platform web --editor-tools skills`**
  (MEASURED, verbatim).

Why it lands: it collapses the cost of finding out to roughly zero, and — the underrated half — **a
short command is itself a claim about the architecture.** A one-line install says "there is no
platform to adopt." Momentic's is the longest of the three and reads it; Playwright's is the
shortest and wins.

Ours today is `npx smolanalytics test --url … --test "…"` and it is competitive with all three.

### Runner-up, noted because it is the strongest single proof point in the field

QA Wolf's testimonial: **"The tests run in 11 minutes. There's about 300 and we rarely get a false
negative."** (MEASURED). Three checkable numbers, and the only sentence anywhere in this research
that makes a claim about **being wrong**. It is not a demo moment because it is buried under a GIF of
a running wolf — which is exactly why it is worth taking. Nobody in this category is competing for
that sentence.

---

# APPENDIX — SOURCES, ALL FETCHED 2026-08-30

| URL | What it gave |
|---|---|
| `https://www.getautonoma.com/` | hero, live sandbox, CTAs, logos, Rauch quote, on-page pricing |
| `https://www.getautonoma.com/pricing` | **HTTP 404** |
| `https://github.com/autonoma-ai/autonoma` | 190★, 48 forks, 3 watchers, 1 open issue, 1,832 commits, BUSL-1.1 |
| `https://getautonoma.com/blog/autonoma-vs-momentic` | 16 feature rows + 7 pricing rows |
| `https://getautonoma.com/blog/autonoma-vs-qa-wolf` | 13 rows + speed table + cost-scaling table |
| `https://getautonoma.com/blog/ai-testing-platform-comparison` | 5 rows × 11 vendors |
| `https://getautonoma.com/blog/opensource-alternative-momentic` | 16 escape-intent rows, $499/mo, BSL admission |
| `https://www.qawolf.com/` | "Agentic QA that just works", wolf GIF, G2 4.8/100+, testimonial numbers |
| `https://www.qawolf.com/pricing` | 1¢/AI credit, 15¢/runner minute, Platform vs Coverage-as-a-Service |
| `https://www.qawolf.com/blog/qa-wolf-alternatives-mabl` | 5 tables incl. a 22-row capability matrix |
| `https://momentic.ai/` | hero, 8 logos, 5 numbered case studies, changelog section |
| `https://momentic.ai/pricing` | Free 2,000 credits/mo · $125/mo · Enterprise; 1 credit = 1 step |
| `https://momentic.ai/docs/` | `npx @momentic/wizard`, API key, YAML tests in repo; **no MCP mention** |
| `https://momentic.ai/comparison/momentic-vs-qa-wolf` | 7 rows incl. "Ownership of test code: N/A" |
| `https://momentic.ai/comparison/momentic-vs-mabl` | 7 rows; $125/mo confirmed |
| `https://www.mabl.com/pricing` | **no prices**; 500 credits/mo; "Request a Quote" |
| `https://www.testim.io/pricing/` | tier names only, no figures; Tricentis branding |
| `https://www.rainforestqa.com/pricing` | **no prices**; sales-gated trial; region-blocked notice |
| `https://reflect.run/pricing` | no figures; credits 1/5/0.1; SmartBear 2026 copyright |
| `https://www.meticulous.ai/` | 15 logos, Dropbox + Notion quotes, static screenshot hero, no pricing |
| `https://www.meticulous.ai/how-it-works` | script-tag install, mocked backends, "up to 10,000 browsers in parallel" |
| `https://www.checklyhq.com/pricing/` | $0 / $24 / $64 / custom; overages to the cent; **"Each retry counts as a check run."** |
| `https://playwright.dev/docs/test-agents` | Planner/Generator/Healer, `npx playwright init-agents --loop=claude`, free |
| `https://github.com/web-infra-dev/midscene` | 14.7k★, MIT, "works from the screenshot alone" |
| `https://github.com/browserbase/stagehand` | 24.1k★, MIT, "Playwright was built for testing, Stagehand is built for agents" |
| `https://www.browserbase.com/pricing` | $0 / $20 / $99 / custom, browser-hours meter |
| `https://www.octomind.dev/` | **DNS does not resolve — `getaddrinfo ENOTFOUND`** |
| `https://smolanalytics.com/` | our own page, read as a stranger |
| `https://www.g2.com/products/qa-wolf/reviews` | **HTTP 403** — QA Wolf's badge could not be independently confirmed |
| `https://www.npmjs.com/package/smolanalytics` | **HTTP 403** — our own download count unverified |
| `https://web.archive.org/…/octomind.dev/pricing` | fetch blocked in this environment |

STATUS: complete.
