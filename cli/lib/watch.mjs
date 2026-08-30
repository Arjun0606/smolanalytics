// `npx smolanalytics watch` — the loop at the keyboard.
//
// WHY THIS EXISTS, AND WHY IT IS NOT A CHECKLIST FEATURE.
//
// Until this file, the product only ever spoke on a pull request. That is where it PROVES its
// worth, but it is not where anybody falls in love with it. A developer decides they depend on a
// tool at the keyboard, before they have ever paid: they save a file, the affected test re-runs on
// its own, and they watch it go green. Vitest, jest --watch and `cargo watch` all own that moment.
// We did not have it at all, and a tool with no local loop is a tool you remember exists only when
// CI reminds you.
//
// FOUR RULES SHAPE EVERY DECISION BELOW.
//
//   IT MUST FEEL INSTANT AND CALM. An editor writes a file several times per save, so saves are
//   debounced and a burst becomes ONE run. A second run never starts while one is in flight —
//   the latest intent is queued and everything behind it is dropped, because running a stale
//   change set after the newest one is spending money to answer a question nobody is asking any
//   more. One Chromium is kept warm for the whole session rather than launched per test.
//
//   IT MUST NEVER LOOP ON ITSELF. A passing test WRITES a recording, a failing one writes a
//   screenshot, a login writes a session file. If those writes reached the watcher, one save would
//   become an infinite run → write → run → write, on somebody's laptop, against somebody's model
//   key, overnight. The ignore list is therefore derived from the DIRECTORIES THIS RUN ACTUALLY
//   WRITES TO — the resolved --plans, --evidence-dir and --auth-dir — and not merely from the fact
//   that they are usually dotted. `--plans recordings/` must be as safe as the default.
//
//   COST MATTERS MORE HERE THAN ANYWHERE. On a pull request a run happens when somebody pushes. In
//   watch mode a run happens when somebody hits ⌘S, which can be forty times an hour. So every run
//   prints what it cost, the session prints its running total, and --max-calls is a ceiling on the
//   WHOLE SESSION rather than on each run — a per-run cap in a loop is not a cap at all.
//
//   IT IS LOCAL, AND IT STAYS LOCAL. Watch mode never posts to a project, never comments on a pull
//   request and never publishes a share link. It calls runSuite directly for exactly that reason:
//   suiteCmd owns --comment and --share, and the safest way to guarantee a thing never happens is
//   for the code that does it to be unreachable from here.
//
// THE FIVE STATUSES ARE UNTOUCHED. passed / failed / stale / errored / flaky are decided by
// lib/test.mjs and lib/suite.mjs exactly as they are on CI; this file re-runs a subset of a suite
// and prints the verdicts more compactly. Nothing here may turn one status into another. The one
// verdict this file does author is the --max-calls ceiling, and that is `errored` — our budget
// stopped the run, so nothing was observed about the application, which is what errored means.

import { watch as fsWatch, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { discover, runSuite, DEFAULT_PLANS_DIR } from "./suite.mjs";
import { testCmd, loadPlaywright } from "./test.mjs";
import { sharedBrowser } from "./pool.mjs";
import { newLedger, merge, costLine, priceFrom, priceHint, overBudget } from "./cost.mjs";
import { DEFAULT_ENGINE } from "./engines.mjs";
import { DEFAULT_AUTH_DIR } from "./auth.mjs";
import { looksProduction } from "./safety.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
  r: (s) => `\x1b[31m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

/**
 * 150ms.
 *
 * MEASURED, not picked: VS Code writing one file emits two `change` events about 20-30ms apart on
 * macOS, and a formatter-on-save (prettier, eslint --fix, gofmt) adds a third write 40-90ms after
 * the first. A 50ms debounce ran the suite twice for one ⌘S. 150ms swallows all of it and is still
 * below the ~200ms at which a keypress stops feeling like it caused the thing that followed.
 */
export const DEFAULT_DEBOUNCE_MS = 150;

/** How often the fallback walker looks, when the platform has no recursive watch. */
export const DEFAULT_POLL_MS = 500;

// Conventional build output. A repo with a source directory genuinely called `build/` will not be
// watched inside it — stated rather than hidden, and the cost of the alternative is worse: `dist/`
// is rewritten by every bundler on every save, so watching it turns one save into a rebuild storm.
const IGNORED_DIRS = new Set(["node_modules", "dist", "build", "out", "coverage", "__pycache__"]);

// What editors leave behind mid-save. vim writes a numeric probe file (4913, then 4914…) into the
// directory to test writability, emacs writes `.#name` (dotted, already covered), JetBrains writes
// `___jb_tmp___`, and every one of them is a change event for a file that is not source.
const JUNK = /(~$)|(\.sw[a-z]$)|(^___jb_(tmp|old)___$)|(^\d{4,5}$)/;

// ---- what the watcher is allowed to see ----------------------------------------------------------

/**
 * The ignore predicate, built from the directories THIS RUN WRITES TO.
 *
 * `dirs` is the load-bearing half and the reason this is not a static list. A recording lands in
 * --plans, a failure screenshot in --evidence-dir, a saved session in --auth-dir. All three default
 * to somewhere under `.smolanalytics/`, which the dotfile rule below would catch anyway — but the
 * moment somebody passes `--plans recordings/` the dotfile rule catches nothing, the first passing
 * test writes recordings/checkout.json, that write wakes the watcher, and the run that follows
 * writes it again. That is not a slow leak; it is a laptop at 100% CPU and a model bill until
 * somebody notices.
 *
 * A path under NONE of the roots is ignored: it is not ours to watch.
 */
export function ignoreFor({ roots = [], dirs = [] } = {}) {
  const rs = roots.map((r) => path.resolve(r));
  const ds = dirs.filter(Boolean).map((d) => path.resolve(d));
  return function ignored(target) {
    const p = path.resolve(target);
    // The written-to directories first, and by containment rather than by name, so `--plans
    // recordings/` is exactly as safe as the default `.smolanalytics/recordings`.
    for (const d of ds) if (p === d || p.startsWith(d + path.sep)) return true;
    let rel = null;
    for (const r of rs) {
      if (p === r) return true;
      if (!p.startsWith(r + path.sep)) continue;
      const candidate = path.relative(r, p);
      if (rel === null || candidate.length < rel.length) rel = candidate;
    }
    if (rel === null) return true;
    for (const seg of rel.split(path.sep)) {
      if (!seg) continue;
      // Dotfiles and dot-directories: .git, .smolanalytics, .next, .turbo, .env.local, .DS_Store.
      // One rule instead of a list nobody can keep current.
      if (seg.startsWith(".")) return true;
      if (IGNORED_DIRS.has(seg)) return true;
    }
    return JUNK.test(path.basename(p));
  };
}

/** Every watchable file under the roots, keyed by path, valued by a mtime+size signature. */
function snapshot(roots, ignored, cap = 20000) {
  const out = new Map();
  const stack = roots.map((r) => path.resolve(r));
  while (stack.length && out.size < cap) {
    const dir = stack.pop();
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const e of entries) {
      const abs = path.join(dir, e.name);
      // An ignored DIRECTORY is never descended into, which is the whole reason polling a repo with
      // a node_modules in it is affordable at all.
      if (ignored(abs)) continue;
      if (e.isDirectory()) stack.push(abs);
      else if (e.isFile()) {
        try {
          const st = statSync(abs);
          out.set(abs, `${st.mtimeMs}:${st.size}`);
        } catch {
          /* vanished between readdir and stat: the next tick will see it gone */
        }
      }
    }
  }
  return out;
}

/**
 * Watch the roots and call onChange(absolutePath) for anything that is not ignored.
 *
 * NATIVE FIRST, POLLING WHEN THE PLATFORM CANNOT. `fs.watch(dir, {recursive:true})` is the cheap
 * path and is what macOS, Windows and current Linux give us. Where it is unavailable Node throws
 * ERR_FEATURE_UNAVAILABLE_ON_PLATFORM rather than silently watching one level, so the throw is the
 * signal to walk-and-compare instead. There is no chokidar here and there will not be: this CLI
 * has zero dependencies, and a watcher is a hundred lines.
 */
export function startWatcher({ roots = [], ignored = () => false, onChange = () => {}, poll = false, pollMs = DEFAULT_POLL_MS, log = () => {} } = {}) {
  const watchers = [];
  let mode = poll ? "polling" : "native";
  let why = "";
  // `+ 1` because Date.now() truncates to the millisecond and st.mtimeMs does not. A file written
  // at 1000.9 against a startedAt of 1000 compares as NEWER than the watcher that opened after it,
  // and the replayed event it produces then runs the whole suite — measured, and it is why this is
  // not a bare Date.now().
  const startedAt = Date.now() + 1;
  if (mode === "native") {
    for (const r of roots) {
      try {
        const w = fsWatch(path.resolve(r), { recursive: true }, (_event, name) => {
          if (!name) return;
          const abs = path.resolve(r, name.toString());
          if (ignored(abs)) return;
          // A CHANGE IS A FILE THAT IS THERE NOW. Three things this one rule throws away, each of
          // which was measured starting a run nobody asked for:
          //
          //   THE PHANTOM. macOS reports the watched root itself, and Node renders that as the
          //   root's own basename — `/tmp/app/app`, a path that does not exist. It arrives at
          //   startup, it is not any test's file, so it read as "a source file changed" and fired
          //   a whole-suite agent run before the person had touched anything.
          //
          //   THE DIRECTORY. macOS reports the containing directory alongside the file inside it.
          //   Left in, `tests/` lands in the change set as a path that is not any test's file, and
          //   "you edited one test" becomes "run all fifty".
          //
          //   THE REPLAY. FSEvents delivers what happened in the moments BEFORE the watcher
          //   opened, so `save a file, then start watch` ran the suite twice. A real save always
          //   moves mtime forward past the moment this watcher started.
          //
          // THE COST, STATED: a DELETION is not a change here, because a deleted path cannot be
          // told from the phantom. Deleting a file is almost always accompanied by saving another
          // one, which does re-run; and the polling fallback below, which compares snapshots, sees
          // deletions properly. A missed re-run is a keystroke away. A phantom run is money.
          let st = null;
          try {
            st = statSync(abs);
          } catch {
            return;
          }
          if (!st.isFile() || st.mtimeMs < startedAt) return;
          onChange(abs);
        });
        // A watcher that errors — the directory was renamed out from under it — must not take the
        // process down. The run in flight still finishes and the next save still works if the
        // directory comes back.
        w.on("error", (e) => log(C.dim(`  the watcher on ${r} reported ${e && e.message ? e.message : e}`)));
        watchers.push(w);
      } catch (e) {
        for (const w of watchers) {
          try {
            w.close();
          } catch {
            /* closing a watcher that never opened */
          }
        }
        watchers.length = 0;
        mode = "polling";
        why = e && e.message ? e.message : String(e);
        break;
      }
    }
  }

  let timer = null;
  if (mode === "polling") {
    let prev = snapshot(roots, ignored);
    timer = setInterval(() => {
      const now = snapshot(roots, ignored);
      for (const [p, sig] of now) if (prev.get(p) !== sig) onChange(p);
      for (const p of prev.keys()) if (!now.has(p)) onChange(p);
      prev = now;
    }, pollMs);
  }

  return {
    mode,
    why,
    close() {
      for (const w of watchers) {
        try {
          w.close();
        } catch {
          /* already closed */
        }
      }
      watchers.length = 0;
      if (timer) clearInterval(timer);
      timer = null;
    },
  };
}

// ---- which tests a change is allowed to skip ------------------------------------------------------

// The exported names lib/select.mjs might reasonably use. This file is written before that one
// exists; the ONLY safe way to depend on something unwritten is to make every failure to
// understand it fall back to running the whole suite, which is what the pull request does today.
const SELECT_NAMES = ["selectTests", "select", "affectedTests", "affected", "testsForChanges", "selectForChanges", "chooseTests", "default"];

/** lib/select.mjs, or null. A module that does not exist, or does not load, is simply "no mapping". */
export async function loadSelector(importer = (s) => import(s)) {
  try {
    const mod = await importer("./select.mjs");
    return mod && pickSelectFn(mod) ? mod : null;
  } catch {
    return null;
  }
}

export function pickSelectFn(mod) {
  if (!mod || typeof mod !== "object") return null;
  for (const name of SELECT_NAMES) if (typeof mod[name] === "function") return mod[name];
  return null;
}

/**
 * Turn whatever the mapping returned into a set of test ids we know.
 *
 * `ok: false` means "we did not understand this", and every caller answers that by running the
 * whole suite. That asymmetry is deliberate: a mapping we misread that runs everything costs time,
 * a mapping we misread that runs nothing costs a bug reaching production green.
 */
export function normalizeSelection(out, tests) {
  const byId = new Map(tests.map((t) => [t.id, t]));
  const byName = new Map(tests.map((t) => [t.name, t]));
  const byFile = new Map();
  for (const t of tests) {
    const key = path.resolve(t.file);
    if (!byFile.has(key)) byFile.set(key, []);
    byFile.get(key).push(t);
  }

  let list = out;
  if (list instanceof Set) list = [...list];
  else if (list && !Array.isArray(list) && typeof list === "object") {
    list = Array.isArray(list.tests) ? list.tests : Array.isArray(list.selected) ? list.selected : Array.isArray(list.ids) ? list.ids : null;
  }
  if (!Array.isArray(list)) return { ok: false, ids: new Set() };

  const ids = new Set();
  for (const entry of list) {
    const key = entry && typeof entry === "object" ? entry.id || entry.file || entry.name : entry;
    if (typeof key !== "string" || !key) return { ok: false, ids: new Set() };
    if (byId.has(key)) {
      ids.add(key);
      continue;
    }
    if (byName.has(key)) {
      ids.add(byName.get(key).id);
      continue;
    }
    const asFile = byFile.get(path.resolve(key));
    if (asFile) {
      for (const t of asFile) ids.add(t.id);
      continue;
    }
    // One entry we cannot place means we have the shape wrong. Say so, and run everything.
    return { ok: false, ids: new Set() };
  }
  return { ok: true, ids };
}

/**
 * Ask the mapping, defensively.
 *
 * Two call shapes are tried because this is written against an interface that does not exist yet.
 * A mapping that throws on both, or answers in a shape normalizeSelection cannot read, is not an
 * error — it is "no selection", and the suite runs whole.
 */
export async function callSelector(mod, arg) {
  const fn = pickSelectFn(mod);
  if (!fn) return { ok: false, ids: new Set() };
  let out;
  try {
    out = await fn(arg);
  } catch {
    try {
      out = await fn(arg.tests, arg.changed);
    } catch {
      return { ok: false, ids: new Set() };
    }
  }
  return normalizeSelection(out, arg.tests);
}

/**
 * The tests this save is going to run, and the sentence explaining why.
 *
 * THE ONE NARROWING THAT NEEDS NO MAPPING AT ALL: a changed .md that IS one of the suite's files
 * selects exactly the tests written in it. No diff analysis can beat that, and it makes the local
 * loop useful on day one — editing a test and watching only that test re-run is most of the value.
 *
 * Everything else is lib/select.mjs's job, and its absence means the whole suite runs, which is
 * precisely what happens on a pull request today. Watch mode is never allowed to be the reason a
 * test did not run.
 */
export async function selectAffected({ tests, changed, selector = null, plansDir = "", env = process.env }) {
  const all = { tests, why: "no selection mapping yet, so all of them run", narrowed: false };
  if (!tests.length) return { tests: [], why: "there are no tests to run", narrowed: false };

  const suiteFiles = new Map();
  for (const t of tests) suiteFiles.set(path.resolve(t.file), true);
  const changedAbs = changed.map((c) => path.resolve(c));
  const changedSuite = new Set(changedAbs.filter((c) => suiteFiles.has(c)));
  const changedOther = changedAbs.filter((c) => !suiteFiles.has(c));
  const fromMd = tests.filter((t) => changedSuite.has(path.resolve(t.file)));

  if (!changedOther.length) {
    return { tests: fromMd, why: fromMd.length === 1 ? "its own test file changed" : "their own test files changed", narrowed: true };
  }
  if (!selector) return all;

  const picked = await callSelector(selector, { tests, changed: changedOther, plansDir, env });
  if (!picked.ok) return { ...all, why: "the selection mapping did not recognise this change, so all of them run" };

  const ids = new Set(picked.ids);
  for (const t of fromMd) ids.add(t.id);
  const chosen = tests.filter((t) => ids.has(t.id));
  if (!chosen.length) return { tests: [], why: "no test touches what changed", narrowed: true };
  return { tests: chosen, why: "the selection mapping connects them to what changed", narrowed: chosen.length < tests.length };
}

// ---- one Chromium, kept warm for the whole session -------------------------------------------------

/**
 * A `loadBrowser` for lib/test.mjs that reuses ONE browser across every test AND every run, and
 * that this file can close on its own terms.
 *
 * Two reasons, and the second is the one that matters.
 *
 * SPEED. Launching Chromium is ~200ms measured (lib/pool.mjs has the table). A save that re-runs
 * three tests pays it three times, every save, forever. A lease on a warm browser is ~40ms and the
 * loop stops feeling like a build.
 *
 * CTRL-C. Playwright installs its own SIGINT handler and does close browsers gracefully — measured
 * here, and it is why the orphan test passes on both mechanisms. But a promise about no orphan
 * Chromium should not rest on a dependency's signal handler that nothing in this repo controls. By
 * launching through this, the browser is an object THIS file holds, and stopping is `await
 * close()` rather than a hope.
 *
 * A dead browser is recycled rather than leased: a watch session lives for hours, Chromium
 * occasionally does not, and a lease on a corpse would turn every later save into `errored`.
 */
export function warmBrowser({ load = loadPlaywright } = {}) {
  let shared = null;
  let everLaunched = false;
  const live = new Set();

  // Wraps the real playwright module so every browser this session launches is an object we hold.
  // sharedBrowser() calls launchEngine(pw, engine, opts), which calls pw[engine].launch(opts).
  const proxied = async (log, yes, engine) => {
    const r = await load(log, yes, engine);
    if (!r || !r.pw) return r || { pw: null, problem: "The browser could not be loaded." };
    const type = r.pw[engine];
    if (!type || typeof type.launch !== "function") return r;
    const launch = async (opts) => {
      const b = await type.launch(opts);
      everLaunched = true;
      live.add(b);
      try {
        b.on?.("disconnected", () => live.delete(b));
      } catch {
        /* a browser stand-in without an event emitter is still a browser we can close */
      }
      return b;
    };
    return { ...r, pw: { ...r.pw, [engine]: { ...type, launch } } };
  };

  const dropShared = async () => {
    const s = shared;
    shared = null;
    everLaunched = false;
    await s?.close().catch(() => {});
  };

  return {
    loadBrowser: async (log, yes, engine = DEFAULT_ENGINE) => {
      // Every browser we launched has disconnected: the process died between runs. Leasing from the
      // old sharedBrowser would hand back a wrapper around it and every test would error.
      if (shared && everLaunched && live.size === 0) await dropShared();
      if (!shared) shared = sharedBrowser({ load: proxied });
      return shared.loadBrowser(log, yes, engine);
    },
    /** Every browser this session opened, closed. What makes Ctrl-C a guarantee and not a hope. */
    close: async () => {
      await dropShared();
      // Whatever the shared lease did not account for — a second browser launched under different
      // options, or one whose owner never closed it. Already-closed browsers are skipped rather
      // than closed twice: closing a dead handle is harmless but it is also a lie in a log.
      for (const b of [...live]) {
        live.delete(b);
        if (b.isConnected && b.isConnected() === false) continue;
        await b.close?.().catch(() => {});
      }
    },
    get open() {
      return live.size;
    },
  };
}

// ---- what a run looks like on the terminal ---------------------------------------------------------

const secs = (ms) => {
  const n = Number(ms);
  return Number.isFinite(n) && n >= 0 ? `${(n / 1000).toFixed(1)}s` : "";
};

/** `14:23:07` — a second terminal is glanced at, and "how old is this?" is the first question. */
export function clockOf(d = new Date()) {
  const p = (n) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/** What just changed, short enough to read sideways. */
export function changedLine(changed, root = process.cwd()) {
  const rels = changed.map((c) => path.relative(root, path.resolve(c)) || path.basename(c)).sort();
  if (!rels.length) return "something changed";
  if (rels.length === 1) return `${rels[0]} changed`;
  if (rels.length <= 3) return `${rels.join(", ")} changed`;
  return `${rels[0]} and ${rels.length - 1} more files changed`;
}

/** `running 2 of 12 tests · the selection mapping connects them to what changed` */
export function selectionLine({ picked, total, why }) {
  const head = picked === total ? `running all ${total} test${total === 1 ? "" : "s"}` : `running ${picked} of ${total} tests`;
  return why ? `${head} ${C.dim(`· ${why}`)}` : head;
}

// The five statuses, each printed as itself. Padded so the names align and the eye can find the one
// that is not "passed" without reading. Never abbreviated to a symbol: a ✗ that means four
// different things is exactly the blurring the statuses exist to prevent.
const WORD = {
  passed: (s) => C.g(s),
  failed: (s) => C.r(s),
  stale: (s) => C.y(s),
  errored: (s) => C.y(s),
  flaky: (s) => C.y(s),
};

function wrap(text, width, indent) {
  const words = String(text).split(/\s+/).filter(Boolean);
  const lines = [];
  let cur = "";
  for (const w of words) {
    if (cur && cur.length + 1 + w.length > width) {
      lines.push(cur);
      cur = w;
    } else cur = cur ? `${cur} ${w}` : w;
  }
  if (cur) lines.push(cur);
  return lines.map((l) => `${indent}${l}`);
}

/**
 * One line per test, plus the reason under anything that is not a pass.
 *
 * The reason is printed IN FULL and never truncated. It is the bug report — the entire argument for
 * this product is that a failure describes what was actually seen — and a watch loop that clipped
 * it would send the reader to a second place to find out what broke.
 */
export function resultLines(results, { width = 96 } = {}) {
  const out = [];
  for (const r of results) {
    const paint = WORD[r.status] || ((s) => s);
    out.push(`  ${paint(r.status.padEnd(7))}  ${r.name} ${C.dim(secs(r.ms))}`);
    if (r.status !== "passed" && r.reason) for (const line of wrap(r.reason, width, "           ")) out.push(C.dim(line));
    // The diff hint, when there is one, under the failure it belongs to — same two lines the pull
    // request comment gets, and just as advisory.
    if (r.status === "failed") for (const s of (Array.isArray(r.suspects) ? r.suspects : []).slice(0, 2)) out.push(C.dim(`           suspect: ${s.file} — ${s.evidence}`));
  }
  return out;
}

/** `3 tests · 2 passed · 1 failed · 4.6s · 3 model calls · 12,004 in / 812 out` */
export function summaryLine(results, ledger, price = null) {
  const count = (s) => results.filter((r) => r.status === s).length;
  const parts = [`${results.length} test${results.length === 1 ? "" : "s"}`, `${count("passed")} passed`];
  if (count("failed")) parts.push(C.r(`${count("failed")} failed`));
  if (count("flaky")) parts.push(C.y(`${count("flaky")} flaky`));
  if (count("stale")) parts.push(C.y(`${count("stale")} stale`));
  if (count("errored")) parts.push(C.y(`${count("errored")} could not run`));
  parts.push(secs(results.reduce((a, r) => a + (r.ms || 0), 0)));
  parts.push(costLine(ledger, price));
  return parts.join(C.dim(" · "));
}

/** `session · 4 runs · 7 model calls · 30,010 in / 1,900 out` */
export function sessionLine(runs, ledger, price = null) {
  return C.dim(`session · ${runs} run${runs === 1 ? "" : "s"} · ${costLine(ledger, price)}`);
}

// ---- the command -----------------------------------------------------------------------------------

/**
 * Watch, and re-run what the save affected.
 *
 * Resolves with an exit code only once it is stopped — by Ctrl-C, or by the `stop()` handed to
 * `onReady`. A watch loop's exit code is NEVER a verdict about the application: 0 when it was
 * asked to stop, 2 when it could not start. 1 is not among them on purpose, because 1 is the code
 * this product reserves for "a test failed" and no CI gate should ever be reading a watch session.
 */
export async function watchCmd({
  suite,
  url,
  root = process.cwd(),
  plans = DEFAULT_PLANS_DIR,
  evidenceDir = "",
  authDir = DEFAULT_AUTH_DIR,
  headed = false,
  yes = false,
  maxSteps = 40,
  retries = 1,
  layout = "report",
  renderCheck = true,
  engine = DEFAULT_ENGINE,
  login = "",
  authFile = "",
  teardown = "",
  seed = "",
  emailDomain = "",
  maxCalls = 0,
  debounceMs = DEFAULT_DEBOUNCE_MS,
  poll = false,
  pollMs = DEFAULT_POLL_MS,
  // The baseline. Without it the terminal sits blank until the first save and the reader cannot
  // tell a working watcher from a broken one — but it is a whole-suite run, so it is a flag.
  initial = true,
  verbose = false,
  signals = true,
  log = console.log,
  env = process.env,
  discoverImpl = discover,
  runSuiteImpl = runSuite,
  testCmdImpl = testCmd,
  loadBrowser = loadPlaywright,
  // undefined asks lib/select.mjs; null is "there is no mapping"; a module object is injected.
  selector = undefined,
  onReady = null,
  onRunComplete = null,
} = {}) {
  if (!suite) {
    log(`\n${C.y("--suite is missing.")} watch re-runs a folder of tests when a file changes.`);
    log(C.dim("  npx smolanalytics watch --suite tests/ --url http://localhost:3000"));
    return 2;
  }
  if (!url) {
    log(`\n${C.y("--url is missing.")} --suite says which tests to run, --url says where to run them.`);
    log(C.dim("  npx smolanalytics watch --suite tests/ --url http://localhost:3000"));
    return 2;
  }

  // A PRODUCTION-LOOKING URL IS REFUSED HERE, UP FRONT, RATHER THAN ASKED ABOUT LATER.
  //
  // lib/safety.mjs asks the question at the start of a run — and it would ask it here too, except
  // that the runner's transcript is suppressed by default so the compact block below can be the
  // output. The prompt would be printed into a log nobody is reading while readline quietly held
  // the terminal: a loop that looks hung, for a reason that is invisible. And the shape of this
  // command makes it worse than a one-off `test` would be — watch re-runs on every ⌘S, so a
  // production URL here is not one accidental order, it is one per save all afternoon.
  if (looksProduction(url) && !yes) {
    log(`\n${C.y(`${url} looks like production, and watch re-runs on every save.`)}`);
    log(C.dim("  Point it at your dev server, or pass --yes if you really do mean that URL."));
    log(C.dim("  Nothing was opened and nothing was tested."));
    return 2;
  }

  const rootAbs = path.resolve(root);
  const suiteAbs = path.resolve(suite);
  const plansDir = /\.json$/i.test(plans) ? path.dirname(plans) : plans;
  const evidenceAbs = path.resolve(evidenceDir || path.join(".smolanalytics", "evidence"));
  const roots = [rootAbs];
  // A suite kept outside the repo still has to wake the loop when it is edited.
  if (!(suiteAbs === rootAbs || suiteAbs.startsWith(rootAbs + path.sep))) roots.push(suiteAbs);

  const ignored = ignoreFor({
    roots,
    // EVERY DIRECTORY THIS RUN WRITES TO. See the header: this is the loop-breaker, and it is
    // derived from the real options rather than from the assumption that they are dotted.
    dirs: [path.resolve(plansDir), evidenceAbs, path.resolve(authDir)],
  });

  const sel = selector === undefined ? await loadSelector() : selector;
  const warm = warmBrowser({ load: loadBrowser });
  const session = newLedger();
  const price = priceFrom(env);
  let runs = 0;
  let hintShown = false;

  const first = discoverImpl(suite, plansDir);
  if (first.missing) {
    log(`\n${C.y(`no such file or directory: ${first.missing}`)}`);
    log(C.dim("  --suite points at a folder of .md files, one sentence per test."));
    return 2;
  }

  // ---- the queue --------------------------------------------------------------------------------
  //
  // Three states and no more: idle, running, and running-with-one-run-queued. The set of changed
  // paths accumulates across all of them, so a burst that lands mid-run is ONE later run over the
  // union of what changed — never one run per save, and never a run over a stale change set while a
  // newer one waits behind it.
  //
  // DECLARED BEFORE THE WATCHER. `bump` is hoisted, but `pending` and `timer` are not: a change
  // event arriving between the two would land in the temporal dead zone and throw inside a
  // filesystem callback, where nothing is there to catch it.
  const pending = new Set();
  let timer = null;
  let running = false;
  let stopping = false;
  let saidQueued = false;
  let settle = null;
  let stopCode = 0;

  const watcher = startWatcher({ roots, ignored, onChange: (p) => bump(p), poll, pollMs, log });

  log("");
  log(`${C.b("watch")}  ${rootAbs}`);
  log(`${C.dim("tests")}  ${suite} ${C.dim(`· ${first.tests.length} test${first.tests.length === 1 ? "" : "s"} · ${watcher.mode}${watcher.why ? ` (${watcher.why})` : ""}`)}`);
  log(`${C.dim("url")}    ${url}`);
  if (sel) log(C.dim("       a save re-runs only the tests the change affects"));
  else log(C.dim("       no selection mapping yet, so a save re-runs the whole suite"));
  if (maxCalls > 0) log(C.dim(`       --max-calls ${maxCalls} is a ceiling on this whole session, not on each run`));
  log(C.dim("       local only: nothing is posted, commented or shared"));
  log(C.dim(`       ctrl-c to stop${verbose ? "" : " · --verbose for the runner's own output"}`));

  function bump(p) {
    if (stopping) return;
    pending.add(p);
    if (running) {
      if (!saidQueued) {
        saidQueued = true;
        log(C.dim("  saved again — one more run is queued for when this one finishes"));
      }
      return;
    }
    if (timer) clearTimeout(timer);
    timer = setTimeout(fire, debounceMs);
  }

  function fire() {
    timer = null;
    if (stopping || running) return;
    const changed = [...pending];
    pending.clear();
    saidQueued = false;
    if (!changed.length) return;
    running = true;
    Promise.resolve()
      .then(() => runFor(changed))
      .catch((e) => log(C.y(`  the watch loop could not complete a run: ${e && e.message ? e.message : e}`)))
      .finally(() => {
        running = false;
        if (stopping) return;
        // Anything that arrived while we ran becomes the ONE queued run, debounced again so a
        // save landing on the last millisecond of a run does not start one it will only interrupt.
        if (pending.size) {
          if (timer) clearTimeout(timer);
          timer = setTimeout(fire, debounceMs);
        }
      });
  }

  // ---- one run ----------------------------------------------------------------------------------

  async function runFor(changed, { all = false } = {}) {
    log("");
    log(C.dim(`${"─".repeat(46)}  ${clockOf()}`));
    log(all ? C.dim("first run — the whole suite, as a baseline") : changedLine(changed, rootAbs));

    // THE CEILING, CHECKED BEFORE THE RUN AND ACROSS THE SESSION. A per-run cap in a loop that runs
    // on every ⌘S is not a cap; this is the number the person actually meant.
    const capped = maxCalls > 0 ? overBudget(session, maxCalls) : "";
    if (capped) {
      log(C.y(capped));
      log(C.dim("  watch is still watching, but it will not spend again. Stop it and raise --max-calls."));
      finishRun([], newLedger(), changed);
      return;
    }

    // RE-DISCOVERED EVERY RUN, never cached. Adding a test file and having the loop ignore it until
    // a restart is the exact thing that makes somebody stop trusting a watcher.
    const found = discoverImpl(suite, plansDir);
    if (found.missing) {
      log(C.y(`${found.missing} is gone, so nothing ran. Watch is still watching.`));
      finishRun([], newLedger(), changed);
      return;
    }
    for (const n of found.notes || []) log(C.y(`  ${n}`));
    for (const e of found.errors || []) log(C.y(`  ${e}`));

    const pick = all
      ? { tests: found.tests, why: found.tests.length ? "the baseline run checks everything" : `no tests found in ${suite}`, narrowed: false }
      : await selectAffected({ tests: found.tests, changed, selector: sel, plansDir, env });
    if (!pick.tests.length) {
      log(C.dim(`nothing to run ${C.dim(`· ${pick.why}`)}`));
      finishRun([], newLedger(), changed);
      return;
    }
    log(selectionLine({ picked: pick.tests.length, total: found.tests.length, why: pick.why }));

    const runLedger = newLedger();
    // ONE test's spend at a time, folded into the run and the session as it lands, so the ceiling
    // handed to the NEXT test already knows what this one cost.
    const runTest = async (opts) => {
      if (maxCalls > 0) {
        const stop = overBudget(session, maxCalls);
        if (stop) {
          // errored, and it says whose decision it was. Our budget stopped this, so nothing was
          // observed about the application — which is exactly what errored means.
          try {
            opts.onRun?.({ test: opts.test, status: "errored", mode: "agent", durationMs: 0, url: opts.url, reason: stop });
          } catch {
            /* a caller's bookkeeping must not change a verdict */
          }
          return 2;
        }
      }
      const testLedger = newLedger();
      try {
        return await testCmdImpl({
          ...opts,
          // The session ceiling, expressed as what is left. lib/cost.mjs checks it BEFORE each
          // model call against this test's own ledger, so remaining + already-spent is the ceiling.
          maxCalls: maxCalls > 0 ? Math.max(1, maxCalls - session.calls) : 0,
          ledger: testLedger,
          // The warm browser, overriding the serial pool's undefined. One Chromium, all session.
          loadBrowser: warm.loadBrowser,
        });
      } finally {
        Object.assign(runLedger, merge(runLedger, testLedger));
        Object.assign(session, merge(session, testLedger));
      }
    };

    const results = await runSuiteImpl({
      tests: pick.tests,
      url,
      plansDir,
      headed,
      yes,
      maxSteps,
      retries,
      evidenceDir,
      layout,
      renderCheck,
      engine,
      login,
      authFile,
      authDir,
      teardown,
      seed,
      emailDomain,
      // ONE AT A TIME. A watch run is two or three tests and a person waiting; lanes buy nothing
      // and cost a braided transcript.
      workers: 1,
      // The runner's own transcript is a page long per test. Off by default because the compact
      // block below is the point of this command, on with --verbose when a run needs explaining.
      log: verbose ? log : () => {},
      env,
      hasKey: Boolean(env.ANTHROPIC_API_KEY),
      runTest,
      loadBrowser: warm.loadBrowser,
      // NOT PASSED, AND THAT IS THE GUARANTEE: no `share`, no `comment`, no publish. This loop is
      // local, and the way to be sure is for the code that posts to be unreachable from here.
    });

    for (const line of resultLines(results)) log(line);
    log(summaryLine(results, runLedger, price));
    finishRun(results, runLedger, changed);
  }

  function finishRun(results, runLedger, changed) {
    // Counted only when tests actually ran. A save that selected nothing, or that arrived after the
    // ceiling, is a decision rather than a run, and inflating the number under "session" would make
    // the one figure a person uses to judge what this is costing them slightly untrue.
    if (results.length) runs += 1;
    log(sessionLine(runs, session, price));
    const hint = priceHint(session, price);
    if (hint && !hintShown) {
      hintShown = true;
      log(C.dim(hint));
    }
    try {
      onRunComplete?.({ results, runLedger, session, changed, runs });
    } catch {
      /* a caller's bookkeeping must not break the loop */
    }
  }

  // ---- stopping ---------------------------------------------------------------------------------

  let stopped = null;
  async function stop(code = 0) {
    if (stopping) return stopped;
    stopping = true;
    stopCode = code;
    if (timer) clearTimeout(timer);
    timer = null;
    watcher.close();
    // Printed synchronously and BEFORE anything is awaited: Playwright installs its own SIGINT
    // handler which will exit the process once it has closed its browsers, and a summary behind an
    // await is a summary that may never print.
    log("");
    log(sessionLine(runs, session, price));
    log(C.dim("stopped. Nothing was posted, commented or shared."));
    stopped = (async () => {
      // THE ORPHAN GUARANTEE. Every browser this session launched came through warmBrowser, so
      // this is the object being closed and not a hope about somebody else's signal handler.
      await warm.close().catch(() => {});
      settle?.(code);
      return code;
    })();
    return stopped;
  }

  const handlers = [];
  if (signals) {
    for (const sig of ["SIGINT", "SIGTERM"]) {
      const onSig = () => {
        // A second Ctrl-C from somebody who has waited long enough leaves immediately.
        if (stopping) process.exit(sig === "SIGTERM" ? 143 : 130);
        stop(sig === "SIGTERM" ? 143 : 130).then((code) => process.exit(code));
      };
      process.on(sig, onSig);
      handlers.push([sig, onSig]);
    }
  }

  // BEFORE the baseline run, not after it. The caller — a person pressing Ctrl-C, or a test — has
  // to be able to stop a session during its first run, which on a fresh suite is the longest one
  // it will ever do.
  onReady?.({ stop, watcher, warm, session, get runs() { return runs; } });

  if (initial && !stopping) {
    // The baseline, through the same in-flight flag as every save, so there is one rule about what
    // may run while something else is running rather than two that can disagree.
    running = true;
    await Promise.resolve()
      .then(() => runFor([], { all: true }))
      .catch((e) => log(C.y(`  the first run could not complete: ${e && e.message ? e.message : e}`)))
      .finally(() => {
        running = false;
        if (pending.size && !stopping) {
          if (timer) clearTimeout(timer);
          timer = setTimeout(fire, debounceMs);
        }
      });
  }

  const code = await new Promise((resolve) => {
    settle = resolve;
    if (stopping) resolve(stopCode);
  });
  for (const [sig, fn] of handlers) process.removeListener(sig, fn);
  return code;
}
