package api

// THE LEDGER'S GUARDS.
//
// The desk's subject changed from "what we noticed" to "what is standing over your product, and
// what it already did". Five properties have to survive that, and every one of them was watched
// FAILING first — by putting the defect back in the source and confirming the test went red —
// because this codebase has repeatedly shipped tests that asserted an EDIT, passed, and left the
// bug live underneath.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// ---- fixtures ----------------------------------------------------------------------------

// steadyTraffic is a product that is fine: the same number of people doing the same thing every
// day for a month. No step change, so the investigation comes back quiet — which is the state
// most instances are in most days, and therefore the state the ledger has to be good at.
func steadyTraffic(t *testing.T, st *memory.Store, name string, days, perDay int) {
	t.Helper()
	now := time.Now().UTC()
	for d := 1; d <= days; d++ {
		day := now.AddDate(0, 0, -d)
		for i := 0; i < perDay; i++ {
			ev := event.Event{
				ID:         fmt.Sprintf("%s-%d-%d", name, d, i),
				Name:       name,
				DistinctID: fmt.Sprintf("u%d-%d", d, i),
				Timestamp:  day.Add(time.Duration(i) * time.Minute),
				Properties: map[string]any{"path": "/"},
			}
			if err := st.Ingest(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func pageOf(t *testing.T, s *Server) string {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %d", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ledgerOf slices the rendered page down to the ledger. Scoping matters: "pull request" is
// legitimate prose in the AI-crawler pane, and a whole-page assertion about it would either be
// permanently red or would have to be weakened until it caught nothing.
func ledgerOf(t *testing.T, html string) string {
	t.Helper()
	a := strings.Index(html, `id="ledger"`)
	b := strings.Index(html, `id="ledger-end"`)
	if a < 0 || b < 0 || b < a {
		t.Fatal("the ledger's own boundary elements are gone from the rendered page, so every guard " +
			"below is scoped to nothing. They are ELEMENTS on purpose: html/template deletes HTML " +
			"comments, so a comment sentinel is invisible here")
	}
	return html[a:b]
}

var rowRe = regexp.MustCompile(`<div class="lrow`)

// rowsOf returns each ledger row's markup, bounded by its own closing tag.
//
// Bounded by the tag, not by "up to the next row": a row's slice would otherwise swallow the
// section note that follows the last one, and both the one-evidence-link rule and the
// no-pull-request rule would be reporting the note's contents as the row's. Rows never nest and
// a row contains no <div> of its own, so the first closing tag after the opening one is its own.
func rowsOf(ledger string) []string {
	var out []string
	for _, m := range rowRe.FindAllStringIndex(ledger, -1) {
		end := strings.Index(ledger[m[0]:], "</div>")
		if end < 0 {
			end = len(ledger) - m[0]
		}
		out = append(out, ledger[m[0]:m[0]+end])
	}
	return out
}

// ---- (1) acts render on a quiet day ---------------------------------------------------------

// THE WHOLE POINT OF THE REBUILD, AS AN ASSERTION.
//
// A guardrail revert is MOST of the news precisely when the investigation is quiet: the system
// pulled a flag and nothing else happened. The previous version of this guard checked that the
// healed block sat within 900 bytes of the {{with .Movements}} anchor — a proxy for "outside the
// quiet branch" that measured template byte offsets rather than behaviour. The property is the
// same and it is still exactly right; the mechanism is obsolete. Render it instead.
func TestActsRenderOnAQuietInvestigation(t *testing.T) {
	st := memory.New()
	s := New(st)
	steadyTraffic(t, st, "signup", 30, 30)

	now := time.Now().UTC()
	if err := st.Ingest(event.Event{
		ID: "receipt-1", Name: RevertEvent, DistinctID: "$system", Timestamp: now.Add(-2 * time.Hour),
		Properties: map[string]any{
			"flag": "risky_checkout", "variant": "treatment", "guardrail": "$exception",
			"reason": "guardrail $exception failed twice for \"treatment\"", "status": "FAIL",
		},
	}); err != nil {
		t.Fatal(err)
	}

	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	inv := s.investigation(evs, now)
	if !inv.Quiet {
		t.Fatalf("the fixture is not quiet (%d findings), so this guard is not testing the case it "+
			"exists for", len(inv.Findings))
	}

	l := ledgerOf(t, pageOf(t, s))
	if !strings.Contains(l, "risky_checkout") {
		t.Error("the investigation is quiet and the ledger does not name the flag the system pulled " +
			"on its own — the single most impressive thing this product does is invisible on exactly " +
			"the day it is the entire news")
	}
	if !strings.Contains(l, "nobody was asked") {
		t.Error("the act rendered without saying it was unattended, which is the only thing that " +
			"makes it different from a human turning a flag off")
	}
}

// ---- (2) the empty ledger is populated, not apologetic ---------------------------------------

// A brand-new instance has no acts, no findings, no flags, no alerts and no plan. That is the
// normal state, and the old desk answered it with one small sentence — "Nothing needs you today"
// — which makes a claim and asks to be believed. The ledger has to answer it with ROWS: the
// conditions being checked, the ones that are not, and the number that would change that.
func TestTheEmptyLedgerRendersStandingOrdersRatherThanAnApology(t *testing.T) {
	st := memory.New()
	s := New(st)
	// enough of one metric to be swept, and a second that is genuinely below the detection floor
	steadyTraffic(t, st, "signup", 30, 30)
	steadyTraffic(t, st, "waitlist_join", 30, 2)

	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	inv := s.investigation(evs, time.Now().UTC())
	if len(inv.BelowFloor) == 0 {
		t.Fatal("the fixture produced no below-floor note, so this guard cannot check that they " +
			"became first-class rows")
	}
	need := inv.BelowFloor[0].NeedPerDay

	l := ledgerOf(t, pageOf(t, s))
	if !strings.Contains(l, "not armed") {
		t.Error("an instance with nothing configured shows no NOT ARMED rows at all — the empty " +
			"ledger is back to being an absence rather than an explanation of what it would take")
	}
	if !strings.Contains(l, fmt.Sprintf("%d/day", need)) {
		t.Errorf("the below-floor rows do not print the %d/day figure that would let the metric "+
			"speak; without the number the row is an apology, which is the one thing it may not be", need)
	}
	if !strings.Contains(l, "step-change detection") {
		t.Error("the armed section never names step-change detection, so an instance with events " +
			"and no configuration renders a ledger with nothing standing in it")
	}
	// and the reader's OWN event names, not a generic sentence about metrics
	if !strings.Contains(l, "waitlist_join") {
		t.Error("the standing orders do not name the reader's own events — the entire persuasive " +
			"force of this screen is that these are their numbers, not an illustration")
	}
	// never the sentences that make an empty state feel like a shrug
	for _, banned := range []string{"all clear", "nothing to see here", "acts will appear here", "coming soon"} {
		if strings.Contains(strings.ToLower(l), banned) {
			t.Errorf("the ledger says %q", banned)
		}
	}
}

// ---- (3) every row carries exactly one evidence link -----------------------------------------

// THE RULE THAT DEMOTES THE THIRTY-THREE PANES.
//
// Every ledger row asserts something, and every one of them owes the reader somewhere to go and
// check it — exactly one link, so a pane is always reached as the proof of a specific claim
// rather than as a menu item. Zero is an unfalsifiable assertion; two is the row arguing with
// itself about where the proof lives. Parsed from the RENDER, not the template, because the
// interesting failure is a branch that drops the link on one kind of row only.
func TestEveryLedgerRowCarriesExactlyOneEvidenceLink(t *testing.T) {
	s := ledgerRichServer(t)
	l := ledgerOf(t, pageOf(t, s))
	rows := rowsOf(l)
	if len(rows) < 3 {
		t.Fatalf("only %d ledger rows rendered; the fixture is not exercising enough row kinds "+
			"for this guard to mean anything", len(rows))
	}
	for _, r := range rows {
		n := strings.Count(r, `class="lev"`)
		if n != 1 {
			head := r
			if i := strings.Index(head, "</span>"); i > 0 {
				head = head[:i]
			}
			t.Errorf("a ledger row carries %d evidence links, want exactly 1:\n  %.200s", n, head)
		}
	}
}

// ledgerRichServer is an instance with one of everything the ledger can render: acts of both
// kinds, an open finding, a closed one, a running experiment, a declared plan, an alert, and a
// metric below the floor.
func ledgerRichServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	s := New(st)
	now := time.Now().UTC()

	steadyTraffic(t, st, "signup", 30, 30)
	steadyTraffic(t, st, "waitlist_join", 30, 2)
	// a step change: checkout collapses in the last three days
	for d := 30; d >= 1; d-- {
		per := 40
		if d <= 3 {
			per = 2
		}
		for i := 0; i < per; i++ {
			if err := st.Ingest(event.Event{
				ID: fmt.Sprintf("co-%d-%d", d, i), Name: "checkout",
				DistinctID: fmt.Sprintf("c%d-%d", d, i),
				Timestamp:  now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, e := range []event.Event{{
		ID: "rcpt-revert", Name: RevertEvent, DistinctID: "$system", Timestamp: now.Add(-3 * time.Hour),
		Properties: map[string]any{
			"flag": "risky_checkout", "guardrail": "$exception", "variant": "treatment",
			"reason": "guardrail $exception failed twice for \"treatment\"",
		},
	}, {
		ID: "rcpt-pr", Name: investigate.TrackingPrEvent, DistinctID: "$system", Timestamp: now.Add(-4 * time.Hour),
		Properties: map[string]any{
			"event": "signup_completed", "repo": "acme/web", "commit": "9f21ab0",
			"file": "src/signup.ts", "line": 42, "mode": "propose",
			"pr_url": "https://github.com/acme/web/pull/17",
		},
	}} {
		if err := st.Ingest(e); err != nil {
			t.Fatal(err)
		}
	}

	fs, err := flag.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Save(flag.Flag{
		Key: "new_checkout", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{
			Goal: "checkout", Control: "control", Mode: flag.ModeSequential, Alpha: 0.05,
			Started:    now.Add(-72 * time.Hour),
			Guardrails: []flag.Guardrail{{Event: "$exception", Direction: flag.DirectionFor("$exception")}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)

	tp, err := trackplan.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Set([]trackplan.PlannedEvent{{Name: "signup"}, {Name: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	s.SetTrackPlan(tp)

	as, err := alert.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := as.Add(alert.Alert{
		Name: "signups fall", Event: "signup", KindName: alert.KindRelative,
		Op: "lt", Threshold: 30, WindowHours: 24, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.SetAlerts(as)
	return s
}

// ---- (4) the promised action tracks the real switch ------------------------------------------

// A SAFETY SWITCH THE PAGE DOES NOT KNOW ABOUT IS WORSE THAN NO SWITCH.
//
// With SMOLANALYTICS_AUTO_REVERT=off the evaluation still runs and the pane still shows FAIL —
// we simply do not touch the flag. An operator who switched acting off and reads "we turn the
// flag off" on the first screen has been told something false about their own instance, in the
// one place the product's whole claim rests on being checkable.
func TestTheGuardrailPromiseFollowsTheAutoRevertSwitch(t *testing.T) {
	t.Setenv("SMOLANALYTICS_AUTO_REVERT", "off")
	l := ledgerOf(t, pageOf(t, ledgerRichServer(t)))
	if strings.Contains(l, "we turn the flag off") {
		t.Error("auto-revert is switched OFF and the ledger still promises to turn the flag off — " +
			"the page is advertising an action this instance will not take")
	}
	if !strings.Contains(l, "left alone") && !strings.Contains(l, "leave the flag alone") {
		t.Error("auto-revert is off and the ledger never says so; the reader has no way to tell an " +
			"armed instance from a disarmed one, which is the entire reason the switch is visible")
	}
}

// And the other direction, or the guard above passes on a page that never mentions guardrails.
func TestTheGuardrailPromiseIsMadeWhenActingIsArmed(t *testing.T) {
	t.Setenv("SMOLANALYTICS_AUTO_REVERT", "")
	l := ledgerOf(t, pageOf(t, ledgerRichServer(t)))
	if !strings.Contains(l, "we turn the flag off") {
		t.Error("acting is armed and the ledger never says what happens on a breach — a standing " +
			"order with no stated consequence is not a standing order")
	}
}

// ---- (5) no row promises a pull request that did not happen ----------------------------------

// THE PATH MOST LIKELY TO GROW A HOPEFUL SENTENCE.
//
// Opening a pull request is the CLOUD's half of the tracking restore, gated on TRACKING_AUTOFIX
// and a per-project switch this binary cannot see. On most instances it will never have run. The
// engine may therefore promise only what the engine does — raise the finding — and a row that
// says "pull request" on an instance with no $tracking_pr_opened receipt is a lie the reader
// cannot check.
func TestNoLedgerRowPromisesAPullRequestWithoutAReceipt(t *testing.T) {
	st := memory.New()
	s := New(st)
	steadyTraffic(t, st, "signup", 30, 30)

	tp, err := trackplan.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Set([]trackplan.PlannedEvent{{Name: "signup"}}); err != nil {
		t.Fatal(err)
	}
	s.SetTrackPlan(tp)
	// a hosted instance, which is the only configuration where the cloud half is even mentioned
	s.SetCloudURL("https://smolanalytics.com/projects/prj_test/setup")

	l := ledgerOf(t, pageOf(t, s))
	if !strings.Contains(l, "tracking break") {
		t.Fatal("the tracking watch did not arm on a declared plan whose event is arriving, so this " +
			"guard is watching a section that is not there")
	}
	for _, r := range rowsOf(l) {
		if strings.Contains(strings.ToLower(r), "pull request") {
			t.Errorf("a ledger row promises a pull request on an instance that has never opened "+
				"one:\n  %.240s", r)
		}
	}

	// And with a receipt, the row exists and carries the real URL — so the rule above is a
	// statement about evidence, not a blanket ban that would hide the feature entirely.
	withPR := ledgerOf(t, pageOf(t, ledgerRichServer(t)))
	if !strings.Contains(withPR, "https://github.com/acme/web/pull/17") {
		t.Error("an instance WITH a $tracking_pr_opened receipt does not link the pull request it " +
			"opened; the receipt is on the timeline and the ledger is hiding the one act that " +
			"changed the customer's repository")
	}
}
