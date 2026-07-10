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
	In       Op = "in"     // value is one of a list — expresses OR over one property (source in [hn, twitter])
	NotIn    Op = "notin"  // value is none of a list (or the property is missing)
	Set      Op = "set"    // the property exists on the event (value ignored)
	NotSet   Op = "notset" // the property is missing (value ignored)
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
	case In:
		return ok && inList(v, f.Value)
	case NotIn:
		return !ok || !inList(v, f.Value)
	case Set:
		return ok
	case NotSet:
		return !ok
	}
	return false
}

// inList reports whether v (stringified) equals any element of list. list is a JSON array
// (decoded to []any); a non-list value never matches.
func inList(v any, list any) bool {
	arr, ok := list.([]any)
	if !ok {
		return false
	}
	s := toStr(v)
	for _, item := range arr {
		if toStr(item) == s {
			return true
		}
	}
	return false
}

// Validate rejects malformed filters up front. An unrecognized op would otherwise
// match NOTHING and every report would return zeros that look like a real answer —
// the exact silent-wrong-number failure this engine exists to prevent.
func Validate(filters []Filter) error {
	for _, f := range filters {
		switch f.Op {
		case Eq, Neq, Contains, Gt, Lt, Set, NotSet:
		case In, NotIn:
			// a list op with a non-list value would silently match nothing — reject it so a
			// malformed "in" can't return a real-looking zero (the silent-wrong-number trap).
			if _, ok := f.Value.([]any); !ok {
				return fmt.Errorf("filter op %q on property %q needs a list value, e.g. \"value\": [\"a\", \"b\"]", f.Op, f.Property)
			}
		default:
			return fmt.Errorf("unknown filter op %q on property %q — valid ops: eq, neq, contains, gt, lt, in, notin, set, notset", f.Op, f.Property)
		}
		if f.Property == "" {
			return fmt.Errorf("filter is missing a property name")
		}
	}
	return nil
}

// Apply returns the events matching ALL filters — and enforces the one default
// scope of the whole query layer: events stamped env=development are EXCLUDED
// unless the filters explicitly reference "env". Localhost traffic polluting
// production funnels is the classic silent report-corruptor; asking for dev data
// stays one filter away (env eq development). Living inside Apply means every
// surface (HTTP API, MCP, dashboard) inherits the same rule — the agreement test
// depends on that.
func Apply(events []event.Event, filters []Filter) []event.Event {
	filtersTouchEnv := false
	for _, f := range filters {
		if f.Property == "env" {
			filtersTouchEnv = true
			break
		}
	}
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if !filtersTouchEnv {
			if v, ok := e.Properties["env"]; ok && v == "development" {
				continue
			}
		}
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
