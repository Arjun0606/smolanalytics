package api

// Round-5 audit regression guards: each test pins one adversarially-verified finding.

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// TestAskPathsHonorsWindow (CRITICAL finding): "what do users do after signup in the last 24
// hours" ran the journey over ALL history while the provenance line claimed the 24h window —
// a false-provenance covenant break. The paths answer must scope to the asked window.
func TestAskPathsHonorsWindow(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 5; i++ {
		u := string(rune('a' + i))
		evs = append(evs,
			event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: now.AddDate(0, 0, -5)},
			event.Event{ID: u + "a", DistinctID: u, Name: "activate", Timestamp: now.AddDate(0, 0, -5).Add(time.Hour)})
	}
	got := answer("what do users do after signup in the last 24 hours?", evs, now)
	if strings.Contains(got, "activate") || strings.Contains(got, "5 users") {
		t.Errorf("paths must honor the 24h window (all events are 5 days old): %q", got)
	}
	// all-time phrasing still reports the journey
	all := answer("what do users do after signup?", evs, now)
	if !strings.Contains(all, "activate") {
		t.Errorf("unwindowed paths should still show the journey: %q", all)
	}
}

// TestIngestClampsAncientTimestamp (finding: one year-100 event stretched every all-time
// report's span back two millennia — ~700K zero buckets, ~30MB responses). Ingest must clamp
// ancient timestamps like it already clamps future ones.
func TestIngestClampsAncientTimestamp(t *testing.T) {
	s := New(memory.New())
	h := s.Handler()
	r := httptest.NewRequest("POST", "/v1/events", strings.NewReader(
		`[{"name":"amp","distinct_id":"a","timestamp":"0100-01-01T00:00:00Z"},{"name":"amp","distinct_id":"b"}]`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code >= 300 {
		t.Fatalf("ingest: %d", w.Code)
	}
	tw := httptest.NewRecorder()
	h.ServeHTTP(tw, httptest.NewRequest("GET", "/v1/trends?event=amp", nil))
	if tw.Code != 200 {
		t.Fatalf("trends: %d", tw.Code)
	}
	if n := tw.Body.Len(); n > 100_000 {
		t.Errorf("all-time trends after an ancient-timestamp ingest is %d bytes — the year-100 event was not clamped", n)
	}
}

// TestMeasureOverflowIsExplicit (finding: 1e308+1e308 = +Inf made GET return an EMPTY 200,
// MCP leak "json: unsupported value: +Inf", and the ask bar state "+Inf" as CI-verified fact).
// Overflow must be an explicit, structured error on every surface.
func TestMeasureOverflowIsExplicit(t *testing.T) {
	s := New(memory.New())
	h := s.Handler()
	r := httptest.NewRequest("POST", "/v1/events", strings.NewReader(
		`[{"name":"big","distinct_id":"b1","properties":{"amount":1e308}},{"name":"big","distinct_id":"b2","properties":{"amount":1e308}}]`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// GET: a real error status with a JSON body — never a 200 with an empty body.
	gw := httptest.NewRecorder()
	h.ServeHTTP(gw, httptest.NewRequest("GET", "/v1/trends?event=big&measure=sum&property=amount&days=2", nil))
	if gw.Code == 200 || gw.Body.Len() == 0 {
		t.Errorf("overflowed sum must be an explicit error, got %d with %d-byte body", gw.Code, gw.Body.Len())
	}
	if !strings.Contains(gw.Body.String(), "overflow") {
		t.Errorf("overflow error should say what happened: %s", gw.Body.String())
	}

	// MCP: a clean tool error, never a leaked Go marshal string.
	mw := httptest.NewRecorder()
	mr := httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"big","measure":"sum","property":"amount","days":2}}}`))
	mr.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(mw, mr)
	if strings.Contains(mw.Body.String(), "json: unsupported") {
		t.Errorf("MCP leaked the raw Go marshal error: %s", mw.Body.String())
	}
	if !strings.Contains(mw.Body.String(), "overflow") {
		t.Errorf("MCP overflow should be a guiding tool error: %s", mw.Body.String())
	}

	// ask bar: an honest refusal, never "+Inf" as fact.
	evs := []event.Event{
		{ID: "b1", DistinctID: "b1", Name: "big", Timestamp: time.Now().UTC().Add(-time.Hour), Properties: map[string]any{"amount": 1e308}},
		{ID: "b2", DistinctID: "b2", Name: "big", Timestamp: time.Now().UTC().Add(-time.Hour), Properties: map[string]any{"amount": 1e308}},
	}
	got := answer("total sum of amount on big events", evs, time.Now().UTC())
	if strings.Contains(got, "Inf") {
		t.Errorf("ask must not state +Inf as fact: %q", got)
	}
	if !strings.Contains(got, "overflow") {
		t.Errorf("ask should explain the overflow honestly: %q", got)
	}
}

// TestAskUniqueUsersCount (finding: "how many unique users did signup" was answered with the
// raw EVENT count — 100 instead of the true 98 the engine computes with unique=true).
func TestAskUniqueUsersCount(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{
		{ID: "1", DistinctID: "u1", Name: "signup", Timestamp: now.Add(-3 * time.Hour)},
		{ID: "2", DistinctID: "u1", Name: "signup", Timestamp: now.Add(-2 * time.Hour)}, // same user twice
		{ID: "3", DistinctID: "u2", Name: "signup", Timestamp: now.Add(-1 * time.Hour)},
	}
	got := answer("how many unique users did signup", evs, now)
	if !strings.Contains(got, "2") || !strings.Contains(got, "distinct users") {
		t.Errorf("unique-users question must dedupe (2 distinct users, not 3 events): %q", got)
	}
	if cnt := answer("how many signup events", evs, now); !strings.Contains(cnt, "3") {
		t.Errorf("plain event count stays 3: %q", cnt)
	}
}

// TestReceiptMatchesBreakdownAnswer (finding: the computed_by receipt re-classified a
// "break down <event> by <property>" question as unknown and claimed "no matching event...
// weekly pulse" directly under a correct breakdown answer).
func TestReceiptMatchesBreakdownAnswer(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{
		{ID: "1", DistinctID: "u1", Name: "plan_event", Timestamp: now.Add(-time.Hour), Properties: map[string]any{"plan": "pro"}},
		{ID: "2", DistinctID: "u2", Name: "plan_event", Timestamp: now.Add(-time.Hour), Properties: map[string]any{"plan": "free"}},
	}
	q := "break down plan_event by plan"
	ans := answer(q, evs, now)
	rec := computedBy(q, evs, now)
	if !strings.Contains(ans, "plan breakdown") {
		t.Fatalf("expected a breakdown answer: %q", ans)
	}
	if strings.Contains(rec, "weekly pulse") || strings.Contains(rec, "no matching event") {
		t.Errorf("receipt contradicts the breakdown answer it accompanies: %q", rec)
	}
	if !strings.Contains(rec, "property-breakdown") {
		t.Errorf("receipt should name the property-breakdown report: %q", rec)
	}
}
