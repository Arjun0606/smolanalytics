package investigate

// The gates of tracking.go, one at a time.
//
// Every test here was watched FAIL before it was trusted: the passing fixture is written first,
// then the specific condition is broken and the specific gate has to be the one that refuses.
// A guard nobody has seen fail is a guard nobody knows is wired up.
//
// And every assertion is on the PROPERTY, never the sentence. The rule-out prose will be
// rewritten — it is user-facing copy — and a test that pins the wording would make improving it
// a chore, which is how tests start getting deleted.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// fixture is an instance in one struct: an event that fires at `rate`/day on `path` until it
// steps to `after`/day `silentDays` ago, alongside autocaptured page views on the same path that
// either keep running or stop with it.
type fixture struct {
	now        time.Time
	span       int // days of history to build
	event      string
	path       string
	rate       int
	after      int
	silentDays int
	views      int
	viewsAfter int
	extra      []event.Event
}

func baseFixture() fixture {
	return fixture{
		now:        time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		span:       30,
		event:      "checkout_completed",
		path:       "/checkout",
		rate:       40,
		after:      0,
		silentDays: 5,
		views:      60,
		viewsAfter: 60,
	}
}

func (fx fixture) events() []event.Event {
	var evs []event.Event
	add := func(name string, day time.Time, n int) {
		for i := 0; i < n; i++ {
			ts := day.Add(time.Duration(i) * time.Minute)
			if ts.After(fx.now) {
				return
			}
			evs = append(evs, event.Event{
				ID:         fmt.Sprintf("%s-%s-%d", name, day.Format("0102"), i),
				Name:       name,
				DistinctID: fmt.Sprintf("u%d", i),
				Timestamp:  ts,
				Properties: map[string]any{"path": fx.path},
			})
		}
	}
	start := fx.now.AddDate(0, 0, -(fx.span - 1)).Truncate(24 * time.Hour)
	cut := fx.now.AddDate(0, 0, -fx.silentDays).Truncate(24 * time.Hour)
	for d := start; !d.After(fx.now); d = d.AddDate(0, 0, 1) {
		n, v := fx.rate, fx.views
		if !d.Before(cut) {
			n, v = fx.after, fx.viewsAfter
		}
		add(fx.event, d, n)
		add("$pageview", d, v)
	}
	return append(evs, fx.extra...)
}

// run is the real path: the full instance-context pass, which is where ClassifyTracking is wired.
func (fx fixture) run(planned PlanLookup) Investigation {
	return WithContext(fx.events(), nil, nil, Opts{Now: fx.now, Days: fx.span, Planned: planned})
}

func plannedOnly(names ...string) PlanLookup {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(n string) bool { return set[n] }
}

func findFor(t *testing.T, inv Investigation, name string) Finding {
	t.Helper()
	for _, f := range inv.Findings {
		if f.Event == name {
			return f
		}
	}
	t.Fatalf("no finding at all about %q — the fixture stopped producing a regression, so this "+
		"test is no longer exercising the gate it claims to: %+v", name, inv.Findings)
	return Finding{}
}

// T1 — THE CLEAN CASE. A planned event goes silent, its page's traffic does not, and the whole
// point of the class is that this is provable without a model.
func TestATrackingBreakIsPromotedWhenTheWitnessHeld(t *testing.T) {
	fx := baseFixture()
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind != KindTrackingBroke {
		t.Fatalf("a planned event at 40/day went to zero while its page held at 60/day and it was "+
			"reported as %q: %q / ruled out: %q", f.Kind, f.Headline, f.TrackingRuledOut)
	}
	if f.SilentSince == "" {
		t.Error("no silent_since — the cloud searches the repository around that day, so without it there is nothing to act on")
	}
	if f.Witness == "" || !strings.Contains(f.Witness, fx.path) {
		t.Errorf("the witness must name the metric that held, since it IS the discriminator: %q", f.Witness)
	}
	if f.TrackingRuledOut != "" {
		t.Errorf("promoted and ruled out at the same time: %q", f.TrackingRuledOut)
	}
	if !f.NeedsYou {
		t.Error("being blind on a metric you declared is worth interrupting someone for")
	}
	if f.Fingerprint != string(KindTrackingBroke)+"|"+f.Event+"|"+f.Day {
		t.Errorf("the fingerprint was not re-stamped after the kind changed, so the cloud's branch "+
			"name and idempotency key would still say regression: %q", f.Fingerprint)
	}
	if !strings.Contains(f.NextMove, "track(") {
		t.Errorf("the next move must be the mechanical one — restore the call: %q", f.NextMove)
	}
}

// T10 — THE SIZE MUST NOT BE A PEOPLE COUNT.
//
// The regression this was promoted from said "~1,200 people/mo". Nobody lost 1,200 people; we
// stopped counting 1,200 actions, and the real user impact is precisely what we can no longer
// see. Printing it as user loss is the invented number this package's own doc comment forbids.
func TestATrackingBreakIsNeverSizedInPeople(t *testing.T) {
	fx := baseFixture()
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Cost.People != 0 || f.Cost.Basis != BasisBlind {
		t.Fatalf("a tracking break carried a people count: %+v", f.Cost)
	}
	countsPeople := regexp.MustCompile(`[0-9]+ *(people|users)`)
	if f.Cost.SizeText == "" {
		t.Fatal("no rendered size at all — a JSON consumer would print an empty cell where the explanation should be")
	}
	if countsPeople.MatchString(f.Cost.SizeText) {
		t.Errorf("the size reads as a measured human count: %q", f.Cost.SizeText)
	}
	if strings.Contains(f.Cost.Readout(), "0") {
		t.Errorf("the slab readout renders a zero, which reads as 'nothing is wrong' on the one "+
			"finding that means the numbers stopped being true: %q", f.Cost.Readout())
	}
}

// T2 — THE PRODUCT BROKE. The event stopped AND the traffic behind it stopped. This is the case
// that must never open a pull request, and G5 is the only gate that can tell it apart from T1.
func TestAProductRegressionIsNotPromotedAndSaysWhy(t *testing.T) {
	fx := baseFixture()
	fx.viewsAfter = 0 // the page died too
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("the whole page went quiet and it was called a tracking break: %q", f.Headline)
	}
	if f.TrackingRuledOut == "" {
		t.Fatal("declined silently — a check that runs and says nothing is a check the reader assumes does not run")
	}
	if !strings.Contains(f.TrackingRuledOut, fx.path) {
		t.Errorf("the rule-out must name the witness that fell, or it is an assertion rather than a measurement: %q", f.TrackingRuledOut)
	}
}

// T3 — A PARTIAL FALL IS A PRODUCT CHANGE. A deleted call site produces silence; 40% still
// arriving means the code still runs.
func TestAPartialDropIsNotPromoted(t *testing.T) {
	fx := baseFixture()
	fx.after = 24 // still 60% of the level
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("an event still arriving at 24/day was called a deleted call site: %q", f.Headline)
	}
	// And the refusal has to be the RIGHT one. G3 (sustained) also declines this fixture, so
	// "not promoted" alone would pass with the silence gate deleted — the first version of this
	// test did exactly that. The fact a reader needs here is the rate it is STILL arriving at,
	// which only the silence gate is in a position to report.
	rates := regexp.MustCompile(`[0-9]+(\.[0-9]+)?/day`).FindAllString(f.TrackingRuledOut, -1)
	if len(rates) < 2 || !strings.Contains(f.TrackingRuledOut, "40.0/day") {
		t.Errorf("the refusal must state BOTH levels — what the event was arriving at before and "+
			"what it is still arriving at now — because that comparison is the entire reason a "+
			"partial fall is a product change and not a deleted line: %q", f.TrackingRuledOut)
	}
}

// T4 — NOT IN THE PLAN. Without a declaration, a stop is a product decision, and opening a pull
// request against a feature somebody deliberately removed is the failure that ends this category.
func TestAnUndeclaredEventIsNotPromoted(t *testing.T) {
	fx := baseFixture()
	f := findFor(t, fx.run(plannedOnly("something_else")), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("promoted an event nobody declared: %q", f.Headline)
	}
	if !strings.Contains(f.TrackingRuledOut, fx.event) {
		t.Errorf("the rule-out must name the undeclared event: %q", f.TrackingRuledOut)
	}
}

// T5 — NO PLAN AT ALL. Nil is a refusal, never a permissive default, and it has to teach the
// reader the one thing that would make the check work.
func TestWithNoTrackingPlanNothingIsEverPromoted(t *testing.T) {
	fx := baseFixture()
	f := findFor(t, fx.run(nil), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("promoted with no declared intent to compare against: %q", f.Headline)
	}
	if !strings.Contains(f.TrackingRuledOut, "set_tracking_plan") {
		t.Errorf("the rule-out must name the action that turns this on, or it is a dead end: %q", f.TrackingRuledOut)
	}
}

// T6 — SUSTAINED. One or two quiet days is a weekend, a cache, or an outage, and all of those
// come back on their own.
func TestATwoDaySilenceIsTooFreshToPromote(t *testing.T) {
	fx := baseFixture()
	fx.silentDays = 2
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("promoted after two quiet days: %q", f.Headline)
	}
	if f.TrackingRuledOut == "" {
		t.Error("no reason given for waiting")
	}
}

// T6b — TOO FRESH TO BE EVIDENCE, asserted through the gate that actually refuses it.
//
// This one is a lesson about test ordering, kept as a test. Written the obvious way — a fixture
// silent for two days, run through WithContext — it passes with the age gate DELETED, because the
// sustained gate refuses the same fixture first. The mutation run caught that: the age check's
// lower bound was unreachable dead code that read like a safety mechanism.
//
// So it is asserted where it is reachable: ClassifyTracking is exported, and a caller handing it a
// finding it built itself is exactly the case the age window exists for. The property is that a
// break too young for the data to be in is refused FOR THAT REASON — not incidentally, by a
// different gate that happens to overlap today.
func TestABreakTooYoungForTheDataToBeInIsRefusedAsSuch(t *testing.T) {
	fx := baseFixture()
	fx.silentDays = 6
	// The finding claims the drop happened YESTERDAY, which is younger than the window we can act
	// in, while the underlying event has in fact been quiet for six days.
	yesterday := fx.now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	fs := []Finding{{Kind: KindRegression, Event: fx.event, Day: yesterday.Format("2006-01-02")}}
	ClassifyTracking(fx.events(), fs, plannedOnly(fx.event), Opts{Now: fx.now, Days: fx.span})

	if fs[0].Kind == KindTrackingBroke {
		t.Fatalf("promoted a break dated yesterday — the cloud would search a repository around a "+
			"day whose data is not in yet: %q", fs[0].Headline)
	}
	if !strings.Contains(fs[0].TrackingRuledOut, "data is not in") {
		t.Errorf("refused, but not by the age window — so the age window is still unproven and may "+
			"be dead code again the moment the gate in front of it moves: %q", fs[0].TrackingRuledOut)
	}
}

// AND THE INVARIANT BEHIND IT: nothing younger than the window may ever promote, whichever gate
// gets there first. This is the claim the cloud relies on when it searches commits around
// silent_since; the test above proves the gate fires, this proves the outcome holds.
func TestNoPromotedBreakIsYoungerThanTheActionableWindow(t *testing.T) {
	for _, silent := range []int{1, 2, 3, 5, 10, 21, 22, 30} {
		fx := baseFixture()
		fx.span, fx.silentDays = 60, silent
		for _, f := range fx.run(plannedOnly(fx.event)).Findings {
			if f.Kind != KindTrackingBroke {
				continue
			}
			day, err := time.Parse("2006-01-02", f.SilentSince)
			if err != nil {
				t.Fatalf("promoted with an unparseable silent_since %q", f.SilentSince)
			}
			age := int(fx.now.Sub(day).Hours() / 24)
			if age < minAgeDays || age > maxAgeDays {
				t.Errorf("silent for %d days promoted a break aged %d, outside the [%d,%d] window the "+
					"whole mechanical-edit claim rests on", silent, age, minAgeDays, maxAgeDays)
			}
		}
	}
}

// T7 — AND SETTLED. Past three weeks the repository has moved on: the surrounding code has
// changed, so putting the line back stops being a mechanical edit and becomes a guess.
func TestALongDeadEventIsNotPromoted(t *testing.T) {
	fx := baseFixture()
	fx.span, fx.silentDays = 60, 25
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("promoted a break that is 25 days old: %q", f.Headline)
	}
	if f.TrackingRuledOut == "" {
		t.Error("no reason given for declining a stale break")
	}
}

// T8 — NO WITNESS. On a quiet instance nothing is busy enough to prove the product kept running,
// and "we cannot tell" is a real answer. This is the honest report-and-say-why outcome.
func TestWithNoBusyWitnessItRefusesAndSaysSo(t *testing.T) {
	fx := baseFixture()
	fx.views, fx.viewsAfter = 2, 2 // nothing else on the instance is busy enough to mean anything
	f := findFor(t, fx.run(plannedOnly(fx.event)), fx.event)

	if f.Kind == KindTrackingBroke {
		t.Fatalf("promoted with a 2/day witness, which proves nothing about anything: %q", f.Headline)
	}
	if !strings.Contains(f.TrackingRuledOut, "witness") {
		t.Errorf("the rule-out must say the witness is the missing piece, not imply the event is fine: %q", f.TrackingRuledOut)
	}
}

// T9 — IT NEVER SILENTLY DECLINES.
//
// The invariant behind every test above, asserted over all of them at once: for any regression,
// exactly one of "promoted" and "explained why not" is true. Neither is the failure mode that
// matters — a reader who sees a regression with no tracking verdict on it concludes the check
// does not run, and stops looking for it.
func TestEveryRegressionIsEitherPromotedOrExplained(t *testing.T) {
	partial := baseFixture()
	partial.after = 24
	product := baseFixture()
	product.viewsAfter = 0
	fresh := baseFixture()
	fresh.silentDays = 2
	stale := baseFixture()
	stale.span, stale.silentDays = 60, 25
	quiet := baseFixture()
	quiet.views, quiet.viewsAfter = 2, 2

	cases := []struct {
		name    string
		fx      fixture
		planned PlanLookup
	}{
		{"clean break", baseFixture(), plannedOnly("checkout_completed")},
		{"partial drop", partial, plannedOnly("checkout_completed")},
		{"product regression", product, plannedOnly("checkout_completed")},
		{"too fresh", fresh, plannedOnly("checkout_completed")},
		{"too stale", stale, plannedOnly("checkout_completed")},
		{"no witness", quiet, plannedOnly("checkout_completed")},
		{"undeclared", baseFixture(), plannedOnly("other")},
		{"no plan", baseFixture(), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := c.fx.run(c.planned)
			for _, f := range inv.Findings {
				switch f.Kind {
				case KindRegression:
					if f.TrackingRuledOut == "" {
						t.Errorf("regression on %q declined silently: %q", f.Event, f.Headline)
					}
				case KindTrackingBroke:
					if f.TrackingRuledOut != "" {
						t.Errorf("promoted AND ruled out on %q: %q", f.Event, f.TrackingRuledOut)
					}
					if f.Witness == "" {
						t.Errorf("promoted %q with no witness recorded", f.Event)
					}
				}
			}
		})
	}
}

// T11 — OUR OWN RECEIPT IS NOT THEIR TRAFFIC. $tracking_pr_opened must never become a finding
// about itself. productEvents happens to skip $-prefixed names today; that exclusion is
// incidental, someone will change the prefix rule, and this is what will notice.
func TestTheTrackingReceiptCanNeverBecomeAFindingAboutItself(t *testing.T) {
	fx := baseFixture()
	// A burst of receipts big enough to be a step change in its own right, had it been counted.
	start := fx.now.AddDate(0, 0, -6).Truncate(24 * time.Hour)
	for d := 0; d < 6; d++ {
		for i := 0; i < 40; i++ {
			fx.extra = append(fx.extra, event.Event{
				ID:   fmt.Sprintf("rcpt%d-%d", d, i),
				Name: TrackingPrEvent, DistinctID: "$system",
				Timestamp: start.AddDate(0, 0, d).Add(time.Duration(i) * time.Minute),
			})
		}
	}
	inv := fx.run(plannedOnly(fx.event))
	for _, f := range inv.Findings {
		if f.Event == TrackingPrEvent {
			t.Fatalf("the robot's own receipts produced a finding about the robot: %q", f.Headline)
		}
	}
	for _, n := range inv.Scanned {
		if n == TrackingPrEvent {
			t.Fatalf("%s was investigated as if it were product traffic", TrackingPrEvent)
		}
	}
}

// A BREAK SOMEBODY ALREADY FIXED BY HAND MUST NOT PROMOTE.
//
// The cloud opens a pull request off a promoted finding, so a resumed event promoting would mean
// a PR restoring a call that is already back. Two mechanisms stop it — Run marks recovery before
// classification, and G2 requires the event to still be silent right now — and this pins the
// outcome rather than either mechanism.
func TestAResumedEventIsNeverPromoted(t *testing.T) {
	fx := baseFixture()
	fx.silentDays = 12
	evs := fx.events()
	// It came back four days ago, at the old level.
	back := fx.now.AddDate(0, 0, -4).Truncate(24 * time.Hour)
	for d := back; !d.After(fx.now); d = d.AddDate(0, 0, 1) {
		for i := 0; i < fx.rate; i++ {
			ts := d.Add(time.Duration(i) * time.Minute)
			if ts.After(fx.now) {
				break
			}
			evs = append(evs, event.Event{
				ID: fmt.Sprintf("back-%s-%d", d.Format("0102"), i), Name: fx.event,
				DistinctID: fmt.Sprintf("b%d", i), Timestamp: ts,
				Properties: map[string]any{"path": fx.path},
			})
		}
	}
	inv := WithContext(evs, nil, nil, Opts{Now: fx.now, Days: fx.span, Planned: plannedOnly(fx.event)})
	for _, f := range inv.Findings {
		if f.Kind == KindTrackingBroke {
			t.Fatalf("promoted an event that is arriving again right now — a pull request off this "+
				"would restore a line that is already there: %q", f.Headline)
		}
	}
}

// RANKING. A tracking break's cost is unmeasurable by definition, so the People tiebreak would
// sink it beneath every finding that CAN be sized — burying the one finding that says the sizes
// are wrong underneath the numbers it is telling you not to trust.
func TestAnUnsizedTrackingBreakOutranksSizedFindings(t *testing.T) {
	fs := []Finding{
		{Kind: KindSurge, Event: "signup", Cost: Cost{People: 5000, Basis: BasisPeople}},
		{Kind: KindRegression, Event: "activate", NeedsYou: true, Cost: Cost{People: 1240, Basis: BasisPeople}},
		{Kind: KindTrackingBroke, Event: "checkout", NeedsYou: true, Cost: Cost{People: 0, Basis: BasisBlind}},
	}
	rank(fs)
	if fs[0].Kind != KindTrackingBroke {
		t.Fatalf("a zero-cost tracking break sank below a sized finding: got %q first", fs[0].Kind)
	}

	// ...but it must not outrank a finding that is genuinely closed, or the desk starts leading
	// with work nobody has to do.
	fs = []Finding{
		{Kind: KindTrackingBroke, Event: "checkout", Recovered: true, Cost: Cost{Basis: BasisBlind}},
		{Kind: KindRegression, Event: "activate", NeedsYou: true, Cost: Cost{People: 10, Basis: BasisPeople}},
	}
	rank(fs)
	if fs[0].Recovered {
		t.Fatal("a recovered finding took the lead over open work")
	}
}

// The path witness must survive another vendor's property names AND their URLs, because we sell an
// import path from them. A dataset carrying a full $current_url is the same instance to us.
//
// Every event here gets a DIFFERENT query string, which is what real traffic looks like and what
// makes this a test rather than a formality: without reducing the URL to its path, those are N
// distinct pages, no one page is dominant, and the tight same-page witness silently degrades into
// the loose instance-wide fallback. The first version of this test appended a constant `?ref=x` to
// every URL — so the URLs still compared equal to each other, the page witness was still found,
// and the test passed with normalizePath deleted. The mutation run is what exposed that.
func TestTheWitnessReadsAnyVendorsPathProperty(t *testing.T) {
	fx := baseFixture()
	evs := fx.events()
	for i := range evs {
		p := evs[i].Properties["path"]
		evs[i].Properties = map[string]any{
			"$current_url": fmt.Sprintf("https://shop.example.com%s?ref=%d&sid=%s", p.(string), i, evs[i].ID),
		}
	}
	inv := WithContext(evs, nil, nil, Opts{Now: fx.now, Days: fx.span, Planned: plannedOnly(fx.event)})
	f := findFor(t, inv, fx.event)
	if f.Kind != KindTrackingBroke {
		t.Fatalf("an imported dataset's $current_url was not recognised as the page, so the witness "+
			"was never found: %q / %q", f.Kind, f.TrackingRuledOut)
	}
	// The page witness specifically — the tight control, same users and same surface. Degrading to
	// the instance-wide fallback here would still promote, which is why the KIND assertion above
	// cannot carry this test on its own.
	if !strings.Contains(f.Witness, "$pageview on /checkout") {
		t.Errorf("every query string was treated as its own page, so no page was dominant and the "+
			"tight same-page witness quietly degraded to the loose one: %q", f.Witness)
	}
	if strings.ContainsAny(f.Witness, "?#") || strings.Contains(f.Witness, "://") {
		t.Errorf("the witness names a URL rather than a page, so it is not comparable between two "+
			"visitors who arrived differently: %q", f.Witness)
	}
}
