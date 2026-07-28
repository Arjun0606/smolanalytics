import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { detect, readDeps } from "../lib/detect.mjs";
import {
  insertIntoHtml,
  insertIntoNextLayout,
  upsertEnv,
  alreadyInstalled,
  applyStrategy,
  MANUAL_SNIPPETS,
} from "../lib/insert.mjs";

/** Minimal fs stub: a map of path -> contents. */
const mkfs = (files) => ({
  exists: (p) => Object.prototype.hasOwnProperty.call(files, p),
  read: (p) => files[p],
});

const pkg = (deps) => JSON.stringify({ dependencies: deps });

describe("detect", () => {
  test("prefers the Next App Router layout over a stray index.html", () => {
    const fs = mkfs({
      "package.json": pkg({ next: "15", react: "19" }),
      "app/layout.tsx": "",
      "index.html": "",
    });
    const d = detect(fs);
    assert.equal(d.strategy, "next-app");
    assert.equal(d.file, "app/layout.tsx");
  });

  test("finds a src/ App Router layout", () => {
    const fs = mkfs({ "package.json": pkg({ next: "15" }), "src/app/layout.tsx": "" });
    assert.equal(detect(fs).file, "src/app/layout.tsx");
  });

  test("falls back to the Pages Router document", () => {
    const fs = mkfs({ "package.json": pkg({ next: "14" }), "pages/_document.tsx": "" });
    assert.equal(detect(fs).strategy, "next-pages");
  });

  test("Next.js with neither layout nor document reports a note and edits nothing", () => {
    const d = detect(mkfs({ "package.json": pkg({ next: "15" }) }));
    assert.match(d.note, /nothing was edited/);
  });

  test("SvelteKit resolves to its single HTML shell", () => {
    const fs = mkfs({ "package.json": pkg({ "@sveltejs/kit": "2" }), "src/app.html": "" });
    assert.deepEqual(
      { f: detect(fs).file, s: detect(fs).strategy },
      { f: "src/app.html", s: "html-head" },
    );
  });

  test("Astro and Nuxt are recognised but never edited", () => {
    const astro = detect(mkfs({ "package.json": pkg({ astro: "5" }) }));
    const nuxt = detect(mkfs({ "package.json": pkg({ nuxt: "3" }) }));
    assert.equal(astro.strategy, "manual");
    assert.equal(nuxt.strategy, "manual");
    // the generic snippet would produce a page that looks instrumented and sends nothing
    assert.equal(applyStrategy("manual", "<head></head>", "h", "k").status, "manual");
  });

  test("the manual snippets are the framework's REAL install, not the html one", () => {
    assert.match(MANUAL_SNIPPETS.astro("h", "k"), /is:inline/);
    assert.match(MANUAL_SNIPPETS.nuxt("h", "k"), /defineNuxtConfig/);
    assert.match(MANUAL_SNIPPETS.nuxt("h", "k"), /app:\s*\{/);
  });

  test("Vite vs plain static HTML are named differently but handled the same", () => {
    assert.equal(detect(mkfs({ "package.json": pkg({ vite: "6" }), "index.html": "" })).framework, "Vite");
    assert.equal(detect(mkfs({ "index.html": "" })).framework, "static HTML");
  });

  test("returns null when nothing is recognised, rather than guessing at a file", () => {
    assert.equal(detect(mkfs({ "README.md": "" })), null);
  });

  test("survives a malformed package.json instead of crashing the install", () => {
    assert.deepEqual(readDeps(mkfs({ "package.json": "{ not json" })), {});
  });
});

describe("insertIntoHtml", () => {
  const html = "<html>\n  <head>\n    <title>x</title>\n  </head>\n  <body></body>\n</html>";

  test("inserts before </head> and keeps the rest byte-for-byte", () => {
    const r = insertIntoHtml(html, "https://i.fly.dev", "sa_key");
    assert.equal(r.status, "inserted");
    assert.ok(r.content.includes('smolanalytics.init("sa_key"'));
    assert.ok(r.content.indexOf("smolanalytics") < r.content.indexOf("</head>"));
    assert.ok(r.content.includes("<title>x</title>"));
  });

  test("is idempotent — running init twice does not add a second tracker", () => {
    const once = insertIntoHtml(html, "https://i.fly.dev", "sa_key").content;
    assert.equal(insertIntoHtml(once, "https://i.fly.dev", "sa_key").status, "already");
  });

  test("matches </HEAD> case-insensitively", () => {
    assert.equal(insertIntoHtml("<HEAD></HEAD>", "h", "k").status, "inserted");
  });

  // A commented-out example earlier in the file must not capture the insert.
  test("anchors on the LAST </head>, not a commented-out one", () => {
    const tricky = "<!-- <head></head> -->\n<html><head><title>t</title></head><body></body></html>";
    const r = insertIntoHtml(tricky, "h", "k");
    assert.ok(r.content.indexOf("smolanalytics") > r.content.indexOf("<title>t</title>"));
  });

  test("refuses rather than guessing when there is no head", () => {
    const r = insertIntoHtml("<div>no head here</div>", "h", "k");
    assert.equal(r.status, "no-anchor");
    assert.equal(r.content, undefined);
  });
});

describe("insertIntoNextLayout", () => {
  // The default create-next-app layout has no <head> at all — this is the majority case.
  const layout = `export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body className="x">{children}</body>
    </html>
  );
}`;

  test("falls back to just after <body> when the layout declares no head", () => {
    const r = insertIntoNextLayout(layout, "https://i.fly.dev", "sa_k");
    assert.equal(r.status, "inserted");
    assert.ok(r.content.indexOf("smolanalytics") > r.content.indexOf("<body"));
    assert.ok(r.content.indexOf("smolanalytics") < r.content.indexOf("{children}"));
  });

  test("prefers an explicit </head> when the layout has one", () => {
    const withHead = `<html><head><title>t</title></head><body>{children}</body></html>`;
    const r = insertIntoNextLayout(withHead, "h", "k");
    assert.ok(r.content.indexOf("smolanalytics") < r.content.indexOf("</head>"));
  });

  test("does not mistake </body> for an opening tag", () => {
    const r = insertIntoNextLayout("<html><body>{children}</body></html>", "h", "k");
    assert.ok(r.content.includes("<body>\n"));
  });

  test("is idempotent", () => {
    const once = insertIntoNextLayout(layout, "h", "k").content;
    assert.equal(insertIntoNextLayout(once, "h", "k").status, "already");
  });
});

describe("upsertEnv", () => {
  test("appends a key to an existing file without touching other lines", () => {
    const out = upsertEnv("EXISTING=1\n", "SMOLANALYTICS_WRITE_KEY", "sa_k");
    assert.equal(out, "EXISTING=1\nSMOLANALYTICS_WRITE_KEY=sa_k\n");
  });

  test("replaces in place rather than duplicating on re-run", () => {
    const once = upsertEnv("", "K", "a");
    assert.equal(upsertEnv(once, "K", "b"), "K=b\n");
  });

  test("adds the missing newline a hand-edited file often lacks", () => {
    assert.equal(upsertEnv("A=1", "B", "2"), "A=1\nB=2\n");
  });

  test("does not match a key that merely shares a prefix", () => {
    const out = upsertEnv("SMOLANALYTICS_WRITE_KEY_OLD=x\n", "SMOLANALYTICS_WRITE_KEY", "new");
    assert.ok(out.includes("SMOLANALYTICS_WRITE_KEY_OLD=x"));
    assert.ok(out.includes("SMOLANALYTICS_WRITE_KEY=new"));
  });
});

describe("applyStrategy", () => {
  test("routes each strategy to its inserter and rejects unknown ones", () => {
    assert.equal(applyStrategy("html-head", "<head></head>", "h", "k").status, "inserted");
    assert.equal(applyStrategy("next-app", "<body></body>", "h", "k").status, "inserted");
    assert.equal(applyStrategy("nonsense", "<head></head>", "h", "k").status, "no-anchor");
  });

  test("alreadyInstalled sees a hand-placed snippet too", () => {
    assert.equal(alreadyInstalled('<script>smolanalytics.init("x")</script>'), true);
    assert.equal(alreadyInstalled("<head></head>"), false);
  });
});
