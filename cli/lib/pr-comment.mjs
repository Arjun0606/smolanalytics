// The pull-request comment — for most people, the entire product.
//
// A developer does not watch the CI job. They get a notification, read the first line of it on a
// phone, and decide whether to look. So this file has one job: put the verdict in the first line,
// put the bug report where nobody has to click for it, and never once make a rename look like an
// outage.
//
// THE THREE STATUSES ARE THE PRODUCT, AND THIS FILE IS WHERE BLURRING THEM WOULD COST US:
//
//   failed   the app did not do what the sentence describes. A bug report. The reason IS the
//            report, so it is printed in full, first, uncollapsed.
//   stale    a RECORDING stopped fitting. A replay cannot tell "the button was renamed" from "the
//            button is gone" (see stalenessNote in test.mjs), so calling it a failure pages
//            somebody at 2am over a copy change. Stale is never red, never worded as a failure,
//            and never counted in the failure total.
//   errored  OUR runner could not run — no browser, no key, no network. Saying this in the same
//            voice as a failure tells a customer their checkout is broken because our machine had
//            no disk space. It gets its own section and says plainly that it is us.
//
// WHY MODE AND DURATION ARE ON EVERY LINE. A recorded run replays with zero model calls; only a
// run that no longer fits wakes the agent. Twelve lines of `replay 640ms` beside one `agent 47.2s`
// is that argument made visible, in the place the buyer already looks, without a word of pitch.
//
// WHY THIS IS DETERMINISTIC. The comment is UPDATED in place on every push (see postComment), so
// any timestamp or ordering wobble would show up as a diff on a PR that did not change. Same
// input, same bytes: no Date, no randomness, input order preserved.

import { readFileSync } from "node:fs";

// GitHub rejects a comment body over 65,536 characters with a 422, which loses the whole report
// rather than the tail of it. A trimmed report beats no report.
const BODY_LIMIT = 65_000;

// One pathological reason (a stack trace pasted into a verdict) must not push the other tests'
// reasons past the limit above.
const REASON_LIMIT = 4_000;

const MARKER_RE = /<!--\s*smolanalytics-run(?::[A-Za-z0-9._-]+)?\s*-->/;

/**
 * The needle used to find our own previous comment so a re-run edits it instead of adding one.
 *
 * The suite key is sanitised because a suite named `e2e --> prod` would close the HTML comment
 * early and dump `prod -->` into the rendered PR, and because a marker that changes shape between
 * runs is a marker that finds nothing: ten pushes, ten comments.
 */
export function commentMarker({ suite } = {}) {
  const key = String(suite ?? "")
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    // Runs collapse so `e2e --> prod` keys as `e2e-prod` and not `e2e----prod`: the marker is a
    // needle two runs have to agree on, so its shape has to be obvious, not incidental.
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
  return key ? `<!-- smolanalytics-run:${key} -->` : "<!-- smolanalytics-run -->";
}

// ---- formatting primitives --------------------------------------------------------------------

/**
 * Durations the way a person reads them: 640ms, 47.2s, 2m 5s.
 *
 * Sub-10s stays in milliseconds on purpose — "0.6s" and "0.7s" beside each other look like noise,
 * while "640ms" beside "47.2s" is the whole economic story in two tokens.
 */
export function formatDuration(ms) {
  const n = Number(ms);
  // A missing duration renders as nothing rather than "NaNms". One NaN in a report makes a reader
  // distrust the numbers next to it, including the verdict.
  if (!Number.isFinite(n) || n < 0) return "";
  if (n < 10_000) return `${Math.round(n)}ms`;
  const whole = Math.round(n / 1000);
  // Rounds to 60 before the seconds branch can print "60.0s".
  if (whole < 60) return `${(n / 1000).toFixed(1)}s`;
  return `${Math.floor(whole / 60)}m ${whole % 60}s`;
}

/**
 * Text placed inline in markdown we generate.
 *
 * Test sentences and app copy are arbitrary strings. An unescaped `*` italicises the rest of the
 * comment; a `<details>` inside a name closes the block we put it in and spills the passing list
 * over the footer. Escaping is invisible when rendered and worth the backslashes.
 */
function inline(s) {
  return String(s ?? "")
    .replace(/\r?\n+/g, " ")
    .replace(/</g, "&lt;")
    .replace(/([*_`[\]\\])/g, "\\$1")
    .trim();
}

/** A code span whose fence is always longer than any backtick run inside it (CommonMark rule). */
function code(s) {
  const t = String(s ?? "").replace(/\r?\n+/g, " ").trim();
  if (!t) return "";
  let longest = 0;
  for (const m of t.matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  const fence = "`".repeat(longest + 1);
  const pad = longest || /^`|`$/.test(t) ? " " : "";
  return `${fence}${pad}${t}${pad}${fence}`;
}

/**
 * The reason, as a blockquote, with its line breaks kept.
 *
 * Only `<` is escaped here: the reason is prose written for a human and mangling its asterisks
 * would be worse than a stray italic, but raw HTML would break out of the comment structure.
 */
function quote(s) {
  const text = truncate(String(s ?? "").replace(/\r\n?/g, "\n").trim(), REASON_LIMIT);
  return text
    .split("\n")
    .map((line) => (line.trim() ? `> ${line.replace(/</g, "&lt;")}` : ">"))
    .join("\n");
}

function truncate(s, limit) {
  return s.length <= limit ? s : `${s.slice(0, limit).trimEnd()}… (truncated)`;
}

const plural = (n, word) => (n === 1 ? word : `${word}s`);

// ---- the rows ----------------------------------------------------------------------------------

const KNOWN = new Set(["passed", "failed", "stale", "errored"]);

/**
 * An unrecognised status becomes `errored`, never `passed` and never `failed`.
 *
 * Passed would turn a real bug green. Failed would page somebody over a typo in a runner we
 * changed. Errored is the only bucket that is honest about not knowing, and it says out loud that
 * the problem is on our side.
 */
function normalize(r) {
  const row = r && typeof r === "object" ? r : {};
  const raw = typeof row.status === "string" ? row.status.toLowerCase().trim() : "";
  const known = KNOWN.has(raw);
  const note = known
    ? ""
    : raw
      ? `The runner reported an unrecognised status ${JSON.stringify(row.status)}, so this test is not a verdict about your app.`
      : "The runner reported no status for this test, so this test is not a verdict about your app.";
  const reason = [note, typeof row.reason === "string" ? row.reason.trim() : ""].filter(Boolean).join(" ");
  return {
    test: typeof row.test === "string" && row.test.trim() ? row.test.trim() : "(unnamed test)",
    status: known ? raw : "errored",
    mode: typeof row.mode === "string" ? row.mode.trim() : "",
    durationMs: row.durationMs,
    reason,
    file: typeof row.file === "string" ? row.file.trim() : "",
  };
}

/** `agent · 47.2s · tests/checkout.md` — whichever of the three we actually have. */
function meta(r) {
  return [r.mode ? inline(r.mode) : "", formatDuration(r.durationMs), r.file ? code(r.file) : ""]
    .filter(Boolean)
    .join(" · ");
}

function block(r) {
  const out = [`**${inline(r.test)}**`];
  const m = meta(r);
  if (m) out.push(m);
  if (r.reason) out.push("", quote(r.reason));
  out.push("");
  return out;
}

/**
 * The first line, which is the only line a notification preview shows.
 *
 * The hard rule lives here: a failure count is printed only when something actually failed. A run
 * whose non-passing tests are all stale or errored says so in words that contain no failure count,
 * because "2 failed" in a preview sends someone to their laptop.
 */
function headline({ passed, failed, stale, errored }, total) {
  if (total === 0) return "**No tests ran.**";
  // "1 of 1 test failed" is arithmetic, not English. A one-test suite is the common first run.
  if (total === 1 && failed.length) return "**The test failed.**";
  if (total === 1 && passed.length) return "**The test passed.**";
  if (failed.length) {
    const lead = `**${failed.length} of ${total} ${plural(total, "test")} failed**`;
    const also = [];
    if (stale.length) also.push(`${stale.length} ${plural(stale.length, "recording")} stale`);
    if (errored.length) also.push(`${errored.length} could not run`);
    return also.length ? `${lead} · ${also.join(" · ")}` : `${lead}.`;
  }
  const tail = [];
  if (stale.length) {
    tail.push(`${stale.length} ${plural(stale.length, "recording")} no longer ${stale.length === 1 ? "fits" : "fit"} the app`);
  }
  if (errored.length) tail.push(`${errored.length} ${plural(errored.length, "test")} could not be run`);
  if (!tail.length) return `**All ${total} ${plural(total, "test")} passed.**`;
  const rest = `${tail.join(", and ")}.`;
  if (!passed.length) return `**Nothing failed.** ${rest[0].toUpperCase()}${rest.slice(1)}`;
  return `**${passed.length} of ${total} ${plural(total, "test")} passed, none failed.** ${rest[0].toUpperCase()}${rest.slice(1)}`;
}

/** `12 replayed with no model calls · 1 agent run` — true only because it is counted, not claimed. */
function economics(rows) {
  const replay = rows.filter((r) => r.mode === "replay").length;
  const agent = rows.filter((r) => r.mode === "agent").length;
  const bits = [];
  if (replay) bits.push(`${replay} replayed with no model calls`);
  if (agent) bits.push(`${agent} ${plural(agent, "agent run")}`);
  return bits.join(" · ");
}

// ---- the comment ---------------------------------------------------------------------------------

/**
 * @param {Array<{test:string,status:string,mode?:string,durationMs?:number,reason?:string,file?:string}>} results
 * @param {{url?:string, commit?:string, runUrl?:string, suite?:string}} opts
 * @returns {string} markdown
 */
export function commentBody(results, opts = {}) {
  const rows = (Array.isArray(results) ? results : []).map(normalize);
  const by = { passed: [], failed: [], stale: [], errored: [] };
  for (const r of rows) by[r.status].push(r);

  const lines = [commentMarker(opts), "", headline(by, rows.length), ""];

  // Failures first and uncollapsed: the reason is the bug report, and a bug report behind a
  // disclosure triangle is a bug report nobody reads.
  if (by.failed.length) {
    lines.push("#### Failed", "");
    for (const r of by.failed) lines.push(...block(r));
  }

  // Worded so it cannot be mistaken for a bug, and placed after failures so it never leads.
  if (by.stale.length) {
    lines.push(
      "#### Stale recordings",
      "",
      "Not failures. A recorded step could not be used, which is exactly what a renamed button looks",
      "like from a replay — a replay cannot tell a rename from a removal. Re-run these with the agent",
      "to re-record, or check the control by hand if you did not expect a change.",
      "",
    );
    for (const r of by.stale) lines.push(...block(r));
  }

  if (by.errored.length) {
    lines.push(
      "#### Could not run",
      "",
      "This is the test runner, not your app. Nothing below is a verdict about this pull request.",
      "",
    );
    for (const r of by.errored) lines.push(...block(r));
  }

  if (by.passed.length === 1) {
    const r = by.passed[0];
    const m = meta(r);
    // Not "1 test passed" — the headline already said the count, and the reader needs the name.
    lines.push(`Passed: **${inline(r.test)}**${m ? ` — ${m}` : ""}`, "");
  } else if (by.passed.length > 1) {
    const econ = economics(by.passed);
    lines.push(
      `<details><summary>${by.passed.length} passed${econ ? ` · ${econ}` : ""}</summary>`,
      "",
      ...by.passed.map((r) => {
        const m = meta(r);
        return `- **${inline(r.test)}**${m ? ` — ${m}` : ""}`;
      }),
      "",
      "</details>",
      "",
    );
  }

  // The footer is what makes the comment checkable: a verdict with no subject is a rumour.
  const footer = [];
  if (opts.url) footer.push(`tested ${code(opts.url)}`);
  if (opts.commit) footer.push(`commit ${code(shortSha(opts.commit))}`);
  if (opts.runUrl) footer.push(`[run log](${String(opts.runUrl).replace(/[\s)]/g, encodeURIComponent)})`);
  lines.push("---", `\`smolanalytics test\`${footer.length ? ` · ${footer.join(" · ")}` : ""}`);

  const body = lines.join("\n").replace(/\n{3,}/g, "\n\n").trimEnd() + "\n";
  if (body.length <= BODY_LIMIT) return body;
  const cut = body.slice(0, BODY_LIMIT);
  const atLine = cut.slice(0, cut.lastIndexOf("\n") + 1) || cut;
  return `${atLine}\n_Trimmed to fit GitHub's comment limit. The full run is in the job log._\n`;
}

/** Full shas are unreadable and a PR reader only ever compares the first seven. */
function shortSha(commit) {
  const s = String(commit).trim();
  return /^[0-9a-f]{40}$/i.test(s) ? s.slice(0, 7) : s;
}

// ---- posting it ---------------------------------------------------------------------------------

const skip = (detail) => ({ posted: false, updated: false, detail });

function prNumber(env) {
  const fromRef = /^refs\/pull\/(\d+)\/(?:merge|head)$/.exec(String(env.GITHUB_REF || ""));
  if (fromRef) return Number(fromRef[1]);
  if (env.GITHUB_EVENT_PATH) {
    try {
      const ev = JSON.parse(readFileSync(env.GITHUB_EVENT_PATH, "utf8"));
      const n = ev?.pull_request?.number ?? ev?.issue?.number ?? ev?.number;
      if (Number.isInteger(n) && n > 0) return n;
    } catch {
      // An unreadable event file is not worth reporting: PR_NUMBER below is the documented escape
      // hatch, and failing here would cost a comment over a file we only guessed would exist.
    }
  }
  const explicit = Number(env.PR_NUMBER);
  return Number.isInteger(explicit) && explicit > 0 ? explicit : null;
}

/** Turn an HTTP failure into the sentence that actually tells someone what to change. */
async function httpDetail(res) {
  const snippet = await res.text().then((t) => String(t).replace(/\s+/g, " ").trim().slice(0, 200)).catch(() => "");
  if (res.status === 403) {
    return "GitHub refused the comment (403). A pull request from a fork gets a read-only GITHUB_TOKEN, and a workflow needs `permissions: pull-requests: write`. The verdict is in the job log either way.";
  }
  if (res.status === 401) return "GitHub rejected the token (401). GITHUB_TOKEN is set but not valid for this repository.";
  if (res.status === 404) {
    return "GitHub returned 404 for this pull request. Either the number is wrong or the token cannot see this repository.";
  }
  return `GitHub returned ${res.status}${snippet ? `: ${snippet}` : ""}.`;
}

/**
 * Put the comment on the pull request, editing our previous one if there is one.
 *
 * NEVER THROWS. A test tool that reddens somebody's build because its own comment could not be
 * delivered is uninstalled the same day, so every path returns { posted:false, detail } and the
 * caller keeps whatever exit code the tests earned.
 *
 * @returns {Promise<{posted:boolean, updated:boolean, detail?:string}>}
 */
export async function postComment(body, env = process.env, fetchImpl = fetch) {
  try {
    const token = env.GITHUB_TOKEN || env.GH_TOKEN;
    const repo = String(env.GITHUB_REPOSITORY || "");
    // Absent credentials mean "running on a laptop", which is the normal case for `smolanalytics
    // test` and must not read like something went wrong.
    if (!token) return skip("no GITHUB_TOKEN, so this run is not commenting on a pull request. The verdict above is the whole report.");
    if (!/^[^/\s]+\/[^/\s]+$/.test(repo)) {
      return skip("no GITHUB_REPOSITORY, so this run is not commenting on a pull request. The verdict above is the whole report.");
    }
    const pr = prNumber(env);
    if (!pr) {
      return skip("this run is not attached to a pull request, so there is nothing to comment on. Set PR_NUMBER to point it at one.");
    }

    // A body without our marker would be invisible to the next run, which is how a PR ends up with
    // one comment per push. Add it rather than refuse.
    const text = MARKER_RE.test(body) ? String(body) : `${commentMarker()}\n${body}`;
    const marker = MARKER_RE.exec(text)[0];

    const api = String(env.GITHUB_API_URL || "https://api.github.com").replace(/\/+$/, "");
    const headers = {
      accept: "application/vnd.github+json",
      authorization: `Bearer ${token}`,
      "user-agent": "smolanalytics-cli",
      "x-github-api-version": "2022-11-28",
    };

    // Paged, because a PR with 120 comments would otherwise hide our own on page 2 and we would
    // post a duplicate on every push. Bounded, because an API that keeps returning full pages must
    // not turn a comment into an infinite loop.
    let existing = null;
    for (let page = 1; page <= 10; page++) {
      const res = await fetchImpl(`${api}/repos/${repo}/issues/${pr}/comments?per_page=100&page=${page}`, { headers });
      if (!res.ok) return skip(await httpDetail(res));
      const list = await res.json().catch(() => null);
      if (!Array.isArray(list) || list.length === 0) break;
      // Last match wins: if an older version of this code ever left duplicates, the one a reader
      // scrolls to is the newest, so that is the one that has to be correct.
      for (const c of list) if (c && typeof c.body === "string" && c.body.includes(marker)) existing = c;
      if (list.length < 100) break;
    }

    const res = await fetchImpl(
      existing ? `${api}/repos/${repo}/issues/comments/${existing.id}` : `${api}/repos/${repo}/issues/${pr}/comments`,
      {
        method: existing ? "PATCH" : "POST",
        headers: { ...headers, "content-type": "application/json" },
        body: JSON.stringify({ body: text }),
      },
    );
    if (!res.ok) return skip(await httpDetail(res));
    const json = await res.json().catch(() => null);
    return { posted: true, updated: Boolean(existing), detail: (json && json.html_url) || undefined };
  } catch (e) {
    return skip(`could not reach GitHub to post the comment (${e && e.message ? e.message : e}). The verdict above still stands.`);
  }
}
