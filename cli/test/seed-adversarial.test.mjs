// THE RED TEAM ON `--seed`. Every test here states a REQUIREMENT the feature makes of itself, in a
// shape that can fail, against a real browser and a real HTTP endpoint.
//
// --seed calls a customer's URL before every test and puts what comes back into a sentence. There
// are exactly two ways that ends badly, and this file is only about those two:
//
//   A LEAKED TOKEN   the seeded value — an order id, and just as easily a session token or a signed
//                    magic link — surviving into something durable: the recording the CI template
//                    caches and users are told to commit, the evidence uploaded as an artifact, the
//                    step summary, the pull request comment, the row posted to a project.
//
//   A BLAMED APP     our setup step not working and the customer reading "your refund flow is
//                    broken" on their pull request. Every seed problem is `errored`/2, names the
//                    endpoint, and says in words that it is not a verdict about their application.
//
// The seeded values here are deliberately hostile in ways the feature's own fixture is not:
//   * one is navigated to, so it lands in a `goto` step rather than a `fill`,
//   * one contains + / = so the browser PERCENT-ENCODES it and a byte scan for the raw value —
//     which is the scan the feature's own leak test performs — cannot see it.
// Both are exactly the shape of the thing the module header promises to protect ("a session token
// or a signed magic link"), and a magic link is a URL.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtempSync, readFileSync, readdirSync, statSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { testCmd, rebase, replay } from "../lib/test.mjs";
import { runSuite } from "../lib/suite.mjs";
import { newIdentity, forgetConfirmations } from "../lib/safety.mjs";
import { postSeed, seedRun } from "../lib/seed.mjs";
import { guardPairs, maskUrl, resolveUrl } from "../lib/seedguard.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed" };

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-seedadv-"));

/**
 * close() never fires its callback while a keep-alive socket is open, and Playwright holds them.
 * closeAllConnections() FIRST, or this file wedges until somebody kills it ten minutes later.
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

/**
 * The application under test AND the seed endpoint the customer would have written.
 *
 * The order is real: /order only says "refundable" for a token /seed actually issued, so a run that
 * navigated with the literal "{{ordertoken}}", an empty string or last run's token cannot reach the
 * proof text. Nothing here mirrors the assertion; the app decides.
 *
 * TWO hostile values on purpose:
 *   orderToken   long, URL-safe, NEVER rendered on the page — the agent NAVIGATES to it, so it
 *                lands in a `goto` step and not in a `fill`.
 *   sessionKey   contains + / = , so the moment a browser puts it in a query string it is
 *                percent-encoded and a byte scan for the raw value goes blind.
 */
async function fixture({ seedStatus = 200, seedBody = null, seedHangs = false, contentType = "application/json", secretSink = null } = {}) {
  let seedHits = 0;
  let live = 0;
  let peak = 0;
  let n = 0;
  const state = await serve(async (req, res) => {
    const u = new URL(req.url, "http://127.0.0.1");
    if (u.pathname === "/seed") {
      seedHits++;
      live++;
      peak = Math.max(peak, live);
      state.lastBody = JSON.parse((await readBody(req)) || "{}");
      state.lastAuth = req.headers.authorization || "";
      state.seedAt.push(Date.now());
      if (secretSink) secretSink.push(req.headers.authorization || "");
      if (seedHangs) return; // never answers, on purpose
      const finish = (fn) => { live--; return fn(); };
      if (seedStatus !== 200) {
        res.writeHead(seedStatus, { "content-type": "text/plain" });
        return finish(() => res.end("the seed handler fell over"));
      }
      if (contentType !== "application/json") {
        res.writeHead(200, { "content-type": contentType });
        return finish(() => res.end("<!doctype html><h1>Sign in to continue</h1>"));
      }
      if (seedBody !== null) return finish(() => json(res, 200, seedBody));
      await new Promise((r) => setTimeout(r, 25)); // long enough for overlap to be visible
      n++;
      const orderToken = `tok${n}${Math.random().toString(36).slice(2, 12)}${Math.random().toString(36).slice(2, 12)}`;
      const sessionKey = `sk+live+${Math.random().toString(36).slice(2, 12)}/${n}=`;
      const orderRef = `ORD-90${n}`;
      state.issued.push({ orderToken, sessionKey, orderRef });
      return finish(() => json(res, 200, { orderToken, sessionKey, orderRef }));
    }
    if (u.pathname.startsWith("/order/")) {
      // The same order, addressed by PATH. The WHATWG URL parser escapes { and } in a path and not
      // in a query, so this is the route that exercises that difference.
      const t = decodeURIComponent(u.pathname.slice("/order/".length));
      const rec = state.issued.find((o) => o.orderToken === t);
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      return res.end(`<!doctype html><meta charset="utf-8"><title>Order</title><h1>Order</h1>${
        rec ? `<p>Order ${rec.orderRef} is refundable.</p>` : "<p>No such order.</p>"}`);
    }
    if (u.pathname === "/order") {
      const t = u.searchParams.get("t") || "";
      const rec = state.issued.find((o) => o.orderToken === t || o.sessionKey === t);
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      // The REF is on the page. Neither token ever is.
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
  state.seedAt = [];
  state.hits = () => seedHits;
  state.peak = () => peak;
  return state;
}

/**
 * One test, scripted model, everything on 127.0.0.1 going to the real network.
 * `modelAt` records WHEN the model was first asked, so ordering can be asserted rather than assumed.
 */
async function run(script, opts = {}) {
  const realFetch = globalThis.fetch;
  const key = process.env.ANTHROPIC_API_KEY;
  process.env.ANTHROPIC_API_KEY = "sk-ant-test";
  const runs = [];
  const lines = [];
  const prompts = [];
  const modelAt = [];
  let attempt = 0;
  let turn = 0;
  globalThis.fetch = async (target, init = {}) => {
    if (String(target).startsWith("http://127.0.0.1:")) return realFetch(target, init);
    assert.match(String(target), /api\.anthropic\.com/, "nothing but the model and local endpoints may be called here");
    modelAt.push(Date.now());
    const body = JSON.parse(init.body);
    if (body.messages.length === 1) {
      attempt++;
      turn = 0;
      prompts.push(body.messages[0].content);
    }
    turn++;
    return { ok: true, status: 200, json: async () => ({ stop_reason: "tool_use", content: script(attempt, turn, init.body) }), text: async () => "" };
  };
  try {
    const code = await testCmd({
      maxSteps: 6, retries: 0, evidenceDir: scratch(),
      log: (...a) => lines.push(a.join(" ")), onRun: (r) => runs.push(r), ...opts,
    });
    return { code, runs, prompts, modelAt, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
    if (key === undefined) delete process.env.ANTHROPIC_API_KEY;
    else process.env.ANTHROPIC_API_KEY = key;
  }
}

/**
 * An agent that NAVIGATES to the seeded order rather than typing it — which is what the tool
 * description ("use this only when the test says to open a page") tells it to do for a sentence
 * that starts with the word "open". It reads the token out of the prompt, so if the value never
 * reached the model it navigates to nothing and the app refuses it.
 */
function navigatingAgent(base, verdict) {
  return (_attempt, turn, seen) => {
    const token = (/tok\d[a-z0-9]{15,}/.exec(String(seen)) || [""])[0];
    if (turn === 1) return [{ type: "tool_use", id: "g1", name: "goto", input: { url: `${base}/order?t=${token}`, why: `open the seeded order ${token}` } }];
    return verdict(String(seen));
  };
}

/** An agent that types the seeded key into the form and lets the PAGE build the URL. */
function typingAgent(verdict) {
  return (_attempt, turn, seen) => {
    const key = (/sk\+live\+[a-z0-9]+\/\d=/.exec(String(seen)) || [""])[0];
    if (turn === 1) return [{ type: "tool_use", id: "f1", name: "fill", input: { ref: "e2", text: key, why: "type the seeded session key" } }];
    if (turn === 2) return [{ type: "tool_use", id: "c1", name: "click", input: { ref: "e3", why: "look the order up" } }];
    return verdict(String(seen));
  };
}

const passWithRef = (seen) => {
  const ref = (/ORD-\d+/.exec(seen) || [""])[0];
  const why = ref ? `Order ${ref} is refundable.` : "There was no such order.";
  return [{ type: "tool_use", id: "v1", name: "finish", input: { passed: Boolean(ref), why, proof: ref ? why : "" } }];
};
const failWithRef = (seen) => {
  const ref = (/ORD-\d+/.exec(seen) || [""])[0];
  return [{ type: "tool_use", id: "v1", name: "finish", input: { passed: false, why: `Order ${ref} showed no Refund button.`, proof: "" } }];
};

// ---- A LEAKED TOKEN ------------------------------------------------------------------------------

describe("a leaked token", () => {
  test("a seeded value the agent NAVIGATED to is in no artefact and no line of output", noBrowser, async () => {
    // The feature's own leak test drives the value through a `fill`. A sentence that says "open
    // order {{orderId}}" invites a `goto` instead, and a goto's URL is written to the recording,
    // the step label, the terminal, the reported run and from there to the pull request comment.
    const app = await fixture();
    const plans = scratch();
    const evidenceDir = scratch();
    const summaryDir = scratch();
    const summaryFile = path.join(summaryDir, "summary.md");
    writeFileSync(summaryFile, "");
    const planPath = path.join(plans, "refund.json");

    const { code, runs, out } = await run(
      navigatingAgent(app.url, failWithRef),
      { url: app.url, test: "open order {{orderToken}} and request a refund", seed: `${app.url}/seed`, plan: planPath, evidenceDir, env: { GITHUB_STEP_SUMMARY: summaryFile } },
    );
    assert.equal(code, 1, `a real failure must stay a failure: ${out}`);
    const { orderToken } = app.issued[0];
    assert.ok(orderToken.length > 8, "precondition: long enough for maskSecrets to touch");
    // The app agreed the token was real, so this is not a test of a token nobody used.
    // The masked ref, not the raw one: the run's own prose is masked, and its PRESENCE is the app
    // saying it recognised the token. "Order  showed no Refund button." would mean it did not.
    assert.match(out, /Order \{\{orderref\}\} showed/, `the navigation never reached a real order:\n${out}`);

    for (const [file, bytes] of [...filesUnder(plans), ...filesUnder(evidenceDir), ...filesUnder(summaryDir)]) {
      assert.ok(!bytes.includes(orderToken), `${file} contains the seeded token`);
    }
    assert.ok(!out.includes(orderToken), `the terminal leaks the navigated token:\n${out}`);
    assert.ok(!JSON.stringify(runs).includes(orderToken), "a reported run carries the navigated token to the cloud");
  });

  test("a seeded value the BROWSER percent-encoded is masked too", noBrowser, async () => {
    // maskSecrets is a byte-for-byte substring replace, so a token containing + / = is invisible to
    // it the instant a browser puts it in a query string. The evidence file records page.url(), the
    // one place that encoded form always lands, and it is uploaded as a CI artifact.
    const app = await fixture();
    const evidenceDir = scratch();
    const { code, runs, out } = await run(
      typingAgent(failWithRef),
      { url: app.url, test: "open order {{sessionKey}} and request a refund", seed: `${app.url}/seed`, evidenceDir },
    );
    assert.equal(code, 1, out);
    const { sessionKey } = app.issued[0];
    const encoded = encodeURIComponent(sessionKey);
    assert.notEqual(encoded, sessionKey, "precondition: this value is changed by URL encoding");
    assert.match(out, /Order \{\{orderref\}\} showed/, `the app never accepted the key, so nothing was proved:\n${out}`);

    const txt = readFileSync(path.join(evidenceDir, ...readdirSync(evidenceDir), "failure.txt"), "utf8");
    assert.ok(!txt.includes(encoded), `the evidence file carries the percent-encoded seeded value:\n${txt}`);
    assert.ok(!txt.includes(sessionKey), `the evidence file carries the raw seeded value:\n${txt}`);
    assert.ok(!out.includes(encoded), `the terminal carries the percent-encoded seeded value:\n${out}`);
    assert.ok(!JSON.stringify(runs).includes(encoded), "the reported run carries the percent-encoded seeded value");
  });

  test("a navigation that FAILS does not quote the seeded value in the error it prints", noBrowser, async () => {
    // The browser's own error text quotes the URL it could not reach, and that text is the step's
    // `detail` — printed on the step line, kept on the step, and reported. A failed goto is the
    // most likely goto there is, because the fixture the URL names is the thing that may not exist.
    const app = await fixture();
    const { code, out } = await run(
      (_a, turn, seen) => {
        const token = (/tok\d[a-z0-9]{15,}/.exec(String(seen)) || [""])[0];
        // Port 1: refused immediately, on every platform, with the URL in the message.
        if (turn === 1) return [{ type: "tool_use", id: "g1", name: "goto", input: { url: `http://127.0.0.1:1/order?t=${token}`, why: "open the seeded order" } }];
        return [{ type: "tool_use", id: "v1", name: "finish", input: { passed: false, why: "The order page never loaded.", proof: "" } }];
      },
      { url: app.url, test: "open order {{orderToken}} and request a refund", seed: `${app.url}/seed` },
    );
    assert.equal(code, 1, out);
    const { orderToken } = app.issued[0];
    assert.match(out, /✗\s+1 goto/, `the navigation did not fail, so nothing was proved:\n${out}`);
    assert.ok(!out.includes(orderToken), `the browser's error text leaks the seeded token:\n${out}`);
  });

  test("the expanded pair list is ordered, floored, and exactly reversible", () => {
    // guardPairs is what makes every other maskSecrets call site cover an encoded value, so its own
    // rules are asserted rather than assumed.
    const pairs = guardPairs([{ value: "a+b/c=d", token: "{{key}}" }, { value: "ab", token: "{{tiny}}" }]);
    const lens = pairs.map((p) => p.value.length);
    assert.deepEqual(lens, [...lens].sort((x, y) => y - x), `longest value first, or a short value corrupts the long one it lives inside: ${JSON.stringify(pairs)}`);
    assert.ok(!pairs.some((p) => p.value.length < 4), `maskSecrets's four-character floor was smuggled past: ${JSON.stringify(pairs)}`);
    assert.ok(pairs.some((p) => p.value === encodeURIComponent("a+b/c=d")), `the percent-encoded form is missing: ${JSON.stringify(pairs)}`);
    // Reversible, which is the whole reason each encoding gets its own placeholder spelling.
    const url = `https://app/o/${encodeURIComponent("a+b/c=d")}?raw=a+b/c=d`;
    const masked = maskUrl(url, pairs);
    assert.ok(!masked.includes("a+b/c=d"), masked);
    assert.ok(!masked.includes(encodeURIComponent("a+b/c=d")), masked);
    assert.equal(resolveUrl(masked, pairs), url, "masking a URL is not reversible, so a recording could not replay");
  });

  test("a recording whose step NAVIGATED to a seeded value replays under the next one", noBrowser, async () => {
    // The cost half of the same bug. A goto URL frozen with run one's token replays against an
    // order that no longer matches, so every later run is `stale` and pays for a full agent run —
    // this repo's rebase() incident, rebuilt inside --seed.
    const app = await fixture();
    const plans = scratch();
    const planPath = path.join(plans, "refund.json");
    const opts = { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, plan: planPath };

    const first = await run(navigatingAgent(app.url, passWithRef), opts);
    assert.equal(first.code, 0, first.out);
    const recorded = JSON.parse(readFileSync(planPath, "utf8"));
    assert.equal(recorded.steps[0].kind, "goto", `expected a navigation to be recorded: ${JSON.stringify(recorded)}`);
    // The durable artefact, checked as bytes: this file is cached by the CI template and users are
    // told to commit it.
    assert.ok(!readFileSync(planPath, "utf8").includes(app.issued[0].orderToken), `the recording froze run one's seeded token: ${readFileSync(planPath, "utf8")}`);
    assert.match(recorded.steps[0].url, /\{\{ordertoken\}\}/, `the navigation lost the token instead of masking it: ${recorded.steps[0].url}`);

    const second = await run(() => assert.fail("the recording did not replay; the agent was woken at full price"), opts);
    assert.equal(second.code, 0, second.out);
    assert.equal(app.issued.length, 2, "the second run got its own fixture");
    assert.notEqual(app.issued[0].orderToken, app.issued[1].orderToken);
    assert.deepEqual(second.runs.map((r) => r.status), ["passed"]);
    assert.match(second.out, /no model calls/, second.out);
  });

  test("the seed secret is not forwarded off the origin it was set for", async () => {
    // The credential the flag needs is the other thing that can leak, and a customer's seed URL is
    // free to redirect. Both halves are asserted, because only the pair proves anything: the
    // SAME-origin redirect must still carry it — otherwise the sink below is simply never seeing
    // any header and the cross-origin assertion is vacuous — and the cross-origin one must not.
    const seen = [];
    const other = await serve((req, res) => {
      seen.push(["other", req.headers.authorization || ""]);
      json(res, 200, { orderId: "A-1042" });
    });
    const home = await serve((req, res) => {
      if (req.url === "/sink") {
        seen.push(["home", req.headers.authorization || ""]);
        return json(res, 200, { orderId: "A-1042" });
      }
      res.writeHead(302, { location: req.url === "/same" ? `${home.url}/sink` : `${other.url}/sink` });
      res.end();
    });
    const env = { SMOLANALYTICS_SEED_SECRET: "SEEDSECRET123" };
    const identity = newIdentity({ runId: "redirect1" });

    const same = await postSeed({ endpoint: `${home.url}/same`, identity, test: "open order {{orderId}}", env });
    assert.ok(same.ok, `precondition: a redirect must still be followed: ${same.detail}`);
    assert.deepEqual(seen, [["home", "Bearer SEEDSECRET123"]], "the secret never reached the endpoint's own redirect target, so this test proves nothing");

    seen.length = 0;
    const cross = await postSeed({ endpoint: `${home.url}/cross`, identity, test: "open order {{orderId}}", env });
    assert.ok(cross.ok, cross.detail);
    assert.deepEqual(seen, [["other", ""]], "the seed secret was forwarded to another origin across a redirect");
  });

  test("nothing a seeded run writes down carries the seed secret", noBrowser, async () => {
    const app = await fixture();
    const plans = scratch();
    const evidenceDir = scratch();
    const planPath = path.join(plans, "refund.json");
    const { code, runs, out } = await run(
      navigatingAgent(app.url, passWithRef),
      { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed`, plan: planPath, evidenceDir, env: { SMOLANALYTICS_SEED_SECRET: "SEEDSECRET123" } },
    );
    assert.equal(code, 0, out);
    assert.equal(app.lastAuth, "Bearer SEEDSECRET123", "precondition: the secret really was sent");
    for (const [file, bytes] of [...filesUnder(plans), ...filesUnder(evidenceDir)]) {
      assert.ok(!bytes.includes("SEEDSECRET123"), `${file} contains the seed secret`);
    }
    assert.ok(!out.includes("SEEDSECRET123"), out);
    assert.ok(!JSON.stringify(runs).includes("SEEDSECRET123"), "a reported run carries the seed secret");
  });
});

// ---- A BLAMED APP -------------------------------------------------------------------------------

describe("a blamed app", () => {
  // Words that, in a CI log above a red build, read as a report about the customer's application.
  const BLAME = /\b(your (app|application|site|page|flow) (is|was|has)|is broken|does not work|bug in your)\b/i;

  const cases = [
    ["a 500", { seedStatus: 500 }, /answered 500/],
    ["a 404", { seedStatus: 404 }, /answered 404/],
    ["HTML instead of JSON", { contentType: "text/html" }, /not JSON/],
    ["a JSON null", { seedBody: null, body: "null" }, /returned null/],
    ["a JSON array", { seedBody: ["a"] }, /a JSON array/],
    ["a JSON string", { seedBody: "\"nope\"" }, /a JSON string/],
    ["a nested object", { seedBody: { order: { id: 7 } } }, /"order"/],
    ["a null value", { seedBody: { orderToken: null } }, /"orderToken"/],
    ["500 keys, none of them the one asked for", { seedBody: Object.fromEntries(Array.from({ length: 500 }, (_, i) => [`k${i}`, `value-${i}`])) }, /did not return/],
  ];

  for (const [what, opts, detail] of cases) {
    test(`${what} is errored, names the endpoint, and blames nobody`, noBrowser, async () => {
      // A raw JSON body has to bypass fixture()'s object serialisation.
      const raw = opts.body;
      const app = await fixture(raw ? { seedBody: undefined, contentType: "application/json" } : opts);
      const endpoint = `${app.url}/seed`;
      const { code, runs, out } = raw
        ? await (async () => {
            const s = await serve((_req, res) => json(res, 200, raw));
            return run(() => assert.fail("the model was called for a run that could not be set up"), { url: app.url, test: "open order {{orderToken}}", seed: `${s.url}/seed` });
          })()
        : await run(() => assert.fail("the model was called for a run that could not be set up"), { url: app.url, test: "open order {{orderToken}}", seed: endpoint });

      assert.equal(code, 2, `a setup problem must exit 2, not 1:\n${out}`);
      assert.deepEqual(runs.map((r) => r.status), ["errored"], `the verdict must be errored, never failed:\n${out}`);
      const said = `${out}\n${runs.map((r) => r.reason).join("\n")}`;
      assert.match(said, /--seed endpoint http:\/\/127\.0\.0\.1:\d+\/seed/, `the endpoint is not named:\n${said}`);
      assert.match(said, /not a verdict about your application/, `nothing says whose fault it is not:\n${said}`);
      assert.match(said, detail, `the reason does not say what actually happened:\n${said}`);
      assert.ok(!BLAME.test(said), `this reads as a bug report about the customer's app:\n${said}`);
      assert.equal(app.hits() >= 0, true);
    });
  }

  test("an endpoint that never answers is abandoned at the cap, not waited on", async () => {
    const app = await fixture({ seedHangs: true });
    const started = Date.now();
    const lines = [];
    const r = await seedRun({
      endpoint: `${app.url}/seed`,
      identity: newIdentity({ runId: "hang1" }),
      test: "open order {{orderToken}}",
      log: (...a) => lines.push(a.join(" ")),
      timeoutMs: 300,
    });
    assert.ok(Date.now() - started < 4000, "the cap did not hold");
    assert.match(r.problem, /did not answer in 0\.3s/);
    assert.match(r.problem, /not a verdict about your application/);
  });

  test("a seed that fails still runs teardown, because it may have built half a fixture", noBrowser, async () => {
    const torn = [];
    const td = await serve(async (req, res) => {
      torn.push(JSON.parse((await readBody(req)) || "{}"));
      json(res, 200, { ok: true });
    });
    const app = await fixture({ seedStatus: 500 });
    const { code } = await run(
      () => assert.fail("the model was called"),
      { url: app.url, test: "open order {{orderToken}}", seed: `${app.url}/seed`, teardown: `${td.url}/teardown` },
    );
    assert.equal(code, 2);
    assert.equal(torn.length, 1, "a half-built fixture was orphaned");
    assert.equal(torn[0].status, "errored");
  });
});

// ---- ORDERING -----------------------------------------------------------------------------------

describe("ordering", () => {
  test("the seed answers BEFORE the model is asked, and the prompt holds no unresolved token", noBrowser, async () => {
    const app = await fixture();
    const { code, prompts, modelAt, out } = await run(
      navigatingAgent(app.url, passWithRef),
      { url: app.url, test: "open order {{orderToken}} and check it can be refunded", seed: `${app.url}/seed` },
    );
    assert.equal(code, 0, out);
    assert.ok(modelAt.length > 0, "the model was never called");
    assert.ok(app.seedAt[0] < modelAt[0], "the model was asked before the seed endpoint was");
    assert.ok(prompts[0].includes(app.issued[0].orderToken), "the seeded value never reached the model");
    assert.ok(!/\{\{\s*ordertoken\s*\}\}/i.test(prompts[0]), `the model was handed a raw placeholder:\n${prompts[0]}`);
  });

  test("the production notice is printed before a single fixture is built", async () => {
    // The notice's own words are "nothing was opened and nothing was tested". A POST that had
    // already fabricated an order in a production database would make that a lie even on the path
    // where nobody is at a terminal to be asked — which is every CI run, and which is the path
    // taken here. The browser is stubbed out so this test makes no request off 127.0.0.1.
    const app = await fixture();
    forgetConfirmations();
    const { out } = await run(
      () => assert.fail("the model was called with no browser"),
      {
        url: "https://shop.example-store.com/", test: "open order {{orderToken}}", seed: `${app.url}/seed`,
        env: {}, loadBrowser: async () => ({ pw: null, problem: "no browser in this test" }),
      },
    );
    forgetConfirmations();
    const notice = out.indexOf("shop.example-store.com");
    const seeding = out.indexOf("seeding: POST");
    assert.ok(notice >= 0, `the production notice was never printed:\n${out}`);
    assert.ok(seeding >= 0, `the seeding line was never printed:\n${out}`);
    assert.ok(notice < seeding, `a fixture was built before the person was warned:\n${out}`);
    assert.equal(app.hits(), 1, "the notice path skipped the seed entirely");
  });

  test("a recording rebased onto another origin still resolves its seeded path", noBrowser, async () => {
    // rebase() round-trips every goto step through the URL parser when the origin changes, which is
    // every pull request. The parser percent-escapes { and } in a PATH, so a masked
    // /order/{{ordertoken}} arrives at replay as /order/%7B%7Bordertoken%7D%7D — and a plain
    // unmask would find nothing, navigate to the literal escaped text and report the test stale on
    // every run forever, at full agent price.
    const app = await fixture();
    const seeded = await seedRun({
      endpoint: `${app.url}/seed`,
      identity: newIdentity({ runId: "rebase1" }),
      test: "open order {{orderToken}}",
      log: () => {},
    });
    assert.ok(!seeded.problem, seeded.problem);
    const { orderToken, orderRef } = app.issued[0];

    // A recording made on a preview host that no longer exists, masked exactly as compile() would.
    const plan = rebase({
      startUrl: "https://preview-old.example.invalid/",
      steps: [{ kind: "goto", url: "https://preview-old.example.invalid/order/{{ordertoken}}" }],
      proof: `Order {{orderref}} is refundable.`,
    }, `${app.url}/`);
    assert.ok(!JSON.stringify(plan).includes(orderToken), "precondition: the recording holds no value");

    const browser = await chromium.launch();
    try {
      const page = await browser.newPage();
      const r = await replay(page, plan, seeded.secrets);
      assert.equal(r.status, "passed", `${r.status}: ${r.detail || r.proof}\n${page.url()}`);
      assert.ok(page.url().includes(encodeURIComponent(orderToken)) || page.url().includes(orderToken), page.url());
      assert.ok(orderRef.length >= 4);
    } finally {
      await browser.close().catch(() => {});
    }
  });
});

// ---- THE SUITE ----------------------------------------------------------------------------------

describe("a suite does not stampede the seed endpoint", () => {
  test("six tests at three workers make six requests, never more than three at once", async () => {
    const app = await fixture();
    const tests = Array.from({ length: 6 }, (_, i) => ({ file: `tests/${i}.md`, name: `T${i}`, test: "open order {{orderToken}}", id: `t${i}`, planPath: `/dev/null/${i}.json` }));
    const identity = () => newIdentity({ runId: `w${Math.random().toString(36).slice(2, 8)}` });
    await runSuite({
      tests,
      url: "http://127.0.0.1:1",
      seed: `${app.url}/seed`,
      workers: 3,
      log: () => {},
      mkdir: () => {},
      hasPlan: () => false,
      // The seed call itself, at the place the suite actually reaches it, without a browser.
      runTest: async (o) => {
        const r = await seedRun({ endpoint: o.seed, identity: identity(), test: o.test, log: () => {} });
        o.onRun({ status: r.problem ? "errored" : "passed", mode: "agent", reason: r.problem || "ok" });
        return r.problem ? 2 : 0;
      },
    });
    assert.equal(app.hits(), 6, "one fixture per test, no more and no fewer");
    assert.ok(app.peak() <= 3, `${app.peak()} seed requests were in flight at once with --workers 3`);
    assert.ok(app.peak() > 1, "precondition: the workers really did overlap, so the cap above means something");
  });
});
