package event

// Numeric reads a property value as a number, and says whether it was one.
//
// One definition, because there were three. internal/flag had numericProp (returning 0 for a
// non-number, which is indistinguishable from a genuine zero) and ratioNumericValue (returning
// ok, which is correct), and internal/query has its own for filter matching. Two of those
// answered the same question differently, which in this codebase is the reliable precursor to
// two surfaces disagreeing about a number in front of a customer.
//
// Deliberately strict: JSON decodes numbers to float64 and strings to string, so a property that
// arrives as "29" was SENT as a string, and quietly parsing it would mean summing values the
// filter engine would not match. If a customer sends amounts as strings, the honest outcome is
// that revenue sizing declines to fire and says so, not that two reports disagree about their
// revenue.
//
// query keeps its own coercion on purpose: filter matching compares a stored value against a
// string from a URL, which is a different question from "is this a number I may sum".
func Numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}
