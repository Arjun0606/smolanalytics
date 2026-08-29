// `--share`, ATTACKED — and the only attack that matters is the leak.
//
// research/CROSS_POLLINATION.md §2 is the post-mortem this file exists because of. Replay.io built
// a strictly better artefact and their bug-report phase died anyway, and their own words for why
// are: users "worried replays could contain sensitive data". Not "a replay leaked" — WORRIED. The
// feature was uninstalled by a suspicion. So a single credential on a single share page is not a
// bug we fix next week; it is the end of the feature, and every other property in this file is
// worth less than that one.
//
// HOW THE LEAK HUNT IS RUN HERE, because reading the redaction code proves nothing:
//
//   A DISTINCT SECRET IN EVERY CHANNEL. Thirteen values, each unique and greppable, planted in
//   every place a credential actually reaches this runner: the model key, the project write key
//   and run key, the login password and email, the seed and teardown shared secrets, a GitHub
//   token, a customer's own STRIPE_SECRET_KEY, a token the seed endpoint returned, an Authorization
//   header the app rendered into a debug panel, a session cookie in a storageState blob, a token in
//   the query string of the URL under test, and a password typed into a form.
//
//   THE BYTES ON THE WIRE ARE WHAT IS GREPPED. Not the return of buildBundle — the body of the
//   POST, read off a real HTTP server, after a real Chromium drove a real app under the real
//   binary with a scripted model. If a value appears anywhere in those bytes, including inside a
//   field nobody remembered existed, the grep finds it.
//
// FOUR LEAKS AND ONE VERDICT BUG WERE MEASURED THIS WAY, and each one has its own test below:
//
//   1. A session cookie in a JSON-ESCAPED storageState blob went out in full. The storageState arm
//      needed a literal `"`, and storage state almost always arrives having been JSON-encoded once
//      already, so `\"name\"` never matched `"name"`.
//   2. A step's `detail` was capped at 400 characters BEFORE any scrub ran, so a credential
//      straddling the cap lost its tail and the first half was published unredacted.
//   3. The literal password an author writes into the test SENTENCE travelled verbatim — as the
//      headline, the test name and the sentence, three times on one page.
//   4. An environment credential echoed back in a different case was not redacted at all.
//   5. NOT a leak but the same severity in the other direction: the headline went through the
//      scrub, so SMOLANALYTICS_LOGIN_PASSWORD=Failed produced `[redacted]: "…"` — a share page that
//      could no longer say what happened.
//
// AND THE THREE RULES THAT ARE NOT ABOUT SECRETS, each proved against the real binary:
// opt-in (a run without the flag makes no share request at all, and posts a byte-identical run to
// a project), verdict integrity (a plane that hangs, 500s, answers garbage or refuses the
// connection changes no verdict, no reason and no exit code), and the five statuses (never blurred,
// never rounded, even when a credential spells one of them).

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  agentSteps,
  buildBundle,
  credentialLiteral,
  envSecrets,
  headline,
  overallVerdict,
  pickPageText,
  postBundle,
  publishShare,
  replaySteps,
  scrub,
  scrubDeep,
  shareLines,
  stripAnsi,
  SHARE_PATH,
} from "../lib/share.mjs";
import { suiteCmd } from "../lib/suite.mjs";
import { testCmd } from "../lib/test.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics test installs it on first use)" };

const here = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(here, "..", "bin", "smolanalytics.mjs");
const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-share-"));
/** Colour out of a transcript. Built with new RegExp so no ESC byte is ever typed into source. */
const ANSI_OUT = new RegExp("\\u001b\\[[0-9;]*m", "g");
const plain = (s) => String(s).replace(ANSI_OUT, "");
/** An ESC byte, built rather than typed: a literal control character in a .mjs file is not source. */
const ESC = String.fromCharCode(27);

// ---- the thirteen planted secrets -----------------------------------------------------------------
//
// Every one is distinct, is at least eight characters, and contains a marker no page text, no
// English word and no other fixture in this repo produces. A grep for any of them in the POSTed
// bytes is unambiguous.

const S = {
  model: "sk-ant-LEAKMODELKEY0000000000",
  write: "sa_LEAKWRITEKEY000000000",
  runkey: "LEAKRUNKEY00000000",
  loginpw: "LEAKLOGINPW0000!",
  loginem: "leak-login-email@example.test",
  seedsec: "LEAKSEEDSECRET00000",
  teardown: "LEAKTEARDOWNSECRET0",
  ghtok: "ghp_LEAKGITHUBTOKEN000000000000000",
  stripe: "LEAKSTRIPEKEY000000",
  seedtok: "LEAKSEEDTOKEN0000000",
  appbear: "LEAKAPPBEARER00000000",
  cookie: "LEAKSESSIONCOOKIE000",
  urltok: "LEAKURLTOKEN00000000",
  typedpw: "LEAKTYPEDPW000000",
};

/** The environment a leaky run happens in. Deliberately every named var AND a customer's own. */
const leakEnv = (extra = {}) => ({
  ANTHROPIC_API_KEY: S.model,
  SMOLANALYTICS_WRITE_KEY: S.write,
  SMOLANALYTICS_RUN_KEY: S.runkey,
  SMOLANALYTICS_LOGIN_PASSWORD: S.loginpw,
  SMOLANALYTICS_LOGIN_EMAIL: S.loginem,
  SMOLANALYTICS_SEED_SECRET: S.seedsec,
  SMOLANALYTICS_TEARDOWN_SECRET: S.teardown,
  GITHUB_TOKEN: S.ghtok,
  STRIPE_SECRET_KEY: S.stripe,
  ...extra,
});

/**
 * No PREFIX of a credential appears either, which is the assertion a cap-straddle needs.
 *
 * MEASURED as a defect in this very file: the first version asserted `!includes(secret.slice(0, 10))`
 * against a fixture that padded the value to land 8 characters before the cap, so the string it
 * looked for could not be there whatever the code did. Mutating the cap back to its broken order
 * left the suite green. Walking every prefix from six characters up cannot be gamed by padding.
 */
function assertNoPrefixOf(bytes, secret, { from = 6 } = {}) {
  for (let n = from; n < secret.length; n++) {
    const head = secret.slice(0, n);
    assert.ok(!bytes.includes(head), `the first ${n} characters of a live credential were published: ${head}`);
  }
  assert.ok(!bytes.includes(secret), "the whole credential was published");
}

/** Every planted value that is not in this bundle by construction, and must never be in its bytes. */
function assertNoSecrets(bytes, { except = [] } = {}) {
  const hits = Object.entries(S)
    .filter(([k]) => !except.includes(k))
    .filter(([, v]) => bytes.includes(v))
    .map(([k, v]) => `${k} (${v})`);
  assert.deepEqual(hits, [], `these credentials were published on a page anyone with the link can open: ${hits.join(", ")}`);
}

// ---- a real application, a real control plane -------------------------------------------------------

/**
 * The customer's app: a login form, and a debug panel that renders every credential a staging box
 * ever renders. closeAllConnections() BEFORE close(), always — a keep-alive socket Chromium is
 * holding open leaves server.close() waiting forever and wedges the whole file.
 */
function startApp() {
  const server = createServer((req, res) => {
    if (req.url.startsWith("/seed")) {
      let body = "";
      req.on("data", (d) => (body += d));
      req.on("end", () => {
        res.writeHead(200, { "content-type": "application/json" });
        res.end(JSON.stringify({ orderId: S.seedtok }));
      });
      return;
    }
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(`<!doctype html><title>Shop</title>
<h1>Order ${S.seedtok}</h1>
<form><label>Password <input type="password" aria-label="Password"></label><button id="go">Sign in</button></form>
<pre id="debug">Authorization: Bearer ${S.appbear}
Set-Cookie: session=${S.cookie}; Path=/
storageState: {"cookies":[{"name":"session","value":"${S.cookie}","domain":"x"}]}
STRIPE_SECRET_KEY=${S.stripe}
model key ${S.model}
github ${S.ghtok}
project key ${S.write}
run key ${S.runkey}
login ${S.loginem} / ${S.loginpw}
seed secret ${S.seedsec}
teardown secret ${S.teardown}</pre>
<div id="out"></div>
<script>document.getElementById("go").onclick=(e)=>{e.preventDefault();document.getElementById("out").textContent="Something went wrong";}</script>`);
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r({
    url: `http://127.0.0.1:${server.address().port}/`,
    close: () => new Promise((z) => { server.closeAllConnections(); server.close(() => z()); }),
  })));
}

/**
 * The control plane. Records EVERY request it receives — path, headers and raw bytes — so "no
 * network call without the flag" is measured at the socket rather than read out of the source.
 *
 * `mode` decides what it answers, which is how a 500, a garbage body and a hang are all real HTTP.
 */
function startPlane({ mode = "ok" } = {}) {
  const seen = [];
  const server = createServer((req, res) => {
    const chunks = [];
    req.on("data", (d) => chunks.push(d));
    req.on("end", () => {
      seen.push({ path: req.url, headers: req.headers, body: Buffer.concat(chunks) });
      if (mode === "hang") return; // deliberately never answered
      if (mode === "500") {
        res.writeHead(500, { "content-type": "text/plain" });
        res.end("the database is on fire");
        return;
      }
      if (mode === "garbage") {
        res.writeHead(200, { "content-type": "application/json" });
        res.end("<!doctype html><h1>a login page, not JSON</h1>");
        return;
      }
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ id: "abc123", url: "https://smolanalytics.com/s/abc123" }));
    });
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r({
    url: `http://127.0.0.1:${server.address().port}`,
    seen,
    shares: () => seen.filter((x) => x.path === SHARE_PATH),
    close: () => new Promise((z) => { server.closeAllConnections(); server.close(() => z()); }),
  })));
}

/**
 * The scripted model. Three turns: fill the password box, click Sign in, then report a failure whose
 * prose quotes every credential the debug panel showed it — which is exactly what a model does when
 * it is asked to describe what it saw.
 */
function preloadOf(dir) {
  const why =
    `The page showed Something went wrong. Its debug panel said Authorization: Bearer ${S.appbear} and ` +
    `Set-Cookie: session=${S.cookie}, and it printed the storageState {"cookies":[{"name":"session","value":"${S.cookie}"}]}, ` +
    `STRIPE_SECRET_KEY=${S.stripe}, ${S.model}, ${S.ghtok}, ${S.write}, ${S.runkey}, ${S.loginem}, ${S.loginpw}, ` +
    `${S.seedsec}, ${S.teardown}, order ${S.seedtok}, typed ${S.typedpw}.`;
  const src = `
const real = globalThis.fetch;
const REF = /(e\\d+) (\\w+) "([^"]*)"/g;
globalThis.fetch = async (t, init = {}) => {
  if (!String(t).includes("api.anthropic.com")) return real(t, init);
  const body = JSON.parse(init.body);
  const seen = body.messages.map((m) => (typeof m.content === "string" ? m.content : JSON.stringify(m.content))).join("\\n");
  const ref = (kind, name) => {
    for (const m of seen.matchAll(REF)) if (m[2] === kind && m[3] === name) return m[1];
    throw new Error("no ref for " + kind + " " + name);
  };
  let block;
  if (body.messages.length === 1) {
    block = { type: "tool_use", id: "t1", name: "fill", input: { ref: ref("textbox", "Password"), text: ${JSON.stringify(S.typedpw)}, why: "sign in" } };
  } else if (body.messages.length === 3) {
    block = { type: "tool_use", id: "t2", name: "click", input: { ref: ref("button", "Sign in"), why: "submit" } };
  } else {
    block = { type: "tool_use", id: "t3", name: "finish", input: { passed: false, proof: "", why: ${JSON.stringify(why)} } };
  }
  return { ok: true, status: 200, text: async () => "", json: async () => ({ stop_reason: "tool_use", content: [block] }) };
};
`;
  const p = path.join(dir, "scripted-model.mjs");
  writeFileSync(p, src);
  return new URL(`file://${path.resolve(p)}`).href;
}

/** The child, run without blocking the event loop the fixture servers are served from. */
function runCli(args, env, { cwd } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [BIN, ...args], { cwd, env });
    let out = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (out += d));
    child.on("error", reject);
    child.on("close", (status) => resolve({ status, out: plain(out) }));
  });
}

/**
 * One leaky run of the real binary. Returns the transcript, the exit code and the plane's requests.
 *
 * `share` off is the same command minus one flag, which is what makes the opt-in comparison honest.
 */
async function leakyRun({ app, plane, share = true, extraArgs = [], extraEnv = {} }) {
  const dir = scratch();
  const env = {
    ...process.env,
    ...leakEnv(),
    SMOLANALYTICS_URL: plane.url,
    NODE_OPTIONS: `--import ${preloadOf(dir)}`,
    ...extraEnv,
  };
  delete env.SMOLANALYTICS_KEY;
  delete env.SMOLANALYTICS_PROJECT;
  delete env.GH_TOKEN;
  if (extraEnv.SMOLANALYTICS_PROJECT) env.SMOLANALYTICS_PROJECT = extraEnv.SMOLANALYTICS_PROJECT;
  const args = [
    "test",
    "--url", `${app.url}?access_token=${S.urltok}`,
    "--test", `open order {{orderId}} and sign in with the password ${S.typedpw}`,
    "--seed", `${app.url}seed`,
    "--evidence-dir", path.join(dir, "evidence"),
    "--plan", path.join(dir, "plan.json"),
    "--retries", "0", "--yes",
    ...(share ? ["--share"] : []),
    ...extraArgs,
  ];
  const r = await runCli(args, env, { cwd: dir });
  return { ...r, dir };
}

// ---- 1. NOTHING SECRET TRAVELS ---------------------------------------------------------------------

describe("nothing secret travels: thirteen credentials, one real run, the bytes on the wire", noBrowser, () => {
  test("not one planted credential appears anywhere in the POSTed bundle", { timeout: 120_000 }, async () => {
    const app = await startApp();
    const plane = await startPlane();
    try {
      const r = await leakyRun({ app, plane });
      assert.equal(r.status, 1, `the fixture must produce a real FAILED verdict, or this greps an empty bundle:\n${r.out.slice(-2000)}`);
      const posts = plane.shares();
      assert.equal(posts.length, 1, `expected exactly one share POST, got ${posts.length}`);

      const bytes = posts[0].body.toString("utf8");
      // The fixture has to have actually carried the secrets into the run, or a clean grep is
      // theatre: the app rendered them, the model quoted them, the agent typed one of them.
      assert.ok(bytes.length > 2_000, `the bundle is ${bytes.length} bytes — too small to have contained a run`);
      assertNoSecrets(bytes);

      const b = JSON.parse(bytes);
      // Each one again by name, so a failure says WHICH channel leaked rather than "something did".
      const t = b.tests[0];
      assert.ok(!t.reason.includes(S.appbear), "the Authorization header the app rendered survived into the reason");
      assert.ok(!t.reason.includes(S.cookie), "the session cookie survived into the reason");
      assert.ok(!t.sentence.includes(S.typedpw), "the password the author wrote into the sentence survived");
      assert.ok(!b.headline.includes(S.typedpw), "the password survived into the headline");
      assert.ok(!t.name.includes(S.typedpw), "the password survived into the test name");
      assert.ok(!b.url.includes(S.urltok), "the token in the URL's query string survived");
      assert.ok(!b.pageText.text.includes(S.cookie), "the session cookie survived into the captured page text");
      assert.ok(!b.pageText.text.includes(S.stripe), "the customer's own STRIPE_SECRET_KEY survived into the page text");
      assert.ok(!JSON.stringify(t.steps).includes(S.typedpw), "the password survived into a fill step's label");
      // And the run is still legible — a bundle redacted into uselessness would pass every grep.
      assert.equal(t.status, "failed");
      assert.match(b.headline, /^Failed: /, `the headline stopped stating the verdict: ${b.headline}`);
      assert.match(b.pageText.text, /Something went wrong/, "the page text was over-redacted into nothing");
      assert.ok(b.screenshot && b.screenshot.base64.length > 1_000, "the failure screenshot did not travel");
    } finally {
      await plane.close();
      await app.close();
    }
  });

  test("the --suite path posts one clean bundle for the whole run", { timeout: 180_000 }, async () => {
    // suiteCmd assembles the bundle from its own results and, unlike testCmd, threads NO seeded
    // masking pairs into buildBundle. That asymmetry is deliberate — the seeded value is already
    // masked upstream, at agentAttempt, at compile, and at captureEvidence — but "deliberate" is
    // worth nothing without a run that would show it if it were wrong. So: a real suite, a real
    // --seed, and the same grep.
    const app = await startApp();
    const plane = await startPlane();
    try {
      const dir = scratch();
      const tests = path.join(dir, "tests");
      mkdirSync(tests, { recursive: true });
      writeFileSync(path.join(tests, "login.md"),
        `# suite\n\n## sign in\n\nOpen order {{orderId}} and sign in with the password ${S.typedpw}.\n`);
      const env = {
        ...process.env,
        ...leakEnv(),
        SMOLANALYTICS_URL: plane.url,
        NODE_OPTIONS: `--import ${preloadOf(dir)}`,
      };
      delete env.SMOLANALYTICS_KEY;
      delete env.SMOLANALYTICS_PROJECT;
      delete env.GH_TOKEN;
      const r = await runCli([
        "test", "--suite", tests,
        "--url", `${app.url}?access_token=${S.urltok}`,
        "--seed", `${app.url}seed`,
        "--plans", path.join(dir, "recordings"),
        "--evidence-dir", path.join(dir, "evidence"),
        "--retries", "0", "--yes", "--share",
      ], env, { cwd: dir });

      assert.equal(r.status, 1, `the suite must reach a real FAILED verdict:\n${r.out.slice(-2000)}`);
      const posts = plane.shares();
      assert.equal(posts.length, 1, "one link for the whole suite, not one per test");
      const bytes = posts[0].body.toString("utf8");
      assertNoSecrets(bytes);
      const b = JSON.parse(bytes);
      assert.equal(b.summary.total, 1);
      assert.equal(b.tests[0].status, "failed");
      assert.equal(b.tests[0].file, path.join(tests, "login.md"));
      assert.match(b.headline, /^Failed: /);
    } finally {
      await plane.close();
      await app.close();
    }
  });

  test("the terminal says the screenshot cannot be masked, because it cannot", { timeout: 120_000 }, async () => {
    const app = await startApp();
    const plane = await startPlane();
    try {
      const r = await leakyRun({ app, plane });
      assert.match(r.out, /cannot be masked/, `a run that attached a picture of the app did not say so:\n${r.out.slice(-1200)}`);
      assert.match(r.out, /https:\/\/smolanalytics\.com\/s\/abc123/, "the link the plane handed back was not printed");
    } finally {
      await plane.close();
      await app.close();
    }
  });
});

// ---- 2. THE ESCAPED STORAGE STATE ------------------------------------------------------------------

describe("a session cookie survives being JSON-encoded on its way into the bundle", () => {
  // MEASURED, with the real binary: a runner error that quoted the conversation carried
  //   \"name\":\"session\",\"value\":\"LEAKSESSIONCOOKIE000\"
  // in full, because the storageState arm needs a literal `"` and every quote here is escaped.
  const raw = '{"cookies":[{"name":"session","value":"LEAKSESSIONCOOKIE000","domain":"x"}]}';
  const escaped = JSON.stringify(raw).slice(1, -1);

  test("the plain form is redacted", () => {
    assert.ok(!scrub(raw, { env: {} }).includes(S.cookie), scrub(raw, { env: {} }));
  });

  test("the JSON-ESCAPED form is redacted too", () => {
    assert.ok(escaped.includes('\\"session\\"'), "the fixture is not actually escaped, so it cannot fail");
    const out = scrub(escaped, { env: {} });
    assert.ok(!out.includes(S.cookie), `a live session cookie went out in full: ${out}`);
  });

  test("a debug panel that prints it without any JSON at all is redacted", () => {
    const out = scrub("session value LEAKSESSIONCOOKIE000 expires tomorrow", { env: {} });
    assert.ok(!out.includes(S.cookie), out);
  });

  test("a credential in a query string is redacted by parameter NAME, and the name is kept", () => {
    // The documented case, from share.mjs's own comment: --url with Vercel's protection bypass.
    // `?access_token=` would also be caught by the bare-assignment arm, so this asserts on names
    // that ONLY this arm can see — otherwise removing the arm changes nothing and no test notices.
    for (const [q, keep] of [
      ["?x-vercel-protection-bypass=SECRETBYPASS123", "x-vercel-protection-bypass"],
      ["?key=SECRETKEYVALUE99", "key"],
      ["#signature=SECRETSIGVALUE1", "signature"],
      ["?sig=SECRETSIGVALUE1", "sig"],
    ]) {
      const out = scrub(`https://preview.example.app/${q}`, { env: {} });
      assert.match(out, new RegExp(`${keep.replace(/[-]/g, "\\-")}=\\[redacted\\]`), `not redacted: ${q} -> ${out}`);
    }
    // And an innocent parameter keeps its value, or every share page loses its own URL.
    assert.equal(scrub("https://x/?author=ada&monkey=7&design=flat", { env: {} }), "https://x/?author=ada&monkey=7&design=flat");
  });

  test("`value` on its own is never touched, so a page that says one is still readable", () => {
    const prose = 'the cart total value 4200 is shown, and the coupon value is "SPRING"';
    assert.equal(scrub(prose, { env: {} }), prose);
  });
});

// ---- 3. A CAP MAY NEVER CUT A CREDENTIAL IN HALF ---------------------------------------------------

describe("every cap lands after the scrub, never before it", () => {
  // The failure this prevents: a value straddling the cap loses its tail, `redact` is a byte-for-
  // byte substring match so it no longer recognises the head, and the FIRST HALF of a live key is
  // published as if it were safe. pickPageText's comment names it; agentSteps had the same hole.
  const env = { SMOLANALYTICS_SEED_SECRET: S.seedsec };

  test("a credential straddling a step detail's 400-character cap is redacted, not halved", () => {
    // Swept across the cap rather than pinned to one offset: wherever the boundary lands inside the
    // value, no prefix of it may survive.
    for (let pad = 380; pad <= 400; pad++) {
      const steps = agentSteps([{ n: 1, label: "click", detail: `${"z".repeat(pad)}${S.seedsec} and then some more text`, ms: 1 }]);
      const b = buildBundle({ tests: [{ name: "n", sentence: "s", status: "failed", steps }], env });
      assertNoPrefixOf(JSON.stringify(b), S.seedsec);
      assert.ok(b.tests[0].steps[0].detail.length <= 420, "the cap stopped being applied at all");
    }
  });

  test("the same for a stale replay's failing-step detail", () => {
    for (let pad = 380; pad <= 400; pad++) {
      const steps = replaySteps([{ kind: "click", role: "button", name: "Go" }], {
        failedAt: 0,
        detail: `${"z".repeat(pad)}${S.seedsec} trailing`,
      });
      const b = buildBundle({ tests: [{ name: "n", sentence: "s", status: "stale", steps }], env });
      assertNoPrefixOf(JSON.stringify(b), S.seedsec);
    }
  });

  test("a credential straddling the page text's cap is redacted, not halved", () => {
    const dir = scratch();
    for (let pad = 3_985; pad <= 4_000; pad++) {
      const txt = path.join(dir, `failure-${pad}.txt`);
      writeFileSync(txt, `${"z".repeat(pad)}${S.seedsec} tail`);
      const got = pickPageText(
        [{ name: "n", status: "failed", evidence: { txt } }],
        { test: "n" },
        { scrubText: (x) => scrub(x, { env }) },
      );
      assertNoPrefixOf(got.text, S.seedsec);
    }
  });

  test("a credential straddling the reason's cap is redacted, not halved", () => {
    for (let pad = 3_985; pad <= 4_000; pad++) {
      const b = buildBundle({
        tests: [{ name: "n", sentence: "s", status: "failed", reason: `${"z".repeat(pad)}${S.seedsec} tail` }],
        env,
      });
      assertNoPrefixOf(JSON.stringify(b), S.seedsec);
    }
  });
});

// ---- 4. THE LITERAL AN AUTHOR TYPES INTO THE SENTENCE ---------------------------------------------

describe("a password written into the test sentence does not become the headline", () => {
  test("it is redacted in the sentence, the name and the headline", () => {
    const b = buildBundle({
      tests: [{ name: "sign in with the password Hunter2-xyz", sentence: "sign in with the password Hunter2-xyz", status: "failed" }],
      env: {},
    });
    const bytes = JSON.stringify(b);
    assert.ok(!bytes.includes("Hunter2-xyz"), `the author's literal password was published: ${b.headline}`);
    assert.match(b.headline, /^Failed: /, "the verdict word was lost along with it");
  });

  test("ordinary prose after the word survives, because the sentence IS the page", () => {
    // The asymmetry runs the other way here than everywhere else in the file: this string is the
    // artefact, and redacting it into uselessness costs more than one `[redacted]`.
    for (const s of [
      "sign in with the password from the vault",
      "the password manager should open",
      "check the password requirements are shown",
      "the secret handshake page loads",
      "confirm the token expires after an hour",
      "the api key field is disabled",
    ]) {
      assert.equal(scrub(s, { env: {} }), s, `ordinary English was redacted: ${s}`);
    }
  });

  test("a credential-shaped literal is caught in every one of its spellings", () => {
    for (const s of ["password Hunter2!", "password is Hunter2!", "passphrase = Hunter2!", "api key: sk_live_9x8y7z", 'password "Hunter2!"']) {
      assert.ok(!scrub(s, { env: {} }).includes("Hunter2!") || !scrub(s, { env: {} }).includes("sk_live_9x8y7z"), s);
      assert.match(scrub(s, { env: {} }), /\[redacted\]/, `not redacted: ${s}`);
    }
  });

  test("credentialLiteral judges a word by shape, and a placeholder is never one", () => {
    assert.equal(credentialLiteral("{{password}}"), false, "a placeholder is the OPPOSITE of a leak — it is the fix");
    assert.equal(credentialLiteral("[redacted]"), false, "redacting a redaction is how [[redacted]] ships");
    assert.equal(credentialLiteral("manager"), false, "a plain word after \"password\" is prose, not a credential");
    assert.equal(credentialLiteral("Requirements"), false);
    assert.equal(credentialLiteral("handshake"), false);
    assert.equal(credentialLiteral("vault"), false, "under six characters is prose");
    assert.equal(credentialLiteral("Hunter2"), true);
    assert.equal(credentialLiteral("sk_live_abc"), true);
    assert.equal(credentialLiteral("correctHorse"), true);
  });
});

// ---- 5. CASE, ENCODING, AND THE SHAPES A VALUE COMES BACK IN --------------------------------------

describe("a credential is still a credential in another case or another encoding", () => {
  // The password deliberately contains `+`, `/` and `=` — every base64 key ever issued does, and a
  // value that percent-encodes to ITSELF makes this whole describe unable to fail. Measured: the
  // first version used "LeakPassword123", removing the urlForms expansion changed nothing, and the
  // suite stayed green over a hole.
  const env = { SMOLANALYTICS_LOGIN_EMAIL: "Leak-Login@Example.Test", SMOLANALYTICS_LOGIN_PASSWORD: "Leak+Pass/word=123" };

  test("lower-cased — which is what an app does to an email before it echoes it back", () => {
    const out = scrub("signed in as leak-login@example.test", { env });
    assert.ok(!out.toLowerCase().includes("leak-login@example.test"), out);
  });

  test("upper-cased", () => {
    const out = scrub("the header said LEAK+PASS/WORD=123", { env });
    assert.ok(!/LEAK\+PASS\/WORD=123/i.test(out), out);
  });

  test("percent-encoded, because a browser writes it that way into a query string", () => {
    const encoded = encodeURIComponent("Leak+Pass/word=123");
    assert.ok(encoded !== "Leak+Pass/word=123", "the fixture encodes to itself, so this test cannot fail");
    const out = scrub(`?next=${encoded}&nothing=here`, { env });
    assert.ok(!out.includes(encoded), `the percent-encoded form of a credential was published: ${out}`);
  });

  test("exact still works, and case-folding did not eat the rest of the string", () => {
    const out = scrub("before Leak+Pass/word=123 after", { env });
    assert.equal(out, "before [redacted] after");
  });

  test("a secret whose case-folding changes its length is left to the exact pass, never mis-sliced", () => {
    // "İ".toLowerCase() is two code units. Indexing the original by offsets found in the folded
    // string would cut the bundle in the wrong place; the guard skips the folded pass instead.
    const odd = { SMOLANALYTICS_LOGIN_PASSWORD: "İSTANBUL-KEY" };
    assert.equal(scrub("x İSTANBUL-KEY y", { env: odd }), "x [redacted] y");
    assert.equal(scrub("nothing to see here", { env: odd }), "nothing to see here");
  });
});

// ---- 6. OVER-MASKING MAY DEGRADE PROSE, NEVER A VERDICT --------------------------------------------

describe("a password that is an ordinary word costs a reader words, never the verdict", () => {
  const tests = [
    { name: "a", file: "tests/a.md", sentence: "the cart shows one line", status: "failed", reason: "The cart said Add to cart.", proof: "2 items in your cart", durationMs: 5 },
    { name: "b", file: "tests/b.md", sentence: "checkout works", status: "passed", durationMs: 7 },
  ];

  test("SMOLANALYTICS_LOGIN_PASSWORD=cart redacts prose and leaves every verdict field intact", () => {
    const b = buildBundle({ tests, url: "https://shop.example.com/", exitCode: 1, env: { SMOLANALYTICS_LOGIN_PASSWORD: "cart" } });
    assert.match(b.tests[0].reason, /\[redacted\]/, "the fixture stopped over-masking, so this proves nothing");
    assert.equal(b.verdict, "failed");
    assert.equal(b.exitCode, 1);
    assert.equal(b.tests[0].status, "failed");
    assert.equal(b.tests[1].status, "passed");
    assert.deepEqual([b.summary.failed, b.summary.passed, b.summary.total], [1, 1, 2]);
    assert.match(b.headline, /1 passed, 1 failed/, `the headline lost its counts: ${b.headline}`);
  });

  for (const word of ["failed", "Failed", "passed", "stale", "flaky", "errored", "status", "verdict", "exitCode", "mode", "agent", "chromium"]) {
    test(`SMOLANALYTICS_LOGIN_PASSWORD=${word} cannot blur the run's verdict`, () => {
      const b = buildBundle({ tests, url: "https://shop.example.com/", exitCode: 1, engine: "chromium", env: { SMOLANALYTICS_LOGIN_PASSWORD: word } });
      assert.equal(b.verdict, "failed");
      assert.equal(b.exitCode, 1);
      assert.equal(b.engine, "chromium");
      assert.deepEqual(b.tests.map((t) => t.status), ["failed", "passed"]);
      assert.deepEqual([b.summary.failed, b.summary.passed], [1, 1]);
      // The headline is the line a person reads first, and it is the one field that MIXES the
      // closed vocabulary with a customer string. It goes through neither half of the scrub.
      assert.ok(!b.headline.includes("[redacted]") || !/failed|passed/i.test(word),
        `the headline can no longer say what happened: ${b.headline}`);
      assert.match(b.headline, /1 passed, 1 failed/, `the headline lost a status word to a password: ${b.headline}`);
    });
  }

  test("a single-test headline keeps its status word too", () => {
    const b = buildBundle({ tests: [tests[0]], env: { SMOLANALYTICS_LOGIN_PASSWORD: "Failed" } });
    assert.match(b.headline, /^Failed: /, `the one line at the top of the page: ${b.headline}`);
  });
});

// ---- 7. THE FIVE STATUSES ------------------------------------------------------------------------

describe("the five statuses are never blurred", () => {
  test("the worst present status wins, and nothing is rounded", () => {
    assert.equal(overallVerdict(["passed", "flaky"]), "flaky", "flaky must never round up to a pass");
    assert.equal(overallVerdict(["passed", "stale"]), "stale");
    assert.equal(overallVerdict(["stale", "errored"]), "errored");
    assert.equal(overallVerdict(["errored", "failed"]), "failed");
    assert.equal(overallVerdict(["passed"]), "passed");
    assert.equal(overallVerdict([]), "errored", "an empty run is not a pass");
  });

  test("each test keeps its own status verbatim and the counts stay separate", () => {
    const five = ["passed", "failed", "flaky", "stale", "errored"].map((s, i) => ({ name: `t${i}`, sentence: `s${i}`, status: s }));
    const b = buildBundle({ tests: five, env: {} });
    assert.deepEqual(b.tests.map((t) => t.status), ["passed", "failed", "flaky", "stale", "errored"]);
    assert.deepEqual(
      [b.summary.passed, b.summary.failed, b.summary.flaky, b.summary.stale, b.summary.errored],
      [1, 1, 1, 1, 1],
    );
    assert.equal(b.verdict, "failed");
  });

  test("the headline never invents a claim a stale or a flaky would make false", () => {
    const h = headline([{ status: "flaky", sentence: "x" }, { status: "passed", sentence: "y" }], "https://a.example.com/");
    assert.ok(!/all good|everything works|all passed/i.test(h), h);
    assert.match(h, /1 passed, 1 flaky/);
  });
});

// ---- 8. SHARING CANNOT CHANGE A VERDICT OR AN EXIT CODE -------------------------------------------

describe("a control plane that misbehaves changes nothing about the run", noBrowser, () => {
  for (const mode of ["500", "garbage"]) {
    test(`a plane that answers ${mode} leaves the verdict, the reason and the exit code alone`, { timeout: 120_000 }, async () => {
      const app = await startApp();
      const good = await startPlane();
      const bad = await startPlane({ mode });
      try {
        const ok = await leakyRun({ app, plane: good });
        const broken = await leakyRun({ app, plane: bad });
        assert.equal(ok.status, 1);
        assert.equal(broken.status, ok.status, `the exit code moved from ${ok.status} to ${broken.status} because a POST failed`);
        assert.match(broken.out, /not shared/, `the reader was not told why there is no link:\n${broken.out.slice(-1200)}`);
        assert.match(broken.out, /verdict above still stands/);
        assert.ok(!/smolanalytics\.com\/s\//.test(broken.out), "a link was printed for a share that never landed");
        // The verdict text itself, compared between the two runs.
        const verdictOf = (out) => (out.match(/\nFAIL[^\n]*\n([\s\S]*?)\n\n/) || [])[1] || "";
        assert.equal(verdictOf(broken.out), verdictOf(ok.out), "the failure's own prose changed because the share failed");
      } finally {
        await bad.close();
        await good.close();
        await app.close();
      }
    });
  }

  test("a plane that refuses the connection outright is the same", { timeout: 120_000 }, async () => {
    const app = await startApp();
    const dead = await startPlane();
    const url = dead.url;
    await dead.close(); // nothing is listening on that port any more
    try {
      const r = await leakyRun({ app, plane: { url, shares: () => [] } });
      assert.equal(r.status, 1, `a refused share changed the exit code:\n${r.out.slice(-1500)}`);
      assert.match(r.out, /not shared: could not reach/);
      assert.match(r.out, /verdict above still stands/);
    } finally {
      await app.close();
    }
  });

  test("a plane that never answers gives up on a timer, against a real socket", async () => {
    const plane = await startPlane({ mode: "hang" });
    try {
      const started = Date.now();
      const r = await postBundle({ bundle: { kind: "x" }, env: { SMOLANALYTICS_URL: plane.url }, timeoutMs: 400 });
      assert.equal(r.ok, false);
      assert.match(r.problem, /no answer within/);
      assert.ok(Date.now() - started < 10_000, "the abort did not fire");
    } finally {
      await plane.close();
    }
  });

  test("postBundle never throws, whatever comes back", async () => {
    const answers = {
      "a response that is a string": async () => "not a response",
      "a fetch that throws": async () => { throw new Error("boom"); },
      "a body that is not JSON": async () => ({ ok: true, status: 200, json: async () => { throw new Error("nope"); } }),
      "JSON that is a number": async () => ({ ok: true, status: 200, json: async () => 7 }),
      "a 200 with no id and no url": async () => ({ ok: true, status: 200, json: async () => ({}) }),
    };
    for (const [name, fetchImpl] of Object.entries(answers)) {
      const r = await postBundle({ bundle: { kind: "x" }, env: {}, fetchImpl });
      assert.equal(r.ok, false, name);
      assert.ok(r.problem, `${name}: a failure with no reason is a link a person hunts for in the scrollback`);
      assert.equal(r.url, "", name);
    }
  });

  test("a bundle that cannot even be serialised is a message, not a throw", async () => {
    const circular = { kind: "x" };
    circular.self = circular;
    const r = await postBundle({ bundle: circular, env: {}, fetchImpl: async () => ({ ok: true, json: async () => ({ id: "x" }) }) });
    assert.equal(r.ok, false);
    assert.match(r.problem, /circular/i);
  });

  test("publishShare never throws, not even when the log it was handed does", async () => {
    // testCmd calls this from a `finally` and suiteCmd as its last await: a rejection there does not
    // lose a link, it replaces a real exit 1 with a 2.
    const r = await publishShare({
      tests: [{ name: "n", sentence: "s", status: "failed" }],
      env: {},
      fetchImpl: async () => ({ ok: true, status: 200, json: async () => ({ id: "z" }) }),
      log: () => { throw new Error("the terminal exploded"); },
    });
    assert.equal(r, null);
  });

  test("suiteCmd's exit code survives a publisher that throws", async () => {
    const lines = [];
    const code = await suiteCmd({
      suite: "",
      url: "http://127.0.0.1:1/",
      test: "a sentence",
      plans: scratch(),
      share: true,
      yes: true,
      log: (l) => lines.push(plain(String(l))),
      env: {},
      runSuiteImpl: async () => [{ id: "t", name: "t", file: "t.md", test: "a sentence", status: "failed", mode: "agent", reason: "it broke", ms: 1, suspects: [], layout: [] }],
      publishShareImpl: async () => { throw new Error("the publisher exploded"); },
    });
    assert.equal(code, 1, "a throwing publisher turned a real failure into our own error");
    assert.ok(lines.some((l) => /not shared/.test(l)), `the reader was not told:\n${lines.join("\n")}`);
  });

  test("testCmd's exit code survives a publisher that throws", { ...noBrowser, timeout: 120_000 }, async () => {
    // testCmd calls the publisher from a `finally`, where a rejection does not merely lose a link:
    // it replaces the `return code` the try had already decided with a rejected promise, and
    // bin/smolanalytics.mjs turns that into exit 2. The run below has no model key, so it errors on
    // its own terms and its own exit code is 2 — asserted against a run with a publisher that works,
    // so a mutation that broke both would not pass by making them agree on the wrong number.
    const app = await startApp();
    try {
      const run = (publishShareImpl) => testCmd({
        url: app.url,
        test: "sign in and check the page",
        yes: true,
        share: true,
        env: { SMOLANALYTICS_URL: "http://127.0.0.1:1" },
        log: () => {},
        publishShareImpl,
      });
      const quiet = await run(async () => null);
      const lines = [];
      const loud = await testCmd({
        url: app.url,
        test: "sign in and check the page",
        yes: true,
        share: true,
        env: { SMOLANALYTICS_URL: "http://127.0.0.1:1" },
        log: (l) => lines.push(plain(String(l))),
        publishShareImpl: async () => { throw new Error("the publisher exploded"); },
      });
      assert.equal(quiet, 2, "the fixture stopped reaching a verdict of its own");
      assert.equal(loud, quiet, `a throwing publisher moved the exit code from ${quiet} to ${loud}`);
      assert.ok(lines.some((l) => /not shared/.test(l)), `the reader was not told:\n${lines.join("\n")}`);
    } finally {
      await app.close();
    }
  });

  test("shareLines says the verdict stands, and prints the URL bare on its own line", () => {
    const ok = shareLines({ ok: true, url: "https://smolanalytics.com/s/x" });
    assert.ok(ok.includes("https://smolanalytics.com/s/x"), "the URL must be its own line so a double-click selects it");
    const bad = shareLines({ ok: false, problem: "it 500'd" });
    assert.equal(bad.length, 1);
    assert.match(bad[0], /not shared: it 500'd\. The verdict above still stands\./);
  });
});

// ---- 9. OPT-IN -------------------------------------------------------------------------------------

describe("a run without --share does not talk to the control plane", noBrowser, () => {
  test("no project, no key, no flag: not one request reaches the plane", { timeout: 120_000 }, async () => {
    const app = await startApp();
    const plane = await startPlane();
    try {
      const r = await leakyRun({ app, plane, share: false });
      assert.equal(r.status, 1, `the run must still have reached a verdict:\n${r.out.slice(-1500)}`);
      assert.deepEqual(plane.seen.map((x) => x.path), [], "a run nobody asked to share made a network call to the plane");
      assert.ok(!/Shared this run/.test(r.out), "a link was printed for a run that did not ask for one");
    } finally {
      await plane.close();
      await app.close();
    }
  });

  test("with a project configured, --share adds a share POST and changes the run POST by nothing", { timeout: 180_000 }, async () => {
    const app = await startApp();
    const plane = await startPlane();
    try {
      const withEnv = { SMOLANALYTICS_PROJECT: "proj_1" };
      const without = await leakyRun({ app, plane, share: false, extraEnv: withEnv });
      const runsBefore = plane.seen.filter((x) => x.path.includes("/runs"));
      const sharesBefore = plane.shares();
      const with_ = await leakyRun({ app, plane, share: true, extraEnv: withEnv });
      const runsAfter = plane.seen.filter((x) => x.path.includes("/runs"));
      const sharesAfter = plane.shares();

      assert.equal(without.status, with_.status, "the flag changed the exit code");
      assert.equal(runsBefore.length, 1, `the fixture did not post a run without the flag: ${JSON.stringify(plane.seen.map((x) => x.path))}`);
      assert.equal(sharesBefore.length, 0, "a run without --share posted a share anyway");
      assert.equal(runsAfter.length, 2, "the flag changed how many runs were posted");
      assert.equal(sharesAfter.length, 1, "--share did not post a share");

      // BYTE FOR BYTE, once the two stopwatch fields are pinned. This is the claim lib/test.mjs's
      // wireRun comment makes about the field it strips, and it had no test behind it.
      // Only the stopwatch is pinned, and only because two runs of one command already differ in
      // those cells: the per-step `ms`, the top-level duration, and the `468ms` the step label
      // prints. Everything a reader or a receiving half acts on is compared as it is.
      const pin = (buf) => {
        const j = JSON.parse(buf.toString("utf8"));
        return JSON.stringify({ ...j, durationMs: 0, steps: (j.steps || []).map((x) => ({ ...x, ms: 0, do: String(x.do).replace(/\d+ms/g, "Nms") })) });
      };
      assert.equal(pin(runsAfter[1].body), pin(runsBefore[0].body),
        "the JSON posted to a project differs depending on whether a person asked for a link");
      assert.ok(!runsAfter[1].body.toString("utf8").includes('"share"'), "the share record crossed the wire to the runs API");
    } finally {
      await plane.close();
      await app.close();
    }
  });

  test("an anonymous share sends no Authorization header, and an attributed one sends it in the header only", async () => {
    const seen = [];
    const fetchImpl = async (_url, init) => {
      seen.push(init.headers);
      return { ok: true, status: 200, json: async () => ({ id: "x" }) };
    };
    await postBundle({ bundle: { projectId: "" }, env: { SMOLANALYTICS_WRITE_KEY: S.write }, fetchImpl });
    assert.equal(seen[0].authorization, undefined, "a person with no project was made to send a key");
    await postBundle({ bundle: { projectId: "p1" }, env: { SMOLANALYTICS_WRITE_KEY: S.write }, fetchImpl });
    assert.equal(seen[1].authorization, `Bearer ${S.write}`);
    // And the key is in the header, never in the body — buildBundle must not carry it.
    const b = buildBundle({ tests: [{ name: "n", sentence: "s", status: "passed" }], projectId: "p1", env: { SMOLANALYTICS_WRITE_KEY: S.write } });
    assert.ok(!JSON.stringify(b).includes(S.write), "the project key was published inside the bundle");
  });
});

// ---- 10. THE GENERIC WALK, AND THE THINGS THAT GO AROUND IT ---------------------------------------

describe("the scrub walks the whole structure, and the verdict fields go around it", () => {
  test("a field nobody remembered to cover is covered because the walk is generic", () => {
    const out = scrubDeep({ a: { b: [{ somethingAddedNextMonth: `key ${S.model}` }] } }, { env: { ANTHROPIC_API_KEY: S.model } });
    assert.ok(!JSON.stringify(out).includes(S.model));
  });

  test("keys are left alone, so a password that spells a key name cannot delete a field", () => {
    const out = scrubDeep({ status: "failed", verdict: "failed" }, { env: { SMOLANALYTICS_LOGIN_PASSWORD: "status" } });
    assert.deepEqual(Object.keys(out), ["status", "verdict"], "a key was rewritten and the field vanished");
  });

  test("the screenshot's pixels are not walked, because a substitution can only corrupt them", () => {
    const dir = scratch();
    const png = path.join(dir, "failure.png");
    // Not a real PNG, deliberately: what matters is that the bytes come back unchanged.
    const raw = Buffer.from("PNGDATA-cart-cart-cart-cart");
    writeFileSync(png, raw);
    const b = buildBundle({
      tests: [{ name: "n", sentence: "s", status: "failed", evidence: { png } }],
      env: { SMOLANALYTICS_LOGIN_PASSWORD: "cart" },
    });
    assert.equal(Buffer.from(b.screenshot.base64, "base64").toString("utf8"), raw.toString("utf8"),
      "the image was rewritten by a text substitution, which can only ever break it");
  });

  test("an oversized screenshot is dropped and SAID to be dropped", () => {
    const dir = scratch();
    const png = path.join(dir, "big.png");
    writeFileSync(png, Buffer.alloc(1_600_000, 7));
    const b = buildBundle({ tests: [{ name: "n", sentence: "s", status: "failed", evidence: { png } }], env: {} });
    assert.equal(b.screenshot.base64, "");
    assert.match(b.screenshot.note, /over the .*-byte limit/);
  });

  test("a missing evidence file is null and a verdict, never a throw", () => {
    const b = buildBundle({
      tests: [{ name: "n", sentence: "s", status: "failed", evidence: { png: "/nope/failure.png", txt: "/nope/failure.txt" } }],
      exitCode: 1,
      env: {},
    });
    assert.equal(b.screenshot, null);
    assert.equal(b.pageText, null);
    assert.equal(b.verdict, "failed");
    assert.equal(b.exitCode, 1);
  });
});

// ---- 11. THE BYTES THEMSELVES --------------------------------------------------------------------

describe("what reaches the wire is text a page can render", () => {
  test("ANSI escape sequences from a step label never reach the bundle", () => {
    const label = `fill "Password" = "x"${ESC}[2m 468ms${ESC}[0m`;
    const b = buildBundle({
      tests: [{ name: "n", sentence: "s", status: "failed", steps: agentSteps([{ n: 1, label, ms: 468 }]) }],
      env: {},
    });
    const bytes = JSON.stringify(b);
    assert.ok(!bytes.includes(ESC), "raw control bytes were POSTed inside a JSON body");
    assert.ok(!bytes.includes("[2m"), "the escape sequence's payload survived as visible garbage");
    assert.match(b.tests[0].steps[0].do, /fill "Password"/, "the label itself was destroyed along with the colour");
  });

  test("a colour code inside a credential cannot hide it from the scrub", () => {
    // This is why the strip runs FIRST: `redact` is a byte-for-byte substring match, and a colour
    // code in the middle of a value splits it in two.
    const split = `key sk-ant-LEAK${ESC}[0mMODELKEY0000000000`;
    const out = scrub(split, { env: { ANTHROPIC_API_KEY: S.model } });
    assert.ok(!out.includes("MODELKEY0000000000"), `a credential hid behind a colour code: ${out}`);
  });

  test("stripAnsi leaves ordinary text exactly as it was", () => {
    const s = "the cart [1 item] shows $4.00 — and a bracket] survives";
    assert.equal(stripAnsi(s), s);
  });

  test("the scrub is idempotent, which is what lets it run twice without growing [[redacted]]", () => {
    // buildBundle's own comment claims this. It was false: `authorization: [redacted]` matched
    // itself and became `authorization: [redacted]]`, and the same for the session arms.
    const corpus = [
      'storageState: {"cookies":[{"name":"session","value":"LEAKSESSIONCOOKIE000"}]}',
      JSON.stringify('{"cookies":[{"name":"session","value":"LEAKSESSIONCOOKIE000"}]}').slice(1, -1),
      "authorization: Bearer abcdefghijklmnop",
      "Authorization=Basic dXNlcjpwYXNz",
      "x-api-key: abcdef123456",
      "Set-Cookie: session=ABCDEFGHIJKL; Path=/",
      "Cookie: session_id=ABCDEFGHIJKL",
      "https://u:p@host/x",
      "https://x/?access_token=TOKENVALUE12345#id_token=OTHER123456",
      'fill textbox "Password" with "hunter2xyz"',
      "sign in with the password Hunter2-xyz",
    ];
    for (const c of corpus) {
      const once = scrub(c, { env: {} });
      assert.equal(scrub(once, { env: {} }), once, `scrubbing twice changed the string: ${c} -> ${once}`);
      assert.ok(!/\[\[redacted/.test(once), `nested redaction markers: ${once}`);
    }
  });
});

// ---- 12. WHICH CREDENTIALS THIS PROCESS CAN SEE ---------------------------------------------------

describe("envSecrets finds every credential in reach, longest first", () => {
  test("the named vars, the login pair, and anything whose NAME says credential", () => {
    const got = envSecrets(leakEnv());
    for (const k of ["model", "write", "runkey", "loginpw", "loginem", "seedsec", "teardown", "ghtok", "stripe"]) {
      assert.ok(got.includes(S[k]), `${k} is reachable by this process and was not in the list`);
    }
  });

  test("longest first, so a short key is never redacted inside a long one", () => {
    const got = envSecrets({ SMOLANALYTICS_KEY: "sk-live-abc", SMOLANALYTICS_RUN_KEY: "sk-live-abcdef" });
    assert.ok(got.indexOf("sk-live-abcdef") < got.indexOf("sk-live-abc"), "[redacted]def is a partial credential published as safe");
    assert.equal(scrub("saw sk-live-abcdef today", { env: { SMOLANALYTICS_KEY: "sk-live-abc", SMOLANALYTICS_RUN_KEY: "sk-live-abcdef" } }), "saw [redacted] today");
  });

  test("the named list applies a lower floor than the name-shaped arm, which is its whole job", () => {
    // Every SECRET_VARS name would ALSO be caught by credentialName, so a long value proves nothing
    // about the named list. What only the named list does is accept four characters instead of
    // eight, for the credentials this runner itself puts into play.
    const short = "sk-x1";
    assert.equal(short.length < 8, true, "the fixture is no longer short enough to distinguish the two arms");
    assert.ok(envSecrets({ ANTHROPIC_API_KEY: short }).includes(short), "a short model key is still a model key");
    assert.ok(envSecrets({ SMOLANALYTICS_SEED_SECRET: short }).includes(short));
    // A customer's own variable with the same short value is NOT taken: eight characters is the
    // floor for a name they chose, because a short one is likelier to be a word in their prose.
    assert.ok(!envSecrets({ CUSTOMER_TOKEN: short }).includes(short));
  });

  test("a short value is refused rather than rewriting the whole document", () => {
    assert.ok(!envSecrets({ SMOLANALYTICS_KEY: "ok" }).includes("ok"));
    assert.equal(scrub("everything is ok", { env: { SMOLANALYTICS_KEY: "ok" } }), "everything is ok");
  });
});
