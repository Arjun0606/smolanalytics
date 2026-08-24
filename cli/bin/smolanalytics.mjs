#!/usr/bin/env node
// `npx smolanalytics init` — wire analytics into the app in this directory.
//
// Zero dependencies on purpose. Every package listed here is something npx has to download
// before the person sees a single character of output, and the whole value of a one-command
// install is that it feels instant.
//
// The rule this file follows: never touch a file without saying exactly which file and what
// will change, and never leave a half-edit behind. Someone runs this against a repo they
// care about, usually for the first time, usually while deciding whether to trust us at all.

import fs from "node:fs";
import path from "node:path";
import readline from "node:readline/promises";
import { stdin, stdout } from "node:process";
import { detect } from "../lib/detect.mjs";
import { applyStrategy, upsertEnv, snippetHtml, MANUAL_SNIPPETS } from "../lib/insert.mjs";
import { connectCmd } from "../lib/connect.mjs";
import { planCheckCmd } from "../lib/plan.mjs";
import { auditCmd } from "../lib/audit.mjs";
import { deskCmd } from "../lib/desk.mjs";
import { testCmd } from "../lib/test.mjs";
import { suiteCmd, DEFAULT_PLANS_DIR } from "../lib/suite.mjs";

const C = {
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  bold: (s) => `\x1b[1m${s}\x1b[0m`,
  green: (s) => `\x1b[32m${s}\x1b[0m`,
  yellow: (s) => `\x1b[33m${s}\x1b[0m`,
  red: (s) => `\x1b[31m${s}\x1b[0m`,
};

const realFs = {
  exists: (p) => fs.existsSync(path.resolve(p)),
  read: (p) => fs.readFileSync(path.resolve(p), "utf8"),
};

function flag(name) {
  const i = process.argv.indexOf(`--${name}`);
  if (i !== -1 && process.argv[i + 1] && !process.argv[i + 1].startsWith("--")) return process.argv[i + 1];
  const eq = process.argv.find((a) => a.startsWith(`--${name}=`));
  return eq ? eq.slice(name.length + 3) : undefined;
}
const hasFlag = (name) => process.argv.includes(`--${name}`);

function help() {
  console.log(`
${C.bold("smolanalytics")} — end-to-end tests without test code

  ${C.bold("npx smolanalytics test")}        one sentence, a real browser, a verdict
  ${C.dim("--url  <url>")}          staging, a deploy preview, anything reachable
  ${C.dim("--test \"<text>\"")}       what should work, in plain English
  ${C.dim("--plan <file>")}         replay the recording; wake the agent only if it stopped fitting
  ${C.dim("--headed")}              watch it happen

  ${C.dim("--suite <dir>")}         a folder of .md files, one sentence per test
  ${C.dim("--comment")}             post the verdicts on the pull request (GitHub Actions)
  ${C.dim("--plans <dir>")}         where recordings are kept (default: ${DEFAULT_PLANS_DIR})
  ${C.dim("No account. No GitHub app. Nothing written to your repo.")}

  ${C.bold("npx smolanalytics audit")}       what your app does that nothing is measuring
  ${C.dim("[dir]")}                 repo to scan (default: here). No account, no network.

  ${C.bold("npx smolanalytics desk")}        what it found, in your terminal\n  ${C.dim("--key <api key>")}       or SMOLANALYTICS_KEY\n\n  ${C.bold("npx smolanalytics init")}        wire the tracker into this app

  ${C.dim("--key   <write key>")}   public ingest key (or SMOLANALYTICS_WRITE_KEY)
  ${C.dim("--host  <url>")}         your instance, e.g. https://you.fly.dev
  ${C.dim("--yes")}                 don't ask before editing
  ${C.dim("--print")}               print the snippet, change nothing

  ${C.bold("npx smolanalytics connect")}     wire the MCP server into every assistant you have
  ${C.dim("[editor]")}              or just one: cursor, claude-code, vscode, windsurf, claude-desktop, cline
  ${C.dim("--key   <org token>")}   the API token from smolanalytics.com → Settings
  ${C.dim("--url   <endpoint>")}    one instance instead of the org (default: smolanalytics.com/api/mcp)

  ${C.bold("npx smolanalytics plan check")}  fail CI when a planned event stops firing
  ${C.dim("--key   <api key>")}     or SMOLANALYTICS_KEY
  ${C.dim("--url   <instance>")}    or SMOLANALYTICS_HOST (default: the cloud org endpoint)
  ${C.dim("--project <name>")}      cloud only: which project to check
  ${C.dim("--window <hours>")}      only count events from the last N hours

Docs: https://smolanalytics.com/docs · 14-day trial at Pro limits, no card
`);
}

async function main() {
  const cmd = process.argv[2];
  if (!cmd || cmd === "help" || cmd === "--help" || cmd === "-h") return help();
  if (cmd === "connect") {
    const bare = process.argv.slice(3).find((a, i, all) => !a.startsWith("--") && !(i > 0 && all[i - 1].startsWith("--") && !all[i - 1].includes("=")));
    process.exitCode = connectCmd({
      url: flag("url") || flag("host") || "",
      key: flag("key") || process.env.SMOLANALYTICS_MCP_KEY || "",
      target: bare || "",
    });
    return;
  }
  if (cmd === "audit") {
    const bare = process.argv[3];
    process.exitCode = auditCmd({
      dir: bare && !bare.startsWith("--") ? bare : flag("dir") || ".",
      json: hasFlag("json"),
      all: hasFlag("all"),
    });
    return;
  }
  if (cmd === "test") {
    const suite = flag("suite");
    const comment = hasFlag("comment");
    // --suite and --comment are the CI shape: many tests, one comment, a status per test. Without
    // either of them this stays the sixty-second command it already was, with the same output.
    if (suite || comment) {
      process.exitCode = await suiteCmd({
        suite,
        url: flag("url"),
        test: flag("test"),
        plans: flag("plans") || flag("plan-dir") || (suite ? undefined : flag("plan")) || DEFAULT_PLANS_DIR,
        comment,
        headed: hasFlag("headed"),
        yes: hasFlag("yes"),
        maxSteps: Number(flag("max-steps")) || 40,
      });
      return;
    }
    process.exitCode = await testCmd({
      url: flag("url"),
      test: flag("test"),
      plan: flag("plan"),
      headed: hasFlag("headed"),
      yes: hasFlag("yes"),
      maxSteps: Number(flag("max-steps")) || 40,
    });
    return;
  }
  if (cmd === "desk") {
    process.exitCode = await deskCmd({
      url: flag("url") || flag("host") || process.env.SMOLANALYTICS_HOST || "",
      key: flag("key") || process.env.SMOLANALYTICS_KEY || "",
      project: flag("project") || "",
    });
    return;
  }
  if (cmd === "plan") {
    const sub = process.argv[3];
    if (sub !== "check") {
      console.error(`plan: only \`check\` is available in the npm CLI (use the binary for init/push/pull)\n`);
      process.exitCode = 1;
      return;
    }
    process.exitCode = await planCheckCmd({
      url: flag("url") || flag("host") || process.env.SMOLANALYTICS_HOST || "",
      key: flag("key") || process.env.SMOLANALYTICS_KEY || "",
      project: flag("project") || "",
      windowHours: Number(flag("window") || 0),
    });
    return;
  }
  if (cmd !== "init") {
    console.error(`unknown command ${C.bold(cmd)}\n`);
    help();
    process.exitCode = 1;
    return;
  }

  const host = (flag("host") || process.env.SMOLANALYTICS_HOST || "").replace(/\/$/, "");
  const key = flag("key") || process.env.SMOLANALYTICS_WRITE_KEY || "";

  if (hasFlag("print")) {
    console.log(snippetHtml(host || "https://YOUR-INSTANCE", key || "YOUR_WRITE_KEY").trim());
    return;
  }

  const found = detect(realFs);
  if (!found) {
    console.log(`
${C.yellow("Couldn't tell what this project is")}, so nothing was changed.

Paste this into your HTML ${C.bold("<head>")}:

${snippetHtml(host || "https://YOUR-INSTANCE", key || "YOUR_WRITE_KEY")}
`);
    return;
  }

  console.log(`\n  detected  ${C.bold(found.framework)}`);
  console.log(`  file      ${found.file}`);
  if (found.note) console.log(`  ${C.yellow("note")}      ${found.note}`);

  // Missing credentials are a stop, not a placeholder. Writing "YOUR_WRITE_KEY" into a real
  // layout produces an app that looks instrumented and silently sends nothing, which is a
  // worse failure than doing nothing at all.
  if (!host || !key) {
    console.log(`
${C.yellow("Need your instance and write key before editing anything.")}

  npx smolanalytics init --host https://your-instance --key sa_xxx

Self-hosting? Both are printed when the binary starts. On the hosted plane they're on
the project's setup page. The write key is public and ingest-only, it cannot read data.
`);
    process.exitCode = 1;
    return;
  }

  // Frameworks whose real install isn't a script tag: print the one that works, edit nothing.
  if (found.strategy === "manual") {
    console.log(`
  ${C.yellow("This one needs a framework-specific install")}, so nothing was changed.

${MANUAL_SNIPPETS[found.manual](host, key)}
`);
    return;
  }

  if (!realFs.exists(found.file)) {
    console.log(`\n  ${C.red("missing")}   ${found.file} does not exist, nothing was changed\n`);
    process.exitCode = 1;
    return;
  }

  const target = path.resolve(found.file);
  const src = fs.readFileSync(target, "utf8");
  const result = applyStrategy(found.strategy, src, host, key);

  if (result.status === "already") {
    console.log(`\n  ${C.green("already wired")} — ${found.file} already calls smolanalytics.init, left alone\n`);
    return;
  }
  if (result.status === "no-anchor") {
    console.log(`
  ${C.yellow("couldn't find a safe place to insert")} (${result.reason}), nothing was changed.

Paste this into your ${C.bold("<head>")}:

${snippetHtml(host, key)}
`);
    process.exitCode = 1;
    return;
  }

  if (!hasFlag("yes")) {
    const rl = readline.createInterface({ input: stdin, output: stdout });
    const answer = (await rl.question(`\n  edit ${C.bold(found.file)}? ${C.dim("[Y/n] ")}`)).trim().toLowerCase();
    rl.close();
    if (answer && answer !== "y" && answer !== "yes") {
      console.log("  nothing was changed\n");
      return;
    }
  }

  fs.writeFileSync(target, result.content, "utf8");
  console.log(`  ${C.green("edited")}    ${found.file}`);

  // .env.local only when one is already the convention here. Creating dotenv files in a repo
  // that doesn't use them is the kind of surprise that gets a tool uninstalled.
  const envFile = [".env.local", ".env"].find((f) => realFs.exists(f));
  if (envFile) {
    const cur = fs.readFileSync(path.resolve(envFile), "utf8");
    let next = upsertEnv(cur, "SMOLANALYTICS_HOST", host);
    next = upsertEnv(next, "SMOLANALYTICS_WRITE_KEY", key);
    fs.writeFileSync(path.resolve(envFile), next, "utf8");
    console.log(`  ${C.green("edited")}    ${envFile}`);
  }

  console.log(`
  ${C.bold("Next")}
  1. run your app and load a page
  2. ${host}  ${C.dim("— the pageview should already be there")}
  3. ${C.dim("connect your editor:")} smolanalytics connect

  Ask it things in plain English. Every answer is a computed report, and a CI test
  proves the dashboard, the API and your editor return the same number:
  ${C.dim("https://smolanalytics.com/proof")}
`);
}

main().catch((err) => {
  console.error(`\n${C.red("failed")} ${err?.message || err}\n`);
  process.exitCode = 1;
});
