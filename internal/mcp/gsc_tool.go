package mcp

// Search Console tool — the acquisition half of the story: which Google queries
// bring people in, next to what they did after arriving.

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/gsc"
)

func (s *Server) SetGSC(g *gsc.Store) { s.gsc = g }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "search_console_report",
			"description": "Google Search Console: the top search queries bringing visitors (clicks, impressions, CTR, position), the biggest movers vs the previous period, and money_pages — page-level SEO opportunities: quick_wins (pages ranking 4-15, one push from page 1), ctr_problems (ranking fine but the snippet doesn't earn the click), and cannibalization (one query split across competing pages). Requires the operator to have connected GSC (`smolanalytics gsc auth`). Pair with web_overview/goal_report to connect acquisition to behavior.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Top N queries (default 20)"},
			}},
		},
	)
}

func (s *Server) callGSC(name string, args json.RawMessage) (bool, string, error) {
	if name != "search_console_report" {
		return false, "", nil
	}
	if s.gsc == nil || !s.gsc.Connected() {
		return true, "", fmt.Errorf("search console isn't connected — the operator runs `smolanalytics gsc auth` once (setup: `smolanalytics gsc`)")
	}
	var p struct {
		Limit int `json:"limit"`
	}
	if err := unmarshalArgs(args, &p); err != nil {
		return true, "", err
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	rows, prev, site, fetched := s.gsc.Snapshot()
	if len(rows) == 0 {
		return true, "", fmt.Errorf("connected to %s but no data pulled yet — the server polls every 12h (or restart it to pull now)", site)
	}
	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
	}

	// movers: clicks delta vs the previous fetch, biggest absolute change first
	prevClicks := map[string]int{}
	for _, r := range prev {
		prevClicks[r.Query] = r.Clicks
	}
	type mover struct {
		Query  string `json:"query"`
		Clicks int    `json:"clicks"`
		Delta  int    `json:"delta"`
	}
	var movers []mover
	if len(prev) > 0 {
		for _, r := range rows {
			if d := r.Clicks - prevClicks[r.Query]; d != 0 {
				movers = append(movers, mover{Query: r.Query, Clicks: r.Clicks, Delta: d})
			}
		}
		sort.Slice(movers, func(i, j int) bool { return abs(movers[i].Delta) > abs(movers[j].Delta) })
		if len(movers) > 5 {
			movers = movers[:5]
		}
	}
	pageRows, _, pageErr := s.gsc.PageSnapshot()
	var money any
	if len(pageRows) == 0 {
		note := "page-level data not fetched yet — the server pulls it on the same 12h poll as queries"
		if pageErr != "" {
			note = "page-level fetch failed (" + pageErr + ") — query data above is current; pages retry on the next poll"
		}
		money = map[string]any{"note": note}
	} else {
		mp := map[string]any{
			"quick_wins":      quickWins(pageRows),
			"ctr_problems":    ctrProblems(pageRows),
			"cannibalization": cannibalization(pageRows),
		}
		if pageErr != "" {
			mp["page_fetch_error"] = pageErr + " — page data below is from the last successful pull"
		}
		money = mp
	}

	return true, jsonStr(map[string]any{
		"site":        site,
		"fetched_at":  fetched,
		"window":      "trailing 28 days (GSC data lags ~2 days)",
		"top_queries": rows,
		"top_movers":  movers,
		"money_pages": money,
	}), nil
}

// quickWin is a page/query pair ranking 4-15: already proven relevant, one
// content or title push from page-1 clicks.
type quickWin struct {
	Page        string  `json:"page"`
	Query       string  `json:"query"`
	Position    float64 `json:"position"`
	Impressions int     `json:"impressions"`
	Clicks      int     `json:"clicks"`
}

func quickWins(rows []gsc.PageRow) []quickWin {
	var out []quickWin
	for _, r := range rows {
		if r.Position >= 4 && r.Position <= 15 {
			out = append(out, quickWin{Page: r.Page, Query: r.Query, Position: r.Position, Impressions: r.Impressions, Clicks: r.Clicks})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Impressions > out[j].Impressions })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// ctrProblem is a page/query pair whose CTR is under half what its position
// typically earns — the ranking is fine, the snippet isn't.
type ctrProblem struct {
	Page           string  `json:"page"`
	Query          string  `json:"query"`
	Position       float64 `json:"position"`
	Impressions    int     `json:"impressions"`
	CTRPct         float64 `json:"ctr_pct"`
	ExpectedCTRPct float64 `json:"expected_ctr_pct"`
}

// typicalCTRPct is a deliberately simple position banding — rough industry
// averages: positions 1-3 ≈ 8%, 4-6 ≈ 4%, 7-10 ≈ 2%, 11+ ≈ 1%.
func typicalCTRPct(position float64) float64 {
	switch {
	case position <= 3:
		return 8
	case position <= 6:
		return 4
	case position <= 10:
		return 2
	default:
		return 1
	}
}

func ctrProblems(rows []gsc.PageRow) []ctrProblem {
	var out []ctrProblem
	for _, r := range rows {
		if r.Impressions < 100 {
			continue // too little data to call the CTR real
		}
		ctr := float64(r.Clicks) / float64(r.Impressions) * 100
		expected := typicalCTRPct(r.Position)
		if ctr < expected/2 {
			out = append(out, ctrProblem{Page: r.Page, Query: r.Query, Position: r.Position,
				Impressions: r.Impressions, CTRPct: round1(ctr), ExpectedCTRPct: expected})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Impressions > out[j].Impressions })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// cannibal is one query served by 2+ pages that each earn clicks — they split
// authority; consolidating usually lifts the survivor.
type cannibal struct {
	Query string    `json:"query"`
	Pages []canPage `json:"pages"`
}

type canPage struct {
	Page     string  `json:"page"`
	Clicks   int     `json:"clicks"`
	Position float64 `json:"position"`
}

func cannibalization(rows []gsc.PageRow) []cannibal {
	byQuery := map[string][]canPage{}
	for _, r := range rows {
		if r.Clicks > 0 {
			byQuery[r.Query] = append(byQuery[r.Query], canPage{Page: r.Page, Clicks: r.Clicks, Position: r.Position})
		}
	}
	var out []cannibal
	for q, pages := range byQuery {
		if len(pages) < 2 {
			continue
		}
		sort.Slice(pages, func(i, j int) bool { return pages[i].Clicks > pages[j].Clicks })
		out = append(out, cannibal{Query: q, Pages: pages})
	}
	// most clicks at stake first; deterministic
	sort.Slice(out, func(i, j int) bool {
		ci, cj := 0, 0
		for _, p := range out[i].Pages {
			ci += p.Clicks
		}
		for _, p := range out[j].Pages {
			cj += p.Clicks
		}
		if ci != cj {
			return ci > cj
		}
		return out[i].Query < out[j].Query
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
