package query

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func mk(name, source string, n float64) event.Event {
	return event.Event{Name: name, Properties: map[string]any{"source": source, "n": n}}
}

func TestFilters(t *testing.T) {
	evs := []event.Event{
		mk("signup", "google", 5),
		mk("signup", "twitter", 20),
		mk("signup", "google", 1),
	}
	if got := Apply(evs, []Filter{{Property: "source", Op: Eq, Value: "google"}}); len(got) != 2 {
		t.Fatalf("eq google = %d, want 2", len(got))
	}
	if got := Apply(evs, []Filter{{Property: "source", Op: Neq, Value: "google"}}); len(got) != 1 {
		t.Fatalf("neq google = %d, want 1", len(got))
	}
	if got := Apply(evs, []Filter{{Property: "n", Op: Gt, Value: 10}}); len(got) != 1 {
		t.Fatalf("n>10 = %d, want 1", len(got))
	}
	// AND of two filters
	got := Apply(evs, []Filter{{Property: "source", Op: Eq, Value: "google"}, {Property: "n", Op: Lt, Value: 3}})
	if len(got) != 1 {
		t.Fatalf("google AND n<3 = %d, want 1", len(got))
	}
}

func TestBreakdown(t *testing.T) {
	evs := []event.Event{
		mk("signup", "google", 0), mk("signup", "google", 0), mk("signup", "twitter", 0),
		{Name: "signup"}, // missing source -> (none)
	}
	g := Breakdown(evs, "source")
	if len(g) != 3 {
		t.Fatalf("want 3 groups, got %d", len(g))
	}
	if g[0].Value != "google" || g[0].Count != 2 {
		t.Fatalf("top group = %s/%d, want google/2", g[0].Value, g[0].Count)
	}
}

// TestStampFirstTouch pins the funnel/report breakdown-by-acquisition fix: a conversion
// event (signup) carries no referrer, but the user's landing pageview does. Without
// stamping, a breakdown by referrer collapses every converter into "(none)". After
// stamping, each user's events inherit their first-touch referrer HOST.
func TestStampFirstTouch(t *testing.T) {
	ev := func(name, user, referrer string, tsSec int64) event.Event {
		p := map[string]any{}
		if referrer != "" {
			p["referrer"] = referrer
		}
		return event.Event{Name: name, DistinctID: user, Properties: p,
			Timestamp: time.Unix(tsSec, 0).UTC()}
	}
	evs := []event.Event{
		ev("$pageview", "u1", "https://www.reddit.com/r/x", 100), // landing carries referrer
		ev("signup", "u1", "", 200),                              // conversion carries none
		ev("$pageview", "u2", "https://news.ycombinator.com/", 100),
		ev("signup", "u2", "", 200),
	}
	out := StampFirstTouch(evs, "referrer")
	// every one of u1's events now reads reddit.com (host, no scheme/www/path)
	for _, e := range out {
		if e.DistinctID == "u1" {
			if got := e.Properties["referrer"]; got != "reddit.com" {
				t.Fatalf("u1 %s referrer = %v, want reddit.com", e.Name, got)
			}
		}
		if e.DistinctID == "u2" {
			if got := e.Properties["referrer"]; got != "news.ycombinator.com" {
				t.Fatalf("u2 %s referrer = %v, want news.ycombinator.com", e.Name, got)
			}
		}
	}
	// the original slice is untouched (copy semantics): u1's signup still has no referrer
	for _, e := range evs {
		if e.DistinctID == "u1" && e.Name == "signup" {
			if _, ok := e.Properties["referrer"]; ok {
				t.Fatal("StampFirstTouch mutated the input events")
			}
		}
	}
}

// TestReferrerFilterHostAware pins that an equality filter on referrer matches by HOST,
// not exact URL. The cards display a host ("reddit.com") but events store a full landing
// URL ("https://reddit.com/r/x"); without host-aware matching, clicking the referrer card
// filtered to zero events, and the dashboard silently disagreed with the ask bar (which
// already matches host-aware). Covers Eq, Neq, and In.
func TestReferrerFilterHostAware(t *testing.T) {
	ev := func(ref string) event.Event {
		return event.Event{Name: "$pageview", Properties: map[string]any{"referrer": ref}}
	}
	evs := []event.Event{
		ev("https://reddit.com/r/webdev"),
		ev("https://www.reddit.com/"),
		ev("http://news.ycombinator.com/item?id=1"),
		ev("https://twitter.com/x/status/9"),
	}
	// Eq by host matches both reddit URLs (with and without www), nothing else.
	got := Apply(evs, []Filter{{Property: "referrer", Op: Eq, Value: "reddit.com"}})
	if len(got) != 2 {
		t.Fatalf("referrer eq reddit.com (host-aware) = %d, want 2", len(got))
	}
	// A full-URL filter value still works (both sides host-normalized).
	got = Apply(evs, []Filter{{Property: "referrer", Op: Eq, Value: "https://reddit.com/anything"}})
	if len(got) != 2 {
		t.Fatalf("referrer eq full-url (host-aware) = %d, want 2", len(got))
	}
	// Neq is the complement.
	if got = Apply(evs, []Filter{{Property: "referrer", Op: Neq, Value: "reddit.com"}}); len(got) != 2 {
		t.Fatalf("referrer neq reddit.com = %d, want 2", len(got))
	}
	// In matches host-aware across a list.
	got = Apply(evs, []Filter{{Property: "referrer", Op: In, Value: []any{"reddit.com", "twitter.com"}}})
	if len(got) != 3 {
		t.Fatalf("referrer in [reddit,twitter] = %d, want 3", len(got))
	}
	// A non-referrer property stays EXACT (no host normalization leaking out).
	plain := []event.Event{{Name: "x", Properties: map[string]any{"source": "reddit.com"}}}
	if got = Apply(plain, []Filter{{Property: "source", Op: Eq, Value: "reddit"}}); len(got) != 0 {
		t.Fatalf("source eq is exact, got %d matches for 'reddit' vs 'reddit.com'", len(got))
	}
}
