package aivis

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// runs writes n sampled checks spread across [from, from+span), with the given hit rates.
func runs(from time.Time, span time.Duration, n, mentioned, recommended int) []event.Event {
	var evs []event.Event
	step := span / time.Duration(n)
	for i := 0; i < n; i++ {
		evs = append(evs, event.Event{
			Name: CheckEvent, DistinctID: "geo-runner", Timestamp: from.Add(time.Duration(i) * step),
			Properties: map[string]any{
				"engine": "claude", "prompt": "best analytics for indie devs",
				"mentioned": i < mentioned, "recommended": i < recommended,
			},
		})
	}
	return evs
}

func shipped(at time.Time, sha, msg string) deploys.Deploy {
	return deploys.Deploy{ID: sha, SHA: sha, Message: msg, At: at}
}

// The headline claim: a release that moved the recommendation rate is named, with the
// commit, and with the run counts that make the claim checkable.
func TestByDeployNamesTheShipThatMovedIt(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	at := now.Add(-2 * week)

	var evs []event.Event
	evs = append(evs, runs(at.Add(-week), week, 30, 9, 3)...) // before: 30% / 10%
	evs = append(evs, runs(at, week, 30, 27, 21)...)          // after:  90% / 70%
	deps := []deploys.Deploy{shipped(at, "abc1234def", "add comparison pages")}

	got := ByDeploy(evs, deps, 7, now, "")
	if len(got) != 1 {
		t.Fatalf("expected one judged deploy, got %d", len(got))
	}
	s := got[0]
	if !s.Significant || s.Direction != "improvement" {
		t.Fatalf("a 60-point recommendation jump on 30 runs a side should read as an improvement: %+v", s)
	}
	if s.ShortSHA != "abc1234" {
		t.Errorf("short sha = %q", s.ShortSHA)
	}
	if s.RunsBefore != 30 || s.RunsAfter != 30 {
		t.Errorf("run counts must ship with the rates: before=%d after=%d", s.RunsBefore, s.RunsAfter)
	}
	if s.RecommendedBefore != 10 || s.RecommendedAfter != 70 || s.RecommendedDelta != 60 {
		t.Errorf("recommendation rates wrong: %d -> %d (delta %d)", s.RecommendedBefore, s.RecommendedAfter, s.RecommendedDelta)
	}
}

// The rule that keeps this honest. The same 60-point swing on a handful of runs is what
// sampling noise looks like, and calling it a win is the exact dishonesty this module
// exists to avoid.
func TestByDeployRefusesToJudgeThinSamples(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	at := now.Add(-2 * week)

	var evs []event.Event
	evs = append(evs, runs(at.Add(-week), week, 6, 1, 0)...)
	evs = append(evs, runs(at, week, 6, 6, 5)...)
	got := ByDeploy(evs, []deploys.Deploy{shipped(at, "abc1234def", "tweak copy")}, 7, now, "")

	if len(got) != 1 {
		t.Fatalf("the deploy should still be listed, just not judged: %d rows", len(got))
	}
	if got[0].Significant {
		t.Fatal("6 runs a side is not evidence and must never be called significant")
	}
	if got[0].Direction != "unknown" || got[0].Why == "" {
		t.Errorf("an unjudgeable deploy must say WHY, not just go quiet: %+v", got[0])
	}
}

// "nothing moved" and "we cannot tell yet" are different sentences and must not collapse
// into one another.
func TestFlatIsDistinctFromUnknown(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	at := now.Add(-2 * week)

	var evs []event.Event
	evs = append(evs, runs(at.Add(-week), week, 40, 20, 12)...) // 50% / 30%
	evs = append(evs, runs(at, week, 40, 21, 13)...)            // 52% / 32%
	got := ByDeploy(evs, []deploys.Deploy{shipped(at, "abc1234def", "refactor")}, 7, now, "")

	if len(got) != 1 || got[0].Significant {
		t.Fatalf("a 2-point move on 40 runs is flat, not significant: %+v", got)
	}
	if got[0].Direction != "flat" {
		t.Errorf("with enough runs and no movement the answer is flat, not unknown: %q", got[0].Direction)
	}
}

// A deploy from this morning has no window after it. Showing it with a hopeful zero would
// read as "your release did nothing".
func TestTooRecentToJudgeIsOmitted(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	evs := runs(now.Add(-2*week), 2*week, 60, 30, 20)
	got := ByDeploy(evs, []deploys.Deploy{shipped(now.Add(-2*time.Hour), "abc1234def", "just shipped")}, 7, now, "")
	if len(got) != 0 {
		t.Fatalf("a deploy without a full window after it must not be judged: %+v", got)
	}
}

// Crawl activity is counted across the same boundary, because a release that adds content
// shows up there first: something has to fetch the page before any model can name it.
func TestCrawlActivityIsCountedAcrossTheSameBoundary(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	at := now.Add(-2 * week)

	evs := append([]event.Event{}, runs(at.Add(-week), week, 25, 5, 2)...)
	evs = append(evs, runs(at, week, 25, 20, 15)...)
	for i := 0; i < 9; i++ { // all after the ship
		evs = append(evs, event.Event{Name: "$ai_crawl", DistinctID: "$crawler",
			Timestamp: at.Add(time.Duration(i+1) * time.Hour)})
	}
	got := ByDeploy(evs, []deploys.Deploy{shipped(at, "abc1234def", "add docs")}, 7, now, "$ai_crawl")
	if len(got) != 1 {
		t.Fatalf("rows: %d", len(got))
	}
	if got[0].CrawlBefore != 0 || got[0].CrawlAfter != 9 {
		t.Errorf("crawl split wrong: before=%d after=%d", got[0].CrawlBefore, got[0].CrawlAfter)
	}
	// and passing no crawl event name must not silently count checks as crawls
	none := ByDeploy(evs, []deploys.Deploy{shipped(at, "abc1234def", "add docs")}, 7, now, "")
	if none[0].CrawlAfter != 0 {
		t.Errorf("crawl counting must be opt-in by event name, got %d", none[0].CrawlAfter)
	}
}

// A deploy nothing was sampled around says nothing, rather than reporting 0% -> 0%.
func TestUnsampledDeployIsSkipped(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	// checks exist, but nowhere near this deploy
	evs := runs(now.Add(-3*week), week, 30, 15, 10)
	got := ByDeploy(evs, []deploys.Deploy{shipped(now.Add(-10*week), "abc1234def", "old ship")}, 7, now, "")
	if len(got) != 0 {
		t.Fatalf("a deploy with no sampling either side must be omitted: %+v", got)
	}
}
