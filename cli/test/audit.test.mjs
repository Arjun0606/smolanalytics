// `audit` is the product's first impression: one command, no account, someone's real repo. So the
// bar is not "finds things" — it is "finds nothing it cannot defend". A report padded with lines
// that are not actions gets dismissed once and never run again, which is worse than not shipping.
//
// Every case below is a real line from a real codebase.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
