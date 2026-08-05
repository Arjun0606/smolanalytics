package web

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// pinWall freezes the package's idea of the present for one test. Live is the only number in
// this report that is about NOW rather than about the selected window, so it needs a clock
// the test controls — otherwise the assertion below would be a statement about the machine's
// calendar rather than about the code.
func pinWall(t *testing.T, at time.Time) {
	t.Helper()
	old := wallClock
	wallClock = func() time.Time { return at }
	t.Cleanup(func() { wallClock = old })
}

// A historical range said two people were on the site right now, months after they left:
// liveCutoff was the range END minus five minutes, so "live" was the tail of whatever window
// the operator happened to be looking at. The green pill in the header is a claim about this
// instant and nothing else.
func TestLiveNowIsWallClockNotRangeEnd(t *testing.T) {
	realNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	pinWall(t, realNow)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) // inclusive-end +1d, as the dashboard builds it
	evs := []event.Event{
		pv("j1", "/", "", nil, to.Add(-3*time.Minute)),
		pv("j2", "/pricing", "", nil, to.Add(-2*time.Minute)),
	}
	r := ComputeRange(evs, from, to)
	if r.Visitors != 2 {
		t.Fatalf("visitors=%d, want 2 — the January range itself must still be reported", r.Visitors)
	}
	if r.LiveNow != 0 {
		t.Fatalf("LiveNow=%d for a range that ended %s ago, want 0", r.LiveNow, realNow.Sub(to))
	}
}

// The other half of the same rule: a range that DOES include the present still reports the
// people who are actually here, so the fix cannot be "always zero".
func TestLiveNowCountsCurrentVisitors(t *testing.T) {
	realNow := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	pinWall(t, realNow)

	evs := []event.Event{
		pv("live1", "/", "", nil, realNow.Add(-2*time.Minute)),
		pv("gone", "/", "", nil, realNow.Add(-40*time.Minute)),
	}
	r := ComputeRange(evs, realNow.AddDate(0, 0, -30), realNow.Add(time.Second))
	if r.LiveNow != 1 {
		t.Fatalf("LiveNow=%d, want 1 (only live1 is inside the last five minutes)", r.LiveNow)
	}
}
