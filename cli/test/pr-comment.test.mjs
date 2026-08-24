// The PR comment is the product's public voice, and three of its rules are load-bearing enough
// that breaking one is worse than shipping no comment at all:
//
//   1. A failure count may only appear when something failed. "2 failed" in a phone notification
//      sends a person to their laptop; saying it about a renamed button burns the tool's credit
//      once and permanently.
//   2. Stale is never worded as a failure. A replay cannot tell a rename from a removal.
//   3. The body is byte-identical for identical input, because the comment is edited in place on
//      every push and a churning diff trains people to mute it.
//
// Every case below was watched fail (by breaking the code under test) before it was trusted.

import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { commentBody, commentMarker, formatDuration, postComment } from "../lib/pr-comment.mjs";

const OPTS = { url: "https://pr-412.example.dev", commit: "9f2c1ab7d4e5f60718293a4b5c6d7e8f90a1b2c3", runUrl: "https://github.com/o/r/actions/runs/7" };

const row = (over = {}) => ({ test: "the pricing page shows a monthly price", status: "passed", mode: "replay", durationMs: 640, ...over });

/** Our own copy, with the caller's reason lines removed — their words are not ours to police. */
const ourWords = (body) => body.split("\n").filter((l) => !l.startsWith(">")).join("\n");

/**
 * Every use of the word "fail" our copy is allowed to make, and all three are explicit denials.
 * Anything else — "failing recording", "1 failed" — is the thing this whole file exists to prevent.
 */
const DENIALS = /(none failed|Nothing failed|Not failures)/g;
const failWords = (body) => ourWords(body).replace(DENIALS, "").match(/fail\w*/gi) || [];

// ---- the failure-count rule ---------------------------------------------------------------------

test("stale and errored runs never produce a failure count", () => {
  const body = commentBody([
    row({ test: "a", status: "stale", reason: "the recording no longer fits" }),
    row({ test: "b", status: "errored", reason: "no browser could be launched" }),
    row({ test: "c", status: "passed" }),
  ], OPTS);
  const first = body.split("\n").find((l) => l.trim() && !l.startsWith("<!--"));
  assert.doesNotMatch(body, /\d+ of \d+ tests? failed/, "a failure count was printed with zero failures");
  assert.match(first, /none failed/, "the headline must say plainly that nothing failed");
  assert.deepEqual(failWords(body), [], "the only permitted use of the word is an explicit denial");
});

test("a headline with real failures counts only the failures, and mentions the rest separately", () => {
  const body = commentBody([
    row({ test: "a", status: "failed", reason: "the button did nothing" }),
    row({ test: "b", status: "failed", reason: "the total was wrong" }),
    row({ test: "c", status: "stale", reason: "recording" }),
    row({ test: "d", status: "errored", reason: "no key" }),
    row({ test: "e", status: "passed" }),
  ], OPTS);
  const first = body.split("\n").find((l) => l.trim() && !l.startsWith("<!--"));
  assert.match(first, /^\*\*2 of 5 tests failed\*\*/, `wrong verdict line: ${first}`);
  assert.doesNotMatch(first, /4 of 5|3 of 5/, "stale or errored runs were folded into the failure count");
});

test("all-passed and no-tests say so without arithmetic", () => {
  assert.match(commentBody([row(), row({ test: "b" }), row({ test: "c" })], OPTS), /\*\*All 3 tests passed\.\*\*/);
  assert.match(commentBody([row()], OPTS), /\*\*The test passed\.\*\*/);
  assert.match(commentBody([], OPTS), /\*\*No tests ran\.\*\*/);
});

// ---- stale is never a failure ---------------------------------------------------------------------

test("a stale-only comment contains no failure language anywhere in our own copy", () => {
  const body = commentBody([
    row({ test: "the docs nav opens", status: "stale", reason: "at step 2 the link named \"Docs\" could not be used" }),
    row({ test: "search works", status: "passed" }),
  ], OPTS);
  assert.deepEqual(failWords(body), [], "our copy calls a stale recording a failure somewhere");
  assert.match(body, /Not failures\./, "the stale section must state what it is not");
  assert.match(body, /renam/i, "the stale section must offer the rename explanation, which is the usual cause");
});

test("the stale section explains a replay cannot tell a rename from a removal", () => {
  const body = commentBody([row({ status: "stale", reason: "locator resolved to 0 elements" })], OPTS);
  assert.match(body, /#### Stale recordings/);
  assert.match(body, /rename from a removal/);
});

test("errored says plainly it is the runner, not the app", () => {
  const body = commentBody([row({ status: "errored", reason: "chromium is not installed" })], OPTS);
  assert.match(body, /#### Could not run/);
  assert.match(body, /the test runner, not your app/i);
  assert.deepEqual(failWords(body), [], "an errored run must not be worded as a failure");
});

test("an unrecognised status is reported as errored, never as passed and never as failed", () => {
  const body = commentBody([row({ test: "mystery", status: "flaky", reason: "" })], OPTS);
  assert.match(body, /#### Could not run/);
  assert.match(body, /unrecognised status "flaky"/);
  assert.doesNotMatch(body, /All 1 test passed|The test passed/, "an unknown status was counted as a pass");
  assert.doesNotMatch(body, /tests? failed/, "an unknown status was counted as a failure");
});

// ---- the bug report ---------------------------------------------------------------------------

test("a failed test leads, keeps its whole reason, and is not hidden behind a details block", () => {
  const reason = "Clicked \"Start free trial\" and the email was accepted.\nThe Continue button stayed disabled and no message appeared.";
  const body = commentBody([
    row({ test: "passing one", status: "passed" }),
    row({ test: "a signed-out visitor can start a trial", status: "failed", mode: "agent", durationMs: 47_230, file: "tests/trial.md", reason }),
  ], OPTS);
  const failedAt = body.indexOf("#### Failed");
  const detailsAt = body.indexOf("<details>");
  assert.ok(failedAt > -1, "no Failed section");
  assert.ok(detailsAt === -1 || failedAt < detailsAt, "the failure was placed after collapsed passing tests");
  for (const line of reason.split("\n")) assert.ok(body.includes(`> ${line}`), `the reason lost a line: ${line}`);
  assert.match(body, /agent · 47\.2s · `tests\/trial\.md`/, "mode, duration and file must be on the failure");
});

test("passing tests collapse, and the collapsed summary carries the model-call economics", () => {
  const rows = [
    ...Array.from({ length: 6 }, (_, i) => row({ test: `p${i}`, mode: "replay", durationMs: 600 + i })),
    row({ test: "p6", mode: "agent", durationMs: 47_200 }),
  ];
  const body = commentBody(rows, OPTS);
  assert.match(body, /<details><summary>7 passed · 6 replayed with no model calls · 1 agent run<\/summary>/);
  assert.match(body, /- \*\*p0\*\* — replay · 600ms/, "each passing test still shows mode and duration");
  assert.match(body, /- \*\*p6\*\* — agent · 47\.2s/);
});

test("one passing test is a single line naming it, not a details block", () => {
  const body = commentBody([row({ test: "the pricing page shows a monthly price" })], OPTS);
  assert.doesNotMatch(body, /<details>/);
  assert.match(body, /Passed: \*\*the pricing page shows a monthly price\*\* — replay · 640ms/);
});

// ---- durations ---------------------------------------------------------------------------------

test("durations are readable at every scale, and a missing one prints nothing", () => {
  assert.equal(formatDuration(640), "640ms");
  assert.equal(formatDuration(9_999), "9999ms");
  assert.equal(formatDuration(47_230), "47.2s");
  assert.equal(formatDuration(59_960), "1m 0s", "rounding must not be allowed to print 60.0s");
  assert.equal(formatDuration(125_000), "2m 5s");
  assert.equal(formatDuration(undefined), "");
  assert.equal(formatDuration(NaN), "", "NaNms in a report makes a reader distrust the verdict beside it");
  assert.equal(formatDuration(-5), "");
});

// ---- checkability and determinism ----------------------------------------------------------------

test("the footer names what was tested, the short commit, and the run", () => {
  const body = commentBody([row()], OPTS);
  assert.match(body, /tested `https:\/\/pr-412\.example\.dev`/);
  assert.match(body, /commit `9f2c1ab`/, "a 40-character sha is unreadable in a footer");
  assert.match(body, /\[run log\]\(https:\/\/github\.com\/o\/r\/actions\/runs\/7\)/);
});

test("identical input produces identical bytes", () => {
  const rows = [
    row({ test: "a", status: "failed", mode: "agent", durationMs: 12_345, reason: "the total was wrong" }),
    row({ test: "b", status: "stale", reason: "recording" }),
    row({ test: "c" }),
  ];
  const a = commentBody(rows, OPTS);
  const b = commentBody(JSON.parse(JSON.stringify(rows)), { ...OPTS });
  assert.equal(a, b, "the comment is edited in place on every push; a churning diff is noise");
  assert.doesNotMatch(a, /20\d\d-\d\d-\d\d|GMT|UTC/, "a timestamp would make every re-run a diff");
});

test("input order is preserved inside a section", () => {
  const body = commentBody([
    row({ test: "zebra", status: "failed", reason: "r1" }),
    row({ test: "apple", status: "failed", reason: "r2" }),
  ], OPTS);
  assert.ok(body.indexOf("zebra") < body.indexOf("apple"), "results were reordered, so the comment does not match the run");
});

// ---- hostile strings ----------------------------------------------------------------------------

test("a test name cannot break out of the markdown or the details block", () => {
  const body = commentBody([
    row({ test: "</details><script>x</script> and *stars*" }),
    row({ test: "second" }),
  ], OPTS);
  assert.doesNotMatch(body, /<\/details><script>/, "a name closed our details block and injected markup");
  assert.equal((body.match(/<\/details>/g) || []).length, 1, "the details block was closed twice");
  assert.match(body, /&lt;\/details>/, "the name must survive, neutered, rather than be dropped");
  assert.match(body, /\\\*stars\\\*/, "an unescaped asterisk italicises the rest of the comment");
});

test("a body over GitHub's limit is trimmed rather than rejected whole", () => {
  const rows = Array.from({ length: 400 }, (_, i) => row({ test: `t${i}`, status: "failed", reason: "x".repeat(500) }));
  const body = commentBody(rows, OPTS);
  assert.ok(body.length <= 65_100, `body is ${body.length} characters; GitHub 422s over 65,536`);
  assert.match(body, /Trimmed to fit GitHub's comment limit/);
  assert.ok(body.startsWith(commentMarker()), "the marker must survive trimming or the next run cannot update this comment");
});

// ---- the marker ----------------------------------------------------------------------------------

test("the marker is stable, hidden, and safe to key by suite", () => {
  assert.equal(commentMarker(), "<!-- smolanalytics-run -->");
  assert.equal(commentMarker({ suite: "e2e" }), "<!-- smolanalytics-run:e2e -->");
  assert.equal(commentMarker({ suite: "e2e --> prod" }), "<!-- smolanalytics-run:e2e-prod -->",
    "an unsanitised suite name would close the HTML comment early and dump markup into the PR");
  assert.ok(commentBody([row()], { suite: "e2e" }).startsWith("<!-- smolanalytics-run:e2e -->"));
});

// ---- posting -------------------------------------------------------------------------------------

/** A fetch that records calls and replays scripted responses. */
function fakeFetch(script) {
  const calls = [];
  const impl = async (url, init = {}) => {
    calls.push({ url, method: init.method || "GET", body: init.body ? JSON.parse(init.body) : undefined, headers: init.headers });
    const next = script.shift();
    if (!next) throw new Error(`unexpected extra fetch to ${url}`);
    if (typeof next === "function") return next(url, init);
    return {
      ok: next.status < 400,
      status: next.status,
      json: async () => next.json ?? {},
      text: async () => JSON.stringify(next.json ?? {}),
    };
  };
  impl.calls = calls;
  return impl;
}

const ENV = { GITHUB_TOKEN: "ghs_x", GITHUB_REPOSITORY: "acme/app", GITHUB_REF: "refs/pull/412/merge" };

test("with no existing comment it creates one", async () => {
  const f = fakeFetch([{ status: 200, json: [] }, { status: 201, json: { html_url: "https://github.com/acme/app/pull/412#issuecomment-1" } }]);
  const res = await postComment(commentBody([row()], OPTS), ENV, f);
  assert.deepEqual({ posted: res.posted, updated: res.updated }, { posted: true, updated: false });
  assert.equal(f.calls[1].method, "POST");
  assert.equal(f.calls[1].url, "https://api.github.com/repos/acme/app/issues/412/comments");
  assert.ok(f.calls[1].body.body.includes(commentMarker()), "a posted comment without the marker can never be updated");
});

test("with our marker already on the PR it edits that comment instead of adding another", async () => {
  const existing = [
    { id: 1, body: "unrelated review comment" },
    { id: 77, body: `${commentMarker()}\n**All 3 tests passed.**` },
  ];
  const f = fakeFetch([{ status: 200, json: existing }, { status: 200, json: { html_url: "https://github.com/acme/app/pull/412#issuecomment-77" } }]);
  const res = await postComment(commentBody([row()], OPTS), ENV, f);
  assert.deepEqual({ posted: res.posted, updated: res.updated }, { posted: true, updated: true });
  assert.equal(f.calls[1].method, "PATCH");
  assert.equal(f.calls[1].url, "https://api.github.com/repos/acme/app/issues/comments/77",
    "ten pushes to a PR must not leave ten comments");
});

test("a suite-keyed comment does not collide with another suite's comment", async () => {
  const other = [{ id: 5, body: `${commentMarker({ suite: "smoke" })}\nold` }];
  const f = fakeFetch([{ status: 200, json: other }, { status: 201, json: {} }]);
  const res = await postComment(commentBody([row()], { ...OPTS, suite: "e2e" }), ENV, f);
  assert.equal(res.updated, false, "the e2e run overwrote the smoke suite's comment");
  assert.equal(f.calls[1].method, "POST");
});

// Each of these strips exactly ONE thing out of an otherwise complete Actions environment, so the
// assertion can only be satisfied by the check it is aiming at.
test("no token means running locally, which is not an error", async () => {
  const f = fakeFetch([]);
  const res = await postComment("body", { ...ENV, GITHUB_TOKEN: undefined }, f);
  assert.deepEqual({ posted: res.posted, updated: res.updated }, { posted: false, updated: false });
  assert.equal(f.calls.length, 0, "no token must mean no network call at all");
  assert.match(res.detail, /GITHUB_TOKEN/, "the detail must name what was missing");
  assert.doesNotMatch(res.detail, /error|fail/i, `a local run must not read like something broke: ${res.detail}`);
});

test("no repository and no pull request are both non-errors", async () => {
  const f = fakeFetch([]);
  const noRepo = await postComment("body", { ...ENV, GITHUB_REPOSITORY: "" }, f);
  assert.equal(noRepo.posted, false);
  assert.equal(f.calls.length, 0);
  assert.match(noRepo.detail, /GITHUB_REPOSITORY/);
  assert.doesNotMatch(noRepo.detail, /error|fail/i);

  const g = fakeFetch([]);
  const noPr = await postComment("body", { ...ENV, GITHUB_REF: "refs/heads/main" }, g);
  assert.equal(noPr.posted, false);
  assert.equal(g.calls.length, 0);
  assert.match(noPr.detail, /not attached to a pull request/);
  assert.doesNotMatch(noPr.detail, /error|fail/i);
});

test("the PR number is read from the event payload, then from PR_NUMBER", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "smol-prc-"));
  const file = path.join(dir, "event.json");
  fs.writeFileSync(file, JSON.stringify({ action: "synchronize", pull_request: { number: 99 } }));
  const f = fakeFetch([{ status: 200, json: [] }, { status: 201, json: {} }]);
  await postComment("x", { GITHUB_TOKEN: "t", GITHUB_REPOSITORY: "acme/app", GITHUB_EVENT_PATH: file }, f);
  assert.match(f.calls[0].url, /\/issues\/99\/comments/);

  const g = fakeFetch([{ status: 200, json: [] }, { status: 201, json: {} }]);
  await postComment("x", { GITHUB_TOKEN: "t", GITHUB_REPOSITORY: "acme/app", GITHUB_EVENT_PATH: path.join(dir, "gone.json"), PR_NUMBER: "5" }, g);
  assert.match(g.calls[0].url, /\/issues\/5\/comments/, "an unreadable event file must fall through to PR_NUMBER, not give up");
});

test("a fork's read-only token is explained, not reported as a crash", async () => {
  const f = fakeFetch([{ status: 200, json: [] }, { status: 403, json: { message: "Resource not accessible by integration" } }]);
  const res = await postComment("x", ENV, f);
  assert.equal(res.posted, false);
  assert.match(res.detail, /fork/i);
  assert.match(res.detail, /pull-requests: write/);
});

test("a network failure never throws and never hides the verdict", async () => {
  const boom = async () => { throw new Error("getaddrinfo ENOTFOUND api.github.com"); };
  const res = await postComment("x", ENV, boom);
  assert.deepEqual({ posted: res.posted, updated: res.updated }, { posted: false, updated: false });
  assert.match(res.detail, /ENOTFOUND/, "the detail must name the actual problem");
  assert.match(res.detail, /verdict above still stands/, "a failed comment must not read like a failed test run");
});

test("a body handed over without a marker gets one, so the next push updates it", async () => {
  const f = fakeFetch([{ status: 200, json: [] }, { status: 201, json: {} }]);
  await postComment("just some text", ENV, f);
  assert.ok(f.calls[1].body.body.startsWith(commentMarker()));
});

test("our comment is still found when it sits past the first page", async () => {
  const page1 = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, body: "chatter" }));
  const page2 = [{ id: 500, body: `${commentMarker()}\nprevious run` }];
  const f = fakeFetch([{ status: 200, json: page1 }, { status: 200, json: page2 }, { status: 200, json: {} }]);
  const res = await postComment(commentBody([row()], OPTS), ENV, f);
  assert.equal(res.updated, true, "a busy PR would otherwise collect one comment per push");
  assert.equal(f.calls[2].url, "https://api.github.com/repos/acme/app/issues/comments/500");
});

test("a failed listing does not post a duplicate", async () => {
  const f = fakeFetch([{ status: 404, json: { message: "Not Found" } }]);
  const res = await postComment("x", ENV, f);
  assert.equal(res.posted, false);
  assert.equal(f.calls.length, 1, "posting blind after a failed lookup adds a comment on every push");
  assert.match(res.detail, /404/);
});
