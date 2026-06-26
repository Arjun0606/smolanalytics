// Package trends computes time-series — how many times an event happened per day
// (optionally unique users), the third core analysis primitive alongside funnels
// and retention. Deterministic and storage-agnostic.
package trends

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Point is one day's value.
type Point struct {
	Date  time.Time `json:"date"`
	Count int       `json:"count"`
}

// Result is the daily series for one event.
type Result struct {
	Event  string  `json:"event"`
	Unique bool    `json:"unique"` // true = distinct users, false = raw count
	Points []Point `json:"points"`
	Total  int     `json:"total"`
}

// Compute returns daily counts for eventName (empty = all events) between from and
// to. unique=true counts distinct users per day instead of raw events. Days with
// no activity are filled with zero so the line/bars are continuous.
func Compute(events []event.Event, eventName string, from, to time.Time, unique bool) Result {
	r := Result{Event: eventName, Unique: unique}
	perDay := map[int64]map[string]int{} // day -> (user->count) or (""->count)

	for _, e := range events {
		if eventName != "" && e.Name != eventName {
			continue
		}
		d := e.Timestamp.UTC().Truncate(24 * time.Hour).Unix() / 86400
		if perDay[d] == nil {
			perDay[d] = map[string]int{}
		}
		if unique {
			perDay[d][e.DistinctID]++
		} else {
			perDay[d][""]++
		}
	}

	// Determine the day span to fill (from/to if given, else min/max seen).
	var lo, hi int64
	have := false
	for d := range perDay {
		if !have || d < lo {
			lo = d
		}
		if !have || d > hi {
			hi = d
		}
		have = true
	}
	if !from.IsZero() {
		lo = from.UTC().Unix() / 86400
	}
	if !to.IsZero() {
		hi = to.UTC().Unix() / 86400
	}
	if !have && from.IsZero() {
		return r
	}

	for d := lo; d <= hi; d++ {
		c := 0
		if m := perDay[d]; m != nil {
			if unique {
				c = len(m)
			} else {
				c = m[""]
			}
		}
		r.Points = append(r.Points, Point{Date: time.Unix(d*86400, 0).UTC(), Count: c})
		r.Total += c
	}
	sort.Slice(r.Points, func(i, j int) bool { return r.Points[i].Date.Before(r.Points[j].Date) })
	return r
}
