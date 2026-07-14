// Package query is the segmentation backbone: filter events by their properties
// and break them down (group by) a property. Every report (funnel, retention,
// trends) gets filtering + breakdown by composing these over the event slice
// before the deterministic compute — the thing that makes analytics powerful.
package query

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Op is a filter comparison.
type Op string

const (
	Eq          Op = "eq"
	Neq         Op = "neq"
	Contains    Op = "contains"
	Gt          Op = "gt"
	Lt          Op = "lt"
	In          Op = "in"     // value is one of a list — expresses OR over one property (source in [hn, twitter])
	NotIn       Op = "notin"  // value is none of a list (or the property is missing)
	Set         Op = "set"    // the property exists on the event (value ignored)
	NotSet      Op = "notset" // the property is missing (value ignored)
	Regex       Op = "regex"  // value is a Go regexp matched against the stringified property
	NotContains Op = "ncontains"
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
	case Regex:
		if !ok {
			return false
		}
		re, err := regexCached(toStr(f.Value))
		return err == nil && re.MatchString(toStr(v))
	case NotContains:
		return !ok || !strings.Contains(toStr(v), toStr(f.Value))
	}
	return false
}

// regexCached compiles patterns once — filters run per event over the whole log,
// and recompiling a regexp per event would turn one filtered report into seconds.
var regexCache sync.Map // pattern -> *regexp.Regexp (or error sentinel)

func regexCached(pat string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pat); ok {
		if re, ok := v.(*regexp.Regexp); ok {
			return re, nil
		}
		return nil, fmt.Errorf("bad regex")
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		regexCache.Store(pat, err.Error())
		return nil, err
	}
	regexCache.Store(pat, re)
	return re, nil
}

// ApplyMode is Apply with an any/all switch: all = every filter must match (the
// default AND), any = at least one must (the OR mode of the dashboard's filter
// builder). Dev-traffic exclusion applies in both modes, before the user filters.
func ApplyMode(evs []event.Event, filters []Filter, anyMode bool) []event.Event {
	if !anyMode || len(filters) <= 1 {
		return Apply(evs, filters)
	}
	base := Apply(evs, nil) // keeps the default dev-env exclusion
	out := make([]event.Event, 0, len(base))
	for _, e := range base {
		for _, f := range filters {
			if f.match(e) {
				out = append(out, e)
				break
			}
		}
	}
	return out
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
		case Eq, Neq, Contains, Gt, Lt, Set, NotSet, NotContains:
		case Regex:
			if _, err := regexCached(toStr(f.Value)); err != nil {
				return fmt.Errorf("filter op %q on property %q: invalid regex", f.Op, f.Property)
			}
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

// ScopeUsers keeps every event of any user who has at least one event matching the
// filters — user-level scoping (vs Apply's event-level). Funnels use this so a filter
// on a user attribute (plan, device) that isn't present on every step event scopes the
// POPULATION, not the events, and later steps aren't dropped. Empty filters = unchanged.
func ScopeUsers(events []event.Event, filters []Filter, anyMode bool) []event.Event {
	if len(filters) == 0 {
		return events
	}
	matched := ApplyMode(events, filters, anyMode)
	users := make(map[string]bool, len(matched))
	for _, e := range matched {
		users[e.DistinctID] = true
	}
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if users[e.DistinctID] {
			out = append(out, e)
		}
	}
	return out
}

// StampFirstTouch returns a copy of events where every event of a user carries that
// user's FIRST-TOUCH value of `prop` (from their earliest event that has it). This lets
// a funnel/report breakdown segment by an acquisition/user attribute (referrer, device,
// country) even though the conversion events don't carry it — otherwise the breakdown
// collapses everyone into "(none)". Referrer values are reduced to their host.
func StampFirstTouch(events []event.Event, prop string) []event.Event {
	type ft struct {
		t   int64
		val string
	}
	first := map[string]ft{}
	for _, e := range events {
		v, ok := e.Properties[prop]
		if !ok {
			continue
		}
		val := toStr(v)
		if val == "" {
			continue
		}
		if prop == "referrer" {
			val = hostOfURL(val)
		}
		ts := e.Timestamp.UnixNano()
		if cur, seen := first[e.DistinctID]; !seen || ts < cur.t {
			first[e.DistinctID] = ft{ts, val}
		}
	}
	out := make([]event.Event, len(events))
	for i, e := range events {
		out[i] = e
		if f, ok := first[e.DistinctID]; ok {
			np := make(map[string]any, len(e.Properties)+1)
			for k, v := range e.Properties {
				np[k] = v
			}
			np[prop] = f.val
			out[i].Properties = np
		}
	}
	return out
}

// hostOfURL reduces a referrer URL to its bare host (no scheme, no path, no www).
func hostOfURL(v string) string {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimPrefix(v, "www.")
}
