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
	// referrer is stored as a full landing URL but shown/filtered by host — so an
	// equality filter must compare HOSTS, exactly like the ask-side segment matcher
	// (hostEquals) does. Without this, clicking "reddit.com" on the referrers card
	// filters referrer==reddit.com against "https://reddit.com/r/x" and matches nothing,
	// and the dashboard silently disagrees with "how's reddit doing" over MCP.
	isRef := f.Property == "referrer"
	eq := func(a, b any) bool {
		if isRef {
			return hostOfURL(toStr(a)) == hostOfURL(toStr(b))
		}
		return toStr(a) == toStr(b)
	}
	switch f.Op {
	case Eq:
		return ok && eq(v, f.Value)
	case Neq:
		return !ok || !eq(v, f.Value)
	case Contains:
		return ok && strings.Contains(toStr(v), toStr(f.Value))
	case Gt:
		return ok && toNum(v) > toNum(f.Value)
	case Lt:
		return ok && toNum(v) < toNum(f.Value)
	case In:
		return ok && inListEq(v, f.Value, eq)
	case NotIn:
		return !ok || !inListEq(v, f.Value, eq)
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

// inListEq is inList with a caller-supplied equality (so referrer IN (...) can match
// host-aware, consistent with the Eq path).
func inListEq(v any, list any, eq func(a, b any) bool) bool {
	arr, ok := list.([]any)
	if !ok {
		return false
	}
	for _, item := range arr {
		if eq(v, item) {
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
		case Gt, Lt:
			// a non-numeric comparand ("gt":"abc") silently coerces to 0 and returns a
			// real-looking filtered number — reject it instead (the silent-wrong-number trap).
			if !isNumericValue(f.Value) {
				return fmt.Errorf("filter op %q on property %q needs a numeric value, got %v", f.Op, f.Property, f.Value)
			}
		case Eq, Neq, Contains, Set, NotSet, NotContains:
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
			return fmt.Errorf("unknown filter op %q on property %q. valid ops: eq, neq, contains, gt, lt, in, notin, set, notset", f.Op, f.Property)
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
// Matches reports whether a single event satisfies ALL of the filters (AND). Unlike Apply it
// does NOT apply the default dev-env exclusion — that's a stream-level concern; callers that
// want it pre-filter the stream with Apply first. Used for per-step conditions in sequenced
// cohorts, where each step constrains one event by name + its own property filters.
func Matches(e event.Event, filters []Filter) bool {
	for _, f := range filters {
		if !f.match(e) {
			return false
		}
	}
	return true
}

// NonProduction lists the env values hidden from every default-scoped query. The browser SDK
// stamps localhost and dev tunnels as "development", and Netlify deploy previews plus
// staging/preview/dev subdomains as "preview"; anyone can set their own via init({env}).
//
// An explicit set, NOT `env != "production"`. The two failure modes are not symmetric: showing
// a bit of preview traffic is noise the viewer can filter away, while hiding real traffic is
// invisible from the dashboard and reads as "your product recorded nothing today". So an
// unrecognised value stays VISIBLE — someone who writes env:"prod" or env:"live" must never
// have their production numbers silently disappear because it did not match a magic string.
var NonProduction = map[string]bool{
	"development": true,
	"preview":     true,
	"staging":     true,
	"test":        true,
	"ci":          true,
}

// Keeper returns the per-event predicate Apply uses, so a caller streaming through
// Store.Scan can filter as events arrive instead of materializing everything first and
// filtering after. The dashboard used to hold two full copies of history — one raw, one
// filtered — which is most of why memory tracked total events rather than the query.
//
// This exists so there is exactly ONE definition of "does this event belong in a
// default-scoped query". Apply is written in terms of it. A second hand-rolled copy of
// the env rule in a streaming caller would drift the moment either changed, and the
// symptom would be two screens quietly disagreeing about the same number.
func Keeper(filters []Filter) func(event.Event) bool {
	filtersTouchEnv := false
	for _, f := range filters {
		if f.Property == "env" {
			filtersTouchEnv = true
			break
		}
	}
	return func(e event.Event) bool {
		// An explicit env filter means the caller asked for that env on purpose, so the
		// default "hide non-production" rule steps out of the way rather than ANDing with
		// it and returning nothing.
		if !filtersTouchEnv {
			if v, ok := e.Properties["env"].(string); ok && NonProduction[v] {
				return false
			}
		}
		for _, f := range filters {
			if !f.match(e) {
				return false
			}
		}
		return true
	}
}

func Apply(events []event.Event, filters []Filter) []event.Event {
	keep := Keeper(filters)
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		if keep(e) {
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

// isNumericValue reports whether a gt/lt comparand is a real number (or a numeric string),
// so a non-numeric one is rejected at validation instead of silently coercing to 0.
func isNumericValue(v any) bool {
	switch n := v.(type) {
	case float64, float32, int, int64, int32:
		return true
	case string:
		_, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return err == nil
	}
	return false
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
// acquisitionProps are visitor/acquisition attributes that live on the LANDING event (the
// $pageview), not on later conversion events. Filtering a conversion event by one of these
// event-level returns 0 (the signup carries no referrer/device), so a report must first-touch
// stamp them onto the user's whole stream — which is what the dashboard and ask bar already do.
var acquisitionProps = map[string]bool{
	"referrer": true, "source": true, "utm_source": true, "utm_medium": true, "utm_campaign": true,
	"channel": true, "device": true, "os": true, "browser": true, "country": true, "platform": true,
}

// StampForFilters first-touch-stamps every acquisition/user-attribute property a filter
// targets, so "signups where device=mobile" (or referrer/utm/country/…) means "signups by
// users acquired on mobile" — the same number the dashboard and ask bar report — instead of a
// silent 0 because the signup event never carried the property. Product/event props (plan,
// amount) are left untouched; they're set at the step itself, so an event-level filter is right.
// Both GET /v1 and the MCP tools call this before applying filters, so all four surfaces agree.
func StampForFilters(evs []event.Event, filters []Filter) []event.Event {
	var want []string
	for _, f := range filters {
		if acquisitionProps[f.Property] {
			want = append(want, f.Property) // StampFirstTouchAll deduplicates
		}
	}
	return StampFirstTouchAll(evs, want)
}

func StampFirstTouch(events []event.Event, prop string) []event.Event {
	return StampFirstTouchAll(events, []string{prop})
}

// StampFirstTouchAll is StampFirstTouch for several properties in ONE pass.
//
// Callers used to chain it: `for _, p := range eightProps { evs = StampFirstTouch(evs, p) }`.
// Because stamping copies every event's property map, that is eight full copies of history
// and eight fresh maps per event — 1.6M map allocations for 200k events, and measured as 47%
// of everything the dashboard allocated. Stamping N properties at once allocates one map per
// event instead of N, because each pass only ever ADDED a key.
//
// Batching is safe precisely because the properties are distinct: stamping "device" never
// changes any event's "country" value, so the first-touch lookup for a later property reads
// the same input either way and the result is identical. The parity test in query_test.go
// pins that equivalence against the chained implementation rather than trusting the argument.
func StampFirstTouchAll(events []event.Event, props []string) []event.Event {
	return BuildFirstTouch(events, props).Stamp(events)
}

// FirstTouch holds each user's earliest value for a set of properties. Building it is a scan
// that allocates nothing per event; applying it is what costs, because stamping has to copy
// a property map.
//
// Separating the two matters because the population you must LOOK AT to find a user's first
// touch (all of history — the country is on the landing pageview) is much larger than the
// population you need to STAMP (in segmentBlame, only the two event names in the funnel step
// being blamed). Fused together, every caller paid to copy history.
type FirstTouch struct {
	props []string
	first []map[string]string // one user→value map per property, parallel to props
}

// BuildFirstTouch scans every event once and records, per property, each user's value from
// their earliest event carrying it. Nothing is copied.
func BuildFirstTouch(events []event.Event, props []string) *FirstTouch {
	// Deduplicate: callers assemble these lists from several sources (known acquisition
	// attributes plus whatever the blame scan discovered), and a repeat would otherwise pay
	// for a second lookup to write the identical value.
	uniq := make([]string, 0, len(props))
	seenProp := make(map[string]bool, len(props))
	for _, p := range props {
		if p != "" && !seenProp[p] {
			seenProp[p] = true
			uniq = append(uniq, p)
		}
	}
	f := &FirstTouch{props: uniq}
	if len(uniq) == 0 {
		return f
	}
	at := make([]map[string]int64, len(uniq))
	f.first = make([]map[string]string, len(uniq))
	for i := range uniq {
		at[i] = map[string]int64{}
		f.first[i] = map[string]string{}
	}
	for _, e := range events {
		if len(e.Properties) == 0 {
			continue
		}
		ts := e.Timestamp.UnixNano()
		for i, prop := range uniq {
			v, ok := e.Properties[prop]
			if !ok {
				continue
			}
			val := toStr(v)
			if val == "" {
				continue // an empty value is not a first touch, it is a missing one
			}
			if prop == "referrer" {
				val = hostOfURL(val)
			}
			if cur, seen := at[i][e.DistinctID]; !seen || ts < cur {
				at[i][e.DistinctID] = ts
				f.first[i][e.DistinctID] = val
			}
		}
	}
	return f
}

// Stamp returns a copy of events carrying each user's first-touch values. The input is never
// mutated: callers re-read the original events to decide what a conversion event natively
// carries, and stamped values leaking back into that check would silently skip properties
// that must be stamped.
func (f *FirstTouch) Stamp(events []event.Event) []event.Event {
	if f == nil || len(f.props) == 0 {
		return events
	}
	out := make([]event.Event, len(events))
	for i, e := range events {
		out[i] = e
		// Count first, so a user with no first-touch value for any of these properties keeps
		// sharing the caller's map instead of paying for a copy that would change nothing.
		extra := 0
		for j := range f.props {
			if _, ok := f.first[j][e.DistinctID]; ok {
				extra++
			}
		}
		if extra == 0 {
			continue
		}
		np := make(map[string]any, len(e.Properties)+extra)
		for k, v := range e.Properties {
			np[k] = v
		}
		for j, prop := range f.props {
			if val, ok := f.first[j][e.DistinctID]; ok {
				np[prop] = val
			}
		}
		out[i].Properties = np
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

// FirstUnknownProp returns the first filter property that uses a POSITIVE operator
// (eq/contains/gt/lt/in/set/regex — ones that can only match when the property exists)
// yet appears on NO event, plus a sorted list of the properties that DO exist. This is
// how a filtered report tells a typo ("plann=pro" → no such property) apart from a
// genuine empty result, instead of silently returning 0 as if it were the honest answer.
// Negative operators (neq/notin/notset/notcontains) are skipped: a missing property
// legitimately satisfies them. Returns ("", nil) when every positive filter is known.
func FirstUnknownProp(events []event.Event, filters []Filter) (string, []string) {
	if len(filters) == 0 {
		return "", nil
	}
	known := map[string]bool{}
	for _, e := range events {
		for k := range e.Properties {
			known[k] = true
		}
	}
	positive := map[Op]bool{Eq: true, Contains: true, Gt: true, Lt: true, In: true, Set: true, Regex: true}
	for _, f := range filters {
		if positive[f.Op] && f.Property != "" && !known[f.Property] {
			list := make([]string, 0, len(known))
			for k := range known {
				list = append(list, k)
			}
			sort.Strings(list)
			return f.Property, list
		}
	}
	return "", nil
}
