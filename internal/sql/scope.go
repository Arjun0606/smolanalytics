package sql

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// ScopedScanner wraps a Scanner with the production-only filter every other report applies.
//
// Exported so the MCP tool and the HTTP endpoint share ONE definition of what an escape hatch
// sees. They diverged once already — run_sql scanned the store raw while every other tool applied
// the default scope, so the same question returned 4 through SQL and 2 through trends. Two
// surfaces, one question, two numbers, and nothing on screen saying which to trust.
type ScopedScanner struct {
	Inner Scanner
	Keep  func(event.Event) bool
}

func (s ScopedScanner) Scan(from, to time.Time, fn func(event.Event) error) error {
	return s.Inner.Scan(from, to, func(e event.Event) error {
		if !s.Keep(e) {
			return nil
		}
		return fn(e)
	})
}

// ExprTouchesEnv reports whether the expression references the env property anywhere.
//
// Walked over the AST rather than matched against Expr.String(): a literal 'prop.env' sitting
// inside a string comparison would fool a text match into silently disabling the default scope,
// which is the kind of bug that only shows up as a number nobody can explain.
func ExprTouchesEnv(e Expr) bool {
	switch t := e.(type) {
	case nil:
		return false
	case Prop:
		return t.Key == "env"
	case Binary:
		return ExprTouchesEnv(t.Left) || ExprTouchesEnv(t.Right)
	case Unary:
		return ExprTouchesEnv(t.X)
	case Call:
		for _, a := range t.Args {
			if ExprTouchesEnv(a) {
				return true
			}
		}
		return false
	case InList:
		if ExprTouchesEnv(t.X) {
			return true
		}
		for _, v := range t.Vals {
			if ExprTouchesEnv(v) {
				return true
			}
		}
		return false
	case IsNull:
		return ExprTouchesEnv(t.X)
	}
	return false
}
