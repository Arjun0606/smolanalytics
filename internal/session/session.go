// Package session reconstructs a user's journey from events already captured — pages, clicks (with
// positions), rage-clicks, and timing — and plays it back. It is NOT pixel-perfect DOM replay
// (that would need a heavy recorder + a separate blob store, breaking the single-binary model);
// it is an event-based session inspector, computed at query time over the existing event log.
package session

import (
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

const gapMinutes = 30 // inactivity gap that starts a new session

// Session is one visit summary.
type Session struct {
	DistinctID  string    `json:"distinct_id"`
	Start       time.Time `json:"start"`
	StartUnix   int64     `json:"start_unix"` // stable handle for fetching the detail
	End         time.Time `json:"end"`
	DurationSec int       `json:"duration_sec"`
	Events      int       `json:"events"`
	Pages       int       `json:"pages"` // distinct paths visited
	RageClicks  int       `json:"rage_clicks"`
	EntryPath   string    `json:"entry_path"`
	ExitPath    string    `json:"exit_path"`
}

// Step is one moment in a session's playback.
type Step struct {
	T    int    `json:"t"` // ms since session start
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	X    int    `json:"x,omitempty"`
	Y    int    `json:"y,omitempty"`
	VW   int    `json:"vw,omitempty"`
	Text string `json:"text,omitempty"`
}

// Detail is a session summary plus its ordered playback steps.
type Detail struct {
	Session
	Steps []Step `json:"steps"`
}

var now = func() time.Time { return time.Now().UTC() }

// Sessions lists recent sessions across users, newest first, capped at limit (0 = 100).
func Sessions(evs []event.Event, days, limit int) []Session {
	if limit <= 0 {
		limit = 100
	}
	var cutoff time.Time
	if days > 0 {
		cutoff = now().AddDate(0, 0, -days)
	}
	byUser := map[string][]event.Event{}
	for _, e := range evs {
		if days > 0 && e.Timestamp.Before(cutoff) {
			continue
		}
		if e.DistinctID == "" {
			continue
		}
		byUser[e.DistinctID] = append(byUser[e.DistinctID], e)
	}
	var out []Session
	for id, ues := range byUser {
		for _, grp := range splitSessions(ues) {
			out = append(out, summarize(id, grp))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.After(out[j].Start)
		}
		return out[i].DistinctID < out[j].DistinctID // deterministic tie-break
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// One reconstructs one session's playback: the events for distinctID whose session starts at
// startUnix (from a Sessions() row). Returns ok=false if no such session exists.
func One(evs []event.Event, distinctID string, startUnix int64) (Detail, bool) {
	var ues []event.Event
	for _, e := range evs {
		if e.DistinctID == distinctID {
			ues = append(ues, e)
		}
	}
	for _, grp := range splitSessions(ues) {
		if grp[0].Timestamp.Unix() != startUnix {
			continue
		}
		d := Detail{Session: summarize(distinctID, grp)}
		start := grp[0].Timestamp
		for _, e := range grp {
			step := Step{T: int(e.Timestamp.Sub(start).Milliseconds()), Name: e.Name}
			if p, _ := e.Properties["path"].(string); p != "" {
				step.Path = p
			}
			step.X = toInt(e.Properties["x"])
			step.Y = toInt(e.Properties["y"])
			step.VW = toInt(e.Properties["vw"])
			if t, _ := e.Properties["text"].(string); t != "" {
				step.Text = t
			}
			d.Steps = append(d.Steps, step)
		}
		return d, true
	}
	return Detail{}, false
}

// splitSessions sorts a user's events by time and splits them on the inactivity gap.
func splitSessions(ues []event.Event) [][]event.Event {
	if len(ues) == 0 {
		return nil
	}
	sorted := make([]event.Event, len(ues))
	copy(sorted, ues)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp.Before(sorted[j].Timestamp) })
	var groups [][]event.Event
	var cur []event.Event
	for i, e := range sorted {
		if i > 0 && e.Timestamp.Sub(sorted[i-1].Timestamp) > gapMinutes*time.Minute {
			groups = append(groups, cur)
			cur = nil
		}
		cur = append(cur, e)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

func summarize(id string, grp []event.Event) Session {
	s := Session{DistinctID: id, Start: grp[0].Timestamp, StartUnix: grp[0].Timestamp.Unix(), End: grp[len(grp)-1].Timestamp, Events: len(grp)}
	s.DurationSec = int(s.End.Sub(s.Start).Seconds())
	paths := map[string]bool{}
	for _, e := range grp {
		if e.Name == "$rageclick" {
			s.RageClicks++
		}
		if p, _ := e.Properties["path"].(string); p != "" {
			if s.EntryPath == "" {
				s.EntryPath = p
			}
			s.ExitPath = p
			paths[p] = true
		}
	}
	s.Pages = len(paths)
	return s
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
