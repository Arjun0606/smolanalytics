package api

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Per-event size limits.
//
// The write key is PUBLIC by design — it ships in the SDK on every customer page, and it has to,
// because the browser needs it to send events. Until now the entire per-event validation was
// "name non-empty, distinct_id non-empty": no size cap on an event, on a property value, or on the
// property bag. There is no rate limit anywhere in this package either.
//
// So anyone who could view a customer's site could read the key out of the page and post
// well-formed, authenticated events carrying megabyte property values until the box died. Not a
// crafted exploit — valid traffic through the documented endpoint. And because a 50KB string
// property costs ~56KB RESIDENT for the life of the process, the memory never comes back without
// a compaction that itself needs twice the heap to run.
//
// These caps make the blast radius bounded. They do not replace a rate limit, which is a separate
// gap worth its own fix.
const (
	// maxEventBytes bounds one event's full serialized size. Comfortably above any legitimate
	// event — the SDK's own autocapture caps every field it collects long before this — and far
	// below the size where a single event meaningfully moves the heap.
	maxEventBytes = 32 << 10 // 32KB
	// maxPropBytes bounds ONE property value. Checked separately from the event total because the
	// realistic abuse is one enormous value rather than a thousand small ones, and naming the
	// offending key is what makes the rejection actionable.
	maxPropBytes = 8 << 10 // 8KB
	// maxProps bounds the number of keys. A property bag with ten thousand keys is not an event,
	// it is a payload wearing one, and each key costs map overhead forever.
	maxProps = 250
)

// checkEventSize returns a human error naming exactly what was too big, or nil.
//
// The message says WHICH event and WHICH property, because the caller is a developer looking at
// their own instrumentation and "event too large" sends them reading their whole tracking plan.
func checkEventSize(e event.Event) error {
	if n := len(e.Properties); n > maxProps {
		return fmt.Errorf("event %q carries %d properties (max %d) — a property describes the event; "+
			"a bag this size is usually a payload that wants to be its own event", e.Name, n, maxProps)
	}
	for k, v := range e.Properties {
		// Only strings and raw JSON can be genuinely large; numbers and bools cannot. Marshalling
		// every value to measure it would cost more than the check saves on a hot ingest path.
		switch t := v.(type) {
		case string:
			if len(t) > maxPropBytes {
				return fmt.Errorf("event %q: property %q is %d bytes (max %d) — store a reference "+
					"or an id, not the document", e.Name, k, len(t), maxPropBytes)
			}
		case json.RawMessage:
			if len(t) > maxPropBytes {
				return fmt.Errorf("event %q: property %q is %d bytes (max %d)", e.Name, k, len(t), maxPropBytes)
			}
		case map[string]any, []any:
			b, err := json.Marshal(t)
			if err == nil && len(b) > maxPropBytes {
				return fmt.Errorf("event %q: property %q is %d bytes nested (max %d) — flatten it, "+
					"or send the parts you will actually query on", e.Name, k, len(b), maxPropBytes)
			}
		}
	}
	// The whole-event check catches what per-property checks cannot: many values each under the
	// per-property cap that together are enormous.
	b, err := json.Marshal(e)
	if err == nil && len(b) > maxEventBytes {
		return fmt.Errorf("event %q serializes to %d bytes (max %d) — this is one event, not a batch",
			e.Name, len(b), maxEventBytes)
	}
	return nil
}
