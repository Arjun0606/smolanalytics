import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { HEALTHY_STATUS } from "../lib/plan.mjs";

// `plan check` IS A CI GATE, SO A WRONG PASS/FAIL REDDENS SOMEONE ELSE'S BUILD.
//
// It gated on `status === "ok" || status === "healthy"`. The server has only ever emitted
// "flowing" for a healthy event (internal/mcp/control.go), so every healthy event printed FAIL and
// the command exited 1: install the gate, break the build, on day one. The Go binary's own check
// was correct the whole time, so only the path a hosted or npx user has was broken.
//
// The reason it survived: cli/test/plan.test.mjs builds its payloads by hand with `status: "ok"`,
// a string the server does not produce. A fixture that invents its input tests the fixture.
//
// So this reads the SERVER'S SOURCE and requires the CLI to accept whatever it can emit for a
// healthy event. Add a status in Go without teaching the CLI and this fails here rather than in a
// customer's pipeline.

// The vocabulary moved: it was inline in internal/mcp/control.go until the dashboard needed the
// same verdict and the computation was extracted. This test caught the move, which is the point —
// the strings are a wire contract with somebody's CI, and they must not be able to relocate
// silently.
const control = readFileSync(new URL("../../internal/planhealth/planhealth.go", import.meta.url), "utf8");

test("the CLI accepts the status the server actually sends for a healthy event", () => {
  // The literal assigned to row["status"] on the path where the event WAS seen.
  const emitted = [...control.matchAll(/Status(?:Flowing|Missing)\s*=\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.ok(emitted.length >= 2, `could not read the status vocabulary out of control.go: ${emitted}`);

  const unhealthy = emitted.filter((s) => /missing/i.test(s));
  const healthy = emitted.filter((s) => !/missing/i.test(s));
  assert.ok(healthy.length > 0, "no healthy status found in control.go");

  for (const s of healthy) {
    assert.ok(HEALTHY_STATUS.has(s),
      `the server sends status ${JSON.stringify(s)} for a healthy event and the CLI treats it as broken — plan check would fail a green build`);
  }
  for (const s of unhealthy) {
    assert.ok(!HEALTHY_STATUS.has(s),
      `the CLI treats ${JSON.stringify(s)} as healthy, so a missing event would pass the gate`);
  }
});

test("the Go binary and the npm CLI agree on what passes", () => {
  // Two implementations of one gate. They disagreed for as long as both existed.
  const goGate = readFileSync(new URL("../../cmd/smolanalytics/plan_cmd.go", import.meta.url), "utf8");
  const m = goGate.match(/p\.Status\s*!=\s*"([^"]+)"/);
  assert.ok(m, "could not find the Go binary's plan-check gate");
  assert.ok(HEALTHY_STATUS.has(m[1]),
    `the Go binary passes on ${JSON.stringify(m[1])} and the CLI does not`);
});
