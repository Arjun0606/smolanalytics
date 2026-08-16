// Package payments turns a payment provider's webhook into an ordinary event in the log.
//
// This exists to feed one sentence. The investigator can size a finding in money
// (internal/investigate/money.go), but only when the metric carries a numeric revenue property —
// which means only for users who remembered to put an `amount` on their own `track("checkout")`
// call. Almost nobody does. Meanwhile the real revenue truth is already sitting in Stripe or
// Polar or Lemon Squeezy, complete and correct, behind a webhook the user can point at us in
// thirty seconds without asking anyone's permission.
//
// So: verify the signature, translate to event.Event{Name: "payment", Properties: {amount, ...}},
// hand it to the same Ingest every other event goes through. Nothing about the append-only log
// changes, no new storage, no new report — the existing machinery starts pricing findings because
// the data finally arrived.
//
// PERMISSIONLESS, which is the whole reason these four and not others. Each is a URL the user
// pastes into their own dashboard. Deliberately NOT the Stripe App or any marketplace listing:
// those are review-gated, and a channel that needs someone's approval is not a channel.
//
// AMOUNTS ARE MINOR UNITS EVERYWHERE. Every provider here sends integer cents; storing 2900
// instead of 29.00 would make every revenue figure a hundred times too large, which is both the
// easiest mistake to make and the most embarrassing one to ship, so the conversion happens once,
// here, and is tested per provider.
package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event is the name every payment lands under. One name, so a funnel or a finding about revenue
// means the same thing regardless of which processor produced it — a user who migrates from
// Lemon Squeezy to Stripe keeps one continuous history rather than two half-series.
const Event = "payment"

// Providers this package speaks, all self-serve.
const (
	Stripe       = "stripe"
	LemonSqueezy = "lemonsqueezy"
	Polar        = "polar"
	Dodo         = "dodo"
)

// Supported lists the providers in the order they appear in the docs.
var Supported = []string{Stripe, LemonSqueezy, Polar, Dodo}

// tolerance is how far a signed timestamp may be from now. Every scheme here signs a timestamp
// precisely so a captured request cannot be replayed later; without checking it, verifying the
// signature proves only that the body was genuine at SOME point.
const tolerance = 5 * time.Minute

// Payment is one settled payment, provider-agnostic.
type Payment struct {
	// ID is the provider's own event id, used verbatim as the event ID so the store's
	// already-seen rule deduplicates retries. Every provider on this list retries, several of
	// them aggressively, and without this a flaky minute would double someone's revenue.
	ID string
	// DistinctID ties the payment to a person. See resolveIdentity for how hard it tries.
	DistinctID string
	// IdentitySource records HOW the identity was resolved, and travels into the event as a
	// property. A revenue figure joined by email is worth less than one joined by an id the app
	// supplied, and a reader must be able to tell which they are looking at.
	IdentitySource string
	Amount         float64 // major units: dollars, not cents
	Currency       string
	Kind           string // the provider's event type, e.g. "checkout.session.completed"
	At             time.Time
}

// Verify checks the signature on a raw request body. `headers` is a lookup rather than
// http.Header so this package stays free of net/http and can be tested directly.
//
// THE BODY MUST BE THE RAW BYTES. Every scheme signs the exact octets sent; decoding to a map
// and re-encoding reorders keys and changes whitespace, and the signature then never matches —
// which usually gets "fixed" by disabling verification.
func Verify(provider, secret string, headers func(string) string, body []byte, now time.Time) error {
	if secret == "" {
		return fmt.Errorf("no signing secret configured for %s", provider)
	}
	switch provider {
	case Stripe:
		return verifyStripe(secret, headers("Stripe-Signature"), body, now)
	case LemonSqueezy:
		// Plain hex HMAC of the body, no timestamp in the scheme at all. Noted rather than
		// silently ignored: Lemon Squeezy deliveries cannot be replay-checked here, so the
		// store's dedupe on event id is the only replay defence for this one.
		return verifyHexHMAC("X-Signature", secret, headers("X-Signature"), body)
	case Polar, Dodo:
		return verifyStandardWebhooks(secret, headers, body, now)
	}
	return fmt.Errorf("unknown provider %q — supported: %s", provider, strings.Join(Supported, ", "))
}

// Parse turns a verified body into a Payment, or reports why it cannot.
//
// Returns ok=false WITHOUT an error for events that are legitimately not payments — every
// provider sends subscription updates, customer changes and test pings down the same webhook,
// and treating those as failures would fill a user's logs with errors about working correctly.
func Parse(provider string, body []byte) (p Payment, ok bool, err error) {
	switch provider {
	case Stripe:
		return parseStripe(body)
	case LemonSqueezy:
		return parseLemonSqueezy(body)
	case Polar:
		return parsePolar(body)
	case Dodo:
		return parseDodo(body)
	}
	return Payment{}, false, fmt.Errorf("unknown provider %q", provider)
}

/* ── signature schemes ─────────────────────────────────────────────────────────────────────── */

// verifyStripe: `Stripe-Signature: t=<unix>,v1=<hex>[,v1=<hex>]`, HMAC-SHA256 over
// "<t>.<body>". Multiple v1 values appear during a secret rotation and ANY may match.
func verifyStripe(secret, header string, body []byte, now time.Time) error {
	if header == "" {
		return fmt.Errorf("missing Stripe-Signature header")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return fmt.Errorf("malformed Stripe-Signature header")
	}
	if err := checkTimestamp(ts, now); err != nil {
		return err
	}
	mac := hmacHex(secret, []byte(ts+"."+string(body)))
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(mac)) {
			return nil
		}
	}
	return fmt.Errorf("stripe signature mismatch")
}

// verifyStandardWebhooks is the Standard Webhooks spec, which Polar and Dodo both implement:
// headers webhook-id / webhook-timestamp / webhook-signature, the last being a space-separated
// list of "v1,<base64>" and HMAC-SHA256 over "<id>.<timestamp>.<body>".
//
// The secret is base64 after an optional "whsec_" prefix. Getting that wrong is the classic
// failure with this spec: HMAC over the ASCII of the secret verifies nothing and fails 100% of
// the time, which at least is loud.
func verifyStandardWebhooks(secret string, headers func(string) string, body []byte, now time.Time) error {
	id, ts, sigHeader := headers("webhook-id"), headers("webhook-timestamp"), headers("webhook-signature")
	if id == "" || ts == "" || sigHeader == "" {
		return fmt.Errorf("missing webhook-id / webhook-timestamp / webhook-signature headers")
	}
	if err := checkTimestamp(ts, now); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return fmt.Errorf("signing secret is not valid base64 (Standard Webhooks secrets are base64 after the whsec_ prefix)")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "." + string(body)))
	want := base64.StdEncoding.EncodeToString(m.Sum(nil))
	for _, part := range strings.Fields(sigHeader) {
		_, v, found := strings.Cut(part, ",")
		if found && hmac.Equal([]byte(v), []byte(want)) {
			return nil
		}
	}
	return fmt.Errorf("webhook signature mismatch")
}

func verifyHexHMAC(name, secret, got string, body []byte) error {
	if got == "" {
		return fmt.Errorf("missing %s header", name)
	}
	if !hmac.Equal([]byte(strings.ToLower(got)), []byte(hmacHex(secret, body))) {
		return fmt.Errorf("%s mismatch", name)
	}
	return nil
}

func hmacHex(secret string, msg []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

func checkTimestamp(ts string, now time.Time) error {
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("unparseable signed timestamp %q", ts)
	}
	if d := now.Sub(time.Unix(n, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("signed timestamp is %s away from now — outside the %s replay window", d.Round(time.Second), tolerance)
	}
	return nil
}

/* ── per-provider payloads ─────────────────────────────────────────────────────────────────── */

// minor converts integer minor units to major. Every provider here sends cents.
func minor(v float64) float64 { return v / 100 }

// resolveIdentity picks the best available handle for the person who paid, and says which it was.
//
// A payment payload carries a BILLING identity, never the analytics distinct_id, so this is the
// join that decides whether revenue is usable at all. In order: an explicit distinct_id the app
// put in metadata (exact, and what the docs tell you to do), then the provider's customer id,
// then the email. Email is last because it is the weakest — a person who pays with a different
// address than they signed up with produces a second identity — but it still beats dropping the
// payment, since internal/alias can merge it later once the app calls identify().
func resolveIdentity(meta map[string]any, customerID, email string) (string, string) {
	for _, k := range []string{"distinct_id", "distinctId", "user_id", "userId", "smolanalytics_distinct_id"} {
		if v, okv := meta[k].(string); okv && v != "" {
			return v, "metadata." + k
		}
	}
	if customerID != "" {
		return customerID, "provider_customer_id"
	}
	if email != "" {
		return email, "email"
	}
	return "", ""
}

func parseStripe(body []byte) (Payment, bool, error) {
	var w struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Create int64  `json:"created"`
		Data   struct {
			Object struct {
				ID                string         `json:"id"`
				AmountTotal       *float64       `json:"amount_total"`
				AmountPaid        *float64       `json:"amount_paid"`
				Currency          string         `json:"currency"`
				ClientReferenceID string         `json:"client_reference_id"`
				Customer          string         `json:"customer"`
				CustomerEmail     string         `json:"customer_email"`
				Metadata          map[string]any `json:"metadata"`
				CustomerDetails   struct {
					Email string `json:"email"`
				} `json:"customer_details"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Payment{}, false, fmt.Errorf("invalid Stripe payload: %w", err)
	}
	// Only the events that mean money actually moved. checkout.session.completed can fire for a
	// zero-amount or unpaid session, which the amount check below handles.
	switch w.Type {
	case "checkout.session.completed", "invoice.paid", "invoice.payment_succeeded", "payment_intent.succeeded":
	default:
		return Payment{}, false, nil
	}
	o := w.Data.Object
	amt := o.AmountTotal
	if amt == nil {
		amt = o.AmountPaid
	}
	if amt == nil || *amt <= 0 {
		return Payment{}, false, nil // a real event, no money in it
	}
	email := o.CustomerEmail
	if email == "" {
		email = o.CustomerDetails.Email
	}
	meta := o.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	// client_reference_id is Stripe Checkout's purpose-built slot for exactly this, so it counts
	// as an explicit identity rather than a fallback.
	if o.ClientReferenceID != "" {
		meta["distinct_id"] = o.ClientReferenceID
	}
	id, src := resolveIdentity(meta, o.Customer, email)
	return Payment{
		ID: w.ID, DistinctID: id, IdentitySource: src,
		Amount: minor(*amt), Currency: strings.ToUpper(o.Currency), Kind: w.Type,
		At: time.Unix(w.Create, 0).UTC(),
	}, id != "", nil
}

func parseLemonSqueezy(body []byte) (Payment, bool, error) {
	var w struct {
		Meta struct {
			EventName  string         `json:"event_name"`
			CustomData map[string]any `json:"custom_data"`
		} `json:"meta"`
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Total       *float64 `json:"total"`
				Currency    string   `json:"currency"`
				UserEmail   string   `json:"user_email"`
				CustomerID  *float64 `json:"customer_id"`
				CreatedAt   string   `json:"created_at"`
				StatusLabel string   `json:"status"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Payment{}, false, fmt.Errorf("invalid Lemon Squeezy payload: %w", err)
	}
	if w.Meta.EventName != "order_created" && w.Meta.EventName != "subscription_payment_success" {
		return Payment{}, false, nil
	}
	a := w.Data.Attributes
	if a.Total == nil || *a.Total <= 0 {
		return Payment{}, false, nil
	}
	cust := ""
	if a.CustomerID != nil {
		cust = strconv.FormatFloat(*a.CustomerID, 'f', -1, 64)
	}
	id, src := resolveIdentity(w.Meta.CustomData, cust, a.UserEmail)
	at, _ := time.Parse(time.RFC3339, a.CreatedAt)
	return Payment{
		ID: w.Meta.EventName + ":" + w.Data.ID, DistinctID: id, IdentitySource: src,
		Amount: minor(*a.Total), Currency: strings.ToUpper(a.Currency), Kind: w.Meta.EventName,
		At: at.UTC(),
	}, id != "", nil
}

func parsePolar(body []byte) (Payment, bool, error) {
	var w struct {
		Type string `json:"type"`
		Data struct {
			ID        string         `json:"id"`
			Amount    *float64       `json:"amount"`
			NetAmount *float64       `json:"net_amount"`
			Currency  string         `json:"currency"`
			CreatedAt string         `json:"created_at"`
			Metadata  map[string]any `json:"metadata"`
			Customer  struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"customer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Payment{}, false, fmt.Errorf("invalid Polar payload: %w", err)
	}
	if w.Type != "order.created" && w.Type != "order.paid" {
		return Payment{}, false, nil
	}
	// net_amount is what actually lands, so prefer it: reporting gross as revenue overstates
	// every figure by the processor's cut, and this product's whole claim is not doing that.
	amt := w.Data.NetAmount
	if amt == nil {
		amt = w.Data.Amount
	}
	if amt == nil || *amt <= 0 {
		return Payment{}, false, nil
	}
	id, src := resolveIdentity(w.Data.Metadata, w.Data.Customer.ID, w.Data.Customer.Email)
	at, _ := time.Parse(time.RFC3339, w.Data.CreatedAt)
	return Payment{
		ID: "polar:" + w.Data.ID, DistinctID: id, IdentitySource: src,
		Amount: minor(*amt), Currency: strings.ToUpper(w.Data.Currency), Kind: w.Type,
		At: at.UTC(),
	}, id != "", nil
}

func parseDodo(body []byte) (Payment, bool, error) {
	var w struct {
		Type string `json:"type"`
		Data struct {
			PaymentID        string         `json:"payment_id"`
			TotalAmount      *float64       `json:"total_amount"`
			SettlementAmount *float64       `json:"settlement_amount"`
			Currency         string         `json:"currency"`
			CreatedAt        string         `json:"created_at"`
			Metadata         map[string]any `json:"metadata"`
			Customer         struct {
				CustomerID string `json:"customer_id"`
				Email      string `json:"email"`
			} `json:"customer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return Payment{}, false, fmt.Errorf("invalid Dodo payload: %w", err)
	}
	if w.Type != "payment.succeeded" {
		return Payment{}, false, nil
	}
	amt := w.Data.SettlementAmount
	if amt == nil {
		amt = w.Data.TotalAmount
	}
	if amt == nil || *amt <= 0 {
		return Payment{}, false, nil
	}
	id, src := resolveIdentity(w.Data.Metadata, w.Data.Customer.CustomerID, w.Data.Customer.Email)
	at, _ := time.Parse(time.RFC3339, w.Data.CreatedAt)
	return Payment{
		ID: "dodo:" + w.Data.PaymentID, DistinctID: id, IdentitySource: src,
		Amount: minor(*amt), Currency: strings.ToUpper(w.Data.Currency), Kind: w.Type,
		At: at.UTC(),
	}, id != "", nil
}
