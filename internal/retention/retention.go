// Package retention computes cohort retention — the other core product-analytics
// primitive: group users by the day they first showed up, then track what % come
// back on day 1, 2, ... N. Deterministic and storage-agnostic, like funnel.
package retention

import (
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

// Result is the full retention grid (one row per cohort day).
type Result struct {
	Cohorts []Cohort `json:"cohorts"`
	MaxDays int      `json:"max_days"`
}

// Compute builds the retention grid over maxDays. A user belongs to the cohort of
// their first event's (UTC) day; they "return on day n" if they have any event on
// the day n days after their first. retentionEvent optionally filters which events
// count as activity (empty = any event).
func Compute(events []event.Event, maxDays int, retentionEvent string) Result {
	if maxDays < 0 {
		maxDays = 0 // never make a negative-length Returned slice
	}
	type userDays struct {
		first time.Time
		days  map[int64]bool // day-number (unix-days) -> active
	}
	users := map[string]*userDays{}

	for _, e := range events {
		if retentionEvent != "" && e.Name != retentionEvent {
			continue
		}
		d := dayNum(e.Timestamp)
		u := users[e.DistinctID]
		if u == nil {
			u = &userDays{first: e.Timestamp, days: map[int64]bool{}}
			users[e.DistinctID] = u
		}
		if e.Timestamp.Before(u.first) {
			u.first = e.Timestamp
		}
		u.days[d] = true
	}

	cohorts := map[int64]*Cohort{}
	for _, u := range users {
		first := dayNum(u.first)
		c := cohorts[first]
		if c == nil {
			c = &Cohort{Date: time.Unix(first*86400, 0).UTC(), Returned: make([]int, maxDays+1)}
			cohorts[first] = c
		}
		c.Size++
		for d := range u.days {
			n := int(d - first)
			if n >= 0 && n <= maxDays {
				c.Returned[n]++
			}
		}
	}

	out := make([]Cohort, 0, len(cohorts))
	for _, c := range cohorts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return Result{Cohorts: out, MaxDays: maxDays}
}

func dayNum(t time.Time) int64 { return t.UTC().Unix() / 86400 }
