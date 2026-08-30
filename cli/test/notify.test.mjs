// TELLING SOMEBODY — and the four ways a notifier ruins the tool it is attached to.
//
// The verdict had exactly one channel: a comment on a pull request. That works while somebody is
// looking at the pull request, and not at 2am, and not for a team that lives in Slack. So this
// exists. But a notifier is a side-channel bolted onto something people trust, and side-channels
// fail in specific ways:
//
//   IT CHANGES A VERDICT. A build that goes red because Slack was down is a tool deleted the same
//   day. Nothing here may throw, and nothing here may touch an exit code.
//
//   IT LEAKS THE WEBHOOK. A Slack incoming-webhook URL is a bearer credential — whoever holds it
//   posts into that channel forever. It comes from the environment, never a flag, because a flag
//   lands in shell history and in the command line CI prints at the top of every log.
//
//   IT CRIES WOLF. A green run posting "all good" on every push is how a channel gets muted, and a
//   muted channel is worse than none: the one message that mattered arrives where nobody reads.
//
//   IT SAYS TOO LITTLE. "3 failed" is a notification people mute in a week. The message has to
//   carry the ship verdict, including what was never checked — which is the half a run that looks
//   green is hiding.

import { test } from "node:test";
import assert from "node:assert/strict";
import { notify, shouldSend, slackPayload, webhookPayload, parseWhen, notifyLine, SLACK_VAR, WEBHOOK_VAR } from "../lib/notify.mjs";
import { shipReport } from "../lib/ship.mjs";

const r = (name, status, mode = "replay") => ({ name, status, mode, ms: 1000 });
const BROKEN = [r("checkout", "failed"), r("search", "passed")];
const GREEN = [r("a", "passed"), r("b", "passed")];
const LOOKS_GREEN = [r("a", "passed"), r("b", "stale"), r("c", "flaky")];
const HOOK = "https://hooks.slack.com/services/T000/B000/xxxxxxxxxxxx";

/** Records every request instead of making one. */
function spy(answer = { ok: true, status: 200 }) {
  const calls = [];
  return { calls, fetchImpl: async (url, init) => { calls.push({ url, body: JSON.parse(init.body) }); return answer; } };
}

/* ── it never changes a verdict ──────────────────────────────────────────────────────────────── */

test("every delivery failure resolves, and none of them throws", async () => {
  const modes = [
    ["a refusal", async () => ({ ok: false, status: 500 })],
    ["a network error", async () => { throw new Error("ECONNREFUSED"); }],
    ["a hang", async (_u, init) => new Promise((_res, rej) => init.signal.addEventListener("abort", () => rej(Object.assign(new Error("aborted"), { name: "AbortError" })))) ],
    ["nonsense back", async () => null],
  ];
  for (const [what, fetchImpl] of modes) {
    const out = await notify(BROKEN, { env: { [SLACK_VAR]: HOOK }, fetchImpl, timeoutMs: 40 });
    assert.equal(out.sent, false, what);
    assert.ok(Array.isArray(out.results), `${what}: it must report rather than throw`);
    assert.match(notifyLine(out), /verdict above still stands/, `${what}: the reader must not think the run is invalid`);
  }
});

test("nothing configured is silence, not an error", async () => {
  const { fetchImpl, calls } = spy();
  const out = await notify(BROKEN, { env: {}, fetchImpl });
  assert.equal(out.sent, false);
  assert.equal(calls.length, 0, "a run with no webhook must make no request at all");
  assert.equal(notifyLine(out), "", "and must print nothing");
});

/* ── it does not cry wolf ────────────────────────────────────────────────────────────────────── */

test("a clean run says nothing", async () => {
  const { fetchImpl, calls } = spy();
  const out = await notify(GREEN, { env: { [SLACK_VAR]: HOOK }, fetchImpl });
  assert.equal(out.sent, false);
  assert.equal(calls.length, 0, "posting on every green push is how a channel gets muted");
});

test("a run that LOOKS green but verified almost nothing does speak", () => {
  // The important case, and the reason the default is not "failed only": nothing failed, and a
  // stale recording plus a flake mean two flows were never verified. That is the run somebody
  // should hear about precisely because it looks fine.
  const report = shipReport(LOOKS_GREEN);
  assert.equal(report.verdict, "partly");
  assert.equal(shouldSend(report, "problems"), true);
});

test("always and never mean what they say", () => {
  assert.equal(shouldSend(shipReport(GREEN), "always"), true);
  assert.equal(shouldSend(shipReport(BROKEN), "never"), false);
  assert.equal(parseWhen("always").value, "always");
  assert.match(parseWhen("loud").problem, /problems, always, never/);
  assert.equal(parseWhen(undefined).value, "problems", "the default is the useful one");
});

/* ── the message is worth reading ────────────────────────────────────────────────────────────── */

test("the message carries the verdict and what was never checked", async () => {
  const { fetchImpl, calls } = spy();
  await notify([r("checkout", "failed"), r("a", "passed"), r("b", "stale")], {
    env: { [SLACK_VAR]: HOOK }, fetchImpl, suite: "tests/", url: "shop.test",
  });
  const body = calls[0].body;
  // `text` as well as `blocks`: a blocks-only post reaches a phone lock screen as "This content
  // can't be displayed", which is the moment the notification most needs to work.
  assert.match(body.text, /Do not ship this/, "the fallback line must carry the verdict");
  const rendered = JSON.stringify(body.blocks);
  assert.match(rendered, /What is broken/);
  assert.match(rendered, /stopped fitting/, "the unverified half must survive into the message");
  assert.match(rendered, /shop\.test/);
});

test("the generic payload is a stable shape somebody can parse", () => {
  const p = webhookPayload(shipReport([r("checkout", "failed"), r("b", "stale")]), { suite: "tests/", commit: "a1b2c3d" });
  assert.equal(p.kind, "smolanalytics.run");
  assert.equal(p.v, 1);
  assert.equal(p.verdict, "no");
  assert.deepEqual(p.broken.tests, ["checkout"]);
  assert.deepEqual(p.unchecked.map((u) => u.kind), ["stale"]);
  assert.equal(p.commit, "a1b2c3d");
  assert.match(p.text, /What is broken/);
});

/* ── the webhook is a credential ─────────────────────────────────────────────────────────────── */

test("an http URL is refused rather than used", async () => {
  // The URL is a bearer credential and the payload names what is broken in somebody's product.
  // Neither belongs on the wire in clear.
  const { fetchImpl, calls } = spy();
  const out = await notify(BROKEN, { env: { [SLACK_VAR]: "http://hooks.slack.com/x" }, fetchImpl });
  assert.equal(calls.length, 0, "it must not post to an http endpoint");
  assert.equal(out.sent, false);
  assert.match(out.results[0].why, /https/);
});

test("the webhook URL never appears in anything printed", async () => {
  // A failure message that echoes the endpoint puts the credential in a CI log, which is forever.
  const out = await notify(BROKEN, {
    env: { [SLACK_VAR]: HOOK },
    fetchImpl: async () => { throw new Error(`connect failed to ${HOOK}`); },
  });
  const line = notifyLine(out);
  assert.ok(!line.includes(HOOK), `the webhook leaked into output: ${line}`);
  assert.ok(!JSON.stringify(out.results).includes("B000"), "nor into the structured result");
});

test("both targets can be configured, and one failing does not stop the other", async () => {
  const calls = [];
  const fetchImpl = async (url, init) => {
    calls.push(url);
    if (url.includes("slack")) throw new Error("down");
    return { ok: true, status: 200 };
  };
  const out = await notify(BROKEN, { env: { [SLACK_VAR]: HOOK, [WEBHOOK_VAR]: "https://example.test/hook" }, fetchImpl });
  assert.equal(calls.length, 2, "both were attempted");
  assert.equal(out.sent, true, "one working target is a send");
  assert.deepEqual(out.results.map((x) => x.ok), [false, true]);
});

/* ── the cause travels with the alert (lib/cluster.mjs) ──────────────────────────────────────── */

const ELEVEN_AND_ONE = [
  ...Array.from({ length: 11 }, (_, i) => ({
    name: `flow ${i}`,
    status: "failed",
    reason: "it broke",
    suspects: [{ file: "src/api/client.ts", evidence: "this PR removed it" }],
  })),
  { name: "search", status: "failed", reason: "it broke", suspects: [{ file: "src/SearchBox.tsx", evidence: "this PR removed it" }] },
];

test("an alert about twelve failures says that eleven of them are one change", async () => {
  // Otherwise the notification only announces the work: somebody opens the pull request and does
  // the grouping by hand, which is the job this is supposed to have already done.
  let posted = null;
  await notify(ELEVEN_AND_ONE, {
    env: { [SLACK_VAR]: HOOK },
    fetchImpl: async (_u, init) => { posted = JSON.parse(init.body); return { ok: true, status: 200 }; },
  });
  const text = posted.blocks.map((b) => (b.text ? b.text.text : "")).join("\n");
  assert.match(text, /11 of these failures group into 1 likely cause/);
  assert.match(text, /src\/api\/client\.ts/);
  assert.match(text, /search shared nothing with the rest/, "and which one it does not explain");
});

test("the generic webhook carries the causes as data, not just prose", async () => {
  // A receiver should be able to page the team that owns the file, once, instead of eleven times.
  let posted = null;
  await notify(ELEVEN_AND_ONE, {
    env: { [WEBHOOK_VAR]: "https://example.test/hook" },
    fetchImpl: async (_u, init) => { posted = JSON.parse(init.body); return { ok: true, status: 200 }; },
  });
  assert.equal(posted.causes.length, 1);
  assert.equal(posted.causes[0].cause, "src/api/client.ts");
  assert.equal(posted.causes[0].signal, "suspect");
  assert.equal(posted.causes[0].tests.length, 11);
  assert.ok(!posted.causes[0].tests.includes("search"), "the odd one out must not be filed under the group");
});

test("failures that do not group add nothing to the message", async () => {
  const unrelated = [
    { name: "a", status: "failed", reason: "it broke", suspects: [{ file: "src/one.ts", evidence: "e" }] },
    { name: "b", status: "failed", reason: "it broke", suspects: [{ file: "src/two.ts", evidence: "e" }] },
  ];
  let slack = null;
  let hook = null;
  await notify(unrelated, {
    env: { [SLACK_VAR]: HOOK, [WEBHOOK_VAR]: "https://example.test/hook" },
    fetchImpl: async (u, init) => {
      if (String(u).includes("slack")) slack = JSON.parse(init.body);
      else hook = JSON.parse(init.body);
      return { ok: true, status: 200 };
    },
  });
  assert.deepEqual(hook.causes, []);
  const text = slack.blocks.map((b) => (b.text ? b.text.text : "")).join("\n");
  assert.ok(!/group into/.test(text), "a header that restates the list is noise in a channel");
});

test("a recording that cannot be read costs the causes, never the alert", async () => {
  // The verdict was decided before this function was called. Clustering is decoration on top of it,
  // and decoration that can swallow a 2am notification is worse than no decoration.
  let posted = null;
  let asked = 0;
  // planPath on every row, so the unreadable recording is genuinely reached rather than skipped.
  const withPlans = ELEVEN_AND_ONE.map((r, i) => ({ ...r, planPath: `p${i}.json` }));
  const out = await notify(withPlans, {
    env: { [SLACK_VAR]: HOOK },
    readPlan: () => { asked++; throw new Error("EACCES"); },
    fetchImpl: async (_u, init) => { posted = JSON.parse(init.body); return { ok: true, status: 200 }; },
  });
  assert.ok(asked > 0, "the test did not actually exercise the unreadable recording");
  assert.equal(out.sent, true);
  assert.match(posted.text, /ship/i, "the verdict still went out");
  // The suspect signal needs no recording, so the strongest grouping survives a dead disk.
  const text = posted.blocks.map((b) => (b.text ? b.text.text : "")).join("\n");
  assert.match(text, /src\/api\/client\.ts/, "blame-based grouping does not depend on the recordings");
});
