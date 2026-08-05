package web

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// ComputeWindow derives its bounds from a duration and an as-of, which is a ROLLING window.
// The dashboard's trend path aligns the same preset to calendar days so the tile, /v1/trends
// and the MCP tool agree — and the two tiles sitting side by side on the KPI row were reading
// windows offset by up to a day at each edge as a result. ComputeRange takes the bounds so
// there can be ONE definition; these pin that it honours them exactly.
func TestComputeRangeHonoursItsBounds(t *testing.T) {
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	pv := func(id string, at time.Time) event.Event {
		return event.Event{Name: "$pageview", DistinctID: id, Timestamp: at, Properties: map[string]any{"path": "/"}}
	}
	evs := []event.Event{
		pv("before", base.Add(-49*time.Hour)),  // outside
		pv("edge-in", base.Add(-48*time.Hour)), // exactly at from — inclusive
		pv("inside", base.Add(-24*time.Hour)),
		pv("edge-out", base),             // exactly at to — inclusive (matches ComputeWindow's !After(asof))
		pv("after", base.Add(time.Hour)), // outside
	}

	// [from, to): the start edge counts, the end edge does not.
	r := ComputeRange(evs, base.Add(-48*time.Hour), base)
	if r.Pageviews != 2 {
		t.Errorf("Pageviews = %d, want 2 (the start edge and the one inside; the end edge belongs to the NEXT window)", r.Pageviews)
	}
	if r.Visitors != 2 {
		t.Errorf("Visitors = %d, want 2", r.Visitors)
	}

	// A window with pageviews in it can never report zero visitors — that contradiction is
	// what made one KPI tile show "71x vs prior" while the tile beside it showed nothing.
	if r.Pageviews > 0 && r.Visitors == 0 {
		t.Error("pageviews without visitors: the two are counted from the same events and cannot disagree")
	}
}

// ComputeWindow is now a thin wrapper. If the two ever compute different things, every caller
// that migrated to ComputeRange silently changes meaning.
func TestComputeWindowIsComputeRange(t *testing.T) {
	asof := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 30; i++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: string(rune('a' + i%7)),
			Timestamp:  asof.Add(-time.Duration(i*8) * time.Hour),
			Properties: map[string]any{"path": "/p"},
		})
	}
	win := 10 * 24 * time.Hour
	a := ComputeWindow(evs, win, asof)
	b := ComputeRange(evs, asof.Add(-win), asof)
	if a.Pageviews != b.Pageviews || a.Visitors != b.Visitors || a.PeriodDays != b.PeriodDays {
		t.Errorf("ComputeWindow and ComputeRange disagree:\n window: pv=%d vis=%d days=%d\n range:  pv=%d vis=%d days=%d",
			a.Pageviews, a.Visitors, a.PeriodDays, b.Pageviews, b.Visitors, b.PeriodDays)
	}
}

// A from that is not before to is a caller bug; falling through to a zero-width window would
// report an empty site rather than failing loudly, so it clamps to the documented default.
func TestComputeRangeSurvivesAnInvertedRange(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{{Name: "$pageview", DistinctID: "u", Timestamp: now.Add(-2 * time.Hour), Properties: map[string]any{"path": "/"}}}
	if r := ComputeRange(evs, now, now.Add(-time.Hour)); r.Pageviews != 1 {
		t.Errorf("inverted range reported %d pageviews, want the 30-day fallback to find 1", r.Pageviews)
	}
}
