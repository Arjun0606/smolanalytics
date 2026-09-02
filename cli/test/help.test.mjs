// HELP THAT FOLDS ITSELF, AND A VERSION THERE WAS NO WAY TO ASK FOR.
//
// Two defects, both measured against the published binary, both invisible to every test that
// existed because every test read the text and none of them measured it:
//
//   1. 31 of the help's 80 lines were over 80 columns and the longest was 153. An 80-column
//      terminal folds those itself, in column 1, so the flag column collapses and a continuation
//      runs underneath the next flag's name.
//   2. `--version`, `-v`, `-V` and `version` each answered "unknown command", then dumped the
//      whole help, and exited 2. The MCP server said version "local" — the same string on every
//      machine that has ever run it.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { wrapHelp, visible } from "../lib/help.mjs";
import { packageVersion } from "../lib/version.mjs";
import { SERVER } from "../lib/mcp.mjs";

const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
const cli = (...argv) => {
  const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 60_000 });
  return { status: r.status, out: r.stdout, err: r.stderr };
};
const lines = (s) => visible(s).split("\n");
const widest = (s) => Math.max(...lines(s).map((l) => l.length));

describe("the help fits the terminal it is printed into", () => {
  for (const argv of [[], ["--help"], ["test", "--help"], ["suggest", "--help"], ["audit", "--help"],
    ["desk", "--help"], ["init", "--help"], ["connect", "--help"], ["plan", "--help"], ["mcp", "--help"],
    ["guard", "--help"]]) {
    test(`\`smolanalytics ${argv.join(" ")}\` has no line over 80 columns`, () => {
      const out = cli(...argv).out;
      const long = lines(out).filter((l) => l.length > 80);
      assert.deepEqual(long, [], `${long.length} line(s) will be folded by the terminal, in column 1`);
    });
  }

  test("a folded description keeps the flag column, so the flag list is still a list", () => {
    // The exact line the bug was measured on. Folded by the terminal it read:
    //   --workers <n>         with --suite: run this many tests at once (default: meas
    //   ured from cores, memory and whether a key is set; 1 is one at a time)
    const row = `  --workers <n>         with --suite: run this many tests at once (default: measured from cores, memory and whether a key is set; 1 is one at a time)`;
    const out = wrapHelp(row, 80).split("\n");
    assert.ok(out.length > 1, "the line was long enough to need folding and was not folded");
    assert.ok(!/meas$/.test(out[0]), "folded mid-word");
    for (const cont of out.slice(1)) {
      assert.match(cont, /^ {24}\S/, `a continuation started outside the description column: ${JSON.stringify(cont)}`);
    }
    // Nothing may be lost or invented by folding.
    assert.equal(out.join(" ").replace(/\s+/g, " ").trim(), row.replace(/\s+/g, " ").trim());
  });

  test("a flag whose own name holds two spaces still hangs under the description", () => {
    // `--url  <url>` has a double space INSIDE the flag, so taking the first gap indented the
    // continuation under `<url>` instead of under the description.
    const row = `  --url  <url>          a browser walks a few pages and proposes the flows it can SEE, which is the only kind it can propose`;
    const out = wrapHelp(row, 80).split("\n");
    assert.ok(out.length > 1);
    for (const cont of out.slice(1)) assert.match(cont, /^ {24}\S/, JSON.stringify(cont));
  });

  test("a sentence with no flag column hangs at its own indent", () => {
    const row = `  SMOLANALYTICS_SEED_SECRET and SMOLANALYTICS_TEARDOWN_SECRET both arrive at your endpoint as the Authorization header, which is how it tells our POST from anyone else's`;
    const out = wrapHelp(row, 80).split("\n");
    assert.ok(out.length > 1);
    for (const cont of out.slice(1)) assert.match(cont, /^ {2}\S/, JSON.stringify(cont));
  });

  test("colour survives the fold, and the width is measured without it", () => {
    const row = `  \x1b[2m--retries <n>\x1b[0m         ${"word ".repeat(30)}`;
    const out = wrapHelp(row, 80);
    assert.ok(out.includes("\x1b[2m"), "the colour codes were stripped rather than carried");
    assert.ok(widest(out) <= 80, `measured the escape codes as width: ${widest(out)}`);
  });

  test("a line already inside the width is returned byte for byte", () => {
    for (const row of ["  --headed              watch it happen", "", "Docs: https://smolanalytics.com/docs"]) {
      assert.equal(wrapHelp(row, 80), row);
    }
  });

  test("folding is idempotent, because the usage blocks are wrapped where they are printed and again on --help", () => {
    const once = wrapHelp(cli("test", "--help").out, 80);
    assert.equal(wrapHelp(once, 80), once);
  });

  test("a token longer than the width is never broken", () => {
    const url = "https://staging.example.com/a/very/long/path/that/nobody/should/have/to/reassemble/by/hand";
    const out = wrapHelp(`  --url <url>   ${url}`, 80);
    assert.ok(out.includes(url), "a URL was folded into pieces the reader has to glue back together");
  });

  test("a wider terminal is used, up to a hundred columns", () => {
    const row = `  --since <ref>         ${"word ".repeat(40)}`;
    assert.ok(widest(wrapHelp(row, 120)) <= 100, "no cap: a 200-column window turns a flag list into a paragraph");
    assert.ok(widest(wrapHelp(row, 100)) > 80, "a wide terminal is still folded at 80");
  });
});

describe("the version, which there was no way to ask for", () => {
  const declared = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8")).version;

  for (const arg of ["--version", "-v", "-V", "version"]) {
    test(`\`smolanalytics ${arg}\` prints it and exits 0`, () => {
      const r = cli(arg);
      assert.equal(r.status, 0, `${r.out}${r.err}`);
      assert.equal(r.out.trim(), declared, "the bare number, so a script can read it");
      assert.ok(!/unknown command/.test(r.err), r.err);
      // The whole help on top of a one-word answer is where the answer goes to hide.
      assert.ok(!/end-to-end tests without test code/.test(r.out + r.err), "the help was dumped over the version");
    });
  }

  test("it is read from package.json, so there is no second place to bump", () => {
    assert.equal(packageVersion(), declared);
  });

  test("an unreadable manifest is answered, never thrown", () => {
    assert.equal(packageVersion(() => { throw new Error("ENOENT"); }), "unknown");
    assert.equal(packageVersion(() => "{not json"), "unknown");
    assert.equal(packageVersion(() => "{}"), "unknown");
  });

  test("the editor is told the same number, instead of the string \"local\"", () => {
    assert.equal(SERVER.version, declared, "MCP serverInfo drifted from the package");
    assert.notEqual(SERVER.version, "local");
  });

  test("the help names it too, since that is where somebody looks first", () => {
    assert.ok(cli("--help").out.includes(declared), "the no-args help does not say which version it is");
  });
});
