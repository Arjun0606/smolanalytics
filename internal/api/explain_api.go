package api

import (
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/whatchanged"
)

// GET /v1/explain?event=signup&days=30 — when did this number change, and by how much.
//
// This is the equivalent of Mixpanel's flagship Root Cause Analysis, which they meter at ten runs
// a day on the free plan. Ours has existed for a while and was reachable only through the
// explain_change MCP tool — so it was invisible to anyone who does not drive this product from an
// editor, which is most of the people it is for.
//
// The MCP tool does two things: detect the change from events, then correlate it with git commits
// in the surrounding window. Only the FIRST half is available to a server, which may have no
// checkout to read, so this endpoint does that half honestly rather than pretending to the other.
// Commit correlation stays in the editor, where the repository actually is, and the response says
// so instead of leaving a reader wondering why they got less.
func (s *Server) apiExplain(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("event")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name the event whose change you want explained, e.g. ?event=signup")
		return
	}
	if !s.knownEventOr400(w, name) {
		return
	}
	days, derr := posIntParam(r, "days", 30, 180)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	// NOT query.WithoutSampler. Every other activity report drops this tool's own writes, but that
	// filter is by event NAME and this report already selects one name — so here it can only ever
	// subtract, never add. The one case where it fires is "why did $ai_crawl change?", a fair
	// question about the AI-crawler pane, and there it would delete every matching event and answer
	// "steady at zero" — confidently, silently, wrong. The filter's own doc comment says not to use
	// it on the reports where sampler events are the input, and this is one of them.
	now := time.Now().UTC()
	series := whatchanged.TrimLeadingSilence(whatchanged.Series(evs, name, days, now))
	change := whatchanged.Detect(series)
	if len(series) < 6 { // whatchanged needs 3 days on each side of a split to call one
		writeJSON(w, http.StatusOK, map[string]any{
			"event": name, "days": days, "series": series,
			"change":  whatchanged.Change{},
			"verdict": "not enough history yet. this needs about a week of days that actually have data before a change means anything.",
			"read":    "come back once this event has been sending for a week or so.",
		})
		return
	}

	out := map[string]any{
		"event": name, "days": days, "series": series, "change": change,
		"verdict": change.Verdict,
	}
	if !change.Found {
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Deploy markers near the change are the one CAUSE this server can honestly offer: they were
	// recorded here, with times, by the person shipping. Correlation, never proof — and the copy
	// says so, because "you deployed that day" is evidence and not a conclusion.
	if s.deploys != nil {
		day, perr := time.Parse("2006-01-02", change.Day)
		if perr == nil {
			var near []any
			for _, d := range s.deploys.List() {
				if diff := d.At.Sub(day); diff > -48*time.Hour && diff < 48*time.Hour {
					near = append(near, d)
				}
			}
			if len(near) > 0 {
				out["deploys_near"] = near
				out["read"] = "these deploys landed within two days of the change. that is correlation " +
					"and a place to look, never proof — the change may have been a link someone posted."
			}
		}
	}
	if _, has := out["deploys_near"]; !has {
		out["read"] = "no deploy was recorded near this change, so there is nothing here to line it up " +
			"against. record deploys and this becomes 'which ship did it'. to search your git history " +
			"around the change, ask your agent — it can read the repo, this server cannot."
	}
	writeJSON(w, http.StatusOK, out)
}
