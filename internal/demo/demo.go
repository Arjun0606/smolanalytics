// Package demo seeds a realistic dataset so `smolanalytics demo` shows a populated,
// beautiful dashboard with zero setup — the 60-second "oh" moment. Seed data uses
// rand (it's fake data); the analytics engine that computes over it stays
// deterministic.
package demo

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store"
)

// Seed populates the store with ~30 days of a SaaS signup→activate→checkout funnel
// plus daily return ("open") activity for retention and trends.
func Seed(s store.Store) error {
	r := rand.New(rand.NewSource(42))
	start := time.Now().UTC().AddDate(0, 0, -29).Truncate(24 * time.Hour)
	id := 0
	emit := func(name, user string, t time.Time) error {
		id++
		return s.Ingest(event.Event{ID: fmt.Sprintf("e%d", id), Name: name, DistinctID: user, Timestamp: t})
	}

	for day := 0; day < 30; day++ {
		dayStart := start.AddDate(0, 0, day)
		// gentle upward trend in signups over the month
		nSignups := 18 + day/2 + r.Intn(20)
		for i := 0; i < nSignups; i++ {
			user := fmt.Sprintf("u%d", id)
			t := dayStart.Add(time.Duration(r.Intn(24)) * time.Hour).Add(time.Duration(r.Intn(60)) * time.Minute)

			if err := emit("signup", user, t); err != nil {
				return err
			}
			if err := emit("open", user, t.Add(time.Minute)); err != nil {
				return err
			}

			// ~58% activate, of those ~45% check out (the funnel drop-off)
			if r.Float64() < 0.58 {
				if err := emit("activate", user, t.Add(time.Duration(r.Intn(180)+5)*time.Minute)); err != nil {
					return err
				}
				if r.Float64() < 0.45 {
					if err := emit("checkout", user, t.Add(time.Duration(r.Intn(48)+2)*time.Hour)); err != nil {
						return err
					}
				}
			}

			// retention: decaying chance of returning on later days
			for d := 1; d <= 14; d++ {
				if r.Float64() < 0.55/float64(d) {
					ret := dayStart.AddDate(0, 0, d).Add(time.Duration(r.Intn(24)) * time.Hour)
					if err := emit("open", user, ret); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
