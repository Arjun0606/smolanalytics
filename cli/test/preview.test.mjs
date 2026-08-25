// PREVIEW-URL AUTO-DETECTION — THE TWO WAYS IT COULD HURT SOMEBODY.
//
// This module decides, with no human in the loop, WHICH DEPLOYMENT a customer's CI tests. There
// are exactly two catastrophic outcomes and both are silent:
//
//   IT TESTS THE WRONG BUILD.  A verdict about someone else's commit, posted on this pull
//   request, is worse than no verdict — it is a lie with a green tick on it. So a deployment for
//   another sha, or one whose latest status is anything but success, must never be used.
//
//   IT HANGS THE PIPELINE.  A poll loop with no cap burns a customer's Actions minutes until the
//   job times out, and the failure they see is "job cancelled after 6 hours", which names nothing
//   and blames us for the wrong thing. So the wait is bounded and the give-up message says what
//   was looked for and what to do instead.
//
// This file exists because the agent that wrote lib/preview.mjs died before writing any tests for
// it, and the module was already wired into the CLI. Shipping an untested auto-resolver that
// chooses what to test is the one thing this product cannot do, so the dangerous paths are pinned
// here rather than trusted.
//
// NOTHING HERE TOUCHES THE REAL GITHUB API: every call goes to an injected fetch that answers from
// a fixture. A test that reaches the network is a test that fails on a plane.

import { test } from "node:test";
import assert from "node:assert/strict";
import { previewContext, resolvePreview, DEFAULT_WAIT_SEC } from "../lib/preview.mjs";

/** A clock we control: every call advances by `step` ms, so a bounded wait is tested without
 *  waiting. Injecting the clock (rather than stubbing sleep to resolve instantly) is what keeps
 *  the poll loop from busy-spinning against a real Date.now — measured: it hangs for the full
 *  240s default otherwise, which is how this file first ran for ten minutes. */
function clock(step = 1000) {
  let t = 0;
  return () => (t += step);
}

const SHA = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2";
const OTHER = "9999999999999999999999999999999999999999";
const REPO = "acme/shop";

/** A fake GitHub deployments API. `deployments` is [{id, sha, statuses:[{state, environment_url}]}] */
function fakeApi(deployments) {
  const calls = { deployments: 0, statuses: 0 };
  const fetchImpl = async (url) => {
    const u = String(url);
    if (u.includes("/statuses")) {
      calls.statuses++;
      const id = Number(u.match(/deployments\/(\d+)\/statuses/)?.[1]);
      const d = deployments.find((x) => x.id === id);
      return { ok: true, status: 200, json: async () => d?.statuses ?? [] };
    }
    calls.deployments++;
    return { ok: true, status: 200, json: async () => deployments.map(({ id, sha }) => ({ id, sha })) };
  };
  return { fetchImpl, calls };
}

const ctxEnv = {
  GITHUB_ACTIONS: "true",
  GITHUB_REPOSITORY: REPO,
  GITHUB_TOKEN: "ghs_faketoken",
  GITHUB_SHA: SHA,
  GITHUB_BASE_REF: "main",
  GITHUB_EVENT_NAME: "pull_request",
};

/* ── the trigger stays narrow ─────────────────────────────────────────────────────────────── */

test("it does not activate outside GitHub Actions", () => {
  const c = previewContext({ ...ctxEnv, GITHUB_ACTIONS: undefined });
  assert.equal(c.eligible, false);
  assert.match(c.why, /Actions/i);
});

test("a push build with no pull request keeps today's missing-url error", () => {
  const c = previewContext({ ...ctxEnv, GITHUB_EVENT_NAME: "push", GITHUB_REF: "refs/heads/main" });
  assert.equal(c.eligible, false);
  assert.match(c.why, /pull request/i);
});

test("no token means it says so instead of polling a 401 for four minutes", async () => {
  const r = await resolvePreview({ repo: REPO, sha: SHA, token: "", log: () => {} });
  assert.equal(r.url, "");
  assert.match(r.problem, /GITHUB_TOKEN/);
});

test("inside Actions on a pull request it is eligible, and knows the repo and sha", () => {
  const c = previewContext({ ...ctxEnv, GITHUB_REF: "refs/pull/42/merge" });
  assert.equal(c.eligible, true, c.why);
  assert.equal(c.repo, REPO);
  assert.equal(c.sha, SHA);
});

/* ── it must never test the wrong build ──────────────────────────────────────────────────── */

test("a successful deployment of ANOTHER commit is never used", async () => {
  // The nightmare case: main's preview is up and green, this PR's is not built yet. Returning
  // main's URL produces a confident verdict about code that is not in this pull request.
  const { fetchImpl } = fakeApi([
    { id: 1, sha: OTHER, statuses: [{ state: "success", environment_url: "https://main-preview.vercel.app" }] },
  ]);
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 3000, pollMs: 1000, log: () => {}, sleep: async () => {}, now: clock(),
  });
  assert.equal(r.url, "", `it resolved another commit's preview: ${r.url}`);
});

test("a deployment whose latest status is a FAILURE is never used", async () => {
  // A preview that was up and then fell over: the newest status is what counts, not the fact
  // that a success appears somewhere in the list.
  const { fetchImpl } = fakeApi([
    {
      id: 1, sha: SHA,
      statuses: [
        { state: "failure", environment_url: "https://broken.vercel.app" },
        { state: "success", environment_url: "https://was-fine-earlier.vercel.app" },
      ],
    },
  ]);
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 3000, pollMs: 1000, log: () => {}, sleep: async () => {}, now: clock(),
  });
  assert.equal(r.url, "", `it used a preview whose latest state is failure: ${r.url}`);
});

test("the matching commit's successful preview IS used", async () => {
  const { fetchImpl } = fakeApi([
    { id: 1, sha: OTHER, statuses: [{ state: "success", environment_url: "https://main-preview.vercel.app" }] },
    { id: 2, sha: SHA, statuses: [{ state: "success", environment_url: "https://pr-preview.vercel.app" }] },
  ]);
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 3000, pollMs: 1000, log: () => {}, sleep: async () => {}, now: clock(),
  });
  assert.equal(r.url, "https://pr-preview.vercel.app");
});

test("it waits for a preview that is still building, then uses it", async () => {
  // Polls 1-2 see a pending deployment; poll 3 sees it go green. This is the ordinary case on a
  // real pull request, and the reason a single look is not enough.
  let poll = 0;
  const fetchImpl = async (url) => {
    const u = String(url);
    if (u.includes("/statuses")) {
      poll++;
      const state = poll >= 3 ? "success" : "pending";
      return { ok: true, status: 200, json: async () => [{ state, environment_url: state === "success" ? "https://ready.vercel.app" : undefined }] };
    }
    return { ok: true, status: 200, json: async () => [{ id: 7, sha: SHA }] };
  };
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 60_000, pollMs: 1000, log: () => {}, sleep: async () => {}, now: clock(),
  });
  assert.equal(r.url, "https://ready.vercel.app");
});

/* ── it must never hang, and never guess ─────────────────────────────────────────────────── */

test("giving up is bounded, says what it looked for, and never invents a URL", async () => {
  const { fetchImpl } = fakeApi([]); // no deployments at all, forever
  let polls = 0;
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 5000, pollMs: 1000, log: () => {}, sleep: async () => { polls++; }, now: clock(),
  });

  assert.equal(r.url, "", "a guessed URL is worse than an honest failure");
  assert.ok(r.problem, "giving up must produce a message a human can act on");
  assert.match(r.problem, new RegExp(REPO), "the message must name the repo it asked about");
  assert.match(r.problem, /a1b2c3d/, "the message must name the commit it looked for");
  assert.match(r.problem, /--url|--wait-preview/, "the message must name the way out");
  assert.ok(polls <= 10, `the wait must be bounded; it polled ${polls} times`);
});

test("a fork's read-only token (403) is explained, not crashed on", async () => {
  // Fork pull requests get a token that cannot read deployments. This is common, it is nobody's
  // bug, and the message has to say so instead of reading like an outage.
  const fetchImpl = async () => ({ ok: false, status: 403, json: async () => ({}), text: async () => "Resource not accessible by integration" });
  const r = await resolvePreview({
    repo: REPO, sha: SHA, token: "t", fetchImpl,
    waitMs: 3000, pollMs: 1000, log: () => {}, sleep: async () => {}, now: clock(),
  });
  assert.equal(r.url, "");
  assert.match(r.problem ?? "", /fork|403/i);
});

test("the default wait is a real bound, not infinity", () => {
  assert.ok(Number.isFinite(DEFAULT_WAIT_SEC) && DEFAULT_WAIT_SEC > 0, "an unbounded default would burn a customer's CI minutes");
  assert.ok(DEFAULT_WAIT_SEC <= 900, `${DEFAULT_WAIT_SEC}s is long enough to read as a hang`);
});
