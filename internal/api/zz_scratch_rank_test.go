package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// Never mentioned, three competitors always named. What does the rank badge say?
func TestScratchRankWhenNeverMentioned(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 12; i++ {
		evs = append(evs, event.Event{
			Name: "$geo_check", DistinctID: "geo-runner",
			Timestamp: now.Add(-time.Duration(i*36) * time.Hour),
			Properties: map[string]any{
				"engine": "claude", "prompt": "best analytics for indie devs",
				"mentioned": false, "recommended": false, "rank": float64(0),
				"competitors":   "PostHog, Plausible, Mixpanel",
				"verbatim":      "",
				"model_version": "claude-test-1",
			},
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")
	i := strings.Index(body, "brands named")
	if i < 0 {
		t.Fatal("no rank badge rendered")
	}
	t.Logf("RANK BADGE CONTEXT: %q", body[i-260:i+40])
}
