package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/payments"
)

// POST /v1/revenue/{provider} — a payment provider's webhook, landing as an ordinary event.
//
// Authenticated by the provider's SIGNATURE, not by our write key, because the user pastes this
// URL into Stripe's or Polar's dashboard and those send what they send. That makes the signing
// secret load-bearing: without one configured this endpoint refuses every request rather than
// accepting unsigned revenue, which anyone on the internet could then invent.
//
// Secrets come from the environment, one per provider:
//
//	SMOLANALYTICS_STRIPE_SECRET, SMOLANALYTICS_LEMONSQUEEZY_SECRET,
//	SMOLANALYTICS_POLAR_SECRET, SMOLANALYTICS_DODO_SECRET
//
// Env rather than the settings store on purpose: a webhook secret is a deployment credential
// like the write key, and putting it in a JSON file next to the event log would place it inside
// every backup and export.
func (s *Server) revenueWebhook(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))

	// UNKNOWN PROVIDER IS 404, NOT 501. Without this, POSTing to /v1/revenue/paypal answered
	// "set SMOLANALYTICS_PAYPAL_SECRET and restart" — instructions for work that could never pay
	// off, since no env var enables a provider this build cannot parse. Sending someone to edit
	// their deployment config for nothing is the same defect as the deploy line that said
	// "record deploys and this becomes which ship did it" while attribution was broken.
	if !supported(provider) {
		writeErr(w, http.StatusNotFound, fmt.Sprintf(
			"no revenue receiver for %q — supported: %s. Ask for another and it can be added; "+
				"there is no environment variable that enables one.",
			provider, strings.Join(payments.Supported, ", ")))
		return
	}

	secret := providerSecret(provider)
	if secret == "" {
		// 501, not 401: nothing is wrong with the request. The operator has not turned this on,
		// and saying which variable to set is the difference between a two-minute fix and an
		// afternoon. Provider dashboards surface the response body on failed deliveries.
		writeErr(w, http.StatusNotImplemented, fmt.Sprintf(
			"revenue ingest for %q is not configured on this instance — set %s and restart",
			provider, secretEnvName(provider)))
		return
	}

	// RAW BYTES, read before anything decodes them. Every scheme signs the exact octets sent, so
	// decoding and re-encoding would reorder keys and invalidate a perfectly good signature.
	body, tooLarge, err := readLimited(r, 1<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read error")
		return
	}
	if tooLarge {
		writeErr(w, http.StatusRequestEntityTooLarge, "payload too large — max 1MB")
		return
	}

	now := time.Now().UTC()
	if err := payments.Verify(provider, secret, r.Header.Get, body, now); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}

	p, ok, err := payments.Parse(provider, body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		// A real, correctly-signed event that is not a payment we can attribute — a subscription
		// update, a test ping, or a payment with no identity to join it to. 200 with a reason:
		// a non-2xx here makes the provider retry forever and eventually disable the endpoint,
		// so refusing to store something must not read as a delivery failure.
		writeJSON(w, http.StatusOK, map[string]any{
			"stored": false,
			"reason": ignoredReason(p),
		})
		return
	}

	at := p.At
	if at.IsZero() || at.After(now) {
		at = now
	}
	e := event.Event{
		// The provider's own event id, so the store's already-seen rule deduplicates retries.
		// Every provider here retries, several aggressively, and without this one flaky minute
		// doubles a customer's revenue — in the report they are most likely to check.
		ID:         provider + ":" + p.ID,
		Name:       payments.Event,
		DistinctID: p.DistinctID,
		Timestamp:  at,
		Properties: map[string]any{
			"amount":          p.Amount,
			"currency":        p.Currency,
			"provider":        provider,
			"kind":            p.Kind,
			"identity_source": p.IdentitySource,
		},
	}
	if err := s.store.Ingest(e); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("revenue.received", provider)
	writeJSON(w, http.StatusOK, map[string]any{
		"stored": true, "event": payments.Event, "amount": p.Amount,
		"currency": p.Currency, "identity_source": p.IdentitySource,
	})
}

// ignoredReason explains a correctly-signed event we chose not to store. Precise on purpose:
// "no identity" and "not a payment event" send an operator to completely different places, and
// the second is normal while the first means their integration is half-done.
func ignoredReason(p payments.Payment) string {
	if p.Amount > 0 && p.DistinctID == "" {
		return "a payment with no identity to attribute it to — pass your analytics distinct_id " +
			"as the checkout reference (Stripe: client_reference_id; others: metadata.distinct_id) " +
			"so revenue joins the person who paid"
	}
	return "not a completed payment — ignored deliberately, nothing is wrong"
}

func secretEnvName(provider string) string {
	return "SMOLANALYTICS_" + strings.ToUpper(provider) + "_SECRET"
}

func supported(provider string) bool {
	for _, p := range payments.Supported {
		if p == provider {
			return true
		}
	}
	return false
}

func providerSecret(provider string) string {
	if !supported(provider) {
		return "" // no env var can enable a provider this build cannot parse
	}
	return os.Getenv(secretEnvName(provider))
}
