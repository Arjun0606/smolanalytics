package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// mixedServer seeds users across two sources so a filter provably changes the
// answer — if a filters argument were silently dropped, the "filtered" result
// would be identical to the unfiltered one and these tests would catch it.
func mixedServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	base := time.Now().UTC().Add(-48 * time.Hour)
	ev := func(u, n, source string, off time.Duration) event.Event {
		return event.Event{ID: u + n + off.String(), DistinctID: u, Name: n, Timestamp: base.Add(off),
			Properties: map[string]any{"source": source, "plan": "pro", "path": "/"}}
	}
	_ = st.Ingest(
		ev("a", "signup", "google", 0), ev("a", "activate", "google", time.Hour),
		ev("b", "signup", "hn", 0), ev("b", "activate", "hn", time.Hour),
		ev("c", "signup", "hn", 0),
		ev("a", "$pageview", "google", time.Minute), ev("b", "$pageview", "hn", time.Minute), ev("c", "$pageview", "hn", time.Minute),
	)
	return New(st)
}

func toolResult(t *testing.T, s *Server, tool, args string) (string, bool) {
	t.Helper()
	r := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+`}}`)
	return toolText(t, r)
}

// The equality-map shorthand must compute the IDENTICAL answer to the canonical
// array form — and the array form must provably filter (differ from unfiltered).
func TestFilterMapShorthandMatchesArray(t *testing.T) {
	s := mixedServer(t)
	cases := []struct {
		tool  string
		plain string // no filters
		array string // canonical filters array
		asMap string // equality-map shorthand
	}{
		{"funnel",
			`{"steps":["signup","activate"]}`,
			`{"steps":["signup","activate"],"filters":[{"property":"source","op":"eq","value":"hn"}]}`,
			`{"steps":["signup","activate"],"filters":{"source":"hn"}}`},
		{"trends",
			`{"event":"signup"}`,
			`{"event":"signup","filters":[{"property":"source","op":"eq","value":"hn"}]}`,
			`{"event":"signup","filters":{"source":"hn"}}`},
		{"breakdown",
			`{"event":"signup","property":"plan"}`,
			`{"event":"signup","property":"plan","filters":[{"property":"source","op":"eq","value":"hn"}]}`,
			`{"event":"signup","property":"plan","filters":{"source":"hn"}}`},
		{"web_overview",
			`{"days":30}`,
			`{"days":30,"filters":[{"property":"source","op":"eq","value":"hn"}]}`,
			`{"days":30,"filters":{"source":"hn"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			plain, isErr := toolResult(t, s, tc.tool, tc.plain)
			if isErr {
				t.Fatalf("unfiltered call errored: %s", plain)
			}
			arr, isErr := toolResult(t, s, tc.tool, tc.array)
			if isErr {
				t.Fatalf("array-filtered call errored: %s", arr)
			}
			if arr == plain {
				t.Fatalf("array filter did not change the answer — filter silently dropped: %s", arr)
			}
			mapped, isErr := toolResult(t, s, tc.tool, tc.asMap)
			if isErr {
				t.Fatalf("map-shorthand call errored: %s", mapped)
			}
			if mapped != arr {
				t.Fatalf("map shorthand must compute the identical answer to the array form:\narray: %s\nmap:   %s", arr, mapped)
			}
		})
	}
}

// A bare filter object (one canonical filter minus its array wrapper) must decode
// as that single filter — never as an equality map over keys named "property"/"op".
func TestFilterBareObjectIsSingleFilter(t *testing.T) {
	s := mixedServer(t)
	arr, isErr := toolResult(t, s, "trends", `{"event":"signup","filters":[{"property":"source","op":"eq","value":"hn"}]}`)
	if isErr {
		t.Fatalf("array-filtered call errored: %s", arr)
	}
	bare, isErr := toolResult(t, s, "trends", `{"event":"signup","filters":{"property":"source","op":"eq","value":"hn"}}`)
	if isErr {
		t.Fatalf("bare filter object errored: %s", bare)
	}
	if bare != arr {
		t.Fatalf("bare filter object must equal its array form:\narray: %s\nbare:  %s", arr, bare)
	}
}

// Garbage filters shapes must come back as errors carrying the shape guide —
// never as unfiltered data presented as filtered.
func TestFilterGarbageShapeIsGuidingError(t *testing.T) {
	s := mixedServer(t)
	cases := []struct {
		tool, args, got string
	}{
		{"funnel", `{"steps":["signup","activate"],"filters":"source=hn"}`, "got a string"},
		{"trends", `{"event":"signup","filters":42}`, "got a number"},
		{"breakdown", `{"event":"signup","property":"plan","filters":{"source":{"op":"eq","value":"hn"}}}`, "an object"},
		{"web_overview", `{"filters":true}`, "got a boolean"},
		{"paths", `{"start":"signup","filters":[{"property":123}]}`, "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			text, isErr := toolResult(t, s, tc.tool, tc.args)
			if !isErr {
				t.Fatalf("garbage filters must be an error, got data: %s", text)
			}
			if !strings.Contains(text, "filters must be an array like") {
				t.Fatalf("error must lead with the shape guide: %s", text)
			}
			if !strings.Contains(text, tc.got) {
				t.Fatalf("error must name what was received (%s): %s", tc.got, text)
			}
		})
	}
}

// A mistyped non-filter argument must also be a self-correcting error naming the
// field — not silently ignored (an ignored steps field would mean "no funnel").
func TestMistypedArgumentIsError(t *testing.T) {
	s := mixedServer(t)
	cases := []struct {
		tool, args, want string
	}{
		{"funnel", `{"steps":"signup,activate"}`, `"steps" must be an array of strings`},
		{"retention", `{"event":"signup","days":"seven"}`, `"days" must be a number`},
		{"user_activity", `{"distinct_id":7}`, `"distinct_id" must be a string`},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			text, isErr := toolResult(t, s, tc.tool, tc.args)
			if !isErr {
				t.Fatalf("mistyped argument must be an error, got data: %s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("error must name the field and expected shape (%s): %s", tc.want, text)
			}
		})
	}
}
