// Package retention computes cohort retention — the other core product-analytics
// primitive: group users by the day they first showed up, then track what % come
// back on day 1, 2, ... N. Deterministic and storage-agnostic, like funnel.
package retention

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Cohort is one first-seen day and how many of its users returned on each later day.
type Cohort struct {
	Date     time.Time `json:"date"`
	Size     int       `json:"size"`     // users first seen on this day
	Returned []int     `json:"returned"` // Returned[n] = users active n days after Date (Returned[0] == Size)
}

// Result is the full retention grid (one row per cohort period).
type Result struct {
	Cohorts []Cohort `json:"cohorts"`
	MaxDays int      `json:"max_days"`          // max periods measured (kept name for compat)
	Bucket  string   `json:"bucket,omitempty"`  // "day" (default), "week", or "month" (30-day)
	Rolling bool     `json:"rolling,omitempty"` // true = "active on OR AFTER period n" (unbounded)
}

// bucketSeconds is the block length for a retention period. Week and month use fixed
// 7-day / 30-day blocks so the grid stays deterministic and storage-agnostic — a weekly
// product read through daily n-day retention looks broken; week bucketing fixes that.
func bucketSeconds(bucket string) int64 {
	switch bucket {
	case "week":
		return 7 * 86400
	case "month":
		return 30 * 86400
	default:
		return 86400
	}
}

// Compute builds daily n-day retention over maxDays — the default. A user belongs to the
// cohort of their first event's (UTC) day; they "return on day n" if they have any event
// on the day n days after their first. retentionEvent optionally filters which events count
// as activity (empty = any event).
func Compute(events []event.Event, maxDays int, retentionEvent string) Result {
	return ComputeBucketed(events, maxDays, retentionEvent, "day", false)
}

// ComputeBucketed generalizes Compute to week/month periods and rolling mode:
//   - bucket "week"/"month" groups cohorts + return periods into 7-/30-day blocks, so a
//     weekly product's retention isn't understated by a daily read.
//   - rolling=true counts a user as retained at period n if they were active on period n OR
//     ANY LATER period (unbounded retention), instead of exactly on period n (classic).
func ComputeBucketed(events []event.Event, maxPeriods int, retentionEvent, bucket string, rolling bool) Result {
	if maxPeriods < 0 {
		maxPeriods = 0 // never make a negative-length Returned slice
	}
	bs := bucketSeconds(bucket)
	periodNum := func(t time.Time) int64 { return t.UTC().Unix() / bs }

	type userPeriods struct {
		first   time.Time
		periods map[int64]bool
	}
	users := map[string]*userPeriods{}

	for _, e := range events {
		if retentionEvent != "" && e.Name != retentionEvent {
			continue
		}
		u := users[e.DistinctID]
		if u == nil {
			u = &userPeriods{first: e.Timestamp, periods: map[int64]bool{}}
			users[e.DistinctID] = u
		}
		if e.Timestamp.Before(u.first) {
			u.first = e.Timestamp
		}
		u.periods[periodNum(e.Timestamp)] = true
	}

	cohorts := map[int64]*Cohort{}
	for _, u := range users {
		first := periodNum(u.first)
		c := cohorts[first]
		if c == nil {
			c = &Cohort{Date: time.Unix(first*bs, 0).UTC(), Returned: make([]int, maxPeriods+1)}
			cohorts[first] = c
		}
		c.Size++
		if rolling {
			// unbounded: count the user toward every period up to their LAST active one.
			maxN := 0
			for p := range u.periods {
				if n := int(p - first); n > maxN {
					maxN = n
				}
			}
			if maxN > maxPeriods {
				maxN = maxPeriods
			}
			for n := 0; n <= maxN; n++ {
				c.Returned[n]++
			}
		} else {
			for p := range u.periods {
				if n := int(p - first); n >= 0 && n <= maxPeriods {
					c.Returned[n]++
				}
			}
		}
	}

	out := make([]Cohort, 0, len(cohorts))
	for _, c := range cohorts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	b := bucket
	if b == "" {
		b = "day"
	}
	return Result{Cohorts: out, MaxDays: maxPeriods, Bucket: b, Rolling: rolling}
}

// PeriodN aggregates period-n retention across cohorts HONESTLY: only cohorts whose
// period-n has fully elapsed as of `now` enter the denominator. Users who signed up
// yesterday cannot have day-7 (or week-2) activity yet — counting them would systematically
// understate retention (the classic retention-triangle mistake), and reporting period-n at
// all when no cohort is old enough would be a fabricated 0%. Uses the Result's own bucket,
// so it is correct for daily, weekly, and monthly grids alike. Every surface that summarizes
// retention (verdict, MCP, ask) must use this.
func PeriodN(r Result, n int, now time.Time) (retained, size int) {
	if n <= 0 || n > r.MaxDays {
		return 0, 0
	}
	bs := bucketSeconds(r.Bucket)
	cur := now.UTC().Unix() / bs
	for _, c := range r.Cohorts {
		cp := c.Date.UTC().Unix() / bs
		if cp+int64(n) < cur && len(c.Returned) > n {
			size += c.Size
			retained += c.Returned[n]
		}
	}
	return retained, size
}

// DayN is the daily-period alias for PeriodN, kept so existing callers read unchanged.
func DayN(r Result, n int, now time.Time) (retained, size int) { return PeriodN(r, n, now) }

// Summarize builds the honest headline retention percentages for a grid, picking a period
// set + labels that match the bucket (day 1/7/30, week 1/2/4, month 1/2/3). A period no
// cohort is old enough to observe is OMITTED, never reported as a fabricated 0%. This is the
// single source both the HTTP API and the MCP tool serialize, so the two can never disagree
// (agreement_test enforces it). Does NOT include the raw cohorts grid — callers add that.
// CohortJSON is the serialization shape shared by the HTTP API and the MCP tool, so
// the two can never disagree (agreement_test locks them). Returned[n] is nil for any
// period whose window has not started relative to now — an unobservable future day
// must serialize as null, never 0, or it reads as "retention cratered to 0%".
type CohortJSON struct {
	Date     time.Time `json:"date"`
	Size     int       `json:"size"`
	Returned []*int    `json:"returned"`
}

// SerializeCohorts nulls out unobservable future periods, one definition for every surface.
func SerializeCohorts(r Result, now time.Time) []CohortJSON {
	bs := bucketSeconds(r.Bucket)
	out := make([]CohortJSON, 0, len(r.Cohorts))
	for _, c := range r.Cohorts {
		cj := CohortJSON{Date: c.Date, Size: c.Size, Returned: make([]*int, len(c.Returned))}
		for n := range c.Returned {
			if c.Date.Unix()+int64(n)*bs > now.Unix() {
				cj.Returned[n] = nil // future period: not yet observable
				continue
			}
			v := c.Returned[n]
			cj.Returned[n] = &v
		}
		out = append(out, cj)
	}
	return out
}

func Summarize(r Result, now time.Time) map[string]any {
	var size int
	for _, c := range r.Cohorts {
		size += c.Size
	}
	unit, periods := "day", []int{1, 7, 30}
	switch r.Bucket {
	case "week":
		unit, periods = "week", []int{1, 2, 4}
	case "month":
		unit, periods = "month", []int{1, 2, 3}
	}
	out := map[string]any{"cohort_users": size, "max_days": r.MaxDays, "bucket": r.Bucket, "rolling": r.Rolling}
	for _, n := range periods {
		if ret, sz := PeriodN(r, n, now); sz > 0 {
			out[fmt.Sprintf("%s%d_retention_pct", unit, n)] = int(float64(ret)/float64(sz)*100 + 0.5)
			out[fmt.Sprintf("%s%d_cohort_users", unit, n)] = sz
		}
	}
	return out
}
