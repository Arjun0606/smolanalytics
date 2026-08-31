// A FLAG THIS CLI DOES NOT READ MUST BE AN ERROR THAT NAMES IT, NEVER A SILENT NO-OP.
//
// bin/smolanalytics.mjs refuses every bad flag VALUE — --retries, --layout, --browser, --workers,
// --since, --max-calls each carry a comment saying why a silent default is worse than a refusal.
// Flag NAMES had no guard at all, so the same mistake one character earlier was free. Measured, by
// typing them at the real binary:
//
//   --tset "the pricing page works"   printed `test`'s usage block with no mention of --tset
//   --urls https://staging.myapp.com  the same, and --url then looked missing
//   --reties 3                        ran, with one retry, in silence
//   --headles                         ran headless, in silence
//
// Two requirements, and the second is the one that rots:
//   1. an unrecognised --flag stops the command and names the flag it is one edit away from
//   2. every flag the help text or the shipped workflow actually uses is recognised — a guard whose
//      list falls behind the help would refuse a flag that works, which is worse than the bug.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
const plain = (s) => String(s).replace(/\x1b\[[0-9;]*m/g, "");
const cli = (...argv) => {
  const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 60_000 });
  return { status: r.status, out: plain(r.stdout), err: plain(r.stderr) };
};

describe("a flag nobody defined", () => {
  test("stops the command and names the flag it is one edit away from", () => {
    const near = [
      [["test", "--url", "https://x.example", "--tset", "it works"], "--tset", "--test"],
      [["test", "--urls", "https://x.example", "--test", "it works"], "--urls", "--url"],
      [["test", "--url", "https://x.example", "--test", "t", "--reties", "3"], "--reties", "--retries"],
      [["test", "--url", "https://x.example", "--test", "t", "--headles"], "--headles", "--headed"],
      [["suggest", "--url", "https://x.example", "--mx", "3"], "--mx", "--max"],
      [["audit", "--jsno"], "--jsno", "--json"],
    ];
    for (const [argv, typed, meant] of near) {
      const r = cli(...argv);
      assert.match(r.err, new RegExp(`unknown option ${typed}\\b`), `${argv.join(" ")}\n${r.err}`);
      assert.match(r.err, new RegExp(`Did you mean ${meant}\\?`), `${argv.join(" ")}: no fix named\n${r.err}`);
    }
  });

  test("the =value shape is caught too, and a flag near nothing gets no invented guess", () => {
    const r = cli("test", "--url=https://x.example", "--test=t", "--frobnicate");
    assert.match(r.err, /unknown option --frobnicate/);
    assert.ok(!/Did you mean/.test(r.err), `a confidently wrong suggestion is worse than none:\n${r.err}`);
    assert.match(r.err, /--help/, "with no guess to offer, say where the list is");
  });

  test("nothing else is printed, and nothing is run", () => {
    // The old behaviour: the usage block, which the reader then has to diff against their command.
    const r = cli("test", "--url", "https://x.example", "--tset", "it works");
    assert.ok(!/--evidence-dir/.test(r.out + r.err), `the usage dump is back:\n${r.out}${r.err}`);
    assert.ok(!/smoltest/.test(r.out + r.err), "an identity was generated for a run that cannot happen");
    assert.ok(!/\bPASS\b|\bFAIL\b/.test(r.out), "a typo produced a verdict");
  });

  test("a typo in a guard's own name never quietly disarms the guard", () => {
    // --no-render-check turns OFF the false-green guard. Typed wrong it used to be ignored, which
    // is harmless; typed wrong the OTHER way round — a person disabling something and finding it
    // still on — is the same silence. Either way the run must not proceed under a flag nobody read.
    const r = cli("test", "--url", "https://x.example", "--test", "t", "--no-render-chek");
    assert.equal(r.status, 2, "our refusal is exit 2 on `test`; 1 says the customer's app is broken");
    assert.match(r.err, /unknown option --no-render-chek/);
  });

  test("exit 2 on `test`, whose 1 is a published contract, and 1 everywhere else", () => {
    assert.equal(cli("test", "--url", "https://x.example", "--zzzz").status, 2);
    assert.equal(cli("audit", "--zzzz").status, 1);
    assert.equal(cli("suggest", "--url", "https://x.example", "--zzzz").status, 1);
  });
});

describe("the guard's list cannot fall behind what we document", () => {
  // THE ROT THIS CATCHES: a flag added to the help and forgotten here would be REFUSED — a working
  // flag turned into an error by a guard that was supposed to help. Read off the help text the
  // binary itself prints, so the two cannot drift.
  const help = plain(cli("--help").out);
  const sections = {};
  let cur = "";
  for (const line of help.split("\n")) {
    const head = /npx smolanalytics (\w+)/.exec(line);
    if (head) cur = head[1];
    if (!cur) continue;
    for (const m of line.matchAll(/(?:^|\s)(--[a-z][a-z-]*)/g)) (sections[cur] ||= new Set()).add(m[1]);
  }

  // `audit` is not here: its help block documents a positional [dir] and no flags at all.
  const documented = ["test", "suggest", "connect", "desk", "init", "plan"];

  test("the help really did list flags for the commands that take them", () => {
    // Without this, a help text that stopped printing flags would make every case below vacuous.
    for (const cmd of documented) assert.ok(sections[cmd]?.size, `no flags parsed out of the help for \`${cmd}\``);
    assert.ok(sections.test.has("--url") && sections.test.has("--no-render-check"), "the test section did not parse");
    assert.ok(sections.test.size > 15, `only ${sections.test.size} flags parsed for \`test\` — the help shape changed`);
  });

  for (const cmd of documented) {
    test(`every flag the help lists under \`${cmd}\` is accepted`, () => {
      for (const f of sections[cmd] || []) {
        const r = cli(cmd, f);
        assert.ok(!/unknown option/.test(r.err), `\`smolanalytics ${cmd} ${f}\` is documented but refused:\n${r.err}`);
      }
    });
  }

  test("every flag the shipped GitHub Actions workflow uses is accepted", () => {
    // Only OUR invocations. The template also documents `npx serve --listen` and `npx wait-on
    // --timeout` in its comments, and those are not flags of ours to accept.
    const yml = readFileSync(new URL("../templates/github-action.yml", import.meta.url), "utf8");
    const calls = [...yml.matchAll(/smolanalytics(?:@[\w.-]+)?\s+test\b([^\n]*)/g)].map((m) => m[1]);
    assert.ok(calls.length, "no `smolanalytics test` invocation found in the workflow template");
    const used = new Set(calls.flatMap((c) => [...c.matchAll(/(?:^|\s)(--[a-z][a-z-]*)/g)].map((m) => m[1])));
    assert.ok(used.size, "no flags found on the workflow's own command — this check would pass on an empty file");
    for (const f of used) {
      const r = cli("test", f);
      assert.ok(!/unknown option/.test(r.err), `the workflow we ship uses ${f} and the CLI refuses it:\n${r.err}`);
    }
  });
});

// ── A MISTYPED COMMAND IS NEVER "YOUR APPLICATION IS BROKEN" ────────────────────────────────────
//
// The exit code is the one part of this CLI that another program reads. 1 means a test failed —
// the shipped GitHub workflow turns it into a bug report on a pull request. 2 means the runner
// could not start. A typo in our own name or our own flag is always the second, and both paths
// were returning the first.
//
// MEASURED against the real binary before the fix:
//   smolanalytics frobnicate                          exited 1
//   smolanalytics suggest --wat                       exited 1
// Neither opened a browser or loaded a page, and both would have posted "a test failed" in CI.

describe("a typo in our own CLI never reports the customer's app as broken", () => {
  test("an unknown command exits 2, not 1", () => {
    const r = cli("frobnicate");
    assert.equal(r.status, 2, `1 means "a test failed and their app is broken"; nothing ran here`);
    assert.match(plain(r.err), /unknown command/i);
  });

  test("a near-miss command still exits 2, even though it guesses well", () => {
    const r = cli("tset");
    assert.equal(r.status, 2);
    assert.match(plain(r.err), /did you mean/i, "the guess is the good part and must survive the fix");
  });

  test("a mistyped COMMAND is 2 even though a mistyped FLAG on audit stays 1", () => {
    // Deliberately different, and the difference is the point. `audit` is a local repo scan whose
    // 1 nothing reads as a verdict, and a test above pins that. An unknown COMMAND is the
    // dangerous one: the shipped workflow invokes us by name to run TESTS, so one wrong character
    // there produces a run that opened nothing and still says "a test failed".
    assert.equal(cli("audit", "--zzzz").status, 1, "the existing flag contract is unchanged");
    assert.equal(cli("aduit").status, 2, "but a mistyped command never claims their app is broken");
  });
});

// ── THE DID-YOU-MEAN LIST AND THE DISPATCHER MUST NOT DRIFT APART ───────────────────────────────
//
// `mcp` was implemented, documented in help, and missing from COMMANDS — so the one command an
// editor integration tells people to type was the one command whose typo got no suggestion.

describe("every command the binary dispatches can also be guessed at", () => {
  const src = readFileSync(bin, "utf8");

  test("COMMANDS lists every command main() actually handles", () => {
    const dispatched = [...src.matchAll(/cmd === "([a-z-]+)"/g)].map((m) => m[1]);
    const listed = JSON.parse((src.match(/const COMMANDS = (\[[^\]]*\])/) || [])[1].replace(/'/g, '"'));
    // The words main() compares against that are commands rather than help aliases.
    const skip = new Set(["help", "--help", "-h"]);
    const missing = [...new Set(dispatched)].filter((c) => !skip.has(c) && !listed.includes(c));
    assert.deepEqual(missing, [], `dispatched but unguessable, so a typo gets no suggestion: ${missing.join(", ")}`);
  });

  test("and a one-character typo of each of them is guessed", () => {
    for (const [typo, meant] of [["mpc", "mcp"], ["tset", "test"], ["sugest", "suggest"], ["conect", "connect"]]) {
      const r = cli(typo);
      assert.match(plain(r.err), new RegExp(`did you mean.*${meant}`, "i"), `${typo} did not suggest ${meant}`);
    }
  });
});

// ── THE ONE PREREQUISITE, NAMED WHERE SOMEBODY LOOKS FIRST ──────────────────────────────────────
//
// The no-args help documented SMOLANALYTICS_SEED_SECRET, SMOLANALYTICS_TEARDOWN_SECRET,
// SMOLANALYTICS_LOGIN_EMAIL, SMOLANALYTICS_LOGIN_PASSWORD and SMOLANALYTICS_KEY — eight variable
// mentions in all — and never once named ANTHROPIC_API_KEY, without which `test` and `suggest`
// cannot do anything. MEASURED: 0 occurrences in the no-args help and 0 in `test --help`.
//
// A newcomer learned it only after composing a whole command and being rejected. The rejection
// itself is good — it fires in about a second, does not download Chromium first, exits 2, and
// names console.anthropic.com — but it is the second thing they should read about the key, not
// the first.

test("the help names the API key everything depends on, and where to get one", () => {
  for (const argv of [[], ["test", "--help"]]) {
    const r = cli(...argv);
    const out = plain(r.out + r.err);
    assert.match(out, /ANTHROPIC_API_KEY/, `\`smolanalytics ${argv.join(" ")}\` never mentions the one key it cannot run without`);
    assert.match(out, /console\.anthropic\.com/, "naming the variable without saying where to get one is half an answer");
  }
});

test("and says the keyless path exists, so --plan users are not turned away", () => {
  // Replay needs no key and no model at all. Telling somebody a key is required, full stop, would
  // send away the users on the cheapest and fastest path we have.
  for (const argv of [[], ["test", "--help"]]) {
    const r = cli(...argv);
    const out = plain(r.out + r.err);
    assert.match(out, /--plan/, "the no-key path must be mentioned beside the requirement");
  }
});
