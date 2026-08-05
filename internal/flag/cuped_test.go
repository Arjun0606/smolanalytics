package flag

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Data for these tests is generated with a seeded RNG at a stated n, and the assertions are about
// what the IMPLEMENTATION produced on it. That distinction is the point: "the variance reduction
// equals 1-rho^2" is an algebraic identity and asserting it proves only that algebra still works.
// What can actually break is the regression, the robust standard error, the imputation of missing
// covariates and the guards, so the checks below compare the reported standard error against the
// spread the estimator really has over repeated samples, and the reported effect against the one
// that was injected.

// cupedRows builds n units, alternating arms, with corr(Y,X) = rho by construction and `effect`
// added to the treatment arm's outcome.
func cupedRows(seed int64, n int, rho, effect float64) []CUPEDUnit {
	r := rand.New(rand.NewSource(seed))
	exposed := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	rows := make([]CUPEDUnit, n)
	for i := 0; i < n; i++ {
		x := r.NormFloat64()
		y := rho*x + math.Sqrt(1-rho*rho)*r.NormFloat64()
		variant := "control"
		if i%2 == 1 {
			variant = "treatment"
			y += effect
		}
		rows[i] = CUPEDUnit{
			Unit:      "u" + itoa(i),
			Variant:   variant,
			Y:         y,
			X:         x,
			HasPre:    true,
			ExposedAt: exposed,
			CovFrom:   exposed.AddDate(0, 0, -7),
			CovTo:     exposed,
		}
	}
	return rows
}

func cupedOpts() CUPEDOptions {
	return CUPEDOptions{
		Unit:         UnitBucketID,
		Covariate:    "purchase",
		LookbackDays: 7,
		From:         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:           time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	}
}

// The adjustment has a closed form — the within-arm pooled slope, applied to centred covariates —
// and the shipped path computes it by least squares instead. Agreeing to 1e-9 on generated data
// means the matrix code, the pivoting and the coefficient extraction are all doing what the
// formula says, which is the half of CUPED that a reader can check by hand.
func TestOLSMatchesClosedFormANCOVA(t *testing.T) {
	rows := cupedRows(11, 500, 0.5, 0.3)
	got, err := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if !got.Applied {
		t.Fatalf("expected the adjustment to apply, skipped: %s", got.SkipReason)
	}

	// within-arm pooled slope: sum over arms of the arm-centred cross-products
	var sxy, sxx float64
	for _, arm := range []string{"control", "treatment"} {
		var xs, ys []float64
		for _, u := range rows {
			if u.Variant == arm {
				xs = append(xs, u.X)
				ys = append(ys, u.Y)
			}
		}
		mx, my := mean(xs), mean(ys)
		for i := range xs {
			sxy += (xs[i] - mx) * (ys[i] - my)
			sxx += (xs[i] - mx) * (xs[i] - mx)
		}
	}
	theta := sxy / sxx
	var yt, yc, xt, xc []float64
	for _, u := range rows {
		if u.Variant == "treatment" {
			yt, xt = append(yt, u.Y), append(xt, u.X)
		} else {
			yc, xc = append(yc, u.Y), append(xc, u.X)
		}
	}
	wantDiff := (mean(yt) - mean(yc)) - theta*(mean(xt)-mean(xc))

	if math.Abs(got.Theta-theta) > 1e-9 {
		t.Errorf("theta: OLS %.12f, closed form %.12f", got.Theta, theta)
	}
	if math.Abs(got.AdjDiff-wantDiff) > 1e-9 {
		t.Errorf("adjusted difference: OLS %.12f, closed form %.12f", got.AdjDiff, wantDiff)
	}
}

// Generated at a stated n with a stated covariance, and checked against what the implementation
// reports rather than against 1-rho^2 in the abstract: the reduction is read off the two standard
// errors the code actually produced.
func TestCUPEDReducesVarianceAtKnownCovariance(t *testing.T) {
	const (
		n   = 20000
		rho = 0.6
	)
	got, err := CUPEDAdjust(cupedRows(7, n, rho, 0.2), "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if !got.Applied {
		t.Fatalf("expected the adjustment to apply at n=%d, rho=%.1f; skipped: %s", n, rho, got.SkipReason)
	}
	if math.Abs(got.Rho-rho) > 0.02 {
		t.Errorf("measured rho %.4f, generated at %.2f", got.Rho, rho)
	}
	if !(got.AdjSE < got.RawSE) {
		t.Errorf("adjusted SE %.6f is not below the raw SE %.6f", got.AdjSE, got.RawSE)
	}
	// at rho=0.6 and n=20,000 the sampling error on the reduction is well under a point
	if got.VarianceReductionPct < 32 || got.VarianceReductionPct > 40 {
		t.Errorf("variance reduction %.1f%% at n=%d, rho=%.1f — outside the range this data can produce",
			got.VarianceReductionPct, n, rho)
	}
	if got.EquivalentExtraUnits < n/4 {
		t.Errorf("a ~36%% variance cut on %d units should be worth thousands of extra users, got %d",
			n, got.EquivalentExtraUnits)
	}
	if !strings.Contains(got.Note, "purchase") || !strings.Contains(got.Note, UnitBucketID) {
		t.Errorf("the note must name the covariate and the randomisation unit, got %q", got.Note)
	}
}

// The claim that matters is not "the interval got narrower" but "the narrower interval is still
// right". This measures the estimator's true spread over 300 independent samples and asserts the
// standard error the code reports on ONE sample predicts it. A reduction achieved by understating
// the standard error would pass every identity check and fail here.
func TestAdjustedSEMatchesEmpiricalSpread(t *testing.T) {
	const (
		reps = 300
		n    = 600
		rho  = 0.6
	)
	diffs := make([]float64, reps)
	seSum := 0.0
	for i := 0; i < reps; i++ {
		got, err := CUPEDAdjust(cupedRows(int64(1000+i), n, rho, 0.25), "control", "treatment", cupedOpts())
		if err != nil {
			t.Fatalf("rep %d: %v", i, err)
		}
		if !got.Applied {
			t.Fatalf("rep %d skipped: %s", i, got.SkipReason)
		}
		diffs[i] = got.AdjDiff
		seSum += got.AdjSE
	}
	reported := seSum / reps
	empirical := math.Sqrt(variance(diffs))
	if rel := math.Abs(reported-empirical) / empirical; rel > 0.12 {
		t.Errorf("reported adjusted SE %.5f against an empirical spread of %.5f over %d samples of n=%d (%.1f%% off)",
			reported, empirical, reps, n, 100*rel)
	}
}

// Variance reduction is only allowed to move the interval, never the answer. A biased adjustment
// is worse than no adjustment because it is a wrong number with a tighter interval around it.
func TestCUPEDIsUnbiased(t *testing.T) {
	const (
		reps   = 400
		n      = 400
		effect = 0.5
	)
	sum, sumSq := 0.0, 0.0
	for i := 0; i < reps; i++ {
		got, err := CUPEDAdjust(cupedRows(int64(500000+i), n, 0.6, effect), "control", "treatment", cupedOpts())
		if err != nil {
			t.Fatalf("rep %d: %v", i, err)
		}
		sum += got.AdjDiff
		sumSq += got.AdjDiff * got.AdjDiff
	}
	m := sum / reps
	sd := math.Sqrt(sumSq/reps - m*m)
	tol := 3 * sd / math.Sqrt(reps) // three standard errors of the mean over the replications
	if math.Abs(m-effect) > tol {
		t.Errorf("mean adjusted effect %.4f against a true effect of %.2f (tolerance %.4f over %d reps)",
			m, effect, tol, reps)
	}
}

// The refusal half. A new-user experiment has no pre-period to adjust against, and the honest
// output is the unchanged interval plus a sentence saying why — not a reduction fitted to noise.
func TestCUPEDSkippedOnNewUserExperiment(t *testing.T) {
	rows := cupedRows(3, 600, 0.6, 0.2)
	for i := range rows {
		rows[i].HasPre = false // nobody existed before they were exposed
		rows[i].X = 0
	}
	got, err := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if got.Applied {
		t.Fatal("adjusted on users with no pre-period at all")
	}
	if !strings.Contains(got.SkipReason, "new-user experiment") {
		t.Errorf("the reason must name the cause a reader can act on, got %q", got.SkipReason)
	}
	if got.AdjSE != got.RawSE || got.AdjDiff != got.RawDiff {
		t.Errorf("a skipped adjustment must leave the estimate alone: raw %.6f/%.6f, adjusted %.6f/%.6f",
			got.RawDiff, got.RawSE, got.AdjDiff, got.AdjSE)
	}
}

// The other refusal: users DO have history, it just does not predict anything. Least squares will
// always find some in-sample reduction here, so without the noise floor this case reports a win.
func TestCUPEDSkippedOnUncorrelatedCovariateAndNamesRho(t *testing.T) {
	got, err := CUPEDAdjust(cupedRows(21, 1000, 0.0, 0.2), "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if got.Applied {
		t.Fatalf("adjusted on an uncorrelated covariate (rho=%.4f) — the in-sample fit of noise was reported as a win", got.Rho)
	}
	if !strings.Contains(got.SkipReason, "correlation") || !strings.Contains(got.SkipReason, f2(got.Rho)) {
		t.Errorf("the reason must quote the correlation it refused on, got %q", got.SkipReason)
	}
	if got.VarianceReductionPct != 0 {
		t.Errorf("a skipped adjustment must report no reduction, got %.1f%%", got.VarianceReductionPct)
	}
}

// A covariate measured into the exposure window is not a covariate: it contains the treatment.
// Adjusting on it shrinks the estimate toward zero AND narrows the interval, so it is wrong in
// both directions at once and cannot be detected from the output. It has to be an error.
func TestCUPEDRejectsPostTreatmentCovariate(t *testing.T) {
	rows := cupedRows(5, 400, 0.6, 0.2)
	rows[17].CovTo = rows[17].ExposedAt.Add(time.Hour) // one row's lookback overruns its own exposure
	_, err := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
	if err == nil {
		t.Fatal("a covariate window overlapping exposure was accepted")
	}
	if !strings.Contains(err.Error(), "after that unit's first exposure") {
		t.Errorf("the error must name the problem, got %q", err)
	}
}

// Thin coverage is its own refusal. Below the floor the adjustment is fitted on a handful of
// users and applied to everyone, which narrows the interval using an estimate noisier than the
// thing it is correcting.
func TestCUPEDRefusesBelowThePreCoverageFloor(t *testing.T) {
	t.Run("too few units with a pre-period", func(t *testing.T) {
		rows := cupedRows(9, 600, 0.6, 0.2)
		for i := range rows {
			if i >= 60 { // 60 < minCUPEDUnitsWithPre
				rows[i].HasPre = false
				rows[i].X = 0
			}
		}
		got, _ := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
		if got.Applied {
			t.Fatal("adjusted with only 60 units carrying a pre-period")
		}
		if !strings.Contains(got.SkipReason, itoa(minCUPEDUnitsWithPre)) {
			t.Errorf("the reason must say how many were needed, got %q", got.SkipReason)
		}
	})
	t.Run("pre-period too rare a slice of the sample", func(t *testing.T) {
		rows := cupedRows(10, 4000, 0.6, 0.2)
		for i := range rows {
			if i >= 150 { // 150 of 4000 = 3.75%, under minCUPEDPreCoverage
				rows[i].HasPre = false
				rows[i].X = 0
			}
		}
		got, _ := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
		if got.Applied {
			t.Fatalf("adjusted when only %.1f%% of the sample has a pre-period", 100*150.0/4000)
		}
	})
}

// Every consumer of this result reads AdjDiff/AdjSE. If a skip left them zero, a caller that
// forgot to branch on Applied would publish an effect of zero with a standard error of zero —
// infinitely significant, from a refusal.
func TestSkippedAdjustmentStillCarriesTheRawEstimate(t *testing.T) {
	rows := cupedRows(13, 40, 0.6, 0.4) // under every coverage floor
	got, err := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if got.Applied {
		t.Fatal("expected a skip at n=40")
	}
	if got.AdjSE <= 0 || got.AdjDiff != got.RawDiff || got.AdjSE != got.RawSE {
		t.Fatalf("skipped result must carry the unadjusted estimate, got diff %.4f se %.4f (raw %.4f/%.4f)",
			got.AdjDiff, got.AdjSE, got.RawDiff, got.RawSE)
	}
	if got.HalfWidthAtZ(z95) <= 0 {
		t.Error("a skipped result must still produce a usable half-width")
	}
}

// Mixed arms must not be averaged in: a third arm's users are not part of this contrast.
func TestCUPEDIgnoresArmsOutsideTheContrast(t *testing.T) {
	rows := cupedRows(15, 900, 0.6, 0.3)
	for i := range rows {
		if i%3 == 0 {
			rows[i].Variant = "third"
			rows[i].Y += 50 // wildly different; must not touch the control/treatment contrast
		}
	}
	got, err := CUPEDAdjust(rows, "control", "treatment", cupedOpts())
	if err != nil {
		t.Fatalf("CUPEDAdjust: %v", err)
	}
	if got.Units >= len(rows) {
		t.Fatalf("the third arm was folded into the contrast: %d units from %d rows", got.Units, len(rows))
	}
	if math.Abs(got.AdjDiff) > 2 {
		t.Errorf("adjusted difference %.3f — the third arm's outcomes leaked into the comparison", got.AdjDiff)
	}
}

// --- building rows from events -------------------------------------------------------------------

func cev(name, id string, ts time.Time, props map[string]any) event.Event {
	if props == nil {
		props = map[string]any{}
	}
	return event.Event{ID: name + id + ts.Format(time.RFC3339Nano), Name: name, DistinctID: id, Timestamp: ts, Properties: props}
}

// exposureEv builds a $feature_flag_called event. Named exposureEv, not expo: srm.go already
// declares `type expo struct` in this package, and the collision made every call read to the
// compiler as a type conversion.
func exposureEv(id string, ts time.Time, flagKey, variant string, extra map[string]any) event.Event {
	p := map[string]any{PropFlag: flagKey, PropVariant: variant}
	for k, v := range extra {
		p[k] = v
	}
	return cev(ExposureEvent, id, ts, p)
}

// The lookback is per user and half-open, and it deliberately reaches back before the report
// window. All three of those are places an implementer guesses: a shared calendar window puts
// post-exposure behaviour into a late joiner's covariate, an inclusive upper bound counts the
// exposure instant itself as pre-exposure, and clipping the covariate to the report window leaves
// everyone with a zero and a method that quietly does nothing.
func TestBuildCUPEDUnitsUsesAPerUserHalfOpenLookback(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return base.AddDate(0, 0, d) }
	from, to := day(5), day(30)

	evs := []event.Event{
		// u1 exposed on day 10, so its covariate window is [day 3, day 10)
		cev("purchase", "u1", day(0), nil), // first sighting, before the window opens → HasPre
		cev("purchase", "u1", day(3), nil), // exactly on the lower bound → counted
		cev("purchase", "u1", day(4), nil), // before `from` → still counted, the pre-period precedes the report
		cev("purchase", "u1", day(6), nil),
		exposureEv("u1", day(10), "checkout_v2", "treatment", nil),
		cev("purchase", "u1", day(10), nil), // exactly at exposure → NOT pre-exposure
		cev("purchase", "u1", day(12), nil),
		// u2 exposed on day 20, so its window is [day 13, day 20) — a different one
		cev("purchase", "u2", day(2), nil),
		cev("purchase", "u2", day(6), nil), // inside u1's window, outside u2's own
		cev("purchase", "u2", day(14), nil),
		exposureEv("u2", day(20), "checkout_v2", "control", nil),
	}
	spec := CUPEDSpec{Flag: "checkout_v2", Goal: "purchase", From: from, To: to, LookbackDays: 7, CountOutcome: true}
	units, unit, err := BuildCUPEDUnits(evs, spec)
	if err != nil {
		t.Fatalf("BuildCUPEDUnits: %v", err)
	}
	if unit != UnitDistinctID {
		t.Errorf("no exposure carried a bucket id, so the unit is %s, got %s", UnitDistinctID, unit)
	}
	byID := map[string]CUPEDUnit{}
	for _, u := range units {
		byID[u.Unit] = u
	}
	if got := byID["u1"].X; got != 3 {
		t.Errorf("u1 covariate = %v, want 3 (days 3, 4 and 6; day 0 predates the window and day 10 is the exposure instant)", got)
	}
	if got := byID["u1"].Y; got != 2 {
		t.Errorf("u1 outcome = %v, want 2 (day 10 at the exposure instant counts, and day 12)", got)
	}
	if got := byID["u2"].X; got != 1 {
		t.Errorf("u2 covariate = %v, want 1 — its window is [day 13, day 20), not u1's", got)
	}
	if !byID["u1"].HasPre || !byID["u2"].HasPre {
		t.Error("both users were seen before their own lookback opened, so both have a pre-period")
	}
	if got := byID["u1"].CovTo; !got.Equal(day(10)) {
		t.Errorf("covariate window must end at the unit's own exposure, got %s", got)
	}
}

// A user first seen inside their own lookback has X=0 because nobody was watching, not because
// they did nothing. Treating those as the same value makes the covariate a proxy for signup date.
func TestUnobservedPrePeriodIsNotAZeroCovariate(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return base.AddDate(0, 0, d) }
	evs := []event.Event{
		exposureEv("newbie", day(10), "f", "control", nil), // first ever sighting is the exposure itself
		cev("purchase", "newbie", day(11), nil),
		cev("purchase", "veteran", day(1), nil),
		exposureEv("veteran", day(10), "f", "treatment", nil),
	}
	units, _, err := BuildCUPEDUnits(evs, CUPEDSpec{Flag: "f", Goal: "purchase", From: day(0), To: day(30), LookbackDays: 7})
	if err != nil {
		t.Fatalf("BuildCUPEDUnits: %v", err)
	}
	for _, u := range units {
		switch u.Unit {
		case "newbie":
			if u.HasPre {
				t.Error("a user whose first event IS their exposure has no observable pre-period")
			}
		case "veteran":
			if !u.HasPre {
				t.Error("a user seen 9 days before a 7-day lookback has a pre-period")
			}
		}
	}
}

// Binding decision 2. One dataset, one randomisation unit. Half the exposures carrying a device
// key and half not is two experiments stacked on each other, and picking either silently answers
// a question the operator did not ask.
func TestBuildCUPEDUnitsRefusesMixedRandomisationUnits(t *testing.T) {
	ts := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	evs := []event.Event{
		exposureEv("a", ts, "f", "control", map[string]any{PropBucketID: "dev1"}),
		exposureEv("b", ts, "f", "treatment", nil),
	}
	_, _, err := BuildCUPEDUnits(evs, CUPEDSpec{Flag: "f", Goal: "purchase", From: ts.AddDate(0, 0, -1), To: ts.AddDate(0, 0, 1)})
	if err == nil {
		t.Fatal("a dataset randomised on two different units was accepted")
	}
	for _, want := range []string{"1 carry a bucket_id", "two different experiments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must explain the estimand problem and the counts; missing %q in %q", want, err)
		}
	}
}

// bucket_id is device-scoped and survives identify(), so one browser that logs in mid-experiment
// is ONE unit whose behaviour is not split in half at the login.
func TestBucketIDCollapsesTheLoginIntoOneUnit(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return base.AddDate(0, 0, d) }
	evs := []event.Event{
		cev("purchase", "anon_9", day(1), nil),
		exposureEv("anon_9", day(10), "f", "treatment", map[string]any{PropBucketID: "dev1"}),
		// same browser, after login: new distinct_id, same bucket
		exposureEv("user_42", day(11), "f", "treatment", map[string]any{PropBucketID: "dev1"}),
		cev("purchase", "user_42", day(12), nil),
	}
	units, unit, err := BuildCUPEDUnits(evs, CUPEDSpec{Flag: "f", Goal: "purchase", From: day(0), To: day(30), CountOutcome: true})
	if err != nil {
		t.Fatalf("BuildCUPEDUnits: %v", err)
	}
	if unit != UnitBucketID {
		t.Errorf("unit = %s, want %s", unit, UnitBucketID)
	}
	if len(units) != 1 {
		t.Fatalf("one browser must be one unit, got %d", len(units))
	}
	if units[0].Unit != "dev1" || units[0].Y != 1 {
		t.Errorf("post-login conversion lost: %+v", units[0])
	}
	if !units[0].ExposedAt.Equal(day(10)) {
		t.Errorf("first exposure must be the earliest across the device, got %s", units[0].ExposedAt)
	}
}
