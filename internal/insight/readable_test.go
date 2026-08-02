package insight

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// scan builds one $site_readable event the way the cloud's scanner writes it.
func scan(at time.Time, props map[string]any) event.Event {
	p := map[string]any{"url": "https://example.com"}
	for k, v := range props {
		p[k] = v
	}
	return event.Event{Name: ReadableEvent, DistinctID: "geo-runner", Timestamp: at, Properties: p}
}

func TestSiteReadabilityStaysQuietWhenTheSiteReadsFine(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{scan(now.Add(-time.Hour), map[string]any{
		"renders_without_js": true, "words_without_js": float64(840), "robots_blocks_ai": false,
	})}
	if f := siteReadability(evs, now); f != nil {
		t.Fatalf("a readable site should produce no finding, got %+v", f)
	}
}

func TestSiteReadabilityFlagsAnEmptyPreJSPage(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{scan(now.Add(-time.Hour), map[string]any{
		"renders_without_js": false, "words_without_js": float64(11), "robots_blocks_ai": false,
	})}
	f := siteReadability(evs, now)
	if f == nil {
		t.Fatal("a client-rendered site should be flagged")
	}
	if f.Kind != KindReadable || f.Metric != "render" || f.Severity != "warn" {
		t.Errorf("wrong shape: %+v", f)
	}
	if !strings.Contains(f.Detail, "example.com") || !strings.Contains(f.Detail, "11 words") {
		t.Errorf("the finding must name the site and the word count it measured: %q", f.Detail)
	}
}

// robots.txt outranks rendering: a crawler that is turned away never gets far enough to
// care whether the HTML was empty, so leading with the render problem would send someone
// to server-render a page nothing is allowed to fetch.
func TestRobotsBlockBeatsTheRenderFinding(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{scan(now.Add(-time.Hour), map[string]any{
		"renders_without_js": false, "words_without_js": float64(4),
		"robots_blocks_ai": true, "robots_detail": "robots.txt disallows GPTBot, ClaudeBot",
	})}
	f := siteReadability(evs, now)
	if f == nil || f.Metric != "robots" {
		t.Fatalf("robots.txt must win: %+v", f)
	}
	if !strings.Contains(f.Detail, "GPTBot") {
		t.Errorf("the finding must say which crawlers are turned away: %q", f.Detail)
	}
}

// The scanner runs daily. Yesterday's failure must not outrank today's fix, or a deploy
// that solved it keeps being reported as broken.
func TestLatestScanWins(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{
		scan(now.Add(-48*time.Hour), map[string]any{"renders_without_js": false, "words_without_js": float64(3)}),
		scan(now.Add(-2*time.Hour), map[string]any{"renders_without_js": true, "words_without_js": float64(900)}),
	}
	if f := siteReadability(evs, now); f != nil {
		t.Fatalf("the fresher passing scan should win, got %+v", f)
	}
	// and the other way round, so this is not passing by ordering luck
	flip := []event.Event{
		scan(now.Add(-48*time.Hour), map[string]any{"renders_without_js": true, "words_without_js": float64(900)}),
		scan(now.Add(-2*time.Hour), map[string]any{"renders_without_js": false, "words_without_js": float64(3)}),
	}
	if f := siteReadability(flip, now); f == nil {
		t.Fatal("the fresher failing scan should win")
	}
}

// Same contract as the GEO checks: the scanner is our robot writing to the customer's own
// log, so it must not appear anywhere in the product half of the verdict. One synthetic id
// posting daily otherwise reads as a perfectly retained user.
func TestReadabilityScansDoNotPolluteTheRestOfTheVerdict(t *testing.T) {
	now := time.Now().UTC()
	base := funnelFixture()
	withScans := append([]event.Event{}, base...)
	for d := 1; d <= 14; d++ {
		withScans = append(withScans, scan(now.Add(-time.Duration(d)*24*time.Hour),
			map[string]any{"renders_without_js": true, "words_without_js": float64(700)}))
	}

	clean := Generate(base)
	dirty := Generate(withScans)
	if len(dirty) != len(clean) {
		t.Fatalf("readability scans changed the verdict: %d findings vs %d", len(dirty), len(clean))
	}
	for i := range clean {
		if !reflect.DeepEqual(dirty[i], clean[i]) {
			t.Errorf("finding %d changed with the scanner on:\n  clean: %+v\n  dirty: %+v", i, clean[i], dirty[i])
		}
	}
}

// If the page cannot be read, every visibility number is a symptom and this is the cause,
// so it has to be the one a reader hits first.
func TestReadabilityIsRankedAboveTheVisibilityFindings(t *testing.T) {
	now := time.Now().UTC()
	week := 7 * 24 * time.Hour
	evs := append([]event.Event{}, funnelFixture()...)
	evs = append(evs, geoWeek("claude", now, 63, 12, 7, false)...)
	evs = append(evs, geoWeek("claude", now.Add(-week), 63, 42, 7, false)...)
	evs = append(evs, scan(now.Add(-time.Hour), map[string]any{
		"renders_without_js": false, "words_without_js": float64(6),
	}))

	readableAt, visAt := -1, -1
	for i, f := range Generate(evs) {
		if f.Kind == KindReadable && readableAt < 0 {
			readableAt = i
		}
		if strings.Contains(f.Title, "AI answers") && visAt < 0 {
			visAt = i
		}
	}
	if readableAt < 0 || visAt < 0 {
		t.Fatalf("expected both findings, got readable=%d visibility=%d", readableAt, visAt)
	}
	if readableAt > visAt {
		t.Errorf("the cause must be ranked above the symptom: readable at %d, visibility at %d", readableAt, visAt)
	}
}
