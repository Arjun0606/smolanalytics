package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// toolText extracts the text payload (or error text) of a tools/call response.
func toolText(t *testing.T, r *response) (text string, isErr bool) {
	t.Helper()
	b, _ := json.Marshal(r.Result)
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(b, &res)
	if len(res.Content) == 0 {
		t.Fatalf("no content in tool response: %s", b)
	}
	return res.Content[0].Text, res.IsError
}

// Misspelled event names must come back as a self-correcting ERROR naming the real
// events — never a zeros-report the model reads as "0 conversions".
func TestUnknownEventIsErrorNotZeros(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"funnel","arguments":{"steps":["signup","purchase"]}}}`)
	text, isErr := toolText(t, r)
	if !isErr {
		t.Fatalf("expected error for unknown step, got data: %s", text)
	}
	if !strings.Contains(text, "purchase") || !strings.Contains(text, "checkout") {
		t.Fatalf("error should name the bad event and list real ones: %s", text)
	}
}

// An unrecognized filter op must be an error — it would otherwise match nothing and
// return zeros that look like a real segment.
func TestUnknownFilterOpIsError(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","filters":[{"property":"plan","op":"equals","value":"pro"}]}}}`)
	text, isErr := toolText(t, r)
	if !isErr || !strings.Contains(text, "eq") {
		t.Fatalf("expected unknown-op error listing valid ops, got isErr=%v: %s", isErr, text)
	}
}

// window_hours: 0.5 must be a 30-minute window, not silently unlimited
// (time.Duration(0.5) truncates to 0 = no window).
func TestFractionalWindowHours(t *testing.T) {
	st := memory.New()
	base := time.Now().UTC().Add(-48 * time.Hour)
	_ = st.Ingest(
		event.Event{ID: "1", DistinctID: "u1", Name: "signup", Timestamp: base},
		event.Event{ID: "2", DistinctID: "u1", Name: "checkout", Timestamp: base.Add(2 * time.Hour)}, // outside 0.5h
		event.Event{ID: "3", DistinctID: "u2", Name: "signup", Timestamp: base},
		event.Event{ID: "4", DistinctID: "u2", Name: "checkout", Timestamp: base.Add(10 * time.Minute)}, // inside 0.5h
	)
	s := New(st)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"funnel","arguments":{"steps":["signup","checkout"],"window_hours":0.5}}}`)
	text, isErr := toolText(t, r)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var res struct {
		Steps []struct {
			Count int `json:"count"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil || len(res.Steps) != 2 {
		t.Fatalf("bad funnel result: %s", text)
	}
	if res.Steps[1].Count != 1 {
		t.Fatalf("0.5h window must convert exactly 1 user (u2), got %d — fractional window was dropped", res.Steps[1].Count)
	}
}

// retention with days<7 must NOT emit a fabricated day7_retention_pct, and young
// cohorts must not sit in day-N denominators.
func TestRetentionSummaryHonesty(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	// old cohort (10d ago): 2 users, 1 returns day1
	old := now.Add(-10 * 24 * time.Hour)
	_ = st.Ingest(
		event.Event{ID: "a1", DistinctID: "a", Name: "open", Timestamp: old},
		event.Event{ID: "a2", DistinctID: "a", Name: "open", Timestamp: old.Add(24 * time.Hour)},
		event.Event{ID: "b1", DistinctID: "b", Name: "open", Timestamp: old},
	)
	// young cohort (today): 5 users — must not pollute denominators
	for _, id := range []string{"c", "d", "e", "f", "g"} {
		_ = st.Ingest(event.Event{ID: id, DistinctID: id, Name: "open", Timestamp: now.Add(-time.Hour)})
	}
	s := New(st)

	// days=3 → day7 must be absent entirely
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"retention","arguments":{"event":"open","days":3}}}`)
	text, isErr := toolText(t, r)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var sum map[string]any
	_ = json.Unmarshal([]byte(text), &sum)
	if _, has := sum["day7_retention_pct"]; has {
		t.Fatalf("days=3 must not fabricate a day7 percentage: %s", text)
	}
	// day1 must be 50% of the 2 observable users, not 1/7 of all users
	if pct, _ := sum["day1_retention_pct"].(float64); int(pct) != 50 {
		t.Fatalf("day1 should be 50%% of the observable cohort, got %v (%s)", sum["day1_retention_pct"], text)
	}
	if n, _ := sum["day1_cohort_users"].(float64); int(n) != 2 {
		t.Fatalf("day1 denominator should be the 2 old users, got %v", sum["day1_cohort_users"])
	}
}

// A property that exists on no events must be an error listing real properties.
func TestBreakdownUnknownPropertyIsError(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"breakdown","arguments":{"event":"signup","property":"utm_campaign"}}}`)
	text, isErr := toolText(t, r)
	if !isErr || !strings.Contains(text, "source") {
		t.Fatalf("expected unknown-property error listing real props, got isErr=%v: %s", isErr, text)
	}
}
