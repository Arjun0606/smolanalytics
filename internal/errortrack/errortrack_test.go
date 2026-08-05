package errortrack

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

func exc(user, msg, src string, line int, at time.Time, path string) event.Event {
	return event.Event{Name: ExceptionEvent, DistinctID: user, Timestamp: at,
		Properties: map[string]any{"message": msg, "source": src, "lineno": float64(line), "path": path}}
}

// The failure mode that makes error trackers unusable: every occurrence its own row. "User 12345
// not found" and "User 67890 not found" are one bug.
func TestVaryingIdentifiersGroupTogether(t *testing.T) {
	same := []string{
		"User 12345 not found",
		"User 67890 not found",
		"User 2 not found",
	}
	fp := Fingerprint(same[0], "app.js", 42)
	for _, m := range same[1:] {
		if got := Fingerprint(m, "app.js", 42); got != fp {
			t.Errorf("%q fingerprinted as %s, want the same group as %q (%s)", m, got, same[0], fp)
		}
	}
	// ...but the opposite mistake is worse because it is silent: genuinely different failures
	// must not merge into one unactionable row.
	if Fingerprint("User 1 not found", "app.js", 42) == Fingerprint("Payment declined", "app.js", 42) {
		t.Error("two unrelated errors share a fingerprint")
	}
	// same message, different file = different bug. Merging them produces a row nobody can fix.
	if Fingerprint("Cannot read properties of undefined", "checkout.js", 10) ==
		Fingerprint("Cannot read properties of undefined", "profile.js", 10) {
		t.Error("the same generic message from two files was merged")
	}
	// URLs and quoted values vary per environment and must not split a group
	if Fingerprint("fetch failed for https://api.example.com/v1/a", "", 0) !=
		Fingerprint("fetch failed for https://api.example.com/v1/b", "", 0) {
		t.Error("two requests to the same API were treated as different bugs")
	}
}

// One user in a retry loop can generate ten thousand events. Ranking by count puts them at the
// top while a bug hitting a quarter of your customers sits below the fold.
func TestRankingIsByPeopleNotOccurrences(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	var evs []event.Event
	// one user, 500 occurrences of a loud error
	for i := 0; i < 500; i++ {
		evs = append(evs, exc("loop-user", "Retry failed", "poll.js", 7, from.Add(time.Minute), "/"))
	}
	// forty users, one occurrence each of a quiet one
	for i := 0; i < 40; i++ {
		evs = append(evs, exc(fmt.Sprintf("u%d", i), "Checkout blew up", "checkout.js", 3, from.Add(time.Hour), "/checkout"))
	}
	r := Compute(evs, from, now, time.Time{})
	if len(r.Groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(r.Groups))
	}
	if r.Groups[0].Users != 40 {
		t.Errorf("the top row has %d users — a single user's retry loop is outranking a bug that "+
			"hit forty people", r.Groups[0].Users)
	}
	if r.Groups[1].Count != 500 || r.Groups[1].Users != 1 {
		t.Errorf("second row = %d occurrences from %d users, want 500 from 1", r.Groups[1].Count, r.Groups[1].Users)
	}
}

// A regression and a long-standing annoyance need opposite reactions.
func TestNewErrorsAreDistinguishedFromKnownOnes(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	prior := from.Add(-24 * time.Hour)
	evs := []event.Event{
		exc("a", "Old friend", "x.js", 1, prior.Add(time.Hour), "/"), // before the window
		exc("b", "Old friend", "x.js", 1, from.Add(time.Hour), "/"),  // and inside it
		exc("c", "Brand new", "y.js", 2, from.Add(2*time.Hour), "/"), // only inside
	}
	r := Compute(evs, from, now, prior)
	byTitle := map[string]Group{}
	for _, g := range r.Groups {
		byTitle[g.Title] = g
	}
	if !byTitle["Brand new"].New {
		t.Error("an error first seen in this window is not marked new")
	}
	if byTitle["Old friend"].New {
		t.Error("an error that predates the window is marked new")
	}
	// the pre-window occurrence must NOT inflate the window's counts
	if got := byTitle["Old friend"].Count; got != 1 {
		t.Errorf("count = %d, want 1 — the prior-window occurrence was counted in this window", got)
	}
}

// The crash-free rate needs everyone active as its denominator, not only people who errored.
func TestCrashFreeRateUsesEveryoneActive(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	var evs []event.Event
	for i := 0; i < 90; i++ { // 90 happy users
		evs = append(evs, event.Event{Name: "$pageview", DistinctID: fmt.Sprintf("ok%d", i), Timestamp: from.Add(time.Hour)})
	}
	for i := 0; i < 10; i++ { // 10 who hit an error
		u := fmt.Sprintf("bad%d", i)
		evs = append(evs, event.Event{Name: "$pageview", DistinctID: u, Timestamp: from.Add(time.Hour)})
		evs = append(evs, exc(u, "Boom", "a.js", 1, from.Add(2*time.Hour), "/"))
	}
	r := Compute(evs, from, now, time.Time{})
	if r.Users != 100 || r.UsersAffected != 10 {
		t.Fatalf("users=%d affected=%d, want 100 and 10", r.Users, r.UsersAffected)
	}
	if r.CrashFreePct != 90 {
		t.Errorf("crash-free = %v%%, want 90", r.CrashFreePct)
	}
	// an empty window must not report a perfect score
	empty := Compute(nil, from, now, time.Time{})
	if empty.CrashFreePct != 0 || empty.Note == "" {
		t.Errorf("an empty window reported %v%% crash-free with note %q — a perfect score off no "+
			"data is the kind of number someone screenshots", empty.CrashFreePct, empty.Note)
	}
}

// The join Sentry cannot make: what did this bug actually cost.
func TestFunnelImpactComparesAffectedAgainstClean(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour)
	steps := []funnel.Step{{Event: "signup"}, {Event: "checkout"}}
	var evs []event.Event
	// 60 clean users, 50 of them convert
	for i := 0; i < 60; i++ {
		u := fmt.Sprintf("clean%d", i)
		evs = append(evs, event.Event{Name: "signup", DistinctID: u, Timestamp: from.Add(time.Hour)})
		if i < 50 {
			evs = append(evs, event.Event{Name: "checkout", DistinctID: u, Timestamp: from.Add(2 * time.Hour)})
		}
	}
	// 40 who hit an error, only 8 convert
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("hit%d", i)
		evs = append(evs, event.Event{Name: "signup", DistinctID: u, Timestamp: from.Add(time.Hour)})
		evs = append(evs, exc(u, "Checkout blew up", "checkout.js", 3, from.Add(90*time.Minute), "/checkout"))
		if i < 8 {
			evs = append(evs, event.Event{Name: "checkout", DistinctID: u, Timestamp: from.Add(2 * time.Hour)})
		}
	}
	r := Compute(evs, from, now, time.Time{})
	im := FunnelImpact(evs, r.Groups, steps, 24*time.Hour)
	if len(im) != 1 {
		t.Fatalf("got %d impacts, want 1", len(im))
	}
	got := im[0]
	if got.Thin {
		t.Fatalf("40 vs 60 users was called too thin to compare: %s", got.Read)
	}
	if got.AffectedUsers != 40 || got.CleanUsers != 60 {
		t.Errorf("split = %d affected / %d clean, want 40/60", got.AffectedUsers, got.CleanUsers)
	}
	if got.GapPct < 50 {
		t.Errorf("gap = %v points; 20%% against 83%% should be a large measured gap", got.GapPct)
	}
	// and it must NOT claim causation
	if !contains(got.Read, "Associated, not proven") {
		t.Errorf("the read claims more than a comparison supports: %q", got.Read)
	}
}

// A comparison on a handful of people is a coin flip, and a ranked list of coin flips reads
// exactly like a ranked list of findings.
func TestThinComparisonsSaySoAndSinkToTheBottom(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour)
	steps := []funnel.Step{{Event: "signup"}, {Event: "checkout"}}
	var evs []event.Event
	for i := 0; i < 3; i++ { // 3 people hit a rare error
		u := fmt.Sprintf("rare%d", i)
		evs = append(evs, event.Event{Name: "signup", DistinctID: u, Timestamp: from.Add(time.Hour)})
		evs = append(evs, exc(u, "Rare thing", "z.js", 9, from.Add(90*time.Minute), "/"))
	}
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("ok%d", i)
		evs = append(evs, event.Event{Name: "signup", DistinctID: u, Timestamp: from.Add(time.Hour)})
		evs = append(evs, event.Event{Name: "checkout", DistinctID: u, Timestamp: from.Add(2 * time.Hour)})
	}
	r := Compute(evs, from, now, time.Time{})
	im := FunnelImpact(evs, r.Groups, steps, 24*time.Hour)
	if len(im) == 0 {
		t.Fatal("the rare error vanished instead of saying it cannot be judged yet")
	}
	if !im[0].Thin {
		t.Errorf("a 3-user comparison was presented as a finding: %+v", im[0])
	}
	if !contains(im[0].Read, "too few") {
		t.Errorf("read does not say why it cannot be judged: %q", im[0].Read)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
