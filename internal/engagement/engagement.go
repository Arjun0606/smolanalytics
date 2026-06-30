// Package engagement computes the standard engagement reports — lifecycle
// (new/returning/resurrected/dormant) and stickiness (DAU/WAU/MAU) — that every
// product-analytics tool ships. Deterministic and storage-agnostic.
package engagement

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func dayNum(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// LifecycleDay classifies a day's active users (and the churn out of it).
type LifecycleDay struct {
	Date        time.Time `json:"date"`
	New         int       `json:"new"`         // first-ever activity today
	Returning   int       `json:"returning"`   // active today and yesterday
	Resurrected int       `json:"resurrected"` // active today, not yesterday, but active before
	Dormant     int       `json:"dormant"`     // active yesterday, not today (churned out)
}

// ComputeLifecycle returns the last `days` of lifecycle classification (daily).
func ComputeLifecycle(events []event.Event, days int) []LifecycleDay {
	type u struct {
		days  map[int64]bool
		first int64
	}
	users := map[string]*u{}
	var maxD int64
	have := false
	for _, e := range events {
		d := dayNum(e.Timestamp)
		uu := users[e.DistinctID]
		if uu == nil {
			uu = &u{days: map[int64]bool{}, first: d}
			users[e.DistinctID] = uu
		}
		uu.days[d] = true
		if d < uu.first {
			uu.first = d
		}
		if !have || d > maxD {
			maxD, have = d, true
		}
	}
	if !have {
		return nil
	}
	if days < 1 {
		days = 30
	}
	lo := maxD - int64(days) + 1
	out := make([]LifecycleDay, 0, days)
	for d := lo; d <= maxD; d++ {
		row := LifecycleDay{Date: time.Unix(d*86400, 0).UTC()}
		for _, uu := range users {
			active, prev := uu.days[d], uu.days[d-1]
			switch {
			case active && d == uu.first:
				row.New++
			case active && prev:
				row.Returning++
			case active:
				row.Resurrected++
			case prev:
				row.Dormant++
			}
		}
		out = append(out, row)
	}
	return out
}

// Stickiness is the classic engagement ratio.
type Stickiness struct {
	DAU        int     `json:"dau"`
	WAU        int     `json:"wau"`
	MAU        int     `json:"mau"`
	DAUoverMAU float64 `json:"dau_over_mau"`
}

// ComputeStickiness counts distinct users active in the trailing 1/7/30 days from
// asof (defaults to now). DAU/MAU is the stickiness ratio.
func ComputeStickiness(events []event.Event, asof time.Time) Stickiness {
	if asof.IsZero() {
		asof = time.Now().UTC()
	}
	d1, d7, d30 := asof.AddDate(0, 0, -1), asof.AddDate(0, 0, -7), asof.AddDate(0, 0, -30)
	dau, wau, mau := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range events {
		// inclusive lower bound (!Before == >=) so "trailing N days" includes the
		// window boundary, consistent with the funnel.
		if !e.Timestamp.Before(d30) {
			mau[e.DistinctID] = true
		}
		if !e.Timestamp.Before(d7) {
			wau[e.DistinctID] = true
		}
		if !e.Timestamp.Before(d1) {
			dau[e.DistinctID] = true
		}
	}
	s := Stickiness{DAU: len(dau), WAU: len(wau), MAU: len(mau)}
	if s.MAU > 0 {
		s.DAUoverMAU = float64(s.DAU) / float64(s.MAU)
	}
	return s
}
