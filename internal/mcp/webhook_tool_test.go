package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// addWebhookID creates a webhook via the tool and returns its id.
func addWebhookID(t *testing.T, s *Server, args string) string {
	t.Helper()
	out, err := callAct(t, s, "add_webhook", args)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Created struct {
			ID string `json:"id"`
		} `json:"created"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Created.ID == "" {
		t.Fatalf("add_webhook response missing created.id: %s", out)
	}
	return created.Created.ID
}

// test_webhook must fire through the REAL delivery path — a Slack-shaped receiver
// that rejects everything but {"text": ...} proves the format rules are honored —
// and report the endpoint's HTTP status.
func TestTestWebhookToolHappyPath(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest listens on loopback
	s := actionServer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &p) != nil || p.Text == "" {
			http.Error(w, "invalid_payload", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	id := addWebhookID(t, s, `{"name":"team slack","url":"`+srv.URL+`","format":"slack"}`)
	out, err := callAct(t, s, "test_webhook", `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatalf("test_webhook against a healthy slack-format endpoint should succeed: %v", err)
	}
	if !strings.Contains(out, "200") || !strings.Contains(out, "Slack said") {
		t.Fatalf("result should report Slack's HTTP status: %s", out)
	}
}

func TestTestWebhookToolFailurePaths(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	s := actionServer(t)

	revoked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no_service", http.StatusNotFound) // Slack's answer for a revoked URL
	}))
	defer revoked.Close()

	id := addWebhookID(t, s, `{"name":"revoked slack","url":"`+revoked.URL+`","format":"slack"}`)
	_, err := callAct(t, s, "test_webhook", `{"id":"`+id+`"}`)
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "recreate it in Slack") {
		t.Fatalf("a 404 from a slack endpoint must say the URL looks revoked and how to fix it, got: %v", err)
	}

	// unknown id must self-correct toward list_webhooks
	if _, err := callAct(t, s, "test_webhook", `{"id":"nope"}`); err == nil || !strings.Contains(err.Error(), "list_webhooks") {
		t.Fatalf("unknown id should point at list_webhooks: %v", err)
	}
	// missing id likewise
	if _, err := callAct(t, s, "test_webhook", `{}`); err == nil || !strings.Contains(err.Error(), "list_webhooks") {
		t.Fatalf("missing id should point at list_webhooks: %v", err)
	}
}

// add_webhook for a slack-shaped URL must say deliveries use Slack text format
// and steer the model to test_webhook — and must NOT hand out a secret the
// receiver can never verify.
func TestAddWebhookSlackShapedResponse(t *testing.T) {
	s := actionServer(t)
	out, err := callAct(t, s, "add_webhook", `{"name":"slack #alerts","url":"https://hooks.slack.com/services/T0/B0/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"format":"slack"`) {
		t.Fatalf("slack URL should be auto-detected as slack format: %s", out)
	}
	if !strings.Contains(out, "text") || !strings.Contains(out, "test_webhook") {
		t.Fatalf("response should explain Slack text format and suggest test_webhook: %s", out)
	}
	if strings.Contains(out, `"secret"`) {
		t.Fatalf("slack endpoints must not return a signing secret (Slack can't verify it): %s", out)
	}

	// non-slack keeps the signed-JSON response contract (secret shown once)
	out, err = callAct(t, s, "add_webhook", `{"name":"generic","url":"https://hooks.example.com/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"secret"`) || !strings.Contains(out, `"format":"json"`) {
		t.Fatalf("non-slack endpoint should return the secret and json format: %s", out)
	}
}
