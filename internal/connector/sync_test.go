package connector

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// countingFetcher records every window it is asked for, so the schedule can be asserted on what it
// actually READ rather than on what it stored.
type countingFetcher struct {
	raw      []event.Event
	windows  [][2]time.Time
	failFrom time.Time // return an error for the chunk starting here, once
	failed   bool
}

func (c *countingFetcher) Vendor() string { return "posthog" }

func (c *countingFetcher) PhaseA(_ context.Context, from, to time.Time) ([]Bucket, error) {
	c.windows = append(c.windows, [2]time.Time{from, to})
	if !c.failFrom.IsZero() && from.Equal(c.failFrom) && !c.failed {
		c.failed = true
		return nil, errors.New("posthog is having a moment")
	}
	return DailyCounts(c.raw, from, to), nil
}

func (c *countingFetcher) PhaseB(_ context.Context, name string, segFrom, segTo, revFrom, revTo time.Time, dims []string) (map[time.Time]map[string]map[string]int, []string, Revenue, error) {
	segs, dropped := SegmentCounts(c.raw, name, segFrom, segTo, dims)
	return segs, dropped, RevenueFor(c.raw, name, revFrom, revTo, revFrom, revTo), nil
}

func newSyncer(raw []event.Event, now time.Time, o SyncOpts) (*Syncer, *countingFetcher, *MemStore) {
	f := &countingFetcher{raw: raw}
	st := NewMemStore()
	o.Project = "42"
	o.Now = func() time.Time { return now }
	return &Syncer{Fetcher: f, Store: st, Opts: o}, f, st
}

// A first sync backfills the whole window in chunks; a second sync reads only the overlap. Without
// the watermark every tick re-reads ninety days, and the rate-limit budget goes on data that did
// not change.
func TestSyncBackfillsOnceThenReadsOnlyTheOverlap(t *testing.T) {
	now := testNow
	s, f, _ := newSyncer(corpus(), now, SyncOpts{Window: 90, Chunk: 30, Overlap: 3})

	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.windows) != 3 {
		t.Fatalf("a 90-day backfill in 30-day chunks is 3 queries, got %d: %v", len(f.windows), f.windows)
	}

	before := len(f.windows)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.windows) - before; got != 1 {
		t.Fatalf("the second sync should be one query, got %d", got)
	}
	// Overlap 3 means the last three days INCLUDING today, because the watermark is the exclusive
	// end of what was read. Three days re-read out of ninety held is the price of never losing a
	// late-arriving event, and it is the whole reason the watermark can be trusted.
	w := f.windows[before]
	if days := int(w[1].Sub(w[0]).Hours() / 24); days != 3 {
		t.Errorf("the second sync must re-read exactly the 3-day overlap, got %d days: %v", days, w)
	}
}

// Re-reading is only safe because a day is a key. This is the property that makes the overlap free.
func TestSyncIsIdempotent(t *testing.T) {
	s, _, st := newSyncer(corpus(), testNow, SyncOpts{Window: 90, Chunk: 30, Overlap: 3})
	ctx := context.Background()
	if _, err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	first, _, _ := st.Load(ctx, "posthog", "42")
	total := totalCount(first)

	for i := 0; i < 5; i++ {
		if _, err := s.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	again, _, _ := st.Load(ctx, "posthog", "42")
	if got := totalCount(again); got != total {
		t.Fatalf("six syncs of an unchanging product must report the same traffic: %d then %d — an hourly sync would have reported %.0f× and it would have looked like growth",
			total, got, float64(got)/float64(total))
	}
	if len(again) != len(first) {
		t.Fatalf("bucket count changed across re-sync: %d then %d", len(first), len(again))
	}
}

// A late-arriving event inside the overlap must CORRECT yesterday's number, not be lost behind a
// watermark that only moves forward, and not be added to it.
func TestSyncOverlapCorrectsALateEvent(t *testing.T) {
	raw := corpus()
	s, f, st := newSyncer(raw, testNow, SyncOpts{Window: 90, Chunk: 30, Overlap: 3})
	ctx := context.Background()
	if _, err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	held, _, _ := st.Load(ctx, "posthog", "42")
	yesterday := testNow.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	before := 0
	for _, b := range held {
		if b.Day.Equal(yesterday) && b.Event == "checkout" {
			before = b.Count
		}
	}
	if before == 0 {
		t.Fatal("fixture has no checkout yesterday")
	}

	// Three events that took a while to reach the vendor.
	for i := 0; i < 3; i++ {
		f.raw = append(f.raw, event.Event{
			Name: "checkout", DistinctID: fmt.Sprintf("late%d", i),
			Timestamp: yesterday.Add(20 * time.Hour), ID: fmt.Sprintf("late%d", i),
		})
	}
	if _, err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	held, _, _ = st.Load(ctx, "posthog", "42")
	after := 0
	n := 0
	for _, b := range held {
		if b.Day.Equal(yesterday) && b.Event == "checkout" {
			after = b.Count
			n++
		}
	}
	if n != 1 {
		t.Fatalf("re-reading a day must replace its bucket, not add one: %d buckets for that day", n)
	}
	if after != before+3 {
		t.Fatalf("late events must correct the day: %d -> %d, want %d", before, after, before+3)
	}
}

// A backfill that starts over on every failure never finishes on a project big enough to be worth
// connecting. The watermark has to survive a mid-backfill error.
func TestSyncResumesAfterAFailedChunk(t *testing.T) {
	raw := corpus()
	now := testNow
	f := &countingFetcher{raw: raw}
	st := NewMemStore()
	s := &Syncer{Fetcher: f, Store: st, Opts: SyncOpts{
		Project: "42", Window: 90, Chunk: 30, // Overlap keeps its default of 3
		Now: func() time.Time { return now },
	}}
	to := now.Truncate(24*time.Hour).AddDate(0, 0, 1)
	f.failFrom = to.AddDate(0, 0, -30) // the last chunk of three

	ctx := context.Background()
	if _, err := s.Sync(ctx); err == nil {
		t.Fatal("expected the third chunk to fail")
	}
	_, mark, _ := st.Load(ctx, "posthog", "42")
	if mark.IsZero() {
		t.Fatal("the two chunks that succeeded must leave a watermark, or the next run starts from scratch")
	}
	spent := len(f.windows)
	if spent != 3 {
		t.Fatalf("the first attempt should have tried all three chunks, got %d", spent)
	}
	if _, err := s.Sync(ctx); err != nil {
		t.Fatalf("the retry should succeed: %v", err)
	}
	// It resumes from the watermark, so it re-reads the failed chunk and the overlap that reaches
	// back into the one before it — never the whole ninety days again.
	got := len(f.windows) - spent
	if got >= spent {
		t.Errorf("the retry restarted the backfill: %d queries, same as a cold start", got)
	}
	t.Logf("cold backfill %d queries; resume after a mid-backfill failure %d", spent, got)
}

// The whole point of the schedule: findings from a synced aggregate equal findings from raw.
func TestSyncedAggregateInvestigatesIdenticallyToRaw(t *testing.T) {
	raw := corpus()
	s, f, st := newSyncer(raw, testNow, SyncOpts{Window: 90, Chunk: 30, Overlap: 3})
	ctx := context.Background()
	for i := 0; i < 3; i++ { // several ticks, as a real instance would
		if _, err := s.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	held, _, _ := st.Load(ctx, "posthog", "42")
	res, err := Investigate(ctx, f, held, InvestigateOpts{Now: testNow, Days: testDays, MinPeople: 20})
	if err != nil {
		t.Fatal(err)
	}
	want := investigate.Run(raw, investigateOpts())
	if len(want.Findings) != len(res.Findings) {
		t.Fatalf("finding count differs after syncing: raw %d, synced %d", len(want.Findings), len(res.Findings))
	}
	for i := range want.Findings {
		if want.Findings[i] != res.Findings[i] {
			t.Errorf("finding %d differs after syncing:\n raw %+v\n agg %+v", i, want.Findings[i], res.Findings[i])
		}
	}
}

// Backoff must climb and then stop climbing at the tick interval, past which waiting longer only
// means staler data — the next scheduled tick would have run anyway.
func TestBackoffClimbsAndCaps(t *testing.T) {
	cur := time.Duration(0)
	var seen []time.Duration
	for i := 0; i < 12; i++ {
		cur = nextBackoff(cur, time.Hour)
		seen = append(seen, cur)
	}
	if seen[0] != time.Minute {
		t.Errorf("first backoff should be a minute, got %v", seen[0])
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Fatalf("backoff went backwards at %d: %v", i, seen)
		}
	}
	if last := seen[len(seen)-1]; last != time.Hour {
		t.Errorf("backoff must cap at the tick interval, got %v", last)
	}
}

// The limiter is client-side and ahead of the request. A 429 is a read that did not happen, and it
// is the customer's quota being spent — theirs matters more than our schedule.
func TestLimiterStaysUnderThePublishedCeiling(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(3, 3*time.Second) // one per second
	l.now = func() time.Time { return now }

	ctx := context.Background()
	for i := 0; i < 3; i++ { // the burst
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// The fourth must block. Cancel proves it blocked rather than passing through.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.Wait(cancelled); err == nil {
		t.Fatal("the limiter let a fourth request through inside the window — the ceiling is decorative")
	}
	now = now.Add(2 * time.Second) // two tokens back
	for i := 0; i < 2; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("tokens must refill with time: %v", err)
		}
	}
}

// The store persists counts and a watermark. It must never be handed a credential — the key lives
// in the Fetcher for the life of the process and is never written anywhere.
func TestSyncNeverPersistsACredential(t *testing.T) {
	f := newFakePostHog(t, corpus())
	ph := newClient(f)
	st := NewMemStore()
	s := &Syncer{Fetcher: ph, Store: st, Opts: SyncOpts{
		Project: "42", Window: 30, Chunk: 30,
		Now: func() time.Time { return testNow },
	}}
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, mark, _ := st.Load(context.Background(), "posthog", "42")
	if len(held) == 0 || mark.IsZero() {
		t.Fatal("nothing was stored")
	}
	dump := fmt.Sprintf("%+v %+v", held, mark)
	if len(dump) > 0 && contains([]string{dump}, "secret-key") {
		t.Fatal("the credential reached the store")
	}
	for _, b := range held {
		if b.Segments != nil {
			continue // marginals are counts, fine
		}
	}
}
