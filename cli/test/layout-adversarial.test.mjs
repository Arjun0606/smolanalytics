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
// scale(0) popover, sr-only text in a 1×1 clip box, an infinitely spinning loader, and a tracking
// pixel. It carries the proof text so a whole scripted run can end on it.
const LOOKALIKES = `<!doctype html><title>Home</title>
  <style>
    body{margin:0;overflow-x:hidden;padding-bottom:80px}
    header{position:sticky;top:0;height:60px;background:#fff;border-bottom:1px solid #ddd;z-index:5}
    .drawer{position:fixed;top:0;left:0;width:300px;height:100%;background:#222;color:#fff;transform:translateX(-100%);transition:transform .2s}
    .track{overflow:hidden;width:600px}.rail{display:flex;width:1800px}
    .slide{width:600px;flex:0 0 600px;padding:20px;box-sizing:border-box}
    .pop{transform:scale(0);transform-origin:top left}
    .sr{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
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

  test("THE PIN: eight look-alike patterns on one page, zero findings, targets and all", noBrowser, async () => {
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

  test("a page that navigates out from under the audit is not a crash", noBrowser, async () => {
    await page.goto(`${base}/leaves`, { waitUntil: "domcontentloaded" });
    // No assertion about WHICH page got measured — that is a race by construction, and both
    // answers are correct. The contract is that it returns, with findings that are a list.
    const findings = await auditLayout(page, { targets: [{ role: "link", name: "Account settings" }] });
    assert.ok(Array.isArray(findings), "an audit that cannot finish must still return a list");
    await page.waitForLoadState("domcontentloaded").catch(() => {});
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
