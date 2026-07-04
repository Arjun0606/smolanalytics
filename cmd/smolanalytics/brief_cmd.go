package main

// `smolanalytics brief` — the morning "what to fix" digest, self-hosted. The cloud
// delivers this by email/Slack; here the same verdict engine prints it on demand,
// so cron + this command is the delivered brief without a server obligation:
//
//	0 8 * * * smolanalytics brief | mail -s "analytics brief" you@example.com
//	0 8 * * * smolanalytics brief --webhook=https://hooks.slack.com/services/...
//
// It reads the SAME durable log `serve` persists to (SMOLANALYTICS_DB / cold tier),
// through the same alias map, so the numbers match the dashboard exactly.
// The computation lives in internal/brief — shared with GET /v1/brief, so the CLI,
// the HTTP API, and the cloud's email can never disagree.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	alias2 "github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/brief"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store"
)

// aliases keep the existing tests (and any muscle memory) intact after the move
// of the computation into internal/brief.
type briefDigest = brief.Brief

func buildBrief(evs []event.Event, days int, now time.Time) briefDigest {
	return brief.Build(evs, days, now)
}

func formatBrief(b briefDigest) string { return brief.Format(b) }

func briefCmd(args []string) {
	fs := flag.NewFlagSet("brief", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the brief as a JSON object")
	webhookURL := fs.String("webhook", "", `POST the brief as {"text": ...} (Slack-incoming-webhook compatible)`)
	days := fs.Int("days", 7, "pulse window: compare the last N days against the N before")
	_ = fs.Parse(args)
	if *days <= 0 {
		log.Fatal("brief: --days must be at least 1")
	}

	st, closeStore, err := openServeStore()
	if err != nil {
		log.Fatal(err)
	}
	// same read path as serve: canonicalize ids through the alias map so stitched
	// visitors aren't double-counted.
	var rd store.Store = st
	if am, err := alias2.Open(dataPath() + ".aliases.json"); err == nil {
		rd = alias2.Wrap(st, am)
	}
	evs, err := rd.Range(time.Time{}, time.Time{})
	_ = closeStore()
	if err != nil {
		log.Fatal(err)
	}

	if len(evs) == 0 && !*asJSON {
		fmt.Println("no events yet")
		return
	}
	b := buildBrief(evs, *days, time.Now().UTC())
	switch {
	case *webhookURL != "":
		if err := postBrief(*webhookURL, formatBrief(b)); err != nil {
			log.Fatalf("brief: %v", err)
		}
		fmt.Println("sent")
	case *asJSON:
		out, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(out))
	default:
		fmt.Print(formatBrief(b))
	}
}

// postBrief delivers the brief as {"text": ...} — the shape Slack incoming webhooks
// (and most chat webhooks) accept as-is. Non-2xx is an error so cron surfaces it.
func postBrief(url, text string) error {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}
