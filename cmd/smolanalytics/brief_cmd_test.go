package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The pulse must count distinct visitors and total events per window — [now-N, now)
// vs [now-2N, now-N) — and events older than both windows must count in neither.
func TestBuildBriefPulse(t *testing.T) {
	now := time.Now().UTC()
	st := memory.New()
	ing := func(id, name, user string, ago time.Duration) {
		t.Helper()
		if err := st.Ingest(event.Event{ID: id, Name: name, DistinctID: user, Timestamp: now.Add(-ago)}); err != nil {
			t.Fatal(err)
		}
	}
	// current window: 2 visitors, 3 events (u1 twice — distinct, not double-counted)
	ing("e1", "signup", "u1", 24*time.Hour)
	ing("e2", "open", "u1", 23*time.Hour)
	ing("e3", "signup", "u2", 48*time.Hour)
	// prior window: 1 visitor, 2 events
	ing("e4", "signup", "u3", 9*24*time.Hour)
	ing("e5", "open", "u3", 9*24*time.Hour-time.Minute)
	// older than both windows: in neither pulse count
	ing("e6", "signup", "u4", 20*24*time.Hour)

	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b := buildBrief(evs, 7, now)
	if b.Visitors != 2 || b.Events != 3 {
		t.Errorf("current window: got %d visitors / %d events, want 2 / 3", b.Visitors, b.Events)
	}
	if b.PriorVisitors != 1 || b.PriorEvents != 2 {
		t.Errorf("prior window: got %d visitors / %d events, want 1 / 2", b.PriorVisitors, b.PriorEvents)
	}
	if b.Findings == nil {
		t.Error("findings must be non-nil so JSON emits [] instead of null")
	}
}

// The portfolio block answers "which product moved" — per-site pulse keyed by
// the `site` property the SDK stamps, busiest first, "(no site)" only when
// untagged events are 2%+ of the window, and absent entirely below 2 named
// sites so a single-product brief (text and JSON) is unchanged.
func TestBuildBriefSites(t *testing.T) {
	now := time.Now().UTC()
	type batch struct {
		site, user string
		n          int
		ago        time.Duration
	}
	build := func(batches []batch) []event.Event {
		var evs []event.Event
		for i, ba := range batches {
			for k := 0; k < ba.n; k++ {
				e := event.Event{ID: fmt.Sprintf("e%d-%d", i, k), Name: "open", DistinctID: ba.user, Timestamp: now.Add(-ba.ago)}
				if ba.site != "" {
					e.Properties = map[string]any{"site": ba.site}
				}
				evs = append(evs, e)
			}
		}
		return evs
	}
	cases := []struct {
		name    string
		batches []batch
		want    []siteLine
	}{
		{
			name: "sorted by current events, stray no-site folds under 2%",
			batches: []batch{
				{"pile.app", "u1", 60, 24 * time.Hour},
				{"smolbill.dev", "u2", 38, 24 * time.Hour},
				{"", "u3", 1, 24 * time.Hour}, // 1 of 99 events ≈ 1% — folded
				{"pile.app", "u1", 40, 9 * 24 * time.Hour},
			},
			want: []siteLine{
				{Site: "pile.app", Visitors: 1, Events: 60, PriorVisitors: 1, PriorEvents: 40},
				{Site: "smolbill.dev", Visitors: 1, Events: 38},
			},
		},
		{
			name: "no-site at 2%+ earns its own line",
			batches: []batch{
				{"pile.app", "u1", 3, 24 * time.Hour},
				{"smolbill.dev", "u2", 2, 24 * time.Hour},
				{"", "u3", 1, 24 * time.Hour}, // 1 of 6 events — shown
			},
			want: []siteLine{
				{Site: "pile.app", Visitors: 1, Events: 3},
				{Site: "smolbill.dev", Visitors: 1, Events: 2},
				{Site: "(no site)", Visitors: 1, Events: 1},
			},
		},
		{
			name: "single named site: no section",
			batches: []batch{
				{"pile.app", "u1", 3, 24 * time.Hour},
				{"", "u2", 1, 24 * time.Hour},
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		b := buildBrief(build(tc.batches), 7, now)
		if !reflect.DeepEqual(b.Sites, tc.want) {
			t.Errorf("%s: sites = %+v, want %+v", tc.name, b.Sites, tc.want)
		}
		out, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(out), `"sites"`); got != (tc.want != nil) {
			t.Errorf("%s: JSON sites key present = %v, want %v\n%s", tc.name, got, tc.want != nil, out)
		}
	}
}

// formatBrief must stay plain text (terminal + email + Slack safe), mark warnings
// distinctly, and never invent a percentage against a zero baseline.
func TestFormatBrief(t *testing.T) {
	at := time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		b    brief
		want []string
		not  []string
	}{
		{
			name: "findings and delta",
			b: brief{GeneratedAt: at, Days: 7, Visitors: 2, Events: 3, PriorVisitors: 1, PriorEvents: 2,
				Findings: []insight.Finding{
					{Severity: "warn", Title: "Biggest drop-off: signup → activate", Detail: "only 40% continue."},
					{Severity: "info", Title: "signup is up 100% week-over-week", Detail: "2 vs 1."},
				}},
			want: []string{
				"smolanalytics brief — Fri Jul 3, 2026",
				"2 visitors · 3 events",
				"1 visitor · 2 events",
				"(visitors +100%, events +50%)",
				"What to look at:",
				"⚠ Biggest drop-off: signup → activate — only 40% continue.",
				"• signup is up 100% week-over-week — 2 vs 1.",
			},
		},
		{
			name: "no prior data, no findings",
			b:    brief{GeneratedAt: at, Days: 7, Visitors: 1, Events: 1},
			want: []string{"1 visitor · 1 event", "(no prior data to compare)", "nothing notable"},
			not:  []string{"%", "\x1b", "By product:"}, // no zero-baseline percentage, no ANSI escapes, no portfolio block without sites
		},
		{
			name: "portfolio block: aligned columns, grouped counts, (new) baseline",
			b: brief{GeneratedAt: at, Days: 7, Visitors: 450, Events: 2005, PriorVisitors: 400, PriorEvents: 1445,
				Sites: []siteLine{
					{Site: "pile.app", Visitors: 412, Events: 1893, PriorVisitors: 380, PriorEvents: 1445},
					{Site: "smolbill.dev", Visitors: 38, Events: 112},
				}},
			want: []string{
				"By product:",
				"  pile.app      412 visitors · 1,893 events  (+31%)",
				"  smolbill.dev   38 visitors ·   112 events  (new)",
			},
		},
		{
			name: "portfolio block caps at 12 sites",
			b: brief{GeneratedAt: at, Days: 7, Visitors: 91, Events: 910, PriorVisitors: 13, PriorEvents: 13,
				Sites: manySites(13)},
			want: []string{"s01", "s12", "…and 1 more"},
			not:  []string{"s13"},
		},
	}
	for _, tc := range cases {
		got := formatBrief(tc.b)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: brief missing %q\n%s", tc.name, w, got)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: brief should not contain %q\n%s", tc.name, n, got)
			}
		}
	}
}

// manySites builds n site lines in descending event order for the cap case.
func manySites(n int) []siteLine {
	lines := make([]siteLine, n)
	for i := range lines {
		lines[i] = siteLine{Site: fmt.Sprintf("s%02d", i+1), Visitors: n - i, Events: (n - i) * 10, PriorEvents: 1}
	}
	return lines
}

// --webhook must POST Slack-compatible {"text": ...} JSON, and a non-2xx response
// must come back as an error naming the status (cron surfaces the exit code).
func TestPostBrief(t *testing.T) {
	var got map[string]string
	var ctype string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	defer ok.Close()
	if err := postBrief(ok.URL, "the brief"); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "the brief" {
		t.Errorf(`payload: got %v, want {"text":"the brief"}`, got)
	}
	if ctype != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ctype)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if err := postBrief(bad.URL, "x"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want an error naming the 500 status, got %v", err)
	}
}
