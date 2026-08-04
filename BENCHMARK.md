# instrumentation time, measured

Mixpanel publishes what instrumenting their product costs: **~30 minutes per event, 10-30
engineer hours for a 20-60 event product, and most customers finishing around day 40.**

That is their number, on their own docs, and it is the benchmark worth beating in public —
because the work it describes is deciding *what* to track and writing the calls, which is exactly
what `instrumentation_coverage` and `propose_instrumentation` do.

Below is what ours actually costs, measured rather than estimated, on real repositories.

## the measurement

Subject: `smolanalytics-cloud`, a Next.js app — 44 files containing user-facing actions.

| step | what it answers | measured |
|---|---|---|
| `instrumentation_coverage` | what does this product do that nothing measures? | **64 actions found**, all uncovered |
| `propose_instrumentation` | write the tracking for the highest-value ones | **4 edits**, each a real auth call site with file, line and snippet |
| **both, wall clock** | repo → a reviewable instrumentation plan | **5.5 seconds** |

Run it yourself:

```
smolanalytics mcp   # then call instrumentation_coverage and propose_instrumentation
```

## what this does and does not claim

**It measures:** going from a repository nobody has instrumented to a named list of what should
be tracked, plus generated edits with the exact file, line and snippet for the high-value events.

**It does not measure:** applying the edits (agent or editor time, minutes), or verifying events
arrive in production (`verify_instrumentation`, which needs real traffic). A number that claimed
otherwise would be marketing rather than a benchmark.

So the honest comparison is against the part of Mixpanel's 30-minutes-per-event that is *deciding
what to track and writing the call*. That is most of it, but not all of it, and the claim should
be stated that way or the first person who checks will find the gap.

## why the numbers are shaped like that

**64 actions but only 4 edits** is deliberate, not a shortfall. Coverage reports everything
user-facing so a person can choose; propose only generates code for the events it can name
correctly and place precisely. Generating 64 half-right edits would produce a PR nobody merges.

**4, not 16.** An earlier run proposed 16, and every one was wrong — `track("checkout")` in
`Funnel.tsx`, a dashboard chart component where "checkout" is a funnel *step name*. That is what
running the benchmark actually found, and it was worth more than the benchmark: the patterns were
matching mentions rather than calls. Three further classes turned up while verifying against real
repositories instead of fixtures:

- matching the **tail of a longer identifier**, so `instanceLogin()` — a server-to-server call —
  was proposed three times as a user login
- **Go test files never skipped**, because the guard checked dotted suffixes like `.test.ts` while
  Go uses `foo_test.go`; the scanner was proposing instrumentation for its own fixtures
- requiring a bare `signIn(` found **nothing at all** in a real better-auth codebase, which calls
  `signIn.social()`, `signIn.magicLink()` and `signUp.email()`

All four are pinned as tests. None of them was visible in unit tests that passed.

## the honest read

Five and a half seconds to a reviewable plan is a real number and it is the right one to publish,
but it is not the whole job. The claim that survives scrutiny is:

> Mixpanel's own docs put instrumentation at 30 minutes per event and most customers finishing
> around day 40. Deciding what to track and writing the calls takes seconds here. Applying and
> verifying is the rest of an afternoon.

That is checkable, it is their number rather than ours, and it does not collapse when someone
tries it.
