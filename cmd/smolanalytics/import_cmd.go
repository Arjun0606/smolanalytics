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
//
// The mappers and batching live in internal/importer, shared with the MCP
// import_events tool — this file is only the flags and the terminal summary.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/importer"
)

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

// runImport parses src and ships batches to host via the shared importer, then
// prints the CLI summary. Split from importCmd so tests can drive it against an
// httptest server.
func runImport(format, host, key string, dryRun bool, src io.Reader, out io.Writer) error {
	sum, err := importer.Run(format, dryRun, src, importer.NewHTTPSender(host, key, out))
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, "dry run: parsed %d, skipped %d, would send %d — nothing was sent\n", sum.Parsed, sum.SkippedTotal(), sum.Parsed)
	} else {
		fmt.Fprintf(out, "import complete: parsed %d, skipped %d, sent %d\n", sum.Parsed, sum.SkippedTotal(), sum.Sent)
	}
	reasons := make([]string, 0, len(sum.Skipped))
	for r := range sum.Skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons) // stable summary order
	for _, r := range reasons {
		fmt.Fprintf(out, "  skipped %d: %s\n", sum.Skipped[r], r)
	}
	if dryRun && len(sum.Preview) > 0 {
		fmt.Fprintf(out, "first %d mapped events:\n", len(sum.Preview))
		for _, e := range sum.Preview {
			b, _ := json.Marshal(e)
			fmt.Fprintf(out, "  %s\n", b)
		}
	}
	return nil
}
