package deploys

import (
	"testing"
	"time"
)

func mustAt(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// flat builds n days ending on `end` (YYYY-MM-DD), each with count v.
func flat(end string, n, v int) []Point {
	e, _ := time.Parse(day, end)
	out := make([]Point, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, Point{Date: e.AddDate(0, 0, -i), Count: v})
	}
	return out
}

func dep(sha, at string) Deploy { return Deploy{SHA: sha, At: mustAt(at)} }

func TestRecordUpsertsBySHA(t *testing.T) {
	s, _ := Open("")
	if _, err := s.Record(Deploy{SHA: "abc123", Message: "first"}); err != nil {
		t.Fatal(err)
	}
	first := s.List()[0]
	if _, err := s.Record(Deploy{SHA: "abc123", Message: "updated"}); err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 1 {
		t.Fatalf("same sha must upsert, got %d rows", len(got))
	}
	if got[0].ID != first.ID || got[0].Created != first.Created {
		t.Errorf("upsert must keep id + created; got id=%s", got[0].ID)
	}
	if got[0].Message != "updated" {
		t.Errorf("upsert must refresh fields; message=%q", got[0].Message)
	}
	// a marker without a sha is always a distinct row
	_, _ = s.Record(Deploy{Message: "manual a"})
	_, _ = s.Record(Deploy{Message: "manual b"})
	if len(s.List()) != 3 {
		t.Errorf("sha-less markers must not collapse; got %d", len(s.List()))
	}
}

func single(series []Point, d Deploy, window int) Impact {
	return ComputeImpact(series, []Deploy{d}, window, 0.25)[0]
}

func TestImpactRegression(t *testing.T) {
	series := append(flat("2026-07-12", 3, 100), flat("2026-07-15", 3, 60)...)
	imp := single(series, dep("abc1234", "2026-07-13T09:00:00Z"), 3)
	if imp.Before == nil || *imp.Before != 100 || imp.After == nil || *imp.After != 60 {
		t.Fatalf("before/after = %v/%v", imp.Before, imp.After)
	}
	if imp.DeltaPct == nil || *imp.DeltaPct > -0.39 || *imp.DeltaPct < -0.41 {
		t.Fatalf("deltaPct = %v (want ~-0.4)", imp.DeltaPct)
	}
	if imp.Direction != "regression" || !imp.Significant {
		t.Errorf("want significant regression, got %s significant=%v", imp.Direction, imp.Significant)
	}
	if imp.ShortSHA != "abc1234" {
		t.Errorf("shortSHA = %q", imp.ShortSHA)
	}
}

func TestImpactFlatWiggleNotSignificant(t *testing.T) {
	series := append(flat("2026-07-12", 3, 100), flat("2026-07-15", 3, 95)...)
	imp := single(series, dep("abc1234", "2026-07-13T09:00:00Z"), 3)
	if imp.Direction != "flat" || imp.Significant {
		t.Errorf("small wiggle must be flat+insignificant, got %s significant=%v", imp.Direction, imp.Significant)
	}
}

func TestImpactThinWindowNeverSignificant(t *testing.T) {
	series := append(flat("2026-07-12", 3, 100), Point{Date: mustAt("2026-07-13T00:00:00Z"), Count: 40})
	imp := single(series, dep("abc1234", "2026-07-13T09:00:00Z"), 3)
	if imp.Significant {
		t.Errorf("only 1 day after the deploy must never be significant")
	}
}

func TestImpactNoDivideByZero(t *testing.T) {
	series := append(flat("2026-07-12", 3, 0), flat("2026-07-15", 3, 80)...)
	imp := single(series, dep("abc1234", "2026-07-13T09:00:00Z"), 3)
	if imp.DeltaPct != nil {
		t.Errorf("0 -> 80 has no finite %%, DeltaPct must be nil, got %v", *imp.DeltaPct)
	}
	if imp.Direction != "improvement" {
		t.Errorf("0 -> 80 is an improvement, got %s", imp.Direction)
	}
}

func TestHeadlinePrefersRegression(t *testing.T) {
	series := append(append(flat("2026-07-09", 3, 100), flat("2026-07-12", 3, 40)...), flat("2026-07-15", 3, 90)...)
	impacts := ComputeImpact(series, []Deploy{
		dep("aaaa111", "2026-07-10T09:00:00Z"), // drop
		dep("bbbb222", "2026-07-13T09:00:00Z"), // recovery
	}, 3, 0.25)
	h := Headline(impacts)
	if h == nil || h.SHA != "aaaa111" {
		t.Errorf("headline should be the regression aaaa111, got %v", h)
	}
	// newest-first ordering
	if impacts[0].SHA != "bbbb222" {
		t.Errorf("want newest-first, got %s", impacts[0].SHA)
	}
}
