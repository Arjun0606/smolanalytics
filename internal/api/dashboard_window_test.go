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
