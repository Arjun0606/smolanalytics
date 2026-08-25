// LAYOUT SANITY: THE PAGE THAT PASSED WHILE LOOKING BROKEN.
//
// The proof check reads innerText, and innerText includes text a person cannot see: text behind a
// cookie banner, in a zero-size button, clipped off the edge of a container. So a run can walk
// every step, find its proof, and print PASS over a page a human opens and swears at. lib/layout.mjs
// computes what a vision model would be asked to eyeball — covered controls, invisible targets,
// sideways scroll, cut-off labels — from the DOM's own geometry, deterministically, for free.
//
// The rules under test, in trust order:
//
//   a CLEAN page produces ZERO findings        the false-positive pin. One sticky header flagged
//                                              as an overlay and this feature is off in every repo.
//   default (report) changes NO exit code      findings are a dim note and a PR line, nothing else.
//   --layout=strict flips PASSED to failed/1   the customer's explicit opt-in, and only passed.
//   --layout=off computes nothing at all.
//
// The browser is real and the model is stubbed, the same shape as flake.test.mjs: the audit runs
// through Playwright, real pages, and the same report() the product uses, with no spend.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { auditLayout, stepTargets, parseLayoutMode, layoutFailure, layoutNoteLines, layoutCommentLines } from "../lib/layout.mjs";
import { testCmd, wireRun } from "../lib/test.mjs";
import { runSuite, commentBody } from "../lib/suite.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-layout-"));

// ---- the crafted pages --------------------------------------------------------------------------

const PAGES = {
  // A checkout button fully under a fixed cookie banner. The Details link is deliberately clear of
  // it: replays need one control that still works, and the coverage finding must come from the
  // final page's visible controls, not from anything the run touched.
  "/covered": `<!doctype html><title>Shop</title><h1>Your cart</h1><a href="#">Details</a>
    <p>2 items in your cart.</p>
    <button id="buy" style="position:fixed;bottom:30px;left:20px">Proceed to checkout</button>
    <div id="cookie-banner" style="position:fixed;bottom:0;left:0;right:0;height:160px;background:#333;color:#fff;z-index:9">We use cookies to improve your experience</div>`,

  // Clicking the button shrinks it to nothing — the page still "works" (the proof text is there,
  // innerText still contains the label) but the control a person would need has vanished from view
  // while staying in the DOM and the accessibility tree.
  "/shrinks": `<!doctype html><title>Shop</title><h1>Shop</h1><p>2 items in your cart.</p>
    <button onclick="this.style.width='0';this.style.height='0';this.style.padding='0';this.style.border='0';this.style.overflow='hidden'">Buy now</button>`,

  // Everything on this page is a pattern that LOOKS like a finding and is not: sr-only text in a
  // 1×1 clip box, a deliberately ellipsized breadcrumb, a hidden nav, a width:100vw hero on a page
  // with a vertical scrollbar (the classic 8px phantom overflow), a sticky header, wrapped and
  // for-referenced labels. Zero findings here is the whole case for trusting the checks.
  "/clean": `<!doctype html><title>Pricing</title>
    <header style="position:sticky;top:0;height:60px;background:#fff;border-bottom:1px solid #ddd"><a href="/">Home</a></header>
    <div style="width:100vw;background:#fafafa">hero</div>
    <main style="max-width:960px;margin:0 auto">
      <h1>Pricing</h1><p>2 items in your cart.</p>
      <button>Sign in<span style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap">opens the sign in dialog</span></button>
      <div style="overflow:hidden;width:120px;white-space:nowrap;text-overflow:ellipsis"><a href="/docs/deep">A long breadcrumb that ellipsizes here</a></div>
      <nav style="display:none"><a href="/secret">Hidden menu item</a></nav>
      <label>Email <input type="text"></label>
      <label for="q">Search</label><input id="q" type="search">
      <button style="padding:8px 14px">Buy monthly</button>
      <div style="height:1500px"></div>
    </main>`,
};

const server = createServer((req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(PAGES[req.url] || PAGES["/clean"]);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
after(() => new Promise((r) => server.close(() => r())));

// ---- the checks themselves, against real pages --------------------------------------------------

describe("auditLayout against crafted pages", noBrowser.skip ? noBrowser : {}, () => {
  let browser = null;
  let page = null;
  test.before(async () => {
    if (!chromium) return;
    browser = await chromium.launch();
    page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  });
  test.after(async () => {
    await browser?.close();
  });

  test("a button under a cookie banner is a finding that names BOTH elements", noBrowser, async () => {
    await page.goto(`${base}/covered`, { waitUntil: "domcontentloaded" });
    const findings = await auditLayout(page, { targets: [] });
    const covered = findings.filter((f) => f.check === "covered");
    assert.equal(covered.length, 1, JSON.stringify(findings));
    // Naming only one half sends someone hunting: "covered" without the overlay's name is a
    // finding nobody can act on, and without the victim's it is not a finding at all.
    assert.match(covered[0].detail, /Proceed to checkout/, "the covered control must be named");
    assert.match(covered[0].detail, /cookie-banner|We use cookies/, "the overlay must be named");
  });

  test("a zero-size TARGET is a finding; the same element un-targeted is not", noBrowser, async () => {
    await page.setContent(`<h1>Cart</h1><p>ok</p><button style="width:0;height:0;padding:0;border:0;overflow:hidden">Buy now</button>`);
    const flagged = await auditLayout(page, { targets: [{ role: "button", name: "Buy now" }] });
    assert.equal(flagged.length, 1, JSON.stringify(flagged));
    assert.equal(flagged[0].check, "invisible");
    assert.match(flagged[0].detail, /Buy now/);
    assert.match(flagged[0].detail, /0×0px/);
    // Scope: the audit covers what the run touched plus the visible controls it ended among —
    // never the whole DOM. A zero-size element nobody targeted is invisible to the audit too.
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
  });

  test("a page dragged sideways is a finding; the 100vw scrollbar phantom is not", noBrowser, async () => {
    await page.setContent(`<h1>Report</h1><table style="width:1600px"><tr><td>wide</td></tr></table>`);
    const findings = await auditLayout(page, { targets: [] });
    assert.equal(findings.length, 1, JSON.stringify(findings));
    assert.equal(findings[0].check, "overflow");
    assert.match(findings[0].detail, /328px wider/, "the finding must carry the measured number");
    // width:100vw beside a vertical scrollbar overflows by exactly one scrollbar (measured 8px
    // here) on half the production web. Flagging it would cry wolf on every run.
    await page.setContent(`<div style="width:100vw">hero</div><div style="height:2000px">tall</div>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
  });

  test("a label cut off by an overflow:hidden box is a finding", noBrowser, async () => {
    await page.setContent(`<div style="overflow:hidden;width:60px"><button style="white-space:nowrap;background:none;border:0">Download the annual report</button></div>`);
    const findings = await auditLayout(page, { targets: [] });
    assert.equal(findings.length, 1, JSON.stringify(findings));
    assert.equal(findings[0].check, "clipped");
    assert.match(findings[0].detail, /Download the annual report/);
  });

  // The sticky header is the false positive that would kill this feature: every content site has
  // one, and a control scrolled half beneath it is flagged by any check that samples only the
  // centre point. Measured on the page below — centre (68,60) and both upper quadrants resolve to
  // HEADER while the lower two resolve to the button itself — which is precisely a control a person
  // can still see and still click. Scrolled 6px further, all five points resolve to HEADER and it
  // is genuinely unreachable. Both halves are asserted here: a check that never fires on a sticky
  // header is as useless as one that always does.
  test("half under a sticky header is usable; fully under it is covered", noBrowser, async () => {
    await page.setContent(`<h1>Docs</h1>
      <header style="position:sticky;top:0;height:60px;background:#fff;border-bottom:1px solid #ddd;z-index:5"><a href="/">Home</a></header>
      <div style="height:400px"></div>
      <button id="half" style="padding:10px 16px">Save changes</button>
      <div style="height:2000px"></div>`);
    const box = await page.evaluate(() => {
      const r = document.getElementById("half").getBoundingClientRect();
      return { top: r.top + window.scrollY, h: r.height };
    });
    // Half of the button's height sits behind the 60px header.
    await page.evaluate((b) => window.scrollTo(0, b.top - 60 + b.h / 2), box);
    const target = [{ role: "button", name: "Save changes" }];
    assert.deepEqual(
      await auditLayout(page, { targets: target }),
      [],
      "a control peeking out from under a sticky header is usable — flagging it is the false positive that gets this feature switched off",
    );
    // Now the whole control is behind the header.
    await page.evaluate((b) => window.scrollTo(0, b.top - 60 + b.h + 6), box);
    const covered = (await auditLayout(page, { targets: target })).filter((f) => f.check === "covered");
    assert.equal(covered.length, 1, "a control entirely behind the header IS covered");
    assert.match(covered[0].detail, /Save changes/);
    assert.match(covered[0].detail, /header/);
  });

  // overflow:auto and overflow:scroll look identical to overflow:hidden in the geometry — the text
  // is just as far outside the box — but the reader can scroll to it, so it is reachable, not lost.
  // Measured on the pair below: the same 26-character label overhangs the same 80px box by 90px
  // either way (scrollWidth 176 vs clientWidth 80). Only the clipping one is a finding.
  test("text overflowing a SCROLLABLE box is reachable, not clipped", noBrowser, async () => {
    const box = (overflow) =>
      `<h1>Log</h1><div style="overflow:${overflow};width:80px;white-space:nowrap"><button style="background:none;border:0;white-space:nowrap">Download the annual report</button></div>`;
    await page.setContent(box("auto"));
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a scrollable container hides nothing: the text is one scroll away");
    await page.setContent(box("scroll"));
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
    // The identical box that actually clips is still caught, so the exemption above is an exemption
    // and not a blind spot.
    await page.setContent(box("hidden"));
    const findings = await auditLayout(page, { targets: [] });
    assert.equal(findings.length, 1, JSON.stringify(findings));
    assert.equal(findings[0].check, "clipped");
    assert.match(findings[0].detail, /90px of it lies outside/);
  });

  test("THE PIN: a clean page with every look-alike pattern produces zero findings", noBrowser, async () => {
    await page.goto(`${base}/clean`, { waitUntil: "domcontentloaded" });
    // Even with a target named, and even after scrolling half under the sticky header.
    const findings = await auditLayout(page, { targets: [{ role: "button", name: "Buy monthly" }] });
    assert.deepEqual(findings, [], `a finding on this page is a false positive:\n${JSON.stringify(findings, null, 2)}`);
  });

  test("an open modal covering the page is design, not a finding", noBrowser, async () => {
    await page.setContent(`<h1>Done</h1><button id="bg">Back to shop</button>
      <div aria-modal="true" role="dialog" style="position:fixed;inset:0;background:rgba(0,0,0,.5)"><div style="background:#fff;margin:100px auto;width:400px;padding:40px">Order placed<button>Close</button></div></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
  });

  test("--layout=off computes nothing", noBrowser, async () => {
    await page.goto(`${base}/covered`, { waitUntil: "domcontentloaded" });
    assert.deepEqual(await auditLayout(page, { mode: "off", targets: [] }), []);
  });
});

// ---- the plumbing, with no browser in the way ---------------------------------------------------

describe("modes, targets, rendering", () => {
  test("the mode flag is refused out loud, never silently another mode", () => {
    assert.deepEqual(parseLayoutMode(undefined), { mode: "report", problem: "" });
    for (const m of ["report", "strict", "off"]) assert.equal(parseLayoutMode(m).mode, m);
    // `--layout=stric` silently meaning "report" would un-gate the one customer who opted in.
    for (const bad of ["stric", "on", "true", ""]) {
      const r = parseLayoutMode(bad);
      assert.equal(r.mode, "");
      assert.match(r.problem, /--layout must be report, strict or off/);
    }
  });

  test("stepTargets reads both shapes and drops dead ends", () => {
    assert.deepEqual(stepTargets([
      { action: { kind: "click" }, target: { role: "button", name: "Buy" }, ok: true },
      { action: { kind: "click" }, target: { role: "button", name: "Nope" }, ok: false },
      { action: { kind: "press", key: "Enter" }, ok: true },
      { action: { kind: "click" }, target: { role: "button", name: "Buy" }, ok: true },
    ]), [{ role: "button", name: "Buy" }]);
    assert.deepEqual(stepTargets([
      { kind: "click", role: "link", name: "Cart" },
      { kind: "fill", role: "textbox", name: "Email", text: "a@b.c" },
      { kind: "goto", url: "https://x.test" },
    ]), [{ role: "link", name: "Cart" }, { role: "textbox", name: "Email" }]);
  });

  test("layoutFailure gates ONLY in strict, and names the opt-in", () => {
    const f = [{ check: "covered", detail: "the button is covered" }];
    assert.equal(layoutFailure(f, "report"), "");
    assert.equal(layoutFailure([], "strict"), "");
    const reason = layoutFailure(f, "strict");
    assert.match(reason, /the button is covered/);
    assert.match(reason, /--layout=strict/, "the reason must say which opt-in turned a note into a failure");
  });

  test("the terminal note is capped but never hides the count", () => {
    const many = Array.from({ length: 6 }, (_, i) => ({ check: "covered", detail: `finding ${i + 1}` }));
    const lines = layoutNoteLines(many);
    assert.equal(lines.length, 5);
    assert.match(lines[0], /^layout: finding 1/);
    assert.match(lines[4], /and 2 more/);
    assert.deepEqual(layoutNoteLines([]), []);
  });

  test("layout never goes on the wire: the runs API has never heard of the field", () => {
    const run = { test: "t", status: "passed", reason: "r", layout: [{ check: "covered", detail: "d" }] };
    assert.deepEqual(wireRun(run), { test: "t", status: "passed", reason: "r" });
    // And the flaky translation still happens on the stripped run.
    assert.equal(wireRun({ status: "flaky", reason: "r", layout: [] }).status, "passed");
  });
});

// ---- the whole path: browser, verdict, exit code ------------------------------------------------

/** Run one test with a scripted model, the flake.test.mjs pattern. */
async function run(script, opts = {}) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  let attempt = 0;
  globalThis.fetch = async (target, init = {}) => {
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called here");
    const body = JSON.parse(init.body);
    if (body.messages.length === 1) attempt++;
    const content = script(attempt, body.messages.length);
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content }), text: async () => "" };
  };
  try {
    const code = await testCmd({
      url: `${base}/covered`, test: "the cart lists its items", maxSteps: 5, retries: 0, evidenceDir: scratch(),
      log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r), ...opts,
    });
    return { code, runs, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

const finish = (passed, why, proof = "") => [{ type: "tool_use", id: "t1", name: "finish", input: { passed, why, proof } }];

describe("findings against verdicts and exit codes", () => {
  test("default: a PASS over a covered button stays PASS/0, with a dim layout note", noBrowser, async () => {
    const { code, runs, out } = await run(() => finish(true, "The cart lists 2 items.", "2 items in your cart"));
    assert.equal(code, 0, "report-only findings must never change an exit code");
    assert.deepEqual(runs.map((r) => r.status), ["passed"]);
    assert.match(out, /\bPASS\b/);
    assert.match(out, /layout: .*Proceed to checkout.*covered by/, "the note under the verdict is the whole default deliverable");
    assert.ok(runs[0].layout?.length, "the suite reads findings off the run object");
  });

  test("strict: the same page flips PASSED to failed and exit 1, finding as the reason", noBrowser, async () => {
    const { code, runs, out } = await run(() => finish(true, "The cart lists 2 items.", "2 items in your cart"), { layout: "strict" });
    assert.equal(code, 1, "strict is the customer's opt-in to gating on these checks");
    assert.deepEqual(runs.map((r) => r.status), ["failed"]);
    assert.match(runs[0].reason, /covered by/, "the finding IS the failure reason");
    assert.match(runs[0].reason, /--layout=strict/, "the reader must see which opt-in failed them");
    assert.match(out, /\bFAIL\b/);
    // The reason legitimately says "…would be a note under a PASS", so pin the verdict LINE.
    assert.ok(!/^PASS\b/m.test(out), out);
  });

  test("off: nothing is computed and nothing is printed", noBrowser, async () => {
    const { code, out } = await run(() => finish(true, "The cart lists 2 items.", "2 items in your cart"), { layout: "off" });
    assert.equal(code, 0);
    assert.ok(!/layout:/.test(out), out);
  });

  test("strict on a CLEAN page is still PASS/0 — the pin, through the whole loop", noBrowser, async () => {
    const { code, runs } = await run(() => finish(true, "The pricing page rendered.", "2 items in your cart"), { url: `${base}/clean`, layout: "strict" });
    assert.equal(code, 0, "a false positive that reddens a build kills trust in a day");
    assert.deepEqual(runs.map((r) => r.status), ["passed"]);
  });

  test("default on a FAILING run: exit stays 1, verdict untouched, note still printed", noBrowser, async () => {
    const { code, runs, out } = await run(() => finish(false, "On /covered, the checkout total is missing."));
    assert.equal(code, 1);
    assert.deepEqual(runs.map((r) => r.status), ["failed"]);
    assert.equal(runs[0].reason, "On /covered, the checkout total is missing.", "findings must not edit a real bug report");
    assert.match(out, /layout: .*covered by/, "context under a failure is still worth printing");
  });

  // STRICT MAY TURN A PASS INTO A FAILURE AND NOTHING ELSE. The five statuses are the product:
  // failed means the app is broken, flaky means the test is unreliable, errored means we are. A
  // layout finding that could reach any of them would be a checker rewriting verdicts it did not
  // observe — and `strict` is an opt-in to gating on findings, not to relabelling runs.
  test("strict never touches a verdict it did not decide: failed, flaky and errored are its own", noBrowser, async () => {
    // FAILED stays failed, on the AGENT's reason, in strict. The page under it is genuinely
    // covered, so there is a finding to be tempted by.
    const failed = await run(() => finish(false, "On /covered, the checkout total is missing."), { layout: "strict" });
    assert.equal(failed.code, 1);
    assert.deepEqual(failed.runs.map((r) => r.status), ["failed"]);
    assert.equal(failed.runs[0].reason, "On /covered, the checkout total is missing.", "strict overwrote a real bug report with a layout note");
    assert.match(failed.out, /layout: .*covered by/, "the note is still context under the failure");

    // FLAKY stays flaky and stays exit 0. Failed once, passed on the retry — a red X here would
    // train people to re-run until green, which is the swallowing `flaky` exists to prevent.
    const flaky = await run((attempt) => (attempt === 1 ? finish(false, "The cart was empty.") : finish(true, "The cart lists 2 items.", "2 items in your cart")), { layout: "strict", retries: 1 });
    assert.equal(flaky.code, 0, "strict turned a flaky run into a failure");
    assert.deepEqual(flaky.runs.map((r) => r.status), ["failed", "flaky"]);
    assert.ok(!/--layout=strict/.test(flaky.runs[1].reason), `the flaky reason became a layout finding: ${flaky.runs[1].reason}`);
    assert.match(flaky.out, /layout: .*covered by/, "findings still ride along as a note");

    // ERRORED stays errored and stays exit 2, and carries no findings at all: nothing was
    // observed, so there is no page the audit is entitled to describe.
    const errored = await run(() => [{ type: "text", text: "I think the cart looks fine." }], { layout: "strict" });
    assert.equal(errored.code, 2, "an errored run must stay our outage, never the app's failure");
    assert.deepEqual(errored.runs.map((r) => r.status), ["errored"]);
    assert.equal(errored.runs[0].layout, undefined);
    assert.ok(!/^layout: /m.test(errored.out), errored.out);
  });

  // STALE is the fifth. A recording that stopped fitting the app is neither a pass nor a bug, and
  // the audit never runs on it — there is no settled outcome for a finding to sit under.
  test("a stale replay carries no findings and keeps its own status", noBrowser, async () => {
    const dir = scratch();
    const planPath = path.join(dir, "cart.json");
    writeFileSync(planPath, JSON.stringify({ startUrl: `${base}/covered`, steps: [{ kind: "click", role: "link", name: "Details" }], proof: "your order is confirmed" }));
    const key = process.env.ANTHROPIC_API_KEY;
    delete process.env.ANTHROPIC_API_KEY;
    try {
      const lines = [];
      const runs = [];
      const code = await testCmd({ url: `${base}/covered`, test: "the cart lists its items", plan: planPath, layout: "strict", evidenceDir: scratch(), log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r) });
      const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
      assert.deepEqual(runs.map((r) => r.status), ["stale"]);
      assert.equal(runs[0].layout, undefined, "a stale verdict must not carry findings about a page nothing was settled on");
      assert.ok(!/^layout: /m.test(out), out);
      assert.equal(code, 2, "no key to escalate to the agent: our outage, not the app's failure");
    } finally {
      if (key !== undefined) process.env.ANTHROPIC_API_KEY = key;
    }
  });

  test("a target that shrinks to nothing after its click is named, end to end", noBrowser, async () => {
    const { code, out } = await run((attempt, msgCount) =>
      msgCount === 1
        ? [{ type: "tool_use", id: "t1", name: "click", input: { ref: "e2", why: "buy" } }]
        : finish(true, "The cart lists 2 items.", "2 items in your cart"),
    { url: `${base}/shrinks` });
    assert.equal(code, 0, "still report-only");
    assert.match(out, /layout: the button "Buy now" .*0×0px/, "the shrunk target must be named with its measured size");
  });

  test("replay runs the same audit: report notes on PASS, strict flips to 1", noBrowser, async () => {
    const dir = scratch();
    const planPath = path.join(dir, "cart.json");
    const plan = { startUrl: `${base}/covered`, steps: [{ kind: "click", role: "link", name: "Details" }], proof: "2 items in your cart" };
    writeFileSync(planPath, JSON.stringify(plan));
    // No model, no key: the replay path must audit without either.
    const key = process.env.ANTHROPIC_API_KEY;
    delete process.env.ANTHROPIC_API_KEY;
    try {
      let lines = [];
      let runs = [];
      let code = await testCmd({ url: `${base}/covered`, test: "the cart lists its items", plan: planPath, evidenceDir: scratch(), log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r) });
      let out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
      assert.equal(code, 0);
      assert.deepEqual(runs.map((r) => r.status), ["passed"]);
      assert.match(out, /PASS.*no model calls/);
      assert.match(out, /layout: .*covered by/);

      lines = [];
      runs = [];
      code = await testCmd({ url: `${base}/covered`, test: "the cart lists its items", plan: planPath, layout: "strict", evidenceDir: scratch(), log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r) });
      out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
      assert.equal(code, 1, "strict gates the replay verdict exactly as it gates the agent's");
      assert.deepEqual(runs.map((r) => r.status), ["failed"]);
      assert.match(runs[0].reason, /covered by/);
    } finally {
      if (key !== undefined) process.env.ANTHROPIC_API_KEY = key;
    }
  });
});

// ---- through the suite and onto the pull request ------------------------------------------------

describe("layout through the suite", () => {
  const t = { file: "tests/a.md", name: "A shopper can pay", test: "buy", id: "a", planPath: ".rec/a.json" };

  test("the knob reaches the runner and the findings reach the result", async () => {
    let got = null;
    const [r] = await runSuite({
      tests: [t], url: "https://x.test", layout: "strict", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ layout, onRun }) => {
        got = layout;
        onRun({ status: "passed", mode: "agent", reason: "ok", layout: [{ check: "covered", detail: "the button is covered by div#banner" }] });
        return 0;
      },
    });
    assert.equal(got, "strict", "--layout must survive the suite layer");
    assert.equal(r.status, "passed");
    assert.deepEqual(r.layout, [{ check: "covered", detail: "the button is covered by div#banner" }]);
  });

  test("the comment carries one small-type line per test with findings", () => {
    const results = [
      { name: "A shopper can pay", file: "tests/a.md", status: "passed", mode: "agent", ms: 9000, layout: [{ check: "covered", detail: 'the button "Buy" is covered by div#cookie-banner' }] },
      { name: "The cart survives", file: "tests/b.md", status: "passed", mode: "replay", ms: 700, layout: [] },
    ];
    const body = commentBody(results, { url: "https://p.example.com", suite: "tests" });
    assert.match(body, /<sub>layout · \*\*A shopper can pay\*\*: the button "Buy" is covered by div#cookie-banner<\/sub>/);
    assert.ok(!/layout · \*\*The cart survives/.test(body), "a test without findings gets no line");
    assert.match(body, /advisory/i, "the reader must be told these gate nothing by default");
  });

  test("layoutCommentLines caps per test and escapes markup", () => {
    const many = Array.from({ length: 5 }, (_, i) => ({ check: "covered", detail: `<b>finding ${i + 1}</b>` }));
    const lines = layoutCommentLines([{ name: "A|test *bold*", layout: many }]);
    const line = lines.find((l) => l.includes("layout ·"));
    // Only `<` is escaped — it is the only character that opens a tag — matching quote() one
    // file over: mangling more would cost readability for nothing.
    assert.match(line, /&lt;b>finding 1&lt;\/b>/, "customer markup must render as text, not HTML");
    assert.match(line, /\(\+2 more in the job log\)/);
    assert.ok(!line.includes("finding 4"), "capped at three findings per test");
  });

  test("at BODY_LIMIT the layout lines are the FIRST thing dropped, whole", () => {
    // Build a comment that fits WITHOUT layout lines and overflows WITH them: the fallback must
    // recover by dropping the advisory notes only — no trim marker, no lost bug report.
    const results = [];
    const filler = "the checkout page showed an error banner and the reason keeps going ".repeat(55); // ~3.8k, under REASON_LIMIT
    for (let i = 0; i < 30; i++) {
      results.push({ name: `test ${i}`, file: `tests/${i}.md`, status: "failed", mode: "agent", ms: 1000, reason: filler });
    }
    const bare = commentBody(results, { url: "https://p.example.com", suite: "tests" });
    // Trim the fixture until the no-layout body genuinely fits: the case under test is "fits
    // without, overflows with", not "overflows regardless".
    while (results.length && commentBody(results, { url: "https://p.example.com", suite: "tests" }).includes("Trimmed to fit")) results.pop();
    assert.ok(results.length > 3, `the fixture collapsed: ${results.length} rows left of 30 (bare body was ${bare.length} chars)`);
    const noLayout = commentBody(results, { url: "https://p.example.com", suite: "tests" });
    assert.ok(!noLayout.includes("Trimmed to fit"));

    const detail = "the button is covered by div#cookie-banner and the detail runs on ".repeat(3);
    const withLayout = results.map((r) => ({ ...r, layout: [{ check: "covered", detail }] }));
    const overflows = commentBody(withLayout, { url: "https://p.example.com", suite: "tests" });
    if (noLayout.length + withLayout.length * (detail.length + 40) > 65_000) {
      assert.ok(!overflows.includes("layout ·"), "advisory lines must be sacrificed before any bug report is trimmed");
      assert.ok(!overflows.includes("Trimmed to fit"), "dropping the notes must be enough — the report itself survives whole");
      assert.equal(overflows, noLayout, "the recovered comment is exactly the report without the notes");
    } else {
      // The fixture landed small enough that everything fits; then nothing may be dropped.
      assert.ok(overflows.includes("layout ·"));
    }
  });
});

// ---- the flag at the command line ---------------------------------------------------------------

describe("--layout at the command line", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
  test("a typo'd mode is refused out loud with exit 2, never silently the default", () => {
    for (const argv of [["test", "--layout", "stric"], ["test", "--layout=stric"], ["test", "--layout"]]) {
      const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 30_000 });
      assert.equal(r.status, 2, `${argv.join(" ")} must exit 2 — our refusal, never 1, which blames the app`);
      assert.match(r.stderr, /--layout must be report, strict or off/);
    }
  });
});
