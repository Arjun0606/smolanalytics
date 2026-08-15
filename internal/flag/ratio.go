package flag

import (
	"math"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Ratio metrics — revenue per user, pageviews per session, click-through rate — with the delta
// method, and a cluster-robust estimator computed alongside it as a permanent self-check.
//
// Everything measured so far in this package is a binary per-user conversion, where the standard
// error is textbook. A ratio is not, and it goes wrong in two specific ways that both produce a
// confident number rather than an error:
//
//  1. THE DROPPED COVARIANCE TERM. Numerator and denominator are correlated — a user with more
//     sessions has more purchases — and the variance of a ratio only comes out right when that
//     covariance is subtracted. Compute Var(S)/N_bar^2 and call it done, as the naive derivation
//     invites, and the standard error is wrong by however strong that correlation is. The sign of
//     the error is not under your control either: with the usual positive correlation the naive
//     variance is too LARGE, and where the correlation runs negative it is too SMALL and the
//     interval is confidently too narrow. TestIgnoringCovarianceIsWrong exists so a later refactor
//     cannot quietly delete the term.
//
//  2. THE ANALYSIS UNIT FINER THAN THE RANDOMISATION UNIT. If the coin was flipped per user but
//     the rows are sessions, the sessions inside one user are not independent draws, and treating
//     them as such understates the standard error by sqrt(1 + (m-1)*ICC) — at 8 sessions per user
//     and an ICC of 0.3, a factor of 1.8, which turns a nominal 5% test into a real one around
//     20-25%. Everything here aggregates to the randomisation unit FIRST and reports the naive
//     figure only as a diagnostic, next to how badly it would have lied.
//
// The delta method (Deng/Knoblich/Lu, KDD 2018) and the cluster-robust sandwich are provably the
// same estimator for a clustered randomised experiment (arXiv:2105.14705). They are computed here
// by two genuinely different routes — one from the moments of S and N, one from the per-cluster
// residuals S_k - theta*N_k — so agreement is not a tautology about the maths but a check on this
// code: any divergence beyond floating-point noise means a bug in one of the two paths or a row
// set that violates its own invariants. It is a zero-cost continuous audit no vendor runs.
//
// Nothing here reads the clock (binding rule 1); windows arrive as instants and are echoed.

// Default winsorization percentiles. Recorded on the result and meant to be recorded in the plan
// hash by the caller, because a cap chosen after seeing the data is a researcher degree of freedom
// — "we winsorized at p99" and "we winsorized until it was significant" look identical afterwards.
const (
	DefaultWinsorLoPct = 1.0
	DefaultWinsorHiPct = 99.0
)

// RatioUnit is one randomisation unit's contribution to a ratio metric.
//
// Num and Den are the aggregates the estimator actually uses. Values is optional and carries the
// per-analysis-unit numerators when the analysis unit is enumerable (per-session revenue, say);
// it buys winsorization and the clustering diagnostic, and when it is absent both are reported as
// unavailable rather than approximated.
type RatioUnit struct {
	Unit    string    `json:"unit"`
	Variant string    `json:"variant"`
	Num     float64   `json:"num"`
	Den     float64   `json:"den"`
	Values  []float64 `json:"values,omitempty"`
}

// RatioOptions states the estimand. All of it is echoed on the report: a ratio with no stated
// numerator, denominator and randomisation unit is not a metric, it is a number.
type RatioOptions struct {
	Unit        string    `json:"unit"` // UnitBucketID or UnitDistinctID
	Numerator   string    `json:"numerator"`
	Denominator string    `json:"denominator"`
	Control     string    `json:"control"`
	WinsorLoPct float64   `json:"winsor_lo_pct"` // 0 and 0 → winsorization off, and the report says so
	WinsorHiPct float64   `json:"winsor_hi_pct"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
}

// RatioArm is one arm's ratio with everything needed to check it.
type RatioArm struct {
	Key           string  `json:"key"`
	Units         int     `json:"units"`          // randomisation units (clusters)
	AnalysisUnits float64 `json:"analysis_units"` // sum of denominators
	Num           float64 `json:"num"`
	Den           float64 `json:"den"`
	Estimable     bool    `json:"estimable"`
	Reason        string  `json:"reason,omitempty"` // why not, when not

	Ratio   float64 `json:"ratio"`
	SE      float64 `json:"se"`       // delta method, clustered at the randomisation unit
	RatioLo float64 `json:"ratio_lo"` // in the metric's own units, not percent
	RatioHi float64 `json:"ratio_hi"`

	// SECluster is the same variance by the sandwich route. SEDeltaNoCov is the same variance with
	// the covariance term deleted — shipped so a reader can see what the classic bug would have
	// printed, rather than being told it matters.
	SECluster    float64 `json:"se_cluster"`
	SEDeltaNoCov float64 `json:"se_delta_no_covariance"`
	// SENaive pretends every analysis unit was independently randomised. ClusterInflation is
	// SE/SENaive: the factor by which a tool that forgot to aggregate would have understated this.
	SENaive          float64 `json:"se_naive,omitempty"`
	ClusterInflation float64 `json:"cluster_inflation,omitempty"`
	MeanClusterSize  float64 `json:"mean_cluster_size"`

	// Relative comparison against control. DeltaLogPoint/DeltaLogSE are the log-scale point and
	// standard error the interval is built from; they are exported so a sequential mode can
	// inflate the critical value (binding rule 5) instead of re-deriving the interval.
	DeltaDefined  bool      `json:"delta_defined"`
	DeltaPct      float64   `json:"delta_pct"`
	DeltaCI       *Interval `json:"delta_ci,omitempty"`
	DeltaLogPoint float64   `json:"delta_log_point"`
	DeltaLogSE    float64   `json:"delta_log_se"`
	PValue        float64   `json:"p_value,omitempty"`
	Significant   bool      `json:"significant"`
	Read          string    `json:"read,omitempty"`
}

// DeltaIntervalAtZ rebuilds the relative-lift interval at an arbitrary critical value, so the
// sequential mode multiplies z by k(N) rather than inflating an already-rounded interval.
func (a RatioArm) DeltaIntervalAtZ(z float64) (Interval, bool) {
	if !a.DeltaDefined {
		return Interval{}, false
	}
	lo := math.Exp(a.DeltaLogPoint-z*a.DeltaLogSE) - 1
	hi := math.Exp(a.DeltaLogPoint+z*a.DeltaLogSE) - 1
	return Interval{
		Point: round1(100 * (math.Exp(a.DeltaLogPoint) - 1)),
		Lo:    round1(100 * lo),
		Hi:    round1(100 * hi),
	}, true
}

// RatioReport is the whole read: every arm, the estimand, and whether the two variance routes
// agreed.
type RatioReport struct {
	Unit        string     `json:"unit"`
	Numerator   string     `json:"numerator"`
	Denominator string     `json:"denominator"`
	Control     string     `json:"control"`
	Arms        []RatioArm `json:"arms"`

	Winsorized    bool    `json:"winsorized"`
	WinsorLoPct   float64 `json:"winsor_lo_pct,omitempty"`
	WinsorHiPct   float64 `json:"winsor_hi_pct,omitempty"`
	WinsorLoValue float64 `json:"winsor_lo_value,omitempty"`
	WinsorHiValue float64 `json:"winsor_hi_value,omitempty"`

	// CrossCheckMaxDivergencePct is the largest disagreement between the delta-method and
	// cluster-robust standard errors across arms, as a percentage. OK is false above 1%, which
	// means one of the two implementations is wrong — not that the reader should pick a side.
	CrossCheckMaxDivergencePct float64 `json:"cross_check_max_divergence_pct"`
	CrossCheckOK               bool    `json:"cross_check_ok"`

	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Note string    `json:"note"`
}

// MeasureRatio computes a ratio metric per arm from rows already aggregated to the randomisation
// unit.
//
// It returns an error rather than a number when the estimand itself is broken — no stated
// randomisation unit, no control arm, rows whose Values contradict their own Num/Den. Every
// vendor default is to compute something anyway; a ratio computed on an undefined unit of
// randomisation is not a weaker estimate, it is an estimate of nothing.
func MeasureRatio(units []RatioUnit, opts RatioOptions) (RatioReport, error) {
	if opts.Unit != UnitBucketID && opts.Unit != UnitDistinctID {
		return RatioReport{}, cupedError("this ratio has no stated randomisation unit. A ratio's standard error " +
			"depends entirely on what the coin was flipped on — per user and per session are different numbers from " +
			"the same events — so there is no correct answer to give until it is declared as " + UnitBucketID +
			" or " + UnitDistinctID + ".")
	}
	if opts.Control == "" {
		return RatioReport{}, cupedError("no control arm was declared, so there is nothing to compare the other arms " +
			"against. Declaring it beats inferring it: an inferred control silently inverts the sign of every lift " +
			"when the arms are named in an unlucky order.")
	}
	rep := RatioReport{
		Unit:        opts.Unit,
		Numerator:   opts.Numerator,
		Denominator: opts.Denominator,
		Control:     opts.Control,
		From:        opts.From,
		To:          opts.To,
	}

	rows := make([]RatioUnit, 0, len(units))
	for _, u := range units {
		if u.Variant == "" {
			continue
		}
		if len(u.Values) > 0 {
			// The rows have to agree with themselves. A Values slice that does not sum to Num, or
			// whose length is not Den, means the builder and the estimator disagree about what an
			// analysis unit is, and every number below would be computed from one of the two
			// answers with no sign of the other.
			sum := 0.0
			for _, v := range u.Values {
				sum += v
			}
			if math.Abs(sum-u.Num) > 1e-6*math.Max(1, math.Abs(u.Num)) || math.Abs(float64(len(u.Values))-u.Den) > 1e-9 {
				return RatioReport{}, cupedError("unit " + u.Unit + " carries " + itoa(len(u.Values)) +
					" per-analysis-unit values summing to " + f4(sum) + ", but declares den=" + f4(u.Den) +
					" and num=" + f4(u.Num) + ". Those describe two different metrics.")
			}
		}
		rows = append(rows, u)
	}

	// Winsorize on the POOLED distribution so both arms are capped at identical values. Capping
	// per arm would move the arms' caps independently and manufacture a difference out of the
	// tails alone.
	if opts.WinsorLoPct > 0 || opts.WinsorHiPct > 0 {
		lo, hi, ok := winsorBounds(rows, opts.WinsorLoPct, opts.WinsorHiPct)
		if ok {
			rows = applyWinsor(rows, lo, hi)
			rep.Winsorized = true
			rep.WinsorLoPct, rep.WinsorHiPct = opts.WinsorLoPct, opts.WinsorHiPct
			rep.WinsorLoValue, rep.WinsorHiValue = lo, hi
		}
	}

	byArm := map[string][]RatioUnit{}
	order := []string{opts.Control}
	seen := map[string]bool{opts.Control: true}
	for _, u := range rows {
		byArm[u.Variant] = append(byArm[u.Variant], u)
		if !seen[u.Variant] {
			seen[u.Variant] = true
			order = append(order, u.Variant)
		}
	}
	sort.Strings(order[1:]) // control first, everything else deterministic

	arms := map[string]RatioArm{}
	for _, k := range order {
		arms[k] = estimateRatioArm(k, byArm[k])
	}

	ctrl := arms[opts.Control]
	for _, k := range order {
		a := arms[k]
		if k != opts.Control {
			compareToControl(&a, ctrl)
		}
		rep.Arms = append(rep.Arms, a)
		if a.Estimable && a.SE > 0 {
			div := 100 * math.Abs(a.SECluster-a.SE) / a.SE
			if div > rep.CrossCheckMaxDivergencePct {
				rep.CrossCheckMaxDivergencePct = div
			}
		}
	}
	rep.CrossCheckOK = rep.CrossCheckMaxDivergencePct <= 1
	rep.Note = ratioNote(rep)
	return rep, nil
}

// estimateRatioArm is the delta method, plus the same variance computed a second way.
func estimateRatioArm(key string, rows []RatioUnit) RatioArm {
	a := RatioArm{Key: key, Units: len(rows)}
	if len(rows) < 2 {
		a.Reason = "fewer than two " + "randomisation units in this arm, so a ratio has no standard error yet"
		return a
	}
	s := make([]float64, len(rows))
	n := make([]float64, len(rows))
	for i, u := range rows {
		s[i], n[i] = u.Num, u.Den
		a.Num += u.Num
		a.Den += u.Den
	}
	a.AnalysisUnits = a.Den
	a.MeanClusterSize = a.Den / float64(len(rows))
	if a.Den <= 0 {
		a.Reason = "every unit in this arm has a denominator of zero — there is nothing to divide by"
		return a
	}
	K := float64(len(rows))
	muN := a.Den / K
	theta := a.Num / a.Den
	a.Ratio = theta
	a.Estimable = true

	varS, varN, covSN := moments(s, n)
	// Var(S_bar/N_bar) ~= (1/(K*muN^2)) * [ var(S) - 2*theta*cov(S,N) + theta^2*var(N) ]
	//
	// The middle term is the one everybody drops. Leaving it out does not add noise, it removes a
	// subtraction that is almost always positive, so the interval comes out narrower and the
	// p-value smaller — wrong in the direction that gets a change shipped.
	v := (varS - 2*theta*covSN + theta*theta*varN) / (K * muN * muN)
	if v < 0 {
		v = 0 // only reachable on degenerate rows; a negative variance is never reported as one
	}
	a.SE = math.Sqrt(v)

	vNoCov := (varS + theta*theta*varN) / (K * muN * muN)
	a.SEDeltaNoCov = math.Sqrt(math.Max(0, vNoCov))

	// The cluster-robust route: each unit's residual around the fitted ratio, summed. Same
	// estimator, different arithmetic, so a disagreement is a bug rather than a modelling choice.
	var ss float64
	for i := range rows {
		r := s[i] - theta*n[i]
		ss += r * r
	}
	a.SECluster = math.Sqrt(ss / ((K - 1) * K * muN * muN))

	// The naive comparison needs the analysis units themselves; without them we say nothing
	// rather than approximating the thing whose whole point is being exact.
	if vals := allValues(rows); len(vals) >= 2 {
		naiveVar := variance(vals) / float64(len(vals))
		a.SENaive = math.Sqrt(naiveVar)
		if a.SENaive > 0 {
			a.ClusterInflation = a.SE / a.SENaive
		}
	}

	a.RatioLo = theta - z95*a.SE
	a.RatioHi = theta + z95*a.SE
	return a
}

// compareToControl fills in the relative comparison, on the log scale for the same reason
// liftInterval uses it: relative change is a ratio, its sampling distribution is skewed, and a
// symmetric interval would put the lower bound below -100%, which cannot happen.
func compareToControl(a *RatioArm, ctrl RatioArm) {
	if !a.Estimable || !ctrl.Estimable {
		a.Read = "one of the two arms has no estimable ratio yet, so there is nothing to compare"
		return
	}
	// Same near-zero-control guard as the binary path, stated in these units: a control ratio
	// statistically indistinguishable from zero turns any movement into a four-figure percentage.
	if ctrl.Ratio <= 0 || (ctrl.SE > 0 && ctrl.Ratio < 10*ctrl.SE) {
		a.Read = "the control's " + "ratio is too close to zero for a relative comparison to mean anything — read the raw totals instead"
		return
	}
	if a.Ratio <= 0 {
		a.Read = "this arm's ratio is zero against a control of " + f4(ctrl.Ratio) +
			" — a relative lift is undefined against zero, but this arm is strictly worse"
		return
	}
	a.DeltaDefined = true
	a.DeltaLogPoint = math.Log(a.Ratio / ctrl.Ratio)
	a.DeltaLogSE = math.Sqrt((a.SE*a.SE)/(a.Ratio*a.Ratio) + (ctrl.SE*ctrl.SE)/(ctrl.Ratio*ctrl.Ratio))
	a.DeltaPct = round1(100 * (a.Ratio/ctrl.Ratio - 1))
	ci, _ := a.DeltaIntervalAtZ(z95)
	c := ci
	a.DeltaCI = &c

	// The p-value comes from the SAME log-scale statistic the interval is built from, so
	// "significant" and "the range excludes zero" are two readings of one number and cannot
	// contradict each other. Testing the difference of ratios on the absolute scale instead —
	// |r_t - r_c| over sqrt(SE_t^2 + SE_c^2), which is the natural thing to reach for — is a
	// different test, and on a skewed ratio it disagrees with the interval near the boundary: the
	// dashboard then shows a green "significant" badge next to a sentence saying the range still
	// spans zero, on the same row, and every number on the page loses its credibility at once.
	if a.DeltaLogSE > 0 {
		z := math.Abs(a.DeltaLogPoint) / a.DeltaLogSE
		a.PValue = round4(math.Erfc(z / math.Sqrt2))
		a.Significant = z >= z95
	}
	// Branch on the unrounded statistic, not on the displayed bounds. A lower bound of +0.04%
	// prints as "0.0" at one decimal place, and a switch on the rounded value would call a
	// significant result "no clear difference".
	switch {
	case a.Significant && a.DeltaLogPoint > 0:
		a.Read = "this arm is better; the true change is very likely between " + pct(ci.Lo) + " and " + pct(ci.Hi)
	case a.Significant && a.DeltaLogPoint < 0:
		a.Read = "this arm is worse; the true change is very likely between " + pct(ci.Lo) + " and " + pct(ci.Hi)
	default:
		a.Read = "no clear difference yet — the range still spans zero (" + pct(ci.Lo) + " to " + pct(ci.Hi) +
			"), so this could be a win or a loss"
	}
}

func ratioNote(rep RatioReport) string {
	n := "one " + rep.Numerator + " per " + rep.Denominator + ", aggregated to the " + rep.Unit +
		" first because that is what was randomised. Standard errors are delta-method with the " +
		"numerator/denominator covariance included, and cross-checked against the cluster-robust estimator."
	if !rep.CrossCheckOK {
		n = "WARNING: the delta-method and cluster-robust standard errors disagree by " +
			pct(round1(rep.CrossCheckMaxDivergencePct)) + ". They are provably the same estimator, so one of the two " +
			"implementations is wrong and nothing in this report should be acted on. " + n
	}
	if rep.Winsorized {
		n += " Values were winsorized at p" + trimFloat(rep.WinsorLoPct) + "/p" + trimFloat(rep.WinsorHiPct) +
			" (" + f4(rep.WinsorLoValue) + " to " + f4(rep.WinsorHiValue) + "), identically in every arm."
	} else {
		n += " No winsorization was applied, so a single outlier can move a mean-shaped metric on its own."
	}
	for _, a := range rep.Arms {
		if a.ClusterInflation > 1.05 {
			n += " Treating each " + rep.Denominator + " as independently randomised would have understated the " +
				"standard error by about " + trimFloat(round1(a.ClusterInflation*10)/10) + "x."
			break
		}
	}
	return n
}

// --- moments, winsorization, plumbing ------------------------------------------------------------

// moments returns the sample variances and covariance, all on the same (K-1) denominator. Using
// the same denominator everywhere is what makes the delta-method and cluster-robust routes
// comparable to floating-point noise instead of to within a factor of K/(K-1).
func moments(s, n []float64) (varS, varN, covSN float64) {
	k := len(s)
	if k < 2 {
		return 0, 0, 0
	}
	ms, mn := mean(s), mean(n)
	for i := 0; i < k; i++ {
		ds, dn := s[i]-ms, n[i]-mn
		varS += ds * ds
		varN += dn * dn
		covSN += ds * dn
	}
	d := float64(k - 1)
	return varS / d, varN / d, covSN / d
}

func allValues(rows []RatioUnit) []float64 {
	total := 0
	for _, u := range rows {
		if len(u.Values) == 0 {
			return nil // partial coverage would silently compare different populations
		}
		total += len(u.Values)
	}
	out := make([]float64, 0, total)
	for _, u := range rows {
		out = append(out, u.Values...)
	}
	return out
}

// winsorBounds computes the pooled percentile cutoffs. Linear interpolation between order
// statistics (the "type 7" definition), chosen because it is the one every stats package agrees
// on, and stated here so a reader reproducing the number by hand knows which of the nine
// definitions to use.
func winsorBounds(rows []RatioUnit, loPct, hiPct float64) (float64, float64, bool) {
	vals := make([]float64, 0)
	for _, u := range rows {
		if len(u.Values) == 0 {
			return 0, 0, false // not enumerable, so there is nothing to cap
		}
		vals = append(vals, u.Values...)
	}
	if len(vals) < 2 {
		return 0, 0, false
	}
	sort.Float64s(vals)
	q := func(p float64) float64 {
		if p <= 0 {
			return vals[0]
		}
		if p >= 100 {
			return vals[len(vals)-1]
		}
		h := (p / 100) * float64(len(vals)-1)
		lo := int(math.Floor(h))
		hi := int(math.Ceil(h))
		return vals[lo] + (h-float64(lo))*(vals[hi]-vals[lo])
	}
	lo, hi := q(loPct), q(hiPct)
	if !(hi > lo) {
		return 0, 0, false
	}
	return lo, hi, true
}

func applyWinsor(rows []RatioUnit, lo, hi float64) []RatioUnit {
	out := make([]RatioUnit, len(rows))
	for i, u := range rows {
		vals := make([]float64, len(u.Values))
		sum := 0.0
		for j, v := range u.Values {
			v = math.Min(hi, math.Max(lo, v))
			vals[j] = v
			sum += v
		}
		out[i] = RatioUnit{Unit: u.Unit, Variant: u.Variant, Num: sum, Den: u.Den, Values: vals}
	}
	return out
}

// --- building rows from raw events ---------------------------------------------------------------

// RatioSpec describes a ratio metric in terms of events.
//
// The denominator has three shapes and they are genuinely different metrics:
//   - DenominatorProp set (e.g. "session_id"): the analysis unit is a distinct value of that
//     property, so the rows are per-session and the clustering diagnostic is available.
//   - DenominatorEvent set: the denominator counts a second event (impressions against clicks).
//     There is no per-analysis-unit decomposition, so no winsorization and no naive comparison.
//   - neither: one analysis unit per randomisation unit, i.e. a plain per-user mean.
type RatioSpec struct {
	Flag             string
	Numerator        string // event whose occurrences form the numerator
	NumeratorProp    string // optional numeric property to sum instead of counting occurrences
	DenominatorEvent string
	DenominatorProp  string
	From, To         time.Time // half-open [From, To)
}

// BuildRatioUnits aggregates raw events into per-randomisation-unit rows, counting only what
// happened at or after each unit's first exposure — the same attribution rule the binary path
// uses, so the two never disagree about which events belong to the experiment.
func BuildRatioUnits(evs []event.Event, spec RatioSpec) ([]RatioUnit, string, error) {
	unitKind, unitOf, err := resolveRandomisationUnit(evs, spec.Flag, spec.From, spec.To)
	if err != nil {
		return nil, "", err
	}
	type acc struct {
		variant   string
		exposedAt time.Time
		num       float64
		den       float64
		perAU     map[string]float64
		auOrder   []string
	}
	units := map[string]*acc{}
	order := []string{}

	for _, e := range evs {
		if e.Name != ExposureEvent || !inHalfOpen(e.Timestamp, spec.From, spec.To) {
			continue
		}
		if fk, _ := e.Properties[PropFlag].(string); fk != spec.Flag {
			continue
		}
		id := unitOf[e.DistinctID]
		if id == "" {
			continue
		}
		a := units[id]
		if a == nil {
			a = &acc{perAU: map[string]float64{}}
			units[id] = a
			order = append(order, id)
		}
		if a.exposedAt.IsZero() || e.Timestamp.Before(a.exposedAt) {
			a.exposedAt = e.Timestamp
			v, _ := e.Properties[PropVariant].(string)
			a.variant = v
		}
	}

	// Numerator events that cannot be placed in any analysis unit. Counted rather than dropped:
	// see the refusal below.
	unattributed := 0
	for _, e := range evs {
		id := unitOf[e.DistinctID]
		if id == "" {
			continue
		}
		a := units[id]
		if a == nil || a.exposedAt.IsZero() {
			continue
		}
		if !inHalfOpen(e.Timestamp, spec.From, spec.To) || e.Timestamp.Before(a.exposedAt) {
			continue
		}
		// The denominator is every analysis unit that OCCURRED, not every one that converted.
		// Registering sessions only when a purchase lands in them is the single easiest way to
		// compute a confidently wrong ratio here: it silently changes "revenue per session" into
		// "revenue per converting session", which is larger, moves with conversion rate, and looks
		// entirely plausible. Any event carrying the property is evidence the analysis unit existed.
		if spec.DenominatorProp != "" {
			if au, ok := e.Properties[spec.DenominatorProp].(string); ok && au != "" {
				if _, seen := a.perAU[au]; !seen {
					a.perAU[au] = 0
					a.auOrder = append(a.auOrder, au)
				}
			}
		}
		if e.Name == spec.Numerator {
			v, ok := 1.0, true
			if spec.NumeratorProp != "" {
				// A purchase with no revenue property is not a zero-revenue purchase; it is an
				// event we cannot value, and averaging in a zero would understate every arm it
				// lands in.
				v, ok = ratioNumericValue(e.Properties[spec.NumeratorProp])
			}
			if ok {
				if spec.DenominatorProp != "" {
					au, hasAU := e.Properties[spec.DenominatorProp].(string)
					if !hasAU || au == "" {
						unattributed++
					} else {
						a.perAU[au] += v
						a.num += v
					}
				} else {
					a.num += v
				}
			}
		}
		if spec.DenominatorEvent != "" && e.Name == spec.DenominatorEvent {
			a.den++
		}
	}
	// Refuse rather than pick one of two wrong answers. A numerator event with no analysis-unit
	// property can either be counted in the total (and then the per-unit values no longer sum to
	// it, so winsorization and the clustering diagnostic are computed on a different metric than
	// the ratio) or dropped (and then revenue silently vanishes from the report). Both are wrong
	// and neither is visible in the output, so this says so instead.
	if unattributed > 0 {
		return nil, "", cupedError(itoa(unattributed) + " " + spec.Numerator + " events carry no " +
			spec.DenominatorProp + ", so they belong to no " + spec.DenominatorProp + " and cannot be counted in a " +
			"per-" + spec.DenominatorProp + " ratio. Including them would make the per-unit values disagree with the " +
			"total; dropping them would lose real events with nothing on the report to say so. Stamp " +
			spec.DenominatorProp + " on every " + spec.Numerator + ", or measure this per " + unitKind + " instead.")
	}

	out := make([]RatioUnit, 0, len(order))
	for _, id := range order {
		a := units[id]
		if a.variant == "" {
			continue
		}
		u := RatioUnit{Unit: id, Variant: a.variant, Num: a.num}
		switch {
		case spec.DenominatorProp != "":
			u.Den = float64(len(a.auOrder))
			for _, au := range a.auOrder {
				u.Values = append(u.Values, a.perAU[au])
			}
		case spec.DenominatorEvent != "":
			u.Den = a.den // no per-analysis-unit decomposition exists for a two-event ratio
		default:
			u.Den = 1
			u.Values = []float64{a.num}
		}
		out = append(out, u)
	}
	return out, unitKind, nil
}

// ratioNumericValue accepts the shapes a JSON number arrives in. A string that looks like a number is
// deliberately NOT accepted: silently coercing "12.50" would make a typo in the SDK indistinguish-
// able from a real amount, and the sum would be wrong with nothing logged.
// One definition of "is this a number", shared with every other caller. See event.Numeric.
func ratioNumericValue(v any) (float64, bool) { return event.Numeric(v) }
