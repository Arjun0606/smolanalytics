# The agent-first E2E testing field, taken apart

Sweep date **2026-08-25**, in two passes. **The second pass** added the open-source agent-first tier
(§1, "The OSS agent-first tier" — Midscene, Shortest, Hercules, auto-playwright, Skyvern), recovered
Octomind's real pricing page from the Wayback raw-HTML endpoint, and **corrected two claims the first
pass got wrong**: that nobody in the field owns a portable local replay artefact (Midscene does), and
that nobody benchmarks (Midscene does). Both corrections are marked in place rather than quietly
edited out. Companion to `AUTONOMA_TEARDOWN.md` (Autonoma is not re-covered; it is
cross-referenced) and `FIELD_INCUMBENTS_AND_FREE.md` (Mabl/Testim/Rainforest/Checkly/Cypress/
BrowserStack/Sauce/Meticulous/Playwright-free-stack are not re-covered).

Evidence labels on every load-bearing claim:

- **MEASURED** — I fetched the page, hit the API, or ran the query on the stated date and am quoting it.
- **CLAIMED** — the vendor's own marketing/docs assert it.
- **REPORTED** — a third party asserts it; I could not verify independently. Never treated as fact.
- **INFERRED** — my read from the above, marked as mine.
- **unverified** — I tried and failed. Says so, stays unpromoted.

---

## 0. The headline: this field has a graveyard, and it is recent

Before any comparison, the exact count, because the exact count is the argument:

- **Of the eleven companies named in the brief, four are gone** — Octomind (**dead**, June 2026),
  ZeroStep (**dead**), Reflect.run (**absorbed** by SmartBear), and heal.dev (**abandoned its SaaS**,
  pricing page now 404, see §1).
- **Two more agent-first testing startups pivoted out of testing entirely** — CamelQA (now an
  inference reseller) and Magnitude (now a local-model general agent).
- **And two of the most-starred free agent-first testing projects are dormant** — Antiwork's
  Shortest and auto-playwright (§1, "The OSS agent-first tier").

**Eight dead, absorbed, pivoted or dormant.** This is the single most important fact in the document
and it is all MEASURED.

| company | status | evidence |
|---|---|---|
| **Octomind** | **Dead.** Product off end of May 2026, company closed end of June 2026 | Farewell letter (below); all 13 GitHub repos `archived: true`, last pushes 2026-07-14; `dig octomind.dev` returns **no A record** on 2026-08-25 |
| **ZeroStep** | **Dead.** | `zerostep.com` and `www.zerostep.com` have **no A record** (2026-08-25); GitHub org `zerostep-ai` has **0 public repos**; npm `@zerostep/playwright` last published **2023-12-08** at v0.1.5 — and still pulls **6,314 downloads/week** into a backend that no longer exists |
| **CamelQA** | **Pivoted out of QA entirely.** `camelqa.com` 301-redirects to `camelai.com`, which now sells "camelStream… unlimited frontier intelligence through one API key, $5 a month" and "camelCode — a coding agent with a permanent computer" | MEASURED redirect + camelai.com homepage, 2026-08-25 |
| **Magnitude** | **Pivoted out of testing.** 179-point Show HN (2025-04-25) for "AI-native test framework"; npm `magnitude-test` last published **2026-02-08** (v0.3.13); the `magnitudedev/magnitude` repo now reads "Open source agent with local models built in. Fully private and offline." | MEASURED GitHub + npm registry, 2026-08-25 |
| **Reflect.run** | **Absorbed.** Acquired by SmartBear **2024-01-25**; now sold as a SmartBear no-code product with quote-only pricing | REPORTED (Crunchbase/SmartBear release); MEASURED that reflect.run/pricing publishes **zero dollar figures** |
| **Shortest** (Antiwork) | **Dormant.** MIT, 5,666★, npm 8,898/week — **0 commits in 30 days, last commit 2026-05-21** | MEASURED GitHub API + npm downloads API, 2026-08-25 |
| **auto-playwright** | **Dormant.** MIT, 846★, npm **13,207/week** — **last push 2025-07-08**, thirteen months | MEASURED GitHub API + npm downloads API, 2026-08-25 |

**The pattern across all seven: downloads keep flowing into things nobody maintains.** `@zerostep/playwright`
6,314/week into a domain with no A record; `auto-playwright` 13,207/week into a repo untouched since
July 2025; `@antiwork/shortest` 8,898/week into a repo untouched since May. **In this category npm
download counts measure curiosity, not use** — a correction that applies to every traction number in
§2, including the ones that flatter the living companies.

**Octomind's own words** (MEASURED — Wayback snapshot `20260519031411` of
`octomind.dev/blog/a-letter-to-our-users-customers-and-readers/`, fetched 2026-08-25):

> "We've decided to close Octomind. The company will wind down by the end of June, and our product
> will be turned off at the end of May… **In the end, we didn't find the market validation we needed
> to keep going.**"

> "Testing is not a solved problem. It's barely a studied one. And as AI agents write more of the
> code, the cost of *not* testing — or of testing badly — is going to grow faster than the
> productivity gains from generating code in the first place. **Someone, somewhere, is going to build
> the right shape of this.**"

Cherry Ventures led their seed (€4.5M, REPORTED). The letter names **15 people** across the team and
alumni. Three years. Deriv, BRM and WingRep were named, quoted customers on the homepage.

**What Octomind actually charged (MEASURED 2026-08-25 — recovered from the Wayback raw-HTML endpoint,
snapshot `20260606112446` of `octomind.dev/pricing`, three weeks after they announced the shutdown).**
Every prior write-up of this number in the wild is REPORTED; this is the page:

| plan | price | test cases | cloud runs/mo | parallel | projects/URLs | **AI test creations/mo** | max steps/test |
|---|---|---|---|---|---|---|---|
| **Basic** | **$89/month** | 80 | 240 | 3 | 3 | **20** | 15 |
| **Pro** | **$589/month** | 300 | 1,800 | 12 | 10 | **75** | 30 |
| **Enterprise** | Custom | unlimited | custom | custom | unlimited | custom | custom |

Header: *"simple, transparent pricing."* No free tier — every plan's button reads *"START FREE TRIAL."*
Enterprise adds a dedicated contact, custom SLA and SOC2.

**The line worth staring at is "20 AI test creations/month" on an $89 plan** (MEASURED). Octomind
metered the *authoring* step — the thing that had already become free everywhere else — at roughly
**$4.45 per generated test**, while a 6.6× price jump to Pro bought only 3.75× the creations. A
$589/month ceiling of 300 test cases and 1,800 runs is **$0.33/run**, the most expensive per-run
number in this document, and their price rose with *how many tests you own* rather than how much
value the suite delivered.

**INFERRED, and it is the whole thesis of §4 Pattern 1 in one table:** they charged for the part that
was becoming a commodity, on a meter that punished growing the suite, and told the buyer that owning
more tests should cost more. Then the letter said they *"didn't find the market validation."*

And the detail that matters more than the eulogy: the letter promised
*"octomind.dev — including this blog — will stick around for a while yet. The writing isn't going
anywhere."* **Three months later the domain has no A record.** Every artefact a customer had —
tests, run history, the docs they were told would persist — is gone. That is the true cost of a
cloud-held test suite, MEASURED rather than argued.

**INFERRED:** the field's mortality rate is the argument, not the competitor list. Octomind had
funding, a real product, named customers, self-healing, an MCP server, a free tier, Azure DevOps and
Jenkins integrations, 527 blog posts' worth of content marketing, and a founder who could explain
the mechanism better than anyone in the category. It still could not find market validation. The
question to answer before building is not "can we beat Momentic" — it is "why did the buyer not
show up for Octomind."

---

## 1. Company by company

### QA Wolf — the one that actually makes money, and it is a services business

**Mechanism (CLAIMED, qawolf.com + docs.qawolf.com, MEASURED 2026-08-25):** three-phase. **Map**
("AI explores your application and identifies the user paths that can be tested"), **Automate**
("Describe a flow in plain language, and QA Wolf generates **Playwright and Appium** tests"),
**Run** ("tests run in parallel across QA Wolf infrastructure"). The output is deterministic code, not
an agent transcript: the marketing line is "Turn prompts into deterministic Playwright & Appium."

**Verdict taxonomy (MEASURED, docs "Diagnose the cause of a failing flow"):** three categories —
**Flakes** ("infrastructure outages, network instability, or failed dependent services"), **Bugs**
("incorrect or unexpected behavior in the application under test"), **Broken tests** ("flow logic,
bad selectors, incorrect assertions"). Classification is done in an **Investigation view by a human**
(theirs or yours). Their release doc is unusually honest: *"Resolving failures is about deliberately
clearing a release, not about achieving 100% passing results."*

**Pricing (MEASURED, qawolf.com/pricing, 2026-08-25):**
- Platform (self-serve): **"1¢ / AI credit"**, **"15¢ / runner minute"**, no per-seat fee, free trial.
- Coverage as a Service (managed): **"Custom-priced based on the number of tests under management"**;
  "QA Wolf's team creates, runs, investigates, and maintains your tests"; "Guaranteed… 80%+ automated
  test coverage"; **"Guaranteed zero flakes."**

**Whose key pays:** theirs. The AI credit *is* the model bill, resold at 1¢.

**Setup friction:** account required. `qawolf auth login` or `QAWOLF_API_KEY`; the env-id comes
"from the QA Wolf dashboard under Settings → Environments" (MEASURED, README).

**Traction (MEASURED unless noted):** `qawolf/cli` = **3,443 stars / 140 forks / 100 commits in the
last 30 days**. But read the creation date: **2019-09-17**. Those stars are the legacy 2019–2020
open-source recorder, renamed and repurposed — the npm package `qawolf` that earned them has decayed
from **20,217/mo (Jun 2021) → 19,652 (Jun 2023) → 2,492 (Jun 2025) → 471/week now**. The new
`@qawolf/cli` went **167 (May 2026) → 883 (Jun) → 2,454 (Jul) → 35,812 (Aug, 24 days)**, of which
31,161 landed last week alone. That 14× step change in one month is more consistent with their own CI
than with adoption (INFERRED; I could not separate them). Funding **$56.1M over 2 rounds**, incl. a
**$36M Series B led by Scale Venture Partners** (REPORTED). Headcount **~187–230** (REPORTED,
conflicting sources). Sacra estimates **$15–20M ARR in 2024 on ~130 customers, ACV $100–200K**
(REPORTED — Sacra models rather than audits, but the ACV band is consistent with the managed
offering). G2 **4.8/5 from 182 reviews**; Capterra **5.0/5 from 73 reviews** (REPORTED).

**Sharpest complaints (REPORTED — verbatim Capterra cons, fetched 2026-08-25):**
- Trey W., CTO: *"Early expectations around test creation speed were set more aggressively during the
  sales cycle than what delivery could realistically support."*
- Ethan C., Software Developer: *"Bug reports sometimes remain open due to irrelevant flows not passing."*
- Chandra G., Test Automation Engineer: *"Not sure how much test suites are maintained; suspect some
  redundancies."*
- Guerrero C., SDET: *"It would be nice if users could mark a run as a test run so QA Wolf doesn't
  alert when failures occur."*
- Jeroen R., Platform Ops Tech Lead: *"Can't run test workflows in parallel; cross-app tests slower
  than desired."*
- Dzuy L., Senior PM: *"Cost structure can be expensive but well worth it."*

A perfect 5.0 across 73 Capterra reviews alongside 4.8 across 182 on G2 reads as a managed review
programme, not organic sentiment (INFERRED). Note also: **4 total HN comments ever mention QA Wolf**
(MEASURED, Algolia) — a $56M company with essentially zero developer-community surface.

**Read (INFERRED):** QA Wolf is the field's only clear revenue success and it is a **staffing
business with software attached**. $100–200K ACV, a human in the Investigation view, "guaranteed zero
flakes" as a service-level promise a piece of software cannot make. The self-serve Platform tier
(1¢/credit, 15¢/runner-minute) is a lead-gen funnel into the managed contract, not the business.
They are not a competitor to a $19/mo tool; they are proof that when this category makes money, it
does so by selling labour.

---

### Momentic — the most serious engineering in the field, and it just deprecated its own cloud

This is the one to study. Everything below is MEASURED from `momentic.ai/docs` (the `.md` sources
behind `llms.txt`), fetched 2026-08-25.

**Mechanism.** Tests are **YAML in your repo**, not code. Steps are a mix of *preset* actions
(Click/Type/Select/checks) with natural-language targets and *AI action* steps for goals. Perception
is **multi-signal, not vision-only and not DOM-only**:

> "A cached step stores more than one way to find its target: where the element sits on screen, what
> it looks like, what text it contains, and the accessibility and structural attributes around it.
> **Which of those signals matters for a given step is inferred from the step's natural-language
> description.** 'The red Cancel button below the Order Summary header' leans on visual and
> positional signals; 'the Sign in button' leans on accessibility and text."

They also ship a "**Global locator redirect** — bridges the accessibility tree and actual interactive
elements so Momentic can click what the user sees, not a hidden backing element" — i.e. they hit the
exact a11y-tree failure mode our runner has to handle, and named a subsystem after it.

**Replay economics (MEASURED, their own benchmark).** *"Step caching keeps over 99% of steps under
500ms."* Click 250ms, Type 340ms, Visual diff 620ms. Against Playwright on the same login flow:
*"Cached Momentic steps were **52ms slower** on average than comparable Playwright functions.
Non-cached steps that require AI were **6354ms slower** on average. Over 99% of steps executed on
Momentic are cached."* First-run AI costs: locating an element **4–8s**, evaluating an assertion
**5–8s**, generating one AI-action command **6–12s**, classifying a failure **20–30s**, auto-healing
a section **30s+**.

**Maintenance ladder (MEASURED, four escalating tiers, in their words):**
1. **Locator auto-healing** — re-resolve from the description; *"applies to the current run and never edits the test."*
2. **Transient recovery** (beta) — generate temporary steps to clear a cookie banner etc., retry;
   *"stops after three recoveries in one run"*; explicitly *"does not hide product regressions,
   configuration errors, network outages, 5xx responses, CAPTCHAs, or browser crashes."*
3. **Permanent healing** — classify → triage → deliver. Triage *"groups related failures, repairs the
   affected tests, and verifies each change,"* then delivers as a **PR**, draft PR, direct commit,
   patch, or nothing. **AI routing** attributes each failure to a commit author from 14 days of git
   history and opens a PR assigned to them (*"It cites commit SHAs and GitHub resolves who authored
   them; the model never supplies a username directly"*).
4. **Quarantine** — *"Keep an unresolved flaky test running and collecting data without letting it
   block CI."* Their own framing: *"quarantine is containment, not a repair."*

**The verdict layer.** In-flow classification runs inside `momentic run` when retries are exhausted
and attaches *"a category, reasoning, confidence, and recoverability."* Categories map to actions
**heal / warn / fail**. Then:

> "By default the classifier only records a category: every failed test still fails CI. Set
> `overrideExitCode: true` so failures routed to **heal** or **warn** exit `0`, keeping the job
> passing while the failure is repaired or reported."

**This is the sharpest thing in the field to be right or wrong about.** The default is correct (a
failure is a failure). The opt-in is a switch that lets an LLM classifier turn a red build green. And
*"Failure categories, their actions, and the optional custom prompt stay cloud-managed in Settings >
Classification"* — the taxonomy that decides whether your build is red is **not in your repo**.

**Cloud deprecation (MEASURED — the biggest strategic signal in this document):**

> "**Authoring and running tests in the Momentic cloud is deprecated.** Run `npx momentic import` to
> pull your tests into your repo and own them with the CLI… Momentic is **CLI-first**. Tests live in
> your repo as YAML… and run locally or in CI."

Deprecated: the cloud editor, cloud-hosted runs (`momentic queue`), and Momentic Copilot (the
cloud AI authoring assistant — replaced by *"your coding agent with the MCP server"*). The
best-funded pure-play in the category has concluded that the cloud editor and the hosted runner were
the wrong products, and has walked its architecture toward tests-in-your-repo, run-on-your-CI,
authored-by-your-coding-agent. That is our architecture, arrived at independently, by a team with
$19.2M and Notion as a customer.

**Where the lock-in actually is.** Tests are portable YAML — but the **step cache is a server-side
cache backend** keyed by branch, and `MOMENTIC_API_KEY` is mandatory in CI (their GitHub Actions
example has no cache-restore step because there is nothing local to restore). The escape hatch is
`momentic snapshot`, which freezes a test *"with… step caches included"* into a zip — and
*"snapshots can only be replayed by the organization that created them."* **Your tests are yours;
the thing that makes them fast is theirs, and the escape hatch is org-locked.**

**Pricing (MEASURED, momentic.ai/pricing, 2026-08-25):**
- Free **$0/forever** — 2,000 credits/mo (**~200 test runs**), all core features, no card.
- Pay-as-you-go **$125/mo** — 10,000 credits (**~1,000 test runs**); overage **$0.01875/credit**;
  top-up 10,000 credits for $125 (**$0.0125/credit**).
- Enterprise — custom, "test-based custom pricing".
- Mobile emulator time metered separately: **8 credits/min Android, 15 credits/min iOS**; free tier
  gets 30 min/mo. Results retention **30 days** on Free and PAYG.
- Derived: **~10 credits per run ⇒ $0.125/run at plan rate, $0.1875/run at overage.**

**Whose key pays:** theirs. Sub-processors listed on their own security page include **Anthropic,
OpenAI, and Microsoft Azure** for "AI" — your DOM, your screenshots, and your app's text go through
their model accounts, and you pay in credits.

**Setup friction:** `npx @momentic/wizard@latest` scaffolds; account + `MOMENTIC_API_KEY` required for
any run; dashboard required for classification settings, quarantine, triage delivery and billing.

**Traction (MEASURED):** npm `momentic` **255,105 downloads last week**; monthly 2026:
Feb 254K, Mar 935K, Apr 270K, May 329K, Jun 321K, Jul 486K, **Aug 590K (24 days)**. GitHub org has no
big public repo (`cli` 14★, `skills` 13★, `wizard` 6★) — the downloads are the traction, and CI
re-installs inflate them (INFERRED). **$15M Series A led by Standard Capital, Nov 2025**, with
Dropbox Ventures + YC; **$19.2M total** (REPORTED, corroborated by their own blog post). Customers
named: **Notion, Xero, Bilt, Webflow, Retool**; *"more than 2,600 users"* (CLAIMED — note *users*,
not customers).

**Sharpest complaints:** I could not reach G2/TrustRadius (403 on 2026-08-25) — **unverified**.
REPORTED third-party round-ups cite Chromium/Chrome only (Safari and Firefox on the roadmap), and
opaque enterprise pricing. The structural critique is mine (INFERRED): `overrideExitCode` plus a
cloud-managed taxonomy plus a server-side cache means **the vendor owns the definition of "your build
is broken."**

**Read (INFERRED):** Momentic is the strongest competitor by a distance and the closest to us
architecturally after their pivot. We do not out-engineer them. The two places they are structurally
unable to follow: (a) their COGS is the model bill, so the meter can never go away, and (b) their
cache and taxonomy are their product surface, so they cannot make the replay artefact a file you own.

---

### TestDriver.ai — computer-use, vision fingerprints, cloud sandboxes

**Mechanism (MEASURED, docs.testdriver.ai, 2026-08-25):** *"using **AI vision** to find elements,
click, type, and assert — through the TestDriver MCP server."* Tests are **JavaScript/TypeScript run
under Vitest**, not Playwright. The agent *"performs each action, writes the generated code to your
test file, verifies the result with a screenshot, and reruns the test until it passes."*
There is a `captcha()` command that *"solve[s] captchas using 2captcha service."*

**Caching (MEASURED, "Learn" page):** *"Every element the AI vision agent discovers is cached with a
**vision fingerprint** — a perceptual hash of the screen state where it was located."* Cache key =
SHA-256 of the test file + the exact selector prompt + perceptual hash of the screen + platform.
Claim: *"Intelligent caching delivers up to **1.7x faster** test execution."* Example given: 2.1s
first call → 12ms cached. **The cache lives in their console** (*"You can clear the cache within the
TestDriver console"*).

**Founder's own description of the fallback ladder** (HN, 2025-04-25, user `tomatohs`, MEASURED):
*"We have multiple fallbacks to prevent flakes; The 'cheap' command, a description of the intended
step, and the original prompt. If any step fails, we fall back to the next source."*

**Pricing (MEASURED, testdriver.ai/pricing, 2026-08-25):**
- **Pro $20/month/user** — **10 Testing Hours/month**, overage **$3.60/hour**, cloud-hosted, Linux,
  web apps + Chrome extensions, community support.
- **Business from $600/month** — self-hosted, Windows desktop apps, VS Code extensions, test
  analytics, VPN deployment, and — note this — **"Bring Your Own Keys."**
- 14-day free trial, no card; "60 free device minutes."

**Whose key pays:** theirs, unless you are on the **$600/mo** tier. BYO-key is a paywalled enterprise
feature here; it is the default for us.

**Setup friction:** `npx testdriverai init`, or a GitHub integration ("no setup"), plus an API key
from `console.testdriver.ai/team`. Tests execute in their cloud sandbox by default
(*"This will spawn a sandbox, launch Chrome, and run the example test!"*).

**Traction (MEASURED):** `testdriverai/testdriverai` **239★ / 35 forks / no licence file / 46 open
issues**, last push 2026-08-16. npm `testdriverai` **17,046/week**; monthly 2026: Jan 18.3K, Feb
15.9K, Mar 25.7K, Apr 10.8K, May 10.9K, Jun 14.8K, Jul 17.7K, **Aug 28.7K**. Flat-to-lumpy for
eighteen months. HN footprint: **16 mentions, almost all their own "Who is hiring" posts.** Austin-based.
The org's headline description has drifted to *"Code review agent that actually runs your app"* — a
repositioning toward the coding-agent loop (INFERRED).

**Sharpest complaint I could source:** none of quality — no G2 presence I could reach, no Reddit
thread, no HN discussion. **unverified.** The absence is itself the signal (INFERRED): 91 public
repos, most of them demo repos with 0–2 stars, and no third-party conversation anywhere.

---

### Ranger — not a CI gate at all; a verifier bolted to your coding agent's inner loop

The most genuinely differentiated shape in the field, and it is not competing where the others are.

**Mechanism (MEASURED, docs.ranger.net/llms-small.txt, 2026-08-25):** *"Ranger acts as your AI
agent's QA team. When your coding agent says it's done, Ranger runs **local browser agents** that
step through your user flows."* Installed as a **Claude Code plugin + Agent Skills**, driven by
`/ranger:enable` and `ranger go`, pointed at **`localhost:3000`**. Evidence = *"screenshots, video
recordings, and Playwright traces."* Scenarios resolve to **verified / blocked / partially verified**.
Feedback loop: *"If verification finds issues, Claude fixes them and re-verifies automatically."*
Then a **Feature Review dashboard** — *"think GitHub PR review, but for UI features"* — where humans
comment on a point in a screenshot, and `/ranger:resume` feeds those comments back to the coding agent.
Only after approval: *"you're one click away from turning it into a permanent end-to-end test."*

Explicit privacy stance (CLAIMED): *"Ranger has nothing to do with this step and never reads or
stores any of your code!"*

**The setup detail worth flagging.** A "profile" is created by
`ranger profile add alice@example.com --url http://localhost:3000`, which *"opens Chromium so you can
log in… Ranger saves the session, and the verification agent reuses it on every run.
**Profiles are shared with everyone in your organization by default.**"* An org-wide shared
authenticated browser session is a real security surface, and it is the default (MEASURED; the risk
read is INFERRED).

**Pricing (MEASURED, ranger.net/pricing, 2026-08-25):** Free forever — **5 reviews**, self-serve
install. Growth — **$50/month/user**, unlimited reviews, Slack/email support. Enterprise — usage-based.
**Per-seat, not per-run** — the only company in the set that priced this way.

**Whose key pays:** theirs (no BYO-key path documented). Setup: `curl -LsSf https://cli.ranger.net/install.sh | sh`,
browser OAuth, `RANGER_CLI_TOKEN` for CI, a pinned Playwright Chromium version.

**Traction (MEASURED):** npm `@ranger-testing/ranger-cli` **1,441/week**, **152 versions** published
between 2026-01-08 and 2026-08-21 (latest `3.1.8-alpha.505027d-82`). Funding **$8.9M** — $6.5M seed
led by General Catalyst + $2.4M pre-seed led by XYZ, announced on their own blog **2025-01-15**;
angels include Clay, Suno, Dust, Hex, Webflow, Linear people. Customers named by them: **Clay, Suno,
Dust**. Claim: *"Freeing up 200+ dev hours a year (per engineer!)"*, *"80% faster shipping rate"*,
*"32% fewer bugs in production"* — all CLAIMED, no method published. Headcount **25 as of
2026-06-30** (REPORTED, Tracxn).

**Read (INFERRED):** Ranger is the only company here that understood the actual 2026 change: the
buyer is not a QA lead choosing a platform, it is a developer whose coding agent just claimed it was
done. But per-seat pricing on an inner-loop tool with a 5-review free tier is a hard sell against a
coding agent that can already open a browser, and their "one click to a permanent E2E test" — the
part that would make it compound — is the least documented feature on the site. Different lane from
ours (pre-commit inner loop vs. per-PR merge gate); the overlap risk is if they build the CI half.

---

### Bug0 — a $2,500/mo human, and a source-available library that is the real artefact

**Two products (MEASURED, bug0.com/llms.txt + /pricing, 2026-08-25):**

**Bug0 Managed** — *"A forward-deployed engineer plus Bug0's expert AI agents. They plan your tests,
generate them, verify every run, file bugs with video and repro steps, and gate your releases."*
**"$2,500/mo flat. Month-to-month. No annual contracts."** Up to **500 user flows**, pro rata beyond.
Never charged for: *"Test runs, AI credits, browser infrastructure, parallel execution, and engineer
hours."* Pilot: *"60 days. Discounted."* Coverage promise: *"100% of critical user flows covered in 7
days… Full app coverage in 4 weeks."* Their own comparison: *"$150K+/yr"* to hire vs *"$30K/yr"* for Bug0.

**Passmark** — the open-source-ish Playwright library that powers it, and **architecturally the
closest thing in the field to our runner** (MEASURED, github.com/bug0inc/passmark, 2026-08-25):

- **ARIA accessibility snapshots by default.** *"By default Passmark uses ARIA accessibility
  snapshots."* Optional `mode: "cua"` switches to OpenAI's computer-use agent (gpt-5.5 + the built-in
  `computer` tool) for visual steps, and can be mixed per-step.
- **The customer's own keys.** *"We need at least one model from Anthropic and one from Google."*
  Supports Vercel AI Gateway / OpenRouter / OpenCode Zen / Cloudflare AI Gateway. Note the honest
  distinction in their README: *"Unlike Vercel/OpenRouter/OpenCode Zen, Cloudflare is a proxy (not a
  reseller), so you still need your own keys."*
- **Multi-model assertion consensus** — *"Consensus-based validation using Claude and Gemini, with an
  **arbiter model** to resolve disagreements."* Nobody else in the field does this.
- **Video assertions** — *"record the full step run and evaluate the assertion against the whole
  video via Gemini's Files API. Useful for ephemeral UI (toasts, snackbars) that a single screenshot
  may miss."* Also unique.
- **Redis-based step caching** — *"Cache-first execution with AI fallback and automatic self-healing
  when cached steps fail."* Note the dependency: **Redis**, which the customer must run.
- Dynamic placeholders `{{run.*}} {{global.*}} {{data.*}} {{email.*}}`, an `emailsink` email provider,
  AST-validated script runner, 8 configurable model slots, Axiom/OTel telemetry.
- **Licence: FSL-1.1-ALv2**, marketed as *"The open-source Playwright library."* Functional Source
  License is source-available, not OSI open source — the same move as Autonoma's BUSL-1.1
  (see `AUTONOMA_TEARDOWN.md` §6).
- **Zero occurrences of "teardown"** in the repo (3 for "cleanup", none of them data cleanup).

**Traction (MEASURED):** Passmark **1,254★ / 182 forks / 11 contributors / 34 open issues / no GitHub
releases**; npm `passmark` **45,633/week**; **but only 2 commits in the last 30 days** (last push
2026-07-30). Momentum is cooling while the managed service is the pitch (INFERRED). Bug0 also runs a
large programmatic-SEO surface (`/knowledge-base/*-alternatives`, `/alternatives/sitemap.md`) —
several of the "competitor review" pages that surface in search for Momentic/Testim/Rainforest are
Bug0's content marketing, which is why I refused to treat them as sources anywhere in this document.

**Read (INFERRED):** Bug0 is QA Wolf's model at 1/10th the price and 1/50th the headcount, with the
runner given away to seed the funnel. The $2,500/mo flat, month-to-month, no-meter shape is the
cleanest pricing in the field — and it is only possible because the true product is one human's time.
Passmark is the piece to watch: it is BYO-key, Playwright-native, a11y-first, and runs on your CI.
If it were MIT and maintained, it would be the strongest free alternative to our runner. It is
neither.

---

### Stably — pure token pass-through, which is honest and probably unsurvivable

**Mechanism (CLAIMED, stably.ai, MEASURED 2026-08-25):** generates **Playwright** from plain English,
converting intent into *"explicit, reviewable test steps"* engineers can edit; runs *"in cloud or your
CI with screenshots, traces, and recordings."* They ship an "Orca IDE" that hosts Claude Code, Codex,
Gemini and Cursor CLI. Whether perception is DOM, vision, or a11y is **not stated anywhere I could
find — unverified.**

**Pricing (MEASURED, stably.ai/pricing, 2026-08-25) — the most transparent in the field:**
- Hobby **$0** — $10/mo of included credits, 1 concurrent browser.
- Team **$60/mo** — $60/mo of included credits, up to 50 concurrent browsers.
- Growth **$250/mo** — $250/mo of included credits, up to 100 concurrent browsers.
- Enterprise — custom, volume discounts above $1k/mo, SAML SSO.
- Meters: **Browser time $0.01/minute. AI tokens $0.30–$15 per million** (varies by model).
- Their own worked example: *"A typical test run uses ~2 minutes of browser time and ~50,000 tokens,
  costing roughly **$0.05–0.15**."*

**The thing to notice:** the plan price *equals* the included credit value. Team is $60/mo for $60 of
credits. **There is no software margin at all** — you are pre-paying for tokens and browser minutes at
roughly list price. INFERRED: this is either a deliberate land-grab (they make money only at
Enterprise) or a business with ~0% gross margin below $1k/mo. Either way it prices the *marginal test
run* honestly, which no one else does, and it means every additional test a customer writes costs
Stably real money.

**Traction (MEASURED):** npm `stably` **5,320/week**. No public GitHub org I could find under
`stably-ai` (404). YC-adjacent (REPORTED). Team size, funding, customers — **unverified.**

---

### Spur — vision-first agents, pivoted to e-commerce

**Mechanism (CLAIMED, spurtest.com, MEASURED 2026-08-25):** *"Scripted tools depend on selectors and
break when the UI changes. **Spur's agents execute intent**"* — e.g. *"add a medium black legging to
cart and check out."* Agents *"visit your site like real users"*; **no codebase access required**,
just a URL and test-account credentials. Bot protection handled by whitelisting **their static IP
list**. Mobile via uploaded `.ipa`/`.apk`. CI: **GitHub Actions only.**

**Pricing (MEASURED — page absent):** `spurtest.com/pricing` returns **404**. The homepage says
*"annual plans based on test-run volume — not per seat,"* and routes to a demo. **No public numbers.**

**Traction:** YC **S24**; **$4.5M seed, April 2025**, First Round + Pear + Neo + Conviction +
Liquid2; founders Sneha Sivakumar and Anushka Nijhawan (REPORTED, consistent across sources).
Customers claimed: **Alo, HelloFresh**. Claims: *"95% of brands automate core flows in the first
month," "80% fewer false positives than scripted suites," "20x faster release times"* — all CLAIMED,
no method. HN footprint: **effectively zero** (13 hits for "Spur QA", none about them).

**Read (INFERRED):** the tell is the vertical. "AI QA Testing for **E-Commerce**" is a narrower
position than they launched with. A vision-first agent with no code access is the *most* change-
sensitive perception layer available (same trap as Rainforest's pixel-matching, see
`FIELD_INCUMBENTS_AND_FREE.md`), and e-commerce is the one vertical where the flows are standardised
enough for it to work and the buyer has a revenue number attached to checkout breaking. Sensible
retreat; not our lane.

---

### heal.dev — abandoned the SaaS, kept the tracer

**Status (MEASURED 2026-08-25):** `heal.dev/pricing` returns **404**. The homepage now sells
*"Heal turns your codebase into a production-like, testable sandbox. Then it tests the hell out of
it, end-to-end, to find all bugs"* — four steps: setup a sandbox, document your codebase, write
tests, grow with you. Early access; *"Free for open source. Message @healdevHQ and get free credits."*

**The live artefact** is `@heal-dev/heal-playwright-tracer` (**AGPL-3.0**, 43★, 0 forks, active — last
push 2026-08-24). It is not a test runner; it is *"an **agent-first diagnostic layer** for your
Playwright tests"*:

> "The playwright trace doesn't contain enough data for LLM-based agents such as Claude or Open Code
> to analyze tests results reliably. That's because the trace is focused on locator evaluation, while
> real-life tests also evaluate non-playwright code."

Statement-level (not action-level) instrumentation via a Babel plugin + reporter, **NDJSON stream**
instead of a trace zip, full variable values (*"Captures that `status_code` was `403` inside a hidden
helper function"*), locator-highlighted screenshots, per-statement timing, API-to-source-line
correlation. Usage: *"Add this to your playwright config, run your tests, point Claude to the improved
heal trace."*

**Traction (MEASURED):** npm **49 downloads/week**. GitHub org created 2021-03-22, 10 repos, most of
them sandboxes. This is a small team that has retreated from selling a testing platform to selling
(giving away) an evidence format.

**Read (INFERRED):** heal.dev has independently concluded that **the valuable thing is the evidence
an agent can read, not the agent that clicks**. That is the same conclusion as Autonoma's adjudicator
and as our evidence bundle. The mechanism — statement-level NDJSON with variable state — is better
than what anyone else emits, including us, and it is AGPL so we cannot lift it. Also note the product
they moved *to*: "turn your codebase into a production-like sandbox" is exactly the heavy
build-an-environment-first shape our README argues against, and it is a much slower first verdict.

---

### Donobu — local-first, BYO-key, and the export is paywalled

**Mechanism (MEASURED, donobu.com, 2026-08-25):** *"a **local-first** testing platform that lets
engineers, QA, and coding agents run real browser flows, validate behavior, and **export resilient
Playwright tests**."* A desktop app (Donobu Studio) that *"spins up a local API server"* at
`localhost:31000/api/schema` and works with Claude Code, Cursor, and MCP clients. AI-powered
assertions *"for semantic checks beyond selectors."*

**Whose key pays: yours.** *"Use your own OpenAI, Anthropic, or Gemini API keys. Donobu works with
whatever LLM you trust."* And: *"All tests run locally. Your prompts, keys, and screenshots never
leave your environment."*

**Pricing (MEASURED, donobu.com/pricing, 2026-08-25):**
- Community **$0.00/month, lifetime** — manual recorder, AI authoring, **bring your own model**,
  Discord support, no card.
- Professional **$34.00/month billed yearly** — 500 Donobu Credits, **Playwright code export**, API
  access, MCP support, enhanced privacy controls, email support.
- Teams — **"Starting at $5,500/mo"** — 20 Professional licences, **self-healing tests**,
  Slack/Teams reporting, GitHub/GitLab integration, "managed quality with FDET support."

**The two decisions worth learning from, both bad (INFERRED):** (a) **Playwright export is
paid-only** — the escape hatch that makes a local-first tool trustworthy is behind the paywall, which
undoes the reason to trust it; (b) **self-healing jumps from $34/mo to $5,500/mo** with nothing in
between, so the single feature that makes a suite survive month two is priced at 160× the individual
tier. npm `donobu` **1,898/week** (MEASURED). Funding/team — **unverified**.

---

### The two newest YC entrants, because their HN threads are the best free research in the field

**TesterArmy (YC P26)** — Launch HN 2026-06-18, **132 points, 69 comments** (MEASURED, item 48586299).

Mechanism, from the founder in-thread: *"We use agents to navigate the app, making real-time
decisions based on its state. I prefer to compare it more to a manual QA engineer than to static e2e
tests."* Perception: *"a **hybrid approach that combines vision and accessibility APIs**, which is
much faster"* (said while comparing to Revyl, *"pretty sure Revyl relies only on vision models"*).
Each agent has its own inbox for email OTP. Caching: *"we cache **the trajectory of the agent** (not
the whole test run yet, as we want to keep the agent in the loop, more like a manual QA engineer, not
a test script)"* — **the model stays in the loop on every run, permanently.**

A commenter (`pranshuchittora`) read their client bundle and published the config:
`FAST_MODEL = "google/gemini-3-flash"`, `DEEP_MODEL = "openai/gpt-5.4"`,
`VISION_CLICK_MODEL = "openai/gpt-5.4"`; 15-minute run timeout; max 2–3 visual calls per step. Then
asked the question of the year: *"your current pricing is $300 for 1K tests which means $0.3 for each
test. We tried out playwright mcp and it easily consumes 1M+ tokens for a test with ~20 steps… **so
with this pricing are you guys default alive?**"* And: *"is there a benchmark which you ran to prove
the efficacy of your testing agent? because in the current stage it is a trust me bro kinda thing."*
Founder's answer, verbatim: *"**We currently do not have any benchmarks**; much of the experience
depends on the test plan. We've been mostly focusing on the customer experience not benchmarking."*

Pricing (MEASURED, tester.army/pricing, 2026-08-25): Hobby **$99/mo** (250 test runs, 3 concurrent,
2 projects); Startup **$299/mo** (1,000 runs, 10 concurrent, 5 projects); Enterprise custom;
**5 free runs** per new team. Note what is gated: **"Powered by the best frontier models" is a
Startup-tier feature.** Model quality is a pricing tier. Hobby customers are being tested by the
cheap model and are not told which.

**Canary (YC W26)** — Launch HN 2026-03-19, **58 points, 26 comments** (MEASURED, item 47441629).
Founders ex-Windsurf/Cognition/Google. Mechanism: read the codebase and the PR diff, generate and run
tests for affected flows against the preview app, comment on the PR. Their perception design, stated
in-thread and the cleanest articulation of the cascade pattern in the whole field:

> "we run a **reliability cascade**. First, we generate and execute **deterministic Playwright from
> the codebase**. If execution fails then we fall back to **DOM and aria tree**. If that still fails,
> we fall back to **vision agents** that verify what the user actually sees before flagging a drift."

**The complaints in these two threads are the most valuable primary source in this document**, because
they are developers reacting to a live pitch rather than reviewers filling in a G2 form. Collected
verbatim, MEASURED:

- `dbbk`: *"'Traditional E2E tests are slow to set up and expensive to maintain.' I don't really
  understand this. **If I'm already using Opus to write the code, surely it would know best what E2E
  tests to write to be able to verify its own output?** This seems like an unnecessary external step."*
- `poisonborz`: *"E2E tests are now quick to write due to LLMs, and are then **deterministic AND cheap
  to run**. How would this compare to the token costs of running an agent the whole time for each
  test? How do you make sure results stay stable regardless of the nondeterministic nature?"*
- `zuzululu`: *"not sure the pain point you mentioned resonate. with LLMs its very easy to do E2E
  testing. also I feel uneasy about **outsourcing this part with all the security issues** these days."*
- `Eridrus`: *"It's cool, but I'm **not super excited about using some 3rd party SaaS as a critical
  part of my testing**."* And separately: *"I don't really want to 'write tests in natural language',
  I want something to **crawl my app and figure out what's there and what's currently broken** and
  then write its own regression tests."*
- `Obertr`: *"How I write tests right now I ask claude/codex to create an eval and it just spins up a
  bg LLM agent worker which verifies the tests in the sandbox/internally. So… **in house testing is
  easier than external testing for us**."*
- `mogili`: *"**This is a solved problem, there are many that do this. Can't believe YC would fund
  this in 2026.**"*
- `negamax`: *"Was writing E2E tests ever a problem that needs automation?… **config overhead and
  potential security leaks makes it a no go**."*
- `Laurel1234`, twice, never answered: *"If you're not using locators are you just passing page
  contents to the LLM? Or using a multi modal model and say screenshotting? **My experience with that
  has been pretty poor and worse than proper e2e scripts, and is fairly expensive to boot.**"*
- `blintz` (on Canary): *"I definitely **don't want three long new messages on every PR. Max 1,
  ideally none?** Codex does a great job just using emoji."* … *"I'd rather just run a massive QA run
  every day, and then have any failures bisected, rather than per-PR."* … *"I am worried that
  **there's not a lot of value beyond the intelligence of the foundation models here**."*
- `pastescreenshot` (on Canary) — the sharpest comment I found anywhere: *"The interesting question
  to me is not whether the system can generate a plausible PR-time test, but **whether the useful ones
  survive after the PR is gone**. If Canary catches a real regression, how often can that check be
  promoted into a stable long-lived regression test without turning into a flaky,
  environment-coupled browser script? **That conversion rate feels closer to the real moat than the
  generation demo.**"*
- `thienannguyencv`: *"AI creates tests that look specific to PR but are actually **generic patterns
  mapped from the training data** — correct test structure, reasonable assertions, but not actually
  interacting with what this specific piece of code does."*
- `ashgam`: *"there has been instance of **Claude already patching the test scripts instead of fixing
  the bugs** to make the tests pass."*
- `engfan` (on Magnitude): *"You should **stop saying 100% open source when test plan generation and
  execution depend on non-open source AI components**. It just doesn't make sense."*
- `SparkyMcUnicorn` (on Magnitude), asking for the thing we built: *"It'd also be ideal if it had an
  **LLM-free executor mode** to reduce costs and increase speed (caching outputs, or maybe **use
  accessibility tree instead of VLM**)."*
- `skinfaxi`, after the TesterArmy founder pasted the same 90-word answer to three different
  objections: *"Goodness I really didn't expect such lazy copy-pasting of responses for a YC company."*

And one from Octomind's own engineers, MEASURED, HN 2026-02-11 (`bothlabs`, "At my last company
(Octomind)"): *"we built AI agents for end-to-end testing and ran into the **indirect [prompt]
injection problem constantly**. Agents that browse or interact with web pages are especially
vulnerable because you can't sanitize the entire internet… The gap between 'works in a demo' and
'works in production with adversarial input' is massive."* **Nobody in this field has a published
answer to prompt injection through the page under test.** Not one of the docs sets I read mentions it.

---

### The OSS agent-first tier — the half of the field the commercial vendors do not mention

**This is the most important addition to this document, and it was nearly missed.** Every company
above is a startup with a pricing page. But the largest agent-first browser-testing codebases by
adoption are MIT-licensed, BYO-key, run on your CI, and belong to nobody's cap table. They are the
actual alternative a developer reaches for, and three of the five are already dead or dormant —
which makes this tier simultaneously the biggest competitive threat in the document and its
second graveyard.

#### Midscene.js — the serious one, and it argues against our mechanism by name

**MEASURED 2026-08-25** unless noted. `github.com/web-infra-dev/midscene`, tagline **"GUI Agent for
E2E Testing"** — testing-first in the repo description itself, not an automation library that
mentions testing.

- **Licence MIT.** **14,676★ / 1,131 forks / 95 open issues.** Created 2024-07-23.
- **98 commits in the last 30 days**, last push **2026-08-25** (today). This is the healthiest
  engineering cadence of any artefact in this document, commercial ones included.
- npm: **`@midscene/web` 47,780/week**, **`@midscene/cli` 11,096/week**.
- Hosted under **`web-infra-dev`** — the org that ships Rspack/Rsbuild/Rslib, credited in Midscene's
  own README as its build tools, and the README credits `bytedance/ui-tars`. **INFERRED: this is
  ByteDance's web-infra team**, i.e. the one project here with a trillion-dollar company's
  infrastructure org behind it and no revenue pressure at all.

**Mechanism — pure vision, and an explicit attack on the accessibility tree.** Their README makes
the case against our perception layer better than any competitor's marketing does (MEASURED, verbatim):

> "Most UI automation — **including AI tools that read the DOM or the accessibility tree** — depends
> on page structure. That structure is fragile and incomplete: selectors break on every refactor,
> elements without semantic markup (icon-only buttons, custom controls, `<canvas>`) are invisible to
> it, native apps and cross-origin iframes are out of reach, and **it cannot tell whether something
> actually looks right**."

And: *"Midscene is all-in on pure vision for UI actions: element localization is based on screenshots
only."* Models named: `Qwen3.x`, `Doubao-Seed-2.1`, `GLM-4.6V`, `gemini-3.5-flash`, `UI-TARS`,
*"including open-source options you can self-host."* DOM is opt-in for extraction only.
Surfaces: web, Android, iOS, HarmonyOS, desktop, `<canvas>`, and *"any custom interface"* via an
`AbstractInterface` class.

**Whose key pays: yours, and nobody sits in the middle** (MEASURED, `/data-privacy.md`):
> "your page data (including the screenshot) is sent **directly to the AI model provider you choose**.
> **No third-party platform will have access to this data.**"

**Pricing: none. There is no company, no account, no meter, no cloud.** MIT, `npm i`, your key.

**They publish real benchmarks — which corrects a claim I made in §4** (MEASURED):
**AndroidWorld Pass@1 93.10%, Pass@2 95.69%, Pass@3 97.41%**; **MobileWorld 117 tasks,
Pass@1 78.63% (92/117)**, each with a per-task report page. These are public academic GUI-agent
benchmarks, so they measure *task completion*, not *web-regression verdict correctness* — the
specific thing this category sells and still nobody measures. But "nobody in this field benchmarks"
was too strong, and §4 is corrected accordingly.

**They retired MCP** (MEASURED, `/mcp.md`): *"Midscene no longer ships MCP servers. Use **Skills** to
let AI coding agents drive Midscene through the platform CLIs… pin Midscene to 1.9.8 [for] the final
version that includes MCP support."* **Independent corroboration of Microsoft's CLI-over-MCP steer**
(see `FIELD_INCUMBENTS_AND_FREE.md` Tier 3): the two most-used browser-automation projects in the
world both concluded in 2026 that coding agents should call a CLI, not an MCP server.

**Their cache — read this closely, it is the boundary of our third differentiator** (MEASURED,
`/caching.md`):
- Cache lives in **`./midscene_run/cache` as `.cache.yaml`** — *a plain local file in the customer's
  repo.* **They have the artefact-you-own property. I claimed nobody did; that was wrong for Midscene.**
- **But caching is off by default:** *"By default, if you don't configure the `cache` option, caching
  is disabled."*
- **And assertions are never cached:** *"**Never cache query results**: The query results like
  `aiBoolean`, `aiQuery`, `aiAssert` will never be cached."* A Midscene test that asserts anything
  **calls a vision model on every single run, forever.** Zero-model replay is not a feature they have
  declined to build — it is structurally unavailable to them, because a pure-vision assertion has
  nothing to cache against.
- Their measured cache effect is **51s → 28s (1.8×)**. Ours is **8.0s → 1.4s (5.7×)**. The gap is
  exactly the assertions.
- The element cache is **stored XPath, web-only, with documented limitations** — an internal
  contradiction worth naming: the project whose pitch is *"no selectors to chase"* caches by
  **saving a selector** and re-verifying it.

**Read (INFERRED):** Midscene is the strongest free alternative in existence and the most credible
argument against our perception layer. Three things keep it from being our replacement, and all three
are structural rather than a matter of effort: (1) **no verdict layer** — it has `aiAssert`, not
passed/failed/flaky/stale/errored with an exit-code contract; (2) **no zero-model replay**, by
construction, so every CI run costs tokens proportional to assertions; (3) **no history** — no
failing-since, no flake tracking, no PR ledger. It is an excellent *actor* and ships no *adjudicator*,
which is the same conclusion `AUTONOMA_TEARDOWN.md` reached about where value sits. Their vision
critique is real and we should answer it honestly rather than ignore it: icon-only buttons,
`<canvas>`, and "does it look right" are genuine a11y-tree blind spots.

#### Shortest (Antiwork) — our exact architecture, MIT, 5,666 stars, and dormant

The single most uncomfortable artefact in this document (MEASURED 2026-08-25,
`github.com/antiwork/shortest`).

- **MIT. 5,666★ / 338 forks. npm `@antiwork/shortest` 8,898/week.**
- **Mechanism (verbatim README):** *"AI-powered natural language end-to-end testing framework…
  AI-powered test execution using **Anthropic Claude API** … **Built on Playwright** … GitHub
  integration with 2FA support … Email validation with Mailosaur."*
- **Whose key pays: yours.** `npx @antiwork/shortest init` *"Generate[s] a `.env.local` file… with
  placeholders for required environment variables, such as **`ANTHROPIC_API_KEY`**"* and adds
  `.shortest/` to `.gitignore`.
- Setup: `npx @antiwork/shortest init`, a `shortest.config.ts`, `baseUrl: "http://localhost:3000"`.

Natural-language tests, Playwright underneath, the customer's own Anthropic key, `npx` to start, runs
on your machine and your CI, MIT. **That is our architecture, shipped, by Antiwork — Sahil
Lavingia's company, with Gumroad's distribution behind it.**

**And it is dormant: 0 commits in the last 30 days; last commit 2026-05-21; 2 open issues.**
(MEASURED via GitHub API.) HN footprint: a Show HN on 2024-12-23 that got **6 points, 0 comments**.

**Read (INFERRED):** this is the closest thing to a controlled experiment on our own thesis that
exists. The BYO-key/Playwright/natural-language/MIT shape was built by a well-known founder, reached
5,666 stars and ~9K weekly downloads, and then stopped. Stars and downloads did not convert into
something worth maintaining. Two readings, and I cannot separate them with the evidence I have:
either the shape is right and Antiwork simply had bigger priorities (Gumroad, Flexile, Helper), or
the shape produces adoption without retention — people try it, it does not become load-bearing, and
nobody notices when it stops. **The second reading is the one that should worry us**, and it is the
same signal as `@zerostep/playwright` still pulling 6,314/week into a dead backend: in this category,
**download counts measure curiosity, not use.**

#### Hercules (TestZeus) — Gherkin in, agent out, AGPL

MEASURED 2026-08-25, `github.com/test-zeus-ai/testzeus-hercules`. **AGPL-3.0, 1,128★**, last push
2026-08-04; PyPI `testzeus-hercules` at **1.0.2, uploaded 2026-08-03**. Self-description: *"the
world's first open-source testing agent… It turns simple, easy-to-write **Gherkin** steps into fully
automated end to end tests — no coding skills needed."* Scope is unusually wide: UI, API, security,
accessibility and visual testing, plus a **Python sandbox** that executes custom scripts from a
Gherkin step with full Playwright `page` access. Docker-distributed. Company is **TestZeus**;
`testzeus.com/pricing` is a Framer page whose numbers I could not extract — **pricing unverified.**
**INFERRED:** the Gherkin front-end is a deliberate bid for the enterprise QA-team buyer (the one
constituency that already writes Gherkin), which is a different buyer from everyone else here, and
AGPL is the standard open-core funnel.

#### auto-playwright — the fourth zombie

MEASURED 2026-08-25. `lucgagan/auto-playwright`, **MIT, 846★ / 133 forks**, npm **13,207/week** —
**last push 2025-07-08, thirteen months ago.** The original `ai()`-inside-Playwright convenience,
same shape as ZeroStep, same fate, still pulling 13K downloads a week into an unmaintained package.

#### Skyvern — adjacent, not a competitor

MEASURED 2026-08-25. `Skyvern-AI/skyvern`, **AGPL-3.0, 22,842★ / 2,146 forks**, pushed 2026-08-25,
218 open issues. *"Automate browser based workflows with AI."* Show HN 2024-03-14 **422 points**,
Launch HN (YC S23) 2024-10-24 **327 points** — by a wide margin the best-received launches in this
whole space, and it is **not a testing product**: no verdicts, no CI gate, no regression story.
Included because it is the most common thing a developer finds when searching "AI browser agent",
and because the contrast is instructive: **the browser-agent projects get 400-point HN launches; the
testing products get 6.**

---

### Also in the field (shorter entries, all MEASURED 2026-08-25)

| name | shape | pricing | note |
|---|---|---|---|
| **Reflect (SmartBear)** | Record-and-play + prompts + agents; *"Visual object detection replaces brittle code-based locators"*; cloud-only; MCP via `smartbear-mcp` | **Quote-only.** Credits 5,000 (Premium) / 20,000 (Advanced) / 40,000 (Enterprise); web test 1 credit, mobile 5, API 0.1; 14-day trial | Acquired 2024-01-25. Now an incumbent's SKU |
| **qa.tech** | *"AI agent that performs manual testing workflows"* + a "feature graph"; *"tests from the outside – no repo access"*; AWS emulators + Device Farm | **Quote-only.** Starter/Growth/Enterprise, metered on users, parallel runs, environments, retention (30/90/custom days). Free POC of "2–3 of your most critical journeys" | No numbers published at all |
| **Revyl** | Mobile-first cloud device platform; "Atlas" builds *"a continuously updated map of what your app actually does"*; Cursor/CLI/MCP/GHA | Solo / Starter / Team Pro / Enterprise — **no numbers** | HN consensus (`okwasniewski`) is that it is vision-only |
| **Magnitude** (historical) | Pure vision, **Moondream 2B as executor** + big-model planner, cached plan of NL-described actions | was OSS Apache-2.0 | **Abandoned testing.** Founder: *"we think it's a half-measure to generate actual Playwright code… you still have a brittle test at the end of the day"* |
| **vostride/agent-qa** | OSS *"self-improving QA agent… a test harness with memory"* | free, NOASSERTION licence | **961★ / 15 forks** in ~3.5 months. Grew by being spammed into every AI-testing HN thread, which got called out |

---

## 2. The comparison tables

### Mechanism — what actually perceives the page

| | perception | output artefact | replay without a model call? | where the artefact lives |
|---|---|---|---|---|
| **smolanalytics (us)** | accessibility tree | proof-carrying recording, plain file | **yes** (measured 8.0s agented → 1.4s replayed) | `.smolanalytics/recordings` in the customer's repo/CI cache |
| Momentic | multi-signal: position + appearance + text + a11y + structure, weighted by the step's own wording | YAML test in your repo + **server-side step cache** | yes, >99% of steps <500ms | tests local, **cache is theirs**, snapshots org-locked |
| Passmark (Bug0) | ARIA snapshots; optional OpenAI CUA per-step | Playwright test file | yes, cache-first | **Redis you run** |
| QA Wolf | DOM/locators (Elements picker) | **real Playwright + Appium code** | yes (it is just code) | their platform; export claimed |
| Canary (YC W26) | cascade: Playwright → DOM+aria → vision | PR-time tests | n/a | theirs |
| TestDriver | **AI vision** + coordinates | Vitest JS/TS | yes, via perceptual-hash cache | **their console** |
| TesterArmy | hybrid vision + accessibility APIs | agent trajectory | **no** — *"we want to keep the agent in the loop"* | theirs |
| Spur | vision/intent agents, no code access | none (agent runs) | not stated — unverified | theirs |
| Magnitude (dead) | **pure vision**, Moondream 2B | cached NL action plan | no (2B model every step) | local |
| Rainforest (see prior doc) | **pixel matching** | no-code steps | no | theirs |
| Stably | not stated — **unverified** | Playwright | not stated | cloud or your CI |
| Ranger | local browser agents, Playwright traces | feature-review evidence | n/a (inner loop) | dashboard |
| heal.dev | n/a — instruments *your* Playwright | **statement-level NDJSON trace** | n/a | local (AGPL) |
| **Midscene (MIT)** | **pure vision, screenshots only**; DOM opt-in for extraction | `.cache.yaml` plan + XPath cache | **partly — assertions are *never* cached** (51s→28s, 1.8×) | **`./midscene_run/cache` in your repo** |
| **Shortest (MIT)** | Playwright + Claude | Playwright test + NL | not documented | local, `.shortest/` (gitignored) |
| Hercules (AGPL) | Gherkin → agent; UI/API/security/a11y/visual | Gherkin features | not documented | local / Docker |
| auto-playwright (MIT, dormant) | `ai()` call inside Playwright | none | no | local |

### Money — the exact numbers

| company | published price | meter | whose model key |
|---|---|---|---|
| **us** | **$19/mo, one plan** | **tested PRs** | **customer's `ANTHROPIC_API_KEY`** |
| Momentic | $0 / **$125/mo** / custom | credits; ~10/run ⇒ **$0.125–0.1875/run**; mobile 8–15 credits/min | theirs (Anthropic + OpenAI + Azure are listed sub-processors) |
| QA Wolf | **1¢/AI credit + 15¢/runner-minute**; managed = quote | credits + minutes + tests-under-management | theirs |
| Bug0 | **$2,500/mo flat**, ≤500 flows, month-to-month | flows | theirs (included) |
| TesterArmy | **$99 / $299/mo** | test runs (250 / 1,000) ⇒ **$0.30–0.40/run** | theirs; **frontier models gated to $299** |
| Ranger | Free (5 reviews) / **$50/mo/user** | **seats** | theirs |
| TestDriver | **$20/mo/user** (10 test-hours, **$3.60/hr** over) / **from $600/mo** | testing hours | theirs; **BYO keys only at $600/mo** |
| Stably | $0 / **$60** / **$250**/mo | **$0.01/browser-minute + $0.30–15/M tokens**; ~$0.05–0.15/run | theirs, resold at ~list |
| Donobu | **$0** (BYO key) / **$34/mo** yearly / **from $5,500/mo** | credits; **export gated at $34, self-healing at $5,500** | **yours** |
| Reflect (SmartBear) | quote-only | credits (web 1 / mobile 5 / API 0.1) | theirs |
| Spur | **404 — no pricing page** | "test-run volume, not per seat" | theirs |
| qa.tech | quote-only | users / parallel / environments | theirs |
| heal.dev | **404 — no pricing page**; "free for open source" | — | — |
| Octomind (dead) | **$89 / $589 per month** (now MEASURED, see below) | test cases + cloud runs + **AI test creations** | theirs |
| **Midscene (MIT)** | **$0. No company, no account, no meter.** | — | **yours, direct to the provider** |
| **Shortest (MIT, dormant)** | **$0** | — | **yours (`ANTHROPIC_API_KEY`)** |
| Hercules (AGPL) | repo free; TestZeus plans **unverified** | — | yours |
| auto-playwright (MIT, dormant) | **$0** | — | yours |

**Five of fourteen publish no price at all.** Every company that pays the model bill meters the runs.
Every company that hands the key to the customer (Donobu, Passmark, us) does not need to.

### Setup friction — what stands between a stranger and their first verdict

| | account? | install | key |
|---|---|---|---|
| **us** | **no** | `npx smolanalytics test --url … --test "…"` | your Anthropic key |
| Passmark | no (GitHub) | `npm i passmark` + Playwright project + `.env` + **Redis for cache** | **your** Anthropic **and** Google keys |
| Donobu | no for Community | **download a desktop app** | your OpenAI/Anthropic/Gemini key |
| Momentic | yes | `npx @momentic/wizard@latest` | `MOMENTIC_API_KEY` |
| Ranger | yes | `curl … cli.ranger.net/install.sh | sh` + Claude Code plugin + skills + browser OAuth + a **shared org-wide logged-in profile** | `RANGER_CLI_TOKEN` |
| TestDriver | yes | `npx testdriverai init` or GitHub integration | console API key |
| QA Wolf | yes | `npm i -g @qawolf/cli`; env-id from the dashboard | `QAWOLF_API_KEY` |
| TesterArmy | yes | CLI + GitHub | theirs |
| Spur | demo call | URL + test credentials + **whitelist their static IPs** | theirs |
| Bug0 Managed | sales call | onboarding with a forward-deployed engineer | theirs |
| Reflect / qa.tech | demo call | — | theirs |
| **Midscene** | **no** | `npm i @midscene/web`, or a Chrome extension playground with no project at all | **your** model key |
| **Shortest** | **no** | `npx @antiwork/shortest init` | **your** `ANTHROPIC_API_KEY` |
| Hercules | no | Docker or `pip install testzeus-hercules` | yours |

### Traction — measured, not claimed

| | GitHub | npm/week | funding | headcount |
|---|---|---|---|---|
| Momentic | small repos (14★ max) | **255,105** (`momentic`) | $19.2M ($15M A, Nov 2025) | unverified |
| Bug0 / Passmark | **1,254★** / 182 forks / **2 commits in 30d** | **45,633** (`passmark`) | unverified | unverified |
| QA Wolf | 3,443★ (but a renamed 2019 repo) / 100 commits in 30d | 31,161 (`@qawolf/cli`), 471 (legacy `qawolf`) | **$56.1M** | ~187–230 (REPORTED) |
| TestDriver | 239★ / 46 open issues / no licence | 17,046 | unverified | Austin, small |
| Ranger | — | 1,441 (**152 versions since Jan**) | **$8.9M** (Jan 2025) | **25** (REPORTED) |
| vostride/agent-qa | **961★** in 3.5 months | — | none | solo-ish |
| Spur | — | none published | **$4.5M** (Apr 2025, YC S24) | small |
| Stably | none found | 5,320 | unverified | unverified |
| Donobu | — | 1,898 | unverified | unverified |
| heal.dev | 43★ (AGPL tracer) | **49** | unverified | small |
| ZeroStep | **org has 0 public repos** | 6,314 into a dead backend | dead | dead |
| Octomind | **all 13 repos archived** | — | €4.5M seed (Cherry) | ~15 named, dead |
| **Midscene** | **14,676★** / 1,131 forks / **98 commits in 30d** | **47,780** (`@midscene/web`) + 11,096 (`@midscene/cli`) | none — ByteDance web-infra (INFERRED) | n/a |
| Skyvern (adjacent) | **22,842★** / 2,146 forks, pushed today | — | YC S23 | — |
| **Shortest** | 5,666★ / 338 forks / **0 commits in 30d** | 8,898 | Antiwork (Gumroad) | dormant |
| Hercules | 1,128★, last push 2026-08-04 | PyPI 1.0.2 (2026-08-03) | unverified | unverified |
| auto-playwright | 846★, **last push 2025-07-08** | **13,207** | none | dormant |

**For scale, from the prior sweep:** `@playwright/mcp` does **5.6M downloads/week** and
`microsoft/playwright` does **83M**. The entire agent-first startup field, summed, is under 400K.

**And the free tier out-engineers the funded one.** Midscene alone — MIT, no company — does
**58,876 weekly downloads across its two main packages and 98 commits in thirty days**. That is more
commit activity than any commercial repo in this document except QA Wolf's, and QA Wolf's is a
renamed 2019 recorder. **The best-maintained agent-first browser-testing codebase on earth has no
pricing page.**

---

## 3. The three mechanisms in this field that nobody else has

Each of these is a *combination* — I have marked which individual pieces exist elsewhere, because
claiming novelty for a piece someone already ships is how a competitive doc becomes flattery.
**Mechanism 3 was narrowed in the second pass** after Midscene turned out to own half of it; the
original overclaim is left visible inside that section rather than deleted.

### 1. A fifth status — `stale` — that is never red, never green, and never silences

Every product here collapses "the recording stopped fitting the app" into one of two lies. Either it
is a **failure** (Momentic's default: *"every failed test still fails CI"*; QA Wolf's "broken test"
lands in the Investigation queue as a red flow), or it is **silently healed green** (Momentic's
`overrideExitCode: true` exits `0` on anything the classifier routes to *heal* or *warn*; Mabl's
auto-heal, per the prior sweep, retargets and moves on).

The taxonomies that exist are all richer than mine on *diagnosis* and poorer on *contract*:
Autonoma has **7 verdicts** with no fallback path; Momentic has cloud-managed categories with
heal/warn/fail actions; QA Wolf has flakes/bugs/broken; Ranger has verified/blocked/partially
verified; Canary has a perception cascade but no published status set. **Not one of them wires the
taxonomy to a documented exit-code contract that a stranger can read before signing up**, and not one
has a status whose entire definition is "this is not a fact about your app."

Ours: `passed` / `failed` / `flaky` (failed then passed from a clean page — exits 0, warns loudly) /
`stale` (the recording stopped fitting; never red, never worded as a failure; the agent re-checks the
*original sentence* and rewrites the recording) / `errored` (this runner could not run — never your
app). Exit codes: `0` nothing failed, `1` a test failed, `2` the runner could not finish. **A pipeline
that gates on `1` alone never reddens because our side had an outage.**

Two things make this defensible rather than cosmetic. First, healing regenerates **from the
sentence**, so intent cannot drift — the failure mode `ashgam` described on HN (*"Claude already
patching the test scripts instead of fixing the bugs to make the tests pass"*) is structurally
impossible when the spec is a human-written sentence and the artefact is regenerated from it, not
edited toward green. Autonoma reached the same conclusion from the other side and **reverts** a
rewrite that does not survive re-run. Second, `stale` is the only status in the field that admits the
tool does not know — and `pastescreenshot`'s HN question (*"whether the useful ones survive after the
PR is gone… that conversion rate feels closer to the real moat than the generation demo"*) is
precisely a question about how you handle staleness.

**Where this is not unique:** the *idea* of classifying failures is everywhere. Momentic's classifier
is more sophisticated than ours. The novelty is the contract, not the taxonomy.

### 2. Test-data teardown, with an identity that cannot reach a human

This one is genuinely empty, and I checked hard.

- **Momentic:** `llms.txt` covering ~120 doc pages has **zero** hits for teardown, cleanup, or data
  seeding. They *provision* identities — Momentic-hosted inboxes and phone numbers for OTP — and
  nothing deletes what the run created.
- **Passmark:** **zero** occurrences of "teardown" in the repo; 3 for "cleanup", none data-related.
  It ships `{{email.*}}` placeholders and an `emailsink` provider — again, provisioning, not cleanup.
- **TestDriver:** zero hits for clean/teardown/test data in its docs index.
- **QA Wolf:** two hits, both irrelevant — "ephemeral emulators that start from a clean state" and a
  CLI lifecycle hook that *"handle[s] setup, teardown, notifications, and file work"*. Not data.
- **TesterArmy:** *"each of our agents has access to its own inbox"* — provisioning again.
- **Midscene (checked 2026-08-25):** its full docs index has no teardown, cleanup or data-lifecycle
  page. The closest is the Test Runner overview naming *"business APIs, **test data setup utilities**,
  and browser resource managers"* — **setup utilities**, named as such, with no counterpart.
- **Shortest:** ships Mailosaur email validation — provisioning, again — and nothing that removes it.

**Extending the audit to the open-source tier did not find a counter-example; it found the same
asymmetry, stated in the same words.** Six independent teams have built a way for a test to *receive*
an identity and not one has built a way to *retire* one.

So the entire field has solved *"how does the agent receive the OTP"* and not one company has
addressed *"what happens to the 400 accounts your nightly suite created in staging this month, and
what happens the day someone points it at prod."*

Ours: every run carries an obviously-synthetic identity — `smoltest+mfz01abc@example.com`,
`smoltest_mfz01abc` — every value prefixed `smoltest` and carrying the same run id, so one
`LIKE 'smoltest%'` finds every row any run ever made in any column. The default domain is
**`example.com`, reserved by RFC 2606**: a test signup **can never land a "welcome!" in a real
inbox**. A production-looking URL is warned about first, and asked about when a human is at the
terminal (CI is told, never asked — *"a question nobody can see is a hung build"*).
`--teardown <url>` POSTs the identity to the customer's own endpoint **after every run, including
failed and errored ones, because the failed run is the likeliest to have left half an account
behind** — with the secret arriving as an env var rather than a flag so it never lands in shell
history or the command line CI prints at the top of every log. A teardown that fails is reported and
changes nothing: the verdict and the exit code were decided before it fired.

**Why nobody has it (INFERRED):** it is only expressible if the customer owns the app *and* the
runner. A vendor running tests in its own cloud against a URL cannot hand you a cleanup hook into
your database, and a vendor whose pitch is "no code access required" (Spur, qa.tech) has definitionally
opted out. This is the sharpest example of an advantage that comes from the *architecture* rather
than the model.

### 3. A replay that costs zero model calls *including the assertions*, from a file the customer owns

Three properties. Individually, each exists somewhere. **Together, nowhere.**

**Corrected 2026-08-25.** I originally wrote that the fourth property below belonged to nobody.
Midscene has it. The honest version is narrower and, I think, still sound.

- *Zero-model replay* — Momentic has it (>99% cached), TestDriver has it (perceptual-hash cache),
  Passmark has it (cache-first). **Not unique.**
- *Customer's own key* — Donobu has it, Passmark has it, Midscene and Shortest have it, TestDriver has
  it at $600/mo. **Not unique.**
- *Runs on the customer's own CI* — Momentic now, Passmark, Midscene, Shortest, Stably optionally.
  **Not unique.**
- *The replay artefact is a plain file in the customer's repo, portable and vendor-independent* —
  **Midscene has this** (`./midscene_run/cache/*.cache.yaml`, MIT). Nobody else does: Momentic's
  cache is a server-side backend and its snapshot escape hatch is org-locked (*"Snapshots can only be
  replayed by the organization that created them"*); TestDriver's cache is in their console;
  Passmark's is in **Redis**; QA Wolf, TesterArmy, Ranger, Spur, Bug0, Reflect and qa.tech are all
  cloud-held. **Among companies, unique. Among artefacts, not.**

**So what actually survives is the intersection, and it is one specific thing: a replay that costs
zero model calls *including the assertions*, from a file the customer owns.** Midscene owns the file
and cannot do the replay — *"**Never cache query results**: the query results like `aiBoolean`,
`aiQuery`, `aiAssert` will never be cached"* — because a pure-vision assertion has no cacheable
artefact to compare against. Momentic can do the replay and does not let you own the cache. The two
halves exist in the field; **they have never been in the same product**, and the reason is a genuine
architectural fork: you can cache an assertion only if you resolved it against a *structure* in the
first place. Our perception layer is what makes the economics possible, which is also the honest
answer to Midscene's critique of the accessibility tree — we are buying cacheable assertions with the
blind spots they name (`<canvas>`, icon-only controls, "does it look right").

The combination is a testable promise: **when the vendor dies, the suite still runs.** Octomind is
the proof of why that matters — a company that told its users the artefacts would persist and whose
DNS was gone in three months. And it is the exact objection HN raised twice unprompted (`Eridrus`:
*"not super excited about using some 3rd party SaaS as a critical part of my testing"*; `sbuccini` on
Meticulous, from the prior doc: *"proprietary tech that could be rendered worthless in an instant if
the company goes bust"*).

Add the distribution property that follows from it: **no account, no GitHub App, nothing committed,
no keys handed over, first verdict in sixty seconds from `npx`.** Every single company in this
document requires an account before the first verdict except Donobu's Community tier — and Donobu
requires downloading a desktop app and paywalls Playwright export.

---

## 4. The three failure patterns that repeat across the field

### Pattern 1 — everyone starts at authoring, discovers authoring was never the bottleneck, and the ones who cannot re-found themselves die

The evidence is unusually clean because the founders say it themselves.

Kosta Welke of Octomind, HN 2025-01-09, describing the whole company in three sentences: *"Let an LLM
agent take a look at your web page and generate the playwright code to test it. Running the test is
just running the deterministic playwright code. **Of course, the actual hard work is _maintaining_
end-to-end tests** so our agent can do that for you as well."* They knew. They built self-healing,
persistent traces (*"cut debugging time by 50%"*), auto-fix (*"'Auto-fix and maintenance is why I buy
Octomind' — Fabian Frank, CTO, BRM"*). They died anyway.

Momentic, in their own build-vs-buy page: *"**AI makes authoring tests cheaper. It does little for
maintenance, triage, or operation, which are the recurring costs.**"* Their entire product surface —
four-tier healing, classification, triage, quarantine, AI routing — is a company that has fully
relocated to the maintenance side.

The ones that did not relocate: **Magnitude** (best-received launch in the category, 179 points;
npm dead Feb 2026; now a local-model general agent), **ZeroStep** (`ai()` inside Playwright — an
authoring convenience; last publish Dec 2023, domain gone), **CamelQA** (now an inference reseller).
And every incumbent in the prior sweep reached the same place: Testim's smart locators, Mabl's
review-queue heals, Rainforest's human crowd all exist because *writing* the test was the easy part.

Concretely: **any roadmap hour spent on making authoring nicer is an hour spent becoming a company
that dies.** The scoreboard is maintenance, adjudication, and evidence.

### Pattern 2 — the vendor pays the model bill, so the price must be a meter, and the meter charges the customer for testing more thoroughly

Follow the money and every architecture in this field collapses into the same shape. If the vendor
runs the agent, the vendor buys the tokens and the browser minutes, so gross margin is a function of
how much the customer tests. So:

- Momentic: **credits**, ~$0.125–0.1875/run, mobile emulator time at 8–15 credits/minute.
- QA Wolf: **1¢/AI credit + 15¢/runner-minute** — you are billed for the browser being on.
- Stably: **$0.01/browser-minute + $0.30–15/M tokens**, and the plan price *equals* the included
  credits, so there is no software margin at all below $1k/mo.
- TesterArmy: **$0.30–0.40/run** — and `pranshuchittora` measured Playwright MCP at *"1M+ tokens for
  a test with ~20 steps"* and asked publicly whether they are default alive. They did not answer with
  a number; they answered with *"we've been doing quite a lot of context engineering."*
- TestDriver: **$3.60/testing-hour** overage.
- Reflect: mobile tests cost **5×** a web test in credits.
- Checkly (prior doc): *"**Flaky checks will increase your usage. Each retry counts as a check run.**"*

Three consequences, all observed:

**(a) The meter punishes the behaviour good testing requires.** Retries, parallelism, per-PR suites,
and re-running a flaky test to *diagnose* it are all billable events. The customer's incentive points
away from quality.

**(b) Model quality becomes a pricing tier.** TesterArmy lists *"Powered by the best frontier
models"* as a **Startup ($299) feature**. A Hobby customer at $99 is being tested by
`gemini-3-flash` and is not told. This is the purest expression of the conflict: the vendor's margin
is inversely proportional to the quality of your verdict.

**(c) Everyone drifts up-market until the price can carry a human.** Bug0 **$2,500/mo**; Donobu Teams
**from $5,500/mo**; QA Wolf **$100–200K ACV** with a forward-deployed team; Rainforest ~$94K/yr with a
paid human crowd. The category's stable equilibrium is a staffing business, and every self-serve tier
in it is a lead magnet.

The one structural escape is to not buy the tokens. Donobu, Passmark, Midscene, Shortest and we do
that; Donobu then paywalls the export and charges $5,500 for healing, and Passmark is FSL-licensed
bait for a $2,500/mo service. **Nobody has yet shipped BYO-key as the whole business rather than the
free tier** — and note the shape of the exceptions: the two products that are purely BYO-key,
Midscene and Shortest, **are not businesses at all.** One is a big company's open-source project with
no revenue line; the other is dormant. That is either the strongest evidence that BYO-key is the
right architecture and nobody has monetised it yet, or the strongest evidence that it cannot carry a
company. This document cannot settle which, and pretending otherwise would be the most expensive
mistake in it.

### Pattern 3 — the escape hatch is always advertised and never real, and the word "open source" is doing work it has not earned

Every company in this field sells portability. Not one of them ships it.

- **QA Wolf:** *"No vendor lock-in — export open-source Playwright anytime."* The flows live in their
  platform; the CLI's own quick start says *"You need a QA Wolf account… The `<env-id>` comes from the
  QA Wolf dashboard."*
- **Momentic:** tests really are YAML in your repo — and the **step cache is a server-side backend**,
  the **failure taxonomy is cloud-managed**, `MOMENTIC_API_KEY` is required for any run, and the
  snapshot escape hatch *"can only be replayed by the organization that created them."*
- **Passmark:** *"The open-source Playwright library"* — **FSL-1.1-ALv2**, which is source-available,
  not open source. Same move as Autonoma's **BUSL-1.1**, which MariaDB's own page states is not an
  open source licence (`AUTONOMA_TEARDOWN.md` §6).
- **Magnitude:** claimed *"100% open source"* and got corrected in public by `engfan`: *"stop saying
  100% open source when test plan generation and execution depend on non-open source AI components."*
- **TestDriver:** `testdriverai/testdriverai` has **no licence file at all** — 239 stars on code
  nobody has permission to use.
- **Donobu:** local-first, your keys, your machine — and **Playwright export is a paid feature**, so
  the one thing that would let you leave is the thing you must pay to have.
- **Octomind:** *"octomind.dev — including this blog — will stick around for a while yet. The writing
  isn't going anywhere."* **No A record, twelve weeks later.**

**The exception proves the point.** The three projects in this field that *are* genuinely OSI open
source — **Midscene (MIT), Shortest (MIT), auto-playwright (MIT)** — are the three with no pricing
page and no company depending on them. Every product that needed the *word* "open source" to sell
something reached for a licence that is not one (FSL, BUSL), no licence at all (TestDriver), or a
paywall on the export (Donobu). **The correlation is exact and it runs the wrong way for the buyer:
in this category, "open source" in the marketing copy predicts that it is not, and actual MIT
predicts that nobody is being paid to keep it alive.**

A closely related tell, worth naming — **and stated more carefully than I first wrote it.** My initial
claim was "nobody benchmarks." That is false: **Midscene publishes Pass@1/2/3 against AndroidWorld
(93.10% / 95.69% / 97.41%) and MobileWorld (78.63%, 92/117)**, with per-task reports, and Momentic
publishes reproducible latency numbers against Playwright with method and hardware stated (52ms /
6,354ms). The accurate claim is sharper:

**Nobody benchmarks the thing they sell.** Midscene measures *task completion on public mobile
GUI-agent benchmarks* — can the agent finish the job. Momentic measures *speed*. **Not one company in
this field publishes a number for verdict correctness on web regression** — the false-positive rate,
the false-negative rate, the flaky-vs-broken classification accuracy — which is the entire product.
And the free MIT project is the one doing more measurement than any funded competitor.

Meanwhile: TesterArmy, asked directly on HN, said *"we currently do not have any benchmarks."* Spur
claims *"80% fewer false positives"*, Ranger claims *"32% fewer bugs in production"*, QA Wolf
guarantees *"zero flakes"*, Rainforest and Mabl publish nothing — all with no method, no dataset, no
repo. In a category whose entire value proposition is **trust in a verdict**, the field has
collectively declined to measure whether its verdicts are right. That gap is a positioning
opportunity and a standing invitation to be embarrassed by anyone who does the work — and note that
"anyone" now includes an unfunded ByteDance side project that already built the habit.

*(Runner-up pattern, not in the top three but real: **nobody has an answer to prompt injection
through the page under test.** An ex-Octomind engineer says they hit it "constantly"; zero of the
doc sets I read mention it. An agent driving a browser against a page that contains attacker-
controlled text — a product review, a username, a support ticket — is a live vulnerability class in
every one of these products, ours included.)*

---

## 5. Where we lose, stated plainly

- **Midscene is free, MIT, better funded than us by accident, and shipping faster than anyone.**
  14,676 stars, 98 commits in thirty days, 58K weekly downloads, cross-platform to iOS/Android/
  HarmonyOS/desktop/`<canvas>`, published benchmarks, and a data-privacy story (your key, direct to
  the provider, no middleman) that is strictly cleaner than any vendor's. It has no verdict layer, no
  history and no zero-model replay — but it has no revenue target either, so it will never need to
  charge us out of the market. If they ever ship an adjudicator, the free option becomes better than
  the paid one. **Their critique of the accessibility tree is also correct on its merits** for
  `<canvas>`, icon-only controls and visual regressions, and we have no answer to it today.
- **Shortest already built our architecture and stopped.** MIT, Playwright, natural language,
  `ANTHROPIC_API_KEY`, `npx`, your CI — from Sahil Lavingia's Antiwork, 5,666 stars, and dormant since
  May. It is the closest thing to a natural experiment on our thesis, and the result was ambiguous at
  best: adoption without retention, then silence. Anyone evaluating us will find it.
- **Momentic is better engineering than us on maintenance.** Multi-signal caching weighted by the
  step's own wording, a four-tier escalation ladder, AI-routed repair PRs attributed to the commit
  author from git history. If they add a portable local cache and a BYO-key mode, our third
  differentiator evaporates. They have $19.2M and Notion.
- **Passmark is one licence change from being the free version of our runner.** ARIA-first, BYO-key,
  Playwright-native, customer's CI, 1,254 stars, 45K npm/week. It is FSL and at 2 commits/month right
  now — but that is a decision, not a moat.
- **The buyer may not exist.** Octomind's *"we didn't find the market validation we needed"* is the
  same conclusion three HN commenters reached unprompted on the TesterArmy thread: with a coding
  agent in the loop, developers believe they can already do this. QA Wolf, the only clear revenue
  success, sells **people**. That is the strongest single piece of evidence in this document and it
  points away from a $19/mo self-serve tool.
- **$19/mo may be the wrong number in the wrong direction.** The stable prices in this field are $0
  (Passmark, Donobu Community, Playwright's own free agents) and $2,500+ (Bug0, Donobu Teams, QA Wolf).
  The $19–$299 band is where Octomind, Magnitude, ZeroStep and CamelQA all died. Being cheapest is
  not a position when the free option is Microsoft's.
- **Our benchmark is one flow on one app.** "8.0s agented vs 1.4s replayed" is a single measured
  data point. Momentic published a method, hardware, and three execution modes. If we are going to
  claim the replay economics, we need to earn them the way they did.

## 6. Evidence gaps in this document

- **G2 and TrustRadius return 403 to me.** Every G2 rating and review count here is REPORTED via
  search snippets. Capterra's QA Wolf cons are verbatim but arrive through WebFetch's summariser, not
  a raw page I parsed — treat them as REPORTED-verbatim, not MEASURED.
- **Reddit is unreachable** (`reddit.com/*.json` returns non-JSON to this environment; DuckDuckGo HTML
  returns empty). There is **no r/QualityAssurance or r/webdev primary material in this document.**
  HN carried the load instead, and HN over-represents skeptics.
- **Funding, headcount and ARR figures are all REPORTED** (Tracxn, PitchBook, Sacra, Crunchbase,
  press releases) except Ranger's $8.9M and Momentic's $15M, which are on the companies' own blogs.
  Sacra's QA Wolf ARR is a model, not an audit.
- **The `@qawolf/cli` August download spike is unexplained.** 14× in one month is as consistent with
  their own CI as with adoption; I could not separate them.
- **Stably's perception layer, Spur's pricing, qa.tech's pricing, heal.dev's pricing, Donobu's
  funding and headcount** — all attempted, all **unverified**.
- ~~Octomind's $89/$589 pricing is REPORTED only.~~ **Closed 2026-08-25:** recovered the real pricing
  page from the Wayback raw-HTML endpoint (snapshot `20260606112446`) — now **MEASURED**, see the
  Octomind pricing block in §0. Its €4.5M Cherry seed remains REPORTED.
- **TestZeus/Hercules pricing is unverified** — `testzeus.com/pricing` is a Framer page that renders
  its numbers client-side and yielded nothing to extraction.
- **Reddit and G2 were re-attempted on 2026-08-25** with a browser user-agent over raw HTTP, not just
  WebFetch: `old.reddit.com/r/QualityAssurance/search.json` returned an **empty body**, and
  `g2.com/products/qa-wolf/reviews` returned **HTTP 403**. Both gaps stand; neither is worked around
  anywhere in this document.

---

## Source index

**MEASURED — fetched, queried, or resolved 2026-08-25 unless noted**
qawolf.com/pricing · docs.qawolf.com/llms.txt + Diagnose-the-cause-of-a-failing-flow.md ·
github.com/qawolf/cli (API: repo, README, commits) ·
momentic.ai/pricing · momentic.ai/docs/llms.txt · .../get-started/cloud-deprecation.md ·
.../reliability/step-cache.md · .../reliability/auto-maintenance.md ·
.../guides/auto-heal/in-flow-classification.md · .../running-tests/performance.md ·
.../comparisons/build-vs-buy.md · .../running-tests/ci/github-actions.md ·
.../cli-reference/momentic/commands/snapshot.md · .../account/security.md ·
docs.testdriver.ai/llms.txt + /v7/caching.md · testdriver.ai/pricing ·
ranger.net/pricing · ranger.net/post/ranger-raises-8-9m-to-find-bugs-faster ·
docs.ranger.net/llms.txt + llms-small.txt ·
bug0.com/pricing · bug0.com/llms.txt · github.com/bug0inc/passmark (API: repo, README, commits,
contributors, code search) ·
stably.ai + stably.ai/pricing · spurtest.com (+ /pricing → 404) ·
heal.dev (+ /pricing → 404) · github.com/heal-dev/heal-playwright-tracer (API: repo, README) ·
donobu.com + donobu.com/pricing · tester.army/pricing · qa.tech/pricing · revyl.com ·
reflect.run + reflect.run/pricing · camelqa.com → camelai.com (301) ·
GitHub API: orgs OctoMind-dev / qawolf / momentic-ai / testdriverai / heal-dev / zerostep-ai;
repos magnitudedev/magnitude, vostride/agent-qa ·
npm registry + downloads API: momentic, qawolf, @qawolf/cli, testdriverai, passmark, stably,
donobu, magnitude-test, @magnitudedev/cli, @momentic/wizard, @ranger-testing/ranger-cli,
@zerostep/playwright, @heal-dev/heal-playwright-tracer ·
`dig` on octomind.dev, zerostep.com, heal.dev, donobu.com, camelqa.com ·
web.archive.org CDX + snapshot 20260519031411 of octomind.dev and
octomind.dev/blog/a-letter-to-our-users-customers-and-readers/ ·
HN Algolia API items 48586299 (TesterArmy Launch HN), 47441629 (Canary Launch HN),
43796003 (Magnitude Show HN), plus comment searches for octomind, QA Wolf, TestDriver.ai,
Reflect.run, ZeroStep ·
smolanalytics `cli/README.md`.

**MEASURED — added in the 2026-08-25 second pass**
web.archive.org **raw-HTML endpoint** (`/web/<ts>id_/<url>`) snapshot `20260606112446` of
octomind.dev/pricing — the full plan table, recovered where WebFetch had failed ·
github.com/web-infra-dev/midscene (API: repo, README, commits since 2026-07-26) ·
midscenejs.com/llms.txt + /caching.md + /data-privacy.md + /mcp.md + /android-world-benchmark-report.md ·
github.com/antiwork/shortest (API: repo, README, commit history) ·
github.com/test-zeus-ai/testzeus-hercules (API: repo, README) + pypi.org/pypi/testzeus-hercules/json ·
github.com/lucgagan/auto-playwright · github.com/Skyvern-AI/skyvern ·
npm downloads API: @midscene/web, @midscene/cli, @antiwork/shortest, auto-playwright ·
HN Algolia story searches: midscene, "shortest antiwork", skyvern ·
**negative results, both re-attempted over raw HTTP with a browser user-agent:**
old.reddit.com/r/QualityAssurance/search.json → **empty body**;
g2.com/products/qa-wolf/reviews → **HTTP 403**.

**REPORTED — via search, unfetchable or secondary, never load-bearing alone**
Crunchbase / PitchBook / Tracxn (funding, headcount, acquisitions) · Sacra (QA Wolf ARR) ·
smartbear.com press release (Reflect acquisition, 2024-01-25) ·
newsfilecorp / TechCrunch / SiliconANGLE (Momentic $15M) ·
seedtable / mrweb (Spur $4.5M) · finsmes (note: the $8.4M "Ranger AI" is a *different* company —
industrial ops — do not conflate) ·
G2 (blocked, 403) and Capterra QA Wolf review pages · stackpick/testguild/test-lab.ai
(Octomind's former $89/$589 pricing).

**REFUSED as sources** — bug0.com/knowledge-base/*, getautonoma.com/blog/*, testsprite,
drizz.dev, testeragents.com and similar: these are competitors' content marketing about each other,
and in this category they are the single largest source of confidently-wrong pricing on the web.
