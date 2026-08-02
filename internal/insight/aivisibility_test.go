package insight

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/aivis"
	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The rule is mostly a suppression problem: sampled LLM answers resample loudly, and a
// verdict that reads "Claude stopped recommending you" when our own sampler skipped a
// day is the most expensive sentence this module could print. Most of what follows
// asserts SILENCE.

// geoWeek lays `runs` checks for one engine inside the window ending `end`, `part` of
// them positive, spread evenly over `days` distinct days. Offsets are half-day-aligned
// so no event lands on a window boundary (aivis's windows are inclusive at both ends).
func geoWeek(engine string, end time.Time, runs, part, days int, mentionOnly bool) []event.Event {
	var out []event.Event
	for i := 0; i < runs; i++ {
		day := i % days
		ts := end.Add(-time.Duration(day)*24*time.Hour - 12*time.Hour - time.Duration(i)*time.Minute)
		positive := i < part
		mentioned, recommended := positive, positive
		if mentionOnly {
			mentioned = positive // recommended held flat at true, so only mention moves
			recommended = true
		}
		out = append(out, event.Event{
			Name: aivis.CheckEvent, DistinctID: "geo-runner", Timestamp: ts,
			Properties: map[string]any{
				"engine": engine, "prompt": fmt.Sprintf("best analytics for indie devs %d", i%3),
				"mentioned": mentioned, "recommended": recommended, "rank": float64(0),
				"competitors": "PostHog", "verbatim": "an analytics tool", "model_version": "m-1",
			},
		})
	}
	return out
}

// aiPageviews puts `n` distinct visitors on the site inside the window ending `end`,
// `fromAI` of them arriving from an AI assistant.
func aiPageviews(prefix string, end time.Time, n, fromAI int) []event.Event {
	var out []event.Event
	for i := 0; i < n; i++ {
		ref := ""
		if i < fromAI {
			ref = "https://chatgpt.com/"
		}
		out = append(out, event.Event{
			Name: "$pageview", DistinctID: fmt.Sprintf("%s%d", prefix, i),
			Timestamp:  end.Add(-36*time.Hour - time.Duration(i)*time.Minute),
			Properties: map[string]any{"path": "/", "referrer": ref},
		})
	}
	return out
}

func aiFinding(fs []Finding) (Finding, bool) {
	for _, f := range fs {
		if strings.Contains(f.Title, "AI answers") {
			return f, true
		}
	}
	return Finding{}, false
}

func TestAIVisibilityDropWithReferralDropIsTheLeadWarning(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)            // 19% this week
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...) // 67% last week
	evs = append(evs, aiPageviews("cur", now, 60, 12)...)
	evs = append(evs, aiPageviews("old", now.Add(-week), 60, 20)...)

	f, ok := aiFinding(Generate(evs))
	if !ok {
		t.Fatalf("expected the AI-visibility finding; got %+v", Generate(evs))
	}
	if f.Severity != "warn" {
		t.Errorf("a visibility drop must be a warning, got %q", f.Severity)
	}
	if !strings.Contains(f.Title, "Claude recommends you in 19% of AI answers, down from 67%") {
		t.Errorf("title: %q", f.Title)
	}
	// the counts are the honesty: the reader sees the sample the rate came off
	if !strings.Contains(f.Detail, "12 of 63 sampled answers this week, down from 42 of 63") {
		t.Errorf("detail must show both run counts: %q", f.Detail)
	}
	// and the half nobody else holds
	if !strings.Contains(f.Detail, "fell 40%") || !strings.Contains(f.Detail, "to 12 from 20") {
		t.Errorf("detail must carry the AI-referral move: %q", f.Detail)
	}
}

func TestAIVisibilityRiseIsInfoNotAWarning(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 63, 42, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 12, 7, false)...)
	evs = append(evs, aiPageviews("cur", now, 60, 30)...)
	evs = append(evs, aiPageviews("old", now.Add(-week), 60, 20)...)

	f, ok := aiFinding(Generate(evs))
	if !ok {
		t.Fatal("expected the AI-visibility finding")
	}
	if f.Severity != "info" {
		t.Errorf("good news is not a warning, got %q", f.Severity)
	}
	if !strings.Contains(f.Title, "up from 19%") || !strings.Contains(f.Detail, "rose 50%") {
		t.Errorf("title=%q detail=%q", f.Title, f.Detail)
	}
}

// Everything below asserts the rule stays quiet.

func TestThinSampleIsHeldBack(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// a total collapse — on 12 runs a side, which is under the run floor
	evs = append(evs, geoWeek("claude", now, 12, 0, 6, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 12, 12, 6, false)...)
	evs = append(evs, aiPageviews("cur", now, 60, 5)...)
	evs = append(evs, aiPageviews("old", now.Add(-week), 60, 25)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("12 runs a side must not produce a verdict: %+v", f)
	}
}

func TestSingleDayOfSamplingIsHeldBack(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// plenty of runs, all on one day each week: this is Tuesday vs Tuesday, not week vs week
	evs = append(evs, geoWeek("claude", now, 60, 6, 1, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 60, 54, 1, false)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("one sampling day a side is not a week: %+v", f)
	}
}

func TestSamplerOutageDoesNotReadAsAnEngineVerdict(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// last week sampled properly; this week the cron died after one day
	evs = append(evs, geoWeek("claude", now, 9, 0, 1, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("our own downtime must never publish as the engine's opinion: %+v", f)
	}
}

func TestPromptSetChangeIsNotAVisibilityShift(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// the set tripled: 21 -> 63 runs. The rate moved, but not over the same questions.
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 21, 14, 7, false)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("a changed prompt set is our edit, not their opinion: %+v", f)
	}
}

func TestSmallShiftBelowTheActionFloorIsHeldBack(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// 63% -> 52%, an 11-point move on a real sample: statistically arguable, not an action
	evs = append(evs, geoWeek("claude", now, 63, 33, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 40, 7, false)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("an 11-point move must not ship: %+v", f)
	}
}

func TestShiftInsideTheClusteredNoiseBandIsHeldBack(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// 67% -> 52% is 15 points, past the action floor — but on 21 runs a side, with 3 runs
	// per prompt, the band is ~46 points wide. This is the case a naive z-test would ship.
	evs = append(evs, geoWeek("claude", now, 21, 11, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 21, 14, 7, false)...)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("a move inside the resampling band must not ship: %+v", f)
	}
}

// ...but a big enough move on the same thin sample DOES clear it — the band scales,
// it is not a run-count cliff.
func TestHugeShiftClearsTheBandEvenOnAThinSample(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 21, 4, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 21, 14, 7, false)...)
	f, ok := aiFinding(Generate(evs))
	if !ok {
		t.Fatal("a 48-point collapse on 21 runs a side must still ship")
	}
	if !strings.Contains(f.Detail, "4 of 21 sampled answers this week, down from 14 of 21") {
		t.Errorf("detail: %q", f.Detail)
	}
	// the counts are the caveat, in the reader's words — the house "(n=..)" annotation
	// would be the same warning said twice, the second time in ours
	if strings.Contains(f.Detail, "small sample") {
		t.Errorf("redundant qualifier alongside the run counts: %q", f.Detail)
	}
}

func TestFirstWeekOfSamplingIsNotAShift(t *testing.T) {
	now := time.Now().UTC()
	evs := geoWeek("claude", now, 63, 12, 7, false)
	if f, ok := aiFinding(Generate(evs)); ok {
		t.Fatalf("no prior week means no comparison: %+v", f)
	}
}

// The two "we measured nothing" cases must not be collapsed into one sentence.
func TestNoAIReferralsIsNotTheSameAsNoTraffic(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	geo := append(geoWeek("claude", now, 63, 12, 7, false), geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)

	// traffic exists, none of it from an AI assistant
	withTraffic := append(append([]event.Event{}, geo...), aiPageviews("cur", now, 40, 0)...)
	withTraffic = append(withTraffic, aiPageviews("old", now.Add(-week), 40, 0)...)
	f, ok := aiFinding(Generate(withTraffic))
	if !ok {
		t.Fatal("expected the finding")
	}
	if !strings.Contains(f.Detail, "leading indicator only") {
		t.Errorf("no AI referrals: %q", f.Detail)
	}
	if strings.Contains(f.Detail, "%,") || strings.Contains(f.Detail, "fell") {
		t.Errorf("must not invent a referral trend from zero: %q", f.Detail)
	}

	// no pageviews at all: checks arrived before the SDK did
	f2, ok := aiFinding(Generate(geo))
	if !ok {
		t.Fatal("a GEO-only instance still deserves its verdict")
	}
	if !strings.Contains(f2.Detail, "no traffic side") {
		t.Errorf("no pageviews at all: %q", f2.Detail)
	}
}

func TestThinReferralBaseGetsCountsNotAPercentage(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)
	evs = append(evs, aiPageviews("cur", now, 40, 3)...)
	evs = append(evs, aiPageviews("old", now.Add(-week), 40, 5)...)
	f, _ := aiFinding(Generate(evs))
	if !strings.Contains(f.Detail, "3 this week against 5 last week") || !strings.Contains(f.Detail, "too few") {
		t.Errorf("5 AI visitors is not a denominator for a percentage: %q", f.Detail)
	}
	if strings.Contains(f.Detail, "fell 40%") {
		t.Errorf("40%% off a base of 5 is exactly the noise this package refuses to print: %q", f.Detail)
	}
}

func TestMentionCollapseWinsWhenItIsTheBiggerMove(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	// recommended pinned at 100% both weeks; mentioned falls 90% -> 20%
	evs = append(evs, geoWeek("claude", now, 63, 13, 7, true)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 57, 7, true)...)
	f, ok := aiFinding(Generate(evs))
	if !ok {
		t.Fatal("expected the finding")
	}
	if !strings.Contains(f.Title, "Claude mentions you") {
		t.Errorf("the bigger move must lead: %q", f.Title)
	}
}

func TestGroundedEngineReadsAsWebSearch(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude-grounded", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude-grounded", now.Add(-week), 63, 42, 7, false)...)
	f, ok := aiFinding(Generate(evs))
	if !ok {
		t.Fatal("expected the finding")
	}
	if !strings.Contains(f.Title, "Claude with web search") {
		t.Errorf("title: %q", f.Title)
	}
	if strings.Contains(f.Title+f.Detail, "-grounded") {
		t.Errorf("the storage convention leaked into prose: %q", f.Title)
	}
}

func TestOnlyTheBiggestMoveShips(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...) // -48pp
	evs = append(evs, geoWeek("chatgpt", now, 63, 25, 7, false)...)
	evs = append(evs, geoWeek("chatgpt", now.Add(-week), 63, 42, 7, false)...) // -27pp

	n := 0
	var got Finding
	for _, f := range Generate(evs) {
		if strings.Contains(f.Title, "AI answers") {
			n++
			got = f
		}
	}
	if n != 1 {
		t.Fatalf("the digest gets one visibility finding, not %d", n)
	}
	if !strings.Contains(got.Title, "Claude") {
		t.Errorf("the sharpest move must win: %q", got.Title)
	}
}

// The regression that would poison the whole digest: our sampler's own writes counted
// as product activity. One robot distinct_id firing daily is a perfectly retained user.
func TestGeoChecksDoNotPolluteTheRestOfTheVerdict(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	base := funnelFixture()
	withGeo := append(append([]event.Event{}, base...), geoWeek("claude", now, 63, 12, 7, false)...)
	withGeo = append(withGeo, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)

	clean := Generate(base)
	dirty := Generate(withGeo)
	var dirtyRest []Finding
	for _, f := range dirty {
		if !strings.Contains(f.Title, "AI answers") {
			dirtyRest = append(dirtyRest, f)
		}
	}
	if len(dirtyRest) != len(clean) {
		t.Fatalf("geo checks changed the product verdict: %d findings vs %d", len(dirtyRest), len(clean))
	}
	for i := range clean {
		// Finding carries a []string now, so compare deeply rather than with != (which no
		// longer compiles — and would have compared only the scalar half if it did).
		if !reflect.DeepEqual(dirtyRest[i], clean[i]) {
			t.Errorf("finding %d changed with GEO on:\n  clean: %+v\n  dirty: %+v", i, clean[i], dirtyRest[i])
		}
	}
	for _, f := range dirty {
		if strings.Contains(f.Title+f.Detail, "$geo_check") || strings.Contains(f.Title+f.Detail, "geo-runner") {
			t.Errorf("the sampler's internals leaked into prose: %+v", f)
		}
	}
}

// The house vocabulary guard, extended to the engine names this rule introduces.
func TestVisibilityFindingLeaksNoInternals(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	var evs []event.Event
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)
	evs = append(evs, aiPageviews("cur", now, 60, 12)...)
	evs = append(evs, aiPageviews("old", now.Add(-week), 60, 20)...)
	for _, f := range Generate(evs) {
		if strings.Contains(f.Title, "$") || strings.Contains(f.Detail, "$") {
			t.Errorf("raw event name in prose: %q / %q", f.Title, f.Detail)
		}
		if m := filterSyntax.FindString(f.Title + f.Detail); m != "" {
			t.Errorf("finding reads like a filter (%s): %q / %q", m, f.Title, f.Detail)
		}
	}
}

func TestHumanEngineNamesTheUnknownWithoutInventing(t *testing.T) {
	cases := map[string]string{
		"claude": "Claude", "chatgpt": "ChatGPT", "perplexity": "Perplexity",
		"claude-grounded": "Claude with web search",
		"mistral":         "Mistral", // never seen before, still reads as a name
	}
	for raw, want := range cases {
		if got := HumanEngine(raw); got != want {
			t.Errorf("HumanEngine(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The retrieval-vs-reputation split. Being read and passed over has a completely different
// fix from never being read, and telling someone to "write more content" for the first one
// wastes months.
func TestCitedButPassedOverIsItsOwnDiagnosis(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 12; i++ {
		e := event.Event{
			Name: aivis.CheckEvent, DistinctID: "geo-runner", Timestamp: now.Add(-time.Duration(i) * time.Hour),
			Properties: map[string]any{
				"engine": "claude-grounded", "prompt": "best analytics", "mentioned": true,
				"recommended": i == 0, "rank": float64(0), "competitors": "PostHog",
				"cited_domain": true, "model_version": "m",
			},
		}
		evs = append(evs, e)
	}
	var found *Finding
	for _, f := range Generate(evs) {
		if strings.Contains(f.Title, "picks someone else") {
			g := f
			found = &g
		}
	}
	if found == nil {
		t.Fatalf("expected the cited-not-recommended finding; got %+v", Generate(evs))
	}
	if !strings.Contains(found.Detail, "not a content problem") {
		t.Errorf("the diagnosis must say what it is NOT, or the reader writes more pages: %q", found.Detail)
	}
	if !strings.Contains(found.Title, "11 times out of 12") {
		t.Errorf("counts, not a bare rate: %q", found.Title)
	}
}

// It must stay silent when the site is simply not being retrieved — that IS a content and
// renderability problem, and this finding would send the reader in the wrong direction.
func TestNotCitedAtAllDoesNotFireTheOffSiteDiagnosis(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	for i := 0; i < 12; i++ {
		evs = append(evs, event.Event{
			Name: aivis.CheckEvent, DistinctID: "geo-runner", Timestamp: now.Add(-time.Duration(i) * time.Hour),
			Properties: map[string]any{
				"engine": "claude-grounded", "prompt": "best analytics", "mentioned": false,
				"recommended": false, "rank": float64(0), "cited_domain": false, "model_version": "m",
			},
		})
	}
	for _, f := range Generate(evs) {
		if strings.Contains(f.Title, "picks someone else") {
			t.Fatalf("a site that was never retrieved must not be told its problem is off-site: %+v", f)
		}
	}
}
