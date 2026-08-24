package api

// The outcome ledger's HTTP surface, and the parity between the desk's JavaScript and the route
// table. Each guard was watched fail before it was trusted.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/acted"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func TestMarkActedEndpoint(t *testing.T) {
	// Without a store the endpoint must refuse loudly — a 200 here would tell the desk the mark
	// was saved when nothing anywhere recorded it.
	bare := httptest.NewServer(New(memory.New()).Handler())
	defer bare.Close()
	resp, err := http.Post(bare.URL+"/v1/findings/acted", "application/json",
		strings.NewReader(`{"fingerprint":"regression|checkout|2026-08-01"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no ledger: want 503, got %d", resp.StatusCode)
	}

	// With one: the mark lands, and marking twice keeps the first timestamp.
	ac, err := acted.Open(t.TempDir() + "/acted.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(memory.New())
	s.SetActed(ac)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	post := func(body string) (int, map[string]any) {
		resp, err := http.Post(srv.URL+"/v1/findings/acted", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	if code, _ := post(`{"note":"no fingerprint"}`); code != http.StatusBadRequest {
		t.Fatalf("missing fingerprint: want 400, got %d", code)
	}
	code, first := post(`{"fingerprint":"regression|checkout|2026-08-01","note":"shipped a fix"}`)
	if code != http.StatusOK {
		t.Fatalf("mark: want 200, got %d", code)
	}
	code, second := post(`{"fingerprint":"regression|checkout|2026-08-01"}`)
	if code != http.StatusOK || second["at"] != first["at"] {
		t.Fatalf("re-mark rewrote history: %v then %v", first["at"], second["at"])
	}
	if e, ok := ac.Get("regression|checkout|2026-08-01"); !ok || e.Note != "shipped a fix" {
		t.Fatalf("the mark did not reach the store: %+v ok=%v", e, ok)
	}
}
