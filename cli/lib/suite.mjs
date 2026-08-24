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
// THE THREE STATUSES ARE THE PRODUCT, AND THIS FILE NEVER BLURS THEM:
//   passed / failed  the app did, or did not, do what the sentence describes. Failed is a bug report.
//   stale            a RECORDING stopped fitting. A replay cannot tell "renamed" from "removed", so
//                    it is never red and never worded as a failure; the agent re-checks it.
//   errored          this runner could not run — no browser, no key, no network. Never the app.
// Blur them and a copy change pages somebody at 2am, or an outage on our side reads to a customer
// as their checkout being broken.

import { readdirSync, readFileSync, statSync, mkdirSync, existsSync } from "node:fs";
import path from "node:path";
import { testCmd } from "./test.mjs";

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
  const m = /^\uFEFF?---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/.exec(String(text));
  if (!m) return { meta: {}, body: String(text) };
  const meta = {};
  for (const line of m[1].split(/\r?\n/)) {
    const kv = /^([A-Za-z][\w-]*)\s*:\s*(.*)$/.exec(line.trim());
    if (!kv) continue;
    meta[kv[1].toLowerCase()] = kv[2].trim().replace(/^["']|["']$/g, "");
  }
  return { meta, body: String(text).slice(m[0].length) };
}

export function parseSuite(file, text) {
  const { meta, body: afterMeta } = frontmatter(text);
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
    const t = line.replace(QUOTE, "").replace(LIST, "").trim();
    if (t) cur.body.push(t);
  }
  flush();

  if (tests.length) return tests;

  // No headings at all: the file is one test. Someone whose whole test is a single sentence should
  // not have to learn a document structure to run it.
  const body = lines.map((l) => l.replace(QUOTE, "").replace(LIST, "").trim()).filter(Boolean).join(" ");
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

function walk(dir, io, out = []) {
  // Sorted, always. Recording filenames and the pull request table are both derived from this
  // order, and a table that reshuffles between runs makes a reviewer re-read the whole thing.
  for (const e of io.list(dir).sort((a, b) => a.name.localeCompare(b.name))) {
    if (e.name.startsWith(".") || e.name === "node_modules") continue;
    const p = path.join(dir, e.name);
    if (e.dir) walk(p, io, out);
    else if (/\.md$/i.test(e.name)) out.push(p);
  }
  return out;
}

/** Find every test under a directory (or in a single file), each with the recording it owns. */
export function discover(target, plansDir = DEFAULT_PLANS_DIR, io = nodeIo) {
  if (!io.exists(target)) return { tests: [], missing: target };
  const isDir = io.isDir(target);
  const files = isDir ? walk(target, io) : [target];
  // A recording is named after the test's path WITHIN the suite, never the path as typed. Otherwise
  // `--suite tests/` and `--suite /home/me/app/tests` name the same test's recording differently,
  // a developer's local runs and CI never share a cache, and every CI run is a fresh agent run —
  // exactly the cost the cache exists to avoid.
  const base = isDir ? target : path.dirname(target);
  const tests = [];
  const taken = new Set();
  for (const f of files) {
    const within = path.relative(base, f).replace(/\.md$/i, "");
    for (const t of parseSuite(f, io.read(f))) {
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
  return { tests, missing: "" };
}

// ---- running them -----------------------------------------------------------------------------

/** testCmd's exit code, for the paths that end before any verdict is produced. */
function fromExit(code) {
  if (code === 0) return "passed";
  if (code === 1) return "failed";
  return "errored";
}

/**
 * Say why a test produced no verdict at all.
 *
 * The pull request comment prints this sentence. "No detail was recorded" is the least useful thing
 * it could say, and the case it says it in — a first run with no recordings and no key — is the one
 * a new user hits first. Name the actual missing thing.
 */
export function noVerdictReason(code, { hasKey, hasPlan }) {
  if (code === 1) return "The run ended without a verdict, so nothing was observed.";
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
  log = console.log,
  runTest = testCmd,
  mkdir = (d) => mkdirSync(d, { recursive: true }),
  hasPlan = (p) => existsSync(p),
  hasKey = Boolean(process.env.ANTHROPIC_API_KEY),
}) {
  // Create the directory BEFORE the first test. testCmd writes the recording only after a test
  // passes, so a missing directory turns a green run into "errored" at the last instruction — the
  // most confusing possible outcome, and the one a first-ever CI run hits every time.
  try {
    mkdir(plansDir);
  } catch (e) {
    log(C.r(`could not create ${plansDir}: ${e && e.message}`));
  }

  const results = [];
  for (const t of tests) {
    log(`\n${C.b(t.name)}  ${C.dim(t.file)}`);
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
        log,
        onRun: (r) => runs.push(r),
      });
    } catch (e) {
      // One test throwing must not abandon the rest of the suite: the reviewer needs the whole
      // picture, and a crash in test 2 of 9 hiding seven verdicts is worse than the crash.
      log(C.r(`  the runner threw: ${e && e.message ? e.message : e}`));
      runs.push({ status: "errored", mode: "agent", reason: `The runner threw: ${e && e.message ? e.message : e}. This is the test runner, not your application.` });
    }

    const last = runs[runs.length - 1];
    const status = last ? last.status : fromExit(code);
    const wentStale = runs.some((r) => r.status === "stale");
    let reason = last?.reason || noVerdictReason(code, { hasKey, hasPlan: hadPlan });
    if (status === "stale" && !hasKey) {
      reason += " ANTHROPIC_API_KEY is not set, so the agent could not re-check it.";
    }
    results.push({
      ...t,
      status,
      mode: last?.mode || "",
      reason: reason.trim(),
      ms: Date.now() - started,
      // A recording that went stale and was then re-verified is worth saying out loud: it is the
      // moment the tool did the thing it promises, and it explains why that test was slow today.
      refreshed: wentStale && status === "passed",
    });
  }
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
    replayed: results.filter((r) => r.mode === "replay" && r.status === "passed").length,
    ms: results.reduce((a, r) => a + r.ms, 0),
  };
}

/**
 * The exit code contract, and why stale is not zero.
 *
 * 1 means the application is broken — the only status that should ever redden a build.
 * 2 means this runner could not finish, which includes a recording that stopped fitting when there
 *   was no key to re-check it with. Exiting 0 there would report "all good" about a test nobody
 *   verified, which is the one lie a test tool cannot tell.
 */
export function exitCode(results) {
  if (results.some((r) => r.status === "failed")) return 1;
  if (results.some((r) => r.status === "errored" || r.status === "stale")) return 2;
  return 0;
}

const secs = (ms) => `${(ms / 1000).toFixed(1)}s`;

// ---- the pull request comment -------------------------------------------------------------------

const RANK = { failed: 0, errored: 1, stale: 2, passed: 3 };
const LABEL = { passed: "pass", failed: "**fail**", stale: "stale", errored: "error" };

export const markerFor = (suite) => `<!-- smolanalytics-e2e:${slug(suite)} -->`;

export function commentBody(results, { url = "", suite = "tests", runUrl = "" } = {}) {
  const s = summarize(results);
  const head = [`${s.passed} passed`];
  if (s.failed) head.unshift(`${s.failed} failed`);
  if (s.errored) head.push(`${s.errored} could not run`);
  if (s.stale) head.push(`${s.stale} stale`);

  const rows = [...results].sort((a, b) => RANK[a.status] - RANK[b.status] || a.name.localeCompare(b.name));
  const how = (r) =>
    r.status === "errored" ? "did not run"
    : r.refreshed ? "recording was stale, agent re-checked"
    : r.mode === "replay" ? "replayed, no model calls"
    : r.mode === "agent" ? "agent"
    : "";

  const out = [
    markerFor(suite),
    `**${head.join(" · ")}** in ${secs(s.ms)}`,
    "",
    url ? `Against \`${url}\`${runUrl ? ` · [run log](${runUrl})` : ""}` : "",
    "",
    "| | test | how | time |",
    "| --- | --- | --- | --- |",
    ...rows.map((r) => `| ${LABEL[r.status] || r.status} | ${escapeCell(r.name)} | ${how(r)} | ${secs(r.ms)} |`),
  ];

  // The failure text is the actual deliverable. It goes below the table in full, because the table
  // cell truncates it and the person reading has not been watching the browser.
  for (const r of rows) {
    if (r.status !== "failed" && r.status !== "errored" && r.status !== "stale") continue;
    out.push("", `**${escapeCell(r.name)}** — \`${r.file}\``, "", `> ${(r.reason || "no detail was recorded").replace(/\n+/g, " ")}`);
  }

  const notes = [];
  if (s.replayed) notes.push(`${s.replayed} of ${s.total} ran from a recording, with no model calls.`);
  if (s.stale || results.some((r) => r.refreshed)) {
    notes.push("Stale is not a failure: a recorded run stopped fitting the app, which a replay cannot tell apart from a rename. The agent re-checks it and rewrites the recording.");
  }
  if (s.errored) notes.push("Errors are this runner, not your app.");
  if (notes.length) out.push("", "---", "", `<sub>${notes.join(" ")}</sub>`);

  return out.filter((l) => l !== null).join("\n").replace(/\n{3,}/g, "\n\n") + "\n";
}

const escapeCell = (s) => String(s).replace(/\|/g, "\\|").replace(/\n+/g, " ");

/** Which pull request we are on. Actions states this three different ways depending on the event. */
export function prNumber(env, readFile = (p) => readFileSync(p, "utf8")) {
  if (env.GITHUB_EVENT_PATH) {
    try {
      const ev = JSON.parse(readFile(env.GITHUB_EVENT_PATH));
      const n = ev?.pull_request?.number ?? ev?.issue?.number;
      if (n) return Number(n);
    } catch {
      /* fall through to the ref */
    }
  }
  const m = /refs\/pull\/(\d+)\//.exec(env.GITHUB_REF || "");
  if (m) return Number(m[1]);
  if (/^\d+$/.test(env.PR_NUMBER || "")) return Number(env.PR_NUMBER);
  return 0;
}

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
  headed = false,
  yes = false,
  maxSteps = 40,
  log = console.log,
  env = process.env,
  discoverImpl = discover,
  runSuiteImpl = runSuite,
  postCommentImpl = postComment,
}) {
  if (!url) {
    log(`\n${C.y("--url is missing.")} --suite says which tests to run, --url says where to run them.`);
    log(C.dim(`  npx smolanalytics test --suite tests/ --url https://your-preview.vercel.app`));
    return 2;
  }

  let tests = [];
  if (suite) {
    const found = discoverImpl(suite, plans);
    if (found.missing) {
      log(`\n${C.y(`no such file or directory: ${found.missing}`)}`);
      log(C.dim("  --suite points at a folder of .md files, one sentence per test. See templates/example-test.md."));
      return 2;
    }
    tests = found.tests;
    if (!tests.length) {
      log(`\n${C.y(`no tests found in ${suite}`)}`);
      log(C.dim("  A test is a markdown heading and one sentence under it. An empty folder is not a passing suite."));
      return 2;
    }
  } else if (test) {
    // `--plan checkout.json --comment` is a reasonable thing to type. Joining a filename as if it
    // were a directory gives ENOTDIR on checkout.json/the-test.json, which reads as a broken tool.
    const planPath = /\.json$/i.test(plans) ? plans : path.join(plans, `${slug(test)}.json`);
    tests = [{ file: "(--test)", name: test.slice(0, 80), test, id: slug(test), planPath }];
  } else {
    log(`\n${C.y("nothing to run.")} Pass --suite tests/ or --test "one sentence".`);
    return 2;
  }

  log(`\n${tests.length} test${tests.length === 1 ? "" : "s"} against ${C.b(url)}`);
  if (!env.ANTHROPIC_API_KEY) {
    // Said once, up front, not per test: without a key the only tests that can run are the ones
    // with a recording that still fits, and the reader should know that before reading verdicts.
    log(C.y("ANTHROPIC_API_KEY is not set. Recordings that still fit will replay; anything else cannot be checked."));
  }

  // The directory to create is the one recordings actually land in — never a .json path, which
  // would be created as a directory and then collide with the file testCmd wants to write.
  const plansDir = /\.json$/i.test(plans) ? path.dirname(plans) : plans;
  const results = await runSuiteImpl({ tests, url, plansDir, headed, yes, maxSteps, log, hasKey: Boolean(env.ANTHROPIC_API_KEY) });
  const s = summarize(results);

  const parts = [`${s.total} test${s.total === 1 ? "" : "s"}`, `${s.passed} passed`];
  if (s.failed) parts.push(C.r(`${s.failed} failed`));
  if (s.stale) parts.push(C.y(`${s.stale} stale`));
  if (s.errored) parts.push(C.y(`${s.errored} could not run`));
  log(`\n${parts.join(" · ")} ${C.dim(`· ${secs(s.ms)}`)}`);
  if (s.replayed) log(C.dim(`${s.replayed} replayed from a recording, with no model calls.`));
  for (const r of results.filter((r) => r.status === "failed")) log(`  ${C.r("fail")} ${r.name} ${C.dim(`· ${r.file}`)}`);
  for (const r of results.filter((r) => r.status === "stale")) log(`  ${C.y("stale")} ${r.name} ${C.dim("· not a failure, the recording stopped fitting")}`);

  if (comment) {
    const runUrl = env.GITHUB_RUN_ID && env.GITHUB_REPOSITORY
      ? `${(env.GITHUB_SERVER_URL || "https://github.com").replace(/\/+$/, "")}/${env.GITHUB_REPOSITORY}/actions/runs/${env.GITHUB_RUN_ID}`
      : "";
    const key = suite || "test";
    const posted = await postCommentImpl({ body: commentBody(results, { url, suite: key, runUrl }), marker: markerFor(key), env });
    log(posted.posted ? C.dim(`  ${posted.updated ? "updated" : "posted"} the pull request comment.`) : C.dim(`  no comment posted: ${posted.reason}`));
  }

  return exitCode(results);
}
