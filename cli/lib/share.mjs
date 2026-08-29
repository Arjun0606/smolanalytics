// `--share` — the run gets an address.
//
// WHY THIS EXISTS. Our artefact is unusual in this field: the test is a sentence and the verdict is
// a sentence, so an account of an agent using someone's app is legible to a person who did not
// write the test. Every competing artefact — a Playwright trace, a video, a stack trace — needs a
// developer to interpret it. Ours does not. And yet ours dies in a terminal, or inside a private
// pull request that three people can open. The volume is already there; only the address is
// missing, and this file is the address.
//
// WHO OPENS THE LINK. An engineer. The person who installs this, wires CI, reads the failure and
// pays is an engineer, and the page is built for them: the steps, the proof text, the commit, the
// suspected files, the exact verdict, the exit code. The plain-English sentence at the top is what
// makes an engineer willing to SEND it to a designer or a founder; the depth underneath it is what
// makes it worth opening. A page that is only the sentence is a marketing card and fails at both.
//
// FOUR RULES, AND THEY ARE THE WHOLE DESIGN.
//
//   1. OPT-IN, ALWAYS. Nothing leaves this machine unless someone typed --share. A run without the
//      flag is byte-identical to the run before this file existed, including the JSON posted to a
//      project — see wireRun in lib/test.mjs, which strips the share record off the wire.
//
//   2. SHARING CANNOT CHANGE A VERDICT OR AN EXIT CODE. The bundle is assembled after the verdict
//      is decided, printed and posted, and the callers hold the exit code in a variable before the
//      POST is attempted. A share that fails says so, in one line, and the run is otherwise exactly
//      what it was. This is the same discipline as evidence: a decoration may never become a
//      verdict.
//
//   3. NOTHING SECRET TRAVELS. Replay.io's own post-mortem names the thing that killed their
//      bug-report phase: users "worried replays could contain sensitive data". A leak here is not a
//      bug we fix next week, it is the feature being uninstalled. So every string in the bundle
//      goes through three passes — the run's own masking pairs, the credentials this process can
//      see in its environment, and a pattern sweep for anything shaped like a bearer token — and
//      the walk is generic, so a field added to the bundle next month is covered without anyone
//      remembering to cover it.
//
//   4. THE FIVE STATUSES ARE NEVER BLURRED. Each test carries its own status verbatim, the counts
//      are separate, and the one summary field is the WORST status present with a documented
//      precedence. Nothing here rounds `flaky` up to a pass or `stale` down to a failure.
//
// WHAT IT CANNOT PROMISE, SAID OUT LOUD. The screenshot is a picture of the customer's own page
// showing the customer's own data. Text on it cannot be masked — it is pixels — exactly as
// captureEvidence in lib/test.mjs already documents for the file it writes next to it. The page
// TEXT that travels beside it is masked; the image is not, and the CLI says so when it attaches
// one.

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { maskSecrets, PREFIX } from "./safety.mjs";
import { urlForms } from "./seedguard.mjs";
import { loginCredentials, redact } from "./auth.mjs";

// ---- the wire contract ---------------------------------------------------------------------------

/**
 * The bundle's kind and version. A receiving half implements against these two fields FIRST: a
 * bundle whose `kind` it does not recognise is not a share, and a `schemaVersion` above what it
 * understands is a newer CLI talking to an older control plane — which must be a stated refusal,
 * never a silent partial render of a verdict.
 */
export const SHARE_KIND = "smolanalytics.share";
export const SHARE_SCHEMA_VERSION = 1;

/** Where the bundle is POSTed, appended to the control-plane base. */
export const SHARE_PATH = "/api/share";

/** The control plane, unless SMOLANALYTICS_URL names another one (a self-hosted instance, a test). */
export const DEFAULT_BASE = "https://smolanalytics.com";

// ---- caps -----------------------------------------------------------------------------------------
//
// Every one of these exists so that a single pathological run cannot make the bundle unpostable.
// The failure mode they prevent is specific and has happened elsewhere in this codebase: the pull
// request comment once grew to 140,053 characters on a 60-failure run and GitHub 422'd the WHOLE
// report, on exactly the run where the most had gone wrong. A cap that drops the tail is strictly
// better than a body that is refused.

const MAX_TESTS = 200;
const MAX_STEPS_PER_TEST = 120;
/**
 * A step's `detail`, capped HERE and not in agentSteps/replaySteps.
 *
 * MEASURED, and it was the pageText hole one function over. agentSteps used to clamp `detail` to
 * 400 characters at the moment the step was recorded — before any scrub had run. A credential
 * straddling character 400 lost its tail, so `redact`'s byte-for-byte substring match no longer
 * recognised it and the FIRST HALF of a live key went to a public page:
 *
 *   SMOLANALYTICS_SEED_SECRET=LEAKSEEDSECRET00000 in a step detail at offset 390
 *   -> "…zzzLEAKSEEDSE… (truncated)"   published, unredacted
 *
 * pickPageText's own comment already names this exact failure for the file it reads. The cap now
 * lives beside `reason` and `proof`, where `keep` scrubs first and clamps second.
 */
const MAX_STEP_DETAIL = 400;
const MAX_REASON = 4_000;
const MAX_PROOF = 500;
const MAX_PAGE_TEXT = 4_000;
const MAX_SUSPECTS = 3;
/** A full-page PNG larger than this is dropped and SAID to be dropped, never silently truncated. */
export const MAX_SCREENSHOT_BYTES = 1_500_000;

// ---- redaction --------------------------------------------------------------------------------

/**
 * Environment variables whose VALUE must never appear in anything we publish.
 *
 * Named explicitly rather than only pattern-matched, because these are the ones this runner itself
 * puts into play: the model key it calls with, the project keys it posts verdicts with, the login
 * it signs in with, the shared secrets it sends to a customer's seed and teardown endpoints as an
 * Authorization header, and the GitHub token it comments with.
 */
export const SECRET_VARS = [
  "ANTHROPIC_API_KEY",
  "SMOLANALYTICS_WRITE_KEY",
  "SMOLANALYTICS_KEY",
  "SMOLANALYTICS_RUN_KEY",
  "SMOLANALYTICS_MCP_KEY",
  "SMOLANALYTICS_SEED_SECRET",
  "SMOLANALYTICS_TEARDOWN_SECRET",
  "GITHUB_TOKEN",
  "GH_TOKEN",
];

/**
 * Anything whose NAME says it is a credential, whoever set it.
 *
 * The named list above cannot be complete: a customer's CI holds STRIPE_SECRET_KEY and
 * AWS_SECRET_ACCESS_KEY, their app prints them, and the agent quotes what it read. The floor is 8
 * characters here rather than 4 because this arm matches on a NAME the customer chose, so a short
 * value is more likely to be a word that also occurs in prose. Over-redaction costs a reader one
 * `[redacted]`; under-redaction costs a customer a credential.
 */
/**
 * Words that mean "credential" wherever they appear in the NAME, not only at its end.
 *
 * MEASURED, and this is why the end-anchored version it replaces was a leak. The docstring above
 * names STRIPE_SECRET_KEY as the motivating example and the old pattern did not match it: the name
 * ends in `_KEY`, and `KEY` was not one of the alternatives — only `API_KEY`, `PRIVATE_KEY` and
 * `ACCESS_KEY` were. So AWS_SECRET_ACCESS_KEY was covered by accident of its suffix while
 * STRIPE_SECRET_KEY, SUPABASE_SERVICE_ROLE_KEY and every `<VENDOR>_SECRET_KEY` in a customer's CI
 * went to a public page in full.
 */
const CREDENTIAL_WORDS = new Set([
  "TOKEN", "TOKENS", "SECRET", "SECRETS", "PASSWORD", "PASSWD", "PASS", "PWD",
  "CREDENTIAL", "CREDENTIALS", "APIKEY", "PRIVATEKEY", "SESSION", "COOKIE", "KEY", "KEYS",
]);

/**
 * Does this variable's NAME say it holds a credential?
 *
 * Segment-wise on `_` / `-` / `.`, so the word counts wherever it sits: STRIPE_SECRET_KEY,
 * SUPABASE_SERVICE_ROLE_KEY, MY_TOKEN_FOR_CI. Over-redaction is the deliberate direction of this
 * asymmetry — a reader loses one `[redacted]`, and the alternative is a customer losing a key.
 */
export function credentialName(name) {
  const parts = String(name || "").toUpperCase().split(/[^A-Z0-9]+/).filter(Boolean);
  return parts.some((w) => CREDENTIAL_WORDS.has(w));
}
const GENERIC_MIN = 8;
/** maskSecrets and redact both refuse anything shorter: masking "OK" rewrites the whole document. */
const NAMED_MIN = 4;

/**
 * Every credential value this process can see, longest first.
 *
 * Longest first matters: a fixture that returns both `sk-live-abc` and `sk-live-abcdef` would
 * otherwise redact the short one inside the long one and leave `[redacted]def` on the page, which
 * is a partial credential published as if it were safe.
 */
export function envSecrets(env = process.env) {
  const found = new Set();
  for (const name of SECRET_VARS) {
    const v = env[name];
    if (typeof v === "string" && v.length >= NAMED_MIN) found.add(v);
  }
  // The login pair, from the one function that owns what a login credential is.
  for (const s of loginCredentials(env).secrets) found.add(s);
  for (const [name, v] of Object.entries(env)) {
    if (typeof v !== "string" || v.length < GENERIC_MIN) continue;
    if (credentialName(name)) found.add(v);
  }
  // THE ENCODED FORM, for the reason lib/seedguard.mjs exists: the moment a value reaches a query
  // string the browser percent-encodes it, and `redact` is a byte-for-byte substring replace, so a
  // key holding + / or = — which is every base64 key ever issued — was invisible to it. The seeded
  // arm got this coverage; this arm did not, and a share page is a URL anybody can open.
  for (const v of [...found]) for (const e of urlForms(v)) if (e.length >= NAMED_MIN) found.add(e);
  return [...found].sort((a, b) => b.length - a.length);
}

/**
 * Token shapes, for the credential that is NOT in this process's environment.
 *
 * The agent reads the customer's page. A staging app that renders its own Authorization header into
 * a debug panel, a 401 body quoted back into a failure reason, a curl example in the app's own docs
 * — all of them put a live token in front of us, and none of them is in our env. These patterns are
 * the second net. They are deliberately narrow: each one requires a recognisable prefix or the
 * literal word `authorization`, so ordinary prose cannot trip them.
 */
const PATTERNS = [
  // authorization: Bearer xyz / authorization=xyz, in a header dump or a quoted request.
  [/(authorization\s*[:=]\s*)(?:bearer\s+|basic\s+|token\s+)?[^\s"',;)}\][]+/gi, "$1[redacted]"],
  // A bare bearer token anywhere else.
  [/\bBearer\s+[A-Za-z0-9._~+/=-]{8,}/g, "Bearer [redacted]"],
  // Anthropic, OpenAI-shaped, GitHub, and our own key prefixes.
  [/\bsk-ant-[A-Za-z0-9_-]{6,}/g, "[redacted]"],
  [/\bsk-[A-Za-z0-9]{16,}/g, "[redacted]"],
  [/\bgh[pousr]_[A-Za-z0-9]{16,}/g, "[redacted]"],
  [/\bgithub_pat_[A-Za-z0-9_]{16,}/g, "[redacted]"],
  [/\bsa_[A-Za-z0-9_-]{12,}/g, "[redacted]"],
  // A credential in a URL's userinfo — https://user:password@host, which a seeded fixture or a
  // basic-auth staging environment produces routinely.
  [/(:\/\/)[^/\s:@]+:[^/\s@]+@/g, "$1[redacted]@"],
  // A CREDENTIAL IN A QUERY STRING OR A FRAGMENT, which is the one this feature could not survive.
  //
  // `--url https://preview.vercel.app/?x-vercel-protection-bypass=<token>` is the documented way to
  // reach a protected Vercel preview, and before this line that token travelled three times over in
  // one bundle: the `url` field, every `goto` step label, and the `URL:` line captureEvidence writes
  // at the top of the page text. Publishing it hands a stranger the customer's protected deployment.
  // Same shape for `?access_token=`, `#id_token=` out of an implicit OAuth redirect, and a signed
  // magic link. The value is replaced and the NAME is kept, so the page still says which parameter
  // was carrying something rather than pretending the URL had no query at all.
  [/([?&#;])([^=&#;\s"'<>]{1,128})=([^&#;\s"'<>]+)/g, (whole, sep, name, value) => (credentialParam(name) ? `${sep}${name}=[redacted]` : whole)],
  // The same credential written as a bare assignment: a Cookie header quoted into a 401 body, a
  // storage-state blob in a step detail, `session=…` in an error the app rendered. Eight characters
  // minimum so `session=1` in ordinary prose survives.
  [/\b((?:access|refresh|id|auth|csrf|xsrf|session|api|bearer)[_-]?token|session(?:[_-]?id)?|jsessionid|phpsessid)(\s*=\s*)(?!\[redacted\])[^\s;,&"'<>]{8,}/gi, "$1$2[redacted]"],
  [/(set-cookie\s*:\s*)[^\n\r]+/gi, "$1[redacted]"],
  // The other header names a debug panel or a quoted curl puts a live key behind. Hyphenated and
  // colon-or-equals, so `x-api-key: …` is caught and the English sentence "your API key is …" —
  // which has a space and no colon, and is a page saying something rather than a header dumping
  // something — is not.
  [/\b((?:x-)?(?:api-key|apikey|auth-token|access-token|csrf-token|session-token|refresh-token)\s*[:=]\s*)[^\s"',;)}\][]+/gi, "$1[redacted]"],
  // THE SAME COOKIE, ONE JSON LEVEL DEEPER, which is where the arm above was MEASURED missing it.
  //
  // The arm above needs a literal `"`. Storage state almost never arrives that way: it arrives
  // inside a string that has already been JSON-encoded once — a runner error that quotes the
  // conversation, an app's debug panel printing a serialised session, a 500 body echoed into a
  // failure reason. Then every quote is `\"` and `"name"` simply does not occur. Measured with the
  // real binary against a real Chromium, on a run that errored:
  //
  //   reason: …storageState: {\"cookies\":[{\"name\":\"session\",\"value\":\"LEAKSESSIONCOOKIE000\"…
  //
  // A live session cookie, in full, on a page anyone with the link can open.
  //
  // So this arm is escape-agnostic by construction: a session-shaped WORD, then the word `value`
  // within a few non-alphanumeric characters of it, then the value. Quotes, backslashes, colons and
  // commas are all just separators, so it reads the escaped form, the plain form and a debug panel
  // that prints `session value abc12345` with equal indifference.
  [/((?:session|sessionid|sid|jwt|csrf|xsrf|auth|token|secret|password|api[_-]?key)[^A-Za-z0-9]{1,12}value[^A-Za-z0-9]{1,12})(?!redacted\])([A-Za-z0-9._~+/=-]{8,})/gi, "$1[redacted]"],
  // The same cookie in Playwright's exact storageState shape, kept AFTER the arm above and not
  // before it. Order matters and was measured: run precise-first and the generic arm then matches
  // its own output's `[redacted]` and publishes `"value": "[[redacted]]"`, which is not idempotent
  // and reads as a bug. Generic-first leaves this arm a no-op on anything already redacted.
  // Deliberately narrow — the session-shaped NAME has to be right there beside the value, so
  // `"value": …` on its own is never touched.
  [/("name"\s*:\s*"(?:session|sid|jwt|token|auth|csrf|xsrf)[^"]*"\s*,\s*"value"\s*:\s*)"[^"]*"/gi, '$1"[redacted]"'],
  // WHATEVER WAS TYPED INTO A PASSWORD FIELD. The step label names the field the agent filled, so
  // when that field is a password the value beside it is a password — whoever's it is. This is the
  // arm that covers a credential nobody told us about: an author who wrote the literal password
  // into the sentence instead of using {{password}} or SMOLANALYTICS_LOGIN_PASSWORD. Both label
  // spellings this codebase produces are covered: describe()'s `fill "Password" = "…"` and
  // replayStepLabel's `fill textbox "Password" with "…"`.
  [/\b(fill\s+[^\n]{0,120}?pass(?:word|code|phrase)?[^\n]{0,60}?(?:with|=)\s*)"(?:[^"\\\n]|\\.)*"/gi, '$1"[redacted]"'],
  // THIS RUN'S OWN LOGIN. newIdentity() mints `Smoltest-<runid>-9!` and substitute() puts the VALUE
  // into the sentence before the model ever sees it, so the bundle's `sentence` and every `fill`
  // step label carried a working password for an account that now exists in the customer's
  // database. It is synthetic and greppable by design; it is still a credential, and a share page
  // is public. Rendered as the placeholder the author wrote, which reads better than the value did.
  [new RegExp(`\\b${PREFIX}-[a-z0-9-]{1,40}-9!`, "gi"), "{{password}}"],
  // A LITERAL CREDENTIAL WRITTEN INTO PROSE, and specifically into the TEST SENTENCE.
  //
  // MEASURED end to end, and it is the one channel the fill-label arm above only half covers. That
  // arm's own docstring names the case: "an author who wrote the literal password into the sentence
  // instead of using {{password}}". It catches the moment the agent TYPES that password. It does
  // not catch the sentence the author WROTE it in, and the sentence is the loudest string on the
  // page — it is the headline, the test name and the `sentence` field, three times over:
  //
  //   --test "open order {{orderId}} and sign in with the password LEAKTYPEDPW000000"
  //   -> headline: Failed: "…sign in with the password LEAKTYPEDPW000000"
  //
  // The sentence is also the artefact, so this arm is the one place in the file where over-redaction
  // is NOT free: turning "sign in with the password from the vault" into "[redacted]" would delete
  // the reason anybody opens the page. So the value has to LOOK like a credential (see
  // credentialLiteral), and ordinary prose after the word survives untouched. Last in the list, so
  // this run's own minted password has already become {{password}} and is not re-redacted.
  [/\b(pass(?:word|code|phrase)|secret|api[ _-]?key|token)(\s*[:=]\s*|\s+(?:is|are|was|of|to)\s+|\s+)("?)([^\s"'<>,;]{6,64})\3/gi,
    (whole, word, mid, quote, value) => (credentialLiteral(value) ? `${word}${mid}${quote}[redacted]${quote}` : whole)],
];

/**
 * Does this word, written after "password" in a sentence, look like a credential rather than English?
 *
 * The asymmetry in this file runs the other way everywhere else — over-redact, because a reader
 * loses a `[redacted]` and a customer loses a key. Here the string being judged is the test
 * sentence, which IS the page, so the default is to LEAVE IT ALONE and only redact what could not
 * be prose: a placeholder is never a secret, a plain word is never a secret, and everything else
 * has to carry a digit or a symbol or an interior capital before it is treated as one.
 */
export function credentialLiteral(value) {
  const v = String(value || "");
  if (v.length < 6 || v.length > 64) return false;
  if (/^\{\{.*\}\}$/.test(v)) return false;
  if (/^\[redacted\]$/i.test(v)) return false;
  // ONE SHAPE TEST, not a word list. "manager", "requirements", "handshake", "Santa" — every plain
  // English word that follows "password" in a sentence has no digit, no symbol and no interior
  // capital, and every credential anybody has ever typed has at least one of the three. An earlier
  // version also carried an explicit `^[A-Za-z][a-z]*$` word guard; it was removed because breaking
  // it changed no answer, and a line no mutation can reach is a line that only looks like a guard.
  return /[0-9]/.test(v) || /[^A-Za-z0-9]/.test(v) || /[a-z][A-Z]/.test(v);
}

/**
 * ANSI escape sequences, removed before anything else looks at the string.
 *
 * TWO REASONS, and the second is the one that matters. The first is that step labels are built by
 * lib/test.mjs's describe(), which colours them, so `steps[].do` arrived on the wire as
 * `fill "Password" = "[redacted]"<ESC>[2m 468ms<ESC>[0m` — raw control bytes in a JSON body, and
 * visible garbage on the page that renders it.
 *
 * The second is redaction. `redact` is a byte-for-byte substring match, so a colour code sitting in
 * the middle of a value splits it and the match silently stops happening. Stripping first means the
 * three passes below see the string a person would read.
 */
const ANSI = new RegExp("\\u001b\\[[0-9;]*[A-Za-z]", "g");
const BARE_ESC = new RegExp("\\u001b", "g");
export const stripAnsi = (text) => String(text ?? "").replace(ANSI, "").replace(BARE_ESC, "");

/**
 * Is this query parameter's name the name of a credential?
 *
 * Two arms, because parameter names are written both ways. A STRONG word counts as a substring —
 * `accessToken`, `bearertoken`, `x-vercel-protection-bypass` — since those letters do not occur
 * inside innocent parameter names. A WEAK word must be a whole `-`/`_`/`.`-delimited segment, so
 * `?key=` and `?auth=` are caught while `?author=`, `?monkey=` and `?design=` are left alone.
 */
const STRONG_PARAM = /(token|secret|passwd|password|apikey|credential|signature|bypass|jwt|sessionid)/;
const WEAK_PARAM = new Set(["key", "keys", "pass", "pwd", "auth", "authorization", "sig", "session", "sid", "otp", "cookie"]);
export function credentialParam(name) {
  const n = String(name || "").toLowerCase();
  if (STRONG_PARAM.test(n)) return true;
  return n.split(/[^a-z0-9]+/).filter(Boolean).some((w) => WEAK_PARAM.has(w));
}

export function scrubPatterns(text) {
  let out = String(text ?? "");
  for (const [re, to] of PATTERNS) out = out.replace(re, to);
  return out;
}

/**
 * One string, all three passes, in the only order that is safe.
 *
 * Seeded values first, because those have a MEANINGFUL replacement — `{{orderid}}` keeps the page
 * readable and keeps the recording's own vocabulary — and turning them into `[redacted]` first
 * would throw that away. Environment credentials second. Patterns last, so they see whatever the
 * first two left behind.
 */
export function scrub(text, { secrets = [], env = process.env, envValues } = {}) {
  const values = envValues || envSecrets(env);
  // `secrets` ARRIVES IN TWO SHAPES, AND ONE OF THEM USED TO BE SILENTLY IGNORED.
  //
  // maskSecrets takes {value, token} pairs — that is how a recording keeps a password replayable,
  // by writing {{password}} where the value was. But most callers here hold plain strings: the
  // values --seed returned, a token an app echoed back. Passing those straight to maskSecrets
  // destructures `value` off a string, gets undefined, and hits its `continue`. No error, no
  // warning, nothing masked.
  //
  // Measured: a seeded token passed in `secrets` survived into the serialised bundle, while the
  // password, the API key and the run key beside it were scrubbed — because those three come from
  // the environment and travel the `values` path instead. Sixty-one tests passed over it, all of
  // them supplying env-backed secrets.
  //
  // So the two shapes are separated here rather than trusted to line up: pairs keep their tokens,
  // bare strings go through redact, which is what actually takes strings.
  const pairs = secrets.filter((s) => s && typeof s === "object" && typeof s.value === "string");
  const bare = secrets.filter((s) => typeof s === "string" && s.length >= 4);
  const masked = maskSecrets(stripAnsi(text), pairs);
  return scrubPatterns(redactFold(redact(redact(masked, bare), values), [...values, ...bare]));
}

/**
 * The environment pass again, ignoring case.
 *
 * `redact` is byte-for-byte on purpose and stays that way — it is the function the recording, the
 * terminal and the pull request comment all use, and a case-insensitive mask there would rewrite
 * text that has to round-trip. But a share page is a URL a stranger can open, and the value on it
 * has been through somebody else's app first. Apps lowercase things constantly: an email is
 * lowercased on the way into a database and echoed back lowercased, a host is lowercased by the
 * URL parser, a token is upper-cased into a header name. SMOLANALYTICS_LOGIN_EMAIL is one of the
 * values in this list, so this is not a hypothetical.
 *
 * Exact first, folded second, so the common case is untouched and this only ever redacts MORE.
 *
 * SKIPPED WHEN CASE-FOLDING CHANGES A LENGTH. `"İ".toLowerCase()` is two code units, and this walk
 * indexes the original string by offsets found in the folded one. Rather than mis-slice a bundle,
 * a string or a secret whose folded length differs is left to the exact pass above.
 */
export function redactFold(text, values = []) {
  let out = String(text ?? "");
  for (const s of values) {
    if (typeof s !== "string" || s.length < NAMED_MIN) continue;
    const needle = s.toLowerCase();
    if (needle.length !== s.length) continue;
    const hay = out.toLowerCase();
    if (hay.length !== out.length) continue;
    let i = hay.indexOf(needle);
    if (i < 0) continue;
    let built = "";
    let from = 0;
    while (i >= 0) {
      built += out.slice(from, i) + "[redacted]";
      from = i + s.length;
      i = hay.indexOf(needle, from);
    }
    out = built + out.slice(from);
  }
  return out;
}

/**
 * The same, over a whole structure.
 *
 * GENERIC ON PURPOSE. The alternative — scrubbing each field by name as the bundle is built — is
 * how a leak ships: somebody adds `pageTitle` in six months, forgets the call, and no test fails
 * because no test knows the field exists. Walking the object means a new field is covered the day
 * it is added.
 *
 * KEYS ARE LEFT ALONE, and this is a correction, not an omission. Scrubbing them was "cheap
 * defence in depth" and it was measured doing the one thing this feature may never do — blurring a
 * verdict. Every key in this bundle is a literal written in buildBundle, agentSteps or replaySteps;
 * none of them can be a customer string, so scrubbing them protects nothing. What it DID do is
 * this, with SMOLANALYTICS_LOGIN_PASSWORD set to an ordinary English word:
 *
 *   password "status"  -> the per-test `status` key became `[redacted]`; the page has no verdict
 *   password "verdict" -> the run's own `verdict` key disappeared
 *   password "exitCode"-> the exit code disappeared
 *
 * A four-character floor cannot save this: `pass`, `mode`, `file`, `name`, `steps` and `proof` are
 * all real passwords somebody has typed. The five statuses are never blurred, so keys pass through.
 */
export function scrubDeep(value, opts = {}) {
  const o = { ...opts, envValues: opts.envValues || envSecrets(opts.env || process.env) };
  const walk = (v) => {
    if (typeof v === "string") return scrub(v, o);
    if (Array.isArray(v)) return v.map(walk);
    if (v && typeof v === "object") {
      const out = {};
      for (const [k, val] of Object.entries(v)) out[k] = walk(val);
      return out;
    }
    return v;
  };
  return walk(value);
}

// ---- where the run happened ---------------------------------------------------------------------

/**
 * Which pull request we are on. Actions states this three different ways depending on the event.
 *
 * Lives here rather than in lib/suite.mjs — where it used to — because lib/test.mjs needs it too
 * for a single-test share, and lib/test.mjs cannot import lib/suite.mjs: suite.mjs imports test.mjs,
 * and completing that cycle is how ESM hands one of the two a half-initialised module. suite.mjs
 * re-exports it, so every existing caller is untouched.
 */
export function prNumber(env, readFile = (p) => readFileSync(p, "utf8")) {
  if (env.GITHUB_EVENT_PATH) {
    try {
      const ev = JSON.parse(readFile(env.GITHUB_EVENT_PATH));
      const n = ev?.pull_request?.number ?? ev?.issue?.number;
      if (n) return Number(n);
    } catch {
      /* fall through to the ref */
    }
  }
  const m = /refs\/pull\/(\d+)\//.exec(env.GITHUB_REF || "");
  if (m) return Number(m[1]);
  if (/^\d+$/.test(env.PR_NUMBER || "")) return Number(env.PR_NUMBER);
  return 0;
}

/** The head commit, not the merge commit. On a pull_request event GITHUB_SHA is the merge commit
 *  Actions invented, which exists in nobody's history and cannot be looked up on GitHub. */
function headSha(env, readFile) {
  if (env.GITHUB_EVENT_PATH) {
    try {
      const ev = JSON.parse(readFile(env.GITHUB_EVENT_PATH));
      const sha = ev?.pull_request?.head?.sha;
      if (typeof sha === "string" && sha.length >= 7) return sha;
    } catch {
      /* fall through */
    }
  }
  return "";
}

/** git, asked quietly. Every failure — no git, no repo, a detached worktree — is "" and no note. */
function git(args, cwd) {
  try {
    const r = spawnSync("git", args, { cwd, encoding: "utf8", timeout: 3_000 });
    if (r.status !== 0) return "";
    return String(r.stdout || "").trim();
  } catch {
    return "";
  }
}

/**
 * Commit, branch, repository, pull request, and the CI run this came from.
 *
 * The commit is the field that makes the page worth opening twice: "this failed on 4f1c2ab" is
 * checkable, and "this failed" is not. It is looked up from the environment first because that is
 * authoritative inside CI, and from git second because a laptop run has a commit too.
 */
export function ciContext({ env = process.env, cwd = process.cwd(), readFile = (p) => readFileSync(p, "utf8"), runGit = git } = {}) {
  const actions = Boolean(env.GITHUB_ACTIONS);
  const commit =
    headSha(env, readFile) ||
    env.GITHUB_SHA ||
    env.VERCEL_GIT_COMMIT_SHA ||
    env.CI_COMMIT_SHA ||
    runGit(["rev-parse", "HEAD"], cwd) ||
    "";
  const branch =
    env.GITHUB_HEAD_REF ||
    env.GITHUB_REF_NAME ||
    env.VERCEL_GIT_COMMIT_REF ||
    env.CI_COMMIT_REF_NAME ||
    runGit(["rev-parse", "--abbrev-ref", "HEAD"], cwd) ||
    "";
  const repo = env.GITHUB_REPOSITORY || "";
  const pr = prNumber(env, readFile);
  const runUrl =
    actions && env.GITHUB_RUN_ID && repo
      ? `${(env.GITHUB_SERVER_URL || "https://github.com").replace(/\/+$/, "")}/${repo}/actions/runs/${env.GITHUB_RUN_ID}`
      : "";
  const ci = {
    provider: actions ? "github-actions" : env.CI ? "ci" : "local",
    repo,
    commit,
    shortCommit: commit ? commit.slice(0, 7) : "",
    branch,
    pr: pr || 0,
    runUrl,
  };
  // Nothing at all is null, not an object of empty strings: a receiving half rendering "commit:"
  // with nothing after it is worse than rendering no commit line.
  return commit || repo || branch || pr || runUrl ? ci : null;
}

// ---- steps ----------------------------------------------------------------------------------------

const clamp = (s, n) => {
  const t = String(s ?? "").trim();
  return t.length <= n ? t : `${t.slice(0, n).trimEnd()}… (truncated)`;
};

/**
 * The agent's steps, in the shape the page renders and the runs API already uses.
 *
 * `do` rather than `label` because that is the key lib/test.mjs already puts on the wire for a
 * failing run's steps, and two names for one thing across two payloads is a bug waiting for a
 * reader. `run` appears only when there was more than one attempt, for the same reason it does
 * there: a lone `run: 1` implies a second run that never happened.
 */
export function agentSteps(steps = [], { run = 0 } = {}) {
  return (Array.isArray(steps) ? steps : []).slice(0, MAX_STEPS_PER_TEST).map((s) => ({
    ...(run ? { run } : {}),
    n: s.n,
    do: String(s.label ?? s.do ?? ""),
    why: String(s.why ?? s.action?.why ?? ""),
    ok: s.ok !== false,
    // NOT CLAMPED HERE. See MAX_STEP_DETAIL: this function runs long before any scrub does, and a
    // credential cut in half by a cap is a credential the scrub can no longer recognise.
    detail: String(s.detail || ""),
    ms: Number(s.ms) || 0,
  }));
}

/** One recorded step, described the way the terminal describes an agent's. */
export function replayStepLabel(s) {
  if (!s || typeof s !== "object") return "";
  if (s.kind === "goto") return `goto ${s.url}`;
  if (s.kind === "press") return `press ${s.key}`;
  if (s.kind === "upload") return `upload to ${s.role} ${JSON.stringify(String(s.name ?? ""))}`;
  if (s.kind === "fill") return `fill ${s.role} ${JSON.stringify(String(s.name ?? ""))} with ${JSON.stringify(String(s.text ?? ""))}`;
  return `${s.kind} ${s.role} ${JSON.stringify(String(s.name ?? ""))}`;
}

/**
 * A replayed run's steps.
 *
 * `failedAt` is the index that broke on a stale replay, and everything after it is DROPPED rather
 * than rendered as unknown: those steps were never attempted, and a page that shows them greyed is
 * a page that invites someone to reason about a step nobody performed.
 */
export function replaySteps(planSteps = [], { failedAt = -1, detail = "" } = {}) {
  const all = Array.isArray(planSteps) ? planSteps : [];
  const upto = failedAt >= 0 ? all.slice(0, failedAt + 1) : all;
  return upto.slice(0, MAX_STEPS_PER_TEST).map((s, i) => ({
    n: i + 1,
    do: replayStepLabel(s),
    why: "",
    ok: !(failedAt >= 0 && i === failedAt),
    // Uncapped for the same reason agentSteps is: MAX_STEP_DETAIL caps it after the scrub.
    detail: failedAt >= 0 && i === failedAt ? String(detail ?? "") : "",
    ms: 0,
  }));
}

// ---- the bundle -----------------------------------------------------------------------------------

/** Which single status a whole run reports at the top. Worst first, and nothing is rounded. */
const PRECEDENCE = ["failed", "errored", "stale", "flaky", "passed"];

export function overallVerdict(statuses = []) {
  for (const s of PRECEDENCE) if (statuses.includes(s)) return s;
  return statuses[0] || "errored";
}

const WORD = { passed: "Passed", failed: "Failed", flaky: "Flaky", stale: "Stale", errored: "Could not run" };

const hostOf = (u) => {
  try {
    return new URL(String(u)).host;
  } catch {
    return String(u || "");
  }
};

/**
 * The sentence at the top of the page.
 *
 * One test: its own sentence, with its own status in front of it, because that IS the artefact —
 * the reason this is worth sending to somebody who cannot read a stack trace. Many tests: the count
 * per status, which is the only honest single line about a suite. Neither one invents a claim: no
 * "all good", no "everything works", nothing that a `stale` or a `flaky` would make false.
 */
export function headline(tests = [], url = "") {
  if (tests.length === 1) {
    const t = tests[0];
    return `${WORD[t.status] || t.status}: "${t.sentence}"`;
  }
  const counts = PRECEDENCE.map((s) => [s, tests.filter((t) => t.status === s).length]).filter(([, n]) => n > 0);
  const order = { passed: 0, failed: 1, flaky: 2, stale: 3, errored: 4 };
  counts.sort((a, b) => order[a[0]] - order[b[0]]);
  const label = { passed: "passed", failed: "failed", flaky: "flaky", stale: "stale", errored: "could not run" };
  const where = hostOf(url);
  return `${tests.length} checks${where ? ` against ${where}` : ""}: ${counts.map(([s, n]) => `${n} ${label[s]}`).join(", ")}`;
}

function cliVersion() {
  try {
    return createRequire(import.meta.url)("../package.json").version || "";
  } catch {
    return "";
  }
}

/**
 * One failure's screenshot, base64, or null.
 *
 * ONE, across the whole run, and the first failure in suite order — not the largest, not the last.
 * A page with twelve screenshots is a gallery nobody scrolls, and the argument a share page has to
 * make is made by one picture of the moment the reason describes.
 *
 * A read that fails is null and a note, never a throw: the evidence file may have been cleaned up,
 * the disk may be full, and a share is a decoration on a verdict that is already decided.
 */
export function pickScreenshot(tests = [], { readFileImpl = readFileSync, maxBytes = MAX_SCREENSHOT_BYTES } = {}) {
  for (const t of tests) {
    if (t.status !== "failed" && t.status !== "flaky") continue;
    const png = t.evidence && t.evidence.png;
    if (!png) continue;
    try {
      const buf = readFileImpl(png);
      if (!buf || !buf.length) continue;
      if (buf.length > maxBytes) {
        return { test: t.name, contentType: "image/png", base64: "", bytes: buf.length, note: `the screenshot was ${buf.length} bytes, over the ${maxBytes}-byte limit, so it was not attached` };
      }
      return { test: t.name, contentType: "image/png", base64: Buffer.from(buf).toString("base64"), bytes: buf.length, note: "" };
    } catch {
      /* the next failure may still have one */
    }
  }
  return null;
}

/** The page text captured beside that same screenshot. Masked here, and it was masked when it was
 *  written — twice, because this one is the copy that becomes a URL anybody can open. */
export function pickPageText(tests = [], screenshot, { readFileImpl = readFileSync, scrubText = (x) => x } = {}) {
  if (!screenshot) return null;
  const t = tests.find((x) => x.name === screenshot.test);
  const txt = t && t.evidence && t.evidence.txt;
  if (!txt) return null;
  try {
    // SCRUBBED FIRST, CAPPED SECOND, and the order is the whole point. Capping first cuts the file
    // at character 4,000, and a credential that straddles that boundary loses its tail — so the
    // scrub that runs afterwards no longer recognises it and the FIRST HALF of a live key is
    // published as if it were safe. This file's own comment calls that out for the sorted-secrets
    // case; the cap had the same hole.
    return { test: t.name, text: clamp(scrubText(readFileImpl(txt, "utf8")), MAX_PAGE_TEXT) };
  } catch {
    return null;
  }
}

/**
 * Assemble and redact.
 *
 * The screenshot's base64 is attached AFTER the scrub and is deliberately not walked. It is not
 * text and it cannot contain a plaintext credential; running a substitution over it could only
 * corrupt an image, and a corrupt image on a share page reads as a broken product.
 */
export function buildBundle({
  tests = [],
  url = "",
  engine = "",
  suite = "",
  exitCode = null,
  projectId = "",
  env = process.env,
  secrets = [],
  now = () => new Date(),
  ci = undefined,
  readFileImpl = readFileSync,
  version = cliVersion(),
} = {}) {
  const kept = tests.slice(0, MAX_TESTS);
  // Computed once and threaded, so that the strings with a CAP on them are scrubbed BEFORE the cap
  // rather than after it. scrubDeep still runs over the finished body — the two passes are
  // idempotent — but a secret that straddles a cap has to be caught while it is still whole.
  const envValues = envSecrets(env);
  const keep = (v, n) => clamp(scrub(v, { secrets, env, envValues }), n);
  const statuses = kept.map((t) => t.status);
  const count = (s) => statuses.filter((x) => x === s).length;
  const modes = [...new Set(kept.map((t) => t.mode).filter(Boolean))];

  const body = {
    kind: SHARE_KIND,
    schemaVersion: SHARE_SCHEMA_VERSION,
    cli: version,
    createdAt: now().toISOString(),
    headline: headline(kept, url),
    verdict: overallVerdict(statuses),
    exitCode: exitCode === null || exitCode === undefined ? null : Number(exitCode),
    url,
    suite,
    engine,
    // "mixed" only when a suite genuinely used both: a replayed recording and an agent run are
    // different amounts of money and different amounts of evidence, and the reader is owed which.
    mode: modes.length === 1 ? modes[0] : modes.length ? "mixed" : "",
    summary: {
      total: kept.length,
      passed: count("passed"),
      failed: count("failed"),
      flaky: count("flaky"),
      stale: count("stale"),
      errored: count("errored"),
      durationMs: kept.reduce((a, t) => a + (Number(t.durationMs) || 0), 0),
      truncated: tests.length > kept.length ? tests.length - kept.length : 0,
    },
    ci: ci === undefined ? ciContext({ env }) : ci,
    projectId: projectId || "",
    tests: kept.map((t) => ({
      name: String(t.name ?? ""),
      file: String(t.file ?? ""),
      sentence: String(t.sentence ?? ""),
      status: String(t.status ?? ""),
      mode: String(t.mode ?? ""),
      reason: keep(t.reason, MAX_REASON),
      proof: keep(t.proof, MAX_PROOF),
      durationMs: Number(t.durationMs) || 0,
      // The cap on `detail` lives here and not in agentSteps/replaySteps, so it lands AFTER the
      // scrub — the same order `reason` and `proof` get from `keep`. See MAX_STEP_DETAIL.
      steps: (Array.isArray(t.steps) ? t.steps : []).slice(0, MAX_STEPS_PER_TEST).map((s) =>
        (s && typeof s === "object" ? { ...s, detail: keep(s.detail, MAX_STEP_DETAIL) } : s)),
      suspects: (Array.isArray(t.suspects) ? t.suspects : []).slice(0, MAX_SUSPECTS).map((s) => ({
        file: String(s.file ?? ""),
        evidence: keep(s.evidence, 400),
      })),
    })),
  };

  const shot = pickScreenshot(kept, { readFileImpl });
  const pageText = pickPageText(kept, shot, { readFileImpl, scrubText: (x) => scrub(x, { secrets, env, envValues }) });
  const scrubbed = scrubDeep({ ...body, pageText }, { secrets, env, envValues });
  // THE VERDICT FIELDS GO AROUND THE SCRUB, not through it — the same rule as the screenshot's
  // pixels below, for a stronger reason. These fields are a closed vocabulary this file chose:
  // five statuses, three engines, two modes, one kind. None of them can carry a customer's secret,
  // and putting them through a substring replace means a credential that happens to spell an
  // English word rewrites the verdict. MEASURED, before this: SMOLANALYTICS_LOGIN_PASSWORD=failed
  // produced `"verdict": "[redacted]"` and `"status": "[redacted]"` on every test in the bundle —
  // a run whose page could no longer say what happened. Sharing may never change a verdict, and
  // erasing one is changing it.
  scrubbed.kind = body.kind;
  scrubbed.schemaVersion = body.schemaVersion;
  scrubbed.verdict = body.verdict;
  scrubbed.exitCode = body.exitCode;
  scrubbed.mode = body.mode;
  scrubbed.engine = body.engine;
  scrubbed.summary = body.summary;
  for (let i = 0; i < scrubbed.tests.length; i++) {
    scrubbed.tests[i].status = body.tests[i].status;
    scrubbed.tests[i].mode = body.tests[i].mode;
  }
  // THE HEADLINE IS BUILT AFTER THE SCRUB, NOT BEFORE IT, and this is the same rule as the two
  // blocks above rather than a new one. The headline is the only field that MIXES the closed
  // vocabulary with a customer string: "Failed: <the sentence>", "2 checks against host: 1 passed,
  // 1 failed". Sending it through the walk put the status words in reach of a substring replace.
  // MEASURED, before this:
  //
  //   SMOLANALYTICS_LOGIN_PASSWORD=Failed  -> headline: `[redacted]: "…"`
  //   SMOLANALYTICS_LOGIN_PASSWORD=failed  -> headline: `2 checks against x: 1 passed, 1 [redacted]`
  //
  // A page whose top line can no longer say what happened. Rebuilding it from `scrubbed.tests` —
  // whose sentences have been through every pass and whose statuses were just restored — keeps both
  // halves right: the customer's words are redacted, the verdict's words are not.
  scrubbed.headline = headline(scrubbed.tests, scrubbed.url);
  // Metadata through the scrub, pixels around it.
  scrubbed.screenshot = shot ? { ...scrubDeep({ test: shot.test, contentType: shot.contentType, note: shot.note }, { secrets, env }), bytes: shot.bytes, base64: shot.base64 } : null;
  return scrubbed;
}

// ---- the POST -------------------------------------------------------------------------------------

export const shareBase = (env = process.env) => String(env.SMOLANALYTICS_URL || DEFAULT_BASE).replace(/\/+$/, "");

/**
 * Publish it. NEVER THROWS, and the caller already holds the exit code.
 *
 * Anonymous by default, and that is the interesting case rather than the degenerate one: the person
 * trying us for the first time has no account, no project and no key, and they are exactly the
 * person whose link travels. A project key, when there is one, only ATTRIBUTES the share — it is
 * never required, and it goes in the header, never in the bundle.
 */
export async function postBundle({ bundle, env = process.env, fetchImpl = fetch, timeoutMs = 20_000 } = {}) {
  const base = shareBase(env);
  const endpoint = `${base}${SHARE_PATH}`;
  const key = env.SMOLANALYTICS_WRITE_KEY || env.SMOLANALYTICS_RUN_KEY || "";
  const headers = { "content-type": "application/json" };
  // Attribution, and only when there is a project to attribute to. An anonymous share sends no
  // Authorization header at all, so the control plane can tell the two apart without guessing.
  if (key && bundle && bundle.projectId) headers.authorization = `Bearer ${key}`;

  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), timeoutMs);
  try {
    const res = await fetchImpl(endpoint, { method: "POST", headers, body: JSON.stringify(bundle), signal: ac.signal });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      return { ok: false, url: "", id: "", problem: `${endpoint} returned ${res.status}${text ? ` ${String(text).slice(0, 200)}` : ""}` };
    }
    const json = await res.json().catch(() => null);
    const id = json && typeof json.id === "string" ? json.id : "";
    // The control plane owns the page's address. We print what it hands back, and only build one
    // ourselves from an id it gave us — inventing a URL shape here is how the CLI ends up printing
    // a 404 to somebody the day the route moves.
    const url = json && typeof json.url === "string" && json.url ? json.url : id ? `${base}/s/${id}` : "";
    if (!url) return { ok: false, url: "", id: "", problem: `${endpoint} accepted the run but returned no link` };
    return { ok: true, url, id, problem: "" };
  } catch (e) {
    const why = e && e.name === "AbortError" ? `no answer within ${Math.round(timeoutMs / 1000)}s` : e && e.message ? e.message : String(e);
    return { ok: false, url: "", id: "", problem: `could not reach ${endpoint} (${why})` };
  } finally {
    clearTimeout(timer);
  }
}

// ---- what the terminal says -----------------------------------------------------------------------

/**
 * The link, printed the way a Loom link is printed: on its own line, at column zero, with no colour
 * and no punctuation attached to it, so a double-click selects the whole URL and nothing else.
 *
 * A failure is one line that names what went wrong and says the verdict stands, which is the same
 * sentence report() already uses when a project POST fails. Silence is not an option: a person who
 * typed --share and got no link and no reason will assume the link is somewhere in the scrollback.
 */
export function shareLines(result, { screenshot = false, pageText = false } = {}) {
  if (result && result.ok) {
    return [
      "",
      "Shared this run. Anyone with the link can open it:",
      result.url,
      // BOTH halves of the evidence are named, because both travel and only one of them used to be
      // admitted to. The picture cannot be masked at all; the text is masked against every
      // credential this runner knows about, which is not the same promise as "masked".
      ...(screenshot ? ["the page above includes one screenshot of the failure — an image of your app, which cannot be masked the way the text is."] : []),
      ...(pageText ? ["it also includes the failing page's own visible text, with every credential this run knows about removed. Text your app renders that we have no way to recognise travels as written."] : []),
      "",
    ];
  }
  return [`  not shared: ${result && result.problem ? result.problem : "the run could not be published"}. The verdict above still stands.`];
}

/**
 * Build it, post it, print it. The one entry point both commands use.
 *
 * Returns nothing a caller can act on ON PURPOSE. There is no outcome of this function that any
 * caller is allowed to branch on: the exit code is decided, the verdict is printed, and the only
 * remaining question is whether a link or a reason appears at the bottom of the transcript.
 */
export async function publishShare({ log = console.log, fetchImpl = fetch, ...opts } = {}) {
  // THE WHOLE BODY IS INSIDE A CATCH, and that is not defensive clutter — it is rule 2 made
  // structural. Both callers invoke this after the exit code is fixed: testCmd from a `finally`,
  // where a rejection REPLACES the value the try was returning, and suiteCmd as its last await,
  // where a rejection reaches bin/smolanalytics.mjs's handler and becomes exit 2. So a throw here
  // does not merely lose a link, it converts a real `1` into a `2` — sharing changing an exit code,
  // which is the one thing this file may never do. buildBundle and postBundle are each guarded
  // below and postBundle never throws; this catches everything left, including a `log` that does.
  try {
    if (!Array.isArray(opts.tests) || !opts.tests.length) {
      log("  not shared: this run reached no verdict, so there is nothing to publish.");
      return null;
    }
    let bundle;
    try {
      bundle = buildBundle(opts);
    } catch (e) {
      // Assembling the bundle is our code reading our own files. If it throws, the run is still
      // exactly what it was and the reader is told why the link is missing.
      log(`  not shared: the run could not be assembled (${e && e.message ? e.message : e}). The verdict above still stands.`);
      return null;
    }
    const result = await postBundle({ bundle, env: opts.env, fetchImpl });
    for (const line of shareLines(result, { screenshot: Boolean(bundle.screenshot && bundle.screenshot.base64), pageText: Boolean(bundle.pageText && bundle.pageText.text) })) log(line);
    return result;
  } catch (e) {
    try {
      log(`  not shared: ${e && e.message ? e.message : e}. The verdict above still stands.`);
    } catch {
      /* the log is what threw; there is nowhere left to say it, and a verdict still outranks a link */
    }
    return null;
  }
}
