package sql

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Result is one answered query. Columns is ordered; Rows are parallel to it.
type Result struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Scanned   int      `json:"scanned"`
	Groups    int      `json:"groups,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Elapsed   string   `json:"elapsed"`
}

// Limits bound what one query may cost. A query surface with no ceiling is an outage waiting
// for its first curious agent.
type Limits struct {
	MaxRows   int           // rows returned
	MaxGroups int           // distinct GROUP BY keys held at once
	Timeout   time.Duration // wall clock
}

func DefaultLimits() Limits {
	return Limits{MaxRows: 1000, MaxGroups: 50_000, Timeout: 10 * time.Second}
}

// ErrTooManyGroups fires when a GROUP BY has unbounded cardinality — grouping by distinct_id or
// by a raw URL on a busy site. Failing with a message that names the cause beats an OOM, and
// beats silently returning the first N groups as though they were the answer.
var ErrTooManyGroups = errors.New("too many distinct groups")

// Scanner streams events. This is the whole store contract the SQL layer needs, and taking the
// streaming one rather than a slice is what keeps memory O(groups) instead of O(events).
type Scanner interface {
	Scan(from, to time.Time, fn func(event.Event) error) error
}

type accumulator struct {
	count    int
	sum      float64
	min, max any
	distinct map[string]struct{}
	seen     bool
}

type group struct {
	key   string
	vals  []any // GROUP BY values, for projection
	accs  map[string]*accumulator
	first event.Event // a representative row, for non-aggregated selects
}

// Run executes a parsed query against a scanner.
func Run(q *Query, sc Scanner, lim Limits) (*Result, error) {
	if q == nil {
		return nil, fmt.Errorf("no query to run")
	}
	start := time.Now()
	if lim.MaxRows <= 0 {
		lim.MaxRows = DefaultLimits().MaxRows
	}
	if lim.MaxGroups <= 0 {
		lim.MaxGroups = DefaultLimits().MaxGroups
	}
	if lim.Timeout <= 0 {
		lim.Timeout = DefaultLimits().Timeout
	}
	deadline := start.Add(lim.Timeout)

	if err := validate(q); err != nil {
		return nil, err
	}

	cols := make([]string, 0, len(q.Select))
	for _, s := range q.Select {
		cols = append(cols, s.Alias)
	}

	aggregated := len(q.GroupBy) > 0 || anyAggregate(q)
	res := &Result{Columns: cols}

	// Collect every aggregate call once so each is accumulated a single time per group even if
	// it appears in SELECT, HAVING and ORDER BY.
	aggs := map[string]Call{}
	for _, s := range q.Select {
		collectAggs(s.Expr, aggs)
	}
	collectAggs(q.Having, aggs)
	for _, o := range q.OrderBy {
		collectAggs(o.Expr, aggs)
	}

	groups := map[string]*group{}
	var order []*group // insertion order, so output is stable without a sort
	scanned := 0
	checkEvery := 4096

	err := sc.Scan(time.Time{}, time.Time{}, func(ev event.Event) error {
		scanned++
		if scanned%checkEvery == 0 && time.Now().After(deadline) {
			return fmt.Errorf("query exceeded %s after scanning %d events — narrow it with WHERE or a shorter time range", lim.Timeout, scanned)
		}
		if q.Where != nil {
			ok, err := evalScalar(q.Where, ev)
			if err != nil {
				return err
			}
			if !truthy(ok) {
				return nil
			}
		}

		if !aggregated {
			// Plain projection: emit and stop as soon as the cap is reached. No sort means no
			// need to hold anything, so a `SELECT * ... LIMIT 20` over 10M events is 20 rows of
			// work — unless ORDER BY forces us to see everything first.
			if len(q.OrderBy) == 0 && len(res.Rows) >= effectiveLimit(q, lim) {
				res.Truncated = true
				return errStop
			}
			row := make([]any, len(q.Select))
			for i, s := range q.Select {
				v, err := evalScalar(s.Expr, ev)
				if err != nil {
					return err
				}
				row[i] = v
			}
			res.Rows = append(res.Rows, row)
			// With ORDER BY we must see every row, but we still refuse to hold unbounded output.
			if len(res.Rows) > lim.MaxGroups {
				return fmt.Errorf("query would return more than %d rows before sorting — add a WHERE clause", lim.MaxGroups)
			}
			return nil
		}

		keyVals := make([]any, len(q.GroupBy))
		var kb strings.Builder
		for i, g := range q.GroupBy {
			v, err := evalScalar(g, ev)
			if err != nil {
				return err
			}
			keyVals[i] = v
			kb.WriteString(toStr(v))
			kb.WriteByte(0x1f) // unit separator: "a"+"b" and "ab"+"" must not collide
		}
		key := kb.String()
		g, ok := groups[key]
		if !ok {
			if len(groups) >= lim.MaxGroups {
				return fmt.Errorf("%w: over %d distinct values for this GROUP BY. Group by something with fewer values, or add a WHERE clause",
					ErrTooManyGroups, lim.MaxGroups)
			}
			g = &group{key: key, vals: keyVals, accs: map[string]*accumulator{}, first: ev}
			groups[key] = g
			order = append(order, g)
		}
		for sig, call := range aggs {
			acc := g.accs[sig]
			if acc == nil {
				acc = &accumulator{}
				g.accs[sig] = acc
			}
			if err := accumulate(acc, call, ev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}
	res.Scanned = scanned

	if aggregated {
		// An aggregate with no GROUP BY is defined over the whole set, so it yields exactly one
		// row even when nothing matched: `count(*)` on an empty selection is 0, not "no answer".
		// Returning no rows here would make a caller report "no data" for a question that has a
		// perfectly good answer.
		if len(order) == 0 && len(q.GroupBy) == 0 {
			order = append(order, &group{accs: map[string]*accumulator{}})
		}
		res.Groups = len(order)
		rows, err := projectGroups(q, order, aggs)
		if err != nil {
			return nil, err
		}
		res.Rows = rows
	}

	if len(q.OrderBy) > 0 {
		if err := sortRows(q, res, aggs, order, aggregated); err != nil {
			return nil, err
		}
	}
	if n := effectiveLimit(q, lim); len(res.Rows) > n {
		res.Rows = res.Rows[:n]
		res.Truncated = true
	}
	res.Elapsed = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

var errStop = errors.New("stop scan")

func effectiveLimit(q *Query, lim Limits) int {
	if q.Limit >= 0 && q.Limit < lim.MaxRows {
		return q.Limit
	}
	return lim.MaxRows
}

func anyAggregate(q *Query) bool {
	for _, s := range q.Select {
		if hasAggregate(s.Expr) {
			return true
		}
	}
	if q.Having != nil && hasAggregate(q.Having) {
		return true
	}
	for _, o := range q.OrderBy {
		if hasAggregate(o.Expr) {
			return true
		}
	}
	return false
}

func collectAggs(e Expr, out map[string]Call) {
	switch v := e.(type) {
	case nil:
		return
	case Call:
		if aggregates[v.Name] {
			out[v.String()] = v
			return
		}
		for _, a := range v.Args {
			collectAggs(a, out)
		}
	case Binary:
		collectAggs(v.Left, out)
		collectAggs(v.Right, out)
	case Unary:
		collectAggs(v.X, out)
	case InList:
		collectAggs(v.X, out)
		for _, a := range v.Vals {
			collectAggs(a, out)
		}
	case IsNull:
		collectAggs(v.X, out)
	}
}

func accumulate(acc *accumulator, c Call, ev event.Event) error {
	// count(*) counts rows and never looks at a value, so it cannot be skipped for being NULL.
	if c.Name == "count" && len(c.Args) == 1 {
		if _, isStar := c.Args[0].(Star); isStar {
			acc.count++
			return nil
		}
	}
	if len(c.Args) == 0 {
		return fmt.Errorf("%s() needs an argument", c.Name)
	}
	v, err := evalScalar(c.Args[0], ev)
	if err != nil {
		return err
	}
	if v == nil {
		return nil // aggregates skip NULLs, as in SQL
	}
	switch c.Name {
	case "count":
		if c.Distinct {
			if acc.distinct == nil {
				acc.distinct = map[string]struct{}{}
			}
			acc.distinct[toStr(v)] = struct{}{}
			return nil
		}
		acc.count++
	case "sum", "avg":
		f, ok := toNum(v)
		if !ok {
			return nil // a non-numeric value contributes nothing rather than failing the query
		}
		acc.sum += f
		acc.count++
	case "min":
		if !acc.seen || compare(v, acc.min) < 0 {
			acc.min = v
		}
		acc.seen = true
	case "max":
		if !acc.seen || compare(v, acc.max) > 0 {
			acc.max = v
		}
		acc.seen = true
	}
	return nil
}

func finish(acc *accumulator, c Call) any {
	if acc == nil {
		if c.Name == "count" {
			return float64(0)
		}
		return nil
	}
	switch c.Name {
	case "count":
		if c.Distinct {
			return float64(len(acc.distinct))
		}
		return float64(acc.count)
	case "sum":
		return acc.sum
	case "avg":
		if acc.count == 0 {
			return nil
		}
		return acc.sum / float64(acc.count)
	case "min":
		return acc.min
	case "max":
		return acc.max
	}
	return nil
}

// projectGroups turns accumulated groups into output rows, applying HAVING.
func projectGroups(q *Query, order []*group, aggs map[string]Call) ([][]any, error) {
	rows := make([][]any, 0, len(order))
	for _, g := range order {
		if q.Having != nil {
			ok, err := evalWithGroup(q.Having, g, aggs)
			if err != nil {
				return nil, err
			}
			if !truthy(ok) {
				continue
			}
		}
		row := make([]any, len(q.Select))
		for i, s := range q.Select {
			v, err := evalWithGroup(s.Expr, g, aggs)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// evalWithGroup evaluates an expression where aggregate calls resolve to the group's accumulated
// value and everything else evaluates against a representative row from the group. Using a
// representative row is what makes `SELECT name, count(*) ... GROUP BY name` work without
// requiring the user to repeat themselves.
func evalWithGroup(e Expr, g *group, aggs map[string]Call) (any, error) {
	switch v := e.(type) {
	case nil:
		return nil, nil
	case Call:
		if aggregates[v.Name] {
			return finish(g.accs[v.String()], v), nil
		}
		args := make([]Expr, 0, len(v.Args))
		for _, a := range v.Args {
			val, err := evalWithGroup(a, g, aggs)
			if err != nil {
				return nil, err
			}
			args = append(args, Lit{Val: val})
		}
		return evalCall(Call{Name: v.Name, Args: args, Distinct: v.Distinct}, g.first)
	case Binary:
		if hasAggregate(v.Left) || hasAggregate(v.Right) {
			l, err := evalWithGroup(v.Left, g, aggs)
			if err != nil {
				return nil, err
			}
			r, err := evalWithGroup(v.Right, g, aggs)
			if err != nil {
				return nil, err
			}
			return evalBinary(Binary{Op: v.Op, Left: Lit{Val: l}, Right: Lit{Val: r}}, g.first)
		}
	case Unary:
		if hasAggregate(v.X) {
			x, err := evalWithGroup(v.X, g, aggs)
			if err != nil {
				return nil, err
			}
			return evalScalar(Unary{Op: v.Op, X: Lit{Val: x}}, g.first)
		}
	}
	return evalScalar(e, g.first)
}

// sortRows orders the result. ORDER BY expressions are computed alongside the output rows rather
// than looked up by name, so `ORDER BY count(*) DESC` works whether or not count(*) was selected.
func sortRows(q *Query, res *Result, aggs map[string]Call, order []*group, aggregated bool) error {
	type keyed struct {
		row  []any
		keys []any
	}
	items := make([]keyed, 0, len(res.Rows))

	if aggregated {
		// Groups that survived HAVING, in the same order projectGroups emitted them.
		kept := make([]*group, 0, len(res.Rows))
		for _, g := range order {
			if q.Having != nil {
				ok, err := evalWithGroup(q.Having, g, aggs)
				if err != nil {
					return err
				}
				if !truthy(ok) {
					continue
				}
			}
			kept = append(kept, g)
		}
		if len(kept) != len(res.Rows) {
			return fmt.Errorf("internal: %d groups but %d rows", len(kept), len(res.Rows))
		}
		for i, row := range res.Rows {
			keys := make([]any, len(q.OrderBy))
			for j, o := range q.OrderBy {
				v, err := evalWithGroup(o.Expr, kept[i], aggs)
				if err != nil {
					return err
				}
				keys[j] = v
			}
			items = append(items, keyed{row: row, keys: keys})
		}
	} else {
		// Non-aggregated: ORDER BY may reference a selected alias, or an expression. Aliases are
		// resolved against the projected row so `ORDER BY total` works after `... AS total`.
		for _, row := range res.Rows {
			keys := make([]any, len(q.OrderBy))
			for j, o := range q.OrderBy {
				if c, ok := o.Expr.(Col); ok {
					if idx := indexOfAlias(q, c.Name); idx >= 0 {
						keys[j] = row[idx]
						continue
					}
				}
				if idx := indexOfAlias(q, o.Expr.String()); idx >= 0 {
					keys[j] = row[idx]
					continue
				}
				return fmt.Errorf("ORDER BY %s must be one of the selected columns", o.Expr.String())
			}
			items = append(items, keyed{row: row, keys: keys})
		}
	}

	sort.SliceStable(items, func(a, b int) bool {
		for j, o := range q.OrderBy {
			x, y := items[a].keys[j], items[b].keys[j]
			// NULLs sort last in both directions, so an empty property never occupies the top
			// of a "worst first" list and reads as a finding.
			switch {
			case x == nil && y == nil:
				continue
			case x == nil:
				return false
			case y == nil:
				return true
			}
			c := compare(x, y)
			if c == 0 {
				continue
			}
			if o.Desc {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	for i, it := range items {
		res.Rows[i] = it.row
	}
	return nil
}

func indexOfAlias(q *Query, name string) int {
	for i, s := range q.Select {
		if strings.EqualFold(s.Alias, name) || s.Expr.String() == name {
			return i
		}
	}
	return -1
}

// validate rejects queries that would produce a misleading answer rather than an error.
func validate(q *Query) error {
	if len(q.Select) == 0 {
		return fmt.Errorf("SELECT needs at least one column")
	}
	for _, s := range q.Select {
		if _, isStar := s.Expr.(Star); isStar {
			return fmt.Errorf("SELECT * is not supported — name the columns you want (%s, or prop.<key>)",
				strings.Join(columnNames(), ", "))
		}
	}
	if len(q.GroupBy) == 0 && q.Having != nil {
		return fmt.Errorf("HAVING requires GROUP BY")
	}
	// A bare column next to an aggregate, with no GROUP BY, is the classic SQL footgun: engines
	// that allow it return an arbitrary row's value beside a total over every row. That is a
	// number a person would act on and it means nothing, so it is refused.
	if len(q.GroupBy) == 0 && anyAggregate(q) {
		for _, s := range q.Select {
			if !hasAggregate(s.Expr) && !isConstant(s.Expr) {
				return fmt.Errorf("%s is selected next to an aggregate but is not grouped — add GROUP BY %s",
					s.Expr.String(), s.Expr.String())
			}
		}
	}
	if len(q.GroupBy) > 0 {
		for _, s := range q.Select {
			if hasAggregate(s.Expr) || isConstant(s.Expr) || isGrouped(s.Expr, q.GroupBy) {
				continue
			}
			return fmt.Errorf("%s is selected but not grouped or aggregated — add it to GROUP BY, or wrap it in an aggregate like max(%s)",
				s.Expr.String(), s.Expr.String())
		}
	}
	return nil
}

func isConstant(e Expr) bool { _, ok := e.(Lit); return ok }

func isGrouped(e Expr, by []Expr) bool {
	s := e.String()
	for _, g := range by {
		if g.String() == s {
			return true
		}
	}
	return false
}

// Grammar is the supported surface, verbatim, for the MCP tool description. An agent that is
// told the grammar writes queries that run; one that has to guess writes SQL that parses
// everywhere else and fails here.
func Grammar() string {
	return `SELECT <expr> [AS alias], ... FROM events
  [WHERE <condition>] [GROUP BY <expr>, ...] [HAVING <condition>]
  [ORDER BY <expr> [ASC|DESC], ...] [LIMIT n]

Read-only: only SELECT parses. The single table is ` + "`events`" + `.

Columns:  name, distinct_id, timestamp, id
Properties: prop.<key>  (e.g. prop.country, prop."$current_url") — case-sensitive, NULL if absent
Aggregates: count(*), count(x), count(distinct x), sum(x), avg(x), min(x), max(x)
Scalars:  date(t), hour(t), week(t), month(t), year(t), lower, upper, length, round,
          abs, coalesce, concat, replace, substr, cast_number
Operators: = != < <= > >= AND OR NOT, IN (...), NOT IN (...), IS [NOT] NULL,
           LIKE ('%' any run, '_' one char), + - * / %

Not supported: JOIN, subqueries, window functions, UNION, CASE, DISTINCT outside count(),
SELECT *. Every column selected alongside an aggregate must be in GROUP BY.

Examples:
  SELECT name, count(*) AS n FROM events GROUP BY name ORDER BY n DESC LIMIT 10
  SELECT date(timestamp) AS day, count(distinct distinct_id) AS users
    FROM events WHERE name = 'signup' GROUP BY day ORDER BY day
  SELECT prop.country AS country, count(*) AS n FROM events
    WHERE prop.plan IS NOT NULL AND timestamp > '2026-07-01'
    GROUP BY country HAVING count(*) > 10 ORDER BY n DESC`
}
