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

// A RISE IS NOT A DROP.
//
// The unattributed-cause line hardcoded the word "drop", so a metric that rose 34% was explained
// with a sentence about a drop — and on a healthy product most findings are rises, which made the
// most-repeated line on the page the one visibly describing something else. Every reader who
// noticed learned the copy is templated, and doubted the numbers next to it.
func TestTheUnattributedLineMatchesTheDirectionOfTheFinding(t *testing.T) {
	now := time.Now().UTC()
	// A surge: 10/day for the first half, 40/day for the second.
	rise := WithContext(seed(t, now, "signup", 10, 40), nil, nil, Opts{Now: now})
	if len(rise.Findings) == 0 {
		t.Fatal("a 4x surge produced no finding")
	}
	for _, f := range rise.Findings {
		if f.Kind != KindSurge {
			continue
		}
		if strings.Contains(f.Cause, "the drop is spread") {
			t.Errorf("a rise is explained as a drop: %q\n  headline: %s", f.Cause, f.Headline)
		}
	}

	// And the drop still says drop, so the fix is not just deleting the word.
	fall := WithContext(seed(t, now, "signup", 40, 10), nil, nil, Opts{Now: now})
	if len(fall.Findings) == 0 {
		t.Fatal("a 4x collapse produced no finding")
	}
	if c := fall.Findings[0].Cause; !strings.Contains(c, "the drop is spread") {
		t.Errorf("a drop no longer reads as a drop: %q", c)
	}
}

// THE SAME PARAGRAPH MUST NOT PRINT ON EVERY ROW.
//
// The unattributed cause is one long sentence ending in an instruction, and on an instance with
// no deploys recorded it is the cause for MOST findings — so the dashboard printed the identical
// two-line paragraph twice inside its first 1000px. The marketing panel already rendered it as
// three words per row plus the instruction once underneath; the product had the worse version.
//
// Renderers need to tell the two apart WITHOUT matching on prose, or the next copy edit silently
// breaks the distinction.
func TestARendererCanTellAnExplainedFindingFromAnUnexplainedOne(t *testing.T) {
	now := time.Now().UTC()

	// Nothing to attribute: no deploys, and the loss spread evenly across browsers.
	plain := WithContext(seed(t, now, "signup", 40, 10), nil, nil, Opts{Now: now})
	if len(plain.Findings) == 0 {
		t.Fatal("no finding")
	}
	if plain.Findings[0].Attributed() {
		t.Errorf("an unexplained finding reports itself as attributed: %q", plain.Findings[0].Cause)
	}
	if !plain.AnyUnattributed() {
		t.Error("AnyUnattributed is false while a finding is unexplained — the renderer would " +
			"omit the one line telling the reader how to fix it")
	}
	if !strings.HasPrefix(plain.Findings[0].Cause, Unattributed) {
		t.Errorf("the fallback no longer starts with the exported prefix, so Attributed() is "+
			"silently broken: %q", plain.Findings[0].Cause)
	}

	// A named segment IS an explanation.
	seg := WithContext(segSeed(t, now, 1.0), nil, nil, Opts{Now: now})
	if len(seg.Findings) == 0 {
		t.Fatal("no finding on the segmented fixture")
	}
	if !seg.Findings[0].Attributed() {
		t.Errorf("a finding naming the browser carrying the whole drop reports as unattributed: %q",
			seg.Findings[0].Cause)
	}
	if seg.AnyUnattributed() {
		t.Error("AnyUnattributed is true when every finding is explained — the renderer would " +
			"print an instruction that applies to nothing on screen")
	}
}

// The quarter table renders from a structured label, not by parsing the headline. "unchanged" and
// "can't tell" are opposite findings and must stay distinguishable in one table cell.
func TestEveryMovementCarriesAShortLabel(t *testing.T) {
	for v, want := range map[Verdict]string{
		MovedUp:    "up",
		MovedDown:  "down",
		Unchanged:  "unchanged",
		CannotTell: "can't tell",
		NeverFired: "not recorded",
	} {
		if got := label(v); got != want {
			t.Errorf("label(%q) = %q, want %q", v, got, want)
		}
	}
	now := time.Now().UTC()
	ms := Movements(seed(t, now, "signup", 40, 10), nil, MovementOpts{Now: now})
	if len(ms) == 0 {
		t.Fatal("no movements")
	}
	for _, m := range ms {
		if m.Label == "" {
			t.Errorf("%q carries no label, so a table cell has nothing to render", m.Metric)
		}
	}
}

// EVERY SURFACE ATTRIBUTES, NOT JUST THE ONE WITH A DEPLOY STORE.
//
// Attribute() was reached only from WithContext, which only the dashboard and the share page
// call. So the CLI brief, GET /v1/brief and the cloud daily email — every surface a person reads
// WITHOUT opening a browser — printed the raw "cause not yet attributed" placeholder on every
// finding, forever, while the dashboard said "62% of the loss is os=Android" about the same event
// on the same instance. Measured on the live demo before the fix.
//
// The deploy half needs a store. The SEGMENT half needs only the events already in hand, so
// there was never a reason to withhold it — it was a call site nobody added.
func TestRunAttributesTheSegmentWithoutADeployStore(t *testing.T) {
	now := time.Now().UTC()

	// segSeed concentrates the whole drop in one browser, which is exactly what worstSegment
	// exists to find.
	inv := Run(segSeed(t, now, 1.0), Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding on a clear drop")
	}
	c := inv.Findings[0].Cause
	if strings.Contains(c, "cause not yet attributed") {
		t.Errorf("Run() left the raw placeholder on a finding whose loss is entirely one browser: %q\n"+
			"  every unprompted surface reads this, and they all said nothing while the dashboard "+
			"named the segment", c)
	}
	if !strings.Contains(c, "Safari") {
		t.Errorf("Run() did not name the browser carrying the whole drop: %q", c)
	}

	// And the honest absence still reads as an absence, not as a placeholder: an evenly-spread
	// loss must say what it could not find, in the words the renderers key on.
	even := Run(segSeed(t, now, 0.5), Opts{Now: now})
	if len(even.Findings) == 0 {
		t.Fatal("no finding on the even fixture")
	}
	if ec := even.Findings[0].Cause; strings.Contains(ec, "cause not yet attributed") {
		t.Errorf("an unattributable finding still carries the placeholder rather than the "+
			"renderable fallback: %q", ec)
	}
	if !even.AnyUnattributed() {
		t.Error("AnyUnattributed is false on an evenly-spread loss, so no surface would print " +
			"the line explaining how to make attribution possible")
	}
}

// THE LOOP CLOSES: A REGRESSION THAT CAME BACK SAYS SO.
//
// Every analytics tool reports the break; none comes back to report the resolution, so the reader
// re-derives last week's state by squinting at a chart. "Recovered" is computed by the same
// daily-series arithmetic that detected the drop — and it says recovered, never fixed, because no
// causality is known.
func TestARecoveredRegressionIsMarkedAndRetired(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	mk := func(day, n int) {
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("r%d_%d", day, i), Name: "checkout",
				DistinctID: fmt.Sprintf("u%d_%d", day, i),
				Timestamp:  now.AddDate(0, 0, -day).Add(time.Duration(i) * time.Minute),
			})
		}
	}
	// 40/day, a collapse to 10/day ten days ago, then a return to 40/day for the last four days.
	for d := 27; d >= 11; d-- {
		mk(d, 40)
	}
	for d := 10; d >= 5; d-- {
		mk(d, 10)
	}
	for d := 4; d >= 0; d-- {
		mk(d, 40)
	}

	inv := Run(evs, Opts{Now: now})
	var reg *Finding
	for i := range inv.Findings {
		if inv.Findings[i].Kind == KindRegression {
			reg = &inv.Findings[i]
		}
	}
	if reg == nil {
		t.Fatal("the collapse was never found, so recovery has nothing to mark")
	}
	if !reg.Recovered {
		t.Fatalf("the metric is back at its pre-drop level for four days and the finding still "+
			"reads as open work: %+v", reg)
	}
	if reg.NeedsYou {
		t.Error("a recovered finding still claims to need a human — sending someone to fix a " +
			"thing that already recovered teaches them the queue is stale")
	}
	if !strings.Contains(reg.NextMove, "recovered") {
		t.Errorf("the next move does not say it recovered: %q", reg.NextMove)
	}

	// AND THE NEGATIVE HALF: while the metric is still down, nothing may claim recovery.
	var down []event.Event
	for d := 27; d >= 11; d-- {
		for i := 0; i < 40; i++ {
			down = append(down, event.Event{ID: fmt.Sprintf("d%d_%d", d, i), Name: "checkout",
				DistinctID: fmt.Sprintf("v%d_%d", d, i), Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)})
		}
	}
	for d := 10; d >= 0; d-- {
		for i := 0; i < 10; i++ {
			down = append(down, event.Event{ID: fmt.Sprintf("e%d_%d", d, i), Name: "checkout",
				DistinctID: fmt.Sprintf("w%d_%d", d, i), Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)})
		}
	}
	inv2 := Run(down, Opts{Now: now})
	for _, f := range inv2.Findings {
		if f.Kind == KindRegression && f.Recovered {
			t.Errorf("a metric still at a quarter of its old level was marked recovered: %q", f.Headline)
		}
	}
}

// A TINY PRODUCT IS TOLD WHY IT IS QUIET, WITH THE NUMBER THAT CHANGES IT.
//
// The gates are correct — 3/day genuinely swings 50% by chance — but correct behaviour rendered
// as permanent silence is zero delivered value, and the smallest products this tool targets are
// exactly the ones the gates exclude. The floor note is the honest day-one answer.
func TestABelowFloorMetricIsNamedWithItsArithmetic(t *testing.T) {
	now := time.Now().UTC()
	evs := seed(t, now, "signup", 3, 3) // steady 3/day: under the 5/day floor, no step change
	inv := Run(evs, Opts{Now: now})

	if len(inv.Findings) != 0 {
		t.Fatalf("a flat 3/day produced findings: %+v", inv.Findings)
	}
	if len(inv.BelowFloor) != 1 || inv.BelowFloor[0].Event != "signup" {
		t.Fatalf("the floored metric was not named: %+v", inv.BelowFloor)
	}
	fn := inv.BelowFloor[0]
	if fn.PerDay <= 0 || fn.NeedPerDay != 5 {
		t.Errorf("the note must carry the real rate and the required rate: %+v", fn)
	}
	if !strings.Contains(fn.Note, "coin flip") && !strings.Contains(fn.Note, "/day") {
		t.Errorf("the note does not explain the arithmetic: %q", fn.Note)
	}

	// A metric ABOVE the floor that simply did not move is quiet, not floored — opposite answers.
	busy := Run(seed(t, now, "signup", 30, 30), Opts{Now: now})
	if len(busy.BelowFloor) != 0 {
		t.Errorf("a healthy 30/day metric was reported as below the floor: %+v", busy.BelowFloor)
	}
}

// THE DIMENSIONS ARE DISCOVERED, NOT HARDCODED.
//
// Six built-in properties were the only ones ever checked, so a drop carried entirely by
// free-plan users was reported as "spread evenly" — a false statement produced by not looking.
// The investigator now discovers the instance's own string properties per event and checks those
// too: what is worth blaming depends on what the operator actually tracks.
func TestADropCarriedByACustomPropertyIsNamed(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	add := func(day, i int, plan string, id string) {
		evs = append(evs, event.Event{
			ID: id, Name: "checkout", DistinctID: id,
			Timestamp:  now.AddDate(0, 0, -day).Add(time.Duration(i) * time.Minute),
			Properties: map[string]any{"plan": plan},
		})
	}
	// Before the drop: 20 free + 20 pro per day. After: free collapses to 2, pro untouched.
	for d := 27; d >= 0; d-- {
		freeN := 20
		if d < 7 {
			freeN = 2
		}
		for i := 0; i < freeN; i++ {
			add(d, i, "free", fmt.Sprintf("f%d_%d", d, i))
		}
		for i := 0; i < 20; i++ {
			add(d, i+100, "pro", fmt.Sprintf("p%d_%d", d, i))
		}
	}
	inv := Run(evs, Opts{Now: now})
	var reg *Finding
	for i := range inv.Findings {
		if inv.Findings[i].Kind == KindRegression {
			reg = &inv.Findings[i]
		}
	}
	if reg == nil {
		t.Fatal("the free-plan collapse was not even detected")
	}
	if !strings.Contains(reg.Cause, "free") {
		t.Errorf("the drop is carried entirely by plan=free and the cause does not say so: %q\n"+
			"  (a hardcoded dimension list reports this as 'spread evenly', which is false)", reg.Cause)
	}
}

// THE LOOP, END TO END: acted + recovered = VERIFIED.
//
// This is the sentence the whole self-healing pivot exists to produce — a human's work connected
// to a measured outcome. And its honest sibling: acted but still down must say so, never
// "verified", never silence.
func TestActedPlusRecoveredReadsVerified(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	mk := func(day, n int, name string) {
		for i := 0; i < n; i++ {
			evs = append(evs, event.Event{ID: fmt.Sprintf("%s%d_%d", name, day, i), Name: name,
				DistinctID: fmt.Sprintf("u%d_%d", day, i),
				Timestamp:  now.AddDate(0, 0, -day).Add(time.Duration(i) * time.Minute)})
		}
	}
	// collapse ten days ago, recovered for the last four
	for d := 27; d >= 11; d-- {
		mk(d, 40, "checkout")
	}
	for d := 10; d >= 5; d-- {
		mk(d, 10, "checkout")
	}
	for d := 4; d >= 0; d-- {
		mk(d, 40, "checkout")
	}
	inv := Run(evs, Opts{Now: now})
	var reg *Finding
	for i := range inv.Findings {
		if inv.Findings[i].Kind == KindRegression {
			reg = &inv.Findings[i]
		}
	}
	if reg == nil || !reg.Recovered {
		t.Fatal("fixture did not produce a recovered regression")
	}
	if reg.Fingerprint == "" {
		t.Fatal("no fingerprint — the ledger has nothing to attach to")
	}

	actedAt := now.AddDate(0, 0, -6)
	ApplyActed(inv.Findings, func(fp string) (time.Time, bool) {
		if fp == reg.Fingerprint {
			return actedAt, true
		}
		return time.Time{}, false
	}, now)

	if !strings.Contains(reg.NextMove, "verified") {
		t.Errorf("acted + recovered did not read as verified: %q", reg.NextMove)
	}

	// the honest sibling: still down → acted but never "verified"
	var down []event.Event
	for d := 27; d >= 11; d-- {
		for i := 0; i < 40; i++ {
			down = append(down, event.Event{ID: fmt.Sprintf("d%d_%d", d, i), Name: "checkout",
				DistinctID: fmt.Sprintf("v%d_%d", d, i), Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)})
		}
	}
	for d := 10; d >= 0; d-- {
		for i := 0; i < 10; i++ {
			down = append(down, event.Event{ID: fmt.Sprintf("e%d_%d", d, i), Name: "checkout",
				DistinctID: fmt.Sprintf("w%d_%d", d, i), Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute)})
		}
	}
	inv2 := Run(down, Opts{Now: now})
	ApplyActed(inv2.Findings, func(string) (time.Time, bool) { return actedAt, true }, now)
	for _, f := range inv2.Findings {
		if f.Kind == KindRegression {
			if strings.Contains(f.NextMove, "verified") {
				t.Errorf("still-down + acted read as verified: %q", f.NextMove)
			}
			if !strings.Contains(f.NextMove, "not recovered yet") {
				t.Errorf("the honest middle state is missing: %q", f.NextMove)
			}
		}
	}
}
