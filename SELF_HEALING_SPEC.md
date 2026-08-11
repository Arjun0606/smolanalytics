# Self-healing: spec

**The sentence we want to be able to say, truthfully:**

> Something breaks. smolanalytics finds the ship that broke it, opens the PR that fixes it, rolls
> it out behind a flag, watches your error rate, and either finishes the rollout or reverts it —
> then tells you what it did.

That is the loop a PM runs. Not a dashboard that helps a PM run it.

---

## 1. Why this is a small build, not a moonshot

Every organ already exists. Verified in the tree on 2026-08-11:

| Step in the loop | Where it already lives | State |
|---|---|---|
| Notice something moved | `internal/insight` verdict, `whats_notable`, alerts | shipped |
| Find when it moved | `internal/whatchanged` → `/v1/explain` | shipped |
| Name the ship that did it | `deploy_impact`, `internal/fixbrief` | shipped |
| Read the offending diff | `github-app.getCommitDiff` | shipped |
| Write the fix as a PR | `github-app.openFixPr` | shipped |
| Ship it to a slice | `internal/flag` rollouts | shipped |
| Measure it honestly | `internal/flag` sequential inference, SRM, CUPED | shipped |
| **Turn it off when it hurts** | — | **missing** |
| **Run without being asked** | — | **missing** |

We have a body and no nervous system. The build is the last two rows.

## 2. The defect at the centre

`flag.EvaluateGuardrails` has **zero callers**. `EvaluateGuardrail` is called only by it.

Meanwhile `internal/api/experiment_api.go:131` attaches a `$exception` guardrail to *every*
experiment it creates, and the MCP tool lets people configure more. The copy tells the user the
guardrail is watching:

> "$exception is watched as a guardrail so a conversion win that broke something does not read as
> a win."

**Nothing has ever evaluated it.** Every experiment this product has created carries a safety net
that is not attached to anything. That is worse than not offering one — someone reading that
sentence ships a change more confidently than they should.

So Phase 1 is not a new feature. It is connecting a promise we already make.

## 3. Phase 1 — make the safety net real (the keystone)

**Goal:** every running experiment's guardrails are evaluated on a schedule, and the result is
visible. No automatic action yet.

**Where it runs.** `cmd/smolanalytics/main.go:517 alertLoop` already ticks every 5 minutes and
calls `app.EvaluateAlerts()`. Add `app.EvaluateGuardrails()` beside it. Same cadence, same
process, no new infrastructure, and it inherits the tab-visibility and boot behaviour that loop
already has.

**New:** `internal/api/guardrail_eval.go`

```go
// EvaluateGuardrails checks every RUNNING experiment's guardrails against current events and
// records the verdict on the flag. Reports only — Phase 2 acts.
func (s *Server) EvaluateGuardrails() []GuardrailBreach
```

For each flag where `f.Experiment != nil && f.Experiment.Running`:
1. load events (`s.store.Range`), apply `query.Apply(evs, nil)` — production scope, same as
   everything else, and drop the sampler
2. build one `flag.GuardrailInput` per configured guardrail
3. `flag.EvaluateGuardrails(ins)`
4. persist the latest `[]GuardrailResult` onto the experiment (new field
   `Experiment.GuardrailStatus []GuardrailResult` + `GuardrailCheckedAt time.Time`)

**Surface it.** The experiment pane must show, per guardrail: `PASS` / `FAIL` / `INCONCLUSIVE`,
the margin, and *when it was last checked*. An unchecked guardrail must render as "not checked
yet", never as PASS — the whole bug we are fixing is a safety claim nobody verified.

**Tests**
- a running experiment whose guardrail event spikes in the treatment arm produces `FAIL`
- a healthy experiment produces `PASS`
- an experiment with too little data produces `INCONCLUSIVE`, never `PASS`
- `GuardrailCheckedAt` is zero before the first run, and the UI says so rather than implying health
- **the copy test:** no surface may claim a guardrail is "watched" unless `EvaluateGuardrails` has
  a caller. Assert the call exists in `alertLoop`. This is the guard against re-shipping the
  original defect.

## 4. Phase 2 — let it act (auto-revert)

**Goal:** a confirmed guardrail breach turns the flag off, by itself, and says so loudly.

`flag.Store.SetEnabled(key, false)` is the kill switch and already exists.

**The rule.** Disable when **all** hold:
- guardrail `Status == "FAIL"` (the sequential test is already always-valid, so a single check is
  statistically legitimate — this is exactly what that machinery was built for)
- the same guardrail returned `FAIL` on **two consecutive** evaluations ≥5 minutes apart
- the experiment has been running longer than a short warm-up (default 15 min) so a cold-start
  blip cannot trigger it

Two consecutive failures is deliberate belt-and-braces on top of an already-valid test: the cost
of a false revert is a wasted experiment, but the cost of a *flappy* revert is someone turning the
whole feature off, which loses the safety net entirely.

**What it does on breach**
1. `SetEnabled(key, false)`
2. write an `$experiment_reverted` event into the instance's own log — so the revert appears on the
   timeline beside the traffic it was reacting to, and is itself queryable
3. record `Experiment.RevertedAt`, `RevertedReason`
4. fire the existing alert/webhook path
5. if the experiment came from a fix PR, `github-app.commentOnPr` with what it saw and what it did

**The copy on the pane matters as much as the mechanism.** It must state the number that moved,
the margin it breached, and that the flag is now off. "Reverted automatically" with no evidence is
the same class of unearned claim we spent a week removing.

**Kill switch for the kill switch.** `SMOLANALYTICS_AUTO_REVERT=off` disables Phase 2 entirely and
leaves Phase 1 reporting. Anyone who does not want software turning their flags off must be able
to say so in one env var, and the pane must say which mode it is in.

**Tests**
- one FAIL does not revert; two consecutive do
- a FAIL inside the warm-up window does not revert
- revert writes the event, sets `RevertedAt`, and disables the flag
- `SMOLANALYTICS_AUTO_REVERT=off` reports and never acts
- a reverted experiment is never silently re-enabled by a later evaluation

## 5. Phase 3 — close the loop (the autonomous fix)

Only after Phases 1–2 have run on our own instance for a week.

**Scope it narrowly, or the PRs will be garbage.** Do **not** attempt "conversion is low" — that is
unbounded product judgment. Attempt exactly one shape:

> A deploy caused a significant regression, `deploy_impact` names the commit, and
> `getCommitDiff` hands us the diff that did it.

That is bounded: the fix is usually "repair or revert this specific change", the evidence is a
diff, and a human can check it in thirty seconds. `internal/fixbrief` already assembles this
trigger — `resolveTrigger` prefers "a significant deploy regression (it names a commit)".

**The chain**
1. `fixbrief` trigger fires with a named commit
2. `getCommitDiff(commit)` → the change
3. the agent proposes a minimal fix, gated behind a new flag
4. `openFixPr` with: the metric, the size of the move, the commit blamed, the diff, and the
   rollout plan written into the PR body
5. on merge, the flag rolls to 10%
6. Phase 1 watches it; Phase 2 reverts it if it hurts
7. if clean after N exposures, roll to 100% and comment on the PR

**Default is propose, not ship.** `SMOLANALYTICS_AUTOFIX=propose|ship`, default `propose`. Nobody
lets unattended software write and merge product code on day one, and pretending otherwise loses
the customer at the exact moment they were interested.

## 6. The trust ladder (this is also the pricing ladder)

| Level | What it does | Config |
|---|---|---|
| L1 | Tells you something broke | always |
| L2 | Names the commit and writes the brief | always |
| L3 | Opens the fix PR for you to review | `AUTOFIX=propose` (default) |
| L4 | Ships it behind a flag at 10% | `AUTOFIX=ship` |
| L5 | Reverts automatically when it hurts | `AUTO_REVERT=on` (default once Phase 2 lands) |

A customer moves down this ladder as they come to trust it. That is the adoption path *and* the
upgrade path, and it is honest at every rung.

## 7. What we deliberately do not do

- **No auto-merge.** Ever. A human merges. The audacity is in the loop, not in bypassing review.
- **No fixes without a named commit.** If we cannot point at the diff, we write a brief and stop.
- **No silent action.** Every autonomous act writes an event, updates the pane, and comments on
  the PR. A system that acts invisibly is indistinguishable from a broken one.
- **No claim in the copy that outruns the config.** If `AUTO_REVERT=off`, no page may say the
  guardrail protects them.

## 8. Risks, honestly

- **A bad revert during a real launch.** Mitigated by two-consecutive-FAIL, the warm-up window and
  the env kill switch — and the blast radius is one flag, which the customer chose to put a change
  behind.
- **The fix PR is wrong.** Likely, often. This is why default is `propose` and why we only attempt
  the named-commit shape. A wrong PR a human closes costs nothing; a wrong PR that merged itself
  costs the company.
- **It looks like magic until it is wrong once.** The defence is evidence in every artefact: the
  number, the margin, the commit, the diff. Same discipline as `/v1/rows` — the claim always ships
  with the rows behind it.

## 9. The order

1. **Phase 1** — connect the guardrail evaluator. Small, and it fixes a live defect where we claim
   a safety net that does not run.
2. **Phase 2** — auto-revert. This is the keystone: it is what makes the brave sentence sayable.
3. Run both on our own instance for a week.
4. **Phase 3** — the autonomous fix, `propose` by default.

Phase 1 + 2 is the whole story change. Phase 3 is the demo.
