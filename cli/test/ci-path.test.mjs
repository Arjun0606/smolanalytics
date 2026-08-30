// THE CI PATH, WALKED AND THEN PINNED DOWN.
//
// Every claim in this file came from running the real binary inside a real GitHub Actions
// environment — GITHUB_ACTIONS=true, an event file, a token, a GITHUB_API_URL pointed at a server
// that recorded the comment bodies — against a real app on 127.0.0.1. What that walk turned up was
// never a crash. It was four ways of being unreadable, all of the same shape: the sentence the
// reader could act on was somewhere they would not get to.
//
//   NO KEY IN CI          three tests, three identical "ANTHROPIC_API_KEY is not set" reasons, and
//                         the only advice anywhere was `export ANTHROPIC_API_KEY=… then run this
//                         again` — inside a job where nothing can be exported and a re-run fails
//                         identically. Neither surface named the secrets page or the env: line.
//   FORTY PASSES FIRST    42 tests, 2 failures. The table sorted the failures to the top, so their
//                         NAMES were visible — and their REASONS, the only actionable part, sat
//                         forty rows of "pass · replayed, no model calls" below them.
//   THE FIRST RUN         all-agent, nothing replayed, and not one word about what that meant. The
//                         only cost note that existed could not appear until something had already
//                         replayed, so the run that needed explaining was guaranteed to get none.
//   A RUN THAT NEVER RAN  the workflow copied in before tests/ existed: exit 2, the reason in the
//                         job log alone, `continue-on-error` painting the check green, and no
//                         comment on the pull request at all. Green, silent, and untested.
//
// Each test below states the requirement rather than the current string, and each was checked by
// breaking the source and watching THIS test fail.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  commentBody,
  cannotStartBody,
  announceCannotStart,
  markerFor,
  economicsNote,
  agentRuns,
  runUrlFor,
  suiteCmd,
} from "../lib/suite.mjs";
import { keyFix, keyWhere } from "../lib/test.mjs";

/** Where the first row of the verdict table starts, in lines. -1 when there is no table. */
const tableAt = (body) => body.split("\n").findIndex((l) => l.startsWith("| --- |"));
const lineOf = (body, re) => body.split("\n").findIndex((l) => re.test(l));

const passed = (n, from = 0) =>
  Array.from({ length: n }, (_, i) => ({
    name: `A shopper can do thing ${from + i}`,
    file: `tests/t${from + i}.md`,
    status: "passed",
    mode: "replay",
    ms: 600,
  }));

describe("the key a job does not have", () => {
  test("the fix named in CI is one that can be carried out in CI", () => {
    // The whole defect: `export …` is the right sentence at a keyboard and useless in Actions.
    const inCi = keyFix({ GITHUB_ACTIONS: "true" });
    assert.ok(!/\bexport\b/.test(inCi), `Actions was told to export a variable: ${inCi}`);
    assert.match(inCi, /secret/i, "the fix in Actions is a repository secret, and it must say so");
    assert.match(inCi, /ANTHROPIC_API_KEY/);

    const atAKeyboard = keyFix({});
    assert.match(atAKeyboard, /export/, "a laptop still gets the shell line");
    assert.notEqual(inCi, atAKeyboard, "one sentence cannot be right in both places");
  });

  test("where a key comes from is said at a keyboard, and never in a CI log", () => {
    // Measured by installing the published package and running the homepage's own command as
    // somebody who had never seen this: it said to export a key and never said where one comes
    // from. Anyone who has used the API fills that gap instantly; the person the first run has to
    // carry is exactly the one who cannot.
    const atAKeyboard = keyWhere({});
    assert.match(atAKeyboard, /console\.anthropic\.com/, "the one thing a newcomer is missing");
    assert.match(atAKeyboard, /billed to you/, "and that the spend is theirs, not resold through us");

    // In Actions the reader is wiring a secret they already hold. A signup link there is noise in
    // the one place nobody can act on it.
    assert.equal(keyWhere({ GITHUB_ACTIONS: "true" }), "", "a CI log gets no signup link");
  });

  test("the comment says where the key goes, once, above every row it explains", () => {
    // Measured before this existed: three identical reasons and no mention of the secrets page.
    const results = ["a", "b", "c"].map((id) => ({
      name: `test ${id}`,
      file: `tests/${id}.md`,
      status: "errored",
      mode: "agent",
      ms: 400,
      reason: "There is no recording for this test yet and ANTHROPIC_API_KEY is not set, so nothing ran.",
    }));
    const body = commentBody(results, { hasKey: false });

    const fix = lineOf(body, /Settings . Secrets and variables . Actions/);
    assert.ok(fix !== -1, `the comment never says where the key goes:\n${body}`);
    assert.ok(fix < tableAt(body), "the fix is below the table of rows it explains");
    // Said ONCE. Three copies of one instruction is a wall, and forty is a comment nobody reads.
    assert.equal((body.match(/Secrets and variables/g) || []).length, 1);
  });

  test("a run that HAS a key never lectures anybody about secrets", () => {
    const body = commentBody(
      [{ name: "x", file: "t/x.md", status: "errored", mode: "agent", ms: 5, reason: "The browser could not be started." }],
      { hasKey: true },
    );
    assert.ok(!/Secrets and variables/.test(body), body);
  });
});

describe("one outage is reported once", () => {
  test("twenty tests stopped by one thing produce one blockquote, and twenty rows", () => {
    // Errored is OUR side. Twenty tests that could not run because the runner had no key is one
    // thing that went wrong, and twenty identical blockquotes push the rest of the report off the
    // screen. The table is still the roster: every name has to survive.
    const reason = "There is no recording for this test yet and ANTHROPIC_API_KEY is not set, so nothing ran.";
    const results = Array.from({ length: 20 }, (_, i) => ({
      name: `test ${i}`, file: `tests/t${i}.md`, status: "errored", mode: "agent", ms: 400, reason,
    }));
    const body = commentBody(results, { hasKey: false });

    assert.equal((body.match(/There is no recording for this test yet/g) || []).length, 1,
      "the same outage was reported more than once");
    assert.match(body, /20 tests could not run/);
    for (let i = 0; i < 20; i++) {
      assert.ok(body.includes(`| test ${i} |`), `test ${i} lost its row in the table`);
    }
  });

  test("two DIFFERENT outages are both reported", () => {
    // Collapsing is by reason, never by status: a missing browser and a missing key are two things
    // to go and fix, and reporting one of them would hide the other.
    const body = commentBody([
      { name: "a", file: "t/a.md", status: "errored", mode: "agent", ms: 1, reason: "The browser could not be started." },
      { name: "b", file: "t/b.md", status: "errored", mode: "agent", ms: 1, reason: "The seed endpoint answered 500." },
    ], {});
    assert.match(body, /browser could not be started/);
    assert.match(body, /seed endpoint answered 500/);
  });

  test("two tests failing the same way each keep their own report", () => {
    // A failure is a claim about ONE test's behaviour, and its suspects and its file belong to it.
    // Grouping is for our outages only.
    const reason = "Clicking Proceed to checkout left the page unchanged.";
    const body = commentBody([
      { name: "checkout works", file: "tests/a.md", status: "failed", mode: "agent", ms: 1, reason },
      { name: "checkout works twice", file: "tests/b.md", status: "failed", mode: "agent", ms: 1, reason },
    ], {});
    assert.equal((body.match(/Clicking Proceed to checkout/g) || []).length, 2);
    assert.match(body, /tests\/a\.md/);
    assert.match(body, /tests\/b\.md/);
  });
});

describe("what a reviewer reaches first", () => {
  test("with forty passes and two failures, the failure reasons are above the table", () => {
    // MEASURED on a real 42-test run: the reasons were forty rows of "pass" below the names.
    const results = [
      ...passed(40),
      { name: "An expired card is refused", file: "tests/t40.md", status: "failed", mode: "agent", ms: 400,
        reason: "Clicking Proceed to checkout left the page unchanged: no confirmation and no error." },
      { name: "The product page shows the price", file: "tests/t41.md", status: "failed", mode: "agent", ms: 400,
        reason: "The price element was empty on the product page." },
    ];
    const body = commentBody(results, { url: "https://p.test" });

    const firstReason = lineOf(body, /left the page unchanged/);
    const secondReason = lineOf(body, /price element was empty/);
    const table = tableAt(body);
    assert.ok(table !== -1, "there is no table at all");
    assert.ok(firstReason !== -1 && secondReason !== -1, "a failure lost its reason");
    assert.ok(firstReason < table, `the first failure's reason is ${firstReason - table} lines BELOW the table`);
    assert.ok(secondReason < table, `the second failure's reason is ${secondReason - table} lines BELOW the table`);
  });

  test("a suite too big for GitHub loses roster rows, never a failure's reason", () => {
    // The body is cut on a line boundary at 65,000 characters. Whatever is last is what goes, so
    // the order above is also the answer to "which half survives the day the most went wrong".
    // Five real bug reports and a very large roster: the shape of a big suite on a bad day.
    const results = [
      ...Array.from({ length: 5 }, (_, i) => ({
        name: `A shopper cannot check out, case ${i}`,
        file: `tests/f${i}.md`,
        status: "failed",
        mode: "agent",
        ms: 500,
        reason: `(bug ${i}) Clicking Proceed to checkout left the page unchanged. ${"observed detail ".repeat(120)}`,
      })),
      ...Array.from({ length: 700 }, (_, i) => ({
        name: `A very long passing test name that eats into the body budget, number ${i}`,
        file: `tests/t${i}.md`,
        status: "passed",
        mode: "replay",
        ms: 500,
      })),
    ];
    const body = commentBody(results, { url: "https://p.test" });
    assert.ok(/Trimmed to fit/.test(body), `the fixture (${body.length} chars) never reached the ceiling`);
    for (let i = 0; i < 5; i++) {
      assert.ok(body.includes(`(bug ${i})`), `failure ${i}'s reason was cut, on the run where the most went wrong`);
    }
    // And the thing that WAS dropped is roster rows, which is the cheap half.
    assert.ok(!body.includes("| A very long passing test name that eats into the body budget, number 699 |"),
      "nothing was actually trimmed, so this proves nothing");
  });
});

describe("what the first run cost, said on the first run", () => {
  test("a run where everything woke the agent explains itself", () => {
    // Before this, `N of M ran from a recording` was the only note, and it cannot exist until
    // something has already replayed. The all-agent run got silence.
    const results = passed(3).map((r) => ({ ...r, mode: "agent" }));
    const body = commentBody(results, {});
    assert.match(body, /woke the agent/, `an all-agent run says nothing about why it was slow:\n${body}`);
    // And it names the thing that changes it, without asserting which of the two is true.
    assert.match(body, /actions\/cache|kept between runs/);
    assert.match(body, /first run/);
  });

  test("it never promises the next run is free", () => {
    // A failing agent run records nothing, and neither does a pass that needed no steps. The set of
    // tests that will replay tomorrow is not one this note can name — in EITHER of its two branches,
    // which is why both are asserted: a promise added to one of them is a promise that ships.
    for (const note of [economicsNote({ total: 3, replayed: 0 }, 3), economicsNote({ total: 3, replayed: 1 }, 2)]) {
      assert.ok(note, "the fixture produced no note at all, so this proves nothing");
      assert.ok(!/next run (is|will be) free/i.test(note), note);
      assert.ok(!/every test/i.test(note), note);
    }
  });

  test("a fully replayed run says nothing about the agent, because none of it woke", () => {
    const body = commentBody(passed(5), {});
    assert.ok(!/woke the agent/.test(body), body);
  });

  test("errored tests are not counted as agent runs, because nothing was spent on them", () => {
    // A browser that refused to launch, and a first model call rejected outright, both report
    // mode "agent" and both cost nothing. Saying "1 of 2 woke the agent — the slow, paid half"
    // about them puts a price on a run that never happened.
    assert.equal(agentRuns([
      { status: "errored", mode: "agent" },
      { status: "passed", mode: "replay" },
    ]), 0, "an errored test was charged as an agent run");
    assert.equal(agentRuns([
      { status: "failed", mode: "agent" },
      { status: "passed", mode: "agent" },
      { status: "stale", mode: "replay" },
    ]), 2, "a real agent run stopped being counted");
    // Counted off what the run REPORTED, never off "anything that is not a replay": a row that
    // reported no mode reported nothing, and guessing it woke the agent is inventing a cost.
    assert.equal(agentRuns([{ status: "passed", mode: "" }]), 0, "a row with no mode was guessed at");
    assert.equal(economicsNote({ total: 2, replayed: 1 }, 0), "",
      "a run with nothing to charge for still talked about cost");
  });
});

describe("a run that never started still speaks on the pull request", () => {
  test("it carries the same marker a verdict does, so the next good run edits it away", () => {
    // Otherwise the pull request keeps a stale scare above every verdict that follows it, forever.
    assert.ok(cannotStartBody("tests/ does not exist", { suite: "tests" }).startsWith(markerFor("tests")));
  });

  test("it says no test ran, and says whose problem it is", () => {
    const b = cannotStartBody("no ready preview deployment for acme/shop after 240s", { suite: "tests" });
    assert.match(b, /could not start/i);
    assert.match(b, /No test ran/i);
    assert.match(b, /not your application/i);
    assert.match(b, /no ready preview deployment/);
    assert.ok(!/pass|fail/i.test(b.split("\n")[1]), "a run that never happened must not read as a verdict");
  });

  test("a suite with no tests folder is announced on the pull request, not only in the log", async () => {
    // MEASURED: the workflow copied in before tests/ existed exited 2, said so in the job log, and
    // left the pull request with a green check and no comment.
    let sent = "";
    const code = await suiteCmd({
      suite: "tests", url: "https://p.test", comment: true, log: () => {},
      env: { ANTHROPIC_API_KEY: "k" },
      discoverImpl: () => ({ tests: [], missing: "tests/", errors: [], notes: [] }),
      postCommentImpl: async ({ body }) => { sent = body; return { posted: true }; },
    });
    assert.equal(code, 2, "the exit code is the runner's own, and this must not change it");
    assert.match(sent, /could not start/i, "nothing reached the pull request");
    assert.match(sent, /tests\//);
    // The pointer has to be somewhere the reader can actually go. `templates/example-test.md` is a
    // path inside an npm package they have not checked out.
    assert.ok(!/templates\/example-test\.md/.test(sent), "the comment points at a file the reader does not have");
    assert.match(sent, /smolanalytics suggest/, "no way forward is named");
  });

  test("an empty tests folder is announced too, and is never a passing suite", async () => {
    let sent = "";
    const code = await suiteCmd({
      suite: "tests", url: "https://p.test", comment: true, log: () => {},
      env: {},
      discoverImpl: () => ({ tests: [], missing: "", errors: [], notes: [] }),
      postCommentImpl: async ({ body }) => { sent = body; return { posted: true }; },
    });
    assert.equal(code, 2);
    assert.match(sent, /could not start/i);
    assert.match(sent, /not a passing suite/);
  });

  test("without --comment nothing is posted at all", async () => {
    let called = false;
    await suiteCmd({
      suite: "tests", url: "https://p.test", comment: false, log: () => {}, env: {},
      discoverImpl: () => ({ tests: [], missing: "tests/", errors: [], notes: [] }),
      postCommentImpl: async () => { called = true; return { posted: true }; },
    });
    assert.equal(called, false, "a run nobody asked to comment on posted a comment");
  });

  test("a comment that cannot be posted is never a second outage", async () => {
    // It is a courtesy on top of an already-reported problem. Throwing here would escape into the
    // caller's last-resort catch and could take the exit code with it.
    const r = await announceCannotStart({
      problem: "no preview",
      comment: true,
      log: () => {},
      postCommentImpl: async () => { throw new Error("network is down"); },
    });
    assert.equal(r.posted, false);
    assert.match(r.reason, /network is down/);
  });
});

describe("which commit the comment is about", () => {
  test("the comment names the commit it tested", () => {
    // One comment is edited in place for the life of a pull request, so it normally describes the
    // newest push. The exception is the shipped workflow's own two defaults: cancel-in-progress,
    // and a 30-minute timeout. A run killed before it posts leaves the PREVIOUS commit's verdicts
    // looking current, and a reader has nothing to check them against.
    const b = commentBody(passed(1), { url: "https://p.test", commit: "4f1c2ab" });
    assert.match(b, /4f1c2ab/, "the comment says nothing about which commit it is about");
    const context = b.split("\n").find((l) => l.includes("https://p.test"));
    assert.match(context, /4f1c2ab/, "the commit is not beside the URL it belongs to");
  });

  test("a run with no commit to name says nothing rather than something empty", () => {
    const b = commentBody(passed(1), { url: "https://p.test" });
    assert.match(b, /Against `https:\/\/p\.test`$/m, "an empty commit left a dangling ` at ` in the line");
  });

  test("it is the pull request's head commit, never the merge commit Actions invents", async () => {
    // GITHUB_SHA on a pull_request event is a commit that exists in nobody's history. Seven
    // characters of it is worse than nothing: a reader compares them to their branch and they never
    // match, on every single run.
    const { writeFileSync, mkdtempSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const pathMod = (await import("node:path")).default;
    const dir = mkdtempSync(pathMod.join(tmpdir(), "smolanalytics-ci-"));
    const event = pathMod.join(dir, "event.json");
    writeFileSync(event, JSON.stringify({ pull_request: { number: 7, head: { sha: "aaaaaaabbbbbbbcccccccdddddddeeeeeeefffffff" } } }));

    let sent = "";
    await suiteCmd({
      suite: "tests", url: "https://p.test", comment: true, log: () => {},
      env: {
        ANTHROPIC_API_KEY: "k",
        GITHUB_ACTIONS: "true",
        GITHUB_REPOSITORY: "acme/shop",
        GITHUB_EVENT_PATH: event,
        GITHUB_SHA: "9999999deadbeefdeadbeefdeadbeefdeadbeef00",
      },
      discoverImpl: () => ({ tests: [{ file: "tests/a.md", name: "a", test: "a", id: "a", planPath: ".rec/a.json" }], missing: "", errors: [], notes: [] }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "passed", mode: "replay", ms: 1 })),
      postCommentImpl: async ({ body }) => { sent = body; return { posted: true }; },
    });
    assert.match(sent, /aaaaaaa/, "the head commit is not in the comment");
    assert.ok(!/9999999/.test(sent), "the comment names the merge commit Actions invented, which nobody can look up");
  });
});

describe("the run log link", () => {
  test("it is built from the environment Actions provides, and is empty anywhere else", () => {
    assert.equal(
      runUrlFor({ GITHUB_RUN_ID: "42", GITHUB_REPOSITORY: "acme/shop" }),
      "https://github.com/acme/shop/actions/runs/42",
    );
    // GitHub Enterprise is a different host, and a link to github.com there is a 404.
    assert.match(
      runUrlFor({ GITHUB_RUN_ID: "42", GITHUB_REPOSITORY: "acme/shop", GITHUB_SERVER_URL: "https://ghe.acme.dev" }),
      /^https:\/\/ghe\.acme\.dev\//,
    );
    assert.equal(runUrlFor({}), "", "a laptop run must not invent a link");
  });
});

// ---- the second walk: what five statuses look like stacked on top of each other ------------------
//
// MEASURED by rendering the comment for a real shape — 40 passed, 2 failed, 1 flaky, 1 errored,
// 1 stale — and reading it as the reviewer who did not write the tests.
//
// Above the table stood five bold names over five blockquotes, rendered identically. Two were bug
// reports. One was a warning, one was our own runner breaking, and one — stale — is by contract not
// a failure at all. Nothing in that stack said which was which. The prose does disambiguate, but
// only in the last clause of a long sentence ("That is not yet a bug", "This is the test runner,
// not your application"), which is the ordering defect this file exists to catch, reproduced inside
// the fix for it. A reviewer scanning that stack reads five broken things and stops trusting the
// tool on the run where it was mostly right.
describe("a reader can tell a bug report from a warning before reading the prose", () => {
  // The name and the file share no text with any status word, deliberately. A fixture called
  // "Thing stale" in tests/stale.md satisfies every assertion below with the labels removed —
  // which is the palindrome-test mistake, written into the check for it.
  const NAMES = { failed: "Alpha", flaky: "Bravo", errored: "Charlie", stale: "Delta" };
  const one = (status, extra = {}) => ({
    name: NAMES[status],
    file: `tests/${NAMES[status].toLowerCase()}.md`,
    status,
    mode: status === "stale" ? "replay" : "agent",
    ms: 1000,
    reason: "Some prose whose disambiguating clause is at the very end.",
    ...extra,
  });
  const mixed = [...passed(3), one("failed"), one("flaky"), one("errored"), one("stale")];

  // THE REQUIREMENT. Every block above the table names its own status, in the same word the table
  // uses, before it names anything else.
  for (const [status, word] of [["failed", "fail"], ["flaky", "flaky"], ["errored", "error"], ["stale", "stale"]]) {
    test(`the ${status} block says "${word}" where the reader meets it`, () => {
      const body = commentBody(mixed, { url: "https://p.test" });
      const head = body.split("\n").find((l) => l.includes(NAMES[status]) && l.includes("**"));
      assert.ok(head, `no block introduces the ${status} test at all:\n${body}`);
      assert.ok(head.includes(word),
        `the ${status} test is introduced as "${head}" — indistinguishable from a failure until the prose is read`);
      // And it is above the table, where the reader actually meets it — the table already labelled
      // these correctly and that was never the problem.
      assert.ok(body.split("\n").indexOf(head) < tableAt(body), `the ${status} block sits below the roster`);
    });
  }

  test("no two statuses are introduced with the same word", () => {
    // The defect was that all four read the same. A fix that labels them all "test" would satisfy
    // every assertion above and none of the requirement.
    const body = commentBody(mixed, { url: "https://p.test" });
    const words = ["failed", "flaky", "errored", "stale"].map((s) => {
      const head = body.split("\n").find((l) => l.includes(NAMES[s]) && l.includes("**"));
      return head.slice(0, head.indexOf(NAMES[s]));
    });
    assert.equal(new Set(words).size, 4, `two statuses are introduced identically: ${JSON.stringify(words)}`);
  });

  test("the word above the table is the word in it, so there is one vocabulary to learn", () => {
    const body = commentBody(mixed, { url: "https://p.test" });
    const rows = body.split("\n").filter((l) => l.startsWith("| ") && !l.startsWith("| ---") && !l.startsWith("| |"));
    for (const status of ["failed", "flaky", "errored", "stale"]) {
      const row = rows.find((l) => l.includes(NAMES[status]));
      const label = row.split("|")[1].trim().replace(/\*/g, "");
      const head = body.split("\n").find((l) => l.includes(NAMES[status]) && l.includes("**"));
      assert.ok(head.includes(label),
        `the table calls it "${label}" and the block above calls it something else: ${head}`);
    }
  });
});

// ---- the evidence the workflow uploads and the comment never mentioned ---------------------------
//
// The shipped workflow gives failure evidence its own step, `if: always()`, and a comment arguing
// that a screenshot on a recycled runner is not evidence. MEASURED by reading the comment a real
// failing run posted: it mentions none of it. The picture is uploaded and, for anyone not also
// reading the workflow file, unreachable — which is the same as not having taken it.
describe("a failure says where its screenshot went", () => {
  const failing = [...passed(2), { name: "Checkout", file: "tests/c.md", status: "failed", mode: "agent", ms: 900, reason: "It did not check out." }];

  test("the reviewer is told the evidence exists and what carries it", () => {
    const body = commentBody(failing, { url: "https://p.test", runUrl: "https://github.com/a/b/actions/runs/1" });
    assert.match(body, /screenshot/i, `a failing run's comment never mentions the evidence it captured:\n${body}`);
    assert.match(body, /smolanalytics-evidence/, "the artifact it is uploaded as is the one thing that makes it findable");
  });

  test("it names the directory this run actually used, not the one we ship by default", () => {
    // --evidence-dir moves the files. A comment naming the default would send the reader to an
    // empty path and a wrong conclusion about whether anything was captured.
    const body = commentBody(failing, { url: "https://p.test", evidenceDir: "artifacts/e2e-shots" });
    assert.match(body, /artifacts\/e2e-shots/, `the comment ignored --evidence-dir:\n${body}`);
    assert.ok(!body.includes(".smolanalytics/evidence"), "it named the default path for a run that did not use it");
  });

  test("a comment that had to shrink still names the right directory", () => {
    // commentBody rebuilds itself twice when it is over GitHub's ceiling. Each rebuild passes the
    // options along by hand, and a new option is exactly the thing those two call sites forget —
    // it is how rebase() lost the URL path with two tests asserting the bug.
    const big = [
      { name: "Checkout", file: "tests/c.md", status: "failed", mode: "agent", ms: 900, reason: "It did not check out.",
        layout: [{ kind: "overlap", detail: "x", selector: "button", note: "the Pay now button overlaps the footer" }],
        suspects: [{ file: "src/pay.ts", evidence: "Pay now" }] },
      ...passed(1000),
    ];
    const body = commentBody(big, { url: "https://p.test", evidenceDir: "artifacts/e2e-shots" });
    // Both rebuilds have to have actually run, or this test proves nothing about either of them.
    assert.ok(!body.includes("overlaps the footer"), `the layout rebuild never ran (${body.length} chars), so nothing was re-passed`);
    assert.ok(!body.includes("Suspect:"), "the suspects rebuild never ran, so nothing was re-passed");
    assert.ok(/Trimmed to fit/.test(body), `the fixture (${body.length} chars) never reached the ceiling`);
    assert.match(body, /artifacts\/e2e-shots/, "a rebuilt body dropped --evidence-dir and named the default instead");
  });

  test("the comment the job actually posts names the directory the job actually used", async () => {
    // END TO END, because every assertion above stops at commentBody. --evidence-dir is read in
    // bin, handed to runSuite, and handed SEPARATELY to the comment; a comment naming one path
    // while the run wrote to another is invisible to a unit test of either half.
    let sent = "";
    await suiteCmd({
      suite: "tests", url: "https://p.test", comment: true, log: () => {},
      evidenceDir: "artifacts/e2e-shots",
      env: { ANTHROPIC_API_KEY: "k", GITHUB_REPOSITORY: "acme/shop" },
      discoverImpl: () => ({ tests: [{ name: "Checkout", file: "tests/c.md", body: "check out", planPath: "" }], missing: "", errors: [], notes: [] }),
      runSuiteImpl: async () => ([{ name: "Checkout", file: "tests/c.md", status: "failed", mode: "agent", ms: 900, reason: "It did not check out.", layout: [], suspects: [], share: null }]),
      postCommentImpl: async ({ body }) => { sent = body; return { posted: true }; },
    });
    assert.match(sent, /artifacts\/e2e-shots/, `the posted comment sent the reader to a directory this run never wrote to:\n${sent}`);
  });

  test("a green run says nothing about screenshots, because there are none", () => {
    const body = commentBody(passed(3), { url: "https://p.test" });
    assert.ok(!/screenshot/i.test(body), `a run where nothing failed talks about failure evidence:\n${body}`);
  });
});

// ---- the third cause of an agent run, which never settles ----------------------------------------
//
// MEASURED, walking a CI suite of three: one test passed by reading the page, performed no step,
// and so recorded nothing — compile() refuses a plan with no steps. Every run afterwards said
// "1 of 3 woke the agent" beside two causes ("no recording yet", "the recording stopped fitting")
// that are both false for it and both imply it will settle. It does not. economicsNote's own doc
// comment already knew — "neither does a pass that needed no steps" — and the sentence the reader
// gets did not say it, which is how a stated guarantee ends up with nothing holding it up.
describe("why the agent woke, enumerated completely", () => {
  const s = (over = {}) => ({ total: 3, passed: 3, failed: 0, stale: 0, errored: 0, flaky: 0, replayed: 2, ms: 3000, ...over });

  test("the cause that never settles is named beside the two that do", () => {
    const note = economicsNote(s(), 1, "`");
    assert.match(note, /reading a page/,
      `the reader is offered only causes that resolve themselves:\n${note}`);
    assert.match(note, /no recording yet|stopped fitting/, "the two ordinary causes must still be there");
  });

  test("a run where nothing replayed does not point only at the cache", () => {
    // This is the branch that sends someone to debug actions/cache. A suite whose tests all read
    // and never click replays nothing forever with a cache that is working perfectly.
    const note = economicsNote(s({ replayed: 0 }), 3, "`");
    assert.match(note, /reading a page/, `nothing replayed, and the only suspects offered are the cache and the first run:\n${note}`);
    assert.match(note, /kept between runs/, "the cache really is the likeliest cause and must stay named");
  });

  test("it still refuses to claim the next run is free", () => {
    for (const replayed of [0, 2]) {
      const note = economicsNote(s({ replayed }), 1, "`");
      assert.ok(!/next run (is|will be) free/i.test(note), `it promised a free run: ${note}`);
    }
  });
});
