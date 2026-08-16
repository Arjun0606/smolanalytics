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

// Chat formats. Each is a receiver that will NOT accept our signed-JSON contract: it wants its
// own tiny envelope and rejects anything else, so an endpoint on one of these hosts gets the
// human rendering and no signature (there is nowhere to put one).
const (
	// FormatSlack — {"text": "<plain-text rendering>"}. Also matches Mattermost and Rocket.Chat,
	// both of which implement Slack's incoming-webhook contract deliberately.
	FormatSlack = "slack"
	// FormatDiscord — {"content": ...}. Discord 400s on {"text": ...}, so a Discord URL pasted
	// into the webhook box was rejected on every single delivery, forever, silently: the send is
	// fire-and-forget, so the failure went nowhere. Discord is where this ICP actually is, which
	// made it the most expensive missing line in the file.
	FormatDiscord = "discord"
)

// discordLimit is Discord's hard cap on `content`. Over it the whole message is rejected, so a
// long brief has to be truncated rather than dropped — a clipped alert is worth vastly more than
// a 400 nobody sees.
const discordLimit = 2000

// Endpoint is one registered webhook target.
type Endpoint struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	URL     string    `json:"url"`
	Secret  string    `json:"secret"` // signs the payload so the receiver can verify
	Format  string    `json:"format,omitempty"`
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`

	// DELIVERY HEALTH. Every outbound delivery was `go func(ep){ _, _ = Send(...) }` — the status
	// and the error both discarded, on a goroutine nobody waited for. A webhook that started
	// 500ing, or whose URL was revoked, simply stopped working and said nothing anywhere: no log
	// line, no dashboard state, no field to inspect. That is how a Discord endpoint could reject
	// every single delivery for as long as the feature existed without one person noticing.
	//
	// "silence = bug" is a rule this product states out loud, and this was the largest violation
	// of it in the codebase.
	LastStatus    int       `json:"last_status,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastAttempt   time.Time `json:"last_attempt,omitempty"`
	LastDelivered time.Time `json:"last_delivered,omitempty"`
	// Failures counts CONSECUTIVE failures; any success resets it to zero. A webhook that fails
	// once a week is a flaky receiver, not a dead one, and auto-disabling on a cumulative count
	// would eventually switch off every endpoint that has ever hiccuped.
	Failures int `json:"consecutive_failures,omitempty"`
	// DisabledAt is set when consecutive failures crossed the limit and delivery was stopped.
	// Recorded rather than just flipping Enabled, so the row can say WHY it is off — an endpoint
	// that silently turned itself off is a second invisible failure on top of the first.
	DisabledAt time.Time `json:"disabled_at,omitempty"`
}

// Healthy reports whether the last attempt succeeded. A brand-new endpoint with no attempt yet
// is healthy: never-tried and known-broken must not render the same.
func (e Endpoint) Healthy() bool { return e.Failures == 0 }

// Health renders the delivery state in words, for the settings row and list_webhooks. One
// renderer, for the same reason Cost has one.
func (e Endpoint) Health() string {
	switch {
	case !e.DisabledAt.IsZero():
		return fmt.Sprintf("auto-disabled after %d consecutive failures — %s", e.Failures, e.LastError)
	case e.LastAttempt.IsZero():
		return "no deliveries yet"
	case e.Failures > 0:
		return fmt.Sprintf("failing (%d in a row) — %s", e.Failures, e.LastError)
	default:
		return "delivered " + e.LastDelivered.UTC().Format("2006-01-02 15:04") + " UTC"
	}
}

// SlackFormat reports whether deliveries to e use Slack's {"text": ...} contract:
// either the endpoint was created with format "slack", or the URL is a Slack
// incoming webhook (hooks.slack.com) — the host check also covers endpoints
// persisted before the format field existed.
func (e Endpoint) SlackFormat() bool {
	return e.Format == FormatSlack || isSlackURL(e.URL)
}

// DiscordFormat reports whether deliveries use Discord's {"content": ...} contract, either
// because the endpoint says so or because the URL is plainly a Discord webhook — the host check
// also covers endpoints persisted before this format existed, which is every one of them.
func (e Endpoint) DiscordFormat() bool {
	return e.Format == FormatDiscord || isDiscordURL(e.URL)
}

// Chat reports whether this endpoint takes a chat envelope rather than signed JSON.
func (e Endpoint) Chat() bool { return e.SlackFormat() || e.DiscordFormat() }

func isSlackURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	// Mattermost and Rocket.Chat implement Slack's contract on the operator's own domain, so
	// they cannot be host-detected; those users pick the format explicitly.
	return h == "hooks.slack.com"
}

func isDiscordURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	// discord.com is current; discordapp.com is the legacy host and still live, and ptb/canary
	// are the public beta rings — a user on any of them pasted a real webhook.
	return (h == "discord.com" || h == "discordapp.com" ||
		h == "ptb.discord.com" || h == "canary.discord.com") &&
		strings.Contains(u.Path, "/api/webhooks/")
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

// Delete removes an endpoint by id. found is true only when an endpoint actually
// went away, so callers never claim a removal that did not occur. A miss is not
// an error.
func (s *Store) Delete(id string) (found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items
	out := make([]Endpoint, 0, len(old))
	for _, e := range old {
		if e.ID != id {
			out = append(out, e)
		} else {
			found = true
		}
	}
	if !found {
		return false, nil
	}
	s.items = out
	if err := s.persist(); err != nil {
		s.items = old
		return false, err
	}
	return true, nil
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
	chat := ep.Chat()
	if chat {
		if text == "" {
			text = string(body)
		}
		if ep.DiscordFormat() {
			if len(text) > discordLimit {
				text = text[:discordLimit-1] + "\u2026"
			}
			body, _ = json.Marshal(map[string]string{"content": text})
		} else {
			body, _ = json.Marshal(map[string]string{"text": text})
		}
	}
	req, err := http.NewRequest(http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "smolanalytics-webhooks")
	if !chat {
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
		go func(ep Endpoint) { s.deliver(ep, body, text) }(ep)
	}
}

// maxAttempts and maxFailures are deliberately small. Retrying is for the transient case — a
// receiver restarting, a momentary 502 — and three tries over ~7 seconds covers that. Anything
// still failing after four consecutive DELIVERIES is broken rather than busy, and continuing to
// POST at it forever is how you end up rate-limited or blocked by the receiver.
const (
	maxAttempts = 3
	maxFailures = 4
)

// backoff is the pause before retry N. A var rather than a literal so tests can shrink it:
// with real timings this package's delivery tests took 36 seconds, and a slow suite is a suite
// people start skipping — which would leave exactly this code, the code whose entire job is to
// notice silent failure, as the least-exercised in the tree.
var backoff = func(attempt int) time.Duration { return time.Duration(1<<attempt) * time.Second }

// deliver sends with backoff and records what happened.
//
// 4xx is NOT retried, with one exception. A 400 or a 404 means the receiver understood us and
// said no — a revoked Slack URL, a wrong body shape — and hammering it changes nothing. 429 is
// the exception because it explicitly means "later".
func (s *Store) deliver(ep Endpoint, body []byte, text string) {
	var status int
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff(attempt)) // 2s, 4s in production
		}
		status, err = Send(ep, body, text)
		if err == nil {
			break
		}
		if status >= 400 && status < 500 && status != 429 {
			break // a definite no; retrying is noise
		}
	}
	s.recordDelivery(ep.ID, status, err)
}

// recordDelivery persists the outcome and auto-disables an endpoint that keeps failing.
func (s *Store) recordDelivery(id string, status int, sendErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		e := &s.items[i]
		now := time.Now().UTC()
		e.LastAttempt, e.LastStatus = now, status
		if sendErr == nil {
			e.LastDelivered, e.Failures, e.LastError = now, 0, ""
		} else {
			e.Failures++
			e.LastError = sendErr.Error()
			if e.Failures >= maxFailures && e.Enabled {
				// Turn it off rather than POSTing forever. Recorded with a reason, because an
				// endpoint that silently switched itself off is a second invisible failure
				// stacked on the first.
				e.Enabled, e.DisabledAt = false, now
			}
		}
		_ = s.persist()
		return
	}
}

// SetEnabled turns an endpoint on or off, clearing the failure state when it is re-enabled.
//
// The field existed from the beginning and no method ever wrote it, so `enabled` was reported to
// agents as if false were reachable while the only way to stop deliveries was to DELETE the
// endpoint — which destroys the signing secret and forces every receiver to be reconfigured. Now
// pausing is a pause.
func (s *Store) SetEnabled(id string, on bool) (Endpoint, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		e := &s.items[i]
		e.Enabled = on
		if on {
			// Re-enabling is a statement that the receiver is fixed. Keeping the old failure
			// count would auto-disable it again after one more blip.
			e.Failures, e.DisabledAt, e.LastError = 0, time.Time{}, ""
		}
		err := s.persist()
		return *e, true, err
	}
	return Endpoint{}, false, nil
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
