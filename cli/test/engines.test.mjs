// CROSS-BROWSER: --browser chromium | firefox | webkit.
//
// THE REQUIREMENTS THESE TESTS STATE, so that each one can be read as a claim and each one can go
// red if the claim stops being true:
//
//   1. chromium stays the default and behaves exactly as it did.
//   2. --browser firefox really launches Firefox, --browser webkit really launches WebKit. Asserted
//      TWICE and at two different layers — Playwright's own browserType().name(), and the user
//      agent the page itself reports — because a launcher that ignored its argument and returned a
//      Chromium would satisfy any one-layer check that only asked our own code what it did.
//   3. A typo'd engine is REFUSED, exit 2, never silently the default.
//   4. A recording carries the engine it was made on, and replaying it on another engine says so
//      — in the terminal AND in the reason posted to the project — while changing no verdict.
//   5. An engine that was never downloaded produces one sentence naming the exact install command,
//      not Playwright's twelve-line Unicode box wrapped in a stack trace, and it is `errored`/2,
//      never a verdict about the app.
//
// WHICH ENGINES ARE ACTUALLY HERE. Playwright downloads browsers on demand and a machine may only
// have Chromium. The per-engine tests below SKIP with the engine named rather than passing on a
// silent fallback, because "webkit works" is a claim, and a test that reports it without launching
// WebKit is the fourth instance of the bug this project keeps producing.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  DEFAULT_ENGINE,
  ENGINES,
  engineNote,
  installCommand,
  launchEngine,
  launchProblem,
  parseEngine,
  recordedEngine,
  withEngine,
  withNote,
} from "../lib/engines.mjs";
import { compile, replay, testCmd } from "../lib/test.mjs";
import { runSuite } from "../lib/suite.mjs";
import { sharedBrowser } from "../lib/pool.mjs";

let pw = null;
try {
  pw = await import("playwright");
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: pw ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

// ---- the flag ------------------------------------------------------------------------------------

describe("--browser", () => {
  test("chromium is the default, and it is the default because nothing was passed", () => {
    assert.deepEqual(parseEngine(undefined), { engine: "chromium", problem: "" });
    assert.equal(DEFAULT_ENGINE, "chromium");
  });

  test("all three engines are accepted, however they are capitalised", () => {
    for (const e of ENGINES) assert.deepEqual(parseEngine(e), { engine: e, problem: "" });
    assert.equal(parseEngine("WebKit").engine, "webkit");
    assert.equal(parseEngine(" Firefox ").engine, "firefox");
  });

  test("a typo is refused out loud, never silently chromium", () => {
    // The person who typed --browser is the one person who explicitly asked to be told about a
    // second engine. Quietly running Chromium reports them a green suite about a browser they did
    // not ask about, which is the whole failure this refusal exists to prevent.
    for (const bad of ["webkti", "safari", "edge", ""]) {
      const r = parseEngine(bad);
      assert.equal(r.engine, "", `${JSON.stringify(bad)} was accepted`);
      assert.match(r.problem, /chromium, firefox or webkit/);
      assert.match(r.problem, /own one-off download/, "the refusal has to say why firefox and webkit are not just there");
    }
  });

  test("the install command names the engine, because `playwright install` on its own downloads all three", () => {
    assert.equal(installCommand("webkit"), "npx playwright install webkit");
    assert.equal(installCommand("firefox"), "npx playwright install firefox");
    assert.equal(installCommand("nonsense"), "npx playwright install chromium");
  });
});

// ---- a browser that was never downloaded ----------------------------------------------------------

describe("an engine that is not installed", () => {
  // Playwright's real message, box-drawing and all. This is what a first `--browser webkit` prints
  // on a machine that only ever downloaded Chromium.
  const REAL = `browserType.launch: Executable doesn't exist at /Users/x/Library/Caches/ms-playwright/webkit-2158/pw_run.sh
╔═════════════════════════════════════════════════════════════════════════╗
║ Looks like Playwright Test or Playwright was just installed or updated. ║
║ Please run the following command to download new browsers:              ║
║                                                                         ║
║     npx playwright install                                              ║
║                                                                         ║
║ <3 Playwright Team                                                      ║
╚═════════════════════════════════════════════════════════════════════════╝`;

  test("becomes one sentence with the exact command, not a twelve-line box", () => {
    const s = launchProblem("webkit", new Error(REAL));
    assert.match(s, /WebKit is not installed/);
    assert.match(s, /npx playwright install webkit/, "the engine has to be in the command, or it downloads all three");
    assert.ok(!s.includes("╔"), "Playwright's box is not our message");
    assert.ok(s.split("\n").length === 1, `one sentence, got ${s.split("\n").length} lines`);
  });

  test("every OTHER launch failure keeps Playwright's own words", () => {
    // "Host system is missing dependencies to run browsers" is the commonest CI failure and its own
    // message is the useful one. Swallowing it into "not installed" would send somebody to run an
    // install that already succeeded.
    const other = new Error("browserType.launch: Host system is missing dependencies to run browsers");
    assert.equal(launchProblem("webkit", other), "", "an unrelated failure must not be relabelled");
  });

  test("launchEngine turns it into that sentence, and testCmd reports errored/2 — never a verdict about the app", async () => {
    const thrower = { webkit: { launch: async () => { throw new Error(REAL); } } };
    await assert.rejects(() => launchEngine(thrower, "webkit", {}), /npx playwright install webkit/);

    const lines = [];
    const runs = [];
    const code = await testCmd({
      url: "http://127.0.0.1:1/",
      test: "the page loads",
      engine: "webkit",
      yes: true,
      log: (s) => lines.push(String(s)),
      onRun: (r) => runs.push(r),
      loadBrowser: async () => ({ pw: thrower }),
    });
    assert.equal(code, 2, "a browser we never downloaded is our problem, and 1 would blame their app");
    assert.equal(runs.at(-1).status, "errored");
    assert.match(runs.at(-1).reason, /npx playwright install webkit/);
    assert.ok(!/\bfailed\b/i.test(runs.at(-1).status), "never `failed`: nothing was observed about the application");
  });

  test("an engine this Playwright build does not have at all says so, rather than reading undefined", async () => {
    await assert.rejects(() => launchEngine({ chromium: {} }, "firefox", {}), /does not expose a Firefox browser/);
  });
});

// ---- the engine is part of what a recording was made against --------------------------------------

describe("the engine a recording carries", () => {
  const plan = { startUrl: "http://x/", steps: [{ kind: "click", role: "button", name: "Pay" }], proof: "Paid" };

  test("EVERY engine is stamped, chromium included", () => {
    // Stamping only the unusual ones would make a Chromium recording byte-identical to one written
    // before this existed, and then replaying it on WebKit could say nothing at all — the silence
    // this whole feature is here to remove.
    for (const e of ENGINES) assert.equal(withEngine(plan, e).engine, e);
  });

  test("a recording that must not be written stays unwritten", () => {
    assert.equal(withEngine(null, "webkit"), null, "compile()'s refusal outranks anything here");
  });

  test("a nonsense engine is not stamped, so a recording never claims an engine that does not exist", () => {
    assert.equal(withEngine(plan, "safari").engine, undefined);
    assert.equal(withEngine(plan, "").engine, undefined);
  });

  test("a recording written before this feature names no engine, and that is not the same as chromium", () => {
    assert.equal(recordedEngine({ steps: [] }), "", "absent must read as `we do not know`");
    assert.equal(recordedEngine({ steps: [], engine: 7 }), "", "a hand-edited recording is untrusted input");
    assert.equal(recordedEngine({ steps: [], engine: " WebKit " }), "webkit");
  });
});

describe("what is said when a recording crosses engines", () => {
  test("nothing at all when it does not cross — which is every ordinary run", () => {
    assert.equal(engineNote("chromium", "chromium"), "");
    assert.equal(engineNote("", "webkit"), "", "a recording that names no engine gives us nothing true to say");
    assert.equal(engineNote("", ""), "");
  });

  test("both engines are named, and the note says the CURRENT one is what was actually checked", () => {
    const n = engineNote("chromium", "webkit", "passed");
    assert.match(n, /made on Chromium/);
    assert.match(n, /replayed on WebKit/);
    assert.match(n, /WebKit-only break/i, "the reason to run a second engine is the reason to say this");
  });

  test("on a stale replay the engine change is offered as a CANDIDATE cause, never as the verdict", () => {
    const n = engineNote("chromium", "firefox", "stale");
    assert.match(n, /candidate/i);
    assert.ok(!/\bfailed\b|\bbug in\b/i.test(n), `a note must not read as a verdict: ${n}`);
  });

  test("withNote never produces a dangling or doubled space", () => {
    assert.equal(withNote("A verdict.", ""), "A verdict.");
    assert.equal(withNote("", "A note."), "A note.");
    assert.equal(withNote("A verdict.", "A note."), "A verdict. A note.");
  });
});

// ---- a real page, in every engine this machine has ------------------------------------------------

const APP = `<!doctype html><meta charset="utf-8"><title>Shop</title>
<h1>Your cart</h1><p id="m">2 items in your cart.</p>
<button id="c">Proceed to checkout</button>
<script>c.onclick=()=>m.textContent='Order placed.'</script>`;

const server = createServer((_req, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(APP);
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${server.address().port}/`;
// closeAllConnections BEFORE close: a keep-alive socket a browser left open holds close() forever,
// and the file then hangs instead of finishing. This has cost this project ten minutes before.
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
      available[e] = String(err && err.message ? err.message : err).slice(0, 120);
    }
  }
}

const RECORDED = [{ ok: true, action: { kind: "click" }, target: { role: "button", name: "Proceed to checkout" } }];

for (const engine of ENGINES) {
  const skip = !pw
    ? noBrowser.skip
    : available[engine] === true
      ? false
      : `${engine} is not installed on this machine — run \`npx playwright install ${engine}\`. (${available[engine]})`;

  test(`the same recording drives a real ${engine}`, { skip }, async () => {
    const browser = await launchEngine(pw, engine, { headless: true });
    try {
      // LAYER ONE: Playwright's own answer to "what did you launch". A launchEngine that ignored
      // its argument and always returned Chromium fails here.
      assert.equal(browser.browserType().name(), engine);
      const page = await browser.newPage();
      await page.goto(url, { waitUntil: "domcontentloaded" });
      // LAYER TWO: what the PAGE says about itself, which our code has no hand in at all. Two
      // independent signals, because one of them being our own bookkeeping is how a green suite
      // covers a launcher that never switched engine.
      const ua = await page.evaluate(() => navigator.userAgent);
      const fingerprint = { chromium: /Chrome\//, firefox: /Firefox\//, webkit: /Version\/.*Safari/ }[engine];
      assert.match(ua, fingerprint, `${engine} reported a user agent that is not ${engine}'s: ${ua}`);
      if (engine !== "chromium") assert.ok(!/Chrome\//.test(ua) || engine === "firefox", `${engine} looks like Chromium: ${ua}`);

      // And the product's actual claim: a recording made anywhere replays here, with no model.
      const r = await replay(page, compile(url, RECORDED, "Order placed"));
      assert.equal(r.status, "passed", `${engine}: ${JSON.stringify(r)}`);
      assert.match(await page.evaluate(() => document.body.innerText), /Order placed/, "it must really have driven the app");
      await page.close();
    } finally {
      await browser.close();
    }
  });
}

test("this machine's engine availability is REPORTED, not assumed", () => {
  // Not an assertion about which engines exist — a machine with one browser is legitimate. This
  // exists so the run's output states what was actually exercised, instead of a reader inferring
  // "webkit works" from three green ticks two of which were skips.
  const line = ENGINES.map((e) => `${e}=${available[e] === true ? "launched" : "absent"}`).join(" ");
  assert.ok(line.length > 0);
  console.log(`      engines on this machine: ${line}`);
});

// ---- through the CLI, end to end -------------------------------------------------------------------

describe("through the command, with no model reachable", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
  const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-engines-"));

  test("a bare --browser and a typo'd one both exit 2 out of the CLI itself", () => {
    for (const argv of [["test", "--browser"], ["test", "--browser", "webkti"], ["test", "--browser=webkti"]]) {
      const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 30_000 });
      assert.equal(r.status, 2, `${argv.join(" ")} must exit 2 — our refusal, never 1, which blames the app`);
      assert.match(r.stderr, /--browser must be chromium, firefox or webkit/);
    }
  });

  const crossEngine = ENGINES.find((e) => e !== "chromium" && available[e] === true);
  test("a Chromium recording replayed on another engine passes AND says which engines", {
    skip: !pw ? noBrowser.skip : crossEngine ? false : "neither firefox nor webkit is installed on this machine",
  }, async () => {
    const plan = path.join(scratch(), "checkout.json");
    // Written as the agent would write it on Chromium: steps, a proof, and the engine.
    writeFileSync(plan, JSON.stringify(withEngine(compile(url, RECORDED, "Order placed"), "chromium"), null, 2));

    const lines = [];
    const runs = [];
    const key = process.env.ANTHROPIC_API_KEY;
    delete process.env.ANTHROPIC_API_KEY; // the replay must need no model at all
    let code;
    try {
      code = await testCmd({
        url, test: "a shopper can check out", plan, engine: crossEngine, yes: true,
        log: (s) => lines.push(String(s)), onRun: (r) => runs.push(r),
      });
    } finally {
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
    const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");

    // THE VERDICT IS UNTOUCHED. This is the contract: an engine change is a note, never a status.
    assert.equal(code, 0, out);
    assert.equal(runs.at(-1).status, "passed");
    assert.equal(runs.at(-1).mode, "replay", "it must not have escalated to the agent");

    // AND IT IS NOT SILENT. Both engines named, in the terminal and in what the project is told.
    assert.match(out, /made on Chromium/, out);
    assert.match(out, new RegExp(`replayed on (Firefox|WebKit)`), out);
    assert.match(runs.at(-1).reason, /made on Chromium and was replayed on (Firefox|WebKit)/,
      "the note has to reach the project too: nobody reads a CI terminal");
  });

  test("a same-engine replay says nothing about engines at all", { skip: noBrowser.skip }, async () => {
    const plan = path.join(scratch(), "checkout.json");
    writeFileSync(plan, JSON.stringify(withEngine(compile(url, RECORDED, "Order placed"), "chromium"), null, 2));
    const lines = [];
    const runs = [];
    const key = process.env.ANTHROPIC_API_KEY;
    delete process.env.ANTHROPIC_API_KEY;
    let code;
    try {
      code = await testCmd({ url, test: "a shopper can check out", plan, yes: true, log: (s) => lines.push(String(s)), onRun: (r) => runs.push(r) });
    } finally {
      if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
      else process.env.ANTHROPIC_API_KEY = key;
    }
    const out = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
    assert.equal(code, 0, out);
    // The default path is not allowed to grow one word of engine chatter, or every green CI log in
    // the world gains a line nobody needed.
    assert.ok(!/made on Chromium/.test(out), `the default path must stay silent: ${out}`);
    assert.equal(runs.at(-1).reason, "Replayed the recorded run; every step still worked.");
  });
});

// ---- the flag reaches every layer, not just the one this file imports -------------------------------

describe("the engine reaches the runner", () => {
  test("runSuite hands it to every test", async () => {
    // A flag that stops at suiteCmd is a flag that runs fifty tests on Chromium while the terminal
    // says webkit.
    let got = null;
    await runSuite({
      tests: [{ file: "tests/a.md", name: "A", test: "t", id: "a", planPath: "/dev/null/a.json" }],
      url: "http://127.0.0.1:1", engine: "webkit", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ engine, onRun }) => {
        got = engine;
        onRun({ status: "passed", mode: "replay", reason: "ok" });
        return 0;
      },
    });
    assert.equal(got, "webkit");
  });

  test("runSuite's default is chromium, so an unchanged caller is unchanged", async () => {
    let got = "unset";
    await runSuite({
      tests: [{ file: "tests/a.md", name: "A", test: "t", id: "a", planPath: "/dev/null/a.json" }],
      url: "http://127.0.0.1:1", log: () => {}, mkdir: () => {}, hasKey: true,
      runTest: async ({ engine, onRun }) => {
        got = engine;
        onRun({ status: "passed", mode: "replay", reason: "ok" });
        return 0;
      },
    });
    assert.equal(got, "chromium");
  });

  test("the shared browser a parallel suite uses exposes the engine that was asked for", async () => {
    // lib/pool.mjs stands in for loadPlaywright when --workers > 1, and its stub used to expose
    // only `chromium` — so `--browser webkit --workers 4` failed with "this Playwright build does
    // not expose a WebKit browser" on a machine that had WebKit installed.
    let asked = null;
    const fake = { webkit: { launch: async () => ({ newContext: async () => ({ close: async () => {} }), close: async () => {} }) } };
    const shared = sharedBrowser({ load: async (_log, _yes, engine) => { asked = engine; return { pw: fake }; } });
    const { pw } = await shared.loadBrowser(() => {}, true, "webkit");
    assert.equal(asked, "webkit", "the lazy install must fetch the engine that is about to launch");
    assert.ok(pw.webkit, `the stub exposed ${JSON.stringify(Object.keys(pw))} instead of webkit`);
    await pw.webkit.launch({ headless: true });
    assert.equal(shared.launched, 1);
    await shared.close();
  });
});

test("--browser is documented where somebody looking for it would look", () => {
  // A flag nobody can find is a flag nobody has. Both the README's flag table and the CLI's own
  // help, because those are the two places a person looks and they drift apart silently.
  const readme = readFileSync(new URL("../README.md", import.meta.url), "utf8");
  assert.match(readme, /`--browser <name>`/, "the flag is missing from the README's flag table");
  assert.match(readme, /firefox.*webkit|webkit.*firefox/, "the README never names the other two engines");
  assert.match(readme, /made on Chromium and was replayed on WebKit/, "the README does not show what a cross-engine replay says");

  const help = spawnSync(process.execPath, [fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url)), "--help"], { encoding: "utf8", timeout: 30_000 });
  assert.match(help.stdout.replace(/\x1b\[[0-9;]*m/g, ""), /--browser <name>/, "the flag is missing from --help");
});
