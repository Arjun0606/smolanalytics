package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

// PostHog, read as an aggregate.
//
// WHY THIS VENDOR FIRST, and only this vendor first: it is the only one of the five where the
// documented API answers every question the investigator asks, in the shape it asks them, inside
// one rate-limit budget.
//
//   - HogQL is real SQL over the event table. count(), uniq(), sum(), GROUP BY, arbitrary event
//     names, arbitrary properties. Nothing has to be approximated to fit the API's vocabulary.
//   - The limits are generous enough that the cost is not a design constraint: 240 requests a
//     minute and 1,200 an hour on analytics endpoints (posthog.com/docs/api#rate-limiting).
//     A sync is one query. An hour of syncing a loud product is under ten.
//   - A HogQL client against this exact endpoint already exists in this repository
//     (cmd/smolanalytics/plan_posthog.go), so the transport is proven rather than assumed.
//
// AND THE HONEST PART. PostHog's query docs say, of this endpoint: "We reserve the right to
// rate-limit, restrict, or reject queries that look like exports", and separately that /query is
// "not a supported export mechanism" and that bulk export belongs in batch exports. That sentence
// is the reason the old importer's raw pull is a countdown rather than an integration.
//
// It does not describe what this file sends. Every query here is an aggregation: GROUP BY day and
// event, GROUP BY day and one property value, five counters over one event. The largest response
// this can produce is bounded by phaseALimit rows regardless of how many events the customer has,
// because the row count is (days × distinct event names), not (events). A read whose size does not
// grow with the customer's volume is not an export, and that is a property of the query, provable
// by looking at it, rather than a promise about intent.

// PostHogHost defaults to US cloud. EU cloud and self-hosted are a different domain, which is the
// single most common cause of a 404 on a correct key.
const PostHogHost = "https://us.i.posthog.com"

// phaseALimit caps Phase A. 90 days × 500 distinct event names is 45,000; a project with more
// distinct event names than that has a naming problem, and truncating silently would make a metric
// vanish from the brief with no explanation, so the limit is checked and reported.
const phaseALimit = 50000

// phaseBLimit caps one Phase B breakdown: 16 days × 6 dimensions × maxSegmentValues.
const phaseBLimit = 60000

// Credential holds a read-only vendor key for the life of one process.
//
// It exists to make the key hard to leak by accident rather than to encrypt it: String and
// MarshalJSON redact, so a %v in a log line, a struct dumped into an error, or a Snapshot
// serialised to a store all print the redaction instead of the secret. Nothing in this package
// writes it to disk — the sync store persists buckets and a watermark, never this.
type Credential struct{ key string }

// NewCredential wraps a key. Read-only scopes only: PostHog needs `query:read` on a personal API
// key, which cannot write, cannot read persons, and can be revoked from the same screen it is made.
func NewCredential(key string) Credential { return Credential{key: strings.TrimSpace(key)} }

func (c Credential) String() string { return "[redacted]" }

func (c Credential) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

func (c Credential) empty() bool { return c.key == "" }

// PostHog is a Fetcher over one PostHog project.
type PostHog struct {
	Host    string
	Project string
	Key     Credential
	HTTP    *http.Client
	// Limiter bounds requests against the vendor's published ceiling. Nil means the default,
	// which is PostHog's documented 240/minute.
	Limiter *Limiter
}

func (p *PostHog) Vendor() string { return "posthog" }

func (p *PostHog) validate() error {
	if p.Project == "" {
		return fmt.Errorf("posthog needs a project id — the number in your PostHog URL, /project/<id>/")
	}
	if p.Key.empty() {
		return fmt.Errorf("posthog needs a personal API key scoped to query:read (Settings → Personal API keys), not the project write key from your snippet")
	}
	return nil
}

// PhaseA is the whole of change detection, in one query.
//
//	SELECT toDate(timestamp), event, count(), min(timestamp), max(timestamp)
//	FROM events WHERE <window> AND <production scope> GROUP BY 1, 2
//
// min and max are not decoration: cause.windowEitherSide measures the comparison window from the
// metric's real first and last row, and a bucket pinned to midnight moves that window by a day.
func (p *PostHog) PhaseA(ctx context.Context, from, to time.Time) ([]Bucket, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`SELECT toDate(timestamp) AS day, event, count() AS c,
       min(timestamp) AS first_seen, max(timestamp) AS last_seen
FROM events
WHERE timestamp >= %s AND timestamp < %s AND %s
GROUP BY day, event
ORDER BY day, event
LIMIT %d`, hogTime(from), hogTime(to), hogScope(), phaseALimit)

	rows, err := p.run(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(rows) >= phaseALimit {
		return nil, fmt.Errorf("posthog returned the %d-row cap for a %d-day window — that is more distinct event names than this can summarise honestly; narrow the window",
			phaseALimit, int(to.Sub(from).Hours()/24))
	}
	out := make([]Bucket, 0, len(rows))
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		day, err := hogDay(r[0])
		if err != nil {
			return nil, fmt.Errorf("posthog returned a day this cannot read (%v): %w", r[0], err)
		}
		name, _ := r[1].(string)
		if name == "" {
			continue
		}
		b := Bucket{Day: day, Event: name, Count: hogInt(r[2])}
		b.First = hogStamp(r[3], day)
		b.Last = hogStamp(r[4], day)
		out = append(out, b)
	}
	sortBuckets(out)
	return out, nil
}

// PhaseB fetches one event's dimension marginals and money counters: two queries, run only for an
// event that actually produced a finding.
func (p *PostHog) PhaseB(ctx context.Context, name string, segFrom, segTo, revFrom, revTo time.Time, dims []string) (map[time.Time]map[string]map[string]int, []string, Revenue, error) {
	if err := p.validate(); err != nil {
		return nil, nil, Revenue{}, err
	}
	segs, dropped, err := p.segments(ctx, name, segFrom, segTo, dims)
	if err != nil {
		return nil, nil, Revenue{}, err
	}
	rev, err := p.revenue(ctx, name, revFrom, revTo)
	if err != nil {
		// A missing money figure is a smaller loss than a missing cause, and the two fail for
		// different reasons. Returning the segments we did get, with a zero Revenue, sizes the
		// finding in people — which is a real answer — instead of discarding both.
		return segs, dropped, Revenue{}, nil
	}
	return segs, dropped, rev, nil
}

// segments asks for MARGINALS: one GROUP BY per dimension, unioned into one request.
//
// A UNION ALL rather than six requests because six requests is six rate-limit units for one
// question, and rather than one GROUP BY over all six because that is a cross-product — every
// dimension's cardinality multiplied together, which is the row explosion that makes people
// conclude an aggregate read is impossible. worstSegment reads one dimension at a time and never
// joins two, so the joint is a product nobody buys.
func (p *PostHog) segments(ctx context.Context, name string, from, to time.Time, dims []string) (map[time.Time]map[string]map[string]int, []string, error) {
	parts := make([]string, 0, len(dims))
	for _, d := range dims {
		parts = append(parts, fmt.Sprintf(
			`SELECT toDate(timestamp) AS day, %s AS dim, toString(properties[%s]) AS val, count() AS c
FROM events
WHERE timestamp >= %s AND timestamp < %s AND event = %s AND %s
  AND toString(properties[%s]) != '' AND isNotNull(properties[%s])
GROUP BY day, dim, val`,
			hogString(d), hogString(d), hogTime(from), hogTime(to), hogString(name), hogScope(), hogString(d), hogString(d)))
	}
	q := strings.Join(parts, "\nUNION ALL\n") + fmt.Sprintf("\nLIMIT %d", phaseBLimit)

	rows, err := p.run(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) >= phaseBLimit {
		return nil, nil, fmt.Errorf("posthog returned the %d-row cap breaking down %s", phaseBLimit, name)
	}

	perDay := map[time.Time]map[string]map[string]int{}
	distinct := map[string]map[string]bool{}
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		day, err := hogDay(r[0])
		if err != nil {
			continue
		}
		dim, _ := r[1].(string)
		val, _ := r[2].(string)
		if dim == "" || val == "" {
			continue
		}
		if distinct[dim] == nil {
			distinct[dim] = map[string]bool{}
		}
		distinct[dim][val] = true
		if perDay[day] == nil {
			perDay[day] = map[string]map[string]int{}
		}
		if perDay[day][dim] == nil {
			perDay[day][dim] = map[string]int{}
		}
		perDay[day][dim][val] += hogInt(r[3])
	}

	// A dimension with too many values is DROPPED WHOLE, never truncated. worstSegment divides one
	// value's loss by the dimension's total loss; keep the head and lose the tail and the
	// denominator shrinks, every surviving share inflates, and a dimension that is genuinely spread
	// evenly reports a concentration over the 55% bar. That is a confident, specific, false
	// sentence about someone's business, which is worse than saying nothing.
	var dropped []string
	for _, d := range dims {
		if len(distinct[d]) > maxSegmentValues {
			dropped = append(dropped, d)
			for _, byDim := range perDay {
				delete(byDim, d)
			}
		}
	}
	return perDay, dropped, nil
}

// revenue computes money.go's counters for every candidate property in ONE query, so the property
// choice is made here on the same evidence money.revenuePropFor would use.
//
// Two windows in one row, deliberately: Seen and Numeric are the property-quality tally over
// everything held, while Rows, People and Total are the reporting window the mean is stated over.
// Computing the mean over one window and multiplying it by a people count from another is the
// easiest way to produce a dollar figure that is wrong by exactly the ratio of the two windows.
func (p *PostHog) revenue(ctx context.Context, name string, from, to time.Time) (Revenue, error) {
	var cols []string
	for _, prop := range revenueProps {
		lit := hogString(prop)
		num := fmt.Sprintf("toFloat(properties[%s])", lit)
		cols = append(cols,
			fmt.Sprintf("countIf(isNotNull(properties[%s])) ", lit),
			fmt.Sprintf("countIf(isNotNull(%s))", num),
			fmt.Sprintf("countIf(isNotNull(%s) AND %s >= 0)", num, num),
			fmt.Sprintf("uniqIf(person_id, isNotNull(%s) AND %s >= 0)", num, num),
			fmt.Sprintf("sumIf(%s, isNotNull(%s) AND %s > 0)", num, num, num),
		)
	}
	q := fmt.Sprintf(`SELECT %s
FROM events
WHERE timestamp >= %s AND timestamp < %s AND event = %s AND %s`,
		strings.Join(cols, ",\n       "), hogTime(from), hogTime(to), hogString(name), hogScope())

	rows, err := p.run(ctx, q)
	if err != nil {
		return Revenue{}, err
	}
	if len(rows) == 0 || len(rows[0]) < len(revenueProps)*5 {
		return Revenue{}, fmt.Errorf("posthog returned no money counters for %s", name)
	}
	r := rows[0]

	// Same tie-break as money.revenuePropFor, in the same priority order, on the same two gates:
	// at least four in five values numeric, then the most numeric values wins. Mirrored rather than
	// imported because those constants are unexported there — and the equivalence test is what
	// catches a drift, on the finding rather than on the constant.
	best := Revenue{}
	for i, prop := range revenueProps {
		got := Revenue{
			Prop:    prop,
			Seen:    hogInt(r[i*5]),
			Numeric: hogInt(r[i*5+1]),
			Rows:    hogInt(r[i*5+2]),
			People:  hogInt(r[i*5+3]),
			Total:   hogFloat(r[i*5+4]),
		}
		if got.Numeric == 0 || got.Seen == 0 {
			continue
		}
		if float64(got.Numeric)/float64(got.Seen) < minNumericRatio {
			continue
		}
		if got.Numeric > best.Numeric {
			best = got
		}
	}
	return best, nil
}

// run posts one HogQL query. Every failure names the credential or the setting to change, because
// "401 Unauthorized" is true and costs an afternoon.
func (p *PostHog) run(ctx context.Context, hogql string) ([][]any, error) {
	lim := p.Limiter
	if lim == nil {
		lim = DefaultPostHogLimiter()
	}
	if err := lim.Wait(ctx); err != nil {
		return nil, err
	}
	host := p.Host
	if host == "" {
		host = PostHogHost
	}
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"kind": "HogQLQuery", "query": hogql},
	})
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(host, "/") + "/api/projects/" + url.PathEscape(p.Project) + "/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.Key.key)

	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w — check the host (EU cloud and self-hosted are different domains)", u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("posthog returned %s — the key needs the query:read scope (Settings → Personal API keys) and access to project %s: %s",
			resp.Status, p.Project, snippet(raw))
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("posthog returned 404 for project %q — check the project id, and the host if this project is on EU cloud: %s", p.Project, snippet(raw))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("posthog rate-limited this read (their analytics limit is 240/minute, 1200/hour) — the sync will retry on the next tick: %s", snippet(raw))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("posthog returned %s: %s", resp.Status, snippet(raw))
	}
	var res struct {
		Results [][]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unreadable posthog response from %s — is that a PostHog host? (%s)", u, snippet(raw))
	}
	return res.Results, nil
}

// hogScope is query.Apply(evs, nil) written in HogQL.
//
// IT MUST MATCH, and it is the likeliest place for this connector to go quietly wrong. The
// investigator filters the events it is handed, so on a directly-instrumented instance development
// and preview traffic never reaches the detector. If the vendor-side WHERE does not hide the same
// population, the aggregate counts include traffic the raw path excludes and every number differs
// by however much dev traffic that customer sends — with no error anywhere.
//
// coalesce, not `!= 'development'`: a missing property compares as NULL in ClickHouse, NULL is not
// true, and the row would be dropped. Every production event of a customer who never sets env has
// no env property, so getting this wrong returns zero rows.
func hogScope() string {
	names := make([]string, 0, len(query.NonProduction))
	for n := range query.NonProduction {
		names = append(names, hogString(n))
	}
	sort.Strings(names) // deterministic query text, so a diff of two syncs is readable
	return "coalesce(toString(properties['env']), '') NOT IN (" + strings.Join(names, ", ") + ")"
}

func hogTime(t time.Time) string {
	return "toDateTime(" + hogString(t.UTC().Format("2006-01-02 15:04:05")) + ")"
}

// hogString renders a HogQL single-quoted literal. Every value this file interpolates — event
// names, property names, dates — comes from the vendor or from our own constants, and is quoted
// anyway: an event name is customer-controlled text, and a customer who names an event with an
// apostrophe must get a working query rather than a syntax error.
func hogString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func hogDay(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("not a string")
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Truncate(24 * time.Hour), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

// hogStamp reads a min/max timestamp, falling back to the day itself. The fallback is a real
// degradation of the comparison window, so it is confined to a genuinely unreadable value rather
// than being the normal path.
func hogStamp(v any, day time.Time) time.Time {
	s, ok := v.(string)
	if !ok {
		return day
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return day
}

func hogInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err == nil {
			return int(f)
		}
	}
	return 0
}

func hogFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
