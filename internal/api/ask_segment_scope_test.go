package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Two qualifiers must both survive into the trend and comparison answers.
//
// Both paths had their own `if len(segs) == 1` filter, so a question naming two qualifiers
// dropped BOTH and printed the site-wide total as the answer: "pro signups on mobile over
// time" said 20 when the true pro+mobile figure was 10, and the week-over-week version said
// "20 vs 8" for a true 6 vs 4 — with no note that the qualifiers were gone.
func TestTwoSegmentQuestionsKeepBothFiltersInTrendAndCompare(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC) // Thursday; this week starts Mon Jun 22
	thisWeek := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	lastWeek := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	var evs []event.Event
	add := func(idPrefix, plan, device string, ts time.Time, n int) {
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{
				Name: "signup", DistinctID: fmt.Sprintf("%s%d", idPrefix, i), Timestamp: ts,
				Properties: map[string]any{"plan": plan, "device": device},
			})
		}
	}
	add("pm-cur", "pro", "mobile", thisWeek, 6)
	add("pm-old", "pro", "mobile", lastWeek, 4)
	add("fd-cur", "free", "desktop", thisWeek, 6)
	add("fd-old", "free", "desktop", lastWeek, 4)

	trend := answer("pro signups on mobile over time", evs, now, nil)
	if !strings.Contains(trend, "10 total") {
		t.Errorf("the trend answer dropped a qualifier — pro+mobile is 10 signups: %s", trend)
	}
	if strings.Contains(trend, "20 total") {
		t.Errorf("the trend answer reported the unfiltered total: %s", trend)
	}
	if !strings.Contains(trend, "mobile") || !strings.Contains(trend, "pro") {
		t.Errorf("the trend answer does not say which segments it counted: %s", trend)
	}

	cmp := answer("did pro signups on mobile grow this week vs last week", evs, now, nil)
	if !strings.Contains(cmp, "6 this week") || !strings.Contains(cmp, "4 last week") {
		t.Errorf("the comparison should be 6 vs 4 for pro+mobile: %s", cmp)
	}
	if strings.Contains(cmp, "12 this week") {
		t.Errorf("the comparison dropped both qualifiers and compared everyone: %s", cmp)
	}
}

// An alias literal must match on word boundaries, not anywhere inside a host.
//
// The twitter row lists "t.co", and "producthunt.com" literally contains it
// (producthun-t.co-m). Asking about producthunt therefore resolved a TWITTER segment, found
// no twitter referrer, and answered "0 — no events with referrer = twitter have been sent"
// for 15 real producthunt visitors: an authoritative zero about a channel the user never
// named. hostEquals already guarded the data side; nothing guarded the question side.
func TestHostContainingAnAliasIsNotHijacked(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 15; i++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("ph%d", i), Timestamp: now.Add(-3 * time.Hour),
			Properties: map[string]any{"path": "/", "referrer": "https://producthunt.com/posts/x"},
		})
	}

	for _, seg := range extractSegments("how many visitors from producthunt.com", evs) {
		if seg.label == "twitter" {
			t.Fatalf("producthunt.com resolved a twitter segment: %+v", seg)
		}
	}
	got := answer("how many visitors from producthunt.com", evs, now, nil)
	if strings.Contains(got, "twitter") {
		t.Errorf("a producthunt question was answered about twitter: %s", got)
	}
	if strings.HasPrefix(got, "0 ") {
		t.Errorf("15 producthunt visitors were answered as zero: %s", got)
	}

	// the alias must still work when the user really does mean twitter
	tw := append([]event.Event{}, evs...)
	tw = append(tw, event.Event{
		Name: "$pageview", DistinctID: "tw1", Timestamp: now.Add(-2 * time.Hour),
		Properties: map[string]any{"path": "/", "referrer": "https://t.co/abc"},
	})
	if got := answer("how many visitors from twitter", tw, now, nil); !strings.HasPrefix(got, "1 visitors from twitter") {
		t.Errorf("the twitter alias stopped resolving a real t.co referrer: %s", got)
	}
}

// The lifecycle report is per calendar day, so an hour-shaped window must name the day it
// actually answered.
//
// "how many users went dormant in the last 24 hours" gave from = 12:00 yesterday, which was
// then truncated to yesterday 00:00: the answer was YESTERDAY'S row wearing the "last 24
// hours" label. Provably wrong on its own instance — the same data answered 0 for "today",
// 7 for "yesterday", and 7 for "the last 24 hours".
func TestHourWindowLifecycleNamesTheDayItAnswered(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	var evs []event.Event
	// 7 users with history who stopped after Aug 2 — they went dormant on Aug 3, not Aug 4
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("gone%d", i)
		for _, d := range []int{4, 3, 2} {
			evs = append(evs, event.Event{Name: "open", DistinctID: id,
				Timestamp: now.Truncate(day).AddDate(0, 0, -d).Add(10 * time.Hour)})
		}
	}
	// 5 users active every day, including today
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("live%d", i)
		for d := 0; d <= 5; d++ {
			evs = append(evs, event.Event{Name: "open", DistinctID: id,
				Timestamp: now.Truncate(day).AddDate(0, 0, -d).Add(10 * time.Hour)})
		}
	}

	rolling := answer("how many users went dormant in the last 24 hours", evs, now, nil)
	today := answer("how many users went dormant today", evs, now, nil)
	yesterday := answer("how many users went dormant yesterday", evs, now, nil)

	if !strings.HasPrefix(yesterday, "7 users went dormant") {
		t.Fatalf("fixture is not what this test thinks it is — yesterday: %s", yesterday)
	}
	if leadInt(rolling) != leadInt(today) {
		t.Errorf("the 24-hour window answered a different calendar day than \"today\":\n"+
			"  last 24 hours: %s\n  today:         %s\n  yesterday:     %s", rolling, today, yesterday)
	}
	if !strings.Contains(rolling, "Aug 4") {
		t.Errorf("the 24-hour answer does not name the calendar day it computed: %s", rolling)
	}
}

// leadInt reads the number an answer opens with ("7 users went dormant …" -> 7), -1 if none.
func leadInt(s string) int {
	n, seen := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n, seen = n*10+int(s[i]-'0'), true
	}
	if !seen {
		return -1
	}
	return n
}
