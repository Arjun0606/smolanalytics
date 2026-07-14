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
