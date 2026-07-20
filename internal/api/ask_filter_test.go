package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// TestAskHonorsCustomPropertyFilter is the regression guard for an audit finding: the ask bar
// silently DROPPED any custom-property qualifier the alias tables didn't know (plan=pro),
// returning the UNFILTERED number as if it were CI-verified fact. It must resolve a token
// that is a real property value in the data and filter by it — matching GET /v1 and MCP.
func TestAskHonorsCustomPropertyFilter(t *testing.T) {
	now := time.Now().UTC()
	ts := now.Add(-2 * time.Hour)
	mk := func(id, plan, device string) event.Event {
		return event.Event{ID: id, DistinctID: id, Name: "signup", Timestamp: ts,
			Properties: map[string]any{"plan": plan, "device": device}}
	}
	evs := []event.Event{
		mk("u1", "pro", "mobile"), mk("u3", "pro", "mobile"), mk("u5", "pro", "mobile"),
		mk("u2", "free", "desktop"), mk("u4", "free", "desktop"), mk("u7", "free", "mobile"),
	}

	// single custom-property filter: "pro signups" must be 3, not the unfiltered 6.
	if got := answer("how many pro signups", evs, now); !strings.Contains(got, "3") || strings.Contains(got, "6 ") {
		t.Errorf("`pro signups` should be 3 (was dropping the filter -> 6): %q", got)
	}
	if got := answer("how many free signups", evs, now); !strings.Contains(got, "3") {
		t.Errorf("`free signups` should be 3: %q", got)
	}
	// two different-property segments = AND, not compare: pro users are all mobile, so
	// "pro signups on desktop" is 0 (was reporting device=desktop only, dropping plan).
	if got := answer("how many pro signups on desktop", evs, now); !strings.HasPrefix(got, "0") {
		t.Errorf("`pro signups on desktop` must be 0 (plan=pro AND device=desktop): %q", got)
	}
}

// TestAskMeasureHonorsSegment is the regression guard for the measure-path variant: a numeric
// aggregation ("average order value for pro users") dropped the segment and reported the
// unfiltered aggregate. The segment value may live OUTSIDE the window, so it must resolve
// against the full data, then the window applies.
func TestAskMeasureHonorsSegment(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 10; i++ {
		evs = append(evs, event.Event{ID: string(rune('a'+i)) + "p", DistinctID: string(rune('a' + i)), Name: "checkout",
			Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"amount": 10.0, "plan": "pro"}})
	}
	for i := 0; i < 10; i++ {
		evs = append(evs, event.Event{ID: string(rune('a'+i)) + "f", DistinctID: string(rune('A' + i)), Name: "checkout",
			Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"amount": 1000.0, "plan": "free"}})
	}
	// avg over pro only = 10 (not the blended (10+1000)/2 = 505).
	if got := answer("average order value for pro users", evs, now); !strings.Contains(got, "10") || strings.Contains(got, "505") {
		t.Errorf("avg order value for pro should be 10, not the blended 505: %q", got)
	}
	if got := answer("average order value for free users", evs, now); !strings.Contains(got, "1000") {
		t.Errorf("avg order value for free should be 1000: %q", got)
	}
}

// TestAskBreakdownByEventProperty is the regression guard for two audit findings: "break down
// <event> by <property>" over a named event's OWN property was either bounced to the web
// user-agent dimension ("No devices recorded") or flattened to the aggregate (dropping the
// dimension). It must return the real per-value breakdown, matching GET /v1/breakdown.
func TestAskBreakdownByEventProperty(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 100; i++ {
		device := "desktop"
		if i >= 50 {
			device = "mobile"
		}
		plan := "free"
		if i%3 == 0 {
			plan = "pro"
		}
		evs = append(evs, event.Event{ID: string(rune(i)) + "s", DistinctID: string(rune(i)), Name: "signup",
			Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"device": device, "plan": plan}})
	}
	// device breakdown of signup (was "No devices recorded" via the web dimension).
	got := answer("break down signup by device", evs, now)
	if !strings.Contains(got, "device") || !strings.Contains(got, "desktop 50") || !strings.Contains(got, "mobile 50") {
		t.Errorf("`break down signup by device` should split 50/50: %q", got)
	}
	// custom-property breakdown (was flattened to the aggregate total).
	got = answer("break signups down by plan", evs, now)
	if !strings.Contains(got, "plan") || !strings.Contains(got, "pro") || !strings.Contains(got, "free") {
		t.Errorf("`break signups down by plan` should split by plan value: %q", got)
	}
}

// TestAskConvertNamedEventCount is the regression guard for an audit finding: "How many
// convert events?" was misrouted to a FUNNEL answer because the event name "convert" tripped
// the conversion-vocabulary guard. A single-event count of an event literally named "convert"
// must return that event's total, not a funnel.
func TestAskConvertNamedEventCount(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 89; i++ {
		evs = append(evs, event.Event{ID: string(rune(i)) + "c", DistinctID: string(rune(i)), Name: "convert", Timestamp: now.Add(-2 * time.Hour)})
	}
	got := answer("How many convert events?", evs, now)
	if !strings.Contains(got, "89") || !strings.Contains(got, "convert") {
		t.Errorf("`how many convert events` should count 89, not answer a funnel: %q", got)
	}
	if strings.Contains(got, "complete") || strings.Contains(got, "drop-off") {
		t.Errorf("`how many convert events` misrouted to a funnel: %q", got)
	}
}

// TestAskFunnelHonorsNamedSteps is the regression guard for an audit finding: "what percent of
// landing users convert" built a volume-detected funnel that PREPENDED the top event, so a
// disjoint entry cohort (landing users who never did the top event) zeroed the named step and
// the ask falsely answered "No landing users to compute a rate from". Named steps must win.
func TestAskFunnelHonorsNamedSteps(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 1002; i++ { // high-volume, DISJOINT event that must not be prepended
		evs = append(evs, event.Event{ID: "v" + string(rune(i)), DistinctID: "v" + string(rune(i)), Name: "view", Timestamp: now.Add(-2 * time.Hour)})
	}
	for i := 0; i < 200; i++ {
		u := "land" + string(rune(i))
		evs = append(evs, event.Event{ID: u + "l", DistinctID: u, Name: "landing", Timestamp: now.Add(-2 * time.Hour)})
		if i < 89 {
			evs = append(evs, event.Event{ID: u + "c", DistinctID: u, Name: "convert", Timestamp: now.Add(-1 * time.Hour)})
		}
	}
	got := answer("what percent of landing users convert?", evs, now)
	if strings.Contains(got, "No landing") {
		t.Fatalf("named funnel landing->convert must not zero the entry step: %q", got)
	}
	if !strings.Contains(got, "200") || !strings.Contains(got, "89") {
		t.Errorf("landing->convert should be 89 of 200: %q", got)
	}
}

// TestAskSourcesFirstTouch is the regression guard for an audit finding: the traffic-sources
// answer tallied EVERY pageview's referrer per visitor, double-counting multi-referrer
// visitors and surfacing "sources" (a later visit's t.co) that the first-touch dashboard/GET/
// MCP never show. Attribution must be each visitor's EARLIEST pageview, once.
func TestAskSourcesFirstTouch(t *testing.T) {
	now := time.Now().UTC()
	mkpv := func(id, ref string, age time.Duration) event.Event {
		return event.Event{ID: id + ref, DistinctID: id, Name: "$pageview", Timestamp: now.Add(-age),
			Properties: map[string]any{"path": "/", "referrer": ref}}
	}
	evs := []event.Event{
		// visitor v1: lands from google, RETURNS later via t.co — first-touch = google only.
		mkpv("v1", "https://www.google.com/", 10*time.Hour),
		mkpv("v1", "https://t.co/abc", 2*time.Hour),
		mkpv("v2", "https://www.google.com/", 8*time.Hour),
	}
	got := answer("where is our traffic coming from", evs, now)
	if strings.Contains(got, "t.co") {
		t.Errorf("first-touch sources must not surface the later-visit t.co referrer: %q", got)
	}
	if !strings.Contains(got, "google.com 2") {
		t.Errorf("both visitors are first-touch google (2): %q", got)
	}
}

// TestAskWebDimCountsVisitors is the regression guard for an audit finding: the device split
// counted raw pageviews while the dashboard/GET/MCP count first-touch visitors — the same
// question answered 4/2 vs 1/2. The ask split must be visitors, labeled as such.
func TestAskWebDimCountsVisitors(t *testing.T) {
	now := time.Now().UTC()
	mkpv := func(id, device string, age time.Duration) event.Event {
		return event.Event{ID: id + age.String(), DistinctID: id, Name: "$pageview", Timestamp: now.Add(-age),
			Properties: map[string]any{"path": "/", "device": device}}
	}
	evs := []event.Event{
		// one desktop visitor with FOUR pageviews, two mobile visitors with one each
		mkpv("d1", "desktop", 4*time.Hour), mkpv("d1", "desktop", 3*time.Hour),
		mkpv("d1", "desktop", 2*time.Hour), mkpv("d1", "desktop", time.Hour),
		mkpv("m1", "mobile", 3*time.Hour), mkpv("m2", "mobile", 2*time.Hour),
	}
	got := answer("what devices do people use", evs, now)
	if !strings.Contains(got, "by visitors") {
		t.Errorf("device split must be labeled as visitors: %q", got)
	}
	if !strings.Contains(got, "mobile (2)") || !strings.Contains(got, "desktop (1)") {
		t.Errorf("visitor split is mobile 2 / desktop 1 (NOT the 4/2 pageview split): %q", got)
	}
}

// TestAskBreakdownNoneBucketAndTail is the regression guard for two audit findings: the ask
// breakdown silently DROPPED events missing the property (no "(none)" bucket, so segments
// didn't sum to the total) and silently truncated to the top 8 values with no note.
func TestAskBreakdownNoneBucketAndTail(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	add := func(i int, plan string) {
		props := map[string]any{}
		if plan != "" {
			props["plan"] = plan
		}
		evs = append(evs, event.Event{ID: fmt.Sprintf("u%d%s", i, plan), DistinctID: fmt.Sprintf("u%d", i),
			Name: "signup", Timestamp: now.Add(-2 * time.Hour), Properties: props})
	}
	n := 0
	for v := 0; v < 10; v++ { // 10 distinct plan values — 2 past the top-8 cut
		for k := 0; k <= v; k++ {
			add(n, fmt.Sprintf("tier%02d", v))
			n++
		}
	}
	for k := 0; k < 12; k++ { // 12 signups with NO plan -> "(none)" ranks in the top 8
		add(n, "")
		n++
	}
	got := answer("break signups down by plan", evs, now)
	if !strings.Contains(got, "(none)") {
		t.Errorf("events missing the property must land in \"(none)\", not vanish: %q", got)
	}
	if !strings.Contains(got, "more values)") {
		t.Errorf("truncating past 8 values must disclose the tail (+N more values): %q", got)
	}
}

// TestAskRetentionHonorsNamedEvent is the regression guard for an audit finding: "retention
// for <event>" silently ignored the named event and answered any-activity retention — a
// different question than the one asked, disagreeing with GET/MCP retention?event=<event>.
func TestAskRetentionHonorsNamedEvent(t *testing.T) {
	now := time.Now().UTC()
	d := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	evs := []event.Event{
		// u1: signup cohort day-10, returns day-9 (retained). Never does "open".
		{ID: "u1s", DistinctID: "u1", Name: "signup", Timestamp: d(10)},
		{ID: "u1r", DistinctID: "u1", Name: "signup", Timestamp: d(9)},
		// u2: open cohort day-10, never returns.
		{ID: "u2o", DistinctID: "u2", Name: "open", Timestamp: d(10)},
	}
	general := answer("what is my day 1 retention", evs, now)
	scoped := answer("day 1 retention for open", evs, now)
	if !strings.Contains(general, "of 2") {
		t.Errorf("general retention cohort is both users (of 2): %q", general)
	}
	if !strings.Contains(scoped, "of 1") {
		t.Errorf("`retention for open` must anchor on the open event (cohort of 1): %q", scoped)
	}
}
