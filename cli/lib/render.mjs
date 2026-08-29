// FALSE-GREEN GUARD — the page that replayed green while a human would call it broken.
//
// Our replay proof is page TEXT: `document.body.innerText.includes(plan.proof)`. innerText is
// computed from the DOM, not from what was painted, so all three of these replay GREEN today:
//
//   the CSS 404'd on the preview   → every string is still in innerText, the page is naked HTML
//   the app rendered nothing       → an empty <div id="root">, and the proof came from a node
//                                    parked at left:-9999px; innerText includes it, the screen
//                                    is white. MEASURED: 32 characters of innerText, 0 painted.
//   a framework error overlay      → Next.js paints its overlay into a shadow root, and
//                                    body.innerText does not include shadow text, so the proof
//                                    underneath is found exactly as if nothing had happened
//
// A green check over a page that visibly does not work is the fastest way to lose a customer who
// already believes us. So this is a VERDICT-AFFECTING GUARD, not an advisory note — the opposite
// of lib/layout.mjs, which is why it is a separate file with its own review surface.
//
// THE VERDICT RULE, exactly:
//
//   a would-be PASS + a catastrophic render → `failed`, reason = the render finding
//   a `failed`                              → untouched. Never softened, never re-worded.
//   `stale` / `errored`                     → never checked at all. Nothing passed, so nothing
//                                             can be falsely green, and a check that fired here
//                                             would be inventing a bug report on a run that
//                                             reached no verdict.
//   `flaky`                                 → also never checked. A retry that passed is already
//                                             a "look at this" verdict; re-labelling it failed
//                                             blurs two of the five statuses to catch a case the
//                                             next green run catches properly.
//   --no-render-check                       → not computed.
//
// FALSE POSITIVES ARE THE ONLY RISK THAT MATTERS. This check reddens a build by default, so one
// legitimately-rendered page flagged once and it gets switched off in every repo that saw it —
// and then the false green comes back with our blessing. Every threshold below is therefore set
// where it CANNOT fire on a working page, accepting that it will miss milder breakage:
// a missed catastrophe costs one bad run, a false catastrophe costs the customer.
//
// FIVE FALSE POSITIVES WERE MEASURED AND KILLED while building this, each against real Chromium
// and a real local server. They are named at the threshold that now excludes them, because a
// number with no story behind it is the number a later edit "simplifies":
//
//   a long page SCROLLED into a whitespace gap        → reported BLANK   (see DOCUMENT, not viewport)
//   a zero-byte stylesheet served 200                 → reported NAKED   (see JUDGEABLE)
//   an incident write-up headed "Internal Server Error"→ reported OVERLAY (see the page-error shape)
//   a docs page whose <body> filled the viewport      → reported OVERLAY (see POSITIONED)
//   a cookie banner reading "Runtime error reporting
//   is optional"                                      → reported OVERLAY (see LINE START)
//
// It detects CATASTROPHE ONLY. Not "looks a bit off", not contrast, not a broken image among
// twenty good ones. Three shapes, each measured in the page in one page.evaluate, deterministic,
// no vision model, no screenshot, no second model call, ~free (measured 5–40ms on a healthy page).

// ---- measured thresholds ------------------------------------------------------------------------

// The smallest picture that counts as "something rendered". MEASURED against the shapes that
// actually occur: an analytics beacon is 1×1 (area 1), a favicon-sized icon 16×16 (256), a
// spinner is 2,304 (measured, the 48×48 ring in the /loading fixture), and a gallery thumbnail
// measured 60,000 with a game canvas at 480,000. 1024 px² (32×32) sits above every tracking pixel
// and glyph-sized icon and far below anything a person would describe as content — and,
// deliberately, below that spinner, so a page that is merely still loading is never called blank.
const MEDIA_MIN_AREA = 1024;

// Effective opacity (the product of the element's own and its ancestors') below which text is
// treated as not painted. 0.05 rather than 0: a fade that has started is still arriving, and
// several UI kits park a decorative layer at 0.02–0.04 on purpose. Text at 5% on white is
// invisible to a person by any reasonable measure.
const MIN_OPACITY = 0.05;

// BLANK is measured TWICE, this far apart, and reported only if both looks agree.
//
// The whole false-positive risk for BLANK is timing: the audit runs the instant after the agent's
// last click or the replay's last step, which is exactly when a route transition, a hydration or
// an opacity fade is in flight. MEASURED against the /fade fixture, which animates its content in
// over 800ms: at t=0 the page had 0 painted characters, and at t=700ms it had 43. 700ms clears
// Material's longest standard transition (375ms) with room, and it is only ever paid on a page
// that already looked blank once — a healthy page pays nothing.
const RE_MEASURE_MS = 700;

// The share of the viewport an element must cover before "an error overlay is covering the page"
// is a fair description of it. A Next.js dev overlay and a Vite overlay are both inset:0 (100%);
// a modal dialog that a working app opened deliberately measured 29.7% of a 1280×900 viewport in
// the /modal fixture. 60% is well above that and below every full-screen error surface. The same
// fixture's BACKDROP does cover 100%, is position:fixed, and hit-tests topmost — it is excluded by
// having no text of its own, which is why coverage is never enough alone: see STRUCTURE below.
const VIEWPORT_COVER = 0.6;

// An error surface is SHORT. The Next.js-shaped dialog measured 114 characters (message, file,
// one frame) and a real one with a longer stack runs to a few hundred; an nginx 502 body measured
// 21 characters, and express in production is the same. A real application screen that happens to
// contain the words "Internal Server Error" — an API doc, a status page, a log viewer — runs to
// thousands. 4000 characters is well above any error surface and below any page of content.
const OVERLAY_TEXT_MAX = 4000;

// LINE START. The match must BEGIN one of the first few lines of the surface's own text — not
// merely appear in it. This is the threshold that killed the worst false positive of the five: a
// consent sheet reading "We use cookies. Runtime error reporting is optional." is positioned,
// full-viewport and topmost, and every structural test passed it; the phrase sits mid-sentence,
// and that is the only thing that distinguishes it from a crash screen.
//
// MEASURED: in the consent sheet the phrase sits at character 39 of the line "We use cookies.
// Runtime error reporting is optional." In the surfaces this is meant to catch it opens a line
// every time — Next.js puts "Unhandled Runtime Error" at the start of its dialog (behind at most a
// "1 of 1 error" and a version line), Vite opens with "[plugin:vite:import-analysis]", nginx with
// "502 Bad Gateway", which is why a leading status code is stripped before the match is tried.
const ERROR_HEAD_LINES = 8;
const ERROR_HEAD_CHARS = 400;

// The whole-page HTTP error shape: an nginx/Apache/express error body is a heading, maybe a server
// signature, and nothing else. MEASURED: an nginx 502 body is 21 characters with 0 controls.
//
// AND THE LENGTH IS THE WEAKEST OF THE THREE TESTS — say so, because the number looks like the one
// doing the work and is not. The incident write-up that this check falsely flagged measured 194
// characters, INSIDE this limit; what excludes it is that it has one link (controls) and a
// stylesheet that reached the body (bodyDefault). Both are required for exactly that reason, and
// either one alone would have kept it green.
const PAGE_ERROR_TEXT_MAX = 200;
const PAGE_ERROR_CONTROLS_MAX = 0;

// Walk caps. A page with more elements than this is emphatically not blank and emphatically not a
// bare error body, and the caps keep one page.evaluate off a pathological DOM.
const ELEMENT_CAP = 4000;
const STYLE_EVIDENCE_CAP = 120;

/**
 * The error surfaces, by TEXT. Never sufficient on its own — see the structure test in inPage, and
 * note that every one of these is matched at the START OF A LINE, never mid-sentence.
 * Each pattern is anchored to wording a framework emits, not to a word a page might use.
 */
const ERROR_TEXT = [
  // Next.js dev overlay and its error pages.
  /\bunhandled runtime error\b/i,
  /\bunhandled promise rejection\b/i,
  /this error happened while generating the page/i,
  /application error: a client-side exception has occurred/i,
  /\bbuild error\b/i,
  /\bfailed to compile\b/i,
  // Vite's overlay puts the failing plugin in brackets, and its dev server's 500 body.
  /\[plugin:vite:/i,
  /failed to resolve import/i,
  /\[vite\] internal server error/i,
  // React's own boundary-less crash surface, and CRA's overlay.
  /\bruntime error\b/i,
  /the above error occurred in the/i,
  // Reverse proxies and gateways.
  /\binternal server error\b/i,
  /\bbad gateway\b/i,
  /\bgateway time-?out\b/i,
  /\bservice (temporarily )?unavailable\b/i,
];

/** Elements a framework paints its error into. Structure, not text: half of the STRUCTURE test. */
const ERROR_SURFACE_SELECTOR = [
  "nextjs-portal",
  "vite-error-overlay",
  "#webpack-dev-server-client-overlay",
  ".react-error-overlay",
  "[data-nextjs-dialog]",
  "[data-nextjs-dialog-overlay]",
  "[data-nextjs-error-overlay]",
  "#__next_error__",
].join(",");

// ---- the probe, which runs inside the page ------------------------------------------------------

/**
 * A plain function: page.evaluate serialises it, so it may close over nothing. Returns a
 * measurement, never a verdict — the caller decides, because BLANK is decided across two
 * measurements and only the caller has both.
 *
 * WHAT EACH CHECK REFUSES TO FLAG is the part worth reviewing:
 *
 * BLANK asks about the DOCUMENT, not the viewport, and is suppressed by ANY running animation and
 * by readyState !== "complete". The viewport was the obvious unit and it was wrong: MEASURED, a
 * 3000px-tall page scrolled 1200px into its own whitespace reported "nothing rendered", and the
 * last step of a real run is very often a scroll. Content anywhere inside the document's own
 * scrollable box counts as rendered; a node at left:-9999px sits outside it and still does not.
 *
 * NAKED only judges a stylesheet whose HTTP status the page can actually SEE, and requires the
 * page to be UNSTYLED rather than merely to have a failed sheet. Two things were measured here and
 * both are counter-intuitive. First, in Chromium a stylesheet that 404s STILL has a non-null
 * `link.sheet` with `cssRules.length === 0` — identical, field for field, to a stylesheet that was
 * served 200 with zero bytes in it, which is a real and harmless thing that shipped a false NAKED.
 * So rule counts decide nothing and only `PerformanceResourceTiming.responseStatus` does. Second,
 * a cross-origin sheet is opaque — status 0, transferSize 0 — whether it loaded or 404'd, so a
 * CDN stylesheet can never be judged either way and is always treated as fine. That is a deliberate
 * miss: the shape this check exists for is the app's own build 404ing on a preview, same origin.
 *
 * OVERLAY MATCHES ON STRUCTURE PLUS TEXT, and the text must OPEN A LINE. Text alone would flag a
 * status page, an incident write-up, or a consent sheet that mentions error reporting — all three
 * measured. Structure is one of: the element is a known framework error surface; or it is
 * POSITIONED out of flow, covers most of the viewport, and hit-tests as the topmost thing at the
 * centre of the screen; or it is a whole page with no author CSS, nothing to click, and less text
 * than a paragraph.
 */
function inPage(args) {
  const {
    mediaMinArea, minOpacity, viewportCover, overlayTextMax, errorHeadLines, errorHeadChars,
    pageErrorTextMax, pageErrorControlsMax, elementCap, styleEvidenceCap,
    errorText, errorSurfaceSelector,
  } = args;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  // Anchored at construction: every match in this file is a match at the start of a line.
  const patterns = errorText.map((s) => new RegExp(`^(?:${s.source})`, s.flags));

  const describe = (el) => {
    if (!el || !el.tagName) return "an element";
    const tag = el.tagName.toLowerCase();
    if (el.id) return `<${tag}#${el.id}>`;
    if (el.classList && el.classList.length) return `<${tag}.${el.classList[0]}>`;
    // A framework overlay usually has neither: <div data-nextjs-dialog-overlay> is the whole of
    // its identity, and "a <div>" in a failure reason sends someone hunting.
    try {
      for (const a of el.attributes) if (a.name.startsWith("data-")) return `<${tag} ${a.name}>`;
    } catch {
      /* an element with no attributes collection */
    }
    return `<${tag}>`;
  };

  const intersects = (r) => r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < vh && r.left < vw;

  // DOCUMENT, NOT VIEWPORT — the fix for the scrolled-page false positive. A rect counts if it
  // lands inside the page's own scrollable box in document coordinates, which is everything a
  // person could reach by scrolling and nothing a developer parked off-screen at -9999px.
  const doc = document.documentElement;
  const scrollW = Math.max(doc ? doc.scrollWidth : 0, document.body ? document.body.scrollWidth : 0, vw);
  const scrollH = Math.max(doc ? doc.scrollHeight : 0, document.body ? document.body.scrollHeight : 0, vh);
  const onPage = (r) => {
    if (!(r.width > 0 && r.height > 0)) return false;
    const left = r.left + window.scrollX;
    const top = r.top + window.scrollY;
    return left + r.width > 0 && top + r.height > 0 && left < scrollW && top < scrollH;
  };

  // Effective opacity: an ancestor at 0 hides a child at 1, and only the product tells the truth.
  const shown = (el) => {
    let o = 1;
    let n = el;
    let hops = 0;
    while (n && n.nodeType === 1 && hops++ < 60) {
      const cs = getComputedStyle(n);
      if (cs.display === "none" || cs.visibility === "hidden" || cs.visibility === "collapse") return 0;
      const v = parseFloat(cs.opacity);
      if (Number.isFinite(v)) o *= v;
      if (o < minOpacity) return 0;
      n = n.parentElement || (n.getRootNode() && n.getRootNode().host) || null;
    }
    return o;
  };

  // Every element in the document AND inside every OPEN shadow root. Frameworks put their error
  // overlays in shadow DOM precisely so the app's CSS cannot touch them, and an overlay we cannot
  // see is an overlay that replays green — which is the bug this file exists for.
  const allElements = () => {
    const out = [];
    const roots = [document];
    while (roots.length && out.length < elementCap) {
      const root = roots.shift();
      let list;
      try {
        list = root.querySelectorAll("*");
      } catch {
        continue;
      }
      for (const el of list) {
        if (out.length >= elementCap) break;
        out.push(el);
        if (el.shadowRoot) roots.push(el.shadowRoot);
      }
    }
    return out;
  };

  // Text a person could actually reach, including open shadow roots, capped by an early exit: the
  // only question BLANK asks is "is there ANY".
  const paintedTextChars = () => {
    let chars = 0;
    const roots = [document.body].filter(Boolean);
    const seen = new Set();
    while (roots.length) {
      const root = roots.shift();
      if (!root || seen.has(root)) continue;
      seen.add(root);
      let walker;
      try {
        walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
      } catch {
        continue;
      }
      let n = walker.currentNode;
      while (n) {
        if (n.nodeType === 1 && n.shadowRoot) roots.push(n.shadowRoot);
        if (n.nodeType === 3) {
          const s = n.nodeValue ? n.nodeValue.trim() : "";
          const parent = n.parentElement;
          if (s && parent && shown(parent) > 0) {
            try {
              const range = document.createRange();
              range.selectNodeContents(n);
              for (const r of range.getClientRects()) {
                if (onPage(r)) {
                  chars += s.length;
                  break;
                }
              }
            } catch {
              /* a detached or unrangeable node proves nothing either way */
            }
          }
          if (chars > 0) return chars;
        }
        n = walker.nextNode();
      }
    }
    return chars;
  };

  const els = allElements();

  // --- (a) BLANK ---------------------------------------------------------------------------------
  let animating = false;
  try {
    animating = (document.getAnimations ? document.getAnimations() : []).some((a) => a.playState === "running");
  } catch {
    /* no getAnimations: treat as still, and let the two-look rule carry the timing risk */
  }
  const chars = paintedTextChars();
  let media = null;
  if (!chars) {
    for (const el of els) {
      const tag = el.tagName ? el.tagName.toLowerCase() : "";
      if (tag !== "img" && tag !== "canvas" && tag !== "svg" && tag !== "video" && tag !== "iframe" && tag !== "object" && tag !== "embed") continue;
      if (!shown(el)) continue;
      const r = el.getBoundingClientRect();
      if (!onPage(r) || r.width * r.height < mediaMinArea) continue;
      // A broken <img> still has a box. naturalWidth is 0 until it decodes, so an image that
      // failed to load cannot stand in for content — that is a blank page with a broken image on
      // it, which is exactly the case we are here for.
      if (tag === "img" && !(el.naturalWidth > 0)) continue;
      media = describe(el);
      break;
    }
  }
  // Something painted: a background, a border, a shadow. This is what keeps a pure-CSS spinner,
  // a chart drawn with divs, or a colour-block splash from reading as a blank page.
  let painted = null;
  if (!chars && !media) {
    let looked = 0;
    for (const el of els) {
      const tag = el.tagName ? el.tagName.toLowerCase() : "";
      if (tag === "html" || tag === "body" || tag === "head" || tag === "script" || tag === "style") continue;
      const r = el.getBoundingClientRect();
      if (!onPage(r) || r.width * r.height < mediaMinArea) continue;
      // Counted here, after the cheap filters, so a page whose first hundred nodes are <script>
      // still gets its hundred and twenty real looks.
      if (looked++ > styleEvidenceCap) break;
      if (!shown(el)) continue;
      const cs = getComputedStyle(el);
      const opaqueBg = cs.backgroundColor && !/^rgba\(.*,\s*0\)$/.test(cs.backgroundColor) && cs.backgroundColor !== "transparent";
      const hasImage = cs.backgroundImage && cs.backgroundImage !== "none";
      const hasBorder = ["borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth"].some((k) => parseFloat(cs[k]) > 0);
      const hasShadow = cs.boxShadow && cs.boxShadow !== "none";
      if (opaqueBg || hasImage || hasBorder || hasShadow) {
        painted = describe(el);
        break;
      }
    }
  }
  const domChars = ((document.body && document.body.innerText) || "").trim().length;
  const blank = !chars && !media && !painted && !animating && document.readyState === "complete"
    ? {
        check: "blank",
        detail: `nothing rendered on the page: no text a person could scroll to, no image, canvas, SVG or iframe larger than ${Math.round(Math.sqrt(mediaMinArea))}×${Math.round(Math.sqrt(mediaMinArea))}, and nothing painting a background or a border — while the DOM text the proof is checked against is ${domChars} characters long`,
      }
    : null;

  // --- (b) NAKED ---------------------------------------------------------------------------------
  //
  // JUDGEABLE. MEASURED, and it rules out every signal except one: in Chromium a stylesheet that
  // 404s is STILL in document.styleSheets, its <link>.sheet is STILL non-null, and it reports
  // cssRules.length === 0 — which is field-for-field identical to a stylesheet served 200 with an
  // empty body. Counting sheets, or trusting a zero rule count, therefore reddens a build over a
  // placeholder CSS file. Only responseStatus tells the truth, and it is visible only for
  // same-origin (or Timing-Allow-Origin) resources: a cross-origin sheet reports status 0 and
  // transferSize 0 whether it loaded or failed. So a sheet we cannot see the status of is a sheet
  // we do not judge, and if ANY link is unjudgeable the page is never called naked.
  const timing = new Map();
  try {
    for (const e of performance.getEntriesByType("resource")) {
      timing.set(e.name, { status: e.responseStatus, transfer: e.transferSize, size: e.decodedBodySize });
    }
  } catch {
    /* no resource timing: nothing is judgeable, and NAKED cannot fire. That is the safe direction */
  }
  const links = [];
  try {
    for (const l of document.querySelectorAll('link[rel~="stylesheet" i][href]')) {
      if (l.disabled) continue;
      // A print-only stylesheet is not missing from a screen render.
      if (l.media && l.media !== "all" && !window.matchMedia(l.media).matches) continue;
      links.push(l);
    }
  } catch {
    /* an exotic media query: the list stays what it is */
  }
  const failedLinks = [];
  let unjudgeable = 0;
  for (const l of links) {
    const t = timing.get(l.href) || null;
    const status = t && typeof t.status === "number" ? t.status : 0;
    if (!status) {
      unjudgeable += 1;
      continue;
    }
    if (status >= 400) failedLinks.push({ href: l.getAttribute("href") || l.href, status });
  }
  // Anything else supplying CSS — an inline <style>, a constructed sheet, a framework's injected
  // styles — means the page is not naked, whatever its links did.
  let otherRules = 0;
  try {
    for (const s of document.styleSheets) {
      if (s.ownerNode && s.ownerNode.tagName && s.ownerNode.tagName.toLowerCase() === "link") continue;
      try {
        otherRules += s.cssRules.length;
      } catch {
        otherRules += 1;
      }
    }
  } catch {
    /* styleSheets is not enumerable mid-navigation */
  }
  let bodyDefault = false;
  let bodyFont = "";
  if (document.body) {
    const cs = getComputedStyle(document.body);
    bodyFont = cs.fontFamily;
    // The UA stylesheet's own `body { margin: 8px }`, still in force. Every reset — Tailwind's
    // preflight, normalize.css, every framework starter — sets this to 0, so it surviving means no
    // author CSS reached the body. Deliberately NOT the font family: the UA default is "Times" on
    // macOS and "Liberation Serif"/"DejaVu Serif" on the Linux images CI runs on, and a threshold
    // that changes per platform is a threshold that fires per platform.
    const marginDefault = ["marginTop", "marginRight", "marginBottom", "marginLeft"].every((k) => cs[k] === "8px");
    const bgDefault = cs.backgroundColor === "rgba(0, 0, 0, 0)" || cs.backgroundColor === "transparent" || cs.backgroundColor === "rgb(255, 255, 255)";
    bodyDefault = marginDefault && bgDefault;
  }
  const allFailed = links.length > 0 && unjudgeable === 0 && failedLinks.length === links.length;
  // Nothing on the page paints. Identical evidence to BLANK's `painted`, asked of a page that does
  // have text — and it is what rescues a page whose styling lives entirely in style attributes.
  let paintedAnywhere = false;
  if (allFailed && otherRules === 0 && bodyDefault) {
    let looked = 0;
    for (const el of els) {
      const tag = el.tagName ? el.tagName.toLowerCase() : "";
      if (tag === "html" || tag === "body" || tag === "head" || tag === "script" || tag === "style" || tag === "link") continue;
      // Form controls, images and rules carry UA paint of their own; they say nothing about
      // whether the author's CSS arrived.
      if (["button", "input", "select", "textarea", "img", "svg", "canvas", "video", "iframe", "hr", "meter", "progress", "fieldset"].includes(tag)) continue;
      const r = el.getBoundingClientRect();
      if (!intersects(r)) continue;
      if (looked++ > styleEvidenceCap) break;
      if (!shown(el)) continue;
      const cs = getComputedStyle(el);
      const opaqueBg = cs.backgroundColor && !/^rgba\(.*,\s*0\)$/.test(cs.backgroundColor) && cs.backgroundColor !== "transparent";
      const hasImage = cs.backgroundImage && cs.backgroundImage !== "none";
      const hasBorder = ["borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth"].some((k) => parseFloat(cs[k]) > 0);
      const hasShadow = cs.boxShadow && cs.boxShadow !== "none";
      const rounded = parseFloat(cs.borderTopLeftRadius) > 0;
      if (opaqueBg || hasImage || hasBorder || hasShadow || rounded) {
        paintedAnywhere = true;
        break;
      }
    }
  }
  const naked = allFailed && otherRules === 0 && bodyDefault && !paintedAnywhere
    ? {
        check: "naked",
        detail: `the page rendered with no CSS at all: ${failedLinks.length === 1 ? "its stylesheet" : `all ${failedLinks.length} of its stylesheets`} returned an error (${failedLinks
          .slice(0, 3)
          .map((f) => `${f.href} → HTTP ${f.status}`)
          .join(", ")}${failedLinks.length > 3 ? `, and ${failedLinks.length - 3} more` : ""}), nothing else supplies a rule, and the body is still sitting on the browser's default 8px margin in ${bodyFont}`,
      }
    : null;

  // --- (c) OVERLAY -------------------------------------------------------------------------------
  const textOf = (el) => {
    let t = "";
    try {
      t = (el.innerText || "").trim();
    } catch {
      t = "";
    }
    if (el.shadowRoot) {
      // innerText stops at the shadow boundary — which is the entire reason a Next.js overlay is
      // invisible to the proof check and replays green.
      try {
        const st = (el.shadowRoot.textContent || "").trim();
        t = `${st}\n${t}`.trim();
      } catch {
        /* closed or hostile shadow root */
      }
    }
    return t;
  };
  // LINE START, and it is the half of the text test that matters: an error surface LEADS with what
  // went wrong, a page that merely mentions an error does not. A leading HTTP status code is
  // stripped first so "502 Bad Gateway" still opens with "Bad Gateway".
  const matchIn = (text) => {
    const lines = text.slice(0, errorHeadChars).split("\n").slice(0, errorHeadLines);
    for (const raw of lines) {
      const line = raw.trim();
      if (!line) continue;
      const stripped = line.replace(/^\d{3}\s*[-–—:|]?\s*/, "");
      for (const re of patterns) {
        const m = re.exec(line) || (stripped !== line ? re.exec(stripped) : null);
        if (m) return m[0];
      }
    }
    return null;
  };
  // A known framework error surface, matched by identity — including from inside its own shadow
  // root, because that is where Next.js puts the element that actually has the text.
  const isKnownSurface = (el) => {
    let n = el;
    let hops = 0;
    while (n && hops++ < 40) {
      try {
        if (n.nodeType === 1 && n.matches && n.matches(errorSurfaceSelector)) return true;
      } catch {
        /* an invalid selector in an old engine */
      }
      n = n.parentElement || (n.getRootNode && n.getRootNode() && n.getRootNode().host) || null;
    }
    return false;
  };
  // Hit-testing, not z-index arithmetic: whatever the browser says is under the middle of the
  // screen IS what the person is looking at. A candidate that does not hit-test to the top is not
  // covering anything, whatever its rect says.
  const stack = (() => {
    try {
      return document.elementsFromPoint(Math.floor(vw / 2), Math.floor(vh / 2)) || [];
    } catch {
      return [];
    }
  })();
  const topmost = (el) => {
    const i = stack.indexOf(el);
    if (i < 0) {
      // Shadow content is reported through its host at the document level.
      const host = el.getRootNode && el.getRootNode() && el.getRootNode().host;
      return Boolean(host && stack.length && (stack[0] === host || host.contains(stack[0])));
    }
    for (let j = 0; j < i; j++) if (!el.contains(stack[j])) return false;
    return true;
  };

  let overlay = null;
  const vArea = vw * vh;
  for (const el of els) {
    const tag = el.tagName ? el.tagName.toLowerCase() : "";
    if (tag === "html" || tag === "head" || tag === "script" || tag === "style" || tag === "link") continue;
    if (!shown(el)) continue;
    const r = el.getBoundingClientRect();
    if (!intersects(r)) continue;
    const known = isKnownSurface(el);
    if (!known) {
      // POSITIONED. An overlay is, by definition, taken out of flow and laid over the page. A
      // <body> or a <main> that happens to fill the screen is a page, not an overlay — measured,
      // that was a docs page about HTTP 500s being reported as one.
      const pos = getComputedStyle(el).position;
      if (pos !== "fixed" && pos !== "absolute" && pos !== "sticky") continue;
      const covers = (Math.min(r.right, vw) - Math.max(r.left, 0)) * (Math.min(r.bottom, vh) - Math.max(r.top, 0)) >= vArea * viewportCover;
      if (!covers) continue;
    }
    const text = textOf(el);
    if (!text || text.length > overlayTextMax) continue;
    const hit = matchIn(text);
    if (!hit) continue;
    // The last structural test: the browser's own hit test must put this element on top of
    // everything at the centre of the screen, or it is not covering anything.
    if (!known && !topmost(el)) continue;
    overlay = {
      check: "overlay",
      detail: `a full-viewport error surface is covering the page: ${describe(el)} opens with ${JSON.stringify(hit)} — ${JSON.stringify(text.replace(/\s+/g, " ").slice(0, 160))}`,
    };
    break;
  }
  // The whole page IS the error body: an nginx 502, an express 500. It covers nothing (the body's
  // box is the height of two lines) so the coverage test above cannot see it, and it is not
  // positioned either. The structure here is that there is nothing else on the page AT ALL: no
  // author CSS reached the body, there is nothing to click, and the text is shorter than a
  // paragraph. All three, because a human page about an error passes any two of them.
  if (!overlay && document.body) {
    const bodyText = (document.body.innerText || "").trim();
    let controls = 0;
    try {
      controls = document.querySelectorAll("a[href],button,input,select,textarea,form").length;
    } catch {
      controls = 0;
    }
    if (bodyDefault && otherRules <= 1 && bodyText && bodyText.length <= pageErrorTextMax && controls <= pageErrorControlsMax) {
      const hit = matchIn(bodyText);
      if (hit) {
        overlay = {
          check: "overlay",
          detail: `the whole page is a server error body: it opens with ${JSON.stringify(hit)}, has nothing to click and no stylesheet of its own — ${JSON.stringify(bodyText.replace(/\s+/g, " ").slice(0, 160))}`,
        };
      }
    }
  }

  return { blank, naked, overlay };
}

// ---- the guard ----------------------------------------------------------------------------------

/**
 * Run the render guard at verdict time, on the page the run ended on. Returns findings, or [].
 *
 * NOTHING HERE MAY THROW INTO A VERDICT. A guard that breaks — a page mid-navigation, a closed
 * context, a detached frame, a page that redefined Array.prototype — is a guard that found
 * nothing, because "our render check crashed" turning somebody's real PASS into a red X is a
 * worse bug than every false green it was built to catch. Every path returns an array.
 */
export async function auditRender(page, { enabled = true, wait = RE_MEASURE_MS } = {}) {
  if (!enabled) return [];
  const args = {
    mediaMinArea: MEDIA_MIN_AREA,
    minOpacity: MIN_OPACITY,
    viewportCover: VIEWPORT_COVER,
    overlayTextMax: OVERLAY_TEXT_MAX,
    errorHeadLines: ERROR_HEAD_LINES,
    errorHeadChars: ERROR_HEAD_CHARS,
    pageErrorTextMax: PAGE_ERROR_TEXT_MAX,
    pageErrorControlsMax: PAGE_ERROR_CONTROLS_MAX,
    elementCap: ELEMENT_CAP,
    styleEvidenceCap: STYLE_EVIDENCE_CAP,
    errorText: ERROR_TEXT.map((re) => ({ source: re.source, flags: re.flags })),
    errorSurfaceSelector: ERROR_SURFACE_SELECTOR,
  };
  try {
    const first = await page.evaluate(inPage, args);
    if (!first || typeof first !== "object") return [];
    const findings = [];
    // BLANK IS THE ONLY ONE MEASURED TWICE. The other two cannot be a timing artefact: a stylesheet
    // that returned 404 will still have returned 404 in 700ms, and an error overlay does not fade
    // out on its own. Blank can, and does — see RE_MEASURE_MS for the fixture that proved it.
    if (first.blank) {
      await new Promise((r) => setTimeout(r, wait));
      const second = await page.evaluate(inPage, args).catch(() => null);
      if (second && second.blank) findings.push(second.blank);
    }
    if (first.naked) findings.push(first.naked);
    if (first.overlay) findings.push(first.overlay);
    return findings;
  } catch {
    return [];
  }
}

/**
 * The gate, and the ONLY place a render finding may touch a verdict.
 *
 * The caller must have decided the run would otherwise PASS — that is the whole contract, and it
 * is enforced at the two call sites rather than here, because only they know the verdict. The
 * reason names what was seen and how to switch the guard off, in that order: someone reading a
 * red build at 2am needs the observation first and the escape hatch second.
 */
export function renderFailure(findings = []) {
  if (!Array.isArray(findings) || !findings.length) return "";
  const more = findings.length > 1 ? ` (and ${findings.length - 1} more render finding${findings.length > 2 ? "s" : ""} below.)` : "";
  return (
    `The steps all worked and the page still says what it should — but ${findings[0].detail}.${more} ` +
    `A person opening this page would not call it working, so this is a failure, not a pass. ` +
    `--no-render-check turns this guard off.`
  );
}

/** The dim terminal note: one line per finding, capped, count never hidden. */
export function renderNoteLines(findings = [], cap = 3) {
  if (!Array.isArray(findings)) return [];
  const lines = findings.slice(0, cap).map((f) => `render: ${f.detail}`);
  if (findings.length > cap) lines.push(`render: … and ${findings.length - cap} more`);
  return lines;
}
