package instrument

import (
	"encoding/json"
	"os"
	"testing"
)

// THE CLOUD'S PORT IS PINNED TO THIS FILE'S OUTPUT, NOT TO ITS OWN.
//
// smolanalytics-cloud/lib/tracking-calls.ts re-implements the call-site recognizer in TypeScript,
// because the engine walks a filesystem and the customer's repo only exists on the cloud side
// behind the GitHub App. The two halves of "tracking broke" are therefore computed in different
// processes: the engine proves the event went silent while its traffic held, the cloud proves a
// commit deleted the call site. The only thing that must agree is what a tracking call LOOKS like.
//
// If they drift, nothing throws. The engine says "signed_up is missing", the cloud says "it is
// right there", and the self-healing loop quietly stops healing — or worse, opens a PR restoring
// a call that was never gone.
//
// So the vectors are generated HERE and merely checked there. Regenerate with:
//
//	SMOLANALYTICS_VECTORS_OUT=../../smolanalytics-cloud/lib/__fixtures__/track-call-vectors.json \
//	  go test ./internal/instrument/ -run TestGenerateTrackCallVectors -count=1
//
// Never by running the TypeScript implementation: that would pin the port to itself and the whole
// guard becomes a tautology.

// vectorLines is the line set both sides must agree on. Every entry is a real shape from a real
// codebase, plus the false positives that would each, on their own, make the feature look broken.
var vectorLines = []string{
	// ours, including the optional-chaining forms agents actually generate
	`  smolanalytics.track("checkout_completed", { plan: "pro" })`,
	`smolanalytics?.track("signup")`,
	`(window as any).smolanalytics?.track("trial_started")`,
	"  smolanalytics.track(`upgrade_clicked`)",

	// the SDKs our customers already run — the whole point of the vendor work
	`  posthog.capture("signed_up", { plan: "free" })`,
	`posthog?.capture('checkout_started')`,
	`  mixpanel.track("invite_sent", { role: "member" })`,
	`  amplitude.track("file_uploaded")`,
	`  amplitude.logEvent("legacy_event")`,
	`  gtag("event", "purchase", { value: 12 })`,
	`  gtag('event', 'sign_up')`,
	`  analytics.track("shared", { channel: "email" })`,

	// NOT instrumentation: a chart naming the same event, a plan array, prose
	`<FunnelStep name="checkout_completed" />`,
	`steps: ["signup", "checkout_completed"]`,
	`const note = "we track checkout_completed here"`,
	`// posthog.capture("commented_out")`,

	// the substring hazard: analytics.track( is the tail of smolanalytics.track(, and a wrapper
	// called productAnalytics.track( is nobody's SDK
	`  productAnalytics.track("wrapper_call")`,

	// two calls on one line
	`  posthog.capture("a"); posthog.capture("b")`,
}

type vectorFile struct {
	Patterns []vectorPattern `json:"patterns"`
	Source   string          `json:"source"`
	Vectors  []vectorCase    `json:"vectors"`
}

type vectorPattern struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
}

type vectorCase struct {
	Line    string        `json:"line"`
	Matches []vectorMatch `json:"matches"`
}

type vectorMatch struct {
	Vendor string `json:"vendor"`
	Event  string `json:"event"`
}

// allVendors is the match order the port must reproduce exactly: most specific signature first,
// ours last, so a wrapper around another SDK does not claim a call that belongs to it.
func allVendors() []Vendor { return append(append([]Vendor{}, vendors...), Smolanalytics) }

// matchesOn is the reference implementation of "what is tracked on this line", and the thing the
// TypeScript port must agree with. First vendor whose pattern matches owns the call.
func matchesOn(line string) []vectorMatch {
	out := []vectorMatch{}
	for _, v := range allVendors() {
		for _, m := range v.track.FindAllStringSubmatch(line, -1) {
			out = append(out, vectorMatch{Vendor: v.ID, Event: m[1]})
		}
		if len(out) > 0 {
			break // one SDK owns a line; see the comment on vendors for the ordering rule
		}
	}
	return out
}

func TestGenerateTrackCallVectors(t *testing.T) {
	f := vectorFile{Source: "generated from internal/instrument/vendor.go by TestGenerateTrackCallVectors"}
	for _, v := range allVendors() {
		f.Patterns = append(f.Patterns, vectorPattern{ID: v.ID, Pattern: v.track.String()})
	}
	for _, line := range vectorLines {
		f.Vectors = append(f.Vectors, vectorCase{Line: line, Matches: matchesOn(line)})
	}

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')

	out := os.Getenv("SMOLANALYTICS_VECTORS_OUT")
	if out == "" {
		t.Skip("set SMOLANALYTICS_VECTORS_OUT to regenerate the cloud's fixture")
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d vectors and %d patterns to %s", len(f.Vectors), len(f.Patterns), out)
}

// TestVectorsCoverEveryVendor stops a vendor being added to the list without a line proving what
// its calls look like. A vendor with no vector is one the port can silently fail to recognise.
func TestVectorsCoverEveryVendor(t *testing.T) {
	seen := map[string]bool{}
	for _, line := range vectorLines {
		for _, m := range matchesOn(line) {
			seen[m.Vendor] = true
		}
	}
	for _, v := range allVendors() {
		if !seen[v.ID] {
			t.Errorf("no vector line produces a %s match; add one to vectorLines", v.ID)
		}
	}
}
