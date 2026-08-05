package flag

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Multiple comparisons: what a four-arm experiment costs you before you have added a second metric.
//
// Three treatment arms tested against control at 95% each is not a 5% chance of a false winner,
// it is about 14%. Add a second metric and it is roughly a coin flip that SOMETHING on the page
// reads as significant. Nothing in the report says so today, and the person reading it ships the
// arm with the small p-value — which is the whole mechanism by which A/B testing produces
// confident, reproducible, wrong decisions.
//
// The correction here is Benjamini–Hochberg, controlling the false discovery rate at q = α: of
// the arms you declare winners, at most q of them are expected to be noise. Bonferroni would
// control the family-wise rate instead, and at m=6 it costs so much power that a real 5% lift
// stops being findable at any sample a solo builder can reach — a correction nobody can afford to
// leave on is a correction nobody leaves on.
//
// The part that matters as much as the arithmetic is STATING THE FAMILY. "Adjusted p = 0.06" is
// meaningless without "adjusted against what?", and every tool that shows an adjusted number
// without its family is asking to be trusted about the one input that changes the answer.

// Hypothesis kinds. Only primary hypotheses form the correction family.
const (
	HypothesisPrimary   = "primary"
	HypothesisSecondary = "secondary"
	HypothesisGuardrail = "guardrail"
)

// BHMethod is the correction name, echoed in the payload so a reader never has to infer it from
// the shape of the numbers.
const BHMethod = "benjamini-hochberg"

// Hypothesis is one (variant, metric) cell tested at ONE look.
type Hypothesis struct {
	Variant string  `json:"variant"` // the non-control arm being compared
	Metric  string  `json:"metric"`  // the goal/metric event
	Kind    string  `json:"kind"`    // primary (default) | secondary | guardrail
	P       float64 `json:"p_value"` // raw two-sided p against control
}

func (h Hypothesis) kind() string {
	if h.Kind == "" {
		return HypothesisPrimary
	}
	return h.Kind
}

// BHFamily names the set of hypotheses corrected together, and is shipped inside the result.
//
// AccumulatesAcrossLooks is a field rather than a comment because it is the question a statistician
// asks first and the one a dashboard silently gets wrong: the family is the cells at ONE look, and
// it does NOT grow every time somebody refreshes. Peeking is already paid for by the confidence
// sequence — inflating the interval at every look AND growing m at every look would charge for the
// same sin twice, and the intervals would crawl toward useless the longer an honest experiment ran.
type BHFamily struct {
	Experiment             string   `json:"experiment"`
	Variants               []string `json:"variants"`
	Metrics                []string `json:"metrics"`
	Size                   int      `json:"size"`
	Description            string   `json:"description"`
	AccumulatesAcrossLooks bool     `json:"accumulates_across_looks"` // always false
	Excluded               string   `json:"excluded,omitempty"`       // what was deliberately left out
}

// BHTest is one hypothesis with its correction applied.
type BHTest struct {
	Variant   string  `json:"variant"`
	Metric    string  `json:"metric"`
	P         float64 `json:"p_value"`
	PAdjusted float64 `json:"p_value_adjusted"`
	Rank      int     `json:"rank"` // 1-based position in the ascending p order
	Rejected  bool    `json:"rejected"`
}

// BHResult is the correction, its family, and everything needed to redo it by hand.
type BHResult struct {
	Method     string   `json:"method"`
	Family     BHFamily `json:"family"`
	Q          float64  `json:"q"` // FDR level, = alpha
	Rejections int      `json:"rejections"`
	// Threshold is the largest raw p that was rejected, i.e. p_(k). Zero when nothing was
	// rejected. It is the one number that lets a reader check the step-up by eye.
	Threshold float64  `json:"threshold"`
	Tests     []BHTest `json:"tests"`
	Note      string   `json:"note"`
}

// PrimaryHypotheses filters a mixed list down to the correction family.
//
// Guardrails are excluded on purpose, and it is not a technicality: a guardrail is a test you WANT
// to fire, so correcting it makes it less likely to catch the harm it exists to catch. Secondary
// metrics are excluded because they were declared as exploratory — they are not decision inputs,
// and folding them in would cost the primary metric power to protect a claim nobody is making.
func PrimaryHypotheses(hs []Hypothesis) []Hypothesis {
	out := make([]Hypothesis, 0, len(hs))
	for _, h := range hs {
		if h.kind() == HypothesisPrimary {
			out = append(out, h)
		}
	}
	return out
}

// BenjaminiHochberg controls the false discovery rate at q across one experiment's primary
// (variant × metric) cells at one look.
//
//	sort p ascending → p_(1) … p_(m)
//	k = max{ i : p_(i) ≤ (i/m)·q }        // step-up; reject 1..k
//	adjusted p_(i) = min over j ≥ i of ( (m/j)·p_(j) ), capped at 1
//
// The min-over-j is what makes the adjusted values monotone, and monotonicity is not cosmetic: a
// non-monotone adjusted column can show a SMALLER adjusted p on a LARGER raw p, and a reader who
// sorts the table by the adjusted column then sees the ranking of their arms silently reordered.
//
// It refuses rather than guesses on: an unnamed experiment (the family would be unnameable), a
// non-primary hypothesis (see PrimaryHypotheses), a duplicate cell (two looks merged into one
// family, which is the one thing binding decision 10 forbids), and a p outside [0,1].
func BenjaminiHochberg(experiment string, hs []Hypothesis, q float64) (BHResult, error) {
	if experiment == "" {
		return BHResult{}, errors.New("multiplicity: the family must name its experiment — an adjusted " +
			"p-value without the family it was adjusted against is not a checkable number")
	}
	if math.IsNaN(q) || q <= 0 || q >= 1 {
		return BHResult{}, fmt.Errorf("multiplicity: q must be in (0,1), got %v", q)
	}
	seen := map[string]bool{}
	for _, h := range hs {
		if h.kind() != HypothesisPrimary {
			return BHResult{}, fmt.Errorf("multiplicity: %s/%s is a %s and cannot be in the correction "+
				"family — guardrails must stay sensitive (correcting one makes it less likely to catch "+
				"the harm it exists for) and secondary metrics are not decision inputs; filter with "+
				"PrimaryHypotheses first", h.Variant, h.Metric, h.kind())
		}
		if h.Variant == "" || h.Metric == "" {
			return BHResult{}, errors.New("multiplicity: every hypothesis needs a variant and a metric — " +
				"they are what names the family")
		}
		key := h.Variant + "\x00" + h.Metric
		if seen[key] {
			return BHResult{}, fmt.Errorf("multiplicity: %s/%s appears twice. The family is "+
				"(variants × metrics) at ONE look; a repeated cell means two looks were merged, and the "+
				"family must not accumulate across peeks — the confidence sequence already pays for "+
				"looking", h.Variant, h.Metric)
		}
		seen[key] = true
		if math.IsNaN(h.P) || h.P < 0 || h.P > 1 {
			return BHResult{}, fmt.Errorf("multiplicity: %s/%s has p-value %v, which is not a probability",
				h.Variant, h.Metric, h.P)
		}
	}

	res := BHResult{Method: BHMethod, Q: q, Family: buildBHFamily(experiment, hs)}
	m := len(hs)
	if m == 0 {
		res.Note = "no primary hypotheses in this family, so there is nothing to correct"
		res.Tests = []BHTest{}
		return res, nil
	}

	// Sort by p, then variant, then metric. The tie-break is not decoration: two arms with
	// identical p-values must produce the same table on every run, or the receipt for a report
	// changes without the data changing.
	ord := make([]int, m)
	for i := range ord {
		ord[i] = i
	}
	sort.SliceStable(ord, func(x, y int) bool {
		a, b := hs[ord[x]], hs[ord[y]]
		if a.P != b.P {
			return a.P < b.P
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		return a.Metric < b.Metric
	})

	// Step-up: the LARGEST i that clears its threshold, not the first that fails. Rejecting up to
	// the first failure is the classic misreading of BH and it under-rejects whenever a large p
	// sits between two small ones.
	k := 0
	for i := 1; i <= m; i++ {
		if hs[ord[i-1]].P <= float64(i)/float64(m)*q {
			k = i
		}
	}

	adj := make([]float64, m)
	running := math.Inf(1)
	for i := m; i >= 1; i-- {
		v := float64(m) / float64(i) * hs[ord[i-1]].P
		if v > 1 {
			v = 1
		}
		if v < running {
			running = v
		}
		adj[i-1] = running
	}

	res.Tests = make([]BHTest, m)
	for pos, idx := range ord {
		h := hs[idx]
		res.Tests[pos] = BHTest{
			Variant: h.Variant, Metric: h.Metric,
			P: h.P, PAdjusted: adj[pos],
			Rank: pos + 1, Rejected: pos < k,
		}
	}
	res.Rejections = k
	if k > 0 {
		res.Threshold = hs[ord[k-1]].P
	}
	res.Note = bhNote(res.Family, q, k)
	return res, nil
}

func buildBHFamily(experiment string, hs []Hypothesis) BHFamily {
	vset, mset := map[string]bool{}, map[string]bool{}
	for _, h := range hs {
		vset[h.Variant] = true
		mset[h.Metric] = true
	}
	vs, ms := make([]string, 0, len(vset)), make([]string, 0, len(mset))
	for v := range vset {
		vs = append(vs, v)
	}
	for m := range mset {
		ms = append(ms, m)
	}
	sort.Strings(vs)
	sort.Strings(ms)
	f := BHFamily{
		Experiment: experiment, Variants: vs, Metrics: ms, Size: len(hs),
		AccumulatesAcrossLooks: false,
		Excluded:               "guardrails and secondary metrics",
	}
	f.Description = itoa(len(vs)) + plural(len(vs), " variant", " variants") + " × " +
		itoa(len(ms)) + plural(len(ms), " metric", " metrics") + " in experiment " + experiment +
		" at one look = " + itoa(len(hs)) + plural(len(hs), " test", " tests")
	return f
}

func bhNote(f BHFamily, q float64, k int) string {
	n := "p-values corrected for multiple comparisons across " + f.Description +
		". Benjamini–Hochberg at q=" + trimFloat(100*q) + "%: of the arms called winners here, at most " +
		trimFloat(100*q) + "% are expected to be noise. "
	if k == 0 {
		n += "Nothing clears the correction yet. "
	} else {
		n += itoa(k) + plural(k, " cell clears", " cells clear") + " it. "
	}
	return n + "The family does not grow when you look again — peeking is already paid for by the " +
		"always-valid interval, and charging for it twice would make an honest experiment's numbers " +
		"worse the longer it ran. Guardrails and secondary metrics are excluded on purpose: a " +
		"guardrail is a test you want to be sensitive."
}

// AdjustedDeltaNote is shipped verbatim beside any BH-adjusted interval.
//
// It is here because there is NO canonical BH-adjusted confidence interval. GrowthBook back-solves
// the standard error that would have produced the adjusted p and rebuilds the interval from it,
// purely so an adjusted-significant result still shows an interval excluding zero. This does the
// same thing — and says so in the payload rather than presenting a presentation convention as a
// derived quantity. Being the tool that admits this is worth more than the interval is.
const AdjustedDeltaNote = "back-solved from the adjusted p-value so the interval agrees with the " +
	"decision; this is a presentation convention, not a derived interval"

// AdjustedDeltaInterval rebuilds a delta interval consistent with an adjusted p-value.
//
// se_implied = |delta| / z_{1-p_adj/2};  interval = delta ± z_{1-q/2} · se_implied
//
// At p_adj = q the bound lands exactly on zero, which is the property that makes the table
// self-consistent: an adjusted-significant row never shows an interval spanning zero, and an
// adjusted-non-significant row never shows one that excludes it.
//
// Returns an error rather than a number in the degenerate cases, because each of them would print
// something that looks like an interval and is not one: a zero point estimate has no scale to
// back-solve from, and p_adj = 1 implies an infinite standard error.
func AdjustedDeltaInterval(deltaPct, pAdjusted, q float64) (Interval, string, error) {
	switch {
	case math.IsNaN(deltaPct) || math.IsInf(deltaPct, 0):
		return Interval{}, "", errors.New("adjusted interval: the point estimate is not finite")
	case deltaPct == 0:
		return Interval{}, "", errors.New("adjusted interval: the point estimate is exactly 0, so there " +
			"is no scale to back-solve a standard error from — show the raw interval instead")
	case math.IsNaN(pAdjusted) || pAdjusted <= 0 || pAdjusted >= 1:
		return Interval{}, "", fmt.Errorf("adjusted interval: adjusted p must be in (0,1), got %v "+
			"(at p=1 the implied standard error is infinite and the interval is unbounded)", pAdjusted)
	case math.IsNaN(q) || q <= 0 || q >= 1:
		return Interval{}, "", fmt.Errorf("adjusted interval: q must be in (0,1), got %v", q)
	}
	zAdj := seqNormInv(1 - pAdjusted/2)
	if zAdj <= 0 {
		return Interval{}, "", errors.New("adjusted interval: the adjusted p implies a zero critical value")
	}
	se := math.Abs(deltaPct) / zAdj
	half := seqNormInv(1-q/2) * se
	return Interval{
		Point: round1(deltaPct),
		Lo:    round1(deltaPct - half),
		Hi:    round1(deltaPct + half),
	}, AdjustedDeltaNote, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
