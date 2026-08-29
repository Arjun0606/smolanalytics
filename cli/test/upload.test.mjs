// FILE UPLOAD: "upload a receipt and confirm it appears in the list", with no file on anyone's disk.
//
// THE REQUIREMENTS THESE TESTS STATE:
//
//   1. The agent can attach a file to a file input (accept="image/*"), to one with no accept at
//      all, and to a button that opens a picker. All three, driven in a real browser.
//   2. The file is FABRICATED from the control's own accept attribute, and it is a real file of
//      that type — the PNG and the JPEG are decoded by a real browser here, and the PDF's
//      cross-reference table is walked and checked against the bytes it indexes. A "PNG" nothing
//      can open is a fixture that turns every upload test red for the wrong reason.
//   3. The fixture is DETERMINISTIC: the same accept produces byte-identical files in a separate
//      process, and different accepts produce genuinely different files. The second half matters
//      as much as the first — a generator that returned one text file for every accept would sail
//      through any same-in-same-out check.
//   4. An upload step is recordable and replayable with ZERO model calls, and the fixture is
//      REGENERATED at replay time rather than stored. Proved by deleting the fixture directory
//      between the recording and the replay, so a replay that leaned on the recording's leftovers
//      cannot pass.
//   5. An upload the app REJECTS is `failed` with the app's own sentence — not `errored`, which
//      would say our runner broke, and not a fixture path in the reason nobody can act on.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  FIXTURE_DIR,
  UPLOAD_TOOL,
  fixtureFor,
  pdfBytes,
  performUpload,
  pngBytes,
  uploadLabel,
  uploadNotes,
  uploadTargets,
  writeFixture,
} from "../lib/upload.mjs";
import { compile, readPlan, testCmd } from "../lib/test.mjs";
import { ENGINES, launchEngine } from "../lib/engines.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const PNG_SIZE = pngBytes().length;

// ---- the app under test --------------------------------------------------------------------------
//
// Three routes, one server: the ordinary form, a button that opens a picker, and a form whose app
// REJECTS what it is given. Served rather than setContent, because replay() begins by navigating to
// the recording's start URL and would erase an injected document.

const ACCEPTING = `<!doctype html><meta charset="utf-8"><title>Expenses</title>
<h1>Expense report</h1>
<form>
  <label>Receipt image <input id="a" type="file" accept="image/*"></label>
  <label>Any attachment <input id="b" type="file"></label>
</form>
<ul id="list"></ul>
<script>
for (const id of ['a','b']) document.getElementById(id).addEventListener('change', (e) => {
  for (const f of e.target.files) {
    const li = document.createElement('li');
    li.textContent = 'Attached ' + f.name + ' (' + f.size + ' bytes)';
    document.getElementById('list').appendChild(li);
  }
});
</script>`;

const PICKER = `<!doctype html><meta charset="utf-8"><title>Documents</title>
<h1>Supporting documents</h1>
<input id="hidden" type="file" accept="application/pdf" style="display:none">
<button type="button" id="pick">Choose a document</button>
<ul id="list"></ul>
<script>
document.getElementById('pick').onclick = () => document.getElementById('hidden').click();
document.getElementById('hidden').addEventListener('change', (e) => {
  for (const f of e.target.files) {
    const li = document.createElement('li');
    li.textContent = 'Attached ' + f.name + ' (' + f.size + ' bytes)';
    document.getElementById('list').appendChild(li);
  }
});
</script>`;

// The app's own rule, and its own words for breaking it. A 16x16 fixture is exactly what
// accept="image/*" asked for, and this app still says no — which is a FAILED test, not a broken
// runner, and the difference is the whole point of the last test in this file.
const STRICT = `<!doctype html><meta charset="utf-8"><title>Expenses</title>
<h1>Expense report</h1>
<label>Receipt image <input id="a" type="file" accept="image/*"></label>
<p id="err"></p>
<ul id="list"></ul>
<script>
document.getElementById('a').addEventListener('change', async (e) => {
  const f = e.target.files[0];
  if (!f) return;
  const img = new Image();
  img.src = URL.createObjectURL(f);
  try { await img.decode(); } catch {
    document.getElementById('err').textContent = 'That file is not an image we can read.';
    return;
  }
  if (img.naturalWidth < 500 || img.naturalHeight < 500) {
    document.getElementById('err').textContent = 'That image is too small — receipts must be at least 500 by 500.';
    return;
  }
  const li = document.createElement('li');
  li.textContent = 'Attached ' + f.name;
  document.getElementById('list').appendChild(li);
});
</script>`;

// An upload widget shipped as an embed, which is how a great many of them arrive.
const EMBEDDED = `<!doctype html><meta charset="utf-8"><title>Claim</title>
<h1>File a claim</h1>
<iframe title="Document uploader" src="/inner" width="400" height="200"></iframe>`;

const INNER = `<!doctype html><meta charset="utf-8"><title>Uploader</title>
<label>Proof of purchase <input id="a" type="file" accept="application/pdf"></label>
<ul id="list"></ul>
<script>
document.getElementById('a').addEventListener('change', (e) => {
  for (const f of e.target.files) {
    const li = document.createElement('li');
    li.textContent = 'Attached ' + f.name + ' (' + f.size + ' bytes)';
    document.getElementById('list').appendChild(li);
  }
});
</script>`;

const ROUTES = { "/": ACCEPTING, "/picker": PICKER, "/strict": STRICT, "/embedded": EMBEDDED, "/inner": INNER };

const server = createServer((req, res) => {
  const body = ROUTES[req.url.split("?")[0]] || ACCEPTING;
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(body);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
// closeAllConnections BEFORE close: a keep-alive socket a browser left open holds close() open
// forever and the whole file hangs instead of finishing.
after(() => new Promise((r) => { server.closeAllConnections(); server.close(() => r()); }));

let browser = null;
async function open(route = "/") {
  browser ??= await chromium.launch();
  const page = await browser.newPage();
  await page.goto(base + route, { waitUntil: "domcontentloaded" });
  return page;
}
after(async () => { await browser?.close(); });

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-upload-"));

// ---- the fabricated files are real files -----------------------------------------------------------

describe("the fixtures are files, not blobs shaped like them", () => {
  test("the PNG decodes in a real browser, at the size it claims", noBrowser, async () => {
    // A PNG assembled by hand — CRC32, Adler-32, stored deflate blocks — is either right or it is
    // an unopenable file that would make every upload test fail for a reason that has nothing to do
    // with uploading. Chromium's decoder is the judge, not this file's comments.
    const page = await open();
    const dims = await page.evaluate(async (b64) => {
      const img = new Image();
      img.src = "data:image/png;base64," + b64;
      await img.decode();
      return [img.naturalWidth, img.naturalHeight];
    }, pngBytes().toString("base64"));
    assert.deepEqual(dims, [16, 16]);
    await page.close();
  });

  test("the JPEG decodes in a real browser too", noBrowser, async () => {
    const page = await open();
    const dims = await page.evaluate(async (b64) => {
      const img = new Image();
      img.src = "data:image/jpeg;base64," + b64;
      await img.decode();
      return [img.naturalWidth, img.naturalHeight];
    }, fixtureFor(".jpg").bytes.toString("base64"));
    assert.deepEqual(dims, [16, 16], "the embedded JPEG constant is not a JPEG any more");
    await page.close();
  });

  test("the PNG's signature and chunk order are what a decoder looks for first", () => {
    const b = pngBytes();
    assert.deepEqual([...b.subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    assert.equal(b.subarray(12, 16).toString("ascii"), "IHDR");
    assert.equal(b.subarray(b.length - 8, b.length - 4).toString("ascii"), "IEND");
  });

  test("the PDF's cross-reference table really indexes its own objects", () => {
    // The part hand-written PDFs get wrong. Every xref entry is a BYTE OFFSET and every entry is
    // exactly twenty bytes wide; get either wrong and the file opens as blank or not at all. This
    // walks the table the way a reader does instead of trusting that the offsets were computed.
    const text = pdfBytes().toString("latin1");
    assert.ok(text.startsWith("%PDF-1."), "no header");
    assert.ok(text.trimEnd().endsWith("%%EOF"), "no trailer marker");

    const table = /\nxref\n0 (\d+)\n([\s\S]*?)trailer\n/.exec(text);
    assert.ok(table, "no xref table");
    const rows = table[2].split("\n").filter((r) => r.length);
    assert.equal(rows.length, Number(table[1]), "the table does not hold the number of entries it declares");
    for (const row of rows) assert.equal(row.length + 1, 20, `an xref entry must be exactly 20 bytes, got ${row.length + 1}: ${JSON.stringify(row)}`);

    // Entry 0 is the free-list head; entries 1..n must land exactly on "<n> 0 obj".
    assert.match(rows[0], /^0000000000 65535 f $/);
    rows.slice(1).forEach((row, i) => {
      const off = Number(row.slice(0, 10));
      assert.ok(text.startsWith(`${i + 1} 0 obj`, off), `xref says object ${i + 1} is at ${off}, where the file says ${JSON.stringify(text.slice(off, off + 14))}`);
    });

    const startxref = /startxref\n(\d+)\n%%EOF/.exec(text);
    assert.ok(startxref, "no startxref");
    assert.ok(text.startsWith("xref\n", Number(startxref[1])), "startxref does not point at the table");
  });
});

// ---- deterministic, and actually different per accept ----------------------------------------------

const sha = (b) => createHash("sha256").update(b).digest("hex");
const ACCEPTS = ["image/*", ".jpg", "application/pdf", "text/csv", "application/json", ""];

describe("the same page always produces the same file", () => {
  test("a SEPARATE PROCESS builds byte-identical fixtures", () => {
    // In-process equality proves nothing about a clock, a random number or a pid leaking in — the
    // function could simply be caching. A second process shares none of that.
    const lib = new URL("../lib/upload.mjs", import.meta.url).href;
    const script =
      `import { fixtureFor } from ${JSON.stringify(lib)};` +
      `import { createHash } from "node:crypto";` +
      `console.log(JSON.stringify(${JSON.stringify(ACCEPTS)}.map((a) => { const f = fixtureFor(a); return [f.name, createHash("sha256").update(f.bytes).digest("hex")]; })));`;
    const out = execFileSync(process.execPath, ["--input-type=module", "-e", script], { encoding: "utf8", timeout: 60_000 });
    const theirs = JSON.parse(out);
    const mine = ACCEPTS.map((a) => { const f = fixtureFor(a); return [f.name, sha(f.bytes)]; });
    assert.deepEqual(theirs, mine, "a fixture that differs between processes cannot be regenerated at replay time");
  });

  test("different accepts produce genuinely different files, not one file with six names", () => {
    // The order-independence bug this project has already shipped once: a check that cannot fail
    // because the data it uses is symmetric. Distinct NAMES would pass even if every file held the
    // same bytes, so the digests are compared too.
    const built = ACCEPTS.map((a) => fixtureFor(a));
    assert.equal(new Set(built.map((f) => f.name)).size, ACCEPTS.length, JSON.stringify(built.map((f) => f.name)));
    assert.equal(new Set(built.map((f) => sha(f.bytes))).size, ACCEPTS.length, "two accepts were served the same bytes");
  });

  test("the accept attribute picks the type, in the order the page wrote it", () => {
    assert.equal(fixtureFor("image/*").name, "smolanalytics-test.png");
    assert.equal(fixtureFor(".pdf").name, "smolanalytics-test.pdf");
    assert.equal(fixtureFor("image/png,application/pdf").name, "smolanalytics-test.png");
    assert.equal(fixtureFor("application/pdf,image/png").name, "smolanalytics-test.pdf", "the page's first choice is the page's first choice");
    assert.equal(fixtureFor("").name, "smolanalytics-test.txt");
    assert.equal(fixtureFor(null).name, "smolanalytics-test.txt");
  });

  test("an accept we cannot honour is attached anyway, and SAYS it is improvised", () => {
    // Silence here has two bad ends: refusing would report "this runner cannot test your upload"
    // for a form that might well accept it, and attaching quietly would report the app's correct
    // rejection as a bug in the app.
    const f = fixtureFor(".docx");
    assert.equal(f.name, "smolanalytics-test.docx", "the name has to match what was asked for or it is rejected before the app sees it");
    assert.equal(f.improvised, true);
    assert.match(f.why, /cannot fabricate/);
    assert.match(f.why, /about this fixture, not about your upload flow/, "whoever reads the failure must be told which of the two it is");
    assert.match(uploadLabel(f), /smolanalytics-test\.docx/);
  });

  test("a hostile accept cannot escape the fixture directory", () => {
    for (const nasty of ["../../etc/passwd", ".../..", "image/../../x", "."]) {
      const f = fixtureFor(nasty);
      assert.ok(!f.name.includes("/") && !f.name.includes("\\") && !f.name.includes(".."), `${nasty} produced ${f.name}`);
    }
  });

  test("writeFixture puts the bytes where it says it did", () => {
    const dir = scratch();
    const f = writeFixture("image/*", dir);
    assert.equal(f.path, path.join(dir, "smolanalytics-test.png"));
    assert.deepEqual(readFileSync(f.path), pngBytes());
  });
});

// ---- attaching a file in a real browser -------------------------------------------------------------

describe("performUpload, against a real page", () => {
  test("a file input that asks for an image gets an image", noBrowser, async () => {
    const page = await open("/");
    const r = await performUpload(page, page.getByRole("button", { name: "Receipt image", exact: true }));
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.png");
    // The APP saw it, not just Playwright: the size the page reports is the fixture's own.
    assert.match(await page.locator("#list").innerText(), new RegExp(`Attached smolanalytics-test\\.png \\(${PNG_SIZE} bytes\\)`));
    await page.close();
  });

  test("a file input with no accept at all still gets a file", noBrowser, async () => {
    const page = await open("/");
    const r = await performUpload(page, page.getByRole("button", { name: "Any attachment", exact: true }));
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.txt", "with nothing asked for, the smallest honest thing is a text file");
    assert.match(await page.locator("#list").innerText(), /Attached smolanalytics-test\.txt/);
    await page.close();
  });

  test("a BUTTON that opens a picker gets the file the hidden input asked for", noBrowser, async () => {
    // The accept lives on an input the agent can never see — it is display:none, so it is not in
    // the accessibility tree at all. Reading it off the file chooser is the only way this control
    // gets the right kind of file, and a .txt here would be a fixture chosen by guessing.
    const page = await open("/picker");
    const r = await performUpload(page, page.getByRole("button", { name: "Choose a document", exact: true }));
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.pdf", "the hidden input asks for application/pdf");
    assert.match(await page.locator("#list").innerText(), /Attached smolanalytics-test\.pdf/);
    await page.close();
  });

  test("a control that opens no picker fails the STEP with a sentence, and never throws", noBrowser, async () => {
    const page = await open("/picker");
    const r = await performUpload(page, page.getByRole("heading", { name: "Supporting documents", exact: true }), { timeout: 1500 });
    assert.equal(r.ok, false);
    assert.match(r.detail, /did not open a file picker|not visible|intercepts/i, r.detail);
    // A failed step is something the agent judges. The runner returning {ok:false} rather than
    // throwing is what keeps it out of the `errored` bucket.
    await page.close();
  });
});

describe("the agent is told these controls are here", () => {
  test("a page with file inputs names them and their accepts", noBrowser, async () => {
    const page = await open("/");
    const targets = await uploadTargets(page);
    assert.deepEqual(targets, [
      { name: "Receipt image", accept: "image/*" },
      { name: "Any attachment", accept: "" },
    ]);
    const lines = uploadNotes(targets).join("\n");
    assert.match(lines, /FILE UPLOADS/);
    assert.match(lines, /"Receipt image" \(accepts image\/\*\)/);
    assert.match(lines, /"Any attachment" \(accepts any file\)/);
    // WHY THE NOTE EXISTS: a file input's role is `button`, and clicking one is a silent no-op —
    // the click reports "done", the page is unchanged, and the agent concludes the feature is
    // broken. Without this instruction the upload tool is a tool nobody uses.
    assert.match(lines, /never click/);
    await page.close();
  });

  test("an uploader inside an iframe is found, and can be uploaded to", noBrowser, async () => {
    // ariaSnapshot renders a whole embedded uploader as the single word "iframe", so without this
    // the note would say "no file uploads on this page" about a page whose only feature is one.
    const page = await open("/embedded");
    await page.frameLocator('iframe[title="Document uploader"]').getByRole("button", { name: "Proof of purchase", exact: true }).waitFor();
    assert.deepEqual(await uploadTargets(page), [{ name: "Proof of purchase", accept: "application/pdf" }]);

    const r = await performUpload(page, page.frameLocator('iframe[title="Document uploader"]').getByRole("button", { name: "Proof of purchase", exact: true }));
    assert.equal(r.ok, true, JSON.stringify(r));
    assert.equal(r.file.name, "smolanalytics-test.pdf");
    assert.match(await page.frameLocator('iframe[title="Document uploader"]').locator("#list").innerText(), /Attached smolanalytics-test\.pdf/);
    await page.close();
  });

  test("a page with no file inputs gains not one character", noBrowser, async () => {
    const page = await open("/picker");
    // The picker page's input is display:none but still an input[type=file], so it IS found — the
    // "no file inputs" case is the empty list, which is what almost every page in the world is.
    assert.deepEqual(uploadNotes([]), []);
    assert.deepEqual(uploadNotes(null), []);
    assert.deepEqual(uploadNotes("nonsense"), []);
    await page.close();
  });
});

// ---- recording and replaying an upload ---------------------------------------------------------------

describe("an upload is a recordable step", () => {
  test("the recording holds the CONTROL and neither a path nor any bytes", () => {
    const steps = [{ ok: true, action: { kind: "upload" }, target: { role: "button", name: "Receipt image" } }];
    const plan = compile("http://x/", steps, "Attached");
    assert.deepEqual(plan.steps, [{ kind: "upload", role: "button", name: "Receipt image" }]);
    // A path would break on the next machine; bytes would put a blob in somebody's repository and
    // freeze the wrong file type the day the form starts asking for a PDF.
    const json = JSON.stringify(plan);
    assert.ok(!/tmp|\/var\/|bytes|base64/i.test(json), `the recording leaked the fixture: ${json}`);
  });

  test("readPlan accepts a well-formed upload step and refuses a broken one", () => {
    const ok = readPlan(JSON.stringify({ startUrl: "http://x/", proof: "p", steps: [{ kind: "upload", role: "button", name: "Receipt image" }] }));
    assert.equal(ok.problem, "");
    const bad = readPlan(JSON.stringify({ startUrl: "http://x/", proof: "p", steps: [{ kind: "upload", role: "button" }] }));
    assert.equal(bad.plan, null);
    assert.match(bad.problem, /missing name/);
  });

  test("the model is offered the tool, and is not asked to name a file", () => {
    assert.equal(UPLOAD_TOOL.name, "upload");
    assert.deepEqual(Object.keys(UPLOAD_TOOL.input_schema.properties).sort(), ["ref", "why"]);
    assert.match(UPLOAD_TOOL.description, /do NOT choose or name a file/i);
  });
});

// ---- the whole path: a scripted model, a real browser, a recording, a free replay --------------------

/** The tool_result text the model was last shown — the page as the runner rendered it. */
function lastObservation(messages) {
  const last = messages[messages.length - 1];
  if (!last) return "";
  if (typeof last.content === "string") return last.content;
  return (last.content || []).filter((b) => b.type === "tool_result").map((b) => String(b.content)).join("\n");
}

/** The ref of an element the agent was actually shown, read out of the rendered ELEMENTS list. */
function refFor(observed, name) {
  const m = new RegExp(`(e\\d+) \\w+ ${JSON.stringify(name).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`).exec(observed);
  assert.ok(m, `the agent was never shown a control named ${name}:\n${observed}`);
  return m[1];
}

const call = (name, input) => [{ type: "tool_use", id: `t${Math.random().toString(36).slice(2)}`, name, input }];

/**
 * One testCmd run with the model scripted in-process. `script(turn, observed)` returns tool_use
 * blocks; `observed` is what the agent was shown, so a script can only answer with what the runner
 * really put in front of it.
 */
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
    const content = script(turn, observed);
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content }), text: async () => "" };
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

test("record an upload with the agent, then replay it for nothing", noBrowser, async () => {
  const plan = path.join(scratch(), "receipt.json");

  // THE SCRIPT ANSWERS OUT OF WHAT IT WAS SHOWN. It quotes the confirmation line verbatim from the
  // page the runner rendered back to it, and if that line is not there it FAILS the run — so a
  // runner that never performed the upload, or never fed the page back, turns this test red rather
  // than getting a proof it can trivially satisfy.
  const record = await runAgent((turn, seen) => {
    if (turn === 1) return call("upload", { ref: refFor(seen, "Receipt image"), why: "attach the receipt" });
    const line = /Attached smolanalytics-test\.png \(\d+ bytes\)/.exec(seen);
    return line
      ? call("finish", { passed: true, why: "The receipt is listed on the expense report.", proof: line[0] })
      : call("finish", { passed: false, why: `no upload confirmation was on the page:\n${seen.slice(0, 400)}`, proof: "" });
  }, { url: `${base}/`, test: "upload a receipt and confirm it appears in the list", plan });

  assert.equal(record.code, 0, record.out);
  // THE NOTE REACHED THE MODEL. perceive() found the file inputs and render() put them in front of
  // the agent — without this the upload tool is a tool nobody is told to use, and the agent clicks
  // a file input instead, which does nothing at all.
  assert.match(record.shown[0], /FILE UPLOADS on this page: "Receipt image" \(accepts image\/\*\)/, record.shown[0]);
  assert.match(record.shown[0], /never click/, record.shown[0]);
  // And the result of the action told it which file was attached, not a bare "done".
  assert.match(record.shown[1], /done — attached smolanalytics-test\.png/, record.shown[1]);
  assert.match(record.out, /upload to "Receipt image" — attached smolanalytics-test\.png/, `the step line must name the file that was made:\n${record.out}`);
  assert.match(record.out, /a 16x16 PNG, because the control accepts "image\/\*"/, record.out);

  const written = JSON.parse(readFileSync(plan, "utf8"));
  assert.deepEqual(written.steps, [{ kind: "upload", role: "button", name: "Receipt image" }]);
  assert.match(written.proof, new RegExp(`^Attached smolanalytics-test\\.png \\(${PNG_SIZE} bytes\\)$`),
    "the proof has to be the app's own confirmation of THIS file, or the replay proves nothing");

  // THE FIXTURE IS DELETED. If the replay leans on the file the recording run left behind rather
  // than rebuilding it, setInputFiles cannot find a path and the replay goes stale — so this line
  // is what makes "regenerated deterministically at replay time" a claim that can fail.
  rmSync(FIXTURE_DIR, { recursive: true, force: true });
  assert.equal(existsSync(path.join(FIXTURE_DIR, "smolanalytics-test.png")), false);

  // AND NOW WITH NO MODEL AT ALL. Not "count the calls afterwards" — the key is removed and every
  // outbound call that is not the local app is an assertion failure at the moment it happens.
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  delete process.env.ANTHROPIC_API_KEY;
  let modelCalls = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    modelCalls++;
    throw new Error(`the replay called ${target}`);
  };
  const lines = [];
  const runs = [];
  let code;
  try {
    code = await testCmd({
      url: `${base}/`, test: "upload a receipt and confirm it appears in the list", plan, yes: true,
      log: (s) => lines.push(String(s)), onRun: (r) => runs.push(r),
    });
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
  const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
  assert.equal(modelCalls, 0, "the replay called the model");
  assert.equal(code, 0, out);
  assert.equal(runs.at(-1).status, "passed");
  assert.equal(runs.at(-1).mode, "replay");
  assert.match(out, /no model calls/, out);
  // The replayed upload says which file it made, exactly as the recorded one did.
  assert.match(out, /attached smolanalytics-test\.png/, out);
  // And it really rebuilt the bytes: the file the recording run left is gone, and the app's proof
  // names this exact size.
  assert.equal(readFileSync(path.join(FIXTURE_DIR, "smolanalytics-test.png")).length, PNG_SIZE);
});

test("an upload the app REJECTS is failed, in the app's own words", noBrowser, async () => {
  // THE DISTINCTION THIS TEST EXISTS FOR. The fixture is exactly what accept="image/*" asked for
  // and the attach itself worked; the APP said no. That is `failed` — the sentence did not happen —
  // and it must never be `errored`, which claims our runner broke and tells a reviewer that nothing
  // was learned about their change.
  const r = await runAgent((turn, seen) => {
    if (turn === 1) return call("upload", { ref: refFor(seen, "Receipt image"), why: "attach the receipt" });
    const m = /That image is too small[^\n]*/.exec(seen);
    // No rejection on the page the agent was shown means the upload never reached the app; passing
    // here is what makes this test capable of going red instead of agreeing with itself.
    return m
      ? call("finish", { passed: false, why: `On the expense report, uploading a receipt showed: ${m[0]}`, proof: "" })
      : call("finish", { passed: true, why: "nothing objected to the receipt", proof: "Expense report" });
  }, { url: `${base}/strict`, test: "upload a receipt and confirm it appears in the list" });

  assert.equal(r.code, 1, `a rejected upload is a failed test, and 2 would say our runner broke:\n${r.out}`);
  assert.equal(r.runs.at(-1).status, "failed");
  assert.notEqual(r.runs.at(-1).status, "errored");
  assert.match(r.runs.at(-1).reason, /at least 500 by 500/, "the reason has to be the app's own message");
  // The STEP succeeded — the file really was attached — which is how we know the failure came from
  // the app's answer and not from our action falling over.
  assert.match(r.out, /✓ +\d+ upload to "Receipt image"/, r.out);
  // And no fixture path in the verdict: nobody can act on a path in a temp directory.
  assert.ok(!/\/var\/folders|\/tmp\//.test(r.runs.at(-1).reason), r.runs.at(-1).reason);
});

// ---- the two features meet: uploading in every engine -------------------------------------------------

// Playwright's setInputFiles and its filechooser event are one API over three very different
// browsers, and "one API" is a claim about their implementations, not about ours. --browser webkit
// and an upload step landed in the same change, so the intersection is checked rather than assumed:
// the same control, the same fabricated file, byte-identical, in whatever engines this machine has.
const engineAvailable = {};
if (chromium) {
  const pw = await import("playwright");
  for (const e of ENGINES) {
    try {
      const b = await launchEngine(pw, e, { headless: true });
      await b.close();
      engineAvailable[e] = true;
    } catch (err) {
      engineAvailable[e] = String(err && err.message ? err.message : err).split("\n")[0].slice(0, 90);
    }
  }
}

for (const engine of ENGINES) {
  const skip = !chromium
    ? noBrowser.skip
    : engineAvailable[engine] === true
      ? false
      : `${engine} is not installed here — npx playwright install ${engine}. (${engineAvailable[engine]})`;

  test(`both upload shapes work in ${engine}, with the same bytes`, { skip }, async () => {
    const pw = await import("playwright");
    const b = await launchEngine(pw, engine, { headless: true });
    try {
      assert.equal(b.browserType().name(), engine, "the wrong engine was launched, so this proves nothing about it");
      const page = await b.newPage();
      await page.goto(`${base}/`, { waitUntil: "domcontentloaded" });
      const direct = await performUpload(page, page.getByRole("button", { name: "Receipt image", exact: true }));
      assert.equal(direct.ok, true, `${engine}: ${direct.detail}`);
      assert.match(await page.locator("#list").innerText(), new RegExp(`Attached smolanalytics-test\\.png \\(${PNG_SIZE} bytes\\)`),
        `${engine} received different bytes than the other engines did`);

      await page.goto(`${base}/picker`, { waitUntil: "domcontentloaded" });
      const chooser = await performUpload(page, page.getByRole("button", { name: "Choose a document", exact: true }));
      assert.equal(chooser.ok, true, `${engine}: ${chooser.detail}`);
      assert.equal(chooser.file.name, "smolanalytics-test.pdf", `${engine} read the hidden input's accept differently`);
      await page.close();
    } finally {
      await b.close();
    }
  });
}

test("uploading is documented where somebody looking for it would look", () => {
  const readme = readFileSync(new URL("../README.md", import.meta.url), "utf8");
  assert.match(readme, /### Uploading a file/, "nobody will discover a feature that is not in the README");
  assert.match(readme, /no file on\s+anybody's disk/, "the whole point — no fixture on disk — has to be the headline");
  assert.match(readme, /accept="image\/\*"/, "the README does not say the file is chosen from the accept attribute");
  assert.match(readme, /never a path and never the bytes/, "the README does not say what a recording holds");
});
