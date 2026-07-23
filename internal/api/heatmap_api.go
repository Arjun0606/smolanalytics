package api

import (
	"net/http"
	"strconv"

	"github.com/Arjun0606/smolanalytics/internal/heatmap"
)

// GET /v1/heatmap?path=/pricing&viewport=desktop&cols=40&row_px=20&days=30&filters=...
// A click-density grid + top clicked elements for one page, aggregated at query time from $click
// autocapture. Same window + filter semantics as every other report; pinned MCP==API by the
// agreement test.
func (s *Server) apiHeatmap(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "path is required (get one from web_overview top_pages)")
		return
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rowPx, _ := strconv.Atoi(r.URL.Query().Get("row_px"))
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	evs = scopeToWindow(evs, from, to)
	writeJSON(w, http.StatusOK, heatmap.Compute(evs, path, r.URL.Query().Get("viewport"), cols, rowPx))
}
