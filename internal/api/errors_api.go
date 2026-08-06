package api

import (
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/errortrack"
)

// apiErrors is the error report: GET /v1/errors?days=7
//
// You have been capturing $exception since the SDK shipped and doing nothing with it. This is the
// report those events were always worth — grouped so one bug is one row, ranked by the PEOPLE it
// hit rather than how loudly it fires, split into new and known, and joined to the funnel so the
// question is "what did this cost" rather than "how many times did this happen".
func (s *Server) apiErrors(w http.ResponseWriter, r *http.Request) {
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
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -7)
	}
	// The preceding equal window is what makes "new" mean anything. Without it every error looks
	// like a regression, which is the same as none of them looking like one.
	prior := from.Add(-to.Sub(from))
	res := errortrack.Compute(evs, from, to, prior)

	out := map[string]any{
		"groups": res.Groups, "users": res.Users, "users_affected": res.UsersAffected,
		"crash_free_pct": res.CrashFreePct, "total": res.Total,
		"from": from.UTC().Format(time.RFC3339), "to": to.UTC().Format(time.RFC3339),
	}
	if res.Note != "" {
		out["note"] = res.Note
	}
	// The funnel join, over the page's OWN funnel when one is detectable. A report that quietly
	// measured a different funnel than the dashboard shows would be the contradiction this
	// codebase spent a week removing, so the steps are detected from the same events rather than
	// guessed at separately.
	if steps, _ := detectFunnel(evs, eventsByVolume(evs)); len(steps) >= 2 {
		if im := errortrack.FunnelImpact(evs, res.Groups, steps, 7*24*time.Hour); len(im) > 0 {
			names := make([]string, 0, len(steps))
			for _, st := range steps {
				names = append(names, st.Event)
			}
			out["impact"] = im
			out["impact_funnel"] = names
		}
	}
	// Whether error tracking is INSTALLED, as distinct from whether errors happened.
	//
	// An empty list means either "your app is healthy" or "you never installed this", and those are
	// opposite conclusions. An hour after pasting a snippet the second is far likelier, and a page
	// that asserts the first lets someone walk away believing their app is fine when nothing was
	// ever watching it.
	out["ever_received"] = s.everReceived("$exception")
	writeJSON(w, http.StatusOK, out)
}
