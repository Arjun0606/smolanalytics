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
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

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
async function perceive(page) {
  const [aria, title, text] = await Promise.all([
    page.locator("body").ariaSnapshot().catch(() => ""),
    page.title().catch(() => ""),
    page.evaluate(() => document.body?.innerText ?? "").catch(() => ""),
  ]);
  const { elements, truncated } = flatten(aria);
  return {
    url: page.url(),
    title,
    elements,
    truncated,
    text: String(text).replace(/\n{3,}/g, "\n\n").trim().slice(0, 4000),
  };
}

export function render(s) {
  const lines = s.elements.map((e) => {
    const bits = [e.ref, e.role, JSON.stringify(e.name)];
    if (e.value) bits.push(`value=${JSON.stringify(e.value)}`);
    if (e.state) bits.push(`[${e.state}]`);
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
    "PAGE TEXT:",
    s.text || "(empty)",
  ].join("\n");
}

function locate(page, snap, ref) {
  const el = snap.elements.find((e) => e.ref === ref);
  if (!el) return null;
  return page.getByRole(el.role, { name: el.name, exact: true });
}

// ---- the agent --------------------------------------------------------------------------------

const TOOLS = [
  { name: "click", description: "Click one element from the ELEMENTS list.",
    input_schema: { type: "object", properties: { ref: { type: "string" }, why: { type: "string" } }, required: ["ref", "why"], additionalProperties: false } },
  { name: "fill", description: "Type text into a textbox, searchbox or combobox. Replaces what is there.",
    input_schema: { type: "object", properties: { ref: { type: "string" }, text: { type: "string" }, why: { type: "string" } }, required: ["ref", "text", "why"], additionalProperties: false } },
  { name: "press", description: "Press a key, e.g. Enter. Use when a form submits on Enter.",
    input_schema: { type: "object", properties: { key: { type: "string" }, why: { type: "string" } }, required: ["key", "why"], additionalProperties: false } },
  { name: "goto", description: "Navigate to a URL. Prefer clicking; use this only when the test says to open a page.",
    input_schema: { type: "object", properties: { url: { type: "string" }, why: { type: "string" } }, required: ["url", "why"], additionalProperties: false } },
  { name: "scroll", description: "Scroll when what you need is probably above or below the fold.",
    input_schema: { type: "object", properties: { direction: { type: "string", enum: ["up", "down"] }, why: { type: "string" } }, required: ["direction", "why"], additionalProperties: false } },
  { name: "finish", description: "End the test. passed=true only if you directly OBSERVED what the test asked you to verify.",
    input_schema: { type: "object", properties: { passed: { type: "boolean" }, why: { type: "string" } }, required: ["passed", "why"], additionalProperties: false } },
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
    case "press": return { kind: "press", key: String(i.key), why: String(i.why) };
    case "goto": return { kind: "goto", url: String(i.url), why: String(i.why) };
    case "scroll": return { kind: "scroll", direction: i.direction === "up" ? "up" : "down", why: String(i.why) };
    case "finish": return { kind: "finish", passed: Boolean(i.passed), why: String(i.why) };
    default: return { kind: "finish", passed: false, why: `unknown tool ${call.name}` };
  }
}

function describe(step) {
  const a = step.action;
  const mark = step.ok ? C.g("✓") : C.r("✗");
  const label =
    a.kind === "click" ? `click ${step.target ? `${step.target.role} "${step.target.name}"` : a.ref}`
    : a.kind === "fill" ? `fill ${step.target ? `"${step.target.name}"` : a.ref} = ${JSON.stringify(a.text)}`
    : a.kind === "goto" ? `goto ${a.url}`
    : a.kind === "press" ? `press ${a.key}`
    : `scroll ${a.direction}`;
  return `  ${mark} ${String(step.n).padStart(2)} ${label}${step.ok ? C.dim(` ${step.ms}ms`) : C.r(` — ${step.detail}`)}`;
}

// ---- replay: the second run costs nothing -----------------------------------------------------

/** Keep only what succeeded and can be replayed. The agent's dead ends are not part of the test. */
export function compile(startUrl, steps) {
  const out = [];
  for (const s of steps) {
    if (!s.ok) continue;
    const a = s.action;
    if (a.kind === "click" && s.target) out.push({ kind: "click", ...s.target });
    else if (a.kind === "fill" && s.target) out.push({ kind: "fill", ...s.target, text: a.text });
    else if (a.kind === "press") out.push({ kind: "press", key: a.key });
    else if (a.kind === "goto") out.push({ kind: "goto", url: a.url });
  }
  // null, never an empty plan: a plan with no steps would "pass" instantly by exercising nothing,
  // which is the most dangerous artefact this code could produce.
  return out.length ? { startUrl, steps: out } : null;
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
  if (from === to) return { ...plan, startUrl: url };
  const steps = (plan.steps || []).map((s) => {
    if (s.kind !== "goto" || typeof s.url !== "string") return s;
    try {
      const u = new URL(s.url);
      return u.origin === from ? { ...s, url: to + u.pathname + u.search + u.hash } : s;
    } catch {
      return s;
    }
  });
  return { ...plan, startUrl: url, steps };
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
  }
  return { plan: raw, problem: "" };
}

export async function replay(page, plan) {
  const started = Date.now();
  await page.goto(plan.startUrl, { waitUntil: "domcontentloaded" });
  for (let i = 0; i < plan.steps.length; i++) {
    const s = plan.steps[i];
    try {
      if (s.kind === "goto") await page.goto(s.url, { waitUntil: "domcontentloaded", timeout: 30_000 });
      else if (s.kind === "click") await page.getByRole(s.role, { name: s.name, exact: true }).click({ timeout: 10_000 });
      else if (s.kind === "fill") await page.getByRole(s.role, { name: s.name, exact: true }).fill(s.text, { timeout: 10_000 });
      else if (s.kind === "press") await page.keyboard.press(s.key);
      // readPlan rejects these before we get here; this is for anyone calling replay() directly.
      // Falling through in silence would count a step nobody performed as a step that worked.
      else throw new Error(`step ${i + 1} is a ${JSON.stringify(s.kind)}, which this version cannot replay`);
      await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});
    } catch (e) {
      return { status: "stale", at: i, step: s, detail: String(e && e.message ? e.message : e).split("\n")[0], ms: Date.now() - started };
    }
  }
  return { status: "passed", steps: plan.steps.length, ms: Date.now() - started };
}

/**
 * A replay failure is STALE, never a bug.
 *
 * "The button was renamed" and "the button is gone" are indistinguishable from a replay, and
 * guessing wrong pages somebody at 2am over a copy change. Only the agent can tell them apart.
 */
export function stalenessNote(r) {
  const s = r.step;
  const what = s.kind === "click" || s.kind === "fill" ? `the ${s.role} named "${s.name}"` : `a ${s.kind}`;
  return `The recorded run no longer fits this app: at step ${r.at + 1}, ${what} could not be used (${r.detail}). That is not yet a bug — the control may simply have been renamed.`;
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

async function loadPlaywright(log, yes) {
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
  log(C.b("This command drives a real browser, which needs Playwright (~50MB) and Chromium."));
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
    log(C.r(`could not install Playwright into ${home}. Install it yourself with: npm i playwright && npx playwright install chromium`));
    return { pw: null, problem: `Playwright could not be installed into ${home}. Install it with: npm i playwright && npx playwright install chromium` };
  }
  // Run the copy we just installed, not `npx playwright`, which would download the CLI a second
  // time and can pick a different version than the library we are about to import.
  const bin = path.join(home, "node_modules", ".bin", process.platform === "win32" ? "playwright.cmd" : "playwright");
  const install = existsSync(bin)
    ? spawnSync(bin, ["install", "chromium"], { stdio: "inherit" })
    : spawnSync("npx", ["playwright", "install", "chromium"], { stdio: "inherit" });
  if (install.status !== 0) {
    log(C.r("Playwright installed but Chromium did not. Run: npx playwright install chromium"));
    return { pw: null, problem: "Playwright installed but Chromium did not. Run: npx playwright install chromium" };
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
async function report(run, log, onRun) {
  try {
    onRun?.(run);
  } catch {
    /* a caller's bookkeeping must not change a verdict */
  }
  const projectId = process.env.SMOLANALYTICS_PROJECT;
  const writeKey = process.env.SMOLANALYTICS_WRITE_KEY;
  if (!projectId || !writeKey) return;
  const base = (process.env.SMOLANALYTICS_URL || "https://smolanalytics.com").replace(/\/$/, "");
  try {
    const res = await fetch(`${base}/api/projects/${encodeURIComponent(projectId)}/runs`, {
      method: "POST",
      headers: { authorization: `Bearer ${writeKey}`, "content-type": "application/json" },
      body: JSON.stringify(run),
    });
    log(res.ok ? C.dim("  recorded to your project.") : C.dim(`  not recorded (${res.status}) — the verdict above still stands.`));
  } catch (e) {
    // A test tool that fails a build because its own telemetry could not be delivered gets removed
    // the same day. The verdict is already decided.
    log(C.dim(`  not recorded (${e && e.message}) — the verdict above still stands.`));
  }
}

export async function testCmd({ url, test, plan: planPath, headed, maxSteps = 40, yes, log = console.log, onRun, loadBrowser = loadPlaywright }) {
  if (!url || !test) {
    log(`
${C.b("npx smolanalytics test")} — one sentence, a real browser, a verdict. No account.

  --url <url>      where the test starts (staging, a deploy preview, anything reachable)
  --test "<text>"  what should work, in plain English
  --plan <file>    replay this recording first; only wake the agent if it no longer fits
  --headed         watch it happen
  --yes            install the browser without asking

  ${C.dim('npx smolanalytics test --url https://yourapp.com --test "the pricing page shows a monthly price"')}
`);
    return 1;
  }

  const { pw, problem } = await loadBrowser(log, yes);
  if (!pw) {
    // REPORTED, not just logged. A suite with no verdict for this test falls back to guessing why
    // (see noVerdictReason), and on a first CI run it guesses "ANTHROPIC_API_KEY is not set" —
    // sending someone to add a secret they already have, over a browser that never downloaded.
    await report({ test, status: "errored", mode: "agent", durationMs: 0, url, reason: `${problem || "The browser could not be started."} This is the test runner, not your application.` }, log, onRun);
    return 2;
  }

  const apiKey = process.env.ANTHROPIC_API_KEY;
  const model = process.env.SMOLANALYTICS_MODEL || "claude-opus-5";

  const started = Date.now();
  let browser = null;

  try {
    // INSIDE the try. `chromium.launch()` is the single most common way a browser run dies on a CI
    // runner — "Host system is missing dependencies to run browsers" — and thrown from out here it
    // went past this function's whole errored/exit-2 contract to the CLI's last-resort catch, which
    // exits 1. That is the code reserved for the customer's app being broken.
    browser = await pw.chromium.launch({ headless: !headed });
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
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
        log(C.dim(`replaying ${plan.steps.length} recorded steps (no model)…`));
        const r = await replay(page, plan);
        if (r.status === "passed") {
          log(`\n${C.g("PASS")}${C.dim(` — replayed ${r.steps} steps in ${(r.ms / 1000).toFixed(1)}s, no model calls.`)}`);
          await report({ test, status: "passed", mode: "replay", durationMs: r.ms, url, reason: "Replayed the recorded run; every step still worked." }, log, onRun);
          await browser.close();
          return 0;
        }
        log(`\n${C.y(stalenessNote(r))}\n`);
        await report({ test, status: "stale", mode: "replay", durationMs: r.ms, url, reason: stalenessNote(r) }, log, onRun);
      }
    }

    if (!apiKey) {
      log(`\n${C.y("The agent needs a Claude API key.")}`);
      log(C.dim("  export ANTHROPIC_API_KEY=sk-ant-…    then run this again"));
      log(C.dim("  Replaying a recording (--plan) needs no key at all."));
      await browser.close();
      return 2;
    }

    await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
    let snap = await perceive(page);
    const messages = [{ role: "user", content: `THE TEST:\n${test}\n\nStarting page:\n\n${render(snap)}` }];
    const steps = [];

    for (let n = 1; n <= maxSteps; n++) {
      const res = await think(messages, apiKey, model);
      // A refusal is not a test failure and must never be reported as one: that would tell somebody
      // their checkout is broken because a safety classifier declined.
      if (res.stop_reason === "refusal") throw new Error("the model declined to continue; this is not a verdict about the app under test");
      messages.push({ role: "assistant", content: res.content });

      const calls = (res.content || []).filter((b) => b.type === "tool_use");
      if (!calls.length) {
        // The model replied with prose instead of a tool call. That is THIS RUNNER misbehaving, not
        // the application, so it errors and exits 2. Reporting it as FAIL/1 told somebody their
        // checkout was broken because the model wandered off — the exact confusion the three
        // statuses exist to prevent.
        const ms = Date.now() - started;
        const why = "The agent stopped without calling finish, so nothing was observed. This is the test runner, not your application.";
        log(`\n${C.y("ERROR")} ${C.dim("· no verdict")}`);
        log(`${why}\n`);
        await report({ test, status: "errored", mode: "agent", durationMs: ms, url, reason: why }, log, onRun);
        await browser.close();
        return 2;
      }

      const results = [];
      for (const call of calls) {
        const a = toAction(call);
        if (a.kind === "finish") {
          const ms = Date.now() - started;
          log(`\n${a.passed ? C.g("PASS") : C.r("FAIL")} ${C.dim(`· ${steps.length} steps · ${(ms / 1000).toFixed(1)}s`)}`);
          log(`${a.why}\n`);
          await report({
            test, status: a.passed ? "passed" : "failed", mode: "agent", durationMs: ms, url, reason: a.why,
            steps: a.passed ? undefined : steps.map((s) => ({ n: s.n, do: s.label, why: s.action.why, ok: s.ok, detail: s.detail, ms: s.ms })),
          }, log, onRun);
          if (a.passed && planPath) {
            const p = compile(url, steps);
            if (p) {
              writeFileSync(planPath, JSON.stringify(p, null, 2) + "\n");
              log(C.dim(`recorded ${p.steps.length} replayable steps to ${planPath} — the next run needs no model.`));
            }
          }
          await browser.close();
          return a.passed ? 0 : 1;
        }

        const t0 = Date.now();
        const el = snap.elements.find((e) => e.ref === a.ref);
        const target = el ? { role: el.role, name: el.name } : undefined;
        const out = await act(page, snap, a);
        const step = { n, action: a, target, ok: out.ok, detail: out.detail, ms: Date.now() - t0 };
        step.label = describe(step).trim().replace(/^[^ ]+ +\d+ /, "");
        steps.push(step);
        log(describe(step));
        if (a.why) log(`     ${C.dim(a.why)}`);

        snap = await perceive(page);
        results.push({ type: "tool_result", tool_use_id: call.id, is_error: !out.ok, content: `${out.ok ? "done" : `FAILED: ${out.detail}`}\n\n${render(snap)}` });
      }
      messages.push({ role: "user", content: results });
    }

    // Out of budget is not a pass, and it is not a bug report either. An unfinished test observed
    // NOTHING, so `failed`/1 would put a red X on a pull request and a "the app did not do what the
    // sentence describes" next to a claim nobody made. The old copy even guessed the cause aloud
    // ("usually the app did not do what the test expected"), which is a finding we did not observe.
    // Our step budget is our limit: errored/2, which is never green and never blames their app.
    const ms = Date.now() - started;
    const why = `The agent used all ${maxSteps} steps without reaching a verdict, so nothing was observed. This is the test runner, not your application: raise --max-steps, or split a test that describes more than one scenario.`;
    log(`\n${C.y("ERROR")} ${C.dim(`· ${maxSteps} steps · ${(ms / 1000).toFixed(1)}s`)}`);
    log(`${why}\n`);
    await report({ test, status: "errored", mode: "agent", durationMs: ms, url, reason: why }, log, onRun);
    await browser.close();
    return 2;
  } catch (e) {
    // browser may be null: launch itself is what usually fails.
    await browser?.close().catch(() => {});
    log(C.r(`\nthe run could not complete: ${e && e.message ? e.message : e}`));
    await report({ test, status: "errored", mode: "agent", durationMs: Date.now() - started, url, reason: `The run could not complete: ${e && e.message}. This is the test runner, not your application.` }, log, onRun);
    // Exit 2, not 1: the test did not fail, the runner did. A CI gate must tell those apart, or an
    // outage on our side reads to a customer as their app being broken.
    return 2;
  }
}
