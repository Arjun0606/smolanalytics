// ADVERSARIAL: FILE UPLOAD, AND THE SAME TEST IN A SECOND BROWSER.
//
// The requirements below are written as claims that can be false, because four times now this
// repository has shipped a green suite over broken code, and every one of those four was a test
// that could not fail: a scripted model answering in whatever shape the assertion wanted, an
// order-independence check run on palindromic data, two tests asserting a bug, and a guarantee
// stated in a file header with nothing behind it.
//
// THE CLAIMS:
//
//   U1. A styled uploader is uploaded to. The visible control on a real page is a <label> or a
//       <div> and the input behind it is display:none. Measured on Playwright 1.52 in all three
//       engines, setInputFiles REFUSES both — "Node is not an HTMLInputElement" — and lib/upload.mjs
//       claimed in a comment that it accepted them. The file goes to the input, not to the thing
//       that contains it. (lib/uploadsafe.mjs)
//   U2. A label whose control lives elsewhere (`for=`) still works, through the picker. The fix for
//       U1 must not eat the fallback that carried this shape.
//   U3. A control that is NOT THERE costs the caller's timeout, not Playwright's 30-second default.
//       A renamed upload control is what every stale recording replays into, and 40 seconds of dead
//       time per test is this project's cost-explosion bug in wall-clock form.
//   U4. A DISABLED file input is refused, and the application sees nothing. setInputFiles skips the
//       actionability checks that stop click() and fill(), so an app that disables its uploader
//       until you log in used to get a green upload step.
//   U5. A drag-and-drop zone with no file input at all fails HONESTLY — a sentence naming
//       drag-and-drop — and never silently passes.
//   U6. The fabricated files are valid for their type, judged by a real decoder reading real
//       PIXELS, in every engine this machine has. A PNG whose deflate stream is wrong decodes to
//       nothing and we would blame the customer's uploader for rejecting it.
//   U7. One path, one content. FIXTURE_DIR is shared between workers and machines, so two different
//       `accept` strings must never produce the same filename with different bytes.
//   U8. No fixture is empty and no `accept` a hostile page can write escapes into the filename.
//   U9. An upload the SERVER rejects with a 500 is `failed`, in the app's own words — never
//       `errored`, which claims our runner broke, and never passed.
//   U10. Our own temp directory failing is said in OUR words and marked as ours.
//
//   X1. `--browser firefox|webkit` really drives that engine, proved through the real CLI by the
//       USER AGENT THE SERVER SAW. Nothing in this assertion comes from our own bookkeeping: a
//       launcher that ignored its argument prints the same note either way.
//   X2. The same recording, replayed in every engine this machine has, uploads BYTE-IDENTICAL
//       bytes — compared by hashing what the server received, not by reading the page.
//   X3. A recorded upload replays with ZERO model calls and delivers the identical bytes to the
//       server as the recording run did.
//   X4. An engine change is a NOTE and never a verdict, including on the outcome-changed path.
//
// WHAT IS DELIBERATELY NOT CLAIMED: an engine that is not installed here is SKIPPED with its name
// in the reason. A test that reports "webkit works" without launching WebKit is the fourth bug
// above, wearing a tick.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ENGINES, launchEngine, withEngine } from "../lib/engines.mjs";
import { FIXTURE_DIR, fixtureFor, pdfBytes, performUpload, pngBytes, writeFixture } from "../lib/upload.mjs";
import { disabledDetail, probeControl } from "../lib/uploadsafe.mjs";
import { compile, testCmd } from "../lib/test.mjs";

let pw = null;
try {
  pw = await import("playwright");
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: pw ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

// ---- the app ------------------------------------------------------------------------------------
//
// Every shape below is one somebody actually ships. The hidden-input-behind-a-styled-control pair is
// the ordinary case on the modern web and is the one that was broken.

const PAGES = {
  // A styled dropzone: the agent sees a button, and the input is display:none inside it.
  "/styled": `<div role="button" aria-label="Attach a receipt" style="padding:12px;border:1px dashed #444">
      Drop a receipt<input type="file" accept="image/*" style="display:none"></div>`,
  // The same, as a <label>, which is how most design systems do it.
  "/labelled": `<label role="button" aria-label="Attach a receipt" style="padding:12px;border:1px solid">
      Attach a receipt<input type="file" accept="application/pdf" style="display:none"></label>`,
  // A label whose control is somewhere else entirely. No descendant input: this one has to go
  // through the picker.
  "/labelfor": `<label role="button" for="far">Attach a receipt</label>
      <input id="far" type="file" accept="application/pdf" style="display:none">`,
  // Drag and drop only. There is no file input anywhere on this page.
  "/dropzone": `<div role="button" aria-label="Drag files here" tabindex="0" style="padding:24px;border:2px dashed">Drag files here</div>`,
  // Disabled by its fieldset, which is how a real form disables a step you have not reached.
  "/disabled": `<fieldset disabled><input type="file" aria-label="Attach a receipt" accept="image/*"></fieldset>`,
  // Takes many files.
  "/multiple": `<input type="file" aria-label="Attach receipts" accept="image/*" multiple>`,
  // Reads the magic bytes, the way a server that means it does.
  "/sniff": `<input type="file" aria-label="Attach a receipt" accept="application/pdf">`,
};

const CHANGE_SCRIPT = `<ul id="list"></ul><p id="err"></p>
<script>
for (const i of document.querySelectorAll('input[type=file]')) i.addEventListener('change', (e) => {
  for (const f of e.target.files) {
    const li = document.createElement('li');
    li.textContent = 'Attached ' + f.name + ' (' + f.size + ' bytes)';
    document.getElementById('list').appendChild(li);
  }
});
</script>`;

// Reads the first four bytes in the browser and says what it really got.
const SNIFF_SCRIPT = `<p id="verdict">nothing yet</p>
<script>
document.querySelector('input').addEventListener('change', async (e) => {
  const f = e.target.files[0];
  const head = new Uint8Array(await f.slice(0, 5).arrayBuffer());
  const magic = String.fromCharCode(...head);
  document.getElementById('verdict').textContent =
    magic.startsWith('%PDF-') ? 'Accepted a real PDF (' + f.size + ' bytes)' : 'Rejected: that is not a PDF, it starts ' + JSON.stringify(magic);
});
</script>`;

// Uploads the raw bytes to the server, so the SERVER can say what it received.
const sinkPage = (fail) => `<!doctype html><meta charset="utf-8"><title>Receipts</title>
<h1>Receipts</h1>
<input type="file" aria-label="Attach a receipt" accept="image/*">
<p id="out">nothing yet</p>
<script>
document.querySelector('input').addEventListener('change', async (e) => {
  const f = e.target.files[0];
  const r = await fetch('${fail ? "/boom" : "/u"}', { method: 'POST', body: f });
  document.getElementById('out').textContent = r.ok ? 'Receipt stored.' : 'Upload failed: the server returned ' + r.status + '.';
});
</script>`;

/** Every user agent that asked for a page, and every body that was POSTed to /u. */
const seenAgents = [];
const received = [];

const server = createServer((req, res) => {
  const route = req.url.split("?")[0];
  if (req.method === "POST") {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      if (route === "/boom") {
        res.writeHead(500, { "content-type": "text/plain" });
        res.end("no");
        return;
      }
      received.push({ sha: createHash("sha256").update(body).digest("hex"), size: body.length });
      res.writeHead(200, { "content-type": "text/plain" });
      res.end("ok");
    });
    return;
  }
  seenAgents.push(String(req.headers["user-agent"] || ""));
  const body =
    route === "/sink" ? sinkPage(false)
    : route === "/boom-page" ? sinkPage(true)
    : route === "/shop" ? `<!doctype html><meta charset="utf-8"><title>Shop</title><h1>Your cart</h1><p id="m">2 items.</p><button id="c">Proceed to checkout</button><script>c.onclick=()=>m.textContent='Order placed.'</script>`
    : `<!doctype html><meta charset="utf-8"><title>Receipts</title><h1>Receipts</h1>${PAGES[route] || PAGES["/styled"]}${route === "/sniff" ? SNIFF_SCRIPT : CHANGE_SCRIPT}`;
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(body);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
// closeAllConnections BEFORE close: a keep-alive socket a browser left open holds close() forever
// and this file would hang rather than finish. That has cost this project ten minutes already.
after(() => new Promise((r) => { server.closeAllConnections(); server.close(() => r()); }));

/** Which engines this machine can actually launch, decided by launching them. */
const available = {};
if (pw) {
  for (const e of ENGINES) {
    try {
      const b = await launchEngine(pw, e, { headless: true });
      await b.close();
      available[e] = true;
    } catch (err) {
      available[e] = String(err && err.message ? err.message : err).split("\n")[0].slice(0, 90);
    }
  }
}
const engineSkip = (e) => (!pw ? noBrowser.skip : available[e] === true ? false : `${e} is not installed here: ${available[e]}`);

let browser = null;
const open = async (route) => {
  browser ??= await pw.chromium.launch();
  const page = await browser.newPage();
  await page.goto(base + route, { waitUntil: "domcontentloaded" });
  return page;
};
after(async () => { await browser?.close(); });

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-advup-"));

// ---- U1, U2: the shapes real uploaders are actually built out of ----------------------------------

describe("a styled uploader — a hidden input behind a label or a div", () => {
  test("U1: the file goes to the INPUT, not to the element that merely contains it", noBrowser, async () => {
    // The claim that failed before lib/uploadsafe.mjs existed:
    //   locator.setInputFiles: Error: Node is not an HTMLInputElement
    // handed to the agent as its evidence about the customer's upload feature.
    const page = await open("/styled");
    const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 5000 });
    assert.equal(r.ok, true, `a div-wrapped hidden input must be uploadable: ${JSON.stringify(r)}`);
    assert.equal(r.file.name, "smolanalytics-test.png", "the accept on the INNER input decides the file");
    // The APP saw it. Playwright reporting success is our bookkeeping; this line is the page's.
    assert.match(await page.locator("#list").innerText(), /Attached smolanalytics-test\.png \(340 bytes\)/);
    await page.close();
  });

  test("U1b: a <label role=button> around a hidden input is the same, and its accept is honoured", noBrowser, async () => {
    const page = await open("/labelled");
    // The descendant is what must be found. Clicking a label ALSO opens the picker, so without this
    // line a probe that had gone blind to descendants would be carried by the fallback and this
    // test would agree with a broken probe. (The wrapper in U1 has no such rescue: a div does not
    // open a picker, which is why U1 is the one that pins the path.)
    const probe = await probeControl(page.getByRole("button", { name: "Attach a receipt", exact: true }), 3000);
    assert.equal(probe && probe.via, "descendant", `the hidden input inside the label was not found: ${JSON.stringify(probe)}`);
    assert.equal(probe.accept, "application/pdf");
    const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 5000 });
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.pdf", "the hidden input asks for application/pdf");
    assert.match(await page.locator("#list").innerText(), /Attached smolanalytics-test\.pdf/);
    await page.close();
  });

  test("U2: a label whose control is elsewhere still works, through the picker", noBrowser, async () => {
    // There is no descendant input here, so this must fall through to click-and-catch-the-chooser.
    // If a fix for U1 ever short-circuits that fallback, this goes red.
    const page = await open("/labelfor");
    const probe = await probeControl(page.getByRole("button", { name: "Attach a receipt", exact: true }), 3000);
    assert.equal(probe, null, "a label with for= has no descendant input, so the picker path is the only one");
    const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 5000 });
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.pdf", "the accept came off the chooser's own input");
    // THE APP, not our return value. {ok:true} is our own bookkeeping and a picker path that
    // opened the chooser and then never handed it the file would satisfy it — measured: deleting
    // chooser.setFiles left this test green until this line existed.
    assert.match(await page.locator("#list").innerText(), /Attached smolanalytics-test\.pdf/,
      "the chooser was opened but the file never reached the page");
    assert.equal(await page.evaluate(() => document.getElementById("far").files.length), 1);
    await page.close();
  });
});

// ---- U3: a control that is not there ---------------------------------------------------------------

test("U3: a control that no longer exists costs the timeout it was given, not Playwright's default", noBrowser, async () => {
  // THE REQUIREMENT: every action in this runner is capped at 10s, and a renamed upload control is
  // what a stale recording replays into. locator.evaluate() carries a 30s default of its own, which
  // is not the caller's to spend. Measured before the fix: 40.0s. The bound below is the caller's
  // timeout twice — the probe, then the click that might still catch a slow-rendering control —
  // plus room for a loaded machine.
  const page = await open("/styled");
  const t0 = Date.now();
  const r = await performUpload(page, page.getByRole("button", { name: "Renamed since the recording", exact: true }), { timeout: 2000 });
  const ms = Date.now() - t0;
  assert.equal(r.ok, false);
  assert.ok(ms < 12_000, `a missing control took ${ms}ms, which is Playwright's 30s default leaking through the probe`);
  await page.close();
});

test("U3b: the probe honours the timeout it is handed, and returns null rather than throwing", noBrowser, async () => {
  const page = await open("/styled");
  const t0 = Date.now();
  const got = await probeControl(page.getByRole("button", { name: "Not on this page", exact: true }), 1200);
  const ms = Date.now() - t0;
  assert.equal(got, null, "a probe that cannot resolve its element reports nothing, it does not throw");
  assert.ok(ms < 6000, `the probe spent ${ms}ms on a 1200ms budget`);
  await page.close();
});

// ---- U4: disabled ----------------------------------------------------------------------------------

test("U4: a disabled file input is refused, and the app never sees a file", noBrowser, async () => {
  // THE REQUIREMENT: an app that disables its uploader until you accept the terms must not get a
  // green upload step. Every other action is stopped by Playwright's actionability checks;
  // setInputFiles is not, so this is ours to refuse.
  const page = await open("/disabled");
  const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 5000 });
  assert.equal(r.ok, false, "a disabled control must not report an upload it could not have performed");
  assert.match(r.detail, /disabled/, r.detail);
  assert.ok(!r.runner, "this is a fact about their page, not our runner breaking");
  // The page's own change handler is the witness: nothing arrived.
  assert.equal(await page.locator("#list").innerText(), "", "the app received a file it had disabled the control for");
  assert.equal(await page.evaluate(() => document.querySelector("input").files.length), 0);
  await page.close();
});

test("U4b: the refusal names the control and says a user could not do it either", () => {
  const d = disabledDetail();
  assert.match(d, /disabled/);
  assert.match(d, /real user could not/i, "the agent has to know this is about the page, not about us");
  assert.ok(!/errored|error/i.test(d), "a disabled control is a failed step, not a runner outage");
});

// ---- U5: no input anywhere --------------------------------------------------------------------------

test("U5: a drag-and-drop zone with no file input fails honestly and never silently passes", noBrowser, async () => {
  const page = await open("/dropzone");
  const r = await performUpload(page, page.getByRole("button", { name: "Drag files here", exact: true }), { timeout: 2000 });
  assert.equal(r.ok, false, "there is no input on this page: reporting an upload would be a fabrication");
  assert.match(r.detail, /drag-and-drop/, `the reader has to be told WHY: ${r.detail}`);
  assert.match(r.detail, /no file was attached/, r.detail);
  // And nothing was invented: the page has no input to have received anything.
  assert.equal(await page.evaluate(() => document.querySelectorAll("input[type=file]").length), 0);
  await page.close();
});

// ---- U6: the fixtures are real files, judged by a real decoder ---------------------------------------

describe("the fabricated files decode as what they claim to be", () => {
  for (const engine of ENGINES) {
    test(`U6: ${engine} decodes the PNG to the right size AND the right pixels`, { skip: engineSkip(engine) }, async () => {
      // NOT the header. A PNG can carry a correct IHDR and a corrupt IDAT, and every real uploader
      // that makes a thumbnail would then reject it — and we would blame their uploader. So the
      // bytes go through a real image decoder and the PIXELS come back out: 0xd8 in the corner,
      // 0x40 in the middle square, which is what pngBytes() says it drew.
      const b = await launchEngine(pw, engine, { headless: true });
      try {
        const page = await b.newPage();
        const dataUrl = `data:image/png;base64,${pngBytes().toString("base64")}`;
        const got = await page.evaluate(async (src) => {
          const img = new Image();
          img.src = src;
          await img.decode();
          const c = document.createElement("canvas");
          c.width = img.naturalWidth;
          c.height = img.naturalHeight;
          c.getContext("2d").drawImage(img, 0, 0);
          const px = (x, y) => [...c.getContext("2d").getImageData(x, y, 1, 1).data].slice(0, 3);
          return { w: img.naturalWidth, h: img.naturalHeight, corner: px(0, 0), middle: px(8, 8) };
        }, dataUrl);
        assert.deepEqual([got.w, got.h], [16, 16], `${engine} read a different size: ${JSON.stringify(got)}`);
        assert.deepEqual(got.corner, [0xd8, 0xd8, 0xd8], `${engine} decoded the wrong corner pixel: ${JSON.stringify(got)}`);
        assert.deepEqual(got.middle, [0x40, 0x40, 0x40], `${engine} decoded the wrong middle pixel: ${JSON.stringify(got)}`);
        await page.close();
      } finally {
        await b.close();
      }
    });
  }

  test("U6b: the JPEG is a JPEG from its first bytes to its last", () => {
    const j = fixtureFor("image/jpeg").bytes;
    assert.equal(j.subarray(0, 3).toString("hex"), "ffd8ff", "no SOI marker: no decoder will touch this");
    assert.equal(j.subarray(-2).toString("hex"), "ffd9", "no EOI marker: a truncated JPEG");
    assert.equal(j.subarray(6, 10).toString("latin1"), "JFIF");
  });

  test("U6c: the PDF's stream length and every xref offset are measured, not guessed", () => {
    // A hardcoded /Length or a hardcoded offset is the classic way a hand-written PDF stops opening
    // the moment somebody edits a string in it.
    const s = pdfBytes().toString("latin1");
    assert.ok(s.startsWith("%PDF-1."), "no PDF header");
    assert.ok(s.endsWith("%%EOF\n"), "no EOF marker");
    const declared = Number(/<< \/Length (\d+) >>/.exec(s)[1]);
    const stream = /stream\n([\s\S]*?)endstream/.exec(s)[1];
    assert.equal(stream.length, declared, "the content stream is not the length the PDF declares it is");
    const startxref = Number(/startxref\n(\d+)/.exec(s)[1]);
    assert.equal(s.slice(startxref, startxref + 4), "xref", "startxref does not point at the cross-reference table");
    const entries = [...s.slice(startxref).matchAll(/^(\d{10}) 00000 n $/gm)].map((m) => Number(m[1]));
    assert.equal(entries.length, 5, `expected five objects, the table indexes ${entries.length}`);
    entries.forEach((off, i) => {
      assert.equal(s.slice(off, off + `${i + 1} 0 obj`.length), `${i + 1} 0 obj`,
        `xref entry ${i + 1} points at ${JSON.stringify(s.slice(off, off + 12))}, not at object ${i + 1}`);
    });
    assert.match(s, new RegExp(`/Size ${entries.length + 1}`), "the trailer's /Size disagrees with the table");
  });

  test("U6d: a page that sniffs the magic bytes accepts what we hand it for accept=.pdf", noBrowser, async () => {
    // The attack the brief names: a control that accepts only PDFs. If the fixture were a text file
    // with a .pdf name, this app would say no and we would have reported a working uploader broken.
    const page = await open("/sniff");
    const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 5000 });
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.match(await page.locator("#verdict").innerText(), /^Accepted a real PDF \(\d+ bytes\)$/,
      "the app read the first five bytes and did not find %PDF-");
    await page.close();
  });
});

// ---- U7, U8: the fixture directory is shared, so the naming has to be a function --------------------

const HOSTILE_ACCEPTS = [
  "", "   ", ".pdf", ".PDF", "application/pdf", "image/pdf", "image/*", "image/png", ".png", ".jpg",
  ".jpeg", "image/jpg", "image/jpeg", "text/csv", ".csv", "application/csv", "application/json",
  ".json", "text/json", "text/plain", "text/*", ".txt", ".text", "*/*", "application/octet-stream",
  ".pdf,.png", "image/png,.pdf", "video/mp4", "image/heic", ".docx", ".doc x", ".Doc X", "app/vnd.foo",
  "image/svg+xml", ".tar.gz", "../../../etc/passwd", "/etc/passwd", "..", ".", "./", "x/", "/",
  "\\", "c:\\windows\\system32", ".exe", ".sh", ".html", "text/html", "\u56fe\u7247/*", ".pdf \u6587\u4ef6",
  "a".repeat(400), ".p\u0000ng", "image/*, .pdf", " .PDF , image/* ",
];

describe("one path, one content — because FIXTURE_DIR is shared", () => {
  test("U7: no two accepts produce the same filename with different bytes", () => {
    // The measured hazard, in lib/upload.mjs's own words: `image/pdf` and `application/pdf` once
    // produced two different files at one path, so with --workers > 1 one test's PDF could be read
    // as another test's text file. This is that property, stated so it can fail, over a corpus
    // deliberately full of near-misses.
    const byName = new Map();
    for (const accept of HOSTILE_ACCEPTS) {
      const f = fixtureFor(accept);
      const sha = createHash("sha256").update(f.bytes).digest("hex");
      const prev = byName.get(f.name);
      if (prev) {
        assert.equal(sha, prev.sha,
          `${JSON.stringify(accept)} and ${JSON.stringify(prev.accept)} both write ${f.name} with DIFFERENT bytes`);
      } else byName.set(f.name, { sha, accept });
    }
    // And the corpus really does collide on names, or the loop above proved nothing.
    assert.ok(byName.size < HOSTILE_ACCEPTS.length, "the corpus never reuses a filename, so this test cannot fail");
  });

  test("U7b: the same accept is the same bytes every time, in a SEPARATE process", () => {
    // In-process equality would be satisfied by a cache. A second process shares no memory.
    const script =
      `import { fixtureFor } from ${JSON.stringify(fileURLToPath(new URL("../lib/upload.mjs", import.meta.url)))};\n` +
      `import { createHash } from "node:crypto";\n` +
      `const out = {};\n` +
      `for (const a of ${JSON.stringify(HOSTILE_ACCEPTS)}) out[a] = fixtureFor(a).name + ":" + createHash("sha256").update(fixtureFor(a).bytes).digest("hex");\n` +
      `console.log(JSON.stringify(out));`;
    const r = spawnSync(process.execPath, ["--input-type=module", "-e", script], { encoding: "utf8", timeout: 60_000 });
    assert.equal(r.status, 0, r.stderr);
    const theirs = JSON.parse(r.stdout);
    for (const a of HOSTILE_ACCEPTS) {
      const f = fixtureFor(a);
      const mine = `${f.name}:${createHash("sha256").update(f.bytes).digest("hex")}`;
      assert.equal(theirs[a], mine, `${JSON.stringify(a)} is not deterministic across processes`);
    }
  });

  test("U8: no fixture is empty, and no accept escapes into the filename", () => {
    // A zero-byte upload is rejected by a great many real apps, and a filename carrying a space, a
    // slash, a NUL or a non-ASCII character breaks a Content-Disposition header or a path. The
    // fixture name is ours to keep boring.
    for (const accept of HOSTILE_ACCEPTS) {
      const f = fixtureFor(accept);
      assert.ok(f.bytes.length > 0, `${JSON.stringify(accept)} produced a 0-byte file`);
      assert.match(f.name, /^smolanalytics-test\.[a-z0-9]{1,8}$/,
        `${JSON.stringify(accept)} produced the filename ${JSON.stringify(f.name)}`);
      assert.equal(path.basename(f.name), f.name, "a fixture name must never contain a path separator");
      assert.ok(f.why && f.why.length > 4, `${JSON.stringify(accept)} has no sentence explaining its file`);
    }
  });

  test("U8b: what cannot be fabricated says so, and what can does not", () => {
    // The caveat is the difference between "your app rejected our stand-in" and "your app is
    // broken". It has to be on the improvised ones and off the real ones.
    const heic = fixtureFor("image/heic");
    assert.equal(heic.improvised, true);
    assert.match(heic.why, /cannot fabricate/);
    assert.match(heic.why, /about this fixture, not about your upload flow/);
    assert.equal(heic.name, "smolanalytics-test.heic", "the name still has to be what the control asked for");
    for (const real of [".pdf", "image/*", ".csv", ".json", ".jpg", ""]) {
      assert.equal(fixtureFor(real).improvised, false, `${JSON.stringify(real)} is a file we really can make`);
      assert.ok(!/cannot fabricate/.test(fixtureFor(real).why), `${JSON.stringify(real)} apologises for a file that is real`);
    }
  });
});

// ---- U10: our own temp directory ---------------------------------------------------------------------

test("U10: a temp directory we cannot write is OUR outage, said in our words", noBrowser, async () => {
  // Measured on a shared CI runner where /tmp/smolanalytics-uploads belonged to another account.
  // The agent's job is to decide whether the APPLICATION is broken, and `EACCES: permission denied,
  // open '/tmp/.../smolanalytics-test.png.31.tmp'` sends a reviewer into their own upload handler.
  const dir = path.join(scratch(), "locked");
  writeFileSync(path.join(path.dirname(dir), "x"), "x");
  chmodSync(path.dirname(dir), 0o500); // no write permission: mkdirSync inside it fails
  try {
    const page = await open("/multiple");
    const r = await performUpload(page, page.getByRole("button", { name: "Attach receipts", exact: true }), { dir, timeout: 5000 });
    assert.equal(r.ok, false);
    assert.equal(r.runner, true, "an EACCES in our own scratch directory is not a finding about their app");
    assert.match(r.detail, /this test runner's own temporary directory, not anything about the app under test/, r.detail);
    assert.match(r.detail, /smolanalytics could not create the file to upload/, r.detail);
    await page.close();
  } finally {
    chmodSync(path.dirname(dir), 0o700);
  }
});

test("U10b: writeFixture really puts those exact bytes at that exact path", () => {
  const dir = scratch();
  const f = writeFixture("image/*", dir);
  assert.equal(f.path, path.join(dir, "smolanalytics-test.png"));
  assert.deepEqual(readFileSync(f.path), pngBytes(), "the file on disk is not the file that was described");
  assert.equal(existsSync(`${f.path}.${process.pid}.tmp`), false, "the temp file was left behind");
});

// ---- the whole path, with a scripted model ------------------------------------------------------------

function lastObservation(messages) {
  const last = messages[messages.length - 1];
  if (!last) return "";
  if (typeof last.content === "string") return last.content;
  return (last.content || []).filter((b) => b.type === "tool_result").map((b) => String(b.content)).join("\n");
}

function refFor(observed, name) {
  const m = new RegExp(`(e\\d+) \\w+ ${JSON.stringify(name).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`).exec(observed);
  assert.ok(m, `the agent was never shown a control named ${name}:\n${observed}`);
  return m[1];
}

const call = (name, input) => [{ type: "tool_use", id: `t${Math.random().toString(36).slice(2)}`, name, input }];

async function runAgent(script, opts) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  const shown = [];
  let turn = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called from a test run");
    turn++;
    const observed = lastObservation(JSON.parse(init.body).messages);
    shown.push(observed);
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content: script(turn, observed) }), text: async () => "" };
  };
  try {
    const code = await testCmd({ yes: true, retries: 0, log: (s) => lines.push(String(s)), onRun: (r) => runs.push(r), ...opts });
    return { code, runs, shown, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

/** A run with no key at all, and every non-local call counted as it happens. */
async function runReplay(opts) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  delete process.env.ANTHROPIC_API_KEY;
  const runs = [];
  const lines = [];
  let modelCalls = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    modelCalls++;
    throw new Error(`the replay called ${target}`);
  };
  try {
    const code = await testCmd({ yes: true, log: (s) => lines.push(String(s)), onRun: (r) => runs.push(r), ...opts });
    return { code, runs, modelCalls, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

test("U9: an upload the SERVER rejects with a 500 is failed, in the app's own words", noBrowser, async () => {
  // THE DISTINCTION. The attach worked and the fixture was exactly what was asked for; the server
  // said no. `failed` — the sentence in the test did not happen. Never `errored`, which claims our
  // runner broke and tells a reviewer nothing was learned about their change.
  const r = await runAgent((turn, seen) => {
    if (turn === 1) return call("upload", { ref: refFor(seen, "Attach a receipt"), why: "attach the receipt" });
    // The script answers out of what it was SHOWN. No 500 line on the page means the upload never
    // reached the server, and this run passes — which is what lets this test go red.
    const m = /Upload failed: the server returned 500\./.exec(seen);
    return m
      ? call("finish", { passed: false, why: `The receipt was not stored: ${m[0]}`, proof: "" })
      : call("finish", { passed: true, why: "nothing went wrong", proof: "Receipts" });
  }, { url: `${base}/boom-page`, test: "upload a receipt and confirm it is stored" });

  assert.equal(r.code, 1, `a server that 500s on the upload is a failing test:\n${r.out}`);
  assert.equal(r.runs.at(-1).status, "failed");
  assert.ok(r.runs.at(-1).status !== "errored", "a 500 from THEIR server is not our runner breaking");
  assert.match(r.runs.at(-1).reason, /the server returned 500/, r.runs.at(-1).reason);
  // The step itself succeeded: attaching worked, and the failure is downstream of it.
  assert.match(r.out, /upload to "Attach a receipt" — attached smolanalytics-test\.png/, r.out);
});

test("X3: a recorded upload replays with zero model calls and delivers the identical bytes", noBrowser, async () => {
  // THE STRONGEST FORM OF THE DETERMINISM CLAIM. Not "the page said 340 bytes" and not "the file on
  // disk is the same length" — the SERVER hashes what actually arrived over the wire, on the
  // recording run and on the replay, and the two hashes have to be equal.
  const plan = path.join(scratch(), "receipt.json");
  received.length = 0;

  const record = await runAgent((turn, seen) => {
    if (turn === 1) return call("upload", { ref: refFor(seen, "Attach a receipt"), why: "attach the receipt" });
    return /Receipt stored\./.test(seen)
      ? call("finish", { passed: true, why: "The receipt was stored.", proof: "Receipt stored." })
      : call("finish", { passed: false, why: `no confirmation on the page:\n${seen.slice(0, 300)}`, proof: "" });
  }, { url: `${base}/sink`, test: "upload a receipt and confirm it is stored", plan });
  assert.equal(record.code, 0, record.out);
  assert.equal(received.length, 1, `the recording run did not upload anything: ${JSON.stringify(received)}`);

  // The recording run's leftovers are removed, so a replay that leans on them goes stale instead of
  // quietly passing on a file it did not rebuild.
  rmSync(FIXTURE_DIR, { recursive: true, force: true });

  const rep = await runReplay({ url: `${base}/sink`, test: "upload a receipt and confirm it is stored", plan });
  assert.equal(rep.modelCalls, 0, "the replay called the model");
  assert.equal(rep.code, 0, rep.out);
  assert.equal(rep.runs.at(-1).mode, "replay");
  assert.equal(rep.runs.at(-1).status, "passed");
  assert.equal(received.length, 2, `the replay never uploaded: ${JSON.stringify(received)}`);
  assert.equal(received[1].sha, received[0].sha,
    `the replayed upload delivered different bytes: ${JSON.stringify(received)}`);
  assert.equal(received[1].sha, createHash("sha256").update(pngBytes()).digest("hex"),
    "what arrived is not the PNG this runner says it fabricates");
  // And the reader is told which file it was, since nobody named one.
  assert.match(rep.out, /attached smolanalytics-test\.png/, rep.out);
});

// ---- cross-browser -------------------------------------------------------------------------------------

describe("the same test in a second browser", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));

  /**
   * spawn, never spawnSync. The app the child browses to is served by THIS process, and spawnSync
   * blocks this event loop — measured, every engine timed out on page.goto after 30s because the
   * server could not answer while the parent sat in the syscall.
   */
  const spawned = (args, env) =>
    new Promise((resolve, reject) => {
      const child = spawn(process.execPath, args, { env, stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";
      const timer = setTimeout(() => child.kill("SIGKILL"), 180_000);
      child.stdout.on("data", (d) => (stdout += d));
      child.stderr.on("data", (d) => (stderr += d));
      child.on("error", (e) => { clearTimeout(timer); reject(e); });
      child.on("close", (status) => { clearTimeout(timer); resolve({ status, stdout, stderr }); });
    });

  for (const engine of ENGINES) {
    test(`X1: --browser ${engine} through the real CLI is answered by a real ${engine}`, { skip: engineSkip(engine) }, async () => {
      // NOTHING HERE IS OUR OWN BOOKKEEPING. The note printed on a cross-engine replay is built from
      // the engine STRING, so a launcher that ignored its argument would print it just the same.
      // The user agent below is the browser's own answer, read off the server's request log, and it
      // travelled through argv, bin/, lib/ and a real process exit code to get here.
      const plan = path.join(scratch(), "shop.json");
      const recorded = [{ ok: true, action: { kind: "click" }, target: { role: "button", name: "Proceed to checkout" } }];
      writeFileSync(plan, JSON.stringify(withEngine(compile(`${base}/shop`, recorded, "Order placed."), "chromium"), null, 2));

      const before = seenAgents.length;
      const env = { ...process.env };
      delete env.ANTHROPIC_API_KEY; // a replay must need no key at all
      const r = await spawned([bin, "test", "--url", `${base}/shop`, "--test", "a shopper can check out", "--plan", plan, "--browser", engine, "--yes"], env);
      const out = `${r.stdout}${r.stderr}`.replace(/\x1b\[[0-9;]*m/g, "");
      assert.equal(r.status, 0, `the replay did not pass:\n${out}`);
      assert.match(out, /\bPASS\b/, out);

      const agents = seenAgents.slice(before);
      assert.ok(agents.length > 0, "no browser ever asked the server for the page");
      const fingerprint = { chromium: /Chrome\//, firefox: /Firefox\//, webkit: /Version\/[\d.]+ Safari/ }[engine];
      assert.ok(agents.some((ua) => fingerprint.test(ua)),
        `--browser ${engine} was answered by ${JSON.stringify(agents)}, which is not ${engine}`);
      if (engine !== "chromium") {
        assert.ok(!agents.some((ua) => /Chrome\/|HeadlessChrome/.test(ua)),
          `--browser ${engine} launched a Chromium: ${JSON.stringify(agents)}`);
      }
    });
  }

  for (const engine of ENGINES) {
    test(`X2: an upload in ${engine} puts the same bytes on the wire as anywhere else`, { skip: engineSkip(engine) }, async () => {
      // The fixture is generated per run, per engine. If any of it depended on the browser — a
      // canvas encode, a zlib the engine happened to have — the hashes would part here.
      const b = await launchEngine(pw, engine, { headless: true });
      try {
        // WHICH ENGINE THIS ACTUALLY IS, at two layers, before anything is claimed on its behalf.
        // Without these two lines the hashes below are identical in every engine BY CONSTRUCTION —
        // measured, a launchEngine that ignored its argument and returned Chromium three times left
        // this test green while calling itself a per-engine test. That is the bug this repository
        // keeps producing, and it was in this file until the mutation sweep found it.
        assert.equal(b.browserType().name(), engine, "the wrong engine was launched, so this proves nothing about it");
        const page = await b.newPage();
        const ua = await page.evaluate(() => navigator.userAgent);
        assert.match(ua, { chromium: /Chrome\//, firefox: /Firefox\//, webkit: /Version\/[\d.]+ Safari/ }[engine],
          `${engine} reported a user agent that is not ${engine}'s: ${ua}`);
        await page.goto(`${base}/sink`, { waitUntil: "domcontentloaded" });
        const before = received.length;
        const r = await performUpload(page, page.getByRole("button", { name: "Attach a receipt", exact: true }), { timeout: 10_000 });
        assert.equal(r.ok, true, `${engine}: ${JSON.stringify(r)}`);
        await page.waitForFunction(() => document.getElementById("out").textContent !== "nothing yet", null, { timeout: 10_000 });
        assert.equal(received.length, before + 1, `${engine} never reached the server`);
        assert.equal(received[before].sha, createHash("sha256").update(pngBytes()).digest("hex"),
          `${engine} delivered different bytes`);
        assert.equal(received[before].size, 340);
        await page.close();
      } finally {
        await b.close();
      }
    });
  }

  test("X4: on an outcome that changed, the engine note is offered as a cause and the status is still stale", noBrowser, async () => {
    // The builder's tests cover the pass and the broken-step stale. `outcome-changed` is the third
    // way a replay ends and it must carry the note too: a proof that stopped matching on WebKit and
    // matched on Chromium is precisely the cross-browser bug a second engine is run to find, and
    // reporting it without naming the engine sends someone hunting a copy change.
    const crossEngine = ENGINES.find((e) => e !== "chromium" && available[e] === true);
    if (!crossEngine) return; // reported by the availability line below
    const plan = path.join(scratch(), "shop.json");
    const recorded = [{ ok: true, action: { kind: "click" }, target: { role: "button", name: "Proceed to checkout" } }];
    const p = withEngine(compile(`${base}/shop`, recorded, "Order placed."), "chromium");
    p.proof = "Refund issued."; // on the page it is not, so the steps work and the outcome does not match
    writeFileSync(plan, JSON.stringify(p, null, 2));

    const r = await runReplay({ url: `${base}/shop`, test: "a shopper can check out", plan, engine: crossEngine });
    assert.equal(r.modelCalls, 0, "there is no key: nothing may have called the model");
    assert.equal(r.runs[0].status, "stale", "an outcome that no longer matches is stale, never a bug report");
    assert.match(r.runs[0].reason, /made on Chromium and was replayed on (Firefox|WebKit)/,
      `the note has to reach the project, not just the terminal: ${r.runs[0].reason}`);
    assert.match(r.runs[0].reason, /candidate cause/, r.runs[0].reason);
    assert.equal(r.code, 2, "no key and a recording that no longer settles it is errored/2, not a verdict about the app");
  });

  test("X5: the shared browser a parallel suite uses really leases the engine that was asked for", { skip: engineSkip("webkit") === false && engineSkip("firefox") === false ? false : "firefox and webkit are not both installed here" }, async () => {
    // lib/pool.mjs stands in for loadPlaywright when --workers > 1, and it is covered elsewhere with
    // a FAKE playwright — which a pool that ignored the engine would satisfy, because the fake's
    // only `launch` is the one the key names. This leases REAL browsers and reads each one's user
    // agent, and it also proves the engine is part of the lease key: three engines, three processes,
    // never one Chromium handed out three times.
    const { sharedBrowser } = await import("../lib/pool.mjs");
    const shared = sharedBrowser({ load: async () => ({ pw, problem: "" }) });
    const agents = {};
    try {
      for (const engine of ENGINES) {
        const { pw: stub } = await shared.loadBrowser(() => {}, true, engine);
        assert.deepEqual(Object.keys(stub), [engine], `the pool exposed ${JSON.stringify(Object.keys(stub))} for ${engine}`);
        const b = await stub[engine].launch({ headless: true });
        const ctx = await b.newContext();
        const page = await ctx.newPage();
        await page.setContent("<h1>x</h1>");
        agents[engine] = await page.evaluate(() => navigator.userAgent);
        await ctx.close();
        await b.close();
      }
    } finally {
      await shared.close();
    }
    assert.match(agents.chromium, /Chrome\//, agents.chromium);
    assert.match(agents.firefox, /Firefox\//, agents.firefox);
    assert.match(agents.webkit, /Version\/[\d.]+ Safari/, agents.webkit);
    assert.equal(new Set(Object.values(agents)).size, 3, "the pool handed the same browser to three different engines");
    assert.equal(shared.launched, 3, "the engine is not part of the lease key: three engines shared one process");
  });

  test("engine availability on THIS machine is reported, never assumed", () => {
    const line = ENGINES.map((e) => `${e}=${available[e] === true ? "launched" : "absent"}`).join(" ");
    console.log(`      adversarial run, engines on this machine: ${line}`);
    assert.ok(line.includes("chromium"));
  });
});
