// Package trends computes time-series — how many times an event happened per day
// (optionally unique users), the third core analysis primitive alongside funnels
// and retention. Deterministic and storage-agnostic.
package trends

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
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
		d := e.Timestamp.UTC().Truncate(24*time.Hour).Unix() / 86400
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

// Series is one line of a broken-down trend (e.g. signups from "google" over time).
type Series struct {
	Value  string  `json:"value"`
	Points []Point `json:"points"`
	Total  int     `json:"total"`
}

// ComputeBreakdown splits a trend into one series per value of property — the
// multi-line "signups by source over time" report. Events missing the property
// fall into "(none)". Series are sorted by total descending.
func ComputeBreakdown(events []event.Event, eventName, property string, from, to time.Time, unique bool) []Series {
	groups := map[string][]event.Event{}
	// every series must share ONE date span (the overall min..max day) — otherwise each
	// line starts/ends at its own first/last event and the multi-line chart's x-axes
	// disagree with each other.
	spanFrom, spanTo := from, to
	for _, e := range events {
		if eventName != "" && e.Name != eventName {
			continue
		}
		if from.IsZero() && (spanFrom.IsZero() || e.Timestamp.Before(spanFrom)) {
			spanFrom = e.Timestamp
		}
		if to.IsZero() && (spanTo.IsZero() || e.Timestamp.After(spanTo)) {
			spanTo = e.Timestamp
		}
		key := "(none)"
		if v, ok := e.Properties[property]; ok {
			key = valueOf(v)
		}
		groups[key] = append(groups[key], e)
	}
	out := make([]Series, 0, len(groups))
	for val, evs := range groups {
		r := Compute(evs, eventName, spanFrom, spanTo, unique)
		out = append(out, Series{Value: val, Points: r.Points, Total: r.Total})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func valueOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// Measure is a numeric aggregation over an event property — the money/growth questions
// Count can't answer: revenue (sum of "amount"), average order value (avg), p90 latency,
// min/max. This is the single most common "it can't do X" a new user hits on day one.
type Measure string

const (
	Sum    Measure = "sum"
	Avg    Measure = "avg"
	Min    Measure = "min"
	Max    Measure = "max"
	Median Measure = "median"
	P90    Measure = "p90"
)

// ParseMeasure maps a string (query param, MCP arg) to a Measure, defaulting to Sum for a
// bare/unknown value so a caller that asks for a numeric aggregation always gets one.
func ParseMeasure(s string) (Measure, bool) {
	switch Measure(s) {
	case Sum, Avg, Min, Max, Median, P90:
		return Measure(s), true
	case "average", "mean":
		return Avg, true
	case "p95", "p99":
		return P90, true // nearest supported high-percentile
	}
	return Sum, false
}

// MeasurePoint is one day's aggregated numeric value. N is how many events contributed —
// 0 marks an empty day so avg/median/p90 read as "no data", not a real zero.
type MeasurePoint struct {
	Date  time.Time `json:"date"`
	Value float64   `json:"value"`
	N     int       `json:"n"`
}

// MeasureResult is the daily numeric series plus the aggregate over the WHOLE window (so
// Total for avg/median/p90 is correct, not a misleading mean-of-daily-means).
type MeasureResult struct {
	Event    string         `json:"event"`
	Property string         `json:"property"`
	Measure  Measure        `json:"measure"`
	Points   []MeasurePoint `json:"points"`
	Total    float64        `json:"total"`
	N        int            `json:"n"` // total events that carried a numeric value
}

// ComputeMeasure aggregates a numeric event property per day between from and to. Events
// missing the property, or whose value isn't numeric, are skipped (never coerced to 0).
// Deterministic and storage-agnostic, same as Compute.
func ComputeMeasure(events []event.Event, eventName, property string, m Measure, from, to time.Time) MeasureResult {
	res := MeasureResult{Event: eventName, Property: property, Measure: m}
	perDay := map[int64][]float64{}
	var all []float64

	for _, e := range events {
		if eventName != "" && e.Name != eventName {
			continue
		}
		raw, ok := e.Properties[property]
		if !ok {
			continue
		}
		f, ok := numOf(raw)
		if !ok {
			continue
		}
		d := e.Timestamp.UTC().Truncate(24*time.Hour).Unix() / 86400
		perDay[d] = append(perDay[d], f)
		all = append(all, f)
	}

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
		return res
	}

	for d := lo; d <= hi; d++ {
		vals := perDay[d]
		res.Points = append(res.Points, MeasurePoint{
			Date:  time.Unix(d*86400, 0).UTC(),
			Value: applyMeasure(m, vals),
			N:     len(vals),
		})
	}
	res.Total = applyMeasure(m, all)
	res.N = len(all)
	sort.Slice(res.Points, func(i, j int) bool { return res.Points[i].Date.Before(res.Points[j].Date) })
	return res
}

// applyMeasure reduces a day's (or the window's) numeric values to a single number. An
// empty slice is 0 for every measure — a day with no matching events.
func applyMeasure(m Measure, vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	switch m {
	case Sum:
		s := 0.0
		for _, v := range vals {
			s += v
		}
		return s
	case Avg:
		s := 0.0
		for _, v := range vals {
			s += v
		}
		return s / float64(len(vals))
	case Min:
		mn := vals[0]
		for _, v := range vals[1:] {
			if v < mn {
				mn = v
			}
		}
		return mn
	case Max:
		mx := vals[0]
		for _, v := range vals[1:] {
			if v > mx {
				mx = v
			}
		}
		return mx
	case Median:
		s := append([]float64(nil), vals...)
		sort.Float64s(s)
		n := len(s)
		if n%2 == 1 {
			return s[n/2]
		}
		return (s[n/2-1] + s[n/2]) / 2
	case P90:
		s := append([]float64(nil), vals...)
		sort.Float64s(s)
		rank := int(math.Ceil(0.9*float64(len(s)))) - 1 // nearest-rank
		if rank < 0 {
			rank = 0
		}
		return s[rank]
	}
	return 0
}

// NumericProps returns the property names that carry at least one numeric value across the
// events — the columns a measure (sum/avg/p90) can aggregate. Sorted for determinism. Lets
// the ask bar resolve "revenue" to a real numeric property, or say honestly that none exists.
func NumericProps(events []event.Event) []string {
	seen := map[string]bool{}
	for _, e := range events {
		for k, v := range e.Properties {
			if seen[k] {
				continue
			}
			if _, ok := numOf(v); ok {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// numOf coerces a JSON-decoded property value to a float. Handles the shapes the store can
// hold: JSON numbers (float64/json.Number), Go ints, and numeric strings ("29.99").
func numOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
