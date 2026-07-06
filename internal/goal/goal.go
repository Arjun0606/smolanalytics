// Package goal stores named conversion goals — "what counts as success on this
// site" — defined once, reusable everywhere. A goal is either an event name
// (signup) or a path glob (/thanks*, matched against $pageview paths). Resolution
// answers the founder question: how many unique users converted, at what rate,
// and which channel sent them.
package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Definition is one named goal.
type Definition struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`  // "event" | "path"
	Value   string    `json:"value"` // event name, or path glob like /thanks*
	Created time.Time `json:"created"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	items []Definition
}

func Open(p string) (*Store, error) {
	s := &Store{path: p}
	if p == "" {
		return s, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("goals file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Definition, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Save(d Definition) (Definition, error) {
	if d.Kind != "event" && d.Kind != "path" {
		return Definition{}, fmt.Errorf(`kind must be "event" or "path", got %q`, d.Kind)
	}
	if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Value) == "" {
		return Definition{}, fmt.Errorf("a goal needs a name and a value")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// reject an exact duplicate so a double-click (or re-adding a forgotten goal) can't
	// clutter the goals row with two identical, indistinguishable cards. The handler
	// maps this error to a 400 the create form surfaces inline.
	for _, existing := range s.items {
		if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(d.Name)) {
			return Definition{}, fmt.Errorf("a goal named %q already exists", strings.TrimSpace(d.Name))
		}
		if existing.Kind == d.Kind && existing.Value == d.Value {
			return Definition{}, fmt.Errorf("a goal for that %s already exists", d.Kind)
		}
	}
	d.ID = newID()
	d.Created = time.Now().UTC()
	s.items = append(s.items, d)
	return d, s.persist()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.items {
		if d.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("no goal with id %q", id)
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	return fmt.Sprintf("gl%x", time.Now().UnixNano())
}

// Row is one channel's contribution to a goal.
type Row struct {
	Value string `json:"value"`
	Users int    `json:"users"`
}

// Report is a resolved goal over a period.
type Report struct {
	Goal          string `json:"goal"`
	Kind          string `json:"kind"`
	Value         string `json:"value"`
	PeriodDays    int    `json:"period_days"`
	Conversions   int    `json:"conversions"`    // unique users who hit the goal
	Visitors      int    `json:"visitors"`       // unique users seen in the period
	ConversionPct int    `json:"conversion_pct"` // conversions / visitors
	ByReferrer    []Row  `json:"by_referrer"`    // which channel sent the converters
	ByUTMSource   []Row  `json:"by_utm_source"`  // campaign attribution when tagged
}

// matches reports whether an event satisfies the goal.
func (d Definition) matches(e event.Event) bool {
	switch d.Kind {
	case "event":
		return e.Name == d.Value
	case "path":
		if e.Name != "$pageview" {
			return false
		}
		p, _ := e.Properties["path"].(string)
		ok, err := path.Match(d.Value, p)
		return err == nil && ok
	}
	return false
}

// Resolve computes the goal report over the trailing period. Attribution is
// first-touch within the period: a converter's channel is the referrer/utm_source
// on their FIRST event in the window — the founder question is "which channel
// brought the people who converted", not "what page were they on when they did".
func Resolve(evs []event.Event, d Definition, days int, now time.Time) Report {
	if days <= 0 {
		days = 30
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	from := now.AddDate(0, 0, -days)

	type first struct {
		ts       time.Time
		referrer string
		utm      string
	}
	firsts := map[string]*first{}
	converted := map[string]bool{}
	for _, e := range evs {
		if e.Timestamp.Before(from) || e.Timestamp.After(now) {
			continue
		}
		f := firsts[e.DistinctID]
		if f == nil || e.Timestamp.Before(f.ts) {
			nf := &first{ts: e.Timestamp}
			if r, ok := e.Properties["referrer"].(string); ok {
				nf.referrer = hostOf(r)
			}
			if u, ok := e.Properties["utm_source"].(string); ok {
				nf.utm = u
			}
			firsts[e.DistinctID] = nf
		}
		if d.matches(e) {
			converted[e.DistinctID] = true
		}
	}

	rep := Report{Goal: d.Name, Kind: d.Kind, Value: d.Value, PeriodDays: days,
		Conversions: len(converted), Visitors: len(firsts)}
	if rep.Visitors > 0 {
		rep.ConversionPct = int(float64(rep.Conversions)/float64(rep.Visitors)*100 + 0.5)
	}
	byRef, byUTM := map[string]int{}, map[string]int{}
	for id := range converted {
		f := firsts[id]
		ref := "direct"
		if f != nil && f.referrer != "" {
			ref = f.referrer
		}
		byRef[ref]++
		if f != nil && f.utm != "" {
			byUTM[f.utm]++
		}
	}
	rep.ByReferrer = toRows(byRef)
	rep.ByUTMSource = toRows(byUTM)
	return rep
}

func toRows(m map[string]int) []Row {
	out := make([]Row, 0, len(m))
	for v, n := range m {
		out = append(out, Row{Value: v, Users: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Users != out[j].Users {
			return out[i].Users > out[j].Users
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func hostOf(ref string) string {
	ref = strings.TrimPrefix(strings.TrimPrefix(ref, "https://"), "http://")
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		ref = ref[:i]
	}
	return strings.TrimPrefix(ref, "www.")
}
