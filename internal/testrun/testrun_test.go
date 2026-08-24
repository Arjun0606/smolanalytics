package testrun

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The three ways this store could lie about somebody's product.

func TestAnEmptyPathIsInMemoryNotAnError(t *testing.T) {
	// The acted store learned this the hard way: it tried to persist to "" and crashed the demo on
	// the first write with `rename .tmp: no such file`. The demo, the tests and every read-only
	// instance pass "".
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\") = %v", err)
	}
	if err := s.Append(Run{ID: "r1", Test: "checkout", Status: StatusPassed}); err != nil {
		t.Fatalf("Append to an in-memory store = %v", err)
	}
	if len(s.Recent(10)) != 1 {
		t.Fatal("the run was not kept")
	}
}

func TestAPassingRunDropsItsStepsAndAFailureKeepsThem(t *testing.T) {
	// A green run's timeline is forty lines nobody reads; a failure's timeline IS the bug report.
	s, _ := Open("")
	steps := []Step{{N: 1, Do: `click button "Pay"`, OK: true}}
	_ = s.Append(Run{ID: "a", Status: StatusPassed, Steps: steps, StartedAt: time.Now()})
	_ = s.Append(Run{ID: "b", Status: StatusFailed, Steps: steps, StartedAt: time.Now().Add(time.Second)})

	got := s.Recent(10)
	byID := map[string]Run{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if len(byID["a"].Steps) != 0 {
		t.Error("a passing run kept its steps")
	}
	if len(byID["b"].Steps) != 1 {
		t.Error("a failing run lost its steps — that timeline is the bug report")
	}
}

func TestRecentIsNewestFirst(t *testing.T) {
	s, _ := Open("")
	base := time.Now()
	for i, id := range []string{"old", "mid", "new"} {
		_ = s.Append(Run{ID: id, StartedAt: base.Add(time.Duration(i) * time.Minute)})
	}
	got := s.Recent(2)
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "mid" {
		t.Fatalf("Recent(2) = %v, want [new mid]", ids(got))
	}
}

func TestTheStoreIsBounded(t *testing.T) {
	// Runs arrive on every push to every branch. Unbounded, this is the largest file in a tenant's
	// data directory within a month.
	s, _ := Open("")
	s.cap = 5
	for i := 0; i < 20; i++ {
		_ = s.Append(Run{ID: itoa(i), StartedAt: time.Now().Add(time.Duration(i) * time.Second)})
	}
	if n := len(s.Recent(0)); n != 5 {
		t.Fatalf("kept %d runs, want the cap of 5", n)
	}
}

func TestItSurvivesARestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.json")
	s, _ := Open(p)
	_ = s.Append(Run{ID: "r1", Test: "checkout", Status: StatusFailed, Reason: "no order number appeared", StartedAt: time.Now()})

	again, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Recent(10)
	if len(got) != 1 || got[0].Reason != "no order number appeared" {
		t.Fatalf("reload lost the run: %+v", got)
	}
}

func TestACorruptFileStartsEmptyRatherThanRefusingToBoot(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.json")
	if err := writeFile(p, "{not json"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatalf("a corrupt run log refused to open: %v", err)
	}
	if len(s.Recent(0)) != 0 {
		t.Error("garbage was parsed as runs")
	}
}

// ---- the sentences, which are the part that can lie ----

func TestStaleIsNeverCountedAsPassingOrBroken(t *testing.T) {
	// The distinction the whole product turns on. A replay cannot tell "renamed" from "gone", and
	// reporting a rename as a failure pages someone at 2am over a copy change.
	sum := Summarize([]Run{
		{Status: StatusPassed}, {Status: StatusPassed}, {Status: StatusStale},
	})
	if sum.Failed != 0 {
		t.Error("a stale recording was counted as a failure")
	}
	if sum.Passed != 2 {
		t.Errorf("passed = %d, want 2", sum.Passed)
	}
	h := sum.Headline()
	// Matched on a CLAIMED COUNT, not the word: "Nothing failed." is the correct sentence and
	// contains "failed". What must never appear is "1 test failed" over a run that was stale.
	if claimsFailures.MatchString(h) {
		t.Errorf("headline counts a stale recording as a failure: %q", h)
	}
	if !strings.Contains(h, "rename") {
		t.Errorf("headline does not explain what stale means: %q", h)
	}
}

func TestARunnerFailureIsNeverReportedAsTheAppBreaking(t *testing.T) {
	// No browser, no API key, no network. Telling a customer their checkout is broken because our
	// runner fell over is the fastest way to lose them.
	h := Summarize([]Run{{Status: StatusPassed}, {Status: StatusErrored}}).Headline()
	if claimsFailures.MatchString(h) {
		t.Errorf("a runner error is counted as an app failure: %q", h)
	}
	if !strings.Contains(h, "our side") {
		t.Errorf("headline does not say whose fault it is: %q", h)
	}
}

func TestAnEmptySuiteSaysWhatToDoRatherThanZero(t *testing.T) {
	h := Summarize(nil).Headline()
	if strings.Contains(h, "0") {
		t.Errorf("an empty suite renders as a zero: %q", h)
	}
	if !strings.Contains(h, "sentence") {
		t.Errorf("headline does not say how to start: %q", h)
	}
}

func TestTheCostNoteShowsWhatTheSuiteCost(t *testing.T) {
	// The economic argument, visible in the data instead of asserted in the copy.
	runs := []Run{
		{Status: StatusPassed, Mode: ModeReplay}, {Status: StatusPassed, Mode: ModeReplay},
		{Status: StatusPassed, Mode: ModeReplay}, {Status: StatusFailed, Mode: ModeAgent},
	}
	sum := Summarize(runs)
	if sum.Replayed != 3 || sum.SavedCalls != 3 {
		t.Fatalf("replayed = %d, want 3", sum.Replayed)
	}
	note := sum.CostNote()
	if !strings.Contains(note, "3 runs replayed with no model") {
		t.Errorf("cost note = %q", note)
	}
	if Summarize(nil).CostNote() != "" {
		t.Error("an empty suite still claims a cost saving")
	}
	all := Summarize([]Run{{Mode: ModeReplay}}).CostNote()
	if !strings.Contains(all, "no model was called at all") {
		t.Errorf("all-replay note = %q", all)
	}
}

func ids(rs []Run) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o600) }

// claimsFailures matches a headline asserting that N of the customer's tests failed — the claim
// neither a stale recording nor a runner error is allowed to produce.
var claimsFailures = regexp.MustCompile(`(?i)\b\d+ tests? failed|\bthe one test failed`)

func TestDurationsAreReadable(t *testing.T) {
	// "47210ms" is the number that proves replaying is worth it, printed in the unit nobody counts
	// in. The comparison a customer is meant to make — 0.6s against 47s — only lands if both are
	// legible at a glance.
	for _, c := range []struct {
		ms   int
		want string
	}{
		{0, ""}, {612, "612ms"}, {9999, "9999ms"}, {10412, "10.4s"}, {47210, "47.2s"}, {125000, "2m 5s"},
	} {
		if got := (Run{DurationMs: c.ms}).Took(); got != c.want {
			t.Errorf("Took(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// ---- the suite: one row per test, worst first ----

func TestTheSuiteIsDerivedNotStored(t *testing.T) {
	// Held as its own list it would drift: a test renamed or deleted from the repo stays green
	// forever because nothing ran it. Derived, the suite can only contain tests that actually ran.
	now := time.Now().UTC()
	runs := []Run{
		{Test: "checkout", Status: StatusPassed, Mode: ModeReplay, StartedAt: now.Add(-3 * time.Hour)},
		{Test: "checkout", Status: StatusFailed, Mode: ModeAgent, StartedAt: now.Add(-time.Hour), Reason: "no order number"},
		{Test: "signup", Status: StatusPassed, Mode: ModeReplay, StartedAt: now.Add(-2 * time.Hour)},
	}
	s := Suite(runs)
	if len(s) != 2 {
		t.Fatalf("suite has %d rows, want one per test", len(s))
	}
	// worst first: checkout is failing, so it leads regardless of name or recency
	if s[0].Test != "checkout" || s[0].Status != StatusFailed {
		t.Fatalf("suite[0] = %+v, want the failing test first", s[0])
	}
	if s[0].Reason != "no order number" {
		t.Error("the failing row does not carry its own explanation")
	}
	// it HAS passed before, so a recording exists — this is a regression, not a never-worked
	if !s[0].Recorded || s[0].NeverPassed() {
		t.Error("checkout passed three hours ago; it should read as recorded and previously green")
	}
	if s[0].Runs != 2 {
		t.Errorf("runs = %d, want 2", s[0].Runs)
	}
}

func TestNeverPassedIsNotTheSameAsRegressed(t *testing.T) {
	// A test that never passed may describe something the product has never done, which is as
	// likely to be a wrong test as a broken feature. Telling someone their checkout is broken when
	// the test was wrong burns the trust this whole product runs on.
	now := time.Now().UTC()
	s := Suite([]Run{{Test: "refunds", Status: StatusFailed, Mode: ModeAgent, StartedAt: now}})
	if !s[0].NeverPassed() {
		t.Error("a test that has only ever failed does not read as never-passed")
	}
	if s[0].Recorded {
		t.Error("a test that never passed cannot have a recording to replay")
	}
}

func TestTheSuiteOrdersByAttentionNotAlphabet(t *testing.T) {
	// A failing test at the bottom of an alphabetical list is a failing test nobody sees.
	now := time.Now().UTC()
	s := Suite([]Run{
		{Test: "aaa passing", Status: StatusPassed, StartedAt: now},
		{Test: "zzz failing", Status: StatusFailed, StartedAt: now.Add(-time.Hour)},
		{Test: "mmm stale", Status: StatusStale, StartedAt: now},
		{Test: "nnn errored", Status: StatusErrored, StartedAt: now},
	})
	got := []string{s[0].Test, s[1].Test, s[2].Test, s[3].Test}
	want := []string{"zzz failing", "mmm stale", "nnn errored", "aaa passing"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("suite order = %v, want %v", got, want)
		}
	}
}
