// AUTHENTICATED FLOWS — the tests worth writing live behind a login.
//
// Everything this product can test today is a page a stranger can reach: pricing, marketing, a
// public product listing. The checkout, the dashboard, the settings page, the thing the customer
// is actually afraid of breaking — all of it is behind a session, and until this file existed the
// honest answer was "we have no answer at all". That is the single biggest hole in the coverage.
//
// TWO MECHANISMS, ONE ARTEFACT. Both end at a Playwright storage state — the cookies and the
// localStorage of a browser that is logged in — because that is the only thing a browser needs to
// arrive already authenticated, and it costs no new dependency.
//
//   --auth-file <path>   somebody's existing storageState JSON, generated however they like. The
//                        escape hatch for teams with a global-setup that already writes one.
//   --login "<sentence>" a LOGIN RECORDING. The agent signs in ONCE, the resulting storage state
//                        is saved, and every later run in the suite starts from it. Exactly the
//                        record-then-replay economics the rest of this product is built on, with
//                        the login as the thing recorded: a 50-test suite logs in once, not fifty
//                        times, and the 49 that follow cost one page load instead of an agent run.
//
// WHAT MAKES THIS HARD IS NOT LOGGING IN, IT IS EXPIRY. A saved session dies — a short-lived JWT,
// a server restart, a deploy that rotates the signing key, a nightly cleanup. When it does, the app
// bounces the browser back to a sign-in page, the agent looks at a login form and truthfully
// reports that it could not find the dashboard, and a green suite turns red across the board with
// a bug report about somebody's working application. That is the worst thing this feature could
// ship, so a bounce is detected, repaired with exactly one fresh login, and the test is run again.
// If the LOGIN is what fails, that is `errored` — our side or the credentials — and never `failed`.
//
// SECRETS. Credentials come from the environment, never from a file and never from a flag, and
// every line this file logs goes through redact() first. The storage state that lands on disk is
// a live session, so it is announced in one line and the directory gets a .gitignore of its own.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { newIdentity, substitute } from "./safety.mjs";

const C = {
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

export const DEFAULT_AUTH_DIR = path.join(".smolanalytics", "auth");

/** Marks a file as ours, so readAuth can tell our wrapper from a bare Playwright storage state. */
export const AUTH_KIND = "smolanalytics-auth/1";

/** `*` — everything in the directory, including any state file added later. */
export const GITIGNORE_BODY = "*\n";

/** The env vars that carry the credentials. Named in every error that is about them. */
export const EMAIL_VAR = "SMOLANALYTICS_LOGIN_EMAIL";
export const PASSWORD_VAR = "SMOLANALYTICS_LOGIN_PASSWORD";

const first = (e) => String(e && e.message ? e.message : e).split("\n")[0];

// ---- secrets ----------------------------------------------------------------------------------

/**
 * Remove known credentials from a string before it is logged or written.
 *
 * split/join rather than a RegExp: a password is chosen by the customer and will eventually contain
 * `$`, `\` or `(`, and a regex built from it would either throw or, worse, quietly stop matching —
 * which is a password printed to a CI log.
 *
 * Short strings are left alone. Redacting a two-character secret would replace every occurrence of
 * those two characters everywhere in the output, which destroys the log without protecting anything.
 */
export function redact(text, secrets = []) {
  let out = String(text ?? "");
  for (const s of secrets) {
    if (typeof s !== "string" || s.length < 4) continue;
    out = out.split(s).join("[redacted]");
  }
  return out;
}

/** The credentials, from the environment only. */
export function loginCredentials(env = process.env) {
  const email = String(env[EMAIL_VAR] ?? "");
  const password = String(env[PASSWORD_VAR] ?? "");
  return { email, password, secrets: [email, password].filter((s) => s.length >= 4) };
}

/**
 * The login sentence with its placeholders filled in, plus what it needs and did not get.
 *
 * substitute() from lib/safety.mjs, so `{{email}}` and `{{password}}` mean exactly what they mean
 * in a test sentence — same tokeniser, same case-insensitivity, same report of a typo'd token.
 * The difference is WHERE the two credential fields come from: a test sentence's `{{email}}` is a
 * fresh throwaway identity for a signup, and a login's `{{email}}` is a real account that already
 * exists, which only the person running this knows.
 *
 * A missing variable is REFUSED rather than defaulted. Falling back to the generated identity would
 * try to sign in as an account that does not exist, and report the customer's app as broken because
 * our own configuration was.
 */
export function prepareLogin(sentence, { env = process.env } = {}) {
  const { email, password, secrets } = loginCredentials(env);
  // The non-credential placeholders keep working; only email and password are overridden. They are
  // "" when unset, never the generated ones, so a caller that ignored `missing` sends an empty
  // field rather than signing in as somebody who was never meant to exist.
  const identity = { ...newIdentity({}), email, password };
  const sub = substitute(sentence, identity);
  const missing = [];
  if (sub.used.includes("email") && !email) missing.push(EMAIL_VAR);
  if (sub.used.includes("password") && !password) missing.push(PASSWORD_VAR);
  return { text: sub.text, used: sub.used, unknown: sub.unknown, secrets, missing };
}

// ---- where the state lives ---------------------------------------------------------------------

/**
 * A filesystem-safe name for a login sentence. Built from the sentence, so two different logins
 * (an admin and a customer) in one repo get two different files.
 *
 * Redacted FIRST. If somebody ignores the advice and writes the password into the sentence itself,
 * this is the one place it would otherwise end up somewhere permanent — a filename, which survives
 * in shell history, in `ls`, and in a directory listing pasted into an issue.
 */
export function authSlug(sentence, secrets = []) {
  return (
    redact(sentence, secrets)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 60) || "login"
  );
}

export function authFileFor(dir, sentence, secrets = []) {
  return path.join(dir || DEFAULT_AUTH_DIR, `${authSlug(sentence, secrets)}.json`);
}

export function authWarning(file) {
  return `${file} now holds a live session for this app — cookies a browser would be believed with. Keep it out of version control.`;
}

/**
 * Read a saved session. Accepts BOTH shapes on purpose.
 *
 * A bare `{cookies, origins}` is what `context.storageState()` writes and what every team who
 * already has one will hand to --auth-file. Our own file wraps that in a little envelope so it can
 * also carry the landing evidence expiry detection needs. Refusing the bare shape would make the
 * escape hatch useless; refusing the envelope would make our own file unreadable.
 *
 * The empty check matters more than it looks: `{"cookies":[],"origins":[]}` is valid JSON, a valid
 * storage state, and authenticates nothing. Loading it silently means every test runs logged out
 * and reports the app as broken.
 */
export function readAuth(text) {
  let raw;
  try {
    raw = JSON.parse(String(text));
  } catch (e) {
    return { state: null, landing: null, problem: `it is not valid JSON (${first(e)}).` };
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { state: null, landing: null, problem: "it is not an object." };
  }
  const wrapped = raw.kind === AUTH_KIND && raw.state && typeof raw.state === "object" && !Array.isArray(raw.state);
  const state = wrapped ? raw.state : raw;
  if (!Array.isArray(state.cookies) || !Array.isArray(state.origins)) {
    return { state: null, landing: null, problem: "it is not a Playwright storage state — a saved session is an object with a cookies list and an origins list, written by context.storageState({ path })." };
  }
  if (!state.cookies.length && !state.origins.length) {
    return { state: null, landing: null, problem: "it carries no cookies and no stored origins, so a browser loaded with it would still be signed out." };
  }
  return { state, landing: wrapped && raw.landing && typeof raw.landing === "object" ? raw.landing : null, problem: "" };
}

/**
 * Save the session, and make it hard to commit by accident.
 *
 * The .gitignore is written into the directory rather than appended to the repo's, because we do
 * not edit files we did not create — that promise is the reason this CLI is used at all — and
 * because a per-directory ignore travels with the directory into whatever cache CI keeps it in.
 */
export function writeAuth(dir, sentence, { state, landing }, secrets = []) {
  const target = dir || DEFAULT_AUTH_DIR;
  mkdirSync(target, { recursive: true });
  const gitignore = path.join(target, ".gitignore");
  let wroteGitignore = false;
  if (!existsSync(gitignore)) {
    writeFileSync(gitignore, GITIGNORE_BODY);
    wroteGitignore = true;
  }
  const file = authFileFor(target, sentence, secrets);
  writeFileSync(file, JSON.stringify({ kind: AUTH_KIND, savedAt: new Date().toISOString(), landing, state }, null, 2) + "\n");
  return { file, wroteGitignore, warning: authWarning(file) };
}

// ---- expiry: what a bounce looks like, for THIS app ---------------------------------------------

const pathOf = (u) => {
  try {
    return new URL(u).pathname;
  } catch {
    return "";
  }
};

/**
 * Point a recorded URL at the deployment being tested right now — origin from `url`, path and query
 * from the recording. The same rule as rebase() in lib/test.mjs, and deliberately a second copy of
 * it: lib/test.mjs imports this file, and importing it back would complete a module cycle, which is
 * how ESM hands one of the two files a half-initialised module. (lib/test.mjs duplicates suite.mjs's
 * slug() for the same reason.) Eight lines is a cheaper price than that class of bug.
 */
export function atOrigin(recorded, url) {
  let rec;
  let now;
  try {
    rec = new URL(recorded);
  } catch {
    return url;
  }
  try {
    now = new URL(url);
  } catch {
    return recorded;
  }
  const bare = rec.pathname === "/" || rec.pathname === "";
  return bare ? now.origin + now.pathname + now.search + now.hash : now.origin + rec.pathname + rec.search + rec.hash;
}

/**
 * What the login looked like, so a bounce back to it can be recognised later.
 *
 * WHY THIS IS EVIDENCE AND NOT A `/login` REGEX. The obvious implementation is
 * `if (/\/(login|signin)/.test(page.url()))`, and it is wrong in both directions:
 *
 *   it MISSES the real apps. Rails writes /users/sign_in, Django /accounts/login/, Laravel /login
 *   but Nova /nova/login, Supabase apps /auth/sign-in, a Shopify storefront /account/login, and a
 *   great many single-page apps do not change the URL at all — they swap the view for a sign-in
 *   modal at the same address, or bounce to an identity provider on a different domain entirely.
 *   Every miss is a red X on a working app, which is the exact failure this feature exists to stop.
 *
 *   it FIRES on pages that are not a bounce. A docs site with /docs/login, a marketing page at
 *   /login-help, a test whose whole subject IS the sign-in page. Every false fire spends a full
 *   agent run re-logging in for nothing.
 *
 * We do not have to guess, because we WATCHED this app's login happen. The page the agent started
 * from is this app's login page, whatever it is called; the page it ended on is this app's
 * authenticated landing. So the evidence is those two pages, and the markers are the control names
 * that were on the first and NOT on the second — "Sign in", "Forgot your password?", "Create an
 * account". A name that is on both (a "Sign in" link that lives in the footer forever) proves
 * nothing and is excluded by construction, which is what keeps this from firing on a healthy page.
 */
export function landingEvidence(entry, landed, secrets = []) {
  const names = (snap) => (snap && Array.isArray(snap.elements) ? snap.elements : []).map((e) => String(e && e.name ? e.name : "").trim()).filter(Boolean);
  const after = new Set(names(landed));
  const markers = [];
  for (const n of names(entry)) {
    if (after.has(n)) continue;
    if (n.length < 3 || n.length > 60) continue;
    // A credential must not travel into a file just because the app echoed it back into a label.
    if (secrets.some((s) => s && n.includes(s))) continue;
    if (!markers.includes(n)) markers.push(n);
    if (markers.length >= 8) break;
  }
  const loginPath = pathOf(entry && entry.url);
  const landedPath = pathOf(landed && landed.url);
  return {
    loginUrl: redact(entry && entry.url ? entry.url : "", secrets),
    loginPath,
    landedUrl: redact(landed && landed.url ? landed.url : "", secrets),
    landedPath,
    markers,
    // Two pages that are indistinguishable give us nothing to detect a bounce WITH, and inventing a
    // rule for that case is how false re-logins start. Said out loud instead, once.
    detectable: markers.length > 0 || Boolean(loginPath && loginPath !== landedPath),
  };
}

/**
 * Has the app bounced us back to its login page?
 *
 * The URL first, because a path is unambiguous when it changed. Then the markers, matched as WHOLE
 * control names rather than as substrings of the page text: a settings page legitimately has a
 * "Change password" button, and a substring match on the marker "Password" would call that a
 * bounce and burn an agent run every single run.
 */
export function bounced(landing, now) {
  if (!landing || !landing.detectable) return { bounced: false, why: "" };
  const here = pathOf(now && now.url);
  if (landing.loginPath && landing.loginPath !== landing.landedPath && here && here === landing.loginPath) {
    return { bounced: true, why: `the browser is on ${landing.loginPath}, the page this app's login was recorded starting from` };
  }
  const names = new Set((now && Array.isArray(now.elements) ? now.elements : []).map((e) => String(e && e.name ? e.name : "").trim()));
  const hit = (Array.isArray(landing.markers) ? landing.markers : []).find((m) => names.has(m));
  if (hit) {
    return { bounced: true, why: `the page is showing ${JSON.stringify(hit)}, which was on this app's login page and not on the page the login landed on` };
  }
  return { bounced: false, why: "" };
}

// ---- the session ---------------------------------------------------------------------------------

/**
 * Which mechanism is in play, or why neither can be.
 */
export function chooseAuth({ login = "", authFile = "" } = {}) {
  const l = String(login || "").trim();
  const f = String(authFile || "").trim();
  if (l && f) {
    return { mode: "", problem: "--auth-file and --login are two answers to the same question: --auth-file loads a session somebody else made, --login records one. Pass one of them." };
  }
  if (f) return { mode: "file", problem: "" };
  if (l) return { mode: "login", problem: "" };
  return { mode: "none", problem: "" };
}

/**
 * A source of pages that are already signed in.
 *
 * `perceive` and `performLogin` are injected rather than imported: lib/test.mjs owns the browser
 * vocabulary and imports this file, so importing it back would complete a module cycle. Injection
 * also means every rule in this file can be tested without a browser at all.
 *
 * Returns an object with `.problem` set when the run cannot proceed — the caller reports that as
 * `errored` and exits 2. It is never `failed`: a session we could not establish says nothing
 * whatsoever about whether the customer's application works.
 */
export async function openSession({
  browser,
  url,
  login = "",
  authFile = "",
  authDir = DEFAULT_AUTH_DIR,
  apiKey = "",
  maxSteps = 40,
  viewport = { width: 1280, height: 900 },
  env = process.env,
  log = console.log,
  perceive,
  performLogin,
  fs: io = { exists: (p) => existsSync(p), read: (p) => readFileSync(p, "utf8") },
}) {
  const { mode, problem: choice } = chooseAuth({ login, authFile });
  if (choice) return { problem: choice, active: false, mode: "", secrets: [], logins: 0, newPage: async () => null, close: async () => {} };

  // NOTHING CHANGES FOR ANYONE NOT USING THIS. Same call, same arguments, same page as before this
  // file existed — an authentication feature that alters how unauthenticated runs behave would be
  // a regression shipped to every user in exchange for a flag almost none of them passed.
  if (mode === "none") {
    return {
      problem: "",
      active: false,
      mode,
      secrets: [],
      logins: 0,
      file: "",
      newPage: () => browser.newPage({ viewport }),
      close: async () => {},
    };
  }

  const prep = mode === "login" ? prepareLogin(login, { env }) : { text: "", used: [], unknown: [], secrets: [], missing: [] };
  const secrets = prep.secrets;
  const say = (line) => log(redact(line, secrets));

  if (mode === "login" && prep.missing.length) {
    return {
      problem: `The login sentence uses ${prep.missing.map((v) => (v === EMAIL_VAR ? "{{email}}" : "{{password}}")).join(" and ")}, but ${prep.missing.join(" and ")} ${prep.missing.length > 1 ? "are" : "is"} not set. Credentials are read from the environment and never from a flag or a file, so nothing was opened.`,
      active: false,
      mode,
      secrets,
      logins: 0,
      newPage: async () => null,
      close: async () => {},
    };
  }
  for (const bad of prep.unknown) say(C.y(`${bad} is not a placeholder this runner knows, so it stays in the login sentence as written.`));

  const file = mode === "login" ? authFileFor(authDir, login, secrets) : String(authFile).trim();

  let state = null;
  let landing = null;
  let logins = 0;
  let ctx = null;

  if (mode === "file") {
    let text = "";
    try {
      text = io.read(file);
    } catch (e) {
      return { problem: `The session file ${file} could not be read (${first(e)}). This is the test runner's input, not your application.`, active: false, mode, secrets, logins, file, newPage: async () => null, close: async () => {} };
    }
    const read = readAuth(text);
    if (!read.state) {
      // Refused, never worked around. A storage state we cannot understand would load an empty
      // session, and every test would then report the customer's app as broken for asking them to
      // sign in — a page of red X's caused entirely by our own input.
      return { problem: `The session file ${file} cannot be used: ${read.problem} This is the test runner's input, not your application.`, active: false, mode, secrets, logins, file, newPage: async () => null, close: async () => {} };
    }
    state = read.state;
    // A file somebody else produced carries no landing evidence, so expiry cannot be detected and
    // there is no login sentence to repair it with either. Both facts are honest and both are said
    // out loud, because a stale --auth-file failing as `failed` is the trap this warns about.
    landing = read.landing;
    say(C.dim(`signed in from ${file}${landing ? "" : " — it carries no login recording, so an expired session cannot be detected or renewed here"}.`));
    say(C.dim(`  ${authWarning(file)}`));
  }

  /** One full agent login into a clean context. Writes the state and returns the landing evidence. */
  const doLogin = async () => {
    if (!apiKey) {
      return { problem: `Signing in needs the agent, and ANTHROPIC_API_KEY is not set. Once one run has signed in, ${file} is reused and no key is needed for the runs that follow.` };
    }
    if (typeof performLogin !== "function" || typeof perceive !== "function") {
      return { problem: "This runner was built without a way to drive the browser for a login." };
    }
    const fresh = await browser.newContext({ viewport });
    try {
      const page = await fresh.newPage();
      await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
      const entry = await perceive(page);
      say(C.dim(`signing in once for this run: ${redact(login, secrets)}`));
      // Every line the login prints is redacted: the agent narrates what it fills in, and one of
      // the fields it fills in is a password. A CI log is forever.
      const a = await performLogin({ page, test: prep.text, maxSteps, log: (...parts) => log(redact(parts.join(" "), secrets)) });
      if (!a || a.kind === "error") {
        return { problem: `The login did not complete: ${redact(a && a.why ? a.why : "the agent reached no verdict", secrets)} This is the test runner, not your application.` };
      }
      if (!a.passed) {
        // The agent watched the sign-in and it did not work. That is OUR side of the fence — the
        // credentials, or the sentence — and reporting it as `failed` would put a bug report about
        // somebody's working login on their pull request.
        return { problem: `Could not sign in: ${redact(a.why, secrets)} Check ${EMAIL_VAR} and ${PASSWORD_VAR}, and that the sentence after --login describes this app's sign-in.` };
      }
      const landed = await perceive(page);
      const saved = await fresh.storageState();
      if (!saved || (!(saved.cookies || []).length && !(saved.origins || []).length)) {
        // The agent believed the login worked, and the browser was left holding nothing that would
        // survive a page load. Saving that would hand every later test an empty session and blame
        // the app for it, so it stops here and says which half is missing.
        return { problem: `The sign-in reported success but left the browser with no cookie and nothing in storage, so there is no session to reuse. This runner cannot carry a login that lives only in the page's memory.` };
      }
      const evidence = landingEvidence(entry, landed, secrets);
      let written;
      try {
        written = writeAuth(authDir, login, { state: saved, landing: evidence }, secrets);
      } catch (e) {
        return { problem: `Signed in, but the session could not be saved to ${file} (${first(e)}), so every test would sign in again.` };
      }
      logins++;
      say(C.y(written.warning));
      if (written.wroteGitignore) say(C.dim(`  wrote ${path.join(authDir, ".gitignore")} so it cannot be committed by accident.`));
      if (!evidence.detectable) {
        say(C.dim("  this app's signed-in page looks the same as its sign-in page, so an expired session cannot be told apart from a working one here."));
      }
      return { state: saved, landing: evidence, problem: "" };
    } finally {
      await fresh.close().catch(() => {});
    }
  };

  if (mode === "login") {
    let text = "";
    let unreadable = "";
    if (io.exists(file)) {
      try {
        text = io.read(file);
      } catch (e) {
        unreadable = first(e);
      }
    }
    if (text || unreadable) {
      const read = unreadable ? { state: null, problem: `it could not be read (${unreadable}).` } : readAuth(text);
      if (read.state) {
        state = read.state;
        landing = read.landing;
        say(C.dim(`reusing the session in ${file} — no sign-in needed this run.`));
      } else {
        // A saved session we cannot use is NO saved session: say so and sign in again, which writes
        // over it. Exactly how a corrupt recording is handled in lib/test.mjs, and for the same
        // reason — the alternative is a run that can never repair itself.
        say(C.y(`Ignoring ${file}: ${read.problem}`));
        say(C.dim("  signing in again, which records over it."));
      }
    }
    if (!state) {
      const r = await doLogin();
      if (r.problem) return { problem: r.problem, active: false, mode, secrets, logins, file, newPage: async () => null, close: async () => {} };
      state = r.state;
      landing = r.landing;
    }
  }

  /** A brand new context seeded from the saved session: signed in, and carrying nothing else. */
  const freshPage = async () => {
    const old = ctx;
    ctx = await browser.newContext({ viewport, storageState: state });
    // Closed AFTER the new one exists, so a context that fails to open leaves the run with the one
    // it had rather than with none.
    if (old) await old.close().catch(() => {});
    return ctx.newPage();
  };

  return {
    problem: "",
    active: true,
    mode,
    secrets,
    file,
    get logins() {
      return logins;
    },
    get landing() {
      return landing;
    },
    /**
     * A page that is signed in, verified, and clean.
     *
     * A CONTEXT PER PAGE, not one context reused. The retry in lib/test.mjs promises a clean page —
     * no cookies, no storage, nothing left over from the run that failed — and that promise has to
     * survive authentication: the retry gets a context seeded from the SAVED session, so it is
     * clean of the failed run and still signed in.
     *
     * The verification is one page load against the page the login landed on. It costs a navigation
     * per test and it buys the difference between "your dashboard is broken" and "your session
     * expired", which is worth far more than a page load.
     */
    async newPage() {
      for (let attempt = 0; attempt < 2; attempt++) {
        const page = await freshPage();
        if (mode !== "login" || !landing || !landing.detectable) return page;
        const target = atOrigin(landing.landedUrl, url);
        await page.goto(target, { waitUntil: "domcontentloaded", timeout: 30_000 });
        const b = bounced(landing, await perceive(page));
        if (!b.bounced) return page;
        if (logins >= 2 || attempt > 0) {
          // Signed in again and got bounced again. Looping would spend an agent run per attempt on
          // an app that is not going to accept us; this is our side either way, so it errors.
          throw new Error(`Signed in again and the app still sent the browser back to its sign-in page (${b.why}). This is the test runner or the credentials, not a fault in your application.`);
        }
        say(C.y(`the saved session is no longer accepted — ${b.why}. Signing in once more and retrying this test.`));
        const r = await doLogin();
        if (r.problem) throw new Error(r.problem);
        state = r.state;
        landing = r.landing;
      }
      throw new Error("the session could not be established");
    },
    async close() {
      if (ctx) await ctx.close().catch(() => {});
      ctx = null;
    },
  };
}
