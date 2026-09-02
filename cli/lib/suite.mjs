// `npx smolanalytics test --suite tests/ --comment` — the whole folder, on the pull request that
// changed the app.
//
// WHY THIS EXISTS SEPARATELY FROM test.mjs.
//
// `test --url --test "…"` proves the idea on a laptop in under a minute. That is a demo. What makes
// it a product is the same verdict arriving on the pull request that broke the thing, without
// anyone remembering to run anything.
//
// WHAT IT REFUSES TO DO TO GET THERE. No GitHub App across every repository. No agent pushing a
// Dockerfile into the code. No environment built from scratch before you are allowed to see
// anything. This runs on the customer's own Actions runner, against the preview URL their host
// already built, and comments with the GITHUB_TOKEN that Actions hands every job for free. The
// customer grants nothing and we write nothing to their repository.
//
// THE STATUSES ARE THE PRODUCT, AND THIS FILE NEVER BLURS THEM:
//   passed / failed  the app did, or did not, do what the sentence describes. Failed is a bug report.
//   stale            a RECORDING stopped fitting. A replay cannot tell "renamed" from "removed", so
//                    it is never red and never worded as a failure; the agent re-checks it.
//   errored          this runner could not run — no browser, no key, no network. Never the app.
//   flaky            the test failed and then passed when retried from a clean page. Not a pass —
//                    silently swallowing a retry is how an intermittent bug hides for months — and
//                    not a bug report either: nothing about the app was pinned down. "This test is
//                    unreliable" is the whole claim, and it warns without failing the build.
// Blur them and a copy change pages somebody at 2am, or an outage on our side reads to a customer
// as their checkout being broken.

import { readdirSync, readFileSync, statSync, mkdirSync, existsSync } from "node:fs";
import path from "node:path";
import { testCmd, loadPlaywright, keyFix } from "./test.mjs";
import { keyProblem } from "./safety.mjs";
import { openPool } from "./pool.mjs";
import { suspectsForFailure, gitDiff } from "./suspect.mjs";
import { declaredNames, introducedConfig, introducedCommentLines } from "./guard.mjs";
import { layoutCommentLines } from "./layout.mjs";
import { DEFAULT_AUTH_DIR } from "./auth.mjs";
import { DEFAULT_ENGINE } from "./engines.mjs";
import { prNumber, publishShare, ciContext } from "./share.mjs";
// DIFF-AWARE TEST SELECTION (lib/select.mjs). Everything about --since lives there: this file only
// asks it which tests to run and prints what it says. Without --since not one line of it is on the
// path and the whole folder runs exactly as it always has.
import { shipText } from "./ship.mjs";
import { aloneNote, cluster, clusterHead, clusterNote, grouped } from "./cluster.mjs";
import { notify, notifyLine, parseWhen } from "./notify.mjs";
import { fileIssues, issueLine } from "./issue.mjs";
import { selectSuite, selectionHeadline, selectionCommentLines, selectionCommentDetail, selectionTerminalLines, selectionTailLines, selectionShareLines } from "./select.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
  r: (s) => `\x1b[31m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

export const DEFAULT_PLANS_DIR = ".smolanalytics/recordings";

// ---- reading the suite ------------------------------------------------------------------------

const HEADING = /^(#{1,6})\s+(.+?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)/;
const LIST = /^\s*(?:[-*+]\s+|\d+[.)]\s+)/;
const QUOTE = /^\s*>\s?/;
// A `---` between two tests is the most ordinary thing in a markdown file. Folded into the body it
// is handed to the agent as part of what to look for on the page: "Click Buy. ---".
const RULE = /^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$/;
// The frontmatter grammar, reused to recover from a block whose closing `---` was forgotten.
const KV = /^([A-Za-z][\w-]*)\s*:\s*(.*)$/;

/**
 * Which heading depth is a test, given every depth the file uses.
 *
 * A file that opens with `# Checkout` and then lists `## …` flows must not count the title as a
 * test: that test's body would be every flow in the file at once, it would run them as one
 * scenario, and it would record a single meaningless plan for all of them. So a top-level heading
 * that appears exactly once with deeper headings under it is a title, not a test. Headings deeper
 * than the test depth are notes inside a test, never tests of their own.
 */
export function testDepth(depths) {
  const uniq = [...new Set(depths)].sort((a, b) => a - b);
  if (uniq.length === 0) return 0;
  if (uniq.length === 1) return uniq[0];
  const shallowest = uniq[0];
  const timesUsed = depths.filter((d) => d === shallowest).length;
  return timesUsed === 1 ? uniq[1] : shallowest;
}

/** A stable, filesystem-safe id. Recordings are looked up by it, so it must not drift. */
export function slug(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "test";
}

/** Turn one markdown file into tests. A heading is a test; the prose under it is the sentence. */
/**
 * YAML-ish frontmatter at the very top of a file.
 *
 * Not parsed as YAML — that would be a dependency, and this CLI has none. Only `key: value` on its
 * own line is read; anything else in the block is skipped rather than erroring, so a key we invent
 * next year does not break a CLI shipped today.
 *
 * It MUST be stripped whether or not we understand it. Left in, the delimiters and keys are folded
 * into the sentence and the agent is told to go and find `--- title: "Checkout" criticality:
 * critical ---` on the page. Caught by running a real two-file suite and reading what the agent
 * would have been handed.
 */
export function frontmatter(text) {
  // THE BOM COMES OFF HERE, ONCE, FOR EVERYTHING DOWNSTREAM. VS Code on Windows and Visual Studio
  // both write UTF-8 with one by default. Left on, it sits in front of the first `#`, that heading
  // stops matching HEADING, and the test under it is dropped with no message at all — a green suite
  // one test lighter than the folder.
  const src = String(text).replace(/^\uFEFF/, "");
  if (!/^---[ \t]*(?:\r?\n|$)/.test(src)) return { meta: {}, body: src, unterminated: false, dropped: "" };
  const lines = src.split(/\r?\n/);

  // Scanned by line rather than matched as one block, so the closing delimiter is exactly `---` and
  // an empty `---\n---` pair is recognised instead of leaving its two delimiters in the sentence.
  let close = -1;
  for (let i = 1; i < lines.length; i++) {
    if (/^---[ \t]*$/.test(lines[i])) {
      close = i;
      break;
    }
  }
  if (close !== -1) {
    // A closed block is frontmatter, and it is trusted as such without inspecting what is in it:
    // real frontmatter holds list values and `#` comments, and refusing those would fold genuine
    // metadata back into the sentence, which is the failure this whole function exists to prevent.
    const block = lines.slice(1, close);
    return { meta: read(block.join("\n")), body: lines.slice(close + 1).join("\n"), unterminated: false, dropped: block.join("\n") };
  }

  // THE CLOSING `---` IS THE EASIEST LINE IN THE FILE TO FORGET, and forgetting it used to hand the
  // agent "--- title: Checkout criticality: critical Click Buy." while throwing the written name
  // away for the filename. Recover the way the block was obviously meant to read: the opening
  // delimiter plus the run of `key: value` lines under it, stopping at the first line that is not
  // one. That stop keeps prose after a blank line out of the metadata.
  let end = 1;
  while (end < lines.length && lines[end].trim() && KV.test(lines[end].trim())) end++;
  // A bare `---` with no keys under it is a horizontal rule someone put at the top of the file, not
  // a forgotten fence. Drop the rule and say nothing: there is no missing delimiter to go and fix.
  if (end === 1) return { meta: {}, body: lines.slice(1).join("\n"), unterminated: false, dropped: "" };
  return { meta: read(lines.slice(1, end).join("\n")), body: lines.slice(end).join("\n"), unterminated: true, dropped: "" };
}

function read(block) {
  const meta = {};
  for (const line of block.split(/\r?\n/)) {
    const kv = KV.exec(line.trim());
    if (!kv) continue;
    meta[kv[1].toLowerCase()] = kv[2].trim().replace(/^["']|["']$/g, "");
  }
  return meta;
}

/**
 * Did a removed block hold a document rather than metadata?
 *
 * `# text` is both a markdown heading and a YAML comment, so a heading alone proves nothing — a
 * frontmatter comment would raise a false alarm on a file where nothing was lost, and a warning
 * that cries wolf gets skipped on the day it is right. What no valid YAML block contains is a bare
 * unindented line with no colon, no `-` and no `#`: that is prose. Prose plus a heading is a
 * document, and a document between two `---` lines was somebody's tests being deleted.
 */
function wasDocument(block) {
  if (!block) return false;
  const lines = block.split(/\r?\n/);
  const prose = (l) => l.trim() && !/^\s/.test(l) && !KV.test(l) && !/^#/.test(l) && !/^[-*+]/.test(l);
  return lines.some((l) => HEADING.test(l)) && lines.some(prose);
}

export function parseSuite(file, text, onNote = () => {}) {
  const { meta, body: afterMeta, unterminated, dropped } = frontmatter(text);
  if (unterminated) {
    onNote(`${file}: the frontmatter block at the top is never closed. Add a line containing only --- after the last key. The keys were read anyway and kept out of the test.`);
  }
  // A file that opens with a horizontal rule and has another one later looks exactly like a closed
  // frontmatter block, and everything between the two rules is removed as metadata — which can be
  // whole tests. A test you believe is running and is not is worse than one you know is missing.
  if (wasDocument(dropped)) {
    onNote(`${file}: everything between the first two --- lines was removed as frontmatter, and it contained a heading. If that --- was meant as a horizontal rule, delete it: the tests under it did not run.`);
  }
  const stripped = String(afterMeta).replace(/<!--[\s\S]*?-->/g, "");
  const lines = [];
  let fence = null;
  for (const raw of stripped.split(/\r?\n/)) {
    const f = FENCE.exec(raw);
    if (fence) {
      if (f && raw.trim().startsWith(fence)) fence = null;
      continue;
    }
    // Code samples are context for the reader, not instructions to the browser. A fenced block
    // folded into the sentence would send the agent chasing a snippet of someone's JSX.
    if (f) {
      fence = f[1];
      continue;
    }
    lines.push(raw);
  }

  const depths = lines.map((l) => HEADING.exec(l)).filter(Boolean).map((m) => m[1].length);
  const want = testDepth(depths);
  const tests = [];
  let cur = null;
  const flush = () => {
    if (!cur) return;
    const body = cur.body.join(" ").replace(/\s+/g, " ").trim();
    // A heading with nothing under it IS the sentence. `## the pricing page shows a monthly price`
    // is a complete test and should not be dropped for having no paragraph.
    tests.push({ file, name: cur.name, test: body || cur.name });
    cur = null;
  };

  for (const line of lines) {
    const m = HEADING.exec(line);
    if (m) {
      if (m[1].length <= want) {
        flush();
        if (m[1].length === want) cur = { name: m[2].trim(), body: [] };
      }
      continue;
    }
    if (!cur) continue;
    // Checked before the list marker is stripped, so `- - -` is read as the rule it is.
    if (RULE.test(line)) continue;
    const t = line.replace(QUOTE, "").replace(LIST, "").trim();
    if (t) cur.body.push(t);
  }
  flush();

  if (tests.length) return tests;

  // No headings at all: the file is one test. Someone whose whole test is a single sentence should
  // not have to learn a document structure to run it.
  const body = lines.filter((l) => !RULE.test(l)).map((l) => l.replace(QUOTE, "").replace(LIST, "").trim()).filter(Boolean).join(" ");
  if (!body) return [];
  // A frontmatter title names a single-test file. It is what the person wrote down as the point of
  // the test, and it is what appears on the pull request — the filename is a fallback, not a name.
  const named = (meta.title || "").trim();
  return [{
    file,
    name: named || path.basename(file).replace(/\.md$/i, "").replace(/[-_]+/g, " "),
    test: body.replace(/\s+/g, " ").trim(),
  }];
}

const nodeIo = {
  isDir: (p) => {
    try {
      return statSync(p).isDirectory();
    } catch {
      return false;
    }
  },
  list: (p) => readdirSync(p, { withFileTypes: true }).map((d) => ({ name: d.name, dir: d.isDirectory() })),
  read: (p) => readFileSync(p, "utf8"),
  exists: (p) => {
    try {
      statSync(p);
      return true;
    } catch {
      return false;
    }
  },
};

const why = (e) => (e && e.message ? e.message : String(e));

function walk(dir, io, out = [], errors = [], skipped = []) {
  let entries;
  try {
    entries = io.list(dir);
  } catch (e) {
    // COLLECTED, NEVER THROWN. Thrown, this reaches the CLI's last-resort catch, which prints a red
    // `failed` and exits 1 — the code reserved for "the application is broken". A locked folder on
    // the runner would redden the build with a bug report about the customer's app.
    errors.push(`${dir} could not be read: ${why(e)}. This is the test runner reading your folder, not your application.`);
    return out;
  }
  // Sorted, always. Recording filenames and the pull request table are both derived from this
  // order, and a table that reshuffles between runs makes a reviewer re-read the whole thing.
  for (const e of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    if (e.name.startsWith(".") || e.name === "node_modules") continue;
    const p = path.join(dir, e.name);
    if (e.dir) walk(p, io, out, errors, skipped);
    else if (DOCS_NOT_TESTS.test(e.name)) skipped.push(p);
    else if (/\.md$/i.test(e.name)) out.push(p);
  }
  return out;
}

// DOCUMENTATION IN A TESTS FOLDER IS NOT A TEST, AND WAS RUN AS ONE.
//
// A file with no heading is deliberately one test — "someone whose whole test is a single sentence
// should not have to learn a document structure" (parseSuite, above). A tests/README.md has no
// heading either, so MEASURED with one in place the run listed them as peers:
//
//   A shopper can add an item to the cart    tests/checkout.md
//   How this suite works                     tests/README.md
//   2 tests · 0 passed · 2 could not run
//
// The prose of the README was handed to the agent and driven against the application. There is no
// ignore mechanism to work around it with, and the empty-folder message promises the opposite
// ("A test is a markdown heading and one sentence under it"). So these two names are skipped —
// and ANNOUNCED, never dropped in silence: a file the person can see, quietly not run, is the same
// class of bug in the other direction.
const DOCS_NOT_TESTS = /^(readme|contributing)\.md$/i;

/** Find every test under a directory (or in a single file), each with the recording it owns. */
export function discover(target, plansDir = DEFAULT_PLANS_DIR, io = nodeIo) {
  // `errors` is "we could not read this" — our problem, exit 2, never a verdict about the app.
  // `notes` is "we read it and had to interpret something" — printed, and it changes nothing.
  const errors = [];
  const notes = [];
  if (!io.exists(target)) return { tests: [], missing: target, errors, notes };
  const isDir = io.isDir(target);
  const skipped = [];
  const files = isDir ? walk(target, io, [], errors, skipped) : [target];
  for (const f of skipped) notes.push(`skipped ${f}: documentation, not a test. A test is a heading and one sentence under it.`);
  // A recording is named after the test's path WITHIN the suite, never the path as typed. Otherwise
  // `--suite tests/` and `--suite /home/me/app/tests` name the same test's recording differently,
  // a developer's local runs and CI never share a cache, and every CI run is a fresh agent run —
  // exactly the cost the cache exists to avoid.
  const base = isDir ? target : path.dirname(target);
  const tests = [];
  const taken = new Set();
  for (const f of files) {
    const within = path.relative(base, f).replace(/\.md$/i, "");
    let text;
    try {
      text = io.read(f);
    } catch (e) {
      // One file we cannot open must not lose the eight beside it that we can.
      errors.push(`${f} could not be read: ${why(e)}. Nothing in it ran. This is the test runner reading your file, not your application.`);
      continue;
    }
    for (const t of parseSuite(f, text, (n) => notes.push(n))) {
      // Two tests sharing one recording file would overwrite each other's plan on every run, and
      // each would then find the other's recording and report it stale forever.
      let id = `${slug(within)}--${slug(t.name)}`;
      if (taken.has(id)) {
        let n = 2;
        while (taken.has(`${id}-${n}`)) n++;
        id = `${id}-${n}`;
      }
      taken.add(id);
      tests.push({ ...t, id, planPath: path.join(plansDir, `${id}.json`) });
    }
  }
  return { tests, missing: "", errors, notes, files };
}

// ---- running them -----------------------------------------------------------------------------

/**
 * testCmd's exit code, for the paths that end before any verdict is produced.
 *
 * 1 does NOT become `failed` here. Exit 1 means "a test failed", but a run that reached this
 * function reported no verdict at all — and noVerdictReason, printed in the same row, says so in
 * as many words. A row reading **fail** beside "the run ended without a verdict, so nothing was
 * observed" is a bug report about a bug nobody saw, on somebody's pull request. `errored` is what
 * we actually know, and it still exits 2, so no gate reads it as green.
 */
function fromExit(code) {
  return code === 0 ? "passed" : "errored";
}

/**
 * Say why a test produced no verdict at all.
 *
 * The pull request comment prints this sentence. "No detail was recorded" is the least useful thing
 * it could say, and the case it says it in — a first run with no recordings and no key — is the one
 * a new user hits first. Name the actual missing thing.
 */
export function noVerdictReason(code, { hasKey, hasPlan }) {
  if (code === 1) return "The run ended without a verdict, so nothing was observed. This is the test runner, not your application.";
  if (!hasKey && !hasPlan) return "There is no recording for this test yet and ANTHROPIC_API_KEY is not set, so nothing ran.";
  if (!hasKey) return "ANTHROPIC_API_KEY is not set, so the agent could not run.";
  return "This runner could not run the test; the log names the reason. This is the test runner, not your application.";
}

export async function runSuite({
  tests,
  url,
  plansDir = DEFAULT_PLANS_DIR,
  headed = false,
  yes = false,
  maxSteps = 40,
  retries = 1,
  evidenceDir = "",
  layout = "report",
  // The false-green guard (lib/render.mjs), on by default and passed straight through: --no-render-check
  // is a suite-wide switch or it is useless, since a suite is where nobody reads the terminal.
  renderCheck = true,
  // CROSS-BROWSER (lib/engines.mjs). One engine for the whole suite: --browser webkit means every
  // test runs on WebKit, including the recordings made on Chromium, each of which says so.
  engine = DEFAULT_ENGINE,
  // AUTHENTICATED FLOWS (lib/auth.mjs). Passed through untouched, and that is the whole point:
  // every test in the suite is handed the same login sentence and the same auth directory, so the
  // FIRST test signs in and writes the saved session and the other forty-nine reuse the file.
  login = "",
  authFile = "",
  authDir = DEFAULT_AUTH_DIR,
  teardown = "",
  // --seed (lib/seed.mjs), passed through untouched. Per TEST, not per suite: every test already
  // gets its own identity, so every test gets its own fixture, and one test cannot leave state
  // behind that quietly makes the next one pass.
  seed = "",
  emailDomain = "",
  log = console.log,
  env = process.env,
  runTest = testCmd,
  mkdir = (d) => mkdirSync(d, { recursive: true }),
  hasPlan = (p) => existsSync(p),
  hasKey = Boolean(process.env.ANTHROPIC_API_KEY),
  findSuspects = suspectsForFailure,
  // PARALLEL SUITE EXECUTION (lib/pool.mjs). 1 is the serial loop this function has always been —
  // same order, same log, a Chromium per test, not one line of pool.mjs on the path.
  workers = 1,
  loadBrowser = loadPlaywright,
  // --share (lib/share.mjs). Collection only: every test's run objects gain the extra facts a
  // share page renders, and suiteCmd publishes ONE bundle for the whole suite afterwards. `publish:
  // false` below is what stops fifty tests printing fifty links.
  share = false,
}) {
  // Create the directory BEFORE the first test. testCmd writes the recording only after a test
  // passes, so a missing directory turns a green run into "errored" at the last instruction — the
  // most confusing possible outcome, and the one a first-ever CI run hits every time.
  try {
    mkdir(plansDir);
  } catch (e) {
    log(C.r(`could not create ${plansDir}: ${e && e.message}`));
  }

  // The concurrency, the buffered per-test transcript, the one shared browser and the login gate
  // all live in lib/pool.mjs. What is below is the same body it always was, run N at a time: `tlog`
  // holds this test's lines until it finishes so eight workers cannot braid one transcript, and the
  // results come back in the SUITE's order, never in the order they finished.
  const pool = openPool({ workers, login, authDir, env, log, loadPlaywright: loadBrowser });
  const results = await pool.map(tests, async (t, i, tlog) => {
    tlog(`\n${C.b(t.name)}  ${C.dim(t.file)}`);
    const started = Date.now();
    // Checked BEFORE the run, because a passing agent run writes one and we would then report that
    // a recording existed all along.
    const hadPlan = hasPlan(t.planPath);
    const runs = [];
    let code = 2;
    try {
      code = await runTest({
        url,
        test: t.test,
        plan: t.planPath,
        headed,
        yes,
        maxSteps,
        retries,
        evidenceDir,
        layout,
        renderCheck,
        engine,
        login,
        authFile,
        authDir,
        // Every test gets its OWN identity, so nine signups are nine findable rows rather than
        // one that collides with itself on test two. That has to survive SMOLANALYTICS_RUN_ID,
        // which pins one id for the whole CI run: the index suffix keeps the rows grouped under
        // the CI id while keeping the identities apart. The index, not the test's name — two long
        // names could collide again inside the 40 characters an email local part gets.
        runId: env.SMOLANALYTICS_RUN_ID ? `${env.SMOLANALYTICS_RUN_ID}-${i + 1}` : "",
        teardown,
        seed,
        emailDomain,
        log: tlog,
        // The header above already printed the export line once. See runOnce.
        inSuite: true,
        // One Chromium for the whole suite, a context per worker. undefined when --workers 1, and
        // lib/test.mjs's own default then applies.
        loadBrowser: pool.loadBrowser,
        share,
        publish: false,
        onRun: (r) => runs.push(r),
      });
    } catch (e) {
      // One test throwing must not abandon the rest of the suite: the reviewer needs the whole
      // picture, and a crash in test 2 of 9 hiding seven verdicts is worse than the crash.
      tlog(C.r(`  the runner threw: ${e && e.message ? e.message : e}`));
      runs.push({ status: "errored", mode: "agent", reason: `The runner threw: ${e && e.message ? e.message : e}. This is the test runner, not your application.` });
    }

    const last = runs[runs.length - 1];
    const status = last ? last.status : fromExit(code);
    const wentStale = runs.some((r) => r.status === "stale");
    let reason = last?.reason || noVerdictReason(code, { hasKey, hasPlan: hadPlan });
    if (status === "stale" && !hasKey) {
      reason += " ANTHROPIC_API_KEY is not set, so the agent could not re-check it.";
    }
    return {
      ...t,
      status,
      mode: last?.mode || "",
      reason: reason.trim(),
      ms: Date.now() - started,
      // A recording that went stale and was then re-verified is worth saying out loud: it is the
      // moment the tool did the thing it promises, and it explains why that test was slow today.
      refreshed: wentStale && status === "passed",
      // Layout findings ride on the verdict-carrying run (lib/layout.mjs). Advisory: nothing about
      // this field may touch status or exit code — the PR comment renders it as small type.
      layout: Array.isArray(last?.layout) ? last.layout : [],
      // Only on failed, and only ever a decoration: which changed files this PR's diff connects to
      // what this run observed, each with its named evidence. [] is the common and correct answer —
      // suspect.mjs refuses to rank a diff it cannot connect — and nothing about it may touch the
      // status or the exit code.
      suspects: status === "failed" ? findSuspects({ runs, reason, planPath: t.planPath, url, env }) : [],
      // The share record from the run that CARRIES the verdict, which is the last one: with
      // --retries the failing first attempt is its own row and the settled row is the verdict.
      // null without --share, and nothing downstream reads it then.
      share: last?.share || null,
    };
  });
  await pool.close();
  return results;
}

export function summarize(results) {
  const count = (s) => results.filter((r) => r.status === s).length;
  return {
    total: results.length,
    passed: count("passed"),
    failed: count("failed"),
    stale: count("stale"),
    errored: count("errored"),
    flaky: count("flaky"),
    replayed: results.filter((r) => r.mode === "replay" && r.status === "passed").length,
    ms: results.reduce((a, r) => a + r.ms, 0),
  };
}

/**
 * The exit code contract, and why stale is not zero but flaky is.
 *
 * 1 means the application is broken — the only status that should ever redden a build.
 * 2 means this runner could not finish, which includes a recording that stopped fitting when there
 *   was no key to re-check it with. Exiting 0 there would report "all good" about a test nobody
 *   verified, which is the one lie a test tool cannot tell.
 * 0 includes flaky, deliberately: the retry DID verify the behaviour the sentence describes, so
 *   nothing known-broken ships — and a gate here trains people to re-run the build until it goes
 *   green, which is the exact swallowing the flaky status exists to prevent. It is a warning, and
 *   the comment and the terminal both say it out loud.
 */
export function exitCode(results) {
  if (results.some((r) => r.status === "failed")) return 1;
  if (results.some((r) => r.status === "errored" || r.status === "stale")) return 2;
  return 0;
}

/**
 * How many tests woke the agent — the paid half of the run.
 *
 * TWO EXCLUSIONS, AND BOTH ARE THE SAME RULE: only count what actually happened.
 *
 * Not "everything that did not replay", because a stale row replayed and an errored row may never
 * have opened a browser. And not every row whose mode says "agent" either: a test that errored
 * carries mode "agent" when the browser refused to launch or the very first model call was
 * rejected, and neither of those is a slow, paid run — the sentence this feeds says a run was slow
 * because N tests woke the agent, and naming a test that cost nothing overstates it.
 */
export const agentRuns = (results) => results.filter((r) => r.mode === "agent" && r.status !== "errored").length;

/**
 * WHY THIS RUN WAS SLOW, SAID ON THE RUN THAT WAS SLOW.
 *
 * MEASURED by walking a first CI run: three tests, no recordings restored, three rows reading
 * "agent" — and not one word anywhere about what that meant or what it bought. The only note that
 * existed was `N of M ran from a recording`, which by construction cannot appear until something
 * has ALREADY replayed. The run that most needs explaining was the only run guaranteed to get no
 * explanation, and on a forty-test suite it reads as alarmingly slow and expensive for no reason.
 *
 * WHAT IT REFUSES TO CLAIM. Not "the next run is free": a failing agent run records nothing, and
 * neither does a pass that needed no steps, so the tests that will replay tomorrow are not the set
 * this function can name. And when nothing replayed it does NOT assert the cache is broken — a
 * first-ever run looks identical from here — it names every possibility and lets the reader tell
 * them apart.
 *
 * THE THIRD CAUSE, WHICH USED TO BE MISSING FROM BOTH SENTENCES. A test that passes by only
 * reading a page performs no step, so compile() records nothing for it — the doc comment above has
 * always known this, and the sentence the reader actually gets did not say it. MEASURED, walking a
 * CI run: one such test among three, and every run afterwards said "1 of 3 woke the agent" beside
 * two causes that both imply it will settle. The template tells that reader their cache is the
 * suspect. It is not, and the number never moves. A cause that never settles has to be named, or
 * the reader is sent to debug something that is working.
 */
export function economicsNote(s, agent, tick = "") {
  if (!agent) return "";
  const what = `${agent} of ${s.total} woke the agent — the slow, paid half of a run. A recording it writes replays with no model calls at all.`;
  return s.replayed
    ? `${what} The agent wakes for a test with no recording yet, one whose recording stopped fitting the app, or one that passes by only reading a page — that last kind performs no step, so there is nothing to record and it never gets cheaper.`
    : `${what} Nothing replayed this time: this is the first run, or the recordings in ${tick}.smolanalytics/recordings${tick} are not being kept between runs (the workflow template caches them with actions/cache restore + save), or nothing here performs a step to record — a test that passes by only reading a page records nothing.`;
}

/** The Actions run this came from, or "" anywhere else. Shared so two surfaces cannot link differently. */
export function runUrlFor(env = process.env) {
  if (!env.GITHUB_RUN_ID || !env.GITHUB_REPOSITORY) return "";
  return `${(env.GITHUB_SERVER_URL || "https://github.com").replace(/\/+$/, "")}/${env.GITHUB_REPOSITORY}/actions/runs/${env.GITHUB_RUN_ID}`;
}

// One NaN in a report makes a reader distrust the verdict printed next to it, so a duration we do
// not have renders as nothing at all.
const secs = (ms) => {
  const n = Number(ms);
  return Number.isFinite(n) && n >= 0 ? `${(n / 1000).toFixed(1)}s` : "";
};

// ---- the pull request comment -------------------------------------------------------------------

// Flaky sits right under failed: after "what broke", the next most actionable line on a pull
// request is "which test cannot be trusted".
const RANK = { failed: 0, flaky: 1, errored: 2, stale: 3, passed: 4 };
const LABEL = { passed: "pass", failed: "**fail**", stale: "stale", errored: "error", flaky: "flaky" };

/**
 * The needle that makes the next push EDIT this comment instead of adding another.
 *
 * There is exactly ONE of these, and there was briefly two. A parallel build produced a second
 * commentBody/postComment pair in lib/pr-comment.mjs with a different marker; nothing imported it,
 * but had both ever run, every push would have left two verdicts on the pull request and neither
 * would have edited the other. That file is deleted. If a second marker shape ever appears, this
 * is the comment that should stop you.
 */
export const markerFor = (suite) => `<!-- smolanalytics-run:${slug(suite)} -->`;

/**
 * @param {object} opts
 * @param {boolean} [opts.hasKey] whether ANTHROPIC_API_KEY was set for this run. Passed as a fact
 *   rather than inferred by matching our own reason strings, because a reason is prose that gets
 *   reworded and a condition that reads prose stops firing silently when it does.
 */
export function commentBody(results, { url = "", suite = "tests", runUrl = "", problems = [], selection = null, hasKey = true, commit = "", evidenceDir = "", cwd = process.cwd(), env = process.env, getDiff = gitDiff, getDeclared = declaredNames } = {}) {
  const s = summarize(results);
  const head = [`${s.passed} passed`];
  // "flaky", never folded into passed: a headline that counts a retry as a pass is the lie the
  // status exists to prevent.
  if (s.flaky) head.push(`${s.flaky} flaky`);
  if (s.failed) head.unshift(`${s.failed} failed`);
  if (s.errored) head.push(`${s.errored} could not run`);
  if (s.stale) head.push(`${s.stale} stale`);
  // --since (lib/select.mjs). NOT a status — a skipped test reached no verdict and is in none of
  // the counts above — but it belongs in the headline for the same reason "not read" does below:
  // "12 passed" over a fifty-test folder is a suite lying about its own coverage.
  const skipHead = selectionHeadline(selection);
  if (skipHead) head.push(skipHead);
  // A folder we could not open produces no row, so without this the comment reads "3 passed" about
  // a suite that is four files long. On CI this comment IS the report — the terminal output nobody
  // opens is not a second chance to mention it.
  if (problems.length) head.push(`${problems.length} not read`);

  const rows = [...results].sort((a, b) => RANK[a.status] - RANK[b.status] || a.name.localeCompare(b.name));
  const how = (r) =>
    r.status === "errored" ? "did not run"
    // The reliability claim, not the mechanics: "agent, twice" would hide the one thing the
    // reader needs, which is that this test's first answer could not be trusted.
    : r.status === "flaky" ? "failed once, passed on retry"
    : r.refreshed ? "recording was stale, agent re-checked"
    // A stale row carries mode "replay" because a replay is what noticed. Saying "replayed, no
    // model calls" beside it reads as "ran fine, cost nothing", when in fact nothing was verified.
    : r.status === "stale" ? "recording stopped fitting"
    : r.mode === "replay" ? "replayed, no model calls"
    : r.mode === "agent" ? "agent"
    : "";

  const out = [
    markerFor(suite),
    secs(s.ms) ? `**${head.join(" · ")}** in ${secs(s.ms)}` : `**${head.join(" · ")}**`,
    "",
    // Directly under the headline, never at the bottom: what did NOT run is the first thing that
    // changes how the numbers above it should be read.
    ...selectionCommentLines(selection),
    // WHICH COMMIT THIS IS ABOUT. One comment is edited in place for the life of a pull request,
    // so on a healthy repository it always describes the newest push — and there is exactly one way
    // it does not: a run cancelled or timed out before it posted leaves the PREVIOUS commit's
    // verdicts sitting there, current-looking, with nothing on them to say otherwise. The template
    // ships `cancel-in-progress: true` and a 30-minute timeout, so both are ordinary. Seven
    // characters a reader can compare against their own branch is the whole fix.
    url ? `Against ${code(url)}${commit ? ` at ${code(commit)}` : ""}${runUrl ? ` · [run log](${runUrl})` : ""}` : "",
  ];

  // THE BUG REPORT COMES BEFORE THE ROSTER, AND THAT ORDER IS THE WHOLE POINT OF THE COMMENT.
  //
  // MEASURED, on a real 42-test run posted to a real comment endpoint: two failures, forty passes.
  // The table sorts failures to the top, so their NAMES were visible — but the reasons, which are
  // the only part a reviewer can act on, sat forty rows of "pass · replayed, no model calls" below
  // them. Forty rows carrying no information stood between a reviewer and the report they came for.
  //
  // It also decides which end survives GitHub's 65,536-character ceiling. The cut at the bottom of
  // this function falls on the tail; with the table last, a suite too big to fit loses roster rows,
  // and with the table first it lost the reasons — the deliverable — on exactly the run where the
  // most had gone wrong.
  //
  // Flaky is included: its reason names what failed and what then passed, which is the whole case
  // for distrusting it.
  //
  // THE CAUSE THAT EXPLAINS EVERY ROW GOES ABOVE THE ROWS. A missing key is not one test's problem,
  // it is the reason none of them ran, and the fix for it is the only line on the page anybody can
  // act on. MEASURED before this block existed: three identical "ANTHROPIC_API_KEY is not set"
  // reasons, and no mention anywhere — comment or log — of where a key goes on GitHub.
  if (!hasKey && s.errored) {
    out.push(
      "",
      "**No ANTHROPIC_API_KEY reached this job**, so the agent could not run.",
      "",
      `> ${keyFix({ GITHUB_ACTIONS: "true" })}`,
      ">",
      "> A test whose recording still fits the app replays with no key at all — these are the ones that had no recording, or whose recording stopped fitting.",
    );
  }

  // AND THE SAME RULE APPLIED TO THEIR BUGS, NOT JUST OUR OUTAGES (lib/cluster.mjs).
  //
  // Twelve failures on one pull request are very rarely twelve bugs. The reviewer's first job is to
  // notice that eleven of them blamed the same changed file and one did not — and that is arithmetic
  // over the suspects and recordings already on this page, so they should not have to do it by hand.
  // Silent unless the failures genuinely group; a header that only restates the list below it is a
  // header people learn to scroll past, and they take the one failure that mattered with them.
  const groups = cluster(rows);
  const causeHead = clusterHead(groups);
  if (causeHead) {
    out.push("", `**${causeHead}**`, "");
    for (const g of grouped(groups)) {
      // The same two rules the suspect lines below use, chosen by what the cause IS: a suspect
      // cause is a path out of their diff and goes through code(), which fences around a backtick
      // the path itself contains; a control label and a route are strings off their page.
      out.push(`> ${g.signal === "suspect" ? code(g.cause) : quote(g.cause)} — ${quote(g.why)}`);
    }
    // And which failures the cause above does NOT explain, by name. Without it a reviewer reads
    // "1 stands alone" and then scans every blockquote to find out which — the half of the job
    // this feature exists to remove. Their test names, so quote().
    const apart = aloneNote(groups);
    if (apart) out.push(">", `> ${quote(apart)}`);
  }

  // ONE OUTAGE IS REPORTED ONCE. Forty tests that could not run because the runner had no key, or
  // no browser, or no network, is ONE thing that went wrong on our side — and forty identical
  // blockquotes is a wall a reviewer scrolls past, taking the failures with it. Errored rows are
  // therefore grouped by their exact reason; the table below still names every one of them.
  //
  // Errored ONLY. A failure is a claim about one test's behaviour and keeps its own block even when
  // two tests broke the same way, because the suspects and the file under it are that test's.
  const outages = new Map();
  for (const r of rows) {
    if (r.status !== "errored") continue;
    const reason = r.reason || "no detail was recorded";
    if (!outages.has(reason)) outages.set(reason, []);
    outages.get(reason).push(r);
  }
  const reported = new Set();

  // EVERY BLOCK SAYS WHICH STATUS IT IS, IN THE SAME WORD THE TABLE USES.
  //
  // MEASURED, reading a rendered comment for 40 passes, 2 failures, 1 flaky, 1 errored and 1 stale:
  // five bold names over five blockquotes, rendered identically, and nothing above the table to
  // tell them apart. Two were bug reports. One was a warning, one was our own runner breaking, and
  // one — stale — is explicitly not a failure at all. A reviewer scanning that stack reads five
  // broken things. The prose does disambiguate, but only in the last clause of a long sentence
  // ("That is not yet a bug", "This is the test runner, not your application"), which is the
  // ordering defect this whole file exists to avoid, reproduced inside it.
  //
  // The words are LABEL's, not new ones — derived from it rather than retyped, so a rewording of
  // the table cannot leave these two surfaces calling one status two things. The reader meets the
  // same vocabulary here and in the table two screens down, and learns it once. It was already
  // inconsistent — two errored tests sharing a reason got "**2 tests could not run**" and a single
  // one got a bare bold name.
  const blockHead = (r) => `**${String(LABEL[r.status] || r.status).replace(/\*/g, "")} · ${escapeCell(r.name)}** — ${code(r.file)}`;

  for (const r of rows) {
    if (r.status !== "failed" && r.status !== "errored" && r.status !== "stale" && r.status !== "flaky") continue;
    const reason = r.reason || "no detail was recorded";
    if (r.status === "errored") {
      if (reported.has(reason)) continue;
      reported.add(reason);
      const hit = outages.get(reason);
      out.push("", hit.length > 1
        ? `**${hit.length} tests could not run**, every one of them for this reason — they are named in the table below.`
        : blockHead(r), "", `> ${quote(reason)}`);
      continue;
    }
    out.push("", blockHead(r), "", `> ${quote(reason)}`);
    // At most two suspects, under the failure they belong to. Each line already carries its named
    // evidence (suspect.mjs emits nothing without one); the file goes through code() because it is
    // a path from the customer's diff, and the evidence through quote() because it quotes strings
    // from their page — the same two rules as the URL and the reason above.
    // FAILED ONLY, checked here and not just where the field is filled in. This loop also renders
    // errored, stale and flaky, and blame under any of those blurs a status: errored is our runner
    // breaking, stale cannot tell a rename from a removal, and flaky pinned nothing down. runSuite
    // never puts suspects on those rows — this is the render refusing to print them if it ever does.
    if (r.status === "failed") {
      for (const s of (Array.isArray(r.suspects) ? r.suspects : []).slice(0, 2)) {
        out.push(">", `> Suspect: ${code(s.file)} — ${quote(s.evidence)}`);
      }
    }
  }

  // WHAT THIS CHANGE STARTED NEEDING THAT NOTHING DECLARES (lib/guard.mjs).
  //
  // Not a test verdict — a break the tests cannot see, because it does not exist until the code
  // runs somewhere that is not the author's machine. It sits with the failures rather than under
  // the roster for that reason: on a green run it is the only finding on the page, and putting it
  // below forty rows of "pass" would bury the one thing worth acting on.
  //
  // The DIFF, never the tree. A repository carrying twelve long-standing undeclared variables
  // would otherwise print the same twelve on every pull request for the rest of its life, and a
  // comment that says the same thing every time is one nobody reads by the second week.
  //
  // Every failure here is silence: no git, a shallow clone, no diff, an unreadable config — all of
  // them mean no lines, exactly as suspect.mjs degrades. Nothing about it may touch a verdict.
  try {
    const introduced = introducedConfig(getDiff({ env, cwd }) || [], getDeclared(cwd));
    out.push(...introducedCommentLines(introduced));
  } catch {
    /* a decoration that can redden a build is worse than no decoration */
  }

  // WHERE THE SCREENSHOT IS, SAID ON THE ONE SURFACE THE REVIEWER READS.
  //
  // A failed or flaky test writes a full-page screenshot and the page's visible text at the moment
  // it broke, and the shipped workflow goes to real trouble to upload that directory — its own
  // step, `if: always()`, with a comment explaining that a screenshot on a recycled runner is not
  // evidence. MEASURED, reading the comment posted by a real failing run: it mentions none of it.
  // The picture is uploaded and, for anybody who is not also reading the workflow file, unreachable.
  //
  // Directly under the failures, not in the footnote: the footnote sits below the roster, which on
  // a forty-test suite is forty rows away from the report this belongs to.
  //
  // WHAT IT REFUSES TO CLAIM. Not "the screenshot is in this run's artifacts" — whether anything
  // uploads it is a fact about the customer's workflow, which we cannot see from in here. It states
  // what this runner did, names the directory it did it in, and names what the template does with
  // that directory. Every clause is true of a workflow that dropped the upload step; the reader
  // whose artifacts list is empty then knows exactly which step is missing.
  if (s.failed || s.flaky) {
    // "Each failing RUN", not "each failure": a flaky test's first run failed and captured a page
    // too, and the block above it is headed `flaky`, not `fail`.
    out.push("", `<sub>Each failing run above wrote a full-page screenshot and the page's text, at the moment it broke, to ${code(evidenceDir || ".smolanalytics/evidence")}. The workflow template uploads that directory as the ${code("smolanalytics-evidence")} artifact${runUrl ? `, downloadable from the [run](${runUrl})` : ""}.</sub>`);
  }

  for (const p of problems) {
    out.push("", "**Part of the suite was not read.** This is the test runner, not your application.", "", `> ${quote(p)}`);
  }

  // THE ROSTER, under the report it is context for.
  out.push(
    "",
    "| | test | how | time |",
    "| --- | --- | --- | --- |",
    ...rows.map((r) => `| ${LABEL[r.status] || r.status} | ${escapeCell(r.name)} | ${how(r)} | ${secs(r.ms)} |`),
  );

  const notes = [];
  if (s.replayed) notes.push(`${s.replayed} of ${s.total} ran from a recording, with no model calls.`);
  // WHY THIS RUN WAS SLOW AND WHAT IT BOUGHT. Without this, a first run is N rows saying "agent",
  // a large number of seconds, and no explanation anywhere the reader will see — measured by
  // running one: the note under the table only ever appeared once something had ALREADY replayed,
  // so the one run that needs explaining was the one run that got none.
  const paid = economicsNote(s, agentRuns(results), "`");
  if (paid) notes.push(paid);
  if (s.flaky) {
    notes.push("Flaky is not a pass and not a bug report: the test failed, was retried from a clean page, and then passed, so the test is unreliable rather than the app being known-broken. It does not fail the build — but a test that keeps doing this is hiding an intermittent bug.");
  }
  if (s.stale || results.some((r) => r.refreshed)) {
    notes.push("Stale is not a failure: a recorded run stopped fitting the app, which a replay cannot tell apart from a rename. The agent re-checks it and rewrites the recording.");
  }
  if (s.errored) notes.push("Errors are this runner, not your app.");
  if (notes.length) out.push("", "---", "", `<sub>${notes.join(" ")}</sub>`);

  // One small-type line per test with layout findings, at the very bottom: advisory by contract
  // (lib/layout.mjs), so it renders after every verdict and is the first casualty below.
  // Who did not run, by name, under the verdicts rather than above them: the one-line claim is
  // already under the headline, and a roster there would push a reviewer's bug report off screen.
  // Above the layout notes, because a test that never ran outranks an advisory about a button.
  out.push(...selectionCommentDetail(selection));
  out.push(...layoutCommentLines(results));

  const body = out.filter((l) => l !== null).join("\n").replace(/\n{3,}/g, "\n\n") + "\n";
  if (body.length <= BODY_LIMIT) return body;
  // Layout notes are dropped before anything else, whole: they are advisory, and a bug report's
  // tail must never be trimmed while an advisory note above it survives.
  if (results.some((r) => Array.isArray(r.layout) && r.layout.length)) {
    return commentBody(results.map((r) => (Array.isArray(r.layout) && r.layout.length ? { ...r, layout: [] } : r)), { url, suite, runUrl, problems, selection, hasKey, commit, evidenceDir });
  }
  // Suspects are the next thing dropped when the comment must shrink: they are a hint about a
  // failure, and the failure's own reason is the deliverable they must never crowd out. Rebuilt
  // without them rather than sliced, so the cut below only ever falls on a body that is already
  // hint-free — a mid-body slice was how a reason lost its tail while a guess above it survived.
  if (results.some((r) => Array.isArray(r.suspects) && r.suspects.length)) {
    return commentBody(results.map((r) => (Array.isArray(r.suspects) && r.suspects.length ? { ...r, suspects: [] } : r)), { url, suite, runUrl, problems, selection, hasKey, commit, evidenceDir });
  }
  // Cut on a line boundary so the trim never lands inside a table row or a blockquote, and say so:
  // a report that is silently missing its tail is worse than one that admits it.
  const cut = body.slice(0, BODY_LIMIT);
  const whole = cut.slice(0, cut.lastIndexOf("\n") + 1) || cut;
  return `${whole}\n_Trimmed to fit GitHub's comment limit. The whole run is in the job log._\n`;
}

/**
 * A test's name is a markdown heading a customer wrote, rendered into a table cell we built.
 *
 * `<details>` in a heading collapsed every row under it, so those tests vanished from the table
 * while still being counted in the headline above it. `**` broke out of its cell. A `|` was already
 * escaped; the others were not, and they are just as ordinary in a heading as a pipe is.
 */
const escapeCell = (s) =>
  String(s)
    .replace(/\r?\n+/g, " ")
    .replace(/</g, "&lt;")
    .replace(/([|*_`[\]\\])/g, "\\$1");

/** A code span whose fence outlives any backtick run inside it — a preview URL is not our text. */
function code(s) {
  const t = String(s).replace(/\r?\n+/g, " ").trim();
  if (!t) return "";
  let longest = 0;
  for (const m of t.matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  const fence = "`".repeat(longest + 1);
  const pad = longest || /^`|`$/.test(t) ? " " : "";
  return `${fence}${pad}${t}${pad}${fence}`;
}

/**
 * Prose we quote back, with its structure defanged and its words left alone.
 *
 * Only `<` is escaped: the reason IS the bug report, and mangling its asterisks would cost more
 * than a stray italic. Truncated per reason so that one stack trace pasted into a verdict cannot
 * spend the whole comment's budget and push the other tests' reasons out of the report.
 */
const quote = (s) => {
  const t = String(s).replace(/\r?\n+/g, " ").replace(/</g, "&lt;").trim();
  return t.length <= REASON_LIMIT ? t : `${t.slice(0, REASON_LIMIT).trimEnd()}… (truncated)`;
};

// GitHub rejects a comment body over 65,536 characters with a 422, which loses the WHOLE report
// rather than its tail — on exactly the run where the most went wrong. Measured: 60 failing tests
// with an ordinary agent verdict each came to 140,053 characters.
const BODY_LIMIT = 65_000;
const REASON_LIMIT = 4_000;

/**
 * A RUN THAT NEVER STARTED HAS TO SAY SO WHERE THE VERDICTS GO.
 *
 * MEASURED, walking the shipped workflow the way a stranger installs it — copy it in first, write
 * the tests second — and pushing:
 *
 *   no such file or directory: tests/
 *     --suite points at a folder of .md files, one sentence per test.
 *
 * Exit 2, correctly. But that sentence exists only in the job log, and the template ships
 * `continue-on-error: true` for its first weeks, so the check on the pull request is GREEN and
 * carries no comment at all. Same silence for a preview that never became ready, and for a --url
 * nobody passed. Green and silent is the worst of the three answers this tool can give, and it is
 * the one a brand new install gets.
 *
 * THE MARKER IS THE ORDINARY ONE, deliberately: the next healthy run EDITS this away instead of
 * leaving a stale scare above its verdicts. And nothing here decides anything — the caller has
 * already fixed the exit code at 2, and postComment cannot change it.
 */
export function cannotStartBody(problem, { suite = "tests", runUrl = "" } = {}) {
  return [
    markerFor(suite),
    "**The end-to-end run could not start.** No test ran, so nothing below this line is known about this change.",
    "",
    `> ${quote(problem)}`,
    "",
    `<sub>This is the test runner, not your application.${runUrl ? ` The whole log is in the [run](${runUrl}).` : ""}</sub>`,
  ].join("\n") + "\n";
}

/**
 * Post that, when --comment was asked for. Never throws and never returns an exit code: the caller
 * decided the exit code before calling, and a tool that changes a build's colour because it could
 * not write a comment is reporting our problem as the customer's.
 */
export async function announceCannotStart({ problem, suite = "tests", comment = false, env = process.env, log = console.log, postCommentImpl = postComment }) {
  if (!comment) return { posted: false, reason: "--comment was not asked for" };
  const key = suite || "tests";
  try {
    const posted = await postCommentImpl({
      body: cannotStartBody(problem, { suite: key, runUrl: runUrlFor(env) }),
      marker: markerFor(key),
      env,
    });
    log(posted.posted
      ? C.dim(`  ${posted.updated ? "updated" : "posted"} the pull request comment: the run could not start.`)
      : C.dim(`  no comment posted: ${posted.reason}`));
    return posted;
  } catch (e) {
    // The caller is on its way out with exit 2 and the problem already printed. An exception thrown
    // from here would escape into bin's last-resort catch, which exits... 2 as well, but only by
    // luck of where it is called from. Swallowed on purpose: this is a courtesy on top of an
    // already-reported outage, and it may never become a second one.
    log(C.dim(`  no comment posted: ${e && e.message ? e.message : e}`));
    return { posted: false, reason: String(e && e.message ? e.message : e) };
  }
}

/**
 * Which pull request we are on. Actions states this three different ways depending on the event.
 *
 * It LIVES in lib/share.mjs now, because a single-test `--share` needs it too and lib/test.mjs
 * cannot import this file — suite.mjs imports test.mjs, and completing that cycle is how ESM hands
 * one of the two a half-initialised module. Re-exported here so every existing caller and every
 * existing test keeps importing it from where it has always been.
 */
export { prNumber };

/** Name the exact wrong thing. A 403 here has one cause and one fix, and guessing wastes an hour. */
export function apiFailure(status, body = "") {
  const tail = body ? ` ${String(body).slice(0, 160)}` : "";
  if (status === 403) {
    return `GitHub refused the comment (403). The workflow needs \`permissions: pull-requests: write\`. A pull request opened from a fork gets a read-only token and cannot be commented on at all.${tail}`;
  }
  if (status === 401) return `GitHub rejected the token (401). GITHUB_TOKEN is set but not valid for this repository.${tail}`;
  if (status === 404) return `GitHub could not find the pull request (404). The token cannot see it, which is what a fork's read-only token looks like.${tail}`;
  return `GitHub returned ${status}.${tail}`;
}

/**
 * Post the verdict as ONE comment, updated in place on every push.
 *
 * NEVER THROWS, AND NEVER CHANGES THE EXIT CODE. The verdict is already decided by the time this
 * runs. A test tool that reddens a build because it could not write a comment is uninstalled the
 * same day, and it would be reporting our problem as the customer's.
 */
export async function postComment({ body, marker, env = process.env, fetchImpl = fetch, readFile }) {
  const token = env.GITHUB_TOKEN || env.GH_TOKEN;
  const repo = env.GITHUB_REPOSITORY || "";
  if (!token) return { posted: false, reason: "GITHUB_TOKEN is not set. In Actions, pass it to the step: env: GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}" };
  if (!repo.includes("/")) return { posted: false, reason: "GITHUB_REPOSITORY is not set. --comment only works inside GitHub Actions." };
  const n = prNumber(env, readFile || ((p) => readFileSync(p, "utf8")));
  if (!n) return { posted: false, reason: "this event has no pull request. --comment needs a workflow triggered by pull_request." };

  const api = (env.GITHUB_API_URL || "https://api.github.com").replace(/\/+$/, "");
  const headers = {
    authorization: `Bearer ${token}`,
    accept: "application/vnd.github+json",
    "x-github-api-version": "2022-11-28",
    "user-agent": "smolanalytics-cli",
    "content-type": "application/json",
  };

  try {
    // Find our previous comment and edit it. Twenty pushes must not mean twenty comments — that is
    // the behaviour that gets a bot muted, and then nobody reads the one that mattered.
    let existing = 0;
    for (let page = 1; page <= 10 && !existing; page++) {
      const res = await fetchImpl(`${api}/repos/${repo}/issues/${n}/comments?per_page=100&page=${page}`, { headers });
      if (!res.ok) return { posted: false, reason: apiFailure(res.status, await res.text().catch(() => "")) };
      const list = await res.json();
      if (!Array.isArray(list) || list.length === 0) break;
      const hit = list.find((c) => typeof c.body === "string" && c.body.includes(marker));
      if (hit) existing = hit.id;
      if (list.length < 100) break;
    }

    const res = existing
      ? await fetchImpl(`${api}/repos/${repo}/issues/comments/${existing}`, { method: "PATCH", headers, body: JSON.stringify({ body }) })
      : await fetchImpl(`${api}/repos/${repo}/issues/${n}/comments`, { method: "POST", headers, body: JSON.stringify({ body }) });
    if (!res.ok) return { posted: false, reason: apiFailure(res.status, await res.text().catch(() => "")) };
    const json = await res.json().catch(() => ({}));
    return { posted: true, updated: Boolean(existing), url: json.html_url || "" };
  } catch (e) {
    return { posted: false, reason: `could not reach the GitHub API: ${e && e.message ? e.message : e}` };
  }
}

// ---- the command ---------------------------------------------------------------------------------

export async function suiteCmd({
  suite,
  url,
  test,
  plans = DEFAULT_PLANS_DIR,
  comment = false,
  // --since <ref> (lib/select.mjs). "" is the default and the default is the whole folder.
  since = "",
  cwd = process.cwd(),
  headed = false,
  yes = false,
  maxSteps = 40,
  retries = 1,
  evidenceDir = "",
  layout = "report",
  renderCheck = true,
  engine = DEFAULT_ENGINE,
  login = "",
  authFile = "",
  authDir = DEFAULT_AUTH_DIR,
  teardown = "",
  seed = "",
  emailDomain = "",
  // PARALLEL SUITE EXECUTION (lib/pool.mjs). Default 1 here so a caller that has not opted in gets
  // the serial behaviour; bin/smolanalytics.mjs computes the measured default and passes it.
  workers = 1,
  log = console.log,
  env = process.env,
  discoverImpl = discover,
  runSuiteImpl = runSuite,
  postCommentImpl = postComment,
  // Injectable for the same reason postCommentImpl is: the guarantee under test is that nothing
  // this does after the verdict can move the exit code, and that is only proved by a notifier
  // that actually fails.
  notifyImpl = notify,
  // Injectable for the same reason: the guarantee is that nothing after the verdict moves the
  // exit code, and only a tracker that really fails can prove it.
  fileIssuesImpl = fileIssues,
  notifyWhen = "problems",
  // --share (lib/share.mjs). ONE link for the whole suite — see the call at the bottom of this
  // function for why that is the right unit.
  share = false,
  publishShareImpl = publishShare,
}) {
  // Every `return 2` below is a run that never started, and each one says so on the pull request as
  // well as in the log (cannotStartBody says why). The sentence posted is the same one printed here:
  // a reader comparing the two must never find two accounts of one outage.
  // `suite || "test"` and not "tests": that expression is what the verdict comment's marker is built
  // from below, and a marker that differs by one character is a scare this tool can never edit away.
  const stop = async (problem) => {
    await announceCannotStart({ problem, suite: suite || "test", comment, env, log, postCommentImpl });
    return 2;
  };

  if (!url) {
    log(`\n${C.y("--url is missing.")} --suite says which tests to run, --url says where to run them.`);
    log(C.dim(`  npx smolanalytics test --suite tests/ --url https://your-preview.vercel.app`));
    return stop("--url is missing: --suite says which tests to run, --url says where to run them. Inside GitHub Actions on a pull request, leaving --url off entirely makes the runner ask the deployments API for this pull request's own preview.");
  }

  let tests = [];
  // Reading the folder went wrong somewhere. Never a verdict about the app: it is printed as ours
  // and it holds the exit code at 2, below the 1 that means the application is broken.
  let problems = [];
  if (suite) {
    const found = discoverImpl(suite, plans);
    if (found.missing) {
      // NOT "see templates/example-test.md": that is a path inside an npm package the reader has
      // not checked out, and pointing somebody at a file they cannot open is the same as pointing
      // them nowhere. `suggest` is the command that writes the folder for them.
      log(`\n${C.y(`no such file or directory: ${found.missing}`)}`);
      log(C.dim("  --suite points at a folder of .md files, one heading and one sentence per test."));
      log(C.dim("  npx smolanalytics suggest --url <your app>   walks the app and writes a starting set into tests/"));
      // The command is named WITHOUT backticks and with the real URL in it. A code span is not
      // decoded by GitHub, so an escaped angle bracket inside one renders as the literal characters
      // &lt; — measured, on a posted comment, from a first draft of this very sentence.
      return stop(`There is no ${found.missing} in this repository, so there were no tests to run. A test is a markdown heading and one sentence under it. To get a starting set, run: npx smolanalytics suggest --url ${url}`);
    }
    for (const n of found.notes || []) log(C.y(`\n${n}`));
    for (const e of found.errors || []) log(C.y(`\n${e}`));
    problems = found.errors || [];
    tests = found.tests;
    if (problems.length && !tests.length) {
      // Zero tests and a folder we could not open is the one case that must not print "no tests
      // found", which reads like the folder is empty when it is only shut.
      return stop(`Nothing in ${suite} could be read, so no test ran: ${problems.join(" ")}`);
    }
    if (!tests.length) {
      // "An empty folder is not a passing suite" is only true of an EMPTY FOLDER, and it was
      // printed for a folder holding a file we read and got nothing out of — measured on a .md
      // that was frontmatter and no body. The reader is then looking at a file they can see,
      // being told the folder is empty. So name the files, and say what was wrong with them.
      const read = (found.files || []).map((f) => path.relative(process.cwd(), f));
      log(`\n${C.y(`no tests found in ${suite}`)}`);
      if (read.length) {
        log(C.dim(`  Read ${read.length} file${read.length === 1 ? "" : "s"} — ${read.slice(0, 5).join(", ")}${read.length > 5 ? `, and ${read.length - 5} more` : ""} — and none held a test.`));
        log(C.dim("  A test is a markdown heading and one sentence under it, or a file that is nothing but the sentence. Frontmatter alone is not one."));
      } else {
        log(C.dim("  A test is a markdown heading and one sentence under it. An empty folder is not a passing suite."));
      }
      log(C.dim("  npx smolanalytics suggest --url <your app>   walks the app and writes a starting set into tests/"));
      return stop(
        read.length
          ? `${suite} holds ${read.length} file${read.length === 1 ? "" : "s"} and no test: ${read.slice(0, 5).join(", ")}. A test is a markdown heading and one sentence under it, or a file that is nothing but the sentence; frontmatter alone is not one. To get a starting set, run: npx smolanalytics suggest --url ${url}`
          : `${suite} holds no tests, and an empty folder is not a passing suite. A test is a markdown heading and one sentence under it. To get a starting set, run: npx smolanalytics suggest --url ${url}`,
      );
    }
  } else if (test) {
    // `--plan checkout.json --comment` is a reasonable thing to type. Joining a filename as if it
    // were a directory gives ENOTDIR on checkout.json/the-test.json, which reads as a broken tool.
    const planPath = /\.json$/i.test(plans) ? plans : path.join(plans, `${slug(test)}.json`);
    tests = [{ file: "(--test)", name: test.slice(0, 80), test, id: slug(test), planPath }];
  } else {
    log(`\n${C.y("nothing to run.")} Pass --suite tests/ or --test "one sentence".`);
    return stop("Nothing to run: the runner was given neither --suite tests/ nor --test \"one sentence\".");
  }

  // DIFF-AWARE SELECTION, between discovery and the first browser. Everything it decides is in
  // lib/select.mjs, including every reason to decide nothing: without --since, or on any doubt at
  // all, `selection.selected` is the array that went in and this is a no-op.
  const selection = selectSuite({ tests, since, cwd });
  tests = selection.selected;

  // Said BEFORE the first verdict, because the transcript below is about to arrive in blocks
  // rather than in order, and a reader who was not told why would read that as a bug.
  const lanes = Math.max(1, Math.min(Math.floor(workers) || 1, tests.length));
  log(`\n${tests.length} test${tests.length === 1 ? "" : "s"} against ${C.b(url)}${lanes > 1 ? C.dim(` · ${lanes} at a time`) : ""}`);
  // What did not run, and why, before anything else is printed. A suite that quietly checked twelve
  // of fifty and then said "12 passed" is the failure this whole feature has to not become.
  for (const line of selectionTerminalLines(selection)) log(line);
  // ONE SENTENCE FOR THE WHOLE SUITE, WHICHEVER WAY THE KEY IS WRONG.
  //
  // The missing-key case already said this once rather than fifty times (test/first-minute.test.mjs
  // measured the fifty). A key that is SET and cannot be sent — `sk-ant-…`, a trailing newline —
  // arrives at the same place and must be treated the same way: named here, then a short line per
  // test, never the whole diagnosis repeated per row.
  const suiteKeyIssue = keyProblem(env.ANTHROPIC_API_KEY);
  if (suiteKeyIssue) {
    log(C.y(suiteKeyIssue));
    log(C.dim("  Recordings that still fit will replay; anything else cannot be checked."));
  } else if (!env.ANTHROPIC_API_KEY) {
    // Said once, up front, not per test: without a key the only tests that can run are the ones
    // with a recording that still fits, and the reader should know that before reading verdicts.
    log(C.y("ANTHROPIC_API_KEY is not set. Recordings that still fit will replay; anything else cannot be checked."));
    // And where it goes, in the place the reader is standing. lib/test.mjs::keyFix.
    log(C.dim(`  ${keyFix(env)}`));
  }

  // The directory to create is the one recordings actually land in — never a .json path, which
  // would be created as a directory and then collide with the file testCmd wants to write.
  const plansDir = /\.json$/i.test(plans) ? path.dirname(plans) : plans;
  const wallStarted = Date.now();
  const results = await runSuiteImpl({ tests, url, plansDir, headed, yes, maxSteps, retries, evidenceDir, layout, renderCheck, engine, login, authFile, authDir, teardown, seed, emailDomain, workers: lanes, log, env, hasKey: Boolean(env.ANTHROPIC_API_KEY), share });
  const s = summarize(results);

  const parts = [`${s.total} test${s.total === 1 ? "" : "s"}`, `${s.passed} passed`];
  if (s.failed) parts.push(C.r(`${s.failed} failed`));
  if (s.flaky) parts.push(C.y(`${s.flaky} flaky`));
  if (s.stale) parts.push(C.y(`${s.stale} stale`));
  if (s.errored) parts.push(C.y(`${s.errored} could not run`));
  // summarize().ms is the sum of every test's own duration, and it stays that in the comment and
  // everywhere else — identical to a serial run, which is the point. But printing only that after a
  // parallel run tells the reader 39.6s about a run their stopwatch says took 7s, so when lanes are
  // in play the wall clock is named first and the old number keeps its meaning explicitly.
  const wall = Date.now() - wallStarted;
  log(lanes > 1
    ? `\n${parts.join(" · ")} ${C.dim(`· ${secs(wall)} · ${secs(s.ms)} of test time across ${lanes} workers`)}`
    : `\n${parts.join(" · ")} ${C.dim(`· ${secs(s.ms)}`)}`);
  if (s.replayed) log(C.dim(`${s.replayed} replayed from a recording, with no model calls.`));
  // The same sentence the comment carries, from the same function: a reader who checks the log
  // against the comment must not find two accounts of what this run cost.
  const paidNote = economicsNote(s, agentRuns(results));
  if (paidNote) log(C.dim(paidNote));
  // Said again under the counts: the counts are what a reader who scrolled to the end sees, and
  // they are counts of what RAN.
  for (const line of selectionTailLines(selection)) log(line);
  // ABOVE THE FAILURES, FOR THE SAME REASON THE COMMENT PUTS IT THERE (lib/cluster.mjs): when
  // twelve tests are red, the first thing worth knowing is that eleven of them are one change.
  // Silent unless they genuinely group.
  for (const line of clusterNote(results).split("\n").filter(Boolean)) log(C.y(line));
  for (const r of results.filter((r) => r.status === "failed")) {
    log(`  ${C.r("fail")} ${r.name} ${C.dim(`· ${r.file}`)}`);
    // The same two suspect lines the comment gets, under the same failure. Dim, because they are a
    // hint from the diff, and the red verdict above them is the fact.
    for (const s of (Array.isArray(r.suspects) ? r.suspects : []).slice(0, 2)) log(C.dim(`       suspect: ${s.file} — ${s.evidence}`));
  }
  // Named like failed and stale are, and worded as reliability: a flaky line that read as a pass
  // would bury the warning, and one that read as a bug would be a claim nobody verified.
  for (const r of results.filter((r) => r.status === "flaky")) log(`  ${C.y("flaky")} ${r.name} ${C.dim("· failed once, passed on retry — unreliable, not counted as a pass")}`);
  // The exit-code consequence is said here, not left for someone to discover in CI: a warning
  // nobody knew was a warning reads as a pass.
  if (s.flaky) log(C.dim("flaky does not fail the build: the retry verified the behaviour, so it warns instead of gating."));
  for (const r of results.filter((r) => r.status === "stale")) log(`  ${C.y("stale")} ${r.name} ${C.dim("· not a failure, the recording stopped fitting")}`);
  // Named, like the other two. "2 could not run" with no names is unactionable in a suite of
  // twenty, and errored is the status whose reason is the one thing the reader can act on.
  for (const r of results.filter((r) => r.status === "errored")) log(`  ${C.y("error")} ${r.name} ${C.dim(`· ${r.reason}`)}`);

  // THE SHIP VERDICT, LAST, because it is the only line most people read.
  //
  // Everything above answers "did the tests pass". The question a person actually asks is "can we
  // ship", and the difference between them is every flow this run left unverified — the flaky one
  // that proves nothing, the stale recording nobody re-derived, the test --since skipped, the run
  // that errored on our side. Those facts were all here already and none of them were being
  // composed into an answer.
  //
  // No vendor in this field prints this, because printing it honestly means listing what you did
  // not check, and that is a bad look for anyone whose price depends on seeming comprehensive.
  // It is not a bad look for us: `stale`, `flaky`, `errored` and --since's skip line already exist
  // precisely so this product never claims more than it verified. See lib/ship.mjs.
  log("");
  for (const line of shipText(results, { selection, suite: suite || "tests", url }).split("\n")) {
    log(line.startsWith("  ") ? C.dim(line) : line);
  }

  // FILE THE BUG, which is already written. Only real failures, one issue per test however many
  // times it fails, and never for stale/flaky/errored — those are our artefact aging, a thing
  // nobody can act on yet, and our own runner breaking. Filing any of them against somebody's
  // product is a lie about whose fault it is. See lib/issue.mjs.
  try {
    const filed = await fileIssuesImpl(results, {
      // ?. — ciContext RETURNS NULL by design when there is no commit, repo, branch, PR or run
      // URL to be had, which is every run outside a git repository and outside CI. See below.
      url, commit: ciContext({ env, cwd })?.shortCommit || "", runUrl: runUrlFor(env), suite: suite || "tests",
    }, { env });
    const line = issueLine(filed);
    if (line) log(C.dim(line));
  } catch {
    /* a tracker that can break a build is a tracker people disconnect; this cannot */
  }

  // TELL SOMEBODY. Last, after the verdict is decided and printed, so a Slack outage cannot reach
  // the exit code — and only when there is something worth interrupting a person for. The message
  // is the ship verdict itself, because "3 failed" is a notification people mute in a week.
  try {
    const told = await notifyImpl(results, {
      selection, suite: suite || "tests", url, when: notifyWhen,
      runUrl: runUrlFor(env), env,
    });
    const line = notifyLine(told);
    if (line) log(C.dim(line));
  } catch {
    /* a notifier that can break a build is a notifier people delete; this cannot */
  }

  if (comment) {
    const runUrl = runUrlFor(env);
    const key = suite || "test";
    // Inside the `if`, because ciContext falls back to `git rev-parse` and a run nobody asked to
    // comment on should not shell out. It reads the pull request's HEAD sha, never GITHUB_SHA — on
    // a pull_request event that is the merge commit Actions invented, which exists in nobody's
    // history and would be seven characters a reader could not find anywhere.
    // ?. BECAUSE ciContext DELIBERATELY RETURNS NULL. It hands back null when it can find no
    // commit, repo, branch, pull request or run URL — "nothing at all is null, not an object of
    // empty strings" (lib/share.mjs). Outside a git repository that is exactly what happens, and
    // MEASURED, in a scratch directory that was not a repo: `test --comment` printed the verdict
    // and the whole summary, then "the run could not complete: Cannot read properties of null
    // (reading 'shortCommit')" and "Nothing was learned about this change." — which was false, the
    // results were on the screen above it. With the guard the run reaches postComment, which says
    // the useful thing instead: no comment posted, GITHUB_TOKEN is not set.
    const commit = ciContext({ env, cwd })?.shortCommit || "";
    const posted = await postCommentImpl({ body: commentBody(results, { url, suite: key, runUrl, problems, selection, hasKey: Boolean(env.ANTHROPIC_API_KEY), commit, evidenceDir }), marker: markerFor(key), env });
    log(posted.posted ? C.dim(`  ${posted.updated ? "updated" : "posted"} the pull request comment.`) : C.dim(`  no comment posted: ${posted.reason}`));
  }

  // A real bug still outranks our own problem: exit 1 stays 1. Anything else with an unreadable
  // file in it is 2, because part of the folder was never checked and 0 would call that all good.
  const code = exitCode(results);
  const finalCode = code === 1 ? 1 : problems.length ? 2 : code;

  // --share, LAST, and computed AFTER the exit code so that nothing below can reach it.
  //
  // ONE SHARE PER SUITE RUN, NOT ONE PER TEST, and this is the decision the whole feature turns on.
  // The thing a person sends is "what happened on this pull request", and that is one page: a
  // twenty-test suite printing twenty URLs is twenty addresses and no message, and nobody forwards
  // twenty links. Loom prints one link per recording, not one per scene. Three consequences make it
  // the cheaper choice as well as the better one: under --workers 8 a per-test share would
  // interleave N POSTs into a transcript that is already buffered per test; a POST that fails would
  // need N separate failure lines for one outage; and the single-test `test --share` becomes the
  // degenerate one-test case of the SAME bundle, so the receiving half implements one shape rather
  // than two. The cost, stated: a reader who wants only test 7 gets a page with twenty on it, and
  // deep-linking to one test inside the page is the receiving half's job, not another POST.
  if (share) {
    // A share page carries only what ran, because a row with no verdict on it would read as one.
    // The person about to send the link is told so here — the page cannot say it for itself, and a
    // link that looks like a whole suite but is twelve of fifty is the same lie in a nicer font.
    for (const line of selectionShareLines(selection, results.length)) log(line);
    // Wrapped HERE as well as inside publishShare, because this is where `finalCode` lives and the
    // guarantee is about THIS variable. publishShareImpl is injectable, so the only guard that
    // cannot be swapped out from under the exit code is the one on this side of the call.
    try {
    await publishShareImpl({
      tests: results.map((r) => ({
        name: r.name,
        file: r.file,
        sentence: r.test,
        status: r.status,
        mode: r.mode,
        reason: r.reason,
        proof: r.share?.proof || "",
        durationMs: r.ms,
        steps: r.share?.steps || [],
        suspects: r.suspects || [],
        evidence: r.share?.evidence || null,
      })),
      url,
      engine,
      suite: suite || "",
      exitCode: finalCode,
      projectId: env.SMOLANALYTICS_PROJECT || "",
      env,
      log,
    });
    } catch (e) {
      log(C.dim(`  not shared: ${e && e.message ? e.message : e}. The verdict above still stands.`));
    }
  }

  return finalCode;
}
