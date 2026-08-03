package sql

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

type slice []event.Event

func (s slice) Scan(from, to time.Time, fn func(event.Event) error) error {
	for _, e := range s {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func at(day, hour int) time.Time {
	return time.Date(2026, 7, day, hour, 0, 0, 0, time.UTC)
}

// A fixture with the awkward shapes real event data has: missing properties, numeric strings,
// mixed types under one key, and a user who appears on several days.
func fixture() slice {
	return slice{
		{ID: "1", Name: "$pageview", DistinctID: "u1", Timestamp: at(1, 9), Properties: map[string]any{"country": "US", "plan": "pro", "value": 10}},
		{ID: "2", Name: "signup", DistinctID: "u1", Timestamp: at(1, 10), Properties: map[string]any{"country": "US", "plan": "pro", "value": "20"}},
		{ID: "3", Name: "$pageview", DistinctID: "u2", Timestamp: at(1, 11), Properties: map[string]any{"country": "GB", "plan": "free"}},
		{ID: "4", Name: "signup", DistinctID: "u2", Timestamp: at(2, 9), Properties: map[string]any{"country": "GB", "plan": "free", "value": 5}},
		{ID: "5", Name: "checkout", DistinctID: "u1", Timestamp: at(2, 12), Properties: map[string]any{"country": "US", "plan": "pro", "value": 99.5}},
		{ID: "6", Name: "$pageview", DistinctID: "u3", Timestamp: at(3, 8), Properties: nil},
		{ID: "7", Name: "signup", DistinctID: "u3", Timestamp: at(3, 9), Properties: map[string]any{"country": "DE"}},
	}
}

func run(t *testing.T, q string) *Result {
	t.Helper()
	parsed, err := Parse(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	res, err := Run(parsed, fixture(), DefaultLimits())
	if err != nil {
		t.Fatalf("run %q: %v", q, err)
	}
	return res
}

func mustFail(t *testing.T, q, wantSubstring string) {
	t.Helper()
	parsed, err := Parse(q)
	if err == nil {
		_, err = Run(parsed, fixture(), DefaultLimits())
	}
	if err == nil {
		t.Fatalf("expected %q to fail, it succeeded", q)
	}
	if wantSubstring != "" && !strings.Contains(err.Error(), wantSubstring) {
		t.Errorf("error for %q was %q, expected it to mention %q", q, err, wantSubstring)
	}
}

func rowStrings(res *Result) []string {
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = toStr(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func wantRows(t *testing.T, res *Result, want ...string) {
	t.Helper()
	got := rowStrings(res)
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// ---- the surface must be read-only, by construction ------------------------------------

// This is the security boundary. It is not a permission check that could be misconfigured —
// there is simply no way to express a write in the grammar. Each of these must fail to PARSE.
func TestOnlySelectParses(t *testing.T) {
	for _, q := range []string{
		"DELETE FROM events",
		"DROP TABLE events",
		"UPDATE events SET name = 'x'",
		"INSERT INTO events (name) VALUES ('x')",
		"TRUNCATE events",
		"ALTER TABLE events ADD COLUMN x",
		"CREATE TABLE t (a int)",
		"PRAGMA table_info(events)",
		"ATTACH DATABASE '/etc/passwd' AS x",
		"SELECT name FROM events; DROP TABLE events",
		"SELECT name FROM events; DELETE FROM events",
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("PARSED A WRITE: %q — the SQL surface is supposed to be read-only", q)
		}
	}
}

func TestOnlyEventsTable(t *testing.T) {
	mustFail(t, "SELECT name FROM users", "only table is")
	mustFail(t, "SELECT name FROM sqlite_master", "only table is")
}

// ---- correctness -------------------------------------------------------------------------

func TestGroupByWithCount(t *testing.T) {
	res := run(t, "SELECT name, count(*) AS n FROM events GROUP BY name ORDER BY n DESC, name ASC")
	wantRows(t, res, "$pageview|3", "signup|3", "checkout|1")
}

func TestCountDistinct(t *testing.T) {
	res := run(t, "SELECT count(distinct distinct_id) AS users FROM events")
	wantRows(t, res, "3")
}

func TestWhereFiltersBeforeAggregating(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE name = 'signup'")
	wantRows(t, res, "3")
}

func TestPropertyAccessAndNulls(t *testing.T) {
	res := run(t, "SELECT prop.country AS c, count(*) AS n FROM events GROUP BY c ORDER BY n DESC, c ASC")
	// u3's first event has no properties at all; its country groups as NULL (empty string key).
	// NULLs sort last in both directions by design, so the group with no country trails DE.
	wantRows(t, res, "US|3", "GB|2", "DE|1", "|1")
}

// NULL must not satisfy a negative comparison. Without this, `prop.plan != 'pro'` counts every
// event that has no plan at all, which reads as a real segment and is not one.
func TestNullDoesNotSatisfyComparisons(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE prop.plan != 'pro'")
	wantRows(t, res, "2") // the two free events; the two with no plan are excluded
	res = run(t, "SELECT count(*) AS n FROM events WHERE prop.plan IS NULL")
	wantRows(t, res, "2")
}

// sum/avg must coerce numeric strings, because SDKs send numbers as strings constantly.
func TestNumericStringsAggregate(t *testing.T) {
	res := run(t, "SELECT sum(prop.value) AS total FROM events")
	wantRows(t, res, "134.5") // 10 + "20" + 5 + 99.5
}

func TestDateBucketing(t *testing.T) {
	res := run(t, "SELECT date(timestamp) AS day, count(*) AS n FROM events GROUP BY day ORDER BY day")
	wantRows(t, res, "2026-07-01|3", "2026-07-02|2", "2026-07-03|2")
}

func TestHaving(t *testing.T) {
	res := run(t, "SELECT name, count(*) AS n FROM events GROUP BY name HAVING count(*) > 1 ORDER BY name")
	wantRows(t, res, "$pageview|3", "signup|3")
}

func TestOrderByAggregateNotSelected(t *testing.T) {
	res := run(t, "SELECT name FROM events GROUP BY name ORDER BY count(*) DESC, name ASC")
	wantRows(t, res, "$pageview", "signup", "checkout")
}

func TestLike(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE name LIKE '$%'")
	wantRows(t, res, "3")
	res = run(t, "SELECT count(*) AS n FROM events WHERE name LIKE '%out'")
	wantRows(t, res, "1")
	res = run(t, "SELECT count(*) AS n FROM events WHERE name LIKE 'signu_'")
	wantRows(t, res, "3")
}

func TestInList(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE name IN ('signup', 'checkout')")
	wantRows(t, res, "4")
	res = run(t, "SELECT count(*) AS n FROM events WHERE name NOT IN ('signup', 'checkout')")
	wantRows(t, res, "3")
}

func TestBooleanLogicAndPrecedence(t *testing.T) {
	// AND must bind tighter than OR, or this returns the wrong count.
	res := run(t, "SELECT count(*) AS n FROM events WHERE name = 'signup' AND prop.country = 'US' OR name = 'checkout'")
	wantRows(t, res, "2")
	res = run(t, "SELECT count(*) AS n FROM events WHERE name = 'signup' AND (prop.country = 'US' OR prop.country = 'GB')")
	wantRows(t, res, "2")
}

func TestMinMaxAndAvg(t *testing.T) {
	res := run(t, "SELECT min(prop.value) AS lo, max(prop.value) AS hi FROM events")
	wantRows(t, res, "5|99.5")
	res = run(t, "SELECT round(avg(prop.value), 2) AS a FROM events")
	wantRows(t, res, "33.63")
}

func TestNonAggregatedProjectionAndLimit(t *testing.T) {
	res := run(t, "SELECT name, distinct_id FROM events WHERE name = 'signup' LIMIT 2")
	wantRows(t, res, "signup|u1", "signup|u2")
}

func TestTimestampComparison(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE timestamp >= '2026-07-02'")
	wantRows(t, res, "4")
}

// ---- refusing to answer misleadingly -----------------------------------------------------

// A bare column beside an aggregate with no GROUP BY is the classic SQL footgun: permissive
// engines return an arbitrary row's value next to a total over every row. That is a number a
// person would act on and it means nothing.
func TestRefusesUngroupedColumnBesideAggregate(t *testing.T) {
	mustFail(t, "SELECT name, count(*) FROM events", "not grouped")
	mustFail(t, "SELECT name, prop.country, count(*) FROM events GROUP BY name", "not grouped")
}

func TestRefusesSelectStar(t *testing.T) {
	mustFail(t, "SELECT * FROM events", "name the columns")
}

func TestUnknownColumnIsAnError(t *testing.T) {
	// Must NOT be silently treated as a property, or a typo returns zero rows and reads as a
	// real answer.
	mustFail(t, "SELECT contry FROM events", "unknown column")
	mustFail(t, "SELECT name FROM events WHERE contry = 'US'", "unknown column")
}

func TestUnknownFunctionIsAnError(t *testing.T) {
	mustFail(t, "SELECT median(prop.value) FROM events", "unknown function")
}

func TestHavingRequiresGroupBy(t *testing.T) {
	mustFail(t, "SELECT count(*) FROM events HAVING count(*) > 1", "requires GROUP BY")
}

// ---- limits --------------------------------------------------------------------------------

func TestGroupCardinalityIsCapped(t *testing.T) {
	var big slice
	for i := 0; i < 5000; i++ {
		big = append(big, event.Event{
			ID: fmt.Sprint(i), Name: "e", DistinctID: fmt.Sprintf("u%d", i), Timestamp: at(1, 0),
		})
	}
	q, err := Parse("SELECT distinct_id, count(*) FROM events GROUP BY distinct_id")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(q, big, Limits{MaxRows: 100, MaxGroups: 100, Timeout: time.Second})
	if err == nil {
		t.Fatal("unbounded GROUP BY was allowed — this is how the box runs out of memory")
	}
	if !strings.Contains(err.Error(), "distinct values") {
		t.Errorf("error should name the cause, got %q", err)
	}
}

func TestRowLimitTruncatesAndSaysSo(t *testing.T) {
	q, err := Parse("SELECT name, distinct_id FROM events")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(q, fixture(), Limits{MaxRows: 2, MaxGroups: 100, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(res.Rows))
	}
	// Silent truncation is the failure mode here: a caller must never read a capped result as
	// the complete answer.
	if !res.Truncated {
		t.Error("result was capped but Truncated is false — the caller cannot tell it is partial")
	}
}

// A projection with no ORDER BY must stop scanning once it has enough rows, or a LIMIT 10 over
// ten million events costs ten million events of work.
func TestProjectionStopsEarly(t *testing.T) {
	var big slice
	for i := 0; i < 100_000; i++ {
		big = append(big, event.Event{ID: fmt.Sprint(i), Name: "e", DistinctID: "u", Timestamp: at(1, 0)})
	}
	q, err := Parse("SELECT name FROM events LIMIT 5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(q, big, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned > 100 {
		t.Errorf("scanned %d events to return 5 rows — the scan is not stopping early", res.Scanned)
	}
}

// ---- determinism ---------------------------------------------------------------------------

// The product's whole claim is that reports are computed, not generated. The same query over
// the same data must return byte-identical output every time, including where groups tie.
func TestResultsAreDeterministic(t *testing.T) {
	q := "SELECT prop.country AS c, count(*) AS n FROM events GROUP BY c ORDER BY n DESC"
	first := strings.Join(rowStrings(run(t, q)), ";")
	for i := 0; i < 25; i++ {
		if got := strings.Join(rowStrings(run(t, q)), ";"); got != first {
			t.Fatalf("run %d returned %q, first run returned %q — results are not deterministic", i, got, first)
		}
	}
}

func TestCaseInsensitiveKeywordsButCaseSensitiveProperties(t *testing.T) {
	lower := run(t, "select name, count(*) as n from events group by name order by n desc, name asc")
	upper := run(t, "SELECT name, COUNT(*) AS n FROM events GROUP BY name ORDER BY n DESC, name ASC")
	if strings.Join(rowStrings(lower), ";") != strings.Join(rowStrings(upper), ";") {
		t.Error("keyword case changed the result")
	}
	// Property keys are user data: prop.country and prop.COUNTRY are different keys, and
	// folding them would silently return nothing.
	res := run(t, "SELECT count(*) AS n FROM events WHERE prop.COUNTRY = 'US'")
	wantRows(t, res, "0")
}

func TestGroupKeysDoNotCollide(t *testing.T) {
	data := slice{
		{ID: "1", Name: "e", DistinctID: "u", Timestamp: at(1, 0), Properties: map[string]any{"a": "x", "b": "yz"}},
		{ID: "2", Name: "e", DistinctID: "u", Timestamp: at(1, 0), Properties: map[string]any{"a": "xy", "b": "z"}},
	}
	q, err := Parse("SELECT prop.a AS a, prop.b AS b, count(*) AS n FROM events GROUP BY a, b")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(q, data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Errorf("got %d groups, want 2 — concatenated group keys are colliding", len(res.Rows))
	}
}

func TestLikeMatcher(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"abc", "abc", true}, {"abc", "a%", true}, {"abc", "%c", true}, {"abc", "%b%", true},
		{"abc", "a_c", true}, {"abc", "a_", false}, {"abc", "%", true}, {"", "%", true},
		{"abc", "abcd", false}, {"abc", "", false}, {"aaa", "%a%a%", true},
		{"abc", "%%c", true}, {"a%b", "a%%b", true},
	}
	for _, c := range cases {
		if got := likeMatch(c.s, c.p); got != c.want {
			t.Errorf("likeMatch(%q, %q) = %v, want %v", c.s, c.p, got, c.want)
		}
	}
}

// The grammar goes into the MCP tool description, so an agent writes queries that run instead
// of guessing. If it drifts from what the parser accepts, every example is a trap.
func TestGrammarExamplesActuallyRun(t *testing.T) {
	g := Grammar()
	var examples []string
	for _, line := range strings.Split(g, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "SELECT ") {
			examples = append(examples, line)
		}
	}
	if len(examples) == 0 {
		t.Fatal("no examples found in Grammar() — the tool description has nothing to copy")
	}
	// Examples wrap across lines; rejoin on SELECT boundaries.
	joined := strings.Split(strings.SplitN(g, "Examples:\n", 2)[1], "\n  SELECT ")
	for i, ex := range joined {
		ex = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ex), "SELECT "))
		if ex == "" {
			continue
		}
		full := "SELECT " + strings.Join(strings.Fields(ex), " ")
		q, err := Parse(full)
		if err != nil {
			t.Errorf("example %d in Grammar() does not parse: %v\n  %s", i, err, full)
			continue
		}
		if _, err := Run(q, fixture(), DefaultLimits()); err != nil {
			t.Errorf("example %d in Grammar() does not run: %v\n  %s", i, err, full)
		}
	}
}

// An aggregate with no GROUP BY is defined over the whole set and yields exactly one row even
// when nothing matched. Returning no rows would make a caller report "no data" for a question
// that has a perfectly good answer: zero.
func TestAggregateOverEmptySelectionReturnsZeroNotNothing(t *testing.T) {
	res := run(t, "SELECT count(*) AS n FROM events WHERE name = 'does-not-exist'")
	wantRows(t, res, "0")
	res = run(t, "SELECT count(distinct distinct_id) AS users FROM events WHERE name = 'nope'")
	wantRows(t, res, "0")
	// A GROUP BY that matches nothing genuinely has no rows — there are no groups to report.
	res = run(t, "SELECT name, count(*) AS n FROM events WHERE name = 'nope' GROUP BY name")
	wantRows(t, res)
}

// Aliases must work where SQL says they work, because `count(*) AS n ... ORDER BY n DESC` is the
// single most natural query to write.
func TestAliasesResolveInGroupHavingAndOrder(t *testing.T) {
	res := run(t, "SELECT prop.country AS c, count(*) AS n FROM events GROUP BY c HAVING n > 1 ORDER BY n DESC")
	wantRows(t, res, "US|3", "GB|2")
	res = run(t, "SELECT date(timestamp) AS day, count(*) AS n FROM events GROUP BY day ORDER BY day DESC LIMIT 1")
	wantRows(t, res, "2026-07-03|2")
}

// A real column always beats an alias of the same name, so a query's meaning never depends on
// the alias list. Here `name` is both the event column and an alias over count(*), and the two
// interpretations give visibly different orderings: alphabetical if the column wins, ordered by
// count if the alias does.
func TestColumnBeatsAliasOfTheSameName(t *testing.T) {
	res := run(t, "SELECT name AS label, count(*) AS name FROM events GROUP BY name ORDER BY name")
	wantRows(t, res, "$pageview|3", "checkout|1", "signup|3")
}

// WHERE runs per event, before grouping exists, so an alias over an aggregate could not be
// evaluated there. The error has to say that rather than silently returning nothing.
func TestAliasesAreRejectedInWhere(t *testing.T) {
	mustFail(t, "SELECT count(*) AS n FROM events WHERE n > 1", "not available in WHERE")
}

// streamN yields n synthetic events while allocating nothing per event: the property maps are
// built once up front and handed out repeatedly. Without that, the fixture's own allocation
// dominates and the measurement says nothing about the query engine.
type streamN struct {
	n    int
	pool []map[string]any
}

func newStreamN(n, cardinality int) streamN {
	pool := make([]map[string]any, cardinality)
	for i := range pool {
		pool[i] = map[string]any{"country": fmt.Sprintf("c%d", i)}
	}
	return streamN{n: n, pool: pool}
}

func (s streamN) Scan(from, to time.Time, fn func(event.Event) error) error {
	ts := at(1, 0)
	for i := 0; i < s.n; i++ {
		if err := fn(event.Event{
			ID: "e", Name: "pageview", DistinctID: "u", Timestamp: ts,
			Properties: s.pool[i%len(s.pool)],
		}); err != nil {
			return err
		}
	}
	return nil
}

// The reason this engine was hand-written rather than materialising rows into an embedded
// database: aggregation must RETAIN memory proportional to the number of GROUPS, not the number
// of EVENTS. Ten times the events over the same cardinality must cost essentially nothing extra.
//
// Retained heap is measured, not cumulative allocation. Evaluating each event's group key
// allocates transiently no matter what, and that churn scales with events by definition; the
// question that matters is whether anything is still being HELD afterwards.
func TestAggregationRetainsGroupsNotEvents(t *testing.T) {
	measure := func(n int) (uint64, *Result) {
		q, err := Parse("SELECT prop.country AS c, count(*) AS n FROM events GROUP BY c")
		if err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&before)
		res, err := Run(q, newStreamN(n, 50), Limits{MaxRows: 1000, MaxGroups: 100_000, Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC() // reclaim the scan's transient garbage; whatever survives is retained
		runtime.ReadMemStats(&after)
		if len(res.Rows) != 50 {
			t.Fatalf("expected 50 groups, got %d", len(res.Rows))
		}
		if after.HeapAlloc < before.HeapAlloc {
			return 0, res
		}
		return after.HeapAlloc - before.HeapAlloc, res
	}

	small, r1 := measure(20_000)
	large, r2 := measure(400_000)
	t.Logf("50 groups retained after 20k events: %d KB; after 400k events: %d KB", small/1024, large/1024)
	runtime.KeepAlive(r1)
	runtime.KeepAlive(r2)

	// 20x the events over identical cardinality. Anything retaining events shows up as a
	// proportional increase; accumulating into 50 groups shows up as roughly flat.
	if large > small+512*1024 {
		t.Errorf("20x the events retained %d KB more — aggregation is holding events rather than accumulating groups",
			(large-small)/1024)
	}
}
