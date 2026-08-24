import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { compile, flatten, replay, stalenessNote } from "../lib/test.mjs";

// THESE DRIVE A REAL BROWSER, ON PURPOSE.
//
// Two claims hold this product up, and neither can be checked by reading the code:
//
//   1. A page can be read and acted on through its accessibility tree, with no vision model. If
//      that is false, the entire cost and latency argument collapses.
//   2. A passing run replays with ZERO model calls, and a page that has changed produces a
//      staleness report rather than a bug report.
//
// There is no mocked page here. A fake browser agrees with whatever you assumed about Playwright's
// API, which is exactly the mistake this caught once already: the design was written against
// `page.accessibility.snapshot()`, which this version of Playwright does not have. Only running it
// found that.
//
// PLAYWRIGHT IS OPTIONAL. The CLI has zero dependencies and fetches the browser only when someone
// runs `test`. So these skip cleanly when it is absent rather than failing a contributor's `npm
// test` for a dependency they never asked for — and they say they skipped, because a silent skip
// is a test suite quietly getting smaller.

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* not installed; every test below skips with a reason */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const APP = `<!doctype html><meta charset="utf-8"><title>Shop</title>
<h1>Your cart</h1>
<p id="msg">2 items in your cart.</p>
<label>Discount code <input type="text" name="code"></label>
<button id="apply">Apply code</button>
<button id="checkout">Proceed to checkout</button>
<button disabled>Save for later</button>
<a href="#help">Need help?</a>
<script>
  document.getElementById('apply').onclick = () => {
    document.getElementById('msg').textContent = 'Discount applied: 10% off.';
  };
  document.getElementById('checkout').onclick = () => {
    document.getElementById('msg').textContent = 'Order placed. Your order number is A-1042.';
  };
</script>`;

// The app is SERVED rather than injected with setContent, because replay() begins by navigating to
// the plan's start URL — correct behaviour, and it silently erases a setContent document. Testing
// against a real URL is both the fix and closer to how this is used.
let server, base, body = APP, browser;

async function serve() {
  if (server) return;
  server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(body);
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  base = `http://127.0.0.1:${server.address().port}/`;
}

async function open(html = APP) {
  await serve();
  body = html;
  browser ??= await chromium.launch();
  const page = await browser.newPage();
  await page.goto(base, { waitUntil: "domcontentloaded" });
  return page;
}

test.after(async () => {
  await browser?.close();
  await new Promise((r) => (server ? server.close(() => r()) : r()));
});

test("the page is legible without a screenshot", noBrowser, async () => {
  const page = await open();
  const { elements } = flatten(await page.locator("body").ariaSnapshot());
  const by = (n) => elements.find((e) => e.name === n);

  assert.equal(by("Proceed to checkout")?.role, "button");
  assert.equal(by("Discount code")?.role, "textbox");
  assert.equal(by("Need help?")?.role, "link");
  // The state that changes what an action MEANS is carried. Without it a click on this hangs for
  // ten seconds and fails with a timeout that reads like a broken app.
  assert.match(by("Save for later")?.state ?? "", /disabled/);
  await page.close();
});

test("prose is not offered as a control", noBrowser, async () => {
  // `- text:` lines in the snapshot are prose. Listing them would have the agent clicking a
  // paragraph and then reporting that the paragraph did not work.
  const page = await open();
  const { elements } = flatten(await page.locator("body").ariaSnapshot());
  assert.ok(
    !elements.some((e) => e.name.includes("items in your cart")),
    `page prose leaked into the actionable list: ${JSON.stringify(elements.map((e) => e.name))}`,
  );
  await page.close();
});

test("a filled field shows its value, so the agent does not retype it", noBrowser, async () => {
  const page = await open();
  await page.getByRole("textbox", { name: "Discount code" }).fill("SAVE10");
  const { elements } = flatten(await page.locator("body").ariaSnapshot());
  assert.equal(elements.find((e) => e.name === "Discount code")?.value, "SAVE10");
  await page.close();
});

/** A recorded run: fill the code, apply it, and — after one dead end — check out. */
const RECORDED = [
  { ok: true, action: { kind: "fill", text: "SAVE10" }, target: { role: "textbox", name: "Discount code" } },
  { ok: true, action: { kind: "click" }, target: { role: "button", name: "Apply code" } },
  { ok: true, action: { kind: "scroll", direction: "down" } },
  { ok: false, action: { kind: "click" }, target: { role: "button", name: "Save for later" }, detail: "element is disabled" },
  { ok: true, action: { kind: "click" }, target: { role: "button", name: "Proceed to checkout" } },
];

test("compiling keeps what worked and drops the agent's fumbling", () => {
  const plan = compile("http://x/", RECORDED);
  // The failed click and the orienting scroll are not part of how the app works. Replaying them
  // would bake a ten-second timeout into every future run.
  assert.deepEqual(plan.steps, [
    { kind: "fill", role: "textbox", name: "Discount code", text: "SAVE10" },
    { kind: "click", role: "button", name: "Apply code" },
    { kind: "click", role: "button", name: "Proceed to checkout" },
  ]);
});

test("replaying a recorded run needs no model at all", noBrowser, async () => {
  const page = await open();
  const r = await replay(page, compile(base, RECORDED));
  assert.equal(r.status, "passed", JSON.stringify(r));
  // It really drove the app, rather than passing by doing nothing.
  assert.match(await page.evaluate(() => document.body.innerText), /Order placed/);
  await page.close();
});

test("a renamed control is reported as stale, never as a bug", noBrowser, async () => {
  // The distinction the whole design turns on. A replay cannot tell "renamed" from "removed", and
  // guessing wrong pages somebody at 2am over a copy change.
  const page = await open(APP.replace(">Proceed to checkout<", ">Checkout<"));
  const r = await replay(page, compile(base, RECORDED));
  assert.equal(r.status, "stale");
  assert.equal(r.at, 2, "it should go stale on the checkout click, not before");

  const note = stalenessNote(r);
  assert.match(note, /Proceed to checkout/);
  assert.match(note, /not yet a bug/);
  assert.ok(!/\bfail/i.test(note), `a staleness note must not read as a failure: ${note}`);
  await page.close();
});

test("replay is fast enough to be worth recording", noBrowser, async () => {
  // The economic claim, measured rather than asserted. A replay as slow as an agent run removes
  // the reason to record one at all.
  const page = await open();
  const r = await replay(page, compile(base, RECORDED));
  assert.equal(r.status, "passed");
  assert.ok(r.ms < 15_000, `replay took ${r.ms}ms`);
  await page.close();
});
