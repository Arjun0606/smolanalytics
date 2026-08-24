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
import { existsSync, readFileSync, writeFileSync } from "node:fs";

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

async function perceive(page) {
  const [aria, title, text] = await Promise.all([
    page.ariaSnapshot().catch(() => ""),
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

async function loadPlaywright(log, yes) {
  try {
    return await import("playwright");
  } catch {
    /* not installed yet */
  }
  log("");
  log(C.b("This command drives a real browser, which needs Playwright (~50MB) and Chromium."));
  log(C.dim("Every other command here has zero dependencies, so it is fetched only now, only once."));
  if (!yes && process.stdin.isTTY) {
    log(C.dim("Re-run with --yes to install without asking."));
    return null;
  }
  log(C.dim("installing…"));
  const r = spawnSync("npm", ["install", "--no-save", "--silent", "playwright"], { stdio: "inherit" });
  if (r.status !== 0) {
    log(C.r("could not install Playwright. Install it yourself with: npm i playwright && npx playwright install chromium"));
    return null;
  }
  spawnSync("npx", ["playwright", "install", "chromium"], { stdio: "inherit" });
  try {
    return await import("playwright");
  } catch (e) {
    log(C.r(`Playwright installed but could not be loaded: ${e && e.message}`));
    return null;
  }
}

/** Post the verdict, if a project is configured. Never throws, never affects the exit code. */
async function report(run, log) {
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

export async function testCmd({ url, test, plan: planPath, headed, maxSteps = 40, yes, log = console.log }) {
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

  const pw = await loadPlaywright(log, yes);
  if (!pw) return 2;

  const apiKey = process.env.ANTHROPIC_API_KEY;
  const model = process.env.SMOLANALYTICS_MODEL || "claude-opus-5";

  const browser = await pw.chromium.launch({ headless: !headed });
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const started = Date.now();

  try {
    // REPLAY FIRST — the path almost every real run takes, and it calls no model at all.
    if (planPath && existsSync(planPath)) {
      const plan = JSON.parse(readFileSync(planPath, "utf8"));
      log(C.dim(`replaying ${plan.steps.length} recorded steps (no model)…`));
      const r = await replay(page, plan);
      if (r.status === "passed") {
        log(`\n${C.g("PASS")}${C.dim(` — replayed ${r.steps} steps in ${(r.ms / 1000).toFixed(1)}s, no model calls.`)}`);
        await report({ test, status: "passed", mode: "replay", durationMs: r.ms, url, reason: "Replayed the recorded run; every step still worked." }, log);
        await browser.close();
        return 0;
      }
      log(`\n${C.y(stalenessNote(r))}\n`);
      await report({ test, status: "stale", mode: "replay", durationMs: r.ms, url, reason: stalenessNote(r) }, log);
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
        log(`\n${C.r("FAIL")} ${C.dim("· no verdict")}`);
        log("The run ended without the agent calling finish, so nothing was observed.\n");
        await browser.close();
        return 1;
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
          }, log);
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

    // Out of budget is explicitly NOT a pass: an unfinished test observed nothing.
    const ms = Date.now() - started;
    log(`\n${C.r("FAIL")} ${C.dim(`· ${maxSteps} steps · ${(ms / 1000).toFixed(1)}s`)}`);
    log(`The test did not reach a verdict within ${maxSteps} steps. Usually the app did not do what the test expected and the agent kept looking, or the test describes more than one scenario and should be split.\n`);
    await report({ test, status: "failed", mode: "agent", durationMs: ms, url, reason: `No verdict within ${maxSteps} steps.` }, log);
    await browser.close();
    return 1;
  } catch (e) {
    await browser.close().catch(() => {});
    log(C.r(`\nthe run could not complete: ${e && e.message ? e.message : e}`));
    await report({ test, status: "errored", mode: "agent", durationMs: Date.now() - started, url, reason: `The run could not complete: ${e && e.message}. This is the test runner, not your application.` }, log);
    // Exit 2, not 1: the test did not fail, the runner did. A CI gate must tell those apart, or an
    // outage on our side reads to a customer as their app being broken.
    return 2;
  }
}
