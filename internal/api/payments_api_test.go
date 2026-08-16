package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// End-to-end through the real handler and the real store. The package tests cover parsing and
// signatures in isolation; this covers the thing a user actually points Stripe at.

func stripePost(t *testing.T, srv *httptest.Server, secret, body string, at time.Time) (int, map[string]any) {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + body))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/revenue/stripe", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Stripe-Signature", "t="+ts+",v1="+hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

const paidBody = `{"id":"evt_pay_1","type":"checkout.session.completed","created":%d,` +
	`"data":{"object":{"amount_total":2900,"currency":"usd","client_reference_id":"u1"}}}`

// A RETRY MUST NOT DOUBLE THE REVENUE.
//
// Every provider on this list retries, several of them aggressively, so a single slow minute
// during a deploy produces the same payment two or three times. Without deduplication that
// lands as real money in the log, and the first place it shows up is the revenue figure the
// customer is most likely to check.
func TestARetriedWebhookIsStoredOnce(t *testing.T) {
	t.Setenv("SMOLANALYTICS_STRIPE_SECRET", "whsec_test")
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()

	now := time.Now()
	body := strings.Replace(paidBody, "%d", strconv.FormatInt(now.Unix(), 10), 1)

	for i := 0; i < 3; i++ {
		code, out := stripePost(t, srv, "whsec_test", body, now)
		if code != http.StatusOK {
			t.Fatalf("delivery %d: status %d (%v)", i+1, code, out)
		}
	}

	code, rows := getJSON(t, srv, "/v1/rows?event=payment&days=30")
	if code != http.StatusOK {
		t.Fatalf("rows: %d", code)
	}
	// The store's already-seen rule keys on the event ID, which we set to the provider's own
	// event id precisely so this holds.
	if n, _ := rows["total"].(float64); n != 1 {
		t.Errorf("three identical deliveries produced %v payment events, want 1 — a retry storm "+
			"would multiply a customer's revenue", rows["total"])
	}
}

// The amount must arrive in DOLLARS and be attributed to the person named in the checkout.
func TestAPaymentLandsAsAJoinableEventInMajorUnits(t *testing.T) {
	t.Setenv("SMOLANALYTICS_STRIPE_SECRET", "whsec_test")
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()

	now := time.Now()
	code, out := stripePost(t, srv, "whsec_test",
		strings.Replace(paidBody, "%d", strconv.FormatInt(now.Unix(), 10), 1), now)
	if code != http.StatusOK || out["stored"] != true {
		t.Fatalf("not stored: %d %v", code, out)
	}
	if amt, _ := out["amount"].(float64); amt != 29 {
		t.Errorf("amount = %v, want 29 (dollars, not cents)", out["amount"])
	}

	// /v1/events/recent returns a bare JSON array, not an object, so it cannot go through the
	// map-shaped getJSON helper.
	resp, err := http.Get(srv.URL + "/v1/events/recent?limit=5")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("recent events did not decode as an array: %v (%s)", err, raw)
	}
	if len(list) == 0 {
		t.Fatal("no events recorded")
	}
	first := list[0]
	if first["name"] != "payment" {
		t.Errorf("event name = %v, want payment", first["name"])
	}
	if first["distinct_id"] != "u1" {
		t.Errorf("distinct_id = %v, want u1 — revenue that joins to nobody cannot size a finding",
			first["distinct_id"])
	}
	props, _ := first["properties"].(map[string]any)
	if props["amount"] != 29.0 {
		t.Errorf("amount property = %v, want 29 — this is the value internal/investigate sums", props["amount"])
	}
	if props["identity_source"] == nil || props["identity_source"] == "" {
		t.Error("no identity_source recorded; a figure joined by email is worth less than one " +
			"joined by an id, and nothing downstream could tell them apart")
	}
}

// A BAD SIGNATURE IS A 401 AND NOTHING IS STORED. This endpoint is authenticated by the
// signature alone — it has to be, because the provider sends what it sends — so if verification
// can be bypassed, anyone on the internet can invent revenue into someone's dashboard.
func TestAnUnsignedOrWronglySignedDeliveryStoresNothing(t *testing.T) {
	t.Setenv("SMOLANALYTICS_STRIPE_SECRET", "whsec_test")
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()

	now := time.Now()
	body := strings.Replace(paidBody, "%d", strconv.FormatInt(now.Unix(), 10), 1)

	// signed with the wrong secret
	if code, _ := stripePost(t, srv, "not-the-secret", body, now); code != http.StatusUnauthorized {
		t.Errorf("wrong secret got %d, want 401", code)
	}
	// no signature header at all
	resp, err := http.Post(srv.URL+"/v1/revenue/stripe", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned delivery got %d, want 401", resp.StatusCode)
	}

	_, rows := getJSON(t, srv, "/v1/rows?event=payment&days=30")
	if n, _ := rows["total"].(float64); n != 0 {
		t.Errorf("%v payment events stored from rejected deliveries", rows["total"])
	}
}

// UNCONFIGURED IS 501 WITH THE VARIABLE NAME IN IT, not a 401. Nothing is wrong with the
// request; the operator has not switched this on. Provider dashboards show the response body on
// a failed delivery, so this message is the whole difference between a two-minute fix and an
// afternoon of guessing.
func TestAnUnconfiguredProviderSaysWhichVariableToSet(t *testing.T) {
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/revenue/polar", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status %d, want 501", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "SMOLANALYTICS_POLAR_SECRET") {
		t.Errorf("the error does not name the variable to set: %s", raw)
	}
}

// A signed non-payment event is a 200, not an error. A non-2xx makes the provider retry forever
// and eventually disable the endpoint, so "we deliberately did not store this" must never read
// as a delivery failure.
func TestASignedNonPaymentEventIsAcceptedAndNotStored(t *testing.T) {
	t.Setenv("SMOLANALYTICS_STRIPE_SECRET", "whsec_test")
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()

	now := time.Now()
	code, out := stripePost(t, srv, "whsec_test",
		`{"id":"evt_2","type":"customer.subscription.updated","created":`+
			strconv.FormatInt(now.Unix(), 10)+`,"data":{"object":{}}}`, now)
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200 — a non-2xx here gets the endpoint disabled by the provider", code)
	}
	if out["stored"] != false {
		t.Errorf("claimed to store a subscription update: %v", out)
	}
	if r, _ := out["reason"].(string); r == "" {
		t.Error("no reason given, so an operator cannot tell a deliberate skip from a silent bug")
	}
}

// AN UNKNOWN PROVIDER MUST NOT BE HANDED A VARIABLE NAME.
//
// The first version of this endpoint answered /v1/revenue/paypal with "set
// SMOLANALYTICS_PAYPAL_SECRET and restart". No env var enables a provider the build cannot
// parse, so that sent an operator to edit their deployment config for nothing — the same defect
// as the cause line that read "record deploys and this becomes which ship did it" while ship
// attribution was broken. Found by curling the deployed instance, not by reading the code.
func TestAnUnknownProviderIsNotToldToSetAVariable(t *testing.T) {
	srv := httptest.NewServer(New(memory.New()).Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/revenue/paypal", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404 — 501 reads as \"not switched on yet\", which is false", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "SMOLANALYTICS_PAYPAL_SECRET") {
		t.Errorf("told the operator to set a variable that does nothing: %s", raw)
	}
	if !strings.Contains(string(raw), "stripe") {
		t.Errorf("does not say which providers ARE supported: %s", raw)
	}
}
