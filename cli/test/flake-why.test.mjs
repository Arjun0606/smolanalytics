// WHY IT WAS FLAKY — and the one failure mode that makes this worse than saying nothing.
//
// A test goes red, somebody re-runs it, it goes green, they merge. Google's published number says
// they are right to shrug about 84% of the time and wrong the other 16%, and that 16% is where an
// intermittent bug lives until a customer finds it.
//
// So this compares the two runs we are already holding. Everything it can say is arithmetic over
// steps we recorded, which means:
//
//   A WRONG DIAGNOSIS IS WORSE THAN NONE. It either sends somebody hunting a race that does not
//   exist, or tells them to ignore one that does. So there is no sentence without named evidence
//   from BOTH runs, and when the two runs cannot be told apart it says exactly that — which is
//   true, useful, and the thing a hedge would replace with something false.
//
//   NOISE IS NOT A SIGNAL. 8ms against 40ms is a five-fold slowdown and means nothing. A step has
//   to be both materially slower AND slower by enough for a person to notice, or a slow morning
//   turns every run into a race-condition report and the feature gets ignored.

import { test } from "node:test";
import assert from "node:assert/strict";
import { compare, diagnose, flakeNote } from "../lib/flake.mjs";

const step = (kind, name, ms, ok = true) => ({ action: { kind }, target: { role: "button", name }, ok, ms });
const run = (ms, steps) => ({ ms, steps });

/* ── it says nothing when it knows nothing ───────────────────────────────────────────────────── */

test("two runs that cannot be told apart produce no diagnosis", () => {
  const cmp = compare(run(3000, [step("click", "Pay", 200)]), run(3050, [step("click", "Pay", 210)]));
  assert.equal(cmp.indistinguishable, true);
  assert.equal(diagnose(cmp), "", "a hedge here would be a guess dressed as a finding");
});

test("and it says so out loud rather than staying silent", () => {
  // Silence reads as "we did not look". The honest message is "we looked and could not tell".
  const note = flakeNote(run(3000, [step("click", "Pay", 200)]), run(3050, [step("click", "Pay", 210)]));
  assert.match(note, /indistinguishable/i);
  assert.match(note, /Nothing in this run says which/i);
});

/* ── noise is not a signal ───────────────────────────────────────────────────────────────────── */

test("a big ratio on a tiny duration is not a slowdown", () => {
  // 8ms to 40ms is five times slower and completely meaningless.
  const cmp = compare(run(1000, [step("click", "Pay", 8)]), run(1100, [step("click", "Pay", 40)]));
  assert.deepEqual(cmp.slower, []);
  assert.equal(diagnose(cmp), "");
});

test("a big absolute on a small ratio is not a slowdown either", () => {
  // 4.0s to 4.9s is 900ms slower and only 1.2x — a busy machine, not a race.
  const cmp = compare(run(9000, [step("click", "Pay", 4000)]), run(10000, [step("click", "Pay", 4900)]));
  assert.deepEqual(cmp.slower, []);
});

test("both thresholds together do catch a real one", () => {
  const cmp = compare(run(3000, [step("click", "Proceed to checkout", 180)]), run(5200, [step("click", "Proceed to checkout", 2400)]));
  assert.equal(cmp.slower.length, 1);
  const said = diagnose(cmp);
  assert.match(said, /0\.2s the first time and 2\.4s the second/, "the numbers must be in the sentence, or it cannot be checked");
  assert.match(said, /the application waiting on something, not this test/);
});

/* ── the comparison is by identity, not position ─────────────────────────────────────────────── */

test("one extra step early does not make every later step look different", () => {
  // Position-matching would report every step after the insertion as changed, which is a wall of
  // false findings from a single real difference.
  const fail = run(3000, [step("click", "Cart", 100), step("click", "Pay", 200)]);
  const pass = run(3300, [step("click", "Cookies", 90), step("click", "Cart", 110), step("click", "Pay", 210)]);
  const cmp = compare(fail, pass);
  assert.deepEqual(cmp.onlyInPass, ["click:button:Cookies"], "only the genuinely new step is new");
  assert.deepEqual(cmp.onlyInFail, []);
  assert.deepEqual(cmp.slower, [], "the shared steps ran at the same speed and must not be flagged");
});

test("matching by position invents a slowdown that never happened", () => {
  // THE CASE THAT DISTINGUISHES THE TWO STRATEGIES, and the first version of this file did not
  // have it: with an extra step at the front, position-matching lines "Cookies" up against "Cart"
  // and reports 0.05s -> 3.0s, a sixty-fold slowdown of a step that did not exist in the first run.
  // Identity-matching sees Cookies has no counterpart and skips it, which is the truth.
  const fail = run(3000, [step("click", "Cart", 50), step("click", "Pay", 80)]);
  const pass = run(6000, [step("click", "Cookies", 3000), step("click", "Cart", 60), step("click", "Pay", 90)]);
  const cmp = compare(fail, pass);
  assert.deepEqual(
    cmp.slower,
    [],
    `a step with no counterpart in the other run cannot be slower than it: ${JSON.stringify(cmp.slower)}`,
  );
  assert.deepEqual(cmp.onlyInPass, ["click:button:Cookies"], "it is a new step, which is a different finding");
});

test("a retry that got further says the pass does not clear the failure", () => {
  const said = diagnose(compare(
    run(3000, [step("click", "Cart", 120)]),
    run(3400, [step("click", "Cart", 130), step("click", "Pay", 200)]),
  ));
  assert.match(said, /never did/);
  assert.match(said, /does not clear the failure/, "the reader must not read this as reassurance");
});

test("a retry that took a different route says it is at least partly the test", () => {
  const said = diagnose(compare(
    run(3000, [step("click", "Cart", 120), step("click", "Old name", 100)]),
    run(3100, [step("click", "Cart", 120)]),
  ));
  assert.match(said, /the retry did not/);
  assert.match(said, /finding its own way/);
});

/* ── the whole-run signal ────────────────────────────────────────────────────────────────────── */

test("a slowdown spread across every step is reported as the app, not one control", () => {
  // No single step stands out, but the run took three times as long. A per-step check alone would
  // find nothing and say "indistinguishable", which would be wrong.
  const fail = run(1000, [step("click", "A", 100), step("click", "B", 100)]);
  const pass = run(4000, [step("click", "A", 220), step("click", "B", 240)]);
  const cmp = compare(fail, pass);
  assert.equal(cmp.runSlower, true);
  assert.match(diagnose(cmp), /whole app being slower/);
});

test("failed steps are not compared, because they are the agent hunting", () => {
  // A dead end the agent tried and abandoned is not evidence about the application.
  const fail = run(3000, [step("click", "Pay", 200), step("click", "Wrong thing", 10000, false)]);
  const pass = run(3100, [step("click", "Pay", 210)]);
  const cmp = compare(fail, pass);
  assert.deepEqual(cmp.onlyInFail, [], "an abandoned attempt is not a difference in behaviour");
  assert.equal(cmp.indistinguishable, true);
});

test("missing or malformed attempts do not throw", () => {
  // Bookkeeping about a verdict must never be able to change one.
  for (const [a, b] of [[null, null], [{}, {}], [run(0, null), run(0, undefined)], [undefined, run(10, [])]]) {
    assert.doesNotThrow(() => flakeNote(a, b));
  }
});
