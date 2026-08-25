# THE OBJECTIVE SCORECARD — smolanalytics vs Autonoma, with the field as context

Date: 2026-08-24. Companion to `AUTONOMA_TEARDOWN.md`, `FIELD_INCUMBENTS_AND_FREE.md`,
`GRAVEYARD_AND_BUYER.md`, `WHITESPACE_DB_AND_AI.md` — built on them, not re-derived.

**Evidence labels.** MEASURED = fetched/ran/read the primary artefact today. CLAIMED = the vendor's
marketing/docs. INFERRED = my read. Nothing below is scored from marketing copy alone; every
Autonoma cell traces to their repo (via the teardown's code reads), their published README numbers,
or a fetch of getautonoma.com made today; every smolanalytics cell traces to code read today and a
test that passed today.

**Ground truth on our side, established before scoring (all MEASURED 2026-08-24):**
- `cd ~/smolanalytics/cli && node --test test/*.test.mjs` → **290 tests, 290 pass, 0 fail** across
  14 test files (verdict, flake, replay, safety, suite, suspect, templates, browser, plan, …).
- Files read in full or by section: `cli/lib/test.mjs` (1,108 lines), `suite.mjs` (826),
  `safety.mjs` (379), `suspect.mjs` (380), `preview.mjs` (272), `suggest.mjs` (387), `plan.mjs`
  (138), `cli/README.md`, `cli/package.json`, `smolanalytics-cloud/lib/runs.ts`,
  `smolanalytics-cloud/lib/plans.ts`.
- Autonoma side: `AUTONOMA_TEARDOWN.md` (their public repo read directly — billing code, README,
  licence, prompts) + getautonoma.com homepage fetched today. Their `/pricing` URL returned **404**
  today (the teardown's billing numbers come from their repo's code, which is stronger evidence
  than a pricing page anyway).

Verdict tally: **we win 9 · tie 5 · they win 3 · both missing 0** — and the three losses are real,
so they get costed, not spun.

---

## The table

| # | Capability | Autonoma | Field context | smolanalytics (MEASURED) | Verdict → closing cost if they win |
|---|---|---|---|---|---|
| 1 | **Perception** | Vision-model screenshots + coordinate clicks — Gemini Flash/Qwen/Moondream, ~2 model calls + an image per step; 4 deterministic vision probes run pre-loop, deliberately not exposed as tools (MEASURED, teardown §1 + our `test.mjs` header notes from reading their repo) | Playwright MCP itself uses "structured accessibility snapshots, bypassing the need for screenshots" (MEASURED README); Rainforest is pixel-matching, the most change-sensitive layer in the field; HN commenter dimal: a11y-tree perception "is a strong signal to developers to make pages accessible" | `perceive()`/`flatten()`: `ariaSnapshot()` → roles/names/states/values as refs, actionable-role whitelist, truncation **reported never silent**, clicks are real locators with actionability checks (`test.mjs:46–130`; exercised by `browser.test.mjs`, passing) | **We win.** Same layer Microsoft chose for MCP; no per-step image tokens; a coordinate can click the wrong thing and blame the wrong feature — a locator cannot. |
| 2 | **Verdicts / adjudication** | 7 closed verdicts (passed / client_bug / engine_artifact / environment_failure / scenario_issue / plan_mismatch / invalid_test), no fallback path; rewrites that fail re-run are **reverted** (MEASURED, their README via teardown §1) | Testim churn quote: "difficult to figure out the reason behind the failure"; DHH: "figuring out why a black-box test has failed is often surprisingly difficult"; the teardown's own conclusion: the adjudicator is the defensible asset in this category | 5 statuses passed/failed/stale/errored/flaky; flaky **never counted as passed**; errored = "our side, not your app"; stale = "rename or removal, replay cannot tell"; exit contract 0/1/2 where only `failed` reddens a build (`test.mjs:432–501`, `suite.mjs:460–471`, `runs.ts:15–47`; `verdict.test.mjs` + `flake.test.mjs` passing; cloud API refuses `flaky` as an incoming run status — "a single run is never flaky") | **Tie.** They have finer per-run causality (their scenario_issue/invalid_test split — see behind-list #5); we have the honesty rules (flaky≠pass, tool-fault≠app-fault) and a machine-checked contract (`lib/runs.test.ts` reads the Go source and fails if statuses drift). Different halves of the same discipline. |
| 3 | **Flake handling** | **Zero files match "flake" in the repo** (MEASURED, teardown §1); homepage claims "no flaky failures from minor layout changes" (CLAIMED today, contradicted by their own code) | 32% of 38 negative reviews across the field = "the tool itself is flaky" — churn reason #1; Checkly bills each retry as a check run | One retry from a clean page; fail-then-pass = **`flaky`, its own verdict, never a pass**, reason names both the failure and the pass; `errored` and `stale` are never retried (`test.mjs:432–501`); cloud derives flaky-vs-broken over a 10-run window, ≥2 flips, same-sha-both-verdicts, only passed/failed count in the series (`runs.ts:138–213`) | **We win** — against Autonoma outright (they have no flake mechanism at all, only marketing), and structurally against the field (retries on the customer's CI cost nothing and produce information instead of a bill). |
| 4 | **Record/replay economics** | No deterministic replay exists; every run is agentic. Their own README: **~48% of classifications have no recording, 97% of those executed no steps** (MEASURED, teardown §1) | Reported 114K tokens/test via Playwright MCP; practitioner consensus "keep MCP out of CI"; Meticulous replays against a **mocked** backend (MEASURED how-it-works) — determinism by freezing the world | `compile()` keeps only replayable steps + a proof string; `replay()` executes **zero model calls**, verifies `text.includes(proof)`; missing proof → `unproven`/`outcome-changed` → stale → agent re-records from the sentence (`test.mjs:258–425`; `replay.test.mjs` passing). Measured on our own site: 8.0s agented vs 1.4s replayed (`README.md:81–82`) | **We win.** They cannot copy this without rearchitecting (their runtime IS the LLM loop); Meticulous's replay never touches a real backend; ours replays proofs of intent against the live app. |
| 5 | **Evidence** | Recordings, when the pipeline survives — which their README says it doesn't for ~half of classifications (MEASURED) | Black-box verdicts are churn tally #6; QA Wolf's top anti-churn feature is exportable Playwright — the artefact belongs to the customer | On failure/flake only: screenshot + page text to `.smolanalytics/evidence/`, named in the GitHub step summary, uploaded as a CI artifact by the shipped workflow; evidence can never change a verdict (`test.mjs:504–539`, `templates/github-action.yml`; `templates.test.mjs` passing). Recordings live in the customer's repo/cache, plain JSON | **We win** — evidence at the moment it's needed, zero evidence-pipeline to die in, and the artefacts are the customer's files. |
| 6 | **Data safety / seeding** | Customer implements create/delete functions through their SDK — "seeds and tears down exactly like your app does" (CLAIMED today); the layer is ~660KB + an 8-language protocol, and it's where runs go to die (MEASURED, teardown §1, §6) | No incumbent or free tool has ANY data-safety default: an MCP agent told "test signup" signs up real-looking emails and nothing cleans up | `smoltest` prefix on every generated value (one `LIKE 'smoltest%'` finds everything ever created); RFC-2606 `example.com` emails that can never reach a human; production-URL warning that asks a human and **never blocks CI**; `--teardown <url>` POSTs the identity after every run **including failures**; none of it may change a verdict (`safety.mjs:1–60,127–360`; `safety.test.mjs` + `comment-safety.test.mjs` passing) | **Tie, split honestly.** Their seeding is deeper when assembled (real fixtures with business rules — we cannot conjure "a logged-in user with 3 past orders"). Our safety works in minute one at zero cost and cannot redden a build. The gap on our side is a seed hook — behind-list #3. |
| 7 | **Preview environments** | Builds an isolated environment: GitHub App across repos, agent pushes a Dockerfile, env-key handover, "ETA ~1h 13m", 28 min in at 3% then 0%, step 1 of 7 failed — timed on a real repo (MEASURED, `test.mjs:1–20` header); homepage now also says "tests run on your preview deploy" (CLAIMED today) | Vercel/Netlify/CF Pages announce every preview as a GitHub deployment + status; the buyer at 5–50 already has this | `preview.mjs` discovers the PR's own preview URL from the deployments API the job's GITHUB_TOKEN can already read — verified against vercel/commerce; **never guesses**, no fallback to production; not found in 4 min = errored exit 2 with instructions (`preview.mjs:1–45`; suite tests passing) | **We win for the buyer that exists** (5–50, on Vercel/Netlify — the graveyard doc's buyer). Honest boundary: where no preview infra exists at all, they can build an environment and we cannot — but half their runs die inside that build, which is why we refused it. |
| 8 | **Suspected code** | A 133-file "diffs" package (MEASURED — their repo, noted in `suspect.mjs:3`); output quality unmeasured | Nobody else in the field ships PR-diff blame at all | `suspect.mjs`: deterministic intersection of what the failing run observed (clicked names, proof text, paths) × git's own diff; **no suspicion without named evidence, zero matches = say nothing**; degrades silently, can never change a status (`suspect.mjs:1–60`; `suspect.test.mjs`, 488 lines, passing) | **Tie.** Both ship it; theirs is bigger, ours is auditable — every suspect line names the string connecting file to test. Neither side's hit-rate is measured in production. Cheap differentiating move available: publish our precision once run volume exists. |
| 9 | **Visual checks** | Vision-based perception + 4 deterministic pre-loop vision probes means a blank page, an error screen, a catastrophic render **is seen** (MEASURED, teardown §1) | Meticulous owns screenshot-diff with Dropbox/Notion logos; Rainforest shows pixel-matching's cost (every visual tweak is a diff, hence their human crowd); Applitools sold to PE | **Nothing.** `grep -riE 'visual|pixel'` over `cli/lib` finds no visual assertion mechanism (MEASURED today); screenshots exist only as failure evidence. Our proof is page **text**: a CSS catastrophe that leaves the DOM text intact replays green | **They win.** Closing: NOT a screenshot-diff engine (that is Meticulous's category and Rainforest's failure mode). A deterministic viewport probe — blank-render / zero-visible-text / error-overlay detection as an `errored`-style guard, plus opt-in shape predicates (`no_selector: ".error-toast"`, element-visible) per WHITESPACE Tier 2. Days, not months, and it closes the false-green trust hole, which is the part that matters. |
| 10 | **Mobile** | Real: Appium in the stack; billing code prices iOS runs at 200 credits and Android at 40 — you don't write rate-card entries for vapor (MEASURED, teardown §1, §4); homepage claims Flutter/React Native/Swift (CLAIMED today) | Churn tally #2 (coverage walls, 29%) includes "no live device support for mobile"; BrowserStack/Sauce sell device clouds at $59–225/parallel | **Nothing.** Zero hits for mobile/appium/ios/android across `cli/lib` and README (MEASURED today) | **They win.** Closing, honestly tiered: mobile-web viewport emulation (Playwright device descriptors) = days and covers the ICP's actual mobile surface (responsive web); native iOS/Android = Appium, a real device farm or customer-provided simulators, months, and it breaks zero-dep. Recommendation: ship viewports, refuse native, say so in docs — our buyer ships web apps. |
| 11 | **Suite generation** | Their onboarding lead: planner maps the app, drafts tests; web generation billed 500 credits = $0.333 (MEASURED billing code); 46 prompt files totalling 388KB, largest source file is a 58KB prompt (MEASURED, teardown §1) | Microsoft ships free Planner/Generator/Healer agents (MEASURED playwright.dev/docs/test-agents) — authoring is being commoditized to $0; the graveyard's lesson: writing tests was never the bottleneck | `suggest`: real browser crawls the app, model proposes only from what the crawl saw, **quote-evidence-or-dropped** (a proposal whose quote appears on no visited page never becomes a file), writes `tests/*.md` in the exact suite format; never exits 1 (`suggest.mjs:1–25`; suite passing) | **Tie** — and deliberately a small bet on both sides' part of the board. Any roadmap hour here is an hour spent becoming Testim (graveyard §5). Our anti-hallucination rule is the only durable part. |
| 12 | **CI integration** | GitHub App; results as PR checks (App MEASURED to exist via onboarding; check UI CLAIMED today) | Cypress Cloud bills per `it()` result and blocked its users' escape plugins; Checkly's meter bills retries | Shipped `templates/github-action.yml`: runs on the customer's runner with the GITHUB_TOKEN Actions already provides, no App, no repo write perms; ONE PR comment edited in place via idempotency marker (`markerFor`, `suite.mjs:492–519` — the two-comment parallel-build bug was found and fixed); step summary; exit contract where stale/errored exit 2, never 1 (`templates.test.mjs`, 305 lines, passing) | **Tie on capability, we win on trust surface** (a workflow file the customer reads vs an App with org-wide repo access) — but scored a tie because both deliver the same artifact: a verdict on the PR. |
| 13 | **Setup friction** | MEASURED on a real repo: install GitHub App everywhere → agent pushes a Dockerfile into your code → hand over SUPABASE_URL/OPENAI_API_KEY → "ETA ~1h 13m" → 3% → 0% → step 1 of 7 failed (`test.mjs:1–20`) | Preflight's HN thread: "without a pricing page I just move along"; forced onboarding = dark pattern; purchase is incident-triggered and must pay off same-day (GRAVEYARD §4–5) | `npx smolanalytics test --url … --test "…"` — no account, no App, nothing written to the repo, Playwright lazily fetched with an explanation, verdict in under a minute (README quickstart shows an 11.4s run; `cli.test.mjs` passing) | **We win, decisively** — and per the buyer research this is the row that decides whether evaluation ever happens. |
| 14 | **Pricing / meter** | Credits at $0.000667; generation 500, web run 10, iOS 200 — but `RUN_CONSUMPTION` is never written anywhere, preview compute hard-defaulted to 0, enforcement off fleet-wide: **the loop is free, it is a funded subsidy** (MEASURED billing code, teardown §4) | Usage-punishing meters are churn reason #2 field-wide (Aeolun's $25K/mo math; testRigor ~$900/mo; Testim "very weird" pricing); every incumbent meters a unit that grows when you test more thoroughly | One plan, $19/mo; meter = **tested pull requests** (`plans.ts:31–32` `prsIncluded`/`overagePerPr`, with the reasoning in comments: crons carry no PR number and are never counted — "metering them would teach people not to run them"); runs execute on the customer's CI with the customer's model key, so marginal runs cost us ≈$0 | **We win on structure** — the meter scales with shipping, not with thoroughness; the COGS shape is the anti-zombie one. Caveat stated plainly: Autonoma's price today is ~$0, so we can never sell "cheaper runs", only "flat, predictable, yours, and still here next year" (their own code comment says real rates await a "go/no-go"). |
| 15 | **Instrumentation half** | None. No analytics or instrumentation surface anywhere in repo or marketing (MEASURED absence, teardown scope + homepage fetch today) | No testing vendor on earth has one; the field's own objection ("it's not always code that's broken… a discrepancy at DB level" — Argus HN thread) is exactly what browser-level testing can't see and instrumentation can | The same product writes and maintains tracking inside the customer's PostHog / Mixpanel / Amplitude / GA4 / Segment / Plausible (`app/layout.tsx:41` states it; the self-serve source-connect shipped per project state), and `npx smolanalytics plan check` gates CI on `instrumentation_health` over MCP — one computation, two front doors, same exit-code contract (`plan.mjs:1–30`) | **We win, and it is the least copyable row on the board** — matching it requires building a second product in a second category. |
| 16 | **Run-history intelligence** | "Dashboards, and team collaboration features" (CLAIMED today); per-run adjudication only — no cross-run flake module exists in the code (MEASURED, teardown §1) | The two questions reviews say no vendor answers: "is it flaky or broken?" and "since when?" (GRAVEYARD §5-6) | `flakyOrBroken()`: 10-run aging window, ≥2 verdict flips required (one flip = a regression or a fix, not flake), same-commit-both-verdicts detection, errored/stale excluded from the series; `failingSince()` boundary; a test failing 100% is **named broken, never flaky** (`runs.ts:138–269,366`; `lib/runs.test.ts` cross-checks status constants against the Go engine source) | **We win** — the cross-run axis exists in our code and does not exist in theirs. |
| 17 | **Self-hosting / licence** | BUSL-1.1 — MariaDB's own page: "not an Open Source license"; use-grant harsher than HashiCorp's (no internal-use safe harbour); change-date cliff 2028-03-23; self-host is a bluff: no deployment guide, dev-only compose, real deploy = EKS + Temporal + Loki + privileged BuildKit (MEASURED, teardown §6) | HN audits open-washing (Magnitude thread: "stop saying 100% open source"); QA Wolf's Playwright export shows exportability is an anti-churn requirement | CLI npm package: `"license": "MIT"` (`package.json:34`, MEASURED); README closes "MIT, every feature in the free binary, self-host forever" (`README.md:287`); recordings/tests are plain files in the customer's repo | **We win — with one internal flag to resolve before saying it anywhere loud:** the Aug-2026 commercial pivot decided the product is fully paid / not open source, while the CLI's README still promises "MIT… self-host forever." Both cannot be true in public. Either the runner stays MIT (strongest possible contrast with their BUSL — recommended) or the README claim comes out. Decide before this row is marketed. |

---

## The 5 rows where we are objectively behind — ranked by how much they matter per the graveyard/buyer research

1. **Visual blindness in the proof (row 9).** Our replay verifies page *text*; a CSS catastrophe,
   blank render or error overlay with intact DOM text replays **green**. Churn reason #1 field-wide
   is "the tool is less trustworthy than the app" — one false green on a visibly broken page
   converts the champion into a detractor faster than any missing feature. Autonoma's vision
   perception sees this class; we structurally cannot.
   *Close:* deterministic viewport guard (zero-visible-text / blank-render / error-overlay probe)
   plus opt-in shape predicates. Days. Do NOT build screenshot-diff — that's Meticulous's category
   and Rainforest's failure mode.
2. **AI-app assertions (row 9's cousin, from WHITESPACE Frontier 2).** Our exact-text proof breaks
   on *every* LLM-output page by design — and the ICP ships those (Vercel `ai` SDK: 22.4M
   downloads/wk, MEASURED). Momentic ($19.2M, Notion/Retool) and mabl ship semantic assertions in
   GA today; we ship nothing for the fastest-growing app shape in our own funnel.
   *Close:* Tier 1 LLM-boundary cassette (record model responses during the agent run, replay them
   deterministically — no product anywhere ships this) + Tier 2 shape predicates. The whitespace
   doc's build order #1–2; the one seam where we'd be first rather than catching up.
3. **Seeding depth (row 6).** We can create-and-label state; we cannot *provide* state. "A
   logged-in user with three past orders can request a refund" is untestable by us and testable by
   Autonoma's SDK seeding. Coverage walls are churn reason #2 (29% of negative reviews) — the suite
   goes green while the flow that matters is untested.
   *Close:* a `--seed <url>` hook mirroring `--teardown` (POST the run identity before the run,
   customer's endpoint fabricates state — their Environment Factory inverted, 1–2 days), then
   `--db neon` + LSN-in-evidence per the whitespace spec. Never build their 660KB version — half
   their runs die in it.
4. **Mobile (row 10).** They price iOS/Android runs in code; we have zero mobile anything, and "no
   mobile" is a named coverage-wall churn quote. Ranked fourth not first because the ICP
   (solo/CTO web builders on Vercel-shaped infra) ships responsive web, not native binaries.
   *Close:* Playwright device-descriptor viewports (days, honest partial answer); refuse native
   Appium out loud in docs. Re-rank only if churn data ever says otherwise.
5. **Scenario-vs-app causality in `failed` (row 2).** Their seven verdicts separate "the app is
   broken" from "the test sentence is wrong" (scenario_issue / invalid_test); our `failed` folds
   both onto the person reading the PR comment. That's churn reason #4 (maintenance returns wearing
   an AI mask) arriving through the back door: a wrong sentence that reads as a red build erodes
   the same trust a flaky verdict does.
   *Close:* one more field in the agent's finish schema ("does the evidence indict the app or the
   sentence?") surfaced as small type under a failed verdict — never a new status, never touching
   the exit code. Days.

## The 3 rows where we are ahead in ways the field cannot cheaply copy

1. **Record/replay economics (row 4).** Zero-model replay of proof-carrying recordings against the
   *real* app — 8.0s agented → 1.4s replayed, $0 marginal. Autonoma cannot copy it without
   abandoning their architecture (their product IS the runtime loop, and 48% of their
   classifications have no recording at all — their own README). Meticulous can't copy it without
   un-mocking their backend, which is their whole determinism story. Token-metered free agents
   can't copy it because the model *is* their runtime. This is the moat row.
2. **The instrumentation half (row 15).** The same browser walk that tests the app maintains the
   tracking inside the customer's existing PostHog/Mixpanel/GA4/Amplitude/Segment — and gates CI on
   instrumentation health through the same exit-code contract. Copying it requires a testing vendor
   to build an analytics product or vice versa; nobody in either field has both. It also answers
   the category's oldest objection ("browser tests can't see data-layer breakage") with a product
   instead of a caveat.
3. **The meter + COGS structure (row 14).** $19 flat, metered on tested PRs, executed on the
   customer's CI with the customer's model key, from a zero-dep runner. The incumbents' COGS is
   cloud browsers and crowd humans — following us down-market destroys their unit economics, which
   is why none of them has ever done it. Autonoma's alternative is pricing at $0 with the meters
   off, which is a runway, not a model. Structure is the defensible part; the price is just its
   visible tip.

---

*Sources: primary artefacts as cited per cell — smolanalytics code + `node --test` run of
2026-08-24 (290/290); AUTONOMA_TEARDOWN.md (their repo, README, licence, billing code);
getautonoma.com homepage fetched 2026-08-24 (their /pricing 404'd today); field/graveyard/buyer
citations live in the three companion research files and are not re-listed here.*

---

## RED-TEAM CORRECTIONS (2026-08-25) — see RED_TEAM.md

Three rows above do not survive the field research as written:

- **Row 4 (record/replay) — DOWNGRADED.** "Nobody else has a portable local replay artefact" is
  FALSE: Midscene (MIT, 14,676★, ByteDance web-infra) caches to `./midscene_run/cache/*.cache.yaml`
  in the customer's repo. The surviving, sharper claim: Midscene's own docs say `aiBoolean`,
  `aiQuery` and `aiAssert` are NEVER cached — they cache navigation and re-run the model for every
  assertion (51s→28s, 1.8×). We cache the assertion itself, so replay is zero model calls, not
  fewer. Rewrite the claim to that precision everywhere it appears.
- **Row 17 (licence) — FLIPPED.** The recommendation to keep the CLI MIT was declined by the founder
  on 2026-08-25; the product is commercial and the MIT claims are stripped. Stop scoring it as a
  win. Market LICENSE §4 instead (tests/recordings/evidence remain the customer's own files) —
  worth more to this buyer than an OSI badge, given Octomind's users lost every artefact when that
  domain lapsed.
- **The evidentiary standard — WEAKENED.** "290/290 tests pass" (now 424) is not proof: this
  codebase has produced three separate green-but-worthless proofs (suggest's 33 tests over a
  non-functional command; a palindromic order-independence test that could not fail; a suite that
  wedged forever on an unclosed keep-alive). Quote mutation-verified guards, not test counts.

**And the framing itself is wrong.** Eight companies in this exact category are dead, dormant,
absorbed or pivoted — Octomind with €4.5M and three years, whose farewell letter says they "didn't
find the market validation." Beating Autonoma on all seventeen rows would not have saved any of
them. See RED_TEAM.md §1 and §6.
