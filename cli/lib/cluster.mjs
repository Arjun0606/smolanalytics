// TWELVE RED TESTS ARE USUALLY ONE THING.
//
// A person opening a red build works out that eleven of the twelve failures are the same broken
// deploy, one is a real bug, and which to open first. That sorting is the work — research/THE_JOB.md
// scores it third of fourteen recurring QA tasks — and it is what QA Wolf and Bug0 put a human on.
// We hand over twelve well-documented failures; they hand over one report. This closes that gap.
//
// WHAT IT CLUSTERS ON, AND WHY EACH IS EVIDENCE RATHER THAN RESEMBLANCE:
//
//   A SHARED SUSPECT FILE. lib/suspect.mjs names changed files with the evidence tying each to a
//   test, and refuses to name one it cannot connect. Two failures independently blaming the same
//   file, each for its own reason, is the strongest thing we hold: neither computed the other.
//
//   A SHARED CONTROL. Two recordings that both drive "Proceed to checkout" are two tests that
//   depend on one button. Taken from the RECORDING — a real `click`/`fill` step with a real
//   accessible name — not from anybody's description of the failure.
//
//   A SHARED PATH. Same, one level weaker: both recordings visit /checkout.
//
// WHAT IT REFUSES TO CLUSTER ON. The failure prose. `reason` on a failed run is written by the
// model, and two failures that both say "the page showed an error" are not evidence of a shared
// cause — that is the resemblance that reads as insight and quietly files a real second bug inside
// a group somebody dismissed as "all one thing". collectFacts() mines the prose too, for a purpose
// where a weak fact still helps; every prose-derived fact is dropped on the floor here.
//
// A GROUP IS NAMED BY THE THING ITS MEMBERS SHARE, never by a member. Grouping "everything that
// overlaps the first failure" would make the answer depend on the order tests happened to run:
// A and B share one file, B and C share another, and whether C joins depends on where you started.
// So each pass takes the ONE file, control or path that explains the most failures, emits exactly
// the failures that carry it, and looks again. Deterministic, and every member genuinely shares the
// named cause rather than sharing a neighbour.
//
// NOTHING HERE MAY TOUCH A VERDICT. Same rule as lib/suspect.mjs and lib/flake.mjs: this decorates
// a report. No status, no exit code, and every failure of ours — an unreadable recording, no
// recording at all — degrades to "these did not group", which is a true and unembarrassing answer.

import { readFileSync } from "node:fs";
import { collectFacts } from "./suspect.mjs";

const defaultRead = (p) => readFileSync(p, "utf8");

/** Fewer than this many failures and there is nothing to triage — the list IS the summary. */
const WORTH_GROUPING = 2;

/**
 * What a failure's recording touches: the controls it drives, the paths it visits.
 *
 * Recording only. collectFacts() accepts `runs` and `reason` as well and weighs prose-derived
 * facts lower for blame; here they are not passed at all, so no sentence below can rest on one.
 */
export function touched(result, readPlan = defaultRead) {
  const p = result && result.planPath;
  if (!p) return { controls: [], paths: [] };
  let plan;
  try {
    plan = JSON.parse(readPlan(p));
  } catch {
    // No recording, an unreadable one, or one that is not JSON. A test we know nothing about
    // groups with nothing, which is exactly right — it is not evidence of independence either.
    return { controls: [], paths: [] };
  }
  if (!plan || typeof plan !== "object" || Array.isArray(plan)) return { controls: [], paths: [] };
  let facts;
  try {
    facts = collectFacts({ plan });
  } catch {
    return { controls: [], paths: [] };
  }
  // collectFacts returns `strings` as an ARRAY of {text, kind} — it builds a Map internally and
  // spreads its values on the way out. Reading it as a Map here silently produced no controls at
  // all, and because "no controls" is a legitimate answer for a test with no recording, nothing
  // downstream could tell the difference: the control signal was simply dead. The test that drives
  // two recordings through one button is what noticed. Read it as what it is, and treat a shape
  // that is neither as no facts rather than as a crash.
  const controls = [];
  for (const v of Array.isArray(facts.strings) ? facts.strings : []) {
    // `click` and `fill` only. `proof` is the page's own text, `path` is a route, and `named` is
    // the failure prose — the one this module will not group on.
    if (v && (v.kind === "click" || v.kind === "fill") && v.text) controls.push(v.text);
  }
  const paths = (Array.isArray(facts.routes) ? facts.routes : []).map((r) => r && r.path).filter(Boolean);
  return { controls, paths };
}

/** Changed files this failure blamed, identity only — the evidence rides on the suspect line. */
const suspectFiles = (r) =>
  (Array.isArray(r && r.suspects) ? r.suspects : [])
    .map((s) => String((s && s.file) || "").trim())
    .filter(Boolean);

/**
 * Take the key that explains the most of the pool, emit its members, repeat.
 *
 * Ties break on the key itself rather than on position, so two files that each explain four
 * failures always come out in the same order no matter what order the suite ran in.
 */
function groupBy(pool, keysOf, describe, signal, out) {
  for (;;) {
    const holders = new Map();
    for (const r of pool) {
      for (const k of new Set(keysOf(r))) {
        if (!holders.has(k)) holders.set(k, []);
        holders.get(k).push(r);
      }
    }
    let best = null;
    for (const [k, members] of holders) {
      if (members.length < WORTH_GROUPING) continue;
      if (!best || members.length > best.members.length || (members.length === best.members.length && k < best.key)) {
        best = { key: k, members };
      }
    }
    if (!best) return;
    out.push({ ...describe(best.key, best.members), tests: best.members.map((r) => r.name), signal });
    for (const m of best.members) {
      const at = pool.indexOf(m);
      if (at >= 0) pool.splice(at, 1);
    }
  }
}

/**
 * The failures of a run, grouped by shared cause, strongest evidence first.
 *
 * Strongest first matters: a shared suspect file is independent per-test evidence, and grouping on
 * the weaker signals first would sweep those failures into a looser bucket and leave nothing to
 * separate them with afterwards.
 */
export function cluster(results = [], { readPlan = defaultRead } = {}) {
  const failed = (Array.isArray(results) ? results : []).filter((r) => r && r.status === "failed" && r.name);
  const out = [];
  if (failed.length < WORTH_GROUPING) {
    return failed.map((r) => ({ cause: r.name, why: "", tests: [r.name], signal: "alone" }));
  }

  // Read each recording once. Twelve failures on a big suite is twelve small files, and doing it
  // inside the grouping loop would re-read them on every pass.
  const facts = new Map(failed.map((r) => [r, touched(r, readPlan)]));
  const pool = [...failed];

  // `why` is PLAIN PROSE and names no markdown, here and below. The cause is a path out of the
  // customer's diff or a control label off their page, and lib/suite.mjs already has the rule for
  // both: a diff path goes through code(), which fences around backticks the path itself contains,
  // and their strings go through quote(). A sentence with the cause already baked between backticks
  // would hand a file named with one a way out of its own code span.
  groupBy(
    pool,
    suspectFiles,
    (file, members) => ({
      cause: file,
      why: `${members.length} failures each independently blamed this file. That is one change, not ${members.length} bugs — start there.`,
    }),
    "suspect",
    out,
  );

  groupBy(
    pool,
    (r) => facts.get(r).controls,
    (control, members) => ({
      cause: control,
      why: `${members.length} failures all drive this one control. If it is broken, that explains all ${members.length}.`,
    }),
    "control",
    out,
  );

  groupBy(
    pool,
    (r) => facts.get(r).paths,
    (path, members) => ({
      cause: path,
      why: `${members.length} failures all go through this page. One page, most likely one cause.`,
    }),
    "path",
    out,
  );

  // Whatever is left shares nothing with anything, and says so by standing alone. Hiding a failure
  // inside a group it does not belong to is the one outcome worse than not grouping at all.
  for (const r of pool) out.push({ cause: r.name, why: "", tests: [r.name], signal: "alone" });

  // Biggest first: the group that explains eleven failures is the one to read.
  return out.sort((a, b) => b.tests.length - a.tests.length || (a.cause < b.cause ? -1 : 1));
}

/** The groups worth showing: a group of one is a failure standing on its own, not a finding. */
export const grouped = (groups = []) => groups.filter((g) => g && g.tests && g.tests.length > 1);

/**
 * The failures that grouped with nothing — named, because naming them IS the second half of the job.
 *
 * MEASURED, rendering a realistic red build: eleven failures from one changed file and one genuine
 * second bug. The summary said "11 group into 1 cause, and 1 stands alone" and then made the reader
 * scan twelve blockquotes to find which one. The whole promise is "it told me all eleven were one
 * thing" — and the corollary a person actually acts on is that the twelfth was NOT, because that is
 * the one a fix for the deploy will not turn green and the one most likely to be a real bug.
 *
 * Only when something else DID group. Twelve failures that all stand alone is a list, not a finding.
 */
export const standalone = (groups = []) =>
  (grouped(groups).length ? groups.filter((g) => g && g.tests && g.tests.length === 1) : []).map((g) => g.tests[0]);

/** Past this many, naming them stops being a shortlist and becomes the list again. */
const NAME_AT_MOST = 5;

/** "A shopper can search — on its own", or "" when nothing stood apart from a group. */
export function aloneNote(groups = []) {
  const names = standalone(groups);
  if (!names.length) return "";
  const shown = names.slice(0, NAME_AT_MOST);
  const rest = names.length - shown.length;
  return (
    `${shown.join(", ")}${rest ? ` and ${rest} more` : ""} shared nothing with the rest. ` +
    `A fix for the cause above will not turn ${names.length === 1 ? "it" : "them"} green.`
  );
}

/**
 * The sentence above a wall of red, or "" when nothing grouped.
 *
 * Lives here rather than in either renderer so the terminal and the pull request cannot drift into
 * describing one red build two different ways. Silent when nothing grouped: "12 failures, 12 causes"
 * is what the list underneath already shows, and a header that only restates the list is one people
 * learn to scroll past — taking the failure that mattered with them.
 */
export function clusterHead(groups = []) {
  const real = grouped(groups);
  if (!real.length) return "";
  const covered = real.reduce((n, g) => n + g.tests.length, 0);
  const alone = groups.length - real.length;
  if (!alone && real.length === 1) return `All ${covered} failures look like one thing.`;
  return (
    `${covered} of these failures group into ${real.length} likely cause${real.length === 1 ? "" : "s"}` +
    `${alone ? `, and ${alone} ${alone === 1 ? "stands" : "stand"} alone` : ""}.`
  );
}

/** The whole note, plain text, for the terminal. "" when there is nothing to say. */
export function clusterNote(results = [], opts = {}) {
  const groups = cluster(results, opts);
  const head = clusterHead(groups);
  if (!head) return "";
  const apart = aloneNote(groups);
  return [head, ...grouped(groups).map((g) => `  ${g.cause} — ${g.why}`), ...(apart ? [`  ${apart}`] : [])].join("\n");
}
