package instrument

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WE MAINTAIN THE ANALYTICS THEY ALREADY HAVE, NOT A SECOND ONE BESIDE IT.
//
// Every emitter in this package wrote `smolanalytics.track(...)`, and every detector recognised
// only `smolanalytics.track` or `/v1/events`. Together those two facts produced the worst possible
// output for the exact customer we sell to. Measured on a fixture whose posthog.capture() call sits
// one line below each action, before this change:
//
//	"2 of 2 user-facing actions have no tracking near them (0% covered)"
//	PROPOSE src/checkout.tsx:4  -> smolanalytics.track("checkout", ...)
//	PROPOSE src/checkout.tsx:10 -> smolanalytics.track("signup", ...)
//
// Both actions were tracked. Applying that proposal would have fired two SDKs at every checkout and
// double-counted their conversions, and the report told them their instrumentation was worthless
// on the way in. This file is the standing guard on both halves: see what they use, write into it.

// posthogRepo is a repository that already has working PostHog tracking.
func posthogRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"shop","dependencies":{"posthog-js":"^1.100.0","next":"^14"}}`)
	write(t, root, "src/checkout.tsx", `import posthog from "posthog-js";

export async function handleCheckout(cart) {
  const session = await stripe.checkout.sessions.create({ line_items: cart });
  posthog.capture("checkout_started", { value: cart.total });
  return session;
}

export async function signUp(email) {
  const user = await auth.signUp({ email });
  posthog.capture("signed_up", { plan: "free" });
  return user;
}
`)
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectVendorReadsTheRepoRatherThanAsking(t *testing.T) {
	cases := []struct {
		name, manifest, source, want string
	}{
		{"posthog", `{"dependencies":{"posthog-js":"1.0.0"}}`, `posthog.capture("x")`, "posthog"},
		{"mixpanel", `{"dependencies":{"mixpanel-browser":"2.0.0"}}`, `mixpanel.track("x")`, "mixpanel"},
		{"amplitude", `{"dependencies":{"@amplitude/analytics-browser":"2.0.0"}}`, `amplitude.track("x")`, "amplitude"},
		{"segment", `{"dependencies":{"@segment/analytics-next":"1.0.0"}}`, `analytics.track("x")`, "segment"},
		{"ga4", `{"dependencies":{"next":"14"}}`, `gtag("event", "x")`, "ga4"},
		{"none", `{"dependencies":{"next":"14"}}`, `const x = 1;`, "smolanalytics"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "package.json", c.manifest)
			write(t, root, "app/page.tsx", c.source)
			if got := DetectVendor(root).ID; got != c.want {
				t.Fatalf("DetectVendor = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAManifestOutweighsALeftoverCall(t *testing.T) {
	// A team that migrated to PostHog still has the old mixpanel.track() sitting in three files.
	// The dependency they installed is the decision; the stale call is debris. Getting this
	// backwards means we write into the SDK they are removing.
	root := t.TempDir()
	write(t, root, "package.json", `{"dependencies":{"posthog-js":"1.0.0"}}`)
	write(t, root, "src/a.tsx", `mixpanel.track("old_a")`)
	write(t, root, "src/b.tsx", `mixpanel.track("old_b")`)
	write(t, root, "src/c.tsx", `mixpanel.track("old_c")`)
	if got := DetectVendor(root).ID; got != "posthog" {
		t.Fatalf("DetectVendor = %q, want posthog: the installed dependency should outweigh stale calls", got)
	}
}

func TestCoverageSeesTrackingWrittenInAnySDK(t *testing.T) {
	// The headline claim of the free audit. If this is wrong, the first thing a prospect with
	// existing analytics ever sees from us is a confident falsehood about their own codebase.
	root := posthogRepo(t)
	r := Report(root)
	if r.Uncovered != 0 || r.Covered != 2 {
		t.Fatalf("covered=%d uncovered=%d, want 2/0 — every action here has posthog.capture() one line below it\nheadline: %s",
			r.Covered, r.Uncovered, r.Headline)
	}
	// And it must name the call that ACTUALLY covers each one. Both PostHog calls are inside the
	// same 12-line window, so a top-down scan attributes the signup to the checkout's call — a real
	// line, measuring something else. "Covered by X" is only worth printing if X is checkable.
	want := map[int]string{4: "checkout_started", 10: "signed_up"}
	for _, a := range r.Actions {
		if w, ok := want[a.Line]; ok && a.CoveredBy != w {
			t.Errorf("%s:%d covered_by = %q, want %q — the nearest tracking call, not the first in the file",
				a.File, a.Line, a.CoveredBy, w)
		}
	}
}

func TestProposalWritesTheirSDKNotOurs(t *testing.T) {
	root := t.TempDir()
	// PostHog installed, and one action that genuinely has no tracking near it.
	write(t, root, "package.json", `{"dependencies":{"posthog-js":"1.0.0","next":"14"}}`)
	write(t, root, "app/join.tsx", `import posthog from "posthog-js";

export async function join(email) {
  const user = await auth.signUp({ email });
  return user;
}
`)
	p := Propose(root, "https://h.example", "wk_123")
	if p.Vendor.ID != "posthog" {
		t.Fatalf("proposal vendor = %q, want posthog", p.Vendor.ID)
	}
	if len(p.Events) == 0 {
		t.Fatal("no call-sites proposed; the fixture has an untracked signup")
	}
	for _, e := range p.Events {
		if !strings.Contains(e.Snippet, "posthog.capture") {
			t.Errorf("%s:%d proposes %q — this repo uses PostHog", e.File, e.Line, e.Snippet)
		}
		if strings.Contains(e.Snippet, "smolanalytics") {
			t.Errorf("%s:%d proposes our SDK into a PostHog codebase: %q", e.File, e.Line, e.Snippet)
		}
	}
	// And nothing to install: their SDK is already initialised. Telling a PostHog user to paste our
	// script tag is asking them to adopt a second analytics tool to get their first fix.
	if strings.Contains(p.Snippet.Code, "smolanalytics.init") {
		t.Errorf("base snippet installs our SDK into a repo that already has PostHog:\n%s", p.Snippet.Code)
	}
}

func TestNoSecondCallBesideAWorkingOne(t *testing.T) {
	// The double-count. ScanCallSites skipped a line that WAS a tracking call, but an action and
	// its tracking are almost never on the same line — the call sits one line below. So every
	// already-instrumented action got a proposal anyway, and applying the proposal fires two SDKs
	// at one checkout. Silent, and it corrupts the number the customer runs the business on.
	root := posthogRepo(t)
	p := Propose(root, "https://h.example", "wk_123")
	if len(p.Events) != 0 {
		t.Fatalf("proposed %d call-sites in a repo where every action already has posthog.capture() one line below it: %+v",
			len(p.Events), p.Events)
	}
}

func TestProposalStillWritesOursWhenTheyHaveNothing(t *testing.T) {
	// The fallback has to keep working, or we have traded one broken audience for another.
	root := t.TempDir()
	write(t, root, "package.json", `{"dependencies":{"next":"14"}}`)
	write(t, root, "app/join.tsx", `export async function join(email) {
  const user = await auth.signUp({ email });
  return user;
}
`)
	p := Propose(root, "https://h.example", "wk_123")
	if p.Vendor.ID != "smolanalytics" {
		t.Fatalf("vendor = %q, want smolanalytics for a repo with no analytics", p.Vendor.ID)
	}
	if !strings.Contains(p.Snippet.Code, "smolanalytics.init") {
		t.Errorf("no install snippet for a repo with no analytics:\n%s", p.Snippet.Code)
	}
	for _, e := range p.Events {
		if !strings.Contains(e.Snippet, "smolanalytics.track") {
			t.Errorf("%s:%d proposes %q, want our track() call", e.File, e.Line, e.Snippet)
		}
	}
}

func TestGA4EventNameIsReadFromTheSecondArgument(t *testing.T) {
	// gtag("event", "signup") puts the literal "event" where PostHog puts the name. A positional
	// guess records every GA4 event in the tracking plan as "event", and then the break detector
	// watches a metric that does not exist.
	name, ok := EventNameOn(`  gtag("event", "purchase", { value: 12 });`)
	if !ok || name != "purchase" {
		t.Fatalf("EventNameOn(gtag) = %q, %v; want \"purchase\", true", name, ok)
	}
	for _, c := range []struct{ line, want string }{
		{`posthog.capture("signed_up", {})`, "signed_up"},
		{`mixpanel.track("checkout_started")`, "checkout_started"},
		{`amplitude.track("invited")`, "invited"},
		{`analytics.track("shared")`, "shared"},
		{`smolanalytics?.track("signup")`, "signup"},
	} {
		got, ok := EventNameOn(c.line)
		if !ok || got != c.want {
			t.Errorf("EventNameOn(%q) = %q, %v; want %q", c.line, got, ok, c.want)
		}
	}
}

func TestTheBreakDetectorWatchesTheirCalls(t *testing.T) {
	// Wired() backs the tracking-break loop: notice a track() call deleted in a refactor and open
	// the PR that puts it back. It recognised only our SDK, so for a PostHog customer the flagship
	// self-healing feature was inert — it could not see the call, so it could never see it vanish.
	root := posthogRepo(t)
	got := Wired(root, []string{"signed_up", "checkout_started"})
	if len(got) != 2 {
		t.Fatalf("Wired found %d of 2 posthog-tracked events: %+v", len(got), got)
	}
	if got["signed_up"].File != "src/checkout.tsx" {
		t.Errorf("signed_up located at %q", got["signed_up"].File)
	}
}

func TestPlanSyncReadsEveryVendorsEvents(t *testing.T) {
	root := posthogRepo(t)
	all := FindAllTracked(root)
	for _, want := range []string{"signed_up", "checkout_started"} {
		if _, ok := all[want]; !ok {
			t.Errorf("FindAllTracked missed %q; the generated tracking plan would omit it: %+v", want, all)
		}
	}
}

func TestBackendCallsUseTheirSDKOrSayNothing(t *testing.T) {
	// A Python call rendered with JS object literals is not paste-ready, and our POST body in a
	// PostHog codebase is the migration we are refusing to ask for. Where we have no confident
	// idiom the answer is to say so, never to substitute ours.
	ph := vendorByID(t, "posthog")
	got, ok := ph.ServerCall("python", "signed_up", []string{"plan"})
	if !ok {
		t.Fatal("no python idiom for PostHog, which has an official python SDK")
	}
	if !strings.Contains(got, "posthog.capture(distinct_id=") || strings.Contains(got, "{ plan:") {
		t.Errorf("python call is not python: %s", got)
	}

	// GA4 deliberately has none: it has no server SDK, only the Measurement Protocol, which needs
	// an API secret nobody gave us. A guess there is a call that runs and records nothing.
	if _, ok := vendorByID(t, "ga4").ServerCall("python", "x", nil); ok {
		t.Error("GA4 claims a python idiom; it has no server SDK and we hold no API secret")
	}
}

func vendorByID(t *testing.T, id string) Vendor {
	t.Helper()
	for _, v := range vendors {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no vendor %q", id)
	return Vendor{}
}
