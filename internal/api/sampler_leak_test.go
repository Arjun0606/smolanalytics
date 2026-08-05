package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The tool must never appear in its own reports as a user.
//
// Found on the live instance, not in a test: the people pane's most active "person" was
// "$crawler" with 19 events — the synthetic id the edge middleware writes for every AI-crawler
// fetch. $ai_crawl was already in the sampler set and already excluded from retention, lifecycle,
// stickiness, paths and the ask bar; profiles and the account roll-up were simply never wired to
// it. The class of bug matters more than the instance: every NEW report that counts distinct ids
// has to opt into the sampler filter, and forgetting is silent.
func TestThisToolsOwnWritesAreNotPeople(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()
	now := time.Now().UTC()

	batch := []map[string]any{
		{"name": "$pageview", "distinct_id": "real1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"company": "acme"}},
		{"name": "$pageview", "distinct_id": "real2", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"company": "acme"}},
	}
	// The tool writing about itself, under its three synthetic ids.
	for i := 0; i < 20; i++ {
		batch = append(batch,
			map[string]any{"name": "$ai_crawl", "distinct_id": "$crawler",
				"timestamp": now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
				"properties": map[string]any{"crawler": "GPTBot", "path": "/"}},
			map[string]any{"name": "$geo_check", "distinct_id": "$sampler",
				"timestamp": now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)},
		)
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var people struct {
		People int `json:"people"`
		Recent []struct {
			DistinctID string `json:"distinct_id"`
			Events     int    `json:"events"`
		} `json:"recent"`
	}
	mustJSON(t, getBody(t, srv, "/v1/people?limit=50"), &people)

	for _, p := range people.Recent {
		if strings.HasPrefix(p.DistinctID, "$") {
			t.Fatalf("%q is this tool writing about itself and it has a profile with %d events",
				p.DistinctID, p.Events)
		}
	}
	if people.People != 2 {
		t.Fatalf("want the 2 real people, got %d — the crawler is being counted", people.People)
	}

	// The account roll-up counts events per account and must not be inflated by the same writes.
	var grp struct {
		TotalGroups int `json:"total_groups"`
		Groups      []struct {
			Value  string `json:"value"`
			Events int    `json:"events"`
			Users  int    `json:"users"`
		} `json:"groups"`
	}
	mustJSON(t, getBody(t, srv, "/v1/groups?property=company"), &grp)
	if grp.TotalGroups != 1 || len(grp.Groups) != 1 {
		t.Fatalf("want exactly one account, got %d", grp.TotalGroups)
	}
	if grp.Groups[0].Events != 2 || grp.Groups[0].Users != 2 {
		t.Fatalf("acme is 2 people / 2 events; got %d users / %d events",
			grp.Groups[0].Users, grp.Groups[0].Events)
	}
}

// Coverage must be computed over REAL traffic. Counting the tool's own writes as
// "could not be attributed to an account" turns a perfectly instrumented instance into one that
// gets told most of its traffic is unattributed — an accusation we would be manufacturing.
func TestGroupCoverageIgnoresThisToolsOwnWrites(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()
	now := time.Now().UTC()

	batch := []map[string]any{
		{"name": "signup", "distinct_id": "real1", "timestamp": now.Format(time.RFC3339),
			"properties": map[string]any{"company": "acme"}},
		{"name": "checkout", "distinct_id": "real1", "timestamp": now.Add(time.Minute).Format(time.RFC3339)},
	}
	for i := 0; i < 50; i++ { // the tool drowning out the signal
		batch = append(batch, map[string]any{"name": "$ai_crawl", "distinct_id": "$crawler",
			"timestamp": now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)})
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var f struct {
		Grain struct {
			Kept         int    `json:"events_kept"`
			Dropped      int    `json:"events_dropped"`
			Unattributed int    `json:"users_unattributed"`
			Note         string `json:"note"`
		} `json:"group_grain"`
	}
	mustJSON(t, getBody(t, srv, "/v1/funnel?steps=signup,checkout&group_by=company"), &f)

	if f.Grain.Dropped != 0 {
		t.Fatalf("every REAL event carries the account, so nothing should be dropped; got %d", f.Grain.Dropped)
	}
	if f.Grain.Unattributed != 0 {
		t.Fatalf("the crawler is not an unattributed user; got %d", f.Grain.Unattributed)
	}
	if f.Grain.Note != "" {
		t.Fatalf("perfect coverage must not carry a coverage warning; got %q", f.Grain.Note)
	}
}
