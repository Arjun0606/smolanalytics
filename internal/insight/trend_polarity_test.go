package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The week-over-week finding hardcoded "a drop is the warning, a rise is good news" — the same
// assumption polarity_test.go removed from anomalies(), left untouched in the sibling detector
// because that test only ever exercised the 24h path. The headline event is `signup` when it
// exists and the highest-volume event otherwise, so on an instance whose top event records a
// FAILURE the verdict inverted itself: dead clicks collapsing was filed warn and sorted to the
// top of the card as the lead item, while dead clicks tripling was filed info, below the fold
// among the wins.
func TestTrendPolarityFollowsTheEvent(t *testing.T) {
	now := time.Now().UTC()
	build := func(name string, prev7, last7 int) []event.Event {
		var evs []event.Event
		for i := 0; i < prev7; i++ {
			evs = append(evs, ev(name, now.Add(-10*24*time.Hour)))
		}
		for i := 0; i < last7; i++ {
			evs = append(evs, ev(name, now.Add(-3*24*time.Hour)))
		}
		return evs
	}
	for _, tc := range []struct {
		name          string
		prev7, last7  int
		wantSev, dir  string
		wantChangeAbs int
	}{
		{"$deadclick", 100, 300, "warn", "up", 200}, // more dead clicks is the regression
		{"$deadclick", 100, 30, "info", "down", 70}, // dead clicks collapsing is good news
		{"$rageclick", 100, 300, "warn", "up", 200}, //
		{"signup", 100, 30, "warn", "down", 70},     // fewer signups is still the warning
		{"signup", 100, 300, "info", "up", 200},     // and more signups still is not
		{"$deadclick", 100, 105, "info", "up", 5},   // a 5% wobble is not a warning either way
	} {
		var trend Finding
		found := false
		for _, f := range GenerateForFunnel(build(tc.name, tc.prev7, tc.last7), nil) {
			if f.Kind == KindTrend {
				trend, found = f, true
			}
		}
		if !found {
			t.Errorf("%s %d→%d: no week-over-week finding at all", tc.name, tc.prev7, tc.last7)
			continue
		}
		if trend.Severity != tc.wantSev {
			t.Errorf("%s %d→%d is severity %q, want %q\n title: %s",
				tc.name, tc.prev7, tc.last7, trend.Severity, tc.wantSev, trend.Title)
		}
		// the prose still has to describe the direction that actually happened
		if !strings.Contains(trend.Title, tc.dir+" ") {
			t.Errorf("%s %d→%d: title does not read %q: %s", tc.name, tc.prev7, tc.last7, tc.dir, trend.Title)
		}
		if absInt(trend.Rate) != tc.wantChangeAbs {
			t.Errorf("%s %d→%d: Rate=%d, want ±%d — consumers branch on the sign",
				tc.name, tc.prev7, tc.last7, trend.Rate, tc.wantChangeAbs)
		}
	}
}
