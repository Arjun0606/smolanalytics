// Package gsc integrates Google Search Console: the search queries that bring
// people to your site, next to what they did after arriving — the one report
// neither GA nor the privacy tools unify well. BYO Google OAuth client (two env
// vars), a one-command loopback auth, a small daily poller, and the same three
// surfaces as everything else: dashboard card, MCP tool, verdict material.
//
// Stdlib only, like the rest of the binary. Google endpoints are package vars so
// tests run against a fake server.
package gsc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Endpoints — overridable in tests.
var (
	AuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenEndpoint = "https://oauth2.googleapis.com/token"
	APIEndpoint   = "https://searchconsole.googleapis.com/webmasters/v3"
)

const scope = "https://www.googleapis.com/auth/webmasters.readonly"

// Creds is the BYO OAuth client (SMOLANALYTICS_GSC_CLIENT_ID / _SECRET).
type Creds struct{ ClientID, ClientSecret string }

func CredsFromEnv() (Creds, bool) {
	c := Creds{
		ClientID:     os.Getenv("SMOLANALYTICS_GSC_CLIENT_ID"),
		ClientSecret: os.Getenv("SMOLANALYTICS_GSC_CLIENT_SECRET"),
	}
	return c, c.ClientID != "" && c.ClientSecret != ""
}

// Row is one search query's performance.
type Row struct {
	Query       string  `json:"query"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	CTRPct      float64 `json:"ctr_pct"`
	Position    float64 `json:"position"`
}

// PageRow is one (page, query) pair's performance — the page-level cut that
// powers the money-pages report (quick wins, CTR problems, cannibalization).
type PageRow struct {
	Page        string  `json:"page"`
	Query       string  `json:"query"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	Position    float64 `json:"position"`
}

// state is what persists: the grant, the chosen property, and the latest rows.
// Fields are additive — files written before a field existed load with it empty.
type state struct {
	RefreshToken string    `json:"refresh_token"`
	SiteURL      string    `json:"site_url"` // GSC property, e.g. sc-domain:example.com or https://example.com/
	Rows         []Row     `json:"rows"`
	PrevRows     []Row     `json:"prev_rows"` // the prior fetch — powers top-mover deltas
	FetchedAt    time.Time `json:"fetched_at"`
	PageRows     []PageRow `json:"page_rows,omitempty"`
	PrevPageRows []PageRow `json:"prev_page_rows,omitempty"`
	// PageFetchError is the last page-level fetch failure — pages are best-effort
	// (query rows land regardless), so the failure surfaces in the report instead
	// of silently going stale.
	PageFetchError string `json:"page_fetch_error,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
	d    state
}

func Open(p string) (*Store, error) {
	s := &Store{path: p}
	if p == "" {
		return s, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.d); err != nil {
			return nil, fmt.Errorf("gsc file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.RefreshToken != "" && s.d.SiteURL != ""
}

func (s *Store) Snapshot() (rows, prev []Row, site string, fetched time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Row(nil), s.d.Rows...), append([]Row(nil), s.d.PrevRows...), s.d.SiteURL, s.d.FetchedAt
}

// PageSnapshot returns the page-level rows plus the last page-fetch error (empty
// when the last page fetch succeeded — or never ran).
func (s *Store) PageSnapshot() (rows, prev []PageRow, fetchErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PageRow(nil), s.d.PageRows...), append([]PageRow(nil), s.d.PrevPageRows...), s.d.PageFetchError
}

func (s *Store) SetGrant(refreshToken, siteURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.RefreshToken = refreshToken
	s.d.SiteURL = siteURL
	return s.persistLocked()
}

func (s *Store) SetRows(rows []Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d.Rows) > 0 {
		s.d.PrevRows = s.d.Rows
	}
	s.d.Rows = rows
	s.d.FetchedAt = time.Now().UTC()
	return s.persistLocked()
}

// SetPageRows stores the latest page-level rows; the prior fetch becomes
// PrevPageRows (mirroring Rows/PrevRows) and any recorded fetch error clears.
func (s *Store) SetPageRows(rows []PageRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d.PageRows) > 0 {
		s.d.PrevPageRows = s.d.PageRows
	}
	s.d.PageRows = rows
	s.d.PageFetchError = ""
	return s.persistLocked()
}

// SetPageFetchError records a page-level fetch failure without touching the
// cached rows, so the report can say "pages are stale/missing because X".
func (s *Store) SetPageFetchError(msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.PageFetchError = msg
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// AuthURL builds the consent URL for the loopback flow.
func AuthURL(c Creds, redirect string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"redirect_uri":  {redirect},
		"response_type": {"code"},
		"scope":         {scope},
		"access_type":   {"offline"},
		"prompt":        {"consent"}, // always mint a refresh token
	}
	return AuthEndpoint + "?" + q.Encode()
}

// Exchange trades the auth code for a refresh token.
func Exchange(ctx context.Context, c Creds, code, redirect string) (string, error) {
	body := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirect},
	}
	var out struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	if err := postForm(ctx, TokenEndpoint, body, &out); err != nil {
		return "", err
	}
	if out.RefreshToken == "" {
		return "", fmt.Errorf("google returned no refresh token (%s) — remove the app's prior grant at myaccount.google.com/permissions and retry", out.Error)
	}
	return out.RefreshToken, nil
}

// accessToken refreshes the short-lived token.
func accessToken(ctx context.Context, c Creds, refreshToken string) (string, error) {
	body := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := postForm(ctx, TokenEndpoint, body, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token refresh failed (%s) — re-run `smolanalytics gsc auth`", out.Error)
	}
	return out.AccessToken, nil
}

// ListSites returns the GSC properties the grant can read.
func ListSites(ctx context.Context, c Creds, refreshToken string) ([]string, error) {
	tok, err := accessToken(ctx, c, refreshToken)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", APIEndpoint+"/sites", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	var out struct {
		SiteEntry []struct {
			SiteURL string `json:"siteUrl"`
		} `json:"siteEntry"`
	}
	if err := do(req, &out); err != nil {
		return nil, err
	}
	sites := make([]string, 0, len(out.SiteEntry))
	for _, e := range out.SiteEntry {
		sites = append(sites, e.SiteURL)
	}
	return sites, nil
}

// apiRow is one Search Analytics result row as Google returns it.
type apiRow struct {
	Keys        []string `json:"keys"`
	Clicks      float64  `json:"clicks"`
	Impressions float64  `json:"impressions"`
	CTR         float64  `json:"ctr"`
	Position    float64  `json:"position"`
}

// searchAnalytics runs one Search Analytics query for the trailing `days`
// (GSC data lags ~2 days, so the window ends day-2 — the honest window, not
// the wishful one).
func searchAnalytics(ctx context.Context, c Creds, refreshToken, siteURL string, days int, dimensions []string, rowLimit int) ([]apiRow, error) {
	tok, err := accessToken(ctx, c, refreshToken)
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 28
	}
	end := time.Now().UTC().AddDate(0, 0, -2)
	start := end.AddDate(0, 0, -days)
	payload, _ := json.Marshal(map[string]any{
		"startDate":  start.Format("2006-01-02"),
		"endDate":    end.Format("2006-01-02"),
		"dimensions": dimensions,
		"rowLimit":   rowLimit,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		APIEndpoint+"/sites/"+url.PathEscape(siteURL)+"/searchAnalytics/query", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Rows []apiRow `json:"rows"`
	}
	if err := do(req, &out); err != nil {
		return nil, err
	}
	return out.Rows, nil
}

// FetchQueries pulls top queries for the trailing `days`.
func FetchQueries(ctx context.Context, c Creds, refreshToken, siteURL string, days int) ([]Row, error) {
	raw, err := searchAnalytics(ctx, c, refreshToken, siteURL, days, []string{"query"}, 100)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(raw))
	for _, r := range raw {
		if len(r.Keys) == 0 {
			continue
		}
		rows = append(rows, Row{
			Query:       r.Keys[0],
			Clicks:      int(r.Clicks),
			Impressions: int(r.Impressions),
			CTRPct:      r.CTR * 100,
			Position:    r.Position,
		})
	}
	return rows, nil
}

// FetchPages pulls (page, query) pairs for the trailing `days` — the raw
// material for the money-pages report.
func FetchPages(ctx context.Context, c Creds, refreshToken, siteURL string, days int) ([]PageRow, error) {
	raw, err := searchAnalytics(ctx, c, refreshToken, siteURL, days, []string{"page", "query"}, 500)
	if err != nil {
		return nil, err
	}
	rows := make([]PageRow, 0, len(raw))
	for _, r := range raw {
		if len(r.Keys) < 2 {
			continue
		}
		rows = append(rows, PageRow{
			Page:        r.Keys[0],
			Query:       r.Keys[1],
			Clicks:      int(r.Clicks),
			Impressions: int(r.Impressions),
			Position:    r.Position,
		})
	}
	return rows, nil
}

// Poll refreshes the cached rows if stale (12h). Safe to call on a timer.
// Queries land first; the page-level fetch is best-effort — a page failure
// never discards fresh query rows, it's recorded and surfaced in the report.
func (s *Store) Poll(ctx context.Context, c Creds) error {
	s.mu.Lock()
	stale := time.Since(s.d.FetchedAt) > 12*time.Hour
	rt, site := s.d.RefreshToken, s.d.SiteURL
	s.mu.Unlock()
	if !stale || rt == "" || site == "" {
		return nil
	}
	rows, err := FetchQueries(ctx, c, rt, site, 28)
	if err != nil {
		return err
	}
	if err := s.SetRows(rows); err != nil {
		return err
	}
	pages, err := FetchPages(ctx, c, rt, site, 28)
	if err != nil {
		return s.SetPageFetchError(err.Error())
	}
	return s.SetPageRows(pages)
}

func postForm(ctx context.Context, endpoint string, body url.Values, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(req, out)
}

func do(req *http.Request, out any) error {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("google api %d: %s", resp.StatusCode, string(b[:min(len(b), 200)]))
	}
	return json.Unmarshal(b, out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
