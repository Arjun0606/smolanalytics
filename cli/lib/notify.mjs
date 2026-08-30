// TELLING SOMEBODY, WHICH IS THE HALF THAT WAS MISSING.
//
// The verdict has had exactly one channel: a comment on a pull request. That works while somebody
// is looking at the pull request. It does not work at 2am, it does not work for the person who did
// not open the PR, and it does not work for a team whose whole day happens in Slack. A verdict
// nobody sees is worth what it cost to produce, which is nothing.
//
// WHAT GETS SENT IS THE SHIP VERDICT, not a count. "3 failed" is a notification somebody mutes in a
// week; "Do not ship this — checkout is broken, and two more flows were never checked" is one they
// read. lib/ship.mjs already composes exactly that from facts the run held, so this file is a
// delivery mechanism and deliberately contains no judgement of its own.
//
// THREE RULES IT INHERITS FROM EVERY OTHER SIDE-CHANNEL IN THIS PRODUCT:
//
//   IT CANNOT CHANGE A VERDICT OR AN EXIT CODE. A test tool that fails a build because its own
//   Slack post 500'd is a tool people delete the same day. Every failure here is reported and
//   swallowed, exactly like --teardown and --share.
//
//   THE URL IS A SECRET AND COMES FROM THE ENVIRONMENT ONLY. A Slack webhook URL is a bearer
//   credential — anyone holding it can post into that channel forever. Passing it as a flag would
//   put it in shell history and in the command line CI prints at the top of every log, which is
//   the same mistake --teardown's secret deliberately avoids.
//
//   IT SAYS NOTHING WHEN THERE IS NOTHING TO SAY. A green run posting "all good" every push is how
//   a channel gets muted, and a muted channel is worse than no channel: the one message that
//   mattered arrives somewhere nobody is reading. Default is failures and gaps only.

import { shipReport } from "./ship.mjs";

export const SLACK_VAR = "SMOLANALYTICS_SLACK_WEBHOOK";
export const WEBHOOK_VAR = "SMOLANALYTICS_WEBHOOK";

/** How loud to be. `problems` (default) speaks when something failed or went unverified. */
export const WHEN = new Set(["problems", "always", "never"]);

export function parseWhen(raw) {
  if (raw === undefined || raw === null || raw === "") return { value: "problems", problem: "" };
  const v = String(raw).trim().toLowerCase();
  if (!WHEN.has(v)) {
    return { value: "", problem: `--notify-when takes ${[...WHEN].join(", ")}, got ${JSON.stringify(String(raw))}.` };
  }
  return { value: v, problem: "" };
}

/**
 * Is this run worth interrupting somebody for?
 *
 * `problems` is deliberately not "failed only". A suite where nothing failed but nine recordings
 * went stale verified almost nothing, and that is precisely the run somebody should hear about —
 * it is the one that looks green.
 */
export function shouldSend(report, when = "problems") {
  if (when === "never") return false;
  if (when === "always") return true;
  return report.verdict === "no" || report.verdict === "partly" || report.verdict === "unknown";
}

const EMOJI = { no: ":red_circle:", partly: ":large_orange_circle:", yes: ":large_green_circle:", unknown: ":white_circle:" };

/**
 * Slack's incoming-webhook shape. `text` is set as well as `blocks` because `text` is what a phone
 * notification and a screen reader use — blocks-only posts arrive on a lock screen as the useless
 * string "This content can't be displayed".
 */
export function slackPayload(report, { suite = "tests", url = "", runUrl = "", shareUrl = "" } = {}) {
  const head = `${EMOJI[report.verdict] || ""} ${report.headline}`.trim();
  const body = report.lines.slice(1).join("\n");
  const links = [runUrl && `<${runUrl}|the run>`, shareUrl && `<${shareUrl}|the shared verdict>`].filter(Boolean).join("  ·  ");

  const blocks = [
    { type: "section", text: { type: "mrkdwn", text: `*${head}*` } },
    { type: "section", text: { type: "mrkdwn", text: body.slice(0, 2900) } },
  ];
  if (links) blocks.push({ type: "context", elements: [{ type: "mrkdwn", text: links }] });
  blocks.push({
    type: "context",
    elements: [{ type: "mrkdwn", text: `${suite}${url ? ` against ${url}` : ""}` }],
  });

  // The fallback line carries the verdict, so the notification is useful before it is expanded.
  return { text: `${report.headline} — ${suite}${url ? ` against ${url}` : ""}`, blocks };
}

/** The generic shape, for anything that is not Slack. Stable and documented, so it can be parsed. */
export function webhookPayload(report, { suite = "tests", url = "", runUrl = "", shareUrl = "", commit = "" } = {}) {
  return {
    kind: "smolanalytics.run",
    v: 1,
    verdict: report.verdict,
    headline: report.headline,
    checked: report.checked,
    total: report.total,
    broken: report.broken ? { count: report.broken.count, tests: report.broken.tests } : null,
    unchecked: (report.gaps || []).map((g) => ({ kind: g.kind, count: g.count, tests: g.tests, why: g.why })),
    text: report.lines.join("\n"),
    suite,
    url,
    runUrl,
    shareUrl,
    commit,
  };
}

/**
 * Deliver, and never let delivery matter more than the verdict.
 *
 * Returns what happened so the caller can print one line about it. Every failure path — no URL
 * configured, a refused POST, a network error, a timeout — resolves rather than throws, because
 * the run's exit code was decided before this function was called and must survive it.
 */
/**
 * Take a webhook URL out of a string, however much of it appears.
 *
 * The whole URL, and also its distinctive tail on its own: an error may quote only the path, and a
 * Slack webhook's path segments ARE the secret — the host is public knowledge.
 */
export function redactEndpoint(text, endpoint) {
  let out = String(text ?? "");
  const url = String(endpoint || "").trim();
  if (!url) return out;
  out = out.split(url).join("[the webhook URL]");
  try {
    const u = new URL(url);
    for (const part of u.pathname.split("/").filter((p) => p.length >= 6)) {
      out = out.split(part).join("[redacted]");
    }
  } catch {
    /* not parseable: the whole-string replacement above is all we can do */
  }
  return out;
}

export async function notify(results = [], {
  selection = null,
  suite = "tests",
  url = "",
  runUrl = "",
  shareUrl = "",
  commit = "",
  when = "problems",
  env = process.env,
  fetchImpl = fetch,
  timeoutMs = 10_000,
} = {}) {
  const report = shipReport(results, { selection, suite, url });
  if (!shouldSend(report, when)) return { sent: false, reason: "nothing worth interrupting anybody for" };

  const slack = String(env[SLACK_VAR] || "").trim();
  const hook = String(env[WEBHOOK_VAR] || "").trim();
  if (!slack && !hook) return { sent: false, reason: "" };

  const out = [];
  for (const [target, endpoint, payload] of [
    ["slack", slack, slack ? slackPayload(report, { suite, url, runUrl, shareUrl }) : null],
    ["webhook", hook, hook ? webhookPayload(report, { suite, url, runUrl, shareUrl, commit }) : null],
  ]) {
    if (!endpoint) continue;
    // https only. A webhook URL is a bearer credential and http would put it, and the verdict, on
    // the wire in clear — over a coffee-shop network, on somebody's laptop.
    if (!/^https:\/\//i.test(endpoint)) {
      out.push({ target, ok: false, why: `${target === "slack" ? SLACK_VAR : WEBHOOK_VAR} must be an https URL; nothing was sent.` });
      continue;
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), timeoutMs);
    try {
      const res = await fetchImpl(endpoint, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload),
        signal: ctrl.signal,
      });
      out.push(res && res.ok
        ? { target, ok: true, why: "" }
        : { target, ok: false, why: `${target} answered ${res ? res.status : "nothing"}` });
    } catch (e) {
      // THE ENDPOINT MUST NOT SURVIVE INTO THE MESSAGE.
      //
      // Measured while writing this file's own leak test: fetch failures routinely echo the URL
      // they were given ("connect failed to https://hooks.slack.com/services/T000/B000/…"), and
      // that string is a bearer credential — whoever reads the CI log can post into that channel
      // forever. We do not control what somebody else's error carries, so the endpoint is scrubbed
      // out of it rather than trusted not to be there.
      const raw = e && e.name === "AbortError" ? `no answer in ${Math.round(timeoutMs / 1000)}s` : String((e && e.message) || e);
      out.push({ target, ok: false, why: `${target} could not be reached (${redactEndpoint(raw, endpoint)})` });
    } finally {
      clearTimeout(timer);
    }
  }

  return { sent: out.some((o) => o.ok), results: out, reason: "" };
}

/** One line for the terminal. Never alarming: a delivery failure is not a verdict. */
export function notifyLine(result) {
  if (!result || result.sent === undefined) return "";
  if (result.sent) {
    const names = (result.results || []).filter((r) => r.ok).map((r) => r.target).join(" and ");
    return `  told ${names}.`;
  }
  const failures = (result.results || []).filter((r) => !r.ok);
  if (!failures.length) return "";
  // "the verdict above still stands" is the same sentence report() uses for a failed POST, for the
  // same reason: the reader must not wonder whether the run is invalid.
  return `  not sent: ${failures.map((f) => f.why).join("; ")} — the verdict above still stands.`;
}
