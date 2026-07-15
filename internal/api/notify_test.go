package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

// POST /v1/webhooks/{id}/test is the one write reachable with an API key (it
// mutates nothing) so the MCP test_webhook tool and scripts share the dashboard's
// delivery path. Creating webhooks must stay session-only.
func TestWebhookTestEndpointKeyAuth(t *testing.T) {
	t.Setenv("SMOLANALYTICS_PASSWORD", "hunter2")
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest receiver is loopback

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	srv := New(memory.New())
	srv.SetReadKey("k123") // the webhook test endpoint is key-OR-session — the read key, not the public write key
	wh, err := webhook.Open("")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetWebhooks(wh)
	ep, err := wh.Add("target", receiver.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	// no session, no key → 401
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/v1/webhooks/"+ep.ID+"/test", nil))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated test: got %d, want 401 (%s)", r.Code, r.Body.String())
	}

	// API key → allowed, and the response reports the endpoint's real status
	req := httptest.NewRequest("POST", "/v1/webhooks/"+ep.ID+"/test", nil)
	req.Header.Set("Authorization", "Bearer k123")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("key-authed test: got %d, want 200 (%s)", r.Code, r.Body.String())
	}
	if !strings.Contains(r.Body.String(), `"endpoint_status":200`) {
		t.Fatalf("response should carry the endpoint's HTTP status: %s", r.Body.String())
	}

	// other webhook writes stay session-only even with a valid key
	req = httptest.NewRequest("POST", "/v1/webhooks", strings.NewReader(`{"name":"x","url":"https://example.com/hook"}`))
	req.Header.Set("Authorization", "Bearer k123")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("create webhook with key only: got %d, want 401", r.Code)
	}
}
