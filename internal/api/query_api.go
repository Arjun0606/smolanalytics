package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/engagement"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/fixbrief"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
	"github.com/Arjun0606/smolanalytics/internal/groups"
	"github.com/Arjun0606/smolanalytics/internal/paths"
	"github.com/Arjun0606/smolanalytics/internal/provenance"
	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/retention"
	"github.com/Arjun0606/smolanalytics/internal/trends"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

// filtersFrom parses ?filters=<url-encoded JSON array> into predicates, so any
// report can be segmented (e.g. plan=pro AND country=US). Malformed filters are an
// ERROR, never ignored — silently returning unfiltered data as if it were the
// segment is the worst kind of wrong answer.
func filtersFrom(r *http.Request) ([]query.Filter, error) {
	fs, _, err := filterSetFrom(r)
	return fs, err
}

// filterSetFrom is the ONE filter parser every /v1 endpoint and the dashboard share,
// so the ask bar, an agent over MCP, a pasted URL, and the dashboard all speak one
// filter language. It accepts both the ?f=prop:op:value chip grammar (repeatable,
// the URL-native form) and ?filters=<JSON array> (the programmatic form), and honors
// ?fm=any to OR the rows. Malformed input is an ERROR, never ignored — silently
// returning unfiltered data as if it were the segment is the worst kind of wrong answer.
func filterSetFrom(r *http.Request) ([]query.Filter, bool, error) {
	var fs []query.Filter
	if raw := r.URL.Query().Get("filters"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fs); err != nil {
			// never embed err.Error() — the encoding/json message leaks internal Go type
			// names (e.g. "[]query.Filter") to the caller. Return a fixed, shape-guiding message.
			return nil, false, badRequestError{`filters must be a JSON array of {"property","op","value"} objects, e.g. [{"property":"plan","op":"eq","value":"pro"}]`}
		}
	}
	// ?f=prop:op:value (or prop:value for eq); multi-value a|b becomes an In list.
	for _, raw := range r.URL.Query()["f"] {
		p, op, v, ok := parseChip(raw)
		if !ok {
			return nil, false, badRequestError{fmt.Sprintf("bad filter %q: use prop:op:value with op in eq,neq,contains,ncontains,regex,gt,lt,set,notset", raw)}
		}
		flt := query.Filter{Property: p, Op: op, Value: v}
		if parts := strings.Split(v, "|"); len(parts) > 1 && op == query.Eq {
			arr := make([]any, len(parts))
			for i, x := range parts {
				arr[i] = x
			}
			flt = query.Filter{Property: p, Op: query.In, Value: arr}
		}
		fs = append(fs, flt)
	}
	if err := query.Validate(fs); err != nil {
		return nil, false, badRequestError{err.Error()}
	}
	return fs, r.URL.Query().Get("fm") == "any", nil
}

// badRequestError marks a caller mistake (bad filters) so handlers return 400, not 500.
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

// writeQueryErr maps a filtered()/store error to the right status code.
func writeQueryErr(w http.ResponseWriter, err error) {
	var br badRequestError
	if errors.As(err, &br) {
		writeErr(w, http.StatusBadRequest, br.msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// filtered loads all events, applies the request's property filters, and (if
// ?cohort=<id> is set) scopes to that cohort's members. Cohort membership is
// resolved over the full history, then the filtered events are kept for those users.
func (s *Server) filtered(r *http.Request) ([]event.Event, error) {
	fs, anyMode, err := filterSetFrom(r)
	if err != nil {
		return nil, err
	}
	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	// A filter on a property that exists on NO event is almost always a typo (plann=pro),
	// and returning a silent 0 would read as a real answer — the exact fabrication the
	// covenant forbids. Say so, and list the properties that do exist. (Value mismatches
	// on a real property still return an honest 0; only an unknown KEY is an error.)
	if bad, known := query.FirstUnknownProp(all, fs); bad != "" {
		return nil, badRequestError{fmt.Sprintf("no events carry the property %q, so this filter can only ever match 0 — check the spelling. Properties seen: %s", bad, strings.Join(known, ", "))}
	}
	// first-touch-stamp acquisition-attribute filters (device/referrer/utm/country/…) so
	// "signups where device=mobile" means "signups by mobile-acquired users" — matching the
	// dashboard + ask bar — instead of a silent 0 because signup carries no device property.
	evs := query.ApplyMode(query.StampForFilters(all, fs), fs, anyMode)
	if err := applyCohort(r, s, all, &evs); err != nil {
		return nil, err
	}
	return evs, nil
}

// funnelScoped applies the request's filter at the USER level, not the event level:
// it keeps every event of any user who has at least one matching event. A funnel
// filtered by a user attribute (plan, device, country) that isn't present on every
// step event would otherwise drop the later steps and report a broken funnel
// ([50,0,0] instead of [50,30,15]). This matches the breakdown path's semantics.
func (s *Server) funnelScoped(r *http.Request) ([]event.Event, error) {
	fs, anyMode, err := filterSetFrom(r)
	if err != nil {
		return nil, err
	}
	all, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	// same unknown-property guard the other reports use — a typo'd filter property
	// ("plann") used to return a confident all-zero funnel, while trends/breakdown/MCP 400.
	if bad, known := query.FirstUnknownProp(all, fs); bad != "" {
		return nil, badRequestError{fmt.Sprintf("no events carry the property %q, so this filter can only ever match 0 — check the spelling. Properties seen: %s", bad, strings.Join(known, ", "))}
	}
	evs := query.ScopeUsers(all, fs, anyMode)
	if err := applyCohort(r, s, all, &evs); err != nil {
		return nil, err
	}
	return evs, nil
}

// These endpoints back the interactive Explore panel: run any report on any of the
// user's own events, not just the demo funnel. The engine already takes arbitrary
// event names — this just exposes it over REST (the MCP tools do the same for AI).

// GET /v1/meta — the event names available, so the UI can offer them.
// boolParam accepts exactly {true, 1, yes}, case-insensitive — ERRORS-1: a boolean
// the caller clearly set must never be silently ignored because of its spelling.
func boolParam(v string) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// knownEventOr400 enforces ERRORS-1's honest-failure rule: naming an event that has
// never been seen returns 400 listing what exists, never a real-looking zero report.
func (s *Server) knownEventOr400(w http.ResponseWriter, name string) bool {
	if name == "" {
		return true
	}
	names, err := s.store.Names()
	if err != nil {
		return true // storage trouble surfaces elsewhere; don't mask it as a 400
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	writeErr(w, http.StatusBadRequest, fmt.Sprintf("unknown event %q — known events: %s", name, strings.Join(names, ", ")))
	return false
}

func (s *Server) apiMeta(w http.ResponseWriter, r *http.Request) {
	names, err := s.store.Names()
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	out := map[string]any{"events": names}
	// ?props=1 adds the property catalog: every property seen in the last 30 days
	// with its top values — the typeahead behind the filter builder, so filtering
	// is picking from what your data actually contains, never guessing names.
	if r.URL.Query().Get("props") == "1" {
		evs, err := s.store.Range(time.Now().UTC().AddDate(0, 0, -30), time.Time{})
		if err == nil {
			evs = query.Apply(evs, nil)
			counts := map[string]map[string]int{}
			for _, e := range evs {
				for k, v := range e.Properties {
					if k == "env" || k == "engaged_ms" || k == "session_id" {
						continue // internal / high-cardinality noise
					}
					sv, ok := v.(string)
					if !ok || sv == "" || len(sv) > 80 {
						continue
					}
					m := counts[k]
					if m == nil {
						m = map[string]int{}
						counts[k] = m
					}
					if len(m) <= 200 {
						m[sv]++
					}
				}
			}
			props := map[string][]string{}
			keys := make([]string, 0, len(counts))
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 50 {
				keys = keys[:50]
			}
			for _, k := range keys {
				type vc struct {
					v string
					n int
				}
				vs := make([]vc, 0, len(counts[k]))
				for v, n := range counts[k] {
					vs = append(vs, vc{v, n})
				}
				sort.Slice(vs, func(i, j int) bool { return vs[i].n > vs[j].n })
				if len(vs) > 20 {
					vs = vs[:20]
				}
				vals := make([]string, len(vs))
				for i, x := range vs {
					vals[i] = x.v
				}
				props[k] = vals
			}
			out["properties"] = props
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /v1/trends?event=signup&unique=true&breakdown=source&filters=...
// With breakdown set, returns one series per property value (multi-line trend).
func (s *Server) apiTrends(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	q := r.URL.Query()
	unique := boolParam(q.Get("unique"))
	event := q.Get("event")
	if !s.knownEventOr400(w, event) {
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	// XAU: measure=dau|wau|mau plots rolling distinct-actives per day (TRENDS-XAU) —
	// intercepted before property measures because they need no property.
	switch q.Get("measure") {
	case "dau":
		writeJSON(w, http.StatusOK, trends.ComputeXAU(evs, event, from, to, 1))
		return
	case "wau":
		writeJSON(w, http.StatusOK, trends.ComputeXAU(evs, event, from, to, 7))
		return
	case "mau":
		writeJSON(w, http.StatusOK, trends.ComputeXAU(evs, event, from, to, 30))
		return
	}
	// numeric aggregation: measure=sum|avg|min|max|median|p90 over a numeric property
	// (revenue, AOV, p90 latency) — the money/growth questions Count can't answer.
	if meas := q.Get("measure"); meas != "" {
		property := q.Get("property")
		if property == "" {
			writeErr(w, http.StatusBadRequest, "measure needs a numeric property, e.g. measure=sum&property=amount")
			return
		}
		m, ok := trends.ParseMeasure(meas)
		if !ok {
			writeErr(w, http.StatusBadRequest, "unknown measure "+meas+" (want sum, avg, min, max, median, p90, p95 or p99)")
			return
		}
		// a measure over a non-numeric property (measure=avg&property=device) must
		// error, not silently return total:0 — a 0 reads as "average device is 0".
		numeric := false
		for _, p := range trends.NumericProps(evs) {
			if p == property {
				numeric = true
				break
			}
		}
		if !numeric {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("property %q is not numeric — measures need a numeric property like amount", property))
			return
		}
		// measure + breakdown = the aggregate per group ("revenue by plan"). This combination
		// used to silently drop the breakdown and return the grand total as if it were the split.
		if bd := q.Get("breakdown"); bd != "" {
			series := trends.ComputeMeasureBreakdown(evs, event, property, m, bd, from, to)
			if !trends.FiniteSeries(series) {
				writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("the %s of %q overflows a float64 (the result is not a finite number) — values this large can't be aggregated exactly; check the property's unit (e.g. cents vs dollars)", m, property))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"event": event, "property": property, "measure": string(m), "breakdown": bd,
				"series": series,
			})
			return
		}
		res := trends.ComputeMeasure(evs, event, property, m, from, to)
		// a +Inf/NaN aggregate (float64 overflow on huge values) is not representable in
		// JSON — an explicit error beats the empty-200 the encoder failure used to produce.
		if !res.Finite() {
			writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("the %s of %q overflows a float64 (the result is not a finite number) — values this large can't be aggregated exactly; check the property's unit (e.g. cents vs dollars)", m, property))
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	iv, iverr := trends.ParseInterval(q.Get("interval"))
	if iverr != nil {
		writeErr(w, http.StatusBadRequest, iverr.Error())
		return
	}
	if bd := q.Get("breakdown"); bd != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"event": event, "breakdown": bd,
			"series": trends.ComputeBreakdown(evs, event, bd, from, to, unique),
		})
		return
	}
	// multi-series: events=a,b,c charts several events on one canvas (the
	// signups-vs-checkouts question). Single event= stays the simple path.
	if multi := q.Get("events"); multi != "" {
		names := strings.Split(multi, ",")
		if len(names) > 8 {
			names = names[:8]
		}
		series := make([]trends.Result, 0, len(names))
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			series = append(series, trends.ComputeInterval(evs, n, from, to, unique, iv))
		}
		writeJSON(w, http.StatusOK, map[string]any{"interval": string(iv), "series": series})
		return
	}
	writeJSON(w, http.StatusOK, trends.ComputeInterval(evs, event, from, to, unique, iv))
}

// parseTrendWindow reads the time scope for /v1/trends from the query: days=N is a
// rolling window ending now (capped at a year); from/to accept RFC3339 or YYYY-MM-DD.
// Zero times mean unbounded, so no params = all recorded history (the long-standing
// default). Unparseable values are returned as an error the caller turns into a 400,
// rather than silently answering over a different range — the trends endpoint used to
// ignore these entirely, so days=7 and days=90 returned the same series.
func parseTrendWindow(r *http.Request) (from, to time.Time, err error) {
	q := r.URL.Query()
	// "last N hours/days" ends NOW — never the future. Capping the upper bound at now
	// (not leaving it unbounded) is what makes /v1 and MCP agree with the ask bar, which
	// already ends its window at now: a clock-skewed, future-dated event would otherwise
	// land in the /v1/MCP count but not the ask count, silently breaking the covenant.
	now := time.Now().UTC()
	if v := q.Get("hours"); v != "" {
		n, perr := strconv.ParseFloat(v, 64)
		// reject NaN/Inf explicitly: ParseFloat("NaN") succeeds, and n<=0 is false for NaN,
		// so without this a hours=NaN query slipped through to a nonsense window bound.
		if perr != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > 24*366 {
			return from, to, fmt.Errorf("hours must be a positive number (got %q)", v)
		}
		return now.Add(-time.Duration(n * float64(time.Hour))), now, nil
	}
	if v := q.Get("days"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("days must be a positive integer")
		}
		if n > 365 {
			n = 365
		}
		// align to whole calendar days: "last N days" is N complete day-buckets ending
		// today, so the first daily bucket is a full day, never a clipped mid-day window
		// that renders a phantom leading 0 on the chart. from = midnight, (n-1) days back.
		today := now.Truncate(24 * time.Hour)
		return today.AddDate(0, 0, -(n - 1)), now, nil
	}
	parse := func(key string) (time.Time, error) {
		v := q.Get(key)
		if v == "" {
			return time.Time{}, nil
		}
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			return t.UTC(), nil
		}
		if t, e := time.Parse("2006-01-02", v); e == nil {
			return t.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", key)
	}
	if from, err = parse("from"); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if to, err = parse("to"); err != nil {
		return time.Time{}, time.Time{}, err
	}
	// absolute windows get the SAME now-cap as rolling ones: any explicit window ends at
	// now, never the future. Without this, from=<today>&to=<tomorrow> counted a clock-skewed
	// future event that days=1 excluded — the identical question answered 3 vs 2 depending
	// on how the window was phrased. Only the fully-unbounded query (no params at all =
	// "all recorded history") keeps future events, matching the raw export.
	if !from.IsZero() || !to.IsZero() {
		if to.IsZero() || to.After(now) {
			to = now
		}
	}
	return from, to, nil
}

// posIntParam reads a count-shaped query param (days, limit, depth, breakdown_limit).
//
// Every report used to parse these inline with `if v, err := strconv.Atoi(x); err == nil && v > 0`,
// which treats "abc", "-5", "0" and "1.5" as ABSENT: GET /v1/web?days=abc answered over the default
// 30 days with a 200 and nothing in the body to reveal that the window was not the one asked for,
// /v1/retention?days=abc silently returned the 7-day grid, /v1/paths?depth=abc returned depth 3.
// Meanwhile /v1/trends?days=abc correctly 400s — the same typo was honest on one report and a
// quietly different window on six others. Missing/empty keeps the default; anything PRESENT but
// unusable is a 400. Over-max values are clamped rather than rejected because the caps are shared
// with the MCP tools (days=500 → 90) and the two surfaces have to keep agreeing.
func posIntParam(r *http.Request, key string, def, max int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, badRequestError{fmt.Sprintf("%s must be a positive integer (got %q)", key, v)}
	}
	if max > 0 && n > max {
		n = max
	}
	return n, nil
}

// rejectWindowParams 400s when a report is handed a time-scope parameter it cannot honor.
//
// These handlers window by whole days (or not at all) and never read from/to/hours, so
// GET /v1/web?from=2020-01-01&to=2020-02-01 returned the LAST 30 DAYS with a 200: a 2020 question
// answered with this month's numbers, with no provenance in the response to expose the swap. The
// same silence hid ?hours=6 on /v1/web and ?days= on /v1/stickiness. Naming the parameter we cannot
// honor is the honest half of the covenant — an answer computed over a window nobody asked for is
// worse than an error.
func rejectWindowParams(r *http.Request, windows string, keys ...string) error {
	for _, k := range keys {
		if r.URL.Query().Get(k) != "" {
			return badRequestError{fmt.Sprintf("this report does not take %s= — %s, so a %s= scope would be silently ignored. /v1/trends and /v1/funnel take days, hours and from/to.", k, windows, k)}
		}
	}
	return nil
}

// GET /v1/breakdown?event=signup&property=source&filters=...
func (s *Server) apiBreakdown(w http.ResponseWriter, r *http.Request) {
	property := r.URL.Query().Get("property")
	if property == "" {
		property = r.URL.Query().Get("prop") // accept prop= as an alias for property=
	}
	if property == "" {
		writeErr(w, http.StatusBadRequest, "property is required")
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	// BREAKDOWN-WINDOW: days/from/to scope this report exactly like trends — it
	// used to ignore them entirely, so every "windowed" breakdown was all-time
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	eventName := r.URL.Query().Get("event")
	if !s.knownEventOr400(w, eventName) {
		return
	}
	scoped := evs[:0:0]
	for _, e := range evs {
		if eventName != "" && e.Name != eventName {
			continue
		}
		ts := e.Timestamp.UTC()
		if !from.IsZero() && ts.Before(from) {
			continue
		}
		if !to.IsZero() && !ts.Before(to) {
			continue
		}
		scoped = append(scoped, e)
	}
	// honesty guard: if NO scoped event carries this property, it's an unknown/misspelled
	// name — error with the real property list (like the MCP breakdown tool does) instead of
	// returning a real-looking all-"(none)" report that reads as "everyone is unsegmented".
	if len(scoped) > 0 {
		known := map[string]bool{}
		hasProp := false
		for _, e := range scoped {
			for k := range e.Properties {
				known[k] = true
			}
			if _, ok := e.Properties[property]; ok {
				hasProp = true
			}
		}
		if !hasProp {
			list := make([]string, 0, len(known))
			for k := range known {
				list = append(list, k)
			}
			sort.Strings(list)
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("no %q events carry a %q property; known properties: %s", eventName, property, strings.Join(list, ", ")))
			return
		}
	}
	unique := boolParam(r.URL.Query().Get("unique"))
	groups := query.Breakdown(scoped, property)
	rows := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		// distinct users per bucket, so "how many USERS signed up on desktop" is
		// answerable and unique=true is honored (it was a silent no-op — the report
		// only ever returned event volume). Both are always exposed; count follows the
		// unique flag so the primary number matches what was asked for.
		usered := make(map[string]struct{}, len(g.Events))
		for _, e := range g.Events {
			usered[e.DistinctID] = struct{}{}
		}
		visitors := len(usered)
		count := g.Count
		if unique {
			count = visitors
		}
		rows = append(rows, map[string]any{"value": g.Value, "count": count, "events": g.Count, "visitors": visitors})
	}
	// when unique, re-sort by the distinct-user count so ordering matches the number.
	if unique {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i]["count"].(int) > rows[j]["count"].(int) })
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": eventName, "property": property, "unique": unique, "groups": rows})
}

// GET /v1/retention?event=open&days=7&filters=...
func (s *Server) apiRetention(w http.ResponseWriter, r *http.Request) {
	days, derr := posIntParam(r, "days", 7, 90)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	if derr := rejectWindowParams(r, "retention windows by whole days (days=N)", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	q := r.URL.Query()
	// validate the anchor event name — a typo'd event used to return a confident empty grid
	// (as if nobody ever did it), while the MCP retention tool errors on the same typo.
	if !s.knownEventOr400(w, q.Get("event")) {
		return
	}
	// bucket=week|month groups cohorts into 7-/30-day periods (a weekly product read daily
	// looks broken); rolling=true is unbounded "active on or after period n" retention.
	// ERRORS-1: an unknown bucket is a 400 naming the valid set, never silently daily.
	switch q.Get("bucket") {
	case "", "day", "week", "month":
	default:
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("unknown bucket %q (want day, week or month)", q.Get("bucket")))
		return
	}
	rr := retention.ComputeBucketed(evs, days, q.Get("event"), q.Get("bucket"), boolParam(q.Get("rolling")))
	// the honest headline summaries come from retention.Summarize — the SAME function the MCP
	// tool serializes, so the two surfaces can't drift (agreement_test locks it).
	now := time.Now().UTC()
	out := retention.Summarize(rr, now)
	out["cohorts"] = retention.SerializeCohorts(rr, now)
	writeJSON(w, http.StatusOK, out)
}

// GET /v1/lifecycle?days=30&filters=... — new/returning/resurrected/dormant per day
func (s *Server) apiLifecycle(w http.ResponseWriter, r *http.Request) {
	days, derr := posIntParam(r, "days", 30, 180)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	if derr := rejectWindowParams(r, "lifecycle windows by whole days (days=N)", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": engagement.ComputeLifecycle(evs, days)})
}

// GET /v1/stickiness?filters=... — DAU/WAU/MAU + ratio
func (s *Server) apiStickiness(w http.ResponseWriter, r *http.Request) {
	// DAU/WAU/MAU are fixed 1/7/30-day windows by definition, so this report reads no time
	// parameter at all — ?days=90 used to be accepted and thrown away, handing back the
	// 30-day MAU as if it were the answer to a 90-day question.
	if derr := rejectWindowParams(r, "stickiness is always DAU/WAU/MAU (fixed 1-, 7- and 30-day windows)", "days", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, engagement.ComputeStickiness(evs, time.Time{}))
}

// GET /v1/ai-visibility?days=90&filters=... — AI search visibility (GEO), computed
// from sampled $geo_check events: per-engine mention/recommend rates, avg rank,
// competitor mentions, weekly share-of-voice, and the latest verbatim per engine.
// Same single query path as everything else — the MCP ai_visibility tool must
// return byte-identical answers (agreement_test).
func (s *Server) apiAIVisibility(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	// 0 → aivis default (90; visibility moves slowly). 365 is the same cap the MCP tool
	// applies — surfaces must agree.
	days, derr := posIntParam(r, "days", 0, 365)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	if derr := rejectWindowParams(r, "AI visibility windows by trailing days (days=N)", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	writeJSON(w, http.StatusOK, aivis.Compute(evs, days, time.Time{}))
}

// GET /v1/fix-brief?finding=<title|fingerprint>&steps=a,b,c — one verdict finding turned into
// something a coding agent can act on: the computed evidence, the measured pages it shows up on,
// and the acceptance criterion. A report like any other, not a UI helper: the fix_brief MCP tool
// must return byte-identical JSON (agreement_test), and the dashboard's "Fix with your agent"
// sheet renders this exact struct.
func (s *Server) apiFixBrief(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fixbrief.Compute(evs,
		parseFunnelSteps(r.URL.Query().Get("steps")), r.URL.Query().Get("finding"), time.Time{}))
}

// parseFunnelSteps reads the funnel definition the CALLER is looking at. The verdict deliberately
// describes the funnel the reader sees (insight.GenerateForFunnel), so a brief pulled with no
// steps can be about a different funnel than the one on the dashboard. The dashboard therefore
// always sends its own, and the brief echoes them in every evidence row so an agent can reproduce
// it exactly. Under two steps there is no funnel and we fall back to detection, identically on
// both surfaces.
func parseFunnelSteps(csv string) []funnel.Step {
	var out []funnel.Step
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, funnel.Step{Event: n})
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// GET /v1/paths?start=signup&depth=3&filters=... — what users do after an event.
// A "/"-prefixed start (?start=/pricing) follows page-to-page navigation instead,
// same contract as the MCP paths tool.
func (s *Server) apiPaths(w http.ResponseWriter, r *http.Request) {
	start := r.URL.Query().Get("start")
	if start == "" {
		writeErr(w, http.StatusBadRequest, "start is required: an event name, or a page path beginning with \"/\" (e.g. /pricing) to follow page-to-page navigation")
		return
	}
	depth, derr := posIntParam(r, "depth", 3, 10) // 10 = the MCP paths cap; surfaces must agree
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	// honor days/hours/from/to like every other report — a "last 7 days" journey used to
	// include 20-day-old events, and the ask bar's provenance falsely claimed the window.
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	evs = scopeToWindow(evs, from, to)
	if strings.HasPrefix(start, "/") {
		writeJSON(w, http.StatusOK, paths.Pages(evs, start, depth))
		return
	}
	writeJSON(w, http.StatusOK, paths.After(evs, start, depth))
}

// GET /v1/web?days=30&filters=... — the web-analytics overview (visitors, pageviews,
// live-now, top pages, referrers, UTM sources, device split) from $pageview events.
func (s *Server) apiWeb(w http.ResponseWriter, r *http.Request) {
	days, derr := posIntParam(r, "days", 30, 365)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	if derr := rejectWindowParams(r, "the web overview windows by whole days (days=N)", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, web.Compute(evs, days, time.Time{}))
}

// GET /v1/groups?property=company&limit=50&filters=... — account-level roll-up
func (s *Server) apiGroups(w http.ResponseWriter, r *http.Request) {
	property := r.URL.Query().Get("property")
	if property == "" {
		writeErr(w, http.StatusBadRequest, "property is required (the group key, e.g. company)")
		return
	}
	limit, derr := posIntParam(r, "limit", 50, 0)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	// the account roll-up is computed over the whole recorded history (groups.Compute takes
	// no window), so a days=/from= scope here was accepted and dropped on the floor.
	if derr := rejectWindowParams(r, "the account roll-up is computed over all recorded history", "days", "from", "to", "hours"); derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups.Compute(evs, property, time.Time{}, limit))
}

// apiWho is the Microscope: the people behind any datapoint. Three descriptor
// modes, each REUSING the exact engine that computed the aggregate, so the list
// always sums to the number on the chart:
//
//	trends point:  event=X&date=YYYY-MM-DD (+ window/filters)
//	breakdown row: event=X&property=P&value=V (+ window/filters)
//	funnel step:   steps=a,b,c&step=N&state=reached|dropped|converted (+ order/exclude/window)
//
// Response: {mode, total, users:[{distinct_id, events, last_seen}]}, capped at 200.
func (s *Server) apiWho(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// The microscope has to load its events through the SAME scope function that produced the
	// number it is explaining. Funnel mode used s.filtered (EVENT-level) while /v1/funnel, the
	// dashboard and MCP all use s.funnelScoped (USER-level), so a funnel filtered by a property
	// only the first step carries — ?steps=signup,checkout&f=plan:eq:pro, where signup has `plan`
	// and checkout does not — charted "2 converted" while clicking that bar listed NOBODY
	// (total_users 0): every checkout event was dropped before funnel.Users ever saw it. Which
	// loader to use is decided before the read so the store is still scanned exactly once.
	var evs []event.Event
	var err error
	if q.Get("steps") != "" {
		evs, err = s.funnelScoped(r)
	} else {
		evs, err = s.filtered(r)
	}
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	type row struct {
		ID       string    `json:"distinct_id"`
		Events   int       `json:"events"`
		LastSeen time.Time `json:"last_seen"`
	}
	collect := func(match func(event.Event) bool) []row {
		agg := map[string]*row{}
		for _, e := range evs {
			if !match(e) {
				continue
			}
			a := agg[e.DistinctID]
			if a == nil {
				a = &row{ID: e.DistinctID}
				agg[e.DistinctID] = a
			}
			a.Events++
			if e.Timestamp.After(a.LastSeen) {
				a.LastSeen = e.Timestamp
			}
		}
		out := make([]row, 0, len(agg))
		for _, a := range agg {
			out = append(out, *a)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
		return out
	}
	respond := func(mode string, rows []row) {
		total := len(rows)
		if len(rows) > 200 {
			rows = rows[:200]
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "total_users": total, "users": rows})
	}

	// funnel-step mode
	if stepsQ := q.Get("steps"); stepsQ != "" {
		steps := parseSteps(stepsQ)
		if len(steps) < 2 {
			writeErr(w, http.StatusBadRequest, "steps must list at least two event names")
			return
		}
		stepN, err := strconv.Atoi(q.Get("step"))
		if err != nil || stepN < 0 || stepN >= len(steps) {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("step must be 0..%d", len(steps)-1))
			return
		}
		state := q.Get("state")
		window, _ := time.ParseDuration(q.Get("window"))
		if window <= 0 {
			window = 7 * 24 * time.Hour
		}
		order, oerr := funnel.ParseOrder(q.Get("order"))
		if oerr != nil {
			writeErr(w, http.StatusBadRequest, oerr.Error())
			return
		}
		outcomes := funnel.Users(evs, steps, window, funnel.Options{Order: order})
		wanted := map[string]bool{}
		for _, o := range outcomes {
			switch state {
			case "dropped": // reached step N, did NOT reach N+1
				if o.Reached == stepN+1 && !o.Converted {
					wanted[o.DistinctID] = true
				}
			case "converted":
				if o.Converted {
					wanted[o.DistinctID] = true
				}
			default: // "reached" (or empty): reached at least step N
				if o.Reached >= stepN+1 {
					wanted[o.DistinctID] = true
				}
			}
		}
		respond("funnel", collect(func(e event.Event) bool { return wanted[e.DistinctID] }))
		return
	}

	eventName := q.Get("event")
	if !s.knownEventOr400(w, eventName) {
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	inWindow := func(e event.Event) bool {
		ts := e.Timestamp.UTC()
		if !from.IsZero() && ts.Before(from) {
			return false
		}
		if !to.IsZero() && !ts.Before(to) {
			return false
		}
		return true
	}

	// trends-point mode: one bucket's day
	if d := q.Get("date"); d != "" {
		day, derr := time.Parse("2006-01-02", d)
		if derr != nil {
			writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		next := day.AddDate(0, 0, 1)
		respond("trends_point", collect(func(e event.Event) bool {
			if eventName != "" && e.Name != eventName {
				return false
			}
			ts := e.Timestamp.UTC()
			return !ts.Before(day) && ts.Before(next) && inWindow(e)
		}))
		return
	}

	// breakdown-row mode
	if prop := q.Get("property"); prop != "" {
		val := q.Get("value")
		respond("breakdown_row", collect(func(e event.Event) bool {
			if eventName != "" && e.Name != eventName {
				return false
			}
			got, _ := e.Properties[prop].(string)
			return got == val && inWindow(e)
		}))
		return
	}

	// plain event-in-window mode
	respond("event", collect(func(e event.Event) bool {
		return (eventName == "" || e.Name == eventName) && inWindow(e)
	}))
}

// applyCohort narrows evs to a saved cohort's members, and ERRORS when the id does not resolve.
//
// It used to swallow the miss: `if d, ok := s.cohorts.Get(cid); ok { ... }` with no else, so a
// deleted, mistyped or foreign cohort id left `evs` at the FULL population and the report came
// back 200 with every user in it, labelled as that cohort. A dashboard link kept after someone
// deleted the cohort, or an agent passing an id it half-remembered, gets a confident answer about
// a group that does not exist — the loudest possible version of the fabrication this codebase
// refuses everywhere else. `?cohort=` is also silently ignored when no cohort store is configured,
// which is the same lie with a different cause, so that is an error too.
func applyCohort(r *http.Request, s *Server, all []event.Event, evs *[]event.Event) error {
	cid := r.URL.Query().Get("cohort")
	if cid == "" {
		return nil
	}
	if s.cohorts == nil {
		return badRequestError{"this instance has no cohort storage configured, so ?cohort= cannot be honored — run `smolanalytics serve` with a data directory"}
	}
	d, ok := s.cohorts.Get(cid)
	if !ok {
		known := s.cohorts.List()
		names := make([]string, 0, len(known))
		for _, c := range known {
			names = append(names, c.ID+" ("+c.Name+")")
		}
		msg := "no cohort with id " + cid
		if len(names) > 0 {
			msg += " — this instance has: " + strings.Join(names, ", ")
		} else {
			msg += " — this instance has no saved cohorts"
		}
		return badRequestError{msg}
	}
	*evs = cohort.FilterToUsers(*evs, cohort.Resolve(all, d))
	return nil
}

// apiRows is the microscope pointed at the EVENTS rather than the people.
// GET /v1/rows?event=signup&days=7[&property=&value=][&unique=1][&limit=N]
//
// /v1/who answers "which people are behind this number". This answers "which ROWS", and then
// recomputes the figure over exactly those rows and hands back a digest of them. That last part
// is the whole feature: a listing invites trust, a listing that reproduces the number it explains
// can be checked.
//
// It is only possible because every report here is a pure function of raw events with no
// rollups, no sampling and no pre-aggregation — the rows are still present at query time. At
// PostHog's or Mixpanel's scale they are not, which is why "why don't your numbers match" is
// answered over there with ad blockers and residency endpoints instead of with the rows.
func (s *Server) apiRows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("event")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "event is required — name the event whose rows you want, e.g. ?event=signup&days=7")
		return
	}
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	// The SAME scoping and window the report used. A microscope loaded through a different path
	// than the number it explains is worse than none: it produces a confident, contradictory
	// second answer. (That exact defect was found in this endpoint's sibling, /v1/who, which
	// scoped funnels at event level while /v1/funnel scoped them at user level.)
	evs = scopeToWindow(evs, from, to)

	// ?date= narrows to ONE bucket, exactly as /v1/who does for the same click.
	//
	// Without this the rows behind a single bar were every row in the window: click one day's
	// 47 signups and the panel proved 2,376 — internally consistent, and about a different
	// number than the one you clicked. A proof that verifies the wrong figure is worse than no
	// proof, because it is confidently checkable and still wrong.
	var dayFrom, dayTo time.Time
	if d := q.Get("date"); d != "" {
		day, derr := time.Parse("2006-01-02", d)
		if derr != nil {
			writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		dayFrom, dayTo = day, day.AddDate(0, 0, 1)
	}

	prop, val := q.Get("property"), q.Get("value")
	match := func(e event.Event) bool {
		if e.Name != name {
			return false
		}
		if !dayFrom.IsZero() {
			ts := e.Timestamp.UTC()
			if ts.Before(dayFrom) || !ts.Before(dayTo) {
				return false
			}
		}
		if prop == "" {
			return true
		}
		got, ok := e.Properties[prop]
		return ok && toStr(got) == val
	}

	unique := q.Get("unique") == "1" || q.Get("unique") == "true"
	recompute := provenance.CountOf
	question := "how many " + name + " events"
	if unique {
		recompute = provenance.UniqueUsersOf
		question = "how many people did " + name
	}
	if prop != "" {
		question += " where " + prop + " = " + val
	}
	if !dayFrom.IsZero() {
		question += " on " + dayFrom.Format("2006-01-02")
	}

	// The claimed figure is computed HERE, over the same slice, by the same function that will
	// be used to verify it. That is deliberate: the endpoint is not asserting a number it was
	// handed, it is showing its own working.
	var matched []event.Event
	for _, e := range evs {
		if match(e) {
			matched = append(matched, e)
		}
	}
	claimed := recompute(matched)

	limit := 0
	if v, err := strconv.Atoi(q.Get("limit")); err == nil {
		limit = v
	}
	writeJSON(w, http.StatusOK, provenance.Collect(evs, question, claimed, limit, match, recompute))
}
