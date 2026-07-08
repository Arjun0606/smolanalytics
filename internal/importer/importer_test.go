package importer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// collectMapped runs a mapper over an inline fixture and returns the mapped events
// plus the per-reason skip counts.
func collectMapped(t *testing.T, mapper func(io.Reader, EmitFn, SkipFn) error, input string) ([]event.Event, map[string]int) {
	t.Helper()
	var evs []event.Event
	skips := map[string]int{}
	err := mapper(strings.NewReader(input), func(e event.Event) error {
		evs = append(evs, e)
		return nil
	}, func(reason string) { skips[reason]++ })
	if err != nil {
		t.Fatalf("mapper: %v", err)
	}
	return evs, skips
}

func checkSkips(t *testing.T, got, want map[string]int) {
	t.Helper()
	if want == nil {
		want = map[string]int{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skips = %v, want %v", got, want)
	}
}

func TestMapJSONL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantN     int
		wantSkips map[string]int
		check     func(t *testing.T, evs []event.Event)
	}{
		{
			name:  "round trip keeps id timestamp and properties",
			input: `{"id":"abc","name":"signup","distinct_id":"u1","timestamp":"2024-03-01T10:00:00Z","properties":{"plan":"pro"}}`,
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				e := evs[0]
				if e.ID != "abc" || e.Name != "signup" || e.DistinctID != "u1" {
					t.Errorf("fields lost in round trip: %+v", e)
				}
				if !e.Timestamp.Equal(time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)) {
					t.Errorf("timestamp = %v", e.Timestamp)
				}
				if e.Properties["plan"] != "pro" {
					t.Errorf("properties = %v", e.Properties)
				}
			},
		},
		{
			name:  "blank lines ignored",
			input: "\n\n" + `{"name":"a","distinct_id":"u"}` + "\n\n",
			wantN: 1,
		},
		{
			name:      "bad json skipped rest kept",
			input:     "not json\n" + `{"name":"a","distinct_id":"u"}`,
			wantN:     1,
			wantSkips: map[string]int{"invalid JSON line": 1},
		},
		{
			name:      "missing name skipped",
			input:     `{"distinct_id":"u"}`,
			wantSkips: map[string]int{"missing event name": 1},
		},
		{
			name:      "missing distinct_id skipped",
			input:     `{"name":"a"}`,
			wantSkips: map[string]int{"missing distinct_id": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, skips := collectMapped(t, MapJSONL, tt.input)
			if len(evs) != tt.wantN {
				t.Fatalf("mapped %d events, want %d", len(evs), tt.wantN)
			}
			checkSkips(t, skips, tt.wantSkips)
			if tt.check != nil {
				tt.check(t, evs)
			}
		})
	}
}

func TestMapCSV(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantN     int
		wantSkips map[string]int
		check     func(t *testing.T, evs []event.Event)
	}{
		{
			name:  "name distinct_id time plus extra columns as string properties",
			input: "name,distinct_id,time,plan\nsignup,u1,2024-03-01T10:00:00Z,pro\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				e := evs[0]
				if e.Name != "signup" || e.DistinctID != "u1" {
					t.Errorf("event = %+v", e)
				}
				if !e.Timestamp.Equal(time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)) {
					t.Errorf("timestamp = %v", e.Timestamp)
				}
				if e.Properties["plan"] != "pro" {
					t.Errorf("properties = %v", e.Properties)
				}
			},
		},
		{
			name:  "event and user_id aliases with unix seconds",
			input: "event,user_id,timestamp\ncheckout,u2,1709287200\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].Name != "checkout" || evs[0].DistinctID != "u2" {
					t.Errorf("event = %+v", evs[0])
				}
				if got := evs[0].Timestamp; !got.Equal(time.Unix(1709287200, 0)) {
					t.Errorf("timestamp = %v", got)
				}
			},
		},
		{
			name:  "missing time column leaves timestamp zero for the server",
			input: "name,anonymous_id\npageview,a1\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				if !evs[0].Timestamp.IsZero() {
					t.Errorf("timestamp should stay zero, got %v", evs[0].Timestamp)
				}
			},
		},
		{
			name:      "empty distinct_id cell skipped",
			input:     "name,distinct_id\nsignup,\nsignup,u1\n",
			wantN:     1,
			wantSkips: map[string]int{"missing distinct_id": 1},
		},
		{
			name:      "unparseable timestamp skipped",
			input:     "name,distinct_id,time\nsignup,u1,yesterday-ish\n",
			wantSkips: map[string]int{"unparseable timestamp": 1},
		},
		{
			name:      "ragged row treated as missing cells",
			input:     "name,distinct_id,plan\nsignup\n",
			wantSkips: map[string]int{"missing distinct_id": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, skips := collectMapped(t, MapCSV, tt.input)
			if len(evs) != tt.wantN {
				t.Fatalf("mapped %d events, want %d", len(evs), tt.wantN)
			}
			checkSkips(t, skips, tt.wantSkips)
			if tt.check != nil {
				tt.check(t, evs)
			}
		})
	}
}

// a CSV without the required columns is a file-level error, not a row skip.
func TestMapCSVMissingColumns(t *testing.T) {
	for _, input := range []string{
		"distinct_id\nu1\n", // no name/event column
		"name\nsignup\n",    // no user column
		"",                  // no header at all
	} {
		err := MapCSV(strings.NewReader(input), func(event.Event) error { return nil }, func(string) {})
		if err == nil {
			t.Errorf("input %q: want an error, got nil", input)
		}
	}
}

func TestMapPostHog(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantN     int
		wantSkips map[string]int
		check     func(t *testing.T, evs []event.Event)
	}{
		{
			name: "properties JSON column merges with flat columns and wins conflicts",
			input: "event,distinct_id,timestamp,properties,properties.$browser,plan\n" +
				`pageview,u1,2024-03-01 10:00:00.123456,"{""$os"":""mac"",""plan"":""pro""}",Chrome,free` + "\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				e := evs[0]
				if e.Name != "pageview" || e.DistinctID != "u1" {
					t.Errorf("event = %+v", e)
				}
				if e.Timestamp.IsZero() || e.Timestamp.Year() != 2024 {
					t.Errorf("SQL-form timestamp not parsed: %v", e.Timestamp)
				}
				if e.Properties["$os"] != "mac" || e.Properties["$browser"] != "Chrome" {
					t.Errorf("properties = %v", e.Properties)
				}
				if e.Properties["plan"] != "pro" { // JSON column wins over the flat cell
					t.Errorf("plan = %v, want pro from the JSON column", e.Properties["plan"])
				}
			},
		},
		{
			name:  "no properties column at all",
			input: "event,distinct_id,timestamp\nsignup,u2,2024-03-01T10:00:00Z\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].Properties != nil {
					t.Errorf("properties = %v, want none", evs[0].Properties)
				}
			},
		},
		{
			name:      "bad properties JSON skipped",
			input:     "event,distinct_id,properties\nsignup,u1,{broken\n",
			wantSkips: map[string]int{"unparseable properties JSON": 1},
		},
		{
			name:      "missing distinct_id skipped",
			input:     "event,distinct_id\nsignup,\n",
			wantSkips: map[string]int{"missing distinct_id": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, skips := collectMapped(t, MapPostHog, tt.input)
			if len(evs) != tt.wantN {
				t.Fatalf("mapped %d events, want %d", len(evs), tt.wantN)
			}
			checkSkips(t, skips, tt.wantSkips)
			if tt.check != nil {
				tt.check(t, evs)
			}
		})
	}
}

func TestMapUmami(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantN     int
		wantSkips map[string]int
		check     func(t *testing.T, evs []event.Event)
	}{
		{
			name:  "empty event_name becomes $pageview with path",
			input: "session_id,event_name,url_path,created_at,browser\ns1,,/pricing,2024-05-01 12:34:56,chrome\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				e := evs[0]
				if e.Name != "$pageview" {
					t.Errorf("name = %q, want $pageview", e.Name)
				}
				if e.DistinctID != "s1" {
					t.Errorf("distinct_id = %q, want the session_id", e.DistinctID)
				}
				if e.Properties["path"] != "/pricing" {
					t.Errorf("path = %v", e.Properties["path"])
				}
				if e.Properties["browser"] != "chrome" {
					t.Errorf("extra column lost: %v", e.Properties)
				}
				if e.Timestamp.IsZero() || e.Timestamp.Year() != 2024 {
					t.Errorf("created_at not parsed: %v", e.Timestamp)
				}
			},
		},
		{
			name:  "named event keeps its name and referrer_domain maps to referrer",
			input: "session_id,event_name,url_path,created_at,referrer_domain\ns2,cta_click,/,2024-05-01 12:00:00,google.com\n",
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].Name != "cta_click" {
					t.Errorf("name = %q", evs[0].Name)
				}
				if evs[0].Properties["referrer"] != "google.com" {
					t.Errorf("referrer = %v", evs[0].Properties["referrer"])
				}
			},
		},
		{
			name:      "missing session_id skipped",
			input:     "session_id,event_name,created_at\n,,2024-05-01 12:00:00\n",
			wantSkips: map[string]int{"missing session_id": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, skips := collectMapped(t, MapUmami, tt.input)
			if len(evs) != tt.wantN {
				t.Fatalf("mapped %d events, want %d", len(evs), tt.wantN)
			}
			checkSkips(t, skips, tt.wantSkips)
			if tt.check != nil {
				tt.check(t, evs)
			}
		})
	}
}

func TestMapMixpanel(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantN     int
		wantSkips map[string]int
		check     func(t *testing.T, evs []event.Event)
	}{
		{
			name:  "name/time/distinct_id lifted out of properties, unix seconds parsed",
			input: `{"event":"Signed up","properties":{"time":1709287200,"distinct_id":"u1","$insert_id":"ins1","plan":"pro"}}`,
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				e := evs[0]
				if e.Name != "Signed up" || e.DistinctID != "u1" || e.ID != "ins1" {
					t.Errorf("fields not lifted from properties: %+v", e)
				}
				if !e.Timestamp.Equal(time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)) {
					t.Errorf("unix time not parsed: %v", e.Timestamp)
				}
				if e.Properties["plan"] != "pro" {
					t.Errorf("extra property lost: %v", e.Properties)
				}
				// the lifted keys must NOT linger in properties
				if _, ok := e.Properties["distinct_id"]; ok {
					t.Errorf("distinct_id leaked into properties: %v", e.Properties)
				}
				if _, ok := e.Properties["time"]; ok {
					t.Errorf("time leaked into properties: %v", e.Properties)
				}
			},
		},
		{
			name:  "numeric distinct_id keeps integer form, millis timestamp parsed",
			input: `{"event":"open","properties":{"time":1709287200000,"distinct_id":12345}}`,
			wantN: 1,
			check: func(t *testing.T, evs []event.Event) {
				if evs[0].DistinctID != "12345" {
					t.Errorf("numeric distinct_id = %q, want 12345 (no float form)", evs[0].DistinctID)
				}
				if !evs[0].Timestamp.Equal(time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)) {
					t.Errorf("millis time = %v", evs[0].Timestamp)
				}
			},
		},
		{
			name:      "a plain jsonl line is skipped — Mixpanel uses event/properties, not top-level name",
			input:     `{"name":"signup","distinct_id":"u1"}`,
			wantSkips: map[string]int{"missing event name": 1}, // no top-level "event" key
		},
		{
			name:      "missing event name skipped",
			input:     `{"properties":{"distinct_id":"u1"}}`,
			wantSkips: map[string]int{"missing event name": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, skips := collectMapped(t, MapMixpanel, tt.input)
			if len(evs) != tt.wantN {
				t.Fatalf("mapped %d events, want %d", len(evs), tt.wantN)
			}
			checkSkips(t, skips, tt.wantSkips)
			if tt.check != nil {
				tt.check(t, evs)
			}
		})
	}
}

func TestParseEventTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2024-03-01T10:00:00Z", time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC), true},
		{"2024-03-01T10:00:00.5+02:00", time.Date(2024, 3, 1, 8, 0, 0, 5e8, time.UTC), true},
		{"2024-03-01 10:00:00", time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC), true},
		{"2024-03-01 10:00:00.123456+00", time.Date(2024, 3, 1, 10, 0, 0, 123456000, time.UTC), true},
		{"2024-03-01", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), true},
		{"1709287200", time.Unix(1709287200, 0).UTC(), true},    // unix seconds
		{"1709287200000", time.Unix(1709287200, 0).UTC(), true}, // unix millis
		{"yesterday-ish", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := parseEventTime(tt.in)
		if ok != tt.ok {
			t.Errorf("parseEventTime(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && !got.Equal(tt.want) {
			t.Errorf("parseEventTime(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// round trip through the HTTP sender: jsonl fixture → Run → fake /v1/events.
// Verifies the ingest shape (JSON array of events), the bearer key, batch
// splitting, preserved timestamps, and the summary counts.
func TestRunHTTPRoundTrip(t *testing.T) {
	oldBatch := BatchSize
	BatchSize = 2 // force two POSTs from three events
	defer func() { BatchSize = oldBatch }()

	var batches [][]event.Event
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/events" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		auths = append(auths, r.Header.Get("Authorization"))
		var batch []event.Event
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("body is not a JSON array of events: %v", err)
		}
		batches = append(batches, batch)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(batch)})
	}))
	defer srv.Close()

	input := `{"id":"e1","name":"signup","distinct_id":"u1","timestamp":"2023-01-05T09:00:00Z"}` + "\n" +
		`{"id":"e2","name":"activate","distinct_id":"u1","timestamp":"2023-01-06T09:00:00Z"}` + "\n" +
		"not json\n" +
		`{"id":"e3","name":"checkout","distinct_id":"u2","timestamp":"2023-01-07T09:00:00Z","properties":{"value":9.5}}` + "\n"

	var out strings.Builder
	sum, err := Run("jsonl", false, strings.NewReader(input), NewHTTPSender(srv.URL, "sekret", &out))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Parsed != 3 || sum.Sent != 3 || sum.SkippedTotal() != 1 {
		t.Fatalf("summary = %+v, want parsed 3, sent 3, skipped 1", sum)
	}
	if sum.Skipped["invalid JSON line"] != 1 {
		t.Fatalf("skip reasons = %v", sum.Skipped)
	}
	if len(sum.Preview) != 3 || sum.Preview[0].ID != "e1" {
		t.Fatalf("preview = %+v, want the first 3 mapped events", sum.Preview)
	}
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("batches = %d, want 2 of sizes [2 1]", len(batches))
	}
	for _, a := range auths {
		if a != "Bearer sekret" {
			t.Errorf("Authorization = %q, want Bearer sekret", a)
		}
	}
	got := batches[0][0]
	if got.ID != "e1" || got.Name != "signup" || got.DistinctID != "u1" {
		t.Errorf("first event mangled: %+v", got)
	}
	if !got.Timestamp.Equal(time.Date(2023, 1, 5, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("historical timestamp not preserved: %v", got.Timestamp)
	}
	if !strings.Contains(out.String(), "sent 2 events (2 total)") || !strings.Contains(out.String(), "sent 1 events (3 total)") {
		t.Errorf("progress lines missing:\n%s", out.String())
	}
}

// a dry run parses everything and ships nothing — Sent stays 0, the preview is
// populated, and the sender is never touched.
func TestRunDryRun(t *testing.T) {
	sender := NewIngestSender(func([]event.Event) error {
		t.Error("dry run must not ingest")
		return nil
	})
	input := `{"name":"signup","distinct_id":"u1"}` + "\n" + "not json\n"
	sum, err := Run("jsonl", true, strings.NewReader(input), sender)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Parsed != 1 || sum.Sent != 0 || sum.Skipped["invalid JSON line"] != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if len(sum.Preview) != 1 || sum.Preview[0].Name != "signup" {
		t.Fatalf("preview = %+v", sum.Preview)
	}
}

// the ingest sender must split batches at the same count threshold as the HTTP
// sender and flush the remainder.
func TestIngestSenderBatching(t *testing.T) {
	oldBatch := BatchSize
	BatchSize = 2
	defer func() { BatchSize = oldBatch }()

	var batches [][]event.Event
	s := NewIngestSender(func(b []event.Event) error {
		batches = append(batches, b)
		return nil
	})
	input := `{"name":"a","distinct_id":"u1"}` + "\n" +
		`{"name":"b","distinct_id":"u1"}` + "\n" +
		`{"name":"c","distinct_id":"u2"}` + "\n"
	sum, err := Run("jsonl", false, strings.NewReader(input), s)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sent != 3 {
		t.Fatalf("sent = %d, want 3", sum.Sent)
	}
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("batches sizes wrong: %d", len(batches))
	}
	if batches[1][0].Name != "c" {
		t.Fatalf("last batch = %+v", batches[1])
	}
}

// an unknown format errors with the fix in the message before anything is read.
func TestRunUnknownFormat(t *testing.T) {
	_, err := Run("xml", false, strings.NewReader("x"), NewIngestSender(func([]event.Event) error { return nil }))
	if err == nil || !strings.Contains(err.Error(), "jsonl, csv, posthog, mixpanel or umami") {
		t.Fatalf("err = %v, want the format menu", err)
	}
}
