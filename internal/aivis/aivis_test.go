package aivis

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

var base = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) // a Monday

func check(engine, prompt string, mentioned, recommended bool, rank float64, competitors, verbatim string, off time.Duration) event.Event {
	return event.Event{
		Name: "$geo_check", DistinctID: "geo-runner", Timestamp: base.Add(off),
		Properties: map[string]any{
			"engine": engine, "prompt": prompt, "mentioned": mentioned, "recommended": recommended,
			"rank": rank, "competitors": competitors, "verbatim": verbatim, "model_version": "m-1",
		},
	}
}

func TestCompute(t *testing.T) {
	evs := []event.Event{
		// claude: 3 runs — mentioned 3x, recommended 2x, ranks 1 and 2
		check("claude", "best analytics for indie devs", true, true, 1, "PostHog, Plausible", "open-source analytics", 0),
		check("claude", "best analytics for indie devs", true, true, 2, "PostHog", "agent-operated analytics", time.Hour),
		check("claude", "what is smolanalytics", true, false, 0, "", "an analytics tool", 2*time.Hour),
		// chatgpt: 1 run, not mentioned
		check("chatgpt", "best analytics for indie devs", false, false, 0, "PostHog, Mixpanel", "", 3*time.Hour),
		// noise: a pageview must not count
		{Name: "$pageview", DistinctID: "v1", Timestamp: base, Properties: map[string]any{"path": "/"}},
	}
	r := Compute(evs, 30, base.Add(24*time.Hour))

	if r.Checks != 4 {
		t.Fatalf("checks = %d, want 4 (pageview excluded)", r.Checks)
	}
	if len(r.Engines) != 2 || r.Engines[0].Engine != "claude" {
		t.Fatalf("engines = %+v, want claude first (most runs)", r.Engines)
	}
	c := r.Engines[0]
	if c.MentionedPct != 100 || c.RecommendedPct != 67 {
		t.Fatalf("claude rates = %d%%/%d%%, want 100/67", c.MentionedPct, c.RecommendedPct)
	}
	if c.AvgRank != 1.5 {
		t.Fatalf("claude avg rank = %v, want 1.5 (ranks 1,2; unranked run excluded)", c.AvgRank)
	}
	// latest verbatim = the newest claude run's
	if c.LatestVerbatim != "an analytics tool" {
		t.Fatalf("latest verbatim = %q", c.LatestVerbatim)
	}
	// PostHog seen in 3 answers, tops competitors
	if len(r.Competitors) == 0 || r.Competitors[0].Name != "PostHog" || r.Competitors[0].Mentions != 3 {
		t.Fatalf("competitors = %+v, want PostHog x3 first", r.Competitors)
	}
	// all four runs land in one week bucket, Monday-keyed
	if len(r.Trend) != 1 || r.Trend[0].WeekStart != "2026-07-20" || r.Trend[0].MentionedPct != 75 {
		t.Fatalf("trend = %+v, want one week 2026-07-20 at 75%% mentioned", r.Trend)
	}
	// 4 runs is thin data: the note must say so
	if r.Note == "" {
		t.Fatal("under 10 runs must carry the low-sample note")
	}
}

func TestComputeEmptyAndWindow(t *testing.T) {
	// outside the window → excluded entirely
	evs := []event.Event{check("claude", "p", true, true, 1, "", "", -40*24*time.Hour)}
	r := Compute(evs, 30, base)
	if r.Checks != 0 || r.Note != "" {
		t.Fatalf("out-of-window check leaked in: %+v", r)
	}
}

func TestRawCountsShipWithRates(t *testing.T) {
	// 3 runs, 2 mentioned, 1 recommended, 2 ranked — the counts must be reported, not
	// left to be re-derived from a rounded percentage downstream.
	evs := []event.Event{
		check("claude", "p", true, true, 1, "", "", 0),
		check("claude", "p", true, false, 2, "", "", time.Hour),
		check("claude", "p", false, false, 0, "", "", 2*time.Hour),
	}
	r := Compute(evs, 30, base.Add(24*time.Hour))
	e := r.Engines[0]
	if e.Runs != 3 || e.Mentioned != 2 || e.Recommended != 1 || e.Ranked != 2 {
		t.Fatalf("counts = runs %d, mentioned %d, recommended %d, ranked %d; want 3/2/1/2", e.Runs, e.Mentioned, e.Recommended, e.Ranked)
	}
	// 2 of 3 rounds to 67% — re-deriving 67% of 3 would give 2.01, which is why the
	// count is carried rather than reconstructed.
	if e.MentionedPct != 67 {
		t.Fatalf("mentioned pct = %d, want 67", e.MentionedPct)
	}
	if r.Prompts[0].Mentioned != 2 || r.Prompts[0].Runs != 3 {
		t.Fatalf("prompt row lost its raw counts: %+v", r.Prompts[0])
	}
}
