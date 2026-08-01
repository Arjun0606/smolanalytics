package brief

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The GEO sampler writes to the same instance the brief reads. Its robot id must not
// appear as a visitor, and its volume must not read as growth — the verdict already
// excludes those events, so the pulse printed above the verdict has to agree.
func TestGeoChecksAreNotProductTraffic(t *testing.T) {
	now := time.Now().UTC()
	base := []event.Event{
		{Name: "$pageview", DistinctID: "u1", Timestamp: now.Add(-2 * time.Hour)},
		{Name: "$pageview", DistinctID: "u2", Timestamp: now.Add(-3 * time.Hour)},
	}
	withGeo := append(append([]event.Event{}, base...), event.Event{
		Name: "$geo_check", DistinctID: "geo-runner", Timestamp: now.Add(-time.Hour),
		Properties: map[string]any{"engine": "claude", "mentioned": true},
	})
	clean, dirty := Build(base, 7, now), Build(withGeo, 7, now)
	if dirty.Events != clean.Events || dirty.Visitors != clean.Visitors {
		t.Errorf("sampler writes leaked into the pulse: %d events/%d visitors vs %d/%d",
			dirty.Events, dirty.Visitors, clean.Events, clean.Visitors)
	}
}
