package api

// GET /v1/investigate — the door that lets a machine read the flagship.
//
// The tests that matter here are not "does it return 200". They are: does it return the SAME
// thing the MCP tool returns (because the cloud will act on this and a human will read that, and
// the two being different is the failure this codebase has now fixed three times), does the
// kill switch actually kill it, and is our own receipt kept out of the numbers we bill on.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
)

// trackingServer is an instance in the exact state the new finding kind describes: a planned
// event that went silent five days ago while the page it fires on kept its traffic.
func trackingServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	app := New(st)
	tp, err := trackplan.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Set([]trackplan.PlannedEvent{{Name: "checkout_completed"}}); err != nil {
		t.Fatal(err)
	}
	app.SetTrackPlan(tp)
	seedTrackingBreak(t, st)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// seedTrackingBreak writes the fixture into any store: checkout_completed at 40/day until five
// days ago, then nothing, while $pageview on the same path never wavers.
func seedTrackingBreak(t *testing.T, st *memory.Store) {
	t.Helper()
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -29).Truncate(24 * time.Hour)
	cut := now.AddDate(0, 0, -5).Truncate(24 * time.Hour)
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		n := 40
		if !d.Before(cut) {
			n = 0 // the track() call was deleted here
		}
		emit := func(name string, count int) {
			for i := 0; i < count; i++ {
				ts := d.Add(time.Duration(i) * time.Minute)
				if ts.After(now) {
					return
				}
				if err := st.Ingest(event.Event{
					ID:   fmt.Sprintf("%s-%s-%d", name, d.Format("0102"), i),
					Name: name, DistinctID: fmt.Sprintf("u%d", i), Timestamp: ts,
					Properties: map[string]any{"path": "/checkout"},
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		emit("checkout_completed", n)
		emit("$pageview", 60) // the witness: unchanged throughout
	}
}

// findingsFrom pulls the findings list out of the frozen wire shape.
func findingsFrom(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	inv, ok := body["investigation"].(map[string]any)
	if !ok {
		t.Fatalf("no investigation in the response — the wire shape changed and every cloud consumer just broke: %v", body)
	}
	raw, _ := inv["findings"].([]any)
	var out []map[string]any
	for _, f := range raw {
		if m, ok := f.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func trackingFinding(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	for _, f := range findingsFrom(t, body) {
		if f["event"] == "checkout_completed" {
			return f
		}
	}
	t.Fatalf("no finding about checkout_completed at all: %v", body["investigation"])
	return nil
}

// THE ROUTE EXISTS AND CARRIES THE CLASS THE CLOUD ACTS ON.
//
// Before this endpoint, investigate.Finding could not cross a process boundary except through
// MCP, which the control plane does not speak — so the one finding kind with a mechanical fix
// was unreachable by the only half of the system holding repository access.
func TestInvestigateRouteServesTheTrackingBreak(t *testing.T) {
	srv := trackingServer(t)
	f := trackingFinding(t, viaAPI(t, srv, "/v1/investigate"))

	if f["kind"] != string(investigate.KindTrackingBroke) {
		t.Fatalf("the route did not promote a planned event that went silent while its page held: %v", f)
	}
	for _, k := range []string{"fingerprint", "silent_since", "witness", "headline", "next_move"} {
		if s, _ := f[k].(string); s == "" {
			t.Errorf("%q is empty on the wire; the cloud reads it to decide what to do", k)
		}
	}
	if f["needs_you"] != true {
		t.Error("a tracking break must arrive flagged for a human")
	}
	cost, _ := f["cost"].(map[string]any)
	if cost["people"] != float64(0) {
		t.Errorf("the wire carries a people count for instrumentation loss: %v", cost)
	}
	if s, _ := cost["size"].(string); s == "" {
		t.Errorf("no rendered size on the wire, so a TypeScript consumer prints an empty cell: %v", cost)
	}
	if s, _ := body(t, srv, "/v1/investigate")["read_me_first"].(string); s == "" {
		t.Error("no read_me_first — that string is the contract text every agent reads first")
	}
}

func body(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	return viaAPI(t, srv, path)
}

// TWO DOORS, ONE COMPUTATION.
//
// The route and the MCP tool must hand out the identical investigation. They already share
// desk.Build, and this is what keeps that true when somebody adds a parameter to one of them.
// generated_at is the only field allowed to differ, because it is a clock reading taken twice.
func TestInvestigateRouteAndMcpToolAgree(t *testing.T) {
	srv := trackingServer(t)
	a := viaAPI(t, srv, "/v1/investigate")
	m := viaMCP(t, srv, "investigate", `{}`)

	for _, side := range []map[string]any{a, m} {
		inv, _ := side["investigation"].(map[string]any)
		if s, _ := inv["generated_at"].(string); s == "" {
			t.Fatal("an investigation with no generated_at cannot be cached or compared")
		}
		delete(inv, "generated_at")
	}
	aj, _ := json.Marshal(a)
	mj, _ := json.Marshal(m)
	if string(aj) != string(mj) {
		t.Fatalf("SURFACES DISAGREE on the investigation —\nAPI:\n%s\nMCP:\n%s", aj, mj)
	}
}

// THE KILL SWITCH. Detection off means findings stay plain regressions everywhere, so the cloud
// has nothing to act on — one variable, no redeploy.
func TestTrackingBreakDetectionCanBeSwitchedOff(t *testing.T) {
	t.Setenv("SMOLANALYTICS_TRACKING_BROKE", "off")
	srv := trackingServer(t)
	f := trackingFinding(t, viaAPI(t, srv, "/v1/investigate"))

	if f["kind"] != string(investigate.KindRegression) {
		t.Fatalf("the switch is off and the finding was promoted anyway: %v", f)
	}
	if _, ok := f["silent_since"]; ok {
		t.Errorf("a non-promoted finding carried silent_since, so the actuator would still fire: %v", f)
	}
	// And it must be OFF, not broken: the regression itself is still reported.
	if s, _ := f["headline"].(string); s == "" {
		t.Error("switching the tracking check off also switched off the regression underneath it")
	}
}

// The switch must not be a typo away from silently disabling itself. Only "off" is off.
func TestOnlyTheWordOffSwitchesDetectionOff(t *testing.T) {
	t.Setenv("SMOLANALYTICS_TRACKING_BROKE", "false")
	srv := trackingServer(t)
	f := trackingFinding(t, viaAPI(t, srv, "/v1/investigate"))
	if f["kind"] != string(investigate.KindTrackingBroke) {
		t.Fatalf("a value that is not %q disabled detection, so an operator who typed 'false' "+
			"thinks the check is running when it is not: %v", "off", f)
	}
}

// The window parameter behaves like every other report's: a typo is a 400, not a quietly
// different window, and an over-cap value clamps the same way /v1/brief and the MCP tool clamp.
func TestInvestigateWindowParamIsHonest(t *testing.T) {
	srv := trackingServer(t)
	resp, err := http.Get(srv.URL + "/v1/investigate?days=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("?days=abc returned %d — a window nobody asked for, with nothing in the body to say so", resp.StatusCode)
	}
	capped := viaAPI(t, srv, "/v1/investigate?days=500")
	ninety := viaAPI(t, srv, "/v1/investigate?days=90")
	ci, _ := capped["investigation"].(map[string]any)
	ni, _ := ninety["investigation"].(map[string]any)
	delete(ci, "generated_at")
	delete(ni, "generated_at")
	cj, _ := json.Marshal(ci)
	nj, _ := json.Marshal(ni)
	if string(cj) != string(nj) {
		t.Error("days=500 did not clamp to the same 90 the MCP tool clamps to, so the two surfaces " +
			"answer a different question for the same request")
	}
}

// OUR ROBOT IS NOT THEIR TRAFFIC.
//
// /v1/usage is what the cloud bills on. Counting the receipt we write when we open a pull
// request on somebody's behalf would charge them for work they did not ask for and inflate their
// visitor count with one synthetic id.
func TestTheTrackingReceiptIsNotBilled(t *testing.T) {
	st := memory.New()
	app := New(st)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	if err := st.Ingest(event.Event{ID: "real", Name: "signup", DistinctID: "u1", Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	before := viaAPI(t, srv, "/v1/usage")
	for i := 0; i < 5; i++ {
		if err := st.Ingest(event.Event{
			ID: fmt.Sprintf("rcpt%d", i), Name: investigate.TrackingPrEvent,
			DistinctID: "$system", Timestamp: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	after := viaAPI(t, srv, "/v1/usage")

	if before["total_events"] != after["total_events"] {
		t.Errorf("%s was billed as customer traffic: %v then %v",
			investigate.TrackingPrEvent, before["total_events"], after["total_events"])
	}
	if before["users"] != after["users"] {
		t.Errorf("the $system receipt writer was counted as a user: %v then %v", before["users"], after["users"])
	}
}

// THE PAGE MUST NOT SAY "0 PEOPLE AFFECTED".
//
// A tracking break's cost is deliberately 0 — see investigate.BasisBlind — because we did not
// measure the people, we stopped counting them. The dashboard slab used to render that zero under
// the hardcoded word "people", stating in 64px that nobody was affected by the one finding meaning
// "your numbers have stopped being true".
//
// The slab is gone with the dashboard, but the property is not about a slab: any surface that
// reads this finding must be able to tell "no people" from "we cannot say". So the assertion moved
// down to the finding itself, where every renderer gets it — the cost carries a blind basis rather
// than a measured zero.
func TestATrackingBreakIsNeverCostedAsZeroPeople(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	tp, err := trackplan.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tp.Set([]trackplan.PlannedEvent{{Name: "checkout_completed"}}); err != nil {
		t.Fatal(err)
	}
	s.SetTrackPlan(tp)
	seedTrackingBreak(t, st)

	// The finding really is the lead, or this test is watching an empty page.
	inv := s.investigation(mustRange(t, st), time.Now().UTC())
	if len(inv.Findings) == 0 || inv.Findings[0].Kind != investigate.KindTrackingBroke {
		t.Fatalf("the tracking break is not the lead finding, so the slab is rendering something else: %+v", inv.Findings)
	}

	lead := inv.Findings[0]
	// The cost must not present itself as a measured zero. BasisBlind is what lets a renderer say
	// "we stopped counting" instead of "nobody was affected".
	if lead.Cost.Basis != investigate.BasisBlind {
		t.Errorf("a tracking break is costed as %q, so a renderer cannot tell a real zero from a blind one", lead.Cost.Basis)
	}
	if strings.Contains(strings.ToLower(lead.Cost.SizeText), "people") {
		t.Errorf("the cost text claims people for loss we never measured: %q", lead.Cost.SizeText)
	}
}

func mustRange(t *testing.T, st *memory.Store) []event.Event {
	t.Helper()
	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}
