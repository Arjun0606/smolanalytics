package api

import (
	"net/http"
	"strconv"

	"github.com/Arjun0606/smolanalytics/internal/person"
)

// apiPeople is the person view: GET /v1/people[?trait=plan][&value=pro][&limit=50]
//
// Profiles are DERIVED from the event log on every request, never stored. A mutable person table
// would be the first state in this product that could disagree with the events — and "why does
// the profile say pro when the events say free" is a real support burden at both incumbents. Here
// there is nothing for the events to disagree with.
//
// Without a trait it lists people. With one it returns the value breakdown, which is what turns a
// trait into a segment rather than a curiosity.
func (s *Server) apiPeople(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	profiles := person.Compute(evs)
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = v
	}
	out := map[string]any{"people": len(profiles), "traits": person.Traits(profiles)}

	if trait := q.Get("trait"); trait != "" {
		if val := q.Get("value"); val != "" {
			ids := person.Matching(profiles, trait, val)
			out["trait"], out["value"], out["matched"] = trait, val, len(ids)
			// The ids are the point: they are what scopes an event-level report to a person
			// property. Capped, with the true count above, so a caller cannot mistake the
			// listing for the population.
			list := make([]string, 0, limit)
			for _, p := range person.List(profiles, 0) {
				if ids[p.DistinctID] {
					if len(list) >= limit {
						break
					}
					list = append(list, p.DistinctID)
				}
			}
			out["distinct_ids"] = list
			writeJSON(w, http.StatusOK, out)
			return
		}
		out["trait"], out["values"] = trait, person.TraitValues(profiles, trait)
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["recent"] = person.List(profiles, limit)
	writeJSON(w, http.StatusOK, out)
}
