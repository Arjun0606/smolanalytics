// AUTHENTICATED RUNS — THE THREE WAYS THIS FEATURE COULD HURT SOMEBODY.
//
// Most tests worth writing live behind a login, so this is the largest coverage wall in the
// product. It is also the only feature that handles a customer's credentials, which makes its
// failure modes worse than "it does not work":
//
//   IT LEAKS A CREDENTIAL. Anything this file's code path can print or persist — the saved session,
//   its filename, the terminal, the pull request comment — must never contain the password. The
//   sibling failure at the recording layer is covered in test/secrets.test.mjs; this file covers
//   the auth artefacts.
//
//   IT BLAMES THE APP FOR OUR OWN SESSION PROBLEM. A session we could not establish says nothing
//   whatsoever about whether the customer's product works. Every such outcome is `errored` (our
//   side, exit 2) and never `failed` (their bug, exit 1). Getting this backwards puts a red X on a
//   working app, which is the fastest way to lose a team's trust in the whole suite.
//
//   IT LOGS IN FIFTY TIMES FOR A FIFTY-TEST SUITE. The economic claim of this product is that the
//   expensive thing happens once and is reused. A login per test would make an authenticated suite
//   cost more than having no recordings at all.
//
// These tests were written after the fact: the agent that built lib/auth.mjs died before writing
// any, and the module was already wired into the run path. An untested module that holds passwords
// is the one thing this product cannot ship, so the contracts are pinned here rather than trusted.

import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, existsSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  DEFAULT_AUTH_DIR, GITIGNORE_BODY, EMAIL_VAR, PASSWORD_VAR,
  redact, loginCredentials, prepareLogin, authSlug, authFileFor, authWarning,
  readAuth, writeAuth, atOrigin, landingEvidence, bounced, chooseAuth, openSession,
} from "../lib/auth.mjs";

const PASSWORD = "SuperSecret-hunter2!";
const EMAIL = "person@example.com";
const ENV = { [EMAIL_VAR]: EMAIL, [PASSWORD_VAR]: PASSWORD };
const SECRETS = [EMAIL, PASSWORD];

const snap = (...names) => ({ elements: names.map((n) => ({ role: "button", name: n })) });

/* ── which mechanism, and the refusal to guess ───────────────────────────────────────────────── */

test("two answers to the same question is refused, not silently ranked", () => {
  const r = chooseAuth({ login: "sign in", authFile: "state.json" });
  assert.equal(r.mode, "");
  assert.match(r.problem, /--auth-file|--login/);
});

test("no flags means nothing changes for anyone not using this", () => {
  assert.equal(chooseAuth({}).mode, "none");
  assert.equal(chooseAuth({}).problem, "");
});

test("each flag alone selects its own mechanism", () => {
  assert.equal(chooseAuth({ login: "sign in as {{email}}" }).mode, "login");
  assert.equal(chooseAuth({ authFile: "state.json" }).mode, "file");
});

test("an unauthenticated run is byte-identical to before this file existed", async () => {
  // The regression that would be shipped to every user in exchange for a flag almost none of them
  // pass. `mode: none` must not open a session, not read the disk, and not alter the page.
  const s = await openSession({ browser: null, url: "https://app.test" });
  assert.equal(s.active, false);
  assert.equal(s.mode, "none");
  assert.equal(s.problem, "");
  assert.equal(s.logins, 0);
});

/* ── the credential never reaches an artefact ────────────────────────────────────────────────── */

test("redact removes every secret from anything we print", () => {
  const line = `filling Password with ${PASSWORD} for ${EMAIL}`;
  const out = redact(line, SECRETS);
  assert.ok(!out.includes(PASSWORD), out);
  assert.ok(!out.includes(EMAIL), out);
  assert.match(out, /\[redacted\]/);
});

test("credentials come from the environment and nowhere else", () => {
  const c = loginCredentials(ENV);
  assert.equal(c.email, EMAIL);
  assert.equal(c.password, PASSWORD);
  assert.deepEqual(c.secrets, [EMAIL, PASSWORD]);
  // Absent env is not a crash: the caller reports it as a problem the reader can act on.
  const none = loginCredentials({});
  assert.deepEqual(none.secrets, []);
});

test("a value too short to mask safely is not treated as a secret", () => {
  // Masking a 3-character value would rewrite unrelated text and corrupt the artefact into
  // something that reads like nonsense to whoever opens it.
  const c = loginCredentials({ [EMAIL_VAR]: "a@b", [PASSWORD_VAR]: "xy" });
  assert.deepEqual(c.secrets, [], "short values must not become masking rules");
});

test("the saved session's FILENAME cannot carry a credential", () => {
  // A filename is the one artefact that shows up in `ls`, in a stack trace, and in a CI log line
  // nobody redacts.
  const name = authSlug(`sign in as ${EMAIL} with ${PASSWORD}`, SECRETS);
  assert.ok(!name.includes(PASSWORD), name);
  assert.ok(!name.includes("hunter2"), name);
  assert.ok(name.length > 0);
});

test("the saved session FILE cannot carry a credential, and cannot be committed by accident", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "sa-auth-"));
  const state = { cookies: [{ name: "sid", value: "abc123", domain: "app.test", path: "/" }], origins: [] };
  const landing = { detectable: true, loginPath: "/login", landedPath: "/app", markers: ["Sign in"] };

  const { file, wroteGitignore } = writeAuth(dir, `sign in as ${EMAIL} with ${PASSWORD}`, { state, landing }, SECRETS);

  const body = readFileSync(file, "utf8");
  assert.ok(!body.includes(PASSWORD), "the password is in the saved session file");
  assert.ok(body.includes("sid"), "the session cookie itself must still be there or the feature does nothing");

  // The file holds a live session cookie, so it must be unable to reach a repository.
  const ignore = path.join(path.dirname(file), ".gitignore");
  assert.ok(wroteGitignore, "writeAuth must report that it protected the directory");
  assert.ok(existsSync(ignore), "no .gitignore beside a file containing a live session");
  assert.equal(readFileSync(ignore, "utf8"), GITIGNORE_BODY);
});

test("the warning names the file, so the reader knows what to protect", () => {
  const w = authWarning(path.join(DEFAULT_AUTH_DIR, "x.json"));
  assert.match(w, /x\.json/);
});

/* ── a session we could not establish is never the customer's bug ────────────────────────────── */

test("an unreadable saved session is explained, and the explanation is about the file", () => {
  for (const [text, why] of [
    ["not json at all", /JSON/i],
    ["null", /object/i],
    ['{"hello":1}', /storage state|cookies/i],
    ['{"cookies":[],"origins":[]}', /signed out|cookies/i],
  ]) {
    const r = readAuth(text);
    assert.equal(r.state, null, `this should not have parsed: ${text}`);
    assert.match(r.problem, why);
    // Never phrased as a verdict about the app under test.
    assert.ok(!/\bfail(ed|s)?\b/i.test(r.problem), `a session problem must not read as a test failure: ${r.problem}`);
  }
});

test("a valid saved session parses, with or without our wrapper", () => {
  const bare = '{"cookies":[{"name":"sid","value":"v","domain":"a","path":"/"}],"origins":[]}';
  assert.ok(readAuth(bare).state, "a plain Playwright storageState must be accepted — teams already generate these");
  const wrapped = JSON.stringify({ kind: "smolanalytics-auth/1", landing: { detectable: true }, state: JSON.parse(bare) });
  const r = readAuth(wrapped);
  assert.ok(r.state);
  assert.ok(r.landing, "our wrapper also carries the evidence expiry detection needs");
});

/* ── recognising an expired session without guessing ─────────────────────────────────────────── */

test("the markers are the controls that vanished when the login worked", () => {
  // A name on BOTH pages proves nothing — a "Sign in" link living in the footer forever would fire
  // on every healthy page. Only what disappeared is evidence.
  const ev = landingEvidence(
    { url: "https://app.test/login", ...snap("Email", "Password", "Sign in", "Docs") },
    { url: "https://app.test/app", ...snap("Dashboard", "Log out", "Docs") },
    SECRETS,
  );
  assert.ok(ev.markers.includes("Sign in"), "a control that vanished is evidence");
  assert.ok(!ev.markers.includes("Docs"), "a control present on both pages is not evidence");
  assert.equal(ev.loginPath, "/login");
  assert.equal(ev.landedPath, "/app");
});

test("a bounce back to the login page is recognised", () => {
  const landing = { detectable: true, loginPath: "/login", landedPath: "/app", markers: ["Sign in"] };
  const back = bounced(landing, { url: "https://app.test/login", ...snap("Email", "Sign in") });
  assert.equal(back.bounced, true);
  assert.match(back.why, /login/);
});

test("a page showing a vanished control is recognised even when the URL never changed", () => {
  // The single-page-app case: the address stays put and the view swaps for a sign-in form. A
  // /login regex misses this entirely, which is why the markers exist.
  const landing = { detectable: true, loginPath: "/app", landedPath: "/app", markers: ["Sign in"] };
  const back = bounced(landing, { url: "https://app.test/app", ...snap("Sign in", "Password") });
  assert.equal(back.bounced, true);
  assert.match(back.why, /Sign in/);
});

test("a healthy authenticated page is NOT called a bounce", () => {
  // Every false fire spends a full agent run re-logging in for nothing, and the run gets slower and
  // more expensive the more reliable the app is.
  const landing = { detectable: true, loginPath: "/login", landedPath: "/app", markers: ["Sign in"] };
  assert.equal(bounced(landing, { url: "https://app.test/app", ...snap("Dashboard", "Log out") }).bounced, false);
  assert.equal(bounced(landing, { url: "https://app.test/settings", ...snap("Save") }).bounced, false);
});

test("with no usable evidence it refuses to guess", () => {
  // Claiming a bounce on no evidence would re-login on healthy pages forever.
  assert.equal(bounced(null, { url: "https://app.test/login" }).bounced, false);
  assert.equal(bounced({ detectable: false, markers: ["Sign in"] }, { url: "https://app.test/login" }).bounced, false);
});

/* ── the saved session follows the app to a new preview ──────────────────────────────────────── */

test("a session recorded on one preview is used against the next one, on the recorded page", () => {
  // Same rule as replay's rebase(): the URL says where the app is, the recording says which page.
  assert.equal(atOrigin("https://old.app/login", "https://new.app"), "https://new.app/login");
  assert.equal(atOrigin("https://old.app/", "https://new.app/start"), "https://new.app/start");
  assert.equal(atOrigin("not a url", "https://new.app"), "https://new.app");
});

/* ── the economics: once per suite, not once per test ────────────────────────────────────────── */

test("the login sentence is prepared from the environment, with the placeholders resolved", () => {
  const p = prepareLogin("sign in as {{email}} with {{password}}", { env: ENV });
  assert.ok(p.text.includes(EMAIL), "the agent must receive a sentence it can actually carry out");
  assert.ok(p.text.includes(PASSWORD));
  assert.deepEqual(p.secrets, [EMAIL, PASSWORD], "and everything substituted becomes a masking rule");
  assert.deepEqual(p.missing, []);
});

test("a login sentence with no credentials in the environment names the variables to set", () => {
  const p = prepareLogin("sign in as {{email}} with {{password}}", { env: {} });
  assert.ok(p.missing.length > 0, "silently signing in as nobody would report the app as broken");
  assert.ok(p.missing.join(" ").includes(EMAIL_VAR) || p.missing.join(" ").includes("email"), JSON.stringify(p.missing));
});

test("one saved session serves every test, so a fifty-test suite logs in once", async () => {
  // The economic claim, asserted against the mechanism rather than a stub: openSession is given a
  // performLogin it counts, then asked for many pages.
  let logins = 0;
  const dir = mkdtempSync(path.join(tmpdir(), "sa-auth-once-"));
  const pages = [];
  const fakeContext = {
    newPage: async () => {
      const p = {
        closed: false,
        goto: async () => {},
        close: async () => { p.closed = true; },
        evaluate: async () => "Signed in",
        url: () => "https://app.test/app",
      };
      pages.push(p);
      return p;
    },
    storageState: async () => ({ cookies: [{ name: "sid", value: "v", domain: "a", path: "/" }], origins: [] }),
    close: async () => {},
  };
  const browser = { newContext: async () => fakeContext };

  const session = await openSession({
    browser,
    url: "https://app.test",
    login: "sign in as {{email}} with {{password}}",
    authDir: dir,
    apiKey: "k",
    env: ENV,
    log: () => {},
    perceive: async () => ({ url: "https://app.test/app", elements: [{ role: "button", name: "Log out" }], text: "Signed in" }),
    performLogin: async () => {
      logins++;
      return { passed: true, why: "signed in" };
    },
  });

  assert.equal(session.problem, "", session.problem);
  assert.equal(session.active, true);

  for (let i = 0; i < 50; i++) {
    const p = await session.newPage();
    assert.ok(p, `page ${i} was not produced`);
  }
  assert.equal(logins, 1, `fifty tests triggered ${logins} logins; the expensive thing must happen once`);
  await session.close();

  // And the credential did not follow the session onto the disk.
  const written = readdirSync(dir).filter((f) => f.endsWith(".json"));
  for (const f of written) {
    assert.ok(!readFileSync(path.join(dir, f), "utf8").includes(PASSWORD), `${f} contains the password`);
  }
});

test("a login that did not work is OUR problem, never a bug report about their app", async () => {
  // The guarantee this file's header claims, and which nothing here tested until a mutation proved
  // it: turning off the `!a.passed` check changed no test at all. A wrong password reported as
  // `failed` puts a red X on somebody's working sign-in.
  const dir = mkdtempSync(path.join(tmpdir(), "sa-auth-bad-"));
  const session = await openSession({
    browser: { newContext: async () => ({
      newPage: async () => ({ goto: async () => {}, close: async () => {}, url: () => "https://app.test/login" }),
      storageState: async () => ({ cookies: [], origins: [] }),
      close: async () => {},
    }) },
    url: "https://app.test",
    login: "sign in as {{email}} with {{password}}",
    authDir: dir, apiKey: "k", env: ENV, log: () => {},
    perceive: async () => ({ url: "https://app.test/login", elements: [], text: "" }),
    performLogin: async () => ({ passed: false, why: "the password field was rejected" }),
  });

  assert.ok(session.problem, "a failed sign-in must stop the run with a problem, not proceed signed-out");
  assert.equal(session.active, false);
  // It must point at the credentials and the sentence — the two things the reader controls.
  assert.match(session.problem, new RegExp(PASSWORD_VAR));
  // And it must never be phrased as a verdict about the application under test.
  assert.ok(!/\byour app\b|\bthe app is\b/i.test(session.problem), session.problem);
  // The credential does not survive into the message.
  assert.ok(!session.problem.includes(PASSWORD), "the failure message leaks the password");
});

test("a sign-in that leaves no cookie is refused rather than saved", async () => {
  // Saving an empty session hands every later test a signed-out browser and blames the app for it.
  const dir = mkdtempSync(path.join(tmpdir(), "sa-auth-empty-"));
  const session = await openSession({
    browser: { newContext: async () => ({
      newPage: async () => ({ goto: async () => {}, close: async () => {}, url: () => "https://app.test/app" }),
      storageState: async () => ({ cookies: [], origins: [] }),
      close: async () => {},
    }) },
    url: "https://app.test",
    login: "sign in as {{email}} with {{password}}",
    authDir: dir, apiKey: "k", env: ENV, log: () => {},
    perceive: async () => ({ url: "https://app.test/app", elements: [], text: "Signed in" }),
    performLogin: async () => ({ passed: true, why: "signed in" }),
  });
  assert.ok(session.problem, "an empty storage state must not be saved as a session");
  assert.match(session.problem, /no cookie|storage/i);
});
