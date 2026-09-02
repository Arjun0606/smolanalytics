// `npx smolanalytics test` — one sentence, a real browser, a verdict. No account.
//
// WHY THIS COMMAND EXISTS, AND WHY IT LOOKS LIKE THIS.
//
// The competing product's onboarding, timed on a real repo: install a GitHub App across every
// repository, let an agent cut a branch and push a Dockerfile into your code, answer which of
// FRED_API_KEY / BRAVE_SEARCH_API_KEY / AWS_ACCESS_KEY_ID you want set, hand over SUPABASE_URL and
// OPENAI_API_KEY, wait while web + backend + Postgres are built from scratch, and then watch a
// planner say "ETA ~1h 13m". Twenty-eight minutes in it was at 3%, then 0%, then step 1 of 7
// failed. That is not a rough edge to out-polish; it is the shape of insisting on BUILDING an
// isolated environment before you are allowed to see anything work.
//
// You already have a URL. Vercel, Netlify, Fly and Render hand you a preview per pull request, and
// staging has existed for thirty years. So this command asks for a URL and a sentence, and gives a
// verdict in under a minute:
//
//   npx smolanalytics test --url https://yourapp.com --test "the pricing page shows a monthly price"
//
// No account. No GitHub App. No preview build. Nothing written to your repository. If it is useful
// you can connect a project later and the same command starts recording its verdicts.
//
// THE ONE DEPENDENCY, AND WHY IT IS LAZY. Driving a real browser needs Playwright, which is tens of
// megabytes. Every other command in this CLI has zero dependencies on purpose — the whole value of
// `npx smolanalytics audit` is that it answers before you have finished reading the command. So
// Playwright is fetched the first time THIS command runs, with a sentence saying what and why, and
// never for anyone who does not use it.

import { spawnSync } from "node:child_process";
import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { confirmProduction, keyProblem, newIdentity, postTeardown, substitute, maskSecrets, unmaskSecrets, PLACEHOLDER_LIST } from "./safety.mjs";
import { newLedger, record as recordUsage, costLine, overBudget, priceFrom, priceHint } from "./cost.mjs";
import { flakeNote } from "./flake.mjs";
import { auditLayout, layoutFailure, layoutNoteLines, stepTargets } from "./layout.mjs";
import { auditRender, renderFailure, renderNoteLines } from "./render.mjs";
import { DEFAULT_AUTH_DIR, openSession } from "./auth.mjs";
import { closedShadowRoots, embeddedNotes, frameLabel, inFrame, readFrames, visibleText } from "./frames.mjs";
import { DEFAULT_ENGINE, ENGINE_LABEL, engineNote, launchEngine, recordedEngine, withEngine, withNote } from "./engines.mjs";
import { UPLOAD_TOOL, performUpload, uploadLabel, uploadNotes, uploadTargets } from "./upload.mjs";
import { seedRun } from "./seed.mjs";
import { maskUrl, resolveUrl } from "./seedguard.mjs";
import { agentSteps, publishShare, replaySteps, scrubDeep } from "./share.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
  r: (s) => `\x1b[31m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

// ---- perception: read the page, do not squint at it -------------------------------------------
//
// The obvious design is to screenshot the page and ask a vision model where the button is. It is a
// good demo and it is the weakest part of the competing product: two model calls and an image per
// step, and a coordinate is a guess about a raster that can click the wrong thing and then blame
// the wrong feature.
//
// Playwright's ariaSnapshot() returns the roles, names, values and states of everything on the
// page, as text, in the format Playwright designed for models. The agent picks an ELEMENT, and the
// click that follows is a real locator with actionability checks.

const ACTIONABLE = new Set([
  "button", "link", "textbox", "searchbox", "combobox", "checkbox", "radio", "switch",
  "slider", "spinbutton", "menuitem", "menuitemcheckbox", "menuitemradio", "option", "tab",
  "heading", "alert", "status", "dialog",
]);

/** One line of ariaSnapshot output: `- button "Sign in" [disabled]` or `- textbox "Email": a@b.com` */
const LINE = /^\s*-\s+([a-z]+)\s+"((?:[^"\\]|\\.)*)"(?:\s+\[([^\]]*)\])?(?::\s*(.*))?$/;
const MEANINGFUL_STATE = /^(disabled|checked|expanded|selected|pressed|readonly|mixed)/;

export function flatten(snapshot, cap = 120) {
  const out = [];
  let seen = 0;
  for (const raw of String(snapshot).split("\n")) {
    const m = LINE.exec(raw);
    if (!m) continue;
    const [, role = "", rawName = "", flags, value] = m;
    if (!ACTIONABLE.has(role)) continue;
    const name = rawName.replace(/\\"/g, '"').trim();
    if (!name) continue;
    seen++;
    if (out.length >= cap) continue;
    const el = { ref: `e${out.length + 1}`, role, name: name.slice(0, 120) };
    if (flags) {
      const states = flags.split(/\s+/).filter((f) => MEANINGFUL_STATE.test(f)).join(",");
      if (states) el.state = states;
    }
    const v = (value ?? "").trim();
    if (v) el.value = v.slice(0, 80);
    out.push(el);
  }
  // Truncation is REPORTED, never silent: a model that cannot see the element it needs should be
  // told to scroll, not left to conclude the element does not exist and fail a working app.
  return { elements: out, truncated: Math.max(0, seen - out.length) };
}

/**
 * Read the page.
 *
 * `page.locator("body").ariaSnapshot()`, not `page.ariaSnapshot()`. The page-level method only
 * arrived in a recent Playwright; the locator form has existed since 1.49 and returns byte-identical
 * output. Customers do not all install the newest version, and "page.ariaSnapshot is not a function"
 * is a crash rather than a graceful degradation — caught by running the browser tests against a
 * 1.52 that happened to be on this machine.
 */
export async function perceive(page) {
  const [aria, title, text] = await Promise.all([
    page.locator("body").ariaSnapshot().catch(() => ""),
    page.title().catch(() => ""),
    visibleText(page).catch(() => ""),
  ]);
  const { elements, truncated } = flatten(aria);
  // EMBEDDED CONTENT. `ariaSnapshot()` renders a whole payment form as the single word "iframe",
  // which flatten() then drops for having no name — so before this, the agent was shown a checkout
  // page with two buttons on it and no hint that a third document existed. See lib/frames.mjs for
  // the measurement. Never allowed to throw: perception is how the agent sees.
  const f = await readFrames(page, flatten, { startRef: elements.length }).catch(() => null);
  const closed = await closedShadowRoots(page).catch(() => []);
  // FILE UPLOADS (lib/upload.mjs). A file input's role IS `button`, so the list above cannot tell
  // one apart from a Save button — and clicking it is a silent no-op. [] on every page without
  // one, which is almost all of them. Never allowed to throw: perception is how the agent sees.
  const uploads = await uploadTargets(page).catch(() => []);
  return {
    url: page.url(),
    title,
    elements: f ? [...elements, ...f.elements] : elements,
    truncated: truncated + (f ? f.truncated : 0),
    frames: f ? f.frames : [],
    closed,
    uploads,
    text: String(text).replace(/\n{3,}/g, "\n\n").trim().slice(0, 4000),
  };
}

export function render(s) {
  const lines = s.elements.map((e) => {
    const bits = [e.ref, e.role, JSON.stringify(e.name)];
    if (e.value) bits.push(`value=${JSON.stringify(e.value)}`);
    if (e.state) bits.push(`[${e.state}]`);
    // Which document it is in, so the model's own reasoning and the step label it produces both
    // say "inside the payment frame" rather than implying it was on the page.
    if (e.frameName || (e.frame && e.frame.length)) bits.push(`in frame ${JSON.stringify(e.frameName || frameLabel(e.frame))}`);
    return "  " + bits.join(" ");
  });
  return [
    `URL: ${s.url}`,
    `TITLE: ${s.title}`,
    "",
    "ELEMENTS (act on these by ref):",
    ...(lines.length ? lines : ["  (none found — the page may still be loading, or its content is in a canvas)"]),
    ...(s.truncated ? [`  … and ${s.truncated} more not shown. Scroll or filter rather than assuming an element is absent.`] : []),
    "",
    // Empty on a page with no frames and no closed shadow roots, which is most of them — so this
    // adds not one character to the ordinary case.
    ...embeddedNotes({ frames: s.frames, closed: s.closed }),
    // Same contract as embeddedNotes: empty on a page with no file inputs.
    ...uploadNotes(s.uploads),
    "PAGE TEXT:",
    s.text || "(empty)",
  ].join("\n");
}

function locate(page, snap, ref) {
  const el = snap.elements.find((e) => e.ref === ref);
  if (!el) return null;
  return inFrame(page, el.frame).getByRole(el.role, { name: el.name, exact: true });
}

// ---- the agent --------------------------------------------------------------------------------

const TOOLS = [
  { name: "click", description: "Click one element from the ELEMENTS list.",
    input_schema: { type: "object", properties: { ref: { type: "string" }, why: { type: "string" } }, required: ["ref", "why"], additionalProperties: false } },
  { name: "fill", description: "Type text into a textbox, searchbox or combobox. Replaces what is there.",
    input_schema: { type: "object", properties: { ref: { type: "string" }, text: { type: "string" }, why: { type: "string" } }, required: ["ref", "text", "why"], additionalProperties: false } },
  // Attaching a file, with no file on anybody's disk (lib/upload.mjs).
  UPLOAD_TOOL,
  { name: "press", description: "Press a key, e.g. Enter. Use when a form submits on Enter.",
    input_schema: { type: "object", properties: { key: { type: "string" }, why: { type: "string" } }, required: ["key", "why"], additionalProperties: false } },
  { name: "goto", description: "Navigate to a URL. Prefer clicking; use this only when the test says to open a page.",
    input_schema: { type: "object", properties: { url: { type: "string" }, why: { type: "string" } }, required: ["url", "why"], additionalProperties: false } },
  { name: "scroll", description: "Scroll when what you need is probably above or below the fold.",
    input_schema: { type: "object", properties: { direction: { type: "string", enum: ["up", "down"] }, why: { type: "string" } }, required: ["direction", "why"], additionalProperties: false } },
  { name: "finish",
    description:
      "End the test. passed=true only if you directly OBSERVED what the test asked you to verify. " +
      "On a pass you MUST supply `proof`: a short, distinctive run of text that was visible on the " +
      "page and that would NOT be there if the test had failed. It is checked verbatim on later " +
      "runs, so quote it exactly and keep it free of anything that changes run to run — an order " +
      "number, a date, a total, a name.",
    input_schema: { type: "object", properties: {
      passed: { type: "boolean" },
      why: { type: "string" },
      proof: { type: "string", description: "Exact page text proving the pass. Required when passed is true." },
    }, required: ["passed", "why", "proof"], additionalProperties: false } },
];

const SYSTEM = `You are testing a web application by using it, the way a careful person would.

You are given a test written in plain English. Carry it out in the browser, then say whether it passed.

HOW YOU SEE THE PAGE
Each turn you get the URL, the page's interactive elements, and its visible text. Act on elements by
their ref (e1, e2, …). You never write CSS selectors and cannot act on anything not in the list. If
what you need is not listed, the page may still be loading or the element may be below the fold —
scroll once before concluding it is absent.

HOW TO DECIDE
- Do only what the test describes. Do not explore or verify things it did not ask about.
- Pass ONLY if you directly observed the thing the test asked you to verify. Not "the click worked
  so presumably it saved" — you must see the evidence on the page.
- FAIL when the application does not offer the path the test describes. Do not find a clever way
  around a broken control: routing around the defect is how a broken sign-up ships green.
- Be economical. Every step costs money and time.

WRITING THE PROOF
When you pass, the proof field is the sentence a later run checks WITHOUT a model. Pick text that is on the
page only because the thing worked: "Order placed", "Welcome back", "Discount applied". Do not pick
a heading that is on the page whether or not the test passed, and do not include anything that
changes between runs — an order number, a total, a timestamp, a username. If you cannot name such a
text, you did not actually observe the outcome, and the honest answer is passed=false.

WRITING THE FAILURE
Your failure text is read by someone who was not watching. Name the page, the control, what you
expected and what actually happened. "Checkout failed" is useless. "On /cart, clicking Proceed to
checkout stayed on /cart and showed no error; the page still lists 2 items" is a bug report.`;

/** Call Claude with plain fetch — no SDK, so this command adds no dependency beyond the browser. */
async function think(messages, apiKey, model) {
  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: { "content-type": "application/json", "x-api-key": apiKey, "anthropic-version": "2023-06-01" },
    body: JSON.stringify({ model, max_tokens: 4000, system: SYSTEM, tools: TOOLS, messages }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`the model call failed (${res.status}). ${body.slice(0, 200)}`);
  }
  return res.json();
}

async function act(page, snap, a) {
  try {
    if (a.kind === "click" || a.kind === "fill") {
      const l = locate(page, snap, a.ref);
      if (!l) return { ok: false, detail: `no element ${a.ref} on the page you were shown` };
      if (a.kind === "click") await l.click({ timeout: 10_000 });
      else await l.fill(a.text, { timeout: 10_000 });
    } else if (a.kind === "upload") {
      const l = locate(page, snap, a.ref);
      if (!l) return { ok: false, detail: `no element ${a.ref} on the page you were shown` };
      // The file is fabricated from the control's own `accept` and the note names it — in the
      // terminal AND in what the agent is told next, because "which file did it attach" is the
      // first question anybody asks about an upload that was rejected.
      const up = await performUpload(page, l);
      if (!up.ok) return up;
      await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});
      return { ok: true, note: up.note };
    } else if (a.kind === "press") await page.keyboard.press(a.key);
    else if (a.kind === "goto") await page.goto(a.url, { waitUntil: "domcontentloaded", timeout: 30_000 });
    else if (a.kind === "scroll") await page.mouse.wheel(0, a.direction === "down" ? 700 : -700);
    // Never fail a step because the network stayed busy: analytics beacons and long-polling keep
    // many real apps from ever reaching networkidle.
    await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});
    return { ok: true };
  } catch (e) {
    return { ok: false, detail: String(e && e.message ? e.message : e).split("\n")[0] };
  }
}

function toAction(call) {
  const i = call.input || {};
  switch (call.name) {
    case "click": return { kind: "click", ref: String(i.ref), why: String(i.why) };
    case "fill": return { kind: "fill", ref: String(i.ref), text: String(i.text), why: String(i.why) };
    case "upload": return { kind: "upload", ref: String(i.ref), why: String(i.why) };
    case "press": return { kind: "press", key: String(i.key), why: String(i.why) };
    case "goto": return { kind: "goto", url: String(i.url), why: String(i.why) };
    case "scroll": return { kind: "scroll", direction: i.direction === "up" ? "up" : "down", why: String(i.why) };
    case "finish": return { kind: "finish", passed: Boolean(i.passed), why: String(i.why), proof: String(i.proof || "") };
    default: return { kind: "finish", passed: false, why: `unknown tool ${call.name}` };
  }
}

function describe(step, secrets = []) {
  const a = step.action;
  const mark = step.ok ? C.g("✓") : C.r("✗");
  // A click inside an embedded checkout that reads as a click on the page sends whoever debugs it
  // to the wrong document. The frame is part of what happened, so it is part of the label.
  const inF = step.target && step.target.frame && step.target.frame.length ? ` in frame "${frameLabel(step.target.frame)}"` : "";
  const label =
    a.kind === "click" ? `click ${step.target ? `${step.target.role} "${step.target.name}"${inF}` : a.ref}`
    : a.kind === "fill" ? `fill ${step.target ? `"${step.target.name}"${inF}` : a.ref} = ${JSON.stringify(maskSecrets(a.text, secrets))}`
    // WHICH FILE WAS MADE, on the step line itself. An upload whose fixture nobody can name is an
    // upload nobody can debug when the app rejects it.
    : a.kind === "upload" ? `upload to ${step.target ? `"${step.target.name}"${inF}` : a.ref}${step.note ? ` — ${step.note}` : ""}`
    // MASKED, like the fill above it. A sentence that says "open order {{orderId}}" makes the
    // agent navigate rather than type, and this line is the terminal, the step label, the row
    // posted to a project and the pull request comment. lib/seedguard.mjs.
    : a.kind === "goto" ? `goto ${maskUrl(a.url, secrets)}`
    : a.kind === "press" ? `press ${a.key}`
    : `scroll ${a.direction}`;
  return `  ${mark} ${String(step.n).padStart(2)} ${label}${step.ok ? C.dim(` ${step.ms}ms`) : C.r(` — ${step.detail}`)}`;
}

/**
 * WHERE THE KEY GOES, ADDRESSED TO THE READER WE CAN ACTUALLY IDENTIFY.
 *
 * "export ANTHROPIC_API_KEY=… then run this again" is the right sentence at a keyboard and the
 * wrong one in the place this product mostly runs. MEASURED, walking the shipped workflow as a
 * stranger who skipped step 1 of its own instructions: every test in the suite came back with that
 * line, inside a GitHub Actions job where nothing can be exported, where "run this again" re-runs a
 * job that fails identically, and where the two things that would actually fix it — the secrets
 * page, and the `env:` line on the step — were named nowhere at all. An unactionable error repeated
 * once per test is how the first minute of CI goes badly.
 *
 * Both sentences live here rather than at their call sites so the terminal and the pull request
 * comment cannot drift into saying different things about the same missing key.
 */
export function keyFix(env = process.env) {
  return env.GITHUB_ACTIONS === "true"
    ? "Add ANTHROPIC_API_KEY under Settings → Secrets and variables → Actions, then pass it to this step: env: ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}"
    : "export ANTHROPIC_API_KEY=sk-ant-…    then run this again";
}

/**
 * WHERE TO GET ONE, which the fix above assumes you already know.
 *
 * Measured by installing the published package and running the homepage's own command as somebody
 * who had never seen this before: the output said to export a key and did not say where a key comes
 * from. Anyone who has used the Anthropic API fills that gap in a second; somebody who has not is
 * stopped by it, and that is exactly the person the first run has to carry.
 *
 * Empty inside Actions. There the reader is wiring a secret they already hold, and a link to a
 * signup page in a CI log is noise in the one place nobody can act on it.
 */
export function keyWhere(env = process.env) {
  return env.GITHUB_ACTIONS === "true" ? "" : "A key comes from console.anthropic.com — the calls are billed to you, and never resold through us.";
}

// ---- replay: the second run costs nothing -----------------------------------------------------

/** Keep only what succeeded and can be replayed. The agent's dead ends are not part of the test. */
export function compile(startUrl, steps, proof = "", secrets = []) {
  const out = [];
  for (const s of steps) {
    if (!s.ok) continue;
    const a = s.action;
    if (a.kind === "click" && s.target) out.push({ kind: "click", ...s.target });
    else if (a.kind === "fill" && s.target) out.push({ kind: "fill", ...s.target, text: maskSecrets(a.text, secrets) });
    // The control, and NOT the file: the fixture is rebuilt at replay time from the accept
    // attribute the page has then. Baking a path in would break on the next machine, and baking
    // the bytes in would put a blob in somebody's repository and freeze the wrong file type the
    // day the form starts asking for a PDF.
    else if (a.kind === "upload" && s.target) out.push({ kind: "upload", ...s.target });
    else if (a.kind === "press") out.push({ kind: "press", key: a.key });
    // The URL is masked for the same reason the fill text and the proof are: a seeded fixture id
    // lives in a URL more naturally than anywhere else, and this file is cached by CI and
    // committed. replay() resolves it again, so a recording made against order A still navigates
    // to order B on the next run instead of being stale forever. lib/seedguard.mjs.
    else if (a.kind === "goto") out.push({ kind: "goto", url: maskUrl(a.url, secrets) });
  }
  // null, never an empty plan: a plan with no steps would "pass" instantly by exercising nothing,
  // which is the most dangerous artefact this code could produce.
  //
  // And null without a PROOF, for the same reason one degree worse. A recording is a list of
  // clicks; replaying it proves the buttons still exist, not that the app still works. Measured:
  // a checkout recorded while it worked, replayed against a build where the same button now says
  // "Something went wrong. Your card was not charged." — PASS in 0.5s, exit 0. A green check over
  // a broken checkout is worse than having no test, and it is the exact corner this speed
  // advantage was cutting.
  if (!out.length || !String(proof).trim()) return null;
  // The proof is masked too. A seeded fixture's own id is very often the thing the agent quotes as
  // proof — "Order A-1042 refunded" — and an unmasked proof would put the value in the recording
  // the CI template caches and users are told to commit. replay() resolves it again before it looks
  // for it on the page, so a recording made under one seeded id still replays green under the next.
  return { startUrl, steps: out, proof: maskSecrets(String(proof).trim(), secrets) };
}

/**
 * Point a recording at the URL being tested RIGHT NOW.
 *
 * A recording is made against one deploy preview and replayed against the next one, and a preview
 * hostname is different on every pull request. Replaying plan.startUrl verbatim drove the browser
 * to the deployment the recording was made on: green, in a few hundred milliseconds, having never
 * opened the change under review — or stale, because that preview had since been torn down. Both
 * are worse than no test. Caching recordings between CI runs is what makes this cheap, so the
 * recording has to survive the URL changing.
 *
 * Only origins matching the recorded start are rewritten. A step that navigates to a payment
 * provider or an identity provider must keep pointing at the provider, not at this preview.
 */
export function rebase(plan, url) {
  if (!url) return plan;
  let from = "";
  try {
    from = new URL(plan.startUrl).origin;
  } catch {
    return { ...plan, startUrl: url };
  }
  let to = "";
  try {
    to = new URL(url).origin;
  } catch {
    return plan;
  }
  // THE RECORDED PAGE IS PART OF THE RECORDING, AND BOTH BRANCHES USED TO THROW IT AWAY.
  //
  // Measured on a 50-test suite, which is the first size at which this is visible: every branch
  // below returned `startUrl: url`, so a test recorded on /checkout replayed from /. The button is
  // not on the home page, the locator spends its full 10s timeout failing to find it, the run is
  // reported STALE, and the agent is woken to re-record — at full model price, on every pull
  // request, for every test that does not happen to start at the site root.
  //
  // So the whole value of record-and-replay silently inverted for any app with more than one page:
  // the suite got slower and more expensive than having no recordings at all, while still looking
  // like it was working. A three-test demo suite starting at / cannot show this; fifty tests can.
  //
  // What `url` names is WHERE THE APP IS, not which page: its origin replaces the recorded origin,
  // and the recorded path, query and hash are kept. When `url` carries a path of its own (an app
  // deployed under a subpath, or a single `--test` run naming one page) it is used as a prefix,
  // unless the recorded path already sits under it — which is what stops `--url http://x/f7`
  // replaying a recording of /f7 against /f7/f7.
  // THE RULE: the URL given now says WHERE THE APP IS; the recording says WHICH PAGE.
  //
  // So the origin always comes from `url`, and the path, query and hash come from the recording —
  // except when the recording has no path of its own, where the path given now is used. One URL is
  // passed for a whole suite, so any other rule makes fifty tests share one page.
  const passed = (() => {
    try {
      return new URL(url);
    } catch {
      return null;
    }
  })();
  const at = (u) => {
    let rec;
    try {
      rec = new URL(u);
    } catch {
      return url;
    }
    if (!passed) return url;
    const bare = rec.pathname === "/" || rec.pathname === "";
    return bare
      ? passed.origin + passed.pathname + passed.search + passed.hash
      : passed.origin + rec.pathname + rec.search + rec.hash;
  };

  if (from === to) return { ...plan, startUrl: at(plan.startUrl) };
  const steps = (plan.steps || []).map((s) => {
    if (s.kind !== "goto" || typeof s.url !== "string") return s;
    try {
      const u = new URL(s.url);
      return u.origin === from ? { ...s, url: at(s.url) } : s;
    } catch {
      return s;
    }
  });
  return { ...plan, startUrl: at(plan.startUrl), steps };
}

/**
 * A RECORDING IS UNTRUSTED INPUT, and the only thing that may come of a bad one is "no recording".
 *
 * compile() refuses to write an empty plan, and that was read as covering the risk. It does not.
 * Every recording replayed in CI is one we READ BACK — out of an actions/cache entry that outlived
 * a cancelled job, a hand-edit, a merge, or a version of this CLI that wrote a different shape.
 * Measured against a real Chromium before this function existed:
 *
 *   steps: []       replayed nothing and printed "PASS — replayed 0 steps, no model calls", exit 0.
 *   steps: "nope"   has .length 4, so four unrecognised steps ran as four no-ops and printed
 *                   "PASS — replayed 4 steps". A green verdict on a pull request nobody tested is
 *                   the worst artefact this codebase can produce.
 *   a truncated file threw out of JSON.parse into the run's catch-all: exit 2 on a healthy app,
 *                   on every push, until somebody worked out that a cache had to be cleared by
 *                   hand — because the corrupt file is only ever replaced by a passing agent run,
 *                   and that path was never reached.
 *
 * So: parse it, and prove every step is one replay can actually perform. Anything else returns
 * plan:null with a sentence about the RECORDING — never a verdict about the app — and the caller
 * runs the agent, which records over it.
 */
const STEP_NEEDS = {
  click: ["role", "name"],
  fill: ["role", "name", "text"],
  upload: ["role", "name"],
  press: ["key"],
  goto: ["url"],
};

export function readPlan(text) {
  let raw;
  try {
    raw = JSON.parse(String(text));
  } catch (e) {
    return { plan: null, problem: `the recording is not valid JSON (${String(e && e.message ? e.message : e).split("\n")[0]}).` };
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return { plan: null, problem: "the recording is not an object." };
  if (!Array.isArray(raw.steps)) return { plan: null, problem: "the recording has no steps list." };
  if (raw.steps.length === 0) return { plan: null, problem: "the recording has no steps, so replaying it would check nothing." };
  for (let i = 0; i < raw.steps.length; i++) {
    const s = raw.steps[i];
    const need = s && typeof s === "object" ? STEP_NEEDS[s.kind] : undefined;
    // An unrecognised kind matches none of replay()'s branches, so it would be skipped in silence
    // and counted as a step that worked. `text` may be "" — clearing a field is a real step.
    if (!need) return { plan: null, problem: `step ${i + 1} of the recording is not something this version can replay.` };
    const missing = need.filter((k) => typeof s[k] !== "string" || (k !== "text" && !s[k]));
    if (missing.length) return { plan: null, problem: `step ${i + 1} of the recording is missing ${missing.join(" and ")}.` };
    // `frame` is OPTIONAL, and its absence is the whole backward-compatibility story: every
    // recording written before frames existed has no frame field and replays against the page
    // exactly as it always did. When it IS present it drives frameLocator(), so a malformed one
    // would silently aim the step at the page instead — the same control name may well exist out
    // there, which is how a step nobody performed passes.
    if (s.frame !== undefined) {
      const bad = !Array.isArray(s.frame) || !s.frame.length || s.frame.some((x) => typeof x !== "string" || !x.trim());
      if (bad) return { plan: null, problem: `step ${i + 1} of the recording names a frame this version cannot use.` };
    }
  }
  return { plan: raw, problem: "" };
}

/**
 * SAY WHAT WE ARE WAITING FOR, WHILE WE ARE WAITING FOR IT.
 *
 * MEASURED, replaying a recording against a server that accepts the connection and never answers:
 *
 *     replaying 1 recorded step (no model)…
 *     <thirty seconds of nothing at all>
 *     http://127.0.0.1:4478/ did not finish loading within 30s.
 *
 * The last two lines are good. The gap is not: thirty silent seconds after a line ending in an
 * ellipsis is indistinguishable from a hung runner, and the reader who Ctrl-Cs at ten seconds —
 * which is most of them, on a tool whose whole promise is that it is fast — never sees the sentence
 * that would have told them their own server is the thing that stopped.
 *
 * Nothing is printed on a page that loads, which is every ordinary run: the timer is cancelled long
 * before it fires. This adds a line only to the runs that were already going badly, and it names
 * the deadline so the wait has a visible end.
 */
export function sayIfSlow(log, line, afterMs = 4000) {
  if (typeof log !== "function") return () => {};
  const t = setTimeout(() => log(C.dim(line)), afterMs);
  // unref, so a pending timer can never be the reason a process stays alive one tick longer than
  // the run it belonged to.
  if (t.unref) t.unref();
  return () => clearTimeout(t);
}

export async function replay(page, plan, secrets = [], log = null) {
  const started = Date.now();
  // What the replay wants said out loud. Today: which file each upload fabricated, because a
  // replayed upload attaches a file nobody named and the reader has to be able to see which.
  const notes = [];
  {
    // The recording's own first navigation, which is where a replay against a dead or wedged
    // deployment spends its whole budget. 30_000 is stated rather than left to Playwright's
    // default so the number in the message and the number enforced are the same number.
    const stop = sayIfSlow(log, `  still waiting for ${plan.startUrl} to load — up to 30s, then this stops and says so.`);
    try {
      await page.goto(plan.startUrl, { waitUntil: "domcontentloaded", timeout: 30_000 });
    } finally {
      stop();
    }
  }
  for (let i = 0; i < plan.steps.length; i++) {
    const s = plan.steps[i];
    try {
      // resolveUrl, for the reason the fill below uses unmaskSecrets: a URL recorded as
      // /order?t={{ordertoken}} has to become THIS run's seeded value before it is navigated to.
      // With no secrets it is the identity function and the navigation is byte-for-byte what it was.
      if (s.kind === "goto") await page.goto(resolveUrl(s.url, secrets), { waitUntil: "domcontentloaded", timeout: 30_000 });
      // inFrame() with no frame IS page.getByRole — see lib/frames.mjs. A recording made inside an
      // embedded checkout replays into that same frame, with no model call, like any other step.
      else if (s.kind === "click") await inFrame(page, s.frame).getByRole(s.role, { name: s.name, exact: true }).click({ timeout: 10_000 });
      // The recording holds {{password}}, never the password. Resolve it from the environment at
      // the moment of the fill, so the credential exists in memory for this keystroke and in no
      // file we ever wrote.
      else if (s.kind === "fill") await inFrame(page, s.frame).getByRole(s.role, { name: s.name, exact: true }).fill(unmaskSecrets(s.text, secrets), { timeout: 10_000 });
      // ZERO MODEL CALLS, like every other step. The fixture is a pure function of the control's
      // `accept`, re-read here off the live page, so the identical bytes are rebuilt every run —
      // and a form that has since changed from image/* to application/pdf gets a PDF, which a
      // stored fixture never could. An upload that cannot be performed throws into the catch
      // below and is STALE, never a bug: the control changed, and only the agent can say how.
      else if (s.kind === "upload") {
        const up = await performUpload(page, inFrame(page, s.frame).getByRole(s.role, { name: s.name, exact: true }));
        if (!up.ok) throw new Error(up.detail);
        notes.push(up.note);
      }
      else if (s.kind === "press") await page.keyboard.press(s.key);
      // readPlan rejects these before we get here; this is for anyone calling replay() directly.
      // Falling through in silence would count a step nobody performed as a step that worked.
      else throw new Error(`step ${i + 1} is a ${JSON.stringify(s.kind)}, which this version cannot replay`);
      await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});
    } catch (e) {
      return { status: "stale", at: i, step: s, detail: String(e && e.message ? e.message : e).split("\n")[0], ms: Date.now() - started, notes };
    }
  }
  // THE STEPS WORKED. That is not the same as the test passing.
  //
  // The proof is the text the agent saw that would not be on the page if the test had failed. A
  // recording without one is refused at compile time, but an older recording on disk may predate
  // this, and treating a missing proof as "fine" would silently restore the bug: unproven, so the
  // agent re-runs.
  if (!plan.proof) {
    return { status: "unproven", detail: "this recording predates outcome checking, so replaying it proves only that the steps still work", ms: Date.now() - started, notes };
  }
  // visibleText, not document.body.innerText: measured, a confirmation rendered inside an iframe
  // or an open shadow root is absent from innerText entirely. Now that the agent can SEE into a
  // frame it will quote its proof from one, and checking only the main document would report
  // `outcome-changed` on a working flow and wake the agent at full price on every single run —
  // the rebase() cost explosion, rebuilt. The main document's text still comes first and uncapped,
  // so this is strictly additive: every proof that matched before matches still.
  const text = await visibleText(page).catch(() => "");
  // unmaskSecrets, for the same reason the fill above uses it: a proof recorded as "Order
  // {{orderid}} refunded" has to become THIS run's seeded id before it is looked for, or every
  // replay of a seeded test reports outcome-changed and wakes the agent at full price forever.
  // With no secrets this is the identity function and the comparison is byte-for-byte what it was.
  if (!String(text).includes(unmaskSecrets(plan.proof, secrets))) {
    // The flow still runs and the outcome changed. That is EITHER a regression or a reword, and a
    // replay cannot tell them apart any more than it can tell a rename from a removal — so it says
    // what it saw and lets the agent judge. Reporting it as a bug outright would page someone over
    // a copy change; reporting it as a pass is the bug this whole change exists to kill.
    return { status: "outcome-changed", proof: plan.proof, ms: Date.now() - started, notes };
  }
  return { status: "passed", steps: plan.steps.length, ms: Date.now() - started, notes };
}

/**
 * A replay failure is STALE, never a bug.
 *
 * "The button was renamed" and "the button is gone" are indistinguishable from a replay, and
 * guessing wrong pages somebody at 2am over a copy change. Only the agent can tell them apart.
 */
export function stalenessNote(r) {
  // The recording did not settle it. Three ways that happens, and none of them is a bug report:
  // only the agent can tell a rename from a removal, or a reword from a regression.
  if (r.status === "outcome-changed") {
    return `The recorded steps still work, but the page no longer says ${JSON.stringify(r.proof)} — the text that proved this test the last time it passed. That is either a regression or a reword, and a replay cannot tell which. Handing it to the agent.`;
  }
  if (r.status === "unproven") {
    return `This recording predates outcome checking: replaying it would prove the steps still work and nothing about whether they still do the right thing. Running the agent to record one that can be checked.`;
  }
  const s = r.step;
  const where = s && s.frame && s.frame.length ? ` inside the frame "${frameLabel(s.frame)}"` : "";
  const what = s.kind === "click" || s.kind === "fill" ? `the ${s.role} named "${s.name}"${where}` : `a ${s.kind}`;
  return `The recorded run no longer fits this app: at step ${r.at + 1}, ${what} could not be used (${r.detail}). That is not yet a bug — the control may simply have been renamed.`;
}

// ---- flake handling: one retry between a transient blip and a red X ---------------------------
//
// A suite people stop believing is worse than a suite that misses things, and the cheapest way to
// lose belief is a false failure with a transient cause: a slow network, a cold serverless start,
// an animation that had not settled. So a FAILED agent run is run once more, from a clean page —
// a fresh browser context with no cookies, no storage, nothing left over from the run that failed.
//
// What a retry may never do is quietly turn a flake into a pass. A test that only passes on retry
// is not healthy, and swallowing that is how a real intermittent bug hides for months. So a
// fail-then-pass is `flaky`: its own verdict, never counted as passed, never worded as a bug in
// the app, and its reason carries both halves.
//
// What is never retried: `errored` (our runner broke, and running a missing API key twice is just
// slower), and a stale or outcome-changed replay (those already escalate to the agent, which IS
// the retry).

/**
 * The verdict, given every agent attempt in order.
 *
 * A second attempt exists only because the first failed, so:
 *   one attempt          → its own verdict, untouched.
 *   a later attempt pass → `flaky`, and the reason names what failed AND what then passed —
 *                          either half alone reads as a verdict nobody reached.
 *   every attempt failed → `failed`, and the reason is the LAST run's: it is the run the evidence
 *                          shows, and two independent observations of one defect make it a much
 *                          stronger report than the first run alone. The first run stays in the
 *                          steps, not in the headline.
 */
export function settle(attempts) {
  const last = attempts[attempts.length - 1];
  if (attempts.length === 1) return { status: last.passed ? "passed" : "failed", reason: last.why };
  if (last.passed) {
    return {
      status: "flaky",
      reason:
        `Passed only on retry, which is not a pass. Run 1 failed: ${last1(attempts[0].why)} ` +
        `Run ${attempts.length}, from a clean page, passed: ${last1(last.why)} ` +
        `Nothing about the app changed in between, so this test is unreliable — if it keeps doing this, an intermittent bug is hiding behind it. ` +
        // WHICH of the two it is, when the runs say so. Both attempts are in hand — every step,
        // every target, every step's duration — and the difference between "this test is
        // unreliable" and "your app has a race" is often sitting right there. flakeNote answers
        // from that material or says it cannot tell; it never guesses. lib/flake.mjs.
        flakeNote(attempts[0], last),
    };
  }
  return {
    status: "failed",
    reason: `${last.why} (Observed twice: the test was retried from a clean page and failed both times.)`,
  };
}

/** A why that ends mid-sentence would splice two runs' prose into one unreadable clause. */
const last1 = (s) => {
  const t = String(s).trim();
  return /[.!?]$/.test(t) ? t : `${t}.`;
};

/**
 * What report() puts on the wire.
 *
 * `flaky` is a CLI-side verdict on purpose. The cloud runs API refuses it as an incoming status
 * (app/api/projects/[id]/runs/route.ts: "a single run is never flaky") because a row in the run
 * log carries one run's own verdict, and flakiness is a shape across rows. This runner has already
 * posted the failing first run as its own row by the time a retry passes, so the retry goes up as
 * what that run was — a pass — and the flakiness is visible exactly where the cloud looks for it:
 * a failed row and a passed row seconds apart, plus a reason that says so. Posting "flaky" instead
 * would 400 and the run would never reach the project at all.
 */
export function wireRun(run) {
  // Layout findings also stay on this side of the wire: the runs API validates its inputs, and a
  // field it has never heard of risks a 400 that silently loses the whole row — a verdict traded
  // for an advisory note. The suite reads them from the run object handed to onRun, which is the
  // un-stripped one.
  //
  // `share` is stripped for the same reason and one stronger one. It exists only when somebody
  // typed --share, and it carries the proof text, every step of a passing run and the path to a
  // screenshot — none of which the runs API has ever been sent. Letting it through would mean the
  // JSON posted to a project differed depending on whether a person asked for a link, which is
  // precisely the "byte-identical without the flag" promise this feature makes. One field, one
  // strip, and test/share.test.mjs compares the two payloads byte for byte.
  const { layout, share, ...r } = run;
  return r.status === "flaky" ? { ...r, status: "passed" } : r;
}

// ---- evidence at failure ----------------------------------------------------------------------
//
// A FAIL's reason describes the moment it broke; these files SHOW it: a full-page screenshot, the
// page's visible text, and the URL, captured before anything navigates away. Only on failed and
// flaky runs — a green run's screenshot is a disk slowly filling with nothing.

/**
 * Never called outside a try: evidence is a bonus on top of a verdict, not part of one. The agent
 * already saw the failure, and a full disk here must not turn a real FAIL into our own error.
 */
export async function captureEvidence(page, dir, secrets = []) {
  const out = { png: path.join(dir, "failure.png"), txt: path.join(dir, "failure.txt") };
  mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: out.png, fullPage: true });
  // The screenshot shows an embedded checkout; document.body.innerText does not contain one word
  // of it. Whoever opens this file after a failure inside a payment frame needs the frame's text.
  const text = await visibleText(page).catch(() => "");
  // Masked, URL included: a seeded token routinely lives in the path (/orders/A-1042) or in a query
  // string, and this file is uploaded as a CI artifact. The PNG beside it is NOT masked and cannot
  // be — it is a picture of the customer's own page, showing the customer's own data, and pretending
  // otherwise would be a guarantee we do not deliver.
  writeFileSync(out.txt, maskSecrets(`URL: ${page.url()}\n\n${String(text).trim()}\n`, secrets));
  return out;
}

/**
 * On GitHub Actions, put the failure where somebody will actually look.
 *
 * A pull request comment cannot embed a file that exists only on the runner's disk, but the run
 * summary can carry the failure and name the evidence files, so whoever opens the run knows what
 * to download — the shipped workflow uploads the evidence directory as an artifact for exactly
 * that click. Appended, never overwritten: other steps share this file.
 */
function appendStepSummary(env, { test, status, reason, evidence }) {
  const file = env.GITHUB_STEP_SUMMARY;
  if (!file) return;
  const lines = [`### ${status}: ${test}`, "", String(reason)];
  if (evidence) lines.push("", `Evidence: \`${evidence.png}\`, \`${evidence.txt}\``);
  try {
    appendFileSync(file, lines.join("\n") + "\n\n");
  } catch {
    /* the summary is a courtesy; a verdict must not change because this file was unwritable */
  }
}

/**
 * A filesystem-safe name for a test that has no recording to borrow one from. Mirrors suite.mjs's
 * slug(), unimported: suite.mjs imports this file, and completing that cycle is how ESM hands one
 * of the two files a half-initialised module.
 */
function fileId(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "test";
}

// ---- the browser, fetched only when this command is used --------------------------------------

/**
 * Find Playwright — and if it is not here, put it somewhere that is not the customer's repository.
 *
 * THREE PLACES, IN THIS ORDER, EACH FOR A FAILURE THAT ACTUALLY HAPPENS.
 *
 * 1. A plain import: in development, or when this CLI is installed as a dependency, it is already
 *    resolvable and nothing should be downloaded.
 * 2. The project's own node_modules, resolved from the working directory. Under `npx` this file
 *    lives in ~/.npm/_npx/<hash>/node_modules/smolanalytics/lib and ESM resolution walks up from
 *    THERE — it never looks at the repo the command was run in. So the previous version installed
 *    into the repo and then imported from the npx directory, found nothing, and told CI the browser
 *    could not be loaded. On a laptop with Playwright already installed it also downloaded a second
 *    copy for no reason.
 * 3. A cache directory under the user's home. `npm install` in the working directory creates
 *    node_modules/ and a package-lock.json inside the project, and in a repo with no package.json it
 *    creates them from nothing. "Nothing written to your repo" is the entire reason to use this
 *    instead of the alternative, and that has to be true of the browser too.
 */
function resolveFrom(dir, spec) {
  try {
    return createRequire(path.join(dir, "package.json")).resolve(spec);
  } catch {
    return "";
  }
}

async function importPlaywright(entry) {
  const m = await import(pathToFileURL(entry).href);
  // Playwright's entry point is CommonJS. Depending on the version, import() hands back the exports
  // directly or wrapped in .default; picking the wrong one fails later as "cannot read chromium of
  // undefined", a hundred lines from the cause.
  return m && m.chromium ? m : m.default;
}

function browserCacheDir() {
  return process.env.SMOLANALYTICS_CACHE || path.join(homedir(), ".cache", "smolanalytics");
}

/**
 * @param engine which browser to make sure is on disk when WE are the ones installing. On the
 * paths where Playwright is already resolvable this function never downloads a browser at all, so
 * a resolvable Playwright with no WebKit still reaches launchEngine — which is where the "install
 * it with" sentence lives. Both places name the ENGINE, never a bare `playwright install`.
 */
export async function loadPlaywright(log, yes, engine = DEFAULT_ENGINE) {
  try {
    return { pw: await import("playwright") };
  } catch {
    /* not resolvable from this file: try the project, then our own cache */
  }
  for (const dir of [process.cwd(), browserCacheDir()]) {
    const entry = resolveFrom(dir, "playwright");
    if (!entry) continue;
    try {
      return { pw: await importPlaywright(entry) };
    } catch {
      /* a partial install; keep going and reinstall over it */
    }
  }

  const home = browserCacheDir();
  log("");
  log(C.b(`This command drives a real browser, which needs Playwright (~50MB) and ${ENGINE_LABEL[engine] || engine}.`));
  log(C.dim("Every other command here has zero dependencies, so it is fetched only now, only once."));
  log(C.dim(`It goes in ${home}. Nothing is written to your project.`));
  if (!yes && process.stdin.isTTY) {
    log(C.dim("Re-run with --yes to install without asking."));
    return { pw: null, problem: "Playwright is not installed and this run was not given --yes, so the browser was never fetched." };
  }
  log(C.dim("installing…"));
  try {
    mkdirSync(home, { recursive: true });
    // npm needs a package.json at the prefix or it warns and behaves differently between versions.
    if (!existsSync(path.join(home, "package.json"))) {
      writeFileSync(path.join(home, "package.json"), JSON.stringify({ name: "smolanalytics-browser", private: true, version: "1.0.0" }, null, 2) + "\n");
    }
  } catch (e) {
    log(C.r(`could not create ${home}: ${e && e.message}`));
    return { pw: null, problem: `The browser cache ${home} could not be created (${e && e.message}).` };
  }
  const r = spawnSync("npm", ["install", "--silent", "--prefix", home, "playwright"], { stdio: "inherit" });
  if (r.status !== 0) {
    log(C.r(`could not install Playwright into ${home}. Install it yourself with: npm i playwright && npx playwright install ${engine}`));
    return { pw: null, problem: `Playwright could not be installed into ${home}. Install it with: npm i playwright && npx playwright install ${engine}` };
  }
  // Run the copy we just installed, not `npx playwright`, which would download the CLI a second
  // time and can pick a different version than the library we are about to import.
  const bin = path.join(home, "node_modules", ".bin", process.platform === "win32" ? "playwright.cmd" : "playwright");
  const install = existsSync(bin)
    ? spawnSync(bin, ["install", engine], { stdio: "inherit" })
    : spawnSync("npx", ["playwright", "install", engine], { stdio: "inherit" });
  if (install.status !== 0) {
    const label = ENGINE_LABEL[engine] || engine;
    log(C.r(`Playwright installed but ${label} did not. Run: npx playwright install ${engine}`));
    return { pw: null, problem: `Playwright installed but ${label} did not. Run: npx playwright install ${engine}` };
  }
  const entry = resolveFrom(home, "playwright");
  try {
    return { pw: await importPlaywright(entry) };
  } catch (e) {
    log(C.r(`Playwright installed but could not be loaded: ${e && e.message}`));
    return { pw: null, problem: `Playwright installed but could not be loaded: ${e && e.message}` };
  }
}

/**
 * Post the verdict, if a project is configured, and hand it to whoever is running this test.
 *
 * onRun is how `--suite` gets a STATUS rather than an exit code. Three statuses do not fit in one
 * integer, and a suite that had to guess "stale" from log text would eventually print "failed" for
 * a renamed button. Never throws, never affects the exit code.
 */
/**
 * What this run cost, for the wire.
 *
 * Absent rather than zero when nothing was measured: a run whose counts are missing must not be
 * recorded as free, because free and unmeasured are indistinguishable on a chart and one of them is
 * a lie. A ledger that exists and counted nothing IS zero — that is a replay, and it is the whole
 * economic argument, so it is reported.
 */
function costOf(ledger) {
  if (!ledger || typeof ledger.calls !== "number") return {};
  return {
    modelCalls: ledger.calls,
    inputTokens: (ledger.input || 0) + (ledger.cacheRead || 0) + (ledger.cacheWrite || 0),
    outputTokens: ledger.output || 0,
  };
}

async function report(run, log, onRun, ledger = null) {
  const priced = { ...run, ...costOf(ledger) };
  try {
    onRun?.(priced);
  } catch {
    /* a caller's bookkeeping must not change a verdict */
  }
  const projectId = process.env.SMOLANALYTICS_PROJECT;
  const writeKey = process.env.SMOLANALYTICS_WRITE_KEY;
  // NEITHER SET IS THE NO-ACCOUNT PATH AND MUST STAY SILENT. Most runs of this CLI have no project
  // at all — that is the whole "no account, nothing written to your repo" promise — and nagging
  // them about a feature they did not ask for is how a tool starts feeling like an advert.
  //
  // ONE SET WITHOUT THE OTHER IS A MISCONFIGURATION, AND IT WAS ALSO SILENT. Measured by walking a
  // real signup: the setup page hands you SMOLANALYTICS_PROJECT and SMOLANALYTICS_WRITE_KEY and
  // says "with those two set, every run your suite posts lands on the project page" — and the CI
  // template it points at has no slot for either of them. So somebody wires up half of it, the run
  // passes, this function returns without a word, and the project page shows "no test runs yet"
  // forever with nothing anywhere saying why. Somebody who set one of these wants the other.
  if (!projectId && !writeKey) return;
  if (!projectId || !writeKey) {
    const missing = projectId ? "SMOLANALYTICS_WRITE_KEY" : "SMOLANALYTICS_PROJECT";
    const has = projectId ? "SMOLANALYTICS_PROJECT" : "SMOLANALYTICS_WRITE_KEY";
    log(C.dim(`  not recorded — ${has} is set but ${missing} is not, so this run went nowhere. Both are on your project's setup page.`));
    return;
  }
  const base = (process.env.SMOLANALYTICS_URL || "https://smolanalytics.com").replace(/\/$/, "");
  try {
    const res = await fetch(`${base}/api/projects/${encodeURIComponent(projectId)}/runs`, {
      method: "POST",
      headers: { authorization: `Bearer ${writeKey}`, "content-type": "application/json" },
      // wireRun, not run: `flaky` exists only on this side of the wire. See wireRun for why.
      body: JSON.stringify(wireRun(priced)),
    });
    log(res.ok ? C.dim("  recorded to your project.") : C.dim(`  not recorded (${res.status}) — the verdict above still stands.`));
  } catch (e) {
    // A test tool that fails a build because its own telemetry could not be delivered gets removed
    // the same day. The verdict is already decided.
    log(C.dim(`  not recorded (${e && e.message}) — the verdict above still stands.`));
  }
}

/**
 * One agent run against one page: navigate, perceive, act, until finish or the budget runs out.
 *
 * Returns a record and never reports, closes, or exits — the caller owns the verdict, because a
 * failed attempt may be retried and only the caller knows whether this one settles it. A model
 * refusal and a model API failure still throw: both are the runner breaking, and neither is
 * retried. On the first attempt the throw lands in runOnce's catch as `errored`; on a retry the
 * loop catches it and keeps the failing run's verdict, so our outage cannot bury their bug.
 *
 *   { kind: "finish", passed, why, proof, steps, ms }   the agent reached a verdict
 *   { kind: "error", banner, why, steps, ms }           the runner did not — prose instead of a
 *                                                       tool call, or the step budget ran out
 */
async function agentAttempt({ page, url, test, apiKey, model, maxSteps, log, secrets = [], ledger = newLedger(), maxCalls = 0 }) {
  const started = Date.now();
  {
    // The agent path's first navigation, silent for the same 30s and for the same reason.
    const stop = sayIfSlow(log, `  still waiting for ${url} to load — up to 30s, then this stops and says so.`);
    try {
      await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
    } finally {
      stop();
    }
  }
  let snap = await perceive(page);
  // The sentence is carried around MASKED — "open order {{orderid}}" — and resolved here, at the
  // one moment the model actually needs the value. Everything else that touches the sentence (the
  // run summary, the row posted to a project, the evidence directory's name) writes the placeholder
  // instead. With no secrets this is the identity function and the prompt is unchanged. lib/seed.mjs.
  const messages = [{ role: "user", content: `THE TEST:\n${unmaskSecrets(test, secrets)}\n\nStarting page:\n\n${render(snap)}` }];
  const steps = [];

  for (let n = 1; n <= maxSteps; n++) {
    // BEFORE the call, never after: a ceiling that reports what was already spent is not a ceiling.
    // Stopping here is OUR decision, so it raises to the caller as a runner problem (exit 2) and is
    // never a verdict about the application — the same fence every other budget in this file sits on.
    const capped = overBudget(ledger, maxCalls);
    if (capped) throw new Error(capped);
    const res = await think(messages, apiKey, model);
    // Free and exact: the API already counted these, and we were discarding them.
    recordUsage(ledger, res);
    // A refusal is not a test failure and must never be reported as one: that would tell somebody
    // their checkout is broken because a safety classifier declined.
    if (res.stop_reason === "refusal") throw new Error("the model declined to continue; this is not a verdict about the app under test");
    messages.push({ role: "assistant", content: res.content });

    const calls = (res.content || []).filter((b) => b.type === "tool_use");
    if (!calls.length) {
      // The model replied with prose instead of a tool call. That is THIS RUNNER misbehaving, not
      // the application. Reporting it as FAIL/1 told somebody their checkout was broken because
      // the model wandered off — the exact confusion the statuses exist to prevent.
      return {
        kind: "error", banner: "no verdict", steps, ms: Date.now() - started,
        why: "The agent stopped without calling finish, so nothing was observed. This is the test runner, not your application.",
      };
    }

    const results = [];
    for (const call of calls) {
      const a = toAction(call);
      // The model quotes what it read, and what it read may be a seeded token. Masked once, here,
      // so every downstream use carries the placeholder: the step label, the reported step, the
      // verdict reason, the run summary, the pull request comment. `a.text` is deliberately NOT
      // masked — that one is the keystroke, and it has to be the real value.
      if (a.why) a.why = maskSecrets(a.why, secrets);
      if (a.kind === "finish") {
        return { kind: "finish", passed: a.passed, why: a.why, proof: a.proof, steps, ms: Date.now() - started };
      }

      const t0 = Date.now();
      const el = snap.elements.find((e) => e.ref === a.ref);
      // `frame` only when there is one, so a recording of an ordinary page is byte-identical to
      // what this wrote before frames existed. compile() spreads target straight into the step.
      const target = el ? { role: el.role, name: el.name, ...(el.frame && el.frame.length ? { frame: el.frame } : {}) } : undefined;
      const out = await act(page, snap, a);
      // `note` carries what an action decided on the agent's behalf — today, which file an upload
      // fabricated. Without it the agent is told "done" and has to guess what it attached, which
      // is exactly the guess that turns the app's correct "PNGs only" into a bug report.
      // `out.detail` is an error string from the browser, and a failed navigation quotes the URL
      // it could not reach — which is where a seeded value lives. Masked on the STEP, which is what
      // is printed, labelled and reported; the model below is still handed the raw one, because
      // that is its eyes.
      const step = { n, action: a, target, ok: out.ok, detail: maskSecrets(out.detail, secrets), note: out.note, ms: Date.now() - t0 };
      step.label = describe(step, secrets).trim().replace(/^[^ ]+ +\d+ /, "");
      steps.push(step);
      log(describe(step, secrets));
      if (a.why) log(`     ${C.dim(a.why)}`);

      snap = await perceive(page);
      results.push({ type: "tool_result", tool_use_id: call.id, is_error: !out.ok, content: `${out.ok ? (out.note ? `done — ${out.note}` : "done") : `FAILED: ${out.detail}`}\n\n${render(snap)}` });
    }
    messages.push({ role: "user", content: results });
  }

  // Out of budget is not a pass, and it is not a bug report either. An unfinished test observed
  // NOTHING, so `failed`/1 would put a red X on a pull request and a "the app did not do what the
  // sentence describes" next to a claim nobody made. Our step budget is our limit.
  const ms = Date.now() - started;
  return {
    kind: "error", banner: `${maxSteps} steps · ${(ms / 1000).toFixed(1)}s`, steps, ms,
    why: `The agent used all ${maxSteps} steps without reaching a verdict, so nothing was observed. This is the test runner, not your application: raise --max-steps, or split a test that describes more than one scenario.`,
  };
}

// `secrets` is the masking pairs for this run: {value, token} for anything that must be written
// down as a placeholder rather than as itself. Today --seed fills it (lib/seed.mjs); it is an array
// on purpose, so a credential source can be added to it without another parameter. [] — the default,
// and what every run without --seed passes — makes maskSecrets and unmaskSecrets identity functions,
// so nothing on this path changes by a single byte for anybody who has not asked for it.
/**
 * WHY THE RUN STOPPED, IN A SENTENCE SOMEBODY CAN ACT ON.
 *
 * Everything that throws out of a run arrives as Playwright's prose or an HTTP status, and the
 * three most common of them are the FIRST MINUTE going badly rather than anything about the app.
 * Measured, verbatim, by running the real binary:
 *
 *   the run could not complete: page.goto: net::ERR_CONNECTION_REFUSED at http://127.0.0.1:4999/pricing
 *   Call log:
 *     - navigating to "http://127.0.0.1:4999/pricing", waiting until "domcontentloaded"
 *
 * Nothing is listening on that port — a dev server that is not running, or a port typed wrong —
 * and neither of those words appears anywhere in the output. The Call log then repeats the URL a
 * third time and adds nothing the first line did not already say. The bad-key run was the same
 * shape one layer up: `the model call failed (401). {"type":"error","error":{"type":
 * "authentication_error","message":"API key is invalid."},"request_id":null}` — the actionable
 * word is inside a JSON blob at the end.
 *
 * So: name the cause first, name the fix second, and print no Call log. Prose ONLY. Nothing here
 * touches a verdict or an exit code; every one of these is still errored, still exit 2.
 *
 * `known` is false when nothing matched, and then `what` is the message exactly as it arrived. The
 * caller wraps that in its own "the run could not complete" / "the survey could not complete", so
 * an error this function has never seen reads today exactly as it read before this existed. Only
 * the cases with something better to say lose the prefix.
 */
/**
 * "replayed 1 steps". A one-step recording is the commonest replay there is — one goto and a proof
 * check — so the fastest, most-printed line in the product read as unfinished. Cosmetic, and the
 * fast path is where cosmetic is the whole impression.
 */
export const count = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function runnerProblem(err) {
  const raw = String(err && err.message ? err.message : err);
  // The Call log is dropped whatever the case: it is the widest thing on screen at the moment the
  // reader is looking for one sentence, and it only ever restates the line above it.
  const first = raw.split(/\n\s*Call log:/)[0].trim();
  const where = /\bat (https?:\/\/[^\s]+)/.exec(raw)?.[1] || /navigating to "([^"]+)"/.exec(raw)?.[1] || "";
  const origin = (() => {
    try {
      return new URL(where).origin;
    } catch {
      return where;
    }
  })();
  const model = /the model call failed \((\d{3})\)/.exec(raw);
  if (model) {
    const code = Number(model[1]);
    if (code === 401 || code === 403) {
      return { known: true, what: `Claude rejected ANTHROPIC_API_KEY (${code}).`, fix: "Check the key at console.anthropic.com/settings/keys, then run this again." };
    }
    if (code === 429) return { known: true, what: "Claude rate-limited this run (429).", fix: "Run it again in a minute, or lower --workers." };
    if (code >= 500) return { known: true, what: `Claude returned ${code}.`, fix: "That is an outage on their side, not your app. Run it again." };
    return { known: true, what: `Claude refused the request (${code}).`, fix: first.replace(/^the model call failed \(\d+\)\.\s*/, "").slice(0, 200) };
  }
  if (/ERR_UNSAFE_PORT/.test(raw)) {
    const port = (() => {
      try {
        return new URL(where).port;
      } catch {
        return "";
      }
    })();
    return { known: true, what: `The browser refuses to open port ${port || "that"}.`, fix: "Chromium blocks a fixed list of ports outright. Serve the app on another one." };
  }
  if (/ERR_CONNECTION_REFUSED/.test(raw)) {
    return { known: true, what: `Nothing is listening at ${origin || "that address"}.`, fix: "Start the app, or fix the port in --url." };
  }
  if (/ERR_NAME_NOT_RESOLVED/.test(raw)) {
    return { known: true, what: `${origin || "That host"} does not resolve.`, fix: "Check the hostname in --url." };
  }
  if (/ERR_CERT_|ERR_SSL_/.test(raw)) {
    return { known: true, what: `${origin || "That host"} served a certificate the browser will not accept.`, fix: "Use http:// for a local server, or trust the certificate first." };
  }
  if (/ERR_CONNECTION_TIMED_OUT|ERR_CONNECTION_RESET|ERR_EMPTY_RESPONSE|ERR_ADDRESS_UNREACHABLE/.test(raw)) {
    return { known: true, what: `${origin || "That address"} accepted no connection.`, fix: "Check the app is up and reachable from this machine." };
  }
  const slow = /Timeout (\d+)ms exceeded/.exec(raw);
  if (slow && /page\.goto|navigating to/.test(raw)) {
    return { known: true, what: `${where || "The page"} did not finish loading within ${Math.round(Number(slow[1]) / 1000)}s.`, fix: "The server took the connection and never answered. Check it is serving that path." };
  }
  return { known: false, what: first, fix: "" };
}

/**
 * `test`'s own usage, in one place because two readers now print it.
 *
 * MEASURED by asking the binary for help, which is the first thing a stranger does:
 *
 *   npx smolanalytics test --help     printed this block and exited 1
 *   npx smolanalytics suggest --help  printed its block and exited 2
 *   npx smolanalytics audit --help    scanned the repo and printed a full audit report
 *   npx smolanalytics init --help     detected the framework and asked for a write key
 *   npx smolanalytics connect --help  "connect needs your MCP token"
 *   npx smolanalytics desk --help     "desk needs a read key"
 *
 * `--help` was in the FLAGS allowlist for every one of those commands, so it was declared known
 * and then read by nobody: help arrived only as a side effect of failing a required-argument
 * check, which is why three commands answered a question about themselves by doing work on the
 * reader's repo instead. bin/smolanalytics.mjs now reads it before dispatch, and this is what it
 * prints for `test`.
 */
export function testUsage() {
  return `
${C.b("npx smolanalytics test")} — one sentence, a real browser, a verdict. No account.

  ${C.dim("Needs ANTHROPIC_API_KEY (console.anthropic.com/settings/keys) — the agent is Claude and the")}
  ${C.dim("calls are billed to you. With --plan it replays the recording instead, with no key at all.")}

  --url <url>      where the test starts (staging, a deploy preview, anything reachable)
  --test "<text>"  what should work, in plain English
  --plan <file>    replay this recording first; only wake the agent if it no longer fits
  --browser <name> chromium (default), firefox or webkit — the same test in a different engine
  --headed         watch it happen
  --yes            install the browser, and don't ask about a production-looking URL
  --seed <url>     POST this run's identity there BEFORE it, and use the JSON it returns as placeholders
  --teardown <url> POST this run's identity there afterwards, so you can delete what it made
  --email-domain <dom>  the domain in {{email}} (default example.com, which cannot receive mail)
  --retries <n>    re-run a failing test from a clean page (default 1; 0 disables)
  --evidence-dir <dir>  where a failure's screenshot and page text go (default .smolanalytics/evidence)

  ${C.dim('npx smolanalytics test --url https://yourapp.com --test "the pricing page shows a monthly price"')}
`;
}

async function runOnce({ url, test, plan: planPath, headed, maxSteps = 40, yes, retries = 1, evidenceDir = "", layout: layoutMode = "report", renderCheck = true, login = "", authFile = "", authDir = DEFAULT_AUTH_DIR, secrets = [], engine = DEFAULT_ENGINE, env = process.env, log = console.log, onRun, loadBrowser = loadPlaywright, share = false, maxCalls = 0,
  // True when runSuite is driving. The only thing it changes is how much is said about a missing
  // key: the suite says it once in its own header, and repeating the export line under every test
  // turned a fifty-test run into fifty copies of the same three sentences.
  inSuite = false,
  // One ledger per test, so the line printed under a verdict is that test's own spend and a suite
  // can total them without any test knowing it is in one. A caller may pass its OWN — lib/watch.mjs
  // does, because a watch session's ceiling and running total span every run, and the usage is
  // otherwise thrown away the moment this function returns. Default: exactly what it always was.
  ledger = newLedger() }) {
  // --share (lib/share.mjs). `share` collects the extra facts a share page renders and nothing
  // else: the proof text, the steps of a run that PASSED (which nothing else here keeps), and where
  // the failure screenshot landed. It is attached to the run object under one key, `share`, which
  // wireRun strips before anything is posted to a project — so with the flag off this is an empty
  // object spread into a literal, and with it on the wire payload is still identical.
  //
  // SCRUBBED HERE, at the moment the record is made, and not only when the bundle is assembled.
  // A suite assembles ONE bundle for fifty tests and does not hold any individual test's seeded
  // values — those live in testCmd, one level down — so a record that left this function carrying
  // a fixture token would reach the bundle with nothing left that knows how to mask it. The paths
  // in `evidence` are deliberately not walked: they are local filenames, they never appear in a
  // bundle (only the bytes they point at do), and rewriting one would make the file unreadable.
  const shareRec = (rec) => (share ? { share: { ...scrubDeep({ proof: rec.proof || "", steps: rec.steps || [] }, { secrets, env }), evidence: rec.evidence || null } } : {});
  if (!url || !test) {
    log(testUsage());
    // 2, NEVER 1. Measured by running the binary with a flag missing: `npx smolanalytics test
    // --url https://staging.myapp.com` (no --test) printed this block and exited 1 — the code the
    // shipped workflow publishes as "a test failed, the application is broken". Nothing was
    // opened and nothing was observed; the runner could not start. `suggest` already refuses this
    // way and test/suggest.test.mjs pins it as "exits 2, never 1"; this is the same rule on the
    // command that actually runs in CI, where a 1 puts a bug report about their app on a pull
    // request whose only defect is a missing flag in the workflow file.
    return 2;
  }

  // BEFORE THE BROWSER, WHEN THERE IS NO RECORDING TO FALL BACK ON.
  //
  // lib/suggest.mjs already writes the rule down: "fetching 50MB of Chromium and THEN failing on a
  // missing env var is the wrong order to disappoint somebody in." It applies here too, and this is
  // the case where it can be honoured: with no --plan there is nothing to replay, so a key that
  // cannot be sent means nothing will happen no matter what the browser does. On a first run — the
  // one where Playwright is not installed yet — the old order downloaded Chromium and then said the
  // key was wrong. WITH a recording the browser is loaded first exactly as before, because the
  // replay needs it and needs no key.
  const earlyKeyIssue = keyProblem(process.env.ANTHROPIC_API_KEY);
  if (earlyKeyIssue && !planPath) {
    if (inSuite) {
      log(C.dim("  no recording that still fits, and ANTHROPIC_API_KEY cannot be used — see above."));
    } else {
      log(`\n${C.y(earlyKeyIssue)}`);
      log(C.dim("  Replaying a recording (--plan) needs no key at all."));
    }
    await report({ test, status: "errored", mode: "agent", durationMs: 0, url, reason: earlyKeyIssue, ...shareRec({ proof: "", steps: [], evidence: null }) }, log, onRun, ledger);
    return 2;
  }
  const { pw, problem } = await loadBrowser(log, yes, engine);
  if (!pw) {
    // REPORTED, not just logged. A suite with no verdict for this test falls back to guessing why
    // (see noVerdictReason), and on a first CI run it guesses "ANTHROPIC_API_KEY is not set" —
    // sending someone to add a secret they already have, over a browser that never downloaded.
    await report({ test, status: "errored", mode: "agent", durationMs: 0, url, reason: `${problem || "The browser could not be started."} This is the test runner, not your application.`, ...shareRec({ proof: "", steps: [], evidence: null }) }, log, onRun, ledger);
    return 2;
  }

  // A KEY THAT CANNOT BE SENT IS NOT A KEY (lib/safety.mjs::keyProblem says what that means and
  // what it measured). Nulled rather than refused, so the only thing that changes for a run with
  // --plan is nothing at all: the recording still replays, and this is only reached if it did not
  // fit. What the reader gets instead of a ByteString error out of undici, or a 401 about a key
  // that was never the problem, is the sentence naming the character.
  const keyIssue = keyProblem(process.env.ANTHROPIC_API_KEY);
  const apiKey = keyIssue ? "" : process.env.ANTHROPIC_API_KEY;
  const model = process.env.SMOLANALYTICS_MODEL || "claude-opus-5";

  const started = Date.now();
  let browser = null;
  let session = null;

  try {
    // INSIDE the try. `chromium.launch()` is the single most common way a browser run dies on a CI
    // runner — "Host system is missing dependencies to run browsers" — and thrown from out here it
    // went past this function's whole errored/exit-2 contract to the CLI's last-resort catch, which
    // exits 1. That is the code reserved for the customer's app being broken.
    // launchEngine, not pw[engine].launch: an engine Playwright ships but nobody downloaded fails
    // with a twelve-line Unicode box and a stack trace that reads like our crash, and the one
    // thing the reader needs from it — which install command fixes this — is the one thing it
    // does not say. See lib/engines.mjs.
    browser = await launchEngine(pw, engine, { headless: !headed });
    // AUTHENTICATED FLOWS (lib/auth.mjs). With neither --login nor --auth-file, session.newPage()
    // IS browser.newPage() with the same viewport, and nothing else on this path runs at all.
    // With one of them, every page this run opens — the first and every retry — is a clean context
    // seeded from a saved session, verified against the login recording's own landing evidence.
    session = await openSession({
      browser, url, login, authFile, authDir, apiKey, maxSteps, env, log, perceive,
      performLogin: (o) => agentAttempt({ url, apiKey, model, ledger, maxCalls, ...o }),
    });
    if (session.problem) {
      // `errored` and exit 2, never `failed`/1: a session we could not establish says nothing
      // whatsoever about whether the application under test works.
      log(`\n${C.y("ERROR")} ${C.dim("· sign-in")}`);
      log(`${session.problem}\n`);
      await report({ test, status: "errored", mode: "agent", durationMs: Date.now() - started, url, reason: session.problem, ...shareRec({ proof: "", steps: [], evidence: null }) }, log, onRun, ledger);
      await browser.close().catch(() => {});
      return 2;
    }
    // let, not const: a retry replaces it with a clean one.
    let page = await session.newPage();
    // REPLAY FIRST — the path almost every real run takes, and it calls no model at all.
    if (planPath && existsSync(planPath)) {
      // A recording we cannot read or cannot replay is NO recording — not a pass, and not an error
      // either. Saying so and running the agent is the only outcome that ends with a correct
      // verdict and a recording that works next time. See readPlan for what this survived.
      let text = "";
      let problem = "";
      try {
        text = readFileSync(planPath, "utf8");
      } catch (e) {
        problem = `it could not be read (${e && e.message ? e.message : e}).`;
      }
      const { plan: recorded, problem: bad } = problem ? { plan: null, problem } : readPlan(text);
      if (!recorded) {
        log(C.y(`Ignoring ${planPath}: ${bad}`));
        log(C.dim("  Running the agent instead, which records over it."));
      } else {
        const plan = rebase(recorded, url);
        // THE ENGINE IS PART OF WHAT THIS WAS RECORDED AGAINST (lib/engines.mjs). "" whenever the
        // recording names this same engine, or names none, which is every ordinary run. When it
        // does differ the replay still runs — refusing would turn a fifty-test suite on three
        // engines into 150 agent runs — but nothing about it is allowed to be silent: the note
        // goes on the terminal AND into the reason posted to the project, on the pass and on the
        // stale alike. The verdict itself is untouched.
        const engineChange = recordedEngine(recorded);
        log(C.dim(`replaying ${count(plan.steps.length, "recorded step")} (no model)…`));
        const r = await replay(page, plan, secrets, log);
        // Which file each replayed upload fabricated. Empty on a recording with no upload step.
        for (const note of r.notes || []) log(C.dim(`  ${note}`));
        if (r.status === "passed") {
          // Computed here, not at each verdict: every way out of this block — the layout gate, the
          // render gate, the pass — is a verdict about a recording made on another engine, and one
          // of them staying silent is the exact "silently pass, silently fail" this forbids.
          const crossed = engineNote(engineChange, engine, "passed");
          // Layout sanity runs at verdict time on the page the replay ended on, scoped to the
          // steps it walked. Report-only unless --layout=strict — see lib/layout.mjs for why a
          // finding gating by default is the fastest way to lose this feature its trust.
          const lay = await auditLayout(page, { mode: layoutMode, targets: stepTargets(plan.steps) });
          const gate = layoutFailure(lay, layoutMode);
          if (gate) {
            log(`\n${C.r("FAIL")} ${C.dim(`· replayed ${count(plan.steps.length, "step")} · --layout=strict`)}`);
            log(`${gate}\n`);
            if (crossed) log(C.y(crossed));
            for (const line of layoutNoteLines(lay)) log(C.dim(line));
            await report({ test, status: "failed", mode: "replay", durationMs: r.ms, url, reason: withNote(gate, crossed), layout: lay, ...shareRec({ proof: plan.proof, steps: replaySteps(plan.steps), evidence: null }) }, log, onRun, ledger);
            await browser.close().catch(() => {});
            return 1;
          }
          // FALSE-GREEN GUARD (lib/render.mjs), and this is the exact place it belongs: a replay
          // decided PASS by finding its proof in DOM text, which a blank page, a page whose CSS
          // 404'd and a page under a crash overlay all still contain. Verdict-affecting by
          // contract — a would-be PASS only, never a failed/stale/errored — and off with
          // --no-render-check.
          const rend = await auditRender(page, { enabled: renderCheck });
          const rgate = renderFailure(rend);
          if (rgate) {
            log(`\n${C.r("FAIL")} ${C.dim(`· replayed ${count(plan.steps.length, "step")} · render check`)}`);
            log(`${rgate}\n`);
            if (crossed) log(C.y(crossed));
            for (const line of renderNoteLines(rend.slice(1))) log(C.dim(line));
            await report({ test, status: "failed", mode: "replay", durationMs: r.ms, url, reason: withNote(rgate, crossed), layout: lay, ...shareRec({ proof: plan.proof, steps: replaySteps(plan.steps), evidence: null }) }, log, onRun, ledger);
            await browser.close().catch(() => {});
            return 1;
          }
          log(`\n${C.g("PASS")}${C.dim(` — replayed ${count(r.steps, "step")} in ${(r.ms / 1000).toFixed(1)}s, no model calls.`)}`);
          if (crossed) log(C.y(crossed));
          for (const line of layoutNoteLines(lay)) log(C.dim(line));
          await report({ test, status: "passed", mode: "replay", durationMs: r.ms, url, reason: withNote("Replayed the recorded run; every step still worked.", crossed), layout: lay, ...shareRec({ proof: plan.proof, steps: replaySteps(plan.steps), evidence: null }) }, log, onRun, ledger);
          // Every close below a decided verdict carries .catch: close() can throw (seen once,
          // intermittently, under load), and unhandled it falls into the catch-all — which would
          // report errored/2 on top of a verdict that was already printed and posted.
          await browser.close().catch(() => {});
          return 0;
        }
        // Everything below is "the recording no longer settles it" — the steps broke, the outcome
        // changed, or there was no proof to check. All three hand over to the agent rather than
        // guessing, and none of them is reported as a bug on its own.
        const note = withNote(stalenessNote(r), engineNote(engineChange, engine, "stale"));
        log(`\n${C.y(note)}\n`);
        await report({ test, status: "stale", mode: "replay", durationMs: r.ms, url, reason: note, ...shareRec({ proof: plan.proof, steps: replaySteps(plan.steps, { failedAt: typeof r.at === "number" ? r.at : -1, detail: r.detail || "" }), evidence: null }) }, log, onRun, ledger);
      }
    }

    if (!apiKey) {
      if (inSuite) {
        // One line. The suite's header carries keyFix already, and its summary names this test.
        // keyIssue is named here too: the suite header says "not set", and a reader who can see
        // their own export would otherwise be told something they know to be false.
        // The diagnosis is the suite header's job (lib/suite.mjs prints keyProblem once for the
        // whole run). Per test, all this row owes the reader is which half stopped it.
        log(C.dim(keyIssue ? "  no recording that still fits, and ANTHROPIC_API_KEY cannot be used — see above." : "  no recording that still fits, and no ANTHROPIC_API_KEY to run the agent with."));
      } else if (keyIssue) {
        // NOT "the agent needs a Claude API key" — they set one. Say which character is wrong and
        // stop; keyFix's `export ANTHROPIC_API_KEY=sk-ant-…` is the line that CAUSED this in the
        // measured case, and repeating it here would send the reader round the same loop.
        log(`\n${C.y(keyIssue)}`);
        if (!planPath) log(C.dim("  Replaying a recording (--plan) needs no key at all."));
      } else {
        log(`\n${C.y("The agent needs a Claude API key.")}`);
        log(C.dim(`  ${keyFix(env)}`));
        { const where = keyWhere(env); if (where) log(C.dim(`  ${where}`)); }
        // Only where it is true. Printed to somebody who just typed --plan and watched the
        // recording go stale, it reads as advice they had already taken.
        if (!planPath) log(C.dim("  Replaying a recording (--plan) needs no key at all."));
      }
      await browser.close().catch(() => {});
      return 2;
    }

    // How to replay a passing run, and where to keep the proof of a failing one. The evidence
    // directory borrows the recording's name so one test's artefacts sit together, and falls back
    // to the sentence when there is no recording to borrow from.
    const evidencePath = path.join(
      evidenceDir || path.join(".smolanalytics", "evidence"),
      planPath ? path.basename(planPath).replace(/\.json$/i, "") : fileId(test),
    );
    const capture = async (pg) => {
      try {
        return await captureEvidence(pg, evidencePath, secrets);
      } catch (e) {
        log(C.dim(`could not capture evidence: ${e && e.message ? e.message : e}. The verdict above is unaffected.`));
        return null;
      }
    };
    const record = async (att, viaRetry) => {
      if (!planPath) return;
      // withEngine, so the next replay can say which engine this walk was discovered on. It only
      // ever ADDS a field — compile()'s refusal to write a proofless or empty recording is
      // untouched, and null stays null.
      const p = withEngine(compile(url, att.steps, att.proof, secrets), engine);
      if (p) {
        // THE PROOF IS CHECKED AGAINST THE PAGE BEFORE IT IS BELIEVED.
        //
        // compile() refuses an EMPTY proof. It cannot refuse a WRONG one, and a wrong one is the
        // likelier mistake: the model is asked for "exact page text" and answers with a paraphrase
        // of what it read — "Your order has been placed successfully" for a page that says "Thanks,
        // your mug is on its way." Measured with a scripted model against a real Chromium: the run
        // passed (exit 0), the recording was written, and the very next replay reported
        //   the page no longer says "Your order has been placed successfully" — the text that
        //   proved this test the last time it passed
        // which is false in both halves. The page never said it, and no run was ever proved by it.
        // Every subsequent run then went stale and woke the agent, so the recording could never
        // settle: the replay saving — the whole economic claim — silently never arrived, and the
        // customer was told their copy had changed when it had not.
        //
        // So: read the page the same way replay() will, and if the proof is not there, keep the
        // verdict and drop the recording. This can only ever cost one more agent run, which is
        // exactly what a recording that could never replay green was going to cost anyway, and it
        // never touches the verdict or the exit code — the agent watched the test pass.
        //
        // Our own failure to read the page is NOT evidence against the proof: a closed context or
        // a navigation in flight returns null here, and null records, as before.
        //
        // "the same way replay() will" is load-bearing, and it is why this reads visibleText and
        // not document.body.innerText. Measured once perception could see into frames: the agent
        // tested an embedded checkout, quoted "Payment received in full." out of the payment
        // frame — correctly, it was on the screen — and this check could not find one word of it,
        // so the recording was DROPPED on every green run. The test then cost a full agent run
        // forever while printing a pass, which is the replay saving quietly never arriving.
        const onPage = await visibleText(page).catch(() => null);
        if (typeof onPage === "string" && !onPage.includes(unmaskSecrets(p.proof, secrets))) {
          log(C.dim(`not recorded: the run passed, but the proof ${JSON.stringify(p.proof)} is not text on the page — a replay would report it as changed on every run. The next run uses the agent again.`));
          return;
        }
        // The verdict is already decided and reported by the time this runs. A read-only checkout
        // (a CI cache mount, a container image) turned a settled PASS into errored/2 here — an
        // outage report about a run the agent watched succeed. The recording is only next run's
        // speed, so losing it costs one more agent run, never the verdict.
        try {
          writeFileSync(planPath, JSON.stringify(p, null, 2) + "\n");
        } catch (e) {
          log(C.dim(`could not write the recording to ${planPath}: ${e && e.message ? e.message : e}. The verdict above is unaffected; the next run will use the agent again.`));
          return;
        }
        log(C.dim(`recorded ${p.steps.length} steps and the proof ${JSON.stringify(p.proof)} to ${planPath}${viaRetry ? " — from the retry, the run that passed" : ""} — the next run needs no model.`));
      } else if (!String(att.proof || "").trim()) {
        // No proof, no recording. Replaying clicks without checking the outcome is how a
        // green check ends up over a broken checkout.
        log(C.dim("not recorded: the run passed but named no proof text, so a replay could not tell a working page from a broken one."));
      } else {
        // A PASS THAT RECORDS NOTHING, AND USED TO SAY NOTHING.
        //
        // compile() also returns null when no step survived — an assertion-only test ("the pricing
        // page shows a monthly price", started on the pricing page) reads the page, finishes, and
        // performs nothing a replay could repeat. That is a legitimate test and a legitimate pass.
        //
        // MEASURED, walking a CI run: three tests, two printed "recorded … — the next run needs no
        // model", the third printed NOTHING, and every run after it reported "1 of 3 woke the
        // agent" forever. Both of the causes the summary offers for that — "no recording yet" and
        // "the recording stopped fitting" — are false here and both imply it will settle, so the
        // reader's next move is to go and debug their Actions cache, which is working fine.
        //
        // Silence at the exact moment its two siblings spoke. Named here, where the fact is known:
        // the number will not drop, and nothing is wrong.
        log(C.dim("not recorded: nothing happened that a replay could repeat — the run passed by reading the page. This test wakes the agent on every run; a read-only test is the one kind that never gets cheaper."));
      }
    };

    let a = await agentAttempt({ page, url, test, apiKey, model, maxSteps, log, secrets, ledger, maxCalls });
    if (a.kind === "error") {
      // The RUNNER misbehaved — the model answered prose, or the step budget ran out. Nothing was
      // observed, so it errors and exits 2, and it is NOT retried: paying for a second run of a
      // broken runner reports the same nothing, twice as slowly.
      const ms = Date.now() - started;
      log(`\n${C.y("ERROR")} ${C.dim(`· ${a.banner}`)}`);
      log(`${a.why}\n`);
      await report({ test, status: "errored", mode: "agent", durationMs: ms, url, reason: a.why, ...shareRec({ proof: "", steps: agentSteps(a.steps), evidence: null }) }, log, onRun, ledger);
      await browser.close().catch(() => {});
      return 2;
    }

    if (a.passed) {
      const ms = Date.now() - started;
      // Layout sanity on the final page, scoped to what this run clicked and filled plus the
      // visible controls it ended among. Report-only unless --layout=strict (lib/layout.mjs).
      const lay = await auditLayout(page, { mode: layoutMode, targets: stepTargets(a.steps) });
      const gate = layoutFailure(lay, layoutMode);
      if (gate) {
        log(`\n${C.r("FAIL")} ${C.dim(`· ${count(a.steps.length, "step")} · ${(ms / 1000).toFixed(1)}s · --layout=strict`)}`);
        log(`${gate}\n`);
        for (const line of layoutNoteLines(lay)) log(C.dim(line));
        const ev = await capture(page);
        if (ev) log(C.dim(`evidence: ${ev.png} and ${ev.txt}`));
        await report({ test, status: "failed", mode: "agent", durationMs: ms, url, reason: gate, layout: lay, ...shareRec({ proof: a.proof, steps: agentSteps(a.steps), evidence: ev }) }, log, onRun, ledger);
        // Still recorded: the walk itself passed and carries a proof, so the next run can replay
        // it for free — and the replay path re-audits the same page, so strict fails it again
        // without an agent run until the layout is actually fixed.
        await record(a, false);
        await browser.close().catch(() => {});
        return 1;
      }
      // FALSE-GREEN GUARD (lib/render.mjs). The agent said it passed by reading the page; this
      // asks whether the page was PAINTED. A would-be PASS only — a failed run is never revisited
      // here — and off with --no-render-check.
      const rend = await auditRender(page, { enabled: renderCheck });
      const rgate = renderFailure(rend);
      if (rgate) {
        log(`\n${C.r("FAIL")} ${C.dim(`· ${count(a.steps.length, "step")} · ${(ms / 1000).toFixed(1)}s · render check`)}`);
        log(`${rgate}\n`);
        for (const line of renderNoteLines(rend.slice(1))) log(C.dim(line));
        // The screenshot is the whole argument here: a reason that says "nothing rendered" is
        // worth far less than the picture of the empty page it is describing.
        const ev = await capture(page);
        if (ev) log(C.dim(`evidence: ${ev.png} and ${ev.txt}`));
        await report({ test, status: "failed", mode: "agent", durationMs: ms, url, reason: rgate, layout: lay, ...shareRec({ proof: a.proof, steps: agentSteps(a.steps), evidence: ev }) }, log, onRun, ledger);
        // Recorded, for the same reason the layout gate records: the walk itself passed and carries
        // a proof, so the replay path re-checks the render for free until the page is fixed.
        await record(a, false);
        await browser.close().catch(() => {});
        return 1;
      }
      log(`\n${C.g("PASS")} ${C.dim(`· ${count(a.steps.length, "step")} · ${(ms / 1000).toFixed(1)}s`)}`);
      log(`${a.why}\n`);
      for (const line of layoutNoteLines(lay)) log(C.dim(line));
      await report({ test, status: "passed", mode: "agent", durationMs: ms, url, reason: a.why, layout: lay, ...shareRec({ proof: a.proof, steps: agentSteps(a.steps), evidence: null }) }, log, onRun, ledger);
      await record(a, false);
      await browser.close().catch(() => {});
      return 0;
    }

    // FAILED. Capture the page as the agent left it, before anything navigates away — this is the
    // moment the reason describes, and it is gone the instant the page closes.
    const attempts = [a];
    let evidence = await capture(page);
    // Whether the loop below ended because a RETRY broke. In that case every attempt in `attempts`
    // has already been posted as its own row, and posting the settled verdict again put ONE
    // observation on the wire twice — two failed rows for one failure, which reads to the cloud's
    // reliability window as a test failing twice as often as it does.
    let retryBroke = false;

    while (!attempts[attempts.length - 1].passed && attempts.length <= retries) {
      const prev = attempts[attempts.length - 1];
      log(`\n${C.r("FAIL")} ${C.dim(`· run ${attempts.length} · ${count(prev.steps.length, "step")} · ${(prev.ms / 1000).toFixed(1)}s`)}`);
      log(prev.why);
      // The failing run goes up as its own row BEFORE the retry. Two runs are two real
      // observations, and the project's run history — where reliability is derived — needs both:
      // a retry that passes lands beside this row, and that pair is what flakiness looks like.
      await report({
        test, status: "failed", mode: "agent", durationMs: prev.ms, url, reason: prev.why,
        steps: prev.steps.map((s) => ({ n: s.n, do: s.label, why: s.action.why, ok: s.ok, detail: s.detail, ms: s.ms })),
        // The interim row of a run that will be settled below. It carries the screenshot taken at
        // the moment this attempt broke, because a retry that then breaks (retryBroke) makes THIS
        // the last word, and a share with no picture of the failure is the picture nobody has.
        ...shareRec({ proof: "", steps: agentSteps(prev.steps), evidence }),
      }, log, onRun, ledger);
      // Said out loud because it is somebody's bill: a retry is a full second agent run.
      log(C.y(`retrying from a clean page (retry ${attempts.length} of ${retries}) — another full agent run, roughly doubling this test's cost. --retries 0 disables it.`));
      await page.close().catch(() => {});
      // newPage() is a fresh browser context: no cookies, no storage, nothing left over from the
      // failure. The retry reuses the run's IDENTITY on purpose — a fresh one would leak the first
      // past the teardown hook, which posts exactly one. The cost is honest: a signup that
      // half-completed on the failing run can make the retry fail differently ("email already
      // exists"), and that lands as `failed`, never as a false pass.
      let next;
      try {
        // newPage is INSIDE the try: a context that cannot open is the retry breaking, and the
        // keep-the-failing-run's-verdict contract below has to cover it, not just the attempt.
        // session.newPage(), so the clean page the retry promises is still a SIGNED-IN clean page:
        // a new context seeded from the saved session, carrying nothing from the run that failed.
        page = await session.newPage();
        next = await agentAttempt({ page, url, test, apiKey, model, maxSteps, log, secrets, ledger, maxCalls });
      } catch (e) {
        next = { kind: "error", banner: "retry", why: String(e && e.message ? e.message : e) };
      }
      if (next.kind === "error") {
        // The RETRY broke — our side, not the app. But the first run observed a real failure, and
        // `errored` here would bury it under our own outage. The verdict falls back to what
        // --retries 0 would have said: failed, on the one observation we do have.
        log(C.y(`the retry could not complete (${next.why}) — keeping the failing run's verdict.`));
        retryBroke = true;
        break;
      }
      attempts.push(next);
      if (!next.passed) evidence = (await capture(page)) || evidence;
    }

    const settled = settle(attempts);
    const ms = Date.now() - started;
    const lastAttempt = attempts[attempts.length - 1];
    // Layout sanity on the page the last attempt ended on. The verdict here is failed or flaky —
    // never passed — so strict has nothing to flip: findings are the same advisory note in every
    // mode, because "the checkout broke AND its button is under a banner" is context, not a gate.
    const lay = await auditLayout(page, { mode: layoutMode, targets: stepTargets(lastAttempt.steps) });
    if (settled.status === "flaky") {
      log(`\n${C.y("FLAKY")} ${C.dim(`· failed, then passed on retry · ${(ms / 1000).toFixed(1)}s total`)}`);
      log(`${settled.reason}`);
      // Not a red X: one false failure costs more trust than ten real catches earn, and a gate
      // here trains people to re-run until green — the exact swallowing `flaky` exists to prevent.
      log(C.dim("flaky is a warning, not a failure: exit 0. A test that keeps doing this is hiding an intermittent bug."));
    } else {
      log(`\n${C.r("FAIL")} ${C.dim(`· ${attempts.length > 1 ? `observed ${attempts.length} times · ` : ""}${count(lastAttempt.steps.length, "step")} · ${(ms / 1000).toFixed(1)}s`)}`);
      log(`${settled.reason}\n`);
    }
    for (const line of layoutNoteLines(lay)) log(C.dim(line));
    // WHAT THIS RUN COST, under the verdict, where the person deciding whether they can afford to
    // put this on every pull request is actually looking. Tokens are exact — the API counted them.
    // Money appears only if a price was supplied, because a figure we invented would be read as a
    // measurement. A replay says "no model calls", which is the entire economic argument in three
    // words.
    log(C.dim(costLine(ledger, priceFrom(env))));
    const hint = priceHint(ledger, priceFrom(env));
    if (hint) log(C.dim(hint));
    if (evidence) log(C.dim(`evidence: ${evidence.png} and ${evidence.txt}`));
    appendStepSummary(env, { test, status: settled.status, reason: settled.reason, evidence });
    // Not when the retry broke: every attempt was already posted as its own row before its retry
    // started, so the settled verdict adds no new observation — posting it doubled one failure
    // into two rows. The loop that finished normally still owes the settled row: it carries the
    // verdict the in-loop rows were building toward (the flaky pair's second half, or the
    // fail-confirmed-twice report with both runs' steps).
    if (!retryBroke) {
      await report({
        test, status: settled.status, mode: "agent", durationMs: ms, url, reason: settled.reason, layout: lay,
        // Every attempt's steps, tagged by run: "the first is kept in the steps" is the contract
        // that lets the reason be the second run's without the first observation disappearing.
        steps: attempts.flatMap((att, i) =>
          att.steps.map((s) => ({
            ...(attempts.length > 1 ? { run: i + 1 } : {}),
            n: s.n, do: s.label, why: s.action.why, ok: s.ok, detail: s.detail, ms: s.ms,
          }))),
        // Same rule for the share: every attempt's steps, tagged, so a `flaky` page shows the run
        // that failed AND the run that then passed. Either half alone reads as a verdict nobody
        // reached — the same reason settle() puts both into the prose.
        ...shareRec({
          proof: lastAttempt.proof || "",
          steps: attempts.flatMap((att, i) => agentSteps(att.steps, { run: attempts.length > 1 ? i + 1 : 0 })),
          evidence,
        }),
      }, log, onRun, ledger);
    }
    // The retry that passed is a genuine passing run with a proof, so it records like one. If the
    // app is intermittently broken, the replay's proof check catches it as outcome-changed and
    // escalates to the agent — never a silent green.
    if (settled.status === "flaky") await record(lastAttempt, true);
    await browser.close().catch(() => {});
    return settled.status === "flaky" ? 0 : 1;
  } catch (e) {
    // browser may be null: launch itself is what usually fails.
    await browser?.close().catch(() => {});
    const why = runnerProblem(e);
    // Unrecognised keeps the sentence it always had, in both its shapes: lower case in the
    // terminal, where it follows a line, and capitalised in `reason`, which is posted on a pull
    // request as a sentence of its own. Only a cause we can name earns its own opening line.
    const what = why.known ? why.what : `the run could not complete: ${why.what}`;
    const said = why.known ? why.what : `The run could not complete: ${why.what}`;
    log(`\n${C.r(what)}`);
    if (why.fix) log(C.dim(`  ${why.fix}`));
    await report({ test, status: "errored", mode: "agent", durationMs: Date.now() - started, url, reason: `${said}${why.fix ? ` ${why.fix}` : ""} This is the test runner, not your application.`, ...shareRec({ proof: "", steps: [], evidence: null }) }, log, onRun, ledger);
    // Exit 2, not 1: the test did not fail, the runner did. A CI gate must tell those apart, or an
    // outage on our side reads to a customer as their app being broken.
    return 2;
  }
}

/**
 * The run, plus the three things that keep a real browser against a real app from being a surprise:
 * a traceable identity, a warning before a production-looking URL, and an optional teardown hook.
 * See lib/safety.mjs for why each one exists.
 *
 * A WRAPPER, not edits threaded through runOnce. runOnce has seven exit paths — no browser, replay
 * passed, no key, a verdict, no tool call, out of steps, a throw — and teardown has to fire on all
 * of them. The one that got forgotten would be the one that created the order.
 */
export async function testCmd(opts = {}) {
  const { url, test, yes, teardown = "", seed = "", emailDomain = "", runId = "", log = console.log, env = process.env, onRun, ask, share = false, publish = share, engine = DEFAULT_ENGINE,
    // Injectable for the same reason suiteCmd's is: the guarantee under test is that NOTHING this
    // function does after the verdict can move the exit code, and a guard is only proved by a
    // publisher that actually fails.
    publishShareImpl = publishShare } = opts;
  // The usage text, and the exit code that goes with it, stay exactly where they were.
  if (!url || !test) return runOnce(opts);


  const identity = newIdentity({
    domain: emailDomain || env.SMOLANALYTICS_TEST_EMAIL_DOMAIN,
    // The explicit option wins over the environment: SMOLANALYTICS_RUN_ID pins ONE id for a
    // whole CI run, and a nine-test suite under one id is nine signups under one email — test two
    // dies on "email already exists" and it reads as the app failing. runSuite passes a per-test
    // id derived from it instead.
    runId: runId || env.SMOLANALYTICS_RUN_ID,
  });
  const sub = substitute(test, identity);
  // With --seed, a token this runner does not know may be one the seed endpoint is about to return,
  // so naming it here would warn about {{orderId}} on every single seeded run. The naming still
  // happens — seedRun does it, once the response is in and it knows which tokens nobody filled, and
  // there it is a setup failure rather than a note. lib/seed.mjs says why.
  for (const bad of (seed ? [] : sub.unknown)) {
    // Named and left in place. Silently dropping it would hand the model an empty field to invent
    // a value for, and an invented value is a row nobody can find afterwards.
    log(C.y(`${bad} is not a placeholder this runner knows, so it stays in the sentence as written.`));
    log(C.dim(`  known: ${PLACEHOLDER_LIST}`));
  }
  if (sub.used.length) log(C.dim(`this run is ${identity.email} (${sub.used.map((k) => `{{${k}}}`).join(" ")})`));

  // DO NOT WARN ABOUT PRODUCTION FOR A RUN THAT CANNOT HAPPEN.
  //
  // Measured by running the homepage's own command as a stranger: with no key, the first thing
  // printed was a twelve-line warning about creating real accounts and possibly a real charge,
  // then a generated identity, and only at the very bottom the one line that mattered — you need
  // an API key, and nothing ran. The warning is right and worth having; it is simply noise ahead
  // of a run that stops two frames later, and it buries the actionable sentence.
  //
  // The key is not checked here as a gate, because a REPLAY needs no key at all (--plan, and a
  // suite whose recordings still fit) — refusing those would break the cheapest path in the
  // product. This only decides whether the QUESTION is worth asking now: with no key and no
  // recording named, runOnce is about to stop and say so, so we let it.
  // keyProblem, not just presence. The bug this whole block exists to prevent — a twelve-line
  // warning about real accounts and a possible real charge, printed ahead of a run that stops two
  // frames later — came back in a second shape the moment a key was SET and unusable: with
  // ANTHROPIC_API_KEY=sk-ant-… (our own help text, pasted) the reader got the production preamble,
  // a generated identity, and then a ByteString error. An unusable key cannot ask the model, so it
  // does not buy the question.
  const rawKey = env.ANTHROPIC_API_KEY ?? process.env.ANTHROPIC_API_KEY;
  const willAskTheModel = (Boolean(rawKey) && !keyProblem(rawKey)) || Boolean(opts.plan) || Boolean(opts.plans);
  const decision = willAskTheModel
    ? await confirmProduction({ url, identity, yes, teardown, log, env, ask })
    : { proceed: true };
  if (!decision.proceed) {
    const why = `Stopped at the question about ${url}: nothing was opened and nothing was tested. Re-run with --yes to skip the question.`;
    log(`\n${C.y("nothing ran")} ${C.dim(why)}\n`);
    // The suite is told, so this test is named in the summary instead of vanishing. Not POSTed to
    // a project: a run that never happened is not a verdict, and `mode` would have to be invented.
    try {
      onRun?.({ test: sub.text, status: "errored", mode: "agent", durationMs: 0, url, reason: why });
    } catch {
      /* a caller's bookkeeping must not change an exit code */
    }
    // 2, never 1. Declining is not the application being broken, and it is certainly not 0.
    return 2;
  }

  let status = "errored";
  // --share (lib/share.mjs). Every run object this command produces, kept so the bundle can be
  // assembled from the same records the verdict was reported from — never from a second, parallel
  // account of what happened, which is how two views of one run drift apart.
  const shareRuns = [];
  // The exit code, held in a variable rather than returned directly, so that the share below runs
  // in the `finally` AFTER the value is fixed. That is not a style choice: it is what makes
  // "sharing can never change an exit code" a property of the control flow rather than a promise.
  let code = 2;
  let shareSecrets = [];
  try {
    // --seed, BEFORE the browser opens and INSIDE this try, so a seed that half-built a fixture and
    // then failed is still cleaned up by the teardown hook in the finally below. After the
    // production question, never before it: declining must leave "nothing was opened and nothing was
    // tested" true, and a POST that fabricated an order first would make it a lie.
    const seeded = seed ? await seedRun({ endpoint: seed, identity, test: sub.text, url, env, log }) : null;
    if (seeded && seeded.problem) {
      // ERRORED AND 2, NEVER FAILED AND 1. We could not build the world the sentence describes, so
      // nothing was observed about the application. Reporting a failure here would put a bug report
      // about their refund flow on a pull request because our setup step could not reach their box.
      log(`\n${C.y("ERROR")} ${C.dim("· seed")}`);
      log(`${seeded.problem}\n`);
      status = "errored";
      try {
        // Told to the suite so the row is named, not POSTed to a project: no run happened, and the
        // decline path above sets the same precedent for the same reason.
        onRun?.({ test: sub.text, status: "errored", mode: "agent", durationMs: seeded.ms || 0, url, reason: seeded.problem });
      } catch {
        /* a caller's bookkeeping must not change an exit code */
      }
      return 2;
    }
    shareSecrets = seeded ? seeded.secrets : [];
    code = await runOnce({
      ...opts,
      // The seeded sentence carries {{orderid}}, not the value, and `secrets` is what resolves it
      // at the model prompt and at a keystroke — the credential pattern, applied to a fixture id
      // that may just as well be a session token. Both are [] / unchanged without --seed.
      test: seeded ? seeded.text : sub.text,
      secrets: seeded ? seeded.secrets : [],
      onRun: (r) => {
        // Recorded BEFORE the caller's handler runs, so a handler that throws cannot cost the
        // teardown its status.
        status = r.status;
        if (share) shareRuns.push(r);
        onRun?.(r);
      },
    });
    return code;
  } finally {
    if (teardown) {
      // In `finally`, so it fires on a failed run and on a crash too. A FAILED run is the likeliest
      // one to have left a half-made account behind: the row was created, then the app broke.
      const r = await postTeardown({ endpoint: teardown, identity, test: sub.text, url, status, env });
      log(r.ok ? C.dim(`  teardown: ${r.detail}`) : C.y(`  teardown failed: ${r.detail}`));
      if (!r.ok) log(C.dim(`  ${identity.email} may still exist. The verdict above is unaffected.`));
    }
    // LAST, and after teardown, because the link is the line a person copies and it belongs at the
    // bottom of the transcript where Loom puts it — not buried above a teardown note. `publish` is
    // false when a suite is running this: the suite publishes ONE link for the whole run (see
    // suiteCmd), because twenty links is twenty addresses and no message.
    if (publish) {
      // The verdict-carrying run is the LAST one: with --retries, the failing first attempt is
      // posted as its own row and then settled, and the settled row is the run's verdict.
      const last = shareRuns[shareRuns.length - 1];
      // Wrapped, because this is a `finally`: a rejection here would replace the `return code`
      // above with a rejected promise and hand bin/smolanalytics.mjs an exit 2 for a run that had
      // already decided it was a 1. publishShare guards itself as well; this is the guard that
      // sits on the same side of the call as the exit code.
      try {
      await publishShareImpl({
        tests: last
          ? [{
              name: sub.text.slice(0, 80),
              // No file: a `--test "…"` run has a sentence and no markdown behind it, and naming
              // the RECORDING here would put a path on the page that is not the test.
              file: "",
              sentence: last.test || sub.text,
              status: last.status,
              mode: last.mode || "",
              reason: last.reason || "",
              proof: last.share?.proof || "",
              durationMs: last.durationMs || 0,
              steps: last.share?.steps || [],
              suspects: [],
              evidence: last.share?.evidence || null,
            }]
          : [],
        url,
        engine,
        exitCode: code,
        projectId: env.SMOLANALYTICS_PROJECT || "",
        env,
        secrets: shareSecrets,
        log,
      });
      } catch (e) {
        log(C.dim(`  not shared: ${e && e.message ? e.message : e}. The verdict above still stands.`));
      }
    }
  }
}
