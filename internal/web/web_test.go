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
