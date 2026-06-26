// Package query is the segmentation backbone: filter events by their properties
// and break them down (group by) a property. Every report (funnel, retention,
// trends) gets filtering + breakdown by composing these over the event slice
// before the deterministic compute — the thing that makes analytics powerful.
package query

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Op is a filter comparison.
type Op string

const (
	Eq       Op = "eq"
	Neq      Op = "neq"
	Contains Op = "contains"
	Gt       Op = "gt"
	Lt       Op = "lt"
)

// Filter is a single predicate over an event property. Filters combine with AND.
type Filter struct {
	Property string `json:"property"`
	Op       Op     `json:"op"`
	Value    any    `json:"value"`
}

func (f Filter) match(e event.Event) bool {
	v, ok := e.Properties[f.Property]
	switch f.Op {
	case Eq:
		return ok && toStr(v) == toStr(f.Value)
	case Neq:
		return !ok || toStr(v) != toStr(f.Value)
	case Contains:
		return ok && strings.Contains(toStr(v), toStr(f.Value))
	case Gt:
		return ok && toNum(v) > toNum(f.Value)
	case Lt:
		return ok && toNum(v) < toNum(f.Value)
	}
	return false
}

// Apply returns the events matching ALL filters (empty filters = passthrough).
func Apply(events []event.Event, filters []Filter) []event.Event {
	if len(filters) == 0 {
		return events
	}
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		keep := true
		for _, f := range filters {
			if !f.match(e) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

// Group is one breakdown bucket: a property value and its events.
type Group struct {
	Value  string        `json:"value"`
	Events []event.Event `json:"-"`
	Count  int           `json:"count"`
}

// Breakdown groups events by a property value, sorted by count descending.
// Events missing the property fall into "(none)".
func Breakdown(events []event.Event, property string) []Group {
	buckets := map[string][]event.Event{}
	for _, e := range events {
		key := "(none)"
		if v, ok := e.Properties[property]; ok {
			key = toStr(v)
		}
		buckets[key] = append(buckets[key], e)
	}
	out := make([]Group, 0, len(buckets))
	for k, evs := range buckets {
		out = append(out, Group{Value: k, Events: evs, Count: len(evs)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}
