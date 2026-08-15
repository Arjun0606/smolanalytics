package investigate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// seedRevenue: `pre`/day then `post`/day of `name`, each carrying `prop` = amount.
func seedRevenue(t *testing.T, now time.Time, name, prop string, amount any, pre, post int) []event.Event {
	t.Helper()
	var evs []event.Event
	for d := 27; d >= 0; d-- {
		n := pre
		if d < 14 {
			n = post
		}
		for i := 0; i < n; i++ {
			e := event.Event{
				ID:   fmt.Sprintf("%s%d_%d", name, d, i),
				Name: name, DistinctID: fmt.Sprintf("u%d_%d", d, i),
				Timestamp: now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			}
			if prop != "" {
				e.Properties = map[string]any{prop: amount}
			}
			evs = append(evs, e)
		}
	}
	return evs
}

// THE RULE THE PACKAGE COULD NOT KEEP. Rule 1 is "rank by money, not p-value", and nothing
// anywhere wrote Cost.UsdPerMonth — so the money branch of rank() was unreachable and every
// finding fell back to people. The convention was already in the log: internal/instrument teaches
// checkout with ["plan","amount"], the demo emits amount: 30.0, and the docs show it in four
// places. It was taught, sent, and ignored by the one report where it mattered.
func TestAFindingOnAnEventThatCarriesRevenueIsSizedInMoney(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seedRevenue(t, now, "checkout", "amount", 30.0, 40, 10), Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding on a 4x collapse")
	}
	f := inv.Findings[0]
	if f.Cost.UsdPerMonth <= 0 {
		t.Fatalf("sized in people only, on an event carrying $30 amounts: %+v", f.Cost)
	}
	// Every person is worth exactly $30 here, so the figure must be people × 30 exactly.
	if want := float64(f.Cost.People) * 30; f.Cost.UsdPerMonth != want {
		t.Errorf("money = %.2f, want %.2f (%d people × $30)", f.Cost.UsdPerMonth, want, f.Cost.People)
	}
	if !strings.Contains(string(f.Cost.Basis), "amount") {
		t.Errorf("the basis must name the property the money came from, or the figure is unauditable: %q", f.Cost.Basis)
	}
	if !strings.Contains(f.Cost.Size(), "$") || !strings.Contains(f.Cost.Size(), "people") {
		t.Errorf("the rendered size must show BOTH the money and the people it derives from: %q", f.Cost.Size())
	}
}

// AND IT MUST NOT INVENT ONE. Rule 2. An event with no revenue property stays sized in people,
// and the basis says why — this is the behaviour that was already correct and must not regress.
func TestAnEventWithNoRevenueIsStillSizedInPeople(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seed(t, now, "signup", 40, 10), Opts{Now: now})
	if len(inv.Findings) == 0 {
		t.Fatal("no finding")
	}
	for _, f := range inv.Findings {
		if f.Cost.UsdPerMonth != 0 {
			t.Errorf("%q claims $%.2f/mo with no revenue property anywhere in the fixture",
				f.Headline, f.Cost.UsdPerMonth)
		}
		if f.Cost.Basis != BasisPeople {
			t.Errorf("%q does not say it was sized in people: %q", f.Headline, f.Cost.Basis)
		}
	}
}

// STRING AMOUNTS ARE NOT SUMMED. JSON decodes numbers to float64, so "29" was SENT as a string,
// and the filter engine would not match it as a number either. Quietly parsing it here would mean
// the revenue report and a filtered report disagree about the same events — the exact split this
// codebase keeps producing. Declining to size is the honest outcome.
func TestStringAmountsDoNotProduceAMoneyFigure(t *testing.T) {
	now := time.Now().UTC()
	inv := Run(seedRevenue(t, now, "checkout", "amount", "30", 40, 10), Opts{Now: now})
	for _, f := range inv.Findings {
		if f.Cost.UsdPerMonth != 0 {
			t.Errorf("summed string amounts into $%.2f/mo — a figure no filtered report would reproduce",
				f.Cost.UsdPerMonth)
		}
	}
}

// A HANDFUL OF SALES IS NOT AN AVERAGE ORDER VALUE. A dollar headline carries more authority than
// a people headline, so the bar to state one is higher, not the same.
func TestTooFewSalesNeverProduceAMoneyFigure(t *testing.T) {
	now := time.Now().UTC()
	// Big enough population to produce a finding, but the revenue events are far below the floor.
	evs := seed(t, now, "checkout", 40, 10)
	for i := 0; i < minRevenueEvents-1; i++ {
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("r%d", i), Name: "checkout", DistinctID: fmt.Sprintf("rich%d", i),
			Timestamp:  now.AddDate(0, 0, -1),
			Properties: map[string]any{"amount": 9999.0},
		})
	}
	for _, f := range Run(evs, Opts{Now: now}).Findings {
		if f.Cost.UsdPerMonth != 0 {
			t.Errorf("stated $%.2f/mo from fewer than %d revenue events", f.Cost.UsdPerMonth, minRevenueEvents)
		}
	}
}

// FREE USERS ARE IN THE DENOMINATOR. Counting a $0 checkout in neither the numerator nor the
// denominator inflates the average order value by exactly the share of customers who pay nothing,
// which is the most flattering possible error and therefore the one to guard hardest.
func TestZeroAmountCustomersDragTheMeanDown(t *testing.T) {
	now := time.Now().UTC()
	paid := seedRevenue(t, now, "checkout", "amount", 100.0, 30, 30)
	free := seedRevenue(t, now, "checkout", "amount", 0.0, 30, 30)
	for i := range free {
		free[i].DistinctID = "free_" + free[i].DistinctID
		free[i].ID = "free_" + free[i].ID
	}
	per, _, ok := revenuePerPerson(append(paid, free...), "checkout", Opts{Now: now}.withDefaults())
	if !ok {
		t.Fatal("declined to size a log with hundreds of paid checkouts")
	}
	// Half the people pay $100, half pay $0 → $50, not $100.
	if per > 60 || per < 40 {
		t.Errorf("mean per person = %.2f; with half the customers paying nothing it must be near $50, "+
			"not near $100 — free users are being dropped from the denominator", per)
	}
}

// A MIXED-TYPE PROPERTY IS NOT A REVENUE PROPERTY. If most `amount` values are strings, a mean
// over the numeric minority describes a subset while claiming to describe the whole.
func TestAMostlyNonNumericPropertyIsRefused(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 200; i++ {
		v := any("n/a")
		if i%5 == 0 { // only 20% numeric
			v = 50.0
		}
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("e%d", i), Name: "checkout", DistinctID: fmt.Sprintf("u%d", i),
			Timestamp: now.AddDate(0, 0, -1), Properties: map[string]any{"amount": v},
		})
	}
	if p := revenuePropFor(evs, "checkout"); p != "" {
		t.Errorf("accepted %q as a revenue property when only 20%% of its values are numeric", p)
	}
}

// THE MONEY BRANCH OF THE RANKING IS NOW REACHABLE — that was the whole point. A cheap finding
// affecting many people must not outrank an expensive one affecting fewer.
func TestTheExpensiveFindingLeadsEvenWhenFewerPeopleAreAffected(t *testing.T) {
	now := time.Now().UTC()
	// Lots of people, no money attached.
	evs := seed(t, now, "signup", 200, 50)
	// Far fewer people, but each worth $500.
	evs = append(evs, seedRevenue(t, now, "checkout", "amount", 500.0, 40, 10)...)

	inv := Run(evs, Opts{Now: now})
	if len(inv.Findings) < 2 {
		t.Fatalf("expected both metrics to be found, got %d", len(inv.Findings))
	}
	if inv.Findings[0].Event != "checkout" {
		t.Errorf("led with %q (%s) instead of the revenue-bearing finding — the money branch of "+
			"rank() is still unreachable\n  0: %s\n  1: %s",
			inv.Findings[0].Event, inv.Findings[0].Cost.Size(),
			inv.Findings[0].Headline, inv.Findings[1].Headline)
	}
	if inv.Findings[0].Cost.People > inv.Findings[1].Cost.People {
		t.Log("note: the money finding also had more people, so this test is weaker than intended")
	}
}
