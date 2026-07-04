package gsc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A fake Google: token endpoint + sites list + search analytics query (both the
// query-dimension and page-dimension cuts). Flip failPages to make the
// page-level cut return 500s while queries keep working.
func fakeGoogle(t *testing.T) (srv *httptest.Server, failPages *atomic.Bool) {
	t.Helper()
	failPages = new(atomic.Bool)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "good-code" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"refresh_token": "rt-1", "access_token": "at-0"})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at-1"})
		}
	})
	mux.HandleFunc("/api/sites", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			http.Error(w, "bad token", 401)
			return
		}
		_, _ = w.Write([]byte(`{"siteEntry":[{"siteUrl":"sc-domain:example.com"}]}`))
	})
	mux.HandleFunc("/api/sites/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "searchAnalytics/query") {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Dimensions []string
			RowLimit   int
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Dimensions) == 2 && req.Dimensions[0] == "page" { // the page-level cut
			if failPages.Load() {
				http.Error(w, "quota exceeded", 429)
				return
			}
			_, _ = w.Write([]byte(`{"rows":[
				{"keys":["https://example.com/","self hosted analytics"],"clicks":40,"impressions":800,"ctr":0.05,"position":3.4},
				{"keys":["https://example.com/blog","self hosted analytics"],"clicks":8,"impressions":300,"ctr":0.027,"position":7.8}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"rows":[
			{"keys":["self hosted analytics"],"clicks":42,"impressions":900,"ctr":0.046,"position":3.2},
			{"keys":["plausible alternative"],"clicks":17,"impressions":400,"ctr":0.042,"position":5.1}]}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, failPages
}

func TestGSCFlow(t *testing.T) {
	srv, _ := fakeGoogle(t)
	oldTok, oldAPI := TokenEndpoint, APIEndpoint
	TokenEndpoint, APIEndpoint = srv.URL+"/token", srv.URL+"/api"
	t.Cleanup(func() { TokenEndpoint, APIEndpoint = oldTok, oldAPI })

	c := Creds{ClientID: "id", ClientSecret: "sec"}
	ctx := context.Background()

	// exchange
	rt, err := Exchange(ctx, c, "good-code", "http://127.0.0.1:8931/callback")
	if err != nil || rt != "rt-1" {
		t.Fatalf("exchange: %v %q", err, rt)
	}
	if _, err := Exchange(ctx, c, "bad-code", "x"); err == nil {
		t.Fatal("bad code must error with guidance")
	}

	// list sites
	sites, err := ListSites(ctx, c, rt)
	if err != nil || len(sites) != 1 || sites[0] != "sc-domain:example.com" {
		t.Fatalf("sites: %v %v", err, sites)
	}

	// fetch queries
	rows, err := FetchQueries(ctx, c, rt, sites[0], 28)
	if err != nil || len(rows) != 2 {
		t.Fatalf("fetch: %v %d", err, len(rows))
	}
	if rows[0].Query != "self hosted analytics" || rows[0].Clicks != 42 || rows[0].CTRPct < 4.5 {
		t.Fatalf("row mapping: %+v", rows[0])
	}

	// store round-trip + prev-rows for movers
	st, _ := Open(filepath.Join(t.TempDir(), "gsc.json"))
	if err := st.SetGrant(rt, sites[0]); err != nil {
		t.Fatal(err)
	}
	if !st.Connected() {
		t.Fatal("should be connected")
	}
	_ = st.SetRows(rows[:1])
	_ = st.SetRows(rows) // second fetch → first becomes prev
	cur, prev, site, _ := st.Snapshot()
	if len(cur) != 2 || len(prev) != 1 || site != "sc-domain:example.com" {
		t.Fatalf("snapshot: cur=%d prev=%d site=%s", len(cur), len(prev), site)
	}
}

// Page-level fetch: dimension mapping, store round-trip with prev, and
// persistence across reopen.
func TestGSCPageRows(t *testing.T) {
	srv, _ := fakeGoogle(t)
	oldTok, oldAPI := TokenEndpoint, APIEndpoint
	TokenEndpoint, APIEndpoint = srv.URL+"/token", srv.URL+"/api"
	t.Cleanup(func() { TokenEndpoint, APIEndpoint = oldTok, oldAPI })

	c := Creds{ClientID: "id", ClientSecret: "sec"}
	ctx := context.Background()

	pages, err := FetchPages(ctx, c, "rt-1", "sc-domain:example.com", 28)
	if err != nil || len(pages) != 2 {
		t.Fatalf("fetch pages: %v %d", err, len(pages))
	}
	if pages[0].Page != "https://example.com/" || pages[0].Query != "self hosted analytics" ||
		pages[0].Clicks != 40 || pages[0].Impressions != 800 || pages[0].Position != 3.4 {
		t.Fatalf("page row mapping: %+v", pages[0])
	}

	path := filepath.Join(t.TempDir(), "gsc.json")
	st, _ := Open(path)
	_ = st.SetGrant("rt-1", "sc-domain:example.com")
	_ = st.SetPageRows(pages[:1])
	_ = st.SetPageRows(pages) // second fetch → first becomes prev, mirroring Rows
	cur, prev, fetchErr := st.PageSnapshot()
	if len(cur) != 2 || len(prev) != 1 || fetchErr != "" {
		t.Fatalf("page snapshot: cur=%d prev=%d err=%q", len(cur), len(prev), fetchErr)
	}

	re, err := Open(path) // reopen — page rows must persist
	if err != nil {
		t.Fatal(err)
	}
	cur, prev, _ = re.PageSnapshot()
	if len(cur) != 2 || len(prev) != 1 || cur[1].Query != "self hosted analytics" {
		t.Fatalf("reopened page snapshot: cur=%d prev=%d", len(cur), len(prev))
	}
}

// A store file written before page-level rows existed must load cleanly:
// missing fields read as empty, everything else intact.
func TestGSCOldFileCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gsc.json")
	old := `{"refresh_token":"rt-1","site_url":"sc-domain:example.com",` +
		`"rows":[{"query":"q1","clicks":3,"impressions":50,"ctr_pct":6,"position":4.2}],` +
		`"prev_rows":null,"fetched_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("old file must load cleanly: %v", err)
	}
	if !st.Connected() {
		t.Fatal("grant from the old file must survive")
	}
	rows, _, site, _ := st.Snapshot()
	if len(rows) != 1 || rows[0].Query != "q1" || site != "sc-domain:example.com" {
		t.Fatalf("old rows must survive: %+v %s", rows, site)
	}
	pages, prevPages, fetchErr := st.PageSnapshot()
	if len(pages) != 0 || len(prevPages) != 0 || fetchErr != "" {
		t.Fatalf("missing page fields must read as empty: %d %d %q", len(pages), len(prevPages), fetchErr)
	}
	// and the old file upgrades in place: page rows persist next to the old rows
	if err := st.SetPageRows([]PageRow{{Page: "/", Query: "q1", Clicks: 1, Impressions: 20, Position: 5}}); err != nil {
		t.Fatal(err)
	}
	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, _ = re.Snapshot()
	pages, _, _ = re.PageSnapshot()
	if len(rows) != 1 || len(pages) != 1 {
		t.Fatalf("upgrade must keep both cuts: rows=%d pages=%d", len(rows), len(pages))
	}
}

// Poll fetches queries and pages in one cycle — and a page-level failure must
// not discard anything: fresh query rows land, cached page rows stay, and the
// error is recorded for the report to surface.
func TestGSCPollBestEffortPages(t *testing.T) {
	srv, failPages := fakeGoogle(t)
	oldTok, oldAPI := TokenEndpoint, APIEndpoint
	TokenEndpoint, APIEndpoint = srv.URL+"/token", srv.URL+"/api"
	t.Cleanup(func() { TokenEndpoint, APIEndpoint = oldTok, oldAPI })

	c := Creds{ClientID: "id", ClientSecret: "sec"}
	ctx := context.Background()

	// happy path: one Poll pulls both cuts
	st, _ := Open(filepath.Join(t.TempDir(), "gsc.json"))
	_ = st.SetGrant("rt-1", "sc-domain:example.com")
	if err := st.Poll(ctx, c); err != nil {
		t.Fatal(err)
	}
	rows, _, _, _ := st.Snapshot()
	pages, _, fetchErr := st.PageSnapshot()
	if len(rows) != 2 || len(pages) != 2 || fetchErr != "" {
		t.Fatalf("poll should fetch both cuts: rows=%d pages=%d err=%q", len(rows), len(pages), fetchErr)
	}

	// page cut down: queries still land, cached pages survive, error recorded
	failPages.Store(true)
	st2, _ := Open(filepath.Join(t.TempDir(), "gsc.json"))
	_ = st2.SetGrant("rt-1", "sc-domain:example.com")
	cached := []PageRow{{Page: "/", Query: "old", Clicks: 5, Impressions: 100, Position: 6}}
	_ = st2.SetPageRows(cached) // from an earlier successful pull
	if err := st2.Poll(ctx, c); err != nil {
		t.Fatalf("page failure must not fail the poll (queries landed): %v", err)
	}
	rows, _, _, _ = st2.Snapshot()
	if len(rows) != 2 {
		t.Fatalf("query rows must land despite the page failure: %d", len(rows))
	}
	pages, _, fetchErr = st2.PageSnapshot()
	if len(pages) != 1 || pages[0].Query != "old" {
		t.Fatalf("cached page rows must survive the failure: %+v", pages)
	}
	if fetchErr == "" || !strings.Contains(fetchErr, "429") {
		t.Fatalf("the page failure must be recorded for the report: %q", fetchErr)
	}

	// next successful set clears the recorded error
	failPages.Store(false)
	if err := st2.SetPageRows(cached); err != nil {
		t.Fatal(err)
	}
	if _, _, fetchErr = st2.PageSnapshot(); fetchErr != "" {
		t.Fatalf("a successful set must clear the error: %q", fetchErr)
	}
}
