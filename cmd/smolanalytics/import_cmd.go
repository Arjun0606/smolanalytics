package main

// `smolanalytics import` — bring history over from another tool so day one here
// isn't a zero dashboard.
//
//	smolanalytics import --format=jsonl --host=http://localhost:8080 --key=KEY events.jsonl
//	smolanalytics import --format=posthog --key=KEY --dry-run posthog-events.csv
//
// Formats: jsonl (our own /v1/export — ids preserved, so re-runs are idempotent),
// csv (generic), posthog (events CSV export), umami (website_event CSV export).
// Rows that can't be mapped are skipped and counted per reason — one bad row never
// aborts the import. docs/migration.md has the per-source export walkthroughs.

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// importBatch is events per POST — half the server's 10k batch cap. A var so tests
// can shrink it to exercise multi-batch sends.
var importBatch = 5000

// importMaxBody flushes a batch early when its encoded size nears the server's 4MB
// request cap (headroom left for the array brackets and commas).
const importMaxBody = 3 << 20

func importCmd(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	format := fs.String("format", "", "source format: jsonl | csv | posthog | umami")
	host := fs.String("host", "http://localhost:8080", "smolanalytics server to import into")
	key := fs.String("key", "", "write key (sent as Authorization: Bearer)")
	dryRun := fs.Bool("dry-run", false, "parse and validate only; print the summary, send nothing")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: smolanalytics import --format=jsonl|csv|posthog|umami [--host=URL] [--key=KEY] [--dry-run] FILE")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		log.Fatalf("import: %v", err)
	}
	defer f.Close()
	if err := runImport(*format, *host, *key, *dryRun, f, os.Stdout); err != nil {
		log.Fatalf("import: %v", err)
	}
}

// emit ships one mapped event; its error aborts the import (it means a send failed,
// not a bad row). skip counts a row that couldn't be mapped.
type emitFn func(event.Event) error
type skipFn func(reason string)

// runImport parses src with the format's mapper and ships batches to host. Split
// from importCmd so tests can drive it against an httptest server.
func runImport(format, host, key string, dryRun bool, src io.Reader, out io.Writer) error {
	mapper, err := mapperFor(format)
	if err != nil {
		return err
	}
	parsed, sent := 0, 0
	skipped := map[string]int{}
	var preview []event.Event
	send := newBatchSender(host, key, out)

	err = mapper(src, func(e event.Event) error {
		parsed++
		if len(preview) < 3 {
			preview = append(preview, e)
		}
		if dryRun {
			return nil
		}
		n, err := send.add(e)
		sent += n
		return err
	}, func(reason string) {
		skipped[reason]++
	})
	if err != nil {
		return err
	}
	if !dryRun {
		n, err := send.flush()
		sent += n
		if err != nil {
			return err
		}
	}

	skipTotal := 0
	for _, c := range skipped {
		skipTotal += c
	}
	if dryRun {
		fmt.Fprintf(out, "dry run: parsed %d, skipped %d, would send %d — nothing was sent\n", parsed, skipTotal, parsed)
	} else {
		fmt.Fprintf(out, "import complete: parsed %d, skipped %d, sent %d\n", parsed, skipTotal, sent)
	}
	reasons := make([]string, 0, len(skipped))
	for r := range skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons) // stable summary order
	for _, r := range reasons {
		fmt.Fprintf(out, "  skipped %d: %s\n", skipped[r], r)
	}
	if dryRun && len(preview) > 0 {
		fmt.Fprintf(out, "first %d mapped events:\n", len(preview))
		for _, e := range preview {
			b, _ := json.Marshal(e)
			fmt.Fprintf(out, "  %s\n", b)
		}
	}
	return nil
}

// mapperFor picks the parser for a --format value.
func mapperFor(format string) (func(io.Reader, emitFn, skipFn) error, error) {
	switch format {
	case "jsonl":
		return mapJSONL, nil
	case "csv":
		return mapCSV, nil
	case "posthog":
		return mapPostHog, nil
	case "umami":
		return mapUmami, nil
	case "":
		return nil, fmt.Errorf("--format is required (jsonl, csv, posthog or umami)")
	default:
		return nil, fmt.Errorf("unknown format %q (want jsonl, csv, posthog or umami)", format)
	}
}

// --- batching ---

// batchSender accumulates events and POSTs them to /v1/events, flushing on count
// or on approximate body size (the server rejects requests over 4MB).
type batchSender struct {
	url, key string
	out      io.Writer
	client   *http.Client
	buf      []json.RawMessage
	bufBytes int
	total    int
}

func newBatchSender(host, key string, out io.Writer) *batchSender {
	return &batchSender{
		url:    strings.TrimRight(host, "/") + "/v1/events",
		key:    key,
		out:    out,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// add queues one event, flushing when the batch is full. Returns how many events
// that flush sent (0 when it only queued).
func (s *batchSender) add(e event.Event) (int, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	s.buf = append(s.buf, b)
	s.bufBytes += len(b) + 1
	if len(s.buf) >= importBatch || s.bufBytes >= importMaxBody {
		return s.flush()
	}
	return 0, nil
}

// flush POSTs the queued batch. A rejected batch aborts the import; batches already
// sent stay stored, so a re-run only avoids duplicates for the jsonl format (ids
// are preserved there and the server dedupes on id).
func (s *batchSender) flush() (int, error) {
	if len(s.buf) == 0 {
		return 0, nil
	}
	body, err := json.Marshal(s.buf)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.key != "" {
		req.Header.Set("Authorization", "Bearer "+s.key)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("server rejected batch (%s): %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	n := len(s.buf)
	s.total += n
	s.buf, s.bufBytes = s.buf[:0], 0
	fmt.Fprintf(s.out, "  sent %d events (%d total)\n", n, s.total)
	return n, nil
}

// --- mappers ---

// mapJSONL reads our own export format: one /v1/events-shaped JSON object per line
// (GET /v1/export?format=jsonl). Ids are kept, so re-importing is idempotent.
func mapJSONL(r io.Reader, emit emitFn, skip skipFn) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20) // a single event can carry big properties
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e event.Event
		if err := json.Unmarshal(line, &e); err != nil {
			skip("invalid JSON line")
			continue
		}
		if e.Name == "" {
			skip("missing event name")
			continue
		}
		if e.DistinctID == "" {
			skip("missing distinct_id")
			continue
		}
		if err := emit(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

// mapCSV reads a generic CSV: header row, a name (or event) column, a distinct_id
// (or user_id / anonymous_id) column, an optional time (or timestamp) column, and
// every other column lands as a string property.
func mapCSV(r io.Reader, emit emitFn, skip skipFn) error {
	cr, hdr, err := openCSV(r)
	if err != nil {
		return err
	}
	nameIdx := findCol(hdr, "name", "event")
	idIdx := findCol(hdr, "distinct_id", "user_id", "anonymous_id")
	timeIdx := findCol(hdr, "time", "timestamp")
	if nameIdx < 0 {
		return fmt.Errorf("csv: no event-name column (want a header named name or event; got %s)", strings.Join(hdr, ", "))
	}
	if idIdx < 0 {
		return fmt.Errorf("csv: no user column (want a header named distinct_id, user_id or anonymous_id; got %s)", strings.Join(hdr, ", "))
	}
	return eachCSVRow(cr, skip, func(row []string) error {
		e := event.Event{Name: cell(row, nameIdx), DistinctID: cell(row, idIdx)}
		if e.Name == "" {
			skip("missing event name")
			return nil
		}
		if e.DistinctID == "" {
			skip("missing distinct_id")
			return nil
		}
		if !takeTime(&e, cell(row, timeIdx), skip) {
			return nil
		}
		for i, h := range hdr {
			if i == nameIdx || i == idIdx || i == timeIdx {
				continue
			}
			if v := cell(row, i); v != "" {
				setProp(&e, h, v)
			}
		}
		return emit(e)
	})
}

// mapPostHog reads PostHog's events CSV export (Activity → Export). Properties
// travel either as one embedded-JSON "properties" column or flattened into
// "properties.$browser"-style columns — both land as event properties here.
func mapPostHog(r io.Reader, emit emitFn, skip skipFn) error {
	cr, hdr, err := openCSV(r)
	if err != nil {
		return err
	}
	nameIdx := findCol(hdr, "event")
	idIdx := findCol(hdr, "distinct_id")
	timeIdx := findCol(hdr, "timestamp")
	propsIdx := findCol(hdr, "properties")
	if nameIdx < 0 || idIdx < 0 {
		return fmt.Errorf("posthog: need event and distinct_id columns (got %s)", strings.Join(hdr, ", "))
	}
	return eachCSVRow(cr, skip, func(row []string) error {
		e := event.Event{Name: cell(row, nameIdx), DistinctID: cell(row, idIdx)}
		if e.Name == "" {
			skip("missing event name")
			return nil
		}
		if e.DistinctID == "" {
			skip("missing distinct_id")
			return nil
		}
		if !takeTime(&e, cell(row, timeIdx), skip) {
			return nil
		}
		// flat extra columns first, then the JSON column, so its typed values win
		for i, h := range hdr {
			if i == nameIdx || i == idIdx || i == timeIdx || i == propsIdx {
				continue
			}
			if v := cell(row, i); v != "" {
				setProp(&e, strings.TrimPrefix(h, "properties."), v)
			}
		}
		if pj := cell(row, propsIdx); pj != "" {
			var props map[string]any
			if err := json.Unmarshal([]byte(pj), &props); err != nil {
				skip("unparseable properties JSON")
				return nil
			}
			for k, v := range props {
				setProp(&e, k, v)
			}
		}
		return emit(e)
	})
}

// mapUmami reads Umami's website_event CSV export. Rows without an event_name are
// pageviews → "$pageview" with url_path as the "path" property (the exact shape our
// web view reads). session_id becomes distinct_id: Umami keeps no stable
// cross-session visitor id, so user-level reports treat each session as a user.
func mapUmami(r io.Reader, emit emitFn, skip skipFn) error {
	cr, hdr, err := openCSV(r)
	if err != nil {
		return err
	}
	idIdx := findCol(hdr, "session_id")
	nameIdx := findCol(hdr, "event_name")
	timeIdx := findCol(hdr, "created_at")
	pathIdx := findCol(hdr, "url_path")
	refIdx := findCol(hdr, "referrer_domain")
	if idIdx < 0 {
		return fmt.Errorf("umami: no session_id column (got %s)", strings.Join(hdr, ", "))
	}
	return eachCSVRow(cr, skip, func(row []string) error {
		e := event.Event{Name: cell(row, nameIdx), DistinctID: cell(row, idIdx)}
		if e.DistinctID == "" {
			skip("missing session_id")
			return nil
		}
		if e.Name == "" {
			e.Name = "$pageview"
		}
		if !takeTime(&e, cell(row, timeIdx), skip) {
			return nil
		}
		if p := cell(row, pathIdx); p != "" {
			setProp(&e, "path", p)
		}
		if rd := cell(row, refIdx); rd != "" {
			setProp(&e, "referrer", rd) // bare domain; the web report reduces referrers to hosts anyway
		}
		for i, h := range hdr {
			if i == idIdx || i == nameIdx || i == timeIdx || i == pathIdx || i == refIdx {
				continue
			}
			if v := cell(row, i); v != "" {
				setProp(&e, h, v)
			}
		}
		return emit(e)
	})
}

// --- CSV + time helpers ---

// openCSV builds a reader that tolerates ragged rows (missing cells read as "")
// and returns the header lowercased/trimmed (BOM stripped) so column matching is
// case-insensitive.
func openCSV(r io.Reader) (*csv.Reader, []string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	hdr, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("missing CSV header row")
	}
	for i := range hdr {
		hdr[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(hdr[i], "\uFEFF")))
	}
	return cr, hdr, nil
}

// eachCSVRow drives a mapper over the data rows. Rows with CSV syntax errors are
// counted and skipped — a stray quote must not abort an import.
func eachCSVRow(cr *csv.Reader, skip skipFn, fn func(row []string) error) error {
	for {
		row, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if _, ok := err.(*csv.ParseError); ok {
				skip("malformed CSV row")
				continue
			}
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
}

// findCol returns the index of the first candidate present in the header, or -1.
// Candidate order is the preference order.
func findCol(hdr []string, names ...string) int {
	for _, n := range names {
		for i, h := range hdr {
			if h == n {
				return i
			}
		}
	}
	return -1
}

// cell returns row[i] trimmed, tolerating short rows and absent columns (i < 0).
func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// setProp allocates Properties lazily so an event with no extras marshals without
// an empty properties object.
func setProp(e *event.Event, k string, v any) {
	if e.Properties == nil {
		e.Properties = map[string]any{}
	}
	e.Properties[k] = v
}

// takeTime stamps e from a timestamp cell. Empty is fine (the server uses "now");
// an unparseable value skips the row — silently stamping history at import time
// would plant old events in today's reports.
func takeTime(e *event.Event, ts string, skip skipFn) bool {
	if ts == "" {
		return true
	}
	t, ok := parseEventTime(ts)
	if !ok {
		skip("unparseable timestamp")
		return false
	}
	e.Timestamp = t
	return true
}

// importTimeLayouts covers what real exports contain: RFC3339 (ours, PostHog's
// ISO) and the space-separated SQL form PostHog/Umami CSVs use, with or without
// fraction and zone.
var importTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

// parseEventTime also accepts unix seconds or milliseconds; >= 1e12 means millis
// (that boundary is year 33658 as seconds, 2001 as millis).
func parseEventTime(s string) (time.Time, bool) {
	for _, layout := range importTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n >= 1e12 {
			return time.UnixMilli(n).UTC(), true
		}
		return time.Unix(n, 0).UTC(), true
	}
	return time.Time{}, false
}
