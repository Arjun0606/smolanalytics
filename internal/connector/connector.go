// Package connector reads the analytics tool someone ALREADY has, and does it without pulling
// their raw event log.
//
// The founder's line is "we rely on the users existing analytics" — PostHog, GA4, Amplitude,
// Mixpanel and Plausible stay exactly where they are and we become the layer that reads them and
// acts. That sentence only becomes true if two things hold, and until this package existed
// neither did:
//
//  1. IT HAS TO KEEP READING. internal/importer/remote.go is a one-shot, human-triggered,
//     key-in-hand backfill. There is no ticker, no watermark, no second run. A migration tool
//     wearing the words of an integration.
//  2. IT HAS TO BE ALLOWED. That backfill pages /api/projects/{id}/events/, which PostHog's own
//     docs now describe as not a supported export mechanism, disable offset pagination on past
//     50,000 rows, and explicitly tell third-party connectors to use batch exports instead. An
//     integration built on an endpoint the vendor has written a sentence to kill is not an
//     integration, it is a countdown.
//
// So this package asks a different question of the vendor. Not "give me your events" — which is
// an export, which is the thing they refuse — but "give me the counts", which is an aggregation,
// which is the thing their query API is FOR.
//
// # Why an aggregate is enough
//
// Derived by reading internal/investigate, not assumed:
//
//   - whatchanged.Series buckets an event into DAILY COUNTS. The change detector never looks at a
//     single raw row: it wants (day, count).
//   - Cost.People is (before − after) per day × 30 — arithmetic over those same daily counts.
//   - cause.worstSegment compares each dimension's value counts either side of the change day.
//     It walks dimensions INDEPENDENTLY, one at a time, and never joins two. Independent
//     marginals are exactly what a GROUP BY returns.
//   - money.revenuePerPerson wants a sum of a numeric property and a count of distinct people.
//     Both are aggregates.
//   - Deploy markers and flag state come from OUR stores and never from the vendor at all.
//
// A day of a customer's events is a few hundred thousand rows. The facts the investigator reads
// out of that day are a few dozen numbers. Moving the rows to compute the numbers here, when the
// vendor's own database will compute them there for one query, is the whole mistake.
//
// # Two phases, because attribution is rare and detection is constant
//
// PHASE A is one query per sync: daily counts per event over the window. A few thousand rows for
// 90 days. It runs on the ticker and it is all detection needs.
//
// PHASE B runs only for events that actually produced a finding: the segment marginals for that
// event across the ±7 days worstSegment examines, plus the revenue figures for it. On a quiet
// product that is zero queries. On a loud one it is a handful.
//
// Phase B is not an optimisation, it is what makes high-cardinality dimensions possible at all.
// Grouping 90 days × every event × every URL is a row explosion that no query API will serve;
// grouping 15 days × one event × every URL is a few hundred rows.
//
// # Nothing synthesised is ever stored
//
// The synthesised events exist inside one investigation and are thrown away. They are NOT written
// to the event store, and this is the single most important boundary in the package.
//
// A synthesised event carries a person id that is unique to itself (see synth.go). That is honest
// for counting and a lie for identity — and funnels, retention, paths, sessions and stickiness are
// all identity. If these rows were persisted, every one of those reports would start answering,
// confidently, from a population that does not exist. Keeping them ephemeral means the only code
// that can ever see them is the code in this package that knows what they are.
package connector

import (
	"fmt"
	"strings"
	"time"
)

// Bucket is ONE ROW of the aggregate contract: everything the investigator can learn about one
// event on one day.
//
// This is the type the vendor query has to fill, and the only type Synthesize reads. Every vendor
// added later must land here — a second shape per vendor is a second definition of "what counts
// as an event", and the two would disagree exactly when someone compared them.
type Bucket struct {
	// Day is midnight UTC. Day boundaries are the investigator's unit: Series buckets by
	// UTC date and worstSegment splits on a date, so a day is lossless and an hour is a cost.
	Day   time.Time
	Event string
	// Count is how many times the event fired that day, after the vendor-side scope filter.
	Count int
	// First and Last are the real min/max timestamps inside the bucket.
	//
	// They look decorative and are not. cause.windowEitherSide measures the distance from the
	// change day to the FIRST and LAST event of that metric in whole days, and picks the shorter
	// side. Pin every synthesised event to midnight and that distance shifts by up to a day, the
	// comparison window either side of the change moves with it, and the segment answer can flip.
	// Two extra columns in the GROUP BY buy exactness at the edges.
	First time.Time
	Last  time.Time
	// Segments holds per-dimension MARGINALS: dim -> value -> count. Never a cross-product.
	// worstSegment reads one dimension at a time, so marginals are sufficient, and the joint
	// distribution the cross-product would carry is the part that explodes.
	Segments map[string]map[string]int
}

// Key is the natural identity of a bucket. Two fetches of the same day must collapse, not stack —
// re-reading yesterday to catch late-arriving events is a NORMAL sync, not a repair.
func (b Bucket) Key() string { return b.Day.UTC().Format(time.DateOnly) + "\x00" + b.Event }

// Revenue is what the vendor can tell us about money on one event, over one window.
//
// The field list is not a guess about what money means: it is the exact set of counters
// investigate/money.go computes while walking raw rows, so the mean computed here and the mean
// computed there are the same arithmetic on the same population.
//
//   - Total sums only values that are numeric AND positive.
//   - People counts distinct persons on rows that are numeric and NOT negative — a $0 trial
//     conversion is a real customer and belongs in the denominator; a negative is a refund the
//     raw path skips entirely.
//   - Rows is that same numeric, non-negative row count, which is the n the ≥20 gate reads.
//   - Seen and Numeric are the property-quality tally: money.revenuePropFor refuses a property
//     unless at least four in five of the values present are numeric.
type Revenue struct {
	Prop    string
	Total   float64
	People  int
	Rows    int
	Seen    int
	Numeric int
}

// minRevenueEvents and minNumericRatio mirror investigate/money.go's two gates.
//
// Mirrored rather than imported because they are unexported there, and a mirror that drifts is
// caught the only way that counts: the equivalence test runs the real investigator over raw
// events and over aggregates of those same events and compares the money it states. If either
// constant moves and this one does not, that test fails on the finding, not on the constant.
const (
	minRevenueEvents = 20
	minNumericRatio  = 0.8
)

// Usable reports whether this is a mean the raw path would also have stated.
func (r Revenue) Usable() bool {
	if r.Prop == "" || r.Numeric == 0 || r.People == 0 || r.Total <= 0 {
		return false
	}
	if float64(r.Numeric)/float64(r.Seen) < minNumericRatio {
		return false
	}
	return r.Rows >= minRevenueEvents
}

// Mean is revenue per PERSON — the figure money.go multiplies the people-affected count by.
func (r Revenue) Mean() float64 {
	if r.People == 0 {
		return 0
	}
	return r.Total / float64(r.People)
}

// CauseDims are the dimensions Phase B asks for, and they are deliberately the same six
// investigate/cause.go's causeProps lists, under the $-prefixed names every vendor writes them
// with. cause.vendorAliases already maps $browser to browser, so emitting the vendor's own name
// is not a translation layer — it is the absence of one, and it means data pulled through this
// connector attributes identically to data POSTed to /v1/events in a vendor's shape.
var CauseDims = []string{"$browser", "$os", "$device_type", "$pathname", "$geoip_country_code", "$referring_domain"}

// LocalDim is the investigator's name for a vendor dimension, used only to say out loud which
// dimensions a finding was and was not checked against.
var LocalDim = map[string]string{
	"$browser":            "browser",
	"$os":                 "os",
	"$device_type":        "device",
	"$pathname":           "path",
	"$geoip_country_code": "country",
	"$referring_domain":   "referrer",
}

// maxSegmentValues caps how many distinct values a dimension may have inside one Phase B window
// before we refuse to use it.
//
// A truncated marginal is WORSE than a missing one. worstSegment divides a value's loss by the
// total loss across the dimension; drop the tail and the denominator shrinks, every surviving
// share inflates, and a dimension that is genuinely spread evenly reports a concentration that
// crosses the 55% bar. That is a confident, specific, false sentence — the exact failure this
// package exists to avoid — so a dimension that overflows is dropped whole and SAID.
const maxSegmentValues = 500

// Snapshot is one read of a vendor: the aggregate facts, plus the honest edges of them.
type Snapshot struct {
	Vendor  string
	Project string
	// From and To bound the daily counts. To is exclusive.
	From, To time.Time
	Buckets  []Bucket
	// Revenue is per event, computed over exactly InvestigateDays trailing days — the same window
	// money.revenuePerPerson scans. A mean over a different window is a different number, and
	// carrying the window with the figure is what lets Investigate refuse when they disagree.
	Revenue         map[string]Revenue
	InvestigateDays int
	// Dims is the vendor-side dimension list Phase B actually grouped by, per event. An event
	// absent from this map has had no Phase B run: detection only.
	Dims map[string][]string
	// DroppedDims records dimensions asked for and refused (too many values), per event. It is
	// the input to a degradation, never silently discarded.
	DroppedDims map[string][]string
	FetchedAt   time.Time
}

// Window returns the Phase B window around a change day: the ±7 days worstSegment can examine.
// Wider costs rows for nothing — cause.windowEitherSide caps its span at 7.
func Window(day time.Time) (from, to time.Time) {
	d := day.UTC().Truncate(24 * time.Hour)
	return d.AddDate(0, 0, -8), d.AddDate(0, 0, 8)
}

// Degradation is a claim this connector CANNOT support, stated in the product's own voice.
//
// The rule the whole package is judged by: a finding computed from aggregates is either identical
// to the finding the raw events would have produced, or it is visibly labelled. A finding is a
// claim about someone's business; a bucketed answer that quietly differs is not an approximation,
// it is a lie with arithmetic in front of it.
type Degradation struct {
	// Code is stable and machine-readable, so a renderer can decide placement without matching
	// prose.
	Code string `json:"code"`
	// What is the capability lost, in the words a user would use for it.
	What string `json:"what"`
	// Why is the mechanical reason, not an apology.
	Why string `json:"why"`
	// Fix is what would restore it, or "" when nothing would.
	Fix string `json:"fix,omitempty"`
}

func (d Degradation) String() string {
	if d.Fix == "" {
		return d.What + " — " + d.Why
	}
	return d.What + " — " + d.Why + " " + d.Fix
}

// Standing degradations: true of every aggregate read from every vendor, always, and therefore
// declared as constants rather than discovered per run. A capability that is never available must
// not depend on a code path noticing it is missing.
var (
	// DegradeSequence is the big one. A daily count per event cannot say what one person did
	// next, and no grouping choice recovers it.
	DegradeSequence = Degradation{
		Code: "sequence",
		What: "Funnels, retention, session paths and stickiness cannot be computed from this source",
		Why:  "they are questions about one person's order of events, and a daily count per event has no person in it — only how many.",
		Fix:  "Send events to this instance directly (SDK or /v1/events) and these work on the events you send, alongside the counts read from your existing tool.",
	}
	// DegradePeople follows from synth.go's one-person-per-row rule.
	DegradePeople = Degradation{
		Code: "people_rates",
		What: "Per-person rates over multi-day windows are not computed from this source",
		Why:  "distinct-person counts across days cannot be recovered from daily aggregates — the same person on two days is indistinguishable from two people.",
		Fix:  "Event counts, the size of every change, and revenue per person over the reporting window are read directly from your tool and are exact.",
	}
)

// DegradeDims names the dimensions a specific finding could NOT be checked against.
func DegradeDims(event string, dropped []string) Degradation {
	names := make([]string, 0, len(dropped))
	for _, d := range dropped {
		if n := LocalDim[d]; n != "" {
			names = append(names, n)
		} else {
			names = append(names, strings.TrimPrefix(d, "$"))
		}
	}
	return Degradation{
		Code: "segment_dims",
		What: fmt.Sprintf("%s was not broken down by %s", event, strings.Join(names, ", ")),
		Why: fmt.Sprintf("more than %d distinct values in the window, and a partial breakdown inflates every share it does return — so it was dropped rather than reported.",
			maxSegmentValues),
		Fix: "Narrow the dimension at source (group URLs into routes) and it comes back.",
	}
}
