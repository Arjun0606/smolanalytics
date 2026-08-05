package insight

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

// The verdict card printed two rates for ONE transition, one line apart:
//
//	[warn/segment]        "…2.0× worse than average — only 30% of mobile visitors continue,
//	                       against 60% for everyone (30 of 100)."
//	[warn/funnel_dropoff] "…only 30% go on to check out"
//
// because segmentBlame measured both the segment and its "for everyone" base with a
// standalone two-step funnel over every event, while the drop-off came off the multi-step
// funnel the page renders. Anyone who did `from` without entering the funnel at step 0 was
// counted in the base and in nothing else, so "60% for everyone" was a number that existed
// nowhere else on the page — and the 0.7 gate fired against that inflated base, inventing an
// underperformer where the segment WAS the funnel average.
//
// These pin the contract: the blame's base is the same funnel, at the same transition, as the
// drop-off finding sitting next to it.

// blameFixture: `steps` is signup → activate → checkout. `outside` users do activate WITHOUT
// signing up (so they never enter the funnel) and convert at a completely different rate —
// they are exactly the population the old two-step base swallowed.
func blameFixture(mobile, mobileConv, desktop, desktopConv, outside, outsideConv int) []event.Event {
	base := time.Now().UTC().Add(-72 * time.Hour)
	var evs []event.Event
	add := func(u, name string, off time.Duration, device string) {
		evs = append(evs, event.Event{ID: u + name, DistinctID: u, Name: name, Timestamp: base.Add(off),
			Properties: map[string]any{"device": device}})
	}
	inFunnel := func(prefix, device string, n, conv int) {
		for i := 0; i < n; i++ {
			u := fmt.Sprintf("%s%d", prefix, i)
			add(u, "signup", time.Duration(i)*time.Minute, device)
			add(u, "activate", time.Duration(i)*time.Minute+time.Hour, device)
			if i < conv {
				add(u, "checkout", time.Duration(i)*time.Minute+2*time.Hour, device)
			}
		}
	}
	inFunnel("m", "mobile", mobile, mobileConv)
	inFunnel("d", "desktop", desktop, desktopConv)
	for i := 0; i < outside; i++ {
		u := fmt.Sprintf("o%d", i)
		add(u, "activate", time.Duration(i)*time.Minute+time.Hour, "desktop")
		if i < outsideConv {
			add(u, "checkout", time.Duration(i)*time.Minute+2*time.Hour, "desktop")
		}
	}
	return evs
}

func funnelSteps() []funnel.Step {
	return []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
}

func findKind(fs []Finding, kind string) (Finding, bool) {
	for _, f := range fs {
		if f.Kind == kind {
			return f, true
		}
	}
	return Finding{}, false
}

// A segment that converts exactly at the funnel average must not be blamed, no matter how
// many people did `from` outside the funnel. This is the reported fixture verbatim.
func TestSegmentBlameDoesNotFabricateAgainstAnOutsideBase(t *testing.T) {
	// every funnel user is mobile and converts at 30%; 100 non-funnel users activate and
	// convert at 90%, which used to drag the "everyone" base up to 60%.
	fs := GenerateForFunnel(blameFixture(100, 30, 0, 0, 100, 90), funnelSteps())

	drop, ok := findKind(fs, KindDropoff)
	if !ok {
		t.Fatalf("expected the activate → checkout drop-off finding, got %+v", fs)
	}
	if drop.Rate != 30 {
		t.Fatalf("drop-off rate is %d%%, want 30%% — the fixture moved", drop.Rate)
	}
	if seg, ok := findKind(fs, KindSegment); ok {
		t.Fatalf("mobile IS the funnel average (%d%%) but was blamed anyway:\n  %s\n  %s",
			drop.Rate, seg.Title, seg.Detail)
	}
}

// And when a segment genuinely does underperform, every number in the blame — the base it is
// compared against and the multiplier derived from it — must be the drop-off card's own.
func TestSegmentBlameBaseEqualsTheDropoffRate(t *testing.T) {
	// mobile 6/60 = 10%, desktop 44/60 = 73%, funnel average 50/120 = 42%.
	// 100 outsiders activate and all convert, which used to push the base to 150/220 = 68%.
	fs := GenerateForFunnel(blameFixture(60, 6, 60, 44, 100, 100), funnelSteps())

	drop, ok := findKind(fs, KindDropoff)
	if !ok {
		t.Fatalf("expected a drop-off finding, got %+v", fs)
	}
	seg, ok := findKind(fs, KindSegment)
	if !ok {
		t.Fatalf("mobile converts 4× worse through the blamed step and was not blamed: %+v", fs)
	}
	if drop.From != seg.From || drop.To != seg.To {
		t.Fatalf("blame is about %s→%s but the drop-off is about %s→%s", seg.From, seg.To, drop.From, drop.To)
	}
	if drop.Rate != 42 {
		t.Fatalf("drop-off rate is %d%%, want 42%% — the fixture moved", drop.Rate)
	}
	want := fmt.Sprintf("against %d%% for everyone", drop.Rate)
	if !strings.Contains(seg.Detail, want) {
		t.Errorf("blame quotes a base the page never shows.\n want it to contain %q\n got %q", want, seg.Detail)
	}
	// 42% / 10% = 4.2×, not the 6.8× the outside-inflated base produced.
	if !strings.Contains(seg.Title, "4.2×") {
		t.Errorf("multiplier is derived from the wrong base: %q", seg.Title)
	}
	if seg.N != 60 {
		t.Errorf("blamed segment base is %d, want the 60 mobile users who entered the funnel", seg.N)
	}
}
