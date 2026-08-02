package api

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// A product the engines never name has no series to rank, and ranking against an absent one
// produced "#4 of 3 brands named" — a position in a list we are not in. Absence is the sharper
// fact, so the card states it.
func TestRankIsAbsentWhenWeAreNeverNamed(t *testing.T) {
	asof := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 6; i++ {
		evs = append(evs, event.Event{
			Name:       "$geo_check",
			DistinctID: "geo",
			Timestamp:  asof.Add(-time.Duration(i+1) * 24 * time.Hour),
			Properties: map[string]any{
				"engine":      "claude",
				"prompt":      "best analytics for indie devs",
				"mentioned":   false,
				"recommended": false,
				"rank":        float64(0),
				"competitors": "PostHog, Plausible, Mixpanel",
				"sentiment":   "absent",
			},
		})
	}
	var vm dashVM
	buildAIVis(&vm, evs, []string{"$geo_check"}, 90, asof, false, "", "")
	t.Logf("checks=%d share=%d rank=%d count=%d", vm.AIVis.Checks, len(vm.AIVis.Share), vm.AIVis.BrandRank, vm.AIVis.BrandCount)
	for _, b := range vm.AIVis.Share {
		t.Logf("  brand=%q us=%v total=%d share=%d", b.Name, b.Us, b.Total, b.SharePct)
	}
	if vm.AIVis.BrandRank > vm.AIVis.BrandCount {
		t.Errorf("IMPOSSIBLE RENDER: #%d of %d brands named", vm.AIVis.BrandRank, vm.AIVis.BrandCount)
	}
}

func TestRankClaimMentionedSometimes(t *testing.T) {
	asof := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 6; i++ {
		evs = append(evs, event.Event{
			Name:       "$geo_check",
			DistinctID: "geo",
			Timestamp:  asof.Add(-time.Duration(i+1) * 24 * time.Hour),
			Properties: map[string]any{
				"engine":      "claude",
				"prompt":      "best analytics for indie devs",
				"mentioned":   i == 0,
				"recommended": false,
				"rank":        float64(0),
				"competitors": "PostHog, Plausible, Mixpanel",
				"sentiment":   "absent",
			},
		})
	}
	var vm dashVM
	buildAIVis(&vm, evs, []string{"$geo_check"}, 90, asof, false, "", "")
	t.Logf("mentioned-once: rank=%d count=%d", vm.AIVis.BrandRank, vm.AIVis.BrandCount)
	if vm.AIVis.BrandRank > vm.AIVis.BrandCount {
		t.Errorf("IMPOSSIBLE RENDER: #%d of %d", vm.AIVis.BrandRank, vm.AIVis.BrandCount)
	}
}
