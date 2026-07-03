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
	sources := []string{"google", "twitter", "hacker news", "direct", "reddit", "chatgpt", "claude", "perplexity"}
	id := 0
	emit := func(name, user string, t time.Time, props map[string]any) error {
		id++
		return s.Ingest(event.Event{ID: fmt.Sprintf("e%d", id), Name: name, DistinctID: user, Timestamp: t, Properties: props})
	}

	for day := 0; day < 30; day++ {
		dayStart := start.AddDate(0, 0, day)
		// window-shoppers: visitors who browse but never sign up (~40% extra), so
		// conversion rates read like reality instead of a rigged demo
		for i := 0; i < 8+r.Intn(10); i++ {
			id++
			v := fmt.Sprintf("v%d", id)
			t := dayStart.Add(time.Duration(r.Intn(24)) * time.Hour)
			src := sources[r.Intn(len(sources))]
			ref := map[string]string{"google": "https://www.google.com/", "twitter": "https://t.co/", "hacker news": "https://news.ycombinator.com/", "reddit": "https://www.reddit.com/", "direct": "", "chatgpt": "https://chatgpt.com/", "claude": "https://claude.ai/", "perplexity": "https://www.perplexity.ai/"}[src]
			dev := "desktop"
			if r.Float64() < 0.4 {
				dev = "mobile"
			}
			if err := emit("$pageview", v, t, map[string]any{"path": "/", "referrer": ref, "device": dev, "source": src}); err != nil {
				return err
			}
			ms := 2000 + r.Intn(25000)
			if err := emit("$engagement", v, t.Add(20*time.Second), map[string]any{"path": "/", "engaged_ms": float64(ms)}); err != nil {
				return err
			}
		}
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

			// the web layer: everyone lands before signing up (referrer per source,
			// device split, some tagged campaigns) — feeds the web_overview report
			device := "desktop"
			if r.Float64() < 0.35 {
				device = "mobile"
			}
			ref := map[string]string{"google": "https://www.google.com/", "twitter": "https://t.co/", "hacker news": "https://news.ycombinator.com/", "reddit": "https://www.reddit.com/", "direct": "", "chatgpt": "https://chatgpt.com/", "claude": "https://claude.ai/", "perplexity": "https://www.perplexity.ai/"}[source]
			wprops := map[string]any{"path": "/", "referrer": ref, "device": device, "source": source}
			if source == "twitter" && r.Float64() < 0.5 {
				wprops["utm_source"] = "twitter"
				wprops["utm_campaign"] = "launch"
			}
			if err := emit("$pageview", user, t.Add(-3*time.Minute), wprops); err != nil {
				return err
			}
			multiPage := r.Float64() < 0.4
			if multiPage {
				p2 := map[string]any{"path": "/pricing", "referrer": "", "device": device, "source": source}
				if err := emit("$pageview", user, t.Add(-time.Minute), p2); err != nil {
					return err
				}
			}
			// engagement: multi-page visitors read for a while; ~1 in 5 of the rest bounce fast
			engagedMs := 15000 + r.Intn(90000)
			if !multiPage && r.Float64() < 0.2 {
				engagedMs = 1500 + r.Intn(6000)
			}
			if err := emit("$engagement", user, t.Add(-30*time.Second), map[string]any{"path": "/", "engaged_ms": float64(engagedMs)}); err != nil {
				return err
			}

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
