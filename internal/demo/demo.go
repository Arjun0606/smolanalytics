// Package demo seeds a realistic dataset so `smolanalytics demo` shows a populated,
// beautiful dashboard with zero setup — the 60-second "oh" moment. Seed data uses
// rand (it's fake data); the analytics engine that computes over it stays
// deterministic.
package demo

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/gsc"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/survey"
)

// reseedEvery re-anchors the 30-day demo dataset to "today" so a long-running demo
// process never goes stale (the biggest pre-signup trust killer: a dashboard that
// shows a week-old snapshot and a fabricated "pageviews dropped 100%"). heartbeatEvery
// keeps a trickle of visitors in the last few minutes so "Live now" is never 0.
const (
	reseedEvery    = 3 * time.Hour
	heartbeatEvery = 90 * time.Second
)

// Live returns a demo Store that stays fresh forever: it re-seeds a whole new 30-day
// dataset every few hours (anchored to the current day) and injects a live visitor
// every ~90s. The re-seed is an atomic pointer swap, so a dashboard request never sees
// a half-built store. Use this for the long-running hosted demo; Seed alone is fine for
// the one-shot CLI `smolanalytics demo`.
func Live() (store.Store, error) {
	first := memory.New()
	if err := Seed(first); err != nil {
		return nil, err
	}
	injectLive(first, time.Now().UTC(), 4) // so "Live now" is populated the instant we boot
	ss := &swapStore{cur: first}

	go func() { // re-anchor the full dataset to today, on a fresh store, then swap it in
		t := time.NewTicker(reseedEvery)
		defer t.Stop()
		for range t.C {
			fresh := memory.New()
			if err := Seed(fresh); err != nil {
				continue // keep serving the last good dataset
			}
			injectLive(fresh, time.Now().UTC(), 4)
			ss.swap(fresh)
		}
	}()

	go func() { // keep "Live now" alive between re-seeds
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for range t.C {
			injectLive(ss, time.Now().UTC(), 1+rand.Intn(3))
		}
	}()

	return ss, nil
}

// liveSeq numbers heartbeat visitors so their event IDs are unique within whatever
// store is current (each fresh re-seed starts empty, and the counter only ever rises).
var liveSeq struct {
	sync.Mutex
	n int
}

// injectLive adds a few "just now" pageviews so the demo's Live-now count and last-24h
// window are always populated. Timestamps sit within the last 3 minutes (< the 5-minute
// live window) so they count as live at request time.
func injectLive(s store.Store, now time.Time, n int) {
	srcs := []string{"google", "twitter", "hacker news", "direct", "reddit", "chatgpt", "claude", "perplexity"}
	for i := 0; i < n; i++ {
		liveSeq.Lock()
		liveSeq.n++
		seq := liveSeq.n
		liveSeq.Unlock()
		src := srcs[rand.Intn(len(srcs))]
		ts := now.Add(-time.Duration(rand.Intn(180)) * time.Second)
		dev := "desktop"
		if rand.Float64() < 0.4 {
			dev = "mobile"
		}
		_ = s.Ingest(event.Event{
			ID: fmt.Sprintf("hb%d", seq), Name: "$pageview", DistinctID: fmt.Sprintf("live%d", seq),
			Timestamp: ts, Properties: map[string]any{"path": "/", "source": src, "device": dev},
		})
	}
}

// swapStore delegates to a backing store.Store that can be replaced atomically, so the
// demo can re-seed in the background with zero window where readers see partial data.
type swapStore struct {
	mu  sync.RWMutex
	cur store.Store
}

func (s *swapStore) get() store.Store                            { s.mu.RLock(); defer s.mu.RUnlock(); return s.cur }
func (s *swapStore) swap(n store.Store)                          { s.mu.Lock(); s.cur = n; s.mu.Unlock() }
func (s *swapStore) Ingest(e ...event.Event) error               { return s.get().Ingest(e...) }
func (s *swapStore) Range(f, t time.Time) ([]event.Event, error) { return s.get().Range(f, t) }
func (s *swapStore) Scan(f, t time.Time, fn func(event.Event) error) error {
	return s.get().Scan(f, t, fn)
}
func (s *swapStore) Names() ([]string, error)          { return s.get().Names() }
func (s *swapStore) Clear() error                      { return s.get().Clear() }
func (s *swapStore) Prune(b time.Time) (int, error)    { return s.get().Prune(b) }
func (s *swapStore) DeleteUser(id string) (int, error) { return s.get().DeleteUser(id) }

// Seed populates the store with ~30 days of a SaaS signup→activate→checkout funnel
// plus daily return ("open") activity for retention and trends.
// weighted picks an index with the given percent weights — tiny helper so demo
// splits look like real-world distributions instead of uniform noise.
func weighted(r *rand.Rand, weights ...int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := r.Intn(total)
	for i, w := range weights {
		if n < w {
			return i
		}
		n -= w
	}
	return 0
}

func Seed(s store.Store) error {
	r := rand.New(rand.NewSource(42))
	now := time.Now().UTC()
	// 8 weeks of history: enough that the 30-day default view has a full prior-30-day
	// window to compare against (so every KPI shows a real delta), and enough cohorts for
	// the retention triangle + lifecycle to read richly.
	const seedDays = 56
	start := now.AddDate(0, 0, -(seedDays - 1)).Truncate(24 * time.Hour)
	sources := []string{"google", "twitter", "hacker news", "direct", "reddit", "chatgpt", "claude", "perplexity"}
	id := 0
	// The real signup population, captured as it is created. seedAgent draws its users from
	// this, so the agent's tool calls belong to people who also signed up. "u<n>" ids come
	// from the shared EVENT counter, not a user counter, so they are sparse — guessing at
	// them (u0, u37, u74...) mostly lands on ids that never existed.
	var signedUp []string
	emit := func(name, user string, t time.Time, props map[string]any) error {
		// A demo must never show future-dated events: the retention loop projects opens up
		// to 14 days past each signup, which overshoots today for recent cohorts. Drop those
		// instead of seeding an event strip that reads into the future.
		if t.After(now) {
			return nil
		}
		id++
		return s.Ingest(event.Event{ID: fmt.Sprintf("e%d", id), Name: name, DistinctID: user, Timestamp: t, Properties: props})
	}

	for day := 0; day < seedDays; day++ {
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
			signedUp = append(signedUp, user)
			source := sources[r.Intn(len(sources))]
			plan := "free"
			if r.Float64() < 0.3 {
				plan = "pro"
			}
			device := "desktop"
			if r.Float64() < 0.35 {
				device = "mobile"
			}
			ref := map[string]string{"google": "https://www.google.com/", "twitter": "https://t.co/", "hacker news": "https://news.ycombinator.com/", "reddit": "https://www.reddit.com/", "direct": "", "chatgpt": "https://chatgpt.com/", "claude": "https://claude.ai/", "perplexity": "https://www.perplexity.ai/"}[source]
			// the richness dimensions real ingest stamps (UA parse + geo lookup),
			// seeded here so the demo shows the full breakdown surface
			browser := [...]string{"Chrome", "Safari", "Firefox", "Edge"}[weighted(r, 55, 25, 12, 8)]
			osName := [...]string{"macOS", "Windows", "iOS", "Android", "Linux"}[weighted(r, 34, 30, 16, 12, 8)]
			country := [...]string{"US", "IN", "DE", "GB", "FR", "BR", "CA", "NL"}[weighted(r, 32, 18, 12, 11, 8, 7, 7, 5)]
			// the same dimensions real ingest stamps ride on the CUSTOM events too, so
			// breakdowns of signup/activate/checkout return real segments, never "(none)"
			props := map[string]any{"source": source, "plan": plan,
				"device": device, "browser": browser, "os": osName, "country": country}
			t := dayStart.Add(time.Duration(r.Intn(24)) * time.Hour).Add(time.Duration(r.Intn(60)) * time.Minute)

			// the web layer: everyone lands before signing up (referrer per source,
			// device split, some tagged campaigns) — feeds the web_overview report
			wprops := map[string]any{"path": "/", "referrer": ref, "device": device, "source": source,
				"browser": browser, "os": osName, "country": country}
			if source == "twitter" && r.Float64() < 0.5 {
				wprops["utm_source"] = "twitter"
				wprops["utm_medium"] = [...]string{"social", "cpc"}[weighted(r, 70, 30)]
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
			// The demo's built-in STORY: mobile onboarding is broken — mobile users sign up
			// fine but activate ~2× worse than desktop. This is what gives the verdict's
			// segment-blame a real, actionable root cause to surface ("signups drop at
			// activate, and it's mobile — 2× worse than desktop"), the diagnosis no rival
			// ships. It's also a realistic failure (mobile onboarding friction).
			if device == "mobile" {
				activateP *= 0.45
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
	seedToolkit(s)
	seedAgent(s, signedUp)
	return nil
}

const (
	demoFlagKey  = "checkout_v2"
	demoSurveyID = "demo-nps"
)

// seedToolkit emits the events behind the product-toolkit features (heatmap clicks, flag
// exposures + conversions, survey shows + responses) so the hosted demo answers "heatmap
// for /pricing", "which variant is winning", and "what's my NPS" with real numbers instead
// of an empty state. Called from Seed() so it survives every re-anchor. The flag/survey
// DEFINITIONS live in their own persistent stores, seeded in serve() (SeedFlagDef /
// SeedSurveyDef / SeedDeploys), keyed to demoFlagKey / demoSurveyID so they line up.
func seedToolkit(s store.Store) {
	now := time.Now().UTC()
	var evs []event.Event
	i := 0
	emit := func(name, user string, at time.Duration, props map[string]any) {
		t := now.Add(at)
		if t.After(now) {
			return
		}
		i++
		evs = append(evs, event.Event{ID: fmt.Sprintf("tk%d", i), Name: name, DistinctID: user, Timestamp: t, Properties: props})
	}

	// click heatmap: positioned clicks (with viewport) clustered on a few elements
	spots := []struct {
		path, text string
		x, y       int
	}{
		{"/pricing", "Start free trial", 640, 300},
		{"/pricing", "Compare plans", 420, 520},
		{"/pricing", "Read the docs", 900, 140},
		{"/", "Get started", 700, 280},
		{"/", "Live demo", 500, 380},
	}
	for d := 0; d < 14; d++ {
		for _, sp := range spots {
			for k := 0; k < 2+(d%3); k++ {
				emit("$click", fmt.Sprintf("clk%d", (d*7+k)%40),
					-time.Duration(d)*24*time.Hour+time.Duration(k)*time.Minute,
					map[string]any{"path": sp.path, "text": sp.text, "x": sp.x + k*3, "y": sp.y + k*2, "sy": 0, "vw": 1280})
			}
		}
	}

	// measured flag checkout_v2: exposures + goal conversions, treatment beats control
	for u := 0; u < 60; u++ {
		user := fmt.Sprintf("exp%d", u)
		variant := "control"
		if u%2 == 1 {
			variant = "treatment"
		}
		at := -time.Duration(18-(u%14)) * 24 * time.Hour
		emit("$feature_flag_called", user, at, map[string]any{"$feature_flag": demoFlagKey, "$feature_flag_response": variant})
		if (variant == "control" && u%5 < 2) || (variant == "treatment" && u%5 < 3) {
			emit("checkout", user, at+2*time.Hour, map[string]any{"amount": 30.0})
		}
	}

	// a signup burst just before the most recent deploy (-6d) so "did my last deploy move
	// signups" reads as a real regression: the 3-day before-window sits high, the after-window
	// is normal, so ComputeImpact sees a clear drop to attribute to that ship.
	for d := 9; d >= 7; d-- {
		for k := 0; k < 30; k++ {
			emit("signup", fmt.Sprintf("dep%d_%d", d, k), -time.Duration(d)*24*time.Hour+time.Duration(k)*time.Minute, map[string]any{"source": "direct"})
		}
	}

	// NPS survey demo-nps: shown + responses skewing promoter
	scores := []int{10, 9, 10, 8, 9, 10, 7, 9, 10, 6, 9, 8, 10, 9, 7, 10, 9, 8, 10, 3, 9, 10, 8, 9, 10, 5, 9, 10}
	for u := 0; u < 36; u++ {
		user := fmt.Sprintf("nps%d", u)
		at := -time.Duration(12-(u%10)) * 24 * time.Hour
		emit("$survey_shown", user, at, map[string]any{"survey_id": demoSurveyID})
		if u < len(scores) {
			emit("$survey_response", user, at+time.Minute, map[string]any{"survey_id": demoSurveyID, "answer": float64(scores[u])})
		}
	}

	_ = s.Ingest(evs...)
}

// seedAgent emits the events behind the computed agent-observability reports (tool health +
// conversation health), so the demo's /v1/agent/* and the homepage "it watches your agent too"
// beat show real, non-empty numbers. Called from Seed() so it survives every re-anchor. Uses the
// two reserved names (agent_tool_call, agent_turn, no $ prefix) so it flows the normal ingest path.
//
// The agent's users are REAL PRODUCT USERS, not a separate population. That is the whole point
// of the product and it used to be invisible here: agent events were seeded under "au0..au39"
// while product events used "u<n>", so the two datasets never touched. A visitor building the
// funnel that only this engine can compute — $pageview -> agent_tool_call -> checkout — got 0 at
// the agent step and reasonably concluded it did not work.
//
// In a product that IS an agent, the person clicking around and the person whose tool calls you
// are measuring are the same person. Seeding them as one population is what makes that true on
// screen as well as in the engine.
// adopters picks the subset of real signed-up users who use the agent. Agent usage
// concentrates — a minority of your users adopt it and they use it a lot — and that shape is
// what makes the joined funnel interesting rather than uniform. Spread across the whole
// population so they span signup cohorts instead of clustering in the first day.
func adopters(signedUp []string, n int) []string {
	if len(signedUp) == 0 {
		return nil
	}
	if n > len(signedUp) {
		n = len(signedUp)
	}
	step := len(signedUp) / n
	if step < 1 {
		step = 1
	}
	out := make([]string, 0, n)
	for i := 0; i < len(signedUp) && len(out) < n; i += step {
		out = append(out, signedUp[i])
	}
	return out
}

// How many distinct product users adopt the agent.
const agentAdopters = 40

func seedAgent(s store.Store, signedUp []string) {
	r := rand.New(rand.NewSource(7))
	now := time.Now().UTC()
	users := adopters(signedUp, agentAdopters)
	if len(users) == 0 {
		return // nothing signed up yet; an agent with no users is not a demo worth seeding
	}
	agentUser := func(r *rand.Rand) string { return users[r.Intn(len(users))] }
	var evs []event.Event
	i := 0
	emit := func(name, user string, at time.Duration, props map[string]any) {
		t := now.Add(at)
		if t.After(now) {
			return
		}
		i++
		evs = append(evs, event.Event{ID: fmt.Sprintf("ag%d", i), Name: name, DistinctID: user, Timestamp: t, Properties: props})
	}
	clients := []string{"cursor", "claude", "copilot", "windsurf"}
	// tool -> base latency ms, error probability, error taxonomy
	type toolSpec struct {
		name    string
		base    int
		errP    float64
		errKind []string
	}
	tools := []toolSpec{
		{"search_web", 420, 0.05, []string{"timeout", "rate_limit"}},
		{"read_file", 35, 0.01, []string{"not_found"}},
		{"run_query", 780, 0.13, []string{"timeout", "bad_args", "db_error"}}, // the slow, error-prone one
		{"send_email", 610, 0.06, []string{"rate_limit", "invalid_recipient"}},
		{"list_dir", 22, 0.0, nil},
	}
	// ~30 days of tool calls
	for d := 0; d < 30; d++ {
		for k := 0; k < 6+r.Intn(8); k++ {
			ts := tools[r.Intn(len(tools))]
			errored := r.Float64() < ts.errP
			lat := float64(ts.base) * (0.5 + r.Float64()*1.4)
			if errored {
				lat += 2000 // errors run slow
			}
			props := map[string]any{
				"tool": ts.name, "server": "app-mcp", "client": clients[r.Intn(len(clients))],
				"latency_ms": lat, "conversation_id": fmt.Sprintf("c%d", r.Intn(70)),
			}
			if errored {
				props["error"] = true
				props["error_type"] = ts.errKind[r.Intn(len(ts.errKind))]
			}
			emit("agent_tool_call", agentUser(r),
				-time.Duration(d)*24*time.Hour+time.Duration(k)*7*time.Minute, props)
		}
	}
	// ~70 conversations of agent_turn events with realistic role sequences
	for c := 0; c < 70; c++ {
		cid := fmt.Sprintf("c%d", c)
		uid := agentUser(r)
		turns := 1 + r.Intn(5)
		base := -time.Duration(r.Intn(30)) * 24 * time.Hour
		var seq []string
		for t := 0; t < turns; t++ {
			seq = append(seq, "user")
			if r.Float64() < 0.15 { // re-ask: a second user turn before the assistant answers
				seq = append(seq, "user")
			}
			if t < turns-1 || r.Float64() > 0.18 { // 18% abandon: last user turn gets no answer
				seq = append(seq, "assistant")
			}
		}
		resolved := seq[len(seq)-1] == "assistant" && r.Float64() < 0.8
		models := []string{"claude-fable-5", "gpt-4o", "claude-opus-4-8"}
		for k, role := range seq {
			p := map[string]any{
				"conversation_id": cid, "role": role, "model": models[r.Intn(len(models))],
			}
			if role == "assistant" {
				p["ttft_ms"] = float64(200 + r.Intn(500))
				p["latency_ms"] = float64(500 + r.Intn(1500))
				p["tokens_out"] = float64(60 + r.Intn(500))
			} else {
				p["latency_ms"] = float64(5 + r.Intn(40))
				p["tokens_in"] = float64(40 + r.Intn(400))
			}
			// resolved only on the final assistant turn of a resolved conversation (never guessed)
			if k == len(seq)-1 && role == "assistant" {
				p["resolved"] = resolved
			}
			emit("agent_turn", uid, base+time.Duration(k)*45*time.Second, p)
		}
	}

	// A handful of agent_label events so the demo shows the BYO-model layer working rather than
	// only its teaching empty state. These are exactly what a visitor's own model would write back
	// through label_conversation, so the panel renders the real shape: values with counts, the
	// labeled/unlabeled split, and the model that inferred them. Deliberately partial (a minority
	// of the 70 conversations) because that is the honest steady state, you label a sample, and the
	// panel is supposed to show how much is still unlabeled.
	intents := []string{"debugging", "data question", "setup help", "feature request"}
	sentiments := []string{"positive", "neutral", "frustrated"}
	for c := 0; c < 22; c++ {
		emit("agent_label", fmt.Sprintf("au%d", c), -time.Duration(c)*3*time.Hour, map[string]any{
			"conversation_id": fmt.Sprintf("c%d", c),
			"intent":          intents[c%len(intents)],
			"sentiment":       sentiments[c%len(sentiments)],
			"labeled_by":      "claude-haiku-4-5 (the visitor's own model, over MCP)",
			"labeled_at":      now.Add(-time.Duration(c) * 3 * time.Hour).Format(time.RFC3339),
		})
	}

	_ = s.Ingest(evs...)
}

// SeedFlagDef seeds the measured flag definition (its exposures come from seedToolkit).
func SeedFlagDef(fs *flag.Store) {
	_, _ = fs.Save(flag.Flag{Key: demoFlagKey, Description: "new checkout flow", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}}})
}

// SeedSurveyDef seeds the NPS survey with a FIXED id so it lines up with the $survey_* events.
func SeedSurveyDef(sv *survey.Store) {
	sv.SeedFixed(survey.Survey{ID: demoSurveyID, Name: "Pricing NPS", Type: "nps",
		Question: "How likely are you to recommend smolanalytics?", Active: true})
}

// SeedDeploys records deploy markers so deploy_impact has ships to attribute a metric change to.
func SeedDeploys(dp *deploys.Store) {
	now := time.Now().UTC()
	_, _ = dp.Record(deploys.Deploy{SHA: "a1b2c3d4e5", Message: "tighten checkout validation", Source: "ci", At: now.AddDate(0, 0, -6)})
	_, _ = dp.Record(deploys.Deploy{SHA: "f6a7b8c9d0", Message: "new pricing page", Source: "ci", At: now.AddDate(0, 0, -20)})
}

// SeedGSC populates the search store so the demo shows the Search card and the
// money_pages report working. The page rows are engineered to hit each bucket:
// two clean quick wins (position 4-15), one CTR problem (position 3 earning a
// fraction of a top-3 CTR), and one cannibalization pair (the same query earning
// clicks on two pages).
func SeedGSC(gs *gsc.Store) error {
	if err := gs.SetGrant("demo", "sc-domain:demo.example"); err != nil {
		return err
	}
	if err := gs.SetRows([]gsc.Row{
		{Query: "self hosted analytics", Clicks: 212, Impressions: 4900, CTRPct: 4.3, Position: 3.1},
		{Query: "plausible alternative", Clicks: 140, Impressions: 3800, CTRPct: 3.7, Position: 4.6},
		{Query: "analytics in one binary", Clicks: 96, Impressions: 1400, CTRPct: 6.9, Position: 2.2},
		{Query: "ask analytics in cursor", Clicks: 74, Impressions: 900, CTRPct: 8.2, Position: 1.8},
		{Query: "posthog too heavy", Clicks: 33, Impressions: 700, CTRPct: 4.7, Position: 5.9},
	}); err != nil {
		return err
	}
	return gs.SetPageRows([]gsc.PageRow{
		// quick wins: ranking 4-15 with real impressions
		{Page: "/blog/self-hosted-analytics-tools", Query: "self hosted analytics tools", Clicks: 88, Impressions: 2600, Position: 6.4},
		{Page: "/compare/plausible", Query: "plausible alternative", Clicks: 41, Impressions: 1900, Position: 8.2},
		// CTR problem: a top-3 position typically earns ~8% — this earns 1.2%
		{Page: "/", Query: "self hosted analytics", Clicks: 60, Impressions: 4900, Position: 2.9},
		// cannibalization: one query, two pages, both getting clicks
		{Page: "/", Query: "analytics in one binary", Clicks: 60, Impressions: 700, Position: 4.8},
		{Page: "/blog/one-binary-analytics", Query: "analytics in one binary", Clicks: 52, Impressions: 640, Position: 5.2},
	})
}
