package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func ev(name string, ts time.Time) event.Event {
	return event.Event{Name: name, DistinctID: "u", Timestamp: ts}
}

// build events: `name` at `perDay` per day across the prior 7 days, and `last24` today.
func series(name string, now time.Time, perDay, last24 int) []event.Event {
	var out []event.Event
	for d := 1; d <= 7; d++ { // prior-week baseline (days -1..-7 before the last 24h)
		day := now.Add(-time.Duration(d)*24*time.Hour - 12*time.Hour)
		for i := 0; i < perDay; i++ {
			out = append(out, ev(name, day))
		}
	}
	for i := 0; i < last24; i++ {
		out = append(out, ev(name, now.Add(-time.Hour)))
	}
	return out
}

func findAnomaly(fs []Finding) (Finding, bool) {
	for _, f := range fs {
		if strings.Contains(f.Title, "in the last 24h") {
			return f, true
		}
	}
	return Finding{}, false
}

func TestAnomalyDropDetected(t *testing.T) {
	now := time.Now().UTC()
	// normally ~20/day, today 2 → ~90% drop
	evs := series("pageview", now, 20, 2)
	f, ok := findAnomaly(Generate(evs))
	if !ok || f.Severity != "warn" || !strings.Contains(f.Title, "dropped") {
		t.Fatalf("expected a drop anomaly (warn), got %+v (ok=%v)", f, ok)
	}
}

func TestAnomalySpikeDetected(t *testing.T) {
	now := time.Now().UTC()
	evs := series("signup", now, 10, 40) // ~10/day normally, 40 today → spike
	f, ok := findAnomaly(Generate(evs))
	if !ok || f.Severity != "info" || !strings.Contains(f.Title, "jumped") {
		t.Fatalf("expected a spike anomaly (info), got %+v (ok=%v)", f, ok)
	}
}

func TestAnomalyNoiseGuard(t *testing.T) {
	now := time.Now().UTC()
	// only ~1/day baseline (below the min), today 0 → must NOT flag (too noisy)
	evs := series("rareclick", now, 1, 0)
	if f, ok := findAnomaly(Generate(evs)); ok {
		t.Fatalf("low-volume event should not trigger an anomaly, got %+v", f)
	}
}

func TestAnomalyStableNoFalseAlarm(t *testing.T) {
	now := time.Now().UTC()
	evs := series("pageview", now, 20, 20) // flat: 20/day and 20 today → no anomaly
	if f, ok := findAnomaly(Generate(evs)); ok {
		t.Fatalf("stable rate should not trigger an anomaly, got %+v", f)
	}
}
