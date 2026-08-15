# The Weekly Update

**The biggest automatable slice of the PM week (15-20%), and the one thing only we can do —
because writing the update is a commodity and knowing the numbers is not.**

Copilot, Notion AI and ChatGPT can all write a status update. None of them can see the event log.
They cannot say "checkout fell 70% on the 3rd, ship a1b3f9 did it, 62% of the loss is Android,
here are the 1,260 rows." We hold both halves, and nobody else does.

## Why this and not more coverage

Percentage of a PM's week is the wrong unit. 81% of PMs say AI saves them time; only 44% say the
job got easier, because reclaimed hours are absorbed by coordination. Automating 60% of the week
would still not remove a head.

What converts is different:

- **For a team with no PM** — the buyer — this is not a percentage. It is the difference between
  flying blind and not. They are comparing us to *nothing*, because nobody is doing any of it.
- **For a team with PMs**, the only honest lever is span: nobody checks all of it, ever.

And there is a second-order reason that matters more than either. **A weekly update is forwarded.**
The Brief is something one person reads alone; an update is something a founder sends to their
team. That is distribution inside the account, for free, with no sales motion — and this founder
has no sales motion.

## The four sections

### 1 · What moved — the metric inversion

**This is the new computation, and it is the one that survived the F2 test.**

Per tracked metric, over 90 days: the value then, the value now, whether the difference is
statistically real, and **how many ships landed in between**.

```
checkout conversion   3.1% -> 3.0%   unchanged   across 23 ships
activation            12%  -> 15%    up 25%      across 23 ships
signup                                unchanged   across 23 ships
```

"Statistically unchanged across 23 ships in 90 days" is the hardest sentence in the product to
ignore, and it needs **no claim object** — which is exactly why it survives where the per-ship
ledger did not. 3.8% of merges yield a checkable claim; 100% of metrics yield this.

*Not new maths.* `trends` gives the values, `twoProportionP`/`wilson` in `internal/flag/interval.go`
give the significance, `deploys.List()` gives the count. It is a join.

### 2 · What shipped, and what we can attribute

Only deploys where `deploys.ComputeImpact` says the move is significant. Everything else is listed
as shipped with no attributable effect — which is the honest majority and must be shown as such,
not hidden. A list of 23 ships with 2 attributions is the finding.

### 3 · What is dead weight

The kill list, already built: shipped, had the traffic to answer, moved nothing. Ranked by how
long it has been sitting there.

### 4 · What needs a decision

The `NeedsYou` findings from the Investigator, already built. Capped at three. An update where
everything is urgent has no triage in it.

## Delivery

- **A stable share URL per project.** `GET /share/{token}` exists — public, read-only,
  token-gated. Extend it to render the update. This is the forwardable artefact and the whole
  distribution argument.
- **A weekly email.** `internal/brief` already computes and emails. Point it at this.
- **Every number opens into its rows.** `/v1/rows` behind each figure, exactly as the KPI proof
  links do. A forwarded document gets read by someone who did not choose to trust us, so the
  evidence has to travel with it.

## What is new vs what exists

| | |
|---|---|
| Metric inversion (value then/now, significance, ships between) | **new — the only real build, ~3 days** |
| Attributable ships | exists (`deploys.ComputeImpact`) |
| Kill list | exists (`internal/investigate`) |
| Needs-a-decision | exists (`Investigation.NeedsYou`) |
| Share URL, token-gated | exists (`GET /share/{token}`) |
| Weekly email | exists (`internal/brief`) |
| Row-level proof | exists (`/v1/rows`) |

**Roughly a week, and most of it is assembly.**

## What it deliberately does not do

- **No prose generation.** It is a structured document, not an essay. The moment it writes
  paragraphs it becomes a thing Copilot also does, and the differentiator evaporates.
- **No "what's next".** That is roadmap, that is judgment, that is the human's. Three of the four
  things a PM writes weekly is the correct amount to take.
- **No per-ship claims.** Refuted at 3.8%.
- **No invented causes.** If the move cannot be attributed, the line says so.

## The test

A founder forwards it without editing it. If they rewrite it before sending, the format is wrong
and the tone is wrong, and we should ask them what they changed.
