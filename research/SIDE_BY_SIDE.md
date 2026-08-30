# SIDE BY SIDE — the comparison a buyer would actually build, scored against us

Date: 2026-08-30. Written against the code in `/Users/arjun/smolanalytics/cli` at version
`0.16.1`, and against pages fetched from the five vendors today.

**Companion to, and built on:** `THE_CHECKLIST.md` (the row list), `TRIAL_BAKEOFF.md` (the tools
actually installed and run), `HOW_THEY_SELL.md`, `ALLURE.md`, `SCORECARD.md`, `RED_TEAM.md`,
`FIELD_AGENT_FIRST.md`, `FIELD_INCUMBENTS_AND_FREE.md`, `GRAVEYARD_AND_BUYER.md`,
`CROSS_POLLINATION.md`, `WHITESPACE_DB_AND_AI.md`. Nothing established there is re-derived. Where
this file contradicts one of them it says so out loud, in §6, with the evidence.

> **A missing file, noted rather than glossed.** The brief names `AUTONOMA_TEARDOWN.md`. It is not
> in `/Users/arjun/smolanalytics/research/` today (`ls` MEASURED). Every Autonoma cell below that
> would have cited it is instead cited to `AUTONOMA_IN_MOTION.md`, `SCORECARD.md` or
> `THE_CHECKLIST.md` — each of which quotes the teardown's code reads — and is labelled **REPORTED
> (via prior research)** rather than MEASURED, because I did not re-read their source myself.

**Evidence labels.** **MEASURED** = I fetched the page, ran the command, or read the code myself
today. **CLAIMED** = the vendor's own marketing or docs. **REPORTED** = a third party (or an
earlier file in this directory) asserts it. **INFERRED** = my read of the evidence. Anything I
could not establish is **UNVERIFIED** and stays that way; it is never promoted by repetition.

**The buyer this is scored for.** A solo builder, or a 5–50 person startup shipping web apps.
Engineers. Buying with a card, between other work, usually because something broke last week. No
procurement, no QA function, no security questionnaire, no seats. Every "REAL vs CHECKBOX" call
below is made for *that* buyer and would come out differently at 500 people — `THE_CHECKLIST.md` §5
does that arithmetic and it is not repeated here.

---

## §0 — Ground truth on our side, established before any scoring

### 0.1 The test suite: what I can and cannot say today

The brief asks for `npm test` → ~839 green. **I could not complete the run on this machine today,
and I am not going to report a number I did not see.** MEASURED:

```
$ uptime
13:08  up 73 days, 23:04, 2 users, load averages: 161.91 177.05 161.69
$ ps -Ao pid,rss,comm | grep -ic chromium
90
```

Load average **~162** on an 8-core machine, with ninety browser processes alive that are not mine.
I ran `pkill -9 -f chromium` before each attempt as instructed. Three attempts:

| attempt | how | result |
|---|---|---|
| 1 | `npm test`, backgrounded | reached 956 lines of TAP, killed before the summary |
| 2 | `node --test --test-concurrency=3 test/*.test.mjs`, foreground, 600s | 234 lines, timed out |
| 3 | same, detached with `nohup` | still running at the time of writing |

Individual assertions inside it are visibly starved — `a suite of four prints the export line once`
took **66.6 seconds**, `fifty real replays across eight workers` took **13.9s**, and the pool suite
alone runs 34 seconds of real browsers. **INFERRED:** the suite is not broken; the machine is
saturated. **What that costs this document:** the "839 green" line is **UNVERIFIED here**. It is
MEASURED in `THE_CHECKLIST.md` §0 and `HOW_THEY_SELL.md` Part 0, both dated today
(`tests 839 · suites 137 · pass 839 · fail 0`, 151.4–151.8s), and I am relying on those rather than
restating them as my own measurement.

**Zero test failures were observed in any of the three attempts** — every line that printed was a
`✔`, and `grep -c "^not ok"` over all three logs returned 0 (MEASURED). That is a weaker statement
than "839 pass" and it is the true one.

Per `RED_TEAM.md` §3 this hardly matters anyway: *"'424 tests pass' is not the proof it looks
like"* — this codebase has shipped three separate green-but-worthless proofs. **No row below is
scored from the test count.** Each is scored from the code, or from a run in `TRIAL_BAKEOFF.md`.

### 0.2 The nine shipped features, re-verified against the code today

Every one was read in `/Users/arjun/smolanalytics/cli/lib` today (MEASURED). I am recording the
*corrections*, not re-listing the confirmations already in `THE_CHECKLIST.md` §0 and
`HOW_THEY_SELL.md` Part 0.

| Feature | Verified | Correction to the brief's wording |
|---|---|---|
| Parallel execution | `lib/pool.mjs`, 405 lines, header carries its own measured ladder | **The brief says "50 tests 39s → 4.6s at 8 workers". The file says `workers 1 → 39.2s … 8 → 4.9s`, and `TRIAL_BAKEOFF.md` §8 re-measured 54.6s → 6.8s on a different machine.** Quote the **ratio (~8×)**, never the seconds. |
| Cross-browser | `lib/engines.mjs`, `ENGINES = ["chromium","firefox","webkit"]`, engine is part of the pool cache key (`pool.mjs:271`), one engine per suite (`suite.mjs:368`) | True. The load-bearing part is the honesty rule, not the coverage: a recording made on one engine and replayed on another **says so** and the note is *"a note, never a verdict"* (file header) |
| Uploads | `lib/upload.mjs` (502) + `lib/uploadsafe.mjs` (121); fixture fabricated from the input's `accept`, never stored | True. `uploadsafe.mjs` exists because `setInputFiles` accepts a `<label>` and would silently target the wrong node |
| `--seed` | `lib/seed.mjs` (366) + `lib/seedguard.mjs` (145) | True — **and no longer unique, see §6.2** |
| Render guard | `lib/render.mjs` (685); verdict rule in the header: would-be PASS + catastrophic render → `failed`; a `failed` is never softened; `stale`/`errored`/`flaky` never checked | True, and the false-positive claim is exact: `test/render.test.mjs` holds **exactly 10** `HEALTHY` fixtures (ordinary page, dark theme, canvas game with no DOM text, image-only gallery, SVG infographic, near-white print view, content mid-fade, full-viewport cookie banner, late-painting SPA, one-line status page), each asserted `deepEqual(f, [])` (MEASURED, lines 78–125) |
| Auth | `lib/auth.mjs` (549) | True, including the hard half — bounce detected, exactly one repair login, a failed *login* is `errored` not `failed`, creds env-only through `redact()` |
| `--max-calls` | `lib/cost.mjs` (145); `overBudget()` returns *"Nothing is known about whether the app works"*; parsed at `bin:318` | True. Cap is on **calls**, not dollars, "because a call cap is exact and needs no pricing" |
| `--share` | `lib/share.mjs` (937); `postBundle()` sends **no Authorization header at all** when there is no project | True, and re-verified live today: `curl` → **share page HTTP 200** from a terminal with no session and no account (MEASURED) |
| `--since` | `lib/select.mjs` (405) | True, and built on the stated asymmetry: every unknown **runs everything and says why** |

**Two facts about us that constrain what we may write, both MEASURED today:**

- `package.json` → `"license": "SEE LICENSE IN LICENSE"`, and `LICENSE` §3 → **"This is not open
  source software."** Any copy that says MIT is false.
- `npmjs` API → **1,515 downloads in the week 2026-08-23→29**. Per `RED_TEAM.md` §2.3 this measures
  curiosity, not use.

**One thing the brief did not list and the code has:** `lib/frames.mjs` (383 lines) +
`test/frames.test.mjs`. Its header records six measured fixtures showing that before it existed, a
same-origin iframe, a cross-origin iframe, a closed shadow root and a nested iframe were all
**invisible** to `perceive()` — *"not a crash, but a CONFIDENT LIE."* That closes one of
`THE_CHECKLIST.md`'s four still-open items (§6.1 below).


---

## §1 — THE MATRIX

Rows are `THE_CHECKLIST.md`'s row list, in its order, plus six rows §1.4 adds because a real
side-by-side surfaces them and no published grid contains them.

**Columns.** **us** = `smolanalytics@0.16.1`. **Autonoma** = the hosted product at
getautonoma.com plus the BUSL repo. **QA Wolf** = the **self-serve Platform** tier (their
Coverage-as-a-Service half is a different product for a different buyer and is marked *(CaaS)*
where it changes an answer). **Momentic** = free tier + $125 pay-as-you-go. **Playwright agents** =
`@playwright/test` + `npx playwright init-agents`, driven by the buyer's own coding agent, $0.

Every cell is **M** (measured), **C** (claimed by them), **R** (reported by a third party) or **U**
(unverified). A cell with no label is a structural fact visible on the page.

### 1.1 TIER 1 — the rows that decide, for an engineer buying with a card

| # | Row | us | Autonoma | QA Wolf | Momentic | Playwright agents |
|---|---|---|---|---|---|---|
| 1 | **Time from `npx` to first verdict** | **M** `npx smolanalytics test --url … --test "…"`. No account, no App, nothing written to the repo. Missing key = **4 lines and exit 2**, no browser launched, no network touched | **C** homepage: one command, `npx @autonoma-ai/planner@latest`. **R** the timed reality: GitHub App across repos → agent pushes a Dockerfile → hand over `SUPABASE_URL`/`OPENAI_API_KEY` → *"ETA ~1h 13m"* → 28 min in at 3% → 0% → step 1 of 7 failed | **C** "Try for free", self-serve door open. Post-signup path **U** — no account created | **M** docs: `npx --yes @momentic/wizard@latest -y --platform web --editor-tools skills`, then `MOMENTIC_API_KEY` from `app.momentic.ai`. Account required | **M** `npm i @playwright/test` **1.0s** → `npx playwright init-agents --loop=claude` **1.6s**. No account. Then your agent writes the test |
| 2 | **False reds in the trial** | **M** one retry from a clean page; render guard has **zero findings on 10 named healthy-but-odd fixtures**. **Against us:** one unexplained `stale` in seven replays of an unchanging page, unreproduced in six controlled runs, and `settle()` never retries a `stale` | **C** *"no flaky failures from minor layout changes"*. **R** zero files match "flake" in their repo | **C** *"flake-free E2E tests"*; testimonial *"we rarely get a false negative"*. **C (CaaS)** *"zero flakes"*, human-verified failures | **C** "failure recovery" + "auto-healing". **M** and both **bill credits while they run** | **M** `retries` in config, default 0. Nothing AI-shaped to add entropy |
| 3 | **What a fail-then-pass is called** | **M** `flaky` — its own status, never `passed`, exit 0, and the reason names **both halves** (`settle()`, `lib/test.mjs:682-697`) | **R** no flake concept exists in the code | **U** mechanism not published | **U** not published | **M** **`flaky` too**, verbatim: *"'flaky' - tests that failed on the first run, but passed when retried"*. **See §6.3 — this contradicts `THE_CHECKLIST.md`** |
| 4 | **Where tests live / what survives cancellation** | **M** sentences in `tests/*.md`, recordings + evidence plain files in your repo; `LICENSE` §4 claims nothing in them and survives termination. **But M against us:** the recordings execute **only in our licensed runner**, §1's grant is *revocable*, and **there is no Playwright export** | **C** self-hosted "Free, forever". **M** repo licence renders as `NOASSERTION` while the description says *"Open-source testing platform"*. Hosted tests live in their platform | **C** homepage: ***"Playwright and Appium are open source, exportable, and yours to keep"*** and *"you can export them at any time… no vendor lock-in"*. **Best paid cell in this row** | **M** YAML in your repo — but it needs `MOMENTIC_API_KEY`. **M** their own comparison page: *"Ownership of test code: N/A, tests run on Momentic's platform"* | **M** `.spec.ts` in your repo on an Apache-2.0 runner. **The best cell in the matrix. There is nothing to cancel** |
| 5 | **What the meter counts** | **M** **tested pull requests**. $19/mo flat, 100 included, then 10c. *"a pull request your suite ran against, counted once however many times you push to it"*. Cron and terminal runs are never counted. *"No seats, no per-site fee, no tiers"* | **M** credits. **$100 per 150K credits** = $0.000667/credit; **R** ~10 credits per web run ≈ $0.0067. Free 100K credits | **M** **1¢ per AI credit + 15¢ per runner minute**. **C** "No per-seat fees". **INFERRED** arithmetic, not their claim: a 300-test suite at 30s of runner time each = 150 runner-minutes = **$22.50 a run** | **M** **1 credit per test step, *"including steps that AI features generate and run (AI actions, failure recovery, auto-heal)"***. Free 2,000/mo (~200 runs); $125/mo for 10,000; overage $0.01875/credit | **$0.** You pay your model provider for the authoring agent, and nothing for execution |
| 6 | **Auth: login, session reuse, expiry** | **M** `--login "<sentence>"` records one login per suite; storage state reused; **a mid-suite bounce is detected, repaired with exactly one fresh login, and the test re-run**; a failed *login* is `errored` not `failed`; creds from env only, every log line through `redact()`; `pool.mjs` serialises the first test so 8 workers don't make 8 logins | **C** handled | **C** handled | **C** handled. **M** docs only address signing into *Momentic* | **M** first-party `storageState` + global setup, free and well documented. Expiry repair is DIY |
| 7 | **Test data — can it *provide* state** | **M** `--seed <url>` POSTs the run identity to **an endpoint the customer wrote**; the flat JSON it returns becomes `{{placeholders}}`; a seed failure is `errored`, never `failed` | **C, and the same architecture:** *"connect your own create and delete functions through our SDK, so Autonoma seeds and tears down exactly like your app does."* **See §6.2 — this contradicts `THE_CHECKLIST.md` row 7** | **U** not published | **U** not published | **M** `tests/seed.spec.ts` is written by `init-agents`; fixtures are yours to write, in code |
| 8 | **Per-PR gate: runs, posts once, exits correctly** | **M** one comment edited in place via an idempotency marker; **only `failed` exits 1, `stale`/`errored` exit 2** (`exitFor`, `lib/suite.mjs:513-527`), so our own outage never reddens their build. Runs on their runner with the `GITHUB_TOKEN` Actions already provides | **C** results in PRs, via a GitHub App | **C** CI integration | **M** docs: runs "on your laptop, in a CI pipeline, on a cloud agent sandbox"; example workflows for Actions/CircleCI/Bitrise | **M** your CI, your workflow. The PR comment is DIY |

### 1.2 TIER 2 — real, but they decide renewal rather than purchase

| # | Row | us | Autonoma | QA Wolf | Momentic | Playwright agents |
|---|---|---|---|---|---|---|
| 9 | **Evidence attached to a failure** | **M** screenshot + page text to `.smolanalytics/evidence/`, on failure and flake only, named in the step summary, uploaded as a CI artifact; plus `lib/suspect.mjs` naming the changed files with the string that connects each to the test. Evidence can never change a verdict | **C** dashboards. **R** their own README: ~48% of classifications have no recording, 97% of those executed no steps | **C** "video playbacks" of failures | **C** dashboard, 30-day retention on free | **M** **Trace Viewer** — time-travel DOM snapshots, network, console, sources. **The best in the field, and free.** We lose this row |
| 10 | **Suite wall-clock at the size you'll have** | **M** two independent measurements of the same 50-test suite: `pool.mjs` header **39.2s → 4.9s at 8 workers, 917MB peak vs 2233MB** for a browser per test; `TRIAL_BAKEOFF.md` §8 on another machine **54.6s → 6.8s, 8.03×**. The ratio reproduces; the seconds do not | **C** "Managed" parallel execution | **C** "unlimited parallel runs". **C** testimonial: *"The tests run in 11 minutes. There's about 300"* — three checkable numbers, the best social proof in the field | **U** not published | **M** free, built-in, mature `workers` |
| 11 | **Maintenance model — who fixes it when the UI changes** | **M** we **do not self-heal**. A recording that stops fitting becomes **`stale`** — never red, never worded as a failure — and the agent re-records from the sentence. In a vendor grid this reads as a blank cell | **C** *"When your frontend evolves, tests adapt automatically — no maintenance, no rewriting selectors."* **R** revert-on-failed-rewrite is the one discipline worth copying | **C (CaaS)** they maintain the suite for you — the only *accountable* answer in the row | **C** auto-heal, **M** and it bills a credit per healed step | **M** healer agent, and **its own shipped file says**: *"do the most reasonable thing possible **to pass the test**"*, with `test.fixme()` as an accepted terminal state. **M** docs: *"Re-runs the test until it passes or until guardrails stop the loop"* |
| 12 | **Whose model key pays, and can the bill run away** | **M** the customer's key. Tokens always from the API's own `usage` block, **never estimated**; dollars only when `SMOLANALYTICS_PRICE_IN/_OUT` are supplied; **`--max-calls` caps calls, not dollars, and exits 2 — never a verdict about the app** | **C** their key, answered with their price. Legitimate | **C** their key, priced at 1¢/credit | **C** their key, priced per credit | **M** **your coding agent's key, no cap, no accounting, no ceiling.** The worst cell in this row |
| 13 | **Will you exist in twelve months** | **M** one person, no funding disclosed, **1,515 npm downloads/week**, **zero external proof of any kind** — no G2, no stars, no logo, no case study. Our worst row and structurally unwinnable | **M** repo: 190★, 48 forks, 1 open issue, pushed 2026-08-28. **C** Vercel/Mercor/Superhuman/Kavak logos; **C** a named Guillermo Rauch quote. **R** Bessemer-led pre-seed | **C/M** **G2 4.8 with 100+ reviews**, displayed on their own page. The strongest third-party proof anyone here has | **C** Notion, Webflow, Runway, Quora, Retool logos + **five case studies with a hard number each**. **R** $19.2M raised | **Microsoft.** Row closed |
| 14 | **Security review: SOC 2, SSO, data handling** | **M** `smolanalytics.com/security` → 200, and it says out loud *"We can't show you a compliance badge or a decade of history"*, then names four subprocessors. **No SOC 2, no SSO, no trust center** | **C** "SOC 2 Type II Certified", SAML SSO **on the free tier**, AES-256 / TLS 1.3, VPC peering. **M against them:** `getautonoma.com/trust` **404**, `/security` **404** | **M** `trust.qawolf.com` → **200**. (`/security` 404s; the trust center is the real page) | **M** `trust.momentic.ai` → **200**. **C** SOC 2 Type 2, SAML SSO, 99.99% uptime SLA | n/a — it is a library you run yourself |
| 15 | **Does it work on pages an LLM wrote** | **M** **nothing.** Our replay proof is `text.includes(proof)`, which is wrong by construction for non-deterministic output | **U** their perception is a vision model, which may be tolerant; nothing published says so | **U** | **C/R** semantic assertions in GA | **M** `expect` is exact. Nothing semantic first-party — but the healer will happily rewrite the expectation |

### 1.3 TIER 3 — the checkboxes. Scored, then set aside

`THE_CHECKLIST.md` §3.2 establishes each of these is a CHECKBOX **for this buyer**, with evidence.
They are here because they appear on every vendor grid, not because they decide anything.

| # | Row | us | Autonoma | QA Wolf | Momentic | Playwright agents |
|---|---|---|---|---|---|---|
| 16 | **Cross-browser** | **M** chromium / firefox / webkit, all three verified launching; one Chromium-made recording replayed **green on all three**, each cross-engine replay printing which engine actually checked it | **C** "real browsers"; their own grid claims Chromium/Firefox/WebKit | **C** "Chrome, Firefox, and WebKit" | **M** their docs: **"Web tests run on Chromium."** No Firefox, no WebKit — with Notion and Retool on the logo wall | **M** all three, first-party |
| 17 | **Native mobile** | **M** **nothing** | **C** "supports mobile devices"; **R** iOS 200 / Android 40 credits in their billing code | **C** "Android phones & tablets", "iPhones & iPads", Electron; Appium | **M** priced on the page: **8 credits/min Android, 15 credits/min iOS**; 30 free minutes/month | **M** none |
| 18 | **"AI-powered test generation"** | **M** `smolanalytics suggest` — a real crawl, and **a proposal whose quote appears on no visited page never becomes a file** | **C** planner maps the app; **R** 500 credits ≈ $0.333 per generated test | **C** yes | **C** yes | **M** **free**, first-party planner + generator. **The market has priced this row at zero** |
| 19 | **Non-technical authoring** | Our test *is* a sentence — which inherits testRigor's most-cited developer objection | **C** "No QA team required" | **C (CaaS)** they do it | **C** plain English | no |
| 20 | **Managed preview environment per PR** | **M** `lib/preview.mjs` **discovers** the PR's own preview deployment from the deployments API and **never guesses**; not found in 4 min = `errored`, exit 2 | **C** builds one. **R** it is where their runs die | **C** their infra | **C** their infra | your CI |
| 21 | **Visual / pixel regression** | **M** no baselines, by choice. `lib/render.mjs` guards the much smaller thing underneath: *did the page render at all* | **R** vision perception sees this class | **C** video | **C** visual testing, Chrome only per a competitor's review | `toHaveScreenshot()`, first-party |
| 23 | **Language flexibility / component / API testing** | no | **C** "Any (reads codebase)" | **C** Playwright + Appium | **C** web + mobile | **M** JS/TS/Python/Java/C#, component testing experimental |

### 1.4 The six rows no published grid contains — and they are where the answers separate

Each of these is a question a buyer can run in an afternoon and no vendor comparison page asks.
`HOW_THEY_SELL.md` Part 1 records the reason: across seven rival comparison pages read in full,
**flake, trust, false positives, evidence and exit codes appear in none of them.**

| # | Row | us | Autonoma | QA Wolf | Momentic | Playwright agents |
|---|---|---|---|---|---|---|
| A | **Is there an exit code that means "our fault, not your app"** | **M** **yes — 3 codes.** `0` pass, `1` your app is broken, `2` we could not tell. `stale`, `errored`, a missing key, a browser that won't start, a seed endpoint that is down and `--max-calls` **all exit 2** | **no** — a platform outage is their check failing on your PR | **no** | **no** | **M** **no.** Playwright exits 1 for its own infrastructure failures exactly as for your bug |
| B | **Does a would-be PASS get overturned when the page did not render** | **M** **yes.** `lib/render.mjs`: naked CSS (404'd stylesheet, HTTP status named), blank render (`32 characters of innerText, 0 painted`), framework error overlay **read out of a shadow root**. Verified on four pages that all contain the proof text: `/ok` PASS, `/naked` FAIL, `/blank` FAIL, `/overlay` FAIL | **U** | **U** | **U** | **M** **no.** A passing assertion passes. `expect(locator).toBeVisible()` on a page with no CSS is green |
| C | **Can a non-developer open the run without an account** | **M** **yes.** `--share` → one URL, `postBundle()` sends **no Authorization header** when there is no project, ~25 redaction passes run before anything leaves the machine. Verified live today: **HTTP 200 from a terminal with no session** | dashboard, login | dashboard, login | dashboard, login | **M** an HTML report you must host yourself |
| D | **Does the failure say what the page said** | **M** **yes**, and this is the product thesis on one bug: ours quotes *"Something went wrong. Your order was not placed."*; **M** Playwright's, same page same bug, is `Error: element(s) not found` and the page's words appear **nowhere in the terminal** | **C** yes (vision) | **C** yes | **C** yes | **M** **no** — precise about what it looked for, silent about what it found |
| E | **What does one test cost on the wire** | **M** **16,414 bytes** across 3 calls, **0 screenshots sent to any model.** Perception is `ariaSnapshot()` — 389 characters for the page with the bug | **R** vision model + an image per step | **C** their compute | **C** their compute | **M** 0 — no model in the runtime at all |
| F | **Data safety on a URL you typed wrong** | **M** every generated value carries the `smoltest` prefix (*one `LIKE 'smoltest%'` finds everything any run ever created*); emails default to RFC-2606 `example.com` so a signup can never reach a stranger; a production-looking URL asks a human once and **never blocks CI**; `--teardown <url>` fires even after a failure. None of it may change a verdict | **C** SDK create/delete | **U** | **U** | **M** **none.** An agent told "test signup" signs up |


---

## §2 — EVERY ROW WE LOSE, what closing it costs, and close-or-refuse

**How the day estimates are calibrated.** Between 2026-08-24 and 2026-08-30 this codebase shipped
nine features totalling roughly 4,000 lines of `lib/` plus their suites: `pool` 405, `engines` 172,
`upload` 502 + `uploadsafe` 121, `seed` 366 + `seedguard` 145, `render` 685, `auth` 549, `cost` 145,
`share` 937, `select` 405. That is **about one shipped feature of that size per day**. Every
estimate below names the shipped feature it is calibrated against, so the number is a comparison
rather than a guess.

**Refusing is a result, not a failure.** Five of the twelve below should be refused, and the reason
each is refused is written in the buyer's own words rather than ours.

### L1 — Row 4. **Nothing you can run after you cancel.** ← the biggest loss in this document

**Where we stand (MEASURED today).** `LICENSE` §4 says the tests, recordings and evidence are yours
and survives termination. True, and insufficient: a recording is
`{"startUrl":…,"steps":[{"kind":"click","role":"button","name":"Add to cart"}],"proof":"1 item in
your cart","engine":"chromium"}`, and **the only program on earth that executes it is our runner**,
under a licence whose §1 grant is *revocable* and whose §7 terminates it on breach. `grep` over
`README.md` finds no export of any kind.

Meanwhile: **QA Wolf** — *"Playwright and Appium are open source, exportable, and yours to keep."*
**Playwright agents** — the artefact *is* a `.spec.ts` on an Apache-2.0 runner; there is nothing to
cancel. Both cells are better than ours, and this is the single most-repeated demand in every HN
launch thread in `GRAVEYARD_AND_BUYER.md` §3: *"Does it output playwright scripts?"*

**Cost to close: 2–3 days.** `smolanalytics export` over the recording format is a pure
transformation — `{kind:"click", role, name}` → `await page.getByRole(role, {name}).click()`, the
proof → `await expect(page.getByText(proof)).toBeVisible()`, `startUrl` → `page.goto`, `engine` →
the project. Calibrated against `lib/select.mjs` (405 lines), which is the same shape of work: read
the recording, transform, print, and be honest about what the output does not carry (an exported
spec loses the render guard, the flake status and the exit-2 contract — say so **in a comment at
the top of the generated file**).

**RECOMMENDATION: CLOSE.** In the buyer's words: *"if I cancel today, does anything still run
tomorrow?"* Today our honest answer is *"your files are yours, and nothing but us can read them"* —
which is Octomind's answer, and `octomind.dev` is still NXDOMAIN. The obvious objection, that an
export makes churn cheaper, is exactly backwards for this buyer: `GRAVEYARD_AND_BUYER.md` §4
establishes that **reversible decisions get made fast**, which is the only speed available at 5–50,
and the vendor that made escape expensive — Cypress, blocking deploysentinel.com — is the
cautionary tale, not the model.

### L2 — Row 9. **Evidence: Playwright's Trace Viewer is better than ours and free**

**Where we stand.** A PNG and a page-text file on failure and flake, named in the step summary,
uploaded as a CI artifact, plus `lib/suspect.mjs` naming changed files with the string that
connects each one. That is good. It is not time-travel DOM snapshots with network, console and
sources, which is what a developer gets from `npx playwright show-trace` for nothing.

**Cost to close: ~1 day, and we do not have to build it.** Playwright is already our driver.
`context.tracing.start({screenshots:true, snapshots:true})` on the failure path and `stop()` into
`.smolanalytics/evidence/…/trace.zip` is a small change on an existing code path, calibrated
against `lib/cost.mjs` (145 lines), which was likewise a matter of keeping data already flowing
through `think()`.

**RECOMMENDATION: CLOSE.** Refusing to *build* a trace viewer is correct; refusing to *emit a
trace* is leaving the field's best debugging artefact on the floor when we already have the
dependency loaded.

### L3 — Row 13. **Will you exist in twelve months** — not closeable, and the proof vacuum is

**MEASURED today:** one person, no funding disclosed, 1,515 npm downloads/week, and — per
`HOW_THEY_SELL.md` Part 6 — **we are the only page in this research with no external proof of any
kind.** Every rival has at least one checkable artefact: QA Wolf a G2 4.8 over 100+ reviews,
Momentic five numbered case studies and eight logos, Autonoma a named Rauch quote, Playwright
Microsoft.

**The row itself: REFUSE.** It cannot be closed by a small vendor, only neutralised — and L1 is how
you neutralise it. **The proof vacuum, though, is closeable in ~1 day** by publishing what
`TRIAL_BAKEOFF.md` already measured: seven tools, one app, one planted bug, quoted output, wire
bytes, exit codes. That is a number about our own software rather than a customer's, which is
weaker than a case study and much stronger than the nothing currently on the page. And per
`RED_TEAM.md` §2.3, the metric to instrument internally is **suites that ran more than once this
week** — a replay is proof somebody kept it — not downloads.

### L4 — Row 14. **SOC 2, SSO, trust center**

**MEASURED:** we have none. `trust.qawolf.com` 200, `trust.momentic.ai` 200, Autonoma claims SOC 2
Type II with `getautonoma.com/trust` returning **404**.

**RECOMMENDATION: REFUSE SOC 2. CLOSE the SSO cell with a sentence, 0 days.** SOC 2 Type II is an
observation window measured in months plus an auditor, and `THE_CHECKLIST.md` row 14 shows it
decides nothing below ~30 people. SSO is not a missing feature for us — **we have no seats, so
there is nothing to single-sign-on into** — and a blank cell reads as a "no" while one sentence
reads as an answer. In the buyer's words: *"there are two of us and we both have the password."*
The one thing to protect: our `/security` page returns 200 and says what we lack. **Never put a
compliance badge on a page until the page behind it exists** — that is the exact finding we mark
Autonoma down for, and it would be worse coming from us.

### L5 — Row 15. **Pages an LLM wrote** ← the #1 item on the behind-list

**MEASURED:** nothing. `text.includes(proof)` is structurally wrong for a page whose text is
non-deterministic by design, and our ICP ships those. Momentic and mabl have semantic assertions in
GA.

**Cost to close: 3–5 days** for the version that is defensible rather than the version that is
fashionable. Not "ask a model whether the page looks right" — that puts a model back in the replay
path and destroys the only row nobody can copy (§4.1). The shape that survives: the recording
stores a **predicate** beside the literal proof — *an element with role `status` whose text is
non-empty and over N characters*, *no element matching `[role=alert]`*, *the same element still
exists and still has text* — and replay passes on either the literal string **or** the predicate,
saying which one it used. Calibrated against `lib/render.mjs` (685 lines), which is precisely this
job: deterministic predicates over a live page, with a false-positive budget defended by named
fixtures.

**RECOMMENDATION: CLOSE.** It is the one place on the whole board where we would be first rather
than catching up, and it does not cost a model call.

### L6 — Row 17. **Native mobile**

**MEASURED:** nothing, against Momentic's published per-minute mobile rates, QA Wolf's real devices
and Autonoma's iOS/Android credit lines.

**RECOMMENDATION: REFUSE the native half, out loud in the docs. CLOSE the viewport half, ~1 day.**
In the buyer's words: *"we ship a responsive web app — I need to know it works on a phone-sized
screen, not on a device farm."* Playwright device descriptors give `--device "iPhone 15"` for
roughly the cost of `lib/engines.mjs` (172 lines), and it covers the mobile surface our ICP
actually ships. Native means Appium, a device farm and the end of zero dependencies — and it is a
*disqualifier* only for a buyer who ships an app-store binary, who is not our buyer.

### L7 — Row 11. **Self-healing** — a blank cell we should fill with a sentence, not a feature

We have an answer; it does not fit the cell competitors defined. **REFUSE to build silent healing**
— the field's own evidence is against it (Autify: *"self-healing can mask real regressions"*;
Microsoft's shipped healer file: *"do the most reasonable thing possible to pass the test"*, with
`test.fixme()` as an accepted end state). **CLOSE the copy gap, 0 days:** in that cell we write
*"No. We never silently change what is asserted. A recording that stops fitting is `stale` — never
red, never green — and the sentence is re-recorded."* In the buyer's words: *"I don't want a robot
editing my assertions while I'm asleep."*

### L8 — Row 5. **The meter, against $0**

Playwright's agents cost nothing and come from the platform vendor. **REFUSE to compete on price.**
The narrow claim that survives, and it is still true: **among the tools you pay for, ours is the
only meter that does not grow when you test more carefully.** Theirs grows with how much you test;
ours grows with how much you ship. Never say *"we're cheap"* and never again say *"their price is
zero"* — Autonoma published one (§6.4).

### L9 — Rows 18, 19, 20, 21, 23. **The checkboxes**

**REFUSE, all five.** Generation is free from Microsoft; non-technical authoring has no persona at
5–50 and carries its own churn quote; managed previews are a capability our buyer already has from
Vercel; visual baselines are Rainforest's documented failure mode; toolchain breadth is how a small
vendor accidentally becomes Katalon. `THE_CHECKLIST.md` §3.2 carries the evidence for each.

### L10 — The small loss inside a row we win: **dollars, not just tokens**

Shortest prints `↳ 7,560 tokens (≈ $0.03)` next to the test that spent it. We print
`6 model calls · 7,200 in / 360 out` and then *instructions for how to configure dollars*. Theirs
is strictly better. **Cost: half a day** — ship a default price table for the models we call and
keep `SMOLANALYTICS_PRICE_IN/_OUT` as the override. **CLOSE.** It is the cheapest high-value item
in this document.

### L11 — The wound we found in ourselves: **one unexplained `stale` in seven replays**

`TRIAL_BAKEOFF.md` §8 records it and does not bury it: on the seventh replay of an unchanging page,
the verdict was `stale` — *"the page no longer says 'Order placed'"* — on a page that `curl`
confirmed was serving that exact text, and it did not reproduce in six controlled runs. It matters
because `settle()` in `lib/suite.mjs` only arbitrates `passed`/`failed` attempts, so **a `stale` is
never retried**: a one-off goes straight to exit 2 with no second look. If it is real, that is a red
CI job on a working app — the exact false-alarm class this whole product exists to avoid.

**Cost: 1–2 days.** Run the same replay 500 times; if it reproduces, give `stale` the same second
look `failed` gets. **CLOSE, and close it before anyone quotes a replay-reliability number.**

### The refusals, collected

| Refused | Because, in the buyer's words |
|---|---|
| SOC 2 Type II | *"There are two of us. Nobody is going to read a report."* |
| Native mobile | *"We ship a responsive web app."* |
| Silent self-healing | *"I don't want a robot editing my assertions while I'm asleep."* |
| Visual baselines | *"Every design tweak becomes a hundred screenshots to re-approve."* |
| Managed preview environments | *"Vercel already gives me a URL per PR."* |
| Non-technical authoring | *"There is no non-technical person here."* |
| Competing on price with free | *"Playwright is free and already installed."* |


---

## §3 — EVERY ROW WE WIN, split into the two lists that matter differently

A side-by-side is decided in an evening. `THE_CHECKLIST.md` §5 establishes it: at 5–50 there is no
evaluation *process*, there is one sitting, by the person who got paged last week. So a win that
only shows up in month two is not a win in the comparison — it is a reason not to churn, which is a
different and later thing.

**The finding this section exists to deliver, stated before the lists:** of the nine features
shipped in the last week, **six are month-two wins** (parallelism, auth, seed, `--since`, uploads,
cross-browser) and **three are thirty-minute wins** (the render guard, `--share`, the cost line).
We have been building the second list. The first list is what a side-by-side is decided on.

### 3.1 — Wins a buyer will NOTICE inside a 30-minute evaluation

Each of these is visible without a suite, without CI, without a second run.

| # | The win | What they see, MEASURED | What they'd have seen elsewhere |
|---|---|---|---|
| N1 | **The first sixty seconds when something is missing** | No key → **four lines and exit 2**. No browser launched, no network touched, and the last line says *"Replaying a recording (`--plan`) needs no key at all."* | Midscene's correct message buried under ~60 lines of `Symbol(step)` wreckage; Shortest's uncaught `ExitPromptError` **exiting 0**; browser-use retrying an unfixable error six times after installing three Chrome extensions and **exiting 0**; auto-playwright unable to import at all |
| N2 | **The failure sentence quotes the page's own words** | *"On /checkout, after clicking 'Proceed to checkout' from /cart, the page rendered the error 'Something went wrong. Your order was not placed.' … so the shopper cannot complete the purchase."* | **Playwright, same page, same bug: `Error: element(s) not found`.** The words *"Something went wrong"* appear nowhere in its terminal — they are in `error-context.md`, one file open away. It also burned 5 of its 5.7 seconds waiting for an element that was never coming |
| N3 | **Break the app's CSS and watch a green test go red** | `FAIL · replayed 1 step · render check` — *"the page rendered with no CSS at all: its stylesheet returned an error (/missing-styles.css → HTTP 404) … A person opening this page would not call it working, so this is a failure, not a pass."* Four pages that all contain the proof text: `/ok` PASS, `/naked` FAIL, `/blank` FAIL, `/overlay` FAIL | Nobody. A passing assertion passes. And this is the test a careful buyer runs anyway — disqualifier 3 in `THE_CHECKLIST.md` §4 |
| N4 | **The stack frame with no model call** | The error-overlay case reaches into a **shadow root** and hands back `app/checkout/page.tsx:42:19` — **in a run that made zero model calls** | Playwright's output on the same class of bug is `element(s) not found` |
| N5 | **Exit codes they will hit in the first ten minutes** | `0` pass · `1` your app is broken · `2` we could not tell. They will meet `2` immediately (no key) and again the first time a preview is not up | Two of the six tools in `TRIAL_BAKEOFF.md` **exit 0 on a failing test**. Nobody separates "our fault" from "your fault" at all |
| N6 | **A link they can paste into Slack** | `--share` → `https://smolanalytics.com/s/…`, **HTTP 200 from a terminal with no session**, verified again today | Every hosted rival: a dashboard behind a login. Playwright: an HTML report you host yourself |
| N7 | **A price on the page, and one plan** | **$19/mo, 100 tested PRs, then 10c**, *"No seats, no per-site fee, no tiers, nothing else"*, 14-day trial at Pro limits with no card | mabl, qa.tech, Rainforest, testRigor, Meticulous and Spur will not show an engineer a number at all. Rainforest's `/pricing` **301s to `/talk-to-sales`** |
| N8 | **What it leaves behind** | **0 runtime dependencies** (`package.json` has no `dependencies` key), and **28 KB / 3 files** after ~20 runs including a 50-test suite | Midscene 156 MB installed and **8.4 MB / 21 files** growing ~2.8 MB per run; browser-use a 338 MB venv; Playwright's `init-agents` writes **seven files into your repo** including `.claude/agents/` and `.mcp.json` |
| N9 | **Steps with the agent's reason under each, in the terminal** | `✓ 1 click button "Add to cart" 546ms` / `put the Blue Widget in the cart` | Midscene has this — in a 2.8 MB HTML report. Nobody else has it in the terminal |
| N10 | **The retry is disclosed and priced *before* it runs** | *"retrying from a clean page (retry 1 of 1) — another full agent run, roughly doubling this test's cost. `--retries 0` disables it."* Then `(Observed twice: … failed both times.)` | No other tool in the bakeoff tells you a retry happened, let alone what it cost |
| N11 | **A ceiling that refuses to guess** | `--max-calls` → *"stopped at the `--max-calls` ceiling of 4 model calls. **Nothing is known about whether the app works**; raise the ceiling or run this test on its own."* Exit 2 | Midscene reported its own XML parse error to the developer as **`Assertion failed`** |
| N12 | **Cross-engine honesty** | *"This recording was made on Chromium and was replayed on Firefox. The steps and the proof were checked against Firefox this time, so a Firefox-only break in this flow would have shown up here."* | A green tick, and you assume |

**The through-line a buyer will feel without being able to name it:** eleven of those twelve are the
product refusing to overclaim — about a missing key, a retry, a budget, an engine, a page that
technically matched. `ALLURE.md` §1 argues that is the mechanic; this table is the evidence it is
also the *visible* mechanic.

### 3.2 — Wins they will only feel in month two

These are real, they are why a team keeps the tool, and **not one of them survives a 30-minute
comparison**, because each needs a suite, a history, or an incident to become visible.

| # | The win | Why it is invisible on day one |
|---|---|---|
| F1 | **Parallel suite wall-clock** — ~8× at 8 workers, one browser + a context per worker, **917 MB peak vs 2233 MB** for a browser per test | A trial has three tests. The memory number is the one that matters, and only on a 2-core CI runner |
| F2 | **Auth: one login per suite, expiry detected mid-suite and repaired with exactly one fresh login, a failed login is `errored` not `failed`** | The expiry path fires in week three, on a Tuesday, and its whole value is a red suite that *didn't* happen |
| F3 | **`--since` diff-aware selection**, which removes a test only on positive evidence and runs everything on every unknown | Needs a suite, a git history and a bill worth reducing |
| F4 | **`flaky` as its own status over a run history**, and the cloud's flaky-vs-broken over a 10-run window with ≥2 verdict flips | Needs ten runs. On day one it is a word in a help text |
| F5 | **`lib/suspect.mjs` naming the changed file with the string that indicts it** — and saying nothing at all when nothing matches | Needs a real PR that really broke something |
| F6 | **`--seed` / `--teardown`** | Needed by test #6, not test #1. The coverage wall is silent by definition |
| F7 | **Uploads with fabricated, magic-byte-valid fixtures and nothing committed to the repo** | Only the buyer with an import flow ever reaches it |
| F8 | **`smoltest` prefix on every generated value, RFC-2606 emails** | Felt exactly once: the day somebody greps the users table, or the day a welcome email *doesn't* reach a stranger |
| F9 | **One PR comment, edited in place** | Felt on the twentieth push, as the absence of nineteen comments |
| F10 | **The meter: tested pull requests** | Felt at the first invoice, thirty days later |
| F11 | **`lib/frames.mjs` — iframes and shadow DOM** | Felt the day a test reaches Stripe Elements. Before that, its value is a confident lie that never got told |
| F12 | **The instrumentation half** | A second product. Per `HOW_THEY_SELL.md` Part 6 it currently reads, on a page whose first noun is testing, as a second product before the first has been believed |

### 3.3 — The move this section implies: make three month-two wins visible in thirty minutes

Not by building the features again — by **shipping the fixture that demonstrates them.** A
`smolanalytics demo` that drops a 50-test recorded suite and a tiny local app into a temp directory
turns F1, F3 and F4 into things a buyer *runs* rather than reads:

- `--workers 1` then `--workers 8` on their own machine → F1 becomes a number they produced
- one commit, then `--since HEAD~1` → F3 becomes *"0 of 50 ran, and here is exactly why, and skipped
  is not passed"*
- a deliberately flaky fixture → F4 becomes the word `flaky` in their own terminal

Calibrated against `templates/` and `test/fixtures-pool` (both already exist): **1–2 days.** This is
the highest-leverage day in this document, because it moves three wins from the list that does not
decide onto the list that does.

---

## §4 — THE THREE ROWS WHERE WE COULD BE UNCOMPARABLE

The bar for this section: **every rival's honest cell is "no", ours is a demonstrable yes, and the
"no" is structural rather than a backlog item.** Three rows clear it. Two obvious candidates do
not, and are named at the end so nobody re-proposes them.

### U1 — **An exit code that means "our fault, not your app"** (matrix row A)

**Ours, MEASURED.** `exitFor()` in `lib/suite.mjs:513-527`: `failed` → 1; `errored` **or** `stale`
→ 2; otherwise 0. Everything that is our problem lands on 2 — a missing key, a browser that will
not start, a seed endpoint that is down, `--max-calls`, an unreadable recording, a preview that
never appeared. `flaky` exits 0 and warns. The file header states the intent in one line: *"an
outage on our side reads to a customer as their checkout being broken"* — and the whole point of
the third code is that it cannot.

**Why every rival's cell is "no", and structurally so.**

- **Playwright (MEASURED architecture):** exits 1 for its own infrastructure failures — a browser
  that will not launch, a `webServer` that never came up — exactly as it does for your bug. One
  code, two meanings.
- **Autonoma, QA Wolf, Momentic:** the runner *is* their cloud. From the buyer's CI there is one
  call, and a platform outage and a broken checkout are the same red check. **A hosted runner
  cannot make this distinction on the customer's behalf**, because it does not own the process that
  reports it. This is the row where our architecture — their runner, their key, our binary — is not
  a cost-saving, it is the only way the promise is available at all.

**Demonstrable in ten seconds:** `env -u ANTHROPIC_API_KEY npx smolanalytics test --url … --test
"…"; echo $?` → `2`.

**The honest caveat.** Playwright *could* add this tomorrow; nobody has. And a vendor whose runner
is their own cloud has a commercial reason not to want a code that says *"this red is ours."*

### U2 — **A would-be PASS overturned because the page did not render** (matrix row B)

**Ours, MEASURED.** `lib/render.mjs`, 685 lines, with the verdict rule in the header: a would-be
PASS plus a catastrophic render becomes `failed`; a `failed` is never softened; `stale`, `errored`
and `flaky` are never checked at all. Verified on four pages that **all contain the proof text**:
`/ok` PASS, `/naked` FAIL (stylesheet 404, URL and status named), `/blank` FAIL (*"12 characters"*
of innerText, nothing painted), `/overlay` FAIL (Next-style error surface read out of a **shadow
root**, handing back `app/checkout/page.tsx:42:19`).

**Why every rival's cell is "no".** The category's entire assertion model is *"did the thing I
asked for happen"*, and a false green is invisible to that model **by construction**: if the
assertion matched, the tool has no further question to ask. Overturning your own pass is something
you have to deliberately want, and it makes a demo look *worse* — more reds, on pages the customer
thinks are fine. Playwright: MEASURED, `expect(locator).toBeVisible()` is green on a page with no
CSS at all. Autonoma, QA Wolf, Momentic: nothing published either way — **UNVERIFIED**, and I am
not going to score a "no" I cannot show, so their honest cell is *"not published; ask them to break
their own stylesheet in your trial."*

**Demonstrable in sixty seconds**, and it is the test a careful buyer already runs — disqualifier 3.

**The honest limits, which must ship with the claim.** It is a *catastrophe* guard, not a visual
check. Every threshold is set where it cannot fire on a working page — 10 named healthy-but-odd
fixtures, zero findings each — which means **it will miss milder breakage**, on purpose: *"a missed
catastrophe costs one bad run, a false catastrophe costs the customer."* And `--no-render-check`
turns it off, which is the sentence that makes the default defensible.

### U3 — **A run anyone can open, with no account** (matrix row C)

**Ours, MEASURED.** `--share` publishes one page per run. `postBundle()` sends **no Authorization
header at all** when there is no project — the anonymous path is the designed case, not the
degenerate one — and roughly twenty-five redaction passes run over the bundle first (masking pairs,
the process's own environment, and a pattern sweep for anything bearer-shaped). It is opt-in,
assembled after the verdict, and **cannot change a verdict or an exit code**. Verified live today:
**HTTP 200 from a terminal with no session and no account.**

**Why every rival's cell is "no".** For Autonoma, QA Wolf and Momentic the login **is** the product
boundary; an account-less public page gives away the thing the account exists to sell. For
Playwright there is no server to publish to — its HTML report is a directory you host yourself.
**We are the only entrant with a server and no commercial reason to gate it**, which is a
consequence of the meter (§1.1 row 5): we bill tested pull requests, so a page nobody is billed for
costs us nothing and travels.

**Demonstrable:** open the link in a private window. **The one thing that must ship beside it:** the
screenshot is pixels of the customer's own page and **cannot be masked** — the page text beside it
is, the image is not, and the CLI says so when it attaches one. Never let that sentence fall out of
the copy.

### The two that do NOT clear the bar, named so they are not re-proposed

- **"Replay with no model key."** `TRIAL_BAKEOFF.md` §9.3 ranks this our #1 win and it is — *within
  that bakeoff*. It is **not uncomparable in this matrix**, because two of the five columns beat it
  outright: QA Wolf exports standard Playwright, and Playwright's agents *are* `.spec.ts` on an
  Apache-2.0 runner. Both run forever with no model and no vendor. The precise surviving claim is
  narrower and still ours (§6.5).
- **The instrumentation half.** Genuinely uncomparable — no testing vendor has an analytics product
  — and genuinely invisible in a thirty-minute testing evaluation. `HOW_THEY_SELL.md` Part 6 records
  the cost of leading with it: on a page whose first noun is testing, it reads as a second product
  before the first has been believed. Keep it; do not put it in the comparison.


---

## §5 — THE ONE-PAGE COMPARISON WE COULD PUBLISH

Written in the format we would actually ship it, at `smolanalytics.com/compare`. Every claim is
traceable to a cell above; the *(source)* markers are the footnotes that would ship with it.

**Three rules it obeys, taken from what the field does badly.** (1) It carries **only rows a buyer
can check in an afternoon** — no row we would have to be believed on. (2) It states **our own
losses in our own table**, because `HOW_THEY_SELL.md` Part 1 records that Autonoma's tables never
once compete on whether the answer is correct, and a table with no losses is read as a sales deck.
(3) Every rival number is **dated and quoted**, and where it is REPORTED it says so — Autonoma
labels its own QA Wolf cost estimates as third-party, and that practice is worth copying.

---

### > Nine questions to ask any end-to-end testing tool

*Everything below was checked on 2026-08-30, from each vendor's own page or by running the tool.
Where we lose, the row says so.*

**1. What happens in the first sixty seconds, when something is missing?**

> **us** — four lines and exit code 2. No browser launched, no network touched. `npx`, no account,
> nothing written to your repo.
> **Playwright agents** — `npm i @playwright/test` (1.0s), `npx playwright init-agents` (1.6s), and
> seven files are written into your repo including `.claude/agents/` and `.mcp.json`.
> **Momentic** — `npx @momentic/wizard`, then an account and a `MOMENTIC_API_KEY`.
> **Autonoma** — one command on the homepage; the onboarding we timed installed a GitHub App across
> our repositories, pushed a Dockerfile into the code, asked for `SUPABASE_URL` and
> `OPENAI_API_KEY`, said *"ETA ~1h 13m"*, and failed at step 1 of 7. *(our own run, Aug 2026)*
> **QA Wolf** — self-serve "Try for free". We did not create an account, so we are not going to
> describe what happens after you do.

**2. When a test fails and then passes on retry, what do you call it?**

> **us** — `flaky`. Its own status, never a pass, and the reason names both the failure and the pass.
> **Playwright** — also `flaky`, verbatim in its docs. *This is a tie, and we are not going to
> pretend otherwise.*
> **Autonoma, QA Wolf, Momentic** — not published. Ask them.

**3. If a test passes but the page rendered blank, unstyled, or under a crash overlay, what do you
say?**

> **us** — `failed`, and the reason names the evidence: the stylesheet's URL *and its HTTP status*,
> or the character count of the text nobody could see, or the framework error read out of a shadow
> root — which on a Next.js crash hands you back your own `app/checkout/page.tsx:42:19`. Verified on
> four pages that all contained the expected text: one passed, three failed.
> **everyone else** — a passing assertion passes. **Break your own stylesheet during the trial and
> watch.**

**4. Is there an exit code that means "our fault, not your app"?**

> **us** — yes, three. `0` pass · `1` your app is broken · `2` we could not tell. A missing key, a
> browser that will not start, an unreadable recording, a preview that never appeared and a budget
> ceiling all exit **2**, so our bad day never reddens your build.
> **Playwright** — exits 1 for its own infrastructure failures too.
> **the hosted tools** — the runner is their cloud; from your CI, their outage and your bug are the
> same red check.

**5. What does the meter count?**

> **us** — **tested pull requests.** $19/month, 100 included, then 10c. Push to a PR five times and
> it is one. Terminal runs and cron runs are never counted.
> **Momentic** — one credit **per test step**, *"including steps that AI features generate and run
> (AI actions, failure recovery, auto-heal)"* — their words. Free 2,000/mo; $125/mo for 10,000.
> **QA Wolf** — 1¢ per AI credit and **15¢ per runner minute**.
> **Autonoma** — credits, $100 per 150K.
> **Playwright agents** — free. *You should try them first.*

**6. If I cancel today, does anything still run tomorrow?**

> **QA Wolf** — *"Playwright and Appium are open source, exportable, and yours to keep."*
> **Playwright agents** — the tests are `.spec.ts` on an Apache-2.0 runner. Nothing to cancel.
> **us** — **we lose this row today.** Your sentences, recordings and evidence are plain files in
> your repo and our licence claims nothing in them — but only our runner executes a recording.
> An export is on the list; until it ships, this row is theirs.
> **Momentic** — their own comparison page: *"Ownership of test code: N/A, tests run on Momentic's
> platform."*

**7. Whose model key pays, and what stops the bill?**

> **us** — your key. Token counts come from the API's own usage block and are never estimated;
> `--max-calls` caps **calls, not dollars**, because a call cap is exact and needs no price table,
> and hitting it exits 2 with *"Nothing is known about whether the app works."*
> **Playwright agents** — your coding agent's key, no cap and no accounting.
> **the hosted tools** — their key, and they answer this with their price. That is a fair answer.

**8. Can someone without an account open the result?**

> **us** — yes. `--share` gives you one URL per run; open it in a private window. Off unless you ask
> for it, it cannot change a verdict, and about twenty-five redaction passes run before anything
> leaves your machine. The screenshot is pixels of your own page and **cannot be masked** — the text
> beside it is.
> **everyone else** — a dashboard behind a login, or an HTML report you host yourself.

**9. Where do you lose?**

> **Native mobile** — we have none. If you ship an app-store binary, use QA Wolf or Momentic.
> **Debugging artefacts** — Playwright's Trace Viewer is better than our screenshot and page text,
> and it is free.
> **Pages an LLM writes** — our proof is the page's text, so a page whose text changes every run is
> not something we handle yet. Momentic does.
> **SOC 2, SSO, a trust center** — none. Our security page says so in the first line.
> **How long we have existed** — one person, no funding to announce. The only answer we can give is
> question 6, which is why it is on the list above us.

---

### 5.1 — What we must NOT claim

Each of these was true in some earlier document in this directory and is **false, unproven, or
refutable today.** They are listed with what killed them.

| Do not say | Because (MEASURED 2026-08-30 unless noted) |
|---|---|
| **"MIT"**, or **"open source"**, or **"self-host forever"** | `package.json` → `"license": "SEE LICENSE IN LICENSE"`; `LICENSE` §3 → *"This is not open source software."* The README's old MIT line is gone; if any copy still carries it, it is false |
| **"Cancel us and every test still runs"** | Only our runner executes a recording; §1's grant is revocable. Until an export ships, QA Wolf and Playwright own this row (§2 L1) |
| **"50 tests, 39s → 4.6s"** | `lib/pool.mjs` says **39.2s → 4.9s**; a re-measurement on another machine gave 54.6s → 6.8s. **Quote the ratio (~8× at 8 workers), or quote the file and name the machine** |
| **"We're the only tool that calls a fail-then-pass flaky"** | **Playwright does too**, verbatim in its own docs. Tie, and say so |
| **"No account, your own key, `npx` — that's our differentiator"** | Shortest gave that away free under MIT with 5,666 stars and still went dormant; QA Wolf, Momentic and Reflect all have a no-call path today. Free is table stakes in 2026 |
| **"Their price is zero / it's a subsidy"** | Autonoma published **$100 per 150K credits** — exactly the rate in their own billing code. The surviving claim is about the *unit*, not the number |
| **"Nobody else has a portable local replay artefact"** | Midscene caches to a plain file in the customer's repo. The precise claim is §6.5 |
| **"Only we let your own app build its test data"** | Autonoma's homepage: *"connect your own create and delete functions through our SDK, so Autonoma seeds and tears down exactly like your app does"* (§6.2) |
| Any **SOC 2 / compliance badge** | We have none — and a badge with a 404 behind it is the exact finding we mark Autonoma down for. `getautonoma.com/trust` → 404 |
| **"839 tests pass"** as evidence of quality | `RED_TEAM.md` §3: this codebase has produced three green-but-worthless proofs, including 33 passing tests over a command that could not write a file. Quote **mutation-verified guards** instead. I could not even complete the run today (§0.1) |
| **npm downloads as traction** | 1,515/week. auto-playwright pulls 13,207/week into a repo untouched for thirteen months. The honest metric is *suites that ran more than once this week* |
| **"Zero false positives"**, unqualified | Zero findings on **ten named fixtures**. Say the ten. An unbounded claim is refuted by one screenshot |
| Anything about **replay reliability** | One unexplained `stale` in seven replays of an unchanging page, unreproduced. Soak it 500× first (§2 L11) |
| **"Cross-browser is a reason to choose us"** | No buyer evidence in this corpus asked for it, and Momentic ships **Chromium-only** with Notion and Retool on its logo wall. It is a checkbox; we happen to tick it |
| **QA Wolf "$40–44/test, ~$90K ACV"** | REPORTED via a competitor's blog citing G2/Vendr, and their own page now publishes different units. Do not restate it as fact |
| **"Autonoma has no mobile / no SSO / no security story"** | All three are on their homepage today. The true and narrower finding is that their **trust page 404s** |

