package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// Reconciling code against data is the thing an events-only tool cannot do. These pin the two
// findings that make it worth having, because both are invisible in event data alone:
// a track() call that never fires, and events arriving from a call site that no longer exists.

func repoWithTracking(t *testing.T, calls map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, file := range calls {
		p := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "export function handler() {\n  smolanalytics.track(\"" + name + "\", { plan: \"pro\" });\n}\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func provServer(t *testing.T, arriving ...string) *Server {
	t.Helper()
	st := memory.New()
	now := time.Now().UTC().Add(-time.Hour)
	var evs []event.Event
	for i, n := range arriving {
		for j := 0; j < 5; j++ {
			evs = append(evs, event.Event{
				ID: n + string(rune('a'+i)) + string(rune('0'+j)), Name: n,
				DistinctID: "u1", Timestamp: now, Properties: map[string]any{"plan": "pro"},
			})
		}
	}
	if len(evs) > 0 {
		if err := st.Ingest(evs...); err != nil {
			t.Fatal(err)
		}
	}
	return New(st)
}

func eventSource(t *testing.T, s *Server, repo string) map[string]any {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"repo_path": repo})
	out, err := s.callTool("event_source", args)
	if err != nil {
		t.Fatalf("event_source: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return m
}

func stateOf(m map[string]any, name string) (string, map[string]any) {
	evs, _ := m["events"].([]any)
	for _, e := range evs {
		row, _ := e.(map[string]any)
		if row["event"] == name {
			st, _ := row["state"].(string)
			return st, row
		}
	}
	return "", nil
}

// The headline case: a track() call exists in the repo but nothing has ever arrived. In every
// events-only tool this event simply does not exist, so nobody ever notices it is missing.
func TestFindsTrackingThatNeverFires(t *testing.T) {
	repo := repoWithTracking(t, map[string]string{
		"signup":   "src/auth.ts",
		"checkout": "src/pay.ts",
	})
	s := provServer(t, "signup") // only signup actually arrives

	m := eventSource(t, s, repo)
	st, row := stateOf(m, "checkout")
	if st != stateSilent {
		t.Fatalf("checkout should be reported as written-but-never-firing, got %q", st)
	}
	if row["file"] != "src/pay.ts" {
		t.Errorf("should point at the call site, got file=%v line=%v", row["file"], row["line"])
	}
	if !strings.Contains(row["meaning"].(string), "never deployed") {
		t.Errorf("meaning should name the likely causes, got %q", row["meaning"])
	}
	if st, _ := stateOf(m, "signup"); st != stateHealthy {
		t.Errorf("signup arrives and is in code, should be healthy, got %q", st)
	}
	if h := m["headline"].(string); !strings.Contains(h, "never arriving") {
		t.Errorf("headline should surface the finding, got %q", h)
	}
}

// The mirror case: events keep arriving from a call site that no longer exists in the repo —
// a deleted or renamed handler still live in production, or another app on the same write key.
func TestFindsEventsWithNoCallSiteLeft(t *testing.T) {
	repo := repoWithTracking(t, map[string]string{"signup": "src/auth.ts"})
	s := provServer(t, "signup", "legacy_purchase")

	m := eventSource(t, s, repo)
	st, row := stateOf(m, "legacy_purchase")
	if st != stateOrphan {
		t.Fatalf("legacy_purchase has no call site in the repo, expected %q, got %q", stateOrphan, st)
	}
	if row["count_30d"].(float64) != 5 {
		t.Errorf("orphan should still report its volume, got %v", row["count_30d"])
	}
	if !strings.Contains(row["meaning"].(string), "deleted or renamed") {
		t.Errorf("meaning should explain the usual cause, got %q", row["meaning"])
	}
}

// SDK-emitted events are not written by anyone, so they can never appear in the repo. Reporting
// them as orphans would bury every real finding under $pageview and friends.
func TestSdkEventsAreNotReportedAsOrphans(t *testing.T) {
	repo := repoWithTracking(t, map[string]string{"signup": "src/auth.ts"})
	s := provServer(t, "signup", "$pageview", "$identify", "$feature_flag_called")

	m := eventSource(t, s, repo)
	for _, n := range []string{"$pageview", "$identify", "$feature_flag_called"} {
		if st, _ := stateOf(m, n); st != "" {
			t.Errorf("%s should be excluded from reconciliation, was reported as %q", n, st)
		}
	}
}

// A clean repo must say so plainly rather than returning a wall of rows with nothing to act on.
func TestCleanReconciliationSaysSo(t *testing.T) {
	repo := repoWithTracking(t, map[string]string{"signup": "src/auth.ts", "checkout": "src/pay.ts"})
	s := provServer(t, "signup", "checkout")

	m := eventSource(t, s, repo)
	h := m["headline"].(string)
	if !strings.Contains(h, "reconcile") {
		t.Errorf("a clean result should say everything reconciles, got %q", h)
	}
	if strings.Contains(h, "never arriving") || strings.Contains(h, "no call site") {
		t.Errorf("a clean result should not report findings, got %q", h)
	}
}

// Asking about one event must work, and a name in neither place must error rather than return an
// empty result that reads as "this event is fine".
func TestSingleEventLookupAndUnknownName(t *testing.T) {
	repo := repoWithTracking(t, map[string]string{"signup": "src/auth.ts"})
	s := provServer(t, "signup")

	args, _ := json.Marshal(map[string]any{"repo_path": repo, "event": "signup"})
	out, err := s.callTool("event_source", args)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	if evs, _ := m["events"].([]any); len(evs) != 1 {
		t.Errorf("asking for one event returned %d rows", len(evs))
	}

	args, _ = json.Marshal(map[string]any{"repo_path": repo, "event": "not_a_real_event"})
	if _, err := s.callTool("event_source", args); err == nil {
		t.Error("an unknown event name should error, not return an empty result that reads as healthy")
	}
}

func TestEventSourceIsListedAndExplainsTheRepoRequirement(t *testing.T) {
	s := newServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	b, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, tl := range got.Tools {
		if tl.Name != "event_source" {
			continue
		}
		// The description has to say it needs the repo, or a model calls it against a hosted
		// dashboard, gets nothing, and concludes the product is broken.
		for _, must := range []string{"repo", "file and line", "never fire"} {
			if !strings.Contains(tl.Description, must) {
				t.Errorf("description missing %q — a model needs to know when this applies", must)
			}
		}
		return
	}
	t.Fatal("event_source is not in tools/list")
}
