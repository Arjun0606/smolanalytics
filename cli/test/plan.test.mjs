// The CI gate's contract: a broken planned event fails the build, an unreachable server fails the
// build, and untidy-but-working never does. Each case watched fail before it was trusted.

import { test, describe } from "node:test";
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
  // CHANGED FROM 1. The rationale on this assertion — "must never report success" — is satisfied
  // by 2, which is not success. The specific value was the stricter part, and it was wrong: the
  // documented contract is that 1 means the customer's application is broken, and an instance we
  // never reached says nothing about their application. `desk` in this same binary already
  // returned 2 for the identical condition.
  assert.equal(code, 2, "cannot reach the server: never success, and never their app's fault either");
});

test("no key stops with instructions rather than calling anything", async () => {
  let called = false;
  const code = await planCheckCmd({ url: "", key: "", log: () => {}, fetchImpl: async () => { called = true; } });
  // CHANGED FROM 1, same reason: nothing was asked and no event was measured, so this cannot be
  // the claim "a planned event stopped firing".
  assert.equal(code, 2);
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

// ── AN OUTAGE ON OUR SIDE IS NEVER A VERDICT ABOUT THEIR TRACKING ───────────────────────────────
//
// `plan check` exists to fail CI when a planned event stops firing, so its 1 is read as exactly
// that claim. It was also returning 1 when it could not reach the instance at all, when the key
// was refused, and when no key was given — none of which has looked at a single event.
//
// MEASURED, same binary, same unreachable host, before the fix:
//   plan check --key sa_bogus --url https://nope.invalid   exit 1
//   desk       --key sa_bogus --url https://nope.invalid   exit 2
//
// lib/desk.mjs had the rule written down already: "an unreachable instance is our side of the
// fence, never a verdict about anybody's product."

describe("plan check separates our outage from their regression", () => {
  const unreachable = async () => { const e = new Error("getaddrinfo ENOTFOUND nope.invalid"); throw e; };
  const refused = async () => { const e = new Error("401 from https://x/mcp"); e.refusal = true; throw e; };

  test("an unreachable instance is exit 2, not a failed build", async () => {
    const code = await planCheckCmd({ url: "https://nope.invalid", key: "sa_x", log: () => {}, fetchImpl: unreachable });
    assert.equal(code, 2, "1 would say their tracking regressed; we never reached the server");
  });

  test("a refusal stays 1, because the server answered and that answer is the verdict", async () => {
    // Deliberately unchanged. This branch also carries "no tracking plan declared yet", which is a
    // real build failure and is pinned by a test above. Only an instance we never reached is ours.
    const code = await planCheckCmd({ url: "https://x", key: "sa_bad", log: () => {}, fetchImpl: refused });
    assert.equal(code, 1);
  });

  test("no key at all is exit 2, because nothing was measured", async () => {
    const code = await planCheckCmd({ url: "https://x", key: "", log: () => {} });
    assert.equal(code, 2);
  });

  test("but a genuine drift verdict is still 1, or the gate would be useless", () => {
    // gate() is the only thing here that has actually looked at the events.
    const drift = JSON.stringify({ planned: [{ name: "signup", status: "missing", missing_properties: [] }] });
    assert.equal(gate(drift, () => {}), 1, "a planned event that stopped firing must still redden the build");
    const healthy = JSON.stringify({ planned: [{ name: "signup", status: "flowing", missing_properties: [] }] });
    assert.equal(gate(healthy, () => {}), 0);
  });
});
