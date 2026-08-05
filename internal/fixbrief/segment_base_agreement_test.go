package fixbrief

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/insight"
)

// One brief used to carry two different answers to one question. The segment finding's Detail
// came from insight.segmentBlame, which measured "for everyone" with a standalone two-step
// funnel over ALL events (so anyone who did `from` counted, funnel entrant or not), while the
// evidence row three lines below it read the same rate off the real multi-step funnel:
//
//	Detail:   "…against 68% for everyone (6 of 60)."
//	Evidence: "device = mobile through activate → checkout = 10%, against 42% for everyone"
//
// A brief is the artefact that gets pasted into a coding agent as ground truth, so shipping
// two bases in one document is worse than shipping either. Both now come off the funnel the
// reader was actually looking at.
func TestSegmentBriefBaseMatchesItsOwnEvidence(t *testing.T) {
	steps := []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	base := time.Now().UTC().Add(-72 * time.Hour)
	var evs []event.Event
	add := func(u, name string, off time.Duration, device string) {
		evs = append(evs, event.Event{ID: u + name, DistinctID: u, Name: name, Timestamp: base.Add(off),
			Properties: map[string]any{"device": device}})
	}
	cohort := func(prefix, device string, n, conv int) {
		for i := 0; i < n; i++ {
			u := fmt.Sprintf("%s%d", prefix, i)
			add(u, "signup", time.Duration(i)*time.Minute, device)
			add(u, "activate", time.Duration(i)*time.Minute+time.Hour, device)
			if i < conv {
				add(u, "checkout", time.Duration(i)*time.Minute+2*time.Hour, device)
			}
		}
	}
	cohort("m", "mobile", 60, 6)   // 10% through activate → checkout
	cohort("d", "desktop", 60, 44) // 73%; funnel average 50/120 = 42%
	for i := 0; i < 100; i++ {     // never entered the funnel, all convert — the old inflated base
		u := fmt.Sprintf("o%d", i)
		add(u, "activate", time.Duration(i)*time.Minute+time.Hour, "desktop")
		add(u, "checkout", time.Duration(i)*time.Minute+2*time.Hour, "desktop")
	}

	var b Brief
	found := false
	for _, x := range ComputeAll(evs, steps, time.Now().UTC()) {
		if x.Finding.Kind == insight.KindSegment {
			b, found = x, true
		}
	}
	if !found {
		t.Fatal("mobile converts 4× worse through activate → checkout and produced no segment brief")
	}

	rate := regexp.MustCompile(`against (\d+)% for everyone`)
	detail := rate.FindStringSubmatch(b.Finding.Detail)
	if detail == nil {
		t.Fatalf("segment detail no longer quotes a base: %q", b.Finding.Detail)
	}
	var ev string
	for _, e := range b.Evidence {
		if strings.HasPrefix(e.Label, "device = mobile through") {
			ev = e.Value
		}
	}
	if ev == "" {
		t.Fatalf("no segment evidence row: %+v", b.Evidence)
	}
	evidence := rate.FindStringSubmatch(ev)
	if evidence == nil {
		t.Fatalf("segment evidence no longer quotes a base: %q", ev)
	}
	if detail[1] != evidence[1] {
		t.Errorf("one brief, two bases for the same transition:\n detail:   %s\n evidence: %s", b.Finding.Detail, ev)
	}
	if detail[1] != "42" {
		t.Errorf("base is %s%%, want the funnel's own 42%% — the fixture moved or the base is off the wrong population", detail[1])
	}
}
