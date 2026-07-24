package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/heatmap"
	"github.com/Arjun0606/smolanalytics/internal/session"
	"github.com/Arjun0606/smolanalytics/internal/survey"
)

// answerToolkit answers the ask-bar intents that reach the product toolkit shipped in
// v0.9.6+ (heatmaps, surveys, feature flags, A/B, the session inspector, deploy impact,
// sequenced cohorts). These need store access (flags/surveys/deploys) the pure answer()
// path lacks, so s.ask dispatches here FIRST. Every answer is computed from the SAME
// report the /v1 endpoint and the MCP tool run, so the number is real and the receipt
// stays honest. Crucially, a toolkit question can no longer fall through to a funnel or
// signup count wearing a "proven" stamp (the exact failure that burned a live demo):
// it either answers the real report or says honestly where to get it. ok=false means the
// intent isn't a toolkit one and the caller should use the normal answer() path.
func (s *Server) answerToolkit(intent askIntent, q string, evs []event.Event, now time.Time) (answer, receipt string, ok bool) {
	switch intent {
	case intentHeatmap:
		path := tkPath(q, evs)
		res := heatmap.Compute(evs, path, "", 0, 0)
		if res.Clicks == 0 {
			return fmt.Sprintf("No positioned click data on %s yet. Once the SDK autocaptures $click events there (viewport included), the heatmap fills in. Open the Heatmap tab to pick a page.", path),
				tkReceipt("the click-heatmap report over "+path, "Heatmap"), true
		}
		n := len(res.TopTargets)
		if n > 3 {
			n = 3
		}
		parts := make([]string, 0, n)
		for _, t := range res.TopTargets[:n] {
			parts = append(parts, fmt.Sprintf("%s (%d)", t.Label, t.N))
		}
		return fmt.Sprintf("On %s: %d positioned clicks. Most-clicked: %s. Open the Heatmap tab for the density grid.", path, res.Clicks, strings.Join(parts, ", ")),
			tkReceipt("the click-heatmap report over "+path, "Heatmap"), true

	case intentSurvey:
		if s.surveys == nil {
			return "Surveys aren't configured on this instance.", tkReceipt("the survey-results report", "Surveys"), true
		}
		sv, found := tkPickSurvey(s.surveys.List())
		if !found {
			return "No surveys running yet. Create one on the Surveys tab (NPS, rating, choice, or text) and responses land here.", tkReceipt("the survey-results report", "Surveys"), true
		}
		res := survey.Results(evs, sv.ID, sv.Type, 30)
		return tkSurveyAnswer(sv, res), tkReceipt(fmt.Sprintf("the survey-results report for %q", sv.Name), "Surveys"), true

	case intentFlag:
		if s.flags == nil {
			return "Feature flags aren't configured on this instance.", tkReceipt("the feature-flag store", "Flags"), true
		}
		fs := s.flags.List()
		if len(fs) == 0 {
			return "No feature flags yet. Create one on the Flags tab or from your editor (create_flag): boolean or multivariate, with targeting and percentage rollout.", tkReceipt("the feature-flag store", "Flags"), true
		}
		parts := make([]string, 0, len(fs))
		for _, f := range fs {
			state := "off"
			if f.Enabled {
				state = "on"
			}
			m := ""
			if f.Measured {
				m = ", measured"
			}
			parts = append(parts, fmt.Sprintf("%s (%s%s)", f.Key, state, m))
		}
		return fmt.Sprintf("%d feature flag%s: %s. Manage them on the Flags tab.", len(fs), tkPlural(len(fs)), strings.Join(parts, ", ")),
			tkReceipt("the feature-flag store", "Flags"), true

	case intentFlagImpact:
		if s.flags == nil {
			return "Feature flags aren't configured on this instance.", tkReceipt("the A/B flag-impact report", "Experiments"), true
		}
		fl, found := tkPickMeasuredFlag(s.flags.List())
		if !found {
			return "No measured flags yet. Mark a flag measured so the SDK logs exposures, pick a goal event, and the A/B read shows per-variant conversion, lift, and 95% significance here.", tkReceipt("the A/B flag-impact report", "Experiments"), true
		}
		goal := tkEvent(q, evs)
		if goal == "" {
			return fmt.Sprintf("Flag %q is measured, but I couldn't tell which goal event to score it on. Ask e.g. \"how is %s doing on checkout?\".", fl.Key, fl.Key), tkReceipt("the A/B flag-impact report", "Experiments"), true
		}
		rep := flag.Measure(evs, fl.Key, goal, 30)
		return tkFlagImpactAnswer(fl.Key, goal, rep), tkReceipt(fmt.Sprintf("the A/B flag-impact report for %q on %q", fl.Key, goal), "Experiments"), true

	case intentSessions, intentSessionTimeline:
		ss := session.Sessions(evs, 7, 100)
		if len(ss) == 0 {
			return "No sessions in the last 7 days yet.", tkReceipt("the session inspector", "Sessions"), true
		}
		top := ss[0] // representative: a real journey (has duration + pages), not a one-click bounce
		for _, sn := range ss {
			if sn.DurationSec > 0 && (top.DurationSec == 0 || sn.Pages > top.Pages || (sn.Pages == top.Pages && sn.Events > top.Events)) {
				top = sn
			}
		}
		return fmt.Sprintf("%d session%s in the last 7 days. Most recent: %s visited %d page%s%s over %s. Open the Sessions tab to replay any one step by step (pages, clicks with positions, rage-clicks, timing).",
				len(ss), tkPlural(len(ss)), tkShortID(top.DistinctID), top.Pages, tkPlural(top.Pages), tkRage(top.RageClicks), tkDur(top.DurationSec)),
			tkReceipt("the session inspector", "Sessions"), true

	case intentDeployImpact:
		if s.deploys == nil {
			return "Deploy tracking isn't configured on this instance.", tkReceipt("the deploy-impact report", "Deploys"), true
		}
		deps := s.deploys.List()
		if len(deps) == 0 {
			return "No deploy markers recorded yet. Add one line in CI after each ship (`smolanalytics deploy`), then I can tie a metric change to the commit behind it.", tkReceipt("the deploy-impact report", "Deploys"), true
		}
		metric := tkEvent(q, evs)
		rep := deploys.Report(evs, deps, metric, 30, 3)
		return tkDeployAnswer(metric, rep), tkReceipt(fmt.Sprintf("the deploy-impact report on %q", metric), "Deploys"), true

	case intentSequence:
		return "Behavioral cohorts (did X then Y, in order) are defined on the Cohorts tab or from your editor with create_sequence_cohort. Tell me the steps, e.g. \"signup then checkout\", and I'll set one up there.",
			tkReceipt("the sequenced-cohort builder", "Cohorts"), true
	}
	return "", "", false
}

func tkReceipt(report, tab string) string {
	return "Computed by " + report + ", the same deterministic report the dashboard and your editor (over MCP) run, not generated by a model. Open the " + tab + " tab for the full interactive view."
}

var tkPathRe = regexp.MustCompile(`/[a-z0-9][a-z0-9\-_/]*`)

// tkPath resolves which page a heatmap question is about: an explicit "/path" in the
// question, then a page keyword, then the busiest pageview path, then "/".
func tkPath(q string, evs []event.Event) string {
	if m := tkPathRe.FindString(q); m != "" {
		return m
	}
	for _, kw := range []struct{ w, p string }{
		{"pricing", "/pricing"}, {"checkout", "/checkout"}, {"signup", "/signup"},
		{"docs", "/docs"}, {"landing", "/"}, {"homepage", "/"}, {"home page", "/"},
	} {
		if strings.Contains(q, kw.w) {
			return kw.p
		}
	}
	counts := map[string]int{}
	for _, e := range evs {
		if e.Name == "$pageview" {
			if p, _ := e.Properties["path"].(string); p != "" {
				counts[p]++
			}
		}
	}
	best, bn := "/", 0
	for p, n := range counts {
		if n > bn {
			best, bn = p, n
		}
	}
	return best
}

// tkEvent resolves the goal/metric event a flag or deploy question is about.
func tkEvent(q string, evs []event.Event) string {
	if ev := namedEvent(q, eventsByVolume(evs)); ev != "" {
		return ev
	}
	// looser: an event name (or its plural) appears in the question, e.g. "signups"->signup,
	// "checkout_v2"->checkout, so a flag/deploy question about a specific metric isn't scored
	// on the generic visit event by accident.
	for _, e := range eventsByVolume(evs) {
		if strings.HasPrefix(e, "$") {
			continue
		}
		if strings.Contains(q, e) || strings.Contains(q, e+"s") {
			return e
		}
	}
	return tkGoal(evs)
}

// tkGoal picks a conversion-like goal when the question names none, preferring a real
// conversion over the generic visit/pageview event (scoring an A/B test on "open" reads as a
// meaningless 0%).
func tkGoal(evs []event.Event) string {
	vol := eventsByVolume(evs)
	for _, want := range []string{"purchase", "checkout", "subscribe", "order", "upgrade", "pay", "convert", "signup", "activate"} {
		for _, e := range vol {
			if e == want {
				return e
			}
		}
	}
	for _, e := range vol {
		if !strings.HasPrefix(e, "$") && e != "open" && e != "pageview" && e != "visit" {
			return e
		}
	}
	if len(vol) > 0 {
		return vol[0]
	}
	return ""
}

func tkPickSurvey(list []survey.Survey) (survey.Survey, bool) {
	for _, sv := range list {
		if sv.Active {
			return sv, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return survey.Survey{}, false
}

func tkPickMeasuredFlag(list []flag.Flag) (flag.Flag, bool) {
	for _, f := range list {
		if f.Measured {
			return f, true
		}
	}
	return flag.Flag{}, false
}

func tkSurveyAnswer(sv survey.Survey, res survey.Result) string {
	if res.Responses == 0 {
		return fmt.Sprintf("%q has no responses yet (shown to %d).", sv.Name, res.Shown)
	}
	switch sv.Type {
	case "nps":
		n := 0
		if res.NPS != nil {
			n = *res.NPS
		}
		return fmt.Sprintf("NPS for %q is %d, from %d responses (%d shown, %.0f%% answered).", sv.Name, n, res.Responses, res.Shown, res.RatePct)
	case "rating":
		a := 0.0
		if res.Average != nil {
			a = *res.Average
		}
		return fmt.Sprintf("%q averages %.1f from %d responses.", sv.Name, a, res.Responses)
	case "choice":
		top := ""
		if len(res.Breakdown) > 0 {
			top = fmt.Sprintf(" Top answer: %s (%d).", res.Breakdown[0].Label, res.Breakdown[0].N)
		}
		return fmt.Sprintf("%q has %d responses.%s Open the Surveys tab for the breakdown.", sv.Name, res.Responses, top)
	default:
		return fmt.Sprintf("%q has %d text response%s (%d shown). Open the Surveys tab to read them.", sv.Name, res.Responses, tkPlural(res.Responses), res.Shown)
	}
}

func tkFlagImpactAnswer(key, goal string, rep flag.Report) string {
	if len(rep.Variants) < 2 {
		return fmt.Sprintf("Not enough exposures yet to compare variants for %q on %q. Once the SDK logs $feature_flag_called for each arm, the A/B read fills in.", key, goal)
	}
	best := rep.Variants[0]
	for _, v := range rep.Variants {
		if v.RatePct > best.RatePct {
			best = v
		}
	}
	sig := "not yet significant"
	if best.Significant {
		sig = "significant at 95%"
	}
	if best.Key == rep.Control {
		return fmt.Sprintf("For %q on %q, the control arm %q still leads at %.1f%%. No variant beats it yet (%s). %d exposed.", key, goal, rep.Control, best.RatePct, sig, best.Exposed)
	}
	return fmt.Sprintf("For %q on %q: %q leads at %.1f%% vs control %q, a %+.1f%% lift (%s). %d exposed in that arm.",
		key, goal, best.Key, best.RatePct, rep.Control, best.DeltaPct, sig, best.Exposed)
}

func tkDeployAnswer(metric string, rep map[string]any) string {
	hl, _ := rep["headline"].(*deploys.Impact)
	if hl == nil {
		return fmt.Sprintf("No deploy has significantly moved %s in the last 30 days (it needs a full window on both sides and a move of at least 25%% to call it). Open the Deploys tab for the per-ship breakdown.", metric)
	}
	verb := "moved"
	switch hl.Direction {
	case "regression":
		verb = "dropped"
	case "improvement":
		verb = "lifted"
	}
	delta := 0.0
	if hl.DeltaPct != nil {
		delta = *hl.DeltaPct
	}
	label := hl.Message
	if label == "" {
		label = hl.ShortSHA
	}
	return fmt.Sprintf("%s %s %+.0f%% after %s %q. Correlation, not proof, but that ship is the suspect. Open the Deploys tab for the full before/after.",
		metric, verb, delta, hl.ShortSHA, label)
}

func tkPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func tkShortID(id string) string {
	if len(id) > 14 {
		return id[:14] + "…"
	}
	return id
}

func tkRage(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d rage-click%s", n, tkPlural(n))
}

func tkDur(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	return fmt.Sprintf("%dm", sec/60)
}
