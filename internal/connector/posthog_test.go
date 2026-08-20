package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// fakePostHog answers the real HogQL the client sends, by running the reference aggregation over
// a raw corpus and rendering it in PostHog's response shape.
//
// It is not a mock of our own client. It parses nothing it does not have to, but it DOES record
// every query text, so the assertions below are about the SQL that would hit a real PostHog —
// which is the only part a test can check without a credential.
type fakePostHog struct {
	raw     []event.Event
	queries []string
	server  *httptest.Server
}

func newFakePostHog(t *testing.T, raw []event.Event) *fakePostHog {
	t.Helper()
	f := &fakePostHog{raw: raw}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/api/projects/42/query") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Query struct {
				Kind  string `json:"kind"`
				Query string `json:"query"`
			} `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query.Kind != "HogQLQuery" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		q := body.Query.Query
		f.queries = append(f.queries, q)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": f.answer(q)})
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePostHog) answer(q string) [][]any {
	switch {
	case strings.Contains(q, "GROUP BY day, event"):
		var out [][]any
		for _, b := range DailyCounts(f.raw, testNow.AddDate(0, 0, -400), testNow.AddDate(0, 0, 1)) {
			out = append(out, []any{
				b.Day.Format(time.DateOnly), b.Event, float64(b.Count),
				b.First.Format("2006-01-02 15:04:05"), b.Last.Format("2006-01-02 15:04:05"),
			})
		}
		return out
	case strings.Contains(q, "GROUP BY day, dim, val"):
		name := between(q, "AND event = '", "'")
		segs, _ := SegmentCounts(f.raw, name, testNow.AddDate(0, 0, -400), testNow.AddDate(0, 0, 1), CauseDims)
		var out [][]any
		for day, byDim := range segs {
			for dim, vals := range byDim {
				for v, n := range vals {
					out = append(out, []any{day.Format(time.DateOnly), dim, v, float64(n)})
				}
			}
		}
		return out
	default: // the money counters
		name := between(q, "AND event = '", "'")
		from := testNow.AddDate(0, 0, -testDays)
		r := RevenueFor(f.raw, name, from, testNow, from, testNow)
		var row []any
		for _, p := range revenueProps {
			if p != r.Prop {
				row = append(row, 0.0, 0.0, 0.0, 0.0, 0.0)
				continue
			}
			row = append(row, float64(r.Seen), float64(r.Numeric), float64(r.Rows), float64(r.People), r.Total)
		}
		return [][]any{row}
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func newClient(f *fakePostHog) *PostHog {
	return &PostHog{
		Host: f.server.URL, Project: "42", Key: NewCredential("secret-key"),
		HTTP: f.server.Client(), Limiter: NewLimiter(1000, time.Millisecond),
	}
}

// The end-to-end claim, through the real HTTP client, the real query text and the real parser:
// findings read out of a vendor as aggregates equal findings computed from the raw events.
func TestPostHogAggregateReadEqualsRawFindings(t *testing.T) {
	raw := corpus()
	f := newFakePostHog(t, raw)
	ph := newClient(f)

	ctx := context.Background()
	held, err := ph.PhaseA(ctx, testNow.AddDate(0, 0, -testDays), testNow)
	if err != nil {
		t.Fatalf("phase A: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("phase A returned nothing")
	}

	res, err := Investigate(ctx, ph, held, InvestigateOpts{Now: testNow, Days: testDays, MinPeople: 20})
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	want := investigate.Run(raw, investigateOpts())
	if !reflect.DeepEqual(want.Findings, res.Findings) {
		t.Errorf("vendor-read findings differ from raw:\n raw %+v\n agg %+v", want.Findings, res.Findings)
	}

	// The honesty half must be attached, not merely true somewhere.
	codes := map[string]bool{}
	for _, d := range res.Degradations {
		codes[d.Code] = true
	}
	for _, want := range []string{"sequence", "people_rates"} {
		if !codes[want] {
			t.Errorf("every aggregate read must declare %q; got %v", want, codes)
		}
	}
	if res.Movements != nil {
		t.Error("Movements is a per-person rate and cannot be computed from daily counts — it must be blank, not plausible")
	}
	if got := res.CheckedDims["checkout"]; len(got) != len(CauseDims) {
		t.Errorf("the finding must say which dimensions it was checked against, got %v", got)
	}
}

// Phase B is what makes high-cardinality dimensions affordable at all, so the query count is a
// design property, not an implementation detail: one query for detection, two per changed event.
func TestPostHogSpendsOneQueryPerSyncAndTwoPerFinding(t *testing.T) {
	raw := corpus()
	f := newFakePostHog(t, raw)
	ph := newClient(f)

	held, err := ph.PhaseA(context.Background(), testNow.AddDate(0, 0, -testDays), testNow)
	if err != nil {
		t.Fatal(err)
	}
	afterPhaseA := len(f.queries)
	if afterPhaseA != 1 {
		t.Fatalf("detection must cost exactly one query, spent %d", afterPhaseA)
	}
	res, err := Investigate(context.Background(), ph, held, InvestigateOpts{Now: testNow, Days: testDays, MinPeople: 20})
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]bool{}
	for _, fd := range res.Findings {
		if fd.Event != "" {
			events[fd.Event] = true
		}
	}
	if want := afterPhaseA + 2*len(events); len(f.queries) != want {
		t.Errorf("attribution must cost two queries per changed event: %d findings-events, %d queries (want %d)",
			len(events), len(f.queries), want)
	}
	t.Logf("%d events scanned, %d findings, %d queries total", len(res.Scanned), len(res.Findings), len(f.queries))
}

// The query is an AGGREGATION, and that is checkable by looking at it.
//
// PostHog's docs say they may "rate-limit, restrict, or reject queries that look like exports", and
// that /query is not an export mechanism. The defence is not a promise about intent, it is the
// shape: every query groups, none selects a row, and the response size is bounded by
// (days × event names) rather than by how many events the customer has.
func TestPostHogQueriesAreAggregationsNotExports(t *testing.T) {
	f := newFakePostHog(t, corpus())
	ph := newClient(f)
	held, _ := ph.PhaseA(context.Background(), testNow.AddDate(0, 0, -testDays), testNow)
	_, _ = Investigate(context.Background(), ph, held, InvestigateOpts{Now: testNow, Days: testDays, MinPeople: 20})

	if len(f.queries) < 3 {
		t.Fatalf("expected phase A and phase B queries, got %d", len(f.queries))
	}
	for i, q := range f.queries {
		if strings.Contains(q, "SELECT *") || strings.Contains(q, "distinct_id") || strings.Contains(q, "uuid") {
			t.Errorf("query %d selects rows rather than counts — that is an export:\n%s", i, q)
		}
		if !strings.Contains(q, "count(") && !strings.Contains(q, "countIf(") {
			t.Errorf("query %d aggregates nothing:\n%s", i, q)
		}
		if !strings.Contains(q, "coalesce(toString(properties['env']), '') NOT IN") {
			t.Errorf("query %d is missing the production scope — it would count development traffic the raw path hides:\n%s", i, q)
		}
		if !strings.Contains(q, "timestamp >= toDateTime(") || !strings.Contains(q, "timestamp < toDateTime(") {
			t.Errorf("query %d is unbounded in time:\n%s", i, q)
		}
	}
	// And the breakdown asks for marginals, one GROUP BY per dimension unioned, never a
	// cross-product — which is the row explosion that makes people believe this cannot work.
	var seg string
	for _, q := range f.queries {
		if strings.Contains(q, "GROUP BY day, dim, val") {
			seg = q
		}
	}
	if seg == "" {
		t.Fatal("no breakdown query was sent")
	}
	if n := strings.Count(seg, "UNION ALL"); n != len(CauseDims)-1 {
		t.Errorf("the breakdown must union one marginal per dimension (%d), got %d UNION ALLs", len(CauseDims)-1, n)
	}
}

// An event name with an apostrophe is customer-controlled text landing in a SQL string. It must
// produce a working query, not a syntax error and not an injection.
func TestPostHogQuotesCustomerText(t *testing.T) {
	f := newFakePostHog(t, corpus())
	ph := newClient(f)
	_, _, _, err := ph.PhaseB(context.Background(), `it's a trap' OR 1=1 --`,
		testNow.AddDate(0, 0, -8), testNow, testNow.AddDate(0, 0, -30), testNow, CauseDims)
	if err != nil {
		t.Fatalf("a quoted name must still query: %v", err)
	}
	// THE PROPERTY IS BALANCE, not the presence of a backslash. Strip the escape sequences and
	// every remaining quote must pair up: an odd count means the name closed its own literal and
	// the rest of it became SQL.
	sawEscape := false
	for _, q := range f.queries {
		if strings.Contains(q, `\'`) {
			sawEscape = true
		}
		if n := strings.Count(strings.ReplaceAll(strings.ReplaceAll(q, `\\`, ""), `\'`, ""), "'"); n%2 != 0 {
			t.Errorf("unbalanced quotes — the event name escaped its literal:\n%s", q)
		}
	}
	if !sawEscape {
		t.Error("the apostrophe was never escaped")
	}
}

// A credential is the most dangerous thing a user hands this tool. It must not be printable by
// accident — a %v in a log line, a struct in an error, a Snapshot serialised to a store.
func TestCredentialNeverPrintsItself(t *testing.T) {
	c := NewCredential("phx_supersecret")
	if s := fmt.Sprintf("%v %s %+v", c, c, struct{ K Credential }{c}); strings.Contains(s, "supersecret") {
		t.Fatalf("credential leaked into a formatted string: %s", s)
	}
	b, err := json.Marshal(map[string]any{"key": c})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "supersecret") {
		t.Fatalf("credential leaked into JSON: %s", b)
	}
	// And it still works, or the redaction is just a broken key.
	ph := &PostHog{Project: "1", Key: c}
	if err := ph.validate(); err != nil {
		t.Fatalf("a redacted credential must still authenticate: %v", err)
	}
}

// Failures have to name the thing to change. "401 Unauthorized" is true and costs an afternoon.
func TestPostHogErrorsSayWhatToFix(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "query:read"},
		{http.StatusNotFound, "EU cloud"},
		{http.StatusTooManyRequests, "240/minute"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		ph := &PostHog{Host: srv.URL, Project: "1", Key: NewCredential("k"), HTTP: srv.Client(), Limiter: NewLimiter(100, time.Millisecond)}
		_, err := ph.PhaseA(context.Background(), testNow.AddDate(0, 0, -1), testNow)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("status %d should mention %q, got %v", tc.status, tc.want, err)
		}
		srv.Close()
	}
}
