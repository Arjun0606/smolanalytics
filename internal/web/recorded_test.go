package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// The geo card printed "US 20 · 52%" for a country that was 20% of recorded visitors, because
// its denominator was the sum of the ten rows that survived rank()'s truncation (38 of 100).
// The visible rows then also added to 100%, asserting there was no tail. Result now carries
// the pre-truncation total per dimension so no caller has to re-derive one from a cut list.
func TestRecordedTotalsSurviveTruncation(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pinWall(t, now)
	var evs []event.Event
	for i := 0; i < 20; i++ { // 20 US visitors
		evs = append(evs, pv(fmt.Sprintf("us%d", i), "/", "", map[string]any{"country": "US"}, now.Add(-time.Hour)))
	}
	for c := 0; c < 40; c++ { // 40 more countries, 2 visitors each — the long tail rank() drops
		for i := 0; i < 2; i++ {
			cc := fmt.Sprintf("C%02d", c)
			evs = append(evs, pv(fmt.Sprintf("%s-%d", cc, i), "/", "", map[string]any{"country": cc}, now.Add(-time.Hour)))
		}
	}
	r := Compute(evs, 30, now)

	if r.Visitors != 100 {
		t.Fatalf("visitors=%d, want 100", r.Visitors)
	}
	if len(r.Countries) != 10 {
		t.Fatalf("countries rows=%d, want the top 10 (the tail is meant to be truncated)", len(r.Countries))
	}
	visible := 0
	for _, row := range r.Countries {
		visible += row.Count
	}
	if visible == 100 {
		t.Fatalf("fixture is wrong: the visible rows must NOT cover everyone, got %d", visible)
	}
	if r.Recorded.Countries != 100 {
		t.Fatalf("Recorded.Countries=%d, want 100 (every geolocated visitor, not the %d visible)", r.Recorded.Countries, visible)
	}
	// the number the card actually prints, computed the honest way
	pct := r.Countries[0].Count * 100 / r.Recorded.Countries
	if r.Countries[0].Value != "US" || pct != 20 {
		t.Fatalf("US share = %d%% (row %+v), want 20%%", pct, r.Countries[0])
	}
}

// Same defect, same shape, on the AI-referrer card: rank caps it at 6 of 10 possible hosts.
func TestRecordedTotalsForAIReferrers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pinWall(t, now)
	hosts := []string{"chatgpt.com", "claude.ai", "perplexity.ai", "gemini.google.com", "copilot.microsoft.com", "you.com", "poe.com", "phind.com", "kagi.com"}
	var evs []event.Event
	for i, h := range hosts {
		evs = append(evs, pv(fmt.Sprintf("ai%d", i), "/", "https://"+h+"/", nil, now.Add(-time.Hour)))
	}
	r := Compute(evs, 30, now)
	if len(r.AIReferrers) != 6 {
		t.Fatalf("ai referrer rows=%d, want 6 (truncated)", len(r.AIReferrers))
	}
	if r.Recorded.AIReferrers != len(hosts) {
		t.Fatalf("Recorded.AIReferrers=%d, want %d", r.Recorded.AIReferrers, len(hosts))
	}
}

// Dimensions that fit on screen must report the same total they always did, or the new field
// is just a second, differently-wrong denominator.
func TestRecordedTotalsMatchRowsWhenNothingIsTruncated(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pinWall(t, now)
	evs := []event.Event{
		pv("u1", "/", "https://google.com/", map[string]any{"device": "desktop", "browser": "Chrome", "os": "macOS", "utm_source": "hn", "utm_medium": "social", "utm_campaign": "launch", "title": "Home"}, now.Add(-time.Hour)),
		pv("u1", "/pricing", "", map[string]any{"title": "Pricing"}, now.Add(-50*time.Minute)),
		pv("u2", "/docs", "", map[string]any{"device": "mobile", "browser": "Safari", "os": "iOS"}, now.Add(-30*time.Minute)),
	}
	r := Compute(evs, 30, now)
	for _, c := range []struct {
		name string
		rows []Row
		got  int
	}{
		{"top_pages", r.TopPages, r.Recorded.TopPages},
		{"referrers", r.Referrers, r.Recorded.Referrers},
		{"utm_sources", r.UTMSources, r.Recorded.UTMSources},
		{"device_split", r.DeviceSplit, r.Recorded.DeviceSplit},
		{"browsers", r.Browsers, r.Recorded.Browsers},
		{"oses", r.OSes, r.Recorded.OSes},
		{"utm_mediums", r.UTMMediums, r.Recorded.UTMMediums},
		{"utm_campaigns", r.UTMCampaigns, r.Recorded.UTMCampaigns},
		{"entry_pages", r.EntryPages, r.Recorded.EntryPages},
		{"exit_pages", r.ExitPages, r.Recorded.ExitPages},
		{"top_titles", r.TopTitles, r.Recorded.TopTitles},
	} {
		sum := 0
		for _, row := range c.rows {
			sum += row.Count
		}
		if sum != c.got {
			t.Errorf("%s: rows sum to %d but Recorded says %d — untruncated lists must agree", c.name, sum, c.got)
		}
	}
}
