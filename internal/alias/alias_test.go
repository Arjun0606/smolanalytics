package alias

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func ev(id, user, name string, ts time.Time) event.Event {
	return event.Event{ID: id, DistinctID: user, Name: name, Timestamp: ts}
}

// The whole point: a funnel crossing the login boundary counts as ONE user.
func TestStitchingJoinsTheJourney(t *testing.T) {
	base := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	st := memory.New()
	// anonymous browsing → signup under the real account id
	_ = st.Ingest(
		ev("1", "a-xyz", "$pageview", base),
		ev("2", "a-xyz", "view_pricing", base.Add(time.Minute)),
		ev("3", "u42", "signup", base.Add(2*time.Minute)),
	)
	am, _ := Open(filepath.Join(t.TempDir(), "aliases.json"))
	ws := Wrap(st, am)

	// before stitching: two separate users, funnel broken
	fr := funnel.Compute(mustRange(t, ws), []funnel.Step{{Event: "view_pricing"}, {Event: "signup"}}, time.Hour)
	if fr.Steps[1].Count != 0 {
		t.Fatal("precondition: unstitched funnel should not convert")
	}

	// identify() breadcrumb lands → journey joins
	if err := am.Add("a-xyz", "u42"); err != nil {
		t.Fatal(err)
	}
	fr = funnel.Compute(mustRange(t, ws), []funnel.Step{{Event: "view_pricing"}, {Event: "signup"}}, time.Hour)
	if fr.Steps[0].Count != 1 || fr.Steps[1].Count != 1 {
		t.Fatalf("stitched funnel should convert 1/1, got %d/%d", fr.Steps[0].Count, fr.Steps[1].Count)
	}

	// GDPR erasure by account id takes the anonymous trail with it
	n, err := ws.DeleteUser("u42")
	if err != nil || n != 3 {
		t.Fatalf("erasure should remove all 3 events across ids, got %d (%v)", n, err)
	}
	if left := mustRange(t, ws); len(left) != 0 {
		t.Fatalf("nothing should remain, got %d", len(left))
	}
	if got := am.Resolve("a-xyz"); got != "a-xyz" {
		t.Fatal("aliases must be forgotten after erasure")
	}
}

// The cookieless sentinel and self/empty pairs must never create aliases —
// stitching "$anon" would merge every cookieless visitor into one account.
func TestStitchingGuards(t *testing.T) {
	am, _ := Open("")
	_ = am.Add("$anon", "u1")
	_ = am.Add("", "u1")
	_ = am.Add("u1", "u1")
	if am.Resolve("$anon") != "$anon" || len(am.AliasesOf("u1")) != 0 {
		t.Fatal("guarded pairs must not stitch")
	}
	// no chains: aliasing to an aliased id collapses to its canonical
	_ = am.Add("a-1", "u1")
	_ = am.Add("a-2", "a-1") // a-1 is an alias, not a canonical
	if am.Resolve("a-2") != "u1" {
		t.Fatalf("chain must collapse to canonical, got %q", am.Resolve("a-2"))
	}
}

func mustRange(t *testing.T, s *Store) []event.Event {
	t.Helper()
	evs, err := s.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}
