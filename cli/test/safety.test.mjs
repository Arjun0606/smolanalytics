// WHAT HAPPENS TO THE CUSTOMER'S DATA WHEN A TEST RUNS.
//
// Every other test file here is about getting the verdict right. This one is about the side effect
// that produces it: the agent drives a real browser, so a sentence about signing up puts a row in
// somebody's users table, and a sentence about checking out can put an order in it.
//
// Four properties, and each one has a way of quietly not being true:
//
//   the identity actually reaches the model      substituting into a string nobody sends is a no-op
//   production is warned about, staging is not   a warning on every run is a warning nobody reads
//   CI never waits on stdin                      a prompt no one can see is a hung build
//   teardown fires, with the right body          an endpoint called with the wrong keys deletes nothing
//
// The model is stubbed and the teardown endpoint is a real local server: no spend, no network.

import { test, describe, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import {
  confirmProduction, forgetConfirmations, looksProduction, newIdentity, newRunId,
  postTeardown, stagingMarker, substitute, PREFIX,
} from "../lib/safety.mjs";
import { testCmd } from "../lib/test.mjs";
import { runSuite } from "../lib/suite.mjs";

const plain = (s) => s.replace(/\x1b\[[0-9;]*m/g, "");

beforeEach(() => forgetConfirmations());

// ---- the identity -------------------------------------------------------------------------------

describe("a run's identity", () => {
  test("every field is obviously ours and carries the same id", () => {
    // The property that makes a run authorisable twice: one LIKE 'smoltest%' finds everything it
    // made, in any column, whether or not a teardown hook was ever configured.
    const id = newIdentity({ runId: "abc123" });
    assert.equal(id.email, "smoltest+abc123@example.com");
    assert.equal(id.username, "smoltest_abc123");
    assert.equal(id.name, "Smoltest abc123");
    for (const v of [id.email, id.username, id.name, id.password]) {
      assert.ok(v.toLowerCase().includes(PREFIX), `${v} is not findable`);
      assert.ok(v.includes("abc123"), `${v} does not lead back to the run`);
    }
  });

  test("the default domain is one that cannot receive mail", () => {
    // example.com is reserved by RFC 2606. Anything else risks a "welcome!" landing in a real
    // stranger's inbox every time somebody tests a signup.
    assert.match(newIdentity().email, /@example\.com$/);
  });

  test("the password satisfies the complexity rule signup forms actually enforce", () => {
    // Otherwise the run dies on "password must contain a number" and reports it as a bug in the app.
    const p = newIdentity({ runId: "abc123" }).password;
    assert.ok(p.length >= 12, p);
    assert.match(p, /[a-z]/);
    assert.match(p, /[A-Z]/);
    assert.match(p, /[0-9]/);
    assert.match(p, /[^A-Za-z0-9]/);
  });

  test("ids sort in the order the runs happened", () => {
    const a = newRunId(1_700_000_000_000, () => 0.1);
    const b = newRunId(1_700_000_001_000, () => 0.1);
    assert.ok(a < b, `${a} should sort before ${b} so a users table groups a run together`);
  });

  test("two runs in the same millisecond still differ", () => {
    assert.notEqual(newRunId(1_700_000_000_000, () => 0.1), newRunId(1_700_000_000_000, () => 0.9));
  });

  test("a supplied id cannot break the address it goes into", () => {
    // SMOLANALYTICS_RUN_ID comes from a CI expression. An unescaped one turns the email into two
    // addresses, or into a header.
    const id = newIdentity({ runId: "PR #42 / feature branch\nX: y" });
    assert.match(id.email, /^smoltest\+[a-z0-9-]+@example\.com$/, id.email);
  });
});

describe("placeholders", () => {
  const id = newIdentity({ runId: "abc123" });

  test("each one becomes the run's own value", () => {
    const r = substitute("sign up as {{email}} with {{password}}, named {{name}}", id);
    assert.equal(r.text, "sign up as smoltest+abc123@example.com with Smoltest-abc123-9!, named Smoltest abc123");
    assert.deepEqual(r.used, ["email", "password", "name"]);
  });

  test("case and spacing are forgiven", () => {
    assert.equal(substitute("{{ Email }}", id).text, id.email);
  });

  test("an unknown token is left alone and reported, never dropped", () => {
    // Dropping {{emial}} leaves the model an empty field to invent a value for, and an invented
    // value is the untraceable row this whole file exists to prevent.
    const r = substitute("sign up as {{emial}}", id);
    assert.equal(r.text, "sign up as {{emial}}");
    assert.deepEqual(r.unknown, ["{{emial}}"]);
  });

  test("an inherited property is not a placeholder", () => {
    // `"constructor" in {}` is true. `in` here would substitute a function into somebody's test.
    assert.equal(substitute("{{constructor}} {{toString}}", id).text, "{{constructor}} {{toString}}");
  });

  test("a sentence with no placeholders is returned untouched", () => {
    const s = "the pricing page shows a monthly price";
    assert.equal(substitute(s, id).text, s);
  });
});

// ---- which URLs get warned about ------------------------------------------------------------------

describe("telling production from somewhere it is safe to break things", () => {
  const safe = [
    "http://localhost:3000/", "http://127.0.0.1:8080/", "http://[::1]:3000/", "http://192.168.1.9/",
    "http://myapp.local/", "https://shop-git-fix-abc.vercel.app/", "https://app.netlify.app",
    "https://x.pages.dev", "https://preview.myapp.com", "https://staging.myapp.com",
    "https://myapp-pr-42.onrender.com", "https://qa.myapp.com", "https://myapp-dev.com",
  ];
  for (const u of safe) {
    test(`no warning for ${u}`, () => {
      assert.ok(!looksProduction(u), `${u} warned, and a warning on every run is one nobody reads (${stagingMarker(u)})`);
    });
  }

  const risky = ["https://myapp.com", "https://www.myapp.com", "https://shop.myapp.com/checkout", "https://app.myapp.io"];
  for (const u of risky) {
    test(`warns for ${u}`, () => {
      assert.ok(looksProduction(u), `${u} was let through silently, which is how an order lands in a real table`);
    });
  }

  test("a platform host that routinely IS production still warns", () => {
    // Unlike a vercel.app branch deploy, on Heroku and Azure the bare platform hostname is often
    // the whole product. Listing these as preview suffixes would let a real order book through
    // silently — the exclusion is deliberate, so pin it.
    for (const u of ["https://myapp.herokuapp.com", "https://myapp.azurewebsites.net"]) {
      assert.ok(looksProduction(u), `${u} must warn: the platform host is where the app lives`);
    }
  });

  test("a marker must be a whole label, not a substring", () => {
    // "dev" inside "developers" and "test" inside "testrail" are real production hostnames.
    assert.ok(looksProduction("https://developers.myapp.com"), "developers.myapp.com is production");
    assert.ok(looksProduction("https://testrail.com"), "testrail.com is production");
  });
});

// ---- the question, and never hanging on it ----------------------------------------------------

const id = newIdentity({ runId: "abc123" });

/**
 * Pretend a person is sitting there. Both ends have to look like a terminal: under `node --test`
 * stdout is a pipe, which is exactly the CI shape, so without this every "a person answers" test
 * silently exercises the no-terminal path instead.
 */
async function asPerson(fn) {
  const inTty = process.stdin.isTTY;
  const outTty = process.stdout.isTTY;
  process.stdin.isTTY = true;
  process.stdout.isTTY = true;
  try {
    return await fn();
  } finally {
    process.stdin.isTTY = inTty;
    process.stdout.isTTY = outTty;
  }
}

/** Fails the test rather than the suite's patience: a prompt that hangs would hang node --test too. */
function within(ms, promise) {
  return Promise.race([
    promise,
    new Promise((_, reject) => setTimeout(() => reject(new Error(`still waiting after ${ms}ms — this is the hang`)), ms).unref?.()),
  ]);
}

describe("the question before a production-looking URL", () => {
  test("a staging URL is not warned about and not asked about", async () => {
    const lines = [];
    const r = await confirmProduction({
      url: "https://staging.myapp.com", identity: id, env: {}, log: (s) => lines.push(s),
      ask: () => assert.fail("nobody should be asked about staging"),
      memo: new Map(),
    });
    assert.equal(r.proceed, true);
    assert.equal(r.warned, false);
    assert.equal(lines.length, 0, lines.join("\n"));
  });

  test("a production URL says what it is about to create, and names the identity", async () => {
    const lines = [];
    await confirmProduction({
      url: "https://myapp.com", identity: id, env: { CI: "1" }, log: (s) => lines.push(s), memo: new Map(),
    });
    const out = plain(lines.join("\n"));
    assert.match(out, /https:\/\/myapp\.com/);
    assert.match(out, /account/i);
    assert.match(out, /order/i);
    assert.match(out, /smoltest\+abc123@example\.com/, "without the identity there is nothing to search for afterwards");
    assert.match(out, /--teardown/);
  });

  test("CI is told and never asked, even with a TTY attached", async () => {
    // Some runners do allocate a TTY. A question there waits for an answer that never comes, until
    // the job's timeout kills it — and a safety feature that hangs builds gets deleted.
    const r = await asPerson(() => within(2000, confirmProduction({
      url: "https://myapp.com", identity: id, env: { CI: "true" }, log: () => {},
      ask: () => new Promise(() => {}), // never resolves: the hang, if it were ever reached
      memo: new Map(),
    })));
    assert.equal(r.proceed, true, "CI must continue");
    assert.equal(r.asked, false, "stdin must not be touched");
  });

  test("no terminal, no question", async () => {
    const inTty = process.stdin.isTTY;
    process.stdin.isTTY = false;
    try {
      const r = await within(2000, confirmProduction({
        url: "https://myapp.com", identity: id, env: {}, log: () => {},
        ask: () => new Promise(() => {}),
        memo: new Map(),
      }));
      assert.equal(r.proceed, true);
      assert.equal(r.asked, false);
    } finally {
      process.stdin.isTTY = inTty;
    }
  });

  test("a piped stdout is not a terminal, even when stdin still is one", async () => {
    // `smolanalytics test | tee build.log` keeps stdin a TTY while the question goes into a file
    // nobody is watching. Checking stdin alone waits there forever; BOTH ends must be a terminal.
    const inTty = process.stdin.isTTY;
    const outTty = process.stdout.isTTY;
    process.stdin.isTTY = true;
    process.stdout.isTTY = false;
    try {
      const r = await within(2000, confirmProduction({
        url: "https://myapp.com", identity: id, env: {}, log: () => {},
        ask: () => new Promise(() => {}),
        memo: new Map(),
      }));
      assert.equal(r.proceed, true);
      assert.equal(r.asked, false, "the question went where nobody is looking");
    } finally {
      process.stdin.isTTY = inTty;
      process.stdout.isTTY = outTty;
    }
  });

  test("--yes skips the question but still says what is happening", async () => {
    const lines = [];
    const r = await confirmProduction({
      url: "https://myapp.com", identity: id, yes: true, env: {}, log: (s) => lines.push(s),
      ask: () => assert.fail("--yes means do not ask"), memo: new Map(),
    });
    assert.equal(r.proceed, true);
    assert.match(plain(lines.join("\n")), /smoltest\+abc123@example\.com/, "--yes silences the question, not the notice");
  });

  test("a person can say yes", async () => {
    const r = await asPerson(() => confirmProduction({
      url: "https://myapp.com", identity: id, env: {}, log: () => {}, ask: async () => "y\n", memo: new Map(),
    }));
    assert.equal(r.proceed, true);
    assert.equal(r.asked, true);
  });

  test("a person can say no, and enter alone means no", async () => {
    await asPerson(async () => {
      for (const answer of ["n", "", "\n"]) {
        const r = await confirmProduction({ url: "https://myapp.com", identity: id, env: {}, log: () => {}, ask: async () => answer, memo: new Map() });
        assert.equal(r.proceed, false, `answer ${JSON.stringify(answer)} should not proceed`);
      }
    });
  });

  test("a suite against one origin asks once, not once per test", async () => {
    // Nine tests against production used to mean nine identical questions, which is how people
    // learn to hold down y.
    const memo = new Map();
    let asked = 0;
    await asPerson(async () => {
      for (let i = 0; i < 3; i++) {
        await confirmProduction({ url: "https://myapp.com/page", identity: id, env: {}, log: () => {}, ask: async () => { asked++; return "y"; }, memo });
      }
    });
    assert.equal(asked, 1);
  });
});

// ---- teardown ---------------------------------------------------------------------------------

describe("the teardown hook", () => {
  const seen = [];
  let reply = { code: 200, body: "ok" };
  const server = createServer((req, res) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => {
      seen.push({ method: req.method, url: req.url, headers: req.headers, body: raw ? JSON.parse(raw) : null });
      res.writeHead(reply.code, { "content-type": "text/plain" });
      res.end(reply.body);
    });
  });
  const listening = new Promise((r) => server.listen(0, "127.0.0.1", r));
  after(() => new Promise((r) => server.close(() => r())));

  test("POSTs the identity the customer's handler needs to delete the rows", async () => {
    await listening;
    seen.length = 0;
    const endpoint = `http://127.0.0.1:${server.address().port}/teardown`;
    const r = await postTeardown({
      endpoint, identity: newIdentity({ runId: "abc123" }), test: "a new customer can sign up",
      url: "https://myapp.com", status: "passed", env: {}, at: () => "2026-08-24T00:00:00.000Z",
    });
    assert.equal(r.ok, true, r.detail);
    assert.equal(seen.length, 1);
    const got = seen[0];
    assert.equal(got.method, "POST");
    assert.equal(got.url, "/teardown");
    assert.equal(got.headers["content-type"], "application/json");
    assert.equal(got.headers["x-smoltest-run"], "abc123");
    // The body is somebody's `if (body.email)`. Renaming a key here breaks their handler silently.
    assert.deepEqual(got.body, {
      runId: "abc123",
      prefix: "smoltest",
      email: "smoltest+abc123@example.com",
      username: "smoltest_abc123",
      name: "Smoltest abc123",
      password: "Smoltest-abc123-9!",
      test: "a new customer can sign up",
      url: "https://myapp.com",
      status: "passed",
      at: "2026-08-24T00:00:00.000Z",
    });
  });

  test("the shared secret goes in the header, and only when there is one", async () => {
    await listening;
    seen.length = 0;
    const endpoint = `http://127.0.0.1:${server.address().port}/teardown`;
    await postTeardown({ endpoint, identity: id, env: { SMOLANALYTICS_TEARDOWN_SECRET: "s3cr3t" } });
    assert.equal(seen[0].headers.authorization, "Bearer s3cr3t");
    await postTeardown({ endpoint, identity: id, env: {} });
    assert.equal(seen[1].headers.authorization, undefined);
  });

  test("an endpoint that answers 500 is reported, not thrown", async () => {
    await listening;
    reply = { code: 500, body: "boom" };
    const r = await postTeardown({ endpoint: `http://127.0.0.1:${server.address().port}/x`, identity: id, env: {} });
    reply = { code: 200, body: "ok" };
    assert.equal(r.ok, false);
    assert.match(r.detail, /500/);
    assert.match(r.detail, /boom/);
  });

  test("an endpoint that never answers gives up instead of holding the run open", async () => {
    const slow = createServer(() => {});
    await new Promise((r) => slow.listen(0, "127.0.0.1", r));
    try {
      const r = await within(3000, postTeardown({
        endpoint: `http://127.0.0.1:${slow.address().port}/`, identity: id, env: {}, timeoutMs: 300,
      }));
      assert.equal(r.ok, false);
      assert.match(r.detail, /no answer in/);
    } finally {
      // closeAllConnections, because if the abort above ever regresses the still-open request
      // holds the server up and close() never finishes — the suite wedges instead of going red.
      slow.closeAllConnections?.();
      slow.close();
    }
  });

  test("no endpoint, no request", async () => {
    const r = await postTeardown({ endpoint: "", identity: id, env: {}, fetchImpl: () => assert.fail("nothing to call") });
    assert.equal(r.skipped, true);
  });
});

// ---- the same three things, through the command --------------------------------------------------

describe("wired into `smolanalytics test`", () => {
  /** The browser is never started here: everything under test happens before or after the run. */
  const noBrowser = async () => ({ pw: null, problem: "Playwright is not installed." });

  test("a production URL is warned about before anything opens", async () => {
    const lines = [];
    const code = await testCmd({
      url: "https://myapp.com", test: "a new customer can sign up as {{email}}",
      // A KEY, because the question is only asked when a run could actually follow it. With no key
      // and no recording the runner stops first and says so, and warning about production data for
      // a run that cannot happen is noise ahead of the sentence that matters.
      env: { CI: "1", ANTHROPIC_API_KEY: "sk-ant-test" }, log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
    });
    const out = plain(lines.join("\n"));
    assert.match(out, /has no staging, preview or localhost marker/);
    assert.equal(code, 2, "the missing browser is still the runner's problem, not the app's");
  });

  test("a localhost URL says nothing about production", async () => {
    const lines = [];
    await testCmd({
      url: "http://localhost:3000", test: "the pricing page shows a monthly price",
      env: { CI: "1" }, log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
    });
    assert.ok(!/marker/.test(plain(lines.join("\n"))), plain(lines.join("\n")));
  });

  test("saying no runs nothing, and is never a bug report", async () => {
    const runs = [];
    const code = await asPerson(() => testCmd({
      url: "https://myapp.com", test: "check out with a card", env: { ANTHROPIC_API_KEY: "sk-ant-test" }, log: () => {},
      onRun: (r) => runs.push(r), ask: async () => "n",
      loadBrowser: () => assert.fail("the browser must not start after a no"),
    }));
    assert.equal(code, 2, "exit 1 says the application is broken; declining says nothing about it");
    assert.deepEqual(runs.map((r) => r.status), ["errored"]);
    assert.match(runs[0].reason, /nothing was tested/i);
  });

  test("teardown fires after a run that never reached a verdict", async () => {
    // The run that errored or failed is the likeliest to have left half an account behind.
    const got = [];
    const server = createServer((req, res) => {
      let raw = "";
      req.on("data", (c) => (raw += c));
      req.on("end", () => { got.push(JSON.parse(raw)); res.end("ok"); });
    });
    await new Promise((r) => server.listen(0, "127.0.0.1", r));
    const lines = [];
    try {
      await testCmd({
        url: "http://localhost:3000", test: "sign up as {{email}}",
        teardown: `http://127.0.0.1:${server.address().port}/teardown`,
        env: {}, log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
      });
    } finally {
      server.close();
    }
    assert.equal(got.length, 1, "nothing was told to clean up");
    assert.equal(got[0].status, "errored");
    assert.match(got[0].email, /^smoltest\+/);
    assert.equal(got[0].test, `sign up as ${got[0].email}`, "the endpoint is told the sentence that actually ran");
    assert.match(plain(lines.join("\n")), /teardown: /);
  });

  test("the email domain can be pointed at a catch-all you own", async () => {
    const lines = [];
    await testCmd({
      url: "http://localhost:3000", test: "sign up as {{email}}", emailDomain: "inbox.myapp.com",
      env: {}, log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
    });
    assert.match(plain(lines.join("\n")), /smoltest\+[a-z0-9]+@inbox\.myapp\.com/);
  });

  test("a typo'd placeholder is named, and the sentence still runs", async () => {
    const lines = [];
    await testCmd({
      url: "http://localhost:3000", test: "sign up as {{emial}}", env: {},
      log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
    });
    const out = plain(lines.join("\n"));
    assert.match(out, /\{\{emial\}\} is not a placeholder/);
    assert.match(out, /\{\{email\}\}/, "the message has to name the ones that do work");
  });
});

// ---- one CI run id, many tests ----------------------------------------------------------------

describe("a suite under one pinned SMOLANALYTICS_RUN_ID", () => {
  const noBrowser = async () => ({ pw: null, problem: "Playwright is not installed." });

  test("an explicit runId option beats the environment", async () => {
    // This is the hook runSuite uses; without it every suite test reads the same env var and nine
    // signups share one email — test two dies on "email already exists", blamed on the app.
    const lines = [];
    await testCmd({
      url: "http://localhost:3000", test: "sign up as {{email}}", runId: "build7-3",
      env: { SMOLANALYTICS_RUN_ID: "build7" }, log: (...a) => lines.push(a.join(" ")), loadBrowser: noBrowser,
    });
    assert.match(plain(lines.join("\n")), /smoltest\+build7-3@example\.com/);
  });

  test("runSuite hands every test a DIFFERENT id, still grouped under the CI id", async () => {
    const got = [];
    const tests = [
      { file: "a.md", name: "A", test: "sign up as {{email}}", id: "a", planPath: "/nowhere/a.json" },
      { file: "b.md", name: "B", test: "sign up as {{email}}", id: "b", planPath: "/nowhere/b.json" },
    ];
    await runSuite({
      tests, url: "https://staging.myapp.com", log: () => {}, mkdir: () => {}, hasKey: true,
      env: { SMOLANALYTICS_RUN_ID: "build7" },
      runTest: async ({ runId, onRun }) => {
        got.push(runId);
        onRun({ status: "passed", mode: "agent", reason: "ok" });
        return 0;
      },
    });
    assert.equal(new Set(got).size, tests.length, `identities collide across the suite: ${got.join(", ")}`);
    for (const r of got) assert.match(String(r), /^build7-/, "the CI id must still group the rows");
  });

  test("with no pinned id, runSuite pins nothing and each run generates its own", async () => {
    const got = [];
    await runSuite({
      tests: [{ file: "a.md", name: "A", test: "x", id: "a", planPath: "/nowhere/a.json" }],
      url: "https://staging.myapp.com", log: () => {}, mkdir: () => {}, hasKey: true, env: {},
      runTest: async ({ runId, onRun }) => {
        got.push(runId);
        onRun({ status: "passed", mode: "agent", reason: "ok" });
        return 0;
      },
    });
    assert.equal(got[0], "", "an invented base id would defeat newIdentity's per-run randomness");
  });
});

// ---- the one that matters most: does the identity reach the model? ---------------------------
//
// Substituting into a string nobody sends is a no-op that every unit test above would still pass.
// So this runs the real command against a real page in a real Chromium, with only the model
// stubbed, and reads what was actually put on the wire.

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; this skips with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

test("the substituted identity is what the agent is actually told", noBrowser, async () => {
  const page = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end('<!doctype html><title>Shop</title><h1>Create an account</h1><input aria-label="Email"><button>Sign up</button>');
  });
  await new Promise((r) => page.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${page.address().port}/`;

  const sent = [];
  const realFetch = globalThis.fetch;
  const saved = {
    key: process.env.ANTHROPIC_API_KEY,
    project: process.env.SMOLANALYTICS_PROJECT,
    write: process.env.SMOLANALYTICS_WRITE_KEY,
  };
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  // Nothing may be posted to a real project from a test run.
  delete process.env.SMOLANALYTICS_PROJECT;
  delete process.env.SMOLANALYTICS_WRITE_KEY;
  globalThis.fetch = async (target, init) => {
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model may be called here");
    sent.push(JSON.parse(init.body));
    return {
      ok: true, status: 200,
      json: async () => ({
        stop_reason: "tool_use",
        content: [{ type: "tool_use", id: "t1", name: "finish", input: { passed: true, why: "Account created.", proof: "Create an account" } }],
      }),
      text: async () => "",
    };
  };

  try {
    const code = await testCmd({
      url,
      test: "sign up as {{email}} with the password {{password}}",
      env: { CI: "1", SMOLANALYTICS_RUN_ID: "abc123" },
      log: () => {},
    });
    assert.equal(code, 0);
  } finally {
    globalThis.fetch = realFetch;
    if (saved.key === undefined) delete process.env.ANTHROPIC_API_KEY; else process.env.ANTHROPIC_API_KEY = saved.key;
    if (saved.project !== undefined) process.env.SMOLANALYTICS_PROJECT = saved.project;
    if (saved.write !== undefined) process.env.SMOLANALYTICS_WRITE_KEY = saved.write;
    await new Promise((r) => page.close(() => r()));
  }

  assert.equal(sent.length, 1);
  const instruction = JSON.stringify(sent[0].messages);
  assert.match(instruction, /smoltest\+abc123@example\.com/, "the model was never told the run's identity");
  assert.match(instruction, /Smoltest-abc123-9!/);
  assert.ok(!instruction.includes("{{email}}"), "the placeholder reached the model unsubstituted");
  assert.ok(!instruction.includes("{{password}}"), "the placeholder reached the model unsubstituted");
});
