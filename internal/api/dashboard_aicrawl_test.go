package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aicrawl"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// crawlSeed writes crawler reports across several days so the dense day trend has more
// than one bucket, and mixes the three purposes because keeping them apart is the whole
// point of the card.
func crawlSeed(t *testing.T, st *memory.Store, n int) {
	t.Helper()
	now := time.Now().UTC()
	kinds := []struct{ name, op, purpose, path string }{
		{"GPTBot", "OpenAI", aicrawl.PurposeTraining, "/pricing"},
		{"OAI-SearchBot", "OpenAI", aicrawl.PurposeSearch, "/docs"},
		{"ChatGPT-User", "OpenAI", aicrawl.PurposeUser, "/"},
		{"ClaudeBot", "Anthropic", aicrawl.PurposeTraining, "/how-it-works"},
	}
	var evs []event.Event
	for i := 0; i < n; i++ {
		k := kinds[i%len(kinds)]
		evs = append(evs, event.Event{
			Name: aicrawl.CrawlEvent, DistinctID: "$crawler",
			Timestamp: now.Add(-time.Duration(i*7) * time.Hour),
			Properties: map[string]any{
				"crawler": k.name, "operator": k.op, "purpose": k.purpose,
				"path": k.path, "status": float64(200), "site": "example.com",
			},
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

// The card has to actually appear, with the crawlers named. A feature the reader cannot
// find on the page is one they will tell you was never built.
func TestAICrawlPaneRendersWithData(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	crawlSeed(t, st, 20)

	body := renderDash(t, s, "op-pass-1234")
	if !strings.Contains(body, `id="pane-aicrawl"`) {
		t.Fatal("AI-crawler pane did not render with data present")
	}
	for _, want := range []string{"GPTBot", "ChatGPT-User", "OpenAI", "Anthropic", "live assistant fetches"} {
		if !strings.Contains(body, want) {
			t.Errorf("the card never printed %q", want)
		}
	}
	// the three purposes are counted apart on purpose; a single "bot traffic" number
	// would bury the one that means a human is asking about you right now
	if !strings.Contains(body, "answering someone now") {
		t.Error("the live-assistant purpose was not distinguished in the table")
	}
}

// The empty state must say the true thing. "No crawler has fetched you" and "nothing on
// your server is reporting" look identical in the data and have opposite fixes.
func TestAICrawlEmptyStateExplainsWhyItIsBlank(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	// ordinary web traffic, no crawler reports at all
	if err := st.Ingest(event.Event{
		Name: "$pageview", DistinctID: "u1", Timestamp: time.Now().UTC().Add(-time.Hour),
		Properties: map[string]any{"path": "/"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	body := renderDash(t, s, "op-pass-1234")
	if !strings.Contains(body, `id="pane-aicrawl"`) {
		t.Fatal("the pane should render even with nothing reporting — it is where you turn it on")
	}
	if !strings.Contains(body, "run no JavaScript") {
		t.Error("the empty state must explain WHY every analytics tool reports zero here")
	}
	// self-hosted (no cloud URL configured) gets the snippet; it must not be told to go
	// click a switch in a control plane it does not have
	if !strings.Contains(body, "$ai_crawl") {
		t.Error("a self-hoster was not given the event contract")
	}
}

// The bot filter drops $-prefixed events from crawler user agents. $ai_crawl is reported
// from INSIDE a crawler's request, so without an explicit exemption the filter silently
// eats every event the feature produces and the card stays empty forever with no error.
func TestAICrawlSurvivesTheBotFilter(t *testing.T) {
	st := memory.New()
	s := New(st)
	h := s.Handler()

	post := func(name, ua string) int {
		r := httptest.NewRequest("POST", "/v1/events", strings.NewReader(
			`{"name":"`+name+`","distinct_id":"$crawler","properties":{"crawler":"GPTBot","operator":"OpenAI","purpose":"training","path":"/","status":200}}`))
		r.Header.Set("Content-Type", "application/json")
		if ua != "" {
			r.Header.Set("User-Agent", ua)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// the two ways this arrives in the real world: carrying the crawler's own UA, and
	// from an edge runtime that sends none at all (which IsBot also treats as a bot)
	for _, ua := range []string{"Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)", ""} {
		if code := post(aicrawl.CrawlEvent, ua); code >= 300 {
			t.Fatalf("ingest rejected the crawler report (ua=%q): %d", ua, code)
		}
	}
	evs, err := st.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	got := 0
	for _, e := range evs {
		if e.Name == aicrawl.CrawlEvent {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("the bot filter ate the crawler reports: stored %d of 2", got)
	}

	// and the exemption must be surgical — an ordinary autocaptured event from a bot UA
	// is still dropped, or the filter that keeps every report honest is now a no-op
	post("$pageview", "Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)")
	evs, _ = st.Range(time.Time{}, time.Time{})
	for _, e := range evs {
		if e.Name == "$pageview" {
			t.Fatal("the exemption widened the bot filter: a crawler pageview was stored")
		}
	}
}
