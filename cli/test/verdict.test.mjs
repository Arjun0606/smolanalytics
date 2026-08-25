// WHICH OF THE THREE THINGS THE RUN SAYS WHEN THE AGENT DOES NOT SAY IT.
//
// Every other path has an obvious status. These do not, and each one of them ends in an exit code
// that either stops a merge or does not:
//
//   the model answers with prose and no tool call
//   the model runs out of the step budget without calling finish
//   the model refuses
//   the API returns 500
//
// None of those is the customer's checkout being broken, so none of them may report `failed` or
// exit 1. This was already wrong once — prose-instead-of-a-tool-call exited 1 — and nothing here
// caught it, so putting it back stayed green. That is what this file is for.
//
// The model is stubbed and the browser is real: the verdict path runs through Playwright, a live
// page and the same report() the product uses, with no spend and no network.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtempSync, existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { testCmd } from "../lib/test.mjs";

// A failing verdict now captures evidence, and the default directory is relative to the CWD —
// which under `npm test` is this repository. Evidence from a test OF the runner does not belong
// in the runner's repo, so every run here points it at a scratch directory instead.
const evidenceDir = mkdtempSync(path.join(tmpdir(), "smolanalytics-evidence-"));

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const server = createServer((_req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end('<!doctype html><title>Shop</title><h1>Your cart</h1><button>Proceed to checkout</button>');
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${server.address().port}/`;

after(() => new Promise((r) => server.close(() => r())));

/** Run one test with the model replying however this case needs it to. */
async function run(reply, opts = {}) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  globalThis.fetch = async (target) => {
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called here");
    return reply();
  };
  try {
    const code = await testCmd({
      url, test: "the cart can be checked out", maxSteps: 2, evidenceDir, log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r), ...opts,
    });
    return { code, runs, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

const ok = (body) => ({ ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) });
const prose = () => ok({ stop_reason: "end_turn", content: [{ type: "text", text: "I think the cart is probably fine." }] });
const clicking = () => ok({
  stop_reason: "tool_use",
  content: [{ type: "tool_use", id: "t1", name: "click", input: { ref: "e1", why: "look at the cart" } }],
});

describe("a run that reaches no verdict is never a bug report", () => {
  test("prose instead of a tool call is the runner, and exits 2", noBrowser, async () => {
    // Exit 1 here told somebody their checkout was broken because the model wandered off.
    const { code, runs, out } = await run(prose);
    assert.equal(code, 2, "exit 1 is reserved for the app being broken");
    assert.deepEqual(runs.map((r) => r.status), ["errored"]);
    assert.match(runs[0].reason, /this is the test runner, not your application/i);
    assert.ok(!/\bFAIL\b/.test(out), out);
  });

  test("running out of the step budget is the runner too", noBrowser, async () => {
    // The agent used every step it was given and never called finish. Nothing was observed, so
    // there is nothing to report about the app — and "usually the app did not do what the test
    // expected" was a guess printed as a finding.
    const { code, runs, out } = await run(clicking);
    assert.equal(code, 2);
    assert.deepEqual(runs.map((r) => r.status), ["errored"]);
    assert.match(runs[0].reason, /2 steps/);
    assert.ok(!/\bFAIL\b/.test(out), out);
    assert.ok(!/usually/i.test(out), "a cause we did not observe must not be printed as one");
  });

  test("a refusal is not a verdict about the app", noBrowser, async () => {
    const { code, runs } = await run(() => ok({ stop_reason: "refusal", content: [] }));
    assert.equal(code, 2);
    assert.equal(runs[0].status, "errored");
  });

  test("the model API being down is our outage, not their bug", noBrowser, async () => {
    const { code, runs } = await run(() => ({ ok: false, status: 529, text: async () => "overloaded" }));
    assert.equal(code, 2);
    assert.equal(runs[0].status, "errored");
    assert.match(runs[0].reason, /not your application/i);
  });

  test("a real verdict still comes back as one", noBrowser, async () => {
    const { code, runs } = await run(() => ok({
      stop_reason: "tool_use",
      content: [{ type: "tool_use", id: "t1", name: "finish", input: { passed: false, why: "On /, the checkout button did nothing." } }],
    }));
    assert.equal(code, 1, "a real observed failure is the one thing that may exit 1");
    assert.equal(runs[0].status, "failed");
  });

  test("a caller's bookkeeping cannot change a verdict", noBrowser, async () => {
    const { code } = await run(prose, { onRun: () => { throw new Error("the suite's own list blew up"); } });
    assert.equal(code, 2);
  });
});

// ---- the browser, when it is not there ----------------------------------------------------------

test("a browser that never downloaded says so, instead of the suite guessing at a missing key", async () => {
  // runSuite has no verdict to read here, so it falls back to noVerdictReason, which on a first CI
  // run answers "ANTHROPIC_API_KEY is not set". That sends someone to add a secret they already
  // have, for a Chromium that failed to install. The run has to name its own problem.
  const runs = [];
  const code = await testCmd({
    url, test: "the cart can be checked out", log: () => {}, onRun: (r) => runs.push(r),
    loadBrowser: async () => ({ pw: null, problem: "Playwright installed but Chromium did not. Run: npx playwright install chromium" }),
  });
  assert.equal(code, 2);
  assert.deepEqual(runs.map((r) => r.status), ["errored"]);
  assert.match(runs[0].reason, /Chromium did not/);
  assert.match(runs[0].reason, /not your application/i);
});

test("even a browser loader that says nothing produces a reason, never an empty one", async () => {
  const runs = [];
  await testCmd({ url, test: "x", log: () => {}, onRun: (r) => runs.push(r), loadBrowser: async () => ({ pw: null }) });
  assert.ok(runs[0].reason.trim().length > 20, runs[0].reason);
});

test("a browser that will not launch is our outage, not a bug in their app", async () => {
  // "Host system is missing dependencies to run browsers" is the commonest way a Playwright run
  // dies on a CI runner, and launch() sat OUTSIDE the try: it threw straight past this function's
  // errored/exit-2 contract to the CLI's last-resort catch, which exits 1.
  const runs = [];
  const code = await testCmd({
    url, test: "the cart can be checked out", log: () => {}, onRun: (r) => runs.push(r),
    loadBrowser: async () => ({ pw: { chromium: { launch: async () => { throw new Error("Host system is missing dependencies to run browsers"); } } } }),
  });
  assert.equal(code, 2, "exit 1 would read as the customer's app being broken");
  assert.deepEqual(runs.map((r) => r.status), ["errored"]);
  assert.match(runs[0].reason, /missing dependencies/);
  assert.match(runs[0].reason, /not your application/i);
});

test("a page that will not open is the same, and closing the browser is still attempted", async () => {
  let closed = false;
  const runs = [];
  const code = await testCmd({
    url, test: "x", log: () => {}, onRun: (r) => runs.push(r),
    loadBrowser: async () => ({ pw: { chromium: { launch: async () => ({
      newPage: async () => { throw new Error("Target page, context or browser has been closed"); },
      close: async () => { closed = true; },
    }) } } }),
  });
  assert.equal(code, 2);
  assert.equal(runs[0].status, "errored");
  assert.equal(closed, true, "a leaked Chromium on a self-hosted runner outlives the job");
});

// A PROOF THE AGENT DID NOT ACTUALLY READ OFF THE PAGE.
//
// `finish` demands "exact page text", and a model asked for exact text sometimes returns a
// paraphrase of it. compile() refuses an EMPTY proof and cannot refuse a WRONG one, so before this
// guard existed the paraphrase was written to the recording and the NEXT run replayed it, found the
// text missing, and reported "the page no longer says …, the text that proved this test the last
// time it passed" — false in both halves, on a page nobody had touched. The recording could then
// never settle: every run went stale and woke the agent, so the replay saving never arrived while
// the customer was told their copy had changed.
//
// The verdict is not in question here — the agent watched the test pass, and this must not move it.
// Only the recording is refused.
describe("a recording is only written when its proof is really on the page", () => {
  // One real click, then the verdict: compile() refuses a recording with no steps, so a run that
  // finishes on turn one would be dropped for a reason that has nothing to do with the proof.
  const finishWith = (proof) => {
    let turn = 0;
    return () => (++turn === 1
      ? ok({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t1", name: "click", input: { ref: "e2", why: "check out" } }] })
      : ok({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t2", name: "finish", input: { passed: true, why: "Checked out.", proof } }] }));
  };

  test("a proof that is not page text is refused, and the pass is untouched", noBrowser, async () => {
    const plan = path.join(mkdtempSync(path.join(tmpdir(), "smolanalytics-plan-")), "checkout.json");
    const r = await run(finishWith("Your order has been placed successfully"), { plan, retries: 0 });
    assert.equal(r.code, 0, "the agent observed a pass; refusing the recording must not change that");
    assert.equal(r.runs.at(-1).status, "passed");
    assert.equal(existsSync(plan), false, "a recording that could never replay green must not be written");
    assert.match(r.out, /not recorded/, "silence here is the bug: the reader must learn why the next run costs a model call");
    assert.match(r.out, /Your order has been placed successfully/, "the refused proof has to be quoted, or nobody can fix the sentence");
  });

  test("a proof that IS page text still records", noBrowser, async () => {
    const plan = path.join(mkdtempSync(path.join(tmpdir(), "smolanalytics-plan-")), "checkout.json");
    const r = await run(finishWith("Your cart"), { plan, retries: 0 });
    assert.equal(r.code, 0);
    assert.equal(existsSync(plan), true, "the guard must not cost us the recordings that were always fine");
    assert.equal(JSON.parse(readFileSync(plan, "utf8")).proof, "Your cart");
  });
});
