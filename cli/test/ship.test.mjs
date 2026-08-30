// THE SHIP VERDICT — the question everyone asks and nobody answers.
//
// Every tool in this field answers "did the tests pass". The question asked on a Friday is "can we
// ship", and answering it honestly means publishing what you did NOT check. A vendor whose price
// depends on looking comprehensive will never print that list; our whole identity is already built
// on printing it, so these tests are mostly about one property:
//
//   THE GAPS MUST NEVER BE SILENT. A run that checked 2 of 5 flows and says "2 passed" is the same
//   lie as a suite that quietly skips 38 tests and prints "12 passed". Every reason a flow went
//   unverified — flaky, stale, errored, skipped by --since — has to survive into the artefact,
//   named separately, because the reader's next action differs for each one.
//
// And the one that bit this file on its first run:
//
//   A FAILURE IS NOT A GAP. It was checked, and it failed. That is a bug to fix, not a question
//   still open, and merging the two into one list makes both less actionable.

import { test } from "node:test";
import assert from "node:assert/strict";
import { verdict, unchecked, broken, shipReport, shipText } from "../lib/ship.mjs";

const r = (name, status, mode = "replay") => ({ name, status, mode, ms: 1000 });
const GREEN = [r("a", "passed"), r("b", "passed")];

/* ── the verdict word ────────────────────────────────────────────────────────────────────────── */

test("a failure means do not ship, whatever else is true", () => {
  assert.equal(verdict([r("a", "passed"), r("b", "failed")]), "no");
  // Even buried among many passes — a headline that leads with the passes buries the bug.
  assert.equal(verdict([...Array(50).fill(0).map((_, i) => r(`t${i}`, "passed")), r("bad", "failed")]), "no");
});

test("nothing failed is not the same as nothing wrong", () => {
  // The distinction the whole artefact exists for. Each of these left a flow unverified.
  assert.equal(verdict([r("a", "passed"), r("b", "flaky")]), "partly");
  assert.equal(verdict([r("a", "passed"), r("b", "stale")]), "partly");
  assert.equal(verdict([r("a", "passed"), r("b", "errored")]), "partly");
  assert.equal(
    verdict(GREEN, { used: true, since: "main", skipped: [{ name: "c" }] }),
    "partly",
    "a test skipped by --since is a judgement about the diff, not a result about the app",
  );
});

test("a clean run says the suite has nothing against you, which is a smaller claim than safe", () => {
  assert.equal(verdict(GREEN), "yes");
  const out = shipText(GREEN, { suite: "tests/" });
  assert.match(out, /nothing about the ones nobody has written yet/i, "a green suite must not read as a guarantee");
});

test("no usable result is 'unknown', never 'yes'", () => {
  // The dangerous default: an empty or malformed result set silently reading as a pass.
  assert.equal(verdict([]), "unknown");
  assert.equal(verdict([null, {}, { name: "x" }]), "unknown");
  assert.equal(verdict(undefined), "unknown");
});

/* ── a failure is not a gap ──────────────────────────────────────────────────────────────────── */

test("a failed test is reported as broken, and never as unchecked", () => {
  const results = [r("checkout", "failed"), r("search", "passed")];
  const gaps = unchecked(results);
  assert.deepEqual(gaps.map((g) => g.kind), [], "a failure was checked; listing it as unchecked is a category error");

  const bad = broken(results);
  assert.equal(bad.count, 1);
  assert.deepEqual(bad.tests, ["checkout"]);
  assert.match(bad.why, /your application/, "and it must say whose problem it is");

  const out = shipText(results);
  assert.match(out, /What is broken:/);
  assert.ok(!/What was NOT checked:/.test(out), out);
});

/* ── every gap survives, named and apart ─────────────────────────────────────────────────────── */

test("each kind of gap is kept separate, because the next action differs", () => {
  const results = [r("a", "passed"), r("b", "flaky"), r("c", "stale"), r("d", "errored")];
  const gaps = unchecked(results, { used: true, since: "main", skipped: [{ name: "e" }] });
  assert.deepEqual(gaps.map((g) => g.kind), ["flaky", "stale", "errored", "skipped"]);
  // Collapsing them into "4 not verified" would be true and useless.
  assert.equal(new Set(gaps.map((g) => g.why)).size, 4, "four gaps must not share one sentence");
});

test("no gap is ever silently dropped from the artefact", () => {
  // The property, asserted against the rendered text rather than the intermediate object, because
  // the text is what a person reads and a gap that exists only in a return value is still silent.
  const results = [r("a", "passed"), r("b", "flaky"), r("c", "stale"), r("d", "errored")];
  const out = shipText(results, { selection: { used: true, since: "main", skipped: [{ name: "e" }] } });
  for (const [what, re] of [
    ["flaky", /retry/i],
    ["stale", /stopped fitting/i],
    ["errored", /could not run/i],
    ["skipped", /did not run/i],
  ]) {
    assert.match(out, re, `the ${what} gap vanished from the report:\n${out}`);
  }
});

test("errored says it is our runner and not their application", () => {
  // The fence this whole product is built on, carried into the release artefact.
  const out = shipText([r("a", "passed"), r("b", "errored")]);
  assert.match(out, /this runner, not your application/i, out);
});

test("flaky is never presented as a pass", () => {
  const out = shipText([r("a", "passed"), r("b", "flaky")]);
  assert.match(out, /proves nothing/i, out);
  const report = shipReport([r("a", "passed"), r("b", "flaky")]);
  assert.equal(report.checked, 1, "a flaky test must not be counted among the passed");
});

test("skipped tests are only a gap when selection actually ran", () => {
  // Without --since there is nothing skipped, and inventing a gap would be as dishonest as hiding
  // one. `used: false` is the shape selectSuite returns when no ref was given.
  // THE CASE THAT DISTINGUISHES THE GUARD, and the first version of this test did not have it.
  // Asserting on `skipped: []` proves nothing: with or without the `used` check, an empty list
  // yields no gap. Only a NON-EMPTY skipped list on a selection that never ran can tell the two
  // apart — and selectSuite really does return that shape, since it seeds `skipped` before it knows
  // whether a ref was given.
  assert.deepEqual(
    unchecked(GREEN, { used: false, since: "", skipped: [{ name: "c" }, { name: "d" }] }),
    [],
    "selection that never ran must not invent a gap out of its own scratch state",
  );
  assert.equal(verdict(GREEN, { used: false, since: "", skipped: [{ name: "c" }] }), "yes");
  assert.deepEqual(unchecked(GREEN, { used: false, since: "", skipped: [] }), []);
  assert.deepEqual(unchecked(GREEN, null), []);
});

/* ── it says how many, and against what ──────────────────────────────────────────────────────── */

test("the count is of what actually passed, not of what was attempted", () => {
  const results = [r("a", "passed"), r("b", "failed"), r("c", "stale")];
  const report = shipReport(results, { url: "shop.test" });
  assert.equal(report.checked, 1);
  assert.equal(report.total, 3);
  assert.match(report.lines.join("\n"), /1 test of 3 ran and passed against shop\.test/);
});

test("counts read as English at one and at many", () => {
  assert.match(shipText([r("a", "passed")]), /1 test of 1/);
  assert.match(shipText([r("a", "passed"), r("b", "passed")]), /2 tests of 2/);
});
