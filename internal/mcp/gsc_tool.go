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
			"description": "Google Search Console: the top search queries bringing visitors (clicks, impressions, CTR, position) plus the biggest movers vs the previous period. Requires the operator to have connected GSC (`smolanalytics gsc auth`). Pair with web_overview/goal_report to connect acquisition to behavior.",
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
	return true, jsonStr(map[string]any{
		"site":        site,
		"fetched_at":  fetched,
		"window":      "trailing 28 days (GSC data lags ~2 days)",
		"top_queries": rows,
		"top_movers":  movers,
	}), nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
