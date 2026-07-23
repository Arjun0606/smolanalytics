// Package survey is in-product micro-surveys — one question (NPS, rating, choice, or text),
// targeted by URL + sampling, answered by a tiny SDK widget. Responses arrive as ordinary events
// ($survey_shown / $survey_response), so results are a query-time aggregation like every other
// report — no separate response store, honoring the single-binary model.
package survey

import (
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

const (
	ShownEvent    = "$survey_shown"
	ResponseEvent = "$survey_response"
	PropSurvey    = "survey_id"
	PropAnswer    = "answer"
	maxRecent     = 20
)

// Survey is one saved micro-survey. One question keeps the widget tiny and the results legible;
// multi-step surveys are a deliberate later add.
type Survey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // nps | rating | choice | text
	Question  string    `json:"question"`
	Choices   []string  `json:"choices,omitempty"`    // for type=choice
	URLMatch  string    `json:"url_match,omitempty"`  // path substring; empty = every page
	SamplePct int       `json:"sample_pct,omitempty"` // 0 or 100 = everyone
	Active    bool      `json:"active"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// Count is a labeled tally (NPS buckets, choice options).
type Count struct {
	Label string `json:"label"`
	N     int    `json:"n"`
}

// Result is the aggregate read for one survey.
type Result struct {
	Survey    string   `json:"survey"`
	Type      string   `json:"type"`
	Shown     int      `json:"shown"`     // distinct users who saw it
	Responses int      `json:"responses"` // distinct users who answered
	RatePct   float64  `json:"rate_pct"`
	NPS       *int     `json:"nps,omitempty"`       // type=nps: %promoters - %detractors
	Average   *float64 `json:"average,omitempty"`   // type=rating
	Breakdown []Count  `json:"breakdown,omitempty"` // nps buckets / choice counts
	Recent    []string `json:"recent,omitempty"`    // type=text, most recent first
	Note      string   `json:"note,omitempty"`
}

// Results aggregates $survey_shown / $survey_response events for one survey. Pure + deterministic
// (stable sort order) so /v1 and MCP agree byte-for-byte.
func Results(evs []event.Event, surveyID, surveyType string, days int) Result {
	var cutoff time.Time
	if days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}
	shown := map[string]bool{}
	responded := map[string]bool{}
	var answers []any
	for _, e := range evs {
		if days > 0 && e.Timestamp.Before(cutoff) {
			continue
		}
		if sid, _ := e.Properties[PropSurvey].(string); sid != surveyID {
			continue
		}
		switch e.Name {
		case ShownEvent:
			shown[e.DistinctID] = true
		case ResponseEvent:
			responded[e.DistinctID] = true
			answers = append(answers, e.Properties[PropAnswer])
		}
	}
	res := Result{Survey: surveyID, Type: surveyType, Shown: len(shown), Responses: len(responded)}
	if res.Shown > 0 {
		res.RatePct = round1(100 * float64(res.Responses) / float64(res.Shown))
	}
	switch surveyType {
	case "nps":
		prom, pass, det := 0, 0, 0
		for _, a := range answers {
			switch n := toInt(a); {
			case n >= 9:
				prom++
			case n >= 7:
				pass++
			default:
				det++
			}
		}
		if total := prom + pass + det; total > 0 {
			score := int(math.Round(100 * float64(prom-det) / float64(total)))
			res.NPS = &score
		}
		res.Breakdown = []Count{{"promoters (9-10)", prom}, {"passives (7-8)", pass}, {"detractors (0-6)", det}}
	case "rating":
		sum, n := 0.0, 0
		for _, a := range answers {
			sum += toFloat(a)
			n++
		}
		if n > 0 {
			avg := round1(sum / float64(n))
			res.Average = &avg
		}
	case "choice":
		counts := map[string]int{}
		for _, a := range answers {
			if s, _ := a.(string); s != "" {
				counts[s]++
			}
		}
		for lbl, n := range counts {
			res.Breakdown = append(res.Breakdown, Count{lbl, n})
		}
		sort.Slice(res.Breakdown, func(i, j int) bool {
			if res.Breakdown[i].N != res.Breakdown[j].N {
				return res.Breakdown[i].N > res.Breakdown[j].N
			}
			return res.Breakdown[i].Label < res.Breakdown[j].Label
		})
	case "text":
		for i := len(answers) - 1; i >= 0 && len(res.Recent) < maxRecent; i-- {
			if s, _ := answers[i].(string); s != "" {
				res.Recent = append(res.Recent, s)
			}
		}
	}
	if res.Responses == 0 {
		res.Note = "no responses yet — activate the survey and let the SDK widget collect answers"
	}
	return res
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
func round1(f float64) float64 { return math.Round(f*10) / 10 }
