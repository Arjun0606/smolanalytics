package investigate

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Cause attribution — the half that makes a finding read like a colleague rather than an alarm.
//
// "checkout fell 70% on Aug 3" is an alert. Anyone can compute it and nobody can act on it. What a
// PM actually produces after a morning of slicing is:
//
//	checkout fell 70% on Aug 3. Ship a1b3f9 landed that day. 61% of the loss is mobile Safari.
//
// Two independent questions, deliberately kept apart because they fail differently:
//
//   - WHICH SHIP — correlation against recorded deploy markers. Often unavailable (nobody records
//     deploys on most instances) and always only correlation.
//   - WHERE IT HURTS — which segment carries the drop. Pure arithmetic over the events we already
//     hold, available on every instance, and frequently the more useful of the two: "it is only
//     mobile Safari" turns an unbounded investigation into a twenty-minute one.
//
// When neither can be established the finding says so. An empty cause line reads as though nobody
// looked; "no deploy recorded near this day" tells the reader what to fix so next time we can.

// minSegmentShare is the concentration below which naming a segment is noise. If the worst
// segment carries 30% of a drop it is not "where the problem is", it is just the biggest slice of
// an even spread — and pointing at it would send someone down a wrong path with confidence.
const minSegmentShare = 0.55

// causeProps are the dimensions worth blaming, in the order a person would check them. Browser
// and device first because a regression concentrated there is almost always a real front-end bug;
// country and referrer later because concentration there is more often a traffic-mix change than
// a defect.
var causeProps = []string{"browser", "os", "device", "path", "country", "referrer"}

// Attribute fills in the Cause of every finding it can explain, in place.
func Attribute(evs []event.Event, findings []Finding, deps []deploys.Deploy, o Opts) {
	o = o.withDefaults()
	for i := range findings {
		f := &findings[i]
		if f.Kind == KindDeadShip || f.Day == "" {
			continue // a dud's cause is "it did nothing", which readFlat already stated
		}
		day, err := time.Parse("2006-01-02", f.Day)
		if err != nil {
			continue
		}
		ship := shipNear(evs, f.Event, day, deps, o)
		seg := worstSegment(evs, f.Event, day, o)

		switch {
		case ship != "" && seg != "":
			f.Cause = ship + " " + seg
		case ship != "":
			f.Cause = ship
		case seg != "":
			f.Cause = seg
		default:
			f.Cause = "no deploy recorded near this day and the drop is spread evenly across " +
				"browsers, devices and pages — record deploys and this line becomes 'which ship did it'"
		}
	}
}

// shipNear returns a sentence naming the deploy that best explains the change, or "".
//
// It reuses deploys.ComputeImpact rather than matching on date alone, so a deploy is only named
// when the metric actually moved across it. Date-matching would blame whichever ship happened to
// be closest, which on a team that deploys daily is a coin toss dressed as a finding.
func shipNear(evs []event.Event, name string, day time.Time, deps []deploys.Deploy, o Opts) string {
	if len(deps) == 0 || name == "" {
		return ""
	}
	series := dailyPoints(evs, name, o)
	if len(series) == 0 {
		return ""
	}
	// THE THRESHOLD IS A FRACTION, NOT A PERCENTAGE. This read 25 — meaning a 2,500% swing —
	// so Significant was never true and shipNear returned "" for every customer who had gone to
	// the trouble of recording deploys. The dashboard's own fallback line invites them to do
	// exactly that ("record deploys and this line becomes 'which ship did it'"), so the product
	// was asking for work it could not pay off. Every other caller passes 0.25; so does this one.
	impacts := deploys.ComputeImpact(series, deps, 3, deploys.SignificantChange)
	var best *deploys.Impact
	for i := range impacts {
		im := impacts[i]
		if !im.Significant {
			continue
		}
		// within two days of the detected change — beyond that the association is a guess
		if d := im.At.Sub(day); d > 48*time.Hour || d < -48*time.Hour {
			continue
		}
		if best == nil || absF(deref(im.DeltaPct)) > absF(deref(best.DeltaPct)) {
			best = &impacts[i]
		}
	}
	if best == nil {
		return ""
	}
	msg := best.Message
	if len(msg) > 60 {
		msg = msg[:57] + "…"
	}
	if msg == "" {
		msg = best.Ref
	}
	return fmt.Sprintf("Ship %s %q landed the same day (correlation, not proof).", best.ShortSHA, msg)
}

// worstSegment finds the dimension value carrying most of the drop, or "".
//
// Compares each value's share of the metric BEFORE the change day against AFTER, and reports the
// value whose loss accounts for most of the total. Concentration is the signal: an even spread
// across every browser is a demand problem, one browser losing everything is a bug.
func worstSegment(evs []event.Event, name string, day time.Time, o Opts) string {
	// EQUAL WINDOWS EITHER SIDE, or the arithmetic is meaningless.
	//
	// The first version compared "everything before the change day" against "everything after".
	// Those are different lengths, so a value's before-count was inflated by however much longer
	// the before period happened to be — and the shares came out over 100%. The demo printed
	// "117% of the loss is browser=Safari", which is the kind of number that ends a sales call.
	span := windowEitherSide(evs, name, day, o)
	if span <= 0 {
		return ""
	}
	from, to := day.AddDate(0, 0, -span), day.AddDate(0, 0, span)

	type ba struct{ before, after int }
	for _, prop := range causeProps {
		counts := map[string]*ba{}
		totalBefore, totalAfter := 0, 0
		for _, e := range evs {
			if e.Name != name || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
				continue
			}
			v, _ := e.Properties[prop].(string)
			if v == "" {
				continue
			}
			c := counts[v]
			if c == nil {
				c = &ba{}
				counts[v] = c
			}
			if e.Timestamp.Before(day) {
				c.before++
				totalBefore++
			} else {
				c.after++
				totalAfter++
			}
		}
		lost := totalBefore - totalAfter
		if lost <= 0 || totalBefore == 0 {
			continue
		}
		// the value that lost the most, in absolute terms
		type row struct {
			v    string
			lost int
		}
		var rows []row
		for v, c := range counts {
			if d := c.before - c.after; d > 0 {
				rows = append(rows, row{v, d})
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].lost != rows[j].lost {
				return rows[i].lost > rows[j].lost
			}
			return rows[i].v < rows[j].v // deterministic: map order must never decide a finding
		})
		// Capped at 1. A value can lose more than the net total when another value GREW over the
		// same window, and "117% of the loss" is arithmetic nobody will trust twice.
		share := float64(rows[0].lost) / float64(lost)
		if share > 1 {
			share = 1
		}
		if share < minSegmentShare {
			continue // spread evenly — naming the biggest slice would mislead
		}
		return fmt.Sprintf("%.0f%% of the loss is %s=%s.", share*100, prop, rows[0].v)
	}
	return ""
}

// dailyPoints builds the series ComputeImpact expects.
func dailyPoints(evs []event.Event, name string, o Opts) []deploys.Point {
	per := map[time.Time]int{}
	from := o.Now.AddDate(0, 0, -o.Days)
	for _, e := range evs {
		if e.Name != name || e.Timestamp.Before(from) {
			continue
		}
		per[e.Timestamp.UTC().Truncate(24*time.Hour)]++
	}
	out := make([]deploys.Point, 0, len(per))
	for d, n := range per {
		out = append(out, deploys.Point{Date: d, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// windowEitherSide is how many whole days of data exist on the SHORTER side of the change day,
// capped at a week. Both comparison windows use it, so neither side is advantaged by simply
// having been observed for longer.
func windowEitherSide(evs []event.Event, name string, day time.Time, o Opts) int {
	var first, last time.Time
	for _, e := range evs {
		if e.Name != name {
			continue
		}
		if first.IsZero() || e.Timestamp.Before(first) {
			first = e.Timestamp
		}
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	if first.IsZero() {
		return 0
	}
	before := int(day.Sub(first).Hours() / 24)
	after := int(last.Sub(day).Hours() / 24)
	span := before
	if after < span {
		span = after
	}
	if span > 7 {
		span = 7
	}
	return span
}
