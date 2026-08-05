package api

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// renderDash runs the real handler and returns the page. Every assertion below is made
// against rendered HTML rather than a view model, because a number that is right in Go and
// wrong on screen is still wrong on screen.
func renderWindowDash(t *testing.T, st *memory.Store, url string) string {
	t.Helper()
	s := New(st)
	w := httptest.NewRecorder()
	s.dashboard(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 200 {
		t.Fatalf("GET %s → %d", url, w.Code)
	}
	return w.Body.String()
}

// paneHTML slices out one pane by its id so a match cannot come from somewhere else on a
// 4000-line page.
func paneHTML(t *testing.T, page, id string) string {
	t.Helper()
	i := strings.Index(page, `id="`+id+`"`)
	if i < 0 {
		return ""
	}
	rest := page[i:]
	if j := strings.Index(rest, `<div class="pane`); j > 0 {
		rest = rest[:j]
	}
	return rest
}

var segNumRe = regexp.MustCompile(`<span class="segname"[^>]*>(?:<span[^>]*>[^<]*</span>)?([^<]*)</span><span class="segnums">([\d,]+)(?: · (\d+)%)?</span>`)

type segReading struct {
	name  string
	count int
	pct   int
}

func readSegs(html string) []segReading {
	var out []segReading
	for _, m := range segNumRe.FindAllStringSubmatch(html, -1) {
		n, _ := strconv.Atoi(strings.ReplaceAll(m[2], ",", ""))
		p, _ := strconv.Atoi(m[3])
		out = append(out, segReading{name: m[1], count: n, pct: p})
	}
	return out
}

// The "<event> by channel" pane sits under a KPI tile labelled with the selected range, on a
// page whose provenance line names that range. It counted converters over ALL history: on an
// instance with a year of data the tile read "signup · 7d — 12" while the pane under it read
// "direct 365", and moving the range control changed nothing in the pane.
func TestByChannelPaneFollowsTheSelectedRange(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 120; i++ { // one signup per day, a distinct user each day, four months back
		id := fmt.Sprintf("u%d", i)
		ts := now.Add(-time.Duration(i)*24*time.Hour - 2*time.Hour)
		evs = append(evs,
			event.Event{ID: "pv" + id, Name: "$pageview", DistinctID: id, Timestamp: ts.Add(-time.Minute),
				Properties: map[string]any{"path": "/", "referrer": "https://news.ycombinator.com/"}},
			event.Event{ID: "sg" + id, Name: "signup", DistinctID: id, Timestamp: ts},
		)
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	total := func(url string) int {
		rows := readSegs(paneHTML(t, renderWindowDash(t, st, url), "pane-sources"))
		n := 0
		for _, r := range rows {
			n += r.count
		}
		return n
	}
	seven, ninety := total("/?days=7"), total("/?days=90")
	t.Logf("by-channel pane: 7d=%d 90d=%d (120 days of one signup/day)", seven, ninety)
	if seven > 8 {
		t.Errorf("the 7d pane counted %d converters — it is reading outside the window it is labelled with", seven)
	}
	if ninety <= seven {
		t.Errorf("the range control does not move the pane at all: 7d=%d, 90d=%d", seven, ninety)
	}
}

// The geo card's percentages divided by the sum of the ten rows it could see. rank() had
// already thrown the tail away, so a 20% share printed as 52% and the visible rows always
// summed to exactly 100% — asserting a long-tailed site has no tail.
func TestCountryShareDividesByRecordedNotByVisibleRows(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	add := func(id, cc string) {
		evs = append(evs, event.Event{
			ID: id, Name: "$pageview", DistinctID: id, Timestamp: now.Add(-2 * time.Hour),
			Properties: map[string]any{"path": "/", "country": cc},
		})
	}
	for i := 0; i < 20; i++ {
		add(fmt.Sprintf("us%d", i), "US")
	}
	// 40 more countries at 2 visitors each: the top-10 rows cover 38 of 100 visitors.
	for c := 0; c < 40; c++ {
		cc := string(rune('A'+c/26)) + string(rune('A'+c%26))
		for i := 0; i < 2; i++ {
			add(fmt.Sprintf("%s%d", cc, i), cc)
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	rows := readSegs(paneHTML(t, renderWindowDash(t, st, "/?days=1"), "pane-geo"))
	if len(rows) == 0 {
		t.Fatal("geo pane rendered no rows")
	}
	sum := 0
	for _, r := range rows {
		sum += r.pct
		t.Logf("  %s %d · %d%%", r.name, r.count, r.pct)
	}
	if rows[0].name != "US" || rows[0].count != 20 {
		t.Fatalf("expected US 20 as the top row, got %+v", rows[0])
	}
	if rows[0].pct != 20 {
		t.Errorf("US is 20 of 100 recorded visitors = 20%%, rendered %d%% — the denominator is the truncated top-10, not what was recorded", rows[0].pct)
	}
	if sum > 60 {
		t.Errorf("ten visible rows sum to %d%% of a hundred-visitor site with a 40-country tail — the tail has been asserted out of existence", sum)
	}
}

// A quiet window is not an uninstalled SDK. Picking a short range on a site with months of
// pageviews used to render "this instance has events but no $pageview yet … drop
// <script src=…/sdk.js> into your app" and silently drop the Visitors KPI card.
func TestQuietWindowDoesNotClaimTheSDKIsMissing(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 300; i++ { // pageviews from 30h ago and older
		id := fmt.Sprintf("v%d", i)
		evs = append(evs, event.Event{
			ID: "pv" + id, Name: "$pageview", DistinctID: id,
			Timestamp:  now.Add(-30*time.Hour - time.Duration(i)*time.Hour),
			Properties: map[string]any{"path": "/", "site": "example.com"},
		})
	}
	// one recent non-pageview event so the page has something in the window
	evs = append(evs, event.Event{ID: "s1", Name: "signup", DistinctID: "v1", Timestamp: now.Add(-time.Hour)})
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	page := renderWindowDash(t, st, "/?hours=6")
	if strings.Contains(page, "no <b>$pageview</b> yet") {
		t.Error("a six-hour window on a site with 300 pageviews claims no $pageview has ever landed, and tells the operator to reinstall a working SDK")
	}
	if !strings.Contains(page, "Visitors ·") {
		t.Error("the Visitors KPI card vanished because the window was quiet — the row silently loses a card")
	}
	if !strings.Contains(page, "no pageviews landed in the last") {
		t.Error("nothing on the page says the window is simply quiet, which is the one fact the reader needs")
	}
	// and the control: an instance that genuinely has no pageviews still gets the install copy
	st2 := memory.New()
	if err := st2.Ingest(event.Event{ID: "b1", Name: "signup", DistinctID: "u1", Timestamp: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderWindowDash(t, st2, "/"), "no <b>$pageview</b> yet") {
		t.Error("a backend-only instance lost the teaching copy that tells it how to get web analytics")
	}
}

// The chart's bar for the current bucket is hatched and says "today so far". The table beside
// it — the only reading a screen reader or a phone gets — carried no such mark, so a morning
// load printed today's part-day against a whole prior day as a red crash.
func TestChartTableMarksThePartialBucket(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	var evs []event.Event
	n := 0
	for d := 1; d <= 20; d++ { // 20 whole prior days at 40 pageviews each
		for i := 0; i < 40; i++ {
			n++
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("e%d", n), Name: "$pageview", DistinctID: fmt.Sprintf("u%d", n),
				Timestamp: day.AddDate(0, 0, -d).Add(time.Duration(i) * 20 * time.Minute),
			})
		}
	}
	for i := 0; i < 3; i++ { // today, barely started
		n++
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("e%d", n), Name: "$pageview", DistinctID: fmt.Sprintf("t%d", i),
			Timestamp: day.Add(time.Minute),
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	page := renderWindowDash(t, st, "/?days=7&metric=$pageview")
	i := strings.Index(page, `<table class="ctab">`)
	if i < 0 {
		t.Fatal("no chart data table on the page")
	}
	table := page[i:]
	if j := strings.Index(table, "</table>"); j > 0 {
		table = table[:j]
	}
	firstRow := table[strings.Index(table, "<tbody>"):]
	if j := strings.Index(firstRow, "</tr>"); j > 0 {
		firstRow = firstRow[:j]
	}
	t.Logf("newest table row: %s", strings.Join(strings.Fields(firstRow), " "))
	if !strings.Contains(firstRow, "so far") {
		t.Error("the newest row is the bucket still filling and the table does not say so")
	}
	if strings.Contains(firstRow, `class="n down"`) {
		t.Error("the still-filling bucket is rendered as a red drop against a whole prior day — an artefact of the clock, not the product")
	}
}
