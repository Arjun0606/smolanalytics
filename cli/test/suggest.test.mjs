// `npx smolanalytics suggest` — THE ANSWER TO "WHAT DO I EVEN WRITE", AND THE LINE IT MUST NOT CROSS.
//
// The command walks a running app with a real browser and writes the flows worth testing into
// tests/*.md. Everything valuable about it is downstream of one property: a proposal is only ever
// about something the crawl ACTUALLY SAW. A model asked "what should an app like this test?"
// answers from every app it has ever read about — password resets, wishlists, coupon codes — and
// one such file is worse than an empty folder, because it fails forever against a feature that
// never existed and teaches the reader to distrust the files beside it.
//
// So the rules under test here, each with the thing it stops:
//
//   evidence must be on a visited page   an invented flow never becomes a file, and the drop is
//                                        SAID out loud — a silent drop reads as the model having
//                                        proposed less.
//   the walk is read-only                /logout is a plain <a href> in most apps. Following it
//                                        would end the session it was surveying.
//   bounded pages and depth              forty product URLs are one flow, and spending the budget
//                                        on them means never reaching /pricing.
//   the placeholders from safety.mjs     a sentence that creates data and names a literal value
//                                        is a row in somebody's database nobody can find again.
//   existing files are never overwritten a suggester that rewrites somebody's suite on re-run is
//                                        a suggester that ran once.
//   0 or 2, never 1                      1 means "the application is broken". A survey cannot
//                                        learn that, and must never claim it.
//
// The browser and the HTTP servers are real; only the model is scripted, the same shape as
// flake.test.mjs — for the library through globalThis.fetch, and for the CLI through a preload
// module handed to the child with NODE_OPTIONS=--import, so the wiring in bin/ is exercised too.

import { test, describe, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { existsSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { suggestCmd, crawl, vet, corpusOf, traceable, depersonalize, fileBody, evidenceIsReal } from "../lib/suggest.mjs";
import { discover, parseSuite } from "../lib/suite.mjs";

let chromium = null;
try {
  ({ chromium } = await import("playwright"));
} catch {
  /* the CLI fetches the browser on first use; these skip with a reason rather than failing */
}
const noBrowser = { skip: chromium ? false : "playwright not installed (npx smolanalytics suggest installs it on first use)" };

// ---- the app being surveyed ---------------------------------------------------------------------
//
// Small on purpose, and DELIBERATELY MISSING the flows a model most likes to invent: there is no
// "Forgot your password?" link, no wishlist and no coupon field anywhere in it. A file proposing
// one of those could only have come from the model's training data, which is exactly what this
// command exists to keep out of somebody's tests/ folder.

const html = (title, body) => `<!doctype html><html><head><title>${title}</title></head><body>${body}</body></html>`;

/** A second origin, so "same-origin only" is a claim with something to be false against. */
let elsewhereHits = [];
const elsewhere = createServer((req, res) => {
  elsewhereHits.push(req.url);
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(html("Partner", "<h1>Partner portal</h1><p>Redeem a coupon code for your wishlist.</p>"));
});
await new Promise((r) => elsewhere.listen(0, "127.0.0.1", r));
const elsewhereUrl = `http://127.0.0.1:${elsewhere.address().port}/`;
after(() => new Promise((r) => { elsewhere.closeAllConnections?.(); elsewhere.close(() => r()); }));

/** Every path the browser asked for, so "never followed" is checked at the server, not in the log. */
let hits = [];

const app = createServer((req, res) => {
  hits.push(req.url);
  const p = req.url.split("?")[0];
  if (p === "/away") {
    res.writeHead(302, { location: elsewhereUrl });
    res.end();
    return;
  }
  // A dead link in somebody's nav. The socket is dropped rather than answered, which is what
  // Playwright's goto() throws on — the same shape as a route that 500s before a byte is written.
  if (p === "/broken") {
    res.destroy();
    return;
  }
  const pages = {
    "/": html("Widget Store", `<h1>Widget Store</h1><nav>
      <a href="/pricing">Pricing</a> <a href="/search">Search</a> <a href="/login">Sign in</a>
      <a href="/logout">Log out</a> <a href="/guide.pdf">Guide</a>
      <a href="${elsewhereUrl}docs">Docs</a> <a href="/away">Partner</a> <a href="/broken">Status</a></nav>`),
    "/pricing": html("Pricing", '<h1>Pricing</h1><p>Pro plan is $29 / month.</p><button>Buy Pro</button> <a href="/checkout">Checkout</a>'),
    "/search": html("Search", '<h1>Search</h1><label>Search products <input type="search"></label><button>Search</button>'),
    "/login": html("Sign in", '<h1>Sign in</h1><label>Email <input></label><label>Password <input type="password"></label><button>Sign in</button>'),
    "/checkout": html("Checkout", "<h1>Checkout</h1><button>Place order</button>"),
    // Reachable, and never to be reached: following it in a real app ends the session mid-survey.
    "/logout": html("Bye", "<h1>Logged out</h1>"),
  };
  if (!pages[p]) {
    res.writeHead(404, { "content-type": "text/html; charset=utf-8" });
    res.end(html("Not found", "<h1>Not found</h1>"));
    return;
  }
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(pages[p]);
});
await new Promise((r) => app.listen(0, "127.0.0.1", r));
const url = `http://127.0.0.1:${app.address().port}/`;
after(() => new Promise((r) => { app.closeAllConnections?.(); app.close(() => r()); }));

const scratch = () => mkdtempSync(path.join(tmpdir(), "smolanalytics-suggest-"));
const outDir = () => path.join(scratch(), "tests");
const filesIn = (dir) => (existsSync(dir) ? readdirSync(dir).sort() : []);
const readAll = (dir) => Object.fromEntries(filesIn(dir).map((f) => [f, readFileSync(path.join(dir, f), "utf8")]));

/**
 * One run with a scripted model. The browser, the crawl and the file writing are all real; only
 * the one model call is answered from `tests`, and every request it makes is captured so the
 * PROMPT can be asserted on rather than read.
 */
async function suggest({ tests = [], at = url, loadBrowser, ...opts } = {}) {
  const realFetch = globalThis.fetch;
  const sent = [];
  globalThis.fetch = async (target, init = {}) => {
    assert.match(String(target), /api\.anthropic\.com/, "a survey may call nothing but the model");
    sent.push(JSON.parse(init.body));
    return {
      ok: true,
      status: 200,
      text: async () => "",
      json: async () => ({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t1", name: "propose", input: { tests } }] }),
    };
  };
  const lines = [];
  try {
    const code = await suggestCmd({
      url: at,
      env: { ANTHROPIC_API_KEY: "sk-ant-test" },
      log: (...a) => lines.push(a.join(" ")),
      loadBrowser: loadBrowser || (async () => ({ pw: { chromium } })),
      ...opts,
    });
    return { code, sent, out: lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "") };
  } finally {
    globalThis.fetch = realFetch;
  }
}

const REAL = [
  { title: "A shopper can buy Pro", sentence: "From the pricing page, buy the Pro plan and see the order confirmed.", criticality: "critical", evidence: "Buy Pro" },
  { title: "A shopper can search", sentence: "Search for a widget and see matching products.", criticality: "normal", evidence: "Search products" },
];

// ---- the walk -----------------------------------------------------------------------------------

describe("the walk: what it reads, and what it refuses to touch", () => {
  test("same-origin pages only, and never a link that could change the app", noBrowser, async () => {
    hits = [];
    const out = outDir();
    const r = await suggest({ tests: REAL, out });

    assert.equal(r.code, 0, r.out);
    // Deleting MUTATING from the link filter puts "/logout" here — measured, and the reason the
    // filter exists: a survey that logs itself out reads the signed-out app and proposes tests for
    // a product nobody uses.
    assert.ok(!hits.includes("/logout"), `the crawl followed a link that ends the session: ${JSON.stringify(hits)}`);
    // A browser pointed at a .pdf perceives zero elements and burns one of eight page slots.
    assert.ok(!hits.includes("/guide.pdf"), `the crawl spent a page slot on a file: ${JSON.stringify(hits)}`);
    for (const p of ["/", "/pricing", "/search", "/login", "/checkout"]) {
      assert.ok(hits.includes(p), `${p} was never read, so nothing about it could be proposed: ${JSON.stringify(hits)}`);
    }
    assert.match(r.out, /read http:\/\/127\.0\.0\.1:\d+\/pricing/, `every page read is named, or nobody can audit the evidence:\n${r.out}`);
  });

  test("a link to another origin is not followed, and a redirect to one contributes no evidence", noBrowser, async () => {
    hits = [];
    elsewhereHits = [];
    const out = outDir();
    const r = await suggest({ tests: REAL, out });

    // NOT EVEN REQUESTED. Discarding another origin's page after loading it would still mean this
    // command quietly fetching somebody's partner site, from the customer's network, on a run they
    // asked to be a survey of their own app. The only request the other origin may ever see is the
    // one their own /away redirect forced.
    assert.ok(!elsewhereHits.includes("/docs"), `a link off this app was followed: ${JSON.stringify(elsewhereHits)}`);
    const block = r.sent[0].messages[0].content;
    // /away 302s to a site whose text says "coupon" and "wishlist" — the exact material a model
    // fabricates from. Somebody else's app must never become evidence about this one.
    assert.ok(!/coupon|wishlist|Partner portal/i.test(block), `another origin's content reached the model:\n${block}`);
    assert.ok(!block.includes(elsewhereUrl), `another origin's URL reached the model:\n${block}`);
    assert.match(r.out, /redirected off this app/, `the skip must be said, not silent:\n${r.out}`);
  });

  test("the page budget holds: twenty linked pages do not become twenty reads", noBrowser, async () => {
    const seen = [];
    const wide = createServer((req, res) => {
      seen.push(req.url);
      const links = Array.from({ length: 20 }, (_, i) => `<a href="/p${i + 1}">Page ${i + 1}</a>`).join(" ");
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(html("Wide", `<h1>Wide</h1>${req.url === "/" ? links : "<p>A product.</p>"}`));
    });
    await new Promise((r) => wide.listen(0, "127.0.0.1", r));
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const pages = await crawl(page, `http://127.0.0.1:${wide.address().port}/`);
      // Eight, and the bound is the point: with it removed a run spent its whole budget on one
      // template at forty URLs and never reached the page the money test comes from.
      assert.equal(pages.length, 8, `the crawl read ${pages.length} pages: ${pages.map((p) => p.url).join(", ")}`);
      assert.equal(seen.length, 8, `the browser requested ${seen.length} URLs: ${JSON.stringify(seen)}`);
    } finally {
      await browser.close();
      await new Promise((r) => { wide.closeAllConnections?.(); wide.close(() => r()); });
    }
  });

  test("the depth budget holds: a fourth level is never opened", noBrowser, async () => {
    const seen = [];
    const deep = createServer((req, res) => {
      seen.push(req.url);
      const next = { "/": "/a", "/a": "/b", "/b": "/c", "/c": "/d" }[req.url] || "/";
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(html("Deep", `<h1>Level ${req.url}</h1><a href="${next}">Next</a>`));
    });
    await new Promise((r) => deep.listen(0, "127.0.0.1", r));
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const pages = await crawl(page, `http://127.0.0.1:${deep.address().port}/`);
      assert.deepEqual(seen, ["/", "/a", "/b"], `depth 2 from the start URL is where distinct sections live; deeper is detail pages of sections already seen: ${JSON.stringify(seen)}`);
      assert.equal(pages.length, 3);
    } finally {
      await browser.close();
      await new Promise((r) => { deep.closeAllConnections?.(); deep.close(() => r()); });
    }
  });

  test("a dead link mid-walk is noted and walked past, and the pages beside it still get read", noBrowser, async () => {
    hits = [];
    const out = outDir();
    const r = await suggest({ tests: REAL, out });
    assert.equal(r.code, 0, r.out);
    assert.ok(hits.includes("/broken"), "the dead link was never even tried");
    assert.match(r.out, /could not open .*\/broken .* — skipped/, `a dead link in their nav must be named, not swallowed:\n${r.out}`);
    // The one thing that must NOT happen: a dead link somewhere in the navigation ending the
    // survey. The other pages still carry the evidence, and /checkout is two links deep.
    //
    // READ, not merely requested. Without the settle in open(), the browser still asks the server
    // for /checkout — the hit is recorded — and the navigation is then interrupted by the dead
    // link's error document committing. Asserting on the request would have called that a pass.
    assert.match(r.out, /read http:\/\/127\.0\.0\.1:\d+\/checkout/, `one dead link cost the walk the page behind it:\n${r.out}`);
    assert.match(r.out, /across 5 pages/, `the model was shown fewer pages than the walk could reach:\n${r.out}`);
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md", "a-shopper-can-search.md"]);
  });

  test("an unopenable start URL is the whole run failing, as exit 2", noBrowser, async () => {
    const out = outDir();
    // Nothing is listening on this port, so the start page cannot be opened. There is no evidence
    // and nothing honest to propose — and it is OUR problem to report (2), never the app's (1).
    const r = await suggest({ tests: REAL, out, at: "http://127.0.0.1:1/" });
    assert.equal(r.code, 2, r.out);
    assert.match(r.out, /could not complete/i);
    assert.match(r.out, /this runner, not your application/i, `a survey that cannot start must not read as a bug report:\n${r.out}`);
    assert.deepEqual(filesIn(out), [], "a run that learned nothing wrote a file");
  });
});

// ---- the prompt ---------------------------------------------------------------------------------

describe("what the model is asked, and what it is shown", () => {
  test("one call, and the hard rule against inventing flows is in it", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({ tests: REAL, out, max: "2" });

    assert.equal(r.sent.length, 1, "a survey has no step-to-step dependency, so it costs exactly one call");
    const { system, messages, tools, tool_choice } = r.sent[0];
    // The prompt is a request, not an enforcement — vet() is the enforcement. But a prompt that
    // never asks produces proposals vet() has to throw away, which is a survey the customer paid
    // for and got nothing from.
    assert.match(system, /never propose a flow the pages do not show evidence of/i, system);
    assert.match(system, /verbatim/i, "the model must be told its evidence is checked against the pages");
    assert.match(system, /password-reset|password reset/i, "the concrete example is what makes the rule land");
    // The placeholders are safety.mjs's, quoted from it rather than retyped here.
    for (const ph of ["{{email}}", "{{password}}", "{{runid}}"]) {
      assert.ok(system.includes(ph), `${ph} is not offered to the model, so it will invent a value: ${system}`);
    }
    assert.match(system, /criticality is "critical"/, "the money/auth ranking has to be defined, not guessed");
    assert.equal(tool_choice.type, "tool", "prose instead of a proposal list is a runner error, not a survey");
    assert.equal(tools[0].name, "propose");
    assert.deepEqual(tools[0].input_schema.properties.tests.items.required.sort(), ["criticality", "evidence", "sentence", "title"]);

    // What it is SHOWN is exactly the pages that were walked. "It is in the corpus" and "the model
    // could have read it" must be the same claim, or the evidence check is checking nothing.
    const block = messages[0].content;
    assert.match(block, /at most 2 tests/, "the cap has to reach the model, or it proposes six and five are trimmed");
    for (const p of ["/pricing", "/search", "/login", "/checkout"]) assert.ok(block.includes(p), `${p} was walked but not shown: ${block}`);
    assert.ok(!block.includes("/logout"), `a page the crawl refused to open was shown anyway: ${block}`);
  });
});

// ---- the anti-fabrication line ------------------------------------------------------------------

describe("the anti-fabrication line", () => {
  test("a proposal for a control the app does not have never becomes a file", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({
      out,
      tests: [
        REAL[0],
        // The measured one: asked for six tests against a five-page fixture with no such link, a
        // run proposed exactly this, quoting evidence from its training data rather than the app.
        { title: "A user can reset a forgotten password", sentence: "Click Forgot your password and follow the reset link.", criticality: "normal", evidence: "Forgot your password?" },
        REAL[1],
      ],
    });

    assert.equal(r.code, 0, r.out);
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md", "a-shopper-can-search.md"], "the invented flow became a file");
    // A silent drop reads as the model having proposed less; the person has to be able to see what
    // was thrown away and why, or the next unexplained absence looks like a bug in this command.
    assert.match(r.out, /dropped "A user can reset a forgotten password"/, r.out);
    assert.match(r.out, /"Forgot your password\?"/, "the drop must quote the evidence that was not there");
    assert.match(r.out, /1 dropped \(no evidence\)/, r.out);

    // The whole folder, against the controls this app deliberately lacks. A file naming one of
    // these could only have come from somewhere other than the pages that were read.
    const all = Object.values(readAll(out)).join("\n").toLowerCase();
    for (const absent of ["forgot", "reset link", "password reset", "wishlist", "coupon", "gift card"]) {
      assert.ok(!all.includes(absent), `a written test names ${JSON.stringify(absent)}, which is on no page of this app:\n${all}`);
    }
  });

  test("evidence is matched the way a model retypes it, not byte for byte", () => {
    // Against the real API the model quoted "sign in" for a link perceived as "Sign in". A
    // byte-exact check would have dropped a genuine flow — the false positive that teaches people
    // to ignore the real drops.
    const corpus = corpusOf([{ url: "/login", title: "Sign in", elements: [{ name: "Sign in", value: "" }], text: "Sign in to Widget Store" }]);
    const { kept, dropped } = vet([{ title: "T", sentence: "Sign in and see the store.", criticality: "normal", evidence: "  sign   IN " }], corpus, 6);
    assert.equal(dropped.length, 0, "case and whitespace are not evidence of fabrication");
    assert.equal(kept.length, 1);
  });

  test("a proposal with no evidence at all is dropped, not trusted", () => {
    const corpus = corpusOf([{ url: "/", title: "Home", elements: [], text: "Widget Store" }]);
    const { kept, dropped } = vet([{ title: "T", sentence: "Do the thing.", criticality: "critical", evidence: "" }], corpus, 6);
    assert.equal(kept.length, 0, "an empty evidence field is an unproven claim, and critical does not make it truer");
    assert.equal(dropped.length, 1);
  });

  test("criticality is one of two words, whatever the model says", () => {
    const corpus = corpusOf([{ url: "/", title: "Home", elements: [{ name: "Buy Pro" }], text: "" }]);
    const { kept } = vet([
      { title: "A", sentence: "Buy Pro.", criticality: "critical", evidence: "Buy Pro" },
      // "P0" in the frontmatter would be a word nothing downstream defines. Unknown means the safe
      // reading — normal — never a made-up severity in somebody's suite.
      { title: "B", sentence: "Buy Pro again.", criticality: "P0", evidence: "Buy Pro" },
    ], corpus, 6);
    assert.deepEqual(kept.map((k) => k.criticality), ["critical", "normal"]);
  });
});

// ---- test data safety ---------------------------------------------------------------------------

describe("a sentence that creates data names no invented value", () => {
  test("a literal email becomes {{email}}", () => {
    assert.equal(
      depersonalize("Sign up as jane.doe+1@testmail.co.uk and see the dashboard."),
      "Sign up as {{email}} and see the dashboard.",
    );
  });

  test("a signup that names nothing gets the identity clause, and it is not spliced into the middle", () => {
    // The common miss: "Sign up from the pricing page and check the dashboard appears" names no
    // value at all, so the agent invents one — a row in somebody's staging database that nobody
    // can grep for, re-created on every run with a different untraceable address.
    const t = traceable("Sign up from the pricing page and check the dashboard appears");
    assert.match(t.sentence, /\{\{email\}\}/);
    assert.match(t.sentence, /\{\{password\}\}/, "an invented password dies on the complexity rule and lands as a FAIL about the app");
    assert.ok(t.sentence.startsWith("Sign up from the pricing page and check the dashboard appears,"), t.sentence);
    assert.equal(t.pinned, "{{email}} {{password}}", "what was added has to be reportable, or the file quietly disagrees with the model");
  });

  test("a sentence that writes a row gets a findable name", () => {
    const t = traceable("Create a project and see it in the sidebar.");
    assert.match(t.sentence, /\{\{runid\}\}/);
    assert.equal(t.pinned, "{{runid}}");
  });

  test("signing IN is not signing UP, and a cart is not a row", () => {
    // Written out rather than a loose "sign" so the flow that creates nothing is not told to
    // register a new identity — a login test with a fresh random email fails forever.
    assert.equal(traceable("Sign in with a known account and see the dashboard.").pinned, "");
    // Deliberately not "add": almost every "add" a survey proposes is "add to cart", whose record
    // dies with the session, and hanging a naming clause off every cart test is how these files
    // stop reading like a careful person wrote them.
    assert.equal(traceable("Add a widget to the cart and see the cart count go to 1.").pinned, "");
  });

  test("a sentence that already uses a placeholder is left exactly as written", () => {
    const s = "Sign up as {{email}} and see the dashboard.";
    assert.equal(traceable(s).sentence, s, "rewriting a sentence somebody could read is worse than the miss");
    assert.equal(traceable(s).pinned, "");
  });

  test("the clause reaches the file, and the run says so", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({
      out,
      tests: [{ title: "A visitor can sign up", sentence: "From the sign in page, sign up for an account and see the store.", criticality: "critical", evidence: "Sign in" }],
    });
    const body = readAll(out)["a-visitor-can-sign-up.md"];
    assert.match(body, /\{\{email\}\}/, body);
    assert.match(body, /\{\{password\}\}/, body);
    // Never silent: the file on disk does not say what the model wrote, and finding that out from
    // a `git diff` is how somebody concludes this command edits prose for reasons of its own.
    assert.match(r.out, /added \{\{email\}\} \{\{password\}\} to "A visitor can sign up"/, r.out);
  });
});

// ---- the files ----------------------------------------------------------------------------------

describe("the files are the exact shape the suite runner reads back", () => {
  test("a written file round-trips through the real parseSuite", noBrowser, async () => {
    const out = outDir();
    await suggest({ tests: REAL, out });
    const file = path.join(out, "a-shopper-can-search.md");
    const tests = parseSuite(file, readFileSync(file, "utf8"));

    assert.equal(tests.length, 1, "one proposal is one test, never a document of them");
    // The name the person reads on the pull request is the title that was written down, not the
    // filename with its dashes taken out.
    assert.equal(tests[0].name, "A shopper can search");
    // Verbatim: this proposal creates nothing, so nothing may be added to it.
    assert.equal(tests[0].test, REAL[1].sentence);
    // The shape drifting apart from the reader is how a generated suite silently becomes files
    // whose frontmatter is handed to the agent as part of what to look for on the page.
    assert.ok(!tests[0].test.includes("criticality"), tests[0].test);
    assert.ok(!tests[0].test.includes("---"), tests[0].test);
  });

  test("the model's own words are never spliced, only followed", noBrowser, async () => {
    const out = outDir();
    await suggest({ tests: REAL, out });
    // "see the order confirmed" trips the writes-a-row rule, so this file DOES gain a clause. The
    // only claim this command is entitled to make about a sentence written by something that had
    // read the app is which values it should use — so the addition goes on the end, and the
    // sentence up to it is byte-for-byte what the model wrote.
    const [t] = parseSuite("x.md", readAll(out)["a-shopper-can-buy-pro.md"]);
    assert.ok(t.test.startsWith(REAL[0].sentence.replace(/\.$/, ", ")), t.test);
    assert.match(t.test, /\{\{runid\}\}/);
  });

  test("fileBody carries both keys, in the order the frontmatter parser expects", () => {
    const body = fileBody({ title: "A shopper can pay", sentence: "Pay and see the receipt.", criticality: "critical" });
    assert.equal(body, "---\ntitle: A shopper can pay\ncriticality: critical\n---\n\nPay and see the receipt.\n");
  });

  test("an existing file is never overwritten, and the skip is printed", noBrowser, async () => {
    const out = outDir();
    await suggest({ tests: [REAL[0]], out });
    const mine = path.join(out, "a-shopper-can-buy-pro.md");
    // Stand in for the file somebody edited after the first run: their sentence, their wording.
    writeFileSync(mine, "---\ntitle: A shopper can buy Pro\ncriticality: critical\n---\n\nMY OWN SENTENCE, edited by hand.\n");

    const r = await suggest({ tests: REAL, out });
    assert.equal(r.code, 0, r.out);
    assert.match(readFileSync(mine, "utf8"), /MY OWN SENTENCE/, "a re-run rewrote somebody's edited test");
    assert.match(r.out, /skipped .*a-shopper-can-buy-pro\.md — it already exists/, r.out);
    assert.match(r.out, /1 skipped \(already there\)/, r.out);
    // The rest of the folder is still offered: one file the person owns must not cost them the
    // other proposals.
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md", "a-shopper-can-search.md"]);
  });

  test("fewer real flows means fewer files, never padding", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({ out, max: "6", tests: [REAL[0]] });
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md"], "a --max of 6 invented company for the one honest proposal");
    assert.match(r.out, /1 test written/, r.out);
  });

  test("--max is a cap that is respected in rank order, and the trim is said out loud", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({ out, max: "1", tests: REAL });
    // Most important first: the model ranks, and the cap takes from the bottom.
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md"]);
    assert.match(r.out, /--max 1/, `a proposal that passed the evidence check and was trimmed anyway must not vanish in silence:\n${r.out}`);
  });

  test("no flows is an honest answer, and it writes nothing at all", noBrowser, async () => {
    const out = outDir();
    const r = await suggest({ out, tests: [] });
    assert.equal(r.code, 0, "an app with no flows worth testing is not a failure of anything");
    assert.match(r.out, /no flows to propose/, r.out);
    // Not even the directory: "nothing written to your repo" has to be true of the empty case too.
    assert.equal(existsSync(out), false, "an empty survey created a folder in somebody's project");
  });
});

// ---- statuses and exit codes --------------------------------------------------------------------

describe("what this command is allowed to say", () => {
  test("no API key is exit 2, with the same sentence testCmd uses, and the browser is never fetched", async () => {
    let asked = false;
    const out = outDir();
    const code = await suggestCmd({
      url,
      out,
      env: {},
      log: () => {},
      loadBrowser: async () => {
        asked = true;
        return { pw: null, problem: "should never get here" };
      },
    });
    assert.equal(code, 2, "a missing key is this runner not running, never a verdict about the app");
    // Fetching 50MB of Chromium and THEN failing on an env var is the wrong order to disappoint
    // somebody in.
    assert.equal(asked, false, "the browser was fetched before the key was checked");
  });

  test("the key message names the variable and the fix", async () => {
    const lines = [];
    await suggestCmd({ url, out: outDir(), env: {}, log: (...a) => lines.push(a.join(" ")), loadBrowser: async () => ({ pw: null }) });
    const text = lines.join("\n").replace(/\x1b\[[0-9;]*m/g, "");
    // Word for word what `test` says when the key is missing. Two commands in one CLI describing
    // the same missing environment variable two different ways is how somebody concludes the
    // second one wants a different key.
    assert.match(text, /The agent needs a Claude API key\./);
    assert.match(text, /export ANTHROPIC_API_KEY=sk-ant-/);
    // And the sentence that differs, because the reason differs: `test` can replay a recording
    // without a key, and a survey has no such mode to point at.
    assert.match(text, /no keyless mode/);
  });

  test("a browser that cannot start is exit 2 and blames nobody's application", async () => {
    const lines = [];
    const code = await suggestCmd({
      url,
      out: outDir(),
      env: { ANTHROPIC_API_KEY: "sk-ant-test" },
      log: (...a) => lines.push(a.join(" ")),
      loadBrowser: async () => ({ pw: null, problem: "Playwright installed but Chromium did not." }),
    });
    assert.equal(code, 2);
    assert.match(lines.join("\n"), /this runner, not your application/i);
  });

  test("a model outage is exit 2, and nothing is written", noBrowser, async () => {
    const realFetch = globalThis.fetch;
    globalThis.fetch = async () => ({ ok: false, status: 529, text: async () => "overloaded", json: async () => ({}) });
    const out = outDir();
    const lines = [];
    try {
      const code = await suggestCmd({
        url,
        out,
        env: { ANTHROPIC_API_KEY: "sk-ant-test" },
        log: (...a) => lines.push(a.join(" ")),
        loadBrowser: async () => ({ pw: { chromium } }),
      });
      assert.equal(code, 2, "our model call failing is never the customer's app failing");
      assert.match(lines.join("\n"), /529/, "the status the model returned is the one useful fact here");
      assert.deepEqual(filesIn(out), []);
    } finally {
      globalThis.fetch = realFetch;
    }
  });

  test("--max is refused rather than silently coerced", async () => {
    for (const bad of ["six", "0", "-2", "2.5"]) {
      const lines = [];
      const code = await suggestCmd({ url, out: outDir(), max: bad, env: { ANTHROPIC_API_KEY: "sk-ant-test" }, log: (...a) => lines.push(a.join(" ")), loadBrowser: async () => ({ pw: null }) });
      assert.equal(code, 2, `--max ${bad} was accepted`);
      assert.match(lines.join("\n"), /--max needs a whole number above zero/);
    }
    // A bare `--max` arrives as "", and quoting an empty string back at somebody names nothing
    // they typed.
    const lines = [];
    await suggestCmd({ url, out: outDir(), max: "", env: { ANTHROPIC_API_KEY: "sk-ant-test" }, log: (...a) => lines.push(a.join(" ")), loadBrowser: async () => ({ pw: null }) });
    assert.match(lines.join("\n"), /--max was given no number/);
  });

  test("a missing --url prints the usage and exits 2, never 1", async () => {
    const lines = [];
    const code = await suggestCmd({ log: (...a) => lines.push(a.join(" ")), env: {} });
    // 1 is the code that means the application under test is broken. A survey that never ran has
    // learned nothing about any application.
    assert.equal(code, 2);
    assert.match(lines.join("\n"), /--url <url>/);
  });
});

// ---- the command line ---------------------------------------------------------------------------

describe("at the command line", () => {
  const bin = fileURLToPath(new URL("../bin/smolanalytics.mjs", import.meta.url));

  test("suggest is offered in the help, with its flags", () => {
    const r = spawnSync(process.execPath, [bin, "help"], { encoding: "utf8", timeout: 30_000 });
    const text = r.stdout.replace(/\x1b\[[0-9;]*m/g, "");
    assert.match(text, /npx smolanalytics suggest/);
    assert.match(text, /--out\s+<dir>/);
    assert.match(text, /--max\s+<n>/);
  });

  test("--max with no number is refused by the CLI too, both flag shapes", () => {
    for (const argv of [["suggest", "--url", url, "--max", "six"], ["suggest", "--url", url, "--max=six"]]) {
      const r = spawnSync(process.execPath, [bin, ...argv], { encoding: "utf8", timeout: 30_000, env: { ...process.env, ANTHROPIC_API_KEY: "sk-ant-test" } });
      assert.equal(r.status, 2, `${argv.join(" ")} must exit 2 — our refusal, never 1, which blames the app`);
      assert.match(r.stdout, /--max needs a whole number/);
    }
  });

  test("the whole command, end to end: a real browser, a scripted model, files on disk", noBrowser, async () => {
    const dir = scratch();
    const out = path.join(dir, "tests");
    const seen = path.join(dir, "request.json");
    // The model is intercepted in the CHILD, so bin/ does the flag parsing, lib/ does the crawl
    // and the exit code is the process's own — the three things an in-process call cannot prove.
    const preload = path.join(dir, "scripted-model.mjs");
    writeFileSync(preload, `
import { writeFileSync } from "node:fs";
const real = globalThis.fetch;
globalThis.fetch = async (target, init = {}) => {
  if (!String(target).includes("api.anthropic.com")) return real(target, init);
  writeFileSync(process.env.SUGGEST_REQUEST_OUT, init.body);
  return {
    ok: true,
    status: 200,
    text: async () => "",
    json: async () => ({ stop_reason: "tool_use", content: [{ type: "tool_use", id: "t1", name: "propose", input: { tests: JSON.parse(process.env.SUGGEST_TESTS) } }] }),
  };
};
`);
    const tests = [
      REAL[0],
      { title: "A user can reset a forgotten password", sentence: "Click Forgot your password and follow the reset link.", criticality: "normal", evidence: "Forgot your password?" },
    ];
    // spawn, NOT spawnSync. The app being surveyed is served from THIS process, and spawnSync
    // blocks its event loop for the whole run — so the child's very first page.goto sat there
    // until Playwright's 30s timeout and the command exited 2, having proved only that a node
    // process cannot answer HTTP while it is blocked.
    const r = await run([bin, "suggest", "--url", url, "--out", out, "--max", "4", "--yes"], {
      ...process.env,
      ANTHROPIC_API_KEY: "sk-ant-test",
      NODE_OPTIONS: `--import ${pathToFileUrl(preload)}`,
      SUGGEST_TESTS: JSON.stringify(tests),
      SUGGEST_REQUEST_OUT: seen,
    });

    assert.equal(r.status, 0, `exit ${r.status}\n${r.stdout}\n${r.stderr}`);
    assert.deepEqual(filesIn(out), ["a-shopper-can-buy-pro.md"], `${r.stdout}\n${r.stderr}`);
    assert.match(r.stdout, /dropped "A user can reset a forgotten password"/, r.stdout);
    // The run tells the person the next command, because a folder of markdown they cannot run is
    // not an answer to "what do I even write".
    assert.match(r.stdout, /npx smolanalytics test --suite/, r.stdout);
    // And the request really went through the flags: --max 4 reached the model.
    assert.match(readFileSync(seen, "utf8"), /at most 4 tests/);
  });
});

/** file:// URL for NODE_OPTIONS --import, which does not accept a bare path on every platform. */
function pathToFileUrl(p) {
  return new URL(`file://${path.resolve(p)}`).href;
}

/** The child, run without blocking the event loop the fixture app is served from. */
function run(argv, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, argv, { env });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (d) => (stdout += d));
    child.stderr.on("data", (d) => (stderr += d));
    child.on("error", reject);
    child.on("close", (status) => resolve({ status, stdout, stderr }));
  });
}

// THE EVIDENCE CHECK MUST ACCEPT THE FORMAT A REAL MODEL ACTUALLY WRITES.
//
// This file had 33 passing tests while the command was incapable of writing a single file. Every
// one of them scripted the model, and a scripted model answers in whatever shape the assertion
// expects. Against a real key the command dropped four proposals out of four and printed "no
// flows to propose" — because we show the model the perception rendering, where a control reads
// `button "Upgrade to Pro"`, and it quoted its evidence back in the format we taught it, while
// the corpus holds the bare name. The anti-fabrication rule rejected every true proposal it was
// built to protect, silently, with exit 0.
//
// So these tests use the strings a live model produced, verbatim, rather than strings chosen to
// make the check pass.
test("evidence survives the role decoration the model copies from our own rendering", () => {
  const corpus = corpusOf([{
    url: "http://127.0.0.1/signup",
    title: "Sign up",
    elements: [{ name: "Create account", value: "" }, { name: "Email", value: "" }],
    text: "Create your account",
  }]);

  // Exactly what the live run returned, and what used to be dropped.
  assert.equal(evidenceIsReal('button "Create account"', corpus), true);
  assert.equal(evidenceIsReal('heading "Create your account"', corpus), true);
  assert.equal(evidenceIsReal('textbox "Email"', corpus), true);
  // Bare quotes still work — the old path must not regress.
  assert.equal(evidenceIsReal("Create your account", corpus), true);
});

test("stripping the decoration does not weaken the rule it exists for", () => {
  const corpus = corpusOf([{
    url: "http://127.0.0.1/signup",
    title: "Sign up",
    elements: [{ name: "Create account", value: "" }],
    text: "Create your account",
  }]);

  // The flow this app does not have. Quoting it in our own format must not launder it in.
  assert.equal(evidenceIsReal('link "Forgot your password?"', corpus), false);
  assert.equal(evidenceIsReal('button "Apply coupon"', corpus), false);
  // A quote short enough to appear in anything is not evidence of anything.
  assert.equal(evidenceIsReal('a', corpus), false);
  assert.equal(evidenceIsReal('"a"', corpus), false);
  assert.equal(evidenceIsReal("", corpus), false);
});

test("vet keeps a proposal whose evidence is role-decorated, and still drops an invented one", () => {
  const corpus = corpusOf([{
    url: "http://127.0.0.1/pricing",
    title: "Pricing",
    elements: [{ name: "Upgrade to Pro", value: "" }],
    text: "Pro is $9/mo.",
  }]);
  const { kept, dropped } = vet([
    { title: "A user can upgrade to Pro", sentence: "Click Upgrade to Pro.", criticality: "critical", evidence: 'button "Upgrade to Pro"' },
    { title: "A user can reset a password", sentence: "Click Forgot password.", criticality: "normal", evidence: 'link "Forgot your password?"' },
  ], corpus, 5);

  assert.deepEqual(kept.map((k) => k.title), ["A user can upgrade to Pro"]);
  assert.deepEqual(dropped.map((d) => d.title), ["A user can reset a password"]);
});

// ================================================================================================
// WHAT THE FIRST FIX STILL LET THROUGH.
//
// The evidence check was repaired once, after a real key proved it dropped four true proposals out
// of four. That repair — accept the run the model put in quotation marks, not only the whole
// string — was right, and it opened three ways to launder a fabrication past the same rule. Each
// of these was MEASURED against the fixture below before it was fixed: `vet()` kept invented
// password-reset, wishlist and coupon proposals, and they were written to disk as files.
// ================================================================================================

describe("evidence that is not evidence", () => {
  /** The app the corpus is built from: no wishlist, no coupon, no password reset, as ever. */
  const shop = () =>
    corpusOf([
      { url: "http://127.0.0.1:8080/", title: "Widget Store", elements: [{ name: "Pricing" }, { name: "Sign in" }], text: "Widget Store — the best widgets for your team. Browse the catalogue." },
      { url: "http://127.0.0.1:8080/pricing", title: "Pricing", elements: [{ name: "Buy Pro" }], text: "Pro plan is $29 / month." },
    ]);

  test("one true quote does not carry a false one in beside it", () => {
    const corpus = shop();
    // MEASURED, and the reason `some` became `every`: a model does not always quote once. This is
    // the shape that got through — a real control named next to an invented one, in a single
    // evidence string. "Buy Pro" is on the pricing page, "Add to wishlist" is on no page of any
    // app the crawl read, and the proposal it justified was a wishlist test.
    assert.equal(evidenceIsReal('the "Add to wishlist" button next to "Buy Pro"', corpus), false);
    const { kept, dropped } = vet(
      [{ title: "A shopper can use their wishlist", sentence: "Add a widget to the wishlist.", criticality: "normal", evidence: 'the "Add to wishlist" button next to "Buy Pro"' }],
      corpus,
      6,
    );
    assert.deepEqual(kept, [], "an invented control was written into somebody's suite because a real one was quoted beside it");
    assert.deepEqual(dropped.map((d) => d.title), ["A shopper can use their wishlist"]);
    // Every run quoted, still true, is still evidence: the rule tightened, it did not invert.
    assert.equal(evidenceIsReal('the "Buy Pro" button under "Pricing"', corpus), true);
  });

  test("a quote may not span two pages, because nobody could have read it there", () => {
    const corpus = shop();
    // Flattened into one string, the pages were joined by a newline that normalising turns into a
    // space — so the last words of the home page and the first of /pricing formed a phrase that is
    // on NEITHER page, and quoting it passed. A corpus is a list of pages for this reason.
    assert.equal(evidenceIsReal("browse the catalogue. /pricing", corpus), false);
    assert.equal(evidenceIsReal("Browse the catalogue.", corpus), true, "a run that IS on one page must still pass");
  });

  test("the words on every page, and the address of the app, prove nothing about it", () => {
    const corpus = shop();
    // All of these cleared the three-character floor, all are in the corpus, and each one was
    // accepted as proof of a flow the app does not have.
    for (const nothing of ['"the"', '"your"', '"http"', 'link "://"', '"127.0.0.1"', '"http://127.0.0.1:8080/"']) {
      assert.equal(evidenceIsReal(nothing, corpus), false, `${nothing} was accepted as evidence of a flow`);
    }
    // The path is not the origin: /pricing is something this app HAS, and the model is shown it.
    assert.equal(evidenceIsReal("/pricing", corpus), true);
    assert.equal(evidenceIsReal('"http://127.0.0.1:8080/pricing"', corpus), true, "a model that quotes the whole URL quoted a real page");
  });

  test("a quote is a run of words, not a fragment of one", () => {
    const corpus = shop();
    assert.equal(evidenceIsReal('"idge"', corpus), false, '"idge" is inside "widgets" and is a quote of nothing');
    assert.equal(evidenceIsReal('"widgets"', corpus), true);
  });

  test("an app not written in English still gets its proposals", () => {
    // MEASURED WHILE FIXING THE RULE ABOVE, and it is this file's original sin wearing another
    // language: splitting a run on [a-z0-9] to look for a word finds NOTHING in 購入する, so every
    // proposal for a Japanese app was substance-free, every one was dropped, and the run said "no
    // flows to propose" and exited 0 — the silent, green, useless answer, for every app in every
    // script but ours. Russian, Arabic, Greek and Korean all did the same.
    const corpus = corpusOf([
      { url: "http://127.0.0.1:8080/checkout", title: "ご購入手続き", elements: [{ name: "購入する" }, { name: "カートに追加" }], text: "ご購入手続き 送料無料" },
    ]);
    assert.equal(evidenceIsReal("購入する", corpus), true);
    assert.equal(evidenceIsReal('ボタン "購入する"', corpus), true, "the role decoration a model adds is not language-specific");
    // Two characters, because one CJK character is a whole word and the three-character floor was
    // written for English.
    assert.equal(evidenceIsReal("購入", corpus), true);
    // And the rule still bites: these are the wishlist and the coupon this app does not have.
    assert.equal(evidenceIsReal("ほしい物リスト", corpus), false);
    assert.equal(evidenceIsReal("クーポン", corpus), false);
    const { kept, dropped } = vet(
      [
        { title: "購入できる", sentence: "商品を購入する。", criticality: "critical", evidence: "購入する" },
        { title: "ほしい物リストが使える", sentence: "ほしい物リストを開く。", criticality: "normal", evidence: "ほしい物リスト" },
      ],
      corpus,
      6,
    );
    assert.deepEqual(kept.map((k) => k.title), ["購入できる"]);
    assert.deepEqual(dropped.map((d) => d.title), ["ほしい物リストが使える"]);
  });

  test("an apostrophe is not a quotation mark, and a contraction must not cost a true proposal", () => {
    // The cost of requiring EVERY quoted run to be real: if ' opened a quote, `button "Don't
    // Panic"` would be read as a quote of `Don`, and an English contraction would drop a genuine
    // flow — the exact failure this whole check has already committed once.
    const corpus = corpusOf([{ url: "http://127.0.0.1:8080/login", title: "Sign in", elements: [{ name: "Sign in" }], text: "Don't have an account? Create one." }]);
    assert.equal(evidenceIsReal(`button "Don't have an account?"`, corpus), true);
    assert.equal(evidenceIsReal(`the 'Sign in' button`, corpus), true, "a single-quoted run is still a quoted run");
    assert.equal(evidenceIsReal(`button "Don't have a wishlist?"`, corpus), false);
  });
});

// ---- pages the server refused ------------------------------------------------------------------

describe("a page the app did not serve is not a page", () => {
  test("a 404 behind a dead nav link is skipped, and cannot become evidence for a flow", noBrowser, async () => {
    // MEASURED, AND IT MANUFACTURED FABRICATIONS. goto() only rejects when the navigation itself
    // fails, so a 404 arrived as a success and its error document was surveyed like any other
    // page: `URL: .../wishlist` went into the corpus, and a proposal quoting "wishlist" passed the
    // evidence check and was written as a file. A dead link in somebody's navigation was inventing
    // features for them.
    const ghost = createServer((req, res) => {
      if (req.url === "/") {
        res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
        // The link is NAMED "More" on purpose. A nav link that reads "Wishlist" is itself evidence
        // that the app offers one, however broken the page behind it — so it would prove nothing
        // about where the corpus came from. Named "More", the only place the word "wishlist"
        // exists is the URL of the page the server refused to serve.
        res.end(html("Shop", '<h1>Shop</h1><nav><a href="/wishlist">More</a></nav><button>Buy</button>'));
        return;
      }
      res.writeHead(404, { "content-type": "text/html; charset=utf-8" });
      res.end(html("Not found", "<h1>Page not found</h1>"));
    });
    await new Promise((r) => ghost.listen(0, "127.0.0.1", r));
    const out = outDir();
    try {
      const r = await suggest({
        at: `http://127.0.0.1:${ghost.address().port}/`,
        out,
        tests: [{ title: "A shopper can use their wishlist", sentence: "Open the wishlist and see saved items.", criticality: "normal", evidence: "wishlist" }],
      });
      assert.equal(r.code, 0, r.out);
      assert.deepEqual(filesIn(out), [], "a 404 page's own URL became the evidence for a flow the app does not have");
      // Said out loud, like every other page the walk declines to use.
      assert.match(r.out, /could not open .*\/wishlist \(the server answered 404\) — skipped/, r.out);
      // And never shown to the model either: "it is in the corpus" and "the model could have read
      // it" have to stay the same claim.
      assert.ok(!r.sent[0].messages[0].content.includes("/wishlist"), "a page the server said does not exist was shown to the model");
    } finally {
      await new Promise((r) => { ghost.closeAllConnections?.(); ghost.close(() => r()); });
    }
  });

  test("a start page that 500s is exit 2 and says so, never a shrug about there being no flows", noBrowser, async () => {
    // The old answer was exit 0 and "the pages visited showed no user flow worth a test", which
    // reads as "your app is fine, there is just nothing here" — about an app that is down.
    const boom = createServer((_, res) => {
      res.writeHead(500, { "content-type": "text/html; charset=utf-8" });
      res.end(html("Error", "<h1>Internal Server Error</h1>"));
    });
    await new Promise((r) => boom.listen(0, "127.0.0.1", r));
    const out = outDir();
    try {
      const r = await suggest({ at: `http://127.0.0.1:${boom.address().port}/`, out, tests: REAL });
      assert.equal(r.code, 2, `an app that never served a page is this runner having nothing to survey:\n${r.out}`);
      assert.match(r.out, /the server answered 500/, `the status is the one useful fact:\n${r.out}`);
      assert.ok(!/no flows to propose/.test(r.out), `a broken app was reported as an app with nothing worth testing:\n${r.out}`);
      assert.deepEqual(filesIn(out), []);
      // 2 is "this runner could not do its job". 1 would claim the application is broken, which is
      // a verdict, and no survey is allowed to reach one.
      assert.notEqual(r.code, 1);
    } finally {
      await new Promise((r) => { boom.closeAllConnections?.(); boom.close(() => r()); });
    }
  });
});

// ---- what happens when the walk or the model comes back with nothing ----------------------------

describe("nothing to survey is reported, never written around", () => {
  test("a start URL that redirects off this app leaves nothing readable, and that is exit 2", noBrowser, async () => {
    // An auth wall or an SSO provider: the app answers, but every page belongs to somebody else,
    // and somebody else's app is exactly the fabrication material this command keeps out. Zero
    // pages read is not zero flows found — there is nothing to have an opinion about.
    const out = outDir();
    const r = await suggest({ at: `${url}away`, out, tests: REAL });
    assert.equal(r.code, 2, r.out);
    assert.match(r.out, /nothing was readable at that URL/, r.out);
    assert.deepEqual(filesIn(out), []);
  });

  test("a model that refuses, and a model that answers without a proposal, are both exit 2", noBrowser, async () => {
    for (const [label, body] of [
      ["a refusal", { stop_reason: "refusal", content: [] }],
      ["prose instead of a proposal list", { stop_reason: "end_turn", content: [{ type: "text", text: "Here are some ideas!" }] }],
    ]) {
      const realFetch = globalThis.fetch;
      globalThis.fetch = async () => ({ ok: true, status: 200, text: async () => "", json: async () => body });
      const out = outDir();
      const lines = [];
      try {
        const code = await suggestCmd({
          url,
          out,
          env: { ANTHROPIC_API_KEY: "sk-ant-test" },
          log: (...a) => lines.push(a.join(" ")),
          loadBrowser: async () => ({ pw: { chromium } }),
        });
        assert.equal(code, 2, `${label} is our model call not working, never a verdict about their app`);
        assert.match(lines.join("\n"), /the survey could not complete/i, label);
        assert.deepEqual(filesIn(out), [], label);
      } finally {
        globalThis.fetch = realFetch;
      }
    }
  });
});

// ---- the handoff --------------------------------------------------------------------------------

describe("the folder suggest writes is a folder the suite runner can run", () => {
  test("discover() finds every file, and reads back the titles that were written", noBrowser, async () => {
    // parseSuite on one file proves the FORMAT. This proves the HANDOFF: the same call
    // `test --suite <dir>` makes, against the directory this command just created — filenames
    // included, since discover() skips anything beginning with a dot and a title that slugs to
    // nothing would have written exactly that.
    const out = outDir();
    await suggest({ tests: REAL, out });
    const found = discover(out, path.join(scratch(), "plans"));
    assert.deepEqual(found.errors, [], "the suite runner could not read what suggest wrote");
    assert.deepEqual(found.notes, [], "the suite runner had to interpret something in a file we generated");
    assert.ok(!found.missing, "the folder suggest just created was reported as missing");
    assert.deepEqual(
      found.tests.map((t) => t.name).sort(),
      ["A shopper can buy Pro", "A shopper can search"],
      "the names on the pull request are the titles the model wrote, not filenames with the dashes taken out",
    );
    for (const t of found.tests) {
      assert.ok(!t.test.includes("---"), t.test);
      assert.ok(!t.test.includes("criticality"), t.test);
    }
  });

  test("two proposals whose titles make the same filename both survive", noBrowser, async () => {
    // slug() keeps [a-z0-9-] and falls back to "test", so "A shopper can pay" and "A shopper can
    // pay!" want one filename — and EVERY title in an app not written in a Latin script does. The
    // second used to hit the never-overwrite guard and be reported as "it already exists", which
    // was untrue: this run had written it a moment earlier. An app with Japanese titles got one
    // file out of six and was told the other five were already there.
    const out = outDir();
    const r = await suggest({
      out,
      tests: [
        { title: "A shopper can pay", sentence: "Pay for Pro and see the receipt.", criticality: "critical", evidence: "Buy Pro" },
        { title: "A shopper can pay!", sentence: "Pay again and see the receipt.", criticality: "normal", evidence: "Buy Pro" },
        { title: "支払いができる", sentence: "Pay from the pricing page and see the receipt.", criticality: "normal", evidence: "Buy Pro" },
        { title: "検索ができる", sentence: "Search and see matching products.", criticality: "normal", evidence: "Search products" },
      ],
    });
    assert.equal(r.code, 0, r.out);
    assert.deepEqual(filesIn(out), ["a-shopper-can-pay-2.md", "a-shopper-can-pay.md", "test-2.md", "test.md"], r.out);
    assert.match(r.out, /4 tests written/, r.out);
    assert.ok(!/already exists/.test(r.out), `a file this run had just written was reported as somebody else's:\n${r.out}`);
    // Still four separate tests to the runner, with the titles the model actually wrote.
    const found = discover(out, path.join(scratch(), "plans"));
    assert.deepEqual(found.tests.map((t) => t.name).sort(), ["A shopper can pay", "A shopper can pay!", "支払いができる", "検索ができる"].sort());
    // And the never-overwrite rule is untouched: a file that was there BEFORE this run is still
    // the person's own.
    const r2 = await suggest({ out, tests: [{ title: "A shopper can pay", sentence: "Different sentence.", criticality: "normal", evidence: "Buy Pro" }] });
    assert.match(r2.out, /already exists/, r2.out);
    assert.match(readAll(out)["a-shopper-can-pay.md"], /Pay for Pro and see the receipt/, "a re-run rewrote a file it had written before");
  });
});
