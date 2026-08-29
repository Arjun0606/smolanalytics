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
import { parseLayoutMode } from "../lib/layout.mjs";
import { autoPreviewUrl } from "../lib/preview.mjs";
import { suggestCmd } from "../lib/suggest.mjs";

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
  ${C.dim("--wait-preview <sec>")}  Actions + no --url: wait for this PR's own preview deployment (default 240)
  ${C.dim("--test \"<text>\"")}       what should work, in plain English
  ${C.dim("--plan <file>")}         replay the recording; wake the agent only if it stopped fitting
  ${C.dim("--headed")}              watch it happen
  ${C.dim("--yes")}                 don't ask before a production-looking URL (CI is never asked)
  ${C.dim("--teardown <url>")}      POST this run's identity there afterwards, to delete what it made
  ${C.dim("--email-domain <dom>")}  the domain in {{email}} (default: example.com)
  ${C.dim("--retries <n>")}         re-run a failing test from a clean page; pass-on-retry is flaky, not passed (default 1; 0 disables)
  ${C.dim("--evidence-dir <dir>")}  where a failure's screenshot + page text land (default .smolanalytics/evidence)
  ${C.dim("--layout <mode>")}       layout sanity on the final page: report (default, notes only) | strict (findings fail a PASS) | off
  ${C.dim("--no-render-check")}     turn off the render guard: a PASS over a blank, unstyled or crashed page fails by default
  ${C.dim("--login \"<sentence>\"")}  sign in once in plain English; every run after it reuses the saved session
  ${C.dim("--auth-file <path>")}    instead of --login: a Playwright storage state you already generate
  ${C.dim("--auth-dir <dir>")}      where the saved session is kept (default: .smolanalytics/auth)
  ${C.dim("SMOLANALYTICS_LOGIN_EMAIL / SMOLANALYTICS_LOGIN_PASSWORD fill {{email}} and {{password}}.")}

  ${C.dim("--suite <dir>")}         a folder of .md files, one sentence per test
  ${C.dim("--comment")}             post the verdicts on the pull request (GitHub Actions)
  ${C.dim("--plans <dir>")}         where recordings are kept (default: ${DEFAULT_PLANS_DIR})
  ${C.dim("No account. No GitHub app. Nothing written to your repo.")}

  ${C.bold("npx smolanalytics suggest")}     the tests worth writing, read off your running app
  ${C.dim("--url  <url>")}          a browser walks a few pages and proposes the flows it can SEE
  ${C.dim("--out  <dir>")}          where the .md files land (default tests/) — existing files are never overwritten
  ${C.dim("--max  <n>")}            at most this many proposals (default 6; a small app honestly yields fewer)

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
    // NOTHING THAT ESCAPES HERE MAY EXIT 1. The workflow template publishes the contract: 1 means a
    // test failed, which means the app did not do what the sentence describes. A crash of ours —
    // out of disk, a Playwright internal, a bug in this file — reaching the last-resort catch below
    // exits 1 and puts a bug report about our own crash on somebody else's pull request.
    // Parsed once for both shapes. A typo'd count must not silently become 0 — that would turn
    // retries OFF for someone who asked for more of them.
    const retriesRaw = flag("retries");
    if (retriesRaw !== undefined && !/^\d+$/.test(retriesRaw)) {
      console.error(`${C.red("--retries needs a whole number")}, got ${JSON.stringify(retriesRaw)}. 1 retries a failing test once; 0 disables retries.`);
      process.exitCode = 2;
      return;
    }
    const retries = retriesRaw === undefined ? 1 : Number(retriesRaw);
    // Same shape as --retries: `--layout=stric` silently meaning the default would quietly un-gate
    // the one customer who explicitly opted into gating. A bare --layout gets the same refusal.
    const layoutRaw = flag("layout") ?? (hasFlag("layout") ? "" : undefined);
    const { mode: layout, problem: layoutProblem } = parseLayoutMode(layoutRaw);
    if (layoutProblem) {
      console.error(C.red(layoutProblem));
      process.exitCode = 2;
      return;
    }
    // NO --url INSIDE ACTIONS ON A PULL REQUEST: the preview host already told GitHub the URL, so
    // ask the deployments API instead of asking the person (lib/preview.mjs says how and why).
    // Anywhere else — a laptop, a push build — autoPreviewUrl skips and the missing --url keeps
    // producing exactly the error it does today. A failed lookup is exit 2, the runner's code:
    // no test ran, so nothing was learned about the app, and 1 would blame it anyway.
    let url = flag("url");
    if (!url) {
      const preview = await autoPreviewUrl({ waitRaw: flag("wait-preview") });
      if (preview.problem) {
        console.error(`\n${C.red("no preview URL")} ${preview.problem}\n`);
        process.exitCode = 2;
        return;
      }
      if (preview.url) url = preview.url;
    }
    try {
      // --suite and --comment are the CI shape: many tests, one comment, a status per test. Without
      // either of them this stays the sixty-second command it already was, with the same output.
      if (suite || comment) {
        process.exitCode = await suiteCmd({
          suite,
          url,
          test: flag("test"),
          plans: flag("plans") || flag("plan-dir") || (suite ? undefined : flag("plan")) || DEFAULT_PLANS_DIR,
          comment,
          headed: hasFlag("headed"),
          yes: hasFlag("yes"),
          teardown: flag("teardown") || "",
          emailDomain: flag("email-domain") || "",
          maxSteps: Number(flag("max-steps")) || 40,
          retries,
          evidenceDir: flag("evidence-dir") || "",
          layout,
          // THE FALSE-GREEN GUARD (lib/render.mjs) is on unless it is explicitly switched off: a
          // guard nobody enabled is a guard nobody has, and a blank page passing green is the one
          // failure mode that loses a customer who already trusts us.
          renderCheck: !hasFlag("no-render-check"),
          // Authenticated flows (lib/auth.mjs). One login sentence for the whole suite: the first
          // test signs in, the rest reuse the saved session.
          login: flag("login") || "",
          authFile: flag("auth-file") || "",
          authDir: flag("auth-dir") || undefined,
        });
        return;
      }
      process.exitCode = await testCmd({
        url,
        test: flag("test"),
        plan: flag("plan"),
        headed: hasFlag("headed"),
        yes: hasFlag("yes"),
        teardown: flag("teardown") || "",
        emailDomain: flag("email-domain") || "",
        maxSteps: Number(flag("max-steps")) || 40,
        retries,
        evidenceDir: flag("evidence-dir") || "",
        layout,
        renderCheck: !hasFlag("no-render-check"),
        login: flag("login") || "",
        authFile: flag("auth-file") || "",
        authDir: flag("auth-dir") || undefined,
      });
    } catch (err) {
      console.error(`\n${C.red("the run could not complete")} ${err?.message || err}`);
      console.error(`  This is the test runner, not your application. Nothing was learned about this change.\n`);
      process.exitCode = 2;
    }
    return;
  }
  if (cmd === "suggest") {
    // A bare `--max` is refused rather than defaulted, the same shape as --retries and --layout
    // above: flag() cannot tell `--max` with no value from no --max at all, and silently handing
    // back the default 6 gives the person who typed a cap a different one than they asked for.
    // suggestCmd owns the refusal so the library and the CLI cannot drift apart on what is valid.
    process.exitCode = await suggestCmd({
      url: flag("url"),
      out: flag("out") || "tests",
      max: flag("max") ?? (hasFlag("max") ? "" : undefined),
      yes: hasFlag("yes"),
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
  // `test` is the one command whose exit code is a published contract (see templates/github-action
  // .yml): 1 says the application is broken. Our own crash is never that.
  process.exitCode = process.argv[2] === "test" ? 2 : 1;
});
