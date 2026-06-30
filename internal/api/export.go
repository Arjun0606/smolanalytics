package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// export streams the full event history out — CSV or JSONL — so there's no
// lock-in: take your data to a warehouse or another tool any time. Gated behind
// the write key (it's a full data dump). GET /v1/export?format=csv|jsonl
//
// It streams via Scan (one event at a time, flushed in batches) rather than
// materializing the whole dataset — so exporting millions of events from the columnar
// scale tier uses flat memory instead of loading everything into RAM.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing write key")
		return
	}
	flusher, _ := w.(http.Flusher)

	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=smolanalytics-export.csv")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "name", "distinct_id", "timestamp", "properties"})
		n := 0
		err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
			props := ""
			if len(e.Properties) > 0 {
				b, _ := json.Marshal(e.Properties)
				props = string(b)
			}
			if err := cw.Write([]string{e.ID, e.Name, e.DistinctID, e.Timestamp.UTC().Format(time.RFC3339), props}); err != nil {
				return err
			}
			if n++; n%10_000 == 0 { // stream to the client in chunks, keep buffers small
				cw.Flush()
				if flusher != nil {
					flusher.Flush()
				}
			}
			return nil
		})
		cw.Flush()
		_ = err
		return
	}

	// default: JSONL — one event per line, the same shape we ingest.
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=smolanalytics-export.jsonl")
	enc := json.NewEncoder(w)
	n := 0
	_ = s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		if err := enc.Encode(e); err != nil {
			return err
		}
		if n++; n%10_000 == 0 && flusher != nil {
			flusher.Flush()
		}
		return nil
	})
}
