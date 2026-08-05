package api

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The browser SDK is the surface every number starts at, and it is the one file with no type
// checker and no compiler behind it. These tests run the REAL internal/api/sdk.js — the exact
// bytes served at GET /sdk.js — inside a stubbed DOM under node, drive it the way a page would,
// and read back what it actually put on the wire. Nothing here asserts on the source text except
// sdkStaticGuards below, which is the fallback for machines with no node on PATH.
//
// Harness lives in this file rather than in internal/api/sdktest so the observations and the
// assertions about them cannot drift apart.
const sdkHarness = `// Drives the real internal/api/sdk.js inside a stubbed browser and prints raw observations as
// JSON. Assertions live in Go (sdk_regress_test.go) — this file only supplies a DOM.
import fs from "node:fs";
import vm from "node:vm";

const src = fs.readFileSync(process.argv[2], "utf8");

function el(spec) {
  spec = spec || {};
  const e = {
    tagName: (spec.tag || "div").toUpperCase(),
    dataset: spec.dataset || {},
    id: spec.id || "",
    className: spec.className || "",
    innerText: spec.innerText || "",
    value: spec.value,
    type: spec.type,
    href: spec.href,
    protocol: spec.protocol,
    target: spec.target,
    action: spec.action,
    labels: spec.labels,
    shadowRoot: null,
    children: [],
    parentElement: null,
    _attrs: spec.attrs || {},
  };
  e.getAttribute = (n) => (n in e._attrs ? e._attrs[n] : null);
  e.hasAttribute = (n) => n in e._attrs;
  e.closest = (sel) => {
    if (sel !== "[data-sa-ignore]") return null;
    let n = e;
    while (n) {
      if (n.dataset && n.dataset.saIgnore !== undefined) return n;
      n = n.parentElement;
    }
    return null;
  };
  (spec.children || []).forEach((c) => {
    c.parentElement = e;
    e.children.push(c);
  });
  return e;
}

function boot(opts, shared) {
  shared = shared || {};
  const storage = shared.storage || new Map();
  const state = {
    storage,
    fetches: [],
    warns: [],
    timers: [],
    observers: [],
    docListeners: {},
    winListeners: {},
    flags: shared.flags || { flags: {}, measured: [] },
    surveys: shared.surveys || { surveys: [] },
  };
  const loc = { search: "", pathname: "/app", hostname: "example.com", href: "https://example.com/app" };
  state.loc = loc;
  const root = el({ tag: "html" });
  const body = el({ tag: "body" });
  const doc = {
    documentElement: root,
    body,
    referrer: "",
    title: "t",
    visibilityState: "visible",
    hasFocus: () => true,
    addEventListener: (t, fn) => {
      (state.docListeners[t] = state.docListeners[t] || []).push(fn);
    },
    createElement: (tag) => {
      const e = el({ tag });
      e.setAttribute = () => {};
      e.appendChild = () => {};
      e.style = {};
      return e;
    },
  };
  class MO {
    constructor(cb) {
      this.cb = cb;
      this.opts = [];
      state.observers.push(this);
    }
    observe(t, o) {
      this.opts.push(o);
    }
    disconnect() {
      this.dead = true;
    }
  }
  const win = {
    console: {
      warn: (m) => state.warns.push(String(m)),
      info: () => {},
      log: () => {},
    },
    innerWidth: 1200,
    innerHeight: 800,
    scrollY: 0,
    addEventListener: (t, fn) => {
      (state.winListeners[t] = state.winListeners[t] || []).push(fn);
    },
  };
  const sandbox = {
    window: win,
    document: doc,
    location: loc,
    navigator: { userAgent: "Mozilla/5.0 (Macintosh)" },
    screen: { width: 1440, height: 900 },
    history: {},
    localStorage: {
      getItem: (k) => (storage.has(k) ? storage.get(k) : null),
      setItem: (k, v) => storage.set(k, String(v)),
      removeItem: (k) => storage.delete(k),
    },
    MutationObserver: MO,
    getComputedStyle: () => ({ cursor: "default" }),
    URLSearchParams,
    setTimeout: (fn, ms) => {
      state.timers.push(fn);
      return state.timers.length;
    },
    clearTimeout: () => {},
    setInterval: () => 1,
    clearInterval: () => {},
    fetch: (url, o) => {
      state.fetches.push({ url, body: (o && o.body) || "", keepalive: !!(o && o.keepalive) });
      let payload = { ok: true };
      if (url.indexOf("/v1/flags/evaluate") >= 0) payload = state.flags;
      if (url.indexOf("/v1/surveys/active") >= 0) payload = state.surveys;
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(payload) });
    },
  };
  sandbox.console = win.console; // the SDK guards on window.console then calls the bare global
  sandbox.self = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox);
  const smol = sandbox.window.smolanalytics;
  smol.init("wk", Object.assign({ host: "https://h" }, opts || {}));

  state.click = (node, x, y) => {
    (state.docListeners.click || []).forEach((fn) => fn({ target: node, clientX: x || 5, clientY: y || 5 }));
  };
  state.submit = (node) => {
    (state.docListeners.submit || []).forEach((fn) => fn({ target: node }));
  };
  state.mutate = (kind) => {
    state.observers.forEach((o) => {
      if (o.dead) return;
      if (o.opts.some((p) => p[kind])) o.cb();
    });
  };
  state.runTimers = () => {
    const t = state.timers.splice(0, state.timers.length);
    t.forEach((fn) => fn());
  };
  // every event the SDK has handed to the network, decoded
  state.sent = () => {
    smol.flush();
    const out = [];
    for (const f of state.fetches) {
      if (f.url.indexOf("/v1/events") < 0) continue;
      try {
        JSON.parse(f.body).forEach((e) => out.push(e));
      } catch (e) {}
    }
    return out;
  };
  state.smol = smol;
  return state;
}

const tick = () => new Promise((r) => setImmediate(r));
const out = {};
const evOf = (evs, name) => evs.filter((e) => e.name === name);

// --- identify: a returning browser must not alias one real account onto another
{
  const storage = new Map();
  const a = boot({ autocapture: false }, { storage });
  a.smol.identify("u-alice");
  a.sent();
  const b = boot({ autocapture: false }, { storage });
  b.smol.identify("u-bob");
  const idev = evOf(b.sent(), "$identify")[0];
  out.identify_returning = { distinct_id: idev.distinct_id, anon_prev: idev.properties.$anon_distinct_id || null };

  const fresh = boot({ autocapture: false }, {});
  fresh.smol.identify("u-carol");
  const ev2 = evOf(fresh.sent(), "$identify")[0];
  out.identify_first_login = { anon_prev: ev2.properties.$anon_distinct_id || null };
}

// --- click capture: labels, not values; hrefs without their tokens
{
  const env = boot({}, {});
  const cb = el({ tag: "input", type: "checkbox", attrs: { name: "invite" }, value: "jane@corp.com" });
  const row = el({ tag: "div", children: [cb] });
  env.click(cb);
  const clicks = evOf(env.sent(), "$click");
  out.click_checkbox = {
    text: clicks[0].properties.text || null,
    chain_text: (clicks[0].properties.$elements[0] || {}).text || null,
    body_has_email: JSON.stringify(clicks[0]).indexOf("jane@corp.com") >= 0,
  };
}
{
  const env = boot({}, {});
  const btn = el({ tag: "input", type: "submit", value: "Sign up" });
  env.click(btn);
  out.click_submit_caption = evOf(env.sent(), "$click")[0].properties.text || null;
}
{
  const env = boot({}, {});
  const a = el({ tag: "a", innerText: "Reset", href: "https://ex.com/reset?token=SECRET#f", protocol: "https:" });
  env.click(a);
  const m = el({ tag: "a", innerText: "Email us", href: "mailto:jane@corp.com", protocol: "mailto:" });
  env.click(m);
  const clicks = evOf(env.sent(), "$click");
  out.click_href = {
    query: clicks[0].properties.href,
    chain: clicks[0].properties.$elements[0].href,
    mailto: clicks[1].properties.href,
  };
}

// --- data-sa-ignore must cover clickable descendants and wrapped forms
{
  const env = boot({}, {});
  const btn = el({ tag: "button", id: "ssn", innerText: "Reveal SSN" });
  el({ tag: "div", dataset: { saIgnore: "" }, children: [btn] });
  env.click(btn);
  const form = el({ tag: "form", id: "payment", action: "https://ex.com/pay" });
  el({ tag: "section", dataset: { saIgnore: "" }, children: [form] });
  env.submit(form);
  const evs = env.sent();
  out.ignored_subtree = {
    clicks: evOf(evs, "$click").length,
    submits: evOf(evs, "$form_submit").length,
    body: JSON.stringify(evs).indexOf("Reveal SSN") >= 0,
  };
}

// --- dead clicks: only for controls that really did nothing
{
  const mk = () => {
    const env = boot({}, {});
    const b = el({ tag: "button", innerText: "Open menu" });
    return { env, b };
  };
  const base = mk();
  base.env.click(base.b);
  base.env.runTimers();
  out.deadclick_really_dead = evOf(base.env.sent(), "$deadclick").length;

  const attr = mk();
  attr.env.click(attr.b);
  attr.env.mutate("attributes");
  attr.env.runTimers();
  out.deadclick_class_toggle = evOf(attr.env.sent(), "$deadclick").length;

  const text = mk();
  text.env.click(text.b);
  text.env.mutate("characterData");
  text.env.runTimers();
  out.deadclick_text_update = evOf(text.env.sent(), "$deadclick").length;

  const nt = boot({}, {});
  const link = el({ tag: "a", innerText: "Follow", href: "https://twitter.com/x", protocol: "https:", target: "_blank" });
  nt.click(link);
  nt.runTimers();
  out.deadclick_new_tab = evOf(nt.sent(), "$deadclick").length;

  const mail = boot({}, {});
  const ml = el({ tag: "a", innerText: "Email", href: "mailto:a@b.c", protocol: "mailto:" });
  mail.click(ml);
  mail.runTimers();
  out.deadclick_mailto = evOf(mail.sent(), "$deadclick").length;

  const hash = mk();
  hash.env.click(hash.b);
  hash.env.loc.href = "https://example.com/app?tab=billing";
  hash.env.runTimers();
  out.deadclick_query_nav = evOf(hash.env.sent(), "$deadclick").length;
}

// --- cookieless mode still resolves flags and surveys
{
  const env = boot(
    { anonymous: true, autocapture: false },
    { flags: { flags: { checkout_v2: "b" }, measured: ["checkout_v2"] } },
  );
  let fired = null;
  env.smol.onFlags((f) => (fired = f));
  await tick();
  await tick();
  const urls = env.fetches.map((f) => f.url);
  const val = env.smol.flag("checkout_v2", "ctrl");
  const evs = env.sent();
  out.cookieless = {
    flags_fetched: urls.some((u) => u.indexOf("/v1/flags/evaluate") >= 0),
    surveys_fetched: urls.some((u) => u.indexOf("/v1/surveys/active") >= 0),
    flag_value: val,
    on_flags_fired: fired !== null,
    exposures: evOf(evs, "$feature_flag_called").length,
    warned: env.warns.filter((w) => w.indexOf("cookieless") >= 0).length,
    stored_keys: [...env.storage.keys()],
    distinct_ids: evs.map((e) => e.distinct_id),
  };
}

// --- reset() ends the visit, not just the id
{
  const env = boot({ autocapture: false }, { flags: { flags: { pro: "on" }, measured: [] } });
  await tick();
  await tick();
  env.smol.identify("u-alice");
  env.smol.track("before");
  const before = env.sent();
  const flagBefore = env.smol.flag("pro", "off");
  env.smol.reset();
  const flagAfter = env.smol.flag("pro", "off");
  const sessionKey = env.storage.has("smol_session");
  env.smol.track("after");
  const all = env.sent();
  const afterEv = all.filter((e) => e.name === "after")[0];
  const beforeEv = all.filter((e) => e.name === "before")[0];
  out.reset = {
    session_before: beforeEv.properties.session_id,
    session_after: afterEv.properties.session_id,
    did_before: beforeEv.distinct_id,
    did_after: afterEv.distinct_id,
    flag_before: flagBefore,
    flag_after: flagAfter,
    session_key_kept: sessionKey,
    bucket_kept: env.storage.has("smol_bucket_id"),
  };
}

// --- batching: one keepalive-sized body per request, keepalive only where it belongs
{
  const env = boot({ autocapture: false }, {});
  const big = "x".repeat(4000);
  for (let i = 0; i < 120; i++) env.smol.track("e" + i, { blob: big });
  env.smol.flush();
  const reqs = env.fetches.filter((f) => f.url.indexOf("/v1/events") >= 0);
  out.batching = {
    requests: reqs.length,
    max_body: Math.max(...reqs.map((r) => r.body.length)),
    max_events: Math.max(...reqs.map((r) => JSON.parse(r.body).length)),
    keepalive_on_timer: reqs.some((r) => r.keepalive),
  };
  env.smol.track("last", {});
  (env.winListeners.pagehide || []).forEach((fn) => fn());
  const tail = env.fetches.filter((f) => f.url.indexOf("/v1/events") >= 0).pop();
  out.batching.keepalive_on_unload = tail.keepalive;

  const env2 = boot({ autocapture: false }, {});
  env2.smol.track("huge", { blob: "y".repeat(70000) });
  env2.smol.track("small", {});
  env2.smol.flush();
  const bodies = env2.fetches.filter((f) => f.url.indexOf("/v1/events") >= 0).map((r) => r.body).join("");
  out.batching.oversize_dropped = bodies.indexOf("\"huge\"") < 0;
  out.batching.small_still_sent = bodies.indexOf("\"small\"") >= 0;
}

console.log(JSON.stringify(out, null, 2));
`

// sdkObserved is what the harness reports back: raw measurements, no judgements.
type sdkObserved struct {
	IdentifyReturning struct {
		DistinctID string `json:"distinct_id"`
		AnonPrev   string `json:"anon_prev"`
	} `json:"identify_returning"`
	IdentifyFirstLogin struct {
		AnonPrev string `json:"anon_prev"`
	} `json:"identify_first_login"`
	ClickCheckbox struct {
		Text         string `json:"text"`
		ChainText    string `json:"chain_text"`
		BodyHasEmail bool   `json:"body_has_email"`
	} `json:"click_checkbox"`
	ClickSubmitCaption string `json:"click_submit_caption"`
	ClickHref          struct {
		Query  string `json:"query"`
		Chain  string `json:"chain"`
		Mailto string `json:"mailto"`
	} `json:"click_href"`
	IgnoredSubtree struct {
		Clicks  int  `json:"clicks"`
		Submits int  `json:"submits"`
		Body    bool `json:"body"`
	} `json:"ignored_subtree"`
	DeadclickReallyDead  int `json:"deadclick_really_dead"`
	DeadclickClassToggle int `json:"deadclick_class_toggle"`
	DeadclickTextUpdate  int `json:"deadclick_text_update"`
	DeadclickNewTab      int `json:"deadclick_new_tab"`
	DeadclickMailto      int `json:"deadclick_mailto"`
	DeadclickQueryNav    int `json:"deadclick_query_nav"`
	Cookieless           struct {
		FlagsFetched   bool     `json:"flags_fetched"`
		SurveysFetched bool     `json:"surveys_fetched"`
		FlagValue      string   `json:"flag_value"`
		OnFlagsFired   bool     `json:"on_flags_fired"`
		Exposures      int      `json:"exposures"`
		Warned         int      `json:"warned"`
		StoredKeys     []string `json:"stored_keys"`
		DistinctIDs    []string `json:"distinct_ids"`
	} `json:"cookieless"`
	Batching struct {
		Requests          int  `json:"requests"`
		MaxBody           int  `json:"max_body"`
		MaxEvents         int  `json:"max_events"`
		KeepaliveOnTimer  bool `json:"keepalive_on_timer"`
		KeepaliveOnUnload bool `json:"keepalive_on_unload"`
		OversizeDropped   bool `json:"oversize_dropped"`
		SmallStillSent    bool `json:"small_still_sent"`
	} `json:"batching"`
	Reset struct {
		SessionBefore  string `json:"session_before"`
		SessionAfter   string `json:"session_after"`
		DidBefore      string `json:"did_before"`
		DidAfter       string `json:"did_after"`
		FlagBefore     string `json:"flag_before"`
		FlagAfter      string `json:"flag_after"`
		SessionKeyKept bool   `json:"session_key_kept"`
		BucketKept     bool   `json:"bucket_kept"`
	} `json:"reset"`
}

func runSDK(t *testing.T) sdkObserved {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — sdkStaticGuards still covers the same fixes at source level")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(script, []byte(sdkHarness), 0o600); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	sdk, err := filepath.Abs("sdk.js")
	if err != nil {
		t.Fatalf("abs sdk.js: %v", err)
	}
	out, err := exec.Command(node, script, sdk).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("harness failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("harness failed: %v", err)
	}
	var obs sdkObserved
	if err := json.Unmarshal(out, &obs); err != nil {
		t.Fatalf("decode harness output: %v\n%s", err, out)
	}
	return obs
}

// identify() used to send $anon_distinct_id whenever the previous id differed from the new one.
// The previous id is whatever distinctId() last resolved — and on a returning browser that is the
// PREVIOUS ACCOUNT'S id, because identify() writes it into the same localStorage key distinctId()
// reads. So a shared laptop, or any app-side logout that never calls reset(), sent
// $identify{$anon_distinct_id:"u-alice"} for u-bob; alias.Add only refuses empty/self/"$anon", so
// the edge was accepted and written to disk, and every historical Alice event was rewritten to Bob
// at read time. Two humans merged into one, permanently.
func TestIdentifyNeverAliasesOneAccountOntoAnother(t *testing.T) {
	obs := runSDK(t)
	if obs.IdentifyReturning.DistinctID != "u-bob" {
		t.Fatalf("second login did not take effect: distinct_id=%q", obs.IdentifyReturning.DistinctID)
	}
	if obs.IdentifyReturning.AnonPrev != "" {
		t.Errorf("identify() sent $anon_distinct_id=%q for u-bob — that is a real account id, and the "+
			"server will alias every one of that user's events onto this one", obs.IdentifyReturning.AnonPrev)
	}
	// the stitch that MUST survive: anonymous browsing before a first login
	if !strings.HasPrefix(obs.IdentifyFirstLogin.AnonPrev, "a-") {
		t.Errorf("first login lost the pre-login breadcrumb (got %q) — the anonymous journey no longer "+
			"joins the account", obs.IdentifyFirstLogin.AnonPrev)
	}
}

// isClickable deliberately admits input[type=checkbox|radio], an <input> has no innerText, and the
// capture chain was `innerText || value` — so a plain multi-select over records
// (<input type=checkbox value="jane@corp.com">) shipped that address as $click.text and again
// inside $elements[0].text, stored raw and shown in recent_events and every session timeline. The
// SDK's own comment three lines up promised "metadata only, never input values".
func TestClickCaptureShipsLabelsNotValues(t *testing.T) {
	obs := runSDK(t)
	if obs.ClickCheckbox.Text != "" || obs.ClickCheckbox.ChainText != "" || obs.ClickCheckbox.BodyHasEmail {
		t.Errorf("clicking a checkbox exfiltrated its value: text=%q chain=%q whole-event-contains-it=%v",
			obs.ClickCheckbox.Text, obs.ClickCheckbox.ChainText, obs.ClickCheckbox.BodyHasEmail)
	}
	// a submit button's value IS its rendered caption — author-set copy, not the visitor's data —
	// so dropping it would make every <input type=submit> click anonymous in the dashboard
	if obs.ClickSubmitCaption != "Sign up" {
		t.Errorf("lost the caption of <input type=submit value=\"Sign up\">: %q", obs.ClickSubmitCaption)
	}
}

// href carried whatever the link carried: password-reset and magic-link tokens live in the query
// string, and mailto:/tel: put a person in the href itself. All of it landed verbatim in
// $click.href and $elements[*].href, then in the event store and every shared session timeline.
func TestClickHrefDropsTokensAndAddresses(t *testing.T) {
	obs := runSDK(t)
	if strings.Contains(obs.ClickHref.Query, "SECRET") || strings.Contains(obs.ClickHref.Query, "?") {
		t.Errorf("$click.href kept the query string: %q — a reset token is now in the event store", obs.ClickHref.Query)
	}
	if strings.Contains(obs.ClickHref.Chain, "SECRET") {
		t.Errorf("$elements[].href kept the query string: %q", obs.ClickHref.Chain)
	}
	if strings.Contains(obs.ClickHref.Mailto, "@") {
		t.Errorf("$click.href kept a mailto address: %q", obs.ClickHref.Mailto)
	}
	if !strings.HasPrefix(obs.ClickHref.Query, "https://ex.com/reset") {
		t.Errorf("stripped too much — which link was clicked is no longer identifiable: %q", obs.ClickHref.Query)
	}
}

// The documented subtree opt-out was checked inside the same walk that hunts for the clickable
// target, on the same node, with a break on the first clickable one. So
// <div data-sa-ignore><button>Reveal SSN</button></div> matched the button first and the ancestor
// carrying the opt-out was never examined: the opt-out worked only for clicks on non-interactive
// filler, i.e. exactly the clicks nobody needed protected. $form_submit had the same shape.
func TestDataSaIgnoreCoversClickableDescendants(t *testing.T) {
	obs := runSDK(t)
	if obs.IgnoredSubtree.Clicks != 0 {
		t.Errorf("captured %d click(s) inside a data-sa-ignore subtree", obs.IgnoredSubtree.Clicks)
	}
	if obs.IgnoredSubtree.Submits != 0 {
		t.Errorf("captured %d form submit(s) inside a data-sa-ignore subtree", obs.IgnoredSubtree.Submits)
	}
	if obs.IgnoredSubtree.Body {
		t.Error("the ignored subtree's own text left the browser")
	}
}

// $deadclick renders to the user as "people clicked something that did nothing", so a false
// positive sends a builder to fix a control that works. The observer watched childList only and
// compared location.pathname, which made all of these read as dead: a CSS-class toggle (attributes
// mutation), a React text update (characterData), a target=_blank or mailto: link, and a tab or
// filter that only rewrites the query string.
func TestDeadClickOnlyFiresOnControlsThatReallyDidNothing(t *testing.T) {
	obs := runSDK(t)
	if obs.DeadclickReallyDead != 1 {
		t.Fatalf("a genuinely dead click stopped being reported (%d) — the signal itself is gone", obs.DeadclickReallyDead)
	}
	for _, c := range []struct {
		got  int
		what string
	}{
		{obs.DeadclickClassToggle, "a control that toggled a CSS class (dropdown/modal/accordion)"},
		{obs.DeadclickTextUpdate, "a control whose React text updated in place"},
		{obs.DeadclickNewTab, "a link that opened a new tab"},
		{obs.DeadclickMailto, "a mailto: link"},
		{obs.DeadclickQueryNav, "a tab/filter that changed only the query string"},
	} {
		if c.got != 0 {
			t.Errorf("$deadclick fired %d time(s) for %s — the product would tell the builder to fix it", c.got, c.what)
		}
	}
}

// With { anonymous: true } distinctId() returns the "$anon" sentinel and never assigns the
// module-level `did`, and both fetchFlags and fetchSurveys were gated on `did`. So on a cookieless
// site flag() returned the caller's default for 100% of visitors, onFlags(cb) never fired (an app
// that gates its render on it never rendered), and no exposure was ever logged — the A/B read
// reported "no exposures yet" as though the SDK were healthy. Nothing was logged.
func TestCookielessModeStillResolvesFlagsAndSurveys(t *testing.T) {
	obs := runSDK(t)
	if !obs.Cookieless.FlagsFetched {
		t.Error("cookieless mode never requested /v1/flags/evaluate — every visitor silently gets the default")
	}
	if !obs.Cookieless.SurveysFetched {
		t.Error("cookieless mode never requested /v1/surveys/active")
	}
	if obs.Cookieless.FlagValue != "b" {
		t.Errorf("flag() returned %q, not the server's resolved value — the experiment never runs", obs.Cookieless.FlagValue)
	}
	if !obs.Cookieless.OnFlagsFired {
		t.Error("onFlags(cb) never fired in cookieless mode — an app gating its render on it never renders")
	}
	if obs.Cookieless.Exposures != 1 {
		t.Errorf("logged %d exposures for a measured flag, want 1", obs.Cookieless.Exposures)
	}
	if obs.Cookieless.Warned != 1 {
		t.Errorf("read a measured flag in cookieless mode and warned %d times — bucketing cannot be "+
			"device-stable here, and saying nothing lets someone read a lift off it", obs.Cookieless.Warned)
	}
	// the whole promise of cookieless mode: nothing on the visitor's device, so no banner
	if len(obs.Cookieless.StoredKeys) != 0 {
		t.Errorf("cookieless mode wrote %v to localStorage — that is the identifier the mode exists to avoid", obs.Cookieless.StoredKeys)
	}
	for _, id := range obs.Cookieless.DistinctIDs {
		if id != "$anon" {
			t.Errorf("cookieless event carried distinct_id %q instead of the $anon sentinel", id)
		}
	}
}

// reset() cleared smol_did and nothing else, so `sess` survived in memory AND in localStorage and
// ensureSession() kept reusing it for the rest of the 30-minute window. web.go keys entry/exit
// pages on session_id alone, so the next visitor joined the logged-out user's session and one
// person's landing page was attributed to the other's visit. The flag caches survived too:
// flagExposed still marked measured flags as exposed, so the new visitor's variant was never
// logged while their conversions still landed.
func TestResetEndsTheVisitNotJustTheID(t *testing.T) {
	obs := runSDK(t)
	if obs.Reset.SessionAfter == obs.Reset.SessionBefore {
		t.Errorf("session_id survived reset() (%q) — two people are now one visit in entry/exit pages",
			obs.Reset.SessionAfter)
	}
	if obs.Reset.SessionKeyKept {
		t.Error("smol_session stayed in localStorage after reset() — the next page load resumes the old visit")
	}
	if obs.Reset.DidAfter == obs.Reset.DidBefore || !strings.HasPrefix(obs.Reset.DidAfter, "a-") {
		t.Errorf("reset() did not mint a fresh anonymous id: %q -> %q", obs.Reset.DidBefore, obs.Reset.DidAfter)
	}
	if obs.Reset.FlagBefore != "on" {
		t.Fatalf("flag was not resolved before reset(): %q", obs.Reset.FlagBefore)
	}
	if obs.Reset.FlagAfter != "off" {
		t.Errorf("flag() still answered %q after reset() — the logged-out user's resolved values are "+
			"being served to the next visitor, and their exposure is deduped away", obs.Reset.FlagAfter)
	}
	// bucketing is per browser BY DESIGN — re-minting it on logout reshuffles a running experiment
	if !obs.Reset.BucketKept {
		t.Error("reset() dropped smol_bucket_id — logging out now re-buckets the browser mid-experiment")
	}
}

// Source-level net for the same fixes, so a machine without node still fails loudly if one of them
// is undone. Behaviour is asserted above; this only pins the shapes that are easy to regress.
func TestSDKStaticGuards(t *testing.T) {
	b, err := os.ReadFile("sdk.js")
	if err != nil {
		t.Fatalf("read sdk.js: %v", err)
	}
	src := string(b)
	for _, c := range []struct{ want, why string }{
		{`prev.indexOf("a-") === 0`, "identify() must only stitch SDK-minted anonymous ids, or it aliases two real accounts together"},
		{"if (ignored(e.target)) return;", "data-sa-ignore must be checked over the whole ancestor chain, before a clickable child short-circuits it"},
		{"attributes: true, characterData: true", "$deadclick must watch attribute and text mutations, or working controls are reported dead"},
		{"if (handsOff(target)) return;", "$deadclick must not arm on links that leave the document by design"},
		{"var id = distinctId();", "fetchFlags must gate on the resolved id, not on `did`, or cookieless mode never fetches flags"},
		{`localStorage.removeItem("smol_session")`, "reset() must end the session, or two visitors share one session_id"},
	} {
		if !strings.Contains(src, c.want) {
			t.Errorf("sdk.js no longer contains %q: %s", c.want, c.why)
		}
	}
	if strings.Contains(src, "target.innerText || target.value") {
		t.Error("the click handler reads an input's value again — every checkbox ships its value attribute")
	}
}

// A keepalive request whose body exceeds 64KiB fails immediately with a TypeError and never
// reaches the network — so there is no HTTP response, the warn path never runs, and .catch()
// requeues the identical oversized body for the 3s timer to retry forever. flush() used to splice
// the WHOLE queue into one body and set keepalive on EVERY flush: twenty autocaptured $click
// events with $elements chains already clear the ceiling, and one offline period grew the queue
// to the 1000 cap. Every event on that page was lost, silently. Chunking alone is not the fix
// either — the unload flushes are the one place the request MUST outlive the document.
func TestFlushStaysUnderTheKeepaliveCeiling(t *testing.T) {
	obs := runSDK(t)
	if obs.Batching.MaxBody > 60000 {
		t.Errorf("largest request body was %d bytes — over the 64KiB keepalive ceiling, so the browser "+
			"rejects it before it leaves and every event behind it is lost", obs.Batching.MaxBody)
	}
	if obs.Batching.MaxEvents > 50 {
		t.Errorf("one request carried %d events; batches are capped at 50", obs.Batching.MaxEvents)
	}
	if obs.Batching.KeepaliveOnTimer {
		t.Error("a routine timer flush asked for keepalive — that is what imposes the 64KiB ceiling " +
			"on a request that has no reason to outlive the document")
	}
	if !obs.Batching.KeepaliveOnUnload {
		t.Error("the pagehide flush did NOT ask for keepalive — the browser cancels it as the document " +
			"goes away, so the last events of every visit (including $engagement) never arrive")
	}
	if !obs.Batching.OversizeDropped || !obs.Batching.SmallStillSent {
		t.Errorf("a single event larger than the whole ceiling wedged the queue: dropped=%v "+
			"later-events-still-sent=%v", obs.Batching.OversizeDropped, obs.Batching.SmallStillSent)
	}
}
