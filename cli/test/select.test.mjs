// DIFF-AWARE TEST SELECTION (`--since <ref>`), and the one direction it is not allowed to be wrong in.
//
// Running a test the change could not have broken costs seconds. SKIPPING a test the change DID
// break ships a regression under a green tick. So almost every assertion in this file is of the
// shape "this test STILL RAN", and the mutation that would make each of them fail is the same
// mutation a plausible optimisation would introduce: treat an unknown as a skip.
//
// WHAT IS MEASURED RATHER THAN READ BACK. The end-to-end case counts requests AT THE SERVER: the
// skipped test's recording navigates to /pricing, so if /pricing is never requested, that test
// genuinely did not run. Asserting on our own transcript would only prove that we printed what we
// decided to print.
//
// Every git repository here is a real one — init, commit, branch, edit, clone --depth 1 — because
// the failures this feature has to survive (a shallow CI clone, a missing base ref, no git binary
// at all) exist only in real git.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync, spawn } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  parseSince, changedSince, factsFor, selectSuite, namesake, pathSegments, wordSegments,
  selectionNote, selectionHeadline, selectionCommentLines, selectionCommentDetail, selectionTerminalLines, selectionTailLines, CAVEAT,
} from "../lib/select.mjs";
import { discover, summarize, exitCode, commentBody, suiteCmd, slug } from "../lib/suite.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(here, "..", "bin", "smolanalytics.mjs");
const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-select-"));
const plain = (s) => String(s).replace(/\x1b\[[0-9;]*m/g, "");

const sh = (cwd, ...args) => {
  const r = spawnSync("git", ["-c", "user.email=t@t.test", "-c", "user.name=t", "-c", "commit.gpgsign=false", ...args], { cwd, encoding: "utf8" });
  assert.equal(r.status, 0, `git ${args.join(" ")} failed: ${r.stderr}`);
  return r.stdout;
};

// ---- one fixture, used by almost everything ------------------------------------------------------

/**
 * A repo with a small app, three tests and two recordings, committed on `main` and branched to `pr`.
 *
 * THE NAMES ARE CHOSEN SO THE TWO SELECTION RULES CANNOT COVER FOR EACH OTHER. The cart test is
 * named "the cart checks out" in tests/cart.md, so its words are {cart, checks, out} and it shares
 * nothing with src/Checkout.jsx — the only thing that can select it there is the recorded string
 * it clicks. The namesake rule gets its own fixture below, with no string in common at all.
 *
 * `startUrl` is "/" on purpose: lib/suspect.mjs ignores the root path (every app has one), so the
 * cart recording contributes exactly two facts, the clicked label and the proof.
 */
function makeRepo({ url = "http://app.test/", extra = {} } = {}) {
  const repo = scratch();
  sh(repo, "init", "-q", "-b", "main");
  mkdirSync(path.join(repo, "src"), { recursive: true });
  mkdirSync(path.join(repo, "tests"), { recursive: true });
  mkdirSync(path.join(repo, "recordings"), { recursive: true });
  writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Proceed to checkout</button>;\n");
  writeFileSync(path.join(repo, "src", "Faq.jsx"), "export const F = () => <p>Frequently asked</p>;\n");
  writeFileSync(path.join(repo, "tests", "cart.md"), "## the cart checks out\n\nClick Proceed to checkout and confirm the order is placed.\n");
  writeFileSync(path.join(repo, "tests", "money.md"), "## the plans page shows a monthly figure\n\nOpen the plans page and confirm a monthly figure is shown.\n");
  writeFileSync(path.join(repo, "tests", "fresh.md"), "## never recorded before\n\nDo something nobody has a recording for.\n");
  writeFileSync(path.join(repo, "recordings", "cart--the-cart-checks-out.json"), JSON.stringify({
    startUrl: url, steps: [{ kind: "click", role: "button", name: "Proceed to checkout" }], proof: "Order placed", engine: "chromium",
  }) + "\n");
  writeFileSync(path.join(repo, "recordings", "money--the-plans-page-shows-a-monthly-figure.json"), JSON.stringify({
    startUrl: `${url}pricing`, steps: [{ kind: "goto", url: `${url}pricing` }], proof: "$19 per month", engine: "chromium",
  }) + "\n");
  // Anything a single test needs in the BASE commit, so that the pull request branch below is the
  // only place a change lives. Written before the commit, never after the branch — a file created
  // on `pr` and then branched away from `main` is a fixture that silently tests nothing.
  for (const [f, body] of Object.entries(extra)) {
    mkdirSync(path.dirname(path.join(repo, f)), { recursive: true });
    writeFileSync(path.join(repo, f), body);
  }
  sh(repo, "add", ".");
  sh(repo, "commit", "-q", "-m", "the app");
  sh(repo, "checkout", "-q", "-b", "pr");
  return repo;
}

const suiteOf = (repo) => discover(path.join(repo, "tests"), path.join(repo, "recordings")).tests;
const ids = (list) => list.map((t) => t.id).sort();
const skippedIds = (sel) => sel.skipped.map((t) => t.id).sort();

const CART = "cart--the-cart-checks-out";
const MONEY = "money--the-plans-page-shows-a-monthly-figure";
const FRESH = "fresh--never-recorded-before";

test("the fixture really produces the three tests these assertions name", () => {
  const repo = makeRepo();
  assert.deepEqual(ids(suiteOf(repo)), [CART, FRESH, MONEY].sort());
});

// ---- the mapping ---------------------------------------------------------------------------------

describe("what a change selects, against a real git repository", () => {
  test("changing the string a recorded test clicks selects that test, and only it", () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue to payment</button>;\n");
    sh(repo, "commit", "-qam", "rename the button");

    const tests = suiteOf(repo);
    const sel = selectSuite({ tests, since: "main", cwd: repo });
    assert.equal(sel.used, true, `selection was abandoned: ${sel.reason}`);
    // The cart test, because the diff removed the label it clicks. The fresh one, because there is
    // no recording and therefore nothing known about it. NOT the plans test.
    assert.deepEqual(ids(sel.selected), [CART, FRESH].sort());
    assert.deepEqual(skippedIds(sel), [MONEY]);
    // The evidence is the contract's, not a restatement of the decision.
    const why = sel.picked.find((p) => p.id === CART).why;
    assert.equal(why, 'src/Checkout.jsx: this PR removed the string "Proceed to checkout" this test clicks');
  });

  test("changing a file no test exercises still runs everything without a recording", () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Faq.jsx"), "export const F = () => <p>Questions we get</p>;\n");
    sh(repo, "commit", "-qam", "reword the faq");

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, sel.reason);
    // The one test with nothing on disk about it is the one thing that MUST survive a change that
    // matches nothing: we do not know what it touches, so we cannot call it unrelated.
    assert.deepEqual(ids(sel.selected), [FRESH]);
    assert.deepEqual(skippedIds(sel), [CART, MONEY].sort());
    assert.match(sel.picked.find((p) => p.id === FRESH).why, /no recording/);
  });

  test("a test's recorded route selects it when the diff moves that path", () => {
    const repo = makeRepo({ extra: { "src/Router.jsx": 'export const routes = ["/pricing", "/faq"];\n' } });
    writeFileSync(path.join(repo, "src", "Router.jsx"), 'export const routes = ["/plans", "/faq"];\n');
    sh(repo, "commit", "-qam", "rename the route");

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, sel.reason);
    assert.ok(ids(sel.selected).includes(MONEY), `the test that visits /pricing was skipped by a diff that removed "/pricing": ${JSON.stringify(sel.skipped)}`);
    assert.deepEqual(skippedIds(sel), [CART]);
    // Pinned to the rule that is supposed to have fired. src/Router.jsx shares no word with this
    // test's name, so if the reason ever reads "shares" the route rule quietly stopped working and
    // the namesake rule covered for it.
    assert.equal(sel.picked.find((p) => p.id === MONEY).why, 'src/Router.jsx: this PR removed "/pricing", a path this test visited');
  });

  test("an UNCOMMITTED edit is part of the change, or a developer's own edit is invisible to selection", () => {
    // The catastrophic shape, and the one an implementation reaches for first: diffing base...HEAD
    // sees only commits, so an edit you have not committed yet gets its test skipped as unrelated.
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue to payment</button>;\n");
    assert.match(sh(repo, "status", "--porcelain"), /^ M src\/Checkout\.jsx$/m, "the fixture committed the edit, so this proves nothing");

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, `selection was abandoned rather than seeing the edit: ${sel.reason}`);
    assert.ok(ids(sel.selected).includes(CART), "an uncommitted edit to the file this test clicks did not select it");
    assert.deepEqual(skippedIds(sel), [MONEY], "nothing was judged at all, so 'the cart test ran' proves nothing");
  });

  test("a STAGED-only edit counts too", () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue to payment</button>;\n");
    sh(repo, "add", "src/Checkout.jsx");
    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    // `used` first, and the skip list after: "the cart test ran" is also true of a run that gave up
    // on selection entirely, and an assertion that cannot tell those apart is not an assertion.
    assert.equal(sel.used, true, `selection was abandoned rather than seeing the staged edit: ${sel.reason}`);
    assert.ok(ids(sel.selected).includes(CART), "a staged-but-uncommitted edit did not select the test it breaks");
    assert.deepEqual(skippedIds(sel), [MONEY]);
  });

  test("a brand new untracked route file selects the test that visits that path", () => {
    const repo = makeRepo();
    mkdirSync(path.join(repo, "app", "pricing"), { recursive: true });
    writeFileSync(path.join(repo, "app", "pricing", "page.tsx"), "export default () => <p>New plans</p>;\n");
    assert.match(sh(repo, "status", "--porcelain"), /^\?\? app\//m, "the fixture tracked the file, so this proves nothing");

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, sel.reason);
    // app/ and page are framework scaffolding and never match; "pricing" is the segment that does,
    // and it is the one the recording's route is named after.
    assert.ok(ids(sel.selected).includes(MONEY), `an untracked new /pricing route did not select the test that visits /pricing: ${JSON.stringify(sel.picked)}`);
    assert.equal(sel.picked.find((p) => p.id === MONEY).why, "app/pricing/page.tsx: its path matches /pricing, a page this test visited");
    // And it is a decision, not a blanket: the cart test is named after nothing in that path.
    assert.deepEqual(skippedIds(sel), [CART]);
  });

  test("a test named after a changed file is run even with no string in common", () => {
    // The convention nothing textual can see: tests/cart.md and src/cart/Total.tsx are the same
    // feature under two naming schemes, and the changed line shares no word with any recording.
    const repo = makeRepo({ extra: { "src/cart/Total.tsx": "export const RATE = 0.2;\n" } });
    writeFileSync(path.join(repo, "src", "cart", "Total.tsx"), "export const RATE = 0.3;\n");
    sh(repo, "commit", "-qam", "bump the rate");

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, sel.reason);
    assert.ok(ids(sel.selected).includes(CART), `nothing textual connects "const RATE" to this test, and its own name does: ${JSON.stringify(sel)}`);
    assert.match(sel.picked.find((p) => p.id === CART).why, /shares "cart"/);
    // And the rule is a rule, not a blanket: the plans test is not named after this file.
    assert.deepEqual(skippedIds(sel), [MONEY]);
  });

  test("a file only git can call changed — a binary — skips the recorded tests rather than abandoning", () => {
    // Stated so nobody is surprised by it: a PNG cannot take away a string a test clicks, so the
    // recorded tests really are unrelated to it. Its PATH still participates, which is why this
    // image is named after nothing.
    const repo = makeRepo({ extra: { "media/banner.bin": Buffer.from([0, 1, 2, 3, 255, 0, 7]) } });
    writeFileSync(path.join(repo, "media", "banner.bin"), Buffer.from([9, 9, 9, 0, 1, 2, 3]));
    sh(repo, "commit", "-qam", "new art");

    const { files, problem } = changedSince({ since: "main", cwd: repo });
    assert.equal(problem, "", "a binary change must not abandon selection: it is a file we CAN read everything relevant about");
    assert.deepEqual(files.map((f) => f.file), ["media/banner.bin"]);
    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.deepEqual(ids(sel.selected), [FRESH], "the test with no recording still runs");
    assert.deepEqual(skippedIds(sel), [CART, MONEY].sort());
  });

  test("a rename keeps BOTH names, so a route named after the old path still selects", () => {
    const repo = makeRepo({ extra: { "src/pricing/Table.tsx": "export const T = () => <table/>;\n" } });
    sh(repo, "mv", "src/pricing/Table.tsx", "src/Table.tsx");
    sh(repo, "commit", "-qm", "flatten");

    const { files, problem } = changedSince({ since: "main", cwd: repo });
    assert.equal(problem, "", "a pure rename must not abandon selection");
    assert.ok(files.some((f) => f.file === "src/pricing/Table.tsx"), `the old path is the only name this change is known by: ${JSON.stringify(files.map((f) => f.file))}`);
    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.ok(ids(sel.selected).includes(MONEY), "a directory named after a visited route was renamed away and the test that visits it was skipped");
  });
});

// ---- everything we do not know runs ---------------------------------------------------------------

describe("every unknown runs the test", () => {
  test("a recording that is not JSON at all", () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Faq.jsx"), "export const F = () => <p>Questions we get</p>;\n");
    sh(repo, "commit", "-qam", "reword the faq");
    // Corrupted on the test that this change WOULD otherwise skip — corrupting the selected one
    // would prove nothing at all.
    writeFileSync(path.join(repo, "recordings", `${MONEY}.json`), '{"startUrl": "http://app.test/pri');

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.equal(sel.used, true, sel.reason);
    assert.ok(ids(sel.selected).includes(MONEY), "an unreadable recording was treated as evidence of anything");
    assert.match(sel.picked.find((p) => p.id === MONEY).why, /not readable JSON/);
    assert.deepEqual(skippedIds(sel), [CART], "and the ones we CAN read are still judged");
  });

  test("a recording that is valid JSON of the wrong shape, and one that names nothing", () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Faq.jsx"), "export const F = () => <p>Questions we get</p>;\n");
    sh(repo, "commit", "-qam", "reword the faq");
    writeFileSync(path.join(repo, "recordings", `${MONEY}.json`), "[1,2,3]");
    writeFileSync(path.join(repo, "recordings", `${CART}.json`), '{"startUrl":"http://app.test/","steps":[],"proof":""}');

    const sel = selectSuite({ tests: suiteOf(repo), since: "main", cwd: repo });
    assert.deepEqual(ids(sel.selected), [CART, FRESH, MONEY].sort(), "a recording we cannot get facts out of is not evidence of unrelatedness");
    assert.deepEqual(sel.skipped, []);
    assert.match(sel.picked.find((p) => p.id === MONEY).why, /shape a recording has/);
    assert.match(sel.picked.find((p) => p.id === CART).why, /names no control, text or path/);
  });

  test("a recording file that cannot be opened at all", () => {
    const tests = [{ id: "a", name: "a", file: "a.md", test: "a", planPath: "/does/not/exist/a.json" },
      { id: "b", name: "b", file: "b.md", test: "b", planPath: "/does/not/exist/b.json" }];
    const sel = selectSuite({
      tests, since: "main", cwd: "/",
      changed: () => ({ files: [{ file: "src/x.ts", removed: ["const a = 1;"], added: ["const a = 2;"] }], problem: "" }),
    });
    assert.equal(sel.used, true);
    assert.equal(sel.selected.length, 2);
    assert.deepEqual(sel.skipped, []);
  });

  test("factsFor tells a missing recording apart from a broken one, because the sentences differ", () => {
    assert.match(factsFor({ planPath: "/nope/x.json" }).why, /no recording yet/);
    assert.match(factsFor({ planPath: "/nope/x.json" }, () => { const e = new Error("EACCES"); e.code = "EACCES"; throw e; }).why, /could not be read/);
    assert.equal(factsFor({}).known, false);
  });
});

// ---- everything we cannot compute runs the whole suite ---------------------------------------------

describe("a diff we cannot compute runs the whole folder, and says why", () => {
  const everythingRan = (sel, tests) => {
    assert.equal(sel.used, false, "selection was used on a diff it could not compute");
    assert.equal(sel.selected.length, tests.length);
    assert.deepEqual(sel.skipped, [], "a test was skipped on the strength of a diff that does not exist");
    assert.ok(sel.reason.length > 10, `no reason was given: ${JSON.stringify(sel.reason)}`);
    assert.match(plain(selectionNote(sel)), /All \d+ tests? ran\./);
  };

  test("no git binary on PATH", () => {
    // The child is the only honest way: spawnSync inherits this process's PATH.
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue</button>;\n");
    sh(repo, "commit", "-qam", "rename");
    const r = spawnSync(process.execPath, ["-e", `
      Promise.all([
        import(${JSON.stringify(new URL("../lib/select.mjs", import.meta.url).href)}),
        import(${JSON.stringify(new URL("../lib/suite.mjs", import.meta.url).href)}),
      ]).then(([L, S]) => {
        const tests = S.discover(${JSON.stringify(path.join(repo, "tests"))}, ${JSON.stringify(path.join(repo, "recordings"))}).tests;
        const sel = L.selectSuite({ tests, since: "main", cwd: ${JSON.stringify(repo)} });
        console.log(JSON.stringify({ used: sel.used, reason: sel.reason, selected: sel.selected.length, skipped: sel.skipped.length, total: sel.total }));
      });`], { env: { ...process.env, PATH: "/nonexistent-bin" }, encoding: "utf8" });
    assert.equal(r.status, 0, `it must not crash: ${r.stderr}`);
    const out = JSON.parse(r.stdout);
    assert.deepEqual(out, { used: false, reason: "git is not on PATH here, so there is no diff to select from", selected: 3, skipped: 0, total: 3 });
  });

  test("not a git repository at all", () => {
    const tests = suiteOf(makeRepo());
    const dir = scratch();
    rmSync(path.join(dir), { recursive: true, force: true });
    mkdirSync(dir, { recursive: true });
    const sel = selectSuite({ tests, since: "main", cwd: dir });
    everythingRan(sel, tests);
    assert.match(sel.reason, /not inside a git repository/);
  });

  test("a base ref this clone never fetched — the shallow single-branch checkout CI produces", () => {
    const origin = makeRepo();
    writeFileSync(path.join(origin, "src", "Checkout.jsx"), "export const B = () => <button>Continue</button>;\n");
    sh(origin, "commit", "-qam", "rename");
    const into = path.join(scratch(), "shallow");
    const cloned = spawnSync("git", ["clone", "-q", "--depth", "1", "--branch", "pr", `file://${origin}`, into], { encoding: "utf8" });
    assert.equal(cloned.status, 0, cloned.stderr);
    assert.equal(spawnSync("git", ["rev-parse", "--verify", "--quiet", "main^{commit}"], { cwd: into }).status, 1,
      "the clone has main after all, so this is not the case it claims to be");

    const tests = discover(path.join(into, "tests"), path.join(into, "recordings")).tests;
    assert.equal(tests.length, 3, "the clone did not bring the suite with it");
    const sel = selectSuite({ tests, since: "main", cwd: into });
    everythingRan(sel, tests);
    assert.match(sel.reason, /shallow or single-branch/);
  });

  test("a base ref that exists but shares no history with HEAD", () => {
    const repo = makeRepo();
    sh(repo, "checkout", "-q", "--orphan", "unrelated");
    writeFileSync(path.join(repo, "OTHER.md"), "nothing to do with the app\n");
    sh(repo, "add", "OTHER.md");
    sh(repo, "commit", "-qm", "an unrelated history");
    sh(repo, "checkout", "-q", "pr");
    assert.equal(spawnSync("git", ["merge-base", "unrelated", "HEAD"], { cwd: repo }).status, 1, "the two branches share history, so this proves nothing");

    const tests = suiteOf(repo);
    const sel = selectSuite({ tests, since: "unrelated", cwd: repo });
    everythingRan(sel, tests);
    assert.match(sel.reason, /no common commit/);
  });

  test("nothing changed since the ref — far more often a wrong ref than an empty pull request", () => {
    const repo = makeRepo();
    const tests = suiteOf(repo);
    const sel = selectSuite({ tests, since: "main", cwd: repo });
    everythingRan(sel, tests);
    assert.match(sel.reason, /nothing has changed since main/);
  });

  test("a changed file the patch does not contain — a truncated diff — abandons selection", () => {
    // The silent one. git lists the file, the patch does not carry it, and a stub with no lines
    // makes every test look unrelated to it. Simulated with an injected runner because a real
    // truncation only happens on a diff far too large to build in a test.
    const tests = suiteOf(makeRepo());
    const run = (args) => {
      const a = args.join(" ");
      if (a === "--version") return "git version 2.0.0";
      if (a.startsWith("rev-parse --is-inside-work-tree")) return "true";
      if (a.startsWith("rev-parse --verify")) return "abc123";
      if (a.startsWith("merge-base")) return "abc123\n";
      if (a.startsWith("diff --name-only")) return "src/Checkout.jsx\nsrc/Faq.jsx\n";
      if (a.startsWith("diff -M")) return "diff --git a/src/Faq.jsx b/src/Faq.jsx\n@@ -1 +1 @@\n-a\n+b\n";
      if (a.startsWith("ls-files")) return "";
      throw new Error(`unexpected git call: ${a}`);
    };
    const sel = selectSuite({ tests, since: "main", cwd: "/", run });
    everythingRan(sel, tests);
    assert.match(sel.reason, /could not be read out of the diff \(src\/Checkout\.jsx\)/);
  });

  test("a diff too large to reason about test by test", () => {
    const tests = suiteOf(makeRepo());
    const big = ["diff --git a/src/vendor.js b/src/vendor.js", "@@ -1,3 +1,3 @@"];
    for (let i = 0; i < 40; i++) big.push(`-old ${i}`, `+new ${i}`);
    const run = (args) => {
      const a = args.join(" ");
      if (a === "--version") return "git version 2.0.0";
      if (a.startsWith("rev-parse")) return "abc123";
      if (a.startsWith("merge-base")) return "abc123\n";
      if (a.startsWith("diff --name-only")) return "src/vendor.js\n";
      if (a.startsWith("diff -M")) return big.join("\n");
      if (a.startsWith("ls-files")) return "";
      throw new Error(`unexpected git call: ${a}`);
    };
    assert.equal(changedSince({ since: "main", cwd: "/", run }).problem, "", "the guard fired at the default limit, so the small case below proves nothing");
    const sel = selectSuite({ tests, since: "main", cwd: "/", run, limits: { maxFiles: 20_000, maxLines: 10 } });
    everythingRan(sel, tests);
    assert.match(sel.reason, /80 lines, too large/);
    const many = selectSuite({ tests, since: "main", cwd: "/", run, limits: { maxFiles: 0, maxLines: 100_000 } });
    everythingRan(many, tests);
    assert.match(many.reason, /files differ from .*too many/);
  });

  test("a bug inside selection itself costs a slower run and nothing else", () => {
    const tests = suiteOf(makeRepo());
    const sel = selectSuite({ tests, since: "main", cwd: "/", changed: () => { throw new Error("boom"); } });
    everythingRan(sel, tests);
    assert.match(sel.reason, /boom/);
  });

  test("a ref whose diff succeeds but whose facts throw still runs everything", () => {
    const tests = suiteOf(makeRepo());
    const sel = selectSuite({
      tests, since: "main", cwd: "/",
      changed: () => ({ files: [{ file: "src/x.ts", removed: ["Proceed to checkout"], added: [] }], problem: "" }),
      readPlan: () => { throw Object.assign(new Error("disk on fire"), { code: "EIO" }); },
    });
    assert.equal(sel.used, true);
    assert.equal(sel.selected.length, tests.length, "a recording we could not read is not evidence");
  });
});

// ---- default OFF ---------------------------------------------------------------------------------

describe("without --since nothing changes", () => {
  test("selectSuite hands back the very array it was given, and calls no git at all", () => {
    const tests = suiteOf(makeRepo());
    let called = 0;
    const sel = selectSuite({ tests, run: () => { called++; return null; } });
    assert.equal(sel.selected, tests, "the run must be the run it would have been, not a copy of it");
    assert.equal(sel.used, false);
    assert.equal(sel.since, "");
    assert.deepEqual(sel.skipped, []);
    assert.equal(called, 0, "no --since must mean no subprocess");
    assert.deepEqual(selectionTerminalLines(sel), []);
    assert.deepEqual(selectionCommentLines(sel), []);
    assert.deepEqual(selectionCommentDetail(sel), []);
    assert.deepEqual(selectionTailLines(sel), []);
    assert.equal(selectionHeadline(sel), "");
    assert.equal(selectionNote(sel), "");
  });
});

// ---- the flag ------------------------------------------------------------------------------------

describe("--since is refused rather than defaulted", () => {
  test("absent is silence; bare, empty and whitespace are refusals", () => {
    assert.deepEqual(parseSince(undefined), { since: "", problem: "" });
    assert.match(parseSince("").problem, /--since needs a ref/);
    assert.match(parseSince("   ").problem, /--since needs a ref/);
    assert.match(parseSince("my branch").problem, /whitespace/);
  });

  test("a ref beginning with a dash is refused, because git would read it as an option", () => {
    for (const bad of ["-x", "--upload-pack=touch /tmp/pwned", "-c"]) {
      const { since, problem } = parseSince(bad);
      assert.equal(since, "", `${bad} was accepted as a ref`);
      assert.match(problem, /starts with a dash/);
    }
  });

  test("an ordinary ref survives, whitespace trimmed", () => {
    assert.deepEqual(parseSince(" origin/main "), { since: "origin/main", problem: "" });
    assert.deepEqual(parseSince("v1.2.3"), { since: "v1.2.3", problem: "" });
  });

  test("origin/<ref> is tried when the bare name is not here, which is what CI has", () => {
    const origin = makeRepo();
    writeFileSync(path.join(origin, "src", "Checkout.jsx"), "export const B = () => <button>Continue</button>;\n");
    sh(origin, "commit", "-qam", "rename");
    const into = path.join(scratch(), "clone");
    assert.equal(spawnSync("git", ["clone", "-q", `file://${origin}`, into], { encoding: "utf8" }).status, 0);
    sh(into, "checkout", "-q", "--detach");
    sh(into, "branch", "-q", "-D", "pr");
    assert.equal(spawnSync("git", ["rev-parse", "--verify", "--quiet", "main^{commit}"], { cwd: into }).status, 1,
      "a local main still exists, so the origin/ fallback is not what is being proved");

    const tests = discover(path.join(into, "tests"), path.join(into, "recordings")).tests;
    const sel = selectSuite({ tests, since: "main", cwd: into });
    assert.equal(sel.used, true, sel.reason);
    assert.ok(ids(sel.selected).includes(CART));
    assert.deepEqual(skippedIds(sel), [MONEY]);
  });
});

// ---- the segment rules, on their own -------------------------------------------------------------

describe("the segment rules", () => {
  test("scaffolding never names a feature", () => {
    assert.deepEqual([...pathSegments("src/components/Checkout.tsx")], ["checkout"]);
    assert.deepEqual([...pathSegments("app/pricing/page.tsx")], ["pricing"]);
    assert.deepEqual([...pathSegments("app/2/index.ts")], []);
    // "the" survives: it is three characters and there is no English stop list here. It would only
    // ever match a directory literally named `the`, and over-selecting costs seconds.
    assert.deepEqual([...wordSegments("tests/cart.md the cart checks out")], ["cart", "the", "checks", "out"]);
    assert.deepEqual([...wordSegments("(--test)")], []);
  });

  test("namesake matches a whole segment, never a prefix of one", () => {
    const t = { file: "tests/cart.md", name: "the cart checks out" };
    assert.equal(namesake(t, [{ file: "src/cart/Total.tsx" }]).seg, "cart");
    assert.equal(namesake(t, [{ file: "src/cartography/Map.tsx" }]), null, '"cart" is not "cartography"');
    assert.equal(namesake({ file: "a.md", name: "x" }, [{ file: "src/cart/Total.tsx" }]), null);
  });
});

// ---- the accounting ------------------------------------------------------------------------------

describe("a skipped test is not a passed test", () => {
  const ran = [
    { id: "a", name: "the cart checks out", file: "tests/cart.md", test: "s", status: "passed", mode: "replay", reason: "ok", ms: 500, layout: [], suspects: [] },
    { id: "b", name: "never recorded before", file: "tests/fresh.md", test: "s", status: "passed", mode: "agent", reason: "ok", ms: 900, layout: [], suspects: [] },
  ];
  const sel = {
    since: "main", used: true, reason: "", total: 5, selected: [{}, {}], picked: [],
    skipped: [
      { id: "c", name: "the plans page shows a monthly figure", file: "tests/money.md" },
      { id: "d", name: "the faq loads", file: "tests/faq.md" },
      { id: "e", name: "search returns results", file: "tests/search.md" },
    ],
  };

  test("the counts are counts of what ran", () => {
    const s = summarize(ran);
    assert.equal(s.total, 2, "a skipped test reached the summary");
    assert.equal(s.passed, 2);
    assert.equal(s.failed + s.stale + s.errored + s.flaky, 0);
  });

  test("the exit code is decided by what ran, and skipping alone never moves it", () => {
    assert.equal(exitCode(ran), 0);
    const withFailure = [...ran, { ...ran[0], id: "z", status: "failed" }];
    assert.equal(exitCode(withFailure), 1, "a real failure among the tests that ran is still a 1");
    // And the skipped list is not an input to it at all.
    assert.equal(exitCode(ran.concat(sel.skipped.map((t) => ({ ...t, status: "passed", ms: 0 })))) , 0);
  });

  test("the comment headline names the skips, and no skipped test gets a row", () => {
    const body = commentBody(ran, { url: "http://app.test/", suite: "tests", selection: sel });
    assert.match(body, /\*\*2 passed · 3 skipped\*\*/, `the headline hides the skips:\n${body.split("\n").slice(0, 4).join("\n")}`);
    assert.match(body, /2 of 5 tests ran; 3 skipped because this change touches no file they exercise \(`--since main`\)\./);
    // Two rows in the table, not five. A skipped test rendered as a row is a green tick over
    // something nobody looked at.
    const rows = body.split("\n").filter((l) => /^\| (pass|\*\*fail\*\*|stale|error|flaky) \|/.test(l));
    assert.equal(rows.length, 2, `the table has ${rows.length} verdict rows for 2 tests that ran:\n${rows.join("\n")}`);
    assert.ok(!rows.some((r) => r.includes("monthly figure")), "a skipped test was rendered as a verdict");
    // Named, all of them, and told what a skip means.
    for (const t of sel.skipped) assert.ok(body.includes(t.name), `${t.name} was skipped and never named`);
    assert.ok(body.includes(CAVEAT), "the comment oversold the selection by omitting what it cannot see");
  });

  test("the note survives the two shrink paths a long comment takes", () => {
    // A body over the limit is rebuilt without layout notes, then without suspects, then cut on a
    // line boundary. The skip sentence must not be what falls off — it is the sentence that says
    // the numbers above it are not the whole folder.
    const many = Array.from({ length: 60 }, (_, i) => ({
      id: `t${i}`, name: `test ${i}`, file: "tests/x.md", test: "s", status: "failed", mode: "agent",
      reason: `a very long verdict. ${"x".repeat(3000)}`, ms: 1000,
      layout: [{ note: "y".repeat(200) }], suspects: [{ file: "src/a.ts", evidence: "z".repeat(200) }],
    }));
    const body = commentBody(many, { url: "http://app.test/", suite: "tests", selection: sel });
    assert.ok(body.length <= 65_000, `the body is ${body.length} characters`);
    assert.match(body, /3 skipped/);
    assert.match(body, /2 of 5 tests ran/);
  });

  test("with nothing skipped the comment still says so, and adds nothing to the headline", () => {
    const none = { ...sel, skipped: [] };
    const body = commentBody(ran, { url: "http://app.test/", suite: "tests", selection: none });
    assert.match(body, /\*\*2 passed\*\*/);
    assert.ok(!/skipped\b/.test(body.split("\n")[1]), "an empty skip list reached the headline");
    assert.match(body, /All 5 tests ran; `--since main` skipped none of them\./);
  });

  test("a selection that was abandoned says so in the comment rather than saying nothing", () => {
    const off = { since: "main", used: false, reason: "git is not on PATH here, so there is no diff to select from", total: 2, selected: [{}, {}], picked: [], skipped: [] };
    const body = commentBody(ran, { url: "http://app.test/", suite: "tests", selection: off });
    assert.match(body, /`--since main` was not used: git is not on PATH here.*All 2 tests ran\./);
    assert.equal(selectionHeadline(off), "");
  });

  test("no selection at all leaves the comment byte for byte what it was", () => {
    const before = commentBody(ran, { url: "http://app.test/", suite: "tests" });
    const after = commentBody(ran, { url: "http://app.test/", suite: "tests", selection: null });
    assert.equal(before, after);
    assert.ok(!/skipped/.test(before));
  });

  test("a very long skip list is capped in the comment and complete in the terminal", () => {
    // Uncapped, this block is at the top of the body and the 65,536-character cut lands inside the
    // <details> — an unclosed tag on GitHub swallows every verdict under it.
    const many = { ...sel, total: 400, skipped: Array.from({ length: 400 }, (_, i) => ({ id: `t${i}`, name: `test number ${i}`, file: "tests/x.md" })) };
    const lines = selectionCommentDetail(many);
    const listed = lines.filter((l) => l.startsWith("- test number "));
    assert.equal(listed.length, 50);
    assert.ok(lines.some((l) => l.includes("…and 350 more")), "the ones it did not list went unmentioned");
    assert.equal(lines.filter((l) => l === "</details>").length, 1, "the block must close");
    assert.ok(lines.join("\n").length < 8000, "the block is still large enough to crowd out the verdicts");
    // The terminal is where every one of them is named, and that is what the comment promises.
    const term = selectionTerminalLines(many).map(plain).filter((l) => l.startsWith("  skipped "));
    assert.equal(term.length, 400);
  });

  test("a test name that would break out of the list is escaped", () => {
    const nasty = { ...sel, skipped: [{ id: "x", name: "<details>a|b*c</details>", file: "tests/<x>.md" }] };
    const lines = selectionCommentDetail(nasty).join("\n");
    assert.ok(!lines.includes("<details>a"), `an unescaped tag from a customer's heading: ${lines}`);
    // `<` escaped is what defangs a tag — the same rule as the table cell in lib/suite.mjs, where a
    // heading containing <details> once collapsed every row under it.
    assert.match(lines, /&lt;details>a\\\|b\\\*c/);
    assert.match(lines, /`tests\/&lt;x>\.md`/);
  });
});

// ---- through suiteCmd: what is run, what is printed, what is posted, what is shared ----------------

describe("suiteCmd wires it end to end", () => {
  function harness(repo, extra = {}) {
    const lines = [];
    const posted = [];
    const shared = [];
    return {
      lines, posted, shared,
      opts: {
        suite: path.join(repo, "tests"),
        plans: path.join(repo, "recordings"),
        url: "http://127.0.0.1:1/",
        cwd: repo,
        yes: true,
        log: (l) => lines.push(plain(String(l))),
        env: { ANTHROPIC_API_KEY: "sk-ant-test", GITHUB_TOKEN: "t", GITHUB_REPOSITORY: "o/r", GITHUB_REF: "refs/pull/7/merge" },
        runSuiteImpl: async ({ tests }) => tests.map((t) => ({
          ...t, status: "passed", mode: "replay", reason: "ok", ms: 100, refreshed: false, layout: [], suspects: [], share: null,
        })),
        postCommentImpl: async ({ body }) => { posted.push(body); return { posted: true, updated: false }; },
        publishShareImpl: async (b) => { shared.push(b); return null; },
        ...extra,
      },
    };
  }

  test("only the selected tests reach runSuite, and the terminal says what did not", async () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue to payment</button>;\n");
    sh(repo, "commit", "-qam", "rename");
    let given = null;
    const h = harness(repo, { runSuiteImpl: async ({ tests }) => { given = tests.map((t) => t.id); return tests.map((t) => ({ ...t, status: "passed", mode: "replay", reason: "ok", ms: 100, layout: [], suspects: [] })); } });
    const code = await suiteCmd({ ...h.opts, since: "main", comment: true, share: true });

    assert.deepEqual(given.sort(), [CART, FRESH].sort(), "runSuite was handed a test selection did not pick");
    assert.equal(code, 0, "skipping must not move the exit code");
    const out = h.lines.join("\n");
    assert.match(out, /2 of 3 tests ran; 1 skipped because this change touches no file they exercise \(--since main\)\./);
    assert.ok(out.includes(CAVEAT), "the terminal oversold the selection");
    assert.match(out, /skipped the plans page shows a monthly figure/, "the skipped test was never named in the terminal");
    assert.match(out, /1 of 3 tests were skipped by --since main — not run, not passed, not counted above\./);
    assert.match(out, /2 tests · 2 passed/, "the counts must be counts of what ran");

    // The pull request comment carries the same claim.
    assert.equal(h.posted.length, 1);
    assert.match(h.posted[0], /\*\*2 passed · 1 skipped\*\*/);
    assert.match(h.posted[0], /2 of 3 tests ran; 1 skipped/);
    assert.match(h.posted[0], /the plans page shows a monthly figure/);

    // The share page carries only what ran — a row with no verdict would read as one — and the
    // person about to send the link is told so.
    assert.equal(h.shared.length, 1);
    assert.deepEqual(h.shared[0].tests.map((t) => t.name).sort(), ["never recorded before", "the cart checks out"]);
    assert.match(out, /the shared page has the 2 tests that ran; the 1 skipped by --since main are not on it\./);
  });

  test("without --since every test runs and not a word about selection is printed", async () => {
    const repo = makeRepo();
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Continue to payment</button>;\n");
    sh(repo, "commit", "-qam", "rename");
    let given = null;
    const h = harness(repo, { runSuiteImpl: async ({ tests }) => { given = tests.map((t) => t.id); return tests.map((t) => ({ ...t, status: "passed", mode: "replay", reason: "ok", ms: 100, layout: [], suspects: [] })); } });
    const code = await suiteCmd({ ...h.opts, comment: true });
    assert.deepEqual(given.sort(), [CART, FRESH, MONEY].sort());
    assert.equal(code, 0);
    const out = h.lines.join("\n");
    assert.ok(!/skip/i.test(out), `selection spoke without being asked:\n${out}`);
    assert.ok(!/skip/i.test(h.posted[0]), "selection reached a comment without being asked");
  });

  test("a change that matches nothing runs nothing, exits 0, and says so rather than reporting a green suite", async () => {
    // The honest end of the trade, and the one worth stating out loud: a README-only change can
    // legitimately run zero tests. "0 passed · 3 skipped" is nobody's idea of a verified suite,
    // which is exactly why the headline has to carry both halves.
    const repo = makeRepo();
    writeFileSync(path.join(repo, "recordings", `${FRESH}.json`), JSON.stringify({
      startUrl: "http://app.test/", steps: [{ kind: "click", role: "link", name: "Documentation" }], proof: "Read the docs",
    }));
    writeFileSync(path.join(repo, "README.md"), "# hello\n");
    sh(repo, "add", "README.md"); sh(repo, "commit", "-qm", "readme");

    const h = harness(repo);
    const code = await suiteCmd({ ...h.opts, since: "main", comment: true });
    assert.equal(code, 0);
    const out = h.lines.join("\n");
    assert.match(out, /0 of 3 tests ran; 3 skipped/);
    assert.match(h.posted[0], /\*\*0 passed · 3 skipped\*\*/);
    const rows = h.posted[0].split("\n").filter((l) => /^\| (pass|\*\*fail\*\*|stale|error|flaky) \|/.test(l));
    assert.deepEqual(rows, [], "a test that did not run was given a verdict row");
  });
});

// ---- the whole command, a real browser, and the proof counted at the server -----------------------

describe("the real command, with the skip measured at the server", () => {
  test("the skipped test's page is never requested, and the run still exits 0", { timeout: 180_000 }, async () => {
    const hits = [];
    const server = createServer((req, res) => {
      hits.push(req.url);
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      if (req.url.startsWith("/pricing")) {
        res.end("<!doctype html><title>Plans</title><h1>Plans</h1><p>$19 per month</p>");
        return;
      }
      res.end('<!doctype html><title>Shop</title><h1>Your cart</h1><button id="go">Proceed to checkout</button><div id="out"></div>'
        + '<script>document.getElementById("go").onclick=()=>{document.getElementById("out").textContent="Order placed";}</script>');
    });
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const url = `http://127.0.0.1:${server.address().port}/`;
    try {
      const repo = makeRepo({ url });
      // The un-recorded test would need a key; this run is about replays, so it goes.
      rmSync(path.join(repo, "tests", "fresh.md"));
      writeFileSync(path.join(repo, "src", "Checkout.jsx"), "export const B = () => <button>Proceed to checkout</button>; // touched\n");
      sh(repo, "commit", "-qam", "touch the checkout file");

      const run = (args) => new Promise((resolve) => {
        const c = spawn(process.execPath, [BIN, ...args], { cwd: repo, env: { ...process.env, ANTHROPIC_API_KEY: "" } });
        let out = "";
        c.stdout.setEncoding("utf8");
        c.stderr.setEncoding("utf8");
        c.stdout.on("data", (d) => (out += d));
        c.stderr.on("data", (d) => (out += d));
        c.on("close", (status) => resolve({ status, out: plain(out) }));
      });
      const args = ["test", "--suite", path.join(repo, "tests"), "--url", url, "--plans", path.join(repo, "recordings"), "--retries", "0", "--yes"];

      hits.length = 0;
      const sel = await run([...args, "--since", "main"]);
      assert.equal(sel.status, 0, `a suite whose only run test passes must exit 0:\n${sel.out.slice(-2000)}`);
      assert.match(sel.out, /1 of 2 tests ran; 1 skipped/);
      assert.match(sel.out, /1 test against/, "the header must count what is about to run");
      assert.match(sel.out, /PASS/);
      // MEASURED AT THE SERVER, not read out of our own transcript: the skipped recording's only
      // step is a navigation to /pricing, so no request for it means no run.
      assert.deepEqual(hits.filter((h) => h.startsWith("/pricing")), [], `the skipped test ran anyway: ${JSON.stringify(hits)}`);
      assert.ok(hits.length > 0, "nothing was requested at all, so the fixture proves nothing");

      // And the same suite with no --since really does hit it, or the assertion above is vacuous.
      hits.length = 0;
      const all = await run(args);
      assert.equal(all.status, 0, all.out.slice(-2000));
      assert.ok(hits.filter((h) => h.startsWith("/pricing")).length > 0, `without --since the plans test must run: ${JSON.stringify(hits)}`);
      assert.match(all.out, /2 tests · 2 passed/);
      assert.ok(!/skipped/.test(all.out), "selection spoke without being asked");
    } finally {
      server.closeAllConnections();
      await new Promise((r) => server.close(() => r()));
    }
  });

  test("--since is refused rather than ignored: bare, dashed, and without --suite", async () => {
    const repo = makeRepo();
    const run = (args) => spawnSync(process.execPath, [BIN, ...args], { cwd: repo, encoding: "utf8" });
    const base = ["test", "--suite", path.join(repo, "tests"), "--url", "http://127.0.0.1:1/", "--yes"];

    const bare = run([...base, "--since"]);
    assert.equal(bare.status, 2, "a bare --since must not quietly run the whole folder");
    assert.match(plain(bare.stderr), /--since needs a ref/);

    const dashed = run([...base, "--since=-upload-pack=touch /tmp/x"]);
    assert.equal(dashed.status, 2);
    assert.match(plain(dashed.stderr), /starts with a dash/);

    const noSuite = run(["test", "--test", "the cart checks out", "--url", "http://127.0.0.1:1/", "--since", "main", "--yes"]);
    assert.equal(noSuite.status, 2, "--since with a single --test must be refused, not ignored");
    assert.match(plain(noSuite.stderr), /--since needs --suite/);
  });
});
