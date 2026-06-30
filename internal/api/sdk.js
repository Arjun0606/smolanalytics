/* smolanalytics browser SDK — drop-in, dependency-free, ~1KB.
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
  var timer = null;
  var captured = false; // autocapture wired once, even if the snippet loads twice
  var warnedAuth = false; // warn once on a bad key, don't spam the console
  var lifecycleBound = false; // flush-on-unload listeners bound once, even on re-init

  function uid() {
    return "a-" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  }

  function distinctId() {
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
        else if ((r.status === 401 || r.status === 403) && !warnedAuth) {
          warnedAuth = true;
          // a typo'd write key drops every event silently otherwise — say so once
          if (window.console) console.warn("smolanalytics: events rejected (" + r.status + ") — check your write key");
        }
      }).catch(requeue);
    } catch (e) {
      requeue();
    }
  }

  function enqueue(name, props) {
    queue.push({
      name: name,
      distinct_id: distinctId(),
      timestamp: new Date().toISOString(),
      properties: props || {},
    });
    if (queue.length >= 20) flush();
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
      lastPath = location.pathname;
      enqueue("$pageview", { path: location.pathname, referrer: document.referrer, title: document.title });
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
        var node = e.target,
          depth = 0;
        while (node && node.tagName && depth < 4) {
          var tag = node.tagName.toLowerCase();
          var clickable =
            tag === "a" ||
            tag === "button" ||
            (tag === "input" && /^(submit|button)$/.test(node.type || "")) ||
            (node.getAttribute && node.getAttribute("role") === "button");
          if (clickable) {
            enqueue("$click", {
              tag: tag,
              text: ((node.innerText || node.value || "") + "").trim().slice(0, 80) || undefined,
              id: node.id || undefined,
              classes: (node.className && (node.className + "").slice(0, 120)) || undefined,
              href: node.href || undefined,
              path: location.pathname,
            });
            return;
          }
          node = node.parentElement;
          depth++;
        }
      },
      true,
    );
  }

  var smol = {
    init: function (writeKey, opts) {
      opts = opts || {};
      key = writeKey || "";
      host = (opts.host || "").replace(/\/$/, "");
      distinctId();
      if (opts.autocapture !== false) {
        setupAutocapture(); // pageviews + clicks, zero manual instrumentation
      }
      if (timer) clearInterval(timer);
      timer = setInterval(flush, (opts.flushInterval || 3) * 1000);
      if (!lifecycleBound) {
        // bind once — a second init() must not stack duplicate flush-on-unload listeners
        lifecycleBound = true;
        window.addEventListener("visibilitychange", function () {
          if (document.visibilityState === "hidden") flush();
        });
        window.addEventListener("pagehide", flush);
      }
      return smol;
    },
    track: function (name, props) {
      enqueue(name, props);
      return smol;
    },
    identify: function (id, traits) {
      if (id) {
        try { localStorage.setItem("smol_did", id); } catch (e) {}
        did = id;
      }
      enqueue("$identify", traits || {});
      return smol;
    },
    reset: function () {
      try { localStorage.removeItem("smol_did"); } catch (e) {}
      did = null;
    },
    flush: flush,
  };

  window.smolanalytics = smol;
})();
