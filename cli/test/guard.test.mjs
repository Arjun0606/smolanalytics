// WHAT THIS REPOSITORY NEEDS THAT NOTHING DECLARES.
//
// The check earns its place on every pull request only if it is silent on a healthy repo, so most
// of this file is about the things it must NOT say. Measured across nine real repositories before
// a line of lib/guard.mjs existed: four of seven report nothing at all, and cabbge-bot — which
// declares 149 variables — reports nothing despite reading twelve. That silence is the feature.
//
// THE PRECISION IS THE FALLBACK RULE. `process.env.PORT || 3000` cannot break a deploy. On
// smolanalytics-cloud, 78 reads become 15 findings because 38 of them carry a fallback and are
// therefore choices rather than requirements. Without that rule the report is forty items long,
// which is the length at which nobody reads item one.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { missingConfig, requiredNames, declaredNames, hasFallback, guardLines, isTestPath } from "../lib/guard.mjs";

/** A throwaway repo on disk: { "src/a.ts": "…", ".env.example": "…" }. */
function repo(files) {
  const root = mkdtempSync(path.join(tmpdir(), "guard-"));
  for (const [rel, body] of Object.entries(files)) {
    const full = path.join(root, rel);
    mkdirSync(path.dirname(full), { recursive: true });
    writeFileSync(full, body);
  }
  return root;
}
const names = (root) => missingConfig(root).map((m) => m.name).sort();

/* ── the fallback rule, which is the whole precision ─────────────────────────────────────────── */

describe("a read with a way out is a choice, not a requirement", () => {
  test("|| and ?? and a default argument are all fallbacks", () => {
    for (const line of [
      'const p = process.env.API_BASE || "https://x.test";',
      "const p = process.env.API_BASE ?? defaults.base;",
      'os.getenv("API_BASE", "https://x.test")',
      'const p = process.env.API_BASE ? one : two;',
      "if (!process.env.API_BASE) return;",
    ]) {
      assert.equal(hasFallback(line, "API_BASE"), true, `read as required: ${line}`);
    }
  });

  test("a bare read is a requirement", () => {
    for (const line of [
      "const key = process.env.STRIPE_SECRET;",
      'key := os.Getenv("STRIPE_SECRET")',
      'self.key = os.getenv("STRIPE_SECRET")',
      "headers.authorization = `Bearer ${process.env.STRIPE_SECRET}`;",
    ]) {
      assert.equal(hasFallback(line, "STRIPE_SECRET"), false, `read as optional: ${line}`);
    }
  });

  test("and only the required, undeclared one is reported", () => {
    const root = repo({
      "src/a.ts": 'const a = process.env.OPTIONAL_THING || "x";\nconst b = process.env.REQUIRED_THING;\n',
    });
    assert.deepEqual(names(root), ["REQUIRED_THING"]);
    rmSync(root, { recursive: true, force: true });
  });
});

/* ── every way it must stay quiet ────────────────────────────────────────────────────────────── */

describe("the silences that keep it installed", () => {
  test("a variable named in ANY config or doc counts as declared", () => {
    // Which file told the deployer is not our business. Being strict here manufactures findings.
    for (const decl of [".env.example", "README.md", "fly.toml", "Dockerfile", "docker-compose.yml"]) {
      const root = repo({ "src/a.ts": "const k = process.env.SOME_TOKEN;", [decl]: "SOME_TOKEN=\n" });
      assert.deepEqual(names(root), [], `${decl} should count as declaring it`);
      rmSync(root, { recursive: true, force: true });
    }
  });

  test("a GitHub workflow declares it too, which is where deployed apps really keep them", () => {
    const root = repo({
      "src/a.ts": "const k = process.env.DEPLOY_TOKEN;",
      ".github/workflows/ci.yml": "jobs:\n  x:\n    env:\n      DEPLOY_TOKEN: ${{ secrets.DEPLOY_TOKEN }}\n",
    });
    assert.deepEqual(names(root), []);
    rmSync(root, { recursive: true, force: true });
  });

  test("the platform's own variables are never reported", () => {
    const root = repo({
      "src/a.ts": "const a = process.env.NODE_ENV;\nconst b = process.env.GITHUB_TOKEN;\nconst c = process.env.VERCEL_URL;\n",
    });
    assert.deepEqual(names(root), [], "nobody writes PATH into a .env.example");
    rmSync(root, { recursive: true, force: true });
  });

  test("a test's own environment is the test's business", () => {
    const root = repo({
      "test/a.test.ts": "const k = process.env.TEST_ONLY_KEY;",
      "src/b.ts": "const j = process.env.REAL_KEY;",
    });
    assert.deepEqual(names(root), ["REAL_KEY"]);
    rmSync(root, { recursive: true, force: true });
  });

  test("a commented-out read is not a read", () => {
    const root = repo({ "src/a.ts": "// const k = process.env.OLD_KEY;\n# const j = process.env.OTHER\n" });
    assert.deepEqual(names(root), []);
    rmSync(root, { recursive: true, force: true });
  });

  test("a healthy repo produces no output at all", () => {
    const root = repo({ "src/a.ts": 'const k = process.env.API_KEY;', ".env.example": "API_KEY=\n" });
    assert.deepEqual(guardLines(missingConfig(root)), [], "a check that speaks on green teaches people to skip it");
    rmSync(root, { recursive: true, force: true });
  });
});

/* ── it has to work on the repos it claims to work on ────────────────────────────────────────── */

describe("any language it can read as text", () => {
  test("finds the read in Go, Python, Ruby, PHP, Java and Rust", () => {
    const root = repo({
      "main.go": 'key := os.Getenv("GO_SECRET")',
      "app.py": 'k = os.getenv("PY_SECRET")',
      "app.rb": 'k = ENV["RB_SECRET"]',
      "app.php": '$k = getenv("PHP_SECRET");',
      "App.java": 'String k = System.getenv("JAVA_SECRET");',
      "main.rs": 'let k = std::env::var("RUST_SECRET");',
    });
    assert.deepEqual(names(root), ["GO_SECRET", "JAVA_SECRET", "PHP_SECRET", "PY_SECRET", "RB_SECRET", "RUST_SECRET"]);
    rmSync(root, { recursive: true, force: true });
  });

  test("a name read in several places is required if ANY read has no fallback", () => {
    // One careful call site does not make the careless one safe.
    const root = repo({
      "src/a.ts": 'const a = process.env.SHARED || "x";',
      "src/b.ts": "const b = process.env.SHARED;",
    });
    assert.deepEqual(names(root), ["SHARED"]);
    rmSync(root, { recursive: true, force: true });
  });

  test("scripts are separated from the application, not hidden", () => {
    const root = repo({ "scripts/deploy.ts": "const k = process.env.DEPLOY_ONLY;" });
    const miss = missingConfig(root);
    assert.equal(miss.length, 1);
    assert.equal(miss[0].tooling, true, "a deploy script is not the running app and must not read as one");
    assert.match(guardLines(miss).join("\n"), /scripts or tooling/);
    rmSync(root, { recursive: true, force: true });
  });
});

/* ── it can never be the thing that breaks ───────────────────────────────────────────────────── */

describe("reading a repository is not allowed to fail a build", () => {
  test("an unreadable or absent directory returns nothing rather than throwing", () => {
    assert.doesNotThrow(() => missingConfig("/definitely/not/a/path/here"));
    assert.deepEqual(missingConfig("/definitely/not/a/path/here"), []);
  });

  test("a binary file in the tree is skipped, not parsed", () => {
    const root = repo({ "src/a.ts": "const k = process.env.REAL_ONE;" });
    writeFileSync(path.join(root, "src", "blob.js"), Buffer.from([0x00, 0x01, 0x02, 0xff]));
    assert.deepEqual(names(root), ["REAL_ONE"]);
    rmSync(root, { recursive: true, force: true });
  });

  test("the report names the file and line, so a reader can go straight there", () => {
    const root = repo({ "src/pay.ts": "\n\nconst k = process.env.PAY_SECRET;\n" });
    const out = guardLines(missingConfig(root)).join("\n");
    assert.match(out, /PAY_SECRET/);
    assert.match(out, /src\/pay\.ts:3/, "a finding without a location is a search, not a report");
    assert.match(out, /starts and then fails/, "the consequence, not just the fact");
    rmSync(root, { recursive: true, force: true });
  });

  test("isTestPath knows the conventions of the languages we scan", () => {
    for (const p of ["test/a.ts", "src/__tests__/b.ts", "a.test.ts", "b_test.go", "test_c.py", "FooTests.swift"]) {
      assert.equal(isTestPath(p), true, `${p} should read as test code`);
    }
    for (const p of ["src/latest.ts", "src/contest.go", "lib/protest.py"]) {
      assert.equal(isTestPath(p), false, `${p} is not test code — the word merely contains "test"`);
    }
  });
});

test("with nothing missing from the app, the tooling section stands on its own", () => {
  // MEASURED on a real repo: with zero application findings the report opened with a blank line
  // and the words "2 more", referring to a first group that was never printed.
  const root = repo({ "scripts/deploy.ts": "const k = process.env.DEPLOY_ONLY;" });
  const out = guardLines(missingConfig(root));
  assert.ok(!/\bmore\b/.test(out.join("\n")), `"more" with no first group: ${out.join(" / ")}`);
  assert.equal(out[0].startsWith(""), true);
  assert.match(out.join("\n"), /Nothing the deployed application needs is missing/);
  rmSync(root, { recursive: true, force: true });
});

test("and with both, the application comes first and the tooling is clearly secondary", () => {
  const root = repo({
    "src/app.ts": "const a = process.env.APP_SECRET;",
    "scripts/deploy.ts": "const b = process.env.DEPLOY_ONLY;",
  });
  const out = guardLines(missingConfig(root)).join("\n");
  assert.ok(out.indexOf("APP_SECRET") < out.indexOf("DEPLOY_ONLY"), "the deploy-breaking one must be read first");
  assert.match(out, /2 more|1 more/);
  rmSync(root, { recursive: true, force: true });
});
