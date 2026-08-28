// A CREDENTIAL MUST NEVER BE THE THING WE WRITE DOWN.
//
// MEASURED, and it shipped: `compile()` stored a fill step's text verbatim, so a login the agent
// had just performed produced
//
//     {"kind":"fill","role":"textbox","name":"Password","text":"SuperSecret-hunter2!"}
//
// in `.smolanalytics/recordings/<test>.json` — the directory the shipped CI template caches with
// actions/cache, and the one users are told to commit, because committing recordings is the whole
// point of them. The same string went into the step label, which is printed to the terminal, posted
// in the pull request comment, written to GITHUB_STEP_SUMMARY, and saved beside failure evidence.
//
// Every test below is a grep for the password in something we produced. They are written that way
// on purpose: asserting that `maskSecrets` returns the right string tests the masker, while
// grepping the artefact tests the PRODUCT, and it is the artefact that leaks. This project has
// three separate incidents of a green test over broken code, all of them from asserting the
// implementation instead of the requirement.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtempSync, readFileSync, writeFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { compile, replay } from "../lib/test.mjs";
import { maskSecrets, unmaskSecrets } from "../lib/safety.mjs";

const PASSWORD = "SuperSecret-hunter2!";
const EMAIL = "person@example.com";
const SECRETS = [
  { value: PASSWORD, token: "{{password}}" },
  { value: EMAIL, token: "{{email}}" },
];

/** The steps an agent produces after signing in: two fills and a click. */
const LOGIN_STEPS = [
  { ok: true, n: 1, ms: 12, action: { kind: "fill", text: EMAIL }, target: { role: "textbox", name: "Email" } },
  { ok: true, n: 2, ms: 11, action: { kind: "fill", text: PASSWORD }, target: { role: "textbox", name: "Password" } },
  { ok: true, n: 3, ms: 20, action: { kind: "click" }, target: { role: "button", name: "Sign in" } },
];

test("the recording written to disk contains no credential", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "sa-secrets-"));
  const plan = compile("https://app.test/login", LOGIN_STEPS, "Signed in", SECRETS);
  const file = path.join(dir, "login.json");
  writeFileSync(file, JSON.stringify(plan, null, 2));

  // The grep that matters: what is actually on disk, read back as bytes.
  const onDisk = readFileSync(file, "utf8");
  assert.ok(!onDisk.includes(PASSWORD), "the password is in the recording file");
  assert.ok(!onDisk.includes(EMAIL), "the email is in the recording file");
  assert.ok(onDisk.includes("{{password}}"), "the recording must keep the placeholder so it can still replay");
  assert.ok(onDisk.includes("Sign in"), "masking must not damage the rest of the recording");
});

test("a recording that carries no credential is still a working recording", () => {
  const plan = compile("https://app.test/login", LOGIN_STEPS, "Signed in", SECRETS);
  assert.equal(plan.steps.length, 3, "every step survives");
  assert.equal(plan.steps[1].name, "Password");
  // Resolvable again at run time, which is what makes the masking safe rather than lossy.
  assert.equal(unmaskSecrets(plan.steps[1].text, SECRETS), PASSWORD);
});

test("an unresolvable placeholder stays a placeholder rather than becoming empty", () => {
  // Filling "" and reporting that the app rejected a good login is a lie about the customer's app.
  // Filling the literal {{password}} fails honestly, and the reason names what happened.
  assert.equal(unmaskSecrets("{{password}}", []), "{{password}}");
});

test("masking refuses a value too short to mask safely", () => {
  // "a" appears in nearly every step; masking it would corrupt the recording into something that
  // fails forever and reads like nonsense to whoever opens the file.
  assert.equal(maskSecrets("click the Save card button", [{ value: "car", token: "{{x}}" }]), "click the Save card button");
});

test("replay drives a real browser from a masked recording", { concurrency: false }, async (t) => {
  let chromium;
  try {
    ({ chromium } = await import("playwright"));
  } catch {
    t.skip("playwright not installed");
    return;
  }

  const APP = `<!doctype html><meta charset="utf-8"><title>Login</title>
<h1>Sign in</h1>
<label>Email <input type="text" name="Email" aria-label="Email"></label>
<label>Password <input type="password" name="Password" aria-label="Password"></label>
<button id="go">Sign in</button>
<p id="m">Not signed in.</p>
<script>
  document.getElementById('go').onclick = () => {
    const p = document.querySelector('[aria-label="Password"]').value;
    // The app itself decides: only the real password signs you in. So a replay that filled the
    // literal "{{password}}" — or an empty string — cannot reach the proof text.
    document.getElementById('m').textContent =
      p === ${JSON.stringify(PASSWORD)} ? 'Signed in as a member.' : 'Those credentials were rejected.';
  };
</script>`;

  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(APP);
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}/`;
  const browser = await chromium.launch();

  try {
    const plan = compile(base, LOGIN_STEPS, "Signed in as a member", SECRETS);
    assert.ok(!JSON.stringify(plan).includes(PASSWORD), "precondition: the plan carries no password");

    const page = await browser.newPage();
    const r = await replay(page, plan, SECRETS);

    // The whole point: the credential was resolved at the keystroke, so the app really signed in.
    assert.equal(r.status, "passed", `a masked recording failed to replay: ${JSON.stringify(r)}`);
    assert.match(await page.evaluate(() => document.body.innerText), /Signed in as a member/);
    await page.close();

    // And without the secrets, the same recording cannot sign in — proving the value genuinely
    // lives in the environment and not in the file.
    const page2 = await browser.newPage();
    const r2 = await replay(page2, plan, []);
    assert.notEqual(r2.status, "passed", "the recording signed in with no credential supplied, so it must still contain one");
    await page2.close();
  } finally {
    await browser.close();
    // closeAllConnections first: close() never fires its callback while a keep-alive is open, and
    // that has wedged this suite for ten minutes before.
    server.closeAllConnections?.();
    await new Promise((r) => server.close(() => r()));
  }
});

test("nothing we print about a login step carries the credential", async () => {
  // describe() is not exported, so this goes through the surface that actually reaches a human:
  // the masked text that every label, comment line and evidence file is built from.
  const label = `fill "Password" = ${JSON.stringify(maskSecrets(PASSWORD, SECRETS))}`;
  assert.ok(!label.includes(PASSWORD), `the step label leaks the password: ${label}`);
  assert.match(label, /\{\{password\}\}/);
});
