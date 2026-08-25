# RED TEAM — the scorecard, attacked

Date: 2026-08-25. Author: the orchestrating session, after four attempts to run this as a subagent
died (three to session caps, one to the machine sleeping). Written against every file in this
directory, `AUTONOMA_TEARDOWN.md`, and the code as it stands at commit `26acb02`.

The founder's bar, verbatim: *"i dont wanna be another one in a 100 yc companies that try and fail
to break into this space with subpar products."* Flattery here is sabotage, so this document leads
with what the research says against us.

---

## 1. The scorecard asked the wrong question

`SCORECARD.md` scores us against Autonoma and returns **we win 9 · tie 5 · they win 3**. Having
re-read it against the field research, I believe the tally is roughly defensible on its own terms
(corrections in §3) — and I also believe **the question it answers barely matters.**

`FIELD_AGENT_FIRST.md` establishes one fact that reframes everything, and it is MEASURED, not
inferred:

**Eight of the agent-first testing companies in this category are dead, dormant, absorbed or
pivoted out.**

| company | fate | date |
|---|---|---|
| Octomind | dead — product off May 2026, company wound down June 2026 | €4.5M seed (Cherry Ventures), 3 years, 15 people |
| ZeroStep | dead — no A record, 0 public repos | npm still serving 6,314/wk into a dead backend |
| CamelQA | pivoted out of QA entirely (now sells an inference API) | 2026 |
| Magnitude | pivoted out of testing (now a local-model agent) | 179-point Show HN, Apr 2025 |
| Reflect.run | absorbed by SmartBear, quote-only pricing | Jan 2024 |
| Shortest (Antiwork) | dormant — 0 commits in 30 days | 5,666★, MIT |
| auto-playwright | dormant — last push Jul 2025 | 846★, 13,207 npm/wk |

Octomind's own farewell letter, recovered from the Wayback machine:

> *"In the end, we didn't find the market validation we needed to keep going."*
>
> *"Testing is not a solved problem. It's barely a studied one… **Someone, somewhere, is going to
> build the right shape of this.**"*

**So the bar is not "better than Autonoma." The bar is "not the ninth name in that table."** A
product can beat Autonoma on all seventeen capability rows and still land there — Octomind did not
die of missing features.

---

## 2. The three findings that should change what we do

### 2.1 Octomind died of its meter, and ours is the structural opposite

This is the most useful thing in the entire research effort, and it is a direct validation of a
decision already shipped.

Octomind's recovered pricing (MEASURED, Wayback snapshot of `octomind.dev/pricing`, three weeks
after the shutdown announcement): **$89/mo for 80 test cases, 240 cloud runs, and — the line to
stare at — 20 AI test creations per month.** That is ~$4.45 per generated test, metering the
*authoring* step, which Microsoft now gives away free in Playwright's own agents. Pro was $589/mo
for 300 test cases: **$0.33/run, and a price that rose with how many tests you owned.**

Their meter punished the customer for growing the suite — the exact behaviour the product exists to
encourage. `GRAVEYARD_AND_BUYER.md` has usage-punishing meters as churn reason #2 field-wide.

Ours: **$19/mo flat, metered on tested pull requests**, runs executing on the customer's CI with the
customer's own model key. Pushing five times to one PR is one unit. Terminal and staging runs are
never counted — the code comment says *"metering them would teach people not to run them."* The
meter scales with shipping, not with thoroughness, and marginal runs cost us ≈$0.

**Verdict: keep this and never negotiate it.** It is the one decision most directly opposed to the
documented cause of death in this category.

### 2.2 Architecture is not a moat — Shortest proves it

`@antiwork/shortest`: MIT, 5,666 stars, 8,898 npm downloads/week. Playwright, natural-language
tests, the customer's own `ANTHROPIC_API_KEY`, run via `npx`.

**That is our architecture, shipped free, by Sahil Lavingia's company — and it has had zero commits
in over three months.**

Two things follow. First, nobody should ever again describe "runs on your own key, no account, npx"
as our differentiator; it is table stakes that a well-known founder already gave away. Second, and
more usefully: *being free and popular did not keep it alive either.* The thing that killed
Shortest's momentum is the thing that kills all of these — nobody owned the unglamorous middle
(flake, evidence, verdict discipline, CI ergonomics) long enough for a team to depend on it.

### 2.3 npm downloads measure curiosity, not use

`auto-playwright` pulls 13,207 downloads/week into a repo untouched for thirteen months.
`@zerostep/playwright` pulls 6,314/week into a company whose domain has no A record.

**This is a warning aimed at us, not at them.** When our own npm numbers start moving, they will be
the easiest metric to quote and the least meaningful. The honest traction metric for this product is
*suites that ran more than once this week* — a replay is proof somebody kept it.

---

## 3. Corrections to the scorecard

Three rows do not survive the field research as written.

### Row 4, record/replay — **downgraded from "the moat row" to "a real but narrower moat"**

The scorecard says our zero-model replay is uncopyable and that nobody else has a portable local
replay artefact. **That is false as stated: Midscene (MIT, 14,676★, 98 commits in the last 30 days,
under ByteDance's web-infra org) caches a replay plan to `./midscene_run/cache/*.cache.yaml` — a
plain file in the customer's repo.** The research file corrected its own first pass on this point
rather than quietly editing it, which is the right instinct.

But the precise version is still ours, and it is sharper for being precise. Midscene's own docs:
**`aiBoolean`, `aiQuery` and `aiAssert` are never cached.** Their replay caches the *navigation* and
re-runs the model for *every assertion* — measured at 51s → 28s, a 1.8× speedup.

Ours caches the assertion itself. The proof string is the recording's assertion, checked with
`text.includes(proof)`, so a replay is **zero model calls, not fewer** — 8.0s → 1.4s on a measured
flow, and $0 marginal rather than a smaller bill.

**Rewrite the claim everywhere it appears.** Not "we have replay and they don't" — that is now
refutable by a fifteen-thousand-star repo. The claim is: *everyone else re-runs the model to decide
whether the page is right; we don't, because a passing run recorded what "right" looked like.*

### Row 17, licence — **flipped from "we win" to "we gave this up on purpose"**

The scorecard's strongest recommendation was to keep the CLI MIT as the sharpest possible contrast
with Autonoma's BUSL-1.1, citing exportability as the field's #1 anti-churn property.

**The founder decided against it on 2026-08-25 — no open source — and it is executed:** a commercial
LICENSE, MIT claims stripped from `package.json` and the README.

I am not relitigating a made decision, and there is a defensible reason for it (Shortest, above,
shows MIT buys stars, not survival). But the row must stop being scored as a win, and one mitigation
already shipped and should be marketed hard: **LICENSE §4 guarantees the customer's tests,
recordings and evidence are their own plain files, and the licence claims nothing in them.** Given
that Octomind's users lost every artefact when the domain lapsed — after being promised the site
would "stick around for a while yet" — that clause is worth more to this buyer than an OSI badge.

### The evidentiary standard — **"424 tests pass" is not the proof it looks like**

The scorecard's ground truth is *"290 tests, 290 pass"* (now 424). Treat that number with
suspicion, because this codebase has now produced **three separate proofs that a green suite can be
worthless**:

1. `suggest` shipped with **33 passing tests while being incapable of writing a single file.** Every
   test scripted the model, and a scripted model answers in whatever shape the assertion expects.
   Found only by running it against a real key.
2. A test asserting order-independence used **palindromic data** — identical read backwards, so the
   property it claimed to test could not fail.
3. The suite **wedged forever** on a `server.close()` whose callback never fires while a keep-alive
   connection is open — a hang that a 20-second per-test timeout could not surface.

**The standard that actually holds is mutation testing**, which this project now does routinely: the
`suspect`, `preview`, `layout` and `flake` work each had every guard broken deliberately to confirm
the right test went red. Quote *that* — "every safety guard is mutation-verified" — not a test count.

---

## 4. The blind spots — dimensions missing from the table entirely

The scorecard has seventeen rows and none of these, each of which a real buyer hits in week one:

| missing dimension | why it bites | what we actually know |
|---|---|---|
| **Suite wall-clock / parallelism** | A 200-test suite run serially is unusable in CI regardless of per-test speed | **Unmeasured.** Our largest verified run is a handful of tests. This is the most urgent gap on this list. |
| **Authenticated flows** | Most valuable tests live behind a login; 2FA/OTP/magic-link blocks them entirely | Partially handled by `--seed`-shaped thinking, but there is no storage-state reuse and no documented auth story |
| **iframes / shadow DOM** | Stripe checkout, embedded widgets, most component libraries | `ariaSnapshot()` behaviour here is **untested by us** |
| **File uploads / downloads** | Any product with an import flow | Unhandled |
| **Cross-browser** | We drive Chromium only | Honest answer: not a priority for this ICP, but say so out loud |
| **A 200-test suite's cost** | Our economics story is per-test; nobody buys per-test | Unmodelled |

**None of these should be built before they are measured.** The first one — run 50 tests and time it
— is an afternoon, and it either validates the economics story or exposes the biggest hole in it.

---

## 5. The economics, head to head

Autonoma's billing code has the meters switched off: `RUN_CONSUMPTION` is never written, preview
compute hard-defaults to 0, enforcement is off fleet-wide. **Their loop is free because it is
funded** — Bessemer-led pre-seed, Guillermo Rauch angel.

We cannot win a price fight against $0, and should never try. What we can say is what their own code
comment concedes — that real rates await a *"go/no-go"* — and what the graveyard demonstrates: the
subsidised price is the runway, and the runway ends. Octomind's customers found out what happens
next.

Our COGS shape is the durable one: the customer's CI, the customer's model key, a zero-dependency
runner, replays that cost nothing. **We are the only vendor in this research whose marginal cost per
run is approximately zero.** That is not a marketing line, it is why we can still be here in 2028.

---

## 6. The verdict

**One sentence, as asked:**

> On the axes this buyer actually churns over — flake honesty, verdict discipline, evidence, setup
> friction, and a meter that doesn't punish testing more — we are already better than Autonoma
> today; but "better than Autonoma" is not the bar that decides whether this lives, because eight
> companies in this exact category died or gave up while being perfectly good software, and the two
> things that separate us from them are unbuilt: **proof it works on a suite big enough to matter,
> and one team that depends on it.**

### The shortest path to a durable yes, ranked

1. **Run a 50-test suite and publish the wall-clock.** (An afternoon.) Every economic claim we make
   is per-test; every buyer's reality is per-suite. This either validates the story or finds the
   hole. Nothing else on this list matters until this number exists.
2. **Close the false-green hole with the deterministic viewport guard.** (Days.) Our proof is page
   *text*; a CSS catastrophe with intact DOM text replays green. `layout.mjs` now exists and is
   report-only — the remaining step is the blank-render / error-overlay probe. One false green on a
   visibly broken page converts a champion into a detractor faster than any missing feature.
3. **Ship the authenticated-flow story.** (Days.) Storage-state reuse plus a documented pattern.
   Most tests worth writing are behind a login; today we have no answer on the page.
4. **Get one team using it on a real repo, and instrument whether the suite ran twice.** Not a
   metric, a survival condition — see §2.3. This is the only item on the list that is not
   engineering, and it is the one Octomind lost on.
5. **Rewrite the replay claim to the precise version** (§3, row 4) everywhere it appears — site,
   README, llms.txt. The loose version is refutable by a 14.6k-star repo; the precise one isn't.

### What to stop doing

- Stop scoring ourselves against Autonoma. They are one entrant in a category with a graveyard, and
  their pricing is a subsidy, not a business.
- Stop quoting test counts as evidence of quality. Quote mutation-verified guards.
- Stop treating "no account, own key, npx" as a differentiator. Shortest gave that away free and it
  still went dormant.
