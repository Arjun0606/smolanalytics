# What we're building

**The vision, one line:** every ship gets adjudicated, and nobody has to remember to check.

Not "analytics with AI". The job nobody currently does — deciding whether the thing you shipped
did what it was supposed to do — done automatically, on every ship, forever.

---

## The one object that does not exist yet

Everything below hangs off a single new record: **the Claim.**

```
Claim {
  ship        // the deploy marker / commit / PR
  metric      // the event it should move
  direction   // up | down
  segment     // for whom, optional
  size        // the smallest move worth calling a win, optional
  due         // when there will be enough traffic to answer
  state       // drafted -> accepted -> locked -> adjudicated
  verdict     // one of a closed set, filled at `due`
  evidence    // the rows behind the verdict
}
```

A claim is a **prediction with a deadline**. That is the whole product. Analytics tells you what
happened; a claim says what was supposed to happen and then holds you to it.

Deploy markers already carry `SHA / Message / Ref / URL / At`. Power planning already computes
`NPlanned` and `RecommendedDays` — that is `due`, already built. Provenance already produces the
evidence. **The claim is the missing noun that turns a pile of capabilities into a product.**

---

## Layer 0 — already built, do not rebuild

| | |
|---|---|
| Investigator | daily unprompted sweep, ranked by cost |
| Cause attribution | which ship, and where the loss concentrates |
| Kill list | shipped, had the traffic, moved nothing |
| Guardrails | evaluated every 5 min, three states |
| Auto-revert | two confirmed failures, receipt in the event log |
| Autofix | opens a PR for named-commit regressions, off by default |
| Backtest | replays 90 days, dates every finding |
| Provenance | `/v1/rows` — every number opens into its rows |
| GitHub App | reads repos, reads diffs, opens PRs |
| Power planning | how much traffic before a question is answerable |

---

## Layer 1 — the Ship Ledger (the wedge)

### F1 · The claim record
Store, lifecycle, one page listing every claim by state. **Append-only**: adjudicating never
overwrites, it appends and repoints a `current` marker. Stolen directly from Autonoma's classifier
— re-filing an item must not erase what was said about it last time.
*Effort: 3-4 days.*

### F2 · Claim derivation from the PR
On merge, read the PR title, body and diff and draft the claim. Which event does this touch, which
way should it move, who for.
**The hard part, and the one that can sink this.** Many merges are refactors, infra, copy or
compliance with no measurable intent. If fewer than ~40% of merges yield a checkable claim, rescope
to *deploys that touch instrumented flows only* — smaller, still real.
*Effort: 1 week. Test it on 200 real PRs before building F4.*

### F3 · One-click accept
The draft is shown once. Accept, edit, or mark "no measurable intent" — which is a valid and
common answer and must be one click, or the product becomes a form.
Then it locks. A claim editable after the data arrives is not a claim.
*Effort: 2 days.*

### F4 · The Adjudicator
At `due`, decide without being asked. **Closed verdict set, no fallback path:**

- `moved` — it did the thing
- `did not move` — it did not
- `moved the wrong way` — worse than before
- `cannot tell: traffic too thin` — not enough people, ever or yet
- `cannot tell: not instrumented` — the metric was never being recorded
- `cannot tell: confounded` — another ship landed inside the window

The last three are the honest ones and they are why this is believable. Autonoma's rule applies
exactly: no default verdict, and any auto-repair of the claim gets reverted if it does not hold —
otherwise the ledger learns to make itself look right.
*Effort: 4-5 days. Most of the maths already exists.*

### F5 · The scorecard
One artefact per adjudicated ship: the claim, the verdict, the number, the rows behind it, and the
deploy that carried it. Delivered as a **PR comment** — Autonoma's real insight is that the unit
of delivery is the developer's existing workflow, never a new tab.
*Effort: 3 days.*

### F6 · The quarterly rollup
"You shipped 23 things this quarter. 7 moved something. 4 made it worse. 12 could not be told
apart from nothing." **This is the sentence that sells the product**, and it is one query over the
ledger once F1-F4 exist.
*Effort: 2 days.*

---

## Layer 2 — making it land

### F7 · The 90-day backtest — **DONE**
`smolanalytics backtest --days 90`. The cold open: what it would have told you, and when.
Needs a hosted no-login version to be a demo rather than a CLI command. *Effort: 3 days.*

### F8 · The weekly email
The Brief already computes it. Wire it to the ledger so the weekly note leads with adjudications:
what came due, what the verdict was.
*Effort: 2 days.*

---

## Layer 3 — the money

### F9 · Stripe restricted read key
The customer makes a scoped read-only key themselves. No approval, no integration deal.
Turns "checkout conversion fell 3.1%" into "**$4,200/month**, 62% on Android".
Every finding in the Investigator is currently sized in *people* because there is no revenue to
size it in. This is the highest quality-per-day item on the entire list.
*Effort: 3-5 days.*

---

## Order, and why

| | | |
|---|---|---|
| 1 | Test F2 on 200 real PRs | 2 days. If under 40% yield a claim, the wedge changes shape. Do this first. |
| 2 | Backtest against 10 real products | 3 days. Ask *knew / didn't know / that's wrong*. Under 30% "didn't know" and stop. |
| 3 | F1 + F3 + F4 | the ledger spine |
| 4 | F5 + F6 | the artefacts people quote |
| 5 | F9 | money instead of people |
| 6 | F8 + hosted backtest | the funnel |

**Steps 1 and 2 are tests, not builds, and together they are a week.** Both are cheap ways to be
told this is wrong before spending a quarter on it, and both use code paths the product needs
anyway.

---

## What we are deliberately not building

- **PRDs, specs, roadmaps, prioritisation.** Unverifiable, therefore unprovable, therefore never
  commands a headcount price. ChatPRD reached 100k+ PMs and sits at six figures.
- **Feedback synthesis / voice of customer.** Viable dead, Zeda shutting down, Kraftful and
  Monterey absorbed, survivors at $7.99/month.
- **Any environment, seeding or replay layer.** Autonoma spent ~660KB of TypeScript plus an
  eight-language protocol on this and ~48% of their runs still die in it.
- **A merge gate.** Product impact needs days of traffic; a status check must resolve in minutes.
  The matching event is the rollout window, not the pull request.
- **SSO, SOC 2, audit log.** 2-4 quarters and cash. Trigger: a signed LOI at ≥$15k blocking on
  nothing else.
