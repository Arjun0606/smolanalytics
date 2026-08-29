// EMBEDDED CONTENT: WHAT PERCEPTION SEES, AND WHAT IT IS ALLOWED TO CLAIM IT DID NOT SEE.
//
// Measured against real Chromium before any of lib/frames.mjs existed. `ariaSnapshot()` renders an
// entire payment form as one bare word, `- iframe`, which `flatten()` then drops for having no
// name — so the agent was handed a checkout page with two controls on it and no indication that a
// third document was sitting in the middle of it:
//
//   (f) normal page ............ listed, clicked
//   (a) same-origin iframe ..... INVISIBLE, click timed out
//   (b) cross-origin iframe .... INVISIBLE, click timed out
//   (c) open shadow DOM ........ already worked (Playwright's selectors pierce open roots)
//   (d) closed shadow DOM ...... INVISIBLE, and genuinely unreadable
//   (e) iframe inside iframe ... INVISIBLE, click timed out
//
// The damage is not the missing click. It is that the agent's next move is to call finish(false)
// and write "the Pay button does not exist on the checkout page" about a checkout that works. A
// runner that invents defects in a customer's application is worse than one that misses them, and
// every one of those four rows is Stripe, reCAPTCHA, Intercom or a component library.
//
// So these tests state the two REQUIREMENTS, not the implementation:
//
//   1. A control our tools CAN reach is listed, attributed to its frame, clickable, and replayable
//      with no model call.
//   2. A control our tools CANNOT reach is SAID OUT LOUD. Not silence — silence is what the agent
//      reads as absence.
//
// The pure half runs with no browser. The rest drives real Chromium against real local servers,
// including a second server on a second port so the cross-origin case is genuinely cross-origin.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtempSync, existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  closedShadowRoots,
  embeddedNotes,
  frameLabel,
  frameSelector,
  inFrame,
  readFrames,
  unstable,
  visibleText,
} from "../lib/frames.mjs";
import { flatten, perceive, readPlan, render, testCmd } from "../lib/test.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

// ---- the fixtures -------------------------------------------------------------------------------

const doc = (b) => `<!doctype html><meta charset="utf-8">${b}`;

// Served by the SECOND server, so "cross-origin" means a different origin and not a different path.
const CROSS_INNER = doc(`<title>Payment</title><h2>Card details</h2>
<label>Card number <input type="text" name="card"></label>
<button id="pay">Pay $20.00</button>`);

let CROSS = "";
const PAGES = () => ({
  "/control": doc(`<title>Control</title><h1>Checkout</h1>
<label>Email <input type="text" name="email"></label><button>Place order</button>`),

  "/inner-form": doc(`<title>Inner form</title><h2>Card details</h2>
<p id="msg">Card not charged yet.</p>
<label>Card number <input type="text" name="card"></label>
<button id="pay">Pay $20.00</button>
<script>document.getElementById('pay').onclick = () => {
  document.getElementById('msg').textContent = 'Payment received in full.';
};</script>`),

  "/same": doc(`<title>Same origin frame</title><h1>Checkout</h1><button>Outer button</button>
<iframe title="Payment form" src="/inner-form" width="500" height="320"></iframe>`),

  "/cross": doc(`<title>Cross origin frame</title><h1>Checkout</h1><button>Outer button</button>
<iframe title="Payment form" src="${CROSS}/x-inner" width="500" height="320"></iframe>`),

  "/open-shadow": doc(`<title>Open shadow</title><h1>Widget page</h1><button>Outer button</button>
<div id="host"></div>
<script>document.getElementById('host').attachShadow({mode:'open'}).innerHTML =
  '<p>Discount applied: 10% off.</p><label>Coupon <input type="text" name="coupon"></label><button>Apply coupon</button>';
</script>`),

  "/closed-shadow": doc(`<title>Closed shadow</title><h1>Widget page</h1><button>Outer button</button>
<my-widget></my-widget>
<script>document.querySelector('my-widget').attachShadow({mode:'closed'}).innerHTML =
  '<button>Secret button</button>';
</script>`),

  "/nested": doc(`<title>Nested frames</title><h1>Outer page</h1><button>Outer button</button>
<iframe title="Middle" src="/mid" width="620" height="440"></iframe>`),

  "/mid": doc(`<title>Middle</title><h2>Middle frame</h2><button>Middle button</button>
<iframe title="Deep" src="/inner-form" width="520" height="340"></iframe>`),
});

/** EVERY server here is closed with closeAllConnections() FIRST: a keep-alive socket from a live
 *  Chromium wedges close() forever, and `node --test` then hangs instead of finishing. */
async function listen(handler) {
  const s = createServer(handler);
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  return s;
}
async function shut(s) {
  if (!s) return;
  s.closeAllConnections();
  await new Promise((r) => s.close(() => r()));
}

const crossServer = await listen((_req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(CROSS_INNER);
});
CROSS = `http://127.0.0.1:${crossServer.address().port}`;
const PAGE = PAGES();

const server = await listen((req, res) => {
  const body = PAGE[req.url.split("?")[0]];
  res.writeHead(body ? 200 : 404, { "content-type": "text/html; charset=utf-8" });
  res.end(body || doc("<title>404</title><p>no such page</p>"));
});
const BASE = `http://127.0.0.1:${server.address().port}`;

let browser = null;
async function open(p) {
  browser ??= await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await page.goto(BASE + p, { waitUntil: "domcontentloaded" });
  await page.waitForLoadState("networkidle").catch(() => {});
  return page;
}

after(async () => {
  await browser?.close().catch(() => {});
  await shut(server);
  await shut(crossServer);
});

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-frames-"));
const named = (snap, role, name) => snap.elements.find((e) => e.role === role && e.name === name);

// ---- naming a frame so a recording survives the next deploy --------------------------------------

describe("a frame is named by something that will still be there tomorrow", () => {
  test("the title wins, because it is what the frame is called", () => {
    assert.equal(
      frameSelector({ tag: "IFRAME", title: "Secure payment input frame", id: "f1", name: "pay" }),
      'iframe[title="Secure payment input frame"]',
    );
  });

  test("a generated name is refused, and the stable attribute beside it is used instead", () => {
    // Stripe: name="__privateStripeFrame1234", title="Secure payment input frame". reCAPTCHA:
    // name="a-9f3c1d20e8", title="reCAPTCHA". Recording the generated half writes a recording that
    // is stale on the very next page load — every embedded checkout in the suite would wake the
    // agent at full model price on every pull request, which is the whole cost argument inverted.
    assert.equal(unstable("__privateStripeFrame1234"), true);
    assert.equal(unstable("a-9f3c1d20e8"), true);
    assert.equal(unstable("Secure payment input frame"), false);
    assert.equal(
      frameSelector({ name: "__privateStripeFrame1234", title: "Secure payment input frame", index: 3 }),
      'iframe[title="Secure payment input frame"]',
    );
  });

  test("when every name is generated, position is used — it says nothing, but it is true", () => {
    assert.equal(frameSelector({ title: "frame-88213", name: "a-0f9e8d7c6b", id: "", index: 2 }), "iframe >> nth=2");
  });

  test("a value that cannot be quoted falls through rather than building a broken selector", () => {
    // A title containing a double quote spliced straight into iframe[title="…"] is a selector that
    // throws at click time — reported as a stale recording, about an app that never changed.
    assert.equal(frameSelector({ title: 'He said "hi"', index: 1 }), "iframe >> nth=1");
  });

  test("id and name are still used when there is no title", () => {
    assert.equal(frameSelector({ id: "checkout-frame", name: "pay", index: 0 }), 'iframe[id="checkout-frame"]');
    assert.equal(frameSelector({ name: "pay", index: 0 }), 'iframe[name="pay"]');
  });

  test("the label a step prints is read back out of the selector, so it can never disagree with it", () => {
    assert.equal(frameLabel(['iframe[title="Payment form"]']), "Payment form");
    assert.equal(frameLabel(['iframe[title="Middle"]', 'iframe[id="deep"]']), "deep", "the innermost frame is the one acted in");
    assert.equal(frameLabel(["iframe >> nth=2"]), "iframe >> nth=2");
    assert.equal(frameLabel([]), "");
    assert.equal(frameLabel(undefined), "");
  });
});

describe("a step with no frame is a step on the page, exactly as before", () => {
  const mk = (log) => ({ log, frameLocator: (sel) => (log.push(sel), mk(log)) });

  test("an empty chain returns the page itself", () => {
    const log = [];
    const page = mk(log);
    assert.equal(inFrame(page, undefined), page, "every recording written before frames existed has no frame field");
    assert.equal(inFrame(page, []), page);
    assert.deepEqual(log, [], "a page with no frames must not be wrapped in a frameLocator");
  });

  test("a chain is entered outermost first", () => {
    const log = [];
    const page = mk(log);
    const inner = inFrame(page, ['iframe[title="Middle"]', 'iframe[title="Deep"]']);
    assert.notEqual(inner, page);
    assert.deepEqual(log, ['iframe[title="Middle"]', 'iframe[title="Deep"]'], "reversing this clicks in the wrong document");
  });
});

// ---- the sentences perception is allowed to print ------------------------------------------------

describe("what perception says about content it could not read", () => {
  test("an ordinary page gets not one extra character", () => {
    // Most pages have no frames and no closed roots. If this ever returns a line for them, every
    // model call in the product carries a paragraph about embedded content that is not there.
    assert.deepEqual(embeddedNotes({ frames: [], closed: [] }), []);
    assert.deepEqual(embeddedNotes({}), []);
  });

  test("a frame that could NOT be read is named, and the absence is disclaimed", () => {
    const out = embeddedNotes({ frames: [{ label: "Payment form", url: "", chain: [], read: false, count: 0 }], closed: [] }).join("\n");
    assert.match(out, /Payment form/, "an unnamed frame is one nobody can act on");
    assert.match(out, /could NOT be read/);
    assert.match(out, /Nothing above is evidence that a control does not exist/,
      "this sentence is the entire point: without it the model reports a working button as missing");
  });

  test("a closed shadow root is named as unreadable, not omitted", () => {
    const out = embeddedNotes({ frames: [], closed: ["my-widget"] }).join("\n");
    assert.match(out, /closed shadow root/);
    assert.match(out, /<my-widget>/);
    assert.match(out, /cannot be read by/, "the honest claim is 'I could not look', never 'it is not there'");
  });

  test("a frame that WAS read says so, so the elements above are attributable", () => {
    const out = embeddedNotes({ frames: [{ label: "Payment form", url: "http://x/pay", chain: [], read: true, count: 3 }] }).join("\n");
    assert.match(out, /Payment form/);
    assert.match(out, /http:\/\/x\/pay/);
    assert.match(out, /3 elements are listed above/);
  });
});

// ---- the recording format ------------------------------------------------------------------------

describe("a recording may name a frame, and one that does not still replays", () => {
  const wrap = (step) => JSON.stringify({ startUrl: "http://x/", proof: "ok", steps: [step] });

  test("a step with no frame is accepted, unchanged, forever", () => {
    const { plan, problem } = readPlan(wrap({ kind: "click", role: "button", name: "Pay" }));
    assert.equal(problem, "");
    assert.equal(plan.steps[0].frame, undefined);
  });

  test("a frame chain is accepted", () => {
    const { plan, problem } = readPlan(wrap({ kind: "click", role: "button", name: "Pay", frame: ['iframe[title="Payment form"]'] }));
    assert.equal(problem, "");
    assert.deepEqual(plan.steps[0].frame, ['iframe[title="Payment form"]']);
  });

  test("a malformed frame is refused, because aiming at the page instead is how a step nobody performed passes", () => {
    // A recording is untrusted input: a hand-edit, a merge, a cache entry from another version. If
    // a broken `frame` were ignored, inFrame() would fall back to the page — where a control with
    // the same name may well exist — and the run would go green having exercised the wrong one.
    for (const bad of ["iframe[title=x]", [], [""], [123], {}, null]) {
      const { plan, problem } = readPlan(wrap({ kind: "click", role: "button", name: "Pay", frame: bad }));
      assert.equal(plan, null, `${JSON.stringify(bad)} was accepted as a frame`);
      assert.match(problem, /frame/);
    }
  });
});

// ---- the measurement, against a real browser -----------------------------------------------------

describe("perception, measured against real embedded content", () => {
  test("(f) a normal page is untouched: no embedded section at all", noBrowser, async () => {
    const page = await open("/control");
    try {
      const snap = await perceive(page);
      assert.ok(named(snap, "button", "Place order"), "the control case must keep working");
      assert.deepEqual(snap.frames, []);
      assert.deepEqual(snap.closed, []);
      assert.ok(!/EMBEDDED CONTENT/.test(render(snap)), "a page with nothing embedded must not be told about embedded content");
    } finally {
      await page.close();
    }
  });

  test("(a) a control inside a same-origin iframe is listed, attributed, and clickable", noBrowser, async () => {
    const page = await open("/same");
    try {
      const snap = await perceive(page);
      const el = named(snap, "button", "Pay $20.00");
      assert.ok(el, `the button inside the frame is missing from perception:\n${render(snap)}`);
      assert.deepEqual(el.frame, ['iframe[title="Payment form"]'], "an element with no frame chain cannot be clicked or recorded");
      assert.match(render(snap), /in frame "Payment form"/, "an unattributed element produces a step label that lies about where it happened");

      // The requirement is not "we stored a selector" — it is that the click lands.
      await inFrame(page, el.frame).getByRole(el.role, { name: el.name, exact: true }).click({ timeout: 5000 });
      assert.match(await visibleText(page), /Payment received in full/, "the click did not reach the control inside the frame");
    } finally {
      await page.close();
    }
  });

  test("(b) a cross-origin iframe reads exactly the same — a different port is a different process", noBrowser, async () => {
    const page = await open("/cross");
    try {
      const snap = await perceive(page);
      const el = named(snap, "button", "Pay $20.00");
      assert.ok(el, `the button inside the cross-origin frame is missing:\n${render(snap)}`);
      assert.ok(snap.frames.some((f) => f.read && f.url.startsWith(CROSS)), "the frame's real origin must be reported");
      await inFrame(page, el.frame).getByRole(el.role, { name: el.name, exact: true }).click({ timeout: 5000 });
    } finally {
      await page.close();
    }
  });

  test("(c) an open shadow root is pierced, and its text reaches the proof check", noBrowser, async () => {
    const page = await open("/open-shadow");
    try {
      const snap = await perceive(page);
      // Playwright already pierces open roots; this is the regression guard on that, plus the half
      // that was NOT working: innerText walks the DOM tree, not the flat tree, so a confirmation
      // rendered inside a web component was invisible to replay()'s proof check.
      assert.ok(named(snap, "button", "Apply coupon"), `open shadow content should be readable:\n${render(snap)}`);
      assert.match(await visibleText(page), /Discount applied: 10% off\./,
        "a proof quoted from inside a web component would report outcome-changed on every replay, forever");
      assert.deepEqual(snap.closed, [], "an OPEN root is read, so warning about it would be a false alarm on every component library");
    } finally {
      await page.close();
    }
  });

  test("(d) a closed shadow root is declared unreadable instead of pretending the page is empty", noBrowser, async () => {
    const page = await open("/closed-shadow");
    try {
      const snap = await perceive(page);
      assert.equal(named(snap, "button", "Secret button"), undefined, "a closed root genuinely cannot be read; claiming otherwise would be worse");
      assert.deepEqual(snap.closed, ["my-widget"], "the host must be identified, or the note is unactionable");
      const out = render(snap);
      assert.match(out, /closed shadow root/);
      assert.match(out, /Nothing above is evidence that a control does not exist/,
        "silence here is exactly what makes the agent report a working control as missing");
    } finally {
      await page.close();
    }
  });

  test("closed-root detection stays silent on a page that has none", noBrowser, async () => {
    // The cheap heuristic — 'childless but has a box' — flagged a 2px rule, a spacer, an avatar,
    // an <img>, a <canvas> and an <input> on an ordinary page. A warning that cries wolf about
    // hidden content trains the agent to ignore the one that matters.
    const page = await open("/control");
    try {
      assert.deepEqual(await closedShadowRoots(page), []);
    } finally {
      await page.close();
    }
  });

  test("(e) an iframe inside an iframe is reached by chaining, innermost last", noBrowser, async () => {
    const page = await open("/nested");
    try {
      const snap = await perceive(page);
      const el = named(snap, "button", "Pay $20.00");
      assert.ok(el, `the button two frames deep is missing:\n${render(snap)}`);
      assert.deepEqual(el.frame, ['iframe[title="Middle"]', 'iframe[title="Deep"]']);
      assert.ok(named(snap, "button", "Middle button"), "the middle frame's own controls are part of the page too");
      await inFrame(page, el.frame).getByRole(el.role, { name: el.name, exact: true }).click({ timeout: 5000 });
      assert.match(await visibleText(page), /Payment received in full/, "the chained click did not land two frames deep");
    } finally {
      await page.close();
    }
  });

  test("a frame we do not read is still announced", noBrowser, async () => {
    // The minimum honest behaviour, forced here by refusing to read any frame at all. Whatever the
    // reason a frame goes unread — a budget, a detached element, a wedged document — the agent has
    // to be told the document exists, or it reports its contents as absent from the application.
    const page = await open("/same");
    try {
      const r = await readFrames(page, flatten, { frameCap: 0 });
      assert.equal(r.elements.length, 0);
      assert.equal(r.frames.length, 1, "the frame must appear even when nothing inside it was read");
      assert.equal(r.frames[0].read, false);
      assert.match(embeddedNotes({ frames: r.frames }).join("\n"), /could NOT be read/);
    } finally {
      await page.close();
    }
  });

  test("the page text keeps the main document first and uncapped, then adds what was hidden", noBrowser, async () => {
    // replay() checks plan.proof against this. Appending rather than replacing is what guarantees
    // that every recording made before today still matches byte for byte.
    const page = await open("/same");
    try {
      const main = await page.evaluate(() => document.body.innerText);
      const all = await visibleText(page);
      assert.ok(all.startsWith(main), "the main document must stay a prefix, or an old proof can stop matching");
      assert.match(main, /Checkout/);
      assert.ok(!/Card details/.test(main), "this test is pointless if innerText already contained the frame");
      assert.match(all, /Card details/, "the frame's text is what an agent testing an embedded checkout will quote");
    } finally {
      await page.close();
    }
  });
});

// ---- record once, replay for nothing --------------------------------------------------------------

describe("a control inside an iframe records and replays with no model call", () => {
  /** Drive testCmd with a scripted model, exactly as flake.test.mjs and verdict.test.mjs do. */
  async function run(script, opts = {}) {
    const realFetch = globalThis.fetch;
    const key = process.env.ANTHROPIC_API_KEY;
    process.env.ANTHROPIC_API_KEY = "sk-ant-test";
    const runs = [];
    const lines = [];
    let turn = 0;
    globalThis.fetch = async (target, init = {}) => {
      assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called here");
      return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content: script(++turn, JSON.parse(init.body)) }), text: async () => "" };
    };
    try {
      const code = await testCmd({
        url: `${BASE}/same`, test: "the embedded payment form takes a card", maxSteps: 4,
        evidenceDir: scratch(), retries: 0, log: (...a) => lines.push(a.join(" ")),
        onRun: (r) => runs.push(r), ...opts,
      });
      return { code, runs, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
    } finally {
      globalThis.fetch = realFetch;
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
  }

  test("the full cycle: the agent clicks inside the frame, and the replay needs no key and no model", noBrowser, async () => {
    const plan = path.join(scratch(), "embedded-checkout.json");

    // --- RECORD. The model is only told a ref; the ref it is given must be the one inside the
    // frame, which is the thing that was impossible before. So resolve it from what perception
    // actually rendered, and fail loudly if the frame's button is not on offer.
    const seen = [];
    const first = await run((turn, body) => {
      if (turn === 1) {
        const shown = String(body.messages[0].content);
        seen.push(shown);
        const m = /\s(e\d+) button "Pay \$20\.00" in frame "Payment form"/.exec(shown);
        assert.ok(m, `the frame's button was never offered to the model:\n${shown}`);
        return [{ type: "tool_use", id: "t1", name: "click", input: { ref: m[1], why: "pay" } }];
      }
      return [{ type: "tool_use", id: "t2", name: "finish", input: { passed: true, why: "The frame confirmed the payment.", proof: "Payment received in full." } }];
    }, { plan });

    assert.equal(first.code, 0, first.out);
    assert.equal(first.runs.at(-1).status, "passed");
    assert.equal(first.runs.at(-1).mode, "agent");
    assert.match(first.out, /in frame "Payment form"/, "the step label must say which document the click happened in");

    // --- WHAT WAS RECORDED.
    assert.equal(existsSync(plan), true, "the run passed and the proof is real, so a recording must exist");
    const written = JSON.parse(readFileSync(plan, "utf8"));
    const step = written.steps.find((s) => s.kind === "click");
    assert.ok(step, `no click was recorded:\n${JSON.stringify(written, null, 2)}`);
    assert.deepEqual(step.frame, ['iframe[title="Payment form"]'],
      "without the frame on the step, the replay looks for this button on the outer page and goes stale every run");
    assert.equal(written.proof, "Payment received in full.");

    // --- REPLAY. No API key at all, and any model call is an assertion failure — this is the
    // claim the whole product rests on, so it is proved by making the model unreachable rather
    // than by counting calls after the fact.
    const realFetch = globalThis.fetch;
    const key = process.env.ANTHROPIC_API_KEY;
    delete process.env.ANTHROPIC_API_KEY;
    const runs = [];
    const lines = [];
    globalThis.fetch = async (target) => {
      throw new Error(`the replay called out to ${target}; a recorded run must cost nothing`);
    };
    try {
      const code = await testCmd({
        url: `${BASE}/same`, test: "the embedded payment form takes a card", plan,
        evidenceDir: scratch(), retries: 0, log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r),
      });
      const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
      assert.equal(code, 0, out);
      assert.equal(runs.at(-1).status, "passed", out);
      assert.equal(runs.at(-1).mode, "replay", "if this says agent, the recording did not replay and the run cost full price");
      assert.match(out, /no model calls/);
    } finally {
      globalThis.fetch = realFetch;
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
  });
});
