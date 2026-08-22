// `npx smolanalytics audit` — one command, no signup, no account, no data required.
//
// The product's whole funnel used to be: sign up, create a project, wait for an instance, paste a
// snippet, then wait days for enough events before anything could be said. First value was a week
// away and cost a signup. Autonoma's first step is `npx @autonoma-ai/planner@latest` and you have
// something in minutes; that difference is not positioning, it is the product.
//
// This reads the repo you are standing in and tells you which user-facing actions have no tracking
// near them. It needs no server, no key, no events and no account, because it reads CODE. Your code
// never leaves the machine: there is no network call on this path at all.
//
// The patterns are a faithful port of internal/instrument/coverage.go, and they are deliberately
// conservative for a reason recorded there: the first version matched any MENTION of a word and
// produced 497 "untracked actions" on one real codebase, where the findings were `import
// { signOut }`, a function declaration, and JSX text. A report like that is dismissed once and
// never opened again, which is worse than not shipping it. Every pattern here matches an
// invocation.

import fs from "node:fs";
import path from "node:path";

const SKIP_DIRS = new Set([
  "node_modules", ".git", ".next", "dist", "build", "out", "coverage", "vendor",
  ".turbo", ".vercel", ".cache", "__pycache__", ".venv", "target",
]);
const EXTS = new Set([".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".svelte", ".vue", ".py", ".rb", ".go", ".php"]);

// Ranked by how much a missing event actually costs you. Payment first because an untracked
// checkout is revenue you cannot attribute; the generic shapes sink to the bottom because a page
// of "form submit" is the noise that gets a tool muted.
const PATTERNS = [
  { kind: "payment", suggest: "checkout", weight: 0, re: /(stripe\.\w+\.\w*create\s*\(|checkout\.sessions?\.create\s*\(|\bcreateCheckoutSession\s*\(|\bcreateSubscription\s*\(|\.subscriptions?\.create\s*\(|\bpaymentIntents?\.create\s*\(|\.charges?\.create\s*\()/i },
  { kind: "signup", suggest: "signup", weight: 1, re: /(\b(auth\.)?sign[_-]?up(\.\w+)?\(|\bsignUpWith\w*\s*\(|\bcreateUserWith\w*\s*\(|\bcreateUser\s*\(|\bregisterUser\s*\(|\bregisterAccount\s*\(|\.users\.create\s*\()/i },
  { kind: "login", suggest: "login", weight: 2, re: /(\b(auth\.)?sign[_-]?in(\.\w+)?\(|\bsignInWith\w*\(|\blog[_-]?in\(|\bauthenticate\()/i },
  { kind: "invite", suggest: "invite_sent", weight: 3, re: /(send[_-]?invite\w*\s*\(|\binviteUser\s*\(|\binviteMember\s*\(|createInvit\w*\s*\()/i },
  { kind: "delete account", suggest: "account_deleted", weight: 4, re: /(\bdeleteAccount\s*\(|\bdeleteUser\s*\(|\bcloseAccount\s*\()/i },
  { kind: "share", suggest: "shared", weight: 5, re: /(\bcreateShareLink\s*\(|\bhandleShare\s*\(|\bshareLink\s*\()/i },
  { kind: "upload", suggest: "file_uploaded", weight: 6, re: /(\buploadFile\s*\(|\bhandleUpload\s*\(|\.upload\s*\()/i },
  { kind: "logout", suggest: "logout", weight: 7, re: /(\b(auth\.)?sign[_-]?out\(|\blog[_-]?out\()/i },
  { kind: "form submit", suggest: "form_submitted", weight: 8, re: /(onsubmit\s*=|\bhandleSubmit\s*\(|<form\b)/i },
  { kind: "api mutation", suggest: "api_called", weight: 9, re: /^\s*export\s+(async\s+)?function\s+(POST|PUT|PATCH|DELETE)\s*\(/ },
  { kind: "api mutation", suggest: "api_called", weight: 9, re: /\b(router|app)\.(post|put|patch|delete)\s*\(/i },
];

// Each of these rejected a real false positive from running the first version on a live codebase.
const NOT_AN_ACTION = [
  /^\s*(import|export)\s/,
  /^\s*(\/\/|\/\*|\*|#)/,
  /^\s*(type|interface|declare)\s/,
  /\bfrom\s+["'`]/,
  /^\s*(function|const|let|var)\s+\w+\s*[=:(]\s*(async\s*)?\(?\s*\)?\s*(=>)?\s*\{?\s*$/,
  // Prose that happens to contain the word. A markdown bullet or a sentence telling the reader to
  // do something is not the app doing it — this matched "tell them to sign up at ${base}/signup
  // (14-day trial…)" inside a route that serves documentation.
  /^\s*[-*]\s+[A-Z]/,
  /\b(tell|ask|click|visit|go to|head to|you can|they can|should)\b.{0,40}\b(sign|log)\s?(up|in|out)\b/i,
];

// A route handler is the one case where DECLARING is DOING, so the declaration guards do not apply.
function skipAsNonAction(line, kind) {
  if (kind === "api mutation") {
    return (NOT_AN_ACTION[0].test(line) && !line.includes("function")) || NOT_AN_ACTION[1].test(line);
  }
  return NOT_AN_ACTION.some((re) => re.test(line));
}

const TRACK_NEARBY = /smolanalytics\??\.track\(|\/v1\/events|posthog\.capture\(|analytics\.track\(|mixpanel\.track\(|gtag\(|amplitude\.\w*track/i;
const NEARBY_LINES = 12;

export function walk(root, io = fs, limit = 4000) {
  const out = [];
  const stack = [root];
  while (stack.length && out.length < limit) {
    const dir = stack.pop();
    let entries;
    try {
      entries = io.readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const e of entries) {
      if (e.name.startsWith(".") && e.name !== ".") {
        if (SKIP_DIRS.has(e.name)) continue;
      }
      const full = path.join(dir, e.name);
      if (e.isDirectory()) {
        if (!SKIP_DIRS.has(e.name)) stack.push(full);
      } else if (EXTS.has(path.extname(e.name))) {
        out.push(full);
      }
    }
  }
  return out;
}

export function scanFile(rel, text) {
  const lines = text.split("\n");
  const found = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.length > 400) continue; // minified or generated
    for (const p of PATTERNS) {
      if (!p.re.test(line)) continue;
      if (skipAsNonAction(line, p.kind)) continue;
      // A line that IS the tracking call is not an untracked action.
      if (TRACK_NEARBY.test(line)) continue;
      const from = Math.max(0, i - NEARBY_LINES);
      const to = Math.min(lines.length, i + NEARBY_LINES + 1);
      const covered = lines.slice(from, to).some((l) => TRACK_NEARBY.test(l));
      found.push({
        kind: p.kind, suggest: p.suggest, weight: p.weight,
        file: rel, line: i + 1, snippet: line.trim().slice(0, 110), covered,
      });
      break; // one action per line
    }
  }
  return found;
}

export function auditRepo(root, io = fs) {
  const files = walk(root, io);
  let actions = [];
  for (const f of files) {
    let text;
    try {
      text = io.readFileSync(f, "utf8");
    } catch {
      continue;
    }
    actions = actions.concat(scanFile(path.relative(root, f), text));
  }
  actions.sort((a, b) => a.weight - b.weight || a.file.localeCompare(b.file) || a.line - b.line);
  const covered = actions.filter((a) => a.covered).length;
  return {
    actions,
    covered,
    uncovered: actions.length - covered,
    pct: actions.length ? Math.round((covered / actions.length) * 100) : 0,
    files: files.length,
  };
}

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
};

// NAMED shapes are worth a line each; the generic ones are counted, not listed. Thirty rows of
// "form submit" is the difference between a report someone acts on and one they close.
const GENERIC = new Set(["form submit", "api mutation"]);

export function render(r, log = console.log, all = false) {
  if (r.actions.length === 0) {
    log("");
    log("No recognisable user-facing actions found here.");
    log(C.dim(`Scanned ${r.files} files. This looks for invoked actions: payments, auth, invites,`));
    log(C.dim("uploads, shares, form submits and mutating API routes. If this is an app repo, try"));
    log(C.dim("running it from the directory that holds your app code."));
    return 0;
  }

  const named = r.actions.filter((a) => !GENERIC.has(a.kind) && !a.covered);
  const generic = r.actions.filter((a) => GENERIC.has(a.kind) && !a.covered);

  log("");
  // LEAD WITH THE SIGNAL, NOT THE TOTAL.
  //
  // This opened with "70 of 70 user-facing actions have no tracking near them", and the six that
  // actually matter — signup, three logins, an invite, a logout — were then listed under 64 rows
  // of "form submit" and "api mutation". A skeptical reader's verdict on that output was not
  // "wow", it was "this thing pads". The named findings ARE the product; the bulk is a footnote.
  if (named.length) {
    log(C.b(`${named.length} thing${named.length === 1 ? "" : "s"} your product does that nothing is measuring.`));
    log(C.dim("An action nobody instrumented looks identical to one nobody performed."));
    log("");
  } else {
    log(C.b("Every named user action here already has tracking near it."));
    log(C.dim(`Checked signups, logins, payments, invites, uploads and shares across ${r.files} files.`));
    log("");
  }

  if (named.length) {
    for (const a of named.slice(0, 12)) {
      log(`  ${C.y("untracked")}  ${C.b(a.kind.padEnd(15))} ${a.file}:${a.line}`);
      log(`             ${C.dim(a.snippet)}`);
      log(`             ${C.dim("suggested event: " + a.suggest)}`);
    }
    if (named.length > 12) log(C.dim(`  … and ${named.length - 12} more named actions`));
    log("");
  }

  if (generic.length) {
    // One line, deliberately. These are shapes (a <form>, a POST handler), not named actions, and
    // most of them are not worth an event. Listing them by kind made the report look padded and
    // buried the findings above.
    const byKind = {};
    for (const a of generic) byKind[a.kind] = (byKind[a.kind] || 0) + 1;
    if (all) {
      log(C.b("Also untracked:"));
      for (const a of generic) log(`  ${C.dim(a.kind.padEnd(14))} ${a.file}:${a.line}`);
    } else {
      const parts = Object.entries(byKind).map(([k, n]) => `${n} ${k}${n === 1 ? "" : "s"}`);
      log(C.dim(`Also seen, mostly not worth an event: ${parts.join(", ")}. Run with --all to list them.`));
    }
    log("");
  }

  if (r.covered > 0) log(C.g(`${r.covered} action${r.covered === 1 ? " is" : "s are"} already tracked.`));

  log(C.b("Next:"));
  log("  Your coding agent can write these track() calls for you. Connect it once:");
  log(`  ${C.b("npx smolanalytics connect --key <your key>")}   ${C.dim("then ask it to instrument your app")}`);
  log(C.dim("  Start free at https://smolanalytics.com — 14 days at Pro limits, no card."));
  log("");
  return 0;
}

export function auditCmd({ dir = ".", json = false, all = false, log = console.log, io = fs }) {
  const root = path.resolve(dir);
  if (!io.existsSync(root)) {
    log(`no such directory: ${root}`);
    return 1;
  }
  const r = auditRepo(root, io);
  if (json) {
    log(JSON.stringify(r, null, 2));
    return 0;
  }
  return render(r, log, all);
}
