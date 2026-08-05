// Package formula evaluates arithmetic across trend series — the derived metrics every mature
// analytics tool has and this engine did not.
//
// "Signups per visitor", "revenue per active user", "the share of sessions that rage-clicked" are
// all one series divided by another, and without them a person has to export two charts and open
// a spreadsheet. That is the moment they stop using the tool.
//
// The whole file is about one thing being right: a derived number must be MORE honest than the
// series it came from, not less. Two failures are easy and both are silent — dividing Tuesday's
// numerator by Wednesday's denominator because the series were not aligned, and rendering a
// divide-by-zero as 0 so "no data" and "genuinely zero" become the same pixel.
package formula

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Point is one bucket of a derived series.
//
// Value is only meaningful when Defined. A float64 cannot carry "undefined" — NaN would be
// swallowed by JSON, and 0 is a lie — so the flag travels alongside it and every consumer has to
// look at it.
type Point struct {
	Date    time.Time `json:"date"`
	Value   float64   `json:"value"`
	Defined bool      `json:"defined"`
	// Why explains an undefined point in words, because "—" in a chart with no reason is the
	// thing that makes someone assume the tool is broken.
	Why string `json:"why,omitempty"`
}

// Series is one named input, already bucketed by the trends engine.
type Series struct {
	Name   string
	Dates  []time.Time
	Values []float64
}

// Result is the derived series plus what it was computed from.
type Result struct {
	Expression string  `json:"expression"`
	Points     []Point `json:"points"`
	// Defined/Undefined counts, so a caller can see at a glance that half the chart is missing
	// rather than discovering it by hovering.
	Defined   int    `json:"defined"`
	Undefined int    `json:"undefined"`
	Note      string `json:"note,omitempty"`
}

// Evaluate computes `expr` over the named series.
//
// Operands are the series names (A, B, signups, …) and decimal literals. Operators are + - * / and
// parentheses, with the usual precedence. Deliberately not a general expression language: this
// runs on user input, and every feature added here is a new way to be surprised by a number.
func Evaluate(expr string, inputs []Series) (Result, error) {
	res := Result{Expression: strings.TrimSpace(expr)}
	if len(inputs) == 0 {
		return res, fmt.Errorf("a formula needs at least one series to compute over")
	}
	// ALIGNMENT FIRST. Combining series bucket-by-bucket only means anything if bucket i is the
	// same instant in every series. Two trends over the same window can still differ in length —
	// one event started being tracked later, or a filter emptied a leading bucket — and zipping
	// them by index would divide Tuesday by Wednesday and produce a chart that is wrong in a way
	// no eye can catch. So the series are indexed BY DATE and only the dates present in all of
	// them survive.
	byName := map[string]map[time.Time]float64{}
	for _, s := range inputs {
		if len(s.Dates) != len(s.Values) {
			return res, fmt.Errorf("series %q has %d dates and %d values", s.Name, len(s.Dates), len(s.Values))
		}
		m := make(map[time.Time]float64, len(s.Dates))
		for i, d := range s.Dates {
			m[d.UTC()] = s.Values[i]
		}
		byName[s.Name] = m
	}
	var dates []time.Time
	for _, d := range inputs[0].Dates {
		d = d.UTC()
		ok := true
		for _, s := range inputs[1:] {
			if _, in := byName[s.Name][d]; !in {
				ok = false
				break
			}
		}
		if ok {
			dates = append(dates, d)
		}
	}
	if len(dates) == 0 {
		return res, fmt.Errorf("the series share no buckets, so there is nothing to combine — they were computed over different windows or granularities")
	}
	if dropped := len(inputs[0].Dates) - len(dates); dropped > 0 {
		res.Note = fmt.Sprintf("%d bucket(s) appear in some series and not others and were left out rather than combined against a missing value", dropped)
	}

	for _, d := range dates {
		env := make(map[string]float64, len(byName))
		for name, m := range byName {
			env[name] = m[d]
		}
		v, why := evalExpr(res.Expression, env)
		p := Point{Date: d, Value: v, Defined: why == ""}
		if !p.Defined {
			p.Why = why
			res.Undefined++
		} else {
			res.Defined++
		}
		res.Points = append(res.Points, p)
	}
	if res.Defined == 0 {
		res.Note = "every bucket is undefined — usually the denominator is zero throughout, which is a fact about the data rather than a failed calculation"
	}
	return res, nil
}

// evalExpr returns the value, or a reason it is undefined.
func evalExpr(expr string, env map[string]float64) (float64, string) {
	p := &parser{src: expr, env: env}
	v, err := p.expr()
	if err != "" {
		return 0, err
	}
	p.ws()
	if p.i < len(p.src) {
		return 0, "unexpected " + string(p.src[p.i])
	}
	// Infinity and NaN must never leave this package as numbers. They serialise as null or
	// crash a chart library, and both read as a bug in the product rather than a hole in the
	// data — which is exactly what an undefined point IS.
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, "the arithmetic produced an undefined value"
	}
	return v, ""
}

// A deliberately small recursive-descent parser. Precedence: + - lowest, * / next, then unary
// minus, then parentheses and operands.
type parser struct {
	src string
	i   int
	env map[string]float64
}

func (p *parser) ws() {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) expr() (float64, string) {
	v, err := p.term()
	if err != "" {
		return 0, err
	}
	for {
		p.ws()
		if p.i >= len(p.src) {
			return v, ""
		}
		op := p.src[p.i]
		if op != '+' && op != '-' {
			return v, ""
		}
		p.i++
		r, err := p.term()
		if err != "" {
			return 0, err
		}
		if op == '+' {
			v += r
		} else {
			v -= r
		}
	}
}

func (p *parser) term() (float64, string) {
	v, err := p.unary()
	if err != "" {
		return 0, err
	}
	for {
		p.ws()
		if p.i >= len(p.src) {
			return v, ""
		}
		op := p.src[p.i]
		if op != '*' && op != '/' {
			return v, ""
		}
		p.i++
		r, err := p.unary()
		if err != "" {
			return 0, err
		}
		if op == '*' {
			v *= r
			continue
		}
		// The honest zero. Rendering this as 0 would make "nobody visited" and "nobody converted
		// out of the people who visited" the same pixel, and someone would ship on it.
		if r == 0 {
			return 0, "the denominator is zero in this bucket, so the ratio does not exist"
		}
		v /= r
	}
}

func (p *parser) unary() (float64, string) {
	p.ws()
	if p.i < len(p.src) && p.src[p.i] == '-' {
		p.i++
		v, err := p.unary()
		return -v, err
	}
	return p.atom()
}

func (p *parser) atom() (float64, string) {
	p.ws()
	if p.i >= len(p.src) {
		return 0, "the formula ends where a value was expected"
	}
	c := p.src[p.i]
	if c == '(' {
		p.i++
		v, err := p.expr()
		if err != "" {
			return 0, err
		}
		p.ws()
		if p.i >= len(p.src) || p.src[p.i] != ')' {
			return 0, "unclosed ("
		}
		p.i++
		return v, ""
	}
	if c >= '0' && c <= '9' || c == '.' {
		start := p.i
		for p.i < len(p.src) && (p.src[p.i] >= '0' && p.src[p.i] <= '9' || p.src[p.i] == '.') {
			p.i++
		}
		var f float64
		if _, e := fmt.Sscanf(p.src[start:p.i], "%g", &f); e != nil {
			return 0, "cannot read the number " + p.src[start:p.i]
		}
		return f, ""
	}
	if isNameByte(c) {
		start := p.i
		for p.i < len(p.src) && isNameByte(p.src[p.i]) {
			p.i++
		}
		name := p.src[start:p.i]
		v, ok := p.env[name]
		if !ok {
			// Naming a series that does not exist must be an ERROR, never a silent zero. A
			// formula with a typo would otherwise render a confident flat line.
			return 0, "no series named " + name + " in this formula"
		}
		return v, ""
	}
	return 0, "unexpected " + string(c)
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}
