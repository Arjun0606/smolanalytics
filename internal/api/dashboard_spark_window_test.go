package api

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// A sparkline is the picture OF the number above it. These helpers were anchored on
// time.Now() and clamped to 30 points, so the picture and the number could be months and
// weeks apart: ?from=2026-01-01&to=2026-01-07 drew a flat August zero-line under
// "Visitors · 7d 412", and ?days=90 drew the trailing 30 days under a 90-day figure.

func TestSeriesWindowEndsWhereTheReportedWindowEnds(t *testing.T) {
	// the window is [Jan 1, Jan 8) — seven days, as the custom-range parser computes it.
	end := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	n, size, from := seriesWindow(7, end)
	if n != 7 || size != 1 {
		t.Fatalf("7-day window → %d buckets of %d days, want 7 of 1", n, size)
	}
	if want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Errorf("series starts %s, want %s — the sparkline covers a different week than the tile", from, want)
	}
}

func TestSeriesWindowBucketsRatherThanTruncatesLongWindows(t *testing.T) {
	end := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	n, size, from := seriesWindow(90, end)
	if n*size < 90 {
		t.Fatalf("90-day window covered by %d buckets × %d days = %d days", n, size, n*size)
	}
	if n > 30 {
		t.Fatalf("%d points for a 90-day window — the cap that keeps a sparkline readable is 30", n)
	}
	covered := int(end.Truncate(24*time.Hour).Sub(from).Hours()/24) + 1
	t.Logf("90d → %d buckets × %dd covering %d days back from %s", n, size, covered, end.Format("Jan 2"))
	if covered < 90 {
		t.Errorf("the sparkline under a 90d tile covers only %d days", covered)
	}
}

// The end-to-end statement: a custom January window must not draw an August line.
func TestDailySeriesDrawsTheWindowNotToday(t *testing.T) {
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var evs []event.Event
	for d := 0; d < 7; d++ {
		for i := 0; i < 3; i++ {
			evs = append(evs, event.Event{
				Name: "$pageview", DistinctID: fmt.Sprintf("u%d-%d", d, i),
				Timestamp: jan.AddDate(0, 0, d).Add(time.Duration(i) * time.Hour),
			})
		}
	}
	end := jan.AddDate(0, 0, 7).Add(-time.Nanosecond) // the last instant of [Jan 1, Jan 8)
	got := dailySeries(evs, func(e event.Event) bool { return e.Name == "$pageview" }, 7, end, true)
	t.Logf("series over the January window: %v", got)
	if len(got) != 7 {
		t.Fatalf("want 7 points, got %d", len(got))
	}
	for i, v := range got {
		if v != 3 {
			t.Errorf("day %d of the reported window drew %d visitors, want 3 — the line is not over the window the tile reports", i, v)
		}
	}

	// cumulative + engagement follow the same window, so all three tiles' lines agree.
	cum := cumulativeUserSeries(evs, 7, end)
	if len(cum) != 7 || cum[len(cum)-1] == 0 {
		t.Errorf("cumulative series %v does not end inside the January window", cum)
	}
	bounce, _ := engagementSeries(evs, 7, end)
	nonzero := false
	for _, b := range bounce {
		if b > 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Errorf("engagement series %v is flat zero over a week that had pageviews every day", bounce)
	}
}

// The same defect through the real page, because the call site is where it lived: buildKPIs
// was handed time.Now() while the tiles were computed over the requested range, so a custom
// window rendered a flat present-day zero-line under a figure from the past.
func TestCustomRangeSparkIsDrawnOverThatRange(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC().Truncate(24 * time.Hour)
	start := now.AddDate(0, 0, -200)
	var evs []event.Event
	n := 0
	for d := 0; d < 7; d++ { // a rising ramp, so a spark over this week cannot be flat
		for i := 0; i <= d; i++ {
			n++
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("e%d", n), Name: "$pageview", DistinctID: fmt.Sprintf("u%d", n),
				Timestamp:  start.AddDate(0, 0, d).Add(time.Duration(i) * time.Hour),
				Properties: map[string]any{"path": "/"},
			})
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	url := fmt.Sprintf("/?from=%s&to=%s", start.Format("2006-01-02"), start.AddDate(0, 0, 6).Format("2006-01-02"))
	page := renderWindowDash(t, st, url)
	// Located by the KPI's label. It read "Visitors ·" until the page settled on ONE noun for a
	// person — it had been using visitors, users, people and accounts for the same thing across
	// five different totals. Checked rather than sliced blind: a missing label used to return -1
	// and panic with "slice bounds out of range", which says nothing about what actually changed.
	at := strings.Index(page, "People ·")
	if at < 0 {
		t.Fatalf("no People KPI card in the rendered page — the tile label moved again")
	}
	card := page[at:]
	if j := strings.Index(card, "</div></div>"); j > 0 {
		card = card[:j]
	}
	m := regexp.MustCompile(`<polyline pathLength="1" points="([^"]+)"`).FindStringSubmatch(card)
	if m == nil {
		t.Fatalf("no sparkline under the Visitors tile for %s:\n%s", url, card)
	}
	ys := map[string]bool{}
	for _, p := range strings.Fields(m[1]) {
		ys[p[strings.Index(p, ",")+1:]] = true
	}
	t.Logf("%s → spark %s", url, m[1])
	if len(ys) < 2 {
		t.Errorf("the sparkline under a %s tile is a flat line — it was drawn over today, not over the window the number came from", url)
	}
}

// A 90-day tile with a 30-day line under it is two different claims about one window.
func TestNinetyDaySparkCoversTheWholeNinetyDays(t *testing.T) {
	end := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var evs []event.Event
	// traffic ONLY in the oldest month of the window — invisible to a 30-day-truncated series
	for d := 60; d < 90; d++ {
		evs = append(evs, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("u%d", d),
			Timestamp: end.AddDate(0, 0, -d),
		})
	}
	got := dailySeries(evs, func(e event.Event) bool { return e.Name == "$pageview" }, 90, end, true)
	sum := 0
	for _, v := range got {
		sum += v
	}
	t.Logf("90d series (%d points): %v", len(got), got)
	if sum != 30 {
		t.Errorf("the 90-day sparkline shows %d of the 30 visitors inside its own window", sum)
	}
}
