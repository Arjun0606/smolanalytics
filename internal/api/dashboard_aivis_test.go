package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// geoSeed writes sampled runs back-dated across two ISO weeks, so the weekly trend has
// more than one bucket and the chart's two-week gate is actually exercised.
func geoSeed(t *testing.T, st *memory.Store, engine string, n int) {
	t.Helper()
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < n; i++ {
		evs = append(evs, event.Event{
			Name: "$geo_check", DistinctID: "geo-runner",
			Timestamp: now.Add(-time.Duration(i*36) * time.Hour),
			Properties: map[string]any{
				"engine": engine, "prompt": "best analytics for indie devs",
				"mentioned": i%3 != 0, "recommended": i%4 == 0, "rank": float64(i%3 + 1),
				"competitors": "PostHog, Plausible",
				"verbatim":    "open-source analytics you can just ask questions",
				// deliberately a fake-looking id: the card must print whatever the runner
				// sent, never a prettified guess
				"model_version": "claude-test-1",
			},
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

// The dashboard's half of the three-surface guarantee: the pane embeds aivis.Result, so
// the numbers it prints must be the ones /v1/ai-visibility serializes — not a recount.
func TestAIVisPaneAgreesWithV1(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	geoSeed(t, st, "claude", 12)

	body := renderDash(t, s, "op-pass-1234")
	if !strings.Contains(body, `id="pane-aivis"`) {
		t.Fatal("AI-visibility pane did not render with data present")
	}

	h := s.Handler()
	c := loginSession(t, h, "op-pass-1234")
	r := httptest.NewRequest("GET", "/v1/ai-visibility?days=30", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("/v1/ai-visibility: %d", w.Code)
	}
	var api aivis.Result
	if err := json.Unmarshal(w.Body.Bytes(), &api); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(api.Engines) == 0 {
		t.Fatal("api returned no engines to compare against")
	}
	e := api.Engines[0]
	// the exact cell the pane renders for the top engine — "N of M (P%)"
	cell := strings.NewReplacer("MENT", itoa(e.Mentioned), "RUNS", itoa(e.Runs), "PCT", itoa(e.MentionedPct)).
		Replace(`MENT of RUNS <span class="mut">(PCT%)</span>`)
	if !strings.Contains(body, cell) {
		t.Errorf("pane and /v1/ai-visibility disagree: API says %q, not found in the rendered pane", cell)
	}
	// and the verbatim must carry the model id the runner sent
	if !strings.Contains(body, e.LatestModel) {
		t.Errorf("pane dropped the model id %q — a quote without one is unfalsifiable", e.LatestModel)
	}
}

// The pane most likely to break the no-vapor rule is the one whose data producer lives in
// another repo. With nothing sampled, it must teach the real contract — and name the bot
// filter, which silently eats $-prefixed events from a UA-less runner.
func TestAIVisPaneTeachesWhenNeverSampled(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	// a working instance that simply has not sampled the AI engines yet — the real
	// "never set up" case. (A totally empty store shows the fresh-install takeover
	// instead of the deck, which is a different screen with its own job.)
	if err := st.Ingest(event.Event{
		Name: "$pageview", DistinctID: "v1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")

	if !strings.Contains(body, `id="pane-aivis"`) {
		t.Fatal("pane must render even with no checks — a missing card teaches nothing")
	}
	// self-hosted (no cloud url): the runner is theirs to write, so teach the contract
	for _, want := range []string{"$geo_check", "/v1/events", "model_version", "claude-grounded", "User-Agent", `data-empty="1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("empty state is missing %q — it has to be enough to actually wire a runner", want)
		}
	}
	for _, banned := range []string{"coming soon", "Coming soon", "shipping soon"} {
		if strings.Contains(body, banned) {
			t.Errorf("empty state contains vapor copy %q", banned)
		}
	}
}

// "Nothing yet" and "nothing HERE" have different fixes. $geo_check carries no site or
// path, so any web-property filter hides every check — and if that read as "never set up"
// the user would go rebuild a runner they already have.
func TestAIVisPaneSeparatesNothingYetFromNothingHere(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	geoSeed(t, st, "claude", 6)
	if err := st.Ingest(event.Event{
		Name: "$pageview", DistinctID: "v1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/", "site": "example.com"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	h := s.Handler()
	c := loginSession(t, h, "op-pass-1234")
	r := httptest.NewRequest("GET", "/?f=site:eq:example.com", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("filtered dashboard: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "has recorded <b>$geo_check</b> events, but none in this window") {
		t.Error("a filtered-out report must say the checks exist but are excluded, not that none were ever taken")
	}
	if !strings.Contains(body, "it carries no <b>site</b> or <b>path</b>") {
		t.Error("the empty state must name the site-filter trap that caused it")
	}
}

// One bar drawn as a trend is the most persuasive lie this card could tell, so the chart
// is gated at two weeks and says why it is absent.
func TestAIVisTrendNeedsTwoWeeks(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	// three runs inside a few hours — one ISO week bucket, guaranteed
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 3; i++ {
		evs = append(evs, event.Event{
			Name: "$geo_check", DistinctID: "geo-runner", Timestamp: now.Add(-time.Duration(i) * time.Hour),
			Properties: map[string]any{"engine": "claude", "prompt": "p", "mentioned": true, "recommended": true, "rank": float64(1), "model_version": "m"},
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")
	if !strings.Contains(body, "a single bar is a reading, not a direction") {
		t.Error("a one-week window must explain the missing trend instead of drawing it")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// The rank badge is computed from mention totals, not from the display order — av.Share is
// sorted with our own series FIRST so the chart's primary line is unambiguous, and reading an
// index off that would tell a product sitting dead last that it ranks #1.
func TestAIVisRankIsByMentionsNotDisplayOrder(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 10; i++ {
		// we are named twice; PostHog and Plausible are named in all ten → we are last
		evs = append(evs, event.Event{
			Name: "$geo_check", DistinctID: "geo-runner", Timestamp: now.Add(-time.Duration(i) * time.Hour),
			Properties: map[string]any{
				"engine": "claude", "prompt": "best analytics", "mentioned": i < 2,
				"recommended": false, "rank": float64(0), "competitors": "PostHog, Plausible",
				"model_version": "m",
			},
		})
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")
	if strings.Contains(body, ">#1</div><div class=\"skl\">of 3 brands named") {
		t.Error("a product named least often must not be shown as #1")
	}
	if !strings.Contains(body, ">#3</div>") {
		t.Error("expected rank #3 of 3 — the honest position")
	}
}

// A hosted customer must never be handed a curl for a job the cloud already does for them.
// That reads as "this feature is not finished" — which is exactly how it read the first time.
func TestAIVisManagedInstanceGetsAButtonNotACurl(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	// main.go wires this from SMOLANALYTICS_CLOUD_URL; the hosted provisioner points it at
	// the project's own setup page, which is where the GEO switch lives
	s.SetCloudURL("https://smolanalytics.com/projects/prj_test/setup")
	// Hosted AND funded. Being hosted was the whole test before, so a trial tenant — whose AI
	// allowance is structurally zero — was promised a job the cloud had already decided never to
	// schedule. TestAIVisManagedButUnfundedSaysSo covers the other half.
	s.SetGeoSampling("managed")
	if err := st.Ingest(event.Event{
		Name: "$pageview", DistinctID: "v1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")

	if !strings.Contains(body, "This starts by itself") {
		t.Error("a managed instance must say the feature is automatic, not hand out a setup task")
	}
	if strings.Contains(body, "needs turning on") {
		t.Error("nothing needs turning on — we derive the site from their own traffic")
	}
	if !strings.Contains(body, "https://smolanalytics.com/projects/prj_test/setup") {
		t.Error("the button must point at this project's own setup page")
	}
	// the curl may exist behind a disclosure for the curious, but must not be the answer
	i, j := strings.Index(body, "This starts by itself"), strings.Index(body, "curl -X POST")
	if j >= 0 && j < i {
		t.Error("the curl appears before the button — the hosted reader meets the protocol first")
	}
}

// THE PROMISE THAT COULD NEVER COME TRUE.
//
// Sampling spends a real model call per question, funded from the plan's AI allowance, which
// lib/cogs.ts sets to zero for any plan priced at zero. So for every trial tenant — and anyone
// whose allowance does not cover a sweep — the cloud had already decided it would never schedule
// this, while the card said "This starts by itself. Nothing to configure."
//
// They wait. Nothing ever arrives, on any timescale, and nothing on the page says why. A paying
// customer concludes the product is broken; a trial user concludes the AI story does not work and
// does not buy. It is the most expensive sentence on the page.
func TestAIVisManagedButUnfundedSaysSo(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "op-pass-1234")
	st := memory.New()
	s := New(st)
	s.SetCloudURL("https://smolanalytics.com/projects/prj_test/setup")
	// hosted, but the cloud did not stamp "managed" — this plan does not fund a sweep
	s.SetGeoSampling("off")
	if err := st.Ingest(event.Event{
		Name: "$pageview", DistinctID: "v1", Timestamp: time.Now().UTC(),
		Properties: map[string]any{"path": "/"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	body := renderDash(t, s, "op-pass-1234")

	if strings.Contains(body, "This starts by itself. We read the site") {
		t.Error("promised automatic sampling to a tenant the cloud will never schedule it for")
	}
	if !strings.Contains(body, "not running on your plan") {
		t.Error("the card must say why it is empty — an unexplained empty card reads as a broken product")
	}
	// and it must not hand a hosted customer a curl as the workaround
	if strings.Contains(body, "post one <b>$geo_check</b> event per answer you sample") {
		t.Error("a hosted tenant was given the self-hosting instructions")
	}
	// the way forward has to be reachable
	if !strings.Contains(body, "https://smolanalytics.com/projects/prj_test/setup") {
		t.Error("no link to the plans page — a dead end instead of a next step")
	}
}
