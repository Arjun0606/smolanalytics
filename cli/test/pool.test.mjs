// PARALLEL SUITE EXECUTION — the tests that have to be able to fail.
//
// Running fifty tests at once is only worth having if the report it produces is the report a serial
// run produced. Every requirement below is stated as the requirement, then given data that would
// break it if the code stopped honouring it:
//
//   ORDER          a suite whose tests finish in EXACTLY REVERSED order, asserted to have finished
//                  reversed, so a pool that returned completion order could not pass by accident.
//   REPORT PARITY  the same mixed suite (passed/failed/stale/errored/flaky) run serially and in
//                  parallel — same results, same summary, same comment, same exit code.
//   ISOLATION      a worker that throws is errored for its own test and costs nobody else theirs.
//   READABILITY    two tests writing at the same time come out as two contiguous blocks.
//   ONE LOGIN      a REAL http server counting REAL sign-in POSTs, driven through a real Chromium,
//                  six tests, four workers, one login. Counted at the server, never at the model:
//                  a scripted model would happily say whatever the assertion wanted.
//   ONE BROWSER    real Playwright, real contexts, N leases, one launch — and contexts that are
//                  still isolated from each other afterwards.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { mkdtempSync, existsSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  parseWorkers, defaultWorkers, mapOrdered, buffered, erroredResult, needsLoginGate, sharedBrowser, openPool,
} from "../lib/pool.mjs";
import { runSuite, suiteCmd, summarize, exitCode, commentBody } from "../lib/suite.mjs";
import { EMAIL_VAR, PASSWORD_VAR, authFileFor, DEFAULT_AUTH_DIR } from "../lib/auth.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const here = path.dirname(fileURLToPath(import.meta.url));
const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-pool-"));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- how many workers ----------------------------------------------------------------------------

describe("--workers is refused rather than defaulted", () => {
  test("a whole number is taken as given", () => {
    assert.deepEqual(parseWorkers("6"), { workers: 6, problem: "" });
    assert.deepEqual(parseWorkers("1"), { workers: 1, problem: "" });
  });

  test("a typo is an error, never a silent default", () => {
    // The --retries shape. Someone who typed a number wants that number; quietly substituting ours
    // is how a person ends up debugging a machine they did not configure.
    for (const bad of ["eight", "", "4.5", "-2", "0", "4x"]) {
      const r = parseWorkers(bad);
      assert.equal(r.workers, 0, `${JSON.stringify(bad)} was accepted`);
      assert.match(r.problem, /--workers needs a whole number/, `${JSON.stringify(bad)} got no explanation`);
    }
  });

  test("no --workers at all is the measured default, not an error", () => {
    const r = parseWorkers(undefined, { cpus: 8, mem: 16 * 1024 ** 3, hasKey: false });
    assert.equal(r.problem, "");
    assert.ok(r.workers >= 1);
  });
});

describe("the default is measured, and every ceiling actually binds", () => {
  test("a 2-core CI runner gets 2, not 1", () => {
    // The machine that needs this most is the one `cpus - 1` would hand a speedup of zero. This
    // workload spends 43% of one core across a 39s serial run: it is waiting on navigation, not
    // computing, so two lanes on two cores is real.
    assert.equal(defaultWorkers({ cpus: 2, mem: 7 * 1024 ** 3, hasKey: false }), 2);
    assert.equal(defaultWorkers({ cpus: 1, mem: 7 * 1024 ** 3, hasKey: false }), 2);
  });

  test("cores bind below the cap", () => {
    assert.equal(defaultWorkers({ cpus: 4, mem: 32 * 1024 ** 3, hasKey: false }), 3, "cpus - 1, leaving a core for the app under test");
  });

  test("a key lowers the cap, because the model is the ceiling a replay cannot see", () => {
    const big = { cpus: 32, mem: 64 * 1024 ** 3 };
    assert.equal(defaultWorkers({ ...big, hasKey: true }), 4);
    assert.equal(defaultWorkers({ ...big, hasKey: false }), 8);
    assert.ok(defaultWorkers({ ...big, hasKey: true }) < defaultWorkers({ ...big, hasKey: false }),
      "with a key every test can wake the agent, and N concurrent agent chains is N times the request rate");
  });

  test("a small container is bound by memory, not by its core count", () => {
    // ~100MB per context measured, plus a 128MB browser base. A 1GB box with 8 cores must not be
    // told to open seven contexts.
    assert.equal(defaultWorkers({ cpus: 8, mem: 1024 ** 3, hasKey: false }), 2);
    assert.ok(defaultWorkers({ cpus: 8, mem: 512 * 1024 ** 2, hasKey: false }) < defaultWorkers({ cpus: 8, mem: 8 * 1024 ** 3, hasKey: false }));
  });

  test("it is never zero, whatever it is handed", () => {
    for (const args of [{ cpus: 0, mem: 0 }, { cpus: NaN, mem: NaN }, { cpus: -4, mem: -1 }]) {
      assert.ok(defaultWorkers({ ...args, hasKey: false }) >= 1, JSON.stringify(args));
    }
  });
});

// ---- ordering ------------------------------------------------------------------------------------

describe("results come back in the SUITE's order, never in the order they finished", () => {
  test("nine tests that finish in exactly reversed order still report in suite order", async () => {
    // The data is deliberately not symmetric: item i sleeps (9 - i) * 12ms, so with enough workers
    // the LAST item finishes first and the first finishes last. A pool that pushed results as they
    // arrived would return the exact reverse of the answer this asserts.
    const items = ["a", "b", "c", "d", "e", "f", "g", "h", "i"];
    const finished = [];
    const out = await mapOrdered(items, async (item, i) => {
      await sleep((items.length - i) * 12);
      finished.push(item);
      return `${item}${i}`;
    }, { workers: 9 });

    assert.deepEqual(finished, [...items].reverse(), "the fixture did not actually finish out of order, so this test could not have failed");
    assert.notDeepEqual(finished, items, "completion order and input order must differ or this proves nothing");
    assert.deepEqual(out, ["a0", "b1", "c2", "d3", "e4", "f5", "g6", "h7", "i8"]);
  });

  test("the index a worker is given is the suite's index, not the order it was picked up", async () => {
    // runSuite derives each test's signup identity from this index (`SMOLANALYTICS_RUN_ID-<i+1>`).
    // Handing indices out by arrival would give two tests the same identity on a re-run and read as
    // "email already exists" — the app's fault, apparently.
    const seen = new Map();
    await mapOrdered([10, 20, 30, 40, 50, 60], async (v, i) => {
      await sleep(v === 10 ? 40 : 1);
      seen.set(v, i);
    }, { workers: 6 });
    assert.deepEqual([...seen.entries()].sort((a, b) => a[0] - b[0]), [[10, 0], [20, 1], [30, 2], [40, 3], [50, 4], [60, 5]]);
  });

  test("workers never exceeds the number in flight, and never exceeds the number of tests", async () => {
    let live = 0;
    let peak = 0;
    await mapOrdered(Array.from({ length: 20 }, (_, i) => i), async () => {
      live++;
      peak = Math.max(peak, live);
      await sleep(5);
      live--;
    }, { workers: 3 });
    assert.equal(peak, 3, `three lanes were asked for and ${peak} ran`);

    let peak2 = 0;
    let live2 = 0;
    await mapOrdered([1, 2], async () => {
      live2++;
      peak2 = Math.max(peak2, live2);
      await sleep(5);
      live2--;
    }, { workers: 16 });
    assert.equal(peak2, 2, "sixteen lanes over two tests must not open sixteen browsers' worth of anything");
  });

  test("workers 1 is the serial loop: strictly one at a time, in order", async () => {
    const order = [];
    let live = 0;
    let peak = 0;
    await mapOrdered([1, 2, 3, 4], async (v) => {
      live++;
      peak = Math.max(peak, live);
      await sleep(v === 1 ? 20 : 1);
      order.push(v);
      live--;
    }, { workers: 1 });
    assert.equal(peak, 1, "--workers 1 must be the old loop, not a pool that happens to have one lane");
    assert.deepEqual(order, [1, 2, 3, 4]);
  });

  test("an empty suite is an empty array, not a hang", async () => {
    assert.deepEqual(await mapOrdered([], async () => 1, { workers: 4 }), []);
  });
});

// ---- failure isolation ---------------------------------------------------------------------------

describe("one worker dying costs exactly one verdict", () => {
  test("the thrower is errored; every other test keeps the verdict it earned", async () => {
    const out = await mapOrdered([1, 2, 3, 4, 5], async (v) => {
      if (v === 3) throw new Error("the browser vanished");
      await sleep(v * 3);
      return { id: v, status: "passed" };
    }, { workers: 5, onError: (item, e) => erroredResult({ id: item }, e) });

    assert.deepEqual(out.map((r) => r.status), ["passed", "passed", "errored", "passed", "passed"]);
    assert.match(out[2].reason, /the browser vanished/);
    assert.match(out[2].reason, /This is the test runner, not your application/,
      "our crash must never be worded as a claim about the app");
  });

  test("every test throwing is five errored results, not one rejection", async () => {
    const out = await mapOrdered([1, 2, 3, 4, 5], async () => {
      throw new Error("nope");
    }, { workers: 3 });
    assert.equal(out.length, 5);
    assert.ok(out.every((r) => r.status === "errored"));
  });

  test("an errored record carries every field the summary and the comment read", () => {
    const r = erroredResult({ name: "n", file: "f.md", id: "n", planPath: "p.json" }, new Error("boom"));
    // exitCode(), summarize() and commentBody() all index into these. A missing one is a crash in
    // the reporting of a crash.
    for (const k of ["name", "file", "status", "mode", "reason", "ms", "refreshed", "layout", "suspects"]) {
      assert.ok(k in r, `${k} is missing`);
    }
    assert.equal(r.status, "errored");
    assert.equal(exitCode([r]), 2, "our own crash is 2 — 1 is reserved for the application being broken");
  });
});

// ---- readable output -----------------------------------------------------------------------------

describe("eight workers, one readable transcript", () => {
  test("each test's lines arrive as ONE contiguous block, never braided", async () => {
    const out = [];
    const sink = (...a) => out.push(a.join(" "));
    const a = buffered(sink);
    const b = buffered(sink);
    // Written in the order two real workers would write them: alternating.
    a.log("A: opening");
    b.log("B: opening");
    a.log("A: PASS");
    b.log("B: FAIL");
    assert.deepEqual(out, [], "buffered lines must not reach the terminal early");
    b.flush();
    a.flush();
    assert.deepEqual(out, ["B: opening", "B: FAIL", "A: opening", "A: PASS"],
      "the two tests braided into each other, which is the thing this exists to prevent");
  });

  test("a line's arguments survive exactly as they were given", () => {
    // lib/auth.mjs logs `(...parts)` and lets the sink decide how to join them. A buffer that
    // joined them itself would silently reword what a sign-in prints.
    const out = [];
    const b = buffered((...a) => out.push(a));
    b.log("one", "two", 3);
    b.log();
    b.flush();
    assert.deepEqual(out, [["one", "two", 3], []]);
  });

  test("a running count is printed, and it sits with the block it belongs to", async () => {
    const lines = [];
    const pool = openPool({ workers: 4, log: (s) => lines.push(String(s).replace(/\x1b\[[0-9;]*m/g, "")) });
    await pool.map([5, 4, 3, 2, 1], async (v, i, tlog) => {
      await sleep(v * 8);
      tlog(`test ${v} PASS`);
      return v;
    });
    // The requirement is not WHICH test finishes first — with four lanes that is the scheduler's
    // business. It is that every block is followed immediately by its own count: a run of four
    // blocks and then four counters reads as if the tool lost track of where it was.
    assert.equal(lines.length, 10, lines.join(" | "));
    for (let i = 0; i < 10; i += 2) {
      assert.match(lines[i], /^test \d PASS$/, `line ${i} is not a test's block: ${lines.join(" | ")}`);
      assert.equal(lines[i + 1], `${i / 2 + 1}/5 done`, `the count drifted away from its block: ${lines.join(" | ")}`);
    }
    // And every test really did report, exactly once.
    assert.deepEqual(lines.filter((_, i) => i % 2 === 0).sort(), ["test 1 PASS", "test 2 PASS", "test 3 PASS", "test 4 PASS", "test 5 PASS"]);
  });

  test("workers 1 prints no progress line and buffers nothing", async () => {
    // The serial escape hatch has to be the old output. A block held until a twenty-second test
    // finishes is not "the same, just ordered" — it is twenty seconds of a blank terminal.
    const lines = [];
    const pool = openPool({ workers: 1, log: (s) => lines.push(String(s)) });
    await pool.map([1, 2], async (v, i, tlog) => {
      tlog(`start ${v}`);
      await sleep(1);
      tlog(`end ${v}`);
    });
    assert.deepEqual(lines, ["start 1", "end 1", "start 2", "end 2"]);
    assert.equal(pool.loadBrowser, undefined, "serial must not go anywhere near the shared browser");
  });

  test("a block still reaches the reader when the test that produced it threw", async () => {
    const lines = [];
    const pool = openPool({ workers: 2, log: (s) => lines.push(String(s)) });
    const out = await pool.map([1], async (v, i, tlog) => {
      tlog("twenty seconds of evidence");
      throw new Error("and then it died");
    });
    assert.ok(lines.includes("twenty seconds of evidence"), "the buffer was dropped on the floor with the exception");
    assert.equal(out[0].status, "errored");
  });
});

// ---- the login gate ------------------------------------------------------------------------------

describe("one login for the whole suite, however many workers", () => {
  const login = 'sign in with {{email}} and {{password}}';
  const env = { [EMAIL_VAR]: "user@example.com", [PASSWORD_VAR]: "hunter2" };

  test("no --login means no gate: nothing signs in, so nothing can race", () => {
    assert.equal(needsLoginGate({ login: "", env }), false);
  });

  test("--login with no saved session gates: the first test signs in alone", () => {
    assert.equal(needsLoginGate({ login, authDir: scratch(), env }), true);
  });

  test("--login with the session already on disk does not gate", () => {
    const dir = scratch();
    const file = authFileFor(dir, login, [{ value: "hunter2", token: "x" }]);
    mkdirSync(path.dirname(file), { recursive: true });
    writeFileSync(file, "{}");
    assert.equal(needsLoginGate({ login, authDir: dir, env }), false, "a session that already exists is reused, so no worker can sign in");
  });

  test("when the question cannot be answered, it gates", () => {
    // Gating when we did not need to costs one test's wall clock. Not gating when we did costs N
    // real sign-ins against somebody's production login form.
    assert.equal(needsLoginGate({ login, env, exists: () => { throw new Error("no such fs"); } }), true);
  });

  test("the gate really does hold every other worker until the first test is done", async () => {
    const events = [];
    await mapOrdered([0, 1, 2, 3, 4, 5], async (v) => {
      events.push(`start${v}`);
      await sleep(v === 0 ? 40 : 2);
      events.push(`end${v}`);
    }, { workers: 6, gateFirst: true });
    assert.equal(events[0], "start0");
    assert.equal(events[1], "end0", `something else started before the sign-in finished: ${events.join(",")}`);
  });

  test("without the gate they all start at once — which is what the gate is preventing", async () => {
    // The control. If this passed with gateFirst too, the test above would prove nothing.
    const events = [];
    await mapOrdered([0, 1, 2, 3, 4, 5], async (v) => {
      events.push(`start${v}`);
      await sleep(v === 0 ? 40 : 2);
      events.push(`end${v}`);
    }, { workers: 6, gateFirst: false });
    assert.notEqual(events[1], "end0", "the ungated pool serialised anyway, so the gate test is measuring nothing");
  });

  test("openPool turns the gate on for a --login run and off otherwise", () => {
    assert.equal(openPool({ workers: 4, login, authDir: scratch(), env }).gateFirst, true);
    assert.equal(openPool({ workers: 4, login: "", env }).gateFirst, false);
    assert.equal(openPool({ workers: 1, login, authDir: scratch(), env }).gateFirst, false, "serial has no race to prevent");
  });
});

// ---- one browser, N contexts ---------------------------------------------------------------------

describe("N workers share ONE Chromium", noBrowser, () => {
  test("eight concurrent leases launch one browser, and closing a lease leaves it alive", async () => {
    // `launches` counts REAL calls into Playwright's own launch, not a number the pool reports
    // about itself.
    let launches = 0;
    const pw = { chromium: { launch: (o) => { launches++; return chromium.launch(o); } } };
    const shared = sharedBrowser({ load: async () => ({ pw }) });

    try {
    const leases = await Promise.all(Array.from({ length: 8 }, async () => {
      const { pw: fake, problem } = await shared.loadBrowser(() => {}, true, "chromium");
      assert.equal(problem, "");
      return fake.chromium.launch({ headless: true });
    }));

    assert.equal(launches, 1, `eight workers launched ${launches} Chromiums`);
    assert.equal(shared.loads, 8, "every worker still asked, which is what makes the count meaningful");

    const pages = await Promise.all(leases.map((b) => b.newPage({ viewport: { width: 800, height: 600 } })));
    assert.equal(pages.length, 8);
    // Eight leases, eight live contexts, one process.
    assert.equal(leases[0].contexts().length, 8);

    await leases[0].close();
    assert.equal(leases[0].contexts().length, 7, "a lease must close its own context and only its own");
    assert.equal(leases[1].isConnected(), true, "one test finishing must not close the browser the other seven are using");
    // The other leases still work after their neighbour closed.
    await pages[1].goto("about:blank");

    await shared.close();
    assert.equal(leases[1].isConnected(), false, "the pool owns the browser's life and must actually end it");
    } finally {
      await shared.close();
    }
  });

  test("a lease's contexts are as isolated as separate browsers were", async () => {
    // The isolation boundary is the CONTEXT, not the process — that is the whole reason sharing is
    // safe. If it were not, two tests would see each other's cookies and a logged-out test would
    // pass because a neighbour had logged in.
    // A REAL origin, because about: has no storage and no cookies — a leak test on about:blank
    // cannot detect a leak.
    const server = createServer((req, res) => {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8", "set-cookie": "seen=yes; Path=/" });
      res.end("<!doctype html><title>Isolation</title><h1>Isolation</h1>");
    });
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const origin = `http://127.0.0.1:${server.address().port}/`;
    const shared = sharedBrowser({ load: async () => ({ pw: { chromium } }) });
    try {
      const { pw } = await shared.loadBrowser(() => {}, true, "chromium");
      const a = await pw.chromium.launch({ headless: true });
      const b = await pw.chromium.launch({ headless: true });
      const pa = await a.newPage({ viewport: { width: 800, height: 600 } });
      const pb = await b.newPage({ viewport: { width: 800, height: 600 } });
      await pa.goto(origin);
      await pa.evaluate(() => localStorage.setItem("who", "test-a"));
      await pa.evaluate(() => (document.cookie = "lane=a; path=/"));
      // Opened AFTER the neighbour wrote, on the same origin, in the same process.
      await pb.goto(origin);
      assert.equal(await pa.evaluate(() => localStorage.getItem("who")), "test-a", "the fixture never wrote anything, so a leak could not be seen");
      assert.equal(await pb.evaluate(() => localStorage.getItem("who")), null, "one test's storage leaked into another's");
      assert.ok(!(await pb.evaluate(() => document.cookie)).includes("lane=a"), "one test's cookie leaked into another's");
    } finally {
      await shared.close();
      server.closeAllConnections();
      await new Promise((r) => server.close(() => r()));
    }
  });

  test("a shared browser that dies takes the run down as ERRORED, never as a failure of the app", async () => {
    // THE TRADE-OFF OF SHARING, stated out loud and pinned. One process behind N workers means one
    // crash can end N tests. What must never happen is those tests being reported as `failed`: that
    // is a bug report about somebody's application, filed because OUR browser died.
    const shared = sharedBrowser({ load: async () => ({ pw: { chromium } }) });
    const { pw } = await shared.loadBrowser(() => {}, true, "chromium");
    const lease = await pw.chromium.launch({ headless: true });
    await shared.close();
    let thrown = null;
    try {
      await lease.newPage({ viewport: { width: 800, height: 600 } });
    } catch (e) {
      thrown = e;
    }
    assert.ok(thrown, "a lease on a dead browser must not silently hand back a page");
    const r = erroredResult({ name: "t", id: "t" }, thrown);
    assert.equal(r.status, "errored");
    assert.notEqual(r.status, "failed");
    assert.match(r.reason, /This is the test runner, not your application/);
  });

  test("a browser that could not be loaded is passed through untouched, not wrapped", async () => {
    // runOnce reads `problem` and reports it as errored with that exact sentence. A pool that
    // swallowed it would produce "The browser could not be started" over a real explanation.
    const shared = sharedBrowser({ load: async () => ({ pw: null, problem: "Playwright installed but Chromium did not." }) });
    const r = await shared.loadBrowser(() => {}, false);
    assert.equal(r.pw, null);
    assert.match(r.problem, /Chromium did not/);
  });

  test("Playwright is loaded once however many workers ask for it", async () => {
    // Under npx this call can install Playwright and download Chromium. Eight of them racing into
    // one directory is a broken install, not a slow one.
    let loads = 0;
    const shared = sharedBrowser({ load: async () => { loads++; await sleep(10); return { pw: { chromium } }; } });
    await Promise.all(Array.from({ length: 6 }, () => shared.loadBrowser(() => {}, true, "chromium")));
    assert.equal(loads, 1);
    await shared.close();
  });
});

// ---- the report is identical ---------------------------------------------------------------------

/**
 * A suite of nine tests with all five statuses in it, whose durations make it finish in an order
 * nothing like its own. `runTest` is injected, so this is the real runSuite: the real result
 * building, the real reasons, the real suspects hook, the real summary.
 */
function mixedSuite() {
  const spec = [
    { id: "one", status: "passed", ms: 45 },
    { id: "two", status: "failed", ms: 5, reason: "On /checkout, the Pay button did nothing." },
    { id: "three", status: "passed", ms: 40 },
    { id: "four", status: "stale", ms: 8, reason: "The recording no longer fits: Pay was not found." },
    { id: "five", status: "flaky", ms: 30, reason: "Failed once, passed on retry." },
    { id: "six", status: "passed", ms: 12 },
    { id: "seven", status: "errored", ms: 3, reason: "The browser could not be started." },
    { id: "eight", status: "passed", ms: 25 },
    { id: "nine", status: "failed", ms: 1, reason: "On /, the price was missing." },
  ];
  const tests = spec.map((s, i) => ({
    file: `tests/${s.id}.md`,
    name: `${s.id} works`,
    test: `${s.id} works`,
    id: s.id,
    planPath: `recordings/${s.id}.json`,
  }));
  const finished = [];
  const runTest = async ({ test: sentence, onRun }) => {
    const s = spec.find((x) => `${x.id} works` === sentence);
    await sleep(s.ms);
    finished.push(s.id);
    onRun({ status: s.status, mode: s.status === "stale" ? "replay" : "agent", reason: s.reason || `${s.id} was checked.` });
    return s.status === "failed" ? 1 : 0;
  };
  return { spec, tests, runTest, finished };
}

const runMixed = async (workers) => {
  const f = mixedSuite();
  const lines = [];
  const results = await runSuite({
    tests: f.tests,
    url: "http://127.0.0.1:1/",
    plansDir: "recordings",
    workers,
    runTest: f.runTest,
    mkdir: () => {},
    hasPlan: () => true,
    hasKey: true,
    findSuspects: () => [],
    log: (...a) => lines.push(a.join(" ")),
    env: {},
  });
  return { results, lines, finished: f.finished };
};

describe("a parallel run reports exactly what a serial run reports", () => {
  test("same order, same statuses, same reasons — from a suite that finished out of order", async () => {
    const serial = await runMixed(1);
    const parallel = await runMixed(6);

    // The fixture has to actually finish out of order or this whole describe is theatre.
    assert.deepEqual(serial.finished, ["one", "two", "three", "four", "five", "six", "seven", "eight", "nine"]);
    assert.notDeepEqual(parallel.finished, serial.finished, `the parallel run finished in suite order (${parallel.finished.join(",")}), so nothing here could have failed`);

    const shape = (rs) => rs.map((r) => [r.id, r.name, r.file, r.status, r.mode, r.reason]);
    assert.deepEqual(shape(parallel.results), shape(serial.results));
  });

  test("same counts, and the count of every one of the five statuses", async () => {
    const a = summarize((await runMixed(1)).results);
    const b = summarize((await runMixed(6)).results);
    for (const k of ["total", "passed", "failed", "stale", "errored", "flaky", "replayed"]) {
      assert.equal(b[k], a[k], `${k} differs: serial ${a[k]}, parallel ${b[k]}`);
    }
    assert.deepEqual({ p: a.passed, f: a.failed, s: a.stale, e: a.errored, x: a.flaky }, { p: 4, f: 2, s: 1, e: 1, x: 1 },
      "the fixture stopped containing all five statuses, so parity across them is no longer being checked");
  });

  test("the same exit code", async () => {
    const a = exitCode((await runMixed(1)).results);
    const b = exitCode((await runMixed(6)).results);
    assert.equal(b, a);
    assert.equal(b, 1, "the fixture has a failure in it; if this stops being 1 the parity above is comparing two greens");
  });

  test("the same pull request comment, byte for byte", async () => {
    const opts = { url: "https://preview.example.com", suite: "tests", runUrl: "" };
    // Durations are pinned before the comparison, and ONLY durations. Two SERIAL runs of one suite
    // already differ in those cells — measured on a real 12-test suite: identical once the times
    // are masked, different before. Comparing them would be comparing a stopwatch to itself, and
    // would hide the thing this test is for: the ORDER and the CONTENT of the rows.
    const pin = (rs) => rs.map((r) => ({ ...r, ms: 1234 }));
    const a = commentBody(pin((await runMixed(1)).results), opts);
    const b = commentBody(pin((await runMixed(6)).results), opts);
    assert.equal(b, a, "the comment on a pull request must not depend on which worker finished first");
    assert.match(a, /the Pay button did nothing/, "the fixture's failure is missing from the comment, so this is comparing two empty strings");
    // The rows in the order the comment puts them, from a run that finished in a different order.
    const rows = (c) => c.split("\n").filter((l) => /^\| (pass|\*\*fail\*\*|stale|error|flaky) \|/.test(l));
    assert.equal(rows(a).length, 9, "the comment stopped listing every test, so row order is no longer being compared");
    assert.deepEqual(rows(b), rows(a));
  });

  test("the summary line is the same, and every test is still named under its own status", async () => {
    const clean = (ls) => ls.map((l) => l.replace(/\x1b\[[0-9;]*m/g, ""));
    const a = clean((await runMixed(1)).lines).filter((l) => l.trim());
    const b = clean((await runMixed(6)).lines).filter((l) => /^\n?\s*\w/.test(l));
    for (const id of ["one", "two", "three", "four", "five", "six", "seven", "eight", "nine"]) {
      assert.ok(b.some((l) => l.includes(`${id} works`)), `${id} is missing from the parallel transcript`);
      assert.ok(a.some((l) => l.includes(`${id} works`)), `${id} is missing from the serial transcript`);
    }
  });

  test("a runner that throws mid-suite still leaves the other eight verdicts standing", async () => {
    const f = mixedSuite();
    const results = await runSuite({
      tests: f.tests,
      url: "http://127.0.0.1:1/",
      plansDir: "recordings",
      workers: 6,
      runTest: async (o) => {
        if (o.test.startsWith("five")) throw new Error("the page crashed");
        return f.runTest(o);
      },
      mkdir: () => {},
      hasPlan: () => true,
      hasKey: true,
      findSuspects: () => [],
      log: () => {},
      env: {},
    });
    assert.equal(results.length, 9);
    assert.equal(results[4].status, "errored");
    assert.match(results[4].reason, /the page crashed/);
    // Everything else is untouched, in place, with its own verdict.
    assert.deepEqual(results.map((r) => r.status),
      ["passed", "failed", "passed", "stale", "errored", "passed", "errored", "passed", "failed"]);
  });
});

// ---- one login, counted at a real server ---------------------------------------------------------

describe("six tests, four workers, one real sign-in", noBrowser, () => {
  test("the login form is POSTed exactly once, and every test still runs signed in", async () => {
    // COUNTED AT THE SERVER. The model is scripted here, and a scripted model will answer in
    // whatever shape an assertion wants — so nothing about the number of logins is taken from it.
    // The number is how many times a real Chromium submitted a real form to a real http server.
    let logins = 0;
    const page = (title, body) => `<!doctype html><title>${title}</title>${body}`;
    const server = createServer((req, res) => {
      const signedIn = /(^|;\s*)sid=ok/.test(req.headers.cookie || "");
      if (req.method === "POST" && req.url === "/login") {
        logins++;
        let b = "";
        req.on("data", (c) => (b += c));
        req.on("end", () => {
          res.writeHead(302, { "set-cookie": "sid=ok; Path=/", location: "/app" });
          res.end();
        });
        return;
      }
      if (!signedIn) {
        res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
        res.end(page("Sign in", `<h1>Sign in</h1><form method="POST" action="/login">
          <label>Email <input name="email" type="email"></label>
          <label>Password <input name="password" type="password"></label>
          <button type="submit">Sign in</button></form>`));
        return;
      }
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(page("Dashboard", `<h1>Your dashboard</h1><p>Signed in. Everything is fine.</p>`));
    });
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const url = `http://127.0.0.1:${server.address().port}/`;

    const realFetch = globalThis.fetch;
    const key = process.env.ANTHROPIC_API_KEY;
    process.env.ANTHROPIC_API_KEY = "sk-ant-test";
    // Read from the environment by lib/auth.mjs and never from a flag or a file — which is exactly
    // how a real CI run supplies them.
    const savedCreds = [process.env[EMAIL_VAR], process.env[PASSWORD_VAR]];
    process.env[EMAIL_VAR] = "user@example.com";
    process.env[PASSWORD_VAR] = "hunter2";
    const authDir = path.join(scratch(), "auth");
    const plansDir = path.join(scratch(), "recordings");
    mkdirSync(plansDir, { recursive: true });
    let modelCalls = 0;
    const transcript = [];

    // The scripted model. For the login it fills the two fields and submits; for a test it looks at
    // the page it was given and says whether the sentence held. It never reports anything about how
    // many logins happened — the server does.
    globalThis.fetch = async (target, init = {}) => {
      if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
      assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called here");
      modelCalls++;
      const body = JSON.parse(init.body);
      const conversation = JSON.stringify(body.messages);
      // The login sentence is the one that says "sign in with". A test's sentence never does, and
      // the signed-in page says "Signed in", not "sign in with".
      const isLogin = /sign in with/.test(conversation);
      const turn = body.messages.length;
      if (isLogin) {
        if (turn === 1) return json([fill(ref(conversation, "textbox", "Email"), "user@example.com", "the email field")]);
        if (turn === 3) return json([fill(ref(conversation, "textbox", "Password"), "hunter2", "the password field")]);
        if (turn === 5) return json([{ type: "tool_use", id: "c", name: "click", input: { ref: ref(conversation, "button", "Sign in"), why: "submit the form" } }]);
        const landed = /Your dashboard/.test(conversation);
        return json([done(landed, landed ? "The dashboard is showing." : "Still on the sign-in page.", landed ? "Your dashboard" : "")]);
      }
      // A plain test. It can only see the dashboard if the session it was handed was real.
      const sees = /Your dashboard/.test(conversation);
      return json([done(sees, sees ? "The dashboard is showing." : "The app asked me to sign in again.", sees ? "Your dashboard" : "")]);
    };

    try {
      const tests = Array.from({ length: 6 }, (_, i) => ({
        file: `tests/t${i + 1}.md`,
        name: `dashboard ${i + 1}`,
        test: `the dashboard is showing, check ${i + 1}`,
        id: `t${i + 1}`,
        planPath: path.join(plansDir, `t${i + 1}.json`),
      }));
      const results = await runSuite({
        tests,
        url,
        plansDir,
        workers: 4,
        yes: true,
        retries: 0,
        maxSteps: 8,
        login: "sign in with {{email}} and {{password}}",
        authDir,
        renderCheck: false,
        hasKey: true,
        log: (...a) => transcript.push(a.join(" ").replace(/\x1b\[[0-9;]*m/g, "")),
        env: { [EMAIL_VAR]: "user@example.com", [PASSWORD_VAR]: "hunter2" },
      });
      if (process.env.POOL_DEBUG) console.error(transcript.join("\n"));

      assert.equal(logins, 1, `six tests across four workers signed in ${logins} times`);
      assert.ok(existsSync(authFileFor(authDir, "sign in with {{email}} and {{password}}", [{ value: "hunter2", token: "x" }])),
        "the saved session was never written, so the count above is measuring the wrong thing");
      assert.deepEqual(results.map((r) => r.status), Array(6).fill("passed"),
        `every test must run signed in: ${results.map((r) => `${r.id}=${r.status} ${r.reason}`).join(" | ")}`);
      assert.ok(modelCalls > 6, "the agent really drove this, rather than everything short-circuiting");
    } finally {
      globalThis.fetch = realFetch;
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
      for (const [name, was] of [[EMAIL_VAR, savedCreds[0]], [PASSWORD_VAR, savedCreds[1]]]) {
        if (was === undefined) delete process.env[name];
        else process.env[name] = was;
      }
      // closeAllConnections BEFORE close, always: a keep-alive socket from Chromium wedges this
      // file forever otherwise.
      server.closeAllConnections();
      await new Promise((r) => server.close(() => r()));
    }
  });
});

const json = (content) => ({ ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content }), text: async () => "" });
const fill = (r, text, why) => ({ type: "tool_use", id: "f", name: "fill", input: { ref: r, text, why } });
const done = (passed, why, proof) => ({ type: "tool_use", id: "d", name: "finish", input: { passed, why, proof } });

/**
 * The ref the perception layer gave one control, read out of the conversation the model was sent.
 *
 * Throws rather than guessing. A fallback of "e1" made the scripted agent type the email into a
 * heading, the sign-in silently do nothing, and the login count read 0 for a reason that had
 * nothing to do with the pool.
 */
function ref(conversation, role, name) {
  const m = new RegExp(`(e\\d+) ${role} \\\\"${name}\\\\"`).exec(conversation);
  if (!m) throw new Error(`no ${role} "${name}" in the snapshot the model was given`);
  return m[1];
}

// ---- the flag actually reaches the runner ----------------------------------------------------------

describe("--workers on the command line", () => {
  const cli = (args) => spawnSync(process.execPath, [path.join(here, "..", "bin", "smolanalytics.mjs"), "test", ...args], {
    encoding: "utf8",
    env: { ...process.env, ANTHROPIC_API_KEY: "" },
  });

  test("a typo'd count stops the run at exit 2, before a browser opens", () => {
    // Exit 2, never 1: 1 is reserved for the application being broken, and a flag we could not read
    // says nothing about the application.
    const r = cli(["--suite", path.join(here, "fixtures-pool"), "--url", "http://127.0.0.1:1", "--workers", "eight", "--yes"]);
    assert.equal(r.status, 2);
    assert.match(r.stderr + r.stdout, /--workers needs a whole number/);
  });

  test("a bare --workers is refused too, rather than silently defaulting", () => {
    const r = cli(["--suite", path.join(here, "fixtures-pool"), "--url", "http://127.0.0.1:1", "--workers", "--yes"]);
    assert.equal(r.status, 2);
    assert.match(r.stderr + r.stdout, /--workers needs a whole number/);
  });

  test("it is named in the help, or nobody can find it", () => {
    // A feature the reader cannot discover is a feature we did not ship.
    const r = spawnSync(process.execPath, [path.join(here, "..", "bin", "smolanalytics.mjs"), "--help"], { encoding: "utf8" });
    assert.match(r.stdout, /--workers <n>/);
    assert.match(r.stdout, /at once/);
  });
});

describe("what the terminal says about a parallel run", () => {
  const suiteRun = async (workers) => {
    const lines = [];
    const code = await suiteCmd({
      suite: "tests",
      url: "https://preview.example.com",
      workers,
      log: (...a) => lines.push(a.join(" ").replace(/\x1b\[[0-9;]*m/g, "")),
      env: {},
      discoverImpl: () => ({ tests: mixedSuite().tests, notes: [], errors: [] }),
      runSuiteImpl: async ({ tests, workers: w }) => {
        assert.equal(w, Math.min(workers, tests.length), "suiteCmd did not pass the worker count through to the run");
        await sleep(20);
        return tests.map((t) => ({ ...t, status: "passed", mode: "replay", reason: "ok", ms: 5000, refreshed: false, layout: [], suspects: [] }));
      },
      postCommentImpl: async () => ({ posted: false, reason: "no" }),
    });
    return { code, out: lines.join("\n") };
  };

  test("a parallel run says how many lanes, up front, before the blocks start arriving", async () => {
    // The transcript below is about to arrive in completion order. A reader who was not told why
    // reads that as the tool losing its place.
    const { out } = await suiteRun(6);
    assert.match(out, /9 tests against https:\/\/preview\.example\.com · 6 at a time/);
  });

  test("it prints the WALL clock, not the sum of nine tests that ran at the same time", async () => {
    // summarize().ms is 9 x 5s = 45s of test time. Printing only that after a run the stopwatch
    // says took a fraction of a second is a number nobody can reconcile.
    const { out } = await suiteRun(6);
    const summary = out.split("\n").find((l) => /^9 tests · 9 passed/.test(l));
    assert.ok(summary, out);
    assert.match(summary, /· 0\.\ds ·/, `the wall clock is missing: ${summary}`);
    assert.match(summary, /45\.0s of test time across 6 workers/, `the old number lost its meaning instead of keeping it: ${summary}`);
  });

  test("a serial run prints exactly what it always printed", async () => {
    const { out } = await suiteRun(1);
    assert.match(out, /^\n?9 tests against https:\/\/preview\.example\.com$/m, "the serial header grew something");
    assert.match(out, /^9 tests · 9 passed · 45\.0s$/m, `the serial summary changed: ${out}`);
    assert.ok(!/at a time|of test time|workers/.test(out), `serial output must not mention workers at all:\n${out}`);
  });
});
