package importer

// The format mappers: each turns one source's export file into our events, one
// row at a time. Rows that can't be mapped are skipped and counted per reason —
// one bad row never aborts an import. These moved here verbatim from the CLI
// `import` command so the MCP import_events tool maps files identically.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// EmitFn ships one mapped event; its error aborts the import (it means a send
// failed, not a bad row). SkipFn counts a row that couldn't be mapped.
type EmitFn func(event.Event) error
type SkipFn func(reason string)

// MapperFor picks the parser for a format value.
func MapperFor(format string) (func(io.Reader, EmitFn, SkipFn) error, error) {
	switch format {
	case "jsonl":
		return MapJSONL, nil
	case "csv":
		return MapCSV, nil
	case "posthog":
		return MapPostHog, nil
	case "mixpanel":
		return MapMixpanel, nil
	case "amplitude":
		return MapAmplitude, nil
	case "umami":
		return MapUmami, nil
	case "":
		return nil, fmt.Errorf("--format is required (jsonl, csv, posthog, mixpanel, amplitude or umami)")
	default:
		return nil, fmt.Errorf("unknown format %q (want jsonl, csv, posthog, mixpanel, amplitude or umami)", format)
	}
}

// MapJSONL reads our own export format: one /v1/events-shaped JSON object per line
// (GET /v1/export?format=jsonl). Ids are kept, so re-importing is idempotent.
func MapJSONL(r io.Reader, emit EmitFn, skip SkipFn) error {
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

// MapCSV reads a generic CSV: header row, a name (or event) column, a distinct_id
// (or user_id / anonymous_id) column, an optional time (or timestamp) column, and
// every other column lands as a string property.
func MapCSV(r io.Reader, emit EmitFn, skip SkipFn) error {
	cr, hdr, err := openCSV(r)
	if err != nil {
		return err
	}
	nameIdx := findCol(hdr, "name", "event")
	idIdx := findCol(hdr, "distinct_id", "user_id", "anonymous_id")
	timeIdx := findCol(hdr, "time", "timestamp")
	eidIdx := findCol(hdr, "event_id", "uuid", "$insert_id") // a stable event id → idempotent re-import
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
		e.ID = cell(row, eidIdx) // "" when absent → store assigns one
		for i, h := range hdr {
			if i == nameIdx || i == idIdx || i == timeIdx || i == eidIdx {
				continue
			}
			if v := cell(row, i); v != "" {
				setProp(&e, h, v)
			}
		}
		return emit(e)
	})
}

// MapPostHog reads PostHog's events CSV export (Activity → Export). Properties
// travel either as one embedded-JSON "properties" column or flattened into
// "properties.$browser"-style columns — both land as event properties here.
func MapPostHog(r io.Reader, emit EmitFn, skip SkipFn) error {
	cr, hdr, err := openCSV(r)
	if err != nil {
		return err
	}
	nameIdx := findCol(hdr, "event")
	idIdx := findCol(hdr, "distinct_id")
	timeIdx := findCol(hdr, "timestamp")
	propsIdx := findCol(hdr, "properties")
	uuidIdx := findCol(hdr, "uuid", "$insert_id") // PostHog stamps each event a uuid
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
		e.ID = cell(row, uuidIdx) // "" when absent → store assigns one; present → re-import is idempotent
		// flat extra columns first, then the JSON column, so its typed values win
		for i, h := range hdr {
			if i == nameIdx || i == idIdx || i == timeIdx || i == propsIdx || i == uuidIdx {
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

// MapMixpanel reads Mixpanel's Raw Event Export (JSONL): one object per line shaped
//
//	{"event":"Signed up","properties":{"time":1704067200,"distinct_id":"u1","$insert_id":"z",...}}
//
// Unlike our own JSONL, the name/id/time live INSIDE properties and time is a unix stamp,
// so feeding a Mixpanel export to --format=jsonl silently drops every row (no top-level
// name). $insert_id becomes the event id, so re-importing the same export is idempotent.
func MapMixpanel(r io.Reader, emit EmitFn, skip SkipFn) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			Event      string         `json:"event"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			skip("invalid JSON line")
			continue
		}
		if row.Event == "" {
			skip("missing event name")
			continue
		}
		props := row.Properties
		if props == nil {
			props = map[string]any{}
		}
		e := event.Event{Name: row.Event, DistinctID: mpStr(props["distinct_id"])}
		if e.DistinctID == "" {
			skip("missing distinct_id")
			continue
		}
		if v, ok := props["time"]; ok {
			t, ok := mixpanelTime(v)
			if !ok {
				skip("unparseable timestamp")
				continue
			}
			e.Timestamp = t
		}
		e.ID = mpStr(props["$insert_id"]) // "" is fine — the store then assigns one
		// everything except the fields we lifted into first-class columns becomes a property
		for k, v := range props {
			switch k {
			case "distinct_id", "time", "$insert_id":
				continue
			}
			setProp(&e, k, v)
		}
		if err := emit(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

// MapAmplitude reads Amplitude's Export API output: JSON, one event object per line, shaped
//
//	{"event_type":"Signed Up","user_id":"u1","event_time":"2024-01-01 12:00:00.000","$insert_id":"z","event_properties":{...},"user_properties":{...}}
//
// Unlike Mixpanel, name/id/time are top-level: event_type→name, user_id (falling back to
// device_id then amplitude_id)→distinct_id, event_time (a space-separated stamp) →timestamp,
// $insert_id→event id so re-import is idempotent, and event+user properties merge into props.
// Amplitude's export is gzipped by default, so a raw .json.gz is auto-decompressed here.
func MapAmplitude(r io.Reader, emit EmitFn, skip SkipFn) error {
	r, err := maybeGunzip(r)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20) // amplitude rows carry both event + user properties
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			EventType       string         `json:"event_type"`
			UserID          any            `json:"user_id"`
			DeviceID        any            `json:"device_id"`
			AmplitudeID     any            `json:"amplitude_id"`
			EventTime       string         `json:"event_time"`
			InsertID        string         `json:"$insert_id"`
			EventProperties map[string]any `json:"event_properties"`
			UserProperties  map[string]any `json:"user_properties"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			skip("invalid JSON line")
			continue
		}
		if row.EventType == "" {
			skip("missing event name")
			continue
		}
		id := mpStr(row.UserID)
		if id == "" {
			id = mpStr(row.DeviceID)
		}
		if id == "" {
			id = mpStr(row.AmplitudeID)
		}
		if id == "" {
			skip("missing user id")
			continue
		}
		e := event.Event{Name: row.EventType, DistinctID: id, ID: row.InsertID}
		if row.EventTime != "" {
			t, ok := parseEventTime(row.EventTime)
			if !ok {
				skip("unparseable timestamp")
				continue
			}
			e.Timestamp = t
		}
		for k, v := range row.UserProperties {
			setProp(&e, k, v)
		}
		for k, v := range row.EventProperties { // event props win over user props on a key clash
			setProp(&e, k, v)
		}
		if err := emit(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

// maybeGunzip transparently decompresses when the stream starts with the gzip magic bytes,
// so an Amplitude .json.gz imports directly (its export is gzipped) while a plain .json still
// works. The peeked reader is returned so no bytes are lost.
func maybeGunzip(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(2)
	if err != nil {
		return br, nil // empty/short input: nothing to decompress, let the caller read it
	}
	if magic[0] == 0x1f && magic[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}

// mpStr renders a JSON scalar as the string our ids/properties use. JSON numbers decode
// to float64, so an integer distinct_id keeps its integer form ("12345", not "1.2345e+04").
func mpStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == math.Trunc(x) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// mixpanelTime reads properties.time — unix seconds (classic export) or millis (some
// exports), as a JSON number or a numeric string.
func mixpanelTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		n := int64(x)
		if n >= 1e12 {
			return time.UnixMilli(n).UTC(), true
		}
		return time.Unix(n, 0).UTC(), true
	case string:
		return parseEventTime(x)
	default:
		return time.Time{}, false
	}
}

// MapUmami reads Umami's website_event CSV export. Rows without an event_name are
// pageviews → "$pageview" with url_path as the "path" property (the exact shape our
// web view reads). session_id becomes distinct_id: Umami keeps no stable
// cross-session visitor id, so user-level reports treat each session as a user.
func MapUmami(r io.Reader, emit EmitFn, skip SkipFn) error {
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
func eachCSVRow(cr *csv.Reader, skip SkipFn, fn func(row []string) error) error {
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
func takeTime(e *event.Event, ts string, skip SkipFn) bool {
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
