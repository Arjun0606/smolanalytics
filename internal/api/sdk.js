/* smolanalytics browser SDK — drop-in, dependency-free, ~7KB gzipped.
 *
 *   <script src="https://YOUR_HOST/sdk.js"></script>
 *   <script>
 *     smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });
 *     smolanalytics.track("signup", { plan: "pro" });
 *   </script>
 *
 * Anonymous id is persisted in localStorage; identify() promotes it to a real
 * user id. Events are batched and flushed on a timer, on 20 queued, and on page
 * unload (keepalive) so nothing is lost.
 */
(function () {
  var host = "";
  var key = "";
  var queue = [];
  var did = null;
  var bid = null; // stable bucketing key for flags/experiments; identify() never touches it
  var anon = false; // cookieless mode: nothing stored on the device, no banner needed
  var envName = "production"; // see detectEnv, or whatever init({env}) says

  // Which environment is this page? The dashboard hides everything that is not "production"
  // by default, so getting this wrong in either direction is expensive: call a preview
  // "production" and every branch deploy and CI run pollutes the real numbers, call a real
  // site "development" and its traffic silently vanishes from the dashboard.
  //
  // So this only claims non-production for hosts that CANNOT be a production site. It is
  // deliberately conservative: under-detecting leaves someone with slightly noisy numbers
  // they can filter, over-detecting makes their actual traffic disappear, and only one of
  // those is recoverable by the person looking at the screen.
  //
  // Notably absent: a bare *.vercel.app rule. Plenty of real production sites are served
  // from vercel.app with no custom domain, so treating the domain as preview would hide
  // their live traffic. Vercel exposes NEXT_PUBLIC_VERCEL_ENV at build time for exactly this
  // — pass it through init({ env }) and it is authoritative.
  function detectEnv(h) {
    h = (h || "").toLowerCase();
    // loopback and private ranges — a LAN address is never a production website
    if (/^(localhost|127\.|0\.0\.0\.0|\[::1\]|::1|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(h)) return "development";
    // reserved dev TLDs (RFC 6761 / 6762) and the conventional .lan
    if (/\.(local|localhost|test|example|invalid|lan)$/.test(h)) return "development";
    // dev tunnels: the host only exists because someone is sharing a local server
    if (/(\.ngrok\.io|\.ngrok-free\.app|\.ngrok\.app|\.loca\.lt|\.trycloudflare\.com|\.lhr\.life|\.serveo\.net|\.telebit\.io)$/.test(h)) return "development";
    // Netlify deploy previews and branch deploys. The double dash is Netlify's own separator
    // and cannot appear in a normal site name, so this is unambiguous.
    if (/--.+\.netlify\.app$/.test(h)) return "preview";
    // explicit environment subdomains, e.g. staging.acme.com, preview.acme.dev
    if (/^(staging|preview|dev|development|test|qa|uat|sandbox|canary)\./.test(h)) return "preview";
    return "production";
  }
  var timer = null;
  var flagCache = {};
  var flagMeasured = {}; // keys the server marked "measured" → log an exposure on read
  var flagExposed = {}; // in-memory half of the per-session exposure dedupe (see alreadyExposed)
  var flagsLoaded = false;
  var flagListeners = [];
  var surveyShownThisLoad = false; // at most one survey popover per page load
  var captured = false; // autocapture wired once, even if the snippet loads twice
  var inited = false; // init() is idempotent: a second call would double every number
  var warnedAuth = false; // warn once on a bad key, don't spam the console
  var warnedAnonMeasured = false; // warn once that cookieless bucketing can't carry an experiment
  var lifecycleBound = false; // flush-on-unload listeners bound once, even on re-init
  // engagement: accumulate time the page is visible AND focused, reported as a
  // $engagement event when the visitor leaves the page (route change or unload).
  // This is what makes bounce/duration honest — a tab open in the background is
  // not engagement.
  var engStart = null;
  var engAccum = 0;
  var engPath = null;

  function uid() {
    return "a-" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  }

  // The bucketing key for feature flags and experiments. Written once per browser and NEVER
  // rewritten by identify(), which is the entire point: distinct_id changes at login, and
  // bucketing on something that changes at login silently reassigns people mid-experiment —
  // they see one variant before signing up and the other after. Kept under its own key so the
  // two can never be confused.
  function bucketId() {
    if (bid) return bid;
    // Cookieless mode promises nothing is written to the visitor's device, and a persisted
    // bucket key is precisely the identifier that promise is about — so it lives for one page
    // load only. Flags still resolve (they used to be skipped entirely, see fetchFlags), and the
    // share of visitors in each variant stays honest, but one visitor can land in a different
    // arm on their next page load. flag() warns the first time a MEASURED flag is read this way,
    // because that is the only case where the instability corrupts a result rather than a pixel.
    if (anon) {
      bid = uid();
      return bid;
    }
    try {
      bid = localStorage.getItem("smol_bucket_id");
      if (!bid) {
        bid = uid();
        localStorage.setItem("smol_bucket_id", bid);
      }
    } catch (e) {
      // Private mode or storage disabled. Fall back to the distinct id: assignment is then only
      // as stable as that is, which is worse, but never wrong within a single page view.
      bid = bid || distinctId();
    }
    return bid;
  }

  function distinctId() {
    if (anon) return "$anon"; // sentinel: the server derives a daily-rotating visitor id
    if (did) return did;
    try {
      did = localStorage.getItem("smol_did");
      if (!did) {
        did = uid();
        localStorage.setItem("smol_did", did);
      }
    } catch (e) {
      did = did || uid();
    }
    return did;
  }

  // utm + device context for web analytics (top sources, campaigns, device split).
  // Computed once per page URL — cheap, no external calls, no fingerprinting.
  function webContext() {
    var ctx = {};
    try {
      var q = new URLSearchParams(location.search);
      ["utm_source", "utm_medium", "utm_campaign"].forEach(function (k) {
        var v = q.get(k);
        if (v) ctx[k] = v.slice(0, 80);
      });
    } catch (e) {}
    var ua = navigator.userAgent || "";
    ctx.device = /iPad|Tablet|PlayBook|Silk|Android(?!.*Mobile)/.test(ua)
      ? "tablet"
      : /Mobi|Android|iPhone|iPod/.test(ua)
        ? "mobile"
        : "desktop";
    try {
      if (window.screen && screen.width) {
        ctx.screen_w = screen.width;
        ctx.screen_h = screen.height;
      }
      if (window.innerWidth) {
        ctx.viewport_w = window.innerWidth;
        ctx.viewport_h = window.innerHeight;
      }
    } catch (e) {}
    return ctx;
  }

  // session + first-touch: a session id ties a visit's events together (path analysis),
  // rotating after 30 minutes of inactivity; the initial referrer + utm are captured once
  // per session so you can attribute a conversion to how the visit STARTED. Skipped in
  // cookieless mode (nothing stored on the device).
  var sess = null;
  function ensureSession() {
    if (anon) return;
    var now = Date.now();
    if (sess === null) {
      try {
        sess = JSON.parse(localStorage.getItem("smol_session"));
      } catch (e) {
        sess = null;
      }
    }
    if (!sess || !sess.id || now - (sess.ts || 0) > 30 * 60 * 1000) {
      var utm = {};
      try {
        var q = new URLSearchParams(location.search);
        var u = q.get("utm_source");
        if (u) utm.utm_source = u.slice(0, 80);
      } catch (e) {}
      sess = { id: "s-" + Math.random().toString(36).slice(2) + now.toString(36), ref: (document.referrer || "").slice(0, 200), utm: utm, ts: now };
      persistSession();
    }
    sess.ts = now;
  }
  function persistSession() {
    if (anon || !sess) return;
    try {
      localStorage.setItem("smol_session", JSON.stringify(sess));
    } catch (e) {}
  }

  // A keepalive request whose body exceeds 64KiB fails immediately with a TypeError — per the
  // Fetch spec, and Chrome enforces the same ceiling as a shared per-origin inflight quota. It
  // never reaches the network, so there is no HTTP response and the warn path below never runs.
  //
  // flush() used to splice the WHOLE queue into ONE body and set keepalive on EVERY flush, not
  // just the unload one. Twenty autocaptured $click events carrying $elements chains is already
  // 20-90KB, and any 5xx/offline period requeues until the 1000 cap — so the 3s timer re-sent the
  // identical oversized body forever, .catch(requeue) put it straight back, and every event on
  // that page was lost with nothing in the console. Hence: chunk by count AND by serialized
  // bytes, and ask for keepalive only where the request must outlive the document.
  var MAX_BATCH = 50;
  var MAX_BODY = 60000; // under the 64KiB ceiling, leaving room for headers

  function flush(final) {
    if (!queue.length) return;
    var batch = [], bytes = 2; // "[" + "]"
    while (queue.length && batch.length < MAX_BATCH) {
      var enc = "";
      try { enc = JSON.stringify(queue[0]); } catch (e) { queue.shift(); continue; }
      // a single event larger than the whole ceiling can never be delivered — drop it rather
      // than wedge every event behind it for the rest of the visit
      if (batch.length === 0 && enc.length + 2 > MAX_BODY) { queue.shift(); continue; }
      if (bytes + enc.length + 1 > MAX_BODY) break;
      bytes += enc.length + 1;
      batch.push(queue.shift());
    }
    if (!batch.length) return;
    var headers = { "Content-Type": "application/json" };
    if (key) headers["Authorization"] = "Bearer " + key;
    // on a network failure, put the batch back so the next flush retries instead of
    // silently dropping events. Bound the queue to the newest 1000 so a long outage can't
    // grow it without limit (the cap must be applied AFTER concat — the queue is ~empty
    // here post-splice, so checking its length first never triggers).
    function requeue() {
      queue = batch.concat(queue);
      if (queue.length > 1000) queue = queue.slice(queue.length - 1000);
    }
    try {
      fetch(host + "/v1/events", {
        method: "POST",
        headers: headers,
        body: JSON.stringify(batch),
        keepalive: !!final,
        mode: "cors",
      }).then(function (r) {
        if (!r.ok && r.status >= 500) requeue();
        else if (!r.ok && !warnedAuth) {
          warnedAuth = true;
          // a typo'd key (401/403) or a wrong host (404/SPA-catch) drops every event
          // silently otherwise — say so once so the builder isn't debugging a blank dashboard
          if (window.console) console.warn("smolanalytics: events rejected (" + r.status + ") — check your host and write key");
        }
      }).catch(requeue);
    } catch (e) {
      requeue();
    }
  }

  function engActive() {
    return document.visibilityState === "visible" && (typeof document.hasFocus !== "function" || document.hasFocus());
  }
  function engTick() {
    if (engActive()) {
      if (engStart === null) engStart = Date.now();
    } else if (engStart !== null) {
      engAccum += Date.now() - engStart;
      engStart = null;
    }
  }
  function engReport() {
    if (engStart !== null) {
      engAccum += Date.now() - engStart;
      engStart = engActive() ? Date.now() : null;
    }
    var ms = Math.round(engAccum);
    engAccum = 0;
    // ignore sub-second blips and absurd values (clock jumps, day-long zombie tabs)
    if (engPath && ms >= 1000 && ms < 4 * 60 * 60 * 1000) {
      var p = { path: engPath, engaged_ms: ms };
      if (maxScroll > 0) p.scroll_pct = maxScroll; // how far down the page they got
      enqueue("$engagement", p);
    }
    engPath = location.pathname;
  }

  // Exposure dedupe has to outlive the page or "exactly one $feature_flag_called per visitor per
  // session" — what the SDK comment, the dashboard note and the create-flag copy all promise — is
  // simply false. flagExposed is IIFE state, so it reset on every full page load and a 5-page
  // visit re-emitted the same exposure 5 times, inflating the raw event count and the billed plan
  // meter by roughly pageviews-per-session. (The A/B numbers were never wrong: Measure and
  // firstExposures both collapse a user to their EARLIEST exposure. The meter was.)
  //
  // Keyed on the same session id enqueue() stamps, so a genuinely new session re-exposes, which
  // is the documented behaviour. Cookieless mode has no session and writes no storage, so it
  // keeps the per-page-load fallback — the console already warns that cookieless bucketing cannot
  // carry an experiment.
  //
  // localStorage rather than sessionStorage, which is the obvious choice and the wrong one:
  // sessionStorage is per-TAB, so opening the site in a second tab is a second exposure and the
  // copy would still be a lie. The 30-minute session id in localStorage is the thing the promise
  // is actually about.
  var EXPOSED_KEY = "smol_flag_exposed";
  function exposureRecord() {
    if (anon || !sess || !sess.id) return null;
    try {
      var rec = JSON.parse(localStorage.getItem(EXPOSED_KEY) || "null");
      if (!rec || rec.sid !== sess.id || !rec.k) rec = { sid: sess.id, k: {} };
      return rec;
    } catch (e) {
      return null;
    }
  }
  // flag:variant, inside a record stamped with the session id — i.e. flag:variant:session_id.
  //
  // The variant belongs IN the key. Deduping on the flag name alone would swallow the second
  // exposure of a visitor whose variant CHANGED mid-session — a weight edit, identity stitching
  // collapsing two ids, or an unstable bucket key — and a multi-variant exposure is precisely
  // what the experiment-health check exists to catch. Deduped away, it looks like a clean test.
  function exposureKey(name, variant) {
    return name + ":" + variant;
  }
  function alreadyExposed(k) {
    var rec = exposureRecord();
    return rec ? !!rec.k[k] : !!flagExposed[k];
  }
  function markExposed(k) {
    flagExposed[k] = true;
    var rec = exposureRecord();
    if (!rec) return;
    rec.k[k] = 1;
    try { localStorage.setItem(EXPOSED_KEY, JSON.stringify(rec)); } catch (e) {}
  }

  function enqueue(name, props) {
    props = props || {};
    // site + env stamped on EVERY event, so multi-site filtering and the
    // exclude-dev-traffic default work across all reports with zero setup.
    // Overridable per event; env also settable at init ({ env: "staging" }).
    if (props.site === undefined) props.site = location.hostname;
    if (props.env === undefined) props.env = envName;
    ensureSession();
    if (sess) {
      if (props.session_id === undefined) props.session_id = sess.id;
      if (sess.ref && props.initial_referrer === undefined) props.initial_referrer = sess.ref;
      if (sess.utm && sess.utm.utm_source && props.initial_utm_source === undefined) props.initial_utm_source = sess.utm.utm_source;
    }
    queue.push({
      // A STABLE id, minted once at enqueue and carried through every retry.
      //
      // Without it the ingest contract's "a retried request never double-counts" was false for
      // the primary client: flush() requeues the whole batch on any network failure, and the
      // server mints a fresh random id per delivery, so a batch that actually landed before the
      // connection dropped was counted twice with no way for either side to tell. The id is the
      // only thing that makes that request idempotent, and the browser is the one place that
      // knows the retry is a retry.
      id: uid(),
      name: name,
      distinct_id: distinctId(),
      timestamp: new Date().toISOString(),
      properties: props,
    });
    if (queue.length >= 20) flush();
  }

  // --- rich autocapture helpers ---

  // classesOf reads a className whether it's a string or an SVGAnimatedString.
  function classesOf(el) {
    var c = el.className;
    if (c && c.baseVal !== undefined) c = c.baseVal; // SVG elements
    return (c && (c + "").trim()) || "";
  }

  // labelOf returns what a control is CALLED, never what it holds.
  //
  // The old chain was `innerText || value`, and isClickable deliberately admits
  // input[type=checkbox|radio|submit|button] — an <input> has no innerText, so the fallback
  // always fired. `<input type="checkbox" name="invite" value="jane@corp.com">`, a plain
  // multi-select over records, shipped that address as $click.text AND again inside
  // $elements[0].text, stored raw and visible in recent_events and every session timeline. The
  // operator never opted into collecting it. Only submit/button expose a value that is the
  // rendered caption (author-set copy, not the visitor's data), so that is the one case where
  // the value is a label at all; for everything else the caption lives in aria-label or the
  // associated <label>.
  function labelOf(el) {
    var t = ((el.innerText || "") + "").trim();
    if (t) return t;
    var tag = ((el.tagName || "") + "").toLowerCase();
    if (tag !== "input") return "";
    var type = ((el.type || "") + "").toLowerCase();
    if (type === "submit" || type === "button" || type === "reset") {
      var caption = el.value; // the button's rendered text, written by the site author
      if (caption) return (caption + "").trim();
      return "";
    }
    try {
      var aria = el.getAttribute && el.getAttribute("aria-label");
      if (aria) return (aria + "").trim();
    } catch (e) {}
    try {
      if (el.labels && el.labels.length) {
        var lt = ((el.labels[0].innerText || "") + "").trim();
        if (lt) return lt;
      }
    } catch (e) {}
    return "";
  }

  // safeHref keeps WHICH link was clicked and drops the secret inside it.
  //
  // Password-reset, magic-link, invite and OAuth callback URLs carry their token in the query
  // string, and mailto:/tel:/sms: put a person's address or number in the href itself — all of
  // it used to land verbatim in $click.href and $elements[*].href, then in the event store,
  // recent_events and every shared session timeline. Origin + path identifies the link; the
  // rest is either a credential or a person.
  function safeHref(v) {
    v = (v + "");
    var cut = v.search(/[?#]/);
    if (cut >= 0) v = v.slice(0, cut);
    var scheme = (v.match(/^([a-z][a-z0-9+.\-]*):/i) || [])[1];
    if (scheme) {
      var s = scheme.toLowerCase();
      if (s !== "http" && s !== "https") v = s + ":"; // mailto:, tel:, sms:, javascript:
    }
    return v.slice(0, 300);
  }

  // elemDesc snapshots ONE element: enough to re-identify it later for a retroactive
  // named event, and for a coding agent to map an element to a business event. Metadata
  // only, never input values.
  function elemDesc(el) {
    var d = { tag: el.tagName.toLowerCase() };
    if (el.id) d.id = (el.id + "").slice(0, 80);
    var cls = classesOf(el);
    if (cls) d.classes = cls.slice(0, 160);
    if (el.getAttribute) {
      var role = el.getAttribute("role"); if (role) d.role = role.slice(0, 40);
      var aria = el.getAttribute("aria-label"); if (aria) d.aria_label = aria.slice(0, 80);
      var name = el.getAttribute("name"); if (name) d.name = name.slice(0, 80);
    }
    if (el.href) d.href = safeHref(el.href);
    // data-sa-* ONLY — the attributes namespaced for this SDK.
    //
    // This used to send EVERY data-* attribute, on the clicked element and four ancestors,
    // under a comment calling it "opt-in element identity". It was opt-OUT: apps routinely
    // carry data-user-id, data-email, data-price, data-account on the very elements people
    // click, and all of it left the browser on every click of every visitor. Reading only our
    // own namespace makes the comment true and makes the opt-in real.
    try {
      if (el.dataset) {
        for (var dk in el.dataset) {
          if (dk.indexOf("sa") !== 0) continue; // data-sa-* → dataset keys begin "sa"
          var dv = el.dataset[dk];
          if (dv) (d.data = d.data || {})[dk] = (dv + "").slice(0, 80);
        }
      }
    } catch (e) {}
    // The element's LABEL, never its contents — see labelOf for what that used to leak.
    var t = labelOf(el);
    if (t) d.text = t.slice(0, 80);
    if (el.parentElement) {
      try {
        var sibs = el.parentElement.children, n = 0, nt = 0;
        for (var i = 0; i < sibs.length; i++) {
          n++;
          if (sibs[i].tagName === el.tagName) nt++;
          if (sibs[i] === el) { d.nth_child = n; d.nth_of_type = nt; break; }
        }
      } catch (e) {}
    }
    return d;
  }

  // elemChain is the target plus up to 4 ancestors — Heap/PostHog-style $elements. This
  // is the substrate for defining events retroactively from autocaptured clicks.
  function elemChain(target) {
    var chain = [], node = target, depth = 0;
    while (node && node.tagName && depth < 5) {
      chain.push(elemDesc(node));
      node = node.parentElement;
      depth++;
    }
    return chain;
  }

  // isClickable broadens capture beyond a/button so modern apps (clickable divs, cards,
  // icons, role targets, cursor:pointer) aren't silently dropped.
  function isClickable(node) {
    var tag = node.tagName.toLowerCase();
    if (tag === "a" || tag === "button" || tag === "select" || tag === "label" || tag === "summary") return true;
    if (tag === "input" && /^(submit|button|checkbox|radio)$/.test(node.type || "")) return true;
    if (node.getAttribute) {
      var r = node.getAttribute("role");
      if (r === "button" || r === "link" || r === "tab" || r === "menuitem" || r === "option") return true;
    }
    if (typeof node.onclick === "function") return true;
    if (node.dataset && (node.dataset.saEvent || node.dataset.saName)) return true;
    try { if (getComputedStyle(node).cursor === "pointer") return true; } catch (e) {}
    return false;
  }

  // ignored answers "is this node inside a data-sa-ignore subtree?" — the documented opt-out.
  //
  // It used to be checked inside the same walk that hunts for the clickable target, on the same
  // node, with a `break` the moment one was found. So `<div data-sa-ignore><button>Reveal
  // SSN</button></div>` matched the button on the FIRST iteration and broke out; the ancestor
  // carrying the opt-out was never examined, and the SDK captured the button's text, id, classes
  // and the whole $elements chain — including the ignored container's own text. The opt-out
  // worked only for clicks on non-interactive filler, i.e. the clicks nobody needed protected.
  // The walk here is uncapped on purpose: the 5-deep limit is about finding a plausible click
  // target, and a privacy opt-out that stops after five ancestors is not an opt-out.
  function ignored(node) {
    try {
      if (node && node.closest) return !!node.closest("[data-sa-ignore]");
    } catch (e) {}
    while (node && node.tagName) {
      if (node.dataset && node.dataset.saIgnore !== undefined) return true;
      node = node.parentElement;
    }
    return false;
  }

  // frustration + scroll state (all best-effort, never allowed to throw into capture)
  var clickBuf = []; // recent click coords, for rage-click detection
  var maxScroll = 0; // deepest scroll % reached on the current page
  var errSeen = {}; // dedupe exceptions so one repeated error can't flood

  function detectRage(e) {
    var now = Date.now();
    clickBuf.push({ t: now, x: e.clientX, y: e.clientY });
    clickBuf = clickBuf.filter(function (c) { return now - c.t < 1000; });
    var near = clickBuf.filter(function (c) { return Math.abs(c.x - e.clientX) < 30 && Math.abs(c.y - e.clientY) < 30; });
    if (near.length >= 3) {
      enqueue("$rageclick", { path: location.pathname, x: e.clientX, y: e.clientY, count: near.length });
      clickBuf = [];
    }
  }

  // handsOff is true for controls that intentionally leave this document untouched: a link that
  // opens a new tab, a download, or a handoff to mailto:/tel:/sms:. They worked perfectly and
  // still produced no DOM change and no navigation, so they were reported as dead clicks.
  function handsOff(el) {
    try {
      if (((el.tagName || "") + "").toLowerCase() !== "a") return false;
      var tgt = ((el.target || "") + "");
      if (tgt && tgt !== "_self") return true;
      if (el.hasAttribute && el.hasAttribute("download")) return true;
      var p = ((el.protocol || "") + "").toLowerCase();
      if (p && p !== "http:" && p !== "https:") return true;
    } catch (e) {}
    return false;
  }

  // armDeadClick fires $deadclick when a clickable element produces no DOM change and no
  // navigation within ~1s — a broken control, exactly the "what to fix" signal.
  //
  // It watched childList+subtree only and compared location.pathname, which made it fire on
  // WORKING controls, and insight/human.go then told the builder "people clicked something that
  // did nothing": a dropdown or modal opened by toggling a CSS class is an ATTRIBUTES mutation;
  // a React text update sets nodeValue on an existing text node, a CHARACTERDATA mutation; a
  // filter or tab that only rewrites the query string or hash leaves pathname identical. All
  // three are covered now. Targets that are supposed to change nothing here are not armed at
  // all (handsOff). Still invisible: mutations inside a shadow root the SDK cannot reach, so a
  // web-component UI can produce a false $deadclick — the target's own shadow root is observed
  // when it has one, which covers the common "click the component, it re-renders itself" case.
  function armDeadClick(target) {
    if (handsOff(target)) return;
    var startHref = location.href, changed = false, obs = null;
    var opts = { childList: true, subtree: true, attributes: true, characterData: true };
    try {
      obs = new MutationObserver(function () { changed = true; if (obs) obs.disconnect(); });
      obs.observe(document.documentElement, opts);
      if (target.shadowRoot) obs.observe(target.shadowRoot, opts);
    } catch (e) { return; }
    setTimeout(function () {
      try { if (obs) obs.disconnect(); } catch (e) {}
      if (!changed && location.href === startHref) {
        enqueue("$deadclick", {
          path: location.pathname,
          tag: target.tagName.toLowerCase(),
          text: labelOf(target).slice(0, 80) || undefined,
        });
      }
    }, 1000);
  }

  function onScroll() {
    try {
      var h = document.documentElement.scrollHeight - window.innerHeight;
      var pct = h > 0 ? Math.min(100, Math.round((window.scrollY / h) * 100)) : 100;
      if (pct > maxScroll) maxScroll = pct;
    } catch (e) {}
  }

  function bindErrors() {
    window.addEventListener("error", function (ev) {
      try {
        var msg = ((ev && ev.message) || "") + "";
        if (!msg) return;
        var k = msg + "|" + ((ev && ev.lineno) || 0);
        if (errSeen[k]) return;
        errSeen[k] = 1;
        enqueue("$exception", {
          message: msg.slice(0, 300),
          source: ((ev && ev.filename) || "").slice(0, 200) || undefined,
          lineno: (ev && ev.lineno) || undefined,
          colno: (ev && ev.colno) || undefined,
          path: location.pathname,
        });
      } catch (e) {}
    });
    window.addEventListener("unhandledrejection", function (ev) {
      try {
        var reason = ev && ev.reason;
        var msg = ((reason && (reason.message || reason)) || "") + "";
        if (!msg || errSeen[msg]) return;
        errSeen[msg] = 1;
        enqueue("$exception", { message: msg.slice(0, 300), kind: "unhandledrejection", path: location.pathname });
      } catch (e) {}
    });
  }

  // autocapture: pageviews (incl. SPA route changes) + clicks on interactive
  // elements, so you get real data with zero manual instrumentation. Element
  // metadata only — never input values.
  function setupAutocapture() {
    if (captured) return; // idempotent — a second init must not double-wrap history or double-bind clicks
    captured = true;
    var lastPath = null;
    function pageview() {
      if (location.pathname === lastPath) return;
      if (lastPath !== null) engReport(); // attribute engaged time to the page being left
      maxScroll = 0; // reset scroll depth for the new page (engReport already read the old page's)
      lastPath = location.pathname;
      if (engPath === null) engPath = location.pathname;
      var props = webContext();
      props.path = location.pathname;
      props.referrer = document.referrer;
      props.title = document.title;
      enqueue("$pageview", props);
    }
    pageview();
    ["pushState", "replaceState"].forEach(function (m) {
      var orig = history[m];
      if (typeof orig !== "function") return;
      history[m] = function () {
        var r = orig.apply(this, arguments);
        pageview();
        return r;
      };
    });
    window.addEventListener("popstate", pageview);

    document.addEventListener(
      "click",
      function (e) {
        try {
          if (ignored(e.target)) return; // opt out of a subtree — checked BEFORE anything is read
          var node = e.target, depth = 0, target = null;
          while (node && node.tagName && depth < 5) {
            if (isClickable(node)) { target = node; break; }
            node = node.parentElement;
            depth++;
          }
          detectRage(e); // frustration is about click cadence, independent of the target
          if (!target) return;
          // data-sa-event turns any element into a named business event with zero code
          var name = (target.dataset && target.dataset.saEvent) ? target.dataset.saEvent : "$click";
          var props = {
            tag: target.tagName.toLowerCase(),
            text: labelOf(target).slice(0, 80) || undefined, // label, never the control's value
            id: target.id || undefined,
            classes: classesOf(target).slice(0, 160) || undefined,
            href: target.href ? safeHref(target.href) : undefined,
            path: location.pathname,
            x: e.clientX,
            y: e.clientY,
            vw: window.innerWidth, // viewport width → click x normalizes across screen sizes (heatmaps)
            $elements: elemChain(target), // the selector chain, for retroactive event definition
          };
          if (window.scrollY) props.sy = Math.round(window.scrollY); // scroll offset → document-space Y
          if (target.dataset && target.dataset.saName) props.name = target.dataset.saName;
          enqueue(name, props);
          armDeadClick(target);
        } catch (err) {}
      },
      true,
    );
    // form submits — metadata only, never field values
    document.addEventListener(
      "submit",
      function (e) {
        try {
          var f = e.target;
          // same opt-out bug as clicks: this only tested the <form> itself, so wrapping a form in
          // a data-sa-ignore container did nothing
          if (!f || f.tagName !== "FORM" || ignored(f)) return;
          enqueue("$form_submit", {
            id: f.id || undefined,
            name: (f.getAttribute && f.getAttribute("name")) || undefined,
            action: f.action ? safeHref(f.action).slice(0, 200) : undefined,
            fields: f.elements ? f.elements.length : undefined,
            path: location.pathname,
          });
        } catch (err) {}
      },
      true,
    );
    window.addEventListener("scroll", onScroll, { passive: true });
    bindErrors(); // $exception on window errors + unhandled rejections (deduped)
  }

  // self-exclusion (the Plausible pattern): open your site once per browser with
  // ?sa_optout=1 and that browser's visits stop being tracked entirely — so a
  // founder testing their own product doesn't dominate their own numbers.
  // ?sa_optout=0 re-enables. Cookieless visitors can also use it: the flag lives
  // in localStorage on YOUR device by YOUR choice, which needs no banner.
  var optedOut = false;
  try {
    var qs = new URLSearchParams(location.search);
    if (qs.get("sa_optout") === "1") localStorage.setItem("sa_optout", "1");
    if (qs.get("sa_optout") === "0") localStorage.removeItem("sa_optout");
    optedOut = localStorage.getItem("sa_optout") === "1";
    if (optedOut && window.console) console.info("smolanalytics: this browser is excluded from tracking (sa_optout=1). Visit any page with ?sa_optout=0 to re-enable.");
  } catch (e) {}

  // Feature flags — fetch this user's resolved values once (and again on identify), cache them,
  // and read them synchronously via flag(key, default). The server does the bucketing; the
  // browser only ever holds resolved values, so there is no targeting logic to leak or drift.
  // Gate on the RESOLVED id, not on the module-level `did`.
  //
  // In cookieless mode distinctId() returns the "$anon" sentinel and never assigns `did`, so
  // `did` stayed null forever and this function returned on its first line: flag() answered
  // every cookieless visitor with the caller's default, onFlags(cb) never fired (an app that
  // gates its render on it never rendered), and no $feature_flag_called exposure was ever
  // logged — so an A/B test on a cookieless site reported "no exposures" as though the SDK were
  // fine. Nothing was written to the console. Same shape in fetchSurveys.

  // ── LOCAL FLAG EVALUATION ────────────────────────────────────────────────────────────────
  //
  // Flags marked `local` on the server publish their DEFINITIONS, so this evaluates them here
  // with no round-trip. Everything else still resolves server-side; the bundle's `rest` field
  // says whether any of those exist, because a client that assumed otherwise would silently
  // treat every server-evaluated flag as off.
  //
  // The bucketing must match Go BIT FOR BIT. Not "closely" — a float that differs in the last
  // ulp moves whoever sits on the boundary into the other arm, which splits an experiment into
  // two populations and makes every result unreadable while looking perfectly healthy. The
  // parity test in internal/api generates a fixture from the real Go engine and runs THESE bytes
  // against it.
  //
  // SHA-256 is implemented here rather than via crypto.subtle because subtle is async and
  // flag(key, default) is synchronous — an async flag read would change the API for every
  // existing caller and reintroduce the render-blocking wait local evaluation exists to remove.
  var K256 = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ];
  // utf8Bytes: Go hashes []byte(s), which is UTF-8. Encoding per UTF-16 code unit instead would
  // diverge on every non-ASCII id — CJK, emoji, any surrogate pair — and those users would be
  // bucketed differently by the browser than by the server.
  function utf8Bytes(s) {
    var out = [], i, c, c2;
    for (i = 0; i < s.length; i++) {
      c = s.charCodeAt(i);
      if (c < 0x80) out.push(c);
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
      else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length &&
               (c2 = s.charCodeAt(i + 1)) >= 0xdc00 && c2 <= 0xdfff) {
        c = 0x10000 + ((c - 0xd800) << 10) + (c2 - 0xdc00); i++;
        out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
      } else out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
    return out;
  }
  // sha256First8 returns only the first 8 bytes as two uint32s — the bucket needs nothing else.
  function sha256First8(str) {
    var b = utf8Bytes(str), L = b.length, i, j;
    var withOne = b.concat([0x80]);
    while (withOne.length % 64 !== 56) withOne.push(0);
    var bitLen = L * 8;
    // Length is 64-bit big-endian. bitLen stays exact well past any realistic id length.
    withOne.push(0, 0, 0, 0,
      (bitLen / 16777216) & 255, (bitLen / 65536) & 255, (bitLen / 256) & 255, bitLen & 255);
    var h = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
    var w = new Array(64);
    for (i = 0; i < withOne.length; i += 64) {
      for (j = 0; j < 16; j++) {
        w[j] = (withOne[i + j * 4] << 24) | (withOne[i + j * 4 + 1] << 16) |
               (withOne[i + j * 4 + 2] << 8) | withOne[i + j * 4 + 3];
      }
      for (j = 16; j < 64; j++) {
        var s0 = ((w[j - 15] >>> 7) | (w[j - 15] << 25)) ^ ((w[j - 15] >>> 18) | (w[j - 15] << 14)) ^ (w[j - 15] >>> 3);
        var s1 = ((w[j - 2] >>> 17) | (w[j - 2] << 15)) ^ ((w[j - 2] >>> 19) | (w[j - 2] << 13)) ^ (w[j - 2] >>> 10);
        w[j] = (w[j - 16] + s0 + w[j - 7] + s1) | 0;
      }
      var a = h[0], bb = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
      for (j = 0; j < 64; j++) {
        var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
        var ch = (e & f) ^ (~e & g);
        var t1 = (hh + S1 + ch + K256[j] + w[j]) | 0;
        var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
        var maj = (a & bb) ^ (a & c) ^ (bb & c);
        var t2 = (S0 + maj) | 0;
        hh = g; g = f; f = e; e = (d + t1) | 0; d = c; c = bb; bb = a; a = (t1 + t2) | 0;
      }
      h[0] = (h[0] + a) | 0; h[1] = (h[1] + bb) | 0; h[2] = (h[2] + c) | 0; h[3] = (h[3] + d) | 0;
      h[4] = (h[4] + e) | 0; h[5] = (h[5] + f) | 0; h[6] = (h[6] + g) | 0; h[7] = (h[7] + hh) | 0;
    }
    return [h[0] >>> 0, h[1] >>> 0];
  }
  // saBucket mirrors Go's bucket(): sha256(salt+":"+id), first 8 bytes big-endian, >>4, /2^60.
  //
  // No BigInt (it is ES2020; this file is ES5). Taking the top 8 bytes as hi/lo uint32s,
  // `u >> 4 === hi * 2^28 + (lo >>> 4)`: hi*2^28 is a 32-bit integer scaled by a power of two so
  // it is exact, lo>>>4 is 28 bits so it is exact, and their sum is under 2^60 — well inside the
  // float64 integer range. The division by 2^60 is then the identical operation Go performs.
  function saBucket(salt, id) {
    var p = sha256First8(salt + ":" + id);
    return (p[0] * 268435456 + (p[1] >>> 4)) / 1152921504606846976;
  }
  function saBucketIn(salt, id, pct) { return saBucket(salt, id) < pct / 100; }

  function saStr(v) {
    if (v === null || v === undefined) return "";
    if (typeof v === "string") return v;
    return String(v);
  }
  // saMatch mirrors one query.Filter. Numeric comparands are NOT parsed here — the server ships
  // its own parse in `n`, because Go's ParseFloat and JS Number() disagree on "1_000", "0x10",
  // " 5 " and "", and each disagreement moves a user into a different arm.
  function saMatch(f, ctx) {
    var has = ctx && Object.prototype.hasOwnProperty.call(ctx, f.p);
    var raw = has ? ctx[f.p] : undefined;
    switch (f.o) {
      case "set": return has && raw !== null && raw !== undefined;
      case "notset": return !has || raw === null || raw === undefined;
    }
    if (!has || raw === null || raw === undefined) {
      // A missing property matches only the negative operators, exactly as the server treats it.
      return f.o === "neq" || f.o === "notin" || f.o === "ncontains";
    }
    var s = saStr(raw);
    switch (f.o) {
      case "eq": return s === saStr(f.v);
      case "neq": return s !== saStr(f.v);
      case "contains": return s.indexOf(saStr(f.v)) >= 0;
      case "ncontains": return s.indexOf(saStr(f.v)) < 0;
      case "in": case "notin": {
        var list = f.v, hit = false;
        if (Object.prototype.toString.call(list) === "[object Array]") {
          for (var i = 0; i < list.length; i++) if (s === saStr(list[i])) { hit = true; break; }
        }
        return f.o === "in" ? hit : !hit;
      }
      case "gt": case "lt": {
        var n = typeof raw === "number" ? raw : parseFloat(s);
        if (n !== n) return false; // NaN: a non-numeric property never satisfies a numeric compare
        var cmp = typeof f.n === "number" ? f.n : 0;
        return f.o === "gt" ? n > cmp : n < cmp;
      }
      case "regex": {
        try { return new RegExp(f.v).test(s); } catch (e) { return false; }
      }
    }
    return false;
  }

  // saEvalLocal resolves ONE bundled flag. The order is the server's order and the order matters:
  // holdout and layer decide whether someone may be in an experiment AT ALL, before any targeting
  // runs, so a client that checked rules first would put held-out users into arms.
  function saEvalLocal(bf, bucketKey, ctx) {
    if (bf.h && saBucketIn("holdout:" + bf.h, bucketKey, bf.hp || 0)) return "";
    if (bf.l && bf.s && bf.s.length === 2) {
      var pos = saBucket("layer:" + bf.l, bucketKey);
      if (pos < bf.s[0] || pos >= bf.s[1]) return "";
    }
    if (bf.r && bf.r.length) {
      var matched = null;
      for (var i = 0; i < bf.r.length; i++) {
        var rule = bf.r[i], ok = true;
        var fs = rule.f || [];
        for (var j = 0; j < fs.length; j++) { if (!saMatch(fs[j], ctx)) { ok = false; break; } }
        if (ok) { matched = rule; break; } // first match wins, like the server
      }
      if (!matched) return "";
      if (!saBucketIn("rollout:" + bf.k, bucketKey, matched.p)) return "";
    }
    return saVariantFor(bf, bucketKey);
  }
  // saVariantFor mirrors variantFor: a fixed [0,1) space walked in DECLARED order, so adding an
  // arm at the end never moves anyone already in an earlier one.
  function saVariantFor(bf, bucketKey) {
    var vs = bf.v || [];
    if (!vs.length) return "on"; // a boolean flag serves "on"
    var total = 0, i;
    for (i = 0; i < vs.length; i++) total += vs[i].w > 0 ? vs[i].w : 0;
    if (total <= 0) return vs[0].k;
    var x = saBucket("variant:" + bf.k, bucketKey), acc = 0;
    for (i = 0; i < vs.length; i++) {
      acc += (vs[i].w > 0 ? vs[i].w : 0) / total;
      if (x < acc) return vs[i].k;
    }
    return vs[vs.length - 1].k;
  }

  // saApplyBundle evaluates every locally-published flag and returns whether the server still has
  // to be asked for the rest. A bundle it cannot hash the same way as the server is REFUSED
  // outright — computing a different answer confidently is worse than not answering.
  function saApplyBundle(b, bucketKey, ctx) {
    if (!b || b.v !== 1 || b.hv !== 1) return true; // unknown schema/hash generation: server only
    var out = {}, meas = {}, fs = b.f || [];
    for (var i = 0; i < fs.length; i++) {
      var bf = fs[i], v = saEvalLocal(bf, bucketKey, ctx);
      if (v) { out[bf.k] = v; if (bf.m) meas[bf.k] = true; }
    }
    flagCache = out;
    flagMeasured = meas;
    return !!b.rest;
  }

  function fetchFlags() {
    var id = distinctId();
    if (!host || !key || !id) return;
    // Definitions first. The browser caches this by ETag, so the common case is a 304 and the
    // flags resolve with no data transferred and no bucketing round-trip.
    try {
      fetch(host + "/v1/flags/definitions", { headers: { Authorization: "Bearer " + key } })
        .then(function (r) { return r && r.ok ? r.json() : null; })
        .then(function (b) {
          var needServer = saApplyBundle(b, bucketId(), saFlagContext());
          if (!needServer) {
            flagsLoaded = true;
            var ls0 = flagListeners; flagListeners = [];
            for (var k = 0; k < ls0.length; k++) { try { ls0[k](flagCache); } catch (e) {} }
            return;
          }
          fetchFlagsFromServer();
        })
        .catch(function () { fetchFlagsFromServer(); });
    } catch (e) { fetchFlagsFromServer(); }
  }

  // saFlagContext is what local rules match against. Only what the page already knows about
  // itself — never anything read back from storage that the server did not send, so a rule can
  // never be satisfied by data this browser invented.
  function saFlagContext() {
    var c = {};
    try {
      c.path = location.pathname;
      c.referrer = document.referrer || "";
      if (typeof siteId !== "undefined" && siteId) c.site = siteId;
      if (typeof env !== "undefined" && env) c.env = env;
    } catch (e) {}
    return c;
  }

  function fetchFlagsFromServer() {
    var id = distinctId();
    if (!host || !key || !id) return;
    try {
      fetch(host + "/v1/flags/evaluate?distinct_id=" + encodeURIComponent(id) +
        "&bucket_id=" + encodeURIComponent(bucketId()), {
        headers: { Authorization: "Bearer " + key },
      })
        .then(function (r) { return r && r.ok ? r.json() : null; })
        .then(function (d) {
          // Merge, do not replace: locally-evaluated flags are already in flagCache and the
          // server response only covers the ones NOT in the bundle. Assigning over it would
          // discard every local answer and undo the round-trip we just saved.
          var sf = (d && d.flags) || {};
          for (var sk in sf) if (Object.prototype.hasOwnProperty.call(sf, sk)) flagCache[sk] = sf[sk];
          if (d && d.measured) for (var mi = 0; mi < d.measured.length; mi++) flagMeasured[d.measured[mi]] = true;
          flagsLoaded = true;
          var ls = flagListeners;
          flagListeners = [];
          for (var i = 0; i < ls.length; i++) { try { ls[i](flagCache); } catch (e) {} }
        })
        .catch(function () {});
    } catch (e) {}
  }

  // Surveys — fetch the active surveys for this page, show the first eligible one as a small
  // non-blocking popover, and post the answer as an event. Dependency-free, one at a time,
  // deduped per browser via localStorage, and sampled deterministically per user.
  function surveySeen(id) {
    try { return (localStorage.getItem("smol_surveys") || "").indexOf(id + ",") >= 0; } catch (e) { return false; }
  }
  function markSurveySeen(id) {
    try { var v = localStorage.getItem("smol_surveys") || ""; if (v.indexOf(id + ",") < 0) localStorage.setItem("smol_surveys", v + id + ","); } catch (e) {}
  }
  function hashPct(s) { var h = 5381; for (var i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) >>> 0; return h % 100; }
  function fetchSurveys() {
    var id = distinctId(); // see fetchFlags: `did` is null for the whole visit in cookieless mode
    // "$anon" is the same string for every cookieless visitor, so hashing it would sample all of
    // them in or all of them out; the per-page-load bucket id samples the right PROPORTION.
    var sampleKey = anon ? bucketId() : id;
    if (!host || !key || !id || surveyShownThisLoad || typeof document === "undefined") return;
    try {
      fetch(host + "/v1/surveys/active?path=" + encodeURIComponent(location.pathname), { headers: { Authorization: "Bearer " + key } })
        .then(function (r) { return r && r.ok ? r.json() : null; })
        .then(function (d) {
          if (!d || !d.surveys) return;
          for (var i = 0; i < d.surveys.length; i++) {
            var sv = d.surveys[i];
            if (surveySeen(sv.id)) continue;
            if (sv.sample_pct && sv.sample_pct > 0 && sv.sample_pct < 100 && hashPct(sv.id + ":" + sampleKey) >= sv.sample_pct) continue;
            showSurvey(sv);
            break;
          }
        })
        .catch(function () {});
    } catch (e) {}
  }
  function showSurvey(sv) {
    if (surveyShownThisLoad) return;
    surveyShownThisLoad = true;
    var wrap = document.createElement("div");
    wrap.setAttribute("style", "position:fixed;bottom:20px;right:20px;z-index:2147483000;max-width:320px;background:#151515;color:#fafafa;border:1px solid #333;border-radius:10px;padding:16px;box-shadow:0 12px 40px rgba(0,0,0,.4);font-family:system-ui,-apple-system,sans-serif;font-size:14px;line-height:1.4");
    function done() { try { document.body.removeChild(wrap); } catch (e) {} }
    function answer(val) {
      enqueue("$survey_response", { survey_id: sv.id, answer: val });
      markSurveySeen(sv.id);
      wrap.innerHTML = "";
      var thanks = document.createElement("div");
      thanks.textContent = "thanks!";
      thanks.setAttribute("style", "color:#f5a623;font-weight:600");
      wrap.appendChild(thanks);
      setTimeout(done, 1200);
    }
    var close = document.createElement("button");
    close.textContent = "×";
    close.setAttribute("style", "position:absolute;top:8px;right:10px;background:none;border:none;color:#888;font-size:18px;cursor:pointer;line-height:1");
    close.onclick = function () { markSurveySeen(sv.id); done(); };
    wrap.appendChild(close);
    var q = document.createElement("div");
    q.textContent = sv.question;
    q.setAttribute("style", "margin:0 16px 12px 0;font-weight:600");
    wrap.appendChild(q);
    var row = document.createElement("div");
    row.setAttribute("style", "display:flex;flex-wrap:wrap;gap:6px");
    function btn(label, val) {
      var b = document.createElement("button");
      b.textContent = label;
      b.setAttribute("style", "flex:0 0 auto;min-width:32px;padding:6px 10px;background:#222;color:#fafafa;border:1px solid #3a3a3a;border-radius:6px;cursor:pointer;font-size:13px");
      b.onmouseover = function () { b.style.borderColor = "#f5a623"; };
      b.onmouseout = function () { b.style.borderColor = "#3a3a3a"; };
      b.onclick = function () { answer(val); };
      return b;
    }
    if (sv.type === "nps") { for (var n = 0; n <= 10; n++) row.appendChild(btn(String(n), n)); }
    else if (sv.type === "rating") { for (var s = 1; s <= 5; s++) row.appendChild(btn(String(s), s)); }
    else if (sv.type === "choice" && sv.choices) { for (var c = 0; c < sv.choices.length; c++) (function (ch) { row.appendChild(btn(ch, ch)); })(sv.choices[c]); }
    else {
      var input = document.createElement("input");
      input.setAttribute("style", "width:100%;box-sizing:border-box;padding:8px;background:#222;color:#fafafa;border:1px solid #3a3a3a;border-radius:6px;font-size:13px;margin-bottom:8px");
      input.placeholder = "your answer";
      input.onkeydown = function (e) { if (e.key === "Enter" && input.value.trim()) answer(input.value.trim().slice(0, 500)); };
      var send = btn("Send", "");
      send.onclick = function () { if (input.value.trim()) answer(input.value.trim().slice(0, 500)); };
      row.appendChild(input);
      row.appendChild(send);
    }
    wrap.appendChild(row);
    try { document.body.appendChild(wrap); enqueue("$survey_shown", { survey_id: sv.id }); } catch (e) { surveyShownThisLoad = false; }
  }

  var smol = {
    init: function (writeKey, opts) {
      if (optedOut) return; // excluded browser: the SDK is a complete no-op
      // Calling init twice DOUBLES every number. It happens easily and silently: the snippet in
      // a layout plus a framework <Script> tag, a tag manager firing alongside a hard-coded
      // include, or a hot reload. Both copies bind their own listeners, so every pageview,
      // click and engagement is recorded twice and the dashboard reports exactly 2x reality with
      // nothing anywhere to indicate it.
      //
      // Found on our own site, where a Next.js layout rendered two loaders and every number was
      // double. Guarding here rather than telling people not to do it, because a silent 2x is
      // the single most damaging failure this SDK can have — every downstream report inherits it.
      if (inited) {
        if (window.console) console.warn("smolanalytics: init() called more than once — ignoring the repeat. Include the snippet in exactly one place, or every event is recorded twice.");
        return;
      }
      // THE FIRST ARGUMENT IS A WRITE KEY, NOT A CONFIG OBJECT.
      //
      // Three framework landing pages shipped `init({ site: "..." })` for months. Because a
      // non-empty object is truthy, `key` became the object, `opts` stayed undefined so `host`
      // became "", and the install sent NOTHING — with one console warning about the host and
      // nothing at all about the key. A person following our own documentation got a silent
      // zero and had every reason to conclude the product was broken.
      //
      // Docs drift; code does not. Refusing here means the next time a snippet goes wrong, the
      // person who pastes it is told exactly what is wrong within a second, by the SDK itself.
      if (writeKey && typeof writeKey !== "string") {
        if (window.console) {
          console.error(
            "smolanalytics: init() takes your WRITE KEY as a string first, then options — " +
            'init("sa_...", { host: "https://YOUR_HOST" }). You passed a ' + typeof writeKey +
            ". Nothing will be recorded until this is fixed."
          );
        }
        return;
      }
      if (!writeKey && window.console) {
        console.error('smolanalytics: init() called with no write key — nothing will be recorded. Pass init("sa_...", { host: "https://YOUR_HOST" }).');
      }
      inited = true;
      opts = opts || {};
      key = writeKey || "";
      host = (opts.host || "").replace(/\/$/, "");
      if (!host && window.console) console.warn("smolanalytics: no host set — pass { host: \"https://YOUR_HOST\" } to init(), or events have nowhere to go");
      // anonymous: true → cookieless mode. Nothing is stored on the visitor's device
      // (no localStorage, no cookies), so no consent banner is needed; the server
      // derives a daily-rotating anonymous id instead. identify() still works after
      // login if you want real user analytics for signed-in users.
      anon = !!opts.anonymous;
      envName = opts.env || detectEnv(location.hostname);
      distinctId();
      fetchFlags();
      if (opts.surveys !== false) fetchSurveys(); // set surveys:false to opt out of the widget
      if (opts.autocapture !== false) {
        setupAutocapture(); // pageviews + clicks, zero manual instrumentation
      }
      if (timer) clearInterval(timer);
      // setInterval hands the callback a timer argument in some engines, and flush(final) reads
      // its first argument as "this request must outlive the document" — a truthy one here would
      // put keepalive on every routine flush, which is the ceiling this whole scheme exists to
      // stay under. Call it with nothing, explicitly.
      timer = setInterval(function () { flush(); }, (opts.flushInterval || 3) * 1000);
      if (!lifecycleBound) {
        // bind once — a second init() must not stack duplicate flush-on-unload listeners
        lifecycleBound = true;
        document.addEventListener("visibilitychange", function () {
          engTick();
          if (document.visibilityState === "hidden") {
            engReport();
            // hidden is where a mobile tab gets frozen or killed, so this one has to survive the
            // document — the ONLY places keepalive belongs (see flush)
            flush(true);
          }
        });
        window.addEventListener("focus", engTick);
        window.addEventListener("blur", engTick);
        window.addEventListener("pagehide", function () {
          engReport();
          persistSession();
          flush(true); // the page is going away; without keepalive the browser cancels this
        });
        engPath = location.pathname;
        engTick();
      }
      return smol;
    },
    track: function (name, props) {
      enqueue(name, props);
      return smol;
    },
    identify: function (id, traits) {
      var prev = did; // the anonymous id this browser used before login
      if (id) {
        anon = false; // an explicit login id overrides cookieless mode for this user
        try { localStorage.setItem("smol_did", id); } catch (e) {}
        did = id;
      }
      var props = traits || {};
      // Breadcrumb for identity stitching — ONLY from an id this SDK minted anonymously.
      //
      // The guard used to be `prev !== id`, and `prev` is whatever distinctId() last resolved,
      // which on a returning browser is the PREVIOUS ACCOUNT'S id: identify() writes it into the
      // same localStorage key distinctId() reads back. So a shared laptop, or any app-side logout
      // that doesn't call reset(), sent $identify{$anon_distinct_id:"u-alice"} for u-bob. The
      // server's alias.Add only refuses empty/self/"$anon", so it accepted the edge and wrote it
      // to disk — from then on every historical Alice event was rewritten to Bob at read time.
      // Two humans merged into one, permanently: user counts, DAU, retention cohorts and funnels
      // all wrong, and DeleteUser("u-alice") would erase Bob too. uid() namespaces anonymous ids
      // with "a-", which is the only thing that distinguishes "not logged in yet" from "was
      // someone else".
      if (prev && prev !== id && prev.indexOf("a-") === 0) props.$anon_distinct_id = prev;
      enqueue("$identify", props);
      if (id && id !== prev) fetchFlags(); // re-evaluate flags for the newly identified user
      return smol;
    },
    reset: function () {
      // Logout ends the VISIT, not just the id. This used to clear smol_did only, so `sess`
      // survived in memory and in localStorage and ensureSession() kept reusing it for the rest
      // of the 30-minute window — including across reloads. web.go keys entry/exit pages on
      // session_id alone, so the next person on that browser joined the logged-out user's
      // session and "where do visits start / where do they die" attributed one person's landing
      // page to the other's visit. The flag caches survived too, so flagExposed still marked
      // measured flags as exposed and the new visitor's variant was never logged while their
      // conversions still landed — an experiment quietly missing one arm's exposures.
      //
      // Order matters: flush() first, so anything already queued goes out under the identity
      // that produced it. smol_bucket_id is deliberately KEPT — bucketing is per browser by
      // design (see bucketId), and re-minting it on logout would reshuffle a running experiment.
      flush();
      try { localStorage.removeItem("smol_did"); } catch (e) {}
      try { localStorage.removeItem("smol_session"); } catch (e) {}
      // the exposure record is keyed on the session id, so a stale one can never be read back
      // after this point — but leaving it behind is dead storage, and removing it keeps "logout
      // ends the visit" literally true of everything the visit wrote.
      try { localStorage.removeItem(EXPOSED_KEY); } catch (e) {}
      did = null;
      sess = null;
      flagCache = {};
      flagMeasured = {};
      flagExposed = {};
      flagsLoaded = false;
      surveyShownThisLoad = false;
      engAccum = 0;
      engStart = engActive() ? Date.now() : null; // restart the clock, don't stop it
      engPath = location.pathname;
      distinctId(); // mint the new visitor's id now, so the re-fetch below buckets against it
      fetchFlags();
      return smol;
    },
    flush: flush,
    // flag(key, default) reads the resolved value for this user (default when off/absent).
    flag: function (name, def) {
      var has = Object.prototype.hasOwnProperty.call(flagCache, name);
      // a measured flag logs one $feature_flag_called exposure per session on first read, so it
      // can be A/B-analysed; ordinary flags add zero events.
      // ensureSession() BEFORE the check, not after: on a second page load `sess` is null until it
      // is rehydrated, and a null session makes alreadyExposed() fall back to the empty in-memory
      // map — which is the very duplicate this dedupe exists to stop.
      if (has && flagMeasured[name]) ensureSession();
      if (has && flagMeasured[name]) {
        var variant = flagCache[name];
        var ekey = exposureKey(name, variant);
        if (!alreadyExposed(ekey)) {
          markExposed(ekey);
          // Cookieless mode has no device-stable bucket key (see bucketId), so the same visitor can
          // be re-bucketed on their next page load and land in both arms of the same experiment.
          // The exposures are real, the comparison is not — say so once rather than let someone
          // read a lift off it.
          if (anon && !warnedAnonMeasured) {
            warnedAnonMeasured = true;
            if (window.console) console.warn("smolanalytics: \"" + name + "\" is a measured experiment but this page is in cookieless mode — bucketing cannot be stable per visitor, so treat the A/B result as indicative only.");
          }
          var ex = { $feature_flag: name, $feature_flag_response: variant };
          // The RANDOMISATION UNIT, stamped on the exposure — the same key that was sent to
          // /v1/flags/evaluate and therefore the thing the coin was actually flipped on.
          //
          // Without it the analysis has nothing but distinct_id to aggregate to, and distinct_id
          // is a different estimand: it changes at login, so one person who signs up mid-test is
          // ONE unit under bucket_id and TWO under distinct_id — the second carrying only the
          // post-signup half of their behaviour. internal/flag reads $bucket_id and states in the
          // result which unit it used; absent, every instance silently fell back to distinct_id.
          //
          // Deliberately NOT stamped in cookieless mode: bucketId() there lives for one page load
          // (nothing may be written to the device), while the server derives a day-stable visitor
          // id from the "$anon" sentinel. Stamping the per-page-load key would hand the analysis a
          // unit that is LESS stable than the one it falls back to, and would hide the re-bucketing
          // inside a pile of single-exposure units instead of surfacing it as the multi-variant
          // exposure it is.
          if (!anon) {
            var bkt = bucketId();
            if (bkt) ex.$bucket_id = bkt;
          }
          enqueue("$feature_flag_called", ex);
        }
      }
      return has ? flagCache[name] : (def === undefined ? false : def);
    },
    flags: function () {
      var out = {};
      for (var k in flagCache) if (Object.prototype.hasOwnProperty.call(flagCache, k)) out[k] = flagCache[k];
      return out;
    },
    // onFlags(cb) runs cb once flags have loaded (or immediately if already loaded).
    onFlags: function (cb) {
      if (flagsLoaded) { try { cb(flagCache); } catch (e) {} } else { flagListeners.push(cb); }
      return smol;
    },
    reloadFlags: fetchFlags,
  };

  // Test hook, deliberately exposed and deliberately ugly.
  //
  // The flag parity test runs the SHIPPED BYTES of this file against a fixture generated by the Go
  // engine, and it can only do that if it can reach the evaluator inside this closure. The
  // alternative is a copy of the bucketing algorithm in the test, which would prove only that the
  // test agrees with itself while the real file drifted.
  //
  // A bucketing divergence between this file and Go has no symptom: nothing errors, and the
  // experiment silently becomes a mixture of two populations. Being able to assert it on every
  // build is worth one underscore-prefixed key.
  smol.__flagInternals = { bucket: saBucket, evalFlag: saEvalLocal, applyBundle: saApplyBundle };
  window.smolanalytics = smol;
})();
