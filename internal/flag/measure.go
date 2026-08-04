package flag

import (
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Exposure/response property keys, mirroring PostHog's $feature_flag_called convention. The "$"
// prefix means the tracking-plan drift gate treats these as system events, not unplanned ones.
const (
	ExposureEvent  = "$feature_flag_called"
	PropFlag       = "$feature_flag"
	PropVariant    = "$feature_flag_response"
	minArmForStats = 30 // below this a variant's rate is too noisy to call significant
)

// VariantResult is one arm of a measured flag's A/B read.
//
// Every rate ships with the raw numerator and denominator that produced it and a 95% interval
// around it. A bare percentage asks to be trusted; 43 of 512, somewhere between 6.3% and 11.1%,
// can be checked. That is the difference this product claims to sell.
type VariantResult struct {
	Key       string `json:"key"`
	Exposed   int    `json:"exposed"`
	Converted int    `json:"converted"`
	// RatePct is the point estimate; RateCI is where the true rate plausibly sits. Wilson
	// interval, so it never reports a bound below 0% or above 100% on a small arm.
	RatePct float64  `json:"rate_pct"`
	RateCI  Interval `json:"rate_ci"`
	// DeltaPct is relative lift vs control (0 for control). DeltaCI is its interval, present
	// only when the control rate is far enough from zero for a ratio to be meaningful —
	// otherwise a handful of conversions prints "+4000%" and someone ships on it.
	DeltaPct    float64   `json:"delta_pct"`
	DeltaCI     *Interval `json:"delta_ci,omitempty"`
	PValue      float64   `json:"p_value,omitempty"`
	Significant bool      `json:"significant"`  // 95% two-proportion z-test vs control
	SmallSample bool      `json:"small_sample"` // too few exposed to trust the rate
	// Read is the sentence a person should act on. "Significant" invites shipping; "the range
	// still spans zero" invites waiting, which is usually the right call and one a boolean
	// never prompts.
	Read string `json:"read,omitempty"`
}

// Report is the A/B read for one measured flag: for each variant, how many exposed users
// converted on the goal event AFTER their first exposure, and whether the lift over the control
// arm is statistically significant. Pure + deterministic (same events → same report), so it is
// pinnable MCP==API by an agreement test, the same contract as every other report.
type Report struct {
	Flag     string          `json:"flag"`
	Goal     string          `json:"goal"`
	Days     int             `json:"days"`
	Control  string          `json:"control"`
	Variants []VariantResult `json:"variants"`
	Note     string          `json:"note"`
}

// Measure computes the report from raw events. An exposure is a $feature_flag_called event tagging
// the user's variant for this flag; a conversion is the user doing `goal` at or after their first
// exposure (so we never credit behavior that predates the experiment). Only events within the last
// `days` (0 = all) are considered.
func Measure(evs []event.Event, flagKey, goal string, days int) Report {
	var cutoff time.Time
	if days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}

	type exposure struct {
		variant string
		at      time.Time
	}
	firstExp := map[string]exposure{}
	firstGoal := map[string]time.Time{}

	for _, e := range evs {
		if days > 0 && e.Timestamp.Before(cutoff) {
			continue
		}
		switch e.Name {
		case ExposureEvent:
			if fk, _ := e.Properties[PropFlag].(string); fk != flagKey {
				continue
			}
			if cur, ok := firstExp[e.DistinctID]; !ok || e.Timestamp.Before(cur.at) {
				v, _ := e.Properties[PropVariant].(string)
				firstExp[e.DistinctID] = exposure{variant: v, at: e.Timestamp}
			}
		case goal:
			if cur, ok := firstGoal[e.DistinctID]; !ok || e.Timestamp.Before(cur) {
				firstGoal[e.DistinctID] = e.Timestamp
			}
		}
	}

	type tally struct{ exposed, converted int }
	byVariant := map[string]*tally{}
	for id, ex := range firstExp {
		t := byVariant[ex.variant]
		if t == nil {
			t = &tally{}
			byVariant[ex.variant] = t
		}
		t.exposed++
		if g, ok := firstGoal[id]; ok && !g.Before(ex.at) {
			t.converted++
		}
	}

	variants := make([]string, 0, len(byVariant))
	for v := range byVariant {
		variants = append(variants, v)
	}
	sort.Strings(variants) // deterministic order; control = first (e.g. "control"/"a"/"on")

	control := ""
	if len(variants) > 0 {
		control = variants[0]
	}
	rep := Report{Flag: flagKey, Goal: goal, Days: days, Control: control}
	var ctrl *tally
	if control != "" {
		ctrl = byVariant[control]
	}
	for _, v := range variants {
		t := byVariant[v]
		rate := 0.0
		if t.exposed > 0 {
			rate = 100 * float64(t.converted) / float64(t.exposed)
		}
		vr := VariantResult{
			Key:         v,
			Exposed:     t.exposed,
			Converted:   t.converted,
			RatePct:     round1(rate),
			RateCI:      wilson(t.converted, t.exposed, z95),
			SmallSample: t.exposed < minArmForStats,
		}
		if ctrl != nil && v != control && ctrl.exposed > 0 {
			ctrlRate := float64(ctrl.converted) / float64(ctrl.exposed)
			if ctrlRate > 0 {
				vr.DeltaPct = round1(100 * ((rate/100 - ctrlRate) / ctrlRate))
			}
			vr.Significant = significant(t.converted, t.exposed, ctrl.converted, ctrl.exposed)
			vr.PValue = round4(twoProportionP(t.converted, t.exposed, ctrl.converted, ctrl.exposed))
			enough := t.exposed >= minArmForStats && ctrl.exposed >= minArmForStats
			ci, ok := liftInterval(t.converted, t.exposed, ctrl.converted, ctrl.exposed, z95)
			if ok {
				c := ci
				vr.DeltaCI = &c
			}
			vr.Read = readLift(ci, ok, vr.PValue, enough)
		}
		rep.Variants = append(rep.Variants, vr)
	}
	switch {
	case len(rep.Variants) == 0:
		rep.Note = "no exposures yet. mark the flag measured and let the SDK log $feature_flag_called, then check back"
	case len(rep.Variants) == 1:
		rep.Note = "only one variant has exposures so far; there is nothing to compare it against yet"
	default:
		rep.Note = "conversion = the goal event at or after a user's first exposure. Every rate carries its raw counts and a 95% Wilson interval; lift carries a 95% interval too, omitted when the control rate is too near zero for a ratio to mean anything. Check the split with experiment_health before acting on any of this — a sample ratio mismatch makes every number here unreadable."
	}
	return rep
}

// significant is a two-proportion z-test at ~95% (|z| >= 1.96), guarded so tiny arms never
// read as significant.
func significant(c1, n1, c2, n2 int) bool {
	if n1 < minArmForStats || n2 < minArmForStats {
		return false
	}
	p1 := float64(c1) / float64(n1)
	p2 := float64(c2) / float64(n2)
	pooled := float64(c1+c2) / float64(n1+n2)
	se := math.Sqrt(pooled * (1 - pooled) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return false
	}
	return math.Abs(p1-p2)/se >= 1.96
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }
