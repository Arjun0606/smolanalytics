// Package webhook delivers outbound, HMAC-signed notifications to operator-
// configured URLs (used by alerts). Persisted store + signed POST, best-effort
// async delivery.
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
	"sync"
	"syscall"
	"time"
)

// Endpoint is one registered webhook target.
type Endpoint struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Secret  string    `json:"secret"` // signs the payload so the receiver can verify
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`
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

func (s *Store) Add(name, url string) (Endpoint, error) {
	if url == "" {
		return Endpoint{}, fmt.Errorf("url is required")
	}
	if u, err := neturl.Parse(url); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Endpoint{}, fmt.Errorf("webhook url must be http:// or https://")
	}
	if name == "" {
		name = url
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := Endpoint{ID: token(6), Name: name, URL: url, Secret: "whsec_" + token(20), Enabled: true, Created: time.Now().UTC()}
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
// opt out with SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS.
var allowPrivateWebhooks = os.Getenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS") != ""

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
				if allowPrivateWebhooks {
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

// Send POSTs the body to one endpoint with a signature header.
func Send(ep Endpoint, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "smolanalytics-webhooks")
	req.Header.Set("X-Smolanalytics-Signature", sign(ep.Secret, body))
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// DeliverAll fires the payload to every enabled endpoint, async + best-effort.
func (s *Store) DeliverAll(payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, ep := range s.List() {
		if !ep.Enabled {
			continue
		}
		go func(ep Endpoint) { _ = Send(ep, body) }(ep)
	}
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
