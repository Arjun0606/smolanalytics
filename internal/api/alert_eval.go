package api

import (
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
)

// evalAlert decides whether one alert fires, and returns the sentence explaining why.
//
// The original shape — a raw count against a number you typed — asks you to already know your own
// numbers, and to keep knowing them as the product grows. That is why most alerts are either never
// set up or muted within a month. The three shapes below ask nothing: they compare against the
// product's own recent behaviour instead of against a constant.
func evalAlert(a alert.Alert, evs []event.Event, now, cutoff time.Time, window time.Duration) (value float64, met bool, detail string) {
	countIn := func(name string, from, to time.Time) float64 {
		var n float64
		for _, e := range evs {
			ts := e.Timestamp
			if e.Name == name && !ts.Before(from) && ts.Before(to) {
				n++
			}
		}
		return n
	}

	switch a.Kind() {
	case alert.KindRelative:
		// Like for like: the same-length window immediately before this one.
		cur := countIn(a.Event, cutoff, now)
		prev := countIn(a.Event, cutoff.Add(-window), cutoff)
		value = cur
		if prev == 0 {
			// There is no percentage change from nothing, and inventing one here would page
			// somebody every time a new event fires for the first time.
			return value, false, fmt.Sprintf("%s has no prior window to compare against yet", a.Event)
		}
		changePct := (cur - prev) / prev * 100
		met = (a.Op == "lt" && changePct <= -a.Threshold) || (a.Op == "gt" && changePct >= a.Threshold)
		dir := "up"
		if changePct < 0 {
			dir = "down"
		}
		return value, met, fmt.Sprintf("%s is %s %.0f%% (%.0f against %.0f in the previous window)",
			a.Event, dir, absf(changePct), cur, prev)

	case alert.KindAnomaly:
		// The detector that has been in internal/insight the whole time, wired to nothing. The
		// dashboard could say "signups dropped 40% in the last 24h" and there was no way to be
		// TOLD it without opening the page.
		//
		// Only warnings fire. The detector also reports good news, and paging someone because
		// signups doubled is how an alert channel gets muted.
		for _, f := range insight.Generate(evs) {
			if f.Kind == insight.KindAnomaly && f.Severity == "warn" {
				return float64(f.Rate), true, f.Title + " — " + f.Detail
			}
		}
		return 0, false, "nothing unusual against its own trailing baseline"

	case alert.KindRatio:
		// Share, not count. Most real health is a ratio: not "how many errors" but "what share of
		// sessions hit one".
		num := countIn(a.Event, cutoff, now)
		den := countIn(a.Against, cutoff, now)
		if den == 0 {
			return 0, false, fmt.Sprintf("no %s in this window, so %s per %s does not exist",
				a.Against, a.Event, a.Against)
		}
		value = num / den * 100
		met = (a.Op == "gt" && value > a.Threshold) || (a.Op == "lt" && value < a.Threshold)
		return value, met, fmt.Sprintf("%s per %s is %.1f%% (%.0f of %.0f)",
			a.Event, a.Against, value, num, den)

	default:
		value = countIn(a.Event, cutoff, now)
		met = (a.Op == "gt" && value > a.Threshold) || (a.Op == "lt" && value < a.Threshold)
		return value, met, fmt.Sprintf("%s: %.0f events in the last %dh", a.Event, value, a.WindowHours)
	}
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
