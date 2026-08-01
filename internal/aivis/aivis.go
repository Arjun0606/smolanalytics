// Package aivis aggregates $geo_check events — AI-visibility sampling results the
// cloud runner (or any self-hoster's script) records as ordinary events on the
// instance. Storing checks AS EVENTS is the design decision that separates this
// module from the incumbents (Profound, Peec): every existing surface (dashboard,
// /v1, MCP, ask bar, alerts, the verdict) reads the same single query path, so
// "what does Claude say about us" carries the same agreement guarantee as a funnel
// — and visibility shifts can be computed NEXT TO the ai_referrers traffic truth,
// which is the half of the story the GEO tools don't hold.
//
// Event contract (properties on $geo_check):
//   engine        string  — "claude", "chatgpt", "perplexity", ...
//   prompt        string  — the question asked, e.g. "best analytics for indie devs"
//   mentioned     bool    — product named anywhere in the answer
//   recommended   bool    — product recommended / listed as a pick
//   rank          number  — 1-based position among named tools (0 = unranked)
//   competitors   string  — comma-separated competitor names seen in the answer
//   verbatim      string  — one-line excerpt of how the answer describes us
//   model_version string  — exact model id the sample ran against
//
// Deterministic like every other report. Low-sample honesty is the caller-facing
// rule: rates always ship with their run counts, and Result.Note flags thin data.
package aivis

import (
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

const checkEvent = "$geo_check"

// EngineRow is one AI engine's visibility over the window.
type EngineRow struct {
	Engine         string  `json:"engine"`
	Runs           int     `json:"runs"`
	MentionedPct   int     `json:"mentioned_pct"`   // % of runs that mention the product
	RecommendedPct int     `json:"recommended_pct"` // % of runs that recommend it
	AvgRank        float64 `json:"avg_rank"`        // mean 1-based rank when ranked; 0 = never ranked
	LatestVerbatim string  `json:"latest_verbatim"` // how the newest run describes us
	LatestModel    string  `json:"latest_model"`
	LatestAt       string  `json:"latest_at"` // RFC3339
}

// PromptRow is one tracked prompt's outcome across engines.
type PromptRow struct {
	Prompt         string `json:"prompt"`
	Runs           int    `json:"runs"`
	MentionedPct   int    `json:"mentioned_pct"`
	RecommendedPct int    `json:"recommended_pct"`
}

// CompetitorRow counts how often a competitor shows up in sampled answers.
type CompetitorRow struct {
	Name     string `json:"name"`
	Mentions int    `json:"mentions"`
}

// WeekPoint is share-of-voice over time: of all runs that week, % mentioning us.
type WeekPoint struct {
	WeekStart      string `json:"week_start"` // YYYY-MM-DD (UTC, Monday)
	Runs           int    `json:"runs"`
	MentionedPct   int    `json:"mentioned_pct"`
	RecommendedPct int    `json:"recommended_pct"`
}

// Result is the AI-visibility report.
type Result struct {
	Days        int             `json:"days"`
	Checks      int             `json:"checks"` // total sampled runs in window
	Engines     []EngineRow     `json:"engines"`
	Prompts     []PromptRow     `json:"prompts"`
	Competitors []CompetitorRow `json:"competitors"`
	Trend       []WeekPoint     `json:"trend"`
	// Note carries the low-sample warning ("" when the data clears the bar) — the
	// same honesty rule the funnel's timing stats and whats_notable follow.
	Note string `json:"note,omitempty"`
}

func asBool(v any) bool { x, _ := v.(bool); return x }
func asStr(v any) string {
	x, _ := v.(string)
	return strings.TrimSpace(x)
}
func asNum(v any) float64 { x, _ := v.(float64); return x }

func pct(part, whole int) int {
	if whole == 0 {
		return 0
	}
	return int(float64(part)/float64(whole)*100 + 0.5)
}

// Compute aggregates the window's $geo_check events. asof zero = now.
func Compute(evs []event.Event, days int, asof time.Time) Result {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	if days <= 0 {
		days = 90 // visibility moves slowly; default to a quarter
	}
	cutoff := asof.Add(-time.Duration(days) * 24 * time.Hour)

	type agg struct {
		runs, mentioned, recommended int
		rankSum                      float64
		ranked                       int
		latestAt                     time.Time
		latestVerbatim, latestModel  string
	}
	engines := map[string]*agg{}
	prompts := map[string]*agg{}
	competitors := map[string]int{}
	weeks := map[string]*agg{}
	total := 0

	for _, e := range evs {
		if e.Name != checkEvent || e.Timestamp.Before(cutoff) || e.Timestamp.After(asof) {
			continue
		}
		total++
		eng := asStr(e.Properties["engine"])
		if eng == "" {
			eng = "unknown"
		}
		pr := asStr(e.Properties["prompt"])
		mentioned, recommended := asBool(e.Properties["mentioned"]), asBool(e.Properties["recommended"])
		rank := asNum(e.Properties["rank"])

		bump := func(m map[string]*agg, k string) *agg {
			a := m[k]
			if a == nil {
				a = &agg{}
				m[k] = a
			}
			a.runs++
			if mentioned {
				a.mentioned++
			}
			if recommended {
				a.recommended++
			}
			if rank >= 1 {
				a.rankSum += rank
				a.ranked++
			}
			return a
		}
		ea := bump(engines, eng)
		if e.Timestamp.After(ea.latestAt) {
			ea.latestAt = e.Timestamp
			ea.latestVerbatim = asStr(e.Properties["verbatim"])
			ea.latestModel = asStr(e.Properties["model_version"])
		}
		if pr != "" {
			bump(prompts, pr)
		}
		// week bucket: Monday of the event's UTC week
		ts := e.Timestamp.UTC()
		monday := ts.AddDate(0, 0, -((int(ts.Weekday()) + 6) % 7)).Format("2006-01-02")
		bump(weeks, monday)

		for _, c := range strings.Split(asStr(e.Properties["competitors"]), ",") {
			if c = strings.TrimSpace(c); c != "" {
				competitors[c]++
			}
		}
	}

	res := Result{Days: days, Checks: total}
	for name, a := range engines {
		row := EngineRow{
			Engine: name, Runs: a.runs,
			MentionedPct: pct(a.mentioned, a.runs), RecommendedPct: pct(a.recommended, a.runs),
			LatestVerbatim: a.latestVerbatim, LatestModel: a.latestModel,
		}
		if a.ranked > 0 {
			row.AvgRank = float64(int(a.rankSum/float64(a.ranked)*10+0.5)) / 10
		}
		if !a.latestAt.IsZero() {
			row.LatestAt = a.latestAt.UTC().Format(time.RFC3339)
		}
		res.Engines = append(res.Engines, row)
	}
	sort.Slice(res.Engines, func(i, j int) bool {
		if res.Engines[i].Runs != res.Engines[j].Runs {
			return res.Engines[i].Runs > res.Engines[j].Runs
		}
		return res.Engines[i].Engine < res.Engines[j].Engine
	})
	for p, a := range prompts {
		res.Prompts = append(res.Prompts, PromptRow{Prompt: p, Runs: a.runs, MentionedPct: pct(a.mentioned, a.runs), RecommendedPct: pct(a.recommended, a.runs)})
	}
	sort.Slice(res.Prompts, func(i, j int) bool {
		if res.Prompts[i].Runs != res.Prompts[j].Runs {
			return res.Prompts[i].Runs > res.Prompts[j].Runs
		}
		return res.Prompts[i].Prompt < res.Prompts[j].Prompt
	})
	for c, n := range competitors {
		res.Competitors = append(res.Competitors, CompetitorRow{Name: c, Mentions: n})
	}
	sort.Slice(res.Competitors, func(i, j int) bool {
		if res.Competitors[i].Mentions != res.Competitors[j].Mentions {
			return res.Competitors[i].Mentions > res.Competitors[j].Mentions
		}
		return res.Competitors[i].Name < res.Competitors[j].Name
	})
	var weekKeys []string
	for k := range weeks {
		weekKeys = append(weekKeys, k)
	}
	sort.Strings(weekKeys)
	for _, k := range weekKeys {
		a := weeks[k]
		res.Trend = append(res.Trend, WeekPoint{WeekStart: k, Runs: a.runs, MentionedPct: pct(a.mentioned, a.runs), RecommendedPct: pct(a.recommended, a.runs)})
	}
	// a single-digit sample is an anecdote, and per-engine slices thinner still —
	// say so in the payload, mirroring the funnel's timing_note discipline.
	if total > 0 && total < 10 {
		res.Note = "visibility rates are computed from under 10 sampled runs — directional at best until more checks accumulate"
	}
	return res
}
