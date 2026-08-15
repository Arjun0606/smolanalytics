package investigate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
)

// seed builds `pre` signups a day for two weeks, then `post` a day for two.
func seed(t *testing.T, now time.Time, name string, pre, post int) []event.Event {
	t.Helper()
	var evs []event.Event
	for d := 27; d >= 0; d-- {
		n := pre
		if d < 14 {
			n = post
		}
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{
				ID:   fmt.Sprintf("%s%d_%d", name, d, i),
				Name: name, DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			})
		}
	}
	return evs
}

// THE POINT OF THE WHOLE PACKAGE: a conclusion, with a number in it, that a person can act on.
func TestItFindsARegressionAndSaysWhatItCost(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 40, 10), Opts{Now: now})

	if inv.Quiet {
		t.Fatal("a 40/day → 10/day collapse was reported as a quiet day")
	}
	var f Finding
	for _, x := range inv.Findings {
		if x.Kind == KindRegression {
			f = x
		}
	}
	if f.Headline == "" {
		t.Fatalf("no regression finding: %+v", inv.Findings)
	}
	if !strings.Contains(f.Headline, "%") || !strings.Contains(f.Headline, "signup") {
		t.Errorf("the headline must name the metric and the size: %q", f.Headline)
	}
	if f.Day == "" {
		t.Error("no day — 'signups fell' without a date is not actionable")
	}
	if f.Cost.People <= 0 {
		t.Error("no size: a finding with no magnitude cannot be ranked against another")
	}
	if f.NextMove == "" {
		t.Error("no next move — a finding that ends at the diagnosis leaves the reader where they started")
	}
	if f.Evidence == "" {
		t.Error("no evidence query: every number here must open into the rows behind it")
	}
	if !f.NeedsYou {
		t.Error("a real regression is the one thing worth interrupting someone for")
	}
}

// RULE 3: A QUIET DAY IS AN ANSWER. Filler is how a daily brief teaches someone to stop reading
// it, and an unread brief replaces nobody.
func TestAQuietProductProducesNothing(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 30, 30), Opts{Now: now})
	if !inv.Quiet || len(inv.Findings) > 0 {
		t.Fatalf("invented %d finding(s) about a flat product: %+v", len(inv.Findings), inv.Findings)
	}
	// but it must still be able to prove it LOOKED
	if len(inv.Scanned) == 0 {
		t.Error("a quiet day with no record of what was scanned is indistinguishable from a broken sweep")
	}
}

// RULE 2: NEVER INVENT A NUMBER. With no revenue instrumented there must be no dollar figure —
// and the basis must say why.
func TestItNeverInventsMoney(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 40, 10), Opts{Now: now})
	for _, f := range inv.Findings {
		if f.Cost.UsdPerMonth != 0 {
			t.Errorf("%q claims $%.0f/mo with no revenue tracked anywhere in the fixture",
				f.Headline, f.Cost.UsdPerMonth)
		}
		if f.Cost.Basis != BasisPeople {
			t.Errorf("%q does not say how it was sized (basis %q)", f.Headline, f.Cost.Basis)
		}
	}
}

// Autocapture noise must not lead the brief. "page view fell 12%" is the cacophony complaint in
// one line: it is the raw material other findings are computed from, not a finding.
func TestAutocaptureNeverBecomesAFinding(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "$pageview", 400, 100), Opts{Now: now})
	for _, f := range inv.Findings {
		if strings.HasPrefix(f.Event, "$") {
			t.Errorf("an autocapture event became a headline finding: %q", f.Headline)
		}
	}
}

// A tiny product moves 50% every other day. Reporting that trains the reader to ignore the brief.
func TestSmallMovesAreNotNews(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 2, 1), Opts{Now: now})
	if len(inv.Findings) > 0 {
		t.Errorf("a 2/day → 1/day product produced %d finding(s): %+v", len(inv.Findings), inv.Findings)
	}
}

// RULE 1: RANK BY SIZE. The top line must be the most expensive thing happening, or the triage is
// decoration.
func TestTheBiggestProblemLeads(t *testing.T) {
	now := time.Now().UTC()
	evs := append(seed(t, now, "signup", 100, 20), seed(t, now, "invite_sent", 30, 8)...)
	inv := Run(evs, Opts{Now: now})
	if len(inv.Findings) < 2 {
		t.Fatalf("expected both metrics to be found, got %d", len(inv.Findings))
	}
	if inv.Findings[0].Cost.People < inv.Findings[1].Cost.People {
		t.Errorf("the brief leads with the smaller problem: %d before %d",
			inv.Findings[0].Cost.People, inv.Findings[1].Cost.People)
	}
}

// THE KILL LIST. The review nobody runs: it shipped, it had the traffic to answer, the answer was
// no, and it is still on.
func TestItNamesWhatShippedAndDidNothing(t *testing.T) {
	now := time.Now().UTC()
	started := now.AddDate(0, 0, -30)

	var evs []event.Event
	// Both arms convert identically — the definition of a dud.
	for i := 0; i < 400; i++ {
		u := fmt.Sprintf("u%d", i)
		arm := "control"
		if i%2 == 1 {
			arm = "treatment"
		}
		ts := started.Add(time.Duration(i) * time.Hour)
		evs = append(evs, event.Event{
			ID: "x" + u, Name: flag.ExposureEvent, DistinctID: u, Timestamp: ts,
			Properties: map[string]any{flag.PropFlag: "tooltip", flag.PropVariant: arm},
		})
		if i%5 == 0 {
			evs = append(evs, event.Event{
				ID: "c" + u, Name: "activate", DistinctID: u, Timestamp: ts.Add(time.Minute),
			})
		}
	}
	f := flag.Flag{
		Key: "tooltip", Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{
			Goal: "activate", Control: "control", Started: started,
		},
	}

	kl := KillList(evs, []flag.Flag{f}, Opts{Now: now, MinPeople: 20})
	if len(kl) == 0 {
		t.Fatal("a flag running 30 days with 400 people and no effect was not flagged for review")
	}
	k := kl[0]
	if !strings.Contains(k.Headline, "tooltip") || !strings.Contains(k.Headline, "activate") {
		t.Errorf("the headline must name the flag and the goal it failed to move: %q", k.Headline)
	}
	if k.NeedsYou {
		t.Error("a dud is not urgent — marking it so drowns the finding that actually is")
	}
	if !strings.Contains(k.NextMove, "remove") {
		t.Errorf("the next move must offer the removal, or nobody ever concludes it: %q", k.NextMove)
	}
}

// "It did nothing" and "we could not tell" are OPPOSITE findings. Conflating them gets a working
// feature deleted, which is the most expensive mistake this package could make.
func TestTooLittleTrafficIsNeverCalledADud(t *testing.T) {
	now := time.Now().UTC()
	started := now.AddDate(0, 0, -30)
	var evs []event.Event
	for i := 0; i < 6; i++ { // far too few to conclude anything
		u := fmt.Sprintf("u%d", i)
		arm := "control"
		if i%2 == 1 {
			arm = "treatment"
		}
		evs = append(evs, event.Event{
			ID: "x" + u, Name: flag.ExposureEvent, DistinctID: u,
			Timestamp:  started.Add(time.Duration(i) * time.Hour),
			Properties: map[string]any{flag.PropFlag: "tooltip", flag.PropVariant: arm},
		})
	}
	f := flag.Flag{
		Key: "tooltip", Measured: true,
		Variants:   []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{Goal: "activate", Control: "control", Started: started},
	}
	if kl := KillList(evs, []flag.Flag{f}, Opts{Now: now, MinPeople: 20}); len(kl) > 0 {
		t.Fatalf("told the user to remove a feature it had no data on: %q", kl[0].Headline)
	}
}

// A young experiment is not a dud, it is young.
func TestAFreshExperimentIsLeftAlone(t *testing.T) {
	now := time.Now().UTC()
	f := flag.Flag{
		Key: "new", Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{
			Goal: "activate", Control: "control", Started: now.AddDate(0, 0, -3),
		},
	}
	if kl := KillList(nil, []flag.Flag{f}, Opts{Now: now}); len(kl) > 0 {
		t.Fatalf("called a 3-day-old experiment a dud: %q", kl[0].Headline)
	}
}

// The triage line at the top of the brief — "3 things changed. 1 needs you." — is the whole
// reason this is not a dashboard.
func TestTriageSeparatesUrgentFromContext(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 100, 20), Opts{Now: now})
	if inv.NeedsYouCount() != 1 {
		t.Errorf("expected exactly one urgent finding, got %d of %d",
			inv.NeedsYouCount(), len(inv.Findings))
	}
}

// The small-sample guard, isolated.
//
// The previous test passed even with that guard deleted, because MinPeople caught its fixture
// first — a test that holds for a reason other than the one it names. This one clears MinPeople
// comfortably (40 exposed) while each ARM stays under the threshold where a rate can be called at
// all, so only the small-sample guard can stop it.
//
// The failure it prevents is the worst this package can produce: telling someone to delete a
// feature that was working, because we could not measure it.
func TestAnUnreadableArmIsNeverCalledADud(t *testing.T) {
	now := time.Now().UTC()
	started := now.AddDate(0, 0, -30)
	var evs []event.Event
	for i := 0; i < 40; i++ { // > MinPeople, but 20 per arm — under minArmForStats
		u := fmt.Sprintf("u%d", i)
		arm := "control"
		if i%2 == 1 {
			arm = "treatment"
		}
		ts := started.Add(time.Duration(i) * time.Hour)
		evs = append(evs, event.Event{
			ID: "x" + u, Name: flag.ExposureEvent, DistinctID: u, Timestamp: ts,
			Properties: map[string]any{flag.PropFlag: "tooltip", flag.PropVariant: arm},
		})
		if i%4 == 0 {
			evs = append(evs, event.Event{
				ID: "c" + u, Name: "activate", DistinctID: u, Timestamp: ts.Add(time.Minute),
			})
		}
	}
	f := flag.Flag{
		Key: "tooltip", Measured: true,
		Variants:   []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
		Experiment: &flag.Experiment{Goal: "activate", Control: "control", Started: started},
	}
	if kl := KillList(evs, []flag.Flag{f}, Opts{Now: now, MinPeople: 20}); len(kl) > 0 {
		t.Fatalf("called a statistically unreadable experiment a dud: %q — %s",
			kl[0].Headline, kl[0].Cause)
	}
}

// segSeed: a drop where `share` of the lost volume is concentrated in one browser.
func segSeed(t *testing.T, now time.Time, share float64) []event.Event {
	t.Helper()
	var evs []event.Event
	add := func(d, i int, browser string) {
		u := fmt.Sprintf("%s%d_%d", browser, d, i)
		evs = append(evs, event.Event{
			ID: u, Name: "checkout", DistinctID: u,
			Timestamp:  now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			Properties: map[string]any{"browser": browser},
		})
	}
	for d := 27; d >= 0; d-- {
		saf, chr := 30, 30
		if d < 14 {
			saf = 30 - int(30*share)
			chr = 30 - int(30*(1-share))
		}
		for i := 0; i < saf; i++ {
			add(d, i, "Safari")
		}
		for i := 0; i < chr; i++ {
			add(d, i+1000, "Chrome")
		}
	}
	return evs
}

// THE LINE THAT MAKES IT A COLLEAGUE. "checkout fell" is an alert; "and it is only Safari" turns
// an unbounded investigation into a twenty-minute one.
func TestItNamesTheSegmentCarryingTheLoss(t *testing.T) {
	now := time.Now().UTC()
	inv := WithContext(segSeed(t, now, 1.0), nil, nil, Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding at all on a clear drop")
	}
	c := inv.Findings[0].Cause
	if !strings.Contains(c, "Safari") {
		t.Errorf("the cause does not name the browser carrying the whole drop: %q", c)
	}
	if !strings.Contains(c, "%") {
		t.Errorf("the cause must quantify the concentration, or it is an assertion: %q", c)
	}
}

// AND IT MUST STAY QUIET WHEN THE LOSS IS EVEN. Naming the biggest slice of an even spread sends
// someone down a wrong path with confidence, which is worse than saying nothing.
func TestItRefusesToBlameASegmentWhenTheLossIsEven(t *testing.T) {
	now := time.Now().UTC()
	inv := WithContext(segSeed(t, now, 0.5), nil, nil, Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding at all on a clear drop")
	}
	c := inv.Findings[0].Cause
	if strings.Contains(c, "Safari") || strings.Contains(c, "Chrome") {
		t.Errorf("blamed a browser for a loss spread evenly across both: %q", c)
	}
	if c == "" {
		t.Error("an empty cause reads as though nobody looked; it must say what it could not find")
	}
}

// With nothing to attribute, the line must still tell the reader what would make attribution
// possible next time.
func TestAnUnattributableDropSaysWhatIsMissing(t *testing.T) {
	now := time.Now().UTC()
	inv := WithContext(seed(t, now, "signup", 40, 10), nil, nil, Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding")
	}
	if c := inv.Findings[0].Cause; !strings.Contains(c, "record deploys") {
		t.Errorf("the unattributed line must name the fix that would attribute it next time: %q", c)
	}
}

// AND WHEN THE DEPLOY IS THERE, IT MUST ACTUALLY BE NAMED.
//
// The test above asserted the FALLBACK — "record deploys and this line becomes 'which ship did
// it'" — and passed happily for months while the paid-off version was unreachable: shipNear
// passed 25 to a threshold measured in fractions, so Significant demanded a 2,500% swing and no
// deploy was ever named. Every customer who did the work the fallback asks for got the fallback
// anyway.
//
// A test for the graceful degradation and none for the capability is how that survives. This is
// the missing half: an ordinary regression with a deploy sitting on it names the deploy.
func TestARecordedShipOnTheDropIsNamed(t *testing.T) {
	now := time.Now().UTC()
	evs := seed(t, now, "signup", 40, 10)
	// The drop day the fixture creates, taken from the finding itself rather than assumed, so
	// this cannot drift if seed() changes.
	base := WithContext(evs, nil, nil, Opts{Now: now})
	if len(base.Findings) == 0 {
		t.Fatal("the fixture produces no finding, so there is nothing to attribute")
	}
	day, err := time.Parse("2006-01-02", base.Findings[0].Day)
	if err != nil {
		t.Fatalf("unparseable change day %q", base.Findings[0].Day)
	}

	inv := WithContext(evs, nil, []deploys.Deploy{{
		ID: "d1", SHA: "abc1234def", Message: "rewrote the signup form", At: day, Source: "cli",
	}}, Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding once a deploy was added")
	}
	c := inv.Findings[0].Cause
	if !strings.Contains(c, "abc1234") {
		t.Errorf("a ship landing on the drop day was not named — attribution is unreachable: %q", c)
	}
	if !strings.Contains(c, "correlation") {
		t.Errorf("attribution must be hedged as correlation, not asserted as cause: %q", c)
	}
}

// A SHARE CANNOT EXCEED THE WHOLE.
//
// The tests above asserted the cause "contains Safari" and "contains %", and both passed while
// the line read "117% of the loss is browser=Safari" — because before and after were measured
// over windows of different lengths, so the earlier side was inflated by however much longer it
// happened to be. Only rendering it caught that.
//
// An impossible number is worse than a missing one: it tells the reader the arithmetic is not
// being checked, and everything else on the page inherits that doubt.
func TestAShareIsNeverImpossible(t *testing.T) {
	now := time.Now().UTC()
	for _, share := range []float64{1.0, 0.8, 0.6} {
		inv := WithContext(segSeed(t, now, share), nil, nil, Opts{Now: now})
		for _, f := range inv.Findings {
			var pct float64
			if n, _ := fmt.Sscanf(f.Cause, "%f%% of the loss", &pct); n == 1 {
				if pct > 100 || pct < 0 {
					t.Errorf("share=%.1f produced an impossible concentration: %q", share, f.Cause)
				}
			}
		}
	}
}
