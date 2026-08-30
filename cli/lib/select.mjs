// `npx smolanalytics test --suite tests/ --since main` — run only the tests this change could
// plausibly have broken.
//
// WHY THIS EXISTS. Every pull request runs the whole folder. Fifty replays is nothing; fifty AGENT
// runs, which is what a redesign's staleness cascade produces, is real money for a change that
// touched one button. Selection is the only lever that makes the price of a suite track the size of
// the change instead of the size of the folder.
//
// THE ASYMMETRY THIS WHOLE FILE IS SHAPED BY. Running a test that the change could not have broken
// costs seconds. SKIPPING a test that the change DID break ships a regression under a green tick,
// which is worse than having no selection at all — worse, in fact, than having no test. So the two
// directions are not weighed against each other. Selection removes a test only when there is
// positive evidence it is unrelated, and every single thing we do not know pushes the other way:
//
//   no recording for a test            -> RUN IT. We know nothing about what it touches.
//   a recording we cannot read         -> RUN IT.
//   a recording that names nothing     -> RUN IT.
//   no git, no such ref, no merge base -> RUN EVERYTHING, and say why.
//   a changed file we could not diff   -> RUN EVERYTHING, and say why.
//   nothing changed since the ref      -> RUN EVERYTHING, and say why. An empty diff is far more
//                                        often a wrong ref than a genuinely empty pull request.
//   this module throws                 -> RUN EVERYTHING. A bug in the optimiser may not cost
//                                        somebody their bug report.
//
// WHAT THE MAPPING IS, STATED PLAINLY SO NOBODY OVER-TRUSTS IT. lib/suspect.mjs already computes
// the facts a run observed — the control names it clicked, the text it filled, the paths it
// visited, the proof text that makes it a pass — and intersects them with git's diff. A RECORDING
// on disk carries those same facts for a test that has already passed, so the same intersection
// answers "could this change have touched this test". That is a TEXTUAL relationship, and it is
// honest about being one: a change with no text in common with a test can still break that test
// (a refactor of the total-price helper breaks checkout without ever containing the word). The
// terminal and the pull request comment both say so, in as many words, on every run that skips
// anything. `--since` is opt-in for exactly this reason, and the default is still the whole folder.
//
// AND A SKIPPED TEST IS NOT A PASSED TEST. Nothing here produces a result row. Skipped tests never
// enter `results`, so they cannot be counted as passed, cannot be rendered as a green row, cannot
// reach the exit code, and are not posted to a project or a share page. They are reported as what
// they are — tests that did not run — in both surfaces a human reads.

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { collectFacts, parseDiff, scoreSuspects } from "./suspect.mjs";

// ---- the flag ------------------------------------------------------------------------------------

/**
 * `--since <ref>`, refused rather than defaulted.
 *
 * A bare `--since` or a `--since=` cannot silently mean "no selection": the person typed the flag
 * because they wanted a smaller run, and quietly running the whole folder wastes the money they
 * were trying not to spend. A ref beginning with `-` is refused because every git call below takes
 * it as a positional revision, and `--upload-pack=…` in that position is an argument this CLI is
 * not going to hand to a subprocess. Whitespace cannot appear in a ref name at all.
 */
export function parseSince(raw) {
  if (raw === undefined) return { since: "", problem: "" };
  const v = String(raw).trim();
  if (!v) return { since: "", problem: '--since needs a ref, e.g. --since main. It runs only the tests this change could have broken; without it the whole suite runs.' };
  if (v.startsWith("-")) return { since: "", problem: `--since takes a git ref, and ${JSON.stringify(v)} starts with a dash. Pass a branch, tag or commit, e.g. --since main.` };
  if (/\s/.test(v)) return { since: "", problem: `--since takes a git ref, and ${JSON.stringify(v)} contains whitespace. Pass a branch, tag or commit, e.g. --since main.` };
  return { since: v, problem: "" };
}

// ---- what changed --------------------------------------------------------------------------------

// Past either of these the diff is not something to reason about test-by-test — a vendored
// dependency bump, a generated lockfile, a whole directory moved. Reasoning would also be slow:
// the match below is containment over every diff line, once per test. Both cases run everything,
// which is the safe direction and very often the right answer for a change that size anyway.
export const LIMITS = { maxFiles: 20_000, maxLines: 100_000 };

/** One git call. Null for every failure — no git, not a repo, a bad ref, a timeout, a huge diff. */
export function gitRun(args, cwd) {
  try {
    // core.quotePath=false for the same reason lib/suspect.mjs sets it: git otherwise octal-escapes
    // any path that is not pure ASCII, the diff header stops matching, and a changed file goes
    // unseen. Here an unseen changed file is not a missing hint, it is a test wrongly skipped.
    const r = spawnSync("git", ["-c", "core.quotePath=false", ...args], {
      cwd,
      encoding: "utf8",
      timeout: 15_000,
      maxBuffer: 32 * 1024 * 1024,
      stdio: ["ignore", "pipe", "ignore"],
    });
    if (r.error || r.status !== 0) return null;
    return r.stdout ?? "";
  } catch {
    return null;
  }
}

/**
 * Every file that differs between `since` and what is on disk right now, in lib/suspect.mjs's
 * parsed-diff shape — or null with a sentence saying why we could not tell.
 *
 * THE WORKING TREE IS PART OF THE CHANGE, and that is not a detail. `git diff <merge-base>` with no
 * second revision compares against the working tree, so a file the developer has edited and not yet
 * committed is a changed file. Diffing `base...HEAD` instead would have made every uncommitted edit
 * invisible to selection, which is the precise shape of the catastrophic error: you change a
 * button, run `--since main`, and the test for that button is skipped as unrelated.
 *
 * Untracked files are included BY PATH, with no content read. They cannot have removed anything, so
 * they cannot break a recorded test by taking away a string it clicks; the way a new file changes
 * behaviour is either through the tracked file that imports it (which is in the diff already) or by
 * being a new filesystem route, and a route is exactly what a path match sees. Not reading them
 * also means this never walks into an un-ignored node_modules and reads a gigabyte.
 */
export function changedSince({ since, cwd = process.cwd(), run = gitRun, limits = LIMITS } = {}) {
  const g = (...args) => run(args, cwd);
  if (!since) return { files: null, problem: "no base ref was given" };
  // Told apart on purpose: "there is no git here" and "this is not a repository" have different
  // fixes, and a runner that says the wrong one sends somebody looking in the wrong place.
  if (g("--version") === null) return { files: null, problem: "git is not on PATH here, so there is no diff to select from" };
  if (g("rev-parse", "--is-inside-work-tree") === null) return { files: null, problem: `${cwd} is not inside a git repository, so there is no diff to select from` };

  // origin/<ref> as well as <ref>: actions/checkout fetches remote refs, and a CI job asked for
  // `--since main` very often has origin/main and no local main at all.
  const base = [since, `origin/${since}`].find((c) => g("rev-parse", "--verify", "--quiet", `${c}^{commit}`) !== null);
  if (!base) return { files: null, problem: `the base ref ${JSON.stringify(since)} is not a commit in this repository (a shallow or single-branch clone often has not fetched it)` };
  const mb = (g("merge-base", base, "HEAD") || "").trim();
  if (!mb) return { files: null, problem: `HEAD and ${base} have no common commit, so there is no "since" to diff against (a shallow clone has no shared history)` };

  const names = g("diff", "--name-only", "-M", mb);
  if (names === null) return { files: null, problem: `git could not list what changed since ${base}` };
  const patch = g("diff", "-M", "--unified=0", mb);
  if (patch === null) return { files: null, problem: `git could not produce the diff since ${base}` };
  const others = g("ls-files", "--others", "--exclude-standard");
  if (others === null) return { files: null, problem: "git could not list the untracked files" };

  const parsed = parseDiff(patch);
  const byFile = new Map(parsed.map((f) => [f.file, f]));
  const tracked = names.split("\n").filter(Boolean);
  // THE TRIPWIRE. --name-only is the authority on which files changed; the patch supplies their
  // lines. lib/suspect.mjs fills a blank stub for any file it cannot find in the patch, because a
  // missing hint costs nothing. Here a blank stub is a file whose contents nothing can be matched
  // against, and calling every test unrelated to it would be the false skip this module exists to
  // prevent. A truncated patch is the way this actually happens, and it is silent.
  const unread = tracked.filter((f) => !byFile.has(f));
  if (unread.length) {
    return { files: null, problem: `${unread.length} changed file${unread.length === 1 ? "" : "s"} could not be read out of the diff (${unread[0]}), so nothing can be called unrelated to ${unread.length === 1 ? "it" : "them"}` };
  }

  const files = [];
  for (const name of tracked) {
    const f = byFile.get(name);
    files.push(f);
    // A rename carries a second identity. `--name-only -M` prints only the new path, and the old
    // one is what a test's route or a test's own name may still be named after.
    if (f.renamed && f.oldFile && f.oldFile !== f.file) files.push(blank(f.oldFile, true));
  }
  for (const u of others.split("\n").filter(Boolean)) files.push(blank(u, false));

  if (!files.length) return { files: null, problem: `nothing has changed since ${base}` };
  if (files.length > limits.maxFiles) return { files: null, problem: `${files.length} files differ from ${base}, too many to reason about test by test` };
  const lines = parsed.reduce((a, f) => a + f.removed.length + f.added.length, 0);
  if (lines > limits.maxLines) return { files: null, problem: `the diff against ${base} is ${lines} lines, too large to reason about test by test` };

  return { files, problem: "", base };
}

const blank = (file, renamed) => ({ file, oldFile: file, removed: [], added: [], binary: false, renamed });

// ---- what one test touches -----------------------------------------------------------------------

const defaultRead = (p) => readFileSync(p, "utf8");

/**
 * The facts a test's recording carries, or the reason we have none.
 *
 * `known: false` is never a skip. It is the answer "we do not know what this test touches", and
 * every one of them runs.
 */
export function factsFor(test, readPlan = defaultRead) {
  const p = test && test.planPath;
  if (!p) return { known: false, why: "it has no recording, so nothing is known about what it touches" };
  let raw;
  try {
    raw = readPlan(p);
  } catch (e) {
    return e && e.code === "ENOENT"
      ? { known: false, why: "it has no recording yet, so nothing is known about what it touches" }
      : { known: false, why: `its recording could not be read (${e && e.message ? e.message : e})` };
  }
  let plan;
  try {
    plan = JSON.parse(raw);
  } catch {
    return { known: false, why: "its recording is not readable JSON, so nothing is known about what it touches" };
  }
  if (!plan || typeof plan !== "object" || Array.isArray(plan)) {
    return { known: false, why: "its recording is not in the shape a recording has, so nothing is known about what it touches" };
  }
  const facts = collectFacts({ plan });
  if (!facts.strings.length && !facts.routes.length) {
    return { known: false, why: "its recording names no control, text or path, so nothing is known about what it touches" };
  }
  return { known: true, facts };
}

// ---- the second chance: a test named after a file the change touched -----------------------------

// Deliberately duplicated from lib/suspect.mjs rather than exported out of it. suspect.mjs's copies
// belong to a blame sentence a customer reads under a failure; these belong to a decision about
// whether to run something. Tying them together would mean a future tightening of blame — where
// precision is the whole point — silently deleting tests from runs, where it is the opposite.
const MIN_CHARS = 3;
const STOP = new Set([
  "src", "app", "apps", "pages", "page", "index", "public", "static", "assets",
  "components", "component", "lib", "libs", "utils", "util", "styles", "style",
  "dist", "build", "test", "tests", "api",
]);
const norm = (s) => String(s).toLowerCase().replace(/[^a-z0-9]/g, "");

/** The nameable parts of a file path: directories, and the basename without its extension. */
export function pathSegments(file) {
  const parts = String(file).split("/");
  const out = new Set();
  for (let i = 0; i < parts.length; i++) {
    const seg = norm(i === parts.length - 1 ? parts[i].split(".")[0] : parts[i]);
    if (seg.length >= MIN_CHARS && !STOP.has(seg) && !/^\d+$/.test(seg)) out.add(seg);
  }
  return out;
}

/** The nameable words in a phrase — a test's markdown file and the heading a person wrote. */
export function wordSegments(text) {
  const out = new Set();
  for (const w of String(text).split(/[^A-Za-z0-9]+/)) {
    const seg = norm(w);
    if (seg.length >= MIN_CHARS && !STOP.has(seg) && !/^\d+$/.test(seg)) out.add(seg);
  }
  return out;
}

/**
 * Does this test's OWN name or file name a changed file?
 *
 * `tests/checkout.md` and `src/checkout/Total.tsx` are the same feature under two conventions, and
 * a recording whose facts are all page text would not connect them. This only ever ADDS a test to
 * the run, so a loose match here costs seconds and buys back the most common way a real
 * relationship is invisible to string matching.
 */
export function namesake(test, files) {
  const want = wordSegments(`${(test && test.file) || ""} ${(test && test.name) || ""}`);
  if (!want.size) return null;
  for (const f of Array.isArray(files) ? files : []) {
    for (const seg of pathSegments(f && f.file)) {
      if (want.has(seg)) return { file: f.file, seg };
    }
  }
  return null;
}

// ---- the decision --------------------------------------------------------------------------------

/**
 * Split a suite into what runs and what does not.
 *
 * Returns `selected` as the very same test objects the caller discovered, so the run below it is
 * byte for byte the run it would have been, only shorter. `skipped` carries names for reporting and
 * nothing else — it never becomes a result, a row or an exit code.
 */
export function selectSuite({
  tests = [],
  since = "",
  cwd = process.cwd(),
  readPlan = defaultRead,
  run = gitRun,
  limits = LIMITS,
  changed = changedSince,
} = {}) {
  const all = { since: String(since || ""), used: false, reason: "", total: tests.length, selected: tests, picked: [], skipped: [] };
  if (!since) return all;
  try {
    const { files, problem } = changed({ since, cwd, run, limits });
    if (!files || !files.length) return { ...all, reason: problem || "the diff could not be computed" };

    const selected = [];
    const picked = [];
    const skipped = [];
    for (const t of tests) {
      const f = factsFor(t, readPlan);
      if (!f.known) {
        selected.push(t);
        picked.push({ id: t.id, name: t.name, file: t.file, why: f.why });
        continue;
      }
      const hit = scoreSuspects(f.facts, files)[0];
      if (hit) {
        selected.push(t);
        picked.push({ id: t.id, name: t.name, file: t.file, why: `${hit.file}: ${hit.evidence}` });
        continue;
      }
      const near = namesake(t, files);
      if (near) {
        selected.push(t);
        picked.push({ id: t.id, name: t.name, file: t.file, why: `${near.file}: its name shares "${near.seg}" with this test` });
        continue;
      }
      skipped.push({ id: t.id, name: t.name, file: t.file });
    }
    return { ...all, used: true, selected, picked, skipped };
  } catch (e) {
    // A bug in the optimiser costs a slower run and nothing else, ever.
    return { ...all, reason: `the diff could not be computed (${e && e.message ? e.message : e})` };
  }
}

// ---- saying it -----------------------------------------------------------------------------------

/**
 * The one sentence, in both surfaces. `tick` is the markdown backtick for the comment and empty for
 * a terminal; the words are identical either way so that nobody can be reading two different claims.
 *
 * SILENCE IS NOT AN OPTION HERE. A run that quietly checked twelve of fifty tests and printed
 * "12 passed" is a suite lying about its own coverage, and that is a worse product than a slow one.
 */
export function selectionNote(sel, tick = "") {
  if (!sel || !sel.since) return "";
  const f = `${tick}--since ${sel.since}${tick}`;
  const n = sel.total;
  if (!sel.used) return `${f} was not used: ${sel.reason}. All ${n} test${n === 1 ? "" : "s"} ran.`;
  const skipped = sel.skipped.length;
  if (!skipped) return `All ${n} test${n === 1 ? "" : "s"} ran; ${f} skipped none of them.`;
  return `${sel.selected.length} of ${n} tests ran; ${skipped} skipped because this change touches no file they exercise (${f}).`;
}

// Said wherever a skip is said. The mapping is textual and this is the sentence that stops it being
// oversold — a reader who knows the limit can decide, and one who does not will assume coverage the
// tool never claimed.
export const CAVEAT =
  "Skipped is not passed: those tests did not run and nothing about them was verified. "
  + "Selection compares each test's recording with the diff, so a change that shares no text with a test can still break it. "
  + "Run without --since to check everything.";

const dim = (s) => `\x1b[2m${s}\x1b[0m`;
const yellow = (s) => `\x1b[33m${s}\x1b[0m`;

/** What the terminal says before the first verdict: the sentence, the caveat, and every name. */
export function selectionTerminalLines(sel) {
  if (!sel || !sel.since) return [];
  const out = [sel.used && sel.skipped.length ? yellow(selectionNote(sel)) : dim(selectionNote(sel))];
  if (!sel.used || !sel.skipped.length) return out;
  out.push(dim(CAVEAT));
  for (const t of sel.skipped) out.push(dim(`  skipped ${t.name} · ${t.file}`));
  return out;
}

/** And again under the counts, for the reader who only sees the tail of a CI log. */
export function selectionTailLines(sel) {
  if (!sel || !sel.since || !sel.used || !sel.skipped.length) return [];
  return [dim(`${sel.skipped.length} of ${sel.total} tests were skipped by --since ${sel.since} — not run, not passed, not counted above.`)];
}

/** The headline fragment, beside "12 passed". Never a status: a skipped test reached no verdict. */
export function selectionHeadline(sel) {
  if (!sel || !sel.since || !sel.used || !sel.skipped.length) return "";
  return `${sel.skipped.length} skipped`;
}

// A test name is a markdown heading somebody wrote, and it is about to sit in a list inside a
// <details> block. The same two hazards as the table cell in lib/suite.mjs: `<` opens a tag, and a
// leading `-` or `#` restructures the list around it.
const cell = (s) =>
  String(s)
    .replace(/\r?\n+/g, " ")
    .replace(/</g, "&lt;")
    .replace(/([|*_`[\]\\])/g, "\\$1");

// The comment has a hard 65,536-character ceiling and lib/suite.mjs cuts on a line boundary when it
// is reached. This block sits at the TOP of the body, so an uncapped list of five hundred names
// would be cut mid-`<details>` — an unclosed tag swallows everything after it on GitHub, which
// would take the verdicts with it. The terminal names every one of them; this names a readable
// number and says how many it did not.
const MAX_LISTED = 50;

/**
 * The claim, directly under the headline: one line, always.
 *
 * ONE LINE AND NOT THE ROSTER, because this sits above the verdict table. Thirty-eight names here
 * would push the failures a reviewer came for off the first screen, and a bug report nobody scrolls
 * to is a bug report nobody reads. The names go at the bottom, in selectionCommentDetail.
 */
export function selectionCommentLines(sel) {
  if (!sel || !sel.since) return [];
  // Blank lines both sides: without the trailing one this runs into the "Against <url>" line below
  // it and markdown renders the two as one paragraph.
  return ["", selectionNote(sel, "`"), ""];
}

/** And the roster at the bottom, under the verdicts: who did not run, and what a skip does not mean. */
export function selectionCommentDetail(sel) {
  if (!sel || !sel.since || !sel.used || !sel.skipped.length) return [];
  const out = ["", `<details><summary>${sel.skipped.length} test${sel.skipped.length === 1 ? "" : "s"} that did not run</summary>`, ""];
  for (const t of sel.skipped.slice(0, MAX_LISTED)) out.push(`- ${cell(t.name)} — \`${cell(t.file)}\``);
  if (sel.skipped.length > MAX_LISTED) out.push(`- …and ${sel.skipped.length - MAX_LISTED} more, every one of them named in the run log.`);
  out.push("", "</details>", "", `<sub>${CAVEAT}</sub>`);
  return out;
}

/** Next to a `--share` link, because the page shows only what ran and the sender should know. */
export function selectionShareLines(sel, published) {
  if (!sel || !sel.since || !sel.used || !sel.skipped.length) return [];
  return [dim(`  the shared page has the ${published} test${published === 1 ? "" : "s"} that ran; the ${sel.skipped.length} skipped by --since ${sel.since} are not on it.`)];
}
