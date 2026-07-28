// detectEnv decides whether an event is counted as production, and the query layer hides
// everything that is not. Get it wrong in one direction and previews pollute real numbers;
// get it wrong in the other and someone's actual traffic silently disappears from their
// dashboard. Only one of those is recoverable by the person looking at the screen, which is
// why the production cases below matter more than the preview ones.
//
// Run: node internal/api/sdktest/env.test.mjs (wired into CI).
import fs from "node:fs";
const src = fs.readFileSync(new URL("../sdk.js", import.meta.url), "utf8");
const m = src.match(/function detectEnv\(h\) \{[\s\S]*?\n  \}/);
if (!m) { console.error("detectEnv not found"); process.exit(1); }
const detectEnv = new Function(`${m[0]}; return detectEnv;`)();

const cases = [
  // must be production — hiding these loses someone's real traffic
  ["acme.com", "production"], ["www.acme.com", "production"],
  ["my-app.vercel.app", "production"],        // production sites DO live here
  ["shop.acme.co.uk", "production"],
  ["previews.acme.com", "production"],        // "previews." != "preview."
  ["development-tools.com", "production"],    // word boundary, not a prefix match
  ["testing-inc.com", "production"],
  // development
  ["localhost", "development"], ["127.0.0.1", "development"], ["[::1]", "development"],
  ["192.168.1.14", "development"], ["10.0.0.5", "development"], ["172.16.4.4", "development"],
  ["myapp.local", "development"], ["foo.test", "development"],
  ["abc123.ngrok-free.app", "development"], ["x.trycloudflare.com", "development"],
  // preview
  ["deploy-preview-42--acme.netlify.app", "preview"],
  ["feat-login--acme.netlify.app", "preview"],
  ["acme.netlify.app", "production"],          // a real Netlify production site
  ["staging.acme.com", "preview"], ["preview.acme.dev", "preview"], ["qa.acme.io", "preview"],
];
let fail = 0;
for (const [host, want] of cases) {
  const got = detectEnv(host);
  const ok = got === want;
  if (!ok) fail++;
  console.log(`${ok ? "ok  " : "FAIL"}  ${host.padEnd(38)} -> ${got}${ok ? "" : `  (want ${want})`}`);
}
console.log(`\n${cases.length - fail}/${cases.length} passed`);
process.exit(fail ? 1 : 0);
