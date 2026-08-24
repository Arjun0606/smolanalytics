// The suite runner decides three things a person will act on: which sentences are tests, what
// status each one ended in, and what the pull request comment says. Each of those has a way of
// being quietly wrong — a heading swallowed into the wrong test, a stale recording reported as a
// failure, a comment posted twenty times — and each of those is checked here.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  parseSuite,
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
