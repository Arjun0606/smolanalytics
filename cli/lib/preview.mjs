// Finding the pull request's own preview URL, so CI needs no --url at all.
//
// WHY THIS EXISTS. The workflow template ships with three ways to produce a URL, and every one of
// them is a thing the customer has to get right: pick the action for their host, wire its output
// into an env var, keep the step id and the run line agreeing. That is the last piece of
// configuration left in the whole install — and it duplicates knowledge GitHub already has.
// Vercel, Netlify and Cloudflare Pages all announce every preview they build by creating a GitHub
// DEPLOYMENT on the commit, then a deployment STATUS carrying the URL. Measured on a real
// repository (vercel/commerce): the bot's deployment carries the branch head sha, its environment
// is "Preview – <project>", and the success status carries the preview URL in BOTH
// `environment_url` and `target_url`. So when no --url is given inside Actions on a pull request,
// this file asks the API the job's own GITHUB_TOKEN can already read, instead of asking the
// customer to install a fourth action.
//
// WHAT IT REFUSES TO DO. It never guesses. No "probably https://<repo>.vercel.app", no falling
// back to the production domain — a verdict delivered against the wrong deployment is worse than
// no verdict, because it puts a green check (or a bug report) on a change nobody tested. If no
// ready deployment shows up in time, the run is ERRORED, exit 2 — the runner could not start, and
// it says exactly what it looked for and the two ways to fix it. Nothing here may ever decide
// passed or failed: by the time a verdict exists this file's work is long over.
//
// THE TRIGGER IS DELIBERATELY NARROW: no --url AND GITHUB_ACTIONS=true AND a pull request in the
// event. Anywhere else — a laptop, a push build, another CI — the missing --url keeps producing
// exactly the error it produces today. A laptop has no deployments API to ask, and a push build
// has no pull request to have a preview.

import { readFileSync } from "node:fs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

/** Four minutes. Vercel's own wait-for-preview action defaults to ten; most preview builds are
 * done in one to three. Long enough that a cold build usually makes it, short enough that a
 * repository with no deployment integration at all learns that in one coffee, not one lunch. */
export const DEFAULT_WAIT_SEC = 240;

/** Ten seconds between polls: 24 progress lines at the cap. Each poll is one or two API calls,
 * far inside the 1,000/hour a GITHUB_TOKEN gets, and each poll prints a line — a CI log that goes
 * quiet for four minutes reads as hung, and gets its job cancelled by a human at minute two. */
const POLL_MS = 10_000;

const short = (sha) => String(sha).slice(0, 7);

// ---- the trigger ------------------------------------------------------------------------------

/**
 * Is this a place where the preview can be discovered at all — and for which commit?
 *
 * The sha comes from the EVENT FILE first, GITHUB_SHA only as a fallback, and the order is the
 * correctness of the whole feature: on a pull_request event GITHUB_SHA is the ephemeral MERGE
 * commit Actions synthesised, a sha that exists nowhere outside the runner. No host ever deploys
 * it — the deployment bots attach to the branch head (measured above: deployment.sha = the head
 * commit). Polling for the merge sha would find zero deployments forever and time out on every
 * healthy repository.
 */
export function previewContext(env = process.env, readFile = (p) => readFileSync(p, "utf8")) {
  if (env.GITHUB_ACTIONS !== "true") return { eligible: false, why: "not GitHub Actions" };
  const repo = env.GITHUB_REPOSITORY || "";
  if (!repo.includes("/")) return { eligible: false, why: "GITHUB_REPOSITORY is not set" };
  let pr = 0;
  let sha = "";
  if (env.GITHUB_EVENT_PATH) {
    try {
      const ev = JSON.parse(readFile(env.GITHUB_EVENT_PATH));
      if (ev?.pull_request) {
        pr = Number(ev.pull_request.number) || 0;
        sha = String(ev.pull_request.head?.sha || "");
      }
    } catch {
      /* an unreadable event file is not the end: the ref can still say this is a pull request */
    }
  }
  if (!pr) {
    const m = /refs\/pull\/(\d+)\//.exec(env.GITHUB_REF || "");
    if (m) pr = Number(m[1]);
  }
  // No pull request, no preview to look for. push builds and cron jobs land here, and they keep
  // today's missing --url error, which names the actual fix.
  if (!pr) return { eligible: false, why: "this event has no pull request" };
  if (!sha) sha = String(env.GITHUB_SHA || "");
  if (!sha) return { eligible: false, why: "no commit sha in the environment" };
  return { eligible: true, repo, sha, pr };
}

// ---- talking to the API -----------------------------------------------------------------------

/**
 * The statuses that end the wait EARLY, with the fix in the message. Everything else — a 500, a
 * 429, a network blip — is worth another poll, but none of these three changes by waiting:
 * repolling a bad credential for four minutes reports the same thing four minutes later.
 */
function fatalApiFailure(status, repo, body = "") {
  const tail = body ? ` ${String(body).slice(0, 160)}` : "";
  if (status === 403) {
    return `GitHub refused to list ${repo}'s deployments (403). A pull request opened from a fork gets a read-only GITHUB_TOKEN that may not see them.${tail}`;
  }
  if (status === 401) return `GitHub rejected the token (401). GITHUB_TOKEN is set but not valid for ${repo}.${tail}`;
  if (status === 404) return `GitHub could not find ${repo}'s deployments (404). The token cannot see the repository, which is what a fork's read-only token looks like.${tail}`;
  return "";
}

/**
 * One look at the API: is there, right now, a deployment of `sha` whose latest status is a success
 * carrying a URL? Returns exactly one of:
 *   { url, environment }  found it
 *   { fatal }             stop polling — the message carries the fix
 *   { saw }               not yet — one sentence of what WAS there, for the progress line
 */
async function lookOnce({ api, repo, sha, headers, fetchImpl }) {
  let res;
  try {
    res = await fetchImpl(`${api}/repos/${repo}/deployments?sha=${encodeURIComponent(sha)}&per_page=100`, { headers });
  } catch (e) {
    // A runner's network does blip. One failed poll out of 24 must not end a wait that the next
    // poll would have satisfied.
    return { saw: `the GitHub API could not be reached (${String(e && e.message ? e.message : e).split("\n")[0]})` };
  }
  if (!res.ok) {
    const fatal = fatalApiFailure(res.status, repo, await res.text().catch(() => ""));
    return fatal ? { fatal } : { saw: `GitHub answered ${res.status}` };
  }
  const list = await res.json().catch(() => null);
  if (!Array.isArray(list)) return { saw: "GitHub answered with something that is not a deployment list" };

  // Filtered AGAIN by sha, even though the query already asked. The server side of that query is
  // not ours to trust with the correctness of a verdict: anything else in the list is a deployment
  // of some OTHER commit — the previous push, the default branch — and testing one of those means
  // putting a verdict on a change nobody ran. Newest first (id breaks the tie): one push measurably
  // creates several deployments — vercel/commerce got two in the same second, one per Vercel
  // project — and the newest is the one that describes the code under review.
  const mine = list
    .filter((d) => d && d.sha === sha)
    .sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || (b.id || 0) - (a.id || 0));
  if (!mine.length) return { saw: `no deployments of ${short(sha)} yet` };

  let newest = "";
  let successNoUrl = false;
  for (const d of mine) {
    let r2;
    try {
      r2 = await fetchImpl(`${api}/repos/${repo}/deployments/${d.id}/statuses?per_page=20`, { headers });
    } catch (e) {
      return { saw: `the GitHub API could not be reached (${String(e && e.message ? e.message : e).split("\n")[0]})` };
    }
    if (!r2.ok) {
      const fatal = fatalApiFailure(r2.status, repo, await r2.text().catch(() => ""));
      return fatal ? { fatal } : { saw: `GitHub answered ${r2.status}` };
    }
    const statuses = await r2.json().catch(() => null);
    // Statuses arrive newest first, so [0] IS the latest — and only the latest may speak for the
    // deployment. An old success below a newer failure is a preview that was up and then fell
    // over, and testing it reports bugs against a page that no longer exists.
    const latest = Array.isArray(statuses) && statuses.length ? statuses[0] : null;
    if (!newest) newest = latest ? `newest deployment (${d.environment || "?"}) is ${latest.state}` : `newest deployment (${d.environment || "?"}) has no status yet`;
    if (!latest || latest.state !== "success") continue;
    const url = String(latest.environment_url || latest.target_url || "").trim();
    if (url) return { url, environment: String(d.environment || "") };
    // Success with no URL is real: a bare deployment API user, or a "deploy finished" webhook that
    // never filled the field in. There is nothing to visit, so it cannot win — but it is worth a
    // different sentence in the log than "still pending".
    successNoUrl = true;
  }
  if (successNoUrl) return { saw: `a deployment of ${short(sha)} succeeded but its status names no URL` };
  return { saw: `${mine.length} deployment${mine.length === 1 ? "" : "s"} of ${short(sha)}, ${newest}` };
}

// ---- the wait ---------------------------------------------------------------------------------

/**
 * Poll until a deployment of `sha` is ready, a fatal answer arrives, or the cap runs out.
 *
 * Never throws, and the failure message is the whole product of the failure path: it names what
 * was looked for (repo, sha, for how long), what the last look actually saw, and both fixes. A
 * bare "no preview found" would send someone to re-run the build three times before they learn
 * their host never talks to the deployments API at all.
 */
export async function resolvePreview({
  repo,
  sha,
  token,
  api = "https://api.github.com",
  waitMs = DEFAULT_WAIT_SEC * 1000,
  pollMs = POLL_MS,
  fetchImpl = fetch,
  log = console.log,
  sleep = (ms) => new Promise((r) => setTimeout(r, ms)),
  now = Date.now,
}) {
  if (!token) {
    // Without a token every poll would 401 for four minutes. The workflow template already passes
    // it; the message repeats the exact line for anyone who wrote their own workflow.
    return { url: "", problem: "GITHUB_TOKEN is not set, so the deployments API cannot be asked which preview belongs to this pull request. In Actions, pass it to the step (env: GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}) — or pass --url yourself." };
  }
  // The same headers postComment sends, for the same reason: the bare token works today, but the
  // api-version and user-agent are what GitHub asks integrations to send, and a request they
  // throttle is a preview this run never finds.
  const headers = {
    authorization: `Bearer ${token}`,
    accept: "application/vnd.github+json",
    "x-github-api-version": "2022-11-28",
    "user-agent": "smolanalytics-cli",
  };
  const totalSec = Math.round(waitMs / 1000);
  const started = now();
  log(C.dim(`no --url given — asking the GitHub deployments API for ${repo}'s preview of ${short(sha)} (up to ${totalSec}s; --wait-preview <sec> changes that).`));
  let last = "";
  for (;;) {
    const look = await lookOnce({ api, repo, sha, headers, fetchImpl });
    if (look.url) {
      log(`${C.b("preview found")} ${look.url} ${C.dim(`(${look.environment || "deployment"}, after ${Math.round((now() - started) / 1000)}s)`)}`);
      return { url: look.url, environment: look.environment, problem: "" };
    }
    if (look.fatal) {
      return { url: "", problem: `${look.fatal} The preview cannot be discovered with this token — pass --url to name it yourself.` };
    }
    last = look.saw;
    const left = waitMs - (now() - started);
    if (left <= 0) {
      return {
        url: "",
        problem:
          `no ready preview deployment for ${repo} at ${sha} after ${totalSec}s — the last look saw: ${last}. ` +
          `Either pass --url <preview> yourself, or raise --wait-preview above ${totalSec} if this host is just slow. ` +
          `Guessing a URL is not an option: a verdict against the wrong deployment is worse than no verdict.`,
      };
    }
    // One line per poll, elapsed over cap. A CI log that goes silent for minutes reads as a hung
    // browser, and hung-looking jobs get cancelled by hand at exactly the moment the build was
    // about to finish.
    log(C.dim(`  waiting for the preview (${Math.round((now() - started) / 1000)}s of ${totalSec}s): ${last}`));
    await sleep(Math.min(pollMs, left));
  }
}

// ---- what bin/smolanalytics.mjs calls ---------------------------------------------------------

/**
 * The whole feature behind one call: validate the flag, check the trigger, resolve. Returns
 *   { skipped: true }             not our situation — the caller keeps today's missing-url path
 *   { url }                       found; the caller runs against it
 *   { problem }                   errored — the caller prints it and exits 2, the runner code,
 *                                 because no test ran and nothing was learned about the app
 * Never throws: a crash escaping this into bin's catch-all would exit with the wrong meaning.
 */
export async function autoPreviewUrl({ waitRaw, env = process.env, log = console.log, fetchImpl = fetch, pollMs, sleep } = {}) {
  // Refused out loud, exactly like --retries: Number("4m") is NaN, and any silent coercion picks
  // a wait the person did not ask for — 0 turns the feature off for whoever asked for MORE time.
  if (waitRaw !== undefined && !/^\d+$/.test(String(waitRaw))) {
    return { skipped: false, url: "", problem: `--wait-preview needs a whole number of seconds, got ${JSON.stringify(waitRaw)}. The default is ${DEFAULT_WAIT_SEC}.` };
  }
  const ctx = previewContext(env);
  if (!ctx.eligible) return { skipped: true, url: "", problem: "" };
  try {
    const r = await resolvePreview({
      repo: ctx.repo,
      sha: ctx.sha,
      token: env.GITHUB_TOKEN || env.GH_TOKEN || "",
      api: (env.GITHUB_API_URL || "https://api.github.com").replace(/\/+$/, ""),
      waitMs: (waitRaw === undefined ? DEFAULT_WAIT_SEC : Number(waitRaw)) * 1000,
      ...(pollMs ? { pollMs } : {}),
      ...(sleep ? { sleep } : {}),
      fetchImpl,
      log,
    });
    return { skipped: false, ...r };
  } catch (e) {
    return { skipped: false, url: "", problem: `the preview lookup failed (${e && e.message ? e.message : e}). Pass --url to name the preview yourself. This is the test runner, not your application.` };
  }
}
