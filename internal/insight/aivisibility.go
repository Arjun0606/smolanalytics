package insight

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

// The finding no other tool can produce. GEO monitors (Profound, Peec) hold what the
// models SAY about you; web analytics holds what the models SEND you. This instance
// holds both — sampled answers arrive as $geo_check events and AI referrals fall out
// of the same $pageview stream — so one sentence can carry both halves, computed over
// the identical pair of weeks, with no cross-product join to get wrong.
//
// The whole rule is a suppression problem. LLM answers resample loudly: a published
// 12,933-response study puts within-prompt resampling at ~34.8% of total variance, so
// a rate off a handful of runs is a coin flip with a percentage sign. A verdict that
// says "Claude stopped recommending you" when our own sampler simply missed a day is
// the single most expensive thing this module could ship, so it stays silent instead.

// geoMinRuns is the per-engine run floor on EACH side of the comparison. It is
// deliberately the same number as minSample: a rate computed from sampled model
// answers earns no laxer a bar than a rate computed from user events. aivis's own
// note fires under 10 runs, but that is a caveat printed on a report the reader
// asked for — pushing an unasked-for verdict off the same data is a different act.
const geoMinRuns = minSample

// geoMinDays is how many distinct UTC days a window's runs must touch. The recipe
// samples daily and reports a 7-day rolling aggregate precisely because one day's
// batch is one model version, one API mood, one prompt-set edit. Without this gate a
// cron that ran Monday and then broke reads as "Claude stopped recommending you":
// we would be publishing our own downtime as the engine's opinion.
const geoMinDays = 3

// geoMinShiftPP is the smallest move worth a reader's attention, in percentage
// points. Below it the finding is suppressed even when it is statistically real,
// because the digest ships actions and "recommended 4 points less often" is not one.
const geoMinShiftPP = 15.0

// geoDesignEffect inflates the variance for how the sampling is actually done: 3 runs
// per prompt over a FIXED prompt set, so runs are clustered, not independent draws.
// With within-prompt variance at ~35% of the total the intra-prompt correlation is
// ~0.65, and 3 runs per cluster inflate the variance by 1 + (3-1)*0.65 ≈ 2.3. Treating
// the runs as independent would make every threshold here ~1.5x too eager to call a
// shift, which is the exact failure the GEO incumbents get criticised for.
const geoDesignEffect = 2.3

// geoRunRatio caps how far the two windows' run counts may diverge. A prompt set that
// halved or doubled is a different set of questions, and a rate compared across it
// measures OUR edit, not the engine's opinion.
const geoRunRatio = 2

// engineLabel names an engine the way the reader thinks of it. The mode of the sample
// is encoded in the engine string ("claude" vs "claude-grounded" — ungrounded asks
// what the model knows, grounded asks what live search surfaces), which is our
// storage convention and not a word anyone says out loud.
var engineLabel = map[string]string{
	"claude": "Claude", "chatgpt": "ChatGPT", "openai": "ChatGPT",
	"perplexity": "Perplexity", "gemini": "Gemini", "copilot": "Copilot",
	"grok": "Grok", "deepseek": "DeepSeek", "you": "You.com",
}

// HumanEngine renders an engine name for prose. An engine we have not met still has
// to read as a name rather than as a row key, and the "-grounded" suffix has to
// become the thing it actually means.
func HumanEngine(name string) string {
	base := strings.TrimSuffix(name, "-grounded")
	label, ok := engineLabel[strings.ToLower(base)]
	if !ok {
		label = capFirst(base)
	}
	if strings.HasSuffix(name, "-grounded") {
		return label + " with web search"
	}
	return label
}

// aiVisibilityShift compares this week's sampled answers against last week's, per
// engine, and pairs the biggest surviving move with what the AI assistants actually
// sent the site over the same two weeks.
//
// evs must be the UNFILTERED slice: the $geo_check events are the input here, even
// though every other finding in the digest is computed with them removed.
func aiVisibilityShift(evs []event.Event, now time.Time) *Finding {
	const week = 7 * 24 * time.Hour
	cur := aivis.Compute(evs, 7, now)
	if cur.Checks == 0 {
		return nil
	}
	prev := aivis.Compute(evs, 7, now.Add(-week))
	if prev.Checks == 0 {
		return nil // a first week of sampling is a baseline, not a shift
	}
	// Day coverage is a gate, never a printed number, so counting timestamps here does
	// not put a second definition of a rate into the world. The window bounds mirror
	// aivis.Compute exactly — a gate measured over a different week than the rates it
	// guards would suppress moves the report shows, and pass moves it does not.
	curDays := geoSamplingDays(evs, now.Add(-week), now)
	prevDays := geoSamplingDays(evs, now.Add(-2*week), now.Add(-week))

	prevByEngine := make(map[string]aivis.EngineRow, len(prev.Engines))
	for _, e := range prev.Engines {
		prevByEngine[e.Engine] = e
	}

	// the winner, held as data so the traffic half is computed once, after the loop
	var (
		found                    bool
		bestMove                 float64
		engine, verb             string
		curPart, curRuns, curPct int
		oldPart, oldRuns, oldPct int
	)
	for _, c := range cur.Engines {
		if c.Engine == "" || c.Engine == "unknown" {
			continue // an engine that did not name itself cannot be named in a sentence
		}
		p, ok := prevByEngine[c.Engine]
		if !ok {
			continue
		}
		if c.Runs < geoMinRuns || p.Runs < geoMinRuns {
			continue
		}
		if curDays[c.Engine] < geoMinDays || prevDays[c.Engine] < geoMinDays {
			continue
		}
		if max(c.Runs, p.Runs) > geoRunRatio*min(c.Runs, p.Runs) {
			continue
		}

		// Being picked is the metric that pays, so it leads. Being named at all is the
		// wider signal, and when THAT is what moved — the pick rate held but the engine
		// stopped naming you — reporting the smaller move would bury the story.
		v, cp, op := "recommends", c.Recommended, p.Recommended
		cpct, opct := c.RecommendedPct, p.RecommendedPct
		move := math.Abs(rateOf(c.Recommended, c.Runs) - rateOf(p.Recommended, p.Runs))
		if m := math.Abs(rateOf(c.Mentioned, c.Runs) - rateOf(p.Mentioned, p.Runs)); m > move {
			v, cp, op, cpct, opct, move = "mentions", c.Mentioned, p.Mentioned, c.MentionedPct, p.MentionedPct, m
		}
		if move*100 < geoMinShiftPP {
			continue
		}
		if move < geoNoiseBand(cp, c.Runs, op, p.Runs) {
			continue
		}
		if found && move <= bestMove {
			continue // biggest move wins; aivis already sorts engines deterministically
		}
		found, bestMove, engine, verb = true, move, c.Engine, v
		curPart, curRuns, curPct = cp, c.Runs, cpct
		oldPart, oldRuns, oldPct = op, p.Runs, opct
	}
	if !found {
		return nil
	}

	dir, sev := "up", "info"
	if curPct < oldPct {
		dir, sev = "down", "warn"
	}
	// Deliberately NOT run through qualify(): the run counts ARE the small-sample
	// caveat, said in the reader's words. "4 of 21 this week, down from 14 of 21
	// (n=21, small sample)" tells them the same thing twice, the second time in ours.
	return &Finding{
		Severity: sev,
		Kind:     KindAIVis,
		Metric:   engine,
		Rate:     curPct,
		N:        curRuns,
		Title: fmt.Sprintf("%s %s you in %d%% of AI answers, %s from %d%%",
			HumanEngine(engine), verb, curPct, dir, oldPct),
		Detail: fmt.Sprintf("%d of %d sampled answers this week, %s from %d of %d the week before. %s",
			curPart, curRuns, dir, oldPart, oldRuns, aiTrafficClause(evs, now)),
	}
}

// aiTrafficClause is the other half: what the AI assistants actually SENT over the
// same two weeks. It calls web.ComputeWindow rather than counting referrers here so
// the number in the verdict is literally the number the web pane shows for AI
// visitors — first-touch attribution, host normalisation and the utm_source fallback
// included. Called once, after the visibility gate has already passed, because two
// extra full passes over the events are not worth paying for a finding we suppress.
func aiTrafficClause(evs []event.Event, now time.Time) string {
	const week = 7 * 24 * time.Hour
	cur := web.ComputeWindow(evs, week, now)
	prev := web.ComputeWindow(evs, week, now.Add(-week))

	// A GEO-only instance (checks arriving before the SDK is installed) has no traffic
	// side at all. Saying "no visitors arrived from an AI assistant" there would be a
	// measurement we never made dressed as a result.
	if cur.Pageviews == 0 && prev.Pageviews == 0 {
		return "No pageviews are recorded here for either week, so there is no traffic side to weigh this against yet."
	}
	if cur.AIVisitors == 0 && prev.AIVisitors == 0 {
		return "Nobody has arrived from an AI assistant in either week, so this is a leading indicator only."
	}
	if prev.AIVisitors < minSample {
		return fmt.Sprintf("Visitors arriving from AI assistants: %d this week against %d last week, too few either way to call a trend.",
			cur.AIVisitors, prev.AIVisitors)
	}
	// same +/-15% band the week-over-week finding treats as a real move, so the digest
	// does not call 12% a change in one sentence and flat in the next
	change := int(math.Round(float64(cur.AIVisitors-prev.AIVisitors) / float64(prev.AIVisitors) * 100))
	switch {
	case change <= -15:
		return fmt.Sprintf("Visitors arriving from AI assistants fell %d%% over the same two weeks, to %d from %d.",
			absInt(change), cur.AIVisitors, prev.AIVisitors)
	case change >= 15:
		return fmt.Sprintf("Visitors arriving from AI assistants rose %d%% over the same two weeks, to %d from %d.",
			change, cur.AIVisitors, prev.AIVisitors)
	default:
		return fmt.Sprintf("Visitors arriving from AI assistants held flat over the same two weeks, %d against %d.",
			cur.AIVisitors, prev.AIVisitors)
	}
}

// geoSamplingDays counts, per engine, how many distinct UTC days in the window carried
// at least one run. Bounds mirror aivis.Compute's inclusive asof / inclusive cutoff.
func geoSamplingDays(evs []event.Event, from, to time.Time) map[string]int {
	seen := map[string]map[string]bool{}
	for _, e := range evs {
		if e.Name != aivis.CheckEvent || e.Timestamp.Before(from) || e.Timestamp.After(to) {
			continue
		}
		eng, _ := e.Properties["engine"].(string)
		if eng = strings.TrimSpace(eng); eng == "" {
			eng = "unknown" // aivis buckets a missing engine the same way
		}
		if seen[eng] == nil {
			seen[eng] = map[string]bool{}
		}
		seen[eng][e.Timestamp.UTC().Format("2006-01-02")] = true
	}
	out := make(map[string]int, len(seen))
	for eng, days := range seen {
		out[eng] = len(days)
	}
	return out
}

// geoNoiseBand is how far a week-over-week move must travel before it is a shift
// rather than the model resampling itself: two standard errors of the difference
// between the two rates, with the variance inflated by the clustered design.
//
// The proportions are smoothed Agresti-Coull style ((x+1)/(n+2)) first. The plain
// Wald error collapses to exactly zero at p=0 and p=1, so "20 of 20 last week, 0 of
// 20 this week" would clear an empty band at any sample size — including one built
// from a single prompt asked twice.
func geoNoiseBand(curPart, curRuns, oldPart, oldRuns int) float64 {
	if curRuns == 0 || oldRuns == 0 {
		return 1 // unreachable past the run floor; a band of 1 suppresses rather than fires
	}
	p1 := (float64(curPart) + 1) / (float64(curRuns) + 2)
	p2 := (float64(oldPart) + 1) / (float64(oldRuns) + 2)
	v := p1*(1-p1)/float64(curRuns) + p2*(1-p2)/float64(oldRuns)
	return 2 * math.Sqrt(v*geoDesignEffect)
}

func rateOf(part, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(part) / float64(n)
}

// withoutGeoChecks removes the sampler's own writes — the AI-visibility checks AND the
// site-readability scans — from the product-activity view.
// They come from the GEO runner under one synthetic distinct_id on a daily schedule,
// so left in they read as a user who returns every single day (retention), as a
// high-volume event competing for the anomaly slot, and as a candidate funnel step
// whose name has no reader-facing wording at all. Returns the input untouched when
// there are none, which is every instance that never turned GEO on.
func withoutGeoChecks(evs []event.Event) []event.Event {
	ours := func(name string) bool { return name == aivis.CheckEvent || name == ReadableEvent }
	n := 0
	for _, e := range evs {
		if ours(e.Name) {
			n++
		}
	}
	if n == 0 {
		return evs
	}
	out := make([]event.Event, 0, len(evs)-n)
	for _, e := range evs {
		if !ours(e.Name) {
			out = append(out, e)
		}
	}
	return out
}

// geoMinCited is the floor for the cited-not-recommended finding. Lower than geoMinRuns on
// purpose: only GROUNDED runs can cite anything, so this denominator is a quarter of the
// sampling by construction, and holding it to the same bar would silence a real and
// actionable diagnosis for months.
const geoMinCited = 8

// citedNotRecommended is the diagnosis every competing tool collapses. Models choose the
// brands they name from what they already know and retrieve sources afterwards, so "your page
// was read and a rival got the recommendation" is not a content problem — writing more pages
// will not fix it. It is a problem of what the model learned about your category before it
// ever saw your site, which is fixed off-site: reviews, directories, being in the listicles
// the model trained on. Saying that plainly is worth more than another percentage.
func citedNotRecommended(evs []event.Event, now time.Time) *Finding {
	r := aivis.Compute(evs, 30, now)
	if r.CitedRuns < geoMinCited {
		return nil
	}
	// only fires when the gap is the STORY: read most of the time, picked rarely
	if r.CitedNotRecommended*100 < r.CitedRuns*70 {
		return nil
	}
	return &Finding{
		Severity: "warn",
		Kind:     KindAIVis,
		Rate:     pctOf(r.CitedNotRecommended, r.CitedRuns),
		N:        r.CitedRuns,
		Title:    fmt.Sprintf("AI search reads your site and still picks someone else, %d times out of %d", r.CitedNotRecommended, r.CitedRuns),
		Detail: fmt.Sprintf("Of %d web-search answers that cited your own domain, %d recommended a competitor instead. That is not a content problem — the page was found and read. Models pick the names they know before they fetch anything, so this is decided off your site: reviews, directory listings, and being named in the roundups the model learned from.",
			r.CitedRuns, r.CitedNotRecommended),
	}
}

func pctOf(part, whole int) int {
	if whole == 0 {
		return 0
	}
	return int(float64(part)/float64(whole)*100 + 0.5)
}
