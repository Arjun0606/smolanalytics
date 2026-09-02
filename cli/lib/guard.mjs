// `npx smolanalytics guard` — what this repository needs that nothing declares.
//
// No key, no account, no network, no config. It reads the repo it is standing in, the same promise
// lib/audit.mjs makes, and for the same reason: a check that needs a signup before it can say
// anything is a check nobody runs on a pull request.
//
// WHY THIS ONE AND NOT THE OTHER TWENTY. Every candidate detector was measured against nine real
// repositories on one machine — 658 shipped files across Go, TypeScript, Python and Swift — before
// any of them was written. The result decided the shape of this file:
//
//   a test marked .only                 0 hits
//   TLS verification switched off       0 hits
//   a payment test key in shipped code  0 hits    (15 raw, every one a test fixture)
//   a sandbox flag hardcoded on         0 hits
//   debug forced on                     0 hits
//   console.log in shipped code       339 hits across 49 files — the noise machine
//   AN ENV VAR READ THAT NOTHING DECLARES        14, 3 and 45 in three separate repos
//
// The first five are real defects and worth catching one day, but they describe a repository's
// STATE, and state is almost always fine. The last one describes a CONTRADICTION — the code needs
// something the configuration never promises — and contradictions accumulate on every pull request,
// which is why it is the one with a base rate worth putting in front of somebody every time.
//
// MEASURED on this project's own control plane, which is the least flattering possible corpus:
// .env.local declares 58 variables, .env.example declares 27, and the code reads 78. GITHUB_APP_SLUG
// and INDEXNOW_KEY are set on the author's machine and named in no example file at all.
//
// THE ONE DISTINCTION THAT MAKES IT TRUSTWORTHY: a read with a fallback is not a requirement.
// `process.env.PORT || 3000` cannot break a deploy and must never be reported as if it could —
// flagging optional configuration is how a check becomes noise and then becomes deleted. Only a
// read with no fallback, declared nowhere, is a deploy that boots and then fails.

import fs from "node:fs";
import path from "node:path";

const SKIP_DIRS = new Set([
  "node_modules", ".git", ".next", "dist", "build", "out", "coverage", "vendor",
  ".turbo", ".vercel", ".cache", "__pycache__", ".venv", "target", "Pods", ".expo",
  "DerivedData", "Carthage", ".gradle", ".dart_tool", "bin", "obj",
]);

const EXTS = new Set([
  ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".rb", ".go", ".php",
  ".swift", ".kt", ".java", ".vue", ".svelte", ".rs", ".dart", ".ex", ".exs",
]);

/** Files that declare configuration rather than consume it. */
const DECLARING_FILES = [
  ".env.example", ".env.sample", ".env.template", ".env.defaults", ".env.dist",
  ".env.local.example", "env.example", ".env.schema",
  "fly.toml", "vercel.json", "render.yaml", "railway.json", "app.yaml", "netlify.toml",
  "docker-compose.yml", "docker-compose.yaml", "Dockerfile", "Procfile",
  "README.md", "DEPLOY.md", "DEPLOYMENT.md", "CONTRIBUTING.md", ".env.README",
];

/**
 * Names the platform supplies, or that mean nothing without one. Reporting these would be reporting
 * the runtime itself: nobody writes PATH into a .env.example, and a check that asks them to is a
 * check that gets muted on its first run.
 */
const AMBIENT = new Set([
  "NODE_ENV", "PATH", "HOME", "PWD", "USER", "SHELL", "LANG", "LC_ALL", "TZ", "TERM", "TMPDIR",
  "PORT", "HOSTNAME", "HOST", "CI", "DEBUG", "NO_COLOR", "FORCE_COLOR", "npm_lifecycle_event",
  // Provided by the CI or hosting platform itself.
  "GITHUB_ACTIONS", "GITHUB_TOKEN", "GITHUB_REPOSITORY", "GITHUB_REF", "GITHUB_SHA",
  "GITHUB_EVENT_NAME", "GITHUB_EVENT_PATH", "GITHUB_RUN_ID", "GITHUB_SERVER_URL",
  "GITHUB_BASE_REF", "GITHUB_HEAD_REF", "GITHUB_STEP_SUMMARY", "GITHUB_OUTPUT", "GITHUB_WORKSPACE",
  "VERCEL", "VERCEL_ENV", "VERCEL_URL", "VERCEL_GIT_COMMIT_SHA", "RAILWAY_ENVIRONMENT",
  "FLY_APP_NAME", "FLY_REGION", "FLY_ALLOC_ID", "AWS_REGION", "AWS_EXECUTION_ENV",
  "RENDER", "NETLIFY", "HEROKU_APP_NAME", "DYNO", "K_SERVICE",
]);

/** A test, a fixture, or a script — none of them is the deploy. */
const TEST_PATH = /(^|\/)(tests?|__tests__|__mocks__|testdata|fixtures?|mocks?|e2e|cypress|playwright|spec)(\/|$)/i;
const TEST_FILE = /(\.|_|-)(test|spec)\.[a-z]+$|^test_.*\.py$|_test\.go$|Tests?\.(swift|kt|java)$/i;
const TOOLING_PATH = /(^|\/)(scripts?|tools?|examples?|samples?|migrations?|seeds?|benchmarks?)(\/|$)/i;

export const isTestPath = (rel) => TEST_PATH.test("/" + rel) || TEST_FILE.test(path.basename(rel));
export const isToolingPath = (rel) => TOOLING_PATH.test("/" + rel);

/**
 * Every way the languages we can read ask for an environment variable.
 *
 * Each pattern captures the NAME in group 1 and is anchored on the accessor, never on a bare
 * identifier: matching `API_KEY` anywhere would find it in a comment, a string of prose and a
 * variable declaration, which is precisely the mistake lib/audit.mjs's header records.
 */
const READS = [
  /process\.env\.([A-Z][A-Z0-9_]{2,})/g,                          // node
  /process\.env\[\s*["'`]([A-Z][A-Z0-9_]{2,})["'`]\s*\]/g,        // node, bracketed
  /import\.meta\.env\.([A-Z][A-Z0-9_]{2,})/g,                     // vite
  /os\.environ(?:\.get)?\(?\[?\s*["']([A-Z][A-Z0-9_]{2,})["']/g,  // python
  /os\.getenv\(\s*["']([A-Z][A-Z0-9_]{2,})["']/g,                 // python
  /os\.Getenv\(\s*["`]([A-Z][A-Z0-9_]{2,})["`]\s*\)/g,            // go
  /ENV\[\s*["']([A-Z][A-Z0-9_]{2,})["']\s*\]/g,                   // ruby
  /ENV\.fetch\(\s*["']([A-Z][A-Z0-9_]{2,})["']/g,                 // ruby
  /getenv\(\s*["']([A-Z][A-Z0-9_]{2,})["']\s*\)/g,                // php, c
  /System\.getenv\(\s*["']([A-Z][A-Z0-9_]{2,})["']/g,             // java, kotlin
  /ProcessInfo\.processInfo\.environment\[\s*["']([A-Z][A-Z0-9_]{2,})["']/g, // swift
  /std::env::var\(\s*["']([A-Z][A-Z0-9_]{2,})["']/g,              // rust
];

/**
 * Does this line give the read a way out?
 *
 * `process.env.PORT || 3000`, `os.getenv("X", "default")`, `?? ""`, a ternary, or anything wrapped
 * in a null-check has a defined behaviour when the variable is absent, so its absence is a choice
 * and not a break. THIS IS THE WHOLE PRECISION OF THE CHECK. Without it the report fills with
 * optional configuration and reads as forty problems where there are three.
 */
export function hasFallback(line, name) {
  const at = line.indexOf(name);
  if (at < 0) return false;
  const after = line.slice(at + name.length);
  // `|| x`, `?? x`, `, "default"` inside the accessor call, or a ternary on the same expression.
  if (/^\s*["'`\]\)]*\s*(\|\||\?\?)/.test(after)) return true;
  if (/^\s*["'`]\s*,\s*[^)]+\)/.test(after)) return true;
  if (/^\s*["'`\]\)]*\s*\?[^?]/.test(after)) return true;
  // A guard on the same line: if (!process.env.X) …, X ? a : b, Boolean(process.env.X)
  const before = line.slice(0, at);
  if (/(!|\?\.|Boolean\(|if\s*\(\s*!?)\s*[A-Za-z_.\[\]"'`]*$/.test(before)) return true;
  return false;
}

function walk(dir, out = [], depth = 0) {
  if (depth > 12) return out;
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const e of entries) {
    if (SKIP_DIRS.has(e.name)) continue;
    if (e.name.startsWith(".") && !e.name.startsWith(".env") && e.name !== ".github") continue;
    const full = path.join(dir, e.name);
    if (e.isDirectory()) walk(full, out, depth + 1);
    else if (EXTS.has(path.extname(e.name))) {
      try {
        if (fs.statSync(full).size < 500_000) out.push(full);
      } catch {
        /* unreadable is not a finding */
      }
    }
  }
  return out;
}

/**
 * Every name any configuration or documentation in this repo mentions.
 *
 * Deliberately generous. A variable named in a README, a Dockerfile, a compose file or a workflow
 * IS documented — somebody deploying has been told about it, and which file told them is not our
 * business. Being strict here would manufacture findings, and a manufactured finding costs more
 * than a missed one.
 */
export function declaredNames(root, read = (p) => fs.readFileSync(p, "utf8")) {
  const names = new Set();
  const add = (text) => {
    for (const m of String(text).matchAll(/\b([A-Z][A-Z0-9_]{2,})\b/g)) names.add(m[1]);
  };
  for (const f of DECLARING_FILES) {
    try {
      add(read(path.join(root, f)));
    } catch {
      /* absent is the common case */
    }
  }
  // Every CI workflow, which is where a deployed app's variables usually really live.
  const wf = path.join(root, ".github", "workflows");
  try {
    for (const f of fs.readdirSync(wf)) {
      if (/\.ya?ml$/i.test(f)) {
        try {
          add(read(path.join(wf, f)));
        } catch {
          /* skip */
        }
      }
    }
  } catch {
    /* no workflows */
  }
  return names;
}

/**
 * What the code requires, and where it first asks for it.
 *
 * Test and tooling files are read but their reads are marked, because a script that wants
 * DEPLOY_TOKEN is not the running application and reporting it alongside a real one blurs both.
 */
export function requiredNames(root, { files = null, read = (p) => fs.readFileSync(p, "utf8") } = {}) {
  const found = new Map();
  for (const file of files || walk(root)) {
    const rel = path.relative(root, file) || path.basename(file);
    let text;
    try {
      text = read(file);
    } catch {
      continue;
    }
    if (text.includes("\u0000")) continue;
    const lines = text.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.length > 800) continue;
      // A commented-out read is not a read.
      if (/^\s*(\/\/|#|\*|--)/.test(line)) continue;
      for (const re of READS) {
        re.lastIndex = 0;
        for (const m of line.matchAll(re)) {
          const name = m[1];
          if (AMBIENT.has(name)) continue;
          const optional = hasFallback(line, name);
          const prev = found.get(name);
          const here = { name, file: rel, line: i + 1, optional, test: isTestPath(rel), tooling: isToolingPath(rel) };
          // Keep the strongest reading: a name read WITHOUT a fallback anywhere in shipped code is
          // required, whatever the other twelve call sites do with it.
          if (!prev || (prev.optional && !optional) || (prev.test && !here.test)) found.set(name, here);
        }
      }
    }
  }
  return found;
}

/**
 * The finding: names the running application needs, with no fallback, that nothing declares.
 *
 * Sorted so the report is stable between runs on an unchanged repo — a check whose output reorders
 * itself makes a reviewer re-read a list they already read.
 */
export function missingConfig(root, opts = {}) {
  const declared = opts.declared || declaredNames(root);
  const required = opts.required || requiredNames(root, opts);
  const out = [];
  for (const v of required.values()) {
    if (v.test) continue;          // a test's own environment is the test's business
    if (v.optional) continue;      // it has a fallback, so its absence is a choice
    if (declared.has(v.name)) continue;
    out.push(v);
  }
  return out.sort((a, b) => (a.tooling === b.tooling ? a.name.localeCompare(b.name) : a.tooling ? 1 : -1));
}

/**
 * What the reader sees, or nothing at all.
 *
 * Silent on a clean repo, for the reason every other surface in this product is silent on green: a
 * check that speaks when it has nothing to say teaches people to skip it, and then it is not there
 * on the day it matters.
 */
export function guardLines(missing, { root = "." } = {}) {
  if (!missing.length) return [];
  const app = missing.filter((m) => !m.tooling);
  const tools = missing.filter((m) => m.tooling);
  const lines = [];
  const n = app.length;
  if (n) {
    lines.push(
      `${n} environment variable${n === 1 ? "" : "s"} ${n === 1 ? "is" : "are"} read with no fallback and declared nowhere in this repo.`,
      `A deploy without ${n === 1 ? "it" : "them"} starts and then fails on the line that reads ${n === 1 ? "it" : "them"}.`,
      "",
    );
    for (const m of app) lines.push(`  ${m.name}`, `      read in ${m.file}:${m.line}`);
  }
  if (tools.length) {
    // "N more" only reads correctly when there was a first group. With no application findings this
    // section IS the report, and it opens the output — measured on a real repo, where the first two
    // lines were a blank one and the word "more" referring to nothing.
    lines.push(
      ...(n ? [""] : []),
      n
        ? `${tools.length} more ${tools.length === 1 ? "is" : "are"} read only by scripts or tooling, which may be deliberate:`
        : `${tools.length} environment variable${tools.length === 1 ? " is" : "s are"} read by scripts or tooling here and declared nowhere. Nothing the deployed application needs is missing.`,
    );
    for (const m of tools) lines.push(`  ${m.name}  ${m.file}:${m.line}`);
  }
  lines.push(
    "",
    n
      ? "Add them to .env.example (or your deploy config) so the next person — or the next machine — knows."
      : "Worth a line in .env.example if anybody else runs these.",
  );
  return lines;
}

/** The command. Never a non-zero exit: this describes the repo, it does not judge a run. */
export function guardCmd({ dir = ".", log = console.log, json = false } = {}) {
  const root = path.resolve(dir);
  let missing;
  try {
    missing = missingConfig(root);
  } catch (e) {
    // Reading a repository must never be the thing that fails a build.
    log(`guard could not read ${root}: ${e && e.message}`);
    return 0;
  }
  if (json) {
    log(JSON.stringify({ root, missing }, null, 2));
    return 0;
  }
  if (!missing.length) {
    log("Every environment variable this code requires is declared somewhere. Nothing to report.");
    return 0;
  }
  for (const line of guardLines(missing, { root })) log(line);
  return 0;
}
