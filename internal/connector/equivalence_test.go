package connector

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// THE TEST THE PACKAGE EXISTS FOR.
//
// Run the real investigator over raw events. Aggregate those same events the way the vendor query
// aggregates them. Synthesise events back out of the aggregate. Run the real investigator again.
// Every finding must be identical — same kind, same headline, same day, same cause, same size,
// same basis, same rendered figure.
//
// Not "close". Identical. A finding is a claim about someone's business, and a bucketed answer
// that quietly differs from the truth is not an approximation, it is a lie with arithmetic in
// front of it. Anything the aggregate genuinely cannot support has to disappear and say so — which
// is what TestMovementsCannotSurviveAggregation pins down — rather than come out slightly wrong.

// testNow sits after the last event of the last day, on purpose.
//
// investigate.revenuePerPerson has no upper bound — it counts every event at or after its cutoff,
// including ones stamped in the future. A vendor query has an upper bound by construction: you
// cannot aggregate rows the vendor does not have yet. With `now` mid-afternoon the two windows
// genuinely differ by that afternoon's sales, and the money figures came out 0.4% apart. That is a
// real edge (a clock-skewed client stamping tomorrow), it is not what this test is about, and
// hiding it inside the corpus would be the dishonest fix — see TestFutureStampedRowsAreNotVisible.
var testNow = time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)

const testDays = 45

// corpus builds a product with a real regression in it, in a vendor's own property shape.
//
//	checkout   40/day for 25 days, then 12/day — and the loss is concentrated in Safari
//	signup     30/day flat, no change
//	refund      2/day — under the detector's floor, so it must produce a BelowFloor note
//	           plus development traffic that the production scope has to hide, and this tool's
//	           own sampler events, which must not become a finding about the customer.
func corpus() []event.Event {
	var evs []event.Event
	start := testNow.AddDate(0, 0, -(testDays - 1)).Truncate(24 * time.Hour)

	add := func(day int, name string, n int, props func(i int) map[string]any) {
		d := start.AddDate(0, 0, day)
		for i := 0; i < n; i++ {
			// Spread across the working day, never at midnight. cause.windowEitherSide measures
			// whole days from the first and last row, so a corpus pinned to midnight would let a
			// broken First/Last pass.
			ts := d.Add(time.Duration(7*60+((i*37)%(11*60))) * time.Minute)
			e := event.Event{
				Name:       name,
				DistinctID: fmt.Sprintf("u%d", (day*17+i)%97),
				Timestamp:  ts,
				ID:         fmt.Sprintf("%s-%d-%d", name, day, i),
			}
			if props != nil {
				e.Properties = props(i)
			}
			evs = append(evs, e)
		}
	}

	// browsers returns a value picked so that Safari carries most of the drop.
	checkoutProps := func(safari, chrome int) func(i int) map[string]any {
		return func(i int) map[string]any {
			b := "Firefox"
			switch {
			case i < safari:
				b = "Safari"
			case i < safari+chrome:
				b = "Chrome"
			}
			return map[string]any{
				"$browser":            b,
				"$os":                 []string{"Mac OS X", "Windows", "iOS"}[i%3],
				"$device_type":        []string{"Desktop", "Mobile"}[i%2],
				"$pathname":           []string{"/checkout", "/cart"}[i%2],
				"$geoip_country_code": []string{"US", "GB", "DE"}[i%3],
				"$referring_domain":   []string{"google.com", "$direct"}[i%2],
				"amount":              float64(20 + i%10),
			}
		}
	}

	for d := 0; d < testDays; d++ {
		if d < 25 {
			add(d, "checkout", 40, checkoutProps(20, 15))
		} else {
			add(d, "checkout", 12, checkoutProps(2, 8))
		}
		add(d, "signup", 30, func(i int) map[string]any {
			return map[string]any{"$browser": []string{"Chrome", "Safari"}[i%2]}
		})
		add(d, "refund", 2, nil)
		// Development traffic, at a volume that would visibly move every number if the vendor-side
		// scope filter did not hide exactly the population query.Apply hides.
		add(d, "checkout", 25, func(i int) map[string]any {
			return map[string]any{"env": "development", "$browser": "Chrome", "amount": 999.0}
		})
		add(d, "$geo_check", 3, nil) // this tool's own robot
	}
	return evs
}

func investigateOpts() investigate.Opts {
	return investigate.Opts{Days: testDays, Now: testNow, MinPeople: 20}
}

func referenceOpts() ReferenceOpts {
	return ReferenceOpts{Now: testNow, Days: testDays, InvestigateDays: testDays, Dims: CauseDims}
}

// roundTrip is raw -> aggregate -> synthesised -> findings.
func roundTrip(t *testing.T, raw []event.Event, opts ReferenceOpts) investigate.Investigation {
	t.Helper()
	snap := Reference(raw, opts)
	evs, degs := Synthesize(snap)
	for _, d := range degs {
		t.Logf("degradation: %s", d)
	}
	return investigate.Run(evs, investigateOpts())
}

func TestFindingsFromAggregatesEqualFindingsFromRaw(t *testing.T) {
	raw := corpus()
	fromRaw := investigate.Run(raw, investigateOpts())
	fromAgg := roundTrip(t, raw, referenceOpts())

	if len(fromRaw.Findings) == 0 {
		t.Fatal("the corpus produced no findings at all — the test proves nothing")
	}
	compareFindings(t, fromRaw.Findings, fromAgg.Findings)

	if !reflect.DeepEqual(fromRaw.Scanned, fromAgg.Scanned) {
		t.Errorf("scanned events differ:\n raw %v\n agg %v", fromRaw.Scanned, fromAgg.Scanned)
	}
	if !reflect.DeepEqual(fromRaw.BelowFloor, fromAgg.BelowFloor) {
		t.Errorf("below-floor notes differ:\n raw %+v\n agg %+v", fromRaw.BelowFloor, fromAgg.BelowFloor)
	}
	if fromRaw.Quiet != fromAgg.Quiet {
		t.Errorf("quiet differs: raw %v agg %v", fromRaw.Quiet, fromAgg.Quiet)
	}
}

// The corpus is only worth anything if it exercises the parts that are hard. Assert what the
// findings actually SAY, so a future change that makes both paths equally wrong still fails.
func TestCorpusExercisesCauseAndMoney(t *testing.T) {
	fs := investigate.Run(corpus(), investigateOpts()).Findings
	var checkout *investigate.Finding
	for i := range fs {
		if fs[i].Event == "checkout" {
			checkout = &fs[i]
		}
	}
	if checkout == nil {
		t.Fatal("no checkout finding")
	}
	if checkout.Kind != investigate.KindRegression {
		t.Errorf("checkout should be a regression, got %q", checkout.Kind)
	}
	if !containsStr(checkout.Cause, "browser=Safari") {
		t.Errorf("the corpus must produce a segment cause; got %q", checkout.Cause)
	}
	if checkout.Cost.UsdPerMonth <= 0 {
		t.Errorf("the corpus must produce a money-sized finding; got %+v", checkout.Cost)
	}
}

func compareFindings(t *testing.T, raw, agg []investigate.Finding) {
	t.Helper()
	if len(raw) != len(agg) {
		t.Fatalf("finding count differs: raw %d, agg %d\n raw %s\n agg %s",
			len(raw), len(agg), render(raw), render(agg))
	}
	for i := range raw {
		a, b := raw[i], agg[i]
		if !reflect.DeepEqual(a, b) {
			t.Errorf("finding %d differs:\n raw %+v\n agg %+v", i, a, b)
		}
	}
}

func render(fs []investigate.Finding) string {
	out := ""
	for _, f := range fs {
		out += fmt.Sprintf("\n  [%s] %s | %s | %s", f.Kind, f.Headline, f.Cause, f.Cost.SizeText)
	}
	return out
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// ---------------------------------------------------------------------------
// GUARDS. Each one reintroduces a specific bug and asserts the equivalence test
// would have caught it. A guard nobody has watched fail is a guard nobody can trust.
// ---------------------------------------------------------------------------

// Pinning every synthesised row to midnight is the obvious shortcut: the bucket is a day, so why
// carry min and max.
//
// Because cause.windowEitherSide measures the comparison window in whole days from the metric's
// FIRST and LAST row and takes the shorter side, and truncating the first row to midnight adds up
// to a day to that side. It only bites when the shorter side is under the 7-day cap — which is
// exactly the case that matters, a change near the start of a metric's history — so the main
// corpus (45 days, change in the middle, both sides capped at 7) cannot see it at all. That is
// what this guard failing the first time proved, and why the fixture below exists.
//
// edgeCorpus: history starts at 23:00 on day 0, the change lands on day 3.
//
//	real     day3 − day0T23:00 = 2d1h  -> span 2 -> window [day1, day5)
//	midnight day3 − day0T00:00 = 3d    -> span 3 -> window [day0, day6)
//
// Day 0 is all Chrome, days 1–2 are Safari-heavy. So the honest window blames Safari and the
// midnight-pinned one blames Chrome: same event, same day, same drop, different culprit.
func edgeCorpus() []event.Event {
	var evs []event.Event
	start := edgeNow.AddDate(0, 0, -11).Truncate(24 * time.Hour)
	add := func(day, n int, browser func(i int) string, hour, spanMin int) {
		d := start.AddDate(0, 0, day)
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{
				Name:       "signup",
				DistinctID: fmt.Sprintf("u%d", (day*13+i)%40),
				Timestamp:  d.Add(time.Duration(hour*60+(i*7)%spanMin) * time.Minute),
				ID:         fmt.Sprintf("s-%d-%d", day, i),
				Properties: map[string]any{"$browser": browser(i)},
			})
		}
	}
	heavy := func(i int) string {
		switch {
		case i < 20:
			return "Safari"
		case i < 35:
			return "Chrome"
		}
		return "Firefox"
	}
	small := func(i int) string {
		switch {
		case i < 2:
			return "Safari"
		case i < 8:
			return "Chrome"
		}
		return "Firefox"
	}
	add(0, 40, func(int) string { return "Chrome" }, 23, 30) // late, and all one browser
	for d := 1; d <= 2; d++ {
		add(d, 40, heavy, 9, 480)
	}
	for d := 3; d <= 11; d++ {
		add(d, 10, small, 9, 480)
	}
	return evs
}

var edgeNow = time.Date(2026, 6, 15, 23, 59, 0, 0, time.UTC)

func edgeOpts() investigate.Opts {
	return investigate.Opts{Days: 12, Now: edgeNow, MinPeople: 20}
}

func TestGuardTimestampsMustNotCollapseToMidnight(t *testing.T) {
	raw := edgeCorpus()
	ref := ReferenceOpts{Now: edgeNow, Days: 12, InvestigateDays: 12, Dims: CauseDims}

	want := investigate.Run(raw, edgeOpts())
	if len(want.Findings) == 0 || want.Findings[0].Cause == "" {
		t.Fatalf("the edge fixture must produce a cause line, got %+v", want.Findings)
	}

	honest := Reference(raw, ref)
	evs, _ := Synthesize(honest)
	if got := investigate.Run(evs, edgeOpts()); !reflect.DeepEqual(want.Findings, got.Findings) {
		t.Fatalf("carrying real First/Last must reproduce the raw finding:\n raw %+v\n agg %+v", want.Findings, got.Findings)
	}

	pinned := Reference(raw, ref)
	for i := range pinned.Buckets {
		pinned.Buckets[i].First = pinned.Buckets[i].Day
		pinned.Buckets[i].Last = pinned.Buckets[i].Day
	}
	evs, _ = Synthesize(pinned)
	broken := investigate.Run(evs, edgeOpts())
	if reflect.DeepEqual(want.Findings, broken.Findings) {
		t.Fatal("collapsing First/Last to midnight changed nothing — the fixture no longer exercises windowEitherSide, so the two columns look free when they are not")
	}
	t.Logf("honest cause:   %s", want.Findings[0].Cause)
	t.Logf("midnight cause: %s", broken.Findings[0].Cause)
}

// The vendor-side WHERE has to hide exactly the population query.Apply hides. If it does not, the
// aggregate counts a development population the raw path excludes and every number is off by
// however much dev traffic that customer sends — with no error anywhere.
func TestGuardProductionScopeMustBeAppliedVendorSide(t *testing.T) {
	raw := corpus()
	// Aggregate WITHOUT the scope filter, which is what forgetting the WHERE clause does.
	unscoped := Snapshot{
		Vendor: "reference", From: testNow.AddDate(0, 0, -testDays), To: testNow,
		Buckets:         unscopedDailyCounts(raw, testNow.AddDate(0, 0, -testDays), testNow),
		Revenue:         map[string]Revenue{},
		InvestigateDays: testDays,
		Dims:            map[string][]string{},
		DroppedDims:     map[string][]string{},
	}
	evs, _ := Synthesize(unscoped)
	got := investigate.Run(evs, investigateOpts())
	want := investigate.Run(raw, investigateOpts())
	if reflect.DeepEqual(want.Findings, got.Findings) {
		t.Fatal("dropping the production scope from the aggregate changed nothing — hogScope() is untested by this corpus")
	}
}

// A high-cardinality dimension must be dropped WHOLE, never truncated to its head.
//
// worstSegment divides the top value's loss by the DIMENSION'S TOTAL loss. Truncation removes rows
// from both, but it removes far more from the denominator — so a dimension that is genuinely spread
// evenly across forty pages reports that one page carries all of it, sails over the 55% bar, and
// the product names a culprit that does not exist.
func TestGuardTruncatingADimensionInventsAConcentration(t *testing.T) {
	// minSegmentShare mirrors investigate/cause.go's concentration bar (unexported there).
	const minSegmentShare = 0.55

	// Forty pages, each losing five: no concentration exists in this data at all.
	full := map[string]int{}
	for i := 0; i < 40; i++ {
		full[fmt.Sprintf("/p/%d", i)] = 5
	}
	if honest := topShare(full); honest >= minSegmentShare {
		t.Fatalf("the even spread must sit under the %.2f bar, got %.2f", minSegmentShare, honest)
	}

	// Truncated to the head, which is what a LIMIT on the breakdown query does.
	head := map[string]int{"/p/0": 5}
	if truncated := topShare(head); truncated < minSegmentShare {
		t.Fatalf("truncation must push the share over the %.2f bar or the guard proves nothing, got %.2f", minSegmentShare, truncated)
	}

	// And the code refuses rather than truncating: an overflowing dimension is dropped from every
	// day AND reported, so no share is ever computed from a partial denominator.
	dims := []string{"$pathname"}
	var raw []event.Event
	day := testNow.AddDate(0, 0, -2).Truncate(24 * time.Hour)
	for i := 0; i < maxSegmentValues+50; i++ {
		raw = append(raw, event.Event{
			Name: "view", DistinctID: fmt.Sprintf("u%d", i), Timestamp: day.Add(time.Hour),
			Properties: map[string]any{"$pathname": fmt.Sprintf("/p/%d", i)},
		})
	}
	segs, dropped := SegmentCounts(raw, "view", day, day.AddDate(0, 0, 1), dims)
	if len(dropped) != 1 || dropped[0] != "$pathname" {
		t.Fatalf("an overflowing dimension must be dropped by name, got %v", dropped)
	}
	for _, byDim := range segs {
		if _, still := byDim["$pathname"]; still {
			t.Fatal("a dropped dimension must not survive in the counts — a partial marginal is worse than none")
		}
	}
	if d := DegradeDims("view", dropped); d.What == "" || d.Why == "" {
		t.Fatal("dropping a dimension must produce a degradation the reader can see")
	}
}

// The sibling guard above proves truncation is dangerous when WE do it. This one covers the case
// where the VENDOR hands us a breakdown that is already inconsistent with its own total.
//
// A vendor reporting 150 breakdown rows for a 100-event day is two reads that disagree, and the
// disagreement is dangerous in one specific direction. column() lays values down in sorted-key
// order into a row slice of exactly Count, so an over-reported dimension does not error — the
// values that sort LAST are silently cut, the survivors keep their full counts, and what reaches
// worstSegment is a truncated marginal. That is the same manufactured concentration, arriving
// through the vendor instead of through a LIMIT, and it flips which value looks like the majority.
//
// So the overflow is refused and said. Written after mutation testing found this was the one
// degradation in the package that no test exercised: deleting the check from synth.go left every
// other test green.
func TestGuardVendorOverReportingADimensionIsRefusedNotTruncated(t *testing.T) {
	day := testNow.AddDate(0, 0, -2).Truncate(24 * time.Hour)
	// 100 events on the day; the vendor's own breakdown claims 150 rows.
	b := Bucket{
		Day: day, Event: "checkout", Count: 100,
		First: day.Add(9 * time.Hour), Last: day.Add(17 * time.Hour),
		Segments: map[string]map[string]int{
			"$browser": {"Safari": 90, "Chrome": 60},
		},
	}

	// The fixture must actually flip the culprit, or this guard proves nothing. Truncation keeps
	// whole values in sorted order and cuts the tail: Chrome sorts first and keeps all 60, Safari
	// loses 50 of its 90 — so the vendor's majority becomes the minority.
	truncated := map[string]int{}
	i := 0
	for _, k := range []string{"Chrome", "Safari"} { // sorted order, which is what column() uses
		for n := 0; n < b.Segments["$browser"][k] && i < b.Count; n++ {
			truncated[k]++
			i++
		}
	}
	if topKey(truncated) == topKey(b.Segments["$browser"]) {
		t.Fatalf("fixture broken: truncation must change which value is largest (vendor says %s, truncated says %s)",
			topKey(b.Segments["$browser"]), topKey(truncated))
	}

	// column() must report the disagreement rather than quietly returning the truncated assignment.
	if _, over := column(b.Segments["$browser"], b.Count); !over {
		t.Fatal("a breakdown outnumbering the day's count must be reported, not truncated into a row assignment")
	}

	snap := Snapshot{
		Vendor: "reference", From: day, To: day.AddDate(0, 0, 1),
		Buckets: []Bucket{b}, Revenue: map[string]Revenue{},
		InvestigateDays: 30, Dims: map[string][]string{}, DroppedDims: map[string][]string{},
	}
	evs, degs := Synthesize(snap)

	var said bool
	for _, d := range degs {
		if d.Code == "segment_overflow" {
			said = true
		}
	}
	if !said {
		t.Error("a breakdown that outnumbers the day's own count must be said out loud, not absorbed silently")
	}

	// And the dimension must be ABSENT, not partial. worstSegment divides a value's loss by the
	// dimension's total loss, so a partial marginal inflates every share it returns.
	counts := map[string]int{}
	for _, e := range evs {
		if v, ok := e.Properties["$browser"].(string); ok {
			counts[v]++
		}
	}
	if len(counts) != 0 {
		t.Errorf("the disagreeing dimension survived into the events as %v — worstSegment would then divide by a denominator the vendor never reported", counts)
	}
	// The count itself is still exact: only the breakdown was refused.
	if len(evs) != b.Count {
		t.Errorf("refusing a breakdown must not change the event count: got %d, want %d", len(evs), b.Count)
	}
}

// topKey is the value with the highest count — the one worstSegment would name.
func topKey(m map[string]int) string {
	best, n := "", -1
	for k, v := range m {
		if v > n {
			best, n = k, v
		}
	}
	return best
}

// topShare is worstSegment's arithmetic: the biggest loser over everything lost.
func topShare(lost map[string]int) float64 {
	top, total := 0, 0
	for _, n := range lost {
		if n > top {
			top = n
		}
		total += n
	}
	if total == 0 {
		return 0
	}
	return float64(top) / float64(total)
}

// Money is per PERSON. Give every synthesised revenue row its own identity — the default for every
// other row, and the obvious thing to do — and the denominator becomes the row count, so the mean
// order value collapses and every dollar figure shrinks by the repeat-purchase rate.
func TestGuardRevenueRowsMustShareThePersonPool(t *testing.T) {
	raw := corpus()
	snap := Reference(raw, referenceOpts())
	if snap.Revenue["checkout"].People >= snap.Revenue["checkout"].Rows {
		t.Fatal("the corpus has no repeat purchasers, so this guard cannot fail — fix the corpus")
	}
	broken := snap
	broken.Revenue = map[string]Revenue{}
	for k, v := range snap.Revenue {
		v.People = v.Rows // one person per row: the bug
		broken.Revenue[k] = v
	}
	evs, _ := Synthesize(broken)
	got := investigate.Run(evs, investigateOpts())
	want := investigate.Run(raw, investigateOpts())
	if reflect.DeepEqual(want.Findings, got.Findings) {
		t.Fatal("one-person-per-revenue-row changed nothing — the money path is not exercised")
	}
}

// Re-reading yesterday is a NORMAL sync, not a repair: every vendor has late-arriving events. It is
// only safe because a day is a key. Append instead of merge and an hourly sync reports 24× traffic
// — and it looks like growth, which is the worst shape a wrong number can take.
func TestGuardResyncMustNotDoubleAnything(t *testing.T) {
	raw := corpus()
	snap := Reference(raw, referenceOpts())

	merged := Merge(snap.Buckets, snap.Buckets)
	if a, b := totalCount(merged), totalCount(snap.Buckets); a != b {
		t.Fatalf("re-reading the same window doubled the counts: %d then %d", b, a)
	}
	appended := append(append([]Bucket{}, snap.Buckets...), snap.Buckets...)
	if totalCount(appended) == totalCount(snap.Buckets) {
		t.Fatal("appending did not double, so the guard proves nothing")
	}
}

// unscopedDailyCounts is DailyCounts with the production filter removed — the bug, on purpose.
func unscopedDailyCounts(raw []event.Event, from, to time.Time) []Bucket {
	type key struct {
		day   time.Time
		event string
	}
	acc := map[key]*Bucket{}
	for _, e := range raw {
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

// ---------------------------------------------------------------------------
// THE OTHER HALF OF HONESTY: what the aggregate genuinely cannot do, proved to
// be undoable rather than asserted in a comment.
// ---------------------------------------------------------------------------

// investigate.Movements is the quarter-level reading a founder forwards, and it is a PER-PERSON
// rate: the share of people active in each half of the window who did the event. A daily count has
// no person in it, only how many — so synthesised rows carry one identity each and the rate comes
// out as garbage that computes cleanly and prints confidently.
//
// This test exists to prove that is true, not to describe it. If a future change made Movements
// survive aggregation, this fails and the DegradePeople sentence should be deleted rather than
// left standing as a false apology.
func TestMovementsCannotSurviveAggregation(t *testing.T) {
	raw := corpus()
	fromRaw := investigate.Movements(raw, nil, investigate.MovementOpts{Now: testNow})
	evs, _ := Synthesize(Reference(raw, referenceOpts()))
	fromAgg := investigate.Movements(evs, nil, investigate.MovementOpts{Now: testNow})

	if reflect.DeepEqual(fromRaw, fromAgg) {
		t.Fatal("per-person rates came out identical from daily counts — either the corpus has one event per person, or DegradePeople is now a lie and should be deleted")
	}
	t.Logf("raw movements: %+v", fromRaw)
	t.Logf("aggregate movements (discarded, never rendered): %+v", fromAgg)

	// And the product must not render them. Investigate blanks the field; this is the assertion
	// that the blanking is not merely intended.
	f := &countingFetcher{raw: raw}
	held := DailyCounts(raw, testNow.AddDate(0, 0, -testDays), testNow)
	res, err := Investigate(context.Background(), f, held, InvestigateOpts{Now: testNow, Days: testDays, MinPeople: 20})
	if err != nil {
		t.Fatal(err)
	}
	if res.Movements != nil {
		t.Fatalf("a connected instance must render no movements at all, got %d", len(res.Movements))
	}
	var said bool
	for _, d := range res.Degradations {
		if d.Code == "people_rates" {
			said = true
		}
	}
	if !said {
		t.Error("blanking a section without saying why is the same silence this package exists to end")
	}
}

// A vendor query has an upper time bound by construction; investigate.revenuePerPerson does not.
// A client with a skewed clock stamping tomorrow is therefore visible to the raw path and invisible
// to any aggregate read, and no grouping choice changes that.
//
// It is a small edge and it is named rather than hidden, because the alternative — a corpus quietly
// arranged so it never comes up — is how a test suite ends up proving less than it claims.
func TestFutureStampedRowsAreNotVisible(t *testing.T) {
	raw := corpus()
	future := append(append([]event.Event{}, raw...), event.Event{
		Name: "checkout", DistinctID: "skewed", ID: "skewed",
		Timestamp:  testNow.Add(6 * time.Hour),
		Properties: map[string]any{"amount": 500000.0, "$browser": "Chrome"},
	})

	// The vendor cannot return it: the aggregate window ends at now.
	snap := Reference(future, referenceOpts())
	if got := totalCount(snap.Buckets); got != totalCount(Reference(raw, referenceOpts()).Buckets) {
		t.Fatal("a future-stamped row reached the aggregate — the window is not bounded above")
	}
	// The raw path can, through the one function with no upper bound.
	rev := investigate.Run(future, investigateOpts())
	base := investigate.Run(raw, investigateOpts())
	if rev.Findings[0].Cost.UsdPerMonth == base.Findings[0].Cost.UsdPerMonth {
		t.Skip("revenuePerPerson has gained an upper bound; this edge no longer exists")
	}
	t.Logf("raw sizes it at $%.0f/mo with the skewed row, $%.0f/mo without — the aggregate always says the latter",
		rev.Findings[0].Cost.UsdPerMonth, base.Findings[0].Cost.UsdPerMonth)
}
