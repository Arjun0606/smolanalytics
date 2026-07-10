package api

// Onboarding data-path simulation. This exists because "we thought it worked, but the
// dashboard was just dead static." It simulates a freshly provisioned instance and proves
// the whole read path (usage, ask, funnel) reflects REAL ingested events: a fresh instance
// must read ZERO (no seeded/fake data), every ingest must move the numbers, and the ask bar
// must report the computed counts, not a canned answer. If any of that regresses, this fails.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestOnboardingDataPathIsLive(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	t.Cleanup(srv.Close)

	usage := func() (total, users int) {
		out := getJSONMap(t, srv, "/v1/usage")
		return int(num(out["total_events"])), int(num(out["users"]))
	}
	ask := func(q string) string {
		body, _ := json.Marshal(map[string]string{"question": q})
		resp, err := http.Post(srv.URL+"/v1/ask", "application/json", strings.NewReader(string(body)))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("ask %q failed: %v (%v)", q, err, resp)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if _, ok := out["computed_by"]; !ok {
			t.Fatalf("ask %q returned no computed_by receipt (trust story broken)", q)
		}
		ans, _ := out["answer"].(string)
		return ans
	}

	// 1. FRESH INSTANCE: must read zero. If a brand-new instance shows non-zero usage,
	//    it is serving seeded/static data, which is the exact bug we're guarding against.
	if tot, u := usage(); tot != 0 || u != 0 {
		t.Fatalf("fresh instance is NOT empty: total_events=%d users=%d (dead-static seeded data)", tot, u)
	}

	// 2. Ingest a known user journey: 100 signup, 60 activate, 30 checkout, 1 pageview each.
	ingestJourney(t, srv, 0, 100, 60, 30)

	tot, users := usage()
	const wantEvents = 100 + 60 + 30 + 100 // signup+activate+checkout+pageview
	if tot != wantEvents {
		t.Fatalf("usage total_events=%d, want %d (pipeline is not counting real ingested events)", tot, wantEvents)
	}
	if users != 100 {
		t.Fatalf("usage users=%d, want 100 (distinct-id counting is wrong)", users)
	}

	// 3. The ask bar must report the COMPUTED count, not a fixed number.
	if a := ask("how many signups?"); !strings.Contains(a, "100") {
		t.Fatalf("ask 'how many signups' did not reflect the 100 ingested: %q", a)
	}

	// 4. The funnel/drop-off ask must actually compute over the ingested funnel.
	drop := ask("where do people drop off?")
	if drop == "" || !strings.ContainsAny(drop, "0123456789") {
		t.Fatalf("drop-off ask did not compute a funnel answer: %q", drop)
	}

	// 5. ANTI-STATIC: ingest 25 more NEW users; the numbers must move by exactly the delta.
	//    A static/cached read would leave them frozen (the original bug).
	ingestJourney(t, srv, 100, 25, 0, 0)
	tot2, users2 := usage()
	if users2 != users+25 {
		t.Fatalf("users did not move on new data: %d -> %d, want +25 (STATIC read)", users, users2)
	}
	if tot2 != tot+25+25 { // 25 signup + 25 pageview
		t.Fatalf("total_events did not move correctly: %d -> %d, want +50 (STATIC read)", tot, tot2)
	}
	if a := ask("how many signups?"); !strings.Contains(a, "125") {
		t.Fatalf("ask did not reflect the new signup total of 125: %q", a)
	}
}

// ingestJourney posts a realistic funnel: nSignup users (ids offset by startID) sign up,
// the first nActivate of them activate, the first nCheckout check out, each also fires one
// pageview. Fails the test if ingestion is not accepted.
func ingestJourney(t *testing.T, srv *httptest.Server, startID, nSignup, nActivate, nCheckout int) {
	t.Helper()
	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < nSignup; i++ {
		u := fmt.Sprintf("u%d", startID+i)
		ts := now.Add(-time.Duration(i%7) * time.Hour).Format(time.RFC3339)
		batch = append(batch,
			map[string]any{"name": "signup", "distinct_id": u, "timestamp": ts, "properties": map[string]any{"path": "/"}},
			map[string]any{"name": "$pageview", "distinct_id": u, "timestamp": ts, "properties": map[string]any{"path": "/pricing", "device": "desktop"}},
		)
		if i < nActivate {
			batch = append(batch, map[string]any{"name": "activate", "distinct_id": u, "timestamp": ts})
		}
		if i < nCheckout {
			batch = append(batch, map[string]any{"name": "checkout", "distinct_id": u, "timestamp": ts, "properties": map[string]any{"amount": 29}})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest failed: %v (%v)", err, resp)
	}
}

func getJSONMap(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %v (%v)", path, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
