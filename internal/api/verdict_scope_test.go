package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The verdict card is the single most prominent thing on the page, and it ignored the page's own
// filter. It was computed a hundred lines above the ?f= chips were applied — the site/env scope
// was handled, the chips were not — so filtering to ?f=plan:pro moved the funnel pane to 90%
// conversion while the card above it still read "Free-plan users convert worst, only 10%
// continue": telling you to go fix the segment you had just filtered out, and contradicting the
// pane directly beneath it.
func TestVerdictFollowsTheActiveFilter(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	ing := func(name, id string, at time.Time, plan string) {
		if err := st.Ingest(event.Event{
			Name: name, DistinctID: id, Timestamp: at,
			Properties: map[string]any{"plan": plan, "path": "/"},
		}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	// free converts terribly (10%), pro converts brilliantly (90%). Unfiltered, the verdict
	// should blame free; filtered to pro, it must not.
	for i := 0; i < 60; i++ {
		id, base := fmt.Sprintf("free%d", i), now.Add(-time.Duration(i)*time.Hour)
		ing("$pageview", id, base, "free")
		ing("signup", id, base.Add(time.Minute), "free")
		if i < 6 {
			ing("checkout", id, base.Add(2*time.Minute), "free")
		}
	}
	for i := 0; i < 40; i++ {
		id, base := fmt.Sprintf("pro%d", i), now.Add(-time.Duration(i)*time.Hour)
		ing("$pageview", id, base, "pro")
		ing("signup", id, base.Add(time.Minute), "pro")
		if i < 36 {
			ing("checkout", id, base.Add(2*time.Minute), "pro")
		}
	}

	get := func(path string) string {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, res.StatusCode)
		}
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	callout := regexp.MustCompile(`(?s)<div class="co-title">(.*?)</div>\s*<div class="co-detail">(.*?)</div>`)
	read := func(html, label string) (string, string) {
		m := callout.FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("%s: no verdict callout rendered", label)
		}
		return m[1], m[2]
	}

	allTitle, allDetail := read(get("/"), "unfiltered")
	proTitle, proDetail := read(get("/?f=plan:pro"), "?f=plan:pro")

	if allTitle == proTitle && allDetail == proDetail {
		t.Errorf("the verdict is identical with and without ?f=plan:pro, while every pane below it "+
			"changed — the headline is describing traffic the reader has filtered out:\n  %s\n  %s",
			allTitle, allDetail)
	}
	// and specifically: once you have filtered to pro, the card must stop blaming free
	if strings.Contains(strings.ToLower(proTitle+proDetail), "free") {
		t.Errorf("filtered to plan:pro, the verdict still blames free-plan users:\n  %s\n  %s", proTitle, proDetail)
	}
}
