package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// Bucketing on distinct_id is the most common silent corruption in A/B testing, and it fires at
// exactly the worst moment. identify() replaces the anonymous id with the account id at login,
// so the hash input changes, so the variant changes: one person sees A while signed out and B
// after signing up, with their pre-login behaviour still credited to the arm they left. Nothing
// errors — the experiment just reports a blend of two populations, and signup is usually the
// conversion the experiment exists to measure.
//
// bucket_id is written once per browser and never rewritten, so assignment survives login.

func bucketServer(t *testing.T) *Server {
	t.Helper()
	s := New(memory.New())
	s.SetWriteKey("wk")
	fs, err := flag.Open("") // in-memory
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Save(flag.Flag{
		Key: "checkout-copy", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "test", Weight: 50}},
	}); err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)
	return s
}

// evalBucketed hits the real endpoint, optionally passing a stable bucket key.
func evalBucketed(t *testing.T, s *Server, distinctID, bucketID string) map[string]string {
	t.Helper()
	url := "/v1/flags/evaluate?distinct_id=" + distinctID
	if bucketID != "" {
		url += "&bucket_id=" + bucketID
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer wk")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("evaluate returned %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Flags map[string]string `json:"flags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Flags
}

func TestLoggingInDoesNotChangeTheVariant(t *testing.T) {
	s := bucketServer(t)
	changed := 0
	const n = 500
	for i := 0; i < n; i++ {
		anon := fmt.Sprintf("a-browser-%d", i)
		account := fmt.Sprintf("user-%d", i)

		before := evalBucketed(t, s, anon, anon)   // signed out: distinct_id == bucket_id
		after := evalBucketed(t, s, account, anon) // signed in: distinct_id changed, bucket_id did not

		if before["checkout-copy"] != after["checkout-copy"] {
			changed++
		}
	}
	if changed != 0 {
		t.Errorf("%d of %d users changed variant when they logged in — assignment is not surviving identify()", changed, n)
	}
}

// The bug this prevents, demonstrated. Without a stable bucket key the same person is reassigned
// at login roughly half the time on a 50/50 experiment, which is what the old behaviour did.
func TestWithoutBucketIdLoginReassignsAboutHalf(t *testing.T) {
	s := bucketServer(t)
	changed := 0
	const n = 500
	for i := 0; i < n; i++ {
		before := evalBucketed(t, s, fmt.Sprintf("a-browser-%d", i), "")
		after := evalBucketed(t, s, fmt.Sprintf("user-%d", i), "")
		if before["checkout-copy"] != after["checkout-copy"] {
			changed++
		}
	}
	ratio := float64(changed) / n
	if ratio < 0.35 || ratio > 0.65 {
		t.Errorf("expected roughly half of users to be reassigned without a stable key, got %.0f%%", 100*ratio)
	}
	t.Logf("without bucket_id, %.0f%% of users would be reassigned at login", 100*ratio)
}

// Older SDKs send no bucket_id. They must keep working exactly as before rather than erroring or
// silently landing everyone in one arm.
func TestOlderSdksWithoutBucketIdStillWork(t *testing.T) {
	s := bucketServer(t)
	seen := map[string]int{}
	for i := 0; i < 400; i++ {
		f := evalBucketed(t, s, fmt.Sprintf("legacy-%d", i), "")
		seen[f["checkout-copy"]]++
	}
	if len(seen) != 2 {
		t.Fatalf("expected both variants to be served without bucket_id, got %v", seen)
	}
	for k, v := range seen {
		if v < 150 {
			t.Errorf("variant %q served only %d of 400 — the split collapsed without bucket_id", k, v)
		}
	}
	// And the fallback must agree with passing distinct_id explicitly as the bucket key.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("legacy-%d", i)
		if evalBucketed(t, s, id, "")["checkout-copy"] != evalBucketed(t, s, id, id)["checkout-copy"] {
			t.Fatalf("omitting bucket_id disagrees with passing distinct_id as the bucket key, for %q", id)
		}
	}
}

// Two people sharing an account id but on different browsers must still bucket independently,
// which is the property that makes bucket_id a DEVICE key rather than a second user id.
func TestSameAccountOnTwoBrowsersBucketsIndependently(t *testing.T) {
	s := bucketServer(t)
	differ := 0
	for i := 0; i < 300; i++ {
		a := evalBucketed(t, s, "shared-account", fmt.Sprintf("browser-a-%d", i))
		b := evalBucketed(t, s, "shared-account", fmt.Sprintf("browser-b-%d", i))
		if a["checkout-copy"] != b["checkout-copy"] {
			differ++
		}
	}
	if differ == 0 {
		t.Error("bucket_id is being ignored — every browser got the same variant for one account")
	}
}
