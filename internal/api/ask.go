package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// ask answers a plain-English question about the data with zero dependencies —
// it routes common questions (conversion, retention, signups, sources, active
// users) deterministically, right in the dashboard, no model required. For
// arbitrary questions the user connects smolanalytics to their OWN Claude /
// Cursor over MCP (we never call a model ourselves) — see internal/mcp.
func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(body, &req)
	q := strings.ToLower(strings.TrimSpace(req.Question))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "ask a question")
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	evs = query.Apply(evs, nil) // production scope: dev-env events excluded by default

	writeJSON(w, http.StatusOK, map[string]string{"answer": answer(q, evs)})
}

func answer(q string, evs []event.Event) string {
	vol := eventsByVolume(evs) // adapt to the user's OWN events, like the dashboard
	switch {
	case hasAny(q, "convert", "conversion", "funnel", "drop", "checkout", "activat"):
		return answerFunnel(evs, vol)
	case hasAny(q, "retention", "retain", "come back", "comeback", "returning", "stick"):
		return answerRetention(evs, vol)
	case hasAny(q, "source", "where", "from", "channel", "acquisition", "referr"):
		return answerSources(evs, vol)
	case hasAny(q, "signup", "sign up", "new user", "how many", "growth", "trend"):
		return answerSignups(evs, vol)
	case hasAny(q, "active", "users", "dau", "wau", "total"):
		return answerActive(evs)
	default:
		return "I can answer about your conversion funnel, retention, signups/growth, traffic sources, and active users right here. " +
			"For anything else, connect smolanalytics to your own Claude or Cursor over MCP and just ask — your model reads the same data through our tools."
	}
}

func answerFunnel(evs []event.Event, vol []string) string {
	fsteps, ftitle := detectFunnel(evs, vol)
	fr := funnel.Compute(evs, fsteps, 7*24*time.Hour)
	if len(fr.Steps) == 0 || fr.Steps[0].Count == 0 {
		return "No events yet to build a funnel from."
	}
	worst, worstDrop := "", -1
	for _, st := range fr.Steps[1:] {
		if st.DroppedFromPrev > worstDrop {
			worstDrop, worst = st.DroppedFromPrev, st.Event
		}
	}
	return fmt.Sprintf("%d of %d users (%d%%) complete %s. The biggest drop-off is at \"%s\" — %d users fall off there.",
		fr.Steps[len(fr.Steps)-1].Count, fr.Steps[0].Count, pct(fr.OverallConversion), ftitle, worst, worstDrop)
}

func answerRetention(evs []event.Event, vol []string) string {
	rr := retention.Compute(evs, 7, pickEvent(vol, "open"))
	now := time.Now().UTC()
	// honest denominators: only cohorts old enough to observe day N (retention.DayN)
	d1, size1 := retention.DayN(rr, 1, now)
	if size1 == 0 {
		return "Not enough history yet to measure retention — check back once users are past their first day."
	}
	out := fmt.Sprintf("Day-1 retention is %d%% (of %d users past day 1).",
		int(float64(d1)/float64(size1)*100+0.5), size1)
	if d7, size7 := retention.DayN(rr, 7, now); size7 > 0 {
		out = fmt.Sprintf("Day-1 retention is %d%% and day-7 is %d%% (of %d and %d users old enough to measure).",
			int(float64(d1)/float64(size1)*100+0.5), int(float64(d7)/float64(size7)*100+0.5), size1, size7)
	}
	return out
}

func answerSources(evs []event.Event, vol []string) string {
	headlineEvent := pickEvent(vol, "signup")
	srcProp := detectProp(evs, "source")
	if srcProp == "" {
		return "Your events don't carry any properties to break down by yet."
	}
	var headline []event.Event
	for _, e := range evs {
		if e.Name == headlineEvent {
			headline = append(headline, e)
		}
	}
	groups := query.Breakdown(headline, srcProp)
	if len(groups) == 0 {
		return "No " + srcProp + " data on your " + headlineEvent + " events yet."
	}
	parts := []string{}
	for i, g := range groups {
		if i >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d, %d%%)", g.Value, g.Count, int(float64(g.Count)/float64(len(headline))*100+0.5)))
	}
	return fmt.Sprintf("Top %s by %s: %s.", headlineEvent, srcProp, strings.Join(parts, ", "))
}

func answerSignups(evs []event.Event, vol []string) string {
	ev := pickEvent(vol, "signup")
	tr := trends.Compute(evs, ev, time.Time{}, time.Time{}, false)
	days := len(tr.Points)
	if days == 0 {
		return "No events recorded yet."
	}
	return fmt.Sprintf("%d \"%s\" events over the last %d days — about %d/day.", tr.Total, ev, days, tr.Total/days)
}

func answerActive(evs []event.Event) string {
	total := distinctUsers(evs)
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	recent := map[string]bool{}
	for _, e := range evs {
		if !e.Timestamp.Before(cutoff) { // inclusive "last 7 days", consistent with the engine
			recent[e.DistinctID] = true
		}
	}
	return fmt.Sprintf("%d total users, %d active in the last 7 days.", total, len(recent))
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
