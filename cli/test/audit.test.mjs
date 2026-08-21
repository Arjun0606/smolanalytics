// `audit` is the product's first impression: one command, no account, someone's real repo. So the
// bar is not "finds things" — it is "finds nothing it cannot defend". A report padded with lines
// that are not actions gets dismissed once and never run again, which is worse than not shipping.
//
// Every case below is a real line from a real codebase.

import { test } from "node:test";
import assert from "node:assert/strict";
import { scanFile, auditRepo, render } from "../lib/audit.mjs";

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

test("the headline states the ratio, which is the whole point", () => {
  const io = {
    readdirSync: () => [{ name: "a.tsx", isDirectory: () => false }],
    readFileSync: () => `await signUp.email({ email });`,
    existsSync: () => true,
  };
  const lines = [];
  render(auditRepo("/fake", io), (l) => lines.push(l));
  const out = lines.join("\n");
  assert.match(out, /1 of 1 user-facing actions have no tracking/);
  assert.ok(out.includes("looks identical to one nobody performed"));
});
