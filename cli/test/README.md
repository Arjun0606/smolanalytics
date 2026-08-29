# Running these tests

```sh
npm test        # node --test --test-concurrency=3 test/*.test.mjs
```

**Use the npm script, not a bare `node --test test/*.test.mjs`.** The concurrency cap is not
cosmetic, and the reason is worth knowing before somebody helpfully removes it.

## Why the cap exists — measured, 2026-08-29

`node --test` runs test FILES in parallel, defaulting to roughly one worker per core. Most files
here drive a real browser, and since cross-browser support landed some of them drive Firefox and
WebKit as well as Chromium. On an 8-core machine that meant up to eight workers each launching
browsers, all competing for the same cores.

The result was not a slightly slower suite. It was pathological:

| | wall clock | slowest single test |
|---|---|---|
| default concurrency (8 workers) | **799s** (13m 19s) | **688s** |
| `--test-concurrency=3` | **102s** | — |
| that same test, run in its own file | — | **8.5s** |

One test took **688 seconds in the suite and 8.5 seconds alone — 80x**. Nothing was wrong with it.
Browsers were starving each other, every navigation crawled, and the timeouts that make our
assertions honest turned into the thing consuming the time.

Two consequences, both bad, and neither announces itself:

- **A 13-minute suite does not get run.** It gets run once before a commit, then not at all, and
  the tests stop being the thing that catches mistakes. We sell a product whose entire argument is
  that a slow suite gets muted; ours was on exactly that path.
- **It reads as flakiness.** Under that contention, tests fail on connection-refused and timeouts
  that have nothing to do with the code, which teaches whoever is watching to re-run rather than to
  investigate. That is how a real defect gets waved through.

## If you change it

Three is not a magic number, it is what was measured on this machine. Raise it if the suite is
comfortably fast and nothing flakes; lower it on a 2-core CI runner. What must not happen is
removing the flag because "parallel is faster" — for a browser-heavy suite past the core count, it
is emphatically not.

Re-measure the same way rather than guessing: time the whole suite, then time the slowest file on
its own. If the two disagree by an order of magnitude, the machine is thrashing, not the test.
