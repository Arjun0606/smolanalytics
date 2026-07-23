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
  var anon = false; // cookieless mode: nothing stored on the device, no banner needed
  var envName = "production"; // "development" on localhost, or whatever init({env}) says
  var timer = null;
  var flagCache = {};
  var flagMeasured = {}; // keys the server marked "measured" → log an exposure on read
  var flagExposed = {}; // per-session dedupe so a measured flag logs at most one exposure
  var flagsLoaded = false;
  var flagListeners = [];
  var surveyShownThisLoad = false; // at most one survey popover per page load
  var captured = false; // autocapture wired once, even if the snippet loads twice
  var warnedAuth = false; // warn once on a bad key, don't spam the console
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

  function flush() {
    if (!queue.length) return;
    var batch = queue.splice(0, queue.length);
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
        keepalive: true,
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
    if (el.href) d.href = (el.href + "").slice(0, 300);
    // data-* attributes (opt-in element identity; data-sa-* drive named events)
    try {
      if (el.dataset) {
        for (var dk in el.dataset) {
          var dv = el.dataset[dk];
          if (dv) (d.data = d.data || {})[dk] = (dv + "").slice(0, 80);
        }
      }
    } catch (e) {}
    var t = ((el.innerText || el.value || "") + "").trim();
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

  // armDeadClick fires $deadclick when a clickable element produces no DOM change and no
  // navigation within ~1s — a broken control, exactly the "what to fix" signal.
  function armDeadClick(target) {
    var startPath = location.pathname, changed = false, obs = null;
    try {
      obs = new MutationObserver(function () { changed = true; if (obs) obs.disconnect(); });
      obs.observe(document.documentElement, { childList: true, subtree: true });
    } catch (e) { return; }
    setTimeout(function () {
      try { if (obs) obs.disconnect(); } catch (e) {}
      if (!changed && location.pathname === startPath) {
        enqueue("$deadclick", {
          path: startPath,
          tag: target.tagName.toLowerCase(),
          text: ((target.innerText || "") + "").trim().slice(0, 80) || undefined,
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
          var node = e.target, depth = 0, target = null;
          while (node && node.tagName && depth < 5) {
            if (node.dataset && node.dataset.saIgnore !== undefined) return; // opt out of a subtree
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
            text: ((target.innerText || target.value || "") + "").trim().slice(0, 80) || undefined,
            id: target.id || undefined,
            classes: classesOf(target).slice(0, 160) || undefined,
            href: target.href || undefined,
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
          if (!f || f.tagName !== "FORM" || (f.dataset && f.dataset.saIgnore !== undefined)) return;
          enqueue("$form_submit", {
            id: f.id || undefined,
            name: (f.getAttribute && f.getAttribute("name")) || undefined,
            action: f.action ? (f.action + "").slice(0, 200) : undefined,
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
  function fetchFlags() {
    if (!host || !key || !did) return;
    try {
      fetch(host + "/v1/flags/evaluate?distinct_id=" + encodeURIComponent(did), {
        headers: { Authorization: "Bearer " + key },
      })
        .then(function (r) { return r && r.ok ? r.json() : null; })
        .then(function (d) {
          flagCache = (d && d.flags) || {};
          flagMeasured = {};
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
    if (!host || !key || !did || surveyShownThisLoad || typeof document === "undefined") return;
    try {
      fetch(host + "/v1/surveys/active?path=" + encodeURIComponent(location.pathname), { headers: { Authorization: "Bearer " + key } })
        .then(function (r) { return r && r.ok ? r.json() : null; })
        .then(function (d) {
          if (!d || !d.surveys) return;
          for (var i = 0; i < d.surveys.length; i++) {
            var sv = d.surveys[i];
            if (surveySeen(sv.id)) continue;
            if (sv.sample_pct && sv.sample_pct > 0 && sv.sample_pct < 100 && hashPct(sv.id + ":" + did) >= sv.sample_pct) continue;
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
      opts = opts || {};
      key = writeKey || "";
      host = (opts.host || "").replace(/\/$/, "");
      if (!host && window.console) console.warn("smolanalytics: no host set — pass { host: \"https://YOUR_HOST\" } to init(), or events have nowhere to go");
      // anonymous: true → cookieless mode. Nothing is stored on the visitor's device
      // (no localStorage, no cookies), so no consent banner is needed; the server
      // derives a daily-rotating anonymous id instead. identify() still works after
      // login if you want real user analytics for signed-in users.
      anon = !!opts.anonymous;
      envName = opts.env || (/^(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])$/.test(location.hostname) ? "development" : "production");
      distinctId();
      fetchFlags();
      if (opts.surveys !== false) fetchSurveys(); // set surveys:false to opt out of the widget
      if (opts.autocapture !== false) {
        setupAutocapture(); // pageviews + clicks, zero manual instrumentation
      }
      if (timer) clearInterval(timer);
      timer = setInterval(flush, (opts.flushInterval || 3) * 1000);
      if (!lifecycleBound) {
        // bind once — a second init() must not stack duplicate flush-on-unload listeners
        lifecycleBound = true;
        document.addEventListener("visibilitychange", function () {
          engTick();
          if (document.visibilityState === "hidden") {
            engReport();
            flush();
          }
        });
        window.addEventListener("focus", engTick);
        window.addEventListener("blur", engTick);
        window.addEventListener("pagehide", function () {
          engReport();
          persistSession();
          flush();
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
      // breadcrumb for identity stitching: the server joins the pre-login journey
      // to this account (guarded server-side against the $anon sentinel)
      if (prev && prev !== id) props.$anon_distinct_id = prev;
      enqueue("$identify", props);
      if (id && id !== prev) fetchFlags(); // re-evaluate flags for the newly identified user
      return smol;
    },
    reset: function () {
      try { localStorage.removeItem("smol_did"); } catch (e) {}
      did = null;
    },
    flush: flush,
    // flag(key, default) reads the resolved value for this user (default when off/absent).
    flag: function (name, def) {
      var has = Object.prototype.hasOwnProperty.call(flagCache, name);
      // a measured flag logs one $feature_flag_called exposure per session on first read, so it
      // can be A/B-analysed; ordinary flags add zero events.
      if (has && flagMeasured[name] && !flagExposed[name]) {
        flagExposed[name] = true;
        enqueue("$feature_flag_called", { $feature_flag: name, $feature_flag_response: flagCache[name] });
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

  window.smolanalytics = smol;
})();
