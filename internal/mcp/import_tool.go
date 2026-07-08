package mcp

// The import tool — "bring my PostHog/Umami/CSV history in" without leaving the
// editor. Same mappers and batching as the `smolanalytics import` CLI (they share
// internal/importer), but events land directly in this server's store, normalized
// exactly like POST /v1/events would. The file is read from the SERVER's disk;
// row data never travels through the tool response — only the computed summary.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/importer"
)

// SetAliases attaches the identity-stitching map so imported $identify events
// record anon→user exactly like the HTTP ingest path does.
func (s *Server) SetAliases(a *alias.Map) { s.aliases = a }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "import_events",
			"description": "Import historical events from another tool into this instance. `path` must be a file on the MACHINE THE SERVER RUNS ON — this tool reads server-local files; it cannot receive file contents through the conversation. Formats: jsonl (this instance's own /v1/export shape — ids preserved, so re-runs are idempotent), csv (generic: name/event + distinct_id/user_id columns), posthog (events CSV export), mixpanel (Raw Event Export JSONL: name/time/distinct_id live inside properties; $insert_id preserved so re-runs are idempotent), umami (website_event CSV export). Rows that can't be mapped are skipped and counted per reason; one bad row never aborts the import. ALWAYS run with dry_run=true first: it parses everything, writes nothing, and returns the first 3 mapped events to eyeball. Returns the summary only — row data is never streamed back through the conversation.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format":  map[string]any{"type": "string", "enum": []string{"jsonl", "csv", "posthog", "mixpanel", "umami"}, "description": "The source file's format"},
					"path":    map[string]any{"type": "string", "description": "Path to the export file on the server's machine"},
					"dry_run": map[string]any{"type": "boolean", "description": "Parse and validate only; write nothing (do this first)"},
				},
				"required": []string{"format", "path"},
			},
		},
	)
}

func (s *Server) callImport(name string, args json.RawMessage) (bool, string, error) {
	if name != "import_events" {
		return false, "", nil
	}
	var p struct {
		Format string `json:"format"`
		Path   string `json:"path"`
		DryRun bool   `json:"dry_run"`
	}
	if err := unmarshalArgs(args, &p); err != nil {
		return true, "", err
	}
	if p.Format == "" {
		return true, "", fmt.Errorf("format is required — one of jsonl, csv, posthog, mixpanel, umami")
	}
	if p.Path == "" {
		return true, "", fmt.Errorf("path is required — the export file's location on the machine the smolanalytics server runs on")
	}
	f, err := os.Open(p.Path)
	if err != nil {
		return true, "", fmt.Errorf("cannot open %q: %v — the path must exist on the machine the server runs on (this tool cannot read files from the client)", p.Path, err)
	}
	defer f.Close()

	send := importer.NewIngestSender(func(batch []event.Event) error {
		s.normalizeImported(batch, time.Now().UTC())
		return s.store.Ingest(batch...)
	})
	sum, err := importer.Run(p.Format, p.DryRun, f, send)
	if err != nil {
		return true, "", err
	}

	out := map[string]any{
		"dry_run":           p.DryRun,
		"parsed":            sum.Parsed,
		"skipped_total":     sum.SkippedTotal(),
		"skipped_by_reason": sum.Skipped,
	}
	if p.DryRun {
		out["would_send"] = sum.Parsed
		out["preview_first_3"] = sum.Preview
		out["note"] = "nothing was written — re-run with dry_run=false to import"
	} else {
		out["sent"] = sum.Sent
		out["note"] = "events with an already-stored id were deduped by the store; jsonl, mixpanel ($insert_id), and posthog/csv exports carrying a uuid/event_id are idempotent on re-run; umami and id-less csv rows get fresh ids, so re-running those duplicates them"
	}
	return true, jsonStr(out), nil
}

// normalizeImported mirrors POST /v1/events' normalization for the direct-store
// path, so an import lands identically whichever surface it came through: missing
// ids get one (idempotency and erasure need it), missing timestamps default to
// now, future timestamps are clamped (a broken export must not plant events in
// tomorrow's reports), and $identify events record identity stitching. Name and
// distinct_id are already guaranteed non-empty by the mappers.
func (s *Server) normalizeImported(batch []event.Event, now time.Time) {
	maxFuture := now.Add(time.Hour) // tolerate clock skew in the source data, no more
	for i := range batch {
		alias.RecordFrom(s.aliases, batch[i]) // $identify + $create_alias, same as HTTP ingest
		if batch[i].ID == "" {
			batch[i].ID = newEventID()
		}
		if batch[i].Timestamp.IsZero() {
			batch[i].Timestamp = now
		} else if batch[i].Timestamp.After(maxFuture) {
			batch[i].Timestamp = now
		}
	}
}

// newEventID matches the id shape the HTTP ingest path assigns (12 random bytes, hex).
func newEventID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
