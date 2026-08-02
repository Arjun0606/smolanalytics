package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
)

// GET /v1/ai-crawlers?days=30&filters=... — which AI crawlers have actually fetched
// this site, computed from the $ai_crawl events the customer's own edge middleware
// reports: per-crawler and per-operator hits split by purpose (training sweep, search
// indexing, live assistant fetch), the pages they read, the errors the server handed
// them back, a dense daily trend, and the pages humans read that no crawler has ever
// seen.
//
// The retrieval half of AI visibility, and the half a pixel cannot measure: GPTBot,
// ClaudeBot and PerplexityBot execute no JavaScript, so a client-side tool reports
// zero of them no matter how the report is written.
//
// Same single query path as everything else — the MCP ai_crawlers tool and the
// dashboard's crawler card read this exact computation, and agreement_test pins the
// first two to byte-identical answers.
func (s *Server) apiAICrawlers(w http.ResponseWriter, r *http.Request) {
	// s.filtered is the scope every /v1 report shares: the production env scope by
	// default plus the ?f=/?filters= grammar. Deliberately NOT a bespoke ?site=/?env=
	// pair here — the dashboard narrows its events ONCE, above every pane, so its GEO
	// and crawler cards already see the same slice; a scope param only this endpoint
	// understood would be one MCP has no way to express, which is exactly the drift the
	// agreement guarantee forbids. A direct caller scopes with ?f=site:eq:example.com,
	// which MCP speaks too.
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	days := 0 // 0 → aicrawl's own default (30), never a second copy of it here
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}
	if days > 365 {
		days = 365 // same cap as the MCP tool — surfaces must agree
	}
	writeJSON(w, http.StatusOK, aicrawl.Compute(evs, days, time.Time{}))
}
