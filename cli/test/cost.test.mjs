// WHAT A RUN COST, AND THE CEILING ON THE NEXT ONE.
//
// This exists because of the objection a real buyer has. Not "will this company survive" — nobody
// researches a vendor before paying $19 — but "I am handing my model key to a loop that drives a
// browser on every pull request across a team, and I cannot see what that costs or stop it."
//
// The rules under test, each with the thing it prevents:
//
//   TOKENS ARE ALWAYS TRUE, MONEY IS ONLY EVER SUPPLIED. Token counts come from the API's own
//   `usage` block and cannot be wrong. A dollar figure needs a per-model price, and one this code
//   invented would print beside real measurements as though it were one of them, on the screen
//   someone uses to decide whether they can afford this. A wrong bill estimate is worse than none,
//   because it is believed.
//
//   THE CEILING IS CHECKED BEFORE SPENDING. A cap that reports what was already spent is a receipt.
//
//   BOOKKEEPING NEVER DECIDES A VERDICT. A malformed usage block must not crash a run that has
//   already worked out whether somebody's checkout is broken.

import { test } from "node:test";
import assert from "node:assert/strict";
import { newLedger, record, merge, priceFrom, dollars, money, costLine, priceHint, overBudget, parseMaxCalls } from "../lib/cost.mjs";

const usage = (i, o, extra = {}) => ({ usage: { input_tokens: i, output_tokens: o, ...extra } });

/* ── the ledger counts what the API actually reported ────────────────────────────────────────── */

test("a run that never called the model says so, which is the whole economic argument", () => {
  assert.equal(costLine(newLedger()), "no model calls");
  assert.equal(costLine(newLedger(), { input: 15, output: 75 }), "no model calls", "a replay has no bill even with a price configured");
});

test("tokens accumulate across calls, including the cached ones a bill still charges for", () => {
  const l = newLedger();
  record(l, usage(12_000, 800, { cache_read_input_tokens: 9_430 }));
  record(l, usage(9_000, 405));
  assert.equal(l.calls, 2);
  assert.equal(l.input, 21_000);
  assert.equal(l.output, 1_205);
  assert.equal(l.cacheRead, 9_430);
  assert.match(costLine(l), /2 model calls · 30,430 in \/ 1,205 out/);
});

test("bookkeeping never crashes a run that already has a verdict", () => {
  // A response missing usage, or carrying a shape a later API version introduces. The call still
  // counts — it happened — and nothing throws.
  for (const res of [{}, null, { usage: null }, { usage: "nope" }, { usage: { input_tokens: "many", output_tokens: -5 } }]) {
    const l = newLedger();
    assert.doesNotThrow(() => record(l, res));
    assert.equal(l.calls, 1, `the call itself must still be counted: ${JSON.stringify(res)}`);
    assert.equal(l.input, 0);
    assert.equal(l.output, 0);
  }
});

test("a suite totals its tests without any test knowing it is in one", () => {
  const a = record(newLedger(), usage(100, 10));
  const b = record(newLedger(), usage(250, 40));
  const both = merge(a, b);
  assert.equal(both.calls, 2);
  assert.equal(both.input, 350);
  assert.equal(both.output, 50);
});

/* ── money only when somebody supplied a price ───────────────────────────────────────────────── */

test("no price means no dollar figure anywhere", () => {
  const l = record(newLedger(), usage(1_000_000, 1_000_000));
  assert.equal(priceFrom({}), null);
  assert.equal(dollars(l, null), null);
  assert.ok(!costLine(l, null).includes("$"), costLine(l, null));
  assert.match(priceHint(l, null), /SMOLANALYTICS_PRICE_IN/, "and it says how to get one");
});

test("HALF a price is not a price", () => {
  // The bug this test exists for: Number("") is 0, which is finite and non-negative, so an earlier
  // version returned {input: 15, output: 0} when only one side was set — a bill in which output
  // tokens are free, printed as though it were measured.
  assert.equal(priceFrom({ SMOLANALYTICS_PRICE_IN: "15" }), null);
  assert.equal(priceFrom({ SMOLANALYTICS_PRICE_OUT: "75" }), null);
  assert.equal(priceFrom({ SMOLANALYTICS_PRICE_IN: "free", SMOLANALYTICS_PRICE_OUT: "75" }), null);
  assert.deepEqual(priceFrom({ SMOLANALYTICS_PRICE_IN: "15", SMOLANALYTICS_PRICE_OUT: "75" }), { input: 15, output: 75 });
  // Zero is a real price somebody may genuinely have, and must survive.
  assert.deepEqual(priceFrom({ SMOLANALYTICS_PRICE_IN: "0", SMOLANALYTICS_PRICE_OUT: "0" }), { input: 0, output: 0 });
});

test("the arithmetic is dollars per million tokens, the way a pricing page quotes it", () => {
  const l = record(newLedger(), usage(1_000_000, 1_000_000));
  assert.equal(dollars(l, { input: 15, output: 75 }), 90);
  // Cache reads bill as input: over-stating is the safe direction for somebody budgeting.
  const c = record(newLedger(), usage(0, 0, { cache_read_input_tokens: 1_000_000 }));
  assert.equal(dollars(c, { input: 15, output: 75 }), 15);
});

test("a per-run figure is not rounded into looking free", () => {
  // Two decimal places turns most real runs into "$0.00", which reads as costless and is exactly
  // the wrong belief to leave someone with.
  assert.equal(money(0.0834), "$0.0834");
  assert.notEqual(money(0.004), "$0.00");
  assert.equal(money(12.5), "$12.50");
});

/* ── the ceiling ─────────────────────────────────────────────────────────────────────────────── */

test("the ceiling stops before spending, not after", () => {
  const l = newLedger();
  l.calls = 2;
  assert.equal(overBudget(l, 5), "", "under the ceiling, carry on");
  assert.match(overBudget(l, 2), /--max-calls/, "at the ceiling, stop — the next call would exceed it");
  assert.match(overBudget(l, 1), /--max-calls/);
});

test("no ceiling configured means no ceiling", () => {
  const l = newLedger();
  l.calls = 9_999;
  assert.equal(overBudget(l, 0), "");
  assert.equal(overBudget(l, undefined), "");
});

test("stopping at the ceiling says nothing about the customer's app", () => {
  const l = newLedger();
  l.calls = 3;
  const why = overBudget(l, 3);
  assert.match(why, /Nothing is known about whether the app works/);
  assert.ok(!/\bfail(ed|s)?\b/i.test(why), `a budget we enforced must not read as a verdict: ${why}`);
});

test("a ceiling that is not a number is refused out loud, never coerced", () => {
  // Number("lots") is NaN, and any silent fallback picks a ceiling the person did not ask for —
  // the same rule --retries and --workers follow.
  assert.match(parseMaxCalls("lots").problem, /whole number/);
  assert.match(parseMaxCalls("-1").problem, /whole number/);
  assert.match(parseMaxCalls("2.5").problem, /whole number/);
  assert.equal(parseMaxCalls("40").value, 40);
  assert.equal(parseMaxCalls("0").value, 0, "zero is how you say 'no ceiling'");
  assert.equal(parseMaxCalls(undefined).problem, "", "absent is not an error");
});
