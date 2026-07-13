package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// askNow is a fixed Thursday so calendar windows ("this week" → Mon Jun 22,
// "last month" → May) are deterministic regardless of when the tests run.
var askNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

func askEv(user, name string, ts time.Time, src string) event.Event {
	e := event.Event{Name: name, DistinctID: user, Timestamp: ts}
	if src != "" {
		e.Properties = map[string]any{"source": src}
	}
	return e
}

// askFixture builds a dataset with known first-touch channels and conversions:
//   - google:  4 users, 2 sign up  → 50% converts best
//   - twitter: 5 users, 1 signs up → 20%
//   - direct:  9 users, 3 sign up  → 33%
//
// plus the signup → activate → checkout journey (1 of 6 completes; the big
// drop is at "activate"), day-1 retention via next-day opens, one signup this
// week (Jun 23), one today-only user, and one signup in May (last month).
func askFixture() []event.Event {
	base := askNow.AddDate(0, 0, -10)
	h := time.Hour
	var evs []event.Event

	for _, u := range []string{"g1", "g2", "g3", "g4"} {
		evs = append(evs, askEv(u, "open", base, "google"))
	}
	for _, u := range []string{"t1", "t2", "t3", "t4", "t5"} {
		evs = append(evs, askEv(u, "open", base, "twitter"))
	}
	for _, u := range []string{"d1", "d2", "d3", "d4", "d5", "d6"} {
		evs = append(evs, askEv(u, "open", base, ""))
	}
	// conversions + journey depth
	for _, u := range []string{"g1", "g2"} {
		evs = append(evs,
			askEv(u, "signup", base.Add(1*h), "google"),
			askEv(u, "activate", base.Add(2*h), "google"))
	}
	evs = append(evs,
		askEv("g1", "checkout", base.Add(3*h), "google"),
		askEv("t1", "signup", base.Add(1*h), "twitter"),
		askEv("t1", "activate", base.Add(2*h), "twitter"),
		askEv("d1", "signup", base.Add(1*h), ""))
	// day-1 retention: next-day opens
	for _, u := range []string{"g1", "g2", "t1", "d1"} {
		evs = append(evs, askEv(u, "open", base.Add(24*h), ""))
	}
	// recent activity: one signup this week (Tue Jun 23), one user active today
	evs = append(evs,
		askEv("u_week", "signup", askNow.AddDate(0, 0, -2), ""),
		askEv("u_today", "open", askNow.Add(-1*h), ""))
	// one signup solidly inside last calendar month (May 22)
	first := time.Date(askNow.Year(), askNow.Month(), 1, 0, 0, 0, 0, time.UTC)
	evs = append(evs, askEv("m1", "signup", first.AddDate(0, 0, -10), ""))
	return evs
}

// TestAskRouterAndAnswers is the sim's list: every ask asserts BOTH the routed
// intent and that the answer carries the right metric's marker (and not a
// confidently wrong neighbor's).
func TestAskRouterAndAnswers(t *testing.T) {
	evs := askFixture()
	tests := []struct {
		q           string
		intent      askIntent
		contains    []string
		notContains []string
	}{
		// --- the default chips must answer their own labels ---
		{
			q:        "where do users drop off?",
			intent:   intentFunnel,
			contains: []string{"drop-off", "signup → activate → checkout", `"activate"`},
		},
		{
			q:           "which channel converts best?",
			intent:      intentChannels,
			contains:    []string{`google converts best to "signup"`, "2 of 4 first-touch users (50%)", "By source"},
			notContains: []string{"drop-off"}, // the funnel answer this used to return
		},
		{
			q:        "how is retention trending?",
			intent:   intentRetention,
			contains: []string{"Day-1 retention"},
		},
		{
			q:        "what happened this week?",
			intent:   intentBrief,
			contains: []string{"Last 7 days:", "visitors"},
		},
		// --- intent collisions the sim caught ---
		{
			q:           "how many active users?",
			intent:      intentActive,
			contains:    []string{"total users", "active in the last 7 days"},
			notContains: []string{`"signup" events`},
		},
		{
			q:        "where do users come from?",
			intent:   intentChannels,
			contains: []string{"By source", "first-touch"},
		},
		{
			q:        "top sources?",
			intent:   intentChannels,
			contains: []string{"By source"},
		},
		// --- action-y asks must not return a metric ---
		{
			q:           "alert me if signups drop",
			intent:      intentAction,
			contains:    []string{"can't create alerts", "Settings → Alerts", "create_alert"},
			notContains: []string{"drop-off", `"signup" events`},
		},
		{
			q:           "set retention to 90 days",
			intent:      intentAction,
			contains:    []string{"can't change settings", "Settings → Retention", "set_retention"},
			notContains: []string{"Day-1 retention"},
		},
		{
			q:           "rename the checkout event to purchase",
			intent:      intentAction,
			contains:    []string{"can't rename events", "set_tracking_plan"},
			notContains: []string{"drop-off"},
		},
		// --- the weekly brief ---
		{
			q:        "how are things?",
			intent:   intentBrief,
			contains: []string{"Last 7 days:", "events"},
		},
		{
			q:        "weekly report",
			intent:   intentBrief,
			contains: []string{"Last 7 days:"},
		},
		// --- honest time scoping ---
		{
			q:        "how many signups this week?",
			intent:   intentSignups,
			contains: []string{`1 "signup" events this week (since Mon Jun 22, UTC)`},
		},
		{
			q:        "how many signups today?",
			intent:   intentSignups,
			contains: []string{`No "signup" events today (UTC)`}, // 0 today — say so, don't widen
		},
		{
			q:        "how many signups last month?",
			intent:   intentSignups,
			contains: []string{`1 "signup" events last month (May 2026, UTC)`},
		},
		{
			q:        "how many active users today?",
			intent:   intentActive,
			contains: []string{"1 active users today (UTC)"},
		},
		{
			q:           "how many signups last quarter?",
			intent:      intentSignups,
			contains:    []string{`can't scope to "quarter"`, "today, yesterday, this/last week"},
			notContains: []string{`"signup" events over`}, // never silently answer a different window
		},
		{
			q:        "how many signups?",
			intent:   intentSignups,
			contains: []string{`6 "signup" events over the last`},
		},
		// --- the tagline question must return the verdict, never a menu ---
		{
			q:           "what should i fix?",
			intent:      intentBrief,
			contains:    []string{"Last 7 days:"},
			notContains: []string{"I can answer about"}, // never the capabilities menu
		},
		{
			q:        "anything broken?",
			intent:   intentBrief,
			contains: []string{"Last 7 days:"},
		},
		// --- pageviews/visitors must never be answered as a signup count ---
		{
			q:           "how many pageviews this month?",
			intent:      intentWeb,
			contains:    []string{"pageview"},
			notContains: []string{`"signup" events`, "signup event"},
		},
		{
			q:           "how much traffic did i get?",
			intent:      intentWeb,
			notContains: []string{`"signup" events`},
		},
		{
			q:      "top pages?",
			intent: intentTopPages,
		},
		// an unknown question leads with the verdict, not a dead-end menu
		{
			q:        "is my experiment variant b winning?",
			intent:   intentUnknown,
			contains: []string{"Last 7 days:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			if got := classifyAsk(tc.q); got != tc.intent {
				t.Fatalf("classifyAsk(%q) = %q, want %q", tc.q, got, tc.intent)
			}
			ans := answer(tc.q, evs, askNow)
			for _, want := range tc.contains {
				if !strings.Contains(ans, want) {
					t.Errorf("answer(%q) missing %q\ngot: %s", tc.q, want, ans)
				}
			}
			for _, bad := range tc.notContains {
				if strings.Contains(ans, bad) {
					t.Errorf("answer(%q) must not contain %q\ngot: %s", tc.q, bad, ans)
				}
			}
		})
	}
}

// TestParseWindow pins the window math itself: calendar weeks start Monday,
// calendar months are exact, rolling days roll from now, and time phrases the
// parser does not support are surfaced instead of swallowed.
func TestParseWindow(t *testing.T) {
	day := 24 * time.Hour
	today := askNow.Truncate(day)
	tests := []struct {
		q           string
		from, to    time.Time
		unsupported string
	}{
		{q: "signups today", from: today, to: askNow},
		{q: "signups yesterday", from: today.Add(-day), to: today},
		{q: "signups this week", from: time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC), to: askNow},
		{q: "signups last week", from: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), to: time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)},
		{q: "signups this month", from: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), to: askNow},
		{q: "signups last month", from: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), to: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{q: "signups in the last 14 days", from: askNow.AddDate(0, 0, -14), to: askNow},
		{q: "signups this quarter", unsupported: "quarter"},
		{q: "signups last year", unsupported: "year"},
		{q: "signups in january", unsupported: "january"},
		{q: "signups"},              // no time phrase → all history, no complaint
		{q: "monthly signup trend"}, // "monthly" is not the word "month" — must not bounce
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			win, unsupported := parseWindow(tc.q, askNow)
			if unsupported != tc.unsupported {
				t.Fatalf("parseWindow(%q) unsupported = %q, want %q", tc.q, unsupported, tc.unsupported)
			}
			if !win.from.Equal(tc.from) || !win.to.Equal(tc.to) {
				t.Errorf("parseWindow(%q) = [%v, %v), want [%v, %v)", tc.q, win.from, win.to, tc.from, tc.to)
			}
		})
	}
}

// TestAskNamedEventAndPage covers the discovery-chip resolvers: a named event and a
// page path answer about exactly what the user named, taking priority over the generic
// intents, while generic questions are untouched.
func TestAskNamedEventAndPage(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	mk := func(user, name, path string) event.Event {
		e := event.Event{Name: name, DistinctID: user, Timestamp: now}
		if path != "" {
			e.Properties = map[string]any{"path": path}
		}
		return e
	}
	evs := []event.Event{
		mk("u1", "$pageview", "/pricing"), mk("u2", "$pageview", "/pricing"), mk("u1", "$pageview", "/pricing"),
		mk("u3", "$pageview", "/"),
		mk("u1", "checkout", ""), mk("u2", "checkout", ""),
		mk("u1", "signup", ""),
	}

	// named event: "checkout" resolves even though the generic classifier would call it a funnel
	if got := answer("how many checkout events?", evs, now); !strings.Contains(got, "checkout") || !strings.Contains(got, "2") {
		t.Errorf("named-event ask: got %q, want a count of 2 checkout events", got)
	}
	// page path: visitors + pageviews to /pricing (case/slash-insensitive)
	if got := answer("visitors to /Pricing/", evs, now); !strings.Contains(got, "2 visitors") || !strings.Contains(got, "3 pageviews") {
		t.Errorf("page ask: got %q, want 2 visitors / 3 pageviews for /pricing", got)
	}
	// web volume: total pageviews + visitors, never mislabeled as a signup count
	if got := answer("how many pageviews?", evs, now); !strings.Contains(got, "4 pageviews") || !strings.Contains(got, "3 visitors") || strings.Contains(got, "signup") {
		t.Errorf("pageview ask: got %q, want 4 pageviews / 3 visitors and no signup", got)
	}
	// top pages ranks the most-viewed path, not the capabilities menu
	if got := answer("top pages?", evs, now); !strings.Contains(got, "/pricing") || strings.Contains(got, "I can answer about") {
		t.Errorf("top pages ask: got %q, want /pricing ranked", got)
	}
	// a bare event mention without a count word still falls through to the funnel intent
	if got := answer("how is checkout doing", evs, now); strings.Contains(got, "pageviews") {
		t.Errorf("non-count event mention should not hit the page/event resolver: %q", got)
	}
	// unknown path answers honestly, not a fake zero passed off as data
	if got := answer("visitors to /nope", evs, now); !strings.Contains(got, "No pageviews for /nope") {
		t.Errorf("unknown path: got %q, want an honest no-data message", got)
	}

	// an explicitly-named event we DON'T have must NOT silently answer a default event
	// (the trust-breaking substitution). Say it doesn't exist, name the real events.
	if got := answer("how many times did the flibbergibbet_zorptastic event fire last week?", evs, now); !strings.Contains(got, "No event named") || strings.Contains(got, "events last week") {
		// must say no-such-event, and must NOT report a real event's count for the window
		t.Errorf("unknown named event: got %q, want an honest no-such-event message", got)
	}
	if got := answer("how many flibbergibbet_zorptastic events?", evs, now); !strings.Contains(got, "No event named") {
		t.Errorf("unknown named event (plural): got %q, want an honest no-such-event message", got)
	}
	// a near-typo of a real event should suggest the nearest name
	if got := answer("how many chekout events?", evs, now); !strings.Contains(got, "No event named") || !strings.Contains(got, `Did you mean "checkout"`) {
		t.Errorf("typo'd event: got %q, want a nearest-match suggestion", got)
	}
	// a correctly-named event must still count (not misfire as unknown)
	if got := answer("how many checkout events?", evs, now); strings.Contains(got, "No event named") {
		t.Errorf("known event mislabeled as unknown: %q", got)
	}
}
