package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func auditRender(t *testing.T, st *memory.Store, url string) string {
	t.Helper()
	s := New(st)
	w := httptest.NewRecorder()
	s.dashboard(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 200 {
		t.Fatalf("GET %s → %d", url, w.Code)
	}
	return w.Body.String()
}

func auditPane(page, id string) string {
	i := strings.Index(page, `id="`+id+`"`)
	if i < 0 {
		return "<<PANE ABSENT>>"
	}
	rest := page[i:]
	if j := strings.Index(rest, `<div class="pane`); j > 0 {
		rest = rest[:j]
	}
	return rest
}

func TestAuditRetentionPanesEmpty(t *testing.T) {
	st := memory.New()
	page := auditRender(t, st, "/")
	for _, id := range []string{"pane-retention", "pane-lifecycle", "pane-stickiness"} {
		fmt.Printf("\n========== EMPTY INSTANCE · %s ==========\n%s\n", id, auditPane(page, id))
	}
}

func TestAuditRetentionPanesOneDay(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	for i := 0; i < 12; i++ {
		st.Ingest(event.Event{Name: "$pageview", DistinctID: fmt.Sprintf("u%d", i), Timestamp: now.Add(-2 * time.Hour)})
	}
	page := auditRender(t, st, "/")
	for _, id := range []string{"pane-retention", "pane-lifecycle", "pane-stickiness"} {
		fmt.Printf("\n========== ONE DAY · %s ==========\n%s\n", id, auditPane(page, id))
	}
}

func TestAuditRetentionPanesRich(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	day := 24 * time.Hour
	for d := 40; d >= 0; d-- {
		for i := 0; i < 6; i++ {
			st.Ingest(event.Event{Name: "$pageview", DistinctID: fmt.Sprintf("u%d_%d", d, i), Timestamp: now.Add(-time.Duration(d) * day)})
		}
		// returners
		for i := 0; i < 3; i++ {
			st.Ingest(event.Event{Name: "$pageview", DistinctID: fmt.Sprintf("u%d_%d", d+1, i), Timestamp: now.Add(-time.Duration(d) * day)})
		}
	}
	page := auditRender(t, st, "/")
	for _, id := range []string{"pane-retention", "pane-lifecycle", "pane-stickiness"} {
		fmt.Printf("\n========== RICH · %s ==========\n%s\n", id, auditPane(page, id))
	}
	page90 := auditRender(t, st, "/?rdays=90&rbucket=day")
	fmt.Printf("\n========== RICH 90d · pane-retention (first 2500 chars) ==========\n%.2500s\n", auditPane(page90, "pane-retention"))
}

func TestAuditEmptyPageShape(t *testing.T) {
	st := memory.New()
	page := auditRender(t, st, "/")
	fmt.Printf("LEN=%d hasDeck=%v hasOnboard=%v\n", len(page),
		strings.Contains(page, `class="deck"`), strings.Contains(page, "sec-return"))
	i := strings.Index(page, "sec-return")
	if i > 0 {
		fmt.Printf("SEC-RETURN CONTEXT:\n%.400s\n", page[i-200:i+900])
	}
}

// dormant instance: real history, but nothing in the last 30 days.
func TestAuditRetentionPanesDormant(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	day := 24 * time.Hour
	for d := 60; d < 75; d++ {
		for i := 0; i < 5; i++ {
			st.Ingest(event.Event{Name: "$pageview", DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp: now.Add(-time.Duration(d) * day)})
		}
	}
	page := auditRender(t, st, "/")
	for _, id := range []string{"pane-retention", "pane-lifecycle", "pane-stickiness"} {
		p := auditPane(page, id)
		if len(p) > 700 {
			p = p[:700] + " …"
		}
		fmt.Printf("\n========== DORMANT · %s ==========\n%s\n", id, p)
	}
	fmt.Printf("\nstratum present: %v\n", strings.Contains(page, `id="sec-return"`))
}

// one single user, one single event — the very first hit on a brand new instance.
func TestAuditRetentionPanesOneEvent(t *testing.T) {
	st := memory.New()
	st.Ingest(event.Event{Name: "$pageview", DistinctID: "u1", Timestamp: time.Now().UTC().Add(-time.Minute)})
	page := auditRender(t, st, "/")
	for _, id := range []string{"pane-retention", "pane-lifecycle", "pane-stickiness"} {
		p := auditPane(page, id)
		if len(p) > 900 {
			p = p[:900] + " …"
		}
		fmt.Printf("\n========== ONE EVENT · %s ==========\n%s\n", id, p)
	}
}

// 60 daily cohorts, horizon set to 90 periods: how many columns actually carry a number?
func TestAuditRetentionHorizonVsCohortCap(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	day := 24 * time.Hour
	for d := 90; d >= 1; d-- { // a new cohort every day for 90 days
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("d%d_u%d", d, i)
			st.Ingest(event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-time.Duration(d) * day)})
			if i < 2 { // half come back later
				st.Ingest(event.Event{Name: "$pageview", DistinctID: id, Timestamp: now.Add(-time.Duration(d-1) * day)})
			}
		}
	}
	page := auditRender(t, st, "/?rdays=90")
	p := auditPane(page, "pane-retention")
	cols := strings.Count(p, "<th>")
	filled := strings.Count(p, `<div class="cell">`)
	empties := strings.Count(p, `class="empty"`)
	rows := strings.Count(p, `<tr><td class="lab">`)
	fmt.Printf("rdays=90: %d header cells, %d rows, %d filled cells, %d blank cells\n", cols, rows, filled, empties)
}
