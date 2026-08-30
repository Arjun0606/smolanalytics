// THE TESTING HALF INSIDE THE EDITOR — and the two ways a local MCP server does damage.
//
// `connect` already wires editors to a remote server for the analytics half. That is impossible
// here: a test drives a browser against an app running on this laptop, on this branch, with this
// person's key. So this one speaks stdio and runs where the code does.
//
//   IT TELLS THE AGENT SOMETHING UNTRUE. An agent that reads "passed" for a stale recording tells
//   its user the feature works. The statuses go across unsoftened and untranslated, or this server
//   is worse than not existing — a person can read a terminal and judge for themselves, while an
//   agent will repeat whatever it is handed.
//
//   IT DOES SOMETHING PUBLIC NOBODY ASKED FOR. An agent poking at tools is exploring. Posting a
//   pull request comment, publishing a share link or recording a run against a team's project are
//   things a PERSON chooses. A tool call that leaves a trace outside this machine is a surprise,
//   and surprises in a testing tool get it uninstalled.
//
// And one protocol rule that is cheap to get wrong: STDOUT IS THE STREAM. Anything human-readable
// on stdout corrupts the JSON-RPC framing, and the editor then reports a broken server rather than
// a bad line, which is a genuinely bad hour for whoever debugs it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { handle, callTool, runTests, listTests, TOOLS, PROTOCOL } from "../lib/mcp.mjs";

const r = (name, status, reason = "") => ({ name, status, reason, mode: "replay", ms: 900 });

// runTests calls the PURE runner (runSuite), which returns results rather than an exit code —
// suiteCmd is the layer that prints, comments and notifies, and an exploring agent must not reach
// it. So the double here returns results, and `find` stands in for suite discovery.
const okRun = (results) => async () => results;
const found = (tests) => () => ({ missing: false, errors: [], notes: [], tests });
const someTests = found([{ name: "a", test: "does a thing" }, { name: "b", test: "does another" }]);

/* ── the protocol ────────────────────────────────────────────────────────────────────────────── */

test("initialize answers with the protocol version and the server's name", async () => {
  const res = await handle({ jsonrpc: "2.0", id: 1, method: "initialize" });
  assert.equal(res.result.protocolVersion, PROTOCOL);
  assert.equal(res.result.serverInfo.name, "smolanalytics");
  assert.ok(res.result.capabilities.tools, "a server with tools must say so or the client lists none");
});

test("a notification is never answered", async () => {
  // MCP clients send notifications/initialized with no id. Replying to one is a protocol violation
  // and some clients treat the server as broken from that point on.
  assert.equal(await handle({ jsonrpc: "2.0", method: "notifications/initialized" }), null);
  assert.equal(await handle({ jsonrpc: "2.0", method: "anything", params: {} }), null);
});

test("a malformed message is answered rather than fatal", async () => {
  const res = await handle({ id: 9, method: "initialize" });
  assert.equal(res.error.code, -32600, "one bad client message must not take the server down");
  // JSON-RPC 2.0: when the id cannot be determined from an invalid request, the response MUST
  // carry id null rather than being withheld. Silence would leave a client waiting on a reply that
  // never comes, which is the failure mode this is meant to avoid.
  const garbage = await handle(null);
  assert.equal(garbage.id, null);
  assert.equal(garbage.error.code, -32600);
});

test("an unknown method is an error, not a crash", async () => {
  const res = await handle({ jsonrpc: "2.0", id: 3, method: "resources/list" });
  assert.equal(res.error.code, -32601);
});

test("every tool is described well enough for an agent to pick the right one", () => {
  for (const t of TOOLS) {
    assert.ok(t.description.length > 80, `${t.name} needs a description an agent can choose by`);
    assert.equal(t.inputSchema.type, "object");
  }
  // The one that matters most: an agent must learn from the description alone that a green result
  // does not mean everything was checked.
  const shipTool = TOOLS.find((t) => t.name === "can_i_ship");
  assert.match(shipTool.description, /not checked/i);
});

/* ── it does not lie ─────────────────────────────────────────────────────────────────────────── */

test("stale and errored reach the agent as themselves", async () => {
  const out = await runTests(
    { url: "http://localhost:3000" },
    { runner: okRun([r("a", "passed"), r("b", "stale", "the button was renamed"), r("c", "errored", "no browser")]), find: someTests, env: {} },
  );
  const body = out.content[0].text;
  assert.match(body, /STALE/, "an agent that reads 'passed' here tells its user a broken feature works");
  assert.match(body, /ERRORED/);
  assert.ok(!out.isError, "a suite that ran is not a tool error, whatever the verdicts were");
});

test("the gaps travel with the result, not just the failures", async () => {
  const out = await runTests(
    { url: "http://localhost:3000" },
    { runner: okRun([r("a", "passed"), r("b", "stale")]), find: someTests, env: {} },
  );
  const body = out.content[0].text;
  assert.match(body, /not the same as nothing being wrong/i, "the ship verdict must come with it");
  assert.match(body, /What was NOT checked/);
});

test("what a run cost is always stated", async () => {
  const out = await runTests({ url: "http://x" }, { runner: okRun([r("a", "passed")]), find: someTests, env: {} });
  assert.match(out.content[0].text, /no model calls|model call/, "an agent spending somebody's money must say so");
});

/* ── it does nothing public ──────────────────────────────────────────────────────────────────── */

test("a tool call cannot comment, share, or notify, by construction", async () => {
  // STRUCTURAL, not a flag anybody has to remember. runTests calls runSuite — the pure runner —
  // and every public side effect lives in suiteCmd one layer above it: the pull request comment,
  // the Slack message, the share link. An agent exploring simply has no path to them.
  let seen = null;
  await runTests(
    { url: "http://localhost:3000" },
    { runner: async (opts) => { seen = opts; return [r("a", "passed")]; }, find: someTests, env: {} },
  );
  assert.equal(seen.comment, undefined, "the pure runner takes no comment option at all");
  assert.equal(seen.share, undefined, "nor a share option");
  // Resolved from THIS file rather than the working directory: `node --test` can be invoked from
  // anywhere, and a read that quietly fails makes the assertion below vacuous — the regex would
  // test the string "undefined", find nothing, and pass no matter what the module imports.
  const { readFileSync } = await import("node:fs");
  const { fileURLToPath } = await import("node:url");
  const here = fileURLToPath(new URL("../lib/mcp.mjs", import.meta.url));
  const src = readFileSync(here, "utf8");
  assert.ok(src.includes("export async function runTests"), "the source under inspection did not load, so what follows would prove nothing");
  // The IMPORT, not the word — the file names suiteCmd in a comment explaining why it does not
  // call it, and a blunt substring match fails on its own documentation.
  // The IMPORT, not the word: the file names suiteCmd in a comment explaining why it does NOT
  // call it, so a substring match fails on its own documentation.
  //
  // Read the import lines and look at what they bind. The first version of this used a regex
  // with a word boundary, and the escaping turned \\b into a literal backspace byte — so it
  // hunted for a control character that never appears and could never fail, which is precisely
  // the bug class this project keeps producing.
  const imported = src
    .split("\n")
    .filter((l) => l.startsWith("import "))
    .flatMap((l) => (l.match(/\{([^}]*)\}/) || [, ""])[1].split(","))
    .map((x) => x.trim().split(/ as /)[0])
    .filter(Boolean);
  assert.ok(
    !imported.includes("suiteCmd"),
    `importing suiteCmd puts commenting, notifying and sharing back within reach of an agent. imports: ${imported.join(", ")}`,
  );
});

test("nothing human-readable is written to stdout", async () => {
  // stdout is the JSON-RPC stream; a stray line corrupts framing and the editor blames the server.
  let seen = null;
  await runTests({ url: "http://x" }, { runner: async (o) => { seen = o; return [r("a", "passed")]; }, find: someTests, env: {} });
  const write = seen.log;
  assert.equal(typeof write, "function", "the runner must be given a log that is not console.log");
  // Prove where it goes rather than trusting the name.
  const original = process.stderr.write;
  let toStderr = "";
  process.stderr.write = (s) => { toStderr += s; return true; };
  try { write("hello"); } finally { process.stderr.write = original; }
  assert.match(toStderr, /hello/, "runner output must go to stderr");
});

/* ── the errors an agent can act on ──────────────────────────────────────────────────────────── */

test("a missing url is explained rather than guessed at", async () => {
  const out = await runTests({}, { runner: okRun([]), find: someTests, env: {} });
  assert.equal(out.isError, true);
  assert.match(out.content[0].text, /localhost:3000/, "it must show the shape of the thing it wants");
});

test("a suite with no tests in it points at suggest", async () => {
  const out = await runTests({ url: "http://x" }, { runner: okRun([]), find: found([]), env: {} });
  assert.equal(out.isError, true);
  assert.match(out.content[0].text, /suggest/);
});

test("a runner that returns nothing is named as the runner's fault, not the suite's", async () => {
  // The distinction this product is built on, carried into a tool result: two tests exist, none
  // produced a verdict, so the fault is ours and the message must say so.
  const out = await runTests({ url: "http://x" }, { runner: okRun([]), find: someTests, env: {} });
  assert.equal(out.isError, true);
  assert.match(out.content[0].text, /this is the runner, not your suite/i);
});

test("an empty or missing suite points at suggest", async () => {
  const missing = await listTests({}, { find: () => ({ missing: true, tests: [], errors: [], notes: [] }) });
  assert.match(missing.content[0].text, /suggest/, "the next action belongs in the message");

  const empty = await listTests({}, { find: () => ({ missing: false, tests: [], errors: [], notes: [] }) });
  assert.match(empty.content[0].text, /suggest/);
});

test("list_tests returns the sentence, which is the thing worth reading", async () => {
  const out = await listTests({}, {
    find: () => ({ missing: false, errors: [], notes: [], tests: [{ name: "Checkout works", test: "Open the cart and pay with the saved card." }] }),
  });
  assert.match(out.content[0].text, /Checkout works/);
  assert.match(out.content[0].text, /saved card/);
});

test("an unknown tool name lists the real ones", async () => {
  const out = await callTool("run_everything", {});
  assert.equal(out.isError, true);
  for (const t of TOOLS) assert.match(out.content[0].text, new RegExp(t.name));
});
