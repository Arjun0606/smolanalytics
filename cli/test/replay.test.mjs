// Replay is the half of this product that costs nothing, and it is the half that can lie. A
// recording that runs against the wrong deployment still prints PASS. These are the two ways that
// happens: the recorded URL outliving the preview it was made on, and a plan with no steps.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { rebase, compile } from "../lib/test.mjs";

describe("a recording follows the URL under test", () => {
  const plan = {
    startUrl: "https://shop-git-old-branch.vercel.app/products",
    steps: [
      { kind: "click", role: "link", name: "Cart" },
      { kind: "goto", url: "https://shop-git-old-branch.vercel.app/checkout?step=1" },
      { kind: "goto", url: "https://checkout.stripe.com/pay/abc" },
    ],
  };

  test("the run starts where this run was told to start, not where the recording was made", () => {
    // Otherwise a cached recording tests the previous pull request's deployment and passes green
    // without ever opening the change under review.
    const r = rebase(plan, "https://shop-git-new-branch.vercel.app");
    assert.equal(r.startUrl, "https://shop-git-new-branch.vercel.app");
  });

  test("recorded navigations move to the new preview, keeping their path and query", () => {
    const r = rebase(plan, "https://shop-git-new-branch.vercel.app");
    assert.equal(r.steps[1].url, "https://shop-git-new-branch.vercel.app/checkout?step=1");
  });

  test("a third party is left alone", () => {
    const r = rebase(plan, "https://shop-git-new-branch.vercel.app");
    assert.equal(r.steps[2].url, "https://checkout.stripe.com/pay/abc", "rewriting this would send the test to our own preview instead of the payment provider");
  });

  test("clicks are untouched", () => {
    assert.deepEqual(rebase(plan, "https://x.test").steps[0], plan.steps[0]);
  });

  test("the same origin is a no-op apart from honouring the path given now", () => {
    const r = rebase(plan, "https://shop-git-old-branch.vercel.app/cart");
    assert.equal(r.startUrl, "https://shop-git-old-branch.vercel.app/cart");
    assert.deepEqual(r.steps, plan.steps);
  });

  test("a recording with an unparseable start still runs, from the URL given now", () => {
    const r = rebase({ startUrl: "", steps: [] }, "https://x.test");
    assert.equal(r.startUrl, "https://x.test");
  });

  test("no URL, no rewrite", () => {
    assert.deepEqual(rebase(plan, ""), plan);
  });
});

describe("what gets recorded", () => {
  test("a run with nothing replayable records nothing at all", () => {
    // An empty plan would replay in milliseconds, exercise nothing, and report PASS forever.
    assert.equal(compile("https://x.test", [{ ok: false, action: { kind: "click" } }]), null);
  });

  test("dead ends are dropped, successful steps are kept", () => {
    const plan = compile("https://x.test", [
      { ok: true, action: { kind: "click" }, target: { role: "link", name: "Cart" } },
      { ok: false, action: { kind: "click" }, target: { role: "button", name: "Nope" } },
      { ok: true, action: { kind: "fill", text: "a@b.test" }, target: { role: "textbox", name: "Email" } },
    ], "Order placed");
    assert.equal(plan.steps.length, 2);
    assert.deepEqual(plan.steps[1], { kind: "fill", role: "textbox", name: "Email", text: "a@b.test" });
  });
});

// A RECORDING IS UNTRUSTED INPUT.
//
// compile() refuses to WRITE an empty plan, and that guard was read as covering the whole risk. It
// does not: every recording this product replays in CI is one it READ back, out of an
// actions/cache entry that survived a cancelled job, a rebase, a hand-edit, or a version of this
// CLI that wrote a different shape. Measured against a real Chromium before this existed:
//
//   {"startUrl":"…","steps":[]}       → "PASS — replayed 0 steps, no model calls." exit 0
//   {"startUrl":"…","steps":"nope"}   → "PASS — replayed 4 steps, no model calls." exit 0
//   {"startUrl":"…","steps":[{"kind":"cl   → exit 2, forever: the corrupt file is never replaced
//
// The first two are the worst artefact this codebase can produce — a green verdict on a pull
// request nobody tested. The third reddens a healthy app until somebody clears a cache by hand.
import { readPlan } from "../lib/test.mjs";

describe("a recording is only believed if it can be replayed", () => {
  const good = { startUrl: "https://x.test/", steps: [{ kind: "click", role: "button", name: "Buy" }] };

  test("a real recording is used", () => {
    assert.deepEqual(readPlan(JSON.stringify(good)).plan, good);
  });

  test("no steps is not a passing test, it is no recording", () => {
    const r = readPlan(JSON.stringify({ startUrl: "https://x.test/", steps: [] }));
    assert.equal(r.plan, null);
    assert.match(r.problem, /no steps/i);
  });

  test("steps that are not a list cannot be counted as steps", () => {
    // `"nope".length` is 4, and four unrecognised steps ran as four no-ops and reported PASS.
    assert.equal(readPlan(JSON.stringify({ startUrl: "https://x.test/", steps: "nope" })).plan, null);
  });

  test("a step missing what its kind needs is not replayable", () => {
    // getByRole(undefined) throws inside the try and reads as stale — a rename that never happened.
    for (const step of [{ kind: "click", role: "button" }, { kind: "fill", role: "textbox", name: "Email" }, { kind: "goto" }, { kind: "press" }, { kind: "swim", why: "?" }]) {
      const r = readPlan(JSON.stringify({ startUrl: "https://x.test/", steps: [step] }));
      assert.equal(r.plan, null, JSON.stringify(step));
      assert.match(r.problem, /step 1/);
    }
  });

  test("a truncated file names itself as the problem instead of ending the run", () => {
    const r = readPlan('{"startUrl":"https://x.test/","steps":[{"kind":"cl');
    assert.equal(r.plan, null);
    assert.match(r.problem, /not valid JSON/i);
  });

  test("null, a list, a number: none of them is a recording", () => {
    for (const text of ["null", "[]", "42", '"a"']) assert.equal(readPlan(text).plan, null, text);
  });

  test("the problem is a sentence about the recording, never about the app", () => {
    for (const text of ["null", '{"steps":[]}', '{"steps":"nope"}', "{"]) {
      const { problem } = readPlan(text);
      assert.ok(!/\bfail/i.test(problem), problem);
    }
  });
});
