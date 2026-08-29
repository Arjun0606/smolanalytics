// `npx smolanalytics desk` — A READ-ONLY COMMAND STILL HAS A CONTRACT TO KEEP.
//
// This command shipped in the published binary with no tests at all, and the first thing running it
// found was a real defect: both of its failure paths exited 1.
//
// One is not a free number in this CLI. Every command shares one meaning for it, and the README
// sells that split to buyers: "0 nothing failed, 1 a test failed, 2 the runner could not finish. A
// pipeline that gates on 1 alone never reddens a build because our side had an outage." A missing
// key or an unreachable instance is unambiguously our side of that fence, so exiting 1 turned a
// network blip into a statement about somebody's application — the exact promise the contract makes.
//
// The other rules this command must keep, and each is here because breaking it would be quiet:
//
//   IT NEVER WRITES. It reads and prints. A reporting command that mutates something surprises you
//   once and is never run again.
//   IT NEVER LEAKS THE KEY. A read key in terminal output ends up in a CI log, and CI logs are
//   forever.
//   A FAILURE SAYS WHAT TO DO. "fetch failed" alone is not actionable at 2am.

import { test } from "node:test";
import assert from "node:assert/strict";
import { deskCmd } from "../lib/desk.mjs";

const KEY = "sa_readkey_9f3c2b81aa";

/** Collect everything the command prints, the way a terminal or a CI log would see it. */
function recorder() {
  const lines = [];
  return { lines, log: (...parts) => lines.push(parts.join(" ")), text: () => lines.join("\n") };
}

/* ── the exit contract, which is the thing a pipeline actually consumes ──────────────────────── */

test("no key is the runner's problem, so it exits 2 and never 1", async () => {
  const r = recorder();
  const code = await deskCmd({ url: "https://x.test", key: "", project: "", log: r.log });
  assert.equal(code, 2, "1 means a test failed, which is a claim about the customer's application");
  assert.match(r.text(), /read key/i, "and it has to say what is missing");
});

test("an unreachable instance is the runner's problem too", async () => {
  const r = recorder();
  const code = await deskCmd({
    url: "https://x.test", key: KEY, project: "", log: r.log,
    fetchImpl: async () => { throw new Error("fetch failed"); },
  });
  assert.equal(code, 2);
  assert.match(r.text(), /could not reach/i);
});

test("a successful read exits 0", async () => {
  const r = recorder();
  const code = await deskCmd({
    url: "https://x.test", key: KEY, project: "", log: r.log,
    fetchImpl: async () => ({ ok: true, status: 200, text: async () => JSON.stringify({ jsonrpc: "2.0", id: 1, result: { content: [{ type: "text", text: "Nothing notable since Friday." }] } }) }),
  });
  assert.equal(code, 0, `a working read must not report failure: ${r.text()}`);
});

/* ── it must not put the key where a CI log can keep it ──────────────────────────────────────── */

test("the read key never appears in anything printed", async () => {
  // Every path, not just the happy one: the failure messages are the ones most likely to echo the
  // request back at you, and they are the ones that get pasted into an issue.
  for (const fetchImpl of [
    async () => { throw new Error(`fetch failed for https://x.test?key=${KEY}`); },
    async () => ({ ok: false, status: 401, text: async () => `bad key ${KEY}`, json: async () => ({}) }),
    async () => ({ ok: true, status: 200, text: async () => JSON.stringify({ jsonrpc: "2.0", id: 1, result: { content: [{ type: "text", text: "fine" }] } }) }),
  ]) {
    const r = recorder();
    await deskCmd({ url: "https://x.test", key: KEY, project: "", log: r.log, fetchImpl });
    assert.ok(!r.text().includes(KEY), `the key leaked into output: ${r.text().slice(0, 200)}`);
  }
});

/* ── it reads, and only reads ────────────────────────────────────────────────────────────────── */

test("it never sends a request that could change anything", async () => {
  const methods = [];
  await deskCmd({
    url: "https://x.test", key: KEY, project: "", log: () => {},
    fetchImpl: async (_u, init) => {
      methods.push(String(init?.method || "GET").toUpperCase());
      return { ok: true, status: 200, text: async () => JSON.stringify({ jsonrpc: "2.0", id: 1, result: { content: [{ type: "text", text: "fine" }] } }) };
    },
  });
  // POST is how this API is called, but the tool it names must be a read. What must never appear is
  // a request whose *purpose* is mutation — asserted at the layer this file can see, the verb.
  assert.ok(methods.every((m) => m === "GET" || m === "POST"), `unexpected verb: ${methods.join(",")}`);
  assert.ok(!methods.includes("DELETE") && !methods.includes("PUT") && !methods.includes("PATCH"));
});

test("a refusal from the server is reported as itself, not as a crash", async () => {
  const r = recorder();
  const err = Object.assign(new Error("that key cannot read this project"), { refusal: true });
  const code = await deskCmd({
    url: "https://x.test", key: KEY, project: "other", log: r.log,
    fetchImpl: async () => { throw err; },
  });
  assert.equal(code, 2);
  assert.match(r.text(), /cannot read this project/);
  assert.ok(!/could not reach/.test(r.text()), "a refusal is not a network failure and must not be described as one");
});
