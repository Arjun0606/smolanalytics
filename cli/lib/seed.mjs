// `--seed <url>` — the half of test-data handling that labelling and deleting could never do.
//
// THE COVERAGE WALL THIS EXISTS TO KNOCK DOWN.
//
// lib/safety.mjs can LABEL what a run creates and, with --teardown, DELETE it afterwards. Both are
// about data the test makes on its way through. Neither can PROVIDE data the test needs before it
// starts, and that is most of what anybody actually wants to test:
//
//   "a logged-in user with three past orders can request a refund"
//   "a subscription in its last week shows the renewal banner"
//   "an admin can suspend an account that has an open dispute"
//
// None of those is writable against a fresh app. Every one of them is testable by a competitor
// whose SDK seeds real fixtures, and untestable by us — so the suite goes green over the flows that
// do not matter while the ones that do are simply never written. That is the shape of a tool people
// keep and stop trusting, and it is the #2 reason a test product gets cancelled: not a false red, a
// silent coverage wall.
//
// THE SHAPE, AND WHY IT IS NOT AN ENVIRONMENT FACTORY.
//
// The competing answer is to own the environment: a GitHub App, a Dockerfile, a database the vendor
// builds and seeds. An hour of onboarding before the first verdict. The customer already has the
// only thing that can fabricate their state correctly — their own app — so this inverts it exactly
// the way --teardown did:
//
//   1. before the test runs, POST this run's identity to an endpoint THEY wrote,
//   2. their app makes whatever the sentence needs and answers with a flat JSON object,
//   3. every key in that object becomes a placeholder the sentence can use, exactly like {{email}}.
//
//   npx smolanalytics test --url https://staging.app.com \
//     --seed https://staging.app.com/api/test-seed \
//     --test "open order {{orderId}} and request a refund"
//
// Ten lines of handler, no vendor in their database, and the fixture is built by the code that owns
// the schema instead of by us guessing at it.
//
// THE THREE RULES THAT ARE NOT NEGOTIABLE.
//
//   ERRORED, NEVER FAILED.  If the seed endpoint is down, slow, or answers something we cannot use,
//                           the world the sentence describes was never built. Reporting `failed`
//                           would put "your refund flow is broken" on a pull request because OUR
//                           setup step could not reach THEIR endpoint. Exit 2, our code, always.
//
//   MASKED LIKE A PASSWORD. A seeded value is whatever their fixture handed us — an order id, and
//                           just as easily a session token or a signed magic link. It goes through
//                           the same maskSecrets pairs as a credential, so the recording, the step
//                           labels, the pull request comment and the evidence text carry
//                           {{orderId}} and never its value. See lib/safety.mjs for the incident
//                           that made that machinery exist, and lib/seedguard.mjs for the two
//                           places — a navigation, and a percent-encoded value — where a plain
//                           substring mask was measured missing it.
//
//   INERT WHEN UNUSED.      No --seed means no request, no placeholders, no secrets, and byte-for
//                           -byte the same recording. This file is not allowed to cost anything to
//                           someone who has not asked for it.

import { PLACEHOLDERS, PLACEHOLDER_LIST } from "./safety.mjs";
import { guardPairs } from "./seedguard.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

/**
 * The secret is env-only, for the same reason --teardown's is: a --seed-secret flag lands in shell
 * history and in the command line every CI runner prints at the top of its log. Deliberately NOT
 * falling back to SMOLANALYTICS_TEARDOWN_SECRET — one endpoint may be public and the other not, and
 * silently sending a teardown credential to a seed URL is a surprise nobody asked for.
 */
export const SEED_SECRET_VAR = "SMOLANALYTICS_SEED_SECRET";

/**
 * The hard cap. A seed endpoint that has to build a fixture is doing real work, so this is generous
 * — and it is a CAP, not a suggestion: a test run that hangs on somebody's setup route is a hung
 * build, and a hung build is how a feature gets deleted.
 */
export const SEED_TIMEOUT_MS = 10_000;

/**
 * MUST match safety.mjs's own TOKEN. It is not exported, so it is mirrored here, and
 * test/seed.test.mjs asserts the two agree on a shared list of strings — if they ever drift, a
 * token one file thinks is a placeholder and the other does not becomes a value silently left in
 * the sentence or a name silently dropped.
 */
const TOKEN = /\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}/g;

/** A key that cannot be written as {{key}} is a placeholder nobody can ever reference. */
const KEY = /^[A-Za-z][A-Za-z0-9_]*$/;

/** Every placeholder name the sentence references, in order, lowercased, without duplicates. */
export function tokensIn(text) {
  const out = [];
  for (const m of String(text ?? "").matchAll(TOKEN)) {
    const k = m[1].toLowerCase();
    if (!out.includes(k)) out.push(k);
  }
  return out;
}

/** The seed tokens a sentence asks for: the ones this runner cannot fill by itself. */
export function seedTokensIn(text) {
  return tokensIn(text).filter((k) => !Object.hasOwn(PLACEHOLDERS, k));
}

/**
 * THE RESPONSE CONTRACT, ENFORCED HERE AND NOWHERE ELSE.
 *
 * A flat object of string values. Numbers and booleans are accepted and stringified — an id column
 * is an integer in most databases and refusing `{"orderId":1042}` would be a papercut on the very
 * first thing anybody tries — but nothing nested, because a placeholder is a string substituted
 * into a sentence and there is no honest rendering of `{"user":{"id":7}}` as one.
 *
 * Every refusal NAMES the key and says what to do about it. Silently dropping a key is the failure
 * mode this whole family of bugs comes from: the sentence then carries a literal {{orderId}} into
 * the model, the agent invents something, and the verdict is about a world nobody built.
 */
export function readSeedValues(raw) {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) {
    const what = raw === null ? "null" : Array.isArray(raw) ? "a JSON array" : `a JSON ${typeof raw}`;
    return { values: null, problem: `it returned ${what}, not an object. Return a flat object of string values, for example {"orderId":"A-1042"}.` };
  }
  const values = {};
  const lower = new Map();
  for (const [key, v] of Object.entries(raw)) {
    if (!KEY.test(key)) {
      return { values: null, problem: `it returned the key ${JSON.stringify(key)}, which cannot be written as a placeholder. Keys must start with a letter and hold only letters, digits and underscores.` };
    }
    const k = key.toLowerCase();
    // A run identity key would shadow the account --teardown deletes and the prefix every row is
    // greppable by. Renaming it costs one character; losing the traceability costs an afternoon
    // in somebody's users table.
    if (Object.hasOwn(PLACEHOLDERS, k)) {
      return { values: null, problem: `it returned the key ${JSON.stringify(key)}, which is already this run's own identity placeholder. Rename it (userEmail, seedName) so ${`{{${k}}}`} keeps naming the account teardown deletes.` };
    }
    if (lower.has(k)) {
      return { values: null, problem: `it returned both ${JSON.stringify(lower.get(k))} and ${JSON.stringify(key)}, which are the same placeholder. Placeholder names ignore case, so one of them has to change.` };
    }
    lower.set(k, key);
    if (typeof v === "string") values[k] = v;
    else if (typeof v === "number" && Number.isFinite(v)) values[k] = String(v);
    else if (typeof v === "boolean") values[k] = String(v);
    else {
      const what = v === null ? "null" : Array.isArray(v) ? "an array" : typeof v === "object" ? "an object" : String(typeof v);
      return { values: null, problem: `it returned ${JSON.stringify(key)} as ${what}. Every value must be a string, a number or true/false — flatten it, for example ${JSON.stringify(`${key}Id`)}.` };
    }
  }
  return { values, problem: "" };
}

/**
 * Replace the seeded placeholders. Case and inner spaces are forgiven, the same way {{ Email }} is.
 *
 * A token this response does not carry is LEFT AS WRITTEN — never guessed at, never emptied. The
 * caller decides what an unfilled token means; see seedRun, where under --seed it is a setup
 * failure rather than a warning, because the sentence named state that was never built.
 */
export function applySeed(text, values = {}) {
  return replaceSeedTokens(text, values, (k) => values[k]);
}

/**
 * The same substitution, but writing the CANONICAL placeholder instead of the value:
 * `{{ OrderId }}` becomes `{{orderid}}`, which is the exact token seedSecrets pairs the value with.
 *
 * This is what the sentence is carried around as. Every artefact — the recording, the run summary,
 * the pull request comment — writes this string down, and unmaskSecrets turns it back into the
 * value at the two moments it is actually needed: the model prompt, and a keystroke.
 *
 * A value TOO SHORT to mask is substituted whole instead of tokenised. Writing {{id}} for a value
 * maskSecrets will refuse to resolve would send the literal text "{{id}}" to the model and into the
 * form field — filling the wrong thing and then reporting the app rejected it, which is a lie about
 * somebody's app. Leaking a two-character id into the recording is the lesser harm, and seedRun
 * says out loud that it is happening.
 */
export function maskSeed(text, values = {}) {
  const maskable = new Set(seedSecrets(values).map((p) => p.token.slice(2, -2)));
  return replaceSeedTokens(text, values, (k) => (maskable.has(k) ? `{{${k}}}` : values[k]));
}

function replaceSeedTokens(text, values, pick) {
  const used = [];
  const missing = [];
  const out = String(text ?? "").replace(TOKEN, (whole, key) => {
    const k = key.toLowerCase();
    // hasOwn, not `in`: {{constructor}} and {{toString}} are inherited by every object literal, and
    // `in` would happily substitute a function into somebody's test.
    if (!Object.hasOwn(values, k)) {
      if (!missing.includes(whole)) missing.push(whole);
      return whole;
    }
    if (!used.includes(k)) used.push(k);
    return pick(k);
  });
  return { text: out, used, missing };
}

/**
 * The masking pairs for everything this run writes down.
 *
 * Shorter than four characters is skipped by maskSecrets on purpose — masking "7" would rewrite
 * every step that happens to contain a seven — so a two-character seeded value simply is not
 * masked. That is stated out loud by seedRun rather than hidden, because a customer whose fixture
 * returns a short token deserves to know it will appear in the recording.
 *
 * Longest first: a fixture that returns {"id":"A-10","orderId":"A-1042"} would otherwise mask the
 * short one inside the long one and leave "{{id}}42" in the recording.
 */
export function seedSecrets(values = {}) {
  return Object.entries(values)
    .filter(([, v]) => typeof v === "string" && v.length >= 4)
    .sort((a, b) => b[1].length - a[1].length)
    .map(([k, v]) => ({ value: v, token: `{{${k}}}` }));
}

/** Values too short for maskSecrets to touch safely. Named, so nobody assumes a guarantee. */
export function unmaskableKeys(values = {}) {
  return Object.entries(values).filter(([, v]) => typeof v !== "string" || v.length < 4).map(([k]) => k);
}

/**
 * POST the run identity to the customer's endpoint and read back the fixture it built.
 *
 * The body is the same shape --teardown posts, plus `placeholders`: the seed tokens THIS sentence
 * asks for, so one handler can serve a whole suite and build only what each test needs. It is a
 * stable contract — adding a field is fine, renaming one is not, because it is somebody's
 * `if (body.placeholders.includes("orderId"))`.
 *
 * Never throws. Every outcome is a { ok, values, detail } that says which endpoint and what it did.
 */
export async function postSeed({
  endpoint,
  identity,
  test = "",
  url = "",
  env = process.env,
  fetchImpl = fetch,
  timeoutMs = SEED_TIMEOUT_MS,
  at = () => new Date().toISOString(),
} = {}) {
  if (!endpoint) return { ok: false, skipped: true, values: {}, detail: "no --seed endpoint" };
  const body = {
    runId: identity.runId,
    prefix: identity.prefix,
    email: identity.email,
    username: identity.username,
    name: identity.name,
    password: identity.password,
    test,
    url,
    placeholders: seedTokensIn(test),
    at: at(),
  };
  const headers = {
    "content-type": "application/json",
    accept: "application/json",
    // On the header too, so a handler can log or rate-limit without parsing the body.
    "x-smoltest-run": identity.runId,
  };
  const secret = env[SEED_SECRET_VAR];
  if (secret) headers.authorization = `Bearer ${secret}`;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  timer.unref?.();
  const started = Date.now();
  try {
    const res = await fetchImpl(endpoint, { method: "POST", headers, body: JSON.stringify(body), signal: controller.signal });
    const text = await res.text().catch(() => "");
    if (!res.ok) {
      return { ok: false, status: res.status, values: null, ms: Date.now() - started, detail: `it answered ${res.status}${text.trim() ? `: ${text.trim().slice(0, 200)}` : ""}` };
    }
    // An empty 200 is a legitimate answer from a handler that only has side effects to perform.
    if (!text.trim()) return { ok: true, status: res.status, values: {}, ms: Date.now() - started, detail: "" };
    let raw;
    try {
      raw = JSON.parse(text);
    } catch (e) {
      return { ok: false, status: res.status, values: null, ms: Date.now() - started, detail: `it answered ${res.status} but the body is not JSON (${String(e && e.message ? e.message : e).split("\n")[0]}). The first bytes were ${JSON.stringify(text.slice(0, 80))}.` };
    }
    const { values, problem } = readSeedValues(raw);
    if (problem) return { ok: false, status: res.status, values: null, ms: Date.now() - started, detail: problem };
    return { ok: true, status: res.status, values, ms: Date.now() - started, detail: "" };
  } catch (e) {
    const why = controller.signal.aborted
      ? `it did not answer in ${timeoutMs / 1000}s`
      : `it could not be reached: ${String(e && e.message ? e.message : e).split("\n")[0]}`;
    return { ok: false, values: null, ms: Date.now() - started, detail: why };
  } finally {
    clearTimeout(timer);
  }
}

/** The one sentence a seed failure is allowed to say. Named endpoint, named cause, named blame. */
export function seedProblem(endpoint, detail) {
  // The full stop is added here rather than in every caller: a detail is often somebody else's
  // error string ("fetch failed"), and "fetch failed Nothing was opened" reads as one broken
  // sentence in a CI log nobody can go back and re-punctuate.
  const said = String(detail || "it did not say why").trim().replace(/[.:;,]?$/, ".");
  return `The --seed endpoint ${endpoint} could not set up this test: ${said} Nothing was opened and nothing was tested, so this is not a verdict about your application — it is the state the sentence needs, which this run could not create.`;
}

/**
 * Everything --seed does, in the order it has to happen, for one test.
 *
 * Returns either { problem } — the caller reports `errored` and exits 2 — or the sentence with its
 * seeded placeholders MASKED (they are resolved at the moment the model is prompted and at the
 * moment of a keystroke, exactly like a password), plus the secret pairs that keep them out of
 * every artefact.
 */
export async function seedRun({ endpoint, identity, test, url = "", env = process.env, log = console.log, fetchImpl = fetch, timeoutMs = SEED_TIMEOUT_MS } = {}) {
  const wanted = seedTokensIn(test);
  log(C.dim(`seeding: POST ${endpoint}${wanted.length ? ` for ${wanted.map((k) => `{{${k}}}`).join(" ")}` : ""}`));

  const res = await postSeed({ endpoint, identity, test, url, env, fetchImpl, timeoutMs });
  if (!res.ok) return { problem: seedProblem(endpoint, res.detail), ms: res.ms || 0 };

  const values = res.values || {};
  const keys = Object.keys(values);
  // NAMES, NEVER VALUES. This line is printed to a terminal, a CI log and a run summary, and the
  // whole point of the masking below is that the value does not appear in any of them.
  log(C.dim(`  seeded ${keys.length} value${keys.length === 1 ? "" : "s"}${keys.length ? `: ${keys.join(", ")}` : " (the endpoint returned no placeholders)"} in ${((res.ms || 0) / 1000).toFixed(1)}s`));

  const applied = maskSeed(test, values);
  if (applied.missing.length) {
    // WITH --seed THIS IS A SETUP FAILURE, NOT A WARNING.
    //
    // Without --seed an unknown token is named and left in the sentence (safety.mjs says why). With
    // --seed the author has declared that the endpoint supplies these, so a token nobody filled
    // means the fixture the sentence names does not exist — and running anyway produces a `failed`
    // verdict about their application caused by our setup gap, which is the one thing this feature
    // is not allowed to do.
    const named = applied.missing.join(" ");
    return {
      problem: seedProblem(
        endpoint,
        `the sentence asks for ${named}, which it did not return. It returned ${keys.length ? keys.map((k) => `{{${k}}}`).join(" ") : "no placeholders at all"}, and this runner fills ${PLACEHOLDER_LIST} by itself.`,
      ),
      ms: res.ms || 0,
    };
  }

  const short = unmaskableKeys(values);
  if (short.length) {
    log(C.y(`  ${short.join(", ")} ${short.length === 1 ? "is" : "are"} under four characters, so ${short.length === 1 ? "it" : "they"} cannot be masked safely and will appear in the recording as written.`));
    log(C.dim("  Masking a value that short would rewrite every step that happens to contain it."));
  }
  if (applied.used.length) log(C.dim(`  the sentence uses ${applied.used.map((k) => `{{${k}}}`).join(" ")}`));

  return {
    // MASKED, deliberately: the sentence handed on carries {{orderid}}, and the value is resolved
    // only into the model prompt and into a keystroke. Everything that writes the sentence down —
    // the run summary, the recording, the pull request comment — writes the placeholder.
    text: applied.text,
    values,
    // guardPairs, not seedSecrets alone: a value is masked byte-for-byte, and a browser
    // percent-encodes it on the way into a URL. Expanded once, here, so every maskSecrets and
    // unmaskSecrets call site downstream covers both forms without knowing about either.
    secrets: guardPairs(seedSecrets(values)),
    ms: res.ms || 0,
  };
}

/** The help line, in one place, so the CLI and the README cannot drift apart on what it does. */
export const SEED_HELP = `--seed <url>     POST this run's identity there BEFORE the test, and use the JSON it returns as placeholders`;
