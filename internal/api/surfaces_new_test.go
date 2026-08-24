package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func newSurfaceServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st := memory.New()
	s := New(st)
	fs, err := flag.Open(t.TempDir() + "/flags.json")
	if err != nil {
		t.Fatalf("flag store: %v", err)
	}
	s.SetFlags(fs)
	is, err := insights.Open(t.TempDir() + "/insights.json")
	if err != nil {
		t.Fatalf("insights store: %v", err)
	}
	s.SetInsights(is)
	return s, s.Handler()
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, rdr))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// The SRM check is the thing you run BEFORE trusting an A/B result, and it had no HTTP route at
// all — callable only from the MCP tool, while the A/B report's own note told the reader to go
// check it. A check nobody can reach is a check nobody runs.
func TestSRMHealthEndpointIsReachableAndDetectsAMismatch(t *testing.T) {
	s, h := newSurfaceServer(t)
	f := flag.Flag{
		Key: "checkout_v2", Enabled: true, Measured: true,
		Variants: []flag.Variant{{Key: "control", Weight: 50}, {Key: "treatment", Weight: 50}},
	}
	if _, err := s.flags.Save(f); err != nil {
		t.Fatal(err)
	}
	// a 50/50 flag that actually split 80/20 — the randomisation is broken
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < 800; i++ {
		_ = s.store.Ingest(exposureEvent("c"+itoa(i), f.Key, "control", base))
	}
	for i := 0; i < 200; i++ {
		_ = s.store.Ingest(exposureEvent("t"+itoa(i), f.Key, "treatment", base))
	}

	code, out := doJSON(t, h, "GET", "/v1/flags/checkout_v2/health?days=30", "")
	if code != 200 {
		t.Fatalf("GET /v1/flags/{key}/health returned %d, want 200 — the check is unreachable again", code)
	}
	if d, _ := out["detected"].(bool); !d {
		t.Errorf("an 80/20 split on a 50/50 flag was not reported as a mismatch: %v", out)
	}

	// an unknown flag is a 404, not an empty-looking pass
	if code, _ := doJSON(t, h, "GET", "/v1/flags/nope/health", ""); code != http.StatusNotFound {
		t.Errorf("health on an unknown flag returned %d, want 404 — a pass on a flag that does not exist reads as good news", code)
	}
	// days must not silently coerce; the tool documents 0 = all time
	if code, _ := doJSON(t, h, "GET", "/v1/flags/checkout_v2/health?days=-3", ""); code != http.StatusBadRequest {
		t.Errorf("days=-3 returned %d, want 400", code)
	}
}

// Boards are the #1 gap against PostHog and Mixpanel, whose users open a dashboard every morning.
// The whole point is that they are REACHABLE over the API the UI will use.
func TestBoardsAPIRoundTrip(t *testing.T) {
	_, h := newSurfaceServer(t)

	// a fresh instance always has the default board, so the UI never renders an empty switcher
	code, out := doJSON(t, h, "GET", "/v1/boards", "")
	if code != 200 {
		t.Fatalf("GET /v1/boards = %d", code)
	}
	boards, _ := out["boards"].([]any)
	if len(boards) == 0 {
		t.Fatal("a fresh instance has no boards at all; the switcher would render empty")
	}

	code, made := doJSON(t, h, "POST", "/v1/boards", `{"name":"Growth","filters":{"plan":"pro"}}`)
	if code != 200 {
		t.Fatalf("POST /v1/boards = %d (%v)", code, made)
	}
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("created board has no id: %v", made)
	}

	// a report saved onto it, then moved off it
	code, ins := doJSON(t, h, "POST", "/v1/insights",
		`{"name":"Checkout","type":"funnel","board_id":"`+id+`"}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("POST /v1/insights = %d (%v)", code, ins)
	}
	if got, _ := ins["board_id"].(string); got != id {
		t.Errorf("saved report landed on board %q, want the one it was posted to (%q)", got, id)
	}
	insID, _ := ins["id"].(string)

	// deleting a board that still holds a report must not silently take the report with it
	code, _ = doJSON(t, h, "DELETE", "/v1/boards/"+id, "")
	if code == 200 {
		_, after := doJSON(t, h, "GET", "/v1/insights", "")
		list, _ := after["insights"].([]any)
		if len(list) == 0 {
			t.Fatal("deleting a board destroyed the saved report on it")
		}
	}

	if insID != "" {
		code, moved := doJSON(t, h, "POST", "/v1/insights/"+insID+"/board",
			`{"board_id":"`+insights.DefaultBoardID+`"}`)
		if code != 200 {
			t.Fatalf("moving an insight between boards = %d (%v)", code, moved)
		}
		if got, _ := moved["board_id"].(string); got != insights.DefaultBoardID {
			t.Errorf("insight landed on board %q, want the default", got)
		}
	}
	// a move to a board that does not exist must fail loudly rather than stranding the report
	if code, _ := doJSON(t, h, "POST", "/v1/insights/"+insID+"/board", `{"board_id":"ghost"}`); code == 200 {
		t.Error("an insight was moved onto a board that does not exist")
	}
}

func exposureEvent(id, key, variant string, at time.Time) event.Event {
	return event.Event{
		Name: flag.ExposureEvent, DistinctID: id, Timestamp: at,
		Properties: map[string]any{flag.PropFlag: key, flag.PropVariant: variant},
	}
}

// itoa without strconv, kept here after dashboard_aivis_test.go went with the AI-visibility pane.
// It is a test helper only; production code uses strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
