// The CI gate's contract: a broken planned event fails the build, an unreachable server fails the
// build, and untidy-but-working never does. Each case watched fail before it was trusted.

import { test } from "node:test";
import assert from "node:assert/strict";

// These payloads now use "flowing", the status the SERVER ACTUALLY SENDS. They previously used
// "ok", which instrumentation_health has never emitted, and that invention hid a live bug for as
// long as it existed: the gate accepted only "ok"/"healthy", so `plan check` failed the build for
// every healthy event on every real instance. A fixture that makes up its input tests the fixture.
// The vocabulary itself is pinned against the server's source in plan-status.test.mjs.
import { gate, parseToolText, endpointFor, planCheckCmd } from "../lib/plan.mjs";

const payload = (planned, unplanned = []) => JSON.stringify({ planned, unplanned_events: unplanned });

test("a planned event that stopped firing fails the build", () => {
  const code = gate(payload([
    { event: "signup", status: "flowing", count: 120 },
    { event: "checkout", status: "missing", count: 0 },
  ]), () => {});
  assert.equal(code, 1);
});

test("all planned events firing passes", () => {
  const code = gate(payload([
    { event: "signup", status: "flowing", count: 120 },
    { event: "checkout", status: "flowing", count: 44 },
  ]), () => {});
  assert.equal(code, 0);
});

test("a planned event missing a property fails, even when the event itself fires", () => {
  const code = gate(payload([{ event: "signup", status: "flowing", count: 90, missing_properties: ["plan"] }]), () => {});
  assert.equal(code, 1, "a half-instrumented event is a broken event");
});

test("events firing outside the plan are reported but never fatal", () => {
  const lines = [];
  const code = gate(payload([{ event: "signup", status: "flowing", count: 5 }], ["debug_click"]), (l) => lines.push(l));
  assert.equal(code, 0, "tracking something you did not write down is untidy, not broken");
  assert.ok(lines.join("\n").includes("debug_click"), "but it must be reported");
});

test("an empty plan fails rather than passing vacuously", () => {
  assert.equal(gate(payload([]), () => {}), 1);
});

test("an unreachable server fails the gate instead of passing green", async () => {
  const code = await planCheckCmd({
    url: "https://nope.invalid", key: "sa_x", log: () => {},
    fetchImpl: async () => { throw new Error("ECONNREFUSED"); },
  });
  assert.equal(code, 1, "a gate that cannot reach the server must never report success");
});

test("no key stops with instructions rather than calling anything", async () => {
  let called = false;
  const code = await planCheckCmd({ url: "", key: "", log: () => {}, fetchImpl: async () => { called = true; } });
  assert.equal(code, 1);
  assert.equal(called, false);
});

test("SSE-framed tool responses parse the same as plain JSON", () => {
  const inner = JSON.stringify({ jsonrpc: "2.0", id: 1, result: { content: [{ type: "text", text: '{"planned":[]}' }] } });
  assert.equal(parseToolText(inner), '{"planned":[]}');
  assert.equal(parseToolText(`event: message\ndata: ${inner}\n\n`), '{"planned":[]}');
});

test("endpointFor appends /mcp once and defaults to the cloud org endpoint", () => {
  assert.equal(endpointFor("https://x.fly.dev"), "https://x.fly.dev/mcp");
  assert.equal(endpointFor("https://x.fly.dev/mcp"), "https://x.fly.dev/mcp");
  assert.equal(endpointFor(""), "https://smolanalytics.com/api/mcp");
});

// The server knows why it refused. Say what it said, rather than "unreadable payload" — measured
// against the live demo, which answers "no tracking plan declared yet" and used to come out as
// gibberish with no next step in it.
test("a tool refusal surfaces the server's own reason", async () => {
  const lines = [];
  const body = JSON.stringify({
    jsonrpc: "2.0", id: 1,
    result: { isError: true, content: [{ type: "text", text: "no tracking plan declared yet — set one with set_tracking_plan" }] },
  });
  const code = await planCheckCmd({
    url: "https://x.fly.dev", key: "k", log: (l) => lines.push(l),
    fetchImpl: async () => ({ ok: true, status: 200, text: async () => body }),
  });
  assert.equal(code, 1);
  const out = lines.join("\n");
  assert.ok(out.includes("no tracking plan declared yet"), `lost the server's reason: ${out}`);
  assert.ok(!out.includes("could not reach"), `a refusal was reported as a transport failure: ${out}`);
});
