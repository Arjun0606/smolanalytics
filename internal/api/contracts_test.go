package api

// The Phase-0 correctness contracts from KILLER_PLAN.md, each pinned as a named
// test. These are the product's covenant: one question yields ONE number across
// every surface and every grain. A failure here is a release blocker, full stop.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// contractEvents builds a deterministic fixture: `signup` events spread across the
// 10 days before now, including edge cases exactly at window boundaries, plus one
// user who is active on three separate days (the range-dedup case).
func contractEvents(now time.Time) []event.Event {
	var evs []event.Event
	add := func(daysAgo int, hour int, user string) {
		evs = append(evs, event.Event{
			Name:       "signup",
			DistinctID: user,
			Timestamp:  now.Truncate(24*time.Hour).AddDate(0, 0, -daysAgo).Add(time.Duration(hour) * time.Hour),
		})
	}
	// 3 signups/day for days 1..9 ago, distinct users
	n := 0
	for d := 1; d <= 9; d++ {
		for k := 0; k < 3; k++ {
			n++
			add(d, 9+k, fmt.Sprintf("u%03d", n))
		}
	}
	// the repeat user: active 2, 4, and 6 days ago (must count ONCE per window)
	add(2, 15, "repeat")
	add(4, 15, "repeat")
	add(6, 15, "repeat")
	return evs
}

// CONTRACT WINDOW-2: bucket-sum invariance — the same window totals identically
// under day, week, and month bucketing, and matches the unbucketed compute.
func TestContractWindow2BucketSumInvariance(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	evs := contractEvents(now)
	from, to := now.AddDate(0, 0, -7), now

	day := trends.Compute(evs, "signup", from, to, false)
	week := trends.ComputeInterval(evs, "signup", from, to, false, trends.Week)
	month := trends.ComputeInterval(evs, "signup", from, to, false, trends.Month)

	if day.Total != week.Total || day.Total != month.Total {
		t.Fatalf("bucket-sum invariance broken: day=%d week=%d month=%d", day.Total, week.Total, month.Total)
	}
	// and the day-bucket points themselves must sum to the total
	sum := 0
	for _, p := range day.Points {
		sum += p.Count
	}
	if sum != day.Total {
		t.Fatalf("day points sum %d != total %d", sum, day.Total)
	}
}

// CONTRACT WINDOW-1: strict window filtering — an event before the window start
// never leaks in, and "yesterday" spans exactly one bucket, not two.
func TestContractWindow1StrictFiltering(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	today := now.Truncate(24 * time.Hour)
	evs := []event.Event{
		{Name: "signup", DistinctID: "in", Timestamp: today.Add(-24 * time.Hour).Add(5 * time.Hour)}, // inside yesterday
		{Name: "signup", DistinctID: "out", Timestamp: today.Add(-24 * time.Hour).Add(-time.Minute)}, // day before — OUT
		{Name: "signup", DistinctID: "out2", Timestamp: today.Add(time.Minute)},                      // today — OUT of yesterday
	}
	r := trends.Compute(evs, "signup", today.Add(-24*time.Hour), today, false)
	if r.Total != 1 {
		t.Fatalf("yesterday must contain exactly the inside event: total=%d", r.Total)
	}
	if len(r.Points) != 1 {
		t.Fatalf("yesterday is ONE bucket, got %d (the off-by-one that made a 1-day window an 8-bucket week)", len(r.Points))
	}
}

// CONTRACT TRENDS-UNIQUE: per-bucket uniques may repeat a user across buckets, but
// the range total counts each user ONCE across the whole window.
func TestContractTrendsUniqueRangeDedup(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	today := now.Truncate(24 * time.Hour)
	evs := []event.Event{
		{Name: "open", DistinctID: "alice", Timestamp: today.AddDate(0, 0, -2).Add(9 * time.Hour)},
		{Name: "open", DistinctID: "alice", Timestamp: today.AddDate(0, 0, -1).Add(9 * time.Hour)},
	}
	r := trends.Compute(evs, "open", today.AddDate(0, 0, -3), today, true)
	got := []int{}
	for _, p := range r.Points {
		if p.Count > 0 {
			got = append(got, p.Count)
		}
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 1 {
		t.Fatalf("per-bucket uniques wrong: %v", got)
	}
	if r.Total != 1 {
		t.Fatalf("range total must dedup across the window: got %d, want 1 (alice is one user, not two)", r.Total)
	}
	// and the interval path agrees
	w := trends.ComputeInterval(evs, "open", today.AddDate(0, 0, -3), today, true, trends.Week)
	if w.Total != 1 {
		t.Fatalf("weekly unique total must also dedup: got %d", w.Total)
	}
}

// CONTRACT SURFACE-1: the ask bar's number equals the trends engine's number for
// the same window — the provenance receipt's claim, now actually enforced.
func TestContractSurface1AskEqualsTrends(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	evs := contractEvents(now)

	win, unsup := parseWindow("how many signups in the last 7 days", now)
	if unsup != "" {
		t.Fatalf("window should parse: %q", unsup)
	}
	answer := answerSignups(scope(evs, win), []string{"signup"}, win)
	m := regexp.MustCompile(`^(\d+)`).FindStringSubmatch(answer)
	if m == nil {
		t.Fatalf("no leading number in answer: %q", answer)
	}
	askN, _ := strconv.Atoi(m[1])

	tr := trends.Compute(evs, "signup", win.from, win.to, false)
	if askN != tr.Total {
		t.Fatalf("SURFACE-1 broken: ask says %d, trends says %d — the receipt's promise must be true", askN, tr.Total)
	}
}

// CONTRACT BREAKDOWN-WINDOW + ERRORS-1, at the HTTP surface: /v1/breakdown honors
// days=, and naming an unknown event 400s with the known list instead of returning
// a real-looking zero report.
func TestContractBreakdownWindowAndHonestErrors(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	ingest := func(daysAgo int, user, plan string) {
		_ = st.Ingest(event.Event{
			Name: "signup", DistinctID: user,
			Timestamp:  today.AddDate(0, 0, -daysAgo).Add(9 * time.Hour),
			Properties: map[string]any{"plan": plan},
		})
	}
	ingest(1, "a", "pro")
	ingest(2, "b", "pro")
	ingest(9, "c", "pro") // outside days=7
	srv := httptest.NewServer(New(st).Handler())
	t.Cleanup(srv.Close)

	get := func(path string) (int, map[string]any) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	_, all := get("/v1/breakdown?event=signup&property=plan")
	_, week := get("/v1/breakdown?event=signup&property=plan&days=7")
	sum := func(m map[string]any) float64 {
		total := 0.0
		if gs, ok := m["groups"].([]any); ok {
			for _, g := range gs {
				total += g.(map[string]any)["count"].(float64)
			}
		}
		return total
	}
	if sum(all) != 3 || sum(week) != 2 {
		t.Fatalf("BREAKDOWN-WINDOW broken: all=%v week=%v (want 3 and 2)", sum(all), sum(week))
	}

	code, body := get("/v1/trends?event=nonexistent_event")
	if code != http.StatusBadRequest {
		t.Fatalf("unknown event must 400, got %d (%v)", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "signup") {
		t.Fatalf("the 400 must list known events, got %q", msg)
	}

	// API-1: the four list GETs exist as JSON (not the HTML 404 page)
	for _, p := range []string{"/v1/goals", "/v1/shares", "/v1/alerts", "/v1/webhooks"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		ct := resp.Header.Get("Content-Type")
		resp.Body.Close()
		if !strings.Contains(ct, "json") {
			t.Fatalf("%s must answer JSON (got %s) — /v1/* never falls through to HTML", p, ct)
		}
	}
}

// CONTRACT FUNNEL-ORDER + FUNNEL-EXCLUDE + FUNNEL-STEPFILTER: the three disciplines
// count a crafted sequence exactly as documented, and an excluded event disqualifies
// the anchor but not a later clean attempt.
func TestContractFunnelDisciplines(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }
	steps := []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	day := 24 * time.Hour

	// u1: signup -> noise -> activate -> checkout  (ordered converts; strict does not)
	// u2: checkout -> activate -> signup           (only unordered converts)
	// u3: signup -> activate -> refund -> checkout (excluded when refund is exclusion)
	// u4: signup -> refund ... then a clean signup -> activate -> checkout (later anchor converts despite exclusion)
	evs := []event.Event{
		{Name: "signup", DistinctID: "u1", Timestamp: at(0)},
		{Name: "$pageview", DistinctID: "u1", Timestamp: at(1)},
		{Name: "activate", DistinctID: "u1", Timestamp: at(2)},
		{Name: "checkout", DistinctID: "u1", Timestamp: at(3)},

		{Name: "checkout", DistinctID: "u2", Timestamp: at(0)},
		{Name: "activate", DistinctID: "u2", Timestamp: at(1)},
		{Name: "signup", DistinctID: "u2", Timestamp: at(2)},

		{Name: "signup", DistinctID: "u3", Timestamp: at(0)},
		{Name: "activate", DistinctID: "u3", Timestamp: at(1)},
		{Name: "refund", DistinctID: "u3", Timestamp: at(2)},
		{Name: "checkout", DistinctID: "u3", Timestamp: at(3)},

		{Name: "signup", DistinctID: "u4", Timestamp: at(0)},
		{Name: "refund", DistinctID: "u4", Timestamp: at(1)},
		{Name: "signup", DistinctID: "u4", Timestamp: at(10)},
		{Name: "activate", DistinctID: "u4", Timestamp: at(11)},
		{Name: "checkout", DistinctID: "u4", Timestamp: at(12)},
	}

	conv := func(opts funnel.Options) int {
		return funnel.ComputeOpts(evs, steps, day, opts).Converted
	}
	if got := conv(funnel.Options{}); got != 3 {
		t.Fatalf("ordered: want u1,u3,u4 = 3 conversions, got %d", got)
	}
	if got := conv(funnel.Options{Order: funnel.Strict}); got != 1 {
		// u1 breaks on the interleaved pageview, u3 breaks on the refund between
		// activate and checkout (ANY intervening event breaks strict) — only u4's
		// clean consecutive second attempt survives
		t.Fatalf("strict: only u4's consecutive run converts; want 1, got %d", got)
	}
	if got := conv(funnel.Options{Order: funnel.Unordered}); got != 4 {
		t.Fatalf("unordered: u2's reversed order must count; want 4, got %d", got)
	}
	if got := conv(funnel.Options{Exclusions: []string{"refund"}}); got != 2 {
		t.Fatalf("exclusion: u3 disqualified, u4's clean second attempt converts; want u1,u4 = 2, got %d", got)
	}
	// per-step filter: only checkouts with plan=pro count as the final step
	evs2 := []event.Event{
		{Name: "signup", DistinctID: "p1", Timestamp: at(0)},
		{Name: "checkout", DistinctID: "p1", Timestamp: at(1), Properties: map[string]any{"plan": "pro"}},
		{Name: "signup", DistinctID: "p2", Timestamp: at(0)},
		{Name: "checkout", DistinctID: "p2", Timestamp: at(1), Properties: map[string]any{"plan": "free"}},
	}
	two := []funnel.Step{{Event: "signup"}, {Event: "checkout"}}
	r := funnel.ComputeOpts(evs2, two, day, funnel.Options{StepFilters: []map[string]string{nil, {"plan": "pro"}}})
	if r.Converted != 1 {
		t.Fatalf("step filter: only the pro checkout converts; want 1, got %d", r.Converted)
	}
	// and the default Compute path is literally ComputeOpts (one engine)
	if a, b := funnel.Compute(evs, steps, day), funnel.ComputeOpts(evs, steps, day, funnel.Options{}); a.Converted != b.Converted || a.Order != b.Order {
		t.Fatalf("Compute must delegate to ComputeOpts identically: %+v vs %+v", a, b)
	}
}

// CONTRACT RETENTION-MODES: "returned on period N" vs rolling "on or after N",
// week bucketing groups cohorts correctly, and an unknown bucket 400s.
func TestContractRetentionModes(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		// alice: first day 0, returns day 2 only
		{Name: "open", DistinctID: "alice", Timestamp: base},
		{Name: "open", DistinctID: "alice", Timestamp: base.AddDate(0, 0, 2)},
	}
	strict := retention.ComputeBucketed(evs, 3, "open", "day", false)
	rolling := retention.ComputeBucketed(evs, 3, "open", "day", true)
	if len(strict.Cohorts) == 0 {
		t.Fatal("no cohorts")
	}
	c, cr := strict.Cohorts[0], rolling.Cohorts[0]
	// strict: day1=0 (didn't return), day2=1
	if c.Returned[1] != 0 || c.Returned[2] != 1 {
		t.Fatalf("returned-on: want day1=0 day2=1, got %v", c.Returned)
	}
	// rolling: day1=1 (active ON OR AFTER day 1 — she came back on day 2)
	if cr.Returned[1] != 1 || cr.Returned[2] != 1 {
		t.Fatalf("on-or-after: want day1=1 day2=1, got %v", cr.Returned)
	}
	// week bucket: day-2 return is the SAME week → period 0, not a later period
	wk := retention.ComputeBucketed(evs, 2, "open", "week", false)
	if wk.Bucket != "week" {
		t.Fatalf("bucket echo: %q", wk.Bucket)
	}

	// unknown bucket must 400 at the API (never silently daily)
	st := memory.New()
	_ = st.Ingest(evs...)
	srv := httptest.NewServer(New(st).Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/v1/retention?bucket=weekly")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bucket=weekly must 400 (it silently meant daily before), got %d", resp.StatusCode)
	}
}

// CONTRACT TRENDS-XAU: each WAU point = distinct users active in the rolling
// 7 days up to and including that day; the total echoes the LAST point.
func TestContractXAU(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := now.Truncate(24 * time.Hour)
	evs := []event.Event{
		{Name: "open", DistinctID: "a", Timestamp: today.AddDate(0, 0, -9).Add(9 * time.Hour)},
		{Name: "open", DistinctID: "b", Timestamp: today.AddDate(0, 0, -2).Add(9 * time.Hour)},
	}
	r := trends.ComputeXAU(evs, "open", today.AddDate(0, 0, -10), today, 7)
	byDay := map[string]int{}
	for _, p := range r.Points {
		byDay[p.Date.Format("2006-01-02")] = p.Count
	}
	d := func(back int) string { return today.AddDate(0, 0, -back).Format("2006-01-02") }
	// 9 days ago: only a active -> 1; 3 days ago: a fell out of the 7d window -> 0... but b arrives day -2
	if byDay[d(9)] != 1 {
		t.Fatalf("day -9 WAU: want 1 (a), got %d", byDay[d(9)])
	}
	if byDay[d(3)] != 1 {
		t.Fatalf("day -3 WAU: a active day -9 is within (d-3 - 7, d-3]; want 1, got %d", byDay[d(3)])
	}
	if byDay[d(2)] != 1 {
		t.Fatalf("day -2 WAU: a expired (day -9 is 7 back), b arrives; want 1, got %d", byDay[d(2)])
	}
	if r.Total != byDay[d(1)] {
		t.Fatalf("total must echo the LAST point (current WAU), got %d want %d", r.Total, byDay[d(1)])
	}
}
