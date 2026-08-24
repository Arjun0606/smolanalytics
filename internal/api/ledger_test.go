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
	now := time.Now().UTC()
	inv := s.investigation(evs, now)
	if len(inv.BelowFloor) == 0 {
		t.Fatal("the fixture produced no below-floor note, so this guard cannot check that they " +
			"became first-class rows")
	}
	need := inv.BelowFloor[0].NeedPerDay

	// Asserted on the COMPOSED ledger, not on a rendered page. The dashboard that used to render
	// this is gone — the instance is a data layer now — but the property was never about markup:
	// an empty ledger must answer with the conditions being checked, not with a reassuring
	// sentence. /v1/investigate serves exactly this, so this is still the thing a reader sees.
	led := s.desk(evs, now).Ledger
	var armed, cold []string
	for _, w := range led.Standing {
		line := w.Subject + " " + w.Sub()
		if w.Armed {
			armed = append(armed, line)
		} else {
			cold = append(cold, line)
		}
	}
	all := strings.Join(append(append([]string{}, armed...), cold...), "\n")

	if len(cold) == 0 {
		t.Error("an instance with nothing configured has no NOT-ARMED rows at all — the empty " +
			"ledger is back to being an absence rather than an explanation of what it would take")
	}
	if !strings.Contains(all, fmt.Sprintf("%d/day", need)) {
		t.Errorf("the below-floor rows do not carry the %d/day figure that would let the metric "+
			"speak; without the number the row is an apology, which is the one thing it may not be", need)
	}
	if !strings.Contains(all, "step-change detection") {
		t.Error("nothing is armed for step-change detection, so an instance with events and no " +
			"configuration has an empty ledger with nothing standing in it")
	}
	// and the reader's OWN event names, not a generic sentence about metrics
	if !strings.Contains(all, "waitlist_join") {
		t.Error("the standing orders do not name the reader's own events — the entire persuasive " +
			"force of this screen is that these are their numbers, not an illustration")
	}
	for _, banned := range []string{"all clear", "nothing to see here", "acts will appear here", "coming soon"} {
		if strings.Contains(strings.ToLower(all), banned) {
			t.Errorf("the ledger says %q", banned)
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
