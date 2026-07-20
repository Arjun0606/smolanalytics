package api

// End-to-end journey test: boot the FULL HTTP server the way a cloud tenant runs (public
// write key + secret read key + dashboard password), seed a realistic SaaS dataset with a
// mobile-activation leak, then exercise it the way a real user + agent does — and assert
// the four things that must never regress:
//
//	1. the security model  — the PUBLIC write key can ingest but can never read a report,
//	   the raw export, the verdict, or MCP; the SECRET read key can.
//	2. the covenant        — the ask bar, GET /v1, and MCP return the SAME number.
//	3. the verdict         — it names the segment to blame (device=mobile).
//	4. correctness         — recomputing from the raw export equals what the report says.
//
// This is the black-box complement to the unit + agreement tests: it proves the whole
// wired-together server behaves, not just the pieces.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestFullUserJourneyE2E(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "e2e-pass-1234")
	s := New(memory.New())
	s.SetWriteKey("wk_public") // ships in the SDK — ingest only
	s.SetReadKey("rk_secret")  // reports + MCP — never in client code
	h := s.Handler()

	do := func(method, path, key, body string) (int, string) {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	// ---- 1. seed a realistic dataset (mobile activates ~2x worse — the story) ----
	now := time.Now().UTC()
	rng := newRng(7)
	sources := []string{"google", "twitter", "hacker news", "reddit", "direct"}
	refs := map[string]string{"google": "https://www.google.com/", "twitter": "https://t.co/",
		"hacker news": "https://news.ycombinator.com/", "reddit": "https://www.reddit.com/", "direct": ""}
	var batch []map[string]any
	emit := func(name, uid string, ts time.Time, props map[string]any) {
		batch = append(batch, map[string]any{"name": name, "distinct_id": uid,
			"timestamp": ts.Format(time.RFC3339), "properties": props})
	}
	signups := 0
	for d := 0; d < 30; d++ {
		// midnight of that calendar day, then a random time-of-day — real events are always
		// in the past, so skip anything that would land after `now` on the current day.
		day0 := now.Truncate(24*time.Hour).AddDate(0, 0, -(29 - d))
		for i := 0; i < 40; i++ {
			uid := fmt.Sprintf("u%d_%d", d, i)
			t0 := day0.Add(time.Duration(rng.intn(23))*time.Hour + time.Duration(rng.intn(60))*time.Minute)
			if !t0.Before(now) {
				continue // don't seed future-dated events (that's the covenant guard's job, below)
			}
			src := sources[rng.intn(len(sources))]
			device := "desktop"
			if rng.f() < 0.35 {
				device = "mobile"
			}
			plan := "free"
			if rng.f() < 0.3 {
				plan = "pro"
			}
			emit("$pageview", uid, t0.Add(-3*time.Minute), map[string]any{"path": "/", "referrer": refs[src], "device": device})
			emit("signup", uid, t0, map[string]any{"plan": plan, "device": device, "source": src})
			signups++
			actP := 0.55
			if plan == "pro" {
				actP = 0.78
			}
			if device == "mobile" {
				actP *= 0.45 // the leak
			}
			if rng.f() < actP {
				emit("activate", uid, t0.Add(time.Hour), map[string]any{"plan": plan, "device": device})
				coP := 0.42
				if plan == "pro" {
					coP = 0.6
				}
				if rng.f() < coP {
					amount := 29.0 // deterministic so the windowed-measure covenant is checkable
					if plan == "pro" {
						amount = 99.0
					}
					emit("checkout", uid, t0.Add(2*time.Hour), map[string]any{"plan": plan, "amount": amount})
				}
			}
			for dd := 1; dd <= 7; dd++ {
				if rng.f() < 0.5/float64(dd) {
					ret := t0.AddDate(0, 0, dd)
					if ret.Before(now) {
						emit("open", uid, ret, map[string]any{"device": device})
					}
				}
			}
		}
	}
	// batch-ingest with the WRITE key
	for i := 0; i < len(batch); i += 500 {
		end := i + 500
		if end > len(batch) {
			end = len(batch)
		}
		b, _ := json.Marshal(batch[i:end])
		if code, _ := do("POST", "/v1/events", "wk_public", string(b)); code >= 300 {
			t.Fatalf("ingest batch %d: status %d", i, code)
		}
	}
	t.Logf("seeded %d events, %d signups", len(batch), signups)

	// ---- 2. SECURITY: the public write key must never read ----
	sec := []struct {
		name, method, path, key string
		want                    int
	}{
		{"write key ingests", "POST", "/v1/events", "wk_public", 202},
		{"write key CANNOT read reports", "GET", "/v1/trends?event=signup", "wk_public", 401},
		{"write key CANNOT read raw export", "GET", "/v1/export", "wk_public", 401},
		{"write key in ?key= CANNOT read export", "GET", "/v1/export?key=wk_public", "", 401},
		{"write key CANNOT read the verdict", "GET", "/v1/notable", "wk_public", 401},
		{"write key CANNOT use MCP", "POST", "/mcp", "wk_public", 401},
		{"read key CAN read reports", "GET", "/v1/trends?event=signup", "rk_secret", 200},
		{"read key CAN read export", "GET", "/v1/export", "rk_secret", 200},
		{"no credential is rejected", "GET", "/v1/trends?event=signup", "", 401},
	}
	for _, c := range sec {
		body := ""
		if c.method == "POST" && strings.Contains(c.path, "events") {
			body = `{"name":"signup","distinct_id":"smoke"}`
		} else if c.method == "POST" {
			body = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
		}
		if code, _ := do(c.method, c.path, c.key, body); code != c.want {
			t.Errorf("SECURITY %s: got %d, want %d", c.name, code, c.want)
		}
	}

	// ---- 3. THE COVENANT: ask (session) == GET /v1 == MCP ----
	numRe := regexp.MustCompile(`(\d[\d,]*)`)
	sess := loginSession(t, h, "e2e-pass-1234")
	askReq := httptest.NewRequest("POST", "/v1/ask", strings.NewReader(`{"question":"how many signups in the last 7 days"}`))
	askReq.Header.Set("Content-Type", "application/json")
	askReq.AddCookie(sess)
	aw := httptest.NewRecorder()
	h.ServeHTTP(aw, askReq)
	if aw.Code != 200 {
		t.Fatalf("ask via session: got %d", aw.Code)
	}
	var askResp struct{ Answer string }
	_ = json.Unmarshal(aw.Body.Bytes(), &askResp)
	askN := atoiComma(numRe.FindString(askResp.Answer))

	_, tb := do("GET", "/v1/trends?event=signup&days=7", "rk_secret", "")
	var tr struct{ Total int }
	_ = json.Unmarshal([]byte(tb), &tr)

	_, mb := do("POST", "/mcp", "rk_secret", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","days":7}}}`)
	mcpN := mcpTotal(t, mb)

	t.Logf("covenant: ask=%d /v1=%d mcp=%d (ask: %q)", askN, tr.Total, mcpN, askResp.Answer)
	if !(askN > 0 && askN == tr.Total && tr.Total == mcpN) {
		t.Errorf("COVENANT BROKEN: ask=%d, /v1=%d, MCP=%d must all be equal and >0", askN, tr.Total, mcpN)
	}

	// ---- 3b. COVENANT UNDER CLOCK SKEW: a future-dated event must not diverge the surfaces.
	// Browser clocks are wrong all the time. Ingest now CLAMPS any future timestamp to now, so
	// the event is never stored future-dated — it counts as happening "now", in the last-7-days
	// window, CONSISTENTLY on every surface. (A future-dated event previously survived ingest
	// and was counted by some surfaces but not others.) So all three must be equal AND == askN+1.
	future := now.Add(30 * time.Minute).Format(time.RFC3339)
	if code, _ := do("POST", "/v1/events", "wk_public",
		fmt.Sprintf(`[{"name":"signup","distinct_id":"clock_skewed","timestamp":%q,"properties":{"plan":"free"}}]`, future)); code >= 300 {
		t.Fatalf("ingest future event: status %d", code)
	}
	skewReq := httptest.NewRequest("POST", "/v1/ask", strings.NewReader(`{"question":"how many signups in the last 7 days"}`))
	skewReq.Header.Set("Content-Type", "application/json")
	skewReq.AddCookie(sess)
	sw := httptest.NewRecorder()
	h.ServeHTTP(sw, skewReq)
	var skewResp struct{ Answer string }
	_ = json.Unmarshal(sw.Body.Bytes(), &skewResp)
	askSkew := atoiComma(numRe.FindString(skewResp.Answer))
	_, tsb := do("GET", "/v1/trends?event=signup&days=7", "rk_secret", "")
	var trSkew struct{ Total int }
	_ = json.Unmarshal([]byte(tsb), &trSkew)
	_, msb := do("POST", "/mcp", "rk_secret", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"signup","days":7}}}`)
	mcpSkew := mcpTotal(t, msb)
	t.Logf("clock-skew guard: ask=%d /v1=%d mcp=%d (all must equal %d — future event clamped to now, counted everywhere)", askSkew, trSkew.Total, mcpSkew, askN+1)
	if !(askSkew == trSkew.Total && trSkew.Total == mcpSkew && askSkew == askN+1) {
		t.Errorf("COVENANT BROKEN under clock skew: ask=%d, /v1=%d, MCP=%d must all equal %d (the future event is clamped to now and counted consistently)", askSkew, trSkew.Total, mcpSkew, askN+1)
	}

	// ---- 3c. MEASURE COVENANT: a windowed numeric aggregate (sum of checkout amount over the
	// last 7 days) must agree across /v1 and MCP AND equal a recomputation from the raw log —
	// and must NOT be the all-time sum. Regression guard for the ComputeMeasure window bug an
	// adversarial audit surfaced (res.Total was built from an unfiltered slice = all-time).
	winFrom := now.Truncate(24*time.Hour).AddDate(0, 0, -6)
	_, xbM := do("GET", "/v1/export?format=jsonl", "rk_secret", "")
	var wantWin, wantAll float64
	for _, line := range strings.Split(xbM, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e struct {
			Name       string
			Timestamp  time.Time
			Properties struct{ Amount float64 }
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Name != "checkout" {
			continue
		}
		wantAll += e.Properties.Amount
		if ts := e.Timestamp.UTC(); !ts.Before(winFrom) && ts.Before(now) {
			wantWin += e.Properties.Amount
		}
	}
	_, msumB := do("GET", "/v1/trends?event=checkout&measure=sum&property=amount&days=7", "rk_secret", "")
	var v1Sum struct{ Total float64 }
	_ = json.Unmarshal([]byte(msumB), &v1Sum)
	_, mcpSumB := do("POST", "/mcp", "rk_secret", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trends","arguments":{"event":"checkout","measure":"sum","property":"amount","days":7}}}`)
	mcpSum := mcpTotalF(t, mcpSumB)
	t.Logf("measure covenant: /v1 sum=%.0f MCP sum=%.0f recompute(win)=%.0f all-time=%.0f", v1Sum.Total, mcpSum, wantWin, wantAll)
	if wantWin <= 0 {
		t.Errorf("measure covenant: expected some checkout revenue in the last 7 days, got %.0f", wantWin)
	}
	// pure covenant: the two machine surfaces must return the EXACTLY equal windowed sum.
	if v1Sum.Total != mcpSum {
		t.Errorf("MEASURE COVENANT BROKEN: /v1 sum=%.0f != MCP sum=%.0f for the same windowed question", v1Sum.Total, mcpSum)
	}
	// window respected: the windowed sum must be at least my (slightly-narrower, test-start
	// clock) recompute and STRICTLY below the all-time sum. The old ComputeMeasure bug
	// returned the all-time aggregate (== wantAll) for this windowed query. The lower bound
	// uses >= (not ==) because the server's window ends at its own query-time now, which can
	// include a boundary event clamped to ~now that my test-start recompute excludes.
	if v1Sum.Total < wantWin || v1Sum.Total >= wantAll {
		t.Errorf("MEASURE WINDOW IGNORED: /v1 windowed sum=%.0f must be in [%.0f, %.0f) — >= all-time means the window was dropped", v1Sum.Total, wantWin, wantAll)
	}

	// ---- 4. VERDICT names the segment to blame ----
	_, nb := do("GET", "/v1/notable", "rk_secret", "")
	var notable struct {
		Findings []struct{ Title, Detail string }
	}
	_ = json.Unmarshal([]byte(nb), &notable)
	if len(notable.Findings) == 0 {
		t.Error("VERDICT: no findings returned")
	}
	joined := ""
	for _, f := range notable.Findings {
		joined += f.Title + " " + f.Detail + " | "
	}
	if !strings.Contains(strings.ToLower(joined), "mobile") {
		t.Errorf("VERDICT: expected the mobile-activation leak to be named; got: %s", joined)
	}

	// ---- 5. CORRECTNESS: recompute signups from the raw export == the report ----
	_, xb := do("GET", "/v1/export?format=jsonl", "rk_secret", "")
	raw := 0
	for _, line := range strings.Split(xb, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e struct{ Name string }
		if json.Unmarshal([]byte(line), &e) == nil && e.Name == "signup" {
			raw++
		}
	}
	_, allB := do("GET", "/v1/trends?event=signup", "rk_secret", "")
	var all struct{ Total int }
	_ = json.Unmarshal([]byte(allB), &all)
	t.Logf("correctness: export=%d report=%d", raw, all.Total)
	if raw == 0 || raw != all.Total {
		t.Errorf("CORRECTNESS: raw export signups=%d must equal report total=%d", raw, all.Total)
	}
}

// --- tiny deterministic helpers (no external rng dependency, no Date.now in the seed) ---

type rng struct{ s uint64 }

func newRng(seed uint64) *rng { return &rng{s: seed*2654435761 + 1} }
func (r *rng) next() uint64   { r.s ^= r.s << 13; r.s ^= r.s >> 7; r.s ^= r.s << 17; return r.s }
func (r *rng) f() float64     { return float64(r.next()%1_000_000) / 1_000_000 }
func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

func atoiComma(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	if s == "" {
		return -1
	}
	return n
}

func mcpTotal(t *testing.T, mcpResp string) int {
	t.Helper()
	var env struct {
		Result struct {
			Content []struct{ Text string }
		}
	}
	if json.Unmarshal([]byte(mcpResp), &env) != nil || len(env.Result.Content) == 0 {
		return -1
	}
	var inner struct{ Total int }
	if json.Unmarshal([]byte(env.Result.Content[0].Text), &inner) != nil {
		return -1
	}
	return inner.Total
}

// mcpTotalF is mcpTotal for a float aggregate (measure=sum/avg/... returns a fractional total).
func mcpTotalF(t *testing.T, mcpResp string) float64 {
	t.Helper()
	var env struct {
		Result struct {
			Content []struct{ Text string }
		}
	}
	if json.Unmarshal([]byte(mcpResp), &env) != nil || len(env.Result.Content) == 0 {
		return -1
	}
	var inner struct{ Total float64 }
	if json.Unmarshal([]byte(env.Result.Content[0].Text), &inner) != nil {
		return -1
	}
	return inner.Total
}
