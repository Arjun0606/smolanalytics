package mcp

// Tool-argument decoding. Two jobs, both honesty-critical:
//
//  1. A malformed or mistyped argument is an ERROR the model can act on — never
//     silently ignored. A dropped filters field would return UNFILTERED data
//     presented as filtered: the exact silent-wrong-number failure this engine
//     exists to prevent.
//  2. The obvious filter shorthand decodes: {"plan":"pro"} means plan eq pro —
//     the tool descriptions themselves suggest that phrasing, so honor it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

// filterShapes heads every filters-decode error: it shows the accepted shapes so
// the model can self-correct on the next call.
const filterShapes = `filters must be an array like [{"property":"plan","op":"eq","value":"pro"}] or an equality map like {"plan":"pro","source":"hn"}`

// FilterSet is []query.Filter that also accepts what an agent naturally writes.
// Three shapes decode:
//   - the canonical array: [{"property":"plan","op":"eq","value":"pro"}]
//   - an equality map: {"plan":"pro","source":"hn"} — each key becomes an eq filter
//   - a bare filter object: {"property":"plan","op":"eq","value":"pro"} — one
//     filter minus its array wrapper (reading it as an equality map would silently
//     filter on a property literally named "property" and return a zeros-report)
//
// Anything else errors with the shapes above — never a silent no-op.
type FilterSet []query.Filter

func (fs *FilterSet) UnmarshalJSON(b []byte) error {
	switch shape := jsonShape(b); shape {
	case "null":
		*fs = nil
		return nil
	case "an array":
		var arr []query.Filter
		if err := json.Unmarshal(b, &arr); err != nil {
			return &argError{fmt.Sprintf("%s — an entry is malformed: %v", filterShapes, err)}
		}
		*fs = arr
		return nil
	case "an object":
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return &argError{fmt.Sprintf("%s — got an unreadable object: %v", filterShapes, err)}
		}
		if _, bareFilter := m["property"]; bareFilter {
			var f query.Filter
			if err := json.Unmarshal(b, &f); err != nil {
				return &argError{fmt.Sprintf("%s — got a malformed filter object: %v", filterShapes, err)}
			}
			*fs = FilterSet{f}
			return nil
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys) // maps are unordered; keep the AND-chain deterministic
		out := make(FilterSet, 0, len(keys))
		for _, k := range keys {
			var v any
			_ = json.Unmarshal(m[k], &v) // m[k] is valid JSON by construction
			switch v.(type) {
			case string, float64, bool:
			default: // null / nested object / nested array — not an equality value
				return &argError{fmt.Sprintf("%s — the map form only expresses equality on scalar values, and %q is %s; use the array form for anything richer", filterShapes, k, jsonShape(m[k]))}
			}
			out = append(out, query.Filter{Property: k, Op: query.Eq, Value: v})
		}
		*fs = out
		return nil
	default:
		return &argError{fmt.Sprintf("%s — got %s", filterShapes, shape)}
	}
}

// argError is a decode failure that already carries its own guidance —
// unmarshalArgs passes it through verbatim instead of re-wrapping it.
type argError struct{ msg string }

func (e *argError) Error() string { return e.msg }

// unmarshalArgs decodes tool arguments; empty args decode to zero values. A decode
// failure comes back as a self-correcting tool error naming the field and the
// expected shape — NEVER discarded, because a silently-dropped argument changes
// what data the answer claims to be.
func unmarshalArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	err := json.Unmarshal(args, v)
	if err == nil {
		return nil
	}
	var ae *argError
	if errors.As(err, &ae) {
		return ae // already guiding (e.g. the FilterSet shapes)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return fmt.Errorf("invalid arguments: %q must be %s — got %s", te.Field, wantShape(te.Type), gotShape(te.Value))
	}
	return fmt.Errorf("invalid arguments: %v — check each field against the tool's input schema", err)
}

// jsonShape names the JSON type of a raw value for error messages.
func jsonShape(b []byte) string {
	for _, c := range b {
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			continue
		case c == '{':
			return "an object"
		case c == '[':
			return "an array"
		case c == '"':
			return "a string"
		case c == 't' || c == 'f':
			return "a boolean"
		case c == 'n':
			return "null"
		default:
			return "a number"
		}
	}
	return "empty"
}

// wantShape names the Go target type in JSON terms.
func wantShape(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.String {
			return "an array of strings"
		}
		return "an array"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Map, reflect.Struct:
		return "an object"
	}
	return t.String()
}

// gotShape prefixes encoding/json's value description ("string", "number", …)
// with an article for readable errors.
func gotShape(v string) string {
	switch v {
	case "object", "array":
		return "an " + v
	case "bool":
		return "a boolean"
	default:
		return "a " + v
	}
}
