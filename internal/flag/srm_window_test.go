package flag

import (
	"strings"
	"testing"
)

// The result had no idea what window it was about. A caller asking for all time and being handed
// 30 days (which is exactly what the MCP experiment_health handler does, coercing days:0 to 30)
// got back "traffic split looks correct" with nothing anywhere in the payload naming the stretch
// of history that sentence covers — so a mismatch that happened on day 40 of a long experiment
// reads as a clean bill of health, and the reader has no way to notice the window is wrong.
func TestSRMSaysWhichWindowItChecked(t *testing.T) {
	f := fiftyFifty()
	evs := toEvents(buildExposures("exp", map[string]int{"control": 5000, "test": 4950}, nil))

	all := CheckSRM(evs, f, 0)
	if all.WindowDays != 0 {
		t.Errorf("window_days = %d for an all-history check, want 0", all.WindowDays)
	}
	if !strings.Contains(all.Verdict, "whole history") {
		t.Errorf("an all-time check does not say so: %q", all.Verdict)
	}

	windowed := CheckSRM(evs, f, 30)
	if windowed.WindowDays != 30 {
		t.Errorf("window_days = %d, want 30 — the result does not carry the window it used", windowed.WindowDays)
	}
	if !strings.Contains(windowed.Verdict, "last 30 days") {
		t.Errorf("a 30-day check reads as if it covered everything: %q", windowed.Verdict)
	}
	// The reassuring verdict is the one that must never be ambiguous about its window.
	if windowed.Detected || all.Detected {
		t.Fatalf("a 5000/4950 split should not fire; the point of this test is the clean verdict")
	}
}
