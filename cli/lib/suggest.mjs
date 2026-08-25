// `npx smolanalytics suggest` — the answer to "what do I even write". One command, no account:
// a real browser walks the running app, and the flows worth testing land in tests/*.md, in the
// exact format `test --suite tests/` already runs.
//
// WHY THIS COMMAND EXISTS. The competing product's onboarding LEADS with this: before anything
// runs, a planner maps the app and drafts a test plan — behind the GitHub App, the Dockerfile and
// the hour of environment building (see lib/test.mjs for that timeline). The planner is the one
// genuinely good idea in that flow, because the person staring at an empty tests/ folder writes
// either nothing or "the homepage loads". So this is that idea with the cost removed: a URL in,
// a handful of .md files out, delete the ones you disagree with. Nothing else is touched.
//
// WHAT IT REFUSES TO DO: INVENT. Ask a model "what should an app like this test?" and it answers
// from every app it has ever read about — password resets, wishlists, coupon codes — whether or
// not this app has them. One such file is worse than an empty folder: the test fails forever
// against a feature that never existed, and teaches the reader to distrust the files beside it.
// So the model is shown only what the crawl actually saw, told to quote its evidence, and a
// proposal whose quote appears on none of the visited pages is dropped here, out loud, before it
// becomes a file. Fewer than --max real flows means fewer files, never padding.
//
// NO VERDICTS LIVE HERE. This command reads the app and writes markdown; it never decides
// passed/failed/stale/errored/flaky, and it never exits 1 — that code means "the application is
// broken", and a survey cannot learn that. 0 is "proposals written (or honestly none)"; 2 is
// "this runner could not do its job" (no key, no browser, the app unreachable, the model down).

import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { perceive, render, loadPlaywright } from "./test.mjs";
import { slug } from "./suite.mjs";
import { PLACEHOLDER_LIST } from "./safety.mjs";

const C = {
  b: (s) => `\x1b[1m${s}\x1b[0m`,
  dim: (s) => `\x1b[2m${s}\x1b[0m`,
  g: (s) => `\x1b[32m${s}\x1b[0m`,
  y: (s) => `\x1b[33m${s}\x1b[0m`,
};

// ---- the walk ---------------------------------------------------------------------------------
//
// Deterministic breadth-first, not agentic. The agent loop in test.mjs pays one model call per
// step because each step DEPENDS on the last; a survey has no such dependency, so the whole walk
// is plain navigation and the model is called exactly once, with everything the walk saw. That is
// the difference between a command that costs one call and one that costs thirty.

/**
 * Both bounds, and why they are small. The signal for "what flows exist" is in the navigation and
 * the first page of each section — a products LIST proves browsing exists, products 2 through 40
 * prove it again. Measured on the twenty-link fixture in test/suggest.test.mjs with MAX_PAGES
 * lifted: 21 page loads, twenty of them the same template, to learn what the first one already
 * said — and on a real catalogue that budget is spent before the walk ever reaches /pricing, which
 * is the page the money test comes from. Depth 2 from the start URL is where distinct sections
 * live; deeper is detail pages of sections already seen.
 */
const MAX_PAGES = 8;
const MAX_DEPTH = 2;

/**
 * Links a read-only survey must not follow. GET is not always read-only in real apps: /logout
 * drops the session, and to-do-list-era apps mutate on plain <a href> ("/delete?id=3"). Proved by
 * deleting this filter and running the fixture in test/suggest.test.mjs: the walk went to /logout
 * on its fifth page load, and the fixture's own server recorded the request — so on a real app
 * every page after it would have been surveyed signed out. Matched against whole path segments,
 * not substrings, for the same reason SAFE_WORDS in safety.mjs is: "remove" as a substring would
 * ban a legitimate /removals page.
 */
const MUTATING = /(?:^|\/)(?:logout|log-out|signout|sign-out|delete|remove|destroy)(?:$|\/|\.)/i;

/** Files, not pages: a browser pointed at a .pdf perceives zero elements and burns a page slot. */
const ASSET = /\.(?:pdf|zip|gz|tar|dmg|exe|png|jpe?g|gif|svg|webp|ico|css|js|mjs|map|mp3|mp4|webm|xml|rss|atom|txt|csv|json|woff2?)$/i;

/** One URL per page, however it is linked: "#section" anchors are the same document. */
function noHash(href) {
  const u = new URL(href);
  u.hash = "";
  return u.href;
}

/**
 * Where the links come from: the DOM, not the aria snapshot. This machine's Playwright (1.52)
 * prints `- /url: /pricing` under each link in ariaSnapshot(), but flatten() reads only
 * `- role "name"` lines, so the href never survives perception — and older Playwrights that
 * test.mjs still supports do not print `/url` at all. `a.href` in the page is absolute and
 * version-proof.
 */
async function linksOn(page) {
  return page.evaluate(() => Array.from(document.querySelectorAll("a[href]"), (a) => a.href)).catch(() => []);
}

/**
 * Open one page. Returns "" or why it could not be opened — and, crucially, does not leave the
 * failure in flight for the NEXT page to trip over.
 *
 * WHEN goto() REJECTS, THE TAB HAS NOT MOVED YET. Measured on this file's own fixture, where one
 * link in the nav drops its socket: goto("/broken") rejected with net::ERR_EMPTY_RESPONSE while
 * page.url() still reported the PREVIOUS page, and the error document committed a beat later —
 * on top of the goto that had started in the meantime. So /checkout, a real page two links deep
 * and the one the money test comes from, died with `Navigation to ".../checkout" is interrupted by
 * another navigation to "chrome-error://chromewebdata/"`. One broken link in somebody's navigation
 * silently costing the survey the pages behind it is the fabrication problem from the other side:
 * proposals missing, with no reason given for their absence.
 *
 * Waiting for that commit is the fix, and the only signal that actually marks it is a frame
 * navigation. Three plausible alternatives were measured and all still lost /checkout: parking on
 * about:blank (that goto is interrupted by the same pending commit, then lands on the next page's),
 * retrying the navigation (each attempt interrupts the last, three for three), and
 * waitForLoadState (the tab is still on the old page, which is already loaded, so it returns at
 * once). A `waitForTimeout` did work, and is what this replaces: a fixed sleep is a guess about
 * somebody else's machine.
 */
async function open(page, url) {
  try {
    await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
    return "";
  } catch (e) {
    const detail = String(e && e.message ? e.message : e).split("\n")[0];
    // Bounded, because a goto that failed by TIMING OUT may have nothing left to commit and no
    // event will ever come. Three seconds of patience once per dead link, against losing every
    // page behind it.
    await page.waitForEvent("framenavigated", { timeout: 3000 }).catch(() => {});
    return detail;
  }
}

export async function crawl(page, startUrl, log = () => {}) {
  const start = new URL(startUrl);
  const queued = new Set([noHash(start.href)]);
  const seen = new Set();
  const queue = [{ url: start.href, depth: 0 }];
  const pages = [];

  while (queue.length && pages.length < MAX_PAGES) {
    const { url, depth } = queue.shift();
    const key = noHash(url);
    if (seen.has(key)) continue;
    seen.add(key);
    const failed = await open(page, url);
    if (failed) {
      // The START page failing is the whole run failing: there is nothing to survey and nothing
      // honest to propose. A page found mid-walk failing is just a dead link in their nav — noted
      // and walked past, because the other pages still carry the evidence.
      if (depth === 0) throw new Error(`could not open ${url}: ${failed}`);
      log(C.dim(`  could not open ${url} (${failed}) — skipped`));
      continue;
    }
    // Same grace as act() in test.mjs: analytics beacons and long-polling keep real apps from
    // ever reaching networkidle, and a survey must not hang on somebody's live-chat widget.
    await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});

    let landed;
    try {
      landed = new URL(page.url());
    } catch {
      continue;
    }
    if (landed.origin !== start.origin) {
      // Redirected off the app — an auth wall, an SSO provider, a marketing site. Its content is
      // evidence about SOMEBODY'S app, which is exactly the fabrication material to keep out.
      log(C.dim(`  ${url} redirected off this app (${landed.origin}) — skipped`));
      continue;
    }
    seen.add(noHash(landed.href));
    const snap = await perceive(page);
    pages.push(snap);
    log(C.dim(`  read ${snap.url} — ${snap.elements.length} elements`));

    if (depth >= MAX_DEPTH) continue;
    for (const href of await linksOn(page)) {
      let u;
      try {
        u = new URL(href);
      } catch {
        continue;
      }
      if (u.origin !== start.origin) continue;
      if (MUTATING.test(u.pathname) || ASSET.test(u.pathname)) continue;
      const k = noHash(u.href);
      if (queued.has(k) || seen.has(k)) continue;
      queued.add(k);
      queue.push({ url: u.href, depth: depth + 1 });
    }
  }
  return pages;
}

// ---- the one model call -----------------------------------------------------------------------

const PROPOSE = {
  name: "propose",
  description: "Propose the end-to-end tests this application is worth writing, most important first.",
  input_schema: {
    type: "object",
    properties: {
      tests: {
        type: "array",
        items: {
          type: "object",
          properties: {
            title: { type: "string", description: 'A short name for the test, e.g. "A shopper can pay for Pro".' },
            sentence: { type: "string", description: "The whole test: one sentence of plain English a careful person could carry out." },
            criticality: { type: "string", enum: ["critical", "normal"] },
            evidence: { type: "string", description: "One short run of text quoted VERBATIM from the pages shown — an element name, a heading — that proves this flow exists." },
          },
          required: ["title", "sentence", "criticality", "evidence"],
          additionalProperties: false,
        },
      },
    },
    required: ["tests"],
    additionalProperties: false,
  },
};

const SYSTEM = `You are surveying a web application to propose the end-to-end tests worth writing for it.

You are shown a handful of the app's pages: for each, its URL, its interactive elements, and its visible text. That is ALL you know about this app.

THE ONE HARD RULE: never propose a flow the pages do not show evidence of. Every proposal's evidence field must quote one short run of text that appears verbatim on the pages shown — an element's name, a heading, a sentence — proving the flow exists. If no page shows a password-reset link, there is no password-reset test to propose, however common that flow is in other apps: an invented test fails forever against a feature the app never had. Proposals whose evidence is not on the pages are discarded. Fewer honest proposals beat a padded list.

WHAT TO PROPOSE: the user flows that matter most — signing up, logging in, paying or checking out, searching, and creating, editing or deleting whatever this app's core object is. Not cosmetics, not "the page loads". Order them most important first. criticality is "critical" for flows where a defect loses money or locks people out — checkout, payment, signup, login — and "normal" for everything else.

HOW TO WRITE EACH SENTENCE: one sentence, the way a careful person would check it — name the page, the action, and what they should SEE afterwards. "Checkout works" cannot fail usefully. Any flow that creates data (an account, a post, an order) MUST write ${PLACEHOLDER_LIST} instead of invented values: the runner substitutes a traceable, deletable identity for them, and an invented email is a row in somebody's database that nobody can find afterwards.`;

/** Same plain fetch as think() in test.mjs — no SDK, no dependency. tool_choice is forced: the
 * only useful answer is a proposal list, and prose here would be a runner error, not a survey. */
async function propose(pagesBlock, max, apiKey, model) {
  const res = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: { "content-type": "application/json", "x-api-key": apiKey, "anthropic-version": "2023-06-01" },
    body: JSON.stringify({
      model,
      max_tokens: 4000,
      system: SYSTEM,
      tools: [PROPOSE],
      tool_choice: { type: "tool", name: "propose" },
      messages: [{ role: "user", content: `Propose at most ${max} tests for this app. Fewer is correct if the pages show fewer real flows.\n\n${pagesBlock}` }],
    }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`the model call failed (${res.status}). ${body.slice(0, 200)}`);
  }
  const json = await res.json();
  if (json.stop_reason === "refusal") throw new Error("the model declined to survey this app");
  const call = (json.content || []).find((b) => b.type === "tool_use" && b.name === "propose");
  if (!call) throw new Error("the model answered without a proposal list");
  return Array.isArray(call.input?.tests) ? call.input.tests : [];
}

// ---- vetting: the prompt asks, this enforces --------------------------------------------------

/** Whitespace- and case-blind, because a model retypes what it read rather than copying bytes out
 * of the page. perceive() hands it names exactly as the app capitalises them — `link "Sign in"` —
 * and a quote of "sign in" is the same claim about the same control. A byte-exact check would drop
 * that genuine flow over one capital letter: the false positive that teaches people to ignore the
 * real drops. */
const norm = (s) => String(s).toLowerCase().replace(/\s+/g, " ").trim();

/** Everything the model was shown, flattened for the evidence check: exactly the perceived pages,
 * nothing else, so "it is in the corpus" and "the model could have read it" are the same claim. */
export function corpusOf(pages) {
  return norm(
    pages
      .map((p) => [p.url, p.title, ...p.elements.map((e) => `${e.name} ${e.value || ""}`), p.text].join("\n"))
      .join("\n"),
  );
}

/**
 * An invented literal email in a sentence is exactly the untraceable row lib/safety.mjs exists to
 * prevent: the file is committed, the agent later signs "jane@testmail.com" up for real, and
 * nobody can grep the users table for it. The prompt asks for {{email}}; this makes it so even
 * when the model writes a literal anyway. Only emails — they are the one invented value with a
 * recognisable shape.
 */
export function depersonalize(sentence) {
  return String(sentence).replace(/\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b/g, "{{email}}");
}

/**
 * A sentence that MAKES AN ACCOUNT. Written out rather than a loose "sign" so that "sign in" — the
 * flow that creates nothing — is not swept up and told to register a new identity.
 */
const MAKES_ACCOUNT = /\b(?:sign(?:s|ing)?[ -]?up|signup|registers?|registering|registration|creat(?:e|es|ing)\s+(?:an?\s+)?(?:new\s+)?account)\b/i;

/**
 * A sentence that WRITES A ROW somebody has to find again. Deliberately not "add": almost every
 * "add" a survey proposes is "add to cart", whose record dies with the session, and hanging a
 * naming clause off every cart test is how these files stop reading like a careful person wrote
 * them. The cost of that choice is stated plainly: "add a todo" is missed, and a model that says
 * "create a todo" — which is what they overwhelmingly say — is caught.
 */
const MAKES_ROW = /\b(?:creat(?:e|es|ing)|posts?|posting|publish(?:es|ing)?|submits?|submitting|uploads?|uploading|writes?|writing|books?|booking|places? an order|orders?|checks? out|checkout|subscrib(?:e|es|ing)|leaves? a (?:review|comment))\b/i;

const HAS_PLACEHOLDER = /\{\{\s*[A-Za-z][A-Za-z0-9_]*\s*\}\}/;

/**
 * The other half of what safety.mjs is for, applied to sentences nobody proofread.
 *
 * The prompt asks for the placeholders; depersonalize catches a literal email because an email has
 * a shape. Neither covers the common miss: "Sign up for a new account from the pricing page and
 * check the dashboard appears" names no value at all, so the agent invents one — and an invented
 * signup is exactly the row lib/safety.mjs exists to keep out of somebody's staging database,
 * committed to their repo and re-created on every run with a different untraceable address. The
 * generated password matters for the same reason substitute() generates one: an invented "test123"
 * dies on the complexity rule almost every signup form has, and that lands as a FAILED test about
 * the app rather than about this sentence.
 *
 * Only when the sentence names NO placeholder. One already there is the model having understood
 * the instruction, and rewriting a sentence somebody could read is worse than the miss.
 */
export function traceable(sentence) {
  const s = String(sentence).trim();
  if (HAS_PLACEHOLDER.test(s)) return { sentence: s, pinned: "" };
  const clause = MAKES_ACCOUNT.test(s)
    ? { text: "signing up as {{email}} with the password {{password}}", pinned: "{{email}} {{password}}" }
    : MAKES_ROW.test(s)
      ? { text: "using {{runid}} in the name so the record it leaves behind can be found afterwards", pinned: "{{runid}}" }
      : null;
  if (!clause) return { sentence: s, pinned: "" };
  // Appended as a trailing clause, never spliced into the middle: the sentence was written by a
  // model that had read the app, and the only claim this function is entitled to make about it is
  // which values it should use.
  return { sentence: `${s.replace(/\.\s*$/, "")}, ${clause.text}.`, pinned: clause.pinned };
}

/**
 * The anti-fabrication line, enforced in code rather than asked for.
 *
 * The prompt already forbids inventing flows, but a prompt is a request, and the request most
 * likely to be ignored is this one: a password reset is in every app a model has ever read about,
 * so "A user can reset a forgotten password" is the proposal it can produce without having seen a
 * single reset link. That file then fails forever against a feature the app never had, and teaches
 * the reader to distrust the files beside it — which is why the fixture in test/suggest.test.mjs
 * deliberately has no such link and scripts exactly that proposal.
 *
 * So the evidence is checked against the pages, not trusted. A proposal whose quote appears on no
 * visited page is dropped and SAID, because a silent drop reads as the model having proposed less.
 */
/**
 * Does this evidence actually appear on the pages the crawl read?
 *
 * MEASURED, AND IT COST THE WHOLE FEATURE. A plain `corpus.includes(norm(evidence))` dropped
 * EVERY proposal on the first live run against a real model — four for four, "no flows to
 * propose", exit 0, zero files written. The reason is that we hand the model the perception
 * rendering, in which a control reads `button "Upgrade to Pro"`, so it quotes its evidence back
 * in the format we taught it, decoration and all. The corpus holds the bare name
 * (`upgrade to pro`), so the substring was never there and the anti-fabrication rule rejected
 * every true proposal it was built to protect.
 *
 * It survived 33 tests because they scripted the model, and a scripted model answers in whatever
 * shape the assertion wants. Only a real key found it.
 *
 * So the quote is checked in the forms a model actually produces: as given, and as each run of
 * text it put in quotation marks. That strips OUR OWN decoration, never the claim — the quoted
 * name still has to appear on a page that was really read, which is the property that matters.
 * Runs under three characters are refused, because "a" is on every page ever written and a
 * check that always passes is the same bug facing the other way.
 */
export function evidenceIsReal(evidence, corpus) {
  const quoted = String(evidence).match(/["'\u201c\u2018]([^"'\u201d\u2019]+)["'\u201d\u2019]/g) || [];
  const candidates = [
    evidence,
    ...quoted.map((q) => q.slice(1, -1)),
  ];
  return candidates.some((c) => {
    const n = norm(c);
    return n.length >= 3 && corpus.includes(n);
  });
}

export function vet(raw, corpus, max) {
  const kept = [];
  const dropped = [];
  // Proposals that PASSED the evidence check and lost to --max. Counted rather than forgotten for
  // the same reason a drop is announced: a proposal that vanishes without a word is indistinguish-
  // able from one the model never made, and the person tuning --max cannot tell there is more to
  // see.
  let trimmed = 0;
  for (const p of Array.isArray(raw) ? raw : []) {
    const title = String(p?.title ?? "").replace(/\s+/g, " ").trim();
    const sentence = String(p?.sentence ?? "").replace(/\s+/g, " ").trim();
    if (!title || !sentence) continue;
    const evidence = String(p?.evidence ?? "").trim();
    if (!evidence || !evidenceIsReal(evidence, corpus)) {
      dropped.push({ title, evidence });
      continue;
    }
    if (kept.length >= max) {
      trimmed++;
      continue;
    }
    const safe = traceable(depersonalize(sentence));
    kept.push({
      title,
      sentence: safe.sentence,
      // What was added to the sentence, so the caller can say it. A file that quietly grew a
      // clause nobody wrote reads, on the first `git diff`, as this command editing prose for
      // reasons of its own.
      pinned: safe.pinned,
      // Two values only. A model inventing "high" or "P0" here would put a word in the
      // frontmatter that nothing downstream defines; unknown means the safe reading, normal.
      criticality: p?.criticality === "critical" ? "critical" : "normal",
    });
  }
  return { kept, dropped, trimmed };
}

// ---- the files --------------------------------------------------------------------------------

/**
 * The EXACT single-test shape parseSuite() in suite.mjs reads back: frontmatter title +
 * criticality, then one sentence of prose. The round trip is pinned by a test that writes a file
 * here and parses it with the real parseSuite — the shape drifting apart from the reader is how a
 * generated suite silently becomes files named by their filenames with frontmatter in the sentence.
 */
export function fileBody({ title, sentence, criticality }) {
  return `---\ntitle: ${title}\ncriticality: ${criticality}\n---\n\n${sentence}\n`;
}

// ---- the command ------------------------------------------------------------------------------

export async function suggestCmd(opts = {}) {
  const { url, out = "tests", max: maxRaw, yes = false, log = console.log, env = process.env, loadBrowser = loadPlaywright } = opts;
  if (!url) {
    log(`
${C.b("npx smolanalytics suggest")} — walk the running app, propose the tests worth writing. No account.

  --url <url>   where the app runs (staging, a deploy preview, localhost)
  --out <dir>   where the .md files land (default tests/) — one proposal per file, existing files never overwritten
  --max <n>     at most this many proposals (default 6; a small app honestly yields fewer)
  --yes         install the browser without asking

  ${C.dim("npx smolanalytics suggest --url http://localhost:3000")}
  ${C.dim("Each file is a test `npx smolanalytics test --suite tests/` runs as-is. Delete the ones you disagree with.")}
`);
    return 2;
  }
  // Refused out loud, exactly like --retries in the bin: Number("six") is NaN, and any coercion
  // from there silently hands someone a different cap than the one they typed.
  if (maxRaw !== undefined && !/^[1-9]\d*$/.test(String(maxRaw))) {
    // A bare `--max` arrives here as "", and `got ""` names nothing the person typed — they typed
    // the flag and left the number off. Say that instead of quoting an empty string back at them.
    log(C.y(String(maxRaw).trim()
      ? `--max needs a whole number above zero, got ${JSON.stringify(String(maxRaw))}.`
      : "--max was given no number. Write --max 6, or leave the flag out for the default of 6."));
    return 2;
  }
  const max = maxRaw === undefined ? 6 : Number(maxRaw);

  // BEFORE the browser, unlike testCmd — which checks the key late because replaying a recording
  // needs no key at all. suggest has no keyless mode, and fetching 50MB of Chromium and THEN
  // failing on a missing env var is the wrong order to disappoint somebody in.
  const apiKey = env.ANTHROPIC_API_KEY;
  if (!apiKey) {
    log(`\n${C.y("The agent needs a Claude API key.")}`);
    log(C.dim("  export ANTHROPIC_API_KEY=sk-ant-…    then run this again"));
    log(C.dim("  suggest reads your app with the model to decide which flows matter, so there is no keyless mode."));
    return 2;
  }
  const model = env.SMOLANALYTICS_MODEL || "claude-opus-5";

  let browser = null;
  try {
    // The browser load is INSIDE the try, unlike testCmd's. That function's throw is caught by a
    // runOnce catch of its own; this one's only backstop is the CLI's last-resort handler, which
    // exits 1 — the code that means the application under test is broken. Nothing in a survey may
    // ever say that, so every failure from here down is caught and turned into 2.
    const { pw, problem } = await loadBrowser(log, yes);
    if (!pw) {
      log(C.y(`${problem || "The browser could not be started."} This is this runner, not your application.`));
      return 2;
    }
    // Inside the try for the same reason runOnce's launch is: "Host system is missing dependencies
    // to run browsers" is the most common way a CI browser run dies, and it is our problem (2).
    browser = await pw.chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

    log(`\nreading ${C.b(url)} ${C.dim(`(up to ${MAX_PAGES} same-origin pages, this app is only read — nothing is clicked or submitted)`)}`);
    const pages = await crawl(page, url, log);
    if (!pages.length) {
      log(C.y("\nnothing was readable at that URL, so there is nothing honest to propose."));
      return 2;
    }

    const block = pages.map((p, i) => `--- page ${i + 1} of ${pages.length} ---\n${render(p)}`).join("\n\n");
    log(C.dim(`asking the model for up to ${max} flows across ${pages.length} page${pages.length === 1 ? "" : "s"} (one call)…`));
    const raw = await propose(block, max, apiKey, model);
    const { kept, dropped, trimmed } = vet(raw, corpusOf(pages), max);

    for (const d of dropped) {
      log(C.y(`  dropped "${d.title}" — its evidence (${JSON.stringify(d.evidence)}) is on none of the pages visited, so the flow cannot be shown to exist.`));
    }
    if (trimmed) {
      log(C.dim(`  ${trimmed} more proposal${trimmed === 1 ? "" : "s"} survived the evidence check but ranked below --max ${max}, so nothing was written for ${trimmed === 1 ? "it" : "them"}. Raise --max to see ${trimmed === 1 ? "it" : "them"}.`));
    }
    for (const p of kept.filter((k) => k.pinned)) {
      // Never silent: the file on disk will not say what the model wrote, and finding that out
      // from a diff is how somebody concludes this command edits prose for reasons of its own.
      log(C.dim(`  added ${p.pinned} to "${p.title}" — it creates data, and an invented value is a row nobody can find afterwards.`));
    }
    if (!kept.length) {
      // Zero is an honest answer for a splash page or a login wall, and padding it with generic
      // flows would be the exact fabrication vet() exists to drop.
      log(`\n${C.y("no flows to propose")} ${C.dim("— the pages visited showed no user flow worth a test. Nothing was written.")}`);
      return 0;
    }

    mkdirSync(out, { recursive: true });
    let written = 0;
    let skipped = 0;
    for (const p of kept) {
      const file = path.join(out, `${slug(p.title)}.md`);
      // NEVER overwritten. A file already there is either the user's own test or an earlier run's
      // that they have since edited; a suggester that rewrites somebody's suite on re-run is a
      // suggester that ran once.
      if (existsSync(file)) {
        log(C.y(`  skipped ${file} — it already exists and suggest never overwrites.`));
        skipped++;
        continue;
      }
      writeFileSync(file, fileBody(p));
      log(`  ${C.g("wrote")} ${file} ${C.dim(`— ${p.criticality}`)}`);
      written++;
    }

    log(`\n${written} test${written === 1 ? "" : "s"} written to ${out}${skipped ? C.dim(` · ${skipped} skipped (already there)`) : ""}${dropped.length ? C.dim(` · ${dropped.length} dropped (no evidence)`) : ""}`);
    if (written) log(C.dim(`  run them: npx smolanalytics test --suite ${out} --url ${url}`));
    return 0;
  } catch (e) {
    log(C.y(`\nthe survey could not complete: ${e && e.message ? e.message : e}`));
    log(C.dim("  This is this runner, not your application. Nothing was written."));
    return 2;
  } finally {
    await browser?.close().catch(() => {});
  }
}
