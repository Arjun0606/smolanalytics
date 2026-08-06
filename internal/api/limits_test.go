package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// post sends a raw ingest body and returns the status and body.
func postEvents(t *testing.T, srv *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// THE VULNERABILITY. The write key is public — it ships in the SDK on every customer page,
// because the browser needs it to send events. Before this cap the whole per-event validation was
// "name non-empty, distinct_id non-empty", so any visitor to a customer's site could read the key
// out of the page and post well-formed, authenticated events with megabyte property values until
// the box died. Valid traffic, documented endpoint, no exploit required — and a 50KB string
// property costs ~56KB RESIDENT for the life of the process.
func TestAnEnormousPropertyIsRefusedNotStored(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	huge := strings.Repeat("A", 200<<10) // 200KB in one property
	body, _ := json.Marshal([]map[string]any{{
		"name": "pageview", "distinct_id": "u1",
		"properties": map[string]any{"payload": huge},
	}})
	code, _ := postEvents(t, srv, string(body))
	if code != 413 {
		t.Fatalf("a 200KB property was accepted with %d — the box is one loop away from dying", code)
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 0 {
		t.Fatalf("the oversized event was STORED (%d events) — rejecting after writing is not rejecting", len(evs))
	}
}

// The rejection has to name the property. "Event too large" sends a developer reading their whole
// tracking plan; naming the key ends the search.
func TestTheRejectionNamesWhatWasTooBig(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	body, _ := json.Marshal([]map[string]any{{
		"name": "checkout", "distinct_id": "u1",
		"properties": map[string]any{"cart_html": strings.Repeat("x", 20<<10)},
	}})
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "cart_html") {
		t.Fatalf("the error must name the offending property, got %q", msg)
	}
	if !strings.Contains(msg, "checkout") {
		t.Fatalf("the error must name the event, got %q", msg)
	}
}

// Many medium values that individually pass but together are enormous. A per-property cap alone
// leaves this open, which is why the whole-event size is checked too.
func TestManyMediumPropertiesAreCaughtByTheWholeEventCap(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	props := map[string]any{}
	for i := 0; i < 30; i++ { // 30 x 4KB = 120KB, each value under the 8KB per-property cap
		props[string(rune('a'+i%26))+string(rune('0'+i/26))] = strings.Repeat("y", 4<<10)
	}
	body, _ := json.Marshal([]map[string]any{{"name": "e", "distinct_id": "u1", "properties": props}})
	code, _ := postEvents(t, srv, string(body))
	if code != 413 {
		t.Fatalf("120KB spread across passing properties was accepted with %d", code)
	}
}

func TestAThousandKeysIsNotAnEvent(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()
	props := map[string]any{}
	for i := 0; i < 1000; i++ {
		props["k"+string(rune(i))] = 1
	}
	body, _ := json.Marshal([]map[string]any{{"name": "e", "distinct_id": "u1", "properties": props}})
	if code, _ := postEvents(t, srv, string(body)); code != 413 {
		t.Fatalf("1000 properties accepted with %d", code)
	}
}

// The cap must not touch normal traffic. A cap that rejects real events is worse than no cap,
// because the failure is silent data loss rather than a visible outage.
func TestOrdinaryEventsStillPass(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	body, _ := json.Marshal([]map[string]any{
		{"name": "$pageview", "distinct_id": "u1", "properties": map[string]any{
			"path": "/pricing", "referrer": "https://news.ycombinator.com/", "device": "desktop",
			"title": strings.Repeat("a long but sane page title ", 20),
		}},
		{"name": "checkout", "distinct_id": "u1", "properties": map[string]any{
			"amount": 30, "currency": "USD", "items": []any{"a", "b", "c"},
		}},
		// A $set bag and a nested object, both realistic.
		{"name": "$identify", "distinct_id": "u1", "properties": map[string]any{
			"$set": map[string]any{"plan": "pro", "company": "acme"},
		}},
	})
	code, _ := postEvents(t, srv, string(body))
	if code != 202 && code != 200 {
		t.Fatalf("ordinary events were rejected with %d — a cap that blocks real traffic is worse than none", code)
	}
	evs, _ := st.Range(time.Time{}, time.Time{})
	if len(evs) != 3 {
		t.Fatalf("want all 3 ordinary events stored, got %d", len(evs))
	}
}

// The SDK's endpoints must be reachable cross-origin, or the browser reports a CORS failure for
// what is really a routing mistake — and the symptom names the wrong subsystem entirely.
//
// This shipped broken: /v1/flags/definitions was added to the mux but not to isPublic(), so the
// dashboard session gate 401'd it BEFORE the handler ran. The handler's setCORS never executed,
// and the live marketing site logged "blocked by CORS policy" for an endpoint whose CORS was fine.
func TestEverySdkEndpointAnswersCrossOrigin(t *testing.T) {
	st := memory.New()
	// The session gate has to be REAL for this to test anything: with no password configured,
	// isPublic() is never consulted and the test would pass against the exact bug it exists to
	// catch. The password is read from the environment at request time.
	t.Setenv("SMOLANALYTICS_PASSWORD", "hunter2")
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	// Every path sdk.js fetches from a customer's page.
	for _, path := range []string{
		"/v1/flags/definitions",
		"/v1/flags/evaluate?distinct_id=u1",
		"/v1/surveys/active",
	} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Origin", "https://example.com")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		// The status may well be 401 (no write key here). What must NOT happen is a response with
		// no CORS header, because the browser then cannot even read the status to learn that.
		if resp.Header.Get("Access-Control-Allow-Origin") == "" {
			t.Errorf("%s answered %d with no Access-Control-Allow-Origin — a browser sees this as a "+
				"CORS failure and never learns the real reason", path, resp.StatusCode)
		}
	}
}

// "Your app is healthy" and "you never installed this" are opposite conclusions, and an empty list
// looks identical for both.
//
// An hour after pasting a snippet the second is far likelier, and the page used to assert the
// first: "an empty list here is good news, not a missing integration." Someone could walk away
// believing their app was fine while nothing was watching it.
func TestAnEmptyErrorListSaysWhetherAnythingIsWatching(t *testing.T) {
	// Never received an error.
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()
	body, _ := json.Marshal([]map[string]any{
		{"name": "$pageview", "distinct_id": "u1", "timestamp": time.Now().UTC().Format(time.RFC3339)},
	})
	r1, _ := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	r1.Body.Close()

	var out map[string]any
	mustJSON(t, getBody(t, srv, "/v1/errors?days=30"), &out)
	if out["ever_received"] != false {
		t.Fatalf("no error has ever arrived, but ever_received = %v — the pane cannot tell the "+
			"reader that nothing is watching", out["ever_received"])
	}

	// Now one arrives, long ago. The window is empty but reporting demonstrably works.
	old := time.Now().UTC().AddDate(0, 0, -90)
	body2, _ := json.Marshal([]map[string]any{
		{"name": "$exception", "distinct_id": "u1", "timestamp": old.Format(time.RFC3339),
			"properties": map[string]any{"message": "TypeError: x is not a function"}},
	})
	r2, _ := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body2)))
	r2.Body.Close()

	var out2 map[string]any
	mustJSON(t, getBody(t, srv, "/v1/errors?days=7"), &out2)
	if out2["ever_received"] != true {
		t.Fatalf("an error arrived 90 days ago, so reporting works; ever_received = %v", out2["ever_received"])
	}
}
