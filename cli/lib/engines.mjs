// WHICH BROWSER ENGINE THE TEST RUNS IN — `--browser chromium|firefox|webkit`.
//
// WHY THIS EXISTS. The whole reason to drive a real browser rather than a jsdom is that real
// browsers disagree with each other. A date input that WebKit renders differently, a flex gap
// Firefox lays out differently, a `:has()` selector one engine shipped a year later than the
// others — those are the bugs an end-to-end test is uniquely able to catch, and a runner that only
// ever launches Chromium cannot catch a single one of them. Playwright already ships all three
// engines and drives them through the same API; this file is the twenty lines that stop us
// hardcoding one.
//
// THE PART THAT IS NOT MECHANICAL: A RECORDING IS MADE AGAINST AN ENGINE.
//
// `npx smolanalytics test --plan` records a walk once with the agent and replays it for free
// afterwards. The walk was discovered by an agent looking at ONE engine's rendering of the page,
// and its proof text was read off ONE engine's layout. Replay it on a different engine and there
// are three honest options:
//
//   refuse it and re-run the agent   correct, and ruinous. A suite of fifty tests run on three
//                                    engines becomes 150 full-price agent runs on every pull
//                                    request instead of 50 — this codebase has already been
//                                    through one cost explosion of exactly that shape (see
//                                    rebase() in lib/test.mjs) and it silently inverted the value
//                                    of recording anything.
//   replay it and say nothing        the cheap option, and the dishonest one: a green check whose
//                                    provenance is a run that never touched this engine.
//   replay it and SAY SO             what this does.
//
// The steps are locators — a role and an accessible name — and the proof is text on the page.
// Neither is engine-specific by construction, so replaying across engines is a real check: if the
// WebKit build of the app cannot reach the confirmation, the replay goes stale or the proof stops
// matching, and the agent is woken to judge it. That is exactly the WebKit-only bug cross-browser
// testing exists to find, and it is found for the price of a replay.
//
// So: the engine is stamped into the recording, the verdict is UNCHANGED by a mismatch — no status
// this file touches, no exit code — and both the terminal output and the reason posted to the
// project name the two engines. A note, never a verdict.

export const ENGINES = ["chromium", "firefox", "webkit"];
export const DEFAULT_ENGINE = "chromium";

/** Names as the vendors write them, for prose. */
export const ENGINE_LABEL = { chromium: "Chromium", firefox: "Firefox", webkit: "WebKit" };

/**
 * `--browser <name>`, refused out loud rather than silently defaulted.
 *
 * The same shape as --retries and --layout in bin/smolanalytics.mjs, for the same reason: a typo'd
 * `--browser webkti` that quietly ran Chromium would report a green suite to the one person who
 * explicitly asked to be told about WebKit. Case is forgiven — "WebKit" is how it is spelled
 * everywhere else — but nothing else is.
 */
export function parseEngine(raw) {
  if (raw === undefined) return { engine: DEFAULT_ENGINE, problem: "" };
  const v = String(raw).trim().toLowerCase();
  if (ENGINES.includes(v)) return { engine: v, problem: "" };
  return {
    engine: "",
    problem:
      `--browser must be chromium, firefox or webkit, got ${JSON.stringify(raw)}. ` +
      `chromium is the default; firefox and webkit are the same test in a different engine, and each needs its own one-off download.`,
  };
}

/** The one command that fixes a missing engine. Named exactly, never "run playwright install". */
export function installCommand(engine) {
  return `npx playwright install ${ENGINES.includes(engine) ? engine : "chromium"}`;
}

// Playwright's own message for a browser that was never downloaded is a twelve-line box drawn in
// Unicode with "npx playwright install" (no engine named) inside it, wrapped in a stack trace. It
// is the single most common first-run failure and it reads like our crash.
const NOT_DOWNLOADED = /executable doesn't exist|please run the following command|browsertype\.launch: executable/i;

/**
 * Our sentence for a launch failure, or "" when the failure is something else entirely (a missing
 * system library, a sandbox, an out-of-memory) which must keep its own message.
 */
export function launchProblem(engine, err) {
  const msg = String(err && err.message ? err.message : err);
  if (!NOT_DOWNLOADED.test(msg)) return "";
  const label = ENGINE_LABEL[engine] || engine;
  return `${label} is not installed, so nothing was tested. Install it with: ${installCommand(engine)}`;
}

/**
 * Launch one engine.
 *
 * Two failures get a sentence instead of a stack: an engine this Playwright build does not expose
 * at all, and an engine that is exposed but was never downloaded. Everything else — the missing
 * shared libraries on a bare CI image, a sandbox refusal — keeps Playwright's own words, which are
 * the useful ones there.
 */
export async function launchEngine(pw, engine, opts = {}) {
  const name = ENGINES.includes(engine) ? engine : DEFAULT_ENGINE;
  const type = pw && pw[name];
  if (!type || typeof type.launch !== "function") {
    throw new Error(
      `this Playwright build does not expose a ${ENGINE_LABEL[name] || name} browser, so --browser ${name} cannot run. Upgrade Playwright, or drop the flag to use Chromium.`,
    );
  }
  try {
    return await type.launch(opts);
  } catch (e) {
    const problem = launchProblem(name, e);
    if (!problem) throw e;
    const clean = new Error(problem);
    clean.cause = e;
    throw clean;
  }
}

// ---- the engine a recording was made on -------------------------------------------------------

/**
 * Stamp the engine onto a compiled plan.
 *
 * EVERY engine, including the default. Stamping only the unusual ones would make a recording made
 * on Chromium byte-identical to one made before this feature existed, and then replaying it on
 * WebKit could say nothing — which is the silence this whole file is here to remove. Recordings
 * written before today have no `engine` and stay silent, because "we do not know" and "it was the
 * same engine" are different facts and only one of them is true.
 *
 * null in, null out: compile() returns null for a recording that must not be written, and that
 * refusal outranks anything here.
 */
export function withEngine(plan, engine) {
  if (!plan) return plan;
  const e = String(engine || "").trim().toLowerCase();
  return ENGINES.includes(e) ? { ...plan, engine: e } : plan;
}

/**
 * The engine named by a recording, or "" for one that names none.
 *
 * A recording is untrusted input (see readPlan). Anything that is not a non-empty string is read
 * as "not stated" — this value only ever reaches prose, never a locator or a verdict, so a
 * hand-edited engine field can at worst produce a sentence, and it is quoted verbatim below so
 * that sentence is still true.
 */
export function recordedEngine(plan) {
  const e = plan && plan.engine;
  return typeof e === "string" && e.trim() ? e.trim().toLowerCase() : "";
}

/**
 * What to say when a recording made on one engine is replayed on another. "" when there is nothing
 * to say — no engine recorded, or the same engine — which is every run of the default setup.
 *
 * `when` shapes the second sentence and nothing else:
 *   "passed"  the flow was checked against THIS engine, which is the good news worth saying.
 *   "stale"   the engine change is a candidate cause of the staleness, and naming it is the
 *             difference between "someone renamed a button" and "this app is broken on WebKit".
 */
export function engineNote(recorded, current, when = "passed") {
  const r = String(recorded || "").trim().toLowerCase();
  const c = String(current || "").trim().toLowerCase() || DEFAULT_ENGINE;
  if (!r || r === c) return "";
  const R = ENGINE_LABEL[r] || JSON.stringify(r);
  const C = ENGINE_LABEL[c] || JSON.stringify(c);
  const head = `This recording was made on ${R} and was replayed on ${C}.`;
  return when === "stale"
    ? `${head} An engine difference is one candidate cause: the recorded walk may still work on ${R} and not here, which is the kind of break running a second browser exists to find.`
    : `${head} The steps and the proof were checked against ${C} this time, so a ${C}-only break in this flow would have shown up here.`;
}

/** Append a note to a reason without producing a double space or a dangling one. */
export function withNote(reason, note) {
  const r = String(reason || "").trim();
  const n = String(note || "").trim();
  if (!n) return r;
  return r ? `${r} ${n}` : n;
}
