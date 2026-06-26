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
	sources := []string{"google", "twitter", "hacker news", "direct", "reddit"}
	id := 0
	emit := func(name, user string, t time.Time, props map[string]any) error {
		id++
		return s.Ingest(event.Event{ID: fmt.Sprintf("e%d", id), Name: name, DistinctID: user, Timestamp: t, Properties: props})
	}

	for day := 0; day < 30; day++ {
		dayStart := start.AddDate(0, 0, day)
		// gentle upward trend in signups over the month
		nSignups := 18 + day/2 + r.Intn(20)
		for i := 0; i < nSignups; i++ {
			user := fmt.Sprintf("u%d", id)
			source := sources[r.Intn(len(sources))]
			plan := "free"
			if r.Float64() < 0.3 {
				plan = "pro"
			}
			props := map[string]any{"source": source, "plan": plan}
			t := dayStart.Add(time.Duration(r.Intn(24)) * time.Hour).Add(time.Duration(r.Intn(60)) * time.Minute)

			if err := emit("signup", user, t, props); err != nil {
				return err
			}
			if err := emit("open", user, t.Add(time.Minute), props); err != nil {
				return err
			}

			// pro users + google traffic convert a bit better (so breakdowns are interesting)
			activateP, checkoutP := 0.55, 0.42
			if plan == "pro" {
				activateP, checkoutP = 0.78, 0.6
			}
			if source == "hacker news" {
				activateP += 0.08
			}
			if r.Float64() < activateP {
				if err := emit("activate", user, t.Add(time.Duration(r.Intn(180)+5)*time.Minute), props); err != nil {
					return err
				}
				if r.Float64() < checkoutP {
					if err := emit("checkout", user, t.Add(time.Duration(r.Intn(48)+2)*time.Hour), props); err != nil {
						return err
					}
				}
			}

			// retention: decaying chance of returning on later days
			for d := 1; d <= 14; d++ {
				if r.Float64() < 0.55/float64(d) {
					ret := dayStart.AddDate(0, 0, d).Add(time.Duration(r.Intn(24)) * time.Hour)
					if err := emit("open", user, ret, props); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
