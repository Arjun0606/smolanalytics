// Package webhook delivers outbound notifications to operator-configured URLs
// (used by alerts and the daily digest). Two delivery contracts: HMAC-signed JSON
// for generic endpoints, and Slack's {"text": ...} shape for Slack incoming
// webhooks (which reject anything else). Persisted store, best-effort async delivery.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FormatSlack marks an endpoint whose deliveries use Slack's incoming-webhook
// contract: {"text": "<plain-text rendering>"} instead of signed JSON.
const FormatSlack = "slack"

// Endpoint is one registered webhook target.
type Endpoint struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Secret  string    `json:"secret"` // signs the payload so the receiver can verify
	Format  string    `json:"format,omitempty"`
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`
}

// SlackFormat reports whether deliveries to e use Slack's {"text": ...} contract:
// either the endpoint was created with format "slack", or the URL is a Slack
// incoming webhook (hooks.slack.com) — the host check also covers endpoints
// persisted before the format field existed.
func (e Endpoint) SlackFormat() bool {
	return e.Format == FormatSlack || isSlackURL(e.URL)
}

func isSlackURL(raw string) bool {
	u, err := neturl.Parse(raw)
	return err == nil && strings.EqualFold(u.Hostname(), "hooks.slack.com")
}

type Store struct {
	mu    sync.Mutex
	path  string
	items []Endpoint
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("webhooks file corrupt: %w", err)
		}
	}
	return s, nil
}

func (s *Store) List() []Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Endpoint, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Endpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.items {
		if e.ID == id {
			return e, true
		}
	}
	return Endpoint{}, false
}

// Add registers a new endpoint. format is "" (auto-detect: Slack contract for
// hooks.slack.com URLs, signed JSON for everything else) or "slack" to force the
// Slack text contract for Slack-compatible receivers on other hosts (Mattermost,
// Rocket.Chat, …).
func (s *Store) Add(name, url, format string) (Endpoint, error) {
	if url == "" {
		return Endpoint{}, fmt.Errorf("url is required")
	}
	if u, err := neturl.Parse(url); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Endpoint{}, fmt.Errorf("webhook url must be http:// or https://")
	}
	switch format {
	case "":
		if isSlackURL(url) {
			format = FormatSlack
		}
	case FormatSlack:
	default:
		return Endpoint{}, fmt.Errorf("unknown format %q — pass \"slack\" for Slack-compatible receivers, or omit it (auto-detected from the URL)", format)
	}
	if name == "" {
		name = url
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := Endpoint{ID: token(6), Name: name, URL: url, Secret: "whsec_" + token(20), Format: format, Enabled: true, Created: time.Now().UTC()}
	s.items = append(s.items, e)
	if err := s.persist(); err != nil {
		s.items = s.items[:len(s.items)-1]
		return Endpoint{}, err
	}
	return e, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Endpoint, 0, len(old))
	for _, e := range old {
		if e.ID != id {
			out = append(out, e)
		}
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old
		return err
	}
	return nil
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// sign returns the HMAC-SHA256 signature the receiver verifies the body against.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SSRF guard: webhooks POST to operator-configured URLs, so a URL pointing at cloud
// metadata (169.254.169.254), loopback, or an internal service would let a webhook
// exfiltrate credentials or scan the private network. We check the *resolved* IP at dial
// time (which also defeats DNS-rebinding and blocks redirects into private space) and
// refuse private/reserved addresses. Operators who genuinely need an internal target can
// opt out with SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS (read per dial, so it takes effect
// without a restart).
func allowPrivateWebhooks() bool { return os.Getenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS") != "" }

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() // 169.254.169.254 (cloud metadata) is link-local
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				if allowPrivateWebhooks() {
					return nil
				}
				host, _, err := net.SplitHostPort(address) // address is host:port, host already resolved to an IP
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
					return fmt.Errorf("refusing to connect to private/reserved address %s (SSRF guard)", ip)
				}
				return nil
			},
		}).DialContext,
	},
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// Send POSTs one delivery to an endpoint and returns the HTTP status the endpoint
// answered with (0 when no response arrived). Slack-format endpoints receive
// {"text": text} — Slack rejects any other body shape and cannot verify signature
// headers — while every other endpoint keeps the signed-JSON contract unchanged:
// the body verbatim plus X-Smolanalytics-Signature. text is the plain-text
// rendering of body; if a caller passes none, the raw JSON body is used as the
// text so a Slack message still carries the facts instead of failing.
func Send(ep Endpoint, body []byte, text string) (int, error) {
	slack := ep.SlackFormat()
	if slack {
		if text == "" {
			text = string(body)
		}
		body, _ = json.Marshal(map[string]string{"text": text})
	}
	req, err := http.NewRequest(http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "smolanalytics-webhooks")
	if !slack {
		req.Header.Set("X-Smolanalytics-Signature", sign(ep.Secret, body))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("endpoint returned %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// SendTest fires a synthetic delivery through the exact path real alerts and
// digests take (same format rules, signing, SSRF guard, and HTTP client), so a
// 2xx here means real deliveries will land. Returns the endpoint's HTTP status.
// Shared by POST /v1/webhooks/{id}/test and the MCP test_webhook tool.
func SendTest(ep Endpoint) (int, error) {
	body, _ := json.Marshal(map[string]any{"type": "test", "message": "smolanalytics test webhook", "at": time.Now().UTC()})
	return Send(ep, body, "smolanalytics test — webhook delivery works.")
}

// DeliverAll fires the payload to every enabled endpoint, async + best-effort.
// text is the plain-text rendering that Slack-format endpoints receive.
func (s *Store) DeliverAll(payload any, text string) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, ep := range s.List() {
		if !ep.Enabled {
			continue
		}
		go func(ep Endpoint) { _, _ = Send(ep, body, text) }(ep)
	}
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
