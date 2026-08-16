package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSlack mimics a Slack incoming webhook: it accepts ONLY bodies with a
// non-empty top-level "text" string (400 invalid_payload otherwise) — which is
// exactly why the signed-JSON contract never rendered in Slack channels.
func fakeSlack(got *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &p) != nil || p.Text == "" {
			http.Error(w, "invalid_payload", http.StatusBadRequest)
			return
		}
		*got = append(*got, p.Text)
		w.WriteHeader(http.StatusOK)
	}))
}

// The old signed-JSON body must FAIL against a Slack-shaped receiver, and the
// slack format must pass — delivering the human rendering, not the raw payload.
func TestSlackFormatDelivery(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest listens on loopback
	var got []string
	srv := fakeSlack(&got)
	defer srv.Close()

	alertBody := []byte(`{"type":"alert","alert":"signup drop","value":4}`)
	rendered := "⚠ signup drop — signup: 4 events in the last 24h, below threshold 10"

	// old contract (signed JSON, no format): Slack rejects it
	status, err := Send(Endpoint{URL: srv.URL, Secret: "s"}, alertBody, rendered)
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("signed-JSON body against Slack must 400, got status=%d err=%v", status, err)
	}
	// slack format: same payload, delivered as {"text": rendered}
	status, err = Send(Endpoint{URL: srv.URL, Secret: "s", Format: FormatSlack}, alertBody, rendered)
	if err != nil || status != http.StatusOK {
		t.Fatalf("slack-format delivery failed: status=%d err=%v", status, err)
	}
	if len(got) != 1 || got[0] != rendered {
		t.Fatalf("Slack should receive the rendered text, got %q", got)
	}

	// a slack delivery with no rendering falls back to the raw JSON as text —
	// the message still carries the facts instead of 400ing silently
	if status, err := Send(Endpoint{URL: srv.URL, Format: FormatSlack}, alertBody, ""); err != nil || status != http.StatusOK {
		t.Fatalf("empty-text slack delivery should fall back to the raw body: status=%d err=%v", status, err)
	}
	if got[1] != string(alertBody) {
		t.Fatalf("fallback text should be the raw payload, got %q", got[1])
	}

	// SendTest goes through the same real path: format rules included
	if status, err := SendTest(Endpoint{URL: srv.URL, Format: FormatSlack}); err != nil || status != http.StatusOK {
		t.Fatalf("SendTest against slack endpoint: status=%d err=%v", status, err)
	}
}

// Non-slack endpoints must keep the existing contract byte-for-byte: the JSON
// payload verbatim plus a verifiable X-Smolanalytics-Signature.
func TestNonSlackKeepsSignedJSONContract(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Smolanalytics-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := []byte(`{"type":"alert","alert":"x"}`)
	if _, err := Send(Endpoint{URL: srv.URL, Secret: "whsec_test"}, body, "rendered text that must NOT replace the body"); err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("non-slack body must be the signed JSON payload unchanged, got %s", gotBody)
	}
	if gotSig != sign("whsec_test", body) {
		t.Fatalf("signature must verify against the delivered body: got %q", gotSig)
	}
}

func TestAddFormatDetection(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		format    string
		wantErr   bool
		wantSlack bool
	}{
		{"slack host auto-detected", "https://hooks.slack.com/services/T0/B0/x", "", false, true},
		{"plain https stays json", "https://example.com/hook", "", false, false},
		{"explicit slack on another host", "https://chat.example.com/hooks/x", "slack", false, true},
		{"unknown format rejected", "https://example.com/hook", "xml", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Store{}
			ep, err := s.Add(c.name, c.url, c.format)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Add accepted format %q", c.format)
				}
				if !strings.Contains(err.Error(), `"slack"`) {
					t.Fatalf("error should name the valid format: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ep.SlackFormat() != c.wantSlack {
				t.Fatalf("SlackFormat() = %v, want %v (format stored %q)", ep.SlackFormat(), c.wantSlack, ep.Format)
			}
		})
	}

	// endpoints persisted before the format field existed still route by host
	legacy := Endpoint{URL: "https://hooks.slack.com/services/T0/B0/x"}
	if !legacy.SlackFormat() {
		t.Fatal("legacy slack endpoint (no format field) must still get slack-format deliveries")
	}
}

// SendTest must surface the endpoint's real HTTP status for both outcomes.
func TestSendTestReportsEndpointStatus(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	if status, err := SendTest(Endpoint{URL: ok.URL, Secret: "s"}); err != nil || status != http.StatusOK {
		t.Fatalf("healthy endpoint: status=%d err=%v", status, err)
	}

	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no_service", http.StatusNotFound) // what Slack returns for a revoked URL
	}))
	defer gone.Close()
	status, err := SendTest(Endpoint{URL: gone.URL, Format: FormatSlack})
	if err == nil || status != http.StatusNotFound {
		t.Fatalf("revoked endpoint must report its 404: status=%d err=%v", status, err)
	}
}

// fakeDiscord mimics a Discord incoming webhook: it accepts ONLY bodies with a non-empty
// "content" string, and 400s on anything else — which is exactly what Discord does, and exactly
// why every delivery to a Discord URL failed silently before this format existed.
func fakeDiscord(got *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		if json.Unmarshal(b, &m) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c, ok := m["content"].(string)
		if !ok || c == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		*got = append(*got, c)
		w.WriteHeader(http.StatusNoContent)
	}))
}

// A DISCORD URL WAS REJECTED ON EVERY DELIVERY, FOREVER, IN SILENCE.
//
// Send is fire-and-forget (`go func(ep){ _, _ = Send(...) }`), so the 400 went nowhere: no log,
// no dashboard state, nothing. A user pasted their Discord webhook, saw it appear in the list,
// pressed nothing else, and simply never got an alert. Discord is where this ICP lives, which
// made it the most expensive missing branch in the file.
func TestADiscordURLGetsDiscordsEnvelopeNotSlacks(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest listens on loopback
	var got []string
	srv := fakeDiscord(&got)
	defer srv.Close()

	// Auto-detected from the host — no format chosen, exactly as a user would add it.
	ep := Endpoint{ID: "d1", Name: "team", URL: srv.URL + "/api/webhooks/123/abc", Secret: "whsec_x", Enabled: true}
	// The real URL is on discord.com; the test server is on 127.0.0.1, so force the format the
	// way an explicit selection would, and assert the host detection separately below.
	ep.Format = FormatDiscord

	status, err := Send(ep, []byte(`{"alert":"signup fell 40%"}`), "signup fell 40%")
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if status >= 300 {
		t.Fatalf("Discord rejected the delivery with %d — the envelope is still wrong", status)
	}
	if len(got) != 1 || got[0] != "signup fell 40%" {
		t.Fatalf("Discord received %q, want the human rendering in `content`", got)
	}
}

// The signed-JSON body must FAIL against a Discord-shaped receiver, or the test above proves
// nothing — a receiver that accepts anything cannot show that the format matters.
func TestSignedJSONIsRejectedByDiscord(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest listens on loopback
	var got []string
	srv := fakeDiscord(&got)
	defer srv.Close()
	plain := Endpoint{ID: "p1", Name: "raw", URL: srv.URL, Secret: "whsec_x", Enabled: true}
	// Send surfaces a 4xx as an error AND a status, so a rejection is err != nil with the code.
	status, err := Send(plain, []byte(`{"alert":"signup fell 40%"}`), "signup fell 40%")
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("a Discord receiver accepted signed JSON (status=%d err=%v) — the fixture is not "+
			"discriminating, so the test above proves nothing", status, err)
	}
	if len(got) != 0 {
		t.Fatalf("Discord recorded %q from a signed-JSON delivery", got)
	}
}

// Host detection, including the legacy and beta rings — a user on any of them pasted a real
// webhook URL, and getting this wrong means silently falling back to signed JSON.
func TestDiscordHostDetection(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://discord.com/api/webhooks/123/abc", true},
		{"https://discordapp.com/api/webhooks/123/abc", true}, // legacy host, still live
		{"https://ptb.discord.com/api/webhooks/123/abc", true},
		{"https://canary.discord.com/api/webhooks/123/abc", true},
		{"https://discord.com/channels/123", false}, // a channel link is not a webhook
		{"https://hooks.slack.com/services/T/B/x", false},
		{"https://example.com/api/webhooks/123/abc", false}, // path alone must not be enough
		{"://nonsense", false},
	} {
		if got := (Endpoint{URL: c.url}).DiscordFormat(); got != c.want {
			t.Errorf("DiscordFormat(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// Over Discord's 2000-character cap the WHOLE message is rejected, so a long daily digest must
// be clipped rather than dropped. A truncated alert is worth vastly more than a 400 nobody sees.
func TestALongMessageIsClippedRatherThanRejected(t *testing.T) {
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1") // httptest listens on loopback
	var got []string
	srv := fakeDiscord(&got)
	defer srv.Close()
	ep := Endpoint{ID: "d2", URL: srv.URL, Format: FormatDiscord, Secret: "s", Enabled: true}
	long := strings.Repeat("x", 5000)
	if _, err := Send(ep, []byte(`{}`), long); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("nothing delivered — a long digest was dropped instead of clipped")
	}
	if n := len([]rune(got[0])); n > discordLimit {
		t.Errorf("delivered %d chars, over Discord's %d cap — this would 400", n, discordLimit)
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Error("clipped without saying so; the reader cannot tell the message is incomplete")
	}
}

// A WEBHOOK MUST NOT BE ABLE TO DIE IN SILENCE.
//
// Delivery was `go func(ep){ _, _ = Send(...) }` — status and error both discarded, on a
// goroutine nobody waited for. An endpoint that started 500ing, or whose URL was revoked, simply
// stopped working and said nothing anywhere: no log line, no dashboard state, no field to
// inspect. That is exactly how a Discord endpoint rejected every delivery for as long as the
// feature existed without one person noticing.
func TestAFailingEndpointRecordsWhyAndEventuallyDisablesItself(t *testing.T) {
	defer shrinkBackoff()()
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	st, err := Open(t.TempDir() + "/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	ep, err := st.Add("broken", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}

	// A 5xx is retried; four consecutive failed DELIVERIES then disable it.
	for i := 0; i < maxFailures; i++ {
		st.deliver(ep, []byte(`{"a":1}`), "a")
	}
	got, _ := st.Get(ep.ID)

	if got.Failures < maxFailures {
		t.Errorf("consecutive failures = %d, want >= %d", got.Failures, maxFailures)
	}
	if got.LastError == "" {
		t.Error("no last error recorded — the operator has nothing to look at")
	}
	if got.LastAttempt.IsZero() {
		t.Error("no last attempt time recorded")
	}
	if got.Enabled {
		t.Error("still enabled after repeated failure; it will POST at a dead receiver forever")
	}
	if got.DisabledAt.IsZero() {
		t.Error("disabled with no timestamp, so the row cannot say WHY it turned itself off — " +
			"a silent auto-disable is a second invisible failure on top of the first")
	}
	if h := got.Health(); !strings.Contains(h, "auto-disabled") {
		t.Errorf("health reads %q, which does not tell the operator it stopped", h)
	}
	if attempts < maxFailures*maxAttempts {
		t.Errorf("only %d HTTP attempts across %d deliveries — 5xx is not being retried", attempts, maxFailures)
	}
}

// A DEFINITE NO IS NOT RETRIED. A 400 or 404 means the receiver understood and refused — a
// revoked URL, a wrong body shape — and hammering it changes nothing except the odds of being
// rate-limited or blocked. 429 is the exception, because it explicitly means "later".
func TestA4xxIsNotRetriedButA429Is(t *testing.T) {
	defer shrinkBackoff()()
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	for _, c := range []struct {
		code        int
		wantRetries bool
	}{
		{http.StatusNotFound, false},
		{http.StatusBadRequest, false},
		{http.StatusTooManyRequests, true},
	} {
		var n int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n++
			w.WriteHeader(c.code)
		}))
		st, _ := Open(t.TempDir() + "/h.json")
		ep, _ := st.Add("x", srv.URL, "")
		st.deliver(ep, []byte(`{}`), "x")
		srv.Close()

		if c.wantRetries && n < 2 {
			t.Errorf("%d: %d attempt(s) — 429 means try later and must be retried", c.code, n)
		}
		if !c.wantRetries && n != 1 {
			t.Errorf("%d: %d attempts — a definite refusal must not be retried", c.code, n)
		}
	}
}

// A SUCCESS CLEARS THE STREAK. A receiver that hiccups once a week is flaky, not dead; counting
// failures cumulatively would eventually switch off every endpoint that has ever wobbled.
func TestOneSuccessResetsTheFailureStreak(t *testing.T) {
	defer shrinkBackoff()()
	t.Setenv("SMOLANALYTICS_ALLOW_PRIVATE_WEBHOOKS", "1")
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st, _ := Open(t.TempDir() + "/h.json")
	ep, _ := st.Add("flaky", srv.URL, "")
	st.deliver(ep, []byte(`{}`), "x")
	if got, _ := st.Get(ep.ID); got.Failures == 0 {
		t.Fatal("a failed delivery was not counted")
	}
	fail = false
	st.deliver(ep, []byte(`{}`), "x")

	got, _ := st.Get(ep.ID)
	if got.Failures != 0 {
		t.Errorf("failures = %d after a success, want 0", got.Failures)
	}
	if got.LastDelivered.IsZero() {
		t.Error("no successful-delivery timestamp recorded")
	}
	if got.LastError != "" {
		t.Errorf("stale error %q survives a successful delivery", got.LastError)
	}
	if h := got.Health(); !strings.Contains(h, "delivered") {
		t.Errorf("health reads %q after a success", h)
	}
}

// PAUSING MUST NOT COST THE SIGNING SECRET. Enabled shipped with the store and no method ever
// wrote it, so the only way to stop deliveries was to DELETE the endpoint — which destroys the
// secret and forces every receiver to be reconfigured. A pause that costs you your secret is
// not a pause.
func TestPausingKeepsTheSecretAndResumingClearsTheFailures(t *testing.T) {
	st, _ := Open(t.TempDir() + "/h.json")
	ep, _ := st.Add("x", "https://example.com/hook", "")
	secret := ep.Secret
	if secret == "" {
		t.Fatal("no secret generated")
	}

	paused, found, err := st.SetEnabled(ep.ID, false)
	if err != nil || !found {
		t.Fatalf("pause failed: found=%v err=%v", found, err)
	}
	if paused.Enabled {
		t.Error("still enabled after being paused")
	}
	if paused.Secret != secret {
		t.Error("pausing changed the signing secret, so every receiver would need reconfiguring")
	}

	// A paused endpoint receives nothing.
	st.recordDelivery(ep.ID, 500, errFake{})
	before, _ := st.Get(ep.ID)

	resumed, _, _ := st.SetEnabled(ep.ID, true)
	if !resumed.Enabled {
		t.Error("resume did not re-enable")
	}
	if resumed.Failures != 0 || !resumed.DisabledAt.IsZero() {
		t.Errorf("resuming kept the old failure state (%d failures) — one more blip would "+
			"auto-disable it again immediately", before.Failures)
	}

	if _, found, _ := st.SetEnabled("nope", true); found {
		t.Error("reported success for an id that does not exist")
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }

// shrinkBackoff makes retry pauses negligible for the duration of a test, and restores them.
// Without it these four tests sleep for 36 real seconds between them.
func shrinkBackoff() func() {
	prev := backoff
	backoff = func(int) time.Duration { return time.Millisecond }
	return func() { backoff = prev }
}
