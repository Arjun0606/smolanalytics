package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/importer"
)

// migrate_from: paste a key, get your history.
//
// import_events already existed and needed a FILE, which means finding the vendor's export UI,
// waiting on a job, downloading, putting the file on the server's own filesystem, and then knowing
// this tool exists. Five steps, each a place to stop. The parsers were never the missing part.
//
// Deliberately agent-first: this is exactly the errand a model should run for someone. It knows
// the date range from the conversation, it can dry-run, read back the real counts, and only then
// commit — which is a better migration UX than any form, and it costs one tool.

func init() {
	toolList = append(toolList, map[string]any{
		"name": "migrate_from",
		"description": "Pull history straight out of PostHog or Mixpanel with an API key — no export file, " +
			"no download. ALWAYS run with dry_run:true first and show the user the counts and the sample " +
			"events before importing for real; an import is easy to repeat and impossible to un-ring. " +
			"The key is used for this one pull and never stored. A date range is required. " +
			"Re-running the same range is safe: the vendor's own event ids come across, so events are " +
			"replaced rather than duplicated.",
		"inputSchema": obj(map[string]any{
			"vendor": map[string]any{"type": "string", "enum": []string{"posthog", "mixpanel"},
				"description": "Which tool to pull from."},
			"key": map[string]any{"type": "string",
				"description": "PostHog: a PERSONAL API key (Settings → Personal API Keys), not the project write key from the website snippet. Mixpanel: the project's API SECRET (Project Settings → Access Keys), not the project token. Never stored."},
			"project": map[string]any{"type": "string",
				"description": "PostHog only: the project id, the number in your PostHog URL at /project/<id>/."},
			"host": map[string]any{"type": "string",
				"description": "Optional host override for EU cloud or self-hosted (e.g. eu.i.posthog.com). Defaults to US cloud."},
			"from": map[string]any{"type": "string", "description": "Range start, YYYY-MM-DD. Required."},
			"to":   map[string]any{"type": "string", "description": "Range end, YYYY-MM-DD. Required."},
			"dry_run": map[string]any{"type": "boolean",
				"description": "Parse and count without importing. Do this first, every time."},
		}, []string{"vendor", "key", "from", "to"}),
	})
}

func (s *Server) callMigrate(name string, args json.RawMessage) (bool, string, error) {
	if name != "migrate_from" {
		return false, "", nil
	}
	var p struct {
		Vendor  string `json:"vendor"`
		Key     string `json:"key"`
		Project string `json:"project"`
		Host    string `json:"host"`
		From    string `json:"from"`
		To      string `json:"to"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := unmarshalArgs(args, &p); err != nil {
		return true, "", err
	}
	from, err := parseDay(p.From)
	if err != nil {
		return true, "", fmt.Errorf("from: %w", err)
	}
	to, err := parseDay(p.To)
	if err != nil {
		return true, "", fmt.Errorf("to: %w", err)
	}
	rem := importer.Remote{
		Vendor: p.Vendor, Key: p.Key, Project: p.Project, Host: p.Host, From: from, To: to,
	}
	if err := rem.Validate(); err != nil {
		return true, "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	body, err := rem.Fetch(ctx, nil)
	if err != nil {
		return true, "", err
	}
	defer body.Close()

	send := importer.NewIngestSender(func(batch []event.Event) error {
		// Imported history goes through the SAME normalisation as a file import — future-date
		// clamping, name trimming, the lot. A remote pull that skipped it would land events a file
		// import of the identical data would have corrected.
		s.normalizeImported(batch, time.Now().UTC())
		return s.store.Ingest(batch...)
	})
	sum, err := importer.Run(rem.Format(), p.DryRun, body, send)
	if err != nil {
		// Report what DID land before the failure. A migration that dies halfway and says only
		// "error" leaves someone unable to tell whether to retry the whole range or part of it.
		return true, "", fmt.Errorf("%w (parsed %d, imported %d before this)", err, sum.Parsed, sum.Sent)
	}

	out := map[string]any{
		"vendor": p.Vendor, "from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly),
		"parsed": sum.Parsed, "imported": sum.Sent, "dry_run": p.DryRun,
	}
	if n := sum.SkippedTotal(); n > 0 {
		out["skipped"] = sum.Skipped
		out["skipped_total"] = n
	}
	if len(sum.Preview) > 0 {
		out["sample"] = sum.Preview
	}
	switch {
	case sum.Parsed == 0:
		out["read"] = "the range came back empty. That is a fact about the range or the project, not a " +
			"failure — widen the dates, or check the project id and host (EU cloud and self-hosted are " +
			"on different domains from US cloud)."
	case p.DryRun:
		out["read"] = fmt.Sprintf("dry run: %d events would be imported, nothing was written. Show the "+
			"user the sample and the counts, then run again with dry_run:false.", sum.Parsed)
	default:
		out["read"] = fmt.Sprintf("%d events imported. Re-running this exact range is safe — the vendor's "+
			"event ids came with them, so a repeat replaces rather than duplicates.", sum.Sent)
	}
	txt, err := jsonText(out)
	if err != nil {
		return true, "", err
	}
	return true, txt, nil
}

// parseDay takes a plain date. Times are rejected rather than silently truncated: a range given
// to the hour reads as precision the vendor APIs do not honour (Mixpanel's export is day-grained),
// and quietly dropping it would make the report claim a window nobody actually got.
func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("required, as YYYY-MM-DD")
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want YYYY-MM-DD, got %q", s)
	}
	return t.UTC(), nil
}
