# THE CHECKLIST — what a buyer actually puts in the spreadsheet

Agentic E2E testing, 2026. Reconstructed from primary sources: vendor comparison pages, review-site
feature grids, RFP-style "how to choose" posts, HN and Reddit evaluation threads, and public
bake-offs. Written 2026-08-30.

**Companion to** `AUTONOMA_TEARDOWN.md`, `AUTONOMA_IN_MOTION.md`, `FIELD_AGENT_FIRST.md`,
`FIELD_INCUMBENTS_AND_FREE.md`, `GRAVEYARD_AND_BUYER.md`, `SCORECARD.md`, `RED_TEAM.md`,
`CROSS_POLLINATION.md`, `WHITESPACE_DB_AND_AI.md`. Nothing already established there is
re-derived; where this file contradicts one of them it says so out loud in §6.

**Evidence labels.** MEASURED = I fetched the page or ran the code myself today. CLAIMED = the
vendor's own marketing. REPORTED = a third party asserts it. INFERRED = my read of the evidence.
An unverifiable claim is marked *unverified* and stays that way.

---

## 0. Ground truth on our side, re-established before any scoring

`SCORECARD.md` is stale — it was written 2026-08-24 against a 290-test codebase and five known
holes. Nine features have shipped since. Every one of them was verified against the code in
`/Users/arjun/smolanalytics/cli` today before it was allowed into a row below.

**Suite run, MEASURED 2026-08-30:** `cd ~/smolanalytics/cli && npm test` →
**839 tests, 839 pass, 0 fail, 137 suites, 151.8s.** (Per `RED_TEAM.md` §3 this number is *not*
the proof it looks like — quoted here only as a floor, with the per-feature verification below
doing the actual work.)

| Claimed as shipped | Verified how (MEASURED 2026-08-30) | Verdict on the claim |
|---|---|---|
| Parallel execution, 50 tests 39s → 4.6s at 8 workers | `lib/pool.mjs` (405 lines) exists; `--workers <n>` parsed in `bin/smolanalytics.mjs:70` via `parseWorkers`; the measurement table is in the file header | **True, with the number corrected.** The header's own measured table says **39.6s serial**, and **4.9s at 8 workers** with one browser + a context per worker (9.6s at 8 workers when each test launched its own Chromium). `README.md:95-96` says "39.2s at `--workers 1`, 5.4s at the default". So the honest range is **39s → 4.9–5.4s**, not 4.6s. Use 39s → ~5s, or quote the file. Peak RSS 917MB shared vs 2233MB separate. |
| Cross-browser chromium/firefox/webkit, all three verified launching | `lib/engines.mjs` (172 lines), `--browser <name>` at `bin:62`, `test/engines.test.mjs` asserts real launches and that a typo is refused rather than silently falling back to chromium | **True**, and the interesting part is the honesty rule: a recording made on one engine and replayed on another *says so* rather than re-running the agent (ruinous) or staying silent (dishonest). |
| File uploads with generated fixtures validated by magic bytes | `lib/upload.mjs` (502) + `lib/uploadsafe.mjs` (121); `test/upload.test.mjs:194-206` asserts the PNG signature `[0x89,0x50,0x4e,0x47,0x0d,0x0a,0x1a,0x0a]` and `%PDF-1.` header | **True.** Fixture is fabricated from the input's `accept` attribute, deterministically, so nothing is committed to the repo and the step stays replayable. |
| `--seed` hook | `lib/seed.mjs` (366) + `lib/seedguard.mjs` (145); `--seed <url>` at `bin:66`; secret via `SMOLANALYTICS_SEED_SECRET` header | **True.** Closes `SCORECARD.md` behind-list #3 (seeding depth) in the inverted shape that doc recommended — their endpoint, their app, flat JSON becomes placeholders. |
| False-green render guard | `lib/render.mjs` (685); `--no-render-check` at `bin:77`; verdict rule in the file header: a would-be PASS + catastrophic render → `failed`; a `failed` is never softened; `stale`/`errored`/`flaky` are never checked at all | **True, and the false-positive claim is exactly right.** `test/render.test.mjs` holds **exactly 10 healthy-but-odd fixtures** (ordinary page, dark theme, canvas game with no DOM text, image-only gallery, SVG infographic, near-white print view, content mid-fade, full-viewport cookie banner, late-painting SPA, one-line status page), each asserted to produce **zero findings**. All pass in today's run. |
| Authenticated runs, one login per suite, credentials never on disk | `lib/auth.mjs` (549); `--login`, `--auth-file`, `--auth-dir` at `bin:74-76`; creds from `SMOLANALYTICS_LOGIN_EMAIL`/`_PASSWORD` env only | **True**, and it handles the hard half: session expiry mid-suite is detected, repaired with exactly one fresh login, and a failed *login* is `errored` (our side) not `failed` (their app). `lib/pool.mjs` serialises the first test when no session exists so 8 workers don't produce 8 logins. |
| Token metering with `--max-calls` | `lib/cost.mjs` (145), `parseMaxCalls` imported in `bin` | **True.** Tokens always reported from the API's own `usage` block (never estimated); dollars only when `SMOLANALYTICS_PRICE_IN`/`_OUT` are supplied; the cap is on **calls not dollars** and hitting it exits **2**, never a verdict about the app. |
| `--share`, a public run page, no account | `lib/share.mjs` (937); `--share` at `bin:82`; `share.mjs:833` states the anonymous path explicitly; `test/share-secrets.test.mjs` exists alongside `share.test.mjs` | **True.** Opt-in only, cannot change a verdict or exit code, and ~25 redaction rules run over the bundle before it leaves the machine. |
| `--since` diff-aware selection | `lib/select.mjs` (405); `--since <ref>` at `bin:80`; refused without `--suite` | **True**, and built on the right asymmetry: selection removes a test only on positive evidence it is unrelated. No recording, unreadable recording, no git, no merge base, empty diff, or an internal throw → **run everything, and say why.** |

Two of the five `SCORECARD.md` behind-rows are now closed (seeding depth #3 by `--seed`; the
false-green half of #1 by `lib/render.mjs`), plus four of the six `RED_TEAM.md` §4 blind spots
(suite wall-clock, authenticated flows, file uploads, cross-browser). Still open: **mobile**
(nothing), **AI-app / semantic assertions** (nothing — the exact-text proof still breaks on
LLM-output pages), **scenario-vs-app causality inside `failed`**, **iframes/shadow DOM**
(`lib/frames.mjs`, 383 lines, exists — needs its own verification pass, not done here).

---

## 1. Method, and what refused to be fetched

**What worked (all MEASURED 2026-08-30):** vendor pricing pages fetched directly; HTTP status and
redirect chains checked with raw `curl` (a redirect is evidence in this category — see §4);
vendor comparison pages; Capterra's category filter list; HN's Algolia API; the Playwright docs.

**What refused.** `reddit.com` and `old.reddit.com` return **HTTP 403** to both HTML and `.json`,
with a browser User-Agent, and WebFetch is blocked from `old.reddit.com` entirely (MEASURED).
`g2.com/categories/ai-testing` returns **403** to WebFetch (MEASURED) — the same block
`GRAVEYARD_AND_BUYER.md` recorded in its method note, so review-site evidence again arrives only
through search-indexed snippets and third-party compilations that quote it, and is labelled
REPORTED throughout. Capterra *did* answer (§3.4), which is the one review-site primary source in
this file.

**Search-engine noise warning.** The query "how to choose an AI E2E testing tool" returns almost
entirely vendor-authored SEO listicles — `virtuosoqa.com`, `getautonoma.com`, `shiplight.ai`,
`baserock.ai`, `confident-ai.com`, `testmuai.com` all rank on it (MEASURED search 2026-08-30).
This matters more than it looks: **the "neutral buyer's guide" genre in this category is written by
sellers.** Every criterion list in §3 that comes from such a page is labelled with who owns the
domain, because a vendor's comparison table is evidence of *what the vendor believes buyers ask* —
which is genuinely useful — and is not evidence that buyers ask it.

---

## 2. The price row, measured — because it is the first cell anyone fills

Every URL below was requested with `curl` and, where a page existed, read. All MEASURED 2026-08-30.
This table is here rather than in the checklist because it is the row that *terminates* evaluations
(§4), and because the field moved since our last pass.

| Vendor | `/pricing` HTTP | Price actually shown | Meter | Free path |
|---|---|---|---|---|
| **Autonoma** | **404** — but the price is on the homepage at `#Pricing` | **"$0 to start… 100K credits free. Then pay only for what you run — $100 per 150K credits, with optional auto top-up. No minimum."** Plus **"Self-hosted / Free, forever / Run on your own infrastructure. No limits, no usage costs."** | credits | "No credit card required to start" |
| **Momentic** | 200 | **"$0 / forever"** (2,000 credits/mo ≈ 200 runs); **"$125 / month + additional usage"** (10,000 credits ≈ 1,000 runs); Enterprise "Custom" | **credits, and "Every test step uses one credit, including steps that AI features generate and run (AI actions, failure recovery, auto-heal)"**; overage **$0.01875/credit** | yes, hard stop, no card |
| **Checkly** | 200 | Hobby **$0**, Starter **$24/mo**, Team **$64/mo**, Enterprise custom | check runs, browser runs — **"Each retry counts as a check run"** | yes, no card |
| **Ranger** | 200 | Free forever (**5 reviews**), Growth **$50/month/user**, Enterprise "Usage Based" → Contact Sales | **per seat** on the paid tier | yes |
| **QA Wolf** | 200 | Platform (self-serve): **"1¢ /AI credit"**, **"15¢ /runner minute"**. Coverage as a Service: **"custom-priced based on the number of tests under management"** | credits + runner minutes / tests under management | "Try for free" |
| **Bug0** | 200 | **"$2,500/mo flat"**, up to 500 user flows, "Month-to-month. No annual contracts." | user flows | none; "Discounted 60-day pilot" |
| **mabl** | 200 | **none** — "Request a Quote", "BOOK A DEMO" | credits (500/mo cloud runs mentioned) | not stated |
| **qa.tech** | 200 | **none on any of three tiers** — every CTA is "Talk to us" | parallel test runs | "a free POC" |
| **Rainforest QA** | **301 → `/talk-to-sales`** | **none** | — | — |
| **testRigor** | **404** on `/pricing`, `/pricing/`, `/plans/` | **no pricing page exists** | — | — |
| **Meticulous** | **404** | none | — | — |
| **Spur** | **404** | none | — | — |
| **Octomind** | **000 (DNS dead)** | — | — | — |
| **smolanalytics (us)** | 200 | **"$19 /mo"**, "100 tested pull requests a month, then 10c each"; "No seats, no per-site fee, no Team or Enterprise tier, no contact-sales" | **tested pull requests** | 14 days at Pro limits, no card |

**Three things this table says that the older files in this directory do not.**

1. **Autonoma published a price.** `SCORECARD.md` row 14 and `RED_TEAM.md` §5 both rest on
   "their loop is free because it is funded — `RUN_CONSUMPTION` is never written, enforcement off
   fleet-wide." The homepage now reads **"$100 per 150K credits"** — which is **$0.000667/credit,
   exactly the rate hard-coded in the billing code the teardown read.** The go/no-go their own code
   comment was waiting on appears to have happened. At their code's rate card that is
   **$0.0067 per web run, $0.333 per generated test, $0.133 per iOS run.** We can no longer say
   "their price is zero"; we must say "their price is usage-based and ours is not." (See §6.1.)
2. **QA Wolf now has a self-serve tier with unit prices on the page.** `GRAVEYARD_AND_BUYER.md` §1
   and qaskills.sh (REPORTED, 2026-07-07: *"Public figures are deliberately scarce and negotiated
   per contract"*) both say QA Wolf publishes no pricing. As of today the **platform** half does:
   1¢/AI credit and 15¢/runner minute. The **service** half is still "Book a demo".
3. **mabl's $450/mo is gone.** The number quoted in the churn tally (REPORTED, via drizz.dev) has no
   published successor — mabl is now quote-only. A vendor moving *from* a published price *to*
   "Request a Quote" is the market moving up-market, which is the vacancy the checklist below sits in.

---

## 3. THE CHECKLIST

Each row: **why it is on the list**, **who it matters to**, **how often it appears** across the nine
criterion-bearing sources catalogued in §3.1, and the honest verdict — **REAL** (it changes
behaviour after purchase) or **CHECKBOX** (it gets a tick in the spreadsheet and is never used
again). A row can be REAL for one buyer and CHECKBOX for another; where that happens it says so.

### 3.1 The nine sources the frequency count is taken from

| # | Source | Who owns it | Date | What it is |
|---|---|---|---|---|
| A | `getautonoma.com/blog/e2e-testing-tools` | **vendor (Autonoma)** | Mar 2026 | 11-row comparison table vs Playwright/Cypress (MEASURED) |
| B | `getautonoma.com/blog/ai-testing-platform-comparison` | **vendor (Autonoma)** | May 2026 | 8-criterion framework across 11 vendors (MEASURED) |
| C | `shiplight.ai/blog/best-ai-e2e-testing-platforms-complex-user-flows` | **vendor (Shiplight)** | 2026-08-10 | 6 criteria, 8-vendor grid (MEASURED) |
| D | `bug0.com/knowledge-base/momentic-review` | **competitor (Bug0)** | 2026-07-31 | 5 review dimensions + 6 named limitations (MEASURED) |
| E | `qaskills.sh/blog/qa-wolf-ai-testing-guide-2026` | independent (The Testing Academy) | 2026-07-07 | 5 criteria + 4 objections (MEASURED) |
| F | `virtuosoqa.com/post/best-end-to-end-testing-tools` | **vendor (Virtuoso)** | 2026-05-10 | 7 selection criteria, per-vendor profiles (MEASURED) |
| G | `capterra.com/automated-testing-software/` | review site | fetched 2026-08-30 | the 24 feature filters buyers can tick (MEASURED) |
| H | HN objection corpus | practitioners | 2024–2026 | 6 launch threads read in full, in `GRAVEYARD_AND_BUYER.md` §3 (MEASURED there) |
| I | Review churn tally | practitioners | — | 34 dislike items across 8 vendors, `GRAVEYARD_AND_BUYER.md` §2 (REPORTED there) |

**Read the ownership column before the frequency column.** Six of the nine are written by somebody
selling into the category. That is not a reason to discard them — a vendor comparison table is the
best available record of *which questions vendors keep having to answer*, which is a real signal —
but it does mean a criterion appearing in A, B and F is one criterion appearing three times in the
marketing department's opinion, not three independent buyers.

### 3.2 The rows — ordered by how much they actually decide, not by how often they are asked

**Frequency** = how many of the nine sources in §3.1 carry the row (A–I noted).

---

#### TIER 1 — the rows that decide, for an engineer buying with a card

| # | Row | Why it is on the list | Who it matters to | Freq | REAL or CHECKBOX — and the evidence |
|---|---|---|---|---|---|
| 1 | **Time from `npx` to first verdict** | This is the whole evaluation for a self-serve buyer. If the tool does not produce one true statement about their app inside the first session, there is no second session. | Solo builders and 5–50 eng leads, absolutely. Enterprise: irrelevant, they have an onboarding call. | 3/9 (A "Setup time", B "Managed preview env per PR", E "coverage… within a few months") | **REAL, and the most under-asked row on this list.** It rarely appears in vendor grids because the vendors that lose it write the grids. Evidence it decides: `RED_TEAM.md`/`AUTONOMA_TEARDOWN.md` timed a real Autonoma onboarding at **"ETA ~1h 13m" → 28 min in at 3% → 0% → step 1 of 7 failed** (MEASURED there). Preflight's HN thread: *"without a pricing page I just move along"* — the same impatience, one field over. |
| 2 | **False reds in the trial** | The category's #1 cause of death. A red that is not a bug teaches the team to ignore red, and a tool whose red is ignored has already been cancelled — it just hasn't been invoiced yet. | Everyone. The only universal row. | 4/9 explicit (B "Filters false positives before they reach the team", C "is AI behavior at run time itself a flake source", H, I) — **and it is item A, the largest group, in the 34-item churn tally** | **REAL, and it is the row.** Google's own numbers (MEASURED, testing.googleblog.com 2016-05, via `GRAVEYARD_AND_BUYER.md`): *"1.5% of all test runs report a flaky result… almost 16% of tests have some level of flakiness… about 84% of the transitions we observe from pass to fail involve a flaky test."* If Google cannot hold that line internally, a $19–$450/mo SaaS adding LLM entropy is on notice. Note what this row is NOT: it is not "do you have retries". It is *what verdict does a fail-then-pass produce* — see row 3. |
| 3 | **What a fail-then-pass is called** | The sharp edge of row 2, and the one question no vendor grid asks. Retry-until-green is how a tool launders flake into a pass; a tool that calls it `passed` is lying to the buyer in the exact place they cannot check. | Engineers who have been burned. Increasingly everyone. | 0/9 — **nobody asks this yet** | **REAL and unasked** — which makes it the best available wedge question for a buyer to add to their own sheet. Ours: pass-on-retry is **`flaky`, its own status, never a pass**, and the reason names both the failure and the pass (`lib/test.mjs`, `flake.test.mjs`, MEASURED today). Autonoma: **zero files match "flake"** in the repo while the homepage claims *"no flaky failures from minor layout changes"* (MEASURED, teardown). Checkly bills each retry as a check run — a meter that profits from flake. |
| 4 | **Where the tests live, and what happens on cancellation** | Buyers in this category have been burned by vendor death, not just vendor lock-in. Octomind's paying customers lost the product **and the artefacts** in May 2026. | Engineers at every size; the only row where a solo builder and a CTO ask identically. | 3/9 (C "do the tests live in your repo or a vendor cloud", D "No code export… vendor lock-in by design", H — export-to-Playwright demanded in *every* HN launch thread read) | **REAL.** The structural proof is not a review, it is a business decision: **Cypress blocked deploysentinel.com** to force flake dashboards into Cypress Cloud (HN 37842778, MEASURED in prior research). If you build on someone else's data plane, the plane's owner can end you. The buyer's version of the question is one sentence: *"if I cancel today, does anything still run tomorrow?"* |
| 5 | **What the meter counts** | Not the price — the *unit*. A meter attached to thoroughness (steps, runs, test cases, retries) makes the tool more expensive exactly as the customer uses it correctly, and the customer's rational response is to test less. | Anyone paying out of an engineering budget with no procurement to hide the growth in. | 3/9 in criteria lists (D "Pricing transparency", F "TCO", I price group) but present in every pricing page in §2 | **REAL, and mis-asked.** Buyers write "price" in the cell and should write "unit". Measured units in the field today: Momentic **per step, "including steps that AI features generate and run"** — you pay for the tool's own healing; Checkly **per check run, retries included**; Ranger **per seat**; QA Wolf **per runner minute + per AI credit**; Bug0 **per user flow**; Autonoma **per credit**; ours **per tested pull request** (a PR pushed to five times is one unit; terminal and cron runs are never counted). Octomind died metering *authoring* at ~$4.45/generated test — a step Microsoft now gives away (see row 18). |
| 6 | **Authenticated flows: login, session reuse, expiry** | The tests worth writing are behind a session. A tool that only reaches public pages tests the marketing site. | Everyone with a product behind a login, i.e. everyone. | 2/9 explicit (C "UI + a real email inbox + auth + multi-tab + multi-tenant", H — MFA/OTP/email-confirm recurs in every launch thread) | **REAL, and the question has a second half nobody asks: what happens when the session expires mid-suite.** The naive answer turns a whole green suite red with a bug report about a working app. Ours: one login per suite, session reused, a bounce to the sign-in page is **detected, repaired with exactly one fresh login, and re-run**; a failed *login* is `errored` (our side) never `failed` (their app); credentials come from env only and are never written to disk (`lib/auth.mjs`, MEASURED today). |
| 7 | **Test data and DB state — can it *provide* state, not just clean up after itself** | "A logged-in user with three past orders can request a refund" is the shape of every test that matters, and it is unwritable against a fresh app. This is the silent coverage wall: the suite goes green over the flows that don't matter. | Everyone past a toy app. | **5/9 — the highest-frequency substantive row** (A "DB state handling", B "Manages test data and database state", C "state and multi-step durability", H — Propolis' founder: state is *"one of our biggest challenges"*, I) | **REAL.** And note the failure mode is on the *vendor* side too: Autonoma's own README says **~48% of classifications have no recording and 97% of those executed no steps** — runs dying before the app is reached, largely in the environment/data layer (MEASURED, teardown). Ours since the last pass: `--seed <url>` POSTs the run identity to **an endpoint the customer wrote**, their app fabricates the state, the flat JSON it returns becomes placeholders (`lib/seed.mjs`, MEASURED today). Their app already knows how to build their data; ours never will. |
| 8 | **Per-PR gate: does it run on every PR, post once, and exit correctly** | The tool has to live where the decision is made. A dashboard somebody visits is a dashboard nobody visits. | Everyone shipping through PRs. | **5/9** (A "CI integration", B "Runs and reports per PR", C "CI integration & determinism… does it gate PRs reliably", E "a bug found by an external triage team reaches developers slower than a red check in their own PR", F) | **REAL — with a checkbox hiding inside it.** "Integrates with GitHub Actions" is the checkbox; the REAL sub-rows are (a) **one comment, edited in place** — HN, `blintz`: *"I definitely don't want three long new messages on every PR. Max 1, ideally none"*; and (b) **which failures redden the build.** Ours: a single PR comment via an idempotency marker, and an exit contract where only `failed` exits 1 while `stale`/`errored` exit 2, so our own outage never reddens a customer's build (`lib/suite.mjs`, `templates.test.mjs`, MEASURED today). |

#### TIER 2 — real, but they decide renewal rather than purchase

| # | Row | Why it is on the list | Who it matters to | Freq | REAL or CHECKBOX — and the evidence |
|---|---|---|---|---|---|
| 9 | **Evidence attached to a failure** | A verdict with nothing under it is a black box, and a black box costs more time than it saves. | Whoever gets paged. Engineers. | 3/9 (A "Interactive debugger", D implicit, I group E "black box") | **REAL.** The quote that names the mechanism, from a Functionize user: *"spent more time investigating whether Functionize's ML made a mistake than we spent investigating actual bugs. The black box created more work, not less"* — REPORTED, and **sourced from a competitor's blog** (getautonoma.com), so treat the wording as adversarial and the direction as consistent with §I. Ours: screenshot + page text written to `.smolanalytics/evidence/` **on failure and flake only**, named in the step summary, uploaded as a CI artifact — and evidence can never change a verdict. |
| 10 | **Suite wall-clock at the size you will actually have** | Every vendor quotes per-test speed; nobody buys per-test. A 200-test suite run serially is unusable in CI no matter how fast one test is. | Anyone past ~30 tests. | 3/9 (A "Parallel execution", F, I group B "slowness" — 7 of 34 dislike items) | **REAL, and it was our biggest hole until this month.** `RED_TEAM.md` §4 named it *"the most urgent gap on this list"* and it is now closed and measured: **50 recorded tests, 39.6s at `--workers 1`, 4.9s at 8 workers**, one Chromium with a context per worker, **917MB peak vs 2233MB** for a browser per test (MEASURED, `lib/pool.mjs` header). The number that matters on a 2-core CI runner is the memory one. Ask a vendor for wall-clock at *your* suite size; a per-test number is a deflection. |
| 11 | **Maintenance model — who fixes it when the UI changes** | The promise the whole category is sold on. Also the promise it most often breaks. | Everyone. | **6/9 — the single highest-frequency row in the corpus** (A "Test maintenance", B "Self-heals on intent, not selectors", C "Self-healing under churn" + "Maintenance model", D "Maintenance", E "Maintenance burden", F "Self-healing effectiveness", I group D) | **The outcome is REAL; the feature called "self-healing" is a CHECKBOX that can go negative.** The evidence for the negative is the cleanest single item in the churn tally: **Autify — "self-healing can mask real regressions"** (REPORTED). A healer that silently changes what is asserted converts your test suite into a machine for producing green. The buyer's question is therefore not *"do you self-heal"* (everyone says yes) but *"when you heal, do you show me the diff, and does a heal that fails re-verification get reverted?"* Autonoma's revert-on-rewrite discipline is the one part of their adjudicator worth copying, and it is measured in their README. |
| 12 | **Whose model key pays, and can the bill run away** | New in 2026 and absent from every grid in §3.1. Handing an API key to a loop that drives a browser on every PR across a team is an unbounded liability, and buyers have no way to size it. | Anyone on BYO-key tools — which is now most agent-first tools. | **0/9 — asked by nobody in the sources, asked by everybody in practice** | **REAL and unrepresented.** The honest form of the row is two cells: *whose key* and *what stops it*. Ours: the customer's key, tokens reported from the API's own `usage` block and never estimated, dollars shown only when the customer supplies their own per-million prices, and **`--max-calls` caps on calls not dollars — a call cap is exact and needs no price table — exiting 2, never a verdict about the app** (`lib/cost.mjs`, MEASURED today). Vendors whose own key pays (Ranger, QA Wolf, Autonoma hosted) answer this with their price instead, which is a legitimate answer. |
| 13 | **Will you exist in twelve months** | Not paranoia in this category. Eight named companies dead, dormant, absorbed or pivoted; Octomind churned its own paying customers in May 2026 after telling them the site would "stick around for a while yet". | Everyone. Asked out loud by nobody. | 0/9 in criteria lists; the mechanism is documented in `GRAVEYARD_AND_BUYER.md` §1 | **REAL — and it is a row a small vendor cannot win, only neutralise.** MEASURED today: `octomind.dev` still **DNS-dead**; `rainforestqa.com/pricing` **301s to `/talk-to-sales`**. The only honest answer to this question is row 4: make cancellation cost the customer nothing, so the answer stops mattering. Ours is LICENSE §4 — the tests, recordings and evidence are the customer's own plain files and the licence claims nothing in them. |
| 14 | **Security review: SOC 2, SSO, data handling** | Below ~30 people this is a vendor-questionnaire ritual. Above it, it is a gate with a person behind it. | 50+ almost always; 5–50 only when their own customer demands it. See §5. | 0/9 in criteria lists (!), but present on 3 of 6 vendor homepages checked today | **CHECKBOX below ~30 people, hard REAL above it.** MEASURED today: Autonoma's homepage now claims **"SOC 2 Type II Certified"** and lists **SSO on the free tier** ("Okta, Google Workspace, Azure AD, or any SAML provider"); Momentic claims "SOC 2 Type 2" + "99.99% uptime SLA" + "SAML SSO"; Bug0 "SOC 2 certified… View Trust Center"; QA Wolf runs `trust.qawolf.com` (200). We have none of this. The sharpened version of the row is **the SSO tax**: sso.tax records **Cypress.io at $75/mo base → $300/mo with SSO, a 300% increase**, and BrowserStack as "call for a quote" (MEASURED). A buyer who needs SSO should price it, not tick it. |
| 15 | **Does it work on pages an LLM wrote** | The fastest-growing app shape, and an exact-text assertion is structurally wrong for it: the same prompt renders differently every run. | Anyone shipping an AI feature — which by 2026 is most of our ICP. | 1/9 (B "Works on AI-generated code" — though B means *code written by an agent*, a different and easier problem) | **REAL and emerging; we are behind.** The distinction is worth stating because vendors blur it: "works on AI-generated *code*" (the UI churns between sprints) is a self-healing problem everyone claims; "works on AI-generated *output*" (the page text is non-deterministic by design) is an assertion problem that our text-proof replay fails by construction. Momentic and mabl ship semantic assertions in GA; we ship nothing. `WHITESPACE_DB_AND_AI.md`'s LLM-boundary cassette is still the unbuilt first-mover move. |

#### TIER 3 — the checkboxes. Asked, ticked, and never used again

These are the rows that fill a spreadsheet and decide nothing. Each one gets its evidence for being
called a checkbox, because "this row doesn't matter" is a claim, not an opinion.

| # | Row | Who asks | Freq | Why it is a CHECKBOX |
|---|---|---|---|---|
| 16 | **Cross-browser (Chromium / Firefox / WebKit)** | Everybody, first, because it is the cheapest cell to fill | 3/9 (A lists it as comparison row #1; D names Chrome-only as a limitation; G implies it) | **CHECKBOX for the 5–50 web-app buyer — evidenced by what funded vendors ship and get away with.** MEASURED today from Momentic's own docs: **"Chromium-based browsers, locally or in CI"** — no Firefox, no WebKit, no Safari. Momentic has raised $19.2M and lists Notion and Retool (prior research). Ranger pins a single Playwright Chromium build. A row that three funded vendors fail while selling to name-brand customers is not a row that decides. **Honest note against our own interest:** we shipped all three engines this month (`lib/engines.mjs`, verified launching), and no buyer evidence in this corpus asked us to. It was the right build for a different reason — a recording made on one engine and replayed on another *says so* instead of pretending — but it should not be marketed as a decisive row, because it isn't. **I could not find a survey measuring what fraction of teams actually run non-Chromium browsers in CI; that number does not appear to exist publicly, and I am not going to invent it.** |
| 17 | **Native mobile (iOS / Android)** | Enterprise buyers and anyone with an app-store binary | 1/9 in criteria lists (D "Web-only"), plus the churn tally's coverage-wall group | **CHECKBOX for our ICP, REAL for a different ICP entirely.** It is a *disqualifier* if you ship a native app and a *dead cell* if you don't. Autonoma prices iOS at 200 credits and Android at 40 in their billing code — you don't write rate-card entries for vapor — so it is real on their side. We have nothing and should say so in docs rather than vapor it. The mistake to avoid is treating this as a gap to close: our buyer ships responsive web. |
| 18 | **"AI-powered test generation"** | Everyone in 2024. Fewer every quarter. | 3/9 (B "Generates coverage from your codebase", F "Autonomous test generation", G "Generative AI" filter) | **CHECKBOX, and the market priced it at zero.** MEASURED today: Playwright ships **"🎭 planner, 🎭 generator and 🎭 healer"** free — planner *"explores the app and produces a Markdown test plan"*, generator *"transforms the Markdown plan into the Playwright Test files"*, healer *"executes the test suite and automatically repairs failing tests"* — via `npx playwright init-agents --loop=vscode|claude|codex|opencode`. Octomind was charging **~$4.45 per generated test** and is dead. Writing tests was never the bottleneck; keeping them true was. HN's sharpest formulation of this, from the Canary thread: *"The interesting question is not whether the system can generate a plausible PR-time test, but whether the useful ones survive after the PR is gone… That conversion rate feels closer to the real moat than the generation demo."* |
| 19 | **"Non-technical people can author tests"** | Enterprise buyers with a QA function | 3/9 (B "No QA team required", C "who can write a flow (engineer vs anyone)", F "Team democratization") | **CHECKBOX for us, and a documented churn risk.** At 5–50 there is no non-technical author to serve: quality is *"a full team effort… engineers writing tests, and testing their own PRs"* until the first QA hire, which arrives reactively after an incident (`GRAVEYARD_AND_BUYER.md` §4). Worse, the plain-English promise has its own review-corpus objection — testRigor: *"the plain English approach may lack precision for complex scenarios… adds ambiguity that developers will find frustrating"* — and walrus.ai, the closest historical analog to a pure plain-English pitch, is DNS-dead. Note this cuts at us: our own pitch is a sentence. The mitigation is showing the compiled artefact, not the sentence. |
| 20 | **Managed preview environments per PR** | Vendors who built one | 1/9 (B, i.e. Autonoma about Autonoma) | **CHECKBOX for our ICP — they already have this.** Vercel, Netlify and Cloudflare Pages announce every preview as a GitHub deployment with a status, so a runner can *discover* the URL instead of building an environment. The vendor-built version is where their runs die: **~48% of Autonoma classifications have no recording, 97% of those executed no steps** (MEASURED, their README). A row that a vendor invented to describe their own hardest-won capability is a row to read sceptically. Honest boundary: for a buyer with *no* preview infrastructure, this is real and we cannot serve them. |
| 21 | **Visual / pixel regression** | Anyone who has heard of Applitools | 1/9 explicit (D "Visual testing on Chrome only") + G | **CHECKBOX with a trap in it.** Real for design-heavy products; a maintenance tax for everyone else. The churn tally on Applitools is the whole argument: *"false positives from anti-aliasing, dynamic content, and one-pixel render shifts"* and *"every legitimate UI tweak means going back and re-approving baselines… teams need to accept baselines almost one by one"*. What is REAL is the much smaller thing underneath it — **did the page render at all** — which is row 2's other half and does not require a baseline (see §6.3). |
| 22 | **Capterra's 24 feature filters** | Buyers who start on a review site | 1/9 (G) | **The purest checkboxes in the file, quoted verbatim as evidence of the genre:** "Action-Word Testing", "Activity Dashboard", "AI Copilot", "API", "Collaboration Tools", "Customizable Reports", "Generative AI", "Hierarchical View", "Model-Based Testing", "Monitoring", "Move & Copy", "Parameterized Testing", "Quality Assurance", "Reporting & Statistics", "Reporting/Analytics", "Requirements Management", "Requirements-Based Testing", "Security Testing", "Software Testing Management", "Static Analysis", "Supports Parallel Execution", "Test Script Reviews", "Unicode Compliance", "Workflow Management" (MEASURED 2026-08-30). **Not one of them is any of rows 1–15.** "Unicode Compliance", "Move & Copy" and "Hierarchical View" are attributes of a 2012 test-management console. **The review-site grid is a different checklist from the engineer's, and optimising for it is how a product ends up sold to a persona that does not exist at 5–50** (`GRAVEYARD_AND_BUYER.md` §5.7). |
| 23 | **Language flexibility / component testing / API testing / "unified toolchain"** | Enterprise and platform teams | 3/9 (A "Language flexibility", A "Component testing", F "Unified vs Fragmented Tool Chains") | **CHECKBOX for our ICP.** These are suite-consolidation arguments aimed at a buyer replacing four tools with one under a procurement mandate. A 12-person team on Next.js and Vercel is not consolidating a toolchain; they are trying to stop shipping a broken checkout. Answering these rows well is how a small vendor accidentally builds Katalon. |

---

## 4. The disqualifiers — single findings that end an evaluation on the spot

A disqualifier is not a low score. It is a finding that stops the spreadsheet being filled in, so
the remaining twenty rows are never scored at all. Ranked by how early in the evaluation they fire.

| # | Disqualifier | Fires at | Evidence it actually ends evaluations |
|---|---|---|---|
| 1 | **No price on the page** | Minute one, before signup | The single best-attested one, because it has a documented mechanism *and* a documented reflex. Mechanism: **`rainforestqa.com/pricing` 301-redirects to `/talk-to-sales`** (MEASURED 2026-08-30) — the disqualifier encoded in an HTTP header. Reflex, from HN's Preflight thread: ***"without a pricing page I just move along"***. Today, quote-only across all tiers: **mabl, qa.tech, Rainforest, testRigor (no pricing page at all — 404 on three URL shapes), Meticulous (404), Spur (404)**. For a founder buying with a card there is no procurement process to absorb a sales cycle, so "Talk to us" reads as "not for you", which is often literally correct. |
| 2 | **A demo call required before you can run it once** | Minute one | Same buyer, same reflex, one step further along. `qa.tech` gates all three tiers behind "Talk to us" and offers "a free POC" — a POC is a meeting. `GRAVEYARD_AND_BUYER.md` §4 establishes there is no QA persona and no budget owner at 5–50; the person evaluating is the eng lead doing it *between* other work, usually at the moment an incident made it urgent. A calendar link is a two-week delay against a problem that felt urgent for four days. |
| 3 | **A false GREEN found in the trial** | Day one to day three | The fastest possible death, and asymmetric with red: a false red costs trust in the tool, a false green costs trust in *every verdict it ever emitted*. This is why we built the render guard rather than more features: our replay proof is page text, so **a 404'd stylesheet, a blank `<div id="root">` with the proof text parked at `left:-9999px`, or a Next.js error overlay in a shadow root all replayed GREEN** (MEASURED, `lib/render.mjs` header — 32 characters of innerText, 0 painted). If a buyer breaks your app on purpose during a trial and your suite stays green, there is no row 2 through 23. |
| 4 | **A false RED in the trial** | Day one to week two | The category's documented #1 churn reason — group A of the 34-item tally, present for **every one of the eight vendors** surveyed. It rarely produces a cancellation email; it produces muting, then non-renewal. Slack built **auto-detection and suppression** of flaky tests at scale; Google reports **84% of pass→fail transitions involve a flaky test**; the endgame in the wild is deletion experienced as relief: *"We deleted 247 E2E tests and CI got 62% faster… developers started trusting CI again"* (REPORTED). |
| 5 | **Nothing survives cancellation** | During the security/legal skim, or the moment someone asks "what if" | Two documented mechanisms, not opinions. **Vendor kills your escape hatch:** Cypress blocked deploysentinel.com to force flake dashboards into Cypress Cloud. **Vendor dies and takes the artefacts:** Octomind's paying customers lost the product in May 2026; `octomind.dev` is **still DNS-dead today** (MEASURED). HN's version, repeated in every launch thread read: *"Does it output playwright scripts?"*, *"User get to take away the script so they don't get vendor lock-in"*. Bug0's review of Momentic names the failure state exactly: *"No code export… vendor lock-in by design."* |
| 6 | **Onboarding wants write access to the repo or the org** | The moment the install flow is read | Autonoma's measured onboarding: **install a GitHub App across repos → the agent pushes a Dockerfile into your code → hand over `SUPABASE_URL` and `OPENAI_API_KEY` → "ETA ~1h 13m" → 28 min in at 3% → 0% → step 1 of 7 failed** (MEASURED, teardown). Each of those is individually survivable and the sequence is not, because it front-loads every irreversible decision before the first verdict. The contrast that matters is not "we're easier" — it is that the buyer is asked to trust before receiving anything. |
| 7 | **The meter charges for the tool's own mistakes** | When someone actually reads the pricing page | Momentic, in their own words: **"Every test step uses one credit, including steps that AI features generate and run (AI actions, failure recovery, auto-heal)"** (MEASURED). You pay per unit of the vendor's healing. Checkly: **"Each retry counts as a check run"** — you pay per unit of flake. Neither is hidden; both are on the pricing page; both are the kind of sentence an engineer reads twice and then closes the tab. Octomind's ~$4.45 per generated test is the version that proved fatal. |
| 8 | **A security question with no answer** | Vendor review, roughly 30+ people | Below ~30 people this is theatre. Above it, one unanswered questionnaire ends the evaluation because the champion has nothing to forward. MEASURED today: **Autonoma, Momentic, Meticulous and Bug0 all return 404 on `/security`**, though Autonoma, Momentic and Bug0 make SOC 2 claims on the homepage and Bug0 and QA Wolf run real trust centers (`trust.qawolf.com`, 200). A homepage badge with no trust page behind it converts a five-minute check into a week of email — which is itself the disqualifier. |
| 9 | **No SSO, or SSO priced by quote** | Vendor review, roughly 50+ people | This is a *bigger-buyer* disqualifier and near-meaningless below it. What makes it worth a row is that the category is a documented offender: **sso.tax records Cypress.io at $75/mo base → $300/mo with SSO (a 300% increase), and BrowserStack as "call for a quote"** (MEASURED). Note the competitive fact this creates: **Autonoma now lists SSO — "Okta, Google Workspace, Azure AD, or any SAML provider" — on its free tier** (MEASURED), which removes their version of this disqualifier entirely and is the smartest thing on their pricing block. |
| 10 | **Liveness signals that say the vendor is already gone** | Any time, and it is instant | Buyers in this category check, because they have been burned. The signals are cheap to read and each has a precedent: **a 404 pricing page** (testRigor, Meticulous, Spur today), **a dead domain** (walrus.ai, test.ai, octomind.dev), **a dormant repo** (Shortest: 5,666★ MIT with zero commits in 30 days; auto-playwright: 13,207 npm/week into a repo untouched for thirteen months), **a redirect into a suite vendor** (launchableinc.com → cloudbees.com; ponicode.com → circleci.com), and the sharpest one of all — on the Deltix launch thread, **the top comment reported that the support email bounces.** |
| 11 | **A named coverage wall you happen to sit behind** | First real test attempted | Not universal, but absolute when it fires: no native mobile when you ship an app-store binary; Chromium-only when your revenue is on Safari; no auth story when everything worth testing is behind a login. These are disqualifiers rather than low scores because there is no partial credit — the flow you care about is either reachable or it is not. |

**The pattern across all eleven.** Nine of them fire *before* the product is meaningfully evaluated,
and eight of the nine are about **trust, price legibility and reversibility** rather than capability.
That is the shape of a market where the software mostly works and the buying mostly doesn't.

---

## 5. For a 5–50 person startup: the three rows that actually decide

The other twenty rows get filled in. These three get argued about, and one of them ends it.

### Row I — **Time from one command to one true statement about my app** (checklist row 1)

At this size there is no evaluation *process*; there is an evening. The person deciding is the
founder or eng lead, they are doing it because something broke last week, and the window in which
they care is measured in days (`GRAVEYARD_AND_BUYER.md` §4: the trigger is an incident streak or
AI-velocity shipping outrunning QA; the first QA hire is reactive, so there is nobody whose job this
is). A tool that needs an hour of setup before its first verdict is competing against `git revert`
and a Playwright script the team's coding agent will write in twenty minutes.

The thing that makes this row decisive rather than merely nice: **the alternative is free and
already installed.** Playwright ships planner/generator/healer agents at $0, and Shortest gave away
"natural-language tests, your own key, `npx`" under MIT — 5,666 stars — and still went dormant. So
setup friction is not measured against other vendors, it is measured against *doing nothing new*.

### Row II — **What the trial's verdicts do when they are wrong, in both directions** (rows 2, 3, 9)

Not "is it accurate" — every vendor claims that. The three sub-questions a 5–50 buyer can actually
run in an afternoon:

1. **Break the app on purpose. Does the suite go red?** (false green — disqualifier 3)
2. **Change something cosmetic. Does the suite stay green?** (false red — disqualifier 4)
3. **When a test fails and then passes on retry, what is it called?** (row 3, which *no source in
   this corpus asks*, and which separates a tool that reports flake from one that launders it)

Why it decides here and not at 500: a small team has no triage layer. There is no QA engineer to
absorb a false red before it reaches an engineer, so every wrong verdict is a direct interrupt to
the person who chose the tool. The champion and the victim are the same human, and they cancel.

### Row III — **What the meter counts, and what still runs after cancellation** (rows 4, 5)

One row, because at this size they are the same question: *how much of my future am I handing over?*

- **Meter:** it must not grow when the team tests more thoroughly. Per-step (Momentic, including its
  own auto-heal), per-retry (Checkly), per-seat (Ranger $50/user/mo — the worst possible shape for a
  team that is about to double), per-test-case (Octomind, dead) all fail this. Per-PR, per-flat-month
  and generous-free-tier pass it.
- **Cancellation:** the answer must be "the tests still run". Bug0's own review of a rival names the
  failing state — *"No code export… vendor lock-in by design"* — and Octomind's customers proved the
  cost when the domain lapsed.

This is one row rather than two because a founder with ₹-level cash discipline is not modelling TCO;
they are asking whether this decision is reversible. Reversible decisions get made fast, which is the
only speed available at this size.

### How a 500-person company picks differently

| | 5–50 startup | 500-person company |
|---|---|---|
| **Who decides** | The founder or eng lead, personally, with a card, usually mid-incident | A committee: an eng manager, a QA lead who owns the function, security, procurement. The champion is not the payer and not the user |
| **Decisive row 1** | Time to first true verdict (row 1) | **Security and compliance (row 14).** SOC 2 report, DPA, SSO/SAML, RBAC, data residency. Not because it is more important — because it is the row with a *person whose job is to say no*. sso.tax exists precisely because vendors price against this asymmetry (Cypress $75 → $300) |
| **Decisive row 2** | Verdict honesty in the trial (rows 2/3) | **Maintenance model and who authors (rows 11, 19).** There *is* a QA function, so "non-technical authoring", seats, permissions and test-management reporting become real: they determine whether the existing team can operate the tool. Capterra's 24 filters — "Requirements Management", "Test Script Reviews", "Hierarchical View" — are the artefacts of exactly this buyer, which is why they read as absurd at 5–50 |
| **Decisive row 3** | Meter shape + reversibility (rows 4/5) | **Coverage breadth and a named accountable owner (rows 16, 17, 21, 23).** Native mobile, cross-browser, API and component testing, and — critically — somebody to call. The only proven large-dollar model in this category is the outcome service: QA Wolf at a **median ~$90K ACV** and Bug0 at **$2,500/mo with a forward-deployed engineer**. Large buyers are not buying software, they are buying an owner |
| **What price means** | An absolute number that must be legible on the page, because there is no procurement to hide growth in | A negotiated line item. Quote-only is *not* a disqualifier here; it is the expected motion. This is why mabl could drop its published $450/mo and why Rainforest can redirect `/pricing` to `/talk-to-sales` and survive |
| **What flake costs** | The person who chose the tool | Absorbed by a triage layer first — which is why enterprise tools can survive flake rates that would kill them at 5–50, and why the churn tally's flake complaints skew toward small teams |
| **Reversibility** | The deciding factor | Nearly irrelevant — a two-year contract is a feature to procurement, and the migration cost is somebody else's next fiscal year |

**The one-line version.** A 500-person company buys *an owner and an audit trail*; a 5–50 startup
buys *a verdict it can trust by Friday and undo on Monday*. Every row in §3 sorts differently under
those two sentences, and a vendor that tries to score well on both ends up as Katalon — which the
review corpus records churning small teams on price alone.

---

## 6. Where this file contradicts the earlier research — stated, not quietly patched

### 6.1 Autonoma published a price, and it is the price in their code. **Contradicts `SCORECARD.md` row 14 and `RED_TEAM.md` §5.**

Both of those rest on: *"`RUN_CONSUMPTION` is never written, preview compute hard-defaults to 0,
enforcement is off fleet-wide — their loop is free because it is funded,"* and on `/pricing`
returning 404. The 404 is still true (MEASURED today) but it is now irrelevant: **the price is on
the homepage.** Verbatim:

> "Free & pay as you go — $0 to start. 100K credits free. Then pay only for what you run — **$100 per
> 150K credits**, with optional auto top-up. No minimum." … "Self-hosted — **Free, forever.** Run on
> your own infrastructure. No limits, no usage costs." … "No credit card required to start. You set
> the auto top-up cap — never pay more than you choose."

$100 / 150,000 credits = **$0.000667 per credit — exactly the rate hard-coded in the billing code
the teardown read.** The "go/no-go" their own code comment was waiting on has evidently happened.
At their published rate card that is ~$0.0067 per web run, ~$0.333 per generated test, ~$0.133 per
iOS run.

**What must change in our positioning.** We can no longer say "their price is zero, which is a
runway not a business." We must say the narrower and still-true thing: **their meter is usage-based
and ours is not.** Theirs grows with how much you test; ours grows with how much you ship. That is
the argument `RED_TEAM.md` §2.1 identified as the one most directly opposed to the documented cause
of death in this category, and it survives their price going live — it just no longer gets to lean
on "$0 is a subsidy". Also note two disqualifiers they have now closed that we have not: **SSO on
the free tier** and **a SOC 2 Type II claim**.

### 6.2 Autonoma's BUSL use-grant is *softer* than the teardown recorded. **Contradicts `AUTONOMA_TEARDOWN.md` §6.**

The teardown says their use-grant is "harsher than HashiCorp's (no internal-use safe harbour)".
MEASURED today, `raw.githubusercontent.com/Autonoma-AI/autonoma/main/LICENSE.md`:

> "**Additional Use Grant:** You may use the Licensed Work **in production**, provided that you do not
> use it to offer a commercial product or service that charges customers, directly or indirectly, for
> the functionality of the Licensed Work or any derivative of it."
> "**Change Date:** March 23, 2028. **Change License:** Apache License, Version 2.0"

That *is* an internal-use production safe harbour. Correct the teardown. **What still stands:**
GitHub classifies the repo's licence as **`NOASSERTION`** (MEASURED via `gh api`) while the repo
description and homepage both call it *"an open-source testing platform"* — BUSL is not an OSI
licence by MariaDB's own definition, so the open-washing observation survives intact. Repo state
today: **190 stars, last push 2026-08-28** — actively developed.

### 6.3 Their SOC 2 badge has no page behind it — which is disqualifier 8, aimed the other way

MEASURED today: the homepage links `/trust`, **`getautonoma.com/trust` returns 404**, and
**`trust.getautonoma.com` does not resolve.** A "SOC 2 Type II Certified" claim with no reachable
report or trust center is exactly the finding that converts a five-minute vendor check into a week
of email. By contrast `trust.momentic.ai` and `trust.qawolf.com` both return 200. Recorded here
because it is the one row where a checklist-driven buyer would mark them down, and because we
should not repeat it: **do not put a compliance badge on a page until the page behind it exists.**

### 6.4 QA Wolf now publishes unit prices. **Contradicts `GRAVEYARD_AND_BUYER.md` §1 and qaskills.sh.**

Prior research (and an independent review as recently as 2026-07-07) says QA Wolf publishes no
pricing. MEASURED today, their pricing page carries **"1¢ /AI credit"** and **"15¢ /runner minute"**
for a self-serve *platform* tier, alongside the unchanged quote-only *Coverage as a Service*. The
$40–44/test/month and ~$90K ACV figures remain REPORTED (Vendr, G2, and a competitor's blog) and
apply to the service half. The move to publish is itself the datum: **the outcome-service vendor
grew a self-serve tier downward**, which is the direction we assumed nobody could afford to go.

### 6.5 mabl's published $450/mo is gone; Ranger repriced to per-seat

mabl is now **"Request a Quote" / "BOOK A DEMO"** with no number (MEASURED) — the churn tally's
$450+ figure has no published successor. Ranger, recorded on 2026-08-25 as "free forever — 5 reviews,
self-serve", now shows **Growth at $50/month/user** (MEASURED). Per-seat is the worst meter shape
for a team that is about to double, and worth watching as a leading indicator of where they are
aiming.

### 6.6 This file down-weights four rows `SCORECARD.md` treated as scoring rows

Not a factual contradiction — a contradiction of emphasis, which matters more for what gets built.
`SCORECARD.md` gives full rows to **visual checks (9), mobile (10), suite generation (11)** and
implicitly to cross-browser. Against the buyer evidence assembled here, three of those four are
**checkbox rows for our ICP** (§3.2 rows 16, 17, 18, 21). The scorecard's *closing costs* were right
to refuse the expensive versions (screenshot-diff, native Appium) — but the ranking that put mobile
fourth on a five-item behind-list still over-weights it. On this evidence the behind-list reorders to:
**(1) AI-output assertions [row 15], (2) scenario-vs-app causality inside `failed` [row 3's cousin],
(3) iframes/shadow DOM, (4) mobile-web viewports, (5) native mobile — refuse.**

### 6.7 A method near-miss, recorded so it does not get repeated

I found `tryranger.com` serving a **307 to a GoDaddy for-sale parking page** and was one step from
adding "Ranger" to the graveyard. **It is the wrong domain** — Ranger is `ranger.net`, alive, with a
pricing page that answered today. The prior research had the correct domain and I checked it against
the prior research before writing anything. A dead domain is the single most-quoted death signal in
this category and therefore the easiest one to get wrong; every liveness claim in §4 row 10 is
against a domain named in an earlier file or fetched from the vendor's own current homepage.

---

## 7. Us, scored against our own checklist — including the rows we lose

No self-congratulation: the rows are scored the same way a buyer would, and the losses are listed
first because they are what a real evaluation would surface.

### The rows we lose

| Row | Where we stand (MEASURED 2026-08-30 unless noted) | Cost to close |
|---|---|---|
| **13 — will you exist in twelve months** | **Our worst row, and structurally unwinnable.** One person, no funding disclosed, `smolanalytics` npm at **1,191 downloads/week** (MEASURED, npm API) — and per `RED_TEAM.md` §2.3 that number measures curiosity, not use (auto-playwright pulls 13,207/wk into a repo untouched for thirteen months). We cannot answer this row; we can only make it not matter, via row 4. | Not closeable. Neutralise: lead with LICENSE §4 and "cancel us and every test still runs", and instrument *suites that ran more than once this week* as the only honest traction metric. |
| **14 — security review** | No SOC 2, no SSO, no trust center. What we do have, and it is the right shape: `smolanalytics.com/security` returns 200 and says out loud **"We can't show you a compliance badge or a decade of history,"** then lists the isolation model, scrypt password hashing, TLS-everywhere and **four named subprocessors** (Vercel, Neon, Fly.io, Dodo). | A truthful page beats a badge with a 404 behind it (§6.3). Above ~50 people this is still a hard stop; below it, the honest page is competitive today. |
| **15 — pages an LLM wrote** | **Nothing.** Our replay proof is `text.includes(proof)`, which is wrong by construction for non-deterministic output. Momentic and mabl ship semantic assertions in GA. | The `WHITESPACE_DB_AND_AI.md` LLM-boundary cassette. Still the one place we would be first rather than catching up, and it is now the **#1** item on the behind-list (§6.6). |
| **17 — native mobile** | **Nothing.** Autonoma prices iOS at 200 credits and Android at 40 in code. | Refuse native, ship mobile-web viewports, say so in docs. Checkbox for our ICP (§3.2 row 17). |
| **19 — non-technical authoring** | Our pitch *is* a sentence, which inherits testRigor's most-cited developer objection and walrus.ai's epitaph. | Not a feature gap — a copy problem. Show the compiled, inspectable recording, not the sentence. |
| **11 — maintenance model** | Partial. We do not "self-heal": a recording that stops fitting becomes **`stale`**, and the agent re-records from the sentence. That is more honest than a silent heal (Autify: *"self-healing can mask real regressions"*) but it is a *different* answer to the question the row asks, and a buyer comparing grids will read a blank cell. | Copy, not code: name the behaviour ("we never silently change what is asserted") in the cell where competitors write "self-healing". |
| **20, 21, 23 — managed previews, visual baselines, toolchain breadth** | Absent by choice. `lib/preview.mjs` *discovers* the PR's own preview deployment and never guesses; `lib/render.mjs` guards catastrophic renders but keeps no baselines. | Keep refusing. §3.2 rows 20, 21, 23. |

### The rows we win, and which of them are new since `SCORECARD.md`

| Row | Evidence, verified in code today | New? |
|---|---|---|
| **1 — time to first verdict** | `npx smolanalytics test --url … --test "…"`; no account, no GitHub App, nothing written to the repo; README quickstart shows an 11.4s run | no |
| **2 + 3 — verdict honesty** | Five statuses; pass-on-retry is **`flaky`, never `passed`**; `errored`/`stale` never retried; the cloud API refuses `flaky` as an incoming run status because a single run is never flaky | no |
| **3b — false green** | `lib/render.mjs`: would-be PASS + catastrophic render → `failed`; a `failed` is never softened; `stale`/`errored`/`flaky` never checked. **10 healthy-but-odd fixtures, zero findings each** | **new** |
| **4 — survives cancellation** | Tests are `.md` sentences, recordings and evidence are plain files in the customer's repo; LICENSE §4 claims nothing in them | no |
| **5 — meter** | **tested pull requests**, $19/mo flat, 100 included then 10c; runs execute on the customer's CI with the customer's key, so marginal COGS ≈ $0; **"No seats, no per-site fee, no Team or Enterprise tier, no contact-sales"** on the page | no |
| **6 — auth** | One login per suite, storage state reused, expiry detected and repaired with exactly one fresh login, a failed login is `errored` not `failed`, credentials from env only | **new** |
| **7 — test data** | `--seed <url>` before the run + `--teardown <url>` after (including after failures), `smoltest` prefix on every generated value, RFC-2606 `example.com` emails | **new (`--seed`)** |
| **8 — per-PR gate** | One PR comment edited in place via an idempotency marker; only `failed` exits 1, `stale`/`errored` exit 2 | no |
| **10 — suite wall-clock** | **50 tests: 39.6s serial → 4.9s at 8 workers; 917MB peak vs 2233MB** for a browser per test; results indexed by suite order so the summary is byte-identical to a serial run | **new** |
| **12 — cost ceiling** | Tokens from the API's own `usage`, never estimated; dollars only from customer-supplied prices; `--max-calls` caps **calls not dollars**, exits 2 | **new** |
| **— — diff-aware selection** | `--since <ref>`: removes a test only on positive evidence it is unrelated; every unknown (no recording, no git, no merge base, empty diff, internal throw) **runs everything and says why** | **new** |
| **— — shareable verdict** | `--share` publishes one run page anyone can open with no account; opt-in; cannot change a verdict or exit code; ~25 redaction rules run before anything leaves the machine | **new** |

### The disqualifier sweep, applied to us

Pass: **1** (price on the page), **2** (no demo required), **3** (render guard), **4** (flaky is its
own status), **5** (files in their repo), **6** (nothing written to the repo, no GitHub App),
**7** (meter does not charge for our mistakes — `--max-calls` exits 2 rather than billing).
Fail: **8/9** (no SOC 2, no SSO — mitigated below ~30 people by an honest security page),
**10** (liveness: we are the smallest vendor in the file and have no public usage to point at),
**11** (native mobile, and semantic assertions for LLM-output pages).

---

## 8. What this changes

1. **Stop leading with "their price is zero."** It isn't any more (§6.1). Lead with the *unit*:
   theirs grows with how much you test, ours grows with how much you ship. That sentence is still
   the one most directly opposed to the documented cause of death in this category, and it no longer
   depends on a subsidy that has now ended.
2. **Add row 3 to our own marketing, because nobody in the corpus asks it.** *"When a test fails and
   then passes on retry, what does your tool call it?"* Zero of nine sources ask this; we have the
   only answer in the file that isn't `passed`; and it is checkable in an afternoon by the exact
   buyer we want. This is a better wedge question than any capability row.
3. **Two disqualifiers we can close cheaply and one we cannot.** SSO is not applicable to a product
   with no seats — say that explicitly rather than leaving the cell blank. A trust page is not a
   SOC 2 report, and ours already reads better than a badge with a 404 behind it (§6.3). Row 13 is
   not closeable and should be answered with row 4 every time it is raised.
4. **Reorder the behind-list.** Per §6.6: AI-output assertions first, scenario-vs-app causality
   second, iframes/shadow DOM third, mobile-web viewports fourth, native mobile refused. `SCORECARD.md`
   put mobile fourth of five on a list where three of the five were checkboxes for our buyer.
5. **Do not build to the review-site grid.** Capterra's 24 filters (§3.2 row 22) contain none of
   rows 1–15. Building toward "Requirements Management" and "Hierarchical View" is the documented
   path to being sold to a persona that does not exist at 5–50.

---

*All URLs fetched and all code read on 2026-08-30 unless a different date is stated in the cell.
Where a claim could not be verified from a primary source it is labelled REPORTED or left as
unverified — in particular, no public survey appears to exist measuring how many teams run
non-Chromium browsers in CI, and QA Wolf's $40–44/test and ~$90K ACV figures remain third-party.*
