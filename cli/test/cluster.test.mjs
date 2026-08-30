// TWELVE REDS ARE ONE CAUSE — and the grouping that would make this worse than printing a list.
//
// The value is real: a person opens a red build and spends their morning working out that eleven
// failures are one broken deploy and one is a genuine bug. But every property below exists because
// the obvious implementation gets that exact job wrong in a way nobody notices:
//
//   A WRONG GROUP HIDES A BUG. If a real second bug is swept into "these eleven are all the same
//   thing", somebody fixes the deploy, re-runs, and ships the bug. That is strictly worse than the
//   twelve-item list we started with, because the list at least made them look.
//
//   ORDER MUST NOT DECIDE THE ANSWER. Group "everything overlapping the first failure" and the
//   grouping depends on which test the pool happened to run first. Two people looking at the same
//   red build get different reports, and neither can tell.
//
//   PROSE IS NOT EVIDENCE. Two failures that both say "the page showed an error" have nothing in
//   common. Grouping on the model's own words produces confident, wrong groups.

import { test } from "node:test";
import assert from "node:assert/strict";
import { aloneNote, cluster, clusterNote, standalone, touched } from "../lib/cluster.mjs";

const fail = (name, extra = {}) => ({ name, status: "failed", reason: "it broke", ...extra });
const blames = (name, ...files) => fail(name, { suspects: files.map((f) => ({ file: f, evidence: "this PR removed it" })) });

/** A recording on disk, addressed by planPath. */
const plans = (map) => (p) => {
  if (!(p in map)) {
    const e = new Error(`ENOENT: ${p}`);
    e.code = "ENOENT";
    throw e;
  }
  return JSON.stringify(map[p]);
};
const rec = (startUrl, steps) => ({ startUrl, proof: "Order confirmed", steps });

/* ── the strongest signal: they blamed the same file ─────────────────────────────────────────── */

test("failures that independently blamed one file are one cause, named by the file", () => {
  const groups = cluster([
    blames("checkout", "src/PayButton.tsx"),
    blames("cart", "src/PayButton.tsx"),
    blames("refund", "src/PayButton.tsx"),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].cause, "src/PayButton.tsx", "the group is named by what its members share");
  assert.deepEqual(groups[0].tests.sort(), ["cart", "checkout", "refund"]);
  assert.match(groups[0].why, /one change, not 3 bugs/);
});

test("a genuine second bug is NOT swept into the big group", () => {
  // The whole point. Eleven failures from one deploy plus one real bug: if the real bug lands
  // inside the group, somebody fixes the deploy and ships the bug.
  const many = Array.from({ length: 11 }, (_, i) => blames(`t${i}`, "src/api/client.ts"));
  const groups = cluster([...many, blames("the real one", "src/Unrelated.tsx")]);
  const big = groups.find((g) => g.tests.length > 1);
  assert.equal(big.tests.length, 11);
  assert.ok(!big.tests.includes("the real one"), "a failure sharing nothing must never join a group");
  const alone = groups.find((g) => g.signal === "alone");
  assert.deepEqual(alone.tests, ["the real one"]);
});

test("a failure with no suspects at all joins nothing", () => {
  // [] is the common answer from suspect.mjs — it refuses to name a file it cannot connect. An
  // empty set must not read as "matches everything", which is what a naive subset check would do.
  const groups = cluster([blames("a", "src/x.ts"), blames("b", "src/x.ts"), fail("c", { suspects: [] })]);
  const big = groups.find((g) => g.tests.length > 1);
  assert.deepEqual(big.tests.sort(), ["a", "b"]);
  assert.ok(groups.some((g) => g.signal === "alone" && g.tests[0] === "c"));
});

/* ── the answer cannot depend on the order tests ran ─────────────────────────────────────────── */

test("the same failures in any order produce the same grouping", () => {
  // A and B share one file, B and C share another. Seeded on A you get {A,B}; seeded on B you get
  // {A,B,C}. Grouping by the shared FILE rather than by a neighbour removes the ambiguity.
  const a = blames("a", "src/one.ts");
  const b = blames("b", "src/one.ts", "src/two.ts");
  const c = blames("c", "src/two.ts");
  const shape = (rs) => JSON.stringify(cluster(rs).map((g) => [g.cause, [...g.tests].sort()]));
  const orders = [[a, b, c], [c, b, a], [b, a, c], [c, a, b]];
  const shapes = new Set(orders.map(shape));
  assert.equal(shapes.size, 1, `grouping changed with input order: ${[...shapes].join("\n")}`);
});

test("a test lands in exactly one group, never two", () => {
  // b blames both files. Reporting it under both would double-count the build and make the totals
  // in the summary line add up to more failures than there were.
  const groups = cluster([
    blames("a", "src/one.ts"),
    blames("b", "src/one.ts", "src/two.ts"),
    blames("c", "src/two.ts"),
    blames("d", "src/two.ts"),
  ]);
  const seen = groups.flatMap((g) => g.tests);
  assert.equal(seen.length, new Set(seen).size, `a test appears in two groups: ${seen.join(", ")}`);
  assert.equal(seen.length, 4, "every failure is accounted for exactly once");
});

test("the group that explains the most failures is taken first, and comes out first", () => {
  const groups = cluster([
    blames("a", "src/small.ts"),
    blames("b", "src/small.ts"),
    ...Array.from({ length: 5 }, (_, i) => blames(`big${i}`, "src/big.ts")),
  ]);
  assert.equal(groups[0].cause, "src/big.ts");
  assert.equal(groups[0].tests.length, 5);
});

test("taking the biggest cause first changes the answer, not just the order", () => {
  // THE CASE THAT SEPARATES "biggest first" FROM "first one I happened to see", and sorting the
  // output afterwards cannot stand in for it: by then the partition is already decided.
  //   a blames X.  b blames X and Y.  c and d blame Y.
  // Biggest-first takes Y — three failures explained by one file, which is the deliverable — and
  // leaves a on its own. First-seen takes X, gets {a,b}, and then {c,d}: two groups of two, and
  // nobody is told that three of the four failures are one change.
  const groups = cluster([
    blames("a", "src/X.ts"),
    blames("b", "src/X.ts", "src/Y.ts"),
    blames("c", "src/Y.ts"),
    blames("d", "src/Y.ts"),
  ]);
  const biggest = groups.find((g) => g.tests.length > 1);
  assert.equal(biggest.cause, "src/Y.ts");
  assert.deepEqual(biggest.tests.sort(), ["b", "c", "d"], "the group that explains the most must win the members");
  assert.equal(groups.filter((g) => g.tests.length > 1).length, 1, "splitting this into two pairs hides the one real cause");
});

/* ── the recording, not the prose ────────────────────────────────────────────────────────────── */

test("failures whose recordings drive the same control are one cause", () => {
  const readPlan = plans({
    "a.json": rec("https://shop.test/cart", [{ kind: "click", role: "button", name: "Proceed to checkout" }]),
    "b.json": rec("https://shop.test/orders", [{ kind: "click", role: "button", name: "Proceed to checkout" }]),
  });
  const groups = cluster([fail("a", { planPath: "a.json" }), fail("b", { planPath: "b.json" })], { readPlan });
  assert.equal(groups.length, 1);
  assert.equal(groups[0].signal, "control");
  assert.equal(groups[0].cause, "Proceed to checkout");
});

test("failures whose recordings share only a path group on the path", () => {
  const readPlan = plans({
    "a.json": rec("https://shop.test/checkout", [{ kind: "click", role: "button", name: "Pay now" }]),
    "b.json": rec("https://shop.test/checkout", [{ kind: "click", role: "button", name: "Apply coupon" }]),
  });
  const groups = cluster([fail("a", { planPath: "a.json" }), fail("b", { planPath: "b.json" })], { readPlan });
  assert.equal(groups[0].signal, "path");
  assert.equal(groups[0].cause, "/checkout");
});

test("identical failure prose is NOT a cluster", () => {
  // The reason on a failed run is written by the model. Two tests that both say the same thing
  // have said the same thing; they have not shown a shared cause. This is the group that would
  // look most convincing in a PR comment and be wrong most often.
  const same = 'On /a, clicking "Save" showed "Something went wrong."';
  const groups = cluster([
    fail("a", { reason: same, suspects: [] }),
    fail("b", { reason: same, suspects: [] }),
    fail("c", { reason: same, suspects: [] }),
  ]);
  assert.ok(groups.every((g) => g.tests.length === 1), `prose was used as evidence: ${JSON.stringify(groups)}`);
});

test("a control quoted in the failure prose is not a control the test drives", () => {
  // collectFacts() also mines the prose, and weighs it lower for blame. Here it must not count at
  // all: the two recordings touch nothing in common, and only the model's wording overlaps.
  const readPlan = plans({
    "a.json": rec("https://shop.test/one", [{ kind: "click", role: "button", name: "Alpha" }]),
    "b.json": rec("https://shop.test/two", [{ kind: "click", role: "button", name: "Beta" }]),
  });
  const reason = 'clicking "Proceed to checkout" showed "Something went wrong."';
  const groups = cluster(
    [fail("a", { planPath: "a.json", reason }), fail("b", { planPath: "b.json", reason })],
    { readPlan },
  );
  assert.ok(groups.every((g) => g.tests.length === 1), `the prose leaked in as a shared control: ${JSON.stringify(groups)}`);
});

test("the proof text is not a control either", () => {
  // Every recording carries a proof string, and two tests of the same app very often share one.
  // Grouping on it would put half a suite in one bucket on every red build.
  const readPlan = plans({
    "a.json": { startUrl: "https://shop.test/one", proof: "Order confirmed", steps: [{ kind: "click", role: "button", name: "Alpha" }] },
    "b.json": { startUrl: "https://shop.test/two", proof: "Order confirmed", steps: [{ kind: "click", role: "button", name: "Beta" }] },
  });
  const groups = cluster([fail("a", { planPath: "a.json" }), fail("b", { planPath: "b.json" })], { readPlan });
  assert.ok(groups.every((g) => g.tests.length === 1), "the shared proof string became a cause");
});

/* ── only failures, and only when there is something to say ──────────────────────────────────── */

test("stale, flaky, errored and passed are never clustered into somebody's bug", () => {
  const groups = cluster([
    blames("real", "src/x.ts"),
    { name: "s", status: "stale", suspects: [{ file: "src/x.ts", evidence: "e" }] },
    { name: "f", status: "flaky", suspects: [{ file: "src/x.ts", evidence: "e" }] },
    { name: "e", status: "errored", suspects: [{ file: "src/x.ts", evidence: "e" }] },
    { name: "p", status: "passed", suspects: [{ file: "src/x.ts", evidence: "e" }] },
  ]);
  assert.deepEqual(groups.flatMap((g) => g.tests), ["real"], "a status that is not `failed` is not a bug in their app");
});

test("one failure, or none, produces no summary line", () => {
  assert.equal(clusterNote([blames("a", "src/x.ts")]), "");
  assert.equal(clusterNote([]), "");
  assert.equal(clusterNote([{ name: "p", status: "passed" }]), "");
});

test("failures that group into nothing produce no summary line", () => {
  // "12 failures, 12 causes" is what the list already shows. A header that restates the list is
  // the kind people learn to scroll past, and then they scroll past the one that matters.
  const note = clusterNote([blames("a", "src/one.ts"), blames("b", "src/two.ts")]);
  assert.equal(note, "");
});

test("the summary counts what grouped and what did not, and they add up", () => {
  const results = [
    ...Array.from({ length: 11 }, (_, i) => blames(`t${i}`, "src/api.ts")),
    blames("odd", "src/Other.tsx"),
  ];
  const note = clusterNote(results);
  assert.match(note, /11 of these failures group into 1 likely cause, and 1 stands alone\./);
  assert.match(note, /src\/api\.ts/);
});

/* ── the second half: which failure the cause does NOT explain ───────────────────────────────── */

test("the failure that grouped with nothing is named, not just counted", () => {
  // MEASURED on a rendered comment before this existed: "11 group into 1 cause, and 1 stands alone"
  // and then twelve blockquotes to scan to find which. That scan is the job this feature removes.
  const note = clusterNote([
    ...Array.from({ length: 11 }, (_, i) => blames(`t${i}`, "src/api.ts")),
    blames("A shopper can search", "src/SearchBox.tsx"),
  ]);
  assert.match(note, /A shopper can search shared nothing with the rest/);
  assert.match(note, /will not turn it green/, "the consequence, not just the fact");
});

test("standalone failures are named only when something else actually grouped", () => {
  // Twelve failures that all stand alone is the list again, and naming all twelve under a heading
  // that says nothing grouped would be pure noise.
  assert.deepEqual(standalone(cluster([blames("a", "src/one.ts"), blames("b", "src/two.ts")])), []);
  assert.equal(aloneNote(cluster([blames("a", "src/one.ts"), blames("b", "src/two.ts")])), "");
});

test("many loners are capped, and the count is never hidden", () => {
  const note = clusterNote([
    ...Array.from({ length: 4 }, (_, i) => blames(`grp${i}`, "src/api.ts")),
    ...Array.from({ length: 8 }, (_, i) => blames(`odd${i}`, `src/Odd${i}.tsx`)),
  ]);
  assert.match(note, /and 3 more shared nothing/, "8 loners, 5 named, 3 counted");
  assert.ok(!note.includes("odd7"), "past the cap the shortlist would become the list again");
  assert.match(note, /will not turn them green/, "plural when there is more than one");
});

test("when everything really is one thing, nothing is said to stand alone", () => {
  const groups = cluster([blames("a", "src/x.ts"), blames("b", "src/x.ts")]);
  assert.deepEqual(standalone(groups), []);
  assert.equal(aloneNote(groups), "");
});

test("when everything really is one thing, it says so plainly", () => {
  const note = clusterNote([blames("a", "src/x.ts"), blames("b", "src/x.ts"), blames("c", "src/x.ts")]);
  assert.match(note, /^All 3 failures look like one thing\./);
});

/* ── it can never break a run ────────────────────────────────────────────────────────────────── */

test("an unreadable, missing or nonsense recording degrades to no grouping, never a throw", () => {
  const broken = {
    "missing.json": null, // plans() throws ENOENT for a path it does not hold
  };
  for (const readPlan of [
    plans(broken),
    () => "{ not json",
    () => "[]",
    () => "null",
    () => "42",
    () => { throw new Error("EACCES"); },
  ]) {
    assert.doesNotThrow(() => {
      const groups = cluster([fail("a", { planPath: "x.json" }), fail("b", { planPath: "y.json" })], { readPlan });
      assert.ok(groups.every((g) => g.tests.length === 1));
    });
  }
});

test("malformed results cannot throw either", () => {
  for (const rs of [null, undefined, [null], [{}], [{ status: "failed" }], "nope", [{ status: "failed", name: "a", suspects: "no" }]]) {
    assert.doesNotThrow(() => clusterNote(rs), `threw on ${JSON.stringify(rs)}`);
  }
});

test("touched() reads a recording without a plan path as knowing nothing", () => {
  assert.deepEqual(touched({}), { controls: [], paths: [] });
  assert.deepEqual(touched(null), { controls: [], paths: [] });
});
