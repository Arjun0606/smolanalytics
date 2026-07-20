package api

// Round-5 verification-pass regression guards (the second batch of fixes).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func recentSignupsP(prefix string, n int, props map[string]any) []event.Event {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < n; i++ {
		p := map[string]any{}
		for k, v := range props {
			p[k] = v
		}
		id := fmt.Sprintf("%s_%d", prefix, i) // unique across batches — a user attribute is
		// user-scoped, so colliding ids would put one user in two source segments
		evs = append(evs, event.Event{ID: id, DistinctID: id, Name: "signup", Timestamp: now.Add(-2 * time.Hour), Properties: p})
	}
	return evs
}

// TestAskSegmentSourceFallbackAndDisclosure guards the critical filter-drop cluster: "from
// twitter" must resolve to the source property (not only referrer), and a value present on NO
// event must be disclosed as 0, never answered with the unfiltered total.
func TestAskSegmentSourceFallbackAndDisclosure(t *testing.T) {
	now := time.Now().UTC()
	evs := append(recentSignupsP("tw", 28, map[string]any{"source": "twitter"}), recentSignupsP("gg", 11, map[string]any{"source": "google"})...)
	if got := answer("how many signups from twitter", evs, now); !strings.Contains(got, "28") {
		t.Errorf("`from twitter` must resolve to source=twitter (28), got: %q", got)
	}
	if got := answer("how many signups where source is twitter", evs, now); !strings.Contains(got, "28") {
		t.Errorf("`where source is twitter` should be 28: %q", got)
	}
	// unresolved value -> honest 0, NOT the unfiltered 39
	got := answer("how many signups from flurbotron", evs, now)
	if !strings.HasPrefix(got, "0") || strings.Contains(got, "39") {
		t.Errorf("`from flurbotron` (absent) must disclose 0, not the unfiltered total: %q", got)
	}
}

// TestAskMinuteWindow guards: "last 30 minutes" must scope, not fall through to all-time.
func TestAskMinuteWindow(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 20; i++ { // all 3 hours old — outside a 30-min window
		evs = append(evs, event.Event{ID: string(rune(i)), DistinctID: string(rune(i)), Name: "signup", Timestamp: now.Add(-3 * time.Hour)})
	}
	got := answer("how many signups in the last 30 minutes", evs, now)
	if strings.Contains(got, "20") || strings.Contains(got, "all time") {
		t.Errorf("`last 30 minutes` must scope (0 here), not answer all-time 20: %q", got)
	}
}

// TestAskMeasureRouting guards: "total checkout amount" is a SUM, not an event count/funnel.
func TestAskMeasureRouting(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 4; i++ {
		evs = append(evs, event.Event{ID: string(rune(i)), DistinctID: string(rune(i)), Name: "checkout",
			Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"amount": 100.0}})
	}
	got := answer("total checkout amount", evs, now)
	if !strings.Contains(got, "400") { // 4 * 100
		t.Errorf("`total checkout amount` should sum to 400, not a count/funnel: %q", got)
	}
	if strings.Contains(got, "complete") || strings.Contains(got, "drop-off") {
		t.Errorf("`total checkout amount` misrouted to a funnel: %q", got)
	}
}

// TestAskPageviewCustomEvent guards: a custom event literally named "pageview" is counted,
// not falsely reported as "No pageviews".
func TestAskPageviewCustomEvent(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 20; i++ {
		evs = append(evs, event.Event{ID: string(rune(i)), DistinctID: string(rune(i)), Name: "pageview", Timestamp: now.Add(-2 * time.Hour)})
	}
	got := answer("how many pageviews", evs, now)
	if strings.Contains(got, "No pageviews") || !strings.Contains(got, "20") {
		t.Errorf("a custom `pageview` event (20) must be counted, not 'No pageviews': %q", got)
	}
}

// TestFunnelAndRetentionValidateEventNames guards: GET /v1/funnel and /v1/retention must 400
// on a typo'd event name instead of a confident empty/zero result.
func TestFunnelAndRetentionValidateEventNames(t *testing.T) {
	s := New(memory.New())
	_ = s
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", DistinctID: "u", Name: "signup", Timestamp: time.Now().UTC()})
	srv := New(st)
	h := srv.Handler()
	for _, path := range []string{"/v1/funnel?steps=signup,chekout", "/v1/retention?event=signuppp"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: typo'd event should 400, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

// TestGtLtRejectsNonNumeric guards: a gt/lt filter with a non-numeric comparand errors
// instead of silently coercing to 0 and returning a real-looking filtered number.
func TestGtLtRejectsNonNumeric(t *testing.T) {
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", DistinctID: "u", Name: "checkout", Timestamp: time.Now().UTC(), Properties: map[string]any{"amount": 50.0}})
	h := New(st).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", `/v1/trends?event=checkout&filters=[{"property":"amount","op":"gt","value":"abc"}]`, nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("gt with a non-numeric value should 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAskRetentionStickinessNotHijacked guards #25/#46: event-scoped retention/stickiness
// questions must reach their real report, not the named-event count intercept.
func TestAskRetentionStickinessNotHijacked(t *testing.T) {
	now := time.Now().UTC()
	d := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	var evs []event.Event
	for i := 0; i < 10; i++ {
		u := fmt.Sprintf("u%d", i)
		evs = append(evs, event.Event{ID: u + "a", DistinctID: u, Name: "app_open", Timestamp: d(3)})
		if i < 4 {
			evs = append(evs, event.Event{ID: u + "b", DistinctID: u, Name: "app_open", Timestamp: d(2)}) // day-1 return
		}
	}
	got := answer("retention for app_open this week", evs, now)
	if strings.Contains(got, "events") && !strings.Contains(got, "retention") && !strings.Contains(got, "%") {
		t.Errorf("event-scoped retention must not be answered as an event count: %q", got)
	}
}

// TestAskTotalEventsCount guards #18: "how many events all time" is the grand total.
func TestAskTotalEventsCount(t *testing.T) {
	now := time.Now().UTC()
	evs := append(recentSignupsP("s", 7, nil), func() []event.Event {
		var e []event.Event
		for i := 0; i < 20; i++ {
			e = append(e, event.Event{ID: fmt.Sprintf("c%d", i), DistinctID: fmt.Sprintf("c%d", i), Name: "checkout", Timestamp: now.Add(-time.Hour)})
		}
		return e
	}()...)
	got := answer("how many events did we receive all time", evs, now)
	if !strings.Contains(got, "27") { // 7 signup + 20 checkout
		t.Errorf("total events should be 27, not one event's count: %q", got)
	}
}

// TestAskConvByTwoStepAndFloor guards #23/#14: "conversion from X to Y by prop" is a real
// 2-step funnel per segment (not 100% for all), and tiny samples don't headline.
func TestAskConvByTwoStepAndFloor(t *testing.T) {
	now := time.Now().UTC()
	t2 := now.Add(-2 * time.Hour)
	var evs []event.Event
	add := func(id, name, plan string) {
		evs = append(evs, event.Event{ID: id + name, DistinctID: id, Name: name, Timestamp: t2, Properties: map[string]any{"plan": plan}})
	}
	for i := 0; i < 10; i++ {
		add(fmt.Sprintf("p%d", i), "signup", "pro")
		if i < 6 {
			add(fmt.Sprintf("p%d", i), "checkout", "pro")
		}
	}
	for i := 0; i < 10; i++ {
		add(fmt.Sprintf("f%d", i), "signup", "free") // 0% checkout
	}
	add("tv0", "signup", "tv")
	add("tv0", "checkout", "tv") // 100% but n=1
	got := answer("conversion from signup to checkout by plan", evs, now)
	if !strings.Contains(got, "pro 60%") || !strings.Contains(got, "free 0%") {
		t.Errorf("2-step conversion by plan should be pro 60%%, free 0%% (not 100%% for all): %q", got)
	}
	if !strings.Contains(got, "small sample") {
		t.Errorf("the n=1 tv=100%% segment must be flagged small-sample, not headlined: %q", got)
	}
}

// TestAskPathsUnknownAnchor guards #2: "after <nonexistent>" discloses instead of anchoring
// on a substring-matched wrong event.
func TestAskPathsUnknownAnchor(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{
		{ID: "1", DistinctID: "u", Name: "signup", Timestamp: now.Add(-2 * time.Hour)},
		{ID: "2", DistinctID: "u", Name: "checkout", Timestamp: now.Add(-time.Hour)},
	}
	got := answer("what do users do after purchase", evs, now)
	if !strings.Contains(got, "No event named") || !strings.Contains(got, "purchase") {
		t.Errorf("`after purchase` (no such event) must disclose, not anchor elsewhere: %q", got)
	}
}
