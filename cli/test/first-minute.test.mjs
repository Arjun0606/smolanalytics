// THE FIRST FIFTEEN MINUTES, MEASURED BY WALKING THEM.
//
// Everything here was found by running the real binary as a stranger would, and each case is the
// verbatim output that was wrong. None of it changes a verdict or an exit code; all of it is about
// what the reader is told, and when.
//
//   a fifty-test suite with no key   printed the same three-line export block fifty times, under a
//                                    header that had already said it once
//   a --plan that went stale         then said "Replaying a recording (--plan) needs no key at
//                                    all" to somebody who had just typed --plan
//   the fastest path in the product  said "replaying 1 recorded steps" and "replayed 1 steps"
//   a tests/ folder holding a file   said "An empty folder is not a passing suite" about a folder
//   with no sentence in it           the reader can see is not empty

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { count, sayIfSlow } from "../lib/test.mjs";
import { keyProblem } from "../lib/safety.mjs";

const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
const plain = (s) => String(s).replace(/\x1b\[[0-9;]*m/g, "");

// The app under test. One page, one proof, so a recording of it replays in milliseconds.
const app = createServer((_q, res) => {
  res.writeHead(200, { "content-type": "text/html" });
  res.end("<!doctype html><title>Pricing</title><h1>Pro is $29 per month</h1>");
});
await new Promise((r) => app.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${app.address().port}`;
after(() => {
  app.closeAllConnections();
  return new Promise((r) => app.close(() => r()));
});

/** spawn, never spawnSync: the app above is served from THIS event loop. */
function run(argv, env = process.env, cwd = undefined) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, argv, { env, cwd });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (d) => (stdout += d));
    child.stderr.on("data", (d) => (stderr += d));
    child.on("error", reject);
    child.on("close", (status) => resolve({ status, out: plain(stdout), err: plain(stderr) }));
  });
}

const noKey = (() => {
  const e = { ...process.env };
  delete e.ANTHROPIC_API_KEY;
  return e;
})();

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-first-"));

describe("a missing key is said once, not once per test", () => {
  test("a suite of four prints the export line once, and every test still says why it did not run", async () => {
    const dir = scratch();
    const tests = path.join(dir, "tests");
    mkdirSync(tests);
    for (const n of ["one", "two", "three", "four"]) {
      writeFileSync(path.join(tests, `${n}.md`), `# Test ${n}\n\nOpen the pricing page and check it shows a monthly price.\n`);
    }
    const r = await run([bin, "test", "--suite", tests, "--url", url, "--yes", "--workers", "1"], noKey);
    const exports = (r.out.match(/export ANTHROPIC_API_KEY/g) || []).length;
    assert.equal(exports, 1, `the export line was printed ${exports} times for four tests:\n${r.out}`);
    // Deleted output, not lost information: the header, each test's own line, and the summary.
    assert.match(r.out, /ANTHROPIC_API_KEY is not set/, r.out);
    assert.equal((r.out.match(/no ANTHROPIC_API_KEY to run the agent with/g) || []).length, 4, r.out);
    assert.equal((r.out.match(/^\s+error /gm) || []).length, 4, `every test must still be named in the summary:\n${r.out}`);
    assert.equal(r.status, 2, "nothing ran, so this is ours: exit 2, never 1");
  });

  test("a single test still gets the whole block — it is the only place it is said", async () => {
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page shows a monthly price", "--yes"], noKey);
    assert.match(r.out, /The agent needs a Claude API key/, r.out);
    assert.match(r.out, /export ANTHROPIC_API_KEY/, r.out);
  });
});

describe("advice the reader has already taken is not printed", () => {
  test("a --plan that went stale is not told that --plan needs no key", async () => {
    const dir = scratch();
    const planPath = path.join(dir, "p.json");
    // The steps still work; the proof no longer matches, which is exactly the stale path that
    // hands over to the agent — and then found no key.
    writeFileSync(planPath, JSON.stringify({
      startUrl: `${url}/`,
      steps: [{ kind: "goto", url: `${url}/` }],
      proof: "a sentence this page has never contained",
    }));
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page works", "--plan", planPath, "--yes"], noKey);
    assert.match(r.out, /the page no longer says/, `this did not reach the stale path at all:\n${r.out}`);
    assert.match(r.out, /The agent needs a Claude API key/, r.out);
    assert.ok(
      !/needs no key at all/.test(r.out),
      `told somebody who just typed --plan that --plan needs no key:\n${r.out}`,
    );
  });

  test("and a run with no --plan is still told that replaying needs none", async () => {
    // The other half: this line is how somebody learns the cheap path exists, so it may not simply
    // be deleted — only withheld where the reader is already on it.
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page works", "--yes"], noKey);
    assert.match(r.out, /Replaying a recording \(--plan\) needs no key at all/, r.out);
  });
});

describe("one of anything is not plural", () => {
  test("count() says one step and two steps", () => {
    assert.equal(count(1, "step"), "1 step");
    assert.equal(count(2, "step"), "2 steps");
    assert.equal(count(0, "step"), "0 steps");
    assert.equal(count(1, "recorded step"), "1 recorded step");
  });

  test("the fastest path in the product prints it that way", async () => {
    const dir = scratch();
    const planPath = path.join(dir, "p.json");
    writeFileSync(planPath, JSON.stringify({ startUrl: `${url}/`, steps: [{ kind: "goto", url: `${url}/` }], proof: "$29 per month" }));
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page works", "--plan", planPath, "--yes"], noKey);
    assert.match(r.out, /\bPASS\b/, `the replay did not pass, so nothing below is being measured:\n${r.out}${r.err}`);
    assert.match(r.out, /replaying 1 recorded step \(no model\)/, r.out);
    assert.match(r.out, /replayed 1 step in/, r.out);
    assert.ok(!/\b1 steps\b/.test(r.out), `"1 steps":\n${r.out}`);
  });
});

describe("a folder that is not empty is never called empty", () => {
  test("files that held no test are named, and so is what was wrong with them", async () => {
    const dir = scratch();
    const tests = path.join(dir, "tests");
    mkdirSync(tests);
    // Frontmatter and no body: parseSuite reads it, finds no sentence, and returns nothing.
    writeFileSync(path.join(tests, "buy-pro.md"), "---\ntitle: A visitor can buy Pro\n---\n");
    writeFileSync(path.join(tests, "log-in.md"), "---\ntitle: A visitor can log in\n---\n");
    const r = await run([bin, "test", "--suite", tests, "--url", url, "--yes"], noKey);
    assert.match(r.out, /no tests found/, r.out);
    assert.ok(!/empty folder/.test(r.out), `a folder holding two files was called empty:\n${r.out}`);
    assert.match(r.out, /Read 2 files/, r.out);
    assert.match(r.out, /buy-pro\.md/, "the reader must be told WHICH file was read and yielded nothing");
    assert.match(r.out, /Frontmatter alone is not one/, "and what would make it a test");
    assert.equal(r.status, 2);
  });

  test("a folder that IS empty still says so", async () => {
    const dir = scratch();
    const tests = path.join(dir, "tests");
    mkdirSync(tests);
    const r = await run([bin, "test", "--suite", tests, "--url", url, "--yes"], noKey);
    assert.match(r.out, /no tests found/, r.out);
    assert.match(r.out, /An empty folder is not a passing suite/, r.out);
    assert.ok(!/Read 0 files/.test(r.out), `there is nothing to name:\n${r.out}`);
  });
});

// -------------------------------------------------------------------------------------------------
// A SECOND WALK, 2026-08-30. Same method: run the real binary as a stranger, and write down what
// it did. Everything below is a case where the answer was worse than no answer.
// -------------------------------------------------------------------------------------------------

describe("asking what a command does is never answered by doing it", () => {
  // MEASURED: `npx smolanalytics audit --help` walked the repo and printed eleven findings, and
  // `init --help` detected Next.js and named the file it would edit. `--help` was in FLAGS for
  // every command, so it passed the typo check as known and was then read by nobody.
  const commands = [["test"], ["suggest"], ["audit"], ["desk"], ["init"], ["connect"], ["plan", "check"]];

  for (const argv of commands) {
    test(`${argv.join(" ")} --help exits 0`, async () => {
      const r = await run([bin, ...argv, "--help"], noKey);
      // Not 1 and not 2: both are published verdicts about a run, and a question that was answered
      // is neither a failing test nor a runner that could not finish.
      assert.equal(r.status, 0, `\`${argv.join(" ")} --help\` exited ${r.status}:\n${r.out}${r.err}`);
      assert.ok(/--url|npx smolanalytics/.test(r.out), `no help was printed:\n${r.out}${r.err}`);
    });
  }

  test("audit --help does not audit the repo it is standing in", async () => {
    const dir = scratch();
    // An action audit reports on. If --help scans, this filename appears in the output.
    writeFileSync(path.join(dir, "app.js"), 'export function go() { return auth.signUp({ email }); }\n');
    const r = await run([bin, "audit", "--help"], noKey, dir);
    assert.ok(!/app\.js/.test(r.out), `--help scanned the repo and reported on it:\n${r.out}`);
    // The report's own headline, not the one-line description of `audit` that the help itself
    // carries — matching on the shared phrase would fail on correct output.
    assert.ok(!/\d+ things your product does/.test(r.out), `--help printed an audit report:\n${r.out}`);
    assert.ok(!/\buntracked\b/.test(r.out), `--help printed findings:\n${r.out}`);
    assert.equal(r.status, 0);
  });

  test("init --help does not inspect the project or ask for a key", async () => {
    const dir = scratch();
    writeFileSync(path.join(dir, "package.json"), '{"name":"x","dependencies":{"next":"15.0.0"}}');
    mkdirSync(path.join(dir, "app"));
    writeFileSync(path.join(dir, "app", "layout.tsx"), "export default function L({children}){return children}\n");
    const r = await run([bin, "init", "--help"], noKey, dir);
    assert.ok(!/detected/i.test(r.out), `--help ran the detector:\n${r.out}`);
    assert.ok(!/Need your instance and write key/.test(r.out), `--help asked for credentials:\n${r.out}`);
    assert.equal(r.status, 0);
  });

  test("connect and desk answer with help, not with a complaint about a missing key", async () => {
    for (const cmd of ["connect", "desk"]) {
      const r = await run([bin, cmd, "--help"], noKey);
      assert.ok(!/needs (your MCP token|a read key)/.test(r.out + r.err),
        `\`${cmd} --help\` answered with an error about credentials:\n${r.out}${r.err}`);
      assert.equal(r.status, 0, `${cmd} --help exited ${r.status}`);
    }
  });

  test("-h is the same as --help, and is not reported as an unknown option", async () => {
    const r = await run([bin, "audit", "-h"], noKey);
    assert.equal(r.status, 0, `${r.out}${r.err}`);
    assert.ok(!/unknown option/.test(r.out + r.err), `${r.out}${r.err}`);
  });
});

describe("a run that never started never exits 1", () => {
  // 1 is published in templates/github-action as "a test failed" — the application under test is
  // broken. MEASURED: `npx smolanalytics test --url https://staging.myapp.com` with the --test flag
  // left off printed the usage block and exited 1, putting a bug report about somebody's app on a
  // pull request whose only defect was a missing flag in the workflow file. suggest already refused
  // this way; test, the command that actually runs in CI, did not.
  for (const [name, argv] of [
    ["no flags at all", ["test"]],
    ["a --url and no --test", ["test", "--url", "https://staging.example.com"]],
    ["a --test and no --url", ["test", "--test", "the pricing page shows a monthly price"]],
  ]) {
    test(`${name}: the usage is printed and the exit code is 2`, async () => {
      const r = await run([bin, ...argv], noKey);
      assert.match(r.out + r.err, /npx smolanalytics test/, `no usage was printed:\n${r.out}${r.err}`);
      assert.notEqual(r.status, 1, `exit 1 says the application under test is broken, and nothing was opened:\n${r.out}`);
      assert.equal(r.status, 2, `${r.out}${r.err}`);
    });
  }
});

describe("a mistyped command names the fix, the way a mistyped flag already did", () => {
  // MEASURED: `smolanalytics test --tset "…"` answered `Did you mean --test?` in one line, while
  // `smolanalytics tset --url … --test "…"` — the same typo, one word to the left — printed
  // `unknown command tset` and then the whole sixty-four-line help.
  for (const [typo, meant] of [["tset", "test"], ["sugest", "suggest"], ["audi", "audit"], ["conect", "connect"]]) {
    test(`${typo} suggests ${meant}`, async () => {
      const r = await run([bin, typo], noKey);
      assert.match(r.out + r.err, new RegExp(`Did you mean.*${meant}`), `no suggestion:\n${r.out}${r.err}`);
      // The guess is the answer. Sixty-four lines under it is where the answer goes to hide.
      assert.ok(!/end-to-end tests without test code/.test(r.out + r.err),
        `the full help was dumped on top of a confident guess:\n${r.out}${r.err}`);
    });
  }

  test("a word that is nobody's typo still gets the help, because there is nothing to guess", async () => {
    const r = await run([bin, "zzzzzzzz"], noKey);
    assert.ok(!/Did you mean/.test(r.out + r.err), `invented a guess:\n${r.out}${r.err}`);
    assert.match(r.out + r.err, /end-to-end tests without test code/, `no help for an unguessable command:\n${r.out}${r.err}`);
  });
});

describe("a key that cannot be sent is named, and named first", () => {
  // MEASURED. With no key the binary prints `export ANTHROPIC_API_KEY=sk-ant-…    then run this
  // again`. Copy that line verbatim — which is what a help message is for — and the next run said:
  //
  //   the run could not complete: Cannot convert argument to a ByteString because the character
  //   at index 7 has a value of 8230 which is greater than 255.
  //
  // undici refusing U+2026 in a header, after the production warning and the generated identity had
  // already been printed and a browser had already launched. Our own onboarding copy, followed
  // exactly, produced a sentence about character encoding with no fix in it.

  test("keyProblem names each way a key cannot be transmitted, and lets a real one through", () => {
    // A key that could work is never refused: this check is about the transport, not the account.
    assert.equal(keyProblem("sk-ant-api03-AAAABBBBCCCC"), "", "refused a well-formed key");
    assert.equal(keyProblem(""), "", "an absent key is the no-key path's business, not this one's");
    assert.equal(keyProblem(undefined), "");

    // The ellipsis out of our own help text. The message has to name the character, or the reader
    // is looking at a key that appears correct.
    assert.match(keyProblem("sk-ant-…"), /…/);
    assert.match(keyProblem("sk-ant-…"), /help message|example/i, "does not say where that … came from");

    // export ANTHROPIC_API_KEY=$(cat key.txt). Reached Anthropic and came back 401, sending the
    // reader to rotate a key that was fine.
    assert.match(keyProblem("sk-ant-abc\n"), /whitespace/i);
    assert.match(keyProblem(" sk-ant-abc"), /whitespace/i);

    // The wrong provider's key in the right variable.
    assert.match(keyProblem("sk-proj-abcdef"), /sk-ant-/);
    assert.match(keyProblem("hello"), /sk-ant-/);

    // NOT refused on a guess about length or about what follows the prefix — a rule written today
    // that outlives the key format it was written for refuses keys that work.
    assert.equal(keyProblem("sk-ant-x"), "");
    assert.equal(keyProblem(`sk-ant-${"z".repeat(400)}`), "");
  });

  test("the ellipsis from our own help text gets a sentence, not a ByteString error", async () => {
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page shows a monthly price", "--yes"],
      { ...process.env, ANTHROPIC_API_KEY: "sk-ant-…" });
    assert.ok(!/ByteString/.test(r.out + r.err), `undici's error still reaches the reader:\n${r.out}${r.err}`);
    assert.match(r.out + r.err, /cannot be sent as a header/, `${r.out}${r.err}`);
    // And NOT the missing-key block: they set one, and being told to set one reads as a tool that
    // cannot see the environment it is running in.
    assert.ok(!/The agent needs a Claude API key/.test(r.out), `told a reader who set a key that they need one:\n${r.out}`);
    assert.equal(r.status, 2, "the runner could not finish; this is never the app's failure");
  });

  test("nothing frightening is printed ahead of a run that cannot happen", async () => {
    // The bug this whole ordering exists to prevent, in its second shape. A production-looking URL
    // plus an unusable key used to print the twelve-line warning about real accounts and a possible
    // real charge, then a generated smoltest identity, and only then the error.
    const r = await run([bin, "test", "--url", "https://shop.example.com", "--test", "checkout works", "--yes"],
      { ...process.env, ANTHROPIC_API_KEY: "sk-ant-…" });
    assert.ok(!/real charge|creates an account/i.test(r.out),
      `warned about charges on a run that never opened anything:\n${r.out}`);
    assert.ok(!/smoltest\+/.test(r.out), `printed an identity nothing used:\n${r.out}`);
    // The actionable sentence is the FIRST thing, not the last.
    const first = r.out.split("\n").map((l) => l.trim()).filter(Boolean)[0] || "";
    assert.match(first, /ANTHROPIC_API_KEY/, `the first line was not the one that matters:\n${r.out}`);
  });

  test("a recording still replays with an unusable key in the environment", async () => {
    // The cheapest path in the product needs no key at all, so a broken one must not become a gate.
    const dir = scratch();
    const planPath = path.join(dir, "p.json");
    writeFileSync(planPath, JSON.stringify({ startUrl: `${url}/`, steps: [{ kind: "goto", url: `${url}/` }], proof: "$29 per month" }));
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page works", "--plan", planPath, "--yes"],
      { ...process.env, ANTHROPIC_API_KEY: "sk-ant-…" });
    assert.match(r.out, /\bPASS\b/, `a broken key blocked a replay that needs no key:\n${r.out}${r.err}`);
    assert.equal(r.status, 0);
  });

  test("a suite says it once, not once per test", async () => {
    // The same rule the missing-key case already follows. Without it the diagnosis — which is long,
    // because it has to name a character — was printed under every single row.
    const dir = scratch();
    const tests = path.join(dir, "tests");
    mkdirSync(tests);
    for (const n of ["one", "two", "three"]) {
      writeFileSync(path.join(tests, `${n}.md`), `# Test ${n}\n\nOpen the pricing page and check it shows a monthly price.\n`);
    }
    const r = await run([bin, "test", "--suite", tests, "--url", url, "--yes", "--workers", "1"],
      { ...process.env, ANTHROPIC_API_KEY: "sk-ant-…" });
    const said = (r.out.match(/cannot be sent as a header/g) || []).length;
    assert.equal(said, 1, `the diagnosis was printed ${said} times for three tests:\n${r.out}`);
    // Deleted output, not lost information: every test still says why it did not run.
    assert.equal((r.out.match(/ANTHROPIC_API_KEY cannot be used/g) || []).length, 3, r.out);
    assert.equal(r.status, 2, "nothing ran, so this is ours: exit 2, never 1");
  });
});

describe("a wait is explained while it is happening", () => {
  // MEASURED, replaying against a server that accepts the connection and never answers:
  //
  //   replaying 1 recorded step (no model)…
  //   <thirty seconds of nothing>
  //   http://127.0.0.1:4478/ did not finish loading within 30s.
  //
  // The verdict is right. The silence is not: a line ending in an ellipsis followed by thirty
  // seconds of nothing is indistinguishable from a hung runner, and a reader who interrupts at ten
  // seconds never learns that their own server was the thing that stopped.

  test("sayIfSlow speaks only when the wait outlives the deadline, and can always be cancelled", async () => {
    const said = [];
    const stop = sayIfSlow((l) => said.push(l), "still waiting", 10);
    await new Promise((r) => setTimeout(r, 60));
    stop();
    assert.equal(said.length, 1, "a wait past the deadline said nothing");
    assert.match(said[0], /still waiting/);

    // The ordinary run: cancelled long before it fires, and silent.
    const quiet = [];
    sayIfSlow((l) => quiet.push(l), "still waiting", 10_000)();
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(quiet, [], "a fast page was made to explain a wait that never happened");

    // No log to write to is not a crash.
    assert.doesNotThrow(() => sayIfSlow(null, "x", 1)());
  });

  test("a page that loads gains no line at all", async () => {
    const dir = scratch();
    const planPath = path.join(dir, "p.json");
    writeFileSync(planPath, JSON.stringify({ startUrl: `${url}/`, steps: [{ kind: "goto", url: `${url}/` }], proof: "$29 per month" }));
    const r = await run([bin, "test", "--url", url, "--test", "the pricing page works", "--plan", planPath, "--yes"], noKey);
    assert.match(r.out, /\bPASS\b/, `${r.out}${r.err}`);
    assert.ok(!/still waiting/.test(r.out), `an instant replay explained a wait it never had:\n${r.out}`);
  });
});
