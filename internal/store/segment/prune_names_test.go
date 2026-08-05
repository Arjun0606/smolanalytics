package segment

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

// s.names is a cache of "which event names does this store still hold". DeleteUser rebuilds it
// and Clear resets it; Prune did neither, so an event whose only data aged out stayed in the
// list forever — it kept appearing in /v1/meta, in the chart and deploy pickers and in the MCP
// list_events tool, and choosing it drew an empty chart with nothing to explain why.
//
// Restarting the process "fixed" it, because Open rebuilds the set from the manifest. That is
// the signature of a stale cache rather than a storage bug, and it is why this survived: every
// test that opened a fresh store passed.
func TestPruneForgetsNamesItNoLongerHolds(t *testing.T) {
	dir := t.TempDir()
	b, err := blob.NewLocal(dir + "/cold")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir+"/hot.data", b, 2) // seal every 2 events
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -90)
	now := time.Now().UTC()

	// two old events seal into one cold segment holding ONLY legacy_event
	for _, e := range []event.Event{
		{ID: "a", Name: "legacy_event", DistinctID: "u1", Timestamp: old},
		{ID: "b", Name: "legacy_event", DistinctID: "u1", Timestamp: old},
		{ID: "c", Name: "current_event", DistinctID: "u1", Timestamp: now},
	} {
		if err := s.Ingest(e); err != nil {
			t.Fatal(err)
		}
	}

	before, _ := s.Names()
	if !contains(before, "legacy_event") {
		t.Fatalf("setup is wrong: legacy_event should be present before the prune, got %v", before)
	}

	if _, err := s.Prune(now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}

	after, _ := s.Names()
	if contains(after, "legacy_event") {
		t.Errorf("legacy_event is still listed after its only data was pruned: %v\n"+
			"every event picker in the product will offer it and draw an empty chart", after)
	}
	if !contains(after, "current_event") {
		t.Errorf("current_event was dropped by the rebuild — it still has data: %v", after)
	}

	// and the in-memory answer must match what a restart would compute, or the cache is
	// simply wrong in a different direction
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir+"/hot.data", b, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	fresh, _ := s2.Names()
	if !sameSet(after, fresh) {
		t.Errorf("live names %v disagree with a fresh open %v — the cache and the manifest have drifted", after, fresh)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	return true
}
