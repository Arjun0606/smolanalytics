// FLAKE HANDLING, AND THE EVIDENCE A FAILURE LEAVES BEHIND.
//
// A suite people stop believing is worse than a suite that misses things, and the two cheapest
// ways to lose belief are a false failure with a transient cause and a retry that quietly turns a
// flake into a pass. The rules under test:
//
//   fail, then pass on retry  → `flaky`. NOT passed, NOT a bug report, does NOT fail the build.
//   fail, then fail again     → `failed`, the reason is the SECOND run's, the first run's steps
//                               are kept. Two observations of one defect, not one.
//   errored                   → never retried. Running a missing API key twice is just slower.
//   --retries 0               → exactly the old behaviour: one run, one verdict.
//
// The browser is real and the model is stubbed, the same shape as verdict.test.mjs: the retry path
// runs through Playwright, fresh pages, evidence capture and report(), with no spend.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { mkdtempSync, readFileSync, existsSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { testCmd, settle, wireRun, captureEvidence } from "../lib/test.mjs";
import { runSuite, summarize, exitCode, commentBody } from "../lib/suite.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const server = createServer((_req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end('<!doctype html><title>Shop</title><h1>Your cart</h1><p>2 items in your cart.</p><button>Proceed to checkout</button>');
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${server.address().port}/`;
after(() => new Promise((r) => server.close(() => r())));

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-flake-"));

// ---- the decision itself, with no browser in the way --------------------------------------------

describe("settle: what a retry is allowed to mean", () => {
  const fail1 = { passed: false, why: "On /, clicking Proceed to checkout showed Something went wrong." };
  const fail2 = { passed: false, why: "On /, the checkout button did nothing at all." };
  const pass2 = { passed: true, why: "The page showed Order placed.", proof: "Order placed" };

  test("one failing run settles as failed, untouched", () => {
    const s = settle([fail1]);
    assert.equal(s.status, "failed");
    assert.equal(s.reason, fail1.why, "a single observation must not grow claims about retries that never ran");
  });

  test("fail then pass is flaky, and the reason carries BOTH halves", () => {
    const s = settle([fail1, pass2]);
    assert.equal(s.status, "flaky", "a test that only passes on retry is not healthy");
    // Either half alone reads as a verdict nobody reached: only-the-failure looks like a bug
    // report, only-the-pass looks like a pass.
    assert.match(s.reason, /Something went wrong/, "what failed is missing");
    assert.match(s.reason, /Order placed/, "what then passed is missing");
    assert.ok(!/^passed/i.test(s.status), "flaky is its own status, never a pass");
  });

  test("fail then fail is failed, and the headline reason is the second run's", () => {
    const s = settle([fail1, fail2]);
    assert.equal(s.status, "failed");
    assert.match(s.reason, /did nothing at all/, "the second observation is the report");
    assert.ok(!s.reason.includes("Something went wrong"), "the first run's prose belongs in the steps, not the headline");
    assert.match(s.reason, /twice|both/i, "two observations are a stronger report, and the reader should know there were two");
  });
});

describe("what goes on the wire", () => {
  test("flaky is translated to the retry's own verdict, because the runs API refuses flaky", () => {
    // app/api/projects/[id]/runs/route.ts 400s an incoming "flaky": a row in the run log carries
    // one run's verdict, and the failing first run was already posted as its own row. Sending
    // "flaky" would lose the run entirely.
    const run = { test: "t", status: "flaky", mode: "agent", reason: "Passed only on retry." };
    assert.equal(wireRun(run).status, "passed");
    assert.equal(wireRun(run).reason, run.reason, "the reason still says it was a retry");
  });

  test("every other status crosses unchanged", () => {
    for (const status of ["passed", "failed", "stale", "errored"]) {
      assert.deepEqual(wireRun({ status, reason: "r" }), { status, reason: "r" });
    }
  });
});

// ---- the whole path: browser, retry, evidence, report -------------------------------------------

/**
 * Run one test with a scripted model. `script(attempt, turn)` returns the tool_use blocks for that
 * think() call; attempts are detected by the conversation starting over (one user message).
 */
async function run(script, opts = {}) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  let attempt = 0;
  let turn = 0;
  globalThis.fetch = async (target, init = {}) => {
    // A local teardown endpoint is part of what some tests below wire up; anything else that is
    // not the model — a project POST, telemetry — is still a bug in the runner.
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model and a local teardown endpoint may be called here");
    const body = JSON.parse(init.body);
    if (body.messages.length === 1) {
      attempt++;
      turn = 0;
    }
    turn++;
    const content = script(attempt, turn);
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content }), text: async () => "" };
  };
  try {
    const code = await testCmd({
      url, test: "the cart can be checked out", maxSteps: 5, evidenceDir: scratch(),
      log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r), ...opts,
    });
    return { code, runs, attempts: attempt, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

const finish = (passed, why, proof = "") => [{ type: "tool_use", id: "t1", name: "finish", input: { passed, why, proof } }];
const click = () => [{ type: "tool_use", id: "t1", name: "click", input: { ref: "e2", why: "try to check out" } }];

describe("a failing agent run is retried from a clean page", () => {
  test("fail then pass is FLAKY: exit 0, never counted as a pass, both halves reported", noBrowser, async () => {
    const evidenceDir = scratch();
    const summaryFile = path.join(scratch(), "summary.md");
    writeFileSync(summaryFile, "");
    const { code, runs, attempts, out } = await run(
      (attempt) => attempt === 1
        ? finish(false, "On /, clicking Proceed to checkout showed Something went wrong.")
        : finish(true, "The page showed Order placed.", "Order placed"),
      { evidenceDir, env: { GITHUB_STEP_SUMMARY: summaryFile } },
    );
    assert.equal(attempts, 2, "the retry really ran");
    assert.equal(code, 0, "flaky is a warning: it must not fail a build by default");
    // Two rows: the failing run as its own observation, then the settled flaky verdict. The run
    // history is where flakiness is derived, and it needs both.
    assert.deepEqual(runs.map((r) => r.status), ["failed", "flaky"]);
    assert.match(runs[1].reason, /Something went wrong/, "what failed is missing from the flaky reason");
    assert.match(runs[1].reason, /Order placed/, "what then passed is missing from the flaky reason");
    assert.match(out, /FLAKY/);
    assert.match(out, /retrying from a clean page/i, "a retry doubles the bill; it must be said out loud");
    assert.match(out, /--retries 0/, "the off switch must be named where the cost is incurred");
    assert.ok(!/\bPASS\b/.test(out), `a flaky run must never print as a pass:\n${out}`);

    // The evidence: a real PNG of the moment it failed, the page's text, and the URL.
    const dir = path.join(evidenceDir, "the-cart-can-be-checked-out");
    const png = readFileSync(path.join(dir, "failure.png"));
    assert.deepEqual([...png.subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], "failure.png is not a PNG");
    const txt = readFileSync(path.join(dir, "failure.txt"), "utf8");
    assert.match(txt, /^URL: http:\/\/127\.0\.0\.1/, "the URL is part of the evidence");
    assert.match(txt, /2 items in your cart/, "the page's visible text is part of the evidence");
    assert.match(out, /failure\.png/, "evidence nobody is told about is evidence nobody opens");

    // And on Actions, the run summary carries the failure and the paths.
    const summary = readFileSync(summaryFile, "utf8");
    assert.match(summary, /flaky/);
    assert.match(summary, /Something went wrong/);
    assert.match(summary, /failure\.png/);
  });

  test("fail then fail is FAILED with the second run's reason and both runs' steps", noBrowser, async () => {
    const { code, runs, attempts, out } = await run((attempt, turn) =>
      turn === 1 ? click() : finish(false, `run ${attempt} could not check out.`));
    assert.equal(attempts, 2, "two independent observations of the same defect");
    assert.equal(code, 1, "a failure confirmed twice is the one thing that exits 1");
    assert.deepEqual(runs.map((r) => r.status), ["failed", "failed"]);
    const final = runs[1];
    assert.match(final.reason, /run 2 could not check out/, "the reason must be the second run's");
    // "The first is kept in the steps": both runs' actions survive, distinguishable by run.
    assert.deepEqual([...new Set(final.steps.map((s) => s.run))].sort(), [1, 2], JSON.stringify(final.steps));
    assert.ok(runs[0].steps.length > 0, "the first run's own row keeps its steps too");
    assert.match(out, /observed 2 times|Observed twice/i);
  });

  test("--retries 0 disables the retry entirely", noBrowser, async () => {
    const { code, runs, attempts, out } = await run(
      () => finish(false, "On /, the checkout button did nothing."),
      { retries: 0 },
    );
    assert.equal(attempts, 1, "retries 0 must mean one agent run, one bill");
    assert.equal(code, 1);
    assert.deepEqual(runs.map((r) => r.status), ["failed"]);
    assert.equal(runs[0].reason, "On /, the checkout button did nothing.", "no retry prose may appear on a run that was never retried");
    assert.ok(!/retrying/i.test(out), out);
  });

  test("errored is never retried: a broken runner run twice is just slower", noBrowser, async () => {
    const realFetch = globalThis.fetch;
    const key = process.env.ANTHROPIC_API_KEY;
    process.env.ANTHROPIC_API_KEY = "sk-ant-test";
    let calls = 0;
    globalThis.fetch = async () => {
      calls++;
      return { ok: true, status: 200, json: async () => ({ stop_reason: "end_turn", content: [{ type: "text", text: "hmm" }] }), text: async () => "" };
    };
    try {
      const runs = [];
      const code = await testCmd({ url, test: "x", maxSteps: 3, evidenceDir: scratch(), log: () => {}, onRun: (r) => runs.push(r) });
      assert.equal(code, 2);
      assert.deepEqual(runs.map((r) => r.status), ["errored"]);
      assert.equal(calls, 1, "an errored run was retried");
    } finally {
      globalThis.fetch = realFetch;
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
  });

  test("the retry breaking keeps ONE failed row, never two", noBrowser, async () => {
    // The failing first run goes up as its own row before the retry starts. If the retry then
    // breaks on OUR side, the settled verdict is that same single observation — posting it again
    // put one failure on the wire twice, and the cloud's reliability window read a test as failing
    // twice as often as it did.
    const realFetch = globalThis.fetch;
    const key = process.env.ANTHROPIC_API_KEY;
    process.env.ANTHROPIC_API_KEY = "sk-ant-test";
    let attempt = 0;
    globalThis.fetch = async (t, init) => {
      const body = JSON.parse(init.body);
      if (body.messages.length === 1) attempt++;
      if (attempt === 2) throw new Error("model API down mid-retry");
      return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content: finish(false, "On /, checkout showed an error.") }), text: async () => "" };
    };
    try {
      const runs = [];
      const lines = [];
      const code = await testCmd({ url, test: "checkout works", maxSteps: 3, evidenceDir: scratch(), log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r) });
      assert.equal(code, 1, "the first run's real failure must survive our broken retry");
      assert.deepEqual(runs.map((r) => r.status), ["failed"], "one observation, one row");
      assert.match(lines.join("\n"), /keeping the failing run's verdict/);
    } finally {
      globalThis.fetch = realFetch;
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
  });

  test("an unwritable recording cannot change a settled flaky verdict", noBrowser, async () => {
    // A read-only checkout is CI housekeeping. The verdict was decided and reported before the
    // recording write; a throw here used to escape to the last-resort catch and append a
    // contradictory errored/2 on top of a run the agent watched settle.
    const notADir = path.join(scratch(), "occupied");
    writeFileSync(notADir, "a file, so no plan can be written under it");
    const { code, runs, out } = await run(
      // The retry clicks once before passing, so the recording has a step to compile and the
      // write is genuinely attempted.
      (attempt, turn) => attempt === 1
        ? finish(false, "On /, clicking Proceed to checkout showed Something went wrong.")
        : turn === 1 ? click() : finish(true, "The cart page rendered.", "2 items in your cart"),
      { plan: path.join(notADir, "plan.json") },
    );
    assert.equal(code, 0, "the settled flaky verdict must survive the unwritable recording");
    assert.deepEqual(runs.map((r) => r.status), ["failed", "flaky"]);
    assert.match(out, /could not write the recording/);
    assert.ok(!/ERROR/.test(out), out);
  });

  test("the teardown hook is told the settled verdict, not a guess", noBrowser, async () => {
    // The handler on the other end decides what to keep by status — a failed run is the one most
    // likely to have left half an account behind. Freezing the field at "errored" (the value it
    // starts at) survived every other test here, so this one pins it.
    const got = [];
    const td = createServer((req, res) => {
      let raw = "";
      req.on("data", (c) => (raw += c));
      req.on("end", () => { got.push(JSON.parse(raw)); res.end("ok"); });
    });
    await new Promise((r) => td.listen(0, "127.0.0.1", r));
    try {
      const { code } = await run(
        () => finish(false, "On /, the checkout button did nothing."),
        { retries: 0, teardown: `http://127.0.0.1:${td.address().port}/teardown` },
      );
      assert.equal(code, 1);
      assert.equal(got.length, 1, "the teardown must fire exactly once, whatever the verdict");
      assert.equal(got[0].status, "failed", "the endpoint was told a verdict nobody reached");
      assert.match(got[0].email, /^smoltest\+/);
    } finally {
      td.closeAllConnections?.();
      td.close();
    }
  });

  test("evidence capture failing cannot change the verdict", noBrowser, async () => {
    // A full disk or an unwritable directory is our housekeeping, and the agent already saw the
    // failure. Point the evidence at a path that cannot be a directory.
    const file = path.join(scratch(), "not-a-dir");
    writeFileSync(file, "occupied");
    const { code, runs } = await run(
      () => finish(false, "On /, the checkout button did nothing."),
      { retries: 0, evidenceDir: file },
    );
    assert.equal(code, 1, "the verdict must survive the missing screenshot");
    assert.deepEqual(runs.map((r) => r.status), ["failed"]);
  });
});

test("a configured project receives the pair — failed, then passed — never 'flaky' on the wire", noBrowser, async () => {
  // The cloud runs API 400s an incoming "flaky" ("a single run is never flaky"), so report() must
  // translate through wireRun. wireRun's own unit test cannot see report() forgetting to call it,
  // so this walks the REAL wire path — env-configured project, the actual fetch report() makes —
  // and asserts what lands. Lose the translation and a flaky run 400s and vanishes.
  const realFetch = globalThis.fetch;
  const savedEnv = {};
  for (const k of ["ANTHROPIC_API_KEY", "SMOLANALYTICS_PROJECT", "SMOLANALYTICS_WRITE_KEY", "SMOLANALYTICS_URL"]) savedEnv[k] = process.env[k];
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  process.env.SMOLANALYTICS_PROJECT = "prj_test";
  process.env.SMOLANALYTICS_WRITE_KEY = "wk_test";
  process.env.SMOLANALYTICS_URL = "https://cloud.invalid";
  const posted = [];
  let attempt = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).includes("cloud.invalid")) {
      posted.push(JSON.parse(init.body));
      return { ok: true, status: 200, json: async () => ({ ok: true }), text: async () => "" };
    }
    const body = JSON.parse(init.body);
    if (body.messages.length === 1) attempt++;
    const content = attempt === 1 ? finish(false, "Run 1 failed.") : finish(true, "Run 2 passed.", "2 items in your cart");
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content }), text: async () => "" };
  };
  try {
    const code = await testCmd({ url, test: "the cart can be checked out", maxSteps: 3, evidenceDir: scratch(), log: () => {} });
    assert.equal(code, 0);
    assert.deepEqual(posted.map((r) => r.status), ["failed", "passed"], "the cloud derives flakiness from exactly this pair of rows");
    assert.match(posted[1].reason, /retry/i, "the passed row's reason must still say it was only a retry that passed");
  } finally {
    globalThis.fetch = realFetch;
    for (const [k, v] of Object.entries(savedEnv)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
});

test("the recording after a flaky run is the RETRY's walk, from a truly clean page", noBrowser, async () => {
  // Its own server, because it needs to see what each request carried: a cookie is set on every
  // response, so a retry that reused the first run's context betrays itself on its first request.
  const navCookies = [];
  const srv = createServer((req, res) => {
    if (req.url === "/") navCookies.push(req.headers.cookie || "");
    res.writeHead(200, { "content-type": "text/html; charset=utf-8", "set-cookie": "crumb=1" });
    // Same shape as the shared page above, so the click() helper's ref (e2 = the button, after
    // the heading at e1) resolves here too.
    res.end('<!doctype html><title>Shop</title><h1>Your cart</h1><p>2 items in your cart.</p><button>Proceed to checkout</button>');
  });
  await new Promise((r) => srv.listen(0, "127.0.0.1", r));
  const plan = path.join(scratch(), "checkout.json");
  try {
    const { code, out } = await run(
      // Run 1 finishes without acting; only the retry clicks. A recording with that click in it
      // can only have come from the retry.
      (attempt, turn) => attempt === 1
        ? finish(false, "Run 1 saw the error banner.")
        : turn === 1 ? click() : finish(true, "The cart rendered.", "2 items in your cart"),
      { url: `http://127.0.0.1:${srv.address().port}/`, plan },
    );
    assert.equal(code, 0);
    const p = JSON.parse(readFileSync(plan, "utf8"));
    assert.equal(p.proof, "2 items in your cart", "the proof is the retry's, the run that passed");
    assert.equal(p.steps.length, 1, `the recorded walk must be the retry's own: ${JSON.stringify(p.steps)}`);
    assert.equal(p.steps[0].kind, "click");
    assert.match(out, /from the retry, the run that passed/);
    // Navigation 1 is run 1's, navigation 2 the retry's. A cookie on the second means the retry
    // inherited run 1's context, and "retrying from a clean page" was a lie.
    assert.deepEqual(navCookies, ["", ""], "the retry's first request carried run 1's cookies");
  } finally {
    await new Promise((r) => srv.close(() => r()));
  }
});

describe("--retries at the command line", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
  test("a typo'd count is refused out loud, never silently another number", () => {
    // Number("twice") is NaN and `NaN || 0`-style coercion turns a typo into retries OFF — the
    // exact person who asked for MORE retries silently gets none. Both flag shapes must refuse.
    for (const argv of [["test", "--retries", "twice"], ["test", "--retries=twice"]]) {
      const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 30_000 });
      assert.equal(r.status, 2, `${argv.join(" ")} must exit 2 — our refusal, never 1, which blames the app`);
      assert.match(r.stderr, /--retries needs a whole number/);
      assert.match(r.stderr, /0 disables/);
    }
  });
});

test("captureEvidence writes a real PNG and the page's text beside it", noBrowser, async () => {
  const browser = await chromium.launch();
  // finally, not a trailing close: a failing assertion that skips close() leaves a live Chromium
  // holding the event loop open, and `node --test` then hangs instead of going red.
  try {
    const page = await browser.newPage();
    await page.goto(url, { waitUntil: "domcontentloaded" });
    const dir = path.join(scratch(), "checkout");
    const out = await captureEvidence(page, dir);
    assert.deepEqual([...readFileSync(out.png).subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    assert.match(readFileSync(out.txt, "utf8"), /Proceed to checkout/);
  } finally {
    await browser.close();
  }
});

// ---- flaky through the suite: aggregation, exit code, the comment -------------------------------

describe("flaky through the suite", () => {
  const t = { file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" };

  test("the suite's status is the settled flaky verdict, and the knobs reach the runner", async () => {
    let got = null;
    const [r] = await runSuite({
      tests: [t], url: "https://x.test", retries: 3, evidenceDir: "ev", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ retries, evidenceDir, onRun }) => {
        got = { retries, evidenceDir };
        onRun({ status: "failed", mode: "agent", reason: "run 1 failed" });
        onRun({ status: "flaky", mode: "agent", reason: "Passed only on retry." });
        return 0;
      },
    });
    assert.equal(r.status, "flaky");
    assert.deepEqual(got, { retries: 3, evidenceDir: "ev" }, "--retries and --evidence-dir must survive the suite layer");
  });

  test("summarize counts flaky apart from passed, because that is the whole point", () => {
    const s = summarize([
      { status: "passed", mode: "replay", ms: 1 },
      { status: "flaky", mode: "agent", ms: 1 },
    ]);
    assert.equal(s.passed, 1);
    assert.equal(s.flaky, 1);
  });

  test("flaky does not fail the build, and never outranks a real problem", () => {
    const at = (status) => [{ status, mode: "", ms: 0, name: "x" }];
    assert.equal(exitCode(at("flaky")), 0, "flaky is a warning, not a gate");
    assert.equal(exitCode([...at("flaky"), ...at("failed")]), 1);
    assert.equal(exitCode([...at("flaky"), ...at("stale")]), 2);
  });

  test("the comment reads flaky as unreliability — never as a pass, never as an app bug", () => {
    const body = commentBody([
      { name: "A shopper can pay", file: "tests/a.md", status: "flaky", mode: "agent", ms: 30_000, reason: "Passed only on retry. Run 1 failed: the button showed Something went wrong. Run 2 passed: Order placed appeared." },
      { name: "The cart survives", file: "tests/b.md", status: "passed", mode: "replay", ms: 700 },
      { name: "Search finds a product", file: "tests/c.md", status: "failed", mode: "agent", ms: 9_000, reason: "No results appeared." },
    ], { url: "https://p.example.com", suite: "tests" });
    assert.match(body, /1 flaky/, "the headline must count it");
    const tableRows = body.split("\n").filter((l) => l.startsWith("|") && !/^\|\s*[-:]/.test(l) && !/\| how \|/.test(l));
    const row = tableRows.find((l) => l.includes("A shopper can pay"));
    assert.match(row, /flaky/);
    assert.match(row, /failed once, passed on retry/);
    assert.ok(!/\| pass \|/.test(row), row);
    assert.ok(!/\*\*fail\*\*/.test(row), `flaky rendered with the failure label:\n${row}`);
    // The order is the triage: what broke, then which test cannot be trusted, then the greens.
    const at = (name) => tableRows.findIndex((l) => l.includes(name));
    assert.ok(at("Search finds a product") < at("A shopper can pay"), `a real failure must outrank flaky:\n${tableRows.join("\n")}`);
    assert.ok(at("A shopper can pay") < at("The cart survives"), `flaky must sit above the passing rows, not among them:\n${tableRows.join("\n")}`);
    // The full reason is below the table, like a failure's is: the unreliability case IS the deliverable.
    assert.match(body, /Something went wrong/);
    assert.match(body, /not a pass and not a bug report/i, "the note must say what flaky is");
    assert.match(body, /does not fail the build/i);
  });
});
