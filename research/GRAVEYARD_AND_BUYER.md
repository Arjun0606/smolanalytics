# The graveyard and the buyer

Why AI QA/testing startups die, what actually gets muted or cancelled, and who pays.
All sources fetched 2026-08-25 unless noted. Labels: **MEASURED** = I fetched/ran it myself.
**CLAIMED** = their marketing or a third party's assertion. **INFERRED** = my read from the evidence.
Builds on `/Users/arjun/smolanalytics/AUTONOMA_TEARDOWN.md` (cited below as TEARDOWN); nothing from it is re-verified here.

Method note: G2, Capterra, TrustRadius, SoftwareAdvice and TheCTOClub all hard-403 non-browser
fetches (MEASURED — direct curl with browser UA returned 403 on all three review hosts). The review
tally below was collected through search-indexed snippets of those same G2/Capterra reviews plus
third-party compilations that quote them; each item is labelled with where it surfaced. HN data is
raw JSON from the Algolia API (MEASURED). DNS/redirect checks are raw curl (MEASURED).

---

## 1. The graveyard

Every notable death, pivot-out, or absorption in AI/autonomous testing, with the mechanism of death.

| Company | Raised | Fate | Evidence |
|---|---|---|---|
| **walrus.ai** — "test your most complicated user flows with plain English" | Crunchbase-listed seed | **Dead. Domain does not resolve.** | MEASURED: `getaddrinfo ENOTFOUND walrus.ai`; curl returns nothing. Show HN 2019-11-05: **7 points** (HN 21454433) |
| **test.ai** (ex-Appdiff) — AI mobile app testing, Gradient/Google-backed | "$30M+" (CLAIMED, testguild.com/podcast/a600-jason) | **Majority of company sold, domain dead.** Founder Jason Arbon restarted as testers.ai | MEASURED: no HTTP response from test.ai. CLAIMED: "team decided to sell the majority of the company" (testguild.com) |
| **Octomind** (Berlin) — AI-generated Playwright E2E, $89/mo Basic / $589/mo Pro | seed | **Testing product discontinued ~May 2026; octomind.dev DNS is dead.** Team now ships an AI dev-agent / "persistent cloud compute for AI agents" | MEASURED: `ENOTFOUND octomind.dev`. CLAIMED: "service was discontinued in May 2026… no longer available to new customers" (test-lab.ai/blog/ai-testing-pricing, stackpick.net/tools/octomind). MEASURED: their own HN posts pivot — "Octomind – AI dev assistant" (44234692, 2025-06), "Show HN: Octomind Cloud – Persistent cloud compute for AI agents" (49007062, 2026-07) |
| **ProdPerfect** — "autonomous E2E tests built from live user traffic," $13M Series A 2019 (techcrunch.com/2019/12/17) | $13M+ | **Original product gone; site now sells "AI-Powered Testing for Mainframe and Legacy Modernization"** — enterprise services, no pricing | MEASURED: fetched prodperfect.com homepage; zero mention of traffic-based testing remains |
| **CamelQA** (YC W24) — "AI that tests mobile apps," natural-language tests on real devices, $500/mo | YC | **Pivoted out of QA within ~a year** → camelAI (AI data analyst). Launch HN got **141 points** — attention did not make a market | Launch HN 39769412 (2024-03); pivot: ai.miraheze.org/wiki/CamelAI, camelai.com |
| **DeploySentinel** (YC) — flaky-test/CI-failure dashboards for Cypress & Playwright | YC seed | **Abandoned the category** → HyperDX (observability) → **acquired by ClickHouse, March 2025** | clickhouse.com/blog/clickhouse-acquires-hyperdx…; HN 37842778: **Cypress blocked deploysentinel.com** to force flake-dashboard demand into Cypress Cloud ("can easily be 6 figures" — jasonlaster11); founder mikeshi42, 2022 (HN 32319404): customers "see their CI as largely ignored" |
| **Ponicode** — AI unit-test generation | seed | Acquired by CircleCI 2022-03-09; **product killed; ponicode.com now 301s to circleci.com homepage** | MEASURED: `301 → https://circleci.com/`; businesswire.com/news/home/20220309005008 |
| **Launchable** (Kohsuke Kawaguchi) — ML test selection | ~$16M (CLAIMED) | Absorbed: launchableinc.com **308-redirects to cloudbees.com/capabilities/cloudbees-smart-tests** | MEASURED redirect chain |
| **Reflect.run** — no-code/AI-prompt E2E | seed | Acquired by SmartBear 2024-01-25; runs as a SmartBear line item. Their two Show HNs: **5 and 2 points** (36433869, 36478413) | smartbear.com/news/news-releases/smartbear-acquires-reflect |
| **Waldo** — no-code mobile E2E | ~$15M (CLAIMED) | Acquired by Tricentis 2023-07-07 | tricentis.com/news/tricentis-acquires-codeless-mobile-test-automation-platform-waldo |
| **Testim** — self-healing AI selectors | ~$20M | Acquired by Tricentis 2022. Founder-adjacent employee (inglor, HN 31236066): "**We tried and failed to create a 'bug capture' offering in Testim**… fundamental issues with anything that doesn't reproduce timing perfectly" | HN 31236066 (2022-05) |
| **Rainforest QA** (YC S12) — crowdtesting → no-code → AI hybrid | ~$82M (CLAIMED) | Alive after **three pivots in 14 years**; never won the category. mrkurt on their 2021 relaunch: "Raising a Series B with an enterprise sales model, then releasing a self service product is like the hardest possible shift" | Launch HN 28947689 (149 pts) |
| **Autonoma** | Bessemer pre-seed | Alive; economics are a subsidy — run metering never fires, preview compute hard-coded to 0 credits, "~48% of classifications have no recording." Show HN: **5 points** | TEARDOWN |

**The long tail (MEASURED, Algolia):** Show HN "AI QA/testing" launches without YC distribution
almost all land at 2–7 points, 0–2 comments: Qanairy 3pts (2019), walrus.ai 7pts (2019), Quell 7pts
(2025), Jina 2pts, Ovi 3pts, RiddleRun 3pts, Testronaut 4pts, "AI-first QA platform" 2pts, Deltix
54pts (2026, the exception — and its top comment found the support email bounces). The YC-labelled
ones get 58–149 points (Rainforest 149, CamelQA 141, Meticulous 122, Propolis 116, Checksum 78,
Canary 58) — **and CamelQA still pivoted out within a year of its 141-point launch.** Launch
attention and market existence are uncorrelated in this category.

**Who profited:** Tricentis bought Testim AND Waldo; SmartBear bought Reflect; CloudBees bought
Launchable; CircleCI bought Ponicode and shut it; Thoma Bravo took Applitools (2021). The category's
proven exit is *consolidation into a suite vendor*, not a standalone winner. INFERRED: buyers of
these companies were buying customer lists and AI marketing, not durable product — Ponicode's
absorption-then-deletion is the clean example.

**The one durable business model found so far:** QA Wolf — humans + automation sold as an outcome
("zero-flake guarantee"), **~$40–70 per test per month, median ACV ~$90K, range $60K–$250K+**
(CLAIMED: vendr.com/marketplace/qa-wolf, bug0.com/knowledge-base/qa-wolf-pricing; QA Wolf publishes
no pricing). Raised $36M in 2024 (techcrunch.com 2024-07-23). The market pays for *a maintained
green suite someone else is accountable for* — not for software that promises one.

---

## 2. The churn tally — negative reviews across vendors

34 distinct dislike items collected across 8 vendors (mabl, Testim, testRigor, Katalon, Applitools,
Virtuoso, Autify, Functionize) from search-indexed G2/Capterra review text and compilations quoting
it. Grouped by reason, most frequent first:

**A. Flakiness the tool was supposed to remove (8 items — every vendor)**
- mabl: "random failures in execution"; "troubles with flakey tests and having to implement hard waits" (G2 snippets)
- Testim: "sometimes tests need to be rerun because of minor flakiness… NLP-based authoring has had reliability issues"; "failures sometimes break the CI/CD flow" (G2 snippets)
- Katalon: "might capture incorrect locators or break if the UI changes slightly, leading to flaky tests. The self-healing capability… sometimes fails to handle elements accurately" (G2 snippets)
- testRigor: "frequent bug issues… crashes and unexpected failures in test cases" (G2 snippets)
- Applitools: "false positives from anti-aliasing, dynamic content, and one-pixel render shifts" (G2/aitestingguide snippets)

**B. Slowness (7 items)**
- mabl: "setup and execution are painfully hard and long… when it does work, it takes so long to run that it slows down development"; "slow cloud runs" (G2 snippets)
- Testim: "test execution slows noticeably on larger suites and parallel runs"
- Katalon: "large test suites may lead to slower execution speeds"
- testRigor: "can feel slower during execution compared with tools like Playwright or Selenium"
- Virtuoso: "users experience slow performance" (G2 pros-and-cons page snippet)

**C. Price, and price growth (7 items)**
- mabl: "$450+/month… for small teams or modest test volumes, the price-per-test can be hard to justify" (drizz.dev/post/mabl-testing, quoting review corpus)
- Katalon: "licensing costs have increased significantly which puts it out of budget for many small and medium sized teams"; premium from $175/mo/license (G2 snippets)
- Testim: "pricing is tied to license and number of parallel runs, which is not a scalable solution"
- Applitools: "the pricing model does not scale well if you want to run visual tests on every code change"
- Meticulous (HN 31236066, kall): "First, they give me a taste with a free plan, then hit me with production pricing we still can't justify"
- Autify: "credit-based usage can be hard to forecast, the free tier being a one-time credit grant" (stackpick/softwareadvice snippets)

**D. Maintenance toil survives the AI (6 items)**
- Applitools: "every legitimate UI tweak means going back and re-approving baselines… teams need to accept baselines almost one by one"
- Katalon: "having to check results by going through each test case one by one"
- mabl: "selector-based testing is inherently brittle… healing only works when alternative attributes exist — insufficient for apps with dynamic IDs, auto-generated class names, or frequently restructured DOMs" (drizz.dev compilation)
- **Autify: "self-healing can mask real regressions"** — the healer itself becomes a churn reason (stackpick snippet)

**E. Trust / black box (3 items)**
- Functionize: "spent more time investigating whether Functionize's ML made a mistake than we spent investigating actual bugs. The black box created more work, not less" — **CLAIMED-by-competitor** (getautonoma.com/blog/opensource-alternative-functionize; directionally consistent with everything above but written by a rival)
- testRigor: "the plain English approach may lack precision for complex scenarios… adds ambiguity that developers will find frustrating" (G2/testingtools.ai snippets)

**F. Support/docs (3 items)** — Katalon docs "incomplete in certain areas", community smaller than
Selenium's; Virtuoso interface complexity; testRigor weak test management for large suites.

**Reading:** categories A+B+D are one compound failure — *the tool re-imports the flake/maintenance
problem it was bought to remove, at 10–100× the price of Playwright.* Price (C) is rarely the root
cause; it is the renewal-time excuse once A/B/D have destroyed trust. INFERRED but consistent
across all 34 items.

---

## 3. The HN objection corpus

Six launch threads read in full (MEASURED, Algolia items API). Objections that repeat, ranked by
frequency across threads:

**1. "My coding agent already does this" — the 2026 default objection.**
- Deltix (49307099, 2026-08): "Just did this locally by giving my AI agent full ADB access to a test phone" [iamcoder18]; "Claude has started doing this after their latest release" [perfectlyFine]; an airline's outsourced QA workflow already runs "claude desktop… tests it on the app with playwright or claude browser… reports bugs via mcp" [alightsoul]
- Canary YC W26 (47441629, 2026-03): "what makes this different than just another feature in Gemini Code Assist or GitHub Copilot?" [warmcat]; "what's the advantage over Claude Code + the GitHub integrations?" [recsv-heredoc]; "Isn't [no moat] the case with every AI startup? Nobody has a moat and it's tough to build one because the playing field is so level" [monkpit]
- Propolis YC X25 (45762012, 2025-10): "I struggle to see how it is different than spinning 10 Atlas tabs with a 2 sentence prompt" [not-chatgpt]
- Vendors' only rebuttal, verbatim (Canary founder): "you would need custom browser fleets, ephemeral environments, data seeding and device farms" — i.e. the defense is *infrastructure*, not intelligence.

**2. AI increases flakiness, it doesn't remove it.**
- CamelQA (39769412): "the entropy of GPT and anything less than 100% accuracy in the computer vision pieces would lead to MORE flakiness" [ngokevin]; founders' own demo video shows GPT-4V "thinking a page in the shop app is an ad"
- Meticulous (31236066): "the indeterminism introduced by network waits… makes e2e complicated and often not worth its upkeep" [polskibus]

**3. The conversion-rate razor — does a generated check survive as a regression test?**
- Canary: "The interesting question is not whether the system can generate a plausible PR-time test, but whether the useful ones survive after the PR is gone… without turning into a flaky, environment-coupled browser script. **That conversion rate feels closer to the real moat than the generation demo**" [pastescreenshot]

**4. State, auth, and test data are where it dies.**
- Propolis founder, verbatim: state changes between runs are "one of our biggest challenges"
- Meticulous: no SSE/WebSocket capture; "you may need to record new sets of sessions as your application changes" (stale-recording problem, admitted)
- Rainforest: "How do you deal with permissions, proprietary information?"; "can the client authenticate your test access?" — the auth/VM-access question recurs in every thread (MFA, OTP, email confirmation)
- Matches TEARDOWN: Autonoma's ~48% of classifications have no recording; 97% of those executed no steps — runs die before the app is even reached.

**5. Lock-in fear — buyers demand the exportable artifact.**
- Propolis: "Does it output playwright scripts?" [orliesaurus]
- Meticulous: "User get to take away the script so they don't get vendor lock-in" [a_c]; "I would want it to be open source and self hostable" [quickthrower2]; portability of replay data raised twice more
- Structural version: **Cypress blocked deploysentinel.com** so failed/flaky-test dashboards route through Cypress Cloud (HN 37842778). If you build on someone else's runner, the runner vendor can end you.

**6. Professional-tester contempt for the category.**
- Checksum (35629050): "Most tool companies making claims about their tools show a shocking lack of knowledge about testing. This generally guarantees that their tools are dismissed by serious professionals. That still leaves a pretty substantial market among credulous wishful thinkers… I would like to see a tool that isn't just more bullshit" [satisfice — James Bach, the best-known living testing methodologist]
- "My experience with automated testing solutions has been lukewarm so far" [sachuin23]; coverage-as-metric suspicion [8organicbits]

**7. Noise/PR-spam.** Canary: "I definitely don't want three long new messages on every PR. Max 1,
ideally none" [blintz]. And category fatigue: "So much noise, might just put my head under rock for
a few months" [4b11b4, Deltix thread]; "there are at least 10 dozen code review startups… I see a
new one on YC every week" [vivzkestrel].

---

## 4. The buyer at 5–50 people

**Who owns the budget.** Nobody, structurally. Before the first QA hire, "quality is a full team
effort… engineers writing tests, and testing their own PRs" (jam.dev/blog/how-to-hire-your-first-qa-person-at-your-startup, MEASURED fetch).
The purchasing decision-maker for an E2E tool at this size is therefore the founder/eng-lead — the
same person who feels CI pain personally. There is no QA persona to sell to; tools that presuppose
a QA owner (test management consoles, tester seats) have no user. First dedicated QA hires
typically arrive "by reaction, when you face quality issues" (medium.com/@vincent.ferreira,
action-or-reaction post) — i.e. after an incident, not on a schedule.

**Purchase triggers, ranked by evidence strength:**
1. **An incident streak / velocity-vs-quality breakdown.** "Like many startups we've struggled with
   velocity vs quality… it's never really worked well. My team did a bake off" [jnathsf, Rainforest
   thread]. The reaction-hire pattern above is the same mechanism. (MEASURED quotes, INFERRED ranking)
2. **AI-accelerated shipping making QA the bottleneck — the new 2026 trigger.** "I've heard a few
   stories of QA departments being near-burnout due to the increased rate developers are shipping at
   these days. Even we're looking for any available QA resources we can pull in" [recsv-heredoc,
   Canary thread, 2026-03]. Autonoma's whole homepage thesis (TEARDOWN §5) is this trigger.
3. **SOC 2.** CC8.1 requires changes be "authorised, designed, tested, approved and deployed"
   through a controlled process; auditors sample deployments and verify "automated CI tests passed"
   per PR before approval (auditpath.io/blog/soc2-change-management; deployhq.com SOC-2 deployment
   post; MEASURED search text). Important nuance: **SOC 2 requires test evidence attached to each
   change, not an E2E tool** — any passing CI satisfies it. So SOC 2 buys "some tests in CI on every
   PR," which is a wedge for a per-PR tool, not for a hosted QA platform.
4. **First QA hire brings a tool.** INFERRED from vendor case-study patterns; no primary quote captured.

**Rip-out triggers (what makes them cancel):** the mechanism is almost never a rage-quit; it is
**muting, then non-renewal.**
- Google's numbers make muting structural, not vendor-specific (testing.googleblog.com/2016/05, MEASURED
  fetch): "1.5% of all test runs report a flaky result… almost 16% of tests have some level of
  flakiness… **about 84% of the transitions we observe from pass to fail involve a flaky test**…
  It is quite common to ignore legitimate failures in flaky tests due to the high number of
  false-positives." If Google can't hold the line internally, a $450/mo SaaS certainly can't while
  adding LLM entropy.
- Slack automated the muting: auto-detection **and suppression** of flaky tests at scale
  (slack.engineering/handling-flaky-tests-at-scale-auto-detection-suppression).
- The endgame is deletion, experienced as relief: "We deleted 247 E2E tests and CI got 62% faster…
  developers started trusting CI again — when a test failed, they investigated instead of assuming
  it was flaky" (medium.com/codetodeploy, 2026-01, CLAIMED numbers).
- DeploySentinel founder on why the flake-dashboard business had no floor: customers "waste dev &
  CI cycles rerunning tests and **see their CI as largely ignored**" — you cannot sell observability
  over a signal the team has already stopped believing.
- Renewal-time math: at mabl $450+/mo, Katalon $175/license/mo rising, QA Wolf $90K ACV, the
  bake-off alternative in 2026 is Playwright + the coding agent the team already pays for.
  (Prices: drizz.dev, G2 snippets, vendr.com — CLAIMED.)
- Vendor death itself: Octomind's paying customers were churned *by the vendor* in May 2026. In a
  category with this body count, "will you exist next year" is a real objection to every small vendor — including us.

---

## 5. Ranked: why these products get muted or cancelled — and what each implies for us

**1. The tool re-imports flakiness at a premium → red becomes noise → muted → non-renewed.**
Evidence: Google 84%/16%/1.5% (MEASURED); 8 review items in §2A; ngokevin's entropy argument;
DeploySentinel founder's "CI largely ignored"; the deletion-as-relief post.
→ **For us:** our five-status model (passed/failed/stale/errored/flaky) and flaky-vs-broken +
failing-since in cloud is exactly the right shape — but the bar is behavioral, not taxonomic: *a
flake must never interrupt a human twice.* Auto-quarantine on first flaky verdict, keep it running
silently, surface it in the run history only. The kill condition for every dead vendor was a false
red on a PR. One false `failed` that blocks a merge costs more trust than ten real bugs caught buy.

**2. "My coding agent already does this" — commoditization by the foundation-model layer.**
Evidence: §3.1 — it is now the top comment on every launch (Deltix, Canary, Propolis), and the
airline anecdote shows real orgs assembling it themselves from Claude + Playwright + MCP.
→ **For us:** this will be the first comment on our launch. The honest answer we can uniquely give:
an agent *re-derives* the test every run (expensive, non-deterministic, no history); we run
**deterministic proof-carrying replay for near-zero cost and invoke intelligence only on breakage**,
and the run history (flaky-vs-broken, failing-since) is longitudinal state a fresh agent doesn't
have. Put that answer on the landing page before HN writes it for us. Also: we should *ride* the
agent wave, not fight it — the agent is our installer and operator (MCP), which none of the dead
vendors could say.

**3. Verdict distrust — black box, false positives, self-healing that lies.**
Evidence: Functionize black-box quote (competitor-sourced but consistent); Applitools baseline
false-positive toil; **Autify's "self-healing can mask real regressions"**; satisfice's "more
bullshit"; Autonoma's 48%-no-recording (TEARDOWN).
→ **For us:** proof-carrying replay is the direct counter — *never emit a verdict without evidence
attached*, and never silently rewrite an assertion (Autonoma's revert-on-rewrite discipline is the
one part of their adjudicator to copy; Autify's masking is the cautionary tale). If a heal changes
what is asserted, that is a diff shown to a human, not a green run.

**4. Pricing that punishes usage — credits, per-step meters, per-run charges.**
Evidence: Momentic (MEASURED pricing page): "every test step uses one credit, **including steps
that AI features generate and run**" — you pay for the tool's own healing; $125/mo + $0.01875/credit
overage. Autify's unforecastable credits (§2C). CamelQA started at $500/mo. Octomind died at
$89–589/mo hosted. TEARDOWN refusal #2 (metering a flat-COGS product).
→ **For us:** the structural advantage nobody in the graveyard had: **the customer's CI runs the
compute and their own API key pays for intelligence — our COGS per run is ~zero**, so $19/mo flat
with a tested-PRs meter doesn't fight the customer's desire to run more tests. Never add a per-step
or per-run meter; it recreates the churn mechanism of §2C. The tested-PR meter also happens to be
the exact unit a SOC 2 auditor samples ("automated CI tests passed" per PR) — say that out loud in
compliance-adjacent copy.

**5. The environment/state/data wall — runs die before the app is reached.**
Evidence: Autonoma 48% no-recording / 97% zero-steps (TEARDOWN); Propolis founder: state is "one of
our biggest challenges"; Meticulous stale recordings + no SSE/WS; recurring MFA/OTP/email-confirm
questions in every thread; Canary's moat claim being *env infrastructure*.
→ **For us:** running in the customer's CI against their own environment sidesteps the hosted-env
wall (no VM access questions, no auth handoff, their secrets never leave), and teardown safety +
retries+evidence already exist. The residual gap is test data/seeding and side-channel flows
(email/OTP): be explicit in docs about what we don't do rather than vapor it — the graveyard's
demos all broke exactly there. A first-class "this test needs: a login, a seeded record" declaration
would answer the single most repeated HN question in the corpus.

**6. Lock-in fear.**
Evidence: §3.5 — export-to-Playwright demanded in every thread; Cypress killing DeploySentinel is
the structural proof that renting someone else's data plane is fatal.
→ **For us:** already structurally answered — tests are sentences plus proof files in *their* repo,
runner is zero-dep and runs on their CI; there is nothing to export because nothing is hosted.
This is a top-three differentiator and should be stated as a guarantee, not a feature: "cancel us
and every test still runs." None of the eight §2 vendors can say that sentence. (Also the MIT-vs-BSL
asymmetry from TEARDOWN applies against Autonoma specifically.)

**7. No natural budget line at 5–50 → sold to a persona that doesn't exist → shelfware.**
Evidence: §4 — quality owned by everyone/no one; QA hire is reactive; the only proven big spend is
outcome-service (QA Wolf $90K ACV); enterprise platforms (mabl/Katalon/Testim) churn small teams on
price alone.
→ **For us:** the buyer is the founder/eng-lead at the incident moment or the SOC 2 moment — a
card-swipe, not a procurement. $19/mo is priced for exactly that buyer. But note the honest gap:
**no one has yet proven a self-serve low-price business in this category** — Octomind at $89/mo
hosted is the closest attempt and it's dead. Our thesis for why we differ must be COGS (their CI,
their key — we can survive margins Octomind couldn't) plus agent-native distribution. That is a
thesis, not a fact; treat early retention as the experiment that tests it.

**8. Launch-moment as a strategy fails in this category specifically.**
Evidence: the long-tail table in §1 — non-YC "AI testing" Show HNs median ~3–7 points; Autonoma's
5-point Show HN and 397-view livestream (TEARDOWN); CamelQA's 141-point launch followed by a pivot.
→ **For us:** consistent with the existing GTM: the wedge is being *found by the agent* (MCP
registries, npm, docs the agent reads) and by SEO the way Autonoma actually acquired (44-page
competitor cluster), not a launch spike. If we do Show HN, the title must survive objection #2 —
lead with proof-carrying determinism, not "AI tests your app," which is the exact phrase the
corpus has learned to dismiss.

---

## 6. Hard truths this research surfaced for us specifically

1. **Our own pitch contains a documented churn reason.** "One sentence, a real browser, a verdict"
   inherits testRigor's most-cited developer objection: "the plain English approach adds ambiguity
   that developers will find frustrating." walrus.ai — the closest historical analog to our exact
   pitch, plain-English E2E — is DNS-dead. Mitigation exists (the sentence compiles to a
   deterministic recorded plan; ambiguity is resolved once at record time, not every run) but we
   must *show* the compiled, inspectable artifact or developers will assume every run re-rolls the dice.
2. **The verdict, not the actor, is the defensible part** — TEARDOWN said it about Autonoma and the
   HN corpus confirms it from the demand side (the conversion-rate razor, §3.3). Our five statuses
   plus evidence bundles are the product; browser driving is a commodity we should spend minimal
   effort defending.
3. **84% of red is flake** is the number the entire category died on. Any week our users see a false
   red on a PR, we are on the graveyard's path regardless of feature velocity. The self-consistency
   work (KILLER_PLAN Phase 0) is the same discipline applied to analytics; here it is existential.
4. **The only proven dollars are $90K outcome-services and consolidation exits.** A $19/mo
   self-serve wedge is contrarian to every observed outcome in this market. It is *plausible* only
   because our COGS structure (their CI, their key) is genuinely novel among the dead — but no
   evidence found here proves the demand side at that price point. First 50 retained payers matter
   more than any feature on the roadmap.
