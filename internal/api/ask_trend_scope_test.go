package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Adding "over time" to a question must not change its answer from twenty to none.
//
// A signup event carries no referrer — the referrer lives on the user's landing pageview — so
// "signups from reddit" has to mean "signups BY reddit-acquired users". answerSegment knew
// that; answerTrendText filtered EVENTS instead, so the same ask bar answered "20 signup events
// from reddit" and, seconds later, "No signup events from reddit the last 30 days, so the trend
// is flat at zero." A confident zero is the worst possible wrong answer.
func TestTrendSegmentScopesByUserLikeTheNonTrendAnswer(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 20; i++ {
		id := "r" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		// the referrer is on the landing pageview only, as it is in real data
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: id, Timestamp: now.AddDate(0, 0, -5),
			Properties: map[string]any{"referrer": "https://reddit.com/r/webdev", "path": "/"},
		})
		evs = append(evs, event.Event{
			Name: "signup", DistinctID: id, Timestamp: now.AddDate(0, 0, -5).Add(time.Minute),
		})
	}

	plain := askText(t, evs, "how many signups from reddit")
	trend := askText(t, evs, "signups from reddit over time")

	if strings.Contains(strings.ToLower(trend), "flat at zero") || strings.Contains(trend, "No \"signup\"") {
		t.Errorf("the trend answer asserts zero for a question the same bar answers with a real number:\n"+
			"  plain: %s\n  trend: %s", plain, trend)
	}
	if !strings.Contains(trend, "20") {
		t.Errorf("the trend answer does not mention the 20 reddit signups:\n  plain: %s\n  trend: %s", plain, trend)
	}
}

// askText runs one question through the real ask path and returns the answer sentence.
func askText(t *testing.T, evs []event.Event, q string) string {
	t.Helper()
	return answer(strings.ToLower(q), evs, time.Now().UTC(), nil)
}
