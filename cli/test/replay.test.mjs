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

  test("the run moves to the deployment named now, ON THE PAGE THAT WAS RECORDED", () => {
    // Two requirements, and this test used to encode only the first:
    //
    //   the ORIGIN must come from the URL given now, or a cached recording tests the previous pull
    //   request's deployment and passes green without ever opening the change under review;
    //
    //   the PAGE must come from the recording. This half was missing, and it asserted the bug:
    //   `startUrl` became the bare origin, so a test recorded on /products replayed against /.
    //   MEASURED on a 50-test suite — the first size at which it is visible — every test went
    //   stale, each burning its full 10s locator timeout looking for a control that was never on
    //   the home page, then waking the agent to re-record at full model price. The suite did not
    //   finish in ten minutes. With the page preserved it is 39.5s, 50/50 passed, no model calls.
    const r = rebase(plan, "https://shop-git-new-branch.vercel.app");
    assert.equal(r.startUrl, "https://shop-git-new-branch.vercel.app/products");
  });

  test("one URL for a whole suite does not collapse every test onto one page", () => {
    // The property the 50-test run actually depends on: `--suite` passes ONE url for every test,
    // so if the recorded page did not survive, fifty tests would all replay against the root.
    const a = rebase({ startUrl: "https://old.app/checkout", steps: [] }, "https://new.app");
    const b = rebase({ startUrl: "https://old.app/signup", steps: [] }, "https://new.app");
    assert.equal(a.startUrl, "https://new.app/checkout");
    assert.equal(b.startUrl, "https://new.app/signup");
    assert.notEqual(a.startUrl, b.startUrl, "two tests recorded on different pages must not share one");
  });

  test("query and hash are part of the page, and survive with it", () => {
    const r = rebase({ startUrl: "https://old.app/search?q=boots#results", steps: [] }, "https://new.app");
    assert.equal(r.startUrl, "https://new.app/search?q=boots#results");
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

  test("a path given now does not override the page a recording was made on", () => {
    // The rule, stated once: the URL says WHERE THE APP IS, the recording says WHICH PAGE. This
    // test previously asserted the opposite ("honouring the path given now"), which is only
    // harmless when one URL accompanies one test — and actively wrong for a suite, where the same
    // URL accompanies every test in it.
    const r = rebase(plan, "https://shop-git-old-branch.vercel.app/cart");
    assert.equal(r.startUrl, "https://shop-git-old-branch.vercel.app/products");
    assert.deepEqual(r.steps, plan.steps);
  });

  test("a recording with no page of its own does take the path given now", () => {
    // The one case where the URL's path is all there is to go on.
    const r = rebase({ startUrl: "https://old.app/", steps: [] }, "https://new.app/cart");
    assert.equal(r.startUrl, "https://new.app/cart");
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
