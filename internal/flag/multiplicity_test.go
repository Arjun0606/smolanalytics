package flag

import (
	"math"
	"strings"
	"testing"
)

// The worked example from Benjamini & Hochberg (1995): 15 p-values, q=0.05, four rejections. If
// this table ever moves, the correction is not BH any more whatever the field names say.
var bh1995 = []float64{
	0.0001, 0.0004, 0.0019, 0.0095, 0.0201, 0.0278, 0.0298, 0.0344,
	0.0459, 0.3240, 0.4262, 0.5719, 0.6528, 0.7590, 1.0000,
}

func published(ps []float64) []Hypothesis {
	hs := make([]Hypothesis, len(ps))
	for i, p := range ps {
		// one arm, many metrics — the family is (variants × metrics) either way
		hs[i] = Hypothesis{Variant: "treatment", Metric: "m" + itoa(i+1), P: p}
	}
	return hs
}

func TestBHAgainstPublishedTable(t *testing.T) {
	res, err := BenjaminiHochberg("checkout_v2", published(bh1995), 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejections != 4 {
		t.Fatalf("rejections = %d, want 4 (the published result)", res.Rejections)
	}
	if math.Abs(res.Threshold-0.0095) > 1e-12 {
		t.Fatalf("threshold p_(k) = %v, want 0.0095", res.Threshold)
	}
	want := map[string]float64{ // adjusted p, min-over-j form
		"m1": 0.0015, "m2": 0.0030, "m3": 0.0095, "m4": 0.035625,
		"m5": 0.0603, "m6": 0.0638571428571, "m7": 0.0638571428571,
		"m15": 1.0,
	}
	for _, tst := range res.Tests {
		if w, ok := want[tst.Metric]; ok && math.Abs(tst.PAdjusted-w) > 1e-9 {
			t.Fatalf("%s adjusted = %.12f, want %.12f", tst.Metric, tst.PAdjusted, w)
		}
	}
	// m1's raw p × m is 0.0015, but the naive (m/i)·p for m2 is 0.0030 — the min-over-j is what
	// keeps the column monotone, and m6 taking m7's value is the case that proves it is applied.
	if res.Method != BHMethod {
		t.Fatalf("method = %q", res.Method)
	}
}

// Step-up means the LARGEST i that clears its threshold, not the first that fails. Reading it as
// "stop at the first failure" under-rejects whenever a large p sits between two small ones, and
// the mistake is invisible on a monotone fixture.
func TestBHIsStepUpNotFirstFailure(t *testing.T) {
	res, err := BenjaminiHochberg("exp", published([]float64{0.01, 0.04, 0.045}), 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejections != 3 {
		t.Fatalf("rejections = %d, want 3: p_(2)=0.04 fails its own threshold but p_(3)=0.045 clears "+
			"0.05, so all three are rejected", res.Rejections)
	}
}

func TestBHAdjustedPIsMonotone(t *testing.T) {
	res, err := BenjaminiHochberg("exp", published(bh1995), 0.05)
	if err != nil {
		t.Fatal(err)
	}
	prev := -1.0
	for _, tst := range res.Tests {
		if tst.PAdjusted < prev-1e-15 {
			t.Fatalf("adjusted p decreased at rank %d (%v after %v): a reader sorting by the adjusted "+
				"column would see their arms silently reordered", tst.Rank, tst.PAdjusted, prev)
		}
		if tst.PAdjusted > 1 {
			t.Fatalf("adjusted p = %v, which is not a probability", tst.PAdjusted)
		}
		if tst.PAdjusted < tst.P-1e-15 {
			t.Fatalf("adjusted p %v is below the raw p %v", tst.PAdjusted, tst.P)
		}
		prev = tst.PAdjusted
	}
}

// The two halves of BH must agree: a cell is rejected by the step-up if and only if its adjusted
// p is at or below q. If they ever disagree, the table shows a "significant" row with an adjusted
// p above the threshold printed next to it, and every reader believes the column they can see.
func TestRejectionAgreesWithAdjustedP(t *testing.T) {
	sets := [][]float64{
		bh1995,
		{0.01, 0.04, 0.045},
		{0.2, 0.2, 0.2, 0.2},
		{0.0001},
		{0.9, 0.001, 0.5, 0.02, 0.049},
		{0.05, 0.05, 0.05},
	}
	for _, q := range []float64{0.01, 0.05, 0.1, 0.2} {
		for _, ps := range sets {
			res, err := BenjaminiHochberg("exp", published(ps), q)
			if err != nil {
				t.Fatal(err)
			}
			for _, tst := range res.Tests {
				if want := tst.PAdjusted <= q+1e-12; tst.Rejected != want {
					t.Fatalf("q=%v %s: rejected=%v but adjusted p=%v", q, tst.Metric, tst.Rejected, tst.PAdjusted)
				}
			}
		}
	}
}

// Guardrails are excluded from the family on purpose — a guardrail is a test you WANT sensitive,
// and correcting it makes it less likely to catch the harm it exists for. Passing one in is a
// caller mistake, not something to silently drop.
func TestGuardrailsExcludedFromFamily(t *testing.T) {
	mixed := []Hypothesis{
		{Variant: "treatment", Metric: "checkout", P: 0.01},
		{Variant: "treatment_b", Metric: "checkout", P: 0.03},
		{Variant: "treatment", Metric: "$exception", Kind: HypothesisGuardrail, P: 0.02},
		{Variant: "treatment", Metric: "time_on_page", Kind: HypothesisSecondary, P: 0.04},
	}
	if _, err := BenjaminiHochberg("exp", mixed, 0.05); err == nil {
		t.Fatal("a guardrail was accepted into the correction family")
	} else if !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("error does not say why guardrails are excluded: %v", err)
	}

	fam := PrimaryHypotheses(mixed)
	if len(fam) != 2 {
		t.Fatalf("family size = %d, want 2 primary cells", len(fam))
	}
	res, err := BenjaminiHochberg("exp", fam, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if res.Family.Size != 2 {
		t.Fatalf("family size = %d, want 2", res.Family.Size)
	}
	if !strings.Contains(res.Family.Excluded, "guardrail") {
		t.Fatalf("the family does not say what it excluded: %+v", res.Family)
	}
}

// Binding decision 10: the family is (variants × metrics) at ONE look and must name itself. A
// duplicate cell is two looks merged, which would charge for peeking twice — the confidence
// sequence already covers that dimension.
func TestFamilyIsNamedAndDoesNotAccumulateAcrossLooks(t *testing.T) {
	hs := []Hypothesis{
		{Variant: "b", Metric: "checkout", P: 0.01},
		{Variant: "a", Metric: "checkout", P: 0.02},
		{Variant: "a", Metric: "signup", P: 0.03},
		{Variant: "b", Metric: "signup", P: 0.04},
	}
	res, err := BenjaminiHochberg("checkout_v2", hs, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	f := res.Family
	switch {
	case f.Size != 4:
		t.Fatalf("family size = %d, want 4", f.Size)
	case f.Experiment != "checkout_v2":
		t.Fatalf("family does not name its experiment: %q", f.Experiment)
	case len(f.Variants) != 2 || f.Variants[0] != "a":
		t.Fatalf("family variants not listed deterministically: %v", f.Variants)
	case len(f.Metrics) != 2 || f.Metrics[0] != "checkout":
		t.Fatalf("family metrics not listed deterministically: %v", f.Metrics)
	case f.AccumulatesAcrossLooks:
		t.Fatal("the family claims to accumulate across looks; the confidence sequence already pays for peeking")
	}
	for _, want := range []string{"2 variants", "2 metrics", "checkout_v2", "one look", "4 tests"} {
		if !strings.Contains(f.Description, want) {
			t.Fatalf("family description %q does not contain %q", f.Description, want)
		}
	}
	if !strings.Contains(res.Note, "does not grow when you look again") {
		t.Fatalf("note does not state the no-accumulation rule: %q", res.Note)
	}

	dup := append(append([]Hypothesis{}, hs...), Hypothesis{Variant: "a", Metric: "checkout", P: 0.005})
	if _, err := BenjaminiHochberg("checkout_v2", dup, 0.05); err == nil {
		t.Fatal("the same cell was accepted twice, which merges two looks into one family")
	} else if !strings.Contains(strings.ToLower(err.Error()), "one look") {
		// Case-folded: the message emphasises "ONE look" on purpose, and the assertion is about the
		// rule being explained, not about its capitalisation.
		t.Fatalf("error does not explain the one-look rule: %v", err)
	}
}

func TestBHRefusesRatherThanGuessing(t *testing.T) {
	ok := []Hypothesis{{Variant: "t", Metric: "checkout", P: 0.01}}
	cases := []struct {
		name string
		exp  string
		hs   []Hypothesis
		q    float64
	}{
		{"unnamed experiment", "", ok, 0.05},
		{"q of zero", "e", ok, 0},
		{"q of one", "e", ok, 1},
		{"p above one", "e", []Hypothesis{{Variant: "t", Metric: "m", P: 1.5}}, 0.05},
		{"negative p", "e", []Hypothesis{{Variant: "t", Metric: "m", P: -0.1}}, 0.05},
		{"NaN p", "e", []Hypothesis{{Variant: "t", Metric: "m", P: math.NaN()}}, 0.05},
		{"no variant", "e", []Hypothesis{{Metric: "m", P: 0.1}}, 0.05},
		{"no metric", "e", []Hypothesis{{Variant: "t", P: 0.1}}, 0.05},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := BenjaminiHochberg(c.exp, c.hs, c.q); err == nil {
				t.Fatal("accepted, want an error")
			}
		})
	}
}

// A two-arm, one-metric experiment has a family of one and BH must be the identity there. If it
// were not, turning the correction on would move every simple experiment's number for no reason
// and nobody would leave it on.
func TestSingleHypothesisIsUncorrected(t *testing.T) {
	res, err := BenjaminiHochberg("exp", []Hypothesis{{Variant: "t", Metric: "checkout", P: 0.031}}, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tests[0].PAdjusted != 0.031 || !res.Tests[0].Rejected {
		t.Fatalf("m=1 must be the identity: %+v", res.Tests[0])
	}
	if res.Family.Size != 1 || !strings.Contains(res.Family.Description, "1 test") {
		t.Fatalf("family of one is misdescribed: %q", res.Family.Description)
	}

	empty, err := BenjaminiHochberg("exp", nil, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Rejections != 0 || len(empty.Tests) != 0 || empty.Note == "" {
		t.Fatalf("an empty family must be a stated no-op: %+v", empty)
	}
}

// Ties must order the same way on every run. Two arms with identical p-values reordering between
// runs would change the rendered table with no change in the data — the precise failure receipts
// exist to make impossible.
func TestTiesAreOrderedDeterministically(t *testing.T) {
	hs := []Hypothesis{
		{Variant: "z", Metric: "b", P: 0.02},
		{Variant: "a", Metric: "b", P: 0.02},
		{Variant: "a", Metric: "a", P: 0.02},
	}
	first, err := BenjaminiHochberg("exp", hs, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := []Hypothesis{hs[2], hs[0], hs[1]}
	second, err := BenjaminiHochberg("exp", shuffled, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Tests {
		if first.Tests[i] != second.Tests[i] {
			t.Fatalf("rank %d differs by input order: %+v vs %+v", i+1, first.Tests[i], second.Tests[i])
		}
	}
	if first.Tests[0].Variant != "a" || first.Tests[0].Metric != "a" {
		t.Fatalf("tie-break is not (p, variant, metric): %+v", first.Tests[0])
	}
}

// The back-solved interval is a presentation convention and the payload says so. Its one real
// property: at adjusted p exactly q the bound lands on zero, so an adjusted-significant row never
// shows an interval spanning zero and vice versa.
func TestAdjustedIntervalAgreesWithTheDecision(t *testing.T) {
	ci, note, err := AdjustedDeltaInterval(18.2, 0.05, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(ci.Lo) > 0.05 {
		t.Fatalf("at p_adj = q the bound should sit on zero, got %v", ci.Lo)
	}
	if !strings.Contains(note, "presentation convention") {
		t.Fatalf("the ad-hoc nature is not stated in the payload: %q", note)
	}

	sig, _, err := AdjustedDeltaInterval(18.2, 0.01, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Lo <= 0 {
		t.Fatalf("an adjusted-significant result must not show an interval spanning zero: %+v", sig)
	}
	ns, _, err := AdjustedDeltaInterval(18.2, 0.2, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if ns.Lo >= 0 {
		t.Fatalf("an adjusted-non-significant result must show an interval spanning zero: %+v", ns)
	}
	neg, _, err := AdjustedDeltaInterval(-18.2, 0.01, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if neg.Hi >= 0 {
		t.Fatalf("a negative significant effect must stay entirely below zero: %+v", neg)
	}

	for _, c := range []struct {
		name           string
		delta, pAdj, q float64
	}{
		{"zero point estimate", 0, 0.01, 0.05},
		{"adjusted p of one", 18.2, 1, 0.05},
		{"adjusted p of zero", 18.2, 0, 0.05},
		{"q out of range", 18.2, 0.01, 0},
	} {
		if _, _, err := AdjustedDeltaInterval(c.delta, c.pAdj, c.q); err == nil {
			t.Fatalf("%s: accepted, want an error rather than something that looks like an interval", c.name)
		}
	}
}
