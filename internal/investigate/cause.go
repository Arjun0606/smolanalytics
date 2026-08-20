package investigate

import (
	"fmt"
	"sort"
	"strings"
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

// Unattributed is the prefix of the cause line written when nothing could be narrowed.
//
// Exported so a RENDERER can tell an explained finding from an unexplained one without matching
// on prose. Every surface used to print this whole two-line sentence per finding, and on an
// instance with no deploys recorded that is most of them — the dashboard showed the identical
// paragraph twice in its first screen. The marketing panel already renders it as three words
// per row plus the instruction once underneath; this is what lets the product do the same.
const Unattributed = "no deploy recorded near this day"

// Attributed reports whether this finding's cause narrows it to something specific — a ship, a
// segment — rather than saying nothing could be found.
func (f Finding) Attributed() bool {
	return f.Cause != "" && !strings.HasPrefix(f.Cause, Unattributed)
}

// causeProps are the BUILT-IN dimensions worth blaming, in the order a person would check them.
// Browser and device first because a regression concentrated there is almost always a real
// front-end bug; country and referrer later because concentration there is more often a
// traffic-mix change than a defect.
//
// THE LIST IS NOT THE CEILING. These six cover what autocapture stamps on every event, and for a
// year they were the ONLY dimensions checked — so a drop carried entirely by free-plan users, or
// by one utm_campaign, was reported as "spread evenly", which is a false statement produced by
// not looking. eventProps() below discovers the instance's own custom properties per event and
// checks those too, because what is worth blaming depends on what the operator actually tracks,
// and a hardcoded list is the wrong shape for that fact.
var causeProps = []string{"browser", "os", "device", "path", "country", "referrer"}

// vendorAliases maps each built-in dimension to the names OTHER TOOLS write for the same thing.
//
// Every vendor prefixes its autocapture properties with "$", and eventProps() deliberately skips
// "$" keys because those are OUR bookkeeping. The result was measured and is worse than missing a
// finding: on PostHog-shaped data the exact same drop that reads "100% of the loss is
// browser=Safari" on our own events came out as "the drop is spread evenly across browsers,
// devices and pages" — a confident sentence about dimensions the code never examined. That is a
// false statement shipped to anyone who imported from another tool, which is a path we sell.
//
// Aliased here rather than renamed at import time on purpose: this also fixes data already
// imported, and data POSTed in a vendor's shape straight to /v1/events.
var vendorAliases = map[string][]string{
	"browser":  {"$browser"},
	"os":       {"$os"},
	"device":   {"$device_type", "$device"},
	"path":     {"$pathname", "$current_url"},
	"country":  {"$geoip_country_name", "$geoip_country_code"},
	"referrer": {"$referring_domain", "$referrer"},
}

// propValue reads a dimension from an event under our name or any vendor's name for it.
func propValue(e event.Event, prop string) string {
	if v, ok := e.Properties[prop].(string); ok && v != "" {
		return v
	}
	for _, alias := range vendorAliases[prop] {
		if v, ok := e.Properties[alias].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// maxDiscoveredProps caps how many custom dimensions are tried per finding, keeping the scan
// bounded on events with sprawling property bags.
const maxDiscoveredProps = 4

// eventProps returns the event's own string-valued property keys, most-present first — the
// dimensions the OPERATOR chose to record, which the built-in list cannot know about.
func eventProps(evs []event.Event, name string, from, to time.Time) []string {
	skip := map[string]bool{}
	for _, p := range causeProps {
		skip[p] = true
	}
	counts := map[string]int{}
	total := 0
	for _, e := range evs {
		if e.Name != name || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
			continue
		}
		total++
		for k, v := range e.Properties {
			if skip[k] || len(k) == 0 || k[0] == '$' {
				continue // $-props are this tool's own bookkeeping, never a customer dimension
			}
			if sv, ok := v.(string); ok && sv != "" {
				counts[k]++
			}
		}
	}
	if total == 0 {
		return nil
	}
	var keys []string
	for k, n := range counts {
		// Present on at least half the rows, or a "worst segment" over it describes a minority
		// of the data while reading as though it describes the whole.
		if float64(n)/float64(total) >= 0.5 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j] // deterministic: same instance, same order, every run
	})
	if len(keys) > maxDiscoveredProps {
		keys = keys[:maxDiscoveredProps]
	}
	return keys
}

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
		seg, examined := worstSegment(evs, f.Event, day, o)

		switch {
		case ship != "" && seg != "":
			f.Cause = ship + " " + seg
		case ship != "":
			f.Cause = ship
		case seg != "":
			f.Cause = seg
		default:
			// "the drop" was hardcoded, so a metric that ROSE 34% was explained with a sentence
			// about a drop. On the busiest instances most findings are rises, which made the
			// single most-repeated line on the page the one visibly describing something else.
			// The word follows the finding.
			// Taken from Kind, not from the headline text: the kind is the structured fact the
			// headline itself is rendered from, so the two cannot drift apart.
			word := "change"
			switch f.Kind {
			case KindRegression:
				word = "drop"
			case KindSurge:
				word = "rise"
			}
			if examined {
				f.Cause = Unattributed + " and the " + word + " is spread evenly across " +
					"browsers, devices and pages — record deploys and this line becomes 'which ship did it'"
			} else {
				// Nothing to slice by. Say that, rather than describing a spread we never measured.
				f.Cause = Unattributed + ", and these events carry no properties to break the " +
					word + " down by — record deploys and send a property or two (browser, plan, path), " +
					"and this line names the ship and who it hit"
			}
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
func worstSegment(evs []event.Event, name string, day time.Time, o Opts) (string, bool) {
	// EQUAL WINDOWS EITHER SIDE, or the arithmetic is meaningless.
	//
	// The first version compared "everything before the change day" against "everything after".
	// Those are different lengths, so a value's before-count was inflated by however much longer
	// the before period happened to be — and the shares came out over 100%. The demo printed
	// "117% of the loss is browser=Safari", which is the kind of number that ends a sales call.
	span := windowEitherSide(evs, name, day, o)
	if span <= 0 {
		return "", false
	}
	from, to := day.AddDate(0, 0, -span), day.AddDate(0, 0, span)

	type ba struct{ before, after int }
	props := append(append([]string{}, causeProps...), eventProps(evs, name, from, to)...)
	// examined records whether ANY dimension was actually present on these events. Without it,
	// "no concentration found" and "there was nothing to look at" produce the same empty string,
	// and the caller states the first while meaning the second.
	examined := false
	for _, prop := range props {
		counts := map[string]*ba{}
		totalBefore, totalAfter := 0, 0
		for _, e := range evs {
			if e.Name != name || e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
				continue
			}
			v := propValue(e, prop)
			if v == "" {
				continue
			}
			examined = true
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
		return fmt.Sprintf("%.0f%% of the loss is %s=%s.", share*100, prop, rows[0].v), true
	}
	return "", examined
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
