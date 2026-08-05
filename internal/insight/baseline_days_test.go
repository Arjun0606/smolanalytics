package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// anomalies() divided the baseline total by a hard-coded 7.0 whether or not seven days of
// data existed. A three-day-old instance at a perfectly flat 30 signups/day has 60 baseline
// events over the two days it has been alive; 60/7 = 8.6, so the verdict announced
// "signup jumped 250% in the last 24h — 30 in the last 24h vs ~9/day normally". Nothing
// jumped, and the event was never at 9/day. minSample (60 ≥ 20) and the 3/day floor
// (8.6 ≥ 3) both waved it through, which is why this shipped.

// flat builds `perDay` events of `name` on each of the last `days` days, the most recent of
// them inside the last-24h window.
func flat(name string, now time.Time, days, perDay int) []event.Event {
	var out []event.Event
	for d := 0; d < days; d++ {
		at := now.Add(-time.Duration(d)*24*time.Hour - 2*time.Hour)
		for i := 0; i < perDay; i++ {
			out = append(out, ev(name, at))
		}
	}
	return out
}

func TestNoAnomalyOnAnInstanceYoungerThanTheBaseline(t *testing.T) {
	now := time.Now().UTC()
	// three days live, 30/day every day, nothing changed.
	if f, ok := findAnomaly(Generate(flat("signup", now, 3, 30))); ok {
		t.Fatalf("flat traffic on a 3-day-old instance was reported as a move: %q | %q", f.Title, f.Detail)
	}
}

// Once enough baseline days exist, the baseline quoted must be the one the data supports —
// the average over the days observed, not the total spread across a window that never held it.
func TestAnomalyBaselineIsPerObservedDay(t *testing.T) {
	now := time.Now().UTC()
	// six days live: five baseline days at 30/day, then 60 in the last 24h — a real doubling.
	evs := flat("signup", now, 6, 30)
	evs = append(evs, flat("signup", now, 1, 30)...) // the last 24h carries 60, not 30

	f, ok := findAnomaly(Generate(evs))
	if !ok {
		t.Fatal("a genuine 2× spike over a five-day baseline must still be reported")
	}
	if !strings.Contains(f.Detail, "~30/day") {
		t.Errorf("baseline is divided by the window instead of the days observed: %q", f.Detail)
	}
	if !strings.Contains(f.Title, "jumped 100%") {
		t.Errorf("deviation is measured against a fabricated baseline: %q", f.Title)
	}
}
