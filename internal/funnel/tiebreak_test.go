package funnel

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The matcher walks the sorted slice and treats "later in the slice" as "later in time". The
// sort was a plain stable sort on timestamp, so events sharing a millisecond kept whatever order
// storage happened to yield — and the same signup+checkout returned converted=1 or converted=0
// depending on nothing but how the bytes came off disk. An import, a backfill, a DeleteUser
// segment rewrite or a compaction could flip a funnel with no data change whatsoever.
func TestFunnelIsIndependentOfStorageOrderUnderTiedTimestamps(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	steps := []Step{{Event: "signup"}, {Event: "checkout"}}

	signup := event.Event{ID: "e1", Name: "signup", DistinctID: "u", Timestamp: at}
	checkout := event.Event{ID: "e2", Name: "checkout", DistinctID: "u", Timestamp: at}

	forward := Compute([]event.Event{signup, checkout}, steps, time.Hour)
	reversed := Compute([]event.Event{checkout, signup}, steps, time.Hour)

	if forward.Converted != reversed.Converted {
		t.Errorf("the same two events at the identical instant give converted=%d in one storage "+
			"order and converted=%d in the other", forward.Converted, reversed.Converted)
	}
	// and the answer has to be the one consistent with the funnel's own definition: if signup
	// and checkout carry the same instant, the sequence that makes sense is signup then checkout
	if forward.Converted != 1 {
		t.Errorf("converted=%d, want 1 — a tie should read in step order, not be discarded", forward.Converted)
	}
}

// The same guarantee has to hold for a longer funnel with non-step events tied in among the
// steps, which is what a batched import actually looks like.
func TestTiedBatchImportConvertsRegardlessOfOrder(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	steps := []Step{{Event: "a"}, {Event: "b"}, {Event: "c"}}
	mk := func(id, name string) event.Event {
		return event.Event{ID: id, Name: name, DistinctID: "u", Timestamp: at}
	}
	// every permutation of a batch that all landed in the same millisecond
	orders := [][]event.Event{
		{mk("1", "a"), mk("2", "noise"), mk("3", "b"), mk("4", "c")},
		{mk("4", "c"), mk("3", "b"), mk("2", "noise"), mk("1", "a")},
		{mk("3", "b"), mk("1", "a"), mk("4", "c"), mk("2", "noise")},
		{mk("2", "noise"), mk("4", "c"), mk("1", "a"), mk("3", "b")},
	}
	var got []int
	for _, o := range orders {
		got = append(got, Compute(o, steps, time.Hour).Converted)
	}
	for i := range got {
		if got[i] != got[0] {
			t.Fatalf("converted differs by input order: %v", got)
		}
	}
	if got[0] != 1 {
		t.Errorf("converted=%d, want 1 — a,b,c all present in the batch in step order", got[0])
	}
}

// sortForFunnel must be a TOTAL order: no pair may compare equal in both directions, or the
// sort itself is free to differ between runs.
func TestFunnelSortIsATotalOrder(t *testing.T) {
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	steps := []Step{{Event: "a"}, {Event: "b"}}
	evs := []event.Event{
		{ID: "x", Name: "a", DistinctID: "u", Timestamp: at},
		{ID: "y", Name: "a", DistinctID: "u", Timestamp: at}, // same step, same instant
		{ID: "z", Name: "zzz", DistinctID: "u", Timestamp: at},
	}
	first := append([]event.Event(nil), evs...)
	second := []event.Event{evs[2], evs[1], evs[0]}
	sortForFunnel(first, steps)
	sortForFunnel(second, steps)
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("two input orders sort differently: %v vs %v",
				[]string{first[0].ID, first[1].ID, first[2].ID},
				[]string{second[0].ID, second[1].ID, second[2].ID})
		}
	}
}
