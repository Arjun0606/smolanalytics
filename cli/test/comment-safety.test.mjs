// THE COMMENT IS THE PRODUCT'S ONLY OUTPUT ON A PULL REQUEST, AND EVERY WORD IN IT IS UNTRUSTED.
//
// A test's name is a markdown heading somebody wrote. A reason is prose a model wrote about a page
// the customer wrote. A file path and a preview URL both arrive from the CI environment. Rendered
// into markdown unescaped, and posted with no length bound, three things happen — all measured
// against the shipped commentBody before this file existed:
//
//   60 failing tests with an ordinary agent verdict each → a 140,053-character body. GitHub rejects
//   a comment over 65,536 with a 422, so the entire report is lost on exactly the run where the
//   most is wrong, and the pull request shows nothing at all.
//
//   a heading containing `<details>` collapses every row under it, so the tests below it vanish
//   from the table while still being counted in the headline.
//
//   a heading containing `**` or a URL containing a backtick breaks out of its own cell.
//
// None of these is exotic. `<details>` is how people write collapsible notes, `**` is how they
// emphasise, and a Vercel preview URL is whatever the action handed us.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { commentBody } from "../lib/suite.mjs";

const row = (over = {}) => ({ name: "A test", file: "tests/a.md", status: "passed", mode: "replay", ms: 700, reason: "", ...over });

describe("nothing a customer wrote can break the comment", () => {
  test("a body over GitHub's limit is trimmed, not rejected whole", () => {
    const many = Array.from({ length: 60 }, (_, i) => row({
      name: `checkout step ${i}`, status: "failed", mode: "agent", ms: 22_000,
      reason: "On /cart, clicking Proceed to checkout stayed on /cart and showed no error. ".repeat(40),
    }));
    const body = commentBody(many, { url: "https://p.example.com", suite: "tests" });
    assert.ok(body.length <= 65_536, `${body.length} characters is a 422 and no comment at all`);
    assert.match(body, /trimmed/i, "a reader has to know the report is not all here");
    assert.ok(body.startsWith("<!--"), "the marker must survive the trim or the next push posts a second comment");
  });

  test("one enormous reason cannot push the other tests out of the report", () => {
    const body = commentBody([
      row({ name: "the noisy one", status: "failed", mode: "agent", reason: "x".repeat(200_000) }),
      row({ name: "the quiet one", status: "failed", mode: "agent", reason: "The total was 0." }),
    ], { suite: "tests" });
    assert.match(body, /the quiet one/);
    assert.match(body, /The total was 0\./);
  });

  test("html in a heading cannot collapse the rows under it", () => {
    const body = commentBody([
      row({ name: "checkout <details><summary>notes</summary>" }),
      row({ name: "the row underneath" }),
    ], {});
    assert.ok(!body.includes("<details>"), body);
    assert.match(body, /the row underneath/);
  });

  test("markdown in a heading stays inside its own cell", () => {
    const body = commentBody([row({ name: "pricing **shows** a price" })], {});
    assert.ok(!/\| pricing \*\*shows\*\* a price \|/.test(body), body);
    assert.match(body, /pricing/);
  });

  test("a pipe in a test name still cannot break the table", () => {
    // The guard that already existed, kept: escaping must not regress into escaping less.
    assert.match(commentBody([row({ name: "a | b" })], {}), /a \\\| b/);
  });

  test("a backtick in the URL or a file path does not end the code span", () => {
    // CommonMark: a span's fence must be LONGER than any backtick run inside it. A single-backtick
    // span around a URL that contains one ends at that character, and the rest of the URL becomes
    // loose text — with a stray fence left open for whatever follows.
    const body = commentBody([row({ name: "n", file: "tests/we`ird.md", status: "failed", reason: "no" })], { url: "https://p`.example.com" });
    for (const raw of ["https://p`.example.com", "tests/we`ird.md"]) {
      const m = new RegExp("(`+)\\s?" + raw.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s?\\1").exec(body);
      assert.ok(m, `${raw} is not inside a code span in:\n${body}`);
      const longest = Math.max(...[...raw.matchAll(/`+/g)].map((r) => r[0].length), 0);
      assert.ok(m[1].length > longest, `a ${m[1].length}-backtick fence around a run of ${longest}`);
    }
  });

  test("a heading cannot forge the marker and orphan the real comment", () => {
    // Our own needle is an HTML comment. A test named with one would leave two in the body, and
    // whichever the next run matched first is the one it would edit.
    const body = commentBody([row({ name: "<!-- smolanalytics-run:tests -->" })], { suite: "tests" });
    assert.equal((body.match(/<!--/g) || []).length, 1, body);
  });

  test("the reason keeps its words: this escapes structure, not prose", () => {
    const body = commentBody([row({
      status: "failed", name: "checkout", reason: 'On /cart, the "Proceed to checkout" button (a <button>) did nothing — 2 items still listed.',
    })], {});
    assert.match(body, /Proceed to checkout/);
    assert.match(body, /2 items still listed/);
  });
});

// ---- a verdict nobody produced is not a verdict ---------------------------------------------------

import { runSuite } from "../lib/suite.mjs";

test("a runner that exits 1 without reporting anything is not a bug report", async () => {
  // Exit 1 means "a test failed". But nothing was observed here, and the reason printed in the same
  // row says exactly that. **fail** next to "nothing was observed" is a bug report about a bug
  // nobody saw. Errored still exits 2, so nothing reads it as green.
  const [r] = await runSuite({
    tests: [{ file: "tests/a.md", name: "A test", test: "do a thing", id: "a", planPath: ".rec/a.json" }],
    url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: true, hasPlan: () => true,
    runTest: async () => 1,
  });
  assert.equal(r.status, "errored");
  assert.match(r.reason, /not your application/i);
});
