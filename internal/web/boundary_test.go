package web

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The dashboard's prior window ends exactly where its current window begins, so a window with
// two inclusive ends puts an event landing on that boundary in BOTH — every delta on the KPI
// row computed against a prior containing one of the events it is compared to. Visible at one
// nanosecond a month, which is to say never, until it is.
func TestAdjacentWindowsCannotShareAnEvent(t *testing.T) {
	// mirror dashboard.go preset math: day0 = nowT.Truncate(24h), rangeDays=30
	nowT := time.Date(2026, 8, 4, 15, 22, 7, 0, time.UTC)
	day0 := nowT.Truncate(24 * time.Hour)
	rangeDays := 30
	curFrom := day0.AddDate(0, 0, -(rangeDays - 1))
	curTo := nowT
	priorFrom := day0.AddDate(0, 0, -(2*rangeDays - 1))
	priorTo := day0.AddDate(0, 0, -(rangeDays - 1))
	if !priorTo.Equal(curFrom) {
		t.Fatalf("bounds do not share an endpoint")
	}
	evs := []event.Event{
		{Name: "$pageview", DistinctID: "edge", Timestamp: curFrom, Properties: map[string]any{"path": "/"}},
	}
	cur := ComputeRange(evs, curFrom, curTo)
	pri := ComputeRange(evs, priorFrom, priorTo)
	t.Logf("boundary=%s cur.pv=%d cur.vis=%d prior.pv=%d prior.vis=%d", curFrom, cur.Pageviews, cur.Visitors, pri.Pageviews, pri.Visitors)
	if cur.Pageviews == 1 && pri.Pageviews == 1 {
		t.Errorf("DOUBLE COUNTED: the same event is in both the current and prior window")
	}
}
