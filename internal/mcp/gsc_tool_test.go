package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/gsc"
)

// Quick wins are the 4-15 position band, sorted by impressions — boundaries matter.
func TestMoneyPagesQuickWins(t *testing.T) {
	cases := []struct {
		name     string
		rows     []gsc.PageRow
		wantQrys []string // in order
	}{
		{"inside band both edges", []gsc.PageRow{
			{Page: "/a", Query: "at four", Impressions: 100, Position: 4.0},
			{Page: "/b", Query: "at fifteen", Impressions: 200, Position: 15.0},
		}, []string{"at fifteen", "at four"}},
		{"outside band both sides", []gsc.PageRow{
			{Page: "/a", Query: "page one", Impressions: 900, Position: 3.9},
			{Page: "/b", Query: "nowhere", Impressions: 900, Position: 15.1},
		}, nil},
		{"sorted by impressions desc", []gsc.PageRow{
			{Page: "/a", Query: "small", Impressions: 50, Position: 7},
			{Page: "/b", Query: "big", Impressions: 5000, Position: 12},
			{Page: "/c", Query: "mid", Impressions: 500, Position: 5},
		}, []string{"big", "mid", "small"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quickWins(tc.rows)
			if len(got) != len(tc.wantQrys) {
				t.Fatalf("got %d wins, want %d: %+v", len(got), len(tc.wantQrys), got)
			}
			for i, q := range tc.wantQrys {
				if got[i].Query != q {
					t.Fatalf("win %d = %q, want %q", i, got[i].Query, q)
				}
			}
		})
	}
}

// CTR problems: impressions >= 100 and CTR under half the band-typical
// (1-3→8%, 4-6→4%, 7-10→2%, 11+→1%).
func TestMoneyPagesCTRProblems(t *testing.T) {
	cases := []struct {
		name         string
		row          gsc.PageRow
		flagged      bool
		wantExpected float64
	}{
		// position 2 typically earns 8%: threshold is 4%
		{"top-3 well under half", gsc.PageRow{Page: "/", Query: "q", Clicks: 10, Impressions: 1000, Position: 2}, true, 8},
		{"top-3 just under half", gsc.PageRow{Page: "/", Query: "q", Clicks: 39, Impressions: 1000, Position: 2}, true, 8},
		{"top-3 exactly half is fine", gsc.PageRow{Page: "/", Query: "q", Clicks: 40, Impressions: 1000, Position: 2}, false, 0},
		// band edges
		{"band 4-6", gsc.PageRow{Page: "/", Query: "q", Clicks: 10, Impressions: 1000, Position: 6}, true, 4},
		{"band 7-10", gsc.PageRow{Page: "/", Query: "q", Clicks: 9, Impressions: 1000, Position: 10}, true, 2},
		{"band 11+", gsc.PageRow{Page: "/", Query: "q", Clicks: 4, Impressions: 1000, Position: 14}, true, 1},
		{"band 11+ above half", gsc.PageRow{Page: "/", Query: "q", Clicks: 6, Impressions: 1000, Position: 14}, false, 0},
		// too few impressions to call the CTR real
		{"under impression floor", gsc.PageRow{Page: "/", Query: "q", Clicks: 0, Impressions: 99, Position: 2}, false, 0},
		{"at impression floor", gsc.PageRow{Page: "/", Query: "q", Clicks: 0, Impressions: 100, Position: 2}, true, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ctrProblems([]gsc.PageRow{tc.row})
			if tc.flagged != (len(got) == 1) {
				t.Fatalf("flagged=%v, want %v: %+v", len(got) == 1, tc.flagged, got)
			}
			if tc.flagged && got[0].ExpectedCTRPct != tc.wantExpected {
				t.Fatalf("expected_ctr_pct=%v, want %v", got[0].ExpectedCTRPct, tc.wantExpected)
			}
		})
	}
}

// Cannibalization: a query on 2+ pages where both earn clicks — zero-click
// duplicates don't count, and the biggest clicker leads the page list.
func TestMoneyPagesCannibalization(t *testing.T) {
	cases := []struct {
		name      string
		rows      []gsc.PageRow
		wantQrys  []string
		wantFirst string // leading page of the first hit
	}{
		{"two pages both clicked", []gsc.PageRow{
			{Page: "/a", Query: "split", Clicks: 5, Impressions: 100, Position: 6},
			{Page: "/b", Query: "split", Clicks: 12, Impressions: 100, Position: 8},
		}, []string{"split"}, "/b"},
		{"second page has zero clicks", []gsc.PageRow{
			{Page: "/a", Query: "solo", Clicks: 5, Impressions: 100, Position: 6},
			{Page: "/b", Query: "solo", Clicks: 0, Impressions: 100, Position: 8},
		}, nil, ""},
		{"one page only", []gsc.PageRow{
			{Page: "/a", Query: "solo", Clicks: 5, Impressions: 100, Position: 6},
		}, nil, ""},
		{"biggest clicks at stake first", []gsc.PageRow{
			{Page: "/a", Query: "minor", Clicks: 1, Impressions: 50, Position: 6},
			{Page: "/b", Query: "minor", Clicks: 2, Impressions: 50, Position: 8},
			{Page: "/c", Query: "major", Clicks: 40, Impressions: 500, Position: 5},
			{Page: "/d", Query: "major", Clicks: 30, Impressions: 400, Position: 9},
		}, []string{"major", "minor"}, "/c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cannibalization(tc.rows)
			if len(got) != len(tc.wantQrys) {
				t.Fatalf("got %d hits, want %d: %+v", len(got), len(tc.wantQrys), got)
			}
			for i, q := range tc.wantQrys {
				if got[i].Query != q {
					t.Fatalf("hit %d = %q, want %q", i, got[i].Query, q)
				}
			}
			if tc.wantFirst != "" && got[0].Pages[0].Page != tc.wantFirst {
				t.Fatalf("leading page = %q, want %q", got[0].Pages[0].Page, tc.wantFirst)
			}
		})
	}
}

// The tool payload: money_pages populated from page rows; when page rows are
// absent the field must explain itself instead of showing empty lists.
func TestSearchConsoleReportMoneyPages(t *testing.T) {
	gs, _ := gsc.Open("") // in-memory
	_ = gs.SetGrant("rt", "sc-domain:example.com")
	_ = gs.SetRows([]gsc.Row{{Query: "q", Clicks: 10, Impressions: 200, CTRPct: 5, Position: 3}})
	s := newServer(t)
	s.SetGSC(gs)

	// no page rows yet → an explanatory note, not empty lists
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_console_report","arguments":{}}}`)
	text, isErr := toolText(t, r)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var out struct {
		MoneyPages map[string]json.RawMessage `json:"money_pages"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if _, has := out.MoneyPages["note"]; !has {
		t.Fatalf("empty page rows must explain themselves: %s", text)
	}
	if _, has := out.MoneyPages["quick_wins"]; has {
		t.Fatalf("no page data must not fabricate empty opportunity lists: %s", text)
	}

	// a recorded page-fetch failure surfaces in the note
	_ = gs.SetPageFetchError("google api 429: quota")
	r = call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_console_report","arguments":{}}}`)
	text, _ = toolText(t, r)
	if !strings.Contains(text, "429") {
		t.Fatalf("page fetch failure must surface in the payload: %s", text)
	}

	// with page rows → all three sections computed
	_ = gs.SetPageRows([]gsc.PageRow{
		{Page: "/blog", Query: "quick win", Clicks: 20, Impressions: 800, Position: 6},
		{Page: "/", Query: "ctr problem", Clicks: 5, Impressions: 1000, Position: 2},
		{Page: "/", Query: "split", Clicks: 9, Impressions: 300, Position: 4.5},
		{Page: "/blog", Query: "split", Clicks: 7, Impressions: 250, Position: 5.5},
	})
	r = call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_console_report","arguments":{}}}`)
	text, isErr = toolText(t, r)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var full struct {
		MoneyPages struct {
			QuickWins       []quickWin   `json:"quick_wins"`
			CTRProblems     []ctrProblem `json:"ctr_problems"`
			Cannibalization []cannibal   `json:"cannibalization"`
			Note            string       `json:"note"`
		} `json:"money_pages"`
	}
	if err := json.Unmarshal([]byte(text), &full); err != nil {
		t.Fatal(err)
	}
	mp := full.MoneyPages
	if mp.Note != "" {
		t.Fatalf("populated page rows must not carry the not-fetched note: %s", text)
	}
	// quick win + the cannibal pair all sit in the 4-15 band → 3 wins
	if len(mp.QuickWins) != 3 || mp.QuickWins[0].Query != "quick win" {
		t.Fatalf("quick_wins: %+v", mp.QuickWins)
	}
	if len(mp.CTRProblems) != 1 || mp.CTRProblems[0].Query != "ctr problem" || mp.CTRProblems[0].ExpectedCTRPct != 8 {
		t.Fatalf("ctr_problems: %+v", mp.CTRProblems)
	}
	if len(mp.Cannibalization) != 1 || mp.Cannibalization[0].Query != "split" || mp.Cannibalization[0].Pages[0].Page != "/" {
		t.Fatalf("cannibalization: %+v", mp.Cannibalization)
	}
}
