# THE JOB — what the QA function actually does all week, and what the market pays to have it done

Written 2026-08-30. Every external fact below was fetched or queried today unless a different
date is given inline. Every claim carries a label:

| label | means |
|---|---|
| **MEASURED** | I read it myself — the page, the API response, the file in our tree. Quoted or reproducible. |
| **CLAIMED** | a vendor or author asserts it about themselves; nobody independent checked it |
| **REPORTED** | a third party asserts it about someone else (a competitor's blog, an aggregator, a review site) |
| **INFERRED** | my arithmetic or my judgement, built on the labelled facts above it |

**The frame this document answers**, from the founder: *"we have to be amazing, like how Claude
Design replaced designers — we need to replace someone and actually drive crazy value."*

That is not grandiosity, and the market says so in dollars. Read the price column before the
argument:

| what you can buy | price | who does the work |
|---|---|---|
| A QA engineer, US, median | **$101,281/yr** base; senior **$136,443/yr**; levels.fyi total comp **$140,345** | a person you hire |
| QA Wolf, Coverage as a Service | **custom, "by number of tests under management"**; REPORTED at **$40–70/test/mo**, median ACV **~$90K** | their engineers + their agents |
| Bug0 | **$2,500/mo flat**, up to 500 user flows | "a forward-deployed engineer plus Bug0's expert AI agents" |
| mabl | **no number anywhere on the pricing page** | you, plus their auto-healing |
| Testim / Rainforest / Reflect / Meticulous | **no number anywhere** | varies |
| QA Wolf, Platform (self-serve) | **1¢/AI credit + 15¢/runner minute** | you |
| **us** | **$19** | an agent |

(Salaries: Glassdoor + levels.fyi, REPORTED via search result text, seen 2026-08-30. QA Wolf and
Bug0 and mabl: MEASURED on their own pricing pages today — sources at the end. The $40–70/test and
$90K ACV figures are REPORTED by competitors and aggregators; QA Wolf publishes nothing to confirm
or deny.)

**The gap between $2,500 and $19 is not a discount. It is the difference between selling a TOOL and
absorbing a FUNCTION.** A tool competes on a feature checklist and gets swapped when a better
checklist appears. A function that has been absorbed does not get swapped, because nobody on the
team wants the job back.

So this document does not ask "what feature is missing". It asks: **what does the person actually
do all week, which parts of that have we already taken, and which of the leftovers — absorbed —
would make somebody say "I cannot go back".**

---

## §0 — GROUND TRUTH: what our runner already takes off the plate

Verified against `/Users/arjun/smolanalytics/cli` today, package version `0.16.1`. This is the
honest starting line; every "what's left over" below is measured from it, not from a wish.

| what it takes off a person | where it lives | MEASURED |
|---|---|---|
| Writing the test | the sentence is the test; no selectors, no page objects | `README.md`, `lib/test.mjs` |
| Deciding what to write in the first place | `suggest --url` walks the running app and writes `tests/*.md`; a proposal whose quoted evidence appears on no visited page is **dropped out loud** rather than shipped | `lib/suggest.mjs` (691 lines) |
| Keeping the test working when the UI changes | there is no selector to update; a recording that stops fitting is `stale`, and the agent re-derives it | `lib/test.mjs`, status contract in `lib/suite.mjs` |
| Paying for judgement twice | a pass is recorded and replays **with no model call** | recordings under `.smolanalytics/recordings` |
| Running the suite | parallel workers off one browser (**39.2s serial → 4.9s at 8 workers**, the file's own measured table), chromium/firefox/webkit, `--since <ref>` diff-aware selection that runs everything and says why whenever it is unsure | `lib/pool.mjs` (405), `lib/engines.mjs` (172), `lib/select.mjs` (405) |
| Confusing "our runner broke" with "your app broke" | five statuses that are never blurred: passed / failed / **stale** / **errored** / **flaky** | `lib/suite.mjs` header |
| Believing a green that is a blank page | render guard turns a would-be PASS over a blank/unstyled/crashed page into `failed`; never softens a `failed` | `lib/render.mjs` (685) |
| Reading a red PR | one comment, edited in place on every push | `lib/suite.mjs` |
| Guessing which change broke it | suspected-file blame — **no suspicion without named evidence, and zero matches says nothing at all** | `lib/suspect.mjs` (452) |
| Building the world the test needs | `--seed` POSTs the run identity to your endpoint; flat JSON becomes `{{placeholders}}`; a failed seed is `errored`, never `failed` | `lib/seed.mjs` (366) |
| Cleaning up after the test | `--teardown` fires after every run including failures | `lib/test.mjs` |
| Logging in | `--login "<sentence>"`, one login per suite, session reused, credentials never on disk | `lib/auth.mjs` (549) |
| Not being surprised by the bill | tokens from the API's own `usage` block, never estimated; `--max-calls` exits 2, never a verdict | `lib/cost.mjs` (145) |
| Showing somebody the result | `--share` — a public run page, no account, three masking passes | `lib/share.mjs` (937) |
| The tracking half | `audit` names untracked user actions from the repo; `plan check` fails CI when an expected event stops firing | `lib/audit.mjs` (335), `lib/plan.mjs` |

**What that adds up to, stated without flattery: a very good RUNNER.** Everything in that table is
labour — typing, waiting, re-typing, cleaning up. We have automated the labour half of the job
about as far as it goes.

**The job is not only labour.** The rest of this document is the other half.

---

## §1 — METHOD

**What I read, in this order:**

1. Our own code, first, so that no "gap" below is a gap we already closed (§0).
2. The existing research in this folder — `GRAVEYARD_AND_BUYER.md` (the HN objection corpus and the
   buyer profile), `HOW_THEY_SELL.md` (the price table), `FIELD_AGENT_FIRST.md`. **I did not redo
   any of it.** Where I quote from it, I say so and I re-verified the load-bearing numbers myself.
3. Vendor pages describing what they take off your plate, in their own words: qawolf.com and
   qawolf.com/pricing, bug0.com/pricing, mabl.com/pricing, jam.dev/pricing, marker.io/pricing.
4. Job descriptions — for the *tasks*, not the buzzwords.
5. The HN corpus via the Algolia items API, which returns full comment trees. This is the only
   large-scale primary source of engineers describing this work in their own words that is
   machine-readable without an account.

**What refused to be fetched, and what I did instead:**

- **Reddit** returned non-JSON to both `www.reddit.com/search.json` and `old.reddit.com` with a
  browser UA (MEASURED — `json.decoder.JSONDecodeError` on both). The brief asks for Reddit
  threads; I could not get them, and I am not going to launder a search-engine summary of a Reddit
  thread into a quotation. HN carries the same population and is fully accessible, so the
  engineer-voice evidence below is HN-only. **This is a real gap in this document.**
- **Ashby and Greenhouse job pages** render client-side; `WebFetch` returned an empty shell
  (MEASURED). `boards-api.greenhouse.io` returned nulls for the two postings I tried — the
  postings had been taken down. The one live posting I got verbatim was via BuiltIn (JetBrains),
  and it is a 2,209-person company, not our buyer. So the job-description evidence below leans on
  **what the tasks are**, which is stable across postings, and not on any one company's wording.
- **The Google flaky-test post** would not summarise correctly through `WebFetch` — three attempts
  returned "no specific numbers are provided" (MEASURED). I pulled the raw HTML and grepped it, and
  the numbers are all there, verbatim, below. **A summariser saying a source is empty is not
  evidence the source is empty.**

**Who the buyer is** (carried forward from `GRAVEYARD_AND_BUYER.md` §4, re-stated because every
"who does this" cell depends on it): a solo builder or a 5–50 person startup shipping a web app.
Engineers. **There is no QA person.** Per jam.dev's own guide to hiring the first one: *"At every
company, but especially at startups, quality is a full team effort. It takes everyone on the team
to be dogfooding the product, giving feedback and reporting bugs… This is in addition to engineers
writing tests, and testing their own PRs"* (MEASURED, jam.dev/blog/how-to-hire-your-first-qa-person-at-your-startup,
seen 2026-08-30). The first QA hire arrives *by reaction, after an incident*, not on a schedule.

So in every task row below, the honest answer to "who does this" is one of exactly three things:
**the person who last touched the code**, **the founder the night before a release**, or
**nobody, and they find out from a customer.**

---

## §2 — THE NUMBERS THAT SHAPE THE WHOLE INVENTORY

Four measured facts do more work than anything else in this document. They are the reason the
ranking in §4 comes out the way it does.

**1. Most red is not a bug.** Google, on their entire corpus (MEASURED — raw HTML of
testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html, grepped today because the
summariser insisted the numbers were absent):

> *"across our entire corpus of tests, we see a continual rate of about 1.5% of all test runs
> reporting a 'flaky' result."*
>
> *"Almost 16% of our tests have some level of flakiness associated with them! This is a staggering
> number; it means that more than 1 in 7 of the tests written by our world-class engineers
> occasionally fail in a way not caused by changes to the code or tests."*
>
> *"What we find in practice is that about 84% of the transitions we observe from pass to fail
> involve a flaky test! This causes extra repetitive work to determine whether a new failure is a
> flaky result or a legitimate failure."*
>
> *"It is quite common to ignore legitimate failures in flaky tests due to the high number of
> false-positives."*
>
> *"If 1.5% of test results are flaky, 15 tests will likely fail, requiring expensive investigation
> by a build cop or developer."*

Google has a **build cop** — a full-time rotating human whose job is this. Our buyer does not have
one, and has the same 84%.

**2. The hardest question in the job has been named in public, by a practitioner, unsolved.** From
"Ask HN: How do you manage flaky E2E tests at scale?" (MEASURED, HN item 46967724, posted
2026-02-10, full comment tree pulled today):

> *"The hardest part is distinguishing 'the test is flaky' from 'the product has a race condition'
> … same symptom, totally different fix."* — alexandriaeden

And from the same thread, the two things that actually happen instead:

> *"I error on the side of just outright killing the tests. Unless it's an absolutely crucial
> business case, I'd rather have no test than a test that slowly degrades trust."* — alexgandy
>
> *"If they're flaky in testing, there's probably something flaky in real world use. If they're
> flaky because of something specific to the test environment then they're not testing what you
> think they're testing, so fix them or get rid of them."* — apothegm

The original poster's own framing of the end state is the churn mechanism in one line:
*"just slowly losing trust in CI signal."*

**3. Maintenance is where the money goes.** PractiTest's 2026 State of Testing report (MEASURED as
displayed on practitest.com/state-of-testing today; **the respondent count is not stated on the
page**, which is a real weakness of this source): **69.6%** use AI for test creation, **59.6%** for
script maintenance, but only **19.9%** for risk identification. Their own name for it is the
*"'Faster Horse' Phenomenon"* — teams building larger test factories rather than better judgement.
And **56% of teams are measured on Test Coverage** rather than on business outcomes.

Third-party figures putting maintenance at 40–60% of QA hours circulate widely (REPORTED, and
traceable only to vendor content marketing — I could not find a primary survey behind the specific
"50%" number, so **do not quote it**). The *direction* is corroborated by the PractiTest split
above and by every job description: maintenance is the largest recurring line.

**4. Nobody at our buyer's size is doing this well, including the people who say testing is
solved.** From the CamelQA Launch HN thread (MEASURED, item 39769412, 2024-03-20):

> *"Over nearly 10 years in startups (big and small), I've been consistently surprised by how much
> I hear that 'testing has been solved', yet I see very little automation in place and PMs/QAs/devs
> and sometimes CEOs and VPs doing lots of manual QA… More than once I worked for a company that
> was against having a manual QA team, out of principle… but ended up hiring external consultants
> to handle QA after a big quality incident."* — batikha

And the objection that most precisely describes our buyer:

> *"'debugging failed tests' is a mature problem that assumes you have working tests and people to
> write them. Most companies don't have the resources to dedicate full engineer time to QA, and if
> they do nobody maintains the test."* — tomatohs

---

## §3 — THE TASK INVENTORY

Fourteen recurring tasks. Eleven were named in the brief; three more (T12–T14) surfaced from the
sources and are marked as additions rather than smuggled in.

**How to read the frequency column.** Frequency is stated for the buyer we actually have — a 5–50
person team shipping a web app several times a week — and is **INFERRED** from release cadence plus
the task's own trigger, unless a measured figure is given. It is a rate, not a guess at effort.

**How to read the "vendor price" column.** This is what somebody currently charges to make this
specific task stop being yours. Where a vendor bundles many tasks into one price, the price appears
in several rows; that is the point — bundling is how a function gets absorbed.

---

### T1 — Deciding what is worth testing at all

| | |
|---|---|
| **Trigger** | a new feature ships; or the very first day, staring at an empty `tests/` folder |
| **Frequency** | per feature — call it **weekly** at our buyer (INFERRED from ship cadence) |
| **Who does it** | the engineer who built the feature, deciding about their own work, ten minutes before merge — or nobody |
| **Pain** | **4/5.** Low frequency of *doing*, high consequence of *not* doing. This is the task whose omission is invisible until an incident. |
| **Vendor price** | QA Wolf sells it as *"Personalized test strategy"* and *"Guaranteed test coverage"* inside Coverage as a Service (custom price, REPORTED ~$90K ACV). Bug0 folds it into $2,500/mo: *"A forward-deployed engineer plus Bug0's expert AI agents"* plan the tests. **Nobody sells it alone.** |
| **Us today** | `suggest --url` — MEASURED, `lib/suggest.mjs`. Solves the **cold start** version, and solves it with the right guard: a proposal whose quoted evidence appears on none of the visited pages is dropped, out loud, because *"One such file is worse than an empty folder"* (the file's own comment). |
| **What is left over** | **The recurring version.** `suggest` answers "what should this app have tests for" once. Nobody answers "this pull request added a feature — does it deserve a test, and which one" every week. That is a different question with a different input (a diff, not a crawl). |

The market's own evidence that this is unsolved: PractiTest's 2026 numbers — **69.6%** of teams
point AI at *creation*, **19.9%** at *risk identification* (MEASURED). The industry has automated
answering the question and not asking it.

---

### T2 — Writing the test

| | |
|---|---|
| **Trigger** | T1 said yes |
| **Frequency** | weekly, in bursts |
| **Who does it** | the engineer, in Playwright, on a Friday, badly — or it does not happen |
| **Pain** | **3/5** — genuinely tedious, but this is the part the whole category already attacked |
| **Vendor price** | QA Wolf: *"The Automation AI writes production-grade code for complex web, iOS, and Android test cases"* + *"Guaranteed creation of E2E tests for any workflow"* (MEASURED, their homepage/pricing). Self-serve at **1¢/AI credit + 15¢/runner minute**. mabl: *"Build, run, and maintain tests autonomously"* (MEASURED, no price shown). |
| **Us today** | **The test is a sentence.** There is no code to write. MEASURED. |
| **What is left over** | Very little, and that is the problem: **this is now table stakes.** Every agentic vendor in `FIELD_AGENT_FIRST.md` claims it. Winning here wins nothing. |

---

### T3 — Keeping the test working as the app changes

| | |
|---|---|
| **Trigger** | any UI change — a renamed button, a moved control, a copy edit |
| **Frequency** | **continuous.** The single largest recurring line in the job. |
| **Who does it** | at our buyer: **nobody.** The suite rots, goes red for reasons nobody trusts, and gets muted. This is the mechanism `GRAVEYARD_AND_BUYER.md` §5 identifies as the actual cause of churn. |
| **Pain** | **5/5** |
| **Vendor price** | QA Wolf CaaS: *"Unlimited maintenance"* and *"24-hour investigation and maintenance"* (MEASURED). Bug0: *"Self-healing AI + engineer oversight"* inside $2,500/mo (MEASURED). mabl: *"auto-healing and agentic recovery"* (MEASURED). **This is the line item the whole managed-QA industry is priced on.** |
| **Us today** | **Structurally removed, not patched.** There is no selector to update because there is no selector. A recording that stops fitting is `stale` — never red, never worded as a failure — and the agent re-derives it. MEASURED, `lib/suite.mjs` + `lib/test.mjs`. |
| **What is left over** | Honestly: not much, *for our architecture*. The leftover is a **trust** problem, not a work problem — the buyer has been burned by self-healing before, and "self-healing" is exactly the phrase that names the failure mode they fear (see the adversarial note below). |

**The adversarial note this row needs.** HN, on our own category (MEASURED, item 46967724): a
practitioner describes self-healing with *"a confidence gate to avoid silent false passes"* — the
gate exists because unguarded healing **turns a real break into a green tick**. Our render guard
(`lib/render.mjs`) is one half of that gate. `stale`-not-`pass` is the other. That pairing is the
defensible version of this row and should be sold as *"we do not heal a test into a lie"*, never as
"self-healing".

---

### T4 — Running it

| | |
|---|---|
| **Trigger** | every push, every PR, every release |
| **Frequency** | **many times daily** |
| **Who does it** | CI, once someone spent an afternoon wiring it |
| **Pain** | **2/5** to keep running; **4/5** the one afternoon it is set up, and that afternoon is where tools lose the buyer |
| **Vendor price** | QA Wolf: **15¢ per runner minute** plus *"unlimited parallel runs"* (MEASURED — and see `HOW_THEY_SELL.md` for why those two statements are in tension: an INFERRED 300-test suite at 30s each is 150 runner-minutes = **$22.50 a run**, ~$6,750/mo at ten runs a day). Bug0 bundles it: *"Test runs, AI credits, infra, hours"* never charged separately (MEASURED). |
| **Us today** | Runs on **their** Actions runner against **their** preview URL, commenting with the `GITHUB_TOKEN` Actions already hands every job. No GitHub App, nothing written to the repo. Parallel (39.2s → 4.9s at 8 workers, MEASURED in `lib/pool.mjs`), three engines, `--since`. |
| **What is left over** | Nothing structural. This is commodity, and our only edge is that the meter does not punish the careful. |

---

### T5 — Triaging a red build

| | |
|---|---|
| **Trigger** | the suite is red. It is 6pm. |
| **Frequency** | **several times a week** at any team with a real suite |
| **Who does it** | whoever notices — usually the person who pushed last, whether or not they caused it |
| **Pain** | **5/5.** This is the task that produces the phrase *"slowly losing trust in CI signal"* (MEASURED, HN 46967724). |
| **Vendor price** | QA Wolf CaaS: *"Investigate every failure"*, *"24-hour investigation"*, video playbacks, and **human-verified bug reports with video, Playwright traces, logs** (MEASURED). Bug0: *"Human-reviewed. Every failure"* (MEASURED). **Both companies put a human on this task and charge for the human.** That is the loudest signal in this entire document about which task is unabsorbed. |
| **Us today** | Real, and partial: evidence at failure (screenshot + page text), the failing step named, and `lib/suspect.mjs` naming the changed files with the string that connects them — *"src/Checkout.tsx — this PR removed the string 'Proceed to checkout' this test clicks"*. And the discipline that makes it trustworthy: **no suspicion without named evidence; zero matches says nothing at all.** |
| **What is left over** | **The triage itself, as a sequence.** Twelve tests are red. A person still has to work out that eleven of them are the same cause, which one to open first, and whether the cause is the app, the test, the environment, the test data, or a third party that was down. We hand over twelve well-documented failures. QA Wolf hands over *one human-verified bug report*. **That difference is the product gap, and it is not a feature — it is a job.** |

---

### T6 — Deciding whether a failure is real

| | |
|---|---|
| **Trigger** | one red test |
| **Frequency** | **every red test — so, several times a week, and 84% of the time the answer is "no"** |
| **Who does it** | the engineer, by re-running it and seeing if it goes green. Which is not an answer; it is a coin flip they have agreed to accept. |
| **Pain** | **5/5**, and uniquely corrosive: this is the task whose repeated failure converts a testing tool into noise, and noise into a cancelled subscription |
| **Vendor price** | This is the **only** task in the inventory that vendors sell as a *guarantee* rather than a service. QA Wolf: **"Guaranteed zero flakes"** (MEASURED, their pricing page). Their best social proof is a customer sentence about exactly this: *"The tests run in 11 minutes. There's about 300 and we rarely get a false negative."* Bug0 puts a human on it: *"Human-reviewed. Every failure"*. |
| **Us today** | The best structural answer in the field at our price, and I will state it precisely because it matters: `--retries` re-runs a failing test **from a clean page**, and a pass-on-retry is **`flaky`, never `passed`** — *"silently swallowing a retry is how an intermittent bug hides for months"* (`lib/suite.mjs`, MEASURED). `flaky` exits 0 and says so out loud. `errored` is never confused with `failed`. |
| **What is left over** | **The verdict inside `flaky`.** We correctly refuse to call it a pass. We do not say *why*. And the practitioner quote at §2.2 names that leftover exactly: *"The hardest part is distinguishing 'the test is flaky' from 'the product has a race condition'… same symptom, totally different fix."* Those are opposite actions — delete the test, or page someone — and today the human picks. **We have all the material to decide it and we do not decide it:** we have both runs' recordings, both pages' text, both timings, and the diff. |

---

### T7 — Filing the bug with enough detail that somebody can act on it

| | |
|---|---|
| **Trigger** | T6 said "real" |
| **Frequency** | several times a week |
| **Who does it** | the person who found it, in Linear, in three lines, at 6:40pm, without the console log |
| **Pain** | **3/5** — the pain is not filing it, it is the round trip when the repro is not in the ticket |
| **Vendor price** | **This task has a clean market price, because it is sold alone.** Jam: **$0 free / $14 per creator/mo** for console + network logs, device metadata, user actions, instant replay (MEASURED). Marker.io: **$39/mo (3 users) / $149/mo (15 users)** for screenshot + annotations + environment + console + network (MEASURED). Inside managed QA it is a headline: Bug0 files bugs *"with video and reproduction steps"*; QA Wolf's reports carry *video, Playwright traces, logs* (MEASURED). |
| **Us today** | We produce the raw material — evidence directory, page text, screenshot, the failing step, suspect files — and one PR comment. |
| **What is left over** | **The artefact itself.** We never create an issue in their tracker, never deduplicate against last week's identical failure, and never close one when it goes green. But note the price column: **$14/seat/month.** This task is cheap because it is well served. Absorbing it is worth doing and is not worth *leading* with. |

---

### T8 — Deciding what to re-test after a fix

| | |
|---|---|
| **Trigger** | a fix lands for a bug that a test found |
| **Frequency** | **several times a week** |
| **Who does it** | the fixer, by re-running the one test they were looking at — and not the neighbours |
| **Pain** | **3/5** each time; **5/5** the once a quarter it is wrong and the fix broke something adjacent |
| **Vendor price** | Sold only inside enterprise CI. CloudBees **Smart Tests** (the acquired Launchable) is *"AI-driven test intelligence for CI/CD"*, GA 2026-04-02, claiming *up to 80% faster test execution* — **no self-serve price** (REPORTED via CloudBees newsroom text; I could not obtain a price). Managed vendors absorb it silently as part of the retainer. **There is no product our buyer can buy for this.** |
| **Us today** | `--since <ref>` intersects each recording's observed controls, paths and proof text with `git diff`, and is deliberately biased toward running too much: no recording → run it; unreadable → run it; no git, no merge base, empty diff, internal throw → run everything **and say why**. MEASURED, `lib/select.mjs`. |
| **What is left over** | **"What the diff touched" is not the same question as "did this fix actually fix it".** The second question needs the *failure* as the unit — this test failed on this cause, a fix claims to address it, re-run exactly that plus its blast radius, and then say the failure is **closed**. We have the failure, the recording, the suspect files and the diff, and we currently do not connect them across two runs. |

---

### T9 — Pruning tests that no longer earn their run time

| | |
|---|---|
| **Trigger** | the suite is slow, or a test has been quarantined for two months |
| **Frequency** | **quarterly, at best.** Usually: never, then all at once, in anger. |
| **Who does it** | nobody, until someone deletes 200 tests in an afternoon and everyone feels relief |
| **Pain** | **3/5** as a task; the *consequence* of never doing it is 5/5, because an unpruned suite is how a suite stops being believed |
| **Vendor price** | **Zero. Nobody sells this, and the reason is structural: the price of every leading vendor goes UP with the number of tests you keep.** QA Wolf CaaS is priced *"by number of tests under management"* (MEASURED) — REPORTED at $40–70/test/mo. Reflect meters credits per test (web=1, mobile=5). mabl meters credits per run. **A vendor paid per test under management will never, ever tell you to delete a test.** |
| **Us today** | Nothing. And note: **our meter has no such conflict** — nothing in our pricing gets better when a customer keeps a dead test. |
| **What is left over** | The whole task. Evidence that engineers want it and reach for the crudest possible version: *"I error on the side of just outright killing the tests… I'd rather have no test than a test that slowly degrades trust"* (MEASURED, HN 46967724); and the deletion-as-relief post already in `GRAVEYARD_AND_BUYER.md` — *"We deleted 247 E2E tests and CI got 62% faster… developers started trusting CI again"* (CLAIMED numbers, 2026-01). |

---

### T10 — Noticing that coverage has drifted from what the product now does

| | |
|---|---|
| **Trigger** | three months of shipping |
| **Frequency** | should be **monthly**; actually **never** |
| **Who does it** | nobody. There is no moment in anybody's week when this question is asked. |
| **Pain** | **4/5**, entirely deferred — you feel it once, in an incident, in the feature nobody tested because it did not exist when the suite was written |
| **Vendor price** | QA Wolf: *"Coverage quality reporting"* and *"100% of teams achieve 80%+ automated test coverage in weeks"* (MEASURED, CaaS only — the managed tier, so effectively REPORTED ~$90K ACV). Nothing self-serve. Note the shape of the industry's proxy: PractiTest, **56% of teams are measured on Test Coverage** (MEASURED) — a number that says nothing about whether the suite matches the product. |
| **Us today** | `suggest` could answer it but is a one-shot command nobody re-runs. The *tracking* half of our product already does the analogous job on the analytics side — `audit` reads the repo and names user actions with no tracking near them (`lib/audit.mjs`). **The pattern exists in our codebase; it has not been pointed at the test suite.** |
| **What is left over** | The recurring diff: *what this app can now do* ∖ *what this suite checks*. We hold both sides — a crawl of the live app, and a set of recordings that state exactly which controls, paths and proof strings each test touches. |

---

### T11 — Reporting "are we safe to ship"

| | |
|---|---|
| **Trigger** | every release |
| **Frequency** | **several times a week** at our buyer |
| **Who does it** | the founder or the eng lead, by looking at a green tick and deciding how brave they feel |
| **Pain** | **5/5.** This is the only task on the list that a *non-engineer* also feels, and the only one anybody loses sleep over. |
| **Vendor price** | Bug0 lists **"Release gating"** in the $2,500/mo base (MEASURED). QA Wolf sells the pre-condition — *"releases get stuck in QA"* is the fear in their own subhead, and the customer sentence they lead with is *"three releases in about three weeks"* (MEASURED). Job postings put it in the title: a Senior QA Engineer at Zip *"owns the quality gate for every release cycle"* (REPORTED via search-result text — the Ashby page itself would not render for me). |
| **Us today** | Per-PR verdicts, one edited comment, five statuses. **We answer "did these tests pass".** |
| **What is left over** | **The question is not "did the tests pass". It is "should I ship".** Those differ by exactly the thing our whole product is built on — honesty about what was *not* checked. A green run today says nothing about the four flows with no test, the two `stale` recordings the agent has not revisited, the test skipped by `--since`, or the feature that shipped last Tuesday and was never covered. We have every one of those facts in hand and we never compose them into one sentence. |

---

### T12 — Keeping test data, accounts and environments alive *(addition)*

| | |
|---|---|
| **Trigger** | a test needs a logged-in user with three past orders; the staging password rotated; the seeded account got cleaned up |
| **Frequency** | **weekly**, and always at the worst moment |
| **Who does it** | the engineer, by hand, then again next month |
| **Pain** | **4/5** — and it is the category's most-cited killer. From `GRAVEYARD_AND_BUYER.md` §3.4, verbatim from a founder in the field: state changes between runs are *"one of our biggest challenges"*; the auth/MFA/OTP question recurs in **every** launch thread. |
| **Vendor price** | Bundled and invisible: Bug0's *"Test runs, AI credits, infra, hours"* never charged separately (MEASURED); QA Wolf CaaS absorbs it in the retainer. Nobody prices it alone because alone it is unsellable. |
| **Us today** | Genuinely strong: `--seed` (their endpoint, their app, flat JSON → placeholders), `--teardown` (fires even on failure, because *"the failed run is the likeliest to have left half an account behind"*), `--login` with mid-suite session repair and a failed login classed `errored` not `failed`, synthetic identities that all start with `smoltest` so one `LIKE` finds every row any run ever made, and `example.com` by default so no test signup lands in a real inbox. MEASURED. |
| **What is left over** | Little, and this row is here mainly to be **defended in a bake-off**, since it is where competitors' demos die. |

---

### T13 — Exploring a brand-new feature nobody has an expectation for yet *(addition)*

| | |
|---|---|
| **Trigger** | a feature ships that has never existed before |
| **Frequency** | weekly |
| **Who does it** | the person who built it, clicking their own work, seeing what they meant to see |
| **Pain** | **4/5** |
| **Vendor price** | Humans, explicitly: Bug0's *"forward-deployed engineer"*; QA Wolf embedding *"full-time QA engineers with your team"* (MEASURED). |
| **Us today** | Nothing directly. `suggest` proposes tests for what exists; it does not go looking for what is wrong. |
| **What is left over** | The whole task — and this is the one row where **I do not think we should try to win.** An agent exploring without a stated expectation produces either nothing or noise, and noise here is the fastest route to being muted (`GRAVEYARD_AND_BUYER.md` §5). Named so that it is a deliberate refusal, not an oversight. |

---

### T14 — Being the one who says the same thing twice a week to non-engineers *(addition)*

| | |
|---|---|
| **Trigger** | standup; the release channel; the founder asking "are we good?" |
| **Frequency** | **daily** |
| **Who does it** | whoever is holding the release |
| **Pain** | **2/5** individually, but it is the visible surface of the whole function — it is *how the team knows the job is being done* |
| **Vendor price** | Bug0: *"Weekly digests plus real-time dashboard access"* and a *"dedicated Slack support channel"* in the $2,500/mo (MEASURED). QA Wolf: *"Coverage quality reporting"* (MEASURED). |
| **Us today** | `--share` produces a public run page with no account, and the OG card carries the verdict and the sentence (MEASURED, `lib/share.mjs` + the cloud `/s/[id]` route). `desk` prints the same composition in the terminal. |
| **What is left over** | The recurring, unprompted version. Everything we emit is pull, on demand. Nothing we build says a thing on a Monday morning without being asked. |

---

## §4 — THE RANKING: (pain × frequency) / how well any tool handles it today

**The rubric, stated so the numbers can be argued with.**

- **P — pain, 1–5.** How much it hurts each time it happens, at a 5–50 person team with no QA hire.
- **F — frequency, 1–5.** 5 = daily or more · 4 = several times a week · 3 = weekly · 2 = monthly ·
  1 = quarterly or never. INFERRED from the trigger plus a several-releases-a-week cadence.
- **H — handled, 1–5.** How well **the best tool our buyer can actually obtain today** does this —
  including us, including free Playwright, including the coding agent they already pay for.
  5 = solved, 1 = nobody does it.
- **Score = (P × F) / H.** High score = a task that hurts often and that nothing absorbs.

| # | task | P | F | H | **score** | who charges to take it away |
|---|---|---|---|---|---|---|
| **T6** | **Deciding whether a failure is real** | 5 | 4 | 2 | **10.0** | QA Wolf: *"Guaranteed zero flakes"*. Bug0: *"Human-reviewed. Every failure"* |
| **T11** | **Reporting "are we safe to ship"** | 5 | 4 | 2 | **10.0** | Bug0 *"Release gating"*, $2,500/mo |
| **T5** | **Triaging a red build** | 5 | 4 | 2.5 | **8.0** | QA Wolf *"24-hour investigation"*, human-verified reports |
| T13 | Exploring a brand-new feature | 4 | 3 | 1.5 | **8.0** | humans only — *"forward-deployed engineer"* |
| T3 | Keeping tests working as the app changes | 5 | 5 | 4 | **6.25** | *the line item the whole managed-QA industry is priced on* |
| T1 | Deciding what is worth testing at all | 4 | 3 | 2 | **6.0** | QA Wolf *"Personalized test strategy"* (CaaS only) |
| T10 | Noticing coverage has drifted | 4 | 2 | 1.5 | **5.3** | QA Wolf *"Coverage quality reporting"* (CaaS only) |
| T8 | Deciding what to re-test after a fix | 3 | 4 | 2.5 | **4.8** | CloudBees Smart Tests — enterprise, no self-serve price |
| T14 | Saying it again on Monday to non-engineers | 2 | 5 | 3 | **3.3** | Bug0 weekly digests + Slack channel |
| T9 | Pruning tests that no longer earn their run | 3 | 1 | 1 | **3.0** | **nobody — and the incumbents are paid not to** |
| T12 | Keeping test data / accounts / envs alive | 4 | 3 | 4 | **3.0** | bundled and invisible everywhere |
| T7 | Filing the bug with enough detail | 3 | 4 | 4.5 | **2.7** | Jam $14/creator/mo · Marker.io $39–149/mo |
| T4 | Running it | 2 | 5 | 5 | **2.0** | QA Wolf 15¢/runner-minute |
| T2 | Writing the test | 3 | 3 | 5 | **1.8** | everybody, loudly, as their headline |

### What the ranking says, in one paragraph

**The three highest-scoring tasks we are willing to take — T6, T11, T5 — are the same task at three
zoom levels.** *Is this failure real* is the question about one test. *What broke* is the question
about one build. *Are we safe to ship* is the question about one release. All three are **judgement
about a result**, and all three are what the human still does after every tool in this field has
finished running.

And look at the bottom of the table. **T2 and T4 — writing the test and running it — score 1.8 and
2.0.** Those are the two things our whole category advertises. They are finished work. Every
vendor's homepage is a headline about the two least valuable rows in this document.

### The corroboration I did not have to argue for

The two companies that charge the most in this field have already told us which task is unabsorbed,
by putting a **human** on it and billing for the human:

- QA Wolf, on the managed tier: *"Investigate every failure"*, *"24-hour investigation and
  maintenance"*, **human-verified** bug reports (MEASURED).
- Bug0, in its $2,500 flat fee: *"Human-reviewed. Every failure."* (MEASURED).

Neither of them puts a human on writing tests. Both put a human on **judging results.** That is the
function, and that is what "replacing someone" has to mean here.

---

## §5 — THE THREE LEFTOVERS THAT WOULD PRODUCE "I CANNOT GO BACK"

Ranked by the table above, restated as the thing a person would actually stop doing.

### 1. The flake verdict — say *why* it was not reproducible (T6, score 10.0)

**What the person does today.** A test goes red. They re-run it. It goes green. They shrug and
merge. Google's own number says they were right to shrug 84% of the time — and wrong the other 16%,
which is *"quite common to ignore"* (MEASURED, verbatim, §2.1).

**What we already have and do not use.** When `--retries` produces a `flaky`, we are holding: the
failing run's recording, page text and screenshot; the passing retry's recording and page text; both
timings; and, on a PR, the diff. The difference between *the test is unreliable* and *the product has
a race condition* is very often visible in exactly that material — the same step at a different
latency, a control that existed in one run and not the other, an assertion that passed only after a
slower paint.

**Why it fits us and not them.** This is a verdict, and verdicts are the thing we already refuse to
blur. `flaky` today is an honest refusal to say "pass". A `flaky` that says *"the second run
differed only in that the confirmation text appeared 1.9s later — this looks like your app, not this
test"* is the same honesty carried one step further. It is also the only place in the field where a
model's judgement is worth paying for and cannot be replaced by a retry loop.

**The sentence it produces:** *"It told me the flake was a real race condition, and it was."*

**The way it goes wrong, and the guard.** A wrong diagnosis here is worse than none — it either
sends someone hunting a race condition that does not exist, or tells them to ignore a real one. So
it must inherit the `suspect.mjs` rule without exception: **no diagnosis without named evidence from
both runs, and no confident wording when the two runs are indistinguishable.** "I cannot tell these
two runs apart" is a legitimate and useful output.

### 2. The ship verdict — one honest sentence, including what was NOT checked (T11, score 10.0)

**What the person does today.** They look at a green tick and decide how brave they feel.

**Why nobody has this.** Every vendor answers *"did the tests pass"*. Nobody answers *"should you
ship"*, because answering honestly requires admitting what you did not check — and no vendor whose
pricing depends on looking comprehensive wants to print that list. **Our entire product identity is
built on printing that list.** `--since` already names every test it skipped, *"because a run that
quietly checked twelve of fifty tests and printed '12 passed' is a suite lying about itself"*
(MEASURED, `README.md`). This is that principle applied to the release instead of the run.

**What it is, concretely.** One artefact, per release, that composes facts we already hold: what
passed; what is `flaky` and therefore proves nothing; what recordings are `stale` and have not been
re-derived; what `--since` skipped and why; and — the part that requires T10 — **which parts of the
app have no test at all**. It ends with a verdict a founder can act on, not a percentage.

**The sentence it produces:** *"It is the only thing that tells me what it did not check."*

**Why this one is strategically the most valuable of the three.** It is the only task in the whole
inventory that a **non-engineer** feels. It is what a founder asks on a Friday. Absorbing it makes
the product the answer to a question that is asked out loud in a channel, every week, by the person
who holds the budget.

### 3. The build verdict — twelve red tests become one cause (T5, score 8.0)

**What the person does today.** Opens twelve failures and works out that eleven of them are the
same broken deploy, one is a real bug, and which one to open first.

**What we already have.** Evidence per failure, the failing step, and `suspect.mjs` blame with named
evidence. We hand over twelve well-documented failures. QA Wolf hands over one human-verified report.

**What is left.** Clustering by cause, ordering by consequence, and separating *app* from *test* from
*environment* from *data* from *third party*. Two thirds of that is computable from material we
already hold: failures whose recordings share a path or a control, failures that all follow one
`errored` login, failures whose suspect sets intersect on one file.

**The sentence it produces:** *"It told me all eleven were one thing."*

### The thing all three have in common — and why this is a strategy and not a backlog

Our runner absorbed the **labour**. These three absorb the **judgement**. And they compose into one
chain, which is the actual product thesis:

> **one failure → is it real (T6) → what caused it, with the others (T5) → can we ship (T11)**

That chain is the job. A tool that runs tests is a runner. A thing that walks that chain **is the
QA function**, and it is the thing you cannot go back from — not because it is better, but because
once nobody on the team has answered "is this flake real" for six months, nobody remembers how.

### Two smaller ones that are uncontested and cheap

- **T9, pruning (score 3.0).** Low frequency, so it will never top the table — but **the price
  column is empty and the incumbents are structurally barred from filling it.** QA Wolf's managed
  tier is priced *"by number of tests under management"* (MEASURED). Reflect meters credits per
  test. mabl meters credits per run. **Every one of them earns more when you keep a dead test.** We
  do not. "The only testing product with no reason to keep your dead tests" is true, checkable, and
  unanswerable — and it costs us a `prune` command that reads the recordings we already write.
- **T10, coverage drift (score 5.3).** Uncontested outside a ~$90K managed contract, it is the
  missing input to the ship verdict above, and **we have already built this shape once**: `audit`
  reads a repo and names user actions with no tracking near them (`lib/audit.mjs`). The same shape,
  pointed at a crawl of the live app minus the controls and paths our recordings touch, answers
  "what does this app now do that nothing checks".

---

## §6 — ADVERSARIAL: how this document could be wrong

**1. The 84% is Google's, not our buyer's.** Google runs millions of tests across a monorepo with
build cops and a hermetic build system. A 5–50 person startup runs forty E2E tests against a Vercel
preview. The *mechanism* transfers (retries hide real bugs; noise destroys trust) but the *ratio*
may not. **Do not put "84%" on a marketing page as though it describes our customer.** It is
evidence that the task is hard even at the top of the industry, and nothing more.

**2. "Judgement" is exactly what the 2026 default objection attacks.** `GRAVEYARD_AND_BUYER.md` §3.1
records the objection that now appears in every launch thread: *"what makes this different than just
another feature in Gemini Code Assist or GitHub Copilot?"*, *"I struggle to see how it is different
than spinning 10 Atlas tabs with a 2 sentence prompt"*. A verdict-on-a-failure is *more* exposed to
that objection than a runner is, not less, because a coding agent with the logs can attempt it.
**The defensible part is not the judgement — it is the material the judgement is made from**: two
recordings of the same test at different latencies, a proof string that vanished, a suspect
intersection. We hold that because we ran the thing. A chat window does not.

**3. Frequency numbers are inferred, and F drives the ranking as hard as P does.** If our buyer
ships weekly rather than daily, T14 and T4 collapse and T10 rises. Nothing in the top three moves —
they are all F4 — but the middle of the table is soft, and I would not defend the ordering of ranks
8–14 in a fight.

**4. PractiTest publishes no respondent count on the page I read** (MEASURED absence). It is the
best survey-shaped source I found, and it is still a vendor's marketing report. The 69.6 / 59.6 /
19.9 split is directionally corroborated by the vendor pages (everyone automates creation and
maintenance; nobody automates risk) but it is not a hard number.

**5. No Reddit, and only one live job posting.** Stated in §1 and repeated here because it is the
largest evidence gap in the document. The task list itself is stable across every job description
template and every day-in-the-life writeup I saw, so I am confident in the *inventory*; I am less
confident in the *relative pain weights*, which lean on HN plus judgement.

**6. The strongest counter-argument to the whole document.** Nobody has ever cancelled a testing
tool because it failed to tell them whether a flake was real. They cancel because the tool became
noise (`GRAVEYARD_AND_BUYER.md` §5). A wrong verdict is noise with more confidence attached. **The
same three features that would produce "I cannot go back" would, done badly, produce exactly the
churn mechanism we already documented.** Which means the guards are not polish on this work — they
are the work: no diagnosis without named evidence from both runs, an explicit "I cannot tell",
never a verdict that changes an exit code, and never a confident sentence built on a single
observation.

---

## SOURCES

All fetched or queried **2026-08-30** unless stated. `[F]` = WebFetch, `[C]` = raw `curl` + parse,
`[S]` = search-result text only (weaker — the underlying page was not read).

**Our own code — MEASURED, read directly**
- `/Users/arjun/smolanalytics/cli/package.json` — version `0.16.1`
- `/Users/arjun/smolanalytics/cli/README.md` — the command surface, the status contract, the flag table
- `lib/suggest.mjs` (691), `lib/suspect.mjs` (452), `lib/watch.mjs` (926), `lib/suite.mjs` (1252),
  `lib/audit.mjs` (335), `lib/desk.mjs` (130), `lib/plan.mjs` (138) — header comments read in full
- Feature verification not re-done here; carried from `THE_CHECKLIST.md` §0 and `ALLURE.md` §0,
  both of which verified against this same tree on 2026-08-30

**Vendors — their own pages**
- `https://www.qawolf.com/` [F] — *"Agentic QA that just works"*; *"AI autonomously explores your app
  and documents its workflows"*; *"The Automation AI writes production-grade code…"*; *"Unlimited
  maintenance"*; *"Investigate every failure"*; testimonial *"The tests run in 11 minutes. There's
  about 300 and we rarely get a false negative."*
- `https://www.qawolf.com/pricing` [F] — Platform: **1¢/AI credit, 15¢/runner minute**. Coverage as
  a Service: **custom, by number of tests under management**; *"Guaranteed creation of E2E tests for
  any workflow"*; *"100% of teams achieve 80%+ automated test coverage in weeks"*; *"24-hour
  investigation and maintenance"*; **"Guaranteed zero flakes"**; *"The Automate Anything Guarantee"*
- `https://www.qawolf.com/coverage-as-a-service` [F] — **404** (MEASURED; the CaaS detail lives on
  the pricing page)
- `https://bug0.com/pricing` [F] — **$2,500/mo flat**, up to 500 user flows, pro rata beyond;
  *"A forward-deployed engineer plus Bug0's expert AI agents"*; *"Self-healing AI + engineer
  oversight"*; **"Human-reviewed. Every failure"**; bugs filed *"with video and reproduction steps"*;
  **"Release gating"**; *"Test runs, AI credits, infra, hours"* never charged separately; weekly
  digests + dedicated Slack; month-to-month, 60-day discounted pilot
- `https://www.mabl.com/pricing` [F] — **no tier names, no dollar figures**; *"Build, run, and
  maintain tests autonomously"* with *"auto-healing and agentic recovery"*; credits, *"500 credits
  per month"* starting point; **"Request a Quote"**
- `https://jam.dev/pricing` [F] — Free $0 / **Team $14 per creator/mo** / Enterprise custom;
  captures console & network logs, device metadata, user actions, instant replay
- `https://marker.io/pricing` [F] — **Starter $39/mo (3 users)**, **Team $149/mo (15 users)**,
  Business custom; captures *"screenshots & annotations", "environment details", "console logs",
  "network requests"*
- Testim/Tricentis, Rainforest, Reflect, Meticulous, Autonoma, Momentic — **not re-fetched.** Their
  price rows are carried from `HOW_THEY_SELL.md` PART 3–4 (all MEASURED on their own pricing pages
  2026-08-30): none of them displays a figure.

**Primary evidence about the work itself**
- `https://testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html` [C] — the 1.5% /
  16% / 84% / *"ignore legitimate failures"* / *"expensive investigation by a build cop"* quotes in
  §2.1, extracted from raw HTML after three WebFetch attempts wrongly reported the page had no
  numbers
- HN item **46967724** [C] — *"Ask HN: How do you manage flaky E2E tests at scale?"*, 2026-02-10.
  Full comment tree. Source of *"distinguishing 'the test is flaky' from 'the product has a race
  condition'… same symptom, totally different fix"* [alexandriaeden], *"I'd rather have no test than
  a test that slowly degrades trust"* [alexgandy], *"fix them or get rid of them"* [apothegm], and
  the OP's *"slowly losing trust in CI signal"*
- HN item **39769412** [C] — Launch HN: CamelQA, 2024-03-20. Source of batikha's *"testing has been
  solved"* / consultants-after-an-incident comment and tomatohs's *"Most companies don't have the
  resources to dedicate full engineer time to QA, and if they do nobody maintains the test"*
- HN item **41924787** [C] — Launch HN: GPT Driver, 2024-10-23. Regression-maintenance-as-bottleneck
  quotes; also the entropy objection (*"lead to more flakiness"*, ngokevin)
- HN item **45038465** [C] — *"Ask HN: What's your 2025 quality stack?"* — the question set itself is
  evidence of which tasks people think about (self-healing, triage, "confidence to release")
- `https://jam.dev/blog/how-to-hire-your-first-qa-person-at-your-startup/` [F] — *"quality is a full
  team effort… in addition to engineers writing tests, and testing their own PRs"*; the first hire
  *"systematize testing, and be the point person responsible to not let a single bug out the door"*
- `https://www.practitest.com/state-of-testing/` [F] — 2026 report: AI used for creation **69.6%**,
  script maintenance **59.6%**, risk identification **19.9%**; *"'Faster Horse' Phenomenon"*; **56%**
  measured on test coverage. **No respondent count shown** (MEASURED absence)
- `https://builtin.com/job/qa-engineer-version-control-experience/8006762` [F] — the only live
  posting I read verbatim: *"Perform functional and regression testing…"*, *"Prioritize and highlight
  critical issues, and assist in workload distribution"*, *"Create and maintain the automated
  tests"* (JetBrains, 2,209 employees — **not our buyer**, used only for task vocabulary)
- QA Engineer job-description task vocabulary — Toptal, Upwork, Manatal templates and live Ashby /
  Greenhouse postings (Zip: *"owns the quality gate for every release cycle"*; Asteri AI: *"define
  and execute quality strategies and test plans aligned with the product roadmap and release
  cycles"*; Seesaw; Merge) [S] — **search-result text only; the pages themselves render
  client-side and returned empty shells to WebFetch**
- Salaries [S] — Glassdoor: QA Engineer **$101,281**, Senior QA Engineer **$136,443**; levels.fyi
  QA Software Engineer total comp **$140,345**
- CloudBees **Smart Tests** (acquired Launchable), GA 2026-04-02, *"AI-driven test intelligence for
  CI/CD"*, *up to 80% faster test execution* [S] — REPORTED, **no self-serve price found**
- QA Wolf managed-tier unit economics — **$40–70/test/mo**, median ACV **~$90K** [S] — REPORTED by
  third parties (Vendr, and competitors' comparison posts). QA Wolf publishes nothing to confirm or
  deny. `HOW_THEY_SELL.md` records the narrower **$40–44/test/mo** figure from Autonoma's table.
  **Treat the range as unverified.**

**Prior research reused, not redone**
- `GRAVEYARD_AND_BUYER.md` §3 (HN objection corpus), §4 (the buyer at 5–50), §5 (why products get
  muted) — quoted and attributed inline
- `HOW_THEY_SELL.md` PART 2 (QA Wolf), PART 4 (the incumbents' missing prices)
- `THE_CHECKLIST.md` §0 and `ALLURE.md` §0 (feature verification against this tree, 2026-08-30)

**Attempted and failed** (recorded so nobody repeats it)
- `reddit.com/search.json` and `old.reddit.com/…/search.json` with a browser UA — both returned
  non-JSON (MEASURED)
- `jobs.ashbyhq.com` posting pages and `api.ashbyhq.com/posting-api/job-board/zip` — empty shell /
  empty result (MEASURED)
- `boards-api.greenhouse.io` for the two Greenhouse postings found in search — nulls, postings
  removed (MEASURED)
