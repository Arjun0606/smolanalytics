// SUSPECTED CODE: when a test fails on a pull request, the changed files most likely responsible.
//
// The rules under test are the honesty spine applied to blame:
//
//   every suspicion carries its NAMED evidence — the exact string or path that connects the file
//   to what the failing run observed;
//   zero matches = say nothing at all. The whole diff is never ranked as vaguely suspicious;
//   a string the diff literally touches outranks a path-name coincidence;
//   nothing here may change a verdict, an exit code, or survive its own failure as anything but [].
//
// The scorer is unit-tested on synthetic diffs, then proven against a REAL git repo: init, commit
// an app, branch, break the button label, commit, and ask which file did it.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { collectFacts, parseDiff, scoreSuspects, gitDiff, suspectsForFailure } from "../lib/suspect.mjs";
import { runSuite, suiteCmd, commentBody } from "../lib/suite.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; the end-to-end test skips with a reason instead */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-"));

// A changed-file record the shape gitDiff produces.
const changed = (file, over = {}) => ({ file, oldFile: file, removed: [], added: [], binary: false, renamed: false, ...over });

// ---- the scorer, on synthetic diffs -------------------------------------------------------------

describe("scoreSuspects: no suspicion without named evidence", () => {
  const clickFact = { strings: [{ text: "Proceed to checkout", kind: "click" }], routes: [] };

  test("a removed button label names the file, and the evidence names the string and the act", () => {
    const [s, ...rest] = scoreSuspects(clickFact, [
      changed("src/Checkout.tsx", { removed: ['        <button>Proceed to checkout</button>'] }),
      changed("src/Header.tsx", { removed: ['<nav className="top">'] }),
    ]);
    assert.equal(s.file, "src/Checkout.tsx");
    assert.equal(s.evidence, 'this PR removed the string "Proceed to checkout" this test clicks');
    assert.equal(rest.length, 0, "the file with no connection to the test must not be mentioned at all");
  });

  test("a vanished proof text is evidence, and says what the text proved", () => {
    const [s] = scoreSuspects({ strings: [{ text: "Order placed", kind: "proof" }], routes: [] }, [
      changed("src/OrderDone.tsx", { removed: ['      <h2>Order placed</h2>'] }),
    ]);
    assert.equal(s.file, "src/OrderDone.tsx");
    assert.equal(s.evidence, 'this PR removed "Order placed" — the text this test checks as its proof of passing');
  });

  test("a file whose path matches a visited route is a suspect, with the route as evidence", () => {
    const [s] = scoreSuspects({ strings: [], routes: [{ path: "/checkout", segments: ["checkout"] }] }, [
      changed("src/pages/checkout.tsx", { removed: ["const x = 1;"] }),
      changed("src/pages/about.tsx", { removed: ["const y = 2;"] }),
    ]);
    assert.equal(s.file, "src/pages/checkout.tsx");
    assert.equal(s.evidence, "its path matches /checkout, a page this test visited");
  });

  test("zero matches is an empty array — never the diff ranked as vaguely suspicious", () => {
    const facts = { strings: [{ text: "Proceed to checkout", kind: "click" }], routes: [{ path: "/cart", segments: ["cart"] }] };
    const out = scoreSuspects(facts, [
      changed("README.md", { removed: ["old sentence"], added: ["new sentence"] }),
      changed("src/unrelated.ts", { added: ["export const n = 2;"] }),
    ]);
    assert.deepEqual(out, [], "a diff with no connection to the test must produce silence");
  });

  test("a pure rename has no removed strings, so it needs a path match or it is nothing", () => {
    const out = scoreSuspects(clickFact, [
      changed("src/CheckoutPanel.tsx", { oldFile: "src/Panel.tsx", renamed: true }),
    ]);
    assert.deepEqual(out, [], "a rename that touches nothing the test used is not evidence");
  });

  test("a binary file cannot match a string, and without a path relation it is nothing", () => {
    const out = scoreSuspects(clickFact, [
      changed("public/hero.png", { binary: true }),
    ]);
    assert.deepEqual(out, [], "bytes are not the string the test clicked");
  });

  test("a literal string match outranks a path-name coincidence", () => {
    const facts = {
      strings: [{ text: "Proceed to checkout", kind: "click" }],
      routes: [{ path: "/checkout", segments: ["checkout"] }],
    };
    const out = scoreSuspects(facts, [
      changed("src/routes/checkout.css", { removed: [".hero { color: red }"] }),
      changed("src/Cart.tsx", { removed: ['<button>Proceed to checkout</button>'] }),
    ]);
    assert.equal(out[0].file, "src/Cart.tsx", "the diff that touches the clicked string is the stronger story");
    assert.match(out[0].evidence, /removed the string "Proceed to checkout"/);
  });

  test("removal outranks addition of the same string, and an added-only match says 'added'", () => {
    const out = scoreSuspects(clickFact, [
      changed("src/New.tsx", { added: ['<button>Proceed to checkout</button>'] }),
      changed("src/Old.tsx", { removed: ['<button>Proceed to checkout</button>'] }),
    ]);
    assert.equal(out[0].file, "src/Old.tsx");
    assert.equal(out[1].file, "src/New.tsx");
    assert.match(out[1].evidence, /added a line containing the string "Proceed to checkout"/);
  });

  test("one file matching several facts carries its best evidence and says there is more", () => {
    const facts = {
      strings: [
        { text: "Order placed", kind: "proof" },
        { text: "Proceed to checkout", kind: "click" },
      ],
      routes: [],
    };
    const [s] = scoreSuspects(facts, [
      changed("src/Checkout.tsx", { removed: ["<h2>Order placed</h2>", "<button>Proceed to checkout</button>"] }),
    ]);
    assert.match(s.evidence, /^this PR removed "Order placed"/, "the proof is the strongest fact and must lead");
    assert.match(s.evidence, /1 more matched fact/);
  });

  test("framework scaffolding segments never match: /app cannot flag everything under app/", () => {
    const out = scoreSuspects({ strings: [], routes: [{ path: "/app", segments: [] }] }, [
      changed("app/layout.tsx", { removed: ["x"] }),
      changed("app/page.tsx", { removed: ["y"] }),
    ]);
    assert.deepEqual(out, []);
  });
});

// ---- reading a unified diff ---------------------------------------------------------------------

describe("parseDiff", () => {
  test("removed and added lines land on the right files, metadata does not", () => {
    const files = parseDiff([
      "diff --git a/src/a.ts b/src/a.ts",
      "index 111..222 100644",
      "--- a/src/a.ts",
      "+++ b/src/a.ts",
      "@@ -1,2 +1,2 @@",
      "-old line",
      "+new line",
      "diff --git a/src/b.ts b/src/b.ts",
      "--- a/src/b.ts",
      "+++ b/src/b.ts",
      "@@ -5 +5 @@",
      "-goodbye",
      "\\ No newline at end of file",
    ].join("\n"));
    assert.deepEqual(files.map((f) => f.file), ["src/a.ts", "src/b.ts"]);
    assert.deepEqual(files[0].removed, ["old line"]);
    assert.deepEqual(files[0].added, ["new line"]);
    assert.deepEqual(files[1].removed, ["goodbye"]);
    assert.deepEqual(files[1].added, [], "the no-newline marker is not content");
    // The `--- a/src/a.ts` header starts with a dash; read as content it would score a file for
    // "removing" its own path.
    assert.ok(!files[0].removed.some((l) => l.includes("src/a.ts")), JSON.stringify(files[0].removed));
  });

  test("a pure rename is recognised with its new name and no content", () => {
    const [f] = parseDiff([
      "diff --git a/src/Panel.tsx b/src/CheckoutPanel.tsx",
      "similarity index 100%",
      "rename from src/Panel.tsx",
      "rename to src/CheckoutPanel.tsx",
    ].join("\n"));
    assert.equal(f.file, "src/CheckoutPanel.tsx");
    assert.equal(f.renamed, true);
    assert.deepEqual([f.removed, f.added], [[], []]);
  });

  test("a binary change is flagged and yields no lines", () => {
    const [f] = parseDiff([
      "diff --git a/public/hero.png b/public/hero.png",
      "index 111..222 100644",
      "Binary files a/public/hero.png and b/public/hero.png differ",
    ].join("\n"));
    assert.equal(f.binary, true);
    assert.deepEqual([f.removed, f.added], [[], []]);
  });

  test("a deletion keeps its only name, the old one", () => {
    const [f] = parseDiff([
      "diff --git a/src/gone.ts b/src/gone.ts",
      "deleted file mode 100644",
      "--- a/src/gone.ts",
      "+++ /dev/null",
      "@@ -1 +0,0 @@",
      "-export const gone = true;",
    ].join("\n"));
    assert.equal(f.file, "src/gone.ts");
    assert.deepEqual(f.removed, ["export const gone = true;"]);
  });
});

// ---- what counts as a fact ----------------------------------------------------------------------

describe("collectFacts: only what the run is known to have interacted with", () => {
  test("wire-step labels, the recording, and the reason each contribute their facts", () => {
    const facts = collectFacts({
      runs: [{ steps: [
        { do: 'click button "Proceed to checkout"', ok: false },
        { do: 'fill "Email" = "a@example.com"', ok: true },
        { do: "goto https://pr-42.example.com/cart", ok: true },
      ] }],
      reason: 'On /cart, the button named "Proceed to checkout" did nothing; the page still says "2 items".',
      plan: { startUrl: "https://pr-42.example.com/cart", proof: "Order placed", steps: [{ kind: "click", role: "button", name: "Place order" }] },
      url: "https://pr-42.example.com/",
    });
    const byText = Object.fromEntries(facts.strings.map((s) => [s.text, s.kind]));
    assert.equal(byText["Proceed to checkout"], "click", "quoted in the reason too, but the clicked reading is the stronger and must win");
    assert.equal(byText["Email"], "fill");
    assert.equal(byText["Order placed"], "proof");
    assert.equal(byText["Place order"], "click");
    assert.equal(byText["2 items"], "named");
    assert.equal(byText["/cart"], "path");
    assert.deepEqual(facts.routes, [{ path: "/cart", segments: ["cart"] }]);
  });

  test("strings too short to be anything but wildcards are refused as facts", () => {
    const facts = collectFacts({ runs: [{ steps: [{ do: 'click button "OK"', ok: false }] }], reason: 'clicking "OK" did nothing on /' });
    assert.deepEqual(facts.strings, [], '"OK" is contained in TOKEN, and a substring fact indicts whichever file changed');
  });

  test("the root path is not a fact: every app has one", () => {
    const facts = collectFacts({ url: "https://pr-42.example.com/" });
    assert.deepEqual(facts, { strings: [], routes: [] });
  });
});

// ---- the real thing: a temp git repo, a broken label, the culprit named -------------------------

const sh = (cwd, ...args) => {
  const r = spawnSync("git", ["-c", "user.email=t@t.test", "-c", "user.name=t", ...args], { cwd, encoding: "utf8" });
  assert.equal(r.status, 0, `git ${args.join(" ")} failed: ${r.stderr}`);
  return r.stdout;
};

/** init on main, commit a small shop app, branch, break the checkout label, commit. */
function makeRepo() {
  const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
  sh(repo, "init", "-q", "-b", "main");
  mkdirSync(path.join(repo, "src"), { recursive: true });
  writeFileSync(path.join(repo, "src", "Checkout.jsx"), [
    "export function Checkout() {",
    "  return <button>Proceed to checkout</button>;",
    "}",
    "",
  ].join("\n"));
  writeFileSync(path.join(repo, "src", "About.jsx"), "export const About = () => <p>About us</p>;\n");
  sh(repo, "add", ".");
  sh(repo, "commit", "-q", "-m", "shop");
  sh(repo, "checkout", "-q", "-b", "pr");
  writeFileSync(path.join(repo, "src", "Checkout.jsx"), [
    "export function Checkout() {",
    "  return <button>Continue to payment</button>;",
    "}",
    "",
  ].join("\n"));
  writeFileSync(path.join(repo, "src", "About.jsx"), "export const About = () => <p>About the team</p>;\n");
  sh(repo, "add", ".");
  sh(repo, "commit", "-q", "-m", "rename the checkout button");
  return repo;
}

describe("against a real git repo", () => {
  test("the file that removed the clicked label is named, with the contract's evidence sentence", () => {
    const repo = makeRepo();
    const suspects = suspectsForFailure({
      runs: [{ status: "failed", steps: [{ do: 'click button "Proceed to checkout"', ok: false, detail: "no such element" }] }],
      reason: 'On /, the button named "Proceed to checkout" is not on the page.',
      url: "http://127.0.0.1:4173/",
      env: { GITHUB_BASE_REF: "main" },
      cwd: repo,
    });
    assert.equal(suspects.length, 1, `About.jsx changed too, but nothing connects it to this test: ${JSON.stringify(suspects)}`);
    assert.equal(suspects[0].file, "src/Checkout.jsx");
    assert.equal(suspects[0].evidence, 'this PR removed the string "Proceed to checkout" this test clicks');
  });

  test("without GITHUB_BASE_REF the default branch is found, and the answer is the same", () => {
    const repo = makeRepo();
    const suspects = suspectsForFailure({
      runs: [{ status: "failed", steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: "The button is gone.",
      env: {},
      cwd: repo,
    });
    assert.equal(suspects[0]?.file, "src/Checkout.jsx", JSON.stringify(suspects));
  });

  test("the recording on disk supplies the proof, and a diff that removed it is evidence", () => {
    const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
    sh(repo, "init", "-q", "-b", "main");
    writeFileSync(path.join(repo, "done.html"), "<h2>Order placed</h2>\n");
    sh(repo, "add", ".");
    sh(repo, "commit", "-q", "-m", "v1");
    sh(repo, "checkout", "-q", "-b", "pr");
    writeFileSync(path.join(repo, "done.html"), "<h2>All done!</h2>\n");
    sh(repo, "add", ".");
    sh(repo, "commit", "-q", "-m", "reword");
    const planPath = path.join(scratch(), "t.json");
    writeFileSync(planPath, JSON.stringify({ startUrl: "http://x/", steps: [{ kind: "goto", url: "http://x/done" }], proof: "Order placed" }));
    const suspects = suspectsForFailure({
      runs: [], reason: "The confirmation page changed.", planPath,
      env: { GITHUB_BASE_REF: "main" }, cwd: repo,
    });
    assert.equal(suspects[0]?.file, "done.html", JSON.stringify(suspects));
    assert.match(suspects[0].evidence, /removed "Order placed" — the text this test checks as its proof of passing/);
  });

  test("gitDiff outside any repo is null, and suspectsForFailure degrades to silence", () => {
    const dir = scratch(); // tmpdir on this machine is not inside a work tree
    assert.equal(gitDiff({ env: {}, cwd: dir }), null);
    const suspects = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: "it broke", env: {}, cwd: dir,
    });
    assert.deepEqual(suspects, [], "no git must mean no output — never a warning, never a throw");
  });

  test("a run that observed nothing never shells out to git at all", () => {
    let called = 0;
    const suspects = suspectsForFailure({
      runs: [], reason: "", url: "", env: {}, cwd: scratch(),
      getDiff: () => { called++; return [changed("src/a.ts", { removed: ["x"] })]; },
    });
    assert.deepEqual(suspects, []);
    assert.equal(called, 0, "with no facts there is nothing to match, and the diff is pure cost");
  });

  test("a getDiff that throws costs nothing but the hint", () => {
    const suspects = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: "it broke", env: {}, cwd: scratch(),
      getDiff: () => { throw new Error("git exploded"); },
    });
    assert.deepEqual(suspects, []);
  });
});

// ---- through the suite: the result row, the terminal, the comment -------------------------------

describe("wired into the suite", () => {
  const t = { file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" };
  const suspect = { file: "src/Checkout.tsx", evidence: 'this PR removed the string "Proceed to checkout" this test clicks' };

  test("a failed test's row carries suspects; every other status never asks", async () => {
    const asked = [];
    const results = await runSuite({
      tests: [t, { ...t, name: "B", test: "browse", id: "b", planPath: ".rec/b.json" }],
      url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: true,
      findSuspects: (args) => { asked.push(args.planPath); return [suspect]; },
      runTest: async ({ test: sentence, onRun }) => {
        if (sentence === "buy") {
          onRun({ status: "failed", mode: "agent", reason: "the button did nothing" });
          return 1;
        }
        onRun({ status: "passed", mode: "replay", reason: "ok" });
        return 0;
      },
    });
    assert.deepEqual(results[0].suspects, [suspect]);
    assert.deepEqual(results[1].suspects, [], "a passing test must never carry blame");
    assert.deepEqual(asked, [".rec/a.json"], "suspects are computed once, for the failed test only");
  });

  test("the terminal prints at most two suspect lines under the failure", async () => {
    const lines = [];
    const three = [suspect, { file: "src/Cart.tsx", evidence: "e2" }, { file: "src/Extra.tsx", evidence: "e3" }];
    await suiteCmd({
      suite: "tests", url: "https://x.test", log: (...a) => lines.push(a.join(" ")), env: {},
      discoverImpl: () => ({ tests: [t], missing: "", errors: [], notes: [] }),
      runSuiteImpl: async ({ tests }) => tests.map((x) => ({ ...x, status: "failed", mode: "agent", ms: 5, reason: "the button did nothing", suspects: three })),
    });
    const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
    assert.match(out, /suspect: src\/Checkout\.tsx — this PR removed the string "Proceed to checkout" this test clicks/);
    assert.match(out, /suspect: src\/Cart\.tsx/);
    assert.ok(!out.includes("src/Extra.tsx"), "the third-best guess is where a blame list stops reading as evidence");
  });

  test("the comment puts each suspect under its failure, capped at two, evidence intact", () => {
    const body = commentBody([
      { name: "A shopper can pay", file: "tests/a.md", status: "failed", mode: "agent", ms: 9000, reason: "The button did nothing.",
        suspects: [suspect, { file: "src/Cart.tsx", evidence: "its path matches /cart, a page this test visited" }, { file: "src/Extra.tsx", evidence: "e3" }] },
      { name: "The cart survives", file: "tests/b.md", status: "passed", mode: "replay", ms: 700 },
    ], { url: "https://p.example.com", suite: "tests" });
    assert.match(body, /> Suspect: `src\/Checkout\.tsx` — this PR removed the string "Proceed to checkout" this test clicks/);
    assert.match(body, /> Suspect: `src\/Cart\.tsx` — its path matches \/cart/);
    assert.ok(!body.includes("src/Extra.tsx"), "at most two suspect lines per failing test");
    const reasonAt = body.indexOf("The button did nothing.");
    const suspectAt = body.indexOf("Suspect:");
    assert.ok(reasonAt !== -1 && reasonAt < suspectAt, "the failure's own reason comes first; the hint sits under it");
  });

  test("a failed row with no suspects renders exactly as it did before this feature", () => {
    const body = commentBody([
      { name: "A shopper can pay", file: "tests/a.md", status: "failed", mode: "agent", ms: 9000, reason: "The button did nothing.", suspects: [] },
    ], { suite: "tests" });
    assert.ok(!body.includes("Suspect:"), "zero matches must mean silence, not an empty heading");
  });

  test("when the comment must shrink, suspects are the first thing dropped — reasons survive", () => {
    // 15 failing tests with maximum-length reasons sit under BODY_LIMIT on their own; the suspect
    // lines push the body over. The right trim loses every hint and keeps every reason, with no
    // "trimmed" banner, because nothing a reader needs is missing.
    const results = Array.from({ length: 15 }, (_, i) => ({
      name: `checkout step ${i}`, file: "tests/a.md", status: "failed", mode: "agent", ms: 1000,
      reason: `reason-${i}-` + "x".repeat(4100),
      suspects: [
        { file: `src/File${i}.tsx`, evidence: "this PR removed the string " + `"${"e".repeat(2000)}"` + " this test clicks" },
        { file: `src/Other${i}.tsx`, evidence: "this PR removed the string " + `"${"f".repeat(2000)}"` + " this test clicks" },
      ],
    }));
    const withSuspects = commentBody(results, { suite: "tests" });
    assert.ok(withSuspects.length <= 65_536, `${withSuspects.length} characters is a 422 and no comment at all`);
    assert.ok(!withSuspects.includes("Suspect:"), "suspects must be dropped before any reason is cut");
    for (let i = 0; i < 15; i++) assert.match(withSuspects, new RegExp(`reason-${i}-`), `test ${i}'s reason was sacrificed for a hint`);
    assert.ok(!/trimmed/i.test(withSuspects), "dropping hints is not a trim a reader needs warning about");
    // And the drop is really doing something: the same rows kept their suspects when there was room.
    const few = commentBody(results.slice(0, 2), { suite: "tests" });
    assert.match(few, /Suspect: `src\/File0\.tsx`/);
  });
});

// ---- end to end: real repo, real server, real Chromium, and the culprit on the comment ----------

test("a failing run in CI names the file whose diff broke it, in the terminal and the comment", noBrowser, async () => {
  // The whole path a customer's CI takes, with only the model scripted: a git repo whose PR branch
  // renames the checkout button, that branch's build served over real HTTP, a real browser failing
  // to find the button, and the suite wiring the failure to the diff. The agent's failure prose is
  // written the way test.mjs's SYSTEM demands — naming the control in quotes — because that prose
  // is where a first-ever failure's facts come from.
  const repo = makeRepo();
  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end("<!doctype html><title>Shop</title><h1>Your cart</h1><button>Continue to payment</button>");
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}/`;

  const cwd = process.cwd();
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const lines = [];
  let commented = null;
  try {
    process.chdir(repo); // where the customer's CI runs: inside the checkout of the PR branch
    globalThis.fetch = async (target, init = {}) => {
      if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
      assert.match(String(target), /api\.anthropic\.com/, "nothing but the model and the app may be called here");
      return {
        ok: true, status: 200, text: async () => "",
        json: async () => ({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t1", name: "finish", input: {
          passed: false, why: 'On /, there is no button named "Proceed to checkout"; the page offers only "Continue to payment".', proof: "",
        } }] }),
      };
    };
    const code = await suiteCmd({
      test: "the cart can be checked out", url, retries: 0, comment: true,
      plans: path.join(repo, ".smolanalytics", "recordings"), evidenceDir: path.join(repo, ".smolanalytics", "evidence"),
      log: (...a) => lines.push(a.join(" ")),
      env: { ANTHROPIC_API_KEY: "sk-ant-test", GITHUB_BASE_REF: "main" },
      postCommentImpl: async ({ body }) => { commented = body; return { posted: true, updated: false, url: "" }; },
    });
    assert.equal(code, 1, "the failure is still a failure: suspects decorate the verdict, never soften it");
    const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
    assert.match(out, /suspect: src\/Checkout\.jsx — this PR removed the string "Proceed to checkout"/);
    assert.ok(!out.includes("About.jsx"), "the changed file with no connection to the test stays unmentioned");
    assert.match(commented, /> Suspect: `src\/Checkout\.jsx` — this PR removed the string "Proceed to checkout"/);
    assert.ok(!commented.includes("About.jsx"), commented);
  } finally {
    globalThis.fetch = realFetch;
    process.chdir(cwd);
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
    await new Promise((r) => server.close(() => r()));
  }
});

// ---- the label the wire ACTUALLY carries, not the one we imagined -------------------------------
//
// Every test above this line feeds collectFacts a label like `click button "Buy"`. test.mjs does
// not produce that string. describe() writes for a terminal, step.label keeps everything but the
// tick and the step number, and report() puts THAT on the wire — so a real label is
// `click button "Buy"\x1b[2m 480ms\x1b[0m`, or with the failure detail after an em dash. Measured
// against a real Chromium taking three real steps: collectFacts returned nothing at all, because
// the click and goto patterns are anchored at a `"` and a `$` that no shipped label ever reaches.

const ESC = String.fromCharCode(27);
const dim = (s) => `${ESC}[2m${s}${ESC}[0m`;
const red = (s) => `${ESC}[31m${s}${ESC}[0m`;

describe("collectFacts reads the label test.mjs really writes", () => {
  test("colour codes and the trailing time do not hide the clicked control or the route", () => {
    const facts = collectFacts({
      runs: [{ steps: [
        { n: 1, do: `click button "Proceed to checkout"${dim(" 480ms")}`, ok: true, ms: 480 },
        { n: 2, do: `goto http://127.0.0.1:53182/cart${dim(" 506ms")}`, ok: true, ms: 506 },
      ] }],
    });
    const byText = Object.fromEntries(facts.strings.map((s) => [s.text, s.kind]));
    assert.equal(byText["Proceed to checkout"], "click", `the clicked control was lost: ${JSON.stringify(facts)}`);
    assert.deepEqual(facts.routes, [{ path: "/cart", segments: ["cart"] }], "the visited route was lost");
  });

  test("a step that FAILED still names its target: the detail after the em dash comes off", () => {
    // The most valuable step there is — the click that could not be performed — and the one whose
    // label carries an arbitrary Playwright error message where a `"` used to end the string.
    const detail = "locator.click: Timeout 10000ms exceeded.";
    const facts = collectFacts({
      runs: [{ steps: [{ n: 3, do: `click button "Proceed to checkout"${red(` — ${detail}`)}`, ok: false, detail }] }],
    });
    assert.deepEqual(facts.strings, [{ text: "Proceed to checkout", kind: "click" }], JSON.stringify(facts));
  });

  test("a control whose own name ends in a measurement is not trimmed into a different control", () => {
    const facts = collectFacts({
      runs: [{ steps: [{ n: 1, do: `click button "Retry in 500ms"${dim(" 12ms")}`, ok: true, ms: 12 }] }],
    });
    assert.deepEqual(facts.strings, [{ text: "Retry in 500ms", kind: "click" }], JSON.stringify(facts));
  });

  test("a filled value containing the label's own separator does not eat the field name", () => {
    // describe() writes `fill "<name>" = <JSON of the text>`. Typing `a" = "b` into a search box
    // puts `" = ` inside that JSON, and slicing at the last one named the field `Search" = "a\`.
    const typed = 'a" = "b';
    const facts = collectFacts({
      runs: [{ steps: [{ n: 1, do: `fill "Search" = ${JSON.stringify(typed)}${dim(" 30ms")}`, ok: true, ms: 30 }] }],
    });
    assert.deepEqual(facts.strings, [{ text: "Search", kind: "fill" }], JSON.stringify(facts));
  });
});

// ---- the survivors of a mutation sweep ----------------------------------------------------------

describe("blame that would be confidently wrong", () => {
  test("the entry point caps at two itself, not only where it is rendered", () => {
    // The terminal and the comment each slice to two, so removing this slice broke nothing they
    // assert. The cap is this module's own contract: the third-best guess is where a blame list
    // stops reading as evidence and starts reading as the diff recited back.
    const many = Array.from({ length: 40 }, (_, i) =>
      changed(`src/Mod${i}.tsx`, { removed: ["<button>Proceed to checkout</button>"] }));
    const out = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"' }] }],
      reason: "gone", env: {}, cwd: scratch(), getDiff: () => many,
    });
    assert.equal(out.length, 2, `${out.length} suspects is the whole diff wearing the uniform of analysis`);
  });

  test("a string the diff touches in a different case is not evidence", () => {
    // Containment is the whole mechanism, so loosening it to case-insensitive is how a TODO
    // comment mentioning ORDER PLACED becomes a confident claim about an "Order placed" banner.
    const out = scoreSuspects({ strings: [{ text: "Order placed", kind: "proof" }], routes: [] }, [
      changed("src/analytics.ts", { removed: ["  // TODO: rename the ORDER PLACED event"] }),
    ]);
    assert.deepEqual(out, [], "a case-folded coincidence is not the string the test proved itself with");
  });

  test("a run that visited /app does not flag every file under a Next.js app/ directory", () => {
    // The measured incident, reached the way production reaches it — through collectFacts, which
    // is where the stop list has to bite. Asserting on a hand-built route with no segments proved
    // only that an empty list matches nothing.
    const facts = collectFacts({ url: "https://pr-42.example.com/app" });
    assert.deepEqual(facts.routes, [], "app/ is a directory a framework imposed, not a feature's name");
    assert.deepEqual(scoreSuspects(facts, [
      changed("app/layout.tsx", { removed: ["<body>"] }),
      changed("app/page.tsx", { removed: ["<main>"] }),
      changed("app/globals.css", { removed: [":root {}"] }),
    ]), []);
  });

  test("a changed file that carries no lines at all is scored, not thrown on", () => {
    // A binary blob and a pure rename both reach the scorer with nothing to match, and gitDiff's
    // fallback record for a file --name-only listed but the patch did not can arrive barer still.
    // A throw here is swallowed upstream, so the whole run silently loses its hint.
    const facts = { strings: [{ text: "Proceed to checkout", kind: "click" }], routes: [{ path: "/checkout", segments: ["checkout"] }] };
    assert.doesNotThrow(() => scoreSuspects(facts, [{ file: "public/hero.png" }, { file: "src/checkout.tsx" }]));
    assert.deepEqual(scoreSuspects(facts, [{ file: "public/hero.png" }]), []);
    assert.equal(scoreSuspects(facts, [{ file: "src/checkout.tsx" }])[0]?.file, "src/checkout.tsx");
  });

  test("every evidence sentence names its own proof and never hedges", () => {
    // The honesty spine, checked on the output rather than on the phrasebook: a suspicion that
    // renders without its evidence clause drags the verdict beside it down.
    const facts = collectFacts({
      runs: [{ steps: [
        { do: `click button "Proceed to checkout"${dim(" 5ms")}`, ok: true, ms: 5 },
        { do: `fill "Coupon code" = "SAVE10"${dim(" 5ms")}`, ok: true, ms: 5 },
        { do: `goto https://pr-42.example.com/checkout${dim(" 5ms")}`, ok: true, ms: 5 },
      ] }],
      reason: 'The page never showed "Order confirmed".',
      plan: { startUrl: "https://pr-42.example.com/", proof: "Order placed", steps: [] },
    });
    const out = scoreSuspects(facts, [
      changed("src/Checkout.tsx", { removed: ["<button>Proceed to checkout</button>"] }),
      changed("src/Coupon.tsx", { removed: ['<input aria-label="Coupon code" />'] }),
      changed("src/Done.tsx", { removed: ["<h2>Order placed</h2>"] }),
      changed("src/Toast.tsx", { added: ['toast("Order confirmed")'] }),
      changed("src/router.ts", { removed: ['redirect("/checkout")'] }),
      changed("src/routes/checkout/panel.ts", { removed: ["export const panel = 1;"] }),
    ]);
    assert.equal(out.length, 6, JSON.stringify(out));
    for (const s of out) {
      assert.doesNotMatch(s.evidence, /\b(probably|likely|may have|might|possibly|perhaps|seems|appears|suspect(?:ed)?|guess)\b/i,
        `a hedge is a suspicion without evidence: ${s.evidence}`);
      assert.match(s.evidence, /"[^"]+"|\/[A-Za-z0-9]/, `no string and no path named: ${s.evidence}`);
      assert.match(s.evidence, /^this PR (removed|added a line containing)|^its path matches/, s.evidence);
    }
  });
});

// ---- what the base branch did is never this pull request's fault -------------------------------

describe("the diff is what THIS pull request changed", () => {
  test("a file a teammate changed on the base branch after the cut is never blamed here", () => {
    // Two-dot would list it, and it would score 30 and print as "this PR removed the string
    // 'Proceed to checkout' this test clicks" on a pull request that only touched documentation.
    // A confident wrong blame on a stranger's pull request is the worst thing this module can do.
    const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
    sh(repo, "init", "-q", "-b", "main");
    mkdirSync(path.join(repo, "src"), { recursive: true });
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "<button>Proceed to checkout</button>\n");
    writeFileSync(path.join(repo, "src", "Docs.jsx"), "<p>docs</p>\n");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "v1");
    sh(repo, "checkout", "-q", "-b", "pr");
    writeFileSync(path.join(repo, "src", "Docs.jsx"), "<p>better docs</p>\n");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "this PR: documentation only");
    sh(repo, "checkout", "-q", "main");
    writeFileSync(path.join(repo, "src", "Checkout.jsx"), "<button>Continue to payment</button>\n");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "a teammate renames the button");
    sh(repo, "checkout", "-q", "pr");

    const suspects = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: 'On /cart, the button named "Proceed to checkout" is not on the page.',
      env: { GITHUB_BASE_REF: "main" }, cwd: repo,
    });
    assert.deepEqual(suspects, [], "src/Checkout.jsx belongs to the base branch, not to this pull request");
  });

  test("a renamed file is named once, by the name it has now", () => {
    // Without rename detection on both git calls, --name-only also lists the path the file used to
    // have — and a dead path matching a visited route would put a file that no longer exists on
    // the comment as a suspect.
    const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
    sh(repo, "init", "-q", "-b", "main");
    mkdirSync(path.join(repo, "src"), { recursive: true });
    writeFileSync(path.join(repo, "src", "checkout.jsx"), "export const a = 1;\n".repeat(40));
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "v1");
    sh(repo, "checkout", "-q", "-b", "pr");
    sh(repo, "mv", "src/checkout.jsx", "src/payment.jsx");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "rename");
    const files = gitDiff({ env: { GITHUB_BASE_REF: "main" }, cwd: repo });
    assert.deepEqual(files.map((f) => f.file), ["src/payment.jsx"], JSON.stringify(files.map((f) => f.file)));
  });

  test("a file whose name is not ASCII is still named, with its real path", () => {
    // git quotes and octal-escapes such a path by default: `"src/caf\303\251.jsx"` from
    // --name-only and `diff --git "a/src/caf\303\251.jsx" …` in the patch, which the header
    // pattern misses. The one file that removed the clicked string went unnamed.
    const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
    sh(repo, "init", "-q", "-b", "main");
    mkdirSync(path.join(repo, "src"), { recursive: true });
    writeFileSync(path.join(repo, "src", "café.jsx"), "<button>Proceed to checkout</button>\n");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "v1");
    sh(repo, "checkout", "-q", "-b", "pr");
    writeFileSync(path.join(repo, "src", "café.jsx"), "<button>Continue to payment</button>\n");
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "rename the button");
    const suspects = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: 'The button named "Proceed to checkout" is gone.',
      env: { GITHUB_BASE_REF: "main" }, cwd: repo,
    });
    assert.equal(suspects[0]?.file, "src/café.jsx", JSON.stringify(suspects));
    assert.equal(suspects[0]?.evidence, 'this PR removed the string "Proceed to checkout" this test clicks');
  });

  test("no git on PATH at all is silence, not a crash", () => {
    // A container image with no git, which is an ordinary way to run a preview test. The child is
    // the only honest way to prove it: spawnSync inherits this process's PATH.
    const repo = makeRepo();
    const r = spawnSync(process.execPath, ["-e", `
      import(${JSON.stringify(new URL("../lib/suspect.mjs", import.meta.url).href)}).then((m) => {
        const out = m.suspectsForFailure({
          runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
          reason: 'the button named "Proceed to checkout" is gone',
          env: process.env, cwd: ${JSON.stringify(repo)},
        });
        console.log(JSON.stringify({ diff: m.gitDiff({ env: process.env, cwd: ${JSON.stringify(repo)} }), out }));
      });`], { env: { ...process.env, PATH: "/nonexistent-bin", GITHUB_BASE_REF: "main" }, encoding: "utf8" });
    assert.equal(r.status, 0, `it must not crash: ${r.stderr}`);
    assert.deepEqual(JSON.parse(r.stdout), { diff: null, out: [] });
  });

  test("a shallow clone that never fetched the base is silence, and a detached HEAD still works", () => {
    const origin = makeRepo();
    const into = path.join(scratch(), "shallow");
    const cloned = spawnSync("git", ["clone", "-q", "--depth", "1", `file://${origin}`, into], { encoding: "utf8" });
    assert.equal(cloned.status, 0, cloned.stderr);
    assert.equal(gitDiff({ env: { GITHUB_BASE_REF: "main" }, cwd: into }), null, "no merge base means no diff, and no diff means no hint");
    assert.deepEqual(suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: "it broke", env: { GITHUB_BASE_REF: "main" }, cwd: into,
    }), []);

    // Actions checks a pull request out detached at the merge commit far more often than not.
    const detached = makeRepo();
    sh(detached, "checkout", "-q", "--detach");
    assert.equal(suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: 'the button named "Proceed to checkout" is gone',
      env: { GITHUB_BASE_REF: "main" }, cwd: detached,
    })[0]?.file, "src/Checkout.jsx");
  });

  test("three hundred changed files stay two suspects and a comment GitHub will accept", () => {
    const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
    sh(repo, "init", "-q", "-b", "main");
    mkdirSync(path.join(repo, "src"), { recursive: true });
    // Every one of them touches the clicked string, so nothing but the cap keeps this honest.
    for (let i = 0; i < 300; i++) writeFileSync(path.join(repo, "src", `Mod${i}.jsx`), `<button>Proceed to checkout</button> // ${i}\n`);
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "v1");
    sh(repo, "checkout", "-q", "-b", "pr");
    for (let i = 0; i < 300; i++) writeFileSync(path.join(repo, "src", `Mod${i}.jsx`), `<button>Continue</button> // ${i}\n`);
    sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "v2");

    const env = { GITHUB_BASE_REF: "main" };
    assert.equal(gitDiff({ env, cwd: repo }).length, 300);
    const suspects = suspectsForFailure({
      runs: [{ steps: [{ do: 'click button "Proceed to checkout"', ok: false }] }],
      reason: 'On /cart, the button named "Proceed to checkout" is not on the page.',
      url: "http://127.0.0.1:4173/cart", env, cwd: repo,
    });
    assert.equal(suspects.length, 2, JSON.stringify(suspects));

    const body = commentBody([{ name: "A shopper can pay", file: "tests/a.md", status: "failed", mode: "agent", ms: 9000,
      reason: "On /cart, clicking Proceed to checkout stayed on /cart and showed no error.", suspects }], { suite: "tests" });
    assert.ok(body.length <= 65_536, `${body.length} characters is a 422 and no comment at all`);
    assert.equal((body.match(/> Suspect:/g) || []).length, 2);
    assert.ok(!body.includes("Mod2.jsx"), "the third file that matched must not appear anywhere");
  });
});

// ---- the five statuses stay five ----------------------------------------------------------------

describe("blame belongs to failed and to nothing else", () => {
  const suspect = { file: "src/Checkout.tsx", evidence: 'this PR removed the string "Proceed to checkout" this test clicks' };

  for (const status of ["stale", "errored", "flaky", "passed"]) {
    test(`a ${status} row carrying suspects still renders none`, () => {
      // runSuite fills the field on failed rows only, but commentBody renders errored, stale and
      // flaky through the same loop. Blame under any of them blurs a status: errored is our runner
      // breaking, stale cannot tell a rename from a removal, and flaky pinned nothing down.
      const body = commentBody([
        { name: "A shopper can pay", file: "tests/a.md", status, mode: "agent", ms: 900, reason: "something happened", suspects: [suspect] },
      ], { suite: "tests" });
      assert.ok(!body.includes("Suspect:"), `${status} must never carry blame:\n${body}`);
      assert.ok(!body.includes("src/Checkout.tsx"), body);
    });
  }

  test("runSuite never even asks for suspects unless the verdict is failed", async () => {
    const asked = [];
    for (const status of ["stale", "errored", "flaky", "passed"]) {
      const [row] = await runSuite({
        tests: [{ file: "tests/a.md", name: "A", test: "buy", id: "a", planPath: ".rec/a.json" }],
        url: "https://x.test", log: () => {}, mkdir: () => {}, hasKey: true,
        findSuspects: () => { asked.push(status); return [suspect]; },
        runTest: async ({ onRun }) => { onRun({ status, mode: "agent", reason: "r" }); return status === "passed" ? 0 : 2; },
      });
      assert.deepEqual(row.suspects, [], `a ${status} row must carry no blame`);
    }
    assert.deepEqual(asked, [], "stale is explicitly not blame, and errored is not about the app at all");
  });
});

// ---- a path names a whole page, and a page is not a file ---------------------------------------

describe("path evidence says something true or it says nothing", () => {
  test("a visited route does not match the head of a longer one", () => {
    // Measured: a run that visited /cart, against a PR whose only change was
    // `const url = "/cart-abandoned";`, printed `this PR removed "/cart", a path this test
    // visited` as its single suspect. That PR did not touch /cart.
    const facts = collectFacts({ runs: [{ steps: [{ do: "goto http://127.0.0.1:4173/cart", ok: true, ms: 5 }] }] });
    assert.deepEqual(facts.strings, [{ text: "/cart", kind: "path" }], JSON.stringify(facts));
    assert.deepEqual(scoreSuspects(facts, [changed("src/emails.ts", { removed: ['  const url = "/cart-abandoned";'] })]), []);
    assert.deepEqual(scoreSuspects(facts, [changed("src/nav.ts", { removed: ['  <a href="/shop/cart">Cart</a>'] })]), [],
      "a route that ends in /cart is not the route /cart either");
    // …and the honest match still lands.
    const [hit] = scoreSuspects(facts, [changed("src/router.ts", { removed: ['  redirect("/cart");'] })]);
    assert.equal(hit.evidence, 'this PR removed "/cart", a path this test visited');
  });

  test("a filesystem path quoted out of a crash is never called a page this test visited", () => {
    // A dev error overlay is a thing the agent is told to describe, and its stack frames look
    // exactly like routes. This one became a route with the segments users, dana, shop and cartts,
    // and then blamed src/users/profile.ts on a segment the customer's home directory supplied.
    const facts = collectFacts({
      reason: "On the cart page the app crashed: TypeError: undefined is not a function at /Users/dana/shop/src/users/cart.ts:12:3",
    });
    assert.deepEqual(facts.routes, [], JSON.stringify(facts.routes));
    assert.deepEqual(scoreSuspects(facts, [changed("src/users/profile.ts", { removed: ["export const x = 1;"] })]), []);
  });

  test("a real URL keeps its extension: /sitemap.xml is a page somebody tests", () => {
    const facts = collectFacts({ url: "https://pr-42.example.com/sitemap.xml" });
    assert.deepEqual(facts.strings, [{ text: "/sitemap.xml", kind: "path" }], JSON.stringify(facts));
    const [hit] = scoreSuspects(facts, [changed("src/seo.ts", { removed: ['  register("/sitemap.xml");'] })]);
    assert.equal(hit?.evidence, 'this PR removed "/sitemap.xml", a path this test visited');
  });
});

// ---- end to end with the STEP as the only fact --------------------------------------------------

test("a laconic failure still names the culprit, from what the browser did rather than what it wrote", noBrowser, async () => {
  // The test above it survives on the agent's prose: it goes straight to finish and quotes the
  // control, so every fact comes from the reason. This one is the case the wire labels exist for.
  // The pull request disables the checkout button — the markup line carrying "Proceed to checkout"
  // is rewritten, so the diff touches the string — and the agent, having clicked a button that did
  // nothing, writes a bug report that quotes NOTHING. The single fact available is the label of a
  // real click by a real Chromium, which is precisely the fact that was being dropped.
  const repo = mkdtempSync(path.join(tmpdir(), "smolanalytics-suspect-repo-"));
  sh(repo, "init", "-q", "-b", "main");
  mkdirSync(path.join(repo, "src"), { recursive: true });
  writeFileSync(path.join(repo, "src", "Checkout.jsx"), '  <button onClick={go}>Proceed to checkout</button>\n');
  writeFileSync(path.join(repo, "src", "About.jsx"), "  <p>About us</p>\n");
  sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "shop");
  sh(repo, "checkout", "-q", "-b", "pr");
  writeFileSync(path.join(repo, "src", "Checkout.jsx"), '  <button onClick={go} disabled>Proceed to checkout</button>\n');
  writeFileSync(path.join(repo, "src", "About.jsx"), "  <p>About the team</p>\n");
  sh(repo, "add", "."); sh(repo, "commit", "-q", "-m", "guard the checkout button");

  // The PR's build: the button is there and clickable, and clicking it does nothing at all.
  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end("<!doctype html><title>Shop</title><h1>Your cart</h1><button>Proceed to checkout</button>");
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}/`;

  const cwd = process.cwd();
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const lines = [];
  let commented = null;
  let turn = 0;
  try {
    process.chdir(repo);
    globalThis.fetch = async (target, init = {}) => {
      if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
      assert.match(String(target), /api\.anthropic\.com/, "nothing but the model and the app may be called here");
      turn++;
      const call = turn === 1
        // e1 is the heading; e2 is the button. flatten() lists them in the page's own order.
        ? { name: "click", input: { ref: "e2", why: "start the checkout" } }
        // Not one quotable string in it: no control name, no path, no page text.
        : { name: "finish", input: { passed: false, why: "Nothing happened after the click and the page did not change.", proof: "" } };
      return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content: [{ type: "tool_use", id: `t${turn}`, ...call }] }) };
    };
    const code = await suiteCmd({
      test: "the cart can be checked out", url, retries: 0, comment: true,
      plans: path.join(repo, ".smolanalytics", "recordings"), evidenceDir: path.join(repo, ".smolanalytics", "evidence"),
      log: (...a) => lines.push(a.join(" ")),
      env: { ANTHROPIC_API_KEY: "sk-ant-test", GITHUB_BASE_REF: "main" },
      postCommentImpl: async ({ body }) => { commented = body; return { posted: true, updated: false, url: "" }; },
    });
    assert.equal(code, 1, "a hint never softens a verdict");
    const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
    assert.match(out, /suspect: src\/Checkout\.jsx — this PR removed the string "Proceed to checkout" this test clicks/, out);
    assert.ok(!out.includes("About.jsx"), "the other changed file is connected to nothing");
    assert.match(commented, /> Suspect: `src\/Checkout\.jsx` — this PR removed the string "Proceed to checkout" this test clicks/, commented);
    assert.ok(!commented.includes("About.jsx"), commented);
  } finally {
    globalThis.fetch = realFetch;
    process.chdir(cwd);
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
    await new Promise((r) => server.close(() => r()));
  }
});
