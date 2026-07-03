package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
			not:  []string{"%", "\x1b"}, // no zero-baseline percentage, no ANSI escapes
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
