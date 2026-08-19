package investigate

import (
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Recovery — the half that closes the loop.
//
// A finding says "checkout fell 33% on the 27th". What no analytics tool then does is come back
// and say whether it ENDED: the reader is left to remember last week's problem and re-derive its
// current state by squinting at a chart. A system that claims to work on your product has to
// report the resolution with the same confidence it reported the break — that is the difference
// between an alert feed and something that heals.
//
// Deliberately narrow: a regression is RECOVERED when the metric's current level is back within
// noise of its pre-drop level, by the same daily-series arithmetic that detected the drop. No
// causality is claimed — we do not know whether the operator's fix did it, and the line says
// "recovered", never "fixed", for exactly that reason.

// recoveryTolerance: current level must be at least this share of the pre-drop level. 0.9 rather
// than 1.0 because day-to-day noise around a full recovery would otherwise flap the label —
// recovered on Tuesday, un-recovered on Wednesday — and a status that flaps is a status nobody
// trusts.
const recoveryTolerance = 0.9

// recoveryWindow: how many recent days form the "current level". Three, matching the window the
// change detector itself uses, so detection and recovery read the same amount of evidence.
const recoveryWindowDays = 3

// MarkRecovered stamps Recovered on every regression finding whose metric has returned to its
// pre-drop level. Call after Attribute; it touches only KindRegression rows.
func MarkRecovered(evs []event.Event, findings []Finding, o Opts) {
	o = o.withDefaults()
	for i := range findings {
		f := &findings[i]
		if f.Kind != KindRegression || f.Event == "" || f.Day == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", f.Day)
		if err != nil {
			continue
		}
		// Too fresh to call: with fewer than recoveryWindowDays since the drop, "recovered"
		// would be a claim about one or two days of data, which is how a status flaps.
		if o.Now.Sub(day) < (recoveryWindowDays+1)*24*time.Hour {
			continue
		}

		before := dailyMean(evs, f.Event, day.AddDate(0, 0, -recoveryWindowDays), day)
		current := dailyMean(evs, f.Event, o.Now.AddDate(0, 0, -recoveryWindowDays), o.Now)
		if before <= 0 {
			continue
		}
		if current >= before*recoveryTolerance {
			f.Recovered = true
			// The queue renders this line INSTEAD of the next move: a recovered finding has no
			// next move, and telling someone to go fix a thing that has already recovered is the
			// fastest way to teach them the queue is stale.
			f.NextMove = fmt.Sprintf("recovered — back to ~%.0f/day against ~%.0f/day before the drop. No action needed; kept here so the week reads whole.", current, before)
		}
	}
}

// dailyMean is the mean daily count of `name` in [from, to). Zero when the span is empty.
func dailyMean(evs []event.Event, name string, from, to time.Time) float64 {
	days := to.Sub(from).Hours() / 24
	if days <= 0 {
		return 0
	}
	n := 0
	for _, e := range evs {
		if e.Name == name && !e.Timestamp.Before(from) && e.Timestamp.Before(to) {
			n++
		}
	}
	return float64(n) / days
}
