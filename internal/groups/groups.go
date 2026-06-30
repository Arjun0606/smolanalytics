// Package groups computes account-level (B2B) analytics — aggregate by a group
// property (company, account_id, team) instead of by user. Answers "which
// accounts are most active", "how many accounts", "account engagement" — the
// questions other tools gate behind expensive plans. Deterministic.
package groups

import (
	"fmt"
	"sort"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Group is one account: its activity, distinct users, and recency.
type Group struct {
	Value    string    `json:"value"`
	Events   int       `json:"events"`
	Users    int       `json:"users"`
	LastSeen time.Time `json:"last_seen"`
}

// Result is the account roll-up for a group property.
type Result struct {
	Property        string  `json:"property"`
	TotalGroups     int     `json:"total_groups"`
	ActiveGroups7d  int     `json:"active_groups_7d"`
	ActiveGroups30d int     `json:"active_groups_30d"`
	Groups          []Group `json:"groups"`
}

// Compute rolls events up by the group property (events without it are skipped).
// Groups are returned sorted by event volume; limit<=0 means 50.
func Compute(events []event.Event, property string, asof time.Time, limit int) Result {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 50
	}
	type agg struct {
		events int
		users  map[string]bool
		last   time.Time
	}
	groups := map[string]*agg{}
	for _, e := range events {
		v, ok := e.Properties[property]
		if !ok {
			continue
		}
		key := valueOf(v)
		g := groups[key]
		if g == nil {
			g = &agg{users: map[string]bool{}}
			groups[key] = g
		}
		g.events++
		g.users[e.DistinctID] = true
		if e.Timestamp.After(g.last) {
			g.last = e.Timestamp
		}
	}

	d7, d30 := asof.AddDate(0, 0, -7), asof.AddDate(0, 0, -30)
	res := Result{Property: property, TotalGroups: len(groups)}
	out := make([]Group, 0, len(groups))
	for val, g := range groups {
		// inclusive window (!Before == >=) so a group active exactly N days ago counts
		if !g.last.Before(d7) {
			res.ActiveGroups7d++
		}
		if !g.last.Before(d30) {
			res.ActiveGroups30d++
		}
		out = append(out, Group{Value: val, Events: g.events, Users: len(g.users), LastSeen: g.last})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > limit {
		out = out[:limit]
	}
	res.Groups = out
	return res
}

func valueOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v) // numeric/bool account ids keep distinct buckets
}
