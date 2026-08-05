package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/formula"
	"github.com/Arjun0606/smolanalytics/internal/trends"
)

// apiFormula computes a derived metric across event series.
// GET /v1/formula?expr=A/B*100&A=signup&B=$pageview&days=30[&unique=1]
//
// "Signups per visitor", "revenue per active user", "the share of sessions that rage-clicked" —
// every one is one series over another, and without this a person exports two charts and opens a
// spreadsheet, which is the moment they stop using the tool.
//
// Each named series is computed by the SAME trends engine the chart uses, over the SAME window,
// so a derived number never disagrees with the two lines it was derived from.
func (s *Server) apiFormula(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	expr := strings.TrimSpace(q.Get("expr"))
	if expr == "" {
		writeErr(w, http.StatusBadRequest,
			"expr is required, e.g. ?expr=A/B*100&A=signup&B=$pageview — name each series with a single-letter parameter")
		return
	}
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
		from = to.AddDate(0, 0, -30)
	}
	iv := trends.Interval(q.Get("interval"))
	unique := boolParam(q.Get("unique"))

	// Operand names are single uppercase letters, mapped to real events by their own parameter.
	// Deliberately not "put the event name in the expression": event names contain dots, dollars
	// and spaces, and parsing them inside arithmetic is how a formula silently means something
	// other than what was typed.
	var inputs []formula.Series
	names := map[string]string{}
	for c := 'A'; c <= 'Z'; c++ {
		key := string(c)
		ev := q.Get(key)
		if ev == "" {
			continue
		}
		if !s.knownEventOr400(w, ev) {
			return // knownEventOr400 already wrote the 400 listing what does exist
		}
		res := trends.ComputeInterval(evs, ev, from, to, unique, iv)
		ser := formula.Series{Name: key}
		for _, p := range res.Points {
			ser.Dates = append(ser.Dates, p.Date)
			ser.Values = append(ser.Values, float64(p.Count))
		}
		inputs = append(inputs, ser)
		names[key] = ev
	}
	if len(inputs) == 0 {
		writeErr(w, http.StatusBadRequest,
			"no series given — name at least one, e.g. &A=signup, and refer to it as A in expr")
		return
	}
	out, ferr := formula.Evaluate(expr, inputs)
	if ferr != nil {
		writeErr(w, http.StatusBadRequest, ferr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expression": out.Expression, "series": names, "points": out.Points,
		"defined": out.Defined, "undefined": out.Undefined, "note": out.Note,
		"unique": unique,
		"from":   from.UTC().Format(time.RFC3339), "to": to.UTC().Format(time.RFC3339),
	})
}
