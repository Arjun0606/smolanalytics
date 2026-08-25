# E2E testing: incumbents, adjacents, and the free tier that matters most

Date of sweep: 2026-08-24; load-bearing MEASURED claims re-verified 2026-08-25 (Checkly, Cypress, Mabl, Meticulous, Playwright agents, Datadog Bits, npm/GitHub counts — all reproduced exactly). Companion to `AUTONOMA_TEARDOWN.md` (not repeated here; cross-referenced).

Evidence labels, used on every load-bearing claim:
- **MEASURED** — I fetched the page / ran the query on 2026-08-24 and am quoting it.
- **CLAIMED** — the vendor's own marketing or docs say it.
- **REPORTED** — a third party (review site, Vendr, a competitor's teardown, an HN commenter) says it; I could not verify independently. Never treated as fact.
- **INFERRED** — my read from the above.

The field splits into three tiers and they fail in three different ways:

1. **Enterprise quote-only AI-QA platforms** (Mabl, Testim/Tricentis, Rainforest) — sell "AI replaces QA" to teams with QA budgets. None publishes a price. All are structurally unable to serve a solo builder.
2. **Dev-tool metered platforms** (Checkly, Datadog, Cypress Cloud, BrowserStack, Sauce Labs) — publish prices, meter runs/results/parallels, and their meters punish exactly the behaviours good testing needs (retries, parallelism, per-PR suites).
3. **The free adjacent** (Playwright MCP + Playwright's own free planner/generator/healer agents + Stagehand + browser-use + any coding agent) — free, enormous distribution, and genuinely good at *writing* tests. Fails at everything after the test is written. That gap is the product.

---

## Tier 1 — enterprise quote-only

### Mabl

**What it does (CLAIMED):** low-code cloud test platform: browser + mobile + API + performance + accessibility testing, "agentic test runtime recovery", "Generative AI auto-healing", "intelligent assertions", "automation that builds itself, runs itself, and recovers itself" (mabl.com/pricing, seen 2026-08-24).

**Pricing (MEASURED page, no numbers on it):** quote-only. The pricing page publishes zero dollar figures. Model: credits consumed per **cloud** test run, "starting point of 500 credits per month", local/CI runs unlimited, 14-day trial. (https://www.mabl.com/pricing)
**REPORTED price points:** ~$450–600/mo Starter, $1,200–3,000/mo Growth, **$40K/yr enterprise floor** per Vendr deal data (vendr.com/marketplace/mabl via search, 2026-08-24). Unverified; Vendr aggregates real contracts so directionally credible.

**Who buys (INFERRED):** mid-market/enterprise QA and quality-engineering teams — the pricing page sells Customer Success Managers and Technical Account Manager add-ons, which only exist for six-figure relationships.

**AI: marketing vs mechanism.** Marketing says self-building, self-recovering automation. Their own help-centre docs (help.mabl.com "How auto-heal works", "Reviewing auto-heals", seen via search 2026-08-24) describe attribute-weighted element re-matching with a **human review queue for heals** — i.e. self-healing that generates homework. REPORTED user evidence: a QA engineer "turned off self-heal on most use cases as it kept giving false positives… more of an optional tool and not something that works everywhere"; a G2 reviewer: "When it did work, it took so long to run that it slowed down our entire development process. This is not a fast, custom QA solution. It is a highly priced, overly complicated solution that actually slows teams down." (via drizz.dev/testsigma round-ups of G2 reviews — secondary, but verbatim-quoted reviews.) G2 aggregate is still 4.5/5 (REPORTED) — the buyers who stay like the ease-of-use.

**The structural complaint:** auto-heal that silently retargets the wrong element **hides real regressions** — the exact failure our stale-vs-failed split and Autonoma's revert-the-rewrite rule both exist to prevent. Mabl's own docs concede heals need review.

### Testim (Tricentis)

**What it does (CLAIMED):** "AI-powered smart locators for increased stability", agentic natural-language test creation, now split into Testim Web / Mobile / **Salesforce** (testim.io/pricing, MEASURED 2026-08-24).

**Pricing (MEASURED page):** quote-only, all three tiers are "Contact Us". A free **Community plan exists, one per organization**, granted after trial. Parallels are the meter on mobile ("platform purchase including one parallel test; additional parallel tests… may be purchased").
**REPORTED:** enterprise contracts "$30,000–50,000+/yr" post-Tricentis-acquisition (bug0.com, testeragents.com — both competitors' content; unverified).

**Who buys (INFERRED):** Tricentis's install base. The Salesforce-first product split says it plainly: they are being aimed at Salesforce admins and enterprise suites, not web startups. Acquired by Tricentis early 2022; "the purchase decision is now a Tricentis decision" (REPORTED, testeragents.com).

**AI vs marketing (REPORTED user evidence, via TrustRadius/G2 aggregations):** smart-locator "stability issues, particularly across different development branches", "difficult to figure out the reason behind the failure of certain tests because AI-based locators might play a role", high memory/CPU. The signature failure of learned locators: when the test fails **you cannot explain why**, which is precisely the verdict problem.

### Rainforest QA

**What it does (CLAIMED):** no-code test editor where steps interact with **the visual layer via pixel-matching, not the DOM** ("just like a real user would, rather than interacting with the underlying code" — rainforestqa.com/no-code-test-automation via search 2026-08-24); AI for execution/self-healing/test-plan suggestion; plus a **paid human crowd** that runs the same plain-English tests on real devices with ~30-minute turnaround.

**Pricing (MEASURED attempt):** rainforestqa.com/pricing returned "Rainforest is not available in your region" + book-a-demo — quote-only AND geo-gated (seen 2026-08-24 from IN). **REPORTED:** $1,500–3,000/mo for <500 runs/mo, $1,500–8,000+/mo typical, ~$94K/yr average contract (Vendr buyer guide via search). Unverified.

**Who buys (INFERRED):** teams with **no QA engineers at all** — PMs and support staff writing tests — who can pay $20K+/yr. The human crowd is the actual moat and the actual COGS; the "AI-accelerated" layer sits on top.

**Churn evidence (REPORTED, G2 aggregation via bug0):** "false positives, confusing troubleshooting UI, slow execution on large suites… costs grew faster than expected, crowdtesting across multiple browsers multiplying hourly charges." Pixel-matching is the most change-sensitive perception layer in the field — every visual tweak is a diff — which is why they need both self-healing AND humans.

**Tier-1 conclusion (INFERRED):** nobody in this tier can quote a price on a web page, everyone meters cloud execution, and every AI feature ships with a human-review valve. They compete for QA *budgets*. A $19/mo product that runs on the customer's own CI is not a cheaper version of these — it is a different species, and they cannot follow it down without destroying their own unit economics (their COGS is cloud browsers + crowd humans; ours is the customer's CI minutes and the customer's own model key).

---

## Meticulous — the closest architecture to ours

**What it does (CLAIMED, meticulous.ai + /how-it-works, MEASURED 2026-08-24):** a script tag records real user sessions in dev/staging/preview ("captures all the user interactions, like click events, scroll events"). AI selects/generates a test suite from those sessions by code-branch coverage. In CI it **replays** the sessions against the new frontend build inside a modified Chromium: "The browser is augmented to be deterministic in order to eliminate flakes — Meticulous is the only product which has this." Crucially: "By default Meticulous mocks out any network requests to your backend… replaying the original recorded responses."

**Verdict mechanism (CLAIMED):** "captures a visual snapshot after dispatching each event, and compares these snapshots to those generated for the base commit" — i.e. **the verdict is a screenshot diff reviewed by a human in the PR**. Claims: "Never write, fix or maintain a test again", "eliminates flakes", replay of "thousands of screens in under two minutes", "over 100 organizations" incl. Dropbox, Notion, Brex (logo wall, CLAIMED).

**Pricing:** not on the site; REPORTED custom/sales-led, free for open-source (SaaSworthy via search). At 2022 launch: tiers around 20 free sessions / ~$100 plans with ~1,000 sessions (HN thread, dated).

**Where it actually breaks (PRIMARY, HN launch thread news.ycombinator.com/item?id=31236066, fetched 2026-08-24):**
- Volume economics: user *kall*: "20 sessions is just a trial and 1000 sessions is very careful use" — 50 scenarios × 3 deploys/day ≈ 15,000 replays/mo.
- Lock-in: *sbuccini*: "I would be very concerned about building up a large suite of tests for my most critical flows on proprietary tech that could be rendered worthless in an instant if the company goes bust."
- The founder's own concession: "if your API significantly changes, you will need to record a new set of sessions to test against."
- Protocol gaps: no WebSocket/SSE at launch (*Klaster_1*).

**The architectural difference that matters (INFERRED, important):** Meticulous replays **with the backend mocked**. It is a frontend visual-regression tool — it can never catch "the API broke", "the signup email never sends", "the order didn't persist". Our replay hits the **real running app** (real backend, real database, synthetic identity, teardown). Their determinism comes from freezing the world; ours comes from proof-carrying steps against a live world, with the agent as the fallback when the world moved. Also: their unit of maintenance is re-recording *user sessions* (needs real users doing the flow again); ours is re-recording from a *sentence* (the agent regenerates it alone). Same word — "replay" — opposite trust models. Do not let a landing page conflate us.

---

## Tier 2 — metered dev-tools

### Checkly

**Pricing (MEASURED, checklyhq.com/pricing 2026-08-24):** Hobby **$0** (1,000 browser check runs + 10,000 API check runs/mo, hard-capped); Starter **$24/mo** (3,000 browser); Team **$64/mo** (12,000 browser); Enterprise custom. Overage: **$6.50/1k browser runs** (Starter), $6.25/1k (Team); API $2.60–2.50/10k. AI: "**AI Root Cause Analysis**" metered at 10/50/150 invocations per month by plan; "Automated RCA" (auto-trigger on failure) is Team+.

**What it is:** Playwright-based **synthetic monitoring** — scheduled probes of production, alerting, uptime. Monitoring-as-code done well; the honest one of the bunch (public prices, Playwright-native, dev-first).

**What its AI does vs says:** RCA summarisation of a failed check, metered per invocation. It does not write, maintain, or adjudicate tests.

**Complaint evidence (REPORTED, G2/Capterra aggregations via cubeapm 2026-08-24):** "pricing can feel rigid or expensive when teams fall between plan limits"; "rigid usage quotas… don't allow users to execute their entire suite on each branch or staging deployment"; and the structural one — "**Flaky checks will increase your usage. Each retry counts as a check run.**" The meter charges you for your own flake. On our model retries run on the customer's CI and cost nothing; flaky is a *verdict*, not a billable event (INFERRED contrast, MEASURED on our side — five-status taxonomy in cli/README.md).

**Overlap with us (INFERRED):** small — Checkly is prod monitoring on a schedule; we are per-PR verdicts. But they prove a $24–64/mo self-serve price point sustains a real company in this exact buyer pool, and their own blog ("The real costs of Synthetics monitoring: Datadog", checklyhq.com/blog/how-to-spend-ten-grand-12-bucks-at-a-time/) is the playbook for attacking a metered incumbent from below.

### Datadog Synthetics + Bits AI testing

**Pricing (REPORTED, converging sources incl. Datadog's pricing page via search + Checkly's teardown, 2026-08-24):** API tests **$5/10k runs**, browser tests **$12/1k runs** annual-commit ("$15–18/1k" on-demand). Datadog docs (MEASURED) confirm the billing units (per-10k API, per-1k browser) but keep dollar figures off the docs page. Cost multiplies by run frequency × browser × location — the Checkly teardown's point: a 5-minute, 3-location, 2-browser check is ~26k runs/mo *per journey*.

**New AI (MEASURED, datadoghq.com/blog/dash-2026-new-feature-roundup-keynote/, fetched 2026-08-24):**
- **Bits Testing Agent** — *Preview*: "explores applications autonomously… generates runnable test suites from URLs or natural language goals"; "goal-based tests let you define an intended outcome rather than a fixed sequence of steps, so tests adapt instead of break"; scheduled explorations maintain coverage.
- **Bits Release** — *Preview*: "analyzes the intended impact of the change, generates a validation plan, runs end-to-end checks in staging, and monitors the production rollout."

**Read (INFERRED):** this is the most serious incumbent AI move in the field — goal-based tests are conceptually identical to our sentence-is-the-test. But it is (a) Preview, (b) priced on the Datadog meter that already bills $12/1k browser runs, (c) sold inside a platform whose buyer is the VP of infrastructure, not the solo builder, and (d) Datadog's own pricing horror stories are the best-documented in SaaS. Datadog will own "AI testing for teams already paying Datadog $100K/yr". It will not come down-market; it never has with any product.

### Cypress Cloud

**Pricing (MEASURED, cypress.io/pricing 2026-08-24):** Starter **free, 500 test results/mo**; Team "**starting at $67/mo** billed annually at $799" (120k results/yr); Business "**$267/mo**" ($3,199/yr, 120k results/yr + advanced features); Enterprise custom (1.8M/yr). Overage **$6/1k results** (Team), $5/1k (Business). AI: **`cy.prompt` test generation**, metered — 100/mo free, 9k/yr Team, 24k/yr Business. UI Coverage and Cypress Accessibility are *separately trialled premium add-ons*.

**The meter's known failure (REPORTED, well documented):** it bills per `it()` block recorded, so a 300-test suite × 40 PRs/day ≈ 150k+ results/mo, and **parallelization doesn't reduce the count** — teams "add --parallel, celebrate faster builds, then open the billing page." History of hostility to escape routes: Cypress 13 **blocked plugins used by Sorry-Cypress/Currents**, the OSS/cheap dashboard alternatives (alternativeto.net news, Oct 2023; sorry-cypress README redirects to currents.dev — MEASURED via search 2026-08-24). BigBinary's "Why we switched from Cypress to Playwright" is emblematic of the wider framework drift toward Playwright.

**Read (INFERRED):** Cypress Cloud monetises a framework whose mindshare is bleeding to Playwright; its AI is a metered autocomplete inside that framework. Threat to us: low. Lesson: never meter a unit the customer can't predict from their own behaviour (results ≠ runs), and never fight your own users' exits.

### BrowserStack

**Pricing (MEASURED, browserstack.com/pricing 2026-08-24):** Live $29–39/mo individual, Team $150–375/mo (5-user min); **Automate $59/mo (Chrome-only) to $225/mo per parallel** — "Desktop & Mobile Pro… includes AI agents". Freelancer $12.50/mo.
**AI (CLAIMED, press release + docs via search 2026-08-24):** a "suite of AI agents" — Test Case Generator (**10 PRD uploads/user/mo** — a meter on *reading your requirements docs*), Low-Code Authoring Agent ("converts test cases into low-code automated tests, up to 10x faster"), Self-Healing Agent (Low Code Automation Pro+ only); low-code AI generation "currently in beta" per their own docs.
**Complaints (REPORTED, G2/review aggregations):** "sessions can feel a bit slow", "real device sessions drop, builds fail to upload, device availability varies by time of day", "high cost for small teams".

### Sauce Labs

**Pricing (MEASURED, saucelabs.com/pricing 2026-08-24):** Virtual Cloud **$149/mo annual** ($199 monthly), Real Device **$199/mo annual** ($249 monthly), both 1 parallel, unlimited minutes/users. AI: "Sauce AI Test Authoring Agent" and "Sauce AI Insights Agent" — **enterprise-tier only**, no public price.
**Complaints (REPORTED):** slow screen response, virtual-device boot issues, support delays.

**Tier-2 conclusion (INFERRED):** BrowserStack and Sauce sell *browser infrastructure* — a business that agent-driven local browsers and Playwright's own container images erode from below. Their AI agents are upsell attachments gated to top plans. None of the five adjudicates. Every one of them meters a unit (runs, results, parallels, PRD uploads) that grows when the customer tests *more thoroughly* — the incentive points away from quality. Our meter (tested PRs) grows only when the customer ships more, which is the one axis they're happy to pay along.

---

## Tier 3 — the free adjacent. This is the real competitor.

**Scale, MEASURED 2026-08-24 (GitHub API + npm registry API):**

| thing | stars | npm weekly downloads | licence |
|---|---|---|---|
| microsoft/playwright | 95,056 | 83,342,096 (`playwright`) | Apache-2.0 |
| **microsoft/playwright-mcp** | 36,424 | **5,618,931** (`@playwright/mcp`) | Apache-2.0 |
| browserbase/stagehand | 24,042 | 1,433,360 | MIT |
| browser-use/browser-use | 110,332 | (python) | MIT |
| microsoft/playwright-cli | 12,817 | — | Apache-2.0 |

5.6M weekly downloads of the MCP server means "my coding agent can drive a browser" is already the default developer condition, not an early-adopter trick.

**What each actually is:**

- **Playwright MCP** (MEASURED, README fetched via GitHub API 2026-08-24): MCP server exposing Playwright "through structured accessibility snapshots, bypassing the need for screenshots" — the same perception layer our runner uses. Microsoft's own README now *steers coding agents away from it*: "Modern coding agents increasingly favor CLI-based workflows exposed as SKILLs over MCP because CLI invocations are more token-efficient… MCP remains relevant for… exploratory automation, self-healing tests, or long-running autonomous workflows."
- **Playwright CLI + skills** (MEASURED, README): `playwright-cli install --skills` gives Claude Code/Copilot purpose-built browser commands; "Point your agent at the CLI and let it cook."
- **Playwright Test Agents** (MEASURED, playwright.dev/docs/test-agents fetched 2026-08-24): **official, free** 🎭 Planner ("explores the app and produces a Markdown test plan"), 🎭 Generator (turns the plan into spec files, "verifying selectors and assertions as it works"), 🎭 Healer (on failure it "replays the failing steps", "inspects the current UI to locate equivalent elements or flows", and "suggests a patch" before re-running — a suggest-and-rerun loop, not a silent rewrite), installed via `npx playwright init-agents --loop=` into Claude Code/VS Code/Codex/OpenCode. **Microsoft has shipped free agent definitions for the whole authoring loop, including healing.** This is the single most under-priced threat in this document.
- **Stagehand** (MEASURED, README): "Playwright was built for testing, Stagehand is built for agents" — act/extract/observe SDK, self-healing actions, token-trimmed accessibility tree; explicitly *not* positioned as a test runner. Browserbase (MEASURED pricing 2026-08-24): Free / $20 / $99/mo, metered per browser-hour ($0.12→$0.10 overage) — infra rent for agents' browsers.
- **browser-use** (MEASURED repo + cloud pricing 2026-08-24): 110K-star Python agent library; cloud at $0.02/browser-hour + token pass-through, plans $29/$299/$999. General web-agent tasks, not testing; no verdicts, no CI story.

### The key question: your coding agent can drive Playwright MCP for free — why pay $19/mo?

Honest first: **for authoring, the free path is genuinely good and getting better.** An agent with MCP inspecting the live DOM writes reasonable Playwright. If the question were "who writes the test file", we lose. The question is what happens on run #2 through run #2,000. Concrete failure modes, each sourced:

1. **Per-run token cost is real money and Microsoft says so.** A reported measurement: **"Playwright MCP burns 114K tokens per test; the new CLI uses 27K"** (scrolltest.medium.com, seen 2026-08-24 — REPORTED, single practitioner, directionally consistent with Microsoft's own README rationale for CLI-over-MCP). At Claude pricing, a 30-test suite through MCP on every PR is dollars per push, forever. Our runner's replay executes **zero model calls** (MEASURED in our README: one flow 8.0s agented vs 1.4s replayed); the model is consulted only when a recording goes stale.

2. **An LLM in the verdict path makes the merge gate non-deterministic.** "LLMs don't always follow the same path through a flow across runs… by the time you're investigating the failure, the agent's context is full of old page states" (testdino.com + practitioner consensus, REPORTED). A gate that can flip verdicts with zero code change trains the team to ignore it — the same death as flaky Selenium, at higher cost. The practitioner consensus is explicit: **"avoid using MCP for full regression suites in CI… full regression stays in traditional Playwright scripts where there's no per-step LLM cost."** So the free path collapses back to a static .spec.ts suite — which reintroduces every maintenance problem AI testing was supposed to remove.

3. **And agent-written .spec.ts rots in a specific, documented way.** "An agent prompted with 'write an E2E test for checkout' hallucinates selectors from training data: `.btn-primary`, `#submit`, an XPath three divs deep… green on the first run because the agent never actually ran it against your DOM, red the moment CI does" (qaby.ai, REPORTED). And the deeper one: "LLM-driven fixes focus on getting a test green… Generated tests that nobody understands are not an asset. They are deferred technical debt with a friendly face." Playwright's own free Healer agent has no rule against blunting an assertion to make a test pass — Autonoma reverts rewrites that don't survive re-run for exactly this reason (see AUTONOMA_TEARDOWN.md §1), and our stale-recording path re-checks the *original sentence*, so intent can't drift: the sentence is the spec, and healing regenerates *from the spec*, never from "make it green".

4. **No verdict taxonomy — and the taxonomy is the product.** The DIY path's output is an agent transcript ending "the flow appears to work". There is no passed/failed/**stale**/**flaky**/errored split, no flaky-vs-broken, no failing-since, no evidence bundle on failure, no PR comment edited in place. Autonoma's teardown found the same thing from the other direction: the defensible asset in this category is the **adjudicator**, not the actor — and the free stack ships no adjudicator at all. When a run fails at 2am, "which of these five things happened" is the entire value; a transcript is homework.

5. **CI is where the free path quietly dies.** Running an agent per-PR means: a model API key in CI secrets with an unbounded bill, non-deterministic run times against CI timeouts, interactive prompts that hang headless builds, and hand-rolled artifact capture. Every team gets to build retries, evidence, exit-code discipline, and the PR comment themselves — per repo. (INFERRED from the mechanics; the "keep MCP out of CI" consensus above is the field independently reaching the same conclusion.)

6. **Nobody's agent has a data-safety policy.** An MCP-driven agent told to "test signup" will happily sign up real-looking emails against whatever URL it's given, and nothing cleans up after it. Our runner ships synthetic `smoltest+runid@example.com` identities (RFC-2606 domain — can never email a human), warns-and-asks on production-looking URLs, and POSTs the identity to a `--teardown` endpoint after every run *including failures* (MEASURED, our README). This is table-stakes safety the free stack simply does not have, and it's the difference between "cool demo" and "allowed to touch staging".

7. **Retries under a meter are self-harm; retries on your own CI are free.** Checkly bills each retry as a check run; token-metered agents bill each retry in dollars. Our retry-from-clean-page costs the customer nothing and *produces information* (the flaky verdict).

**The honest boundary (INFERRED):** a solo dev with three critical flows, an existing Claude Max plan, and tolerance for wiring will get 70% of this free, and some will. The $19 buys the other 30%: the replay economics, the five verdicts, the flaky/failing-since history, the PR ledger, the teardown safety — i.e. everything between "an agent can do a test" and "a test system I never think about". The pitch is not "better than your agent"; it's "your agent writes the sentence, we make it a permanent, adjudicated, zero-token regression gate". Fighting the free tier is unwinnable; *completing* it is the position — same shape as our instrumentation half (we write into their PostHog, we don't replace it).

---

## Where we lose / what would kill us (no flattery)

- **Microsoft decides to finish the job.** Planner/Generator/Healer + playwright-cli skills + an official GitHub Action with a verdict comment would be our product, free, from the vendor of the runtime we depend on. Mitigation is speed and the parts Microsoft won't ship (hosted history, flaky-vs-broken across runs, teardown policy, the analytics tie-in) — but be clear-eyed: they've shipped three of the five layers in ten months (MCP 2025-03, agents 2025-10, CLI skills 2026).
- **Datadog's Bits Testing Agent goal-based tests** are our sentence-tests with a $50B distribution machine. Different buyer today; watch whether it escapes Preview and the meter.
- **Meticulous owns "zero-maintenance" mindshare with Dropbox/Notion logos.** If they un-mock the backend or we let anyone describe us as "like Meticulous", we inherit their ceiling. The one-line separation: *they replay recordings of users against a mocked world; we replay proofs of intent against the real one.*
- **The category's graveyard is authoring tools.** Every tier-1 vendor discovered that writing tests was never the bottleneck — trusting results is. Any roadmap hour spent making authoring fancier (instead of verdicts, history, and safety harder) is an hour spent becoming Testim.
- **Evidence gaps in this doc:** all review-site quotes are REPORTED (G2/TrustRadius block fetching — 403s on 2026-08-24); Vendr/bug0/testeragents price points are unverified and two of those are competitors' content marketing; the 114K-token figure is one practitioner's measurement. None of these is load-bearing alone; the structural conclusions stand on the MEASURED pricing pages and READMEs.

## Source index (fetched/queried 2026-08-24 unless noted)

MEASURED: mabl.com/pricing · testim.io/pricing · rainforestqa.com/pricing (geo-blocked page) · meticulous.ai + /how-it-works · checklyhq.com/pricing · cypress.io/pricing · browserstack.com/pricing · saucelabs.com/pricing · browserbase.com/pricing · browser-use.com/pricing · datadoghq.com/blog/dash-2026-new-feature-roundup-keynote · docs.datadoghq.com/account_management/billing/pricing · playwright.dev/docs/test-agents · GitHub API (playwright, playwright-mcp, playwright-cli, stagehand, browser-use) · npm downloads API · news.ycombinator.com/item?id=31236066 · smolanalytics cli/README.md.
REPORTED (via search, unfetchable or secondary): vendr.com/marketplace/mabl · vendr.com/buyer-guides/rainforest-qa · G2/TrustRadius review aggregations (via drizz.dev, testsigma.com, bug0.com, cubeapm.com) · testeragents.com/pricing/testim · scrolltest.medium.com (114K tokens) · testdino.com · qaby.ai · alternativeto.net (Cypress 13 plugin blocking) · bigbinary.com (Cypress→Playwright) · checklyhq.com/blog/how-to-spend-ten-grand-12-bucks-at-a-time (Datadog cost mechanics).
