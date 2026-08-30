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
import { mkdtempSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { runnerProblem, testCmd } from "../lib/test.mjs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

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


// ---- what the reader is told when the run could not happen ---------------------------------------
//
// THE REQUIREMENT: the sentence the reader sees first must name the cause, the sentence after it
// must name the fix, and neither may be a Call log or a JSON error body.
//
// Measured before this existed, by running the real binary against a port with nothing on it:
//
//   the run could not complete: page.goto: net::ERR_CONNECTION_REFUSED at http://127.0.0.1:4999/pricing
//   Call log:
//     - navigating to "http://127.0.0.1:4999/pricing", waiting until "domcontentloaded"
//
// and with a key that had been revoked:
//
//   the run could not complete: the model call failed (401). {"type":"error","error":{"type":
//   "authentication_error","message":"API key is invalid."},"request_id":null}
//
// Both are the first minute going badly, and neither says "your app is not running" or "your key
// was rejected". None of this touches a status or an exit code: every case below is still errored.

describe("why the run stopped, in a sentence somebody can act on", () => {
  const refused = new Error(
    'page.goto: net::ERR_CONNECTION_REFUSED at http://127.0.0.1:4999/pricing\nCall log:\n  - navigating to "http://127.0.0.1:4999/pricing", waiting until "domcontentloaded"\n',
  );

  test("nothing listening: the cause names the address, the fix names the flag", () => {
    const r = runnerProblem(refused);
    assert.match(r.what, /Nothing is listening at http:\/\/127\.0\.0\.1:4999\.?$/);
    assert.match(r.fix, /--url/, "the fix must name what to change");
    assert.ok(!/ERR_CONNECTION_REFUSED/.test(`${r.what} ${r.fix}`), "a Chromium error code is not a sentence");
  });

  test("the Call log never survives, whatever the cause", () => {
    for (const e of [refused, new Error('boom\nCall log:\n  - navigating to "http://x.test/"')]) {
      const r = runnerProblem(e);
      assert.ok(!/Call log/.test(`${r.what} ${r.fix}`), `the Call log was printed:\n${r.what}\n${r.fix}`);
      assert.ok(!/navigating to/.test(`${r.what} ${r.fix}`));
    }
  });

  test("a rejected key says the key was rejected, and no JSON body reaches the reader", () => {
    const r = runnerProblem(new Error('the model call failed (401). {"type":"error","error":{"type":"authentication_error","message":"API key is invalid."},"request_id":null}'));
    assert.match(r.what, /ANTHROPIC_API_KEY/, "the reader has to know WHICH key");
    assert.match(r.what, /401/);
    assert.ok(!/\{|\}/.test(`${r.what} ${r.fix}`), `a JSON blob reached the reader:\n${r.what} ${r.fix}`);
    assert.match(r.fix, /console\.anthropic\.com/, "the fix must say where to go");
  });

  test("each model status gets its own answer, and 429 and 5xx are never blamed on the app", () => {
    assert.match(runnerProblem(new Error("the model call failed (429). x")).what, /rate-limited/i);
    assert.match(runnerProblem(new Error("the model call failed (503). x")).what, /503/);
    assert.match(runnerProblem(new Error("the model call failed (503). x")).fix, /their side/i);
    assert.match(runnerProblem(new Error("the model call failed (403). x")).what, /ANTHROPIC_API_KEY/);
  });

  test("a name that does not resolve, a bad certificate and a hang each say which one it is", () => {
    const dns = runnerProblem(new Error("page.goto: net::ERR_NAME_NOT_RESOLVED at https://nope.invalid/x"));
    assert.match(dns.what, /nope\.invalid.*does not resolve/);
    const tls = runnerProblem(new Error("page.goto: net::ERR_CERT_AUTHORITY_INVALID at https://local.test/x"));
    assert.match(tls.what, /certificate/);
    assert.match(tls.fix, /http:\/\//, "the usual cause is https on a local server");
    const slow = runnerProblem(new Error('page.goto: Timeout 30000ms exceeded.\nCall log:\n  - navigating to "http://127.0.0.1:4322/pricing", waiting until "domcontentloaded"\n'));
    assert.match(slow.what, /did not finish loading within 30s/);
    assert.match(slow.what, /127\.0\.0\.1:4322/, "the reader must be told WHICH page hung");
  });

  test("anything unrecognised is passed through, never swallowed or renamed", () => {
    const r = runnerProblem(new Error("ENOSPC: no space left on device, write"));
    assert.equal(r.known, false, "an error with nothing better to say must not pretend it was recognised");
    assert.equal(r.what, "ENOSPC: no space left on device, write");
    assert.equal(r.fix, "");
  });

  test("known is what lets an unrecognised error keep the sentence it always had", () => {
    // The callers wrap `known: false` in their own "the run could not complete" / "the survey could
    // not complete", so nothing this function has never seen reads differently than it did before
    // this function existed. Only a cause we can actually name gets its own opening line.
    assert.equal(runnerProblem(new Error("page.goto: net::ERR_CONNECTION_REFUSED at http://x.test/")).known, true);
    assert.equal(runnerProblem(new Error("the model call failed (401). {}")).known, true);
    assert.equal(runnerProblem(new Error("something nobody has seen")).known, false);
  });

  test("a port the browser refuses outright says so, rather than blaming the app", () => {
    const r = runnerProblem(new Error("page.goto: net::ERR_UNSAFE_PORT at http://127.0.0.1:1/x"));
    assert.match(r.what, /refuses to open port 1\b/);
    assert.match(r.fix, /another one/);
  });

  test("the real binary prints it that way round, and it is still errored and still exit 2", async () => {
    // A port nothing is on: bound, read, released. Real process, real Chromium, real replay path —
    // the only way to prove the translation is on the path the customer takes. Not a hardcoded low
    // port: Chromium blocks those outright with a different error, which is its own case above.
    const probe = createServer(() => {});
    await new Promise((r) => probe.listen(0, "127.0.0.1", r));
    const port = probe.address().port;
    probe.closeAllConnections();
    await new Promise((r) => probe.close(() => r()));

    const dir = mkdtempSync(path.join(tmpdir(), "smolanalytics-why-"));
    const planPath = path.join(dir, "p.json");
    const at = `http://127.0.0.1:${port}/x`;
    writeFileSync(planPath, JSON.stringify({ startUrl: at, steps: [{ kind: "goto", url: at }], proof: "anything" }));
    const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
    const r = spawnSync(process.execPath, [bin, "test", "--url", `http://127.0.0.1:${port}`, "--test", "it works", "--plan", planPath, "--yes"], { encoding: "utf8", timeout: 120_000 });
    const out = `${r.stdout}${r.stderr}`.replace(/\x1b\[[0-9;]*m/g, "");
    assert.equal(r.status, 2, `the runner failing is exit 2, never 1:\n${out}`);
    assert.match(out, new RegExp(`Nothing is listening at http://127\\.0\\.0\\.1:${port}\\.`), out);
    assert.ok(!/Call log/.test(out), `the Call log reached the terminal:\n${out}`);
    assert.ok(!/ERR_CONNECTION_REFUSED/.test(out), `the Chromium error code reached the terminal:\n${out}`);
  });
});

// A PASS THAT RECORDS NOTHING, AND SAID NOTHING ABOUT IT.
//
// compile() returns null for a run with no replayable step, exactly as it does for one with no
// proof — but only the proofless case ever said so. The other branch is an ordinary, correct test:
// "the pricing page shows a monthly price", started on the pricing page, reads the page, finishes.
// Nothing to replay, and nothing to record.
//
// MEASURED, walking a CI run of three tests: two printed "recorded 1 steps … the next run needs no
// model", this one printed NOTHING, and every run afterwards reported "1 of 3 woke the agent". The
// two causes the summary offered — no recording yet, recording stopped fitting — are both false
// here and both imply the number will settle. It never does. The shipped workflow then tells that
// reader to suspect their Actions cache, which is working perfectly.
//
// Silence at the exact moment the sibling tests spoke, on the one fact that explains the bill.
describe("a pass that could not be recorded says so, whichever reason it was", () => {
  const finishAtOnce = (proof) => () => ok({
    stop_reason: "tool_use",
    content: [{ type: "tool_use", id: "t1", name: "finish", input: { passed: true, why: "The cart is there.", proof } }],
  });

  test("a read-only pass names its own cost instead of leaving the reader to the cache", noBrowser, async () => {
    const plan = path.join(mkdtempSync(path.join(tmpdir(), "smolanalytics-plan-")), "cart.json");
    const r = await run(finishAtOnce("Your cart"), { plan, retries: 0 });

    assert.equal(r.code, 0, "the agent observed a pass; nothing here may move the verdict");
    assert.equal(r.runs.at(-1).status, "passed");
    assert.equal(existsSync(plan), false, "a plan with no steps would pass by exercising nothing — it must still not be written");

    // THE REQUIREMENT: the run must say a recording was not made. Silence is the defect.
    assert.match(r.out, /not recorded/,
      `a pass that recorded nothing said nothing, so the next run's cost has no explanation anywhere:\n${r.out}`);
    // And it must say the thing the summary line cannot: this one does not settle.
    assert.match(r.out, /every run/,
      "without this the reader expects the number to fall, and goes looking for a broken cache when it does not");
    // It must NOT be reported as the OTHER no-recording case. The proof was fine; blaming it sends
    // someone to reword a sentence that is already correct.
    assert.ok(!/named no proof/.test(r.out),
      `a read-only pass was reported as a proofless one:\n${r.out}`);
  });

  test("the proofless case is still reported as the proofless case", noBrowser, async () => {
    // The two branches must stay told apart: this one IS fixable by rewording the test, and the
    // read-only one is not. One message for both would make the actionable half unactionable.
    const plan = path.join(mkdtempSync(path.join(tmpdir(), "smolanalytics-plan-")), "cart.json");
    const r = await run(finishAtOnce(""), { plan, retries: 0 });
    assert.equal(r.code, 0);
    assert.equal(existsSync(plan), false);
    assert.match(r.out, /named no proof/, `the proofless case lost its own diagnosis:\n${r.out}`);
  });
});
