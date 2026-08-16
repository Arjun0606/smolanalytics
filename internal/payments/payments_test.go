package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func hdr(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func hexSig(secret, msg string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

/* ── the money conversion, per provider ────────────────────────────────────────────────────── */

// CENTS ARE NOT DOLLARS. Every provider here sends integer minor units, so a missed division
// makes every revenue figure 100× too large. It is the easiest mistake to make in this package
// and the most embarrassing to ship — a finding reading "$1,450,000/mo" on a product doing
// $14.5k destroys the credibility of every other number on the page.
func TestAmountsAreConvertedFromMinorUnits(t *testing.T) {
	cases := []struct {
		provider string
		body     string
		want     float64
	}{
		{Stripe, `{"id":"evt_1","type":"checkout.session.completed","created":1,"data":{"object":{"amount_total":2900,"currency":"usd","client_reference_id":"u1"}}}`, 29},
		{LemonSqueezy, `{"meta":{"event_name":"order_created","custom_data":{"distinct_id":"u1"}},"data":{"id":"7","attributes":{"total":4900,"currency":"USD"}}}`, 49},
		{Polar, `{"type":"order.created","data":{"id":"o1","net_amount":1999,"currency":"usd","metadata":{"distinct_id":"u1"}}}`, 19.99},
		{Dodo, `{"type":"payment.succeeded","data":{"payment_id":"p1","settlement_amount":500,"currency":"usd","metadata":{"distinct_id":"u1"}}}`, 5},
	}
	for _, c := range cases {
		p, ok, err := Parse(c.provider, []byte(c.body))
		if err != nil || !ok {
			t.Fatalf("%s: parse failed ok=%v err=%v", c.provider, ok, err)
		}
		if p.Amount != c.want {
			t.Errorf("%s: amount = %v, want %v (minor units not converted?)", c.provider, p.Amount, c.want)
		}
	}
}

// NET, NOT GROSS, where the provider tells us both. Reporting gross as revenue overstates every
// figure by the processor's cut, and not doing that is this product's entire claim.
func TestNetAmountWinsOverGross(t *testing.T) {
	p, ok, _ := Parse(Polar, []byte(`{"type":"order.created","data":{"id":"o1","amount":2000,"net_amount":1830,"currency":"usd","metadata":{"distinct_id":"u1"}}}`))
	if !ok {
		t.Fatal("did not parse")
	}
	if p.Amount != 18.30 {
		t.Errorf("amount = %v, want 18.30 (net) not 20.00 (gross)", p.Amount)
	}
}

/* ── identity, the join that decides whether any of this is usable ─────────────────────────── */

// A payment carries a BILLING identity, never the analytics distinct_id. The ladder matters:
// an explicit id is exact, an email is a guess that a person paying from a second address will
// break. Whichever was used has to travel with the number so a reader can weigh it.
func TestIdentityLadderAndItsSourceIsRecorded(t *testing.T) {
	cases := []struct {
		name, body, wantID, wantSrc string
	}{
		{
			"explicit metadata beats everything",
			`{"id":"e","type":"invoice.paid","created":1,"data":{"object":{"amount_paid":100,"currency":"usd","metadata":{"distinct_id":"u_exact"},"customer":"cus_1","customer_email":"a@b.c"}}}`,
			"u_exact", "metadata.distinct_id",
		},
		{
			"stripe's client_reference_id is an explicit identity, not a fallback",
			`{"id":"e","type":"checkout.session.completed","created":1,"data":{"object":{"amount_total":100,"currency":"usd","client_reference_id":"u_ref","customer":"cus_1","customer_email":"a@b.c"}}}`,
			"u_ref", "metadata.distinct_id",
		},
		{
			"customer id beats email",
			`{"id":"e","type":"invoice.paid","created":1,"data":{"object":{"amount_paid":100,"currency":"usd","customer":"cus_1","customer_email":"a@b.c"}}}`,
			"cus_1", "provider_customer_id",
		},
		{
			"email is last but still better than dropping the payment",
			`{"id":"e","type":"invoice.paid","created":1,"data":{"object":{"amount_paid":100,"currency":"usd","customer_email":"a@b.c"}}}`,
			"a@b.c", "email",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok, err := Parse(Stripe, []byte(c.body))
			if err != nil || !ok {
				t.Fatalf("parse failed ok=%v err=%v", ok, err)
			}
			if p.DistinctID != c.wantID {
				t.Errorf("distinct_id = %q, want %q", p.DistinctID, c.wantID)
			}
			if p.IdentitySource != c.wantSrc {
				t.Errorf("identity_source = %q, want %q — a figure joined by email is worth less "+
					"than one joined by an id, and the reader must be able to tell", p.IdentitySource, c.wantSrc)
			}
		})
	}
}

// With nothing at all to join on, the payment must NOT be stored. Ingesting it under a synthetic
// id would add a person who appears in no funnel, inflating the denominator of every
// revenue-per-person figure and quietly making the mean wrong.
func TestAPaymentWithNoIdentityIsNotStorable(t *testing.T) {
	_, ok, err := Parse(Stripe, []byte(`{"id":"e","type":"invoice.paid","created":1,"data":{"object":{"amount_paid":5000,"currency":"usd"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("accepted a payment with no customer, no email and no metadata — it would join to nobody")
	}
}

/* ── what is deliberately ignored ──────────────────────────────────────────────────────────── */

// Every provider sends subscription updates, customer changes and test pings down the same
// webhook. Those are not failures, and treating them as errors would fill an operator's logs
// with alarms about the system working correctly.
func TestNonPaymentEventsAreIgnoredWithoutError(t *testing.T) {
	for _, c := range []struct{ provider, body string }{
		{Stripe, `{"id":"e","type":"customer.subscription.updated","created":1,"data":{"object":{}}}`},
		{LemonSqueezy, `{"meta":{"event_name":"subscription_updated"},"data":{"id":"1","attributes":{}}}`},
		{Polar, `{"type":"checkout.created","data":{"id":"c1"}}`},
		{Dodo, `{"type":"payment.failed","data":{"payment_id":"p1"}}`},
	} {
		_, ok, err := Parse(c.provider, []byte(c.body))
		if err != nil {
			t.Errorf("%s: a non-payment event produced an error: %v", c.provider, err)
		}
		if ok {
			t.Errorf("%s: stored a non-payment event as revenue", c.provider)
		}
	}
}

// A zero-amount completed checkout is real and common — a 100%-off coupon, a free trial
// converting. It must not become a $0 payment row dragging down average revenue per person.
func TestZeroAmountPaymentsAreNotStored(t *testing.T) {
	_, ok, _ := Parse(Stripe, []byte(`{"id":"e","type":"checkout.session.completed","created":1,"data":{"object":{"amount_total":0,"currency":"usd","client_reference_id":"u1"}}}`))
	if ok {
		t.Error("stored a zero-amount checkout as revenue")
	}
}

/* ── signatures ────────────────────────────────────────────────────────────────────────────── */

func TestStripeSignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"id":"evt_1"}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	good := hexSig("whsec_test", ts+"."+string(body))

	if err := Verify(Stripe, "whsec_test", hdr(map[string]string{
		"Stripe-Signature": "t=" + ts + ",v1=" + good,
	}), body, now); err != nil {
		t.Fatalf("a valid Stripe signature was rejected: %v", err)
	}

	// Rotation: Stripe sends several v1 values while two secrets are live, and ANY may match.
	if err := Verify(Stripe, "whsec_test", hdr(map[string]string{
		"Stripe-Signature": "t=" + ts + ",v1=" + hexSig("old", ts+"."+string(body)) + ",v1=" + good,
	}), body, now); err != nil {
		t.Errorf("rejected during a secret rotation, where multiple v1 values are expected: %v", err)
	}

	// A tampered body must fail — the whole point.
	if err := Verify(Stripe, "whsec_test", hdr(map[string]string{
		"Stripe-Signature": "t=" + ts + ",v1=" + good,
	}), []byte(`{"id":"evt_1","amount_total":999999}`), now); err == nil {
		t.Error("accepted a body that did not match its signature")
	}

	// REPLAY. Verifying the signature alone proves the body was genuine at some point, not that
	// it is genuine now; without the timestamp check a captured request could be resent forever.
	old := now.Add(-1 * time.Hour)
	oldTS := strconv.FormatInt(old.Unix(), 10)
	if err := Verify(Stripe, "whsec_test", hdr(map[string]string{
		"Stripe-Signature": "t=" + oldTS + ",v1=" + hexSig("whsec_test", oldTS+"."+string(body)),
	}), body, now); err == nil {
		t.Error("accepted a correctly-signed request from an hour ago — replayable")
	}
}

func TestStandardWebhooksSignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"type":"order.created"}`)
	// The secret is base64 AFTER the whsec_ prefix. HMAC-ing the ASCII of the secret instead is
	// the classic failure with this spec, and it fails 100% of the time, which is at least loud.
	raw := []byte("0123456789abcdef")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(raw)
	id, ts := "msg_1", strconv.FormatInt(now.Unix(), 10)

	m := hmac.New(sha256.New, raw)
	m.Write([]byte(id + "." + ts + "." + string(body)))
	sig := base64.StdEncoding.EncodeToString(m.Sum(nil))

	for _, provider := range []string{Polar, Dodo} {
		h := hdr(map[string]string{
			"webhook-id": id, "webhook-timestamp": ts, "webhook-signature": "v1," + sig,
		})
		if err := Verify(provider, secret, h, body, now); err != nil {
			t.Errorf("%s: valid Standard Webhooks signature rejected: %v", provider, err)
		}
		// Multiple space-separated signatures during rotation, ours second.
		h2 := hdr(map[string]string{
			"webhook-id": id, "webhook-timestamp": ts,
			"webhook-signature": "v1,AAAA v1," + sig,
		})
		if err := Verify(provider, secret, h2, body, now); err != nil {
			t.Errorf("%s: rejected when a second signature was present: %v", provider, err)
		}
		// Tampered body.
		if err := Verify(provider, secret, h, []byte(`{"type":"order.created","x":1}`), now); err == nil {
			t.Errorf("%s: accepted a tampered body", provider)
		}
	}
}

func TestLemonSqueezySignature(t *testing.T) {
	body := []byte(`{"meta":{"event_name":"order_created"}}`)
	now := time.Now()
	if err := Verify(LemonSqueezy, "s3cret", hdr(map[string]string{
		"X-Signature": hexSig("s3cret", string(body)),
	}), body, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := Verify(LemonSqueezy, "s3cret", hdr(map[string]string{"X-Signature": "deadbeef"}), body, now); err == nil {
		t.Error("accepted a wrong signature")
	}
	if err := Verify(LemonSqueezy, "s3cret", hdr(map[string]string{}), body, now); err == nil {
		t.Error("accepted a delivery with no signature header at all")
	}
}

// NO SECRET MEANS NO. An unconfigured instance must refuse rather than accept unsigned revenue,
// which anyone on the internet could otherwise invent into someone's dashboard.
func TestAnEmptySecretNeverVerifies(t *testing.T) {
	for _, p := range Supported {
		if err := Verify(p, "", hdr(map[string]string{}), []byte(`{}`), time.Now()); err == nil {
			t.Errorf("%s: verified with an empty signing secret", p)
		}
	}
}

func TestUnknownProviderIsRefused(t *testing.T) {
	if err := Verify("paypal", "x", hdr(map[string]string{}), []byte(`{}`), time.Now()); err == nil {
		t.Error("verified a provider this package does not implement")
	}
	if _, _, err := Parse("paypal", []byte(`{}`)); err == nil {
		t.Error("parsed a provider this package does not implement")
	}
}

// The event id must be stable and provider-scoped, because it becomes the store's dedupe key.
// Every provider here retries; without a stable id one flaky minute doubles a customer's
// revenue in the report they are most likely to check.
func TestTheEventIDIsStableForDedupe(t *testing.T) {
	body := []byte(`{"id":"evt_abc","type":"invoice.paid","created":1,"data":{"object":{"amount_paid":100,"currency":"usd","customer":"c1"}}}`)
	a, _, _ := Parse(Stripe, body)
	b, _, _ := Parse(Stripe, body)
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("unstable or empty event id: %q vs %q", a.ID, b.ID)
	}
	if a.ID != "evt_abc" {
		t.Errorf("event id = %q, want the provider's own id so retries deduplicate", a.ID)
	}
	if a.Kind != "invoice.paid" {
		t.Errorf("kind = %q; the provider event type must travel for auditability", a.Kind)
	}
}
