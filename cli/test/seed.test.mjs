// --seed: PROVIDING state, not just labelling and deleting it.
//
// lib/safety.mjs can mark what a run creates and delete it afterwards. It cannot build what a run
// NEEDS, and "a logged-in user with three past orders can request a refund" is not writable without
// that. The wall is silent — the suite stays green while the flows that matter are simply never
// written — which is exactly the shape of a tool people keep and stop trusting.
//
// WHAT THESE TESTS ARE WRITTEN AGAINST. This project has four separate incidents of a green suite
// over broken code, and three of them share a cause: the test asserted the implementation instead
// of the requirement, so it could not fail. So, deliberately:
//
//   * The seed endpoint is a REAL http server on 127.0.0.1 that really invents an order and really
//     refuses a token it did not issue. The fixture app decides whether a token is good, so a run
//     that filled the literal "{{ordertoken}}" — or an empty string — cannot reach the proof text.
//   * The scripted model reads the token OUT OF THE PROMPT it was handed and types that. If the
//     value never reached the model, the fill is wrong and the app says so. Nothing is palindromic.
//   * The leak tests grep BYTES ON DISK — every file under the recordings, evidence and summary
//     directories, the PNG included — for the value the endpoint returned. Asserting maskSecrets
//     returns the right string tests the masker; grepping the artefact tests the product, and it is
//     the artefact that leaks.
//   * Every masking test is paired with a run that proves the masked artefact still WORKS: a
//     recording made under one seeded token replays green under the next one. A recording that
//     carries no value and also cannot replay is not privacy, it is a broken feature.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtempSync, readFileSync, readdirSync, statSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  SEED_SECRET_VAR, applySeed, maskSeed, postSeed, readSeedValues, seedProblem, seedRun,
  seedSecrets, seedTokensIn, tokensIn, unmaskableKeys,
} from "../lib/seed.mjs";
import { forgetConfirmations, newIdentity, substitute, unmaskSecrets, maskSecrets } from "../lib/safety.mjs";
import { testCmd, compile, replay } from "../lib/test.mjs";
import { runSuite } from "../lib/suite.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));
const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-seed-"));
const IDENTITY = newIdentity({ runId: "seedrun1" });

/**
 * A server, and the one line of shutdown this project has paid for twice.
 *
 * close() never fires its callback while a keep-alive socket is open, and Playwright holds them.
 * closeAllConnections() FIRST, or the file wedges until somebody kills it ten minutes later.
 */
const servers = [];
async function serve(handler) {
  const s = createServer(handler);
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  servers.push(s);
  return { server: s, url: `http://127.0.0.1:${s.address().port}` };
}
after(async () => {
  for (const s of servers) {
    s.closeAllConnections?.();
    await new Promise((r) => s.close(() => r()));
  }
});

const json = (res, code, body) => {
  res.writeHead(code, { "content-type": "application/json" });
  res.end(typeof body === "string" ? body : JSON.stringify(body));
};
const readBody = (req) => new Promise((r) => {
  let b = "";
  req.on("data", (d) => (b += d));
  req.on("end", () => r(b));
});

// ---- the response contract -----------------------------------------------------------------------

describe("what a seed endpoint is allowed to answer", () => {
  test("a flat object of strings becomes placeholders, keyed case-insensitively", () => {
    const { values, problem } = readSeedValues({ orderId: "A-1042", CouponCode: "SAVE20" });
    assert.equal(problem, "");
    assert.deepEqual(values, { orderid: "A-1042", couponcode: "SAVE20" });
  });

  test("numbers and booleans are accepted, because an id column is an integer", () => {
    // Refusing {"orderId":1042} would be a papercut on the very first thing anybody tries.
    const { values, problem } = readSeedValues({ orderId: 1042, trial: false, balance: 0 });
    assert.equal(problem, "");
    assert.deepEqual(values, { orderid: "1042", trial: "false", balance: "0" });
  });

  test("an endpoint with only side effects may answer with an empty object", () => {
    // "make sure a user exists" is a legitimate seed that has nothing to hand back.
    assert.deepEqual(readSeedValues({}), { values: {}, problem: "" });
  });

  for (const [what, raw] of [["an array", [1, 2]], ["null", null], ["a string", "ok"], ["a number", 7]]) {
    test(`${what} is refused, and the refusal says what to send instead`, () => {
      const { values, problem } = readSeedValues(raw);
      assert.equal(values, null);
      assert.match(problem, /flat object/, problem);
      assert.match(problem, /orderId/, "a refusal without an example is a refusal nobody can act on");
    });
  }

  test("a nested value is refused BY NAME, never quietly dropped", () => {
    // Dropping it leaves a literal {{user}} in the sentence for the model to invent a meaning for,
    // and the verdict is then about a world nobody built.
    const { values, problem } = readSeedValues({ orderId: "A-1", user: { id: 7 } });
    assert.equal(values, null);
    assert.match(problem, /"user"/, problem);
    assert.match(problem, /object/, problem);
    assert.match(problem, /flatten/i, problem);
  });

  test("a key that cannot be written as a placeholder is refused by name", () => {
    const { problem } = readSeedValues({ "order id": "A-1" });
    assert.match(problem, /"order id"/, problem);
    assert.match(problem, /letters, digits and underscores/, problem);
  });

  test("a run-identity key is refused, so {{email}} keeps naming the row teardown deletes", () => {
    // The whole traceability story in lib/safety.mjs rests on {{email}} being the smoltest identity.
    // A fixture that shadowed it would point the sentence at an account teardown never posts and
    // no LIKE 'smoltest%' ever finds.
    for (const key of ["email", "Password", "runid", "username", "name"]) {
      const { values, problem } = readSeedValues({ [key]: "x" });
      assert.equal(values, null, `${key} was allowed to shadow the run identity`);
      assert.match(problem, new RegExp(`"${key}"`), problem);
      assert.match(problem, /teardown/, "the refusal has to say what would break");
    }
  });

  test("two keys that differ only in case are refused, because they are one placeholder", () => {
    const { values, problem } = readSeedValues({ orderId: "A-1", orderid: "A-2" });
    assert.equal(values, null);
    assert.match(problem, /same placeholder/, problem);
  });
});

// ---- placeholders: the same vocabulary {{email}} uses ---------------------------------------------

describe("seeded placeholders behave like the ones already in the sentence", () => {
  test("this file's idea of a token is the same as safety.mjs's, character for character", () => {
    // Two regexes for one syntax is how a token one file thinks is a placeholder and the other does
    // not becomes a value silently left in a sentence. If safety.mjs's TOKEN ever changes, this goes
    // red before anybody ships the drift.
    const id = newIdentity({ runId: "drift" });
    for (const text of [
      "sign up as {{email}}", "open order {{orderId}}", "{{ Email }} and {{ orderId }}",
      "{{email}}{{orderId}}", "not a token: {{9lives}} {{}} {{ }} { {email} }", "{{a_b1}}",
      "literal braces {not} and {{email}}",
    ]) {
      // What safety.mjs recognises: every token it either substitutes or names as unknown.
      const sub = substitute(text, id);
      const theirs = [...sub.used.map((k) => `{{${k}}}`), ...sub.unknown].map((t) => t.replace(/[{}\s]/g, "").toLowerCase()).sort();
      const mine = tokensIn(text).sort();
      assert.deepEqual(mine, theirs, `the two files disagree about ${JSON.stringify(text)}`);
    }
  });

  test("seedTokensIn names only what this runner cannot fill by itself", () => {
    assert.deepEqual(seedTokensIn("sign up as {{email}} then open order {{orderId}}"), ["orderid"]);
    assert.deepEqual(seedTokensIn("sign up as {{email}}"), []);
  });

  test("the masked sentence and the secret pairs round-trip to the resolved one, exactly", () => {
    // THE INVARIANT THE WHOLE FEATURE RESTS ON. The sentence is carried around masked and resolved
    // at the model prompt and at a keystroke; if the round trip is not exact, the agent is handed a
    // literal {{orderid}} and reports that the app rejected it — a lie about somebody's app.
    const values = { orderid: "A-1042", token: "tok_9f2c11ab", ref: "X1" };
    const text = "open order {{orderId}} with {{token}} and quote {{ref}}, twice: {{orderId}}";
    const masked = maskSeed(text, values);
    const resolved = applySeed(text, values);
    assert.equal(unmaskSecrets(masked.text, seedSecrets(values)), resolved.text);
    assert.deepEqual(masked.used, ["orderid", "token", "ref"]);
  });

  test("a value too short to mask is substituted whole, and named as unmaskable", () => {
    // maskSecrets refuses anything under four characters — masking "X1" would rewrite every step
    // containing it. Writing {{ref}} anyway would send the literal text to the model and the form.
    const values = { ref: "X1", orderid: "A-1042" };
    assert.deepEqual(unmaskableKeys(values), ["ref"]);
    assert.equal(maskSeed("quote {{ref}} for {{orderid}}", values).text, "quote X1 for {{orderid}}");
  });

  test("longest value first, so a short id cannot corrupt the long one it lives inside", () => {
    const pairs = seedSecrets({ short: "A-10", long: "A-1042" });
    assert.equal(pairs[0].value, "A-1042", "masking A-10 first leaves {{short}}42 in the recording");
    assert.equal(maskSecrets("order A-1042 refunded", pairs), "order {{long}} refunded");
  });

  test("an inherited property is not a placeholder", () => {
    // `in` would happily substitute a function into somebody's test.
    assert.equal(applySeed("{{constructor}} {{toString}}", {}).text, "{{constructor}} {{toString}}");
  });
});

// ---- the request ----------------------------------------------------------------------------------

describe("what we send, and how the secret gets there", () => {
  test("the body carries the run identity, the sentence and the tokens it wants", async () => {
    let seen = null;
    let headers = null;
    const { url } = await serve(async (req, res) => {
      headers = req.headers;
      seen = JSON.parse(await readBody(req));
      json(res, 200, { orderId: "A-1042" });
    });
    const r = await postSeed({
      endpoint: `${url}/seed`, identity: IDENTITY, url: "https://staging.app.com",
      test: "open order {{orderId}} as {{email}}", env: {}, at: () => "2026-01-01T00:00:00.000Z",
    });
    assert.equal(r.ok, true, r.detail);
    assert.deepEqual(r.values, { orderid: "A-1042" });
    assert.equal(seen.runId, IDENTITY.runId);
    assert.equal(seen.email, IDENTITY.email);
    assert.equal(seen.prefix, "smoltest");
    assert.equal(seen.url, "https://staging.app.com");
    // The tokens this sentence asks for, so one handler can serve a whole suite and build only
    // what each test actually needs.
    assert.deepEqual(seen.placeholders, ["orderid"]);
    assert.equal(headers["x-smoltest-run"], IDENTITY.runId, "a handler must be able to rate-limit without parsing the body");
    assert.equal(headers.authorization, undefined, "no secret was set, so none may be invented");
  });

  test("the secret comes from the environment and nowhere else", async () => {
    // A --seed-secret flag would land in shell history and in the command line every CI runner
    // prints at the top of its log. It is an environment variable or it does not exist.
    let auth = "missing";
    const { url } = await serve(async (req, res) => {
      auth = req.headers.authorization;
      json(res, 200, {});
    });
    await postSeed({ endpoint: `${url}/seed`, identity: IDENTITY, env: { [SEED_SECRET_VAR]: "s3cr3t" } });
    assert.equal(auth, "Bearer s3cr3t");
    // And it is its own variable: a teardown credential is not silently posted to a seed URL.
    await postSeed({ endpoint: `${url}/seed`, identity: IDENTITY, env: { SMOLANALYTICS_TEARDOWN_SECRET: "other" } });
    assert.equal(auth, undefined);
  });

  test("no flag in bin/ can ever set the secret", () => {
    // The guarantee above is only worth anything if the CLI has no way to accept one.
    const src = readFileSync(bin, "utf8");
    assert.ok(!/seed-secret/i.test(src), "bin/ grew a flag that puts the seed secret on the command line");
  });

  test("an endpoint that is not there is an answer, not a crash", async () => {
    // Nothing in this file may throw past the caller: a seed failure has a verdict of its own.
    const r = await postSeed({ endpoint: "http://127.0.0.1:1/seed", identity: IDENTITY, env: {} });
    assert.equal(r.ok, false);
    assert.equal(r.values, null);
    assert.match(r.detail, /could not be reached/);
  });

  test("a slow endpoint is abandoned at the cap, not waited on", async () => {
    const { url } = await serve(() => { /* never answers, on purpose */ });
    const t0 = Date.now();
    const r = await postSeed({ endpoint: `${url}/seed`, identity: IDENTITY, env: {}, timeoutMs: 300 });
    assert.equal(r.ok, false);
    assert.match(r.detail, /did not answer in 0\.3s/, r.detail);
    assert.ok(Date.now() - t0 < 5000, "the cap is a cap, not a suggestion");
  });
});

// ---- seedRun: the decision the runner acts on ------------------------------------------------------

describe("seedRun turns an endpoint into a sentence, or into a reason", () => {
  const quiet = () => {};

  test("a 500 is a setup failure that names the endpoint, the status and the body", async () => {
    const { url } = await serve((_req, res) => {
      res.writeHead(500, { "content-type": "text/plain" });
      res.end("no test database configured");
    });
    const r = await seedRun({ endpoint: `${url}/seed`, identity: IDENTITY, test: "open order {{orderId}}", log: quiet });
    assert.equal(r.text, undefined);
    assert.match(r.problem, /answered 500/, r.problem);
    assert.match(r.problem, /no test database configured/, "the body is the only clue the customer has");
    assert.match(r.problem, new RegExp(`${url}/seed`.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), r.problem);
    assert.match(r.problem, /not a verdict about your application/, "a setup failure must never read as a bug report");
  });

  test("a body that is not JSON says so, and quotes what it actually got", async () => {
    const { url } = await serve((_req, res) => {
      res.writeHead(200, { "content-type": "text/html" });
      res.end("<!doctype html><h1>Sign in to continue</h1>");
    });
    const r = await seedRun({ endpoint: `${url}/seed`, identity: IDENTITY, test: "open order {{orderId}}", log: quiet });
    assert.match(r.problem, /not JSON/, r.problem);
    // An auth redirect to a login page is the likeliest cause, and the first bytes are what say so.
    assert.match(r.problem, /Sign in to continue|doctype/, r.problem);
  });

  test("a token nobody filled is a setup failure, and it names both sides", async () => {
    // The sentence names state that was never built. Running anyway produces a `failed` verdict
    // about their application caused by our setup gap, which is the one thing this must never do.
    const { url } = await serve((_req, res) => json(res, 200, { couponCode: "SAVE20" }));
    const r = await seedRun({ endpoint: `${url}/seed`, identity: IDENTITY, test: "open order {{orderId}}", log: quiet });
    assert.equal(r.text, undefined, "a sentence with an unfilled placeholder must not reach the model");
    assert.match(r.problem, /\{\{orderId\}\}/, r.problem);
    assert.match(r.problem, /\{\{couponcode\}\}/, "say what it DID return, or the fix is a guessing game");
  });

  test("an endpoint that returns nothing still seeds, and the sentence is untouched", async () => {
    const { url } = await serve((_req, res) => json(res, 200, {}));
    const r = await seedRun({ endpoint: `${url}/seed`, identity: IDENTITY, test: "the pricing page loads", log: quiet });
    assert.equal(r.problem, undefined);
    assert.equal(r.text, "the pricing page loads");
    assert.deepEqual(r.secrets, []);
  });

  test("nothing printed about a seed carries a seeded value", async () => {
    // This line goes to a terminal, a CI log and a run summary. Names only.
    const { url } = await serve((_req, res) => json(res, 200, { orderToken: "tok_9f2c11ab7d" }));
    const lines = [];
    const r = await seedRun({ endpoint: `${url}/seed`, identity: IDENTITY, test: "open {{orderToken}}", log: (...a) => lines.push(a.join(" ")) });
    const out = lines.join("\n");
    assert.ok(!out.includes("tok_9f2c11ab7d"), `the seed log leaks the value:\n${out}`);
    assert.match(out, /orderToken|ordertoken/, "the NAME is what a person needs to see");
    assert.equal(r.text, "open {{ordertoken}}");
  });

  test("seedProblem always says whose fault it is not", () => {
    assert.match(seedProblem("http://x/seed", "it fell over."), /not a verdict about your application/);
  });
});

// ---- the whole path: a real browser, a real fixture, a scripted model --------------------------------

/**
 * The application under test AND the seed endpoint the customer would have written, in one server.
 *
 * The server invents the order. The page then only refunds a token the server issued, so a run that
 * filled the literal "{{ordertoken}}" — or an empty string, or a stale token — cannot reach the
 * proof text. That is what stops these tests being a mirror of the code they cover.
 *
 * TWO seeded values on purpose:
 *   orderToken  long, secret-shaped, used in a fill and in the URL, NEVER rendered on the page —
 *               so a byte scan of every artefact, the screenshot included, is a fair test.
 *   orderRef    short human id, rendered on the page and quoted as the run's proof — so proof
 *               masking is exercised. It is pixels in the PNG, which no masking can redact.
 */
async function fixture({ seedStatus = 200, seedBody = null, seedHangs = false, contentType = "application/json" } = {}) {
  const issued = new Set();
  let seedHits = 0;
  let n = 0;
  const state = await serve(async (req, res) => {
    const u = new URL(req.url, "http://127.0.0.1");
    if (u.pathname === "/seed") {
      seedHits++;
      state.lastBody = JSON.parse((await readBody(req)) || "{}");
      if (seedHangs) return; // never answers, on purpose
      if (seedStatus !== 200) {
        res.writeHead(seedStatus, { "content-type": "text/plain" });
        return res.end("the seed handler fell over");
      }
      if (contentType !== "application/json") {
        res.writeHead(200, { "content-type": contentType });
        return res.end("<!doctype html><h1>Sign in to continue</h1>");
      }
      if (seedBody) return json(res, 200, seedBody);
      n++;
      const orderToken = `tok_${"abcdef".slice(0, 3)}${n}${Math.random().toString(36).slice(2, 10)}`;
      const orderRef = `ORD-90${n}`;
      issued.add(orderToken);
      state.issued.push({ orderToken, orderRef });
      return json(res, 200, { orderToken, orderRef });
    }
    if (u.pathname === "/order") {
      const t = u.searchParams.get("t") || "";
      const rec = state.issued.find((o) => o.orderToken === t);
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      // The REF is on the page (it is what a human would see). The TOKEN never is.
      return res.end(`<!doctype html><meta charset="utf-8"><title>Order</title><h1>Order</h1>${
        rec ? `<p>Order ${rec.orderRef} is refundable.</p>` : "<p>No such order.</p>"}`);
    }
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(`<!doctype html><meta charset="utf-8"><title>Refunds</title><h1>Refunds</h1>
<label>Order token <input type="text" aria-label="Order token"></label>
<button id="go">Look up</button>
<script>
  document.getElementById('go').onclick = () => {
    location = '/order?t=' + encodeURIComponent(document.querySelector('[aria-label="Order token"]').value);
  };
</script>`);
  });
  state.issued = [];
  state.hits = () => seedHits;
  state.tokens = issued;
  return state;
}

/**
 * Run one test with a scripted model. Anything on 127.0.0.1 — the app, the seed endpoint, the
 * teardown endpoint — goes to the real network; anything else must be the model or it is a bug.
 */
async function run(script, opts = {}) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  const prompts = [];
  let attempt = 0;
  let turn = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model and local endpoints may be called here");
    const body = JSON.parse(init.body);
    if (body.messages.length === 1) {
      attempt++;
      turn = 0;
      prompts.push(body.messages[0].content);
    }
    turn++;
    // The whole conversation, not just the opening prompt: what the page said AFTER a step arrives
    // as a tool_result, and a script that could only see the first message would be blind to it.
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content: script(attempt, turn, init.body) }), text: async () => "" };
  };
  try {
    const code = await testCmd({
      maxSteps: 6, retries: 0, evidenceDir: scratch(),
      log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r), ...opts,
    });
    return { code, runs, prompts, attempts: attempt, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

/**
 * The scripted agent for the refund flow. It reads the token OUT OF THE PROMPT — so if the value
 * never reached the model, it types the wrong thing and the fixture refuses it. There is no way for
 * this script to "know" the answer the assertion wants.
 */
function refundAgent(finalise, { token: override } = {}) {
  // e1 is the heading, e2 the textbox, e3 the button: perception lists headings as elements too.
  return (attempt, turn, seen) => {
    const token = override ?? (/tok_[a-z0-9]+/.exec(String(seen)) || [""])[0];
    if (turn === 1) return [{ type: "tool_use", id: "t1", name: "fill", input: { ref: "e2", text: token, why: "type the seeded token" } }];
    if (turn === 2) return [{ type: "tool_use", id: "t2", name: "click", input: { ref: "e3", why: "look the order up" } }];
    return finalise(attempt, seen);
  };
}

/** The verdict the fixture's own page justifies: it names the ref only if the lookup succeeded. */
const refundVerdict = (passed) => (_a, seen) => {
  const ref = (/ORD-\d+/.exec(String(seen)) || [""])[0];
  const why = ref ? `Order ${ref} is refundable.` : "There was no such order.";
  return [{ type: "tool_use", id: "t3", name: "finish", input: { passed, why, proof: passed ? why : "" } }];
};

/** Every file under a directory tree, as [path, bytes]. */
function filesUnder(dir) {
  if (!existsSync(dir)) return [];
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) out.push(...filesUnder(p));
    else out.push([p, readFileSync(p)]);
  }
  return out;
}

describe("a seeded test, end to end", () => {
  test("the endpoint builds an order, the sentence uses it, and the run passes", noBrowser, async () => {
    const app = await fixture();
    const plans = scratch();
    const planPath = path.join(plans, "refund.json");
    const { code, runs, prompts, out } = await run(
      refundAgent(refundVerdict(true)),
      { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, plan: planPath },
    );

    assert.equal(code, 0, `exit ${code}\n${out}`);
    assert.equal(app.hits(), 1, "the seed endpoint was not called exactly once before the test");
    assert.deepEqual(runs.map((r) => r.status), ["passed"]);
    const { orderToken } = app.issued[0];

    // THE VALUE REALLY REACHED THE MODEL. Without this the fill could be passing for some other
    // reason and the placeholder would be doing nothing at all.
    assert.ok(prompts[0].includes(orderToken), "the model was never given the seeded value");
    assert.ok(!prompts[0].includes("{{ordertoken}}"), "the model was handed the placeholder instead of the value");

    // AND THE APP AGREED. A recording is only written when its proof is REALLY text on the page,
    // and this fixture prints the order ref for a token the seed endpoint issued and "No such
    // order." for anything else. So a recording on disk is the app's own signature on the fill.
    const recorded = JSON.parse(readFileSync(planPath, "utf8"));
    assert.equal(recorded.proof, "Order {{orderref}} is refundable.", `${out}\n${JSON.stringify(recorded)}`);
    assert.ok(!out.includes("No such order"), out);
  });

  test("a WRONG token really does fail, which is what makes the test above mean anything", noBrowser, async () => {
    // The negative control. Without it, every assertion above could be satisfied by a fixture that
    // says "refundable" to everybody — and this project has already shipped one order-independence
    // test on palindromic data that could not fail.
    const app = await fixture();
    const evidenceDir = scratch();
    const { code, runs, out } = await run(
      refundAgent(refundVerdict(false), { token: "tok_nevernevernever" }),
      { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, evidenceDir },
    );
    assert.equal(code, 1, out);
    assert.deepEqual(runs.map((r) => r.status), ["failed"]);
    // The APP's own words, read back off the page it left behind — not the scripted model's prose,
    // which could say anything. The fixture refuses a token it never issued.
    const txt = readFileSync(path.join(evidenceDir, ...readdirSync(evidenceDir), "failure.txt"), "utf8");
    assert.match(txt, /No such order/, txt);
  });

  test("the seeded value is in no artefact this run wrote, and the recording still names it", noBrowser, async () => {
    const app = await fixture();
    const plans = scratch();
    const evidenceDir = scratch();
    const summaryDir = scratch();
    const summaryFile = path.join(summaryDir, "summary.md");
    writeFileSync(summaryFile, "");
    const planPath = path.join(plans, "refund.json");

    // A FAILING run, so evidence is captured too: the screenshot, the page text and the URL — and
    // the URL is where a seeded token most often lives.
    const { code, runs, out } = await run(
      refundAgent((_a, seen) => {
        const ref = (/ORD-\d+/.exec(String(seen)) || [""])[0];
        return [{ type: "tool_use", id: "t3", name: "finish", input: { passed: false, why: `Order ${ref} showed no Refund button.`, proof: "" } }];
      }),
      { url: app.url, test: "open order {{orderToken}} and request a refund", seed: `${app.url}/seed`, plan: planPath, evidenceDir, env: { GITHUB_STEP_SUMMARY: summaryFile } },
    );
    assert.equal(code, 1, `a real failure must still be a failure: ${out}`);
    const { orderToken, orderRef } = app.issued[0];
    assert.ok(orderToken.length > 8, "precondition: the token is long enough for maskSecrets to touch");

    // THE GREP THAT MATTERS. Every byte of every file this run wrote, the PNG included.
    const written = [...filesUnder(plans), ...filesUnder(evidenceDir), ...filesUnder(summaryDir)];
    assert.ok(written.length >= 3, `nothing was written to compare: ${written.map(([p]) => p).join(", ")}`);
    for (const [file, bytes] of written) {
      assert.ok(!bytes.includes(orderToken), `${file} contains the seeded token`);
    }
    // And it did not vanish: the URL line of the evidence is still there, carrying the placeholder.
    const txt = readFileSync(path.join(evidenceDir, ...readdirSync(evidenceDir), "failure.txt"), "utf8");
    assert.match(txt, /\{\{ordertoken\}\}/, `the evidence URL lost the token instead of masking it:\n${txt}`);
    assert.match(txt, /^URL: http:\/\/127\.0\.0\.1/, "masking must not damage the rest of the evidence");

    // Nothing printed and nothing reported carries it either.
    assert.ok(!out.includes(orderToken), `the terminal output leaks the token:\n${out}`);
    assert.ok(!JSON.stringify(runs).includes(orderToken), "a reported run carries the token to the cloud");
    assert.match(JSON.stringify(runs), /\{\{ordertoken\}\}/, "the placeholder is what the step label should carry");
    // The human-facing ref is a different matter, and the PNG is a photograph of their own page:
    // pixels cannot be redacted, and claiming otherwise would be a guarantee we do not deliver.
    assert.ok(orderRef.length >= 4);
  });

  test("a recording made under one seeded token replays green under the next one", noBrowser, async () => {
    // The masking is only safe if it is not lossy. A recording that carries no value AND cannot
    // replay is not privacy, it is a broken feature — and this project has already shipped one
    // recording bug that turned a 39s suite into 600s by silently never replaying.
    const app = await fixture();
    const plans = scratch();
    const planPath = path.join(plans, "refund.json");
    const opts = { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, plan: planPath };
    const first = await run(refundAgent(refundVerdict(true)), opts);
    assert.equal(first.code, 0, first.out);
    const recorded = JSON.parse(readFileSync(planPath, "utf8"));
    assert.equal(recorded.steps[0].text, "{{ordertoken}}", `the recording kept a value: ${JSON.stringify(recorded)}`);
    assert.equal(recorded.proof, "Order {{orderref}} is refundable.", `the proof kept a value: ${recorded.proof}`);

    // Second run: a NEW order, a NEW token and ref, and the recording must still settle it with no
    // model call at all. The scripted model asserts if it is ever asked.
    const second = await run(() => assert.fail("the recording did not replay; the agent was woken"), opts);
    assert.equal(second.code, 0, second.out);
    assert.equal(app.issued.length, 2, "the second run got its own fixture");
    assert.deepEqual(second.runs.map((r) => r.status), ["passed"]);
    assert.match(second.out, /replayed 2 recorded steps|no model calls/, second.out);
    // Proof: the recording really resolved the SECOND order, not the first.
    assert.notEqual(app.issued[0].orderToken, app.issued[1].orderToken);
  });

  test("the recording cannot replay without a seed, which is what proves it holds no value", noBrowser, async () => {
    // The mirror of secrets.test.mjs's check on a login: if the same recording still passed with no
    // seeded values supplied, the value would have to be inside it.
    const app = await fixture();
    const plans = scratch();
    const planPath = path.join(plans, "refund.json");
    const first = await run(
      refundAgent(refundVerdict(true)),
      { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, plan: planPath },
    );
    assert.equal(first.code, 0, first.out);

    const browser = await chromium.launch();
    try {
      const page = await browser.newPage();
      const r = await replay(page, JSON.parse(readFileSync(planPath, "utf8")), []);
      assert.notEqual(r.status, "passed", "the recording settled a seeded test with no seeded value, so it must still contain one");
      await page.close();
    } finally {
      await browser.close();
    }
  });
});

describe("a seed that fails is errored, never a verdict about their app", () => {
  const cases = [
    ["a 500", { seedStatus: 500 }, /answered 500/],
    ["a body that is not JSON", { contentType: "text/html" }, /not JSON/],
    ["a shape we cannot use", { seedBody: { user: { id: 7 } } }, /"user"/],
  ];
  for (const [what, opts, expect] of cases) {
    test(`${what} errors and exits 2, and no browser is ever opened`, noBrowser, async () => {
      const app = await fixture(opts);
      const { code, runs, out } = await run(
        () => assert.fail("the model was called after a seed failure"),
        { url: app.url, test: "open order {{orderToken}} and request a refund", seed: `${app.url}/seed` },
      );
      // 2, never 1. 1 is the published contract for "the application is broken".
      assert.equal(code, 2, `exit ${code}\n${out}`);
      assert.deepEqual(runs.map((r) => r.status), ["errored"], "a setup failure reported as failed is a bug report about a bug nobody saw");
      assert.match(runs[0].reason, expect, runs[0].reason);
      assert.match(runs[0].reason, /--seed endpoint/, runs[0].reason);
      assert.match(out, /ERROR/, out);
    });
  }

  test("an endpoint that never answers is abandoned at the cap and errors", noBrowser, async () => {
    const app = await fixture({ seedHangs: true });
    const t0 = Date.now();
    const { code, runs } = await run(
      () => assert.fail("the model was called after a seed timeout"),
      { url: app.url, test: "open order {{orderToken}}", seed: `${app.url}/seed` },
    );
    assert.equal(code, 2);
    assert.equal(runs[0].status, "errored");
    assert.match(runs[0].reason, /did not answer in 10s/, runs[0].reason);
    assert.ok(Date.now() - t0 < 30_000, "a seed that hangs must not hang the build");
  });

  test("an unreachable endpoint errors rather than failing the app", noBrowser, async () => {
    const app = await fixture();
    const { code, runs } = await run(
      () => assert.fail("the model was called after a seed failure"),
      { url: app.url, test: "open order {{orderToken}}", seed: "http://127.0.0.1:1/seed" },
    );
    assert.equal(code, 2);
    assert.equal(runs[0].status, "errored");
    assert.match(runs[0].reason, /could not be reached/);
  });

  test("teardown still fires after a seed failed, because it may have built half a fixture", noBrowser, async () => {
    const app = await fixture({ seedStatus: 500 });
    let torn = null;
    const td = await serve(async (req, res) => {
      torn = JSON.parse(await readBody(req));
      json(res, 200, { ok: true });
    });
    const { code, out } = await run(
      () => assert.fail("the model was called after a seed failure"),
      { url: app.url, test: "open order {{orderToken}}", seed: `${app.url}/seed`, teardown: `${td.url}/teardown` },
    );
    assert.equal(code, 2);
    assert.ok(torn, `teardown never fired, so a half-built fixture is orphaned:\n${out}`);
    assert.equal(torn.status, "errored", "teardown is told what happened");
    assert.match(torn.email, /^smoltest\+/);
  });

  test("declining the production question never touches the seed endpoint", noBrowser, async () => {
    // "Nothing was opened and nothing was tested" has to stay true. A POST that fabricated an order
    // first would make it a lie, and the order would be in a production database.
    const app = await fixture();
    // A person at a terminal is the only situation the question is ever asked in, so one has to be
    // faked; forgetConfirmations() clears the per-origin memo other files may have filled.
    forgetConfirmations();
    const inTty = process.stdin.isTTY;
    const outTty = process.stdout.isTTY;
    process.stdin.isTTY = true;
    process.stdout.isTTY = true;
    let code = 0;
    let out = "";
    try {
      ({ code, out } = await run(
        () => assert.fail("nothing may run after declining"),
        {
          url: "https://shop.example-store.com", test: "open order {{orderToken}}", seed: `${app.url}/seed`,
          env: {}, ask: async () => "n",
        },
      ));
    } finally {
      process.stdin.isTTY = inTty;
      process.stdout.isTTY = outTty;
      forgetConfirmations();
    }
    assert.equal(code, 2, out);
    assert.match(out, /nothing ran/, out);
    assert.equal(app.hits(), 0, "the seed endpoint was called for a run the person declined");
  });
});

describe("without --seed, nothing changes", () => {
  test("no flag, no request", noBrowser, async () => {
    const app = await fixture();
    const { code } = await run(
      () => [{ type: "tool_use", id: "t1", name: "finish", input: { passed: true, why: "The refunds page loads.", proof: "Refunds" } }],
      { url: app.url, test: "the refunds page loads" },
    );
    assert.equal(code, 0);
    assert.equal(app.hits(), 0, "a run with no --seed reached a seed endpoint");
  });

  test("an unknown placeholder is still named and left in the sentence, exactly as before", noBrowser, async () => {
    // The regression this feature could most easily cause: --seed makes an unfilled token a setup
    // failure, and that rule must not leak onto the runs of everybody who has never used the flag.
    const app = await fixture();
    const { code, runs, prompts, out } = await run(
      () => [{ type: "tool_use", id: "t1", name: "finish", input: { passed: true, why: "The refunds page loads.", proof: "Refunds" } }],
      { url: app.url, test: "the refunds page loads for {{emial}}" },
    );
    assert.equal(code, 0, `an unknown token with no --seed must not stop the run:\n${out}`);
    assert.deepEqual(runs.map((r) => r.status), ["passed"]);
    assert.match(out, /\{\{emial\}\} is not a placeholder/, out);
    assert.ok(prompts[0].includes("{{emial}}"), "the token must reach the model as written, never dropped");
  });

  test("the recording written without --seed is byte-identical to compile()'s own output", noBrowser, async () => {
    // "Inert when unused" is a claim about bytes, so it is checked as bytes.
    const app = await fixture();
    const plans = scratch();
    const planPath = path.join(plans, "loads.json");
    const { code } = await run(
      (_a, turn) => turn === 1
        ? [{ type: "tool_use", id: "t1", name: "click", input: { ref: "e3", why: "look up nothing" } }]
        : [{ type: "tool_use", id: "t2", name: "finish", input: { passed: true, why: "It went somewhere.", proof: "No such order" } }],
      { url: app.url, test: "the refunds page loads", plan: planPath },
    );
    assert.equal(code, 0);
    const onDisk = JSON.parse(readFileSync(planPath, "utf8"));
    const golden = compile(app.url, [{ ok: true, n: 1, ms: 1, action: { kind: "click" }, target: { role: "button", name: "Look up" } }], "No such order");
    // Every field compile() owns, compared whole. Fields other parts of the runner add afterwards
    // (the engine the recording was made against) are theirs to test, not this feature's.
    assert.deepEqual({ startUrl: onDisk.startUrl, steps: onDisk.steps, proof: onDisk.proof }, golden, "the no-seed recording is not what compile() with no secrets produces");
    assert.ok(!JSON.stringify(onDisk).includes("{{"), `a run with no --seed wrote a placeholder into the recording: ${JSON.stringify(onDisk)}`);
  });
});

// ---- the wiring: the suite, and the process's own exit code -----------------------------------------

test("the suite hands --seed to every test, so each one gets its own fixture", async () => {
  const seen = [];
  await runSuite({
    tests: [
      { file: "tests/a.md", name: "A", test: "open order {{orderId}}", id: "a", planPath: "/dev/null/a.json" },
      { file: "tests/b.md", name: "B", test: "open order {{orderId}}", id: "b", planPath: "/dev/null/b.json" },
    ],
    url: "http://127.0.0.1:1",
    seed: "http://seed.test/seed",
    log: () => {},
    mkdir: () => {},
    hasPlan: () => false,
    runTest: async (o) => {
      seen.push(o.seed);
      o.onRun({ status: "passed", mode: "agent", reason: "ok" });
      return 0;
    },
  });
  assert.deepEqual(seen, ["http://seed.test/seed", "http://seed.test/seed"]);
});

test("the CLI itself: --seed reaches the endpoint and the process exits 0", noBrowser, async () => {
  // In a CHILD, so bin/ does the flag parsing and the exit code is the process's own — the things
  // an in-process call cannot prove.
  const app = await fixture();
  const dir = scratch();
  const preload = path.join(dir, "scripted-model.mjs");
  writeFileSync(preload, `
const real = globalThis.fetch;
globalThis.fetch = async (target, init = {}) => {
  if (!String(target).includes("api.anthropic.com")) return real(target, init);
  const body = JSON.parse(init.body);
  const seen = JSON.stringify(body.messages);
  const token = (/tok_[a-z0-9]+/.exec(seen) || [""])[0];
  const ref = (/ORD-\\d+/.exec(seen) || [""])[0];
  const turn = body.messages.length;
  const content = turn === 1
    ? [{ type: "tool_use", id: "t1", name: "fill", input: { ref: "e2", text: token, why: "type the seeded token" } }]
    : turn === 3
      ? [{ type: "tool_use", id: "t2", name: "click", input: { ref: "e3", why: "look it up" } }]
      : [{ type: "tool_use", id: "t3", name: "finish", input: { passed: ref !== "", why: ref ? "Order " + ref + " is refundable." : "There was no such order.", proof: ref ? "Order " + ref + " is refundable." : "" } }];
  return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content }) };
};
`);
  const r = await child([bin, "test", "--url", app.url, "--seed", `${app.url}/seed`, "--test", "open order {{orderToken}} and check it can be refunded", "--retries", "0", "--yes"], {
    ...process.env,
    ANTHROPIC_API_KEY: "sk-ant-test",
    SMOLANALYTICS_SEED_SECRET: "s3cr3t",
    NODE_OPTIONS: `--import ${new URL(`file://${path.resolve(preload)}`).href}`,
  });
  assert.equal(r.status, 0, `exit ${r.status}\n${r.stdout}\n${r.stderr}`);
  assert.equal(app.hits(), 1, r.stdout);
  assert.match(r.stdout, /PASS/, r.stdout);
  assert.match(r.stdout, /seeded 2 values/, r.stdout);
  // The token reached the APP, not the placeholder: the fixture only prints an order ref for a
  // token it issued, and the agent's own verdict quotes it back.
  assert.match(r.stdout, /is refundable/, r.stdout);
  assert.ok(!r.stdout.includes("no such order"), r.stdout);
  // The step really was the fill and the button, in that order.
  assert.match(r.stdout, /fill "Order token"/, r.stdout);
  assert.match(r.stdout, /click button "Look up"/, r.stdout);
  // And nothing printed carries the value.
  assert.ok(!r.stdout.includes(app.issued[0].orderToken), `the CLI leaks the seeded token:\n${r.stdout}`);
  assert.match(r.stdout, /\{\{ordertoken\}\}/, "the placeholder is what the step label should carry");
});

test("--seed is documented where somebody looking for it would look", () => {
  const help = readFileSync(bin, "utf8");
  assert.match(help, /--seed <url>/);
  assert.match(help, /SMOLANALYTICS_SEED_SECRET/);
  const readme = readFileSync(fileURLToPath(new URL("../README.md", import.meta.url)), "utf8");
  assert.match(readme, /--seed/, "a flag nobody can find is a flag nobody has");
  assert.match(readme, /SMOLANALYTICS_SEED_SECRET/);
});

/** A child process, run without blocking the event loop this file's fixture servers answer on. */
function child(argv, env) {
  return new Promise((resolve, reject) => {
    const c = spawn(process.execPath, argv, { env });
    let stdout = "";
    let stderr = "";
    c.stdout.setEncoding("utf8");
    c.stderr.setEncoding("utf8");
    c.stdout.on("data", (d) => (stdout += d));
    c.stderr.on("data", (d) => (stderr += d));
    c.on("error", reject);
    c.on("close", (status) => resolve({ status, stdout, stderr }));
  });
}
