package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Remote pulls history straight out of the tool someone is leaving, so migrating is a key and a
// date range instead of an export ticket.
//
// This is the whole switching cost. Exporting from PostHog or Mixpanel means finding the export
// UI, waiting on a job, downloading a file and then finding our importer — four steps, each of
// which is a place to give up. The parsers already existed; only the fetch was missing.
//
// It deliberately produces a STREAM in the vendor's own export format and hands it to the same
// mapper the file path uses. A second parser per vendor is a second place for "what counts as an
// event" to drift, and the two would disagree exactly when someone compared a file import against
// a live pull and found different numbers.

// The vendor key is used for the length of one request and never written anywhere: not to the
// event store, not to settings, not to the audit log. A migration credential is the most
// dangerous secret a user will ever hand this tool — it reads their entire analytics history —
// and the only safe place for it is nowhere.
type Remote struct {
	Vendor  string // "posthog" | "mixpanel"
	Key     string // API key/secret. Never persisted.
	Project string // PostHog project id
	Host    string // vendor host override — PostHog is commonly self-hosted or on EU cloud
	From    time.Time
	To      time.Time
}

// Validate refuses an unbounded pull.
//
// A date range is REQUIRED rather than defaulted. Defaulting to "everything" turns one mistyped
// call into a multi-year pull that burns the user's vendor rate limit and lands a pile of history
// they did not ask for — and on a metered plan here, bills them for it. Making the caller state
// the window means the size of the import is always something they chose.
func (r Remote) Validate() error {
	switch r.Vendor {
	case "posthog":
		if r.Project == "" {
			return fmt.Errorf("posthog needs a project id — it is the number in your PostHog URL, /project/<id>/")
		}
	case "mixpanel":
	default:
		return fmt.Errorf("unknown vendor %q (posthog or mixpanel)", r.Vendor)
	}
	if r.Key == "" {
		return fmt.Errorf("%s needs an API key; it is read once for this pull and never stored", r.Vendor)
	}
	if r.From.IsZero() || r.To.IsZero() {
		return fmt.Errorf("a date range is required (from and to) — an unbounded pull can take hours, "+
			"exhaust your %s rate limit and import years you did not ask for", r.Vendor)
	}
	if !r.To.After(r.From) {
		return fmt.Errorf("the range ends before it starts (from %s, to %s)",
			r.From.Format(time.DateOnly), r.To.Format(time.DateOnly))
	}
	return nil
}

// Format is the mapper this vendor's stream should be parsed with.
func (r Remote) Format() string {
	if r.Vendor == "posthog" {
		return "posthog-api" // their REST shape, not the CSV their UI exports
	}
	return "mixpanel" // /api/2.0/export emits exactly the JSONL MapMixpanel already reads
}

// Fetch opens the vendor's export stream. The caller closes it.
func (r Remote) Fetch(ctx context.Context, hc *http.Client) (io.ReadCloser, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute} // a raw export of months of history is slow by nature
	}
	if r.Vendor == "mixpanel" {
		return r.fetchMixpanel(ctx, hc)
	}
	return r.fetchPostHog(ctx, hc)
}

func (r Remote) host(def string) string {
	if r.Host == "" {
		return def
	}
	h := strings.TrimSuffix(r.Host, "/")
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "https://" + h
	}
	return h
}

// fetchMixpanel streams the Raw Event Export. It returns JSONL directly, so the body IS the
// stream — no buffering, which matters because a year of events does not fit in memory.
func (r Remote) fetchMixpanel(ctx context.Context, hc *http.Client) (io.ReadCloser, error) {
	q := url.Values{
		"from_date": {r.From.Format(time.DateOnly)},
		"to_date":   {r.To.Format(time.DateOnly)},
	}
	u := r.host("https://data.mixpanel.com") + "/api/2.0/export?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Mixpanel authenticates the export API with the SECRET as the basic-auth username and an
	// empty password. Sending it as a Bearer token returns 401 with no explanation, which is a
	// long afternoon if you have not hit it before.
	req.SetBasicAuth(r.Key, "")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching mixpanel: %w", err)
	}
	if err := checkStatus(resp, "mixpanel"); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// fetchPostHog pages their events API and re-emits it as one JSON stream.
//
// PostHog paginates with an opaque `next` URL and there is no bulk endpoint on a plain personal
// API key, so this walks pages and concatenates. The pipe means the mapper starts parsing while
// later pages are still in flight, instead of accumulating the whole history in memory first.
func (r Remote) fetchPostHog(ctx context.Context, hc *http.Client) (io.ReadCloser, error) {
	base := r.host("https://us.i.posthog.com")
	q := url.Values{
		"after":  {r.From.UTC().Format(time.RFC3339)},
		"before": {r.To.UTC().Format(time.RFC3339)},
		"limit":  {"1000"},
	}
	next := fmt.Sprintf("%s/api/projects/%s/events/?%s", base, url.PathEscape(r.Project), q.Encode())

	pr, pw := io.Pipe()
	go func() {
		enc := json.NewEncoder(pw)
		var pages int
		for next != "" {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			req.Header.Set("Authorization", "Bearer "+r.Key)
			resp, err := hc.Do(req)
			if err != nil {
				pw.CloseWithError(fmt.Errorf("reaching posthog: %w", err))
				return
			}
			if err := checkStatus(resp, "posthog"); err != nil {
				resp.Body.Close()
				pw.CloseWithError(err)
				return
			}
			var page struct {
				Next    string            `json:"next"`
				Results []json.RawMessage `json:"results"`
			}
			err = json.NewDecoder(resp.Body).Decode(&page)
			resp.Body.Close()
			if err != nil {
				pw.CloseWithError(fmt.Errorf("posthog returned something that is not a page of events: %w", err))
				return
			}
			for _, raw := range page.Results {
				if err := enc.Encode(raw); err != nil { // one JSON object per line
					pw.CloseWithError(err)
					return
				}
			}
			pages++
			// A runaway `next` loop would page forever against someone's rate limit. 1000 pages at
			// 1000 events is a million events, well past any sane one-shot migration.
			if pages >= 1000 {
				pw.CloseWithError(fmt.Errorf(
					"stopped after %d pages — narrow the date range and import in chunks", pages))
				return
			}
			next = page.Next
		}
		pw.Close()
	}()
	return pr, nil
}

// checkStatus turns a vendor HTTP error into something that says what to DO. "401 Unauthorized"
// is true and useless; which credential, and where to get it, is the part that unblocks someone.
func checkStatus(resp *http.Response, vendor string) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 300 {
		snippet = snippet[:300] + "…"
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if vendor == "mixpanel" {
			return fmt.Errorf("mixpanel rejected the credential. The raw export API wants your project's "+
				"API SECRET (Project Settings → Access Keys), not a service-account token or the project "+
				"token that goes in your website snippet. [%s]", snippet)
		}
		return fmt.Errorf("posthog rejected the credential. It wants a PERSONAL API KEY (Settings → "+
			"Personal API Keys) scoped to read events, not the project write key from your snippet — "+
			"and the key must belong to an account with access to project %s. [%s]", "given", snippet)
	case http.StatusTooManyRequests:
		wait := resp.Header.Get("Retry-After")
		if wait == "" {
			wait = "a minute"
		} else if n, err := strconv.Atoi(wait); err == nil {
			wait = (time.Duration(n) * time.Second).String()
		}
		return fmt.Errorf("%s rate-limited this pull; wait %s and import a narrower date range", vendor, wait)
	case http.StatusNotFound:
		return fmt.Errorf("%s returned 404 — check the project id and the host "+
			"(EU cloud and self-hosted instances are on different domains). [%s]", vendor, snippet)
	}
	return fmt.Errorf("%s returned %s [%s]", vendor, resp.Status, snippet)
}

// MapPostHogAPI reads PostHog's REST shape, one JSON object per line:
//
//	{"id":"...","event":"$pageview","distinct_id":"u1","timestamp":"2026-01-01T00:00:00Z","properties":{…}}
//
// Separate from MapPostHog because that one reads the CSV their UI exports, and the two genuinely
// differ: the API nests properties as real JSON rather than a quoted string column, and carries
// `id` where the CSV carries `uuid`. Feeding one to the other silently maps zero rows.
func MapPostHogAPI(r io.Reader, emit EmitFn, skip SkipFn) error {
	dec := json.NewDecoder(r)
	for {
		var row struct {
			ID         string         `json:"id"`
			UUID       string         `json:"uuid"`
			Event      string         `json:"event"`
			DistinctID string         `json:"distinct_id"`
			Timestamp  string         `json:"timestamp"`
			Properties map[string]any `json:"properties"`
		}
		err := dec.Decode(&row)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Only malformed JSON is a bad row. Everything else — a 401 surfaced through the
			// pager's pipe, a dropped connection, a rate limit — is the TRANSPORT failing, and
			// answering that with "imported 0 events, no errors" would let someone believe their
			// history had moved. Propagate it.
			var se *json.SyntaxError
			var ue *json.UnmarshalTypeError
			if errors.As(err, &se) || errors.As(err, &ue) {
				skip("invalid JSON object")
				// A decoder cannot resynchronise after a syntax error, so stopping is honest:
				// continuing would import a prefix and report success.
				return nil
			}
			return err
		}
		if row.Event == "" {
			skip("missing event name")
			continue
		}
		id := row.DistinctID
		if id == "" {
			if v, ok := row.Properties["distinct_id"].(string); ok {
				id = v
			}
		}
		if id == "" {
			skip("missing distinct_id")
			continue
		}
		e := event.Event{Name: row.Event, DistinctID: id}
		// The vendor's own event id becomes ours, so re-running an import is idempotent rather
		// than doubling every number — the single most destructive way a migration can go wrong,
		// and the most likely, because the natural response to a partial import is to run it again.
		e.ID = row.ID
		if e.ID == "" {
			e.ID = row.UUID
		}
		if !takeTime(&e, row.Timestamp, skip) {
			continue
		}
		for k, v := range row.Properties {
			setProp(&e, k, v)
		}
		if err := emit(e); err != nil {
			return err
		}
	}
}
