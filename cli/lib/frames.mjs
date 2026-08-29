// EMBEDDED CONTENT: iframes and shadow DOM.
//
// WHAT WAS MEASURED, BEFORE ANY OF THIS EXISTED.
//
// Six fixtures, real Chromium, our own `perceive()`. `page.locator("body").ariaSnapshot()` returns
// this for a page whose only real control is inside an iframe:
//
//     - heading "Checkout" [level=1]
//     - button "Outer button"
//     - iframe
//
// One bare word. No name, no URL, and nothing inside it. `flatten()` then drops that line, because
// "iframe" is not an actionable role and the line carries no name — so the agent was shown a page
// with two elements and told, truthfully as far as it knew, that those were all of them. Clicking
// the control inside spent the full locator timeout and failed.
//
//   (f) normal page ............ element listed, click reached it
//   (a) same-origin iframe ..... INVISIBLE, click timed out
//   (b) cross-origin iframe .... INVISIBLE, click timed out
//   (c) open shadow DOM ........ already worked; Playwright's selectors pierce open roots
//   (d) closed shadow DOM ...... INVISIBLE, and genuinely unreadable
//   (e) iframe inside iframe ... INVISIBLE, click timed out
//
// That is Stripe Elements, Stripe Checkout, reCAPTCHA, Intercom, Calendly, YouTube and every
// payment form any of our customers has ever embedded. And the failure mode is the worst one this
// product has: not a crash, but a CONFIDENT LIE. The agent reports "the Pay button does not exist
// on the checkout page" about a checkout that works, and someone goes looking for a bug that was
// never there. A test tool that invents defects is removed faster than one that misses them.
//
// So, in order of how much they are worth:
//
//   1. SAY SO. A frame that is present is named, even when its contents could not be read, and a
//      closed shadow root is named as unreadable. The agent may then fail honestly — "there is a
//      frame here I could not read" — instead of asserting an absence it never established.
//   2. READ IT. Playwright reads a frame's accessibility tree the same way at any origin
//      (`frame.locator("body").ariaSnapshot()` returned the full tree for the cross-origin case),
//      and `frameLocator()` clicks into it, chained for nesting. Measured: same-origin, cross-
//      origin, nested and anonymous frames all clicked.
//   3. KEEP IT REPLAYABLE. A step inside a frame records the frame as a chain of CSS selectors, so
//      the second run costs nothing, exactly like every other step.
//
// WHY A CLOSED SHADOW ROOT IS DETECTED WITH CDP AND NOT A HEURISTIC. The obvious cheap test is "an
// element with no children and no text that still has a box on the screen". Measured against a
// perfectly ordinary page, that flags a 2px rule, a spacer, an avatar circle, an <img>, a <canvas>
// and an <input> — six false alarms about hidden content on a page that has none. Chromium's
// `DOM.getDocument({pierce:true})` reports the real answer, `shadowRootType: "closed"`, in 1-4ms,
// and reports nothing on a page with no closed roots. A warning that cries wolf is worse than
// silence, so we use the exact one and stay quiet everywhere it is unavailable.

/** Frames read per perception. An ad-heavy page can carry dozens; each one is a round trip. */
export const FRAME_CAP = 12;
/** Elements taken from frames per perception, on top of the main document's own cap. */
export const FRAME_ELEMENT_CAP = 60;
/** A frame whose document is wedged must not hang the whole run. */
export const FRAME_READ_MS = 3000;
/** Frame and shadow text appended for the proof check. The main document's text is never capped. */
export const EXTRA_TEXT_CAP = 20_000;

/**
 * Is this attribute value different on the next run?
 *
 * Stripe names its frames `__privateStripeFrame1234`, reCAPTCHA names them `a-9f3c1d20e8`. A
 * selector built from either is a recording that goes stale the moment the page reloads — the same
 * defect as a proof quoting an order number, and it would turn every embedded checkout into a full
 * agent re-run on every pull request. Titles are written for humans and survive; generated ids do
 * not, and we would rather fall back to position than record a selector we know expires.
 */
export function unstable(v) {
  const s = String(v);
  if (/\d{4,}/.test(s)) return true;
  return /[0-9a-f]{8,}/i.test(s) && /\d/.test(s);
}

/** A value we cannot put inside a CSS attribute selector without an escaping bug. */
const unquotable = (v) => /["\\\n\r\t]/.test(String(v));

const usable = (v) => {
  const s = String(v ?? "").trim();
  return s && !unquotable(s) && !unstable(s) ? s : "";
};

/**
 * A CSS selector for one frame element, from its own attributes. Pure, so the preference order is
 * testable without a browser.
 *
 * `title` first on purpose: it is what the frame is CALLED, it is what a screen reader announces,
 * and across every embed we looked at it is the attribute the vendor keeps stable while randomising
 * `name`. Then the author's own `id` and `name`, then position, which always works and says
 * nothing.
 */
export function frameSelector({ tag = "iframe", id = "", name = "", title = "", index = -1 } = {}) {
  const el = String(tag || "iframe").toLowerCase().replace(/[^a-z-]/g, "") || "iframe";
  for (const [attr, raw] of [["title", title], ["id", id], ["name", name]]) {
    const v = usable(raw);
    if (v) return `${el}[${attr}="${v}"]`;
  }
  const n = Number.isInteger(index) && index >= 0 ? index : 0;
  return `${el} >> nth=${n}`;
}

/**
 * The words for a frame in a step label, recovered from the selector.
 *
 * The label the agent writes has to be true: "click button Pay" reads as a click on the page, and
 * someone debugging a checkout needs to know it happened inside the payment frame. Deriving it
 * from the selector rather than storing a second field keeps the recording tight and keeps the
 * label from ever disagreeing with what was actually clicked.
 */
export function frameLabel(chain) {
  const c = Array.isArray(chain) ? chain : [];
  if (!c.length) return "";
  const last = String(c[c.length - 1]);
  const m = /^[a-z-]+\[(?:title|id|name)="([^"]*)"\]$/.exec(last);
  return m ? m[1] : last;
}

/**
 * Point a locator at the frame a step belongs to. An empty chain is the page itself, which is what
 * every recording made before frames existed carries — so this is also the backward-compatibility
 * story: no `frame` field, no frame, byte-identical behaviour.
 */
export function inFrame(page, chain) {
  const c = Array.isArray(chain) ? chain.filter((s) => typeof s === "string" && s) : [];
  let root = page;
  for (const sel of c) root = root.frameLocator(sel);
  return root;
}

/** Outermost-first chain of selectors for a frame, given each frame's own selector. */
function chainFor(frame, own) {
  const chain = [];
  for (let f = frame; f && own.has(f); f = f.parentFrame()) chain.unshift(own.get(f));
  return chain;
}

/**
 * Read every child frame's accessibility tree.
 *
 * `flatten` is passed in rather than imported: this module must not import lib/test.mjs, which
 * imports it. It also means the capping and attribution below can be tested with a stub.
 *
 * Nothing here throws. Perception is how the agent sees; a frame that navigated away mid-read must
 * cost us that frame, never the verdict.
 */
export async function readFrames(page, flatten, opts = {}) {
  const {
    frameCap = FRAME_CAP,
    elementCap = FRAME_ELEMENT_CAP,
    timeout = FRAME_READ_MS,
    startRef = 0,
  } = opts;

  let children = [];
  let main = null;
  try {
    main = page.mainFrame();
    children = page.frames().filter((f) => f !== main && typeof f.parentFrame === "function" && f.parentFrame());
  } catch {
    return { elements: [], frames: [], skipped: 0, truncated: 0 };
  }
  if (!children.length) return { elements: [], frames: [], skipped: 0, truncated: 0 };

  // Every frame gets a selector first, so a nested frame can name its ancestors.
  const own = new Map();
  const named = new Map();
  for (const f of children) {
    let attrs = null;
    try {
      const h = await f.frameElement();
      try {
        attrs = await h.evaluate((el) => ({
          tag: el.tagName,
          id: el.getAttribute("id") || "",
          name: el.getAttribute("name") || "",
          title: el.getAttribute("title") || "",
          index: Array.prototype.indexOf.call(document.querySelectorAll(el.tagName), el),
        }));
      } finally {
        await h.dispose().catch(() => {});
      }
    } catch {
      /* detached, or a frame we are not allowed to reach from here */
    }
    if (!attrs) continue;
    own.set(f, frameSelector(attrs));
    named.set(f, String(attrs.title || attrs.name || attrs.id || "").trim());
  }

  const frames = [];
  const elements = [];
  let ref = startRef;
  let truncated = 0;
  let skipped = 0;
  // The cap counts frames we READ. Counting the unaddressable ones too would let a page whose
  // first dozen frames are detached ad slots stop us before the payment frame.
  let readCount = 0;

  for (const f of children) {
    const chain = own.has(f) ? chainFor(f, own) : [];
    let url = "";
    try {
      url = f.url();
    } catch {
      /* a frame can detach between the list and the read */
    }
    const label = named.get(f) || frameLabel(chain) || url || "frame";

    // UNADDRESSABLE IS STILL REPORTED. A frame whose element we could not reach cannot be clicked
    // into, but the agent must still be told it is there, or it goes back to concluding a control
    // is absent from a page it only half read.
    if (!own.has(f) || readCount >= frameCap) {
      skipped++;
      frames.push({ label, url, chain, read: false, count: 0 });
      continue;
    }

    let aria = "";
    try {
      aria = await f.locator("body").ariaSnapshot({ timeout });
    } catch {
      skipped++;
      frames.push({ label, url, chain, read: false, count: 0 });
      continue;
    }

    readCount++;
    const flat = flatten(aria);
    let count = 0;
    for (const el of flat.elements || []) {
      if (elements.length >= elementCap) {
        truncated++;
        continue;
      }
      elements.push({ ...el, ref: `e${++ref}`, frame: chain, frameName: label });
      count++;
    }
    truncated += flat.truncated || 0;
    frames.push({ label, url, chain, read: true, count });
  }

  return { elements, frames, skipped, truncated };
}

/**
 * Closed shadow roots on the page, as the tag names of their hosts.
 *
 * Chromium only, by design — this is the one place the runner reaches past Playwright's API, and
 * the alternative is guessing. On any browser or context without CDP it returns nothing at all,
 * which reads as "no closed roots found" and is exactly as honest as we can be there.
 */
export async function closedShadowRoots(page) {
  let cdp = null;
  try {
    cdp = await page.context().newCDPSession(page);
    const { root } = await cdp.send("DOM.getDocument", { depth: -1, pierce: true });
    const hosts = [];
    const walk = (n) => {
      if (!n || typeof n !== "object") return;
      for (const r of n.shadowRoots || []) {
        // "user-agent" is Chromium's own plumbing inside every <input> and <img>. Reporting those
        // would put a scary sentence about unreadable content on literally every page.
        if (r.shadowRootType === "closed") hosts.push(String(n.nodeName || "").toLowerCase());
        walk(r);
      }
      for (const c of n.children || []) walk(c);
    };
    walk(root);
    return hosts;
  } catch {
    return [];
  } finally {
    try {
      await cdp?.detach();
    } catch {
      /* the page may already be closed; a detach failure is not a verdict */
    }
  }
}

/** Runs inside the page: the main document's text, then every OPEN shadow root's. */
function textInPage() {
  const main = document.body ? document.body.innerText || "" : "";
  const shadow = [];
  let hosts = [];
  try {
    hosts = Array.prototype.slice.call(document.querySelectorAll("*"));
  } catch {
    hosts = [];
  }
  for (const el of hosts) {
    const r = el.shadowRoot;
    if (!r) continue;
    for (const child of Array.prototype.slice.call(r.children)) {
      const t = child.innerText || child.textContent || "";
      if (t.trim()) shadow.push(t);
    }
  }
  return { main, shadow };
}

/**
 * EVERY WORD A PERSON WOULD READ ON THIS PAGE, and the reason it is not `document.body.innerText`.
 *
 * Measured: on a page whose confirmation is inside an iframe, `document.body.innerText` is the
 * single word "Checkout" — "Order placed. Thank you." lives only in the frame. Chromium's innerText
 * walks the DOM tree, not the flat tree, so an open shadow root's text is missing too.
 *
 * That matters because of what reads this. `replay()` checks `plan.proof` against the page text to
 * decide whether a recorded flow still does the right thing. Once perception can see into frames,
 * the agent will quote its proof FROM a frame — "Payment received" out of Stripe's — and every
 * later replay would fail to find it, report `outcome-changed`, and wake the agent at full model
 * price on every pull request forever. That is the rebase() cost explosion again, and shipping the
 * frame fix without this would have built it.
 *
 * The main document's text comes FIRST and is never truncated, so this is strictly additive: any
 * proof that matched before still matches, byte for byte. Only the appended part is capped.
 *
 * THIS THROWS when the main document cannot be read, and every caller decides what that means for
 * itself — because they do not agree. A replay treats an unreadable page as "the proof is not
 * there"; the recorder treats it as "we learned nothing, write the recording anyway". Swallowing
 * the failure into "" here would silently flip the second one into dropping every recording made
 * against a page that navigated at the wrong moment.
 */
export async function visibleText(page) {
  const r = await page.evaluate(textInPage);
  const main = String(r?.main ?? "");
  const extra = (r?.shadow ?? []).map(String);
  try {
    const kids = page.frames().filter((f) => f !== page.mainFrame());
    for (const f of kids.slice(0, FRAME_CAP)) {
      const t = await f.evaluate(() => (document.body ? document.body.innerText || "" : "")).catch(() => "");
      if (String(t).trim()) extra.push(String(t));
    }
  } catch {
    /* frames are a bonus on top of the main document, never a reason to return nothing */
  }
  const tail = extra.join("\n").slice(0, EXTRA_TEXT_CAP);
  return tail ? `${main}\n${tail}` : main;
}

/**
 * The sentences perception prints about embedded content.
 *
 * The last one is the whole point of this file. A model that cannot see a control has two
 * explanations available to it — "it is not there" and "I could not look there" — and it will pick
 * the first unless we hand it the second in writing.
 */
export function embeddedNotes({ frames = [], closed = [] } = {}) {
  const out = [];
  const read = frames.filter((f) => f.read);
  const unread = frames.filter((f) => !f.read);
  for (const f of read) {
    const where = f.url ? ` (${f.url})` : "";
    out.push(
      f.count
        ? `  frame ${JSON.stringify(f.label)}${where} — its ${f.count} element${f.count === 1 ? " is" : "s are"} listed above, marked with the frame`
        : `  frame ${JSON.stringify(f.label)}${where} — read, no interactive elements inside it`,
    );
  }
  if (unread.length) {
    const names = unread.slice(0, 4).map((f) => JSON.stringify(f.label)).join(", ");
    out.push(
      `  ${unread.length} frame${unread.length === 1 ? "" : "s"} on this page could NOT be read (${names}${unread.length > 4 ? ", …" : ""}). ` +
        `Anything inside them is missing from the list above.`,
    );
  }
  if (closed.length) {
    const tags = [...new Set(closed)].slice(0, 4).map((t) => `<${t}>`).join(", ");
    out.push(
      `  ${closed.length} closed shadow root${closed.length === 1 ? "" : "s"} (${tags}). A closed shadow root cannot be read by ` +
        `any tool, including a browser's own accessibility tree, so its contents are not listed above.`,
    );
  }
  if (out.length) {
    out.push(
      `  Nothing above is evidence that a control does not exist. If the test needs something you cannot see, say that it is ` +
        `inside embedded content you could not read — do not report it as missing from the application.`,
    );
    out.unshift("EMBEDDED CONTENT:");
    out.push("");
  }
  return out;
}
