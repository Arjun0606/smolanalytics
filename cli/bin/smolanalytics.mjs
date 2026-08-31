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
import { runnerProblem, testCmd, testUsage } from "../lib/test.mjs";
import { suiteCmd, DEFAULT_PLANS_DIR, announceCannotStart } from "../lib/suite.mjs";
import { parseLayoutMode } from "../lib/layout.mjs";
import { parseEngine } from "../lib/engines.mjs";
import { parseWorkers } from "../lib/pool.mjs";
import { parseMaxCalls } from "../lib/cost.mjs";
import { parseSince } from "../lib/select.mjs";
import { autoPreviewUrl } from "../lib/preview.mjs";
import { suggestCmd, suggestUsage } from "../lib/suggest.mjs";
import { normalizeUrl } from "../lib/safety.mjs";

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

// A FLAG NOBODY DEFINED IS A FLAG THAT DID NOTHING, AND SAID NOTHING.
//
// Every flag VALUE below is refused rather than defaulted — --retries, --layout, --browser,
// --workers, --since, --max-calls all do it, each with the same reason written out beside it:
// quietly handing back the default gives the person who typed the flag something other than what
// they asked for. Flag NAMES had no such guard at all. Measured, by typing them:
//
//   --tset "the pricing page works"  printed `test`'s usage block with no mention of --tset, so the
//                                    reader has to diff the help against their own command to find
//                                    the one character that is wrong.
//   --urls https://staging.myapp.com the same, and --url then looked missing.
//   --reties 3                       ran with one retry, silently.
//   --headles                        ran headless, silently — which is the one thing the person
//                                    typing it was trying to stop.
//
// Scanning every `--` token is exactly the shape flag() already assumes: it refuses to read a
// value that begins with `--`, so nothing that used to be consumed as a value is caught here by
// surprise. Positional arguments (`audit ./app`, `connect cursor`, `plan check`) are not scanned.
const FLAGS = {
  test: ["url", "wait-preview", "test", "plan", "plans", "plan-dir", "browser", "headed", "yes",
    "teardown", "seed", "email-domain", "retries", "workers", "evidence-dir", "layout",
    "no-render-check", "login", "auth-file", "auth-dir", "suite", "since", "comment", "share",
    "max-steps", "max-calls", "help"],
  suggest: ["url", "out", "max", "yes", "help"],
  audit: ["dir", "json", "all", "help"],
  desk: ["url", "host", "key", "project", "help"],
  init: ["key", "host", "yes", "print", "help"],
  connect: ["url", "host", "key", "help"],
  plan: ["url", "host", "key", "project", "window", "help"],
};

// The commands this binary dispatches. `plan` covers `plan check`; `help` is handled above.
// EVERY COMMAND main() DISPATCHES, and `mcp` was missing from it. This list only feeds the
// did-you-mean guess, so the omission was silent in the way that matters least and annoys most:
// `mcp` is documented in help(), implemented at `cmd === "mcp"`, and is the command an editor
// integration tells people to type — and `npx smolanalytics mpc` answered "unknown command mpc"
// with no suggestion, while every other one-transposition typo gets one. Two lists that must
// agree, which is the same shape as the flag guard's own warning directly above.
const COMMANDS = ["test", "suggest", "audit", "desk", "init", "connect", "plan", "mcp"];

/** Edit distance, capped at 3 — far enough to catch a typo, near enough not to invent a guess. */
function distance(a, b) {
  const prev = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let diag = prev[0];
    prev[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const next = Math.min(prev[j] + 1, prev[j - 1] + 1, diag + (a[i - 1] === b[j - 1] ? 0 : 1));
      diag = prev[j];
      prev[j] = next;
    }
  }
  return prev[b.length];
}

/** The first `--` token this command does not read, as a sentence naming the fix. "" when all are known. */
function unknownFlag(cmd) {
  const known = FLAGS[cmd];
  if (!known) return "";
  for (const raw of process.argv.slice(3)) {
    if (raw === "--" || !raw.startsWith("--")) continue;
    const name = raw.slice(2).split("=")[0];
    if (!name || known.includes(name)) continue;
    // A suggestion is only offered when it is nearly certain. "--foo" is nobody's typo of anything
    // here, and a confidently wrong "did you mean" is worse than no guess at all.
    let near = "";
    let best = 3;
    for (const k of known) {
      const d = distance(name, k);
      if (d < best && d < Math.max(name.length, k.length)) {
        best = d;
        near = k;
      }
    }
    return near
      ? `unknown option --${name}. Did you mean ${C.bold(`--${near}`)}?`
      : `unknown option --${name} for \`smolanalytics ${cmd}\`. Run \`npx smolanalytics --help\` for the flags it takes.`;
  }
  return "";
}

function help() {
  console.log(`
${C.bold("smolanalytics")} — end-to-end tests without test code

  ${C.dim("ANTHROPIC_API_KEY is the one thing everything here needs: the agent is Claude, and the")}
  ${C.dim("calls are billed to you. Get one at console.anthropic.com/settings/keys.")}
  ${C.dim("Replaying a recording with --plan is the exception — that runs with no key and no model.")}

  ${C.bold("npx smolanalytics test")}        one sentence, a real browser, a verdict
  ${C.dim("--url  <url>")}          staging, a deploy preview, anything reachable
  ${C.dim("--wait-preview <sec>")}  Actions + no --url: wait for this PR's own preview deployment (default 240)
  ${C.dim("--test \"<text>\"")}       what should work, in plain English
  ${C.dim("--plan <file>")}         replay the recording; wake the agent only if it stopped fitting
  ${C.dim("--browser <name>")}      chromium (default), firefox or webkit — the same test in a different engine
  ${C.dim("--headed")}              watch it happen
  ${C.dim("--yes")}                 don't ask before a production-looking URL (CI is never asked)
  ${C.dim("--teardown <url>")}      POST this run's identity there afterwards, to delete what it made
  ${C.dim("--seed <url>")}          POST it there BEFORE the test; the flat JSON it returns becomes placeholders the sentence can use
  ${C.dim("SMOLANALYTICS_SEED_SECRET / SMOLANALYTICS_TEARDOWN_SECRET arrive as the Authorization header.")}
  ${C.dim("--email-domain <dom>")}  the domain in {{email}} (default: example.com)
  ${C.dim("--retries <n>")}         re-run a failing test from a clean page; pass-on-retry is flaky, not passed (default 1; 0 disables)
  ${C.dim("--workers <n>")}         with --suite: run this many tests at once (default: measured from cores, memory and whether a key is set; 1 is one at a time)
  ${C.dim("--evidence-dir <dir>")}  where a failure's screenshot + page text land (default .smolanalytics/evidence)
  ${C.dim("--layout <mode>")}       layout sanity on the final page: report (default, notes only) | strict (findings fail a PASS) | off
  ${C.dim("--no-render-check")}     turn off the render guard: a PASS over a blank, unstyled or crashed page fails by default
  ${C.dim("--login \"<sentence>\"")}  sign in once in plain English; every run after it reuses the saved session
  ${C.dim("--auth-file <path>")}    instead of --login: a Playwright storage state you already generate
  ${C.dim("--auth-dir <dir>")}      where the saved session is kept (default: .smolanalytics/auth)
  ${C.dim("SMOLANALYTICS_LOGIN_EMAIL / SMOLANALYTICS_LOGIN_PASSWORD fill {{email}} and {{password}}.")}

  ${C.dim("--suite <dir>")}         a folder of .md files, one sentence per test
  ${C.dim("--since <ref>")}         with --suite: run only the tests this change could have broken, and say what was skipped. Anything we cannot rule out still runs.
  ${C.dim("--comment")}             post the verdicts on the pull request (GitHub Actions)
  ${C.dim("--share")}               publish this run to a link anyone can open, and print it. Off unless you ask; one link per run, never per test.
  ${C.dim("--plans <dir>")}         where recordings are kept (default: ${DEFAULT_PLANS_DIR})
  ${C.dim("No account. No GitHub app. Nothing written to your repo.")}


  ${C.bold("npx smolanalytics suggest")}     the tests worth writing, read off your running app
  ${C.dim("--url  <url>")}          a browser walks a few pages and proposes the flows it can SEE
  ${C.dim("--out  <dir>")}          where the .md files land (default tests/) — existing files are never overwritten
  ${C.dim("--max  <n>")}            at most this many proposals (default 6; a small app honestly yields fewer)

  ${C.bold("npx smolanalytics audit")}       what your app does that nothing is measuring
  ${C.dim("[dir]")}                 repo to scan (default: here). No account, no network.

  ${C.bold("npx smolanalytics mcp")}         the testing half inside your editor (stdio MCP)
  ${C.dim("run_tests")}             run the suite and read every verdict
  ${C.dim("can_i_ship")}            the verdict plus what was NOT checked
  ${C.dim("list_tests")}            what this project already covers
  ${C.dim("Local only: it never comments, shares, or records to a project.")}

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

/**
 * ASKING WHAT A COMMAND DOES MUST NEVER BE ANSWERED BY DOING IT.
 *
 * `--help` was listed in FLAGS for every command, so it passed the typo check as a known flag and
 * was then read by nobody. Help arrived only as the side effect of failing a required-argument
 * check, and where a command had no required argument it did not arrive at all. MEASURED, by
 * typing each one:
 *
 *   npx smolanalytics audit --help    walked the repo and printed a full audit report, 11 findings
 *   npx smolanalytics init --help     detected Next.js, named the file it would edit, then asked
 *                                     for a write key
 *   npx smolanalytics connect --help  "connect needs your MCP token"
 *   npx smolanalytics desk --help     "desk needs a read key"
 *   npx smolanalytics plan check --help  "plan check needs a key"
 *   npx smolanalytics test --help     the right text, exit 1 — the code that means their app broke
 *   npx smolanalytics suggest --help  the right text, exit 2 — the code that means we broke
 *
 * Exit 0 on all of them, because a question that was answered is not a failure, and because these
 * two exit codes are a published contract that a `--help` has no business speaking in.
 *
 * `test` and `suggest` print their own blocks; the rest fall to the top-level help, which already
 * documents every command with its flags. Writing five more usage blocks would be five more places
 * for the flag list to drift out of date, and the reader is one screen from the answer either way.
 */
function helpFor(cmd) {
  if (cmd === "test") return console.log(testUsage());
  if (cmd === "suggest") return console.log(suggestUsage());
  return help();
}

async function main() {
  const cmd = process.argv[2];
  if (!cmd || cmd === "help" || cmd === "--help" || cmd === "-h") return help();
  // Before the typo check, and long before any command opens a file or a browser: -h is not in
  // FLAGS and would otherwise be reported as an unknown option on every subcommand.
  if (process.argv.slice(3).some((a) => a === "--help" || a === "-h")) return helpFor(cmd);
  // Before anything is parsed, opened or printed: a typo must not be discovered by its silence.
  const typo = unknownFlag(cmd);
  if (typo) {
    console.error(`\n${C.red(typo)}\n`);
    // 2 on `test`, whose exit code is a published contract: 1 means the customer's app is broken,
    // and a typo in our own flag is never that. main()'s catch-all applies the same rule.
    process.exitCode = cmd === "test" ? 2 : 1;
    return;
  }
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
    // CROSS-BROWSER (lib/engines.mjs). The same refusal shape once more: `--browser webkti` quietly
    // meaning chromium would report a green suite to the one person who explicitly asked to be told
    // about WebKit, which is the only reason they typed the flag.
    const browserRaw = flag("browser") ?? (hasFlag("browser") ? "" : undefined);
    const { engine, problem: engineProblem } = parseEngine(browserRaw);
    if (engineProblem) {
      console.error(C.red(engineProblem));
      process.exitCode = 2;
      return;
    }
    // PARALLEL SUITE EXECUTION (lib/pool.mjs). Same refusal shape again: a bare `--workers` or a
    // `--workers eight` is an error, never a silent default, because the person who typed it was
    // asking for a specific amount of machine.
    const workersRaw = flag("workers") ?? (hasFlag("workers") ? "" : undefined);
    const { workers, problem: workersProblem } = parseWorkers(workersRaw, { hasKey: Boolean(process.env.ANTHROPIC_API_KEY) });
    if (workersProblem) {
      console.error(C.red(workersProblem));
      process.exitCode = 2;
      return;
    }
    // DIFF-AWARE SELECTION (lib/select.mjs). Refused the same way, and for the sharpest version of
    // the same reason: a bare `--since` silently meaning "no selection" bills the person for the
    // whole folder they were explicitly trying not to run.
    const { since, problem: sinceProblem } = parseSince(flag("since") ?? (hasFlag("since") ? "" : undefined));
    if (sinceProblem) {
      console.error(C.red(sinceProblem));
      process.exitCode = 2;
      return;
    }
    // Refused rather than ignored. --since chooses among the tests in a FOLDER; on the single-test
    // path there is nothing to choose, and accepting it there would silently do nothing at all.
    if (since && !suite) {
      console.error(`${C.red("--since needs --suite.")} It chooses which of a folder's tests to run; a single --test has nothing to choose between.`);
      process.exitCode = 2;
      return;
    }
    // The spend ceiling, refused the same way for the same reason.
    const { value: maxCalls, problem: callsProblem } = parseMaxCalls(flag("max-calls"));
    if (callsProblem) {
      console.error(C.red(callsProblem));
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
        // AND ON THE PULL REQUEST, not only in the job log. A preview that never became ready is
        // the one outage where nothing this tool does is visible where it is meant to be visible:
        // the run stops here, before suiteCmd and before any comment, and the shipped workflow's
        // continue-on-error paints the check green. Green, silent, and untested is the answer a
        // reviewer is least equipped to catch (lib/suite.mjs::cannotStartBody).
        await announceCannotStart({ problem: preview.problem, suite: suite || "test", comment, log: console.error });
        process.exitCode = 2;
        return;
      }
      if (preview.url) url = preview.url;
    }
    // A MISSING SCHEME IS REPAIRED HERE OR IT IS A FALSE GREEN LATER (lib/safety.mjs says how one
    // gets produced). Ahead of the try, so a URL nothing can open never reaches a browser, a
    // recording or the production question, and exit 2 — our side, nothing was learned.
    const fixed = normalizeUrl(url);
    if (fixed.problem) {
      console.error(`\n${C.red(fixed.problem)}\n`);
      process.exitCode = 2;
      return;
    }
    url = fixed.url;
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
          // "" unless it was typed. Nothing about the run changes without it.
          since,
          headed: hasFlag("headed"),
          yes: hasFlag("yes"),
          teardown: flag("teardown") || "",
          seed: flag("seed") || "",
          emailDomain: flag("email-domain") || "",
          maxSteps: Number(flag("max-steps")) || 40,
          retries,
          workers,
          evidenceDir: flag("evidence-dir") || "",
          layout,
          // THE FALSE-GREEN GUARD (lib/render.mjs) is on unless it is explicitly switched off: a
          // guard nobody enabled is a guard nobody has, and a blank page passing green is the one
          // failure mode that loses a customer who already trusts us.
          renderCheck: !hasFlag("no-render-check"),
          maxCalls,
          engine,
          // Authenticated flows (lib/auth.mjs). One login sentence for the whole suite: the first
          // test signs in, the rest reuse the saved session.
          login: flag("login") || "",
          authFile: flag("auth-file") || "",
          authDir: flag("auth-dir") || undefined,
          // --share (lib/share.mjs). Opt-in, and nothing about the run changes without it: the
          // verdict, the exit code, the transcript and the JSON posted to a project are all what
          // they were. One link for the whole suite, printed last.
          share: hasFlag("share"),
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
        seed: flag("seed") || "",
        emailDomain: flag("email-domain") || "",
        maxSteps: Number(flag("max-steps")) || 40,
        retries,
        evidenceDir: flag("evidence-dir") || "",
        layout,
        renderCheck: !hasFlag("no-render-check"),
          maxCalls,
        engine,
        login: flag("login") || "",
        authFile: flag("auth-file") || "",
        authDir: flag("auth-dir") || undefined,
        share: hasFlag("share"),
      });
    } catch (err) {
      // Named first, fix second, no Call log — lib/test.mjs's runnerProblem says why.
      const why = runnerProblem(err);
      console.error(`\n${C.red(why.known ? why.what : `the run could not complete: ${why.what}`)}`);
      if (why.fix) console.error(C.dim(`  ${why.fix}`));
      console.error(C.dim(`  This is the test runner, not your application. Nothing was learned about this change.\n`));
      process.exitCode = 2;
    }
    return;
  }
  // `watch` IS NOT SHIPPED YET, DELIBERATELY.
  //
  // lib/watch.mjs exists and its command works by hand, but its own test file wedges the suite: a
  // promise inside the session's stop() never settles, so node --test hangs past any timeout and
  // the whole file resists being killed. That is not a test problem — it means Ctrl-C does not
  // reliably release a real session either, which on a feature that runs unattended on somebody's
  // laptop and spends their model budget is the one bug that must not ship. The command is wired
  // back the moment that lifecycle is fixed and its adversarial pass has actually run.

  if (cmd === "suggest") {
    // A bare `--max` is refused rather than defaulted, the same shape as --retries and --layout
    // above: flag() cannot tell `--max` with no value from no --max at all, and silently handing
    // back the default 6 gives the person who typed a cap a different one than they asked for.
    // suggestCmd owns the refusal so the library and the CLI cannot drift apart on what is valid.
    // The same repair as `test`, for the same reason: suggest drives a real browser too, and
    // `--url myapp.com` used to reach Playwright as a URL it could not parse.
    const surveyed = normalizeUrl(flag("url"));
    if (surveyed.problem) {
      console.error(`\n${C.red(surveyed.problem)}\n`);
      process.exitCode = 2;
      return;
    }
    process.exitCode = await suggestCmd({
      url: surveyed.url,
      out: flag("out") || "tests",
      max: flag("max") ?? (hasFlag("max") ? "" : undefined),
      yes: hasFlag("yes"),
    });
    return;
  }
  if (cmd === "mcp") {
    // stdio, and the process stays alive until the editor closes the pipe. Nothing is printed here
    // — the first byte on stdout must be JSON-RPC or the client reports a broken server.
    const { serve } = await import("../lib/mcp.mjs");
    process.exitCode = await serve();
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
    // A MISTYPED COMMAND GETS THE SAME SENTENCE A MISTYPED FLAG GETS.
    //
    // MEASURED: `npx smolanalytics tset --url … --test "…"` printed `unknown command tset` and then
    // the full sixty-four-line help, leaving the reader to diff it against what they typed to find
    // the two transposed characters — while `npx smolanalytics test --tset "…"`, one keystroke
    // away, answered `Did you mean --test?` in a single line. Same typo, same distance(), two
    // different first minutes. The help dump is dropped where a guess is confident: it is sixty-
    // four lines that bury the one line naming the fix.
    let near = "";
    let best = 3;
    for (const k of COMMANDS) {
      const d = distance(cmd, k);
      if (d < best && d < Math.max(cmd.length, k.length)) {
        best = d;
        near = k;
      }
    }
    if (near) {
      console.error(`\n${C.red(`unknown command ${C.bold(cmd)}. Did you mean ${C.bold(near)}?`)}\n`);
    } else {
      console.error(`unknown command ${C.bold(cmd)}\n`);
      help();
    }
    // 2, for the same reason a mistyped FLAG is 2 and with a wider blast radius: an unknown
    // command cannot be `test`, so this path always exited 1 — "a test failed, the application is
    // broken" — for a run in which nothing was opened and no page was ever loaded. The shipped
    // workflow invokes us by name, so one wrong character in a job somebody copied puts a bug
    // report about their product on their own pull request. MEASURED against the real binary:
    // `smolanalytics frobnicate` exited 1.
    process.exitCode = 2;
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
  // .yml): 1 says the application is broken. Our own crash is never that. `watch` runs the same
  // runner and prints the same five statuses, so it keeps the same promise about 1.
  process.exitCode = process.argv[2] === "test" ? 2 : 1;
});
