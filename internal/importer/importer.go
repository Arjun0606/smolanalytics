// Package importer brings history over from another tool so day one here isn't a
// zero dashboard. It holds the format mappers (jsonl, csv, posthog, umami) and the
// batching senders shared by the two import surfaces: the `smolanalytics import`
// CLI (batches POSTed to /v1/events) and the MCP import_events tool (batches
// ingested straight into the running server's store). One parser, one batcher —
// the two paths cannot drift.
package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// BatchSize is events per batch — half the server's 10k batch cap. A var so tests
// can shrink it to exercise multi-batch sends.
var BatchSize = 5000

// maxBody flushes a batch early when its encoded size nears the server's 4MB
// request cap (headroom left for the array brackets and commas).
const maxBody = 3 << 20

// Sender ships mapped events in batches. Add queues one event (flushing when the
// batch is full) and Flush ships whatever remains; both return how many events
// that call actually shipped.
type Sender interface {
	Add(event.Event) (int, error)
	Flush() (int, error)
}

// Summary is what one import run did — the exact counts, never estimates.
type Summary struct {
	Parsed  int            // rows mapped to events
	Skipped map[string]int // rows dropped, counted per reason
	Sent    int            // events actually shipped (0 on a dry run)
	Preview []event.Event  // the first 3 mapped events, for dry-run eyeballing
}

// SkippedTotal sums the per-reason skip counts.
func (s Summary) SkippedTotal() int {
	n := 0
	for _, c := range s.Skipped {
		n += c
	}
	return n
}

// Run parses src with the format's mapper and ships batches through send. On
// dryRun it parses and validates only — send is never called. A send error aborts
// the run (batches already shipped stay shipped); a bad row never does.
func Run(format string, dryRun bool, src io.Reader, send Sender) (Summary, error) {
	mapper, err := MapperFor(format)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Skipped: map[string]int{}}
	err = mapper(src, func(e event.Event) error {
		sum.Parsed++
		if len(sum.Preview) < 3 {
			sum.Preview = append(sum.Preview, e)
		}
		if dryRun {
			return nil
		}
		n, err := send.Add(e)
		sum.Sent += n
		return err
	}, func(reason string) {
		sum.Skipped[reason]++
	})
	if err != nil {
		return sum, err
	}
	if !dryRun {
		n, err := send.Flush()
		sum.Sent += n
		if err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// HTTPSender accumulates events and POSTs them to /v1/events, flushing on count
// or on approximate body size (the server rejects requests over 4MB). This is the
// CLI's sender; progress lines go to out.
type HTTPSender struct {
	url, key string
	out      io.Writer
	client   *http.Client
	buf      []json.RawMessage
	bufBytes int
	total    int
}

func NewHTTPSender(host, key string, out io.Writer) *HTTPSender {
	return &HTTPSender{
		url:    strings.TrimRight(host, "/") + "/v1/events",
		key:    key,
		out:    out,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Add queues one event, flushing when the batch is full. Returns how many events
// that flush sent (0 when it only queued).
func (s *HTTPSender) Add(e event.Event) (int, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	s.buf = append(s.buf, b)
	s.bufBytes += len(b) + 1
	if len(s.buf) >= BatchSize || s.bufBytes >= maxBody {
		return s.Flush()
	}
	return 0, nil
}

// Flush POSTs the queued batch. A rejected batch aborts the import; batches already
// sent stay stored, so a re-run only avoids duplicates for the jsonl format (ids
// are preserved there and the server dedupes on id).
func (s *HTTPSender) Flush() (int, error) {
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

// IngestSender batches events for a direct in-process ingest — the same flush
// thresholds as the HTTP sender (batch count + approximate encoded size), minus
// the HTTP hop. The MCP import_events tool uses it to write the server's own
// store, so both import paths ship identical batches.
type IngestSender struct {
	ingest   func([]event.Event) error
	buf      []event.Event
	bufBytes int
}

func NewIngestSender(ingest func([]event.Event) error) *IngestSender {
	return &IngestSender{ingest: ingest}
}

func (s *IngestSender) Add(e event.Event) (int, error) {
	b, err := json.Marshal(e) // size accounting only — keeps batch boundaries identical to the HTTP path
	if err != nil {
		return 0, err
	}
	s.buf = append(s.buf, e)
	s.bufBytes += len(b) + 1
	if len(s.buf) >= BatchSize || s.bufBytes >= maxBody {
		return s.Flush()
	}
	return 0, nil
}

func (s *IngestSender) Flush() (int, error) {
	if len(s.buf) == 0 {
		return 0, nil
	}
	if err := s.ingest(s.buf); err != nil {
		return 0, err
	}
	n := len(s.buf)
	s.buf, s.bufBytes = nil, 0 // fresh slice — the ingest callback may still hold the old one
	return n, nil
}
