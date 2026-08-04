package flag

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Sample Ratio Mismatch: the users actually exposed to each arm do not match the split that was
// configured. A 50/50 experiment that delivered 5,200 / 4,800 did not randomize the way it said
// it did, and once that is true nothing downstream can be trusted — the arms differ by whatever
// mechanism broke the split, not by the change being tested.
//
// This is the single most valuable check an experiment tool can run, because without it a broken
// experiment is indistinguishable from a successful one. It reports a confident number either
// way. Every serious platform runs it; the difference between them and a homegrown A/B feature
// is usually this test and nothing else.
//
// The threshold is deliberately far stricter than the usual 0.05. Experiments are checked
// constantly and by many people, so a 1-in-20 false-alarm rate would cry wolf until the warning
// is ignored — which is worse than not having it. GrowthBook fires at 0.001 and Microsoft at
// 0.0005; 0.001 matches the former.
const SRMAlpha = 0.001

// minExpectedPerArm is the standard validity floor for a chi-square goodness-of-fit test. Below
// roughly five expected observations in a cell the chi-square approximation stops holding and the
// p-value is not meaningful, so the check reports "not enough data" rather than a number that
// looks like an answer.
const minExpectedPerArm = 5

// SRMResult is the health check for one experiment's traffic split.
type SRMResult struct {
	Checked   bool               `json:"checked"`
	Detected  bool               `json:"detected"`
	PValue    float64            `json:"p_value"`
	ChiSquare float64            `json:"chi_square"`
	Observed  map[string]int     `json:"observed"`
	Expected  map[string]float64 `json:"expected"`
	Total     int                `json:"total"`
	// Culprit names the segment whose split is most skewed, when one stands out. This is the
	// difference between "your experiment is broken" and "your experiment is broken, and it is
	// iOS users: 340 in control against 91 in test".
	Culprit string `json:"culprit,omitempty"`
	Verdict string `json:"verdict"`
}

// CheckSRM compares how many users were actually exposed to each arm against the flag's
// configured weights.
//
// Only arms the flag actually declares are counted. An exposure tagged with a variant that no
// longer exists is itself a problem, but it is a different one — folding it into the chi-square
// would blame the split for a stale SDK.
func CheckSRM(evs []event.Event, f Flag, days int) SRMResult {
	res := SRMResult{Observed: map[string]int{}, Expected: map[string]float64{}}

	weights := map[string]float64{}
	total := 0.0
	for _, v := range f.Variants {
		if v.Weight > 0 {
			weights[v.Key] = float64(v.Weight)
			total += float64(v.Weight)
		}
	}
	if len(weights) < 2 || total <= 0 {
		res.Verdict = "no split to check: this flag does not have two or more weighted variants"
		return res
	}

	firstExp := firstExposures(evs, f.Key, days)
	n := 0
	for _, ex := range firstExp {
		if _, ok := weights[ex.variant]; !ok {
			continue // an arm the flag no longer declares; see the doc comment
		}
		res.Observed[ex.variant]++
		n++
	}
	res.Total = n

	for k, w := range weights {
		res.Expected[k] = round1(float64(n) * w / total)
	}
	if n == 0 {
		res.Verdict = "no exposures recorded yet"
		return res
	}
	for _, w := range weights {
		if float64(n)*w/total < minExpectedPerArm {
			res.Verdict = fmt.Sprintf("not enough exposures to check the split yet (need about %d per arm)", minExpectedPerArm*len(weights))
			return res
		}
	}

	chi := 0.0
	for k, w := range weights {
		exp := float64(n) * w / total
		obs := float64(res.Observed[k])
		chi += (obs - exp) * (obs - exp) / exp
	}
	res.Checked = true
	res.ChiSquare = math.Round(chi*1000) / 1000
	res.PValue = chiSquareP(chi, len(weights)-1)
	res.Detected = res.PValue < SRMAlpha

	if !res.Detected {
		res.Verdict = fmt.Sprintf("traffic split looks correct (p=%.3f). %s", res.PValue, describeSplit(res))
		return res
	}
	res.Culprit = worstSegment(evs, f, days, weights)
	res.Verdict = fmt.Sprintf(
		"SAMPLE RATIO MISMATCH (p=%.5f). %s That is further from the configured split than chance explains, so the randomization is broken and these results cannot be trusted. Common causes: a variant added or removed while the experiment was running, a redirect or bot filter that hits one arm harder, or exposures logged from more than one place.",
		res.PValue, describeSplit(res))
	if res.Culprit != "" {
		res.Verdict += " Most skewed segment: " + res.Culprit + "."
	}
	return res
}

func describeSplit(r SRMResult) string {
	keys := make([]string, 0, len(r.Observed))
	for k := range r.Expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "Got"
	for i, k := range keys {
		if i > 0 {
			s += " /"
		}
		s += fmt.Sprintf(" %s %d (expected %.0f)", k, r.Observed[k], r.Expected[k])
	}
	return s + "."
}

type expo struct {
	variant string
	at      int64
	props   map[string]any
}

// firstExposures keeps each user's EARLIEST exposure. A user who somehow saw two arms is counted
// once, under the first, because counting them twice would itself create a ratio mismatch and the
// check would report its own bookkeeping as the bug.
func firstExposures(evs []event.Event, flagKey string, days int) map[string]expo {
	var cutoff int64 = math.MinInt64
	if days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days).UnixNano()
	}
	out := map[string]expo{}
	for _, e := range evs {
		if e.Name != ExposureEvent {
			continue
		}
		if e.Timestamp.UnixNano() < cutoff {
			continue
		}
		if fk, _ := e.Properties[PropFlag].(string); fk != flagKey {
			continue
		}
		ts := e.Timestamp.UnixNano()
		if cur, ok := out[e.DistinctID]; ok && cur.at <= ts {
			continue
		}
		v, _ := e.Properties[PropVariant].(string)
		out[e.DistinctID] = expo{variant: v, at: ts, props: e.Properties}
	}
	return out
}

// worstSegment finds the property value whose arm split deviates most from the configured one.
//
// An SRM is almost always caused by something structural — one platform, one browser, one
// country, one SDK — and naming it turns an alarm into a lead. Only segments with enough users to
// be meaningful are considered, so a single odd user never gets blamed.
func worstSegment(evs []event.Event, f Flag, days int, weights map[string]float64) string {
	const minSegment = 30
	candidates := []string{"device", "platform", "os", "browser", "country", "sdk", "$lib"}
	totalW := 0.0
	for _, w := range weights {
		totalW += w
	}

	type seg struct {
		prop, val string
		obs       map[string]int
		n         int
	}
	segs := map[string]*seg{}
	for _, ex := range firstExposures(evs, f.Key, days) {
		if _, ok := weights[ex.variant]; !ok {
			continue
		}
		for _, p := range candidates {
			raw, ok := ex.props[p]
			if !ok {
				continue
			}
			val := fmt.Sprintf("%v", raw)
			if val == "" {
				continue
			}
			key := p + "=" + val
			s := segs[key]
			if s == nil {
				s = &seg{prop: p, val: val, obs: map[string]int{}}
				segs[key] = s
			}
			s.obs[ex.variant]++
			s.n++
		}
	}

	bestChi, best := 0.0, ""
	keys := make([]string, 0, len(segs))
	for k := range segs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic when two segments tie
	for _, k := range keys {
		s := segs[k]
		if s.n < minSegment {
			continue
		}
		chi := 0.0
		for arm, w := range weights {
			exp := float64(s.n) * w / totalW
			if exp <= 0 {
				continue
			}
			obs := float64(s.obs[arm])
			chi += (obs - exp) * (obs - exp) / exp
		}
		if chi > bestChi {
			bestChi, best = chi, fmt.Sprintf("%s=%s (%s)", s.prop, s.val, splitOf(s.obs))
		}
	}
	// Only worth naming if this segment is genuinely skewed rather than merely the largest.
	if bestChi < 10.83 { // chi-square 0.001 at 1 degree of freedom
		return ""
	}
	return best
}

func splitOf(obs map[string]int) string {
	keys := make([]string, 0, len(obs))
	for k := range obs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for i, k := range keys {
		if i > 0 {
			s += " vs "
		}
		s += fmt.Sprintf("%s %d", k, obs[k])
	}
	return s
}

// ---- chi-square distribution ------------------------------------------------------------

// chiSquareP returns P(X > x) for a chi-square with df degrees of freedom — the probability of
// seeing a split at least this lopsided if the randomization were working correctly.
//
// This is the regularized upper incomplete gamma Q(df/2, x/2), which the standard library does
// not provide, so it is implemented here: a series expansion where it converges quickly and a
// continued fraction elsewhere, which is the standard split.
func chiSquareP(x float64, df int) float64 {
	if df <= 0 || x <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return 1
	}
	return gammaQ(float64(df)/2, x/2)
}

// gammaQ is the regularized upper incomplete gamma function Q(a,x) = 1 - P(a,x).
func gammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaPSeries(a, x)
	}
	return gammaQContinuedFraction(a, x)
}

// gammaPSeries is the series representation, used where x < a+1 because it converges fast there.
func gammaPSeries(a, x float64) float64 {
	const maxIter = 500
	const eps = 3e-14
	lg, _ := math.Lgamma(a)
	ap := a
	sum := 1 / a
	del := sum
	for i := 0; i < maxIter; i++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*eps {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-lg)
}

// gammaQContinuedFraction is the modified Lentz continued fraction, used where x >= a+1.
func gammaQContinuedFraction(a, x float64) float64 {
	const maxIter = 500
	const eps = 3e-14
	const tiny = 1e-300
	lg, _ := math.Lgamma(a)
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i <= maxIter; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lg) * h
}
