package api

import (
	"container/heap"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/brief"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

// notable returns the proactive "what's broken / what to look at" digest — the
// verdict that fronts the dashboard AND the cloud's daily brief. The dashboard
// arrives with a session cookie; the control plane arrives with the write key
// (Authorization: Bearer) — accept either. Session-only here would 401 every
// cloud poll and silently kill the daily-brief retention hook.
func (s *Server) notable(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled() && !s.validSession(r) && !s.keyAuthed(r) {
		writeErr(w, http.StatusUnauthorized, "login or a valid key required")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default

	writeJSON(w, http.StatusOK, map[string]any{"findings": insight.Generate(evs)})
}

// apiBrief returns the full morning digest — pulse, per-product portfolio split,
// findings — computed by the same internal/brief the CLI renders, so the cloud's
// email, `smolanalytics brief`, and this endpoint can never disagree. Session or
// key auth for the same reason as notable: the control plane polls with the key.
func (s *Server) apiBrief(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled() && !s.validSession(r) && !s.keyAuthed(r) {
		writeErr(w, http.StatusUnauthorized, "login or a valid key required")
		return
	}
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= 90 {
			days = n
		}
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default
	writeJSON(w, http.StatusOK, brief.Build(evs, days, time.Now().UTC()))
}

// usage reports this instance's event + user counts so a control plane (the Cloud)
// can meter and enforce plan limits. Key-authed (it's a programmatic endpoint).
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing key")
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	month := time.Now().UTC().Format("2006-01")
	users := map[string]bool{}
	var total, events30d, eventsMonth int
	// Stream the counts instead of materializing the whole history — the cloud polls this
	// often, so keep it to a counter + a distinct-users set, not a full event slice.
	err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		total++
		users[e.DistinctID] = true
		if !e.Timestamp.Before(cutoff) { // inclusive "last 30 days"
			events30d++
		}
		if e.Timestamp.UTC().Format("2006-01") == month {
			eventsMonth++
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_events":  total,
		"events_30d":    events30d,
		"events_month":  eventsMonth,
		"period":        month,
		"users":         len(users),
		"bots_filtered": s.botsFiltered.Load(),
	})
}

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
	// Stream a bounded top-N by timestamp (a min-heap of size `limit`) instead of pulling
	// the whole history into RAM to sort it — O(limit) memory over a single Scan, so this
	// stays cheap even against the columnar scale tier.
	h := &tsHeap{}
	err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		if h.Len() < limit {
			heap.Push(h, e)
		} else if e.Timestamp.After((*h)[0].Timestamp) {
			(*h)[0] = e
			heap.Fix(h, 0)
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []event.Event(*h)
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	writeJSON(w, http.StatusOK, out)
}

// userActivity returns one user's timeline + latest traits. GET /v1/users/{id}. Streams
// and keeps only this user's events, not the whole dataset.
func (s *Server) userActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var mine []event.Event
	err := s.store.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		if e.DistinctID == id {
			mine = append(mine, e)
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userProfile(mine, id))
}

// tsHeap is a min-heap of events by timestamp (oldest at the root) for streaming top-N.
type tsHeap []event.Event

func (h tsHeap) Len() int           { return len(h) }
func (h tsHeap) Less(i, j int) bool { return h[i].Timestamp.Before(h[j].Timestamp) }
func (h tsHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *tsHeap) Push(x any)        { *h = append(*h, x.(event.Event)) }
func (h *tsHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
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
