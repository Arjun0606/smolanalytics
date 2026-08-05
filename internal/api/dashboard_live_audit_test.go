package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// Everything pinned here was found by opening a real instance in a browser at 1440px and at
// 390px and looking at it. Not one of them failed a test, and several were invisible in the
// source — the mobile overflow came from a table JavaScript writes, and the polling only shows
// up in a network panel. They are pinned at whatever level is actually checkable.

// The KPI row read "Visitors · 10d" beside "$pageview · 10d" and the two measured DIFFERENT
// ten-day windows: the web block derived its own rolling bounds from a duration while the trend
// beside it used the calendar-aligned window that /v1/trends and the MCP tool use. On a
// ten-day-old instance that was the difference between a prior window with data and one
// without — "71x vs prior" on one tile, no delta at all on its neighbour.
func TestKPIRowUsesOneWindowDefinition(t *testing.T) {
	src := dashboardGoSource(t)
	if strings.Contains(src, "web.ComputeWindow(evs, winDur") {
		t.Error("the web tiles derive their own rolling window again — they must take the same " +
			"curFrom/curTo the trend tile and /v1/trends use, or the row lies about its own label")
	}
	for _, want := range []string{
		"web.ComputeRange(evs, curFrom, curTo)",
		"web.ComputeRange(evs, priorFrom, priorTo)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q — the KPI row is back to two window definitions", want)
		}
	}
}

// A bucket from before the instance recorded its first event is UNMEASURED, not zero. The
// prior column printed a hard 0 for every one of them, so a ten-day-old install read as a site
// with a fortnight of zero traffic behind it.
func TestPriorBucketsBeforeTheFirstEventAreUnknown(t *testing.T) {
	first := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	day := func(d int) trends.Point {
		return trends.Point{Date: time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC)}
	}
	pts := []trends.Point{day(24), day(25), day(26), day(27)}

	// Jul 24 and Jul 25 both END (at the next bucket's start) before the first event.
	for _, i := range []int{0, 1} {
		if !bucketPredatesData(pts, i, trends.Day, first) {
			t.Errorf("bucket %s should be reported as unmeasured — it ends before the first event at %s",
				pts[i].Date.Format("Jan 2"), first.Format("Jan 2 15:04"))
		}
	}
	// Jul 26 CONTAINS the first event, so 0 there would be a real zero.
	for _, i := range []int{2, 3} {
		if bucketPredatesData(pts, i, trends.Day, first) {
			t.Errorf("bucket %s contains or follows the first event and must report a real number",
				pts[i].Date.Format("Jan 2"))
		}
	}
	// No events at all: nothing is known to predate anything, so nothing is suppressed.
	if bucketPredatesData(pts, 0, trends.Day, time.Time{}) {
		t.Error("with no first event the column must not start hiding buckets")
	}

	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, `class="nodata"`) {
		t.Error("the prior column no longer distinguishes unmeasured from zero on screen")
	}
}

// A clock-skewed client must not be able to move the start of history backwards or forwards.
func TestFirstEventTimeIgnoresFutureAndZeroStamps(t *testing.T) {
	now := time.Now().UTC()
	real := now.Add(-48 * time.Hour)
	evs := seedForFirstEvent(now, real)
	got := firstEventTime(evs)
	if !got.Equal(real) {
		t.Errorf("firstEventTime = %v, want %v (future-dated and zero stamps must be ignored)", got, real)
	}
	if firstEventTime(nil).IsZero() != true {
		t.Error("no events should yield the zero time, not a fabricated start of history")
	}
}

// Every pane contains its own overflow. This was an allowlist of five id rules, #pane-heatmap
// was never on it, and the click-target table it writes in JavaScript pushed the whole DOCUMENT
// 37px past the viewport at 390px — the entire page side-scrolling because of a table three
// screens down.
func TestPanesContainTheirOwnOverflow(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, "padding:var(--card-pad);min-width:0;overflow-x:auto") {
		t.Error("`.deck .pane` no longer contains its overflow by default; the next wide table " +
			"added to any pane will drag the document wider than the phone it is read on")
	}
	// the per-id rules the default replaced must not creep back — an allowlist is exactly how
	// #pane-heatmap got missed the first time
	for _, gone := range []string{
		".deck #pane-retention{overflow-x:auto}",
		".deck #pane-agent{overflow-x:auto}",
		".deck #pane-sessions{overflow-x:auto}",
	} {
		if strings.Contains(tpl, gone) {
			t.Errorf("%s is back — overflow is a pane default now, not a list somebody has to remember to extend", gone)
		}
	}
}

// Four bare setIntervals polled forever, including the live feed every 4 seconds, which
// recomputes from the raw event log on every call. A dashboard is a tab people leave open for
// days; a minimised one is not a free request.
func TestEveryPollerRespectsTabVisibility(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, "function saPoll(") {
		t.Fatal("saPoll is gone — the pollers have no visibility guard again")
	}
	if !strings.Contains(tpl, "if(!document.hidden) fn()") {
		t.Error("saPoll no longer skips its tick while the document is hidden")
	}
	if !strings.Contains(tpl, "visibilitychange") {
		t.Error("saPoll no longer refreshes on the way back — returning to the tab would show a stale number")
	}
	// script-side setInterval must only ever appear inside saPoll itself
	body := tpl[strings.Index(tpl, "<script>"):]
	if n := strings.Count(body, "setInterval("); n != 1 {
		t.Errorf("%d setInterval( calls in the page script, want exactly 1 (the one inside saPoll) — "+
			"a bare interval polls a backgrounded tab forever", n)
	}
}

// Two functions each fetched /v1/cohorts for the same list, and one called the other from
// inside its own .then, so a cold load made three identical requests.
func TestCohortsAreFetchedOnce(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	body := tpl[strings.Index(tpl, "<script>"):]
	if n := strings.Count(body, "fetch('/v1/cohorts')"); n != 1 {
		t.Errorf("%d GET fetches of /v1/cohorts in the script, want exactly 1 — "+
			"one fetch, both consumers", n)
	}
	if !strings.Contains(body, "function fillCohortSelect(") {
		t.Error("the cohort select filler fetches again instead of being handed the list")
	}
}

// The tile labels carry the operator's own event names. `text-transform:capitalize` does not
// know an identifier from a word, so "$pageview" rendered as "$Pageview" and the conversion
// tile read "$Pageview → $Click" — strings that appear nowhere in their tracking code, their
// filters, the API or their MCP arguments.
func TestTileLabelsDoNotRewriteEventNames(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	i := strings.Index(tpl, ".kpi .kk{")
	if i < 0 {
		t.Fatal(".kpi .kk rule not found")
	}
	if rule := tpl[i : i+260]; strings.Contains(rule, "text-transform:capitalize") {
		t.Error("the KPI label is capitalising again — it carries raw event names, which are identifiers")
	}
}

// The fix brief stamped the identical reason on all five rows of "where it shows up", so the
// sheet printed one long sentence five times in a column meant to be read as paths and numbers.
func TestFixBriefSaysTheSharedReasonOnce(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if !strings.Contains(tpl, `class="fxwhy"`) {
		t.Error("the section-level reason is gone; the where-it-shows-up rows repeat it per row again")
	}
	if !strings.Contains(tpl, "oneWhy") {
		t.Error("the shared-reason collapse is gone from the brief renderer")
	}
}

// helpers ------------------------------------------------------------------------------

func dashboardGoSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("dashboard.go")
	if err != nil {
		t.Fatalf("read dashboard.go: %v", err)
	}
	return string(src)
}

// seedForFirstEvent returns a slice whose earliest REAL timestamp is `real`, salted with the
// two kinds of stamp that must not count: a zero time and one dated into the future.
func seedForFirstEvent(now, real time.Time) []event.Event {
	return []event.Event{
		{Name: "signup", DistinctID: "future", Timestamp: now.Add(72 * time.Hour)},
		{Name: "signup", DistinctID: "zero"},
		{Name: "signup", DistinctID: "real", Timestamp: real},
		{Name: "signup", DistinctID: "later", Timestamp: now.Add(-time.Hour)},
	}
}
