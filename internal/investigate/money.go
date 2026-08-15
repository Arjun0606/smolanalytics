package investigate

import (
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Sizing a finding in money instead of people.
//
// Rule 1 of this package is "rank by money, not p-value", and it could not be honoured: nothing
// anywhere ever wrote Cost.UsdPerMonth, so the money branch of the ranking comparator (which has
// been sitting in rank() the whole time) was unreachable and every finding fell back to people.
//
// The convention this reads is not invented here. internal/instrument already teaches `checkout`
// with properties ["plan", "amount"], the demo generator already emits amount: 30.0, and the
// docs show `{"name":"checkout","properties":{"amount":29}}` in four places. The value was in the
// log, taught to customers, and ignored by the one report where it mattered most.
//
// WHAT IT DELIBERATELY DOES NOT DO: attribute downstream revenue. If `signup` falls, the money
// lost is eventually real, but computing it needs a funnel-conversion model, and a model is a
// place to invent a number. A finding is sized in money only when the metric ITSELF carries
// revenue. Everything else stays sized in people and says so.

// revenueProps are the property names that mean money, in priority order. `amount` first because
// it is the one the instrumenter teaches and the docs show; the others are common enough in
// imported logs (Mixpanel and Amplitude both use `revenue`) that refusing them would size a
// migrated customer's findings in people for no reason.
var revenueProps = []string{"amount", "revenue", "value", "price", "total"}

// minRevenueEvents is the floor below which a mean is not worth computing. Three sales do not
// establish an average order value, and a headline dollar figure carries far more authority than
// a headline people figure — so the bar to state one is higher, not the same.
const minRevenueEvents = 20

// revenuePerPerson returns the mean revenue per PERSON who did `name` inside the window, and
// whether the log genuinely supports the figure.
//
// Per person rather than per event, because the thing being multiplied is a count of people. A
// customer who checks out three times at $10 is $30 of exposure, not $10, and dividing the total
// by the event count would understate every finding on a product with repeat purchases.
func revenuePerPerson(evs []event.Event, name string, o Opts) (float64, string, bool) {
	if name == "" {
		return 0, "", false
	}
	prop := revenuePropFor(evs, name)
	if prop == "" {
		return 0, "", false
	}

	cutoff := o.Now.AddDate(0, 0, -o.Days)
	total := 0.0
	n := 0
	people := map[string]bool{}
	for _, e := range evs {
		if e.Name != name || e.Timestamp.Before(cutoff) {
			continue
		}
		v, ok := event.Numeric(e.Properties[prop])
		if !ok || v <= 0 {
			// A zero or missing amount is a real event that carried no money — a free plan, a
			// $0 trial conversion. Counting it in the denominator but not the numerator is what
			// makes the mean honest; counting it in neither would inflate the average order
			// value by exactly the share of your customers who pay nothing.
			if ok && v == 0 {
				people[e.DistinctID] = true
				n++
			}
			continue
		}
		total += v
		n++
		people[e.DistinctID] = true
	}

	if n < minRevenueEvents || len(people) == 0 || total <= 0 {
		return 0, "", false
	}
	return total / float64(len(people)), prop, true
}

// revenuePropFor picks which property on `name` carries money, or "" if none does.
//
// Chosen by which candidate is most consistently present AND numeric, not merely by which name
// appears first: a log where half the `amount` values are strings is a log where the mean would
// silently describe a subset, and describing a subset as the whole is the failure this package
// exists to prevent.
func revenuePropFor(evs []event.Event, name string) string {
	type tally struct{ numeric, seen int }
	counts := map[string]*tally{}
	for _, e := range evs {
		if e.Name != name || len(e.Properties) == 0 {
			continue
		}
		for _, p := range revenueProps {
			v, present := e.Properties[p]
			if !present {
				continue
			}
			t := counts[p]
			if t == nil {
				t = &tally{}
				counts[p] = t
			}
			t.seen++
			if _, ok := event.Numeric(v); ok {
				t.numeric++
			}
		}
	}
	// Stable: candidates are considered in the declared priority order, so a log carrying both
	// `amount` and `revenue` resolves the same way on every run.
	best := ""
	bestN := 0
	for _, p := range revenueProps {
		t := counts[p]
		if t == nil || t.numeric == 0 {
			continue
		}
		// At least four in five of the values present must be numeric. Below that the property
		// is being used for something else, or the customer is sending mixed types, and either
		// way a mean over the numeric subset is not the number they think it is.
		if float64(t.numeric)/float64(t.seen) < 0.8 {
			continue
		}
		if t.numeric > bestN {
			best, bestN = p, t.numeric
		}
	}
	return best
}

// sizeInMoney upgrades a finding's Cost from people to dollars when the metric carries revenue.
// Called after People is set, and leaves the finding untouched when it cannot do better — the
// people figure is a real answer, not a placeholder.
func sizeInMoney(evs []event.Event, f *Finding, o Opts) {
	if f.Cost.People <= 0 {
		return
	}
	per, prop, ok := revenuePerPerson(evs, f.Event, o)
	if !ok {
		return
	}
	f.Cost.UsdPerMonth = round2(per * float64(f.Cost.People))
	f.Cost.Basis = BasisRevenue(prop)
}

// StampSizes fills SizeText on every finding, so the JSON carries the rendered figure and no
// consumer — in any language — has to reassemble it. Called once at the end of a pass.
func StampSizes(fs []Finding) {
	for i := range fs {
		fs[i].Cost.SizeText = fs[i].Cost.Size()
	}
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// scannedRevenue reports which of the investigated metrics carry revenue, so a brief can say why
// a finding is sized in people rather than leaving the reader to assume the tool cannot do money.
func scannedRevenue(evs []event.Event, names []string) []string {
	var out []string
	for _, n := range names {
		if revenuePropFor(evs, n) != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
