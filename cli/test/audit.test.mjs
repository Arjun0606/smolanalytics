// `audit` is the product's first impression: one command, no account, someone's real repo. So the
// bar is not "finds things" — it is "finds nothing it cannot defend". A report padded with lines
// that are not actions gets dismissed once and never run again, which is worse than not shipping.
//
// Every case below is a real line from a real codebase.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { compile, flatten, render as renderPage, stalenessNote } from "../lib/test.mjs";
import { scanFile, auditRepo, render, VENDORS } from "../lib/audit.mjs";

const kinds = (rel, src) => scanFile(rel, src).map((a) => a.kind);

test("finds real auth call sites", () => {
  const src = `
    const res = await signIn.magicLink({ email, callbackURL: dest });
    await signUp.email({ name, email, password });
    await signOut();
  `;
  const k = kinds("app/AuthForm.tsx", src);
  assert.ok(k.includes("login"), "missed signIn.magicLink(");
  assert.ok(k.includes("signup"), "missed signUp.email(");
  assert.ok(k.includes("logout"), "missed signOut()");
});

// A STOREFRONT IS THE CASE THIS COMMAND MOST NEEDS TO GET RIGHT, AND IT SAW NOTHING.
//
// Every payment pattern was a SERVER-side SDK call: stripe.checkout.sessions.create, charges.create,
// paymentIntents.create. MEASURED on a three-button React fixture — an add-to-cart, a
// stripe.redirectToCheckout() and a Paddle.Checkout.open() — the whole report was:
//
//   No recognisable user-facing actions found here.
//
// Payment is the finding this report ranks first, and the shop that most needs it got a blank page
// in the thirty seconds the command has to be believed.
test("client-side commerce is an action, because that is where a lot of shops take the money", () => {
  const src = `
    <button onClick={() => addToCart(item)}>Add to cart</button>
    <button onClick={async () => { await stripe.redirectToCheckout({ sessionId }); }}>Buy</button>
    <button onClick={() => Paddle.Checkout.open({ product: 123 })}>Subscribe</button>
  `;
  const k = kinds("app/Cart.jsx", src);
  assert.ok(k.includes("add to cart"), "missed addToCart(");
  assert.ok(k.includes("payment"), "missed a client-side checkout");
  assert.equal(k.filter((x) => x === "payment").length, 2, "one of the two checkout calls was missed");
});

test("a payment outranks an add to cart, which outranks a signup", () => {
  const found = scanFile("a.tsx", `
    await signUp.email({ email });
    addToCart(item);
    await stripe.redirectToCheckout({ sessionId });
  `);
  const sorted = [...found].sort((a, b) => a.weight - b.weight).map((a) => a.kind);
  assert.deepEqual(sorted, ["payment", "add to cart", "signup"]);
});

test("the same guards apply: a handler DECLARED is not a handler CALLED", () => {
  // The whole file's rule. `<button onClick={handleAddToCart}>` has no paren and is not a call,
  // and a declaration is where the action is written down rather than performed.
  for (const line of [
    "function handleCheckout() {",
    "const handleCheckout = async () => {",
    "export function addToCart(item) {",
    'import { addToCart } from "./cart";',
    "// call addToCart() when they click",
    "<button onClick={handleAddToCart}>Add</button>",
    "type CreateOrderArgs = { id: string };",
  ]) {
    assert.deepEqual(scanFile("x.tsx", line), [], `reported a non-action: ${line}`);
  }
});

test("a client-side checkout with tracking beside it is covered, not a finding", () => {
  const src = `
    async function buy() {
      await stripe.redirectToCheckout({ sessionId });
      posthog.capture("checkout");
    }
  `;
  assert.equal(scanFile("a.tsx", src).find((a) => a.kind === "payment").covered, true);
});

test("PROSE is not an action, even when it contains the word", () => {
  // Verbatim from app/install.md/route.ts, a route that SERVES documentation. The paren belongs to
  // "(14-day trial)", not to a call, and this shipped as a finding until it was caught by running
  // the audit on a real repo rather than reading the patterns.
  const src = `- If they don't: tell them to sign up at \${base}/signup (14-day trial at Pro limits, no card),`;
  assert.deepEqual(scanFile("app/install.md/route.ts", src), [], "prose was reported as a user action");
});

test("imports, comments and type declarations are not actions", () => {
  const cases = [
    `import { signOut } from "@/lib/auth-client";`,
    `// call signIn() when the user submits`,
    `type SignUpArgs = { email: string };`,
    `export { signIn } from "./auth";`,
  ];
  for (const line of cases) {
    assert.deepEqual(scanFile("x.ts", line), [], `reported a non-action: ${line}`);
  }
});

test("a route handler IS the action, because declaring it is doing it", () => {
  assert.ok(kinds("app/api/x/route.ts", `export async function POST(req: Request) {`).includes("api mutation"));
});

test("tracking within a dozen lines marks an action covered", () => {
  const near = `
    async function onSubmit() {
      await signUp.email({ email });
      smolanalytics.track("signup", { plan });
    }
  `;
  const far = `
    async function onSubmit() {
      await signUp.email({ email });
    }
    ${"\n".repeat(20)}
    smolanalytics.track("something_else");
  `;
  assert.equal(scanFile("a.tsx", near).find((a) => a.kind === "signup").covered, true);
  assert.equal(scanFile("b.tsx", far).find((a) => a.kind === "signup").covered, false, "a call twenty lines away does not cover it");
});

test("another vendor's tracking counts as covered, because the question is whether it is measured", () => {
  const src = `
    await signUp.email({ email });
    posthog.capture("signup");
  `;
  assert.equal(scanFile("a.tsx", src).find((a) => a.kind === "signup").covered, true);
});

test("named actions outrank generic ones, so the report leads with what costs most", () => {
  const src = `
    <form onSubmit={x}>
    await signUp.email({ email });
  `;
  const found = scanFile("a.tsx", src);
  const signupIdx = found.findIndex((a) => a.kind === "signup");
  const formIdx = found.findIndex((a) => a.kind === "form submit");
  assert.ok(signupIdx >= 0 && formIdx >= 0);
  const sorted = [...found].sort((a, b) => a.weight - b.weight);
  assert.equal(sorted[0].kind, "signup", "a form submit outranked a signup");
});

test("a repo with nothing recognisable says so, and does not invent findings", () => {
  const io = {
    readdirSync: () => [{ name: "notes.txt", isDirectory: () => false }],
    readFileSync: () => "just some prose about signing up",
    existsSync: () => true,
  };
  const r = auditRepo("/fake", io);
  assert.equal(r.actions.length, 0);
  const lines = [];
  render(r, (l) => lines.push(l));
  assert.ok(lines.join("\n").includes("No recognisable user-facing actions"));
});

// The property: the headline states a COUNT of real findings and keeps the line that explains why
// they matter. It used to assert the exact phrase "1 of 1 user-facing actions", which pinned the
// padded-total wording rather than the property, and would have blocked the fix for it.
test("the headline states a count of findings and why they matter", () => {
  const io = {
    readdirSync: () => [{ name: "a.tsx", isDirectory: () => false }],
    readFileSync: () => `await signUp.email({ email });`,
    existsSync: () => true,
  };
  const lines = [];
  render(auditRepo("/fake", io), (l) => lines.push(l));
  const out = lines.join("\n");
  assert.match(out, /1 thing your product does/, `the headline must count the finding: ${out.split("\n")[1]}`);
  assert.ok(out.includes("looks identical to one nobody performed"), "lost the line that says why it matters");
});

// LEAD WITH THE SIGNAL. A skeptical reader shown "70 of 70 user-facing actions have no tracking"
// with six real findings buried under 64 rows of "form submit" did not say "wow", they said "this
// thing pads". The named findings ARE the product; the shapes are a footnote.
test("the headline counts the named findings, not the padded total", () => {
  const io = {
    readdirSync: () => [{ name: "a.tsx", isDirectory: () => false }],
    readFileSync: () => `
      await signUp.email({ email });
      <form onSubmit={x}>
      <form onSubmit={y}>
      <form onSubmit={z}>
    `,
    existsSync: () => true,
  };
  const lines = [];
  render(auditRepo("/fake", io), (l) => lines.push(l));
  const out = lines.join("\n");
  assert.match(out, /^\s*\x1b\[1m1 thing your product does that nothing is measuring/m,
    `headline should count the 1 named finding, not the 4 total: ${out.split("\n")[1]}`);
  assert.ok(!/4 of 4/.test(out), "the padded total is back in the headline");
});

test("the bulk shapes are one line, not a section, unless --all", () => {
  const io = {
    readdirSync: () => [{ name: "a.tsx", isDirectory: () => false }],
    readFileSync: () => `<form onSubmit={x}>\n<form onSubmit={y}>`,
    existsSync: () => true,
  };
  const brief = [];
  render(auditRepo("/fake", io), (l) => brief.push(l));
  assert.match(brief.join("\n"), /mostly not worth an event/);
  assert.ok(!/a\.tsx:1/.test(brief.join("\n")), "bulk rows were listed without --all");

  const full = [];
  render(auditRepo("/fake", io), (l) => full.push(l), true);
  assert.match(full.join("\n"), /a\.tsx:1/, "--all did not expand the bulk");
});

test("a fully instrumented repo is told so, rather than shown a zero", () => {
  const io = {
    readdirSync: () => [{ name: "a.tsx", isDirectory: () => false }],
    readFileSync: () => `await signUp.email({ email });\nsmolanalytics.track("signup");`,
    existsSync: () => true,
  };
  const lines = [];
  render(auditRepo("/fake", io), (l) => lines.push(l));
  assert.match(lines.join("\n"), /already has tracking/);
});

// WE OFFER TO MAINTAIN THE ANALYTICS THEY ALREADY HAVE.
//
// The audit is the free front door, and the sentence under "Next:" is the only offer it makes. A
// generic "your agent can write these track() calls" reads, to someone already paying for PostHog,
// as an invitation to adopt a second analytics tool — which is the migration nobody wants and the
// reason "I'd rather just use the free PostHog" is the standing objection. The offer has to name
// the SDK in their repo and promise to write into it.
//
// These fixtures mirror the Go ones in internal/instrument/vendor_test.go. The CLI ships with no
// network and no binary, so its detector is a deliberate second implementation; keeping the
// fixtures identical is what stops the two from drifting apart unnoticed.

const repoIo = (pkg, files) => ({
  readdirSync: () => Object.keys(files).map((name) => ({ name, isDirectory: () => false })),
  readFileSync: (p) => {
    if (String(p).endsWith("package.json")) return pkg;
    const hit = Object.entries(files).find(([name]) => String(p).endsWith(name));
    return hit ? hit[1] : "";
  },
  existsSync: () => true,
});

test("detects the analytics SDK the repo already uses", () => {
  const cases = [
    [`{"dependencies":{"posthog-js":"1.0.0"}}`, `posthog.capture("x")`, "posthog", "PostHog"],
    [`{"dependencies":{"mixpanel-browser":"2.0.0"}}`, `mixpanel.track("x")`, "mixpanel", "Mixpanel"],
    [`{"dependencies":{"@amplitude/analytics-browser":"2.0"}}`, `amplitude.track("x")`, "amplitude", "Amplitude"],
    [`{"dependencies":{"@segment/analytics-next":"1.0"}}`, `analytics.track("x")`, "segment", "Segment"],
    [`{"dependencies":{"next":"14"}}`, `gtag("event", "x")`, "ga4", "Google Analytics 4"],
    [`{"dependencies":{"next":"14"}}`, `const x = 1;`, "smolanalytics", "smolanalytics"],
  ];
  for (const [pkg, src, wantId, wantName] of cases) {
    const r = auditRepo("/fake", repoIo(pkg, { "a.tsx": src }));
    assert.equal(r.vendor.id, wantId, `${wantName}: detected ${r.vendor.id}`);
  }
});

test("our own SDK is not mistaken for Segment", () => {
  // `analytics.track(` matches the tail of `smolanalytics.track(`. Without a word boundary every
  // repo running our own SDK reads as a Segment customer, and we then write Segment calls into it.
  const r = auditRepo("/fake", repoIo(`{"dependencies":{"next":"14"}}`, {
    "a.tsx": `smolanalytics.track("signup");`,
  }));
  assert.equal(r.vendor.id, "smolanalytics", "smolanalytics.track() was read as Segment");
});

test("an installed dependency outweighs a call left over from a migration", () => {
  const r = auditRepo("/fake", repoIo(`{"dependencies":{"posthog-js":"1.0.0"}}`, {
    "a.tsx": `mixpanel.track("old_a")`,
    "b.tsx": `mixpanel.track("old_b")`,
    "c.tsx": `mixpanel.track("old_c")`,
  }));
  assert.equal(r.vendor.id, "posthog", "stale calls outvoted the dependency they actually installed");
});

test("the offer names their SDK and writes into it, not beside it", () => {
  const r = auditRepo("/fake", repoIo(`{"dependencies":{"posthog-js":"1.0.0"}}`, {
    "invite.tsx": `const res = await inviteUser({ email });`,
  }));
  const out = [];
  render(r, (l) => out.push(l));
  const text = out.join("\n").replace(/\x1b\[\d+m/g, "");
  assert.match(text, /This repo uses PostHog/, `no vendor named in the offer:\n${text}`);
  assert.match(text, /posthog\.capture\("invite_sent"/, "the example is not in their SDK");
  assert.match(text, /into PostHog, not beside it/, "the no-second-SDK promise is missing");
  assert.ok(!/smolanalytics\.track/.test(text), "offered our SDK to a PostHog repo");
});

test("a repo with no analytics is still offered ours", () => {
  const r = auditRepo("/fake", repoIo(`{"dependencies":{"next":"14"}}`, {
    "invite.tsx": `const res = await inviteUser({ email });`,
  }));
  const out = [];
  render(r, (l) => out.push(l));
  const text = out.join("\n").replace(/\x1b\[\d+m/g, "");
  assert.match(text, /Your coding agent can write these track\(\) calls/, `fallback offer missing:\n${text}`);
  assert.ok(!/This repo uses/.test(text), "claimed a vendor in a repo that has none");
});

test("tracking written in any SDK counts as tracking", () => {
  // The false-negative direction: telling someone with working PostHog tracking that nothing in
  // their product is measured is a confident falsehood about their own codebase, delivered as our
  // first impression.
  for (const call of [
    `posthog.capture("signup");`,
    `mixpanel.track("signup");`,
    `amplitude.track("signup");`,
    `gtag("event", "sign_up");`,
    `analytics.track("signup");`,
    `smolanalytics.track("signup");`,
  ]) {
    const r = auditRepo("/fake", repoIo(`{}`, { "a.tsx": `await signUp.email({ email });\n${call}` }));
    assert.equal(r.uncovered, 0, `${call} was not recognised as tracking`);
  }
});

test("the CLI's vendor list matches the engine's", () => {
  // The CLI ships with no network and no binary, so its detector must be a second implementation
  // of internal/instrument/vendor.go. Two implementations drift, and the failure is quiet: the
  // engine learns a new SDK, the free audit keeps telling those users nothing measures their
  // product. Identical fixtures do not catch that on their own — this does.
  const src = readFileSync(new URL("../../internal/instrument/vendor.go", import.meta.url), "utf8");
  const goIds = [...src.matchAll(/^\t\tID: "([a-z0-9]+)",/gm)].map((m) => m[1]).sort();
  const jsIds = VENDORS.map((v) => v.id).sort();
  assert.deepEqual(jsIds, goIds,
    `vendor lists have drifted — engine: ${goIds.join(", ")} / cli: ${jsIds.join(", ")}`);
});

// `npx smolanalytics test` — THE SIXTY-SECOND PATH.
//
// The competing product's onboarding, timed on a real repo: a GitHub App across every repository,
// an agent pushing a Dockerfile into your code, questions about which of FRED_API_KEY /
// BRAVE_SEARCH_API_KEY / AWS_ACCESS_KEY_ID to set, SUPABASE_URL and OPENAI_API_KEY handed over, a
// web + backend + Postgres build, then "ETA ~1h 13m". Twenty-eight minutes in it read 3%, then 0%,
// then step 1 of 7 failed and the preview selection errored out.
//
// This command exists so ours is: a URL you already have, a sentence, a verdict. These guards pin
// the three properties that make that claim true rather than aspirational.

test("the parser reads roles, names, values and states out of an aria snapshot", () => {
  // Captured from a live page, not imagined — the whole cost argument rests on reading the page as
  // text rather than paying for a vision call per step.
  const snap = [
    '- heading "Sign in" [level=1]',
    "- text: Email",
    '- textbox "Email": a@b.com',
    '- button "Sign in"',
    '- button "Disabled thing" [disabled]',
    '- checkbox "Remember me" [checked]',
  ].join("\n");
  const { elements } = flatten(snap);
  const by = (n) => elements.find((e) => e.name === n);
  assert.equal(by("Email").role, "textbox");
  assert.equal(by("Email").value, "a@b.com", "a filled field must show its value so the agent does not retype it");
  assert.match(by("Disabled thing").state, /disabled/);
  assert.ok(!elements.some((e) => e.name === "Email" && e.role === "text"), "prose was offered as a control");
});

test("truncation is reported, never silent", () => {
  // A model that cannot see the element it needs must be told to scroll. Dropping the tail silently
  // makes it conclude the element does not exist and fail a working app.
  const many = Array.from({ length: 30 }, (_, i) => `- button "b${i}"`).join("\n");
  const { elements, truncated } = flatten(many, 10);
  assert.equal(elements.length, 10);
  assert.equal(truncated, 20);
  assert.match(renderPage({ url: "u", title: "t", elements, truncated, text: "" }), /20 more not shown/);
});

test("compiling keeps what worked and refuses to produce an empty plan", () => {
  const steps = [
    { ok: true, action: { kind: "fill", text: "SAVE10" }, target: { role: "textbox", name: "Discount code" } },
    { ok: false, action: { kind: "click" }, target: { role: "button", name: "Save for later" } },
    { ok: true, action: { kind: "click" }, target: { role: "button", name: "Apply code" } },
  ];
  const plan = compile("http://x/", steps, "Order placed");
  assert.equal(plan.steps.length, 2, "the agent's dead end was baked into the recording");
  // An empty plan would "pass" instantly by exercising nothing — the most dangerous artefact here.
  assert.equal(compile("http://x/", [], "Order placed"), null);
  assert.equal(compile("http://x/", [{ ok: false, action: { kind: "click" } }], "Order placed"), null);
});

test("a stale recording is never worded as a bug", () => {
  // "The button was renamed" and "the button is gone" are indistinguishable from a replay, and
  // guessing wrong pages somebody at 2am over a copy change.
  const note = stalenessNote({ at: 2, step: { kind: "click", role: "button", name: "Proceed to checkout" }, detail: "Timeout 10000ms exceeded." });
  assert.match(note, /Proceed to checkout/);
  assert.match(note, /not yet a bug/);
  assert.ok(!/\bfail/i.test(note), `a staleness note must not read as a failure: ${note}`);
});

// THE AUDIT MAY NEVER CLAIM COVERAGE IT DID NOT OBSERVE.
//
// Measured in a repo with a payment call, a signup form and no analytics of any kind — both
// actions written in a shape no pattern matches, so the named list came back empty. The headline,
// in bold, first line on the screen:
//
//   Every named user action here already has tracking near it.
//   Checked signups, logins, payments, invites, uploads and shares across 2 files.
//
// Not one thing in that repo was measured by anything. `named` is the UNCOVERED named actions, and
// empty means either "all of them are covered" or "there were none" — two opposite facts collapsed
// into the reassuring one. The block under it then offered to write "these track() calls", pointing
// at an empty list, and with a vendor detected it named an event the repo has no code for.
const audited = (files) => {
  const names = Object.keys(files);
  const io = {
    readdirSync: () => names.map((name) => ({ name, isDirectory: () => false })),
    readFileSync: (p) => {
      const hit = names.find((n) => String(p).endsWith(n));
      if (!hit) throw new Error(`ENOENT ${p}`);
      return files[hit];
    },
    existsSync: () => true,
  };
  const lines = [];
  render(auditRepo("/fake", io), (l) => lines.push(l));
  return lines.join("\n");
};

test("no named actions is never reported as everything being covered", () => {
  // A <form> and a POST route: real shapes, both GENERIC, so there is no named action anywhere.
  const out = audited({
    "signup.tsx": `export default function S() {\n  return <form onSubmit={go}><button>Create account</button></form>;\n}\n`,
    "route.ts": `export async function POST(req) {\n  return Response.json({ ok: true });\n}\n`,
  });
  assert.ok(
    !/already has tracking/.test(out),
    `claimed coverage over a repo where nothing named was found at all:\n${out}`,
  );
  assert.match(out, /No signup, login, payment, invite, upload or share found/, out);
  assert.match(out, /run this from there/, "the reader is one wrong directory away, and must be told");
});

test("and offers nothing to write, because there is nothing to write", () => {
  const out = audited({ "route.ts": `export async function POST(req) {\n  return Response.json({});\n}\n` });
  assert.ok(!/these track\(\) calls/.test(out), `offered to write calls for an empty list:\n${out}`);
  assert.ok(!/capture\("signup"/.test(out), `invented an event the repo has no code for:\n${out}`);
  assert.ok(!/Next:/.test(out), `a pitch under "nothing found" is padding:\n${out}`);
});

test("named actions that ARE all covered still get the sentence that says so", () => {
  // The other half of the fork, and the reason it cannot simply be deleted: this claim is TRUE
  // here, and someone with working tracking must not be told their product is unmeasured.
  const out = audited({
    "auth.ts": `import posthog from "posthog-js";\nexport async function go(auth) {\n  const r = await auth.signIn({ email });\n  posthog.capture("login", {});\n  return r;\n}\n`,
  });
  assert.match(out, /Every named user action here already has tracking near it/, out);
  assert.ok(!/thing your product does that nothing is measuring/.test(out), out);
});

test("an uncovered named action still leads with the count and the vendor's own call", () => {
  // The guard against fixing the above by making every branch say "nothing found".
  const out = audited({
    "pay.ts": `export async function pay(stripe) {\n  return stripe.checkout.sessions.create({ mode: "payment" });\n}\n`,
  });
  assert.match(out, /1 thing your product does that nothing is measuring/, out);
  assert.match(out, /Next:/, out);
  assert.match(out, /smolanalytics connect/, out);
});

// WALKED 2026-08-30, on a repo carrying posthog-js, mixpanel-browser and @segment/analytics-next
// with a live posthog.capture() and a live mixpanel.track():
//
//   This repo uses PostHog. Your coding agent can write these as posthog.capture("signup", { … })
//
// PostHog and Mixpanel had scored an identical 11, and the tie went to PostHog because it is first
// in the VENDORS array. So the one sentence in the whole report a reader can check against their
// own repo in a second was a coin flip stated as a fact, and a Mixpanel shop reads it as evidence
// that we did not understand their codebase.
test("every analytics SDK in the repo is named, not whichever one won the array order", () => {
  const io = repoIo(`{"dependencies":{"posthog-js":"1.0.0","mixpanel-browser":"2.0.0","@segment/analytics-next":"1.0"}}`, {
    "checkout.tsx": `posthog.capture("checkout_completed");`,
    "team.tsx": `mixpanel.track("invite_sent");`,
    // Uncovered, and in its own file, or there is nothing for the offer to be about.
    "invite.tsx": `const r = await inviteUser({ email });`,
  });
  const r = auditRepo("/fake", io);
  const out = [];
  render(r, (l) => out.push(l));
  const text = out.join("\n").replace(/\x1b\[\d+m/g, "");
  // The requirement is that no vendor actually present is left out. Naming only the winner is the
  // bug; naming a vendor that is NOT in the repo would be the same bug facing the other way.
  for (const name of ["PostHog", "Mixpanel", "Segment"]) {
    assert.match(text, new RegExp(`This repo uses[^.]*${name}`), `${name} is in this repo and the offer does not say so:\n${text}`);
  }
  assert.ok(!/Amplitude|Google Analytics/.test(text), `named a vendor the repo does not have:\n${text}`);
  // The snippet still has to be written in exactly one of them, or it is not pasteable.
  assert.match(text, /posthog\.capture\("invite_sent"/, `the example is not in a real SDK:\n${text}`);
  assert.match(text, /into PostHog, not beside it/, text);
});

test("one vendor is still named on its own, with no list punctuation", () => {
  const r = auditRepo("/fake", repoIo(`{"dependencies":{"mixpanel-browser":"2.0.0"}}`, {
    "invite.tsx": `const res = await inviteUser({ email });`,
  }));
  const out = [];
  render(r, (l) => out.push(l));
  const text = out.join("\n").replace(/\x1b\[\d+m/g, "");
  assert.match(text, /This repo uses Mixpanel\./, `a single vendor grew a list around it:\n${text}`);
});

// THE UPGRADE IS THE REVENUE EVENT, and nothing matched it.
//
// This file's own ranking says "Payment first because an untracked checkout is revenue you cannot
// attribute". On a SaaS the plan change is that same money, and audit had no pattern for it: a
// fixture with upgradePlan(), startTrial() and changePlan() reported nothing at all.
//
// Compound forms only. A bare `subscribe(` is an event emitter far more often than a plan change,
// and lib/audit.mjs's header records what a wildcard costs: 497 findings on one real repo, every
// one of them noise.

const suggests = (src) => scanFile("src/Billing.jsx", src).map((f) => f.suggest);

test("a plan change is found, in the shapes people actually write", () => {
  for (const src of [
    "const go = () => upgradePlan(p);",
    "await upgradeSubscription(orgId);",
    "startTrial();",
    "await changePlan('pro');",
    "handleSubscribe(plan);",
  ]) {
    assert.ok(suggests(src).includes("plan_upgraded"), `missed the money event in: ${src}`);
  }
});

test("and a bare subscribe() is left alone, because it is usually an event emitter", () => {
  for (const src of ["emitter.subscribe(fn);", "store.subscribe(render);", "observable$.subscribe(next);"]) {
    assert.ok(!suggests(src).includes("plan_upgraded"), `false positive on: ${src}`);
  }
});
