package connector

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// The reference aggregation.
//
// Every function here computes, over raw events in Go, EXACTLY what the vendor query is written
// to compute in SQL. That duplication is the point: the SQL runs against a database nobody here
// can test, and the only way to know what it should return is to have a second implementation
// that can be tested against real raw events — which is what equivalence_test.go does. When a
// vendor's shape or a gate in the investigator changes, the reference is where it changes first
// and the SQL is checked against it.
//
// It is also useful on its own: Reference() turns a local event log into the same Snapshot a
// vendor would produce, so the whole connector path — synthesis, guarding, degradation — can be
// exercised end to end with no network and no credential.

// scoped applies the same default filter the investigator applies before anything else:
// query.Apply(evs, nil), which hides development/preview/staging/test/ci traffic.
//
// It matters here more than anywhere. Run() filters the events it is handed — so on the raw path
// dev traffic never reaches the detector. If the VENDOR query does not filter the same way, the
// aggregate counts include a population the raw counts exclude, and every number differs by
// however much dev traffic that customer sends. The WHERE clause in posthog.go is this function,
// in SQL.
func scoped(raw []event.Event) []event.Event { return query.Apply(raw, nil) }

// DailyCounts is PHASE A: one row per (day, event) with the count and the real first/last
// timestamps inside it. This is the whole of what change detection needs.
func DailyCounts(raw []event.Event, from, to time.Time) []Bucket {
	type key struct {
		day   time.Time
		event string
	}
	acc := map[key]*Bucket{}
	for _, e := range scoped(raw) {
		t := e.Timestamp.UTC()
		if t.Before(from) || !t.Before(to) {
			continue
		}
		k := key{t.Truncate(24 * time.Hour), e.Name}
		b := acc[k]
		if b == nil {
			b = &Bucket{Day: k.day, Event: e.Name, First: t, Last: t}
			acc[k] = b
		}
		b.Count++
		if t.Before(b.First) {
			b.First = t
		}
		if t.After(b.Last) {
			b.Last = t
		}
	}
	out := make([]Bucket, 0, len(acc))
	for _, b := range acc {
		out = append(out, *b)
	}
	sortBuckets(out)
	return out
}

// SegmentCounts is PHASE B: per-dimension marginals for ONE event over a narrow window.
//
// Marginals, never a cross-product. worstSegment walks dimensions one at a time and asks each the
// same question independently, so the joint distribution is data nobody reads — and it is the
// half that multiplies row counts by every dimension's cardinality at once.
//
// Returns the counts keyed by day, and the dimensions that had to be dropped for having too many
// values to summarise honestly.
func SegmentCounts(raw []event.Event, name string, from, to time.Time, dims []string) (map[time.Time]map[string]map[string]int, []string) {
	perDay := map[time.Time]map[string]map[string]int{}
	distinct := map[string]map[string]bool{}
	for _, e := range scoped(raw) {
		if e.Name != name {
			continue
		}
		t := e.Timestamp.UTC()
		if t.Before(from) || !t.Before(to) {
			continue
		}
		day := t.Truncate(24 * time.Hour)
		for _, d := range dims {
			v, ok := e.Properties[d].(string)
			if !ok || v == "" {
				// A missing dimension is not a value. propValue() skips an empty string, so the
				// row simply does not participate in that dimension's arithmetic — and the
				// synthesised row must not participate either, which is why nothing is written.
				continue
			}
			if distinct[d] == nil {
				distinct[d] = map[string]bool{}
			}
			distinct[d][v] = true
			if perDay[day] == nil {
				perDay[day] = map[string]map[string]int{}
			}
			if perDay[day][d] == nil {
				perDay[day][d] = map[string]int{}
			}
			perDay[day][d][v]++
		}
	}
	var dropped []string
	for _, d := range dims {
		if len(distinct[d]) > maxSegmentValues {
			dropped = append(dropped, d)
			for _, byDim := range perDay {
				delete(byDim, d)
			}
		}
	}
	return perDay, dropped
}

// revenueProps mirrors investigate/money.go's list AND its priority order. The order is not
// cosmetic: a log carrying both `amount` and `revenue` must resolve to the same property on both
// paths or the two means are computed from different columns.
var revenueProps = []string{"amount", "revenue", "value", "price", "total"}

// RevenueFor computes the money counters for one event, and picks the property the same way
// money.revenuePropFor does.
//
// TWO WINDOWS, and they are deliberately different because the raw path uses two:
//
//   - the property-quality tally (Seen/Numeric) is what revenuePropFor scans. On the raw path that
//     is UNWINDOWED — every event of that name the instance holds. On this path "everything held"
//     is the snapshot, so the tally is bounded by [heldFrom, heldTo): synthesis can only place
//     these rows on events that exist in the snapshot, and a tally counting rows the synthesised
//     set cannot contain is a tally describing a population the sum does not.
//   - the sums (Rows, People, Total) are taken over the trailing reporting window, which is what
//     revenuePerPerson scans, and which is narrower.
//
// The equivalence test found this: the corpus's last day ran past the snapshot's exclusive `to`,
// the tally counted five rows the buckets did not, and Synthesize refused the money figure because
// the two reads disagreed. It was right to refuse. This is the read that stops disagreeing.
func RevenueFor(raw []event.Event, name string, heldFrom, heldTo, windowFrom, windowTo time.Time) Revenue {
	evs := scoped(raw)

	type tally struct{ seen, numeric int }
	quality := map[string]*tally{}
	for _, e := range evs {
		if e.Name != name || len(e.Properties) == 0 {
			continue
		}
		if t := e.Timestamp.UTC(); t.Before(heldFrom) || !t.Before(heldTo) {
			continue
		}
		for _, p := range revenueProps {
			v, present := e.Properties[p]
			if !present {
				continue
			}
			t := quality[p]
			if t == nil {
				t = &tally{}
				quality[p] = t
			}
			t.seen++
			if _, ok := event.Numeric(v); ok {
				t.numeric++
			}
		}
	}
	prop, bestN := "", 0
	for _, p := range revenueProps {
		t := quality[p]
		if t == nil || t.numeric == 0 {
			continue
		}
		if float64(t.numeric)/float64(t.seen) < minNumericRatio {
			continue
		}
		if t.numeric > bestN {
			prop, bestN = p, t.numeric
		}
	}
	if prop == "" {
		return Revenue{}
	}

	r := Revenue{Prop: prop, Seen: quality[prop].seen, Numeric: quality[prop].numeric}
	people := map[string]bool{}
	for _, e := range evs {
		if e.Name != name || e.Timestamp.UTC().Before(windowFrom) || !e.Timestamp.UTC().Before(windowTo) {
			continue
		}
		v, ok := event.Numeric(e.Properties[prop])
		if !ok || v < 0 {
			// Negative is a refund. The raw path counts it in neither the total nor the
			// denominator, so neither does this.
			continue
		}
		r.Rows++
		people[e.DistinctID] = true
		if v > 0 {
			r.Total += v
		}
	}
	r.People = len(people)
	return r
}

// ReferenceOpts configures a local, no-network Snapshot.
type ReferenceOpts struct {
	Now             time.Time
	Days            int // Phase A window. Default 90.
	InvestigateDays int // the reporting window money is computed over. Default 30.
	Dims            []string
	// Events limits Phase B to these event names, exactly as the live flow does: segments are
	// fetched only for events that produced a finding. Nil means every event, which is what a
	// test wants and what a poller must never do.
	Events []string
}

func (o ReferenceOpts) withDefaults() ReferenceOpts {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.Days <= 0 {
		o.Days = 90
	}
	if o.InvestigateDays <= 0 {
		o.InvestigateDays = 30
	}
	if o.Dims == nil {
		o.Dims = CauseDims
	}
	return o
}

// Reference builds the Snapshot a vendor WOULD return, from raw events held locally.
func Reference(raw []event.Event, opts ReferenceOpts) Snapshot {
	o := opts.withDefaults()
	to := o.Now.UTC()
	from := to.AddDate(0, 0, -o.Days)

	s := Snapshot{
		Vendor: "reference", From: from, To: to,
		Buckets:         DailyCounts(raw, from, to),
		Revenue:         map[string]Revenue{},
		InvestigateDays: o.InvestigateDays,
		Dims:            map[string][]string{},
		DroppedDims:     map[string][]string{},
		FetchedAt:       o.Now,
	}

	names := o.Events
	if names == nil {
		seen := map[string]bool{}
		for _, b := range s.Buckets {
			if !seen[b.Event] {
				seen[b.Event] = true
				names = append(names, b.Event)
			}
		}
		sort.Strings(names)
	}
	for _, n := range names {
		segs, dropped := SegmentCounts(raw, n, from, to, o.Dims)
		AttachSegments(&s, n, segs)
		var kept []string
		for _, d := range o.Dims {
			if !contains(dropped, d) {
				kept = append(kept, d)
			}
		}
		s.Dims[n] = kept
		if len(dropped) > 0 {
			s.DroppedDims[n] = dropped
		}
		if r := RevenueFor(raw, n, from, to, to.AddDate(0, 0, -o.InvestigateDays), to); r.Prop != "" {
			s.Revenue[n] = r
		}
	}
	return s
}

// AttachSegments folds a Phase B result into the Phase A buckets it belongs to.
//
// Phase B for an event that has no Phase A bucket on some day is dropped rather than creating a
// bucket: a segment count without a total is a fragment, and inventing the total to hold it is
// exactly the kind of gap-filling that ends with a synthesised number nobody can trace.
func AttachSegments(s *Snapshot, name string, perDay map[time.Time]map[string]map[string]int) {
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Event != name {
			continue
		}
		got := perDay[b.Day.UTC().Truncate(24*time.Hour)]
		if len(got) == 0 {
			continue
		}
		if b.Segments == nil {
			b.Segments = map[string]map[string]int{}
		}
		for dim, vals := range got {
			cp := make(map[string]int, len(vals))
			for v, n := range vals {
				cp[v] = n
			}
			b.Segments[dim] = cp
		}
	}
}

// Merge folds a fresh read into a held one, LAST WRITE WINS PER BUCKET KEY.
//
// This is the whole of dedupe, and it is why the sync can re-read the last few days on every run
// without doubling anything. A day is a key, not an append: yesterday fetched again replaces
// yesterday, so late-arriving events (which every vendor has) simply correct the number instead
// of adding to it. Appending would have made a sync that runs hourly report 24× traffic, and it
// would have looked like growth.
func Merge(held, fresh []Bucket) []Bucket {
	idx := map[string]int{}
	out := make([]Bucket, 0, len(held)+len(fresh))
	for _, b := range held {
		idx[b.Key()] = len(out)
		out = append(out, b)
	}
	for _, b := range fresh {
		if i, ok := idx[b.Key()]; ok {
			out[i] = b
			continue
		}
		idx[b.Key()] = len(out)
		out = append(out, b)
	}
	sortBuckets(out)
	return out
}

func sortBuckets(b []Bucket) {
	sort.Slice(b, func(i, j int) bool {
		if !b[i].Day.Equal(b[j].Day) {
			return b[i].Day.Before(b[j].Day)
		}
		return b[i].Event < b[j].Event
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
