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
    ]);
    assert.equal(plan.steps.length, 2);
    assert.deepEqual(plan.steps[1], { kind: "fill", role: "textbox", name: "Email", text: "a@b.test" });
  });
});
