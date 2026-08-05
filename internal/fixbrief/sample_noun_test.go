package fixbrief

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
)

// The small-sample caveat called every finding's N "people". For four of the seven kinds it is
// not: an anomaly and a trend count EVENTS, a crawl finding counts bot fetches, an
// AI-visibility finding counts sampled model answers. The caveat is embedded verbatim in
// b.Prompt, the block whose entire job is to be pasted into a coding agent as ground truth, so
// "a percentage over 40 people" hands an agent a population that does not exist.
func TestSampleCaveatNamesWhatNActuallyCounts(t *testing.T) {
	for _, tc := range []struct {
		kind, want string
	}{
		{insight.KindAnomaly, "over 40 events"},
		{insight.KindTrend, "over 40 events"},
		{insight.KindCrawl, "over 40 crawler fetches"},
		{insight.KindAIVis, "over 40 sampled model answers"},
		{insight.KindDropoff, "over 40 people"},
		{insight.KindSegment, "over 40 people"},
		{insight.KindRetention, "over 40 people"},
	} {
		got := lowN(insight.Finding{Kind: tc.kind, N: 40})
		if !strings.Contains(got, tc.want) {
			t.Errorf("kind %q: caveat should read %q\n got: %s", tc.kind, tc.want, got)
		}
	}
}

// End to end through the real brief: one person firing 25 events must never be described to an
// agent as 25 people.
func TestTrendBriefDoesNotTurnEventsIntoPeople(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 25; i++ { // the prior week: 25 events, ONE user
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("p%d", i), DistinctID: "solo", Name: "signup",
			Timestamp: now.Add(-time.Duration(8*24+i) * time.Hour),
		})
	}
	for i := 0; i < 5; i++ { // the last week
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("r%d", i), DistinctID: "solo", Name: "signup",
			Timestamp: now.Add(-time.Duration(24+i) * time.Hour),
		})
	}

	var b Brief
	found := false
	for _, x := range ComputeAll(evs, nil, now) {
		if x.Finding.Kind == insight.KindTrend {
			b, found = x, true
		}
	}
	if !found {
		t.Fatal("expected a week-over-week finding on a 25 → 5 collapse")
	}
	if b.Finding.N != 25 {
		t.Fatalf("fixture moved: trend N is %d, want the 25 prior-week events", b.Finding.N)
	}
	if strings.Contains(b.Note, "25 people") || strings.Contains(b.Prompt, "25 people") {
		t.Errorf("one user's 25 events are described as 25 people:\n%s", b.Note)
	}
	if !strings.Contains(b.Note, "25 events") {
		t.Errorf("caveat does not name the unit: %q", b.Note)
	}
}
