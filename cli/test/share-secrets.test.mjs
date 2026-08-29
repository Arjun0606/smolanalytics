// THE SECRET THAT WAS NOT IN THE ENVIRONMENT.
//
// A shared run is a URL a stranger can open. Replay.io's bug-report business died on exactly this
// ("replays could contain sensitive data"), so the redaction is the feature — the rest is
// plumbing. This file exists because sixty-one tests passed over a real leak, and the reason they
// did is worth writing down.
//
// `scrub` accepts `secrets` in two shapes. maskSecrets takes {value, token} PAIRS, because that is
// how a recording stays replayable — it writes {{password}} where the value was. But most callers
// hold PLAIN STRINGS: the values `--seed` got back from the customer's endpoint, a token their app
// echoed into the page. Passing a bare string to maskSecrets destructures `value` off it, gets
// undefined, and hits the `continue`. No error, no warning, nothing masked.
//
// It stayed invisible because every existing test supplied secrets that were ALSO in the
// environment, and those travel a different path (`redact` over envSecrets) which does take plain
// strings. So the password, the API key and the run key were all scrubbed correctly in every test,
// while a seeded token passed the same way survived into the bundle.
//
// Which makes this the fifth instance of one bug class in this codebase: an interface mismatch that
// fails silently, covered by tests that all happen to use the working shape. So these tests use the
// shape the CALLERS use, and assert on the serialised bytes rather than on a function's return.

import { test } from "node:test";
import assert from "node:assert/strict";
import { scrub, scrubDeep } from "../lib/share.mjs";

const PW = "SuperSecret-hunter2!";
const KEY = "sk-ant-api03-REALKEYVALUE1234567890";
const SEED = "SEEDSECRET123";
const ESC = String.fromCharCode(27);

/** Only the API key is in the environment. The other two are runtime values, like real ones are. */
const ENV = { ANTHROPIC_API_KEY: KEY };

test("a plain-string secret is masked, even with nothing in the environment backing it", () => {
  // The seeded case, minimal: --seed returns {"token": "..."} and that value goes straight into a
  // sentence, a step label and the page.
  const out = scrub(`the order ${SEED} was opened`, { secrets: [SEED], env: {} });
  assert.ok(!out.includes(SEED), `a bare-string secret survived: ${out}`);
});

test("both shapes work in the same call, because both shapes reach it", () => {
  // Pairs come from the recording layer, bare strings from --seed and auth. A caller mixing them
  // must not silently lose one, which is precisely what happened.
  const text = `fill = ${PW} then open ${SEED}`;
  const out = scrub(text, { secrets: [{ value: PW, token: "{{password}}" }, SEED], env: {} });
  assert.ok(!out.includes(PW), `the pair was not masked: ${out}`);
  assert.ok(!out.includes(SEED), `the bare string was not masked: ${out}`);
  assert.match(out, /\{\{password\}\}/, "a pair keeps its token, so the recording stays replayable");
});

test("the whole bundle is scrubbed, through every channel a secret reaches it by", () => {
  // Asserted on the SERIALISED BYTES, because that is what is posted. A function returning the
  // right thing for one field proves nothing about a nested one.
  const bundle = {
    test: "sign in and check out",
    reason: `the login failed with ${PW}`,
    steps: [
      { label: `fill "Password" = ${JSON.stringify(PW)}` },
      { label: `goto https://app.test/cb?token=${KEY}` },
      { label: `${ESC}[31mfill "Password" = ${PW}${ESC}[0m` },
      { label: `fill = ${PW.toUpperCase()}` },
      { label: `goto https://app.test/x?p=${encodeURIComponent(PW)}` },
      { label: `seeded order ${SEED} opened` },
    ],
    proof: "Signed in",
    headers: { authorization: `Bearer ${KEY}` },
    pageText: `debug: pw=${PW} seed=${SEED}`,
    seeded: { token: SEED },
  };

  const out = JSON.stringify(scrubDeep(bundle, { secrets: [PW, KEY, SEED], env: ENV }));

  for (const [what, needle] of [
    ["the password", PW],
    ["the api key", KEY],
    ["the seeded token", SEED],
    ["the password upper-cased by the app", PW.toUpperCase()],
    ["the password url-encoded into a link", encodeURIComponent(PW)],
  ]) {
    assert.ok(!out.includes(needle), `${what} reached the wire: ${out.slice(0, 300)}`);
  }
});

test("scrubbing leaves a bundle worth opening", () => {
  // The failure mode on the other side: masking so aggressively that the artefact says nothing. A
  // share page whose every line is [redacted] is not a share page.
  const bundle = {
    test: "a shopper can check out",
    proof: "Order placed. Your order number is",
    reason: "clicking Proceed to checkout showed an order number",
    steps: [{ label: `fill "Password" = ${JSON.stringify(PW)}` }, { label: 'click button "Proceed to checkout"' }],
  };
  const j = scrubDeep(bundle, { secrets: [PW], env: {} });

  assert.equal(j.test, "a shopper can check out", "the sentence is the artefact and must survive");
  assert.equal(j.proof, "Order placed. Your order number is");
  assert.match(j.reason, /Proceed to checkout/);
  assert.match(j.steps[1].label, /Proceed to checkout/, "an unrelated step must be untouched");
  assert.ok(!j.steps[0].label.includes(PW));
  assert.match(j.steps[0].label, /Password/, "the field name is context, and is not the secret");
});

test("a secret too short to mask safely does not shred the bundle", () => {
  // Over-masking is its own leak-shaped bug: it destroys the evidence instead of the secret. A
  // three-character value appears inside ordinary words, so it must not become a masking rule.
  const out = scrub("click the Save card button and check the cart", { secrets: ["car", "the"], env: {} });
  assert.match(out, /Save card button/, `over-masking corrupted the text: ${out}`);
  assert.match(out, /cart/);
});
