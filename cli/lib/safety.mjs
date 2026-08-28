// The agent drives a REAL browser against a REAL URL. Everything in this file exists because of
// what that means and nothing else.
//
// If the sentence says "sign up", a row appears in somebody's users table. If it says "check out",
// an order appears, and against a live payment key, a charge. Before this file there was nothing
// between a typo in a URL and a real order: no warning, no label on the data, no way to find it
// afterwards. The measured shape of the risk is not exotic — `--url https://myapp.com` is one
// character away from `--url https://staging.myapp.com`, and both run silently.
//
// The competing product solves this by making the customer build an isolated environment first: a
// GitHub App, a Dockerfile, a seed/teardown endpoint per run. That is the correct shape and it is
// also the reason their onboarding takes an hour before you learn anything. So this is the version
// that costs the customer nothing:
//
//   1. RUN IDENTITY   every run gets an obviously-synthetic, greppable, deletable identity, and the
//                     test author can write it straight into the sentence.
//   2. A WARNING      a URL with no staging/preview/localhost marker says what is about to happen,
//                     once, and asks — only where there is a human to answer. Never blocks CI.
//   3. TEARDOWN       `--teardown <url>` POSTs the identity to the customer's own endpoint after the
//                     run. Their Environment Factory in one flag, entirely optional.
//
// Nothing here may change a verdict. A test that passed still passed if the teardown endpoint is
// down; a tool that reddens a build over its own housekeeping gets removed the same day.

import readline from "node:readline/promises";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

// ---- 1. run identity --------------------------------------------------------------------------

/**
 * The prefix every generated value starts with. It is the whole point: one `LIKE 'smoltest%'`
 * finds every row any run of this tool has ever created, whether or not the teardown hook fired,
 * whether or not anyone remembered to configure one.
 */
export const PREFIX = "smoltest";

/**
 * example.com by default, and deliberately. It is reserved by RFC 2606 — it resolves nowhere and
 * accepts no mail, so a test signup can never deliver a "welcome!" to a real stranger's inbox.
 * Point --email-domain at a catch-all you own if your app rejects it, which some signup forms do.
 */
export const DEFAULT_DOMAIN = "example.com";

/** Lowercase, ASCII, no dots: safe in an email local part, a URL, a username field and a log line. */
function cleanId(raw) {
  return String(raw).toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
}

/**
 * A short id that sorts chronologically. base36 milliseconds first, then randomness: two runs in
 * the same millisecond still differ, and a human scanning a users table sees this run's rows
 * grouped together instead of scattered by a hash.
 */
export function newRunId(now = Date.now(), random = Math.random) {
  const rand = Math.floor(random() * 36 ** 4).toString(36).padStart(4, "0");
  return `${now.toString(36)}${rand}`;
}

/**
 * The identity for one run. Every field carries PREFIX and the same id, so a row found in any one
 * column leads to every other row the run made.
 *
 * The password satisfies the complexity rule almost every signup form enforces — upper, lower,
 * digit, symbol, over twelve characters — because a run that dies on "password must contain a
 * number" reports a failure about the app that is really a failure about this file.
 */
export function newIdentity({ domain, runId, now, random } = {}) {
  const id = cleanId(runId || newRunId(now, random)) || newRunId(now, random);
  const at = String(domain || DEFAULT_DOMAIN).trim().replace(/^@/, "") || DEFAULT_DOMAIN;
  return {
    runId: id,
    prefix: PREFIX,
    email: `${PREFIX}+${id}@${at}`,
    username: `${PREFIX}_${id}`,
    name: `Smoltest ${id}`,
    password: `Smoltest-${id}-9!`,
  };
}

/**
 * What an author may write in the sentence. `{{email}}`, `{{password}}`, `{{name}}`,
 * `{{username}}`, `{{runid}}` — braces because no English sentence contains them by accident, and
 * because every templating language a web developer has ever used spells it this way.
 */
export const PLACEHOLDERS = {
  email: (i) => i.email,
  password: (i) => i.password,
  name: (i) => i.name,
  username: (i) => i.username,
  runid: (i) => i.runId,
};

export const PLACEHOLDER_LIST = Object.keys(PLACEHOLDERS).map((k) => `{{${k}}}`).join(" ");

const TOKEN = /\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}/g;

/**
 * Replace the placeholders before the sentence reaches the model. Case and inner spaces are
 * forgiven: `{{ Email }}` is the same token as `{{email}}`.
 *
 * An unknown token is LEFT AS WRITTEN and reported, never dropped and never guessed at. A typo'd
 * `{{emial}}` that silently vanished would leave the model an empty email field to invent a value
 * for, which is exactly the untraceable row this whole file exists to prevent.
 */
export function substitute(text, identity) {
  const used = [];
  const unknown = [];
  const out = String(text ?? "").replace(TOKEN, (whole, key) => {
    const k = key.toLowerCase();
    // hasOwn, not `in`: `{{constructor}}` and `{{toString}}` are inherited properties of every
    // object literal, and `in` would happily "substitute" a function into somebody's test.
    if (!Object.hasOwn(PLACEHOLDERS, k)) {
      if (!unknown.includes(whole)) unknown.push(whole);
      return whole;
    }
    if (!used.includes(k)) used.push(k);
    return PLACEHOLDERS[k](identity);
  });
  return { text: out, used, unknown };
}

// ---- 2. the warning ---------------------------------------------------------------------------

/** Hostnames that are the local machine, whatever the port. */
const LOCAL_HOSTS = new Set(["localhost", "0.0.0.0", "::1", "[::1]", "::", "[::]"]);

/** Reserved and private-use suffixes. None of these is reachable from the public internet. */
const LOCAL_SUFFIXES = [".localhost", ".local", ".test", ".example", ".invalid", ".internal", ".home.arpa"];

/**
 * Default platform subdomains. A business that has customers puts its own domain in front of one
 * of these, so the bare host is a preview, a branch deploy or a hobby project — not the order book.
 * Deliberately does NOT include the general-purpose clouds (azurewebsites.net, herokuapp.com,
 * amplifyapp.com), where the platform hostname routinely IS the production site.
 */
const PREVIEW_SUFFIXES = [
  ".vercel.app", ".netlify.app", ".netlify.live", ".pages.dev", ".workers.dev", ".deno.dev",
  ".fly.dev", ".onrender.com", ".railway.app", ".up.railway.app", ".surge.sh",
  ".ngrok.io", ".ngrok.app", ".ngrok-free.app", ".loca.lt", ".trycloudflare.com",
  ".repl.co", ".replit.dev", ".gitpod.io", ".github.dev", ".codespaces.dev", ".e2b.dev",
];

/**
 * Words that mark a hostname as somewhere it is safe to break things. Matched as whole LABELS
 * (split on dots and hyphens), never as substrings: "dev" as a substring would call
 * developers.myapp.com a staging box, and "test" would do the same to testrail.com.
 *
 * "beta" is not here on purpose. beta.myapp.com is a product stage with real users in it.
 */
const SAFE_WORDS = new Set([
  "staging", "stage", "dev", "devel", "develop", "development", "test", "tests", "testing",
  "qa", "uat", "sandbox", "sbx", "preview", "pr", "review", "demo", "local", "canary", "e2e", "ci",
]);

const PRIVATE_IP = /^(10\.|127\.|192\.168\.|169\.254\.|172\.(1[6-9]|2\d|3[01])\.)/;

/**
 * Why this URL is NOT production, or "" when nothing says so.
 *
 * The default is to warn. A false positive costs one keystroke; a false negative costs somebody a
 * real order in a real database, which is the entire thing this is for.
 */
export function stagingMarker(url) {
  let host = "";
  try {
    host = new URL(String(url)).hostname.toLowerCase();
  } catch {
    // Not a URL we can read. We cannot judge it, and the run is about to fail at goto() with a
    // message that names the real problem, so say nothing rather than guess.
    return "the URL could not be read";
  }
  if (LOCAL_HOSTS.has(host) || LOCAL_SUFFIXES.some((s) => host.endsWith(s))) return `${host} is this machine`;
  if (PRIVATE_IP.test(host)) return `${host} is a private address`;
  if (host.startsWith("[fd") || host.startsWith("[fc")) return `${host} is a private address`;
  const preview = PREVIEW_SUFFIXES.find((s) => host.endsWith(s));
  if (preview) return `${preview} is a preview host`;
  const word = host.split(/[.-]/).find((label) => SAFE_WORDS.has(label));
  if (word) return `"${word}" is in the hostname`;
  return "";
}

/** True when nothing about the URL says "safe to break". */
export function looksProduction(url) {
  return stagingMarker(url) === "";
}

/**
 * What the person reads before a run against a production-looking URL.
 *
 * It names the URL, what the run can create, and the identity it will be created under — that last
 * part is the difference between "something in my orders table" and one search. It does not
 * threaten and it does not moralise: people test against production deliberately, and the tool's
 * job is to make sure nobody does it by accident.
 */
export function productionNotice(url, identity, { teardown = "" } = {}) {
  const lines = [
    "",
    C.y(`${url} has no staging, preview or localhost marker in it.`),
    "This drives a real browser and really uses the app. A sentence about signing up creates an",
    "account; a sentence about checking out can create an order, and on a live payment key, a charge.",
    "",
    `Anything it creates under its own identity is findable — every field starts with ${C.b(PREFIX)}:`,
    C.dim(`  email     ${identity.email}`),
    C.dim(`  username  ${identity.username}`),
    C.dim(`  password  ${identity.password}`),
    C.dim(`  Write ${PLACEHOLDER_LIST} in the sentence to make the agent use them.`),
    "",
  ];
  if (!teardown) lines.push(C.dim("  --teardown <url>  POST this identity to your own endpoint afterwards, and delete what it made."));
  lines.push(C.dim("  --yes             skip this question (CI is never asked)"));
  lines.push("");
  return lines.join("\n");
}

/**
 * Every environment variable that means "there is no person here". Checked BEFORE isTTY and
 * deliberately long: Buildkite, Jenkins with a pty, and plenty of self-hosted docker runners DO
 * hand a job a TTY, and a question asked there waits for an answer that is never coming — until
 * the job's timeout kills it an hour later. A safety feature that hangs builds gets deleted.
 */
const CI_VARS = [
  "CI", "CONTINUOUS_INTEGRATION", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "BUILDKITE",
  "TRAVIS", "APPVEYOR", "DRONE", "TEAMCITY_VERSION", "TF_BUILD", "JENKINS_URL", "HUDSON_URL",
  "BUILD_NUMBER", "CODEBUILD_BUILD_ID", "BITBUCKET_BUILD_NUMBER", "NETLIFY", "VERCEL", "RENDER",
];

/** A terminal with a person in front of it. Everything else is a log, and a log cannot answer. */
function interactive(env) {
  if (CI_VARS.some((v) => env[v])) return false;
  // BOTH ends. stdout being a pipe is the shape of `| tee build.log`, where the question is
  // written somewhere nobody is looking at while the run waits on it.
  return Boolean(process.stdin && process.stdin.isTTY && process.stdout && process.stdout.isTTY);
}

/**
 * Ask on the terminal, and never hang.
 *
 * The belt: if stdin closes — a pipe that ended, a parent process that went away — readline's
 * question promise simply never settles, and the run is wedged forever with no output. The close
 * listener turns that into an empty answer, which declines.
 */
async function askTty(question) {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    return String(
      await new Promise((resolve) => {
        rl.on("close", () => resolve(""));
        rl.question(question).then(resolve, () => resolve(""));
      }),
    );
  } finally {
    rl.close();
  }
}

/** One decision per origin per process, so a nine-test suite asks once, not nine times. */
const decided = new Map();

/**
 * Warn, and where there is a human, ask. Returns { proceed } — false ONLY when a person answered
 * and said no.
 *
 * The contract that matters most here: with no TTY this prints and returns proceed:true without
 * ever touching stdin. A CI job that blocks on a question nobody can see is a hung build, and a
 * hung build is how a safety feature gets deleted.
 */
export async function confirmProduction({
  url,
  identity,
  yes = false,
  teardown = "",
  log = console.log,
  env = process.env,
  ask = askTty,
  memo = decided,
} = {}) {
  const marker = stagingMarker(url);
  if (marker) return { proceed: true, warned: false, asked: false, marker };

  let origin = String(url);
  try {
    origin = new URL(String(url)).origin;
  } catch {
    /* stagingMarker already returned a marker for anything unparseable */
  }
  if (memo.has(origin)) return { proceed: memo.get(origin), warned: false, asked: false, memoized: true };

  log(productionNotice(url, identity, { teardown }));

  if (yes || !interactive(env)) {
    // Said, not asked. The point of the notice is that nobody learns about this from their orders
    // table afterwards; blocking was never the point.
    log(C.dim(yes ? "  --yes given, continuing." : "  no terminal to ask, continuing."));
    memo.set(origin, true);
    return { proceed: true, warned: true, asked: false };
  }

  const answer = (await ask(`  continue against ${url}? ${C.dim("[y/N] ")}`)).trim().toLowerCase();
  const proceed = answer === "y" || answer === "yes";
  memo.set(origin, proceed);
  return { proceed, warned: true, asked: true };
}

/** Tests and long-lived processes need the per-origin answers back. */
export function forgetConfirmations() {
  decided.clear();
}

// ---- 3. teardown ------------------------------------------------------------------------------

/**
 * POST the run identity to the customer's own endpoint so they can delete what the run created.
 *
 * This is the whole Environment Factory, inverted. Instead of "implement seed and teardown before
 * you may run your first test", the test runs, and then — if you want it — one flag hands you the
 * exact identity to clean up. Ten lines of handler, no vendor in your database.
 *
 * Fires whatever the verdict. A FAILED run is the one most likely to have left a half-made account
 * behind: it got as far as creating the row and then the app broke.
 *
 * Never throws and never changes an exit code. The body is a stable contract — adding a field is
 * fine, renaming one is not, because it is somebody's `if (body.email)`.
 */
export async function postTeardown({
  endpoint,
  identity,
  test = "",
  url = "",
  status = "",
  env = process.env,
  fetchImpl = fetch,
  timeoutMs = 10_000,
  at = () => new Date().toISOString(),
} = {}) {
  if (!endpoint) return { ok: false, skipped: true, detail: "no --teardown endpoint" };
  const body = {
    runId: identity.runId,
    prefix: identity.prefix,
    email: identity.email,
    username: identity.username,
    name: identity.name,
    password: identity.password,
    test,
    url,
    status,
    at: at(),
  };
  const headers = {
    "content-type": "application/json",
    // On the header too, so a handler can log or rate-limit without parsing the body.
    "x-smoltest-run": identity.runId,
  };
  // The secret is env-only on purpose. A --teardown-secret flag lands in shell history and in the
  // command line every CI runner prints at the top of the log.
  const secret = env.SMOLANALYTICS_TEARDOWN_SECRET;
  if (secret) headers.authorization = `Bearer ${secret}`;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  timer.unref?.();
  try {
    const res = await fetchImpl(endpoint, { method: "POST", headers, body: JSON.stringify(body), signal: controller.signal });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      return { ok: false, status: res.status, body, detail: `${endpoint} answered ${res.status}. ${text.slice(0, 200)}`.trim() };
    }
    return { ok: true, status: res.status, body, detail: `${endpoint} accepted ${identity.runId}` };
  } catch (e) {
    const why = controller.signal.aborted ? `no answer in ${timeoutMs / 1000}s` : String(e && e.message ? e.message : e).split("\n")[0];
    return { ok: false, body, detail: `${endpoint} could not be reached: ${why}` };
  } finally {
    clearTimeout(timer);
  }
}

/**
 * A SECRET MUST NEVER BE THE THING WE WRITE DOWN.
 *
 * MEASURED, and it was live in a published release: `compile()` stored a fill step's text verbatim,
 * so a login the agent had just performed produced
 *   {"kind":"fill","name":"Password","text":"SuperSecret-hunter2!"}
 * in `.smolanalytics/recordings/<test>.json` — a directory the shipped CI template CACHES with
 * actions/cache and that users are told to commit, because committing recordings is the point.
 * The same string went into the step label, which is printed to the terminal, posted in the pull
 * request comment, written to GITHUB_STEP_SUMMARY, and saved beside the failure evidence.
 *
 * So the value is masked back to the placeholder it came from at the moment of recording, and
 * resolved again from the environment at the moment of replay. The recording stays replayable and
 * carries no credential; a leaked recording leaks the SHAPE of the login and nothing else.
 *
 * Pairs are {value, token}. A value under 4 characters is refused: masking "a" would rewrite every
 * step that happens to contain it, and a recording corrupted by over-masking fails forever in a way
 * nobody can read.
 */
export function maskSecrets(text, pairs = []) {
  let out = String(text ?? "");
  for (const { value, token } of pairs) {
    if (typeof value !== "string" || value.length < 4 || !token) continue;
    out = out.split(value).join(token);
  }
  return out;
}

/** The inverse, at replay time: turn {{password}} back into the value the environment holds now.
 *  Unresolvable tokens are left exactly as they are — filling the literal text "{{password}}" and
 *  failing honestly beats filling an empty string and reporting that the app rejected a good login. */
export function unmaskSecrets(text, pairs = []) {
  let out = String(text ?? "");
  for (const { value, token } of pairs) {
    if (typeof value !== "string" || !value || !token) continue;
    out = out.split(token).join(value);
  }
  return out;
}
