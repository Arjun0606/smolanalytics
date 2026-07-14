package api

// ask_battery_test.go — the contracts pinned by the July 2026 ask battery: 188
// natural PM questions adversarially judged against the /v1 reports found 108
// failures in the ask engine. Every failure class fixed gets a row here, so none
// of them can quietly come back. Shapes covered: segment-scoped counts ("traffic
// from reddit"), window comparisons ("this week vs last week"), segment-vs-segment
// ("android vs ios signups"), traffic ranking vs conversion attribution, funnel
// sub-metrics ("signup to activation rate"), retention anchored to ANY event (the
// same default /v1/retention uses — the covenant), DAU/WAU/MAU windows, and the
// honest-zero rule for segments with no data.

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// batNow is a fixed Thursday, like askNow, so calendar windows are deterministic.
var batNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

func batEv(user, name string, ts time.Time, props map[string]any) event.Event {
	return event.Event{Name: name, DistinctID: user, Timestamp: ts, Properties: props}
}

// batteryFixture is a small web-shaped dataset with exactly known counts:
//   - 6 visitors ($pageview): r1,r2 referrer reddit.com; t1 referrer t.co;
//     t2 utm_source=twitter (no referrer); h1 news.ycombinator.com; d1 direct
//   - devices: r1,r2,t1 mobile; t2,h1,d1 desktop
//   - os: r1 Android, r2 iOS; browsers: h1 Chrome, d1 Safari
//   - countries: r1,r2 IN; t1,t2 US; h1 GB; d1 BR
//   - paths: everyone lands on /, d1 also views /pricing
//   - funnel: r1,r2,t1,h1 signup; r1,r2 activate; r1 checkout
//   - this week (Mon Jun 22+): w1 pageview+signup on Tue Jun 23
//   - last week: r1..h1 signups fall on Thu Jun 18 (base)
func batteryFixture() []event.Event {
	base := batNow.AddDate(0, 0, -7) // Thu Jun 18 — inside "last week"
	h := time.Hour
	pv := func(u string, ts time.Time, ref, dev, browser, osn, country string, extra map[string]any) event.Event {
		p := map[string]any{"path": "/", "device": dev}
		if ref != "" {
			p["referrer"] = "https://" + ref + "/"
		}
		if browser != "" {
			p["browser"] = browser
		}
		if osn != "" {
			p["os"] = osn
		}
		if country != "" {
			p["country"] = country
		}
		for k, v := range extra {
			p[k] = v
		}
		return batEv(u, "$pageview", ts, p)
	}
	var evs []event.Event
	evs = append(evs,
		pv("r1", base, "reddit.com", "mobile", "", "Android", "IN", nil),
		pv("r2", base.Add(h), "reddit.com", "mobile", "", "iOS", "IN", nil),
		pv("t1", base.Add(2*h), "t.co", "mobile", "", "", "US", nil),
		pv("t2", base.Add(3*h), "", "desktop", "", "", "US", map[string]any{"utm_source": "twitter", "utm_medium": "social"}),
		pv("h1", base.Add(4*h), "news.ycombinator.com", "desktop", "Chrome", "", "GB", nil),
		pv("d1", base.Add(5*h), "", "desktop", "Safari", "", "BR", nil),
	)
	evs = append(evs, batEv("d1", "$pageview", base.Add(6*h), map[string]any{"path": "/pricing", "device": "desktop"}))
	// funnel — custom events carry NO referrer (like real SDKs), only device
	for _, u := range []string{"r1", "r2", "t1", "h1"} {
		evs = append(evs, batEv(u, "signup", base.Add(7*h), map[string]any{"device": "mobile"}))
	}
	for _, u := range []string{"r1", "r2"} {
		evs = append(evs, batEv(u, "activate", base.Add(8*h), nil))
	}
	evs = append(evs, batEv("r1", "checkout", base.Add(9*h), nil))
	// day-1 returns for r1, r2 (retention: ANY activity counts)
	evs = append(evs, batEv("r1", "open", base.Add(25*h), nil), batEv("r2", "$pageview", base.Add(26*h), map[string]any{"path": "/"}))
	// this week: w1 lands Tue Jun 23 and signs up
	tue := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	evs = append(evs,
		batEv("w1", "$pageview", tue, map[string]any{"path": "/", "device": "desktop"}),
		batEv("w1", "signup", tue.Add(h), nil))
	// lifecycle shape: r1 resurfaces Wed Jun 24 (so "yesterday" exists — w1 goes
	// dormant that day), r2 resurfaces today Thu Jun 25 (resurrected today)
	evs = append(evs,
		batEv("r1", "open", time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC), nil),
		batEv("r2", "open", time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC), nil))
	return evs
}

func TestAskBatteryContracts(t *testing.T) {
	evs := batteryFixture()
	cases := []struct {
		name        string
		q           string
		contains    []string
		notContains []string
	}{
		// --- segment-scoped counts: the filter must actually apply ---
		{"traffic from reddit", "how much traffic came from reddit",
			[]string{"2 visitors", "reddit"}, []string{"6 visitors"}},
		{"traffic from hn", "any traffic from hacker news?",
			[]string{"1 visitors", "hacker news"}, nil},
		// twitter = t.co referrer OR utm_source twitter, and reddit.com must NOT
		// count toward it (the substring bug: "reddit.com" contains "t.co")
		{"twitter union not substring", "how many folks came from twitter",
			[]string{"2 visitors", "twitter"}, []string{"4 visitors"}},
		{"country scope", "visitors from india this week?",
			[]string{"0 "}, nil}, // IN visits were last week
		{"os scope", "how much of my traffic is on ios",
			[]string{"1 visitors", "iOS"}, nil},
		{"path scope", "homepage pageviews last 7 days",
			[]string{"8 pageviews", "the homepage"}, []string{"9 pageviews"}}, // 6 landings + r2 return + w1; /pricing excluded
		// --- the honest zero: a named segment with no data answers 0, not the total ---
		{"honest zero tiktok", "any signups from tiktok",
			[]string{"0 — no events", "tiktok"}, []string{"5 "}},
		{"honest zero blog", "how many people hit the blog last week",
			[]string{"0 — no events", "/blog"}, nil},
		// --- window comparisons: both periods + direction ---
		{"wow visitors", "did traffic grow vs last week",
			[]string{"this week", "vs", "last week"}, nil},
		{"wow pageviews", "week over week pageviews",
			[]string{"Pageviews", "this week", "last week"}, nil},
		{"event compare", "signups this week vs last week",
			[]string{`"signup" events`, "1 this week", "4 last week", "down"}, nil},
		// --- segment vs segment ---
		{"seg vs seg", "android vs ios signups",
			[]string{"Android", "iOS"}, nil},
		{"device product", "are we more of a mobile or desktop product",
			[]string{"mobile 3", "desktop 4", "desktop leads"}, nil}, // w1 is desktop
		// --- sources = traffic ranking (direct included); channels = attribution ---
		{"sources ranking", "where is our traffic coming from",
			[]string{"Traffic by source", "direct"}, []string{"converts best"}},
		{"attribution kept", "which channel drives the most signups",
			[]string{"converts best"}, nil},
		{"utm breakdown", "utm medium breakdown",
			[]string{"utm_medium breakdown", "social 1"}, nil},
		// --- funnel sub-metrics ---
		{"step pair rate", "signup to activation rate?",
			[]string{"40% of \"signup\" users go on to \"activate\"", "2 of 5"}, nil}, // w1 signed up too
		{"drop between", "how many people dropped between activate and checkout",
			[]string{"1 users dropped between", "2 → 1"}, nil},
		{"completers", "how many users made it all the way through to checkout",
			[]string{"1 users completed the full funnel"}, nil},
		{"share of first step", "what percent of signups end up checking out",
			[]string{"20%", "1 of 5"}, nil},
		{"visitor share", "what share of visitors ever sign up",
			[]string{"71% of visitors did \"signup\"", "5 of 7"}, nil},
		{"benchmarks honest", "how does our conversion compare to industry benchmarks",
			[]string{"no industry-benchmark data"}, nil},
		// --- funnel scoped by USER for referrer segments (signup events carry no referrer) ---
		{"funnel by referrer users", "does traffic from reddit actually convert",
			[]string{"1 of 2 users (50%)", "scoped to reddit"}, []string{"No events"}},
		// --- DAU/WAU/MAU mean their own windows ---
		{"dau", "dau?", []string{"DAU (24h actives)"}, []string{"last 7 days"}},
		{"mau", "monthly active users", []string{"MAU (30-day actives)"}, []string{"last 7 days"}},
		{"stickiness", "how sticky is the product", []string{"Stickiness (DAU/MAU)"}, nil},
		// --- the extra window forms ---
		{"since monday", "traffic since monday",
			[]string{"since Monday"}, nil},
		{"date range", "unique visitors between june 20 and june 24",
			[]string{"Jun 20 – Jun 24"}, nil},
		{"peak day", "what was our biggest traffic day this month",
			[]string{"Biggest day for visitors"}, nil},
		{"peak hour", "what hour of the day gets the most traffic",
			[]string{"Activity peaks at"}, nil},
		// --- entry pages are not top pages ---
		{"entry pages", "which pages do people land on first",
			[]string{"Entry pages", "/ (7)"}, []string{"/pricing ("}},
		// --- splits ---
		{"direct vs search", "whats the direct vs search split",
			[]string{"Direct 4 visitors", "search 0"}, nil}, // t2, d1, r2's return pv, w1
		{"paid vs organic", "paid vs organic split",
			[]string{"Paid", "organic"}, nil},
		{"ai referrers", "which ai tools send us the most visitors",
			[]string{"No visitors from AI assistants"}, nil},
		// --- conversion by segment ranks with real denominators ---
		{"conv by device", "conversion rate by device",
			[]string{"conversion by device"}, nil},
		// --- lifecycle + returning ---
		{"new vs returning", "is our traffic mostly new or returning",
			[]string{"new", "returning"}, nil},
		// --- the re-judge stragglers (round 3) ---
		{"dormant asked day", "how many users went dormant yesterday",
			[]string{"went dormant yesterday", "per-day lifecycle"}, []string{"this week"}},
		{"resurrected not returning", "how many people came back today after being gone a while",
			[]string{"resurrected users today"}, nil},
		{"step rate compare", "activation rate this week vs last week",
			[]string{"signup → activate rate", "this week", "last week"}, []string{"checkout"}},
		{"trend is a series", "signup trend over time",
			[]string{"→", "total"}, nil},
		{"event count over range", "checkouts from june 18 to june 19",
			[]string{`"checkout" events Jun 18 – Jun 19`}, []string{"complete"}},
		{"people visited routes web", "how many people visited the site this week",
			[]string{"1 pageviews from 1 visitors this week"}, []string{`"signup"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := answer(tc.q, evs, batNow)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("answer(%q)\n  missing %q\n  got: %s", tc.q, want, got)
				}
			}
			for _, not := range tc.notContains {
				if strings.Contains(got, not) {
					t.Errorf("answer(%q)\n  must NOT contain %q\n  got: %s", tc.q, not, got)
				}
			}
		})
	}
}

// TestAskRetentionCovenant pins the battery's sharpest catch: the ask bar quoted
// retention numbers that contradicted /v1/retention on the same instance at the
// same moment, because the two surfaces anchored to different events. Both now
// anchor to ANY activity, and day-N beyond the data answers honestly.
func TestAskRetentionCovenant(t *testing.T) {
	evs := batteryFixture()
	got := answer("whats our day 1 retention", evs, batNow)
	// cohorts: base day has 6 users, r1+r2 return next day; w1's cohort (Jun 23) is
	// also past day 1 with no return. DayN(1) = 2 returned of 7 eligible = 29%.
	if !strings.Contains(got, "29%") || !strings.Contains(got, "of 7") {
		t.Errorf("day-1 retention must be 29%% of 7 (any-activity anchor, the /v1 default), got: %s", got)
	}
	if a := answer("day 30 retention", evs, batNow); !strings.Contains(a, "Not enough history") {
		t.Errorf("day-30 with 7 days of data must answer not-enough-history, got: %s", a)
	}
	if a := answer("retention for mobile users", evs, batNow); !strings.Contains(a, "scoped to mobile") {
		t.Errorf("segment-scoped retention must say its scope, got: %s", a)
	}
}

// TestChannelAttributionUsesReferrer pins the journey-caught bug: on realistic
// autocapture data the landing $pageview carries `referrer` (not `source`), so keying
// channel attribution off a single `source` property collapsed every visitor to
// "direct". Attribution must fall back to the referrer host. (Also pins that the named
// winner appears in the by-channel list beneath it.)
func TestChannelAttributionUsesReferrer(t *testing.T) {
	base := batNow.AddDate(0, 0, -3)
	evs := []event.Event{}
	// 3 reddit visitors (2 sign up), 2 google visitors (0 sign up), 1 direct (1 signs up).
	// referrer lives on the pageview; signup carries no source — the realistic shape.
	mk := func(u, ref string, signs bool) {
		p := map[string]any{"path": "/"}
		if ref != "" {
			p["referrer"] = "https://" + ref + "/"
		}
		evs = append(evs, event.Event{DistinctID: u, Name: "$pageview", Timestamp: base, Properties: p})
		if signs {
			evs = append(evs, event.Event{DistinctID: u, Name: "signup", Timestamp: base.Add(time.Minute)})
		}
	}
	mk("r1", "reddit.com", true)
	mk("r2", "reddit.com", true)
	mk("r3", "reddit.com", false)
	mk("g1", "google.com", false)
	mk("g2", "google.com", false)
	mk("d1", "", true)
	got := answer("which channel converts best", evs, batNow)
	// must NOT collapse to one "direct" row of everyone
	if strings.Contains(got, "direct 6") || !strings.Contains(got, "reddit.com") {
		t.Errorf("channel attribution collapsed / ignored referrer: %s", got)
	}
	// direct (1/1 = 100%) is the highest rate → named winner, and it must appear in the list
	if !strings.Contains(got, "direct converts best") {
		t.Errorf("expected direct (100%%) as winner, got: %s", got)
	}
}
