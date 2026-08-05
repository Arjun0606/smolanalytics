package groups

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/retention"
)

func gev(user, name, acct string, day int) event.Event {
	e := event.Event{
		DistinctID: user,
		Name:       name,
		Timestamp:  time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, day),
		Properties: map[string]any{},
	}
	if acct != "" {
		e.Properties["company"] = acct
	}
	return e
}

// The whole reason this feature exists. One admin at a fifty-seat account converting is a
// CONVERTED ACCOUNT and forty-nine unconverted users, and a B2B company reading the user number
// concludes the product is failing while its revenue says otherwise.
func TestAnAccountConvertsWhenAnyoneInItConverts(t *testing.T) {
	var evs []event.Event
	// Ten people at bigco, all visit, exactly one buys.
	for i := 0; i < 10; i++ {
		u := string(rune('a' + i))
		evs = append(evs, gev(u, "visit", "bigco", 0))
	}
	evs = append(evs, gev("a", "buy", "bigco", 1))
	// One person at solo, who buys.
	evs = append(evs, gev("z", "visit", "solo", 0), gev("z", "buy", "solo", 1))

	steps := []funnel.Step{{Event: "visit"}, {Event: "buy"}}
	users := funnel.Compute(evs, steps, 7*24*time.Hour)
	if got := users.Steps[1].Count; got != 2 {
		t.Fatalf("user grain: want 2 of 11 people converting, got %d", got)
	}

	regrouped, g := Regroup(evs, "company")
	accts := funnel.Compute(regrouped, steps, 7*24*time.Hour)
	if accts.Steps[0].Count != 2 || accts.Steps[1].Count != 2 {
		t.Fatalf("account grain: want 2 of 2 accounts converting, got %d of %d",
			accts.Steps[1].Count, accts.Steps[0].Count)
	}
	// 18% of users and 100% of accounts. Same events, same engine, and the two numbers answer
	// genuinely different questions — which is the point, and why one must never be labelled as
	// the other.
	if g.Groups != 2 {
		t.Fatalf("want 2 accounts, got %d", g.Groups)
	}
	if g.Dropped != 0 {
		t.Fatalf("nothing should have been dropped, got %d", g.Dropped)
	}
}

// A group property is set once, on signup or identify, and is absent from the pageviews that
// follow. Without first-touch stamping every account funnel would collapse to a single step,
// because only one event per user would carry an account at all.
func TestTheAccountFollowsAUserOntoTheirOtherEvents(t *testing.T) {
	evs := []event.Event{
		gev("u1", "signup", "acme", 0), // only this one carries the property
		gev("u1", "visit", "", 1),
		gev("u1", "buy", "", 2),
	}
	regrouped, g := Regroup(evs, "company")
	if len(regrouped) != 3 {
		t.Fatalf("want all 3 events attributed to acme, got %d (dropped %d)", len(regrouped), g.Dropped)
	}
	for _, e := range regrouped {
		if e.DistinctID != "acme" {
			t.Fatalf("event %q keyed to %q, want acme", e.Name, e.DistinctID)
		}
	}
	f := funnel.Compute(regrouped, []funnel.Step{{Event: "signup"}, {Event: "visit"}, {Event: "buy"}}, 7*24*time.Hour)
	if f.Steps[2].Count != 1 {
		t.Fatalf("the account should reach the last step, got %d", f.Steps[2].Count)
	}
}

// Retention at account grain is the B2B renewal question, and it is not the user question: an
// account is retained if ANYONE comes back, so churn at the seat level is invisible to it and
// churn at the account level is what actually costs money.
func TestAnAccountIsRetainedWhenAnyoneComesBack(t *testing.T) {
	evs := []event.Event{
		// Two people at acme in week 0; a DIFFERENT one returns in week 1.
		gev("u1", "visit", "acme", 0),
		gev("u2", "visit", "acme", 0),
		gev("u2", "visit", "acme", 7),
		// solo never returns.
		gev("u3", "visit", "solo", 0),
	}
	regrouped, _ := Regroup(evs, "company")
	r := retention.ComputeBucketed(regrouped, 2, "visit", "week", false)
	if len(r.Cohorts) == 0 {
		t.Fatal("no cohorts computed")
	}
	c := r.Cohorts[0]
	if c.Size != 2 {
		t.Fatalf("week 0 cohort should be 2 ACCOUNTS, got %d", c.Size)
	}
	if len(c.Returned) < 2 || c.Returned[1] != 1 {
		t.Fatalf("acme should be retained in week 1 via a different person, got %v", c.Returned)
	}
}

// Events belonging to nobody are excluded and SAID SO. Silently computing an account funnel over
// a third of the traffic is the truncated-denominator failure this engine exists to not have.
func TestUnattributedTrafficIsExcludedOutLoud(t *testing.T) {
	evs := []event.Event{
		gev("u1", "visit", "acme", 0),
		gev("anon1", "visit", "", 0),
		gev("anon2", "visit", "", 0),
		gev("anon3", "visit", "", 0),
	}
	_, g := Regroup(evs, "company")
	if g.Kept != 1 || g.Dropped != 3 {
		t.Fatalf("want 1 kept / 3 dropped, got %d/%d", g.Kept, g.Dropped)
	}
	if g.Unattributed != 3 {
		t.Fatalf("want 3 users with no account, got %d", g.Unattributed)
	}
	if g.Note == "" {
		t.Fatal("majority of events dropped and the result said nothing about it")
	}
	// The number in the note has to be the real coverage, not a vibe.
	if want := "only 25% of events"; len(g.Note) < len(want) || g.Note[:len(want)] != want {
		t.Fatalf("note should open with the measured coverage, got %q", g.Note)
	}
}

// Instrumenting nothing must read as instrumenting nothing, not as an empty product.
func TestAPropertyNobodySendsSaysHowToSendIt(t *testing.T) {
	evs := []event.Event{gev("u1", "visit", "", 0)}
	out, g := Regroup(evs, "company")
	if len(out) != 0 || g.Groups != 0 {
		t.Fatalf("nothing carries the property, want an empty regroup, got %d events / %d groups", len(out), g.Groups)
	}
	if g.Note == "" {
		t.Fatal("a totally uninstrumented group property should explain itself")
	}
}

// Two accounts whose names differ only by type must not merge. valueOf stringifies, so account
// 42 (number) and "42" (string) land in the same bucket — which is correct, they ARE the same
// account written two ways by two SDKs, and the alternative is one customer showing up twice.
func TestNumericAndStringAccountIdsAreTheSameAccount(t *testing.T) {
	e1 := gev("u1", "visit", "", 0)
	e1.Properties["company"] = 42
	e2 := gev("u2", "visit", "42", 0)
	_, g := Regroup([]event.Event{e1, e2}, "company")
	if g.Groups != 1 {
		t.Fatalf("42 and \"42\" are one account, got %d", g.Groups)
	}
}

func TestActiveAccountsCountAccountsNotSeats(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	evs := []event.Event{
		gev("u1", "visit", "acme", 0),
		gev("u2", "visit", "acme", 0),
		gev("u3", "visit", "acme", 0),
		gev("u4", "visit", "solo", 30),
	}
	regrouped, _ := Regroup(evs, "company")
	if n := ActiveWithin(regrouped, base.AddDate(0, 0, -1), base.AddDate(0, 0, 7)); n != 1 {
		t.Fatalf("three seats at one account is ONE active account, got %d", n)
	}
}

// Re-keying must not change what the underlying events say. If Regroup mutated the input, the
// user-grain report rendered next to the account-grain one on the same page would silently
// become an account-grain report too.
func TestRegroupDoesNotMutateTheCallersEvents(t *testing.T) {
	evs := []event.Event{gev("u1", "signup", "acme", 0), gev("u1", "visit", "", 1)}
	Regroup(evs, "company")
	if evs[0].DistinctID != "u1" || evs[1].DistinctID != "u1" {
		t.Fatalf("input was re-keyed in place: %q, %q", evs[0].DistinctID, evs[1].DistinctID)
	}
	if _, ok := evs[1].Properties["company"]; ok {
		t.Fatal("first-touch stamping leaked into the caller's event")
	}
}
