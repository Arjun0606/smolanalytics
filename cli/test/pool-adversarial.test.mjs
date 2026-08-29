// PARALLEL SUITE EXECUTION, ATTACKED.
//
// test/pool.test.mjs states the four guarantees. This file tries to break them, at the scale and in
// the shapes a customer's CI reaches and a nine-test fixture never does. A concurrency bug that
// passes a hundred runs and fails on somebody's pull request is the only kind worth hunting, so
// every claim here is measured — contexts counted at the browser, bytes counted in the file, PIDs
// counted in the process table — and never taken from something we told the run to say.
//
// WHAT IS ATTACKED, and how each one is made able to fail:
//
//   DETERMINISM     twelve tests with sleeps of 500/10/200/… so completion order is scrambled, run
//                   TEN times against one serial baseline. Each run asserts the fixture really did
//                   finish out of order before it compares anything, so a pool that quietly went
//                   serial could not pass by looking identical.
//   EXACTLY ONCE    fifty tests, all five statuses, each carrying a reason that names ITS OWN
//                   index. Counts alone cannot see a result duplicated over its neighbour when
//                   both are `passed`; identity can.
//   IDENTITIES      SMOLANALYTICS_RUN_ID + the suite index is what keeps fifty concurrent signups
//                   from colliding in somebody's database. Asserted distinct, and asserted equal
//                   to the set a serial run produces.
//   NO LEAKED       real Chromium, real app, fifty real replays: contexts OPENED and CLOSED are
//   CONTEXTS        counted on the real browser object. One leak per test is invisible at five and
//                   fatal at five hundred.
//   CANCELLATION    a real `npx smolanalytics test --suite` killed with SIGINT mid-run, and the
//                   process table checked for the browsers it started.
//   SHARED FILES    GITHUB_STEP_SUMMARY written by eight concurrent workers with payloads far over
//                   PIPE_BUF, and four tests whose recording ids collide before de-duplication.
//   THE MODULE      openPool's own boundary: a one-shot iterable, and a callback that throws
//   BOUNDARY        synchronously. Both silently lost work before this file existed.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn, execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { openPool } from "../lib/pool.mjs";
import { discover, runSuite, summarize, exitCode, commentBody, slug } from "../lib/suite.mjs";
import { loadPlaywright } from "../lib/test.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(here, "..", "bin", "smolanalytics.mjs");
const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-pooladv-"));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const plain = (s) => String(s).replace(/\x1b\[[0-9;]*m/g, "");

// ---- a real application, on a real port ----------------------------------------------------------

/**
 * One page with one button. `?ms=` delays the response, which is how a run is still in flight when
 * the signal arrives.
 *
 * closeAllConnections() BEFORE close(), always. A keep-alive socket held open by Chromium leaves
 * server.close() waiting forever and wedges the whole file.
 */
function startApp() {
  const server = createServer((req, res) => {
    const slow = Number(new URL(req.url, "http://x").searchParams.get("ms") || 0);
    const send = () => {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end('<!doctype html><title>Shop</title><h1>Your cart</h1><p>2 items in your cart.</p>'
        + '<button id="go">Proceed to checkout</button><div id="out"></div>'
        + '<script>document.getElementById("go").onclick=()=>{document.getElementById("out").textContent="Order placed";}</script>');
    };
    if (slow > 0) setTimeout(send, slow);
    else send();
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r({
    url: `http://127.0.0.1:${server.address().port}/`,
    close: () => new Promise((z) => {
      server.closeAllConnections();
      server.close(() => z());
    }),
  })));
}

/** N markdown tests on disk, each with its own sentence, and a replayable recording for each. */
function suiteOnDisk(n, url, { record = true } = {}) {
  const dir = scratch();
  const tests = path.join(dir, "tests");
  const plans = path.join(dir, "recordings");
  mkdirSync(tests, { recursive: true });
  mkdirSync(plans, { recursive: true });
  for (let i = 0; i < n; i++) {
    const file = `t${String(i).padStart(3, "0")}`;
    const name = `checkout ${i} works`;
    writeFileSync(path.join(tests, `${file}.md`),
      `# suite\n\n## ${name}\n\nClick Proceed to checkout and confirm order ${i} is placed.\n`);
    if (record) {
      writeFileSync(path.join(plans, `${slug(file)}--${slug(name)}.json`), JSON.stringify({
        startUrl: url,
        steps: [{ kind: "click", role: "button", name: "Proceed to checkout" }],
        proof: "Order placed",
        engine: "chromium",
      }) + "\n");
    }
  }
  return { dir, tests, plans };
}

/** The child, run without blocking the event loop this file's fixture app is served from. */
function runCli(args, env, { cwd, onStart } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [BIN, ...args], { cwd, env });
    let out = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (out += d));
    child.on("error", reject);
    child.on("close", (status, signal) => resolve({ status, signal, out: plain(out) }));
    if (onStart) onStart(child, () => out);
  });
}

/**
 * A scripted model that is STATELESS across calls.
 *
 * Eight workers share one process, so a `let turn = 0` in here is shared between tests: it made the
 * second test answer "finished" on its first turn, which is a 0-step pass that records nothing and
 * reads exactly like a lost file. The turn is read out of the conversation the model was handed,
 * and a ref that is not in that conversation throws instead of falling back to e1.
 */
const SCRIPTED_PASS = `
const real = globalThis.fetch;
globalThis.fetch = async (t, init = {}) => {
  if (!String(t).includes("api.anthropic.com")) return real(t, init);
  const body = JSON.parse(init.body);
  const seen = JSON.stringify(body.messages);
  let block;
  if (body.messages.length === 1) {
    const m = seen.match(/(e\\d+) button \\\\"Proceed to checkout\\\\"/);
    if (!m) throw new Error("no ref for the checkout button in the snapshot; refusing to guess");
    block = { type: "tool_use", id: "t1", name: "click", input: { ref: m[1], why: "check out" } };
  } else {
    block = { type: "tool_use", id: "t2", name: "finish", input: { passed: true, why: "The page showed Order placed.", proof: "Order placed" } };
  }
  return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content: [block] }) };
};
`;

/**
 * Every test fails, with a reason that names ITS OWN test and is padded past PIPE_BUF.
 *
 * The padding is the point: a 6KB append is the size at which a shared file written by eight
 * workers can tear, and a torn write shows up as a BEGIN whose END names a different test.
 */
const SCRIPTED_FAIL = `
const real = globalThis.fetch;
globalThis.fetch = async (t, init = {}) => {
  if (!String(t).includes("api.anthropic.com")) return real(t, init);
  const seen = JSON.stringify(JSON.parse(init.body).messages);
  const m = seen.match(/confirm (order \\d+) is placed/);
  if (!m) throw new Error("no 'order N' in the prompt; refusing to answer for a test it cannot identify");
  const pad = "x".repeat(6000);
  return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content: [
    { type: "tool_use", id: "t1", name: "finish", input: { passed: false, why: "BEGIN " + m[1] + " " + pad + " END " + m[1], proof: "" } },
  ] }) };
};
`;

const preloadOf = (dir, source) => {
  const p = path.join(dir, "scripted-model.mjs");
  writeFileSync(p, source);
  return new URL(`file://${path.resolve(p)}`).href;
};

// ---- determinism: ten runs against one serial baseline --------------------------------------------

// Deliberately not sorted and deliberately not palindromic: reversed, shuffled and identity are all
// different orderings of this, so a pool that returned completion order cannot come out matching.
const SLEEPS = [500, 10, 200, 350, 30, 500, 15, 250, 400, 20, 300, 5];
const STATUSES = ["passed", "failed", "stale", "errored", "flaky", "passed", "failed", "passed", "stale", "flaky", "errored", "passed"];

function outOfOrderSuite() {
  const finished = [];
  const runIds = [];
  const tests = SLEEPS.map((_, i) => ({
    file: `tests/t${i}.md`, name: `test ${i}`, test: `sentence ${i}`, id: `t${i}`, planPath: `recordings/t${i}.json`,
  }));
  const runTest = async ({ test, runId, onRun }) => {
    const i = Number(test.split(" ")[1]);
    await sleep(SLEEPS[i]);
    finished.push(i);
    runIds.push(runId);
    onRun({ status: STATUSES[i], mode: STATUSES[i] === "stale" ? "replay" : "agent", reason: `reason for test ${i}, and nothing else` });
    return STATUSES[i] === "failed" ? 1 : 0;
  };
  return { tests, runTest, finished, runIds };
}

async function reportOf(workers) {
  const f = outOfOrderSuite();
  const results = await runSuite({
    tests: f.tests, url: "http://127.0.0.1:1/", plansDir: "recordings", workers,
    runTest: f.runTest, mkdir: () => {}, hasPlan: () => true, hasKey: true, findSuspects: () => [],
    log: () => {}, env: { SMOLANALYTICS_RUN_ID: "ci-42" },
  });
  // ONLY the durations are pinned, and only because two SERIAL runs of one suite already differ in
  // those cells: comparing them would be comparing a stopwatch to itself. Everything a reviewer
  // reads — the rows, their order, the reasons, the counts, the exit code — is compared as it is.
  const pinned = results.map((r) => ({ ...r, ms: 1234 }));
  return {
    finished: f.finished.slice(),
    runIds: f.runIds.slice(),
    comment: commentBody(pinned, { url: "https://preview.example.com", suite: "tests", runUrl: "" }),
    exit: exitCode(results),
    summary: JSON.stringify({ ...summarize(results), ms: 0 }),
    rows: JSON.stringify(results.map((r) => [r.id, r.name, r.status, r.mode, r.reason])),
  };
}

describe("a suite that finishes out of order reports the same thing every single time", () => {
  test("ten parallel runs are identical to the serial run, and to each other", async () => {
    const base = await reportOf(1);
    assert.deepEqual(base.finished, SLEEPS.map((_, i) => i), "the serial baseline did not finish in suite order");
    assert.equal(base.exit, 1, "the fixture stopped containing a failure, so this is comparing two greens");

    const drift = [];
    let scrambled = 0;
    for (let run = 0; run < 10; run++) {
      const p = await reportOf(6);
      // The fixture has to actually finish out of order, on THIS run, or the comparison below is
      // theatre — a pool that had silently gone serial would pass every assertion after this one.
      if (JSON.stringify(p.finished) !== JSON.stringify(base.finished)) scrambled++;
      for (const k of ["comment", "exit", "summary", "rows"]) {
        if (JSON.stringify(p[k]) !== JSON.stringify(base[k])) drift.push(`run ${run}: ${k}`);
      }
      // Identities are per test, not per worker: same set, however the work was handed out.
      if (JSON.stringify([...p.runIds].sort()) !== JSON.stringify([...base.runIds].sort())) drift.push(`run ${run}: runIds`);
    }
    assert.equal(scrambled, 10, `the parallel runs finished in suite order ${10 - scrambled} time(s), so nothing here could have failed`);
    assert.deepEqual(drift, [], "the report is not deterministic under concurrency");
  });

  test("the identities fifty concurrent signups are kept apart by are distinct", async () => {
    const f = outOfOrderSuite();
    await runSuite({
      tests: f.tests, url: "http://127.0.0.1:1/", plansDir: "recordings", workers: 6,
      runTest: f.runTest, mkdir: () => {}, hasPlan: () => true, hasKey: true, findSuspects: () => [],
      log: () => {}, env: { SMOLANALYTICS_RUN_ID: "ci-42" },
    });
    assert.equal(f.runIds.length, SLEEPS.length);
    assert.equal(new Set(f.runIds).size, SLEEPS.length,
      `two tests were handed the same run id (${f.runIds.join(",")}), which is two signups colliding on one row`);
    assert.deepEqual([...f.runIds].sort(), SLEEPS.map((_, i) => `ci-42-${i + 1}`).sort());
  });
});

// ---- fifty tests, every verdict exactly once ------------------------------------------------------

describe("fifty tests at once, and every verdict lands exactly once", () => {
  const FIFTY = Array.from({ length: 50 }, (_, i) => ["passed", "failed", "stale", "errored", "flaky"][i % 5]);

  const runFifty = async (workers) => {
    const seen = [];
    const tests = FIFTY.map((_, i) => ({
      file: `tests/t${i}.md`, name: `test ${i}`, test: `sentence ${i}`, id: `t${i}`, planPath: `recordings/t${i}.json`,
    }));
    const results = await runSuite({
      tests, url: "http://127.0.0.1:1/", plansDir: "recordings", workers,
      runTest: async ({ test, onRun }) => {
        const i = Number(test.split(" ")[1]);
        // Staggered so the fifty genuinely FINISH interleaved rather than resolving in a neat
        // queue — and recorded after the sleep, because start order is 0,1,2,… whatever the pool
        // does and would have made the guard below unfalsifiable.
        await sleep(3 + ((i * 7) % 29));
        seen.push(i);
        const status = FIFTY[i];
        // The reason NAMES ITS OWN TEST. Counts cannot see result 12 written over result 13 when
        // both are `passed`; this can.
        onRun({ status, mode: status === "stale" ? "replay" : "agent", reason: `verdict belonging to test ${i}` });
        return status === "failed" ? 1 : 0;
      },
      mkdir: () => {}, hasPlan: () => true, hasKey: true, findSuspects: () => [],
      log: () => {}, env: {},
    });
    return { results, seen };
  };

  test("no verdict is lost, duplicated or written over its neighbour", async () => {
    const { results, seen } = await runFifty(8);
    assert.equal(results.length, 50);
    assert.deepEqual([...seen].sort((a, b) => a - b), FIFTY.map((_, i) => i), "some test ran twice, or never ran");
    assert.notDeepEqual(seen, FIFTY.map((_, i) => i), "the fifty ran in strict suite order, so interleaving was never exercised");
    for (let i = 0; i < 50; i++) {
      assert.equal(results[i].id, `t${i}`, `slot ${i} holds ${results[i].id}`);
      assert.equal(results[i].status, FIFTY[i], `slot ${i} has the wrong verdict`);
      assert.equal(results[i].reason, `verdict belonging to test ${i}`,
        `slot ${i} is carrying test ${(results[i].reason.match(/test (\d+)/) || [])[1]}'s verdict`);
    }
    // Every one of the five is really present, or "exactly once" is being proved about four.
    const s = summarize(results);
    assert.deepEqual({ p: s.passed, f: s.failed, st: s.stale, e: s.errored, x: s.flaky }, { p: 10, f: 10, st: 10, e: 10, x: 10 });
  });

  test("fifty at eight workers is the same report as fifty one at a time", async () => {
    const a = await runFifty(1);
    const b = await runFifty(8);
    const shape = (rs) => rs.map((r) => [r.id, r.status, r.mode, r.reason]);
    assert.deepEqual(shape(b.results), shape(a.results));
    assert.equal(exitCode(b.results), exitCode(a.results));
    assert.equal(exitCode(b.results), 1, "a fifty-test suite with ten failures in it must exit 1");
  });
});

// ---- the module boundary: two ways work was silently lost ------------------------------------------

describe("openPool's own boundary loses nothing", () => {
  test("a one-shot iterable is run, not drained and discarded", async () => {
    // Counting the total with Array.from(items) and then handing `items` on to mapOrdered drained
    // a generator between the two: zero tests ran and the caller got [] — a green suite, exit 0,
    // over nothing at all. The most dangerous possible answer, and it was silent.
    function* tests() {
      for (let i = 0; i < 5; i++) yield { i };
    }
    const lines = [];
    const pool = openPool({ workers: 4, log: (s) => lines.push(plain(s)) });
    const out = await pool.map(tests(), async (t) => ({ ran: t.i }));
    assert.deepEqual(out, [{ ran: 0 }, { ran: 1 }, { ran: 2 }, { ran: 3 }, { ran: 4 }]);
    assert.ok(lines.includes("5/5 done"), `the count never reached the total: ${JSON.stringify(lines)}`);
  });

  test("a callback that throws SYNCHRONOUSLY still flushes its block and still counts", async () => {
    // Promise.resolve(fn(...)) evaluates fn first, so a synchronous throw escaped before .finally
    // was attached. The verdict was still `errored` — but that test's whole buffered block went on
    // the floor, and the running count stayed one short of the verdicts for the rest of the run.
    const lines = [];
    const pool = openPool({ workers: 4, log: (s) => lines.push(plain(s)) });
    const out = await pool.map([0, 1, 2], (n, i, tlog) => {
      tlog(`evidence from test ${n}`);
      if (n === 1) throw new Error("it died before it returned a promise");
      return (async () => {
        await sleep(5);
        return { status: "passed" };
      })();
    });
    assert.equal(out[1].status, "errored");
    assert.match(out[1].reason, /it died before it returned a promise/);
    assert.ok(lines.includes("evidence from test 1"), "the block was dropped on the floor with the exception");
    assert.ok(lines.includes("3/3 done"),
      `the reader was told fewer tests finished than produced verdicts: ${JSON.stringify(lines.filter((l) => /done/.test(l)))}`);
  });
});

// ---- contexts, counted on the real browser --------------------------------------------------------

/** A loadBrowser that wraps the REAL one and counts what it opens and closes. */
function countingLoader() {
  const seen = { launched: 0, opened: 0, closed: 0, browser: null };
  const load = async (log, yes, engine) => {
    const r = await loadPlaywright(log, yes, engine);
    if (!r || !r.pw) return r;
    const real = r.pw[engine];
    return {
      ...r,
      pw: {
        ...r.pw,
        [engine]: {
          ...real,
          launch: async (options) => {
            seen.launched++;
            const b = await real.launch(options);
            seen.browser = b;
            const newContext = b.newContext.bind(b);
            b.newContext = async (o) => {
              seen.opened++;
              const c = await newContext(o);
              const close = c.close.bind(c);
              c.close = async (...a) => {
                seen.closed++;
                return close(...a);
              };
              return c;
            };
            return b;
          },
        },
      },
    };
  };
  return { seen, load };
}

describe("N workers, one browser, and not one context left behind", noBrowser, () => {
  const accounting = async (n, workers) => {
    const app = await startApp();
    try {
      const s = suiteOnDisk(n, app.url);
      const found = discover(s.tests, s.plans);
      assert.equal(found.tests.length, n, `discovered ${found.tests.length} of ${n} tests`);
      const c = countingLoader();
      const results = await runSuite({
        tests: found.tests, url: app.url, plansDir: s.plans, yes: true, workers,
        evidenceDir: path.join(s.dir, "evidence"),
        loadBrowser: c.load, log: () => {}, env: {}, hasKey: false,
      });
      const live = c.seen.browser ? c.seen.browser.contexts().length : -1;
      await c.seen.browser?.close().catch(() => {});
      return { ...c.seen, live, results };
    } finally {
      await app.close();
    }
  };

  test("fifty real replays across eight workers open fifty contexts and close fifty", async () => {
    const r = await accounting(50, 8);
    assert.equal(r.launched, 1, `eight workers launched ${r.launched} browsers; the whole point is one`);
    assert.equal(r.opened, 50, `fifty tests opened ${r.opened} contexts`);
    assert.equal(r.closed, r.opened, `${r.opened - r.closed} contexts were never closed — that is a leak per test, fatal at five hundred`);
    assert.equal(r.live, 0, `${r.live} contexts were still attached to the shared browser when the suite finished`);
    assert.deepEqual(r.results.map((x) => x.status), Array(50).fill("passed"),
      r.results.filter((x) => x.status !== "passed").map((x) => `${x.id}=${x.status} ${x.reason}`).join(" | "));
  });

  test("--workers 32 on a suite of 20 still balances, and still lands twenty verdicts", async () => {
    // Far more workers than cores, and more than the machine would ever choose for itself. It must
    // not lose a verdict and it must not leave a context behind; the memory it costs is the
    // caller's business and is documented in lib/pool.mjs.
    const r = await accounting(20, 32);
    assert.equal(r.launched, 1);
    assert.equal(r.opened, 20);
    assert.equal(r.closed, 20, `${r.opened - r.closed} contexts leaked at 32 workers`);
    assert.equal(r.live, 0);
    assert.deepEqual(r.results.map((x) => x.status), Array(20).fill("passed"));
  });

  test("a stale and an errored test close their contexts too, not only a passing one", async () => {
    // The paths that leak are the ones nobody runs fifty of: a recording that stopped fitting, and
    // a test with no recording and no key. Both still have to hand the context back.
    const app = await startApp();
    try {
      const dir = scratch();
      const tests = path.join(dir, "tests");
      const plans = path.join(dir, "recordings");
      mkdirSync(tests, { recursive: true });
      mkdirSync(plans, { recursive: true });
      const kinds = ["pass", "badproof", "badstep", "noplan", "pass", "badproof", "badstep", "noplan"];
      kinds.forEach((kind, i) => {
        const file = `t${String(i).padStart(3, "0")}`;
        const name = `${kind} ${i} works`;
        writeFileSync(path.join(tests, `${file}.md`), `# suite\n\n## ${name}\n\nClick Proceed to checkout and confirm order ${i} is placed.\n`);
        if (kind === "noplan") return;
        writeFileSync(path.join(plans, `${slug(file)}--${slug(name)}.json`), JSON.stringify({
          startUrl: app.url, engine: "chromium",
          steps: [{ kind: "click", role: "button", name: kind === "badstep" ? "A Button That Is Not There" : "Proceed to checkout" }],
          proof: kind === "badproof" ? "Refund issued to your card" : "Order placed",
        }) + "\n");
      });
      const found = discover(tests, plans);
      const c = countingLoader();
      const results = await runSuite({
        tests: found.tests, url: app.url, plansDir: plans, yes: true, workers: 4,
        evidenceDir: path.join(dir, "evidence"), loadBrowser: c.load, log: () => {}, env: {}, hasKey: false,
      });
      const live = c.seen.browser ? c.seen.browser.contexts().length : -1;
      await c.seen.browser?.close().catch(() => {});
      const statuses = results.map((r) => r.status);
      assert.ok(statuses.includes("stale") && statuses.includes("errored"),
        `the fixture stopped producing the non-passing paths this test is for: ${statuses.join(",")}`);
      assert.ok(statuses.includes("passed"), "and it stopped producing a passing one");
      assert.equal(c.seen.closed, c.seen.opened, `${c.seen.opened - c.seen.closed} contexts leaked on the stale/errored paths`);
      assert.equal(live, 0);
    } finally {
      await app.close();
    }
  });
});

// ---- cancellation ---------------------------------------------------------------------------------

describe("a run that is killed mid-flight leaves nothing behind", noBrowser, () => {
  /**
   * Every process descended from one pid, as [pid, command] pairs.
   *
   * By DESCENT, not by name. `pgrep -f ms-playwright` counts every browser on the machine, so a
   * second suite running in another terminal was attributed to this run and the test failed once
   * in five for a reason that had nothing to do with the pool — a test that can fail for the wrong
   * reason is as useless as one that cannot fail at all. Playwright puts the browser in its own
   * process group deliberately, so the group is not the family either; the parent chain is.
   */
  const descendants = (root) => {
    const byParent = new Map();
    for (const line of String(execSync("ps -Ao pid,ppid,command")).split("\n").slice(1)) {
      const m = line.trim().match(/^(\d+)\s+(\d+)\s+(.*)$/);
      if (!m) continue;
      if (!byParent.has(m[2])) byParent.set(m[2], []);
      byParent.get(m[2]).push([m[1], m[3]]);
    }
    const out = [];
    const stack = [String(root)];
    while (stack.length) {
      for (const [pid, cmd] of byParent.get(stack.pop()) || []) {
        out.push([pid, cmd]);
        stack.push(pid);
      }
    }
    return out;
  };
  /**
   * Of these [pid, command] pairs, the ones still running THE SAME command.
   *
   * The command is compared as well as the pid, because an orphaned browser is reparented to pid 1
   * and its own pid could in principle have been handed to something else by then. A recycled pid
   * would otherwise read as an orphan.
   */
  const stillRunning = (pairs) => {
    const alive = new Map();
    for (const line of String(execSync("ps -Ao pid,command")).split("\n").slice(1)) {
      const m = line.trim().match(/^(\d+)\s+(.*)$/);
      if (m) alive.set(m[1], m[2]);
    }
    return pairs.filter(([pid, cmd]) => alive.get(pid) === cmd);
  };

  test("SIGINT: no orphan browser survives, and the tests it cut short are errored, never failed", { timeout: 180_000 }, async () => {
    const app = await startApp();
    try {
      // Every page takes 1.5s, so the run is definitely still going when the signal lands.
      const slow = `${app.url}?ms=1500`;
      const s = suiteOnDisk(60, slow);
      let started = [];
      const r = await runCli(
        ["test", "--suite", s.tests, "--url", slow, "--plans", s.plans, "--workers", "8", "--yes"],
        { ...process.env, ANTHROPIC_API_KEY: "" },
        {
          cwd: s.dir,
          onStart: (child) => {
            setTimeout(() => {
              // Captured while the parent is still alive: once it dies the browsers are reparented
              // to pid 1 and there is no family left to walk.
              started = descendants(child.pid).filter(([, cmd]) => /ms-playwright/.test(cmd));
              child.kill("SIGINT");
            }, 8000);
          },
        },
      );
      assert.ok(started.length > 0, "no browser was running when the signal was sent, so this proves nothing");
      await sleep(3000);
      const orphans = stillRunning(started);
      assert.deepEqual(orphans.map(([pid, cmd]) => `${pid} ${cmd.slice(0, 60)}`), [],
        `${orphans.length} of ${started.length} browser processes outlived the run`);
      // The exit code is the signal's, never a verdict: 0, 1 and 2 all claim something about the
      // application, and a run that was killed observed nothing to claim.
      assert.ok(r.status === null || r.status > 2, `a cancelled run exited ${r.status}, which reads as a verdict`);
      // And the tests the teardown cut short are OUR outage, not a bug report about their app.
      // Sixty tests, a browser taken away at eight seconds: the ones that had not started yet
      // cannot open a context, and every one of them has to come back `errored`.
      const lines = r.out.split("\n").map((l) => l.trim());
      const errored = lines.filter((l) => /^error /.test(l));
      const failed = lines.filter((l) => /^fail /.test(l));
      assert.ok(errored.length > 0, `no test was cut short by the signal, so this proves nothing:\n${r.out.slice(-1200)}`);
      // Every one of them in the runner's own vocabulary, and the ones that never got a context at
      // all say whose outage it was in so many words.
      for (const l of errored) assert.match(l, /The run could not complete/);
      assert.ok(errored.some((l) => /test runner, not your application/i.test(l)),
        `not one cut-short test said whose outage it was:\n${errored.slice(-3).join("\n")}`);
      assert.deepEqual(failed, [],
        `a cancelled run reported ${failed.length} FAILURES, and a failure is a bug report about somebody's application`);
    } finally {
      await app.close();
    }
  });
});

// ---- files two workers share ----------------------------------------------------------------------

describe("the files eight workers write at the same time", noBrowser, () => {
  test("GITHUB_STEP_SUMMARY takes twelve six-kilobyte appends without a torn block", { timeout: 180_000 }, async () => {
    const app = await startApp();
    try {
      const s = suiteOnDisk(12, app.url, { record: false });
      const summary = path.join(s.dir, "STEP_SUMMARY.md");
      writeFileSync(summary, "");
      const r = await runCli(
        ["test", "--suite", s.tests, "--url", app.url, "--plans", s.plans, "--workers", "8", "--retries", "0", "--yes"],
        {
          ...process.env,
          ANTHROPIC_API_KEY: "sk-ant-test",
          GITHUB_STEP_SUMMARY: summary,
          NODE_OPTIONS: `--import ${preloadOf(s.dir, SCRIPTED_FAIL)}`,
        },
        { cwd: s.dir },
      );
      assert.equal(r.status, 1, `twelve failing tests must exit 1:\n${r.out.slice(-1500)}`);
      const text = readFileSync(summary, "utf8");
      assert.ok(text.length > 12 * 6000, `the summary is ${text.length} bytes; the payloads were meant to be over PIPE_BUF each`);
      const begins = [...text.matchAll(/BEGIN (order \d+) /g)].map((m) => m[1]);
      const ends = [...text.matchAll(/END (order \d+)/g)].map((m) => m[1]);
      assert.equal(begins.length, 12, `${begins.length} of 12 blocks reached the file`);
      assert.equal(new Set(begins).size, 12, "two workers wrote the same test's block");
      // A torn append shows up here: a BEGIN whose matching END is not the next END in the file,
      // because another worker's bytes landed in between.
      assert.deepEqual(ends, begins, "a block was interleaved with another worker's");
      assert.equal((text.match(/^### /gm) || []).length, 12);
    } finally {
      await app.close();
    }
  });

  test("four tests whose recording ids collide get four recordings, the same four a serial run writes", { timeout: 180_000 }, async () => {
    // Two tests sharing one recording file would overwrite each other on every run and then each
    // find the other's recording and report it stale forever. De-duplication is what prevents it,
    // and concurrency is where a lost de-duplication actually corrupts rather than merely churns.
    const app = await startApp();
    try {
      const run = async (workers) => {
        const dir = scratch();
        const tests = path.join(dir, "tests");
        const plans = path.join(dir, "recordings");
        mkdirSync(tests, { recursive: true });
        mkdirSync(plans, { recursive: true });
        writeFileSync(path.join(tests, "same.md"), Array.from({ length: 4 }, () =>
          "## checkout works\n\nClick Proceed to checkout and confirm the order is placed.\n").join("\n"));
        const r = await runCli(
          ["test", "--suite", tests, "--url", app.url, "--plans", plans, "--workers", String(workers), "--retries", "0", "--yes"],
          { ...process.env, ANTHROPIC_API_KEY: "sk-ant-test", NODE_OPTIONS: `--import ${preloadOf(dir, SCRIPTED_PASS)}` },
          { cwd: dir },
        );
        return { r, written: readdirSync(plans).sort(), bodies: readdirSync(plans).sort().map((f) => readFileSync(path.join(plans, f), "utf8")) };
      };
      const parallel = await run(4);
      assert.equal(parallel.r.status, 0, `\n${parallel.r.out.slice(-2000)}`);
      assert.match(parallel.r.out, /4 tests/, "the fixture stopped producing four colliding tests");
      assert.deepEqual(parallel.written, [
        "same--checkout-works-2.json", "same--checkout-works-3.json", "same--checkout-works-4.json", "same--checkout-works.json",
      ], "four concurrent tests did not each get their own recording");
      const serial = await run(1);
      assert.deepEqual(parallel.written, serial.written, "the files on disk depend on how many workers ran");
      assert.deepEqual(parallel.bodies, serial.bodies, "the recordings written in parallel are not the ones a serial run writes");
      for (const b of parallel.bodies) assert.match(b, /"proof":\s*"Order placed"/, "a recording was written without its proof");
    } finally {
      await app.close();
    }
  });
});
