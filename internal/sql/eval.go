package sql

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// columns are the bare event fields. Properties are reached with prop.<key>, deliberately kept
// separate so a typo'd column is an error rather than a silently-empty property lookup — the
// difference between "you asked for something that does not exist" and "zero rows matched".
var columns = map[string]bool{
	"name": true, "distinct_id": true, "timestamp": true, "id": true,
}

func isColumn(s string) bool { return columns[s] }

func columnNames() []string {
	out := make([]string, 0, len(columns))
	for k := range columns {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var aggregates = map[string]bool{
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
}

var scalars = map[string]bool{
	"date": true, "hour": true, "week": true, "month": true, "year": true,
	"lower": true, "upper": true, "length": true, "round": true, "abs": true,
	"coalesce": true, "substr": true, "replace": true, "concat": true, "cast_number": true,
}

func knownFunc(n string) bool { return aggregates[n] || scalars[n] }

func funcNames() []string {
	out := make([]string, 0, len(aggregates)+len(scalars))
	for k := range aggregates {
		out = append(out, k)
	}
	for k := range scalars {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hasAggregate reports whether an expression tree contains an aggregate call, which is what
// decides between streaming rows straight out and accumulating groups.
func hasAggregate(e Expr) bool {
	switch v := e.(type) {
	case Call:
		if aggregates[v.Name] {
			return true
		}
		for _, a := range v.Args {
			if hasAggregate(a) {
				return true
			}
		}
	case Binary:
		return hasAggregate(v.Left) || hasAggregate(v.Right)
	case Unary:
		return hasAggregate(v.X)
	case InList:
		if hasAggregate(v.X) {
			return true
		}
		for _, a := range v.Vals {
			if hasAggregate(a) {
				return true
			}
		}
	case IsNull:
		return hasAggregate(v.X)
	}
	return false
}

// evalScalar computes a non-aggregate expression against one event.
func evalScalar(e Expr, ev event.Event) (any, error) {
	switch v := e.(type) {
	case Lit:
		return v.Val, nil
	case Star:
		return nil, fmt.Errorf("* is only valid as count(*)")
	case Col:
		switch v.Name {
		case "name":
			return ev.Name, nil
		case "distinct_id":
			return ev.DistinctID, nil
		case "id":
			return ev.ID, nil
		case "timestamp":
			return ev.Timestamp, nil
		}
		return nil, fmt.Errorf("unknown column %q", v.Name)
	case Prop:
		if ev.Properties == nil {
			return nil, nil
		}
		val, ok := ev.Properties[v.Key]
		if !ok {
			return nil, nil
		}
		return val, nil
	case Unary:
		x, err := evalScalar(v.X, ev)
		if err != nil {
			return nil, err
		}
		if v.Op == "NOT" {
			return !truthy(x), nil
		}
		f, ok := toNum(x)
		if !ok {
			return nil, nil
		}
		return -f, nil
	case Binary:
		return evalBinary(v, ev)
	case InList:
		x, err := evalScalar(v.X, ev)
		if err != nil {
			return nil, err
		}
		found := false
		for _, cand := range v.Vals {
			c, err := evalScalar(cand, ev)
			if err != nil {
				return nil, err
			}
			if compare(x, c) == 0 {
				found = true
				break
			}
		}
		return found != v.Negate, nil
	case IsNull:
		x, err := evalScalar(v.X, ev)
		if err != nil {
			return nil, err
		}
		return (x == nil) != v.Negate, nil
	case Call:
		return evalCall(v, ev)
	}
	return nil, fmt.Errorf("cannot evaluate %s", e.String())
}

func evalBinary(b Binary, ev event.Event) (any, error) {
	// AND/OR short-circuit, so `prop.x IS NOT NULL AND prop.x > 5` behaves as written.
	if b.Op == "AND" || b.Op == "OR" {
		l, err := evalScalar(b.Left, ev)
		if err != nil {
			return nil, err
		}
		lt := truthy(l)
		if b.Op == "AND" && !lt {
			return false, nil
		}
		if b.Op == "OR" && lt {
			return true, nil
		}
		r, err := evalScalar(b.Right, ev)
		if err != nil {
			return nil, err
		}
		return truthy(r), nil
	}
	l, err := evalScalar(b.Left, ev)
	if err != nil {
		return nil, err
	}
	r, err := evalScalar(b.Right, ev)
	if err != nil {
		return nil, err
	}
	switch b.Op {
	case "=", "!=", "<", "<=", ">", ">=":
		// NULL compares to nothing, as in SQL. Without this, `prop.plan != 'pro'` would count
		// every event that has no plan at all, which reads as a real segment and is not one.
		if l == nil || r == nil {
			return false, nil
		}
		c := compare(l, r)
		switch b.Op {
		case "=":
			return c == 0, nil
		case "!=":
			return c != 0, nil
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		case ">=":
			return c >= 0, nil
		}
	case "LIKE":
		if l == nil || r == nil {
			return false, nil
		}
		return likeMatch(toStr(l), toStr(r)), nil
	case "+", "-", "*", "/", "%":
		lf, lok := toNum(l)
		rf, rok := toNum(r)
		if !lok || !rok {
			return nil, nil
		}
		switch b.Op {
		case "+":
			return lf + rf, nil
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		case "/":
			if rf == 0 {
				return nil, nil // NULL, not an error: one bad row must not kill the query
			}
			return lf / rf, nil
		case "%":
			if rf == 0 {
				return nil, nil
			}
			return math.Mod(lf, rf), nil
		}
	}
	return nil, fmt.Errorf("unsupported operator %q", b.Op)
}

func evalCall(c Call, ev event.Event) (any, error) {
	if aggregates[c.Name] {
		return nil, fmt.Errorf("%s() is an aggregate and cannot be used here", c.Name)
	}
	args := make([]any, 0, len(c.Args))
	for _, a := range c.Args {
		v, err := evalScalar(a, ev)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch c.Name {
	case "date", "hour", "week", "month", "year":
		t, ok := toTime(arg(0))
		if !ok {
			return nil, nil
		}
		switch c.Name {
		case "date":
			return t.UTC().Format("2006-01-02"), nil
		case "hour":
			return t.UTC().Format("2006-01-02 15:00"), nil
		case "week":
			// ISO week start (Monday), so buckets line up with how people read weeks
			u := t.UTC()
			off := (int(u.Weekday()) + 6) % 7
			return u.AddDate(0, 0, -off).Format("2006-01-02"), nil
		case "month":
			return t.UTC().Format("2006-01"), nil
		case "year":
			return float64(t.UTC().Year()), nil
		}
	case "lower":
		return strings.ToLower(toStr(arg(0))), nil
	case "upper":
		return strings.ToUpper(toStr(arg(0))), nil
	case "length":
		return float64(len(toStr(arg(0)))), nil
	case "abs":
		f, ok := toNum(arg(0))
		if !ok {
			return nil, nil
		}
		return math.Abs(f), nil
	case "round":
		f, ok := toNum(arg(0))
		if !ok {
			return nil, nil
		}
		places := 0.0
		if len(args) > 1 {
			places, _ = toNum(arg(1))
		}
		p := math.Pow(10, places)
		return math.Round(f*p) / p, nil
	case "coalesce":
		for _, a := range args {
			if a != nil {
				return a, nil
			}
		}
		return nil, nil
	case "concat":
		var b strings.Builder
		for _, a := range args {
			b.WriteString(toStr(a))
		}
		return b.String(), nil
	case "replace":
		return strings.ReplaceAll(toStr(arg(0)), toStr(arg(1)), toStr(arg(2))), nil
	case "substr":
		s := toStr(arg(0))
		start, _ := toNum(arg(1))
		i := int(start)
		if i > 0 { // SQL substr is 1-indexed
			i--
		}
		if i < 0 || i > len(s) {
			return "", nil
		}
		s = s[i:]
		if len(args) > 2 {
			n, _ := toNum(arg(2))
			if int(n) < len(s) && n >= 0 {
				s = s[:int(n)]
			}
		}
		return s, nil
	case "cast_number":
		f, ok := toNum(arg(0))
		if !ok {
			return nil, nil
		}
		return f, nil
	}
	return nil, fmt.Errorf("unknown function %q", c.Name)
}

// ---- value helpers ----------------------------------------------------------------------

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

func toNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case time.Time:
		return float64(x.UnixNano()), true
	}
	return 0, false
}

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case float64:
		// integers print without a trailing .000000, so grouping keys read as "1" not "1.000000"
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprintf("%v", v)
}

func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// compare orders two values. Numbers compare numerically, times chronologically, everything
// else as strings. Mixed types fall back to string comparison so a query never errors mid-scan
// on one odd row — analytics properties are user-supplied and genuinely heterogeneous.
func compare(a, b any) int {
	if at, ok := a.(time.Time); ok {
		if bt, ok2 := toTime(b); ok2 {
			return at.Compare(bt)
		}
	}
	if bt, ok := b.(time.Time); ok {
		if at, ok2 := toTime(a); ok2 {
			return at.Compare(bt)
		}
	}
	_, aIsStr := a.(string)
	_, bIsStr := b.(string)
	if !(aIsStr && bIsStr) {
		af, aok := toNum(a)
		bf, bok := toNum(b)
		if aok && bok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			}
			return 0
		}
	}
	return strings.Compare(toStr(a), toStr(b))
}

// likeMatch implements SQL LIKE: % is any run, _ is one character. Written as a linear matcher
// rather than by building a regexp, because the pattern comes from the query and compiling
// user input per row is both slow and a way to get surprised.
func likeMatch(s, pattern string) bool {
	return likeAt(s, pattern, 0, 0)
}

func likeAt(s, p string, si, pi int) bool {
	for pi < len(p) {
		switch p[pi] {
		case '%':
			// collapse runs of % so "%%" does not recurse needlessly
			for pi < len(p) && p[pi] == '%' {
				pi++
			}
			if pi == len(p) {
				return true
			}
			for i := si; i <= len(s); i++ {
				if likeAt(s, p, i, pi) {
					return true
				}
			}
			return false
		case '_':
			if si >= len(s) {
				return false
			}
			si++
			pi++
		default:
			if si >= len(s) || s[si] != p[pi] {
				return false
			}
			si++
			pi++
		}
	}
	return si == len(s)
}
