// WATCH MODE — the loop at the keyboard.
//
// What every test in this file is defending, and the failure it is defending against:
//
//   ONE SAVE IS ONE RUN. An editor writes a file two or three times per ⌘S, and a formatter-on-save
//   writes it again. Without a debounce that is three agent runs and three bills for one keystroke.
//
//   A RUN IS NEVER INTERRUPTED, AND ONLY ONE IS EVER QUEUED. Two saves during a run must not become
//   two more runs; the second answers a question the third already superseded.
//
//   THE RUNNER'S OWN WRITES ARE INVISIBLE TO IT. A passing test writes a recording, a failing one
//   writes a screenshot, a login writes a session file. If any of those woke the watcher, one save
//   would become run → write → run → write until somebody noticed — on their laptop, against their
//   model key. This is the way this feature destroys somebody's afternoon, so it is tested twice:
//   once with the directories injected, and once for real, with the real runner writing a real
//   recording into a NON-dotted directory inside the watched tree.
//
//   THE CEILING IS ON THE SESSION. --max-calls per run, in a loop that runs on every save, is not
//   a ceiling at all.
//
//   CTRL-C LEAVES NO BROWSER BEHIND. Counted, by descent from the child's own pid, before and after.
//
//   IT IS LOCAL. Watch never posts, comments or shares — proved by running the real binary with
//   every credential that would make it post set in the environment, and recording every request
//   that leaves.
//
// THE SHAPE OF THE FAKES, AND WHY THEY ARE ALLOWED. The queue tests inject a stand-in for runSuite,
// because what is under test is WHEN a run starts, not what a run decides — a real browser there
// would measure Chromium's startup and nothing else. Everything a fake could lie about — that a
// recording is really written, that a replay really costs nothing, that no request really leaves —
// is proved separately by the two tests at the bottom, which run the real binary against a real
// server with a real browser and no stand-ins at all.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, execSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, rmSync, utimesSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  watchCmd,
  ignoreFor,
  selectAffected,
  normalizeSelection,
  callSelector,
  pickSelectFn,
  loadSelector,
  resultLines,
  summaryLine,
  sessionLine,
  changedLine,
  selectionLine,
  warmBrowser,
  startWatcher,
  DEFAULT_DEBOUNCE_MS,
} from "../lib/watch.mjs";
import { newLedger, record } from "../lib/cost.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; the end-to-end tests skip with a reason */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };
const BIN = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const plain = (s) => String(s).replace(/\x1b\[[0-9;]*m/g, "");

async function until(pred, ms = 8000, step = 20) {
  const end = Date.now() + ms;
  for (;;) {
    if (await pred()) return true;
    if (Date.now() >= end) return false;
    await sleep(step);
  }
}

const dirs = [];
function fixture() {
  const dir = mkdtempSync(path.join(tmpdir(), "smolanalytics-watch-"));
  dirs.push(dir);
  mkdirSync(path.join(dir, "tests"), { recursive: true });
  mkdirSync(path.join(dir, "src"), { recursive: true });
  writeFileSync(path.join(dir, "tests", "cart.md"), "# Cart\n\n## the cart shows one item\n\nOpen the cart and check it lists one item.\n");
  writeFileSync(path.join(dir, "tests", "checkout.md"), "# Checkout\n\n## checkout takes a card\n\nPay with a test card and check the order confirms.\n");
  writeFileSync(path.join(dir, "src", "cart.ts"), "export const a = 1;\n");
  writeFileSync(path.join(dir, "src", "checkout.ts"), "export const b = 1;\n");
  return dir;
}
after(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true });
});

/** A stand-in for runSuite. Records the options it was handed and passes every test. */
function fakeSuite(calls, { gate = null, onEnter = null, statusFor = () => "passed" } = {}) {
  return async (opts) => {
    calls.push(opts);
    if (onEnter) await onEnter(opts);
    if (gate) await gate;
    return opts.tests.map((t) => ({ ...t, status: statusFor(t), mode: "replay", reason: "", ms: 5, suspects: [], layout: [], refreshed: false }));
  };
}

/**
 * A stand-in that goes THROUGH watchCmd's own runTest, which is where the ledger and the session
 * ceiling live. Nothing here decides a status that watchCmd did not compute.
 */
function meteredSuite(calls) {
  return async (opts) => {
    calls.push(opts);
    const rows = [];
    for (const t of opts.tests) {
      const seen = [];
      const code = await opts.runTest({ url: opts.url, test: t.test, plan: t.planPath, onRun: (r) => seen.push(r) });
      const last = seen[seen.length - 1];
      rows.push({ ...t, status: last ? last.status : code === 0 ? "passed" : "errored", reason: last?.reason || "", mode: "agent", ms: 3, suspects: [], layout: [] });
    }
    return rows;
  };
}

/** A stand-in for testCmd that spends `calls` model calls into whatever ledger it is handed. */
function meteredTestCmd(seenCeilings, perTest = 2) {
  return async (opts) => {
    seenCeilings.push(opts.maxCalls);
    for (let i = 0; i < perTest; i++) record(opts.ledger, { usage: { input_tokens: 100, output_tokens: 10 } });
    opts.onRun?.({ test: opts.test, status: "passed", mode: "agent", durationMs: 1, url: opts.url, reason: "ok" });
    return 0;
  };
}

/** Start a real watch session over a real directory. Everything below stops it in a finally. */
function start(dir, opts = {}) {
  const completed = [];
  const lines = [];
  let handle = null;
  const done = watchCmd({
    suite: path.join(dir, "tests"),
    url: "http://127.0.0.1:9/",
    root: dir,
    plans: path.join(dir, "recs"),
    evidenceDir: path.join(dir, "shots"),
    authDir: path.join(dir, "auth"),
    initial: false,
    signals: false,
    debounceMs: 60,
    selector: null,
    log: (s) => lines.push(plain(String(s))),
    onReady: (h) => (handle = h),
    onRunComplete: (r) => completed.push(r),
    ...opts,
  });
  const stop = async () => {
    await handle?.stop();
    return done;
  };
  return {
    done,
    completed,
    lines,
    stop,
    text: () => lines.join("\n"),
    ready: () => until(() => handle !== null),
  };
}

const touch = (p, body) => writeFileSync(p, body ?? `// ${Date.now()}-${Math.random()}\n`);

/* ── one save, one run ─────────────────────────────────────────────────────────────────────────── */

describe("a save starts exactly one run", () => {
  test("one save is one run, not three", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready(), "watch never became ready");
      touch(path.join(dir, "src", "cart.ts"), "export const a = 2;\n");
      assert.ok(await until(() => w.completed.length === 1), `no run happened: ${w.text()}`);
      // The window that matters: an editor's second and third write land tens of milliseconds
      // after the first, and a formatter's a hundred after that.
      await sleep(800);
      assert.equal(w.completed.length, 1, `one save produced ${w.completed.length} runs`);
      assert.equal(calls.length, 1);
    } finally {
      await w.stop();
    }
  });

  test("an editor writing the same file four times in a row is still one run", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready());
      const f = path.join(dir, "src", "checkout.ts");
      for (let i = 0; i < 4; i++) {
        touch(f, `export const b = ${i};\n`);
        await sleep(12);
      }
      assert.ok(await until(() => w.completed.length === 1), `no run happened: ${w.text()}`);
      await sleep(800);
      assert.equal(calls.length, 1, `four writes of one file produced ${calls.length} runs`);
    } finally {
      await w.stop();
    }
  });

  test("a burst across many files coalesces into one run over the union of them", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready());
      const written = [];
      for (let i = 0; i < 5; i++) {
        const f = path.join(dir, "src", `burst-${i}.ts`);
        touch(f, `export const x${i} = ${i};\n`);
        written.push(path.resolve(f));
        await sleep(8);
      }
      assert.ok(await until(() => w.completed.length === 1), `no run happened: ${w.text()}`);
      await sleep(800);
      assert.equal(calls.length, 1, `a five-file burst produced ${calls.length} runs`);
      const changed = w.completed[0].changed.map((c) => path.resolve(c));
      // A superset, not an equality: the platform may also report the directory that contained
      // them, and one extra path in the change set costs nothing.
      for (const f of written) assert.ok(changed.includes(f), `${path.basename(f)} is missing from the run's change set`);
    } finally {
      await w.stop();
    }
  });
});

/* ── the loop that would eat a laptop ──────────────────────────────────────────────────────────── */

describe("the runner's own writes are invisible to the watcher", () => {
  test("a write inside the recording, evidence and auth directories triggers nothing — and a source file still does", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready());
      for (const rel of ["recs", "shots", "auth", ".smolanalytics/recordings", "node_modules/pkg", ".git/objects", "dist", "build"]) {
        mkdirSync(path.join(dir, rel), { recursive: true });
        touch(path.join(dir, rel, "written.json"), '{"steps":[]}\n');
      }
      touch(path.join(dir, "src", "cart.ts~"), "backup\n");
      touch(path.join(dir, "src", ".cart.ts.swp"), "swap\n");
      await sleep(1200);
      assert.equal(calls.length, 0, `an ignored write started a run: ${w.text()}`);

      // AND THE SAME WATCHER STILL WORKS. Without this the test above passes just as well when the
      // watcher is broken, which is the version of this test that proves nothing at all.
      touch(path.join(dir, "src", "cart.ts"), "export const a = 3;\n");
      assert.ok(await until(() => calls.length === 1), `the watcher stopped seeing real files: ${w.text()}`);
    } finally {
      await w.stop();
    }
  });

  test("the recording a run writes cannot start the next run — the loop that would never stop", async () => {
    const dir = fixture();
    const calls = [];
    // Exactly what a passing test and a failing test do, at exactly the moment they do it.
    const writesArtefacts = async (opts) => {
      mkdirSync(path.join(dir, "recs"), { recursive: true });
      mkdirSync(path.join(dir, "shots", "cart"), { recursive: true });
      for (const t of opts.tests) writeFileSync(path.join(dir, "recs", `${t.id}.json`), JSON.stringify({ url: "http://x/", steps: [], proof: "x" }));
      writeFileSync(path.join(dir, "shots", "cart", "failure.png"), "not really a png");
      writeFileSync(path.join(dir, "shots", "cart", "failure.txt"), "page text");
    };
    const w = start(dir, { runSuiteImpl: fakeSuite(calls, { onEnter: writesArtefacts }) });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 4;\n");
      assert.ok(await until(() => calls.length === 1), `no run happened: ${w.text()}`);
      // Long enough that a feedback loop would have produced several more runs by now.
      await sleep(1800);
      assert.equal(calls.length, 1, `writing a recording fed back into the watcher: ${calls.length} runs from one save`);
    } finally {
      await w.stop();
    }
  });

  test("ignoreFor: the directories a run writes to are ignored by CONTAINMENT, not because they are dotted", () => {
    const root = path.resolve("/repo");
    const ignored = ignoreFor({
      roots: [root],
      // The undotted case, which the dotfile rule cannot save: `--plans recordings/`.
      dirs: [path.resolve("/repo/recordings"), path.resolve("/repo/shots"), path.resolve("/repo/sessions")],
    });
    for (const p of [
      "/repo/recordings/checkout.json",
      "/repo/recordings",
      "/repo/shots/cart/failure.png",
      "/repo/sessions/login.json",
      "/repo/.smolanalytics/recordings/a.json",
      "/repo/.git/index",
      "/repo/node_modules/react/index.js",
      "/repo/dist/bundle.js",
      "/repo/build/main.css",
      "/repo/out/index.html",
      "/repo/coverage/lcov.info",
      "/repo/.env.local",
      "/repo/src/cart.ts~",
      "/repo/src/.cart.ts.swp",
      "/repo/src/4913",
      "/elsewhere/src/cart.ts",
    ]) {
      assert.equal(ignored(p), true, `${p} should be ignored`);
    }
    for (const p of ["/repo/src/cart.ts", "/repo/tests/cart.md", "/repo/package.json", "/repo/app/routes/checkout.tsx", "/repo/recordings-of-my-band/song.ts"]) {
      assert.equal(ignored(p), false, `${p} should NOT be ignored`);
    }
  });
});

/* ── one run at a time ─────────────────────────────────────────────────────────────────────────── */

describe("a run in flight is never interrupted, and only one run is ever queued", () => {
  test("two saves during a run become ONE later run over both of them", async () => {
    const dir = fixture();
    const calls = [];
    let release = null;
    const gate = new Promise((r) => (release = r));
    let entered = 0;
    let finished = 0;
    const suite = async (opts) => {
      calls.push(opts);
      entered++;
      if (entered === 1) await gate;
      finished++;
      return opts.tests.map((t) => ({ ...t, status: "passed", mode: "replay", reason: "", ms: 5, suspects: [], layout: [] }));
    };
    const w = start(dir, { runSuiteImpl: suite });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 5;\n");
      assert.ok(await until(() => calls.length === 1), "the first run never started");

      const later = [path.resolve(dir, "src", "checkout.ts"), path.resolve(dir, "src", "extra.ts")];
      touch(later[0], "export const b = 5;\n");
      await sleep(120);
      touch(later[1], "export const c = 5;\n");
      // Well past the debounce: if a second run could start while one is in flight, it would have.
      await sleep(500);
      assert.equal(calls.length, 1, `a second run started while one was in flight (${calls.length} runs)`);
      assert.equal(finished, 0, "the first run finished early, so this proves nothing about interrupting it");

      release();
      assert.ok(await until(() => w.completed.length === 2), `the queued run never happened: ${w.text()}`);
      await sleep(600);
      assert.equal(calls.length, 2, `two saves during one run produced ${calls.length - 1} follow-up runs, not 1`);
      assert.equal(finished, 2, "the run in flight did not finish on its own terms");
      const changed = w.completed[1].changed.map((c) => path.resolve(c));
      for (const f of later) assert.ok(changed.includes(f), `${path.basename(f)} was dropped from the queued run`);
      assert.ok(w.text().includes("one more run is queued"), "the reader was never told a run was waiting");
    } finally {
      release?.();
      await w.stop();
    }
  });
});

/* ── cost ──────────────────────────────────────────────────────────────────────────────────────── */

describe("cost is per run and per session, and the ceiling is on the session", () => {
  test("the session total accumulates across runs while each run reports only its own", async () => {
    const dir = fixture();
    const calls = [];
    const ceilings = [];
    const w = start(dir, { runSuiteImpl: meteredSuite(calls), testCmdImpl: meteredTestCmd(ceilings, 2) });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 6;\n");
      assert.ok(await until(() => w.completed.length === 1), `no first run: ${w.text()}`);
      assert.equal(w.completed[0].runLedger.calls, 4, "two tests at two calls each is four model calls in the run");
      assert.equal(w.completed[0].session.calls, 4);

      touch(path.join(dir, "src", "checkout.ts"), "export const b = 6;\n");
      assert.ok(await until(() => w.completed.length === 2), `no second run: ${w.text()}`);
      assert.equal(w.completed[1].runLedger.calls, 4, "the RUN total must be this run's own spend, not the session's");
      assert.equal(w.completed[1].session.calls, 8, "the SESSION total must carry across runs");
      const out = w.text();
      assert.ok(out.includes("session · 2 runs · 8 model calls · 800 in / 80 out"), `the session line is wrong:\n${out}`);
      // ON THE PRINTED LINE, not only in the object. The second run's own summary must still read
      // 4 — printing the session total there would tell somebody watching their spend that one
      // save cost twice what it did, and the numbers would go on diverging with every run.
      const secondBlock = out.split("session · 1 run")[1];
      assert.ok(secondBlock, "there was no second run in the transcript");
      assert.match(secondBlock, /2 tests · 2 passed · [\d.]+s · 4 model calls · 400 in \/ 40 out/, `the second run's own cost line is wrong:\n${secondBlock}`);
    } finally {
      await w.stop();
    }
  });

  test("--max-calls is a ceiling on the WHOLE session, and a per-run ceiling would not catch it", async () => {
    const dir = fixture();
    const calls = [];
    const ceilings = [];
    const w = start(dir, { maxCalls: 5, runSuiteImpl: meteredSuite(calls), testCmdImpl: meteredTestCmd(ceilings, 2) });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 7;\n");
      assert.ok(await until(() => w.completed.length === 1), `no first run: ${w.text()}`);
      // Two tests, two calls each: the ceiling handed to the second already knows what the first
      // spent. A per-run cap would have handed both of them 5.
      assert.deepEqual(ceilings, [5, 3], `the ceiling did not shrink as the session spent: ${JSON.stringify(ceilings)}`);

      touch(path.join(dir, "src", "checkout.ts"), "export const b = 7;\n");
      assert.ok(await until(() => w.completed.length === 2), `no second run: ${w.text()}`);
      assert.deepEqual(ceilings, [5, 3, 1], "the second run's first test should have had 1 call left");
      const second = w.completed[1].results;
      assert.equal(second[1].status, "errored", "a test the budget stopped is errored — our decision, never a verdict about the app");
      assert.match(second[1].reason, /--max-calls ceiling of 5/);
      assert.ok(!second.some((r) => r.status === "failed"), "our own budget must never read as the application being broken");

      // And the run after the ceiling does not start at all.
      touch(path.join(dir, "src", "cart.ts"), "export const a = 8;\n");
      assert.ok(await until(() => w.completed.length === 3), `the third save produced no decision: ${w.text()}`);
      await sleep(400);
      assert.equal(calls.length, 2, "a run started after the session ceiling was reached");
      assert.equal(ceilings.length, 3, "a model call was made after the session ceiling was reached");
      assert.match(w.text(), /stopped at the --max-calls ceiling of 5/);
      assert.match(w.text(), /it will not spend again/);
    } finally {
      await w.stop();
    }
  });
});

/* ── which tests a save runs ───────────────────────────────────────────────────────────────────── */

describe("selection", () => {
  const tests = [
    { id: "cart--a", name: "a", file: "/repo/tests/cart.md", test: "…" },
    { id: "cart--b", name: "b", file: "/repo/tests/cart.md", test: "…" },
    { id: "checkout--c", name: "c", file: "/repo/tests/checkout.md", test: "…" },
  ];

  test("with no mapping, a source change runs the whole suite — watch is never the reason a test did not run", async () => {
    const r = await selectAffected({ tests, changed: ["/repo/src/cart.ts"], selector: null });
    assert.equal(r.tests.length, 3);
    assert.match(r.why, /no selection mapping yet/);
  });

  test("a changed test file runs its own tests and nothing else, with no mapping at all", async () => {
    const r = await selectAffected({ tests, changed: ["/repo/tests/cart.md"], selector: null });
    assert.deepEqual(r.tests.map((t) => t.id), ["cart--a", "cart--b"]);
    assert.equal(r.narrowed, true);
  });

  test("a mapping's ids narrow the run, and a changed test file is unioned in", async () => {
    const selector = { selectTests: () => ["checkout--c"] };
    const r = await selectAffected({ tests, changed: ["/repo/src/checkout.ts", "/repo/tests/cart.md"], selector });
    assert.deepEqual(r.tests.map((t) => t.id), ["cart--a", "cart--b", "checkout--c"]);
  });

  test("a mapping that answers with FILES selects every test in them", async () => {
    const r = await selectAffected({ tests, changed: ["/repo/src/x.ts"], selector: { select: () => ["/repo/tests/cart.md"] } });
    assert.deepEqual(r.tests.map((t) => t.id), ["cart--a", "cart--b"]);
  });

  test("an empty selection runs nothing and says so — editing the README is not a reason to spend", async () => {
    const r = await selectAffected({ tests, changed: ["/repo/README.md"], selector: { selectTests: () => [] } });
    assert.equal(r.tests.length, 0);
    assert.match(r.why, /no test touches what changed/);
  });

  test("a shape we do not understand runs EVERYTHING, in every direction it can be wrong", async () => {
    // lib/select.mjs does not exist yet. Every one of these is a plausible thing it could answer,
    // and each must degrade to today's behaviour rather than to a suite that silently shrank.
    for (const out of [null, undefined, 42, "cart--a", { nope: 1 }, ["not-an-id"], [{ weird: true }], [null]]) {
      const r = await selectAffected({ tests, changed: ["/repo/src/x.ts"], selector: { selectTests: () => out } });
      assert.equal(r.tests.length, 3, `a selection of ${JSON.stringify(out)} narrowed the suite`);
    }
  });

  test("a mapping that throws on every call shape runs everything", async () => {
    const r = await selectAffected({
      tests,
      changed: ["/repo/src/x.ts"],
      selector: {
        selectTests: () => {
          throw new Error("no git here");
        },
      },
    });
    assert.equal(r.tests.length, 3);
    assert.match(r.why, /did not recognise/);
  });

  test("a mapping written against the positional shape still works", async () => {
    const selector = {
      affectedTests: (a, b) => {
        // Refuses the object form, accepts (tests, changed) — the other half of the contract this
        // file has to guess, because it is written before lib/select.mjs is.
        if (!Array.isArray(a) || !Array.isArray(b)) throw new TypeError("wants two arrays");
        return a.filter((t) => t.file.includes("checkout"));
      },
    };
    const r = await selectAffected({ tests, changed: ["/repo/src/x.ts"], selector });
    assert.deepEqual(r.tests.map((t) => t.id), ["checkout--c"]);
  });

  test("an async mapping is awaited", async () => {
    const r = await selectAffected({ tests, changed: ["/repo/src/x.ts"], selector: { select: async () => new Set(["cart--a"]) } });
    assert.deepEqual(r.tests.map((t) => t.id), ["cart--a"]);
  });

  test("normalizeSelection accepts the wrappers a module might return, and refuses the rest", () => {
    assert.equal(normalizeSelection({ tests: ["cart--a"] }, tests).ok, true);
    assert.equal(normalizeSelection({ selected: ["cart--a"] }, tests).ok, true);
    assert.equal(normalizeSelection({ ids: ["cart--a"] }, tests).ok, true);
    assert.equal(normalizeSelection([{ id: "cart--a" }], tests).ok, true);
    assert.equal(normalizeSelection({ count: 2 }, tests).ok, false);
  });

  test("a module with no function we recognise is no mapping at all", async () => {
    assert.equal(pickSelectFn({ VERSION: 3 }), null);
    assert.equal((await callSelector({ VERSION: 3 }, { tests, changed: [] })).ok, false);
    // And the real import: today there is no lib/select.mjs, and that must be silence, not a crash.
    assert.equal(await loadSelector(), null);
    assert.equal(await loadSelector(() => Promise.reject(new Error("boom"))), null);
  });

  test("a save that selects nothing does not start a run at all", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls), selector: { selectTests: () => [] } });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 9;\n");
      assert.ok(await until(() => w.completed.length === 1), `the save was never noticed: ${w.text()}`);
      assert.equal(calls.length, 0, "a run started for a change no test touches");
      assert.match(w.text(), /nothing to run/);
    } finally {
      await w.stop();
    }
  });

  test("a test file added after watch started is discovered, and only it runs", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready());
      writeFileSync(path.join(dir, "tests", "refunds.md"), "# Refunds\n\n## a refund goes back to the card\n\nRefund an order.\n");
      assert.ok(await until(() => calls.length === 1), `a new test file was never picked up: ${w.text()}`);
      assert.deepEqual(calls[0].tests.map((t) => t.name), ["a refund goes back to the card"]);
      assert.match(w.text(), /running 1 of 3 tests/);
    } finally {
      await w.stop();
    }
  });
});

/* ── the watcher itself ────────────────────────────────────────────────────────────────────────── */

describe("watching without a dependency", () => {
  test("the polling fallback sees a save, with no OS watcher involved", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls), poll: true, pollMs: 60 });
    try {
      assert.ok(await w.ready());
      assert.match(w.text(), /polling/, "the header must say which watcher is in use");
      touch(path.join(dir, "src", "cart.ts"), "export const a = 10;\n");
      assert.ok(await until(() => calls.length === 1, 6000), `polling never noticed the save: ${w.text()}`);
      await sleep(500);
      assert.equal(calls.length, 1, "polling reported the same save twice");
    } finally {
      await w.stop();
    }
  });

  test("a new directory is not a change, but the file that lands in it is", async () => {
    // MEASURED: macOS reports `rename src [DIR]` and `rename src/nested [DIR]` when a directory is
    // created. Left in the change set, `src/` is a path that is no test's file, so `mkdir` — which
    // every scaffold, every `next build` and every `git checkout` does — would run the whole suite.
    const dir = fixture();
    const seen = [];
    const watcher = startWatcher({ roots: [dir], ignored: ignoreFor({ roots: [dir], dirs: [] }), onChange: (p) => seen.push(p) });
    try {
      await sleep(150);
      mkdirSync(path.join(dir, "src", "nested"), { recursive: true });
      await sleep(600);
      assert.deepEqual(seen, [], `a directory was reported as a change: ${seen.join(", ")}`);
      writeFileSync(path.join(dir, "src", "nested", "a.ts"), "export const n = 1;\n");
      assert.ok(await until(() => seen.length > 0, 4000), "the file inside the new directory was never seen");
      assert.deepEqual([...new Set(seen.map((p) => path.relative(dir, p)))], [path.join("src", "nested", "a.ts")]);
    } finally {
      watcher.close();
    }
  });

  test("an event from before the watcher opened is not a change — FSEvents replays them", async () => {
    // macOS delivers what happened in the moments before the watcher opened, so `save, then start
    // watch` ran a whole extra suite against a change already finished. Here the same shape is
    // produced deterministically: an mtime older than the watcher is not this session's work.
    const dir = fixture();
    const seen = [];
    const watcher = startWatcher({ roots: [dir], ignored: ignoreFor({ roots: [dir], dirs: [] }), onChange: (p) => seen.push(p) });
    try {
      await sleep(150);
      const old = new Date(Date.now() - 600_000);
      utimesSync(path.join(dir, "src", "cart.ts"), old, old);
      await sleep(600);
      assert.deepEqual(seen, [], `a change from before the session started ran the suite: ${seen.join(", ")}`);
      writeFileSync(path.join(dir, "src", "cart.ts"), "export const a = 13;\n");
      assert.ok(await until(() => seen.length > 0, 4000), "a real save after it was never seen");
    } finally {
      watcher.close();
    }
  });

  test("the poller never descends into an ignored directory, however large", async () => {
    const dir = fixture();
    // If node_modules were walked, this alone would put 400 files into every tick.
    for (let i = 0; i < 400; i++) {
      mkdirSync(path.join(dir, "node_modules", `p${i}`), { recursive: true });
      writeFileSync(path.join(dir, "node_modules", `p${i}`, "index.js"), "module.exports = 1;\n");
    }
    const seen = [];
    const ignored = ignoreFor({ roots: [dir], dirs: [] });
    const watcher = startWatcher({ roots: [dir], ignored, onChange: (p) => seen.push(p), poll: true, pollMs: 40 });
    try {
      await sleep(150);
      writeFileSync(path.join(dir, "node_modules", "p1", "index.js"), "module.exports = 2;\n");
      writeFileSync(path.join(dir, "src", "cart.ts"), "export const a = 11;\n");
      assert.ok(await until(() => seen.length > 0, 4000), "the poller saw nothing at all");
      await sleep(200);
      assert.deepEqual([...new Set(seen.map((p) => path.relative(dir, p)))], [path.join("src", "cart.ts")]);
    } finally {
      watcher.close();
    }
  });
});

/* ── what a person reads ───────────────────────────────────────────────────────────────────────── */

describe("the five statuses survive the compact output", () => {
  const results = [
    { name: "one", status: "passed", ms: 1200, reason: "Replayed the recorded run.", suspects: [] },
    { name: "two", status: "failed", ms: 3400, reason: "On /cart, clicking Proceed to checkout showed Something went wrong.", suspects: [{ file: "src/Checkout.tsx", evidence: "this diff removed 'Proceed to checkout'" }] },
    { name: "three", status: "stale", ms: 900, reason: "The recording no longer fits." },
    { name: "four", status: "errored", ms: 100, reason: "ANTHROPIC_API_KEY is not set." },
    { name: "five", status: "flaky", ms: 5000, reason: "Failed once, passed on retry." },
  ];

  test("every status is printed as itself, never folded into another", () => {
    const out = plain(resultLines(results).join("\n"));
    for (const s of ["passed", "failed", "stale", "errored", "flaky"]) assert.match(out, new RegExp(`\\b${s}\\b`), `${s} is missing`);
    const sum = plain(summaryLine(results, newLedger()));
    assert.match(sum, /5 tests/);
    assert.match(sum, /1 passed/);
    assert.match(sum, /1 failed/);
    assert.match(sum, /1 flaky/);
    assert.match(sum, /1 stale/);
    assert.match(sum, /1 could not run/);
    assert.ok(!/2 passed/.test(sum), "flaky or stale was counted as a pass");
  });

  test("a failure's reason is printed in full — it is the bug report, and it is never clipped", () => {
    const long = "x".repeat(40) + " " + "y".repeat(40) + " " + "z".repeat(40);
    const out = plain(resultLines([{ name: "n", status: "failed", ms: 1, reason: long, suspects: [] }]).join("\n"));
    for (const word of long.split(" ")) assert.ok(out.includes(word), "part of the reason was dropped");
    assert.ok(!out.includes("…"), "the reason was truncated");
  });

  test("the diff hint rides under the failure, and only under a failure", () => {
    const out = plain(resultLines(results).join("\n"));
    assert.match(out, /suspect: src\/Checkout\.tsx/);
    assert.equal((out.match(/suspect:/g) || []).length, 1);
  });

  test("a run that called no model says so, which is the whole economic argument", () => {
    assert.match(plain(summaryLine([results[0]], newLedger())), /no model calls/);
    assert.match(plain(sessionLine(3, newLedger())), /session · 3 runs · no model calls/);
  });

  test("the header names what changed without becoming a wall of paths", () => {
    const root = path.resolve("/repo");
    assert.equal(changedLine([path.resolve("/repo/src/a.ts")], root), "src/a.ts changed");
    assert.match(changedLine(["/repo/a.ts", "/repo/b.ts", "/repo/c.ts", "/repo/d.ts"].map((p) => path.resolve(p)), root), /and 3 more files changed/);
    assert.equal(plain(selectionLine({ picked: 2, total: 12, why: "why" })), "running 2 of 12 tests · why");
    assert.equal(plain(selectionLine({ picked: 12, total: 12, why: "" })), "running all 12 tests");
  });

  test("the ceiling and the locality are stated up front, not discovered later", async () => {
    const dir = fixture();
    const w = start(dir, { maxCalls: 4, runSuiteImpl: fakeSuite([]) });
    try {
      assert.ok(await w.ready());
      const out = w.text();
      assert.match(out, /nothing is posted, commented or shared/);
      assert.match(out, /ceiling on this whole session, not on each run/);
      assert.match(out, /ctrl-c to stop/);
    } finally {
      await w.stop();
    }
  });
});

/* ── refusals ──────────────────────────────────────────────────────────────────────────────────── */

describe("what watch refuses to start on", () => {
  test("no --suite, no --url, and a folder that is not there are each exit 2 and never 1", async () => {
    const said = [];
    const log = (s) => said.push(plain(String(s)));
    assert.equal(await watchCmd({ url: "http://x/", log, signals: false, selector: null }), 2);
    assert.equal(await watchCmd({ suite: "tests/", log, signals: false, selector: null }), 2);
    assert.equal(await watchCmd({ suite: path.join(tmpdir(), "definitely-not-here-9182"), url: "http://x/", log, signals: false, selector: null }), 2);
    assert.match(said.join("\n"), /--suite is missing/);
    assert.match(said.join("\n"), /--url is missing/);
    assert.match(said.join("\n"), /no such file or directory/);
  });

  test("the default debounce is the measured one, not zero", () => {
    assert.equal(DEFAULT_DEBOUNCE_MS, 150);
  });

  test("a production-looking URL is refused up front, because the question cannot be asked later", async () => {
    // lib/safety.mjs asks about a production URL from inside the run, whose transcript watch
    // suppresses by default — so the question would be printed where nobody is looking while
    // readline held the terminal, and the loop would read as hung. And a production URL under a
    // watcher is not one accidental order; it is one per save.
    const dir = fixture();
    const said = [];
    const code = await watchCmd({
      suite: path.join(dir, "tests"),
      url: "https://shop.example.com",
      root: dir,
      signals: false,
      selector: null,
      initial: false,
      log: (s) => said.push(plain(String(s))),
    });
    assert.equal(code, 2, "a refusal to start is exit 2 — never 1, which means the app is broken");
    assert.match(said.join("\n"), /looks like production, and watch re-runs on every save/);
    assert.match(said.join("\n"), /Nothing was opened and nothing was tested/);
  });

  test("--yes takes responsibility for a production URL and the loop starts", async () => {
    const dir = fixture();
    const calls = [];
    const w = start(dir, { url: "https://shop.example.com", yes: true, runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready(), "--yes did not get past the refusal");
      assert.match(w.text(), /https:\/\/shop\.example\.com/);
    } finally {
      await w.stop();
    }
  });

  test("nothing about sharing or commenting is even passed to the runner", async () => {
    // The structural half of "it is local". The half that can actually catch a regression is the
    // end-to-end test at the bottom, which sets every credential that would make this post and
    // then asserts that no request left the process.
    const dir = fixture();
    const calls = [];
    const w = start(dir, { runSuiteImpl: fakeSuite(calls) });
    try {
      assert.ok(await w.ready());
      touch(path.join(dir, "src", "cart.ts"), "export const a = 14;\n");
      assert.ok(await until(() => calls.length === 1), `no run happened: ${w.text()}`);
      for (const key of ["share", "comment", "publish", "publishShareImpl", "postCommentImpl"]) {
        assert.ok(!calls[0][key], `watch handed the runner ${key}`);
      }
      assert.equal(calls[0].workers, 1, "a watch run is one test at a time, for a person watching it");
    } finally {
      await w.stop();
    }
  });
});

/* ── the warm browser ──────────────────────────────────────────────────────────────────────────── */

describe("one browser for the session", () => {
  const stubPw = () => {
    const closed = [];
    let n = 0;
    const make = () => {
      const b = {
        id: ++n,
        connected: true,
        newContext: async () => ({ close: async () => {}, newPage: async () => ({}) }),
        close: async () => {
          b.connected = false;
          closed.push(b.id);
        },
        isConnected: () => b.connected,
        contexts: () => [],
        version: () => "1",
        on: (evt, fn) => {
          if (evt === "disconnected") b.fireDisconnect = fn;
        },
      };
      return b;
    };
    const launched = [];
    return {
      closed,
      launched,
      load: async () => ({ pw: { chromium: { launch: async () => { const b = make(); launched.push(b); return b; } } }, problem: "" }),
    };
  };

  test("many runs, one Chromium", async () => {
    const s = stubPw();
    const warm = warmBrowser({ load: s.load });
    for (let i = 0; i < 4; i++) {
      const { pw } = await warm.loadBrowser(() => {}, true, "chromium");
      const lease = await pw.chromium.launch({});
      await lease.newPage();
      await lease.close();
    }
    assert.equal(s.launched.length, 1, `four runs launched ${s.launched.length} browsers`);
    assert.equal(s.launched[0].connected, true, "a lease's close() must not close the shared browser");
    await warm.close();
    assert.deepEqual(s.closed, [1], "the session's browser was not closed on stop");
  });

  test("close() really ends the Chromium this session started — the mechanism, not the signal handler", { ...noBrowser, timeout: 120_000 }, async () => {
    // Playwright installs its own SIGINT handler and does close browsers. That is why the Ctrl-C
    // test below passes on two mechanisms at once, and why it cannot tell them apart. THIS test is
    // ours alone: no signal, no Playwright handler, just the object this file holds being closed.
    // The marker arg makes the browser findable in `ps` while other tests in this file are running
    // browsers of their own.
    const MARK = "SmolanalyticsWarmProbe";
    const warm = warmBrowser();
    const { pw } = await warm.loadBrowser(() => {}, true, "chromium");
    const lease = await pw.chromium.launch({ args: [`--enable-features=${MARK}`] });
    const page = await lease.newPage();
    await page.goto("about:blank");
    const running = descendants(process.pid).filter(([, cmd]) => cmd.includes(MARK));
    assert.ok(running.length > 0, "no browser was launched, so closing one proves nothing");
    await warm.close();
    await sleep(2000);
    const left = stillRunning(running);
    assert.deepEqual(left.map(([pid, cmd]) => `${pid} ${cmd.slice(0, 60)}`), [], "warmBrowser.close() left a browser running");
  });

  test("a browser that died between runs is replaced, not leased as a corpse", async () => {
    const s = stubPw();
    const warm = warmBrowser({ load: s.load });
    const first = await warm.loadBrowser(() => {}, true, "chromium");
    await (await first.pw.chromium.launch({})).close();
    s.launched[0].fireDisconnect?.();
    const second = await warm.loadBrowser(() => {}, true, "chromium");
    await (await second.pw.chromium.launch({})).close();
    assert.equal(s.launched.length, 2, "a dead browser was leased again, so every later save would have errored");
    await warm.close();
  });
});

/* ── the real thing ────────────────────────────────────────────────────────────────────────────── */

/** A page with a button that reveals the proof text, so a passing run has steps worth recording. */
function startApp() {
  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(
      '<!doctype html><title>Shop</title><h1>Shop</h1>' +
        '<button onclick="document.getElementById(\'c\').textContent=\'2 items in your cart.\'">Open cart</button>' +
        '<p id="c"></p>',
    );
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r({ server, url: `http://127.0.0.1:${server.address().port}/` })));
}

/**
 * A scripted model, plus a log of every request that left the process.
 *
 * `leaked.json` is the load-bearing half of the "it is local" test: anything that is neither the
 * model nor the fixture server is written down, and the assertion is that the file stays empty
 * while every credential that would make the product post something is set.
 */
function preloadOf(dir, notes, { hang = false } = {}) {
  const src = `
import { appendFileSync, writeFileSync } from "node:fs";
const real = globalThis.fetch;
const REF = /(e\\d+) (\\w+) "([^"]*)"/g;
const LEAKS = ${JSON.stringify(path.join(notes, "leaked.txt"))};
const MARK = ${JSON.stringify(path.join(notes, "in-flight.txt"))};
globalThis.fetch = async (t, init = {}) => {
  const url = String(typeof t === "object" && t && t.url ? t.url : t);
  if (!url.includes("api.anthropic.com")) {
    if (!url.startsWith("http://127.0.0.1:")) appendFileSync(LEAKS, url + "\\n");
    return real(t, init);
  }
  writeFileSync(MARK, "1");
  ${hang ? "await new Promise(() => {});" : ""}
  const body = JSON.parse(init.body);
  const seen = body.messages.map((m) => (typeof m.content === "string" ? m.content : JSON.stringify(m.content))).join("\\n");
  const ref = (kind, name) => {
    for (const m of seen.matchAll(REF)) if (m[2] === kind && m[3] === name) return m[1];
    throw new Error("no ref for " + kind + " " + name);
  };
  const block = body.messages.length === 1
    ? { type: "tool_use", id: "t1", name: "click", input: { ref: ref("button", "Open cart"), why: "open the cart" } }
    : { type: "tool_use", id: "t2", name: "finish", input: { passed: true, proof: "2 items in your cart", why: "The cart showed 2 items in your cart." } };
  return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content: [block], usage: { input_tokens: 1200, output_tokens: 90 } }) };
};
`;
  // OUTSIDE the watched tree, all of it. The stub writes a file on every model call; inside the
  // root that write is itself a save, and the first version of this harness had the model's own
  // bookkeeping starting runs — which is a small, private demonstration of exactly the loop the
  // feature is built to prevent.
  const p = path.join(notes, "scripted-model.mjs");
  writeFileSync(p, src);
  return new URL(`file://${path.resolve(p)}`).href;
}

/** Everything that would make the product post, comment or share, all set at once. */
const loudEnv = {
  GITHUB_TOKEN: "ghp_watch_must_not_use_this",
  GITHUB_REPOSITORY: "acme/shop",
  GITHUB_EVENT_NAME: "pull_request",
  GITHUB_REF: "refs/pull/7/merge",
  GITHUB_RUN_ID: "77",
  SMOLANALYTICS_KEY: "sa_watch_must_not_use_this",
  SMOLANALYTICS_PROJECT: "prj_watch",
  SMOLANALYTICS_URL: "https://example.invalid",
};

function watchApp(dir, notes, url, { extraArgs = [], extraEnv = {}, hang = false } = {}) {
  const child = spawn(
    process.execPath,
    [BIN, "watch", "--suite", path.join(dir, "tests"), "--url", url, "--root", dir, "--plans", path.join(dir, "recs"), "--evidence-dir", path.join(dir, "shots"), "--auth-dir", path.join(dir, "auth"), "--yes", ...extraArgs],
    { cwd: dir, env: { ...process.env, ...loudEnv, ANTHROPIC_API_KEY: "sk-ant-test", NODE_OPTIONS: `--import ${preloadOf(dir, notes, { hang })}`, ...extraEnv } },
  );
  let out = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (d) => (out += d));
  child.stderr.on("data", (d) => (out += d));
  const exited = new Promise((r) => child.on("close", (status, signal) => r({ status, signal })));
  return { child, exited, text: () => plain(out) };
}

/** Every process descended from one pid — by descent, so another terminal's browser is not ours. */
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

/** Of those pairs, the ones still running the SAME command — a recycled pid is not an orphan. */
const stillRunning = (pairs) => {
  const alive = new Map();
  for (const line of String(execSync("ps -Ao pid,command")).split("\n").slice(1)) {
    const m = line.trim().match(/^(\d+)\s+(.*)$/);
    if (m) alive.set(m[1], m[2]);
  }
  return pairs.filter(([pid, cmd]) => alive.get(pid) === cmd);
};

describe("the whole loop, for real: a real browser, a real server, a real recording", noBrowser, () => {
  test("save → re-run → replay for free, and the recording it writes does not start another run", { timeout: 180_000 }, async () => {
    const dir = fixture();
    // One test, so the model-call arithmetic below is a number a reader can check by hand.
    rmSync(path.join(dir, "tests", "checkout.md"));
    const notes = mkdtempSync(path.join(tmpdir(), "smolanalytics-watch-notes-"));
    dirs.push(notes);
    const app = await startApp();
    const w = watchApp(dir, notes, app.url);
    try {
      assert.ok(await until(() => /session · 1 run/.test(w.text()), 90_000), `the baseline run never finished:\n${w.text()}`);
      const first = w.text();
      assert.match(first, /passed\s+the cart shows one item/);
      assert.match(first, /2 model calls · 2,400 in \/ 180 out/);
      // A real recording, written by the real runner, into a NON-dotted directory inside the tree
      // this watcher is watching. This is the loop, set up exactly as it would really happen.
      assert.ok(existsSync(path.join(dir, "recs", "cart--the-cart-shows-one-item.json")), "no recording was written, so the loop this test exists for cannot even occur");

      await sleep(2500);
      assert.equal((w.text().match(/session · /g) || []).length, 1, `the recording write started another run:\n${w.text()}`);

      writeFileSync(path.join(dir, "src", "cart.ts"), "export const a = 12;\n");
      assert.ok(await until(() => /session · 2 runs/.test(w.text()), 60_000), `the save never re-ran:\n${w.text()}`);
      const second = w.text().split("session · 1 run")[1];
      assert.match(second, /src\/cart\.ts changed/);
      // The economic claim, in the loop: the second run replays and costs nothing, and the session
      // total stays where the first run left it.
      assert.match(second, /no model calls/);
      assert.match(w.text(), /session · 2 runs · 2 model calls/);

      // NOTHING LEFT THE MACHINE. Every credential that would make this post is set above.
      const leaked = existsSync(path.join(notes, "leaked.txt")) ? readFileSync(path.join(notes, "leaked.txt"), "utf8").trim() : "";
      assert.equal(leaked, "", `watch mode made requests it must never make:\n${leaked}`);
      assert.ok(!/smolanalytics\.com\/s\/|shared at|https:\/\/example\.invalid/.test(w.text()), "watch printed a share link");

      const before = descendants(w.child.pid).filter(([, cmd]) => /ms-playwright/.test(cmd));
      assert.ok(before.length > 0, "no browser was warm between runs, so the Ctrl-C half of this proves nothing");
      w.child.kill("SIGINT");
      const { status } = await w.exited;
      assert.ok(status === null || status > 2, `a stopped watch session exited ${status}, which reads as a verdict about the app`);
      await sleep(2500);
      const orphans = stillRunning(before);
      assert.deepEqual(orphans.map(([pid, cmd]) => `${pid} ${cmd.slice(0, 60)}`), [], `${orphans.length} browser processes outlived the session`);
      assert.match(w.text(), /stopped\. Nothing was posted, commented or shared\./);
    } finally {
      w.child.kill("SIGKILL");
      app.server.closeAllConnections();
      await new Promise((r) => app.server.close(r));
    }
  });

  test("Ctrl-C in the MIDDLE of a run leaves no orphan Chromium", { timeout: 180_000 }, async () => {
    const dir = fixture();
    const notes = mkdtempSync(path.join(tmpdir(), "smolanalytics-watch-notes-"));
    dirs.push(notes);
    const app = await startApp();
    // The model never answers, so the run is genuinely in flight — a browser open, a page loaded,
    // a request outstanding — for as long as we like.
    const w = watchApp(dir, notes, app.url, { hang: true });
    try {
      assert.ok(await until(() => existsSync(path.join(notes, "in-flight.txt")), 90_000), `the run never reached the model:\n${w.text()}`);
      const before = descendants(w.child.pid).filter(([, cmd]) => /ms-playwright/.test(cmd));
      assert.ok(before.length > 0, "no browser was running when the signal was sent, so this proves nothing");
      w.child.kill("SIGINT");
      const { status } = await w.exited;
      assert.ok(status === null || status > 2, `a cancelled session exited ${status}, which reads as a verdict`);
      await sleep(3000);
      const orphans = stillRunning(before);
      assert.deepEqual(orphans.map(([pid, cmd]) => `${pid} ${cmd.slice(0, 60)}`), [], `${orphans.length} of ${before.length} browser processes outlived Ctrl-C`);
      // And the session still said what it cost before it went.
      assert.match(w.text(), /session · /);
    } finally {
      w.child.kill("SIGKILL");
      app.server.closeAllConnections();
      await new Promise((r) => app.server.close(r));
    }
  });
});
