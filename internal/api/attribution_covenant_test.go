package api

// The attribution covenant: filtering a CONVERSION event by an ACQUISITION attribute
// (referrer/device/utm — which the SDK stamps on the landing $pageview, NOT on the signup)
// must return the SAME number on GET /v1, MCP, and the ask bar. Before the first-touch
// convergence, GET/MCP returned a silent 0 (the signup carried no referrer) while the ask
// bar/dashboard returned the real first-touch number — two surfaces in the same editor
// disagreeing on "how many signups from reddit", the flagship question, under the
// "cannot be fabricated" seal. This is THE trust bug; this test locks the fix.
import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestAttributionCovenant_AcquisitionFilterAllSurfaces(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	now := time.Now().UTC()
	ts := now.Add(-2 * time.Hour)
	var evs []event.Event
	// reddit-acquired: 20 land (referrer on pageview only), 8 sign up (signup carries NO referrer)
	for i := 0; i < 20; i++ {
		u := fmt.Sprintf("r%d", i)
		evs = append(evs, event.Event{ID: u + "p", DistinctID: u, Name: "$pageview", Timestamp: ts,
			Properties: map[string]any{"referrer": "https://www.reddit.com/", "device": "mobile"}})
		if i < 8 {
			evs = append(evs, event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: ts, Properties: map[string]any{"plan": "free"}})
		}
	}
	// google-acquired: 30 land, 15 sign up
	for i := 0; i < 30; i++ {
		u := fmt.Sprintf("g%d", i)
		evs = append(evs, event.Event{ID: u + "p", DistinctID: u, Name: "$pageview", Timestamp: ts,
			Properties: map[string]any{"referrer": "https://www.google.com/", "device": "desktop"}})
		if i < 15 {
			evs = append(evs, event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: ts, Properties: map[string]any{"plan": "pro"}})
		}
	}
	_ = st.Ingest(evs...)
	s := New(st)
	s.SetWriteKey("wk")
	s.SetReadKey("rk")
	h := s.Handler()

	// GET /v1/trends?event=signup&f=referrer:contains:reddit
	gw := httptest.NewRecorder()
	gr := httptest.NewRequest("GET", "/v1/trends?event=signup&f=referrer:contains:reddit", nil)
	gr.Header.Set("Authorization", "Bearer rk")
	h.ServeHTTP(gw, gr)
	var get struct{ Total int }
	_ = json.Unmarshal(gw.Body.Bytes(), &get)

	// MCP trends with the same filter
	mw := httptest.NewRecorder()
	mr := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","filters":[{"property":"referrer","op":"contains","value":"reddit"}]}}}`))
	mr.Header.Set("Authorization", "Bearer rk")
	mr.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(mw, mr)
	var env struct {
		Result struct{ Content []struct{ Text string } }
	}
	_ = json.Unmarshal(mw.Body.Bytes(), &env)
	var mcp struct{ Total int }
	if len(env.Result.Content) > 0 {
		_ = json.Unmarshal([]byte(env.Result.Content[0].Text), &mcp)
	}

	// ask bar (session)
	c := loginSession(t, h, "op-pass-1234")
	aw := httptest.NewRecorder()
	ar := httptest.NewRequest("POST", "/v1/ask", strings.NewReader(`{"question":"how many signups from reddit"}`))
	ar.Header.Set("Content-Type", "application/json")
	ar.AddCookie(c)
	h.ServeHTTP(aw, ar)
	var ask struct{ Answer string }
	_ = json.Unmarshal(aw.Body.Bytes(), &ask)

	if get.Total != 8 {
		t.Errorf("GET /v1 signups-from-reddit = %d, want 8 (first-touch acquisition filter)", get.Total)
	}
	if mcp.Total != 8 {
		t.Errorf("MCP signups-from-reddit = %d, want 8 — must match GET, not a silent 0", mcp.Total)
	}
	if !strings.Contains(ask.Answer, "8") {
		t.Errorf("ask bar signups-from-reddit = %q, want 8 — all four surfaces must agree", ask.Answer)
	}
}

// TestConversionRateComparison guards: "pro vs free conversion rate" compares RATES via the
// per-segment funnel, not raw signup counts — comparing counts named the wrong winner when
// the higher-signup segment converts worse.
func TestConversionRateComparison(t *testing.T) {
	now := time.Now().UTC()
	ts := now.Add(-2 * time.Hour)
	var evs []event.Event
	mk := func(id, name, plan string) {
		evs = append(evs, event.Event{ID: id + name, DistinctID: id, Name: name, Timestamp: ts, Properties: map[string]any{"plan": plan}})
	}
	for i := 0; i < 30; i++ { // free: 30 signup, 6 checkout = 20%
		mk(fmt.Sprintf("f%d", i), "signup", "free")
		if i < 6 {
			mk(fmt.Sprintf("f%d", i), "checkout", "free")
		}
	}
	for i := 0; i < 20; i++ { // pro: 20 signup, 16 checkout = 80%
		mk(fmt.Sprintf("p%d", i), "signup", "pro")
		if i < 16 {
			mk(fmt.Sprintf("p%d", i), "checkout", "pro")
		}
	}
	got := answer("pro vs free conversion rate", evs, now)
	if !strings.Contains(got, "pro 80%") || !strings.Contains(got, "free 20%") {
		t.Errorf("conversion rate comparison should be pro 80%% / free 20%% (rates, not counts): %q", got)
	}
}

// TestRetentionComparisonHonorsPeriod guards: "day-7 retention mobile vs desktop" must answer
// DAY-7, not silently substitute day-1 under the "cannot be fabricated" seal.
func TestRetentionComparisonHonorsPeriod(t *testing.T) {
	now := time.Now().UTC()
	d := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	var evs []event.Event
	// mobile users: signup 10d ago; some return day-1, fewer return day-7
	for i := 0; i < 20; i++ {
		u := fmt.Sprintf("m%d", i)
		evs = append(evs, event.Event{ID: u + "p", DistinctID: u, Name: "$pageview", Timestamp: d(10), Properties: map[string]any{"device": "mobile"}})
		evs = append(evs, event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: d(10)})
		if i < 12 { // day-1 return
			evs = append(evs, event.Event{ID: u + "r1", DistinctID: u, Name: "open", Timestamp: d(9)})
		}
		if i < 3 { // day-7 return (fewer)
			evs = append(evs, event.Event{ID: u + "r7", DistinctID: u, Name: "open", Timestamp: d(3)})
		}
	}
	got7 := answer("day-7 retention for mobile vs desktop", evs, now)
	got1 := answer("day-1 retention for mobile vs desktop", evs, now)
	if !strings.Contains(got7, "Day-7") {
		t.Errorf("`day-7 retention ...` must answer Day-7, not substitute day-1: %q", got7)
	}
	if !strings.Contains(got1, "Day-1") {
		t.Errorf("`day-1 retention ...` should answer Day-1: %q", got1)
	}
	if got7 == got1 {
		t.Errorf("day-7 and day-1 retention should differ, got identical: %q", got7)
	}
}
