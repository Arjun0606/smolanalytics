package api

// The agreement test — CI enforcement of the product's core promise: the answer your
// AI gives over MCP is byte-for-byte the SAME computation the HTTP API returns and the
// dashboard renders. There is no second query path that can drift (competitors' AI
// layers generate queries and admit results "may not match the UI"; ours cannot,
// and this test is why that claim stays true forever). If a default, a window, or a
// boundary ever diverges between surfaces, this fails the build.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func agreementServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	h := New(st).Handler()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < 40; i++ {
		u := fmt.Sprintf("u%d", i)
		age := time.Duration(i%14) * 24 * time.Hour // spread across two weeks
		batch = append(batch, map[string]any{
			"name": "signup", "distinct_id": u, "timestamp": now.Add(-age).Format(time.RFC3339),
			"properties": map[string]any{"plan": map[bool]string{true: "pro", false: "free"}[i%3 == 0], "path": "/"},
		})
		if i%2 == 0 {
			batch = append(batch, map[string]any{
				"name": "activate", "distinct_id": u, "timestamp": now.Add(-age + time.Hour).Format(time.RFC3339),
			})
		}
		if i%4 == 0 {
			batch = append(batch, map[string]any{
				"name": "checkout", "distinct_id": u, "timestamp": now.Add(-age + 2*time.Hour).Format(time.RFC3339),
			})
		}
		batch = append(batch, map[string]any{
			"name": "$pageview", "distinct_id": u, "timestamp": now.Add(-age).Format(time.RFC3339),
			"properties": map[string]any{"path": "/pricing", "referrer": "https://news.ycombinator.com/", "device": "desktop"},
		})
	}
	body, _ := json.Marshal(batch)
	resp, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed ingest failed: %v %v", err, resp)
	}
	return srv
}

func viaAPI(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %v (%v)", path, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func viaMCP(t *testing.T, srv *httptest.Server, tool, args string) map[string]any {
	t.Helper()
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, args)
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(req))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("mcp %s: %v (%v)", tool, err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var envelope struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.IsError {
		t.Fatalf("mcp %s returned error: %s", tool, envelope.Result.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMCPAPIAgreement: the same question through both public surfaces must produce
// the identical answer — same defaults, same windows, same boundaries, same numbers.
func TestMCPAPIAgreement(t *testing.T) {
	srv := agreementServer(t)

	filters := `[{"property":"plan","op":"eq","value":"pro"}]`
	cases := []struct {
		name   string
		api    string
		tool   string
		args   string
		subset bool // MCP may ADD model-facing context (notes, extra counts) but every API field must match
	}{
		{"funnel default window", "/v1/funnel?steps=signup,activate,checkout", "funnel", `{"steps":["signup","activate","checkout"]}`, false},
		{"funnel filtered", "/v1/funnel?steps=signup,activate&filters=" + urlEnc(filters), "funnel", `{"steps":["signup","activate"],"filters":` + filters + `}`, false},
		{"trends", "/v1/trends?event=signup", "trends", `{"event":"signup"}`, false},
		{"retention", "/v1/retention?days=7&event=signup", "retention", `{"days":7,"event":"signup"}`, true},
		{"retention capped at 90 both sides", "/v1/retention?days=500&event=signup", "retention", `{"days":500,"event":"signup"}`, true},
		{"web overview", "/v1/web?days=30", "web_overview", `{"days":30}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := viaAPI(t, srv, c.api)
			m := viaMCP(t, srv, c.tool, c.args)
			if c.subset {
				for k, av := range a {
					if !reflect.DeepEqual(av, m[k]) {
						aj, _ := json.Marshal(av)
						mj, _ := json.Marshal(m[k])
						t.Fatalf("SURFACES DISAGREE on %s field %q —\nAPI: %s\nMCP: %s", c.name, k, aj, mj)
					}
				}
				return
			}
			if !reflect.DeepEqual(a, m) {
				aj, _ := json.MarshalIndent(a, "", " ")
				mj, _ := json.MarshalIndent(m, "", " ")
				t.Fatalf("SURFACES DISAGREE on %s —\nAPI:\n%s\nMCP:\n%s", c.name, aj, mj)
			}
		})
	}
}

func urlEnc(s string) string { return url.QueryEscape(s) }
