// When a test FAILS on a pull request, name the changed files most likely responsible.
//
// The competing product ships a 133-file "diffs" package to answer the same question. This is the
// computed-first version: no model call, no heuristic prose, just the intersection of two things we
// hold in our hands — what the failing run OBSERVED (the control names it clicked, the URL paths it
// visited, the proof text that vanished) and what the pull request CHANGED (git's own diff).
//
// THE HONESTY SPINE, APPLIED TO BLAME. A failed verdict earns trust by describing exactly what was
// seen; a suspicion next to it must clear the same bar or it drags the verdict down with it.
// So the two rules that shape everything below:
//
//   NO SUSPICION WITHOUT NAMED EVIDENCE. Every line this module emits says which string or path
//   connects the file to the test: "src/Checkout.tsx — this PR removed the string 'Proceed to
//   checkout' this test clicks". A file we cannot connect is never mentioned.
//
//   ZERO MATCHES = SAY NOTHING AT ALL. "These 14 files changed, one of them probably did it" is
//   the whole diff ranked as vaguely suspicious — noise wearing the uniform of analysis. If no
//   changed file touches anything the test interacted with, no suspect lines appear anywhere.
//
// And because this decorates a verdict rather than making one: nothing in this file may change a
// status or an exit code, and every failure of OURS — no git, no refs, a repo too big to diff —
// degrades silently to "no suspects". A blame hint that can redden a build is worse than none.

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";

// ---- what the failing run observed --------------------------------------------------------------

// How strongly each kind of fact indicts a file whose diff contains it. The proof is the strongest:
// it is the one string that exists on the page ONLY because the feature works, so a diff that
// removes it is a diff that removed the feature's outcome. A clicked control is next — a rename is
// the single most common way a recorded test breaks. Strings quoted in the failure prose were
// written by the agent about the breakage, one step less direct. A literal route path in a changed
// line is weaker still, and a mere path-segment match (tier two) is the floor: real signal, but
// two files can share a name by coincidence in a way two exact strings cannot.
const WEIGHT = { proof: 40, click: 30, fill: 30, named: 20, path: 15 };
const SEGMENT = 5;
// A string found only in ADDED lines still connects the file to the test — the label moved, or its
// casing changed — but "this PR removed the button you click" is a stronger story than "this PR
// also contains that phrase somewhere new", so removal outranks addition at equal kind.
const ADDED_PENALTY = 5;

// Strings shorter than this (letters and digits only) never become facts. Measured with the guard
// off: a two-letter button label "OK" scored src/auth/session.ts as a 30-point suspect because its
// diff removed `const TOKEN_TTL = 3600;` — substring containment is the whole matching mechanism,
// so a short fact is a wildcard that indicts whichever file happened to change.
const MIN_CHARS = 3;

// Segments that never count in a path-to-file match, on either side. Measured with the list off: a
// run that visited /app flagged all three changed files under a Next.js app/ directory — layout,
// page, and a stylesheet, none of them about the failure. These are the directory names frameworks
// impose, not names anyone chose to describe a feature, and matching them blames scaffolding.
const STOP_SEGMENTS = new Set([
  "src", "app", "apps", "pages", "page", "index", "public", "static", "assets",
  "components", "component", "lib", "libs", "utils", "util", "styles", "style",
  "dist", "build", "test", "tests", "api",
]);

const norm = (s) => String(s).toLowerCase().replace(/[^a-z0-9]/g, "");

/** Route segments worth matching: normalized, not framework scaffolding, not a bare id number. */
function routeSegments(pathname) {
  return String(pathname)
    .split("/")
    .map((seg) => norm(seg))
    .filter((seg) => seg.length >= MIN_CHARS && !/^\d+$/.test(seg) && !STOP_SEGMENTS.has(seg));
}

function addString(map, text, kind) {
  const t = String(text ?? "").trim();
  if (t.replace(/[^A-Za-z0-9]/g, "").length < MIN_CHARS) return;
  const key = t.toLowerCase();
  const prev = map.get(key);
  // One string can arrive as several kinds — the clicked label is usually also quoted in the
  // failure prose. Keep the strongest reading: "this test clicks" is better evidence than "the
  // failure names", and emitting both would be the same fact counted twice.
  if (!prev || WEIGHT[kind] > WEIGHT[prev.kind]) map.set(key, { text: t, kind });
}

/**
 * `observed` is false for a bare `/token` picked out of the failure prose, and it is the difference
 * between a page and a filename. Measured: an agent quoting a dev error overlay wrote "…at
 * /Users/dana/shop/src/users/cart.ts:12:3", which became a route with the segments users, dana,
 * shop and cartts — and then blamed src/users/profile.ts with "its path matches
 * /Users/dana/shop/src/users/cart.ts, a page this test visited". The test visited no such page;
 * that string is a stack frame. A real URL's pathname is an observation and keeps its extension
 * (/sitemap.xml is a page somebody tests); a bare token that names a file is not one.
 */
function addPath(strings, routes, pathname, observed = true) {
  let p = String(pathname ?? "").trim();
  if (!p.startsWith("/")) return;
  p = p.replace(/\/+$/, "") || "/";
  if (p === "/") return; // every app has a root; it names nothing
  if (!observed && /\/[^/]*\.[A-Za-z0-9]{1,8}$/.test(p)) return;
  addString(strings, p, "path");
  const segments = routeSegments(p);
  if (segments.length && !routes.some((r) => r.path === p)) routes.push({ path: p, segments });
}

// test.mjs's describe() writes the label for a TERMINAL, and the wire carries that same string:
// `step.label` is describe() with only its tick and step number removed. So every label arrives
// wearing colour codes and a trailing measurement — `click button "Buy"\x1b[2m 480ms\x1b[0m`, or
// `click button "Buy"\x1b[31m — locator.click: Timeout 10000ms exceeded.\x1b[0m`.
//
// MEASURED, against a real Chromium driving a real page (scratch harness, three real steps):
// collectFacts returned {strings: [], routes: []}. The click and goto patterns below are anchored
// at the end of the string, and no real label has ever ended where they expect. Every fact this
// module gets from the browser's own steps was being dropped in production, and the only reason
// the suite still named a culprit is that the agent's failure prose happens to quote the control
// too — a weaker fact (20, not 30) that a laconic agent may not write at all. The unit tests all
// passed because they were written against the label we imagined rather than the one we ship.
const ANSI = /\u001b\[[0-9;]*m/g;

/** describe()'s label, with the terminal's colour and its trailing measurement taken back off. */
function plainLabel(step) {
  const raw = String(step?.do ?? "").replace(ANSI, "");
  // The step's OWN fields say exactly what describe() appended, so the suffix comes off by length
  // rather than by pattern — a control named "wait 500ms" or an error message containing " — "
  // would both defeat a pattern, and the wire has carried `ok`, `detail` and `ms` all along.
  const detail = typeof step?.detail === "string" ? step.detail : "";
  if (detail && raw.endsWith(` — ${detail}`)) return raw.slice(0, raw.length - detail.length - 3);
  if (Number.isFinite(step?.ms) && raw.endsWith(` ${step.ms}ms`)) return raw.slice(0, raw.length - String(step.ms).length - 3);
  // A label handed to us without its step (an older recording, a direct caller) still loses a
  // plain measurement; a detail we cannot measure is left alone and simply matches nothing.
  return raw.replace(/ \d+ms$/, "");
}

/** The `do` labels report() puts on the wire are our own describe() grammar; read them back. */
function factsFromStep(strings, routes, step) {
  const l = plainLabel(step);
  let m = /^click [a-z]+ "([\s\S]*)"$/.exec(l);
  if (m) return addString(strings, m[1], "click");
  if (l.startsWith('fill "')) {
    // The typed text is JSON at the end of the label, and it may itself contain the separator:
    // filling a search box with `a" = "b` puts `" = ` inside the JSON, and a single lastIndexOf
    // sliced the name as `Email" = "a\`. So walk the candidates from the right and take the first
    // whose tail actually parses as the string describe() stringified. None parsing means this is
    // not the shape we think it is, and a fact we cannot read is not a fact.
    for (let cut = l.lastIndexOf('" = '); cut > 6; cut = l.lastIndexOf('" = ', cut - 1)) {
      try {
        JSON.parse(l.slice(cut + 4));
      } catch {
        continue;
      }
      return addString(strings, l.slice(6, cut), "fill");
    }
    return;
  }
  m = /^goto (\S+)$/.exec(l);
  if (m) {
    try {
      addPath(strings, routes, new URL(m[1]).pathname);
    } catch {
      addPath(strings, routes, m[1]);
    }
  }
}

/**
 * Everything the failing run is known to have interacted with, from the three records a suite
 * already holds: the wire runs (steps and reasons), the recording on disk (roles, names, the
 * proof), and the URL the run started at. Nothing here is inferred — a fact either appears in one
 * of those records or it is not a fact.
 */
export function collectFacts({ runs = [], reason = "", plan = null, url = "" } = {}) {
  const strings = new Map();
  const routes = [];

  if (plan && typeof plan === "object") {
    if (typeof plan.proof === "string") addString(strings, plan.proof, "proof");
    for (const s of Array.isArray(plan.steps) ? plan.steps : []) {
      if (!s || typeof s !== "object") continue;
      if (s.kind === "click") addString(strings, s.name, "click");
      else if (s.kind === "fill") addString(strings, s.name, "fill");
      else if (s.kind === "goto") {
        try {
          addPath(strings, routes, new URL(s.url).pathname);
        } catch {
          /* a goto we cannot parse is a fact we do not have */
        }
      }
    }
    try {
      if (typeof plan.startUrl === "string") addPath(strings, routes, new URL(plan.startUrl).pathname);
    } catch {
      /* same */
    }
  }

  for (const run of Array.isArray(runs) ? runs : []) {
    for (const s of Array.isArray(run?.steps) ? run.steps : []) {
      if (!s || typeof s !== "object") continue;
      if (typeof s.do === "string") factsFromStep(strings, routes, s);
      // The raw agent-attempt shape, for anyone calling this without going through the wire.
      else if (s.target && s.action) {
        if (s.action.kind === "click") addString(strings, s.target.name, "click");
        else if (s.action.kind === "fill") addString(strings, s.target.name, "fill");
      }
    }
  }

  // The failure prose is written to name the page and the control (test.mjs's SYSTEM demands it),
  // so its quoted strings and /paths are observations, not decoration. stalenessNote() quotes the
  // vanished proof the same way, which is how a stale-then-failed test keeps its best fact even
  // though the recording write never happened.
  const prose = String(reason ?? "");
  const withoutUrls = prose.replace(/https?:\/\/[^\s"'`)\]]+/g, (u) => {
    try {
      addPath(strings, routes, new URL(u).pathname);
    } catch {
      /* not a URL after all */
    }
    return " ";
  });
  for (const m of withoutUrls.matchAll(/"([^"\n]{1,120})"/g)) addString(strings, m[1], "named");
  // false: these are tokens that LOOK like paths, not URLs the run was ever at. See addPath.
  for (const m of withoutUrls.matchAll(/(?:^|[\s"'`(])((?:\/[A-Za-z0-9._-]+)+)/g)) addPath(strings, routes, m[1], false);

  try {
    if (url) addPath(strings, routes, new URL(url).pathname);
  } catch {
    /* no fact */
  }

  return { strings: [...strings.values()], routes };
}

// ---- what the pull request changed --------------------------------------------------------------

/**
 * A unified diff, reduced to what scoring needs: per file, the removed and added lines. Hunk
 * content is only read between a `@@` header and the next `diff --git` — the metadata block also
 * has lines starting with `-` and `+` (`--- a/…`, `+++ b/…`), and reading those as content once
 * scored a file for "matching" its own header.
 */
export function parseDiff(text) {
  const files = [];
  let cur = null;
  let inHunk = false;
  for (const line of String(text).split("\n")) {
    if (line.startsWith("diff --git ")) {
      inHunk = false;
      cur = { file: "", oldFile: "", removed: [], added: [], binary: false, renamed: false };
      const m = /^diff --git a\/(.*?) b\/(.*)$/.exec(line);
      if (m) {
        cur.oldFile = m[1];
        cur.file = m[2];
      }
      files.push(cur);
      continue;
    }
    if (!cur) continue;
    if (line.startsWith("@@")) {
      inHunk = true;
      continue;
    }
    if (!inHunk) {
      // A pure rename has no hunks at all, and a binary change has no text ones; both are named
      // only by these metadata lines, so they are read here rather than skipped.
      if (line.startsWith("rename from ")) cur.renamed = true;
      else if (line.startsWith("rename to ")) {
        cur.renamed = true;
        cur.file = line.slice(10);
      } else if (line.startsWith("Binary files ") || line.startsWith("GIT binary patch")) cur.binary = true;
      else if (line.startsWith("+++ b/")) cur.file = line.slice(6);
      else if (line.startsWith("--- a/")) cur.oldFile = line.slice(6);
      // `+++ /dev/null` is a deletion: the only name the file has is its old one.
      else if (line === "+++ /dev/null") cur.file = cur.oldFile;
      continue;
    }
    if (line.startsWith("\\")) continue; // "\ No newline at end of file"
    if (line.startsWith("-")) cur.removed.push(line.slice(1));
    else if (line.startsWith("+")) cur.added.push(line.slice(1));
  }
  return files.filter((f) => f.file);
}

// A ROUTE, not a prefix of one. Substring containment is right for a page's text — "Order placed"
// inside "Order placed successfully" really is the string this test proved itself with — but it is
// wrong for a path, because a path names a whole thing. Measured: a run that visited /cart, against
// a PR whose only change was `const url = "/cart-abandoned";`, printed `this PR removed "/cart", a
// path this test visited` as its single suspect. That PR did not touch /cart, and the sentence
// under the failure said in as many words that it had.
const BOUNDARY = /[A-Za-z0-9_-]/;
function hasRoute(line, p) {
  for (let i = line.indexOf(p); i !== -1; i = line.indexOf(p, i + 1)) {
    const before = i === 0 ? "" : line[i - 1];
    const after = line[i + p.length] ?? "";
    if (!BOUNDARY.test(before) && !BOUNDARY.test(after)) return true;
  }
  return false;
}

const PHRASE = {
  proof: (v, t) => `this PR ${v} ${t} — the text this test checks as its proof of passing`,
  click: (v, t) => `this PR ${v} the string ${t} this test clicks`,
  fill: (v, t) => `this PR ${v} the string ${t} this test fills`,
  named: (v, t) => `this PR ${v} the string ${t} named in this test's failure`,
  path: (v, t) => `this PR ${v} ${t}, a path this test visited`,
};

/**
 * Score every changed file against the observed facts. Pure, so the tests can feed it synthetic
 * diffs. Returns files with evidence only, strongest first — an empty array is the correct and
 * common answer, and nothing upstream is allowed to pad it.
 */
export function scoreSuspects(facts, files) {
  const out = [];
  for (const f of Array.isArray(files) ? files : []) {
    if (!f || typeof f !== "object" || !f.file) continue;
    const removed = Array.isArray(f.removed) ? f.removed : [];
    const added = Array.isArray(f.added) ? f.added : [];
    let score = 0;
    const sentences = [];

    for (const fact of facts?.strings ?? []) {
      // Case-sensitive containment on purpose: tier one's claim is that the diff LITERALLY touches
      // the string the test interacted with, and loosening that is how "Order" in a comment
      // becomes evidence about an "Order placed" banner.
      const has = fact.kind === "path" ? hasRoute : (l, t) => l.includes(t);
      const inRemoved = removed.some((l) => has(l, fact.text));
      const inAdded = !inRemoved && added.some((l) => has(l, fact.text));
      if (!inRemoved && !inAdded) continue;
      const w = WEIGHT[fact.kind] - (inAdded ? ADDED_PENALTY : 0);
      score += w;
      sentences.push({ w, s: PHRASE[fact.kind](inRemoved ? "removed" : "added a line containing", JSON.stringify(fact.text)) });
    }

    // Tier two: the file's path names the same thing a visited route does. Once per file — a route
    // like /account/settings sharing two segments with account/settings.tsx is one relationship,
    // not two independent pieces of evidence.
    const fileSegments = new Set();
    const parts = f.file.split("/");
    for (let i = 0; i < parts.length; i++) {
      const seg = norm(i === parts.length - 1 ? parts[i].split(".")[0] : parts[i]);
      if (seg.length >= MIN_CHARS && !STOP_SEGMENTS.has(seg)) fileSegments.add(seg);
    }
    const route = (facts?.routes ?? []).find((r) => r.segments.some((seg) => fileSegments.has(seg)));
    if (route) {
      score += SEGMENT;
      sentences.push({ w: SEGMENT, s: `its path matches ${route.path}, a page this test visited` });
    }

    if (!score) continue;
    sentences.sort((a, b) => b.w - a.w);
    const evidence = sentences.length > 1
      ? `${sentences[0].s} (and ${sentences.length - 1} more matched ${sentences.length > 2 ? "facts" : "fact"})`
      : sentences[0].s;
    out.push({ file: f.file, score, evidence });
  }
  return out.sort((a, b) => b.score - a.score || a.file.localeCompare(b.file));
}

// ---- the pull request's diff, from git ----------------------------------------------------------

// One diff per (repo, base) per process: a nine-test suite with five failures asks five times, and
// the answer cannot change mid-run.
const DIFF_CACHE = new Map();

function git(args, cwd) {
  try {
    // The guards are for the repo this CLI is developed in: the whole home directory is one git
    // worktree there, and an unbounded diff against it is exactly the kind of accident a customer
    // with a monorepo can reproduce. spawnSync's default 1MB buffer truncates with an error;
    // 32MB covers a large PR, and past either limit the answer is silently "no suspects" —
    // never a slower suite or a changed verdict.
    // core.quotePath=false, or a file whose name is not pure ASCII is unblameable. Measured on a
    // repo whose PR renames the button inside src/café.jsx: git's default quoting prints
    // `"src/caf\303\251.jsx"` from --name-only and `diff --git "a/src/caf\303\251.jsx" …` in the
    // patch, so the header regex misses, the file arrives with no lines, and the one file that
    // literally removed the clicked string is never named. Silence over a real culprit is exactly
    // the miss this module exists to close.
    const r = spawnSync("git", ["-c", "core.quotePath=false", ...args], { cwd, encoding: "utf8", timeout: 4000, maxBuffer: 32 * 1024 * 1024, stdio: ["ignore", "pipe", "ignore"] });
    if (r.error || r.status !== 0) return null;
    return r.stdout ?? "";
  } catch {
    return null;
  }
}

/**
 * The changed files between the pull request and its base, or null.
 *
 * The base is GITHUB_BASE_REF when Actions provides it (tried as origin/<ref> first — actions/
 * checkout fetches the remote ref, not a local branch), and otherwise the default branch, from
 * origin/HEAD or the usual names. Three-dot on purpose: `base...HEAD` diffs from the merge-base,
 * which is "what this PR changed"; two-dot would blame this PR for every commit the base gained
 * since the branch was cut.
 *
 * Null — silently — whenever any of it is missing: no git on PATH, not a repo, a shallow CI clone
 * that never fetched the base, an empty diff. A missing diff means no suspects, and no suspects
 * means no output; it must never mean a warning, a slower run, or a different verdict.
 */
export function gitDiff({ env = process.env, cwd = process.cwd() } = {}) {
  const key = `${cwd}\u0000${env.GITHUB_BASE_REF || ""}`;
  if (DIFF_CACHE.has(key)) return DIFF_CACHE.get(key);
  const result = computeDiff(env, cwd);
  DIFF_CACHE.set(key, result);
  return result;
}

function computeDiff(env, cwd) {
  const candidates = env.GITHUB_BASE_REF
    ? [`origin/${env.GITHUB_BASE_REF}`, env.GITHUB_BASE_REF]
    : [git(["symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"], cwd)?.trim(), "origin/main", "origin/master", "main", "master"];
  const base = candidates.filter(Boolean).find((c) => git(["rev-parse", "--verify", "--quiet", `${c}^{commit}`], cwd) !== null);
  if (!base) return null;
  // -M on both calls: a rename detected in one but not the other would list a file the parsed diff
  // has no record for, or vice versa, and the two views must name the same files.
  const names = git(["diff", "--name-only", "-M", `${base}...HEAD`], cwd);
  if (!names || !names.trim()) return null;
  const parsed = parseDiff(git(["diff", "-M", "--unified=0", `${base}...HEAD`], cwd) ?? "");
  const byFile = new Map(parsed.map((f) => [f.file, f]));
  // --name-only is the authority on WHICH files changed (it lists binaries and renames the same as
  // everything else); the parsed diff supplies each one's lines when it has them.
  return names
    .split("\n")
    .filter(Boolean)
    .map((file) => byFile.get(file) || { file, oldFile: file, removed: [], added: [], binary: false, renamed: false });
}

// ---- the one entry point the suite calls --------------------------------------------------------

/**
 * At most two suspects for one failed test, each with its named evidence — or nothing.
 *
 * Two, not the full ranking: the third-best guess under a failure is where a blame list stops
 * reading as evidence and starts reading as the diff recited back. Never throws, and returns []
 * for every internal failure: this decorates a verdict that is already decided, and no bug here
 * may cost a customer their bug report.
 */
export function suspectsForFailure({ runs = [], reason = "", planPath = "", url = "", env = process.env, cwd = process.cwd(), getDiff = gitDiff } = {}) {
  try {
    let plan = null;
    if (planPath) {
      try {
        plan = JSON.parse(readFileSync(planPath, "utf8"));
      } catch {
        /* no recording is the normal case for a first failure */
      }
    }
    const facts = collectFacts({ runs, reason, plan, url });
    // No facts, no git call: a run that observed nothing has nothing to match a diff against, and
    // shelling out to answer an unanswerable question is pure cost.
    if (!facts.strings.length && !facts.routes.length) return [];
    const files = getDiff({ env, cwd });
    if (!files || !files.length) return [];
    return scoreSuspects(facts, files).slice(0, 2).map(({ file, evidence }) => ({ file, evidence }));
  } catch {
    return [];
  }
}
