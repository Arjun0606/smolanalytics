// The suite runner decides three things a person will act on: which sentences are tests, what
// status each one ended in, and what the pull request comment says. Each of those has a way of
// being quietly wrong — a heading swallowed into the wrong test, a stale recording reported as a
// failure, a comment posted twenty times — and each of those is checked here.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  parseSuite,
  frontmatter,
  testDepth,
  slug,
  discover,
  runSuite,
  summarize,
  exitCode,
  commentBody,
  markerFor,
  prNumber,
  apiFailure,
  postComment,
  suiteCmd,
} from "../lib/suite.mjs";

describe("reading a suite file", () => {
  test("an h1 title with h2 tests: the title is not a test", () => {
    const tests = parseSuite("tests/checkout.md", `# Checkout

## A shopper can add an item
Open the first product and add it to the cart.

## The cart survives a reload
Reload and check the item is still there.
`);
    assert.deepEqual(tests.map((t) => t.name), ["A shopper can add an item", "The cart survives a reload"]);
    assert.equal(tests[0].test, "Open the first product and add it to the cart.");
  });

  test("h1s with nothing above them are the tests", () => {
    const tests = parseSuite("tests/a.md", `# One
does a thing.

# Two
does another thing.
`);
    assert.equal(tests.length, 2);
    assert.equal(tests[1].name, "Two");
  });

  test("a heading with no prose is itself the test", () => {
    const tests = parseSuite("tests/a.md", "## the pricing page shows a monthly price\n");
    assert.equal(tests[0].test, "the pricing page shows a monthly price");
  });

  test("a file with no headings at all is one test named after the file", () => {
    const tests = parseSuite("tests/sign-up.md", "A new email address can create an account and lands on the dashboard.\n");
    assert.equal(tests.length, 1);
    assert.equal(tests[0].name, "sign up");
    assert.match(tests[0].test, /^A new email address/);
  });

  test("headings deeper than the test level stay inside their test", () => {
    const tests = parseSuite("tests/a.md", `# Title

## A test
Do the thing.

### A note about the thing
It is seeded on staging.
`);
    assert.equal(tests.length, 1, "the ### is a note, not a second test");
    assert.match(tests[0].test, /seeded on staging/);
  });

  test("code fences and html comments never become instructions", () => {
    const tests = parseSuite("tests/a.md", `## A test
<!-- reviewer: this one is flaky on Safari -->
Click Buy.

\`\`\`tsx
<Button onClick={() => alert("hi")}>Buy</Button>
\`\`\`
`);
    assert.equal(tests[0].test, "Click Buy.");
  });

  test("list markers and quotes are stripped so the sentence reads as a sentence", () => {
    const tests = parseSuite("tests/a.md", "## A test\n- Open the cart\n> and check it is empty\n");
    assert.equal(tests[0].test, "Open the cart and check it is empty");
  });

  test("testDepth: a single top-level heading with deeper ones under it is a title", () => {
    assert.equal(testDepth([1, 2, 2, 2]), 2);
    assert.equal(testDepth([2, 2]), 2);
    assert.equal(testDepth([1, 1, 2]), 1, "several h1s means the h1s are the tests");
    assert.equal(testDepth([]), 0);
  });
});

describe("discovery", () => {
  const io = (files) => ({
    exists: (p) => p === "tests" || Object.prototype.hasOwnProperty.call(files, p),
    isDir: (p) => p === "tests",
    list: () => Object.keys(files).map((f) => ({ name: f.split("/").pop(), dir: false })),
    read: (p) => files[p],
  });

  test("every test gets its own recording, and two tests never share one", () => {
    // Same heading text in two files, and a duplicate inside one file: without a unique id these
    // three would overwrite each other's recording on every run and each would then find somebody
    // else's plan and report it stale forever.
    const { tests } = discover("tests", ".rec", io({
      "tests/a.md": "## Buy\nbuy it.\n\n## Buy\nbuy it again.\n",
      "tests/b.md": "## Buy\nbuy it elsewhere.\n",
    }));
    const paths = tests.map((t) => t.planPath);
    assert.equal(new Set(paths).size, 3, paths.join(" "));
    assert.match(paths[0], /^\.rec[/\\]a--buy\.json$/);
    assert.match(paths[1], /buy-2\.json$/);
  });

  test("the recording's name does not change with how the suite path was spelled", () => {
    // `--suite tests/` locally and `--suite /abs/app/tests` in CI must find the same recordings, or
    // the cache never hits and every run pays for an agent.
    const files = { "tests/a.md": "## Buy\nbuy it.\n" };
    const here = discover("tests", ".rec", io(files)).tests[0].planPath;
    const abs = discover("tests", ".rec", {
      ...io({ "tests/a.md": files["tests/a.md"] }),
      isDir: (p) => p === "tests",
    }).tests[0].planPath;
    assert.equal(here, abs);
    assert.match(here, /a--buy\.json$/, here);
  });

  test("a missing folder is reported, not silently empty", () => {
    const { tests, missing } = discover("nope", ".rec", io({}));
    assert.equal(missing, "nope");
    assert.equal(tests.length, 0);
  });

  test("slugs are stable and filesystem-safe", () => {
    assert.equal(slug("A shopper can add an item — 50% off!"), "a-shopper-can-add-an-item-50-off");
  });
});

describe("statuses, kept apart", () => {
  const t = { file: "tests/a.md", name: "A test", test: "do a thing", id: "a", planPath: ".rec/a.json" };
  const run = (runs, code = 0, extra = {}) =>
    runSuite({
      tests: [t], url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ onRun }) => {
        runs.forEach(onRun);
        return code;
      },
      ...extra,
    });

  test("a recording that went stale and was then re-verified reads as passed, and says so", async () => {
    const [r] = await run([
      { status: "stale", mode: "replay", reason: "The recorded run no longer fits this app…" },
      { status: "passed", mode: "agent", reason: "The cart shows one line." },
    ]);
    assert.equal(r.status, "passed");
    assert.equal(r.refreshed, true);
  });

  test("stale with no key stays stale, and names the missing key", async () => {
    const [r] = await runSuite({
      tests: [t], url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: false,
      runTest: async ({ onRun }) => {
        onRun({ status: "stale", mode: "replay", reason: "The recorded run no longer fits this app." });
        return 2;
      },
    });
    assert.equal(r.status, "stale");
    assert.match(r.reason, /ANTHROPIC_API_KEY is not set/);
  });

  test("a runner that throws errors that one test and keeps going", async () => {
    const results = await runSuite({
      tests: [t, { ...t, name: "B", test: "do another thing", id: "b", planPath: ".rec/b.json" }],
      url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ test: sentence, onRun }) => {
        if (sentence === "do a thing") throw new Error("chromium crashed");
        onRun({ status: "passed", mode: "replay", reason: "ok" });
        return 0;
      },
    });
    assert.equal(results[0].status, "errored");
    assert.match(results[0].reason, /not your application/);
    assert.equal(results[1].status, "passed", "the second test still ran");
  });

  test("a run that produced no verdict at all falls back to the exit code", async () => {
    const [r] = await run([], 2);
    assert.equal(r.status, "errored");
  });

  test("a test that never ran says which thing was missing", async () => {
    const [r] = await runSuite({
      tests: [t], url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: false,
      hasPlan: () => false,
      runTest: async () => 2,
    });
    // "no detail was recorded" in a pull request comment is worse than useless on the first run,
    // which is exactly when this path is hit.
    assert.match(r.reason, /no recording for this test yet and ANTHROPIC_API_KEY is not set/i);
  });

  test("exit codes: a bug is 1, our own problem is 2, and stale we could not check is not 0", () => {
    const at = (status) => [{ status, mode: "", ms: 0, name: "x" }];
    assert.equal(exitCode(at("passed")), 0);
    assert.equal(exitCode(at("failed")), 1);
    assert.equal(exitCode(at("errored")), 2);
    assert.equal(exitCode(at("stale")), 2, "reporting 'all good' about a test nobody verified is the one lie a test tool cannot tell");
    assert.equal(exitCode([...at("failed"), ...at("errored")]), 1, "a real bug outranks our own outage");
  });

  test("summarize counts replays separately, because that is the economic claim", () => {
    const s = summarize([
      { status: "passed", mode: "replay", ms: 700 },
      { status: "passed", mode: "agent", ms: 22000 },
      { status: "failed", mode: "agent", ms: 9000 },
    ]);
    assert.deepEqual([s.total, s.passed, s.failed, s.replayed], [3, 2, 1, 1]);
  });
});

describe("one test, with a comment", () => {
  test("--plan naming a file is used as that file, not as a folder", async () => {
    let seen = "";
    let madeDir = "";
    const code = await suiteCmd({
      url: "https://x.test", test: "the pricing page shows a monthly price", plans: "rec/checkout.json",
      comment: false, log: () => {}, env: { ANTHROPIC_API_KEY: "k" },
      runSuiteImpl: async ({ tests, plansDir }) => {
        seen = tests[0].planPath;
        madeDir = plansDir;
        return [{ ...tests[0], status: "passed", mode: "replay", ms: 1 }];
      },
    });
    assert.equal(code, 0);
    assert.equal(seen, "rec/checkout.json");
    assert.equal(madeDir, "rec", "creating rec/checkout.json as a directory would collide with the recording");
  });

  test("no --suite and no --test refuses rather than passing vacuously", async () => {
    assert.equal(await suiteCmd({ url: "https://x.test", log: () => {}, env: {} }), 2);
  });

  test("no --url refuses before launching anything", async () => {
    let ran = false;
    const code = await suiteCmd({ suite: "tests", log: () => {}, env: {}, runSuiteImpl: async () => { ran = true; return []; } });
    assert.equal(code, 2);
    assert.equal(ran, false);
  });
});

describe("the pull request comment", () => {
  const results = [
    { name: "The cart survives a reload", file: "tests/checkout.md", status: "passed", mode: "replay", ms: 700, reason: "Replayed the recorded run." },
    { name: "A shopper can pay", file: "tests/checkout.md", status: "failed", mode: "agent", ms: 22400, reason: "On /cart, clicking Proceed to checkout stayed on /cart and showed no error." },
    { name: "An empty cart says so", file: "tests/cart.md", status: "stale", mode: "replay", ms: 1200, reason: "The recorded run no longer fits this app: at step 2, the button named \"Shop\" could not be used." },
  ];
  const body = commentBody(results, { url: "https://preview.example.com", suite: "tests/", runUrl: "https://github.com/o/r/actions/runs/1" });

  test("it carries a marker so the next push edits it instead of adding another", () => {
    assert.ok(body.startsWith(markerFor("tests/")));
    assert.equal(markerFor("tests/"), markerFor("tests"), "a trailing slash must not orphan the previous comment");
    // lib/pr-comment.mjs looks for its own comments with this pattern. If the two ever disagree,
    // whichever one runs second posts a duplicate instead of editing.
    assert.match(markerFor("tests/"), /<!--\s*smolanalytics-run(?::[A-Za-z0-9._-]+)?\s*-->/);
  });

  test("the failure is at the top and its full text is in the body", () => {
    const rows = body.split("\n").filter((l) => l.startsWith("| ") && !l.startsWith("| ---"));
    assert.match(rows[1], /A shopper can pay/, "the failure is the first row after the header");
    assert.match(body, /clicking Proceed to checkout stayed on \/cart/);
  });

  test("stale is never worded as a failure", () => {
    const staleRow = body.split("\n").find((l) => l.includes("An empty cart says so"));
    assert.ok(!/fail/i.test(staleRow), staleRow);
    assert.match(body, /Stale is not a failure/);
  });

  test("it says how many runs cost nothing", () => {
    assert.match(body, /1 of 3 ran from a recording, with no model calls/);
  });

  test("a pipe in a test name cannot break the table", () => {
    const b = commentBody([{ name: "a | b", file: "f.md", status: "passed", mode: "replay", ms: 1 }], {});
    assert.match(b, /a \\\| b/);
  });
});

describe("posting it", () => {
  const env = {
    GITHUB_TOKEN: "ghs_x",
    GITHUB_REPOSITORY: "acme/shop",
    GITHUB_EVENT_PATH: "/tmp/event.json",
  };
  const readFile = () => JSON.stringify({ pull_request: { number: 42 } });
  const ok = (json) => ({ ok: true, status: 200, json: async () => json, text: async () => "" });

  test("it finds the pull request three different ways", () => {
    assert.equal(prNumber(env, readFile), 42);
    assert.equal(prNumber({ GITHUB_REF: "refs/pull/7/merge" }, readFile), 7);
    assert.equal(prNumber({ PR_NUMBER: "9" }, readFile), 9);
    assert.equal(prNumber({}, readFile), 0);
  });

  test("an existing comment is edited, not duplicated", async () => {
    const calls = [];
    const fetchImpl = async (url, init = {}) => {
      calls.push(`${init.method || "GET"} ${url}`);
      if (!init.method) return ok([{ id: 5, body: "something else" }, { id: 11, body: "<!-- smolanalytics-e2e:tests --> old" }]);
      return ok({ html_url: "https://github.com/acme/shop/pull/42#issuecomment-11" });
    };
    const r = await postComment({ body: "new", marker: "<!-- smolanalytics-e2e:tests -->", env, fetchImpl, readFile });
    assert.equal(r.posted, true);
    assert.equal(r.updated, true);
    assert.ok(calls.some((c) => c === "PATCH https://api.github.com/repos/acme/shop/issues/comments/11"), calls.join("\n"));
    assert.ok(!calls.some((c) => c.startsWith("POST")), "twenty pushes must not mean twenty comments");
  });

  test("the first run posts a new comment", async () => {
    let posted = "";
    const fetchImpl = async (url, init = {}) => {
      if (!init.method) return ok([]);
      posted = url;
      return ok({ html_url: "x" });
    };
    const r = await postComment({ body: "new", marker: "m", env, fetchImpl, readFile });
    assert.equal(r.posted, true);
    assert.equal(r.updated, false);
    assert.equal(posted, "https://api.github.com/repos/acme/shop/issues/42/comments");
  });

  test("a 403 names the permission that is missing", () => {
    assert.match(apiFailure(403), /permissions: pull-requests: write/);
    assert.match(apiFailure(403), /fork/);
  });

  test("nothing about commenting can throw or change a verdict", async () => {
    const dead = async () => {
      throw new Error("ENOTFOUND api.github.com");
    };
    const r = await postComment({ body: "x", marker: "m", env, fetchImpl: dead, readFile });
    assert.equal(r.posted, false);
    assert.match(r.reason, /could not reach the GitHub API/);

    const noToken = await postComment({ body: "x", marker: "m", env: { GITHUB_REPOSITORY: "a/b" }, fetchImpl: dead, readFile });
    assert.equal(noToken.posted, false);
    assert.match(noToken.reason, /GITHUB_TOKEN is not set/);

    const notCi = await postComment({ body: "x", marker: "m", env: { GITHUB_TOKEN: "t" }, fetchImpl: dead, readFile });
    assert.match(notCi.reason, /only works inside GitHub Actions/);
  });
});

// FRONTMATTER MUST BE STRIPPED, WHETHER OR NOT WE UNDERSTAND IT.
//
// Found by running a real two-file suite and reading what the agent would have been handed:
//   "--- title: \"Checkout shows an order number\" criticality: critical --- Click Proceed to…"
// The agent was being told to go and find the YAML on the page. It also meant the title the person
// actually wrote — the one that appears on the pull request — was thrown away for a filename.
test("frontmatter is stripped and its title names the test", () => {
  const [t] = parseSuite("/x/checkout.md", [
    "---",
    'title: "Checkout shows an order number"',
    "criticality: critical",
    "---",
    "",
    "Click Proceed to checkout and confirm an order number appears.",
  ].join("\n"));
  assert.equal(t.name, "Checkout shows an order number");
  assert.equal(t.test, "Click Proceed to checkout and confirm an order number appears.");
  assert.ok(!t.test.includes("---"), "the delimiters reached the agent");
  assert.ok(!t.test.includes("criticality"), "a frontmatter key reached the agent");
});

test("a key we do not know is skipped, not an error", () => {
  // A CLI shipped today must not break on a key invented next year.
  const [t] = parseSuite("/x/a.md", "---\ntitle: Hello\nsomething_new: 42\n---\nDo the thing.");
  assert.equal(t.name, "Hello");
  assert.equal(t.test, "Do the thing.");
});

test("a file with no frontmatter still works, and CRLF does not break it", () => {
  const [a] = parseSuite("/x/plain-file.md", "Just do the thing.");
  assert.equal(a.name, "plain file", "the filename is the fallback name");
  assert.equal(a.test, "Just do the thing.");
  const [b] = parseSuite("/x/b.md", "---\r\ntitle: Windows\r\n---\r\n\r\nDo it.\r\n");
  assert.equal(b.name, "Windows");
  assert.equal(b.test, "Do it.");
});

// ---- what a real folder of markdown actually contains -------------------------------------------

describe("markdown people actually write", () => {
  test("a byte-order mark does not delete the first test in the file", () => {
    // VS Code on Windows and Visual Studio both write UTF-8 with a BOM by default. The BOM sits in
    // front of the first `#`, that heading stops being a heading, and the test under it is dropped
    // with no message at all: a green suite silently one test lighter than the folder.
    const src = "# A shopper can pay\nClick Buy.\n\n# The cart survives a reload\nReload it.\n";
    assert.deepEqual(
      parseSuite("t/a.md", "﻿" + src).map((t) => t.name),
      parseSuite("t/a.md", src).map((t) => t.name),
    );
  });

  test("a thematic break is not part of the sentence", () => {
    // `---` between two tests is the most ordinary thing in a markdown file. Folded into the body it
    // is handed to the agent as part of what to look for on the page.
    const [a, b] = parseSuite("t/a.md", "## A shopper can pay\nClick Buy.\n\n---\n\n## The cart survives a reload\nReload it.\n");
    assert.equal(a.test, "Click Buy.");
    assert.equal(b.test, "Reload it.");
    assert.equal(parseSuite("t/a.md", "## T\nDo it.\n\n***\n")[0].test, "Do it.");
    assert.equal(parseSuite("t/a.md", "## T\nDo it.\n\n___\n")[0].test, "Do it.");
  });

  test("a leading --- that swallowed a heading is reported, never silently dropped", () => {
    // A file opening with a horizontal rule and carrying another one later is indistinguishable
    // from a closed frontmatter block, so everything between them is removed as metadata. Here that
    // is a whole test. A test you believe is running and is not is worse than one you know is gone.
    const notes = [];
    const tests = parseSuite("t/a.md", "---\n\n# A shopper can pay\nClick Buy.\n\n---\n\n# The cart survives\nReload it.\n", (n) => notes.push(n));
    assert.equal(tests.length, 1, "one test really did disappear");
    assert.equal(notes.length, 1, notes.join(" "));
    assert.match(notes[0], /did not run/);
    assert.match(notes[0], /t\/a\.md/);
  });

  test("frontmatter holding a list or a comment is still frontmatter", () => {
    // Refusing to trust a closed block unless every line is `key: value` would fold real metadata
    // back into the sentence — the exact failure stripping frontmatter exists to prevent.
    const notes = [];
    const [t] = parseSuite("t/a.md", "---\ntitle: Checkout\n# owned by payments\ntags:\n  - smoke\n  - slow\n---\nClick Buy.\n", (n) => notes.push(n));
    assert.equal(t.name, "Checkout");
    assert.equal(t.test, "Click Buy.");
    assert.deepEqual(notes, [], "nothing was lost, so nothing to report");
  });

  test("an empty frontmatter block leaves no delimiter behind", () => {
    // `---` on two consecutive lines is a real, if pointless, frontmatter block. frontmatter() is
    // exported, so a caller other than parseSuite must not get a delimiter handed back as body.
    assert.deepEqual(frontmatter("---\n---\nBody.\n").body, "Body.\n");
    assert.deepEqual(frontmatter("---\n---\nBody.\n").meta, {});
  });

  test("a rule in a file with no headings is not part of the one sentence it holds", () => {
    // The single-sentence file is the shape a first-time user writes. A `---` under the sentence is
    // handed to the agent as something to look for on the page.
    assert.equal(parseSuite("t/sign-up.md", "A new email address can create an account.\n\n---\n")[0].test,
      "A new email address can create an account.");
  });

  test("a frontmatter block that is never closed does not reach the agent, and says so", () => {
    // The closing `---` is the easiest thing in the file to forget. Left folded in, the agent is
    // told to go and find `title: Checkout criticality: critical` on the page, and the name the
    // person wrote is thrown away for the filename.
    const notes = [];
    const [t] = parseSuite("t/checkout.md", "---\ntitle: Checkout\ncriticality: critical\n\nClick Buy.\n", (n) => notes.push(n));
    assert.equal(t.test, "Click Buy.");
    assert.equal(t.name, "Checkout");
    assert.equal(notes.length, 1);
    assert.match(notes[0], /t\/checkout\.md/);
    assert.match(notes[0], /never closed/i);
  });
});

describe("a folder we cannot read is our problem, not a failing app", () => {
  test("an unreadable file is collected, never thrown", () => {
    // Thrown, this reaches the CLI's last-resort catch, which prints a red `failed` and exits 1.
    // Exit 1 is the code reserved for "the application is broken". A permissions problem on the
    // runner would redden the build with a bug report about the customer's app.
    const { tests, errors } = discover("tests", ".rec", {
      exists: () => true,
      isDir: (p) => p === "tests",
      list: () => [{ name: "a.md", dir: false }, { name: "b.md", dir: false }],
      read: (p) => {
        if (p.endsWith("a.md")) throw Object.assign(new Error("EACCES: permission denied, open 'tests/a.md'"), { code: "EACCES" });
        return "## Buy\nbuy it.\n";
      },
    });
    assert.equal(errors.length, 1);
    assert.match(errors[0], /tests[/\\]a\.md/);
    assert.match(errors[0], /permission denied/);
    assert.equal(tests.length, 1, "the readable file still produced its test");
  });

  test("an unreadable subdirectory is collected, never thrown", () => {
    const { tests, errors } = discover("tests", ".rec", {
      exists: () => true,
      isDir: (p) => !p.endsWith(".md"),
      list: (p) => {
        if (p !== "tests") throw Object.assign(new Error("EACCES: permission denied, scandir 'tests/locked'"), { code: "EACCES" });
        return [{ name: "a.md", dir: false }, { name: "locked", dir: true }];
      },
      read: () => "## Buy\nbuy it.\n",
    });
    assert.equal(errors.length, 1);
    assert.match(errors[0], /locked/);
    assert.equal(tests.length, 1);
  });

  test("the run says which tests could not run, and exits 2, not 1", async () => {
    const lines = [];
    const code = await suiteCmd({
      suite: "tests", url: "https://x.test", log: (l) => lines.push(String(l)), env: {},
      discoverImpl: () => ({ tests: [{ file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" }], missing: "", errors: [], notes: [] }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "errored", mode: "agent", ms: 5, reason: "No browser is installed. This is the test runner, not your application." })),
    });
    assert.equal(code, 2, "our runner failing is never exit 1");
    const out = lines.join("\n");
    // "1 could not run" with no name is unactionable in a suite of twenty. Failed and stale are
    // both named here; errored was the only status the reader could not act on.
    assert.match(out, /A shopper can pay/);
    assert.match(out, /not your application/);
    assert.ok(!/\bfail\b/i.test(out.split("\n").filter((l) => l.includes("A shopper can pay")).join(" ")), out);
  });

  test("a note about a file we did read reaches the terminal", async () => {
    const lines = [];
    await suiteCmd({
      suite: "tests", url: "https://x.test", log: (l) => lines.push(String(l)), env: { ANTHROPIC_API_KEY: "k" },
      discoverImpl: () => ({
        tests: [{ file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" }],
        missing: "", errors: [], notes: ["tests/a.md: the frontmatter block at the top is never closed."],
      }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "passed", mode: "replay", ms: 1, reason: "ok" })),
    });
    // A note nobody prints is a note nobody acts on, and this one names a file that will keep
    // running under the wrong name until someone fixes the delimiter.
    assert.match(lines.join("\n"), /never closed/);
  });

  test("a folder that could not be read stops the run instead of reporting a green suite", async () => {
    const lines = [];
    let ran = false;
    const code = await suiteCmd({
      suite: "tests", url: "https://x.test", log: (l) => lines.push(String(l)), env: {},
      discoverImpl: () => ({ tests: [], missing: "", errors: ["tests/locked could not be read: EACCES: permission denied"], notes: [] }),
      runSuiteImpl: async () => { ran = true; return []; },
    });
    assert.equal(code, 2);
    assert.equal(ran, false);
    assert.match(lines.join("\n"), /could not be read/);
    // "no tests found" reads as "the folder is empty". It was not empty, it was shut, and those
    // are two completely different things to go and do something about.
    assert.doesNotMatch(lines.join("\n"), /no tests found/);
  });
});

describe("the exit code when part of the folder was never read", () => {
  test("passing tests do not turn a folder we could not read into a green run", async () => {
    // The worst shape this can take: nine tests pass, a tenth folder was locked, and exit 0 reports
    // "all good" about a part of the suite nobody looked at.
    const code = await suiteCmd({
      suite: "tests", url: "https://x.test", log: () => {}, env: { ANTHROPIC_API_KEY: "k" },
      discoverImpl: () => ({
        tests: [{ file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" }],
        missing: "", errors: ["tests/locked could not be read: EACCES"], notes: [],
      }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "passed", mode: "replay", ms: 700, reason: "ok" })),
    });
    assert.equal(code, 2);
  });

  test("a real bug still outranks a folder we could not read", async () => {
    const code = await suiteCmd({
      suite: "tests", url: "https://x.test", log: () => {}, env: { ANTHROPIC_API_KEY: "k" },
      discoverImpl: () => ({
        tests: [{ file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" }],
        missing: "", errors: ["tests/locked could not be read: EACCES"], notes: [],
      }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "failed", mode: "agent", ms: 700, reason: "the button did nothing" })),
    });
    assert.equal(code, 1, "exit 1 is the app being broken, and that is still the headline");
  });
});

describe("the comment never overstates what happened", () => {
  test("a stale row does not claim the recording replayed", () => {
    // `replayed, no model calls` beside `stale` reads as "ran fine, cost nothing". The recording
    // stopped fitting; nothing was verified.
    const b = commentBody([{ name: "An empty cart says so", file: "t/cart.md", status: "stale", mode: "replay", ms: 1200, reason: "The recorded run no longer fits this app." }], {});
    // The TABLE row, named as such. A stale test also gets a detail block carrying its reason, and
    // that block now sits above the table — so "the first line with this test's name in it" stopped
    // meaning "this test's row" the day the report moved above the roster.
    const row = b.split("\n").find((l) => l.startsWith("|") && l.includes("An empty cart says so"));
    assert.ok(row, `no table row for the test:\n${b}`);
    assert.ok(!/no model calls/.test(row), row);
    assert.ok(!/fail/i.test(row), row);
    assert.match(row, /stopped fitting/);
  });

  test("a folder that was never read is on the pull request, not only in a log nobody opens", async () => {
    const b = commentBody([{ name: "x", file: "f.md", status: "passed", mode: "replay", ms: 700 }],
      { problems: ["tests/locked could not be read: EACCES: permission denied"] });
    // "1 passed" alone, about a suite that is two folders long, is the tool reporting all-clear on
    // something it never looked at.
    assert.match(b, /1 not read/);
    assert.match(b, /tests\/locked/);
    assert.match(b, /not your application/);
    assert.ok(!/fail/i.test(b.split("\n").filter((l) => /locked|not read/.test(l)).join(" ")));

    let sent = "";
    await suiteCmd({
      suite: "tests", url: "https://x.test", comment: true, log: () => {}, env: { ANTHROPIC_API_KEY: "k" },
      discoverImpl: () => ({
        tests: [{ file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" }],
        missing: "", errors: ["tests/locked could not be read: EACCES"], notes: [],
      }),
      runSuiteImpl: async ({ tests }) => tests.map((t) => ({ ...t, status: "passed", mode: "replay", ms: 1, reason: "ok" })),
      postCommentImpl: async ({ body }) => { sent = body; return { posted: true }; },
    });
    assert.match(sent, /tests\/locked/, "suiteCmd dropped it on the way to the comment");
  });

  test("a missing duration renders as nothing, never as NaN", () => {
    // One NaN in a report makes a reader distrust the verdict printed next to it.
    const b = commentBody([{ name: "x", file: "f.md", status: "passed", mode: "replay" }], {});
    assert.ok(!/NaN/.test(b), b);
  });
});
