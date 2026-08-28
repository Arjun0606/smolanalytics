// THE FALSE-GREEN GUARD, AND THE ONLY RISK THAT DECIDES WHETHER IT SHIPS.
//
// The problem it exists for is real and measured: a replay's proof is page TEXT, so a build whose
// CSS 404'd, or that rendered blank, or that is showing a framework error overlay while the DOM
// text is intact, replays GREEN. That is the worst thing this product can do — a green tick on a
// visibly broken page — and it is why this was the top-ranked item in our own review.
//
// But the cure can be worse than the disease. This check is the only thing in the product that can
// turn a passing build red on evidence the customer did not ask us to judge. ONE false positive on
// a legitimately-rendered page destroys trust in every other verdict we produce, and a team that
// stops believing the suite is a team that has already churned.
//
// So the acceptance bar is not "it catches broken pages" — it is "it says NOTHING about pages that
// are fine". Most of this file is therefore pages that are perfectly healthy but look strange to a
// naive check: a canvas game with no DOM text, an image-only gallery, white-on-white print styling,
// content mid-fade, a dark theme, a cookie banner covering the viewport, a single-page app that
// paints late. Every one of them must produce zero findings.
//
// The module is deliberately NOT wired into the verdict path yet. These tests are what decides
// whether it earns that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { auditRender, renderFailure, renderNoteLines } from "../lib/render.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* every browser test below skips with a reason */
}
const noBrowser = { skip: chromium ? false : "playwright not installed" };

let server, base, body = "", browser;

async function serve() {
  if (server) return;
  server = createServer((req, res) => {
    if (req.url === "/missing.css") {
      res.writeHead(404, { "content-type": "text/plain" });
      res.end("not found");
      return;
    }
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(body);
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  base = `http://127.0.0.1:${server.address().port}/`;
}

async function open(html) {
  await serve();
  body = html;
  browser ??= await chromium.launch();
  const page = await browser.newPage();
  await page.goto(base, { waitUntil: "domcontentloaded" });
  return page;
}

test.after(async () => {
  await browser?.close();
  // closeAllConnections before close: close()'s callback never fires while a keep-alive is open,
  // and that has wedged this suite for ten minutes before.
  server?.closeAllConnections?.();
  await new Promise((r) => (server ? server.close(() => r()) : r()));
});

/** Run the audit against a page and return its findings. */
async function findingsFor(html) {
  const page = await open(html);
  try {
    return await auditRender(page);
  } finally {
    await page.close();
  }
}

/* ── PAGES THAT ARE FINE. Every one of these must produce nothing. ───────────────────────────── */

const HEALTHY = [
  ["an ordinary page", `<!doctype html><title>Shop</title><style>body{font-family:sans-serif;background:#fff;color:#111}</style>
<h1>Your cart</h1><p>2 items in your cart.</p><button>Checkout</button>`],

  ["a dark theme", `<!doctype html><title>App</title><style>body{background:#0b0b0d;color:#f2f0ec;font-family:sans-serif}</style>
<h1>Dashboard</h1><p>Everything is running.</p><button>Deploy</button>`],

  ["a canvas game with no DOM text at all", `<!doctype html><title>Game</title><style>body{margin:0;background:#111}canvas{display:block}</style>
<canvas id="c" width="800" height="600"></canvas>
<script>const x=document.getElementById('c').getContext('2d');x.fillStyle='#4af';x.fillRect(10,10,700,500);</script>`],

  ["an image-only gallery", `<!doctype html><title>Gallery</title><style>body{margin:0}img{width:400px;height:300px;display:block}</style>
<img alt="one" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='400' height='300'%3E%3Crect width='400' height='300' fill='%23888'/%3E%3C/svg%3E">`],

  ["an SVG infographic with no body text", `<!doctype html><title>Chart</title><style>body{margin:0;background:#fff}</style>
<svg width="600" height="400"><rect width="600" height="400" fill="#eee"/><circle cx="300" cy="200" r="120" fill="#39c"/></svg>`],

  ["a print view that is deliberately near-white on white", `<!doctype html><title>Invoice</title>
<style>body{background:#fff;color:#f7f7f7;font-family:serif}</style><h1>Invoice 4021</h1><p>Thank you for your order.</p>`],

  ["content mid fade-in", `<!doctype html><title>App</title>
<style>body{background:#fff}main{opacity:0;animation:f 2s forwards}@keyframes f{to{opacity:1}}</style>
<main><h1>Welcome back</h1><p>Loading your projects.</p></main>`],

  ["a cookie banner covering the viewport", `<!doctype html><title>Shop</title>
<style>body{background:#fff;font-family:sans-serif}#c{position:fixed;inset:0;background:rgba(0,0,0,.6)}
#d{position:fixed;left:20px;right:20px;bottom:20px;background:#fff;padding:24px}</style>
<h1>Our products</h1><p>Browse the catalogue.</p>
<div id="c"></div><div id="d"><p>We use cookies.</p><button>Accept</button></div>`],

  ["a single-page app that paints after a delay", `<!doctype html><title>App</title><style>body{background:#fff;font-family:sans-serif}</style>
<div id="root"></div>
<script>setTimeout(()=>{document.getElementById('root').innerHTML='<h1>Projects</h1><p>Three projects.</p>'},400)</script>`],

  ["one short line of text and nothing else", `<!doctype html><title>Status</title><style>body{background:#fff;font-family:sans-serif}</style><p>All systems normal.</p>`],
];

for (const [what, html] of HEALTHY) {
  test(`says nothing about ${what}`, noBrowser, async () => {
    const f = await findingsFor(html);
    assert.deepEqual(
      f, [],
      `a healthy page produced ${f.length} finding(s): ${JSON.stringify(f)}\n` +
      "One false positive on a page like this destroys trust in every other verdict.",
    );
  });
}

/* ── PAGES THAT ARE GENUINELY BROKEN. These are the whole point. ─────────────────────────────── */

test("a page that renders nothing is caught", noBrowser, async () => {
  // The shape a failed build actually takes: the document loads, the app never mounts.
  const f = await findingsFor(`<!doctype html><title>App</title><div id="root"></div>`);
  assert.ok(f.length > 0, "a blank page replayed green, which is the bug this exists to stop");
  assert.match(JSON.stringify(f), /blank|empty|nothing/i);
});

test("a framework error overlay is caught, and named", noBrowser, async () => {
  const f = await findingsFor(`<!doctype html><title>App</title>
<style>body{margin:0;font-family:monospace}#o{position:fixed;inset:0;background:#100;color:#f66;padding:40px}</style>
<div id="root"><h1>Checkout</h1></div>
<div id="o"><h2>Unhandled Runtime Error</h2><p>TypeError: Cannot read properties of undefined (reading 'total')</p></div>`);
  assert.ok(f.length > 0, "an error overlay over the app replayed green");
});

test("the reason names what was seen, not just that something was wrong", noBrowser, async () => {
  // A verdict a human cannot act on is barely better than no verdict.
  const f = await findingsFor(`<!doctype html><title>App</title><div id="root"></div>`);
  const reason = renderFailure(f);
  assert.ok(reason && reason.length > 20, `an unhelpful reason: ${reason}`);
  assert.ok(!/\bmaybe\b|\bpossibly\b/i.test(reason), `a guess is not a verdict: ${reason}`);
});

/* ── the contract that keeps it from doing harm ──────────────────────────────────────────────── */

test("disabled means it does not even look", noBrowser, async () => {
  // Proven by instrumenting the page rather than by reading the code: if `enabled:false` still
  // evaluated, a customer who switched it off would still pay its cost and its risk.
  const page = await open(`<!doctype html><title>App</title><div id="root"></div>`);
  let evaluated = 0;
  const spy = new Proxy(page, {
    get(t, k) {
      if (k === "evaluate") return (...a) => { evaluated++; return t.evaluate(...a); };
      return Reflect.get(t, k);
    },
  });
  const f = await auditRender(spy, { enabled: false });
  assert.deepEqual(f, []);
  assert.equal(evaluated, 0, "the page was inspected despite the check being switched off");
  await page.close();
});

test("a check that throws leaves the verdict alone", noBrowser, async () => {
  // A page with a Content-Security-Policy can refuse our evaluate. That is our problem, and it must
  // never become a finding about the customer's app.
  const broken = { evaluate: async () => { throw new Error("CSP"); } };
  assert.deepEqual(await auditRender(broken), []);
});

test("no findings means no note and no failure", () => {
  assert.deepEqual(renderNoteLines([]), []);
  assert.equal(renderFailure([]), "");
});

test("the note is capped, and says what it dropped rather than truncating silently", () => {
  // A wall of findings is not read; a silent truncation reads as "that was all of them".
  const many = Array.from({ length: 12 }, (_, i) => ({ kind: "blank", detail: `finding ${i}` }));
  const lines = renderNoteLines(many, 3);
  assert.equal(lines.length, 4, "three findings plus the line that accounts for the rest");
  assert.match(lines[3], /9 more/);
  assert.deepEqual(renderNoteLines([{ kind: "blank", detail: "only one" }], 3).length, 1);
});
