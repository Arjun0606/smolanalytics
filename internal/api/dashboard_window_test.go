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
	if !strings.Contains(page, "People ·") {
		t.Error("the People KPI card vanished because the window was quiet — the row silently loses a card")
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

// THE FUNNEL FOLLOWS THE RANGE CONTROL.
//
// It did not. The funnel was computed near the top of the handler over the unwindowed events, so
// the pane, the accent conversion tile and the "conversion by X" pane were always all-history —
// under a range control they ignored, between two tiles labelled "· 7d", beneath the words
// "computed by /v1/funnel" (which does honour the window), and beside a grain-off link promising
// "keeping this window".
//
// Measured before the fix: with 7d selected the pane read signup 80 → checkout 32 and the tile
// read 40%, while /v1/funnel?days=7 answered 20 → 2, i.e. 10%. Four claims to a window it did not
// have, and a founder reading "we convert at 40% this week" was reading all of time.
func TestFunnelPaneFollowsTheSelectedRange(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	// 60 people a month ago, half converting; 20 people two days ago, 2 converting.
	// The rates differ sharply on purpose: an all-history read and a 7d read cannot coincide.
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("old%d", i)
		ts := now.AddDate(0, 0, -30).Add(time.Duration(i) * time.Minute)
		evs = append(evs, event.Event{ID: "s" + id, Name: "signup", DistinctID: id, Timestamp: ts})
		if i%2 == 0 {
			evs = append(evs, event.Event{ID: "c" + id, Name: "checkout", DistinctID: id, Timestamp: ts.Add(time.Hour)})
		}
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("new%d", i)
		ts := now.AddDate(0, 0, -2).Add(time.Duration(i) * time.Minute)
		evs = append(evs, event.Event{ID: "s" + id, Name: "signup", DistinctID: id, Timestamp: ts})
		if i < 2 {
			evs = append(evs, event.Event{ID: "c" + id, Name: "checkout", DistinctID: id, Timestamp: ts.Add(time.Hour)})
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}

	firstStep := func(url string) int {
		html := paneHTML(t, renderWindowDash(t, st, url+"&steps=signup,checkout"), "pane-funnel")
		m := regexp.MustCompile(`data-count="(\d+)"`).FindStringSubmatch(html)
		if m == nil {
			// fall back to the first standalone integer rendered in the pane
			m = regexp.MustCompile(`>(\d{1,6})<`).FindStringSubmatch(html)
		}
		if m == nil {
			t.Fatalf("could not read a step count out of the funnel pane for %s", url)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}

	seven := firstStep("/?days=7")
	ninety := firstStep("/?days=90")
	t.Logf("funnel first step: 7d=%d 90d=%d (20 signups in the last 7d, 80 all-time)", seven, ninety)

	// Only 20 people signed up inside 7 days. Reading 80 means the pane ignored the control.
	if seven > 20 {
		t.Errorf("the 7d funnel counted %d at its first step, but only 20 people signed up in that "+
			"window — the pane is reading outside the range it is labelled with", seven)
	}
	// And it must be window-SENSITIVE, so a future regression cannot pass by being constant.
	if ninety <= seven {
		t.Errorf("90d (%d) is not larger than 7d (%d) — the pane does not respond to the range "+
			"control at all", ninety, seven)
	}
}
