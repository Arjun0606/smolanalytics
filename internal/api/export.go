package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"time"
)

// export streams the full event history out — CSV or JSONL — so there's no
// lock-in: take your data to a warehouse or another tool any time. Gated behind
// the write key (it's a full data dump). GET /v1/export?format=csv|jsonl
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=smolanalytics-export.csv")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "name", "distinct_id", "timestamp", "properties"})
		for _, e := range evs {
			props := ""
			if len(e.Properties) > 0 {
				b, _ := json.Marshal(e.Properties)
				props = string(b)
			}
			_ = cw.Write([]string{e.ID, e.Name, e.DistinctID, e.Timestamp.UTC().Format(time.RFC3339), props})
		}
		cw.Flush()
		return
	}

	// default: JSONL — one event per line, the same shape we ingest.
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=smolanalytics-export.jsonl")
	enc := json.NewEncoder(w)
	for _, e := range evs {
		_ = enc.Encode(e)
	}
}
