// FILING THE BUG — and the two things that get an integration switched off.
//
// The bug report is already written: a sentence naming the page, the control, what was expected and
// what happened, plus the changed file most likely responsible with the evidence connecting it to
// the test. Copying that into Linear or Jira by hand is transcription, which is the work this
// product removes. So it files it.
//
//   IT OPENS FORTY TICKETS. A test failing on every push for three days must produce ONE issue.
//   The second duplicate is annoying; the fortieth means somebody turns the integration off and
//   distrusts everything else we do. This is the property most of this file is about.
//
//   IT FILES A BUG THAT IS NOT THEIRS. `stale` is our recording aging. `flaky` is a thing nobody
//   can act on yet. `errored` is our runner failing. Filing any of those against somebody's
//   product is a lie about whose fault it is, and it puts work in a sprint for a button they
//   renamed on purpose.

import { test } from "node:test";
import assert from "node:assert/strict";
import { fingerprint, filable, issueBody, issueTitle, toLinear, toJira, fileIssues, issueLine, LINEAR_KEY, LINEAR_TEAM, JIRA_URL, JIRA_EMAIL, JIRA_TOKEN, JIRA_PROJECT } from "../lib/issue.mjs";

const fail = (name = "A shopper can check out") => ({
  name,
  status: "failed",
  file: "tests/checkout.md",
  test: "Open the cart and pay with the saved card.",
  reason: 'On /cart, clicking "Proceed to checkout" showed "Something went wrong."',
  suspects: [{ file: "src/PayButton.tsx", evidence: 'this PR removed the string "Proceed to checkout" this test clicks' }],
});

const LINEAR_ENV = { [LINEAR_KEY]: "lin_api_x", [LINEAR_TEAM]: "team_x" };
const JIRA_ENV = {
  [JIRA_URL]: "https://acme.atlassian.net",
  [JIRA_EMAIL]: "qa@acme.test",
  [JIRA_TOKEN]: "tok",
  [JIRA_PROJECT]: "ENG",
};

/** A Linear double: `existing` decides whether the search finds an open issue. */
function linear({ existing = null } = {}) {
  const calls = [];
  const fetchImpl = async (_url, init) => {
    const body = JSON.parse(init.body);
    calls.push(body);
    if (body.query.includes("query(")) {
      return { ok: true, status: 200, json: async () => ({ data: { issues: { nodes: existing ? [existing] : [] } } }) };
    }
    if (body.query.includes("commentCreate")) {
      return { ok: true, status: 200, json: async () => ({ data: { commentCreate: { success: true } } }) };
    }
    return { ok: true, status: 200, json: async () => ({ data: { issueCreate: { success: true, issue: { id: "i1", identifier: "ENG-7", url: "https://linear.app/x/ENG-7" } } } }) };
  };
  return { calls, fetchImpl };
}

/* ── only their bugs are filed ───────────────────────────────────────────────────────────────── */

test("only a failed test is a bug about somebody's application", () => {
  const mixed = [fail(), { name: "b", status: "stale" }, { name: "c", status: "flaky" }, { name: "d", status: "errored" }, { name: "e", status: "passed" }];
  assert.deepEqual(filable(mixed).map((r) => r.status), ["failed"]);
});

test("a run with nothing failed files nothing at all", async () => {
  const { fetchImpl, calls } = linear();
  const out = await fileIssues([{ name: "a", status: "passed" }, { name: "b", status: "stale" }], {}, { env: LINEAR_ENV, fetchImpl });
  assert.deepEqual(out.filed, []);
  assert.equal(calls.length, 0, "a stale recording must not reach a tracker at all");
});

/* ── it does not open forty tickets ──────────────────────────────────────────────────────────── */

test("the fingerprint is the TEST, so the same failure is the same issue every time", () => {
  // Not the run, which would make every push a new ticket. Not the failing step, which would make
  // an issue vanish and reappear when the agent takes a different route to the same broken thing.
  assert.equal(fingerprint(fail()), fingerprint({ ...fail(), reason: "a totally different reason" }));
  assert.equal(fingerprint(fail()), fingerprint({ ...fail(), suspects: [] }));
  assert.notEqual(fingerprint(fail("A shopper can check out")), fingerprint(fail("A shopper can search")));
  // Readable, so a person who finds it in a tracker can search for it without reading our source.
  assert.match(fingerprint(fail()), /^smolanalytics:a-shopper-can-check-out$/);
});

test("an existing open issue is commented on, never duplicated", async () => {
  const { fetchImpl, calls } = linear({ existing: { id: "i9", identifier: "ENG-9", url: "https://linear.app/x/ENG-9" } });
  const res = await toLinear(fail(), { commit: "a1b2c3d" }, { env: LINEAR_ENV, fetchImpl });
  assert.equal(res.filed, true);
  assert.equal(res.updated, true, "the second failure of the same test must update, not create");
  assert.equal(res.id, "ENG-9");
  assert.ok(!calls.some((c) => c.query.includes("issueCreate")), "it created a duplicate issue");
});

test("no existing issue means one is created", async () => {
  const { fetchImpl, calls } = linear();
  const res = await toLinear(fail(), {}, { env: LINEAR_ENV, fetchImpl });
  assert.equal(res.filed, true);
  assert.equal(res.updated, false);
  assert.equal(res.id, "ENG-7");
  assert.ok(calls.some((c) => c.query.includes("issueCreate")));
});

test("the search looks in the description, so renaming an issue does not orphan it", async () => {
  // People rename issue titles constantly. If the fingerprint were matched against the title, the
  // next failure would open a second ticket for a bug somebody is already working on.
  const { fetchImpl, calls } = linear();
  await toLinear(fail(), {}, { env: LINEAR_ENV, fetchImpl });
  const query = calls[0];
  assert.match(query.query, /description/);
  assert.equal(query.variables.q, fingerprint(fail()));
});

test("the fingerprint really is in what gets written, or the search can never match it", () => {
  // The two halves have to agree. A tag we search for but never write is a dedupe that silently
  // never fires, and that is exactly how forty tickets happen.
  assert.ok(issueBody(fail(), {}).includes(fingerprint(fail())));
});

/* ── the ticket is worth reading ─────────────────────────────────────────────────────────────── */

test("the body carries the sentence, the reason and the suspect with its evidence", () => {
  const body = issueBody(fail(), { url: "shop.test", commit: "a1b2c3d" });
  assert.match(body, /Something went wrong/, "the failure as written");
  assert.match(body, /Open the cart and pay/, "the sentence the test checks");
  assert.match(body, /src\/PayButton\.tsx/);
  assert.match(body, /removed the string/, "a suspect without its evidence is a guess somebody must verify");
  assert.match(body, /shop\.test/);
  assert.match(body, /a1b2c3d/);
});

test("a long test name is trimmed rather than refused by the tracker", () => {
  const long = issueTitle(fail("x".repeat(300)));
  assert.ok(long.length <= 120, `${long.length} characters will be rejected or silently truncated`);
  assert.match(long, /\.\.\.$/);
});

/* ── it never matters more than the verdict ──────────────────────────────────────────────────── */

test("nothing configured means nothing attempted and no noise", async () => {
  const { fetchImpl, calls } = linear();
  const out = await fileIssues([fail()], {}, { env: {}, fetchImpl });
  assert.deepEqual(out.filed, []);
  assert.equal(calls.length, 0);
  assert.equal(issueLine(out), "", "a project with no tracker configured must print nothing");
});

test("every tracker failure is reported and swallowed", async () => {
  for (const [what, fetchImpl] of [
    ["a refusal", async () => ({ ok: false, status: 401, json: async () => ({}) })],
    ["a network error", async () => { throw new Error("ECONNRESET"); }],
    ["nonsense back", async () => ({ ok: true, status: 200, json: async () => ({ data: null }) })],
    ["graphql errors", async () => ({ ok: true, status: 200, json: async () => ({ errors: [{ message: "no access" }] }) })],
  ]) {
    const out = await fileIssues([fail()], {}, { env: LINEAR_ENV, fetchImpl });
    assert.ok(Array.isArray(out.filed), `${what}: it must report rather than throw`);
    assert.ok(out.filed.every((f) => !f.filed), what);
    assert.ok(issueLine(out).length > 0, `${what}: the reader should be told it did not file`);
  }
});

test("a tracker that throws outright cannot escape", async () => {
  const out = await fileIssues([fail()], {}, {
    env: LINEAR_ENV,
    fetchImpl: () => { throw new Error("boom"); },
  });
  assert.ok(out.filed.every((f) => !f.filed));
});

/* ── Jira ────────────────────────────────────────────────────────────────────────────────────── */

test("an http Jira URL is refused rather than used", async () => {
  let called = false;
  const res = await toJira(fail(), {}, {
    env: { ...JIRA_ENV, [JIRA_URL]: "http://acme.atlassian.net" },
    fetchImpl: async () => { called = true; return { ok: true, status: 200, json: async () => ({}) }; },
  });
  assert.equal(called, false, "credentials must not cross an unencrypted connection");
  assert.match(res.why, /https/);
});

test("Jira dedupes on the same fingerprint, and quotes it into the query", async () => {
  const urls = [];
  const fetchImpl = async (url, init) => {
    urls.push(url);
    if (String(url).includes("/search")) return { ok: true, status: 200, json: async () => ({ issues: [{ key: "ENG-4" }] }) };
    return { ok: true, status: 200, json: async () => ({ key: "ENG-4" }) };
  };
  const res = await toJira(fail(), {}, { env: JIRA_ENV, fetchImpl });
  assert.equal(res.updated, true, "an open Jira issue with this tag must be commented on");
  assert.equal(res.id, "ENG-4");
  const search = decodeURIComponent(urls[0]);
  assert.match(search, new RegExp(fingerprint(fail())));
  // A test name containing a quote would otherwise change the meaning of the JQL.
  assert.match(search, /text ~ "/);
});

test("Jira gets Atlassian Document Format, because v3 refuses a plain string", async () => {
  let created = null;
  const fetchImpl = async (url, init) => {
    if (String(url).includes("/search")) return { ok: true, status: 200, json: async () => ({ issues: [] }) };
    created = JSON.parse(init.body);
    return { ok: true, status: 200, json: async () => ({ key: "ENG-5" }) };
  };
  await toJira(fail(), {}, { env: JIRA_ENV, fetchImpl });
  assert.equal(created.fields.description.type, "doc");
  assert.equal(created.fields.description.version, 1);
  assert.ok(Array.isArray(created.fields.description.content));
  assert.equal(created.fields.issuetype.name, "Bug");
});
