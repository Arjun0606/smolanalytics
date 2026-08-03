// Package sql is a small, read-only SQL dialect over the event stream.
//
// It exists so an agent can answer a question nobody pre-built a report for. Every other report
// in this codebase answers one shape of question; this answers the rest, which is the difference
// between "we support the twenty things we shipped" and "ask anything".
//
// Three deliberate constraints:
//
//   - READ ONLY BY CONSTRUCTION. There is no INSERT, UPDATE, DELETE or DDL in the grammar, so
//     there is nothing to escape from and no injection surface. A write is a parse error, not a
//     permission check that could be wrong.
//   - STREAMING. Aggregation happens as events go past, so memory is O(distinct groups) rather
//     than O(events). A GROUP BY over ten million events holds a few thousand accumulators. This
//     is the same lesson as the dashboard render: never hold history to compute over it.
//   - NO DEPENDENCIES. The binary has none, which is a claim worth more than the weeks this cost
//     to hand-write. Embedding SQLite would have been faster to build and would have materialized
//     every row into a table first.
//
// The dialect is a SUBSET and says so loudly, because an agent that writes valid SQL and gets
// "unsupported" back has a worse experience than one told the grammar up front. Grammar() returns
// the exact supported surface for the tool description.
package sql

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ---- AST --------------------------------------------------------------------------------

type Query struct {
	Select  []SelectItem
	Where   Expr
	GroupBy []Expr
	Having  Expr
	OrderBy []OrderItem
	Limit   int
}

type SelectItem struct {
	Expr  Expr
	Alias string
}

type OrderItem struct {
	Expr Expr
	Desc bool
}

type Expr interface{ String() string }

// Col is a bare event field: name, distinct_id, timestamp, id.
type Col struct{ Name string }

// Prop is a property lookup, written prop.country or properties.country.
type Prop struct{ Key string }

// Lit is a literal string, number, boolean or NULL.
type Lit struct{ Val any }

// Star is `*`, only legal inside count(*).
type Star struct{}

// Ref is an identifier that is not a known column — it may still be a SELECT alias, which is
// only knowable once the whole statement is parsed. Resolved by resolveRefs before execution;
// anything still unresolved then is a genuine typo and becomes an error there.
type Ref struct{ Name string }

// Binary is a comparison, arithmetic or boolean operator.
type Binary struct {
	Op          string
	Left, Right Expr
}

// Unary is NOT, or unary minus.
type Unary struct {
	Op string
	X  Expr
}

// Call is a function or aggregate: count, sum, avg, min, max, date, hour, lower, ...
type Call struct {
	Name     string
	Args     []Expr
	Distinct bool // count(distinct x)
}

// InList is `x IN (a, b, c)`.
type InList struct {
	X      Expr
	Vals   []Expr
	Negate bool
}

// IsNull is `x IS NULL` / `x IS NOT NULL`.
type IsNull struct {
	X      Expr
	Negate bool
}

func (c Col) String() string  { return c.Name }
func (p Prop) String() string { return "prop." + p.Key }
func (Star) String() string   { return "*" }
func (r Ref) String() string  { return r.Name }
func (l Lit) String() string  { return fmt.Sprintf("%v", l.Val) }
func (b Binary) String() string {
	return "(" + b.Left.String() + " " + b.Op + " " + b.Right.String() + ")"
}
func (u Unary) String() string  { return u.Op + " " + u.X.String() }
func (i InList) String() string { return i.X.String() + " IN (...)" }
func (i IsNull) String() string { return i.X.String() + " IS NULL" }
func (c Call) String() string {
	parts := make([]string, 0, len(c.Args))
	for _, a := range c.Args {
		parts = append(parts, a.String())
	}
	d := ""
	if c.Distinct {
		d = "distinct "
	}
	return c.Name + "(" + d + strings.Join(parts, ", ") + ")"
}

// ---- lexer ------------------------------------------------------------------------------

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tPunct
)

type token struct {
	kind tokKind
	s    string
	num  float64
	pos  int
}

// keywords are matched case-insensitively but compared in upper case, so `select` and `SELECT`
// parse identically — an agent writes either.
var keywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "GROUP": true, "BY": true, "HAVING": true,
	"ORDER": true, "LIMIT": true, "AND": true, "OR": true, "NOT": true, "IN": true, "IS": true,
	"NULL": true, "LIKE": true, "AS": true, "ASC": true, "DESC": true, "DISTINCT": true,
	"TRUE": true, "FALSE": true,
}

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-': // line comment
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '\'' || c == '"':
			quote := byte(c)
			j := i + 1
			var b strings.Builder
			for j < len(s) {
				if s[j] == quote {
					// '' inside a quoted string is an escaped quote, per SQL
					if j+1 < len(s) && s[j+1] == quote {
						b.WriteByte(quote)
						j += 2
						continue
					}
					break
				}
				b.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string starting at position %d", i)
			}
			out = append(out, token{kind: tString, s: b.String(), pos: i})
			i = j + 1
		case unicode.IsDigit(c):
			j := i
			for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
				j++
			}
			f, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q", s[i:j])
			}
			out = append(out, token{kind: tNumber, num: f, s: s[i:j], pos: i})
			i = j
		case unicode.IsLetter(c) || c == '_' || c == '$':
			j := i
			// '.' is part of an identifier so prop.country and $current_url lex as one token
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_' || s[j] == '.' || s[j] == '$') {
				j++
			}
			out = append(out, token{kind: tIdent, s: s[i:j], pos: i})
			i = j
		default:
			// two-character operators first, so <= does not lex as < then =
			if i+1 < len(s) {
				two := s[i : i+2]
				if two == ">=" || two == "<=" || two == "!=" || two == "<>" {
					out = append(out, token{kind: tPunct, s: two, pos: i})
					i += 2
					continue
				}
			}
			if strings.ContainsRune("(),*=<>+-/%", c) {
				out = append(out, token{kind: tPunct, s: string(c), pos: i})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q at position %d", c, i)
		}
	}
	out = append(out, token{kind: tEOF, pos: len(s)})
	return out, nil
}

// ---- parser -----------------------------------------------------------------------------

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }
func (p *parser) kw() string {
	t := p.peek()
	if t.kind == tIdent {
		return strings.ToUpper(t.s)
	}
	return ""
}
func (p *parser) isKw(k string) bool { return p.kw() == k }

func (p *parser) acceptKw(k string) bool {
	if p.isKw(k) {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectKw(k string) error {
	if !p.acceptKw(k) {
		return fmt.Errorf("expected %s but found %q", k, p.peek().s)
	}
	return nil
}

func (p *parser) acceptPunct(s string) bool {
	if t := p.peek(); t.kind == tPunct && t.s == s {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectPunct(s string) error {
	if !p.acceptPunct(s) {
		return fmt.Errorf("expected %q but found %q", s, p.peek().s)
	}
	return nil
}

// Parse turns one SELECT statement into a Query. Anything that is not a SELECT over `events` is
// rejected here, which is what makes the whole surface read-only.
func Parse(src string) (*Query, error) {
	src = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(src), ";"))
	if src == "" {
		return nil, fmt.Errorf("empty query")
	}
	// A second statement would let a write ride along behind a legal SELECT. Rejected before
	// anything is parsed rather than trusted to fail later.
	if strings.Contains(src, ";") {
		return nil, fmt.Errorf("only one statement is allowed per query")
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	if !p.isKw("SELECT") {
		return nil, fmt.Errorf("only SELECT is supported (this query starts with %q) — the SQL surface is read-only", p.peek().s)
	}
	p.i++

	q := &Query{Limit: -1}
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		q.Select = append(q.Select, item)
		if !p.acceptPunct(",") {
			break
		}
	}

	if err := p.expectKw("FROM"); err != nil {
		return nil, err
	}
	t := p.next()
	if t.kind != tIdent || !strings.EqualFold(t.s, "events") {
		return nil, fmt.Errorf("the only table is `events` (got %q)", t.s)
	}

	if p.acceptKw("WHERE") {
		if q.Where, err = p.parseExpr(0); err != nil {
			return nil, err
		}
	}
	if p.acceptKw("GROUP") {
		if err := p.expectKw("BY"); err != nil {
			return nil, err
		}
		for {
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			q.GroupBy = append(q.GroupBy, e)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	if p.acceptKw("HAVING") {
		if q.Having, err = p.parseExpr(0); err != nil {
			return nil, err
		}
	}
	if p.acceptKw("ORDER") {
		if err := p.expectKw("BY"); err != nil {
			return nil, err
		}
		for {
			e, err := p.parseExpr(0)
			if err != nil {
				return nil, err
			}
			it := OrderItem{Expr: e}
			if p.acceptKw("DESC") {
				it.Desc = true
			} else {
				p.acceptKw("ASC")
			}
			q.OrderBy = append(q.OrderBy, it)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	if p.acceptKw("LIMIT") {
		t := p.next()
		if t.kind != tNumber {
			return nil, fmt.Errorf("LIMIT needs a number, got %q", t.s)
		}
		q.Limit = int(t.num)
		if q.Limit < 0 {
			return nil, fmt.Errorf("LIMIT cannot be negative")
		}
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing input at %q", p.peek().s)
	}
	if err := resolveRefs(q); err != nil {
		return nil, err
	}
	return q, nil
}

// resolveRefs turns leftover identifiers into columns or SELECT aliases, and rejects the rest.
//
// `SELECT count(*) AS n FROM events GROUP BY name ORDER BY n DESC` is the single most natural
// query an agent writes, and it requires knowing the alias list — which only exists once the
// whole statement has been read. So unknown identifiers are parsed optimistically and settled
// here.
//
// A real column always wins over an alias of the same name, matching what every SQL engine does
// and avoiding a query whose meaning depends on the alias list.
func resolveRefs(q *Query) error {
	// SELECT is resolved first and WITHOUT aliases: an alias cannot refer to itself or to one
	// declared later, and allowing it would make evaluation order load-bearing.
	for i := range q.Select {
		e, err := resolveExpr(q.Select[i].Expr, nil, "SELECT")
		if err != nil {
			return err
		}
		q.Select[i].Expr = e
	}
	aliases := map[string]Expr{}
	for _, s := range q.Select {
		if s.Alias != "" {
			aliases[strings.ToLower(s.Alias)] = s.Expr
		}
	}
	// WHERE is deliberately strict. It runs per event, before any grouping exists, so an alias
	// over an aggregate could not be evaluated there — and silently allowing the non-aggregate
	// ones would make which aliases work in WHERE depend on their definition.
	e, err := resolveExpr(q.Where, nil, "WHERE")
	if err != nil {
		return err
	}
	q.Where = e
	for i := range q.GroupBy {
		if e, err = resolveExpr(q.GroupBy[i], aliases, "GROUP BY"); err != nil {
			return err
		}
		q.GroupBy[i] = e
	}
	if q.Having, err = resolveExpr(q.Having, aliases, "HAVING"); err != nil {
		return err
	}
	for i := range q.OrderBy {
		if e, err = resolveExpr(q.OrderBy[i].Expr, aliases, "ORDER BY"); err != nil {
			return err
		}
		q.OrderBy[i].Expr = e
	}
	return nil
}

func resolveExpr(e Expr, aliases map[string]Expr, clause string) (Expr, error) {
	switch v := e.(type) {
	case nil:
		return nil, nil
	case Ref:
		lower := strings.ToLower(v.Name)
		if isColumn(lower) {
			return Col{Name: lower}, nil
		}
		if target, ok := aliases[lower]; ok {
			return target, nil
		}
		hint := "use prop.<key> for properties"
		if aliases == nil && clause == "WHERE" {
			hint = "use prop.<key> for properties; column aliases are not available in WHERE"
		}
		return nil, fmt.Errorf("unknown column %q in %s — event columns are %s; %s",
			v.Name, clause, strings.Join(columnNames(), ", "), hint)
	case Binary:
		l, err := resolveExpr(v.Left, aliases, clause)
		if err != nil {
			return nil, err
		}
		r, err := resolveExpr(v.Right, aliases, clause)
		if err != nil {
			return nil, err
		}
		return Binary{Op: v.Op, Left: l, Right: r}, nil
	case Unary:
		x, err := resolveExpr(v.X, aliases, clause)
		if err != nil {
			return nil, err
		}
		return Unary{Op: v.Op, X: x}, nil
	case Call:
		args := make([]Expr, 0, len(v.Args))
		for _, a := range v.Args {
			r, err := resolveExpr(a, aliases, clause)
			if err != nil {
				return nil, err
			}
			args = append(args, r)
		}
		return Call{Name: v.Name, Args: args, Distinct: v.Distinct}, nil
	case InList:
		x, err := resolveExpr(v.X, aliases, clause)
		if err != nil {
			return nil, err
		}
		vals := make([]Expr, 0, len(v.Vals))
		for _, a := range v.Vals {
			r, err := resolveExpr(a, aliases, clause)
			if err != nil {
				return nil, err
			}
			vals = append(vals, r)
		}
		return InList{X: x, Vals: vals, Negate: v.Negate}, nil
	case IsNull:
		x, err := resolveExpr(v.X, aliases, clause)
		if err != nil {
			return nil, err
		}
		return IsNull{X: x, Negate: v.Negate}, nil
	}
	return e, nil
}

func (p *parser) parseSelectItem() (SelectItem, error) {
	e, err := p.parseExpr(0)
	if err != nil {
		return SelectItem{}, err
	}
	it := SelectItem{Expr: e, Alias: defaultAlias(e)}
	if p.acceptKw("AS") {
		t := p.next()
		if t.kind != tIdent && t.kind != tString {
			return SelectItem{}, fmt.Errorf("expected a column name after AS, got %q", t.s)
		}
		it.Alias = t.s
	} else if t := p.peek(); t.kind == tIdent && !keywords[strings.ToUpper(t.s)] {
		// `count(*) total` — alias without AS
		p.i++
		it.Alias = t.s
	}
	return it, nil
}

func defaultAlias(e Expr) string {
	switch v := e.(type) {
	case Col:
		return v.Name
	case Prop:
		return v.Key
	default:
		return e.String()
	}
}

// precedence climbing. Lower binds looser.
var precedence = map[string]int{
	"OR": 1, "AND": 2,
	"=": 3, "!=": 3, "<>": 3, "<": 3, "<=": 3, ">": 3, ">=": 3, "LIKE": 3,
	"+": 4, "-": 4,
	"*": 5, "/": 5, "%": 5,
}

func (p *parser) parseExpr(minPrec int) (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		if t := p.peek(); t.kind == tPunct {
			op = t.s
		} else if t.kind == tIdent {
			switch strings.ToUpper(t.s) {
			case "AND", "OR", "LIKE":
				op = strings.ToUpper(t.s)
			case "IN":
				p.i++
				lst, err := p.parseInList(left, false)
				if err != nil {
					return nil, err
				}
				left = lst
				continue
			case "IS":
				p.i++
				neg := p.acceptKw("NOT")
				if err := p.expectKw("NULL"); err != nil {
					return nil, err
				}
				left = IsNull{X: left, Negate: neg}
				continue
			case "NOT":
				// `x NOT IN (...)`
				if strings.ToUpper(p.toks[p.i+1].s) == "IN" {
					p.i += 2
					lst, err := p.parseInList(left, true)
					if err != nil {
						return nil, err
					}
					left = lst
					continue
				}
			}
		}
		prec, ok := precedence[op]
		if !ok || prec < minPrec {
			return left, nil
		}
		p.i++
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		if op == "<>" {
			op = "!="
		}
		left = Binary{Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseInList(x Expr, negate bool) (Expr, error) {
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	var vals []Expr
	for {
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		vals = append(vals, e)
		if !p.acceptPunct(",") {
			break
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return InList{X: x, Vals: vals, Negate: negate}, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.acceptKw("NOT") {
		x, err := p.parseExpr(2)
		if err != nil {
			return nil, err
		}
		return Unary{Op: "NOT", X: x}, nil
	}
	if p.acceptPunct("-") {
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Unary{Op: "-", X: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.next()
	switch {
	case t.kind == tPunct && t.s == "(":
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		return e, p.expectPunct(")")
	case t.kind == tPunct && t.s == "*":
		return Star{}, nil
	case t.kind == tString:
		return Lit{Val: t.s}, nil
	case t.kind == tNumber:
		return Lit{Val: t.num}, nil
	case t.kind == tIdent:
		up := strings.ToUpper(t.s)
		switch up {
		case "NULL":
			return Lit{Val: nil}, nil
		case "TRUE":
			return Lit{Val: true}, nil
		case "FALSE":
			return Lit{Val: false}, nil
		}
		// function call
		if p.peek().kind == tPunct && p.peek().s == "(" {
			p.i++
			c := Call{Name: strings.ToLower(t.s)}
			if p.acceptKw("DISTINCT") {
				c.Distinct = true
			}
			if !p.acceptPunct(")") {
				for {
					a, err := p.parseExpr(0)
					if err != nil {
						return nil, err
					}
					c.Args = append(c.Args, a)
					if !p.acceptPunct(",") {
						break
					}
				}
				if err := p.expectPunct(")"); err != nil {
					return nil, err
				}
			}
			if !knownFunc(c.Name) {
				return nil, fmt.Errorf("unknown function %q — supported: %s", c.Name, strings.Join(funcNames(), ", "))
			}
			return c, nil
		}
		lower := strings.ToLower(t.s)
		if rest, ok := strings.CutPrefix(lower, "prop."); ok {
			return Prop{Key: propKeyCase(t.s, rest)}, nil
		}
		if rest, ok := strings.CutPrefix(lower, "properties."); ok {
			return Prop{Key: propKeyCase(t.s, rest)}, nil
		}
		if isColumn(lower) {
			return Col{Name: lower}, nil
		}
		// Might be a SELECT alias used in GROUP BY / HAVING / ORDER BY, which cannot be checked
		// until the whole statement is parsed.
		return Ref{Name: t.s}, nil
	}
	return nil, fmt.Errorf("unexpected %q", t.s)
}

// propKeyCase preserves the ORIGINAL case of a property key. Property names are user data, and
// `prop.userId` and `prop.userid` are different keys — lowercasing them to match SQL's
// case-insensitive identifiers would silently return nothing.
func propKeyCase(orig, lowerRest string) string {
	if i := strings.Index(strings.ToLower(orig), "."); i >= 0 {
		return orig[i+1:]
	}
	return lowerRest
}
