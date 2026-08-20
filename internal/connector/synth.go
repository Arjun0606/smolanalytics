package connector

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Synthesis: turning aggregate rows back into the event slice the investigator already reads.
//
// The alternative was to teach internal/investigate to consume aggregates. That is a second
// implementation of change detection, cause attribution and money sizing, living beside the first,
// and the two would agree until the day they did not — which would be the day someone compared a
// vendor-connected instance against a directly-instrumented one and got two answers to one
// question. This package exists because that already happened once with the importer.
//
// So the aggregate is expanded, in memory, into events that produce the SAME NUMBERS the raw rows
// would have produced under every function investigate.Run calls. Not the same events. The same
// answers. The difference is the whole honesty problem, and it is why every field below is chosen
// against a specific line of the investigator rather than for realism:
//
//	Count       -> whatchanged.Series counts rows per UTC day.
//	First/Last  -> cause.windowEitherSide measures whole days from the metric's first and last row.
//	Segments    -> cause.worstSegment counts one dimension's values either side of the change day.
//	Revenue     -> money.revenuePerPerson sums a numeric property and divides by distinct people.
//
// Everything else about a real event — who, in what order, in what session — is NOT reconstructed,
// because it cannot be, and the reports that need it are refused by name in Degradations rather
// than answered from invented identity. See DegradeSequence and DegradePeople.

// synthPrefix marks every id this file mints, so a row that escapes into a store is greppable
// rather than mysterious. Nothing here is ever persisted (see the package doc), and the prefix is
// the check on that promise, not the promise itself.
const synthPrefix = "agg:"

// Synthesize expands a Snapshot into the events investigate.Run consumes.
//
// The returned degradations are the ones DISCOVERED in this snapshot — a dimension the vendor had
// to drop, a money figure whose window disagrees. The standing ones that are true of every
// aggregate read (sequence, per-person rates) are added by Investigate, not here, because a
// capability that is never available must not depend on a code path noticing it is missing.
func Synthesize(s Snapshot) ([]event.Event, []Degradation) {
	var degs []Degradation
	out := make([]event.Event, 0, totalCount(s.Buckets))

	byName := map[string][]int{} // event name -> indices into out, chronological

	buckets := append([]Bucket(nil), s.Buckets...)
	sortBuckets(buckets)

	for _, b := range buckets {
		if b.Count <= 0 {
			continue
		}
		stamps := spread(b)
		// Dimension values are laid down one dimension at a time, each starting from row 0.
		//
		// MARGINALS, NOT A JOINT. Row 3 getting browser=Safari and os=Windows is not a claim that
		// anyone used Safari on Windows — worstSegment never joins two dimensions, so the joint
		// distribution is data nobody reads. Fabricating a plausible-looking joint would cost a
		// cross-product query (every dimension's cardinality multiplied together) to make a number
		// no caller consumes more convincing to a human who should never see it.
		assign := map[string][]string{}
		for dim, vals := range b.Segments {
			col, over := column(vals, b.Count)
			if over {
				degs = append(degs, Degradation{
					Code: "segment_overflow",
					What: fmt.Sprintf("%s was not broken down by %s on %s", b.Event, LocalDim[dim], b.Day.Format(time.DateOnly)),
					Why: fmt.Sprintf("the vendor reported more rows for that dimension (%d) than events on the day (%d), so the breakdown and the total disagree.",
						sum(vals), b.Count),
					Fix: "This is a vendor-side inconsistency; the count itself is still exact and the finding stands without the breakdown.",
				})
				continue
			}
			assign[dim] = col
		}

		for i := 0; i < b.Count; i++ {
			e := event.Event{
				Name:       b.Event,
				Timestamp:  stamps[i],
				DistinctID: fmt.Sprintf("%s%s:%s:%d", synthPrefix, b.Day.Format(time.DateOnly), b.Event, i),
			}
			e.ID = e.DistinctID
			for dim, col := range assign {
				if v := col[i]; v != "" {
					if e.Properties == nil {
						e.Properties = map[string]any{}
					}
					e.Properties[dim] = v
				}
			}
			byName[b.Event] = append(byName[b.Event], len(out))
			out = append(out, e)
		}
	}

	names := make([]string, 0, len(s.Revenue))
	for n := range s.Revenue {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if d, ok := overlayRevenue(out, byName[n], n, s); !ok {
			degs = append(degs, d)
		}
	}
	return out, degs
}

// spread places b.Count timestamps inside one day, pinned to the real first and last.
//
// The pinning is not decoration. cause.windowEitherSide computes whole days from the change day to
// the metric's first and last event and takes the SHORTER side as the comparison window. Pin every
// synthesised row to midnight and that distance moves by up to a day, the window either side of
// the change moves with it, and the segment the product blames can flip. Two extra columns in the
// GROUP BY (min and max of timestamp) buy exactness at both edges for one query.
func spread(b Bucket) []time.Time {
	first, last := b.First.UTC(), b.Last.UTC()
	if first.IsZero() {
		first = b.Day.UTC()
	}
	if last.Before(first) {
		last = first
	}
	out := make([]time.Time, b.Count)
	if b.Count == 1 {
		out[0] = first
		return out
	}
	span := last.Sub(first)
	for i := 0; i < b.Count; i++ {
		out[i] = first.Add(time.Duration(int64(span) * int64(i) / int64(b.Count-1)))
	}
	out[b.Count-1] = last // exact, not interpolated-to-approximately
	return out
}

// column turns one dimension's value counts into a per-row assignment.
//
// Rows past the end of the counted values get "", which is precisely what a raw row missing that
// property produces: cause.propValue returns "" and worstSegment skips the row. A dimension that
// is present on only half a day's events must participate in the arithmetic for exactly that half.
func column(vals map[string]int, count int) ([]string, bool) {
	if sum(vals) > count {
		return nil, true
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic: map order must never decide which row carries which value
	col := make([]string, count)
	i := 0
	for _, k := range keys {
		for n := 0; n < vals[k] && i < count; n++ {
			col[i] = k
			i++
		}
	}
	return col, false
}

// overlayRevenue writes the money property onto rows of one event so that
// money.revenuePropFor and money.revenuePerPerson compute the vendor's figures exactly.
//
// Four counters have to come out right, and each is written by a different group of rows:
//
//	Seen/Numeric — the property-quality tally revenuePropFor gates on (four in five values numeric).
//	Rows         — the n that must reach 20 before a mean may be stated.
//	People       — the denominator; the mean is revenue per PERSON, not per event.
//	Total        — the numerator.
//
// The value rows carry the entire Total on the FIRST of them and 0 on the rest. That looks like a
// trick and is the opposite: money.go counts a $0 row into both n and the person set and only
// skips it in the sum, so an even split and a concentrated one produce identical Rows, People and
// Total — except that the concentrated one sums to Total EXACTLY in float64, where summing
// Total/Rows a hundred times drifts, and a drift of one cent changes a headline dollar figure.
// These rows are never stored and no individual amount is ever read; only the sum and the count.
func overlayRevenue(out []event.Event, idx []int, name string, s Snapshot) (Degradation, bool) {
	r := s.Revenue[name]
	if r.Prop == "" {
		return Degradation{}, true
	}
	if r.Seen > len(idx) {
		return Degradation{
			Code: "revenue_unplaceable",
			What: fmt.Sprintf("%s is sized in people affected, not money", name),
			Why: fmt.Sprintf("the vendor reported the %s property on %d rows but only %d events of that name in the window — the two reads disagree.",
				r.Prop, r.Seen, len(idx)),
			Fix: "Re-sync; if it persists the vendor's aggregate is inconsistent and the people figure is the honest one.",
		}, false
	}

	// The money window is the investigator's reporting window, and it is narrower than the sync
	// window. A mean computed over 90 days and multiplied by a 30-day people count is a different
	// number wearing the same units, so the value rows go inside the reporting window or nowhere.
	cutoff := s.To.UTC().AddDate(0, 0, -s.InvestigateDays)
	var inWindow, elsewhere []int
	for _, i := range idx {
		if out[i].Timestamp.Before(cutoff) {
			elsewhere = append(elsewhere, i)
		} else {
			inWindow = append(inWindow, i)
		}
	}
	if r.Rows > len(inWindow) {
		return Degradation{
			Code: "revenue_window",
			What: fmt.Sprintf("%s is sized in people affected, not money", name),
			Why: fmt.Sprintf("the vendor reported %d priced rows in the last %d days but only %d events of that name landed in that window.",
				r.Rows, s.InvestigateDays, len(inWindow)),
			Fix: "Re-sync; the people figure is exact either way.",
		}, false
	}

	people := r.People
	if people <= 0 {
		people = 1
	}
	for n := 0; n < r.Rows; n++ {
		e := &out[inWindow[n]]
		if e.Properties == nil {
			e.Properties = map[string]any{}
		}
		v := 0.0
		if n == 0 {
			v = r.Total
		}
		e.Properties[r.Prop] = v
		// Round-robin over a pool of exactly People ids. Distinct-person count over these rows is
		// then min(People, Rows) = People, because People can never exceed Rows.
		e.DistinctID = fmt.Sprintf("%sperson:%s:%d", synthPrefix, name, n%people)
	}

	// Filler rows carry the property so the quality tally matches, and carry a value money.go
	// skips: negative is a refund it counts in neither n nor the sum, non-numeric fails Numeric().
	// They are inert wherever they land, so they take whatever rows are left, oldest first.
	spare := append(append([]int{}, elsewhere...), inWindow[r.Rows:]...)
	fill := func(n int, v any) {
		for k := 0; k < n && len(spare) > 0; k++ {
			e := &out[spare[0]]
			spare = spare[1:]
			if e.Properties == nil {
				e.Properties = map[string]any{}
			}
			e.Properties[r.Prop] = v
		}
	}
	fill(r.Numeric-r.Rows, -1.0)
	fill(r.Seen-r.Numeric, "not-a-number")
	return Degradation{}, true
}

func totalCount(bs []Bucket) int {
	n := 0
	for _, b := range bs {
		n += b.Count
	}
	return n
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
