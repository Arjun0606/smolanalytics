package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// TWO DEFINITIONS OF "THE LAST 30 DAYS", AND THEY DISAGREED IN PRODUCTION.
//
// web.Compute windowed by a rolling N×24h; everything else — trends, funnels, retention, rows,
// and the dashboard's own tiles — windowed by N calendar days ending today. The rolling form
// reaches back a further (24h − time-of-day), about ten hours at midday.
//
// Measured on the live demo at the same instant, same days=30, same event:
//
//	/v1/web   →  1,779 visitors, 2,371 pageviews
//	/v1/rows  →  1,753 people,   2,336 pageview rows
//
// Same question, two answers, on the product whose footer promises "ask == dashboard == MCP, all
// from one report engine". /v1/web, the MCP web_overview tool and the public share page all used
// the rolling form while the dashboard beside them used the calendar form.
//
// It went unnoticed because both numbers are individually plausible — nothing renders wrong, it
// just quietly isn't the same number. That is the hardest kind of defect to catch by looking, and
// the only reliable guard is an equality test between the surfaces.
func TestWebAndRowsCountTheSameWindow(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	// Traffic every hour for 40 days. Hourly matters: the two window definitions differ only by a
	// part-day at the far edge, so a fixture with one event per day can straddle the gap and pass
	// against the bug.
	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < 40; d++ {
		for h := 0; h < 24; h++ {
			ts := now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			batch = append(batch, map[string]any{
				"name": "$pageview", "distinct_id": fmt.Sprintf("d%dh%d", d, h),
				"timestamp":  ts.Format(time.RFC3339),
				"properties": map[string]any{"path": "/"},
			})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	code := resp.StatusCode
	resp.Body.Close()
	if code >= 300 {
		t.Fatalf("seeding failed with %d — the fixture never landed", code)
	}

	for _, days := range []int{7, 14, 30} {
		var wv struct {
			Visitors   int `json:"visitors"`
			Pageviews  int `json:"pageviews"`
			PeriodDays int `json:"period_days"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/web?days=%d", days)), &wv)

		var rows struct {
			Total int `json:"total"`
			Users int `json:"users"`
		}
		mustJSON(t, getBody(t, srv,
			fmt.Sprintf("/v1/rows?event=%%24pageview&days=%d&unique=1&limit=1", days)), &rows)

		if wv.Pageviews != rows.Total {
			t.Errorf("days=%d: /v1/web counts %d pageviews, /v1/rows counts %d — two windows for "+
				"one question", days, wv.Pageviews, rows.Total)
		}
		if wv.Visitors != rows.Users {
			t.Errorf("days=%d: /v1/web counts %d visitors, /v1/rows recomputes %d people — a proof "+
				"link on this number would contradict the number it proves",
				days, wv.Visitors, rows.Users)
		}
		// And the caption must say what was asked for. A calendar window is (N-1) days plus
		// today-so-far, which truncating reported as N-1 — the range control disagreeing with the
		// label beneath it.
		if wv.PeriodDays != days {
			t.Errorf("days=%d: the report says it covers %d days", days, wv.PeriodDays)
		}
	}
}

// The same window rule has to reach the MCP tool, or "ask == dashboard == MCP" holds for two of
// the three. web_overview reads through the same web.Compute, so this pins the shared entry point
// rather than the transport.
func TestTrendsAndRowsCountTheSameWindow(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < 40; d++ {
		for h := 0; h < 24; h += 3 {
			ts := now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			batch = append(batch, map[string]any{
				"name": "signup", "distinct_id": fmt.Sprintf("u%d_%d", d, h),
				"timestamp": ts.Format(time.RFC3339),
			})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	for _, days := range []int{7, 30} {
		var tr struct {
			Total int `json:"total"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/trends?event=signup&days=%d", days)), &tr)
		var rows struct {
			Total int `json:"total"`
		}
		mustJSON(t, getBody(t, srv, fmt.Sprintf("/v1/rows?event=signup&days=%d&limit=1", days)), &rows)
		if tr.Total != rows.Total {
			t.Errorf("days=%d: trends says %d, rows says %d", days, tr.Total, rows.Total)
		}
	}
}
