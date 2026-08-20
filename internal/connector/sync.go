package connector

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// The part that makes "we read your existing analytics" a true sentence.
//
// internal/importer/remote.go is a one-shot, human-triggered, key-in-hand backfill: no ticker, no
// watermark, no second run. Everything in this file is the difference between a migration tool and
// an integration — it keeps reading, it knows where it got to, and it can be re-run at any moment
// without changing a number.
//
// FOUR PROPERTIES, and each one is a specific way this goes wrong without it:
//
//  1. WATERMARK. Where the last successful read got to, in whole UTC days. Without it every tick
//     re-reads ninety days and the rate-limit budget is spent on data that has not changed.
//  2. OVERLAP. Every tick re-reads the last few days anyway. Every vendor has late-arriving
//     events — a mobile SDK flushing after a flight, a server retrying — and a watermark that
//     only ever moves forward freezes yesterday at whatever it was at midnight.
//  3. DEDUPE BY KEY, NOT BY APPEND. Which is why (2) is safe. A day is a key; yesterday fetched
//     again REPLACES yesterday. Appending would make an hourly sync report 24× traffic, and — the
//     part that makes it dangerous rather than merely wrong — it would look like growth.
//  4. BACKFILL IN CHUNKS, RESUMABLE. The first run wants ninety days. One query for ninety days is
//     the largest thing this ever asks a vendor for, and a failure at day 88 must not start over.
type Store interface {
	// Load returns the buckets held and the watermark: the last UTC day fully read.
	Load(ctx context.Context, vendor, project string) ([]Bucket, time.Time, error)
	// Save replaces the held buckets and advances the watermark. It is called once per chunk, so
	// a backfill interrupted halfway resumes from the last saved chunk rather than the beginning.
	//
	// IT NEVER RECEIVES A CREDENTIAL. The key lives in a Credential inside the Fetcher for the
	// life of the process and is never handed to anything that writes.
	Save(ctx context.Context, vendor, project string, buckets []Bucket, watermark time.Time) error
}

// MemStore is a Store for tests and for a single-process instance that re-backfills on boot.
type MemStore struct {
	mu   sync.Mutex
	data map[string][]Bucket
	mark map[string]time.Time
}

func NewMemStore() *MemStore {
	return &MemStore{data: map[string][]Bucket{}, mark: map[string]time.Time{}}
}

func (m *MemStore) Load(_ context.Context, vendor, project string) ([]Bucket, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := vendor + "\x00" + project
	return append([]Bucket(nil), m.data[k]...), m.mark[k], nil
}

func (m *MemStore) Save(_ context.Context, vendor, project string, b []Bucket, w time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := vendor + "\x00" + project
	m.data[k] = append([]Bucket(nil), b...)
	m.mark[k] = w
	return nil
}

// SyncOpts configures the poller.
type SyncOpts struct {
	Project string
	// Window is how much history to hold. Default 90 days — three months is enough for the
	// quarter-level read and small enough that a full backfill is a handful of queries.
	Window int
	// Overlap is how many trailing days to re-read on every tick, regardless of the watermark.
	// Default 3. See property (2) above.
	Overlap int
	// Chunk is how many days one backfill query covers. Default 30.
	Chunk int
	// Interval is the ticker period. Default 1 hour.
	Interval time.Duration
	// Jitter spreads many projects' ticks so they do not all hit a vendor on the hour. Default
	// 10% of Interval.
	Jitter time.Duration
	Now    func() time.Time
	Rand   *rand.Rand
}

func (o SyncOpts) withDefaults() SyncOpts {
	if o.Window <= 0 {
		o.Window = 90
	}
	if o.Overlap <= 0 {
		o.Overlap = 3
	}
	if o.Chunk <= 0 {
		o.Chunk = 30
	}
	if o.Interval <= 0 {
		o.Interval = time.Hour
	}
	if o.Jitter <= 0 {
		o.Jitter = o.Interval / 10
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return o
}

// Syncer keeps one project's aggregate up to date.
type Syncer struct {
	Fetcher Fetcher
	Store   Store
	Opts    SyncOpts

	mu      sync.Mutex // single-flight: a slow read must never overlap the next tick
	backoff time.Duration
}

// Sync performs one read. Safe to call at any time, from anywhere, as often as you like — that is
// property (3), and it is what makes the ticker below boring.
func (s *Syncer) Sync(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.Opts.withDefaults()

	now := o.Now().UTC()
	to := now.Truncate(24*time.Hour).AddDate(0, 0, 1) // through the end of today, exclusive
	oldest := to.AddDate(0, 0, -o.Window)

	held, mark, err := s.Store.Load(ctx, s.Fetcher.Vendor(), o.Project)
	if err != nil {
		return 0, fmt.Errorf("reading the held aggregate: %w", err)
	}

	from := oldest
	if !mark.IsZero() {
		// Resume from the watermark, minus the overlap. The overlap is what lets a late event
		// correct yesterday's number instead of being lost behind a watermark that only advances.
		if r := mark.UTC().Truncate(24*time.Hour).AddDate(0, 0, -o.Overlap); r.After(from) {
			from = r
		}
	}
	if !from.Before(to) {
		return 0, nil
	}

	fetched := 0
	for start := from; start.Before(to); start = start.AddDate(0, 0, o.Chunk) {
		end := start.AddDate(0, 0, o.Chunk)
		if end.After(to) {
			end = to
		}
		fresh, err := s.Fetcher.PhaseA(ctx, start, end)
		if err != nil {
			// Save what the earlier chunks got and leave the watermark where they reached, so the
			// next tick resumes there. A backfill that starts over on every failure never finishes
			// on a project big enough to be worth connecting.
			if fetched > 0 {
				_ = s.Store.Save(ctx, s.Fetcher.Vendor(), o.Project, Trim(held, oldest, to), start)
			}
			return fetched, err
		}
		held = Merge(held, fresh)
		fetched += len(fresh)
		if err := s.Store.Save(ctx, s.Fetcher.Vendor(), o.Project, Trim(held, oldest, to), end); err != nil {
			return fetched, fmt.Errorf("saving the aggregate: %w", err)
		}
	}
	return fetched, nil
}

// Run ticks until the context is cancelled.
//
// Errors do not stop it and are not swallowed: they go to onErr and the next attempt backs off
// exponentially to a cap. A vendor being rate-limited or briefly down is the normal case for a
// long-running poller, and a sync loop that dies on the first 429 is a connector that silently
// stops working a week after someone sets it up.
func (s *Syncer) Run(ctx context.Context, onErr func(error)) {
	o := s.Opts.withDefaults()
	rng := o.Rand
	if rng == nil {
		rng = rand.New(rand.NewSource(o.Now().UnixNano()))
	}
	for {
		if _, err := s.Sync(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if onErr != nil {
				onErr(err)
			}
			s.backoff = nextBackoff(s.backoff, o.Interval)
		} else {
			s.backoff = 0
		}

		wait := o.Interval
		if s.backoff > 0 {
			wait = s.backoff
		}
		if o.Jitter > 0 {
			wait += time.Duration(rng.Int63n(int64(o.Jitter)))
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// nextBackoff doubles from a minute, capped at the tick interval — past which backing off further
// only means the data is staler, since the next scheduled tick would have run anyway.
func nextBackoff(cur, cap time.Duration) time.Duration {
	if cur <= 0 {
		cur = time.Minute
	} else {
		cur *= 2
	}
	if cur > cap {
		cur = cap
	}
	return cur
}

// Trim drops buckets outside the retention window, so a long-lived instance does not accumulate
// history nobody reads. [from, to).
func Trim(bs []Bucket, from, to time.Time) []Bucket {
	out := make([]Bucket, 0, len(bs))
	for _, b := range bs {
		d := b.Day.UTC()
		if d.Before(from) || !d.Before(to) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Limiter is a token bucket sized to a vendor's PUBLISHED ceiling.
//
// Client-side, ahead of the request, rather than reacting to a 429. A 429 from PostHog is a read
// that did not happen and a tick that produced nothing; staying under the published limit means
// the sync is never the reason the customer's own dashboard is throttled — which matters more than
// our schedule, because it is their key and their quota.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration // minimum spacing between requests
	burst    int
	tokens   int
	last     time.Time
	now      func() time.Time
}

// NewLimiter allows n requests per period, with a burst of n.
func NewLimiter(n int, period time.Duration) *Limiter {
	if n <= 0 {
		n = 1
	}
	return &Limiter{interval: period / time.Duration(n), burst: n, tokens: n, now: time.Now}
}

// DefaultPostHogLimiter is PostHog's documented analytics limit: 240 requests per minute
// (posthog.com/docs/api#rate-limiting), which a sync never comes close to — one query per tick,
// plus two per finding.
func DefaultPostHogLimiter() *Limiter { return NewLimiter(240, time.Minute) }

// Wait blocks until a token is available or ctx ends.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		if !l.last.IsZero() {
			if gained := int(now.Sub(l.last) / l.interval); gained > 0 {
				l.tokens += gained
				if l.tokens > l.burst {
					l.tokens = l.burst
				}
				l.last = l.last.Add(time.Duration(gained) * l.interval)
			}
		} else {
			l.last = now
		}
		if l.tokens > 0 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		wait := l.interval - now.Sub(l.last)
		l.mu.Unlock()
		if wait <= 0 {
			wait = l.interval
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.New("cancelled while waiting on the vendor rate limit")
		case <-t.C:
		}
	}
}
