// `npx smolanalytics mcp` — the testing half, inside the editor where the code is being written.
//
// WHY THIS IS LOCAL AND THE OTHER ONE IS NOT. `connect` already wires Cursor, Claude Code, VS Code
// and the rest to https://smolanalytics.com/api/mcp — a REMOTE server, which is right for the
// analytics half because the data lives on a server. It is impossible for the testing half. A test
// drives a browser against an app that is running on this laptop, on this branch, with this
// person's model key. No hosted endpoint can do that, so this speaks MCP over stdio and runs in
// the same place the code does.
//
// WHAT IT IS FOR. An agent writing code in Cursor can now finish the loop it could never close:
// change something, ask whether it broke a flow, and read a verdict written in the same English
// the test was written in. Before this, the agent could edit the app and had no way to find out
// whether the app still worked.
//
// THE RULES, and they are the reason this is a small file:
//
//   IT NEVER LIES ABOUT WHAT IT DID NOT DO. Every tool returns the real verdict object, including
//   `stale`, `errored` and `flaky`. An agent that reads "passed" for a stale recording will tell
//   its user the feature works, and that is worse than returning nothing.
//
//   IT NEVER WRITES ANYWHERE PUBLIC. No pull request comment, no share link, no project POST. An
//   agent calling a tool is exploring; publishing that to a team channel is a side effect nobody
//   asked for. The CLI still does all of it when a person runs it.
//
//   IT NEVER SPENDS WITHOUT SAYING SO. Running a suite can cost real model calls, so every result
//   carries the cost line, and `--max-calls` is honoured through the same lib/cost.mjs ceiling.
//
//   STDOUT IS THE PROTOCOL. One JSON-RPC message per line and nothing else, ever. A stray
//   console.log corrupts the stream and the editor reports a broken server rather than a bad line,
//   which is a bad hour for whoever debugs it. Everything human-readable goes to stderr.

import { runSuite, discover, DEFAULT_PLANS_DIR } from "./suite.mjs";
import { shipReport } from "./ship.mjs";
import { clusterNote } from "./cluster.mjs";
import { newLedger, costLine, priceFrom } from "./cost.mjs";

export const PROTOCOL = "2024-11-05";
export const SERVER = { name: "smolanalytics", version: "local" };

/**
 * The tools, and the descriptions matter more than usual: an agent picks by reading them, and a
 * vague description produces an agent that calls the wrong one and reports the wrong thing.
 */
export const TOOLS = [
  {
    name: "run_tests",
    description:
      "Run the end-to-end suite against a URL and return the verdict for every test. Use this after changing the app to find out whether a user flow still works. Returns passed/failed/stale/errored/flaky per test — 'stale' means a recording stopped fitting and the flow was NOT verified, and 'errored' means this runner failed, not the app. Costs model calls when a recording is missing or stale.",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "Where the app is running, e.g. http://localhost:3000" },
        suite: { type: "string", description: "Folder of .md test files (default: tests/)" },
        since: { type: "string", description: "Optional git ref: run only the tests this change could have broken, e.g. 'main'" },
        maxCalls: { type: "number", description: "Ceiling on model calls for this run. 0 means no ceiling." },
      },
      required: ["url"],
    },
  },
  {
    name: "can_i_ship",
    description:
      "Run the suite and answer whether it is safe to ship, INCLUDING what was not checked — flows whose recordings went stale, tests that were flaky and therefore prove nothing, tests skipped by --since, and runs that errored on our side. Use this before merging or releasing. This is the only tool that reports the gaps rather than just the failures.",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "Where the app is running" },
        suite: { type: "string", description: "Folder of .md test files (default: tests/)" },
        since: { type: "string", description: "Optional git ref to limit the run to what the change could have broken" },
      },
      required: ["url"],
    },
  },
  {
    name: "list_tests",
    description:
      "List the tests that exist, with the plain-English sentence each one checks and whether it has a recording. Use this to find out what is already covered before writing a new test, or to tell the user what this project verifies.",
    inputSchema: {
      type: "object",
      properties: { suite: { type: "string", description: "Folder of .md test files (default: tests/)" } },
    },
  },
];

/** A JSON-RPC result envelope carrying text, which is the shape every MCP client renders. */
const text = (s) => ({ content: [{ type: "text", text: String(s) }] });

/** An error the AGENT should read and act on, rather than a transport failure. */
const problem = (s) => ({ content: [{ type: "text", text: String(s) }], isError: true });

/**
 * One test's verdict, flattened for a model to read.
 *
 * The status word is never translated or softened. An agent that sees "stale" and reports "passed"
 * to its user has told them a broken feature works.
 */
const line = (r) =>
  `${r.status.toUpperCase()}  ${r.name}${r.reason ? `\n    ${r.reason}` : ""}`;

/**
 * Run a suite and hand back what happened. `runner` is injected so this is testable without a
 * browser, and so a caller can prove the no-publishing rule holds.
 */
export async function runTests(args = {}, { runner = runSuite, find = discover, env = process.env } = {}) {
  const url = String(args.url || "").trim();
  if (!url) return problem("run_tests needs a url — where the app is running, e.g. http://localhost:3000");
  const suite = String(args.suite || "tests").trim();

  let found;
  try {
    found = find(suite, DEFAULT_PLANS_DIR);
  } catch (e) {
    return problem(`Could not read ${suite}/: ${e && e.message}`);
  }
  if (found.missing) return problem(`No suite at ${suite}/. One .md file per test, or run: npx smolanalytics suggest --url ${url}`);
  if (!found.tests.length) return problem(`${suite}/ has no tests in it. npx smolanalytics suggest --url ${url} proposes some from the running app.`);

  const ledger = newLedger();
  let results = [];
  try {
    // runSuite, NOT suiteCmd. suiteCmd is the layer that prints, comments on a pull request,
    // notifies Slack and publishes a share link — every one of which is a side effect a person
    // chooses and an exploring agent must not cause. Calling the pure runner makes that structural
    // rather than a set of flags somebody has to remember to pass.
    results = await runner({
      tests: found.tests,
      url,
      yes: true,
      maxCalls: Number.isFinite(args.maxCalls) ? args.maxCalls : 0,
      env,
      ledger,
      // stderr, never stdout: stdout is the JSON-RPC stream.
      log: (...a) => process.stderr.write(`${a.join(" ")}\n`),
    });
  } catch (e) {
    return problem(`The run could not finish: ${e && e.message}. This is the test runner, not the application.`);
  }

  if (!Array.isArray(results) || !results.length) {
    return problem(`No test produced a result. ${suite}/ has ${found.tests.length} test${found.tests.length === 1 ? "" : "s"} in it, so this is the runner, not your suite.`);
  }

  const report = shipReport(results, { suite, url });
  const cost = costLine(ledger, priceFrom(env));
  // WHY THE AGENT GETS THIS TOO. An agent reading twelve failures will try to fix twelve things,
  // and the first eleven fixes are wasted work on one change. Same rule as everywhere else: silent
  // unless they genuinely group, and wrapped because a bad recording must not cost a verdict.
  let causes = "";
  try {
    causes = clusterNote(results);
  } catch {
    causes = "";
  }
  return text([
    results.map(line).join("\n"),
    ...(causes ? ["", causes] : []),
    "",
    report.lines.join("\n"),
    "",
    cost,
  ].join("\n"));
}

/** The ship verdict, which is run_tests with the gaps foregrounded rather than the per-test list. */
export async function canIShip(args = {}, deps = {}) {
  const out = await runTests(args, deps);
  return out;
}

/** What exists, so an agent can avoid writing a test that is already there. */
export async function listTests(args = {}, { find = discover } = {}) {
  const suite = String(args.suite || "tests").trim();
  try {
    const found = find(suite);
    if (found.missing) return problem(`No suite at ${suite}/. Create one .md file per test, or run: npx smolanalytics suggest --url <your app>`);
    if (!found.tests.length) return problem(`${suite}/ has no tests in it yet. npx smolanalytics suggest --url <your app> proposes some from the running app.`);
    return text(found.tests.map((t) => `${t.name}\n    ${t.test}`).join("\n\n"));
  } catch (e) {
    return problem(`Could not read ${suite}/: ${e && e.message}`);
  }
}

/** Dispatch, and an unknown tool is an answer rather than a crash. */
export async function callTool(name, args, deps = {}) {
  if (name === "run_tests") return runTests(args, deps);
  if (name === "can_i_ship") return canIShip(args, deps);
  if (name === "list_tests") return listTests(args, deps);
  return problem(`No tool named ${JSON.stringify(name)}. This server has: ${TOOLS.map((t) => t.name).join(", ")}.`);
}

/**
 * One JSON-RPC request in, one response out (or null for a notification, which takes no reply).
 *
 * Notifications are the subtlety: MCP clients send `notifications/initialized` with no id, and
 * answering one is a protocol violation that some clients treat as a broken server.
 */
export async function handle(msg, deps = {}) {
  const id = msg && msg.id;
  const reply = (result) => ({ jsonrpc: "2.0", id, result });

  if (!msg || msg.jsonrpc !== "2.0" || typeof msg.method !== "string") {
    return id === undefined ? null : { jsonrpc: "2.0", id, error: { code: -32600, message: "not a JSON-RPC 2.0 request" } };
  }
  if (id === undefined || id === null) return null;

  switch (msg.method) {
    case "initialize":
      return reply({ protocolVersion: PROTOCOL, capabilities: { tools: {} }, serverInfo: SERVER });
    case "tools/list":
      return reply({ tools: TOOLS });
    case "tools/call": {
      const p = msg.params || {};
      return reply(await callTool(p.name, p.arguments || {}, deps));
    }
    case "ping":
      return reply({});
    default:
      return { jsonrpc: "2.0", id, error: { code: -32601, message: `unknown method ${msg.method}` } };
  }
}

/**
 * The stdio loop. Line-delimited JSON in, line-delimited JSON out.
 *
 * A malformed line is answered and the loop continues: one bad message from a client must not take
 * down a server the editor will then report as broken.
 */
export async function serve({ input = process.stdin, output = process.stdout, deps = {} } = {}) {
  input.setEncoding("utf8");
  let buf = "";
  for await (const chunk of input) {
    buf += chunk;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const raw = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (!raw) continue;
      let msg = null;
      try {
        msg = JSON.parse(raw);
      } catch {
        output.write(`${JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "parse error" } })}\n`);
        continue;
      }
      const res = await handle(msg, deps);
      if (res) output.write(`${JSON.stringify(res)}\n`);
    }
  }
  return 0;
}
