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
	writeJSON(w, http.StatusOK, map[string]string{"answer": answer(q, evs)})
}

func answer(q string, evs []event.Event) string {
	switch {
	case hasAny(q, "convert", "conversion", "funnel", "drop", "checkout", "activat"):
		return answerFunnel(evs)
	case hasAny(q, "retention", "retain", "come back", "comeback", "returning", "stick"):
		return answerRetention(evs)
	case hasAny(q, "source", "where", "from", "channel", "acquisition", "referr"):
		return answerSources(evs)
	case hasAny(q, "signup", "sign up", "new user", "how many", "growth", "trend"):
		return answerSignups(evs)
	case hasAny(q, "active", "users", "dau", "wau", "total"):
		return answerActive(evs)
	default:
		return "I can answer about your conversion funnel, retention, signups/growth, traffic sources, and active users right here. " +
			"For anything else, connect smolanalytics to your own Claude or Cursor over MCP and just ask — your model reads the same data through our tools."
	}
}

func answerFunnel(evs []event.Event) string {
	fr := funnel.Compute(evs, []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}, 7*24*time.Hour)
	if len(fr.Steps) == 0 || fr.Steps[0].Count == 0 {
		return "No signups yet to build a funnel from."
	}
	worst, worstDrop := "", -1
	for _, st := range fr.Steps[1:] {
		if st.DroppedFromPrev > worstDrop {
			worstDrop, worst = st.DroppedFromPrev, st.Event
		}
	}
	return fmt.Sprintf("%d of %d users (%d%%) complete signup → activate → checkout. The biggest drop-off is at \"%s\" — %d users fall off there.",
		fr.Steps[len(fr.Steps)-1].Count, fr.Steps[0].Count, pct(fr.OverallConversion), worst, worstDrop)
}

func answerRetention(evs []event.Event) string {
	rr := retention.Compute(evs, 7, "open")
	var size, d1, d7 int
	for _, c := range rr.Cohorts {
		size += c.Size
		if len(c.Returned) > 1 {
			d1 += c.Returned[1]
		}
		if len(c.Returned) > 7 {
			d7 += c.Returned[7]
		}
	}
	if size == 0 {
		return "Not enough activity yet to measure retention."
	}
	return fmt.Sprintf("Day-1 retention is %d%% and day-7 is %d%% (across %d users). ",
		int(float64(d1)/float64(size)*100+0.5), int(float64(d7)/float64(size)*100+0.5), size)
}

func answerSources(evs []event.Event) string {
	var signups []event.Event
	for _, e := range evs {
		if e.Name == "signup" {
			signups = append(signups, e)
		}
	}
	groups := query.Breakdown(signups, "source")
	if len(groups) == 0 {
		return "No source data on your signups yet."
	}
	parts := []string{}
	for i, g := range groups {
		if i >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d, %d%%)", g.Value, g.Count, int(float64(g.Count)/float64(len(signups))*100+0.5)))
	}
	return "Top signup sources: " + strings.Join(parts, ", ") + "."
}

func answerSignups(evs []event.Event) string {
	tr := trends.Compute(evs, "signup", time.Time{}, time.Time{}, false)
	days := len(tr.Points)
	if days == 0 {
		return "No signups recorded yet."
	}
	return fmt.Sprintf("%d signups over the last %d days — about %d/day.", tr.Total, days, tr.Total/days)
}

func answerActive(evs []event.Event) string {
	total := distinctUsers(evs)
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	recent := map[string]bool{}
	for _, e := range evs {
		if e.Timestamp.After(cutoff) {
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

