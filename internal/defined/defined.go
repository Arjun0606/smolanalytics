// Package defined implements retroactive, zero-code events — the Heap wedge. A person
// with autocapture flowing has raw $click / $pageview / $form_submit rows; a defined
// event names a slice of them ("checkout = $click where text contains Buy") and makes it
// a first-class event across EVERY report, retroactive to install, with no tracking code
// and no coding agent. It is the non-agent instrumentation path.
//
// A Store decorator injects a synthetic event for every autocaptured row that matches a
// definition, so funnels, trends, retention, and the ask bar see the defined name like
// any tracked event. Reports already recompute from history, so the backfill is free.
package defined

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store"
)

// baseEvents are the autocapture events a definition may be built from.
var baseEvents = map[string]bool{"$click": true, "$pageview": true, "$form_submit": true, "$rageclick": true, "$deadclick": true}

// Condition matches one autocapture property. Fields are the flat props the SDK stamps.
type Condition struct {
	Field string `json:"field"` // text | id | classes | href | path | tag | name
	Op    string `json:"op"`    // equals | contains | prefix
	Value string `json:"value"`
}

// Definition is one retroactive event: a name over a base autocapture event + AND-ed
// conditions.
type Definition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Event       string      `json:"event"` // base autocapture event
	Where       []Condition `json:"where"`
	Created     time.Time   `json:"created"`
}

func fieldOf(e event.Event, field string) string {
	switch field {
	case "text", "id", "classes", "href", "path", "tag", "name":
		s, _ := e.Properties[field].(string)
		return s
	}
	return ""
}

func condMatch(v, op, val string) bool {
	v, val = strings.ToLower(v), strings.ToLower(val)
	switch op {
	case "equals":
		return v == val
	case "prefix":
		return strings.HasPrefix(v, val)
	default: // contains
		return strings.Contains(v, val)
	}
}

// Matches reports whether an autocaptured event belongs to this definition.
func (d Definition) Matches(e event.Event) bool {
	if e.Name != d.Event {
		return false
	}
	for _, c := range d.Where {
		if !condMatch(fieldOf(e, c.Field), c.Op, c.Value) {
			return false
		}
	}
	return true
}

// Store persists the definitions (a small JSON file, like the tracking plan / cohorts).
type Store struct {
	mu   sync.Mutex
	path string
	defs []Definition
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.defs); err != nil {
			return nil, fmt.Errorf("defined-events file corrupt: %w", err)
		}
	}
	return s, nil
}

// Save adds or replaces a definition by name and validates it.
func (s *Store) Save(d Definition) (Definition, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return d, fmt.Errorf("name is required")
	}
	if strings.HasPrefix(d.Name, "$") {
		return d, fmt.Errorf("name %q can't start with $ (reserved for autocapture events)", d.Name)
	}
	if !baseEvents[d.Event] {
		return d, fmt.Errorf("event must be one of $click, $pageview, $form_submit, $rageclick, $deadclick (got %q)", d.Event)
	}
	if len(d.Where) == 0 {
		return d, fmt.Errorf("at least one condition is required (e.g. text contains \"Buy\") — an unconditioned rule would rename every %s", d.Event)
	}
	for i, c := range d.Where {
		if fieldOf(event.Event{Properties: map[string]any{}}, c.Field) == "" && !validField(c.Field) {
			return d, fmt.Errorf("where[%d]: unknown field %q (text|id|classes|href|path|tag|name)", i, c.Field)
		}
		if c.Op != "equals" && c.Op != "contains" && c.Op != "prefix" {
			return d, fmt.Errorf("where[%d]: op must be equals|contains|prefix (got %q)", i, c.Op)
		}
	}
	if d.Created.IsZero() {
		d.Created = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replaced := false
	for i := range s.defs {
		if s.defs[i].Name == d.Name {
			s.defs[i] = d
			replaced = true
			break
		}
	}
	if !replaced {
		s.defs = append(s.defs, d)
	}
	return d, s.persistLocked()
}

func validField(f string) bool {
	switch f {
	case "text", "id", "classes", "href", "path", "tag", "name":
		return true
	}
	return false
}

// Delete removes a definition by name; reports stop resolving it immediately.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.defs[:0]
	for _, d := range s.defs {
		if d.Name != name {
			out = append(out, d)
		}
	}
	s.defs = out
	return s.persistLocked()
}

// List returns a copy of the definitions.
func (s *Store) List() []Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Definition, len(s.defs))
	copy(out, s.defs)
	return out
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.defs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

// --- the Store decorator ---

// wrapped injects a synthetic event for every autocaptured row matching a definition, so
// the defined name is a first-class event everywhere. Ingest/erasure pass straight
// through — definitions are a read-time projection, never stored.
type wrapped struct {
	store.Store
	ds *Store
}

// Wrap decorates s so reads resolve defined events. A nil ds is a no-op passthrough.
func Wrap(s store.Store, ds *Store) store.Store {
	if ds == nil {
		return s
	}
	return &wrapped{Store: s, ds: ds}
}

func (w *wrapped) Range(from, to time.Time) ([]event.Event, error) {
	evs, err := w.Store.Range(from, to)
	if err != nil {
		return nil, err
	}
	defs := w.ds.List()
	if len(defs) == 0 {
		return evs, nil
	}
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		out = append(out, e)
		for _, d := range defs {
			if d.Matches(e) {
				out = append(out, synth(e, d.Name))
			}
		}
	}
	return out, nil
}

func (w *wrapped) Scan(from, to time.Time, fn func(event.Event) error) error {
	defs := w.ds.List()
	return w.Store.Scan(from, to, func(e event.Event) error {
		if err := fn(e); err != nil {
			return err
		}
		for _, d := range defs {
			if d.Matches(e) {
				if err := fn(synth(e, d.Name)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (w *wrapped) Names() ([]string, error) {
	names, err := w.Store.Names()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, d := range w.ds.List() {
		if !seen[d.Name] {
			names = append(names, d.Name)
			seen[d.Name] = true
		}
	}
	sort.Strings(names)
	return names, nil
}

// synth is the projected event: same user, time, and properties as the autocaptured row,
// under the defined name. A stable id keeps it distinct from its source row.
func synth(e event.Event, name string) event.Event {
	e.Name = name
	if e.ID != "" {
		e.ID = e.ID + "#" + name
	}
	return e
}
