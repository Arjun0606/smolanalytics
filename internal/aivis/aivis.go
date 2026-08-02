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
//
//	engine        string  — "claude", "chatgpt", "perplexity", ...
//	prompt        string  — the question asked, e.g. "best analytics for indie devs"
//	mentioned     bool    — product named anywhere in the answer
//	recommended   bool    — product recommended / listed as a pick
//	rank          number  — 1-based position among named tools (0 = unranked)
//	competitors   string  — comma-separated competitor names seen in the answer
//	verbatim      string  — one-line excerpt of how the answer describes us
//	model_version string  — exact model id the sample ran against
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

// CheckEvent is the event name the GEO runner writes. Exported because other packages
// must recognise the sampler's own writes — the verdict engine has to keep them OUT of
// the product-activity view and IN the visibility view, and a second copy of the literal
// is a drift waiting to happen.
const CheckEvent = "$geo_check"

// usBrand labels this product's own series in the share-of-voice chart. A literal "you"
// rather than the brand name: the chart is read by the one person it is about, and a
// legend that says "you" against three competitor names needs no explaining.
const usBrand = "you"

// EngineRow is one AI engine's visibility over the window. Raw counts ship beside
// every percentage on purpose: "recommended in 3 of 5 runs" is a fact a reader can
// judge, "60%" over an unstated denominator is not — and re-deriving the count from
// the rate downstream would both round wrong and create a second computed path.
type EngineRow struct {
	Engine         string  `json:"engine"`
	Runs           int     `json:"runs"`
	Mentioned      int     `json:"mentioned"`       // runs that mention the product
	Recommended    int     `json:"recommended"`     // runs that recommend it
	MentionedPct   int     `json:"mentioned_pct"`   // % of runs that mention the product
	RecommendedPct int     `json:"recommended_pct"` // % of runs that recommend it
	Ranked         int     `json:"ranked"`          // runs that gave a 1-based rank (AvgRank's denominator)
	AvgRank        float64 `json:"avg_rank"`        // mean 1-based rank when ranked; 0 = never ranked
	LatestVerbatim string  `json:"latest_verbatim"` // how the newest run describes us
	LatestModel    string  `json:"latest_model"`
	LatestAt       string  `json:"latest_at"` // RFC3339
}

// PromptRow is one tracked prompt's outcome across engines.
type PromptRow struct {
	Prompt         string `json:"prompt"`
	Runs           int    `json:"runs"`
	Mentioned      int    `json:"mentioned"`
	Recommended    int    `json:"recommended"`
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

// BrandSeries is one brand's presence across the window — us, or a competitor named
// alongside us. This is the raw material for the share-of-voice chart: the question a
// GEO report exists to answer is not "how visible are we" in isolation, it is "how
// visible are we COMPARED TO the alternatives the model keeps naming".
type BrandSeries struct {
	Name     string `json:"name"`
	Us       bool   `json:"us"`        // the product this instance belongs to
	Weekly   []int  `json:"weekly"`    // mentions per week, index-aligned to Result.Weeks
	Total    int    `json:"total"`     // mentions across the whole window
	SharePct int    `json:"share_pct"` // total as a share of every brand mention in the window
}

// SentimentRoll counts how the answers that DID mention us described us. Absent is
// counted separately from negative on purpose: not being named is not the same as
// being named badly, and collapsing them would turn silence into criticism.
type SentimentRoll struct {
	Positive int `json:"positive"`
	Neutral  int `json:"neutral"`
	Negative int `json:"negative"`
	Absent   int `json:"absent"`
	Rated    int `json:"rated"` // runs carrying any sentiment at all (the denominator)
}

// RankBucket is how often we land in each position of the lists that rank us. "4+" is
// one bucket because the difference between 7th and 9th is noise at these sample sizes.
type RankBucket struct {
	Label string `json:"label"` // "1st" | "2nd" | "3rd" | "4+"
	Runs  int    `json:"runs"`
	Pct   int    `json:"pct"`
}

// Result is the AI-visibility report.
type Result struct {
	Days        int             `json:"days"`
	Checks      int             `json:"checks"` // total sampled runs in window
	Engines     []EngineRow     `json:"engines"`
	Prompts     []PromptRow     `json:"prompts"`
	Competitors []CompetitorRow `json:"competitors"`
	Trend       []WeekPoint     `json:"trend"`
	// Weeks is the shared x-axis every series below is aligned to, so a chart can plot
	// them together without re-deriving buckets and drifting from Trend.
	Weeks     []string      `json:"weeks"`
	Share     []BrandSeries `json:"share"`
	Sentiment SentimentRoll `json:"sentiment"`
	RankDist  []RankBucket  `json:"rank_dist"`
	// CitedRate is how often an answer cited THIS product's own domain — content earning
	// retrieval, which is a different achievement from being named from memory.
	CitedRate  int `json:"cited_rate"`
	CitedRuns  int `json:"cited_runs"`
	GroundRuns int `json:"ground_runs"` // grounded runs, the only ones that can cite anything
	// CitedNotRecommended is the split that every tool in this category collapses. A model
	// picks the brands it names from what it already knows, THEN goes and fetches sources —
	// so your page being cited while a competitor gets the recommendation is a completely
	// different failure from never being retrieved at all, and it has a different fix.
	// Reported as counts because the denominator here is small by construction.
	CitedRecommended    int `json:"cited_recommended"`
	CitedNotRecommended int `json:"cited_not_recommended"`
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
	// share-of-voice raw material: per week, how many times each brand was named. "us" is
	// counted from `mentioned` and competitors from the `competitors` property, so both
	// sides of the comparison come off the same sampled answer.
	brandWeek := map[string]map[string]int{} // brand -> weekStart -> mentions
	weekSeen := map[string]bool{}
	var sent SentimentRoll
	ranks := map[int]int{}
	citedRuns, groundRuns, citedRec := 0, 0, 0

	for _, e := range evs {
		if e.Name != CheckEvent || e.Timestamp.Before(cutoff) || e.Timestamp.After(asof) {
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

		weekSeen[monday] = true
		bump2 := func(brand string) {
			if brandWeek[brand] == nil {
				brandWeek[brand] = map[string]int{}
			}
			brandWeek[brand][monday]++
		}
		if mentioned {
			bump2(usBrand)
		}
		for _, c := range strings.Split(asStr(e.Properties["competitors"]), ",") {
			if c = strings.TrimSpace(c); c != "" {
				competitors[c]++
				bump2(c)
			}
		}

		switch asStr(e.Properties["sentiment"]) {
		case "positive":
			sent.Positive++
			sent.Rated++
		case "neutral":
			sent.Neutral++
			sent.Rated++
		case "negative":
			sent.Negative++
			sent.Rated++
		case "absent":
			sent.Absent++
			sent.Rated++
		}
		if r := int(rank); r >= 1 {
			if r > 4 {
				r = 4 // 7th vs 9th is noise at these sample sizes
			}
			ranks[r]++
		}
		// only a grounded run can cite anything, so it is the honest denominator for a
		// citation rate — dividing by every run would understate it by the ungrounded share
		if strings.HasSuffix(eng, "-grounded") {
			groundRuns++
			if asBool(e.Properties["cited_domain"]) {
				citedRuns++
				if recommended {
					citedRec++
				}
			}
		}
	}

	res := Result{Days: days, Checks: total}
	for name, a := range engines {
		row := EngineRow{
			Engine: name, Runs: a.runs, Mentioned: a.mentioned, Recommended: a.recommended,
			MentionedPct: pct(a.mentioned, a.runs), RecommendedPct: pct(a.recommended, a.runs),
			Ranked:         a.ranked,
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
		res.Prompts = append(res.Prompts, PromptRow{Prompt: p, Runs: a.runs, Mentioned: a.mentioned, Recommended: a.recommended, MentionedPct: pct(a.mentioned, a.runs), RecommendedPct: pct(a.recommended, a.runs)})
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
	// --- the shared x-axis, then every series aligned to it ---
	for w := range weekSeen {
		res.Weeks = append(res.Weeks, w)
	}
	sort.Strings(res.Weeks)
	idx := make(map[string]int, len(res.Weeks))
	for i, w := range res.Weeks {
		idx[w] = i
	}
	allMentions := 0
	for _, byWeek := range brandWeek {
		for _, n := range byWeek {
			allMentions += n
		}
	}
	for brand, byWeek := range brandWeek {
		row := BrandSeries{Name: brand, Us: brand == usBrand, Weekly: make([]int, len(res.Weeks))}
		for w, n := range byWeek {
			row.Weekly[idx[w]] = n
			row.Total += n
		}
		row.SharePct = pct(row.Total, allMentions)
		res.Share = append(res.Share, row)
	}
	// us first so the chart's primary series is unambiguous, then the loudest competitors
	sort.Slice(res.Share, func(i, j int) bool {
		if res.Share[i].Us != res.Share[j].Us {
			return res.Share[i].Us
		}
		if res.Share[i].Total != res.Share[j].Total {
			return res.Share[i].Total > res.Share[j].Total
		}
		return res.Share[i].Name < res.Share[j].Name
	})
	res.Sentiment = sent
	for _, b := range []struct {
		label string
		key   int
	}{{"1st", 1}, {"2nd", 2}, {"3rd", 3}, {"4+", 4}} {
		if n := ranks[b.key]; n > 0 {
			res.RankDist = append(res.RankDist, RankBucket{Label: b.label, Runs: n, Pct: pct(n, rankedTotal(ranks))})
		}
	}
	res.CitedRuns, res.GroundRuns = citedRuns, groundRuns
	res.CitedRate = pct(citedRuns, groundRuns)
	res.CitedRecommended, res.CitedNotRecommended = citedRec, citedRuns-citedRec

	// a single-digit sample is an anecdote, and per-engine slices thinner still —
	// say so in the payload, mirroring the funnel's timing_note discipline.
	if total > 0 && total < 10 {
		res.Note = "visibility rates are computed from under 10 sampled runs — directional at best until more checks accumulate"
	}
	return res
}

// rankedTotal is the denominator for the rank distribution: only the runs that actually
// ranked us. Dividing by every run would report "10% first place" for a product that was
// first every single time it was ranked at all.
func rankedTotal(ranks map[int]int) int {
	t := 0
	for _, n := range ranks {
		t += n
	}
	return t
}
