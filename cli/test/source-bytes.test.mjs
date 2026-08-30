// NO CONTROL BYTES IN SOURCE — the defect that makes a file invisible to the tools that review it.
//
// This has now bitten twice. Both times the byte was written on purpose and worked at runtime:
//
//   lib/layout.mjs — a NUL byte inside a string literal
//   lib/suspect.mjs — `${cwd}` + a raw NUL + `${env.GITHUB_BASE_REF}` as a cache-key separator
//
// Both ran correctly forever. What they broke is everything that reads the file AS TEXT: git calls
// it binary, so `git diff` on it renders "Binary files a/… and b/… differ" and every future change
// to that file is invisible in review; grep skips it; `file` reports `data`. On lib/suspect.mjs, of
// all files — the one whose whole job is producing blame a person is supposed to trust — nobody
// could have reviewed a change to it.
//
// The fix in both cases was the escape (`\u0000`), which is the identical string at runtime and
// plain text on disk. So there is no cost to this rule and no legitimate exception in a .mjs file,
// which is why the test is a flat ban rather than a warning.
//
// Tab, newline and carriage return are the three control bytes that belong in source.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const ALLOWED = new Set([0x09, 0x0a, 0x0d]);

/** Every .mjs and .js file we ship or test with, walked from the package root. */
function sources(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name.startsWith(".")) continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) sources(p, out);
    else if (/\.(mjs|js|json)$/.test(e.name)) out.push(p);
  }
  return out;
}

test("no source file contains a control byte that would make git call it binary", () => {
  const bad = [];
  for (const file of sources(ROOT)) {
    const buf = readFileSync(file);
    for (let i = 0; i < buf.length; i++) {
      const b = buf[i];
      if (b < 0x20 && !ALLOWED.has(b)) {
        const line = buf.subarray(0, i).toString("utf8").split("\n").length;
        bad.push(`${file.slice(ROOT.length + 1)}:${line} — byte 0x${b.toString(16).padStart(2, "0")}`);
        break;
      }
    }
  }
  assert.deepEqual(
    bad,
    [],
    `these files are binary to git, so changes to them cannot be reviewed. Write the byte as an escape ` +
      `(\\u0000, \\b, \\x1b) — identical at runtime, plain text on disk:\n  ${bad.join("\n  ")}`,
  );
});

test("the check would actually fail if a control byte appeared", () => {
  // A hygiene test that cannot fail is worse than no hygiene test: it reports clean forever. This
  // runs the same predicate over a buffer that definitely contains one.
  const withNul = Buffer.from(`const key = \`x${String.fromCharCode(0)}y\`;\n`, "utf8");
  const found = [...withNul].some((b) => b < 0x20 && !ALLOWED.has(b));
  assert.equal(found, true);
  // And the escaped form, which is what the fix looks like, passes.
  const escaped = Buffer.from("const key = `x\\u0000y`;\n", "utf8");
  assert.equal([...escaped].some((b) => b < 0x20 && !ALLOWED.has(b)), false);
  // The two are the same string once JavaScript has read them.
  assert.equal(eval(withNul.toString("utf8").replace("const key = ", "")), eval(escaped.toString("utf8").replace("const key = ", "")));
});
