package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ev := func(u, n string, off time.Duration) event.Event {
		return event.Event{ID: u + n + off.String(), DistinctID: u, Name: n, Timestamp: base.Add(off),
			Properties: map[string]any{"source": "google"}}
	}
	_ = st.Ingest(
		ev("a", "signup", 0), ev("a", "activate", time.Hour), ev("a", "checkout", 2*time.Hour),
		ev("b", "signup", 0), ev("b", "activate", time.Hour),
		ev("c", "signup", 0),
	)
	return New(st)
}

func call(t *testing.T, s *Server, raw string) *response {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("bad request json: %v", err)
	}
	return s.Dispatch(req)
}

func TestInitializeAndToolsList(t *testing.T) {
	s := newServer(t)
	if r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); r == nil || r.Error != nil {
		t.Fatalf("initialize failed: %+v", r)
	}
	r := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	b, _ := json.Marshal(r.Result)
	for _, name := range []string{"overview", "funnel", "retention", "trends", "breakdown", "list_events"} {
		if !strings.Contains(string(b), `"`+name+`"`) {
			t.Fatalf("tools/list missing %s: %s", name, b)
		}
	}
}

func TestNotificationReturnsNil(t *testing.T) {
	s := newServer(t)
	if r := call(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r != nil {
		t.Fatalf("notification should produce no response, got %+v", r)
	}
}

func TestFunnelToolCall(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"funnel","arguments":{"steps":["signup","activate","checkout"]}}}`)
	b, _ := json.Marshal(r.Result)
	// 3 signup, 2 activate, 1 checkout from the seeded data
	if !strings.Contains(string(b), `\"count\":3`) || !strings.Contains(string(b), `\"count\":1`) {
		t.Fatalf("funnel result missing expected counts: %s", b)
	}
	if strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("funnel call errored: %s", b)
	}
}

func TestUnknownToolIsError(t *testing.T) {
	s := newServer(t)
	r := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	b, _ := json.Marshal(r.Result)
	if !strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("unknown tool should be isError: %s", b)
	}
}
