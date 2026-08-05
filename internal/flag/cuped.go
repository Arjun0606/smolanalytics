package flag

import (
	"math"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// CUPED — variance reduction by regression adjustment on a strictly pre-exposure covariate.
//
// The idea is old and boring and works: a user who bought four times last week is likely to buy
// again this week no matter which arm they landed in. That predictable part of the outcome is
// noise as far as the experiment is concerned, and subtracting it shrinks the interval without
// touching the estimand. Microsoft measured 20-50% variance reduction on engagement metrics,
// which is the same thing as 20-50% more traffic you did not have to wait for.
//
// Two decisions here are load-bearing and both exist to stop a confidently wrong number:
//
//  1. REGRESSION ADJUSTMENT, NOT THE CLOSED FORM. The textbook CUPED writes
//     theta = Cov(Y,X)/Var(X) pooled over everyone. That pooled theta is only the right one when
//     the arms happen to have the same covariate mean; when they do not — and with a few hundred
//     users they never exactly do — it leaves the pre-exposure imbalance in the estimate. Fitting
//     Y = a + b*T + theta*X gives the WITHIN-ARM pooled slope (Lin 2013), which is exactly the
//     adjustment that cancels that imbalance, and it can never hurt asymptotic precision. The
//     treatment coefficient b IS the adjusted difference, and the same code path gives
//     post-stratification (X = stratum dummies) and CUPAC (X = an out-of-sample prediction) for
//     free. One auditable path, three features.
//
//  2. AN HONEST REFUSAL. Every implementation of CUPED reduces the IN-SAMPLE residual variance,
//     always, including when the covariate is pure noise — least squares cannot do otherwise.
//     So "the adjusted variance is smaller" is not evidence that the adjustment did anything, and
//     a tool that reports a reduction whenever it computes one is reporting the fit of noise as a
//     win. When the covariate is too weakly correlated to help, this refuses, says so, names the
//     correlation, and leaves the interval alone. Nobody in the category tells you CUPED did
//     nothing.
//
// Nothing here reads the clock. The window arrives as absolute instants and is echoed back, so
// the same events always produce the same adjustment (binding rule 1). Every window is half-open.

// PropBucketID is the event property carrying the browser's stable bucket key — the randomisation
// unit named by UnitBucketID in experiment.go. The evaluate endpoint already accepts bucket_id as
// a query parameter (internal/api/flags_api.go), but the SDK does not yet stamp it onto
// $feature_flag_called, so today most instances fall back to distinct_id. That fallback is stated
// in every result rather than hidden, because the two are different estimands: a user who logs in
// mid-test is one unit under bucket_id and two under distinct_id.
const PropBucketID = "$bucket_id"

// The per-user pre-exposure window is DefaultCUPEDLookbackDays (experiment.go). Per user rather
// than a fixed calendar window: each user's covariate is measured in the days before THAT user's
// own first exposure, so a user exposed on day 1 and a user exposed on day 12 both get a clean
// pre-period. A shared calendar window would put half of the later user's "pre-period" after the
// experiment started, which is the covariate being contaminated by the treatment.

// Guards. Below these the adjustment is estimated from too little to be worth anything, and an
// interval that got narrower because theta was fit on 40 users is a worse number than the one it
// replaced.
const (
	minCUPEDUnitsWithPre = 100  // absolute floor on units carrying a pre-period
	minCUPEDPreCoverage  = 0.05 // and they must be more than 5% of the sample
	minCUPEDReduction    = 0.01 // below a 1% variance cut there is nothing to claim
)

// CUPEDUnit is one randomisation unit's outcome and pre-exposure covariate.
//
// It carries the covariate window and the exposure instant, not just the number, because "was
// this covariate measured strictly before treatment" is the one property that makes the whole
// method valid, and it has to be checkable by the code that uses the number rather than trusted
// of whoever built the rows. A covariate that overlaps exposure is post-treatment: adjusting on
// it bakes the treatment effect into the control variable and biases the answer toward zero, with
// a narrower interval to make it look better.
type CUPEDUnit struct {
	Unit    string  `json:"unit"`    // randomisation-unit id
	Variant string  `json:"variant"` // arm this unit was exposed to (first exposure wins)
	Y       float64 `json:"y"`       // outcome
	X       float64 `json:"x"`       // pre-exposure covariate; meaningless when HasPre is false
	// HasPre says the unit was OBSERVABLE for the whole lookback, not that X is non-zero. A user
	// first seen an hour before exposure has X=0 because we were not watching, not because they
	// did nothing, and treating those two as the same value is how a new-user experiment gets a
	// covariate that is really a proxy for signup date.
	HasPre    bool      `json:"has_pre"`
	ExposedAt time.Time `json:"exposed_at"`
	CovFrom   time.Time `json:"cov_from"`
	CovTo     time.Time `json:"cov_to"` // exclusive; must never be after ExposedAt
}

// CUPEDOptions describes the rows so the result can state what it did. Nothing here changes the
// arithmetic except the guard thresholds; it exists so no reader has to ask what the covariate was.
type CUPEDOptions struct {
	Unit         string    `json:"unit"`          // UnitBucketID or UnitDistinctID
	Covariate    string    `json:"covariate"`     // event name counted in the pre-period
	LookbackDays int       `json:"lookback_days"` // 0 → DefaultCUPEDLookbackDays
	From         time.Time `json:"from"`          // resolved report window, echoed
	To           time.Time `json:"to"`
}

// CUPEDResult is the adjustment, its cost, and — when it was skipped — why.
//
// RawSE and AdjSE are both here on purpose. "CUPED cut your variance 31%" is an assertion; two
// standard errors a reader can divide is a measurement. Whoever composes this with a sequential
// interval must take AdjDiff and AdjSE FIRST and inflate the resulting half-width by k(N) second
// (binding rule 5) — inflating the raw SE and then adjusting would apply the always-valid penalty
// to a variance the estimator no longer has.
//
// When Applied is false, AdjDiff and AdjSE are set to the raw values. A caller that always reads
// the adjusted fields is then always correct, and cannot accidentally report an unadjusted number
// as adjusted or branch its way into an inconsistency.
type CUPEDResult struct {
	Applied      bool   `json:"applied"`
	SkipReason   string `json:"skip_reason,omitempty"`
	Unit         string `json:"unit"`
	Covariate    string `json:"covariate,omitempty"`
	LookbackDays int    `json:"lookback_days"`

	Control   string `json:"control"`
	Treatment string `json:"treatment"`

	Units      int `json:"units"`          // units in both arms
	UnitsWithP int `json:"units_with_pre"` // units carrying a usable pre-period

	Theta float64 `json:"theta"` // within-arm pooled regression coefficient on the covariate
	Rho   float64 `json:"rho"`   // correlation of Y with X among units that have a pre-period

	RawDiff float64 `json:"raw_diff"`
	RawSE   float64 `json:"raw_se"`
	AdjDiff float64 `json:"adj_diff"`
	AdjSE   float64 `json:"adj_se"`

	// VarianceReductionPct is measured from the two standard errors actually produced
	// (1 - (AdjSE/RawSE)^2), not derived from rho. The identity 1-rho^2 is what the method
	// promises asymptotically; this is what it delivered on these rows.
	VarianceReductionPct float64 `json:"variance_reduction_pct"`
	// EquivalentExtraUnits is that reduction restated in the only currency an operator cares
	// about: how many more users they would have had to wait for to get this interval without it.
	EquivalentExtraUnits int `json:"equivalent_extra_units"`

	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Note string `json:"note"`
}

// HalfWidthAtZ is the half-width of the adjusted interval at critical value z.
//
// Exposed as a method so the sequential mode composes in the fixed order: adjust, then inflate.
// Pass z95 for a fixed-horizon interval, or z95*k(N) for the always-valid one.
func (r CUPEDResult) HalfWidthAtZ(z float64) float64 { return z * r.AdjSE }

// CUPEDAdjust computes the covariate-adjusted difference in means between one treatment arm and
// the control arm, with heteroskedasticity-robust (HC1) standard errors.
//
// Returns an error only for a covariate that is not strictly pre-exposure — that is a broken
// input rather than a thin one, and silently adjusting on it would bias the estimate toward zero
// while making the interval narrower, which is the worst combination available. Everything else
// that goes wrong is a SKIP with a reason, because "we could not help here" is an answer.
func CUPEDAdjust(units []CUPEDUnit, control, treatment string, opts CUPEDOptions) (CUPEDResult, error) {
	lookback := opts.LookbackDays
	if lookback <= 0 {
		lookback = DefaultCUPEDLookbackDays
	}
	unitKind := opts.Unit
	if unitKind == "" {
		unitKind = UnitDistinctID
	}
	res := CUPEDResult{
		Unit:         unitKind,
		Covariate:    opts.Covariate,
		LookbackDays: lookback,
		Control:      control,
		Treatment:    treatment,
		From:         opts.From,
		To:           opts.To,
	}

	// Keep only the two arms being compared, in a stable order. A unit exposed to a third arm is
	// not part of this contrast and averaging it in would answer a question nobody asked.
	rows := make([]CUPEDUnit, 0, len(units))
	for _, u := range units {
		if u.Variant != control && u.Variant != treatment {
			continue
		}
		// The validity check, run on every row rather than trusted of the builder.
		if u.HasPre && !u.CovTo.IsZero() && !u.ExposedAt.IsZero() && u.CovTo.After(u.ExposedAt) {
			return res, errCUPEDPostTreatment(u)
		}
		rows = append(rows, u)
	}
	res.Units = len(rows)

	nC, nT := 0, 0
	for _, u := range rows {
		if u.Variant == control {
			nC++
		} else {
			nT++
		}
	}
	if nC < 2 || nT < 2 {
		res.SkipReason = "each arm needs at least two units before a difference has a standard error at all; " +
			"control has " + itoa(nC) + " and " + treatment + " has " + itoa(nT)
		return finishSkipped(res), nil
	}

	// The unadjusted comparison, from the same estimator as the adjusted one. Computing the
	// baseline a different way (a pooled two-proportion SE, say) would make the reduction partly
	// an artefact of changing estimators, and the whole claim here is a like-for-like ratio.
	rawB, rawSE, rawOK := olsHC1(designNoCovariate(rows, treatment), outcomes(rows))
	if !rawOK {
		res.SkipReason = "the unadjusted comparison is not estimable on these rows, so there is nothing to improve on"
		return finishSkipped(res), nil
	}
	res.RawDiff, res.RawSE = rawB[1], rawSE[1]
	res.AdjDiff, res.AdjSE = res.RawDiff, res.RawSE

	withPre := 0
	for _, u := range rows {
		if u.HasPre {
			withPre++
		}
	}
	res.UnitsWithP = withPre
	res.Rho = cupedRho(rows)

	switch {
	case withPre == 0:
		res.SkipReason = "none of these " + itoa(len(rows)) + " users existed before they were exposed, so there is no " +
			"pre-period to adjust against. This is a new-user experiment: your interval is unchanged and that is correct, not a bug."
		return finishSkipped(res), nil
	case withPre < minCUPEDUnitsWithPre:
		res.SkipReason = "only " + itoa(withPre) + " users have a pre-period (we need " + itoa(minCUPEDUnitsWithPre) +
			"). Fitting the adjustment on that few would narrow the interval using an estimate noisier than the thing it is correcting."
		return finishSkipped(res), nil
	case float64(withPre)/float64(len(rows)) <= minCUPEDPreCoverage:
		res.SkipReason = "only " + pct(round1(100*float64(withPre)/float64(len(rows)))) + " of exposed users have a pre-period, " +
			"so the adjustment would be carried almost entirely by a small, unrepresentative slice of the sample"
		return finishSkipped(res), nil
	}

	// The noise floor for a sample correlation. Under a truly unrelated covariate rho-hat has a
	// standard deviation of about 1/sqrt(n), so anything inside two of those is indistinguishable
	// from zero, and "adjusting" on it is relabelling noise as a variance reduction. This is the
	// guard that makes the refusal honest rather than decorative: without it the code below would
	// report a reduction on literally any covariate, including a random one.
	floor := 2 / math.Sqrt(float64(withPre))
	if math.Abs(res.Rho) <= floor {
		res.SkipReason = "CUPED was skipped: the correlation between pre-period and post-period behaviour is " +
			f2(res.Rho) + ", which on " + itoa(withPre) + " users is inside the noise floor of " + f2(floor) +
			" — there is nothing predictable to subtract. Your interval is unchanged and that is correct, not a bug."
		return finishSkipped(res), nil
	}

	x, hasMissing := covariates(rows)
	if variance(x) <= 0 {
		res.SkipReason = "every user has the same value for this covariate, so it cannot explain any of the variation in the outcome"
		return finishSkipped(res), nil
	}
	adjB, adjSE, adjOK := olsHC1(designWithCovariate(rows, treatment, x, hasMissing), outcomes(rows))
	if !adjOK {
		res.SkipReason = "the covariate is collinear with the arm assignment on these rows, so the adjusted model is not identified"
		return finishSkipped(res), nil
	}

	adjDiff, adjSEv, theta := adjB[1], adjSE[1], adjB[2]
	if !(adjSEv > 0) || !(res.RawSE > 0) || adjSEv >= res.RawSE {
		res.SkipReason = "the adjusted standard error (" + f4(adjSEv) + ") is no smaller than the unadjusted one (" +
			f4(res.RawSE) + "), so the adjustment cost more than it bought"
		return finishSkipped(res), nil
	}
	reduction := 1 - (adjSEv*adjSEv)/(res.RawSE*res.RawSE)
	if reduction < minCUPEDReduction {
		res.SkipReason = "CUPED would have cut the variance by " + pct(round1(100*reduction)) +
			", which is not worth reporting as an improvement; the unadjusted interval stands"
		return finishSkipped(res), nil
	}

	res.Applied = true
	res.Theta = theta
	res.AdjDiff, res.AdjSE = adjDiff, adjSEv
	res.VarianceReductionPct = round1(100 * reduction)
	// At a fixed effect, the standard error falls with 1/sqrt(n), so the extra sample that would
	// have bought this same interval unaided is n*((raw/adj)^2 - 1).
	res.EquivalentExtraUnits = int(math.Round(float64(len(rows)) * ((res.RawSE*res.RawSE)/(adjSEv*adjSEv) - 1)))
	res.Note = "CUPED cut the variance " + pct(res.VarianceReductionPct) + " using each user's " + opts.Covariate +
		" count in the " + itoa(lookback) + " days before their own first exposure (correlation " + f2(res.Rho) +
		"). That is worth about " + itoa(res.EquivalentExtraUnits) + " extra users you did not have to wait for. " +
		"Randomisation unit: " + unitKind + "."
	return res, nil
}

// finishSkipped fills in the fields a skipped result still owes the reader. The adjusted numbers
// stay equal to the raw ones so a consumer that always reads AdjDiff/AdjSE is never silently
// reporting an unadjusted figure as adjusted.
func finishSkipped(res CUPEDResult) CUPEDResult {
	res.Applied = false
	res.AdjDiff, res.AdjSE = res.RawDiff, res.RawSE
	res.VarianceReductionPct = 0
	res.EquivalentExtraUnits = 0
	res.Note = "no variance reduction was applied — " + res.SkipReason
	return res
}

func errCUPEDPostTreatment(u CUPEDUnit) error {
	return cupedError("the covariate for unit " + u.Unit + " is measured up to " + u.CovTo.UTC().Format(time.RFC3339) +
		", which is after that unit's first exposure at " + u.ExposedAt.UTC().Format(time.RFC3339) +
		". A covariate that overlaps the treatment is not a covariate: adjusting on it absorbs part of the effect " +
		"being measured and reports a narrower interval around a smaller number. Rebuild the rows with a lookback " +
		"that ends at each unit's own exposure instant.")
}

type cupedError string

func (e cupedError) Error() string { return string(e) }

// --- row helpers -------------------------------------------------------------------------------

func outcomes(rows []CUPEDUnit) []float64 {
	y := make([]float64, len(rows))
	for i, u := range rows {
		y[i] = u.Y
	}
	return y
}

// covariates returns the covariate column with missing values imputed at the observed mean, plus
// whether any were missing.
//
// Imputing the mean rather than zero is the difference between "we did not observe this user's
// history" and "this user did nothing", and those are not the same claim. A mean-imputed unit
// contributes nothing to the adjustment (X - Xbar = 0 for it) instead of being pulled toward the
// low end of the covariate, which is what a zero would do. The missing-indicator column added
// alongside it keeps the treatment coefficient unbiased: both columns are functions of
// pre-exposure information only, and under randomisation any pre-treatment covariate is safe to
// adjust on.
func covariates(rows []CUPEDUnit) ([]float64, bool) {
	sum, n := 0.0, 0
	for _, u := range rows {
		if u.HasPre {
			sum += u.X
			n++
		}
	}
	mean := 0.0
	if n > 0 {
		mean = sum / float64(n)
	}
	x := make([]float64, len(rows))
	missing := false
	for i, u := range rows {
		if u.HasPre {
			x[i] = u.X
		} else {
			x[i] = mean
			missing = true
		}
	}
	return x, missing
}

func designNoCovariate(rows []CUPEDUnit, treatment string) [][]float64 {
	d := make([][]float64, len(rows))
	for i, u := range rows {
		t := 0.0
		if u.Variant == treatment {
			t = 1
		}
		d[i] = []float64{1, t}
	}
	return d
}

func designWithCovariate(rows []CUPEDUnit, treatment string, x []float64, hasMissing bool) [][]float64 {
	d := make([][]float64, len(rows))
	for i, u := range rows {
		t := 0.0
		if u.Variant == treatment {
			t = 1
		}
		row := []float64{1, t, x[i]}
		if hasMissing {
			m := 0.0
			if !u.HasPre {
				m = 1
			}
			row = append(row, m)
		}
		d[i] = row
	}
	return d
}

// cupedRho is the Pearson correlation between outcome and covariate over the units that actually
// have a pre-period. Computed on that subset because it is the number the refusal message quotes,
// and quoting a correlation diluted by imputed rows would understate exactly when the covariate is
// good and coverage is thin.
func cupedRho(rows []CUPEDUnit) float64 {
	ys := make([]float64, 0, len(rows))
	xs := make([]float64, 0, len(rows))
	for _, u := range rows {
		if u.HasPre {
			ys = append(ys, u.Y)
			xs = append(xs, u.X)
		}
	}
	return correlation(ys, xs)
}

func correlation(a, b []float64) float64 {
	if len(a) != len(b) || len(a) < 2 {
		return 0
	}
	ma, mb := mean(a), mean(b)
	var sab, saa, sbb float64
	for i := range a {
		da, db := a[i]-ma, b[i]-mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	if saa <= 0 || sbb <= 0 {
		return 0
	}
	return sab / math.Sqrt(saa*sbb)
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func variance(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := mean(v)
	s := 0.0
	for _, x := range v {
		d := x - m
		s += d * d
	}
	return s / float64(len(v)-1)
}

// --- OLS ---------------------------------------------------------------------------------------

// olsHC1 fits y = Xb by ordinary least squares and returns the coefficients with
// heteroskedasticity-consistent (HC1) standard errors.
//
// HC1 rather than the classical homoskedastic SE because a binary outcome guarantees
// heteroskedasticity by construction — the variance of a Bernoulli is p(1-p), which differs
// between arms whenever the effect is non-zero. The classical formula would understate the
// standard error precisely on the experiments that found something, and the finite-sample n/(n-k)
// factor keeps it from being optimistic on small samples.
//
// Solved by Gaussian elimination with partial pivoting: deterministic, no dependency, and the
// design matrices here have three or four columns.
func olsHC1(x [][]float64, y []float64) (b []float64, se []float64, ok bool) {
	n := len(y)
	if n == 0 || len(x) != n {
		return nil, nil, false
	}
	k := len(x[0])
	if k == 0 || n <= k {
		return nil, nil, false
	}
	xtx := make([][]float64, k)
	for i := range xtx {
		xtx[i] = make([]float64, k)
	}
	xty := make([]float64, k)
	for r := 0; r < n; r++ {
		if len(x[r]) != k {
			return nil, nil, false
		}
		for i := 0; i < k; i++ {
			xty[i] += x[r][i] * y[r]
			for j := 0; j < k; j++ {
				xtx[i][j] += x[r][i] * x[r][j]
			}
		}
	}
	inv, ok := invert(xtx)
	if !ok {
		return nil, nil, false
	}
	b = make([]float64, k)
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			b[i] += inv[i][j] * xty[j]
		}
	}
	// meat = sum of u_i^2 * x_i x_i', the heteroskedasticity-robust middle of the sandwich
	meat := make([][]float64, k)
	for i := range meat {
		meat[i] = make([]float64, k)
	}
	for r := 0; r < n; r++ {
		fit := 0.0
		for i := 0; i < k; i++ {
			fit += x[r][i] * b[i]
		}
		u := y[r] - fit
		u2 := u * u
		for i := 0; i < k; i++ {
			for j := 0; j < k; j++ {
				meat[i][j] += u2 * x[r][i] * x[r][j]
			}
		}
	}
	scale := float64(n) / float64(n-k) // HC1's finite-sample correction
	se = make([]float64, k)
	for i := 0; i < k; i++ {
		v := 0.0
		for a := 0; a < k; a++ {
			for c := 0; c < k; c++ {
				v += inv[i][a] * meat[a][c] * inv[c][i]
			}
		}
		v *= scale
		if v < 0 {
			return nil, nil, false // only reachable through catastrophic cancellation; refuse rather than sqrt a negative
		}
		se[i] = math.Sqrt(v)
	}
	return b, se, true
}

// invert inverts a small square matrix by Gauss-Jordan elimination with partial pivoting.
// Reports false on a singular (or numerically singular) matrix rather than returning infinities,
// because a collinear design should skip the adjustment, not print one made of NaNs.
func invert(m [][]float64) ([][]float64, bool) {
	k := len(m)
	a := make([][]float64, k)
	for i := range a {
		a[i] = make([]float64, 2*k)
		copy(a[i], m[i])
		a[i][k+i] = 1
	}
	for col := 0; col < k; col++ {
		p := col
		for r := col + 1; r < k; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[p][col]) {
				p = r
			}
		}
		if math.Abs(a[p][col]) < 1e-12 {
			return nil, false
		}
		a[col], a[p] = a[p], a[col]
		pv := a[col][col]
		for j := 0; j < 2*k; j++ {
			a[col][j] /= pv
		}
		for r := 0; r < k; r++ {
			if r == col || a[r][col] == 0 {
				continue
			}
			f := a[r][col]
			for j := 0; j < 2*k; j++ {
				a[r][j] -= f * a[col][j]
			}
		}
	}
	inv := make([][]float64, k)
	for i := range inv {
		inv[i] = make([]float64, k)
		copy(inv[i], a[i][k:])
	}
	return inv, true
}

// --- building rows from raw events ---------------------------------------------------------------

// CUPEDSpec describes the rows to build. Instants only — no durations resolved against a clock —
// so the same event log always yields the same covariate.
type CUPEDSpec struct {
	Flag         string
	Goal         string    // outcome event
	Covariate    string    // pre-period event to count; empty → Goal
	From, To     time.Time // half-open [From, To); zero To means no upper bound
	LookbackDays int       // 0 → DefaultCUPEDLookbackDays
	CountOutcome bool      // true → Y is the number of goal events; false → Y is 0/1 converted
}

// BuildCUPEDUnits derives one row per randomisation unit from raw events: the outcome inside the
// report window, and the covariate in the lookback window that ends at that unit's OWN first
// exposure.
//
// The covariate deliberately reads events from before `From`. That is the point — the pre-period
// sits before the experiment, so restricting it to the report window would leave every user with
// a covariate of zero and a method that quietly does nothing.
//
// Returns the randomisation unit it used. It refuses when some exposures carry a bucket id and
// others do not: that dataset is two estimands stacked on top of each other (device-scoped for
// some users, account-scoped for the rest), and silently picking one produces a number that is
// not an estimate of anything.
func BuildCUPEDUnits(evs []event.Event, spec CUPEDSpec) ([]CUPEDUnit, string, error) {
	unitKind, unitOf, err := resolveRandomisationUnit(evs, spec.Flag, spec.From, spec.To)
	if err != nil {
		return nil, "", err
	}
	cov := spec.Covariate
	if cov == "" {
		cov = spec.Goal
	}
	lookback := spec.LookbackDays
	if lookback <= 0 {
		lookback = DefaultCUPEDLookbackDays
	}

	type acc struct {
		variant   string
		exposedAt time.Time
		y         float64
		x         float64
		firstSeen time.Time
	}
	units := map[string]*acc{}
	order := []string{}

	// pass 1 — first exposure per unit, inside the window
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
			a = &acc{}
			units[id] = a
			order = append(order, id)
		}
		if a.exposedAt.IsZero() || e.Timestamp.Before(a.exposedAt) {
			a.exposedAt = e.Timestamp
			v, _ := e.Properties[PropVariant].(string)
			a.variant = v
		}
	}

	// pass 2 — outcome, covariate and first-ever sighting. Separate pass because the covariate
	// window is not known until the exposure instant is, and a single pass over unsorted events
	// would score early events against a window that later moves.
	for _, e := range evs {
		id := unitOf[e.DistinctID]
		if id == "" {
			continue
		}
		a := units[id]
		if a == nil || a.exposedAt.IsZero() {
			continue
		}
		if a.firstSeen.IsZero() || e.Timestamp.Before(a.firstSeen) {
			a.firstSeen = e.Timestamp
		}
		if e.Name == spec.Goal && inHalfOpen(e.Timestamp, spec.From, spec.To) && !e.Timestamp.Before(a.exposedAt) {
			if spec.CountOutcome {
				a.y++
			} else {
				a.y = 1
			}
		}
		if e.Name == cov {
			covFrom := a.exposedAt.AddDate(0, 0, -lookback)
			// half-open [covFrom, exposedAt): an event AT the exposure instant is not
			// pre-exposure, and counting it would make the covariate partly post-treatment
			if !e.Timestamp.Before(covFrom) && e.Timestamp.Before(a.exposedAt) {
				a.x++
			}
		}
	}

	out := make([]CUPEDUnit, 0, len(order))
	for _, id := range order {
		a := units[id]
		if a.exposedAt.IsZero() || a.variant == "" {
			continue
		}
		covFrom := a.exposedAt.AddDate(0, 0, -lookback)
		out = append(out, CUPEDUnit{
			Unit:    id,
			Variant: a.variant,
			Y:       a.y,
			X:       a.x,
			// Observable for the WHOLE lookback, not merely present at some point. A unit whose
			// first sighting is inside its own pre-period has a covariate that measures how long
			// we have been watching, which correlates with tenure and not with the outcome.
			HasPre:    !a.firstSeen.IsZero() && !a.firstSeen.After(covFrom),
			ExposedAt: a.exposedAt,
			CovFrom:   covFrom,
			CovTo:     a.exposedAt,
		})
	}
	return out, unitKind, nil
}

// resolveRandomisationUnit decides ONCE, for the whole dataset, what the coin was flipped on, and
// returns the distinct_id → unit mapping implied by it.
//
// One decision for the dataset rather than one per event: bucket_id is device-scoped and survives
// identify(), distinct_id does not, so a mixed dataset would collapse two users onto one unit here
// and split one user across two units there. The counts in the refusal name exactly how mixed it
// is, because the fix (ship the SDK version that stamps the bucket id, or wait for the old one to
// age out) depends on which way the split runs.
//
// One distinct_id can legitimately carry SEVERAL bucket ids — the same account signed in on a
// laptop and a phone is two devices, two coin flips. Goal events carry no bucket id, so they can
// only be attributed to one of them, and the choice has to be made the same way every time:
// EARLIEST exposure wins, ties broken by the smaller bucket id. Taking whichever exposure appeared
// last in the slice, which is the obvious implementation, makes the unit assignment depend on the
// order the events happen to be stored in — the same log read back sorted differently would put
// that user's conversions in the other arm and quietly change the reported lift. Earliest-wins also
// matches the attribution rule the rest of this package already uses (measure.go counts conversions
// from a unit's first exposure), so the two never disagree about which arm a user is in.
func resolveRandomisationUnit(evs []event.Event, flagKey string, from, to time.Time) (string, map[string]string, error) {
	type pick struct {
		at     time.Time
		bucket string
	}
	withBucket, withoutBucket := 0, 0
	picks := map[string]pick{}
	unitOf := map[string]string{}
	for _, e := range evs {
		if e.Name != ExposureEvent || !inHalfOpen(e.Timestamp, from, to) {
			continue
		}
		if fk, _ := e.Properties[PropFlag].(string); fk != flagKey {
			continue
		}
		if bid, ok := bucketIDOf(e); ok {
			withBucket++
			cur, seen := picks[e.DistinctID]
			if !seen || e.Timestamp.Before(cur.at) || (e.Timestamp.Equal(cur.at) && bid < cur.bucket) {
				picks[e.DistinctID] = pick{at: e.Timestamp, bucket: bid}
			}
		} else {
			withoutBucket++
			if _, seen := unitOf[e.DistinctID]; !seen {
				unitOf[e.DistinctID] = e.DistinctID
			}
		}
	}
	for did, p := range picks {
		unitOf[did] = p.bucket
	}
	switch {
	case withBucket == 0 && withoutBucket == 0:
		return UnitDistinctID, unitOf, nil // nothing exposed yet; the caller will report an empty result
	case withBucket > 0 && withoutBucket > 0:
		return "", nil, cupedError("this flag's exposures are randomised on two different units: " +
			itoa(withBucket) + " carry a bucket_id (device-scoped, survives login) and " + itoa(withoutBucket) +
			" carry only a distinct_id (account-scoped). Those are two different experiments and averaging them " +
			"estimates neither. Measure a window in which every exposure comes from one SDK version.")
	case withBucket > 0:
		return UnitBucketID, unitOf, nil
	default:
		return UnitDistinctID, unitOf, nil
	}
}

func bucketIDOf(e event.Event) (string, bool) {
	if v, ok := e.Properties[PropBucketID].(string); ok && v != "" {
		return v, true
	}
	if v, ok := e.Properties["bucket_id"].(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// inHalfOpen is the one window rule used everywhere here: [from, to), zero meaning unbounded.
// Half-open so an event exactly on a boundary is counted by exactly one of two adjacent windows.
func inHalfOpen(t, from, to time.Time) bool {
	if !from.IsZero() && t.Before(from) {
		return false
	}
	return to.IsZero() || t.Before(to)
}

func f2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }
func f4(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) }
