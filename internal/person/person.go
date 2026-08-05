// Package person builds person profiles — the last-known traits of every user — from the event
// log, at query time.
//
// Derived, never stored. That is the whole architectural point of this engine: reports are pure
// functions of raw events with no rollups, which is what makes any number re-checkable against
// its own rows. A mutable profile table would be the first piece of state in the product that
// could disagree with the events, and the first number nobody could verify. So profiles are
// recomputed like everything else, and a trait is simply "the most recent value the events say".
//
// PostHog and Mixpanel both keep person tables, and both spend real support effort on the
// question "why does the profile say pro when the events say free". That question cannot be asked
// here, because there is nothing for the events to disagree with.
package person

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// IdentifyEvent carries traits explicitly. Any event may also carry them via SetProp.
const (
	IdentifyEvent = "$identify"
	// SetProp is the nested bag convention ({"$set": {"plan": "pro"}}), so a trait can ride along
	// with an ordinary event instead of requiring a second call.
	SetProp = "$set"
)

// Profile is one person as the events describe them.
type Profile struct {
	DistinctID string         `json:"distinct_id"`
	Traits     map[string]any `json:"traits,omitempty"`
	FirstSeen  time.Time      `json:"first_seen"`
	LastSeen   time.Time      `json:"last_seen"`
	Events     int            `json:"events"`
	// TraitAt records when each trait took its current value. It is what makes "last known" a
	// statement about time rather than about which event happened to be processed last.
	TraitAt map[string]time.Time `json:"-"`
}

// Compute derives every profile in the slice.
//
// Last-known wins, decided by TIMESTAMP and never by arrival order. That distinction is the whole
// correctness question here: events do not arrive in order. A backfilled import, a mobile client
// flushing a queue after a week offline, or a retry across a network partition all deliver old
// events late — and "whatever I saw last" would let a week-old trait overwrite today's. Someone
// who upgraded to pro on Monday would show as free on Friday because their offline phone finally
// synced, and nothing anywhere would look broken.
func Compute(evs []event.Event) map[string]*Profile {
	out := map[string]*Profile{}
	for _, e := range evs {
		if e.DistinctID == "" {
			continue
		}
		p := out[e.DistinctID]
		if p == nil {
			p = &Profile{DistinctID: e.DistinctID, Traits: map[string]any{}, TraitAt: map[string]time.Time{}}
			out[e.DistinctID] = p
		}
		ts := e.Timestamp.UTC()
		p.Events++
		if p.FirstSeen.IsZero() || ts.Before(p.FirstSeen) {
			p.FirstSeen = ts
		}
		if ts.After(p.LastSeen) {
			p.LastSeen = ts
		}
		if e.Name == IdentifyEvent {
			apply(p, e.Properties, ts)
		}
		if set, ok := e.Properties[SetProp].(map[string]any); ok {
			apply(p, set, ts)
		}
	}
	return out
}

// reserved are the transport and enrichment properties that describe the EVENT rather than the
// person. Promoting them to traits would make "plan" and "session_id" the same kind of thing, and
// a profile full of session ids is a profile nobody can filter on.
var reserved = map[string]bool{
	SetProp: true, "session_id": true, "path": true, "referrer": true, "title": true,
	"env": true, "site": true, "engaged_ms": true, "$feature_flag": true,
	"$feature_flag_response": true, "$bucket_id": true,
}

func apply(p *Profile, props map[string]any, ts time.Time) {
	for k, v := range props {
		if k == "" || reserved[k] || v == nil {
			continue
		}
		// Strictly newer wins. On an exact tie the existing value stands, so replaying the same
		// event twice is a no-op rather than a coin flip decided by map iteration order.
		if at, seen := p.TraitAt[k]; seen && !ts.After(at) {
			continue
		}
		p.Traits[k] = v
		p.TraitAt[k] = ts
	}
}

// List returns profiles in a deterministic order: most recently seen first, ties by id. Map
// iteration order is random in Go, and a "top users" list that reshuffles on every load is the
// kind of thing that makes a reader distrust every other number on the page.
func List(profiles map[string]*Profile, limit int) []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].DistinctID < out[j].DistinctID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// TraitValues counts how many people hold each value of a trait — the breakdown that makes a
// trait usable as a segment rather than a curiosity.
func TraitValues(profiles map[string]*Profile, trait string) []Value {
	counts := map[string]int{}
	for _, p := range profiles {
		if v, ok := p.Traits[trait]; ok {
			counts[toStr(v)]++
		}
	}
	out := make([]Value, 0, len(counts))
	for v, n := range counts {
		out = append(out, Value{Value: v, People: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].People != out[j].People {
			return out[i].People > out[j].People
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// Value is one trait value and how many people hold it.
type Value struct {
	Value  string `json:"value"`
	People int    `json:"people"`
}

// Traits lists every trait key seen, most widely held first, so a UI can offer real options
// rather than asking someone to remember what they instrumented.
func Traits(profiles map[string]*Profile) []Value {
	counts := map[string]int{}
	for _, p := range profiles {
		for k := range p.Traits {
			counts[k]++
		}
	}
	out := make([]Value, 0, len(counts))
	for k, n := range counts {
		out = append(out, Value{Value: k, People: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].People != out[j].People {
			return out[i].People > out[j].People
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// Matching returns the ids of people whose trait equals value — the bridge from a person
// property to the event-level reports, which is the thing a person store is actually for.
func Matching(profiles map[string]*Profile, trait, value string) map[string]bool {
	out := map[string]bool{}
	for id, p := range profiles {
		if v, ok := p.Traits[trait]; ok && toStr(v) == value {
			out[id] = true
		}
	}
	return out
}

// toStr stringifies a trait value exactly the way query.toStr does, so a person filter and an
// event filter can never disagree about what "plan = 50" means.
func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
