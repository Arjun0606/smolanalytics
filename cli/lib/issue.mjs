// FILING THE BUG, WHICH IS ALREADY WRITTEN.
//
// A failure here is not "assertion failed at line 42". It is a sentence naming the page, the
// control, what was expected and what the page did instead, plus the changed file most likely
// responsible and the evidence connecting it to the test. That is a bug report. Somebody then
// copies it into Linear or Jira by hand, which is transcription, and transcription is the kind of
// work this product exists to remove.
//
// THE HARD PART IS NOT CREATING A TICKET. It is not creating forty. A test that fails on every push
// for three days must produce ONE issue, or the integration gets switched off in a week and takes
// the product's credibility with it. So every issue carries a fingerprint of the test it came from,
// and an existing open issue with that fingerprint is updated rather than duplicated.
//
// WHAT IS NEVER FILED, and this is the part that keeps a tracker trustworthy:
//
//   `stale`     a recording stopped fitting. That is our artefact aging, not their bug. Filing it
//               puts a ticket in somebody's sprint for a button they renamed on purpose.
//   `flaky`     it failed and then passed. Nobody can act on that yet, and a ticket saying "this
//               sometimes fails" is the ticket everybody closes as cannot-reproduce.
//   `errored`   our runner could not run. Filing that against their product is a lie about whose
//               fault it is, and it is the fastest way to lose a team's trust.
//
// Only `failed` is a bug about the customer's application, and only `failed` is filed.

export const LINEAR_KEY = "SMOLANALYTICS_LINEAR_API_KEY";
export const LINEAR_TEAM = "SMOLANALYTICS_LINEAR_TEAM_ID";
export const JIRA_URL = "SMOLANALYTICS_JIRA_URL";
export const JIRA_EMAIL = "SMOLANALYTICS_JIRA_EMAIL";
export const JIRA_TOKEN = "SMOLANALYTICS_JIRA_API_TOKEN";
export const JIRA_PROJECT = "SMOLANALYTICS_JIRA_PROJECT";

/**
 * The identity of a bug, and it is deliberately the TEST rather than the run.
 *
 * A run id would make every push a new issue. The failing step would make an issue vanish and
 * reappear when the agent takes a different route to the same broken thing. The sentence is what
 * the person wrote down and what they will recognise, and it survives both.
 */
export function fingerprint(test) {
  const name = String((test && test.name) || "").trim().toLowerCase();
  // A tag rather than a hash: whoever finds this in a tracker six months from now can read it, and
  // can search for it without needing our source to know what to search for.
  return `smolanalytics:${name.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "test"}`;
}

/** Only a `failed` test is a bug about somebody's application. */
export function filable(results = []) {
  return results.filter((r) => r && r.status === "failed");
}

/**
 * The issue body: everything the person opening it needs, and nothing they would have to ask for.
 *
 * The suspect goes in with its evidence attached, never on its own — "src/PayButton.tsx is
 * probably the problem" is a guess somebody has to verify, while "this PR removed the string
 * 'Proceed to checkout' this test clicks" is a fact they can check in one look.
 */
export function issueBody(result, { url = "", commit = "", runUrl = "", shareUrl = "", suite = "tests" } = {}) {
  const out = [];
  out.push(result.reason || "The test did not do what its sentence describes.");
  out.push("");
  out.push(`**The test** — \`${result.file || suite}\``);
  out.push("");
  out.push(`> ${String(result.test || result.name || "").trim()}`);

  const suspects = Array.isArray(result.suspects) ? result.suspects.slice(0, 3) : [];
  if (suspects.length) {
    out.push("");
    out.push("**Most likely responsible**");
    for (const s of suspects) out.push(`- \`${s.file}\` — ${s.evidence}`);
  }

  out.push("");
  const facts = [url && `against ${url}`, commit && `at \`${commit}\``].filter(Boolean).join(" ");
  if (facts) out.push(facts);
  const links = [runUrl && `[the CI run](${runUrl})`, shareUrl && `[the run, opened by anyone](${shareUrl})`].filter(Boolean);
  if (links.length) out.push(links.join(" · "));

  out.push("");
  out.push(`<sub>Opened by smolanalytics. ${fingerprint(result)} — this tag is how the same failure updates this issue instead of opening another.</sub>`);
  return out.join("\n");
}

export function issueTitle(result) {
  const name = String(result.name || "a test").trim();
  return name.length > 120 ? `${name.slice(0, 117)}...` : name;
}

/* ── Linear ──────────────────────────────────────────────────────────────────────────────────── */

async function linearGraphQL(query, variables, { key, fetchImpl, timeoutMs }) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetchImpl("https://api.linear.app/graphql", {
      method: "POST",
      headers: { "content-type": "application/json", authorization: key },
      body: JSON.stringify({ query, variables }),
      signal: ctrl.signal,
    });
    if (!res || !res.ok) return { data: null, problem: `Linear answered ${res ? res.status : "nothing"}` };
    const body = await res.json();
    if (body && body.errors && body.errors.length) return { data: null, problem: `Linear refused: ${body.errors[0].message}` };
    return { data: body && body.data, problem: "" };
  } catch (e) {
    return { data: null, problem: `Linear could not be reached (${e && e.name === "AbortError" ? "no answer in time" : e && e.message})` };
  } finally {
    clearTimeout(timer);
  }
}

/**
 * One failure to Linear: find an open issue carrying this fingerprint, comment on it if it exists,
 * create it if it does not.
 *
 * The search is by the fingerprint in the DESCRIPTION rather than the title, because people rename
 * issue titles and a rename must not orphan the thread and start a second one.
 */
export async function toLinear(result, ctx = {}, { env = process.env, fetchImpl = fetch, timeoutMs = 10_000 } = {}) {
  const key = String(env[LINEAR_KEY] || "").trim();
  const teamId = String(env[LINEAR_TEAM] || "").trim();
  if (!key || !teamId) return { filed: false, why: "" };

  const tag = fingerprint(result);
  const found = await linearGraphQL(
    `query($q: String!) { issues(filter: { description: { contains: $q }, state: { type: { neq: "completed" } } }, first: 1) { nodes { id identifier url } } }`,
    { q: tag },
    { key, fetchImpl, timeoutMs },
  );
  if (found.problem) return { filed: false, why: found.problem };

  const existing = found.data && found.data.issues && found.data.issues.nodes && found.data.issues.nodes[0];
  if (existing) {
    const body = `Failed again${ctx.commit ? ` at \`${ctx.commit}\`` : ""}.\n\n${result.reason || ""}`;
    const said = await linearGraphQL(
      `mutation($id: String!, $body: String!) { commentCreate(input: { issueId: $id, body: $body }) { success } }`,
      { id: existing.id, body },
      { key, fetchImpl, timeoutMs },
    );
    return said.problem
      ? { filed: false, why: said.problem }
      : { filed: true, updated: true, url: existing.url, id: existing.identifier };
  }

  const made = await linearGraphQL(
    `mutation($teamId: String!, $title: String!, $description: String!) { issueCreate(input: { teamId: $teamId, title: $title, description: $description }) { success issue { id identifier url } } }`,
    { teamId, title: issueTitle(result), description: issueBody(result, ctx) },
    { key, fetchImpl, timeoutMs },
  );
  if (made.problem) return { filed: false, why: made.problem };
  const issue = made.data && made.data.issueCreate && made.data.issueCreate.issue;
  return issue ? { filed: true, updated: false, url: issue.url, id: issue.identifier } : { filed: false, why: "Linear accepted the request but returned no issue" };
}

/* ── Jira ────────────────────────────────────────────────────────────────────────────────────── */

/** Jira Cloud: basic auth with an API token, which is the shape Atlassian documents. */
const jiraAuth = (email, token) => `Basic ${Buffer.from(`${email}:${token}`).toString("base64")}`;

async function jiraCall(path, init, { base, auth, fetchImpl, timeoutMs }) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetchImpl(`${base}${path}`, {
      ...init,
      headers: { "content-type": "application/json", accept: "application/json", authorization: auth, ...(init.headers || {}) },
      signal: ctrl.signal,
    });
    if (!res || !res.ok) return { data: null, problem: `Jira answered ${res ? res.status : "nothing"}` };
    return { data: await res.json(), problem: "" };
  } catch (e) {
    return { data: null, problem: `Jira could not be reached (${e && e.name === "AbortError" ? "no answer in time" : e && e.message})` };
  } finally {
    clearTimeout(timer);
  }
}

export async function toJira(result, ctx = {}, { env = process.env, fetchImpl = fetch, timeoutMs = 10_000 } = {}) {
  const base = String(env[JIRA_URL] || "").trim().replace(/\/$/, "");
  const email = String(env[JIRA_EMAIL] || "").trim();
  const token = String(env[JIRA_TOKEN] || "").trim();
  const project = String(env[JIRA_PROJECT] || "").trim();
  if (!base || !email || !token || !project) return { filed: false, why: "" };
  if (!/^https:\/\//i.test(base)) return { filed: false, why: `${JIRA_URL} must be an https URL; nothing was filed.` };

  const auth = jiraAuth(email, token);
  const tag = fingerprint(result);
  // JQL, and the tag is quoted: a test name with a quote in it would otherwise change the query.
  const jql = `project = ${JSON.stringify(project)} AND statusCategory != Done AND text ~ ${JSON.stringify(tag)}`;
  const found = await jiraCall(`/rest/api/3/search?jql=${encodeURIComponent(jql)}&maxResults=1`, { method: "GET" }, { base, auth, fetchImpl, timeoutMs });
  if (found.problem) return { filed: false, why: found.problem };

  const hit = found.data && Array.isArray(found.data.issues) && found.data.issues[0];
  if (hit) {
    const said = await jiraCall(`/rest/api/3/issue/${hit.key}/comment`, {
      method: "POST",
      body: JSON.stringify({ body: adf(`Failed again${ctx.commit ? ` at ${ctx.commit}` : ""}. ${result.reason || ""}`) }),
    }, { base, auth, fetchImpl, timeoutMs });
    return said.problem ? { filed: false, why: said.problem } : { filed: true, updated: true, url: `${base}/browse/${hit.key}`, id: hit.key };
  }

  const made = await jiraCall("/rest/api/3/issue", {
    method: "POST",
    body: JSON.stringify({
      fields: {
        project: { key: project },
        summary: issueTitle(result),
        description: adf(issueBody(result, ctx)),
        issuetype: { name: "Bug" },
      },
    }),
  }, { base, auth, fetchImpl, timeoutMs });
  if (made.problem) return { filed: false, why: made.problem };
  const key = made.data && made.data.key;
  return key ? { filed: true, updated: false, url: `${base}/browse/${key}`, id: key } : { filed: false, why: "Jira accepted the request but returned no issue key" };
}

/** Atlassian Document Format, minimally: Jira Cloud v3 refuses a plain string. */
function adf(text) {
  return {
    type: "doc",
    version: 1,
    content: String(text).split("\n\n").map((para) => ({
      type: "paragraph",
      content: [{ type: "text", text: para.slice(0, 30_000) }],
    })),
  };
}

/* ── the one entry point ─────────────────────────────────────────────────────────────────────── */

/**
 * File every failure that is worth filing, and never let filing matter more than the verdict.
 *
 * Configured trackers only, failures only, one issue per test, and every error swallowed — the
 * exit code was decided before this ran and must survive it.
 */
export async function fileIssues(results = [], ctx = {}, deps = {}) {
  const bugs = filable(results);
  if (!bugs.length) return { filed: [], skipped: "nothing failed, so there is nothing to file" };

  const out = [];
  for (const bug of bugs) {
    for (const [name, fn] of [["linear", toLinear], ["jira", toJira]]) {
      try {
        const res = await fn(bug, ctx, deps);
        if (res.filed || res.why) out.push({ tracker: name, test: bug.name, ...res });
      } catch (e) {
        out.push({ tracker: name, test: bug.name, filed: false, why: `${name} threw (${e && e.message})` });
      }
    }
  }
  return { filed: out };
}

/** One line for the terminal, and a filing failure never reads as a test failure. */
export function issueLine(outcome) {
  const filed = (outcome && outcome.filed) || [];
  if (!filed.length) return "";
  const made = filed.filter((f) => f.filed);
  const failed = filed.filter((f) => !f.filed);
  const parts = [];
  for (const m of made) parts.push(`${m.updated ? "updated" : "opened"} ${m.id || m.tracker}`);
  for (const f of failed) parts.push(f.why);
  return parts.length ? `  ${parts.join("; ")}` : "";
}
