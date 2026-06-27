package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// recentEvents returns the most recent events (newest first) — the live feed you
// watch right after instrumenting to confirm data is flowing. GET /v1/events/recent?limit=50
func (s *Server) recentEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, recent(evs, limit))
}

// userActivity returns one user's timeline + latest traits. GET /v1/users/{id}
func (s *Server) userActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userProfile(evs, id))
}

// recent sorts a copy newest-first and takes the first n.
func recent(evs []event.Event, n int) []event.Event {
	out := make([]event.Event, len(evs))
	copy(out, evs)
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// userProfile builds a single user's activity: counts, first/last seen, the latest
// known properties (merged forward), and the event timeline (newest first).
func userProfile(evs []event.Event, id string) map[string]any {
	var mine []event.Event
	for _, e := range evs {
		if e.DistinctID == id {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return map[string]any{"distinct_id": id, "found": false}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Timestamp.Before(mine[j].Timestamp) })

	traits := map[string]any{}
	counts := map[string]int{}
	for _, e := range mine {
		counts[e.Name]++
		for k, v := range e.Properties {
			traits[k] = v // last write wins → latest known value
		}
	}
	timeline := recent(mine, 100)
	return map[string]any{
		"distinct_id":  id,
		"found":        true,
		"event_count":  len(mine),
		"first_seen":   mine[0].Timestamp,
		"last_seen":    mine[len(mine)-1].Timestamp,
		"traits":       traits,
		"event_counts": counts,
		"timeline":     timeline,
	}
}
