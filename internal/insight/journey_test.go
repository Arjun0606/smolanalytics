package insight

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The auto-funnel must follow the real user journey (land → register → upgrade),
// not raw volume order (which would put the noisy "click" event first).
func TestDetectJourneyOrdersByFirstTouch(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var evs []event.Event
	for i := 0; i < 30; i++ {
		u := fmt.Sprintf("u%d", i)
		t0 := base.Add(time.Duration(i) * time.Hour)
		evs = append(evs, event.Event{ID: u + "l", DistinctID: u, Name: "land", Timestamp: t0})
		// lots of clicks AFTER landing — highest volume, but not the journey start
		for c := 0; c < 5; c++ {
			evs = append(evs, event.Event{ID: fmt.Sprintf("%sc%d", u, c), DistinctID: u, Name: "click", Timestamp: t0.Add(10 * time.Minute)})
		}
		if i < 20 {
			evs = append(evs, event.Event{ID: u + "r", DistinctID: u, Name: "register", Timestamp: t0.Add(time.Hour)})
		}
		if i < 8 {
			evs = append(evs, event.Event{ID: u + "u", DistinctID: u, Name: "upgrade", Timestamp: t0.Add(24 * time.Hour)})
		}
	}
	steps := detectJourney(evs)
	if len(steps) < 3 {
		t.Fatalf("want ≥3 journey steps, got %v", steps)
	}
	if steps[0].Event != "land" {
		t.Fatalf("journey must start at land (first touch), got %q — volume order leaked in", steps[0].Event)
	}
	idx := map[string]int{}
	for i, s := range steps {
		idx[s.Event] = i
	}
	if idx["register"] > 0 && idx["upgrade"] > 0 && idx["register"] > idx["upgrade"] {
		t.Fatalf("register must come before upgrade in the journey: %v", steps)
	}
}

// When one segment converts far worse through the leaky step, the verdict must
// name it — and stay silent when segments are uniform (no noise).
func TestSegmentBlame(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var evs []event.Event
	mk := func(i int, src string, converts bool) {
		u := fmt.Sprintf("u_%s_%d", src, i)
		evs = append(evs, event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: base,
			Properties: map[string]any{"source": src}})
		if converts {
			evs = append(evs, event.Event{ID: u + "a", DistinctID: u, Name: "activate", Timestamp: base.Add(time.Hour),
				Properties: map[string]any{"source": src}})
		}
	}
	// google: 20 users, 80% convert. tiktok: 20 users, 10% convert.
	for i := 0; i < 20; i++ {
		mk(i, "google", i < 16)
	}
	for i := 0; i < 20; i++ {
		mk(i, "tiktok", i < 2)
	}
	f := segmentBlame(evs, "signup", "activate")
	if f == nil {
		t.Fatal("expected a blame finding for the tiktok segment")
	}
	if !strings.Contains(f.Title, "tiktok") || !strings.Contains(f.Title, "source") {
		t.Fatalf("blame should name source=tiktok: %q", f.Title)
	}

	// uniform segments → no finding
	evs = evs[:0]
	for i := 0; i < 20; i++ {
		mk(i, "google", i < 10)
	}
	for i := 0; i < 20; i++ {
		mk(i, "tiktok", i < 10)
	}
	if f := segmentBlame(evs, "signup", "activate"); f != nil {
		t.Fatalf("uniform segments must not produce blame: %+v", f)
	}
}

// TestSegmentBlameFirstTouch is the realistic case: the acquisition attribute (device)
// lives ONLY on the landing $pageview, not on the signup/activate steps. Without
// first-touch stamping, segmentBlame can't see device on the step event and stays silent;
// with it, it names the underperforming segment. This is the fix that makes the verdict
// sharp ("it's mobile") on real data instead of vague.
func TestSegmentBlameFirstTouch(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var evs []event.Event
	mk := func(i int, device string, converts bool) {
		u := fmt.Sprintf("u_%s_%d", device, i)
		// device is on the landing pageview ONLY — the realistic autocapture shape.
		evs = append(evs, event.Event{ID: u + "p", DistinctID: u, Name: "$pageview", Timestamp: base,
			Properties: map[string]any{"device": device, "path": "/"}})
		evs = append(evs, event.Event{ID: u + "s", DistinctID: u, Name: "signup", Timestamp: base.Add(time.Minute),
			Properties: map[string]any{}}) // signup carries NO device
		if converts {
			evs = append(evs, event.Event{ID: u + "a", DistinctID: u, Name: "activate", Timestamp: base.Add(time.Hour),
				Properties: map[string]any{}})
		}
	}
	for i := 0; i < 25; i++ {
		mk(i, "desktop", i < 20) // desktop: 80% activate
	}
	for i := 0; i < 25; i++ {
		mk(i, "mobile", i < 3) // mobile: 12% activate — the segment to blame
	}
	f := segmentBlame(evs, "signup", "activate")
	if f == nil {
		t.Fatal("expected blame on device=mobile, got nil (first-touch stamp not applied?)")
	}
	if !strings.Contains(f.Title, "mobile") || !strings.Contains(f.Title, "device") {
		t.Fatalf("blame should name device=mobile: %q", f.Title)
	}
	if !strings.Contains(f.Title, "×") { // the multiplier makes it land
		t.Fatalf("blame should quantify how much worse: %q", f.Title)
	}
}

// End to end: a product with non-conventional event names gets a journey-ordered
// funnel finding, and a lopsided segment shows up as a blame finding.
func TestGenerateIncludesJourneyAndBlame(t *testing.T) {
	base := time.Now().UTC().Add(-3 * 24 * time.Hour)
	var evs []event.Event
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("u%d", i)
		src := "google"
		if i%2 == 0 {
			src = "tiktok"
		}
		props := map[string]any{"source": src}
		evs = append(evs, event.Event{ID: u + "v", DistinctID: u, Name: "visit", Timestamp: base, Properties: props})
		// google converts 90%, tiktok 10%
		if (src == "google" && i%10 != 1) || (src == "tiktok" && i%10 == 0) {
			evs = append(evs, event.Event{ID: u + "j", DistinctID: u, Name: "join", Timestamp: base.Add(time.Hour), Properties: props})
		}
	}
	fs := Generate(evs)
	var hasDrop, hasBlame bool
	for _, f := range fs {
		if strings.Contains(f.Title, "drop-off") && strings.Contains(f.Title, "visit") {
			hasDrop = true
		}
		if strings.Contains(f.Title, "tiktok") {
			hasBlame = true
		}
	}
	if !hasDrop {
		t.Fatalf("expected a journey-ordered drop-off finding, got %+v", fs)
	}
	if !hasBlame {
		t.Fatalf("expected a segment-blame finding naming tiktok, got %+v", fs)
	}
}
