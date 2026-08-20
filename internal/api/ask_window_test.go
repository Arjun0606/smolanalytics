package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// dailyPageviews is one pageview per day, one visitor each, ending the day before `now`.
func dailyPageviews(now time.Time, days int) []event.Event {
	var evs []event.Event
	for d := 0; d < days; d++ {
		ts := now.Truncate(24*time.Hour).AddDate(0, 0, -d).Add(9 * time.Hour)
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("u%d", d), Timestamp: ts,
			Properties: map[string]any{"path": "/"},
		})
	}
	return evs
}

// "last 2 weeks" must not silently become ALL TIME.
//
// parseWindow only ever matched days/hours/minutes, and the refusal path is whole-word so it
// saw "weeks", never "week". The window therefore stayed unscoped: 60 days of history
// answered "60 pageviews from 60 visitors" to a question about fourteen days, with a receipt
// that said "over all recorded events". Neither the right number nor an honest refusal.
func TestLastNWeeksIsTheAskedWindowNotAllTime(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	evs := dailyPageviews(now, 60)

	got := answer("how many pageviews in the last 2 weeks", evs, now, nil)
	if !strings.Contains(got, "14") {
		t.Errorf("last 2 weeks should count 14 daily pageviews, got: %s", got)
	}
	if strings.Contains(got, "60") {
		t.Errorf("the 2-week question was answered over all 60 days of history: %s", got)
	}
	if win, unsupported := parseWindow("how many pageviews in the last 2 weeks", now); unsupported != "" || !win.scoped() {
		t.Errorf("parseWindow left the window unscoped (unsupported=%q) for an explicit 2-week question", unsupported)
	}
}

// Same for months, which are calendar months — the span people read off a calendar.
func TestLastNMonthsIsTheAskedWindowNotAllTime(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	evs := dailyPageviews(now, 200)

	// independent count: events on or after Apr 25 00:00 UTC (now minus two calendar months)
	from := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	want := 0
	for _, e := range evs {
		if !e.Timestamp.Before(from) && e.Timestamp.Before(now) {
			want++
		}
	}
	got := answer("how many pageviews in the last 2 months", evs, now, nil)
	if !strings.Contains(got, fmt.Sprintf("%d", want)) {
		t.Errorf("last 2 months should count %d pageviews, got: %s", want, got)
	}
	if strings.Contains(got, "200") {
		t.Errorf("the 2-month question was answered over all 200 days of history: %s", got)
	}
}

// A time phrase the parser still cannot honor must be REFUSED by name, never widened to all
// time behind the user's back. "last 2 quarters" is the shape that proves the plural forms
// reach the refusal now.
func TestUnhandledPluralTimePhraseIsRefusedNotWidened(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	if tok := unsupportedTimePhrase("how many pageviews in the last 2 quarters"); tok == "" {
		t.Error(`"quarters" is not refused, so the question is silently answered over all time`)
	}
	got := answer("how many pageviews in the last 2 quarters", dailyPageviews(now, 60), now, nil)
	if strings.Contains(got, "60 pageviews") {
		t.Errorf("an unsupported window was answered with the all-time total: %s", got)
	}
}

// "worst day" is the trough. classifyAsk routes best/worst to the same intent and the answer
// only ever computed the MAXIMUM, so "what was our worst day last month" named the BIGGEST
// day — the exact opposite fact, in the confident voice of a computed report.
func TestWorstDayIsTheTroughNotThePeak(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for day := 1; day <= 31; day++ {
		n := 3
		switch day {
		case 5:
			n = 9 // the peak
		case 10:
			n = 1 // the trough
		}
		for u := 0; u < n; u++ {
			evs = append(evs, event.Event{
				Name: "$pageview", DistinctID: fmt.Sprintf("jul%d-%d", day, u),
				Timestamp:  time.Date(2026, 7, day, 10, 0, 0, 0, time.UTC),
				Properties: map[string]any{"path": "/"},
			})
		}
	}

	worst := answer("what was our worst day last month", evs, now, nil)
	if strings.Contains(worst, "Biggest") || strings.Contains(worst, "Jul 5") {
		t.Errorf("the trough question was answered with the peak day: %s", worst)
	}
	if !strings.Contains(worst, "Jul 10") || !strings.Contains(worst, "with 1") {
		t.Errorf("worst day should be Fri Jul 10 with 1: %s", worst)
	}
	// the peak path must keep working
	best := answer("what was our best day last month", evs, now, nil)
	if !strings.Contains(best, "Jul 5") || !strings.Contains(best, "with 9") {
		t.Errorf("best day should still be Sun Jul 5 with 9: %s", best)
	}
}
