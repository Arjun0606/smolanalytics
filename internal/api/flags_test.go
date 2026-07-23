package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func flagServer(t *testing.T) *Server {
	t.Helper()
	s := New(memory.New())
	s.SetWriteKey("wk")
	fs, err := flag.Open("") // in-memory
	if err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)
	return s
}

func evalFlags(t *testing.T, h http.Handler, id string) map[string]string {
	t.Helper()
	r := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/flags/evaluate?distinct_id="+id, nil)
	req.Header.Set("Authorization", "Bearer wk")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("evaluate %s: got %d, want 200 (%s)", id, r.Code, r.Body.String())
	}
	var out struct {
		Flags map[string]string `json:"flags"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Flags
}

// The SDK reads flags with the PUBLIC WRITE key over a CORS'd GET; off flags are absent so the
// SDK falls back to its default.
func TestFlagEvaluateEndpoint(t *testing.T) {
	s := flagServer(t)
	if _, err := s.flags.Save(flag.Flag{Key: "checkout_v2", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.flags.Save(flag.Flag{Key: "dark_mode", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	// no write key -> 401
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/v1/flags/evaluate?distinct_id=u1", nil))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("no key: got %d, want 401", r.Code)
	}

	// good key -> 200, CORS on, correct payload
	r = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/flags/evaluate?distinct_id=u1", nil)
	req.Header.Set("Authorization", "Bearer wk")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("good key: got %d, want 200", r.Code)
	}
	if r.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("evaluate must be CORS-enabled for the browser SDK")
	}
	flags := evalFlags(t, h, "u1")
	if flags["checkout_v2"] != "on" {
		t.Fatalf("checkout_v2 should be on, got %q", flags["checkout_v2"])
	}
	if _, present := flags["dark_mode"]; present {
		t.Fatal("a disabled flag must be absent from evaluate")
	}
}

func TestFlagEvaluateMissingDistinctID(t *testing.T) {
	s := flagServer(t)
	h := s.Handler()
	r := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/flags/evaluate", nil)
	req.Header.Set("Authorization", "Bearer wk")
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("missing distinct_id: got %d, want 400", r.Code)
	}
}

// The endpoint the SDK calls must resolve variants identically to the shared flag core that the
// MCP evaluate_flag tool uses — same deterministic bucket for a user across both paths. This is
// the "provably correct" contract, extended to flags.
func TestFlagEvaluateAgreesWithCore(t *testing.T) {
	s := flagServer(t)
	if _, err := s.flags.Save(flag.Flag{
		Key: "banner", Enabled: true,
		Variants: []flag.Variant{{Key: "a", Weight: 50}, {Key: "b", Weight: 50}},
	}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	f, _ := s.flags.Get("banner")
	for _, id := range []string{"u1", "u2", "u3", "alice", "bob", "carol", "dave", "erin"} {
		want, on := f.Evaluate(id, nil)
		if !on {
			t.Fatalf("%s: banner should be on", id)
		}
		if got := evalFlags(t, h, id)["banner"]; got != want {
			t.Fatalf("%s: endpoint=%q core=%q — evaluate paths disagree", id, got, want)
		}
	}
}

// Rollout is deterministic: the same user always lands the same way, so an SDK that caches the
// value never flickers between page loads.
func TestFlagRolloutDeterministic(t *testing.T) {
	s := flagServer(t)
	if _, err := s.flags.Save(flag.Flag{
		Key: "beta", Enabled: true,
		Rules: []flag.Rule{{RolloutPct: 40}},
	}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	first := evalFlags(t, h, "steady-user")
	for i := 0; i < 5; i++ {
		if got := evalFlags(t, h, "steady-user"); got["beta"] != first["beta"] {
			t.Fatalf("rollout flapped for the same user: %q vs %q", got["beta"], first["beta"])
		}
	}
}
