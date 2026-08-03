package query

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// chainedStampFirstTouch is the implementation StampFirstTouchAll replaced: one full copy of
// history per property. It is kept here, in the test only, as the reference the batched
// version is measured against. The optimization is only worth anything if the numbers on the
// dashboard do not move, so the argument that batching is safe ("the properties are
// independent") gets checked rather than believed.
func chainedStampFirstTouch(evs []event.Event, props []string) []event.Event {
	out := evs
	done := map[string]bool{}
	for _, p := range props {
		if p == "" || done[p] {
			continue
		}
		done[p] = true
		out = stampOneTheOldWay(out, p)
	}
	return out
}

func stampOneTheOldWay(events []event.Event, prop string) []event.Event {
	type ft struct {
		t   int64
		val string
	}
	first := map[string]ft{}
	for _, e := range events {
		v, ok := e.Properties[prop]
		if !ok {
			continue
		}
		val := toStr(v)
		if val == "" {
			continue
		}
		if prop == "referrer" {
			val = hostOfURL(val)
		}
		ts := e.Timestamp.UnixNano()
		if cur, seen := first[e.DistinctID]; !seen || ts < cur.t {
			first[e.DistinctID] = ft{ts, val}
		}
	}
	out := make([]event.Event, len(events))
	for i, e := range events {
		out[i] = e
		if f, ok := first[e.DistinctID]; ok {
			np := make(map[string]any, len(e.Properties)+1)
			for k, v := range e.Properties {
				np[k] = v
			}
			np[prop] = f.val
			out[i].Properties = np
		}
	}
	return out
}

// adversarialEvents deliberately includes the cases where a batched implementation could
// diverge: users whose first touch of one property is on a different event than another,
// events with no properties at all, empty-string values that must NOT count as a first touch,
// referrers that get reduced to a host, non-string values, out-of-order timestamps, and users
// who never carry a given property.
func adversarialEvents() []event.Event {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	return []event.Event{
		// u1: country on the landing event, device only later, referrer needs host reduction
		{ID: "1", Name: "$pageview", DistinctID: "u1", Timestamp: at(0),
			Properties: map[string]any{"country": "US", "referrer": "https://news.ycombinator.com/item?id=1"}},
		{ID: "2", Name: "signup", DistinctID: "u1", Timestamp: at(5),
			Properties: map[string]any{"device": "mobile", "country": "GB"}}, // later country must LOSE to US
		{ID: "3", Name: "checkout", DistinctID: "u1", Timestamp: at(10), Properties: map[string]any{}},

		// u2: arrives OUT OF ORDER — the later-listed event is chronologically first
		{ID: "4", Name: "signup", DistinctID: "u2", Timestamp: at(20), Properties: map[string]any{"source": "twitter"}},
		{ID: "5", Name: "$pageview", DistinctID: "u2", Timestamp: at(2), Properties: map[string]any{"source": "google"}},

		// u3: empty strings must not register as a first touch
		{ID: "6", Name: "$pageview", DistinctID: "u3", Timestamp: at(1),
			Properties: map[string]any{"device": "", "country": "DE"}},
		{ID: "7", Name: "signup", DistinctID: "u3", Timestamp: at(3), Properties: map[string]any{"device": "desktop"}},

		// u4: no properties at all, ever — must keep sharing the caller's nil map
		{ID: "8", Name: "$pageview", DistinctID: "u4", Timestamp: at(4), Properties: nil},

		// u5: non-string values, and a property nobody else has
		{ID: "9", Name: "$pageview", DistinctID: "u5", Timestamp: at(6),
			Properties: map[string]any{"country": 42, "plan": true, "os": "ios"}},

		// u6: only ever carries a property that is NOT in the stamp list
		{ID: "10", Name: "click", DistinctID: "u6", Timestamp: at(7), Properties: map[string]any{"button": "buy"}},
	}
}

func TestStampFirstTouchAllMatchesChained(t *testing.T) {
	evs := adversarialEvents()
	cases := [][]string{
		{"country"},
		{"device", "country"},
		{"source", "channel", "device", "country", "browser", "platform", "os", "referrer"}, // the segmentBlame set
		{"referrer"},
		{"country", "country", "device"}, // duplicates
		{"nonexistent"},
		{},
	}
	for _, props := range cases {
		name := fmt.Sprintf("%v", props)
		want := chainedStampFirstTouch(evs, props)
		got := StampFirstTouchAll(evs, props)
		if len(got) != len(want) {
			t.Fatalf("%s: got %d events, chained produced %d", name, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID {
				t.Fatalf("%s: position %d is event %q, chained had %q", name, i, got[i].ID, want[i].ID)
			}
			if !reflect.DeepEqual(got[i].Properties, want[i].Properties) {
				t.Errorf("%s: event %q properties diverged\n batched: %v\n chained: %v",
					name, got[i].ID, got[i].Properties, want[i].Properties)
			}
		}
	}
}

// The caller's events must come back untouched. segmentBlame stamps and then re-reads `evs`
// separately to decide which properties the conversion event natively carries; if stamping
// mutated the input, that check would see stamped values and skip properties it must stamp,
// which is exactly the bug the Step-3 comment in journey.go describes.
func TestStampFirstTouchAllDoesNotMutateInput(t *testing.T) {
	evs := adversarialEvents()
	before := make([]map[string]any, len(evs))
	for i, e := range evs {
		if e.Properties == nil {
			continue
		}
		cp := make(map[string]any, len(e.Properties))
		for k, v := range e.Properties {
			cp[k] = v
		}
		before[i] = cp
	}
	StampFirstTouchAll(evs, []string{"source", "device", "country", "referrer", "os"})
	for i, e := range evs {
		if !reflect.DeepEqual(e.Properties, before[i]) {
			t.Errorf("input event %q was mutated: now %v, was %v", e.ID, e.Properties, before[i])
		}
	}
}

// One map allocation per event, not one per property. This is the whole point of the change,
// so it is asserted rather than left to a benchmark nobody runs.
func TestStampFirstTouchAllAllocatesOnceRegardlessOfPropertyCount(t *testing.T) {
	evs := make([]event.Event, 0, 5000)
	base := time.Now()
	for i := 0; i < 5000; i++ {
		evs = append(evs, event.Event{
			ID: fmt.Sprintf("e%d", i), Name: "$pageview", DistinctID: fmt.Sprintf("u%d", i%500),
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Properties: map[string]any{
				"source": "google", "channel": "organic", "device": "mobile", "country": "US",
				"browser": "Chrome", "platform": "web", "os": "macOS", "referrer": "https://x.com/a",
			},
		})
	}
	eight := []string{"source", "channel", "device", "country", "browser", "platform", "os", "referrer"}

	batched := testing.AllocsPerRun(3, func() { StampFirstTouchAll(evs, eight) })
	chained := testing.AllocsPerRun(3, func() { chainedStampFirstTouch(evs, eight) })
	t.Logf("8 properties over %d events — batched: %.0f allocs, chained: %.0f allocs (%.1fx fewer)",
		len(evs), batched, chained, chained/batched)

	// Chaining allocates a fresh map per event per property. Batching must stay far below
	// that; 3x is a loose floor that still fails loudly if a chained call creeps back in.
	if batched*3 > chained {
		t.Errorf("batched stamping allocated %.0f vs chained %.0f — the per-property copy is back", batched, chained)
	}
}
