// PARALLEL SUITE EXECUTION — the concurrency, and nothing else.
//
// lib/suite.mjs ran `for (const [i, t] of tests.entries())`. Fifty replays is 39.6s, which is fine.
// Fifty FIRST-TIME agent runs, or fifty replays that all went stale after a redesign, is eight to
// twenty minutes of CI, one test at a time. This file runs N of them at once without changing a
// single verdict.
//
// THE FOUR THINGS THAT MAKE THIS SAFE, each of which has a test that can fail:
//
//   ORDER. Results come back indexed by the suite's own order, never by completion time. The
//   summary, the pull request comment and the exit code are byte-identical to a serial run.
//
//   READABILITY. Eight workers writing to one terminal is unreadable and worse than serial. Each
//   test's lines are buffered and flushed as ONE block when that test finishes, so the transcript
//   still reads test-by-test. A "N/M done" line follows each block so nobody thinks it hung.
//
//   ISOLATION. One worker throwing is `errored` for its own test and nothing else. The other
//   workers keep their verdicts.
//
//   ONE LOGIN. lib/auth.mjs signs in once and writes a session file every later test reuses. Eight
//   workers starting at the same instant would all find no file and all sign in — eight logins,
//   eight sets of credentials on the wire, and on some apps a rate-limit that reads as the app
//   being broken. When a login is needed and no session exists yet, the first test runs ALONE and
//   the rest are released after it, which is exactly what serial did.
//
// ONE BROWSER, N CONTEXTS — MEASURED, NOT ASSUMED, on this machine (8 cores, 17.2GB) against a
// 50-flow fixture, with `ps -Ao rss` summed over every Chromium process.
//
// The primitives:
//
//   context + page + goto on an existing browser :  35-46ms,  ~100MB marginal (128MB browser base)
//   launch + page + goto, its own browser        : 206-225ms, ~257MB each
//   eight of them at once                        :  828MB shared   vs   2054MB separate
//
// And the same 50-test suite run both ways, same concurrency, same verdicts (50/50 passed each):
//
//              a Chromium per test        one Chromium, a context per worker
//   4 workers  14.8s, peak 1450MB         8.1s, peak  741MB
//   8 workers   9.6s, peak 2233MB         4.9s, peak  917MB
//
// So the naive version is ~1.9x slower and ~2.4x heavier. Every test already ran in its own
// context — the isolation boundary is the context, not the process — so the win is running N
// contexts at once inside one Chromium. That matters most on the 2-core, 7GB CI runner this is
// for, where eight Chromiums is the difference between slow and swapping.

import os from "node:os";
import { existsSync } from "node:fs";
import { DEFAULT_AUTH_DIR, authFileFor, prepareLogin } from "./auth.mjs";
import { DEFAULT_ENGINE, launchEngine } from "./engines.mjs";

// The same dim as lib/suite.mjs uses, kept local so this file imports nothing from the caller it
// is meant to be separable from.
const DIM = (s) => `\x1b[2m${s}\x1b[0m`;

// ---- how many ------------------------------------------------------------------------------------

/**
 * The default, and why it is this number.
 *
 * MEASURED end to end, `npx smolanalytics test --suite`, 50 recorded tests, this machine:
 *
 *   workers  1   39.2s        workers  6   6.1s        workers 16   3.8s
 *   workers  2   15.0s        workers  8   4.9s
 *   workers  4    8.2s        workers 12   3.7s
 *
 * Better than linear to 4, because sharing one browser also deletes the 200ms launch each of the
 * fifty tests used to pay. It flattens at 12 and turns at 16, where aggregate test time balloons
 * from 39s to 53s: past the knee the tests are not faster, they are just queueing inside Chromium,
 * and a test that takes twice as long is a test twice as close to its own timeout. Serial spends
 * 43% of ONE core across those 39.2s — this work is mostly waiting on navigation, which is why it
 * parallelises past the core count at all.
 *
 * THREE CEILINGS, and the default is the smallest of them:
 *
 *   CPU. `cpus - 1`, leaving a core for the application under test, which on CI is usually the
 *   same runner. A 2-core runner therefore gets 2, not 1 — this workload is not CPU-bound and 1
 *   would hand no speedup at all to the machine that needs it most.
 *
 *   MEMORY. ~100MB per context plus a 128MB base, measured above. A 7GB CI runner could hold far
 *   more than we ask for; a 2GB container could not hold twelve. One worker per 512MB of total
 *   memory is a deliberately conservative read of a 100MB measurement, because the number that
 *   matters is the application's peak page, not our floor.
 *
 *   THE MODEL, which is the ceiling the numbers above cannot see, because a replay never calls it.
 *   A first-time suite is N concurrent agent runs, each a chain of Claude calls. The lowest
 *   Anthropic rate-limit tier is ~50 requests a minute; one agent turn is one request and a turn
 *   takes a few seconds, so four concurrent chains sit inside that and eight does not. A 429 here
 *   arrives as an `errored` test — our own throughput wearing the application's clothes, on
 *   somebody's pull request. So the cap is 4 whenever a key is set and the suite could actually
 *   call the model, and 8 when it cannot. This deliberately under-serves the common case of a
 *   replay suite that happens to have a key; `--workers <n>` is the answer for anyone who has
 *   measured their own runner and their own rate limit.
 *
 * On this machine that resolves to 7 without a key and 4 with one. A 2-core 7GB Actions runner
 * gets 2 either way. Measured at the default, three times: 5.47s, 5.39s, 5.36s — 50/50 passed,
 * exit 0, every time.
 */
export function defaultWorkers({ cpus = os.cpus().length, mem = os.totalmem(), hasKey = false } = {}) {
  const byCpu = Math.max(2, (Number(cpus) || 1) - 1);
  const byMem = Math.max(1, Math.floor((Number(mem) || 0) / (512 * 1024 * 1024)));
  const cap = hasKey ? 4 : 8;
  return Math.max(1, Math.min(byCpu, byMem, cap));
}

/**
 * `--workers` off the command line.
 *
 * REFUSED, not defaulted, the same shape as --retries and --layout in bin/smolanalytics.mjs: a
 * typo'd `--workers eight` silently becoming the default would hand somebody who explicitly asked
 * for a number whatever we felt like, and `--workers 0` would run no tests at all.
 */
export function parseWorkers(raw, { cpus, mem, hasKey } = {}) {
  if (raw === undefined) return { workers: defaultWorkers({ cpus, mem, hasKey }), problem: "" };
  const s = String(raw).trim();
  if (!/^\d+$/.test(s) || Number(s) < 1) {
    return { workers: 0, problem: `--workers needs a whole number of 1 or more, got ${JSON.stringify(raw)}. 1 runs the suite one test at a time, as it always did.` };
  }
  return { workers: Number(s), problem: "" };
}

// ---- the buffered transcript ---------------------------------------------------------------------

/**
 * One test's lines, held until that test is done, then written as one block.
 *
 * Arguments are kept as given and replayed with the same arity: callers here pass a single string,
 * but lib/auth.mjs logs `(...parts)` and a sink that joined them itself would change what a login
 * prints.
 */
export function buffered(sink) {
  const held = [];
  return {
    log: (...a) => {
      held.push(a);
    },
    // Synchronous from first line to last, which is what keeps two workers' blocks from braiding:
    // nothing else can run between them.
    flush: () => {
      for (const a of held) sink(...a);
      held.length = 0;
    },
    get lines() {
      return held.length;
    },
  };
}

// ---- ordered concurrency -------------------------------------------------------------------------

/**
 * The record a test gets when the runner itself threw.
 *
 * `errored`, never `failed`: our crash says nothing whatsoever about the application. Every field
 * lib/suite.mjs's summary, comment and exit code read is present, so one dead worker costs exactly
 * one verdict and changes nothing about the other forty-nine.
 */
export function erroredResult(item, err) {
  const why = err && err.message ? err.message : String(err);
  return {
    ...item,
    status: "errored",
    mode: "",
    reason: `The runner threw: ${why}. This is the test runner, not your application.`,
    ms: 0,
    refreshed: false,
    layout: [],
    suspects: [],
  };
}

/**
 * Run `fn` over `items` with at most `workers` in flight, and return the results IN INPUT ORDER.
 *
 * The ordering is the whole contract. A pool that pushed results as they finished would reorder
 * the pull request comment on every run and make two identical suites produce two different
 * transcripts — and the suite's `runId` suffix, which keeps signup identities apart, is derived
 * from the index, so the index has to travel with the work rather than be handed out by arrival.
 */
export async function mapOrdered(items, fn, { workers = 1, gateFirst = false, onError = erroredResult } = {}) {
  const list = Array.from(items);
  const out = new Array(list.length);
  if (!list.length) return out;

  let next = 0;

  const one = async (i) => {
    try {
      out[i] = await fn(list[i], i);
    } catch (e) {
      // Caught HERE, per item. A rejection escaping into Promise.all below would abandon every
      // other worker's in-flight test and lose verdicts that were already earned.
      out[i] = onError(list[i], e, i);
    }
  };

  // Exactly the old loop, in the old order, with nothing else in the way. `--workers 1` has to be
  // today's behaviour and not an emulation of it.
  const n = Math.max(1, Math.min(Math.floor(workers) || 1, list.length));
  if (n === 1) {
    for (let i = 0; i < list.length; i++) await one(i);
    return out;
  }

  // THE LOGIN GATE. The first test runs alone, then the rest are released. See the header.
  if (gateFirst) {
    next = 1;
    await one(0);
  }

  const worker = async () => {
    // `next++` is safe without a lock: this is one event loop, and there is no await between the
    // read and the write.
    for (let i = next++; i < list.length; i = next++) await one(i);
  };
  await Promise.all(Array.from({ length: n }, worker));
  return out;
}

// ---- one browser, a context per worker -----------------------------------------------------------

/**
 * A `loadBrowser` to hand lib/test.mjs that shares ONE Chromium across every test.
 *
 * runOnce already takes `loadBrowser` injected, and it only ever asks the result for
 * `pw.chromium.launch()`, `browser.newPage()`, `browser.newContext()` and `browser.close()`. So
 * the whole of sharing is a stand-in for `launch()` that hands back a lease on the one real
 * browser: contexts pass straight through, and `close()` closes the contexts THIS test made and
 * leaves the browser alone.
 *
 * The isolation is unchanged. A Playwright context is the isolation boundary — its own cookies,
 * its own storage, its own cache — and every test still gets a brand new one. What is shared is
 * the process, which holds none of that.
 *
 * THE TRADE-OFF, stated rather than hidden: one process behind N workers means one crash can end N
 * tests. What that must never do is turn into `failed`, which is a bug report about somebody's
 * application. A lease on a dead browser throws, lib/suite.mjs catches it, and the test is
 * `errored` with our own sentence on it — there is a test that holds that line.
 */
export function sharedBrowser({ load } = {}) {
  let loading = null;
  let realBrowser = null;
  let realKey = "";
  let launched = 0;
  let loads = 0;

  const wrap = (browser, owned) => {
    // Every context this lease opens, so close() can close exactly those and no others.
    const mine = new Set();
    const track = async (c) => {
      mine.add(c);
      return c;
    };
    return {
      newContext: async (options) => track(await browser.newContext(options)),
      // Playwright's own browser.newPage() opens an owned context behind the page and closes it
      // with the page. Doing it explicitly is the same isolation, and it is the only way this
      // lease can close what it opened without closing the shared browser.
      newPage: async (options) => (await track(await browser.newContext(options))).newPage(),
      close: async () => {
        for (const c of mine) await c.close().catch(() => {});
        mine.clear();
        if (owned) await browser.close().catch(() => {});
      },
      contexts: () => browser.contexts(),
      isConnected: () => browser.isConnected(),
      version: () => browser.version(),
    };
  };

  const lease = async (pw, engine, options = {}) => {
    // The ENGINE is part of the key (lib/engines.mjs): --browser webkit next to --browser chromium
    // is two different processes, and sharing one under a key that ignored the engine would hand a
    // WebKit test a Chromium — a green verdict about a browser nobody ran.
    const key = JSON.stringify([engine, options || {}]);
    if (!realBrowser) {
      realKey = key;
      launched++;
      realBrowser = launchEngine(pw, engine, options);
    } else if (key !== realKey) {
      // Different launch options — headed next to headless — cannot share a process. Nothing in a
      // suite does this today, and if something ever does it gets its own browser rather than
      // silently the wrong kind.
      launched++;
      return wrap(await launchEngine(pw, engine, options), true);
    }
    return wrap(await realBrowser, false);
  };

  return {
    /** Drop-in for lib/test.mjs's `loadPlaywright(log, yes, engine)`. */
    loadBrowser: async (log, yes, engine = DEFAULT_ENGINE) => {
      loads++;
      // Loaded ONCE for the whole suite. Under `npx` this can install Playwright and download the
      // browser; eight workers racing that would be eight downloads into one directory.
      if (!loading) loading = Promise.resolve(load(log, yes, engine));
      const r = await loading;
      if (!r || !r.pw) return r || { pw: null, problem: "The browser could not be loaded." };
      // Keyed by the engine asked for, so runOnce's `launchEngine(pw, engine, …)` finds it exactly
      // where it looks. A stub that only ever exposed `chromium` made `--browser webkit --workers 4`
      // fail with "this Playwright build does not expose a WebKit browser".
      return { pw: { [engine]: { launch: (options) => lease(r.pw, engine, options) } }, problem: "" };
    },
    close: async () => {
      if (!realBrowser) return;
      const b = await realBrowser.catch(() => null);
      realBrowser = null;
      await b?.close().catch(() => {});
    },
    /** For the test that proves N workers is still ONE Chromium. */
    get launched() {
      return launched;
    },
    get loads() {
      return loads;
    },
  };
}

// ---- the login gate ------------------------------------------------------------------------------

/**
 * Does this run have to serialise its first test?
 *
 * ONLY when a sign-in would actually happen: `--login` with no saved session yet. `--auth-file`
 * loads a session somebody else made and never signs in, and a `--login` whose session file is
 * already on disk reuses it — neither races, so neither pays for the gate.
 *
 * Every failure to answer the question answers "yes". Gating when we did not need to costs the
 * first test's wall-clock; not gating when we did costs N real sign-ins against somebody's
 * production login form.
 */
export function needsLoginGate({ login = "", authDir = DEFAULT_AUTH_DIR, env = process.env, exists = (p) => existsSync(p) } = {}) {
  if (!String(login || "").trim()) return false;
  try {
    const { secrets } = prepareLogin(login, { env });
    return !exists(authFileFor(authDir, login, secrets));
  } catch {
    return true;
  }
}

// ---- what lib/suite.mjs uses ---------------------------------------------------------------------

/**
 * The whole feature behind one call: a `map` that replaces the serial loop, a `loadBrowser` to pass
 * through to each test, and a `close`.
 *
 * `workers: 1` returns a pool that is a for-loop with the caller's own unbuffered log and NO
 * shared browser — the same code path, the same output, the same Chromium-per-test as before this
 * file existed. That is the escape hatch, and it has to be the real thing rather than a parallel
 * run that happens to have one worker.
 */
export function openPool({
  workers = 1,
  login = "",
  authDir = DEFAULT_AUTH_DIR,
  env = process.env,
  log = console.log,
  loadPlaywright = null,
  exists = (p) => existsSync(p),
} = {}) {
  const n = Math.max(1, Math.floor(workers) || 1);
  const serial = n === 1;
  const browsers = serial || !loadPlaywright ? null : sharedBrowser({ load: loadPlaywright });
  const gateFirst = serial ? false : needsLoginGate({ login, authDir, env, exists });

  return {
    workers: n,
    gateFirst,
    // undefined, not a wrapper, when serial: lib/test.mjs's own default parameter then applies and
    // the serial path never touches a line of this file.
    loadBrowser: browsers ? browsers.loadBrowser : undefined,
    browsers,
    map: (items, fn) => {
      // MATERIALISED ONCE, and mapOrdered is handed the array rather than the original. Counting
      // the total with `Array.from(items)` and then passing `items` on drained a one-shot iterable
      // between the two: a generator of tests ran ZERO of them and came back `[]` — a green suite,
      // exit 0, over nothing at all.
      const list = Array.from(items);
      const total = list.length;
      let done = 0;
      return mapOrdered(list, (item, i) => {
        if (serial) return fn(item, i, log);
        const b = buffered(log);
        // ONE synchronous callback, so the block and the count that follows it can never be split
        // by another worker's block. In `finally`, so a block that took twenty seconds to produce
        // still reaches the reader when the work that produced it threw.
        //
        // The async wrapper is what makes that true of a SYNCHRONOUS throw too. `Promise.resolve(
        // fn(…))` evaluates fn first, so a synchronous throw escaped before `.finally` was ever
        // attached: the verdict was still `errored`, but that test's whole buffered block went on
        // the floor and the running count stayed one short of the verdicts for the rest of the run.
        return (async () => fn(item, i, b.log))().finally(() => {
          b.flush();
          done++;
          log(DIM(`${done}/${total} done`));
        });
      }, { workers: n, gateFirst });
    },
    close: async () => {
      await browsers?.close();
    },
  };
}

