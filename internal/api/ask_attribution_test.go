package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A visitor belongs to ONE acquisition channel, the one they arrived from.
//
// Ten people landed from google and came back later via reddit. The traffic report (first
// touch, like web.Compute) said "google.com 10", but asking channel by channel answered
// "10 visitors from google" AND "10 visitors from reddit" — 20 attributed visitors out of
// 10 real ones, with the reddit number contradicting the very report its receipt cites.
func TestVisitorsFromChannelAreFirstTouchLikeTheTrafficReport(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("v%d", i)
		evs = append(evs,
			event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-10 * time.Hour),
				Properties: map[string]any{"path": "/", "referrer": "https://www.google.com/"}},
			event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-2 * time.Hour),
				Properties: map[string]any{"path": "/pricing", "referrer": "https://www.reddit.com/r/x"}},
		)
	}

	sources := answer("where is our traffic coming from", evs, now)
	google := answer("how many visitors from google", evs, now)
	reddit := answer("how many visitors from reddit", evs, now)

	if !strings.Contains(sources, "google.com 10") {
		t.Fatalf("fixture is not what this test thinks it is — traffic report: %s", sources)
	}
	if !strings.HasPrefix(google, "10 visitors from google") {
		t.Errorf("first-touch google visitors should be all 10: %s", google)
	}
	// the honest number is 0: nobody was ACQUIRED from reddit. Anything else re-attributes
	// visitors the traffic report already gave to google.
	if !strings.HasPrefix(reddit, "0 visitors from reddit") {
		t.Errorf("reddit visitors are double-counted — the traffic report attributes all 10 to google:\n"+
			"  sources: %s\n  google:  %s\n  reddit:  %s", sources, google, reddit)
	}
}

// The same rule has to hold on the OTHER phrasings of the same question, or the ask bar
// contradicts itself depending on how the sentence was worded.
func TestFirstTouchAttributionHoldsAcrossAskPhrasings(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("v%d", i)
		evs = append(evs,
			event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-10 * time.Hour),
				Properties: map[string]any{"path": "/", "referrer": "https://www.google.com/"}},
			event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-2 * time.Hour),
				Properties: map[string]any{"path": "/pricing", "referrer": "https://www.reddit.com/r/x"}},
		)
	}

	// "google vs reddit" runs through answerSegVsSeg
	vs := answer("google vs reddit visitors", evs, now)
	if !strings.Contains(vs, "google 10") || !strings.Contains(vs, "reddit 0") {
		t.Errorf("segment-vs-segment must use the same first-touch attribution: %s", vs)
	}
	// "over time" runs through answerTrendText
	trend := answer("visitors from reddit over time", evs, now)
	if strings.Contains(trend, "10 total") {
		t.Errorf("the trend answer re-attributes google visitors to reddit: %s", trend)
	}
}

// Products that never send a referrer but stamp source=<channel> on their own events must
// NOT be pushed into a confident zero by first-touch attribution: when no landing event
// carries the property, any-touch is the only honest read of that data.
func TestSourceOnlyOnCustomEventsStillCounts(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("s%d", i)
		evs = append(evs,
			event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-5 * time.Hour),
				Properties: map[string]any{"path": "/"}},
			event.Event{Name: "signup", DistinctID: id, Timestamp: now.Add(-4 * time.Hour),
				Properties: map[string]any{"source": "twitter"}},
		)
	}
	got := answer("how many visitors from twitter", evs, now)
	if strings.HasPrefix(got, "0 ") {
		t.Errorf("source is only on the signup event, so first-touch must fall back to any-touch: %s", got)
	}
}
