package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// collect runs a stream through the mapper and returns what came out.
func collect(t *testing.T, format string, body string) ([]event.Event, map[string]int) {
	t.Helper()
	m, err := MapperFor(format)
	if err != nil {
		t.Fatal(err)
	}
	var got []event.Event
	skips := map[string]int{}
	if err := m(strings.NewReader(body), func(e event.Event) error {
		got = append(got, e)
		return nil
	}, func(r string) { skips[r]++ }); err != nil {
		t.Fatal(err)
	}
	return got, skips
}

// An unbounded pull is refused. Defaulting to "everything" turns one mistyped call into a
// multi-year fetch that burns the user's vendor rate limit and bills them here for history they
// never asked for.
func TestARemotePullMustBeBounded(t *testing.T) {
	base := Remote{Vendor: "mixpanel", Key: "secret"}
	if err := base.Validate(); err == nil {
		t.Fatal("no date range and it was accepted")
	}
	r := base
	r.From, r.To = time.Now().Add(-24*time.Hour), time.Now()
	if err := r.Validate(); err != nil {
		t.Fatalf("a bounded pull should be fine: %v", err)
	}
	// Backwards ranges are a typo, not a request.
	r.From, r.To = r.To, r.From
	if err := r.Validate(); err == nil {
		t.Fatal("a range that ends before it starts was accepted")
	}
}

func TestPostHogNeedsAProjectAndEveryVendorNeedsAKey(t *testing.T) {
	from, to := time.Now().Add(-24*time.Hour), time.Now()
	if err := (Remote{Vendor: "posthog", Key: "k", From: from, To: to}).Validate(); err == nil {
		t.Fatal("posthog without a project id was accepted")
	}
	if err := (Remote{Vendor: "mixpanel", From: from, To: to}).Validate(); err == nil {
		t.Fatal("no key and it was accepted")
	}
	if err := (Remote{Vendor: "segment", Key: "k", From: from, To: to}).Validate(); err == nil {
		t.Fatal("an unknown vendor was accepted")
	}
}

// The vendor's own event id becomes ours. Re-running an import must not double every number —
// which is the single most destructive way a migration can fail, and the most likely, because the
// natural reaction to a partial import is to run it again.
func TestReimportingIsIdempotentBecauseVendorIdsAreKept(t *testing.T) {
	body := `{"id":"ph-1","event":"signup","distinct_id":"u1","timestamp":"2026-01-02T03:04:05Z","properties":{"plan":"pro"}}
{"id":"ph-2","event":"checkout","distinct_id":"u1","timestamp":"2026-01-02T04:00:00Z","properties":{"amount":30}}`
	got, skips := collect(t, "posthog-api", body)
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d (skips %v)", len(got), skips)
	}
	if got[0].ID != "ph-1" || got[1].ID != "ph-2" {
		t.Fatalf("vendor ids dropped: %q, %q — a re-import would duplicate everything", got[0].ID, got[1].ID)
	}
	if got[0].Name != "signup" || got[0].DistinctID != "u1" {
		t.Fatalf("bad mapping: %+v", got[0])
	}
	if got[0].Properties["plan"] != "pro" {
		t.Fatalf("properties lost: %+v", got[0].Properties)
	}
	if !got[0].Timestamp.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("timestamp wrong: %v", got[0].Timestamp)
	}
}

// PostHog sometimes carries distinct_id only inside properties. Dropping those rows would silently
// lose events while reporting a clean import.
func TestDistinctIdIsFoundInPropertiesWhenItIsNotTopLevel(t *testing.T) {
	body := `{"id":"a","event":"signup","timestamp":"2026-01-02T03:04:05Z","properties":{"distinct_id":"u9"}}`
	got, skips := collect(t, "posthog-api", body)
	if len(got) != 1 {
		t.Fatalf("row dropped: skips %v", skips)
	}
	if got[0].DistinctID != "u9" {
		t.Fatalf("want u9, got %q", got[0].DistinctID)
	}
}

func TestRowsWithoutTheEssentialsAreCountedNotSilentlyDropped(t *testing.T) {
	body := `{"id":"a","event":"","distinct_id":"u1","timestamp":"2026-01-02T03:04:05Z"}
{"id":"b","event":"signup","timestamp":"2026-01-02T03:04:05Z"}
{"id":"c","event":"signup","distinct_id":"u1","timestamp":"2026-01-02T03:04:05Z"}`
	got, skips := collect(t, "posthog-api", body)
	if len(got) != 1 {
		t.Fatalf("want the 1 good row, got %d", len(got))
	}
	if skips["missing event name"] != 1 || skips["missing distinct_id"] != 1 {
		t.Fatalf("skips must be counted per reason, got %v", skips)
	}
}

// The CSV mapper and the API mapper are NOT interchangeable, and picking the wrong one is LOUD.
//
// The dangerous version of this is a mapper that shrugs at a format it does not understand and
// reports a clean import of zero events — someone would believe their history had moved. The CSV
// mapper errors on a JSON body instead, which is the behaviour worth pinning.
func TestFeedingAMapperTheWrongFormatIsAnErrorNotASilentZero(t *testing.T) {
	apiBody := `{"id":"a","event":"signup","distinct_id":"u1","timestamp":"2026-01-02T03:04:05Z"}`
	m, err := MapperFor("posthog") // the CSV mapper
	if err != nil {
		t.Fatal(err)
	}
	var got []event.Event
	err = m(strings.NewReader(apiBody), func(e event.Event) error { got = append(got, e); return nil }, func(string) {})
	if err == nil && len(got) == 0 {
		t.Fatal("the CSV mapper silently reported a clean import of zero events from a JSON body")
	}
	if len(got) != 0 {
		t.Fatalf("the CSV mapper invented %d events out of JSON", len(got))
	}
}

// Paging is followed to the end, and the whole thing is one stream so the mapper can start work
// while later pages are still arriving.
func TestPostHogPagingFollowsNextToTheEnd(t *testing.T) {
	var srv *httptest.Server
	page := func(w http.ResponseWriter, ids []string, next string) {
		var results []json.RawMessage
		for _, id := range ids {
			results = append(results, json.RawMessage(fmt.Sprintf(
				`{"id":%q,"event":"signup","distinct_id":"u1","timestamp":"2026-01-02T03:04:05Z"}`, id)))
		}
		out := map[string]any{"results": results}
		if next != "" {
			out["next"] = srv.URL + next
		}
		json.NewEncoder(w).Encode(out)
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pk" {
			t.Errorf("posthog must get a bearer personal key, got %q", got)
		}
		switch r.URL.Query().Get("p") {
		case "":
			page(w, []string{"a", "b"}, "/next?p=2")
		case "2":
			page(w, []string{"c"}, "")
		}
	}))
	defer srv.Close()

	rem := Remote{Vendor: "posthog", Key: "pk", Project: "42", Host: srv.URL,
		From: time.Now().Add(-48 * time.Hour), To: time.Now()}
	body, err := rem.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	sum, err := Run(rem.Format(), true, body, nil) // dry run: parse only
	if err != nil {
		t.Fatal(err)
	}
	if sum.Parsed != 3 {
		t.Fatalf("want 3 events across 2 pages, got %d", sum.Parsed)
	}
}

// Mixpanel authenticates the export API with the secret as the basic-auth USERNAME. Sending it as
// a bearer token returns 401 with no explanation.
func TestMixpanelUsesBasicAuthAndItsStreamIsAlreadyOurFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "sec" || p != "" {
			t.Errorf("want the secret as basic-auth username, got user=%q pass=%q ok=%v", u, p, ok)
		}
		if r.URL.Query().Get("from_date") == "" || r.URL.Query().Get("to_date") == "" {
			t.Errorf("the date range must reach mixpanel, got %v", r.URL.Query())
		}
		fmt.Fprint(w, `{"event":"signup","properties":{"time":1767322800,"distinct_id":"u1","$insert_id":"mx-1"}}`+"\n")
	}))
	defer srv.Close()

	rem := Remote{Vendor: "mixpanel", Key: "sec", Host: srv.URL,
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}
	body, err := rem.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	sum, err := Run(rem.Format(), true, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Parsed != 1 {
		t.Fatalf("want 1 event, got %d (skips %v)", sum.Parsed, sum.Skipped)
	}
}

// A rejected credential must say WHICH credential and where to get it. "401 Unauthorized" is true
// and useless, and the project token from the website snippet is the wrong key people reach for
// first on both vendors.
func TestABadCredentialSaysWhichKeyIsWanted(t *testing.T) {
	for _, tc := range []struct{ vendor, want string }{
		{"mixpanel", "API SECRET"},
		{"posthog", "PERSONAL API KEY"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"detail":"nope"}`)
		}))
		rem := Remote{Vendor: tc.vendor, Key: "wrong", Project: "1", Host: srv.URL,
			From: time.Now().Add(-24 * time.Hour), To: time.Now()}
		body, err := rem.Fetch(context.Background(), srv.Client())
		if err == nil {
			// PostHog errors arrive through the pipe, so drain it to surface them.
			_, err = Run(rem.Format(), true, body, nil)
			body.Close()
		}
		if err == nil {
			t.Fatalf("%s: a rejected key produced no error", tc.vendor)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error must name the right credential (%q), got %v", tc.vendor, tc.want, err)
		}
		srv.Close()
	}
}

// Rate limiting is a "try a smaller range" instruction, not a stack trace.
func TestRateLimitingTellsYouWhatToDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	rem := Remote{Vendor: "mixpanel", Key: "s", Host: srv.URL,
		From: time.Now().Add(-24 * time.Hour), To: time.Now()}
	_, err := rem.Fetch(context.Background(), srv.Client())
	if err == nil {
		t.Fatal("a 429 produced no error")
	}
	if !strings.Contains(err.Error(), "2m0s") || !strings.Contains(err.Error(), "narrower") {
		t.Fatalf("want the wait and the fix, got %v", err)
	}
}
