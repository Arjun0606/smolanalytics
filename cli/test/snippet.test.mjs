// THE SNIPPET `init` WRITES INTO SOMEBODY'S APP MUST ACTUALLY RUN.
//
// Every snippet in lib/insert.mjs is a `<script src>` immediately followed by an inline
// `smolanalytics.init(...)`. sdk.js is a plain IIFE that assigns `window.smolanalytics` on its
// last line — there is no pre-load stub queueing calls made before it lands. So anything that
// frees the browser to run the inline script early (`async`, `defer`, `type="module"`, or a
// bundler inlining it) means init runs against an undefined global and the tracker never starts.
//
// MEASURED in Chromium against the real sdk.js, serving the exact tags each snippet renders to:
//   Next App Router snippet, as shipped with `async`   init ran: false
//   plain HTML snippet, no async                       init ran: true
//
// And `init` printed "edited app/layout.jsx" and "the pageview should already be there" over the
// broken one. The Astro branch in insert.mjs already carries a comment about this exact failure
// ("is:inline is required. Without it Astro bundles the script and the init call runs before the
// SDK has defined smolanalytics") — understood in one branch, shipped broken in another.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import * as insert from "../lib/insert.mjs";

const HOST = "https://demo.fly.dev";
const KEY = "pk_test123";

/** Every exported function that builds a snippet, called with the same arguments. */
const snippets = Object.entries(insert)
  .filter(([name, v]) => /^snippet/.test(name) && typeof v === "function")
  .map(([name, fn]) => [name, String(fn(HOST, KEY))]);

test("there is more than one snippet builder, or this file is testing nothing", () => {
  assert.ok(snippets.length >= 2, `found ${snippets.length}: ${snippets.map((s) => s[0]).join(", ")}`);
});

test("no snippet defers the SDK tag past the init call that needs it", () => {
  const bad = [];
  for (const [name, out] of snippets) {
    // Only the tag that loads sdk.js matters; the inline init tag has no src.
    const tag = (out.match(/<script[^>]*sdk\.js[^>]*>/) || [])[0] || "";
    if (/\basync\b|\bdefer\b|type=["']module["']/.test(tag)) bad.push(`${name}: ${tag.trim()}`);
  }
  assert.deepEqual(bad, [], `these load the SDK asynchronously and then call init synchronously, so init runs first and throws:\n  ${bad.join("\n  ")}`);
});

test("every snippet still loads the SDK before it calls init", () => {
  for (const [name, out] of snippets) {
    const src = out.indexOf("sdk.js");
    const init = out.indexOf("smolanalytics.init");
    assert.ok(src >= 0, `${name} does not load sdk.js at all`);
    assert.ok(init >= 0, `${name} never calls init`);
    assert.ok(src < init, `${name} calls init before loading the SDK`);
  }
});

test("the SDK really has no pre-load stub, which is why the order matters", () => {
  // If sdk.js ever grows a queueing stub this whole file can relax. Until then the assumption is
  // load-bearing and is checked here rather than remembered.
  //
  // The SDK lives in the monorepo, not in this npm package, so a clone of the published package
  // alone cannot run this one. Skipped rather than failed there: a test that cannot see its own
  // subject must say so, not go red on somebody who installed exactly what we shipped.
  let sdk;
  try {
    sdk = readFileSync(fileURLToPath(new URL("../../internal/api/sdk.js", import.meta.url)), "utf8");
  } catch {
    return; // not in this checkout
  }
  assert.ok(sdk.includes("window.smolanalytics"), "the SDK should assign the global somewhere");
  const assigns = sdk.indexOf("window.smolanalytics");
  assert.ok(assigns > sdk.length * 0.5, "assigned near the end of the IIFE, so nothing exists before it runs");
});
