package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func pv(user, path, ref string, props map[string]any, ts time.Time) event.Event {
	p := map[string]any{"path": path, "referrer": ref}
	for k, v := range props {
		p[k] = v
	}
	return event.Event{ID: user + path + ts.String(), DistinctID: user, Name: "$pageview", Timestamp: ts, Properties: p}
}

func TestComputeOverview(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	// 3 visitors over the period; one live (2 min ago); google + direct referrers
	evs = append(evs,
		pv("u1", "/", "https://www.google.com/search?q=x", map[string]any{"device": "desktop", "utm_source": "hn"}, now.Add(-2*time.Minute)),
		pv("u1", "/pricing", "", map[string]any{"device": "desktop"}, now.Add(-1*time.Minute)),
		pv("u2", "/", "https://google.com/", map[string]any{"device": "mobile"}, now.Add(-2*24*time.Hour)),
		pv("u3", "/docs", "", map[string]any{"device": "desktop"}, now.Add(-10*24*time.Hour)),
		// outside the 7-day window — must not count
		pv("u4", "/", "", nil, now.Add(-40*24*time.Hour)),
		// non-pageview noise — must not count
		event.Event{ID: "x", DistinctID: "u1", Name: "signup", Timestamp: now},
	)
	r := Compute(evs, 30, now)

	if r.Visitors != 3 || r.Pageviews != 4 {
		t.Fatalf("visitors=%d pageviews=%d, want 3/4", r.Visitors, r.Pageviews)
	}
	if r.LiveNow != 1 {
		t.Fatalf("live=%d, want 1 (only u1 in last 5 min)", r.LiveNow)
	}
	if r.TopPages[0].Value != "/" || r.TopPages[0].Count != 2 {
		t.Fatalf("top page: %+v", r.TopPages)
	}
	// both google URL shapes collapse into one host row; empty referrer = direct
	var google, direct int
	for _, row := range r.Referrers {
		if row.Value == "google.com" {
			google = row.Count
		}
		if row.Value == "direct" {
			direct = row.Count
		}
	}
	if google != 2 || direct != 2 {
		t.Fatalf("referrers: google=%d direct=%d, want 2/2 (%+v)", google, direct, r.Referrers)
	}
	if len(r.UTMSources) != 1 || r.UTMSources[0].Value != "hn" {
		t.Fatalf("utm: %+v", r.UTMSources)
	}
	if len(r.DeviceSplit) != 2 || r.DeviceSplit[0].Value != "desktop" {
		t.Fatalf("devices: %+v", r.DeviceSplit)
	}
}

func TestComputeEmpty(t *testing.T) {
	r := Compute(nil, 0, time.Time{})
	if r.Visitors != 0 || r.Pageviews != 0 || r.LiveNow != 0 || r.PeriodDays != 30 {
		t.Fatalf("empty compute should be all zeros with default period: %+v", r)
	}
}

func TestRankDeterministicTies(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i, p := range []string{"/b", "/a"} {
		evs = append(evs, pv(fmt.Sprintf("u%d", i), p, "", nil, now.Add(-time.Hour)))
	}
	r := Compute(evs, 7, now)
	if r.TopPages[0].Value != "/a" || r.TopPages[1].Value != "/b" {
		t.Fatalf("tied counts must sort by name: %+v", r.TopPages)
	}
}

func TestEngagementBounceAndAIChannel(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	// u1: 1 pageview from chatgpt, engaged 45s → NOT a bounce, AI visitor
	evs = append(evs,
		pv("u1", "/", "https://chatgpt.com/", nil, now.Add(-2*time.Hour)),
		event.Event{ID: "e1", DistinctID: "u1", Name: "$engagement", Timestamp: now.Add(-2*time.Hour + time.Minute),
			Properties: map[string]any{"path": "/", "engaged_ms": 45000.0}},
	)
	// u2: 1 pageview direct, engaged 3s → bounce
	evs = append(evs,
		pv("u2", "/", "", nil, now.Add(-3*time.Hour)),
		event.Event{ID: "e2", DistinctID: "u2", Name: "$engagement", Timestamp: now.Add(-3*time.Hour + time.Second),
			Properties: map[string]any{"path": "/", "engaged_ms": 3000.0}},
	)
	// u3: 2 pageviews, no engagement events → not a bounce (multi-page)
	evs = append(evs,
		pv("u3", "/", "", nil, now.Add(-time.Hour)),
		pv("u3", "/docs", "", nil, now.Add(-50*time.Minute)),
	)
	r := Compute(evs, 30, now)
	if !r.HasEngagement {
		t.Fatal("engagement events present → HasEngagement")
	}
	if r.BounceRatePct != 33 { // u2 of 3 visitors
		t.Fatalf("bounce = %d%%, want 33", r.BounceRatePct)
	}
	if r.AvgEngagedSecs != 24 { // (45s+3s)/2 engaged visitors
		t.Fatalf("avg engaged = %ds, want 24", r.AvgEngagedSecs)
	}
	if r.AIVisitors != 1 || len(r.AIReferrers) != 1 || r.AIReferrers[0].Value != "chatgpt.com" {
		t.Fatalf("AI channel: %d %+v", r.AIVisitors, r.AIReferrers)
	}
	// engagement events must not count as pageviews
	if r.Pageviews != 4 {
		t.Fatalf("pageviews = %d, want 4", r.Pageviews)
	}
}

func TestNoEngagementNoFabrication(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{pv("u1", "/", "", nil, now.Add(-time.Hour))}
	r := Compute(evs, 30, now)
	if r.HasEngagement || r.BounceRatePct != 0 || r.AvgEngagedSecs != 0 {
		t.Fatalf("no engagement data → no fabricated bounce/duration: %+v", r)
	}
}
