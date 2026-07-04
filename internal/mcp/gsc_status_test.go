package mcp

// gsc_status must report exactly what the store holds — each state (no store, not
// connected, connected-but-unfetched, fetched, page-fetch failing) gets an honest
// answer with the next step named, never a guessed zero.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/gsc"
)

// statusOf calls the gsc_status tool and decodes its JSON (or returns the error text).
func statusOf(t *testing.T, s *Server) (map[string]any, string, bool) {
	t.Helper()
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gsc_status","arguments":{}}}`)
	res, _ := r.Result.(map[string]any)
	content, _ := res["content"].([]map[string]any)
	if len(content) == 0 {
		t.Fatalf("no content in response: %+v", r.Result)
	}
	text, _ := content[0]["text"].(string)
	if isErr, _ := res["isError"].(bool); isErr {
		return nil, text, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("gsc_status returned non-JSON: %q", text)
	}
	return out, text, false
}

func TestGSCStatusNoStore(t *testing.T) {
	s := newServer(t) // no SetGSC — bare demo/stdio mode
	_, text, isErr := statusOf(t, s)
	if !isErr || !strings.Contains(text, "search-console") {
		t.Fatalf("want the no-store error naming search-console, got (%v) %q", isErr, text)
	}
}

func TestGSCStatusNotConnected(t *testing.T) {
	s := newServer(t)
	g, err := gsc.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.SetGSC(g)
	out, _, isErr := statusOf(t, s)
	if isErr {
		t.Fatalf("status of a healthy-but-unconnected store must not error: %v", out)
	}
	if out["connected"] != false {
		t.Fatalf("connected = %v, want false", out["connected"])
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "gsc auth") {
		t.Fatalf("note must name the fix (`smolanalytics gsc auth`): %v", out)
	}
}

func TestGSCStatusConnectedNotFetched(t *testing.T) {
	s := newServer(t)
	g, _ := gsc.Open("")
	if err := g.SetGrant("refresh-token", "sc-domain:example.com"); err != nil {
		t.Fatal(err)
	}
	s.SetGSC(g)
	out, _, isErr := statusOf(t, s)
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	if out["connected"] != true || out["site"] != "sc-domain:example.com" {
		t.Fatalf("connected/site wrong: %v", out)
	}
	if out["query_rows"] != float64(0) || out["page_rows"] != float64(0) {
		t.Fatalf("row counts must be exactly 0 before a fetch: %v", out)
	}
	if _, has := out["fetched_at"]; has {
		t.Fatalf("fetched_at must be absent (not a fabricated time) before the first pull: %v", out)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, "no data fetched yet") {
		t.Fatalf("note must say why it's empty: %v", out)
	}
}

func TestGSCStatusFetchedWithPageError(t *testing.T) {
	s := newServer(t)
	g, _ := gsc.Open("")
	_ = g.SetGrant("refresh-token", "sc-domain:example.com")
	_ = g.SetRows([]gsc.Row{{Query: "smol analytics", Clicks: 3}, {Query: "tiny analytics", Clicks: 1}})
	_ = g.SetPageFetchError("google api 429: quota")
	s.SetGSC(g)
	out, _, isErr := statusOf(t, s)
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	if out["query_rows"] != float64(2) || out["page_rows"] != float64(0) {
		t.Fatalf("row counts wrong: %v", out)
	}
	if _, has := out["fetched_at"]; !has {
		t.Fatalf("fetched_at missing after a fetch: %v", out)
	}
	if e, _ := out["last_page_fetch_error"].(string); !strings.Contains(e, "google api 429") {
		t.Fatalf("page fetch error not surfaced: %v", out)
	}
}
