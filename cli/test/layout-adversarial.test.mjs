// LAYOUT SANITY, ATTACKED: the pages that are FINE and were being reported as broken.
//
// The whole value of this feature hangs on one number — how often it cries wolf. A finding on a
// healthy page is not a small cost paid against a large benefit; it is the end of the feature,
// because the first sticky header flagged as an overlay is the last day anybody leaves it on. So
// this file is not more coverage of what layout.mjs catches. It is a list of ordinary,
// well-built pages, each of which produced a finding in real Chromium before the check that
// exempts it existed:
//
//   an off-canvas nav at translateX(-100%) on a page with `body { overflow-x: hidden }`
//                                        → "Account settings … is cut off — 300px of it lies
//                                           outside body, which clips"
//   a three-slide carousel                → the same, twice, once per parked slide
//   a modal backdrop closing on a .3s
//     opacity transition                  → "Back to shop is covered by div#b"
//   a dropdown closing on a .25s one      → "Sign out … has opacity:0 — no person can see it"
//   a popover closed with scale(0)        → "Sign out … renders at 0×0px"
//   a modal rendered into a portal, the
//     way React actually ships one        → "Back to shop is covered by div#modal-root"
//   an open custom select over its own
//     trigger                             → "Sort: Newest is covered by ul "Newest Oldest Price""
//   a checkbox at opacity:0 behind its
//     own drawn label                     → "Accept terms … has opacity:0"
//   a line-clamped card title             → "36px of it lies outside span, which clips"
//   a tab rail at overflow-x:auto         → "77px of it lies outside div.rail, which clips"
//   a scrollable panel taller than its
//     own box                             → "10px of it lies outside div.box, which clips"
//
// The last six were found by attacking this file's own claims, and two of them were being covered
// by a test that asserted the ONE DOM shape the implementation happened to handle: the modal
// fixture put aria-modal on the backdrop element itself, and the scrollable-box fixture set
// overflow on both axes at once. Both stayed green while the check they guarded was blind to the
// shape the whole web actually ships. A fixture that mirrors the implementation is not a test.
//
// Not one of those pages has anything wrong with it. Every test below therefore asserts BOTH
// halves — the healthy page is silent AND the defect that wears the same geometry is still caught
// — because an exemption nobody pins from both sides quietly becomes a blind spot, and a check
// that has gone blind is indistinguishable from one that works.
//
// The rest of the file probes ABSENCE: --layout=off must not touch the page, an audit whose
// evaluate is refused must lose its findings and nothing else, and a page that navigates out from
// under the audit must not turn somebody's verdict into a crash.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { auditLayout } from "../lib/layout.mjs";
import { testCmd } from "../lib/test.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-layout-adv-"));

// ---- the fixture app ----------------------------------------------------------------------------

// Every look-alike on one page, the way a real marketing site stacks them: a sticky header, a
// fixed footer with the body padded clear of it, a carousel with two slides parked outside its
// track, an off-canvas drawer under the near-universal `body { overflow-x: hidden }`, a closed
// scale(0) popover, sr-only text in a 1×1 clip box, an infinitely spinning loader, a tracking
// pixel, a horizontally scrollable tab rail, a line-clamped card title, a checkbox hidden behind
// its own styled label, an open custom select over its trigger, a `hidden` block that has not
// rendered, and a lazy image that never loads. It carries the proof text so a whole scripted run
// can end on it.
const LOOKALIKES = `<!doctype html><title>Home</title>
  <style>
    body{margin:0;overflow-x:hidden;padding-bottom:80px}
    header{position:sticky;top:0;height:60px;background:#fff;border-bottom:1px solid #ddd;z-index:5}
    .drawer{position:fixed;top:0;left:0;width:300px;height:100%;background:#222;color:#fff;transform:translateX(-100%);transition:transform .2s}
    .track{overflow:hidden;width:600px}.rail{display:flex;width:1800px}
    .slide{width:600px;flex:0 0 600px;padding:20px;box-sizing:border-box}
    .pop{transform:scale(0);transform-origin:top left}
    .sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
    .clamp{display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;width:220px}
    .tabs{overflow-x:auto;overflow-y:hidden;width:260px;white-space:nowrap}
    .tabs button{white-space:nowrap;padding:10px 14px}
    @keyframes spin{to{transform:rotate(360deg)}}
    .spinner{width:20px;height:20px;border:2px solid #ccc;border-top-color:#333;border-radius:50%;animation:spin 1s linear infinite}
    footer{position:fixed;bottom:0;left:0;right:0;height:64px;background:#111;color:#fff}
  </style>
  <header><a href="/">Home</a></header>
  <h1>Everything is fine here</h1>
  <p>2 items in your cart.</p>
  <button id="menu">Menu<span class="sr">opens the site navigation drawer</span></button>
  <nav class="drawer"><a href="/settings">Account settings</a><a href="/orders">Your orders</a></nav>
  <div class="track"><div class="rail">
    <div class="slide"><button>Shop the sale</button></div>
    <div class="slide"><button>Read the guide</button></div>
    <div class="slide"><button>See the lookbook</button></div>
  </div></div>
  <div class="tabs"><button>All products</button><button>New arrivals</button><button>Sale items today</button><button>Gift cards for everyone</button></div>
  <a href="/post"><span class="clamp">Why our team moved every single one of its background jobs off cron and onto a queue, and what broke on the way</span></a>
  <label style="display:inline-flex;align-items:center;gap:8px"><input type="checkbox" style="position:absolute;opacity:0"><span style="width:16px;height:16px;border:2px solid #333;display:inline-block"></span>Accept terms</label>
  <div style="position:relative;width:200px">
    <button role="combobox" aria-expanded="true" style="width:200px;padding:8px">Sort: Newest</button>
    <ul role="listbox" style="position:absolute;top:0;left:0;width:200px;background:#fff;border:1px solid #ccc;margin:0;padding:0;list-style:none;z-index:10"><li role="option" tabindex="0">Newest</li><li role="option" tabindex="0">Oldest</li></ul></div>
  <div hidden><button>Not rendered yet</button><a href="/x">Nor this</a></div>
  <a href="/product"><img loading="lazy" src="/never-loads.png" alt="Product photo"></a>
  <div class="pop"><button>Sign out</button></div>
  <div class="spinner"></div>
  <img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7" width="1" height="1" style="position:fixed;top:0;left:0">
  <div style="height:1500px"></div>
  <footer><button>Contact us</button></footer>`;

// The same page one navigation later, so a mid-audit navigation lands somewhere real.
const ELSEWHERE = `<!doctype html><title>Elsewhere</title><h1>Elsewhere</h1><p>2 items in your cart.</p><button>Go back</button>`;

const server = createServer((req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  if (req.url === "/elsewhere") return void res.end(ELSEWHERE);
  // Navigates out from under whatever is measuring it. 20ms: long enough that the audit has
  // started, short enough that it has not finished.
  if (req.url === "/leaves") return void res.end(`${LOOKALIKES}<script>setTimeout(()=>{location.href='/elsewhere'},20)</script>`);
  res.end(LOOKALIKES);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
after(() => new Promise((r) => server.close(() => r())));

// ---- the false positives ------------------------------------------------------------------------

describe("pages that are FINE", noBrowser.skip ? noBrowser : {}, () => {
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

  test("THE PIN: fourteen look-alike patterns on one page, zero findings, targets and all", noBrowser, async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    // Named as targets too: the drawer link and the closed popover's button are exactly what a run
    // that opened a menu and signed out would hand the audit, and the target path skips none of
    // the checks the untargeted path runs.
    const findings = await auditLayout(page, {
      targets: [
        { role: "link", name: "Account settings" },
        { role: "button", name: "Sign out" },
        { role: "button", name: "Menu" },
        { role: "button", name: "Contact us" },
        { role: "checkbox", name: "Accept terms" },
        { role: "combobox", name: "Sort: Newest" },
      ],
    });
    assert.deepEqual(findings, [], `every finding here is a false positive:\n${JSON.stringify(findings, null, 2)}`);
  });

  // Text ENTIRELY outside its clipping box is a panel parked out of view — a carousel slide, a
  // drawer, a collapsed region — not a label losing its tail. Measured overlaps: the carousel's
  // third slide x=-628, the drawer x=-193; a genuinely truncated label x=54 y=15 and a paragraph
  // clipped by a short box x=189 y=20. The rule is intersection, and both halves are pinned.
  test("text parked outside a clipping box is hidden on purpose; text STRADDLING it is cut off", noBrowser, async () => {
    const carousel = `<style>body{margin:0}.track{overflow:hidden;width:600px}.rail{display:flex;width:1800px}
      .slide{width:600px;flex:0 0 600px;padding:20px;box-sizing:border-box}</style>
      <div class="track"><div class="rail">
        <div class="slide"><button>Shop the sale</button></div>
        <div class="slide"><button>Read the guide</button></div>
        <div class="slide"><button>See the lookbook</button></div></div></div>`;
    await page.setContent(carousel);
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a carousel reported both parked slides as cut-off labels");

    await page.setContent(`<style>body{overflow-x:hidden;margin:0}
      nav{position:fixed;top:0;left:0;width:300px;height:100%;background:#222;transform:translateX(-100%)}</style>
      <h1>Shop</h1><p>2 items in your cart.</p><nav><a href="/settings">Account settings</a></nav>`);
    assert.deepEqual(
      await auditLayout(page, { targets: [{ role: "link", name: "Account settings" }] }),
      [],
      "an off-canvas drawer under body{overflow-x:hidden} reported its own links as cut off",
    );

    // The exemption is an exemption, not a blind spot: a label that really is cut off mid-word,
    // and a paragraph whose lines fall out the bottom of a short box, both still land.
    await page.setContent(`<div style="overflow:hidden;width:60px"><button style="white-space:nowrap;background:none;border:0">Download the annual report</button></div>`);
    const cut = await auditLayout(page, { targets: [] });
    assert.equal(cut.length, 1, JSON.stringify(cut));
    assert.equal(cut[0].check, "clipped");
    await page.setContent(`<h1>Refunds</h1><div style="overflow:hidden;height:20px;width:200px"><button style="background:none;border:0;margin:0;padding:0">Refunds are processed within five business days of the request being made</button></div>`);
    const tall = await auditLayout(page, { targets: [] });
    assert.equal(tall.length, 1, JSON.stringify(tall));
    assert.equal(tall[0].check, "clipped");
  });

  // getBoundingClientRect applies transforms; offsetWidth/offsetHeight do not. Measured on one
  // page: the real defect came back rect 0×0 / offset 0×0, a menu inside transform:scale(0) came
  // back rect 0×0 / offset 65×21.
  test("a popover closed with scale(0) is closed; a control that is really zero-size is broken", noBrowser, async () => {
    const target = [{ role: "button", name: "Sign out" }];
    await page.setContent(`<h1>Docs</h1><div style="transform:scale(0);transform-origin:top left"><button>Sign out</button></div>`);
    assert.deepEqual(await auditLayout(page, { targets: target }), [], "a closed scale(0) popover read as a 0×0 control");
    // Radix and Tippy close on opacity AND scale together, so the opacity branch must not pick up
    // what the size branch just let go.
    await page.setContent(`<h1>Docs</h1><div style="opacity:0;transform:scale(0);transform-origin:top left"><button>Sign out</button></div>`);
    assert.deepEqual(await auditLayout(page, { targets: target }), [], "the opacity branch caught the closed popover the size branch exempted");

    await page.setContent(`<h1>Cart</h1><p>ok</p><button style="width:0;height:0;padding:0;border:0;overflow:hidden">Sign out</button>`);
    const broken = await auditLayout(page, { targets: target });
    assert.equal(broken.length, 1, JSON.stringify(broken));
    assert.equal(broken[0].check, "invisible");
    assert.match(broken[0].detail, /0×0px/);
  });

  // The skip link at the top of every accessible page is a 1×1 clip box sitting underneath the
  // heading, and a run that used it hands it to the audit as a target. Measured before this:
  // `the link "Skip to content" is covered by h1 "Shop"` — all five sample points land on the one
  // pixel it occupies. The clip check has skipped boxes this size since it was written; the
  // coverage check had not, so the same accessibility pattern was exempt from one and a finding in
  // the other.
  test("an sr-only control is not covered by whatever it hides behind", noBrowser, async () => {
    const sr = `<style>.sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}</style>`;
    await page.setContent(`${sr}<a class="sr" href="#m">Skip to content</a><h2 class="sr">Main navigation</h2>
      <h1>Shop</h1><p>2 items</p><button>Buy now</button>`);
    assert.deepEqual(
      await auditLayout(page, { targets: [{ role: "link", name: "Skip to content" }] }),
      [],
      "a skip link read as a control covered by the heading it hides behind",
    );
    // The same link at a size a person could click, genuinely under a banner, is still a finding:
    // the exemption is about 1×1 boxes, not about links.
    await page.setContent(`<h1>Shop</h1><p>2 items</p>
      <a href="#m" style="position:fixed;top:100px;left:20px;padding:8px 12px">Skip to content</a>
      <div style="position:fixed;top:90px;left:0;right:0;height:60px;background:#333;color:#fff">We use cookies</div>`);
    const covered = await auditLayout(page, { targets: [{ role: "link", name: "Skip to content" }] });
    assert.equal(covered.length, 1, JSON.stringify(covered));
    assert.equal(covered[0].check, "covered");
    assert.match(covered[0].detail, /Skip to content/);
  });

  // The audit runs the instant after the last click, which is exactly when the UI that click
  // started is still moving. Both fixtures below close the way real code closes — transition, then
  // display:none on transitionend — and both produced a finding before the audit waited.
  test("a page still finishing its close animation is not a broken page", noBrowser, async () => {
    await page.setContent(`<style>.backdrop{position:fixed;inset:0;background:rgba(0,0,0,.5);transition:opacity .3s;opacity:1}
      .backdrop.gone{opacity:0}</style>
      <h1>Done</h1><p>Order placed</p><button style="margin:40px">Back to shop</button><div class="backdrop" id="b"></div>
      <script>const b=document.getElementById('b');b.addEventListener('transitionend',()=>b.style.display='none');
        requestAnimationFrame(()=>b.classList.add('gone'))</script>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a modal backdrop mid-fade-out read as an overlay covering the page");

    await page.setContent(`<style>.menu{transition:opacity .25s;opacity:1}.menu.shut{opacity:0}</style>
      <h1>Account</h1><p>2 items in your cart.</p><div class="menu" id="m"><button>Sign out</button></div>
      <script>const m=document.getElementById('m');m.addEventListener('transitionend',()=>m.style.display='none');
        requestAnimationFrame(()=>m.classList.add('shut'))</script>`);
    assert.deepEqual(
      await auditLayout(page, { targets: [{ role: "button", name: "Sign out" }] }),
      [],
      "a dropdown mid-fade-out read as an opacity:0 control still in the accessibility tree",
    );

    // Waiting is not the same as forgiving. An overlay that never goes away eats clicks forever,
    // and settling the page must not make it invisible to the check.
    await page.setContent(`<h1>Cart</h1><button style="margin:40px">Proceed to checkout</button>
      <div style="position:fixed;inset:0;background:rgba(0,0,0,.4)">We use cookies</div>`);
    const stuck = await auditLayout(page, { targets: [] });
    assert.equal(stuck.length, 1, JSON.stringify(stuck));
    assert.equal(stuck[0].check, "covered");
  });

  // A loading spinner is `animation: spin 1s linear infinite` and its finished promise never
  // resolves. Awaiting it would spend the whole settle ceiling on every page that has ever shown
  // one — measured 31ms with the spinner against 31ms without, because Chromium reports its
  // endTime as null and it is filtered out before anything is awaited.
  test("an infinite spinner is not waited on", noBrowser, async () => {
    await page.setContent(`<h1>Cart</h1><p>2 items</p><button>Load more</button>`);
    const t0 = Date.now();
    await auditLayout(page, { targets: [] });
    const still = Date.now() - t0;
    await page.setContent(`<style>@keyframes spin{to{transform:rotate(360deg)}}
      .s{width:20px;height:20px;animation:spin 1s linear infinite}</style>
      <h1>Cart</h1><p>2 items</p><button>Load more</button><div class="s"></div>`);
    const t1 = Date.now();
    await auditLayout(page, { targets: [] });
    const spinning = Date.now() - t1;
    // Relative, not absolute: a loaded CI runner is slow at both, and what must never happen is the
    // spinner costing hundreds of milliseconds the still page does not.
    assert.ok(spinning - still < 300, `the spinner cost ${spinning - still}ms more than a still page (${spinning} vs ${still}) — the infinite-animation filter is gone`);
  });

  // THE MODAL TEST THAT WAS PASSING BY ACCIDENT. The exemption used to ask `top.closest(dialog…)`,
  // which is true only when the backdrop element ITSELF carries aria-modal — and that is not how
  // modals are built. React, Vue and every headless-UI library render one into a portal:
  //
  //   <div id="modal-root"><div class="overlay"><div role="dialog" aria-modal="true">…
  //
  // The overlay root is #modal-root and the dialog is two levels inside it, so `closest` said no
  // and the audit reported, measured in real Chromium:
  //   `the button#bg "Back to shop" is covered by div#modal-root "Order placedClose"`.
  // The old fixture asserted the one shape the implementation happened to handle, which is how a
  // check goes blind while its test stays green. Both shapes are pinned here.
  test("a modal rendered through a portal is still a modal", noBrowser, async () => {
    const bg = `<h1>Done</h1><button id="bg" style="margin:40px">Back to shop</button>`;
    const sheet = (attrs) => `<div style="background:#fff;margin:100px auto;width:400px;padding:40px" ${attrs}>Order placed<button>Close</button></div>`;
    await page.setContent(`${bg}<div id="modal-root"><div class="overlay" style="position:fixed;inset:0;background:rgba(0,0,0,.5)">${sheet('role="dialog" aria-modal="true"')}</div></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a portal-rendered modal read as an overlay covering the page");
    // The confirm-dialog spelling too: role=alertdialog, no aria-modal.
    await page.setContent(`${bg}<div class="portal"><div style="position:fixed;inset:0;background:rgba(0,0,0,.5)">${sheet('role="alertdialog"')}</div></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);

    // AND THE EXEMPTION IS NOT A SKELETON KEY. A closed modal left in the tree at display:none is
    // on half the apps that ever rendered one; if a dead dialog template could vouch for whatever
    // is above it, any stuck banner sharing that container would walk free.
    await page.setContent(`${bg}<div id="modal-root">
      <div style="position:fixed;inset:0;background:rgba(0,0,0,.5)">We use cookies</div>
      <div role="dialog" aria-modal="true" style="display:none">Order placed</div></div>`);
    const stuck = await auditLayout(page, { targets: [] });
    assert.equal(stuck.length, 1, JSON.stringify(stuck));
    assert.equal(stuck[0].check, "covered");
    assert.match(stuck[0].detail, /Back to shop/);
  });

  // A control's OWN popup renders over it because the control was used. The click that opened it
  // landed exactly where it was aimed — the opposite of the cookie banner this check is for.
  // Measured before the exemption existed: `the button#trig "Sort: Newest" is covered by
  // ul "Newest Oldest Price"`, on an open custom select doing precisely its job.
  test("a control's own popup is not something covering it", noBrowser, async () => {
    // (a) a custom select whose listbox overlays its trigger: linked by aria-expanded + the role.
    await page.setContent(`<h1>Filters</h1><p>2 items</p><div style="position:relative;width:200px">
      <button role="combobox" aria-expanded="true" id="trig" style="width:200px;padding:8px">Sort: Newest</button>
      <ul role="listbox" style="position:absolute;top:0;left:0;width:200px;background:#fff;border:1px solid #ccc;margin:0;padding:0;list-style:none;z-index:10">
        <li role="option" tabindex="0">Newest</li><li role="option" tabindex="0">Oldest</li></ul></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [{ role: "combobox", name: "Sort: Newest" }] }), [], "an open custom select reported its own listbox as an overlay");

    // (b) a menu button under its own menu: linked by aria-controls.
    await page.setContent(`<h1>Account</h1><p>2 items</p><div style="position:relative">
      <button id="mb" aria-expanded="true" aria-haspopup="menu" aria-controls="mm" style="padding:8px 12px">Account</button>
      <div id="mm" role="menu" style="position:absolute;top:0;left:0;width:180px;background:#fff;border:1px solid #ccc">
        <button role="menuitem">Profile</button><button role="menuitem">Sign out</button></div></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [{ role: "button", name: "Account" }] }), []);

    // (c) a tooltip over the control it describes: linked by aria-describedby. A click leaves the
    // control focused, which is exactly when a focus-shown tooltip is on screen.
    await page.setContent(`<h1>Shop</h1><p>2 items</p><div style="position:relative">
      <button aria-describedby="tip" style="padding:8px">Help</button>
      <div id="tip" role="tooltip" style="position:absolute;top:0;left:0;background:#000;color:#fff;padding:8px">What this does</div></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [{ role: "button", name: "Help" }] }), []);

    // THE OTHER HALF: aria-expanded is not a hall pass. The same attribute on a control that is
    // genuinely under a cookie banner — no reference to it, and a banner is no kind of popup — is
    // the finding this whole check exists for, and it still lands.
    await page.setContent(`<h1>Account</h1><p>2 items</p>
      <button aria-expanded="true" style="position:fixed;bottom:30px;left:20px">Account</button>
      <div id="cookie-banner" style="position:fixed;bottom:0;left:0;right:0;height:160px;background:#333;color:#fff;z-index:9">We use cookies</div>`);
    const banner = await auditLayout(page, { targets: [] });
    assert.equal(banner.length, 1, JSON.stringify(banner));
    assert.equal(banner[0].check, "covered");
    assert.match(banner[0].detail, /cookie-banner/);
  });

  // The checkbox at opacity:0 under a drawn box is how custom checkboxes, radios and toggle
  // switches are built everywhere. The input is invisible ON PURPOSE and every click, tap and
  // space-bar press arrives through the label. Measured before the exemption: both spellings —
  // the wrapping <label> and the <label for> — produced `the checkbox "Accept terms" is still on
  // the page and in the accessibility tree, but has opacity:0 — no person can see it`.
  //
  // This is also the only shape the false positive can take: the untargeted sweep skips hidden
  // elements outright, so a control reaches that check only because the run clicked it BY NAME —
  // and the name Playwright resolved is the one on this very label.
  test("a checkbox hidden behind its own label is the pattern, not a defect", noBrowser, async () => {
    const target = [{ role: "checkbox", name: "Accept terms" }];
    const box = `<span style="width:16px;height:16px;border:2px solid #333;display:inline-block"></span>`;
    // The wrapping label, opacity:0 — Bootstrap's spelling.
    await page.setContent(`<h1>Checkout</h1><p>2 items</p><label style="display:inline-flex;align-items:center;gap:8px">
      <input type="checkbox" style="position:absolute;opacity:0;width:16px;height:16px">${box}Accept terms</label>`);
    assert.deepEqual(await auditLayout(page, { targets: target }), [], "a styled checkbox read as an invisible control");
    // The same pattern collapsed to nothing rather than faded — it must not fall through to the
    // zero-size branch instead.
    await page.setContent(`<h1>Checkout</h1><p>2 items</p><label style="display:inline-flex;align-items:center;gap:8px">
      <input type="checkbox" style="position:absolute;opacity:0;width:0;height:0">${box}Accept terms</label>`);
    assert.deepEqual(await auditLayout(page, { targets: target }), []);
    // label[for], the input outside it.
    await page.setContent(`<h1>Checkout</h1><p>2 items</p><input type="checkbox" id="tos" style="position:absolute;opacity:0">
      <label for="tos" style="display:inline-flex;align-items:center;gap:8px">${box}Accept terms</label>`);
    assert.deepEqual(await auditLayout(page, { targets: target }), []);

    // THREE WAYS THE EXEMPTION MUST NOT REACH.
    // (a) the whole component faded out: the label went with it, and nobody can click what nobody
    //     can see.
    await page.setContent(`<h1>Checkout</h1><p>2 items</p><div style="opacity:0"><label style="display:inline-flex;gap:8px">
      <input type="checkbox" style="position:absolute;opacity:0">${box}Accept terms</label></div>`);
    const gone = await auditLayout(page, { targets: target });
    assert.equal(gone.length, 1, JSON.stringify(gone));
    assert.equal(gone[0].check, "invisible");
    // (b) a button is not a checkbox: it has no label standing in for it, and an invisible one is
    //     just invisible.
    await page.setContent(`<h1>Checkout</h1><p>2 items</p><button style="opacity:0">Place order</button>`);
    const ghost = await auditLayout(page, { targets: [{ role: "button", name: "Place order" }] });
    assert.equal(ghost.length, 1, JSON.stringify(ghost));
    assert.match(ghost[0].detail, /opacity:0/);
    // (c) A HIDDEN TEXT FIELD IS NOT THE SAME PATTERN, and this is the line the exemption is drawn
    //     on. Clicking a label focuses a checkbox and the job is done; it focuses a text field a
    //     person then has to type into blind. Only click-only controls are covered.
    await page.setContent(`<h1>Login</h1><p>2 items</p><label for="e">Email address</label>
      <input id="e" type="email" style="opacity:0">`);
    const blind = await auditLayout(page, { targets: [{ role: "textbox", name: "Email address" }] });
    assert.equal(blind.length, 1, JSON.stringify(blind));
    assert.match(blind[0].detail, /Email address/);
  });

  // Truncation an author ASKED FOR says so in CSS, and there are two ways to say it. The check
  // knew `text-overflow: ellipsis` and not `-webkit-line-clamp`, which is what every card title on
  // the web uses — Tailwind ships it as `line-clamp-2`. Measured before this: a two-line clamped
  // headline inside a card link came back `36px of it lies outside span …, which clips`, on a page
  // rendering exactly as intended, ellipsis and all.
  test("a line-clamped headline is truncated on purpose", noBrowser, async () => {
    const long = "Why our team moved every single one of its background jobs off cron and onto a queue, and what broke on the way";
    await page.setContent(`<h1>Blog</h1><p>2 items</p><a href="/post" style="display:block;width:220px">
      <span style="display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden">${long}</span></a>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a line-clamped card title read as a cut-off label");
    // Nested one level deeper, the clamp on the inner box and an outer overflow:hidden card.
    await page.setContent(`<h1>Blog</h1><p>2 items</p><a href="/post" style="display:block;width:220px;overflow:hidden">
      <span style="display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden">${long}</span></a>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
    // No declaration of intent anywhere is still a finding: a paragraph falling out of a short box.
    await page.setContent(`<h1>Refunds</h1><div style="overflow:hidden;height:20px;width:200px">
      <button style="background:none;border:0;margin:0;padding:0">Refunds are processed within five business days of the request being made</button></div>`);
    const cut = await auditLayout(page, { targets: [] });
    assert.equal(cut.length, 1, JSON.stringify(cut));
    assert.equal(cut[0].check, "clipped");
  });

  // THE AXIS THAT SCROLLS IS NOT THE AXIS THAT CLIPS. overflow-x and overflow-y are set separately
  // and constantly disagree, and asking "does either clip?" then measuring both axes defeated the
  // auto/scroll exemption on the two commonest scrolling containers there are. Measured before
  // this, in real Chromium:
  //
  //   overflow-x:auto; overflow-y:hidden   a tab rail   → "77px of it lies outside div.rail"
  //   overflow-y:auto; overflow-x:hidden   a side panel → "10px of it lies outside div.box"
  //
  // Both texts are one scroll away. The same containers clipping the OTHER axis lose the text for
  // good, and both of those still land.
  test("text is only cut off on an axis that actually clips", noBrowser, async () => {
    const rail = (ox, oy) => `<style>body{margin:0}.rail{overflow-x:${ox};overflow-y:${oy};width:120px;height:40px;white-space:nowrap}
      .rail button{white-space:nowrap;background:none;border:0;padding:0;font-size:16px}</style>
      <h1>Shop</h1><p>2 items</p><div class="rail"><button>Download the annual report</button></div>`;
    await page.setContent(rail("auto", "hidden"));
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a horizontal tab rail reported its own scrollable label as cut off");
    await page.setContent(rail("scroll", "hidden"));
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
    // A scrollable panel taller than its box is what a scrollable panel IS.
    await page.setContent(`<style>body{margin:0}.box{overflow-y:auto;overflow-x:hidden;width:300px;height:20px}</style>
      <h1>Refunds</h1><div class="box"><button style="background:none;border:0;margin:0;padding:0">Refunds are processed within five business days of the request being made and no sooner</button></div>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a vertical scroller reported its own scrollable content as cut off");

    // The other half, on the same two containers: text that runs off the axis they DO clip.
    await page.setContent(rail("hidden", "auto"));
    const lost = await auditLayout(page, { targets: [] });
    assert.equal(lost.length, 1, JSON.stringify(lost));
    assert.equal(lost[0].check, "clipped");
    assert.match(lost[0].detail, /77px of it lies outside/);
    await page.setContent(rail("hidden", "hidden"));
    assert.equal((await auditLayout(page, { targets: [] })).length, 1);
  });
});

// ---- absence: what must NOT happen ---------------------------------------------------------------

/** A page whose every method call is recorded, so "computed nothing" can be asserted rather than believed. */
function spyOn(page, seen, brokenEvaluate = null) {
  let evaluations = 0;
  return new Proxy(page, {
    get(target, key) {
      if (typeof key === "string") seen.push(key);
      if (key === "evaluate" && brokenEvaluate) {
        return async (...args) => {
          evaluations++;
          if (brokenEvaluate === "all" || evaluations === brokenEvaluate) {
            // The shape a locked-down context throws in: the audit must lose its findings and
            // absolutely nothing else.
            throw new Error("Blocked by Content Security Policy");
          }
          return Reflect.get(target, key).apply(target, args);
        };
      }
      const value = Reflect.get(target, key);
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
}

describe("the audit's absence", noBrowser.skip ? noBrowser : {}, () => {
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

  test("--layout=off does not touch the page at all", noBrowser, async () => {
    await page.goto(`${base}/covered-does-not-exist`, { waitUntil: "domcontentloaded" });
    const seen = [];
    const findings = await auditLayout(spyOn(page, seen), {
      mode: "off",
      targets: [{ role: "button", name: "Menu" }],
    });
    assert.deepEqual(findings, []);
    // "off" that still evaluates is off in the report and on in the bill: it costs the round trip,
    // it can still throw, and it can still hold a page open. Asserted on the page object, not on
    // the output, because the output of a working audit and a skipped one look the same.
    assert.deepEqual(seen, [], `--layout=off reached for ${seen.join(", ")}`);
    // And the same page in report mode DOES reach for it, so the assertion above means something.
    const used = [];
    await auditLayout(spyOn(page, used), { mode: "report", targets: [{ role: "button", name: "Menu" }] });
    assert.ok(used.includes("evaluate"), `report mode never evaluated: ${used.join(", ")}`);
    assert.ok(used.includes("getByRole"), `report mode never resolved its targets: ${used.join(", ")}`);
  });

  test("an evaluate that is refused costs the findings and nothing else", noBrowser, async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    // The audit itself refused: no findings, no throw. A checker that crashes must never redden a
    // build, so [] is the only answer it is allowed to have.
    assert.deepEqual(await auditLayout(spyOn(page, [], "all"), { targets: [] }), []);
    assert.deepEqual(await auditLayout(spyOn(page, [], 2), { targets: [] }), []);
    // But the SETTLE refusing is not the audit refusing. Settling is a precaution against a page
    // mid-animation; trading every finding for it would be paying the feature to protect it.
    await page.setContent(`<h1>Cart</h1><button style="margin:40px">Proceed to checkout</button>
      <div style="position:fixed;inset:0;background:rgba(0,0,0,.4)">We use cookies</div>`);
    const stillWorks = await auditLayout(spyOn(page, [], 1), { targets: [] });
    assert.equal(stillWorks.length, 1, JSON.stringify(stillWorks));
    assert.equal(stillWorks[0].check, "covered");
  });

  // The Proxy above proves nothing was asked of the Playwright page. This proves nothing ran in
  // the BROWSER — the page counts the two calls the audit cannot do its job without, and an audit
  // that reached the DOM some other way would still be caught by a counter sitting in the DOM.
  test("--layout=off leaves no fingerprint INSIDE the page either", noBrowser, async () => {
    const instrumented = `<script>
        window.__hits = { anims: 0, efp: 0 };
        const ga = document.getAnimations.bind(document);
        document.getAnimations = () => { window.__hits.anims++; return ga(); };
        const efp = document.elementFromPoint.bind(document);
        document.elementFromPoint = (x, y) => { window.__hits.efp++; return efp(x, y); };
      </script>
      <h1>Cart</h1><p>2 items</p><button style="margin:40px">Proceed to checkout</button>
      <div style="position:fixed;inset:0;background:rgba(0,0,0,.4)">We use cookies</div>`;
    await page.setContent(instrumented);
    assert.deepEqual(await auditLayout(page, { mode: "off", targets: [{ role: "button", name: "Proceed to checkout" }] }), []);
    assert.deepEqual(await page.evaluate(() => window.__hits), { anims: 0, efp: 0 }, "--layout=off ran code in the customer's page");
    // The same page in report mode leaves the fingerprint, so the zero above is evidence and not
    // an instrument that never worked. Measured: 2 settle looks, 5 sampled points.
    await page.setContent(instrumented);
    const found = await auditLayout(page, { mode: "report", targets: [{ role: "button", name: "Proceed to checkout" }] });
    const hits = await page.evaluate(() => window.__hits);
    assert.ok(hits.anims > 0 && hits.efp > 0, `report mode left no fingerprint either: ${JSON.stringify(hits)}`);
    assert.equal(found.length, 1);
  });

  test("a page with nothing to click produces nothing, not a crash", noBrowser, async () => {
    await page.setContent(`<h1>Terms</h1><p>2 items in your cart.</p><p>Nothing here is clickable.</p>`);
    assert.deepEqual(await auditLayout(page, { targets: [] }), []);
    // And with a target that no longer resolves — the ordinary case after a navigation.
    assert.deepEqual(await auditLayout(page, { targets: [{ role: "button", name: "Buy now" }] }), []);
  });

  test("a page that navigates out from under the audit is not a crash", noBrowser, async () => {
    await page.goto(`${base}/leaves`, { waitUntil: "domcontentloaded" });
    // No assertion about WHICH page got measured — that is a race by construction, and both
    // answers are correct. The contract is that it returns, with findings that are a list.
    const findings = await auditLayout(page, { targets: [{ role: "link", name: "Account settings" }] });
    assert.ok(Array.isArray(findings), "an audit that cannot finish must still return a list");
    await page.waitForLoadState("domcontentloaded").catch(() => {});
  });
});

// ---- the scope and the caps, which are what keep a finding readable ------------------------------
//
// The contract is "what the run touched, plus the visible controls on the final page". Widened to
// the whole DOM it becomes a site-wide linter that reports a marketing page's footer under a
// checkout test, which is a different product and a worse one. Uncapped it becomes a wall of text
// nobody reads. Neither had a test that could fail.

describe("the audited set", noBrowser.skip ? noBrowser : {}, () => {
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

  // A control 2000px below the fold is not on the page a person is looking at. Auditing it is how
  // a checkout test starts reporting the footer of a page the run never scrolled to.
  test("is the final VIEWPORT, not the whole document", noBrowser, async () => {
    const broken = `<div style="position:relative;height:44px">
      <button style="position:absolute;top:0;left:0;width:200px;height:40px">Buy now</button>
      <div style="position:absolute;top:0;left:0;width:200px;height:40px;background:#333">Banner</div></div>`;
    await page.setContent(`<h1>Shop</h1><p>2 items</p><div style="height:2000px"></div>${broken}`);
    // setContent keeps the previous document's scroll offset — measured 1195px carried across a
    // fresh setContent — and "below the fold" means nothing on a page that is already scrolled.
    await page.evaluate(() => window.scrollTo(0, 0));
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a control far below the fold was audited: the scope is the whole document");
    // Scrolled to, it is part of the page the run ended on, and it is a finding. Without this the
    // assertion above would also pass on a check that had simply stopped working.
    await page.evaluate(() => window.scrollTo(0, 2000));
    const seen = await auditLayout(page, { targets: [] });
    assert.equal(seen.length, 1, JSON.stringify(seen));
    assert.equal(seen[0].check, "covered");

    // The COVERED check filters its sample points to the viewport on its own, so a coverage
    // fixture alone cannot tell a scoped audit from an unscoped one. The clipped check has no such
    // filter, and a cut-off label 2000px down is what an unscoped audit reports and a scoped one
    // never sees. Same page, same two halves.
    const clipped = `<div style="overflow:hidden;width:60px"><button style="white-space:nowrap;background:none;border:0">Download the annual report</button></div>`;
    await page.setContent(`<h1>Shop</h1><p>2 items</p><div style="height:2000px"></div>${clipped}`);
    await page.evaluate(() => window.scrollTo(0, 0));
    assert.deepEqual(await auditLayout(page, { targets: [] }), [], "a cut-off label far below the fold was audited: the scope is the whole document");
    await page.evaluate(() => window.scrollTo(0, 2000));
    const below = await auditLayout(page, { targets: [] });
    assert.equal(below.length, 1, JSON.stringify(below));
    assert.equal(below[0].check, "clipped");
  });

  // A button whose own ripple/overlay span sits on top of it is one control, not two. Every
  // Material-flavoured button on the web is built this way, and elementFromPoint returns the span.
  test("never reports a control as covered by its own child", noBrowser, async () => {
    await page.setContent(`<h1>Shop</h1><p>2 items</p>
      <button style="position:relative;padding:12px 20px">Buy now<span style="position:absolute;inset:0;background:rgba(255,255,255,.05)"></span></button>`);
    assert.deepEqual(await auditLayout(page, { targets: [{ role: "button", name: "Buy now" }] }), [], "a button was reported as covered by its own child");
    // The identical span lifted OUT of the button covers it for real, so the rule above is about
    // ancestry and not about spans.
    await page.setContent(`<h1>Shop</h1><p>2 items</p><div style="position:relative;display:inline-block">
      <button style="padding:12px 20px">Buy now</button>
      <span style="position:absolute;inset:0;background:#333"></span></div>`);
    const covered = await auditLayout(page, { targets: [{ role: "button", name: "Buy now" }] });
    assert.equal(covered.length, 1, JSON.stringify(covered));
    assert.equal(covered[0].check, "covered");
    // A control the run touched and one the sweep found read as the same kind of thing in the same
    // sentence. A live run printed both halves of one grouped finding —
    // `covers 6 controls, including button "Menu" and the button "All products"` — and a missing
    // article in the only sentence the customer sees reads as a typo in the finding.
    assert.match(covered[0].detail, /^the button "Buy now" is covered by/, covered[0].detail);
  });

  // Twelve separately-covered controls, so grouping cannot do the capping and the number has to
  // come from the cap itself.
  test("stops at the cap however broken the page is", noBrowser, async () => {
    let html = `<style>body{margin:0}</style><h1>Shop</h1><p>2 items</p>`;
    for (let i = 0; i < 12; i++) {
      html += `<div style="position:relative;height:44px">
        <button style="position:absolute;top:0;left:0;width:200px;height:40px">Buy ${i}</button>
        <div style="position:absolute;top:0;left:0;width:200px;height:40px;background:#333">Banner ${i}</div></div>`;
    }
    await page.setContent(html);
    const findings = await auditLayout(page, { targets: [] });
    assert.equal(findings.length, 8, `a broken page returned ${findings.length} findings — the cap is what keeps the note readable`);
    assert.ok(findings.every((f) => f.check === "covered"), JSON.stringify(findings));
  });

  // Targets are capped too, and the ones kept are the LAST ones: the end of a run is the page the
  // verdict is about.
  test("audits the most recent targets when a run touched more than the cap", noBrowser, async () => {
    let html = `<h1>Shop</h1><p>2 items</p>`;
    for (let i = 0; i < 12; i++) html += `<button style="opacity:0">Ghost ${i}</button>`;
    await page.setContent(html);
    const findings = await auditLayout(page, { targets: Array.from({ length: 12 }, (_, i) => ({ role: "button", name: `Ghost ${i}` })) });
    assert.equal(findings.length, 8, JSON.stringify(findings));
    assert.match(findings[0].detail, /Ghost 4/, "the cap kept the oldest targets instead of the newest");
    assert.match(findings[7].detail, /Ghost 11/);
  });
});

// ---- the whole binary, on a page full of look-alikes ---------------------------------------------

describe("--layout=strict on a healthy page, through the real command", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));

  /** The child, run without blocking the event loop the fixture app is served from. */
  const spawned = (argv, env) =>
    new Promise((resolve, reject) => {
      const child = spawn(process.execPath, argv, { env });
      let stdout = "";
      let stderr = "";
      child.stdout.setEncoding("utf8");
      child.stderr.setEncoding("utf8");
      child.stdout.on("data", (d) => (stdout += d));
      child.stderr.on("data", (d) => (stderr += d));
      child.on("error", reject);
      child.on("close", (status) => resolve({ status, stdout, stderr }));
    });

  test("exit 0, in the process's own exit code, with the model scripted in the child", noBrowser, async () => {
    const dir = scratch();
    // Intercepted in the CHILD, so bin/ parses the flag, lib/ runs the browser, and the exit code
    // is a real process's — the three things an in-process call cannot prove. There is no key on
    // this machine and this run needs none.
    const preload = path.join(dir, "scripted-model.mjs");
    writeFileSync(preload, `
const real = globalThis.fetch;
globalThis.fetch = async (target, init = {}) => {
  if (!String(target).includes("api.anthropic.com")) return real(target, init);
  return {
    ok: true,
    status: 200,
    text: async () => "",
    json: async () => ({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t1", name: "finish",
      input: { passed: true, why: "The home page rendered its cart line.", proof: "2 items in your cart." } }] }),
  };
};
`);
    const r = await spawned([bin, "test", "--url", base, "--test", "the home page shows the cart line", "--layout", "strict", "--yes", "--retries", "0", "--evidence-dir", path.join(dir, "evidence")], {
      ...process.env,
      ANTHROPIC_API_KEY: "sk-ant-test",
      NODE_OPTIONS: `--import ${new URL(`file://${path.resolve(preload)}`).href}`,
    });
    const out = `${r.stdout}${r.stderr}`.replace(/\x1b\[[0-9;]*m/g, "");
    assert.equal(r.status, 0, `strict gated a page with nothing wrong with it:\n${out}`);
    assert.match(out, /\bPASS\b/, out);
    assert.ok(!/^layout: /m.test(out), `a finding on this page is a false positive:\n${out}`);
  });
});
