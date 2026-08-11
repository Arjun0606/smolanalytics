# The PM loop, automated

**What Autonoma did to QA, we do to product management.**

Autonoma's bravest asset is one sentence: *"Does this replace QA teams? Yes, and that's a good
thing."* Playwright already existed. It did not matter, because Playwright is a **tool for doing
the job** and Autonoma **does the job**.

PostHog and Mixpanel already exist. It does not matter, for the same reason.

**The claim:** you keep the product manager who decides. We delete the four who find out.

---

## 1. What we are actually replacing

A PM's week, honestly decomposed:

| The work | Share of the week | Who does it after us |
|---|---|---|
| Noticing something moved | high | us, continuously |
| Pulling and slicing data until it explains itself | **highest** | us |
| Working out which of six problems is worth doing | medium | us (ranked by money), them (final call) |
| Writing it up so eng can act | medium | us |
| Chasing whether last month's ship worked | high | us |
| Killing what did not work | rarely done at all | us |
| **Deciding what the company should be** | **low** | **them, forever** |

The judgment is roughly a tenth of it. The other nine tenths is *finding out* — and finding out is
a mechanical loop over an event log we already hold.

**Why we can claim this and the incumbents cannot.** To let software do this work, you have to act
on its output without re-deriving it. PostHog and Mixpanel sample and pre-aggregate; their numbers
cannot be recomputed from the rows, so a careful PM re-checks, so the labour never actually goes
away. Every figure we emit recomputes from the raw log, and `/v1/rows` will show its working on
demand. That is not a feature. **It is the precondition for automating the job at all.** You can
only automate work you can prove you did correctly.

## 2. The organs already exist

Verified in the tree on 2026-08-11. This is why this is a quarter, not a company rewrite.

| Capability | Where it lives | State |
|---|---|---|
| What moved, and is it notable | `internal/insight`, `whats_notable` | shipped |
| When it moved | `internal/whatchanged` → `/v1/explain` | shipped |
| Which ship caused it | `deploy_impact`, `internal/fixbrief` | shipped |
| The offending diff | `github-app.getCommitDiff` | shipped |
| Where people fall out | `funnel`, `paths`, `retention`, `lifecycle` | shipped |
| Which segment is worst | `breakdown`, `groups`, `cohorts` | shipped |
| Is anything untracked | `instrumentation_coverage` | shipped |
| Write a PR | `github-app.openFixPr`, `openInstrumentationPr` | shipped |
| Ship to a slice | `internal/flag` rollouts | shipped |
| Prove it worked | sequential inference, SRM, CUPED, guardrails | shipped |
| **Investigate unprompted** | — | **missing** |
| **Rank by money** | — | **missing** |
| **Act on a breach** | — | **missing** |

Three missing rows. That is the build.

## 3. The defect that proves the point

`flag.EvaluateGuardrails` has **zero callers**. `internal/api/experiment_api.go:131` attaches a
`$exception` guardrail to every experiment it creates, and the UI tells the user:

> "$exception is watched as a guardrail so a conversion win that broke something does not read as
> a win."

It has never run. Not once.

That is the whole thesis in miniature: **a promise nobody was assigned to keep.** A human PM would
have caught it, eventually, by noticing the number never changed. The product that intends to
replace the PM has to catch it by construction.

---

## 4. Phase 1 — The Investigator

**The missing organ. An agent that studies the product every day without being asked.**

Not a ticker that evaluates one thing. A sweep that asks, across the whole product, *what is
different and what is it costing*:

- what moved since yesterday, and beyond noise (`whats_notable`, `/v1/explain`)
- which ship did it (`deploy_impact` → `getCommitDiff`)
- where people are falling out now that they were not before (`funnel`, `paths`)
- which segment carries it (`breakdown`, `groups`)
- what shipped in the last 30 days that never moved anything (**the kill list**)
- what the product does that nothing measures (`instrumentation_coverage`)

**Ranked by money, not by p-value.** Every finding carries an estimated cost: `people affected ×
conversion delta × value per conversion`. A PM prioritises in currency; so must we. Where revenue
is not instrumented, rank by people affected and *say* that is what we did.

**Output: The Brief.** One artefact a day, and it is a PM's actual output, not a dashboard:

```
3 things changed. 1 needs you.

1. Checkout conversion fell 34% on Aug 9.        ~$4,200/mo
   Ship a1b3f9 "simplify address form" removed the
   autocomplete. Mobile Safari only — 61% of the loss.
   → PR #218 is open with the fix.

2. The onboarding tooltip you shipped Jul 21 did nothing.  kill it
   14 days, 2,900 users, +0.2% activation (inconclusive).
   It is still costing you a maintenance surface.

3. Signups from Reddit are up 3x since Aug 5.      ~$900/mo
   No campaign is running. One thread is sending them:
   r/webdev "what analytics do you use". Nobody replied yet.
```

Three findings, each with a number, a cause, and a next move. That is the thing a PM sends on a
Monday. Generated, every day, from events we already hold.

**Where it runs.** `cmd/smolanalytics/main.go:485` already has a 6-hour ticker and `:517` a
5-minute alert loop. The Investigator is a daily pass in the same process. On the cloud it can run
on the org's AI allowance; self-hosted it runs on the operator's own model over MCP, unmetered —
which is the same bring-your-own-model economics that makes the ask bar free.

**Tests**
- a seeded regression appears in the Brief with the right commit named
- a shipped-and-did-nothing flag appears on the kill list
- a finding whose cost cannot be estimated says so rather than inventing a number
- an instance with nothing notable produces "nothing needs you today" — never filler
- every number in the Brief matches the `/v1` endpoint that computes it

## 5. Phase 2 — The safety net that actually runs

Connect `EvaluateGuardrails` to the 5-minute loop. Persist `GuardrailStatus` +
`GuardrailCheckedAt` on the experiment. An unchecked guardrail renders **"not checked yet"**, never
PASS.

Then let it act. On two consecutive `FAIL`s ≥5 minutes apart, past a 15-minute warm-up:

1. `flag.Store.SetEnabled(key, false)` — the kill switch already exists
2. write `$experiment_reverted` into the instance's own log, so the revert sits on the timeline
   beside the traffic it reacted to and is itself queryable
3. record `RevertedAt` / `RevertedReason`
4. comment on the originating PR with the number, the margin and the action

`SMOLANALYTICS_AUTO_REVERT=off` disables acting and keeps reporting. The pane says which mode it
is in.

Two consecutive failures on top of an already always-valid sequential test is deliberate: a false
revert costs one experiment, but a *flappy* revert makes someone switch the whole thing off, which
costs them the safety net entirely.

## 6. Phase 3 — It writes the change

Only for the one shape where the evidence is unambiguous:

> A deploy caused a significant regression, `deploy_impact` names the commit, `getCommitDiff`
> hands over the diff.

Bounded, evidenced, checkable in thirty seconds. `fixbrief.resolveTrigger` already prefers exactly
this trigger.

Chain: trigger → diff → minimal fix behind a new flag → `openFixPr` with metric, delta, blamed
commit and rollout plan in the body → on merge roll to 10% → Phase 2 watches → clean after N
exposures, roll to 100% and comment.

**Never auto-merge.** A human merges, always. The audacity is closing the loop, not bypassing
review — and the first unattended merge that breaks someone's checkout kills the category for us.

Do **not** attempt "conversion is low." That is unbounded product judgment and produces PRs nobody
reads. We are replacing the finding-out, not the deciding.

## 7. The ladder — adoption path, trust path, price path

| | It does | Default |
|---|---|---|
| L1 | Tells you what changed and what it cost | on |
| L2 | Names the cause and writes it up | on |
| L3 | Opens the PR | propose |
| L4 | Ships it behind a flag at 10% | opt in |
| L5 | Reverts it automatically when it hurts | on, once Phase 2 lands |

Customers walk down this as trust accrues. Same ladder prices it: L1–L2 is the product, L3–L5 is
what a PM salary compares against.

## 8. The brave page

Autonoma's `Does this replace QA teams? Yes, and that's a good thing.` earns more trust than any
feature list, because it refuses to hedge. Ours:

> **Does this replace your product managers?**
> It replaces most of what they spend the week on: pulling data, working out why a number moved,
> writing it up, and chasing whether last month's ship worked. It does not decide what your
> company should build — keep the person who does that. Teams that used to need four PMs to stay
> informed need one to decide.

And beneath it, the thing no competitor can put on a page: **every number in that brief opens into
the rows it came from and recomputes in front of you.** The audacity is only sellable because the
arithmetic is checkable.

## 9. Order, and why

1. **Phase 1, The Investigator.** This is the product. Everything else is plumbing around it.
2. **Phase 2, the safety net.** Fixes a live broken promise, and makes autonomy safe enough to sell.
3. Dogfood both on our own instance for two weeks — the Brief has to be good enough that *we* read
   it every morning before we ask anyone else to.
4. **Phase 3, the fix PR.** This is the demo that gets posted.

**The test for whether this worked:** a founder cancels a PM req, or a PM stops opening the
dashboard because the Brief already told them. Nothing else counts.
