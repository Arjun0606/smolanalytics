# Experimentation, as built

What actually shipped, why it is shaped this way, and the one number that justifies all of it.
The ranked plan is in `EXPERIMENTATION_SPEC.md`; the adversarial review of that plan is in
`EXPERIMENTATION_CRITIQUE.md`. This file is the record of what exists.

---

## The number

```
A/A harness — 2,000 simulated experiments, peeked every 100 users up to 20,000
  sequential   false-positive rate = 0.015
  fixed        false-positive rate = 0.426        (nominal 0.05)
```

`internal/flag/sequential_test.go`, seeded RNG, no network, runs in CI.

`significant()` was a fixed-horizon 1.96 z-test sitting behind a dashboard built to be opened
every morning. A fixed-horizon test is only valid if you look **once**, at a sample size you
committed to in advance. Looked at repeatedly, it calls a false winner about **43%** of the time.
That is not a rounding error on the advertised 5% — it is the difference between a tool that
helps you ship and one that talks you into shipping nothing.

So always-valid inference is the default now, and the width it costs is printed on the page.

---

## What is in the engine

All of `internal/flag`, all pure, all deterministic, no Monte Carlo in any reportable number.

| File | What it is |
|---|---|
| `sequential.go` | Asymptotic confidence sequences. `K` is the inflation over the fixed half-width; `LiftZ` is that same inflation as a critical value, so the relative interval widens by exactly the factor the absolute one does. Always-valid p-value by deterministic bisection. |
| `power.go` | Sample size, MDE and duration for two proportions. Feeds `n_tune`. |
| `experiment.go` | The pre-registered plan: control arm, goal, guardrails, alpha, mode, N\*, layer, hash version. Locked on start, canonically hashed. |
| `guardrail.go` | One-sided non-inferiority tests. A margin of zero is refused by name — at δ=0 the test can essentially never pass at finite n. |
| `multiplicity.go` | Benjamini-Hochberg over (variants × metrics) within one experiment at one look. The family and its size are named in the payload. |
| `cuped.go` | Variance reduction by regression adjustment, with an honest refusal when the covariate is too weakly correlated to help. |
| `ratio.go` | Ratio metrics via the delta method, with a cluster-robust cross-check. |
| `srm.go` | Sample-ratio-mismatch, χ² at p<0.001. Pre-existing and correct; it simply had nowhere to be seen. |

## The decisions that were not in the spec

The critique found nineteen places where an implementer would have had to guess. These are the
rulings, all of them load-bearing:

1. **No pure function calls `time.Now()`.** Windows resolve to absolute instants at the API
   boundary and are echoed in the response. `Measure` used to resolve `days` against the clock
   *inside* the computation, so the same events returned a different report every run — on a
   product whose claim is byte-identical answers.
2. **The randomisation unit is `bucket_id`**, device-scoped so it survives `identify()`, falling
   back to `distinct_id` when absent. The report states which it used. Analysing by
   `distinct_id` splits one person's behaviour in half at the login: exposed anonymously,
   converts signed-in, and the conversion attaches to a unit that was never exposed. Measured
   before the fix: 40 exposed, 0 converted, where the truth was 40/40.
3. **`n_tune` comes from `n_planned`** when the plan declares baseline + MDE + power. `k(N)` is
   minimised near N\*, so a wrong default systematically widens everyone's intervals.
4. **`Resolve()` is idempotent.** It re-derived `n_tune_source` on every call, so loading a
   stored plan and saving it back reported a change nobody made and tripped its own lock.
5. **Sequential is the default; fixed is gated.** A fixed-horizon test read before its
   pre-registered N renders the always-valid interval instead, and says why — rather than
   printing a number that only becomes true later.
6. **A layer is immutable.** Setting one on a flag that already has exposures is an error;
   folding it into the salt would re-randomise everyone already in the experiment.
7. **Everything is echoed**: mode, mode-reason, alpha, N\* and its source, randomisation unit,
   `pre_registered`, and the resolved window.

## Where it surfaces

- `GET /v1/flags/{key}/measure` — the A/B read, now with the design on it.
- `GET /v1/flags/{key}/health` — SRM. **This had no route at all.** The best code in the package
  was callable from exactly one MCP tool, while the A/B report's own note told the reader to go
  and check it. A check nobody can reach is a check nobody runs.
- The flags pane: SRM banner above the table greying out the lift column when the split is
  broken, the intervals the renderer used to compute and discard, a `never served` marker on any
  arm with zero exposures, and the design line underneath.

## Known sharp edge

With no pre-registered design, N\* falls back to 5000. On a small experiment that reads as
*"327% wider than a fixed-horizon test"* — correct, honest, and alarming out of context. Running
the power calculator and declaring a design collapses it. If experiments here routinely have no
plan, revisit the default rather than the arithmetic.

## Not built

Holdouts, mutually-exclusive layer *allocation* (the layer field and its immutability exist; the
allocator does not), and MCP tools for the new experiment surfaces. The HTTP endpoints are there;
`internal/mcp` has not been extended to match, and `readonly_guard_test.go` will fail the build
until any new tool is classified read-or-write — which is deliberate.
