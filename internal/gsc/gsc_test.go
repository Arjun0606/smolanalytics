package gsc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A fake Google: token endpoint + sites list + search analytics query.
func fakeGoogle(t *testing.T) *httptest.Server {
	t.Helper()
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
		var req struct{ RowLimit int }
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = w.Write([]byte(`{"rows":[
			{"keys":["self hosted analytics"],"clicks":42,"impressions":900,"ctr":0.046,"position":3.2},
			{"keys":["plausible alternative"],"clicks":17,"impressions":400,"ctr":0.042,"position":5.1}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGSCFlow(t *testing.T) {
	srv := fakeGoogle(t)
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
